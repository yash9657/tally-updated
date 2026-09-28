-- 0097_agent_ro_and_published_views.sql
-- W3 TIER B (PLAN-AGENTIC §2.3, §3 W3): "Tier B only over security_barrier
-- views under agent_ro with AST allow-list, READ ONLY, 5 s timeout, forced
-- LIMIT." This file is the DATABASE half — the role, the schema and the
-- views. internal/tierb is the validator half; either must hold on its own,
-- and internal/tierb's DB test runs raw, UNVALIDATED SQL as agent_ro to prove
-- this half does.
--
-- ===========================================================================
-- ⛔ THE THREE ROLES AND WHY THERE ARE THREE
-- ===========================================================================
-- agent_ro          NOLOGIN, NOBYPASSRLS. Reached ONLY by `SET LOCAL ROLE
--                   agent_ro` inside a tally_app transaction that has
--                   already set the tenant GUC. Holds USAGE on schema agent
--                   and SELECT on the agent.* views — and NOTHING on any base
--                   table. A Tier-B query that named `ledger` would be refused
--                   by the validator and, were the validator broken, by
--                   Postgres ("permission denied for table ledger").
-- agent_view_owner  NOLOGIN, NOBYPASSRLS. OWNS the views and holds SELECT on
--                   the base tables they read. A view (security_invoker =
--                   false, the default) is checked with its OWNER's
--                   privileges, and RLS on the base tables is evaluated for
--                   the owner too — so the owner must NOT be the superuser
--                   that runs migrations (a superuser bypasses RLS, and every
--                   view would leak every tenant). FORCE RLS on every base
--                   table means even an owner of the TABLE would be scoped;
--                   agent_view_owner owns only the views. The tenant GUC is a
--                   session setting, so the owner's policy check reads the
--                   caller's app.current_company_id: one company, always.
-- tally_app         is GRANTed agent_ro WITH INHERIT FALSE, SET TRUE: it may
--                   switch INTO agent_ro, and does not silently gain the
--                   views' privileges in its own sessions.
--
-- ⛔ WHY security_barrier. The views carry the current-version / not-deleted
-- guard in their WHERE; security_barrier stops the planner pushing a
-- caller-supplied (possibly leaky) predicate below it, so a function the
-- caller controls can never observe a row the guard would have removed. The
-- validator's function allow-list already refuses every user-defined
-- function; this is the database refusing it as well.
--
-- ⛔ WHAT THE VIEWS OMIT is listed, with reasons, in internal/tierb/views.go
-- (company_id, payload JSON, narrations, bank descriptions, PAN / account
-- numbers, payroll). The column lists here and there are pinned equal by
-- tierb's views test, as agent_ro, from information_schema.
--
-- ===========================================================================
-- NO ACL RULE, NO SEEDER RESTATEMENT
-- ===========================================================================
-- No route reaches Tier B in W3 (W2's agent loop will call internal/tierb
-- under the agent principal). The seeder census stays at 17 PERFORMs.
--
-- ⚠️ ROLES ARE CLUSTER-WIDE. CREATE ROLE here affects every database on the
-- cluster, which is why both are guarded by IF NOT EXISTS and carry no
-- password: they cannot log in, anywhere.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'agent_ro') THEN
        CREATE ROLE agent_ro NOLOGIN NOBYPASSRLS NOINHERIT;
    ELSE
        ALTER ROLE agent_ro NOLOGIN NOBYPASSRLS NOINHERIT;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'agent_view_owner') THEN
        CREATE ROLE agent_view_owner NOLOGIN NOBYPASSRLS;
    ELSE
        ALTER ROLE agent_view_owner NOLOGIN NOBYPASSRLS;
    END IF;
END
$$;

GRANT agent_ro TO tally_app WITH INHERIT FALSE, SET TRUE;

CREATE SCHEMA IF NOT EXISTS agent;
REVOKE ALL ON SCHEMA agent FROM PUBLIC;
GRANT USAGE ON SCHEMA agent TO agent_ro;
GRANT USAGE ON SCHEMA agent TO agent_view_owner;
-- agent_view_owner must create nothing: the views are created by this
-- migration and handed over below.
GRANT USAGE ON SCHEMA public TO agent_view_owner;

