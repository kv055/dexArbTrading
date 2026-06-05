#!/usr/bin/env bash
# Generate a self-signed TLS cert/key for the nginx edge.
#
# nginx terminates TLS on :443 (published as 5443) and refuses to start if the
# cert/key are missing, so this MUST run once on each host before `docker compose
# up nginx`. The files land in deploy/nginx/certs/ (gitignored — keys are never
# committed) and are mounted read-only into the container at /etc/nginx/certs.
#
# This is a stop-gap so agents get TLS without a domain. For a public,
# browser-trusted edge use Let's Encrypt instead: issue a cert for your domain
# and drop the fullchain/privkey in as server.crt/server.key (or mount
# /etc/letsencrypt and point ssl_certificate at it).
#
# Usage:
#   deploy/nginx/gen-self-signed-cert.sh [CN] [days]
# Example:
#   deploy/nginx/gen-self-signed-cert.sh monitor.example.com 825
set -euo pipefail

CN="${1:-localhost}"
DAYS="${2:-825}"
CERT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/certs"

mkdir -p "$CERT_DIR"

if [[ -f "$CERT_DIR/server.crt" && -f "$CERT_DIR/server.key" ]]; then
    echo "Cert already present at $CERT_DIR — refusing to overwrite."
    echo "Delete server.crt/server.key first to regenerate."
    exit 0
fi

# SAN covers the CN plus localhost/loopback so curl from the host works too.
openssl req -x509 -newkey rsa:2048 -nodes \
    -keyout "$CERT_DIR/server.key" \
    -out "$CERT_DIR/server.crt" \
    -days "$DAYS" \
    -subj "/CN=${CN}" \
    -addext "subjectAltName=DNS:${CN},DNS:localhost,IP:127.0.0.1"

chmod 600 "$CERT_DIR/server.key"
echo "Wrote $CERT_DIR/server.crt and server.key (CN=${CN}, ${DAYS}d)."
echo "Agents must trust this cert or skip verification (e.g. curl --cacert server.crt)."
