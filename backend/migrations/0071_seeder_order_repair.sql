-- 0071_seeder_order_repair.sql
-- ⛔ THIS IS A SECURITY REPAIR, NOT A FEATURE. On any FRESH database, every
-- company created after migration hands its Data Entry level the IMPORTED BANK
-- STATEMENT — the bank's own file of every counterparty, balance and narration.
--
-- Reserved as T6's write-path ACL block in PLAN-TALLY-PARITY.md §2.5. T6 turned
-- out to need NO ACL rows (see the last section of this header), and this defect
-- was found while establishing that. The number is spent on the repair instead.
--
-- ===========================================================================
-- WHAT HAPPENED: A MIGRATION WAS NUMBERED BELOW ITS OWN BASE
-- ===========================================================================
-- seed_company_auth_defaults can only be extended by RESTATING its whole
-- plpgsql body, so every restatement is a chance to lose a predecessor's rule
-- set. 0046, 0068 and 0063 all carry the same warning in their headers: "Two
-- migrations that each restate this function from a different base silently
-- DROP the other's PERFORM, and nothing fails."
--
-- Both files OBEYED that rule. Neither derived from a stale base:
--
--   0068_tds_register_acl.sql   authored 2026-08-02, derived from 0046,
--                               10 PERFORMs, adds tds_register.
--   0063_bank_statement_acl.sql authored 2026-08-03, derived from 0068 (its
--                               header says so at line 149), 11 PERFORMs,
--                               adds bank_statement.
--
-- 0063 was written a DAY LATER and derived from the right base. Its only defect
-- is its NUMBER: migrations.go:71 applies files in `sort.Strings` order, and
-- '0063' < '0068'. So on a fresh database 0063's correct 11-PERFORM body is
-- installed first and 0068's older 10-PERFORM body then CREATE OR REPLACEs it.
-- The last writer wins and the last writer is the earlier author.
--
-- ⚠️ THE RULE EVERY HEADER STATES IS NECESSARY BUT NOT SUFFICIENT. "Derive from
-- the immediately preceding restatement" silently assumes the new file will
-- SORT after it. Deriving correctly and numbering below is indistinguishable
-- from deriving from a stale base, because CREATE OR REPLACE order is decided by
-- the filename and not by authorship. The rule for the next one is therefore:
-- DERIVE FROM THE HIGHEST-NUMBERED RESTATEMENT AND TAKE A HIGHER NUMBER THAN IT.
-- A reserved block below an existing restatement cannot be used for a seeder
-- change — leave the gap unused and take a number at the end, exactly as
-- PLAN-TALLY-PARITY.md §2.5 already says for table numbering.
--
-- ===========================================================================
-- WHY NOTHING CAUGHT IT, INCLUDING THE TEST WRITTEN TO CATCH IT
-- ===========================================================================
-- plpgsql does not resolve function identifiers at CREATE time, so 0063 can
-- PERFORM a function defined by a higher-numbered file and still apply cleanly.
-- There is no compile error, no failing migration, and no test that enumerates
-- the seeded rule set.
--
-- TestDataEntryIsRefusedTheBankStatement (internal/server/handler_bank_statement_test.go)
-- creates its company AT TEST TIME and is exactly the right test. It passes on
-- the shared dev Postgres for a reason that has nothing to do with the code:
-- 0068 was stamped there on 2026-08-01 and 0063 on 2026-08-02, so on THAT
-- database 0063 ran last and the correct body is live. The test is green on
-- every developer machine and would have been red on the first fresh deploy.
--
-- MEASURED, not reasoned about, on 2026-08-04 (a scratch database, migrated
-- from empty with `cored migrate`, then one company seeded):
--
--   fresh DB, prosrc of seed_company_auth_defaults
--     -> MISSING seed_data_entry_bank_statement_rules   (0068's body won)
--   fresh DB, seeded company, Data Entry rules matching 'report%'
--     -> 29 rows: `allow display report` plus disallows for bank-reconciliation,
--        batch-summary, godown-summary, ims-inbox, itc-reconciliation, five
--        payroll reports, four stock reports and tds-register.
--        NO report:bank-statement row, in either action.
--
-- 0011 seeds the coarse `allow display report`, which internal/authz matches
-- against ANY report:NAME at specificity 1. With no specificity-2 counterweight
-- the statement is GRANTED. That is the whole mechanism 0026/0028/0033/0035/
-- 0046/0063/0068 exist to defeat, defeated here by a filename.
--
-- ===========================================================================
-- THE REPAIR
-- ===========================================================================
-- Both halves, because either alone is a half-measure:
--
--   1. BACKFILL. A fresh deployment that created companies under the broken
--      seeder has companies that lack the rule right now. 0063's backfill
--      already ran for them and found nothing to do, because they did not exist
--      yet. Re-run it, anti-joined so it is a no-op everywhere else.
--   2. RESTATE THE SEEDER so new companies get it. The body below is 0063's,
--      the highest-numbered CORRECT restatement, carried forward verbatim with
--      all ELEVEN PERFORMs. Nothing is added: this migration introduces no rule
--      of its own.
--
-- RE-RUNNABLE: CREATE OR REPLACE, and the backfill is a single anti-joined
-- statement rather than a per-company loop (0028's header records a loop
-- reaching 13% of 38,748 companies in 1h57m before it was killed).
--
-- ⛔ THE NEXT RESTATEMENT DERIVES FROM THIS FILE, AND MUST SORT ABOVE 0071.
--
-- ===========================================================================
-- WHY T6's OWN RESOURCES NEED NO ROWS HERE
-- ===========================================================================
-- The TDS challan register and the deductor return profile are BARE RESOURCE
-- TOKENS (`tds-challan`, `tds-profile`), not `report:NAME`s. Measured on the
-- same fresh database: the seeded Data Entry level has default_access = 'none',
-- and every rule it carries names a specific token. No coarse rule reaches a
-- non-report resource, so a token that has never been mentioned is refused by
-- default_access — authz.go's deny-by-default. authz/vocabulary.go:188-196
-- records the identical decision for `deductor-status`, and 0033's header for
-- the three ITC writes.
--
-- So Data Entry is refused both of T6's routes with no rule written, and the
-- profile row that carries a named individual's PAN is protected by the default
-- rather than by a migration. A `report:NAME` would have been the opposite case
-- and MANDATORY, which is why the shape was chosen deliberately and not by
-- analogy with the register that sits beside it.
--
-- If an administrator later wants a clerk recording challans, that is an
-- explicit `allow create tds-challan` written through the admin API — a
-- decision, made by the tenant, visible in acl_rule. It is not a default.

-- ---------------------------------------------------------------------------
-- 1. BACKFILL. Repairs any company created under the dropped-PERFORM seeder.
--    bank_statement_acl_grants() is 0063's single definition of the rule set,
--    read by both its seeder and its backfill; this reads the same one rather
--    than restating the pair of rules and risking a third version of them.
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
-- 2. RESTATE THE SEEDER. Derived verbatim from 0063_bank_statement_acl.sql
--    (lines 154-227), the highest-numbered correct restatement. ELEVEN
--    PERFORMs. Diff this against 0063 and the only difference must be comments.
--
-- The signature is byte-identical on purpose: CREATE OR REPLACE cannot change a
-- return type, and a changed argument list creates a SECOND overload instead of
-- replacing, which would make authz/store.go's `seed_company_auth_defaults($1,$2)`
-- ambiguous at company creation rather than at migration time.
--
-- The PERFORMs stay at the very END of the body, after the coarse-seed block and
-- after the company_membership insert. The coarse block is guarded by
-- `IF NOT EXISTS (SELECT 1 FROM acl_rule WHERE security_level_id = de_level_id)`:
-- a PERFORM hoisted above it would insert the level's first rule, the guard would
-- then see rows, and the six voucher classes plus `allow display report` would
-- SILENTLY NEVER BE SEEDED — every new tenant's Data Entry level unable to key an
-- invoice, with no migration test to catch it.
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
    -- THE IMPORTED BANK STATEMENT: two refusals, no allow (migration 0063). It
    -- is the bank's own file of every counterparty.
    --
    -- ⛔ THIS IS THE LINE 0068 DROPPED AND THE REASON THIS FILE EXISTS. It is
    -- last because 0063 put it last; keep it, and keep every line above it, when
    -- you derive the next restatement from here.
    PERFORM seed_data_entry_bank_statement_rules(p_company_id);

    RETURN owner_level_id;
END
$$;

GRANT EXECUTE ON FUNCTION seed_company_auth_defaults(UUID, UUID) TO tally_app;
