-- Migration 0006: correct balances under voucher edits (ALTER/DELETE).
--
-- Problem fixed: ledger_balance was a running total to which every posted
-- version ADDED its postings. An ALTER appends a new version, so the prior
-- version's effect was never removed and balances double-counted after any
-- edit. That silently breaks the "reconcile to the rupee" promise the moment a
-- user alters a voucher.
--
-- Fix (preserves the incremental-balance performance design and the truly
-- append-only voucher_version log):
--   * voucher_head — a MUTABLE pointer table recording which immutable version
--     is current for each logical voucher. voucher_version itself stays
--     append-only (its immutability trigger is untouched).
--   * On ALTER/DELETE the application reverses the prior current version's
--     postings from ledger_balance before adding the new version's postings
--     (see internal/ledger). Only current, non-deleted versions contribute to
--     ledger_balance.
--   * This migration also REBUILDS ledger_balance from current versions so any
--     pre-0006 double-count is repaired. (Runs as the superuser migrator, which
--     bypasses RLS; on a fresh DB it is a no-op.)
-- ---------------------------------------------------------------------------

CREATE TABLE voucher_head (
    company_id         UUID NOT NULL,
    voucher_id         UUID NOT NULL,
    current_version_id UUID NOT NULL REFERENCES voucher_version (id),
    version_no         INTEGER NOT NULL,
    op                 voucher_op NOT NULL,
    is_deleted         BOOLEAN NOT NULL DEFAULT false,
    voucher_date       DATE NULL,
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (company_id, voucher_id)
);
CREATE INDEX voucher_head_company ON voucher_head (company_id);
CREATE INDEX voucher_head_current ON voucher_head (company_id, current_version_id);
CREATE INDEX voucher_head_date    ON voucher_head (company_id, voucher_date);

COMMENT ON TABLE voucher_head IS
    'Mutable pointer to the current immutable voucher_version for each logical '
    'voucher. The version log stays append-only; this row moves forward on each '
    'ALTER/DELETE. Only current, non-deleted versions contribute to ledger_balance.';

-- Default-deny RLS, matching every other tenant table.
ALTER TABLE voucher_head ENABLE ROW LEVEL SECURITY;
ALTER TABLE voucher_head FORCE  ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON voucher_head
    USING (company_id = app_current_company_id())
    WITH CHECK (company_id = app_current_company_id());

GRANT SELECT, INSERT, UPDATE, DELETE ON voucher_head TO tally_app;

-- Backfill the head from the latest version of each existing voucher.
INSERT INTO voucher_head
    (company_id, voucher_id, current_version_id, version_no, op, is_deleted, voucher_date)
SELECT DISTINCT ON (vv.company_id, vv.voucher_id)
       vv.company_id, vv.voucher_id, vv.id, vv.version_no, vv.op,
       (vv.op = 'DELETE'), vv.voucher_date
  FROM voucher_version vv
 ORDER BY vv.company_id, vv.voucher_id, vv.version_no DESC;

-- Rebuild ledger_balance from CURRENT, non-deleted versions only. This repairs
-- any double-count introduced before this migration. No-op on a fresh DB.
DELETE FROM ledger_balance;
INSERT INTO ledger_balance (company_id, ledger_id, debit, credit)
SELECT p.company_id, p.ledger_id,
       COALESCE(SUM(p.amount) FILTER (WHERE p.dr_cr = 'D'), 0),
       COALESCE(SUM(p.amount) FILTER (WHERE p.dr_cr = 'C'), 0)
  FROM posting p
  JOIN voucher_head vh
    ON vh.company_id = p.company_id
   AND vh.current_version_id = p.voucher_version_id
 WHERE vh.is_deleted = false
 GROUP BY p.company_id, p.ledger_id;
