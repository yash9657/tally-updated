package forecast

// forecast_basis_test.go pins the projection's DISCLOSURE — the answer to "how
// much of this number did you make up?".
//
// ⛔⛔ THE DEFECT THIS CLOSES. The cash-flow response carried seven keys — as_on,
// horizon_days, bucket_days, opening_cash, buckets, beyond_horizon,
// overdue_folded_into_first_bucket — and not one of them contained the words
// projection, estimate, basis, assumed or method. Every money value in it is a
// fixed-4 string and reads exactly like a posted ledger figure. Meanwhile the
// engine INVENTS a settlement date for every unpaid bill: the bill's due date if
// it has one, else the party's credit_days, else defaultCreditDays — a 30-day
// convention compiled into forecast.go and stated nowhere on the wire.
//
// ⛔ RULE 4 — these tests assert SIDES and the SOURCE, never a single total. A
// projection in which every inflow was mis-sourced sums to exactly the same
// money, so a total would be a flattering oracle.
//
// PURE: no DB, no clock. Every date below is hand-derivable from the fixture.

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func ptrDay(s string) *time.Time { t := day(s); return &t }
func ptrInt(n int) *int          { return &n }

// bills covers all three settlement-date rules on both sides, plus an overdue
// one, so every field of Basis is exercised by a bill whose expected date can be
// worked out on paper.
func basisBills() []ProjBill {
	return []ProjBill{
		// OBSERVED: an explicit due date, inside the horizon.
		{Kind: Inflow, Pending: d("1000"), DueDate: ptrDay("2025-05-10"), VoucherDate: day("2025-05-01")},
		// ASSUMED (party): no due date, credit_days 15 -> 2025-05-16.
		{Kind: Inflow, Pending: d("500"), VoucherDate: day("2025-05-01"), CreditDays: ptrInt(15)},
		// ⛔ ASSUMED (build convention): no due date, no credit_days -> +30 =
		// 2025-05-31. This is the arm that is pure convention.
		{Kind: Outflow, Pending: d("700"), VoucherDate: day("2025-05-01")},
		// OBSERVED and OVERDUE: due before as_on, folded into the first bucket.
		{Kind: Outflow, Pending: d("250"), DueDate: ptrDay("2025-04-20"), VoucherDate: day("2025-04-01")},
		// Zero pending — skipped by the projector, so counted NOWHERE.
		{Kind: Inflow, Pending: decimal.Zero, DueDate: ptrDay("2025-05-12"), VoucherDate: day("2025-05-01")},
	}
}

func TestBasisReportsWhereEachSettlementDateCameFrom(t *testing.T) {
	p, err := Project(basisBills(), d("0"), day("2025-05-01"), 60, 7)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	b := p.Basis

	if b.FromDueDate.Bills != 2 {
		t.Errorf("from_due_date bills = %d, want 2", b.FromDueDate.Bills)
	}
	if got := b.FromDueDate.Inflow.StringFixed(4); got != "1000.0000" {
		t.Errorf("from_due_date inflow = %s, want 1000.0000", got)
	}
	if got := b.FromDueDate.Outflow.StringFixed(4); got != "250.0000" {
		t.Errorf("from_due_date outflow = %s, want 250.0000", got)
	}

	if b.FromPartyCreditDays.Bills != 1 {
		t.Errorf("from_party_credit_days bills = %d, want 1", b.FromPartyCreditDays.Bills)
	}
	if got := b.FromPartyCreditDays.Inflow.StringFixed(4); got != "500.0000" {
		t.Errorf("from_party_credit_days inflow = %s, want 500.0000", got)
	}

	// ⛔ The one an operator most needs to see: money placed by a constant.
	if b.FromDefaultCreditDays.Bills != 1 {
		t.Errorf("from_default_credit_days bills = %d, want 1", b.FromDefaultCreditDays.Bills)
	}
	if got := b.FromDefaultCreditDays.Outflow.StringFixed(4); got != "700.0000" {
		t.Errorf("from_default_credit_days outflow = %s, want 700.0000", got)
	}
	if b.DefaultCreditDays != defaultCreditDays {
		t.Errorf("default_credit_days = %d, want %d (the published constant)", b.DefaultCreditDays, defaultCreditDays)
	}
	if defaultCreditDays != 30 {
		t.Errorf("defaultCreditDays = %d, want 30 — the convention this disclosure "+
			"exists to name; changing it must be a deliberate act", defaultCreditDays)
	}
}

func TestBasisSideIsNotFoldedIntoOneTotal(t *testing.T) {
	// RULE 4 in one assertion: the receivable and the payable dated from a real
	// due date must not be summed. "1250 rests on an observed date" hides that all
	// of the RECEIVABLE and none of the PAYABLE-by-convention is observed.
	p, _ := Project(basisBills(), d("0"), day("2025-05-01"), 60, 7)
	if p.Basis.FromDueDate.Inflow.Equal(p.Basis.FromDueDate.Outflow) {
		t.Fatalf("inflow and outflow are indistinguishable in the basis (%s / %s)",
			p.Basis.FromDueDate.Inflow, p.Basis.FromDueDate.Outflow)
	}
}

