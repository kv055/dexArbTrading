# Monitoring Stack

Prometheus + Grafana setup for the dexArbTrading stack. All services run as Docker Compose services alongside the application.

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                      Grafana :3000                      │
│              (served via nginx at /grafana/)             │
└───────────────────────┬─────────────────────────────────┘
                        │ datasource
┌───────────────────────▼─────────────────────────────────┐
│                   Prometheus :9090                       │
│              retention: 30d  scrape: 15s                 │
└──┬──────────┬──────────┬──────────┬──────────┬──────────┘
   │          │          │          │          │
:9100      :8080      :9187      :5010      :5000
node-     cadvisor  postgres-  unified-    app
exporter            exporter    api     (dexMonitor)
```

## Services

| Service | Image | Role |
|---|---|---|
| `prometheus` | prom/prometheus:v2.55.1 | Metrics storage and scraping |
| `grafana` | grafana/grafana:11.3.0 | Dashboards and visualization |
| `node-exporter` | prom/node-exporter:v1.8.2 | Host CPU, memory, disk, network metrics |
| `cadvisor` | gcr.io/cadvisor/cadvisor:v0.49.1 | Per-container resource metrics |
| `postgres-exporter` | prometheuscommunity/postgres-exporter:v0.16.0 | PostgreSQL metrics |

The application services (`unified-api`, `app`) expose their own `/metrics` endpoints and are scraped directly.

## Dashboards

Pre-provisioned dashboards live in [grafana/dashboards/](grafana/dashboards/) and are loaded automatically on startup:

**Start here:** `system-overview.json` is the single-pane golden-signals view; drill
from it into the logs, traces and domain dashboards below.

| File | What it shows |
|---|---|
| `system-overview.json` | Golden signals across metrics + logs in one pane (exchanges up, snapshot age, error rate, latency, recent errors) |
| `trading-overview.json` | Arbitrage opportunity: best net premium, profitable offers, liquidity, premium distribution |
| `data-freshness.json` | Snapshot/OHLC age, exchange up/down, price-feed staleness & reconnects |
| `accounts-and-ws.json` | Trading-account health + live-feed WebSocket clients/broadcasts |
| `logs-and-errors.json` | **(Loki)** Log volume by level, error/warn rate per service, live error stream — filter by `$service` |
| `traces-and-latency.json` | **(Tempo + Prom)** Snapshot-run durations, errored spans, HTTP/ingest latency |
| `go-services.json` | Go runtime + HTTP RED metrics from `unified-api` and `app` |
| `postgres.json` | PostgreSQL connections, transactions, cache hit rate |
| `host.json` | Node-exporter host metrics (CPU, RAM, disk, network) |
| `containers.json` | cAdvisor per-container resource usage |

The logs/traces dashboards rely on the `loki` and `tempo` datasources (provisioned in
[grafana/provisioning/datasources/](grafana/provisioning/datasources/)). `service` is the
same label across metrics, logs and traces (`unified-api`, `unified-snapshot`,
`dexmonitor`), so you can pivot between dashboards on one identity.

Dashboard updates from the Grafana UI are allowed (`allowUiUpdates: true`). To persist changes back to the repo, export the dashboard JSON from Grafana and overwrite the file.

## Configuration Files

```
deploy/monitoring/
├── prometheus.yml                          # Scrape targets
└── grafana/
    ├── provisioning/
    │   ├── datasources/
    │   │   ├── prometheus.yml              # Default datasource
    │   │   ├── loki.yml                     # Logs (+ trace_id -> Tempo link)
    │   │   └── tempo.yml                    # Traces (+ span -> Loki link)
    │   └── dashboards/dashboards.yml       # Points Grafana at the dashboards directory
    └── dashboards/                          # Auto-loaded; see the Dashboards table above
