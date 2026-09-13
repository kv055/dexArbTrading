#!/usr/bin/env bash
# Decide which image builds and Go test modules a CI event should run.
# Used by both GitHub and Gitea detect-changes jobs.
#
# Main pushes compare each image's source against a locked production snapshot.
# Missing baseline access fails; unavailable Git objects conservatively rebuild.
# workflow_dispatch builds everything for validation without deploying.
set -Eeuo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=image-service-inputs.sh
source "$repo_root/scripts/image-service-inputs.sh"
IMAGE_INPUTS_REPO="$repo_root"
cd "$repo_root"

ALL_SERVICES="$(image_input_services | paste -sd, -)"
ALL_MODULES="commons,dexUnifiedApi,dexMmController,dexMonitor,dexFiatController"
ZERO="0000000000000000000000000000000000000000"
EVENT="${EVENT:-}"
BEFORE="${BEFORE:-}"
AFTER="${AFTER:-}"
PR_BASE="${PR_BASE:-}"
PR_HEAD="${PR_HEAD:-}"
REF="${REF:-}"
REF_NAME="${REF_NAME:-}"
EVENT_REF="${EVENT_REF:-}"
GITHUB_OUTPUT="${GITHUB_OUTPUT:-/dev/stdout}"

add_csv() {
  local list="$1" item="$2"
  case ",$list," in *",$item,"*) echo "$list" ;; *) echo "${list:+$list,}$item" ;; esac
}

comma_to_json_array() {
  local csv="${1:-}"
  if [ -z "$csv" ]; then echo '[]'; return; fi
  local IFS=',' parts=() p json='[' first=true
  read -ra parts <<<"$csv"
  for p in "${parts[@]}"; do
    [ -n "$p" ] || continue
    $first || json+=','
    first=false
    json+="\"$p\""
  done
  json+=']'
  echo "$json"
}

build_matrix_json_for() {
  local csv="${1:-}"
  [ -n "$csv" ] || { echo '[]'; return; }
  local service context dockerfile
  for service in ${csv//,/ }; do
    read -r context dockerfile < <(image_service_build "$service")
    jq -n --arg service "$service" --arg context "$context" --arg dockerfile "$dockerfile" \
      '{service:$service, context:$context, dockerfile:$dockerfile}'
  done | jq -s .
}

write_outputs() {
  local changes="$1" test_modules="$2" run_frontend="$3"
  local test_modules_json build_matrix_json
  test_modules_json=$(comma_to_json_array "$test_modules")
  build_matrix_json=$(build_matrix_json_for "$changes")
  echo "changes=${changes:-}" >>"$GITHUB_OUTPUT"
  echo "test_modules=${test_modules:-}" >>"$GITHUB_OUTPUT"
  echo "run_frontend=${run_frontend}" >>"$GITHUB_OUTPUT"
  {
    echo 'test_modules_json<<EOF'
    echo "$test_modules_json"
    echo 'EOF'
    echo 'build_matrix_json<<EOF'
    echo "$build_matrix_json"
    echo 'EOF'
  } >>"$GITHUB_OUTPUT"
  echo "services to build: ${changes:-<none>}"
  echo "test modules: ${test_modules:-<none>}"
  echo "run frontend: ${run_frontend}"
  echo "build matrix: ${build_matrix_json}"
}

emit_all() {
  echo "${1:-build all}"
  write_outputs "$ALL_SERVICES" "$ALL_MODULES" "true"
}

is_main_push() {
  [[ "$EVENT" == "push" ]] || return 1
  [[ "$REF" == "refs/heads/main" ||
     "$REF_NAME" == "main" ||
     "$EVENT_REF" == "refs/heads/main" ]]
}

resolve_base() {
  local before="${1:-}" after="${2:-}"
  echo "resolve_base: event=${EVENT:-} before=${before:-<empty>} after=${after:-<empty>}" >&2
  if [ -n "$before" ] && [ "$before" != "$ZERO" ] &&
     git cat-file -e "${before}^{commit}" 2>/dev/null; then
    echo "$before"
    return 0
  fi
  if git cat-file -e "${after}^{commit}" 2>/dev/null &&
     parent=$(git rev-parse "${after}^" 2>/dev/null); then
    echo "before unusable (${before:-<empty>}); falling back to parent ${parent}" >&2
    echo "$parent"
    return 0
  fi
  echo ""
}

test_modules_for_services() {
  local services="$1" modules=""
  case ",$services," in
    *,unified-api,*|*,unified-snapshot,*|*,postgres-backup,*)
      modules=$(add_csv "$modules" dexUnifiedApi)
      ;;
  esac
  case ",$services," in
    *,dexmonitor,*) modules=$(add_csv "$modules" dexMonitor) ;;
  esac
  case ",$services," in
    *,mm-controller,*) modules=$(add_csv "$modules" dexMmController) ;;
  esac
  case ",$services," in
    *,fiat-controller,*) modules=$(add_csv "$modules" dexFiatController) ;;
  esac
  if parent_paths_changed "${BASE:?}" "${HEAD:?}" commons 2>/dev/null ||
     git diff --name-only "$BASE" "$HEAD" | grep -qE '^commons(/|$)'; then
    modules=$(add_csv "$modules" commons)
    modules=$(add_csv "$modules" dexUnifiedApi)
    modules=$(add_csv "$modules" dexMonitor)
    modules=$(add_csv "$modules" dexMmController)
    modules=$(add_csv "$modules" dexFiatController)
  fi
  local module
  for module in dexUnifiedApi dexMonitor dexMmController dexFiatController; do
    if repo_tree_paths_changed "$module" "$BASE" "$HEAD"; then
      modules=$(add_csv "$modules" "$module")
    fi
  done
  if parent_paths_changed "$BASE" "$HEAD" go.work go.work.sum; then
    modules="$ALL_MODULES"
  fi
  printf '%s\n' "$modules"
}

