-- 0033_itc_report_acl.sql
-- Keep the SUPPLIER RECONCILIATION and the IMS INBOX away from the
-- 'Data Entry' level -- written in the same wave that builds them, so the
-- rules exist before the routes do rather than after somebody notices.
--
-- WHY THIS MIGRATION HAS TO EXIST AT ALL
-- --------------------------------------
-- Migration 0011 seeds the 'Data Entry' level with the COARSE grant
-- `allow display report`. internal/authz's resource matcher matches a bare
-- `report` rule against a request for ANY `report:NAME` -- so every report ever
-- added to this product is handed to every operator in every tenant BY
-- INHERITANCE, without anybody deciding to. 0026 wrote the first counterweight
-- and 0028 wrote four more, each for a report whose every row prints the
-- company's own cost book.
--
-- Note what is NOT needed here, because it is easy to over-seed out of caution:
-- the seeded Data Entry level has default_access = 'none', so a brand-new
-- NON-report resource token (the ingest endpoint, an IMS action) is
-- DEFAULT-DENIED already and needs no rule at all. It is ONLY the coarse
-- report grant that leaks, and it leaks only to report NAMES. So this migration
-- seeds exactly two rows per level and nothing else. Writing more would be
-- noise that hides the two that matter.
--
-- A rule naming report:NAME scores specificity 2 against the bare grant's 1, so
-- the disallow wins (internal/authz, matchResource / ruleSpecificity). Same
-- mechanism 0026 and 0028 rely on.
--
-- ===========================================================================
-- THE JUDGEMENT, REPORT BY REPORT
-- ===========================================================================
--   * report:itc-reconciliation -- DISALLOWED.
--     This is the books-versus-2B match. Every line of it carries a NAMED
--     SUPPLIER, that supplier's GSTIN, the invoice number and date they issued,
--     and the taxable value we paid them -- for every purchase in the period,
--     on one screen, with the arithmetic already done. It is the company's
--     entire supplier book and its purchase prices in a single export. The five
--     reports 0026 and 0028 withheld leak the AVERAGED cost of goods; this one
--     leaks who we buy from, at what, per document. It is strictly the sharper
--     disclosure, and the operator's job -- keying the purchase bill in front of
--     them -- needs none of it.
--
--     There is a real cost to this, and it should be recorded rather than
--     glossed: the person who keys purchases is often the person best placed to
--     notice "supplier X has not filed our March bills". Withholding the report
--     pushes that noticing up to whoever can see it. The honest fix is the same
--     one 0026 recorded for the expiry view -- a narrow "these of MY entries
--     have no matching supplier document" screen, keyed to the operator's own
--     work, with no prices and no other supplier's rows. That is a NEW report
--     with its own name and its own decision, not a relaxation of this rule.
--
--   * report:ims-inbox -- DISALLOWED, and this one is not about disclosure.
--     The IMS inbox is where a document is ACCEPTED, REJECTED or held PENDING,
--     and that action is what decides whether its input tax credit is claimed.
--     Rejecting a supplier's genuine invoice silently forfeits real money;
--     accepting one that should have been rejected claims credit that is not
--     ours, which is the direction that draws a notice and interest. The action
--     endpoints are default-denied by default_access='none' already, but the
--     INBOX itself is disallowed too, because a read-only view of "which of our
--     suppliers' invoices are unmatched, and what we did about each" is the
--     filer's working paper for the return -- and it names, per supplier, where
--     we think they are wrong. That is a conversation between the person who
--     signs the return and the supplier, not a screen on the data-entry desk.
--
-- WHY AN EXPLICIT `disallow` AND NOT SILENCE
-- ------------------------------------------
-- Silence would NOT deny these: the coarse `allow display report` from 0011
-- matches them today. Unlike 0031's stock-voucher rules -- which changed no
-- answer when they were written and existed only to survive a future wildcard
-- -- these two rows change the answer THE MOMENT the routes are mounted. If
-- this migration is not applied before the wiring wave lands, every operator in
-- every tenant gets the supplier book on the day the route appears.
--
-- WHAT THIS MIGRATION DELIBERATELY DOES NOT DO
-- --------------------------------------------
-- It seeds NOTHING for the Owner level (default_access 'full' already answers
-- yes), it creates no new resource tokens, and it touches no other level -- a
-- tenant that has built its own 'Accounts' or 'Compliance' level has made its
-- own decision and this migration must not second-guess it. The set-based
-- statement below is filtered on name = 'Data Entry' for exactly that reason.

