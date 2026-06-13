#!/usr/bin/env bash
# Trial-run bootstrap for a self-hosted Gitea instance (reached over Tailscale).
# Idempotent: connects to Gitea, ensures the 6 repos exist (creates + pushes if
# missing), runs the secrets transfer, enables Actions per repo, and prepares a
# runner registration token. Does NOT touch origin / .gitmodules / GitHub.
#
#   1. cp deploy/provision/gitea/.env.gitea.example deploy/provision/gitea/.env.gitea
#   2. fill it in, then: bash deploy/provision/gitea/setup-gitea.sh
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
cd "$REPO_ROOT"

die() { echo "ERROR: $*" >&2; exit 1; }
log() { printf '\n\033[1;36m==> %s\033[0m\n' "$*"; }

# ---------------------------------------------------------------------------
# Config + preconditions
# ---------------------------------------------------------------------------
ENV_GITEA="$SCRIPT_DIR/.env.gitea"
[[ -f "$ENV_GITEA" ]] || die "missing $ENV_GITEA — copy .env.gitea.example and fill it in"
# shellcheck disable=SC1090
set -a; . "$ENV_GITEA"; set +a

: "${GITEA_URL:?GITEA_URL not set in .env.gitea}"
: "${GITEA_OWNER:?GITEA_OWNER not set in .env.gitea}"
: "${GITEA_TOKEN:?GITEA_TOKEN not set in .env.gitea}"
: "${RUNNER_NAME:=homeserver-1}"
: "${RUNNER_LABELS:=ubuntu-latest:docker://catthehacker/ubuntu:act-22.04}"

GITEA_URL="${GITEA_URL%/}"
API="$GITEA_URL/api/v1"

for tool in curl jq git sed; do
  command -v "$tool" >/dev/null 2>&1 || die "missing required tool: $tool (install all with: sudo apt install -y curl jq git)"
done

# curl helpers ---------------------------------------------------------------
AUTH=(-H "Authorization: Bearer $GITEA_TOKEN" -H "Content-Type: application/json")

# api_code METHOD PATH [json-body] -> prints HTTP status, body to /dev/null
api_code() {
  local method="$1" path="$2" body="${3-}"
  if [[ -n "$body" ]]; then
    curl -s -o /dev/null -w '%{http_code}' -X "$method" "${AUTH[@]}" -d "$body" "$API$path"
  else
    curl -s -o /dev/null -w '%{http_code}' -X "$method" "${AUTH[@]}" "$API$path"
  fi
}

# api_get PATH -> prints body (fails on non-2xx)
api_get() { curl -fsS "${AUTH[@]}" "$API$1"; }

# ---------------------------------------------------------------------------
# 1. Validate token + detect owner type
# ---------------------------------------------------------------------------
log "Connecting to Gitea: $GITEA_URL"
ME="$(api_get /user)" || die "cannot reach Gitea or token invalid (GET $API/user)"
LOGIN="$(echo "$ME" | jq -r .login)"
echo "  authenticated as: $LOGIN"

OWNER_TYPE="user"
if [[ "$(api_code GET "/orgs/$GITEA_OWNER")" == "200" ]]; then
  OWNER_TYPE="org"
fi
echo "  owner: $GITEA_OWNER ($OWNER_TYPE)"

# ---------------------------------------------------------------------------
# 2 + 3. Ensure repos exist, then push local clones (additive 'gitea' remote)
# ---------------------------------------------------------------------------
# name -> local path. Root repo lives at the repo root; submodules at their dir.
REPOS=(dexArbTrading dexUnifiedApi dexMmTradingLogicHummingBot dexMmController dexMonitor dexFiatController)
declare -A REPO_DIR=(
  [dexArbTrading]="."
  [dexUnifiedApi]="dexUnifiedApi"
  [dexMmTradingLogicHummingBot]="dexMmTradingLogicHummingBot"
  [dexMmController]="dexMmController"
  [dexMonitor]="dexMonitor"
  [dexFiatController]="dexFiatController"
)

