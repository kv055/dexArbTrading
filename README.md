<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/brand/wordmark-dark.svg">
  <img src="assets/brand/wordmark-light.svg" alt="dexArbPendulum — BTC DEX ↔ CEX Arbitrage Stack" height="110">
</picture>

`dexArbTrading` is a Docker Compose workspace for monitoring Bitcoin DEX/CEX spreads and coordinating the supporting services for arbitrage experiments.

The repository is organized as a parent deployment repo with several component repositories mounted as submodules:

- `dexUnifiedApi` owns the shared Postgres schema, live offer ingestion API, Bisq snapshot worker, payment notification receiver, and database backup sidecar.
- `dexMonitor` serves the React dashboard plus monitor-facing REST and WebSocket APIs.
- `dexMmController` tracks arbitrage transactions, wallet addresses, account health, premium offers, LND wallet access, and DEX offer execution.
- `dexFiatController` automates fiat account monitoring and payment notification forwarding for PayPal, Wise, and Revolut.
- `commons` is a shared Go module for price feeds, trade-cost calculation, hedge modelling, and Bitcoin fee analysis.
- `deploy` contains the VPS webhook deploy helper.

## Architecture

At runtime the stack centers on Postgres:

1. The `dexUnifiedApi` migration command bootstraps roles, applies schema migrations, and seeds payment methods before apps start.
2. The Bisq and snapshot containers gather live and historical offers.
3. `dexUnifiedApi` keeps live offer state in memory and exposes `/api/live_offers` plus `/ws/live_offers`.
4. `dexMonitor` reads shared tables, serves the dashboard, enriches offer data with trade-cost and premium calculations, and proxies selected MM controller data.
5. `dexMmController` consumes premium offer streams, records transactions, tracks account health, manages wallet-address state, and can take qualified DEX offers.
6. `dexFiatController` reads connector credentials from the shared database, monitors fiat services, and forwards payment events to `dexUnifiedApi`.
7. `postgres-backup` periodically creates compressed logical backups and uploads them with `rclone`.

External dependencies include Tor, Bisq, LND, optional OpenClaw services, CEX APIs, and fiat-provider web sessions.

## Repository Layout

```text
.
+-- docker-compose.yml              # Full local/VPS runtime stack
+-- .env.example                    # Compose-level Postgres and backup settings
+-- commons/                        # Shared Go module for cost, price, hedge, and BTC fee logic
+-- dexUnifiedApi/                  # Schema, live API, snapshot worker, backup helper
+-- dexMonitor/                     # Dashboard server and React frontend
+-- dexMmController/                # Transaction, wallet, account-health, and offer execution service
+-- dexFiatController/              # Fiat connector automation and TOTP helper service
+-- nginx/                          # Local reverse proxy for dashboard/API
+-- deploy/                     # Webhook-based deployment scripts
```

## Quick Start

Initialize submodules if this is a fresh checkout:

```bash
git submodule update --init --recursive
```

Create local env files from the examples:

```bash
cp .env.example .env
cp dexUnifiedApi/.env.example dexUnifiedApi/.env
cp dexMonitor/.env.example dexMonitor/.env
cp dexMmController/.env.example dexMmController/.env
cp dexFiatController/.env.example dexFiatController/.env
```

Fill in the generated files with real passwords, API keys, and wallet/account secrets. Then migrate and start the stack:

```bash
make staging-up
```

Main local URLs:

- Dashboard and monitor API: `http://localhost:5000`
- Unified API: `http://localhost:5010`
- MM controller API: `http://localhost:5001`

Useful health checks:

```bash
curl http://localhost:5000/health
curl http://localhost:5010/health
curl http://localhost:5001/health
```

## Configuration

The root `.env` is consumed by Docker Compose and should contain stack-wide Postgres passwords plus backup configuration. Each service also has its own `.env.example` for service-local settings.

Important root settings:

- `POSTGRES_DB`
- `POSTGRES_ADMIN_USER`
- `POSTGRES_ADMIN_PASSWORD`
- `POSTGRES_UNIFIED_API_PASSWORD`
- `POSTGRES_MONITOR_PASSWORD`
- `POSTGRES_MM_CONTROLLER_PASSWORD`
- `POSTGRES_FIAT_CONTROLLER_PASSWORD`
- `PG_BACKUP_*`
- `RCLONE_CONFIG_*`

The service-specific READMEs explain their own environment variables and runtime responsibilities.

## LND Neutrino sync

The Compose `lnd` service uses `--bitcoin.node=neutrino` (no local bitcoind). `lncli getinfo` `block_height` is the Neutrino **header** tip. `synced_to_chain` stays false until the wallet rescan (`LNWL`) catches up to that tip. Magma LSP orders stay disabled until `synced_to_chain` is true.

`num_peers` / `uris` on `getinfo` are **Lightning** peers, not Neutrino peers. Both stay empty until LND reaches `SERVER_ACTIVE`.

The `lnd` healthcheck only hits `/v1/state`. A healthy container is not a synced node.

Watch these log lines, in order, after an LND start:

1. `LNWL: Started rescan ...`
2. `LNWL: Rescanned through block ...` (when the rescan spans more than the start height)
3. `LNWL: Finished rescan for 1 address ...`
4. `LTND: Chain backend is fully synced ...`

BTCN `Processed 1 block` at tip does **not** mean the wallet rescan finished.

```bash
docker compose exec -T lnd lncli getinfo \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d["synced_to_chain"], d["block_height"])'
```

Restart discipline:

