#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

extract_key() {
  local file="$1"
  if [[ ! -f "$file" ]]; then
    echo "ERROR: missing $file — run dotenvx encrypt first" >&2
    exit 1
  fi
  grep '^DOTENV_PRIVATE_KEY_ENC=' "$file" | cut -d= -f2-
}

if [[ ! -f .env ]]; then
  echo "ERROR: .env not found" >&2
  exit 1
fi
TOKEN=$(grep '^GH_PAT=' .env | cut -d= -f2-)
if [[ -z "$TOKEN" ]]; then
  echo "ERROR: GH_PAT not set in .env" >&2
  exit 1
fi

REMOTE=$(git remote get-url origin)
GITEA_BASE=$(echo "$REMOTE" | sed 's|/[^/]*/[^/]*\.git$||; s|/[^/]*/[^/]*$||')
REPO=$(echo "$REMOTE" | sed 's|.*/\([^/]*/[^/]*\)$|\1|; s|\.git$||')

echo "Gitea : $GITEA_BASE"
echo "Repo  : $REPO"
echo ""

set_secret() {
  local name="$1" value="$2"
  local http_code
  http_code=$(curl -s -o /dev/null -w "%{http_code}" -X PUT \
    "$GITEA_BASE/api/v1/repos/$REPO/actions/secrets/$name" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"data\":\"$value\"}")
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
set_secret GH_PAT                             "$TOKEN"

echo ""
echo "Done — 6 secrets written to gitea:$REPO"
