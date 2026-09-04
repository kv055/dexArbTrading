# Gitea trial-run CI

Trial of a self-hosted **Gitea** instance (home server, over Tailscale) as an
alternative CI host — **without touching GitHub**. GitHub Actions keeps working
unchanged; you can switch back at any time.

## Why this is non-destructive

- **GitHub is untouched.** Nothing under `.github/` is modified. The trial workflow
  lives in [`.gitea/workflows/ci.yml`](../../../.gitea/workflows/ci.yml). GitHub never
  reads `.gitea/`; Gitea, once a `.gitea/workflows/` file exists, uses it and ignores
  `.github/workflows/` ([gitea#31456](https://github.com/go-gitea/gitea/issues/31456)).
  So GitHub runs its full pipeline, Gitea runs only the trial.
- **`origin` / `.gitmodules` / `docker-compose.yml` untouched.** The push to Gitea uses
  an additive remote named `gitea`. Submodules still resolve from github.com (the runner
  fetches them using the `GH_PAT` secret).
- **Images go to the Gitea registry, not GHCR.** `ci.yml` runs `test-go` + `test-frontend`
  then builds all 10 service images and pushes them to the **Gitea container registry**
  (`<gitea-host>/<owner>/dexarbtrading-*`). It never logs in to or pushes to GHCR. The
  production `docker-compose.yml` and the VPS keep pulling from GHCR, so production is
  unaffected — the Gitea registry is a parallel store for the trial.
- **No `gh` calls, no deploy.** The trial cannot reach or change the production VPS.

## Prerequisites (on the homeserver, one time)

1. Gitea `app.ini` has Actions enabled, then restart Gitea:
   ```ini
   [actions]
   ENABLED = true
   ```
2. Docker installed (for the runner).
3. Create a **Gitea** personal access token (Settings → Applications) with
   `write:repository`, `write:package` (push images to the registry),
   `write:organization` (only if the owner is an org), and repo-admin / `write:admin`
   (needed to mint a runner registration token).

## Local tooling

`setup-gitea.sh` needs `curl`, `jq`, `git`, and the OpenSSH client on the machine
you run it from:

```
sudo apt install -y curl jq git openssh-client
```

## What you provide

Copy the template and fill it in (gitignored):

```
cp deploy/provision/gitea/.env.gitea.example deploy/provision/gitea/.env.gitea
```

| Variable | Meaning |
|---|---|
| `GITEA_URL` | Gitea base URL over Tailscale, no trailing slash, no `/api`. Prefer the Tailscale **IP** (`http://100.x.y.z:3000`) — MagicDNS `*.ts.net` names often don't resolve inside the runner container. |
| `GITEA_OWNER` | User/org that will own the repos (e.g. `kv055`). |
| `GITEA_TOKEN` | The **Gitea** PAT from step 3 above (NOT the GitHub PAT). |
| `RUNNER_NAME` / `RUNNER_LABELS` | Runner identity + the `ubuntu-latest → catthehacker` image mapping. |
| `HOMESERVER_ADMIN_SSH_USER` | Sudo-capable account used by `provision-runner-user.sh` for docker group + lingering (default `umbrel`). On Umbrel OS this is the same as `HOMESERVER_SSH_USER`. |
| `HOMESERVER_SSH_USER` | Account that owns runner files and that `setup-gitea.sh` logs in as (default `umbrel`). Must be an OS-image user on Umbrel; overlay-only users vanish after a Rugix overlay rebuild. |
| `HOMESERVER_SSH_HOST` | Tailscale IP of the homeserver (e.g. `100.x.y.z`). Use the IP, not a `*.ts.net` hostname. |
| `HOMESERVER_RUNNER_DIR` | Directory on the homeserver where runner files are placed (default `~/gitea-runner`, i.e. `/home/umbrel/gitea-runner`). |

Also reused: `GH_PAT` in the repo-root `.env` (the GitHub PAT) — written as a Gitea
secret so the runner can fetch submodules from github.com.

## Bootstrap the runner owner

`setup-gitea.sh` logs in to the homeserver **as** `HOMESERVER_SSH_USER` (`umbrel`
on Umbrel OS) to copy the runner files and `docker compose up`. That account
must be in the `docker` group and have systemd lingering. Run this before the
first `setup-gitea.sh` and again after an Umbrel overlay rebuild (unclean reboot):

```bash
bash deploy/provision/gitea/provision-runner-user.sh
```

Over a sudo SSH session as `HOMESERVER_ADMIN_SSH_USER` (same as `umbrel` by
default) it:

1. uses the existing OS-image user (does not create a dedicated overlay user);
2. adds it to the `docker` group so it can run `docker compose`;
3. enables systemd lingering so the reconciliation timer starts at boot;
4. if `/home/gitea-runner/gitea-runner` still exists from the old overlay
   account, copies it to `~/gitea-runner` on the stock user (same compose
   project name, so named volumes keep working).

Verify (should not prompt for a password, and docker must work):

```bash
ssh umbrel@100.x.y.z docker ps
```

## Run it

```bash
# From the repo root, from anywhere that can reach Gitea over Tailscale:
bash deploy/provision/gitea/setup-gitea.sh
```

`setup-gitea.sh` is idempotent (safe to re-run) and does the following in order:

### Step 1 — validate token + detect owner type

Calls `GET /api/v1/user` to confirm the token works and to get the authenticated username.
Then checks whether `GITEA_OWNER` is an **org** or a **personal user** (the repo-creation
API endpoint differs between the two).

### Step 2 — ensure the repos exist

Checks `GET /repos/{owner}/{name}` for each repo. If a repo does not exist yet (HTTP 404)
it creates it as **private + empty** (`auto_init: false`). The repo list is **not hardcoded
here** — it comes from the shared manifest
[`deploy/provision/manifest.sh`](../manifest.sh) (`REPOS` array + `ROOT_REPO`), which is the
single source of truth shared with the secrets scripts. By default:

| Repo | What it is |
|---|---|
| `dexArbTrading` | Root repo (the one you're running this from) |
| `dexUnifiedApi` | Submodule |
| `dexMmController` | Submodule |
| `dexMonitor` | Submodule |
| `dexFiatController` | Submodule |

To add or remove a repo, edit the `REPOS` array in the manifest — nothing else needs to change.

### Step 3 — push local clones

For each repo it adds (or updates) an additive `gitea` remote — **`origin` (GitHub) is never
touched**. Then pushes all branches + tags. For submodules that are in detached HEAD state
(normal for git submodules) it seeds a `main` branch. The Gitea PAT is injected into the
push URL in memory only and is never written to `.git/config`.

After the push it sets the root repo's default branch to `develop` so that the Gitea UI and
`workflow_dispatch` target the trial branch.

### Step 4 — enable Actions on each repo

Sends `PATCH /repos/{owner}/{name}` with `{"has_actions": true}` for every manifest repo. Without
this, the Gitea Actions tab does not appear and workflows do not run.

### Step 5 — transfer secrets + write daemon.json

Runs [`deploy/provision/secrets/setup-gitea-secrets.sh`](../secrets/setup-gitea-secrets.sh),
which writes the Actions secrets to the root repo (`ROOT_REPO`). The secret set is declared
in the shared manifest [`deploy/provision/manifest.sh`](../manifest.sh) (the same list the
GitHub secrets script uses), plus 3 `REGISTRY_*` secrets computed here from the Gitea
connection. These mirror the secrets the CI workflow needs:

| Secret | Source |
|---|---|
| `DOTENV_PRIVATE_KEY_*` (one per repo) | Each repo's `<dir>/.env.keys` — from the manifest `REPOS` entries |
| `GH_PAT` | Root `.env` — GitHub PAT so the runner can fetch submodules from github.com (manifest `ENV_SECRETS`) |
| `VPS_*` | `deploy/provision/.env.vps` (manifest `VPS_SECRETS`) |
| `REGISTRY_URL` | `.env.gitea` `GITEA_URL` (or `GITEA_INTERNAL_URL`) — computed, not in the manifest |
| `REGISTRY_USER` | Detected from the authenticated Gitea login |
| `REGISTRY_TOKEN` | `.env.gitea` `GITEA_TOKEN` |

Also writes `daemon.json` (marks the Gitea registry as an insecure registry for the DinD
Docker daemon, needed when `GITEA_URL` is HTTP or uses a self-signed cert).

### Step 6 — write act-runner.env (runner registration token)

Fetches a one-time runner registration token from the Gitea API and writes it — along with
the instance URL, runner name, and labels — to `act-runner.env` (mode 600, gitignored).
This file is what the runner container reads on first boot to register itself with Gitea.

### Step 7 — deploy and start the runner on the homeserver

SSHes into `HOMESERVER_SSH_USER@HOMESERVER_SSH_HOST`, creates `HOMESERVER_RUNNER_DIR`, SCPs
the runner files and user-systemd units, installs/enables the reconciliation timer,
then invokes the same reconciler used by that timer.
Setup waits up to 120 seconds for Gitea to report exactly one online runner with every
configured label. It fails with remote status/log output otherwise and removes only idle,
offline registrations left behind under the same runner name.

It runs Docker-in-Docker, so every job image/layer/cache lives in a named disposable
volume. DinD and act-runner use pinned image digests and health checks; act-runner is not
started until the Docker daemon is ready.

**How jobs get Docker (for `docker buildx`):** the DinD daemon runs on plain TCP 2375 on
the internal compose network (no TLS — never published to the host/LAN). `runner-config.yaml`
sets `container.options` to bind-mount the DinD daemon's own `/var/run/docker.sock` into every
job container, so build steps talk to DinD (not the host) — builds stay inside DinD and are
wiped by teardown. That file also carries the runner log level and is deployed alongside the
compose.

## Automatic recovery

The runner is permanent homeserver infrastructure. Docker's `unless-stopped` policy
restarts existing containers, while `gitea-runner-reconcile.timer` also recreates
containers that have been removed. The timer runs 30 seconds after boot and then every
two minutes after each reconciliation finishes. It is a lingering user-systemd timer,
so the user's manager starts at boot even when nobody logs in.

The reconciler serializes itself with setup and teardown, waits for Compose health checks,
and writes state transitions to journald. Inspect it with:

```bash
ssh umbrel@100.x.y.z 'systemctl --user status gitea-runner-reconcile.timer'
ssh umbrel@100.x.y.z 'journalctl --user -u gitea-runner-reconcile.service'
```

It records a disk warning at 90% usage and a critical message at 95%, but never prunes
Docker data automatically. A normal `docker compose restart` is safe. To keep the runner
stopped for maintenance, suppress reconciliation first, then restore it afterwards:

```bash
touch .reconcile-disabled
docker compose -f act-runner-compose.yml stop
# ...maintenance...
rm .reconcile-disabled
./runner-reconcile.sh
```

Do not use `down` or delete runner volumes manually; use the guarded teardown below when
you intend to remove the runner and its cached data.

## Trigger the trial

```bash
git push gitea develop          # or use "Run workflow" (workflow_dispatch) in the Gitea UI
```

The `ci` workflow runs `test-go` + `test-frontend`, then builds and pushes all 10 service
images to the Gitea registry. Browse them in Gitea under the owner's **Packages** tab
(`<gitea-host>/<owner>/-/packages`). GitHub is unaffected (a push to `origin` still
triggers the normal GitHub pipeline). First run is cold (no build cache) — the heavy
tor/bisq/bisq2 images take a while and use real disk in the DinD volume; the guarded
teardown command below reclaims it all.

## Intentional teardown

Run teardown from the provisioning checkout, where the Gitea PAT is already available:

```bash
bash deploy/provision/gitea/teardown-gitea.sh
```

The command creates `.reconcile-disabled` before taking the reconciliation lock, so the
systemd timer cannot recreate containers during teardown. It then removes the runner,
DinD cache, registration volume, runner images, and `act-runner.env`, and unregisters
matching runner records through the Gitea API without copying the PAT to the homeserver.
Verify:

```bash
docker ps -a | grep gitea-      # empty
docker volume ls | grep gitea   # empty
```

The reconciliation timer remains installed but suppressed by the marker. Running
`setup-gitea.sh` removes the marker and restores the permanent runner. Images pushed to
Gitea's registry are independent; delete those from the owner's **Packages** tab only
when they are no longer required.
