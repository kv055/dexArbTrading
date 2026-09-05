# Gitea Actions — session-commit recipes

Use when session-commit **destination is `gitea`**.

Load credentials once (never echo the token):

```bash
set -a
# shellcheck disable=SC1091
source deploy/provision/gitea/.env.gitea
set +a
REPO="${GITEA_OWNER}/dexArbTrading"
AUTH=(-H "Authorization: Bearer ${GITEA_TOKEN}")
```

Repo name is `dexArbTrading` for the parent. Submodule mirrors use the same owner and their directory names.

## Find the run for a pushed SHA

```bash
SHA=$(git rev-parse HEAD)   # after push to develop
curl -sS "${AUTH[@]}" \
  "${GITEA_URL}/api/v1/repos/${REPO}/actions/runs?limit=10" \
  | jq -r --arg sha "$SHA" --arg br develop '
      .workflow_runs[]
      | select(.head_branch==$br and (.head_sha|startswith($sha[0:8]) or .head_sha==$sha))
      | "#\(.run_number) id=\(.id) \(.status)/\(.conclusion // "-") sha=\(.head_sha[0:8]) \(.html_url // .url)"
    '
```

If nothing matches within ~60s after push, check whether the commit only touched CI `paths-ignore` entries (e.g. `.agents/**`). In that case, stop polling and report “no run expected”.

## Poll until completed

```bash
RUN_ID=<id>
for i in $(seq 1 120); do
  RUN=$(curl -sS "${AUTH[@]}" "${GITEA_URL}/api/v1/repos/${REPO}/actions/runs/${RUN_ID}")
  STATUS=$(echo "$RUN" | jq -r '.status')
  CONCLUSION=$(echo "$RUN" | jq -r '.conclusion // "-"')
  echo "$(date +%H:%M:%S) status=$STATUS conclusion=$CONCLUSION"
  curl -sS "${AUTH[@]}" "${GITEA_URL}/api/v1/repos/${REPO}/actions/runs/${RUN_ID}/jobs" \
    | jq -r '.jobs[] | "  \(.name): \(.status)/\(.conclusion // "-")"'
  [ "$STATUS" = "completed" ] && break
  sleep 15
done
```

## Failed job logs

```bash
FAILED=$(curl -sS "${AUTH[@]}" \
  "${GITEA_URL}/api/v1/repos/${REPO}/actions/runs/${RUN_ID}/jobs" \
  | jq -r '.jobs[] | select(.conclusion=="failure") | .id')

for JID in $FAILED; do
  echo "==== job $JID ===="
  curl -sS "${AUTH[@]}" \
    "${GITEA_URL}/api/v1/repos/${REPO}/actions/jobs/${JID}/logs" \
    | grep -E 'Failure|fatal|error:|❌|not our ref|Submodule|exitcode' \
    | tail -80
done
```

Prefer grepping for high-signal lines first; fetch a larger tail only if the cause is unclear.

## Common failure: missing submodule SHA on Gitea

Symptom in Secret protection / checkout:

```text
fatal: remote error: upload-pack: not our ref <sha>
fatal: Fetched in submodule path '...', but it did not contain <sha>
```

Fix pattern (after user approval): push the missing submodule commit to `gitea` `main`, then re-run or re-push parent.

## Re-run a failed workflow

```bash
curl -sS -X POST "${AUTH[@]}" \
  "${GITEA_URL}/api/v1/repos/${REPO}/actions/runs/${RUN_ID}/rerun" \
  -w "\nHTTP %{http_code}\n"
```

Only re-run after the underlying cause is fixed (or user asked to retry without code changes).

## Run URL

Prefer `html_url` from the run JSON when present. Fallback shape:

```text
${GITEA_URL}/${GITEA_OWNER}/dexArbTrading/actions/runs/${RUN_ID}
```
