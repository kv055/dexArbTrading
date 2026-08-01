#!/usr/bin/env bash
# Decrypt, validate, and stage a complete environment bundle on the VPS.
# Plaintext is confined to a mode-0700 temporary directory and the VPS staging
# directory for the target SHA; active .env files are never touched here.
set -euo pipefail

: "${SSH_HOST:?SSH_HOST is required}"
: "${SSH_USER:?SSH_USER is required}"
: "${REPO_DIR:?REPO_DIR is required}"
: "${KEY_ROOT:?KEY_ROOT is required}"
: "${KEY_UNIFIED_API:?KEY_UNIFIED_API is required}"
: "${KEY_MONITOR:?KEY_MONITOR is required}"
: "${KEY_MM_CONTROLLER:?KEY_MM_CONTROLLER is required}"
: "${KEY_FIAT_CONTROLLER:?KEY_FIAT_CONTROLLER is required}"

[[ "$REPO_DIR" == /* && "$REPO_DIR" != "/" ]] || {
  echo "REPO_DIR must be a specific absolute path" >&2
  exit 2
}

new_sha="${1:-}"
[[ "$new_sha" =~ ^[0-9a-f]{40}$ ]] || {
  echo "target SHA must be a full 40-character hexadecimal commit" >&2
  exit 2
}

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
bundle="$(mktemp -d)"
cleanup() {
  rm -rf "$bundle"
}
trap cleanup EXIT
chmod 700 "$bundle"

mkdir -p \
  "$bundle/dexUnifiedApi" \
  "$bundle/dexMonitor" \
  "$bundle/dexMmController" \
  "$bundle/dexFiatController"

decrypt_copy() {
  local source="$1" destination="$2" key="$3" secret_name="$4"
  [[ -f "$source" ]] || {
    echo "ERROR: missing $source" >&2
    return 1
  }
  install -m 600 "$source" "$destination"
  if ! DOTENV_PRIVATE_KEY_ENC="$key" dotenvx decrypt -f "$destination" >/dev/null 2>&1; then
    echo "ERROR: cannot decrypt $source with $secret_name" >&2
    return 1
  fi
}

cd "$repo_root"
decrypt_copy .env.enc "$bundle/.env" "$KEY_ROOT" DOTENV_PRIVATE_KEY_ROOT
decrypt_copy dexUnifiedApi/.env.enc "$bundle/dexUnifiedApi/.env" \
  "$KEY_UNIFIED_API" DOTENV_PRIVATE_KEY_UNIFIED_API
decrypt_copy dexMonitor/.env.enc "$bundle/dexMonitor/.env" \
  "$KEY_MONITOR" DOTENV_PRIVATE_KEY_MONITOR
decrypt_copy dexMmController/.env.enc "$bundle/dexMmController/.env" \
  "$KEY_MM_CONTROLLER" DOTENV_PRIVATE_KEY_MM_CONTROLLER
decrypt_copy dexFiatController/.env.enc "$bundle/dexFiatController/.env" \
  "$KEY_FIAT_CONTROLLER" DOTENV_PRIVATE_KEY_FIAT_CONTROLLER

"$repo_root/scripts/validate-env-contract.sh" values "$bundle"

SSH_IDENTITY="${SSH_IDENTITY:-$HOME/.ssh/id_ed25519}"
SSH_OPTS=(
  -i "$SSH_IDENTITY"
  -o BatchMode=yes
  -o StrictHostKeyChecking=yes
  -o ServerAliveInterval=15
  -o ServerAliveCountMax=4
  -o ConnectTimeout=10
)

stage="$REPO_DIR/.deploy/staging/$new_sha"
# shellcheck disable=SC2029
ssh "${SSH_OPTS[@]}" "$SSH_USER@$SSH_HOST" \
  "set -eu; case '$new_sha' in (*[!0-9a-f]*|'') exit 2;; esac; rm -rf '$stage'; mkdir -p '$stage'"
scp -q "${SSH_OPTS[@]}" -r "$bundle" \
  "$SSH_USER@$SSH_HOST:$stage/env"
scp -q "${SSH_OPTS[@]}" \
  "$repo_root/scripts/validate-env-contract.sh" \
  "$repo_root/deploy/transactional-deploy.sh" \
  "$SSH_USER@$SSH_HOST:$stage/"
# shellcheck disable=SC2029
ssh "${SSH_OPTS[@]}" "$SSH_USER@$SSH_HOST" \
  "chmod 700 '$stage' '$stage/transactional-deploy.sh' '$stage/validate-env-contract.sh'; \
   find '$stage/env' -type d -exec chmod 700 {} +; \
   find '$stage/env' -type f -exec chmod 600 {} +"

echo "Validated environment bundle staged for $new_sha"
