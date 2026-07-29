#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
validator="$repo_root/scripts/validate-env-contract.sh"
deployer="$repo_root/deploy/transactional-deploy.sh"
fixture="$(mktemp -d)"
cleanup() { rm -rf "$fixture"; }
trap cleanup EXIT

fake_repo="$fixture/repo"
fake_bin="$fixture/bin"
mkdir -p "$fake_repo" "$fake_bin"

git -C "$fake_repo" init -q
git -C "$fake_repo" config user.name "deploy-test"
git -C "$fake_repo" config user.email "deploy-test@example.invalid"
mkdir -p "$fake_repo/deploy/nginx"
touch "$fake_repo/docker-compose.yml"
printf '#!/usr/bin/env bash\nprintf "%%s\\n" "$1" >>"${MOCK_CERT_LOG:?}"\n' \
  >"$fake_repo/deploy/nginx/letsencrypt.sh"
chmod +x "$fake_repo/deploy/nginx/letsencrypt.sh"
git -C "$fake_repo" add docker-compose.yml deploy/nginx/letsencrypt.sh
git -C "$fake_repo" commit -qm "previous"
previous_sha="$(git -C "$fake_repo" rev-parse HEAD)"
printf '%s\n' "$previous_sha" >"$fake_repo/.deployed_sha"

printf 'candidate\n' >"$fake_repo/release-marker"
git -C "$fake_repo" add release-marker
git -C "$fake_repo" commit -qm "candidate"
candidate_sha="$(git -C "$fake_repo" rev-parse HEAD)"

contract_keys() {
  local contract="$1"
  sed -n "/^${contract}_REQUIRED=(/,/^)/p; /^${contract}_PRESENT=(/,/^)/p" "$validator" |
    sed -n 's/^[[:space:]]*\([A-Z][A-Z0-9_]*\)[[:space:]]*$/\1/p'
}

write_bundle() {
  local root="$1" marker="$2" contract path key
  while IFS='|' read -r contract path; do
    mkdir -p "$root/$path"
    : >"$root/$path/.env"
    while IFS= read -r key; do
      printf '%s=%s-%s\n' "$key" "$marker" "$key" >>"$root/$path/.env"
    done < <(contract_keys "$contract")
  done <<'EOF'
ROOT|.
UNIFIED_API|dexUnifiedApi
MONITOR|dexMonitor
MM_CONTROLLER|dexMmController
FIAT_CONTROLLER|dexFiatController
EOF
}

write_bundle "$fake_repo" previous

cat >"$fake_bin/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [ "${1:-}" = compose ] && [ "${2:-}" = config ] &&
   [ "${3:-}" = --services ]; then
  printf '%s\n' tor bisq bisq2 unified-api unified-snapshot dexmonitor mm-controller fiat-controller
  exit 0
fi
printf 'code=%s dexmonitor=%s snapshot=%s fail=%s arg1=%s arg2=%s all=%s\n' \
  "$(cat release-marker 2>/dev/null || printf previous)" \
  "${DEXMONITOR_IMAGE_TAG:-none}" \
  "${UNIFIED_SNAPSHOT_IMAGE_TAG:-none}" \
  "${MOCK_FAIL_SHA:-unset}" "${1:-}" "${2:-}" "$*" >>"${MOCK_LOG:?}"
if [ "${1:-}" = compose ] && [ "${2:-}" = up ] &&
   [ "${DEXMONITOR_IMAGE_TAG:-}" = "${MOCK_FAIL_SHA:-never}" ]; then
  exit 1
fi
exit 0
EOF

cat >"$fake_bin/curl" <<'EOF'
#!/usr/bin/env bash
case " $* " in
  *" -w "*) printf '200' ;;
esac
exit 0
EOF
chmod +x "$fake_bin/docker" "$fake_bin/curl"

make_stage() {
  local sha="$1" marker="$2" stage
  stage="$fake_repo/.deploy/staging/$sha"
  mkdir -p "$stage"
  write_bundle "$stage/env" "$marker"
  install -m 700 "$validator" "$stage/validate-env-contract.sh"
  printf '%s\n' "$stage"
}

export PATH="$fake_bin:$PATH"
export MOCK_LOG="$fixture/docker.log"
export MOCK_CERT_LOG="$fixture/certificate.log"

stage="$(make_stage "$candidate_sha" candidate)"
"$deployer" \
  --repo-dir "$fake_repo" \
  --sha "$candidate_sha" \
  --env-bundle "$stage/env"

[[ "$(cat "$fake_repo/.deployed_sha")" == "$candidate_sha" ]]
grep -q '^APP_ENCRYPTION_KEY=candidate-' "$fake_repo/.env"
[[ "$(sed -n '1p' "$MOCK_CERT_LOG")" == verify ]]
[[ "$(sed -n '2p' "$MOCK_CERT_LOG")" == install ]]
grep -q "code=candidate dexmonitor=$candidate_sha snapshot=$candidate_sha .* all=compose up -d --remove-orphans --wait --wait-timeout 1800" \
  "$MOCK_LOG"
