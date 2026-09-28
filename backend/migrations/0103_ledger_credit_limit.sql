-- 0103_ledger_credit_limit.sql
-- W4: the party's CREDIT LIMIT, the one input the credit-limit-breach detector
-- (PLAN-AGENTIC W4, r6 §8.1 #11) cannot run without.
--
-- TallyPrime keeps a credit limit on the party ledger master ("Specify credit
-- limit" beside "Default credit period"); this repo had the period
-- (credit_days, 0002) and never the limit, so "exposure > limit" had no limit
-- to compare against. It is a ledger column for the reason credit_days is:
-- the limit is an attribute of the party, set with the party, audited with the
-- party (masters.CreateLedger writes it into master_audit's after_doc).
--
-- ⛔ NULL MEANS "NO LIMIT AGREED", NOT ZERO. A zero limit is a real and
-- different instruction (cash only; every credit sale breaches), and a default
-- of 0 would make every existing debtor breach on the first sale after this
-- migration. So: NULL by default, and the detector is silent for NULL.
--
-- No ACL: the column rides the existing `master` token of POST /ledgers; the
-- seeder census stays at 17 PERFORMs.
ALTER TABLE ledger ADD COLUMN IF NOT EXISTS credit_limit NUMERIC(19,4) NULL;
ALTER TABLE ledger DROP CONSTRAINT IF EXISTS ledger_credit_limit_non_negative;
ALTER TABLE ledger ADD  CONSTRAINT ledger_credit_limit_non_negative
    CHECK (credit_limit IS NULL OR credit_limit >= 0);
COMMENT ON COLUMN ledger.credit_limit IS
    'The party''s credit limit in the base currency (Tally: "Specify credit '
    'limit"). NULL = no limit agreed (the breach detector is silent); 0 = no '
    'credit at all. Compared against the ledger''s debit closing balance.';
