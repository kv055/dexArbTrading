#!/usr/bin/env bash
# npm-peer-sync.sh — Renovate post-upgrade task for npm (scripts/renovate-run.sh
# copies it into the Renovate container; renovate/shared.json runs it).
#
# Renovate picks the newest version of every npm package independently and
# never checks peer ranges, so a single all-dependencies PR can contain an
# uninstallable set (e.g. typescript 7 while typescript-eslint only accepts
# typescript <6.1, which made npm fail with ERESOLVE). This task replaces
# Renovate's npm edits with the newest PEER-COMPATIBLE set:
#
#   for every package-lock.json in the repo:
#     1. restore package.json + lock from the base branch (Renovate's edits
#        may already be inconsistent, and ncu never downgrades),
#     2. npm ci --ignore-scripts      (ncu --peer reads installed packages),
#     3. npm-check-updates --peer -u  (newest versions whose peer ranges fit,
#                                     honouring the same 3-day release age),
#     4. npm install --package-lock-only (regenerate the lock file).
#
# It runs once per Renovate branch, only when that branch carries an npm
# update, before Renovate commits, so the PR arrives already consistent.
#
# Usage: npm-peer-sync.sh <base-branch>     (run from the repository root)
set -Eeuo pipefail

base="${1:?usage: npm-peer-sync.sh <base-branch>}"
NCU="npm-check-updates@23.1.1"
COOLDOWN_DAYS="${NPM_PEER_SYNC_COOLDOWN_DAYS:-3}"   # keep in step with minimumReleaseAge
# The same age limit for what the lock file resolves inside the new ranges
# (ncu's --cooldown only governs the versions it writes to package.json).
BEFORE="$(date -u -d "-${COOLDOWN_DAYS} days" +%Y-%m-%dT%H:%M:%SZ)"
ref="refs/remotes/origin/$base"

if ! git rev-parse --verify -q "$ref" >/dev/null; then
  git fetch -q --depth=1 origin "+refs/heads/$base:$ref"
fi

# Strict peer resolution even though Renovate's own (superseded) npm step runs
# with legacy-peer-deps, and allow tarball-URL deps (e.g. Tailwind's wasm
# package), which the npm config inside the Renovate image refuses.
npm_flags=(--ignore-scripts --no-audit --no-fund --loglevel=error
           --legacy-peer-deps=false --strict-peer-deps=false --allow-remote=all)

found=0
while IFS= read -r lock; do
  found=1
  dir=$(dirname "$lock")
  echo "npm-peer-sync: $dir (base $base)"
  git show "$ref:$dir/package.json" > "$dir/package.json"
  git show "$ref:$lock" > "$lock"
  (
    cd "$dir"
    npm ci "${npm_flags[@]}"
    npx --yes "$NCU" --peer --upgrade --target latest --cooldown "${COOLDOWN_DAYS}d"
    npm install --package-lock-only --before="$BEFORE" "${npm_flags[@]}"
    rm -rf node_modules
  )
done < <(git ls-files -- '*package-lock.json' | grep -v '/node_modules/' || true)

if [ "$found" = 0 ]; then echo "npm-peer-sync: no package-lock.json found"; fi
