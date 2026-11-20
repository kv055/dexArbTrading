#!/usr/bin/env bash
# Apply dexUnifiedApi schema migrations to the local Compose database before
# app containers start. Images built from sources that predate the migrate
# command still apply their schema at startup, so they are skipped rather than
# handed an argument they would ignore while starting a second API server.
set -Eeuo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ -n "${COMPOSE:-}" ]]; then
  read -ra compose <<<"$COMPOSE"
else
  compose=(docker compose --project-directory "$root" -f "$root/docker-compose.yml" -f "$root/docker-compose.override.yml")
fi
cd "$root"

"${compose[@]}" build unified-api
"${compose[@]}" up -d --wait --wait-timeout 180 postgres
image="$("${compose[@]}" config --format json unified-api | python3 -c \
  'import json, sys; print(json.load(sys.stdin)["services"]["unified-api"]["image"])')"
label="$(docker image inspect \
  --format '{{index .Config.Labels "dexarbtrading.unified-api.migrate"}}' "$image")"
if [[ "$label" != 1 ]]; then
  echo "unified-api predates the migrate command; it applies its schema at startup"
  exit 0
fi
"${compose[@]}" run --rm --no-deps unified-api ./server migrate
