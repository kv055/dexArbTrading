#!/usr/bin/env bash
# Run sonar-scanner against the local SonarQube Docker stack (make sonar-up first).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

SONAR_HOST_URL="${SONAR_HOST_URL:-http://localhost:9000}"

if [[ -f .env.sonar ]]; then
  # shellcheck disable=SC1091
  set -a
  source .env.sonar
  set +a
fi

if [[ -z "${SONAR_TOKEN:-}" ]]; then
  echo "✖ SONAR_TOKEN is not set."
  echo "  1. make sonar-up"
  echo "  2. Open ${SONAR_HOST_URL} (login admin/admin on first run, change password)"
  echo "  3. My Account → Security → Generate Token"
  echo "  4. cp .env.sonar.example .env.sonar  # add SONAR_TOKEN=..."
  exit 1
fi

echo "Waiting for SonarQube at ${SONAR_HOST_URL} ..."
deadline=$((SECONDS + 600))
until curl -sf "${SONAR_HOST_URL}/api/system/status" | grep -q '"status":"UP"'; do
  if (( SECONDS > deadline )); then
    echo "✖ SonarQube did not become ready within 10 minutes. Try: make sonar-up"
    exit 1
  fi
  sleep 5
done
echo "✓ SonarQube is UP"

if git submodule status --recursive 2>/dev/null | grep -q '^-'; then
  echo "⚠ Some submodules are not initialized. Run: git submodule update --init --recursive"
fi

echo "Running sonar-scanner ..."
docker run --rm \
  --network host \
  -v "${ROOT}:/usr/src" \
  -w /usr/src \
  -e SONAR_HOST_URL="${SONAR_HOST_URL}" \
  -e SONAR_TOKEN \
  sonarsource/sonar-scanner-cli

echo ""
echo "✓ Scan complete — review results at ${SONAR_HOST_URL}/dashboard?id=dexArbTrading"
