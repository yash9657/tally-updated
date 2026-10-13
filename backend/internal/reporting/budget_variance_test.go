package reporting

// THE PURE HALF OF BUDGET VS ACTUAL — the arithmetic and the seven statuses.
//
// Everything here runs without Postgres, deliberately. scoreBudgetRow and
// finaliseBudgetVariance are the two functions in budget_variance.go most likely
// to be wrong and least likely to be noticed, because EVERY WAY THEY CAN BE WRONG
// STILL FOOTS: a status collapsed into a neighbour, a percentage computed against
// a zero budget, a grand total that adds a position to a flow. None of those
// produces an error, a crash or an unbalanced statement — they produce a
// confident number, which is the defect class this repository's register is full
// of (D100, D105).
//
// ⛔ EVERY FAILURE MESSAGE BELOW NAMES THE CONSEQUENCE, not `want X got Y`. A
// test that says "want UNSPENT got UNDER" tells the next person which line to
// edit; one that says "an untouched budget would read as a budget that was nearly
// met" tells them why not to.

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func dec(s string) decimal.Decimal {
	d, err := decimal.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return d
}

// row builds a scored row from the four inputs that decide everything.
func row(drCr string, budget, debit, credit string) BudgetVarianceRow {
	r := BudgetVarianceRow{
		Dimension:    BudgetDimensionLedger,
		TargetID:     uuid.New(),
		TargetName:   "Travel",
		Nature:       "EXPENSE",
		Basis:        BudgetBasisNett,
		DrCr:         drCr,
		Budget:       dec(budget),
		ActualDebit:  dec(debit),
		ActualCredit: dec(credit),
	}
	scoreBudgetRow(&r)
	return r
}

// ---------------------------------------------------------------------------
// THE SEVEN STATUSES. Each is a DISTINCT FACT and none may be a rounding of
// another — see failure modes 1 and 2 in budget_variance.go's header.
// ---------------------------------------------------------------------------

func TestTheSevenStatusesStayDistinct(t *testing.T) {
	cases := []struct {
		what                  string
		drCr                  string
		budget, debit, credit string
		wantStatus            string
		wantActual            string
		// consequence is what a reader would be told if this row were mislabelled.
		consequence string
	}{
		{
			what: "an expense budget spent exactly", drCr: "D",
			budget: "500000", debit: "500000", credit: "0",
			wantStatus: BudgetOnTarget, wantActual: "500000",
			consequence: "a plan met to the paisa would read as over or under budget",
		},
		{
			what: "an expense budget overspent", drCr: "D",
			budget: "500000", debit: "620000", credit: "0",
			wantStatus: BudgetOver, wantActual: "620000",
			consequence: "overspend would not be flagged, which is the one thing this " +
				"report exists to surface",
		},
		{
			what: "an expense budget partly spent", drCr: "D",
			budget: "500000", debit: "300000", credit: "0",
			wantStatus: BudgetUnder, wantActual: "300000",
			consequence: "ordinary underspend would be confused with an untouched budget",
		},
		{
			// ⛔ FAILURE MODE 1. Nothing was posted at all.
			what: "a budget nothing was posted against", drCr: "D",
			budget: "500000", debit: "0", credit: "0",
			wantStatus: BudgetUnspent, wantActual: "0",
			consequence: "an UNTOUCHED budget would read as a budget that was simply " +
				"underspent — hiding that the plan was wrong, or that nobody is keying " +
				"the invoices",
		},
		{
			// ⛔ FAILURE MODE 1, SECOND HALF. Movement happened and cancelled.
			what: "a budget whose actual netted to zero", drCr: "D",
			budget: "500000", debit: "300000", credit: "300000",
			wantStatus: BudgetNettedToZero, wantActual: "0",
			consequence: "a reversal or a misposting would be indistinguishable from a " +
				"budget nobody touched, and the 300000 that moved would be invisible",
		},
		{
			what: "an actual on the opposite side from the budget", drCr: "D",
			budget: "500000", debit: "0", credit: "120000",
			wantStatus: BudgetContra, wantActual: "-120000",
			consequence: "a net CREDIT on a ledger budgeted for spend would read as " +
				"\"comfortably under budget\" instead of as the reversal or wrong-side " +
				"posting it almost always is",
		},
		{
			// An income budget: the natural side is credit, and exceeding it is still
			// OVER. The report refuses to call that favourable — see Nature.
			what: "an income budget exceeded", drCr: "C",
			budget: "1000000", debit: "0", credit: "1400000",
			wantStatus: BudgetOver, wantActual: "1400000",
			consequence: "sales ahead of plan would be scored on the wrong side and read " +
				"as a shortfall",
		},
		{
			what: "an income budget missed", drCr: "C",
			budget: "1000000", debit: "0", credit: "700000",
			wantStatus: BudgetUnder, wantActual: "700000",
			consequence: "a revenue shortfall would be mis-signed",
		},
	}

	for _, c := range cases {
		got := row(c.drCr, c.budget, c.debit, c.credit)
		if got.Status != c.wantStatus {
			t.Errorf("%s was scored %s and not %s: %s",
				c.what, got.Status, c.wantStatus, c.consequence)
		}
		if !got.Actual.Equal(dec(c.wantActual)) {
			t.Errorf("%s produced an actual of %s and not %s — the comparison is against "+
				"the wrong figure, so every conclusion drawn from this row is wrong: %s",
				c.what, got.Actual.StringFixed(4), dec(c.wantActual).StringFixed(4), c.consequence)
		}
	}
}

