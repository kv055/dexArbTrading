#!/usr/bin/env bash
# Exercises detached activate polling and safe staging cleanup without a VPS.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Keep both hosts' outer deadlines above the remote operation's default waits.
# Use the configured values so increasing a migration/health timeout cannot
# silently put rollback outside the CI polling window again.
python3 - "$repo_root" <<'PY'
import pathlib
import re
import sys

root = pathlib.Path(sys.argv[1])
activate = (root / 'deploy/ci-activate.sh').read_text()
transaction = (root / 'deploy/transactional-deploy.sh').read_text()

def seconds(pattern, text, label):
    match = re.search(pattern, text, re.M)
    assert match, f'cannot find {label} ({pattern})'
    return int(match[1])

poll = seconds(r'POLL_TIMEOUT_SEC:-([0-9]+)', activate, 'poll timeout')
migration = seconds(r'DEPLOY_MIGRATION_TIMEOUT_SEC:-([0-9]+)', transaction, 'migration timeout')
kill_grace = seconds(r'timeout --kill-after=([0-9]+)', transaction, 'migration kill grace')
migration_pg = seconds(r'^MIGRATION_PG_WAIT_SEC=([0-9]+)$', transaction, 'migration postgres wait')
reconcile_pg = seconds(r'^RECONCILE_PG_WAIT_SEC=([0-9]+)$', transaction, 'reconcile postgres wait')
reconcile_api = seconds(r'^RECONCILE_API_WAIT_SEC=([0-9]+)$', transaction, 'reconcile API wait')
stack = seconds(r'^STACK_WAIT_SEC=([0-9]+)$', transaction, 'stack health wait')
restart = seconds(r'^RESTART_WAIT_SEC=([0-9]+)$', transaction, 'config restart wait')
# Worst-case critical path: migration once, then the stack and restart waits
# for both candidate and rollback, plus the rollback-only role restoration,
# plus 20 minutes for smoke probes, pulls and other I/O.
minimum_poll = (migration_pg + migration + kill_grace
                + 2 * (stack + restart)
                + reconcile_pg + reconcile_api
                + 20 * 60)
assert poll >= minimum_poll, f'polling budget {poll}s is below remote waits/headroom {minimum_poll}s'

for name in ('.github/workflows/ci-cd.yml', '.gitea/workflows/ci.yml'):
    workflow = (root / name).read_text()
    deploy = re.search(r'^  deploy:\n(.*?)(?=^  [\w-]+:\n|\Z)', workflow, re.M | re.S)[1]
    job = int(re.search(r'^    timeout-minutes: ([0-9]+)$', deploy, re.M)[1]) * 60
    step = re.search(r'- name: Activate staged release transactionally\n\s+timeout-minutes: ([0-9]+)', deploy)
    activation = int(step[1]) * 60
    assert activation >= poll + 5 * 60, f'{name}: activation needs 5m beyond the poll deadline'
    assert job >= activation + 10 * 60, f'{name}: deploy job needs 10m for staging and cleanup'
print('ok deployment timeout budgets on GitHub and Gitea')
PY

fixture="$(mktemp -d)"
live_pid=""
cleanup() {
  if [[ -n "${live_pid:-}" ]]; then
    kill "$live_pid" 2>/dev/null || true
    wait "$live_pid" 2>/dev/null || true
  fi
  rm -rf "$fixture"
}
trap cleanup EXIT

fake_home="$fixture/home"
fake_bin="$fixture/bin"
fake_remote="$fixture/remote"
mkdir -p "$fake_home/.ssh" "$fake_bin" "$fake_remote"
chmod 700 "$fake_home/.ssh"
: >"$fake_home/.ssh/id_ed25519"
chmod 600 "$fake_home/.ssh/id_ed25519"

repo_dir="/opt/dexArbTrading"
sha="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
run_dir="$fake_remote$repo_dir/.deploy/runs/$sha"
stage_dir="$fake_remote$repo_dir/.deploy/staging/$sha"

mkdir -p "$stage_dir/env" "$run_dir"
chmod 700 \
  "$fake_remote$repo_dir/.deploy" \
  "$fake_remote$repo_dir/.deploy/runs" \
  "$fake_remote$repo_dir/.deploy/staging" \
  "$stage_dir" \
  "$run_dir"

