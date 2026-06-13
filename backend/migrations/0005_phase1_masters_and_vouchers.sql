-- 0005_phase1_masters_and_vouchers.sql
-- Phase 1 schema: complete the masters (ledger GST fields, voucher_type
-- semantics, stock_item + unit), add bill-wise allocation, enrich
-- voucher_version with queryable columns the reporting / GSTR / migration
-- agents need, and add a masters edit-log (MCA Rule 11(g) audit trail for
-- master changes, mirroring the voucher immutable log).
--
-- Every new tenant table carries company_id, is added to the default-deny
-- FORCE-RLS set with the total app_current_company_id() predicate, and is
-- granted to the tally_app role. No RLS is weakened.

-- ---------------------------------------------------------------------------
-- Enums for masters
-- ---------------------------------------------------------------------------
-- GST registration type of a party / company ledger.
CREATE TYPE gst_registration_type AS ENUM (
    'UNREGISTERED', 'REGULAR', 'COMPOSITION', 'CONSUMER', 'SEZ', 'UNKNOWN'
);

-- Bill-wise allocation reference type (RESEARCH sec 5: New Ref / Agst Ref /
-- Advance / On Account).
CREATE TYPE bill_ref_type AS ENUM ('NEW', 'AGAINST', 'ADVANCE', 'ON_ACCOUNT');

-- ---------------------------------------------------------------------------
-- ledger: GST master fields needed by the tax engine and GSTR reports.
--   gstin already exists (0002). Add the two-digit state code (derived from /
--   validated against GSTIN), and the GST registration type.
-- ---------------------------------------------------------------------------
ALTER TABLE ledger
    ADD COLUMN state_code        TEXT NULL,
    ADD COLUMN registration_type gst_registration_type NOT NULL DEFAULT 'UNKNOWN';

-- ---------------------------------------------------------------------------
-- voucher_type: accounting/inventory semantics so the post path and reports
-- know how a type behaves.
--   affects_stock  -> Item Invoice (posts stock movements)
--   is_accounting  -> posts to ledgers (all Phase 1 types do)
-- ---------------------------------------------------------------------------
ALTER TABLE voucher_type
    ADD COLUMN affects_stock BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN is_accounting BOOLEAN NOT NULL DEFAULT true;

-- ---------------------------------------------------------------------------
-- voucher_version: denormalised, queryable columns. The canonical hashed
-- payload remains the source of truth in `payload`; these columns are written
-- alongside it on every version so reporting / GSTR / migration agents can
-- filter without parsing JSONB. They are part of NO hash (the hash is over the
-- canonical payload only), so adding them does not change existing hashes.
-- ---------------------------------------------------------------------------
ALTER TABLE voucher_version
    ADD COLUMN voucher_type_id  UUID NULL REFERENCES voucher_type (id),
    ADD COLUMN party_ledger_id  UUID NULL REFERENCES ledger (id),
    ADD COLUMN voucher_no       TEXT NULL,
    ADD COLUMN voucher_date     DATE NULL,
    -- Derived GST totals for the version (NULL for non-GST voucher types).
    ADD COLUMN place_of_supply  TEXT NULL,
    ADD COLUMN is_reverse_charge BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN taxable_value    NUMERIC(19,4) NULL,
    ADD COLUMN cgst             NUMERIC(19,4) NULL,
    ADD COLUMN sgst             NUMERIC(19,4) NULL,
    ADD COLUMN igst             NUMERIC(19,4) NULL;

CREATE INDEX voucher_version_party ON voucher_version (company_id, party_ledger_id);
CREATE INDEX voucher_version_date  ON voucher_version (company_id, voucher_date);

