#!/bin/sh
set -eu

: "${MADDY_RECIPIENT:?MADDY_RECIPIENT is required}"
: "${MADDY_IMAP_PASSWORD:?MADDY_IMAP_PASSWORD is required}"

if maddy creds list | grep -Fqx "$MADDY_RECIPIENT"; then
    maddy creds password --password "$MADDY_IMAP_PASSWORD" "$MADDY_RECIPIENT"
else
    maddy creds create --password "$MADDY_IMAP_PASSWORD" "$MADDY_RECIPIENT"
fi

if ! maddy imap-acct list | grep -Fqx "$MADDY_RECIPIENT"; then
    maddy imap-acct create "$MADDY_RECIPIENT"
fi
