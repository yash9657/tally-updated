package query_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/query"
	"github.com/khata/cored/internal/reporting"
)

// widen_execute_test.go is the LIVE-POSTGRES gate for the WS2 DSL widening. It
// uses its OWN seed (seedOpeningsAndMovement) because the pre-existing seedSales
// creates every ledger through identity.CreateLedger, which has no opening-
// balance parameter — so a closing_net test built on it would be VACUOUS
// (closing_net would trivially equal net). This seed carries a 'D' opening, a
// 'C' opening, an opening on a ledger that ALSO has postings, and — the case
// that actually catches the missing-COALESCE bug — openings on ledgers with
// ZERO postings (hence NO ledger_balance row at all).
//
// Setup helpers (setupCompany, dec, adminDSN/appDSN) are reused from
// execute_test.go / query_security_test.go; nothing is redeclared here.

// seededLedger describes one ledger this file seeds, with the opening it was
// created with. The hand-computed closing figures live in the tests.
type seededLedger struct {
	name    string
	group   string
	opening string // absolute value, fixed-4 friendly
	dc      string // "D" | "C"
}

// openingLedgers is the seed's master list. It is BALANCED (100000 D vs
// 100000 C) so the Trial Balance oracle is a meaningful reconciliation.
var openingLedgers = []seededLedger{
	// --- trap cases: non-zero opening, ZERO postings -> no ledger_balance row.
	{"Owner Capital", "Capital Account", "100000", "C"},
	{"Opening Stock", "Stock-in-Hand", "60000", "D"},
	// --- opening AND posted movement.
	{"Cash A/c", "Cash-in-Hand", "40000", "D"},
	// --- zero-opening operating ledgers.
	{"Acme Corp", "Sundry Debtors", "0", "D"},
	{"Zenith Traders", "Sundry Debtors", "0", "D"},
	{"Domestic Sales", "Sales Accounts", "0", "C"},
	{"Local Purchase", "Purchase Accounts", "0", "D"},
}

// seedOpeningsAndMovement creates the ledgers above (via masters, the ONLY
// service that can set an opening balance) and posts three vouchers spanning two
// calendar quarters and two calendar years:
//
//	2025-05-15 (2025-Q2) Sales  INV-A: Acme Dr 12000 / Domestic Sales Cr 12000
//	2025-06-10 (2025-Q2) Purch  PB-A : Local Purchase Dr 7000 / Cash A/c Cr 7000
//	2026-01-20 (2026-Q1) Sales  INV-Z: Zenith Dr 5000 / Domestic Sales Cr 5000
//
// Every figure asserted downstream is derived BY HAND from this table, never
// from the query engine.
func seedOpeningsAndMovement(t *testing.T, pool *db.Pool, ctx context.Context, ids *identity.Service) map[string]uuid.UUID {
	t.Helper()
	ms := masters.NewService(pool)
	led := ledger.NewService(pool)

	byName := map[string]uuid.UUID{}
	for _, sl := range openingLedgers {
		id, err := ms.CreateLedger(ctx, masters.Ledger{
			Name:           sl.name,
			GroupName:      sl.group,
			OpeningBalance: dec(sl.opening),
			OpeningDC:      sl.dc,
		}, "tester")
		if err != nil {
			t.Fatalf("create ledger %s: %v", sl.name, err)
		}
		byName[sl.name] = id
	}

	salesVT, err := ids.VoucherTypeID(ctx, "Sales")
	if err != nil {
		t.Fatalf("sales vt: %v", err)
	}
	purchaseVT, err := ids.VoucherTypeID(ctx, "Purchase")
	if err != nil {
		t.Fatalf("purchase vt: %v", err)
	}

	post := func(vt uuid.UUID, class ledger.Class, party uuid.UUID, no, date string,
		ps []ledger.PostingInput, ba []ledger.BillAllocationInput) {
		if _, err := led.PostVoucher(ctx, ledger.VoucherInput{
			Class: class, VoucherTypeID: vt, PartyLedgerID: party, VoucherNo: no, VoucherDate: date,
			Party: "p", Narration: "n", CreatedBy: "tester", Postings: ps, BillAllocations: ba,
		}); err != nil {
			t.Fatalf("post %s: %v", no, err)
		}
	}

	acme, zenith := byName["Acme Corp"], byName["Zenith Traders"]
	sales, cash, purch := byName["Domestic Sales"], byName["Cash A/c"], byName["Local Purchase"]

	post(salesVT, ledger.Sales, acme, "INV-A", "2025-05-15",
		[]ledger.PostingInput{
			{LedgerID: acme, DrCr: ledger.Debit, Amount: dec("12000")},
			{LedgerID: sales, DrCr: ledger.Credit, Amount: dec("12000")},
		},
		[]ledger.BillAllocationInput{{LedgerID: acme, RefName: "INV-A", RefType: ledger.RefNew, DrCr: ledger.Debit, Amount: dec("12000"), DueDate: "2025-06-15"}})

	post(purchaseVT, ledger.Purchase, cash, "PB-A", "2025-06-10",
		[]ledger.PostingInput{
			{LedgerID: purch, DrCr: ledger.Debit, Amount: dec("7000")},
			{LedgerID: cash, DrCr: ledger.Credit, Amount: dec("7000")},
		}, nil)

	post(salesVT, ledger.Sales, zenith, "INV-Z", "2026-01-20",
		[]ledger.PostingInput{
			{LedgerID: zenith, DrCr: ledger.Debit, Amount: dec("5000")},
			{LedgerID: sales, DrCr: ledger.Credit, Amount: dec("5000")},
		},
		[]ledger.BillAllocationInput{{LedgerID: zenith, RefName: "INV-Z", RefType: ledger.RefNew, DrCr: ledger.Debit, Amount: dec("5000"), DueDate: "2026-02-20"}})

	return byName
}

