#!/usr/bin/env bash
# Create (idempotently) a read-only Grafana service account + token for scraping
# agents, and print the token once.
#
# Grafana OSS cannot provision service-account *tokens* via YAML, so this runs
# the Admin API with $GRAFANA_ADMIN_PASSWORD instead. The account is role
# `Viewer` — it can query datasources but cannot mutate anything. Store the
# printed token in your encrypted env (.env.enc) as GRAFANA_AGENT_TOKEN and hand
# it to the agents; revoke/rotate it from Grafana → Administration → Service
# accounts.
#
# Agents then query datasources over the TLS edge, e.g.:
#   curl --cacert deploy/nginx/certs/server.crt \
#     -H "Authorization: Bearer $GRAFANA_AGENT_TOKEN" \
#     'https://<host>:5443/grafana/api/datasources/proxy/uid/prometheus/api/v1/query?query=up'
#
# Usage:
#   GRAFANA_ADMIN_PASSWORD=... deploy/monitoring/grafana/bootstrap-agent-token.sh [GRAFANA_BASE_URL]
#
# Grafana's :3000 is not published to the host — only the nginx TLS edge is — so
# the easy path is to point at the edge's /grafana prefix and accept the
# self-signed cert via CURL_OPTS:
#   CURL_OPTS='-k' GRAFANA_ADMIN_PASSWORD=... \
#     deploy/monitoring/grafana/bootstrap-agent-token.sh https://localhost:5443/grafana
# (Default base URL assumes Grafana is reachable directly, e.g. from inside the
# docker network or with port 3000 published.)
set -euo pipefail

BASE_URL="${1:-http://localhost:3000}"
SA_NAME="agent-readonly"
TOKEN_NAME="agent-token-$(date +%Y%m%d-%H%M%S)"
ADMIN_USER="admin"
ADMIN_PASS="${GRAFANA_ADMIN_PASSWORD:?set GRAFANA_ADMIN_PASSWORD}"

# CURL_OPTS lets callers pass e.g. -k / --cacert for the self-signed TLS edge.
auth=(-u "${ADMIN_USER}:${ADMIN_PASS}" -H "Content-Type: application/json" -sS ${CURL_OPTS:-})

# Find the service account, or create it (Viewer = read-only).
sa_id="$(curl "${auth[@]}" "${BASE_URL}/api/serviceaccounts/search?query=${SA_NAME}" \
    | grep -o "\"id\":[0-9]*" | head -1 | grep -o "[0-9]*" || true)"

if [[ -z "${sa_id}" ]]; then
    echo "Creating service account '${SA_NAME}' (Viewer)..." >&2
    sa_id="$(curl "${auth[@]}" -X POST "${BASE_URL}/api/serviceaccounts" \
        -d "{\"name\":\"${SA_NAME}\",\"role\":\"Viewer\",\"isDisabled\":false}" \
        | grep -o "\"id\":[0-9]*" | head -1 | grep -o "[0-9]*")"
else
    echo "Reusing existing service account '${SA_NAME}' (id=${sa_id})." >&2
fi

if [[ -z "${sa_id}" ]]; then
    echo "ERROR: could not resolve service account id." >&2
    exit 1
fi

# Mint a fresh token. Tokens can't be listed back, so we always create one and
# the caller stores it; old tokens stay valid until revoked in the UI.
echo "Minting token '${TOKEN_NAME}'..." >&2
token="$(curl "${auth[@]}" -X POST "${BASE_URL}/api/serviceaccounts/${sa_id}/tokens" \
    -d "{\"name\":\"${TOKEN_NAME}\"}" \
    | grep -o "\"key\":\"[^\"]*\"" | sed 's/.*"key":"\([^"]*\)".*/\1/')"

if [[ -z "${token}" ]]; then
    echo "ERROR: token creation failed (token name may already exist — pass a new run/day)." >&2
    exit 1
fi

echo >&2
echo "GRAFANA_AGENT_TOKEN=${token}"
echo >&2
echo "Store the line above in your encrypted env (.env.enc). It is shown only once." >&2
