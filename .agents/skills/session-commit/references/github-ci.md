# GitHub Actions — session-commit recipes

Requires `gh` authenticated to github.com (token already available via keyring for this repo). Never print tokens.

Parent repo: `kv055/dexArbTrading`. Workflow file: `.github/workflows/ci-cd.yml` (name: **CI/CD**).

## Find the run for a pushed SHA

```bash
SHA=$(git rev-parse HEAD)   # after push to develop
gh run list --branch develop --limit 10 \
  --json databaseId,headSha,status,conclusion,url,displayTitle,createdAt \
  | jq -r --arg sha "$SHA" '
      .[]
      | select(.headSha == $sha or (.headSha | startswith($sha[0:8])))
      | "id=\(.databaseId) \(.status)/\(.conclusion // "-") sha=\(.headSha[0:8]) \(.url)"
    '
```

If nothing matches within ~60s after push, check whether the commit only touched CI `paths-ignore` entries (e.g. `.agents/**`). In that case, stop polling and report “no run expected”.

## Poll until completed

```bash
RUN_ID=<id>
gh run watch "$RUN_ID" --exit-status
# or, if you need job-level detail while waiting:
gh run view "$RUN_ID" --json status,conclusion,jobs,url \
  | jq '{status, conclusion, url, jobs: [.jobs[] | {name, status, conclusion}]}'
```

`gh run watch` blocks until the run finishes and exits non-zero on failure.

## Failed job logs

```bash
gh run view "$RUN_ID" --log-failed \
  | grep -E 'Failure|fatal|error:|❌|not our ref|Submodule|exitcode' \
  | tail -80
```

Prefer grepping for high-signal lines first; fetch a larger tail only if the cause is unclear.

```bash
# full failed logs if needed
gh run view "$RUN_ID" --log-failed | tail -200
```

## Common failure: missing submodule SHA on GitHub

Symptom in checkout:

```text
fatal: remote error: upload-pack: not our ref <sha>
fatal: Fetched in submodule path '...', but it did not contain <sha>
```

Fix pattern (after user approval): push the missing submodule commit to `origin` `main`, then re-run or re-push parent.

## Re-run a failed workflow

```bash
gh run rerun "$RUN_ID" --failed
```

Only re-run after the underlying cause is fixed (or user asked to retry without code changes).

## Run URL

```bash
gh run view "$RUN_ID" --json url -q .url
```

Shape: `https://github.com/kv055/dexArbTrading/actions/runs/<id>`
