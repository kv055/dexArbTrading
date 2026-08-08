---
name: session-commit
description: >-
  Commit this session's changes across submodules and the parent repo, push to
  Gitea develop, and monitor the CI pipeline. Use when asked for a session
  commit, to commit and push develop, to ship session work to Gitea, or to
  commit submodule pointers and parent changes then watch the pipeline. Prefer
  this over the simple commit skill when submodules, develop push, or CI
  monitoring are in scope.
---

# Session Commit

## Goals

- Commit **this session's** dirty work in each touched submodule on `main`.
- Commit parent changes and updated submodule pointers on `develop`.
- Push to **Gitea only** (`gitea` remote), then monitor Actions until done.
- On pipeline failure: diagnose, then **ask before fixing**.

## Non-goals

- Do not promote `develop` → `main` / `master`.
- Do not push GitHub `origin` unless the user separately asks.
- Do not replace the simple [`commit`](../commit/SKILL.md) skill for local-only commits.

## Repo facts

| Item | Value |
|------|--------|
| Parent remote | `gitea` |
| Parent branch | `develop` |
| Submodule remote | `gitea` |
| Submodule branch | `main` (all four) |
| Submodules | `dexFiatController`, `dexMmController`, `dexMonitor`, `dexUnifiedApi` |
| Gitea env | `deploy/provision/gitea/.env.gitea` (`GITEA_URL`, `GITEA_OWNER`, `GITEA_TOKEN`) |
| CI workflow | `.gitea/workflows/ci.yml` |

Order matters: **push submodule commits to Gitea before** the parent pin bump, so CI can fetch those SHAs.

## Workflow checklist

```
Session commit:
- [ ] 1. Inventory + classify (session vs other) — hard stop if other
- [ ] 2. Commit each in-scope submodule on main
- [ ] 3. Push each submodule to gitea main
- [ ] 4. Parent on develop: pins + parent files → commit
- [ ] 5. Push gitea develop
- [ ] 6. Monitor Gitea Actions for that SHA
- [ ] 7. On failure: diagnose, ask permission to fix
```

## 1. Scope classification (hard stop)

Before staging anything:

1. Inventory dirty paths in the parent and each submodule (`git status`, `git diff`).
2. Using **this session's history**, mark each path as session-owned or unrelated (pre-existing dirty work, other features, leftover pin bumps).
3. If any **non-session** changes exist: list them clearly and **ask** whether to include them. Do not proceed until the user answers.
4. Only stage paths the user confirmed (session-only, or session + explicitly included).

## 2. Submodule commits + push

For each submodule with included changes:

1. Ensure a branch checkout on **`main`** (not detached HEAD). Create/switch as needed.
2. Stage with `git add <specific-paths>`. Avoid `git add -A` unless the whole dirty tree is in scope.
3. Commit with a conventional message (see Message template). Match the message to the staged diff.
4. Push: `git push gitea HEAD:main`.
5. Do not push parent `main`/`master`. Do not push submodule `origin` unless asked.

## 3. Parent repo on `develop`

1. Check out parent `develop` (track `gitea/develop` if needed).
2. Update submodule gitlinks to the commits just pushed.
3. Stage included parent files + updated submodule pointers.
4. Commit on `develop` (message covers parent changes and pin bumps).
5. Push: `git push gitea develop` (lefthook blocks only `main`/`master`).

## 4. Monitor Gitea pipeline

After the parent push:

1. Load env from `deploy/provision/gitea/.env.gitea`. Never print `GITEA_TOKEN`.
2. Find the Actions run for branch `develop` and the pushed `head_sha`.
3. Poll run + jobs until `status=completed`.
4. Report conclusion, per-job results, and the run URL.

API recipes: [references/gitea-ci.md](references/gitea-ci.md).

**paths-ignore:** CI ignores `.agents/**` (and other listed paths). If the push only touches ignored paths, there may be **no new run** — report that explicitly and treat as success (do not poll forever).

## 5. On failure: diagnose, then ask

1. Fetch failed job logs via the Gitea API (see reference).
2. Summarize the root cause for the user.
3. **Stop and ask for permission to fix.** Do not auto-patch or re-push without approval.
4. After approval: fix, then repeat this skill's commit/push/monitor loop for the fix.

## Safety

- Never commit secrets (`.env`, credentials, tokens). Warn if asked to include them.
- Respect lefthook / gitleaks; do not use `--no-verify` unless the user explicitly overrides.
- Never force-push.
- Prefer `git add <file>` over blanket adds.
- Do not amend commits you did not create in this session, and do not amend if already pushed.

## Message template

Adapt `Co-authored-by` to the **current agent** (examples below). Subject: imperative, ≤72 chars, no trailing period. Wrap body at 72 chars. Prefer `git commit -F` / HEREDOC so newlines are literal.

```
<type>(<scope>): <short summary>

Summary:
- <what changed>

Rationale:
- <why>

Tests:
- <command or "not run (reason)">

Co-authored-by: <Agent> <<email>>
```

Co-authored-by examples:

- Cursor: `Cursor <cursoragent@cursor.com>`
- Codex: `Codex <codex@openai.com>`
- Claude: `Claude <noreply@anthropic.com>`
- OpenCode: `OpenCode <opencode@opencode.ai>`

Use the identity that matches the harness running this skill, unless the user specifies otherwise.

## Output

When finished, report:

- Submodule commits + SHAs pushed to `gitea/main`
- Parent commit SHA on `develop` and push result
- Pipeline: run URL, conclusion (or “no run — paths-ignore”)
- If failed: diagnosis + explicit ask for permission to fix