grep -q "^DEXMONITOR_IMAGE_TAG=$candidate_sha$" \
  "$fake_repo/.deploy/releases/$candidate_sha/images.env"
grep -q "^UNIFIED_SNAPSHOT_IMAGE_TAG=$candidate_sha$" \
  "$fake_repo/.deploy/releases/$candidate_sha/images.env"
grep -q "^UNIFIED_SNAPSHOT_IMAGE_TAG=$previous_sha$" \
  "$fake_repo/.deploy/releases/$previous_sha/images.env"

printf 'frontend\n' >"$fake_repo/release-marker"
git -C "$fake_repo" add release-marker
git -C "$fake_repo" commit -qm "frontend candidate"
frontend_sha="$(git -C "$fake_repo" rev-parse HEAD)"
stage="$(make_stage "$frontend_sha" frontend)"
"$deployer" \
  --repo-dir "$fake_repo" \
  --sha "$frontend_sha" \
  --env-bundle "$stage/env" \
  --changed-services dexmonitor

[[ "$(cat "$fake_repo/.deployed_sha")" == "$frontend_sha" ]]
grep -q "^DEXMONITOR_IMAGE_TAG=$frontend_sha$" \
  "$fake_repo/.deploy/releases/$frontend_sha/images.env"
grep -q "^UNIFIED_SNAPSHOT_IMAGE_TAG=$candidate_sha$" \
  "$fake_repo/.deploy/releases/$frontend_sha/images.env"
grep -q "code=frontend dexmonitor=$frontend_sha snapshot=$candidate_sha .* all=compose pull dexmonitor" \
  "$MOCK_LOG"
if grep -q "^code=frontend .* all=compose up -d --force-recreate" "$MOCK_LOG"; then
  echo "unchanged config services were force-recreated" >&2
  exit 1
fi

printf 'failing\n' >"$fake_repo/release-marker"
git -C "$fake_repo" add release-marker
git -C "$fake_repo" commit -qm "failing candidate"
failing_sha="$(git -C "$fake_repo" rev-parse HEAD)"
stage="$(make_stage "$failing_sha" failing)"
export MOCK_FAIL_SHA="$failing_sha"

if "$deployer" \
  --repo-dir "$fake_repo" \
  --sha "$failing_sha" \
  --env-bundle "$stage/env" \
  --changed-services dexmonitor; then
  echo "expected candidate deployment to fail" >&2
  tail -20 "$MOCK_LOG" >&2
  exit 1
fi

[[ "$(git -C "$fake_repo" rev-parse HEAD)" == "$frontend_sha" ]]
[[ "$(cat "$fake_repo/.deployed_sha")" == "$frontend_sha" ]]
grep -q '^APP_ENCRYPTION_KEY=frontend-' "$fake_repo/.env"
grep -q "code=frontend dexmonitor=$frontend_sha snapshot=$candidate_sha .* all=compose up -d --remove-orphans --wait --wait-timeout 1800" \
  "$MOCK_LOG"

printf '#!/usr/bin/env bash\nexit 1\n' >"$fake_repo/deploy/nginx/letsencrypt.sh"
git -C "$fake_repo" add deploy/nginx/letsencrypt.sh
git -C "$fake_repo" commit -qm "broken certificate issuance"
certificate_failure_sha="$(git -C "$fake_repo" rev-parse HEAD)"
stage="$(make_stage "$certificate_failure_sha" certificate-failure)"
unset MOCK_FAIL_SHA

if "$deployer" \
  --repo-dir "$fake_repo" \
  --sha "$certificate_failure_sha" \
  --env-bundle "$stage/env" \
  --changed-services ""; then
  echo "expected certificate preflight to fail" >&2
  exit 1
fi

[[ "$(git -C "$fake_repo" rev-parse HEAD)" == "$frontend_sha" ]]
[[ "$(cat "$fake_repo/.deployed_sha")" == "$frontend_sha" ]]
grep -q '^APP_ENCRYPTION_KEY=frontend-' "$fake_repo/.env"
if grep -q "^code=certificate-failure .* arg1=compose arg2=up " "$MOCK_LOG"; then
  echo "compose started before the certificate preflight completed" >&2
  exit 1
fi

stage="$(make_stage "$frontend_sha" lock-test)"
exec 8>"$fake_repo/.deploy/deploy.lock"
flock -n 8
if "$deployer" \
  --repo-dir "$fake_repo" \
  --sha "$frontend_sha" \
  --env-bundle "$stage/env" >/dev/null 2>&1; then
  echo "expected the deployment lock to reject a concurrent rollout" >&2
  exit 1
fi
flock -u 8

stage="$(make_stage "$frontend_sha" invalid-service)"
if "$deployer" \
  --repo-dir "$fake_repo" \
  --sha "$frontend_sha" \
  --env-bundle "$stage/env" \
  --changed-services dexmonitor,not-a-service >/dev/null 2>&1; then
  echo "expected unknown changed service to be rejected" >&2
  exit 1
fi

echo "transactional deployment tests passed"
