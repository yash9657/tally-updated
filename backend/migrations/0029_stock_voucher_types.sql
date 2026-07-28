-- 0029_stock_voucher_types.sql
-- THE FOUR NON-ACCOUNTING STOCK VOUCHER TYPES: Delivery Note, Receipt Note,
-- Stock Journal, Physical Stock.
--
-- WHAT THESE ARE
-- --------------
-- A Delivery Note records goods LEAVING before (or without) the invoice; a
-- Receipt Note records goods ARRIVING before the supplier's bill. A Stock
-- Journal transfers stock between locations, or consumes components and
-- produces a finished good on the SAME voucher. A Physical Stock voucher
-- records what was actually COUNTED on the shelf.
--
-- None of them posts a single accounting line. In Tally an operator lives in
-- these screens all day: the warehouse moves hours or weeks before the billing
-- department catches up, and a system that can only record a movement by also
-- recording revenue forces the operator to either invent an invoice or key
-- nothing at all. Both are worse than the truth.
--
-- ===========================================================================
-- READ THIS BEFORE YOU TOUCH voucher_type.is_accounting
-- ===========================================================================
-- The rows seeded below carry is_accounting = false. That column is
-- DESCRIPTIVE METADATA -- it is what the UI reads to decide whether to draw a
-- ledger grid, and it is what a report reads to label a voucher. IT IS NOT THE
-- SWITCH.
--
-- Whether a voucher class must state balanced double-entry postings is a
-- COMPILE-TIME CLOSED SET IN GO (ledger.accountingClasses, in
-- internal/ledger/nonaccounting.go). The post path never reads this column.
--
-- That is deliberate and it is the whole design decision of this slice. If
-- double-entry were keyed off this flag, then a single
--
--     UPDATE voucher_type SET is_accounting = false WHERE name = 'Sales';
--
-- -- one row, in a table the admin surface already exposes, in a tenant nobody
-- is watching -- would make the server accept a SALES INVOICE THAT MOVES STOCK
-- OUT OF THE WAREHOUSE AND POSTS NOTHING AT ALL. No revenue, no receivable, no
-- tax. And the database could not catch it: posting_balanced (0002) is a
-- deferred trigger on the posting TABLE, so a voucher with ZERO posting rows
-- never fires it. The trial balance would still balance -- it would simply be
-- missing the sale. There is no report anywhere that shows you a voucher that
-- did not happen.
--
-- An invariant that must never be data-driven does not get a data-driven
-- switch. The Go set is the authority; this column describes it.
-- ===========================================================================
--
-- WHY THERE ARE NO SALES ORDER / PURCHASE ORDER TYPES HERE
-- -------------------------------------------------------
-- Because an ORDER IS NOT A MOVEMENT and this schema can only record
-- movements. stock_movement has exactly one primitive -- a positive quantity
-- plus a direction -- and every reader of it (check_stock_not_negative above,
-- ledger.checkNegativeStock, inventory.Replay, every stock report) SUMS it.
-- There is no way to express "10 units are promised to a customer but are
-- still physically on the shelf" in a sum. Recording an order as a movement
-- would let a QUOTATION deplete the warehouse; recording it as a zero movement
-- would record nothing.
--
-- What an order actually needs is a COMMITTED QUANTITY: its own table, its own
-- fulfilment linkage (an order is closed by the Delivery Note that ships it,
-- partially and over time), and a second pair of columns in every stock report
-- -- which is exactly the shape TallyPrime uses, where "Orders Pending" sits
-- beside the physical balance rather than inside it. That is a slice of its
-- own. Seeding an order voucher TYPE now would advertise a screen the post
-- path refuses (ledger.ErrOrderVouchersNotModelled), so it is not seeded.
--
-- WHY NO acl_rule ROWS ARE SEEDED FOR THESE CLASSES
-- ------------------------------------------------
-- Nothing routes to them yet: internal/server registers one literal path per
-- accounting class and none of these four is mounted. Granting the seeded
-- 'Data Entry' level `allow create delivery-note` today would be a widening
-- with no present effect and a real future one -- the level would acquire the
-- right to move goods the moment somebody mounted the route, without anybody
-- deciding to. The fail-closed order is the other one: the slice that mounts
-- POST /vouchers/delivery-note adds the rules in the same change, at the same
-- resource tokens the router derives ('delivery-note', 'receipt-note',
-- 'stock-journal', 'physical-stock'), exactly as 0019 did for the notes.

