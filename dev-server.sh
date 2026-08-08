#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FRONTEND="$ROOT/dexMonitor/react-frontend"

for command in docker npm tmux; do
	if ! command -v "$command" >/dev/null 2>&1; then
		echo "dev-server: $command is required" >&2
		exit 1
	fi
done

if [[ ! -d "$FRONTEND/node_modules" ]]; then
	echo "dev-server: frontend dependencies are missing; run npm ci in $FRONTEND" >&2
	exit 1
fi

# Keep this an explicit allowlist. In particular, do not use a bare
# `docker compose up`: the normal production graph includes Bisq and the full
# observability stack. nginx is omitted because it depends on Grafana; Vite is
# the development web entrypoint and talks directly to the published backends.
compose_services=(
	postgres
	tor
	lnd
	unified-api
	unified-snapshot
	mm-controller
	fiat-controller
	dexmonitor
)

# Vite runs over local HTTP, so its login cookie must not carry Secure.
export SESSION_COOKIE_SECURE=false

echo "dev-server: starting backend services (detached)"
docker compose --project-directory "$ROOT" -f "$ROOT/docker-compose.yml" up -d --build "${compose_services[@]}"

if tmux has-session -t vite 2>/dev/null; then
	echo "dev-server: tmux session 'vite' already exists; leaving it running" >&2
else
	echo "dev-server: starting Vite in detached tmux session 'vite' at http://localhost:5173"
	tmux new-session -d -s vite -c "$FRONTEND" "SESSION_COOKIE_SECURE=false npm run dev"
fi

echo "dev-server: backend and Vite are running detached"
echo "dev-server: attach to Vite logs with: tmux attach -t vite"
echo "dev-server: stop Vite with: tmux kill-session -t vite"
