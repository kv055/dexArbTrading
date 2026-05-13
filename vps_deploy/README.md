# VPS Deploy

This folder contains the VPS-side deploy script that GitHub Actions invokes over SSH.

## What It Does

Tests and image builds run in GitHub Actions ([`.github/workflows/ci-cd.yml`](../.github/workflows/ci-cd.yml)). The VPS only pulls pre-built images from GHCR and restarts the stack:

1. The CI/CD workflow finishes a successful build and pushes images to `ghcr.io/<owner>/dexarbtrading-<service>:latest` (and `:<sha>`).
2. The `pre-deploy` job verifies every image SHA tag is present in GHCR, then SSHes into the VPS to confirm docker, compose, env files, disk, and memory are all healthy.
3. The `deploy` job SCPs `docker-compose.yml` to the VPS and runs [`redeploy.sh`](./redeploy.sh) over SSH.
4. `redeploy.sh` acquires a `flock`, runs `docker compose pull`, and restarts the stack with `docker compose up -d --remove-orphans`.
5. Post-deploy smoke tests hit `http://<vps>:5010/health` and `http://<vps>:5000/` from the runner to confirm the stack is reachable.

This keeps the VPS thin: no builds, no test runs — just an SSH endpoint that runs `redeploy.sh`.

## Files

- [`redeploy.sh`](./redeploy.sh) performs the actual deploy (pull + recreate). Invoked over SSH by the `deploy` job, and also usable manually by an operator.
- [`upload-envs.sh`](./upload-envs.sh) copies local `.env` files to the VPS when you want to refresh service configuration without committing secrets.

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

## Environment File Uploads

Runtime `.env` files are intentionally not committed. If you maintain them locally and want to push them to the VPS, use [`upload-envs.sh`](./upload-envs.sh).

The helper covers the repo's env layout:

- root `.env`
- `dexUnifiedApi/.env`
- `dexMonitor/.env`
- `dexMmController/.env`
- `dexFiatController/.env`

Review the script before running it on a new machine so the target host, target repo path, and SSH user match your VPS setup.

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
chmod +x /opt/dexArbTrading/vps_deploy/redeploy.sh

# Authenticate for GHCR pulls (one-time, using a PAT with read:packages)
sudo -u deploy docker login ghcr.io
```

From your dev machine:

```bash
./vps_deploy/upload-envs.sh deploy@<vps-host> /opt/dexArbTrading
```

In the GitHub repo settings, add the five secrets listed above. The next push to `main` will deploy.
