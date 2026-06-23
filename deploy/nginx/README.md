# nginx edge: secrets

## TLS cert (`certs/server.{crt,key}`) — automatic

Host-local (gitignored, never committed). Created/renewed by the CI deploy
guard on every rollout via `gen-self-signed-cert.sh` (ENSURE mode). Locally, run
the script once before `docker compose up nginx`. See the script header for
Let's Encrypt notes.

## Authentication — application login (no edge basic auth)

The edge no longer does HTTP basic auth. Access is gated by the application: the
dexMonitor service serves a login page and a Postgres-backed session cookie
(`dex_session`), and both dexMonitor and unified-api require a valid session on
their browser-facing API/WS routes (see `commons/session`). nginx just terminates
TLS and proxies; only Grafana keeps its own separate login.

No nginx auth secret is needed here anymore. The legacy `EDGE_AUTH_HTPASSWD` env
var was removed from `docker-compose.yml`; any stale `EDGE_AUTH_HTPASSWD` left in
`.env`/`.env.enc` is simply unused.
