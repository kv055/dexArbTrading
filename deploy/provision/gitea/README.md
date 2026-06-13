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

Also reused: `GH_PAT` in the repo-root `.env` (the GitHub PAT) — written as a Gitea
secret so the runner can fetch submodules from github.com.

## Run it

```bash
# From the repo root, from anywhere that can reach Gitea over Tailscale:
bash deploy/provision/gitea/setup-gitea.sh
```

`setup-gitea.sh` is idempotent and does, in order:

1. Validates the token and detects whether `GITEA_OWNER` is a user or org.
2. Ensures the 6 repos exist (root `dexArbTrading` + the 5 submodules), creating any
   missing ones as private/empty.
3. Pushes local clones to the additive `gitea` remote (root: all branches+tags;
   submodules: best-effort, since they're not used by the trial).
4. Enables Actions on each repo (`has_actions: true`).
5. Runs the secrets transfer ([`../secrets/setup-gitea-secrets.sh`](../secrets/setup-gitea-secrets.sh))
   — writes the 14 Actions secrets to the root repo (incl. the `GITEA_REGISTRY_*` login).
6. Fetches a runner registration token (`act-runner.env`, mode 600) and writes
   `daemon.json` (marks the Gitea registry insecure so the runner can push over HTTP).

## Start the runner (on the homeserver)

The runner must run where Docker lives. Get this `gitea/` directory (incl. the generated
`act-runner.env` **and** `daemon.json`) onto the homeserver, then:

```bash
docker compose -f act-runner-compose.yml up -d
```

It runs Docker-in-Docker, so every job image/layer/cache lives in a throwaway volume.
In Gitea → repo Settings → Actions → Runners the runner shows `online` with the
`ubuntu-latest` label.

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

On the homeserver:

```bash
bash teardown.sh
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