// runQuery is a small helper that executes and indexes the result by the first
// (dimension) column.
func runQuery(t *testing.T, ctx context.Context, pool *db.Pool, q query.Query) (query.Result, map[string]string) {
	t.Helper()
	res, err := query.Execute(ctx, pool, q)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	idx := map[string]string{}
	for _, r := range res.Rows {
		if len(r) >= 2 {
			idx[r[0]] = r[1]
		}
	}
	return res, idx
}

// --------------------------------------------------------------------------
// The flagship gate: closing_net reconciles with the Trial Balance report.
// --------------------------------------------------------------------------

// TestExecute_ClosingNetEqualsTrialBalance is the WS2 flagship acceptance check.
// The ORACLE IS INDEPENDENT: reporting.Service.TrialBalance reads the same rows
// through a completely separate Go code path (loadLedgerBalances + per-ledger
// netting), not through the query compiler. For every Trial Balance row the
// expected value is Debit - Credit (the report nets each ledger to ONE side), and
// that must equal sum(closing_net) for the same ledger_name.
//
// NON-VACUOUS, three ways:
//   - the seed carries non-zero openings, so closing_net != net for real ledgers
//     (asserted explicitly below); a compiler that ignored the opening fold fails;
//   - two of those ledgers have NO postings at all, so the inner COALESCE trap is
//     exercised (without it their openings vanish and the comparison fails);
//   - the row sets are compared for completeness, so a query that silently
//     dropped ledgers fails.
func TestExecute_ClosingNetEqualsTrialBalance(t *testing.T) {
	pool, ctx, companyID, ids := setupCompany(t, "Closing Co")
	seedOpeningsAndMovement(t, pool, ctx, ids)

	// --- independent oracle ---
	tb, err := reporting.NewService(pool).TrialBalance(ctx, companyID)
	if err != nil {
		t.Fatalf("trial balance oracle: %v", err)
	}
	if !tb.IsBalanced() {
		t.Fatalf("oracle Trial Balance is not balanced (seed is wrong): Dr=%s Cr=%s",
			tb.TotalDebit.StringFixed(4), tb.TotalCredit.StringFixed(4))
	}
	want := map[string]decimal.Decimal{}
	for _, row := range tb.Rows {
		want[row.LedgerName] = row.Debit.Sub(row.Credit)
	}
	if len(want) == 0 {
		t.Fatal("oracle produced no rows")
	}

	// --- system under test ---
	_, gotClosing := runQuery(t, ctx, pool, query.Query{
		Entity:     query.EntityLedgerBalances,
		Dimensions: []string{"ledger_name"},
		Metrics:    []query.Metric{{Func: query.AggSum, Column: "closing_net", As: "closing"}},
		Limit:      1000,
	})
	// The movement-only metric, for the vacuity proof below.
	_, gotNet := runQuery(t, ctx, pool, query.Query{
		Entity:     query.EntityLedgerBalances,
		Dimensions: []string{"ledger_name"},
		Metrics:    []query.Metric{{Func: query.AggSum, Column: "net", As: "net"}},
		Limit:      1000,
	})

	if len(gotClosing) != len(want) {
		t.Fatalf("row-set size mismatch: query %d ledgers, Trial Balance %d ledgers", len(gotClosing), len(want))
	}
	for name, w := range want {
		got, ok := gotClosing[name]
		if !ok {
			t.Errorf("ledger %q present in the Trial Balance but missing from closing_net", name)
			continue
		}
		if got != w.StringFixed(4) {
			t.Errorf("ledger %q: closing_net = %s, Trial Balance (Debit-Credit) = %s", name, got, w.StringFixed(4))
		}
	}

	// VACUITY PROOF: for at least one ledger the opening fold must actually
	// CHANGE the answer. If this ever stops holding, the test above no longer
	// distinguishes closing_net from net and must be re-seeded.
	diverged := 0
	for name := range want {
		if gotNet[name] != gotClosing[name] {
			diverged++
		}
	}
	if diverged < 2 {
		t.Fatalf("vacuity guard: only %d ledgers where closing_net differs from movement-only net; "+
			"the seed no longer distinguishes the opening fold", diverged)
	}
}

