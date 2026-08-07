package reporting_test

// T4 bank reconciliation. Each test here is named for an invariant in
// docs/design/t4-model.md §4, and several are named for the BREACH rather than
// the behaviour, because that is what makes a regression legible.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/banking"
	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/reporting"
)

// bankFixture creates a bank ledger and three payments out of it, each on its
// own date, plus one receipt in. Nothing is reconciled yet.
type bankFixture struct {
	bank     uuid.UUID
	rent     uuid.UUID
	acme     uuid.UUID
	vouchers []uuid.UUID // in the order posted
	amounts  []decimal.Decimal
}

func bankSetup(t *testing.T) (*db.Pool, context.Context, uuid.UUID, bankFixture) {
	t.Helper()
	pool, ctx, companyID, ids := setup(t)
	led := ledger.NewService(pool)

	paymentVT, err := ids.VoucherTypeID(ctx, "Payment")
	if err != nil {
		t.Fatalf("payment vt: %v", err)
	}
	receiptVT, err := ids.VoucherTypeID(ctx, "Receipt")
	if err != nil {
		t.Fatalf("receipt vt: %v", err)
	}

	var f bankFixture
	mk := func(name, group string) uuid.UUID {
		id, err := ids.CreateLedger(ctx, name, group)
		if err != nil {
			t.Fatalf("create ledger %s: %v", name, err)
		}
		return id
	}
	// "Bank Accounts" is a SYSTEM group seeded with every company; a ledger under
	// it is what makes this reconcilable at all (invariant I-3).
	f.bank = mk("HDFC Current", "Bank Accounts")
	f.rent = mk("Rent Paid", "Indirect Expenses")
	f.acme = mk("Acme Corp", "Sundry Debtors")

	pay := func(no, date, amount string) uuid.UUID {
		res, err := led.PostVoucher(ctx, ledger.VoucherInput{
			Class:         ledger.Payment,
			VoucherTypeID: paymentVT,
			VoucherNo:     no,
			VoucherDate:   date,
			Narration:     "cheque " + no,
			CreatedBy:     "tester",
			Postings: []ledger.PostingInput{
				{LedgerID: f.rent, DrCr: ledger.Debit, Amount: dec(amount)},
				{LedgerID: f.bank, DrCr: ledger.Credit, Amount: dec(amount)},
			},
		})
		if err != nil {
			t.Fatalf("post payment %s: %v", no, err)
		}
		f.vouchers = append(f.vouchers, res.VoucherID)
		f.amounts = append(f.amounts, dec(amount))
		return res.VoucherID
	}
	pay("PAY-1", "2025-04-05", "1000")
	pay("PAY-2", "2025-04-12", "250")
	pay("PAY-3", "2025-04-20", "75")

	// A receipt IN, so the fixture is not all one direction — a sign error that
	// only ever sees credits would pass an all-payments fixture.
	res, err := led.PostVoucher(ctx, ledger.VoucherInput{
		Class:         ledger.Receipt,
		VoucherTypeID: receiptVT,
		VoucherNo:     "RCP-1",
		VoucherDate:   "2025-04-15",
		Narration:     "collection",
		CreatedBy:     "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: f.bank, DrCr: ledger.Debit, Amount: dec("400")},
			{LedgerID: f.acme, DrCr: ledger.Credit, Amount: dec("400")},
		},
	})
	if err != nil {
		t.Fatalf("post receipt: %v", err)
	}
	f.vouchers = append(f.vouchers, res.VoucherID)
	f.amounts = append(f.amounts, dec("400"))

	return pool, ctx, companyID, f
}

