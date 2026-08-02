# Project API documentation

The `api-docs` Compose profile hosts a single Swagger UI with a selector for
every HTTP-facing service in the repository:

```bash
docker compose --profile docs up api-docs
```

Open <http://127.0.0.1:8088>. The UI is deliberately bound to loopback because
several specifications describe internal or administrative endpoints.

## Specification ownership

- `dexUnifiedApi/api/openapi.yaml`: complete generated route catalog.
- `dexMmController/api/openapi.yaml`: complete generated route catalog.
- `dexMmController/api/execution.openapi.yaml`: strongly typed execution
  contract used to generate frontend types.
- `dexMonitor/api/openapi.yaml`: complete generated route catalog.
- `dexMonitor/api/bisqstats.openapi.yaml`: standalone `bisqstats` command.
- `dexFiatController/api/openapi.yaml`: complete generated route catalog.
- `commons`: no specification because it is a Go library and does not expose an
  HTTP server.

Route catalogs are intentionally explicit about their maturity. They enumerate
all registered methods and paths, but use `x-contract-status: route-catalog`
until a handler's request and response schemas have been formalized.

Regenerate catalogs after changing a Go route registration:

```bash
python3 scripts/openapi-route-catalogs.py
```

CI uses `--check` to reject stale generated catalogs.
