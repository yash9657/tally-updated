-- 0101_bank_statement_proof_and_narration.sql
-- W5 (PLAN-AGENTIC.md §3): THE BALANCE PROOF EVERY IMPORT NOW CARRIES, AND THE
-- STRUCTURED READING OF EVERY ROW'S NARRATION. Design memo:
-- docs/design/d123-w5-documents-bank.md §2.
--
-- ===========================================================================
-- ⛔ WHY THE PROOF IS STORED, AND WHY opening_balance CAN NO LONGER SAY IT
-- ===========================================================================
-- 0062 recorded opening/closing balance only when the RUNNING-BALANCE walk ran,
-- and reporting/bankstatement.go reads `opening_balance IS NOT NULL` as "the
-- walk ran". W5 adds a second proof — DECLARED opening and closing balances,
-- from the file's own summary or keyed by the operator off the printed
-- statement, with opening + deposits − withdrawals = closing — so a statement
-- with NO running balance now stores an opening balance too. Left alone, the
-- read model would report every declared-only import as balance-WALKED, which
-- is the exact confusion 0062's comment exists to prevent ("a check that did
-- not run must never be reported the same way as a check that passed").
--
-- So the proof is stored as what it IS: balance_proof names which check held.
-- NULL is every import written before this file, and is read exactly as 0062
-- defined it (the walk ran iff opening_balance is set). Nothing is backfilled:
-- a proof that was never run cannot be recorded after the fact.
--
-- ===========================================================================
-- THE NARRATION COLUMNS ARE ANNOTATIONS, NOT FACTS
-- ===========================================================================
-- bankstatement/narration.go reads the payment rail, a counterparty, a VPA and
-- the rail's reference (UTR / RRN / cheque number) out of the bank's free-text
-- description. They are stored so the matcher and the party picker can use
-- them without re-parsing, and so the reading that was SHOWN to an operator is
-- the one on record. ⛔ They are DELIBERATELY NOT PART OF line_hash (0062): a
-- better parser next year must not make a re-import of the same file look like
-- new transactions. All four are nullable; NULL is "not recognised", never "".
--
-- No new table, so no RLS block: both tables are already ENABLE + FORCE RLS
-- with USING and WITH CHECK (0062). No ACL: the columns ride the existing
-- `bank-statement` resource token (0063), and no route is added, so the
-- seeder census (seeder_census_test.go) is unchanged by this file.
--
-- RE-RUNNABLE: every statement is IF NOT EXISTS or a DROP-then-ADD of a named
-- constraint, because migrations.go wraps each file in one transaction and a
-- file that cannot re-run walls off every later migration.

ALTER TABLE bank_statement_import
    ADD COLUMN IF NOT EXISTS balance_proof TEXT,
    ADD COLUMN IF NOT EXISTS balance_declared_by TEXT,
    ADD COLUMN IF NOT EXISTS row_order TEXT;

ALTER TABLE bank_statement_import DROP CONSTRAINT IF EXISTS bank_statement_import_balance_proof_known;
ALTER TABLE bank_statement_import ADD CONSTRAINT bank_statement_import_balance_proof_known
    CHECK (balance_proof IS NULL OR balance_proof IN ('running', 'declared', 'running+declared'));

ALTER TABLE bank_statement_import DROP CONSTRAINT IF EXISTS bank_statement_import_declared_by_known;
ALTER TABLE bank_statement_import ADD CONSTRAINT bank_statement_import_declared_by_known
    CHECK (balance_declared_by IS NULL OR balance_declared_by IN ('file', 'operator', 'file+operator'));

-- A proof that used declared figures must say whose they were, and one that
-- did not must not claim anybody declared anything.
ALTER TABLE bank_statement_import DROP CONSTRAINT IF EXISTS bank_statement_import_declared_by_matches_proof;
ALTER TABLE bank_statement_import ADD CONSTRAINT bank_statement_import_declared_by_matches_proof
    CHECK (
        balance_proof IS NULL
        OR (balance_proof = 'running' AND balance_declared_by IS NULL)
        OR (balance_proof IN ('declared', 'running+declared') AND balance_declared_by IS NOT NULL)
    );

