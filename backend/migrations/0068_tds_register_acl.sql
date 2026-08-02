-- 0068_tds_register_acl.sql
-- THE TDS/TCS REGISTER IS A LIST OF EVERY SUPPLIER YOU PAY AND EVERY CUSTOMER
-- YOU COLLECT FROM, WITH AMOUNTS. Refuse it out loud.
--
-- Design memo: docs/design/t6-model.md §1.4, which says this in one line:
-- "Needs an explicit `disallow display report:` and `disallow print report:` at
-- specificity 2 ... Data Entry's seeded coarse `allow display report` matches
-- every `report:NAME` at specificity 1."
--
-- Migration number from PLAN-TALLY-PARITY.md §2.5, which reserves 0068 for it.
--
-- ===========================================================================
-- THIS IS THE FIFTH TIME THIS COUNTERWEIGHT HAS BEEN NEEDED, AND THE REASON IS
-- ALWAYS THE SAME
-- ===========================================================================
-- Migration 0011 seeds the coarse `allow display report` for Data Entry, and
-- internal/authz matches a bare `report` rule against ANY report:NAME at
-- specificity 1. So the MOMENT report:tds-register exists it is ALREADY GRANTED
-- to Data Entry by a rule written years earlier for a different purpose.
-- 0026, 0028, 0033, 0035 and 0046 each had to write this same counterweight.
--
-- ⚠️ IT IS NOT WRITTEN HERE OUT OF HABIT. Ask what the register actually
-- discloses and the answer settles it:
--
--   * EVERY DEDUCTEE, BY NAME, WITH WHAT THEY WERE PAID. That is the company's
--     contractor and consultant list with rates — the commercially sensitive
--     half of the supplier ledger, and the document a leaver walks out with.
--   * EVERY BUYER OF SCRAP OR VEHICLES, with consideration. The customer list,
--     the same way round.
--   * WHETHER A PAN IS ON RECORD for each of them, which is a fact about a
--     third party that the third party gave us for one purpose.
--
-- Against that, the case for granting it to Data Entry is that an operator
-- keying a purchase might want to check what was deducted. They can already see
-- that on the voucher they keyed. The register's value is the AGGREGATE view,
-- and the aggregate view is exactly what is sensitive.
--
-- WHY BOTH `display` AND `print`, AND WHY THAT IS NOT BELT-AND-BRACES: they are
-- DIFFERENT ACTIONS in the PDP vocabulary. `print` is already default-denied for
-- Data Entry (migration 0038 seeds zero `allow print report:`), so the
-- disallow-print row changes no answer today — and that is precisely why it is
-- written now, while the report is new, rather than after somebody adds a
-- blanket print grant to make a delivery note printable and silently hands out
-- the whole supplier list with it. 0046 made the same call for the same reason.
--
-- ===========================================================================
-- THE seed_company_auth_defaults RESTATEMENT IS A SERIALIZATION POINT
-- ===========================================================================
-- 0046's header warns: "Two migrations that each restate this function from a
-- different base silently DROP the other's PERFORM, and nothing fails." There is
-- no way to append to a plpgsql body in place, so a new seeder can only be
-- reached by restating the whole function -- and every restatement is a chance
-- to lose a predecessor's rule set.
--
-- The rule 0046 states for whoever lands the next one is followed literally
-- here: THE BODY BELOW IS DERIVED FROM 0046, the immediately preceding
-- restatement, with exactly one PERFORM added at the end. Diff it against 0046
-- before changing anything, and derive the NEXT one from this file.
--
-- RE-RUNNABLE: CREATE OR REPLACE on both functions, NOT EXISTS on every insert,
-- and the backfill is a single anti-joined statement rather than a per-company
-- loop (0028's header records a loop reaching 13% of 38,748 companies in one
-- hour fifty-seven minutes before it was killed).

-- ---------------------------------------------------------------------------
-- ONE definition of the rule set, read by BOTH the per-company seeder and the
-- backfill.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION tds_register_acl_grants()
RETURNS TABLE (effect TEXT, action TEXT, resource TEXT) AS $$
    SELECT * FROM (VALUES
        -- The register is the company's supplier and customer list with amounts.
        -- `display` COUNTERWEIGHTS 0011's coarse `allow display report`
        -- (specificity 2 beats specificity 1). `print` changes no answer today
        -- and is written so a later blanket print grant cannot reach this.
        ('disallow', 'display', 'report:tds-register'),
        ('disallow', 'print',   'report:tds-register')
    ) AS t(effect, action, resource);
$$ LANGUAGE sql IMMUTABLE;

-- ---------------------------------------------------------------------------
-- The per-company seeder, called when a company is created.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION seed_data_entry_tds_register_rules(p_company_id UUID)
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

    FOR rec IN SELECT * FROM tds_register_acl_grants() LOOP
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

GRANT EXECUTE ON FUNCTION tds_register_acl_grants() TO tally_app;
GRANT EXECUTE ON FUNCTION seed_data_entry_tds_register_rules(UUID) TO tally_app;

-- ---------------------------------------------------------------------------
-- Backfill EVERY existing company as ONE set-based statement, so an operator in
-- a company created last month has the same authority as one in a company
-- created next week. The anti-join is what makes a re-run a no-op.
-- ---------------------------------------------------------------------------
INSERT INTO acl_rule (id, company_id, security_level_id, effect, action, resource)
SELECT gen_random_uuid(), sl.company_id, sl.id, g.effect, g.action, g.resource
  FROM security_level sl
 CROSS JOIN tds_register_acl_grants() AS g
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
-- so the seeded Data Entry level CAN read the whole supplier list, in exactly
-- the tenants nobody tested. TestDataEntryIsRefusedTheTDSRegister creates its
-- company AT TEST TIME and therefore exercises this path rather than the
-- backfill.
--
-- ⚠️ THIS BODY WAS DERIVED FROM 0046, the immediately preceding restatement, as
-- 0046's own note instructs. If you land the next one, derive it from THIS one:
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
    -- THE TDS/TCS REGISTER: two refusals, no allow (this migration). It is the
    -- supplier and customer list with amounts. See the header.
    PERFORM seed_data_entry_tds_register_rules(p_company_id);

    RETURN owner_level_id;
END
$$;

GRANT EXECUTE ON FUNCTION seed_company_auth_defaults(UUID, UUID) TO tally_app;
