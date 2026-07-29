-- 0035_payroll_acl.sql
-- KEEP PAYROLL AWAY FROM THE 'Data Entry' LEVEL -- the voucher class, the
-- employee master, and all five payroll reports -- BEFORE any of them is
-- reachable over HTTP.
--
-- WHAT CHANGED, AND WHY IT IS NOT OPTIONAL PAPERWORK
-- --------------------------------------------------
-- Migration 0034 created the employee master, the pay run and the payslips, and
-- internal/ledger gained a `Payroll` voucher class. The wiring wave that mounts
-- the routes will register, at minimum:
--
--   POST/PUT .../vouchers/payroll        -> (create|alter, 'payroll')
--   POST/GET .../employees               -> (create|display, 'employee')
--   GET      .../reports/payroll-register        -> (display, report:payroll-register)
--   GET      .../reports/payroll-payslip         -> (display, report:payroll-payslip)
--   GET      .../reports/payroll-form-138        -> (display, report:payroll-form-138)
--   GET      .../reports/payroll-pf-ecr          -> (display, report:payroll-pf-ecr)
--   GET      .../reports/payroll-esi-return      -> (display, report:payroll-esi-return)
--
-- The rules land HERE, one wave BEFORE the routes, and that ordering is the
-- point: 0031's header records what happens the other way round -- 0018 added
-- the note classes, nobody added the rules, and 0019 had to record the bare 403
-- as a bug report. The fail-closed direction is to write the refusal first.
--
-- ===========================================================================
-- THE ASYMMETRY THAT MAKES THESE ROWS NECESSARY, AND IT IS EASY TO GET WRONG
-- ===========================================================================
-- For a NEW BARE RESOURCE TOKEN -- 'payroll', 'employee' -- SILENCE ALREADY
-- DENIES. The seeded Data Entry level has default_access = 'none', so a request
-- no rule matches falls through internal/authz.Evaluate to deny("default
-- access: none"). The six `disallow` rows for those two tokens change no answer
-- today.
--
-- FOR A REPORT NAME, SILENCE DOES NOT DENY. Migration 0011 seeds that same
-- level a COARSE `allow display report`, and the PDP's resource matcher
-- (internal/authz, matchResource) matches a bare `report` rule against ANY
-- request for report:NAME. So EVERY report added to this product is handed to
-- every operator in every tenant BY INHERITANCE, without anyone deciding to.
-- 0026 discovered this, and 0028 and 0033 each had to write the same
-- counterweight. A rule naming report:NAME scores specificity 2 against the
-- bare rule's 1, so the disallow wins.
--
-- If only the report half were written, the six bare-token rows would look like
-- the redundant ones and the five report rows like the necessary ones. Both
-- halves are written, for the reason below.
--
-- WHY EVEN THE REDUNDANT-TODAY ROWS ARE WRITTEN OUT LOUD
-- ------------------------------------------------------
-- Because an explicit refusal SURVIVES A LATER ADMINISTRATOR'S CONVENIENCE.
-- `allow create *` and `allow display *` are natural things for an owner to
-- write in Users & Roles, they need no code change, and they score specificity
-- 1. Silence would then hand over the payroll; an explicit `disallow` at
-- specificity 2 would not, and disallow beats allow at equal specificity
-- anyway. 0026, 0028 and 0031 each wrote theirs for exactly this reason.
--
-- ===========================================================================
-- THE JUDGEMENT: DATA ENTRY SEES NOTHING PAYROLL, BY DEFAULT
-- ===========================================================================
-- This is the least generous default in the product, and deliberately so.
--
--   * payroll (the voucher class) -- create/alter/display all DISALLOWED.
--     A pay run voucher's postings ARE the wage bill. `display payroll` alone
--     would disclose it, and create/alter would let whoever keys the invoices
--     also decide what the company pays its staff.
--
--   * employee (the master) -- create/alter/display all DISALLOWED.
--     The master row carries PAN, UAN, ESIC IP number, bank IFSC and the last
--     four digits of an account number for every person in the company. That is
--     the single most sensitive row set in this database, and unlike a cost
--     price it is sensitive to the PERSON, not to the business -- a leak here
--     is somebody's identity, not somebody's margin. `display` is refused along
--     with the writes for that reason: there is no version of "the operator
--     needs to browse the staff list" that justifies handing over the PANs.
--
--   * report:payroll-register  -- every employee's gross, deductions and net.
--   * report:payroll-payslip   -- one employee's, in full.
--   * report:payroll-form-138  -- PAN + salary + TDS, per person, per quarter.
--   * report:payroll-pf-ecr    -- UAN + wages, per person, per month.
--   * report:payroll-esi-return-- IP number + wages + last working day.
--     All five DISALLOWED, and note that four of the five are the statutory
--     outputs. NEXT-SESSION.md sec 4.3 item 5 states the rule this follows:
--     statutory outputs and payroll are more sensitive than the cost book, and
--     salaries and PANs are not "reports an operator may see by inheritance".
--
-- An Owner (default_access 'full') runs payroll. An owner who wants somebody
-- else to run it creates a named payroll level in Users & Roles and grants it
-- there -- an admin surface that already exists, and a decision an
-- administrator can reverse in one row. Withholding it here is not "not built":
-- it is the correct default for the most sensitive data in the product.
--
-- NOTHING ELSE CHANGES. The Trial Balance, the Day Book and the outstandings an
-- operator legitimately works from stay reachable, because these are five rules
-- naming five report NAMES and not a blanket disallow at the coarse `report`
-- grain -- which would take the whole reporting surface away and leave the
-- level unable to check its own work.
--
-- ONE CAVEAT WORTH WRITING DOWN: the aggregate control ledgers migration 0034
-- seeds (Salaries & Wages, PF Payable, ...) DO appear in the Day Book and the
-- Trial Balance, which Data Entry can read. That is intended and is the whole
-- reason employee is not a ledger: the operator sees that the company's wage
-- bill was 8.4 lakh -- a figure already on the P&L -- and never that Ravi earns
-- 84,317.

-- ---------------------------------------------------------------------------
-- The grant table itself, as a function, so the per-company seeder below and
-- the fleet backfill below it read from ONE definition. Two copies of this list
-- is how a tenant created next week ends up with a different rule set from one
-- created last month -- which here would mean payroll is an Owner action in
-- some tenants and an operator action in others.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION payroll_acl_grants()
RETURNS TABLE (effect TEXT, action TEXT, resource TEXT) AS $$
    SELECT * FROM (VALUES
        -- The voucher class. Refused out loud so a later `allow create *`
        -- cannot hand over the wage bill.
        ('disallow', 'create',  'payroll'),
        ('disallow', 'alter',   'payroll'),
        ('disallow', 'display', 'payroll'),
        -- The master: PAN, UAN, IP number, IFSC, account last4, per person.
        ('disallow', 'create',  'employee'),
        ('disallow', 'alter',   'employee'),
        ('disallow', 'display', 'employee'),
        -- The five reports. THESE are the rows that change an answer today:
        -- 0011's coarse `allow display report` matches every report:NAME, so
        -- without them the whole payroll surface is readable by inheritance.
        ('disallow', 'display', 'report:payroll-register'),
        ('disallow', 'display', 'report:payroll-payslip'),
        ('disallow', 'display', 'report:payroll-form-138'),
        ('disallow', 'display', 'report:payroll-pf-ecr'),
        ('disallow', 'display', 'report:payroll-esi-return')
    ) AS v(effect, action, resource);
$$ LANGUAGE sql IMMUTABLE;

-- ---------------------------------------------------------------------------
-- The per-company seeder. Idempotent at the (level, effect, action, resource)
-- grain, so a re-run inserts nothing -- `cored migrate` does NOT wrap a
-- migration in a transaction when it is applied by hand with psql, so a run
-- killed halfway leaves its rows behind WITHOUT stamping schema_migrations, and
-- only idempotence makes that recoverable (0028's header records the run that
-- proved it).
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION seed_data_entry_payroll_rules(p_company_id UUID)
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

    FOR rec IN SELECT * FROM payroll_acl_grants() LOOP
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

GRANT EXECUTE ON FUNCTION payroll_acl_grants() TO tally_app;
GRANT EXECUTE ON FUNCTION seed_data_entry_payroll_rules(UUID) TO tally_app;

-- ---------------------------------------------------------------------------
-- Backfill EVERY existing company, as ONE set-based statement.
--
-- NOT a per-company loop calling the function above. 0028's header records what
-- that costs on this project's own dev database: 38,748 companies reached 13%
-- in ONE HOUR AND FIFTY-SEVEN MINUTES before it was killed, while starving the
-- connection pool. The anti-join replaces the per-row EXISTS and is what makes
-- re-running a no-op.
--
-- Without the backfill, an operator in a company created last month could read
-- the payroll register while one in a company created next week could not --
-- two companies whose security levels differ only by the date they were
-- created, which is the failure mode every one of these seed migrations exists
-- to prevent. Here that difference is a payroll disclosure.
-- ---------------------------------------------------------------------------
INSERT INTO acl_rule (id, company_id, security_level_id, effect, action, resource)
SELECT gen_random_uuid(), sl.company_id, sl.id, g.effect, g.action, g.resource
  FROM security_level sl
 CROSS JOIN payroll_acl_grants() AS g
 WHERE sl.name = 'Data Entry'
   AND NOT EXISTS (
        SELECT 1 FROM acl_rule ar
         WHERE ar.security_level_id = sl.id
           AND ar.effect   = g.effect
           AND ar.action   = g.action
           AND ar.resource = g.resource
   );

-- ---------------------------------------------------------------------------
-- Fold it into the per-company auth seed so NEW companies get it at creation.
-- The whole body is restated (rather than wrapped) for the same reason 0028,
-- 0029, 0031 and 0033 restated theirs: a fresh database applying 0011 -> 0019
-- -> 0022 -> 0026 -> 0028 -> 0031 -> 0033 -> 0035 must end with ONE complete,
-- self-describing definition. The only change from 0033's version is the final
-- PERFORM.
--
-- NOTE FOR WHOEVER LANDS THE NEXT ONE: this restatement is a serialization
-- point. Two migrations that each restate this function from a different base
-- silently DROP the other's PERFORM, and nothing fails -- the companies created
-- afterwards simply miss a rule set. If 0033 changes, this body must be
-- re-derived from it, not from 0031.
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
    -- Payroll: the class, the employee master and the five reports (this
    -- migration). All disallowed -- see the header.
    PERFORM seed_data_entry_payroll_rules(p_company_id);

    RETURN owner_level_id;
END
$$;

GRANT EXECUTE ON FUNCTION seed_company_auth_defaults(UUID, UUID) TO tally_app;
