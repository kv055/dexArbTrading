#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="${REPO_DIR:-$(cd "$SCRIPT_DIR/.." && pwd)}"
REMOTE="${REMOTE:-origin}"
BRANCH="${BRANCH:-light_setting}"
COMPOSE_FILE="${COMPOSE_FILE:-$REPO_DIR/docker-compose.yml}"
LOCK_FILE="${LOCK_FILE:-/tmp/dexarbtrading-redeploy.lock}"

timestamp() {
  date '+%Y-%m-%dT%H:%M:%S%z'
}

log() {
  printf '[%s] %s\n' "$(timestamp)" "$*"
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    log "missing required command: $1"
    exit 1
  fi
}

require_cmd git
require_cmd docker
require_cmd flock

exec 9>"$LOCK_FILE"
if ! flock -n 9; then
  log "another redeploy is already in progress"
  exit 0
fi

if [[ ! -d "$REPO_DIR/.git" ]]; then
  log "repo directory is not a git repository: $REPO_DIR"
  exit 1
fi

if [[ ! -f "$COMPOSE_FILE" ]]; then
  log "compose file not found: $COMPOSE_FILE"
  exit 1
fi

current_branch="$(git -C "$REPO_DIR" rev-parse --abbrev-ref HEAD)"
if [[ "$current_branch" != "$BRANCH" ]]; then
  log "checked out branch is '$current_branch', expected '$BRANCH'"
  exit 1
fi

if [[ -n "$(git -C "$REPO_DIR" status --porcelain)" ]]; then
  log "working tree is dirty, refusing to deploy"
  exit 1
fi

log "fetching latest changes from $REMOTE/$BRANCH"
git -C "$REPO_DIR" fetch --prune "$REMOTE"

local_sha="$(git -C "$REPO_DIR" rev-parse HEAD)"
remote_sha="$(git -C "$REPO_DIR" rev-parse "$REMOTE/$BRANCH")"

if [[ "$local_sha" == "$remote_sha" ]]; then
  log "already up to date at $local_sha"
else
  log "pulling $REMOTE/$BRANCH"
  git -C "$REPO_DIR" pull --ff-only "$REMOTE" "$BRANCH"
fi

new_sha="$(git -C "$REPO_DIR" rev-parse HEAD)"
log "rebuilding docker compose stack from commit $new_sha"
docker compose -f "$COMPOSE_FILE" up --build -d --remove-orphans
log "redeploy finished successfully"