// TestTheStatementBalancesAndIsVerifiedByAnIndependentSum is I-1.
//
// The report computes bank balance AS books minus un-cleared, so asserting
// "books - uncleared == bank" against the report's own output would be
// tautological — the §3.4 trap of a test that calls the code under test to
// compute its own expectation. So this test re-derives the bank balance a
// DIFFERENT way: by summing the cleared legs from the fixture's own known
// amounts, and comparing.
func TestTheStatementBalancesAndIsVerifiedByAnIndependentSum(t *testing.T) {
	pool, ctx, companyID, f := bankSetup(t)
	bank := banking.NewService(pool)
	rep := reporting.NewService(pool)

	// Clear PAY-1 (1000 out) and RCP-1 (400 in). PAY-2 and PAY-3 stay un-cleared.
	if err := bank.SetBankDate(ctx, companyID, f.vouchers[0], f.bank, 0, "2025-04-07", "tester"); err != nil {
		t.Fatalf("set bank date PAY-1: %v", err)
	}
	if err := bank.SetBankDate(ctx, companyID, f.vouchers[3], f.bank, 0, "2025-04-16", "tester"); err != nil {
		t.Fatalf("set bank date RCP-1: %v", err)
	}

	brs, err := rep.BankReconciliationStatement(ctx, companyID, f.bank, "2025-04-30")
	if err != nil {
		t.Fatalf("BRS: %v", err)
	}
	if !brs.IsBalanced() {
		t.Errorf("difference = %s, want exactly 0", brs.Difference.StringFixed(4))
	}

	// INDEPENDENT re-derivation: a debit-natured bank account's balance per the
	// BANK is the net of everything the bank has actually seen. Cleared: +400
	// (receipt in) and -1000 (PAY-1 out) => -600.
	wantBank := dec("400").Sub(dec("1000"))
	if !brs.BalanceAsPerBank.Equal(wantBank) {
		t.Errorf("balance as per bank = %s, want %s (independently summed from the cleared legs)",
			brs.BalanceAsPerBank.StringFixed(4), wantBank.StringFixed(4))
	}

	// And the books: -1000 -250 -75 +400 = -925.
	wantBooks := dec("-925")
	if !brs.BalanceAsPerBooks.Equal(wantBooks) {
		t.Errorf("balance as per books = %s, want %s",
			brs.BalanceAsPerBooks.StringFixed(4), wantBooks.StringFixed(4))
	}
	if len(brs.Uncleared) != 2 {
		t.Errorf("uncleared count = %d, want 2 (PAY-2 and PAY-3)", len(brs.Uncleared))
	}
	if len(brs.Cleared) != 2 {
		t.Errorf("cleared count = %d, want 2", len(brs.Cleared))
	}
}

// TestAnItemClearedAfterTheAsOnDateIsUnclearedInThatStatement is the AS-AT trap
// (memo §5, Time). It is the easiest bug in this track to write: filtering on
// "is there a reconciliation row" instead of "bank_date <= as_on" makes today's
// statement look right while silently rewriting last month's.
//
// The CONTROL is what makes it bite: the SAME item, in a LATER statement, must
// be cleared. Without that arm, a query that dropped the row entirely would also
// pass the first assertion.
func TestAnItemClearedAfterTheAsOnDateIsUnclearedInThatStatement(t *testing.T) {
	pool, ctx, companyID, f := bankSetup(t)
	bank := banking.NewService(pool)
	rep := reporting.NewService(pool)

	// PAY-1 is dated 05-Apr and clears on 07-MAY — after the April statement.
	if err := bank.SetBankDate(ctx, companyID, f.vouchers[0], f.bank, 0, "2025-05-07", "tester"); err != nil {
		t.Fatalf("set bank date: %v", err)
	}

	april, err := rep.BankReconciliationStatement(ctx, companyID, f.bank, "2025-04-30")
	if err != nil {
		t.Fatalf("april BRS: %v", err)
	}
	for _, ln := range april.Cleared {
		if ln.VoucherID == f.vouchers[0] {
			t.Fatalf("PAY-1 cleared on 2025-05-07 but the 30-Apr statement shows it as cleared; "+
				"the filter must be bank_date <= as_on, not bank_date IS NOT NULL (bank_date=%q)", ln.BankDate)
		}
	}
	foundUncleared := false
	for _, ln := range april.Uncleared {
		if ln.VoucherID == f.vouchers[0] {
			foundUncleared = true
			if ln.BankDate != "" {
				t.Errorf("an item uncleared as at the statement date carries BankDate %q, want empty", ln.BankDate)
			}
		}
	}
	if !foundUncleared {
		t.Errorf("PAY-1 is missing from the April statement entirely; it must appear as UN-CLEARED, not vanish")
	}

	// CONTROL — in May the same item IS cleared. Without this the test would
	// pass against a query that dropped reconciled rows altogether.
	may, err := rep.BankReconciliationStatement(ctx, companyID, f.bank, "2025-05-31")
	if err != nil {
		t.Fatalf("may BRS: %v", err)
	}
	clearedInMay := false
	for _, ln := range may.Cleared {
		if ln.VoucherID == f.vouchers[0] {
			clearedInMay = true
		}
	}
	if !clearedInMay {
		t.Errorf("CONTROL FAILED: PAY-1 is not cleared even in the 31-May statement, so the "+
			"April assertion above proves nothing. cleared=%d uncleared=%d",
			len(may.Cleared), len(may.Uncleared))
	}
}

