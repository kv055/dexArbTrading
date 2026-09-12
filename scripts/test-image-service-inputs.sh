#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=image-service-inputs.sh
source "$repo_root/scripts/image-service-inputs.sh"

fixture="$(mktemp -d)"
cleanup() { rm -rf "$fixture"; }
trap cleanup EXIT

make_repo() {
  local dir="$1"
  mkdir -p "$dir"
  git -C "$dir" init -q
  git -C "$dir" config user.name test
  git -C "$dir" config user.email test@example.invalid
}

make_repo "$fixture/unified"
mkdir -p "$fixture/unified/cmd/server" "$fixture/unified/bisqseed"
printf 'tor-old\n' >"$fixture/unified/Dockerfile.tor"
printf 'bisq2-old\n' >"$fixture/unified/Dockerfile.bisq2"
printf 'startup-old\n' >"$fixture/unified/bisqseed/bisq2-startup.sh"
printf 'go-old\n' >"$fixture/unified/cmd/server/main.go"
git -C "$fixture/unified" add .
git -C "$fixture/unified" commit -qm "unified base"

make_repo "$fixture/monitor"
mkdir -p "$fixture/monitor/internal"
printf 'ui-old\n' >"$fixture/monitor/internal/app.go"
git -C "$fixture/monitor" add .
git -C "$fixture/monitor" commit -qm "monitor base"

make_repo "$fixture/parent"
IMAGE_INPUTS_REPO="$fixture/parent"
git -C "$fixture/parent" config -f "$fixture/parent/.gitmodules" \
  submodule.dexUnifiedApi.path dexUnifiedApi
git -C "$fixture/parent" config -f "$fixture/parent/.gitmodules" \
  submodule.dexUnifiedApi.url "$fixture/unified"
git -C "$fixture/parent" config -f "$fixture/parent/.gitmodules" \
  submodule.dexMonitor.path dexMonitor
git -C "$fixture/parent" config -f "$fixture/parent/.gitmodules" \
  submodule.dexMonitor.url "$fixture/monitor"
mkdir -p "$fixture/parent/commons"
printf 'workspace-old\n' >"$fixture/parent/go.work"
printf 'commons-old\n' >"$fixture/parent/commons/mod.go"
git -C "$fixture/parent" add go.work commons
git -C "$fixture/parent" commit -qm "parent files"
git -C "$fixture/parent" -c protocol.file.allow=always submodule add --force \
  "$fixture/unified" dexUnifiedApi >/dev/null
git -C "$fixture/parent" -c protocol.file.allow=always submodule add --force \
  "$fixture/monitor" dexMonitor >/dev/null
git -C "$fixture/parent" commit -qm "parent base"
base="$(git -C "$fixture/parent" rev-parse HEAD)"

# Go-only UnifiedApi pin change must rebuild API images, not daemons.
printf 'go-new\n' >"$fixture/unified/cmd/server/main.go"
git -C "$fixture/unified" add cmd/server/main.go
git -C "$fixture/unified" commit -qm "go only"
git -C "$fixture/parent/dexUnifiedApi" -c protocol.file.allow=always pull -q
git -C "$fixture/parent" add dexUnifiedApi
git -C "$fixture/parent" commit -qm "pin go-only unified"
go_only="$(git -C "$fixture/parent" rev-parse HEAD)"

cd "$fixture/parent"
IMAGE_INPUTS_REPO="$fixture/parent"

image_service_inputs_changed unified-api "$base" "$go_only" || {
  echo "expected unified-api to change on Go-only pin" >&2
  exit 1
}
if image_service_inputs_changed unified-snapshot "$base" "$go_only"; then
  echo "server-only pin must not rebuild snapshot" >&2
  exit 1
fi
if image_service_inputs_changed postgres-backup "$base" "$go_only"; then
  echo "cmd/server/main.go must not rebuild postgres-backup" >&2
  exit 1
fi
if image_service_inputs_changed bisq2 "$base" "$go_only"; then
  echo "Go-only UnifiedApi pin must not rebuild bisq2" >&2
  exit 1
fi
if image_service_inputs_changed tor "$base" "$go_only"; then
  echo "Go-only UnifiedApi pin must not rebuild tor" >&2
  exit 1
fi
if image_service_inputs_changed bisq "$base" "$go_only"; then
  echo "Go-only UnifiedApi pin must not rebuild bisq" >&2
  exit 1
fi
if image_service_inputs_changed dexmonitor "$base" "$go_only"; then
  echo "Go-only UnifiedApi pin must not rebuild dexmonitor" >&2
  exit 1
fi

