package reporting_test

// outstanding_as_on_test.go — ⛔ THE AGEING REPORT'S `as_on` DID NOT DECIDE WHAT
// IS IN IT. DRIVEN IN A BROWSER.
//
// ===========================================================================
// WHAT WAS WRONG, AND WHY IT IS WORSE THAN A WRONG NUMBER
// ===========================================================================
// Outstandings honoured `as_on` for AGEING — every bill was bucketed against it —
// and NOT for INCLUSION. The reference sub-select carried no date predicate at
// all, so bills raised AFTER the as-on date were reported under a caption
// asserting the two dates agreed, and settlements made after it had already been
// applied. Driven: os-total 2,65,720.00 at as-on 2026-04-30 against a Balance
// Sheet Sundry Debtors of 1,79,000.00.
//
// A wrong total that announces itself is a bug. THIS one produced two reports
// over one set of books which disagree by 86,720.00, each internally consistent,
// each footing, with nothing anywhere to reconcile them — and the operator using
// it is chasing customers for invoices that did not exist on the date they are
// asking about.
//
// AND THE OPENING BREAKUP HAD THE SAME HOLE FROM THE OTHER END. It carries no
// date column: it is stated as at company.books_beginning_from, exactly as
// ledger.opening_balance is. queryBalances guards that for the Trial Balance and
// the Balance Sheet; nothing guarded it here, so an ageing report dated before
// the books began reported the ENTIRE opening ageing — a position that did not
// exist, on a date the company did not have.
//
// ⚠️ EVERY TEST HERE CARRIES ITS CONTROL, because "exclude everything" passes
// every assertion about exclusion.

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/reporting"

	"github.com/shopspring/decimal"
)

// debtorsAsAt is the INDEPENDENT ORACLE, dated. It reads the Balance Sheet's
// Sundry Debtors — computed from ledger_balance, i.e. from postings — so
// agreement with Outstandings, which is computed from bill_allocation, is a
// genuine cross-check of two paths rather than one query reformatted.
func debtorsAsAt(t *testing.T, svc *reporting.Service, ctx context.Context,
	companyID uuid.UUID, asOn string,
) decimal.Decimal {
	t.Helper()
	bs, err := svc.BalanceSheet(ctx, companyID, asOn)
	if err != nil {
		t.Fatalf("balance sheet as at %s: %v", asOn, err)
	}
	var walk func(nodes []reporting.StatementNode) decimal.Decimal
	walk = func(nodes []reporting.StatementNode) decimal.Decimal {
		for _, n := range nodes {
			if n.Name == "Sundry Debtors" {
				return n.Amount
			}
			if v := walk(n.Children); !v.IsZero() {
				return v
			}
		}
		return decimal.Zero
	}
	return walk(bs.Assets.Nodes)
}