// TestUnspentAndNettedToZeroAreNeverTheSameRow is the pair the header calls out
// explicitly, asserted as an INVARIANT rather than as two separate rows, because
// the tempting simplification is `if actual.IsZero() { UNSPENT }` and that one
// line silently merges them.
func TestUnspentAndNettedToZeroAreNeverTheSameRow(t *testing.T) {
	quiet := row("D", "500000", "0", "0")
	cancelled := row("D", "500000", "300000", "300000")

	if quiet.Actual.Equal(cancelled.Actual) && quiet.Status == cancelled.Status {
		t.Errorf("a ledger with NO postings and a ledger with 300000 of debit and 300000 of "+
			"credit both scored %s. Their nett is identically zero, so only the raw sides "+
			"tell them apart — collapsing them means a reversal, a refund or a wrong-side "+
			"posting is reported as an unused budget, and the money that moved is invisible "+
			"on the report entirely", quiet.Status)
	}
	if quiet.Status != BudgetUnspent {
		t.Errorf("a budget with nothing posted against it scored %s: an untouched budget "+
			"must say so", quiet.Status)
	}
	if cancelled.Status != BudgetNettedToZero {
		t.Errorf("a budget whose movement cancelled scored %s: the report must not claim "+
			"nothing happened when 300000 moved each way", cancelled.Status)
	}
	// And the raw sides must survive to the wire, or a client cannot tell either.
	if cancelled.ActualDebit.IsZero() || cancelled.ActualCredit.IsZero() {
		t.Error("the raw debit and credit were dropped from a netted-to-zero row: they are " +
			"the ONLY evidence that anything happened, so a client would have no way to " +
			"distinguish this row from an untouched budget")
	}
}

// ---------------------------------------------------------------------------
// ⛔ FAILURE MODE 3 — NOTHING IS EVER DIVIDED BY A ZERO BUDGET.
// ---------------------------------------------------------------------------

