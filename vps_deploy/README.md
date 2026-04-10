# VPS Deploy

This folder contains a lightweight VPS-side auto-deploy setup built around [`adnanh/webhook`](https://github.com/adnanh/webhook) and a guarded rebuild script.

## What It Does

The goal is simple:

1. GitHub sends a `push` webhook to your VPS.
2. `webhook` validates the request signature and checks that the push is for the configured branch and repository.
3. If the request matches, `webhook` runs [`redeploy.sh`](/home/kili/Dokumente/Demo_Projects/dexArbTrading/vps_deploy/redeploy.sh).
4. `redeploy.sh` pulls the latest git changes and rebuilds the Docker Compose stack.

This avoids a custom deploy app and keeps the VPS logic small and easy to inspect.

## Files

- [`install.sh`](/home/kili/Dokumente/Demo_Projects/dexArbTrading/vps_deploy/install.sh) is the main bootstrap script. It installs and configures the deploy setup on the VPS.
- [`redeploy.sh`](/home/kili/Dokumente/Demo_Projects/dexArbTrading/vps_deploy/redeploy.sh) performs the actual deploy.
- [`hooks.json.example`](/home/kili/Dokumente/Demo_Projects/dexArbTrading/vps_deploy/hooks.json.example) shows the generated `webhook` hook shape.
- [`dexarb-webhook.service`](/home/kili/Dokumente/Demo_Projects/dexArbTrading/vps_deploy/dexarb-webhook.service) is a static example of the `systemd` service layout.
- [`config.env.example`](/home/kili/Dokumente/Demo_Projects/dexArbTrading/vps_deploy/config.env.example) and [`config.yaml.example`](/home/kili/Dokumente/Demo_Projects/dexArbTrading/vps_deploy/config.yaml.example) show the supported config inputs for the installer.

## Install Logic

[`install.sh`](/home/kili/Dokumente/Demo_Projects/dexArbTrading/vps_deploy/install.sh) is meant to be idempotent.

When you run it, it does this:

1. Loads config from `vps_deploy/config.env` if present.
2. Otherwise loads `vps_deploy/config.yaml` if present.
3. Applies defaults for values you did not provide.
4. Validates that the repo checkout, compose file, and webhook secret exist.
5. Checks whether the `webhook` binary is already installed.
6. Installs `webhook` automatically if it is missing and the VPS is apt-based.
7. Makes [`redeploy.sh`](/home/kili/Dokumente/Demo_Projects/dexArbTrading/vps_deploy/redeploy.sh) executable if needed.
8. Renders `vps_deploy/hooks.json` from your config.
9. Renders `/etc/systemd/system/<service>.service` from your config.
10. Runs `systemctl daemon-reload`.
11. Enables the service if it is not already enabled.
12. Starts or restarts the service so the newest config is active.
13. Optionally opens the webhook port in `ufw` or `firewalld` if `OPEN_FIREWALL=true`.

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
- `WEBHOOK_PORT`: port used by `webhook`. Default is `9000`.
- `WEBHOOK_HOST`: listen address for `webhook`. Default is `0.0.0.0`.
- `WEBHOOK_BINARY`: path to the `webhook` binary. Default is `/usr/bin/webhook`.
- `HOOK_ID`: hook route id. Default is `dexarb-redeploy`.
- `SERVICE_NAME`: systemd service name. Default is `dexarb-webhook`.
- `SERVICE_USER`: Linux user that runs the service.
- `OPEN_FIREWALL`: set to `true` if the installer should try to open the port.

The generated webhook URL is:

`http://YOUR_VPS_IP:$WEBHOOK_PORT/hooks/$HOOK_ID`

## Hook Logic

The generated `hooks.json` requires all of these to match before a deploy starts:

- `X-GitHub-Event` must be `push`
- `X-Hub-Signature-256` must match your `WEBHOOK_SECRET`
- payload `ref` must equal `refs/heads/<BRANCH>`
- payload `repository.full_name` must match `REPO_FULL_NAME`

If any of those checks fail, the deploy command is not run.

## Redeploy Logic

[`redeploy.sh`](/home/kili/Dokumente/Demo_Projects/dexArbTrading/vps_deploy/redeploy.sh) is intentionally strict.

Before rebuilding containers, it:

- verifies `git`, `docker`, and `flock` exist
- acquires a lock file so two deploys cannot run at the same time
- verifies the repo path is really a git checkout
- verifies the compose file exists
- verifies the checked-out branch matches `BRANCH`
- refuses to deploy if the worktree is dirty

Then it:

- fetches from the configured remote
- fast-forward pulls the configured branch if needed
- runs `docker compose -f <compose-file> up --build -d --remove-orphans`

This means it is safe to rerun, and it also helps prevent accidentally deploying from a locally modified checkout.

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
cp /opt/dexArbTrading/vps_deploy/config.env.example /opt/dexArbTrading/vps_deploy/config.env
cd /opt/dexArbTrading
./vps_deploy/install.sh
```

Then in GitHub create a webhook with:

- Payload URL: `http://YOUR_VPS_IP:9000/hooks/dexarb-redeploy`
- Content type: `application/json`
- Secret: same value as `WEBHOOK_SECRET`
- Event: push only

If you changed `WEBHOOK_PORT` or `HOOK_ID`, use those values instead.

## Notes

- The current installer auto-installs `webhook` only on apt-based systems.
- The installer expects `systemd` to be available.
- The service user must already exist.
- `hooks.json`, `config.env`, and `config.yaml` are intentionally ignored by git because they may contain secrets or machine-specific paths.
