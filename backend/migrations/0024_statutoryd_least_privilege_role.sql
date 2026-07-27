-- 0024_statutoryd_least_privilege_role.sql
-- A DEDICATED, NARROW DATABASE ROLE for statutoryd.
--
-- THE GAP THIS CLOSES
-- -------------------
-- Migration 0013 gave statutoryd a durable step store so an IRN workflow
-- survives a restart — and to reach it, statutoryd was handed a `tally_app`
-- DSN. That is the SAME role cored uses for the entire ledger. It was disclosed
-- at the time (HANDOFF §6: "a compromised statutoryd could read tenant tables it
-- previously could not reach — a dedicated, narrower DB role is the follow-up")
-- and this is that follow-up.
--
-- statutoryd is a separate deployable whose only legitimate database need is its
-- own workflow state. Everything else it does goes over HTTP to cored, as a
-- service principal, through the PDP — which is the boundary an archtest already
-- enforces in the code. This makes the DATABASE agree with that boundary
-- instead of quietly contradicting it.
--
-- WHY A ROLE AND NOT MORE RLS. `statutory_step` has no company_id to scope by
-- (it is workflow bookkeeping, not tenant data), so RLS has nothing to key on.
-- Least privilege here is therefore grant-shaped: one role, one table, and no
-- USAGE on anything else.
--
-- OPERATIONAL NOTE. The password below matches the dev convention of 0001
-- (role name == password, for a local Postgres that is not reachable from
-- anywhere). A real deployment must ALTER ROLE ... PASSWORD out of band; the
-- migration deliberately does not read a secret from the environment, because a
-- migration file is the wrong place for one to live.

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'tally_statutory') THEN
    CREATE ROLE tally_statutory LOGIN PASSWORD 'tally_statutory' NOBYPASSRLS;
  ELSE
    ALTER ROLE tally_statutory LOGIN PASSWORD 'tally_statutory' NOBYPASSRLS;
  END IF;
END
$$;

-- Schema usage is required to name any object at all; it grants nothing by
-- itself, because table privileges are default-deny.
GRANT USAGE ON SCHEMA public TO tally_statutory;

-- The ONE table this role may touch.
GRANT SELECT, INSERT, UPDATE, DELETE ON statutory_step TO tally_statutory;

-- And nothing else, stated explicitly rather than left to the default. A future
-- `GRANT ... ON ALL TABLES ... TO PUBLIC` (or a table created without REVOKE)
-- would otherwise silently widen this role.
REVOKE ALL ON ALL TABLES IN SCHEMA public FROM tally_statutory;
GRANT SELECT, INSERT, UPDATE, DELETE ON statutory_step TO tally_statutory;

-- Default privileges for tables created LATER: none for this role. Without this
-- a table added by a future migration could become reachable depending on how
-- its grants were written.
ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE ALL ON TABLES FROM tally_statutory;

COMMENT ON ROLE tally_statutory IS
    'statutoryd''s least-privilege DB role: statutory_step ONLY. Everything else '
    'statutoryd needs goes over HTTP to cored as a service principal, through '
    'the PDP. Use it via STATUTORYD_DSN.';
