#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TOOLS_DIR="${BACKEND_TOOLS_DIR:-/tmp/dexarb-backend-tools}"
STATICCHECK="$TOOLS_DIR/staticcheck"
DEADCODE="$TOOLS_DIR/deadcode"
export GOCACHE="${GOCACHE:-/tmp/dexarb-backend-gocache}"
export STATICCHECK_CACHE="${STATICCHECK_CACHE:-/tmp/dexarb-staticcheck-cache}"

# Style cleanup and dependency deprecation migrations are tracked separately.
# Unreachable code is reviewed by deadcode below, which is more accurate than
# Staticcheck's package-local U1000 check for this workspace.
STATICCHECK_CHECKS="all,-ST*,-SA1019,-U1000"

if [[ ! -x "$STATICCHECK" || ! -x "$DEADCODE" ]]; then
	echo "backend tools are missing; run: make backend-tools" >&2
	exit 2
fi

modules=(commons dexFiatController dexTakerController dexMonitor dexUnifiedApi)

module_packages() {
	local module="$1"
	(
		cd "$ROOT/$module"
		go list ./... \
			| awk -v module="$module" '
				!/\/react-frontend\// &&
				!/\/internal\/scraping(\/|$)/ &&
				!(module == "dexFiatController" && $0 == "dexFiatController")
			'
	)
}

for module in "${modules[@]}"; do
	echo "==> $module"
	mapfile -t packages < <(module_packages "$module")
	if [[ ${#packages[@]} -eq 0 ]]; then
		echo "no backend packages found" >&2
		exit 1
	fi

	(
		cd "$ROOT/$module"
		go test "${packages[@]}"
		go vet "${packages[@]}"
		go mod tidy -diff
		"$STATICCHECK" -checks="$STATICCHECK_CHECKS" "${packages[@]}"
	)
done

echo "==> unreachable backend functions (review report)"
for module in "${modules[@]}"; do
	echo "[$module]"
	if [[ "$module" == "commons" ]]; then
		echo "shared library module: review reachability from consuming service binaries"
		continue
	fi
	if [[ "$module" == "dexFiatController" ]]; then
		packages=(./...)
	else
		mapfile -t packages < <(module_packages "$module")
	fi
	(
		cd "$ROOT/$module"
		"$DEADCODE" -test "${packages[@]}" \
			| awk '!/(^|\/)internal\/scraping\// && !/(^|\/)react-frontend\//'
	)
done
