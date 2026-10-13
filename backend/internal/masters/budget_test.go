package masters

// THE PURE HALF OF THE BUDGET MASTER. NO DATABASE, ON PURPOSE.
//
// Two rules in budget.go are the ONLY thing standing between an operator and a
// confidently wrong variance report, and both are pure functions of a resolved
// target — so both are tested here rather than behind Postgres:
//
//  1. THE SIDE DERIVATION. A budget stored on the wrong side makes the report
//     compare a plan against a figure oriented the other way, so a business that
//     overspent by 40% reads as one that spent nothing. Nothing about that is an
//     error: it foots, it renders, and it is wrong.
//  2. THE COST-CENTRE BASIS REFUSAL. Migration 0085 carries the CHECK, but the
//     database refuses by CONSTRAINT NAME and the operator's question is "why
//     not". These tests pin the sentence as well as the refusal.
//
// ⛔ EVERY FAILURE MESSAGE NAMES THE CONSEQUENCE, not `want X got Y`.

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func amount(s string) decimal.Decimal {
	d, err := decimal.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return d
}

// ---------------------------------------------------------------------------
// ⛔ THE SIDE DERIVATION
// ---------------------------------------------------------------------------

// TestAnOmittedSideTakesTheTargetsNaturalSide is the ordinary path: an operator
// budgeting Travel or Sales never types a debit/credit indicator, and the nature
// of the group decides it.
func TestAnOmittedSideTakesTheTargetsNaturalSide(t *testing.T) {
	cases := []struct {
		nature, want string
		why          string
	}{
		{"EXPENSE", "D", "spend is a debit; budgeting Travel on the credit side would " +
			"report every rupee spent as a NEGATIVE actual, so a department 40% over " +
			"budget would read as one that spent nothing"},
		{"ASSET", "D", "what a business owns is a debit balance; the wrong side would " +
			"invert a debtors ceiling"},
		{"INCOME", "C", "revenue is a credit; budgeting Sales on the debit side would " +
			"turn every invoice raised into a negative actual and report a record " +
			"quarter as a total collapse"},
		{"LIABILITY", "C", "what a business owes is a credit balance"},
	}
	for _, c := range cases {
		got, err := validateBudgetLine(BudgetLineInput{
			Dimension: DimensionLedger, Basis: BasisNettTransactions,
			DrCr: "", Amount: amount("500000"),
		}, budgetTarget{Name: "Travel", Nature: c.nature})
		if err != nil {
			t.Fatalf("a %s ledger with no dr_cr was refused (%v). Omitting the side is the "+
				"ORDINARY case — an operator does not know what a debit is", c.nature, err)
		}
		if got.DrCr != c.want {
			t.Errorf("a %s budget with no stated side was stored as %q rather than %q: %s",
				c.nature, got.DrCr, c.want, c.why)
		}
	}
}

// TestAnExplicitContraSideIsAllowed — the reason dr_cr is STORED rather than
// derived at read time. A bank account budgeted to sit overdrawn is a credit
// expectation on an ASSET ledger, and it is a real plan.
func TestAnExplicitContraSideIsAllowed(t *testing.T) {
	got, err := validateBudgetLine(BudgetLineInput{
		Dimension: DimensionLedger, Basis: BasisClosingBalance,
		DrCr: "C", Amount: amount("250000"),
	}, budgetTarget{Name: "Overdraft A/c", Nature: "ASSET"})
	if err != nil {
		t.Fatalf("a deliberate contra expectation was refused (%v). Budgeting a bank "+
			"account to sit overdrawn is an ordinary plan, and refusing it would force the "+
			"operator to misfile the ledger to express it", err)
	}
	if got.DrCr != "C" {
		t.Errorf("an explicitly stated credit expectation on an ASSET ledger was overwritten "+
			"with %q. Deriving over the top of an explicit choice means the stored column "+
			"can never disagree with nature — which makes storing it pointless and makes "+
			"the operator's stated intent unrecordable", got.DrCr)
	}
}

// ---------------------------------------------------------------------------
// ⛔⛔ THE COST-CENTRE BASIS REFUSAL
// ---------------------------------------------------------------------------

func TestACostCentreCannotBeBudgetedOnAClosingBalance(t *testing.T) {
	_, err := validateBudgetLine(BudgetLineInput{
		Dimension: DimensionCostCentre, Basis: BasisClosingBalance,
		DrCr: "D", Amount: amount("500000"),
	}, budgetTarget{Name: "Mumbai", CategoryName: "Branch"})

	if err == nil {
		t.Fatal("a cost centre was accepted on a CLOSING_BALANCE basis. A cost centre holds " +
			"no position — only allocations against dated postings — so its \"closing " +
			"balance\" is every rupee ever booked to that department since the books began. " +
			"A year's budget measured against it looks almost entirely unspent in year one " +
			"and several times overspent in year four, and every one of those figures foots")
	}
	if !errors.Is(err, ErrInvalidBudget) {
		t.Errorf("the refusal is not an ErrInvalidBudget (%v), so the HTTP seam answers 500 "+
			"rather than 400 and the operator is told the server broke rather than what to "+
			"fix", err)
	}
	// ⛔ THE SENTENCE MATTERS AS MUCH AS THE REFUSAL. The database refuses this too,
	// by constraint name; this layer exists to say WHY.
	msg := err.Error()
	if !strings.Contains(msg, "holds no opening balance") || !strings.Contains(msg, "Mumbai") {
		t.Errorf("the refusal does not explain itself or name the centre: %q. An operator "+
			"told only that a combination is invalid will try the other basis at random, "+
			"and the other basis is the one that silently answers the wrong question", msg)
	}
	if !strings.Contains(msg, BasisNettTransactions) {
		t.Errorf("the refusal does not name the basis that WOULD work: %q. A refusal that "+
			"does not offer the alternative is a dead end", msg)
	}
}

