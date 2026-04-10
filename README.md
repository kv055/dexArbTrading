# dexArbTrading

## VPS Auto-Deploy

This repo now includes a small webhook-based deploy flow in [`vps_deploy/`](/home/kili/Dokumente/Demo_Projects/dexArbTrading/vps_deploy):

- [`vps_deploy/install.sh`](/home/kili/Dokumente/Demo_Projects/dexArbTrading/vps_deploy/install.sh) bootstraps the VPS, installs `webhook` when needed, renders the hook and `systemd` files from config, and safely reuses existing setup when it is already present.
- [`vps_deploy/redeploy.sh`](/home/kili/Dokumente/Demo_Projects/dexArbTrading/vps_deploy/redeploy.sh) pulls the latest git changes and runs `docker compose up --build -d --remove-orphans`.
- [`vps_deploy/hooks.json.example`](/home/kili/Dokumente/Demo_Projects/dexArbTrading/vps_deploy/hooks.json.example) is an `adnanh/webhook` hook definition that validates the GitHub signature and only triggers on the `light_setting` branch.
- [`vps_deploy/dexarb-webhook.service`](/home/kili/Dokumente/Demo_Projects/dexArbTrading/vps_deploy/dexarb-webhook.service) is a `systemd` unit template for running `webhook` on your VPS.
- [`vps_deploy/config.env.example`](/home/kili/Dokumente/Demo_Projects/dexArbTrading/vps_deploy/config.env.example) and [`vps_deploy/config.yaml.example`](/home/kili/Dokumente/Demo_Projects/dexArbTrading/vps_deploy/config.yaml.example) show the supported config format.

### Setup

1. Clone this repo onto the VPS at the path you want to deploy from, for example `/opt/dexArbTrading`.
2. Create a config file from either example:

```bash
cp /opt/dexArbTrading/vps_deploy/config.env.example /opt/dexArbTrading/vps_deploy/config.env
```

You can use YAML instead if you prefer:

```bash
cp /opt/dexArbTrading/vps_deploy/config.yaml.example /opt/dexArbTrading/vps_deploy/config.yaml
```

3. Edit the config and set at least:

- `REPO_DIR`
- `REPO_FULL_NAME`
- `WEBHOOK_SECRET`
- `SERVICE_USER`
- `BRANCH` if you do not want `light_setting`

4. Run the installer:

```bash
cd /opt/dexArbTrading
./vps_deploy/install.sh
```

Or point it at a specific config file:

```bash
cd /opt/dexArbTrading
./vps_deploy/install.sh ./vps_deploy/config.yaml
```

5. In GitHub, add a repository webhook:

- Payload URL: `http://YOUR_VPS_IP:9000/hooks/dexarb-redeploy`
- Content type: `application/json`
- Secret: same value as `WEBHOOK_SECRET` in your config file
- Event type: `Just the push event`

### Notes

- The deploy script refuses to run if the VPS checkout has local uncommitted changes.
- Redeploys are serialized with a lock file so overlapping webhook calls do not launch multiple rebuilds at once.
- The installer is idempotent: it checks the existing VPS state and only installs or rewrites pieces that are missing or out of date.
- The installer currently auto-installs `webhook` only on apt-based systems. On other distros, install the binary yourself and point `WEBHOOK_BINARY` at it.
- Set `OPEN_FIREWALL=true` in the config if you want the installer to open the webhook port with `ufw` or `firewalld`.
