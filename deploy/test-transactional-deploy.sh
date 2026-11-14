#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
validator="$repo_root/scripts/validate-env-contract.sh"
deployer="$repo_root/deploy/transactional-deploy.sh"
deploy_options=(--legacy-images)
if [[ "${DEPLOY_TEST_DIGEST:-0}" == 1 ]]; then deploy_options=(); fi
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
mkdir -p "$fake_repo/dexMonitor/internal"
touch "$fake_repo/docker-compose.yml"
printf 'previous\n' >"$fake_repo/dexMonitor/internal/build-input"
printf '#!/usr/bin/env bash\nprintf "%%s\\n" "$1" >>"${MOCK_CERT_LOG:?}"\n' \
  >"$fake_repo/deploy/nginx/letsencrypt.sh"
chmod +x "$fake_repo/deploy/nginx/letsencrypt.sh"
git -C "$fake_repo" add docker-compose.yml deploy/nginx/letsencrypt.sh dexMonitor/internal/build-input
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
MM_CONTROLLER|dexTakerController
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
if [[ "${1:-} ${2:-} ${3:-}" == 'compose config --format' ]]; then
  python3 - <<'PY'
import json, os
services = {}
for service in ['tor', 'bisq', 'bisq2', 'unified-api', 'unified-snapshot', 'dexmonitor', 'mm-controller', 'fiat-controller']:
    variable = service.upper().replace('-', '_') + '_IMAGE_TAG'
    sha = os.environ.get(variable, os.environ.get('IMAGE_TAG', 'a' * 40))
    services[service] = {'image': 'ghcr.io/test/dexarbtrading-' + service + ':' + sha}
print(json.dumps({'services': services}))
PY
  exit 0
fi
if [[ "${1:-} ${2:-} ${3:-}" == 'compose ps -a' && "${4:-}" == -q ]]; then
  exit 0
fi
if [[ "${1:-} ${2:-} ${3:-}" == 'image inspect --format' ]]; then
  # Unified API images declare migrate support; an empty value models an image
  # built from sources that predate the command.
  printf '%s\n' "${MOCK_MIGRATE_LABEL-1}"
  exit 0
fi
if [[ "${1:-} ${2:-}" == 'image inspect' ]]; then
  python3 - "$3" <<'PY'
import json, sys
repo = sys.argv[1].split('@')[0].rsplit(':', 1)[0]
if '@' in sys.argv[1]: repo = sys.argv[1].split('@')[0]
print(json.dumps([{'Id': 'sha256:' + '1' * 64, 'RepoDigests': [repo + '@sha256:' + '1' * 64]}]))
PY
  exit 0
fi
printf 'code=%s dexmonitor=%s snapshot=%s fail=%s arg1=%s arg2=%s all=%s\n' \
  "$(cat release-marker 2>/dev/null || printf previous)" \
  "${DEXMONITOR_IMAGE_TAG:-none}" \
  "${UNIFIED_SNAPSHOT_IMAGE_TAG:-none}" \
  "${MOCK_FAIL_SHA:-unset}" "${1:-}" "${2:-}" "$*" >>"${MOCK_LOG:?}"
if [[ "${1:-} ${2:-}" == 'compose run' ]]; then
  migrate_pattern='^compose run --rm --no-deps --name dexarb-migrate-[0-9a-f]{40} unified-api \./server migrate$'
  [[ "$*" =~ $migrate_pattern ]] || {
    echo "invalid migration invocation: $*" >&2
    exit 2
  }
  marker="$(cat release-marker)"
  if [[ "$marker" == "${MOCK_MIGRATE_HANG_MARKER:-never}" ]]; then
    exec sleep 30
  fi
  if [[ "$marker" == "${MOCK_MIGRATE_FAIL_MARKER:-never}" ]]; then
    if [[ "${MOCK_REQUIRE_ROLE_RESTORE:-false}" == true ]]; then
      : >"$MOCK_LOG.roles-pending"
    fi
    echo 'simulated migration SQL failure' >&2
    exit 1
  fi
