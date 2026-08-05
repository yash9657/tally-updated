package reporting_test

// THE DB-BACKED HALF OF THE TWO COST SUMMARIES.
//
// Everything here needs Postgres, because everything here is about something the
// pure half CANNOT check: that the SQL groups by (posting, category) and not by
// posting, that the current-version join actually excludes an altered voucher's
// old allocations, that the allocable denominator honours
// cost_category.allocate_non_revenue against real account_group rows, and that
// RLS keeps one tenant's departmental profitability out of another's report.
//
// ⚠️⚠️ NOT RUN IN THE AUTHORING SESSION. Every worktree on this machine shares one
// Postgres on :5433 and concurrent runs race, so these tests were WRITTEN and
// type-checked (go vet compiles test files) and NOT EXECUTED. Nothing below has
// been observed to pass. The first session with the database to itself must run
// them SERIALLY; treat a failure here as an unverified claim in cost_summary.go,
// not as flake. The pure half (cost_summary_test.go, package reporting) WAS run
// and is green.
//
// They reuse setup(t) from reporting_test.go, which creates the company and
// registers testdb.Cleanup for it. THE SECOND COMPANY in the RLS test registers
// its own cleanup, immediately after it exists — internal/testhygiene fails the
// build over a company-creating test that does not, and testdb.Keep is banned.
//
// ===========================================================================
// WHY THE ALLOCATIONS ARE SEEDED BY SQL WHILE THE VOUCHERS ARE POSTED
// ===========================================================================
// The vouchers go through internal/ledger, because a report that read postings
// no posting path produced would prove nothing about the current-version join —
// which is half of what these tests are for.
//
// The ALLOCATIONS are inserted directly, and that is a deliberate seam and not
// laziness: ledger.post()'s allocation write is another lane's file in this
// wave, and a report test that could not run until that landed would be a report
// test nobody ran. Every inserted row is one the write path could legally create
// — the amounts total the posting EXACTLY per axis, which is what
// cost_allocation_sums_to_posting demands at COMMIT (and it is NOT suppressed
// here: these inserts go through the ordinary app role, so an incomplete fixture
// FAILS rather than seeding data production could never hold).
//
// WHAT THAT COSTS, STATED: nothing here proves the hashed payload's
// CostAllocations agrees with these rows. That agreement is the spine lane's, and
// its test belongs beside ledger.post().

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/internal/reporting"
)

// ---------------------------------------------------------------------------
// Fixture helpers
// ---------------------------------------------------------------------------

// seedCostCategory inserts one axis. allocateNonRevenue is migration 0040's flag:
// FALSE means the axis belongs on P&L ledgers only, and it is what decides the
// allocable denominator the Category Summary measures against.
func seedCostCategory(
	t *testing.T, pool *db.Pool, ctx context.Context, name string, allocateNonRevenue bool,
) uuid.UUID {
	t.Helper()
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		t.Fatal("seedCostCategory needs a company-scoped context")
	}
	id := uuid.New()
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO cost_category (id, company_id, name, allocate_non_revenue, created_by)
			 VALUES ($1, $2, $3, $4, 'cost-summary-test')`,
			id, companyID, name, allocateNonRevenue)
		return err
	}); err != nil {
		t.Fatalf("seed cost category %q: %v", name, err)
	}
	return id
}

// seedCostCentre inserts one position on an axis. parent may be uuid.Nil.
func seedCostCentre(
	t *testing.T, pool *db.Pool, ctx context.Context, categoryID, parent uuid.UUID, name string,
) uuid.UUID {
	t.Helper()
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		t.Fatal("seedCostCentre needs a company-scoped context")
	}
	id := uuid.New()
	var parentArg any
	if parent != uuid.Nil {
		parentArg = parent
	}
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO cost_centre (id, company_id, cost_category_id, name, parent_id, created_by)
			 VALUES ($1, $2, $3, $4, $5, 'cost-summary-test')`,
			id, companyID, categoryID, name, parentArg)
		return err
	}); err != nil {
		t.Fatalf("seed cost centre %q: %v", name, err)
	}
	return id
}

