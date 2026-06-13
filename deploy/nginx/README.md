# nginx edge: secrets

## TLS cert (`certs/server.{crt,key}`) — automatic

Host-local (gitignored, never committed). Created/renewed by the CI deploy
guard on every rollout via `gen-self-signed-cert.sh` (ENSURE mode). Locally, run
the script once before `docker compose up nginx`. See the script header for
Let's Encrypt notes.

## Basic-auth credentials (`EDGE_AUTH_HTPASSWD`) — set once, ships via `.env.enc`

The whole edge (dashboard + APIs) is gated with HTTP basic auth because it
exposes trading credentials and LND channel controls; only the Grafana
locations are exempt (Grafana enforces its own session/token auth). The nginx
container writes `/etc/nginx/htpasswd` from `$EDGE_AUTH_HTPASSWD` at startup and
refuses to start when it is unset.

This is a normal env var, so it rides the same encrypted path as every other
secret: it lives in the root `.env` (plaintext, gitignored), is encrypted into
the committed `.env.enc` with dotenvx, and the CI deploy job decrypts `.env.enc`
and ships it to the VPS as `.env`. **You do not touch the VPS** — set it once
and commit the re-encrypted `.env.enc`. The on-VPS deploy preflight still
**fails fast** if the decrypted `.env` somehow lacks the var.

> **IMPORTANT — double every `$`.** Compose performs `${VAR}` interpolation on
> `.env` values, so the `$` in an htpasswd hash (`$apr1$…`, bcrypt `$2y$…`) is
> eaten unless escaped as `$$`. A raw hash silently mangles to garbage and locks
> everyone out (nginx still starts, all logins 401). dotenvx stores the value
> literally, so the `$$` must already be in `.env`/`.env.enc`. The `sed` below
> does the escaping:

```bash
# 1. Generate the doubled user:hash (apr1 default; htpasswd -nbB for bcrypt):
htpasswd -nb <user> <password> | sed 's/\$/$$/g'
#    or, without apache2-utils:
printf '%s:%s\n' <user> "$(openssl passwd -apr1 <password>)" | sed 's/\$/$$/g'

# 2. Put it in the root .env (gitignored), then encrypt into the committed .env.enc:
#    add the line   EDGE_AUTH_HTPASSWD=<user>:$$apr1$$...   to .env, then:
dotenvx set EDGE_AUTH_HTPASSWD '<user>:$$apr1$$...' -f .env.enc   # reuses the existing keypair
#    (equivalently, the project's standard `cp .env .env.enc && dotenvx encrypt -f .env.enc`)

# 3. Commit .env.enc.
```

To confirm the round-trip before deploying — dotenvx decrypt preserves the
`$$`, and compose collapses it to a single `$` for the container (don't trust
`docker compose config`, which re-escapes `$` in its own output):

```bash
docker compose run --rm --no-deps --entrypoint sh nginx -c 'printf "%s\n" "$EDGE_AUTH_HTPASSWD"'
# must print a single-$ hash: <user>:$apr1$...
```