fi
if [[ "${1:-} ${2:-}" == 'compose up' ]]; then
  if [[ " $* " == *' --force-recreate '* && "${!#}" == unified-api ]]; then
    rm -f "$MOCK_LOG.roles-pending"
  fi
  if [[ " $* " == *' --remove-orphans '* && -f "$MOCK_LOG.roles-pending" ]]; then
    echo 'stack unhealthy: previous database passwords have not been restored' >&2
    exit 1
  fi
fi
if [[ "${MOCK_DIAGNOSTICS:-false}" == true ]]; then
  if [[ "${1:-} ${2:-} ${3:-} ${4:-}" == 'compose ps -a --format' ]]; then
    printf 'unhealthy-app\trunning\tunhealthy\nstarting-app\trunning\tstarting\nstopped-app\texited\t\nhealthy-app\trunning\thealthy\n'
    exit 0
  fi
  if [[ "${1:-}" == inspect ]]; then
    printf 'state={"OOMKilled":false,"Health":{"Log":[{"ExitCode":1,"Output":"connection refused"}]}} restart_count=2\n'
    exit 0
  fi
fi
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
export REAL_INSTALL="$(command -v install)"
cat >"$fake_bin/install" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ -n "${MOCK_INSTALL_FAIL_PATH:-}" && "${!#}" == "$MOCK_INSTALL_FAIL_PATH" ]]; then
  echo 'simulated release write failure' >&2
  exit 1
fi
exec "$REAL_INSTALL" "$@"
EOF
chmod +x "$fake_bin/install"

make_stage() {
  local sha="$1" marker="$2" stage
  stage="$fake_repo/.deploy/staging/$sha"
  mkdir -p "$stage"
  write_bundle "$stage/env" "$marker"
  install -m 700 "$validator" "$stage/validate-env-contract.sh"
  if [[ "${DEPLOY_TEST_DIGEST:-0}" == 1 ]]; then
    install -m 600 "$repo_root/scripts/release-images.py" "$stage/release-images.py"
    install -m 600 "$repo_root/scripts/image-service-inputs.sh" "$stage/image-service-inputs.sh"
    local changed=''
    case "$marker" in
      candidate) changed="$(bash -c 'source "$1"; image_input_services' bash "$repo_root/scripts/image-service-inputs.sh" | paste -sd, -)" ;;
      frontend|failing|obsolete-drop|manifest-failure) changed=dexmonitor ;;
    esac
    python3 - "$fake_repo" "$stage" "$sha" "$changed" <<'PY'
import importlib.util, os, pathlib, sys
repo, stage, sha, changed = sys.argv[1:]
spec = importlib.util.spec_from_file_location('images', stage + '/release-images.py')
images = importlib.util.module_from_spec(spec)
spec.loader.exec_module(images)
os.chdir(repo)
images.write(stage + '/baseline.json', images.snapshot(repo))
pathlib.Path(stage + '/receipts').mkdir(exist_ok=True)
for service in filter(None, changed.split(',')):
    images.write(stage + '/receipts/' + service + '.json', {
        'service': service, 'source': sha,
        'image': 'ghcr.io/test/dexarbtrading-' + service + '@sha256:' + '2' * 64})
PY
  fi
  printf '%s\n' "$stage"
}

export PATH="$fake_bin:$PATH"
export MOCK_LOG="$fixture/docker.log"
export MOCK_CERT_LOG="$fixture/certificate.log"

stage="$(make_stage "$candidate_sha" candidate)"
# An incomplete active environment must stop before container activation.
mv "$fake_repo/.env" "$fixture/active-env.saved"
if "$deployer" "${deploy_options[@]}" \
  --repo-dir "$fake_repo" --sha "$candidate_sha" --env-bundle "$stage/env"; then
  echo 'expected missing rollback environment to reject activation' >&2
  exit 1
fi
[[ "$(cat "$fake_repo/.deployed_sha")" == "$previous_sha" ]]
if [[ -f "$MOCK_LOG" ]] && grep -q 'arg2=up ' "$MOCK_LOG"; then
  echo 'containers started without an environment rollback snapshot' >&2
  exit 1
fi
mv "$fixture/active-env.saved" "$fake_repo/.env"

# The very first digest migration must be able to roll back the legacy release.
stage="$(make_stage "$candidate_sha" candidate)"
export MOCK_FAIL_SHA="$candidate_sha"
if "$deployer" "${deploy_options[@]}" \
  --repo-dir "$fake_repo" --sha "$candidate_sha" --env-bundle "$stage/env"; then
  echo 'expected failed initial migration' >&2
  exit 1
