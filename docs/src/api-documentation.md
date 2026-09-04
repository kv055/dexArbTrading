# API documentation

The repository provides one local Swagger UI backed by service-owned OpenAPI
documents. Start it with:

```bash
docker compose --profile docs up api-docs
```

Open <http://127.0.0.1:8088> and select a service from the definition picker.
The site is bound to loopback because it includes internal and administrative
routes.

## Contract flow

The OpenAPI 3.1 documents under each service's `api/` directory are the source
of truth. Public and internal audiences are split into separate contracts.
Shared health and error schemas live in `commons/api/openapi/`; committed,
self-contained bundles under `api-docs/specs/` feed Swagger UI and every code
generator.

The bundles generate strict Go server interfaces and models in the services,
typed Go clients in `commons/api/client/`, and the monitor frontend's TypeScript
types. During the wire-compatible cutover, generated strict adapters own normal
JSON route registration and request binding. They buffer each established
handler's response, validate its status, content type, and body against the
contract, and only then send it to the client. A violation is rejected as a
server error. These adapters are recorded as `validated-legacy` in
`api-contracts.json`; `native-strict` is reserved for handlers that directly
implement generated request and response types. WebSockets, transparent
proxies, downloads, metrics, and Fiat capture mode are explicit raw exceptions
documented in the contracts.

## Changing an API

Edit the appropriate source contract before changing its implementation, then
run from the repository root:

```bash
make api-generate
make api-check
```

`api-check` bundles and lints every contract, checks generated versus raw route
ownership, regenerates Go and TypeScript artifacts (including response-validator
tests), and fails if committed outputs are stale. CI runs the same check for
every parent-repository change so a submodule pointer update cannot bypass
contract verification.
