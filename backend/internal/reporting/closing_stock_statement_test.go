package reporting_test

// closing_stock_statement_test.go — CLOSING STOCK ON THE BALANCE SHEET AND THE
// TRADING TERMS ON THE P&L, against the live database.
//
// THE ONE THING TO UNDERSTAND BEFORE READING ANY ASSERTION HERE
// -------------------------------------------------------------
// The fold-in adds the SAME figure S to Assets and to net profit. With A = L +
// NP by double entry, Assets' - Liabilities' = (A + S) - (L + NP + S) = 0. S
// CANCELS. So `bs.IsBalanced()` is true for ANY value of S, INCLUDING A WRONG
// ONE. Balance can catch exactly one bug class — an ASYMMETRIC fold-in (an asset
// without the P&L term, or the two taken at different dates) — and nothing more.
//
// The NUMBER is therefore pinned by a SECOND INDEPENDENT PATH: the Stock
// Summary, which derives the same position by the same replay but is rendered by
// a different report. `BS "Closing Stock" == StockSummary(asOn).TotalValue` is
// the load-bearing assertion of this whole feature, and
// TestClosingStockOnTheBalanceSheetEqualsTheStockSummaryTotal is where it lives.
// TestBalanceSheetWithStockStillBalancesAtEveryMonthEnd deliberately carries a
// second half (Assets moved by exactly the stock figure against a twin company
// with no stock) precisely because its balance half cannot fail on its own.
//
// ABSENT vs 0
// -----------
// A company with no inventory must produce a tree BYTE-IDENTICAL to the one it
// produced before this feature existed: no "Closing Stock" row, and no
// resurrected empty "Stock-in-Hand" group either. That is why every lookup here
// returns (amount, found) and never a bare decimal — a helper that answered
// decimal.Zero for a missing name could not tell "absent" from "present with
// amount 0", which is exactly the vacuous test this feature must avoid.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/reporting"
)

// --------------------------------------------------------------------------
// Tree lookups that distinguish ABSENT from present-with-zero
// --------------------------------------------------------------------------

// findStatementNode walks a statement tree depth-first and returns the first
// node with the given name: its amount, the chain of ancestor names above it,
// and whether it was found at all.
//
// The `found` flag is the whole point. debtorsFromBalanceSheet in
// outstanding_version_test.go returns decimal.Zero for a name it cannot find and
// is therefore unusable here: "no Closing Stock row" and "a Closing Stock row
// reading 0" are the two outcomes this file exists to tell apart.
func findStatementNode(nodes []reporting.StatementNode, name string) (decimal.Decimal, []string, bool) {
	var (
		amount decimal.Decimal
		path   []string
		found  bool
		walk   func(ns []reporting.StatementNode, ancestors []string)
	)
	walk = func(ns []reporting.StatementNode, ancestors []string) {
		for _, n := range ns {
			if found {
				return
			}
			if n.Name == name {
				amount, path, found = n.Amount, append([]string(nil), ancestors...), true
				return
			}
			walk(n.Children, append(ancestors, n.Name))
		}
	}
	walk(nodes, nil)
	return amount, path, found
}

// countStatementNodes counts every node with the given name at any depth, so a
// fold-in that injected the asset twice cannot hide behind a first-match lookup.
func countStatementNodes(nodes []reporting.StatementNode, name string) int {
	n := 0
	var walk func(ns []reporting.StatementNode)
	walk = func(ns []reporting.StatementNode) {
		for _, node := range ns {
			if node.Name == name {
				n++
			}
			walk(node.Children)
		}
	}
	walk(nodes)
	return n
}

// describeTree renders a statement tree for a failure message. A failure that
// says only "Closing Stock not found" sends the reader to the debugger; one that
// prints the tree usually answers the question on the spot.
func describeTree(nodes []reporting.StatementNode) string {
	var b strings.Builder
	var walk func(ns []reporting.StatementNode, depth int)
	walk = func(ns []reporting.StatementNode, depth int) {
		for _, n := range ns {
			b.WriteString("\n    " + strings.Repeat("  ", depth) +
				string(n.Kind) + " " + n.Name + " = " + n.Amount.StringFixed(4))
			walk(n.Children, depth+1)
		}
	}
	walk(nodes, 0)
	if b.Len() == 0 {
		return " (empty)"
	}
	return b.String()
}

// --------------------------------------------------------------------------
// Fixture helpers on the shared stockBooks harness (stock_summary_test.go)
// --------------------------------------------------------------------------

// balanceSheet reads the Balance Sheet, failing the test on error. Tests that
// EXPECT an error call rep.BalanceSheet directly.
func (b *stockBooks) balanceSheet(t *testing.T, asOn string) reporting.BalanceSheet {
	t.Helper()
	bs, err := b.rep.BalanceSheet(b.ctx, b.company, asOn)
	if err != nil {
		t.Fatalf("balance sheet as on %q: %v", asOn, err)
	}
	return bs
}

func (b *stockBooks) profitLoss(t *testing.T, from, to string) reporting.ProfitAndLoss {
	t.Helper()
	pl, err := b.rep.ProfitAndLoss(b.ctx, b.company, from, to)
	if err != nil {
		t.Fatalf("p&l %q..%q: %v", from, to, err)
	}
	return pl
}

// closingStockOn returns the Balance Sheet's "Closing Stock" figure and whether
// the node exists at all.
func (b *stockBooks) closingStockOn(t *testing.T, asOn string) (decimal.Decimal, []string, bool) {
	t.Helper()
	return findStatementNode(b.balanceSheet(t, asOn).Assets.Nodes, "Closing Stock")
}

// postPlain writes a voucher with the SAME accounting legs b.post uses (Dr
// Purchases / Cr the creditor) and NO inventory lines. It exists so a twin
// company can carry an identical accounting plane with an empty warehouse, which
// is what turns "the sheet still balances" into a test that can fail.
func (b *stockBooks) postPlain(t *testing.T, date, amount string) uuid.UUID {
	t.Helper()
	res, err := b.led.PostVoucher(b.ctx, ledger.VoucherInput{
		Class:       ledger.Purchase,
		VoucherDate: date,
		Party:       "Stock Supplies",
		CreatedBy:   "closing-stock-test",
		Postings: []ledger.PostingInput{
			{LedgerID: b.purchase, DrCr: ledger.Debit, Amount: dec(amount)},
			{LedgerID: b.creditor, DrCr: ledger.Credit, Amount: dec(amount)},
		},
	})
	if err != nil {
		t.Fatalf("post plain voucher of %s on %s: %v", amount, date, err)
	}
	return res.VoucherID
}

// postCashIn debits Cash, so the Assets side carries a real accounting figure
// alongside the derived stock and the tests are not asserting over a sheet whose
// only asset is the thing under test.
func (b *stockBooks) postCashIn(t *testing.T, date, amount string) {
	t.Helper()
	if _, err := b.led.PostVoucher(b.ctx, ledger.VoucherInput{
		Class:       ledger.Purchase,
		VoucherDate: date,
		Party:       "Stock Supplies",
		CreatedBy:   "closing-stock-test",
		Postings: []ledger.PostingInput{
			{LedgerID: b.namedLedger(t, "Cash"), DrCr: ledger.Debit, Amount: dec(amount)},
			{LedgerID: b.creditor, DrCr: ledger.Credit, Amount: dec(amount)},
		},
	}); err != nil {
		t.Fatalf("post cash receipt of %s on %s: %v", amount, date, err)
	}
}

