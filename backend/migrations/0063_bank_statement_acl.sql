-- 0063_bank_statement_acl.sql
-- THE BANK-STATEMENT AUTHORITY: refuse the imported statement to Data Entry,
-- out loud, because silence would GRANT it.
--
-- Design memo: docs/design/t4-slice2-model.md §8.3 (slice 2a).
--
-- ===========================================================================
-- WHY A MIGRATION IS NEEDED AT ALL, WHEN 0046'S HALF-MEASURE WAS NOT
-- ===========================================================================
-- Slice 2a mounts:
--
--   POST .../bank-statement/{ledgerID}/import -> (create,  bank-statement)
--   GET  .../bank-statement/{ledgerID}        -> (display, report:bank-statement)
--
-- THE WRITE NEEDS NOTHING. Data Entry's default_access is 'none' and no coarse
-- seeded rule reaches a bare `bank-statement` resource -- 0011's is
-- `display report`, which matches report:NAME only; 0022's is `master`. So the
-- import route is ALREADY refused to Data Entry and already allowed to Owner,
-- and restating seed_company_auth_defaults for it would buy nothing while
-- paying that function's serialization cost. Item Zero's steps 2 and 3 made the
-- same call for the same reason.
--
-- THE READ NEEDS THIS FILE, AND IT IS THE HALF THAT WOULD BE MISSED. 0011 seeds
-- the coarse `allow display report` for Data Entry and internal/authz matches a
-- bare `report` rule against ANY report:NAME at specificity 1 -- so the moment
-- report:bank-statement exists it is ALREADY GRANTED by a rule written years
-- earlier for a different purpose. 0026, 0028, 0033, 0035, 0046 and 0068 each
-- had to write the same counterweight.
--
-- ===========================================================================
-- WHY THERE IS NO CLERICAL `allow`, UNLIKE 0046
-- ===========================================================================
-- 0046 granted Data Entry `alter bank-reconciliation`, because ticking an item
-- off moves no balance and alters no voucher -- it is exactly the clerical act
-- that level exists for, and without the grant the feature reached nobody.
--
-- IMPORTING A STATEMENT IS NOT THE SAME ACT, and the difference is what is on
-- the paper. Ticking off is per-item, against vouchers the clerk keyed and can
-- already see. An imported statement is the bank's own file: every counterparty
-- the business pays, every amount, every date, including the ones that
-- correspond to no voucher the clerk has ever seen -- salaries, the owner's
-- drawings, the loan repayment. It is the document a leaver walks out with,
-- which is the same sentence 0046 used to REFUSE the statement report to the
-- very level it granted the ticking to.
--
-- So both halves are refused here, and the refusal is the recoverable
-- direction: an owner who wants their clerk importing statements writes one
-- allow rule. The opposite mistake cannot be un-made -- the file has been read.
--
-- ⚠️ IF THIS IS REVISITED, REVISIT BOTH HALVES TOGETHER. Granting `create
-- bank-statement` without `display report:bank-statement` would let a clerk
-- feed the file in and not read it back, which sounds cautious and is merely
-- incoherent: the import's own response is the statement.
--
-- WHY `print` IS REFUSED TOO, and why that is not belt-and-braces: `display`
-- and `print` are DIFFERENT ACTIONS in the PDP vocabulary. `print` is already
-- default-denied (no seeded rule names it), so the disallow-print row changes
-- no answer today -- which is precisely why it is written now, while the report
-- is new, rather than after somebody adds a blanket print grant to make a
-- delivery note printable and hands out the bank statement with it.
--
-- ===========================================================================
-- RE-RUNNABLE: CREATE OR REPLACE on both functions, NOT EXISTS on every insert,
-- and the backfill is a single anti-joined statement rather than a per-company
-- loop -- 0028's header records that a loop reached 13% of 38,748 companies in
-- one hour fifty-seven minutes before it was killed.
-- ===========================================================================

-- ---------------------------------------------------------------------------
-- ONE definition of the rule set, read by BOTH the per-company seeder and the
-- backfill. Two copies is how a company created next week ends up with a
-- different rule set from one created last month.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION bank_statement_acl_grants()
RETURNS TABLE (effect TEXT, action TEXT, resource TEXT) AS $$
    SELECT * FROM (VALUES
        -- NO ALLOW. See the header: the import route is already refused by
        -- default_access = 'none', and that is the intended authority.
        ('disallow', 'display', 'report:bank-statement'),
        ('disallow', 'print',   'report:bank-statement')
    ) AS t(effect, action, resource);
$$ LANGUAGE sql IMMUTABLE;

-- ---------------------------------------------------------------------------
-- The per-company seeder, called when a company is created.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION seed_data_entry_bank_statement_rules(p_company_id UUID)
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

    FOR rec IN SELECT * FROM bank_statement_acl_grants() LOOP
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

GRANT EXECUTE ON FUNCTION bank_statement_acl_grants() TO tally_app;
GRANT EXECUTE ON FUNCTION seed_data_entry_bank_statement_rules(UUID) TO tally_app;

-- ---------------------------------------------------------------------------
-- Backfill EVERY existing company as ONE set-based statement, so an operator in
-- a company created last month has the same authority as one in a company
-- created next week. The anti-join is what makes a re-run a no-op.
-- ---------------------------------------------------------------------------
INSERT INTO acl_rule (id, company_id, security_level_id, effect, action, resource)
SELECT gen_random_uuid(), sl.company_id, sl.id, g.effect, g.action, g.resource
  FROM security_level sl
 CROSS JOIN bank_statement_acl_grants() AS g
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
-- WITHOUT THIS THE BACKFILL ABOVE IS A HALF-MEASURE, and the failure is silent
-- in the worst way: every company that existed when this migration ran behaves
-- correctly, and every company created AFTERWARDS silently lacks the refusal --
-- so the seeded Data Entry level CAN read the imported bank statement, in
-- exactly the tenants nobody tested. TestDataEntryIsRefusedTheBankStatement
-- creates its company AT TEST TIME and therefore exercises this path rather
-- than the backfill.
--
-- ⚠️ THIS BODY WAS DERIVED FROM 0068, the immediately preceding restatement, as
-- 0068's own note instructs. If you land the next one, derive it from THIS one:
-- two migrations restating this function from different bases silently drop each
-- other's PERFORM and nothing fails.
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
    -- THE IMPORTED BANK STATEMENT: two refusals, no allow (this migration). It
    -- is the bank's own file of every counterparty. See the header.
    PERFORM seed_data_entry_bank_statement_rules(p_company_id);

    RETURN owner_level_id;
END
$$;

GRANT EXECUTE ON FUNCTION seed_company_auth_defaults(UUID, UUID) TO tally_app;
