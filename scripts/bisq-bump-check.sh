#!/usr/bin/env bash
# bisq-bump-check.sh — shared detection + patch-compatibility check for the
# nightly Bisq v1 version bump. Used by BOTH bump workflows
# (.github/workflows/bisq-version-bump.yml and .gitea/workflows/bisq-version-bump.yml)
# so the logic cannot drift between platforms.
#
# What it does:
#   1. Reads the CURRENT version from dexUnifiedApi/Dockerfile.bisq's ARG —
#      the single source of truth (docker-compose.yml mirrors it; reading the
#      compose value while sed-ing the Dockerfile is exactly the drift bug
#      that made the old bump job fail on every run).
#   2. Fetches the latest upstream release tag from the GitHub API.
#   3. Decides whether a bump is warranted (strictly newer + tag exists).
#   4. If so, clones bisq at the NEW tag and test-applies every patch in
#      dexUnifiedApi/bisq-patches/ — the bisq image build applies these with
#      git apply --3way, so an incompatible upstream refactor must be caught
#      here, before any PR is opened.
#
# A bump carries the new tag's peeled commit (latest_commit): Dockerfile.bisq
# builds that exact commit (ARG BISQ_COMMIT), never the floating tag, so a
# re-cut tag cannot silently change a build. When the version is unchanged
# but the tag moved under it, the check reports bump=true with repin=true so
# the pin is refreshed without a version change.
#
# Outputs (stdout as key=value, and appended to $GITHUB_OUTPUT when set):
#   current=<x.y.z>   latest=<x.y.z>   bump=true|false   repin=true|false
#   latest_commit=<sha|none>
#   patches_ok=true|false|skipped      failed_patch=<basename|none>
#
# Env overrides (testing): CURRENT_OVERRIDE, CURRENT_COMMIT_OVERRIDE,
#   LATEST_OVERRIDE, PATCH_DIR, UPSTREAM_REPO,
#   GITHUB_TOKEN (optional API auth).
set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DOCKERFILE="$REPO_ROOT/dexUnifiedApi/Dockerfile.bisq"
PATCH_DIR="${PATCH_DIR:-$REPO_ROOT/dexUnifiedApi/bisq-patches}"
UPSTREAM_REPO="${UPSTREAM_REPO:-https://github.com/bisq-network/bisq.git}"

emit() {
  echo "$1"
  if [ -n "${GITHUB_OUTPUT:-}" ]; then echo "$1" >> "$GITHUB_OUTPUT"; fi
}

api_curl() {
  if [ -n "${GITHUB_TOKEN:-}" ]; then
    curl -fsSL -H "Authorization: Bearer $GITHUB_TOKEN" -H "Accept: application/vnd.github+json" "$@"
  else
    curl -fsSL -H "Accept: application/vnd.github+json" "$@"
  fi
}

# --- 1. current version + commit (Dockerfile ARGs = source of truth) ---
current="${CURRENT_OVERRIDE:-$(grep -oP '^ARG BISQ_VERSION=\K[0-9]+\.[0-9]+\.[0-9]+' "$DOCKERFILE" | head -1)}"
if [ -z "$current" ]; then
  echo "ERROR: could not parse ARG BISQ_VERSION from $DOCKERFILE" >&2
  exit 1
fi
emit "current=$current"
current_commit="${CURRENT_COMMIT_OVERRIDE:-$(grep -oP '^ARG BISQ_COMMIT=\K[0-9a-f]{40}' "$DOCKERFILE" | head -1)}"
if [ -z "$current_commit" ]; then
  echo "ERROR: could not parse ARG BISQ_COMMIT from $DOCKERFILE" >&2
  exit 1
fi

# Peeled commit a tag resolves to (annotated tags need the ^{} suffix;
# lightweight tags resolve directly). Empty when the tag does not exist.
tag_commit() {
  local sha
  sha=$(git ls-remote "$UPSTREAM_REPO" "refs/tags/v${1}^{}" 2>/dev/null | awk '{print $1}')
  if [ -z "$sha" ]; then
    sha=$(git ls-remote "$UPSTREAM_REPO" "refs/tags/v${1}" 2>/dev/null | awk '{print $1}')
  fi
  printf '%s' "$sha"
}

# --- 2. latest upstream release ---
latest="${LATEST_OVERRIDE:-$(api_curl "https://api.github.com/repos/bisq-network/bisq/releases/latest" | jq -r '.tag_name' | sed 's/^v//')}"
if [ -z "$latest" ] || [ "$latest" = "null" ]; then
  echo "ERROR: could not determine latest upstream bisq release" >&2
  exit 1
fi
emit "latest=$latest"

# --- 3. bump decision ---
no_bump() {
  emit "bump=false"
  emit "repin=false"
  emit "latest_commit=none"
  emit "patches_ok=skipped"
  emit "failed_patch=none"
  echo "$1"
  exit 0
}

repin=false
if [ "$current" = "$latest" ]; then
  # Same version: only a silently re-cut tag justifies a bump (pin refresh).
  tag_now="$(tag_commit "$current")"
  if [ -z "$tag_now" ]; then
    no_bump "WARNING: bisq ${current} has no git tag (yet); skipping"
  fi
  if [ "$tag_now" = "$current_commit" ]; then
    no_bump "Up to date at ${current} (${current_commit})"
  fi
  repin=true
  echo "Upstream re-cut v${current}: pin ${current_commit} -> ${tag_now}"
fi
higher=$(printf '%s\n%s\n' "$current" "$latest" | sort -V | tail -1)
if [ "$higher" != "$latest" ]; then
  no_bump "Upstream ${latest} is not newer than current ${current}; skipping"
fi
if [ -z "$(tag_commit "$latest")" ]; then
  no_bump "WARNING: bisq ${latest} has no git tag yet; skipping"
fi
emit "bump=true"
emit "repin=$repin"

# --- 4. patch compatibility against the NEW version ---
workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT
echo "Cloning bisq v${latest} to verify patches from ${PATCH_DIR} ..."
git clone --depth 1 --branch "v${latest}" --quiet "$UPSTREAM_REPO" "$workdir/bisq"

patches_ok=true
failed_patch=none
for p in "$PATCH_DIR"/*.patch; do
  [ -e "$p" ] || { echo "ERROR: no patches found in $PATCH_DIR" >&2; exit 1; }
  name=$(basename "$p")
  if git -C "$workdir/bisq" apply --3way "$p"; then
    echo "patch OK: $name"
  else
    echo "patch FAILED against v${latest}: $name" >&2
    patches_ok=false
    failed_patch="$name"
    break
  fi
done

# Pin the exact content the patches were verified against, not the tag as
# resolved earlier: a tag move in between must not change the bump.
latest_commit="$(git -C "$workdir/bisq" rev-parse HEAD)"
emit "latest_commit=$latest_commit"
emit "patches_ok=$patches_ok"
emit "failed_patch=$failed_patch"
