-- 0064_ledger_bank_account.sql
-- THE BANK ACCOUNT'S OWN IDENTITY: account number and IFSC on the ledger.
--
-- Design memo: docs/design/t4-slice2-model.md §1.3, invariant I-11.
--
-- ===========================================================================
-- WHY THIS IS NOT DECORATION
-- ===========================================================================
-- It is how an import knows which ledger a file belongs to. An operator
-- importing three banks' statements in a morning must not have to remember
-- which is which, and a file imported against the WRONG ledger reconciles
-- nothing and corrupts two accounts at once -- silently, because both accounts
-- still balance and the lines are all real.
--
-- With these columns the import can check the file's own account number against
-- the ledger's and refuse a disagreement by name. Without them it can only take
-- the operator's word.
--
-- ===========================================================================
-- ⚠️ COLUMNS AND DATA TOGETHER. THAT IS THE WHOLE INVARIANT (I-11).
-- ===========================================================================
-- HANDOFF-PARITY-SPRINT.md §3.3 states it and it is the reason this migration
-- exists as a unit with the seed change beside it: A BLANK LABELLED FIELD READS
-- AS "THIS BANK HAS NO IFSC". Shipping the columns alone would put two
-- permanently empty rows on the reconciliation screen and make the product look
-- like it lost the data.
--
-- Two things follow, and neither is optional:
--
--   1. The demo company's bank ledger is POPULATED (frontend/scripts/seed-demo.mts),
--      so the shipped demo shows the feature working rather than showing two
--      empty fields.
--   2. NULL AND EMPTY-STRING ARE NOT THE SAME ANSWER, and the CHECKs below
--      enforce it. NULL means "not recorded" and the client must render those
--      words; '' would be an assertion that the account HAS no number, which is
--      never true of a real bank account. A column that permits both has two
--      spellings of one state and every reader gets to pick.
--
-- THERE IS NO BACKFILL, AND THAT IS DELIBERATE. An account number cannot be
-- derived, guessed, or defaulted from anything in this database. Every existing
-- bank ledger is therefore "not recorded" until a human types it, which is the
-- honest state and is exactly what the screen will say.
--
-- ===========================================================================
-- WHY THE FULL ACCOUNT NUMBER, WHEN 0034 STORES ONLY AN EMPLOYEE'S LAST FOUR
-- ===========================================================================
-- 0034's header refuses to store an employee's full account number: "v1 emits
-- no bank advice file, so the full number would be pure exposure". That
-- reasoning is about SOMEBODY ELSE'S account held for a payment we might make.
--
-- This column is THE COMPANY'S OWN ACCOUNT, and the asymmetry is real:
--
--   * A shipped output already needs it. internal/printing puts "A/c: ... IFSC:
--     ..." on an invoice, because a customer cannot pay you without it -- it is
--     a number businesses print on their own stationery.
--   * The matching use above needs the full number to compare against the file.
--     Last-four would match a file the operator mis-selected roughly one time in
--     ten thousand and pass the other times, which is worse than no check: a
--     safety net that usually works is one nobody re-checks by hand.
--
-- WHAT THIS DOES *NOT* DO: it does not re-point the invoice at these columns.
-- print_template.bank_lines (0037) is free text ON THE DOCUMENT and stays the
-- authority for what is printed; these are structured facts ABOUT THE ACCOUNT.
-- Unifying them is T16's business (per-company print templates), and doing it
-- here would change what every existing company's invoice says as a side effect
-- of a bank-reconciliation migration.
--
-- RE-RUNNABLE: ADD COLUMN IF NOT EXISTS, and each CONSTRAINT is dropped before
-- it is added -- ALTER TABLE ... ADD CONSTRAINT has no IF NOT EXISTS form.

ALTER TABLE ledger ADD COLUMN IF NOT EXISTS bank_account_number TEXT NULL;
ALTER TABLE ledger ADD COLUMN IF NOT EXISTS bank_ifsc           TEXT NULL;

-- NULL means "not recorded". '' would be a second spelling of the same state
-- and the two would drift apart the first time a form posted an empty input.
ALTER TABLE ledger DROP CONSTRAINT IF EXISTS ledger_bank_account_number_not_blank;
ALTER TABLE ledger ADD  CONSTRAINT ledger_bank_account_number_not_blank
    CHECK (bank_account_number IS NULL OR length(btrim(bank_account_number)) > 0);

-- THE IFSC SHAPE IS FIXED AND PUBLIC: four letters (the bank code), then '0'
-- (reserved), then six alphanumerics (the branch code) -- eleven characters.
-- Every IFSC already in this repository's own fixtures obeys it: SBIN0001234
-- (internal/printing), HDFC0000123 (printingdb), HDFC0004321 (the demo OCR
-- fixture), HDFC0001234 (handler_print_test).
--
-- IT IS CHECKED HERE RATHER THAN ONLY IN THE CLIENT because this column's
-- entire purpose is to be COMPARED against a value read out of a bank's file. A
-- typo'd IFSC does not fail loudly -- it just never matches, and "the import
-- says this is the wrong account" is then a lie the operator cannot debug.
ALTER TABLE ledger DROP CONSTRAINT IF EXISTS ledger_bank_ifsc_shape;
ALTER TABLE ledger ADD  CONSTRAINT ledger_bank_ifsc_shape
    CHECK (bank_ifsc IS NULL OR bank_ifsc ~ '^[A-Z]{4}0[A-Z0-9]{6}$');

COMMENT ON COLUMN ledger.bank_account_number IS
    'The company''s OWN account number for this bank ledger, in full. NULL means '
    'NOT RECORDED and a client must render those words -- a blank field reads as '
    '"this bank has no account number" (invariant I-11). Stored in full, unlike '
    'employee.bank_account_last4, because a statement import compares it against '
    'the file''s own account number; see 0064''s header for the asymmetry.';

COMMENT ON COLUMN ledger.bank_ifsc IS
    'The IFSC of this bank ledger''s branch: four letters, ''0'', six '
    'alphanumerics. NULL means NOT RECORDED. Shape-checked in the database '
    'because the column exists to be COMPARED against a bank file, and a typo '
    'that merely fails to match produces a refusal nobody can debug.';