```

## Access

Grafana is served through nginx at the `/grafana/` sub-path:

```
http://<host>:5000/grafana/
```

Login with `admin` / `$GRAFANA_ADMIN_PASSWORD` (set in your `.env`).

Prometheus is not exposed externally — it is only reachable inside the Docker network.

## Adding a Scrape Target

Add a new job to [prometheus.yml](prometheus.yml):

```yaml
- job_name: my-service
  static_configs:
    - targets: ["my-service:PORT"]
```

Then reload Prometheus without restarting (lifecycle API is enabled):

```bash
docker compose exec prometheus wget -qO- --post-data='' http://localhost:9090/-/reload
```

## Data Persistence

| Volume | Content |
|---|---|
| `prometheus-data` | Time-series data, 30-day retention |
| `grafana-data` | Grafana state (users, alert rules, manually saved changes) |
| `loki-data` | Log chunks + index, 7-day retention |
| `tempo-data` | Trace blocks, 7-day retention |
| `alloy-data` | Alloy positions/WAL (so log scraping resumes after a restart) |

## Logs & Traces (OpenTelemetry)

Metrics answer "is something wrong"; logs and traces answer "why". The Go
services emit both via OpenTelemetry, stored in Loki (logs) and Tempo (traces)
and viewed in the same Grafana.

```
Go services ──stdout JSON──► Alloy ──► Loki ─┐
Go services ──OTLP :4317───► Alloy ──► Tempo ─┼──► Grafana (Explore + correlation)
Prometheus (metrics, unchanged) ─────────────┘
```

| Service | Image | Role |
|---|---|---|
| `loki` | grafana/loki:3.3.2 | Log storage/query (7-day retention) |
| `tempo` | grafana/tempo:2.7.1 | Trace storage/query, OTLP receiver |
| `alloy` | grafana/alloy:v1.5.1 | Scrapes container stdout → Loki; receives OTLP → Tempo |

- **Logs.** Services log structured JSON to stdout (`commons/logging`, built on
  `slog`). Alloy discovers containers via the docker socket, parses the JSON,
  promotes `level` to a label and `trace_id` to structured metadata, and ships
  to Loki. Non-JSON lines (bisq/nginx/postgres) still arrive, just without those
  fields. Config: [alloy/config.alloy](alloy/config.alloy),
  [loki/loki-config.yml](loki/loki-config.yml).
- **Traces.** Services build a TracerProvider in `commons/tracing` and push OTLP
  to Alloy, which forwards to Tempo. Instrumented boundaries: inbound HTTP
  (server spans, named by route), outbound HTTP (`otelhttp` transport — incl. the
  snapshot → unified-api `/internal/live_offers` push, so a snapshot run and the
  resulting ingest are **one trace**), Bisq gRPC (`otelgrpc`), and the snapshot
  cycle stages. Config: [tempo/tempo.yml](tempo/tempo.yml).
- **Correlation.** The Tempo datasource links a span to its logs in Loki
  (`tracesToLogsV2`); the Loki datasource turns a log line's `trace_id` into a
  link back to the trace. Both are provisioned in
  [grafana/provisioning/datasources/](grafana/provisioning/datasources/).

### Viewing

- **Logs:** Grafana → Explore → *Loki*, e.g. `{service="unified-snapshot"}` or
  `{service="unified-api"} | json | level="ERROR"`.
- **Traces:** Grafana → Explore → *Tempo* → Search by service/name, or paste a
  `trace_id` from a log line.

### Tuning

`OTEL_EXPORTER_OTLP_ENDPOINT` (default `alloy:4317`; empty disables tracing),
`OTEL_TRACES_SAMPLER_ARG` (head-sampling ratio, default `1.0`), `LOG_LEVEL`, and
`DEPLOY_ENV` are set per Go service in `docker-compose.yml`. See root
`.env.example`.

> **Security note:** Alloy mounts the docker socket read-only for log discovery.
> A read-only mount blocks writes to the socket *file* but not Docker API calls,
> so Alloy effectively has Docker API access — consistent with the existing
> privileged agents (cadvisor `privileged: true`, node-exporter host mounts).
> Loki/Tempo/Alloy publish no ports; they are reachable only on the docker
> network and through Grafana.
