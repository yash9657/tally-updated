package reporting

// cost_summary_test.go — the PURE half of the two cost summaries, with NO
// DATABASE.
//
// It lives in `package reporting` rather than `package reporting_test` for the
// reason closing_stock_internal_test.go records: buildCostCentreTree and
// summariseCostCategories are unexported, and routing these assertions through
// the service would test them only at whatever shapes a SQL fixture happened to
// produce — which for a hierarchy walk means never at a cycle and never at the
// depth bound, and for the per-axis grouping means only if somebody remembered to
// seed two axes on one posting. The DB-backed half is
// cost_summary_db_test.go in package reporting_test, where the fixtures are.
//
// THE ORDER OF THIS FILE IS THE ORDER OF THE RISK:
//
//  1. TestOnePostingOnTwoAxesReportsOneHundredPercentTwiceNotTwoHundred — the
//     DOUBLE-COUNT GUARD. The single most important test in this lane. It is
//     mutation-proved: folding every fact onto every axis makes it fail with the
//     rupee consequence.
//  2. Its CONTROL — the same fixture with ONE axis allocated. Without the
//     control, test 1 would still pass if the aggregator ignored facts entirely.
//  3. The roll-up arithmetic, the deep chain, and the two bounded-walk cases.
//  4. The remainder arithmetic and the reconciliation.

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// d is a money literal. NEVER a float: 1000.50 is not representable as one, and
// the whole of this file is about figures agreeing to the paisa.
func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// axis mints a cost category for a fixture.
func axis(name string, allowNonRevenue bool) CostAxis {
	return CostAxis{ID: uuid.New(), Name: name, AllocateNonRevenue: allowNonRevenue}
}

// base is one axis's denominator row, as costAxisBaseSQL would return it.
func base(a CostAxis, allocableDr, allocableCr string, allocableN int,
	unallocDr, unallocCr string, unallocN int) costAxisBase {
	return costAxisBase{
		Axis:                a,
		AllocableDebit:      d(allocableDr),
		AllocableCredit:     d(allocableCr),
		AllocablePostings:   allocableN,
		UnallocatedDebit:    d(unallocDr),
		UnallocatedCredit:   d(unallocCr),
		UnallocatedPostings: unallocN,
	}
}

// rowFor finds an axis's row by name, failing loudly rather than returning a
// zero row that would make every later assertion pass vacuously.
func rowFor(t *testing.T, rows []CostCategoryRow, name string) CostCategoryRow {
	t.Helper()
	for _, r := range rows {
		if r.CategoryName == name {
			return r
		}
	}
	t.Fatalf("no row for axis %q in %d rows", name, len(rows))
	return CostCategoryRow{}
}

// ---------------------------------------------------------------------------
// 1. THE DOUBLE-COUNT GUARD
// ---------------------------------------------------------------------------

