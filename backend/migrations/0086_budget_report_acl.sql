-- 0086_budget_report_acl.sql
-- A BUDGET IS WHAT THE OWNER INTENDS. Without this file, every seeded Data Entry
-- operator can read it the moment the report exists.
--
-- Migration number from PLAN-TALLY-PARITY.md §2.5, which reserves 0086 for T11's
-- ACL. 0085 carries the tables, which have no ordering hazard because nothing
-- replaces them.
--
-- ===========================================================================
-- WHY THIS FILE HAS TO EXIST AT ALL: THE GRANT IS THE DEFAULT
-- ===========================================================================
-- Migration 0011 (line 249) seeds the Data Entry level a COARSE
-- `allow display report`, and internal/authz matches that BARE resource against
-- ANY `report:NAME`. So a new report is GRANTED to Data Entry the moment its name
-- is added to authz.KnownReportNames -- THE GRANT IS THE DEFAULT AND THE REFUSAL
-- IS THE THING THAT MUST BE WRITTEN. That is registered defect D95, and nine
-- migrations now write the refusal for a comparable report: 0026 (Stock Summary),
-- 0028, 0033, 0035, 0046, 0063 (the imported bank statement), 0068 (the TDS/TCS
-- register), 0072 (the cost reports), 0073 (both order books) and 0076 (the
-- forecast plane).
--
-- ⭐ CONTRAST, BECAUSE THE CONTRAST IS THE DESIGN RULE: T11's MASTER token is
-- `budget`, a BARE token, and it takes NO ROWS HERE. The seeded Data Entry level
-- has default_access = 'none' and every rule it carries names a specific token,
-- so a token never mentioned is denied with nothing written. tds-challan,
-- tds-profile, cost-centre, sales-order, purchase-order and bom all shipped that
-- way with zero ACL rows. ⛔ DO NOT ADD RULES FOR `budget` -- adding them is how a
-- later reader concludes that bare tokens need rules, and starts writing them.
-- THE SHAPE OF THE NAME DECIDES THE DEFAULT, and only `report:NAME` arrives
-- pre-granted.
--
-- ===========================================================================
-- ⚠️ THE SPECIFICITY NUMBERS IN THE OLDER HEADERS ARE OFF BY ONE. 0076 FIXED IT.
-- ===========================================================================
-- Migrations 0026 through 0073 say the coarse rule matches "at specificity 1".
-- internal/authz/authz.go's ruleSpecificity does not say that, and 0076's header
-- records the correction after reading the code. Specificity is the SUM of two
-- terms:
--
--     +1 when the rule's ACTION matches exactly (matchAction, not a wildcard)
--     +0 / +1 / +2 for the RESOURCE (wildcard / exact `report` / exact `report:NAME`)
--
-- so `allow display report` scores **2** and `disallow display
-- report:budget-variance` scores **3**. The CONCLUSION those headers draw is
-- right -- `report:NAME` outranks a bare `report`, and Disallow beats Allow at
-- equal specificity, so the rows below win -- but the number is wrong in eight
-- files because each was derived from the last. ⛔ Do not propagate it into a
-- tenth.
--
-- ===========================================================================
-- WHAT THIS REPORT ACTUALLY EXPOSES, since that is the whole justification
-- ===========================================================================
-- report:budget-variance is not one more view of the books. It is the only report
-- in this product that discloses something no voucher contains: WHAT THE OWNER
-- PLANNED. Three things fall out of one read, and none of them is derivable from
-- the ledger a clerk already sees:
--
--   * THE PLAN ITSELF -- what the business intends to spend and earn, by
--     department, for the year ahead. That is forward-looking commercial
--     strategy. The order books (0073) leak what a company is about to do; this
--     leaks what it MEANS to do, including the parts it has not acted on.
--   * WHERE IT IS FAILING -- every overspent line, named, with the figure. The
--     cost reports (0072) show which department is expensive; this shows which
--     department is expensive AGAINST WHAT IT PROMISED, which is the sharper
--     fact and the one that decides headcount.
--   * SALARY AND MARGIN BY INFERENCE. A budget on a payroll group, compared
--     against the actual, is the payroll total -- the disclosure migration 0035
--     refuses at five separate report names. A refusal that can be walked around
--     through a second report is not a refusal.
--
-- A data-entry clerk keying invoices needs none of it.
--
-- ===========================================================================
-- ⚠️ THE POLICY DECISION, AND WHY IT IS RECORDED HERE AS AN ASSUMPTION
-- ===========================================================================
-- ⛔ THIS ONE WAS *NOT* PUT TO THE OWNER. Unlike 0076 -- where "should a clerk
-- read the cash-flow projection" was asked and answered on 2026-08-23 -- the
-- question "should a data-entry clerk see a budget-variance report" is on the
-- owner's ask list and is UNANSWERED at the time this file was written.
--
-- IT PROCEEDS ON 0072'S PRECEDENT: DENY, NO ALLOW SEEDED. 0072 made exactly this
-- call for the cost reports, deliberately, and this report is a strict superset of
-- that disclosure -- it is the cost report plus the plan it was measured against.
--
-- THE FAILURE DIRECTIONS ARE ASYMMETRIC AND THAT IS THE ARGUMENT: over-denying is
-- a support ticket and one admin-API call to fix; under-denying is a clerk reading
-- the company's margins and strategy, discovered later or never.
--
-- ⭐ IT IS ONE RULE TO REVERSE. If the owner disagrees, a tenant writes
-- `allow display report:budget-variance` through the admin API at specificity 3,
-- which ties with the disallow below -- and Disallow beats Allow at equal
-- specificity, so the DISALLOW ROW MUST BE DELETED rather than out-voted. That is
-- a real difference from a rule that was never seeded, and it is stated here so
-- the reversal is done correctly rather than attempted with an allow that silently
-- loses.
--
-- ===========================================================================
-- ⚠️ THE `print` HALF IS DURABILITY, NOT A FIX, AND SAYING SO IS THE POINT
-- ===========================================================================
-- Data Entry is ALREADY refused the print form today, before this file: 0038 seeds
-- ZERO `allow print report:NAME` rows, 0011's coarse grant is action 'display'
-- only, and the level's default_access is 'none' -- so print of every report is
-- already default-denied. The `disallow print` row below therefore changes NOTHING
-- observable right now.
--
-- It is written anyway, for the reason 0038's own header gives and 0063/0068/0072/
-- 0073/0076 all follow: A REPORT THAT CANNOT BE DISPLAYED BUT *CAN* BE PRINTED IS
-- A LEAK WITH A PAPER TRAIL, and the day anyone seeds a coarse print grant this
-- name must not be swept up in it.
--
-- ⛔ A PROBE MUST NOT REPORT THESE TWO ROWS AS TWO EQUAL WINS. Only the `display`
-- row goes red-to-green. A drive that asserts "Data Entry is 403 on both" passes
-- BEFORE this migration exists and proves nothing about it.
--
-- ⚠️ AND THE OWNER CONTROL ARM CANNOT ASSERT 200 ON THE PRINT PATH. Printing the
-- budget variance is NOT IMPLEMENTED -- handlePrintReport's default arm answers
-- 501, and handler_print_test.go's
-- TestTheUnimplementedReportsAreStillGovernableAndSaySo actively requires that
-- 501. An Owner gets 501 there, never 200. A printed variance statement needs a
-- specified layout, and a blank page that looks like a filed budget is worse than
-- a refusal.
--
-- RE-RUNNABLE. migrations.go applyOne wraps each file in ONE transaction and
-- stamps schema_migrations INSIDE it, so a file that cannot be re-run walls off
-- every later migration.