func TestAZeroBudgetNeverYieldsAPercentage(t *testing.T) {
	// The real path: unbudgetedRow, which is how a zero budget reaches a row at
	// all.
	r := unbudgetedRow(BudgetDimensionLedger, uuid.New(), "Staff Welfare", "EXPENSE",
		uuid.Nil, "", dec("83000"), dec("0"), BudgetBasisNett)

	if r.VariancePercent != nil {
		t.Errorf("an actual with no budget produced a variance percentage of %s. There is "+
			"nothing for it to be a percentage OF: any number here — 0, 100, or an "+
			"infinity — is a figure a reader will believe and act on. It must be null so "+
			"the screen is forced to say \"no budget set\"", r.VariancePercent.String())
	}
	if r.Status != BudgetUnbudgeted {
		t.Errorf("an actual with no budget scored %s and not %s: spend the plan never "+
			"mentioned would read as spend that BLEW a plan, which points a reader at the "+
			"wrong problem entirely", r.Status, BudgetUnbudgeted)
	}
	if r.Budget.IsZero() && r.Variance.Equal(r.Actual) {
		// Correct: with no budget the whole actual is the variance.
	} else {
		t.Errorf("an unbudgeted row's variance (%s) is not its whole actual (%s): with no "+
			"budget there is nothing to subtract, and any other figure here is invented",
			r.Variance.StringFixed(4), r.Actual.StringFixed(4))
	}
	if !strings.Contains(strings.ToLower(strings.Join(r.Notes, " ")), "not 100% over") {
		t.Error("an unbudgeted row does not carry the note saying it is NOT 100% over " +
			"budget. The distinction is the whole point of the status, and a caveat that " +
			"does not travel with the number is a caveat nobody reads")
	}
}

// TestScoreBudgetRowItselfNeverDividesByZero drives scoreBudgetRow DIRECTLY with
// a zero budget, and it exists because the test above did not.
//
// ⛔⛔ READ THIS BEFORE DELETING IT AS A DUPLICATE. TestAZeroBudgetNeverYieldsA-
// Percentage above goes through unbudgetedRow, which sets VariancePercent nil by
// construction and NEVER CALLS scoreBudgetRow. Mutation-proving the guard found
// exactly that: replacing scoreBudgetRow's `if !row.Budget.IsZero()` with a
// divide-by-one fallback left the whole suite GREEN. The property was asserted on
// a path that could not execute the code being protected — the same shape as the
// JWKS security test that passed while testing nothing (§7).
//
// The guard is defence in depth: 0085's budget_line_amount_positive means no
// stored line can reach here with a zero amount today. That is exactly why it
// needs its own test — an unreachable branch with no coverage is a branch that
// will be quietly deleted, and the next caller who lands on it gets a percentage
// of nothing.
func TestScoreBudgetRowItselfNeverDividesByZero(t *testing.T) {
	r := BudgetVarianceRow{
		Dimension: BudgetDimensionLedger, TargetName: "Travel", Nature: "EXPENSE",
		Basis: BudgetBasisNett, DrCr: "D",
		Budget:      decimal.Zero,
		ActualDebit: dec("83000"), ActualCredit: decimal.Zero,
	}
	scoreBudgetRow(&r)

	if r.VariancePercent != nil {
		t.Errorf("scoreBudgetRow produced a variance percentage of %s against a budget of "+
			"ZERO. There is nothing for it to be a percentage of, and any number here — 0, "+
			"100, or the result of dividing by a substituted 1 — is a figure a reader will "+
			"believe and act on. It must stay nil so the wire carries null and the screen "+
			"is forced to say \"no budget set\"", r.VariancePercent.String())
	}
	if r.Status != BudgetUnbudgeted {
		t.Errorf("scoreBudgetRow scored a zero-budget row %s rather than %s: a target with "+
			"no budget would be reported as having missed or blown one", r.Status, BudgetUnbudgeted)
	}
}

func TestAPercentageIsStatedWhereItMeansSomething(t *testing.T) {
	r := row("D", "500000", "600000", "0")
	if r.VariancePercent == nil {
		t.Fatal("a real budget with a real actual produced no variance percentage: the " +
			"figure a reader actually looks at first is missing, and the report is " +
			"suppressing something it knows")
	}
	// 100000 over 500000 = +20%.
	if !r.VariancePercent.Equal(dec("20")) {
		t.Errorf("100000 over a budget of 500000 was reported as %s%% rather than 20%%: an "+
			"overspend is being understated or overstated by a factor a reader cannot see",
			r.VariancePercent.String())
	}
}

