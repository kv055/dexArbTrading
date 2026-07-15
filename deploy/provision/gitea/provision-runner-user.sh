#!/usr/bin/env bash
# Run-once bootstrap: create the dedicated `gitea-runner` user on the homeserver.
#
# WHY a separate script (not part of setup-gitea.sh):
#   setup-gitea.sh connects to the homeserver AS the runner user (HOMESERVER_SSH_USER,
#   = gitea-runner) to scp the runner files and `docker compose up`. It therefore
#   cannot be the thing that CREATES that user — that needs a sudo-capable admin login
#   (HOMESERVER_ADMIN_SSH_USER, e.g. umbrel). This chicken-and-egg is why provisioning
#   lives here. Run this ONCE before the first setup-gitea.sh; it's idempotent, so
#   re-running is harmless.
#
# WHAT it does, over a single sudo SSH session as the admin user:
#   1. creates `gitea-runner` (no password — key-only, like the admin account)
#   2. adds it to the `docker` group so it can run `docker compose` for the runner
#   3. authorizes the SAME SSH key(s) already used by the admin user (copies the
#      admin's authorized_keys) — no separate keypair to manage for a homelab trial.
#
#   bash deploy/provision/gitea/provision-runner-user.sh
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

die() { echo "ERROR: $*" >&2; exit 1; }
log() { printf '\n\033[1;36m==> %s\033[0m\n' "$*"; }

# ---------------------------------------------------------------------------
# Config (shared with setup-gitea.sh)
# ---------------------------------------------------------------------------
ENV_GITEA="$SCRIPT_DIR/.env.gitea"
[[ -f "$ENV_GITEA" ]] || die "missing $ENV_GITEA — copy .env.gitea.example and fill it in"
set -a
# shellcheck disable=SC1090
. "$ENV_GITEA"
set +a

: "${HOMESERVER_SSH_HOST:?HOMESERVER_SSH_HOST not set in .env.gitea}"
: "${HOMESERVER_SSH_USER:=gitea-runner}"          # the user we're creating
: "${HOMESERVER_ADMIN_SSH_USER:?HOMESERVER_ADMIN_SSH_USER not set in .env.gitea (a sudo-capable account, e.g. umbrel)}"

RUNNER_USER="$HOMESERVER_SSH_USER"
ADMIN="$HOMESERVER_ADMIN_SSH_USER"
HOST="$HOMESERVER_SSH_HOST"

[[ "$RUNNER_USER" != "$ADMIN" ]] || die "HOMESERVER_SSH_USER ($RUNNER_USER) must differ from HOMESERVER_ADMIN_SSH_USER ($ADMIN)"

log "Provisioning runner user '$RUNNER_USER' on $ADMIN@$HOST"

# Two-step so interactive `sudo` works: piping the script into `sudo bash -s` over
# stdin leaves no terminal for sudo's password prompt. Instead (1) upload the
# provisioning body to a temp file on the host (no sudo, stdin = heredoc), then
# (2) run it under sudo over an `ssh -t` session so sudo can prompt on the TTY.
# Values are passed as positional args ($1 runner, $2 admin) — nothing is
# interpolated by the local shell into the quoted heredoc body.
REMOTE_TMP="/tmp/provision-runner-user.$$.sh"

# The temp path is generated locally and intentionally expanded into the
# trusted remote command.
# shellcheck disable=SC2029
ssh "$ADMIN@$HOST" "cat > '$REMOTE_TMP' && chmod +x '$REMOTE_TMP'" <<'REMOTE'
set -Eeuo pipefail
RUNNER_USER="$1"
ADMIN="$2"

# 1. Create the user if missing (no password; key-only access).
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

# A home dir left over from a prior attempt can be owned by a stale UID. sshd's
# StrictModes then refuses to read authorized_keys (silently falls back to a
# password prompt), so force ownership to the user and drop group/other write.
if [ -d "$RUNNER_HOME" ]; then
  chown -R "$RUNNER_USER:$RUNNER_USER" "$RUNNER_HOME"
  chmod g-w,o-w "$RUNNER_HOME"
fi

# 2. Docker access: needed so `docker compose up` can start the dind + act_runner
#    containers. If there's no docker group, the runner user will fall back to
#    needing sudo — flagged by the verification step in the script header.
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

# 3. Reuse the admin user's authorized key(s): any key that can log into $ADMIN can
#    now log into $RUNNER_USER. No new keypair for this homelab trial.
ADMIN_HOME="$(getent passwd "$ADMIN" | cut -d: -f6)"
ADMIN_KEYS="$ADMIN_HOME/.ssh/authorized_keys"
[ -f "$ADMIN_KEYS" ] || { echo "admin authorized_keys not found at $ADMIN_KEYS" >&2; exit 1; }

install -d -m 700 -o "$RUNNER_USER" -g "$RUNNER_USER" "$RUNNER_HOME/.ssh"
install -m 600 -o "$RUNNER_USER" -g "$RUNNER_USER" "$ADMIN_KEYS" "$RUNNER_HOME/.ssh/authorized_keys"
echo "  authorized $(wc -l < "$RUNNER_HOME/.ssh/authorized_keys") key(s) from $ADMIN for $RUNNER_USER"

# 4. Keep the runner's systemd user manager alive after SSH sessions end and
# across reboots. setup-gitea.sh installs/enables the user-owned timer itself.
loginctl enable-linger "$RUNNER_USER"
echo "  enabled systemd lingering for $RUNNER_USER"
REMOTE

# Run it under sudo with a TTY (-t) so the password prompt has somewhere to go,
# then clean up the temp file regardless of outcome.
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
Runner user '$RUNNER_USER' provisioned on $HOST.

Verify (should NOT prompt for a password, and docker must work):
  ssh $RUNNER_USER@$HOST docker ps

If 'docker ps' is denied, this host has no usable docker group for the user — add
$RUNNER_USER to the group owning /var/run/docker.sock, or grant passwordless sudo.

Then run the runner deploy:
  bash deploy/provision/gitea/setup-gitea.sh
EOF