-- ---------------------------------------------------------------------------
-- bill_allocation: bill-wise (New Ref / Agst Ref / Advance / On Account)
-- allocations attached to a party posting line. Drives the Outstanding /
-- Bills Receivable & Payable ageing reports. Belongs to a voucher_version so it
-- is versioned with the voucher (an alter re-states the allocations under the
-- new version). Immutable, like postings.
-- ---------------------------------------------------------------------------
CREATE TABLE bill_allocation (
    id                 UUID PRIMARY KEY,
    company_id         UUID NOT NULL,
    voucher_version_id UUID NOT NULL REFERENCES voucher_version (id),
    ledger_id          UUID NOT NULL REFERENCES ledger (id),
    ref_name           TEXT NOT NULL,
    ref_type           bill_ref_type NOT NULL,
    -- The signed-by-dr_cr amount allocated to this reference. Stored as the
    -- absolute amount plus the dr_cr side so the report layer can compute the
    -- outstanding sign per party nature.
    dr_cr              dr_cr NOT NULL,
    amount             NUMERIC(19,4) NOT NULL CHECK (amount >= 0),
    due_date           DATE NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX bill_allocation_company ON bill_allocation (company_id);
CREATE INDEX bill_allocation_version ON bill_allocation (voucher_version_id);
CREATE INDEX bill_allocation_ledger  ON bill_allocation (company_id, ledger_id);
CREATE INDEX bill_allocation_ref     ON bill_allocation (company_id, ledger_id, ref_name);

CREATE OR REPLACE FUNCTION bill_allocation_immutable()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'bill_allocation is immutable; % is not permitted', TG_OP;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER bill_allocation_no_mutate
    BEFORE UPDATE OR DELETE ON bill_allocation
    FOR EACH ROW EXECUTE FUNCTION bill_allocation_immutable();

-- ---------------------------------------------------------------------------
-- unit: unit of measure for stock items (basic — symbol + formal name).
-- ---------------------------------------------------------------------------
CREATE TABLE unit (
    id           UUID PRIMARY KEY,
    company_id   UUID NOT NULL,
    symbol       TEXT NOT NULL,          -- e.g. "Nos", "Kg"
    formal_name  TEXT NOT NULL DEFAULT '',
    decimal_places INTEGER NOT NULL DEFAULT 0 CHECK (decimal_places >= 0),
    is_system    BOOLEAN NOT NULL DEFAULT false,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (company_id, symbol)
);
CREATE INDEX unit_company ON unit (company_id);

-- ---------------------------------------------------------------------------
-- stock_item: inventory master (basic). Lives under a stock group concept in
-- Tally; Phase 1 keeps it flat with an optional HSN + GST rate so Item Invoices
-- and GSTR can attribute tax per item.
-- ---------------------------------------------------------------------------
CREATE TABLE stock_item (
    id           UUID PRIMARY KEY,
    company_id   UUID NOT NULL,
    name         TEXT NOT NULL,
    unit_id      UUID NULL REFERENCES unit (id),
    hsn          TEXT NULL,
    gst_rate_pct NUMERIC(9,4) NOT NULL DEFAULT 0,
    opening_qty  NUMERIC(19,4) NOT NULL DEFAULT 0,
    opening_rate NUMERIC(19,4) NOT NULL DEFAULT 0,
    is_system    BOOLEAN NOT NULL DEFAULT false,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (company_id, name)
);
CREATE INDEX stock_item_company ON stock_item (company_id);
CREATE INDEX stock_item_unit    ON stock_item (unit_id);

-- ---------------------------------------------------------------------------
-- master_audit: MCA Rule 11(g)-style edit log for masters. Masters are mutable
-- (a ledger can be renamed/re-grouped), but every create/alter/delete appends
-- an audit row capturing the before/after snapshot, so the change is traceable.
-- Append-only, like the voucher log.
-- ---------------------------------------------------------------------------
CREATE TABLE master_audit (
    id           UUID PRIMARY KEY,
    company_id   UUID NOT NULL,
    entity_type  TEXT NOT NULL,           -- 'group','ledger','voucher_type','stock_item','unit'
    entity_id    UUID NOT NULL,
    op           voucher_op NOT NULL,     -- reuse CREATE/ALTER/DELETE enum
    before_doc   JSONB NULL,
    after_doc    JSONB NULL,
    created_by   TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX master_audit_company ON master_audit (company_id);
CREATE INDEX master_audit_entity  ON master_audit (company_id, entity_type, entity_id);

CREATE OR REPLACE FUNCTION master_audit_immutable()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'master_audit is append-only; % is not permitted', TG_OP;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER master_audit_no_mutate
    BEFORE UPDATE OR DELETE ON master_audit
    FOR EACH ROW EXECUTE FUNCTION master_audit_immutable();

-- ---------------------------------------------------------------------------
-- Row-level security for the new tenant tables: default-deny, ENABLE + FORCE,
-- USING + WITH CHECK pinned to the total app_current_company_id() resolver.
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'bill_allocation', 'unit', 'stock_item', 'master_audit'
    ] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE  ROW LEVEL SECURITY', t);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I '
            || 'USING (company_id = app_current_company_id()) '
            || 'WITH CHECK (company_id = app_current_company_id())',
            t);
    END LOOP;
