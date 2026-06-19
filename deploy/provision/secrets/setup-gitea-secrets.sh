#!/usr/bin/env bash
set -Eeuo pipefail

# Writes the 11 repository Actions secrets to a Gitea repo via the REST API.
# Connection details come from the environment (exported by setup-gitea.sh) or,
# when run standalone, from deploy/provision/gitea/.env.gitea.
#
#   GITEA_URL    base URL, no trailing slash, no /api (e.g. https://gitea.x.ts.net)
#   GITEA_OWNER  user/org that owns the repo
#   GITEA_TOKEN  *Gitea* PAT with write access to the repo's secrets
#
# The GH_PAT secret value still comes from the root .env (it is the GitHub PAT the
# runner uses to fetch submodules from github.com); GH_PAT is NOT the Gitea token.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR/../../.."   # repo root

# Fall back to the gitea env file when not invoked via setup-gitea.sh.
if [[ -z "${GITEA_URL:-}" || -z "${GITEA_OWNER:-}" || -z "${GITEA_TOKEN:-}" ]]; then
  ENV_GITEA="deploy/provision/gitea/.env.gitea"
  if [[ -f "$ENV_GITEA" ]]; then
    # shellcheck disable=SC1090
    set -a; . "$ENV_GITEA"; set +a
  fi
fi

: "${GITEA_URL:?GITEA_URL not set (env or deploy/provision/gitea/.env.gitea)}"
: "${GITEA_OWNER:?GITEA_OWNER not set (env or deploy/provision/gitea/.env.gitea)}"
: "${GITEA_TOKEN:?GITEA_TOKEN not set (env or deploy/provision/gitea/.env.gitea)}"

GITEA_URL="${GITEA_URL%/}"
REPO="$GITEA_OWNER/dexArbTrading"

for tool in curl jq git; do
  command -v "$tool" >/dev/null 2>&1 || { echo "ERROR: missing required tool: $tool (install with: sudo apt install -y curl jq git)" >&2; exit 1; }
done

extract_key() {
  local file="$1"
  if [[ ! -f "$file" ]]; then
    echo "ERROR: missing $file — run dotenvx encrypt first" >&2
    exit 1
  fi
  grep '^DOTENV_PRIVATE_KEY_ENC=' "$file" | cut -d= -f2-
}

# Source .env.vps so multi-line quoted values (SSH key, known_hosts) are preserved,
# then read each via indirect expansion — same approach as setup-github-secrets.sh.
read_vps_var() {
  printf '%s' "${!1-}"
}

if [[ ! -f .env ]]; then
  echo "ERROR: .env not found" >&2
  exit 1
fi
GH_PAT_VALUE=$(grep '^GH_PAT=' .env | cut -d= -f2-)
if [[ -z "$GH_PAT_VALUE" ]]; then
  echo "ERROR: GH_PAT not set in .env" >&2
  exit 1
fi

if [[ ! -f deploy/provision/.env.vps ]]; then
  echo "ERROR: deploy/provision/.env.vps not found — copy deploy/provision/.env.vps.example and fill it in" >&2
  exit 1
fi

# shellcheck disable=SC1091
set -a; . deploy/provision/.env.vps; set +a

echo "Gitea : $GITEA_URL"
echo "Repo  : $REPO"
echo ""

set_secret() {
  local name="$1" value="$2"
  local http_code
  http_code=$(curl -s -o /dev/null -w "%{http_code}" -X PUT \
    "$GITEA_URL/api/v1/repos/$REPO/actions/secrets/$name" \
    -H "Authorization: Bearer $GITEA_TOKEN" \
    -H "Content-Type: application/json" \
    --data-binary @- <<JSON
{"data": $(printf '%s' "$value" | jq -Rs .)}
JSON
  )
  if [[ "$http_code" != "201" && "$http_code" != "204" ]]; then
    echo "ERROR: failed to set $name (HTTP $http_code)" >&2
    exit 1
  fi
  echo "  set $name"
}

set_secret DOTENV_PRIVATE_KEY_ROOT            "$(extract_key .env.keys)"
set_secret DOTENV_PRIVATE_KEY_UNIFIED_API     "$(extract_key dexUnifiedApi/.env.keys)"
set_secret DOTENV_PRIVATE_KEY_MONITOR         "$(extract_key dexMonitor/.env.keys)"
set_secret DOTENV_PRIVATE_KEY_MM_CONTROLLER   "$(extract_key dexMmController/.env.keys)"
set_secret DOTENV_PRIVATE_KEY_FIAT_CONTROLLER "$(extract_key dexFiatController/.env.keys)"
set_secret GH_PAT                             "$GH_PAT_VALUE"
set_secret VPS_SSH_PRIVATE_KEY                "$(read_vps_var VPS_SSH_PRIVATE_KEY)"
set_secret VPS_SSH_KNOWN_HOSTS                "$(read_vps_var VPS_SSH_KNOWN_HOSTS)"
set_secret VPS_SSH_HOST                       "$(read_vps_var VPS_SSH_HOST)"
set_secret VPS_SSH_USER                       "$(read_vps_var VPS_SSH_USER)"
set_secret VPS_REPO_DIR                       "$(read_vps_var VPS_REPO_DIR)"

# Gitea container-registry login for the build-and-push job (pushes images to the
# Gitea store instead of GHCR). USER defaults to the owner; the orchestrator passes
# the token's actual login. TOKEN reuses the Gitea PAT (needs write:package scope).
# NB: secret names must NOT start with GITEA_ — Gitea reserves that prefix (and
# GITHUB_), rejecting such names with HTTP 400. Hence REGISTRY_* not GITEA_REGISTRY_*.
set_secret REGISTRY_URL                       "${GITEA_REGISTRY_URL_VALUE:-$GITEA_URL}"
set_secret REGISTRY_USER                      "${GITEA_REGISTRY_USER:-$GITEA_OWNER}"
set_secret REGISTRY_TOKEN                     "$GITEA_TOKEN"

echo ""
echo "Done — 14 secrets written to gitea:$REPO"
