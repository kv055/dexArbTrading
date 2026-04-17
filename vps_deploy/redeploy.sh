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

die() {
  log "$*"
  exit 1
}

git_repo() {
  git -C "$REPO_DIR" "$@"
}

ensure_clean_worktree() {
  if [[ -n "$(git_repo status --porcelain --ignore-submodules=none)" ]]; then
    die "$1"
  fi
}

on_error() {
  log "redeploy failed at line $1"
}

trap 'on_error $LINENO' ERR

require_cmd git
require_cmd docker
require_cmd flock

exec 9>"$LOCK_FILE"
if ! flock -n 9; then
  log "another redeploy is already in progress"
  exit 0
fi

if [[ ! -d "$REPO_DIR/.git" ]]; then
  die "repo directory is not a git repository: $REPO_DIR"
fi

if [[ ! -f "$COMPOSE_FILE" ]]; then
  die "compose file not found: $COMPOSE_FILE"
fi

current_branch="$(git_repo rev-parse --abbrev-ref HEAD)"
if [[ "$current_branch" != "$BRANCH" ]]; then
  die "checked out branch is '$current_branch', expected '$BRANCH'"
fi

ensure_clean_worktree "working tree is dirty, refusing to deploy"

log "fetching latest changes from $REMOTE/$BRANCH"
git_repo fetch --prune "$REMOTE"

if ! git_repo rev-parse --verify "$REMOTE/$BRANCH" >/dev/null 2>&1; then
  die "remote branch not found: $REMOTE/$BRANCH"
fi

local_sha="$(git_repo rev-parse HEAD)"
remote_sha="$(git_repo rev-parse "$REMOTE/$BRANCH")"

if [[ "$local_sha" == "$remote_sha" ]]; then
  log "already up to date at $local_sha"
else
  log "pulling $REMOTE/$BRANCH"
  git_repo pull --ff-only "$REMOTE" "$BRANCH"
fi

if [[ -f "$REPO_DIR/.gitmodules" ]]; then
  log "syncing and updating git submodules"
  git_repo submodule sync --recursive
  git_repo submodule update --init --recursive
fi

ensure_clean_worktree "working tree became dirty after pull/submodule update, refusing to continue"

new_sha="$(git_repo rev-parse HEAD)"
log "rebuilding docker compose stack from commit $new_sha"
docker compose -f "$COMPOSE_FILE" up --build -d --remove-orphans
log "redeploy finished successfully"
