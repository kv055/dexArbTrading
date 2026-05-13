#!/usr/bin/env bash
# Exit immediately on error, treat unset variables as errors, and propagate pipe failures.
set -Eeuo pipefail

# Resolve absolute paths so the script works regardless of where it is called from.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="${REPO_DIR:-$(cd "$SCRIPT_DIR/.." && pwd)}"
COMPOSE_FILE="${COMPOSE_FILE:-$REPO_DIR/docker-compose.yml}"
# Lock file used to prevent two concurrent redeploys from running at the same time.
LOCK_FILE="${LOCK_FILE:-/tmp/dexarbtrading-redeploy.lock}"

# --- Logging helpers ---

timestamp() {
  date '+%Y-%m-%dT%H:%M:%S%z'
}

log() {
  printf '[%s] %s\n' "$(timestamp)" "$*"
}

# Print a timestamped error message and exit with a non-zero status.
die() {
  log "$*"
  exit 1
}

# Abort with a clear message if a required external command is not installed.
require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    die "missing required command: $1"
  fi
}

# --- Error trap ---

# Called automatically by bash whenever a command fails; reports the line number.
on_error() {
  log "redeploy failed at line $1"
}

trap 'on_error $LINENO' ERR

# --- Prerequisite checks ---

# Ensure docker and flock are available before doing anything else.
require_cmd docker
require_cmd flock

# --- Concurrency lock ---

# Open the lock file on fd 9 and try to acquire an exclusive non-blocking lock.
# If another redeploy process is already holding the lock, exit cleanly instead
# of running a second deploy in parallel, which could corrupt container state.
exec 9>"$LOCK_FILE"
if ! flock -n 9; then
  log "another redeploy is already in progress"
  exit 0
fi

# --- Compose file sanity check ---

# Abort early if the compose file is missing rather than letting docker error later.
if [[ ! -f "$COMPOSE_FILE" ]]; then
  die "compose file not found: $COMPOSE_FILE"
fi

# --- Rolling update ---

# Pull the latest image tags from the registry (built by CI, not locally).
# This does NOT restart anything yet; it just downloads the new layers.
log "pulling pre-built images from registry"
docker compose -f "$COMPOSE_FILE" pull

# Recreate containers using the freshly pulled images.
# --remove-orphans cleans up containers for services no longer in the compose file.
log "recreating containers from new images"
docker compose -f "$COMPOSE_FILE" up -d --remove-orphans

log "redeploy finished successfully"