-- ---------------------------------------------------------------------------
-- ONE definition of the rule set, read by BOTH the per-company seeder and the
-- backfill. Two copies is how a company created next week ends up with a
-- different rule set from one created last month.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION budget_report_acl_grants()
RETURNS TABLE (effect TEXT, action TEXT, resource TEXT) AS $$
    SELECT * FROM (VALUES
        -- NO ALLOW. See the header: the decision is deny, on 0072's precedent,
        -- and it is recorded as an assumption because the owner has not ruled.
        --
        -- BOTH the display and the print form. The `print` row is durability
        -- rather than a behaviour change today -- see the header.
        ('disallow', 'display', 'report:budget-variance'),
        ('disallow', 'print',   'report:budget-variance')
    ) AS t(effect, action, resource);
$$ LANGUAGE sql IMMUTABLE;

-- ---------------------------------------------------------------------------
-- The per-company seeder, called when a company is created.
--
-- ⛔ THE NAME MUST MATCH `seed_data_entry_\w+` OR THE CENSUS CANNOT SEE IT.
-- migrations/seeder_census_test.go regexes exactly that shape to build its list
-- of rule seeders; a function named anything else is invisible to it, and a
-- forgotten PERFORM would then ship silently -- which is the 0071-class defect
-- this whole convention exists to prevent.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION seed_data_entry_budget_report_rules(p_company_id UUID)
RETURNS VOID AS $$
DECLARE
    de_level_id UUID;
    rec         RECORD;
