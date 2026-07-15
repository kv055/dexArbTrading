#!/usr/bin/env bash
# Canonical runner teardown. Gitea API credentials stay on this provisioning
# machine; only credential-free Docker cleanup runs on the homeserver.
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
ENV_GITEA="$SCRIPT_DIR/.env.gitea"

[[ -f "$ENV_GITEA" ]] || { echo "ERROR: missing $ENV_GITEA" >&2; exit 1; }
set -a
# shellcheck disable=SC1090
. "$ENV_GITEA"
set +a
# shellcheck source=../manifest.sh
. "$REPO_ROOT/deploy/provision/manifest.sh"

: "${GITEA_URL:?GITEA_URL not set in .env.gitea}"
: "${GITEA_OWNER:?GITEA_OWNER not set in .env.gitea}"
: "${GITEA_TOKEN:?GITEA_TOKEN not set in .env.gitea}"
: "${RUNNER_NAME:=homeserver-1}"
: "${HOMESERVER_SSH_USER:=gitea-runner}"
: "${HOMESERVER_SSH_HOST:?HOMESERVER_SSH_HOST not set in .env.gitea}"
: "${HOMESERVER_RUNNER_DIR:=~/gitea-runner}"

if [[ "${1-}" != "--yes" ]]; then
  read -r -p "Remove runner '$RUNNER_NAME', its DinD cache, and registration? [y/N] " answer
  [[ "$answer" =~ ^[Yy]$ ]] || { echo "Cancelled."; exit 0; }
fi

GITEA_URL="${GITEA_URL%/}"
API="$GITEA_URL/api/v1"
AUTH=(-H "Authorization: Bearer $GITEA_TOKEN")
SSH_TARGET="$HOMESERVER_SSH_USER@$HOMESERVER_SSH_HOST"

echo "Disabling reconciliation and removing the homeserver runner stack..."
# HOMESERVER_RUNNER_DIR comes from the trusted, local .env.gitea file.
# shellcheck disable=SC2029
ssh "$SSH_TARGET" "cd $HOMESERVER_RUNNER_DIR && ./teardown.sh"

runner_base="/repos/$GITEA_OWNER/$ROOT_REPO/actions/runners"
if curl -fsS -o /dev/null "${AUTH[@]}" "$API/orgs/$GITEA_OWNER" 2>/dev/null; then
  runner_base="/orgs/$GITEA_OWNER/actions/runners"
fi

runner_ids="$(curl -fsS "${AUTH[@]}" "$API$runner_base" \
  | jq -r --arg n "$RUNNER_NAME" '.runners[]? | select(.name == $n) | .id')"
for id in $runner_ids; do
  curl -fsS -X DELETE "${AUTH[@]}" "$API$runner_base/$id" >/dev/null
  echo "Unregistered runner id=$id"
done

echo "Teardown complete. Run setup-gitea.sh to enable and recreate the runner."
