\set ON_ERROR_STOP on

-- Temporary one-time SQL helper for existing Postgres volumes.
--
-- This file is intentionally verbose and plain SQL so it is easy to audit
-- before running. It is called by `apply_existing_volume_admin.sh`, which
-- injects the role names and passwords via psql `-v` variables.
--
-- High-level behavior:
-- 1. create or update the persistent admin role (`dex_admin`)
-- 2. create or update each application role
-- 3. grant the current broad phase-1 access model on the `public` schema
-- 4. set default privileges so future tables/sequences created by
--    `dex_unified_api` are also usable by the other app roles
--
-- This script does not drop data, recreate the database, or remove existing
-- tables. It is designed to be safe to rerun.

-- Ensure the long-lived admin user exists.
-- If the role does not exist yet, create it with superuser-level privileges so
-- it can be used later from DBeaver or another DB client for manual admin work.
SELECT format(
  'CREATE ROLE %I WITH LOGIN SUPERUSER CREATEDB CREATEROLE PASSWORD %L',
  :'admin_user',
  :'admin_password'
)
WHERE NOT EXISTS (
  SELECT 1 FROM pg_roles WHERE rolname = :'admin_user'
)
\gexec

-- Always align the admin role's password and privileges with the current root
-- `.env`, even if the role already existed from a previous run.
SELECT format(
  'ALTER ROLE %I WITH LOGIN SUPERUSER CREATEDB CREATEROLE PASSWORD %L',
  :'admin_user',
  :'admin_password'
)
\gexec

-- Ensure `dex_unified_api` exists and has the password currently stored in the
-- root `.env`.
SELECT format(
  'CREATE ROLE %I WITH LOGIN PASSWORD %L',
  'dex_unified_api',
  :'unified_password'
)
WHERE NOT EXISTS (
  SELECT 1 FROM pg_roles WHERE rolname = 'dex_unified_api'
)
\gexec

SELECT format(
  'ALTER ROLE %I WITH LOGIN PASSWORD %L',
  'dex_unified_api',
  :'unified_password'
)
\gexec

-- Same pattern for `dex_monitor`.
SELECT format(
  'CREATE ROLE %I WITH LOGIN PASSWORD %L',
  'dex_monitor',
  :'monitor_password'
)
WHERE NOT EXISTS (
  SELECT 1 FROM pg_roles WHERE rolname = 'dex_monitor'
)
\gexec

SELECT format(
  'ALTER ROLE %I WITH LOGIN PASSWORD %L',
  'dex_monitor',
  :'monitor_password'
)
\gexec

-- Same pattern for `dex_mm_controller`.
SELECT format(
  'CREATE ROLE %I WITH LOGIN PASSWORD %L',
  'dex_mm_controller',
  :'mm_password'
)
WHERE NOT EXISTS (
  SELECT 1 FROM pg_roles WHERE rolname = 'dex_mm_controller'
)
\gexec

SELECT format(
  'ALTER ROLE %I WITH LOGIN PASSWORD %L',
  'dex_mm_controller',
  :'mm_password'
)
\gexec

-- Same pattern for `dex_fiat_controller`.
SELECT format(
  'CREATE ROLE %I WITH LOGIN PASSWORD %L',
  'dex_fiat_controller',
  :'fiat_password'
)
WHERE NOT EXISTS (
  SELECT 1 FROM pg_roles WHERE rolname = 'dex_fiat_controller'
)
\gexec

SELECT format(
  'ALTER ROLE %I WITH LOGIN PASSWORD %L',
  'dex_fiat_controller',
  :'fiat_password'
)
\gexec

-- Allow all of the known roles to connect to the project database.
-- This does not grant table access by itself; the grants below handle that.
SELECT format(
  'GRANT CONNECT ON DATABASE %I TO %I, %I, %I, %I, %I',
  :'db_name',
  :'admin_user',
  'dex_unified_api',
  'dex_monitor',
  'dex_mm_controller',
  'dex_fiat_controller'
)
\gexec

-- Phase-1 access model:
-- every application role can use the `public` schema and can read/write every
-- existing table there. This is intentionally broad for now and can be
-- tightened later once service ownership boundaries are stable.
GRANT USAGE ON SCHEMA public TO dex_unified_api, dex_monitor, dex_mm_controller, dex_fiat_controller;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO dex_unified_api, dex_monitor, dex_mm_controller, dex_fiat_controller;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO dex_unified_api, dex_monitor, dex_mm_controller, dex_fiat_controller;

-- Default privileges are forward-looking:
-- when `dex_unified_api` creates future tables in `public`, the other app roles
-- should immediately be able to use them without needing a manual GRANT step.
ALTER DEFAULT PRIVILEGES FOR ROLE dex_unified_api IN SCHEMA public
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO dex_unified_api, dex_monitor, dex_mm_controller, dex_fiat_controller;

ALTER DEFAULT PRIVILEGES FOR ROLE dex_unified_api IN SCHEMA public
GRANT USAGE, SELECT ON SEQUENCES TO dex_unified_api, dex_monitor, dex_mm_controller, dex_fiat_controller;
