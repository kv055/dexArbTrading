#!/usr/bin/env bash
set -euo pipefail

# Lefthook's post-checkout hook passes:
#   $1 old HEAD, $2 new HEAD, $3 branch checkout flag (1 for branch checkout)
old_revision=${1:-}
new_revision=${2:-}
branch_checkout=${3:-0}

if [[ "$branch_checkout" != "1" ]]; then
	exit 0
fi

root_dir=$(git rev-parse --show-toplevel)
compose_file="$root_dir/docker-compose.yml"

if [[ ! -f "$compose_file" || "$old_revision" =~ ^0+$ || "$new_revision" =~ ^0+$ ]]; then
	echo "dev-server: checkout has no comparable commit; skipping automatic rebuild" >&2
	exit 0
fi

compose=(docker compose --project-directory "$root_dir" -f "$compose_file")

# Keep this in sync with the allowlist in dev-server.sh. External-image-only
# services such as postgres and lnd are intentionally excluded.
dev_services=(
	tor
	unified-api
	unified-snapshot
	mm-controller
	fiat-controller
	dexmonitor
)

mapfile -t running_services < <("${compose[@]}" ps --status running --services 2>/dev/null || true)
if ((${#running_services[@]} == 0)); then
	exit 0
fi

is_running() {
	local candidate=$1 running
	for running in "${running_services[@]}"; do
		if [[ "$running" == "$candidate" ]]; then
			return 0
		fi
	done
	return 1
}

affected_services=()
add_service() {
	local candidate=$1 existing
	if ! is_running "$candidate"; then
		return
	fi
	for existing in "${affected_services[@]}"; do
		if [[ "$existing" == "$candidate" ]]; then
			return
		fi
	done
	affected_services+=("$candidate")
}

add_all_buildable_services() {
	local service
	for service in "${dev_services[@]}"; do
		add_service "$service"
	done
}

mapfile -t changed_paths < <(git diff --name-only "$old_revision" "$new_revision")
for path in "${changed_paths[@]}"; do
	case "$path" in
		docker-compose.yml|.gitmodules)
			add_all_buildable_services
			;;
		commons/*)
			add_service unified-api
			add_service unified-snapshot
			add_service mm-controller
			add_service fiat-controller
			add_service dexmonitor
			;;
		dexUnifiedApi/Dockerfile.tor)
			add_service tor
			;;
		dexUnifiedApi|dexUnifiedApi/*)
			add_service unified-api
			add_service unified-snapshot
			;;
		dexMonitor|dexMonitor/*)
			add_service dexmonitor
			;;
		dexMmController|dexMmController/*)
			add_service mm-controller
			;;
		dexFiatController|dexFiatController/*)
			add_service fiat-controller
			;;
		# Files outside known build inputs may still affect a Docker context or
		# Compose behavior. Prefer a safe rebuild over a stale running image.
		*)
			add_all_buildable_services
			;;
	esac
done

if ((${#affected_services[@]} == 0)); then
	echo "dev-server: branch checkout changed no running dev service inputs"
	exit 0
fi

echo "dev-server: branch checkout changed: ${affected_services[*]}"
echo "dev-server: rebuilding affected running services"
"${compose[@]}" up -d --build "${affected_services[@]}"
