-- 0046_bank_recon_acl.sql
-- THE BANK-RECONCILIATION AUTHORITY: let the clerk who keys the payments tick
-- them off against the statement, and refuse the STATEMENT ITSELF out loud.
--
-- Design memo: docs/design/t4-model.md §5, Authorization.
--
-- ===========================================================================
-- THE TWO HALVES PULL IN OPPOSITE DIRECTIONS, FOR THE SAME REASON AS 0038
-- ===========================================================================
-- T4 mounts:
--
--   GET  .../bank-reconciliation/{ledgerID}                  -> (display, report:bank-reconciliation)
--   PUT  .../bank-reconciliation/{ledgerID}/{voucherID}/{leg} -> (alter,   bank-reconciliation)
--   DELETE ditto                                             -> (alter,   bank-reconciliation)
--
--   TICKING AN ITEM OFF IS CLERICAL, AND SILENCE DENIES IT. The seeded
--   'Data Entry' level has default_access = 'none', so without an explicit
--   allow the person who keys the cheques cannot reconcile them and the feature
--   is delivered to nobody. Reconciling moves NO balance and alters NO voucher
--   (invariant I-2, proven by TestReconcilingChangesNoBalanceAndNoVoucher), so
--   it is exactly the kind of clerical act Data Entry exists for. Hence the
--   allow below.
--
--   THE STATEMENT IS SENSITIVE, AND SILENCE DOES *NOT* DENY IT. This is the
--   half that would be missed. Migration 0011 seeds the coarse
--   `allow display report` for Data Entry, and internal/authz matches a bare
--   `report` rule against ANY report:NAME at specificity 1 -- so the moment
--   report:bank-reconciliation exists it is ALREADY GRANTED to Data Entry by a
--   rule written years earlier for a different purpose. 0026, 0028, 0033 and
--   0035 each had to write the same counterweight.
--
--   A bank reconciliation statement is the company's full bank position, every
--   un-cleared cheque and every counterparty. It is the document a leaver walks
--   out with. So it gets an EXPLICIT disallow at specificity 2, which beats the
--   coarse allow at specificity 1.
--
-- WHY BOTH `display` AND `print` ARE REFUSED, AND WHY THAT IS NOT BELT-AND-
-- BRACES: they are DIFFERENT ACTIONS in the PDP vocabulary. `print` is already
-- default-denied (no seeded rule names it), so the disallow-print row changes
-- no answer today -- and that is precisely why it is written now, while the
-- report is new, rather than after somebody adds `allow print *` to make a
-- delivery note printable and silently hands out the bank position with it.
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
CREATE OR REPLACE FUNCTION bank_recon_acl_grants()
RETURNS TABLE (effect TEXT, action TEXT, resource TEXT) AS $$
    SELECT * FROM (VALUES
        -- THE ALLOW. Ticking an item off against the statement is clerical and
        -- moves nothing; without this the feature reaches nobody.
        ('allow',    'alter',   'bank-reconciliation'),

        -- THE REFUSALS. The statement is the company's bank position.
        -- `display` COUNTERWEIGHTS 0011's coarse `allow display report`
        -- (specificity 2 beats specificity 1). `print` changes no answer today
        -- and is written so a later blanket print grant cannot reach this.
        ('disallow', 'display', 'report:bank-reconciliation'),
        ('disallow', 'print',   'report:bank-reconciliation')
    ) AS t(effect, action, resource);
$$ LANGUAGE sql IMMUTABLE;

-- ---------------------------------------------------------------------------
-- The per-company seeder, called when a company is created.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION seed_data_entry_bank_recon_rules(p_company_id UUID)
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

    FOR rec IN SELECT * FROM bank_recon_acl_grants() LOOP
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

GRANT EXECUTE ON FUNCTION bank_recon_acl_grants() TO tally_app;
GRANT EXECUTE ON FUNCTION seed_data_entry_bank_recon_rules(UUID) TO tally_app;

-- ---------------------------------------------------------------------------
-- Backfill EVERY existing company as ONE set-based statement, so an operator in
-- a company created last month has the same authority as one in a company
-- created next week. The anti-join is what makes a re-run a no-op.
-- ---------------------------------------------------------------------------
INSERT INTO acl_rule (id, company_id, security_level_id, effect, action, resource)
SELECT gen_random_uuid(), sl.company_id, sl.id, g.effect, g.action, g.resource
  FROM security_level sl
 CROSS JOIN bank_recon_acl_grants() AS g
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
-- correctly, and every company created AFTERWARDS silently lacks both the grant
-- and the refusal -- so a clerk cannot reconcile, and the seeded Data Entry
-- level CAN read the bank statement, in exactly the tenants nobody tested.
-- This was caught by TestDataEntryMayTickOffButMayNotReadTheStatement, which
-- creates its company at test time and therefore exercises this path rather
-- than the backfill.
--
-- The whole body is restated (rather than wrapped) for the same reason 0028,
-- 0029, 0031, 0033, 0035 and 0038 restated theirs: a fresh database applying
-- them in order must end with ONE complete, self-describing definition.
--
-- NOTE FOR WHOEVER LANDS THE NEXT ONE: this restatement is a serialization
-- point. Two migrations that each restate this function from a different base
-- silently DROP the other's PERFORM, and nothing fails -- the companies created
-- afterwards simply miss a rule set. This body was re-derived from 0038, which
-- is the immediately preceding restatement; derive yours from THIS one.
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
    -- BANK RECONCILIATION: the clerical allow and the two refusals of the
    -- statement itself (this migration). See the header.
    PERFORM seed_data_entry_bank_recon_rules(p_company_id);

    RETURN owner_level_id;
END
$$;

GRANT EXECUTE ON FUNCTION seed_company_auth_defaults(UUID, UUID) TO tally_app;
