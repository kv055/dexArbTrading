#!/usr/bin/env bash
set -Eeuo pipefail

# Writes the repository Actions secrets to a Gitea repo via the REST API. The secret
# set (per-repo dotenv keys + GH_PAT + VPS_*) is declared in the shared manifest
# deploy/provision/manifest.sh; the 3 REGISTRY_* secrets are computed here from the
# Gitea connection. Connection details come from the environment (exported by
# setup-gitea.sh) or, when run standalone, from deploy/provision/gitea/.env.gitea.
#
#   GITEA_URL    base URL, no trailing slash, no /api (e.g. https://gitea.x.ts.net)
#   GITEA_OWNER  user/org that owns the repo
#   GITEA_TOKEN  *Gitea* PAT with write access to the repo's secrets
#
# The GH_PAT secret value still comes from the root .env (it is the GitHub PAT the
# runner uses to fetch submodules from github.com); GH_PAT is NOT the Gitea token.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR/../../.."   # repo root

# Shared repo list + secret set (single source of truth; see the file's header).
# shellcheck source=../manifest.sh
. "$SCRIPT_DIR/../manifest.sh"

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
REPO="$GITEA_OWNER/$ROOT_REPO"

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
  local name="$1" value="$2" base="${3:-/repos/$REPO/actions/secrets}"
  local http_code
  http_code=$(curl -s -o /dev/null -w "%{http_code}" -X PUT \
    "$GITEA_URL/api/v1$base/$name" \
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

count=0

# Per-repo dotenv private keys, from each repo's <dir>/.env.keys (manifest REPOS).
for entry in "${REPOS[@]}"; do
  secret="$(repo_dotenv_secret "$entry")"
  [[ -n "$secret" ]] || continue
  set_secret "$secret" "$(extract_key "$(keys_file_for_dir "$(repo_dir "$entry")")")"
  count=$((count + 1))
done

# Plain secrets read from the root .env by name (currently just GH_PAT).
for name in "${ENV_SECRETS[@]}"; do
  value=$(grep "^$name=" .env | cut -d= -f2-)
  if [[ -z "$value" ]]; then
    echo "ERROR: $name not set in .env" >&2
    exit 1
  fi
  set_secret "$name" "$value"
  count=$((count + 1))
done

# VPS_* secrets sourced from deploy/provision/.env.vps (manifest VPS_SECRETS).
for name in "${VPS_SECRETS[@]}"; do
  set_secret "$name" "$(read_vps_var "$name")"
  count=$((count + 1))
done

# Gitea container-registry login for the build-and-push job (pushes images to the
# Gitea store instead of GHCR). USER defaults to the owner; the orchestrator passes
# the token's actual login. TOKEN reuses the Gitea PAT (needs write:package scope).
# NB: secret names must NOT start with GITEA_ — Gitea reserves that prefix (and
# GITHUB_), rejecting such names with HTTP 400. Hence REGISTRY_* not GITEA_REGISTRY_*.
# These are computed from the Gitea connection, not the shared manifest.
set_secret REGISTRY_URL                       "${GITEA_REGISTRY_URL_VALUE:-$GITEA_URL}"
set_secret REGISTRY_USER                      "${GITEA_REGISTRY_USER:-$GITEA_OWNER}"
set_secret REGISTRY_TOKEN                     "$GITEA_TOKEN"
count=$((count + 3))

# The service repos' own CI (.gitea/workflows/ci.yml in each submodule repo)
# clones the private root repo and its submodules, so REGISTRY_URL/TOKEN are
# also written as owner-level secrets, visible to every repo of the owner.
SECRET_API_BASE="/user/actions/secrets"
if curl -fsS -o /dev/null -H "Authorization: Bearer $GITEA_TOKEN" "$GITEA_URL/api/v1/orgs/$GITEA_OWNER" 2>/dev/null; then
  SECRET_API_BASE="/orgs/$GITEA_OWNER/actions/secrets"
fi
set_secret REGISTRY_URL   "${GITEA_REGISTRY_URL_VALUE:-$GITEA_URL}" "$SECRET_API_BASE"
set_secret REGISTRY_TOKEN "$GITEA_TOKEN"                            "$SECRET_API_BASE"
count=$((count + 2))

echo ""
echo "Done — $count secrets written to gitea:$REPO"
