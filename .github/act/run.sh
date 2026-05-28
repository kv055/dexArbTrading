#!/usr/bin/env bash
# Test GitHub Actions workflow config changes locally.
# Use when editing .github/workflows/*.yml — not a substitute for go test / npm test.
#
# Prerequisites: Docker running, GH_PAT set in .env (see .env.example CI section)
#
# Usage: .github/act/run.sh [command]
#   test-go        Go tests job (PR event)
#   test-frontend  Frontend lint, build, e2e job (PR event)
#   test           All test jobs — skips build/deploy (PR event)
#   bisq-bump      Bisq version bump workflow (workflow_dispatch)
#   ci             Full CI as push to main
#   <job-id>       Any specific job by ID (PR event)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
IMAGE="dexarbtrading-act"

if ! docker image inspect "$IMAGE" &>/dev/null; then
  docker build -t "$IMAGE" "$SCRIPT_DIR"
fi

run_act() {
  docker run --rm \
    -v /var/run/docker.sock:/var/run/docker.sock \
    -v "$REPO_ROOT:/workspace" \
    -w /workspace \
    "$IMAGE" \
    --config .github/act/actrc \
    "$@"
}

CMD=${1:-test}
case "$CMD" in
  test-go)       run_act pull_request -j test-go ;;
  test-frontend) run_act pull_request -j test-frontend ;;
  test)          run_act pull_request ;;
  bisq-bump)     run_act workflow_dispatch -W .github/workflows/bisq-version-bump.yml ;;
  ci)            run_act push --eventpath .github/act/push-main.json ;;
  *)             run_act pull_request -j "$CMD" ;;
esac
