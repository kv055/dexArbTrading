#!/usr/bin/env bash
# Idempotently replace the apex A record set for one GoDaddy DNS zone. The
# calling Ansible task provides the scoped PAT via the environment, never argv.
set -Eeuo pipefail

: "${GODADDY_PAT:?GODADDY_PAT is required}"
: "${GODADDY_DOMAIN:?GODADDY_DOMAIN is required}"
: "${GODADDY_IP:?GODADDY_IP is required}"

api="https://api.godaddy.com/v3/domains/zones/${GODADDY_DOMAIN}/dns-records"
auth=( -H "Authorization: Bearer ${GODADDY_PAT}" -H "Accept: application/json" )
records="$(curl -fsS "${auth[@]}" "$api")"

# The current v3 API returns an object with records; accepting a top-level array
# keeps the script tolerant of earlier response shapes.
existing="$(jq -r '(.records // .)[]? | select(.type == "A" and (.name == "@" or .name == "")) | [.id, .data] | @tsv' <<<"$records")"
if [[ "$existing" == *$'\t'"$GODADDY_IP"* ]] && [[ "$(wc -l <<<"$existing")" -eq 1 ]]; then
  exit 0
fi

while IFS=$'\t' read -r record_id _; do
  [[ -z "$record_id" ]] && continue
  curl -fsS -X DELETE "${auth[@]}" "$api/$record_id" >/dev/null
done <<<"$existing"

curl -fsS -X POST "${auth[@]}" -H 'Content-Type: application/json' \
  -d "{\"name\":\"@\",\"type\":\"A\",\"data\":\"${GODADDY_IP}\",\"ttl\":300}" \
  "$api" >/dev/null