-- ---------------------------------------------------------------------------
-- The seeder. Idempotent per company at the (company_id, name) grain, which is
-- also voucher_type's UNIQUE constraint, so a re-run inserts nothing.
--
-- parent_class is stored UPPER_SNAKE because that is what
-- ledger.defaultVoucherTypeID matches on:
--     upper(replace('Delivery Note', ' ', '_')) = 'DELIVERY_NOTE'
-- Get this wrong and the class silently loses its voucher type, which is not
-- cosmetic: numbering cannot run and voucher_version.voucher_type_id goes NULL
-- -- the exact NULL that dropped altered invoices out of GSTR-1 (see the
-- carry-forward note in ledger.post).
--
-- affects_stock = true, is_accounting = false: descriptive, per the header.
-- numbering AUTOMATIC so each type gets its OWN series -- voucher_number_counter
-- is keyed (company_id, voucher_type_id, fin_year), so a Delivery Note never
-- consumes an invoice number.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION seed_stock_voucher_types(p_company_id UUID)
RETURNS VOID AS $$
DECLARE
    rec RECORD;
BEGIN
    FOR rec IN
        SELECT * FROM (VALUES
            ('Delivery Note',  'DELIVERY_NOTE'),
            ('Receipt Note',   'RECEIPT_NOTE'),
            ('Stock Journal',  'STOCK_JOURNAL'),
            ('Physical Stock', 'PHYSICAL_STOCK')
        ) AS v(name, parent_class)
    LOOP
        IF NOT EXISTS (
            SELECT 1 FROM voucher_type
             WHERE company_id = p_company_id AND name = rec.name
        ) THEN
            INSERT INTO voucher_type (id, company_id, name, parent_class,
                                      numbering_method, affects_stock,
                                      is_accounting, is_system)
            VALUES (gen_random_uuid(), p_company_id, rec.name, rec.parent_class,
                    'AUTOMATIC', true, false, true);
        END IF;
    END LOOP;
END;
$$ LANGUAGE plpgsql;

GRANT EXECUTE ON FUNCTION seed_stock_voucher_types(UUID) TO tally_app;

-- ---------------------------------------------------------------------------
-- Backfill EVERY existing company, as ONE set-based statement.
--
-- NOT a per-company loop calling the function above. 0028's header records what
-- that costs: 38,748 companies x 4 names reached 13% in ONE HOUR AND FIFTY-SEVEN
-- MINUTES before it was killed, while starving the connection pool. The
-- anti-join is what makes this idempotent -- re-running inserts nothing --
-- which matters because `cored migrate` does NOT wrap a migration in a
-- transaction: a run killed halfway leaves its rows behind WITHOUT stamping
-- schema_migrations, and only idempotence makes that recoverable.
--
-- Without the backfill, a company created last month could not enter a Delivery
-- Note while one created next week could, and the difference would surface as a
-- post that cannot allocate a voucher number.
-- ---------------------------------------------------------------------------
INSERT INTO voucher_type (id, company_id, name, parent_class,
                          numbering_method, affects_stock, is_accounting, is_system)
SELECT gen_random_uuid(), c.id, v.name, v.parent_class,
       'AUTOMATIC', true, false, true
  FROM company c
 CROSS JOIN (VALUES
        ('Delivery Note',  'DELIVERY_NOTE'),
        ('Receipt Note',   'RECEIPT_NOTE'),
        ('Stock Journal',  'STOCK_JOURNAL'),
        ('Physical Stock', 'PHYSICAL_STOCK')
 ) AS v(name, parent_class)
 WHERE NOT EXISTS (
        SELECT 1 FROM voucher_type vt
         WHERE vt.company_id = c.id AND vt.name = v.name
 );

-- The warning that belongs where the next person will actually meet it.
COMMENT ON COLUMN voucher_type.is_accounting IS
    'DESCRIPTIVE ONLY. Whether a class must state balanced double-entry '
    'postings is a compile-time closed set in Go (ledger.accountingClasses); '
    'the post path never reads this column. Keying it off this flag would let '
    'one UPDATE turn a Sales invoice into a voucher that ships goods and posts '
    'nothing -- which posting_balanced cannot catch, because a voucher with '
    'zero posting rows never fires a trigger on the posting table.';

