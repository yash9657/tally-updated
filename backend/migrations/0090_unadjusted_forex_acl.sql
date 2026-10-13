-- 0090_unadjusted_forex_acl.sql
-- THE UNADJUSTED-FOREX REPORT IS THE COMPANY'S WHOLE CURRENCY EXPOSURE ON ONE
-- PAGE. Without this file, every seeded Data Entry operator can read it the moment
-- the report exists.
--
-- ===========================================================================
-- ⛔⛔ THE NUMBER IS 0090 AND **NOT** ONE OF T8's OWN RESERVED 0081-0084
-- ===========================================================================
-- PLAN-TALLY-PARITY.md §2.5 reserves 0081-0084 for T8, and this IS T8 -- so the
-- reservation's `⛔ DO NOT TAKE THESE` is addressed to other lanes, not to this
-- one. The number is still 0090, and the reason is apply order rather than
-- etiquette.
--
-- migrations.go applies files in `sort.Strings` order and the LAST
-- `CREATE OR REPLACE FUNCTION seed_company_auth_defaults` wins. A body installed
-- at 0081 would be overwritten on every fresh database by 0086's, then 0087's --
-- sixteen PERFORMs, none of them this one -- and the rules below would simply not
-- exist for any new company. ⛔ THAT IS NOT HYPOTHETICAL: it is exactly the defect
-- 0071 exists to repair, after 0063 derived its body correctly and took a number
-- five LOWER than 0068, handing every new tenant's Data Entry level the imported
-- bank statement.
--
-- ⭐ SO THE RULE IS: A FILE THAT RESTATES THE SEEDER TAKES A NUMBER AT THE END, and
-- a file that only creates a TABLE may take a reserved one. T8's schema took 0080
-- and needs nothing more; steps 4, 5, 6 and 7 have added no table at all.
-- 0081-0084 therefore stay FREE, and §2.5 has been updated in place to record that
-- T8's ACL number is 0090 and that the four reserved slots were never needed.
--
-- ===========================================================================
-- WHY THIS FILE HAS TO EXIST AT ALL: THE GRANT IS THE DEFAULT
-- ===========================================================================
-- Migration 0011 (line 249) seeds the Data Entry level a COARSE
-- `allow display report`, and internal/authz matches that BARE resource against
-- ANY `report:NAME`. So a new report is GRANTED to Data Entry the moment its name
-- is added to authz.KnownReportNames -- THE GRANT IS THE DEFAULT AND THE REFUSAL
-- IS THE THING THAT MUST BE WRITTEN. That is registered defect D95, and this is the
-- THIRTEENTH migration to write the refusal: 0026, 0028, 0033, 0035, 0046, 0063,
-- 0068, 0072, 0073, 0076, 0086, 0087 and now 0090.
--
-- ⭐ CONTRAST, BECAUSE THE CONTRAST IS THE DESIGN RULE: T8 step 5's
-- `forex-settings` route needed NO migration at all. It is a BARE token, not
-- `report:NAME`, against a level whose default_access is 'none' -- so both halves
-- were default-DENIED with zero acl_rule rows. THE SHAPE OF THE NAME DECIDES THE
-- DEFAULT, and only `report:NAME` arrives pre-granted. Steps 4 and 6 added no route
-- and no name and therefore needed nothing either; 0088's line-274 note is the
-- authority for a slice legitimately needing none.
--
-- ⚠️ THE SPECIFICITY ARITHMETIC, WHICH EIGHT OLDER HEADERS GET WRONG. Specificity is
-- the SUM of two terms in internal/authz/authz.go's ruleSpecificity:
--
--     +1 when the rule's ACTION matches exactly (matchAction, not a wildcard)
--     +0 / +1 / +2 for the RESOURCE (wildcard / exact `report` / exact `report:NAME`)
--
-- so `allow display report` scores **2** and `disallow display
-- report:unadjusted-forex` scores **3**. The rows below win. ⛔ 0076, 0086 and 0087
-- carry this correction; do not propagate the old "specificity 1" wording.
--
-- ===========================================================================
-- WHAT THIS REPORT ACTUALLY EXPOSES, since that is the whole justification
-- ===========================================================================
-- report:unadjusted-forex is not one more view of the ageing. Four things fall out
-- of one read:
--
--   * EVERY FOREIGN COUNTERPARTY, both sides, and what each still owes or is owed
--     IN ITS OWN CURRENCY. The ageing report a clerk may already reach shows the
--     rupee figure; this shows the currency and the amount, which is the figure a
--     competitor reads as "who they buy from abroad and how much".
--   * THE RATE EVERY DOCUMENT WAS RAISED AT, to ten decimals -- i.e. the terms this
--     company actually transacted on, bill by bill.
--   * THE WHOLE UNHEDGED EXPOSURE AS ONE SIGNED TOTAL. Nothing else in this product
--     produces that number, and it is a treasury figure rather than a bookkeeping
--     one.
--   * AND WHERE THE BOOKS ARE ALREADY WRONG BY OMISSION: the count of bills whose
--     currency has no rate on file, which is a list of the positions nobody can
--     price.
--
-- A data-entry clerk keying receipts needs none of it, and -- unlike the payables
-- ageing -- no form in this product asks for it. See the next section.
--
-- ===========================================================================
-- ⛔⛔ ONE NAME COVERING BOTH SIDES. THE OPPOSITE CALL FROM `bills-payable` (D94),
--     AND IT IS ARGUED RATHER THAN INHERITED.
-- ===========================================================================
-- The receivables and payables ageings are TWO report names because the PDP decides
-- on the report NAME and a query parameter is invisible to it: served only as
-- `/outstandings?kind=PAYABLE`, *"this level may see what customers owe us but not
-- what we owe suppliers"* was a rule nobody could write down. That reasoning is
-- real and it does NOT reach this report:
--
--   * THE SUBJECT IS THE **NET** EXPOSURE TO A CURRENCY, which is not separable by
--     side. A $1,000 receivable and a $1,000 payable at one rate are an exposure of
--     NOTHING. Two reports would show two numbers neither of which is the company's
--     position -- so splitting would not restrict information, it would destroy the
--     only figure the report exists to produce.
--   * AND NOTHING KEYS OFF IT. `bills-payable` ships with NO counterweight
--     precisely because a clerk keying a Payment NEEDS the payable ageing: the
--     bill-wise picker asks for it, and a clerk refused it settles ON ACCOUNT
--     instead -- the balance nobody can chase later. This report posts nothing and
--     feeds no form, so denying it costs no operator any work.
--
-- ⚠️ SO A TENANT THAT GRANTS IT GRANTS BOTH SIDES, and that is stated on the screen
-- rather than left to be discovered. A tenant wanting a one-sided exposure figure
-- does not want this report; they want the ageing, which they already have.
--
-- ===========================================================================
-- ⭐ THE POLICY DECISION IS A DENY, AND IT IS NOT RECORDED AS AN ASSUMPTION
-- ===========================================================================
-- 0086 recorded its deny as an ASSUMPTION because "should a data-entry clerk see a
-- budget-variance report" was on the owner's ask list and unanswered. ⛔ THAT HEDGE
-- IS NOT INHERITED HERE -- a caveat copied along with the SQL it sat beside is how
-- every migration in a family ends up equally uncertain.
--
-- The question here is not a judgement call about management information. 0072
-- already refuses departmental profitability and 0068 already refuses the full
-- supplier-and-customer list with amounts; this report is that list, restricted to
-- the foreign counterparties, PLUS the rates transacted on and the company's
-- unhedged position. It does not raise a new question.
--
-- ⛔ IT IS STILL ONE RULE TO REVERSE, and the mechanics are 0086's and 0087's: an
-- `allow display report:unadjusted-forex` written through the admin API TIES at
-- specificity 3 with the disallow below, and Disallow beats Allow at equal
-- specificity -- so the DISALLOW ROW MUST BE DELETED rather than out-voted. Stated
-- here so a reversal is done correctly rather than attempted with an allow that
-- silently loses.
--
-- ===========================================================================
-- ⚠️ THE `print` HALF IS DURABILITY -- BUT LESS SO HERE THAN IN ANY EARLIER FILE
-- ===========================================================================
-- Data Entry is ALREADY refused every print form today: 0038 seeds ZERO
-- `allow print report:NAME` rules, 0011's coarse grant is action 'display' only, and
-- the level's default_access is 'none'. So the `disallow print` row below changes
-- nothing observable right now, exactly as 0063/0068/0072/0073/0076/0086/0087 all
-- record of their own.
--
-- ⛔ BUT THIS ONE MATTERS MORE THAN THEIRS, AND THE DIFFERENCE IS WORTH THE LINE:
-- every report in that set answers 501 on the print path, so their `disallow print`
-- rows guard a route that produces nothing. THIS REPORT ACTUALLY PRINTS
-- (handler_print_reports.go's `unadjusted-forex` arm, T8 step 7), so the rule below
-- is the only thing standing between a coarse print grant and a PDF of the company's
-- entire currency exposure. A REPORT THAT CANNOT BE DISPLAYED BUT *CAN* BE PRINTED
-- IS A LEAK WITH A PAPER TRAIL -- and here the paper exists.
--
-- ⛔ A PROBE MUST NOT REPORT THESE TWO ROWS AS TWO EQUAL WINS. Only the `display`
-- row goes red-to-green. A drive asserting "Data Entry is 403 on both" passes
-- BEFORE this migration exists and proves nothing about it.
--
-- ⭐ AND UNLIKE 0087's, THE OWNER CONTROL ARM **CAN** ASSERT 200 ON THE PRINT PATH,
-- because the layout exists. That makes the print half of this file testable in a
-- way no earlier one was: Owner 200, Data Entry 403, on a route that really renders.
--
-- RE-RUNNABLE. migrations.go applyOne wraps each file in ONE transaction and stamps
-- schema_migrations INSIDE it, so a file that cannot be re-run walls off every later
-- migration.

-- ---------------------------------------------------------------------------
-- ONE definition of the rule set, read by BOTH the per-company seeder and the
-- backfill. Two copies is how a company created next week ends up with a
-- different rule set from one created last month.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION unadjusted_forex_acl_grants()
RETURNS TABLE (effect TEXT, action TEXT, resource TEXT) AS $$
    SELECT * FROM (VALUES
        -- NO ALLOW. See the header: the decision is a deny and it is NOT recorded
        -- as an assumption -- this is the foreign supplier and customer list with
        -- amounts and transacted rates, which 0068 and 0072 already refuse in their
        -- domestic form.
        --
        -- BOTH the display and the print form. ⛔ The `print` row is the only thing
        -- standing between a coarse print grant and a PDF of the whole exposure,
        -- because unlike every earlier file in this family THIS report really
        -- prints.
        ('disallow', 'display', 'report:unadjusted-forex'),
        ('disallow', 'print',   'report:unadjusted-forex')
    ) AS t(effect, action, resource);
$$ LANGUAGE sql IMMUTABLE;

-- ---------------------------------------------------------------------------
-- The per-company seeder, called when a company is created.
--
-- ⛔ THE NAME MUST MATCH `seed_data_entry_\w+` OR THE CENSUS CANNOT SEE IT.
-- migrations/seeder_census_test.go regexes exactly that shape to build its list of
-- rule seeders; a function named anything else is invisible to it, and a forgotten
-- PERFORM would then ship silently -- which is the 0071-class defect this whole
-- convention exists to prevent.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION seed_data_entry_unadjusted_forex_rules(p_company_id UUID)
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

    FOR rec IN SELECT * FROM unadjusted_forex_acl_grants() LOOP
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

GRANT EXECUTE ON FUNCTION unadjusted_forex_acl_grants() TO tally_app;
GRANT EXECUTE ON FUNCTION seed_data_entry_unadjusted_forex_rules(UUID) TO tally_app;

-- ---------------------------------------------------------------------------
-- 1. BACKFILL. Narrows every company that already exists, reading the SAME single
--    definition of the rule set rather than restating the two rules and risking a
--    third version of them.
--
--    ⛔ IT IS NOT OPTIONAL AND THE SEEDER IS NOT A SUBSTITUTE. Existing companies
--    already HAVE their Data Entry level, so the seeder above never runs for them
--    again -- and 0086 and 0087 both record "303/303 Data Entry levels covered" as
--    their measured proof precisely because the seeder alone would have left every
--    company in the field granted.
-- ---------------------------------------------------------------------------
INSERT INTO acl_rule (id, company_id, security_level_id, effect, action, resource)
SELECT gen_random_uuid(), sl.company_id, sl.id, g.effect, g.action, g.resource
  FROM security_level sl
 CROSS JOIN unadjusted_forex_acl_grants() AS g
 WHERE sl.name = 'Data Entry'
   AND NOT EXISTS (
        SELECT 1 FROM acl_rule ar
         WHERE ar.security_level_id = sl.id
           AND ar.effect   = g.effect
           AND ar.action   = g.action
           AND ar.resource = g.resource
   );

-- ---------------------------------------------------------------------------
-- 2. RESTATE THE SEEDER. Derived VERBATIM from 0087_gstr9_acl.sql, which was
--    MEASURED to be the HIGHEST-NUMBERED restatement of seed_company_auth_defaults
--    at the time this file was written.
--
--    ⛔ SORT EXPLICITLY. `grep -ln … | tail -1` is neither sorted NOR STABLE here:
--    `grep` is ugrep, which matches in parallel and emits files in COMPLETION
--    order, and three consecutive runs of the unsorted recipe gave two different
--    answers (0086 twice, then 0087). A recipe that usually gives one answer and
--    sometimes another is how the wrong body gets derived from once and nobody can
--    reproduce it. Use:
--
--      grep -ln "CREATE OR REPLACE FUNCTION seed_company_auth_defaults" \
--          backend/migrations/*.sql | sort | tail -1
--      # -> backend/migrations/0087_gstr9_acl.sql     (stable across runs)
--
--    ⚠️ AND COUNT THE FUNCTION BODY, NOT THE FILE. A whole-file `grep -c` counts the
--    copies of the pattern INSIDE COMMENTS -- 0087 measures 17 that way and 16
--    truly. Use the range anchored on the CREATE line and the terminator:
--
--      awk '/^CREATE OR REPLACE FUNCTION seed_company_auth_defaults\(/,/^\$\$;$/' \
--          backend/migrations/0087_gstr9_acl.sql | grep -c 'PERFORM seed_data_entry'
--      # -> 16     (0076 -> 14 · 0086 -> 15 · 0087 -> 16 · this file -> 17)
--
--    SEVENTEEN PERFORMs: the sixteen 0087 carries, unchanged and in its order, plus
--    the unadjusted-forex report. Diff this against 0087 and the only differences
--    must be comments and the single new PERFORM at the end.
--
--    ⚠️ THE NUMBER IS A HUMAN CHECK AND NOT AN ASSERTION. seeder_census_test.go is
--    STRUCTURAL: it finds the winning body and fails if that body does not PERFORM
--    every seeder some other migration defines. It hard-codes no count. So the
--    census test is the gate -- run it -- and 17 is a reader's arithmetic.
--
--    ⚠️ 0081-0084 REMAIN A DELIBERATE GAP and are now recorded as PERMANENTLY
--    UNNEEDED by T8: steps 4-7 added no table, and this file is the lane's only
--    migration after 0080. Leaving gaps rather than renumbering is §2.5's own rule.
--    The next genuinely free number is 0091.
--
-- ⛔ WHY THE NUMBER MATTERS MORE THAN THE CONTENT -- see this file's header. A body
-- installed below 0087 is overwritten on every fresh database, and the rules above
-- then do not exist for any new company. backend/migrations/seeder_census_test.go
-- guards it STATICALLY, with no database, because the defect lives in apply order
-- and is invisible on any incrementally-migrated developer machine. ⛔ If it fails,
-- do not route around it.
--
-- The signature is byte-identical on purpose: CREATE OR REPLACE cannot change a
-- return type, and a changed argument list creates a SECOND overload instead of
-- replacing, which would make authz/store.go's `seed_company_auth_defaults($1,$2)`
-- ambiguous at company creation rather than at migration time.
--
-- The PERFORMs stay at the very END of the body, after the coarse-seed block and
-- after the company_membership insert. The coarse block is guarded by
-- `IF NOT EXISTS (SELECT 1 FROM acl_rule WHERE security_level_id = de_level_id)`: a
-- PERFORM hoisted above it would insert the level's first rule, the guard would then
-- see rows, and the six voucher classes plus `allow display report` would SILENTLY
-- NEVER BE SEEDED -- every new tenant's Data Entry level unable to key an invoice,
-- with no migration test to catch it.
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
    -- ⛔ THIS IS THE LINE 0068 DROPPED AND THE REASON 0071 EXISTS. Keep it, and
    -- keep every line above it, when you derive the next restatement from here.
    PERFORM seed_data_entry_bank_statement_rules(p_company_id);
    -- THE COST REPORTS: four refusals, no allow (migration 0072). They are
    -- departmental profitability -- which part of the business earns and which
    -- loses.
    PERFORM seed_data_entry_cost_report_rules(p_company_id);
    -- THE ORDER BOOK, both directions: four refusals, no allow (migration 0073).
    -- It is every commitment made and received, by counterparty, with quantities
    -- and agreed rates.
    PERFORM seed_data_entry_order_book_rules(p_company_id);
    -- THE FORECAST PLANE: four refusals, no allow (migration 0076). The cash-flow
    -- projection is the forward cash position; the anomaly list is the CONTROL
    -- SURFACE that names, by voucher number, every entry this very level keyed
    -- irregularly.
    PERFORM seed_data_entry_forecast_report_rules(p_company_id);
    -- THE BUDGET VARIANCE REPORT: two refusals, no allow (migration 0086). It is
    -- the only report here that discloses something no voucher contains -- WHAT
    -- THE OWNER PLANNED -- and, line by line, where the business is failing
    -- against it. ⚠️ Its deny is 0072's precedent applied to an unanswered
    -- question; see 0086's header before reversing it.
    PERFORM seed_data_entry_budget_report_rules(p_company_id);
    -- THE GSTR-9 ANNUAL RECONCILIATION: two refusals, no allow (this migration).
    -- A whole financial year of outward supply and input credit in ONE document,
    -- plus the month-by-month divergence between what was filed and what the books
    -- say -- i.e. exactly which periods would not survive a departmental look.
    -- It is the company's tax exposure on one page.
    PERFORM seed_data_entry_gstr9_report_rules(p_company_id);

    -- THE UNADJUSTED-FOREX REPORT: two refusals, no allow (migration 0090). It is
    -- every foreign customer and supplier, what each still owes or is owed IN ITS
    -- OWN CURRENCY, the rate each document was raised at, and the company's whole
    -- unhedged currency exposure as one signed total.
    --
    -- ⛔ ONE NAME COVERING BOTH SIDES, WHICH IS THE OPPOSITE CALL FROM
    -- `bills-payable` AND IS ARGUED IN THIS FILE'S HEADER: the subject is the NET
    -- exposure to a currency, and a net figure is not separable by side.
    PERFORM seed_data_entry_unadjusted_forex_rules(p_company_id);

    RETURN owner_level_id;
END
$$;
GRANT EXECUTE ON FUNCTION seed_company_auth_defaults(UUID, UUID) TO tally_app;
