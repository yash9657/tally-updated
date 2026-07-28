-- 0031_stock_voucher_acl.sql
-- The ACL rules that make the four NON-ACCOUNTING STOCK VOUCHERS and the PRICE
-- LISTS reachable by somebody other than an Owner -- written in the SAME change
-- that mounts their routes, which is the order migration 0029 explicitly asked
-- for.
--
-- WHY THIS MIGRATION HAS TO EXIST AT ALL
-- --------------------------------------
-- internal/server now registers
--
--   POST/PUT .../vouchers/delivery-note    -> (create|alter, 'delivery-note')
--   POST/PUT .../vouchers/receipt-note     -> (create|alter, 'receipt-note')
--   POST/PUT .../vouchers/stock-journal    -> (create|alter, 'stock-journal')
--   POST/PUT .../vouchers/physical-stock   -> (create|alter, 'physical-stock')
--   GET      .../price-lists/quote|{id}    -> (display,      'price-list')
--   POST     .../price-lists               -> (create,       'price-list')
--
-- The seeded 'Data Entry' level has default_access = 'none', so "no rule" means
-- DENY. Without the rows below, the warehouse operator this whole slice exists
-- for presses the key and gets a bare 403 with no indication that the cause is
-- a missing ACL row rather than a broken screen. That is the failure mode 0019
-- recorded verbatim when 0018 added the note classes and nobody added the
-- rules: adding a voucher class is a TWO-file change, the class and the levels
-- permitted to use it.
--
-- 0029 deliberately seeded NOTHING, because granting `allow create
-- delivery-note` while no route existed would have been a widening with no
-- present effect and a real future one -- the level would have acquired the
-- right to move goods the moment somebody mounted the route, without anybody
-- deciding to. The routes exist now, so the decision is due now.
--
-- ===========================================================================
-- THE JUDGEMENT: WHICH STOCK VOUCHERS DATA ENTRY GETS, AND WHY
-- ===========================================================================
-- The line is NOT drawn between "accounting" and "non-accounting" -- all four
-- of these post zero ledger lines. It is drawn between a document that records
-- a movement SOMEBODY ELSE CAN CHECK and a document that records the operator's
-- own assertion about reality.
--
--   * delivery-note -- GRANTED (create, alter, display).
--     Goods leave against a named customer, hours or weeks before the billing
--     department raises the invoice. This is the single most common thing a
--     warehouse hand does all day, and a system that can only record it by also
--     recording revenue forces them to invent an invoice or key nothing. Both
--     are worse than the truth. It is CHECKABLE: the note names a party, and
--     the invoice that follows has to agree with it.
--
--   * receipt-note -- GRANTED (create, alter, display).
--     The exact mirror: goods arrive from a supplier before the bill. Granting
--     the delivery half and withholding this one would be an arbitrary
--     distinction -- the same person, at the same desk, on the same shift,
--     keying the same document in the opposite direction -- and the operator
--     would simply key the arrival as something else.
--
--   * stock-journal -- GRANTED (create, alter, display).
--     A transfer between godowns, or a manufacture that consumes components and
--     produces a finished good. In any company with more than one location this
--     is ordinary floor work: stock physically moved, and the books have to say
--     so. It is the one class that carries BOTH directions on a voucher, and
--     that is what makes it checkable -- a transfer states where the goods went
--     as well as where they left, so a location's shortfall has a named
--     counterpart. (A journal keyed with only OUT lines is a write-off, and a
--     write-off by an operator is visible as a location whose stock left for
--     nowhere: it shows up in the godown summary a supervisor reads.)
--
--   * physical-stock -- WITHHELD, and this is the deliberate one.
--     A Physical Stock voucher is the ONLY document in the system that makes
--     the book position agree with a figure the operator TYPES, with no
--     counter-party, no counter-location and no second document that has to
--     agree with it. The server differences the count against the books and
--     writes the delta silently; the closing position afterwards IS the count,
--     by construction. That is precisely the authority a person must not hold
--     over their own shortages -- whoever keyed the deliveries could then make
--     the shelf agree with the books and the shrinkage would never surface in
--     any report, because there is no report that shows you a discrepancy
--     somebody already reconciled away.
--
--     Stock-taking is a supervisory act in every inventory-control regime for
--     exactly this reason. An Owner (default_access 'full') keys it, or an Owner
--     grants it to a named stock-take level in Users & Roles -- that admin
--     surface already exists, and `allow create physical-stock` is now a rule
--     they can actually write, because authz.KnownVoucherClasses names the
--     token. Withholding it is not "not built": it is a decision an
--     administrator can reverse in one row.
--
-- WHY THE REFUSAL IS AN EXPLICIT `disallow` AND NOT JUST SILENCE
-- -------------------------------------------------------------
-- With default_access 'none', omitting the rule already denies it, so the
-- disallow rows below change no answer TODAY. They are written anyway for the
-- reason 0026 wrote its explicit disallow: the PDP resolves by MOST-SPECIFIC
-- match (internal/authz, ruleSpecificity), so an exact (action, resource) rule
-- scores 2 against a wildcard's 0..1, and disallow beats allow at equal
-- specificity. The day an administrator broadens this level with `allow create
-- *` -- which is a natural thing to do and needs no code change -- silence
-- would hand over the stock-take and an explicit disallow would not. A decision
-- this deliberate should survive somebody else's convenience.
--
-- ===========================================================================
-- PRICE LISTS: WHY DATA ENTRY GETS THE RATE CARD, UNLIKE THE COST REPORTS
-- ===========================================================================
-- Migrations 0026 and 0028 took FIVE reports away from this level because every
-- one of them prints the company's COST book -- what we paid, per item, per
-- lot, per supplier, and therefore what we make. That reasoning does not
-- transfer here, and copying it reflexively would be the wrong answer.
--
-- A price list is a SELL price. It is the number the operator has to charge; it
-- is printed, in full, on every invoice they raise and hand to the customer. An
-- operator who cannot read it does not stop knowing it -- they get emailed the
-- whole rate card instead, which discloses strictly more and leaves no audit
-- trail at all. And the resolver (GET .../price-lists/quote) answers ONE line at
-- a time for an item, a date and a quantity they already have in front of them.
--
-- The genuinely sensitive part is the PARTY-SCOPED list: "Acme gets 12% off and
-- Beta does not". That is real, and it is still on Acme's invoice, keyed by the
-- same operator. So: `allow display price-list`, and NO create.
--
-- What stays denied, unchanged and deliberate: no `create price-list` (rates
-- are set by whoever owns the margin, not by whoever keys the order), no
-- `create master`, no delete anywhere, no `security`, no `import`, no `sync`,
-- no `query`, and the five cost reports 0026/0028 disallowed.