-- ---------------------------------------------------------------------------
-- Extend the per-company seed so NEW companies get the four types at creation.
-- The whole body is restated (rather than wrapped) for the same reason 0025
-- restated it: a fresh database applying 0003 -> 0005 -> 0018 -> 0025 -> 0029
-- must end with one complete, self-describing definition.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION seed_company_defaults(p_company_id UUID)
RETURNS VOID AS $$
DECLARE
    rec RECORD;
    grp_id      UUID;
    cash_grp    UUID;
    pl_primary  UUID;
BEGIN
    FOR rec IN
        SELECT * FROM (VALUES
            ('Capital Account',       'LIABILITY', false),
            ('Loans (Liability)',     'LIABILITY', false),
            ('Current Liabilities',   'LIABILITY', false),
            ('Fixed Assets',          'ASSET',     false),
            ('Investments',           'ASSET',     false),
            ('Current Assets',        'ASSET',     false),
            ('Branch / Divisions',    'ASSET',     false),
            ('Misc. Expenses (ASSET)','ASSET',     false),
            ('Suspense A/c',          'ASSET',     false),
            ('Sales Accounts',        'INCOME',    true),
            ('Purchase Accounts',     'EXPENSE',   true),
            ('Direct Incomes',        'INCOME',    true),
            ('Indirect Incomes',      'INCOME',    true),
            ('Direct Expenses',       'EXPENSE',   true),
            ('Indirect Expenses',     'EXPENSE',   true)
        ) AS v(name, nature, is_pl)
    LOOP
        grp_id := gen_random_uuid();
        INSERT INTO account_group (id, company_id, name, parent_id, path,
                                   nature, is_pl, is_system)
        VALUES (grp_id, p_company_id, rec.name, NULL,
                text2ltree('g' || replace(grp_id::text, '-', '')),
                rec.nature::account_nature, rec.is_pl, true);
    END LOOP;

    FOR rec IN
        SELECT * FROM (VALUES
            ('Bank Accounts',          'Current Assets',      false),
            ('Cash-in-Hand',           'Current Assets',      false),
            ('Deposits (Asset)',       'Current Assets',      false),
            ('Loans & Advances (Asset)','Current Assets',     false),
            ('Stock-in-Hand',          'Current Assets',      false),
            ('Sundry Debtors',         'Current Assets',      true),
            ('Duties & Taxes',         'Current Liabilities', false),
            ('Provisions',             'Current Liabilities', false),
            ('Sundry Creditors',       'Current Liabilities', true),
            ('Bank OD A/c',            'Loans (Liability)',   false),
            ('Secured Loans',          'Loans (Liability)',   false),
            ('Unsecured Loans',        'Loans (Liability)',   false),
            ('Reserves & Surplus',     'Capital Account',     false)
        ) AS v(name, parent_name, behaves_as_subledger)
    LOOP
        grp_id := gen_random_uuid();
        INSERT INTO account_group (id, company_id, name, parent_id, path,
                                   nature, is_pl, behaves_as_subledger, is_system)
        SELECT grp_id, p_company_id, rec.name, parent.id,
               parent.path || text2ltree('s' || replace(grp_id::text, '-', '')),
               parent.nature, parent.is_pl, rec.behaves_as_subledger, true
          FROM account_group parent
         WHERE parent.company_id = p_company_id AND parent.name = rec.parent_name;
    END LOOP;

    SELECT id INTO cash_grp FROM account_group
        WHERE company_id = p_company_id AND name = 'Cash-in-Hand';
    INSERT INTO ledger (id, company_id, name, group_id, is_system)
    VALUES (gen_random_uuid(), p_company_id, 'Cash', cash_grp, true);

    SELECT id INTO pl_primary FROM account_group
        WHERE company_id = p_company_id AND name = 'Capital Account';
    INSERT INTO ledger (id, company_id, name, group_id, is_system)
    VALUES (gen_random_uuid(), p_company_id, 'Profit & Loss A/c', pl_primary, true);

    INSERT INTO voucher_type (id, company_id, name, parent_class,
                              numbering_method, is_system)
    VALUES (gen_random_uuid(), p_company_id, 'Sales', 'SALES', 'AUTOMATIC', true);

    PERFORM seed_phase1_voucher_types(p_company_id);
    PERFORM seed_credit_debit_note_types(p_company_id);
    PERFORM seed_inventory_defaults(p_company_id);
    PERFORM seed_stock_voucher_types(p_company_id);
END;
$$ LANGUAGE plpgsql;

GRANT EXECUTE ON FUNCTION seed_company_defaults(UUID) TO tally_app;
