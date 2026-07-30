-- 0045_bank_reconciliation.sql
-- BANK RECONCILIATION: the bank's assertion that an item has cleared, recorded
-- ALONGSIDE the books rather than inside them.
--
-- Design memo: docs/design/t4-model.md. Read §0.1 before changing the key.
--
-- WHY THIS IS A TABLE AND NOT A COLUMN ON `posting`
-- ------------------------------------------------
-- PLAN-TALLY-PARITY.md says reconciliation "is metadata on the posting". It
-- cannot be, for two measured reasons, and the whole shape of this table follows
-- from them:
--
--   1. `posting` is HARD IMMUTABLE. Trigger posting_no_mutate fires BEFORE
--      UPDATE OR DELETE and raises unconditionally. Marking a posting cleared
--      would be an UPDATE. There is no "except this one column" escape and there
--      must not be: that immutability is what makes the ledger worth trusting.
--
--   2. AN ALTER RE-CREATES EVERY POSTING ROW. ledger.go inserts a fresh
--      uuid.NewV7() per posting per version, and posting.voucher_version_id is
--      an FK to the version. So the row an operator reconciled is, after any
--      alter, no longer the current version's posting -- and the current
--      version's bank leg is a row that has never existed before.
--
-- A reconciliation keyed on posting.id would therefore go SILENTLY STALE on the
-- first alter: the cleared row drops out of every report (it is not the current
-- version), the new row is unreconciled, and the Bank Reconciliation Statement
-- changes without anybody touching the bank. That is the voucher-lifecycle trap
-- the printed Day Book already fell into once.
--
-- WHY THE KEY IS (company_id, voucher_id, ledger_id, leg_no)
-- ---------------------------------------------------------
-- voucher_id -- NOT voucher_version_id and NOT posting_id -- is the identity
-- that survives an alter; it is what voucher_head is keyed on. ledger_id names
-- the bank account. leg_no disambiguates a voucher with TWO legs on the SAME
-- bank ledger (a bank charge netted against a transfer), and is the ordinal of
-- the leg within the current version's postings for that ledger, ordered by
-- posting.id -- v7 UUIDs are monotonic by creation, so that is the order the
-- legs were submitted in and it is stable within a version.
--
-- leg_no IS THE SHARP EDGE, and cleared_amount/cleared_dr_cr are what blunt it.
-- If an alter reorders or removes legs, an ordinal could re-point at a different
-- leg. So this table records WHAT WAS CLEARED, and the reporting layer compares
-- that against the current version: a disagreement makes the item BROKEN BY
-- ALTER and visible, never silently valid. Storing the amount is therefore NOT
-- redundant with the posting -- it is the entire safety mechanism.
--
-- WHY RECONCILIATION IS *OUTSIDE* THE HASH CHAIN
-- ---------------------------------------------
-- Deliberately unlike T6's TDS facts, which must live inside the hashed
-- canonical payload. A bank date is not a fact about the voucher; it is a fact
-- about the BANK, asserted later by a human, and it changes no balance. Putting
-- it in the payload would mean every reconciliation rewrote a voucher version
-- and broke the very immutability that makes the audit log meaningful. Do not
-- "fix" this by moving it inside.
--
-- RE-RUNNABLE. migrations.go applyOne wraps each file in ONE transaction and
-- stamps schema_migrations INSIDE it, so a file that cannot be re-run walls off
-- every later migration. Hence IF NOT EXISTS on the table, CREATE OR REPLACE on
-- the function, and DROP ... IF EXISTS before each CREATE that has no such
-- clause.

CREATE TABLE IF NOT EXISTS bank_reconciliation (
    id             UUID PRIMARY KEY,
    company_id     UUID NOT NULL,
    -- The STABLE voucher identity. See the header: not the version, not the
    -- posting, both of which are re-created by an alter.
    voucher_id     UUID NOT NULL,
    -- The bank ledger whose statement cleared this item. Constrained to a
    -- ledger under a bank group by the trigger below.
    ledger_id      UUID NOT NULL,
    -- Ordinal of the leg within (voucher, ledger), ordered by posting.id.
    -- 0-based. Almost always 0; non-zero only when one voucher touches the same
    -- bank account twice.
    leg_no         INTEGER NOT NULL DEFAULT 0,
    -- The date the BANK cleared it -- a calendar date off a statement, never an
    -- instant, hence DATE and not TIMESTAMPTZ.
    bank_date      DATE NOT NULL,
    -- WHAT WAS CLEARED. Not redundant with the posting: this is what makes an
    -- alter detectable rather than silent. See the header.
    cleared_amount NUMERIC(19,4) NOT NULL,
    cleared_dr_cr  dr_cr NOT NULL,
    -- Audit. reconciled_at IS an instant, unlike bank_date.
    reconciled_by  TEXT NOT NULL,
    reconciled_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- ONE reconciliation per leg. A second row would make "did this clear, and
    -- on what date?" unanswerable, and would double-count the item in the BRS.
    CONSTRAINT bank_reconciliation_one_per_leg
        UNIQUE (company_id, voucher_id, ledger_id, leg_no),
    -- The composite tenant key, so a future child table can reference the PAIR
    -- and have a cross-tenant id refused BY THE FK rather than by RLS alone.
    CONSTRAINT bank_reconciliation_company_scope UNIQUE (company_id, id),
    -- Composite FKs: a cross-tenant voucher or ledger is refused by the FK
    -- itself. voucher_head's primary key IS (company_id, voucher_id); ledger
    -- carries ledger_company_scope UNIQUE (company_id, id).
    CONSTRAINT bank_reconciliation_voucher_fk
        FOREIGN KEY (company_id, voucher_id)
        REFERENCES voucher_head (company_id, voucher_id),
    CONSTRAINT bank_reconciliation_ledger_fk
        FOREIGN KEY (company_id, ledger_id)
        REFERENCES ledger (company_id, id),
    CONSTRAINT bank_reconciliation_leg_no_non_negative CHECK (leg_no >= 0),
    -- Mirrors posting_amount_check: magnitude is non-negative and DIRECTION is
    -- carried by cleared_dr_cr, never by a sign.
    CONSTRAINT bank_reconciliation_amount_non_negative CHECK (cleared_amount >= 0)
);

