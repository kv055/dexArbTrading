#!/usr/bin/env bash
# Best-effort removal of a staged plaintext environment bundle.
# Skips deletion while a detached activate for the same SHA is still running.
set -euo pipefail

: "${SSH_HOST:?SSH_HOST is required}"
: "${SSH_USER:?SSH_USER is required}"
: "${REPO_DIR:?REPO_DIR is required}"
: "${NEW_SHA:?NEW_SHA is required}"

SSH_IDENTITY="${SSH_IDENTITY:-$HOME/.ssh/id_ed25519}"

[[ "$REPO_DIR" == /* && "$REPO_DIR" != "/" ]] || {
  echo "REPO_DIR must be a specific absolute path" >&2
  exit 2
}
[[ "$NEW_SHA" =~ ^[0-9a-f]{40}$ ]] || {
  echo "NEW_SHA must be a full 40-character hexadecimal commit" >&2
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

# shellcheck disable=SC2029
ssh "${SSH_OPTS[@]}" "$SSH_USER@$SSH_HOST" \
  "REPO_DIR=$(printf '%q' "$REPO_DIR") NEW_SHA=$(printf '%q' "$NEW_SHA") bash -s" <<'REMOTE'
set -euo pipefail

[[ "$NEW_SHA" =~ ^[0-9a-f]{40}$ ]]
[[ "$REPO_DIR" == /* && "$REPO_DIR" != "/" ]]

stage_dir="$REPO_DIR/.deploy/staging/$NEW_SHA"
run_dir="$REPO_DIR/.deploy/runs/$NEW_SHA"
pid_file="$run_dir/pid"
result_file="$run_dir/result"

if [[ ! -f "$result_file" && -f "$pid_file" ]]; then
  pid="$(tr -d '[:space:]' <"$pid_file")"
  if [[ "$pid" =~ ^[0-9]+$ ]] && kill -0 "$pid" 2>/dev/null; then
    echo "skipping staging cleanup: deploy pid $pid still running"
    exit 0
  fi
fi

rm -rf "$stage_dir"
echo "removed $stage_dir"
REMOTE