func TestACostCentreOnNettTransactionsIsAccepted(t *testing.T) {
	got, err := validateBudgetLine(BudgetLineInput{
		Dimension: DimensionCostCentre, Basis: BasisNettTransactions,
		DrCr: "D", Amount: amount("500000"),
	}, budgetTarget{Name: "Mumbai", CategoryName: "Branch"})
	if err != nil {
		t.Fatalf("the ONLY legal cost-centre basis was refused (%v): the cost-centre arm of "+
			"T11 would not exist at all", err)
	}
	if got.Basis != BasisNettTransactions {
		t.Errorf("the basis was rewritten to %q", got.Basis)
	}
}

// TestACostCentreMustStateItsSide — a cost centre has no nature, so there is
// nothing to derive from and silence must be a refusal rather than a guess.
func TestACostCentreMustStateItsSide(t *testing.T) {
	_, err := validateBudgetLine(BudgetLineInput{
		Dimension: DimensionCostCentre, Basis: BasisNettTransactions,
		DrCr: "", Amount: amount("500000"),
	}, budgetTarget{Name: "Mumbai", CategoryName: "Branch"})

	if err == nil {
		t.Fatal("a cost centre budget with no side was accepted. A cost centre is an axis " +
			"of analysis, not an account: it has no nature, so a default would be a coin " +
			"flip — and the wrong face turns every rupee of departmental cost into a " +
			"negative actual against the plan")
	}
	if !strings.Contains(err.Error(), "Mumbai") || !strings.Contains(err.Error(), "\"D\"") {
		t.Errorf("the refusal neither names the centre nor tells the operator that a cost "+
			"budget is a debit: %q", err.Error())
	}
}

// ---------------------------------------------------------------------------
// A BUDGET OF ZERO IS THE ABSENCE OF A BUDGET
// ---------------------------------------------------------------------------

func TestAZeroBudgetIsRefusedRatherThanStored(t *testing.T) {
	for _, bad := range []string{"0", "-1", "-500000"} {
		_, err := validateBudgetLine(BudgetLineInput{
			Dimension: DimensionLedger, Basis: BasisNettTransactions,
			Amount: amount(bad),
		}, budgetTarget{Name: "Travel", Nature: "EXPENSE"})
		if err == nil {
			t.Errorf("an amount of %s was accepted as a budget. A budget of zero is the "+
				"ABSENCE of a budget and the variance report already has a distinct answer "+
				"for that (UNBUDGETED); two ways to spell one thing is how one of them stops "+
				"being read. A negative one is a magnitude with a sign, and the side is "+
				"already carried by dr_cr", bad)
			continue
		}
		if !errors.Is(err, ErrInvalidBudget) {
			t.Errorf("an amount of %s was refused with the wrong sentinel (%v)", bad, err)
		}
	}
}

// ---------------------------------------------------------------------------
// THE PERIOD
// ---------------------------------------------------------------------------

func TestAReversedBudgetPeriodIsRefused(t *testing.T) {
	_, _, err := validateBudgetPeriod("2027-03-31", "2026-04-01")
	if err == nil {
		t.Fatal("a budget whose period ends before it begins was accepted. Every " +
			"nett-transactions line on it would match no movement at all and the whole plan " +
			"would report as UNSPENT — which reads as a business fact (\"we planned and " +
			"spent nothing\") rather than as the typo it is")
	}
	if !strings.Contains(err.Error(), "unspent") {
		t.Errorf("the refusal does not name what would have happened: %q", err.Error())
	}
}

func TestASingleDayBudgetIsLegal(t *testing.T) {
	from, to, err := validateBudgetPeriod("2026-04-01", "2026-04-01")
	if err != nil {
		t.Fatalf("a one-day budget was refused (%v). Both bounds are inclusive everywhere "+
			"else in this codebase and a single-day plan is a legitimate, if unusual, "+
			"thing to key", err)
	}
	if from != "2026-04-01" || to != "2026-04-01" {
		t.Errorf("a one-day period came back as %s..%s", from, to)
	}
}

