# Shared provisioning manifest — the single source of truth for the repo list and
# the secret set used by the GitHub and Gitea provisioning scripts.
#
# This file is *sourced* (no shebang, not executable). Consumers:
#   deploy/provision/gitea/setup-gitea.sh          (repo list, ROOT_REPO)
#   deploy/provision/gitea/teardown.sh             (ROOT_REPO)
#   deploy/provision/secrets/setup-gitea-secrets.sh  (repo list + secret set)
#   deploy/provision/secrets/setup-github-secrets.sh (repo list + secret set)
#
# Edit THIS file to add/remove a repo or a secret — nothing else hardcodes them.

# Root repo: owns the Actions secrets + runner registration, and is the
# default-branch / workflow_dispatch target.
ROOT_REPO="dexPendulum"

# One entry per repo to mirror/secure, in order. Fields: name|dir|dotenv_secret
#   name          repo name on the remote
#   dir           local path from the repo root ("." = root repo)
#   dotenv_secret name of the DOTENV_PRIVATE_KEY_* Actions secret built from
#                 <dir>/.env.keys (leave empty if the repo has no .env.keys)
REPOS=(
  "dexPendulum|.|DOTENV_PRIVATE_KEY_ROOT"
  "dexUnifiedApi|dexUnifiedApi|DOTENV_PRIVATE_KEY_UNIFIED_API"
  "dexTakerController|dexTakerController|DOTENV_PRIVATE_KEY_MM_CONTROLLER"
  "dexMonitor|dexMonitor|DOTENV_PRIVATE_KEY_MONITOR"
  "dexFiatController|dexFiatController|DOTENV_PRIVATE_KEY_FIAT_CONTROLLER"
)

# Plain secrets read from the root .env by name (grep '^NAME=' .env | cut -d= -f2-).
ENV_SECRETS=( "GH_PAT" )

# Secrets read from deploy/provision/.env.vps by indirect expansion (preserves
# multi-line quoted values like the SSH key and known_hosts).
VPS_SECRETS=(
  "VPS_SSH_PRIVATE_KEY"
  "VPS_SSH_KNOWN_HOSTS"
  "VPS_SSH_HOST"
  "VPS_SSH_USER"
  "VPS_REPO_DIR"
)

# --- helpers: split a "name|dir|dotenv_secret" REPOS entry on '|' -------------
repo_name()          { printf '%s' "${1%%|*}"; }
repo_dir()           { local r="${1#*|}"; printf '%s' "${r%%|*}"; }
repo_dotenv_secret() { printf '%s' "${1##*|}"; }

# .env.keys path for a repo dir: "." -> ".env.keys", else "<dir>/.env.keys".
keys_file_for_dir() {
  if [[ "$1" == "." ]]; then printf '.env.keys'; else printf '%s/.env.keys' "$1"; fi
}
