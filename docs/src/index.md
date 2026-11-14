# dexPendulum Docs

This wiki collects the generated Go code documentation for the main trading
services in this repository.

## Modules

- [API documentation](api-documentation.md) explains the combined Swagger UI,
  service-owned source contracts, generated bundles, and drift checks.

- [dexMonitor](dexMonitor/README.md) documents the monitoring API, live-offer views,
  account health proxying, statistics, and trading-account management code.
- [dexUnifiedApi](dexUnifiedApi/README.md) documents the unified API, snapshot
  ingestion, database helpers, lifecycle persistence, and payment notification
  code.
- [dexTakerController](dexTakerController/README.md) documents the market-maker controller,
  exchange clients, wallet bridge, account health checks, and related
  orchestration code.

Build the documentation image once:

```bash
docker build -f docs/Dockerfile.docs -t dexarbtrading-docs .
```

Serve the wiki locally:

```bash
docker run --rm -p 127.0.0.1:8000:8000 -v "$PWD:/workspace:ro" \
  dexarbtrading-docs serve -f /workspace/docs/mkdocs.yml --dev-addr=0.0.0.0:8000
```

Validate the wiki build:

```bash
docker run --rm -v "$PWD:/workspace:ro" dexarbtrading-docs \
  build -f /workspace/docs/mkdocs.yml --strict
```