-- The grant table as a function, so the per-company seeder and the fleet
-- backfill read from ONE definition. Two copies of the list is how a tenant
-- created next week ends up with a different rule set from one created last
-- month -- which here would mean the supplier book is operator-visible in some
-- tenants and not others.
CREATE OR REPLACE FUNCTION itc_report_acl_grants()
RETURNS TABLE (effect TEXT, action TEXT, resource TEXT) AS $$
    SELECT * FROM (VALUES
        ('disallow', 'display', 'report:itc-reconciliation'),
        ('disallow', 'display', 'report:ims-inbox')
    ) AS v(effect, action, resource);
$$ LANGUAGE sql IMMUTABLE;

-- ---------------------------------------------------------------------------
-- The per-company seeder. Idempotent at the (level, effect, action, resource)
-- grain, so a re-run inserts nothing -- `cored migrate` applies each file in its
-- own transaction, but a by-hand `psql -f` has no such envelope, and a run
-- killed halfway leaves its rows behind WITHOUT stamping schema_migrations.
-- Only idempotence makes that recoverable; 0028's header records the run that
-- proved it.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION seed_data_entry_itc_report_rules(p_company_id UUID)
RETURNS VOID AS $$
DECLARE
    de_level_id UUID;
    rec         RECORD;
BEGIN
    SELECT id INTO de_level_id
      FROM security_level
     WHERE company_id = p_company_id AND name = 'Data Entry';
    IF de_level_id IS NULL THEN
        RETURN;   -- company has no Data Entry level; nothing to restrict
    END IF;

    FOR rec IN SELECT * FROM itc_report_acl_grants() LOOP
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

GRANT EXECUTE ON FUNCTION itc_report_acl_grants() TO tally_app;
GRANT EXECUTE ON FUNCTION seed_data_entry_itc_report_rules(UUID) TO tally_app;

-- ---------------------------------------------------------------------------
-- Backfill EVERY existing company, as ONE set-based statement.
--
-- NOT a per-company loop calling the function above. 0028's header records what
-- that costs on this project's own database: 38,748 companies x 4 resource
-- names reached 13% in ONE HOUR AND FIFTY-SEVEN MINUTES before it was killed,
-- while starving the connection pool. The anti-join replaces the per-row EXISTS
-- and is what makes re-running a no-op.
--
-- Without the backfill, a company created before this migration would expose
-- the supplier book to its operators while one created after would not -- two
-- companies whose security differs only by the date they were created, which is
-- the failure mode every one of these seed migrations exists to prevent.
-- ---------------------------------------------------------------------------
INSERT INTO acl_rule (id, company_id, security_level_id, effect, action, resource)
SELECT gen_random_uuid(), sl.company_id, sl.id, g.effect, g.action, g.resource
  FROM security_level sl
 CROSS JOIN itc_report_acl_grants() AS g
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
-- 0029 and 0031 restated theirs: a fresh database applying 0011 -> 0019 -> 0022
-- -> 0026 -> 0028 -> 0031 -> 0033 must end with ONE complete, self-describing
-- definition. The only change from 0031's version is the final PERFORM.
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
    -- The supplier reconciliation and the IMS inbox (this migration).
    PERFORM seed_data_entry_itc_report_rules(p_company_id);

    RETURN owner_level_id;
END
$$;

GRANT EXECUTE ON FUNCTION seed_company_auth_defaults(UUID, UUID) TO tally_app;
