-- 0003_seed_function.sql
-- Per-company seed: the 28 predefined Tally account groups (15 primary +
-- 13 sub-groups, RESEARCH.md sec 5) plus the two non-deletable system ledgers
-- (Cash under Cash-in-Hand, Profit & Loss A/c under Primary) and the F8 Sales
-- voucher type. Because groups are tenant-scoped, they cannot be seeded by a
-- static migration; instead this function is invoked (under the company RLS
-- context) when a company is created.
--
-- The function is SECURITY INVOKER and inserts only rows for the supplied
-- company id, so it composes with the default-deny RLS policy: a session must
-- already be scoped to p_company_id for the inserts to be visible/permitted.

CREATE OR REPLACE FUNCTION seed_company_defaults(p_company_id UUID)
RETURNS VOID AS $$
DECLARE
    -- name, parent name (NULL = primary), nature, is_pl, behaves_as_subledger
    rec RECORD;
    grp_id      UUID;
    cash_grp    UUID;
    pl_primary  UUID;  -- the "Primary" pseudo-group P&L A/c sits under
BEGIN
    -- Pass 1: 15 primary groups. Path is the slugged label; we use the row id
    -- text (sans dashes) is awkward for ltree, so paths are name-derived.
    FOR rec IN
        SELECT * FROM (VALUES
            -- Balance Sheet primary groups (9)
            ('Capital Account',       'LIABILITY', false),
            ('Loans (Liability)',     'LIABILITY', false),
            ('Current Liabilities',   'LIABILITY', false),
            ('Fixed Assets',          'ASSET',     false),
            ('Investments',           'ASSET',     false),
            ('Current Assets',        'ASSET',     false),
            ('Branch / Divisions',    'ASSET',     false),
            ('Misc. Expenses (ASSET)','ASSET',     false),
            ('Suspense A/c',          'ASSET',     false),
            -- Profit & Loss primary groups (6)
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

    -- Pass 2: 13 sub-groups, each under a named primary parent. Sub-groups
    -- inherit nature/is_pl from their parent.
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

    -- System ledgers: Cash (under Cash-in-Hand) and Profit & Loss A/c.
    SELECT id INTO cash_grp FROM account_group
        WHERE company_id = p_company_id AND name = 'Cash-in-Hand';
    INSERT INTO ledger (id, company_id, name, group_id, is_system)
    VALUES (gen_random_uuid(), p_company_id, 'Cash', cash_grp, true);

    -- Profit & Loss A/c sits under Primary in Tally; we attach it to the
    -- Capital Account group as the canonical retained-earnings carrier so it
    -- has a valid Balance-Sheet home for Phase 0.
    SELECT id INTO pl_primary FROM account_group
        WHERE company_id = p_company_id AND name = 'Capital Account';
    INSERT INTO ledger (id, company_id, name, group_id, is_system)
    VALUES (gen_random_uuid(), p_company_id, 'Profit & Loss A/c', pl_primary, true);

    -- F8 Sales voucher type.
    INSERT INTO voucher_type (id, company_id, name, parent_class,
                              numbering_method, is_system)
    VALUES (gen_random_uuid(), p_company_id, 'Sales', 'SALES', 'AUTOMATIC', true);
END;
$$ LANGUAGE plpgsql;

GRANT EXECUTE ON FUNCTION seed_company_defaults(UUID) TO tally_app;
