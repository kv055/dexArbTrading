#!/usr/bin/env bash
# Block until SonarQube responds on localhost:9000.
set -euo pipefail

SONAR_HOST_URL="${SONAR_HOST_URL:-http://localhost:9000}"
deadline=$((SECONDS + 600))

echo "Waiting for SonarQube at ${SONAR_HOST_URL} ..."
until curl -sf "${SONAR_HOST_URL}/api/system/status" | grep -q '"status":"UP"'; do
  if (( SECONDS > deadline )); then
    echo "✖ SonarQube did not become ready within 10 minutes."
    exit 1
  fi
  sleep 5
done
echo "✓ SonarQube is ready at ${SONAR_HOST_URL}"