func (b *stockBooks) namedLedger(t *testing.T, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := b.pool.RunInTx(b.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id FROM ledger WHERE name = $1`, name).Scan(&id)
	}); err != nil {
		t.Fatalf("resolve ledger %q: %v", name, err)
	}
	return id
}

// newExpiringBatch plants a batch that has already expired by the report date.
// stock_summary_test.go's newBatch carries no dates, and an expiry test needs
// one that is genuinely in the past or it proves nothing.
func (b *stockBooks) newExpiringBatch(t *testing.T, item uuid.UUID, name, expiry string) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new batch id: %v", err)
	}
	if err := b.pool.RunInTx(b.ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO stock_batch (id, company_id, stock_item_id, name, expiry_date)
			 VALUES ($1, $2, $3, $4, $5::date)`,
			id, b.company, item, name, expiry)
		return err
	}); err != nil {
		t.Fatalf("create expiring batch %q: %v", name, err)
	}
	return id
}

// stockLedgerUnder creates an ORDINARY ledger under Stock-in-Hand and gives it a
// balance with a journal — the chart of accounts
// corpora/migration/01-opening-trading.xml plants and that
// internal/query/widen_execute_test.go repeats.
func (b *stockBooks) stockLedgerUnder(t *testing.T, name, date, amount string) uuid.UUID {
	t.Helper()
	ids := identity.NewService(b.pool)
	id, err := ids.CreateLedger(b.ctx, name, "Stock-in-Hand")
	if err != nil {
		t.Fatalf("create ledger %q under Stock-in-Hand: %v", name, err)
	}
	postJournalOn(t, b.pool, b.ctx, ids, date, id, b.creditor, amount)
	return id
}

// --------------------------------------------------------------------------
// THE NUMBER
// --------------------------------------------------------------------------

// TestClosingStockOnTheBalanceSheetEqualsTheStockSummaryTotal is the assertion
// the whole feature rests on.
//
// The Balance Sheet's stock figure and the Stock Summary's TotalValue are the
// same position reached by the same replay but rendered by two different
// reports. If they ever disagree, a user reading the Stock Summary and a user
// reading the Balance Sheet are being told two different things about one
// warehouse, and only one of them can file a return from it.
//
// It sweeps five dates rather than one convenient one, because a single date
// proves only that the two agree there: before the first movement (opening stock
// only), exactly ON a movement date, mid-period, after the last movement, and
// unbounded. balanced() is NOT part of this test's evidence — see the file
// header for why it could not be.
func TestClosingStockOnTheBalanceSheetEqualsTheStockSummaryTotal(t *testing.T) {
	b := stockSetup(t) // books begin 2025-04-01
	item := b.newItem(t, "Copper Rod")

	// The books opened owning 20 @ 5.0000 = 100.0000, stated as at the books'
	// beginning. It is what makes the before-first-movement date non-zero, and a
	// zero date could not tell a working fold-in from a missing one.
	b.opening(t, item, b.main, uuid.Nil, "20", "5")
	b.post(t, "2025-05-01", stockLine(item, b.main, uuid.Nil, ledger.Inward, "100", "10", "0"))
	b.post(t, "2025-06-15", stockLine(item, b.main, uuid.Nil, ledger.Outward, "30", "50", "0"))
	b.post(t, "2025-08-01", stockLine(item, b.main, uuid.Nil, ledger.Inward, "50", "14", "0"))

	dates := []struct{ asOn, why string }{
		{"2025-04-15", "before the first movement — the opening position alone"},
		{"2025-05-01", "exactly ON a movement date"},
		{"2025-06-20", "mid-period, after an issue has consumed a blended cost"},
		{"2025-12-31", "after the last movement"},
		{"", "unbounded: everything ever posted"},
	}

	seen := map[string]bool{}
	for _, d := range dates {
		want := b.summary(t, d.asOn).TotalValue
		got, path, ok := b.closingStockOn(t, d.asOn)
		if !ok {
			t.Errorf("as on %q (%s) the Balance Sheet carries NO Closing Stock node, but the Stock "+
				"Summary values the same position at %s — the asset is missing from the sheet entirely.%s",
				d.asOn, d.why, want.StringFixed(4),
				describeTree(b.balanceSheet(t, d.asOn).Assets.Nodes))
			continue
		}
		if !got.Equal(want) {
			t.Errorf("as on %q (%s) the Balance Sheet states closing stock of %s while the Stock Summary "+
				"values the SAME position at %s: two reports of one warehouse disagree by %s",
				d.asOn, d.why, got.StringFixed(4), want.StringFixed(4), got.Sub(want).StringFixed(4))
		}
		if want.IsZero() {
			t.Errorf("as on %q (%s) the position is worth nothing, so this row of the sweep cannot "+
				"distinguish a correct fold-in from a missing one — fix the fixture", d.asOn, d.why)
		}
		seen[want.String()] = true

		// Nesting. A top-level Closing Stock node would leave Current Assets
		// subtotalling WITHOUT the stock while Assets.Total included it: an
		// internally inconsistent sheet that still foots.
		if len(path) != 2 || path[0] != "Current Assets" || path[1] != "Stock-in-Hand" {
			t.Errorf("as on %q the Closing Stock node sits under %v, want [Current Assets Stock-in-Hand] — "+
				"nested anywhere else, Current Assets subtotals without the stock that Assets.Total includes",
				d.asOn, path)
		}
		if n := countStatementNodes(b.balanceSheet(t, d.asOn).Assets.Nodes, "Closing Stock"); n != 1 {
			t.Errorf("as on %q the Assets tree carries %d Closing Stock nodes, want exactly 1", d.asOn, n)
		}
	}

	// The sweep must actually move. If every date reported the same figure, a
	// fold-in that ignored `asOn` entirely would pass all of the above.
	if len(seen) < 3 {
		t.Errorf("the five swept dates produced only %d distinct closing-stock figures (%v); a fold-in "+
			"that ignored the as-on date would survive this test", len(seen), seen)
	}
}

// --------------------------------------------------------------------------
// BALANCE — and the second half that makes it able to fail
// --------------------------------------------------------------------------

