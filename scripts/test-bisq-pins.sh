#!/usr/bin/env bash
# Regression tests for SHA-pinned Bisq sources.
#
# Gitea run #348 failed because Dockerfile.bisq cloned the floating
# `v${BISQ_VERSION}` tag from a stale layer cache while bisq-patches had moved
# on. These tests lock in the fix without network access: a fixture upstream
# repo (local path remote) stands in for github.com, and fixture Dockerfiles
# stand in for dexUnifiedApi/Dockerfile.bisq*.
#
# Covered:
#   1. scripts/bisq-pin-args.sh accepts pinned Dockerfiles and rejects
#      floating-tag clones and missing/malformed pins (plus the real
#      Dockerfiles, when submodules are checked out).
#   2. scripts/bisq-bump-check.sh end to end via its *_OVERRIDE hooks:
#      version bump, up-to-date, re-cut tag re-pin, lightweight-tag fallback,
#      incompatible patch, and the patch-less bisq2 flavor.
set -Eeuo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
pin_args="$repo_root/scripts/bisq-pin-args.sh"
bump_check="$repo_root/scripts/bisq-bump-check.sh"

fixture="$(mktemp -d)"
cleanup() { rm -rf "$fixture"; }
trap cleanup EXIT

fail() {
  echo "FAIL: $1" >&2
  exit 1
}

expect_kv() {
  local output="$1" key="$2" want="$3" got
  got="$(printf '%s\n' "$output" | grep -m1 "^${key}=" | cut -d= -f2-)" || true
  [ "$got" = "$want" ] || fail "expected $key=$want, got '$got' in: $output"
}

# --- fixture upstream repo: A <- B, app.txt identical in both ---
git init -q "$fixture/upstream"
git -C "$fixture/upstream" config user.name test
git -C "$fixture/upstream" config user.email test@example.invalid
printf 'line1\n' >"$fixture/upstream/app.txt"
git -C "$fixture/upstream" add app.txt
git -C "$fixture/upstream" commit -qm base
sha_a="$(git -C "$fixture/upstream" rev-parse HEAD)"
git -C "$fixture/upstream" tag v9.9.7
printf 'other\n' >"$fixture/upstream/other.txt"
git -C "$fixture/upstream" add other.txt
git -C "$fixture/upstream" commit -qm second
sha_b="$(git -C "$fixture/upstream" rev-parse HEAD)"
git -C "$fixture/upstream" tag -a v9.9.9 -m "nine nine nine"

# --- fixture patch dirs ---
mkdir -p "$fixture/patches-ok" "$fixture/patches-broken"
{
  echo "--- a/app.txt"
  echo "+++ b/app.txt"
  echo "@@ -1 +1 @@"
  echo "-line1"
  echo "+line1-fixed"
} >"$fixture/patches-ok/fix.patch"
{
  echo "--- a/nope.txt"
  echo "+++ b/nope.txt"
  echo "@@ -1 +1 @@"
  echo "-missing"
  echo "+other"
} >"$fixture/patches-broken/bad.patch"

run_bump() {
  # $1=current $2=current_commit $3=latest $4=patch_dir
  CURRENT_OVERRIDE="$1" CURRENT_COMMIT_OVERRIDE="$2" LATEST_OVERRIDE="$3" \
    PATCH_DIR="$4" UPSTREAM_REPO="$fixture/upstream" \
    bash "$bump_check"
}

echo "== bump path (annotated tag)"
out="$(run_bump 9.9.8 0000000000000000000000000000000000000000 9.9.9 "$fixture/patches-ok")"
expect_kv "$out" bump true
expect_kv "$out" repin false
expect_kv "$out" latest 9.9.9
expect_kv "$out" latest_commit "$sha_b"
expect_kv "$out" patches_ok true
expect_kv "$out" failed_patch none

echo "== up-to-date path"
out="$(run_bump 9.9.9 "$sha_b" 9.9.9 "$fixture/patches-ok")"
expect_kv "$out" bump false
expect_kv "$out" latest_commit none

echo "== re-cut tag re-pin path"
out="$(run_bump 9.9.9 0000000000000000000000000000000000000000 9.9.9 "$fixture/patches-ok")"
expect_kv "$out" bump true
expect_kv "$out" repin true
expect_kv "$out" latest 9.9.9
expect_kv "$out" latest_commit "$sha_b"
expect_kv "$out" patches_ok true