// TestOnePostingOnTwoAxesReportsOneHundredPercentTwiceNotTwoHundred is THE test
// in this lane.
//
// ONE Rs 1,00,000 salary posting, allocated IN FULL across Departments (Sales
// 60,000 / Admin 40,000) AND AGAIN IN FULL across Branches (Mumbai 70,000 /
// Delhi 30,000). Both allocations are complete and both are correct — the
// deferred trigger cost_allocation_sums_to_posting groups by (posting, category)
// precisely so that the second one is possible.
//
// So the ONLY correct answer is: Department accounts for 1,00,000, Branch
// accounts for 1,00,000, and NOTHING anywhere reports 2,00,000. A report that
// summed across axes would tell a shopkeeper their April salary bill was twice
// what they paid — and it would foot, and every subtotal on it would tie.
//
// ⚠️ MUTATION-PROVED. See the comment at the end of this function for what was
// changed, what failed, and the message it printed.
func TestOnePostingOnTwoAxesReportsOneHundredPercentTwiceNotTwoHundred(t *testing.T) {
	dept := axis("Department", false)
	branch := axis("Branch", false)
	posting := uuid.New()
	const salary = "100000.0000"

	// The denominator is the SAME single posting for both axes: one Rs 1,00,000
	// debit to Salaries, a P&L ledger, so both revenue-only axes cover it. Neither
	// axis has any unallocated remainder — both are complete.
	bases := []costAxisBase{
		base(branch, salary, "0", 1, "0", "0", 0),
		base(dept, salary, "0", 1, "0", "0", 0),
	}
	// The facts are ONE ROW PER (POSTING, CATEGORY) — the grain the invariant is
	// defined on. The same posting appears twice, each time with that axis's own
	// COMPLETE total. That is parallel allocation, not duplication.
	facts := []costAllocationFact{
		{CategoryID: dept.ID, PostingID: posting, DrCr: "D",
			PostingAmount: d(salary), Allocated: d(salary), Allocable: true},
		{CategoryID: branch.ID, PostingID: posting, DrCr: "D",
			PostingAmount: d(salary), Allocated: d(salary), Allocable: true},
	}

	rows := summariseCostCategories(bases, facts)
	if len(rows) != 2 {
		t.Fatalf("got %d rows for 2 axes", len(rows))
	}

	for _, name := range []string{"Department", "Branch"} {
		r := rowFor(t, rows, name)
		if !r.AllocatedDebit.Equal(d(salary)) {
			t.Errorf("the %s axis reports %s allocated against a %s salary posting. "+
				"The same money is allocated IN FULL on every axis, so each axis must report "+
				"exactly %s. %s means this axis absorbed another axis's allocation, and the "+
				"report is telling the shopkeeper their salary cost was %s when they paid %s.",
				name, r.AllocatedDebit.StringFixed(4), salary, salary,
				r.AllocatedDebit.StringFixed(4), r.AllocatedDebit.StringFixed(4), salary)
		}
		if r.AllocatedPostings != 1 {
			t.Errorf("the %s axis reports %d allocated posting(s); there is exactly ONE posting, "+
				"allocated once on this axis and once on the other", name, r.AllocatedPostings)
		}
		// And the row must still reconcile: allocable - allocated - unallocated = 0.
		if !r.Reconciles() {
			t.Errorf("the %s axis does not reconcile: allocable %s, allocated %s, unallocated %s, "+
				"difference %s — with one fully-allocated posting the difference must be zero to the paisa",
				name, r.AllocableDebit.StringFixed(4), r.AllocatedDebit.StringFixed(4),
				r.UnallocatedDebit.StringFixed(4), r.DifferenceDebit.StringFixed(4))
		}
		if !r.IsFullyAllocated() {
			t.Errorf("the %s axis is reported as incomplete although its only posting is "+
				"allocated in full", name)
		}
	}

	// ⛔ AND NOTHING REPORTS 2,00,000 — the assertion the whole model turns on.
	// Stated separately from the per-axis check because a bug that split the
	// double count unevenly (150,000 and 50,000) would satisfy neither of the
	// checks above in the same way and this one catches every shape of it.
	twice := d(salary).Mul(decimal.NewFromInt(2))
	sumAcross := decimal.Zero
	for _, r := range rows {
		sumAcross = sumAcross.Add(r.AllocatedDebit)
		if r.AllocatedDebit.GreaterThan(d(salary)) {
			t.Errorf("axis %q reports %s allocated, which is MORE than the %s posting it was "+
				"allocated from: money has been counted twice", r.CategoryName,
				r.AllocatedDebit.StringFixed(4), salary)
		}
	}
	// The sum ACROSS axes is 2,00,000 and that is CORRECT — it is exactly why no
	// such total may ever be shown. Asserted here so that anybody who later adds
	// a grand total finds a test that explains why it cannot exist.
	if !sumAcross.Equal(twice) {
		t.Errorf("the sum across axes is %s, expected %s: the fixture no longer describes "+
			"one posting allocated in full on two axes, so this test proves nothing",
			sumAcross.StringFixed(4), twice.StringFixed(4))
	}

	// MUTATION PROOF (performed, then reverted):
	// In summariseCostCategories the lookup
	//     i, ok := byAxis[f.CategoryID]
	// was replaced with a loop folding every fact onto EVERY row. This test then
	// failed with:
	//   the Department axis reports 200000.0000 allocated against a 100000.0000
	//   salary posting. The same money is allocated IN FULL on every axis, so each
	//   axis must report exactly 100000.0000. 200000.0000 means this axis absorbed
	//   another axis's allocation, and the report is telling the shopkeeper their
	//   salary cost was 200000.0000 when they paid 100000.0000.
	// plus the same for Branch, plus the "MORE than the posting it was allocated
	// from" line and both rows' reconciliation failures. Restored afterwards.
}

