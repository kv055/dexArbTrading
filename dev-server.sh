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

echo "dev-server: migrating the local database"
bash "$ROOT/scripts/migrate-local-db.sh"

echo "dev-server: starting backend services (detached)"
docker compose --project-directory "$ROOT" -f "$ROOT/docker-compose.yml" up -d --build "${compose_services[@]}"

# Tailnet endpoint (Tailscale Serve): expose Vite on the tailnet over HTTPS on
# a dedicated port. This never touches the :443 serve config (if any).
# Set DEV_NO_TAILNET=1 to skip, or TAILNET_HTTPS_PORT to change the port.
TAILNET_HTTPS_PORT="${TAILNET_HTTPS_PORT:-8443}"
TAILNET_DNS=""
if [[ -n "${DEV_NO_TAILNET:-}" ]]; then
	echo "dev-server: DEV_NO_TAILNET is set; skipping tailnet endpoint" >&2
elif ! command -v tailscale >/dev/null 2>&1; then
	echo "dev-server: tailscale not found; skipping tailnet endpoint (local-only)" >&2
elif ! command -v python3 >/dev/null 2>&1; then
	echo "dev-server: python3 not found; skipping tailnet endpoint (local-only)" >&2
else
	TAILNET_DNS="$(tailscale status --json 2>/dev/null | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d.get("BackendState")=="Running"; name=d["Self"]["DNSName"].rstrip("."); assert name; print(name)' 2>/dev/null || true)"
	if [[ -z "$TAILNET_DNS" ]]; then
		echo "dev-server: tailnet is not up; skipping tailnet endpoint (local-only)" >&2
	fi
fi

# tmux sessions inherit the server environment, not ours, so Vite-bound
# variables must be inlined in the tmux command (like SESSION_COOKIE_SECURE).
VITE_ENV="SESSION_COOKIE_SECURE=false"
if [[ -n "$TAILNET_DNS" ]]; then
	# Allow the tailnet hostname through Vite's host check.
	VITE_ENV+=" __VITE_ADDITIONAL_SERVER_ALLOWED_HOSTS=$TAILNET_DNS"
fi

if tmux has-session -t =vite 2>/dev/null; then
	echo "dev-server: tmux session 'vite' already exists; leaving it running" >&2
else
	echo "dev-server: starting Vite in detached tmux session 'vite' at http://localhost:5173"
	tmux new-session -d -s vite -c "$FRONTEND" "$VITE_ENV npm run dev"
fi

# Point the tailnet endpoint at the local Vite listener. Re-running is a no-op
# when this exact mapping already exists.
TAILNET_UP=false
if [[ -n "$TAILNET_DNS" ]]; then
	TAILNET_URL="https://$TAILNET_DNS:$TAILNET_HTTPS_PORT"
	serve_status="$(tailscale serve status 2>/dev/null || true)"
	if [[ "$serve_status" == *":$TAILNET_HTTPS_PORT"* && "$serve_status" == *"127.0.0.1:5173"* ]]; then
		echo "dev-server: tailnet endpoint already serving at $TAILNET_URL; leaving it as-is"
		TAILNET_UP=true
	elif serve_err="$(tailscale serve --bg --https="$TAILNET_HTTPS_PORT" http://127.0.0.1:5173 2>&1)"; then
		echo "dev-server: tailnet endpoint up at $TAILNET_URL"
		TAILNET_UP=true
	else
		echo "dev-server: WARNING: tailscale serve failed; tailnet endpoint unavailable" >&2
		if [[ "$serve_err" == *"Access denied"* ]]; then
			echo "dev-server: one-time fix (persistent): sudo tailscale set --operator=${USER:-$(id -un)}" >&2
		else
			echo "$serve_err" | sed 's/^/dev-server: tailscale: /' >&2
		fi
	fi
fi

if [[ "$TAILNET_UP" == true ]]; then
	# The endpoint 502s until Vite listens on localhost (e.g. a stale tmux
	# session bound elsewhere); warn loudly instead of claiming success.
	for _ in 1 2 3 4 5 6; do
		if (echo > /dev/tcp/127.0.0.1/5173) 2>/dev/null; then
			break
		fi
		sleep 1
	done
	if ! (echo > /dev/tcp/127.0.0.1/5173) 2>/dev/null; then
		echo "dev-server: WARNING: nothing listens on 127.0.0.1:5173; the tailnet endpoint will 502 until Vite binds localhost (try: tmux kill-session -t vite, then re-run dev-server.sh)" >&2
	fi
fi

echo "dev-server: backend and Vite are running detached"
echo "dev-server: attach to Vite logs with: tmux attach -t vite"
echo "dev-server: stop Vite with: tmux kill-session -t vite"

# Advisory only: dependency findings or registry outages must not fail startup.
node "$ROOT/scripts/audit-frontend-dependencies.cjs" "$FRONTEND" || \
	echo "dev-server: WARNING: dependency audit could not run; check manually with npm audit --include=dev in $FRONTEND" >&2
