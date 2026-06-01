# VPS Deploy

## Infrastructure as Code (Terraform + Ansible)

The `terraform/` and `ansible/` subdirectories provision and configure the VPS from scratch. Run these once before the CI/CD pipeline takes over.

### Automated provisioning (recommended)

A single script runs all three steps (Terraform, Ansible, GitHub secrets) inside a Docker container — no host-side Terraform or Ansible required.

**Prerequisites:**

- Docker
- A Hetzner Cloud API token (read/write) from [console.hetzner.cloud](https://console.hetzner.cloud)
- An SSH keypair for the deploy user
- `GH_PAT` set in the root `.env` file (used by Ansible to clone private submodules and by the secrets upload step)

```bash
# Fill in your Hetzner token and SSH public key
cp deploy/provision/terraform/terraform.tfvars.example deploy/provision/terraform/terraform.tfvars
$EDITOR deploy/provision/terraform/terraform.tfvars

# Run — defaults to ~/.ssh/id_ed25519; override with --key <path>
deploy/provision/run.sh [--key ~/.ssh/your_key]
```

This creates a **cx33** server (4 vCPU, 8 GB RAM, 80 GB disk, Debian 12) in `hel1` (Helsinki), configures it with Docker CE and the `deploy` user, clones the repo to `/opt/dexArbTrading`, and writes all 11 GitHub repository secrets. The next push to `main` deploys the full compose stack automatically.

The script is idempotent — safe to re-run if it fails partway through.

### Teardown

```bash
cd deploy/provision/terraform
terraform destroy
```

### Regenerating DBeaver connection files

The VPS IP changes whenever Terraform recreates the box. `deploy/provision/dbeaver-sync.sh` generates a DBeaver project archive (`dexarb-vps.dbp`, with the connection details and an AES-encrypted password bundled) from the current `deploy/provision/.env.vps` and the root `.env`. Output lands in `deploy/provision/dbeaver-out/` (gitignored).

```bash
deploy/provision/dbeaver-sync.sh
```

Import it via DBeaver: **File → Import → DBeaver → Project**, then select `deploy/provision/dbeaver-out/dexarb-vps.dbp`. It imports as a separate project (`dexArbTrading-vps-import`) with the password bundled. The import always creates a new project, so to re-import after an IP change, delete the existing `dexArbTrading-vps-import` project first.

---

### Manual steps (alternative)

If you prefer to run the steps individually, install [Terraform](https://developer.hashicorp.com/terraform/install) ≥ 1.5 and [Ansible](https://docs.ansible.com/ansible/latest/installation_guide/) ≥ 2.14 on your host, then follow the steps below.

### 1 — Provision with Terraform

```bash
cd deploy/provision/terraform
cp terraform.tfvars.example terraform.tfvars   # fill in token + SSH public key
terraform init
terraform apply
```

This creates a **cx33** server (4 vCPU, 8 GB RAM, Debian 12) in `hel1` (Helsinki, cheapest region), a firewall, and uploads your SSH key. Takes ~30 seconds.

```bash
# Capture the IP for the next step
SERVER_IP=$(terraform output -raw server_ip)
```

### 2 — Configure with Ansible

```bash
cd deploy/provision/ansible
sed "s/SERVER_IP/$SERVER_IP/" inventory.ini.tpl > inventory.ini
ansible-playbook -i inventory.ini playbook.yml \
  --private-key ~/.ssh/your_key \
  --extra-vars "gh_pat=<your-GH_PAT>"
```

Ansible installs Docker CE, adds the `deploy` user to the `docker` group, configures log rotation, and clones the repo (including private submodules) to `/opt/dexArbTrading`. The `gh_pat` extra-var is required for the submodule clone to authenticate.

### 3 — Wire up CI/CD

After Ansible completes, add or update these GitHub repository secrets (see [GitHub Actions Wiring](#github-actions-wiring) below):

| Secret | Value |
|--------|-------|
| `VPS_SSH_HOST` | value of `$SERVER_IP` |
| `VPS_SSH_USER` | `deploy` |
| `VPS_SSH_PRIVATE_KEY` | private half of the keypair you used above |
| `VPS_SSH_KNOWN_HOSTS` | output of `ssh-keyscan $SERVER_IP` (all key types) |
| `VPS_REPO_DIR` | `/opt/dexArbTrading` |

> **Note:** `VPS_SSH_KNOWN_HOSTS` must include all three host key types (RSA, ECDSA, ED25519) from `ssh-keyscan`. A single-type scan will cause "Host key verification failed" in CI because OpenSSH negotiates ED25519 by default.

The next push to `main` will deploy the full compose stack automatically.

---

This folder contains secrets bootstrap scripts for the CI/CD pipeline.

## What It Does

Tests and image builds run in GitHub Actions ([`.github/workflows/ci-cd.yml`](../.github/workflows/ci-cd.yml)). The VPS only pulls pre-built images from GHCR and restarts the stack:

1. The CI/CD workflow finishes a successful build and pushes images to `ghcr.io/<owner>/dexarbtrading-<service>:latest` (and `:<sha>`).
2. The `pre-deploy` job verifies every image SHA tag is present in GHCR, then SSHes into the VPS to confirm docker, compose, disk, and memory are all healthy.
3. The `deploy` job SCPs `docker-compose.yml` to the VPS and runs `docker compose pull` + `docker compose up -d --remove-orphans` over SSH.
4. Post-deploy smoke tests hit `http://<vps>:5010/health` and `http://<vps>:5000/` from the runner to confirm the stack is reachable.

This keeps the VPS thin: no builds, no test runs — just an SSH endpoint for docker compose commands.

To pull private GHCR images, run `docker login ghcr.io` once as the deploy user on the VPS using a read-only PAT (classic token with `read:packages` scope).

## GitHub Actions Wiring

The `pre-deploy` and `deploy` jobs in [`.github/workflows/ci-cd.yml`](../.github/workflows/ci-cd.yml) need these repository secrets:

- `VPS_SSH_HOST`: VPS hostname or IP.
- `VPS_SSH_USER`: the deploy user on the VPS.
- `VPS_SSH_PRIVATE_KEY`: full ed25519 private key for the deploy user. Generate a dedicated keypair (`ssh-keygen -t ed25519 -f deploy_key -N ''`), put the public half in `/home/<user>/.ssh/authorized_keys` on the VPS, and store the private half here.
- `VPS_SSH_KNOWN_HOSTS`: output of `ssh-keyscan -t ed25519 <vps-host>` from a trusted machine. Prevents TOFU prompts and MITM during deploy.
- `VPS_REPO_DIR`: absolute path to the repo checkout on the VPS (e.g. `/opt/dexArbTrading`).

## Secrets Management

Plaintext `.env` files are gitignored everywhere. Instead, each repo carries an encrypted `.env.enc` that is committed and safe to push. The private decryption keys live only in your local `.env.keys` files and in GitHub repository secrets. The CI/CD pipeline decrypts at deploy time and uploads the plaintext only to the VPS over SSH.

### Local setup — lefthook + dotenvx

Every developer machine that commits to this monorepo (or any submodule) needs two CLI tools installed and the git hooks wired up.

**Install tools** (one-time per machine):

```bash
# dotenvx — encrypts/decrypts .env files
curl -sfS "https://dotenvx.sh?directory=$HOME/.local/bin" | sh

# lefthook — git hook manager
curl -sL "https://github.com/evilmartians/lefthook/releases/latest/download/lefthook_Linux_x86_64" \
  -o "$HOME/.local/bin/lefthook" && chmod +x "$HOME/.local/bin/lefthook"
```

**Install hooks** — run once in each repo after cloning:

```bash
# Parent repo
lefthook install

# Each submodule
for repo in dexFiatController dexMmController dexMonitor dexUnifiedApi dexMmTradingLogicHummingBot; do
  (cd "$repo" && lefthook install)
done
```

**What the hook does on every commit:**

The `post-commit` hook defined in each repo's [`lefthook.yml`](../lefthook.yml) runs `.lefthook/post-commit/encrypt-env.sh` automatically after every commit. It:

1. Skips if the commit message starts with `.env values update` (loop guard).
2. Skips if no `.env` file exists in the repo root.
3. Copies `.env` → `.env.enc` and encrypts the copy in-place with `dotenvx encrypt -f .env.enc`.
4. Stages `.env.enc` and creates a follow-up commit: `.env values update <reponame>`.

The original `.env` is never modified. The private key is written to `.env.keys` (gitignored). Only `.env.enc` is committed.

**Branch-protection guards (also installed by `lefthook install`):**

The same [`lefthook.yml`](../lefthook.yml) wires two guards that keep production safe on dev machines:

- `pre-push` → `.lefthook/pre-push/protect-main.sh` rejects any push whose target ref is `refs/heads/main`.
- `pre-merge-commit` → `.lefthook/pre-merge-commit/protect-main.sh` rejects a local merge made while on `main`.

These are convenience guards (bypassable with `--no-verify`). On this Free private repo they are the protection for `main`; non-bypassable server-side GitHub branch protection (PR + green checks) would require GitHub Pro or making the repo public. All work flows `feature/* → develop → main`; `develop` is the default branch and only a reviewed `develop → main` PR updates production. See the README's *Branching & environments* section.

### Automated secrets upload

Instead of copying keys manually, run the appropriate bootstrap script from the repo root:

```bash
# GitHub
./setup-github-secrets.sh

# Gitea
./setup-gitea-secrets.sh
```

Both scripts read `GH_PAT` from `.env` for authentication and derive the repo slug from `git remote get-url origin`. The GitHub script installs `gh` CLI automatically if missing (Debian/Ubuntu only — install it manually for other distros). The Gitea script uses plain `curl` with no extra dependencies.

### Extracting private keys for GitHub secrets

After the first encryption in each repo, the private key is stored in `.env.keys`. You need to add these to GitHub repository secrets so CI can decrypt at deploy time.

The key name dotenvx uses for a file named `.env.enc` is `DOTENV_PRIVATE_KEY_ENC`. Extract it per repo:

```bash
# Root repo
grep DOTENV_PRIVATE_KEY_ENC .env.keys

# Submodules
grep DOTENV_PRIVATE_KEY_ENC dexUnifiedApi/.env.keys
grep DOTENV_PRIVATE_KEY_ENC dexMonitor/.env.keys
grep DOTENV_PRIVATE_KEY_ENC dexMmController/.env.keys
grep DOTENV_PRIVATE_KEY_ENC dexFiatController/.env.keys
```

Add each value to **Settings → Secrets and variables → Actions** in the GitHub repo:

| Secret name | Source |
|---|---|
| `DOTENV_PRIVATE_KEY_ROOT` | root `.env.keys` |
| `DOTENV_PRIVATE_KEY_UNIFIED_API` | `dexUnifiedApi/.env.keys` |
| `DOTENV_PRIVATE_KEY_MONITOR` | `dexMonitor/.env.keys` |
| `DOTENV_PRIVATE_KEY_MM_CONTROLLER` | `dexMmController/.env.keys` |
| `DOTENV_PRIVATE_KEY_FIAT_CONTROLLER` | `dexFiatController/.env.keys` |

Keep `.env.keys` backed up securely (password manager, encrypted vault). If a key is lost the `.env.enc` cannot be decrypted and secrets must be rotated.

### Rotating keys

To generate a fresh keypair and re-encrypt a file:

```bash
# Root repo
dotenvx rotate -f .env.enc

# Submodules
dotenvx rotate -f dexUnifiedApi/.env.enc
dotenvx rotate -f dexMonitor/.env.enc
dotenvx rotate -f dexMmController/.env.enc
dotenvx rotate -f dexFiatController/.env.enc
```

Each `rotate` call:
1. Generates a new keypair.
2. Re-encrypts the file in-place with the new public key.
3. Prints the new private key.

After rotating, update the corresponding GitHub secret with the newly printed private key and commit the changed `.env.enc` files. The old private key is immediately invalid.

To inspect the current keypair for a file without rotating it:

```bash
dotenvx keypair -f .env.enc
```

### GitHub Actions submodule access

The CI checkout steps that clone submodules require a GitHub Personal Access Token (classic with `repo` scope, or a fine-grained token with `Contents: read` for each submodule repo). Add it as a repository secret:

| Secret name | Value |
|---|---|
| `GH_PAT` | PAT with read access to all submodule repositories |

The token is passed to `actions/checkout` via `token: ${{ secrets.GH_PAT }}` in the `test-go`, `test-frontend`, and `build-and-push` jobs.

### CI/CD decryption flow

The `deploy` job in [`.github/workflows/ci-cd.yml`](../.github/workflows/ci-cd.yml) handles secrets delivery to the VPS without ever storing plaintext in the repo or in CI logs:

1. **Verify** — checks that all five `.env.enc` files are present in the checkout. If any is missing the job fails with a hint to run the encryption locally.
2. **Install dotenvx** on the runner (`curl -sfS https://dotenvx.sh | sh`).
3. **Decrypt** — each `.env.enc` is decrypted in-place using its corresponding private key injected as an environment variable:
   ```
   DOTENV_PRIVATE_KEY_ENC=<key>  dotenvx decrypt -f <path>/.env.enc
   ```
   After this step the files on the runner contain plaintext values.
4. **Upload** — each decrypted file is SCPed to the VPS as `.env` in the correct location under `$REPO_DIR`.
5. **Permissions** — a final SSH command sets `chmod 600` on every uploaded `.env`.

The decrypted files exist only in the runner's ephemeral workspace and are never logged, cached, or uploaded as artifacts.

### Manual env upload (without CI)

If CI is unavailable, decrypt the `.env.enc` files locally and SCP them manually:

```bash
# Decrypt (requires .env.keys in each directory)
dotenvx decrypt -f .env.enc
dotenvx decrypt -f dexUnifiedApi/.env.enc
dotenvx decrypt -f dexMonitor/.env.enc
dotenvx decrypt -f dexMmController/.env.enc
dotenvx decrypt -f dexFiatController/.env.enc

# Upload
scp .env.enc                  deploy@<vps-host>:/opt/dexArbTrading/.env
scp dexUnifiedApi/.env.enc    deploy@<vps-host>:/opt/dexArbTrading/dexUnifiedApi/.env
scp dexMonitor/.env.enc       deploy@<vps-host>:/opt/dexArbTrading/dexMonitor/.env
scp dexMmController/.env.enc  deploy@<vps-host>:/opt/dexArbTrading/dexMmController/.env
scp dexFiatController/.env.enc deploy@<vps-host>:/opt/dexArbTrading/dexFiatController/.env

ssh deploy@<vps-host> "chmod 600 /opt/dexArbTrading/.env /opt/dexArbTrading/{dexUnifiedApi,dexMonitor,dexMmController,dexFiatController}/.env"
```

## One-time VPS Setup

```bash
# As root on the VPS
useradd -m -s /bin/bash deploy
mkdir -p /home/deploy/.ssh
cp deploy_key.pub /home/deploy/.ssh/authorized_keys
chown -R deploy:deploy /home/deploy/.ssh
chmod 700 /home/deploy/.ssh
chmod 600 /home/deploy/.ssh/authorized_keys

# Install docker + compose plugin (adjust for your distro)
apt-get install -y docker.io docker-compose-plugin
usermod -aG docker deploy

# Clone the repo
git clone https://github.com/<owner>/dexArbTrading.git /opt/dexArbTrading

# Authenticate for GHCR pulls (one-time, using a PAT with read:packages)
sudo -u deploy docker login ghcr.io
```

In the GitHub repo settings, add the five secrets listed above. The next push to `main` will deploy — CI will decrypt and upload the `.env` files automatically.
