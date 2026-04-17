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

warn() {
  log "warning: $*"
}

fail() {
  log "$*"
  exit 1
}

command_exists() {
  command -v "$1" >/dev/null 2>&1
}

run_root() {
  if [[ "${EUID:-$(id -u)}" -eq 0 ]]; then
    "$@"
  elif command_exists sudo; then
    sudo "$@"
  else
    fail "this step requires root privileges and sudo is not available"
  fi
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
  REPO_FULL_NAME="${REPO_FULL_NAME:-}"
  BRANCH="${BRANCH:-light_setting}"
  REMOTE="${REMOTE:-origin}"
  COMPOSE_FILE="${COMPOSE_FILE:-$REPO_DIR/docker-compose.yml}"
  WEBHOOK_SECRET="${WEBHOOK_SECRET:-}"
  WEBHOOK_PORT="${WEBHOOK_PORT:-9000}"
  WEBHOOK_HOST="${WEBHOOK_HOST:-0.0.0.0}"
  WEBHOOK_BINARY="${WEBHOOK_BINARY:-/usr/bin/webhook}"
  HOOK_ID="${HOOK_ID:-dexarb-redeploy}"
  SERVICE_NAME="${SERVICE_NAME:-dexarb-webhook}"
  SERVICE_USER="${SERVICE_USER:-${SUDO_USER:-$USER}}"
  OPEN_FIREWALL="${OPEN_FIREWALL:-false}"
  PING_STATUS_FILE="${PING_STATUS_FILE:-/tmp/${SERVICE_NAME}-last-ping}"
  WEBHOOK_VERIFY_TIMEOUT="${WEBHOOK_VERIFY_TIMEOUT:-180}"

  HOOKS_FILE="$REPO_DIR/vps_deploy/hooks.json"
  REDEPLOY_SCRIPT="$REPO_DIR/vps_deploy/redeploy.sh"
  WEBHOOK_HANDLER_SCRIPT="$REPO_DIR/vps_deploy/webhook-handler.sh"
  VERIFY_WEBHOOK_SCRIPT="$REPO_DIR/vps_deploy/verify-webhook.sh"
  SERVICE_PATH="/etc/systemd/system/${SERVICE_NAME}.service"
}

validate_config() {
  [[ -n "$REPO_FULL_NAME" ]] || fail "REPO_FULL_NAME is required"
  [[ -n "$WEBHOOK_SECRET" ]] || fail "WEBHOOK_SECRET is required"
  [[ -d "$REPO_DIR/.git" ]] || fail "REPO_DIR is not a git repo: $REPO_DIR"
  [[ -f "$REDEPLOY_SCRIPT" ]] || fail "redeploy script not found: $REDEPLOY_SCRIPT"
  [[ -f "$WEBHOOK_HANDLER_SCRIPT" ]] || fail "webhook handler script not found: $WEBHOOK_HANDLER_SCRIPT"
  [[ -f "$VERIFY_WEBHOOK_SCRIPT" ]] || fail "webhook verifier script not found: $VERIFY_WEBHOOK_SCRIPT"
  [[ -f "$COMPOSE_FILE" ]] || fail "compose file not found: $COMPOSE_FILE"
  [[ "$WEBHOOK_PORT" =~ ^[0-9]+$ ]] || fail "WEBHOOK_PORT must be a number: $WEBHOOK_PORT"
  (( WEBHOOK_PORT >= 1 && WEBHOOK_PORT <= 65535 )) || fail "WEBHOOK_PORT must be between 1 and 65535: $WEBHOOK_PORT"
  [[ "$WEBHOOK_VERIFY_TIMEOUT" =~ ^[0-9]+$ ]] || fail "WEBHOOK_VERIFY_TIMEOUT must be a number of seconds: $WEBHOOK_VERIFY_TIMEOUT"
}

json_escape() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  value="${value//$'\n'/\\n}"
  value="${value//$'\r'/\\r}"
  value="${value//$'\t'/\\t}"
  printf '%s' "$value"
}

