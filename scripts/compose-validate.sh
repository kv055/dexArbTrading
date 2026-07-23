#!/usr/bin/env bash
# Assert the docker-compose network topology is what the bridge-network
# migration expects. Runs `docker compose config` (which fully resolves
# interpolation and env_file merges) and checks:
#   - no service uses network_mode: host
#   - mm-controller reaches every upstream by docker DNS, not localhost
#   - lnd's TLS cert carries the "lnd" SAN so https://lnd:8080 verifies
#   - dexmonitor reaches mm-controller by docker DNS, not host.docker.internal
#
# Designed to run both locally (with real .env files) and in CI (without):
# any env_file referenced by a service that doesn't exist on disk is
# materialized as an empty placeholder for the duration of the check, then
# removed. Only files this script created are removed — pre-existing .env
# files are left untouched.
#
# Usage: bash scripts/compose-validate.sh [path/to/docker-compose.yml]
set -euo pipefail

COMPOSE_FILE="${1:-docker-compose.yml}"
PROJECT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$PROJECT_DIR"

if ! command -v docker >/dev/null 2>&1; then
	echo "✖ docker not found on PATH" >&2
	exit 2
fi

# env_file references look like "  - ./some/.env" or "  - ./some/.env:ro".
mapfile -t ENV_FILES < <(
	awk '
		/^[[:space:]]*env_file:/ { in_env=1; next }
		in_env && /^[[:space:]]*-/ {
			line=$0; sub(/^[[:space:]]*-[[:space:]]*/,"",line); sub(/:ro$/,"",line);
			gsub(/^\.\/+/, "", line); print line; next
		}
		in_env && !/^[[:space:]]*-/ { in_env=0 }
	' "$COMPOSE_FILE"
)

created=()
cleanup() {
	for f in "${created[@]:-}"; do
		if [ -f "$f" ] && [ ! -s "$f" ]; then
			rm -f "$f"
		fi
	done
	# Remove dirs we created only to host a placeholder .env (if empty).
	for d in dexUnifiedApi dexMonitor dexMmController; do
		if [ -d "$d" ] && [ -z "$(ls -A "$d" 2>/dev/null)" ]; then
			rmdir "$d" 2>/dev/null || true
		fi
	done
}
trap cleanup EXIT

for f in "${ENV_FILES[@]:-}"; do
	if [ -n "$f" ] && [ ! -f "$f" ]; then
		mkdir -p "$(dirname "$f")"
		: >"$f"
		created+=("$f")
	fi
done

CONFIG="$(docker compose -f "$COMPOSE_FILE" config 2>/dev/null)"

fail=0
assert_contains() {
	local label="$1" needle="$2"
	if printf '%s\n' "$CONFIG" | grep -qF -- "$needle"; then
		echo "✓ $label"
	else
		echo "✖ $label (missing: $needle)" >&2
		fail=1
	fi
}
assert_absent() {
	local label="$1" needle="$2"
	if printf '%s\n' "$CONFIG" | grep -qF -- "$needle"; then
		echo "✖ $label (forbidden but present: $needle)" >&2
		fail=1
	else
		echo "✓ $label"
	fi
}

# assert_in_service checks that needle appears within one service's resolved
# config block. Used for env vars that aren't unique across services (e.g.
# INTERNAL_API_SECRET is also set on unified-api/unified-snapshot, so a global
# grep would pass even if mm-controller's were removed).
assert_in_service() {
	local label="$1" service="$2" needle="$3"
	local block
	block=$(printf '%s\n' "$CONFIG" | awk -v svc="  ${service}:" '
		$0 == svc { inblk=1; print; next }
		inblk && /^  [A-Za-z][A-Za-z0-9_-]*:$/ { inblk=0 }
		inblk { print }
	')
	if printf '%s\n' "$block" | grep -qF -- "$needle"; then
		echo "✓ $label"
	else
		echo "✖ $label (missing in $service: $needle)" >&2
		fail=1
	fi
}

echo "Checking compose topology from $COMPOSE_FILE"

# No service may use host networking — everything must be on the bridge.
assert_absent "no service uses network_mode: host" "network_mode: host"

# mm-controller reaches every upstream by docker DNS.
assert_contains "mm-controller BISQ_GRPC_HOST=bisq:9998"         "BISQ_GRPC_HOST: bisq:9998"
assert_contains "mm-controller POSTGRES_HOST=postgres"           "POSTGRES_HOST: postgres"
assert_contains "mm-controller LND_HOST=https://lnd:8080"        "LND_HOST: https://lnd:8080"
assert_contains "mm-controller TOR_PROXY_URL=socks5h://tor:9050" "TOR_PROXY_URL: socks5h://tor:9050"
assert_contains "mm-controller WS via unified-api DNS (internal route)" "OFFER_PREMIUM_WSS_URL: ws://unified-api:5010/internal/ws/live_offers?cex=kraken"
assert_in_service "mm-controller INTERNAL_API_SECRET wired (secret-gated WS)" "mm-controller" "INTERNAL_API_SECRET:"

# lnd's cert must include the docker DNS name so https://lnd:8080 verifies.
assert_contains "lnd TLS cert includes lnd SAN" "--tlsextradomain=lnd"

# dexmonitor reaches mm-controller by docker DNS, not host.docker.internal.
assert_contains "dexmonitor reaches mm-controller by DNS" "MM_CONTROLLER_API_URL: http://mm-controller:5001"
assert_absent "no host.docker.internal in dexmonitor"     "host.docker.internal:5001"

# Maddy is receive-only: SMTP 25 is public, while IMAP is only exposed to the
# bridge network and no submission ports exist.
assert_contains "Maddy image is pinned" "image: foxcpp/maddy:0.9.5"
assert_contains "Maddy publishes SMTP 25" 'published: "25"'
assert_absent "Maddy does not publish IMAP 993" 'published: "993"'
assert_absent "no SMTP submission port 465" "target: 465"
assert_absent "no SMTP submission port 587" "target: 587"
assert_in_service "fiat-controller uses dedicated payment IMAP settings" "fiat-controller" "PAYMENT_EMAIL_IMAP_HOST: maddy"

if [ "$fail" -ne 0 ]; then
	echo >&2
	echo "✖ compose topology validation FAILED" >&2
	exit 1
fi
echo
echo "✓ compose topology validation passed"