// ---------------------------------------------------------------------------
// ⛔⛔ FAILURE MODE 4 — A POSITION AND A FLOW ARE NEVER ADDED.
// ---------------------------------------------------------------------------

func TestAPlanOnOneBasisGetsAnAddableGrandTotal(t *testing.T) {
	out := BudgetVariance{
		FromDate: "2026-04-01", ToDate: "2027-03-31",
		StatusCounts:     map[string]int{},
		TotalBudget:      decimal.Zero,
		TotalActual:      decimal.Zero,
		TotalVariance:    decimal.Zero,
		UnbudgetedActual: decimal.Zero,
		Rows: []BudgetVarianceRow{
			row("D", "500000", "300000", "0"),
			row("D", "200000", "250000", "0"),
		},
	}
	finaliseBudgetVariance(&out)

	if !out.TotalsAreAddable {
		t.Error("a plan whose every line is measured on NETT TRANSACTIONS was refused a " +
			"grand total. Suppressing a total that IS correct pushes the reader to add the " +
			"column up by hand, which is the same arithmetic with no caveat attached")
	}
	if !out.TotalBudget.Equal(dec("700000")) || !out.TotalActual.Equal(dec("550000")) {
		t.Errorf("single-basis totals came out budget=%s actual=%s rather than 700000 / "+
			"550000: the footer disagrees with the rows above it, and an accountant "+
			"reconciles to the footer",
			out.TotalBudget.StringFixed(4), out.TotalActual.StringFixed(4))
	}
	if len(out.Totals) != 1 {
		t.Errorf("%d per-basis subtotals for a single-basis plan: there is one basis, so "+
			"there is one subtotal", len(out.Totals))
	}
}

func TestAMixedBasisPlanIsREFUSEDAGrandTotal(t *testing.T) {
	closing := row("D", "4000000", "3500000", "0")
	closing.Basis = BudgetBasisClosing
	nett := row("D", "500000", "300000", "0")
	nett.Basis = BudgetBasisNett

	out := BudgetVariance{
		FromDate: "2026-04-01", ToDate: "2027-03-31",
		StatusCounts:     map[string]int{},
		TotalBudget:      decimal.Zero,
		TotalActual:      decimal.Zero,
		TotalVariance:    decimal.Zero,
		UnbudgetedActual: decimal.Zero,
		Rows:             []BudgetVarianceRow{closing, nett},
	}
	finaliseBudgetVariance(&out)

	if out.TotalsAreAddable {
		t.Error("a plan mixing a CLOSING BALANCE line with a NETT TRANSACTIONS line was " +
			"declared addable. A closing balance is a POSITION as at a date and nett " +
			"transactions are MOVEMENT within a period: their sum is not a quantity at " +
			"all. A footer showing it would be the single most believable wrong number on " +
			"the page — it foots, it ties to nothing, and no reader can see why")
	}
	if !out.TotalBudget.IsZero() || !out.TotalActual.IsZero() || !out.TotalVariance.IsZero() {
		t.Errorf("a mixed-basis plan still published grand totals (budget=%s actual=%s "+
			"variance=%s). The flag alone is not enough: a client that ignores it must "+
			"find zeros, not a plausible figure",
			out.TotalBudget.StringFixed(4), out.TotalActual.StringFixed(4),
			out.TotalVariance.StringFixed(4))
	}
	if len(out.Totals) != 2 {
		t.Fatalf("%d per-basis subtotals for a plan spanning both bases: the subtotals are "+
			"the ONLY totals this report may state here, so losing one leaves the reader "+
			"with nothing", len(out.Totals))
	}
	// The subtotals must still be right — they are the replacement for the total.
	byBasis := map[string]BudgetBasisTotal{}
	for _, tt := range out.Totals {
		byBasis[tt.Basis] = tt
	}
	if !byBasis[BudgetBasisClosing].Budget.Equal(dec("4000000")) {
		t.Errorf("the CLOSING_BALANCE subtotal is %s rather than 4000000",
			byBasis[BudgetBasisClosing].Budget.StringFixed(4))
	}
	if !byBasis[BudgetBasisNett].Budget.Equal(dec("500000")) {
		t.Errorf("the NETT_TRANSACTIONS subtotal is %s rather than 500000",
			byBasis[BudgetBasisNett].Budget.StringFixed(4))
	}
	// And the reader must be TOLD, on the artefact.
	joined := strings.Join(out.Notes, " ")
	if !strings.Contains(joined, "NO GRAND TOTAL") {
		t.Error("the mixed-basis report does not say on its own face that there is no grand " +
			"total. A suppressed number with no explanation reads as a bug, and the next " +
			"person to \"fix\" it will add the two bases together")
	}
}