-- The base tables the views read, and nothing else.
GRANT SELECT ON ledger, account_group, ledger_balance, voucher_version, voucher_head,
                voucher_type, posting, stock_movement, stock_item, godown, stock_batch,
                bank_statement_line, bank_statement_match, bill_allocation,
                ledger_opening_bill, entity_facts
   TO agent_view_owner;
GRANT EXECUTE ON FUNCTION app_current_company_id() TO agent_view_owner, agent_ro;

-- ---------------------------------------------------------------------------
-- The views. DROP + CREATE (not CREATE OR REPLACE) so a column removed here
-- is really removed on a re-run, and so the owner hand-off is re-asserted.
-- ---------------------------------------------------------------------------
DROP VIEW IF EXISTS agent.ledgers, agent.vouchers, agent.postings, agent.stock_movements,
                    agent.bank_lines, agent.bills, agent.items, agent.facts;

CREATE VIEW agent.ledgers WITH (security_barrier = true) AS
SELECT l.id                                  AS ledger_id,
       l.name                                AS ledger_name,
       g.name                                AS group_name,
       g.nature::text                        AS nature,
       CASE WHEN l.opening_dc = 'D' THEN l.opening_balance ELSE -l.opening_balance END AS opening_signed,
       COALESCE(lb.debit, 0)                 AS debit,
       COALESCE(lb.credit, 0)                AS credit,
       (CASE WHEN l.opening_dc = 'D' THEN l.opening_balance ELSE -l.opening_balance END)
         + COALESCE(lb.debit, 0) - COALESCE(lb.credit, 0) AS closing_net
  FROM ledger l
  JOIN account_group g ON g.id = l.group_id
  LEFT JOIN ledger_balance lb ON lb.ledger_id = l.id;

CREATE VIEW agent.vouchers WITH (security_barrier = true) AS
SELECT vv.voucher_id,
       vv.voucher_date,
       vv.voucher_no,
       vt.name                               AS voucher_type,
       COALESCE(vv.payload->>'class', '')    AS voucher_class,
       party.name                            AS party_name,
       vv.taxable_value, vv.cgst, vv.sgst, vv.igst
  FROM voucher_version vv
  JOIN voucher_head vh
    ON vh.company_id = vv.company_id AND vh.voucher_id = vv.voucher_id AND vh.current_version_id = vv.id
  LEFT JOIN voucher_type vt ON vt.id = vv.voucher_type_id
  LEFT JOIN ledger party ON party.company_id = vv.company_id AND party.id = vv.party_ledger_id
 WHERE vh.is_deleted = false;

CREATE VIEW agent.postings WITH (security_barrier = true) AS
SELECT vv.voucher_id,
       vv.voucher_date,
       vt.name                               AS voucher_type,
       COALESCE(vv.payload->>'class', '')    AS voucher_class,
       l.name                                AS ledger_name,
       g.name                                AS group_name,
       g.nature::text                        AS nature,
       p.dr_cr::text                         AS dr_cr,
       p.amount,
       CASE WHEN g.nature::text IN ('ASSET', 'EXPENSE')
            THEN (CASE WHEN p.dr_cr = 'D' THEN p.amount ELSE -p.amount END)
            ELSE (CASE WHEN p.dr_cr = 'C' THEN p.amount ELSE -p.amount END) END AS natural_net
  FROM posting p
  JOIN voucher_version vv ON vv.id = p.voucher_version_id
  JOIN voucher_head vh
    ON vh.company_id = vv.company_id AND vh.voucher_id = vv.voucher_id AND vh.current_version_id = vv.id
  LEFT JOIN voucher_type vt ON vt.id = vv.voucher_type_id
  JOIN ledger l ON l.id = p.ledger_id
  JOIN account_group g ON g.id = l.group_id
 WHERE vh.is_deleted = false;

