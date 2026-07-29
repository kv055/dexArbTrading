#!/usr/bin/env bash
# Activate one staged release on the VPS. Code, per-service image tags, and all
# plaintext environment files move together; a failed rollout restores the
# previous environment and image manifests before restarting the previous SHA.
set -Eeuo pipefail

usage() {
  echo "usage: $0 --repo-dir <path> --sha <commit> --env-bundle <path> [--changed-services <csv>] [--fetch-origin]" >&2
  exit 2
}

repo_dir=""
new_sha=""
env_bundle=""
changed_services_csv=""
changed_services_supplied=false
fetch_origin=false
while (($#)); do
  case "$1" in
    --repo-dir) repo_dir="${2:-}"; shift 2 ;;
    --sha) new_sha="${2:-}"; shift 2 ;;
    --env-bundle) env_bundle="${2:-}"; shift 2 ;;
    --changed-services)
      changed_services_csv="${2:-}"
      changed_services_supplied=true
      shift 2
      ;;
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
candidate_manifest="$state_root/.images.${new_sha}.$$"

exec 9>"$state_root/deploy.lock"
if ! flock -n 9; then
  echo "another deployment is already active" >&2
  exit 1
fi

cleanup() {
  rm -f "$candidate_manifest"
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
image_services=(
  tor
  bisq
  bisq2
  unified-api
  unified-snapshot
  postgres-backup
  dexmonitor
  mm-controller
  totp-api
  fiat-controller
)
image_vars=(
  TOR_IMAGE_TAG
  BISQ_IMAGE_TAG
  BISQ2_IMAGE_TAG
  UNIFIED_API_IMAGE_TAG
  UNIFIED_SNAPSHOT_IMAGE_TAG
  POSTGRES_BACKUP_IMAGE_TAG
  DEXMONITOR_IMAGE_TAG
  MM_CONTROLLER_IMAGE_TAG
  TOTP_API_IMAGE_TAG
  FIAT_CONTROLLER_IMAGE_TAG
)
config_services=(grafana prometheus loki tempo alloy nginx)
changed_services=()

image_var_for_service() {
  local wanted="$1" index
  for index in "${!image_services[@]}"; do
    if [[ "${image_services[$index]}" == "$wanted" ]]; then
      printf '%s\n' "${image_vars[$index]}"
      return 0
    fi
  done
  return 1
}

parse_changed_services() {
  local raw service
  declare -A seen=()

  if ! "$changed_services_supplied"; then
    changed_services=("${image_services[@]}")
    return 0
  fi
  case "$changed_services_csv" in
    ,*|*,|*,,*)
      echo "changed service list contains an empty entry" >&2
      return 1
      ;;
  esac
  [[ -n "$changed_services_csv" ]] || return 0

  local IFS=','
  read -ra changed_services <<<"$changed_services_csv"
  for raw in "${changed_services[@]}"; do
    service="${raw//$'\r'/}"
    [[ "$service" == "$raw" && "$service" != *[[:space:]]* ]] || {
      echo "invalid changed service: $raw" >&2
      return 1
    }
    image_var_for_service "$service" >/dev/null || {
      echo "unknown changed service: $service" >&2
      return 1
    }
    [[ -z "${seen[$service]:-}" ]] || {
      echo "duplicate changed service: $service" >&2
      return 1
    }
    seen["$service"]=1
  done
}

parse_changed_services || exit 2

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