BEGIN
    SELECT id INTO de_level_id
      FROM security_level
     WHERE company_id = p_company_id AND name = 'Data Entry';
    IF de_level_id IS NULL THEN
        RETURN;   -- company has no Data Entry level; nothing to narrow
    END IF;

    FOR rec IN SELECT * FROM budget_report_acl_grants() LOOP
        IF NOT EXISTS (
            SELECT 1 FROM acl_rule
             WHERE security_level_id = de_level_id
               AND effect   = rec.effect
               AND action   = rec.action
               AND resource = rec.resource
        ) THEN
            INSERT INTO acl_rule (id, company_id, security_level_id,
                                  effect, action, resource)
            VALUES (gen_random_uuid(), p_company_id, de_level_id,
                    rec.effect, rec.action, rec.resource);
        END IF;
    END LOOP;
END;
$$ LANGUAGE plpgsql;

GRANT EXECUTE ON FUNCTION budget_report_acl_grants() TO tally_app;
GRANT EXECUTE ON FUNCTION seed_data_entry_budget_report_rules(UUID) TO tally_app;

-- ---------------------------------------------------------------------------
-- 1. BACKFILL. Narrows every company that already exists, reading the SAME
--    single definition of the rule set rather than restating the two rules and
--    risking a third version of them.
-- ---------------------------------------------------------------------------
INSERT INTO acl_rule (id, company_id, security_level_id, effect, action, resource)
SELECT gen_random_uuid(), sl.company_id, sl.id, g.effect, g.action, g.resource
  FROM security_level sl
 CROSS JOIN budget_report_acl_grants() AS g
 WHERE sl.name = 'Data Entry'
   AND NOT EXISTS (
        SELECT 1 FROM acl_rule ar
         WHERE ar.security_level_id = sl.id
           AND ar.effect   = g.effect
           AND ar.action   = g.action
           AND ar.resource = g.resource
   );

