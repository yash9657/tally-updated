-- 0076_forecast_report_acl.sql
-- THE FORECAST PLANE IS A MANAGEMENT REPORT AND A CONTROL SURFACE. Without this
-- file, every seeded Data Entry operator can read both -- and the anomaly list is
-- the list a SUPERVISOR reads ABOUT THAT OPERATOR.
--
-- Register defect D95, found by building D22's door in L16 and then asking what
-- this repo does for every other sensitive report.
--
-- ===========================================================================
-- WHY THIS FILE HAS TO EXIST AT ALL: THE GRANT IS THE DEFAULT
-- ===========================================================================
-- Migration 0011 seeds the Data Entry level a COARSE `allow display report`, and
-- internal/authz matches that against ANY `report:NAME`. So a new report is
-- GRANTED to Data Entry the moment its name is added to authz.KnownReportNames --
-- the grant is the DEFAULT and the REFUSAL is the thing that must be written.
--
-- `cash-flow` and `anomalies` have been in KnownReportNames (vocabulary.go:457)
-- and served by cored since wave 2. Eight migrations write the refusal for every
-- comparable report -- 0026 (Stock Summary: it prints cost prices), 0028, 0033,
-- 0035, 0063 (the imported bank statement), 0068 (the TDS/TCS register), 0072
-- (the cost reports) and 0073 (both order books) -- and these two had NOTHING.
--
-- ⚠️ MEASURED BEFORE THIS FILE WAS WRITTEN, not inferred from the headers above:
-- on the live dev database the demo tenant's Data Entry level carries 92 acl_rule
-- rows, 19 distinct `report:NAME` tokens with a `disallow`, the coarse
-- `allow display report` row -- and ZERO rows mentioning cash-flow or anomalies.
--
-- ===========================================================================
-- ⛔ THE SPECIFICITY NUMBERS IN THE OTHER HEADERS ARE OFF BY ONE. READ THE CODE.
-- ===========================================================================
-- Several sibling migrations say the coarse rule matches "at specificity 1".
-- internal/authz/authz.go's ruleSpecificity was read directly while writing this
-- file and it does not say that. Specificity is the SUM of two terms:
--
--     +1 when the rule's ACTION matches exactly (matchAction, not a wildcard)
--     +0 / +1 / +2 for the RESOURCE (wildcard / exact `report` / exact `report:NAME`)
--
-- so `allow display report` scores **2** and `disallow display report:cash-flow`
-- scores **3**. The CONCLUSION those headers draw is right -- `report:NAME` outranks
-- a bare `report`, and `Disallow` beats `Allow` at equal specificity, so the rows
-- below win -- but the number is wrong, and it is wrong in eight files because each
-- was derived from the last. Do not propagate it into a ninth.
--
-- ===========================================================================
-- WHAT THESE TWO REPORTS ACTUALLY EXPOSE, since that is the whole justification
-- ===========================================================================
-- report:anomalies is not a report about money. It lists, BY VOUCHER NUMBER, every
-- voucher that was backdated more than 7 days, entered outside 07:00-22:00 IST,
-- edited three or more times inside 24 hours, or written for a taxable value at or
-- above 10,000 that is an exact multiple of 1,000. ⛔ THAT IS A CONTROL SURFACE,
-- AND THE PEOPLE IT MONITORS ARE THE DATA ENTRY OPERATORS. A clerk who can read it
-- can see precisely which of their own irregular entries were flagged, which is
-- most of the way to learning which ones are not. A control visible to its subject
-- is not much of a control.
--
-- report:cash-flow is the forward cash position: what the business expects to
-- collect and pay, week by week, for 90 days. That is a management report of the
-- same kind as the cost statements 0072 refuses.
--
-- ===========================================================================
-- THE USER'S DECISION, RECORDED BECAUSE IT IS AUTHORITY AND NOT A DEFAULT
-- ===========================================================================
-- Asked and answered 2026-08-23: DENY BOTH, NO ALLOW SEEDED. The reasoning the
-- user endorsed, so that a later session does not re-litigate it: a forward cash
-- position is a MANAGEMENT report, and a tenant that wants a senior clerk reading
-- it writes an explicit `allow display report:cash-flow` through the admin API --
-- a decision they make, visible in acl_rule, rather than a default nobody chose.
-- ⛔ So do NOT seed an allow back, and do NOT treat the two tokens differently.
--
-- ===========================================================================
-- ⚠️ THE `print` HALF IS DURABILITY, NOT A FIX, AND SAYING SO IS THE POINT
-- ===========================================================================
-- Data Entry is ALREADY refused both print forms today, before this file: 0038
-- seeds ZERO `allow print report:NAME` rows, 0011's coarse grant is action
-- 'display' only, and the level's default_access is 'none' -- so print of every
-- report is already default-denied. The `disallow print` rows below therefore
-- change NOTHING observable right now.
--
-- They are written anyway, for the reason 0038's own header gives and 0063/0068/
-- 0072/0073 all follow: a report that cannot be displayed but CAN be printed is a
-- leak with a paper trail, and the day anyone seeds a coarse print grant these two
-- must not be swept up in it.
--
-- ⛔ A PROBE MUST NOT REPORT THESE FOUR ROWS AS FOUR EQUAL WINS. Only the two
-- `display` rows go red-to-green. A drive that asserts "Data Entry is 403 on all
-- four" passes BEFORE this migration exists and proves nothing about it.
--
-- ⚠️ AND THE OWNER CONTROL ARM CANNOT ASSERT 200 ON THE PRINT PATHS. Printing
-- cash-flow and anomalies is NOT IMPLEMENTED -- handlePrintReport's default arm
-- answers 501, and handler_print_test.go's
-- TestTheUnimplementedReportsAreStillGovernableAndSaySo actively requires that
-- 501. An Owner gets 501 there, never 200.
--
-- RE-RUNNABLE. migrations.go applyOne wraps each file in ONE transaction and
-- stamps schema_migrations INSIDE it, so a file that cannot be re-run walls off
-- every later migration.

