#!/usr/bin/env bash
# Reconcile the permanent Gitea runner Compose stack on the homeserver.
#
# This script is invoked by gitea-runner-reconcile.timer and by setup-gitea.sh.
# It is deliberately credential-free: Gitea API verification remains on the
# provisioning machine, while this host-side check only manages Docker state.
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

COMPOSE_FILE="act-runner-compose.yml"
DISABLED_MARKER=".reconcile-disabled"
LOCK_FILE=".reconcile.lock"
STATE_FILE=".reconcile-state"
DISK_STATE_FILE=".reconcile-disk-state"

log() { printf '%s\n' "gitea-runner-reconcile: $*"; }

record_state() {
  local next="$1" message="$2" previous=""
  [[ -f "$STATE_FILE" ]] && previous="$(<"$STATE_FILE")"
  if [[ "$previous" != "$next" ]]; then
    printf '%s\n' "$next" > "$STATE_FILE"
    log "$message"
  fi
  return 0
}

record_disk_state() {
  local next="$1" message="$2" previous=""
  [[ -f "$DISK_STATE_FILE" ]] && previous="$(<"$DISK_STATE_FILE")"
  if [[ "$previous" != "$next" ]]; then
    printf '%s\n' "$next" > "$DISK_STATE_FILE"
    [[ -n "$message" ]] && log "$message"
  fi
  return 0
}

check_disk() {
  local used
  used="$(df -P /var/lib/docker 2>/dev/null | awk 'NR == 2 { gsub(/%/, "", $5); print $5 }')"
  [[ "$used" =~ ^[0-9]+$ ]] || return 0
  if (( used >= 95 )); then
    record_disk_state critical "CRITICAL: Docker filesystem is ${used}% full; runner cache cleanup is required"
  elif (( used >= 90 )); then
    record_disk_state warning "WARNING: Docker filesystem is ${used}% full; inspect runner build-cache usage"
  else
    record_disk_state normal ""
  fi
}

container_state() {
  local name="$1" running health
  running="$(docker inspect -f '{{.State.Running}}' "$name" 2>/dev/null || true)"
  [[ "$running" == "true" ]] || { printf '%s\n' missing; return; }
  health="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$name" 2>/dev/null || true)"
  printf '%s\n' "$health"
}

on_error() {
  local rc=$?
  trap - ERR
  record_state failed "ERROR: reconciliation command failed (exit=${rc})"
  exit "$rc"
}
trap on_error ERR

exec 9>"$LOCK_FILE"
if ! flock -n 9; then
  exit 0
fi

if [[ -f "$DISABLED_MARKER" ]]; then
  record_state disabled "automatic recovery is intentionally disabled"
  exit 0
fi

if [[ ! -f "$COMPOSE_FILE" ]]; then
  record_state unprovisioned "runner files are not deployed yet; nothing to reconcile"
  exit 0
fi

if ! docker info >/dev/null 2>&1; then
  record_state failed "ERROR: host Docker daemon is unavailable"
  exit 1
fi

check_disk

dind_state="$(container_state gitea-runner-dind)"
runner_state="$(container_state gitea-act-runner)"

if [[ "$dind_state" == healthy && "$runner_state" == healthy ]]; then
  record_state healthy "runner stack is healthy"
  exit 0
fi

record_state repairing "repairing runner stack (dind=${dind_state}, runner=${runner_state})"

compose=(docker compose -f "$COMPOSE_FILE")
if [[ "$dind_state" == unhealthy ]]; then
  "${compose[@]}" up -d --force-recreate --remove-orphans --wait --wait-timeout 120 docker act_runner
elif [[ "$runner_state" == unhealthy ]]; then
  "${compose[@]}" up -d --force-recreate --remove-orphans --wait --wait-timeout 120 act_runner
else
  # Missing/stopped containers are recreated without disturbing a healthy
  # dependency or its persistent volumes.
  "${compose[@]}" up -d --remove-orphans --wait --wait-timeout 120
fi

dind_state="$(container_state gitea-runner-dind)"
runner_state="$(container_state gitea-act-runner)"
if [[ "$dind_state" != healthy || "$runner_state" != healthy ]]; then
  record_state failed "ERROR: recovery finished without healthy services (dind=${dind_state}, runner=${runner_state})"
  exit 1
fi

record_state healthy "runner stack recovered and is healthy"
