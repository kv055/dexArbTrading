#!/usr/bin/env bash
# Generate a self-signed TLS cert/key for the nginx edge.
#
# nginx terminates TLS on :443 (published as 5443) and refuses to start without a
# cert/key, so one must exist at deploy/nginx/certs/server.{crt,key} before
# `docker compose up nginx`. In prod the CI deploy guard runs this in ENSURE mode
# on every rollout, so the cert is created/renewed automatically — no manual step.
# The files are gitignored (keys are never committed) and mounted read-only into
# the container at /etc/nginx/certs.
#
# This is a stop-gap so agents get TLS without a domain. For a public,
# browser-trusted edge use Let's Encrypt instead: issue a cert for your domain
# and drop the fullchain/privkey in as server.crt/server.key (or mount
# /etc/letsencrypt and point ssl_certificate at it).
#
# Modes:
#   (default)  create if absent; refuse to overwrite an existing cert.
#   ENSURE=1   create if absent OR if the existing cert expires within 30 days;
#              otherwise no-op. Used by the deploy guard so it self-heals.
#
# Usage:
#   deploy/nginx/gen-self-signed-cert.sh [CN] [days]
#   ENSURE=1 deploy/nginx/gen-self-signed-cert.sh 203.0.113.9
# Example:
#   deploy/nginx/gen-self-signed-cert.sh monitor.example.com 825
set -euo pipefail

CN="${1:-localhost}"
DAYS="${2:-825}"
CERT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/certs"
CRT="$CERT_DIR/server.crt"
KEY="$CERT_DIR/server.key"

mkdir -p "$CERT_DIR"

if [[ -f "$CRT" && -f "$KEY" ]]; then
    if [[ -z "${ENSURE:-}" ]]; then
        echo "Cert already present at $CERT_DIR — refusing to overwrite."
        echo "Delete server.{crt,key} to regenerate, or run with ENSURE=1."
        exit 0
    fi
    # ENSURE: keep the existing cert unless it is expiring within 30 days.
    if openssl x509 -checkend $((30 * 86400)) -noout -in "$CRT" >/dev/null 2>&1; then
        echo "Cert present and valid >30d at $CERT_DIR — nothing to do (ENSURE)."
        exit 0
    fi
    echo "Cert missing/key or expiring within 30d — regenerating (ENSURE)."
fi

# Build the SAN list: the CN as IP: or DNS: depending on its form (an IP CN as a
# DNS entry would fail hostname verification when an agent pins with --cacert),
# plus loopback so host-local curl works. Dedup keeps it tidy if CN == localhost.
declare -A seen=()
san_parts=()
add_san() { [[ -n "${seen[$1]:-}" ]] || { seen[$1]=1; san_parts+=("$1"); }; }
if [[ "$CN" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    add_san "IP:${CN}"
else
    add_san "DNS:${CN}"
fi
add_san "DNS:localhost"
add_san "IP:127.0.0.1"
SAN="$(IFS=,; echo "${san_parts[*]}")"

openssl req -x509 -newkey rsa:2048 -nodes \
    -keyout "$KEY" \
    -out "$CRT" \
    -days "$DAYS" \
    -subj "/CN=${CN}" \
    -addext "subjectAltName=${SAN}"

chmod 600 "$KEY"
echo "Wrote $CRT and server.key (CN=${CN}, SAN=${SAN}, ${DAYS}d)."
echo "Agents pin it with: curl --cacert server.crt (skip-verify -k only as a fallback)."