// TestExecute_OpeningSurvivesWithZeroPostings is the DEDICATED regression for the
// missing-inner-COALESCE trap. "Owner Capital" (100000 C) and "Opening Stock"
// (60000 D) have NO postings, hence NO ledger_balance row, hence NULL lb.debit /
// lb.credit from the LEFT JOIN. If the closing_net expression drops either inner
// COALESCE, NULL propagates, SUM skips the row, and these openings silently
// report 0.0000 instead of their true value.
//
// NON-VACUOUS: it asserts the exact hand-computed openings, and it separately
// proves the movement-only `net` for the same ledgers IS 0 — so the test cannot
// pass by accident on a compiler that ignores openings.
func TestExecute_OpeningSurvivesWithZeroPostings(t *testing.T) {
	pool, ctx, _, ids := setupCompany(t, "Trap Co")
	seedOpeningsAndMovement(t, pool, ctx, ids)

	_, closing := runQuery(t, ctx, pool, query.Query{
		Entity:     query.EntityLedgerBalances,
		Dimensions: []string{"ledger_name"},
		Metrics:    []query.Metric{{Func: query.AggSum, Column: "closing_net", As: "closing"}},
		Limit:      1000,
	})
	_, opening := runQuery(t, ctx, pool, query.Query{
		Entity:     query.EntityLedgerBalances,
		Dimensions: []string{"ledger_name"},
		Metrics:    []query.Metric{{Func: query.AggSum, Column: "opening", As: "opening"}},
		Limit:      1000,
	})
	_, movement := runQuery(t, ctx, pool, query.Query{
		Entity:     query.EntityLedgerBalances,
		Dimensions: []string{"ledger_name"},
		Metrics:    []query.Metric{{Func: query.AggSum, Column: "net", As: "net"}},
		Limit:      1000,
	})

	// Hand-computed from the seed table (debit-positive orientation).
	cases := []struct {
		ledger      string
		wantOpening string
		wantClosing string
		wantNet     string // movement only
	}{
		{"Owner Capital", "-100000.0000", "-100000.0000", "0.0000"}, // C opening, no postings
		{"Opening Stock", "60000.0000", "60000.0000", "0.0000"},     // D opening, no postings
		{"Cash A/c", "40000.0000", "33000.0000", "-7000.0000"},      // 40000 opening, Cr 7000
		{"Acme Corp", "0.0000", "12000.0000", "12000.0000"},         // no opening, Dr 12000
		{"Domestic Sales", "0.0000", "-17000.0000", "-17000.0000"},  // no opening, Cr 17000
	}
	for _, tc := range cases {
		if got := opening[tc.ledger]; got != tc.wantOpening {
			t.Errorf("%s: opening = %q, want %q", tc.ledger, got, tc.wantOpening)
		}
		if got := closing[tc.ledger]; got != tc.wantClosing {
			t.Errorf("%s: closing_net = %q, want %q", tc.ledger, got, tc.wantClosing)
		}
		if got := movement[tc.ledger]; got != tc.wantNet {
			t.Errorf("%s: net (movement only) = %q, want %q", tc.ledger, got, tc.wantNet)
		}
	}
}