// TestBalanceSheetWithStockStillBalancesAtEveryMonthEnd pins the ONE bug class
// `balanced` can catch: an ASYMMETRIC fold-in, where the asset moves and the
// net-profit term does not (or the two are taken at different dates).
//
// Its balance half CANNOT fail for a merely wrong number — S cancels out of
// Assets - Liabilities, so any S balances. The second half is therefore not
// decoration: an identical TWIN company carries the same accounting vouchers
// with no inventory lines at all, and the difference between the two sheets'
// Assets.Total must be EXACTLY the closing stock. That is the part that fails
// when the asset is injected at the wrong value, in the wrong place, or twice.
func TestBalanceSheetWithStockStillBalancesAtEveryMonthEnd(t *testing.T) {
	withStock := stockSetup(t)
	noStock := stockSetup(t)
	item := withStock.newItem(t, "Brass Sheet")

	// A real accounting asset on both books, so Assets.Total is not composed
	// solely of the figure under test.
	withStock.postCashIn(t, "2025-04-10", "5000")
	noStock.postCashIn(t, "2025-04-10", "5000")

	// Each stock voucher's accounting legs are Dr Purchases / Cr creditor for the
	// line amount, so the twin reproduces them exactly with postPlain.
	type mv struct {
		date, qty, rate, amount string
		dir                     ledger.Direction
	}
	for _, m := range []mv{
		{"2025-05-05", "100", "12.5", "1250", ledger.Inward},
		{"2025-06-20", "40", "90", "3600", ledger.Outward},
		{"2025-07-11", "30", "17.25", "517.5", ledger.Inward},
	} {
		withStock.post(t, m.date, stockLine(item, withStock.main, uuid.Nil, m.dir, m.qty, m.rate, "0"))
		noStock.postPlain(t, m.date, m.amount)
	}

	nonZero := 0
	for _, monthEnd := range []string{"2025-04-30", "2025-05-31", "2025-06-30", "2025-07-31", "2025-08-31"} {
		bs := withStock.balanceSheet(t, monthEnd)
		twin := noStock.balanceSheet(t, monthEnd)

		if !bs.IsBalanced() {
			t.Errorf("as on %s the Balance Sheet with stock does NOT balance: assets %s against "+
				"liabilities %s, out by %s. The fold-in must add the SAME figure to the asset and to "+
				"net profit, at the SAME date, or the two sides part company",
				monthEnd, bs.Assets.Total.StringFixed(4), bs.Liabilities.Total.StringFixed(4),
				bs.Assets.Total.Sub(bs.Liabilities.Total).StringFixed(4))
		}
		if !twin.IsBalanced() {
			t.Errorf("as on %s the twin company with NO stock does not balance either; the fixture is broken",
				monthEnd)
		}

		want := withStock.summary(t, monthEnd).TotalValue
		if !want.IsZero() {
			nonZero++
		}
		if got := bs.Assets.Total.Sub(twin.Assets.Total); !got.Equal(want) {
			t.Errorf("as on %s the stock-carrying company's assets exceed its twin's by %s, but the "+
				"warehouse is worth %s. The two companies differ ONLY by the inventory lines, so the "+
				"difference IS the closing stock — a discrepancy of %s means the sheet foots on a "+
				"figure that is not the one the Stock Summary derives",
				monthEnd, got.StringFixed(4), want.StringFixed(4), got.Sub(want).StringFixed(4))
		}
		// And net profit must have moved by the same amount, in the same
		// direction, or the balance above was luck.
		if got := bs.NetProfit.Sub(twin.NetProfit); !got.Equal(want) {
			t.Errorf("as on %s net profit differs from the twin's by %s while the closing stock is %s: "+
				"the asset and the P&L term must move TOGETHER (that is the only thing IsBalanced can police)",
				monthEnd, got.StringFixed(4), want.StringFixed(4))
		}
	}
	if nonZero < 3 {
		t.Errorf("only %d of the five month-ends carried any stock at all; a fixture with an empty "+
			"warehouse cannot fail this test", nonZero)
	}
}

// --------------------------------------------------------------------------
// ABSENT, NOT ZERO
// --------------------------------------------------------------------------

// TestACompanyWithNoInventoryHasNoClosingStockNode is the compatibility contract
// for every tenant that does not keep stock, which is most of them.
//
// The distinction being asserted is ABSENT vs 0. A zero-amount "Closing Stock"
// row would change the SHAPE of the tree — and worse, it would resurrect the
// empty "Stock-in-Hand" group, which buildGroupNode has always dropped under
// Tally's "exclude zero" default. Both are checked, because the second is what
// proves the tree did not change rather than merely that one row did not appear.
func TestACompanyWithNoInventoryHasNoClosingStockNode(t *testing.T) {
	b := stockSetup(t)
	// A company that trades in services: real vouchers, no stock item anywhere.
	b.postCashIn(t, "2025-04-10", "5000")
	b.postPlain(t, "2025-05-02", "1200")

	for _, asOn := range []string{"2025-04-30", "2025-12-31", ""} {
		bs := b.balanceSheet(t, asOn)

		// (i) no Closing Stock row, at any depth.
		if amount, path, ok := findStatementNode(bs.Assets.Nodes, "Closing Stock"); ok {
			t.Errorf("as on %q a company with no stock items at all carries a Closing Stock node of %s "+
				"under %v. The distinction is ABSENT vs 0: a zero row still changes the tree every "+
				"existing report test compares against.%s",
				asOn, amount.StringFixed(4), path, describeTree(bs.Assets.Nodes))
		}
		// (ii) the empty Stock-in-Hand group is STILL dropped.
		if amount, _, ok := findStatementNode(bs.Assets.Nodes, "Stock-in-Hand"); ok {
			t.Errorf("as on %q the empty Stock-in-Hand group was resurrected (amount %s). It has no "+
				"ledgers and no stock, so buildGroupNode must drop it exactly as it did before closing "+
				"stock existed — its presence proves the SHAPE of the tree changed, not just a row.%s",
				asOn, amount.StringFixed(4), describeTree(bs.Assets.Nodes))
		}
		// (iv) and the sheet is unchanged: Cash 5000 is the only asset.
		if !bs.IsBalanced() {
			t.Errorf("as on %q the no-inventory Balance Sheet does not balance: %s vs %s",
				asOn, bs.Assets.Total.StringFixed(4), bs.Liabilities.Total.StringFixed(4))
		}
		if want := dec("5000"); !bs.Assets.Total.Equal(want) {
			t.Errorf("as on %q Assets.Total is %s, want %s — the only asset on these books is the "+
				"5000 of cash, so the fold-in must have added nothing at all",
				asOn, bs.Assets.Total.StringFixed(4), want.StringFixed(4))
		}
	}

	// (iii) the P&L carries neither Trading-account term.
	for _, w := range [][2]string{{"", ""}, {"2025-04-01", "2025-12-31"}, {"2025-05-01", "2025-05-31"}} {
		pl := b.profitLoss(t, w[0], w[1])
		for _, name := range []string{"Opening Stock", "Closing Stock"} {
			if amount, _, ok := findStatementNode(pl.Expenses.Nodes, name); ok {
				t.Errorf("p&l %q..%q carries a %q node of %s on the EXPENSES side for a company with no "+
					"inventory; ABSENT vs 0", w[0], w[1], name, amount.StringFixed(4))
			}
			if amount, _, ok := findStatementNode(pl.Income.Nodes, name); ok {
				t.Errorf("p&l %q..%q carries a %q node of %s on the INCOME side for a company with no "+
					"inventory; ABSENT vs 0", w[0], w[1], name, amount.StringFixed(4))
			}
		}
	}
}

// --------------------------------------------------------------------------
// BEFORE THE BOOKS BEGAN
// --------------------------------------------------------------------------

// TestBalanceSheetBeforeTheBooksBeganHasNoClosingStock is the inventory plane's
// copy of a bug the accounting plane already shipped once: a live verifier caught
// a Trial Balance dated 2020 showing 520000 of opening capital on books that
// begin in 2026.
//
// A stock_opening row has NO date column — it is stated AS AT
// company.books_beginning_from, exactly as a ledger's opening_balance is. A
// Balance Sheet dated before that must therefore show no stock at all. The
// fixture MUST carry opening rows or the test asserts nothing; the second half
// (the same node PRESENT on the beginning date) is what proves it does.
func TestBalanceSheetBeforeTheBooksBeganHasNoClosingStock(t *testing.T) {
	b := stockSetup(t) // books begin 2025-04-01
	item := b.newItem(t, "Nickel Bar")
	b.opening(t, item, b.main, uuid.Nil, "40", "10") // 400.0000 as at 2025-04-01
	b.post(t, "2025-05-01", stockLine(item, b.main, uuid.Nil, ledger.Inward, "10", "20", "0"))

	for _, before := range []string{"2020-01-01", "2025-03-31"} {
		bs := b.balanceSheet(t, before)
		if amount, _, ok := findStatementNode(bs.Assets.Nodes, "Closing Stock"); ok {
			t.Errorf("a Balance Sheet dated %s — before the books began on 2025-04-01 — carries %s of "+
				"closing stock. The opening position is stated AS AT the books' beginning, so nothing "+
				"can be on hand the day before, and this is the exact leak that put opening capital on "+
				"a Trial Balance dated 2020.%s",
				before, amount.StringFixed(4), describeTree(bs.Assets.Nodes))
		}
		if !bs.Assets.Total.IsZero() {
			t.Errorf("assets as on %s total %s, want 0 — nothing at all existed before the books began",
				before, bs.Assets.Total.StringFixed(4))
		}
		if !bs.IsBalanced() {
			t.Errorf("the Balance Sheet as on %s does not balance: %s vs %s",
				before, bs.Assets.Total.StringFixed(4), bs.Liabilities.Total.StringFixed(4))
		}
	}

	// Non-vacuity: ON the beginning date the opening stock IS the asset. Without
	// this, deleting the whole fold-in would pass the loop above.
	amount, _, ok := b.closingStockOn(t, "2025-04-01")
	if !ok {
		t.Fatalf("ON the books-beginning date the opening stock must appear as closing stock; it did not, "+
			"so the assertions above are vacuous.%s",
			describeTree(b.balanceSheet(t, "2025-04-01").Assets.Nodes))
	}
	eq(t, "closing stock ON the books-beginning date (the opening position, 40 @ 10)", amount, dec("400"))
}