# Public callbacks follow the active edge identity, not the generic
# PUBLIC_HOSTNAME kept in the encrypted environment bundle. This keeps
# IP-edge development deployments reachable from a physical phone without a
# manual dashboard override, while production derives the equivalent domain
# URL.
configure_public_callback_urls() {
  local identifier telegram_url pushpush_url temporary
  [[ -r /etc/dexarb/edge.env ]] || return 0

  identifier="$(sed -nE 's/^EDGE_CERT_IDENTIFIER="?([^"[:space:]]+)"?$/\1/p' /etc/dexarb/edge.env | tail -n 1)"
  [[ "$identifier" =~ ^[A-Za-z0-9.-]+$ ]] || {
    echo "invalid EDGE_CERT_IDENTIFIER for public callback URLs" >&2
    return 1
  }
  telegram_url="https://${identifier}/telegram-webhook"
  pushpush_url="https://${identifier}"
  temporary=".env.public-callback-urls.$$"
  awk -v telegram_url="$telegram_url" -v pushpush_url="$pushpush_url" '
    /^DEXMM_TELEGRAM_WEBHOOK_URL=/ { print "DEXMM_TELEGRAM_WEBHOOK_URL=" telegram_url; telegram_found=1; next }
    /^PUSHPUSH_PUBLIC_BASE_URL=/ { print "PUSHPUSH_PUBLIC_BASE_URL=" pushpush_url; pushpush_found=1; next }
    { print }
    END {
      if (!telegram_found) print "DEXMM_TELEGRAM_WEBHOOK_URL=" telegram_url
      if (!pushpush_found) print "PUSHPUSH_PUBLIC_BASE_URL=" pushpush_url
    }
  ' .env >"$temporary"
  chmod 600 "$temporary"
  mv -f "$temporary" .env
  echo "configured system-managed public callback base URL: $pushpush_url"
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

write_uniform_image_manifest() {
  local destination="$1" sha="$2" variable
  [[ "$sha" =~ ^[0-9a-f]{40}$ ]] || return 1
  : >"$destination"
  chmod 600 "$destination"
  for variable in "${image_vars[@]}"; do
    printf '%s=%s\n' "$variable" "$sha" >>"$destination"
  done
}

validate_image_manifest() {
  local manifest="$1" line variable value expected variable_name
  declare -A seen=()

  [[ -f "$manifest" ]] || {
    echo "image manifest is missing: $manifest" >&2
    return 1
  }
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ "$line" =~ ^([A-Z][A-Z0-9_]*)=([0-9a-f]{40})$ ]] || {
      echo "invalid image manifest entry: $line" >&2
      return 1
    }
    variable="${BASH_REMATCH[1]}"
    value="${BASH_REMATCH[2]}"
    expected=false
    for variable_name in "${image_vars[@]}"; do
      if [[ "$variable_name" == "$variable" ]]; then
        expected=true
        break
      fi
    done
    "$expected" || {
      echo "unknown image manifest variable: $variable" >&2
      return 1
    }
    [[ -z "${seen[$variable]:-}" ]] || {
      echo "duplicate image manifest variable: $variable" >&2
      return 1
    }
    seen["$variable"]="$value"
  done <"$manifest"

  for variable in "${image_vars[@]}"; do
    [[ -n "${seen[$variable]:-}" ]] || {
      echo "image manifest is missing $variable" >&2
      return 1
    }
  done
  [[ "${#seen[@]}" -eq "${#image_vars[@]}" ]]
}

load_image_manifest() {
  local manifest="$1" variable value
  validate_image_manifest "$manifest" || return 1
  while IFS='=' read -r variable value; do
    export "$variable=$value"
  done <"$manifest"
}

ensure_previous_image_manifest() {
  local sha="$1" manifest
  [[ "$sha" =~ ^[0-9a-f]{40}$ ]] || return 0
  manifest="$release_root/$sha/images.env"
  if [[ -f "$manifest" ]]; then
    validate_image_manifest "$manifest"
    return
  fi

  # Migration from the former global IMAGE_TAG scheme: every service was
  # deliberately tagged with the release SHA, so that SHA is the exact active
  # image version for every entry in the first per-service manifest.
  echo "migrating release $sha from global to per-service image tags"
  mkdir -p "$release_root/$sha"
  write_uniform_image_manifest "$manifest.tmp" "$sha"
  mv -f "$manifest.tmp" "$manifest"
}

build_candidate_image_manifest() {
  local base_manifest="$1" destination="$2" service variable value
  declare -A values=()

  if [[ -n "$base_manifest" ]]; then
    validate_image_manifest "$base_manifest" || return 1
    while IFS='=' read -r variable value; do
      values["$variable"]="$value"
    done <"$base_manifest"
  else
    if [[ "${#changed_services[@]}" -ne "${#image_services[@]}" ]]; then
      echo "the first deployment must publish every service image" >&2
      return 1
    fi
    for variable in "${image_vars[@]}"; do
      values["$variable"]="$new_sha"
    done
  fi

  for service in "${changed_services[@]}"; do
    variable="$(image_var_for_service "$service")"
    values["$variable"]="$new_sha"
  done

  : >"$destination"
  chmod 600 "$destination"
  for variable in "${image_vars[@]}"; do
    printf '%s=%s\n' "$variable" "${values[$variable]}" >>"$destination"
  done
  validate_image_manifest "$destination"
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
  if ! output="$(docker compose config --quiet 2>&1)"; then
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

certificate_bundle_checksum() {
  local path
  for path in deploy/nginx/certs/server.crt deploy/nginx/certs/server.key; do
    if [[ -f "$path" ]]; then
      sha256sum "$path"
    else
      printf 'missing  %s\n' "$path"
    fi
  done | sha256sum | awk '{print $1}'
}

config_services_to_recreate() {
  local from_sha="$1" to_sha="$2" certificate_changed="$3" path service
  declare -A wanted=()

  if [[ "$from_sha" =~ ^[0-9a-f]{40}$ ]] &&
     git cat-file -e "$from_sha^{commit}" 2>/dev/null &&
     git cat-file -e "$to_sha^{commit}" 2>/dev/null; then
    while IFS= read -r path; do
      case "$path" in
        nginx/*) wanted[nginx]=1 ;;
        deploy/monitoring/prometheus.yml) wanted[prometheus]=1 ;;
        deploy/monitoring/grafana/*) wanted[grafana]=1 ;;
        deploy/monitoring/loki/*) wanted[loki]=1 ;;
        deploy/monitoring/tempo/*) wanted[tempo]=1 ;;
        deploy/monitoring/alloy/*) wanted[alloy]=1 ;;
      esac
    done < <(git diff --name-only "$from_sha" "$to_sha")
  else
    for service in "${config_services[@]}"; do
      wanted["$service"]=1
    done
  fi
  [[ "$certificate_changed" == true ]] && wanted[nginx]=1

  for service in "${config_services[@]}"; do
    [[ -n "${wanted[$service]:-}" ]] && printf '%s\n' "$service"
  done
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

pull_changed_images() {
  local service active
  declare -A active_services=()
  local pull_services=()

  while IFS= read -r active; do
    [[ -n "$active" ]] && active_services["$active"]=1
  done < <(docker compose config --services)
  for service in "${changed_services[@]}"; do
    [[ -n "${active_services[$service]:-}" ]] && pull_services+=("$service")
  done
  if ((${#pull_services[@]})); then
    docker compose pull "${pull_services[@]}"
  else
    echo "no active service image changed; skipping compose pull"
  fi
}

start_release() {
  local sha="$1" bundle="$2" manifest="$3" issue_certificate="$4" from_sha="$5"
  local certificate_before certificate_after certificate_changed=false
  local restart_services=()
  sync_tree "$sha" || return 1
  activate_bundle "$bundle" || return 1
  configure_public_callback_urls || return 1
  load_image_manifest "$manifest" || return 1
  compose_preflight || return 1
  if [[ "$issue_certificate" == true ]]; then
    certificate_before="$(certificate_bundle_checksum)"
    ensure_trusted_certificate || return 1
    certificate_after="$(certificate_bundle_checksum)"
    [[ "$certificate_before" != "$certificate_after" ]] &&
      certificate_changed=true
  fi
  pull_changed_images || return 1
  # Unified Snapshot's first cycle calls several external/Tor markets and can
  # legitimately exceed twenty minutes before writing its success marker. With
  # per-service tags, Compose keeps its existing container unless its image or
  # service configuration actually changed.
  docker compose up -d --remove-orphans \
    --wait --wait-timeout 1800 || return 1
  mapfile -t restart_services < <(
    config_services_to_recreate "$from_sha" "$sha" "$certificate_changed"
  )
  if ((${#restart_services[@]})); then
    docker compose up -d --force-recreate --no-deps \
      --wait --wait-timeout 120 "${restart_services[@]}" || return 1
  fi
  smoke || return 1
}

dump_failing_logs() {
  local manifest="$1" service
  load_image_manifest "$manifest" || true
  echo "--- container logs for failing services ---"
  while IFS= read -r service; do
    [[ -n "$service" ]] || continue
    echo "=== $service ==="
    docker logs --tail 80 "$service" 2>&1 || true
  done < <(
    docker compose ps --format '{{.Name}} {{.State}}' 2>/dev/null |
      awk '$2 != "running" {print $1}'
  )
  echo "--- all container states ---"
  docker compose ps 2>/dev/null || true
}

snapshot_active_bundle "$prev_sha" || true
previous_manifest=""
if [[ "$prev_sha" =~ ^[0-9a-f]{40}$ ]]; then
  ensure_previous_image_manifest "$prev_sha" || exit 1
  previous_manifest="$release_root/$prev_sha/images.env"
fi
build_candidate_image_manifest "$previous_manifest" "$candidate_manifest"

if start_release \
  "$new_sha" "$env_bundle" "$candidate_manifest" true "$prev_sha"; then
  copy_bundle "$env_bundle" "$release_root/$new_sha/env"
  install -m 600 "$candidate_manifest" \
    "$release_root/$new_sha/images.env.tmp"
  mv -f "$release_root/$new_sha/images.env.tmp" \
    "$release_root/$new_sha/images.env"
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

dump_failing_logs "$candidate_manifest"
echo "deploy of $new_sha failed; rolling back to ${prev_sha:-<none>}" >&2
if [[ "$prev_sha" =~ ^[0-9a-f]{40}$ &&
      -d "$release_root/$prev_sha/env" &&
      -f "$release_root/$prev_sha/images.env" ]]; then
  if start_release \
    "$prev_sha" \
    "$release_root/$prev_sha/env" \
    "$release_root/$prev_sha/images.env" \
    false \
    "$new_sha"; then
    echo "rolled back code, environment, and images to $prev_sha" >&2
  else
    echo "ROLLBACK to $prev_sha FAILED; manual intervention required" >&2
  fi
else
  echo "no complete previous release snapshot is available" >&2
fi
exit 1
