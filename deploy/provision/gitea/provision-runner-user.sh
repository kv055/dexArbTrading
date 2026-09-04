#!/usr/bin/env bash
# Prepare the homeserver account that owns the Gitea Actions runner.
#
# On Umbrel OS the runner must be the stock OS-image user (umbrel, uid 1000).
# That account survives Rugix overlay rebuilds after an unclean reboot.
# A dedicated adduser account (the old gitea-runner) lives only in /etc/passwd
# on the writable overlay and disappears when that overlay is discarded.
#
# setup-gitea.sh SSHes in as HOMESERVER_SSH_USER to scp runner files and
# `docker compose up`. This script, run with sudo as HOMESERVER_ADMIN_SSH_USER,
# grants that account docker + systemd lingering. When SSH user == admin
# (the Umbrel default), it does not create a second user.
#
#   bash deploy/provision/gitea/provision-runner-user.sh
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

die() { echo "ERROR: $*" >&2; exit 1; }
log() { printf '\n\033[1;36m==> %s\033[0m\n' "$*"; }

ENV_GITEA="$SCRIPT_DIR/.env.gitea"
[[ -f "$ENV_GITEA" ]] || die "missing $ENV_GITEA — copy .env.gitea.example and fill it in"
set -a
# shellcheck disable=SC1090
. "$ENV_GITEA"
set +a

: "${HOMESERVER_SSH_HOST:?HOMESERVER_SSH_HOST not set in .env.gitea}"
: "${HOMESERVER_SSH_USER:=umbrel}"
: "${HOMESERVER_ADMIN_SSH_USER:=$HOMESERVER_SSH_USER}"

RUNNER_USER="$HOMESERVER_SSH_USER"
ADMIN="$HOMESERVER_ADMIN_SSH_USER"
HOST="$HOMESERVER_SSH_HOST"

log "Provisioning runner owner '$RUNNER_USER' on $ADMIN@$HOST"

REMOTE_TMP="/tmp/provision-runner-user.$$.sh"

# shellcheck disable=SC2029
ssh "$ADMIN@$HOST" "cat > '$REMOTE_TMP' && chmod +x '$REMOTE_TMP'" <<'REMOTE'
set -Eeuo pipefail
RUNNER_USER="$1"
ADMIN="$2"

if id "$RUNNER_USER" >/dev/null 2>&1; then
  echo "  user exists: $RUNNER_USER"
else
  if command -v adduser >/dev/null 2>&1; then
    adduser --disabled-password --gecos "" "$RUNNER_USER"
  else
    useradd --create-home --shell /bin/bash "$RUNNER_USER"
  fi
  echo "  created user: $RUNNER_USER"
fi

RUNNER_HOME="$(getent passwd "$RUNNER_USER" | cut -d: -f6)"
[ -n "$RUNNER_HOME" ] || { echo "could not resolve home dir for $RUNNER_USER" >&2; exit 1; }

# Only rewrite home ownership for a dedicated (non-admin) account whose leftover
# home may be owned by a stale UID after an overlay reset. Never chown -R the
# stock umbrel home.
if [ "$RUNNER_USER" != "$ADMIN" ] && [ -d "$RUNNER_HOME" ]; then
  chown -R "$RUNNER_USER:$RUNNER_USER" "$RUNNER_HOME"
  chmod g-w,o-w "$RUNNER_HOME"
fi

if getent group docker >/dev/null 2>&1; then
  if id -nG "$RUNNER_USER" | tr ' ' '\n' | grep -qx docker; then
    echo "  already in docker group: $RUNNER_USER"
  else
    usermod -aG docker "$RUNNER_USER"
    echo "  added to docker group: $RUNNER_USER"
  fi
else
  echo "  WARN: no 'docker' group on host — $RUNNER_USER may need passwordless sudo for docker"
fi

if [ "$RUNNER_USER" != "$ADMIN" ]; then
  ADMIN_HOME="$(getent passwd "$ADMIN" | cut -d: -f6)"
  ADMIN_KEYS="$ADMIN_HOME/.ssh/authorized_keys"
  [ -f "$ADMIN_KEYS" ] || { echo "admin authorized_keys not found at $ADMIN_KEYS" >&2; exit 1; }
  install -d -m 700 -o "$RUNNER_USER" -g "$RUNNER_USER" "$RUNNER_HOME/.ssh"
  install -m 600 -o "$RUNNER_USER" -g "$RUNNER_USER" "$ADMIN_KEYS" "$RUNNER_HOME/.ssh/authorized_keys"
  echo "  authorized $(wc -l < "$RUNNER_HOME/.ssh/authorized_keys") key(s) from $ADMIN for $RUNNER_USER"
fi

# Move leftover overlay-user runner files onto the persist home of the OS-image
# user so compose project name (gitea-runner) and named volumes stay the same.
OLD_RUNNER_DIR="/home/gitea-runner/gitea-runner"
NEW_RUNNER_DIR="$RUNNER_HOME/gitea-runner"
if [ "$RUNNER_USER" != "gitea-runner" ] && [ -d "$OLD_RUNNER_DIR" ]; then
  if [ ! -e "$NEW_RUNNER_DIR" ]; then
    mkdir -p "$RUNNER_HOME"
    cp -a "$OLD_RUNNER_DIR" "$NEW_RUNNER_DIR"
    chown -R "$RUNNER_USER:$RUNNER_USER" "$NEW_RUNNER_DIR"
    echo "  migrated $OLD_RUNNER_DIR -> $NEW_RUNNER_DIR"
  else
    echo "  skip migrate: $NEW_RUNNER_DIR already exists"
  fi
fi

loginctl enable-linger "$RUNNER_USER"
echo "  enabled systemd lingering for $RUNNER_USER"
REMOTE

log "Running provisioning as root (sudo may prompt for $ADMIN's password)"
ssh -t "$ADMIN@$HOST" "sudo bash '$REMOTE_TMP' '$RUNNER_USER' '$ADMIN'; rc=\$?; rm -f '$REMOTE_TMP'; exit \$rc"

log "Verifying runner account and persistent user manager"
ssh "$RUNNER_USER@$HOST" '
  id -nG | tr " " "\n" | grep -qx docker
  docker info >/dev/null
  loginctl show-user "$USER" -p Linger --value | grep -qx yes
'

log "Done"
cat <<EOF
Runner owner '$RUNNER_USER' is ready on $HOST.

Verify (should NOT prompt for a password, and docker must work):
  ssh $RUNNER_USER@$HOST docker ps

If 'docker ps' is denied, add $RUNNER_USER to the group that owns
/var/run/docker.sock, or grant passwordless sudo.

Then deploy the runner:
  bash deploy/provision/gitea/setup-gitea.sh
EOF
