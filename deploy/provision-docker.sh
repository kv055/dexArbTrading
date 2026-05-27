#!/usr/bin/env bash
# Wrapper: builds the provisioning image on first run, then runs
# deploy/provision.sh inside it. The host needs only docker + a chosen
# SSH key pair; terraform/ansible/gh live inside the container.
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
IMAGE="dexarb-provision:latest"

SSH_PRIVATE_KEY="${SSH_PRIVATE_KEY:-$HOME/.ssh/id_ed25519}"
PASSTHRU=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --key) SSH_PRIVATE_KEY="$2"; shift 2 ;;
    *) PASSTHRU+=("$1"); shift ;;
  esac
done

[[ -f "$SSH_PRIVATE_KEY" ]]       || { echo "ERROR: SSH key not found: $SSH_PRIVATE_KEY" >&2; exit 1; }
[[ -f "${SSH_PRIVATE_KEY}.pub" ]]  || { echo "ERROR: missing pubkey: ${SSH_PRIVATE_KEY}.pub" >&2; exit 1; }

# Auto-build on first run (or when Dockerfile changes).
if ! docker image inspect "$IMAGE" >/dev/null 2>&1; then
  echo "==> Building $IMAGE (first run)"
  docker build -t "$IMAGE" -f "$SCRIPT_DIR/Dockerfile.provision" "$SCRIPT_DIR"
fi

# Resolve key paths to absolute (docker -v needs absolute paths).
KEY_ABS="$(readlink -f "$SSH_PRIVATE_KEY")"
KEY_PUB_ABS="$(readlink -f "${SSH_PRIVATE_KEY}.pub")"

# Only attach a TTY when both stdin AND stdout are real terminals.
# Otherwise (CI, piped output, harness-managed shell) `-it` makes docker
# buffer indefinitely and the script appears to hang.
TTY_FLAGS=()
if [[ -t 0 && -t 1 ]]; then
  TTY_FLAGS=(-it)
fi

HOST_UID="$(id -u)"
HOST_GID="$(id -g)"

# Chown back any root-owned files this run creates in the bind mount, even on failure.
cleanup() {
  docker run --rm --user 0:0 \
    -v "$REPO_ROOT":/repo \
    "$IMAGE" -c "chown -R $HOST_UID:$HOST_GID /repo/deploy" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker run --rm "${TTY_FLAGS[@]}" \
  -v "$REPO_ROOT":/repo \
  -v "$KEY_ABS":/keys/id_provision:ro \
  -v "$KEY_PUB_ABS":/keys/id_provision.pub:ro \
  -e SSH_PRIVATE_KEY=/keys/id_provision \
  -e IN_PROVISION_CONTAINER=1 \
  -w /repo \
  "$IMAGE" \
  /repo/deploy/provision.sh "${PASSTHRU[@]}"
