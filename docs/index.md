# dexArbTrading Docs

This wiki collects the generated Go code documentation for the main trading
services in this repository.

## Modules

- [dexMonitor](dexMonitor/README.md) documents the monitoring API, live-offer views,
  account health proxying, statistics, and trading-account management code.
- [dexUnifiedApi](dexUnifiedApi/README.md) documents the unified API, snapshot
  ingestion, database helpers, lifecycle persistence, and payment notification
  code.
- [dexMmController](dexMmController/README.md) documents the market-maker controller,
  exchange clients, wallet bridge, account health checks, and related
  orchestration code.

Serve locally through Docker:

```bash
docker compose --profile docs up docs
```

Validate the wiki build through Docker:

```bash
docker compose run --rm docs build -f docs/mkdocs.yml --strict
```
