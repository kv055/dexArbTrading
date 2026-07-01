#!/usr/bin/env bash
# Tear down the Gitea Actions runner and leave NO artifacts on the host.
# Run on the homeserver where the runner is up.
#
#   bash teardown.sh
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"
COMPOSE="act-runner-compose.yml"

# ROOT_REPO from the shared manifest if setup-gitea.sh deployed it alongside the
# runner files; fall back for older runner dirs that predate the manifest.
# shellcheck source=manifest.sh
[[ -f manifest.sh ]] && . ./manifest.sh
: "${ROOT_REPO:=dexArbTrading}"

# 1. Best-effort: unregister the runner from Gitea (needs .env.gitea + jq + curl).
if [[ -f .env.gitea ]] && command -v curl >/dev/null && command -v jq >/dev/null; then
  # shellcheck disable=SC1091
  set -a; . .env.gitea; set +a
  GITEA_URL="${GITEA_URL%/}"; API="$GITEA_URL/api/v1"
  AUTH=(-H "Authorization: Bearer $GITEA_TOKEN")
  if curl -fsS -o /dev/null "${AUTH[@]}" "$API/orgs/$GITEA_OWNER" 2>/dev/null; then
    base="/orgs/$GITEA_OWNER/actions/runners"
  else
    base="/repos/$GITEA_OWNER/$ROOT_REPO/actions/runners"
  fi
  ids="$(curl -fsS "${AUTH[@]}" "$API$base" 2>/dev/null \
          | jq -r --arg n "${RUNNER_NAME:-homeserver-1}" '.runners[]? | select(.name==$n) | .id' 2>/dev/null || true)"
  for id in $ids; do
    curl -fsS -X DELETE "${AUTH[@]}" "$API$base/$id" >/dev/null 2>&1 \
      && echo "unregistered runner id=$id" || echo "WARN: could not unregister runner id=$id"
  done
fi

# 2. Stop runner + dind and remove ALL their volumes (job images/cache + registration).
docker compose -f "$COMPOSE" down -v --remove-orphans || true

# 3. Remove the two runner images (job images lived inside dind-storage, already gone).
docker image rm gitea/act_runner:latest docker:dind >/dev/null 2>&1 || true

# 4. Drop the generated env file holding the registration token.
rm -f act-runner.env

echo "Teardown complete — runner, its volumes, and pulled images removed."
echo "Verify: docker ps -a | grep gitea-  ;  docker volume ls | grep gitea  (both empty)"
