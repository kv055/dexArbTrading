# act — local workflow config testing

Use this when editing `.github/workflows/*.yml` and you want to verify the config change works before pushing. It is not a replacement for running `go test` or `npm test` directly.

> Has no relation to the main `docker-compose.yml` deploy stack.

## Prerequisites

- Docker running
- `GH_PAT` filled in your `.env` (see the `CI / act` section in `.env.example`)

## Usage

```bash
.github/act/run.sh [command]
```

| Command | What it runs |
|---------|-------------|
| `test-go` | Go tests job (PR event) |
| `test-frontend` | Frontend lint, build, e2e job (PR event) |
| `test` | All test jobs — skips build/deploy (PR event) |
| `bisq-bump` | Bisq version bump workflow (workflow_dispatch) |
| `ci` | Full CI simulating a push to main |
| `<job-id>` | Any specific job by its ID |

The act image is built on first run and cached by Docker.

## Limitations

| Job | Status | Notes |
|-----|--------|-------|
| `test-go` | ✅ Full | |
| `test-frontend` | ✅ Full | |
| `build-and-push` | ⚠️ Build only | Push to GHCR skipped without a valid `GITHUB_TOKEN` |
| `build-report` | ⚠️ Partial | `gh api` step requires `GITHUB_TOKEN` with repo scope |
| `pre-deploy` | ❌ Skip | Requires VPS SSH access |
| `deploy` | ❌ Skip | Requires VPS SSH + dotenvx keys |

## How it works

`act` runs inside the container defined in `Dockerfile`, with the host Docker socket mounted so job containers appear as siblings on the host daemon (not nested). The repo root is mounted as `/workspace` via the `REPO_ROOT` env var set by the script. Config is in `actrc`.
