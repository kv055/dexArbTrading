#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR/../../.."

# Shared repo list + secret set (single source of truth; see the file's header).
# shellcheck source=../manifest.sh
. "$SCRIPT_DIR/../manifest.sh"

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

count=0

# Per-repo dotenv private keys, from each repo's <dir>/.env.keys (manifest REPOS).
for entry in "${REPOS[@]}"; do
  secret="$(repo_dotenv_secret "$entry")"
  [[ -n "$secret" ]] || continue
  set_secret "$secret" "$(extract_key "$(keys_file_for_dir "$(repo_dir "$entry")")")"
  count=$((count + 1))
done

# Plain secrets read from the root .env by name (currently just GH_PAT; == $TOKEN).
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

echo ""
echo "Done — $count secrets written to github:$REPO"
