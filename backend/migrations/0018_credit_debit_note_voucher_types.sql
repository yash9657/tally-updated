-- 0018_credit_debit_note_voucher_types.sql
-- CREDIT NOTE and DEBIT NOTE voucher types.
--
-- WHY THESE MATTER MORE THAN "two more voucher types"
-- ---------------------------------------------------
-- Without them, the ONLY way to correct a sales invoice in this system was to
-- ALTER it. That is wrong twice over:
--
--   * Accounting: a sales return is a real, separately-dated event. Rewriting
--     the original invoice erases the fact that goods went out in April and
--     came back in July, and back-dates the reversal into a period that may
--     already be filed.
--   * GST: credit/debit notes have their OWN GSTR-1 section (cdnr, table 9B)
--     keyed to the original invoice. A return expressed as an alteration is
--     invisible there — and, until the fix in this same pass, an altered
--     invoice was dropped from the return entirely.
--
-- NUMBERING: each gets its own voucher_type row, and voucher_number_counter is
-- keyed on (company_id, voucher_type_id, fin_year), so both automatically get an
-- INDEPENDENT series. A credit note never consumes an invoice number.
--
-- Both are is_accounting (they post to ledgers) and NOT affects_stock — the
-- inventory plane is not built (see F11 Features), so a return moves money only.

CREATE OR REPLACE FUNCTION seed_credit_debit_note_types(p_company_id UUID)
RETURNS VOID AS $$
DECLARE
    rec RECORD;
BEGIN
    FOR rec IN
        SELECT * FROM (VALUES
            ('Credit Note', 'CREDIT_NOTE'),
            ('Debit Note',  'DEBIT_NOTE')
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
                    'AUTOMATIC', false, true, true);
        END IF;
    END LOOP;
END;
$$ LANGUAGE plpgsql;

GRANT EXECUTE ON FUNCTION seed_credit_debit_note_types(UUID) TO tally_app;

-- Backfill EVERY existing company. Without this, only companies created after
-- this migration could raise a credit note, and the difference would surface as
-- a confusing "voucher type not found" on older tenants.
DO $$
DECLARE
    c RECORD;
BEGIN
    FOR c IN SELECT id FROM company LOOP
        PERFORM seed_credit_debit_note_types(c.id);
    END LOOP;
END
$$;

-- Extend the per-company seed so NEW companies get them at creation. Appends a
-- single PERFORM to the existing definition rather than restating the whole
-- body: 0005 already re-created seed_company_defaults in full, and duplicating
-- that body a third time is how the two copies drift apart.
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
END;
$$ LANGUAGE plpgsql;

GRANT EXECUTE ON FUNCTION seed_company_defaults(UUID) TO tally_app;