fi
unset MOCK_FAIL_SHA
[[ "$(git -C "$fake_repo" rev-parse HEAD)" == "$previous_sha" ]]
[[ "$(cat "$fake_repo/.deployed_sha")" == "$previous_sha" ]]
grep -q '^APP_ENCRYPTION_KEY=previous-' "$fake_repo/.env"
[[ -f "$fake_repo/.deploy/releases/$previous_sha/env/.env" ]]
git -C "$fake_repo" checkout -q --detach "$candidate_sha"
: >"$MOCK_CERT_LOG"

stage="$(make_stage "$candidate_sha" candidate)"
"$deployer" "${deploy_options[@]}" \
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

# Source detection belongs to CI. The VPS trusts its changed list and receipts
# without requiring submodule objects to repeat the diff.
printf 'omitted-monitor\n' >"$fake_repo/release-marker"
printf 'stale monitor image\n' >"$fake_repo/dexMonitor/internal/build-input"
git -C "$fake_repo" add release-marker dexMonitor/internal/build-input
git -C "$fake_repo" commit -qm "omit changed monitor"
omitted_monitor_sha="$(git -C "$fake_repo" rev-parse HEAD)"
stage="$(make_stage "$omitted_monitor_sha" omitted-monitor)"
"$deployer" "${deploy_options[@]}" \
  --repo-dir "$fake_repo" \
  --sha "$omitted_monitor_sha" \
  --env-bundle "$stage/env" \
  --changed-services ""
[[ "$(cat "$fake_repo/.deployed_sha")" == "$omitted_monitor_sha" ]]

# The privileged migration step must run before app containers start.
migrate_line="$(grep -n "code=omitted-monitor .* all=compose run --rm --no-deps --name dexarb-migrate-$omitted_monitor_sha unified-api ./server migrate" "$MOCK_LOG" | head -n1 | cut -d: -f1 || true)"
up_line="$(grep -n "code=omitted-monitor .* all=compose up -d --remove-orphans" "$MOCK_LOG" | head -n1 | cut -d: -f1 || true)"
[[ -n "$migrate_line" ]] || { echo "migrate step did not run" >&2; exit 1; }
[[ -n "$up_line" ]] || { echo "app startup did not run" >&2; exit 1; }
[[ "$migrate_line" -lt "$up_line" ]] || { echo "migrate step did not run before app startup" >&2; exit 1; }

# Restore the chain the remaining cases assume (candidate as previous).
git -C "$fake_repo" checkout -q --detach "$candidate_sha"
printf '%s\n' "$candidate_sha" >"$fake_repo/.deployed_sha"

printf 'frontend\n' >"$fake_repo/release-marker"
git -C "$fake_repo" add release-marker
git -C "$fake_repo" commit -qm "frontend candidate"
frontend_sha="$(git -C "$fake_repo" rev-parse HEAD)"
stage="$(make_stage "$frontend_sha" frontend)"
"$deployer" "${deploy_options[@]}" \
  --repo-dir "$fake_repo" \
  --sha "$frontend_sha" \
  --env-bundle "$stage/env" \
  --changed-services dexmonitor

[[ "$(cat "$fake_repo/.deployed_sha")" == "$frontend_sha" ]]
if [[ "${DEPLOY_TEST_DIGEST:-0}" == 1 ]]; then
  python3 - "$fake_repo" "$frontend_sha" "$candidate_sha" <<'PY'
import json, pathlib, sys
root, current, previous = sys.argv[1:]
manifest = json.loads((pathlib.Path(root) / '.deploy/releases' / current / 'images.json').read_text())
assert manifest['services']['dexmonitor']['source'] == current
assert manifest['services']['bisq2']['source'] == previous
assert manifest['services']['bisq2']['image'].endswith('@sha256:' + '2' * 64)
assert all('@sha256:' in row['image'] for row in manifest['services'].values())
PY
fi
grep -q "^DEXMONITOR_IMAGE_TAG=$frontend_sha$" \
  "$fake_repo/.deploy/releases/$frontend_sha/images.env"