# Daemon Dockerfile / version pin rebuilds only that daemon.
printf 'bisq2-new\n' >"$fixture/unified/Dockerfile.bisq2"
git -C "$fixture/unified" add Dockerfile.bisq2
git -C "$fixture/unified" commit -qm "bisq2 version"
git -C "$fixture/parent/dexUnifiedApi" -c protocol.file.allow=always pull -q
git -C "$fixture/parent" add dexUnifiedApi
git -C "$fixture/parent" commit -qm "pin bisq2 dockerfile"
bisq2_only="$(git -C "$fixture/parent" rev-parse HEAD)"

image_service_inputs_changed bisq2 "$go_only" "$bisq2_only" || {
  echo "expected bisq2 to change when Dockerfile.bisq2 changes" >&2
  exit 1
}
if image_service_inputs_changed unified-api "$go_only" "$bisq2_only"; then
  echo "Dockerfile.bisq2 must not rebuild unified-api" >&2
  exit 1
fi
if image_service_inputs_changed tor "$go_only" "$bisq2_only"; then
  echo "Dockerfile.bisq2 must not rebuild tor" >&2
  exit 1
fi

# Monitor submodule pin rebuilds only dexmonitor.
printf 'ui-new\n' >"$fixture/monitor/internal/app.go"
git -C "$fixture/monitor" add internal/app.go
git -C "$fixture/monitor" commit -qm "ui"
git -C "$fixture/parent/dexMonitor" -c protocol.file.allow=always pull -q
git -C "$fixture/parent" add dexMonitor
git -C "$fixture/parent" commit -qm "pin monitor"
monitor_only="$(git -C "$fixture/parent" rev-parse HEAD)"

image_service_inputs_changed dexmonitor "$bisq2_only" "$monitor_only" || {
  echo "expected dexmonitor to change on monitor pin" >&2
  exit 1
}
if image_service_inputs_changed unified-api "$bisq2_only" "$monitor_only"; then
  echo "monitor pin must not rebuild unified-api" >&2
  exit 1
fi

# go.work is not copied into these images. It affects test selection only.
printf 'workspace-new\n' >"$fixture/parent/go.work"
git -C "$fixture/parent" add go.work
git -C "$fixture/parent" commit -qm "go.work"
gowork="$(git -C "$fixture/parent" rev-parse HEAD)"

if image_service_inputs_changed unified-api "$monitor_only" "$gowork"; then
  echo "go.work must not rebuild unified-api" >&2
  exit 1
fi
if image_service_inputs_changed dexmonitor "$monitor_only" "$gowork"; then
  echo "go.work must not rebuild dexmonitor" >&2
  exit 1
fi
if image_service_inputs_changed bisq2 "$monitor_only" "$gowork"; then
  echo "go.work must not rebuild bisq2" >&2
  exit 1
fi

# Shared source must invalidate every consumer, even when its gitlink did not move.
printf 'commons-new\n' >commons/mod.go
git add commons/mod.go
git commit -qm commons
shared="$(git rev-parse HEAD)"
for service in unified-api unified-snapshot postgres-backup dexmonitor mm-controller fiat-controller; do
  image_service_inputs_changed "$service" "$gowork" "$shared" || {
    echo "commons must rebuild $service" >&2; exit 1;
  }
done
if image_service_inputs_changed bisq2 "$gowork" "$shared"; then
  echo 'commons must not rebuild bisq2' >&2; exit 1
fi

printf 'module new\n' >"$fixture/unified/go.mod"
git -C "$fixture/unified" add go.mod
git -C "$fixture/unified" commit -qm dependencies
git -C dexUnifiedApi -c protocol.file.allow=always pull -q
git add dexUnifiedApi
git commit -qm dependencies
dependencies="$(git rev-parse HEAD)"
image_service_inputs_changed postgres-backup "$shared" "$dependencies" || {
  echo 'module manifest must rebuild postgres-backup' >&2; exit 1;
}

printf 'test only\n' >"$fixture/unified/cmd/server/main_test.go"
git -C "$fixture/unified" add cmd/server/main_test.go
git -C "$fixture/unified" commit -qm tests
git -C dexUnifiedApi -c protocol.file.allow=always pull -q
git add dexUnifiedApi
git commit -qm tests
tests="$(git rev-parse HEAD)"
if image_service_inputs_changed unified-api "$dependencies" "$tests"; then
  echo 'test-only change must not rebuild unified-api' >&2; exit 1
fi

printf 'ignore new\n' >.dockerignore
git add .dockerignore
git commit -qm ignores
ignores="$(git rev-parse HEAD)"
for service in unified-api unified-snapshot postgres-backup dexmonitor mm-controller fiat-controller; do
  image_service_inputs_changed "$service" "$tests" "$ignores" || {
    echo "root context rules must rebuild $service" >&2; exit 1;
  }
done

echo "image service input tests passed"
