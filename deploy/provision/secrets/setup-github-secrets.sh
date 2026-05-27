#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR/../../.."

extract_key() {
  local file="$1"
  if [[ ! -f "$file" ]]; then
    echo "ERROR: missing $file — run dotenvx encrypt first" >&2
    exit 1
  fi
  grep '^DOTENV_PRIVATE_KEY_ENC=' "$file" | cut -d= -f2-
}

read_vps_var() {
  # Use the already-sourced variables so multi-line quoted values
  # (e.g. VPS_SSH_KNOWN_HOSTS, VPS_SSH_PRIVATE_KEY) are preserved.
  printf '%s' "${!1-}"
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

if [[ ! -f deploy/provision/.env.vps ]]; then
  echo "ERROR: deploy/provision/.env.vps not found — copy deploy/provision/.env.vps.example and fill in the values" >&2
  exit 1
fi

# shellcheck disable=SC1091
set -a; . deploy/provision/.env.vps; set +a

REMOTE=$(git remote get-url origin)
REPO=$(echo "$REMOTE" | sed 's|.*github\.com[:/]||; s|\.git$||')

echo "Repo : $REPO"
echo ""

# Install gh CLI to ~/.local/bin if not found
if ! command -v gh &>/dev/null; then
  echo "Installing gh CLI to ~/.local/bin..."
  GH_VERSION=$(curl -fsSL https://api.github.com/repos/cli/cli/releases/latest | grep '"tag_name"' | cut -d'"' -f4 | tr -d v)
  curl -fsSL "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_amd64.tar.gz" \
    | tar -xz -C /tmp
  mkdir -p ~/.local/bin
  cp "/tmp/gh_${GH_VERSION}_linux_amd64/bin/gh" ~/.local/bin/gh
  export PATH="$HOME/.local/bin:$PATH"
fi

set_secret() {
  GH_TOKEN="$TOKEN" gh secret set "$1" --body "$2" --repo "$REPO"
  echo "  set $1"
}

set_secret DOTENV_PRIVATE_KEY_ROOT            "$(extract_key .env.keys)"
set_secret DOTENV_PRIVATE_KEY_UNIFIED_API     "$(extract_key dexUnifiedApi/.env.keys)"
set_secret DOTENV_PRIVATE_KEY_MONITOR         "$(extract_key dexMonitor/.env.keys)"
set_secret DOTENV_PRIVATE_KEY_MM_CONTROLLER   "$(extract_key dexMmController/.env.keys)"
set_secret DOTENV_PRIVATE_KEY_FIAT_CONTROLLER "$(extract_key dexFiatController/.env.keys)"
set_secret GH_PAT                             "$TOKEN"
set_secret VPS_SSH_PRIVATE_KEY                "$(read_vps_var VPS_SSH_PRIVATE_KEY)"
set_secret VPS_SSH_KNOWN_HOSTS                "$(read_vps_var VPS_SSH_KNOWN_HOSTS)"
set_secret VPS_SSH_HOST                       "$(read_vps_var VPS_SSH_HOST)"
set_secret VPS_SSH_USER                       "$(read_vps_var VPS_SSH_USER)"
set_secret VPS_REPO_DIR                       "$(read_vps_var VPS_REPO_DIR)"

echo ""
echo "Done — 11 secrets written to github:$REPO"