-- The BRS reads "every reconciliation for this bank ledger up to a date", so
-- (company_id, ledger_id, bank_date) is the access path. The one-per-leg unique
-- leads with (company_id, voucher_id) and cannot serve it.
CREATE INDEX IF NOT EXISTS bank_reconciliation_ledger_date
    ON bank_reconciliation (company_id, ledger_id, bank_date);

COMMENT ON TABLE bank_reconciliation IS
    'The bank''s assertion that a voucher''s bank leg has cleared, recorded '
    'ALONGSIDE the books. Keyed on the STABLE voucher_id -- never posting.id, '
    'which an alter re-creates -- and carrying what was cleared so an alter is '
    'detectable rather than silent. Deliberately OUTSIDE the hash chain: a bank '
    'date is a fact about the bank, not about the voucher.';

COMMENT ON COLUMN bank_reconciliation.leg_no IS
    'Ordinal of the leg within (voucher, ledger), ordered by posting.id, 0-based. '
    'Non-zero only when one voucher touches the same bank account twice. Safe '
    'under an alter only because cleared_amount/cleared_dr_cr are compared '
    'against the current version -- see the table comment.';

COMMENT ON COLUMN bank_reconciliation.cleared_amount IS
    'What was cleared. NOT redundant with the posting: if the current version''s '
    'leg disagrees, the item is reported BROKEN BY ALTER instead of silently '
    'reconciled. This column is the safety mechanism, not a cache.';

-- ---------------------------------------------------------------------------
-- Two cross-table rules that a CHECK cannot express, so they are a trigger.
--
--   I-3  Only a ledger under a BANK GROUP may be reconciled. Reconciling a
--        Sales ledger is meaningless and would put a nonsense line on the BRS.
--        Checked by ltree ANCESTRY, not by the ledger's immediate group name,
--        so a company that creates "HDFC Current" UNDER "Bank Accounts" still
--        works -- which is exactly how Tally users organise their banks.
--
--   I-7  bank_date may not precede the voucher's own date. A cheque cannot
--        clear before it is written. voucher_head.voucher_date is the current
--        version's date, which is the one a reader compares against.
--
-- Both raise rather than silently skipping the row: a refusal is a support
-- call, a silently-dropped reconciliation is a BRS that will not balance and
-- nobody can say why.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION bank_reconciliation_valid() RETURNS trigger AS $$
DECLARE
    v_date       DATE;
    is_bank      BOOLEAN;
BEGIN
    -- I-3: the ledger's group, or any ancestor of it, must be one of the two
    -- system bank groups. `<@` is ltree "is a descendant of (or equal to)".
    SELECT EXISTS (
        SELECT 1
          FROM ledger l
          JOIN account_group g  ON g.id = l.group_id
          JOIN account_group bg ON bg.company_id = l.company_id
         WHERE l.id = NEW.ledger_id
           AND l.company_id = NEW.company_id
           AND bg.name IN ('Bank Accounts', 'Bank OD A/c')
           AND g.path <@ bg.path
    ) INTO is_bank;

    IF NOT is_bank THEN
        RAISE EXCEPTION
            'bank_reconciliation: ledger % is not under a bank group; only bank ledgers can be reconciled',
            NEW.ledger_id;
    END IF;

    -- I-7: a cheque cannot clear before it is written.
    SELECT vh.voucher_date INTO v_date
      FROM voucher_head vh
     WHERE vh.company_id = NEW.company_id
       AND vh.voucher_id = NEW.voucher_id;

    IF v_date IS NOT NULL AND NEW.bank_date < v_date THEN
        RAISE EXCEPTION
            'bank_reconciliation: bank_date % precedes the voucher date %; a cheque cannot clear before it is written',
            NEW.bank_date, v_date;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS bank_reconciliation_valid_trg ON bank_reconciliation;
CREATE TRIGGER bank_reconciliation_valid_trg
    BEFORE INSERT OR UPDATE ON bank_reconciliation
    FOR EACH ROW EXECUTE FUNCTION bank_reconciliation_valid();

-- ---------------------------------------------------------------------------
-- Row-level security: default-deny, ENABLE *and* FORCE, policy with BOTH USING
-- and WITH CHECK. USING alone would let a session write a row for another
-- company that it can never see or correct; WITH CHECK alone would leak every
-- tenant's bank position on read. FORCE because migrations run as the table
-- owner and an owner bypasses its own policies unless forced.
-- ---------------------------------------------------------------------------
DO $$
BEGIN
    EXECUTE 'ALTER TABLE bank_reconciliation ENABLE ROW LEVEL SECURITY';
    EXECUTE 'ALTER TABLE bank_reconciliation FORCE  ROW LEVEL SECURITY';
    EXECUTE 'DROP POLICY IF EXISTS tenant_isolation ON bank_reconciliation';
    EXECUTE 'CREATE POLICY tenant_isolation ON bank_reconciliation '
         || 'USING (company_id = app_current_company_id()) '
         || 'WITH CHECK (company_id = app_current_company_id())';
END
$$;

GRANT SELECT, INSERT, UPDATE, DELETE ON bank_reconciliation TO tally_app;
