# Temporary Postgres Admin Migration Helper

This folder exists only to help migrate an already-initialized Postgres volume
to the new `dex_admin`-based setup without wiping data.

You can delete this whole folder after:

1. `dex_admin` exists and can log in.
2. the app roles (`dex_unified_api`, `dex_monitor`, `dex_mm_controller`, `dex_fiat_controller`) exist.
3. you have verified you can connect with `dex_admin` from DBeaver or another DB client.
4. the stack starts normally with the current `docker-compose.yml`.

## When To Use It

Use this helper only when all of these are true:

- you already have a persistent Postgres volume with useful data
- that volume was initialized before the current `dex_admin` role setup
- you want to add or repair the admin/app roles without recreating the volume

Do not use it for a fresh install. Fresh databases are initialized by the root
Compose stack and the `dexUnifiedApi` bootstrap flow.

## What It Does

- creates or updates the persistent admin user from the root `.env`
- creates or updates the app users from the same `.env`
- grants broad phase-1 access on `public` tables and sequences

It does not run the full application schema migrations. It only fixes the role
layer needed for the current services to connect.

## Files

- `apply_existing_volume_admin.sh` loads env values, connects to Postgres with
  the old superuser credentials, and applies the SQL script.
- `apply_existing_volume_admin.sql` creates or updates `dex_admin` and the app
  roles, then grants table and sequence privileges.
- `README.md` is this operator note.

## Usage

Use:

```bash
./temporary_helpers/postgres_admin_migration/apply_existing_volume_admin.sh
```

Defaults:

- old superuser login: `postgres`
- old superuser password: `postgres`
- local connection target: `127.0.0.1:5432`

Override if needed:

```bash
OLD_PGUSER=postgres OLD_PGPASSWORD=your_old_password ./temporary_helpers/postgres_admin_migration/apply_existing_volume_admin.sh
```

## Requirements

- the existing Postgres container or server must already be running
- port `5432` must be reachable from your machine
- `psql` must be installed locally
- the root `.env` must contain the current admin/app role passwords

## Notes

- This is only for existing volumes. Fresh databases will initialize from the
  current Compose and app bootstrap flow.
- The helper intentionally mirrors the current broad phase-1 permissions and is
  not meant to be permanent infrastructure.
- Back up the database before applying role changes to a volume you care about.