| Action | Effect |
|--------|--------|
| Rebuild `mm-controller` / `dexmonitor` only | Does not recreate `lnd` — preferred for app changes. |
| `docker compose stop` / `down` (no `-v`) | Volume kept. The next LND start re-runs a near-tip wallet rescan (often 1–4 hours). |
| Recreate `lnd` (`up -d lnd` after a compose/image change) | Restarts that rescan cycle. Avoid unless LND config or image changed. |
| `docker compose down -v` | Deletes `dexarbtrading_lnd-data` and forces a full Neutrino resync. Never use this to “unstick” sync. |

After changing LND flags, run `docker compose up -d lnd` only and wait for `Chain backend is fully synced` before expecting Magma to enable. Do not copy the `lnd` service `dns:` block onto other services — it replaces Docker DNS and those services need sibling hostnames.

## Database And Backups

`dexUnifiedApi` owns schema setup and application role creation. Deploys run `./server migrate` before app activation; the local staging targets, `dev-server.sh`, and the post-checkout rebuild hook do the same through `scripts/migrate-local-db.sh`. API and snapshot startup check migration history without applying schema changes. Role/password reconciliation remains at startup, and a rollback after a password-changing migrate step restarts `unified-api` to restore the previous credentials (see [deploy/README.md](deploy/README.md)). The Compose stack starts a `postgres-backup` sidecar that runs `pg_dump -Fc`, validates the archive with `pg_restore --list`, keeps local files in the `postgres-backups` volume, and optionally uploads with `rclone`.

By default, backup upload targets Proton Drive:

```env
PG_BACKUP_ENABLED=true
PG_BACKUP_UPLOAD=true
PG_BACKUP_UPLOAD_REQUIRED=true
PG_BACKUP_RCLONE_REMOTE=proton:postgres-backups/dexarb
```

Dropbox can be used by switching `PG_BACKUP_RCLONE_REMOTE` and providing the Dropbox rclone token. See `dexUnifiedApi/README.md` for detailed backup commands.

## Deployment

`main` is the protected production branch — it is never pushed to directly. Changes reach
prod by promoting `develop` → `main` via a reviewed PR (see [Branching & environments](#branching--environments)).
Each merge to `main` triggers the fully automated VPS deploy. Tests already ran on
`develop`; the promotion skips Go, frontend, OpenAPI, cert, and Bisq-patch jobs and:

1. **Build & push** — Docker images for all services are built and pushed to GHCR tagged with `:latest` and `:<sha>`.
2. **Pre-deploy checks** — the runner SSHes into the VPS to verify docker, compose, disk space, and memory are healthy.
3. **Decrypt & upload secrets** — each `.env.enc` (committed, encrypted) is decrypted on the CI runner using private keys from GitHub Secrets, then SCPed to the VPS as a plain `.env` file.
4. **Deploy** — `docker-compose.yml` is pushed to the VPS and `deploy/redeploy.sh` is invoked over SSH, which pulls new images and restarts the stack.
5. **Smoke tests** — the runner hits `/health` endpoints to confirm the stack came up.

See `deploy/README.md` for required GitHub Secrets, the one-time VPS setup, and the dotenvx secrets management workflow.

## Branching & environments

| Branch | Environment | How it updates |
|--------|-------------|----------------|
| `feature/*` | local | your working tree; open a PR into `develop` |
| `develop` | **staging (local)** — default branch, where all dev work lands | merge feature PRs; CI runs tests + build-only validation |
| `main` | **production** (VPS) | promoted from `develop` via PR only; merge auto-deploys |

Rules of the road:

- **Never commit or merge to `main` locally.** Lefthook installs `pre-commit`, `pre-push`, and
  `pre-merge-commit` guards that reject it. Run `lefthook install` once per clone to activate
  them (see `deploy/README.md`). These are the protection on this Free private repo and are
  bypassable with `--no-verify`; server-side GitHub branch protection (PR + green checks,
  non-bypassable) would require GitHub Pro or a public repo.
- Day-to-day: branch off `develop` → open a PR into `develop` → CI validates → squash-merge.
- Ship to prod: open a PR `develop` → `main`; the merge rebuilds GHCR images and deploys (tests are not re-run).
- The automated Bisq version bump follows the same path — it opens a PR into `develop` (not
  `main`); review and merge it once CI is green.
- Branches are cleaned up automatically: GitHub deletes a PR's head branch on merge, a weekly
  *Branch cleanup* workflow sweeps any remaining branches already merged into `develop`/`main`,
  and `make prune` removes locally-merged branches on your machine.

### Local staging

`develop` is validated locally before any PR. A `Makefile` builds the **full stack from your
local source** (never pulling prod images):

```bash
make staging-env-check   # confirm decrypted .env files are present
make staging-wait        # build + start everything, block until healthy
make staging-ps          # status
make staging-logs        # tail logs
make staging-down        # tear down (named volumes are kept)
make docker-reclaim      # free build cache / stopped containers / anonymous volumes
```

`staging-down` keeps named volumes, images, and BuildKit cache on purpose so the
next bring-up stays fast. Use `make docker-reclaim` when local Docker disk usage
grows (safe: does not delete `dexarbtrading_*` named volumes).

The dashboard is then at `http://localhost:5000/` and the unified API at
`http://localhost:5010/health`.

## Development

Run Go tests from the relevant module:

```bash
go test ./...
```

For the React dashboard:

```bash
cd dexMonitor/react-frontend
npm install
npm run dev
```

For full-stack verification, prefer Compose because several services expect the shared network, Postgres roles, and generated runtime credentials.

## Security Notes

- Do not commit `.env` files or files under service `secrets/` directories.
- Fiat account passwords, TOTP secrets, passkeys, API keys, and Bisq seeds should be stored through the shared account/secret storage flow rather than hard-coded in service env files.
- The MM controller restores LND wallets from their 24-word aezeed mnemonic and the latest static channel backup.
- The VPS deploy script intentionally refuses dirty worktrees before rebuilding containers.