-- The grant table itself, as a function, so the per-company seeder below and
-- the fleet backfill below it read from ONE definition. Two copies of this list is
-- how a tenant created next week ends up with a different rule set from one
-- created last month -- which for physical-stock would mean the stock-take is
-- an Owner action in some tenants and an operator action in others.
CREATE OR REPLACE FUNCTION stock_voucher_acl_grants()
RETURNS TABLE (effect TEXT, action TEXT, resource TEXT) AS $$
    SELECT * FROM (VALUES
        -- The three movement documents an operator keys (see the header).
        ('allow',    'create',  'delivery-note'),
        ('allow',    'alter',   'delivery-note'),
        ('allow',    'display', 'delivery-note'),
        ('allow',    'create',  'receipt-note'),
        ('allow',    'alter',   'receipt-note'),
        ('allow',    'display', 'receipt-note'),
        ('allow',    'create',  'stock-journal'),
        ('allow',    'alter',   'stock-journal'),
        ('allow',    'display', 'stock-journal'),
        -- The stock-take: refused OUT LOUD, so a later wildcard cannot grant it.
        -- display IS allowed: reading a stock-take somebody else keyed is how an
        -- operator finds out the shelf was recounted, and it discloses a
        -- quantity they can already see by walking to it.
        ('disallow', 'create',  'physical-stock'),
        ('disallow', 'alter',   'physical-stock'),
        ('allow',    'display', 'physical-stock'),
        -- The rate card: read, never define.
        ('allow',    'display', 'price-list')
    ) AS v(effect, action, resource);
$$ LANGUAGE sql IMMUTABLE;

-- ---------------------------------------------------------------------------
-- The per-company seeder. Idempotent at the (level, effect, action, resource)
-- grain, so a re-run inserts nothing -- `cored migrate` does NOT wrap a
-- migration in a transaction, so a run killed halfway leaves its rows behind
-- WITHOUT stamping schema_migrations, and only idempotence makes that
-- recoverable (0028's header records the run that proved it).
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION seed_data_entry_stock_voucher_rules(p_company_id UUID)
RETURNS VOID AS $$
DECLARE
    de_level_id UUID;
    rec         RECORD;
BEGIN
    SELECT id INTO de_level_id
      FROM security_level
     WHERE company_id = p_company_id AND name = 'Data Entry';
    IF de_level_id IS NULL THEN
        RETURN;   -- company has no Data Entry level; nothing to extend
    END IF;

    FOR rec IN SELECT * FROM stock_voucher_acl_grants() LOOP
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

GRANT EXECUTE ON FUNCTION stock_voucher_acl_grants() TO tally_app;
GRANT EXECUTE ON FUNCTION seed_data_entry_stock_voucher_rules(UUID) TO tally_app;

-- ---------------------------------------------------------------------------
-- Backfill EVERY existing company, as ONE set-based statement.
--
-- NOT a per-company loop calling the function above. 0028's header records what
-- that costs on this project's own dev database: 38,748 companies x 4 resource
-- names reached 13% in ONE HOUR AND FIFTY-SEVEN MINUTES before it was killed,
-- while starving the connection pool. This is thirteen rows per company across
-- ~39,000 companies; as a loop it would be worse than the run that had to be
-- killed. The anti-join replaces the per-row EXISTS and is what makes re-running
-- a no-op.
--
-- Without the backfill a company created last month could not enter a Delivery
-- Note while one created next week could -- two companies whose security levels
-- differ only by the date they were created, which is the failure mode every one
-- of these seed migrations exists to prevent.
-- ---------------------------------------------------------------------------
INSERT INTO acl_rule (id, company_id, security_level_id, effect, action, resource)
SELECT gen_random_uuid(), sl.company_id, sl.id, g.effect, g.action, g.resource
  FROM security_level sl
 CROSS JOIN stock_voucher_acl_grants() AS g
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
-- The whole body is restated (rather than wrapped) for the same reason 0028 and
-- 0029 restated theirs: a fresh database applying 0011 -> 0019 -> 0022 -> 0026
-- -> 0028 -> 0031 must end with ONE complete, self-describing definition. The
-- only change from 0028's version is the final PERFORM.
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
    -- The stock vouchers and the rate card (this migration).
    PERFORM seed_data_entry_stock_voucher_rules(p_company_id);

    RETURN owner_level_id;
END
$$;

GRANT EXECUTE ON FUNCTION seed_company_auth_defaults(UUID, UUID) TO tally_app;