-- ⛔ A PROOF IS BOUND BETWEEN TWO BALANCES; an import that names a proof and
-- stores no balance has recorded a proof it cannot show.
ALTER TABLE bank_statement_import DROP CONSTRAINT IF EXISTS bank_statement_import_proof_has_balances;
ALTER TABLE bank_statement_import ADD CONSTRAINT bank_statement_import_proof_has_balances
    CHECK (balance_proof IS NULL OR (opening_balance IS NOT NULL AND closing_balance IS NOT NULL));

ALTER TABLE bank_statement_import DROP CONSTRAINT IF EXISTS bank_statement_import_row_order_known;
ALTER TABLE bank_statement_import ADD CONSTRAINT bank_statement_import_row_order_known
    CHECK (row_order IS NULL OR row_order IN ('oldest-first', 'newest-first'));

COMMENT ON COLUMN bank_statement_import.balance_proof IS
    'W5. WHICH balance proof held: ''running'' (every row''s running balance '
    'walks from the one before it), ''declared'' (opening + deposits − '
    'withdrawals = closing, on figures the file or the operator declared), or '
    '''running+declared'' (both, and the walk starts and ends on the declared '
    'figures — the only check that sees a missing first or last row). NULL only '
    'on imports written before 0101, which read as 0062 defined them. ⛔ Since '
    '0101 no statement is imported without a proof.';

COMMENT ON COLUMN bank_statement_import.balance_declared_by IS
    'W5. Where the DECLARED opening/closing came from: ''file'' (its own '
    'preamble/footer summary, read by label), ''operator'' (keyed off the '
    'printed statement), or ''file+operator'' (both, and equal — a disagreement '
    'refuses the import). NULL when the proof used no declared figure.';

COMMENT ON COLUMN bank_statement_import.row_order IS
    'W5. The row order the RUNNING BALANCES revealed. Never inferred from dates, '
    'which cannot order two rows on one day. NULL when the file carried no '
    'running balance to reveal it.';

ALTER TABLE bank_statement_line
    ADD COLUMN IF NOT EXISTS txn_rail TEXT,
    ADD COLUMN IF NOT EXISTS counterparty TEXT,
    ADD COLUMN IF NOT EXISTS counterparty_vpa TEXT,
    ADD COLUMN IF NOT EXISTS rail_reference TEXT;

ALTER TABLE bank_statement_line DROP CONSTRAINT IF EXISTS bank_statement_line_txn_rail_known;
ALTER TABLE bank_statement_line ADD CONSTRAINT bank_statement_line_txn_rail_known
    CHECK (txn_rail IS NULL OR txn_rail IN
        ('UPI', 'NEFT', 'IMPS', 'RTGS', 'NACH', 'CHEQUE', 'ATM', 'CARD', 'INTEREST', 'CHARGES'));

-- '' is refused on all four: an empty string would read as "recognised, and
-- blank", which is not a thing the parser can mean.
ALTER TABLE bank_statement_line DROP CONSTRAINT IF EXISTS bank_statement_line_narration_not_blank;
ALTER TABLE bank_statement_line ADD CONSTRAINT bank_statement_line_narration_not_blank
    CHECK (
        (counterparty IS NULL OR length(btrim(counterparty)) > 0)
        AND (counterparty_vpa IS NULL OR length(btrim(counterparty_vpa)) > 0)
        AND (rail_reference IS NULL OR length(btrim(rail_reference)) > 0)
    );

COMMENT ON COLUMN bank_statement_line.txn_rail IS
    'W5. The payment rail read off a keyword in the narration (bankstatement/'
    'narration.go). An ANNOTATION: never an input to amount, direction, date or '
    'clearance, and NOT part of line_hash. NULL = no keyword recognised.';
COMMENT ON COLUMN bank_statement_line.counterparty IS
    'W5. A SUGGESTED counterparty — the first narration segment that reads as a '
    'name. The weakest parsed field (each bank orders its segments differently); '
    'for the party picker, never for matching money.';
COMMENT ON COLUMN bank_statement_line.counterparty_vpa IS
    'W5. The UPI VPA (handle@psp) found in the narration, lower-cased.';
COMMENT ON COLUMN bank_statement_line.rail_reference IS
    'W5. The rail''s own identifier, by format: the 12-digit RRN for UPI/IMPS, '
    'the UTR for NEFT/RTGS, the 6-digit cheque number for a cheque row. Distinct '
    'from bank_reference, which is the file''s own reference COLUMN.';
