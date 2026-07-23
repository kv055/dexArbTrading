#!/usr/bin/env bash
# Issue or renew the public edge certificate, then atomically refresh nginx's
# mounted cert/key and reload nginx. Run from the repository root on the VPS.
set -Eeuo pipefail

action="${1:-renew}"
domain="${PUBLIC_HOSTNAME:-dexmonitor.live}"
env_file=".env"

if [[ -f "$env_file" ]]; then
  configured_email="$(sed -n 's/^LETSENCRYPT_EMAIL=//p' "$env_file" | tail -n 1 | tr -d '\r')"
  configured_domain="$(sed -n 's/^PUBLIC_HOSTNAME=//p' "$env_file" | tail -n 1 | tr -d '\r')"
  [[ -n "$configured_domain" ]] && domain="$configured_domain"
else
  configured_email=""
fi

[[ -n "${LETSENCRYPT_EMAIL:-}" ]] || LETSENCRYPT_EMAIL="$configured_email"
[[ -n "$LETSENCRYPT_EMAIL" ]] || { echo "LETSENCRYPT_EMAIL must be set in .env" >&2; exit 2; }

mkdir -p deploy/nginx/{acme-webroot,certs,letsencrypt}
if [[ "$action" == "issue" ]]; then
  docker compose --profile certbot run --rm certbot certonly --webroot \
    --webroot-path /var/www/certbot --domain "$domain" --email "$LETSENCRYPT_EMAIL" \
    --agree-tos --non-interactive --keep-until-expiring
elif [[ "$action" == "renew" ]]; then
  docker compose --profile certbot run --rm certbot renew --webroot --webroot-path /var/www/certbot
else
  echo "usage: $0 [issue|renew]" >&2
  exit 2
fi

cert_dir="deploy/nginx/letsencrypt/live/$domain"
[[ -f "$cert_dir/fullchain.pem" && -f "$cert_dir/privkey.pem" ]] || exit 0
install -m 0644 "$cert_dir/fullchain.pem" deploy/nginx/certs/server.crt
install -m 0600 "$cert_dir/privkey.pem" deploy/nginx/certs/server.key
docker compose exec -T nginx nginx -s reload