// --------------------------------------------------------------------------
// THE P&L: the unbounded seam, additivity, and the bounds
// --------------------------------------------------------------------------

// TestOpeningStockOfAnUnboundedPeriodIsZeroNotTheWholeWarehouse pins the seam
// that is the DEFAULT HTTP PATH, not an edge case: reportDate returns "" for an
// absent query param, so `from == ""` is what an operator opening the P&L with
// no dates gets.
//
// On this seam "" means UNBOUNDED. If it were passed through to the position
// query it would mean "everything ever posted", and the opening-stock term would
// subtract the ENTIRE warehouse — understating gross profit by the whole of it,
// while the statement still footed perfectly.
func TestOpeningStockOfAnUnboundedPeriodIsZeroNotTheWholeWarehouse(t *testing.T) {
	b := stockSetup(t) // books begin 2025-04-01
	item := b.newItem(t, "Tin Coil")
	b.post(t, "2025-05-01", stockLine(item, b.main, uuid.Nil, ledger.Inward, "80", "12.5", "0"))
	b.post(t, "2025-06-10", stockLine(item, b.main, uuid.Nil, ledger.Outward, "20", "40", "0"))

	warehouse := b.summary(t, "").TotalValue
	if warehouse.IsZero() {
		t.Fatalf("the fixture holds no stock; this test cannot distinguish a zero opening term from a "+
			"whole-warehouse one%s", "")
	}

	unbounded := b.profitLoss(t, "", "")
	if amount, _, ok := findStatementNode(unbounded.Expenses.Nodes, "Opening Stock"); ok {
		t.Errorf("the unbounded P&L carries an Opening Stock node of %s. An empty `from` means UNBOUNDED, "+
			"and the day before unbounded is not a date: passing \"\" through to the position query would "+
			"read the position of EVERYTHING EVER POSTED (%s) and subtract the whole warehouse, "+
			"understating gross profit by exactly that — and the statement would still foot",
			amount.StringFixed(4), warehouse.StringFixed(4))
	}
	// The closing term must still be there, or the assertion above passes for the
	// trivial reason that no stock term exists at all.
	closing, _, ok := findStatementNode(unbounded.Income.Nodes, "Closing Stock")
	if !ok {
		t.Fatalf("the unbounded P&L carries no Closing Stock node either, so the Opening Stock assertion "+
			"above proves nothing")
	}
	eq(t, "the unbounded P&L's closing-stock credit against the Stock Summary", closing, warehouse)

	// The unbounded P&L and one bounded at the books' beginning describe the same
	// period, so they must produce the same result. If "" leaked through as
	// CS(""), these would differ by the whole warehouse.
	bounded := b.profitLoss(t, "2025-04-01", "")
	if !unbounded.NetProfit.Equal(bounded.NetProfit) {
		t.Errorf("the unbounded P&L nets %s while the same period bounded at the books' beginning nets %s, "+
			"a difference of %s against a warehouse worth %s",
			unbounded.NetProfit.StringFixed(4), bounded.NetProfit.StringFixed(4),
			unbounded.NetProfit.Sub(bounded.NetProfit).StringFixed(4), warehouse.StringFixed(4))
	}
}

// TestProfitAndLossWithStockIsAdditiveAcrossContiguousHalves pins the
// telescoping sum that is the reason the opening term is taken at d0-1 and not
// at d0.
//
//	S(d0,d1) + S(d1+1,d2) = (CS(d1) - CS(d0-1)) + (CS(d2) - CS(d1))
//	                      = CS(d2) - CS(d0-1) = S(d0,d2)
//
// CS(d1) appears once positively and once negatively and cancels. Take the
// opening at d0 instead and the two halves stop sharing a boundary value: they
// then differ by whatever moved ON d1+1, which is why this fixture puts a real
// movement on exactly that date. Without it the test could not fail.
//
// CONTIGUOUS ONLY. Two disjoint windows do NOT telescope, which is why the
// existing TestProfitAndLossIsAPeriodStatement (April + July against the whole)
// must never be extended with stock.
func TestProfitAndLossWithStockIsAdditiveAcrossContiguousHalves(t *testing.T) {
	b := stockSetup(t)
	item := b.newItem(t, "Zinc Ingot")

	const (
		d0     = "2025-05-01"
		d1     = "2025-06-30"
		d1next = "2025-07-01"
		d2     = "2025-08-31"
	)

	// Before d0, so the first half has a genuinely non-zero opening term.
	b.post(t, "2025-04-20", stockLine(item, b.main, uuid.Nil, ledger.Inward, "40", "7", "0"))
	// Inside the first half.
	b.post(t, "2025-05-10", stockLine(item, b.main, uuid.Nil, ledger.Inward, "100", "10", "0"))
	// EXACTLY on d1, the shared boundary.
	b.post(t, d1, stockLine(item, b.main, uuid.Nil, ledger.Outward, "20", "55", "0"))
	// EXACTLY on d1+1. THIS is what makes an off-by-one visible.
	b.post(t, d1next, stockLine(item, b.main, uuid.Nil, ledger.Inward, "60", "17.5", "0"))
	// Inside the second half.
	b.post(t, "2025-08-15", stockLine(item, b.main, uuid.Nil, ledger.Outward, "30", "60", "0"))

	// The boundary value must actually MOVE between d1 and d1+1, or an
	// implementation that took the opening at d0 would telescope just as well.
	atD1, atD1Next := b.summary(t, d1).TotalValue, b.summary(t, d1next).TotalValue
	if atD1.Equal(atD1Next) {
		t.Fatalf("the position is worth %s on both %s and %s, so nothing moved on the second half's "+
			"first day: this fixture CANNOT detect an opening term taken at d0 instead of d0-1. "+
			"The fixture is the finding.", atD1.StringFixed(4), d1, d1next)
	}

	net := func(from, to string) decimal.Decimal { return b.profitLoss(t, from, to).NetProfit }
	first, second, whole := net(d0, d1), net(d1next, d2), net(d0, d2)

	if !first.Add(second).Equal(whole) {
		t.Errorf("%s..%s nets %s and %s..%s nets %s, summing to %s, but the whole window %s..%s nets %s — "+
			"a difference of %s, against %s of position on the shared boundary. Two CONTIGUOUS periods "+
			"must telescope: the first half's closing stock is the second half's opening stock, and they "+
			"cancel only when the opening term is taken at d0-1",
			d0, d1, first.StringFixed(4), d1next, d2, second.StringFixed(4),
			first.Add(second).StringFixed(4), d0, d2, whole.StringFixed(4),
			first.Add(second).Sub(whole).StringFixed(4), atD1.StringFixed(4))
	}

	// And the shared boundary really is shared: the first half's closing credit
	// must be the second half's opening debit, to the paisa.
	firstClosing, _, ok := findStatementNode(b.profitLoss(t, d0, d1).Income.Nodes, "Closing Stock")
	if !ok {
		t.Fatalf("the first half carries no Closing Stock node; there is no boundary value to telescope")
	}
	secondOpening, _, ok := findStatementNode(b.profitLoss(t, d1next, d2).Expenses.Nodes, "Opening Stock")
	if !ok {
		t.Fatalf("the second half carries no Opening Stock node; the telescoping term is missing")
	}
	eq(t, "the first half's closing stock against the second half's opening stock", firstClosing, secondOpening)
	eq(t, "that shared boundary value against the position on "+d1, firstClosing, atD1)
}