END
$$;

-- ---------------------------------------------------------------------------
-- Least-privilege grants for the new tables.
-- ---------------------------------------------------------------------------
GRANT SELECT, INSERT, UPDATE, DELETE ON
    bill_allocation, unit, stock_item, master_audit
    TO tally_app;

-- ---------------------------------------------------------------------------
-- Extend the per-company seed with the remaining Phase 1 voucher types
-- (Contra/Payment/Receipt/Journal/Purchase) and a default Unit. Sales already
-- seeded in 0003. CREATE OR REPLACE so existing companies pick this up on the
-- next call; new companies get the full set. Idempotent via NOT EXISTS guards.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION seed_phase1_voucher_types(p_company_id UUID)
RETURNS VOID AS $$
DECLARE
    rec RECORD;
BEGIN
    FOR rec IN
        SELECT * FROM (VALUES
            ('Contra',   'CONTRA',   false),
            ('Payment',  'PAYMENT',  false),
            ('Receipt',  'RECEIPT',  false),
            ('Journal',  'JOURNAL',  false),
            ('Purchase', 'PURCHASE', false)
        ) AS v(name, parent_class, affects_stock)
    LOOP
        IF NOT EXISTS (
            SELECT 1 FROM voucher_type
             WHERE company_id = p_company_id AND name = rec.name
        ) THEN
            INSERT INTO voucher_type (id, company_id, name, parent_class,
                                      numbering_method, affects_stock,
                                      is_accounting, is_system)
            VALUES (gen_random_uuid(), p_company_id, rec.name, rec.parent_class,
                    'AUTOMATIC', rec.affects_stock, true, true);
        END IF;
    END LOOP;

    -- Default unit "Nos" (numbers), used for item invoices when no unit set.
    IF NOT EXISTS (
        SELECT 1 FROM unit WHERE company_id = p_company_id AND symbol = 'Nos'
    ) THEN
        INSERT INTO unit (id, company_id, symbol, formal_name, is_system)
        VALUES (gen_random_uuid(), p_company_id, 'Nos', 'Numbers', true);
    END IF;
END;
$$ LANGUAGE plpgsql;

GRANT EXECUTE ON FUNCTION seed_phase1_voucher_types(UUID) TO tally_app;

-- Re-define seed_company_defaults so NEW companies automatically get the full
-- Phase 1 voucher-type set and default unit. We re-create it to append a single
-- call to seed_phase1_voucher_types at the end; the body otherwise mirrors 0003
-- exactly (groups, system ledgers, Sales type). Keeping the whole body here (vs
-- a thin wrapper) means a fresh DB applying 0003 then 0005 ends with the
-- complete definition, and the function stays self-describing.
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

    -- Phase 1 additions: remaining accounting voucher types + default unit.
    PERFORM seed_phase1_voucher_types(p_company_id);
END;
$$ LANGUAGE plpgsql;

GRANT EXECUTE ON FUNCTION seed_company_defaults(UUID) TO tally_app;
