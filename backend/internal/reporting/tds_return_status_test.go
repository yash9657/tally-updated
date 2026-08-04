package reporting_test

// THE RETURN-FILE STATUS, AND THE ONE PROPERTY THE OLD VERSION COULD NOT HAVE.
//
// ReturnFileRefusal() was niladic: same three blockers for every company, read
// from no database. It could not be wrong about a specific company because it
// never mentioned one — and that is exactly why it went stale twice, asserting
// "There is no tds_challan table" after 0069 shipped it and "This company record
// holds none of them" to companies that had filled the form in.
//
// So the tests here are not about wording. They are about the status CHANGING as
// the books change, which is the only thing that makes it un-stale-able:
//
//	no profile              -> the profile blocker leads and says it hides the rest
//	profile, no challan     -> the blocker NAMES THE VOUCHER, the party and the tax
//	profile + challan       -> the books are declared ready, and the last remaining
//	                           blocker is explicitly OURS rather than the operator's
//	voucher altered after   -> the discrepancy is surfaced, not silently used
//
// Every one of these is DB-backed, because the whole point is the read.

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/reporting"
)

// newReturnFixtureWithoutProfile is the ordinary fixture with migration 0070's
// row removed again.
//
// Built by DELETING rather than by skipping seedDeductorProfile, because the
// fixture's other seed (0049's company_deductor_status) is what lets a TDS
// voucher post at all — without it the deduct() helper is refused and the test
// would prove nothing about the profile.
func newReturnFixtureWithoutProfile(t *testing.T) *returnFixture {
	t.Helper()
	f := newReturnFixture(t)
	adminExec(t, `DELETE FROM company_tds_profile WHERE company_id = $1`, f.companyID)
	return f
}

// reduceDeduction alters the voucher downwards, which re-derives its deduction
// with a smaller tax and leaves the earlier allocation pointing at a figure that
// no longer exists. That is what an ALTER does to a paid deduction —
// internal/ledger/tds.go deletes and re-derives the row — and it is the only way
// to produce the broken-by-alter state honestly.
func (f *returnFixture) reduceDeduction(t *testing.T, voucherID uuid.UUID,
	section, base, tax string) {
	t.Helper()
	purchaseVT, err := f.ids.VoucherTypeID(f.ctx, "Purchase")
	if err != nil {
		t.Fatal(err)
	}
	baseD, taxD := d4(base), d4(tax)
	if _, err := f.led.AlterVoucher(f.ctx, voucherID, ledger.VoucherInput{
		Class: ledger.Purchase, VoucherTypeID: purchaseVT,
		VoucherNo: "P1", VoucherDate: "2026-05-10",
		CreatedBy: "test", PartyLedgerID: f.consultant,
		Postings: []ledger.PostingInput{
			{LedgerID: f.expense, DrCr: ledger.Debit, Amount: baseD},
			{LedgerID: f.consultant, DrCr: ledger.Credit, Amount: baseD.Sub(taxD)},
			{LedgerID: f.tdsPayable, DrCr: ledger.Credit, Amount: taxD},
		},
		TDS: &ledger.TDSParams{
			Section: section, DeducteeLedgerID: f.consultant,
			DeducteeType: "OTHER", PANAvailable: true,
			TaxableAmount: baseD, TDSLedgerID: f.tdsPayable,
		},
	}); err != nil {
		t.Fatalf("alter: %v", err)
	}
}

// statusFor is the register's own period for Q1 2026-27, the same range the
// screen asks for.
func statusFor(t *testing.T, f *returnFixture) reporting.ReturnFileStatus {
	t.Helper()
	svc := reporting.NewService(f.pool)
	st, err := svc.ReturnFileStatusFor(f.ctx, f.companyID, "2026-04-01", "2026-06-30")
	if err != nil {
		t.Fatalf("ReturnFileStatusFor: %v", err)
	}
	return st
}

