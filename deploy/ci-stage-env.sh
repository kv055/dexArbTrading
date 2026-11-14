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
: "${RELEASE_ARTIFACTS:?RELEASE_ARTIFACTS must contain baseline.json and receipts}"
[[ -f "$RELEASE_ARTIFACTS/baseline.json" && -d "$RELEASE_ARTIFACTS/receipts" ]] || {
  echo 'missing release artifacts' >&2; exit 1;
}
bundle="$(mktemp -d)"
incoming=""
cleanup() {
  rm -rf "$bundle"
  if [[ -n "$incoming" ]]; then
    ssh "${SSH_OPTS[@]}" "$SSH_USER@$SSH_HOST" \
      "rm -rf -- $(printf '%q' "$incoming")" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT
chmod 700 "$bundle"

mkdir -p \
  "$bundle/dexUnifiedApi" \
  "$bundle/dexMonitor" \
  "$bundle/dexTakerController" \
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
decrypt_copy dexTakerController/.env.enc "$bundle/dexTakerController/.env" \
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

q_repo="$(printf '%q' "$REPO_DIR")"
# shellcheck disable=SC2029
incoming="$(ssh "${SSH_OPTS[@]}" "$SSH_USER@$SSH_HOST" \
  "set -eu; umask 077; mkdir -p $q_repo/.deploy/incoming; mktemp -d $q_repo/.deploy/incoming/$new_sha.XXXXXXXX")"
[[ "$incoming" == "$REPO_DIR/.deploy/incoming/$new_sha."* &&
   "${incoming##*/}" =~ ^[0-9a-f]{40}\.[A-Za-z0-9]{8}$ ]] || {
  incoming=''
  echo 'invalid incoming directory returned by VPS' >&2; exit 1;
}
scp -q "${SSH_OPTS[@]}" -r "$bundle" \
  "$SSH_USER@$SSH_HOST:$incoming/env"
scp -q "${SSH_OPTS[@]}" \
  "$repo_root/scripts/validate-env-contract.sh" \
  "$repo_root/scripts/image-service-inputs.sh" \
  "$repo_root/scripts/release-images.py" \
  "$RELEASE_ARTIFACTS/baseline.json" \
  "$repo_root/deploy/transactional-deploy.sh" \
  "$SSH_USER@$SSH_HOST:$incoming/"
scp -q "${SSH_OPTS[@]}" -r "$RELEASE_ARTIFACTS/receipts" "$SSH_USER@$SSH_HOST:$incoming/"
q_incoming="$(printf '%q' "$incoming")"
# shellcheck disable=SC2029
ssh "${SSH_OPTS[@]}" "$SSH_USER@$SSH_HOST" \
  "set -eu; chmod 700 $q_incoming/transactional-deploy.sh $q_incoming/validate-env-contract.sh \
     $q_incoming/image-service-inputs.sh; \
   find $q_incoming/env -type d -exec chmod 700 {} +; \
   find $q_incoming/env -type f -exec chmod 600 {} +; \
   python3 $q_incoming/release-images.py publish-stage --repo $q_repo --incoming $q_incoming --sha $new_sha"
incoming=""

echo "Validated environment bundle staged for $new_sha"