-- ---------------------------------------------------------------------------
-- 2. RESTATE THE SEEDER. Derived VERBATIM from 0076_forecast_report_acl.sql
--    (its restatement begins at line 189), which was MEASURED to be the
--    HIGHEST-NUMBERED restatement of seed_company_auth_defaults at the time this
--    file was written:
--
--      for f in backend/migrations/*.sql; do \
--        c=$(awk '/CREATE OR REPLACE FUNCTION seed_company_auth_defaults/,/^\$\$ LANGUAGE plpgsql/' "$f" \
--            | grep -c PERFORM); [ "$c" -gt 0 ] && echo "$(basename $f) -> $c"; done | tail -3
--      # -> 0072 -> 12 · 0073 -> 13 · 0076 -> 14
--
--    ⚠️ COUNT THE FUNCTION BODY, NOT THE FILE. `grep -c PERFORM` over the whole
--    file gives a different number, because migrations also call the seeder for
--    existing companies OUTSIDE the body -- and it is a false-alarm generator on
--    exactly this check.
--
--    FIFTEEN PERFORMs: the fourteen 0076 carries, unchanged and in its order,
--    plus the budget report. Diff this against 0076 and the only differences must
--    be comments and the single new PERFORM at the end.
--
--    ⚠️ 0081-0084 ARE A DELIBERATE GAP -- the T8 multi-currency lane is in flight
--    and working forward from 0080. Leaving gaps rather than renumbering is
--    §2.5's own rule, and it is why this file is 0086 and not 0081.
--
-- ⛔ WHY THE NUMBER MATTERS MORE THAN THE CONTENT. migrations.go applies files in
-- `sort.Strings` order and the last CREATE OR REPLACE wins, so a body installed
-- BELOW 0076 would be overwritten by 0076's on every fresh database -- fourteen
-- PERFORMs, none of them this one -- and the rules above would simply not exist
-- for any new company. That is not hypothetical: it is exactly the defect 0071
-- exists to repair, after 0063 derived its body correctly and took a number five
-- LOWER than 0068. Every company created on a fresh deployment handed its Data
-- Entry level the imported bank statement.
--
-- backend/migrations/seeder_census_test.go guards this STATICALLY, with no
-- database, because the defect lives in apply order and is invisible on any
-- incrementally-migrated developer machine. ⛔ If it fails, do not route around
-- it.
--
-- The signature is byte-identical on purpose: CREATE OR REPLACE cannot change a
-- return type, and a changed argument list creates a SECOND overload instead of
-- replacing, which would make authz/store.go's `seed_company_auth_defaults($1,$2)`
-- ambiguous at company creation rather than at migration time.
--
-- The PERFORMs stay at the very END of the body, after the coarse-seed block and
-- after the company_membership insert. The coarse block is guarded by
-- `IF NOT EXISTS (SELECT 1 FROM acl_rule WHERE security_level_id = de_level_id)`:
-- a PERFORM hoisted above it would insert the level's first rule, the guard would
-- then see rows, and the six voucher classes plus `allow display report` would
-- SILENTLY NEVER BE SEEDED -- every new tenant's Data Entry level unable to key an
-- invoice, with no migration test to catch it.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION seed_company_auth_defaults(
    p_company_id UUID,
    p_user_id    UUID
)
RETURNS uuid
LANGUAGE plpgsql
AS $$
DECLARE
    owner_level_id UUID;
    de_level_id    UUID;
    cls            TEXT;
BEGIN
    INSERT INTO security_level (id, company_id, name, is_owner, default_access, requires_webauthn)
    VALUES (gen_random_uuid(), p_company_id, 'Owner', true, 'full', true)
    ON CONFLICT (company_id, name) DO NOTHING;

    SELECT id INTO owner_level_id
      FROM security_level
     WHERE company_id = p_company_id AND name = 'Owner';

    INSERT INTO security_level (id, company_id, name, is_owner, default_access, requires_webauthn)
    VALUES (gen_random_uuid(), p_company_id, 'Data Entry', false, 'none', false)
    ON CONFLICT (company_id, name) DO NOTHING;

    SELECT id INTO de_level_id
      FROM security_level
     WHERE company_id = p_company_id AND name = 'Data Entry';

    IF NOT EXISTS (SELECT 1 FROM acl_rule WHERE security_level_id = de_level_id) THEN
        FOREACH cls IN ARRAY ARRAY['sales','purchase','receipt','payment','contra','journal'] LOOP
            INSERT INTO acl_rule (id, company_id, security_level_id, effect, action, resource)
            VALUES
                (gen_random_uuid(), p_company_id, de_level_id, 'allow', 'create',  cls),
                (gen_random_uuid(), p_company_id, de_level_id, 'allow', 'alter',   cls),
                (gen_random_uuid(), p_company_id, de_level_id, 'allow', 'display', cls);
        END LOOP;
        INSERT INTO acl_rule (id, company_id, security_level_id, effect, action, resource)
        VALUES (gen_random_uuid(), p_company_id, de_level_id, 'allow', 'display', 'report');
    END IF;

    INSERT INTO company_membership (user_id, company_id, security_level_id)
    VALUES (p_user_id, p_company_id, owner_level_id)
    ON CONFLICT (user_id, company_id) DO NOTHING;

    -- The note classes (migration 0018/0019).
    PERFORM seed_data_entry_note_rules(p_company_id);
    -- The reads an operator needs to open the product at all (migration 0022).
    PERFORM seed_data_entry_read_rules(p_company_id);
    -- The inventory position report (migration 0026).
    PERFORM seed_data_entry_inventory_rules(p_company_id);
    -- The other four inventory reports (migration 0028).
    PERFORM seed_data_entry_stock_report_rules(p_company_id);
    -- The stock vouchers and the rate card (migration 0031).
    PERFORM seed_data_entry_stock_voucher_rules(p_company_id);
    -- The supplier reconciliation and the IMS inbox (migration 0033).
    PERFORM seed_data_entry_itc_report_rules(p_company_id);
    -- Payroll: the class, the employee master and the five reports (0035).
    PERFORM seed_data_entry_payroll_rules(p_company_id);
    -- PRINT: the six documents an operator may print, and the twelve reports plus
    -- the pay run they may not (migration 0038).
    PERFORM seed_data_entry_print_rules(p_company_id);
    -- BANK RECONCILIATION: the clerical allow and the two refusals of the
    -- statement itself (migration 0046).
    PERFORM seed_data_entry_bank_recon_rules(p_company_id);
    -- THE TDS/TCS REGISTER: two refusals, no allow (migration 0068). It is the
    -- supplier and customer list with amounts.
    PERFORM seed_data_entry_tds_register_rules(p_company_id);
    -- THE IMPORTED BANK STATEMENT: two refusals, no allow (migration 0063). It
    -- is the bank's own file of every counterparty.
    --
    -- ⛔ THIS IS THE LINE 0068 DROPPED AND THE REASON 0071 EXISTS. Keep it, and
    -- keep every line above it, when you derive the next restatement from here.
    PERFORM seed_data_entry_bank_statement_rules(p_company_id);
    -- THE COST REPORTS: four refusals, no allow (migration 0072). They are
    -- departmental profitability -- which part of the business earns and which
    -- loses.
    PERFORM seed_data_entry_cost_report_rules(p_company_id);
    -- THE ORDER BOOK, both directions: four refusals, no allow (migration 0073).
    -- It is every commitment made and received, by counterparty, with quantities
    -- and agreed rates.
    PERFORM seed_data_entry_order_book_rules(p_company_id);
    -- THE FORECAST PLANE: four refusals, no allow (migration 0076). The cash-flow
    -- projection is the forward cash position; the anomaly list is the CONTROL
    -- SURFACE that names, by voucher number, every entry this very level keyed
    -- irregularly.
    PERFORM seed_data_entry_forecast_report_rules(p_company_id);
    -- THE BUDGET VARIANCE REPORT: two refusals, no allow (this migration). It is
    -- the only report here that discloses something no voucher contains -- WHAT
    -- THE OWNER PLANNED -- and, line by line, where the business is failing
    -- against it. ⚠️ The deny is 0072's precedent applied to an unanswered
    -- question; see this file's header before reversing it.
    PERFORM seed_data_entry_budget_report_rules(p_company_id);

    RETURN owner_level_id;
END
$$;

GRANT EXECUTE ON FUNCTION seed_company_auth_defaults(UUID, UUID) TO tally_app;
