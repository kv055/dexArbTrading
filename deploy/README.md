# VPS Deploy

This folder contains the VPS-side deploy script that GitHub Actions invokes over SSH.

## What It Does

Tests and image builds run in GitHub Actions ([`.github/workflows/ci-cd.yml`](../.github/workflows/ci-cd.yml)). The VPS only pulls pre-built images from GHCR and restarts the stack:

1. The CI/CD workflow finishes a successful build and pushes images to `ghcr.io/<owner>/dexarbtrading-<service>:latest` (and `:<sha>`).
2. The `pre-deploy` job verifies every image SHA tag is present in GHCR, then SSHes into the VPS to confirm docker, compose, disk, and memory are all healthy.
3. The `deploy` job SCPs `docker-compose.yml` to the VPS and runs [`redeploy.sh`](./redeploy.sh) over SSH.
4. `redeploy.sh` acquires a `flock`, runs `docker compose pull`, and restarts the stack with `docker compose up -d --remove-orphans`.
5. Post-deploy smoke tests hit `http://<vps>:5010/health` and `http://<vps>:5000/` from the runner to confirm the stack is reachable.

This keeps the VPS thin: no builds, no test runs — just an SSH endpoint that runs `redeploy.sh`.

## Files

- [`redeploy.sh`](./redeploy.sh) performs the actual deploy (pull + recreate). Invoked over SSH by the `deploy` job, and also usable manually by an operator.

## Redeploy Logic

[`redeploy.sh`](./redeploy.sh) is intentionally minimal — it only pulls images and recreates containers. Everything else (tests, builds, image publishing, pre-flight validation) happens in GitHub Actions.

It:

- verifies `docker` and `flock` exist
- acquires a lock file so two deploys cannot run at the same time
- verifies the compose file exists
- runs `docker compose -f <compose-file> pull`
- runs `docker compose -f <compose-file> up -d --remove-orphans`

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
chmod +x /opt/dexArbTrading/deploy/redeploy.sh

# Authenticate for GHCR pulls (one-time, using a PAT with read:packages)
sudo -u deploy docker login ghcr.io
```

In the GitHub repo settings, add the five secrets listed above. The next push to `main` will deploy — CI will decrypt and upload the `.env` files automatically.