// TestExecute_ClosingNetByGroupSumsToZero proves the group-wise rollup is
// arithmetically sound (no LEFT-JOIN fan-out multiplying openings): summed over
// every group, the closing position of a balanced book is exactly zero.
func TestExecute_ClosingNetByGroupSumsToZero(t *testing.T) {
	pool, ctx, _, ids := setupCompany(t, "GroupSum Co")
	seedOpeningsAndMovement(t, pool, ctx, ids)

	res, _ := runQuery(t, ctx, pool, query.Query{
		Entity:     query.EntityLedgerBalances,
		Dimensions: []string{"group_name"},
		Metrics:    []query.Metric{{Func: query.AggSum, Column: "closing_net", As: "closing"}},
		Limit:      1000,
	})
	total := decimal.Zero
	nonZero := 0
	for _, r := range res.Rows {
		d, err := decimal.NewFromString(r[1])
		if err != nil {
			t.Fatalf("group %q: unparseable closing_net %q: %v", r[0], r[1], err)
		}
		if !d.IsZero() {
			nonZero++
		}
		total = total.Add(d)
	}
	if !total.IsZero() {
		t.Fatalf("group-wise closing_net total = %s, want 0 (balanced book)", total.StringFixed(4))
	}
	if nonZero < 4 {
		t.Fatalf("vacuity guard: only %d groups with a non-zero closing position; sum-to-zero proves nothing", nonZero)
	}
}

// --------------------------------------------------------------------------
// min / max / avg
// --------------------------------------------------------------------------

