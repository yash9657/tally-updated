-- 0028_data_entry_stock_report_rules.sql
-- Keep the COST BOOK away from the 'Data Entry' level on the four inventory
-- reports mounted alongside the Stock Summary.
--
-- WHAT CHANGED, AND WHY THIS IS NOT OPTIONAL PAPERWORK
-- ---------------------------------------------------
-- Four reports that already existed in internal/reporting are now reachable
-- over HTTP:
--
--   GET .../stock-movement       what moved in a period, with cost consumed
--   GET .../godown-summary       the position by LOCATION, valued
--   GET .../batch-summary        the position by BATCH, valued, expiry-banded
--   GET .../stock-item-vouchers  one item's voucher lines, rate and amount
--
-- Migration 0026 established the rule for exactly this situation. The seeded
-- Data Entry level carries the COARSE grant `allow display report` (from 0011),
-- and the PDP's resource matcher matches a bare `report` rule against any
-- request for report:NAME — so EVERY report added to this product is handed to
-- every operator in every tenant BY INHERITANCE, without anyone deciding to.
-- 0026 wrote the first counterweight (`disallow display report:stock-summary`)
-- because the Stock Summary's closing rate is the company's own cost price. A
-- rule naming report:NAME scores specificity 2 against the bare rule's 1, so
-- the disallow wins (internal/authz, matchResource).
--
-- All four reports above have the SAME exposure, and two of them are worse.
-- Without this migration, mounting them would have silently widened what an
-- operator can see, and nothing would have failed.
--
-- THE JUDGEMENT, REPORT BY REPORT
-- -------------------------------
--   * report:godown-summary — DISALLOWED. Every cell is a closing VALUATION at
--     the location's own weighted-average cost, and the column footers are what
--     each warehouse is worth. It is the Stock Summary sliced sideways; the
--     figure that made 0026 necessary is on every row of it.
--
--   * report:batch-summary — DISALLOWED, and this is the one that costs the
--     business something real. An operator picking a batch to ship genuinely
--     needs to know what is expiring (FEFO is a data-entry decision, not a
--     management one). But every row carries a closing value AND a closing
--     RATE at the batch grain — a rate per lot is a sharper disclosure than the
--     item average, because it is what one specific delivery was bought for.
--     There is no column-level ACL, so the choice is all or nothing and the
--     costlier mistake is the one that leaks. The honest fix is the same one
--     0026 recorded: a QUANTITY-ONLY expiry view an operator can see. That is a
--     new report with its own name, not a relaxation of this rule.
--
--   * report:stock-movement — DISALLOWED. The outward column is COST OF GOODS
--     SOLD for the window, and inward value is what the goods were bought for.
--     Two of those printed side by side for a quarter is the company's gross
--     margin, derivable by anyone who can also see a sales figure.
--
--   * report:stock-item-vouchers — DISALLOWED, and it is the bluntest of the
--     four. Its lines carry rate AS ENTERED, so an INWARD line is a PURCHASE
--     PRICE stated outright, next to the supplier's name in the party column.
--     That is not the averaged cost the other reports round off — it is what a
--     competitor who hires an operator would most like to receive with them.
--
-- NO REPORT MOUNTED IN THIS WAVE CARRIES QUANTITY WITHOUT VALUE, so no report
-- is left permitted here. If one had — a purely quantitative "what is on hand
-- to sell" view — it would deliberately NOT be listed below, because "do we
-- have ten to ship?" is a question the person keying the invoice has to answer
-- and answering it discloses nothing about margins or suppliers.
--
-- WHAT IS UNCHANGED: the pickers stay open. GET .../godowns, /batches,
-- /stock-items and /units map to (display, master), which migration 0022
-- granted; an operator still picks from the masters a supervisor defined and
-- still cannot mint one. An Owner who WANTS an operator to see one of these
-- reports deletes the corresponding rule in Users & Roles — that admin surface
-- already exists.

CREATE OR REPLACE FUNCTION seed_data_entry_stock_report_rules(p_company_id UUID)
RETURNS VOID AS $$
DECLARE
    de_level_id UUID;
    res         TEXT;
BEGIN
    SELECT id INTO de_level_id
      FROM security_level
     WHERE company_id = p_company_id AND name = 'Data Entry';
    IF de_level_id IS NULL THEN
        RETURN;   -- company has no Data Entry level; nothing to narrow
    END IF;

    -- One rule per report NAME. Naming them individually (rather than
    -- disallowing the bare `report` resource) is what keeps the Trial Balance,
    -- the Day Book and the outstandings an operator legitimately works from
    -- reachable: a blanket disallow at the coarse grain would take the whole
    -- reporting surface away, and the level would stop being able to check its
    -- own work.
    FOREACH res IN ARRAY ARRAY[
        'report:stock-movement',
        'report:godown-summary',
        'report:batch-summary',
        'report:stock-item-vouchers'
    ] LOOP
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

