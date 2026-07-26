-- 0017_ledger_opening_bill.sql
-- BILL-WISE OPENING BREAKUP: the individual unpaid bills that make up a party
-- ledger's opening balance.
--
-- THE TALLY CONCEPT
-- -----------------
-- When a business migrates its books in, a customer does not owe one lump sum —
-- it owes specific invoices. Tally makes you break the opening balance of any
-- bill-wise ledger into its constituent references ("New Ref" per bill, with
-- its own due date) and REFUSES to accept a breakup that does not add up to the
-- opening balance. Without it the ageing report is a lie on day one: every
-- carried-forward rupee looks equally old (or equally new).
--
-- WHY A SEPARATE TABLE FROM bill_allocation
-- -----------------------------------------
-- bill_allocation.voucher_version_id is NOT NULL: allocations are VERSIONED with
-- the voucher that made them, which is what lets an alter re-state them. An
-- opening balance is not a voucher — it is a property of the ledger MASTER, set
-- as at books_beginning_from. Forcing it into bill_allocation would mean either
-- a nullable voucher_version_id (destroying that invariant for every row) or a
-- synthetic phantom voucher that would then appear in the Day Book and the edit
-- log as a transaction that never happened.
--
-- CONSEQUENCE FOR REPORTING
-- -------------------------
-- Outstandings must UNION these rows with bill_allocation, because the Balance
-- Sheet's Sundry Debtors/Creditors totals DO include ledger.opening_balance
-- (reporting.go folds it into the closing figure). Omitting them would make the
-- two reports disagree by exactly the opening balance — the same class of silent
-- cross-report disagreement this pass exists to remove.
--
-- Masters are mutable in this schema (a ledger can be re-grouped or renamed), so
-- unlike posting/bill_allocation these rows carry no immutability trigger;
-- master_audit records changes to the owning ledger.

CREATE TABLE ledger_opening_bill (
    id         UUID PRIMARY KEY,
    company_id UUID NOT NULL,
    ledger_id  UUID NOT NULL REFERENCES ledger (id),
    ref_name   TEXT NOT NULL CHECK (ref_name <> ''),
    -- Reuses the same enum as bill_allocation. An opening bill is almost always
    -- NEW (it opens an outstanding); ADVANCE covers money held on account at
    -- migration time.
    ref_type   bill_ref_type NOT NULL DEFAULT 'NEW',
    dr_cr      dr_cr NOT NULL,
    amount     NUMERIC(19,4) NOT NULL CHECK (amount >= 0),
    -- The bill's own due date, which is what makes the opening ageing real
    -- rather than uniform.
    due_date   DATE NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- One reference name per ledger: a second "INV-77" for the same party would
    -- be ambiguous to settle against.
    UNIQUE (company_id, ledger_id, ref_name)
);

CREATE INDEX ledger_opening_bill_company ON ledger_opening_bill (company_id);
CREATE INDEX ledger_opening_bill_ledger  ON ledger_opening_bill (company_id, ledger_id);

COMMENT ON TABLE ledger_opening_bill IS
    'Bill-wise breakup of a ledger''s OPENING balance, as at '
    'company.books_beginning_from. Sums to ledger.opening_balance (enforced in '
    'masters.CreateLedger). UNIONed with bill_allocation by the Outstandings '
    'report, whose total must tie to the Balance Sheet.';

-- Default-deny, ENABLE + FORCE RLS, USING + WITH CHECK on the total resolver.
ALTER TABLE ledger_opening_bill ENABLE ROW LEVEL SECURITY;
ALTER TABLE ledger_opening_bill FORCE  ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON ledger_opening_bill
    USING      (company_id = app_current_company_id())
    WITH CHECK (company_id = app_current_company_id());

GRANT SELECT, INSERT, UPDATE, DELETE ON ledger_opening_bill TO tally_app;