// TestOutstandingsExcludesBillsRaisedAfterTheAsOnDate is the driven defect, with
// the Balance Sheet as the oracle at BOTH dates.
func TestOutstandingsExcludesBillsRaisedAfterTheAsOnDate(t *testing.T) {
	pool, ctx, companyID, _ := setup(t)
	led := ledger.NewService(pool)
	svc := reporting.NewService(pool)
	party, sales := billParties(t, pool, ctx)

	// April's invoice, then May's. Only the first exists on 30 April.
	billSale(t, led, ctx, party, sales, "APR-1", "2026-04-10", "APR-1", "10000.0000")
	billSale(t, led, ctx, party, sales, "MAY-1", "2026-05-20", "MAY-1", "5000.0000")

	as30Apr, err := svc.Outstandings(ctx, companyID, reporting.Receivables, "2026-04-30", nil)
	if err != nil {
		t.Fatalf("outstandings as at 2026-04-30: %v", err)
	}
	if want := dec("10000"); !as30Apr.Total.Equal(want) {
		t.Errorf("Outstandings as at 2026-04-30 = %s, want %s — the May invoice is being reported "+
			"under a caption that says the report is as at 30 April, so the operator is chasing a "+
			"debt that did not exist on the date they asked about", as30Apr.Total, want)
	}
	for _, p := range as30Apr.Parties {
		for _, b := range p.Bills {
			if b.RefName == "MAY-1" {
				t.Errorf("bill MAY-1 (raised 2026-05-20) appears in the report as at 2026-04-30")
			}
		}
	}
	// ⛔ THE ORACLE. Two reports over one set of books, at one date.
	if bs := debtorsAsAt(t, svc, ctx, companyID, "2026-04-30"); !bs.Equal(as30Apr.Total) {
		t.Errorf("as at 2026-04-30 the Balance Sheet says Sundry Debtors %s and Outstandings says %s — "+
			"two reports over the same books, each footing, with nothing to reconcile them", bs, as30Apr.Total)
	}

	// ── THE CONTROL ────────────────────────────────────────────────────────────
	// A fix that excluded everything would satisfy every assertion above.
	as31May, err := svc.Outstandings(ctx, companyID, reporting.Receivables, "2026-05-31", nil)
	if err != nil {
		t.Fatalf("outstandings as at 2026-05-31: %v", err)
	}
	if want := dec("15000"); !as31May.Total.Equal(want) {
		t.Fatalf("CONTROL: Outstandings as at 2026-05-31 = %s, want %s — the date predicate has eaten "+
			"the report", as31May.Total, want)
	}
	if bs := debtorsAsAt(t, svc, ctx, companyID, "2026-05-31"); !bs.Equal(as31May.Total) {
		t.Errorf("as at 2026-05-31 the Balance Sheet says %s and Outstandings says %s", bs, as31May.Total)
	}
}

// TestASettlementAfterTheAsOnDateHasNotHappenedYet is the other half of the same
// predicate, and it is the half that flatters: a report that applied later
// receipts would UNDERSTATE what was owed on the date asked about, which looks
// like good news and is the direction nobody checks.
func TestASettlementAfterTheAsOnDateHasNotHappenedYet(t *testing.T) {
	pool, ctx, companyID, _ := setup(t)
	led := ledger.NewService(pool)
	svc := reporting.NewService(pool)
	party, sales := billParties(t, pool, ctx)
	bank := ensureBank(t, pool, ctx)

	billSale(t, led, ctx, party, sales, "APR-9", "2026-04-10", "APR-9", "10000.0000")
	// Paid in May. On 30 April the customer still owed the whole amount.
	if _, err := led.PostVoucher(ctx,
		receiptAgainst(party, bank, "APR-9", "2026-05-25", "4000.0000")); err != nil {
		t.Fatalf("post receipt: %v", err)
	}

	as30Apr, err := svc.Outstandings(ctx, companyID, reporting.Receivables, "2026-04-30", nil)
	if err != nil {
		t.Fatalf("outstandings as at 2026-04-30: %v", err)
	}
	if want := dec("10000"); !as30Apr.Total.Equal(want) {
		t.Errorf("Outstandings as at 2026-04-30 = %s, want %s — a receipt dated 25 May has been "+
			"applied to a position asked for as at 30 April, so the report understates what was owed "+
			"and the understatement is invisible", as30Apr.Total, want)
	}
	if bs := debtorsAsAt(t, svc, ctx, companyID, "2026-04-30"); !bs.Equal(as30Apr.Total) {
		t.Errorf("as at 2026-04-30 the Balance Sheet says %s and Outstandings says %s", bs, as30Apr.Total)
	}

	// ── THE CONTROL: the receipt DOES settle, on a date after it. ─────────────
	as31May, err := svc.Outstandings(ctx, companyID, reporting.Receivables, "2026-05-31", nil)
	if err != nil {
		t.Fatalf("outstandings as at 2026-05-31: %v", err)
	}
	if want := dec("6000"); !as31May.Total.Equal(want) {
		t.Fatalf("CONTROL: Outstandings as at 2026-05-31 = %s, want %s — settlements have stopped "+
			"working altogether", as31May.Total, want)
	}
}

