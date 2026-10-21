-- The five references identified by the agent-wave review must be enforced
-- below the HTTP handlers. Existing inconsistent books are reported, never
-- deleted or silently reassigned. The migration is atomic and safe to retry.
DO $$
DECLARE
    edge RECORD;
    bad_count BIGINT;
    first_id UUID;
BEGIN
    FOR edge IN SELECT * FROM (VALUES
        ('posting', 'ledger_id', 'ledger', 'posting_ledger_company_fk'),
        ('voucher_version', 'party_ledger_id', 'ledger', 'voucher_party_company_fk'),
        ('bill_allocation', 'ledger_id', 'ledger', 'bill_allocation_ledger_company_fk'),
        ('ledger', 'group_id', 'account_group', 'ledger_group_company_fk'),
        ('stock_item', 'unit_id', 'unit', 'stock_item_unit_company_fk')
    ) AS edges(child, field, parent, constraint_name)
    LOOP
        EXECUTE format(
            'SELECT count(*), min(c.id::text)::uuid FROM %I c '
            'LEFT JOIN %I p ON p.id = c.%I AND p.company_id = c.company_id '
            'WHERE c.%I IS NOT NULL AND p.id IS NULL',
            edge.child, edge.parent, edge.field, edge.field)
            INTO bad_count, first_id;
        IF bad_count > 0 THEN
            RAISE EXCEPTION 'company reference audit: %.% has % inconsistent rows; first row %. Repair explicitly before retrying migration 0111.',
                edge.child, edge.field, bad_count, first_id USING ERRCODE = '23503';
        END IF;
    END LOOP;
END
$$;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'unit'::regclass AND conname = 'unit_company_scope') THEN
        ALTER TABLE unit ADD CONSTRAINT unit_company_scope UNIQUE (company_id, id);
    END IF;
END
$$;

DO $$
DECLARE edge RECORD;
BEGIN
    FOR edge IN SELECT * FROM (VALUES
        ('posting', 'ledger_id', 'ledger', 'posting_ledger_company_fk'),
        ('voucher_version', 'party_ledger_id', 'ledger', 'voucher_party_company_fk'),
        ('bill_allocation', 'ledger_id', 'ledger', 'bill_allocation_ledger_company_fk'),
        ('ledger', 'group_id', 'account_group', 'ledger_group_company_fk'),
        ('stock_item', 'unit_id', 'unit', 'stock_item_unit_company_fk')
    ) AS edges(child, field, parent, constraint_name)
    LOOP
        IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = edge.child::regclass AND conname = edge.constraint_name) THEN
            EXECUTE format('ALTER TABLE %I ADD CONSTRAINT %I FOREIGN KEY (company_id, %I) REFERENCES %I (company_id, id) NOT VALID',
                edge.child, edge.constraint_name, edge.field, edge.parent);
        END IF;
        EXECUTE format('ALTER TABLE %I VALIDATE CONSTRAINT %I', edge.child, edge.constraint_name);
    END LOOP;
END
$$;
