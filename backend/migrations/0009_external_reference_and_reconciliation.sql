-- 0009_external_reference_and_reconciliation.sql
-- FREEZE-ON-EXTERNAL-REFERENCE: registering an IRN (or a filed GSTR-1) pins a
-- voucher's display number and HARD-BLOCKS any later alter/delete, routing the
-- attempt to a reconciliation queue.
--
-- Two new tenant tables:
--   * voucher_external_reference: the pin. One row per (voucher, kind, ref);
--     pinned_voucher_no captures the voucher's display number AT PIN TIME so the
--     number can never drift after an IRN is minted.
--   * reconciliation_queue: an alter/delete attempt against a frozen voucher is
--     refused (409) AND raises an open row here for human reconciliation.
--
-- Both mirror 0005's RLS template exactly: every tenant table has company_id,
-- ENABLE + FORCE ROW LEVEL SECURITY, a tenant_isolation policy pinned to the
-- total app_current_company_id() resolver (0004), then least-privilege grants.

CREATE TABLE voucher_external_reference (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id        UUID NOT NULL,
    voucher_id        UUID NOT NULL,
    kind              TEXT NOT NULL CHECK (kind IN ('IRN', 'FILED_GSTR1')),
    ref_value         TEXT NOT NULL,
    pinned_voucher_no TEXT NOT NULL,
    created_by        TEXT NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (company_id, voucher_id, kind, ref_value)
);

-- The freeze lookup is keyed on (company_id, voucher_id); the UNIQUE constraint
-- above indexes the 4-col tuple, not this 2-col prefix on its own, so add it.
CREATE INDEX voucher_external_reference_lookup
    ON voucher_external_reference (company_id, voucher_id);

CREATE TABLE reconciliation_queue (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id        UUID NOT NULL,
    voucher_id        UUID NOT NULL,
    attempted_op      TEXT NOT NULL CHECK (attempted_op IN ('ALTER', 'DELETE')),
    kind              TEXT NOT NULL,
    ref_value         TEXT NOT NULL,
    frozen_voucher_no TEXT NOT NULL,
    detail            TEXT NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at       TIMESTAMPTZ NULL
);

CREATE INDEX reconciliation_queue_open
    ON reconciliation_queue (company_id, created_at DESC)
    WHERE resolved_at IS NULL;

-- ---------------------------------------------------------------------------
-- Row-level security for the two new tenant tables: default-deny, ENABLE +
-- FORCE, USING + WITH CHECK pinned to the total app_current_company_id()
-- resolver. Mirrors 0005's DO-block exactly.
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'voucher_external_reference', 'reconciliation_queue'
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
    voucher_external_reference, reconciliation_queue
    TO tally_app;
