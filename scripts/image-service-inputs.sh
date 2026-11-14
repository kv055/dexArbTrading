#!/usr/bin/env bash
# Shared image-input map for CI detect-changes and VPS deploy validation.
#
# A service is rebuilt only when files that are COPYed into its image, or that
# pin the upstream version the Dockerfile clones, change. Daemon images
# (tor/bisq/bisq2) are independent of Go changes in the same submodule.
#
# If a submodule pin moved but the old/new objects cannot be inspected, the
# helper reports "changed" so a missed rebuild cannot be inherited silently.

image_inputs_repo() {
  if [[ -n "${IMAGE_INPUTS_REPO:-}" ]]; then
    printf '%s\n' "$IMAGE_INPUTS_REPO"
    return
  fi
  git rev-parse --show-toplevel 2>/dev/null || pwd
}

image_input_services() {
  printf '%s\n' \
    tor bisq bisq2 bitcoind-rpc-proxy \
    unified-api unified-snapshot postgres-backup \
    dexmonitor mm-controller fiat-controller
}

image_service_variable() {
  local key="${1//-/_}"
  printf '%s_IMAGE_TAG\n' "${key^^}"
}

image_service_build() {
  case "$1" in
    tor|bisq|bisq2) printf 'dexUnifiedApi dexUnifiedApi/Dockerfile.%s\n' "$1" ;;
    bitcoind-rpc-proxy) echo 'deploy/bitcoind-rpc-proxy deploy/bitcoind-rpc-proxy/Dockerfile' ;;
    unified-api) echo '. dexUnifiedApi/Dockerfile' ;;
    unified-snapshot) echo '. dexUnifiedApi/Dockerfile.snapshot' ;;
    postgres-backup) echo '. dexUnifiedApi/Dockerfile.backup' ;;
    dexmonitor) echo '. dexMonitor/Dockerfile' ;;
    mm-controller) echo '. dexTakerController/Dockerfile' ;;
    fiat-controller) echo '. dexFiatController/Dockerfile' ;;
    *) return 1 ;;
  esac
}

ls_tree_sha() {
  local commit="$1" path="$2" entry
  entry="$(git ls-tree "$commit" "$path" 2>/dev/null | head -n 1)" || return 1
  [[ -n "$entry" ]] || return 1
  awk '{print $1, $2, $3}' <<<"$entry"
}

# git_dir for a submodule even after `git submodule deinit`.
submodule_git_dir() {
  local name="$1" repo root
  repo="$(image_inputs_repo)"
  if [[ -d "$repo/$name/.git" ]]; then
    git -C "$repo/$name" rev-parse --absolute-git-dir
    return
  fi
  if [[ -f "$repo/$name/.git" ]]; then
    git -C "$repo/$name" rev-parse --absolute-git-dir
    return
  fi
  root="$(git -C "$repo" rev-parse --absolute-git-dir)"
  if [[ -d "$root/modules/$name" ]]; then
    printf '%s\n' "$root/modules/$name"
    return
  fi
  return 1
}

ensure_git_objects() {
  local git_dir="$1" sha="$2"
  git --git-dir="$git_dir" cat-file -e "${sha}^{commit}" 2>/dev/null && return 0
  git --git-dir="$git_dir" fetch --no-tags origin "$sha" 2>/dev/null ||
    git --git-dir="$git_dir" fetch --no-tags origin 2>/dev/null ||
    true
  git --git-dir="$git_dir" cat-file -e "${sha}^{commit}" 2>/dev/null
}