cat >"$stage_dir/transactional-deploy.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
echo "fake deploy start"
sleep 0.2
echo "fake deploy done"
exit "${FAKE_DEPLOY_EXIT:-0}"
EOF
chmod 700 "$stage_dir/transactional-deploy.sh"

# Mock ssh written with paths baked in (quoted body, values substituted via env).
export TEST_FAKE_ROOT="$fake_remote"
export TEST_REAL_REPO="$repo_dir"
cat >"$fake_bin/ssh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
fake_root="${TEST_FAKE_ROOT:?}"
real_repo="${TEST_REAL_REPO:?}"

args=("$@")
remote_cmd=""
i=0
while (( i < ${#args[@]} )); do
  arg="${args[$i]}"
  case "$arg" in
    -i|-o)
      i=$((i + 2))
      ;;
    -*)
      i=$((i + 1))
      ;;
    *@*)
      # destination user@host
      i=$((i + 1))
      ;;
    *)
      remote_cmd="${args[*]:$i}"
      break
      ;;
  esac
done
[[ -n "$remote_cmd" ]] || exit 2

docker() { return 0; }
export -f docker

if [[ "$remote_cmd" == *'bash -s'* ]]; then
  prefix="${remote_cmd%% bash -s*}"
  eval "$prefix"
  # Remap absolute VPS paths into the fixture tree.
  if [[ -n "${REPO_DIR:-}" ]]; then
    REPO_DIR="$fake_root${REPO_DIR#"$fake_root"}"
  fi
  if [[ -n "${RUN_DIR:-}" ]]; then
    RUN_DIR="$fake_root${RUN_DIR#"$fake_root"}"
  fi
  # Run in this shell so VAR=value prefixes are visible (bash -c would drop them).
  eval "$(cat)"
else
  cmd="${remote_cmd//$real_repo/$fake_root$real_repo}"
  eval "$cmd"
fi
EOF
chmod 700 "$fake_bin/ssh"

cat >"$fake_bin/docker" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
chmod 700 "$fake_bin/docker"

export PATH="$fake_bin:$PATH"
export HOME="$fake_home"
export SSH_HOST=vps.example
export SSH_USER=deploy
export REPO_DIR="$repo_dir"
export NEW_SHA="$sha"
export GHCR_TOKEN=dummy-token
export GHCR_USER=dummy-user
export CHANGED_SERVICES=dexmonitor
export FETCH_ORIGIN=false
export POLL_INTERVAL_SEC=1
export POLL_TIMEOUT_SEC=30
export SSH_IDENTITY="$fake_home/.ssh/id_ed25519"
export FAKE_DEPLOY_EXIT=0

echo "== success path =="
bash "$repo_root/deploy/ci-activate.sh"
[[ -f "$run_dir/result" ]]
[[ "$(tr -d '[:space:]' <"$run_dir/result")" == "0" ]]
grep -q "fake deploy done" "$run_dir/deploy.log"

echo "== resume already-finished =="
bash "$repo_root/deploy/ci-activate.sh"

echo "== failure path =="
rm -f "$run_dir/result" "$run_dir/pid" "$run_dir/deploy.log" "$run_dir/wrapper.sh"
export FAKE_DEPLOY_EXIT=7
set +e
bash "$repo_root/deploy/ci-activate.sh"
ec=$?
set -e
[[ "$ec" -eq 7 ]]
[[ "$(tr -d '[:space:]' <"$run_dir/result")" == "7" ]]

echo "== retry failed result =="
export FAKE_DEPLOY_EXIT=0
bash "$repo_root/deploy/ci-activate.sh"
[[ "$(tr -d '[:space:]' <"$run_dir/result")" == "0" ]]
grep -q "fake deploy done" "$run_dir/deploy.log"

echo "== cleanup skips in-flight deploy =="
rm -f "$run_dir/result"
sleep 60 &
live_pid=$!
echo "$live_pid" >"$run_dir/pid"
mkdir -p "$stage_dir/env"
bash "$repo_root/deploy/ci-cleanup-staging.sh"
[[ -d "$stage_dir" ]]
kill "$live_pid" 2>/dev/null || true
wait "$live_pid" 2>/dev/null || true
live_pid=""

echo "== cleanup removes after result =="
echo 0 >"$run_dir/result"
bash "$repo_root/deploy/ci-cleanup-staging.sh"
[[ ! -e "$stage_dir" ]]

echo "ok deploy/ci-activate protocol"
