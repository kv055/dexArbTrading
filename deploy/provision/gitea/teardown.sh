#!/usr/bin/env bash
# Low-level homeserver cleanup invoked by teardown-gitea.sh.
# It intentionally leaves .reconcile-disabled behind so the permanent systemd
# timer does not recreate the runner after an operator-requested teardown.
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

COMPOSE="act-runner-compose.yml"
touch .reconcile-disabled

exec 9>.reconcile.lock
flock -x 9

mapfile -t image_ids < <(docker compose -f "$COMPOSE" images -q 2>/dev/null | sort -u)
docker compose -f "$COMPOSE" down -v --remove-orphans || true

for image_id in "${image_ids[@]}"; do
  [[ -n "$image_id" ]] && docker image rm "$image_id" >/dev/null 2>&1 || true
done

rm -f act-runner.env .reconcile-state .reconcile-disk-state

echo "Runner containers, volumes, images, and local registration data removed."
echo "Automatic reconciliation remains disabled until setup-gitea.sh is run."