GRANT EXECUTE ON FUNCTION seed_data_entry_stock_report_rules(UUID) TO tally_app;

-- Backfill every existing company, so a tenant created before this wave gets
-- the same rule set a tenant created after it does. Two companies whose
-- security levels differ only by the date they were created is the failure mode
-- every one of these seed migrations exists to prevent — and here it would mean
-- the cost book is confidential in tenants onboarded next week and public in
-- the ones onboarded last month.
-- acl_rule HAS NEVER HAD AN INDEX BEYOND ITS PRIMARY KEY, and that is a bug on
-- the hottest path in the product, not merely a migration inconvenience.
--
-- internal/authz/store.go loads a level's rules with
-- `SELECT ... FROM acl_rule WHERE security_level_id = $1` on EVERY
-- authorization decision — that is, on every authenticated request. With only
-- acl_rule_pkey (on id) that is a sequential scan of the whole table: ~165k rows
-- on this project's dev database today, and it grows with (companies x levels x
-- rules), so it degrades as the fleet grows rather than with any one tenant's
-- size. The admin surface's `WHERE company_id = $1` read has the same problem.
--
-- It was found because the backfill below ran for nearly two hours: 5,106 Data
-- Entry levels x 4 names, each probing acl_rule by security_level_id, is 20k
-- probes that the planner served with a PARALLEL SEQUENTIAL SCAN of the whole
-- table. Measured with EXPLAIN (ANALYZE, BUFFERS) on the same probe: with these
-- indexes it is a bitmap heap scan touching 3 buffers; with index scans
-- disabled it is a Gather over two parallel workers. The set-based rewrite
-- makes the migration fast, but the missing index is the real defect, it was
-- there before inventory existed, and it is on the authz path of every
-- authenticated request rather than only on this migration.
CREATE INDEX IF NOT EXISTS acl_rule_level   ON acl_rule (security_level_id);
CREATE INDEX IF NOT EXISTS acl_rule_company ON acl_rule (company_id);

-- SET-BASED, NOT A PER-COMPANY LOOP.
--
-- The obvious shape here — and the shape 0022 and 0026 both used — is
-- `FOR c IN SELECT company_id ... LOOP PERFORM seed_...(c.company_id) END LOOP`.
-- That is one round trip per company per name, each carrying its own EXISTS
-- probe against acl_rule. With the index below now in place it would be
-- tolerable; without it, it was not. Either way one INSERT ... SELECT does the
-- same work as a single set operation, and the anti-join on acl_rule is what
-- makes it idempotent, replacing the per-row EXISTS.
--
-- Write every seed migration re-runnable regardless. `cored migrate` DOES apply
-- each file atomically (migrations.go applyOne wraps the body and the
-- schema_migrations stamp in one transaction), but that guarantee only covers
-- that one path: applying a migration by hand with `psql -f` has no such
-- envelope, and this file's own history is the example — a by-hand apply during
-- development left its rows committed with nothing stamped, and only the
-- idempotence of this statement made that recoverable.

INSERT INTO acl_rule (id, company_id, security_level_id, effect, action, resource)
SELECT gen_random_uuid(), sl.company_id, sl.id, 'disallow', 'display', r.res
  FROM security_level sl
 CROSS JOIN (VALUES
        ('report:stock-movement'),
        ('report:godown-summary'),
        ('report:batch-summary'),
        ('report:stock-item-vouchers')
 ) AS r(res)
 WHERE sl.name = 'Data Entry'
   AND NOT EXISTS (
        SELECT 1 FROM acl_rule ar
         WHERE ar.security_level_id = sl.id
           AND ar.effect = 'disallow' AND ar.action = 'display'
           AND ar.resource = r.res
   );

-- Fold it into the per-company auth seed so NEW companies get it at creation.
-- The body mirrors 0026's definition with the single added PERFORM at the end;
-- as there, the rule set for a new grant is extended in ONE place, not two.
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
    -- The other four inventory reports (this migration).
    PERFORM seed_data_entry_stock_report_rules(p_company_id);

    RETURN owner_level_id;
END
$$;

GRANT EXECUTE ON FUNCTION seed_company_auth_defaults(UUID, UUID) TO tally_app;