echo "== lightweight-tag fallback path"
out="$(run_bump 9.9.6 0000000000000000000000000000000000000000 9.9.7 "$fixture/patches-ok")"
expect_kv "$out" bump true
expect_kv "$out" latest_commit "$sha_a"
expect_kv "$out" patches_ok true

echo "== incompatible patch path"
out="$(run_bump 9.9.8 0000000000000000000000000000000000000000 9.9.9 "$fixture/patches-broken")"
expect_kv "$out" bump true
expect_kv "$out" patches_ok false
expect_kv "$out" failed_patch bad.patch

echo "== bisq2 flavor: bump without patches pins the tag commit"
out="$(BISQ_FLAVOR=bisq2 run_bump 9.9.8 0000000000000000000000000000000000000000 9.9.9 "")"
expect_kv "$out" bump true
expect_kv "$out" repin false
expect_kv "$out" latest 9.9.9
expect_kv "$out" latest_commit "$sha_b"
expect_kv "$out" patches_ok skipped
expect_kv "$out" failed_patch none

echo "== bisq2 flavor: up-to-date path"
out="$(BISQ_FLAVOR=bisq2 run_bump 9.9.9 "$sha_b" 9.9.9 "")"
expect_kv "$out" bump false

echo "== unknown flavor is rejected"
if BISQ_FLAVOR=nope run_bump 9.9.8 0000000000000000000000000000000000000000 9.9.9 "" >/dev/null 2>&1; then
  fail "unknown BISQ_FLAVOR must fail"
fi

# --- pin-args helper: fixture Dockerfiles ---
mkdir -p "$fixture/docker"
cat >"$fixture/docker/Dockerfile.good" <<'EOF'
ARG BISQ_VERSION=1.2.3
ARG BISQ_COMMIT=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
RUN git init -q /src/foo && \
    git -C /src/foo fetch --depth 1 origin "${BISQ_COMMIT}" && \
    git -C /src/foo checkout -q --detach FETCH_HEAD
EOF
cat >"$fixture/docker/Dockerfile.good2" <<'EOF'
ARG BISQ2_VERSION=4.5.6
ARG BISQ2_COMMIT=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
RUN git init -q /src/bar && \
    git -C /src/bar fetch --depth 1 origin "${BISQ2_COMMIT}" && \
    git -C /src/bar checkout -q --detach FETCH_HEAD
EOF
cat >"$fixture/docker/Dockerfile.floating" <<'EOF'
ARG BISQ_VERSION=1.2.3
ARG BISQ_COMMIT=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
RUN git clone --depth 1 --branch "v${BISQ_VERSION}" https://example.com/foo.git /src/foo
EOF
cat >"$fixture/docker/Dockerfile.nocommit" <<'EOF'
ARG BISQ_VERSION=1.2.3
RUN git init -q /src/foo
EOF

echo "== pin-args accepts pinned fixtures"
out="$(bash "$pin_args" "$fixture/docker/Dockerfile.good" "$fixture/docker/Dockerfile.good2")"
expect_kv "$out" BISQ_VERSION 1.2.3
expect_kv "$out" BISQ_COMMIT aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
expect_kv "$out" BISQ2_VERSION 4.5.6
expect_kv "$out" BISQ2_COMMIT bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb

echo "== pin-args rejects floating-tag clone"
if bash "$pin_args" "$fixture/docker/Dockerfile.floating" "$fixture/docker/Dockerfile.good2" >/dev/null 2>&1; then
  fail "floating-tag Dockerfile was accepted"
fi

echo "== pin-args rejects missing commit"
if bash "$pin_args" "$fixture/docker/Dockerfile.nocommit" "$fixture/docker/Dockerfile.good2" >/dev/null 2>&1; then
  fail "commit-less Dockerfile was accepted"
fi

# --- real Dockerfiles (present only when submodules are checked out) ---
if [ -f "$repo_root/dexUnifiedApi/Dockerfile.bisq" ]; then
  echo "== pin-args accepts real Dockerfiles"
  out="$(bash "$pin_args" \
    "$repo_root/dexUnifiedApi/Dockerfile.bisq" \
    "$repo_root/dexUnifiedApi/Dockerfile.bisq2")"
  [ -n "$(printf '%s\n' "$out" | grep -m1 '^BISQ_COMMIT=')" ] \
    || fail "real Dockerfiles produced no BISQ_COMMIT"
else
  echo "== real Dockerfiles absent (no submodules); skipped"
fi

echo "ALL BISQ PIN TESTS PASSED"