#!/usr/bin/env bash
# Print the pinned Bisq source coordinates from the Dockerfiles (key=value):
#   BISQ_VERSION, BISQ_COMMIT, BISQ2_VERSION, BISQ2_COMMIT
#
# Exits 1 unless every ARG is present and well-formed AND the clone step
# consumes the commit instead of a floating tag: cloning `--branch v${...}`
# reuses a stale cached layer while upstream moves on (Gitea run #348), so a
# floating clone must fail here rather than deep inside the image build.
#
# Used by the bisq-patches-apply CI jobs and scripts/test-bisq-pins.sh.
set -Eeuo pipefail

bisq_dockerfile="${1:?usage: bisq-pin-args.sh <Dockerfile.bisq> [Dockerfile.bisq2]}"
bisq2_dockerfile="${2:-$(dirname "$bisq_dockerfile")/Dockerfile.bisq2}"

arg() {
  grep -m1 -oP "^ARG ${2}=\K${3}" "$1" || true
}

fail() {
  echo "ERROR: $1" >&2
  exit 1
}

version="$(arg "$bisq_dockerfile" BISQ_VERSION '[0-9]+\.[0-9]+\.[0-9]+')"
[ -n "$version" ] || fail "no well-formed ARG BISQ_VERSION in $bisq_dockerfile"
commit="$(arg "$bisq_dockerfile" BISQ_COMMIT '[0-9a-f]{40}')"
[ -n "$commit" ] || fail "no well-formed ARG BISQ_COMMIT in $bisq_dockerfile"
grep -qF 'fetch --depth 1 origin "${BISQ_COMMIT}"' "$bisq_dockerfile" \
  || fail "$bisq_dockerfile does not fetch \${BISQ_COMMIT}"
grep -q 'git clone.*--branch "v${BISQ_VERSION}"' "$bisq_dockerfile" \
  && fail "$bisq_dockerfile still clones the floating tag" || true

version2="$(arg "$bisq2_dockerfile" BISQ2_VERSION '[0-9]+\.[0-9]+\.[0-9]+')"
[ -n "$version2" ] || fail "no well-formed ARG BISQ2_VERSION in $bisq2_dockerfile"
commit2="$(arg "$bisq2_dockerfile" BISQ2_COMMIT '[0-9a-f]{40}')"
[ -n "$commit2" ] || fail "no well-formed ARG BISQ2_COMMIT in $bisq2_dockerfile"
grep -qF 'fetch --depth 1 origin "${BISQ2_COMMIT}"' "$bisq2_dockerfile" \
  || fail "$bisq2_dockerfile does not fetch \${BISQ2_COMMIT}"
grep -q 'git clone.*--branch "v${BISQ2_VERSION}"' "$bisq2_dockerfile" \
  && fail "$bisq2_dockerfile still clones the floating tag" || true

out="BISQ_VERSION=${version}
BISQ_COMMIT=${commit}
BISQ2_VERSION=${version2}
BISQ2_COMMIT=${commit2}"
printf '%s\n' "$out"
if [ -n "${GITHUB_OUTPUT:-}" ]; then
  printf '%s\n' "$out" >>"$GITHUB_OUTPUT"
fi