ensure_repo() {
  local name="$1" code
  code="$(api_code GET "/repos/$GITEA_OWNER/$name")"
  case "$code" in
    200) echo "  exists: $name" ;;
    404)
      local endpoint body
      body="$(jq -n --arg n "$name" '{name:$n, private:true, auto_init:false}')"
      if [[ "$OWNER_TYPE" == "org" ]]; then
        endpoint="/orgs/$GITEA_OWNER/repos"
      elif [[ "$LOGIN" == "$GITEA_OWNER" ]]; then
        endpoint="/user/repos"
      else
        endpoint="/admin/users/$GITEA_OWNER/repos"
      fi
      local rc; rc="$(api_code POST "$endpoint" "$body")"
      [[ "$rc" == "201" ]] || die "failed to create $name (HTTP $rc via $endpoint)"
      echo "  created: $name"
      ;;
    *) die "unexpected HTTP $code checking repo $name" ;;
  esac
}

push_repo() {
  local name="$1" dir="${REPO_DIR[$1]}"
  [[ -d "$dir/.git" || -f "$dir/.git" ]] || { echo "  WARN: $dir is not a git checkout — skipping push"; return; }

  local url="$GITEA_URL/$GITEA_OWNER/$name.git"
  # token-in-URL for the push only — never written to .git/config
  local turl; turl="$(printf '%s' "$url" | sed -E "s#^(https?://)#\1${GITEA_TOKEN}@#")"

  # additive clean remote 'gitea' (origin is left untouched)
  if git -C "$dir" remote get-url gitea >/dev/null 2>&1; then
    git -C "$dir" remote set-url gitea "$url"
  else
    git -C "$dir" remote add gitea "$url"
  fi

  local pushed=0
  if [[ -n "$(git -C "$dir" for-each-ref refs/heads)" ]]; then
    git -C "$dir" push --quiet "$turl" --all && pushed=1 || true
  fi
  git -C "$dir" push --quiet "$turl" --tags || true
  if [[ "$pushed" -eq 0 ]]; then
    # detached HEAD with no local branch (typical for submodules): seed 'main'
    git -C "$dir" push --quiet "$turl" "HEAD:refs/heads/main" && pushed=1 || true
  fi
  [[ "$pushed" -eq 1 ]] && echo "  pushed: $name" || echo "  WARN: nothing pushed for $name"
}

log "Ensuring repos exist on Gitea"
for r in "${REPOS[@]}"; do ensure_repo "$r"; done

log "Pushing local clones (additive 'gitea' remote; origin untouched)"
for r in "${REPOS[@]}"; do push_repo "$r"; done

# Point the root repo's default branch at 'develop' (the trial branch) so the Gitea
# UI and workflow_dispatch target it. No-op if develop wasn't pushed.
api_code PATCH "/repos/$GITEA_OWNER/dexArbTrading" '{"default_branch":"develop"}' >/dev/null || true

# ---------------------------------------------------------------------------
# 4. Enable Actions per repo
# ---------------------------------------------------------------------------
log "Enabling Actions on each repo"
for r in "${REPOS[@]}"; do
  rc="$(api_code PATCH "/repos/$GITEA_OWNER/$r" '{"has_actions": true}')"
  [[ "$rc" == "200" ]] && echo "  actions on: $r" || echo "  WARN: could not enable actions on $r (HTTP $rc)"
done

# ---------------------------------------------------------------------------
# 5. Secrets transfer (existing script, now env-driven)
# ---------------------------------------------------------------------------
log "Transferring secrets (deploy/provision/secrets/setup-gitea-secrets.sh)"
# Pass the token's actual login so the registry-login secret is unambiguous.
GITEA_REGISTRY_USER="$LOGIN" "$REPO_ROOT/deploy/provision/secrets/setup-gitea-secrets.sh"