func TestBasisIsDisjointAndExhaustiveOverTheProjectedBills(t *testing.T) {
	// The conservation property: every bill the projection PLACED is accounted for
	// in exactly one source bucket, and a zero-pending bill (skipped by Project) is
	// accounted for in none. Without this, a fourth date rule could quietly place
	// money the disclosure never mentions.
	p, _ := Project(basisBills(), d("0"), day("2025-05-01"), 60, 7)
	b := p.Basis
	gotBills := b.FromDueDate.Bills + b.FromPartyCreditDays.Bills + b.FromDefaultCreditDays.Bills
	if gotBills != 4 {
		t.Fatalf("basis accounts for %d bills, want 4 (the 5th has zero pending and is skipped)", gotBills)
	}

	sumIn := b.FromDueDate.Inflow.Add(b.FromPartyCreditDays.Inflow).Add(b.FromDefaultCreditDays.Inflow)
	sumOut := b.FromDueDate.Outflow.Add(b.FromPartyCreditDays.Outflow).Add(b.FromDefaultCreditDays.Outflow)

	projIn, projOut := decimal.Zero, decimal.Zero
	for _, bk := range p.Buckets {
		projIn = projIn.Add(bk.Inflow)
		projOut = projOut.Add(bk.Outflow)
	}
	projIn = projIn.Add(p.Beyond.Inflow)
	projOut = projOut.Add(p.Beyond.Outflow)

	if !sumIn.Equal(projIn) {
		t.Errorf("basis inflow %s != projected inflow %s — the disclosure does not "+
			"account for all the money the projection placed", sumIn, projIn)
	}
	if !sumOut.Equal(projOut) {
		t.Errorf("basis outflow %s != projected outflow %s", sumOut, projOut)
	}
}

func TestOverdueIsDisclosedInMoneyAndNotOnlyAsABool(t *testing.T) {
	p, _ := Project(basisBills(), d("0"), day("2025-05-01"), 60, 7)
	if !p.OverdueFolded {
		t.Fatal("OverdueFolded = false, want true (a payable was due 2025-04-20)")
	}
	if p.Basis.Overdue.Bills != 1 {
		t.Errorf("overdue bills = %d, want 1", p.Basis.Overdue.Bills)
	}
	if got := p.Basis.Overdue.Outflow.StringFixed(4); got != "250.0000" {
		t.Errorf("overdue outflow = %s, want 250.0000 — a bool cannot tell an "+
			"operator whether 250 or 250000 of the first bucket is already late", got)
	}
}

func TestNothingOverdueDisclosesZeroRatherThanOmitting(t *testing.T) {
	// THE CONTROL. A disclosure that only ever reports non-zero would look
	// identical to a working one on the fixture above, and would silently stop
	// distinguishing "no overdue bills" from "not checked".
	bills := []ProjBill{
		{Kind: Inflow, Pending: d("1000"), DueDate: ptrDay("2025-05-10"), VoucherDate: day("2025-05-01")},
	}
	p, _ := Project(bills, d("0"), day("2025-05-01"), 60, 7)
	if p.OverdueFolded {
		t.Fatal("OverdueFolded = true with no overdue bill")
	}
	if p.Basis.Overdue.Bills != 0 ||
		p.Basis.Overdue.Inflow.StringFixed(4) != "0.0000" ||
		p.Basis.Overdue.Outflow.StringFixed(4) != "0.0000" {
		t.Errorf("overdue basis = %+v, want an explicit zero", p.Basis.Overdue)
	}
	// And the observed arm still reports the bill, so this fixture is not vacuous.
	if p.Basis.FromDueDate.Bills != 1 {
		t.Errorf("from_due_date bills = %d, want 1", p.Basis.FromDueDate.Bills)
	}
}

func TestExpectedDateAndItsSourceCannotDisagree(t *testing.T) {
	// expectedDate() delegates to expectedDateWithSource() precisely so the date
	// and the claim about where it came from are produced by one function. Pin it:
	// a re-implementation of either would be caught here.
	cases := []struct {
		bill ProjBill
		want time.Time
		src  DateSource
	}{
		{ProjBill{DueDate: ptrDay("2025-05-10"), VoucherDate: day("2025-05-01")}, day("2025-05-10"), DateFromDueDate},
		{ProjBill{VoucherDate: day("2025-05-01"), CreditDays: ptrInt(15)}, day("2025-05-16"), DateFromPartyCreditDays},
		{ProjBill{VoucherDate: day("2025-05-01")}, day("2025-05-31"), DateFromDefault},
	}
	for i, c := range cases {
		gotDate, gotSrc := c.bill.expectedDateWithSource()
		if !gotDate.Equal(c.want) {
			t.Errorf("case %d: date = %s, want %s", i, gotDate.Format("2006-01-02"), c.want.Format("2006-01-02"))
		}
		if gotSrc != c.src {
			t.Errorf("case %d: source = %q, want %q", i, gotSrc, c.src)
		}
		if !c.bill.expectedDate().Equal(gotDate) {
			t.Errorf("case %d: expectedDate disagrees with expectedDateWithSource", i)
		}
	}
}