// TestClosingStockPeriodBoundsAreInclusiveOnBothEnds places one movement EXACTLY
// on `from` and one EXACTLY on `to`.
//
// A bound flip once survived the ENTIRE reporting package because no fixture sat
// on a bound. The accounting side is pinned by TestPeriodBoundsAreInclusiveOnBothEnds;
// this pins the STOCK terms, which have their own arithmetic: the closing term is
// taken at `to` (inclusive) and the opening term at `from` MINUS ONE DAY.
//
// Every figure below is derived by hand:
//
//	2025-05-01  IN  100 @ 10       -> 100 units, 1000.0000, average 10.0000
//	2025-06-01  OUT  20 @ 300      -> cost 200.0000; CS(06-01) =  800.0000
//	2025-06-30  OUT  30 @ 250      -> cost 300.0000; CS(06-30) =  500.0000
//	CS(05-31) = 1000.0000, CS(06-29) = 800.0000
//
// The accounting legs of each voucher are Dr Purchases / Cr creditor for the line
// amount (6000.0000 and 7500.0000), which is what makes the net-profit figures
// below fall out.
func TestClosingStockPeriodBoundsAreInclusiveOnBothEnds(t *testing.T) {
	b := stockSetup(t)
	item := b.newItem(t, "Lead Sheet")

	const from, to = "2025-06-01", "2025-06-30"

	b.post(t, "2025-05-01", stockLine(item, b.main, uuid.Nil, ledger.Inward, "100", "10", "0"))
	b.post(t, from, stockLine(item, b.main, uuid.Nil, ledger.Outward, "20", "300", "0"))
	b.post(t, to, stockLine(item, b.main, uuid.Nil, ledger.Outward, "30", "250", "0"))
	b.post(t, "2025-07-10", stockLine(item, b.main, uuid.Nil, ledger.Inward, "10", "10", "0"))

	term := func(t *testing.T, from, to, side, name string) decimal.Decimal {
		t.Helper()
		pl := b.profitLoss(t, from, to)
		nodes := pl.Income.Nodes
		if side == "expense" {
			nodes = pl.Expenses.Nodes
		}
		amount, _, ok := findStatementNode(nodes, name)
		if !ok {
			t.Fatalf("p&l %q..%q carries no %q node on the %s side%s", from, to, name, side, describeTree(nodes))
		}
		return amount
	}

	// LOWER BOUND. The opening term is the position the day BEFORE `from`, so
	// moving `from` forward by one day past a movement must change it by exactly
	// that movement's COST.
	openAtFrom := term(t, from, to, "expense", "Opening Stock")
	openADayLater := term(t, "2025-06-02", to, "expense", "Opening Stock")
	eq(t, "the opening stock of a period starting "+from+" (the position on 2025-05-31)", openAtFrom, dec("1000"))
	eq(t, "the opening stock of a period starting 2025-06-02 (the position on "+from+")", openADayLater, dec("800"))
	if openAtFrom.Equal(openADayLater) {
		t.Errorf("the opening stock is %s whether the period starts %s or 2025-06-02. The outward dated "+
			"EXACTLY on %s must be INSIDE the first period and OUTSIDE the second, so the two openings "+
			"must differ by its cost of 200.0000 — equality means the opening term is being taken at d0 "+
			"instead of d0-1, which also destroys additivity",
			openAtFrom.StringFixed(4), from, from)
	}

	// UPPER BOUND. The closing term is the position AT `to`, inclusive.
	closeAtTo := term(t, from, to, "income", "Closing Stock")
	closeADayEarlier := term(t, from, "2025-06-29", "income", "Closing Stock")
	eq(t, "the closing stock of a period ending "+to, closeAtTo, dec("500"))
	eq(t, "the closing stock of a period ending 2025-06-29", closeADayEarlier, dec("800"))
	if closeAtTo.Equal(closeADayEarlier) {
		t.Errorf("the closing stock is %s whether the period ends %s or 2025-06-29: an outward dated "+
			"EXACTLY on the upper bound must be INCLUDED, so the two must differ by its cost of 300.0000",
			closeAtTo.StringFixed(4), to)
	}

	// And the net result, hand-derived, so a bound flip cannot hide inside a
	// term that happens to compensate:
	//   [06-01, 06-30]: income 500 - (purchases 13500 + opening 1000) = -14000
	//   [06-02, 06-30]: income 500 - (purchases  7500 + opening  800) =  -7800
	//   [06-01, 06-29]: income 800 - (purchases  6000 + opening 1000) =  -6200
	net := func(from, to string) decimal.Decimal { return b.profitLoss(t, from, to).NetProfit }
	eq(t, "net profit over "+from+".."+to, net(from, to), dec("-14000"))
	eq(t, "net profit when the lower bound moves one day past the voucher sitting on it",
		net("2025-06-02", to), dec("-7800"))
	eq(t, "net profit when the upper bound moves one day before the voucher sitting on it",
		net(from, "2025-06-29"), dec("-6200"))
}

// --------------------------------------------------------------------------
// ORDER DEPENDENCE, ALTERS AND DELETES
// --------------------------------------------------------------------------

// TestABackDatedInwardRestatesEveryLaterClosingStock is the test the whole
// no-stored-value architecture exists for.
//
// Valuation is ORDER-DEPENDENT: inserting a purchase BEFORE an issue re-prices
// that issue, so every closing figure after it changes. A materialised stock
// value would be wrong the instant this voucher was posted, and the Balance Sheet
// would carry an asset nobody could re-derive.
//
// Both figures are worked out by hand, and the rates are chosen so the average is
// NON-TERMINATING — 4340/220 = 19.7272... — because a float implementation, or one
// that rounded the running rate to 4 dp before multiplying it back out, diverges
// here and agrees on friendlier numbers.
//
//	before: 120 @ 25.75            -> 3090.0000 / 120
//	        OUT 50 at average 25.75 -> consumes 1287.5000, leaving 1802.5000
//	after:  120 @ 25.75 + 100 @ 12.50 -> 4340.0000 / 220, average 19.7272727272727273
//	        OUT 50 consumes round(986.363636363636365) = 986.3636
//	        leaving 4340.0000 - 986.3636 = 3353.6364
func TestABackDatedInwardRestatesEveryLaterClosingStock(t *testing.T) {
	b := stockSetup(t)
	item := b.newItem(t, "Alloy Plate")

	b.post(t, "2025-05-10", stockLine(item, b.main, uuid.Nil, ledger.Inward, "120", "25.75", "0"))
	b.post(t, "2025-06-10", stockLine(item, b.main, uuid.Nil, ledger.Outward, "50", "999", "0"))

	before, _, ok := b.closingStockOn(t, "2025-06-30")
	if !ok {
		t.Fatalf("the Balance Sheet carries no closing stock before the back-dated voucher; nothing to restate")
	}
	eq(t, "closing stock as on 2025-06-30 before the back-dated purchase (70 units of a 25.75 average)",
		before, dec("1802.5"))

	// The back-dated inward, BEFORE the issue and at a materially different rate.
	b.post(t, "2025-05-20", stockLine(item, b.main, uuid.Nil, ledger.Inward, "100", "12.5", "0"))

	after, _, ok := b.closingStockOn(t, "2025-06-30")
	if !ok {
		t.Fatalf("the Balance Sheet lost its closing stock after a back-dated PURCHASE was posted")
	}
	eq(t, "closing stock as on 2025-06-30 after a back-dated purchase re-priced the earlier issue",
		after, dec("3353.6364"))
	if after.Equal(before) {
		t.Errorf("the closing stock is %s both before and after a back-dated purchase at a different "+
			"rate. A back-dated inward RE-VALUES every later outward; a figure that did not move is a "+
			"stored value, or a replay that ignored the earlier date",
			after.StringFixed(4))
	}
	// And the second path agrees, as it must at every date.
	eq(t, "the restated Balance Sheet figure against the Stock Summary for the same date",
		after, b.summary(t, "2025-06-30").TotalValue)
}

