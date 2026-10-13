package reporting_test

// outstanding_on_account_test.go — ⛔ D106. MONEY THAT NAMES NO BILL WAS INVISIBLE
// TO THE REPORT THAT PROMISES TO EXPLAIN SUNDRY DEBTORS. DRIVEN IN A BROWSER.
//
// ===========================================================================
// WHAT WAS WRONG
// ===========================================================================
// Outstandings was built from REFERENCES — bill_allocation UNION
// ledger_opening_bill — and a party's balance is not obliged to be fully
// referenced. A Receipt for 7,000 entered with an empty bill-wise panel moved
// the ledger and named no bill, so it reached Sundry Debtors and reached this
// report NOWHERE. Driven 2026-08-30 on the demo books: Outstandings
// 318584.0000 against a Balance Sheet Sundry Debtors of 311584.0000.
//
// ⚠️ AND THE REPORT'S OWN CAPTION PROMISED THE TWO AGREE ("…so this agrees with
// Sundry Debtors/Creditors on the Balance Sheet"). Both screens rendered, every
// figure was real, both totals footed, and the operator chases a customer for
// 7,000 they have already paid. It is D86's family: that one was the DATE half
// of coverage, this one is the ALLOCATION half.
//
// ===========================================================================
// WHAT THE FIX IS, AND WHY IT IS AN IDENTITY AND NOT A SPECIAL CASE
// ===========================================================================
//
//	On Account  =  the ledger's whole balance  −  every named reference
//
// so a fully-referenced invoice contributes nothing and an unreferenced receipt
// keeps its whole amount. The report reconciles BY CONSTRUCTION, which is the
// only form of the fix that also covers the cases nobody has driven.
//
// ⚠️ EVERY TEST HERE CARRIES ITS CONTROL, because a residue branch that computed
// zero for everything would pass every assertion about reconciliation.

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/reporting"

	"github.com/shopspring/decimal"
)

// TestAPartyNotMaintainedBillByBillIsReportedOnAccount is the case that is
// reachable through the ordinary write path: a debtor whose master does not ask
// for bill-wise tracking. It has a balance and it has no bills at all.
//
// ⭐ TALLY REPORTS EXACTLY THIS: "when the sales and receipt entries are not
// recorded bill-wise then such entries are tracked as On Account and are
// displayed in the Ledger Outstandings report".
func TestAPartyNotMaintainedBillByBillIsReportedOnAccount(t *testing.T) {
	pool, ctx, companyID, _ := setup(t)
	led := ledger.NewService(pool)
	svc := reporting.NewService(pool)
	ms := masters.NewService(pool)

	// ⛔ NO MaintainBillByBill, so the entry path is entitled to accept a voucher
	// that names no reference — and did.
	plain, err := ms.CreateLedger(ctx, masters.Ledger{
		Name: "Walk-In Wholesale", GroupName: "Sundry Debtors",
	}, "tester")
	if err != nil {
		t.Fatalf("create the un-tracked party: %v", err)
	}
	sales, err := ms.EnsureLedger(ctx, "Bill Sales", "Sales Accounts", "tester")
	if err != nil {
		t.Fatalf("sales ledger: %v", err)
	}
	if _, err := led.PostVoucher(ctx, ledger.VoucherInput{
		Class: ledger.Sales, PartyLedgerID: plain, VoucherNo: "WI-1",
		VoucherDate: "2026-06-10", Party: "walk-in", CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: plain, DrCr: ledger.Debit, Amount: dec("9000")},
			{LedgerID: sales, DrCr: ledger.Credit, Amount: dec("9000")},
		},
	}); err != nil {
		t.Fatalf("post the un-referenced sale: %v", err)
	}

	out, err := svc.Outstandings(ctx, companyID, reporting.Receivables, "", nil)
	if err != nil {
		t.Fatalf("outstandings: %v", err)
	}

	// ⛔ THE ORACLE. The Balance Sheet reads ledger_balance (postings); this report
	// reads references. Agreement is a genuine cross-check of two paths.
	if bs := debtorsFromBalanceSheet(t, svc, ctx, companyID); !bs.Equal(out.Total) {
		t.Fatalf("Balance Sheet says Sundry Debtors %s and Outstandings says %s — "+
			"two reports over one set of books, and the caption on this one says they agree",
			bs, out.Total)
	}
	if want := dec("9000"); !out.Total.Equal(want) {
		t.Fatalf("Outstandings total = %s, want %s", out.Total, want)
	}

	row := onlyBill(t, out, plain)
	if row.RefName != reporting.OnAccountRefName {
		t.Errorf("the un-referenced 9,000 is reported as a bill named %q — the report "+
			"invented a bill for a voucher that named none", row.RefName)
	}
	if !row.OnAccount {
		t.Errorf("the row is not flagged OnAccount: %+v", row)
	}
	// ⛔ UN-AGED. An amount with no bill has no due date to be overdue against, and
	// ageBill would read an empty due date as "not overdue" and file it in the
	// FIRST slab — reporting money that is not a bill as the freshest debt there is.
	if row.SlabLabel != "" {
		t.Errorf("the On Account row was aged into slab %q", row.SlabLabel)
	}
	if row.OverdueDays != 0 {
		t.Errorf("the On Account row claims to be %d days overdue", row.OverdueDays)
	}
	assertSlabsPlusOnAccountReconcile(t, out)
}