grep -q "^UNIFIED_SNAPSHOT_IMAGE_TAG=$candidate_sha$" \
  "$fake_repo/.deploy/releases/$frontend_sha/images.env"
grep -q "code=frontend dexmonitor=$frontend_sha snapshot=$candidate_sha .* all=compose pull" \
  "$MOCK_LOG"
if grep -q "^code=frontend .* all=compose up -d --force-recreate" "$MOCK_LOG"; then
  echo "unchanged config services were force-recreated" >&2
  exit 1
fi

# Failed migrations abort before candidate app startup and preserve the active
# release. Rollback must not run schema migrations. Diagnostics include probes
# for unhealthy/starting containers and process state for stopped containers.
printf 'migration-failure\n' >"$fake_repo/release-marker"
git -C "$fake_repo" add release-marker
git -C "$fake_repo" commit -qm 'migration failure'
migration_failure_sha="$(git -C "$fake_repo" rev-parse HEAD)"
stage="$(make_stage "$migration_failure_sha" migration-failure)"
export MOCK_MIGRATE_FAIL_MARKER=migration-failure
export MOCK_DIAGNOSTICS=true
export MOCK_REQUIRE_ROLE_RESTORE=true
prior_migrations="$(grep -c '^code=frontend .* all=compose run ' "$MOCK_LOG")"
if "$deployer" "${deploy_options[@]}" \
  --repo-dir "$fake_repo" --sha "$migration_failure_sha" \
  --env-bundle "$stage/env" --changed-services "" >"$fixture/migration-failure.log" 2>&1; then
  echo 'expected migration failure to abort deployment' >&2
  exit 1
fi
unset MOCK_MIGRATE_FAIL_MARKER MOCK_DIAGNOSTICS MOCK_REQUIRE_ROLE_RESTORE
[[ "$(cat "$fake_repo/.deployed_sha")" == "$frontend_sha" ]]
[[ "$(git -C "$fake_repo" rev-parse HEAD)" == "$frontend_sha" ]]
grep -q '^APP_ENCRYPTION_KEY=frontend-' "$fake_repo/.env"
grep -q 'simulated migration SQL failure' "$fixture/migration-failure.log"
grep -q 'connection refused' "$fixture/migration-failure.log"
grep -q "rolled back code, environment, and images to $frontend_sha" "$fixture/migration-failure.log" || {
  cat "$fixture/migration-failure.log" >&2
  exit 1
}
[[ ! -f "$MOCK_LOG.roles-pending" ]]
[[ "$(grep -c '^code=frontend .* all=compose run ' "$MOCK_LOG")" == "$prior_migrations" ]]
if grep -q '^code=migration-failure .* all=compose up -d --remove-orphans' "$MOCK_LOG"; then
  echo 'candidate apps started after migration failure' >&2
  exit 1
fi
for container in unhealthy-app starting-app stopped-app; do
  grep -q "arg1=inspect .* $container$" "$MOCK_LOG"
done
if grep -q 'arg1=inspect .* healthy-app$' "$MOCK_LOG"; then
  echo 'healthy container incorrectly selected for failure diagnostics' >&2
  exit 1
fi
[[ "$(grep -c "all=rm -f dexarb-migrate-$migration_failure_sha$" "$MOCK_LOG")" == 2 ]]
# The failed migration already applied the candidate's role passwords, which
# differ in this fixture. Compose kept the previous unified-api container, so
# rollback must restart it to reapply the previous passwords.
recreate_api='all=compose up -d --force-recreate --no-deps --wait --wait-timeout 600 --no-build unified-api$'
grep -q "^code=frontend .* $recreate_api" "$MOCK_LOG"

