-- 0022_data_entry_can_read_the_company_it_keys.sql
-- Grant the seeded 'Data Entry' level DISPLAY on `company` and on `master`.
--
-- THE DECISION, AND WHY IT IS A DECISION
-- --------------------------------------
-- This was left open on purpose (HANDOFF §10.1 item F): the SERVER was correct —
-- nobody had granted the right, and default-deny denied it — but the seeded rule
-- set was incomplete for the client that exists, so **the product was not usable
-- end to end as a Data Entry operator**. Two rules were missing:
--
--   display company — GET /v1/companies/{id} maps to (display, company), and the
--     web client hydrates the home GST state from exactly that endpoint to
--     derive the intra/inter-state split in the browser. Without it an operator
--     signs in, gets a 403 on load, and cannot key a GST voucher correctly.
--
--   display master — GET .../ledgers is (display, master), and the voucher
--     screen's party/ledger picker reads the real chart of accounts from it.
--     PICKING an account is what data entry IS.
--
-- WHY GRANTING THEM IS RIGHT, not merely convenient:
--
--   * The company master carries nothing an operator does not already see on
--     every invoice they type — name, address, GSTIN, state. Withholding the
--     home state does not protect it; it just makes the operator compute the
--     wrong tax.
--   * Reading the chart of accounts is not the same as changing it. There is
--     deliberately NO `create master` or `alter master` rule here, so an
--     operator still picks from the accounts a supervisor defined and cannot
--     mint a new ledger — which is the restriction that actually matters, and
--     is Tally's own division of labour for this role.
--   * Both are DISPLAY, so neither inherits the back-dated cutoff or the
--     WebAuthn step-up, and neither widens what the operator may WRITE.
--
-- What is still denied is unchanged and deliberate: no delete anywhere, no
-- `security` (administering users is not data entry), no `import`, no `sync`,
-- no `query`, and no master creation.

CREATE OR REPLACE FUNCTION seed_data_entry_read_rules(p_company_id UUID)
RETURNS VOID AS $$
DECLARE
    de_level_id UUID;
    res         TEXT;
BEGIN
    SELECT id INTO de_level_id
      FROM security_level
     WHERE company_id = p_company_id AND name = 'Data Entry';
    IF de_level_id IS NULL THEN
        RETURN;   -- company has no Data Entry level; nothing to extend
    END IF;

    FOREACH res IN ARRAY ARRAY['company', 'master'] LOOP
        -- Idempotent at the (level, effect, action, resource) grain so a re-run
        -- never duplicates a rule.
        IF NOT EXISTS (
            SELECT 1 FROM acl_rule
             WHERE security_level_id = de_level_id
               AND effect = 'allow' AND action = 'display' AND resource = res
        ) THEN
            INSERT INTO acl_rule (id, company_id, security_level_id,
                                  effect, action, resource)
            VALUES (gen_random_uuid(), p_company_id, de_level_id,
                    'allow', 'display', res);
        END IF;
    END LOOP;
END;
$$ LANGUAGE plpgsql;

GRANT EXECUTE ON FUNCTION seed_data_entry_read_rules(UUID) TO tally_app;

-- Backfill every existing company, so older tenants are not left with a Data
-- Entry level that cannot open the product.
DO $$
DECLARE
    c RECORD;
BEGIN
    FOR c IN SELECT DISTINCT company_id FROM security_level WHERE name = 'Data Entry' LOOP
        PERFORM seed_data_entry_read_rules(c.company_id);
    END LOOP;
END
$$;

-- Fold it into the per-company auth seed so NEW companies get it at creation.
-- The body mirrors 0019's definition with the single added PERFORM at the end;
-- as there, the rule set for a new grant is added in ONE place, not two.
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
    -- The reads an operator needs to open the product at all (this migration).
    PERFORM seed_data_entry_read_rules(p_company_id);

    RETURN owner_level_id;
END
$$;

GRANT EXECUTE ON FUNCTION seed_company_auth_defaults(UUID, UUID) TO tally_app;
