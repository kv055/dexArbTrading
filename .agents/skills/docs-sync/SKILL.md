---
name: docs-sync
description:
  Audit, write, rewrite, or synchronize dexPendulum documentation with the
  current code. Use this skill for requests to update MkDocs pages, replace
  generated low-value docs, document Go functions, explain call flows, check
  whether service docs match implementation behavior, or update docs for
  specific file paths or described features.
---

# Docs Sync

## Goals

- Keep documentation aligned with the behavior of the code in this repository.
- Replace generated trivia with useful maintainer-facing explanations.
- Document functions by explaining what they do, where they are called, and how
  they participate in runtime flow.
- Preserve the existing MkDocs monorepo layout and service-local docs trees.

## Scope

Documentation work is limited to:

- Root docs: `docs/index.md`, `docs/mkdocs.yml`
- Service docs: `dexMonitor/docs/**`, `dexUnifiedApi/docs/**`,
  `dexTakerController/docs/**`
- Service nav files: `dexMonitor/mkdocs.yml`, `dexUnifiedApi/mkdocs.yml`,
  `dexTakerController/mkdocs.yml`

Treat source code, Docker Compose configuration, scripts, migrations, and
existing runtime behavior as the source of truth.

Do not edit application code as part of docs sync work.

Do not rewrite every existing documentation page by default. Treat the user's
prompt as the scope input and keep work limited to that scope unless the user
explicitly asks for a repo-wide documentation rewrite.

## Optional Scope Inputs

This skill accepts optional scope inputs from the user's prompt. These are not
formal command-line parameters; they are natural-language inputs that should be
parsed before investigation starts.

Supported inputs:

- `paths`: one or more source files, source folders, documentation files,
  documentation folders, or MkDocs nav files.
- `feature`: a description of runtime behavior, a user-visible workflow, an API,
  a command, a background job, a config area, or an integration.
- `mode`: audit, rewrite, update, create, or validate. If omitted, use
  audit-first behavior.

Path-scoped behavior:

- If the user provides source paths, inspect only those files plus direct callers,
  direct callees, neighboring package docs, and the matching service nav.
- If the user provides documentation paths, inspect those docs plus the source
  files they claim to document.
- If the user provides folder paths, include files in that folder and only expand
  outward when call flow or documentation accuracy requires it.

Feature-scoped behavior:

- First locate the feature in code before editing documentation.
- Search for likely symbols, handlers, routes, commands, config names, database
  tables, Compose services, log messages, and tests related to the feature.
- Build a small feature map of entrypoints, core implementation files, storage
  or external integrations, and callers.
- Then inspect the docs that already mention the feature or that should contain
  it based on the service docs structure.
- If multiple unrelated implementations match the description, stop after the
  investigation and ask the user to choose the intended feature.

Unscoped behavior:

- If no paths or feature description are provided, do not perform a broad rewrite.
- Produce a short report asking for a path, folder, or feature description.
- A broad repo-wide audit is allowed only when the user explicitly asks for one,
  and even then proposed edits must be grouped into small approval-sized batches.

## Audit-First Workflow

Before changing documentation, produce a `Docs Sync Report` and ask for approval
to apply the proposed edits.

The report should include:

- Existing docs to replace or rewrite.
- Missing function coverage.
- Incorrect or outdated behavior descriptions.
- Proposed page or schema changes.
- Questions that must be answered before docs can be accurate.

After approval, edit only the documentation and MkDocs nav files required for
the approved scope.

## Investigation Steps

1. Parse optional scope inputs from the user's prompt: paths, feature
   description, and requested mode.
2. Refuse to infer a repo-wide rewrite from an unscoped request.
3. For path-scoped requests, resolve matching source docs and docs source files.
4. For feature-scoped requests, find the feature in code before reviewing docs.
5. Inspect the relevant docs and source files directly.
6. If on `main`, compare docs against the current working tree.
7. If on another branch, compare relevant behavior against `main` without
   switching branches.
8. Build a code inventory:
   - Go functions and methods.
   - Call sites.
   - Commands, handlers, jobs, constructors, and other entrypoints.
   - Configuration and environment handling.
   - Database migrations and persistence behavior.
   - Docker Compose services and scripts when they affect runtime behavior.
9. Review existing docs for old low-value generated content:
   - Source line counts.
   - Type inventories without behavior.
   - Key dependency lists.
   - Package trivia.
   - Generic function blurbs.
   - Metrics or summaries that can be gathered by skimming code.
10. Identify stale, incomplete, duplicated, misplaced, or nav-missing pages.
11. Write proposed documentation changes using the schemas below.

## Source File Page Schema

Use this structure for pages that document a source file.

~~~markdown
# `<source-file>.go`

## Purpose

A short, concrete explanation of why this file exists in the service.

## Runtime Role

Describe when this file participates in the application lifecycle or
request/job flow. Mention important inputs, outputs, external systems,
persistence, network calls, and side effects.

## Function Map

### `<functionOrMethodName>`

Signature:

```go
func example(...)
```

Description:
Explain what the function does in practical terms, including important
branches, data transformations, validation, persistence, network calls, and
error behavior.

Called by:
- `<caller>` in `<path>`: explain why/when it calls this function.
- `external/runtime entrypoint`: use this only for `main`, HTTP handlers,
  goroutines, callbacks, or framework-invoked code.

Calls:
- `<callee>`: explain the important relationship only when it helps understand
  flow.

Side effects:
- Database writes, HTTP/gRPC calls, file I/O, environment reads, logging,
  goroutines, timers, process exit, or shared state mutation.

Failure behavior:
- Returned errors, swallowed/logged errors, fallbacks, retries, panics, fatal
  exits, or best-effort behavior.

Notes:
Any non-obvious domain rules, invariants, or coupling that future maintainers
need.
~~~

Omit sections that truly do not apply. Do not replace missing analysis with
generic filler.

## Folder README Schema

Use this structure for folder `README.md` pages.

~~~markdown
# `<package-or-folder>`

## Purpose

Why this package/folder exists.

## Main Flows

Describe the important flows across files, with links to the relevant
source-file docs.

## Entry Points

List commands, handlers, exported functions, jobs, or constructors that start
behavior in this folder.

## Operational Notes

Mention configuration, persistence, network dependencies, runtime assumptions,
and failure modes that matter in production.
~~~

Omit sections that truly do not apply. Do not add placeholder text.

## Writing Rules

- Prefer behavioral explanations over inventories.
- Explain why a function exists, what data it consumes and produces, what it
  changes, and how failure is handled.
- Describe call relationships in terms of runtime flow, not just symbol names.
- Link to nearby docs when that helps navigation.
- Keep prose concise, but include enough detail for a maintainer to understand
  the function without re-reading every branch of the source file.
- Do not include source line counts, generic package summaries, dependency
  lists, or type/constant inventories unless a specific type or constant is
  essential to understanding behavior.
- Keep headings stable and predictable so future docs sync work can update
  pages mechanically.
- Update the relevant `mkdocs.yml` nav file when adding, moving, or removing a
  page.

## Validation

After approved documentation edits, validate the docs build:

```bash
docker compose run --rm docs build -f docs/mkdocs.yml --strict
```

If validation cannot be run, state why and describe the remaining risk.