-- ---------------------------------------------------------------------------
-- ONE definition of the rule set, read by BOTH the per-company seeder and the
-- backfill. Two copies is how a company created next week ends up with a
-- different rule set from one created last month.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION forecast_report_acl_grants()
RETURNS TABLE (effect TEXT, action TEXT, resource TEXT) AS $$
    SELECT * FROM (VALUES
        -- NO ALLOW. See the header: the user's decision is deny both.
        --
        -- BOTH the display and the print form, for each report. The `print` pair
        -- is durability rather than a behaviour change today -- see the header.
        ('disallow', 'display', 'report:cash-flow'),
        ('disallow', 'print',   'report:cash-flow'),
        ('disallow', 'display', 'report:anomalies'),
        ('disallow', 'print',   'report:anomalies')
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
CREATE OR REPLACE FUNCTION seed_data_entry_forecast_report_rules(p_company_id UUID)
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

    FOR rec IN SELECT * FROM forecast_report_acl_grants() LOOP
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

GRANT EXECUTE ON FUNCTION forecast_report_acl_grants() TO tally_app;
GRANT EXECUTE ON FUNCTION seed_data_entry_forecast_report_rules(UUID) TO tally_app;

-- ---------------------------------------------------------------------------
-- 1. BACKFILL. Narrows every company that already exists, reading the SAME
--    single definition of the rule set rather than restating the four rules and
--    risking a third version of them.
-- ---------------------------------------------------------------------------
INSERT INTO acl_rule (id, company_id, security_level_id, effect, action, resource)
SELECT gen_random_uuid(), sl.company_id, sl.id, g.effect, g.action, g.resource
  FROM security_level sl
 CROSS JOIN forecast_report_acl_grants() AS g
 WHERE sl.name = 'Data Entry'
   AND NOT EXISTS (
        SELECT 1 FROM acl_rule ar
         WHERE ar.security_level_id = sl.id
           AND ar.effect   = g.effect
           AND ar.action   = g.action
           AND ar.resource = g.resource
   );

-- ---------------------------------------------------------------------------
-- 2. RESTATE THE SEEDER. Derived VERBATIM from 0073_order_book_acl.sql
--    (its restatement begins at line 189), which was confirmed to be the
--    HIGHEST-NUMBERED restatement of seed_company_auth_defaults at the time this
--    file was written -- 0074 and 0075 exist but neither restates it.
--    FOURTEEN PERFORMs: the thirteen 0073 carries, unchanged and in its order,
--    plus the forecast plane. Diff this against 0073 and the only differences
--    must be comments and the single new PERFORM at the end.
--
-- ⛔ WHY THE NUMBER MATTERS MORE THAN THE CONTENT. migrations.go applies files in
-- `sort.Strings` order and the last CREATE OR REPLACE wins, so a body installed
-- BELOW 0073 would be overwritten by 0073's on every fresh database -- thirteen
-- PERFORMs, none of them this one -- and the rules above would simply not exist
-- for any new company. That is not hypothetical: it is exactly the defect 0071
-- exists to repair, after 0063 derived its body correctly and took a number five
-- LOWER than 0068.
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
    -- THE FORECAST PLANE: four refusals, no allow (this migration). The cash-flow
    -- projection is the forward cash position; the anomaly list is the CONTROL
    -- SURFACE that names, by voucher number, every entry this very level keyed
    -- irregularly.
    PERFORM seed_data_entry_forecast_report_rules(p_company_id);

    RETURN owner_level_id;
END
$$;

GRANT EXECUTE ON FUNCTION seed_company_auth_defaults(UUID, UUID) TO tally_app;
