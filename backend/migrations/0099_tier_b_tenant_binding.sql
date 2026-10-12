-- 0099_tier_b_tenant_binding.sql
-- W3 wave 2: CLOSE THE TIER B GUC RESIDUAL (HANDOFF-AGENTIC-WAVE-1 §8 #13,
-- d122 §5 / §6 item 12). After this file the DATABASE alone keeps raw,
-- UNVALIDATED SQL run through Tier B inside the caller's tenant; the
-- validator (internal/tierb/parse.go) becomes the first wall again, not the
-- only one.
--
-- ===========================================================================
-- ⛔ WHAT 0097 LEFT OPEN — TWO HOLES, NOT ONE (measured before this file)
-- ===========================================================================
-- 1. GUC TAMPER (the pinned residual). RLS on every base table reads
--    app.current_company_id, a placeholder GUC that ANY role may SET or
--    set_config. Raw `SELECT set_config('app.current_company_id', '<A>', true)`
--    as agent_ro re-scoped the views to tenant A.
-- 2. ROLE ESCAPE (worse, and not in the wave-1 record). `SET LOCAL ROLE
--    agent_ro` is not a boundary against the SQL that runs after it: SET ROLE
--    is checked against the SESSION user, and the session user is tally_app.
--    Measured on 0097 through tierb.RunRawAsAgent as tenant B:
--      SELECT set_config('role','tally_app',true), current_user
--        -> tally_app
--      SELECT set_config('role','tally_app',true),
--             set_config('app.current_company_id','<A>',true),
--             query_to_xml('select name, company_id from public.ledger',…)::text
--        -> tenant A's ledger names, from the BASE table, narrations and all
--           reachable the same way.
--    So "agent_ro holds SELECT on agent.* only" held only for SQL that did
--    not ask to be someone else.
--
-- ===========================================================================
-- THE DESIGN — two properties the database now enforces on its own
-- ===========================================================================
-- (a) THE UNTRUSTED TEXT RUNS ONLY INSIDE A SECURITY DEFINER FUNCTION.
--     agent.tier_b_run(sql, max_rows) and agent.tier_b_plan(sql) are
--     SECURITY DEFINER, owned by agent_ro, and EXECUTE the caller's text with
--     `FOR r IN EXECUTE` (tier_b_plan too, since the wave-2 review: it
--     used `EXECUTE … INTO`, which runs every statement of a multi-statement
--     string — see its body). Inside a security-definer function Postgres refuses
--     to change `role` or `session_authorization` at all ("cannot set
--     parameter "role" within security-definer function") — by SET, RESET,
--     set_config, or a function's SET clause — so the text cannot leave
--     agent_ro. `FOR … IN EXECUTE` opens a cursor, so SET / RESET / DO /
--     multi-statement text is refused before it runs ("cannot open SET query
--     as cursor"). internal/tierb never sends model or caller text to
--     Postgres any other way: it is the $1 of these two functions (and a
--     Parse/Describe, which executes nothing — see tierb/exec.go).
--     ⚠️ WHAT THE DATABASE ALONE DOES NOT CLOSE (the wave-2 review): the
--     text runs with search_path = pg_catalog and agent_ro may read the
--     cluster-wide statistics views, so `SELECT relname, n_tup_ins FROM
--     pg_stat_user_tables` returns insert counts for ledger / posting /
--     voucher_version across EVERY tenant — an activity side channel, no rows.
--     Revoking PUBLIC's SELECT on the pg_stat_* views is cluster-wide and is
--     not attempted here; the validator (views-only grammar) is the wall.
-- (b) THE TENANT IS BOUND BY SOMETHING agent_ro CANNOT WRITE.
--     agent.tier_b_binding holds (backend pid, transaction id) -> company.
--     tally_app writes it through agent.tier_b_bind() at the top of the
--     Tier-B transaction, BEFORE the transaction is switched to READ ONLY and
--     before any untrusted text exists in it; the row is keyed to this
--     backend and this transaction id (xid8, never reused), and the
--     transaction is always rolled back, so it is never visible to another
--     session and never outlives the query. agent_ro holds no privilege on
--     the table and cannot call tier_b_bind(). A RESTRICTIVE policy on every
--     base table the views read, TO agent_view_owner (the only role whose
--     rights the views run with), ANDs `company_id = (SELECT
--     agent.tier_b_company())` onto the existing GUC policy. Re-pointing the
--     GUC now yields (A's rows) ∩ (B's binding) = nothing; unsetting it
--     yields nothing; and an unbound transaction yields nothing (NULL), and
--     tier_b_run refuses to start in one at all, so a wiring mistake is loud.
--
-- WHY NOT THE OTHER OPTIONS (d122 "Wave 2" records them in full):
--   - REVOKE EXECUTE ON set_config FROM PUBLIC: `SET` and `RESET` are
--     statements, not functions, and stay open; and set_config is used by
--     every app transaction. It closes neither hole.
--   - GRANT SET ON PARAMETER: only restricts SUSET parameters; a placeholder
--     GUC like app.current_company_id is USERSET for everyone.
--   - A LOGIN role for Tier B (session user = agent_ro, so no role escape):
--     a new credential to provision and rotate, a second pool, and it still
--     needs (b). (a) gets the same property with no credential.
--   - Making app_current_company_id() itself honour a binding: every RLS
--     check in the product would pay a table lookup. The restrictive policy
--     is scoped TO agent_view_owner, so no other role's plans change.
--
-- ===========================================================================
-- ALSO CLOSED HERE: SECURITY DEFINER functions callable by PUBLIC
-- ===========================================================================
-- A security-definer function owned by the superuser runs without RLS, so
-- any one EXECUTE-able by agent_ro is a door around both walls. 0074 created
-- auth_session_state() and auth_admin_clear_lock() without the REVOKE FROM
-- PUBLIC that 0023/0092/0094 remember: agent_ro (and every other role) could
-- read any user's lock state by subject, and use clear_lock's boolean as a
-- company-membership oracle. Both keep their explicit tally_app grants.
-- tierb's DB test pins the census: the only security-definer functions
-- agent_ro may execute are this file's.
--
-- NO ACL RULE, NO SEEDER RESTATEMENT (census stays 17 PERFORMs). Roles are
-- cluster-wide, hence IF NOT EXISTS. Idempotent: every object is re-asserted.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'agent_binder') THEN
        CREATE ROLE agent_binder NOLOGIN NOBYPASSRLS NOINHERIT;
    ELSE
        ALTER ROLE agent_binder NOLOGIN NOBYPASSRLS NOINHERIT;
    END IF;
END
$$;

GRANT USAGE ON SCHEMA agent TO agent_binder;
-- tally_app calls agent.tier_b_bind() and nothing else in the schema: USAGE
-- lets it resolve the name; it holds no privilege on any view or table here.
GRANT USAGE ON SCHEMA agent TO tally_app;
GRANT EXECUTE ON FUNCTION app_current_company_id() TO agent_binder;
-- ⚠️ A NON-SUPERUSER MIGRATOR (managed Postgres, e.g. Neon's neondb_owner):
-- ALTER ... OWNER TO requires the NEW owner to hold CREATE on the schema, a
-- check superusers skip. Granted for the hand-overs below and revoked right
-- after the last one, inside this migration's transaction, so the committed
-- privileges equal a superuser migrator's. Same as 0097. (docs/DEPLOY_VERCEL.md)
GRANT CREATE ON SCHEMA agent TO agent_binder, agent_ro;

-- ---------------------------------------------------------------------------
-- The binding. One row per Tier-B transaction; never committed in practice.
-- The PRIMARY KEY (backend_pid, xact_id) is the uniqueness that matters —
-- one company per transaction, so a second bind() in the same transaction
-- (to another tenant) is a duplicate-key error, not a re-bind. A
-- (company_id, …) UNIQUE would add nothing: the key already names exactly
-- one row.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS agent.tier_b_binding (
    backend_pid INT         NOT NULL,
    xact_id     XID8        NOT NULL,
    company_id  UUID        NOT NULL,
    bound_at    TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT tier_b_binding_pkey PRIMARY KEY (backend_pid, xact_id)
);
ALTER TABLE agent.tier_b_binding OWNER TO agent_binder;
REVOKE ALL ON agent.tier_b_binding FROM PUBLIC;
GRANT SELECT, INSERT ON agent.tier_b_binding TO agent_binder;

-- FORCE, because the owner is the role that reads it: even agent_binder sees
-- only its own backend's current transaction, and may only write a row for
-- the company the GUC names at that moment.
ALTER TABLE agent.tier_b_binding ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent.tier_b_binding FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tier_b_binding_own_transaction ON agent.tier_b_binding;
CREATE POLICY tier_b_binding_own_transaction ON agent.tier_b_binding
    TO agent_binder
    USING (backend_pid = pg_backend_pid() AND xact_id = pg_current_xact_id_if_assigned())
    WITH CHECK (backend_pid = pg_backend_pid()
                AND xact_id = pg_current_xact_id()
                AND company_id = app_current_company_id());

-- tier_b_bind(): called by tally_app with the tenant GUC already set by
-- db.RunInTx. Binds THAT company — it takes no argument, so it cannot be
-- told to bind anything the caller's own transaction is not already scoped
-- to. Refuses a read-only transaction (it would fail on the INSERT anyway;
-- this says why).
CREATE OR REPLACE FUNCTION agent.tier_b_bind() RETURNS UUID
    LANGUAGE plpgsql VOLATILE SECURITY DEFINER
    SET search_path = pg_catalog
AS $$
DECLARE
    c UUID := public.app_current_company_id();
BEGIN
    IF c IS NULL THEN
        RAISE EXCEPTION 'tier_b_bind: no tenant in scope (app.current_company_id unset)'
            USING ERRCODE = '42501';
    END IF;
    IF current_setting('transaction_read_only') = 'on' THEN
        RAISE EXCEPTION 'tier_b_bind: must run before the transaction is made READ ONLY'
            USING ERRCODE = '25006';
    END IF;
    INSERT INTO agent.tier_b_binding (backend_pid, xact_id, company_id)
    VALUES (pg_backend_pid(), pg_current_xact_id(), c);
    RETURN c;
END
$$;
ALTER FUNCTION agent.tier_b_bind() OWNER TO agent_binder;
REVOKE ALL ON FUNCTION agent.tier_b_bind() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent.tier_b_bind() TO tally_app;

-- tier_b_company(): this backend's, this transaction's binding, or NULL.
-- STABLE, and the policies call it as a scalar subquery (an InitPlan), so it
-- is evaluated once per statement, not once per row.
CREATE OR REPLACE FUNCTION agent.tier_b_company() RETURNS UUID
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path = pg_catalog
AS $$
    SELECT b.company_id FROM agent.tier_b_binding b
     WHERE b.backend_pid = pg_backend_pid()
       AND b.xact_id = pg_current_xact_id_if_assigned()
$$;
ALTER FUNCTION agent.tier_b_company() OWNER TO agent_binder;
REVOKE ALL ON FUNCTION agent.tier_b_company() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent.tier_b_company() TO agent_view_owner, agent_ro;

-- ---------------------------------------------------------------------------
-- The restrictive policies: every base table 0097 granted to
-- agent_view_owner. RESTRICTIVE ANDs with the permissive tenant_isolation;
-- TO agent_view_owner keeps every other role's plans exactly as they were.
-- ---------------------------------------------------------------------------
DO $$
DECLARE t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['ledger', 'account_group', 'ledger_balance', 'voucher_version',
                             'voucher_head', 'voucher_type', 'posting', 'stock_movement',
                             'stock_item', 'godown', 'stock_batch', 'bank_statement_line',
                             'bank_statement_match', 'bill_allocation', 'ledger_opening_bill',
                             'entity_facts'] LOOP
        EXECUTE format('DROP POLICY IF EXISTS tier_b_bound ON public.%I', t);
        EXECUTE format('CREATE POLICY tier_b_bound ON public.%I AS RESTRICTIVE FOR SELECT '
                       'TO agent_view_owner USING (company_id = (SELECT agent.tier_b_company()))', t);
    END LOOP;
END
$$;

-- ---------------------------------------------------------------------------
-- The two doors untrusted text may pass through. Owned by agent_ro, so the
-- text runs with agent_ro's rights and nothing more, inside a security-
-- definer frame, so it cannot change role.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION agent.tier_b_run(p_sql TEXT, p_max_rows INT) RETURNS SETOF JSON
    LANGUAGE plpgsql VOLATILE SECURITY DEFINER
    SET search_path = pg_catalog
AS $$
DECLARE
    r RECORD;
    n INT := 0;
BEGIN
    -- Loud, not empty: an unbound or writable transaction is a wiring bug in
    -- the caller, and "no rows" would read as "no data" to the model.
    IF agent.tier_b_company() IS NULL THEN
        RAISE EXCEPTION 'tier_b_run: no tenant binding in this transaction (call agent.tier_b_bind() first)'
            USING ERRCODE = '42501';
    END IF;
    IF current_setting('transaction_read_only') <> 'on' THEN
        RAISE EXCEPTION 'tier_b_run: the transaction must be READ ONLY'
            USING ERRCODE = '25006';
    END IF;
    FOR r IN EXECUTE p_sql LOOP
        n := n + 1;
        EXIT WHEN n > p_max_rows;
        RETURN NEXT row_to_json(r);
    END LOOP;
    RETURN;
END
$$;

CREATE OR REPLACE FUNCTION agent.tier_b_plan(p_sql TEXT) RETURNS JSON
    LANGUAGE plpgsql VOLATILE SECURITY DEFINER
    SET search_path = pg_catalog
AS $$
DECLARE
    r RECORD;
BEGIN
    IF agent.tier_b_company() IS NULL THEN
        RAISE EXCEPTION 'tier_b_plan: no tenant binding in this transaction'
            USING ERRCODE = '42501';
    END IF;
    IF current_setting('transaction_read_only') <> 'on' THEN
        RAISE EXCEPTION 'tier_b_plan: the transaction must be READ ONLY'
            USING ERRCODE = '25006';
    END IF;
    -- ⛔ A CURSOR, LIKE tier_b_run — NOT `EXECUTE … INTO` (the wave-2 review).
    -- EXECUTE … INTO runs EVERY statement of a multi-statement string: measured,
    -- 'SELECT 1; PREPARE planted AS SELECT 42; DO $d$…$d$; SET …; SELECT …'
    -- ran the DO as agent_ro, re-pointed the GUC, and left 'planted' prepared
    -- on the pooled connection after the Tier-B rollback. FOR … IN EXECUTE
    -- opens a cursor, which refuses a multi-query string before any of it runs.
    FOR r IN EXECUTE 'EXPLAIN (FORMAT JSON) ' || p_sql LOOP
        RETURN r."QUERY PLAN";
    END LOOP;
    RETURN NULL;
END
$$;

ALTER FUNCTION agent.tier_b_run(TEXT, INT) OWNER TO agent_ro;
ALTER FUNCTION agent.tier_b_plan(TEXT) OWNER TO agent_ro;
REVOKE CREATE ON SCHEMA agent FROM agent_binder, agent_ro;
REVOKE ALL ON FUNCTION agent.tier_b_run(TEXT, INT) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent.tier_b_plan(TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent.tier_b_run(TEXT, INT) TO agent_ro;
GRANT EXECUTE ON FUNCTION agent.tier_b_plan(TEXT) TO agent_ro;

-- ---------------------------------------------------------------------------
-- 0074's two security-definer functions: PUBLIC loses EXECUTE; tally_app
-- keeps the explicit grant 0074 gave it.
-- ---------------------------------------------------------------------------
REVOKE ALL ON FUNCTION auth_session_state(TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION auth_admin_clear_lock(UUID, UUID, UUID) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION auth_session_state(TEXT) TO tally_app;
GRANT EXECUTE ON FUNCTION auth_admin_clear_lock(UUID, UUID, UUID) TO tally_app;

COMMENT ON TABLE agent.tier_b_binding IS
    'W3 Tier B tenant binding (0099): (backend, transaction) -> company, written by '
    'tally_app via agent.tier_b_bind() before READ ONLY; agent_ro cannot write it. '
    'The restrictive tier_b_bound policies read it for agent_view_owner.';