func TestABudgetNeedsBothBounds(t *testing.T) {
	for _, c := range [][2]string{{"", "2027-03-31"}, {"2026-04-01", ""}, {"", ""}} {
		if _, _, err := validateBudgetPeriod(c[0], c[1]); err == nil {
			t.Errorf("a budget with from=%q to=%q was accepted. Unlike every REPORT period "+
				"in this codebase, a budget's bounds are not optional: a plan for no stated "+
				"period cannot be compared against anything, and the closing-balance basis "+
				"would have no date to be measured as at", c[0], c[1])
		}
	}
}

// ---------------------------------------------------------------------------
// NAMES
// ---------------------------------------------------------------------------

// TestTwoBudgetsCannotShareAName pins the scope budget_name_unique enforces: the
// whole COMPANY, compared ignoring case and surrounding spaces.
func TestTwoBudgetsCannotShareAName(t *testing.T) {
	mine := uuid.New()
	existing := map[string]uuid.UUID{costNameKey("FY 2026-27 Plan"): mine}

	for _, clash := range []string{"FY 2026-27 Plan", "fy 2026-27 plan", "  FY 2026-27 Plan  "} {
		_, err := validateBudgetName(clash, existing, uuid.Nil)
		if err == nil {
			t.Errorf("a second budget called %q was accepted alongside \"FY 2026-27 Plan\". "+
				"Two plans with one name is how half the lines end up on each and both look "+
				"complete — and the operator picking one from a list cannot tell them apart",
				clash)
			continue
		}
		if !errors.Is(err, ErrBudgetNameTaken) {
			t.Errorf("the clash on %q used the wrong sentinel (%v): ErrInvalidBudget maps to "+
				"400 and tells the operator their request was malformed, when in fact the "+
				"request was fine and the books already say otherwise", clash, err)
		}
	}

	// ⛔ AND THE ROW MAY KEEP ITS OWN NAME. Without the selfID exemption, renaming a
	// budget to correct its capitalisation would be refused as a clash with itself,
	// and the only way to fix a typo would be to delete the plan.
	if _, err := validateBudgetName("FY 2026-27 Plan", existing, mine); err != nil {
		t.Errorf("a budget was refused its OWN name on an alter (%v): correcting a typo "+
			"would mean deleting the plan and every figure on it", err)
	}
}

// TestTheStoredNameIsTheTrimmedForm — the index is on lower(btrim(name)), so a
// name stored with surrounding spaces would make btrim in the index a real
// operation and let Go accept what the database would refuse.
func TestTheStoredNameIsTheTrimmedForm(t *testing.T) {
	got, err := validateBudgetName("   Q1 Plan   ", map[string]uuid.UUID{}, uuid.Nil)
	if err != nil {
		t.Fatalf("a name with surrounding spaces was refused outright (%v)", err)
	}
	if got != "Q1 Plan" {
		t.Errorf("the stored name is %q rather than \"Q1 Plan\". budget_name_unique is on "+
			"lower(btrim(name)): storing the untrimmed form makes the Go check and the index "+
			"normalise differently, so Go would accept a name the database then refuses and "+
			"the operator would meet a raw constraint name", got)
	}
}

func TestABlankBudgetNameIsRefused(t *testing.T) {
	for _, blank := range []string{"", "   ", "\t"} {
		if _, err := validateBudgetName(blank, map[string]uuid.UUID{}, uuid.Nil); err == nil {
			t.Errorf("a blank budget name (%q) was accepted: a plan nobody can name is a "+
				"plan nobody can be asked about, and it is unpickable from a list", blank)
		}
	}
}

// TestAnUnknownDimensionOrBasisNamesTheAlternatives — a closed set of three and a
// closed set of two, so a refusal has no excuse for not listing them.
func TestAnUnknownDimensionOrBasisNamesTheAlternatives(t *testing.T) {
	_, err := validateBudgetLine(BudgetLineInput{
		Dimension: "DEPARTMENT", Basis: BasisNettTransactions, Amount: amount("1"),
	}, budgetTarget{Name: "X", Nature: "EXPENSE"})
	if err == nil {
		t.Fatal("an unknown dimension was accepted")
	}
	for _, d := range KnownBudgetDimensions {
		if !strings.Contains(err.Error(), d) {
			t.Errorf("the dimension refusal does not name %s as an option: %q", d, err.Error())
		}
	}

	_, err = validateBudgetLine(BudgetLineInput{
		Dimension: DimensionLedger, Basis: "OPENING", Amount: amount("1"),
	}, budgetTarget{Name: "X", Nature: "EXPENSE"})
	if err == nil {
		t.Fatal("an unknown basis was accepted")
	}
	// ⛔ THE BASIS REFUSAL MUST EXPLAIN THE TWO, not just list the tokens. They
	// answer different questions and the same figure means different things under
	// each, which is why 0085 gives the column no default.
	msg := err.Error()
	if !strings.Contains(msg, "POSITION") || !strings.Contains(msg, "MOVEMENT") {
		t.Errorf("the basis refusal lists the tokens without saying what they MEAN: %q. An "+
			"operator picking between two opaque strings will pick the first one, and half "+
			"the time the report then answers a question they did not ask", msg)
	}
}