# Unchanged role passwords leave nothing to restore after a failed migration.
printf 'migration-failure-same-roles\n' >"$fake_repo/release-marker"
git -C "$fake_repo" add release-marker
git -C "$fake_repo" commit -qm 'migration failure with unchanged roles'
same_roles_sha="$(git -C "$fake_repo" rev-parse HEAD)"
stage="$(make_stage "$same_roles_sha" migration-failure-same-roles)"
roles_env="$stage/env/dexUnifiedApi/.env"
{
  grep -Ev '^POSTGRES_[A-Z0-9_]*PASSWORD=' "$roles_env"
  grep -E '^POSTGRES_[A-Z0-9_]*PASSWORD=' \
    "$fake_repo/.deploy/releases/$frontend_sha/env/dexUnifiedApi/.env"
} >"$roles_env.tmp"
mv "$roles_env.tmp" "$roles_env"
prior_recreates="$(grep -c "$recreate_api" "$MOCK_LOG" || true)"
export MOCK_MIGRATE_FAIL_MARKER=migration-failure-same-roles
if "$deployer" "${deploy_options[@]}" \
  --repo-dir "$fake_repo" --sha "$same_roles_sha" \
  --env-bundle "$stage/env" --changed-services "" >/dev/null 2>&1; then
  echo 'expected migration failure to abort deployment' >&2
  exit 1
fi
unset MOCK_MIGRATE_FAIL_MARKER
[[ "$(cat "$fake_repo/.deployed_sha")" == "$frontend_sha" ]]
[[ "$(grep -c "$recreate_api" "$MOCK_LOG" || true)" == "$prior_recreates" ]]

# A migration that outlives its limit aborts activation and removes its
# one-off container, which the stopped Compose client may leave running.
printf 'migration-timeout\n' >"$fake_repo/release-marker"
git -C "$fake_repo" add release-marker
git -C "$fake_repo" commit -qm 'migration timeout'
migration_timeout_sha="$(git -C "$fake_repo" rev-parse HEAD)"
stage="$(make_stage "$migration_timeout_sha" migration-timeout)"
export MOCK_MIGRATE_HANG_MARKER=migration-timeout DEPLOY_MIGRATION_TIMEOUT_SEC=1
if "$deployer" "${deploy_options[@]}" \
  --repo-dir "$fake_repo" --sha "$migration_timeout_sha" \
  --env-bundle "$stage/env" --changed-services "" >"$fixture/migration-timeout.log" 2>&1; then
  echo 'expected migration timeout to abort deployment' >&2
  exit 1
fi
unset MOCK_MIGRATE_HANG_MARKER DEPLOY_MIGRATION_TIMEOUT_SEC
[[ "$(cat "$fake_repo/.deployed_sha")" == "$frontend_sha" ]]
grep -q 'database migration exceeded 1s' "$fixture/migration-timeout.log"
[[ "$(grep -c "all=rm -f dexarb-migrate-$migration_timeout_sha$" "$MOCK_LOG")" == 2 ]]
if grep -q '^code=migration-timeout .* all=compose up -d --remove-orphans' "$MOCK_LOG"; then
  echo 'candidate apps started after migration timeout' >&2
  exit 1
fi

# An image without the migrate command is refused before it can start a second
# API server. Nothing touched the roles, so rollback leaves unified-api alone.
printf 'migration-unsupported\n' >"$fake_repo/release-marker"
git -C "$fake_repo" add release-marker
git -C "$fake_repo" commit -qm 'unified-api image without migrate'
unsupported_sha="$(git -C "$fake_repo" rev-parse HEAD)"
stage="$(make_stage "$unsupported_sha" migration-unsupported)"
prior_recreates="$(grep -c "$recreate_api" "$MOCK_LOG" || true)"
export MOCK_MIGRATE_LABEL=''
if "$deployer" "${deploy_options[@]}" \
  --repo-dir "$fake_repo" --sha "$unsupported_sha" \
  --env-bundle "$stage/env" --changed-services "" >"$fixture/migration-unsupported.log" 2>&1; then
  echo 'expected an image without the migrate command to be refused' >&2
  exit 1
fi
unset MOCK_MIGRATE_LABEL
[[ "$(cat "$fake_repo/.deployed_sha")" == "$frontend_sha" ]]
grep -q 'does not provide the migrate command' "$fixture/migration-unsupported.log"
if grep -q '^code=migration-unsupported .* all=compose run ' "$MOCK_LOG"; then
  echo 'migrate ran on an image without the command' >&2
  exit 1
fi
[[ "$(grep -c "$recreate_api" "$MOCK_LOG" || true)" == "$prior_recreates" ]]