// postingIDFor finds the CURRENT version's posting for (voucher, ledger, side).
// Allocations key on posting_id and posting rows are re-created wholesale by
// every ALTER, so a test that allocates against a voucher must look the id up
// AFTER the version it means — never cache one across an alter.
func postingIDFor(
	t *testing.T, pool *db.Pool, ctx context.Context, voucherID, ledgerID uuid.UUID, drcr string,
) (uuid.UUID, decimal.Decimal) {
	t.Helper()
	var (
		id     uuid.UUID
		amount decimal.Decimal
	)
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT p.id, p.amount
			   FROM posting p
			   JOIN voucher_version vv ON vv.id = p.voucher_version_id
			   JOIN voucher_head vh
			     ON vh.company_id = vv.company_id
			    AND vh.voucher_id = vv.voucher_id
			    AND vh.current_version_id = vv.id
			  WHERE vv.voucher_id = $1 AND p.ledger_id = $2 AND p.dr_cr = $3::dr_cr`,
			voucherID, ledgerID, drcr).Scan(&id, &amount)
	}); err != nil {
		t.Fatalf("find posting for voucher %s ledger %s %s: %v", voucherID, ledgerID, drcr, err)
	}
	return id, amount
}

// allocation is one (centre, amount) pair of a split.
type allocation struct {
	CentreID uuid.UUID
	Amount   string
}

// seedAllocation writes ONE COMPLETE split of one posting on ONE axis, in ONE
// transaction.
//
// ⚠️ ONE TRANSACTION PER AXIS, AND THAT IS THE POINT OF THIS HELPER.
// cost_allocation_sums_to_posting is DEFERRABLE INITIALLY DEFERRED and grouped by
// (posting, category), so the three INSERTs of a three-way split are each
// legitimately short and only the COMMIT decides. A fixture that split across two
// helper calls would commit an incomplete set and fail — which is exactly the
// behaviour under test, so it must not be how the fixture is written.
func seedAllocation(
	t *testing.T, pool *db.Pool, ctx context.Context,
	postingID, categoryID uuid.UUID, parts []allocation,
) {
	t.Helper()
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		t.Fatal("seedAllocation needs a company-scoped context")
	}
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		for _, p := range parts {
			if _, err := tx.Exec(ctx,
				`INSERT INTO posting_cost_allocation
				   (id, company_id, posting_id, cost_category_id, cost_centre_id, amount)
				 VALUES ($1, $2, $3, $4, $5, $6)`,
				uuid.New(), companyID, postingID, categoryID, p.CentreID,
				decimal.RequireFromString(p.Amount)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed allocation on posting %s axis %s: %v", postingID, categoryID, err)
	}
}

// postExpense posts a one-line expense journal and returns the voucher id.
func postExpense(
	t *testing.T, pool *db.Pool, ctx context.Context, ids *identity.Service,
	date string, expense, funding uuid.UUID, amount string,
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
		CreatedBy:     "cost-summary-test",
		Postings: []ledger.PostingInput{
			{LedgerID: expense, DrCr: ledger.Debit, Amount: dec(amount)},
			{LedgerID: funding, DrCr: ledger.Credit, Amount: dec(amount)},
		},
	})
	if err != nil {
		t.Fatalf("post expense %s on %s: %v", amount, date, err)
	}
	return res.VoucherID
}

// costFixture is the scenario every test below reads.
//
//	Department (axis, revenue items only)
//	  Sales                      -- parent
//	    Sales - North
//	  Admin
//	Branch (axis, revenue items only)
//	  Mumbai
//	  Delhi
//
//	2025-04-20  Salaries 1,00,000  Dr  (funded by Cash)
//	              Department: Sales - North 60,000 / Admin 40,000   [complete]
//	              Branch:     Mumbai       70,000 / Delhi 30,000    [complete]
//	2025-04-25  Rent Paid  20,000  Dr  (funded by Cash)
//	              NOT ALLOCATED ON EITHER AXIS  <-- the gap the report exists for
type costFixture struct {
	deptID, branchID           uuid.UUID
	sales, salesNorth, admin   uuid.UUID
	mumbai, delhi              uuid.UUID
	salaries, rent, cash       uuid.UUID
	salaryVoucher, rentVoucher uuid.UUID
	salaryPosting              uuid.UUID
}

func seedCostFixture(
	t *testing.T, pool *db.Pool, ctx context.Context, ids *identity.Service,
) costFixture {
	t.Helper()
	var f costFixture

	mk := func(name, group string) uuid.UUID {
		id, err := ids.CreateLedger(ctx, name, group)
		if err != nil {
			t.Fatalf("create ledger %s: %v", name, err)
		}
		return id
	}
	f.salaries = mk("Salaries", "Indirect Expenses")
	f.rent = mk("Rent - Unallocated", "Indirect Expenses")
	cash, err := ids.LedgerID(ctx, "Cash")
	if err != nil {
		t.Fatalf("cash ledger: %v", err)
	}
	f.cash = cash

	f.deptID = seedCostCategory(t, pool, ctx, "Department", false)
	f.branchID = seedCostCategory(t, pool, ctx, "Branch", false)
	f.sales = seedCostCentre(t, pool, ctx, f.deptID, uuid.Nil, "Sales")
	f.salesNorth = seedCostCentre(t, pool, ctx, f.deptID, f.sales, "Sales - North")
	f.admin = seedCostCentre(t, pool, ctx, f.deptID, uuid.Nil, "Admin")
	f.mumbai = seedCostCentre(t, pool, ctx, f.branchID, uuid.Nil, "Mumbai")
	f.delhi = seedCostCentre(t, pool, ctx, f.branchID, uuid.Nil, "Delhi")

	f.salaryVoucher = postExpense(t, pool, ctx, ids, "2025-04-20", f.salaries, f.cash, "100000")
	f.rentVoucher = postExpense(t, pool, ctx, ids, "2025-04-25", f.rent, f.cash, "20000")

	postingID, amount := postingIDFor(t, pool, ctx, f.salaryVoucher, f.salaries, "D")
	if !amount.Equal(dec("100000")) {
		t.Fatalf("salary posting amount is %s, want 100000: the fixture is not what the tests assume",
			amount.StringFixed(4))
	}
	f.salaryPosting = postingID

	// ⛔ THE WHOLE POINT: the SAME posting, allocated IN FULL TWICE, once per axis.
	seedAllocation(t, pool, ctx, postingID, f.deptID, []allocation{
		{f.salesNorth, "60000"}, {f.admin, "40000"},
	})
	seedAllocation(t, pool, ctx, postingID, f.branchID, []allocation{
		{f.mumbai, "70000"}, {f.delhi, "30000"},
	})
	return f
}

// aprilFrom / aprilTo bracket the fixture's month.
const (
	aprilFrom = "2025-04-01"
	aprilTo   = "2025-04-30"
)

// ---------------------------------------------------------------------------
// The Cost Centre Summary
// ---------------------------------------------------------------------------

// TestCostCentreSummaryRollsUpOneAxisAndReconciles is the primary DB test for
// report 1: the hierarchy rolls up, the total is the roots', and the roll-up
// agrees with the flat sum of the same allocations to the paisa.
func TestCostCentreSummaryRollsUpOneAxisAndReconciles(t *testing.T) {
	pool, ctx, companyID, ids := setup(t)
	f := seedCostFixture(t, pool, ctx, ids)
	rep := reporting.NewService(pool)

	sum, err := rep.CostCentreSummaryFor(ctx, companyID, f.deptID, aprilFrom, aprilTo)
	if err != nil {
		t.Fatalf("cost centre summary: %v", err)
	}
	if sum.CategoryName != "Department" {
		t.Errorf("axis name %q, want Department", sum.CategoryName)
	}
	if !sum.Reconciles() {
		t.Fatalf("the summary does not reconcile: allocated %s Dr, rolled up %s Dr, difference %s — "+
			"a cost report whose roll-up ties to nothing must not be read",
			sum.AllocatedDebit.StringFixed(4), sum.TotalDebit.StringFixed(4),
			sum.DifferenceDebit.StringFixed(4))
	}
	if len(sum.Unattached) != 0 {
		t.Errorf("%d centre(s) could not be placed on a two-level axis: %v", len(sum.Unattached), sum.Warnings)
	}
	// 60,000 + 40,000 = the whole salary posting. THE AXIS ACCOUNTS FOR 100%.
	if !sum.TotalDebit.Equal(dec("100000")) {
		t.Errorf("the Department axis rolls up to %s Dr, want 100000.0000 — the axis carries the "+
			"whole of the salary posting and nothing else", sum.TotalDebit.StringFixed(4))
	}
	if !sum.TotalCredit.IsZero() {
		t.Errorf("credit side %s, want 0: the fixture allocates only debit postings",
			sum.TotalCredit.StringFixed(4))
	}

	// Sales holds NOTHING of its own and 60,000 through its child. Both figures
	// matter: a screen showing only the total could not tell an operator whether
	// the money was keyed against Sales or against Sales - North.
	var sales, admin *reporting.CostCentreNode
	for i := range sum.Roots {
		switch sum.Roots[i].Name {
		case "Sales":
			sales = &sum.Roots[i]
		case "Admin":
			admin = &sum.Roots[i]
		}
	}
	if sales == nil || admin == nil {
		t.Fatalf("roots are %v, want Admin and Sales", rootNames(sum.Roots))
	}
	if !sales.OwnDebit.IsZero() {
		t.Errorf("Sales OWN debit %s, want 0 (everything was keyed against Sales - North)",
			sales.OwnDebit.StringFixed(4))
	}
	if !sales.TotalDebit.Equal(dec("60000")) {
		t.Errorf("Sales TOTAL debit %s, want 60000.0000 rolled up from Sales - North",
			sales.TotalDebit.StringFixed(4))
	}
	if len(sales.Children) != 1 || sales.Children[0].Name != "Sales - North" {
		t.Errorf("Sales children are %v, want [Sales - North]", childNames(sales.Children))
	}
	if !admin.TotalDebit.Equal(dec("40000")) {
		t.Errorf("Admin TOTAL debit %s, want 40000.0000", admin.TotalDebit.StringFixed(4))
	}

	// THE CROSS-AXIS CAVEAT IS ON THE ARTEFACT, not in a design note.
	if !strings.Contains(strings.Join(sum.Warnings, " "), "must never be added") {
		t.Errorf("the summary does not carry the cross-axis warning: %v", sum.Warnings)
	}
}

// TestTheOtherAxisReportsTheSameMoneyInFull is the differential half of the
// double-count guard, against the DATABASE this time: the Branch axis must also
// account for the whole 1,00,000, split differently.
//
// ITS CONTROL is the assertion that the two axes' CENTRE LISTS are disjoint. If
// the query leaked across axes the totals could still both read 1,00,000 while
// Mumbai appeared under Department — a wrong report with right totals.
func TestTheOtherAxisReportsTheSameMoneyInFull(t *testing.T) {
	pool, ctx, companyID, ids := setup(t)
	f := seedCostFixture(t, pool, ctx, ids)
	rep := reporting.NewService(pool)

	dept, err := rep.CostCentreSummaryFor(ctx, companyID, f.deptID, aprilFrom, aprilTo)
	if err != nil {
		t.Fatalf("department summary: %v", err)
	}
	branch, err := rep.CostCentreSummaryFor(ctx, companyID, f.branchID, aprilFrom, aprilTo)
	if err != nil {
		t.Fatalf("branch summary: %v", err)
	}

	if !dept.TotalDebit.Equal(dec("100000")) || !branch.TotalDebit.Equal(dec("100000")) {
		t.Fatalf("Department %s, Branch %s: ONE Rs 1,00,000 posting is allocated in full on each "+
			"axis, so each must report exactly 100000.0000 — anything else means one axis "+
			"absorbed the other's allocation and the shopkeeper is being told their salary "+
			"cost was a multiple of what they paid",
			dept.TotalDebit.StringFixed(4), branch.TotalDebit.StringFixed(4))
	}
	// ⛔ AND NOTHING REPORTS 2,00,000.
	for _, s := range []reporting.CostCentreSummary{dept, branch} {
		if s.TotalDebit.GreaterThan(dec("100000")) {
			t.Errorf("axis %q reports %s, more than the posting it came from", s.CategoryName,
				s.TotalDebit.StringFixed(4))
		}
	}
	// THE CONTROL: the axes are disjoint sets of centres.
	deptNames := map[string]bool{}
	var collect func([]reporting.CostCentreNode)
	collect = func(ns []reporting.CostCentreNode) {
		for _, n := range ns {
			deptNames[n.Name] = true
			collect(n.Children)
		}
	}
	collect(dept.Roots)
	for _, n := range branch.Roots {
		if deptNames[n.Name] {
			t.Errorf("centre %q appears on BOTH axes: the query is not filtering by "+
				"cost_category_id and the two allocation sets are rolling into each other", n.Name)
		}
	}
	if len(deptNames) != 3 {
		t.Errorf("the Department axis lists %d centres (%v), want 3", len(deptNames), deptNames)
	}
}

// TestCostCentreSummaryRefusesWithoutAnAxisAndNamesThem pins the missing- and
// unknown-parameter answers against real data. Both silent alternatives are
// worse than a refusal: picking an axis renders a real report for the wrong one,
// and summing them reports twice the cost.
func TestCostCentreSummaryRefusesWithoutAnAxisAndNamesThem(t *testing.T) {
	pool, ctx, companyID, ids := setup(t)
	seedCostFixture(t, pool, ctx, ids)
	rep := reporting.NewService(pool)

	_, err := rep.CostCentreSummaryFor(ctx, companyID, uuid.Nil, aprilFrom, aprilTo)
	if err == nil {
		t.Fatal("a Cost Centre Summary with no axis was answered rather than refused")
	}
	ae, ok := reporting.AsCostAxisError(err)
	if !ok || !ae.Missing {
		t.Fatalf("want a *CostAxisError with Missing set, got %T: %v", err, err)
	}
	if len(ae.Available) != 2 {
		t.Errorf("the refusal offers %d axes, want 2 (Branch, Department)", len(ae.Available))
	}
	for _, want := range []string{"Department", "Branch", "cost_category_id"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}

	// An axis that does not exist is a DIFFERENT refusal, and must not be an empty
	// report — "nothing was allocated" is a reassuring claim and a false one.
	ghost := uuid.New()
	_, err = rep.CostCentreSummaryFor(ctx, companyID, ghost, aprilFrom, aprilTo)
	ae, ok = reporting.AsCostAxisError(err)
	if !ok || ae.Missing {
		t.Fatalf("an unknown axis: want a *CostAxisError with Missing clear, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), ghost.String()) {
		t.Errorf("the refusal does not name the id that was wrong: %v", err)
	}
}

// TestCostCentreSummaryHonoursThePeriodOnBothBounds — the period is a CLOSED
// interval and both ends are inclusive, exactly as period_test.go pins for every
// other statement. A voucher dated ON a bound is IN.
func TestCostCentreSummaryHonoursThePeriodOnBothBounds(t *testing.T) {
	pool, ctx, companyID, ids := setup(t)
	f := seedCostFixture(t, pool, ctx, ids)
	rep := reporting.NewService(pool)

	total := func(from, to string) decimal.Decimal {
		s, err := rep.CostCentreSummaryFor(ctx, companyID, f.deptID, from, to)
		if err != nil {
			t.Fatalf("summary %q..%q: %v", from, to, err)
		}
		return s.TotalDebit
	}
	// The salary voucher is dated 2025-04-20.
	if got := total("2025-04-20", "2025-04-20"); !got.Equal(dec("100000")) {
		t.Errorf("a one-day period ON the voucher's own date reports %s, want 100000.0000: both "+
			"bounds are INCLUSIVE", got.StringFixed(4))
	}
	if got := total("2025-04-21", aprilTo); !got.IsZero() {
		t.Errorf("a period starting the day AFTER the voucher reports %s, want 0", got.StringFixed(4))
	}
	if got := total(aprilFrom, "2025-04-19"); !got.IsZero() {
		t.Errorf("a period ending the day BEFORE the voucher reports %s, want 0", got.StringFixed(4))
	}
	// Unbounded is the whole book, which here is the same 1,00,000.
	if got := total("", ""); !got.Equal(dec("100000")) {
		t.Errorf("the unbounded summary reports %s, want 100000.0000", got.StringFixed(4))
	}
}

// TestAnAlteredVoucherIsNotAllocatedTwice is I-9 against the database, and it is
// the test most likely to catch a real regression.
//
// posting_cost_allocation keys on posting_id, and an ALTER re-creates posting
// rows wholesale — so the OLD version's allocations still exist in the table
// after an alter. They must not appear, and the only thing excluding them is the
// current-version join. Without it the axis would report 1,80,000 against a
// voucher restated to 80,000, which is the printed Day Book's defect one table
// over.
func TestAnAlteredVoucherIsNotAllocatedTwice(t *testing.T) {
	pool, ctx, companyID, ids := setup(t)
	f := seedCostFixture(t, pool, ctx, ids)
	rep := reporting.NewService(pool)
	led := ledger.NewService(pool)

	vt, err := ids.VoucherTypeID(ctx, "Journal")
	if err != nil {
		t.Fatalf("journal vt: %v", err)
	}
	// Restate the salary from 1,00,000 to 80,000.
	if _, err := led.AlterVoucher(ctx, f.salaryVoucher, ledger.VoucherInput{
		Class: ledger.Journal, VoucherTypeID: vt, VoucherDate: "2025-04-20",
		CreatedBy: "cost-summary-test",
		Postings: []ledger.PostingInput{
			{LedgerID: f.salaries, DrCr: ledger.Debit, Amount: dec("80000")},
			{LedgerID: f.cash, DrCr: ledger.Credit, Amount: dec("80000")},
		},
	}); err != nil {
		t.Fatalf("alter salary voucher: %v", err)
	}

	// The alter produced NEW posting rows with no allocations of their own (this
	// lane does not write them), so the axis must now report NOTHING — not the old
	// 1,00,000, and certainly not 1,80,000.
	sum, err := rep.CostCentreSummaryFor(ctx, companyID, f.deptID, aprilFrom, aprilTo)
	if err != nil {
		t.Fatalf("summary after alter: %v", err)
	}
	if !sum.TotalDebit.IsZero() {
		t.Errorf("after restating the salary voucher the Department axis reports %s Dr. The "+
			"superseded version's allocations are still in posting_cost_allocation, keyed on "+
			"postings that are no longer current; only the current-version join excludes them. "+
			"100000.0000 means the old version is still counted, 180000.0000 means BOTH are.",
			sum.TotalDebit.StringFixed(4))
	}
	if !sum.Reconciles() {
		t.Errorf("the summary stopped reconciling after an alter: difference %s Dr",
			sum.DifferenceDebit.StringFixed(4))
	}

	// Re-allocate the NEW posting in full, and the axis reports the new amount —
	// once.
	newPosting, amount := postingIDFor(t, pool, ctx, f.salaryVoucher, f.salaries, "D")
	if !amount.Equal(dec("80000")) {
		t.Fatalf("the current posting is %s, want 80000: the alter did not take", amount.StringFixed(4))
	}
	seedAllocation(t, pool, ctx, newPosting, f.deptID, []allocation{{f.admin, "80000"}})
	sum, err = rep.CostCentreSummaryFor(ctx, companyID, f.deptID, aprilFrom, aprilTo)
	if err != nil {
		t.Fatalf("summary after re-allocation: %v", err)
	}
	if !sum.TotalDebit.Equal(dec("80000")) {
		t.Errorf("after re-allocating the restated voucher the axis reports %s, want 80000.0000",
			sum.TotalDebit.StringFixed(4))
	}
}

// TestADeletedVoucherIsNotAllocated — a tombstone version carries no posting
// rows, so a deleted voucher's allocations fall out of every figure. Asserted
// rather than reasoned about, because the reasoning is about another package's
// delete path.
func TestADeletedVoucherIsNotAllocated(t *testing.T) {
	pool, ctx, companyID, ids := setup(t)
	f := seedCostFixture(t, pool, ctx, ids)
	rep := reporting.NewService(pool)

	if _, err := ledger.NewService(pool).DeleteVoucher(ctx, f.salaryVoucher, "cost-summary-test"); err != nil {
		t.Fatalf("delete salary voucher: %v", err)
	}
	for _, axis := range []uuid.UUID{f.deptID, f.branchID} {
		sum, err := rep.CostCentreSummaryFor(ctx, companyID, axis, aprilFrom, aprilTo)
		if err != nil {
			t.Fatalf("summary after delete: %v", err)
		}
		if !sum.TotalDebit.IsZero() {
			t.Errorf("axis %q still reports %s Dr for a DELETED voucher", sum.CategoryName,
				sum.TotalDebit.StringFixed(4))
		}
	}
}

// ---------------------------------------------------------------------------
// The Cost Category Summary — and the gap
// ---------------------------------------------------------------------------

// TestCostCategorySummaryReportsEachAxisInFullAndNeverTwice is report 2's
// primary DB test, and it is the double-count guard against real SQL.
func TestCostCategorySummaryReportsEachAxisInFullAndNeverTwice(t *testing.T) {
	pool, ctx, companyID, ids := setup(t)
	seedCostFixture(t, pool, ctx, ids)
	rep := reporting.NewService(pool)

	sum, err := rep.CostCategorySummaryFor(ctx, companyID, aprilFrom, aprilTo)
	if err != nil {
		t.Fatalf("cost category summary: %v", err)
	}
	if len(sum.Rows) != 2 {
		t.Fatalf("got %d rows, want 2 (Branch, Department)", len(sum.Rows))
	}
	if !sum.Reconciles() {
		for _, r := range sum.Rows {
			t.Logf("axis %s: allocable %s, allocated %s, unallocated %s, diff %s; notes %v",
				r.CategoryName, r.AllocableDebit.StringFixed(4), r.AllocatedDebit.StringFixed(4),
				r.UnallocatedDebit.StringFixed(4), r.DifferenceDebit.StringFixed(4), r.Notes)
		}
		t.Fatal("a row does not reconcile: the allocable base, what was allocated and the " +
			"remainder must agree TO THE PAISA, with no tolerance")
	}

	for _, r := range sum.Rows {
		if !r.AllocatedDebit.Equal(dec("100000")) {
			t.Errorf("axis %q reports %s allocated. ONE Rs 1,00,000 posting is allocated in full "+
				"on each axis, so each row must read exactly 100000.0000 — 200000.0000 would be "+
				"the same money counted once per axis and would tell the shopkeeper their salary "+
				"bill was twice what they paid.", r.CategoryName, r.AllocatedDebit.StringFixed(4))
		}
		if r.AllocatedPostings != 1 {
			t.Errorf("axis %q reports %d allocated posting(s), want 1", r.CategoryName, r.AllocatedPostings)
		}
	}
}

// TestTheUnallocatedRemainderIsTheReportsWholePurpose pins the figure the
// Category Summary exists to produce.
//
// The fixture posts Rs 20,000 of rent and allocates it on NOTHING. The trigger
// has nothing to say about it — it fires on allocations, and there are none — so
// this is exactly the gap no constraint can see. Both axes must report 20,000
// missing, and say so in rupees.
func TestTheUnallocatedRemainderIsTheReportsWholePurpose(t *testing.T) {
	pool, ctx, companyID, ids := setup(t)
	seedCostFixture(t, pool, ctx, ids)
	rep := reporting.NewService(pool)

	sum, err := rep.CostCategorySummaryFor(ctx, companyID, aprilFrom, aprilTo)
	if err != nil {
		t.Fatalf("cost category summary: %v", err)
	}
	for _, r := range sum.Rows {
		// The allocable base is the period's P&L debits: 1,00,000 salaries +
		// 20,000 rent. Cash is a balance-sheet ledger and both axes are
		// revenue-only, so the credit legs are NOT in the denominator — which is
		// the allocate_non_revenue rule doing its job.
		if !r.AllocableDebit.Equal(dec("120000")) {
			t.Errorf("axis %q: allocable base %s Dr, want 120000.0000 (1,00,000 salaries + "+
				"20,000 rent; the Cash credit legs are balance-sheet and this axis is "+
				"revenue-only)", r.CategoryName, r.AllocableDebit.StringFixed(4))
		}
		if !r.AllocableCredit.IsZero() {
			t.Errorf("axis %q: allocable credit %s, want 0 — a revenue-only axis must not count "+
				"the Cash leg", r.CategoryName, r.AllocableCredit.StringFixed(4))
		}
		if !r.UnallocatedDebit.Equal(dec("20000")) || r.UnallocatedPostings != 1 {
			t.Errorf("axis %q: %s Dr / %d posting(s) unallocated, want 20000.0000 / 1. That rent "+
				"is real cost with nothing attached to it: it is in the Profit & Loss and missing "+
				"from every %s report, and NO database constraint can see it — which is why this "+
				"report exists.", r.CategoryName, r.UnallocatedDebit.StringFixed(4),
				r.UnallocatedPostings, r.CategoryName)
		}
		if r.IsFullyAllocated() {
			t.Errorf("axis %q claims to be complete with 20,000 unallocated", r.CategoryName)
		}
		if !strings.Contains(strings.Join(r.Notes, " "), "20000.0000") {
			t.Errorf("axis %q does not state the gap in rupees: %v", r.CategoryName, r.Notes)
		}
	}
	joined := strings.Join(sum.Notes, " ")
	if !strings.Contains(joined, "NO TOTAL IS SHOWN ACROSS THESE ROWS") {
		t.Errorf("the summary does not carry the no-cross-axis-total note: %v", sum.Notes)
	}
	if !strings.Contains(joined, "Incomplete on:") {
		t.Errorf("the summary does not name the incomplete axes: %v", sum.Notes)
	}
}

// TestAllocatingTheRentClosesTheGapExactly is the differential CONTROL for the
// test above: the gap must be a live figure that moves with the books, not a
// constant the fixture happens to produce.
func TestAllocatingTheRentClosesTheGapExactly(t *testing.T) {
	pool, ctx, companyID, ids := setup(t)
	f := seedCostFixture(t, pool, ctx, ids)
	rep := reporting.NewService(pool)

	rentPosting, amount := postingIDFor(t, pool, ctx, f.rentVoucher, f.rent, "D")
	if !amount.Equal(dec("20000")) {
		t.Fatalf("rent posting is %s, want 20000", amount.StringFixed(4))
	}
	// Allocate the rent on the Department axis ONLY, so the two axes must now
	// DIFFER — a report that reported the same gap for both would be reporting
	// something other than the books.
	seedAllocation(t, pool, ctx, rentPosting, f.deptID, []allocation{{f.admin, "20000"}})

	sum, err := rep.CostCategorySummaryFor(ctx, companyID, aprilFrom, aprilTo)
	if err != nil {
		t.Fatalf("cost category summary: %v", err)
	}
	var dept, branch *reporting.CostCategoryRow
	for i := range sum.Rows {
		switch sum.Rows[i].CategoryName {
		case "Department":
			dept = &sum.Rows[i]
		case "Branch":
			branch = &sum.Rows[i]
		}
	}
	if dept == nil || branch == nil {
		t.Fatalf("rows are %v, want Branch and Department", rowAxisNames(sum.Rows))
	}
	if !dept.IsFullyAllocated() {
		t.Errorf("Department still reports %s unallocated after every P&L posting in the period "+
			"was allocated on it", dept.UnallocatedDebit.StringFixed(4))
	}
	if !dept.AllocatedDebit.Equal(dec("120000")) {
		t.Errorf("Department allocated %s, want 120000.0000 (salaries 1,00,000 + rent 20,000)",
			dept.AllocatedDebit.StringFixed(4))
	}
	if !branch.UnallocatedDebit.Equal(dec("20000")) {
		t.Errorf("Branch reports %s unallocated, want 20000.0000: the rent was allocated on the "+
			"DEPARTMENT axis only, and an axis that borrowed another's allocations would show "+
			"full coverage of cost nobody analysed by branch",
			branch.UnallocatedDebit.StringFixed(4))
	}
	if !sum.Reconciles() {
		t.Errorf("a row stopped reconciling: Department diff %s, Branch diff %s",
			dept.DifferenceDebit.StringFixed(4), branch.DifferenceDebit.StringFixed(4))
	}
}

// TestANonRevenueAxisCountsTheWholeBook pins the allocate_non_revenue rule from
// the other side. An axis that allows non-revenue items is measured against
// EVERY posting in the period — including the Cash credit legs — so its
// denominator is strictly larger and its gap is bigger for the same allocations.
//
// Without this test the flag could be ignored entirely and every assertion above
// would still pass.
func TestANonRevenueAxisCountsTheWholeBook(t *testing.T) {
	pool, ctx, companyID, ids := setup(t)
	seedCostFixture(t, pool, ctx, ids)
	rep := reporting.NewService(pool)

	// A third axis, this one permitted on balance-sheet ledgers.
	seedCostCategory(t, pool, ctx, "Project", true)

	sum, err := rep.CostCategorySummaryFor(ctx, companyID, aprilFrom, aprilTo)
	if err != nil {
		t.Fatalf("cost category summary: %v", err)
	}
	var project *reporting.CostCategoryRow
	for i := range sum.Rows {
		if sum.Rows[i].CategoryName == "Project" {
			project = &sum.Rows[i]
		}
	}
	if project == nil {
		t.Fatalf("no Project row: %v", rowAxisNames(sum.Rows))
	}
	if !project.AllocableDebit.Equal(dec("120000")) {
		t.Errorf("Project allocable debit %s, want 120000.0000", project.AllocableDebit.StringFixed(4))
	}
	// THE DIFFERENCE FROM A REVENUE-ONLY AXIS: the Cash CREDIT legs are in.
	if !project.AllocableCredit.Equal(dec("120000")) {
		t.Errorf("Project allocable CREDIT %s, want 120000.0000 (the two Cash legs). An axis that "+
			"allows non-revenue items is measured against every posting; reporting 0 here means "+
			"allocate_non_revenue is being ignored and a Project axis would look complete while "+
			"saying nothing about the balance sheet", project.AllocableCredit.StringFixed(4))
	}
	if project.UnallocatedPostings != 4 {
		t.Errorf("Project reports %d unallocated posting(s), want 4 (two vouchers, two legs each, "+
			"none allocated on this axis)", project.UnallocatedPostings)
	}
	if !project.Reconciles() {
		t.Errorf("the Project row does not reconcile: diff %s Dr / %s Cr",
			project.DifferenceDebit.StringFixed(4), project.DifferenceCredit.StringFixed(4))
	}
}

// TestCostCategorySummaryOnACompanyWithNoAxesSaysSo — no cost categories is a
// legitimate state (most books will never use the feature), and the honest answer
// is "no cost analysis is being kept", not an empty table.
func TestCostCategorySummaryOnACompanyWithNoAxesSaysSo(t *testing.T) {
	pool, ctx, companyID, ids := setup(t)
	seedBooks(t, pool, ctx, ids)
	rep := reporting.NewService(pool)

	sum, err := rep.CostCategorySummaryFor(ctx, companyID, aprilFrom, aprilTo)
	if err != nil {
		t.Fatalf("cost category summary: %v", err)
	}
	if len(sum.Rows) != 0 {
		t.Errorf("got %d rows on a company with no cost categories", len(sum.Rows))
	}
	if !strings.Contains(strings.Join(sum.Notes, " "), "no cost categories") {
		t.Errorf("the summary does not say the company keeps no cost analysis: %v", sum.Notes)
	}
	if !sum.Reconciles() {
		t.Error("an empty summary does not reconcile")
	}
}

// ---------------------------------------------------------------------------
// RLS
// ---------------------------------------------------------------------------

// TestCostReportsAreTenantIsolated is the leak test. cost_category, cost_centre
// and posting_cost_allocation are all FORCE ROW LEVEL SECURITY with a policy on
// company_id, and migration 0040's header says what the exposure is: company A's
// Cost Centre Summary quietly including company B's cost. Two companies, each
// with an axis of the SAME NAME (a name is unique per company, so this is legal
// and is exactly how a leak would look plausible), and neither may see the other.
func TestCostReportsAreTenantIsolated(t *testing.T) {
	poolA, ctxA, companyA, idsA := setup(t)
	fA := seedCostFixture(t, poolA, ctxA, idsA)
	rep := reporting.NewService(poolA)

	// A SECOND tenant, in its own scope, with its own cleanup registered the
	// moment it exists. testdb.Keep is banned; a company created without a
	// Cleanup fails internal/testhygiene.
	companyB, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new company id: %v", err)
	}
	ctxB := db.WithCompany(context.Background(), companyB)
	idsB := identity.NewService(poolA)
	if _, err := idsB.CreateCompany(ctxB, identity.Company{
		ID:          companyB,
		Name:        "Cost Co B " + companyB.String()[:8],
		FYStartDate: "2025-04-01",
	}); err != nil {
		t.Fatalf("create company B: %v", err)
	}
	testdb.Cleanup(t, companyB)
	fB := seedCostFixture(t, poolA, ctxB, idsB)

	// B's books are twice A's would-be figure if they ever leaked, so a leak is
	// unmistakable rather than a coincidence of equal fixtures.
	bExtra := postExpense(t, poolA, ctxB, idsB, "2025-04-22", fB.salaries, fB.cash, "500000")
	bPosting, _ := postingIDFor(t, poolA, ctxB, bExtra, fB.salaries, "D")
	seedAllocation(t, poolA, ctxB, bPosting, fB.deptID, []allocation{{fB.admin, "500000"}})

	// A's report, in A's scope: A's figures only.
	sumA, err := rep.CostCentreSummaryFor(ctxA, companyA, fA.deptID, aprilFrom, aprilTo)
	if err != nil {
		t.Fatalf("company A summary: %v", err)
	}
	if !sumA.TotalDebit.Equal(dec("100000")) {
		t.Errorf("company A's Department axis reports %s, want 100000.0000: company B's 5,00,000 "+
			"of departmental cost has leaked into it", sumA.TotalDebit.StringFixed(4))
	}
	for _, n := range sumA.Roots {
		if n.CentreID == fB.sales || n.CentreID == fB.admin {
			t.Errorf("company B's cost centre %s appears in company A's report", n.CentreID)
		}
	}

	// A's own axis id, asked for in B's scope, is UNKNOWN — not an empty report
	// and not A's data. The refusal names B's axes, which are B's alone.
	_, err = rep.CostCentreSummaryFor(ctxB, companyB, fA.deptID, aprilFrom, aprilTo)
	ae, ok := reporting.AsCostAxisError(err)
	if !ok {
		t.Fatalf("company A's axis id resolved inside company B's scope: %v", err)
	}
	for _, a := range ae.Available {
		if a.ID == fA.deptID || a.ID == fA.branchID {
			t.Errorf("company B's axis list offers company A's axis %s", a.ID)
		}
	}

	// And the category summary is scoped too: B sees three axes' worth of its own
	// numbers and none of A's.
	catB, err := rep.CostCategorySummaryFor(ctxB, companyB, aprilFrom, aprilTo)
	if err != nil {
		t.Fatalf("company B category summary: %v", err)
	}
	for _, r := range catB.Rows {
		if r.CategoryID == fA.deptID || r.CategoryID == fA.branchID {
			t.Errorf("company A's axis %s is a row in company B's summary", r.CategoryID)
		}
	}

	// THE CONTROL for the whole test: asking for A's company id from B's scope is
	// refused by the scope check itself, so the isolation above is not resting on
	// an accidentally-empty read.
	if _, err := rep.CostCentreSummaryFor(ctxB, companyA, fA.deptID, aprilFrom, aprilTo); err == nil {
		t.Error("a report for company A was built inside company B's transaction scope")
	}
}

// ---------------------------------------------------------------------------
// small helpers used only for failure messages
// ---------------------------------------------------------------------------

func rootNames(ns []reporting.CostCentreNode) []string {
	out := make([]string, 0, len(ns))
	for _, n := range ns {
		out = append(out, n.Name)
	}
	return out
}

func childNames(ns []reporting.CostCentreNode) []string { return rootNames(ns) }

func rowAxisNames(rows []reporting.CostCategoryRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.CategoryName)
	}
	return out
}
