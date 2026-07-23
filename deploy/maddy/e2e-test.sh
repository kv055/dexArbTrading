#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
work_dir="$(mktemp -d)"
container="maddy-payment-e2e-$$"

cleanup() {
  docker rm -f "$container" >/dev/null 2>&1 || true
  docker run --rm -v "$work_dir:/cleanup" alpine:3.20 \
    sh -c 'rm -rf /cleanup/data' >/dev/null 2>&1 || true
  rmdir "$work_dir" >/dev/null 2>&1 || true
}
trap cleanup EXIT

mkdir -p "$work_dir/data/tls"
openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout "$work_dir/data/tls/privkey.pem" \
  -out "$work_dir/data/tls/fullchain.pem" \
  -days 1 -subj '/CN=mx.example.test' >/dev/null 2>&1

common=(
  -e MADDY_HOSTNAME=mx.example.test
  -e MADDY_DOMAIN=example.test
  -e MADDY_RECIPIENT=payments@example.test
  -v "$work_dir/data:/data"
  -v "$repo_root/deploy/maddy/maddy.conf:/data/maddy.conf:ro"
)

docker run --rm "${common[@]}" \
  -e MADDY_IMAP_PASSWORD=test-secret \
  -v "$repo_root/deploy/maddy/bootstrap.sh:/bootstrap.sh:ro" \
  --entrypoint /bin/sh foxcpp/maddy:0.9.5 /bootstrap.sh >/dev/null

start_maddy() {
  docker run -d --name "$container" "${common[@]}" \
    -p 127.0.0.1:25250:25 foxcpp/maddy:0.9.5 >/dev/null
  for _ in $(seq 1 30); do
    if docker exec "$container" kill -0 1 2>/dev/null; then
      return
    fi
    sleep 0.2
  done
  return 1
}

start_maddy
{
  sleep 1
  printf 'EHLO sender.gmail.com\r\n'
  printf 'MAIL FROM:<test@gmail.com>\r\n'
  printf 'RCPT TO:<payments@example.test>\r\n'
  printf 'DATA\r\n'
  printf 'From: test@gmail.com\r\nTo: payments@example.test\r\n'
  printf 'Subject: capture fixture\r\nMessage-ID: <capture-e2e@example.test>\r\n\r\n'
  printf 'capture body\r\n.\r\nQUIT\r\n'
} | openssl s_client -quiet -starttls smtp \
      -connect 127.0.0.1:25250 -servername mx.example.test \
      -CAfile "$work_dir/data/tls/fullchain.pem" >/dev/null 2>&1

# Restart against the same volume before reading to prove durable recovery.
docker rm -f "$container" >/dev/null
start_maddy
docker rm -f "$container" >/dev/null

output="$(docker run --rm "${common[@]}" foxcpp/maddy:0.9.5 \
  imap-msgs list payments@example.test INBOX)"
grep -Fq 'capture fixture' <<<"$output"
echo "Maddy SMTP -> durable mailbox test passed"
