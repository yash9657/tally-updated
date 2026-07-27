-- 0026_data_entry_inventory_rules.sql
-- Grant the seeded 'Data Entry' level DISPLAY on the inventory position report.
--
-- WHAT DATA ENTRY NEEDS IN THE INVENTORY WORLD
-- -------------------------------------------
-- Wave 1 gave the product stock: godowns and batches are masters (migration
-- 0025), an inventory line is written inside ledger.post(), and GET
-- .../stock-summary derives every position by replaying the movement ledger.
-- An operator keying an item invoice needs to PICK an item, a location and a
-- batch, and needs to see what is actually on hand before they promise to ship
-- it. Picking is what data entry IS.
--
-- The pickers are already covered: routeAction maps GET .../godowns, /batches,
-- /stock-items and /units to (display, master), which migration 0022 granted to
-- this level for exactly the same reason (the party/ledger picker). Read and
-- write of a master deliberately share the `master` resource token, so that one
-- rule governs both and the two cannot drift; there is still NO `create master`
-- rule here, so an operator picks from the locations and batches a supervisor
-- defined and cannot mint one.
--
-- WHAT THIS MIGRATION ADDS, AND WHY IT IS NARROW
-- ---------------------------------------------
-- One rule: `allow display report:stock-summary`.
--
-- BE HONEST ABOUT ITS EFFECT TODAY: on a freshly seeded company this rule
-- grants nothing that is not already permitted. seed_company_auth_defaults
-- gives Data Entry the COARSE rule `allow display report`, and the PDP's
-- resource matcher (internal/authz, matchResource) matches a bare `report` rule
-- against a request for report:stock-summary. So the endpoint is reachable
-- either way, and this migration is not what makes it reachable.
--
-- It is written anyway, at the SPECIFIC grain, because the PDP resolves by
-- most-specific match: `report:NAME` scores above a bare `report`. That is what
-- makes the inventory position survivable as its own decision. The coarse grant
-- is the thing that should eventually be narrowed — as written, EVERY report
-- added to the product in the future is handed to every operator in every
-- tenant without anyone deciding to — and when it is narrowed, the reads an
-- operator needs must already be named individually or the narrowing silently
-- takes the item grid's companion report away with it.
--
-- What stays denied is unchanged and deliberate: no `create master` (so no
-- minting godowns, batches, ledgers or items), no delete anywhere, no
-- `security`, no `import`, no `sync`, no `query`.

CREATE OR REPLACE FUNCTION seed_data_entry_inventory_rules(p_company_id UUID)
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

    -- DISALLOW, not allow. The Data Entry level already carries the COARSE rule
    -- `allow display report` (seeded since 0011), and matchResource matches a
    -- bare `report` rule against any report:NAME — so an explicit allow here
    -- would be a no-op, and EVERY report added to this product from now on is
    -- handed to every operator in every tenant without anyone deciding to.
    --
    -- The Stock Summary is the first report where that is actively wrong. It
    -- prints the company's own COST prices: the closing valuation rate is what
    -- the business PAID, which is commercially sensitive in a way the chart of
    -- accounts is not. An operator keying invoices has no need of it, and a
    -- competitor who hires one should not receive it with the job.
    --
    -- A rule naming `report:stock-summary` has specificity 2 against the bare
    -- rule's 1, so this disallow WINS (internal/authz, matchResource). An Owner
    -- who wants an operator to see it deletes this rule in Users & Roles — the
    -- admin surface for that already exists and is gated by verify:tenancy.
    --
    -- Tracked follow-up (HANDOFF §6): the honest fix is a quantity-only Stock
    -- Summary an operator CAN see, since "is there stock to sell?" is a real
    -- question they need answered. Column-level ACL is not built.
    FOREACH res IN ARRAY ARRAY['report:stock-summary'] LOOP
        -- Idempotent at the (level, effect, action, resource) grain so a re-run
        -- never duplicates a rule.
        IF NOT EXISTS (
            SELECT 1 FROM acl_rule
             WHERE security_level_id = de_level_id
               AND effect = 'disallow' AND action = 'display' AND resource = res
        ) THEN
            INSERT INTO acl_rule (id, company_id, security_level_id,
                                  effect, action, resource)
            VALUES (gen_random_uuid(), p_company_id, de_level_id,
                    'disallow', 'display', res);
        END IF;
    END LOOP;
END;
$$ LANGUAGE plpgsql;

GRANT EXECUTE ON FUNCTION seed_data_entry_inventory_rules(UUID) TO tally_app;

-- Backfill every existing company, so a tenant created before wave 1 gets the
-- same rule set a tenant created after it does. Two companies whose security
-- levels differ only by the date they were created is the failure mode every
-- one of these seed migrations exists to prevent.
DO $$
DECLARE
    c RECORD;
BEGIN
    FOR c IN SELECT DISTINCT company_id FROM security_level WHERE name = 'Data Entry' LOOP
        PERFORM seed_data_entry_inventory_rules(c.company_id);
    END LOOP;
END
$$;

-- Fold it into the per-company auth seed so NEW companies get it at creation.
-- The body mirrors 0022's definition with the single added PERFORM at the end;
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
    -- The reads an operator needs to open the product at all (migration 0022).
    PERFORM seed_data_entry_read_rules(p_company_id);
    -- The inventory position report (this migration).
    PERFORM seed_data_entry_inventory_rules(p_company_id);

    RETURN owner_level_id;
END
$$;

GRANT EXECUTE ON FUNCTION seed_company_auth_defaults(UUID, UUID) TO tally_app;