// ---------------------------------------------------------------------------
// THE CAVEATS THAT MUST ALWAYS TRAVEL
// ---------------------------------------------------------------------------

func TestThePeriodAndInterpretationCaveatsAlwaysTravel(t *testing.T) {
	out := BudgetVariance{
		FromDate: "2026-04-01", ToDate: "2027-03-31",
		StatusCounts:     map[string]int{},
		TotalBudget:      decimal.Zero,
		TotalActual:      decimal.Zero,
		TotalVariance:    decimal.Zero,
		UnbudgetedActual: decimal.Zero,
		Rows:             []BudgetVarianceRow{row("D", "500000", "300000", "0")},
	}
	finaliseBudgetVariance(&out)
	joined := strings.Join(out.Notes, " ")

	if !strings.Contains(joined, "not apportioned") {
		t.Error("the report does not state that the budget column is the WHOLE period and " +
			"is not apportioned. A reader looking at a year's plan in month four will " +
			"otherwise read a 75%-unspent budget as a crisis, and the report will have " +
			"caused it")
	}
	if !strings.Contains(joined, "2026-04-01") || !strings.Contains(joined, "2027-03-31") {
		t.Error("the period caveat does not NAME the period it refers to, which is the one " +
			"thing that makes it actionable")
	}
	if !strings.Contains(joined, "good and bad") {
		t.Error("the report does not state that OVER and UNDER are arithmetic rather than " +
			"judgement. Without it a screen colours every OVER row red — including sales " +
			"ahead of plan, which is the best news on the page")
	}
}

func TestAnEmptyPlanSaysSoRatherThanLookingMet(t *testing.T) {
	out := BudgetVariance{
		FromDate: "2026-04-01", ToDate: "2027-03-31",
		StatusCounts:     map[string]int{},
		TotalBudget:      decimal.Zero,
		TotalActual:      decimal.Zero,
		TotalVariance:    decimal.Zero,
		UnbudgetedActual: decimal.Zero,
		Unbudgeted: []BudgetVarianceRow{
			unbudgetedRow(BudgetDimensionLedger, uuid.New(), "Travel", "EXPENSE",
				uuid.Nil, "", dec("83000"), dec("0"), BudgetBasisNett),
		},
	}
	finaliseBudgetVariance(&out)
	joined := strings.Join(out.Notes, " ")

	if !strings.Contains(joined, "no lines at this grain") {
		t.Error("a budget with no lines produced a report that does not say so. A page of " +
			"unbudgeted actuals with an empty budget column reads as \"nothing was " +
			"planned\", which is indistinguishable from \"the plan was never keyed\" — and " +
			"the second is a data-entry emergency")
	}
	if out.UnbudgetedCount != 1 || !out.UnbudgetedActual.Equal(dec("83000")) {
		t.Errorf("the unbudgeted column came out count=%d total=%s rather than 1 / 83000: "+
			"the spend nobody planned is the one figure on this page a finance person needs "+
			"to be exact", out.UnbudgetedCount, out.UnbudgetedActual.StringFixed(4))
	}
	// ⛔ And it must NOT have leaked into the plan's totals.
	if !out.TotalActual.IsZero() {
		t.Errorf("unbudgeted actuals (%s) leaked into the plan's total actual. They are the "+
			"plan's GAPS, not variances against it: folding them in makes a plan that was "+
			"never set look like a plan that was exceeded",
			out.TotalActual.StringFixed(4))
	}
}

