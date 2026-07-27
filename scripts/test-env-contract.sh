#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VALIDATOR="$SCRIPT_DIR/validate-env-contract.sh"
FIXTURE="$(mktemp -d)"
GUARD_FIXTURE="$(mktemp -d)"
trap 'rm -rf "$FIXTURE" "$GUARD_FIXTURE"' EXIT

# Build fixtures from the contract arrays without duplicating them here.
contract_keys() {
  local contract="$1"
  sed -n "/^${contract^^}_REQUIRED=(/,/^)/p; /^${contract^^}_PRESENT=(/,/^)/p" "$VALIDATOR" |
    sed -n 's/^[[:space:]]*\([A-Z][A-Z0-9_]*\)[[:space:]]*$/\1/p'
}

write_fixture() {
  local mode="$1" suffix contract path key value
  [[ "$mode" == keys ]] && suffix=".env.enc" || suffix=".env"
  while IFS='|' read -r contract path; do
    mkdir -p "$(dirname "$FIXTURE/$path/$suffix")"
    : >"$FIXTURE/$path/$suffix"
    while IFS= read -r key; do
      [[ "$mode" == keys ]] && value="encrypted:test-value" || value="test-value"
      printf '%s=%s\n' "$key" "$value" >>"$FIXTURE/$path/$suffix"
    done < <(contract_keys "$contract")
  done <<'EOF'
ROOT|.
UNIFIED_API|dexUnifiedApi
MONITOR|dexMonitor
MM_CONTROLLER|dexMmController
FIAT_CONTROLLER|dexFiatController
EOF
}

write_fixture keys
"$VALIDATOR" keys "$FIXTURE"

rm -rf "$FIXTURE"
mkdir -p "$FIXTURE"
write_fixture values
"$VALIDATOR" values "$FIXTURE"

# The encryption hook must reject a partial local .env before replacing the
# existing encrypted file.
mkdir -p "$GUARD_FIXTURE/scripts" "$GUARD_FIXTURE/.lefthook/post-commit"
install -m 700 "$VALIDATOR" "$GUARD_FIXTURE/scripts/validate-env-contract.sh"
install -m 700 "$SCRIPT_DIR/../.lefthook/post-commit/encrypt-env.sh" \
  "$GUARD_FIXTURE/.lefthook/post-commit/encrypt-env.sh"
install -m 600 "$FIXTURE/.env" "$GUARD_FIXTURE/.env"
: >"$GUARD_FIXTURE/.env.enc"
while IFS= read -r key; do
  printf '%s=encrypted:test-value\n' "$key" >>"$GUARD_FIXTURE/.env.enc"
done < <(contract_keys ROOT)
printf 'LEGACY_ONLY=encrypted:test-value\n' >>"$GUARD_FIXTURE/.env.enc"
git -C "$GUARD_FIXTURE" init -q
git -C "$GUARD_FIXTURE" config user.name env-contract-test
git -C "$GUARD_FIXTURE" config user.email env-contract-test@example.invalid
touch "$GUARD_FIXTURE/marker"
git -C "$GUARD_FIXTURE" add marker
git -C "$GUARD_FIXTURE" commit -qm initial
before_hash="$(sha256sum "$GUARD_FIXTURE/.env.enc")"
if (cd "$GUARD_FIXTURE" && bash .lefthook/post-commit/encrypt-env.sh >/dev/null 2>&1); then
  echo "expected the encryption hook to reject a removed key" >&2
  exit 1
fi
after_hash="$(sha256sum "$GUARD_FIXTURE/.env.enc")"
[[ "$before_hash" == "$after_hash" ]] || {
  echo "encryption hook modified .env.enc before rejecting the removal" >&2
  exit 1
}

sed -i '/^POSTGRES_MONITOR_PASSWORD=/d' "$FIXTURE/.env"
if "$VALIDATOR" values "$FIXTURE" >/dev/null 2>&1; then
  echo "expected a missing root key to fail validation" >&2
  exit 1
fi

printf 'POSTGRES_MONITOR_PASSWORD=\n' >>"$FIXTURE/.env"
if "$VALIDATOR" values "$FIXTURE" >/dev/null 2>&1; then
  echo "expected an empty root value to fail validation" >&2
  exit 1
fi

echo "environment contract tests passed"
