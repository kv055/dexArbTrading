#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="${REPO_DIR:-$(cd "$SCRIPT_DIR/.." && pwd)}"
REMOTE="${REMOTE:-origin}"
BRANCH="${BRANCH:-light_setting}"
COMPOSE_FILE="${COMPOSE_FILE:-$REPO_DIR/docker-compose.yml}"
EXPECTED_REPO_FULL_NAME="${EXPECTED_REPO_FULL_NAME:-}"
PING_STATUS_FILE="${PING_STATUS_FILE:-/tmp/dexarb-webhook-last-ping}"
HOOK_EVENT="${HOOK_EVENT:-}"
HOOK_REF="${HOOK_REF:-}"
HOOK_REPOSITORY_FULL_NAME="${HOOK_REPOSITORY_FULL_NAME:-}"
HOOK_ZEN="${HOOK_ZEN:-}"

timestamp() {
  date '+%Y-%m-%dT%H:%M:%S%z'
}

log() {
  printf '[%s] %s\n' "$(timestamp)" "$*"
}

write_ping_status() {
  mkdir -p "$(dirname "$PING_STATUS_FILE")"
  cat >"$PING_STATUS_FILE" <<EOF
timestamp=$(timestamp)
event=${HOOK_EVENT}
repository_full_name=${HOOK_REPOSITORY_FULL_NAME}
zen=${HOOK_ZEN}
EOF
  log "recorded GitHub ping in $PING_STATUS_FILE"
}

handle_push() {
  if [[ -n "$EXPECTED_REPO_FULL_NAME" && "$HOOK_REPOSITORY_FULL_NAME" != "$EXPECTED_REPO_FULL_NAME" ]]; then
    log "ignoring push for repository '$HOOK_REPOSITORY_FULL_NAME', expected '$EXPECTED_REPO_FULL_NAME'"
    exit 0
  fi

  if [[ "$HOOK_REF" != "refs/heads/$BRANCH" ]]; then
    log "ignoring push for ref '$HOOK_REF', expected 'refs/heads/$BRANCH'"
    exit 0
  fi

  log "accepted GitHub push for $HOOK_REPOSITORY_FULL_NAME on $HOOK_REF"
  exec "$SCRIPT_DIR/redeploy.sh"
}

case "$HOOK_EVENT" in
  ping)
    write_ping_status
    ;;
  push)
    handle_push
    ;;
  *)
    log "ignoring unsupported GitHub event '$HOOK_EVENT'"
    ;;
esac
