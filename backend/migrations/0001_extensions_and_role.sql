-- 0001_extensions_and_role.sql
-- Bootstrap: extensions and the least-privilege application role.
-- This migration runs as the ADMIN DSN (a superuser/owner). The application
-- itself never connects with these privileges; it connects as tally_app.

-- ltree powers the account_group / cost_centre hierarchical paths used for
-- O(groups) report roll-ups.
CREATE EXTENSION IF NOT EXISTS ltree;

-- pgcrypto is handy for gen_random_uuid in seed data / defaults. We mint
-- UUIDv7 application-side, but having gen_random_uuid available is cheap.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- The application role. LOGIN so cored can authenticate; NOBYPASSRLS so it can
-- NEVER see across tenants even if a query forgets the company GUC (default-deny
-- RLS then yields zero rows). No SUPERUSER, no CREATEDB, no CREATEROLE.
-- HOSTED DATABASES (docs/DEPLOY_VERCEL.md): the password is the session
-- setting tally.app_role_password when the migrator sets it (`cored migrate`
-- does, from the env var named in cmd/cored), else the dev value 'tally_app'. Managed
-- Postgres such as Neon REJECTS the dev value (minimum password entropy), and a
-- known password on an internet-reachable database is a hole anyway. The
-- secret is passed as a session setting, not written here.
DO $$
DECLARE
  pw TEXT := COALESCE(NULLIF(current_setting('tally.app_role_password', true), ''), 'tally_app');
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'tally_app') THEN
    EXECUTE format('CREATE ROLE tally_app LOGIN PASSWORD %L NOBYPASSRLS', pw);
  ELSE
    -- Ensure the role is in the expected state on re-runs.
    EXECUTE format('ALTER ROLE tally_app LOGIN PASSWORD %L NOBYPASSRLS', pw);
  END IF;
END
$$;

-- Allow the app role to use the public schema. Table-level privileges are
-- granted in the schema migration after the tables exist.
GRANT USAGE ON SCHEMA public TO tally_app;