render_hooks_json() {
  local repo_dir_json branch_json remote_json compose_json secret_json repo_full_json hook_id_json
  local handler_json ping_status_json
  repo_dir_json="$(json_escape "$REPO_DIR")"
  branch_json="$(json_escape "$BRANCH")"
  remote_json="$(json_escape "$REMOTE")"
  compose_json="$(json_escape "$COMPOSE_FILE")"
  secret_json="$(json_escape "$WEBHOOK_SECRET")"
  repo_full_json="$(json_escape "$REPO_FULL_NAME")"
  hook_id_json="$(json_escape "$HOOK_ID")"
  handler_json="$(json_escape "$WEBHOOK_HANDLER_SCRIPT")"
  ping_status_json="$(json_escape "$PING_STATUS_FILE")"

  cat <<EOF
[
  {
    "id": "${hook_id_json}",
    "execute-command": "${handler_json}",
    "command-working-directory": "${repo_dir_json}",
    "response-message": "webhook received",
    "pass-environment-to-command": [
      {
        "source": "string",
        "name": "${repo_dir_json}",
        "envname": "REPO_DIR"
      },
      {
        "source": "string",
        "name": "${branch_json}",
        "envname": "BRANCH"
      },
      {
        "source": "string",
        "name": "${remote_json}",
        "envname": "REMOTE"
      },
      {
        "source": "string",
        "name": "${compose_json}",
        "envname": "COMPOSE_FILE"
      },
      {
        "source": "string",
        "name": "${repo_full_json}",
        "envname": "EXPECTED_REPO_FULL_NAME"
      },
      {
        "source": "string",
        "name": "${ping_status_json}",
        "envname": "PING_STATUS_FILE"
      },
      {
        "source": "header",
        "name": "X-GitHub-Event",
        "envname": "HOOK_EVENT"
      },
      {
        "source": "payload",
        "name": "ref",
        "envname": "HOOK_REF"
      },
      {
        "source": "payload",
        "name": "repository.full_name",
        "envname": "HOOK_REPOSITORY_FULL_NAME"
      },
      {
        "source": "payload",
        "name": "zen",
        "envname": "HOOK_ZEN"
      }
    ],
    "trigger-rule": {
      "and": [
        {
          "match": {
            "type": "value",
            "value": "${repo_full_json}",
            "parameter": {
              "source": "payload",
              "name": "repository.full_name"
            }
          }
        },
        {
          "match": {
            "type": "payload-hmac-sha256",
            "secret": "${secret_json}",
            "parameter": {
              "source": "header",
              "name": "X-Hub-Signature-256"
            }
          }
        },
        {
          "or": [
            {
              "match": {
                "type": "value",
                "value": "push",
                "parameter": {
                  "source": "header",
                  "name": "X-GitHub-Event"
                }
              }
            },
            {
              "match": {
                "type": "value",
                "value": "ping",
                "parameter": {
                  "source": "header",
                  "name": "X-GitHub-Event"
                }
              }
            }
          ]
        }
      ]
    }
  }
]
EOF
}

render_service_file() {
  cat <<EOF
[Unit]
Description=DexArbTrading GitHub webhook listener
After=network-online.target docker.service
Wants=network-online.target

[Service]
Type=simple
User=${SERVICE_USER}
WorkingDirectory=${REPO_DIR}
ExecStart=${WEBHOOK_BINARY} -verbose -hotreload -ip ${WEBHOOK_HOST} -port ${WEBHOOK_PORT} -hooks ${HOOKS_FILE}
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
}

write_file_if_changed() {
  local target="$1"
  local content="$2"
  local mode="$3"
  local tmp_file
  tmp_file="$(mktemp)"
  printf '%s\n' "$content" >"$tmp_file"

  mkdir -p "$(dirname "$target")"

  if [[ -f "$target" ]] && cmp -s "$tmp_file" "$target"; then
    log "already up to date: $target"
    rm -f "$tmp_file"
    return 0
  fi

  install -m "$mode" "$tmp_file" "$target"
  log "wrote $target"
  rm -f "$tmp_file"
}

