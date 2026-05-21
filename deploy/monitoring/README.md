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

| File | What it shows |
|---|---|
| `host.json` | Node-exporter host metrics (CPU, RAM, disk, network) |
| `containers.json` | cAdvisor per-container resource usage |
| `postgres.json` | PostgreSQL connections, transactions, cache hit rate |
| `go-services.json` | Go runtime metrics from `unified-api` and `app` |

Dashboard updates from the Grafana UI are allowed (`allowUiUpdates: true`). To persist changes back to the repo, export the dashboard JSON from Grafana and overwrite the file.

## Configuration Files

```
deploy/monitoring/
├── prometheus.yml                          # Scrape targets
└── grafana/
    ├── provisioning/
    │   ├── datasources/prometheus.yml      # Auto-wires Prometheus as default datasource
    │   └── dashboards/dashboards.yml       # Points Grafana at the dashboards directory
    └── dashboards/
        ├── host.json
        ├── containers.json
        ├── postgres.json
        └── go-services.json
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