// TestALegacyUnreferencedPostingStillReconciles is the 7,000 receipt itself: a
// bill-by-bill party carrying a posting that names no reference.
//
// ⛔⛔ THE WRITE PATH NO LONGER ALLOWS THIS VOUCHER, AND THAT IS THE POINT OF THE
// PAIR. The other half of D106 refuses it at entry (the control at the end), so
// the only books that can contain one are books written BEFORE that rule — the
// demo company, every migrated book, every import, and every party somebody
// switches to bill-wise after the fact, which is how this fixture builds it.
//
// ⛔ AND THEY CANNOT BE REPAIRED IN PLACE. bill_allocation rows are inside the
// voucher's canonical JSON and therefore inside its HASH, and the chain is a
// one-way door: writing the missing reference into an existing version would
// change its bytes. So the report has to reconcile them where they lie, and this
// test is what says so.
func TestALegacyUnreferencedPostingStillReconciles(t *testing.T) {
	pool, ctx, companyID, _ := setup(t)
	led := ledger.NewService(pool)
	svc := reporting.NewService(pool)
	ms := masters.NewService(pool)

	// ⭐ THE BOOK IS WRITTEN THE WAY A REAL ONE ACQUIRED THIS SHAPE: the party was
	// NOT tracked bill-by-bill when the vouchers were keyed, so an unreferenced
	// receipt was legal — and then somebody switched the master on. That is the
	// same state a migrated or imported book arrives in, and the same state the
	// demo company was found in, without needing to forge a hash-chained row.
	party, err := ms.CreateLedger(ctx, masters.Ledger{
		Name: "Switched To Bill-Wise", GroupName: "Sundry Debtors",
	}, "tester")
	if err != nil {
		t.Fatalf("create party: %v", err)
	}
	sales, err := ms.EnsureLedger(ctx, "Bill Sales", "Sales Accounts", "tester")
	if err != nil {
		t.Fatalf("sales ledger: %v", err)
	}

	billSale(t, led, ctx, party, sales, "INV-9", "2026-05-01", "INV-9", "20000.0000")

	// The receipt that names nothing — legal at the time it was keyed.
	if _, err := led.PostVoucher(ctx, ledger.VoucherInput{
		Class: ledger.Receipt, PartyLedgerID: party, VoucherNo: "RCP-NOREF",
		VoucherDate: "2026-05-20", Party: "party", CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: sales, DrCr: ledger.Debit, Amount: dec("7000")},
			{LedgerID: party, DrCr: ledger.Credit, Amount: dec("7000")},
		},
	}); err != nil {
		t.Fatalf("post the un-referenced receipt: %v", err)
	}

	setBillByBill(t, pool, ctx, party)

	out, err := svc.Outstandings(ctx, companyID, reporting.Receivables, "", nil)
	if err != nil {
		t.Fatalf("outstandings: %v", err)
	}
	if bs := debtorsFromBalanceSheet(t, svc, ctx, companyID); !bs.Equal(out.Total) {
		t.Fatalf("Balance Sheet Sundry Debtors %s, Outstandings %s — the exact shape of "+
			"D106: %s of receipt the ageing report cannot see", bs, out.Total,
			bs.Sub(out.Total).Abs())
	}
	if want := dec("13000"); !out.Total.Equal(want) {
		t.Fatalf("Outstandings total = %s, want %s (20,000 invoiced less 7,000 received)",
			out.Total, want)
	}
	if want := dec("-7000"); !out.OnAccountTotal.Equal(want) {
		t.Errorf("OnAccountTotal = %s, want %s", out.OnAccountTotal, want)
	}
	assertSlabsPlusOnAccountReconcile(t, out)

	// ⛔ AND THE INVOICE IS UNTOUCHED. The residue must not net itself into the
	// bill: the party owes 20,000 on INV-9 and separately holds 7,000 applied to
	// nothing. Netting them would destroy the distinction the row exists to draw,
	// and the total would still be right.
	var invoice, onAcct bool
	for _, p := range out.Parties {
		if p.LedgerID != party {
			continue
		}
		for _, b := range p.Bills {
			switch b.RefName {
			case "INV-9":
				invoice = true
				if !b.Pending.Equal(dec("20000")) {
					t.Errorf("INV-9 pending = %s, want 20000 — the on-account money was "+
						"netted into a bill it was never applied to", b.Pending)
				}
				if b.SlabLabel == "" {
					t.Errorf("INV-9 is a real bill and must still be aged")
				}
			case reporting.OnAccountRefName:
				onAcct = true
			}
		}
	}
	if !invoice || !onAcct {
		t.Fatalf("want BOTH the aged invoice and the On Account row, got invoice=%v onAccount=%v",
			invoice, onAcct)
	}

	// ── THE CONTROL: the same voucher can no longer be keyed. If this ever starts
	// succeeding the hole is open again, and the residue above would quietly paper
	// over every new one.
	_, err = led.PostVoucher(ctx, ledger.VoucherInput{
		Class: ledger.Receipt, PartyLedgerID: party, VoucherNo: "RCP-NOREF-2",
		VoucherDate: "2026-05-21", Party: "party", CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: sales, DrCr: ledger.Debit, Amount: dec("100")},
			{LedgerID: party, DrCr: ledger.Credit, Amount: dec("100")},
		},
	})
	if err == nil {
		t.Fatal("a bill-by-bill party accepted a voucher naming no reference — this is " +
			"the entry that produced D106, and the report should not have to clean up " +
			"after it")
	}
	if !strings.Contains(err.Error(), "ON_ACCOUNT") {
		t.Errorf("the refusal does not say what to record instead: %v", err)
	}
}

