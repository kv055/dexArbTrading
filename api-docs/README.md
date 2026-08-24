# Project API documentation

The `api-docs` Compose profile hosts a single Swagger UI with a selector for
every HTTP-facing service in the repository:

```bash
docker compose --profile docs up api-docs
```

Open <http://127.0.0.1:8088>. The UI is deliberately bound to loopback because
several specifications describe internal or administrative endpoints.

## Specification ownership

Each HTTP service owns handwritten OpenAPI 3.1 source files under its `api/`
directory. Public and service-to-service contracts are separate:

- `dexUnifiedApi/api/{public,internal}.openapi.yaml`
- `dexMmController/api/{public,internal}.openapi.yaml`
- `dexMonitor/api/public.openapi.yaml` and
  `dexMonitor/api/bisqstats.public.openapi.yaml`
- `dexFiatController/api/{public,internal}.openapi.yaml`

Reusable error and health schemas live in `commons/api/openapi/`. The source
documents may reference those fragments, so generators, Swagger UI, and other
consumers use self-contained bundles committed under `api-docs/specs/`.
`commons` itself does not expose an HTTP server.

## Maintainer workflow

Change the source specification first, then regenerate all derived artifacts:

```bash
make api-install   # first run, or after a pinned tool version changes
make api-generate
make api-check
```

Generation updates the self-contained bundles, strict Go server interfaces and
models in each service, Go clients in `commons/api/client/`, and browser types
in `dexMonitor/react-frontend/src/generated/`. `make api-check` also lints all
contracts and verifies that generated and explicitly raw runtime routes account
for every operation. Contracts marked `validated-legacy` use generated strict
request binding and validate the established handler's complete response before
writing it to the client. `native-strict` is reserved for direct implementations
of the generated request and response interfaces.

WebSockets, transparent MM-controller proxies, binary downloads, metrics, and
Fiat capture mode are deliberately handwritten transports. Contract operations
for these routes carry the `raw` tag and an `x-raw-reason`; ordinary JSON routes
are owned by generated strict routers.