// TestAlteringAReconciledVoucherBreaksTheReconciliationLoudly is I-4, and it is
// the invariant that makes the whole (voucher_id, ledger_id, leg_no) key safe.
//
// The reconciliation is keyed on the STABLE voucher id, so after an alter it
// still matches a row. If the statement simply believed it, an operator could
// clear a 1000 cheque, alter the voucher to 9999, and the statement would still
// report a clean zero difference against a bank that never saw 9999.
func TestAlteringAReconciledVoucherBreaksTheReconciliationLoudly(t *testing.T) {
	pool, ctx, companyID, f := bankSetup(t)
	bank := banking.NewService(pool)
	rep := reporting.NewService(pool)
	led := ledger.NewService(pool)

	if err := bank.SetBankDate(ctx, companyID, f.vouchers[0], f.bank, 0, "2025-04-07", "tester"); err != nil {
		t.Fatalf("set bank date: %v", err)
	}
	before, err := rep.BankReconciliationStatement(ctx, companyID, f.bank, "2025-04-30")
	if err != nil {
		t.Fatalf("BRS before: %v", err)
	}
	if before.BrokenCount != 0 {
		t.Fatalf("BrokenCount before the alter = %d, want 0", before.BrokenCount)
	}

	// Alter PAY-1 from 1000 to 1500 AFTER it was reconciled.
	ids := identity.NewService(pool)
	paymentVT, err := ids.VoucherTypeID(ctx, "Payment")
	if err != nil {
		t.Fatalf("payment vt: %v", err)
	}
	if _, err := led.AlterVoucher(ctx, f.vouchers[0], ledger.VoucherInput{
		Class:         ledger.Payment,
		VoucherTypeID: paymentVT,
		VoucherNo:     "PAY-1",
		VoucherDate:   "2025-04-05",
		Narration:     "cheque PAY-1 (altered)",
		CreatedBy:     "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: f.rent, DrCr: ledger.Debit, Amount: dec("1500")},
			{LedgerID: f.bank, DrCr: ledger.Credit, Amount: dec("1500")},
		},
	}); err != nil {
		t.Fatalf("alter PAY-1: %v", err)
	}

	after, err := rep.BankReconciliationStatement(ctx, companyID, f.bank, "2025-04-30")
	if err != nil {
		t.Fatalf("BRS after: %v", err)
	}
	if after.BrokenCount != 1 {
		t.Fatalf("BrokenCount after altering a reconciled voucher = %d, want 1 — "+
			"an alter must not silently re-point a reconciliation", after.BrokenCount)
	}
	var broken *reporting.BankReconLine
	for i := range after.Uncleared {
		if after.Uncleared[i].BrokenByAlter {
			broken = &after.Uncleared[i]
		}
	}
	if broken == nil {
		t.Fatalf("the broken item is not listed as un-cleared; it must not be counted as cleared")
	}
	if !broken.ClearedAmount.Equal(dec("1000")) {
		t.Errorf("ClearedAmount = %s, want 1000 — the statement must show WHAT was cleared as evidence",
			broken.ClearedAmount.StringFixed(4))
	}
	if !broken.Amount.Equal(dec("1500")) {
		t.Errorf("current amount = %s, want 1500", broken.Amount.StringFixed(4))
	}
}