// TestStatusCountsCoverEveryRow guards the summary a screen leads with. A count
// that silently omits a status is a headline that under-reports the problem.
func TestStatusCountsCoverEveryRow(t *testing.T) {
	out := BudgetVariance{
		FromDate: "2026-04-01", ToDate: "2027-03-31",
		StatusCounts:     map[string]int{},
		TotalBudget:      decimal.Zero,
		TotalActual:      decimal.Zero,
		TotalVariance:    decimal.Zero,
		UnbudgetedActual: decimal.Zero,
		Rows: []BudgetVarianceRow{
			row("D", "500000", "600000", "0"), // OVER
			row("D", "500000", "0", "0"),      // UNSPENT
			row("D", "500000", "500000", "0"), // ON_TARGET
		},
		Unbudgeted: []BudgetVarianceRow{
			unbudgetedRow(BudgetDimensionLedger, uuid.New(), "Travel", "EXPENSE",
				uuid.Nil, "", dec("83000"), dec("0"), BudgetBasisNett),
		},
	}
	finaliseBudgetVariance(&out)

	total := 0
	for _, n := range out.StatusCounts {
		total += n
	}
	if total != len(out.Rows)+len(out.Unbudgeted) {
		t.Errorf("the status counts add to %d over %d rows and %d unbudgeted. A headline "+
			"that does not count every row under-reports how much of the plan is in "+
			"trouble, and it is the only figure most readers will look at",
			total, len(out.Rows), len(out.Unbudgeted))
	}
	if out.StatusCounts[BudgetUnspent] != 1 || out.StatusCounts[BudgetOver] != 1 ||
		out.StatusCounts[BudgetUnbudgeted] != 1 {
		t.Errorf("status counts came out %v: each of OVER, UNSPENT and UNBUDGETED must be "+
			"countable on its own, because they send a reader to three different places",
			out.StatusCounts)
	}
}

// ---------------------------------------------------------------------------
// THE UNBUDGETED SWEEP'S BASIS — stated rather than implicit.
// ---------------------------------------------------------------------------

func TestTheUnbudgetedSweepMeasuresMovementUnlessThePlanIsAllPositions(t *testing.T) {
	nettPlan := []budgetLine{{Basis: BudgetBasisNett}}
	closingPlan := []budgetLine{{Basis: BudgetBasisClosing}}
	mixedPlan := []budgetLine{{Basis: BudgetBasisClosing}, {Basis: BudgetBasisNett}}

	if got := unbudgetedBasis(nil); got != BudgetBasisNett {
		t.Errorf("an EMPTY plan sweeps unbudgeted actuals on %s. It must be %s: with no "+
			"lines at all this report is nothing BUT the unbudgeted list, and measuring it "+
			"as a closing position would list every ledger carrying an opening balance from "+
			"before the books began", got, BudgetBasisNett)
	}
	if got := unbudgetedBasis(nettPlan); got != BudgetBasisNett {
		t.Errorf("a nett-transactions plan sweeps on %s rather than %s", got, BudgetBasisNett)
	}
	if got := unbudgetedBasis(mixedPlan); got != BudgetBasisNett {
		t.Errorf("a MIXED plan sweeps on %s. \"Spend nobody planned\" is a question about "+
			"MOVEMENT, and a closing-position sweep would bury the handful of rows that "+
			"matter under every ledger with an opening balance", got)
	}
	if got := unbudgetedBasis(closingPlan); got != BudgetBasisClosing {
		t.Errorf("a plan made entirely of closing-balance lines sweeps on %s. It follows "+
			"the plan so the unbudgeted column is measured the same way as the budgeted "+
			"one — otherwise the two halves of one page answer different questions", got)
	}
}

// ---------------------------------------------------------------------------
// GROUP ROLL-UP — the walk bounds, which are the difference between a wrong
// number and no report at all.
// ---------------------------------------------------------------------------