write_root_file_if_changed() {
  local target="$1"
  local content="$2"
  local mode="$3"
  local tmp_file
  tmp_file="$(mktemp)"
  printf '%s\n' "$content" >"$tmp_file"

  if run_root test -f "$target" && run_root cmp -s "$tmp_file" "$target"; then
    log "already up to date: $target"
    rm -f "$tmp_file"
    return 0
  fi

  run_root install -m "$mode" "$tmp_file" "$target"
  log "wrote $target"
  rm -f "$tmp_file"
}

ensure_webhook_installed() {
  if command_exists "$WEBHOOK_BINARY"; then
    log "webhook already installed at $WEBHOOK_BINARY"
    return
  fi

  if [[ "$WEBHOOK_BINARY" != "/usr/bin/webhook" ]]; then
    fail "WEBHOOK_BINARY does not exist: $WEBHOOK_BINARY"
  fi

  if command_exists apt-get; then
    log "installing webhook via apt"
    run_root apt-get update
    run_root apt-get install -y webhook
  else
    fail "webhook is missing and automatic install is only implemented for apt-based systems"
  fi

  command_exists "$WEBHOOK_BINARY" || fail "webhook install did not provide $WEBHOOK_BINARY"
}

ensure_redeploy_script_executable() {
  if [[ -x "$REDEPLOY_SCRIPT" ]]; then
    log "redeploy script already executable"
  else
    chmod +x "$REDEPLOY_SCRIPT"
    log "made redeploy script executable"
  fi
}

ensure_webhook_handler_script_executable() {
  if [[ -x "$WEBHOOK_HANDLER_SCRIPT" ]]; then
    log "webhook handler script already executable"
  else
    chmod +x "$WEBHOOK_HANDLER_SCRIPT"
    log "made webhook handler script executable"
  fi
}

ensure_verify_webhook_script_executable() {
  if [[ -x "$VERIFY_WEBHOOK_SCRIPT" ]]; then
    log "webhook verifier script already executable"
  else
    chmod +x "$VERIFY_WEBHOOK_SCRIPT"
    log "made webhook verifier script executable"
  fi
}

ensure_service_user_exists() {
  if id -u "$SERVICE_USER" >/dev/null 2>&1; then
    log "service user exists: $SERVICE_USER"
  else
    fail "service user does not exist: $SERVICE_USER"
  fi
}

ensure_hooks_file() {
  local content
  content="$(render_hooks_json)"
  write_file_if_changed "$HOOKS_FILE" "$content" 600
}

ensure_service_file() {
  local content
  content="$(render_service_file)"
  write_root_file_if_changed "$SERVICE_PATH" "$content" 644
}

ensure_systemd_service() {
  command_exists systemctl || fail "systemctl is required for service installation"

  run_root systemctl daemon-reload

  if run_root systemctl is-enabled --quiet "$SERVICE_NAME"; then
    log "service already enabled: $SERVICE_NAME"
  else
    run_root systemctl enable "$SERVICE_NAME"
    log "enabled service: $SERVICE_NAME"
  fi

  if run_root systemctl is-active --quiet "$SERVICE_NAME"; then
    run_root systemctl restart "$SERVICE_NAME"
    log "restarted service: $SERVICE_NAME"
  else
    run_root systemctl start "$SERVICE_NAME"
    log "started service: $SERVICE_NAME"
  fi
}

