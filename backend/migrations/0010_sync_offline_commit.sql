-- 0010_sync_offline_commit.sql
-- OFFLINE CLOUD-SEQUENCER slice of Task C (WORKSTREAM 1).
--
-- A desktop node holds a local append-only OUTBOX of voucher intents (each a
-- node-partitioned UUIDv7 + HLC + provisional number). On reconnect the outbox
-- COMMITS through the cloud sequencer, which assigns the FINAL display number,
-- resolves document-level conflicts deterministically (loser parked + surfaced),
-- and HARD-BLOCKS superseding an e-invoiced/filed voucher (reusing Task B's
-- freeze rule). Two schema changes support that:
--
--   * reconciliation_queue.payload (JSONB, nullable): when the sequencer PARKS a
--     conflict loser, the loser's full intent payload is preserved here so it can
--     be replayed/inspected. The freeze path (migration 0009) leaves it NULL.
--   * sync_applied: the exactly-once outbox-replay ledger. One row per
--     (company, idempotency_key) records the outcome of applying that intent, so
--     a whole re-Commit of the same batch is a no-op (StatusDuplicate) and no
--     number is burned.
--
-- sync_applied mirrors 0005/0009's RLS template exactly: it carries company_id,
-- gets ENABLE + FORCE ROW LEVEL SECURITY with a tenant_isolation policy pinned to
-- the total app_current_company_id() resolver (0004), then least-privilege grants
-- to tally_app. gen_random_uuid() is available (0001 enables pgcrypto).

ALTER TABLE reconciliation_queue
    ADD COLUMN payload JSONB NULL;

CREATE TABLE sync_applied (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id        UUID NOT NULL,
    idempotency_key   TEXT NOT NULL,
    client_voucher_id UUID NOT NULL,
    outcome           TEXT NOT NULL,
    final_voucher_no  TEXT NOT NULL DEFAULT '',
    applied_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (company_id, idempotency_key)
);

-- ---------------------------------------------------------------------------
-- Row-level security for the new tenant table: default-deny, ENABLE + FORCE,
-- USING + WITH CHECK pinned to the total app_current_company_id() resolver.
-- Mirrors 0005/0009's DO-block exactly.
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'sync_applied'
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
-- Least-privilege grant for the new table. reconciliation_queue is already
-- granted (0009), so the new payload column needs no additional grant.
-- ---------------------------------------------------------------------------
GRANT SELECT, INSERT, UPDATE, DELETE ON
    sync_applied
    TO tally_app;