// TestOneAxisAllocatedIsTheControl is the CONTROL for the test above.
//
// Without it, the double-count test would pass just as happily against an
// aggregator that ignored the facts and reported zero everywhere, or one that
// reported the allocable base as the allocated figure. This fixture has the SAME
// posting allocated on Department ONLY, so the two axes must now DIFFER: one
// complete, one entirely unallocated. A refusal (or an agreement) that fires for
// both fixtures alike proves nothing about either.
func TestOneAxisAllocatedIsTheControl(t *testing.T) {
	dept := axis("Department", false)
	branch := axis("Branch", false)
	posting := uuid.New()
	const salary = "100000.0000"

	bases := []costAxisBase{
		// Branch covers the same posting and has NOTHING allocated on it, so the
		// whole of it is Branch's remainder.
		base(branch, salary, "0", 1, salary, "0", 1),
		base(dept, salary, "0", 1, "0", "0", 0),
	}
	facts := []costAllocationFact{
		{CategoryID: dept.ID, PostingID: posting, DrCr: "D",
			PostingAmount: d(salary), Allocated: d(salary), Allocable: true},
	}

	rows := summariseCostCategories(bases, facts)

	dr := rowFor(t, rows, "Department")
	if !dr.AllocatedDebit.Equal(d(salary)) || !dr.IsFullyAllocated() {
		t.Errorf("Department: allocated %s, unallocated %s — the one axis that WAS allocated "+
			"must report the whole posting and no gap",
			dr.AllocatedDebit.StringFixed(4), dr.UnallocatedDebit.StringFixed(4))
	}

	br := rowFor(t, rows, "Branch")
	if !br.AllocatedDebit.IsZero() {
		t.Errorf("Branch reports %s allocated, but nothing was allocated on the Branch axis. "+
			"An axis that borrows another axis's allocations reports full coverage of cost "+
			"nobody analysed by branch.", br.AllocatedDebit.StringFixed(4))
	}
	if !br.UnallocatedDebit.Equal(d(salary)) || br.UnallocatedPostings != 1 {
		t.Errorf("Branch reports %s / %d posting(s) unallocated, want %s / 1: the gap is the "+
			"whole point of this report and an axis nobody used must show the whole cost as missing",
			br.UnallocatedDebit.StringFixed(4), br.UnallocatedPostings, salary)
	}
	if br.IsFullyAllocated() {
		t.Error("Branch is reported as fully allocated although nothing was allocated on it")
	}
	if !br.Reconciles() || !dr.Reconciles() {
		t.Errorf("a row does not reconcile: Department diff %s, Branch diff %s",
			dr.DifferenceDebit.StringFixed(4), br.DifferenceDebit.StringFixed(4))
	}
	// The gap must be legible in words, in rupees, in the note that travels with
	// the data — not left to a reader to compute from two columns.
	joined := strings.Join(br.Notes, " ")
	if !strings.Contains(joined, "100000.0000") || !strings.Contains(joined, "Branch") {
		t.Errorf("the Branch gap note does not state the missing amount and the axis: %q", joined)
	}
}

// TestAnUnallocatedRemainderIsNotHiddenByAnotherAxisBeingComplete is the second
// half of the same worry, at the report level: the summary's own notes must NAME
// the incomplete axis. A report where one complete axis makes the page look
// finished is how 40,000 stays unattributed for a year.
func TestAnUnallocatedRemainderIsNotHiddenByAnotherAxisBeingComplete(t *testing.T) {
	dept := axis("Department", false)
	branch := axis("Branch", false)
	p1, p2 := uuid.New(), uuid.New()

	bases := []costAxisBase{
		// Branch: two allocable postings (60,000 + 40,000), only the first allocated.
		base(branch, "100000", "0", 2, "40000", "0", 1),
		base(dept, "100000", "0", 2, "0", "0", 0),
	}
	facts := []costAllocationFact{
		{CategoryID: dept.ID, PostingID: p1, DrCr: "D", PostingAmount: d("60000"), Allocated: d("60000"), Allocable: true},
		{CategoryID: dept.ID, PostingID: p2, DrCr: "D", PostingAmount: d("40000"), Allocated: d("40000"), Allocable: true},
		{CategoryID: branch.ID, PostingID: p1, DrCr: "D", PostingAmount: d("60000"), Allocated: d("60000"), Allocable: true},
	}

	rows := summariseCostCategories(bases, facts)
	notes := costCategoryNotes(rows)
	joined := strings.Join(notes, " ")
	if !strings.Contains(joined, "Incomplete on: Branch") {
		t.Errorf("the summary notes do not name the incomplete axis: %q", joined)
	}
	if strings.Contains(joined, "Incomplete on: Branch, Department") ||
		strings.Contains(joined, "Department, Branch") {
		t.Errorf("the summary names Department as incomplete although both its postings are "+
			"allocated in full: %q", joined)
	}
	// THE NO-TOTAL NOTE IS NOT OPTIONAL. It is the only place the artefact itself
	// says why there is no total row, and a screen must not be able to omit it.
	if !strings.Contains(joined, "NO TOTAL IS SHOWN ACROSS THESE ROWS") {
		t.Errorf("the summary does not carry the no-cross-axis-total note: %q", joined)
	}
}

// TestAnOffAxisAllocationIsNamedNotCounted pins the one contradiction the
// database does NOT enforce.
//
// allocate_non_revenue is a Go-side rule: migration 0040 stores the flag and
// nothing in it stops an allocation on a balance-sheet ledger. If such a row
// existed and this report counted it, a revenue-only axis would report coverage
// of cost that is not in its denominator at all — and the row would still
// reconcile, because both sides would move together. So it is reported
// SEPARATELY and named.
func TestAnOffAxisAllocationIsNamedNotCounted(t *testing.T) {
	dept := axis("Department", false) // revenue items only
	posting := uuid.New()
	capital := uuid.New()

	// One allocable P&L posting of 60,000 (allocated), plus an allocation against
	// a FIXED ASSET posting of 25,000 which this axis has no business carrying and
	// which is therefore NOT in the allocable base.
	bases := []costAxisBase{base(dept, "60000", "0", 1, "0", "0", 0)}
	facts := []costAllocationFact{
		{CategoryID: dept.ID, PostingID: posting, DrCr: "D", PostingAmount: d("60000"), Allocated: d("60000"), Allocable: true},
		{CategoryID: dept.ID, PostingID: capital, DrCr: "D", PostingAmount: d("25000"), Allocated: d("25000"), Allocable: false},
	}

	r := summariseCostCategories(bases, facts)[0]
	if !r.AllocatedDebit.Equal(d("60000")) {
		t.Errorf("allocated %s, want 60000.0000: an allocation on a ledger this axis may not "+
			"carry must not be added to the coverage of a base it is not part of",
			r.AllocatedDebit.StringFixed(4))
	}
	if !r.OffAxisDebit.Equal(d("25000")) || r.OffAxisPostings != 1 {
		t.Errorf("off-axis %s / %d posting(s), want 25000.0000 / 1", r.OffAxisDebit.StringFixed(4), r.OffAxisPostings)
	}
	if !r.Reconciles() {
		t.Errorf("the row must still reconcile with the off-axis row excluded from both sides: diff %s",
			r.DifferenceDebit.StringFixed(4))
	}
	if !strings.Contains(strings.Join(r.Notes, " "), "25000.0000") {
		t.Errorf("the off-axis contradiction is not stated in rupees: %q", r.Notes)
	}
}

