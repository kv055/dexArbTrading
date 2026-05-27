#!/usr/bin/env bash
# Automates deploy/README.md steps 1-3: terraform apply -> ansible playbook -> github secrets.
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

SSH_PRIVATE_KEY="${SSH_PRIVATE_KEY:-$HOME/.ssh/id_ed25519}"

usage() {
  cat <<EOF
Usage: $0 [--key <path-to-ssh-private-key>]

Runs Terraform (provision VPS), Ansible (configure VPS), and writes
GitHub repository secrets via deploy/setup-github-secrets.sh.

Options:
  --key <path>   Path to the SSH private key whose public half is in
                 deploy/terraform/terraform.tfvars. Default: ~/.ssh/id_ed25519
                 Can also be set via SSH_PRIVATE_KEY env var.
  -h, --help     Show this help.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --key) SSH_PRIVATE_KEY="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

die() { echo "ERROR: $*" >&2; exit 1; }
log() { printf '\n\033[1;36m==> %s\033[0m\n' "$*"; }

# ---------------------------------------------------------------------------
# Preconditions
# ---------------------------------------------------------------------------
log "Checking prerequisites"

for tool in terraform ansible-playbook ssh-keyscan git curl sed; do
  command -v "$tool" >/dev/null 2>&1 || die "missing required tool: $tool"
done

TFVARS="$SCRIPT_DIR/terraform/terraform.tfvars"
[[ -f "$TFVARS" ]] || die "missing $TFVARS — copy terraform.tfvars.example and fill it in"

[[ -f "$SSH_PRIVATE_KEY" ]]      || die "SSH private key not found: $SSH_PRIVATE_KEY"
[[ -f "${SSH_PRIVATE_KEY}.pub" ]] || die "matching public key not found: ${SSH_PRIVATE_KEY}.pub"

[[ -f "$REPO_ROOT/.env" ]] || die "missing $REPO_ROOT/.env (GH_PAT required for secrets step)"
grep -q '^GH_PAT=' "$REPO_ROOT/.env" || die "GH_PAT not set in $REPO_ROOT/.env"

echo "  terraform.tfvars : $TFVARS"
echo "  ssh private key  : $SSH_PRIVATE_KEY"

# ---------------------------------------------------------------------------
# Step 1: Terraform
# ---------------------------------------------------------------------------
log "Step 1/3: Terraform (Hetzner provisioning)"
cd "$SCRIPT_DIR/terraform"
terraform init -input=false
terraform apply -auto-approve -input=false
SERVER_IP="$(terraform output -raw server_ip)"
[[ -n "$SERVER_IP" ]] || die "terraform did not produce a server_ip output"
echo "  server_ip = $SERVER_IP"

# ---------------------------------------------------------------------------
# Step 2: Ansible
# ---------------------------------------------------------------------------
log "Step 2/3: Ansible (configure VPS)"
cd "$SCRIPT_DIR/ansible"

INVENTORY="$SCRIPT_DIR/ansible/inventory.ini"
# Substitute SERVER_IP and point ansible_ssh_private_key_file at the chosen key.
# Using # as sed delimiter so paths with / don't collide.
sed -e "s/SERVER_IP/$SERVER_IP/" \
    -e "s#ansible_ssh_private_key_file=[^[:space:]]*#ansible_ssh_private_key_file=$SSH_PRIVATE_KEY#" \
    inventory.ini.tpl > "$INVENTORY"

# Wait for sshd to be reachable (Hetzner usually takes <20s).
for i in {1..30}; do
  if ssh-keyscan -T 3 -t ed25519 "$SERVER_IP" >/dev/null 2>&1; then
    break
  fi
  [[ $i -eq 30 ]] && die "ssh on $SERVER_IP not reachable after 30 attempts"
  sleep 2
done

GH_PAT_VALUE="$(grep '^GH_PAT=' "$REPO_ROOT/.env" | cut -d= -f2-)"
ANSIBLE_HOST_KEY_CHECKING=False \
  ansible-playbook -i "$INVENTORY" playbook.yml \
    --private-key "$SSH_PRIVATE_KEY" \
    --extra-vars "gh_pat=$GH_PAT_VALUE"

# ---------------------------------------------------------------------------
# Step 3: GitHub secrets
# ---------------------------------------------------------------------------
log "Step 3/3: Generating deploy/.env.vps and uploading GitHub secrets"

ENV_VPS="$SCRIPT_DIR/.env.vps"
if [[ -f "$ENV_VPS" ]]; then
  cp -p "$ENV_VPS" "$ENV_VPS.bak"
  echo "  backed up existing .env.vps to .env.vps.bak"
fi

# Collect all three host key types for known_hosts.
KNOWN_HOSTS="$(ssh-keyscan -H "$SERVER_IP" 2>/dev/null | grep -v '^#' || true)"
[[ -n "$KNOWN_HOSTS" ]] || die "ssh-keyscan returned no host keys for $SERVER_IP"

PRIVATE_KEY_CONTENTS="$(cat "$SSH_PRIVATE_KEY")"

TMP_ENV="$(mktemp)"
trap 'rm -f "$TMP_ENV"' EXIT

{
  printf 'VPS_SSH_PRIVATE_KEY="%s"\n\n' "$PRIVATE_KEY_CONTENTS"
  printf 'VPS_SSH_KNOWN_HOSTS="%s"\n\n' "$KNOWN_HOSTS"
  printf 'VPS_SSH_HOST="%s"\n' "$SERVER_IP"
  printf 'VPS_SSH_USER="deploy"\n'
  printf 'VPS_REPO_DIR="/opt/dexArbTrading"\n'
} > "$TMP_ENV"

chmod 600 "$TMP_ENV"
mv "$TMP_ENV" "$ENV_VPS"
trap - EXIT

log "Summary"
echo "  server IP        : $SERVER_IP"
echo "  ssh manual login : ssh -i $SSH_PRIVATE_KEY deploy@$SERVER_IP"
echo "  inventory        : $INVENTORY"
echo "  env.vps          : $ENV_VPS"
echo ""
echo "Delegating to setup-github-secrets.sh..."
exec "$SCRIPT_DIR/setup-github-secrets.sh"
