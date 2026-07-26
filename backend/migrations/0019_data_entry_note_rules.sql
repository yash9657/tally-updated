-- 0019_data_entry_note_rules.sql
-- Extend the seeded 'Data Entry' security level to the CREDIT NOTE and DEBIT
-- NOTE voucher classes.
--
-- THE GAP THIS CLOSES
-- -------------------
-- 0011 seeded Data Entry with allow(create/alter/display) for exactly six
-- voucher classes: sales, purchase, receipt, payment, contra, journal. Migration
-- 0018 then added two MORE classes (credit-note, debit-note) without touching
-- the ACL. Because the level is default_access = 'none', "no rule" means DENY —
-- so a data-entry operator pressing Alt+F6 to raise a sales return got a bare
-- 403 with no indication that the cause was a missing ACL row rather than a
-- broken screen.
--
-- A sales/purchase return is ordinary data-entry work: an operator who may key
-- an invoice may key the note that corrects it. They still may NOT delete one —
-- there is deliberately no delete rule here, matching the other six classes, so
-- reversing a posted document remains an Owner action.
--
-- This is the failure mode a default-deny policy is SUPPOSED to have (new
-- surface is inaccessible until explicitly granted, rather than silently
-- world-readable). The lesson is that adding a voucher class is a TWO-file
-- change: the class, and the levels permitted to use it.

CREATE OR REPLACE FUNCTION seed_data_entry_note_rules(p_company_id UUID)
RETURNS VOID AS $$
DECLARE
    de_level_id UUID;
    cls         TEXT;
    act         TEXT;
BEGIN
    SELECT id INTO de_level_id
      FROM security_level
     WHERE company_id = p_company_id AND name = 'Data Entry';
    IF de_level_id IS NULL THEN
        RETURN;   -- company has no Data Entry level; nothing to extend
    END IF;

    FOREACH cls IN ARRAY ARRAY['credit-note', 'debit-note'] LOOP
        FOREACH act IN ARRAY ARRAY['create', 'alter', 'display'] LOOP
            -- Idempotent at the (level, effect, action, resource) grain so a
            -- re-run never duplicates a rule.
            IF NOT EXISTS (
                SELECT 1 FROM acl_rule
                 WHERE security_level_id = de_level_id
                   AND effect = 'allow' AND action = act AND resource = cls
            ) THEN
                INSERT INTO acl_rule (id, company_id, security_level_id,
                                      effect, action, resource)
                VALUES (gen_random_uuid(), p_company_id, de_level_id,
                        'allow', act, cls);
            END IF;
        END LOOP;
    END LOOP;
END;
$$ LANGUAGE plpgsql;

GRANT EXECUTE ON FUNCTION seed_data_entry_note_rules(UUID) TO tally_app;

-- Backfill every existing company, so older tenants are not left with a Data
-- Entry level that cannot reach the note screens.
DO $$
DECLARE
    c RECORD;
BEGIN
    FOR c IN SELECT DISTINCT company_id FROM security_level WHERE name = 'Data Entry' LOOP
        PERFORM seed_data_entry_note_rules(c.company_id);
    END LOOP;
END
$$;

-- Fold it into the per-company auth seed so NEW companies get it at creation.
-- The body mirrors 0011's definition with the single added PERFORM at the end.
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

    -- The note classes (migration 0018/0019). Kept as a separate call so the
    -- rule set for a new class is added in ONE place, not two.
    PERFORM seed_data_entry_note_rules(p_company_id);

    RETURN owner_level_id;
END
$$;

GRANT EXECUTE ON FUNCTION seed_company_auth_defaults(UUID, UUID) TO tally_app;
