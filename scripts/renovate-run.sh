#!/usr/bin/env bash
# renovate-run.sh — run self-hosted Renovate once. Shared by BOTH pipelines
# (.github/workflows/ci-cd.yml → GitHub repos, .gitea/workflows/ci.yml → Gitea
# repos) so the run logic cannot drift between platforms.
#
# What it does:
#   1. Builds one global config: renovate/shared.json (the dependency policy,
#      applied to every repo as its defaults) deep-merged with
#      renovate/global.json (bot settings). Repo renovate.json files only add
#      repo-specific rules and never reference another repo by name, because
#      the repos are named differently on GitHub and Gitea.
#   2. Stages commons/ where each service clone's `../commons` resolves.
#      Every service go.mod carries `replace commons => ../commons`; Renovate
#      clones repos to <baseDir>/repos/<platform>/<owner>/<repo>, so without
#      <baseDir>/repos/<platform>/<owner>/commons its `go get` fails and every
#      Go PR ships a stale go.sum.
#   3. Copies the post-upgrade task scripts (scripts/renovate/) into the
#      container at /tmp/renovate-tools, then runs the Renovate image.
#      Files move in/out with `docker cp`, never bind
#      mounts: on the Gitea runner the daemon is Docker-in-Docker, where a
#      bind-mount path resolves on the daemon host, not in this job.
#
# Env:
#   RENOVATE_IMAGE             image to run (required; pinned by the caller)
#   RENOVATE_TOKEN             platform token the bot acts with (required)
#   RENOVATE_PLATFORM          github | gitea (default: github)
#   RENOVATE_ENDPOINT          platform API endpoint (gitea: <url>/api/v1/)
#   RENOVATE_REPOSITORIES      JSON array; overrides global.json's list
#   RENOVATE_OWNER             owner of the repos (staging path; default kv055)
#   RENOVATE_GITHUB_COM_TOKEN  github.com token for lookups/changelogs (gitea)
#   RENOVATE_CACHE_DIR         host dir carried between runs (optional). Only
#                              Renovate's own cache (<baseDir>/cache/renovate:
#                              repository + package lookups) round-trips; the
#                              Go/npm module caches and the OSV database (>1 GB)
#                              are re-fetched, so CI cache quota is not evicted.
#   DOCKERHUB_USERNAME, DOCKERHUB_TOKEN  optional authenticated Docker Hub lookups
#   RENOVATE_DRY_RUN           optional (lookup | full)
#   LOG_LEVEL                  default info
set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
: "${RENOVATE_IMAGE:?RENOVATE_IMAGE is required}"
: "${RENOVATE_TOKEN:?RENOVATE_TOKEN is required}"
export RENOVATE_PLATFORM="${RENOVATE_PLATFORM:-github}"
export LOG_LEVEL="${LOG_LEVEL:-info}"
owner="${RENOVATE_OWNER:-kv055}"

work=$(mktemp -d)
cid=""
cleanup() {
  if [ -n "$cid" ]; then docker rm -f "$cid" >/dev/null 2>&1 || true; fi
  rm -rf "$work"
}
trap cleanup EXIT

# --- 1. global config = policy + bot settings ---
jq -s '(.[0] | del(."$schema", .description)) * (.[1] | del(."$schema"))' \
  "$REPO_ROOT/renovate/shared.json" "$REPO_ROOT/renovate/global.json" > "$work/config.json"

# --- 2. base dir with commons staged + previous cache ---
base="$work/base"
mkdir -p "$base/cache" "$base/repos/$RENOVATE_PLATFORM/$owner"
cp -a "$REPO_ROOT/commons" "$base/repos/$RENOVATE_PLATFORM/$owner/commons"
test -f "$base/repos/$RENOVATE_PLATFORM/$owner/commons/go.mod"
if [ -n "${RENOVATE_CACHE_DIR:-}" ] && [ -d "$RENOVATE_CACHE_DIR" ]; then
  mkdir -p "$base/cache/renovate"
  cp -a "$RENOVATE_CACHE_DIR/." "$base/cache/renovate/"
fi
# The Renovate image runs as a non-root user; docker cp keeps these modes.
chmod -R a+rwX "$base"

# --- 3. run ---
opts=(
  -e RENOVATE_CONFIG_FILE=/tmp/renovate-config.json
  -e RENOVATE_BASE_DIR=/tmp/renovate
  -e RENOVATE_PLATFORM
  -e RENOVATE_TOKEN
  -e LOG_LEVEL
)
for var in RENOVATE_ENDPOINT RENOVATE_REPOSITORIES RENOVATE_GITHUB_COM_TOKEN RENOVATE_DRY_RUN; do
  if [ -n "${!var:-}" ]; then opts+=(-e "$var"); fi
done
if [ -n "${DOCKERHUB_TOKEN:-}" ]; then
  RENOVATE_HOST_RULES="$(jq -nc --arg u "${DOCKERHUB_USERNAME:-}" --arg p "$DOCKERHUB_TOKEN" \
    '[{hostType: "docker", matchHost: "docker.io", username: $u, password: $p}]')"
  export RENOVATE_HOST_RULES
  opts+=(-e RENOVATE_HOST_RULES)
fi

cid=$(docker create "${opts[@]}" "$RENOVATE_IMAGE")
docker cp "$work/config.json" "$cid:/tmp/renovate-config.json"
docker cp "$base/." "$cid:/tmp/renovate"
# Post-upgrade task scripts (allowedCommands in renovate/global.json).
docker cp "$REPO_ROOT/scripts/renovate/." "$cid:/tmp/renovate-tools"
rc=0
docker start -a "$cid" || rc=$?

if [ "$rc" -eq 0 ] && [ -n "${RENOVATE_CACHE_DIR:-}" ]; then
  rm -rf "$RENOVATE_CACHE_DIR"
  mkdir -p "$RENOVATE_CACHE_DIR"
  docker cp "$cid:/tmp/renovate/cache/renovate/." "$RENOVATE_CACHE_DIR/" || echo "WARN: could not export Renovate cache" >&2
fi
exit "$rc"
