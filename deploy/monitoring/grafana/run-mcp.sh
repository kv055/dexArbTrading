#!/usr/bin/env bash
# Launch grafana/mcp-grafana on stdio for a local MCP client (e.g. Cursor).
#
# Reads GRAFANA_AGENT_TOKEN from the environment or the repo-root .env and
# maps it to GRAFANA_SERVICE_ACCOUNT_TOKEN. Does not start Grafana — the
# monitoring stack (nginx + grafana + datasources) must already be up.
# Writes nothing to stdout so the MCP protocol stays intact.
#
# Usage (Cursor user MCP — .cursor/ is gitignored, so this is not project config):
#   "command": "/absolute/path/to/dexArbTrading/deploy/monitoring/grafana/run-mcp.sh"
#
# Override GRAFANA_URL (default https://localhost/grafana) or
# GRAFANA_AGENT_TOKEN in the environment; existing values win over .env.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
ENV_FILE="$REPO_ROOT/.env"
CERT_FILE="$REPO_ROOT/deploy/nginx/certs/server.crt"

# Load KEY from .env into the environment when the var is unset. Does not
# source the rest of .env (avoids leaking unrelated secrets into mcp-grafana).
load_dotenv_var() {
    local key="$1"
    if [[ -n "${!key:-}" ]]; then
        return 0
    fi
    [[ -f "$ENV_FILE" ]] || return 0
    local raw
    raw="$(grep -E "^${key}=" "$ENV_FILE" | tail -1 || true)"
    [[ -n "$raw" ]] || return 0
    local val="${raw#*=}"
    val="${val%$'\r'}"
    if [[ "${#val}" -ge 2 ]]; then
        if [[ "$val" == \"*\" && "$val" == *\" ]]; then
            val="${val:1:-1}"
        elif [[ "$val" == \'*\' && "$val" == *\' ]]; then
            val="${val:1:-1}"
        fi
    fi
    export "${key}=${val}"
}

load_dotenv_var GRAFANA_AGENT_TOKEN
load_dotenv_var GRAFANA_URL

if [[ -z "${GRAFANA_URL:-}" ]]; then
    GRAFANA_URL="https://localhost/grafana"
fi

if [[ -z "${GRAFANA_AGENT_TOKEN:-}" ]]; then
    echo "ERROR: GRAFANA_AGENT_TOKEN is empty." >&2
    echo "Mint one with deploy/monitoring/grafana/bootstrap-agent-token.sh and store it in .env / .env.enc." >&2
    echo "See deploy/monitoring/README.md \"Agent datasource access\"." >&2
    exit 1
fi

if [[ "$GRAFANA_AGENT_TOKEN" == encrypted:* ]]; then
    echo "ERROR: GRAFANA_AGENT_TOKEN is still encrypted. Decrypt .env.enc into .env first." >&2
    exit 1
fi

export GRAFANA_URL
export GRAFANA_SERVICE_ACCOUNT_TOKEN="${GRAFANA_SERVICE_ACCOUNT_TOKEN:-$GRAFANA_AGENT_TOKEN}"

tls_args=()
if [[ -f "$CERT_FILE" ]]; then
    tls_args=(--tls-ca-file "$CERT_FILE")
else
    echo "WARNING: ${CERT_FILE} not found; skipping TLS certificate verification." >&2
    echo "Generate it with deploy/nginx/gen-self-signed-cert.sh, or drop in a trusted pair." >&2
    tls_args=(--tls-skip-verify)
fi

if command -v uvx >/dev/null 2>&1; then
    exec uvx mcp-grafana --disable-write "${tls_args[@]}"
fi
if command -v mcp-grafana >/dev/null 2>&1; then
    exec mcp-grafana --disable-write "${tls_args[@]}"
fi

echo "ERROR: neither uvx nor mcp-grafana is on PATH." >&2
echo "Install uv (https://docs.astral.sh/uv/) or put the mcp-grafana binary on PATH." >&2
exit 1