// TestAFullyReferencedBookHasNoOnAccountRow is the control on the residue itself.
// The identity must compute ZERO for an ordinary book — otherwise every party
// grows a spurious row and the totals are wrong in the other direction.
func TestAFullyReferencedBookHasNoOnAccountRow(t *testing.T) {
	pool, ctx, companyID, _ := setup(t)
	led := ledger.NewService(pool)
	svc := reporting.NewService(pool)
	party, sales := billParties(t, pool, ctx)

	billSale(t, led, ctx, party, sales, "A-1", "2026-04-10", "A-1", "10000.0000")
	billSale(t, led, ctx, party, sales, "A-2", "2026-05-11", "A-2", "5000.0000")

	out, err := svc.Outstandings(ctx, companyID, reporting.Receivables, "", nil)
	if err != nil {
		t.Fatalf("outstandings: %v", err)
	}
	if !out.OnAccountTotal.IsZero() {
		t.Errorf("a fully referenced book reports %s on account — the residue is "+
			"inventing money", out.OnAccountTotal)
	}
	for _, p := range out.Parties {
		for _, b := range p.Bills {
			if b.OnAccount {
				t.Errorf("a fully referenced book grew an On Account row: %+v", b)
			}
		}
	}
	if want := dec("15000"); !out.Total.Equal(want) {
		t.Fatalf("Outstandings total = %s, want %s", out.Total, want)
	}
	if bs := debtorsFromBalanceSheet(t, svc, ctx, companyID); !bs.Equal(out.Total) {
		t.Fatalf("Balance Sheet %s != Outstandings %s", bs, out.Total)
	}
	assertSlabsPlusOnAccountReconcile(t, out)
}