ensure_submodule_objects() {
  git submodule update --init --recursive >/dev/null 2>&1 || true
}

if [ "$EVENT" = "workflow_dispatch" ]; then
  emit_all "manual trigger -> build all"
  exit 0
fi

if [ "$EVENT" = "pull_request" ]; then
  BASE="${PR_BASE:-}"
  HEAD="${PR_HEAD:-}"
else
  BASE="${BEFORE:-}"
  HEAD="${AFTER:-}"
fi

if is_main_push; then
  : "${BASELINE_FILE:?production detection requires a production baseline snapshot}"
  BASE="$(jq -er '.release // "empty"' "$BASELINE_FILE")"
  if [[ "$BASE" == empty ]]; then
    emit_all "first deployment -> build all"
    exit 0
  fi
  [[ "$BASE" =~ ^[0-9a-f]{40}$ ]] || { echo 'invalid production baseline' >&2; exit 1; }
  git cat-file -e "$BASE^{commit}" 2>/dev/null || git fetch --no-tags origin "$BASE" || true
fi

if ! is_main_push; then BASE="$(resolve_base "$BASE" "$HEAD")"; fi
if [ -z "$BASE" ]; then
  emit_all "no usable base commit -> build all"
  exit 0
fi
HEAD="${HEAD:-$AFTER}"
git cat-file -e "$HEAD^{commit}" 2>/dev/null || { echo 'target commit unavailable' >&2; exit 1; }
if ! git cat-file -e "$BASE^{commit}" 2>/dev/null; then
  emit_all "baseline source unavailable -> build and test all"
  exit 0
fi

if git diff --name-only "$BASE" "$HEAD" | grep -qE '^\.gitmodules$'; then
  emit_all "gitmodules change -> build all"
  exit 0
fi

ensure_submodule_objects

changed_services=()
if is_main_push; then
  for service in $(image_input_services); do
    source_sha="$(jq -r --arg s "$service" '.services[$s].source // empty' "$BASELINE_FILE")"
    digest="$(jq -r --arg s "$service" '.services[$s].image // empty' "$BASELINE_FILE")"
    if [[ -n "$source_sha" ]]; then
      git cat-file -e "$source_sha^{commit}" 2>/dev/null || git fetch --no-tags origin "$source_sha" || true
    fi
    if [[ -z "$digest" || -z "$source_sha" ]] || image_service_inputs_changed "$service" "$source_sha" "$HEAD"; then
      echo "$service: build (missing digest/source, changed inputs, or uninspectable source ${source_sha:-none})"
      changed_services+=("$service")
    else
      echo "$service: reuse $source_sha $digest"
    fi
  done
else
  mapfile -t changed_services < <(image_services_changed_in_range "$BASE" "$HEAD")
fi
S=""
for service in "${changed_services[@]}"; do
  [[ -n "$service" ]] || continue
  S=$(add_csv "$S" "$service")
done

echo "diff range: ${BASE}..${HEAD}"
echo "changed parent paths:"
git diff --name-only "$BASE" "$HEAD"

M="$(test_modules_for_services "$S")"
run_frontend=false
if [[ -n "$S" || -n "$M" ]]; then
  run_frontend=true
fi
if $run_frontend; then
  write_outputs "$S" "$M" "true"
else
  write_outputs "$S" "$M" "false"
fi