// TestExecute_MinMaxAvgOnPostingsAmount exercises the new aggregates against
// posting.amount — the only always-populated money column under any seed (NOT
// NULL, CHECK amount >= 0). The oracle is arithmetic done BY HAND from the seed
// table, not from the query engine.
//
// Debit postings in the seed: 12000 (Acme), 7000 (Local Purchase), 5000 (Zenith)
//
//	min = 5000, max = 12000, avg = 24000/3 = 8000
func TestExecute_MinMaxAvgOnPostingsAmount(t *testing.T) {
	pool, ctx, _, ids := setupCompany(t, "Agg Co")
	seedOpeningsAndMovement(t, pool, ctx, ids)

	res, err := query.Execute(ctx, pool, query.Query{
		Entity: query.EntityPostings,
		Metrics: []query.Metric{
			{Func: query.AggMin, Column: "amount", As: "lo"},
			{Func: query.AggMax, Column: "amount", As: "hi"},
			{Func: query.AggAvg, Column: "amount", As: "mean"},
			{Func: query.AggSum, Column: "amount", As: "total"},
			{Func: query.AggCount, Column: "*", As: "n"},
		},
		Filters: []query.Filter{{Column: "dr_cr", Op: query.OpEq, Values: []string{"D"}}},
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected one aggregate row, got %d: %+v", len(res.Rows), res.Rows)
	}
	row := res.Rows[0]
	for i, want := range []string{"5000.0000", "12000.0000", "8000.0000", "24000.0000", "3.0000"} {
		if row[i] != want {
			t.Errorf("column %q = %q, want %q (row=%v)", res.Columns[i].Name, row[i], want, row)
		}
	}
	// Every aggregate column is declared numeric on the wire.
	for _, c := range res.Columns {
		if c.Type != "numeric" {
			t.Errorf("column %q type = %q, want numeric", c.Name, c.Type)
		}
	}
}

// TestExecute_MinMaxAvgGroupedByLedger proves the new aggregates group correctly
// (they are not a single-row special case). Domestic Sales is credited twice:
// 12000 and 5000 -> min 5000, max 12000, avg 8500.
func TestExecute_MinMaxAvgGroupedByLedger(t *testing.T) {
	pool, ctx, _, ids := setupCompany(t, "AggGroup Co")
	seedOpeningsAndMovement(t, pool, ctx, ids)

	res, err := query.Execute(ctx, pool, query.Query{
		Entity:     query.EntityPostings,
		Dimensions: []string{"ledger_name"},
		Metrics: []query.Metric{
			{Func: query.AggMin, Column: "amount", As: "lo"},
			{Func: query.AggMax, Column: "amount", As: "hi"},
			{Func: query.AggAvg, Column: "amount", As: "mean"},
		},
		Filters: []query.Filter{{Column: "ledger_name", Op: query.OpEq, Values: []string{"Domestic Sales"}}},
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected one row for Domestic Sales, got %+v", res.Rows)
	}
	if got := res.Rows[0]; got[1] != "5000.0000" || got[2] != "12000.0000" || got[3] != "8500.0000" {
		t.Fatalf("Domestic Sales min/max/avg = %v, want [5000.0000 12000.0000 8500.0000]", got[1:])
	}
}

// TestExecute_MinMaxAvgNullRendersEmptyString PINS the NULL wire contract
// documented in whitelist.go: MIN/MAX/AVG are deliberately NOT COALESCE'd, so a
// group whose underlying values are all SQL NULL renders as the EMPTY STRING in
// a column typed "numeric". vv.taxable_value is NULL for every voucher this seed
// posts (VoucherInput carries no taxable/tax field), which makes the case
// reachable. sum() on the same column still returns 0.0000, proving the two
// paths are deliberately different rather than accidentally the same.
func TestExecute_MinMaxAvgNullRendersEmptyString(t *testing.T) {
	pool, ctx, _, ids := setupCompany(t, "NullAgg Co")
	seedOpeningsAndMovement(t, pool, ctx, ids)

	res, err := query.Execute(ctx, pool, query.Query{
		Entity: query.EntityVouchers,
		Metrics: []query.Metric{
			{Func: query.AggMin, Column: "taxable_value", As: "lo"},
			{Func: query.AggMax, Column: "taxable_value", As: "hi"},
			{Func: query.AggAvg, Column: "taxable_value", As: "mean"},
			{Func: query.AggSum, Column: "taxable_value", As: "total"},
		},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected one aggregate row, got %+v", res.Rows)
	}
	row := res.Rows[0]
	for i, name := range []string{"lo", "hi", "mean"} {
		if row[i] != "" {
			t.Errorf("%s over an all-NULL column = %q, want \"\" (the pinned NULL rendering)", name, row[i])
		}
	}
	if row[3] != "0.0000" {
		t.Errorf("sum over an all-NULL column = %q, want 0.0000 (sum IS COALESCE'd)", row[3])
	}
	if res.Columns[0].Type != "numeric" {
		t.Errorf("column type = %q, want numeric (the \"\" is inside a numeric column — the documented wrinkle)", res.Columns[0].Type)
	}
}

// TestExecute_AvgOnLedgerBalancesUsesSQLDenominator pins the AVG denominator
// decision recorded in whitelist.go: because "debit"/"credit" keep the BARE
// column as rawExpr (which is what keeps the SUM SQL byte-identical to the
// pre-WS2 compiler), ledgers with NO ledger_balance row are EXCLUDED from the
// average, whereas "closing_net" (which COALESCEs inside) counts every ledger.
//
// NON-VACUOUS: the two denominators are compared against independently counted
// row populations, so a change of either rawExpr flips an assertion.
func TestExecute_AvgOnLedgerBalancesUsesSQLDenominator(t *testing.T) {
	pool, ctx, _, ids := setupCompany(t, "AvgDenom Co")
	seedOpeningsAndMovement(t, pool, ctx, ids)

	// Independent populations, read straight from the query engine's *count*
	// (count(*) counts JOIN rows, unaffected by NULL) and from a per-ledger
	// listing of the movement-only net.
	resAll, _ := runQuery(t, ctx, pool, query.Query{
		Entity:     query.EntityLedgerBalances,
		Dimensions: []string{"ledger_name"},
		Metrics:    []query.Metric{{Func: query.AggSum, Column: "debit", As: "d"}},
		Limit:      1000,
	})
	totalLedgers := len(resAll.Rows)

	// Ledgers WITH a ledger_balance row are exactly those the postings touched.
	resPosted, _ := runQuery(t, ctx, pool, query.Query{
		Entity:     query.EntityPostings,
		Dimensions: []string{"ledger_name"},
		Metrics:    []query.Metric{{Func: query.AggCount, Column: "*", As: "n"}},
		Limit:      1000,
	})
	postedLedgers := len(resPosted.Rows)
	if postedLedgers == 0 || totalLedgers <= postedLedgers {
		t.Fatalf("vacuity guard: totalLedgers=%d postedLedgers=%d — the two denominators are not distinguishable",
			totalLedgers, postedLedgers)
	}

	res, err := query.Execute(ctx, pool, query.Query{
		Entity: query.EntityLedgerBalances,
		Metrics: []query.Metric{
			{Func: query.AggSum, Column: "debit", As: "sum_debit"},
			{Func: query.AggAvg, Column: "debit", As: "avg_debit"},
			{Func: query.AggSum, Column: "closing_net", As: "sum_closing"},
			{Func: query.AggAvg, Column: "closing_net", As: "avg_closing"},
		},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	row := res.Rows[0]
	sumDebit := decimal.RequireFromString(row[0])
	avgDebit := decimal.RequireFromString(row[1])
	sumClosing := decimal.RequireFromString(row[2])
	avgClosing := decimal.RequireFromString(row[3])

	// avg(debit) denominator == number of ledgers WITH a ledger_balance row.
	wantAvgDebit := sumDebit.Div(decimal.NewFromInt(int64(postedLedgers)))
	if !avgDebit.Round(4).Equal(wantAvgDebit.Round(4)) {
		t.Errorf("avg(debit) = %s, want %s (sum %s / %d posted ledgers) — the bare-column denominator",
			avgDebit.StringFixed(4), wantAvgDebit.StringFixed(4), sumDebit.StringFixed(4), postedLedgers)
	}
	// avg(closing_net) denominator == ALL ledgers (inner COALESCE).
	wantAvgClosing := sumClosing.Div(decimal.NewFromInt(int64(totalLedgers)))
	if !avgClosing.Round(4).Equal(wantAvgClosing.Round(4)) {
		t.Errorf("avg(closing_net) = %s, want %s (sum %s / %d total ledgers) — the COALESCE'd denominator",
			avgClosing.StringFixed(4), wantAvgClosing.StringFixed(4), sumClosing.StringFixed(4), totalLedgers)
	}
}

// --------------------------------------------------------------------------
// quarter / year time buckets
// --------------------------------------------------------------------------

// TestExecute_QuarterAndYearBuckets pins the ACTUAL bucket LABELS (not just that
// grouping happened): a 2025-05-15 voucher lands in "2025-Q2" and a 2026-01-20
// voucher in "2026-Q1" — the CALENDAR quarter, which is deliberately NOT the
// Indian financial-year quarter (see whitelist.go). Counts are hand-derived from
// the seed: 2 vouchers in 2025-Q2, 1 in 2026-Q1.
func TestExecute_QuarterAndYearBuckets(t *testing.T) {
	pool, ctx, _, ids := setupCompany(t, "Bucket Co")
	seedOpeningsAndMovement(t, pool, ctx, ids)

	_, byQuarter := runQuery(t, ctx, pool, query.Query{
		Entity:     query.EntityVouchers,
		Dimensions: []string{"quarter"},
		Metrics:    []query.Metric{{Func: query.AggCount, Column: "*", As: "n"}},
		OrderBy:    []query.OrderTerm{{Column: "quarter"}},
		Limit:      100,
	})
	if byQuarter["2025-Q2"] != "2.0000" {
		t.Errorf("2025-Q2 voucher count = %q, want 2.0000 (got buckets %v)", byQuarter["2025-Q2"], byQuarter)
	}
	if byQuarter["2026-Q1"] != "1.0000" {
		t.Errorf("2026-Q1 voucher count = %q, want 1.0000 (got buckets %v)", byQuarter["2026-Q1"], byQuarter)
	}
	if len(byQuarter) != 2 {
		t.Errorf("expected exactly 2 quarter buckets, got %v", byQuarter)
	}

	_, byYear := runQuery(t, ctx, pool, query.Query{
		Entity:     query.EntityVouchers,
		Dimensions: []string{"year"},
		Metrics:    []query.Metric{{Func: query.AggCount, Column: "*", As: "n"}},
		OrderBy:    []query.OrderTerm{{Column: "year"}},
		Limit:      100,
	})
	if byYear["2025"] != "2.0000" || byYear["2026"] != "1.0000" {
		t.Errorf("year buckets = %v, want {2025:2, 2026:1}", byYear)
	}

	// The SAME buckets on postings, where the amounts are known by hand:
	// 2025-Q2 debits = 12000 (Acme) + 7000 (Local Purchase) = 19000;
	// 2026-Q1 debits = 5000 (Zenith).
	_, postingsByQuarter := runQuery(t, ctx, pool, query.Query{
		Entity:     query.EntityPostings,
		Dimensions: []string{"quarter"},
		Metrics:    []query.Metric{{Func: query.AggSum, Column: "amount", As: "total"}},
		Filters:    []query.Filter{{Column: "dr_cr", Op: query.OpEq, Values: []string{"D"}}},
		OrderBy:    []query.OrderTerm{{Column: "quarter"}},
		Limit:      100,
	})
	if postingsByQuarter["2025-Q2"] != "19000.0000" || postingsByQuarter["2026-Q1"] != "5000.0000" {
		t.Errorf("postings by quarter = %v, want {2025-Q2:19000.0000, 2026-Q1:5000.0000}", postingsByQuarter)
	}
}

// TestExecute_QuarterIsCalendarNotFinancialYear makes the pinned (and
// CA-surprising) semantic explicit: an April voucher and a May voucher share a
// bucket, and that bucket is Q2 — calendar. Under an Indian FY quartering they
// would be Q1. Documented in whitelist.go and surfaced for HANDOFF §6.
func TestExecute_QuarterIsCalendarNotFinancialYear(t *testing.T) {
	pool, ctx, _, ids := setupCompany(t, "CalQ Co")
	seedOpeningsAndMovement(t, pool, ctx, ids)

	_, byQuarter := runQuery(t, ctx, pool, query.Query{
		Entity:     query.EntityVouchers,
		Dimensions: []string{"quarter"},
		Metrics:    []query.Metric{{Func: query.AggCount, Column: "*", As: "n"}},
		DateRange:  &query.DateRange{Column: "voucher_date", From: "2025-04-01", To: "2025-06-30"},
		Limit:      100,
	})
	if _, ok := byQuarter["2025-Q1"]; ok {
		t.Fatalf("April-June vouchers landed in 2025-Q1 — quarter is no longer the CALENDAR quarter: %v", byQuarter)
	}
	if byQuarter["2025-Q2"] != "2.0000" {
		t.Fatalf("April-June bucket = %v, want 2025-Q2 with 2 vouchers (calendar quartering)", byQuarter)
	}
}

// --------------------------------------------------------------------------
// top-N (order_by + limit) — proven at the DB level, not just at compile time
// --------------------------------------------------------------------------

// TestExecute_TopNReturnsLargestRows proves order_by on a metric ALIAS plus a
// small limit returns the RIGHT rows from Postgres, not merely that it compiles.
// Seed outstandings: Acme Corp 12000, Zenith Traders 5000.
//
// NON-VACUOUS: it asserts the identity AND the amount of the top row, and
// separately asserts the ascending order returns the OTHER ledger — so a query
// that ignored the sort direction fails.
func TestExecute_TopNReturnsLargestRows(t *testing.T) {
	pool, ctx, _, ids := setupCompany(t, "TopN Co")
	seedOpeningsAndMovement(t, pool, ctx, ids)

	top := func(desc bool, limit int) query.Result {
		res, err := query.Execute(ctx, pool, query.Query{
			Entity:     query.EntityOutstandings,
			Dimensions: []string{"ledger_name"},
			Metrics:    []query.Metric{{Func: query.AggSum, Column: "pending", As: "pending"}},
			Filters:    []query.Filter{{Column: "nature", Op: query.OpEq, Values: []string{"ASSET"}}},
			OrderBy:    []query.OrderTerm{{Column: "pending", Desc: desc}},
			Limit:      limit,
		})
		if err != nil {
			t.Fatalf("execute (desc=%v): %v", desc, err)
		}
		return res
	}

	// Both debtors are visible without a limit.
	all := top(true, 100)
	if len(all.Rows) != 2 {
		t.Fatalf("expected 2 debtors, got %+v", all.Rows)
	}
	if all.Rows[0][0] != "Acme Corp" || all.Rows[0][1] != "12000.0000" {
		t.Fatalf("top debtor = %v, want [Acme Corp 12000.0000]", all.Rows[0])
	}
	if all.Rows[1][0] != "Zenith Traders" || all.Rows[1][1] != "5000.0000" {
		t.Fatalf("second debtor = %v, want [Zenith Traders 5000.0000]", all.Rows[1])
	}

	// Top-1 descending picks the LARGEST.
	one := top(true, 1)
	if len(one.Rows) != 1 || one.Rows[0][0] != "Acme Corp" {
		t.Fatalf("top-1 desc = %+v, want exactly [[Acme Corp 12000.0000]]", one.Rows)
	}
	// Top-1 ascending picks the SMALLEST — proves the direction is honoured.
	asc := top(false, 1)
	if len(asc.Rows) != 1 || asc.Rows[0][0] != "Zenith Traders" {
		t.Fatalf("top-1 asc = %+v, want exactly [[Zenith Traders 5000.0000]]", asc.Rows)
	}
}

// TestExecute_FakeTranslatorClosingBalancesEndToEnd drives the NEW canonical
// question through the full Service seam (translate -> compile -> execute) and
// reconciles it against the independent Trial Balance oracle, proving the
// translator mapping is wired to a metric that actually answers the question.
func TestExecute_FakeTranslatorClosingBalancesEndToEnd(t *testing.T) {
	pool, ctx, companyID, ids := setupCompany(t, "NLClosing Co")
	seedOpeningsAndMovement(t, pool, ctx, ids)

	svc := query.NewService(pool, query.FakeTranslator{})
	q, err := svc.Translate(ctx, companyID, "closing balances")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	res, err := svc.Run(ctx, q)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	tb, err := reporting.NewService(pool).TrialBalance(ctx, companyID)
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}
	want := map[string]string{}
	for _, row := range tb.Rows {
		want[row.LedgerName] = row.Debit.Sub(row.Credit).StringFixed(4)
	}
	if len(res.Rows) == 0 {
		t.Fatal("closing balances returned no rows")
	}
	for _, r := range res.Rows {
		if w, ok := want[r[0]]; !ok {
			t.Errorf("ledger %q not in the Trial Balance oracle", r[0])
		} else if r[1] != w {
			t.Errorf("ledger %q: NL closing = %q, Trial Balance = %q", r[0], r[1], w)
		}
	}
	// Sanity: the seeded, unposted capital opening is present with its real value.
	found := false
	for _, r := range res.Rows {
		if r[0] == "Owner Capital" {
			found = true
			if r[1] != "-100000.0000" {
				t.Errorf("Owner Capital closing = %q, want -100000.0000", r[1])
			}
		}
	}
	if !found {
		t.Error("Owner Capital (opening-only ledger) missing from the NL closing-balances answer")
	}
}
