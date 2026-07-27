#!/usr/bin/env bash
set -e

export PATH="$HOME/.local/bin:$PATH"

LAST_MSG=$(git log -1 --pretty=%B HEAD)
if [[ "$LAST_MSG" == ".env values update"* ]]; then
  exit 0
fi

if [[ ! -f ".env" ]]; then
  exit 0
fi

PROJECT_ROOT=$(git rev-parse --show-toplevel)
VALIDATOR="$PROJECT_ROOT/scripts/validate-env-contract.sh"
if [[ -x "$VALIDATOR" ]]; then
  "$VALIDATOR" file root values .env
fi

# Replacing .env.enc from a partial local .env silently deletes every omitted
# production secret. Require an explicit override for intentional removals;
# normal additions and value rotations remain automatic.
if [[ -f ".env.enc" && "${ALLOW_ENV_KEY_REMOVAL:-0}" != "1" ]]; then
  mapfile -t REMOVED_KEYS < <(
    comm -23 \
      <(sed -n 's/^\([A-Za-z_][A-Za-z0-9_]*\)=.*/\1/p' .env.enc |
        grep -v '^DOTENV_PUBLIC_KEY_ENC$' | sort -u) \
      <(sed -n 's/^\([A-Za-z_][A-Za-z0-9_]*\)=.*/\1/p' .env | sort -u)
  )
  if ((${#REMOVED_KEYS[@]})); then
    echo "Refusing to remove encrypted environment keys:" >&2
    printf '  %s\n' "${REMOVED_KEYS[@]}" >&2
    echo "Restore them in .env, or set ALLOW_ENV_KEY_REMOVAL=1 for an intentional removal." >&2
    exit 1
  fi
fi

GIT_DIR=$(git rev-parse --git-dir)
HASH_FILE="$GIT_DIR/env.hash"
CURRENT_HASH=$(sha256sum .env | awk '{print $1}')

if [[ -f "$HASH_FILE" ]]; then
  STORED_HASH=$(cat "$HASH_FILE")
  if [[ "$CURRENT_HASH" == "$STORED_HASH" ]]; then
    exit 0  # .env has not changed, skip encryption
  fi
fi

REPO_NAME=$(basename "$(git rev-parse --show-toplevel)")

# Preserve the existing keypair across the cp. Plain `cp .env .env.enc` wipes the
# DOTENV_PUBLIC_KEY_ENC header, so `dotenvx encrypt` would mint a BRAND-NEW keypair
# on every .env change — and the new private key only ever lands in the gitignored
# .env.keys, never reaching the CI secret or other machines, which breaks the deploy
# decrypt step (WRONG_PRIVATE_KEY). Re-inject the committed pubkey so the keypair
# stays stable. (Empty PUB = no prior .env.enc → first-time encrypt mints one, fine.)
PUB=$(grep -oE 'DOTENV_PUBLIC_KEY_ENC="[0-9a-f]+"' .env.enc 2>/dev/null | head -1)
cp .env .env.enc
if [[ -n "$PUB" ]]; then
  printf '%s\n' "$PUB" | cat - .env.enc > .env.enc.tmp && mv .env.enc.tmp .env.enc
fi
dotenvx encrypt -f .env.enc

echo "$CURRENT_HASH" > "$HASH_FILE"

git add .env.enc
git commit -m ".env values update $REPO_NAME"
