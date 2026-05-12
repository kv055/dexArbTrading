# VPS Deploy

This folder contains a lightweight VPS-side auto-deploy setup built around [`adnanh/webhook`](https://github.com/adnanh/webhook) and a guarded pull script.

## What It Does

Tests and image builds run in GitHub Actions ([`.github/workflows/ci-cd.yml`](../.github/workflows/ci-cd.yml)). The VPS only pulls pre-built images from GHCR and restarts the stack:

1. The CI/CD workflow finishes a successful build and pushes images to `ghcr.io/<owner>/dexarbtrading-<service>:latest` (and `:<sha>`).
2. The workflow's final `notify-vps` job POSTs a signed payload to the VPS webhook.
3. `webhook` validates the request signature and checks that the event and repository match the generated hook rules.
4. If the request matches, `webhook` runs [`webhook-handler.sh`](./webhook-handler.sh).
5. On GitHub `ping`, the handler records a local confirmation marker for verification.
6. On a matching `push`, the handler runs [`redeploy.sh`](./redeploy.sh).
7. `redeploy.sh` fast-forwards the repo (to pick up `docker-compose.yml` changes), runs `docker compose pull`, and restarts the stack with `docker compose up -d`.

This keeps the VPS thin: no builds, no test runs — just pull and restart.

## Files

- [`install.sh`](./install.sh) is the main bootstrap script. It installs and configures the deploy setup on the VPS.
- [`webhook-handler.sh`](./webhook-handler.sh) is the webhook entrypoint. It records GitHub `ping` deliveries and forwards matching `push` events to deploy logic.
- [`verify-webhook.sh`](./verify-webhook.sh) waits for GitHub's setup `ping` so you can confirm the webhook path works end to end.
- [`redeploy.sh`](./redeploy.sh) performs the actual deploy.
- [`upload-envs.sh`](./upload-envs.sh) copies local environment files to the VPS when you want to refresh service configuration without committing secrets.
- [`hooks.json.example`](./hooks.json.example) shows the generated `webhook` hook shape.
- [`dexarb-webhook.service`](./dexarb-webhook.service) is a static example of the `systemd` service layout.
- [`config.env.example`](./config.env.example) and [`config.yaml.example`](./config.yaml.example) show the supported config inputs for the installer.

## Install Logic

[`install.sh`](./install.sh) is meant to be idempotent.

When you run it, it does this:

1. Loads config from `vps_deploy/config.env` if present.
2. Otherwise loads `vps_deploy/config.yaml` if present.
3. Applies defaults for values you did not provide.
4. Validates that the repo checkout, compose file, and webhook secret exist.
5. Checks whether the `webhook` binary is already installed.
6. Installs `webhook` automatically if it is missing and the VPS is apt-based.
7. Makes [`redeploy.sh`](./redeploy.sh) executable if needed.
8. Makes [`webhook-handler.sh`](./webhook-handler.sh) and [`verify-webhook.sh`](./verify-webhook.sh) executable if needed.
9. Renders `vps_deploy/hooks.json` from your config.
10. Renders `/etc/systemd/system/<service>.service` from your config.
11. Runs `systemctl daemon-reload`.
12. Enables the service if it is not already enabled.
13. Starts or restarts the service so the newest config is active.
14. Optionally opens the webhook port in `ufw` or `firewalld` if `OPEN_FIREWALL=true`.
15. Verifies that something is listening on `WEBHOOK_PORT` and that a detected firewall allows `WEBHOOK_PORT/tcp`.
16. Launches [`verify-webhook.sh`](./verify-webhook.sh) in interactive shells so you can save the GitHub webhook and wait for the automatic `ping`.

If a file already exists with the exact expected contents, the installer leaves it alone and reports that it is already up to date.

## Config Logic

The installer supports two config formats:

- `.env`
- `.yaml`

Supported keys:

- `REPO_DIR`: absolute path to the repo checkout on the VPS.
- `REPO_FULL_NAME`: GitHub repo in `owner/name` format.
- `BRANCH`: branch to watch and deploy. Default is `light_setting`.
- `REMOTE`: git remote. Default is `origin`.
- `COMPOSE_FILE`: absolute path to `docker-compose.yml`.
- `WEBHOOK_SECRET`: GitHub webhook secret.
- `WEBHOOK_PORT`: port used by `webhook`. Default is `9000`. This is your installer default, not a GitHub default.
- `WEBHOOK_HOST`: listen address for `webhook`. Default is `0.0.0.0`.
- `WEBHOOK_BINARY`: path to the `webhook` binary. Default is `/usr/bin/webhook`.
- `HOOK_ID`: hook route id. Default is `dexarb-redeploy`.
- `SERVICE_NAME`: systemd service name. Default is `dexarb-webhook`.
- `SERVICE_USER`: Linux user that runs the service.
- `OPEN_FIREWALL`: set to `true` if the installer should try to open the port.
- `PING_STATUS_FILE`: local file used to record the most recent GitHub `ping`. Default is `/tmp/<service>-last-ping`.
- `WEBHOOK_VERIFY_TIMEOUT`: seconds [`verify-webhook.sh`](./verify-webhook.sh) waits for the GitHub `ping`. Default is `180`.

The generated webhook URL is:

`http://YOUR_VPS_IP:$WEBHOOK_PORT/hooks/$HOOK_ID`

## Hook Logic

The generated `hooks.json` requires all of these to match before the handler script runs:

- `X-Hub-Signature-256` must match your `WEBHOOK_SECRET`
- payload `repository.full_name` must match `REPO_FULL_NAME`
- `X-GitHub-Event` must be either `ping` or `push`

Then [`webhook-handler.sh`](./webhook-handler.sh) applies event-specific behavior:

- `ping`: records a confirmation file in `PING_STATUS_FILE`
- `push`: only deploys when payload `ref` equals `refs/heads/<BRANCH>`

If any of those checks fail, the deploy command is not run.

## Redeploy Logic

[`redeploy.sh`](./redeploy.sh) is intentionally minimal — it only pulls images and recreates containers. Everything else (tests, builds, image publishing) happens in GitHub Actions.

It:

- verifies `docker` and `flock` exist
- acquires a lock file so two deploys cannot run at the same time
- verifies the compose file exists
- runs `docker compose -f <compose-file> pull`
- runs `docker compose -f <compose-file> up -d --remove-orphans`

No git, no branch checks, no source tree on the VPS. The compose file and `.env` are maintained on the VPS by the operator (or shipped with [`upload-envs.sh`](./upload-envs.sh)).

To pull private GHCR images, run `docker login ghcr.io` once on the VPS using a read-only PAT (classic token with `read:packages` scope).

## Existing Setup Detection

The installer checks for existing setup in a few ways:

- If `webhook` is already installed at `WEBHOOK_BINARY`, it does not reinstall it.
- If `vps_deploy/hooks.json` already matches the expected rendered output, it does not rewrite it.
- If the `systemd` unit file already matches the expected rendered output, it does not rewrite it.
- If the service is already enabled, it does not re-enable it.
- If the service is already active, it restarts it only to pick up changes.

So you can safely rerun the installer after changing config, pulling repo updates, or replacing the server.

## Typical Setup

On the VPS:

```bash
cp vps_deploy/config.env.example vps_deploy/config.env
# Edit BRANCH to match the branch CI builds from (e.g. `main`).
cp .env.example .env
# Set IMAGE_OWNER to the lowercase GitHub owner that hosts the GHCR packages.
docker login ghcr.io   # one-time, using a PAT with read:packages
./vps_deploy/install.sh
```

In an interactive shell, the installer then launches [`verify-webhook.sh`](./verify-webhook.sh), prints the webhook details, and waits for the first signed `ping` (sent by GitHub when you save the webhook, or by the `notify-vps` job after a successful build).

Instead of a GitHub repository webhook, the trigger is now the **GitHub Actions `notify-vps` job**. Add these repository secrets so the workflow can call your VPS:

- `VPS_WEBHOOK_URL`: e.g. `http://YOUR_VPS_IP:9000/hooks/dexarb-redeploy`
- `VPS_WEBHOOK_SECRET`: same value as `WEBHOOK_SECRET` on the VPS

GitHub does not reserve or require port `9000` for webhooks. The Actions runner just `curl`s whatever URL you configure, so the port must match your VPS listener and firewall rules.

If you also want the legacy GitHub-side webhook for ad-hoc redeploys, you can still configure it in the repo settings (push event, same secret). The trigger rules accept either source.

## Environment File Uploads

Runtime `.env` files are intentionally not committed. If you maintain them locally and want to push them to the VPS, use [`upload-envs.sh`](./upload-envs.sh).

The helper is designed for the repo's current env layout:

- root `.env`
- `dexUnifiedApi/.env`
- `dexMonitor/.env`
- `dexMmController/.env`
- `dexFiatController/.env`

Review the script before running it on a new machine so the target host, target repo path, and SSH user match your VPS setup.

## Notes

- The current installer auto-installs `webhook` only on apt-based systems.
- The installer expects `systemd` to be available.
- The service user must already exist.
- `hooks.json`, `config.env`, and `config.yaml` are intentionally ignored by git because they may contain secrets or machine-specific paths.