ensure_firewall() {
  case "${OPEN_FIREWALL,,}" in
    true|1|yes)
      ;;
    *)
      log "firewall changes skipped"
      return
      ;;
  esac

  if command_exists ufw; then
    if run_root ufw status | grep -q "${WEBHOOK_PORT}/tcp"; then
      log "ufw already allows port ${WEBHOOK_PORT}"
    else
      run_root ufw allow "${WEBHOOK_PORT}/tcp"
      log "opened port ${WEBHOOK_PORT}/tcp in ufw"
    fi
    return
  fi

  if command_exists firewall-cmd; then
    if run_root firewall-cmd --query-port="${WEBHOOK_PORT}/tcp" >/dev/null; then
      log "firewalld already allows port ${WEBHOOK_PORT}/tcp"
    else
      run_root firewall-cmd --permanent --add-port="${WEBHOOK_PORT}/tcp"
      run_root firewall-cmd --reload
      log "opened port ${WEBHOOK_PORT}/tcp in firewalld"
    fi
    return
  fi

  log "OPEN_FIREWALL is true but no supported firewall tool was found"
}

ensure_webhook_port_reachable() {
  local listener_found=0

  if command_exists ss; then
    if ss -ltn "sport = :${WEBHOOK_PORT}" | awk 'NR > 1 { found = 1 } END { exit(found ? 0 : 1) }'; then
      listener_found=1
    fi
  elif command_exists netstat; then
    if netstat -ltn 2>/dev/null | awk -v port=":${WEBHOOK_PORT}" '$4 ~ port"$" { found = 1 } END { exit(found ? 0 : 1) }'; then
      listener_found=1
    fi
  else
    warn "skipping local port-listener check because neither ss nor netstat is installed"
  fi

  if [[ "$listener_found" -eq 0 ]]; then
    fail "no process is listening on TCP port ${WEBHOOK_PORT}; the webhook endpoint will not receive GitHub pushes"
  fi

  if command_exists ufw; then
    if run_root ufw status | grep -q "Status: active"; then
      if run_root ufw status | grep -Eq "\\b${WEBHOOK_PORT}/tcp\\b.*ALLOW"; then
        log "verified ufw allows port ${WEBHOOK_PORT}/tcp"
      else
        fail "ufw is active but port ${WEBHOOK_PORT}/tcp is not allowed; GitHub will not be able to reach the webhook"
      fi
    else
      log "ufw is installed but inactive"
    fi
    return
  fi

  if command_exists firewall-cmd; then
    if run_root firewall-cmd --state >/dev/null 2>&1; then
      if run_root firewall-cmd --query-port="${WEBHOOK_PORT}/tcp" >/dev/null; then
        log "verified firewalld allows port ${WEBHOOK_PORT}/tcp"
      else
        fail "firewalld is active but port ${WEBHOOK_PORT}/tcp is not allowed; GitHub will not be able to reach the webhook"
      fi
    else
      log "firewalld is installed but inactive"
    fi
    return
  fi

  warn "no supported firewall tool found for verification; if your VPS uses another firewall, allow TCP port ${WEBHOOK_PORT} manually"
}

print_summary() {
  cat <<EOF

Setup complete.

Config file: $CONFIG_PATH
Repo dir: $REPO_DIR
Branch: $BRANCH
Hook id: $HOOK_ID
Webhook URL: http://YOUR_VPS_IP:${WEBHOOK_PORT}/hooks/${HOOK_ID}
Systemd service: $SERVICE_NAME
Hooks file: $HOOKS_FILE
Ping status file: $PING_STATUS_FILE
EOF
}

launch_webhook_verifier() {
  if [[ ! -t 0 || ! -t 1 ]]; then
    log "skipping interactive webhook verification because this shell is non-interactive"
    log "run ${VERIFY_WEBHOOK_SCRIPT} manually on the VPS when you are ready to confirm GitHub delivery"
    return
  fi

  "${VERIFY_WEBHOOK_SCRIPT}"
}

main() {
  load_config "${1:-}"
  apply_defaults
  validate_config

  log "using config from $CONFIG_PATH"
  ensure_webhook_installed
  ensure_service_user_exists
  ensure_redeploy_script_executable
  ensure_webhook_handler_script_executable
  ensure_verify_webhook_script_executable
  ensure_hooks_file
  ensure_service_file
  ensure_systemd_service
  ensure_firewall
  ensure_webhook_port_reachable
  print_summary
  launch_webhook_verifier
}

main "$@"
