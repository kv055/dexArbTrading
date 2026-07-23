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

# The current v3 API returns an object with items; accepting prior and top-level
# array shapes keeps the script compatible with earlier response formats.
existing="$(jq -r '(.items // .records // .)[]? | select(.type == "A" and (.name == "@" or .name == "")) | [(.recordId // .id), .data] | @tsv' <<<"$records")"
if [[ "$existing" == *$'\t'"$GODADDY_IP"* ]] && [[ "$(wc -l <<<"$existing")" -eq 1 ]]; then
  exit 0
fi

while IFS=$'\t' read -r record_id _; do
  [[ -z "$record_id" ]] && continue
  # GoDaddy record IDs can contain square brackets, which curl otherwise treats
  # as a URL glob expression.
  record_path="$(jq -nr --arg record_id "$record_id" '$record_id | @uri')"
  curl -fsS -X DELETE "${auth[@]}" "$api/$record_path" >/dev/null
done <<<"$existing"

curl -fsS -X POST "${auth[@]}" -H 'Content-Type: application/json' \
  -d "{\"name\":\"@\",\"type\":\"A\",\"data\":\"${GODADDY_IP}\",\"ttl\":600}" \
  "$api" >/dev/null
