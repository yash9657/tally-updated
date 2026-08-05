-- 0072_cost_report_acl.sql
-- THE COST REPORTS ARE DEPARTMENTAL PROFITABILITY. Without this file, every
-- seeded Data Entry operator can read it the moment the reports exist.
--
-- ===========================================================================
-- WHY THIS IS 0072 AND NOT 0041, WHICH THE PLAN RESERVED FOR IT
-- ===========================================================================
-- PLAN-TALLY-PARITY.md §2.5 reserves 0040-0041 for T2, and 0041 CANNOT CARRY
-- THIS. seed_company_auth_defaults is extensible only by RESTATING its whole
-- plpgsql body, migrations.go:71 applies files in `sort.Strings` order, and the
-- last CREATE OR REPLACE wins. The highest-numbered restatement is
-- 0071_seeder_order_repair.sql, so a body installed at 0041 would be overwritten
-- by 0071's on every fresh database -- eleven PERFORMs, none of them this one --
-- and the rules below would simply not exist for any new company.
--
-- That is not hypothetical. It is exactly the defect 0071 exists to repair:
-- 0063_bank_statement_acl.sql derived its body correctly from 0068 and was
-- numbered five LOWER, so 0068's older body won and every company on a fresh
-- database got the imported bank statement handed to its Data Entry level.
-- Measured on a scratch DB at the time: 29 report rules, no
-- report:bank-statement row.
--
-- THE RULE, restated because a reserved block makes it easy to forget: DERIVE
-- FROM THE HIGHEST-NUMBERED RESTATEMENT AND TAKE A HIGHER NUMBER THAN IT. The
-- reserved 0041 is left permanently unused; 0040 carries T2's tables, which have
-- no ordering hazard because nothing replaces them.
--
-- backend/migrations/seeder_census_test.go guards this STATICALLY, with no
-- database, because the defect lives in apply order and is invisible on any
-- incrementally-migrated developer machine. If it fails, do not route around it.
--
-- ===========================================================================
-- WHY THESE TWO REPORTS NEED A COUNTERWEIGHT AT ALL
-- ===========================================================================
-- Migration 0011 seeds the Data Entry level a COARSE `allow display report`, and
-- internal/authz matches that against ANY `report:NAME` at specificity 1. So a
-- new report is GRANTED to Data Entry the moment it is added to
-- authz.KnownReportNames -- the grant is the DEFAULT and the refusal is the thing
-- that must be written. This file writes it at specificity 2, exactly as
-- 0026/0028/0033/0035/0046/0063/0068 do.
--
-- ⚠️ NOTE THE ASYMMETRY WITH T2'S OTHER RESOURCE. The cost MASTERS are a bare
-- token (`cost-centre`) and need NO rows here: the seeded Data Entry level has
-- default_access = 'none' and every rule it carries names a specific token, so a
-- token never mentioned is denied. T6 shipped tds-challan and tds-profile that
-- way with zero ACL rows. It is only the `report:NAME` form that arrives
-- pre-granted and needs taking back.
--
-- WHAT THE REPORTS ACTUALLY EXPOSE, since that is the whole justification:
-- which department, branch or project earns and which loses. It is the
-- tds-register problem one table over -- that report leaks the list of suppliers
-- a company pays and customers it collects from, this one leaks what each part of
-- the business is worth. A data-entry clerk keying invoices needs neither.
--
-- NO ALLOW IS SEEDED, deliberately. An operator does not need to read
-- departmental profitability in order to key a voucher that will be allocated to
-- a department; the allocation is part of voucher entry, governed by the voucher
-- class rules 0011 already seeds. If a tenant later wants a clerk reading a cost
-- report, that is an explicit `allow display report:cost-centre-summary` written
-- through the admin API -- a decision, made by the tenant, visible in acl_rule.
-- It is not a default.
--
-- RE-RUNNABLE. migrations.go applyOne wraps each file in ONE transaction and
-- stamps schema_migrations INSIDE it, so a file that cannot be re-run walls off
-- every later migration.

-- ---------------------------------------------------------------------------
-- ONE definition of the rule set, read by BOTH the per-company seeder and the
-- backfill. Two copies is how a company created next week ends up with a
-- different rule set from one created last month.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION cost_report_acl_grants()
RETURNS TABLE (effect TEXT, action TEXT, resource TEXT) AS $$
    SELECT * FROM (VALUES
        -- NO ALLOW. See the header.
        --
        -- BOTH the display and the print form, for each report. `disallow
        -- display` alone would leave `print` matched only by 0011's coarse rule
        -- and 0038's print block, and a report that cannot be displayed but CAN
        -- be printed is a leak with a paper trail. 0063 and 0068 both write the
        -- pair for this reason.
        ('disallow', 'display', 'report:cost-centre-summary'),
        ('disallow', 'print',   'report:cost-centre-summary'),
        ('disallow', 'display', 'report:cost-category-summary'),
        ('disallow', 'print',   'report:cost-category-summary')
    ) AS t(effect, action, resource);
$$ LANGUAGE sql IMMUTABLE;

-- ---------------------------------------------------------------------------
-- The per-company seeder, called when a company is created.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION seed_data_entry_cost_report_rules(p_company_id UUID)
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

    FOR rec IN SELECT * FROM cost_report_acl_grants() LOOP
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

GRANT EXECUTE ON FUNCTION cost_report_acl_grants() TO tally_app;
GRANT EXECUTE ON FUNCTION seed_data_entry_cost_report_rules(UUID) TO tally_app;

-- ---------------------------------------------------------------------------
-- 1. BACKFILL. Narrows every company that already exists, reading the SAME
--    single definition of the rule set rather than restating the four rules and
--    risking a third version of them.
-- ---------------------------------------------------------------------------
INSERT INTO acl_rule (id, company_id, security_level_id, effect, action, resource)
SELECT gen_random_uuid(), sl.company_id, sl.id, g.effect, g.action, g.resource
  FROM security_level sl
 CROSS JOIN cost_report_acl_grants() AS g
 WHERE sl.name = 'Data Entry'
   AND NOT EXISTS (
        SELECT 1 FROM acl_rule ar
         WHERE ar.security_level_id = sl.id
           AND ar.effect   = g.effect
           AND ar.action   = g.action
           AND ar.resource = g.resource
   );

-- ---------------------------------------------------------------------------
-- 2. RESTATE THE SEEDER. Derived VERBATIM from 0071_seeder_order_repair.sql
--    (lines 153-232), the highest-numbered restatement. TWELVE PERFORMs -- the
--    eleven 0071 carries, unchanged and in its order, plus the cost reports.
--    Diff this against 0071 and the only differences must be comments and the
--    single new PERFORM at the end.
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
    -- THE COST REPORTS: four refusals, no allow (this migration). They are
    -- departmental profitability -- which part of the business earns and which
    -- loses.
    PERFORM seed_data_entry_cost_report_rules(p_company_id);

    RETURN owner_level_id;
END
$$;

GRANT EXECUTE ON FUNCTION seed_company_auth_defaults(UUID, UUID) TO tally_app;
