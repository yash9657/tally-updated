-- 0038_print_acl.sql
-- THE PRINT AUTHORITY: let the operator who KEYS a document PRINT it, and refuse
-- print of the sensitive statements OUT LOUD, before either becomes reachable.
--
-- ===========================================================================
-- WHY THERE IS ANYTHING TO DO HERE AT ALL
-- ===========================================================================
-- Track T1 mounts two routes:
--
--   GET .../print/vouchers/{class}/{voucherID} -> (print,   <class>)
--   GET .../print/reports/{reportName}         -> (print,   report:NAME)
--
-- `print` is a DISTINCT ACTION in the PDP vocabulary (internal/authz,
-- KnownActions), and it always has been -- Tally models "may see it on screen"
-- and "may take it out of the building" as different authorities. That single
-- fact decides both halves of this migration, in OPPOSITE directions:
--
--   FOR VOUCHER CLASSES, SILENCE DENIES. The seeded 'Data Entry' level has
--   default_access = 'none' and NOT ONE seeded rule anywhere names action
--   'print' (grep migrations 0011, 0019, 0022, 0026, 0028, 0031, 0033, 0035:
--   every rule is create/alter/display). So on the day the print route mounts,
--   an operator who may create a sale, alter it and display it CANNOT PRINT IT.
--   That is not a security posture, it is a broken feature: the person who keys
--   the invoice is the person who hands it to the customer, and a print button
--   that 403s means the invoice gets typed again into a spreadsheet -- at which
--   point the paper and the books disagree and the audit trail is decorative.
--   The allow rules below are what make T1 deliver anything to the person it is
--   for.
--
--   FOR REPORTS, SILENCE ALREADY DENIES TOO -- and this is the one place where
--   this migration differs from 0026/0028/0033/0035. Those four each had to
--   write a counterweight because migration 0011 seeds the coarse
--   `allow display report`, and the PDP's resource matcher matches a bare
--   `report` rule against ANY report:NAME at specificity 1. But that rule's
--   ACTION is 'display'. internal/authz matches the action too, so it does NOT
--   reach action 'print' -- there is no coarse print grant to counterweight, and
--   print of EVERY report is already default-denied for the seeded level.
--
-- ===========================================================================
-- SO WHY WRITE THE `disallow print report:NAME` ROWS ANYWAY
-- ===========================================================================
-- For exactly the reason 0035's header gives, and it is the strongest argument
-- in this whole family of migrations: AN EXPLICIT REFUSAL SURVIVES A LATER
-- ADMINISTRATOR'S CONVENIENCE.
--
-- `allow print *` is a natural thing for an owner to write in Users & Roles the
-- first time somebody complains that they cannot print anything. It needs no
-- code change, it scores specificity 1, and against SILENCE it wins -- handing
-- that level the payroll register, the priced supplier reconciliation and the
-- company's own cost prices, as a PDF, in one click. A rule naming
-- report:NAME scores specificity 2 and beats it, and `disallow` beats `allow` at
-- equal specificity anyway. The rows below are therefore not redundant
-- paperwork; they are the difference between a convenience rule being safe and
-- being a disclosure.
--
-- Twelve report names are refused, and each is a disclosure somebody would
-- actually mind leaving the building on paper:
--
--   the five PAYROLL reports -- every employee's gross/deductions/net; one named
--     employee's pay component by component; PAN + salary + TDS per person; UAN
--     + PF wages per person; ESIC IP number + wages + last working day. Four of
--     the five are statutory outputs. This is the least generous default in the
--     product and deliberately so (0035's header).
--   itc-reconciliation -- the whole supplier book: who we buy from, their GSTIN,
--     their invoice numbers and what we paid, with the arithmetic done.
--   ims-inbox -- the filer's working paper on which suppliers' invoices we think
--     are wrong.
--   the five VALUE-BEARING STOCK reports (stock-summary, stock-movement,
--     godown-summary, batch-summary, stock-item-vouchers) -- every one carries
--     COST: a closing valuation rate is what the business PAID, and the item
--     drill states the rate of each individual purchase outright rather than
--     averaged. 0026 and 0028 refused the on-screen versions of these for the
--     same reason; a competitor who hires an operator should not receive the
--     cost book with the job, and least of all as a file.
--
-- ===========================================================================
-- WHICH VOUCHER CLASSES GET `allow print`, AND WHY NOT THE OTHERS
-- ===========================================================================
-- The seeded Data Entry level may already create/alter/display: sales, purchase,
-- receipt, payment, contra, journal (0011), credit-note, debit-note (0019),
-- delivery-note, receipt-note, stock-journal (0031), and display physical-stock
-- (0031 refuses create/alter on it).
--
-- Print is granted for the SIX of those that PRODUCE A DOCUMENT AT ALL:
--
--   sales         -- the tax invoice or bill of supply. This is the one the whole
--                    migration exists for.
--   receipt       -- the money receipt handed over when a customer pays.
--   payment       -- the payment voucher.
--   credit-note   -- issued to OUR customer, so we print it.
--   debit-note    -- issued to a SUPPLIER, so we print it.
--   delivery-note -- travels with the goods; the transporter has to show it.
--
-- The other seven get NO print rule, and that is a decision rather than an
-- oversight. internal/printing/printingdb refuses each of them with a written
-- reason: a Purchase is the SUPPLIER's invoice and printing ours as a tax invoice
-- would be this company issuing a document it has no authority to issue; Contra
-- and Journal move nothing to anybody; a Receipt Note is the inward counterpart
-- of a delivery note, so we RECEIVE that paper rather than issue it; a Stock
-- Journal and a Physical Stock are internal transfers and counts and nobody is
-- handed anything; and a Payroll voucher's document is a payslip, per employee,
-- which is a payroll report and is refused below. Granting print on a class that
-- can only ever answer 400 would be a rule that names an authority which does not
-- exist -- and the fail-closed direction for an authority that does not exist is
-- silence, not an allow.
--
-- PAYROLL IS REFUSED OUT LOUD. `disallow print payroll` changes no answer today
-- (default_access 'none' already denies it, and the class prints nothing anyway),
-- and it is written for the same reason 0035 wrote its `disallow create payroll`:
-- so that a later `allow print *` cannot reach the wage bill. It also pairs with
-- the check the print handler makes -- the ACL decision is taken from the class in
-- the PATH, so the handler additionally compares it against the STORED class and
-- 404s a mismatch. Without that, a level holding `allow print sales` could ask for
-- /print/vouchers/sales/<a payroll voucher's id>. Belt and braces, because one of
-- the two is an ACL row an administrator can delete.
--
-- WHAT IS DELIBERATELY *NOT* GRANTED: `allow print report:NAME` for anything.
-- Data Entry can DISPLAY the Trial Balance, the Day Book and the outstandings
-- (0011's coarse grant) and cannot PRINT any of them. That is the fail-closed
-- default for a statement leaving the building, it is reversible by an owner in
-- one row in Users & Roles, and it is deliberately narrower than the display
-- grant. If the product decides an operator should print the outstandings they
-- chase, that is one `allow print report:outstandings` row in a successor
-- migration -- a decision, made once, on the record.
--
-- ===========================================================================
-- IDEMPOTENCE
-- ===========================================================================
-- Every insert below is guarded at the (level, effect, action, resource) grain.
-- `cored migrate` does NOT wrap a migration in a transaction when it is applied
-- by hand with psql, so a run killed halfway leaves its rows behind WITHOUT
-- stamping schema_migrations, and only idempotence makes that recoverable (0028's
-- header records the run that proved it). This file was applied twice and the
-- second run inserted nothing.

-- ---------------------------------------------------------------------------
-- The grant table, as a function, so the per-company seeder and the fleet
-- backfill read from ONE definition. Two copies of this list is how a tenant
-- created next week ends up with a different rule set from one created last
-- month -- which here would mean an operator can print an invoice in some
-- tenants and not in others.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION print_acl_grants()
RETURNS TABLE (effect TEXT, action TEXT, resource TEXT) AS $$
    SELECT * FROM (VALUES
        -- ------------------------------------------------------------------
        -- THE ALLOWS. Without these, T1 delivers nothing to the person who
        -- keys the invoice: `print` is its own action and no seeded rule
        -- anywhere names it, so silence denies.
        -- ------------------------------------------------------------------
        ('allow',    'print', 'sales'),
        ('allow',    'print', 'receipt'),
        ('allow',    'print', 'payment'),
        ('allow',    'print', 'credit-note'),
        ('allow',    'print', 'debit-note'),
        ('allow',    'print', 'delivery-note'),

        -- ------------------------------------------------------------------
        -- THE WAGE BILL, refused out loud so a later `allow print *` cannot
        -- reach it. Changes no answer today; that is the point.
        -- ------------------------------------------------------------------
        ('disallow', 'print', 'payroll'),

        -- ------------------------------------------------------------------
        -- THE FIVE PAYROLL REPORTS. Salaries, PAN, UAN and ESIC IP numbers.
        -- ------------------------------------------------------------------
        ('disallow', 'print', 'report:payroll-register'),
        ('disallow', 'print', 'report:payroll-payslip'),
        ('disallow', 'print', 'report:payroll-form-138'),
        ('disallow', 'print', 'report:payroll-pf-ecr'),
        ('disallow', 'print', 'report:payroll-esi-return'),

        -- ------------------------------------------------------------------
        -- THE SUPPLIER BOOK AND THE FILER'S WORKING PAPER (cf. 0033).
        -- ------------------------------------------------------------------
        ('disallow', 'print', 'report:itc-reconciliation'),
        ('disallow', 'print', 'report:ims-inbox'),

        -- ------------------------------------------------------------------
        -- THE FIVE VALUE-BEARING STOCK REPORTS (cf. 0026 and 0028). Each one
        -- prints the company's own cost prices.
        -- ------------------------------------------------------------------
        ('disallow', 'print', 'report:stock-summary'),
        ('disallow', 'print', 'report:stock-movement'),
        ('disallow', 'print', 'report:godown-summary'),
        ('disallow', 'print', 'report:batch-summary'),
        ('disallow', 'print', 'report:stock-item-vouchers')
    ) AS v(effect, action, resource);
$$ LANGUAGE sql IMMUTABLE;

-- ---------------------------------------------------------------------------
-- The per-company seeder. Idempotent at the (level, effect, action, resource)
-- grain.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION seed_data_entry_print_rules(p_company_id UUID)
RETURNS VOID AS $$
DECLARE
    de_level_id UUID;
    rec         RECORD;
BEGIN
    SELECT id INTO de_level_id
      FROM security_level
     WHERE company_id = p_company_id AND name = 'Data Entry';
    IF de_level_id IS NULL THEN
        RETURN;   -- company has no Data Entry level; nothing to grant or narrow
    END IF;

    FOR rec IN SELECT * FROM print_acl_grants() LOOP
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

GRANT EXECUTE ON FUNCTION print_acl_grants() TO tally_app;
GRANT EXECUTE ON FUNCTION seed_data_entry_print_rules(UUID) TO tally_app;

-- ---------------------------------------------------------------------------
-- Backfill EVERY existing company, as ONE set-based statement.
--
-- NOT a per-company loop calling the function above. 0028's header records what
-- that costs on this project's own dev database: 38,748 companies reached 13% in
-- ONE HOUR AND FIFTY-SEVEN MINUTES before it was killed, while starving the
-- connection pool. The anti-join replaces the per-row EXISTS and is what makes
-- re-running a no-op.
--
-- Without the backfill, an operator in a company created last month could not
-- print the invoice they keyed while one in a company created next week could --
-- two companies whose security levels differ only by the date they were created,
-- which is the failure mode every one of these seed migrations exists to prevent.
-- ---------------------------------------------------------------------------
INSERT INTO acl_rule (id, company_id, security_level_id, effect, action, resource)
SELECT gen_random_uuid(), sl.company_id, sl.id, g.effect, g.action, g.resource
  FROM security_level sl
 CROSS JOIN print_acl_grants() AS g
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
--
-- The whole body is restated (rather than wrapped) for the same reason 0028,
-- 0029, 0031, 0033 and 0035 restated theirs: a fresh database applying
-- 0011 -> 0019 -> 0022 -> 0026 -> 0028 -> 0031 -> 0033 -> 0035 -> 0038 must end
-- with ONE complete, self-describing definition. The only change from 0035's
-- version is the final PERFORM.
--
-- NOTE FOR WHOEVER LANDS THE NEXT ONE: this restatement is a serialization
-- point. Two migrations that each restate this function from a different base
-- silently DROP the other's PERFORM, and nothing fails -- the companies created
-- afterwards simply miss a rule set. This body was re-derived from 0035, which is
-- the immediately preceding restatement; derive yours from THIS one.
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
    -- the pay run they may not (this migration). See the header.
    PERFORM seed_data_entry_print_rules(p_company_id);

    RETURN owner_level_id;
END
$$;

GRANT EXECUTE ON FUNCTION seed_company_auth_defaults(UUID, UUID) TO tally_app;