# Return 0 if any of the given paths changed between two commits in a
# submodule or ordinary directory recorded at $name.
# With no paths, every file counts. Git pathspec exclusions are supported.
repo_tree_paths_changed() {
  local name="$1" from="$2" to="$3"
  shift 3
  local -a paths=("$@")
  local meta_from meta_to mode kind sha_from sha_to git_dir file

  meta_from="$(ls_tree_sha "$from" "$name" || true)"
  meta_to="$(ls_tree_sha "$to" "$name" || true)"
  if [[ -z "$meta_from" && -z "$meta_to" ]]; then
    return 1
  fi
  if [[ -z "$meta_from" || -z "$meta_to" ]]; then
    return 0
  fi
  mode="${meta_from%% *}"
  sha_from="$(awk '{print $3}' <<<"$meta_from")"
  sha_to="$(awk '{print $3}' <<<"$meta_to")"
  [[ "$sha_from" == "$sha_to" ]] && return 1

  kind="$(awk '{print $2}' <<<"$meta_to")"
  if [[ "$mode" != "160000" && "$kind" != "commit" ]]; then
    if ((${#paths[@]})); then
      local prefixed=() path
      for path in "${paths[@]}"; do
        if [[ "$path" == ':!:'* ]]; then
          prefixed+=(":!:$name/${path#:!:}")
        else
          prefixed+=("$name/$path")
        fi
      done
      ! git diff --quiet "$from" "$to" -- "${prefixed[@]}"
    else
      ! git diff --quiet "$from" "$to" -- "$name"
    fi
    return
  fi

  git_dir="$(submodule_git_dir "$name")" || return 0
  ensure_git_objects "$git_dir" "$sha_from" || return 0
  ensure_git_objects "$git_dir" "$sha_to" || return 0

  if ((${#paths[@]})); then
    ! git --git-dir="$git_dir" diff --quiet "$sha_from" "$sha_to" -- "${paths[@]}"
  else
    ! git --git-dir="$git_dir" diff --quiet "$sha_from" "$sha_to"
  fi
}

parent_paths_changed() {
  local from="$1" to="$2"
  shift 2
  git cat-file -e "${from}^{commit}" 2>/dev/null || return 0
  git cat-file -e "${to}^{commit}" 2>/dev/null || return 0
  ! git diff --quiet "$from" "$to" -- "$@"
}

# Return 0 when $service's image inputs changed from $from_sha to $to_sha.
image_service_inputs_changed() {
  local service="$1" from_sha="$2" to_sha="$3"

  git cat-file -e "${from_sha}^{commit}" 2>/dev/null || return 0
  git cat-file -e "${to_sha}^{commit}" 2>/dev/null || return 0

  parent_paths_changed "$from_sha" "$to_sha" .gitmodules \
    scripts/image-service-inputs.sh scripts/detect-changed-services.sh \
    .github/workflows/ci-cd.yml .gitea/workflows/ci.yml && return 0
  local context dockerfile
  read -r context dockerfile < <(image_service_build "$service")
  if [[ "$context" == . ]]; then
    parent_paths_changed "$from_sha" "$to_sha" .dockerignore commons \
      ':!:**/*_test.go' ':!:**/testdata/**' && return 0
  fi
  # Ignore-file rules themselves always invalidate the image.
  if [[ "$context" == dexUnifiedApi ]]; then
    repo_tree_paths_changed dexUnifiedApi "$from_sha" "$to_sha" \
      .dockerignore "${dockerfile#dexUnifiedApi/}.dockerignore" && return 0
  elif [[ "$context" == . ]]; then
    repo_tree_paths_changed "${dockerfile%%/*}" "$from_sha" "$to_sha" \
      "${dockerfile#*/}.dockerignore" && return 0
  fi

  case "$service" in
    tor)
      repo_tree_paths_changed dexUnifiedApi "$from_sha" "$to_sha" Dockerfile.tor
      ;;
    bisq)
      repo_tree_paths_changed dexUnifiedApi "$from_sha" "$to_sha" \
        Dockerfile.bisq bisq-patches bisqseed/bisq-startup.sh \
        bisqseed/ExtractBisqSeed.java
      ;;
    bisq2)
      repo_tree_paths_changed dexUnifiedApi "$from_sha" "$to_sha" \
        Dockerfile.bisq2 bisqseed/bisq2-startup.sh
      ;;
    bitcoind-rpc-proxy)
      parent_paths_changed "$from_sha" "$to_sha" deploy/bitcoind-rpc-proxy
      ;;
    unified-api)
      repo_tree_paths_changed dexUnifiedApi "$from_sha" "$to_sha" \
        Dockerfile go.mod go.sum cmd/server internal db ':!:**/*_test.go' ':!:**/testdata/**'
      ;;
    unified-snapshot)
      repo_tree_paths_changed dexUnifiedApi "$from_sha" "$to_sha" \
        Dockerfile.snapshot go.mod go.sum cmd/snapshot internal db bisq_grpc/go \
        ':!:**/*_test.go' ':!:**/testdata/**'
      ;;
    postgres-backup)
      repo_tree_paths_changed dexUnifiedApi "$from_sha" "$to_sha" \
        Dockerfile.backup go.mod go.sum cmd/pgbackup ':!:**/*_test.go' ':!:**/testdata/**'
      ;;
    dexmonitor)
      repo_tree_paths_changed dexMonitor "$from_sha" "$to_sha" \
        Dockerfile go.mod go.sum cmd/server internal react-frontend \
        ':!:**/*_test.go' ':!:**/testdata/**' \
        ':!:**/*_test.ts' ':!:**/*_test.tsx' ':!:**/e2e/**'
      ;;
    mm-controller)
      repo_tree_paths_changed dexTakerController "$from_sha" "$to_sha" \
        Dockerfile go.mod go.sum main.go main_lnd_backup.go main_lnd_provision.go \
        boltz_runtime.go internal ':!:**/*_test.go' ':!:**/testdata/**'
      ;;
    fiat-controller)
      repo_tree_paths_changed dexFiatController "$from_sha" "$to_sha" \
        . ':!:**/*_test.go' ':!:**/testdata/**' ':!:services/totp-api' \
        ':!:.env' ':!:.env.enc' ':!:.env.local' ':!:.env.production' \
        ':!:**/node_modules/**' ':!:**/dist/**' ':!:**/build/**'
      ;;
    *)
      return 0
      ;;
  esac
}

# Print changed image services, one per line, for parent-repo range from..to.
image_services_changed_in_range() {
  local from_sha="$1" to_sha="$2" service
  for service in $(image_input_services); do
    if image_service_inputs_changed "$service" "$from_sha" "$to_sha"; then
      printf '%s\n' "$service"
    fi
  done
}
