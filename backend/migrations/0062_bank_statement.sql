-- 0062_bank_statement.sql
-- BANK STATEMENT IMPORT: what the BANK says, recorded next to what the books
-- say, and never confused with it.
--
-- Design memo: docs/design/t4-slice2-model.md. Read §8 first -- it settles the
-- two product calls that shape these tables, and §2.1, which is why NEITHER of
-- them records a clearance.
--
-- WHY THESE TABLES DO NOT RECORD "CLEARED"
-- ----------------------------------------
-- A resolved match writes a `bank_reconciliation` row -- the SAME table the
-- manual bank-date loop writes (memo §2.1). These tables record the statement
-- and, later, the matching PROCESS; the outcome lands in 0045's table.
--
-- Two reasons, the second load-bearing:
--
--   1. The BRS, the BRS print and reporting.bankReconSQL all read
--      bank_reconciliation. A second source of "cleared" is two statements that
--      can disagree, with no way for an operator to know which is authoritative.
--
--   2. bank_reconciliation CARRIES THE ALTER-DETECTION MECHANISM. It stores
--      cleared_amount/cleared_dr_cr and the reporting layer compares them
--      against the current version, marking an item BROKEN BY ALTER rather than
--      silently valid (0045's header). A matcher writing clearances anywhere
--      else would lose that, and lose it SILENTLY -- the exact failure 0045 was
--      built to prevent.
--
-- ⚠️ DIRECTION IS INVERTED RELATIVE TO THE BANK'S OWN WORDING, AND THAT IS THE
--    SHARPEST EDGE IN THIS FILE. See the comment on bank_statement_line.dr_cr.
--
-- WHAT IS DELIBERATELY NOT HERE
-- -----------------------------
--   * `bank_statement_match` -- the candidate links. That is slice 2b (memo
--     §8.3) and it arrives with the matcher that fills it, not before.
--   * A `reconciliation_id` column on bank_statement_line. Memo §2.1 sketches
--     one, but WHERE the line-to-clearance link lives is a matching decision:
--     it may belong on the match row instead. Adding a nullable FK now that
--     nothing writes would be a column shipped ahead of its meaning, which is
--     the smaller cousin of the "correct code nothing calls" defect Item Zero
--     existed to pay off. 2b decides it, in 2b's migration.
--
-- RE-RUNNABLE. migrations.go applyOne wraps each file in ONE transaction and
-- stamps schema_migrations INSIDE it, so a file that cannot be re-run walls off
-- every later migration. Hence IF NOT EXISTS on tables and indexes, CREATE OR
-- REPLACE on functions, and DROP ... IF EXISTS before each CREATE lacking one.

-- ---------------------------------------------------------------------------
-- ONE definition of "this ledger is a bank account", in the database.
--
-- IT IS ANCESTRY, NOT A GROUP NAME. A company may keep its accounts in a
-- user-created "Current Accounts" sub-group UNDER the seeded "Bank Accounts"
-- primary, so such a ledger's IMMEDIATE group name is "Current Accounts" and
-- only `path <@ bg.path` recognises it. That reasoning is written out at length
-- on reporting.BankLedgerPredicate, which is the Go half of the same rule.
--
-- 0045 inlined this test in bank_reconciliation_valid(). This migration needs
-- the identical test for imports, so rather than write a second copy it extracts
-- ONE function and re-points 0045's trigger at it below. A copy that drifted
-- would fail in the worse direction: an import silently refused for a real bank
-- account reads as "this product does not know about my account".
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION is_bank_ledger(p_company_id UUID, p_ledger_id UUID)
RETURNS BOOLEAN AS $$
    SELECT EXISTS (
        SELECT 1
          FROM ledger l
          JOIN account_group g  ON g.id = l.group_id
          JOIN account_group bg ON bg.company_id = l.company_id
         WHERE l.id         = p_ledger_id
           AND l.company_id = p_company_id
           AND bg.name IN ('Bank Accounts', 'Bank OD A/c')
           AND g.path <@ bg.path
    );
$$ LANGUAGE sql STABLE;

GRANT EXECUTE ON FUNCTION is_bank_ledger(UUID, UUID) TO tally_app;

COMMENT ON FUNCTION is_bank_ledger(UUID, UUID) IS
    'THE database-side definition of "this ledger is a bank account", by ltree '
    'ANCESTRY of its group -- not by the group''s name, so a ledger under a '
    'user-created sub-group of "Bank Accounts" still counts. Mirrors '
    'reporting.BankLedgerPredicate. Called by bank_reconciliation_valid() and '
    'bank_statement_import_valid(); do not inline a third copy.';

-- ---------------------------------------------------------------------------
-- THE BATCH: one imported file.
--
-- It is a first-class row rather than a column on the lines because the
-- QUESTION "how was this file interpreted?" has to stay answerable months later
-- (memo §8.2). column_mapping is the operator's confirmed answer to that, stored
-- verbatim.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS bank_statement_import (
    id            UUID PRIMARY KEY,
    company_id    UUID NOT NULL,
    -- WHICH BANK ACCOUNT this file belongs to. Constrained to a ledger under a
    -- bank group by the trigger below, for the same reason 0045 constrains its
    -- own: a statement imported against a Sales ledger reconciles nothing and
    -- would put a nonsense account on the screen.
    ledger_id     UUID NOT NULL,

    -- Provenance. file_name is what the operator uploaded, kept for the screen;
    -- file_sha256 is the identity that makes a double-click detectable.
    file_name     TEXT NOT NULL,
    file_sha256   TEXT NOT NULL,

    -- 'csv' is the only value this build parses (memo §8.1). It is a COLUMN
    -- rather than an assumption so MT940 later is a new value and a new parser,
    -- not a schema change -- and so a row always says how it was read.
    format        TEXT NOT NULL,

    -- THE CONFIRMED COLUMN MAPPING (memo §8.2), verbatim. There are no built-in
    -- per-bank profiles in this build and this column is why that costs nothing:
    -- the interpretation travels with the data instead of living in a constant
    -- that a future edition of somebody's export silently invalidates.
    column_mapping JSONB NOT NULL,

    -- What the FILE claimed, for evidence. These are the bank's own arithmetic,
    -- NOT ours: nothing is ever derived from them. They are nullable because
    -- not every export carries a running balance -- and when they are NULL, the
    -- balance walk (memo B-4) cannot run and the import says so rather than
    -- reporting a walk it never performed.
    opening_balance NUMERIC(19,4),
    closing_balance NUMERIC(19,4),

    -- The period the file covers, derived from its own lines. DATE, never
    -- TIMESTAMPTZ: a bank asserts a day, not an instant.
    period_from   DATE,
    period_to     DATE,

    line_count    INTEGER NOT NULL,

    imported_by   TEXT NOT NULL,
    imported_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- THE SAME FILE, INTO THE SAME ACCOUNT, TWICE IS ONE IMPORT (memo B-3).
    -- This is the constraint an operator hits in week one having clicked twice.
    -- It is scoped to the ledger on purpose: the same file legitimately imported
    -- against a DIFFERENT account is a different assertion, and refusing it here
    -- would refuse a real case in order to catch a mistake the trigger below
    -- already catches better.
    CONSTRAINT bank_statement_import_one_per_file
        UNIQUE (company_id, ledger_id, file_sha256),
    -- The composite tenant key, so a child references the PAIR and a
    -- cross-tenant id is refused BY THE FK rather than by RLS alone.
    CONSTRAINT bank_statement_import_company_scope UNIQUE (company_id, id),
    -- The three-column key the lines reference. It is what makes
    -- "line.ledger_id = its import's ledger_id" an FK guarantee rather than
    -- application discipline -- see bank_statement_line_import_fk.
    CONSTRAINT bank_statement_import_scope_ledger UNIQUE (company_id, id, ledger_id),
    CONSTRAINT bank_statement_import_ledger_fk
        FOREIGN KEY (company_id, ledger_id) REFERENCES ledger (company_id, id),
    CONSTRAINT bank_statement_import_format_known
        CHECK (format IN ('csv')),
    CONSTRAINT bank_statement_import_line_count_non_negative
        CHECK (line_count >= 0),
    -- period_from <= period_to when both are known. A file whose own dates run
    -- backwards was mis-parsed, and the date columns are the most commonly
    -- mis-mapped of all (dd/mm vs mm/dd).
    CONSTRAINT bank_statement_import_period_ordered
        CHECK (period_from IS NULL OR period_to IS NULL OR period_from <= period_to)
);

CREATE INDEX IF NOT EXISTS bank_statement_import_ledger_period
    ON bank_statement_import (company_id, ledger_id, period_from);

COMMENT ON TABLE bank_statement_import IS
    'One imported bank statement file. Records the file, WHO imported it, and '
    'the confirmed column mapping it was read with -- so "how was this file '
    'interpreted?" stays answerable. Records NO clearance: a resolved match '
    'writes bank_reconciliation, which is the single source of "cleared".';

COMMENT ON COLUMN bank_statement_import.column_mapping IS
    'The operator-confirmed mapping from this file''s columns to our fields. '
    'This build ships NO per-bank profiles: a layout recalled from memory that '
    'is wrong by one column reads the running balance as the amount, and every '
    'figure is then a real number off a real statement that was never a '
    'transaction. See docs/design/t4-slice2-model.md §8.2.';

COMMENT ON COLUMN bank_statement_import.opening_balance IS
    'The bank''s own opening figure, for VERIFICATION ONLY -- it is how a '
    'dropped or duplicated row is detected, never an input to any derivation. '
    'NULL when the export carries no running balance, in which case the balance '
    'walk cannot run and the import reports that rather than implying it passed.';

-- ---------------------------------------------------------------------------
-- THE LINES: what the bank said, one row per transaction on the statement.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS bank_statement_line (
    id            UUID PRIMARY KEY,
    company_id    UUID NOT NULL,
    import_id     UUID NOT NULL,
    -- Denormalised from the import so the natural key and the query path can
    -- lead with it. The three-column FK below makes it impossible for it to
    -- disagree with the import's own ledger.
    ledger_id     UUID NOT NULL,

    -- 1-based position in the FILE. This is what a refusal names ("the balance
    -- walk breaks at row 47"), and a row number an operator can find in their
    -- spreadsheet is the difference between a fixable import and a support call.
    line_no       INTEGER NOT NULL,

    -- The bank's own date. This is what becomes bank_date on a match, so it is
    -- the one that matters. DATE, never TIMESTAMPTZ.
    value_date    DATE NOT NULL,
    -- May differ from the value date. STORED, NEVER CONFLATED (memo §1.1):
    -- collapsing the two loses the only evidence that a cheque was presented on
    -- one day and credited on another. NULL when the format carries only one.
    txn_date      DATE,

    description   TEXT NOT NULL,

    -- MAGNITUDE ONLY, exactly as everywhere else in this codebase. The CHECK
    -- mirrors posting_amount_check and bank_reconciliation's.
    amount        NUMERIC(19,4) NOT NULL,

    -- ⚠️ DIRECTION IN *OUR* ORIENTATION, WHICH IS THE OPPOSITE OF THE WORD ON
    --    THE STATEMENT. A deposit into our account is a DEBIT to the bank
    --    ledger in our books (the asset rose); the bank calls it a credit,
    --    because in the BANK'S books we are a liability. See the column comment.
    dr_cr         dr_cr NOT NULL,

    -- The bank's own running balance. VERIFICATION ONLY -- see the import's
    -- opening_balance comment. NULL when the export omits it.
    running_balance NUMERIC(19,4),

    -- The strongest matching key when present (memo §1.1). Both nullable
    -- because most exports carry one or the other and some carry neither.
    bank_reference TEXT,
    cheque_number  TEXT,

    -- The original row, verbatim. "Anything richer is preserved rather than
    -- parsed" (memo §1.1): a field we did not model today is still recoverable
    -- tomorrow without asking the operator to re-download a statement their
    -- bank may no longer serve.
    raw           JSONB NOT NULL,

    -- ---------------------------------------------------------------------
    -- RE-IMPORT IDENTITY (memo B-3). line_hash is over the transaction's own
    -- fields -- value date, txn date, description, amount, direction,
    -- reference, cheque number -- and DELIBERATELY NOT over running_balance:
    -- the same rows re-exported with the balance column turned off must still
    -- deduplicate, or an overlapping download silently doubles the statement.
    --
    -- occurrence IS THE PART THAT IS EASY TO GET WRONG. Two genuinely distinct
    -- transactions can be identical in every modelled field (two identical ATM
    -- withdrawals on one day). A bare unique-on-hash would silently DROP the
    -- second -- an under-import, which is worse than a duplicate because
    -- nothing on the screen is wrong, there is simply less of it. So the key
    -- carries the ordinal among identical lines WITHIN a file, 0-based: two
    -- such rows are 0 and 1, and an overlapping file reproduces the same two
    -- ordinals and collides with both.
    -- ---------------------------------------------------------------------
    line_hash     TEXT NOT NULL,
    occurrence    INTEGER NOT NULL DEFAULT 0,

    CONSTRAINT bank_statement_line_company_scope UNIQUE (company_id, id),
    -- ONE row per distinct transaction per bank account, across every import.
    -- This is what makes a re-import a no-op instead of a doubled statement.
    CONSTRAINT bank_statement_line_one_per_txn
        UNIQUE (company_id, ledger_id, line_hash, occurrence),
    -- The three-column FK: the line's ledger MUST be its import's ledger. A
    -- two-column FK to the import plus a separate one to the ledger would let a
    -- line claim a different account from the file it came in on, and every
    -- report keyed on ledger_id would then disagree with the batch.
    CONSTRAINT bank_statement_line_import_fk
        FOREIGN KEY (company_id, import_id, ledger_id)
        REFERENCES bank_statement_import (company_id, id, ledger_id)
        ON DELETE CASCADE,
    CONSTRAINT bank_statement_line_amount_non_negative CHECK (amount >= 0),
    CONSTRAINT bank_statement_line_no_positive        CHECK (line_no >= 1),
    CONSTRAINT bank_statement_line_occurrence_non_negative CHECK (occurrence >= 0)
);

-- The screen reads "every line for this account over a period", so
-- (company_id, ledger_id, value_date) is the access path. The one-per-txn
-- unique leads with the hash and cannot serve it.
CREATE INDEX IF NOT EXISTS bank_statement_line_ledger_date
    ON bank_statement_line (company_id, ledger_id, value_date);
CREATE INDEX IF NOT EXISTS bank_statement_line_import
    ON bank_statement_line (company_id, import_id);

COMMENT ON TABLE bank_statement_line IS
    'One transaction as the BANK reported it. Never a clearance: a line becomes '
    'reconciled only by a match writing a bank_reconciliation row (memo §2.1). '
    'Deduplicated across imports by (line_hash, occurrence) so an overlapping '
    'download is a no-op rather than a doubled statement.';

COMMENT ON COLUMN bank_statement_line.dr_cr IS
    'Direction IN OUR BOOKS, which INVERTS the word on the statement: a DEPOSIT '
    'is ''D'' (our bank asset rose) though the bank calls it a credit, and a '
    'WITHDRAWAL is ''C''. Stored our way because the matcher compares it '
    'directly against a posting''s dr_cr -- normalising at the parser instead '
    'would leave every comparison to remember the inversion, and the one that '
    'forgot would produce ZERO matches, which reads as "nothing to reconcile".';

COMMENT ON COLUMN bank_statement_line.occurrence IS
    'Ordinal among lines with an identical line_hash within one file, 0-based. '
    'It exists so two genuinely identical transactions both survive: without it '
    'the uniqueness constraint would silently drop the second, which is an '
    'UNDER-import and shows nothing wrong on screen.';

COMMENT ON COLUMN bank_statement_line.running_balance IS
    'The bank''s own arithmetic, kept for VERIFICATION ONLY. It is how a '
    'dropped or duplicated row is detected (memo B-4); it is never summed, '
    'never differenced, and never used to derive an amount.';

-- ---------------------------------------------------------------------------
-- A statement may only be imported against a BANK ledger -- the same rule 0045
-- enforces for a reconciliation, via the same function.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION bank_statement_import_valid() RETURNS trigger AS $$
BEGIN
    IF NOT is_bank_ledger(NEW.company_id, NEW.ledger_id) THEN
        RAISE EXCEPTION
            'bank_statement_import: ledger % is not under a bank group; a bank statement can only be imported against a bank account',
            NEW.ledger_id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS bank_statement_import_valid_trg ON bank_statement_import;
CREATE TRIGGER bank_statement_import_valid_trg
    BEFORE INSERT OR UPDATE ON bank_statement_import
    FOR EACH ROW EXECUTE FUNCTION bank_statement_import_valid();

-- ---------------------------------------------------------------------------
-- Re-point 0045's trigger function at the extracted definition, so the database
-- holds ONE answer to "is this a bank ledger". The body is otherwise unchanged
-- from 0045 -- including invariant I-7, the cheque that cannot clear before it
-- is written -- and is restated in full because CREATE OR REPLACE replaces the
-- whole function and a partial restatement would silently drop I-7.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION bank_reconciliation_valid() RETURNS trigger AS $$
DECLARE
    v_date DATE;
BEGIN
    -- I-3: the ledger's group, or any ancestor of it, must be a bank group.
    IF NOT is_bank_ledger(NEW.company_id, NEW.ledger_id) THEN
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

-- ---------------------------------------------------------------------------
-- Row-level security: default-deny, ENABLE *and* FORCE, policy with BOTH USING
-- and WITH CHECK. USING alone would let a session write a row for another
-- company that it can never see or correct; WITH CHECK alone would leak every
-- tenant's bank statement on read. FORCE because migrations run as the table
-- owner and an owner bypasses its own policies unless forced.
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['bank_statement_import', 'bank_statement_line'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE  ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I '
            'USING (company_id = app_current_company_id()) '
            'WITH CHECK (company_id = app_current_company_id())', t);
    END LOOP;
END
$$;

GRANT SELECT, INSERT, UPDATE, DELETE ON bank_statement_import TO tally_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON bank_statement_line   TO tally_app;