# Docker daemon needs the Gitea registry marked insecure so `docker login`/pull work
# over plain HTTP (harmless for an HTTPS host). Mounted into the DinD daemon by
# act-runner-compose.yml. Travels with the gitea/ dir to the homeserver.
REGISTRY_HOST="${GITEA_URL#*://}"
cat > "$SCRIPT_DIR/daemon.json" <<EOF
{"insecure-registries": ["$REGISTRY_HOST"]}
EOF
echo "  wrote $SCRIPT_DIR/daemon.json (insecure-registries: $REGISTRY_HOST)"

# ---------------------------------------------------------------------------
# 6. Runner registration token + act-runner.env
# ---------------------------------------------------------------------------
log "Fetching runner registration token"
reg_path="/repos/$GITEA_OWNER/dexArbTrading/actions/runners/registration-token"
[[ "$OWNER_TYPE" == "org" ]] && reg_path="/orgs/$GITEA_OWNER/actions/runners/registration-token"

# Endpoint is GET on current Gitea; older builds used POST. Try GET then POST.
REG_JSON="$(curl -fsS "${AUTH[@]}" "$API$reg_path" 2>/dev/null || true)"
[[ -z "$REG_JSON" ]] && REG_JSON="$(curl -fsS -X POST "${AUTH[@]}" "$API$reg_path" 2>/dev/null || true)"
REG_TOKEN="$(echo "$REG_JSON" | jq -r '.token // empty' 2>/dev/null || true)"
[[ -n "$REG_TOKEN" ]] || die "could not obtain a runner registration token from $reg_path — is [actions] ENABLED=true in Gitea's app.ini, and does the token have admin/repo-admin scope?"

RUNNER_ENV="$SCRIPT_DIR/act-runner.env"
umask 077
cat > "$RUNNER_ENV" <<EOF
GITEA_INSTANCE_URL=$GITEA_URL
GITEA_RUNNER_REGISTRATION_TOKEN=$REG_TOKEN
GITEA_RUNNER_NAME=$RUNNER_NAME
GITEA_RUNNER_LABELS=$RUNNER_LABELS
EOF
echo "  wrote $RUNNER_ENV (registration token, mode 600)"

# ---------------------------------------------------------------------------
# 7. Deploy runner to homeserver and start it
# ---------------------------------------------------------------------------
log "Deploying runner to homeserver"

: "${HOMESERVER_SSH_USER:?HOMESERVER_SSH_USER not set in .env.gitea}"
: "${HOMESERVER_SSH_HOST:?HOMESERVER_SSH_HOST not set in .env.gitea}"
: "${HOMESERVER_RUNNER_DIR:=~/gitea-runner}"

SSH_TARGET="$HOMESERVER_SSH_USER@$HOMESERVER_SSH_HOST"

echo "  target: $SSH_TARGET:$HOMESERVER_RUNNER_DIR"

ssh "$SSH_TARGET" "mkdir -p $HOMESERVER_RUNNER_DIR"

scp \
  "$SCRIPT_DIR/act-runner-compose.yml" \
  "$SCRIPT_DIR/act-runner.env" \
  "$SCRIPT_DIR/daemon.json" \
  "$SSH_TARGET:$HOMESERVER_RUNNER_DIR/"

ssh "$SSH_TARGET" "cd $HOMESERVER_RUNNER_DIR && docker compose -f act-runner-compose.yml up -d"

echo "  runner started on $HOMESERVER_SSH_HOST"

# ---------------------------------------------------------------------------
# Done
# ---------------------------------------------------------------------------
log "Gitea bootstrap complete"
cat <<EOF
Trigger the trial:
  git push gitea develop
  — or use "Run workflow" (workflow_dispatch) in the Gitea UI.

The runner should appear online in Gitea → dexArbTrading → Settings → Actions → Runners.

Tear everything down later (no artifacts left):
  ssh $SSH_TARGET "cd $HOMESERVER_RUNNER_DIR && bash teardown.sh"
EOF