// TestAPartlyAllocatedPostingIsReportedAsBroken pins the loud path for the case
// the trigger makes impossible. It is here because "impossible" is a property of
// a trigger that exists, and the report must not be the thing that hides its
// absence.
func TestAPartlyAllocatedPostingIsReportedAsBroken(t *testing.T) {
	dept := axis("Department", false)
	posting := uuid.New()
	bases := []costAxisBase{base(dept, "100000", "0", 1, "0", "0", 0)}
	facts := []costAllocationFact{
		// 60,000 allocated against a 1,00,000 posting: 40,000 unattributed.
		{CategoryID: dept.ID, PostingID: posting, DrCr: "D",
			PostingAmount: d("100000"), Allocated: d("60000"), Allocable: true},
	}

	r := summariseCostCategories(bases, facts)[0]
	if r.PartlyAllocatedPostings != 1 || !r.PartlyAllocatedShortfall.Equal(d("40000")) {
		t.Fatalf("partly allocated %d / shortfall %s, want 1 / 40000.0000",
			r.PartlyAllocatedPostings, r.PartlyAllocatedShortfall.StringFixed(4))
	}
	// ⛔ AND THE ROW MUST NOT RECONCILE. The 40,000 is in the allocable base, is
	// not allocated, and is not in the "no allocation at all" remainder either —
	// so the residual is exactly it, and a report that absorbed the difference
	// would show 60% coverage and no fault.
	if r.Reconciles() {
		t.Error("a part-allocated posting left the row reconciling: the 40,000 gap would be invisible")
	}
	if !r.DifferenceDebit.Equal(d("40000")) {
		t.Errorf("difference %s, want 40000.0000 — the residual must be the unattributed amount "+
			"itself so the report can quote it", r.DifferenceDebit.StringFixed(4))
	}
	joined := strings.Join(r.Notes, " ")
	if !strings.Contains(joined, "40000.0000") || !strings.Contains(joined, "DOES NOT RECONCILE") {
		t.Errorf("the broken-installation note does not state the amount and the failure: %q", joined)
	}
}

// TestCreditSideIsKeptApartFromDebit — an allocation's amount is a MAGNITUDE and
// the POSTING's dr_cr carries the direction. Netting the two sides inside one
// figure would report a fully-allocated axis as half allocated (or as zero) the
// moment a credit note landed in the period.
func TestCreditSideIsKeptApartFromDebit(t *testing.T) {
	dept := axis("Department", false)
	pDr, pCr := uuid.New(), uuid.New()
	bases := []costAxisBase{base(dept, "60000", "10000", 2, "0", "0", 0)}
	facts := []costAllocationFact{
		{CategoryID: dept.ID, PostingID: pDr, DrCr: "D", PostingAmount: d("60000"), Allocated: d("60000"), Allocable: true},
		{CategoryID: dept.ID, PostingID: pCr, DrCr: "C", PostingAmount: d("10000"), Allocated: d("10000"), Allocable: true},
	}
	r := summariseCostCategories(bases, facts)[0]
	if !r.AllocatedDebit.Equal(d("60000")) || !r.AllocatedCredit.Equal(d("10000")) {
		t.Errorf("allocated %s Dr / %s Cr, want 60000.0000 / 10000.0000 — the credit allocation "+
			"has been folded into the debit column",
			r.AllocatedDebit.StringFixed(4), r.AllocatedCredit.StringFixed(4))
	}
	if !r.Reconciles() {
		t.Errorf("both sides must reconcile independently: diff %s Dr / %s Cr",
			r.DifferenceDebit.StringFixed(4), r.DifferenceCredit.StringFixed(4))
	}
}

// TestAnAxisWithNothingAllocableSaysSo — an axis whose denominator is empty is
// not "0% complete", it is "nothing this period could go here". Reporting the
// first would send an operator looking for postings that do not exist.
func TestAnAxisWithNothingAllocableSaysSo(t *testing.T) {
	a := axis("Project", false)
	rows := summariseCostCategories([]costAxisBase{base(a, "0", "0", 0, "0", "0", 0)}, nil)
	if len(rows) != 1 {
		t.Fatalf("an axis with no allocable postings must still produce a ROW: got %d", len(rows))
	}
	r := rows[0]
	if !r.Reconciles() || !r.IsFullyAllocated() {
		t.Errorf("an empty axis must reconcile trivially: diff %s", r.DifferenceDebit.StringFixed(4))
	}
	if !strings.Contains(strings.Join(r.Notes, " "), "Nothing in this period could be allocated") {
		t.Errorf("an empty axis does not say why it is empty: %q", r.Notes)
	}
}

