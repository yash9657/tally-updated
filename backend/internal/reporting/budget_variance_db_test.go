package reporting_test

// THE DB-BACKED HALF OF BUDGET VS ACTUAL (T11, migrations 0085/0086).
//
// Everything here needs Postgres, because everything here is about something the
// pure half CANNOT check:
//
//   - that CLOSING_BALANCE and NETT_TRANSACTIONS really are two different
//     queries against real vouchers, and differ by exactly the movement outside
//     the budget period. This is the single most consequential claim in T11: the
//     pure tests can prove the arithmetic given an actual, but only the database
//     can prove the ACTUAL was fetched the way the line asked for;
//   - that the GROUP arm rolls a real account_group subtree up through real
//     ledgers;
//   - that the cost-centre arm refuses without an axis, naming the ones that
//     exist;
//   - that RLS keeps one tenant's PLAN — which is sharper than their books,
//     because it states intent — out of another's report.
//
// It reuses setup(t) from reporting_test.go, which creates the company and
// registers testdb.Cleanup for it. THE SECOND COMPANY in the RLS test registers
// its own cleanup, immediately after it exists — internal/testhygiene fails the
// build over a company-creating test that does not, and testdb.Keep is banned.
//
// ===========================================================================
// WHY THE BUDGETS GO THROUGH internal/masters AND NOT THROUGH SQL
// ===========================================================================
// The vouchers go through internal/ledger and the budgets through
// internal/masters, both deliberately. A report test that seeded budget_line by
// hand could not catch the write path storing a dr_cr the report then reads the
// other way round — which is the defect the masters tests describe as "a business
// that overspent by 40% reads as one that spent nothing". Driving both ends means
// the side derivation is exercised end to end, once, by the two packages that
// have to agree about it.

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/internal/reporting"
)

// ---------------------------------------------------------------------------
// Fixture
// ---------------------------------------------------------------------------

// budgetFixture is the scenario every test below reads.
//
//	Ledgers, all under "Indirect Expenses" except sales:
//	  Travel Expenses      (EXPENSE)
//	  Stationery           (EXPENSE)   <-- never budgeted: the UNBUDGETED row
//	  Office Rent          (EXPENSE)   <-- budgeted, never posted: the UNSPENT row
//	  Domestic Sales       (INCOME)
//
//	2025-04-05  Travel      40,000 Dr   <-- BEFORE the budget period
//	2025-08-10  Travel   3,00,000 Dr
//	2025-08-12  Stationery  25,000 Dr
//	2025-09-01  Domestic Sales 5,00,000 Cr
//
//	Budget period on every plan below: 2025-07-01 .. 2026-03-31
//
// ⛔ THE 2025-04-05 POSTING IS THE WHOLE POINT OF THE FIXTURE. It is inside the
// financial year and OUTSIDE the budget period, which is the only arrangement
// under which the two bases give different answers — and therefore the only one
// that can prove the basis is being honoured rather than ignored.
type budgetFixture struct {
	travel, stationery, rent, sales, cash uuid.UUID
	indirectExpenses                      uuid.UUID
}

func budgetSetup(t *testing.T) (*db.Pool, context.Context, uuid.UUID, *masters.Service, budgetFixture) {
	t.Helper()
	pool, ctx, companyID, ids := setup(t)

	var f budgetFixture
	mk := func(name, group string) uuid.UUID {
		id, err := ids.CreateLedger(ctx, name, group)
		if err != nil {
			t.Fatalf("create ledger %s: %v", name, err)
		}
		return id
	}
	f.travel = mk("Travel Expenses", "Indirect Expenses")
	f.stationery = mk("Stationery", "Indirect Expenses")
	f.rent = mk("Office Rent", "Indirect Expenses")
	f.sales = mk("Domestic Sales", "Sales Accounts")

	cash, err := ids.LedgerID(ctx, "Cash")
	if err != nil {
		t.Fatalf("cash ledger: %v", err)
	}
	f.cash = cash
	f.indirectExpenses = budgetGroupID(t, pool, ctx, "Indirect Expenses")

	postJournal(t, pool, ctx, ids, "2025-04-05", f.travel, f.cash, "40000")
	postJournal(t, pool, ctx, ids, "2025-08-10", f.travel, f.cash, "300000")
	postJournal(t, pool, ctx, ids, "2025-08-12", f.stationery, f.cash, "25000")
	// Sales: Dr Cash, Cr Sales — the credit side, so an INCOME budget has
	// something to be measured against on its natural side.
	postJournal(t, pool, ctx, ids, "2025-09-01", f.cash, f.sales, "500000")

	return pool, ctx, companyID, masters.NewService(pool), f
}

