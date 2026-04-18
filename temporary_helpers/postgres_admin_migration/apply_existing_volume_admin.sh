#!/usr/bin/env bash
# Temporary one-time helper for existing Postgres volumes.
#
# Why this file exists:
# - the project now expects a persistent admin user called `dex_admin`
# - fresh databases get initialized from the current Compose/app flow
# - already-existing Postgres volumes do not automatically gain that new admin
#   user just because the env vars changed
#
# This script bridges that gap. It connects with the old working superuser
# credentials, creates/updates the new admin user, creates/updates the app
# users, and applies the current broad phase-1 grants.
#
# Typical use:
#   ./temporary_helpers/postgres_admin_migration/apply_existing_volume_admin.sh
#
# If the old superuser password is not the default `postgres`, override it:
#   OLD_PGPASSWORD=your_old_password \
#   ./temporary_helpers/postgres_admin_migration/apply_existing_volume_admin.sh
#
# You can also override the old connection target completely:
#   OLD_PGHOST=127.0.0.1
#   OLD_PGPORT=5432
#   OLD_PGUSER=postgres
#   OLD_PGPASSWORD=your_old_password
#
# Requirements:
# - the old Postgres instance must already be running
# - `psql` must be installed on the machine running this script
# - the repository root `.env` must already exist, because this script reads the
#   new generated passwords from there
#
# Safety notes:
# - this script is meant to be rerunnable
# - it does not drop tables or wipe data
# - it only updates roles/passwords/grants
# - once the migration is done and verified, this whole helper folder can be
#   deleted
set -euo pipefail

# Resolve the helper folder and repository root without depending on the
# caller's current working directory.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"

# We intentionally use `psql` rather than hiding SQL inside bash because it
# keeps the one-time migration transparent and easy to inspect.
if ! command -v psql >/dev/null 2>&1; then
  echo "psql is required but was not found in PATH." >&2
  exit 1
fi

# The root `.env` is where Compose-level database passwords now live. This file
# is the source of truth for the new admin password and the per-service user
# passwords that will be applied by the SQL script.
if [[ ! -f "${ROOT_DIR}/.env" ]]; then
  echo "Missing ${ROOT_DIR}/.env" >&2
  exit 1
fi

# Export every variable from the root `.env` so the shell can pass them to psql
# as `-v` parameters below.
set -a
. "${ROOT_DIR}/.env"
set +a

# These are the OLD credentials used only to get into the already-existing
# database. They default to the historical local dev values, but can be
# overridden if your existing database uses different ones.
OLD_PGHOST="${OLD_PGHOST:-127.0.0.1}"
OLD_PGPORT="${OLD_PGPORT:-5432}"
OLD_PGUSER="${OLD_PGUSER:-postgres}"
OLD_PGPASSWORD="${OLD_PGPASSWORD:-postgres}"

# `psql` reads the login password from PGPASSWORD. We set it only for this
# process invocation path.
export PGPASSWORD="${OLD_PGPASSWORD}"

# Run the SQL migration script against the existing database.
#
# Important:
# - the connection itself still uses the old working superuser
# - the `-v ...` parameters inject the NEW target credentials from the root .env
# - the SQL file handles create-if-missing and alter-if-existing behavior
psql \
  "postgresql://${OLD_PGUSER}@${OLD_PGHOST}:${OLD_PGPORT}/${POSTGRES_DB}?sslmode=disable" \
  -v admin_user="${POSTGRES_ADMIN_USER}" \
  -v admin_password="${POSTGRES_ADMIN_PASSWORD}" \
  -v db_name="${POSTGRES_DB}" \
  -v unified_password="${POSTGRES_UNIFIED_API_PASSWORD}" \
  -v monitor_password="${POSTGRES_MONITOR_PASSWORD}" \
  -v mm_password="${POSTGRES_MM_CONTROLLER_PASSWORD}" \
  -v fiat_password="${POSTGRES_FIAT_CONTROLLER_PASSWORD}" \
  -f "${SCRIPT_DIR}/apply_existing_volume_admin.sql"

echo
echo "Applied temporary admin migration helper."
echo "You can now try connecting with ${POSTGRES_ADMIN_USER} to database ${POSTGRES_DB}."
