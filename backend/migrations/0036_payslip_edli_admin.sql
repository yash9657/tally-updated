-- 0036_payslip_edli_admin.sql
-- THE EMPLOYER'S THREE PF CHARGES, ON THE PAYSLIP ROW: the EDLI premium, the
-- EDLI administrative charge and the EPF administrative charge.
--
-- WHY THIS MIGRATION EXISTS
-- -------------------------
-- These three are EMPLOYER LIABILITIES TO THE FUND that go to the fund and NOT
-- into the member's account. internal/payroll carries them only as tagged
-- components on PFResult -- pf.go deliberately has no total for them, so that
-- an UNNOTIFIED rate can never be mistaken for a zero one -- and migration 0034
-- gave the payslip table a column for every PFResult TOTAL and none for these.
--
-- The consequence was a real hole in the books, not a reporting gap. The pay
-- run's voucher is built FROM THE PERSISTED PAYSLIPS (it is posted in a second
-- transaction, and crash recovery re-posts from the rows alone), so a figure
-- with no column is a figure the voucher cannot carry: every pay run posted by
-- the product booked the employer's EPF and EPS and NOTHING for EDLI or
-- administration. Under the rates in force before 29 June 2026 that is 0.50% of
-- EDLI wages plus 0.50% of EPF wages -- about Rs 150 per member per month on a
-- 15,000 wage -- of EPFO liability that never reached the ledger, so the challan
-- does not reconcile against the books.
--
-- Storing the figure rather than recomputing it at posting time is the same
-- rule the rest of the payslip follows: what an employee was told they were paid
-- and what a return was filed from must be READABLE YEARS LATER without
-- re-running an engine whose rate table has moved on.
--
-- A ZERO IN THESE COLUMNS IS NOT NECESSARILY A ZERO RATE. Where the rate has not
-- been notified the engine emits a DEFERRAL and no component at all, and the
-- payslip.deferrals JSONB column is where that distinction survives -- exactly
-- as it already does for the EDLI contribution under the EDLI Scheme 2026. For
-- POSTING the two cases are identical (both book nothing), so these columns
-- carry the amount and deferrals carries the reason.
--
-- RLS: NO POLICY CHANGES ARE NEEDED. `payslip` already has ENABLE + FORCE ROW
-- LEVEL SECURITY and a tenant_isolation policy with both USING and WITH CHECK
-- (migration 0034), and a new column on an existing table inherits the table's
-- policies. No new table, no new FK, no new grant.
--
-- IMMUTABILITY: payslip_no_mutate fires on UPDATE and DELETE only. ADD COLUMN is
-- DDL and does not touch a row, so existing payslips keep their figures and pick
-- up the default. THE DEFAULT IS ZERO AND THAT IS A KNOWN UNDERSTATEMENT for any
-- pay run posted before this migration: those vouchers are already short of the
-- charge and cannot be corrected by a backfill, because a payslip is immutable
-- and its voucher is hash-chained. A month that has to be corrected is REVERSED
-- and re-run, which is the same path every other payroll correction takes.
--
-- STATUTORY PROVENANCE OF THE FIGURES THESE COLUMNS HOLD (the rates themselves
-- live in internal/payroll/rates.go, each with its own citation and
-- effective-from date; they are NOT duplicated here, because a rate in two
-- places is a rate that can disagree with itself):
--   * EDLI contribution 0.50% of EDLI wages, effective from 2014-09-01, under
--     para 7 of the EDLI Scheme 1976 -- SECONDARY SOURCE, re-verify. Under the
--     EDLI Scheme 2026 (effective 2026-06-29) the rate is UNVERIFIED and the
--     engine DEFERS it.
--   * EDLI administrative charge 0% (a WAIVER, effective from 2017-04-01) for
--     the pre-2026 windows -- SECONDARY SOURCE, re-verify. UNVERIFIED under the
--     2026 Scheme.
--   * EPF administrative charge 0.50% of EPF wages, effective from 2018-06-01 --
--     SECONDARY SOURCE, re-verify. UNVERIFIED under the EPF Scheme 2026.

ALTER TABLE payslip
    ADD COLUMN IF NOT EXISTS edli_contribution  NUMERIC(19,4) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS edli_admin_charges NUMERIC(19,4) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS epf_admin_charges  NUMERIC(19,4) NOT NULL DEFAULT 0;

COMMENT ON COLUMN payslip.edli_contribution IS
    'The employer''s EDLI premium for this member for this month. It is a '
    'liability to the fund and NOT a contribution into the member''s account, '
    'which is why no PFResult total carries it. Debited to Employer PF '
    'Contribution and credited to PF Payable on the pay run voucher. Zero may '
    'mean "rate not notified" -- see payslip.deferrals.';
COMMENT ON COLUMN payslip.edli_admin_charges IS
    'EDLI administrative charge (EPFO account 22) for this member for this '
    'month. Zero has meant a WAIVER since 2017-04-01 in the pre-2026 windows and '
    'means "not notified" under the EDLI Scheme 2026 -- payslip.deferrals is what '
    'distinguishes the two.';
COMMENT ON COLUMN payslip.epf_admin_charges IS
    'EPF administrative charge (EPFO account 2) for this member for this month. '
    'Same posting treatment as the EDLI premium: an employer charge, never part '
    'of the member''s account and never a deduction from the employee.';

-- The three are never negative. A sign error here would post the employer's
-- charge on the credit side of its own expense, which still balances and is
-- invisible in the Trial Balance total.
--
-- WHY THE DO-BLOCK, when the ADD COLUMNs above just say IF NOT EXISTS: because
-- ADD CONSTRAINT has no such clause, and a bare one makes this whole FILE
-- non-idempotent while its own ADD COLUMNs promise the opposite.
--
-- That is not a tidiness point here. migrations.go applyOne wraps each file in
-- ONE transaction and stamps schema_migrations INSIDE it, so the documented
-- recovery path -- somebody applies this by hand with `psql -f`, which has no
-- transaction envelope, so it is NOT stamped, and then `cored migrate` runs --
-- would abort on "constraint already exists", roll the whole file back, and
-- return an error. No migration after 0036 would ever be applied: the next ACL
-- seed migration would silently never land, and the per-company seeder + fleet
-- backfill pattern that 0033 and 0035 rely on would stop dead. 0036 carries no
-- ACL content itself; it would simply be the wall the next one hits. 0028's
-- header records the by-hand run that proved this failure mode is real.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'payslip_employer_charges_non_negative'
           AND conrelid = 'payslip'::regclass
    ) THEN
        ALTER TABLE payslip
            ADD CONSTRAINT payslip_employer_charges_non_negative
            CHECK (edli_contribution >= 0 AND edli_admin_charges >= 0 AND epf_admin_charges >= 0);
    END IF;
END $$;