// TestADeletedVoucherLeavesTheStatementEntirely is I-5/I-6: the current-version
// join must drop a tombstoned voucher, exactly as movementSQL does. The printed
// Day Book got the sibling case wrong by listing versions.
func TestADeletedVoucherLeavesTheStatementEntirely(t *testing.T) {
	pool, ctx, companyID, f := bankSetup(t)
	rep := reporting.NewService(pool)
	led := ledger.NewService(pool)

	before, err := rep.BankReconciliationStatement(ctx, companyID, f.bank, "2025-04-30")
	if err != nil {
		t.Fatalf("BRS before: %v", err)
	}
	if got := len(before.Uncleared); got != 4 {
		t.Fatalf("uncleared before delete = %d, want 4", got)
	}

	if _, err := led.DeleteVoucher(ctx, f.vouchers[1], "tester"); err != nil {
		t.Fatalf("delete PAY-2: %v", err)
	}

	after, err := rep.BankReconciliationStatement(ctx, companyID, f.bank, "2025-04-30")
	if err != nil {
		t.Fatalf("BRS after: %v", err)
	}
	if got := len(after.Uncleared); got != 3 {
		t.Errorf("uncleared after delete = %d, want 3 — a deleted voucher must contribute nothing", got)
	}
	for _, ln := range after.Uncleared {
		if ln.VoucherID == f.vouchers[1] {
			t.Errorf("the deleted voucher PAY-2 is still on the statement")
		}
	}
	// The books must have moved by the deleted amount, and the statement must
	// still balance — a delete that unbalanced the BRS would be worse than one
	// that left the line behind.
	if !after.IsBalanced() {
		t.Errorf("difference after delete = %s, want 0", after.Difference.StringFixed(4))
	}
	if want := before.BalanceAsPerBooks.Add(dec("250")); !after.BalanceAsPerBooks.Equal(want) {
		t.Errorf("books after delete = %s, want %s",
			after.BalanceAsPerBooks.StringFixed(4), want.StringFixed(4))
	}
}

// TestANonBankLedgerCannotBeReconciled is I-3, from BOTH sides: the report
// refuses to build a statement for a non-bank ledger, and the trigger refuses
// the write. Testing only one side would leave the other reachable — the write
// path is not the only way rows arrive (an import, a by-hand psql fix).
func TestANonBankLedgerCannotBeReconciled(t *testing.T) {
	pool, ctx, companyID, f := bankSetup(t)
	rep := reporting.NewService(pool)
	bank := banking.NewService(pool)

	if _, err := rep.BankReconciliationStatement(ctx, companyID, f.rent, "2025-04-30"); err == nil {
		t.Errorf("a bank reconciliation statement for the Rent ledger succeeded; it must be refused")
	}
	// The write side, refused by the 0045 trigger rather than by Go.
	err := bank.SetBankDate(ctx, companyID, f.vouchers[0], f.rent, 0, "2025-04-07", "tester")
	if err == nil {
		t.Errorf("reconciling a leg on the Rent ledger succeeded; the trigger must refuse it")
	}
}

// TestABankDateBeforeTheVoucherDateIsRefused is I-7 — a cheque cannot clear
// before it is written. Enforced by the 0045 trigger so it holds for every
// writer, not only for banking.SetBankDate.
func TestABankDateBeforeTheVoucherDateIsRefused(t *testing.T) {
	pool, ctx, companyID, f := bankSetup(t)
	bank := banking.NewService(pool)

	// PAY-1 is dated 2025-04-05.
	if err := bank.SetBankDate(ctx, companyID, f.vouchers[0], f.bank, 0, "2025-04-04", "tester"); err == nil {
		t.Errorf("a bank date one day BEFORE the voucher date was accepted; it must be refused")
	}
	// CONTROL: the same date ON the voucher date is legal (a cheque can clear
	// the day it is written). Without this the test would pass against an
	// implementation that refused every bank date.
	if err := bank.SetBankDate(ctx, companyID, f.vouchers[0], f.bank, 0, "2025-04-05", "tester"); err != nil {
		t.Errorf("CONTROL FAILED: a bank date EQUAL to the voucher date was refused: %v", err)
	}
}

