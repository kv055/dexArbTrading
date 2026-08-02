# API documentation

The repository provides one local Swagger UI backed by service-owned OpenAPI
documents. Start it with:

```bash
docker compose --profile docs up api-docs
```

Open <http://127.0.0.1:8088> and select a service from the definition picker.
The site is bound to loopback because it includes internal and administrative
routes.

## Coverage and maturity

The route catalogs cover every statically registered HTTP route in
`dexUnifiedApi`, `dexMmController`, `dexMonitor`, the monitor's standalone
`bisqstats` command, and `dexFiatController`. `commons` is a shared Go library
and does not expose an HTTP server.

Catalog operations marked `x-contract-status: route-catalog` document the
method and path but do not yet promise a precise request or response model. The
MM execution definition is the first strongly typed contract and also generates
the monitor frontend's TypeScript types.

After adding or removing a route, regenerate the catalogs:

```bash
python3 scripts/openapi-route-catalogs.py
```

Use `python3 scripts/openapi-route-catalogs.py --check` to verify that committed
catalogs match the Go route registrations.