# Healthy containers must still roll back if the release manifest cannot commit.
printf 'manifest-failure\n' >"$fake_repo/release-marker"
git -C "$fake_repo" add release-marker
git -C "$fake_repo" commit -qm 'manifest write failure'
manifest_failure_sha="$(git -C "$fake_repo" rev-parse HEAD)"
stage="$(make_stage "$manifest_failure_sha" manifest-failure)"
export MOCK_INSTALL_FAIL_PATH="$fake_repo/.deploy/releases/$manifest_failure_sha/images.env.tmp"
if "$deployer" "${deploy_options[@]}" \
  --repo-dir "$fake_repo" --sha "$manifest_failure_sha" \
  --env-bundle "$stage/env" --changed-services dexmonitor; then
  echo 'expected release persistence failure to roll back' >&2
  exit 1
fi
unset MOCK_INSTALL_FAIL_PATH
[[ "$(git -C "$fake_repo" rev-parse HEAD)" == "$frontend_sha" ]]
[[ "$(cat "$fake_repo/.deployed_sha")" == "$frontend_sha" ]]
grep -q '^APP_ENCRYPTION_KEY=frontend-' "$fake_repo/.env"

printf 'failing\n' >"$fake_repo/release-marker"
git -C "$fake_repo" add release-marker
git -C "$fake_repo" commit -qm "failing candidate"
failing_sha="$(git -C "$fake_repo" rev-parse HEAD)"
stage="$(make_stage "$failing_sha" failing)"
export MOCK_FAIL_SHA="$failing_sha"

if "$deployer" "${deploy_options[@]}" \
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

if "$deployer" "${deploy_options[@]}" \
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
if "$deployer" "${deploy_options[@]}" \
  --repo-dir "$fake_repo" \
  --sha "$frontend_sha" \
  --env-bundle "$stage/env" >/dev/null 2>&1; then
  echo "expected the deployment lock to reject a concurrent rollout" >&2
  exit 1
fi
flock -u 8

stage="$(make_stage "$frontend_sha" invalid-service)"
if "$deployer" "${deploy_options[@]}" \
  --repo-dir "$fake_repo" \
  --sha "$frontend_sha" \
  --env-bundle "$stage/env" \
  --changed-services dexmonitor,not-a-service >/dev/null 2>&1; then
  echo "expected unknown changed service to be rejected" >&2
  exit 1
fi

# Prior releases may still list image tags for services that were later
# removed from image_vars (e.g. TOTP_API_IMAGE_TAG). Deploy must strip those
# and continue inheriting the remaining per-service tags.
printf 'TOTP_API_IMAGE_TAG=%s\n' "$frontend_sha" \
  >>"$fake_repo/.deploy/releases/$frontend_sha/images.env"
printf 'obsolete-drop\n' >"$fake_repo/release-marker"
git -C "$fake_repo" add release-marker
git -C "$fake_repo" commit -qm "drop obsolete image tag"
obsolete_sha="$(git -C "$fake_repo" rev-parse HEAD)"
stage="$(make_stage "$obsolete_sha" obsolete-drop)"
"$deployer" "${deploy_options[@]}" \
  --repo-dir "$fake_repo" \
  --sha "$obsolete_sha" \
  --env-bundle "$stage/env" \
  --changed-services dexmonitor

[[ "$(cat "$fake_repo/.deployed_sha")" == "$obsolete_sha" ]]
if grep -q '^TOTP_API_IMAGE_TAG=' \
  "$fake_repo/.deploy/releases/$frontend_sha/images.env"; then
  echo "previous image manifest still contains obsolete TOTP_API_IMAGE_TAG" >&2
  exit 1
fi
if grep -q '^TOTP_API_IMAGE_TAG=' \
  "$fake_repo/.deploy/releases/$obsolete_sha/images.env"; then
  echo "candidate image manifest still contains obsolete TOTP_API_IMAGE_TAG" >&2
  exit 1
fi
grep -q "^DEXMONITOR_IMAGE_TAG=$obsolete_sha$" \
  "$fake_repo/.deploy/releases/$obsolete_sha/images.env"
grep -q "^UNIFIED_SNAPSHOT_IMAGE_TAG=$candidate_sha$" \
  "$fake_repo/.deploy/releases/$obsolete_sha/images.env"

echo "transactional deployment tests passed"