// TestReconcilingChangesNoBalanceAndNoVoucher is I-2. Reconciling is an
// assertion about the bank, not about the books: the Trial Balance must be
// byte-identical across it. If this ever fails, the feature has become a way to
// edit the ledger without an audit trail.
func TestReconcilingChangesNoBalanceAndNoVoucher(t *testing.T) {
	pool, ctx, companyID, f := bankSetup(t)
	bank := banking.NewService(pool)
	rep := reporting.NewService(pool)

	before, err := rep.TrialBalance(ctx, companyID, "2025-04-30")
	if err != nil {
		t.Fatalf("TB before: %v", err)
	}
	for i := range f.vouchers {
		if err := bank.SetBankDate(ctx, companyID, f.vouchers[i], f.bank, 0, "2025-04-25", "tester"); err != nil {
			t.Fatalf("set bank date %d: %v", i, err)
		}
	}
	after, err := rep.TrialBalance(ctx, companyID, "2025-04-30")
	if err != nil {
		t.Fatalf("TB after: %v", err)
	}
	if !before.TotalDebit.Equal(after.TotalDebit) || !before.TotalCredit.Equal(after.TotalCredit) {
		t.Errorf("reconciling moved the trial balance: before %s/%s, after %s/%s",
			before.TotalDebit.StringFixed(4), before.TotalCredit.StringFixed(4),
			after.TotalDebit.StringFixed(4), after.TotalCredit.StringFixed(4))
	}
	if !after.IsBalanced() {
		t.Errorf("trial balance no longer balances after reconciling")
	}
}