// TestClosingStockFollowsAltersAndDeletes pins that the fold-in inherits the
// current-version join rather than re-implementing it.
//
// Movements are versioned exactly like postings: an ALTER re-states the lines
// under a NEW voucher_version and the superseded rows stay in stock_movement
// forever. A Balance Sheet that counted both versions would carry an asset the
// company never owned — and, because the same figure lands on both sides, it
// would still balance.
func TestClosingStockFollowsAltersAndDeletes(t *testing.T) {
	b := stockSetup(t)
	item := b.newItem(t, "Silver Wire")

	const asOn = "2025-12-31"
	id := b.post(t, "2025-05-10", stockLine(item, b.main, uuid.Nil, ledger.Inward, "100", "12.5", "0"))

	amount, _, ok := b.closingStockOn(t, asOn)
	if !ok {
		t.Fatalf("no closing stock after posting 100 @ 12.50%s", describeTree(b.balanceSheet(t, asOn).Assets.Nodes))
	}
	eq(t, "closing stock after posting 100 @ 12.50", amount, dec("1250"))
	plClosing, _, ok := findStatementNode(b.profitLoss(t, "2025-04-01", asOn).Income.Nodes, "Closing Stock")
	if !ok {
		t.Fatalf("the P&L carries no closing-stock credit for the same period")
	}
	eq(t, "the P&L's closing-stock credit against the Balance Sheet's asset", plClosing, amount)

	// ALTER 100 -> 40. An alter RESTATES; 140 would mean both versions counted.
	b.alter(t, id, "2025-05-10", stockLine(item, b.main, uuid.Nil, ledger.Inward, "40", "12.5", "0"))
	amount, _, ok = b.closingStockOn(t, asOn)
	if !ok {
		t.Fatalf("the closing stock vanished after an ALTER that only reduced the quantity")
	}
	eq(t, "closing stock after altering 100 to 40 (1750 would mean both versions counted)",
		amount, dec("500"))
	plClosing, _, ok = findStatementNode(b.profitLoss(t, "2025-04-01", asOn).Income.Nodes, "Closing Stock")
	if !ok {
		t.Fatalf("the P&L lost its closing-stock credit after the ALTER")
	}
	eq(t, "the P&L's restated closing-stock credit", plClosing, dec("500"))

	// DELETE: the tombstone version carries no movement rows, so the asset ceases
	// to exist — ABSENT, not a zero row.
	b.del(t, id)
	bs := b.balanceSheet(t, asOn)
	if amount, _, ok := findStatementNode(bs.Assets.Nodes, "Closing Stock"); ok {
		t.Errorf("after DELETING the only stock voucher the Balance Sheet still carries %s of closing "+
			"stock. A delete must remove the asset entirely — and the distinction is ABSENT vs 0, "+
			"because a zero row would resurrect the Stock-in-Hand group too.%s",
			amount.StringFixed(4), describeTree(bs.Assets.Nodes))
	}
	if _, _, ok := findStatementNode(bs.Assets.Nodes, "Stock-in-Hand"); ok {
		t.Errorf("the empty Stock-in-Hand group survived the delete%s", describeTree(bs.Assets.Nodes))
	}
	if _, _, ok := findStatementNode(b.profitLoss(t, "2025-04-01", asOn).Income.Nodes, "Closing Stock"); ok {
		t.Errorf("the P&L still credits closing stock after the only stock voucher was deleted")
	}
	if !bs.IsBalanced() {
		t.Errorf("the Balance Sheet does not balance after the delete: %s vs %s",
			bs.Assets.Total.StringFixed(4), bs.Liabilities.Total.StringFixed(4))
	}
}

// --------------------------------------------------------------------------
// EXPIRY
// --------------------------------------------------------------------------

// TestClosingStockCarriesExpiredBatchesAtCost forbids the one-line change that
// would make physical stock vanish from the Balance Sheet.
//
// An expired batch is STILL IN THE WAREHOUSE and still the company's property. It
// stays on the Balance Sheet at cost until somebody writes it off — and a
// write-off is a VOUCHER somebody signs, never a `WHERE expiry_date >= $1` in a
// report. stock_batch_summary.go's header anticipated this fold-in by name: "the
// day closing stock is folded into the Balance Sheet the same filter would
// quietly understate the assets of the business".
//
// So all three reports must state the same figure, and the batch report's
// ExpiredValue must be PART of the asset, never netted off it.
func TestClosingStockCarriesExpiredBatchesAtCost(t *testing.T) {
	b := stockSetup(t)
	item := b.newItem(t, "Vitamin Syrup")
	expired := b.newExpiringBatch(t, item, "EXP-01", "2025-05-31")
	fresh := b.newBatch(t, item, "OK-01")

	const asOn = "2025-08-31" // three months after EXP-01 expired

	b.post(t, "2025-05-01", stockLine(item, b.main, expired, ledger.Inward, "25", "40", "0"))
	b.post(t, "2025-05-02", stockLine(item, b.main, fresh, ledger.Inward, "10", "30", "0"))

	summary := b.summary(t, asOn)
	batches, err := b.rep.BatchStockSummary(b.ctx, b.company, asOn)
	if err != nil {
		t.Fatalf("batch stock summary as on %s: %v", asOn, err)
	}

	got, _, ok := b.closingStockOn(t, asOn)
	if !ok {
		t.Fatalf("the Balance Sheet carries no closing stock at all on %s, though the warehouse holds "+
			"%s of it%s", asOn, summary.TotalValue.StringFixed(4),
			describeTree(b.balanceSheet(t, asOn).Assets.Nodes))
	}
	eq(t, "the Balance Sheet's closing stock against the Stock Summary (expired batches included)",
		got, summary.TotalValue)
	eq(t, "the Balance Sheet's closing stock against the Batch Stock Summary",
		got, batches.TotalValue)
	// The hand-derived total: 25 @ 40 in the EXPIRED batch plus 10 @ 30 in the
	// fresh one. This is the assertion an expiry predicate anywhere on the
	// position path breaks, and it must say what that costs.
	if want := dec("1300"); !got.Equal(want) {
		t.Errorf("the Balance Sheet carries %s of closing stock as on %s, but the company physically "+
			"holds 25 units in the EXPIRED batch EXP-01 (at cost, 1000.0000) and 10 in the fresh one "+
			"(300.0000) — %s in all. THE BALANCE SHEET UNDERSTATES THE ASSETS OF THE BUSINESS by %s, "+
			"which is what an expiry predicate on the position path does: the goods are still in the "+
			"warehouse and still the company's property until a write-off VOUCHER somebody signs says "+
			"otherwise. Expired stock is FLAGGED, never excluded",
			got.StringFixed(4), asOn, want.StringFixed(4), want.Sub(got).StringFixed(4))
	}

	// The expired money must be INSIDE that figure, not beside it.
	if want := dec("1000"); !batches.ExpiredValue.Equal(want) {
		t.Errorf("the batch report classifies %s as expired, want %s. If it fell to zero, the expired "+
			"stock did not merely lose its flag — it left the reports entirely, and the Balance Sheet "+
			"above is understating the assets of the business by that much",
			batches.ExpiredValue.StringFixed(4), want.StringFixed(4))
	}
	if batches.ExpiredValue.IsZero() {
		t.Fatalf("no value was classified as expired, so this test cannot detect an expiry filter on " +
			"the position path — the fixture is the finding")
	}
	if got.Equal(summary.TotalValue.Sub(batches.ExpiredValue)) {
		t.Errorf("the Balance Sheet states %s, which is the warehouse (%s) LESS the expired batch (%s). "+
			"Expired stock is FLAGGED, never excluded: derecognition is a write-off voucher somebody "+
			"signs, not a WHERE clause, and netting it here understates the assets of the business",
			got.StringFixed(4), summary.TotalValue.StringFixed(4), batches.ExpiredValue.StringFixed(4))
	}
	if !b.balanceSheet(t, asOn).IsBalanced() {
		t.Errorf("the Balance Sheet carrying expired stock does not balance")
	}
}