CREATE VIEW agent.stock_movements WITH (security_barrier = true) AS
SELECT vv.voucher_id,
       vv.voucher_date,
       vt.name                               AS voucher_type,
       party.name                            AS party_name,
       si.name                               AS item_name,
       gd.name                               AS godown_name,
       sb.name                               AS batch_name,
       sm.direction::text                    AS direction,
       sm.qty, sm.rate, sm.amount
  FROM stock_movement sm
  JOIN voucher_version vv ON vv.id = sm.voucher_version_id
  JOIN voucher_head vh
    ON vh.company_id = vv.company_id AND vh.voucher_id = vv.voucher_id AND vh.current_version_id = vv.id
  JOIN stock_item si ON si.id = sm.stock_item_id
  LEFT JOIN godown gd ON gd.id = sm.godown_id
  LEFT JOIN stock_batch sb ON sb.id = sm.batch_id
  LEFT JOIN voucher_type vt ON vt.id = vv.voucher_type_id
  LEFT JOIN ledger party ON party.company_id = vv.company_id AND party.id = vv.party_ledger_id
 WHERE vh.is_deleted = false;

-- NO description column, on purpose (third-party text; see tierb/views.go).
CREATE VIEW agent.bank_lines WITH (security_barrier = true) AS
SELECT bl.id                                 AS line_id,
       l.name                                AS bank_ledger_name,
       bl.value_date,
       bl.dr_cr::text                        AS dr_cr,
       bl.amount,
       COALESCE(bm.status, 'unresolved')     AS match_status
  FROM bank_statement_line bl
  JOIN ledger l ON l.id = bl.ledger_id
  LEFT JOIN bank_statement_match bm ON bm.company_id = bl.company_id AND bm.line_id = bl.id;

CREATE VIEW agent.bills WITH (security_barrier = true) AS
SELECT l.name AS ledger_name, g.name AS group_name, g.nature::text AS nature,
       b.ref_name, b.ref_type::text AS ref_type, b.dr_cr::text AS dr_cr, b.amount, b.due_date,
       vv.voucher_date AS bill_date, 'voucher'::text AS source
  FROM bill_allocation b
  JOIN voucher_version vv ON vv.id = b.voucher_version_id
  JOIN voucher_head vh
    ON vh.company_id = vv.company_id AND vh.voucher_id = vv.voucher_id AND vh.current_version_id = vv.id
  JOIN ledger l ON l.id = b.ledger_id
  JOIN account_group g ON g.id = l.group_id
 WHERE vh.is_deleted = false
UNION ALL
SELECT l.name, g.name, g.nature::text, ob.ref_name, ob.ref_type::text, ob.dr_cr::text, ob.amount, ob.due_date,
       NULL::date, 'opening'::text
  FROM ledger_opening_bill ob
  JOIN ledger l ON l.id = ob.ledger_id
  JOIN account_group g ON g.id = l.group_id;

CREATE VIEW agent.items WITH (security_barrier = true) AS
SELECT si.id AS item_id, si.name AS item_name, si.hsn, si.gst_rate_pct
  FROM stock_item si;

CREATE VIEW agent.facts WITH (security_barrier = true) AS
SELECT f.entity_kind, f.entity_name, f.fact_key, f.qualifier_name, f.value_num, f.value_text, f.unit,
       f.valid_from AS as_of
  FROM entity_facts f
 WHERE f.superseded_at IS NULL AND f.valid_to IS NULL;

DO $$
DECLARE v TEXT;
BEGIN
    FOREACH v IN ARRAY ARRAY['ledgers', 'vouchers', 'postings', 'stock_movements',
                             'bank_lines', 'bills', 'items', 'facts'] LOOP
        EXECUTE format('ALTER VIEW agent.%I OWNER TO agent_view_owner', v);
        EXECUTE format('REVOKE ALL ON agent.%I FROM PUBLIC', v);
        EXECUTE format('GRANT SELECT ON agent.%I TO agent_ro', v);
    END LOOP;
END
$$;

COMMENT ON SCHEMA agent IS
    'W3 Tier B: the ONLY relations agent_ro may read. security_barrier views '
    'owned by agent_view_owner (NOBYPASSRLS) over FORCE-RLS base tables.';
