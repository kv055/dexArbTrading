#!/usr/bin/env bash
# service-ci.sh — PR/push checks for ONE service repo (dexUnifiedApi,
# dexMonitor, dexTakerController, dexFiatController).
#
# The services import `commons` via `replace commons => ../commons`, so they
# only build inside a root (dexPendulum) checkout. Each service repo therefore
# carries a thin CI workflow (.github/workflows/ci.yml and
# .gitea/workflows/ci.yml) that checks out the root repo at develop, puts the
# commit under test into its submodule, and runs this script — so the checks
# match the root pipeline and cannot drift per repo.
#
# Checks:
#   1. go test ./... for every Go module in the service (incl. nested ones,
#      e.g. dexFiatController/services/totp-api).
#   2. dexMonitor only: frontend install, API-contract check, lint, build and
#      unit tests (Playwright e2e stays in the root pipeline).
#   3. Image build (no push) for the service's images, using the same
#      context/Dockerfile map as the root pipeline (image-service-inputs.sh).
#      The Bisq daemon images (bisq, bisq2) are left to the root pipeline:
#      full Java builds, and their version pins have their own bump workflow.
#
# Usage: service-ci.sh <service-dir>     (run from the root checkout)
# Env:   VITEST_ARGS   extra args for `npm test` (Gitea: a longer timeout)
#        SKIP_IMAGES=1 skip step 3
set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"
service="${1:?usage: service-ci.sh <service-dir>}"
[ -f "$service/go.mod" ] || { echo "ERROR: $service/go.mod not found (run from a root checkout)" >&2; exit 1; }

case "$service" in
  dexUnifiedApi)      images=(unified-api unified-snapshot postgres-backup tor) ;;
  dexMonitor)         images=(dexmonitor) ;;
  dexTakerController) images=(mm-controller) ;;
  dexFiatController)  images=(fiat-controller) ;;
  *) echo "ERROR: unknown service $service" >&2; exit 1 ;;
esac

echo "== $service @ $(git -C "$service" rev-parse --short HEAD) (root develop @ $(git rev-parse --short HEAD))"

# --- 1. Go tests ---
while IFS= read -r mod; do
  dir=$(dirname "$mod")
  echo "::group::go test ($dir)"
  (cd "$dir" && go test ./...)
  echo "::endgroup::"
done < <(git -C "$service" ls-files -- 'go.mod' '**/go.mod' | sed "s#^#$service/#")

# --- 2. dexMonitor frontend ---
if [ "$service" = dexMonitor ]; then
  echo "::group::frontend"
  (
    cd dexMonitor/react-frontend
    npm ci --no-audit --no-fund
    npm run check:api-contract
    npm run lint
    npm run build
    # shellcheck disable=SC2086  # VITEST_ARGS is intentionally word-split
    npm test -- ${VITEST_ARGS:-}
  )
  echo "::endgroup::"
fi

# --- 3. image builds (no push) ---
if [ "${SKIP_IMAGES:-0}" != 1 ]; then
  # shellcheck source=scripts/image-service-inputs.sh
  source scripts/image-service-inputs.sh
  for image in "${images[@]}"; do
    read -r context dockerfile < <(image_service_build "$image")
    echo "::group::docker build $image ($dockerfile, context $context)"
    docker build --pull -f "$dockerfile" -t "service-ci/$image:check" "$context"
    echo "::endgroup::"
  done
fi

echo "== $service: all checks passed"
