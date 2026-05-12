#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="${REPO_DIR:-$(cd "$SCRIPT_DIR/.." && pwd)}"
COMPOSE_FILE="${COMPOSE_FILE:-$REPO_DIR/docker-compose.yml}"
LOCK_FILE="${LOCK_FILE:-/tmp/dexarbtrading-redeploy.lock}"

timestamp() {
  date '+%Y-%m-%dT%H:%M:%S%z'
}

log() {
  printf '[%s] %s\n' "$(timestamp)" "$*"
}

die() {
  log "$*"
  exit 1
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    die "missing required command: $1"
  fi
}

on_error() {
  log "redeploy failed at line $1"
}

trap 'on_error $LINENO' ERR

require_cmd docker
require_cmd flock

exec 9>"$LOCK_FILE"
if ! flock -n 9; then
  log "another redeploy is already in progress"
  exit 0
fi

if [[ ! -f "$COMPOSE_FILE" ]]; then
  die "compose file not found: $COMPOSE_FILE"
fi

log "pulling pre-built images from registry"
docker compose -f "$COMPOSE_FILE" pull

log "recreating containers from new images"
docker compose -f "$COMPOSE_FILE" up -d --remove-orphans

log "redeploy finished successfully"