// TestTheOpeningBreakupIsNotReportedBeforeTheBooksBegan is queryBalances' guard,
// applied to the other place an opening position hides. The company created by
// setup() has fy_start_date 2025-04-01 and no explicit books_beginning_from, so
// COALESCE puts the opening at 2025-04-01 — the same date the Trial Balance and
// the Balance Sheet use for it.
func TestTheOpeningBreakupIsNotReportedBeforeTheBooksBegan(t *testing.T) {
	pool, ctx, companyID, _ := setup(t)
	svc := reporting.NewService(pool)
	ms := masters.NewService(pool)

	if _, err := ms.CreateLedger(ctx, masters.Ledger{
		Name: "Carried In Before Time", GroupName: "Sundry Debtors",
		OpeningBalance: dec("120000"), OpeningDC: "D",
		MaintainBillByBill: true,
		OpeningBills: []masters.OpeningBill{
			{RefName: "OB/A", Amount: dec("70000"), DueDate: "2026-04-20"},
			{RefName: "OB/B", Amount: dec("50000"), DueDate: "2026-05-05"},
		},
	}, "tester"); err != nil {
		t.Fatalf("create party with opening: %v", err)
	}

	before, err := svc.Outstandings(ctx, companyID, reporting.Receivables, "2024-12-31", nil)
	if err != nil {
		t.Fatalf("outstandings as at 2024-12-31: %v", err)
	}
	if !before.Total.IsZero() {
		t.Errorf("Outstandings as at 2024-12-31 = %s, want 0 — the books begin 2025-04-01 and an "+
			"opening breakup carries no date of its own, so reporting it here states a position for a "+
			"date the company did not have. The Balance Sheet already refuses to do this "+
			"(queryBalances' foldOpening guard); the two must not disagree", before.Total)
	}
	// ⛔ THE ORACLE, at the same date: the Balance Sheet excludes the opening too.
	if bs := debtorsAsAt(t, svc, ctx, companyID, "2024-12-31"); !bs.Equal(before.Total) {
		t.Errorf("as at 2024-12-31 the Balance Sheet says Sundry Debtors %s and Outstandings says %s",
			bs, before.Total)
	}

	// ── THE CONTROL ────────────────────────────────────────────────────────────
	// After the books begin, the whole opening ageing is there — this is the
	// behaviour TestOutstandingsIncludesOpeningBills exists to protect, and a
	// guard that swallowed it would make the report short by exactly the opening
	// balance while both reports still looked internally consistent.
	after, err := svc.Outstandings(ctx, companyID, reporting.Receivables, "2026-07-26", nil)
	if err != nil {
		t.Fatalf("outstandings as at 2026-07-26: %v", err)
	}
	if want := dec("120000"); !after.Total.Equal(want) {
		t.Fatalf("CONTROL: Outstandings as at 2026-07-26 = %s, want %s — the guard has swallowed the "+
			"opening breakup on every date", after.Total, want)
	}
	if bs := debtorsAsAt(t, svc, ctx, companyID, "2026-07-26"); !bs.Equal(after.Total) {
		t.Errorf("as at 2026-07-26 the Balance Sheet says %s and Outstandings says %s", bs, after.Total)
	}
}