// blockerText flattens the blockers so an assertion can ask whether a FACT is
// present anywhere in the panel, rather than pinning which field carries it.
func blockerText(st reporting.ReturnFileStatus) string {
	var b strings.Builder
	for _, bl := range st.Blockers {
		b.WriteString(" " + bl.What + " " + bl.Where + " " + bl.Why)
	}
	return b.String()
}

// TestTheReturnFilePanelNamesTheVoucherThatIsBlockingIt is the headline change.
//
// Before this, an operator was told "nothing here records the challans you have
// deposited" — a sentence about the software, identical for everyone. Now they
// are told which voucher, which party, which amount and which section.
func TestTheReturnFilePanelNamesTheVoucherThatIsBlockingIt(t *testing.T) {
	f := newReturnFixture(t) // includes the deductor profile
	f.setPAN(t, f.consultant, "ABCPK9012Q")
	// ₹2,00,000 of professional fees at 10% = ₹20,000 deducted, NOT deposited.
	f.deduct(t, "P1", "2026-05-10", "194J-P", f.consultant, "200000.0000", "20000.0000")

	st := statusFor(t, f)
	if st.Available {
		t.Fatal("available must be false while a deduction has no challan behind it")
	}
	all := blockerText(st)
	// The four facts an operator needs in order to act, none of which the niladic
	// version could have carried.
	for _, want := range []string{"P1", "Chitra Consulting", "20000", "194J-P"} {
		if !strings.Contains(all, want) {
			t.Errorf("the blocker must name %q so the operator knows WHICH deduction to "+
				"deposit; got %q", want, all)
		}
	}
	// And it must not have reverted to a claim about the software.
	for _, gone := range []string{
		"Nothing in this software records any of it today",
		"This company record holds none of them",
		"There is no tds_challan table",
	} {
		if strings.Contains(all, gone) {
			t.Errorf("a retired software-fact claim is back: %q", gone)
		}
	}
	if !strings.Contains(st.Summary, "1 deduction has no challan") {
		t.Errorf("the summary must count the work outstanding; got %q", st.Summary)
	}
	// AND THE COUNT MUST BE IN THE BLOCKER'S OWN TITLE. This assertion exists
	// because it was WRONG: the title read "A CHALLAN for deduction in this
	// period" — a %s where the number should have been — and it was invisible in
	// four passing tests until a mutation run printed the panel in full.
	if !strings.Contains(st.Blockers[0].What, "1 deduction") {
		t.Errorf("the blocker title must carry the count, since it is the line an operator "+
			"scans first; got %q", st.Blockers[0].What)
	}
}

// TestNoDeductorProfileLeadsTheBlockersAndSaysItHidesTheOthers pins the
// pre-emption honestly.
//
// readDeductorProfile runs before any deduction is read, so a company with no
// profile CANNOT be told about its challans in the same breath. The status could
// have quietly shown one blocker and let the operator think that was all;
// instead the blocker says out loud that it is hiding the rest.
func TestNoDeductorProfileLeadsTheBlockersAndSaysItHidesTheOthers(t *testing.T) {
	f := newReturnFixtureWithoutProfile(t)
	f.setPAN(t, f.consultant, "ABCPK9012Q")
	f.deduct(t, "P1", "2026-05-10", "194J-P", f.consultant, "200000.0000", "20000.0000")

	st := statusFor(t, f)
	if len(st.Blockers) == 0 {
		t.Fatal("a company with no profile must be told so")
	}
	first := st.Blockers[0]
	if !strings.Contains(first.What, "TAN") {
		t.Errorf("the profile blocker must LEAD, because it pre-empts every other "+
			"check; got %q", first.What)
	}
	if !strings.Contains(first.Why, "hides every other one") {
		t.Errorf("the profile blocker must admit that it hides the others, or an "+
			"operator reads a one-item list as one item of work; got %q", first.Why)
	}
	if !strings.Contains(st.Summary, "no TDS return profile") {
		t.Errorf("summary must name the profile as the first reason; got %q", st.Summary)
	}
}

