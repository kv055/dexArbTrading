#!/usr/bin/env bash
# Install/update the reconciliation units in the runner user's systemd manager.
# provision-runner-user.sh enables lingering, so the timer remains active after
# SSH sessions end and starts automatically at boot without a root-owned unit.
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
UNIT_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
mkdir -p "$UNIT_DIR"

service_contents="$(<"$SCRIPT_DIR/gitea-runner-reconcile.service")"
service_contents="${service_contents//@RUNNER_DIR@/$SCRIPT_DIR}"
printf '%s\n' "$service_contents" > "$UNIT_DIR/gitea-runner-reconcile.service"
install -m 0644 "$SCRIPT_DIR/gitea-runner-reconcile.timer" "$UNIT_DIR/gitea-runner-reconcile.timer"

systemctl --user daemon-reload
systemctl --user enable gitea-runner-reconcile.timer
systemctl --user restart gitea-runner-reconcile.timer
systemctl --user is-enabled --quiet gitea-runner-reconcile.timer

echo "Enabled gitea-runner-reconcile.timer for $SCRIPT_DIR"
