#!/usr/bin/env bash
# Start transactional-deploy detached on the VPS and poll a result file over
# short SSH sessions. Avoids false CI failures when a long-lived SSH pipe hangs
# after (or during) a successful remote deploy.
set -euo pipefail

: "${SSH_HOST:?SSH_HOST is required}"
: "${SSH_USER:?SSH_USER is required}"
: "${REPO_DIR:?REPO_DIR is required}"
: "${NEW_SHA:?NEW_SHA is required}"
: "${GHCR_TOKEN:?GHCR_TOKEN is required}"
: "${GHCR_USER:?GHCR_USER is required}"

CHANGED_SERVICES="${CHANGED_SERVICES:-}"
FETCH_ORIGIN="${FETCH_ORIGIN:-false}"
POLL_INTERVAL_SEC="${POLL_INTERVAL_SEC:-15}"
POLL_TIMEOUT_SEC="${POLL_TIMEOUT_SEC:-3600}"
SSH_IDENTITY="${SSH_IDENTITY:-$HOME/.ssh/id_ed25519}"

[[ "$REPO_DIR" == /* && "$REPO_DIR" != "/" ]] || {
  echo "REPO_DIR must be a specific absolute path" >&2
  exit 2
}
[[ "$NEW_SHA" =~ ^[0-9a-f]{40}$ ]] || {
  echo "NEW_SHA must be a full 40-character hexadecimal commit" >&2
  exit 2
}
case "$FETCH_ORIGIN" in
  true|false) ;;
  *)
    echo "FETCH_ORIGIN must be true or false" >&2
    exit 2
    ;;
esac
[[ "$POLL_INTERVAL_SEC" =~ ^[0-9]+$ && "$POLL_INTERVAL_SEC" -ge 1 ]] || {
  echo "POLL_INTERVAL_SEC must be a positive integer" >&2
  exit 2
}
[[ "$POLL_TIMEOUT_SEC" =~ ^[0-9]+$ && "$POLL_TIMEOUT_SEC" -ge 1 ]] || {
  echo "POLL_TIMEOUT_SEC must be a positive integer" >&2
  exit 2
}

SSH_OPTS=(
  -i "$SSH_IDENTITY"
  -o BatchMode=yes
  -o StrictHostKeyChecking=yes
  -o ServerAliveInterval=15
  -o ServerAliveCountMax=4
  -o ConnectTimeout=10
)

remote_ssh() {
  # shellcheck disable=SC2029
  ssh "${SSH_OPTS[@]}" "$SSH_USER@$SSH_HOST" "$@"
}

run_dir="$REPO_DIR/.deploy/runs/$NEW_SHA"
log_offset=0

q_repo="$(printf '%q' "$REPO_DIR")"
q_sha="$(printf '%q' "$NEW_SHA")"
q_changed="$(printf '%q' "$CHANGED_SERVICES")"
q_fetch="$(printf '%q' "$FETCH_ORIGIN")"
q_token="$(printf '%q' "$GHCR_TOKEN")"
q_user="$(printf '%q' "$GHCR_USER")"

echo "ensuring detached deploy for $NEW_SHA on $SSH_USER@$SSH_HOST"

# Start (or resume) the remote deploy. GHCR login happens in this short SSH
# session so the long-running deploy uses docker's credential store.
remote_ssh \
  "REPO_DIR=$q_repo NEW_SHA=$q_sha CHANGED_SERVICES=$q_changed FETCH_ORIGIN=$q_fetch GHCR_TOKEN=$q_token GHCR_USER=$q_user bash -s" <<'REMOTE'
set -euo pipefail

[[ "$NEW_SHA" =~ ^[0-9a-f]{40}$ ]]
[[ "$REPO_DIR" == /* && "$REPO_DIR" != "/" ]]

run_dir="$REPO_DIR/.deploy/runs/$NEW_SHA"
stage_dir="$REPO_DIR/.deploy/staging/$NEW_SHA"
deployer="$stage_dir/transactional-deploy.sh"
wrapper="$run_dir/wrapper.sh"

mkdir -p "$run_dir"
chmod 700 "$REPO_DIR/.deploy" "$REPO_DIR/.deploy/runs" "$run_dir"
exec 8>"$REPO_DIR/.deploy/stage.lock"
flock -n 8 || { echo 'another activation dispatch or stage publication is active' >&2; exit 1; }

if [[ -f "$run_dir/result" ]]; then
  prev="$(tr -d '[:space:]' <"$run_dir/result")"
  if [[ "$prev" == "0" ]]; then
    echo "remote deploy already finished (result present)"
    exit 0
  fi
  echo "previous deploy for this SHA failed (exit ${prev:-unknown}); retrying"
  rm -f "$run_dir/result" "$run_dir/result.tmp" "$run_dir/pid"
fi

if [[ -f "$run_dir/pid" ]]; then
  pid="$(tr -d '[:space:]' <"$run_dir/pid")"
  if [[ "$pid" =~ ^[0-9]+$ ]] && kill -0 "$pid" 2>/dev/null; then
    echo "remote deploy already running (pid $pid)"
    exit 0
  fi
fi

[[ -x "$deployer" ]] || {
  echo "staged deployer missing: $deployer" >&2
  exit 1
}
[[ -d "$stage_dir/env" ]] || {
  echo "staged environment bundle missing: $stage_dir/env" >&2
  exit 1
}

echo "$GHCR_TOKEN" | docker login ghcr.io -u "$GHCR_USER" --password-stdin >/dev/null

fetch_args=()
if [[ "$FETCH_ORIGIN" == true ]]; then
  fetch_args+=(--fetch-origin)
fi

cat >"$wrapper" <<'WRAP'
#!/usr/bin/env bash
set -euo pipefail
run_dir="$1"
deployer="$2"
repo_dir="$3"
new_sha="$4"
changed_services="$5"
shift 5
set +e
"$deployer" \
  --repo-dir "$repo_dir" \
  --sha "$new_sha" \
  --env-bundle "$repo_dir/.deploy/staging/$new_sha/env" \
  --changed-services "$changed_services" \
  "$@" \
  >"$run_dir/deploy.log" 2>&1
ec=$?
set -e
printf '%s\n' "$ec" >"$run_dir/result.tmp"
mv -f "$run_dir/result.tmp" "$run_dir/result"
exit "$ec"
WRAP
chmod 700 "$wrapper"

rm -f "$run_dir/result" "$run_dir/result.tmp"
: >"$run_dir/deploy.log"
nohup "$wrapper" "$run_dir" "$deployer" "$REPO_DIR" "$NEW_SHA" "$CHANGED_SERVICES" \
  "${fetch_args[@]}" \
  8>&- >/dev/null 2>&1 &
echo $! >"$run_dir/pid.tmp"
mv -f "$run_dir/pid.tmp" "$run_dir/pid"
echo "started remote deploy pid $(tr -d '[:space:]' <"$run_dir/pid")"
REMOTE

deadline=$((SECONDS + POLL_TIMEOUT_SEC))
echo "polling $run_dir (timeout ${POLL_TIMEOUT_SEC}s, interval ${POLL_INTERVAL_SEC}s)"

while true; do
  q_offset="$(printf '%q' "$log_offset")"
  poll="$(
    remote_ssh \
      "RUN_DIR=$(printf '%q' "$run_dir") LOG_OFFSET=$q_offset bash -s" <<'POLL'
set -euo pipefail
run_dir="$RUN_DIR"
log_offset="$LOG_OFFSET"
log_file="$run_dir/deploy.log"
pid_file="$run_dir/pid"
result_file="$run_dir/result"

status=running
pid=""
result=""
alive=0
if [[ -f "$result_file" ]]; then
  status=done
  result="$(tr -d '[:space:]' <"$result_file")"
fi
if [[ -f "$pid_file" ]]; then
  pid="$(tr -d '[:space:]' <"$pid_file")"
  if [[ "$pid" =~ ^[0-9]+$ ]] && kill -0 "$pid" 2>/dev/null; then
    alive=1
  fi
fi
if [[ "$status" != done && "$alive" -eq 0 ]]; then
  status=dead
fi

bytes=0
if [[ -f "$log_file" ]]; then
  bytes="$(wc -c <"$log_file" | tr -d '[:space:]')"
fi
if [[ "$bytes" =~ ^[0-9]+$ && "$log_offset" =~ ^[0-9]+$ ]] && (( bytes > log_offset )); then
  dd if="$log_file" bs=1 skip="$log_offset" count=$((bytes - log_offset)) 2>/dev/null || true
fi

printf 'CI_ACTIVATE_META status=%s alive=%s pid=%s result=%s bytes=%s\n' \
  "$status" "$alive" "${pid:-}" "${result:-}" "$bytes"
POLL
  )"

  meta="$(printf '%s\n' "$poll" | awk '/^CI_ACTIVATE_META / {line=$0} END {print line}')"
  body="$(printf '%s\n' "$poll" | awk '!/^CI_ACTIVATE_META / {print}')"
  if [[ -n "${body:-}" ]]; then
    printf '%s\n' "$body"
  fi

  [[ -n "${meta:-}" ]] || {
    echo "poll returned no metadata" >&2
    exit 1
  }

  status="$(printf '%s\n' "$meta" | sed -n 's/.* status=\([^ ]*\).*/\1/p')"
  result="$(printf '%s\n' "$meta" | sed -n 's/.* result=\([^ ]*\).*/\1/p')"
  bytes="$(printf '%s\n' "$meta" | sed -n 's/.* bytes=\([^ ]*\).*/\1/p')"
  [[ "$bytes" =~ ^[0-9]+$ ]] && log_offset="$bytes"

  if [[ "$status" == done ]]; then
    if [[ "$result" =~ ^[0-9]+$ ]]; then
      echo "remote deploy finished with exit code $result"
      exit "$result"
    fi
    echo "remote result file is invalid: '$result'" >&2
    exit 1
  fi

  if [[ "$status" == dead ]]; then
    echo "remote deploy process exited without writing a result file" >&2
    echo "check $run_dir/deploy.log and .deployed_sha on the VPS" >&2
    exit 1
  fi

  if (( SECONDS >= deadline )); then
    echo "timed out after ${POLL_TIMEOUT_SEC}s waiting for remote deploy $NEW_SHA" >&2
    echo "CI no longer depends on one long SSH session; check VPS state manually:" >&2
    echo "  cat $REPO_DIR/.deployed_sha" >&2
    echo "  cat $run_dir/result; tail -n 100 $run_dir/deploy.log" >&2
    exit 1
  fi

  sleep "$POLL_INTERVAL_SEC"
done
