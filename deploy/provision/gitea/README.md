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

`setup-gitea.sh` needs `curl`, `jq`, and `git` on the machine you run it from:

```
sudo apt install -y curl jq git
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
| `HOMESERVER_SSH_USER` | SSH user on the homeserver (e.g. `kili`). |
| `HOMESERVER_SSH_HOST` | Tailscale IP of the homeserver (e.g. `100.x.y.z`). Use the IP, not a `*.ts.net` hostname. |
| `HOMESERVER_RUNNER_DIR` | Directory on the homeserver where runner files are placed (default `~/gitea-runner`). |

Also reused: `GH_PAT` in the repo-root `.env` (the GitHub PAT) — written as a Gitea
secret so the runner can fetch submodules from github.com.

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

### Step 2 — ensure the 6 repos exist

Checks `GET /repos/{owner}/{name}` for each repo. If a repo does not exist yet (HTTP 404)
it creates it as **private + empty** (`auto_init: false`). The 6 repos are:

| Repo | What it is |
|---|---|
| `dexArbTrading` | Root repo (the one you're running this from) |
| `dexUnifiedApi` | Submodule |
| `dexMmTradingLogicHummingBot` | Submodule |
| `dexMmController` | Submodule |
| `dexMonitor` | Submodule |
| `dexFiatController` | Submodule |

### Step 3 — push local clones

For each repo it adds (or updates) an additive `gitea` remote — **`origin` (GitHub) is never
touched**. Then pushes all branches + tags. For submodules that are in detached HEAD state
(normal for git submodules) it seeds a `main` branch. The Gitea PAT is injected into the
push URL in memory only and is never written to `.git/config`.

After the push it sets the root repo's default branch to `develop` so that the Gitea UI and
`workflow_dispatch` target the trial branch.

### Step 4 — enable Actions on each repo

Sends `PATCH /repos/{owner}/{name}` with `{"has_actions": true}` for all 6 repos. Without
this, the Gitea Actions tab does not appear and workflows do not run.

### Step 5 — transfer secrets + write daemon.json

Runs [`deploy/provision/secrets/setup-gitea-secrets.sh`](../secrets/setup-gitea-secrets.sh),
which writes **14 Actions secrets** to the root repo (`dexArbTrading`). These mirror the
secrets the CI workflow needs:

| Secret | Source |
|---|---|
| `GH_PAT` | Root `.env` — GitHub PAT so the runner can fetch submodules from github.com |
| `GITEA_REGISTRY_URL` | `.env.gitea` `GITEA_URL` |
| `GITEA_REGISTRY_USER` | Detected from the authenticated Gitea login |
| `GITEA_REGISTRY_TOKEN` | `.env.gitea` `GITEA_TOKEN` |
| `DOTENV_PRIVATE_KEY_*`, `VPS_*`, `KNOWN_HOSTS` | Sourced from `deploy/provision/.env.vps` and `.env.keys` |

Also writes `daemon.json` (marks the Gitea registry as an insecure registry for the DinD
Docker daemon, needed when `GITEA_URL` is HTTP or uses a self-signed cert).

### Step 6 — write act-runner.env (runner registration token)

Fetches a one-time runner registration token from the Gitea API and writes it — along with
the instance URL, runner name, and labels — to `act-runner.env` (mode 600, gitignored).
This file is what the runner container reads on first boot to register itself with Gitea.

### Step 7 — deploy and start the runner on the homeserver

SSHes into `HOMESERVER_SSH_USER@HOMESERVER_SSH_HOST`, creates `HOMESERVER_RUNNER_DIR`, SCPs
the three runner files (`act-runner-compose.yml`, `act-runner.env`, `daemon.json`), then
runs `docker compose -f act-runner-compose.yml up -d` remotely.

It runs Docker-in-Docker, so every job image/layer/cache lives in a throwaway volume.
In Gitea → repo Settings → Actions → Runners the runner shows `online` with the
`ubuntu-latest` label after a few seconds.

## Trigger the trial

```bash
git push gitea develop          # or use "Run workflow" (workflow_dispatch) in the Gitea UI
```

The `ci` workflow runs `test-go` + `test-frontend`, then builds and pushes all 10 service
images to the Gitea registry. Browse them in Gitea under the owner's **Packages** tab
(`<gitea-host>/<owner>/-/packages`). GitHub is unaffected (a push to `origin` still
triggers the normal GitHub pipeline). First run is cold (no build cache) — the heavy
tor/bisq/bisq2 images take a while and use real disk in the DinD volume; `teardown.sh`
reclaims it all.

## Tear down — leaves no artifacts

SSH into the homeserver and run teardown from the runner directory:

```bash
ssh user@100.x.y.z "cd ~/gitea-runner && bash teardown.sh"
```

This unregisters the runner from Gitea, runs `docker compose down -v` (removing the
runner, the DinD storage with all pulled job images/cache, and the registration volume),
removes the two runner images, and deletes `act-runner.env`. Verify:

```bash
docker ps -a | grep gitea-      # empty
docker volume ls | grep gitea   # empty
```

The homeserver is back to its prior state (Docker itself aside). The images you pushed
live in Gitea's registry (not on the homeserver) — delete them from the owner's
**Packages** tab if you want them gone. To fully undo the trial, you can also delete the
6 repos in the Gitea UI; GitHub was never touched.