// TestBooksReadyPutsTheRemainingBlockerOnUSRatherThanOnTheOperator is the case
// that matters most for honesty.
//
// When every data blocker is cleared, the operator must not be shown a stale
// "your books are incomplete" message — nor a download button, because nothing
// serves the bytes. They are told their books are ready and that the remaining
// gap is ours.
func TestBooksReadyPutsTheRemainingBlockerOnUSRatherThanOnTheOperator(t *testing.T) {
	f := newReturnFixture(t)
	f.setPAN(t, f.consultant, "ABCPK9012Q")
	v := f.deduct(t, "P1", "2026-05-10", "194J-P", f.consultant, "200000.0000", "20000.0000")
	f.challan(t, "0004329", "2026-06-07", "05122", "20000.0000", v, "194J-P", "20000.0000")

	st := statusFor(t, f)
	// STILL FALSE. Not because the books are wanting, but because no route emits
	// the file — and Available is the one thing a client branches on, so flipping
	// it here would offer a download that cannot happen.
	if st.Available {
		t.Fatal("available must stay false: no route serves the file bytes, so a client " +
			"branching on available would offer a download that does not exist")
	}
	if !strings.Contains(st.Summary, "YOUR BOOKS ARE READY") {
		t.Errorf("with every deduction deposited the operator must be told their books "+
			"are ready, not shown a generic refusal; got %q", st.Summary)
	}
	all := blockerText(st)
	if !strings.Contains(all, "this one is ours, not yours") {
		t.Errorf("the remaining blocker must be attributed to this software, so it is "+
			"not mistaken for more data entry; got %q", all)
	}
	// The four facts that must be ASKED for rather than inferred — inferring any
	// of them would put a false statement on a tax return.
	if !strings.Contains(all, "file sequence number") {
		t.Errorf("the blocker must name the facts that cannot be inferred; got %q", all)
	}
	// The FVU never goes away, however complete the books are.
	if !strings.Contains(all, "File Validation Utility") {
		t.Error("the FVU blocker is unconditional and must survive a ready set of books")
	}
	if st.Validated {
		t.Error("validated is always false; the FVU cannot run here")
	}
}

// TestAnAlteredVoucherSurfacesInTheStatusRatherThanBeingUsed is the 0045
// mechanism seen from the screen.
//
// It also pins the thing that makes the evidence trustworthy: the sentence shown
// here comes from challanRefusalFor, THE SAME function the emitter calls, so the
// panel cannot drift away from what would actually refuse at emission.
func TestAnAlteredVoucherSurfacesInTheStatusRatherThanBeingUsed(t *testing.T) {
	f := newReturnFixture(t)
	f.setPAN(t, f.consultant, "ABCPK9012Q")
	v := f.deduct(t, "P1", "2026-05-10", "194J-P", f.consultant, "200000.0000", "20000.0000")
	f.challan(t, "0004329", "2026-06-07", "05122", "20000.0000", v, "194J-P", "20000.0000")
	// The deposit stays as it was; the deduction is re-derived smaller. This is
	// what an ALTER does — internal/ledger/tds.go deletes and re-derives the row.
	// ₹1,00,000 at 10% = ₹10,000, against a ₹20,000 deposit already made.
	f.reduceDeduction(t, v, "194J-P", "100000.0000", "10000.0000")

	st := statusFor(t, f)
	all := blockerText(st)
	if !strings.Contains(all, "ALTERED") {
		t.Errorf("an allocation that no longer matches its deduction must be surfaced as "+
			"an alter, not silently used to build a return; got %q", all)
	}
	if !strings.Contains(all, "P1") {
		t.Errorf("the alter blocker must name the voucher to correct; got %q", all)
	}
	if !strings.Contains(st.Summary, "altered after the tax was deposited") {
		t.Errorf("the summary must distinguish an alter from an unpaid deduction, because "+
			"the remedy is different; got %q", st.Summary)
	}
}