// budgetGroupID reads an account_group id by name. Through the pool rather than
// through identity, which exposes no group lookup — and reading it rather than
// hardcoding it because the seeded chart's ids are per company.
func budgetGroupID(t *testing.T, pool *db.Pool, ctx context.Context, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id FROM account_group WHERE name = $1`, name).Scan(&id)
	}); err != nil {
		t.Fatalf("group %s: %v", name, err)
	}
	return id
}

func postJournal(
	t *testing.T, pool *db.Pool, ctx context.Context, ids *identity.Service,
	date string, debit, credit uuid.UUID, amount string,
) uuid.UUID {
	t.Helper()
	vt, err := ids.VoucherTypeID(ctx, "Journal")
	if err != nil {
		t.Fatalf("journal vt: %v", err)
	}
	res, err := ledger.NewService(pool).PostVoucher(ctx, ledger.VoucherInput{
		Class:         ledger.Journal,
		VoucherTypeID: vt,
		VoucherDate:   date,
		CreatedBy:     "budget-variance-test",
		Postings: []ledger.PostingInput{
			{LedgerID: debit, DrCr: ledger.Debit, Amount: dec(amount)},
			{LedgerID: credit, DrCr: ledger.Credit, Amount: dec(amount)},
		},
	})
	if err != nil {
		t.Fatalf("post journal %s on %s: %v", amount, date, err)
	}
	return res.VoucherID
}

// newPlan creates a budget over the fixture's window and returns its id.
func newPlan(t *testing.T, ms *masters.Service, ctx context.Context, name string) uuid.UUID {
	t.Helper()
	id, err := ms.CreateBudget(ctx, masters.BudgetInput{
		Name: name, FromDate: "2025-07-01", ToDate: "2026-03-31",
	}, "budget-variance-test")
	if err != nil {
		t.Fatalf("create budget %s: %v", name, err)
	}
	return id
}

func addLine(
	t *testing.T, ms *masters.Service, ctx context.Context, budgetID uuid.UUID,
	dimension string, target uuid.UUID, basis, drCr, amount string,
) uuid.UUID {
	t.Helper()
	id, err := ms.AddBudgetLine(ctx, budgetID, masters.BudgetLineInput{
		Dimension: dimension, TargetID: target, Basis: basis,
		DrCr: drCr, Amount: dec(amount),
	}, "budget-variance-test")
	if err != nil {
		t.Fatalf("add %s line: %v", dimension, err)
	}
	return id
}

func findRow(rows []reporting.BudgetVarianceRow, target uuid.UUID) (reporting.BudgetVarianceRow, bool) {
	for _, r := range rows {
		if r.TargetID == target {
			return r, true
		}
	}
	return reporting.BudgetVarianceRow{}, false
}

// ---------------------------------------------------------------------------
// ⛔⛔ THE CLAIM T11 LIVES OR DIES ON: THE TWO BASES ARE TWO DIFFERENT QUERIES.
// ---------------------------------------------------------------------------

// TestTheTwoBasesGiveDifferentActualsForTheSameLedger is the most important test
// in this file.
//
// Travel has 40,000 posted INSIDE the financial year but BEFORE the budget period,
// and 3,00,000 inside it. So:
//
//	NETT_TRANSACTIONS over 2025-07-01..2026-03-31 = 3,00,000
//	CLOSING_BALANCE    as at 2026-03-31           = 3,40,000
//
// ⛔ IF THESE TWO EVER COME BACK EQUAL, `basis` IS BEING IGNORED — and that is a
// silent failure of the worst kind, because BOTH numbers are real and correctly
// footed and only one of them answers the question the operator asked. A business
// that set a spend cap would be reported against a position, and nothing on the
// page would say so.
func TestTheTwoBasesGiveDifferentActualsForTheSameLedger(t *testing.T) {
	pool, ctx, companyID, ms, f := budgetSetup(t)
	svc := reporting.NewService(pool)

	// Two budgets, because 0085's budget_line_one_per_ledger allows only ONE line
	// per ledger per plan — deliberately, so a report cannot show one target twice.
	nettPlan := newPlan(t, ms, ctx, "Nett Plan")
	addLine(t, ms, ctx, nettPlan, masters.DimensionLedger, f.travel,
		masters.BasisNettTransactions, "", "500000")

	closingPlan := newPlan(t, ms, ctx, "Closing Plan")
	addLine(t, ms, ctx, closingPlan, masters.DimensionLedger, f.travel,
		masters.BasisClosingBalance, "", "500000")

	nett, err := svc.BudgetVarianceFor(ctx, companyID, nettPlan, uuid.Nil,
		reporting.BudgetDimensionLedger)
	if err != nil {
		t.Fatalf("nett report: %v", err)
	}
	closing, err := svc.BudgetVarianceFor(ctx, companyID, closingPlan, uuid.Nil,
		reporting.BudgetDimensionLedger)
	if err != nil {
		t.Fatalf("closing report: %v", err)
	}

	nettRow, ok := findRow(nett.Rows, f.travel)
	if !ok {
		t.Fatal("the nett plan's Travel line is missing from its own report")
	}
	closingRow, ok := findRow(closing.Rows, f.travel)
	if !ok {
		t.Fatal("the closing plan's Travel line is missing from its own report")
	}

	if !nettRow.Actual.Equal(dec("300000")) {
		t.Errorf("NETT_TRANSACTIONS reported Travel's actual as %s, want 300000. The 40,000 "+
			"posted on 2025-04-05 is OUTSIDE the budget period and must not be counted: "+
			"folding it in means a spend cap is measured against movement the plan never "+
			"covered, and the report overstates the overspend",
			nettRow.Actual.StringFixed(4))
	}
	if !closingRow.Actual.Equal(dec("340000")) {
		t.Errorf("CLOSING_BALANCE reported Travel's actual as %s, want 340000. A closing "+
			"balance is the POSITION as at the period end and must include every movement "+
			"up to that date, including the 40,000 from before the period began — dropping "+
			"it reports a debtors or spend ceiling as if the entity had no history",
			closingRow.Actual.StringFixed(4))
	}

	// ⛔ THE INVARIANT, STATED AS ITSELF. The pair of equalities above could both be
	// wrong in the same direction; this cannot.
	gap := closingRow.Actual.Sub(nettRow.Actual)
	if !gap.Equal(dec("40000")) {
		t.Errorf("the two bases differ by %s rather than by the 40,000 posted outside the "+
			"budget period. If this is ZERO then `basis` is being IGNORED and every "+
			"closing-balance line in the product is silently measured as movement (or the "+
			"reverse) — both real, correctly-footed, confidently wrong numbers",
			gap.StringFixed(4))
	}

	// And each report must NAME the basis it used, on its own face.
	if !strings.Contains(strings.Join(nett.Notes, " "), "NETT TRANSACTIONS") {
		t.Errorf("the nett report does not state its basis in its notes: %v. A variance "+
			"whose basis is unstated is a variance a reader cannot interpret", nett.Notes)
	}
	if !strings.Contains(strings.Join(closing.Notes, " "), "CLOSING BALANCE") {
		t.Errorf("the closing report does not state its basis in its notes: %v", closing.Notes)
	}
}

// ---------------------------------------------------------------------------
// UNSPENT AND UNBUDGETED, AGAINST REAL BOOKS
// ---------------------------------------------------------------------------

func TestUnspentAndUnbudgetedAppearAgainstRealBooks(t *testing.T) {
	pool, ctx, companyID, ms, f := budgetSetup(t)
	svc := reporting.NewService(pool)

	plan := newPlan(t, ms, ctx, "Coverage Plan")
	addLine(t, ms, ctx, plan, masters.DimensionLedger, f.travel,
		masters.BasisNettTransactions, "", "500000")
	// Office Rent is budgeted and NOTHING is ever posted to it.
	addLine(t, ms, ctx, plan, masters.DimensionLedger, f.rent,
		masters.BasisNettTransactions, "", "120000")

	rep, err := svc.BudgetVarianceFor(ctx, companyID, plan, uuid.Nil,
		reporting.BudgetDimensionLedger)
	if err != nil {
		t.Fatalf("report: %v", err)
	}

	// ⛔ UNSPENT, NOT UNDER. Nothing at all was posted to Office Rent.
	rentRow, ok := findRow(rep.Rows, f.rent)
	if !ok {
		t.Fatal("a budgeted ledger with no postings vanished from the report entirely. A " +
			"budget line that does not appear is a plan nobody is held to")
	}
	if rentRow.Status != reporting.BudgetUnspent {
		t.Errorf("Office Rent, budgeted 120000 with NOTHING posted, was scored %s rather "+
			"than %s. An untouched budget is a fact about the plan or about what is being "+
			"keyed, and reporting it as ordinary underspend hides both",
			rentRow.Status, reporting.BudgetUnspent)
	}

	// ⛔ UNBUDGETED, IN ITS OWN ARRAY. Stationery has 25,000 of real spend and no line.
	stat, ok := findRow(rep.Unbudgeted, f.stationery)
	if !ok {
		t.Fatalf("Stationery has 25000 of spend in the period and no budget line, and it is "+
			"absent from the unbudgeted list (%d entries). Money leaving the business that "+
			"no plan accounted for is the row a finance person most wants to see",
			len(rep.Unbudgeted))
	}
	if !stat.Actual.Equal(dec("25000")) {
		t.Errorf("the unbudgeted Stationery row reports %s rather than 25000",
			stat.Actual.StringFixed(4))
	}
	if stat.VariancePercent != nil {
		t.Errorf("the unbudgeted row carries a variance percentage of %s. There is no budget "+
			"for it to be a percentage of", stat.VariancePercent.String())
	}
	// ⛔ AND IT MUST NOT BE IN `Rows`, which is the plan.
	if _, leaked := findRow(rep.Rows, f.stationery); leaked {
		t.Error("an unbudgeted ledger appears in `Rows` as well as `Unbudgeted`. Rows is the " +
			"PLAN: a target with a budget of zero in it makes the budget column carry a " +
			"figure nobody set, and the plan's totals stop meaning anything")
	}
	// ⛔ AND IT MUST NOT BE IN THE PLAN'S TOTALS.
	if !rep.TotalBudget.Equal(dec("620000")) {
		t.Errorf("the plan's total budget is %s rather than 620000 (500000 + 120000)",
			rep.TotalBudget.StringFixed(4))
	}
	if !rep.TotalActual.Equal(dec("300000")) {
		t.Errorf("the plan's total actual is %s rather than 300000. The 25000 of unbudgeted "+
			"Stationery spend must stay OUT of it: folding it in makes a plan that never "+
			"covered that money look like a plan that was measured against it",
			rep.TotalActual.StringFixed(4))
	}
}

// ---------------------------------------------------------------------------
// THE GROUP ARM — a real account_group subtree over real ledgers
// ---------------------------------------------------------------------------

func TestAGroupBudgetRollsUpItsLedgers(t *testing.T) {
	pool, ctx, companyID, ms, f := budgetSetup(t)
	svc := reporting.NewService(pool)

	plan := newPlan(t, ms, ctx, "Group Plan")
	addLine(t, ms, ctx, plan, masters.DimensionGroup, f.indirectExpenses,
		masters.BasisNettTransactions, "", "400000")

	rep, err := svc.BudgetVarianceFor(ctx, companyID, plan, uuid.Nil,
		reporting.BudgetDimensionGroup)
	if err != nil {
		t.Fatalf("report: %v", err)
	}

	row, ok := findRow(rep.Rows, f.indirectExpenses)
	if !ok {
		t.Fatal("the budgeted group is missing from its own report")
	}
	// Travel 300000 + Stationery 25000 in the period. Office Rent contributes zero.
	if !row.Actual.Equal(dec("325000")) {
		t.Errorf("the Indirect Expenses group rolled up to %s rather than 325000 (Travel "+
			"300000 + Stationery 25000, both in the period). A roll-up that misses a ledger "+
			"reports a group as under budget when it is over; one that reaches outside the "+
			"subtree charges a group for spend its budget never covered",
			row.Actual.StringFixed(4))
	}
	// The side must have been derived from the group's own nature.
	if row.DrCr != "D" {
		t.Errorf("the Indirect Expenses budget was stored on the %q side. An EXPENSE group "+
			"is debit-natured, and the credit side would report every rupee spent as a "+
			"NEGATIVE actual", row.DrCr)
	}

	// ⛔ AND THE LEDGERS UNDER THE BUDGETED GROUP MUST NOT ALSO APPEAR AS UNBUDGETED.
	// They are covered by their ancestor; listing them too would put the same money
	// on the page twice.
	for _, led := range []uuid.UUID{f.travel, f.stationery} {
		if _, dup := findRow(rep.Unbudgeted, led); dup {
			t.Error("a ledger under the budgeted group is ALSO listed as unbudgeted. Its " +
				"spend is already inside the group's roll-up, so the same money appears " +
				"twice on one page — and a reader adding the two columns doubles it")
		}
	}
	// Domestic Sales is NOT under Indirect Expenses, so it IS uncovered and must
	// show. Its absence would mean the coverage walk reports everything as covered.
	if _, ok := findRow(rep.Unbudgeted, f.sales); !ok {
		t.Errorf("Domestic Sales has 500000 of movement and sits under no budgeted group, "+
			"yet it is absent from the unbudgeted list (%d entries). If the coverage walk "+
			"treats everything as covered, the report silently asserts complete coverage it "+
			"has not checked", len(rep.Unbudgeted))
	}
}

// ---------------------------------------------------------------------------
// THE COST-CENTRE ARM REFUSES WITHOUT AN AXIS
// ---------------------------------------------------------------------------

// TestTheCostCentreArmRefusesWithoutAnAxis pins the refusal that makes a
// cross-axis variance total UNREPRESENTABLE rather than merely discouraged.
//
// A cost category is an AXIS and the same money is allocated in full on every
// axis, so a report spanning two would total a multiple of the cost borne.
func TestTheCostCentreArmRefusesWithoutAnAxis(t *testing.T) {
	pool, ctx, companyID, ms, _ := budgetSetup(t)
	svc := reporting.NewService(pool)
	plan := newPlan(t, ms, ctx, "Centre Plan")

	_, err := svc.BudgetVarianceFor(ctx, companyID, plan, uuid.Nil,
		reporting.BudgetDimensionCostCentre)
	if err == nil {
		t.Fatal("a cost-centre variance report was served with NO axis. The same money is " +
			"allocated in full on every axis, so picking one silently renders a real, " +
			"correctly-footed report about an axis the operator did not choose — and summing " +
			"axes reports a multiple of the cost actually borne")
	}
	ax, ok := reporting.AsCostAxisError(err)
	if !ok {
		t.Fatalf("the refusal is not a CostAxisError (%v), so the HTTP seam cannot tell a "+
			"missing axis (400) from an unknown one (422) and answers 422 for both", err)
	}
	if !ax.Missing {
		t.Error("the refusal for an ABSENT axis is not flagged Missing, so the HTTP seam " +
			"answers 422 (a request about something absent) for what is actually a " +
			"malformed request")
	}

	// And a NAMED axis that does not exist must be a different refusal, or the seam
	// cannot distinguish them either.
	_, err = svc.BudgetVarianceFor(ctx, companyID, plan, uuid.New(),
		reporting.BudgetDimensionCostCentre)
	ax2, ok := reporting.AsCostAxisError(err)
	if !ok || ax2.Missing {
		t.Errorf("naming an axis these books do not contain produced %v, which is not a "+
			"non-Missing CostAxisError. An empty report for a nonexistent axis reads as "+
			"\"nothing was allocated\", which is a different and reassuring claim", err)
	}
}

// TestAnUnknownDimensionIsRefusedNamingTheThree — the grain is a closed set, so
// there is no excuse for a refusal that does not list it.
func TestAnUnknownDimensionIsRefusedNamingTheThree(t *testing.T) {
	pool, ctx, companyID, ms, _ := budgetSetup(t)
	svc := reporting.NewService(pool)
	plan := newPlan(t, ms, ctx, "Dim Plan")

	for _, bad := range []string{"", "DEPARTMENT", "ledger "} {
		_, err := svc.BudgetVarianceFor(ctx, companyID, plan, uuid.Nil, bad)
		if bad == "ledger " {
			// Trimmed and upper-cased, so this one is LEGAL — a caller's stray space
			// must not be a refusal.
			if err != nil {
				t.Errorf("dimension %q was refused (%v): it is a legal grain with "+
					"surrounding whitespace, and refusing it makes the API brittle for no "+
					"safety gain", bad, err)
			}
			continue
		}
		de, ok := reporting.AsBudgetDimensionError(err)
		if !ok {
			t.Errorf("dimension %q produced %v rather than a BudgetDimensionError", bad, err)
			continue
		}
		for _, d := range reporting.KnownBudgetDimensions {
			if !strings.Contains(de.Error(), d) {
				t.Errorf("the refusal for %q does not name %s as an option: %q",
					bad, d, de.Error())
			}
		}
	}
}

// TestAnUnknownBudgetIsRefusedNamingTheOnesThatExist — an empty report for a plan
// that does not exist reads as "nothing was planned and nothing was spent", which
// is a real and reassuring claim about a company and would be a lie.
func TestAnUnknownBudgetIsRefusedNamingTheOnesThatExist(t *testing.T) {
	pool, ctx, companyID, ms, _ := budgetSetup(t)
	svc := reporting.NewService(pool)
	newPlan(t, ms, ctx, "Real Plan")

	// Missing.
	_, err := svc.BudgetVarianceFor(ctx, companyID, uuid.Nil, uuid.Nil,
		reporting.BudgetDimensionLedger)
	be, ok := reporting.AsBudgetSelectionError(err)
	if !ok || !be.Missing {
		t.Fatalf("an absent budget_id produced %v rather than a Missing "+
			"BudgetSelectionError", err)
	}
	if !strings.Contains(be.Error(), "Real Plan") {
		t.Errorf("the refusal does not name the plans this company HAS: %q. A refusal that "+
			"does not list the alternatives leaves the operator guessing at uuids",
			be.Error())
	}

	// Named but absent.
	_, err = svc.BudgetVarianceFor(ctx, companyID, uuid.New(), uuid.Nil,
		reporting.BudgetDimensionLedger)
	be2, ok := reporting.AsBudgetSelectionError(err)
	if !ok || be2.Missing {
		t.Errorf("naming a budget these books do not contain produced %v, which is not a "+
			"non-Missing BudgetSelectionError — so the HTTP seam cannot answer 422 for it",
			err)
	}
}

// ---------------------------------------------------------------------------
// ⛔ RLS — A PLAN IS SHARPER THAN THE BOOKS IT MEASURES
// ---------------------------------------------------------------------------

// TestOneTenantsBudgetIsInvisibleToAnother is the tenancy gate.
//
// ⛔ A BUDGET IS A SHARPER DISCLOSURE THAN THE LEDGER IT IS COMPARED AGAINST,
// because it states what a business INTENDS rather than what it has done. So the
// leak this guards against is worse than a books leak, and migration 0085's policy
// carries BOTH USING and WITH CHECK for that reason.
func TestOneTenantsBudgetIsInvisibleToAnother(t *testing.T) {
	pool, ctx, companyID, ms, f := budgetSetup(t)
	svc := reporting.NewService(pool)

	mine := newPlan(t, ms, ctx, "My Plan")
	addLine(t, ms, ctx, mine, masters.DimensionLedger, f.travel,
		masters.BasisNettTransactions, "", "500000")

	// A second tenant, with its own cleanup registered THE MOMENT it exists.
	otherID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new company id: %v", err)
	}
	octx := db.WithCompany(context.Background(), otherID)
	ids := identity.NewService(pool)
	if _, err := ids.CreateCompany(octx, identity.Company{
		ID: otherID, Name: "Other Budget Co " + otherID.String()[:8], FYStartDate: "2025-04-01",
	}); err != nil {
		t.Fatalf("create second company: %v", err)
	}
	testdb.Cleanup(t, otherID)

	// The other tenant cannot SEE my plan: it is not in its list of choices, so
	// asking for it by id is "no such budget" rather than a report.
	_, err = svc.BudgetVarianceFor(octx, otherID, mine, uuid.Nil,
		reporting.BudgetDimensionLedger)
	if err == nil {
		t.Fatal("⛔ ANOTHER TENANT READ THIS COMPANY'S BUDGET VARIANCE. A plan states what a " +
			"business intends, which is a sharper disclosure than the books it is measured " +
			"against — this is a cross-tenant leak of commercial strategy")
	}
	be, ok := reporting.AsBudgetSelectionError(err)
	if !ok {
		t.Fatalf("the cross-tenant refusal is not a BudgetSelectionError (%v). It must be "+
			"the ORDINARY not-found refusal: a distinct \"forbidden\" answer would confirm "+
			"to the other tenant that this budget id exists somewhere", err)
	}
	if strings.Contains(be.Error(), "My Plan") {
		t.Errorf("⛔ the refusal LEAKS THE PLAN'S NAME to the other tenant: %q. The refusal "+
			"names the budgets available, and RLS must make that list the caller's OWN — "+
			"naming someone else's plan in an error message is the leak in miniature",
			be.Error())
	}

	// The control: my own read still works, so the refusal above is tenancy and not
	// a broken fixture.
	if _, err := svc.BudgetVarianceFor(ctx, companyID, mine, uuid.Nil,
		reporting.BudgetDimensionLedger); err != nil {
		t.Fatalf("CONTROL: the owning tenant cannot read its own plan either (%v) — the "+
			"refusal above proves nothing about RLS", err)
	}
}

// ---------------------------------------------------------------------------
// THE MIXED-BASIS SUPPRESSION, END TO END
// ---------------------------------------------------------------------------

// TestAMixedBasisPlanSuppressesItsGrandTotalEndToEnd drives the addability rule
// through real storage, because the pure test constructs its rows by hand and
// could not catch `basis` being lost on the way out of the database.
func TestAMixedBasisPlanSuppressesItsGrandTotalEndToEnd(t *testing.T) {
	pool, ctx, companyID, ms, f := budgetSetup(t)
	svc := reporting.NewService(pool)

	plan := newPlan(t, ms, ctx, "Mixed Plan")
	addLine(t, ms, ctx, plan, masters.DimensionLedger, f.travel,
		masters.BasisNettTransactions, "", "500000")
	addLine(t, ms, ctx, plan, masters.DimensionLedger, f.sales,
		masters.BasisClosingBalance, "", "1000000")

	rep, err := svc.BudgetVarianceFor(ctx, companyID, plan, uuid.Nil,
		reporting.BudgetDimensionLedger)
	if err != nil {
		t.Fatalf("report: %v", err)
	}

	if rep.TotalsAreAddable {
		t.Error("a stored plan mixing a NETT_TRANSACTIONS line with a CLOSING_BALANCE one " +
			"was declared addable. If `basis` survives storage correctly this cannot " +
			"happen — so either the column is being lost on read (and every closing-balance " +
			"line in the product is measured as movement) or the addability rule is gone")
	}
	if !rep.TotalBudget.IsZero() {
		t.Errorf("a mixed-basis plan published a grand total budget of %s. A position plus a "+
			"flow is not a quantity, and a client that ignores the flag must find zero "+
			"rather than a plausible figure", rep.TotalBudget.StringFixed(4))
	}
	if len(rep.Totals) != 2 {
		t.Fatalf("%d per-basis subtotals for a plan spanning both bases: they are the only "+
			"totals this report may state, so losing one leaves the reader with nothing",
			len(rep.Totals))
	}

	// The INCOME line's side must have been derived as credit, or its actual comes
	// back negative and the row reads CONTRA.
	salesRow, ok := findRow(rep.Rows, f.sales)
	if !ok {
		t.Fatal("the Domestic Sales line is missing from its own report")
	}
	if salesRow.DrCr != "C" {
		t.Errorf("the Domestic Sales budget was stored on the %q side. Sales Accounts is "+
			"INCOME and therefore credit-natured; the debit side turns every invoice raised "+
			"into a negative actual and reports a record quarter as a collapse", salesRow.DrCr)
	}
	if salesRow.Actual.IsNegative() {
		t.Errorf("Domestic Sales' actual came back as %s. A negative actual on a revenue "+
			"budget means the side was derived the wrong way round",
			salesRow.Actual.StringFixed(4))
	}
	if !salesRow.Actual.Equal(dec("500000")) {
		t.Errorf("Domestic Sales' closing position is %s rather than 500000",
			salesRow.Actual.StringFixed(4))
	}
}

// ---------------------------------------------------------------------------
// THE MASTER'S OWN DATABASE-LEVEL REFUSALS
// ---------------------------------------------------------------------------

// TestTheDatabaseRefusesASecondLineForOneTarget pins 0085's three partial unique
// indexes, which exist because a plain composite UNIQUE over three nullable
// columns would never fire (two NULLs are not equal in SQL).
func TestTheDatabaseRefusesASecondLineForOneTarget(t *testing.T) {
	pool, ctx, _, ms, f := budgetSetup(t)
	_ = pool
	plan := newPlan(t, ms, ctx, "Dup Plan")
	addLine(t, ms, ctx, plan, masters.DimensionLedger, f.travel,
		masters.BasisNettTransactions, "", "500000")

	_, err := ms.AddBudgetLine(ctx, plan, masters.BudgetLineInput{
		Dimension: masters.DimensionLedger, TargetID: f.travel,
		Basis: masters.BasisNettTransactions, Amount: dec("700000"),
	}, "budget-variance-test")
	if err == nil {
		t.Fatal("a SECOND budget line for the same ledger in the same plan was stored. The " +
			"variance report would show that ledger twice with the actual counted in full " +
			"against each, so the budget column would total more than anyone set — and it " +
			"would foot")
	}
	if !strings.Contains(err.Error(), "already has a line") {
		t.Errorf("the duplicate was refused with a raw constraint name rather than a "+
			"sentence: %v", err)
	}
}

// TestTheDatabaseRefusesACostCentreOnAClosingBalance proves 0085's CHECK is real
// and not merely mirrored in Go. The masters layer refuses first — that is tested
// purely — so this drives the DATABASE by asking for a basis Go would allow for a
// LEDGER and the schema forbids for a CENTRE.
func TestTheDatabaseRefusesACostCentreOnAClosingBalance(t *testing.T) {
	pool, ctx, companyID, _, _ := budgetSetup(t)

	centre := seedCostCentreForBudget(t, pool, ctx, companyID)
	var pgErr error
	err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		id, _ := uuid.NewV7()
		budgetID, _ := uuid.NewV7()
		if _, err := tx.Exec(ctx,
			`INSERT INTO budget (id, company_id, name, from_date, to_date, created_by)
			 VALUES ($1, $2, 'Raw Plan', DATE '2025-07-01', DATE '2026-03-31', 'test')`,
			budgetID, companyID); err != nil {
			return err
		}
		_, pgErr = tx.Exec(ctx,
			`INSERT INTO budget_line
			   (id, company_id, budget_id, dimension, cost_centre_id, basis, dr_cr, amount)
			 VALUES ($1, $2, $3, 'COST_CENTRE', $4, 'CLOSING_BALANCE', 'D', 500)`,
			id, companyID, budgetID, centre)
		// Return the error so the transaction rolls back either way; the assertion
		// is on pgErr.
		return pgErr
	})
	_ = err

	if pgErr == nil {
		t.Fatal("⛔ THE DATABASE ACCEPTED A COST CENTRE ON A CLOSING_BALANCE BASIS. A cost " +
			"centre holds no position — only allocations against dated postings — so its " +
			"\"closing balance\" is every rupee booked to that department since the books " +
			"began. Go refuses this, but Go is not the authority: with the CHECK gone, any " +
			"other writer (an import, a script, a later handler) plants a line whose report " +
			"looks almost entirely unspent in year one and several times overspent in " +
			"year four")
	}
	if !strings.Contains(pgErr.Error(), "budget_line_cost_centre_is_transactional") {
		t.Errorf("the write was refused, but not by the constraint that is supposed to "+
			"refuse it: %v. Something else is rejecting it, and the CHECK may not exist",
			pgErr)
	}
}

func seedCostCentreForBudget(
	t *testing.T, pool *db.Pool, ctx context.Context, companyID uuid.UUID,
) uuid.UUID {
	t.Helper()
	catID, _ := uuid.NewV7()
	centreID, _ := uuid.NewV7()
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO cost_category (id, company_id, name, created_by)
			 VALUES ($1, $2, 'Department', 'budget-test')`, catID, companyID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO cost_centre (id, company_id, cost_category_id, name, created_by)
			 VALUES ($1, $2, $3, 'Sales', 'budget-test')`, centreID, companyID, catID)
		return err
	}); err != nil {
		t.Fatalf("seed cost centre: %v", err)
	}
	return centreID
}