// ---------------------------------------------------------------------------
// 3. THE ROLL-UP AND THE BOUNDED WALK
// ---------------------------------------------------------------------------

// centre is one costCentreFact for a hand-built tree.
func centre(id, parent uuid.UUID, name, debit, credit string) costCentreFact {
	return costCentreFact{CentreID: id, Name: name, ParentID: parent, Debit: d(debit), Credit: d(credit)}
}

// nodeByName finds a node anywhere in the forest.
func nodeByName(nodes []CostCentreNode, name string) (CostCentreNode, bool) {
	for _, n := range nodes {
		if n.Name == name {
			return n, true
		}
		if c, ok := nodeByName(n.Children, name); ok {
			return c, true
		}
	}
	return CostCentreNode{}, false
}

// mustNode fails loudly rather than letting a missing centre make an assertion
// pass against a zero value.
func mustNode(t *testing.T, nodes []CostCentreNode, name string) CostCentreNode {
	t.Helper()
	n, ok := nodeByName(nodes, name)
	if !ok {
		t.Fatalf("no cost centre named %q in the rolled-up tree", name)
	}
	return n
}

// TestTheRollUpAddsDescendantsAndKeepsOwnFiguresApart is the roll-up arithmetic
// over a hand-built tree.
//
//	Sales            own 10,000
//	  Sales - North  own 60,000
//	  Sales - South  own 40,000
//	    Chennai      own  5,000
//	Admin            own 25,000
//
// Sales' TOTAL is 1,15,000 and its OWN is 10,000, and a reader needs both: a
// screen showing only the total cannot tell an operator whether the money was
// keyed against Sales itself or against its sub-teams.
func TestTheRollUpAddsDescendantsAndKeepsOwnFiguresApart(t *testing.T) {
	sales, north, south, chennai, admin := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	facts := []costCentreFact{
		centre(sales, uuid.Nil, "Sales", "10000", "0"),
		centre(north, sales, "Sales - North", "60000", "0"),
		centre(south, sales, "Sales - South", "40000", "1000"),
		centre(chennai, south, "Chennai", "5000", "0"),
		centre(admin, uuid.Nil, "Admin", "25000", "0"),
	}

	roots, unattached, warnings := buildCostCentreTree(facts)
	if len(unattached) != 0 {
		t.Fatalf("a well-formed tree produced %d unattached centre(s): %v", len(unattached), warnings)
	}
	if len(roots) != 2 {
		t.Fatalf("got %d roots, want 2 (Admin, Sales)", len(roots))
	}
	// Stable, name-folded order: Admin before Sales.
	if roots[0].Name != "Admin" || roots[1].Name != "Sales" {
		t.Errorf("roots are %q, %q; want Admin, Sales — an unstable order cannot be diffed "+
			"by a reader or golden-tested by a machine", roots[0].Name, roots[1].Name)
	}

	s := mustNode(t, roots, "Sales")
	if !s.OwnDebit.Equal(d("10000")) {
		t.Errorf("Sales OWN debit %s, want 10000.0000 — the roll-up has overwritten the "+
			"centre's own figure with its subtree's", s.OwnDebit.StringFixed(4))
	}
	if !s.TotalDebit.Equal(d("115000")) {
		t.Errorf("Sales TOTAL debit %s, want 115000.0000 (10,000 own + 60,000 North + "+
			"40,000 South + 5,000 Chennai)", s.TotalDebit.StringFixed(4))
	}
	if !s.TotalCredit.Equal(d("1000")) {
		t.Errorf("Sales TOTAL credit %s, want 1000.0000 — the credit side must roll up too, "+
			"or a department's net cost is overstated by every credit note under it",
			s.TotalCredit.StringFixed(4))
	}
	if !s.TotalNet().Equal(d("114000")) {
		t.Errorf("Sales net %s, want 114000.0000", s.TotalNet().StringFixed(4))
	}
	// A grandchild rolls into its parent AND into its grandparent, once each.
	so := mustNode(t, roots, "Sales - South")
	if !so.TotalDebit.Equal(d("45000")) {
		t.Errorf("Sales - South TOTAL debit %s, want 45000.0000 (40,000 own + 5,000 Chennai)",
			so.TotalDebit.StringFixed(4))
	}
	// Depth is what a flat renderer indents by.
	if ch := mustNode(t, roots, "Chennai"); ch.Depth != 2 {
		t.Errorf("Chennai depth %d, want 2", ch.Depth)
	}
	if !mustNode(t, roots, "Admin").TotalDebit.Equal(d("25000")) {
		t.Errorf("Admin TOTAL debit is not its own 25,000")
	}
}