// TestTheGroupWalkSurvivesAParentCycle is the test that must never be deleted.
// account_group.parent_id is a self-reference and no constraint in 0002 forbids a
// multi-hop cycle, so A-under-B-under-A is storable. Unbounded recursion on it
// does not produce a wrong figure — it hangs the request, which is how a report
// stops existing.
func TestTheGroupWalkSurvivesAParentCycle(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	nodes := map[uuid.UUID]*groupNode{
		a: {id: a, name: "A", parent: &b, children: []uuid.UUID{b}},
		b: {id: b, name: "B", parent: &a, children: []uuid.UUID{a}},
	}
	led := uuid.New()
	balances := map[uuid.UUID]ledgerBalance{
		led: {LedgerID: led, GroupID: a, Debit: dec("1000"), Credit: dec("0")},
	}

	// If the walk is unbounded this call never returns and the test times out —
	// which is itself the failure, reported by the harness.
	dr, cr := subtreeTotals(nodes, a, balances)
	if !dr.Equal(dec("1000")) || !cr.IsZero() {
		t.Errorf("a cyclic group tree rolled up to debit=%s credit=%s rather than 1000 / 0. "+
			"The cycle must not double-count either: visiting A twice would report 2000 of "+
			"spend against a budget, and the report would foot",
			dr.StringFixed(4), cr.StringFixed(4))
	}

	// And the coverage walk must terminate too, reporting UNCOVERED rather than
	// looping — the failure direction that SHOWS a figure.
	if groupIsCovered(nodes, a, map[uuid.UUID]bool{}) {
		t.Error("a ledger in a cyclic group chain was reported as covered by a budgeted " +
			"group when none is budgeted: its spend would vanish from the unbudgeted list, " +
			"which is the one place it could have been seen")
	}
}

func TestASubtreeRollUpIncludesDescendantsExactlyOnce(t *testing.T) {
	root, mid, leaf := uuid.New(), uuid.New(), uuid.New()
	nodes := map[uuid.UUID]*groupNode{
		root: {id: root, name: "Expenses", children: []uuid.UUID{mid}},
		mid:  {id: mid, name: "Admin", parent: &root, children: []uuid.UUID{leaf}},
		leaf: {id: leaf, name: "Travel", parent: &mid},
	}
	l1, l2, outside := uuid.New(), uuid.New(), uuid.New()
	balances := map[uuid.UUID]ledgerBalance{
		l1:      {LedgerID: l1, GroupID: mid, Debit: dec("100"), Credit: dec("0")},
		l2:      {LedgerID: l2, GroupID: leaf, Debit: dec("250"), Credit: dec("30")},
		outside: {LedgerID: outside, GroupID: uuid.New(), Debit: dec("9999"), Credit: dec("0")},
	}

	dr, cr := subtreeTotals(nodes, root, balances)
	if !dr.Equal(dec("350")) || !cr.Equal(dec("30")) {
		t.Errorf("a three-level group rolled up to debit=%s credit=%s rather than 350 / 30. "+
			"A roll-up that misses a descendant reports a group as under budget when it is "+
			"over, and one that includes a ledger from OUTSIDE the subtree reports spend "+
			"against a budget that never covered it",
			dr.StringFixed(4), cr.StringFixed(4))
	}

	// The coverage rule: a ledger deep under a budgeted ancestor is covered.
	budgeted := map[uuid.UUID]bool{root: true}
	if !groupIsCovered(nodes, leaf, budgeted) {
		t.Error("a ledger three levels under a budgeted group was reported as UNBUDGETED. " +
			"It would appear in both the budgeted roll-up AND the unbudgeted list, so the " +
			"same money would be counted twice on one page — which is the overlap the " +
			"leaf-level rule exists to prevent")
	}
	if groupIsCovered(nodes, uuid.New(), budgeted) {
		t.Error("a ledger in a group that is not in the tree at all was reported as covered " +
			"by a budgeted group: spend outside every budget would silently disappear from " +
			"the coverage list")
	}
}
