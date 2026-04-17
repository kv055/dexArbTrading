#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEFAULT_ENV_CONFIG="$SCRIPT_DIR/config.env"
DEFAULT_YAML_CONFIG="$SCRIPT_DIR/config.yaml"

timestamp() {
  date '+%Y-%m-%dT%H:%M:%S%z'
}

log() {
  printf '[%s] %s\n' "$(timestamp)" "$*"
}

fail() {
  log "$*"
  exit 1
}

trim() {
  local value="$1"
  value="${value#"${value%%[![:space:]]*}"}"
  value="${value%"${value##*[![:space:]]}"}"
  printf '%s' "$value"
}

strip_quotes() {
  local value="$1"
  if [[ "$value" =~ ^\".*\"$ ]]; then
    value="${value:1:${#value}-2}"
  elif [[ "$value" =~ ^\'.*\'$ ]]; then
    value="${value:1:${#value}-2}"
  fi
  printf '%s' "$value"
}

load_env_config() {
  local path="$1"
  set -a
  # shellcheck disable=SC1090
  source "$path"
  set +a
}

load_yaml_config() {
  local path="$1"
  local line key raw_value value

  while IFS= read -r line || [[ -n "$line" ]]; do
    line="${line%%#*}"
    line="$(trim "$line")"
    [[ -z "$line" ]] && continue

    if [[ "$line" =~ ^([A-Za-z_][A-Za-z0-9_]*)[[:space:]]*:[[:space:]]*(.*)$ ]]; then
      key="${BASH_REMATCH[1]}"
      raw_value="${BASH_REMATCH[2]}"
      value="$(strip_quotes "$(trim "$raw_value")")"
      printf -v "$key" '%s' "$value"
      export "$key"
    else
      fail "unsupported yaml line in $path: $line"
    fi
  done <"$path"
}

load_config() {
  local path="${1:-}"

  if [[ -z "$path" ]]; then
    if [[ -f "$DEFAULT_ENV_CONFIG" ]]; then
      path="$DEFAULT_ENV_CONFIG"
    elif [[ -f "$DEFAULT_YAML_CONFIG" ]]; then
      path="$DEFAULT_YAML_CONFIG"
    else
      fail "no config file found. create $DEFAULT_ENV_CONFIG or $DEFAULT_YAML_CONFIG first"
    fi
  fi

  [[ -f "$path" ]] || fail "config file not found: $path"

  case "$path" in
    *.env) load_env_config "$path" ;;
    *.yaml|*.yml) load_yaml_config "$path" ;;
    *)
      fail "unsupported config format: $path"
      ;;
  esac

  CONFIG_PATH="$path"
}

apply_defaults() {
  REPO_DIR="${REPO_DIR:-$(cd "$SCRIPT_DIR/.." && pwd)}"
  BRANCH="${BRANCH:-light_setting}"
  WEBHOOK_PORT="${WEBHOOK_PORT:-9000}"
  HOOK_ID="${HOOK_ID:-dexarb-redeploy}"
  SERVICE_NAME="${SERVICE_NAME:-dexarb-webhook}"
  PING_STATUS_FILE="${PING_STATUS_FILE:-/tmp/${SERVICE_NAME}-last-ping}"
  WEBHOOK_VERIFY_TIMEOUT="${WEBHOOK_VERIFY_TIMEOUT:-180}"
}

validate_config() {
  [[ -n "${REPO_FULL_NAME:-}" ]] || fail "REPO_FULL_NAME is required"
  [[ -n "${WEBHOOK_SECRET:-}" ]] || fail "WEBHOOK_SECRET is required"
  [[ "$WEBHOOK_PORT" =~ ^[0-9]+$ ]] || fail "WEBHOOK_PORT must be numeric"
  [[ "$WEBHOOK_VERIFY_TIMEOUT" =~ ^[0-9]+$ ]] || fail "WEBHOOK_VERIFY_TIMEOUT must be numeric"
}

print_instructions() {
  cat <<EOF

GitHub webhook verification

Add or update this webhook in your GitHub repository now:
- Payload URL: http://YOUR_VPS_IP:${WEBHOOK_PORT}/hooks/${HOOK_ID}
- Content type: application/json
- Secret: use the same WEBHOOK_SECRET value from ${CONFIG_PATH}
- Event selection: Just the push event

As soon as you save the webhook, GitHub should send a ping event automatically.
This script will wait up to ${WEBHOOK_VERIFY_TIMEOUT} seconds for that ping and confirm it locally.
EOF
}

wait_for_ping() {
  local start_epoch now recorded_epoch
  start_epoch="$(date +%s)"

  while :; do
    if [[ -f "$PING_STATUS_FILE" ]]; then
      recorded_epoch="$(stat -c %Y "$PING_STATUS_FILE" 2>/dev/null || echo 0)"
      if (( recorded_epoch >= start_epoch )); then
        log "GitHub ping received and recorded in $PING_STATUS_FILE"
        printf '\nSuccess: the GitHub webhook is working, connected to this VPS, and ready to receive push events for deployment.\n'
        printf 'Confirmation file: %s\n' "$PING_STATUS_FILE"
        cat "$PING_STATUS_FILE"
        return 0
      fi
    fi

    now="$(date +%s)"
    if (( now - start_epoch >= WEBHOOK_VERIFY_TIMEOUT )); then
      fail "timed out waiting for the GitHub ping. Check the GitHub webhook delivery page and your VPS network path."
    fi

    sleep 2
  done
}

main() {
  load_config "${1:-}"
  apply_defaults
  validate_config
  print_instructions
  wait_for_ping
}

main "$@"
