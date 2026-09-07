---
name: session-commit
description: >-
  Commit this session's changes across submodules and the parent repo, push to
  develop, and monitor CI. Accepts destination github (default) or gitea. Use
  when asked for a session commit, to commit and push develop, to ship session
  work to GitHub or Gitea, or to commit submodule pointers and parent changes
  then watch the pipeline. Prefer this over the simple commit skill when
  submodules, develop push, or CI monitoring are in scope.
---

# Session Commit

## Destination param

Resolve **once** at the start from the user request:

| User says / args | Destination | Remote name |
|------------------|-------------|-------------|
| (none) / `github` / `origin` | **`github`** (default) | `origin` |
| `gitea` | `gitea` | `gitea` |

Only push that remote. Do not push the other host unless the user separately asks.

Examples that select Gitea: “session commit to gitea”, “push gitea”, `destination=gitea`.
Anything else (or no destination) → GitHub.

## Goals

- Commit **this session's** dirty work in each touched submodule on `main`.
- Commit parent changes and updated submodule pointers on `develop`.
- Push to the chosen remote, then monitor that host's Actions until done.
- On pipeline failure: diagnose, then **ask before fixing**.

## Non-goals

- Do not promote `develop` → `main` / `master`.
- Do not push the non-selected remote (e.g. skip `gitea` when destination is `github`).
- Do not replace the simple [`commit`](../commit/SKILL.md) skill for local-only commits.

## Repo facts

| Item | GitHub (`origin`) | Gitea (`gitea`) |
|------|-------------------|-----------------|
| Parent branch | `develop` | `develop` |
| Submodule branch | `main` (all four) | `main` (all four) |
| Submodules | `dexFiatController`, `dexMmController`, `dexMonitor`, `dexUnifiedApi` | same |
| CI workflow | `.github/workflows/ci-cd.yml` | `.gitea/workflows/ci.yml` |
| CI monitor | `gh` CLI — [references/github-ci.md](references/github-ci.md) | Gitea API — [references/gitea-ci.md](references/gitea-ci.md) |
| Creds / auth | `gh` (logged-in GitHub account) | `deploy/provision/gitea/.env.gitea` (`GITEA_URL`, `GITEA_OWNER`, `GITEA_TOKEN`) |

Order matters: **push submodule commits to the destination remote before** the parent pin bump, so CI can fetch those SHAs.

## Workflow checklist

```
Session commit (destination = github|gitea):
- [ ] 0. Resolve destination (default github)
- [ ] 1. Inventory + classify (session vs other) — hard stop if other
- [ ] 2. Commit each in-scope submodule on main
- [ ] 3. Push each submodule to <remote> main
- [ ] 4. Parent on develop: pins + parent files → commit
- [ ] 5. Push <remote> develop
- [ ] 6. Monitor that host's Actions for the pushed SHA
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
4. Push: `git push <remote> HEAD:main` where `<remote>` is `origin` or `gitea`.
5. Do not push parent `main`/`master`. Do not push the non-selected remote.

## 3. Parent repo on `develop`

1. Check out parent `develop` (track `<remote>/develop` if needed).
2. Update submodule gitlinks to the commits just pushed.
3. Stage included parent files + updated submodule pointers.
4. Commit on `develop` (message covers parent changes and pin bumps).
5. Push: `git push <remote> develop` (lefthook blocks only `main`/`master`).

## 4. Monitor pipeline

After the parent push, monitor **only** the destination host:

- **github** → [references/github-ci.md](references/github-ci.md) (`gh run list` / `gh run watch`).
- **gitea** → [references/gitea-ci.md](references/gitea-ci.md) (API + `.env.gitea`). Never print `GITEA_TOKEN`.

Find the run for branch `develop` and the pushed `head_sha`. Poll until completed. Report conclusion, per-job results, and the run URL.

**paths-ignore:** Both workflows ignore `.agents/**` (and other listed paths). If the push only touches ignored paths, there may be **no new run** — report that explicitly and treat as success (do not poll forever).

## 5. On failure: diagnose, then ask

1. Fetch failed job logs (destination reference).
2. Summarize the root cause for the user.
3. **Stop and ask for permission to fix.** Do not auto-patch or re-push without approval.
4. After approval: fix, then repeat this skill's commit/push/monitor loop for the fix (same destination).

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

- Destination used (`github` / `gitea`) and remote (`origin` / `gitea`)
- Submodule commits + SHAs pushed to `<remote>/main`
- Parent commit SHA on `develop` and push result
- Pipeline: run URL, conclusion (or “no run — paths-ignore”)
- If failed: diagnosis + explicit ask for permission to fix