// TestOutstandingsWithNoAsOnAGREESWithTheBalanceSheet is D86, and it is the other
// end of the same predicate the three tests above pin.
//
// ⛔⛔ THE DEFECT WAS THE **DEFAULT**, NOT THE PREDICATE. Everything above asks for a
// DATE and is right to be bounded by it. Asked for NOTHING, `resolveAsOn` handed the
// predicate today's date — so the bill-wise breakup covered less than the ledger
// balance it is a breakup OF the moment a bill was dated ahead, and the two reports
// disagreed by exactly those bills with nothing on either to reconcile them. Driven:
// Balance Sheet 109950.0000 against Outstandings 108770.0000.
//
// ⛔ AND THE DECISION WAS THAT **OUTSTANDINGS** MOVES. The Balance Sheet is
// deliberately NOT bounded at today — a ledger balance includes every posted voucher
// whatever its date — so `verify:demo` needs no re-pin and the sheet's own figure is
// byte-identical to what it was.
//
// ⚠️ THIS PACKAGE HAD NO TEST ON THE UNBOUNDED DEFAULT AT ALL, WHICH IS WHY THE
// DEFECT LIVED HERE. Making the change and running `go test ./internal/reporting/`
// was entirely GREEN; the only three failures anywhere were in `internal/query`. So
// every assertion in this file was about a date that had been ASKED for, and the
// answer nobody asked for was the one that was wrong.
func TestOutstandingsWithNoAsOnAGREESWithTheBalanceSheet(t *testing.T) {
	pool, ctx, companyID, _ := setup(t)
	led := ledger.NewService(pool)
	svc := reporting.NewService(pool)
	party, sales := billParties(t, pool, ctx)

	// One ordinary invoice and one dated well AHEAD — a proforma keyed early, a
	// scheduled billing run, an advance invoice. It is posted, so the ledger
	// balance carries it.
	billSale(t, led, ctx, party, sales, "NOW-1", "2026-05-10", "NOW-1", "10000.0000")
	billSale(t, led, ctx, party, sales, "FUT-1", "2027-01-15", "FUT-1", "4000.0000")

	// ⛔ THE ORACLE, AND NEITHER CALL NAMES A DATE. `debtorsAsAt` reads the Balance
	// Sheet, which is computed from ledger_balance (postings); Outstandings is
	// computed from bill_allocation. Two paths, one answer.
	unbounded, err := svc.Outstandings(ctx, companyID, reporting.Receivables, "", nil)
	if err != nil {
		t.Fatalf("outstandings with no as_on: %v", err)
	}
	if bs := debtorsAsAt(t, svc, ctx, companyID, ""); !bs.Equal(unbounded.Total) {
		t.Errorf("with NO as_on the Balance Sheet says Sundry Debtors %s and Outstandings says %s — "+
			"a bill-wise breakup that explains less than the balance it breaks up (short by %s)",
			bs, unbounded.Total, bs.Sub(unbounded.Total))
	}
	if want := dec("14000"); !unbounded.Total.Equal(want) {
		t.Errorf("Outstandings with no as_on = %s, want %s", unbounded.Total, want)
	}
	// It NAMES the row, not just the total: two errors cancelling would satisfy an
	// arithmetic-only check.
	refs := map[string]bool{}
	for _, p := range unbounded.Parties {
		for _, b := range p.Bills {
			refs[b.RefName] = true
		}
	}
	if !refs["FUT-1"] {
		t.Errorf("the future-dated bill FUT-1 is absent from an unbounded report; refs=%v", refs)
	}
	if !refs["NOW-1"] {
		t.Errorf("NOW-1 is absent too — the report is not simply unbounded, it is empty; refs=%v", refs)
	}

	// ⛔ AND THE REPORT SAYS WHICH QUESTION IT ANSWERED. `AsOn` is the AGEING
	// reference and is always populated; `Bounded` is the claim about the ROWS. One
	// field could not say both, and every caption downstream reads the wrong one if
	// it tries.
	if unbounded.Bounded {
		t.Errorf("Bounded=true for an empty as_on — every caption downstream will claim a date " +
			"limit these rows do not have, which is the same class of defect as the wrong number")
	}
	if unbounded.AsOn == "" {
		t.Errorf("AsOn is empty — the bills are still aged against a day, and dropping that " +
			"trades a false caption for a missing one")
	}

	// ── THE CONTROL, AND IT IS THE WHOLE POINT ────────────────────────────────
	// "Remove the bound" would satisfy every assertion above. An EXPLICIT as_on must
	// still bound, exactly as the three tests above require, and must still say so.
	as31May, err := svc.Outstandings(ctx, companyID, reporting.Receivables, "2026-05-31", nil)
	if err != nil {
		t.Fatalf("outstandings as at 2026-05-31: %v", err)
	}
	if want := dec("10000"); !as31May.Total.Equal(want) {
		t.Fatalf("CONTROL: as at 2026-05-31 = %s, want %s — the bound was REMOVED rather than "+
			"made conditional, and the report can no longer answer 'as at the quarter end'",
			as31May.Total, want)
	}
	if !as31May.Bounded {
		t.Errorf("CONTROL: Bounded=false for an explicit as_on")
	}
	if bs := debtorsAsAt(t, svc, ctx, companyID, "2026-05-31"); !bs.Equal(as31May.Total) {
		t.Errorf("as at 2026-05-31 the Balance Sheet says %s and Outstandings says %s", bs, as31May.Total)
	}
}