// TestTheOnAccountRowIsNotOfferedAsASettleableBill guards the settle picker.
//
// ⛔ THE PICKER'S ROWS BECOME `AGAINST` ALLOCATIONS, AND AN AGAINST NAMES THE
// REFERENCE IT SETTLES. `On Account` is not a stored reference — it is the
// COMPUTED remainder — so offering it would invite an allocation naming a bill
// that does not exist, and the arithmetic would still foot.
func TestTheOnAccountRowIsNotOfferedAsASettleableBill(t *testing.T) {
	pool, ctx, companyID, _ := setup(t)
	svc := reporting.NewService(pool)
	ms := masters.NewService(pool)

	plain, err := ms.CreateLedger(ctx, masters.Ledger{
		Name: "Untracked Debtor", GroupName: "Sundry Debtors",
		OpeningBalance: dec("5000"), OpeningDC: "D",
	}, "tester")
	if err != nil {
		t.Fatalf("create party: %v", err)
	}
	out, err := svc.Outstandings(ctx, companyID, reporting.Receivables, "", nil)
	if err != nil {
		t.Fatalf("outstandings: %v", err)
	}
	row := onlyBill(t, out, plain)
	if !row.OnAccount {
		t.Fatalf("the opening balance with no breakup is not reported on account: %+v", row)
	}
	// The server drops OnAccount rows from /open-bills; this asserts the FLAG the
	// handler filters on is actually set, which is the part a reporting-level test
	// can own. The handler's own behaviour is covered in internal/server.
	if row.RefName != reporting.OnAccountRefName {
		t.Fatalf("ref name %q", row.RefName)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// setBillByBill switches a party's master to bill-wise tracking AFTER its
// vouchers exist. It is a master edit, not a forgery: nothing about the vouchers
// changes, which is exactly why the report has to cope.
func setBillByBill(t *testing.T, pool *db.Pool, ctx context.Context, ledgerID uuid.UUID) {
	t.Helper()
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE ledger SET maintain_bill_by_bill = true WHERE id = $1`, ledgerID)
		return err
	}); err != nil {
		t.Fatalf("switch %s to bill-by-bill: %v", ledgerID, err)
	}
}

// onlyBill returns the single row reported for a party, failing if there is not
// exactly one — "exactly one" is itself part of what these tests assert.
func onlyBill(t *testing.T, out reporting.Outstanding, ledgerID uuid.UUID) reporting.Bill {
	t.Helper()
	for _, p := range out.Parties {
		if p.LedgerID != ledgerID {
			continue
		}
		if len(p.Bills) != 1 {
			t.Fatalf("want exactly one row for %s, got %+v", p.LedgerName, p.Bills)
		}
		return p.Bills[0]
	}
	t.Fatalf("party %s is absent from the report entirely", ledgerID)
	return reporting.Bill{}
}

// assertSlabsPlusOnAccountReconcile pins the report's own reconciliation
// identity.
//
// ⛔ THE SLABS DO NOT SUM TO THE TOTAL AND THEY ARE NOT SUPPOSED TO. On-account
// money is in no bucket, so the column of slabs beside a larger total is exactly
// the shape a reader checks by adding up — and `OnAccountTotal` is the published
// difference that makes the check come out. If this identity ever fails, the
// report has a number in it that nothing explains.
func assertSlabsPlusOnAccountReconcile(t *testing.T, out reporting.Outstanding) {
	t.Helper()
	sum := decimal.Zero
	for _, s := range out.SlabTotals {
		sum = sum.Add(s.Amount)
	}
	if got := sum.Add(out.OnAccountTotal); !got.Equal(out.Total) {
		t.Errorf("sum(slabs)=%s + onAccount=%s = %s, but total=%s — the report does not "+
			"reconcile to itself", sum, out.OnAccountTotal, got, out.Total)
	}
}