// TestTheAxisTotalIsTheRootsNotEveryNode pins the hierarchy version of the
// double count.
//
// Summing every NODE's total gives 1,15,000 (Sales) + 60,000 + 45,000 + 5,000 +
// 25,000 = 2,50,000 against 1,40,000 of allocations. The report's total is the
// sum of the ROOTS' totals, which is the only sum that equals what was
// allocated.
func TestTheAxisTotalIsTheRootsNotEveryNode(t *testing.T) {
	sales, north, south, chennai, admin := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	facts := []costCentreFact{
		centre(sales, uuid.Nil, "Sales", "10000", "0"),
		centre(north, sales, "Sales - North", "60000", "0"),
		centre(south, sales, "Sales - South", "40000", "0"),
		centre(chennai, south, "Chennai", "5000", "0"),
		centre(admin, uuid.Nil, "Admin", "25000", "0"),
	}
	roots, unattached, _ := buildCostCentreTree(facts)

	// The FLAT oracle: every centre's OWN figure, added without the tree.
	flat := decimal.Zero
	for _, f := range facts {
		flat = flat.Add(f.Debit)
	}
	rootTotal := decimal.Zero
	for _, n := range roots {
		rootTotal = rootTotal.Add(n.TotalDebit)
	}
	if !rootTotal.Equal(flat) {
		t.Errorf("the roots roll up to %s but the centres hold %s: the roll-up and the flat sum "+
			"of the same allocations must agree to the paisa, or the axis total ties to nothing",
			rootTotal.StringFixed(4), flat.StringFixed(4))
	}
	if !flat.Equal(d("140000")) {
		t.Fatalf("fixture drift: the centres hold %s, expected 140000.0000", flat.StringFixed(4))
	}
	if len(unattached) != 0 {
		t.Fatalf("unexpected unattached centres in a well-formed tree")
	}

	// And the every-node sum is what must NOT be used, stated so a later reader
	// finds the reason rather than the habit.
	everyNode := decimal.Zero
	var walk func([]CostCentreNode)
	walk = func(ns []CostCentreNode) {
		for _, n := range ns {
			everyNode = everyNode.Add(n.TotalDebit)
			walk(n.Children)
		}
	}
	walk(roots)
	if everyNode.Equal(rootTotal) {
		t.Error("summing every node equals summing the roots, so this fixture has no depth and " +
			"cannot demonstrate the hierarchy double count")
	}
	if !everyNode.Equal(d("250000")) {
		t.Errorf("summing every node gives %s; the fixture expected 250000.0000, which is the "+
			"figure a naive total would print against 140000.0000 of real allocations",
			everyNode.StringFixed(4))
	}
}

// TestADeepChainRollsAllTheWayUp — a chain well inside the bound must roll up
// completely. A recursion that stopped one level short would still foot at every
// level it visited, so the assertion is on the ROOT of a long chain.
func TestADeepChainRollsAllTheWayUp(t *testing.T) {
	const depth = 40 // comfortably inside MaxCostCentreDepth
	ids := make([]uuid.UUID, depth)
	for i := range ids {
		ids[i] = uuid.New()
	}
	facts := make([]costCentreFact, 0, depth)
	for i := 0; i < depth; i++ {
		parent := uuid.Nil
		if i > 0 {
			parent = ids[i-1]
		}
		// Names are zero-padded so the fold-by-name ordering is also the depth
		// order; an unpadded "L10" would sort before "L2".
		facts = append(facts, centre(ids[i], parent, padName(i), "100", "0"))
	}

	roots, unattached, warnings := buildCostCentreTree(facts)
	if len(unattached) != 0 {
		t.Fatalf("a %d-deep chain produced %d unattached centre(s): %v", depth, len(unattached), warnings)
	}
	if len(roots) != 1 {
		t.Fatalf("got %d roots for one chain", len(roots))
	}
	want := d("100").Mul(decimal.NewFromInt(int64(depth)))
	if !roots[0].TotalDebit.Equal(want) {
		t.Errorf("a %d-deep chain of 100 each rolls up to %s, want %s — a walk that stops short "+
			"loses the cost of every level below it and still foots at the ones it visited",
			depth, roots[0].TotalDebit.StringFixed(4), want.StringFixed(4))
	}
	// The deepest node's Depth is the chain length less one.
	deepest := mustNode(t, roots, padName(depth-1))
	if deepest.Depth != depth-1 {
		t.Errorf("deepest centre reports depth %d, want %d", deepest.Depth, depth-1)
	}
}

// padName is a zero-padded level name, so name-folded ordering matches depth.
func padName(i int) string {
	s := "L"
	switch {
	case i < 10:
		s += "00"
	case i < 100:
		s += "0"
	}
	return s + itoa(i)
}

