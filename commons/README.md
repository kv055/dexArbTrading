# commons

`commons` is the shared Go module used by the dexPendulum services for market data, trade-cost modelling, Bitcoin fee analysis, and hedge calculations.

It is not a standalone service. It is imported by modules such as `dexMonitor`, `dexUnifiedApi`, and `dexTakerController` through local module replacement in the parent workspace.

## Packages

```text
commons/
+-- bitcoin/     # Fee recommendation, mempool/network fetching, and fee prediction
+-- hedge/       # Hedge configuration, venue modelling, transfer-time estimates, simulations
+-- prices/      # Live CEX price manager and exchange price feeds
+-- tradecost/   # CEX fee/withdrawal profiles and route-cost estimation
```

## Bitcoin Fee Logic

The `bitcoin` package models on-chain fee pressure and converts network data into fee recommendations. It includes network fetchers, fee analyzers, predictors, and recommendation models used by higher-level trade-cost flows.

Use this package when a service needs a consistent on-chain fee estimate instead of embedding its own fee heuristics.

## Price Feeds

The `prices` package provides a manager for CEX prices across supported exchanges. Services use it to keep premium and spread calculations fresh without duplicating exchange polling logic.

The currently supported CEX list is exposed through the shared trade-cost package, so the UI, unified API, and monitor can stay aligned.

## Trade-Cost Modelling

The `tradecost` package centralizes CEX taker fees, withdrawal fees, Lightning/on-chain method choices, and route-level cost estimation.

It is used to answer questions like:

- What is the effective BTC acquisition cost on a specific CEX?
- How much premium remains after taker fees and withdrawal fees?
- Which supported CEX route looks cheapest for a given offer?

## Hedge Modelling

The `hedge` package models hedging venues and transfer timing. It includes config loading, live input helpers, simulations, and optimizer tests.

`hedge/config.json` is the embedded default config. Set service-specific override variables, such as `DEXMONITOR_HEDGE_CONFIG`, when a runtime needs a different config file.

## Testing

Run tests from this module directory:

```bash
cd commons
go test ./...
```

Many tests are deterministic unit tests. Any future test that touches external networks should be marked clearly at the call site so regular local runs remain predictable.

## Development Notes

- Keep this module dependency-light. It sits underneath multiple services.
- Prefer adding shared market/cost logic here when two or more services need the same calculation.
- Avoid service-specific HTTP handlers or database code in this module; those belong in the consuming service.