// --------------------------------------------------------------------------
// THE CUSTOM-CHART FALLBACK
// --------------------------------------------------------------------------

// dropStockInHandGroup removes the seeded Stock-in-Hand group from THIS
// company's chart, so the fallback path has something to fall back from.
//
// It is safe only because the group is empty (the seed plants no ledgers under
// it) and because RLS scopes the delete to the throwaway company this test
// minted — no pre-existing company's chart is touched.
func (b *stockBooks) dropStockInHandGroup(t *testing.T) {
	t.Helper()
	if err := b.pool.RunInTx(b.ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM account_group WHERE name = 'Stock-in-Hand'`)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("deleted %d Stock-in-Hand groups, want exactly 1", tag.RowsAffected())
		}
		return nil
	}); err != nil {
		t.Fatalf("remove the seeded Stock-in-Hand group: %v", err)
	}
}

// TestAChartWithNoStockInHandGroupStillStatesTheAsset covers the FALLBACK.
//
// Nesting is preferred wherever the group exists, because Current Assets must
// subtotal the stock. But a tenant may run a custom chart of accounts with no
// Stock-in-Hand group at all, and dropping the asset on the floor because a
// group is missing would understate the Balance Sheet silently. The fallback
// states it as a top-level computed node on Assets instead.
//
// The invariant that matters is that BOTH paths add EXACTLY the same amount to
// Assets.Total — only the nesting differs — so this test asserts the figure and
// the top-level placement, and that the sheet still balances.
func TestAChartWithNoStockInHandGroupStillStatesTheAsset(t *testing.T) {
	b := stockSetup(t)
	b.dropStockInHandGroup(t)

	item := b.newItem(t, "Unfiled Widget")
	b.post(t, "2025-05-01", stockLine(item, b.main, uuid.Nil, ledger.Inward, "40", "12.5", "0"))
	b.post(t, "2025-06-01", stockLine(item, b.main, uuid.Nil, ledger.Outward, "10", "90", "0"))

	const asOn = "2025-12-31"
	want := b.summary(t, asOn).TotalValue
	if want.IsZero() {
		t.Fatalf("the fixture holds no stock; the fallback cannot be observed")
	}

	bs := b.balanceSheet(t, asOn)
	amount, path, ok := findStatementNode(bs.Assets.Nodes, "Closing Stock")
	if !ok {
		t.Fatalf("a chart of accounts with no Stock-in-Hand group lost its closing stock entirely: the "+
			"warehouse holds %s and the Balance Sheet states none of it. A missing GROUP must not be "+
			"able to delete an ASSET.%s", want.StringFixed(4), describeTree(bs.Assets.Nodes))
	}
	eq(t, "the fallback Closing Stock node against the Stock Summary", amount, want)
	if len(path) != 0 {
		t.Errorf("the fallback Closing Stock node is nested under %v; with no Stock-in-Hand group to "+
			"nest under it must sit at the top of Assets", path)
	}
	if !bs.IsBalanced() {
		t.Errorf("the fallback sheet does not balance: %s vs %s — the fallback must add the amount to "+
			"Assets.Total AND leave the net-profit term in place",
			bs.Assets.Total.StringFixed(4), bs.Liabilities.Total.StringFixed(4))
	}
	// Assets.Total must actually include it: appending the node without adding to
	// Total would leave a row the column does not sum, and the sheet would then
	// fail to balance — which is why the two assertions are kept side by side.
	if !bs.Assets.Total.Equal(want) {
		t.Errorf("Assets.Total is %s while the only asset on these books is %s of stock; the fallback "+
			"must add to Section.Total explicitly, because Total is never derived from Nodes",
			bs.Assets.Total.StringFixed(4), want.StringFixed(4))
	}
}

// --------------------------------------------------------------------------
// THE DOUBLE-BOOKING REFUSAL
// --------------------------------------------------------------------------

// TestStockBookedBothAsALedgerAndAsInventoryIsRefused pins the refusal, and the
// reason it has to BE a refusal.
//
// Some charts carry stock as an ordinary ledger under Stock-in-Hand
// (corpora/migration/01-opening-trading.xml plants exactly that, 60000 Dr). If
// such a company ALSO posts stock movements, the same goods are stated twice.
//
// AND THE SHEET STILL FOOTS. The fold-in adds the same figure to Assets and to
// net profit, so the double count cancels out of Assets - Liabilities:
// IsBalanced() is true, verify-demo's balance check is green, and the statement
// that gets filed overstates both the assets and the profit. There is no gate
// that can see it. That is why the report refuses to render rather than netting
// the two figures off each other (which would silently rewrite the accounting
// side, on no principled basis) or documenting it as a limitation (which would
// leave an invisible failure in a filed statement). It follows the precedent
// inventory.ValuerFor already set: a report that cannot render beats one that
// renders a wrong number.
func TestStockBookedBothAsALedgerAndAsInventoryIsRefused(t *testing.T) {
	b := stockSetup(t)
	b.stockLedgerUnder(t, "Opening Stock", "2025-04-01", "60000")

	// Before any stock movement exists, the books render normally: the refusal
	// must fire only when BOTH figures are non-zero.
	early := b.balanceSheet(t, "2025-04-15")
	if amount, _, ok := findStatementNode(early.Assets.Nodes, "Stock-in-Hand"); !ok {
		t.Fatalf("the manual stock ledger is missing from the sheet, so the refusal below would be "+
			"testing nothing%s", describeTree(early.Assets.Nodes))
	} else {
		eq(t, "the manual Stock-in-Hand ledger balance before any movement exists", amount, dec("60000"))
	}

	item := b.newItem(t, "Double Booked Widget")
	b.post(t, "2025-05-01", stockLine(item, b.main, uuid.Nil, ledger.Inward, "10", "100", "0"))

	_, err := b.rep.BalanceSheet(b.ctx, b.company, "2025-12-31")
	if err == nil {
		bs := b.balanceSheet(t, "2025-12-31")
		t.Fatalf("a company that books stock BOTH as a 60000 ledger under Stock-in-Hand AND as 1000 of "+
			"inventory movement rendered a Balance Sheet with no complaint (assets %s, balanced=%v). "+
			"The same goods are counted twice, and because the figure is added to assets AND to net "+
			"profit the sheet still FOOTS — no balance check can ever catch this, which is why the "+
			"report must refuse.%s",
			bs.Assets.Total.StringFixed(4), bs.IsBalanced(), describeTree(bs.Assets.Nodes))
	}
	msg := err.Error()
	for _, want := range []string{"Stock-in-Hand", "60000.0000", "1000.0000"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal message does not name %q, so the operator cannot tell which two "+
				"figures collide or which one to remove. Message: %s", want, msg)
		}
	}
	// The P&L is deliberately NOT refused: Stock-in-Hand is a Balance-Sheet group
	// and never appears there, so there is nothing to double count.
	if _, err := b.rep.ProfitAndLoss(b.ctx, b.company, "2025-04-01", "2025-12-31"); err != nil {
		t.Errorf("the P&L was refused too (%v); the double-booking check belongs on the Balance Sheet "+
			"only, because Stock-in-Hand is a Balance-Sheet group", err)
	}
}

// TestAStockLedgerWithNoInventoryIsLeftAlone is the other half of the refusal:
// it must fire ONLY when both figures are non-zero.
//
// corpora/migration/01-opening-trading.xml and
// internal/query/widen_execute_test.go both carry a manual "Opening Stock"
// ledger with ZERO stock_movement rows, and internal/migration's five corpora
// oracles run over exactly that shape. A refusal that fired on the ledger alone
// would break every one of them, and would break every real tenant that has not
// adopted inventory.
func TestAStockLedgerWithNoInventoryIsLeftAlone(t *testing.T) {
	b := stockSetup(t)
	b.stockLedgerUnder(t, "Opening Stock", "2025-04-01", "60000")

	for _, asOn := range []string{"2025-04-30", "2025-12-31", ""} {
		bs, err := b.rep.BalanceSheet(b.ctx, b.company, asOn)
		if err != nil {
			t.Fatalf("as on %q a company with a manual stock ledger and NO inventory was refused: %v. "+
				"The refusal must fire only when BOTH the ledger balance and the derived closing stock "+
				"are non-zero, or every pre-inventory tenant and all five migration corpora break",
				asOn, err)
		}
		if amount, _, ok := findStatementNode(bs.Assets.Nodes, "Closing Stock"); ok {
			t.Errorf("as on %q a company with no stock movements carries a derived Closing Stock node "+
				"of %s beside its manual ledger", asOn, amount.StringFixed(4))
		}
		amount, path, ok := findStatementNode(bs.Assets.Nodes, "Stock-in-Hand")
		if !ok {
			t.Fatalf("as on %q the manual Stock-in-Hand group vanished%s", asOn, describeTree(bs.Assets.Nodes))
		}
		eq(t, fmt.Sprintf("the manual stock ledger balance as on %q", asOn), amount, dec("60000"))
		if len(path) != 1 || path[0] != "Current Assets" {
			t.Errorf("as on %q Stock-in-Hand sits under %v, want [Current Assets]", asOn, path)
		}
		if !bs.IsBalanced() {
			t.Errorf("as on %q the sheet does not balance: %s vs %s",
				asOn, bs.Assets.Total.StringFixed(4), bs.Liabilities.Total.StringFixed(4))
		}
	}
}

// postNote writes a RECEIPT NOTE — a non-accounting stock voucher. It moves
// goods and posts ZERO ledger lines, which is what makes pre-books stock
// visible to the Balance Sheet's cumulative profit with no offsetting expense.
// b.post cannot be used for that: it carries Dr Purchases / Cr creditor legs, so
// a pre-books voucher lands in the accounting plane too and the divergence this
// test is about never appears.
func (b *stockBooks) postNote(t *testing.T, date string, lines ...ledger.InventoryLine) uuid.UUID {
	t.Helper()
	res, err := b.led.PostVoucher(b.ctx, ledger.VoucherInput{
		Class:       ledger.ReceiptNote,
		VoucherDate: date,
		Party:       "Stock Supplies",
		CreatedBy:   "closing-stock-test",
		Inventory:   lines,
	})
	if err != nil {
		t.Fatalf("post receipt note on %s: %v", date, err)
	}
	return res.VoucherID
}

// TestBackDatedStockKeepsTheSheetBalanced pins the behaviour of stock received
// BEFORE the books began, and exists mostly to stop a plausible "fix" that
// breaks the Balance Sheet.
//
// A NON-ACCOUNTING stock voucher (receipt note) dated before
// books_beginning_from puts goods on hand with no counter-entry in any ledger.
// The Balance Sheet is a POSITION AS AT a date whose period runs from inception,
// so those goods are inside its period; a P&L for the first financial year
// starts later, so they are outside its period and appear as its OPENING stock
// instead. The two statements therefore report DIFFERENT net profits, and both
// are correct — they cover different spans.
//
// An adversarial review measured exactly this (7500 against 500, 7000 apart) and
// read it as a defect, recommending the sheet derive its opening-stock term from
// books_beginning_from. THAT CHANGE WAS TRIED AND IT BREAKS THE SHEET: the
// profit side loses the pre-books stock while the ASSET side still carries the
// full closing figure, so the two stop moving together and the identity fails —
// measured at assets 7500 against liabilities 500. The stock plane is symmetric
// here with the accounting plane, where loadClosingBalances has no lower bound
// either and a pre-books journal splits the two net profits the same way.
//
// So this test asserts what must hold: the sheet BALANCES, its closing-stock
// node still equals the Stock Summary for the same date (the load-bearing
// identity — see the file header on why balance alone proves nothing), and the
// P&L's opening-stock term picks up exactly the pre-books goods.
func TestBackDatedStockKeepsTheSheetBalanced(t *testing.T) {
	b := stockSetup(t) // the company's books begin 2025-04-01
	item := b.newItem(t, "Back-dated Bar")

	// Received BEFORE the books begin. Accepted at this layer — no back-dated
	// cutoff refuses it here — which is why the reports must cope with it rather
	// than assume it away.
	b.postNote(t, "2025-03-15", stockLine(item, b.main, uuid.Nil, ledger.Inward, "10", "700", "0"))
	b.postNote(t, "2025-05-10", stockLine(item, b.main, uuid.Nil, ledger.Inward, "5", "100", "0"))

	const from, to = "2025-04-01", "2026-03-31"

	bs, err := b.rep.BalanceSheet(b.ctx, b.company, to)
	if err != nil {
		t.Fatalf("balance sheet: %v", err)
	}
	if !bs.IsBalanced() {
		t.Fatalf("stock received before the books began unbalanced the sheet: assets %s vs liabilities %s — "+
			"the asset and the profit term must move together; deriving the sheet's opening-stock term from "+
			"books_beginning_from is what breaks this",
			bs.Assets.Total.StringFixed(4), bs.Liabilities.Total.StringFixed(4))
	}

	// The number itself, pinned against the independent path.
	sum := b.summary(t, to)
	got, _, ok := findStatementNode(bs.Assets.Nodes, "Closing Stock")
	if !ok {
		t.Fatal("the sheet carries no Closing Stock node at all, though the warehouse is not empty")
	}
	if !got.Equal(sum.TotalValue) {
		t.Errorf("the sheet's Closing Stock node is %s but the Stock Summary for the same date totals %s; "+
			"balance cannot catch this because the term cancels on both sides",
			got.StringFixed(4), sum.TotalValue.StringFixed(4))
	}

	// The P&L must see the pre-books goods as OPENING stock, not as nothing.
	pl, err := b.rep.ProfitAndLoss(b.ctx, b.company, from, to)
	if err != nil {
		t.Fatalf("profit and loss: %v", err)
	}
	opening := b.summary(t, "2025-03-31").TotalValue
	if opening.IsZero() {
		t.Fatal("the position the day before the books began is zero, so this test is no longer exercising " +
			"back-dated stock at all — it would pass for the wrong reason")
	}
	if !pl.NetProfit.Equal(bs.NetProfit.Sub(opening)) {
		t.Errorf("P&L net profit %s does not differ from the sheet's %s by exactly the pre-books stock %s; "+
			"the two statements cover different periods and that difference is the whole of it",
			pl.NetProfit.StringFixed(4), bs.NetProfit.StringFixed(4), opening.StringFixed(4))
	}
}