// itoa avoids importing strconv for one call in a test file.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// TestACycleIsListedNotWalkedForever is the BOUNDED-WALK case that matters most.
//
// A pure two-row cycle (Mumbai parents Delhi, Delhi parents Mumbai) has NO root,
// so no depth bound can ever see it — a walk that started from roots and trusted
// acyclicity would simply never visit them, and one that started from every node
// would recurse until the stack ended. The reachability sweep is what catches
// this, and the centres are LISTED WITH THEIR FIGURES, because a cost centre that
// silently vanishes from a cost report takes its money with it.
//
// ⛔ The test must complete. If buildCostCentreTree ever loops, this test HANGS
// rather than fails, which is itself the signal — a hung request is exactly the
// failure the bound exists to prevent.
func TestACycleIsListedNotWalkedForever(t *testing.T) {
	mumbai, delhi, sales := uuid.New(), uuid.New(), uuid.New()
	facts := []costCentreFact{
		centre(sales, uuid.Nil, "Sales", "10000", "0"),
		centre(mumbai, delhi, "Mumbai", "70000", "0"),
		centre(delhi, mumbai, "Delhi", "30000", "0"),
	}

	roots, unattached, warnings := buildCostCentreTree(facts)
	if len(roots) != 1 || roots[0].Name != "Sales" {
		t.Fatalf("got %d roots, want just Sales: a cycle has no root and must not become one", len(roots))
	}
	if !roots[0].TotalDebit.Equal(d("10000")) {
		t.Errorf("Sales rolls up to %s, want 10000.0000: the cycle must not be attached to it",
			roots[0].TotalDebit.StringFixed(4))
	}
	if len(unattached) != 2 {
		t.Fatalf("got %d unattached centre(s), want 2 (Mumbai and Delhi): a cycle's members are "+
			"unreachable from any root and must be reported, not dropped", len(unattached))
	}
	sum := decimal.Zero
	for _, n := range unattached {
		sum = sum.Add(n.OwnDebit)
	}
	if !sum.Equal(d("100000")) {
		t.Errorf("the unattached centres hold %s, want 100000.0000 — their figures must survive "+
			"being unplaceable", sum.StringFixed(4))
	}
	joined := strings.Join(warnings, " ")
	if !strings.Contains(joined, "100000.0000") ||
		!strings.Contains(joined, "Mumbai") || !strings.Contains(joined, "Delhi") {
		t.Errorf("the warning does not name the centres and the amount at risk: %q", joined)
	}
	if !strings.Contains(joined, "loops") {
		t.Errorf("the warning does not tell the operator what to fix: %q", joined)
	}
}

// TestAChainDeeperThanTheBoundStopsAndSaysSo is the other bounded-walk case: a
// chain that DOES have a root but runs past MaxCostCentreDepth. The walk stops,
// the tail is reported as unattached with its money, and the warning says why.
func TestAChainDeeperThanTheBoundStopsAndSaysSo(t *testing.T) {
	const depth = MaxCostCentreDepth + 5
	ids := make([]uuid.UUID, depth)
	for i := range ids {
		ids[i] = uuid.New()
	}
	facts := make([]costCentreFact, 0, depth)
	for i := 0; i < depth; i++ {
		parent := uuid.Nil
		if i > 0 {
			parent = ids[i-1]
		}
		facts = append(facts, centre(ids[i], parent, padName(i), "100", "0"))
	}

	roots, unattached, warnings := buildCostCentreTree(facts)
	if len(roots) != 1 {
		t.Fatalf("got %d roots for one over-deep chain", len(roots))
	}
	// The bound admits levels 0..MaxCostCentreDepth inclusive; everything below
	// is unplaced.
	wantPlaced := MaxCostCentreDepth + 1
	wantUnattached := depth - wantPlaced
	if len(unattached) != wantUnattached {
		t.Fatalf("got %d unattached centre(s), want %d: the walk must stop at %d levels and "+
			"report the rest, never recurse without a bound",
			len(unattached), wantUnattached, MaxCostCentreDepth)
	}
	if !roots[0].TotalDebit.Equal(d("100").Mul(decimal.NewFromInt(int64(wantPlaced)))) {
		t.Errorf("the placed part rolls up to %s, want %s", roots[0].TotalDebit.StringFixed(4),
			d("100").Mul(decimal.NewFromInt(int64(wantPlaced))).StringFixed(4))
	}
	joined := strings.Join(warnings, " ")
	if !strings.Contains(joined, "deeper than") || !strings.Contains(joined, "levels deep") {
		t.Errorf("the warnings do not explain the depth bound: %q", joined)
	}
	// The whole 100 * depth is still accounted for between the two halves — the
	// bound must not LOSE money, only refuse to nest it.
	total := roots[0].TotalDebit
	for _, n := range unattached {
		total = total.Add(n.OwnDebit)
	}
	if !total.Equal(d("100").Mul(decimal.NewFromInt(int64(depth)))) {
		t.Errorf("placed + unattached = %s, want %s: the bound lost cost rather than refusing "+
			"to nest it", total.StringFixed(4), d("100").Mul(decimal.NewFromInt(int64(depth))).StringFixed(4))
	}
}

