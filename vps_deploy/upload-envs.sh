#!/usr/bin/env bash
# Exit immediately on error, treat unset variables as errors, and propagate pipe failures.
set -Eeuo pipefail

# Resolve the repo root relative to this script so it works from any working directory.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

# --- Usage / help ---

usage() {
  cat <<'EOF'
Usage: upload-envs.sh user@host [remote_repo_dir]

Uploads the local .env files used by this project to the target server over SSH.

Arguments:
  user@host         SSH target, for example root@203.0.113.10
  remote_repo_dir   Optional remote repo path. Default: /opt/dexArbTrading

Example:
  ./vps_deploy/upload-envs.sh deploy@example.com
  ./vps_deploy/upload-envs.sh deploy@example.com /srv/dexArbTrading
EOF
}

# --- Logging helpers ---

log() {
  printf '[upload-envs] %s\n' "$*"
}

# Abort with a clear message if a required external command is not installed.
require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    printf 'Missing required command: %s\n' "$1" >&2
    exit 1
  fi
}

# --- Main logic ---

main() {
  # Parse arguments: SSH target is mandatory; remote path defaults to /opt/dexArbTrading.
  local target="${1:-}"
  local remote_repo_dir="${2:-/opt/dexArbTrading}"
  local remote_path local_path relative_dir relative_path

  # Show help if no target was provided or -h/--help was passed.
  if [[ -z "$target" || "$target" == "-h" || "$target" == "--help" ]]; then
    usage
    exit 0
  fi

  # Ensure scp and ssh are available before attempting any network operations.
  require_cmd scp
  require_cmd ssh

  # Full list of .env files this project uses, relative to the repo root.
  # Each service keeps its own .env alongside its source code.
  local env_files=(
    ".env"
    "dexFiatController/.env"
    "dexMmController/.env"
    "dexMonitor/.env"
    "dexUnifiedApi/.env"
  )

  # --- Remote directory pre-flight ---
  # Verify that every target directory already exists on the remote server before
  # uploading anything. This prevents silently writing .env files into wrong or
  # unexpected paths if the server hasn't been bootstrapped yet.
  for relative_dir in "" "dexFiatController" "dexMmController" "dexMonitor" "dexUnifiedApi"; do
    remote_path="$remote_repo_dir"
    if [[ -n "$relative_dir" ]]; then
      remote_path="$remote_repo_dir/$relative_dir"
    fi

    log "checking remote directory exists: $remote_path"
    if ! ssh "$target" "test -d '$remote_path'"; then
      printf 'Remote directory not found: %s\n' "$remote_path" >&2
      printf 'Refusing to upload .env files to an unexpected or incomplete server path.\n' >&2
      exit 1
    fi
  done

  # --- Local file pre-flight ---
  # Confirm every .env file exists locally before starting any uploads so we don't
  # leave the remote in a half-updated state if one file is missing.
  for relative_path in "${env_files[@]}"; do
    local_path="$REPO_DIR/$relative_path"
    if [[ ! -f "$local_path" ]]; then
      printf 'Missing local env file: %s\n' "$local_path" >&2
      exit 1
    fi
  done

  # --- Upload ---
  # Copy each .env file to its corresponding location on the remote server.
  for relative_path in "${env_files[@]}"; do
    local_path="$REPO_DIR/$relative_path"
    remote_path="$remote_repo_dir/$relative_path"
    log "uploading $(basename "$local_path") to $target:$remote_path"
    scp "$local_path" "$target:$remote_path"
  done

  # --- Secure remote permissions ---
  # Tighten permissions to 600 (owner read/write only) on all uploaded files in a
  # single SSH call. This prevents other users on the server from reading secrets.
  log "setting remote permissions to 600"
  ssh "$target" "\
chmod 600 \
'$remote_repo_dir/.env' \
'$remote_repo_dir/dexFiatController/.env' \
'$remote_repo_dir/dexMmController/.env' \
'$remote_repo_dir/dexMonitor/.env' \
'$remote_repo_dir/dexUnifiedApi/.env'"

  log "env upload complete"
}

main "$@"
