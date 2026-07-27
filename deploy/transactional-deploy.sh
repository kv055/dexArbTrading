#!/usr/bin/env bash
# Activate one staged release on the VPS. Code, image tag, and all plaintext
# environment files move together; a failed rollout restores the previous
# environment snapshot before restarting the previous SHA.
set -Eeuo pipefail

usage() {
  echo "usage: $0 --repo-dir <path> --sha <commit> --env-bundle <path> [--fetch-origin]" >&2
  exit 2
}

repo_dir=""
new_sha=""
env_bundle=""
fetch_origin=false
while (($#)); do
  case "$1" in
    --repo-dir) repo_dir="${2:-}"; shift 2 ;;
    --sha) new_sha="${2:-}"; shift 2 ;;
    --env-bundle) env_bundle="${2:-}"; shift 2 ;;
    --fetch-origin) fetch_origin=true; shift ;;
    *) usage ;;
  esac
done

[[ -n "$repo_dir" && "$repo_dir" == /* && "$repo_dir" != "/" &&
   -d "$repo_dir/.git" ]] || usage
[[ "$new_sha" =~ ^[0-9a-f]{40}$ ]] || usage
[[ -d "$env_bundle" ]] || usage

repo_dir="$(cd "$repo_dir" && pwd)"
stage_dir="$(cd "$(dirname "$env_bundle")" && pwd)"
expected_stage="$repo_dir/.deploy/staging/$new_sha"
[[ "$stage_dir" == "$expected_stage" ]] || {
  echo "environment bundle must be staged under $expected_stage" >&2
  exit 2
}
env_bundle="$stage_dir/env"
validator="$stage_dir/validate-env-contract.sh"
[[ -x "$validator" ]] || {
  echo "staged environment validator is missing" >&2
  exit 1
}
"$validator" values "$env_bundle"

state_root="$repo_dir/.deploy"
release_root="$state_root/releases"
mkdir -p "$release_root"
chmod 700 "$state_root" "$release_root"

exec 9>"$state_root/deploy.lock"
if ! flock -n 9; then
  echo "another deployment is already active" >&2
  exit 1
fi

cleanup() {
  rm -rf "$stage_dir"
}
trap cleanup EXIT

cd "$repo_dir"
prev_sha="$(cat .deployed_sha 2>/dev/null || true)"
env_paths=(
  ".env"
  "dexUnifiedApi/.env"
  "dexMonitor/.env"
  "dexMmController/.env"
  "dexFiatController/.env"
)
recreate_services=(grafana prometheus loki tempo alloy nginx)

copy_bundle() {
  local source_root="$1" destination_root="$2" relative
  mkdir -p "$destination_root"
  chmod 700 "$destination_root"
  for relative in "${env_paths[@]}"; do
    [[ -f "$source_root/$relative" ]] || {
      echo "environment bundle is missing $relative" >&2
      return 1
    }
    mkdir -p "$destination_root/$(dirname "$relative")"
    install -m 600 "$source_root/$relative" \
      "$destination_root/$relative.tmp"
    mv -f "$destination_root/$relative.tmp" "$destination_root/$relative"
  done
}

activate_bundle() {
  local source_root="$1" relative
  for relative in "${env_paths[@]}"; do
    [[ -f "$source_root/$relative" ]] || return 1
    mkdir -p "$(dirname "$relative")"
    install -m 600 "$source_root/$relative" "$relative.tmp"
    mv -f "$relative.tmp" "$relative"
  done
}

snapshot_active_bundle() {
  local sha="$1" target temporary relative
  target="$release_root/$sha/env"
  temporary="$release_root/.${sha}.tmp.$$"
  [[ "$sha" =~ ^[0-9a-f]{40}$ ]] || return 0
  [[ -d "$target" ]] && return 0
  for relative in "${env_paths[@]}"; do
    [[ -f "$relative" ]] || {
      echo "warning: cannot snapshot missing active $relative" >&2
      return 1
    }
  done
  rm -rf "$temporary"
  copy_bundle "$repo_dir" "$temporary/env"
  mv "$temporary" "$release_root/$sha"
}

sync_tree() {
  local sha="$1"
  if ! git cat-file -e "$sha^{commit}" 2>/dev/null; then
    if "$fetch_origin"; then
      git fetch --no-tags origin "$sha" || git fetch --no-tags origin main
    else
      echo "commit $sha is not present in the VPS repository" >&2
      return 1
    fi
  fi
  git checkout --force --detach "$sha"
}

compose_preflight() {
  local output
  "$validator" values "$repo_dir"
  if ! output="$(IMAGE_TAG="$1" docker compose config --quiet 2>&1)"; then
    echo "docker compose configuration validation failed" >&2
    return 1
  fi
  if [[ -n "$output" ]]; then
    echo "docker compose configuration produced warnings; refusing deployment" >&2
    return 1
  fi
}

ensure_trusted_certificate() {
  [[ -x deploy/nginx/letsencrypt.sh ]] || {
    echo "trusted-certificate helper is missing" >&2
    return 1
  }
  if [[ ! -r /etc/dexarb/edge.env ]] &&
     ! grep -q '^LETSENCRYPT_EMAIL=.' .env; then
    echo "no provisioned edge configuration or dotenv certificate contact is available" >&2
    return 1
  fi
  if bash deploy/nginx/letsencrypt.sh verify; then
    # A valid Certbot lineage is not necessarily the certificate currently
    # mounted by nginx. Always synchronize it into certs/server.{crt,key}.
    bash deploy/nginx/letsencrypt.sh install
  else
    bash deploy/nginx/letsencrypt.sh issue
  fi
}

edge_up() {
  local code identifier=""
  if [[ -r /etc/dexarb/edge.env ]]; then
    local EDGE_CERT_IDENTIFIER=""
    # Root-owned Ansible output containing only quoted scalar values.
    # shellcheck disable=SC1091
    source /etc/dexarb/edge.env
    identifier="$EDGE_CERT_IDENTIFIER"
  fi
  if [[ -z "$identifier" ]]; then
    identifier="$(sed -n 's/^PUBLIC_HOSTNAME=//p' .env | tail -n 1 | tr -d '\r')"
    identifier="${identifier#\"}"
    identifier="${identifier%\"}"
    identifier="${identifier#\'}"
    identifier="${identifier%\'}"
  fi
  [[ -n "$identifier" ]] || return 1

  # Route the configured public identity to local nginx while retaining normal
  # hostname/SAN and CA verification. This rejects a stale self-signed cert.
  code="$(curl --resolve "$identifier:443:127.0.0.1" -so /dev/null \
    -w '%{http_code}' --max-time 10 "https://$identifier/" 2>/dev/null)" ||
    code=000
  [[ "$code" == 200 || "$code" == 401 ]]
}

app_get() {
  local path="$1" code
  code="$(docker compose exec -T dexmonitor curl -so /dev/null -w '%{http_code}' \
    --max-time 30 "http://localhost:5000$path" 2>/dev/null)" || code=000
  [[ "$code" == 200 || "$code" == 401 ]] && return 0
  code="$(curl -sko /dev/null -w '%{http_code}' --max-time 30 \
    "https://127.0.0.1$path" 2>/dev/null)" || code=000
  [[ "$code" == 200 || "$code" == 401 ]] && return 0
  code="$(curl -so /dev/null -w '%{http_code}' --max-time 30 \
    "http://127.0.0.1$path" 2>/dev/null)" || code=000
  [[ "$code" == 200 || "$code" == 401 ]]
}

smoke() {
  local name probe attempt ok
  for entry in \
    "unified-api /health::curl -fsSL --max-time 10 http://127.0.0.1:5010/health" \
    "web edge::edge_up" \
    "general_stats overview::app_get '/api/general_stats/overview?hours=24&cex=kraken'" \
    "ohlc::app_get '/api/ohlc?hours=24&cex=kraken'"; do
    name="${entry%%::*}"
    probe="${entry#*::}"
    ok=""
    for attempt in 1 2 3; do
      if eval "$probe" >/dev/null 2>&1; then ok=1; break; fi
      echo "smoke attempt $attempt failed for $name"
      [[ "$attempt" -lt 3 ]] && sleep 5 || true
    done
    [[ -n "$ok" ]] || {
      echo "smoke test failed: $name" >&2
      return 1
    }
    echo "ok $name"
  done
}

start_release() {
  local sha="$1" bundle="$2" issue_certificate="$3"
  sync_tree "$sha" || return 1
  activate_bundle "$bundle" || return 1
  compose_preflight "$sha" || return 1
  if [[ "$issue_certificate" == true ]]; then
    ensure_trusted_certificate || return 1
  fi
  IMAGE_TAG="$sha" docker compose pull || return 1
  IMAGE_TAG="$sha" docker compose up -d --remove-orphans \
    --wait --wait-timeout 300 || return 1
  IMAGE_TAG="$sha" docker compose up -d --force-recreate --no-deps \
    --wait --wait-timeout 120 "${recreate_services[@]}" || return 1
  smoke || return 1
}

dump_failing_logs() {
  local sha="$1" service
  echo "--- container logs for failing services ---"
  while IFS= read -r service; do
    [[ -n "$service" ]] || continue
    echo "=== $service ==="
    docker logs --tail 80 "$service" 2>&1 || true
  done < <(
    IMAGE_TAG="$sha" docker compose ps --format '{{.Name}} {{.State}}' 2>/dev/null |
      awk '$2 != "running" {print $1}'
  )
  echo "--- all container states ---"
  IMAGE_TAG="$sha" docker compose ps 2>/dev/null || true
}

snapshot_active_bundle "$prev_sha" || true

if start_release "$new_sha" "$env_bundle" true; then
  copy_bundle "$env_bundle" "$release_root/$new_sha/env"
  printf '%s\n' "$new_sha" >.deployed_sha.tmp
  mv -f .deployed_sha.tmp .deployed_sha
  find "$release_root" -mindepth 1 -maxdepth 1 -type d -printf '%T@ %p\n' |
    sort -nr | tail -n +3 | cut -d' ' -f2- |
    while IFS= read -r old_release; do
      [[ "$old_release" == "$release_root/"* ]] && rm -rf "$old_release"
    done
  echo "deploy of $new_sha healthy"
  exit 0
fi

dump_failing_logs "$new_sha"
echo "deploy of $new_sha failed; rolling back to ${prev_sha:-<none>}" >&2
if [[ "$prev_sha" =~ ^[0-9a-f]{40}$ &&
      -d "$release_root/$prev_sha/env" ]]; then
  if start_release "$prev_sha" "$release_root/$prev_sha/env" false; then
    echo "rolled back code and environment to $prev_sha" >&2
  else
    echo "ROLLBACK to $prev_sha FAILED; manual intervention required" >&2
  fi
else
  echo "no complete previous release snapshot is available" >&2
fi
exit 1