// TestAnOrphanedCentreIsReportedNotDropped — a centre whose parent is not on the
// axis cannot exist (cost_centre_parent_shares_axis_fk), which is exactly why the
// handling has to be tested rather than reasoned about: the constraint is in
// another file and a report that assumed it would drop the centre silently if it
// were ever relaxed.
func TestAnOrphanedCentreIsReportedNotDropped(t *testing.T) {
	orphan, ghost, sales := uuid.New(), uuid.New(), uuid.New()
	facts := []costCentreFact{
		centre(sales, uuid.Nil, "Sales", "10000", "0"),
		centre(orphan, ghost, "Warehouse", "8000", "0"), // parent not in the set
	}
	roots, unattached, warnings := buildCostCentreTree(facts)
	if len(roots) != 1 {
		t.Fatalf("got %d roots, want 1", len(roots))
	}
	if len(unattached) != 1 || unattached[0].Name != "Warehouse" {
		t.Fatalf("the orphan was not reported: unattached = %v", unattached)
	}
	if !strings.Contains(strings.Join(warnings, " "), "8000.0000") {
		t.Errorf("the warning does not state the orphan's amount: %q", warnings)
	}
}

// TestASelfParentedCentreDoesNotHang — cost_centre_not_its_own_parent forbids
// parent_id = id, so reaching this means the constraint is gone. It must be an
// unattached row, not an infinite descent.
func TestASelfParentedCentreDoesNotHang(t *testing.T) {
	self := uuid.New()
	facts := []costCentreFact{centre(self, self, "Loop", "500", "0")}
	roots, unattached, _ := buildCostCentreTree(facts)
	if len(roots) != 0 {
		t.Errorf("a self-parented centre became a root")
	}
	if len(unattached) != 1 || !unattached[0].OwnDebit.Equal(d("500")) {
		t.Errorf("a self-parented centre must be listed with its 500: got %v", unattached)
	}
}

// TestAnEmptyAxisIsAnEmptyForest — no centres at all is a legitimate answer
// (an axis was created and nothing hung off it yet), not a fault.
func TestAnEmptyAxisIsAnEmptyForest(t *testing.T) {
	roots, unattached, warnings := buildCostCentreTree(nil)
	if len(roots) != 0 || len(unattached) != 0 || len(warnings) != 0 {
		t.Errorf("an axis with no centres produced roots=%d unattached=%d warnings=%v",
			len(roots), len(unattached), warnings)
	}
}

// ---------------------------------------------------------------------------
// 4. THE PERIOD
// ---------------------------------------------------------------------------

// TestCostPeriodFollowsTheStatementConvention — a closed interval, inclusive at
// both ends, either bound optional, and an unparseable date refused rather than
// silently dropped.
func TestCostPeriodFollowsTheStatementConvention(t *testing.T) {
	if f, to, err := costPeriod(" 2025-04-01 ", "2025-06-30"); err != nil ||
		f != "2025-04-01" || to != "2025-06-30" {
		t.Errorf("costPeriod trimmed/parsed to (%q,%q,%v)", f, to, err)
	}
	if f, to, err := costPeriod("", ""); err != nil || f != "" || to != "" {
		t.Errorf("an unbounded period must be allowed (a cost analysis of the whole book is a "+
			"real question): (%q,%q,%v)", f, to, err)
	}
	if _, _, err := costPeriod("01-04-2025", ""); err == nil {
		t.Error("a dd-mm-yyyy date was accepted: a report that quietly ignored the period asked " +
			"for would answer a different question and still look right")
	}
	if _, _, err := costPeriod("2025-06-30", "2025-04-01"); err == nil {
		t.Error("a period that ends before it begins was accepted")
	}
	// The boundary itself: equal bounds are a single day, not an empty range.
	if _, _, err := costPeriod("2025-04-01", "2025-04-01"); err != nil {
		t.Errorf("a one-day period was refused: %v", err)
	}
}

// TestTheAxisRefusalNamesTheAlternatives — the missing-parameter answer. It must
// name the axes, because the two silent alternatives are both worse: picking one
// renders a real report for the wrong axis, and summing them reports twice the
// cost.
func TestTheAxisRefusalNamesTheAlternatives(t *testing.T) {
	dept, branch := axis("Department", false), axis("Branch", false)
	err := &CostAxisError{Missing: true, Available: []CostAxis{branch, dept}}
	msg := err.Error()
	for _, want := range []string{"Department", "Branch", dept.ID.String(), branch.ID.String(),
		"cost_category_id", "twice the cost"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not contain %q: %q", want, msg)
		}
	}

	// An unknown axis names the id it was given, so an operator can see WHICH id
	// was wrong rather than being told to choose again.
	bad := uuid.New()
	msg = (&CostAxisError{Named: bad, Available: []CostAxis{dept}}).Error()
	if !strings.Contains(msg, bad.String()) || !strings.Contains(msg, "Department") {
		t.Errorf("the unknown-axis refusal does not name the id and the alternatives: %q", msg)
	}

	// A company with no axes at all gets told to create one, not handed an empty
	// list of things to choose from.
	msg = (&CostAxisError{Missing: true}).Error()
	if !strings.Contains(msg, "no cost categories at all") {
		t.Errorf("a company with no axes gets an unhelpful refusal: %q", msg)
	}
}