// TestUnreconcilingRestoresTheStatementExactly pins the round trip, and that
// ClearBankDate on an unreconciled leg is not an error (a double-click must not
// be a support call).
func TestUnreconcilingRestoresTheStatementExactly(t *testing.T) {
	pool, ctx, companyID, f := bankSetup(t)
	bank := banking.NewService(pool)
	rep := reporting.NewService(pool)

	before, err := rep.BankReconciliationStatement(ctx, companyID, f.bank, "2025-04-30")
	if err != nil {
		t.Fatalf("BRS before: %v", err)
	}
	if err := bank.SetBankDate(ctx, companyID, f.vouchers[0], f.bank, 0, "2025-04-07", "tester"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := bank.ClearBankDate(ctx, companyID, f.vouchers[0], f.bank, 0); err != nil {
		t.Fatalf("clear: %v", err)
	}
	after, err := rep.BankReconciliationStatement(ctx, companyID, f.bank, "2025-04-30")
	if err != nil {
		t.Fatalf("BRS after: %v", err)
	}
	if len(before.Uncleared) != len(after.Uncleared) || !before.UnclearedNet.Equal(after.UnclearedNet) {
		t.Errorf("un-reconciling did not restore the statement: uncleared %d->%d, net %s->%s",
			len(before.Uncleared), len(after.Uncleared),
			before.UnclearedNet.StringFixed(4), after.UnclearedNet.StringFixed(4))
	}
	// Idempotent: clearing again is a no-op, not an error.
	if err := bank.ClearBankDate(ctx, companyID, f.vouchers[0], f.bank, 0); err != nil {
		t.Errorf("clearing an already-unreconciled leg returned an error: %v", err)
	}
}

// TestAnUnknownLegIsRefusedByName pins that SetBankDate names its failure rather
// than silently writing a row nothing will ever read.
func TestAnUnknownLegIsRefusedByName(t *testing.T) {
	pool, ctx, companyID, f := bankSetup(t)
	bank := banking.NewService(pool)

	// leg 1 does not exist — each fixture voucher has exactly one bank leg.
	err := bank.SetBankDate(ctx, companyID, f.vouchers[0], f.bank, 1, "2025-04-07", "tester")
	if err == nil {
		t.Fatalf("reconciling a non-existent leg succeeded")
	}
	if !isLegNotFound(err) {
		t.Errorf("error = %v, want ErrLegNotFound so the HTTP layer can answer 404", err)
	}
}

func isLegNotFound(err error) bool {
	for e := err; e != nil; {
		if e == banking.ErrLegNotFound {
			return true
		}
		u, ok := e.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		e = u.Unwrap()
	}
	return false
}

// TestAnUndatedStatementSTATESTheDateItAgedAgainst is the third instance in this
// repository of one defect shape: a report that resolves a default INTERNALLY
// and echoes back the caller's empty parameter.
//
// The order book shipped it first (`As at ·` with nothing after it, HANDOFF
// §1.1); this statement shipped it next. Requested with no as-on, the header
// rendered "As at " followed by nothing, while every AgeDays on the page was
// computed against TODAY — a reconciliation that ages every item against a date
// it refuses to name.
//
// ⛔ IT IS INVISIBLE TO ANY TEST THAT ASSERTS THE SENTENCE IT EXPECTS, because
// the sentence is there and only the date is missing. The order book's own test
// asserted `As at ${book.as_on}` and passed on the empty string. So this asserts
// the FIELD, and asserts it against the same clock the ageing uses.
func TestAnUndatedStatementSTATESTheDateItAgedAgainst(t *testing.T) {
	pool, ctx, companyID, f := bankSetup(t)
	rep := reporting.NewService(pool)

	brs, err := rep.BankReconciliationStatement(ctx, companyID, f.bank, "")
	if err != nil {
		t.Fatalf("unbounded BRS: %v", err)
	}
	if brs.AsOn == "" {
		t.Fatalf("a statement requested with no as-on echoed an EMPTY as_on; it renders " +
			"\"As at \" with nothing after it while ageing every item against today")
	}
	today := time.Now().UTC().Format("2006-01-02")
	if brs.AsOn != today {
		t.Errorf("as_on = %q, want the resolved date %q — the same date AgeDays is measured "+
			"against, or the statement states one date and ages against another", brs.AsOn, today)
	}

	// CONTROL: an EXPLICIT as-on is echoed unchanged. Without this arm the fix
	// would pass by always reporting today, which would be a worse bug.
	april, err := rep.BankReconciliationStatement(ctx, companyID, f.bank, "2025-04-30")
	if err != nil {
		t.Fatalf("april BRS: %v", err)
	}
	if april.AsOn != "2025-04-30" {
		t.Errorf("CONTROL FAILED: an explicit as-on came back as %q, want 2025-04-30", april.AsOn)
	}
}

// TestEveryStatementLineCarriesItsVoucherClass exists because VoucherNo is NOT
// unique on one bank ledger — numbering is per (class, financial year), so a
// real bank account carries several legs all reading "1". The seeded demo shows
// FOUR on HDFC Bank.
//
// Without the class the screen renders four indistinguishable rows and an
// operator ticking one cannot know which voucher they cleared. Found by driving
// the screen (HANDOFF-PARITY-AUDIT §7, row 11), not by any suite: every fixture
// here numbers its vouchers distinguishably, which is precisely why none of them
// could see it.
func TestEveryStatementLineCarriesItsVoucherClass(t *testing.T) {
	pool, ctx, companyID, f := bankSetup(t)
	rep := reporting.NewService(pool)

	brs, err := rep.BankReconciliationStatement(ctx, companyID, f.bank, "2025-12-31")
	if err != nil {
		t.Fatalf("BRS: %v", err)
	}
	lines := append(append([]reporting.BankReconLine{}, brs.Uncleared...), brs.Cleared...)
	if len(lines) == 0 {
		t.Fatal("the fixture produced no statement lines, so this proves nothing")
	}
	for _, ln := range lines {
		if ln.Class == "" {
			t.Errorf("statement line for voucher %q (%s) carries an EMPTY class; the number "+
				"alone does not identify a voucher on a bank ledger", ln.VoucherNo, ln.VoucherDate)
		}
	}
}
