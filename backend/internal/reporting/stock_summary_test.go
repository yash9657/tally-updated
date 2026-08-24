package reporting_test

// stock_summary_test.go — the Stock Summary against the LIVE database.
//
// These tests exist to pin the four things a stock report gets wrong quietly:
//
//  1. VALUE. Every figure here is checked against arithmetic worked out by hand
//     in the test, and the closing value of one scenario is checked against the
//     PURE corpus fixture that states the same scenario with no database in it
//     (TestClosingValueEqualsTheCorpusOracle). A stock report that valued an
//     issue at the price it sold for instead of what it cost would still foot,
//     still look sensible, and silently report zero gross profit.
//  2. VERSIONS. Movements are versioned like postings: an ALTER re-states the
//     lines under a new voucher_version and leaves the old rows in the table
//     forever. Anything that forgets the current-version join counts an altered
//     invoice twice.
//  3. BOUNDS. The accounting reports carried a live off-by-one on both period
//     bounds for a whole phase because no fixture ever placed a voucher exactly
//     ON a bound. TestStockPeriodBoundsAreInclusiveOnBothEnds places one on each.
//  4. ZEROS. An item bought and entirely sold is the most interesting row on a
//     stock report, not a row to hide.

import (
	"context"
	"encoding/json"
	"os"
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

// --------------------------------------------------------------------------
// Fixture harness
// --------------------------------------------------------------------------

// stockBooks is one isolated company with everything an inventory voucher
// needs, plus the two report services under test.
type stockBooks struct {
	pool    *db.Pool
	ctx     context.Context
	company uuid.UUID
	rep     *reporting.Service
	led     *ledger.Service

	// The accounting legs every voucher needs to be a legal double entry. The
	// inventory lines are the subject of these tests; these two exist only so
	// the voucher can be posted at all.
	creditor uuid.UUID
	purchase uuid.UUID

	// main is the system 'Main Location' the company seed plants.
	main uuid.UUID
}

func stockSetup(t *testing.T) *stockBooks {
	t.Helper()
	pool, ctx, companyID, ids := setup(t)
	b := &stockBooks{
		pool:    pool,
		ctx:     ctx,
		company: companyID,
		rep:     reporting.NewService(pool),
		led:     ledger.NewService(pool),
	}
	var err error
	if b.creditor, err = ids.CreateLedger(ctx, "Stock Supplies", "Sundry Creditors"); err != nil {
		t.Fatalf("create creditor: %v", err)
	}
	if b.purchase, err = ids.CreateLedger(ctx, "Stock Purchases", "Purchase Accounts"); err != nil {
		t.Fatalf("create purchase ledger: %v", err)
	}
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT id FROM godown WHERE company_id = $1 AND name = 'Main Location'`,
			companyID).Scan(&b.main)
	}); err != nil {
		t.Fatalf("the company seed must plant a 'Main Location' for stock to sit in: %v", err)
	}
	return b
}

// newItem plants a stock item directly rather than through masters, so these
// tests describe the report and nothing else: a change to the master service's
// opening handling must not be able to move a number in here without the
// fixture saying so.
func (b *stockBooks) newItem(t *testing.T, name string) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new stock item id: %v", err)
	}
	if err := b.pool.RunInTx(b.ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO stock_item (id, company_id, name) VALUES ($1, $2, $3)`,
			id, b.company, name)
		return err
	}); err != nil {
		t.Fatalf("create stock item %q: %v", name, err)
	}
	return id
}

func (b *stockBooks) newGodown(t *testing.T, name string) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new godown id: %v", err)
	}
	if err := b.pool.RunInTx(b.ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO godown (id, company_id, name) VALUES ($1, $2, $3)`,
			id, b.company, name)
		return err
	}); err != nil {
		t.Fatalf("create godown %q: %v", name, err)
	}
	return id
}

func (b *stockBooks) newBatch(t *testing.T, item uuid.UUID, name string) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new batch id: %v", err)
	}
	if err := b.pool.RunInTx(b.ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO stock_batch (id, company_id, stock_item_id, name)
			 VALUES ($1, $2, $3, $4)`,
			id, b.company, item, name)
		return err
	}); err != nil {
		t.Fatalf("create batch %q: %v", name, err)
	}
	return id
}

// opening plants a stock_opening row: the position the books began with. It has
// no date column of its own — it is stated as at company.books_beginning_from,
// which is exactly what TestPositionBeforeTheBooksBeganHasNoOpeningStock is
// about.
func (b *stockBooks) opening(t *testing.T, item, godown, batch uuid.UUID, qty, rate string) {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new stock opening id: %v", err)
	}
	if err := b.pool.RunInTx(b.ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO stock_opening
			   (id, company_id, stock_item_id, godown_id, batch_id, qty, rate)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			id, b.company, item, nilAsNull(godown), nilAsNull(batch),
			dec(qty), dec(rate))
		return err
	}); err != nil {
		t.Fatalf("create stock opening: %v", err)
	}
}

// nilAsNull mirrors the post path: uuid.Nil is an UNTRACKED dimension and must
// be written as SQL NULL, never as the all-zeroes uuid.
func nilAsNull(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

// stockLine builds one inventory line. Amount is the line as entered, and here
// it is deliberately kept equal to qty * rate net of the discount, because that
// is what stock_movement.amount must hold — the valuer never reads it, but a
// fixture that disagreed with it would be describing an impossible voucher.
func stockLine(item, godown, batch uuid.UUID, dir ledger.Direction, qty, rate, discountPct string) ledger.InventoryLine {
	q, r, d := dec(qty), dec(rate), dec(discountPct)
	amount := q.Mul(r).Mul(decimal.NewFromInt(100).Sub(d)).Div(decimal.NewFromInt(100)).Round(4)
	return ledger.InventoryLine{
		StockItemID: item,
		GodownID:    godown,
		BatchID:     batch,
		Direction:   dir,
		Qty:         q,
		Rate:        r,
		DiscountPct: d,
		Amount:      amount,
	}
}

// post writes one voucher carrying the given inventory lines. The accounting
// legs are Dr Purchases / Cr the creditor for the sum of the line amounts, so
// the voucher is a legal double entry; the inventory lines are the subject.
func (b *stockBooks) post(t *testing.T, date string, lines ...ledger.InventoryLine) uuid.UUID {
	t.Helper()
	res, err := b.led.PostVoucher(b.ctx, b.input(date, lines...))
	if err != nil {
		t.Fatalf("post inventory voucher on %s: %v", date, err)
	}
	return res.VoucherID
}

func (b *stockBooks) alter(t *testing.T, id uuid.UUID, date string, lines ...ledger.InventoryLine) {
	t.Helper()
	if _, err := b.led.AlterVoucher(b.ctx, id, b.input(date, lines...)); err != nil {
		t.Fatalf("alter inventory voucher: %v", err)
	}
}

func (b *stockBooks) del(t *testing.T, id uuid.UUID) {
	t.Helper()
	if _, err := b.led.DeleteVoucher(b.ctx, id, "stock-report-test"); err != nil {
		t.Fatalf("delete inventory voucher: %v", err)
	}
}

func (b *stockBooks) input(date string, lines ...ledger.InventoryLine) ledger.VoucherInput {
	total := decimal.Zero
	for _, l := range lines {
		total = total.Add(l.Amount)
	}
	if total.IsZero() {
		total = dec("1")
	}
	return ledger.VoucherInput{
		Class:       ledger.Purchase,
		VoucherDate: date,
		Party:       "Stock Supplies",
		CreatedBy:   "stock-report-test",
		Postings: []ledger.PostingInput{
			{LedgerID: b.purchase, DrCr: ledger.Debit, Amount: total},
			{LedgerID: b.creditor, DrCr: ledger.Credit, Amount: total},
		},
		Inventory: lines,
	}
}

func (b *stockBooks) summary(t *testing.T, asOn string) reporting.StockSummary {
	t.Helper()
	s, err := b.rep.StockSummary(b.ctx, b.company, asOn)
	if err != nil {
		t.Fatalf("stock summary as on %q: %v", asOn, err)
	}
	return s
}

func (b *stockBooks) movement(t *testing.T, from, to string) reporting.StockMovementReport {
	t.Helper()
	r, err := b.rep.StockMovementReport(b.ctx, b.company, from, to)
	if err != nil {
		t.Fatalf("stock movement report %q..%q: %v", from, to, err)
	}
	return r
}

// positionRow finds one (item, godown, batch) row, failing with the positions
// that DO exist — a missing row is the interesting failure and "not found" on
// its own says nothing about why.
func positionRow(t *testing.T, s reporting.StockSummary, item, godown, batch uuid.UUID) reporting.StockPositionRow {
	t.Helper()
	for _, r := range s.Rows {
		if r.StockItemID == item && r.GodownID == godown && r.BatchID == batch {
			return r
		}
	}
	t.Fatalf("no stock position for item %s / godown %s / batch %s; the report holds %s",
		item, godown, batch, describePositions(s))
	return reporting.StockPositionRow{}
}

func describePositions(s reporting.StockSummary) string {
	if len(s.Rows) == 0 {
		return "no positions at all"
	}
	out := ""
	for _, r := range s.Rows {
		out += "\n    " + r.ItemName + " @ " + r.GodownName + " / " + r.BatchName +
			" closing " + r.ClosingQty.StringFixed(4) + " valued " + r.ClosingValue.StringFixed(4)
	}
	return out
}

func rowsForItem(s reporting.StockSummary, item uuid.UUID) []reporting.StockPositionRow {
	var out []reporting.StockPositionRow
	for _, r := range s.Rows {
		if r.StockItemID == item {
			out = append(out, r)
		}
	}
	return out
}

func itemRollup(t *testing.T, s reporting.StockSummary, item uuid.UUID) reporting.StockItemRollup {
	t.Helper()
	for _, g := range s.Items {
		if g.StockItemID == item {
			return g
		}
	}
	t.Fatalf("no item rollup for %s; the report holds %s", item, describePositions(s))
	return reporting.StockItemRollup{}
}

func movementRow(t *testing.T, r reporting.StockMovementReport, item, godown, batch uuid.UUID) reporting.StockMovementRow {
	t.Helper()
	for _, row := range r.Rows {
		if row.StockItemID == item && row.GodownID == godown && row.BatchID == batch {
			return row
		}
	}
	t.Fatalf("no movement row for item %s / godown %s / batch %s in %s..%s (%d rows)",
		item, godown, batch, r.From, r.To, len(r.Rows))
	return reporting.StockMovementRow{}
}

// eq asserts one decimal figure, naming what it MEANS rather than "got x want y".
func eq(t *testing.T, what string, got, want decimal.Decimal) {
	t.Helper()
	if !got.Equal(want) {
		t.Errorf("%s is %s, want %s", what, got.StringFixed(4), want.StringFixed(4))
	}
}

// --------------------------------------------------------------------------

// TestStockSummaryPositionVsPeriodAreDifferentFunctions pins the split that
// reporting.go makes for the accounts and this file makes for stock.
//
// A POSITION as at a date needs the opening stock plus EVERY movement up to
// that date — dropping the earlier part of an item's history would still
// produce a tidy table and would be wrong. A PERIOD reports only what moved
// inside the window and excludes the opening entirely. The two answer different
// questions about the same books, and their arities differ so neither can be
// called with the other's meaning by accident.
func TestStockSummaryPositionVsPeriodAreDifferentFunctions(t *testing.T) {
	b := stockSetup(t)
	item := b.newItem(t, "Copper Rod")

	// The books opened owning 40 units at 10.0000 = 400.0000.
	b.opening(t, item, b.main, uuid.Nil, "40", "10")
	// A purchase well before the window: 10 @ 20.0000 = 200.0000.
	b.post(t, "2025-05-01", stockLine(item, b.main, uuid.Nil, ledger.Inward, "10", "20", "0"))
	// An issue inside the window. State 50 units / 600.0000 -> average 12.0000,
	// so 5 units cost 60.0000 — NOT 5 * 20 (the last purchase price) and not the
	// price they left at.
	b.post(t, "2025-08-01", stockLine(item, b.main, uuid.Nil, ledger.Outward, "5", "99", "0"))

	pos := positionRow(t, b.summary(t, "2025-12-31"), item, b.main, uuid.Nil)
	eq(t, "the position's opening quantity", pos.OpeningQty, dec("40"))
	eq(t, "the position's opening value", pos.OpeningValue, dec("400"))
	eq(t, "the position's inward quantity (a position has no lower bound)", pos.InwardQty, dec("10"))
	eq(t, "the position's inward value", pos.InwardValue, dec("200"))
	eq(t, "the position's outward quantity", pos.OutwardQty, dec("5"))
	eq(t, "the cost consumed by the issue (5 units of a 12.0000 average)", pos.OutwardValue, dec("60"))
	eq(t, "the closing quantity", pos.ClosingQty, dec("45"))
	eq(t, "the closing value", pos.ClosingValue, dec("540"))

	// The same books, asked the PERIOD question over July-August. The May
	// purchase is outside the window and the opening is not a period's business
	// at all — but the issue is still costed at what the goods cost, which is
	// the whole reason the pre-window history is replayed and not reported.
	win := movementRow(t, b.movement(t, "2025-07-01", "2025-08-31"), item, b.main, uuid.Nil)
	eq(t, "the window's inward quantity (the May purchase is outside it)", win.InwardQty, decimal.Zero)
	eq(t, "the window's inward value", win.InwardValue, decimal.Zero)
	eq(t, "the window's outward quantity", win.OutwardQty, dec("5"))
	eq(t, "the window's cost of goods sold", win.OutwardValue, dec("60"))
	eq(t, "the window's net quantity movement", win.NetQty, dec("-5"))
	eq(t, "the window's net value movement", win.NetValue, dec("-60"))

	// The two must actually DISAGREE. If a `from` ever leaked into the position
	// it would start answering the period's question and this is what would
	// stop being true.
	if pos.InwardQty.Equal(win.InwardQty) {
		t.Errorf("the position and the period report the same inward quantity (%s): "+
			"a position has NO lower bound and must see the May purchase the July-August window cannot",
			pos.InwardQty.StringFixed(4))
	}
	if pos.OpeningQty.IsZero() {
		t.Errorf("the position lost its opening stock; only a PERIOD excludes the opening")
	}
}

// TestStockPeriodBoundsAreInclusiveOnBothEnds places one movement EXACTLY on
// `from` and one EXACTLY on `to`.
//
// It exists because the accounting reports carried a live off-by-one on both
// bounds for an entire phase: `voucher_date >= $1` mutated to `> $1` and
// `<= $2` to `< $2` both left the package green, because every fixture date sat
// strictly inside or strictly outside a bound. "1 June to 30 June" includes
// everything dated 1 June and everything dated 30 June, and nothing else.
func TestStockPeriodBoundsAreInclusiveOnBothEnds(t *testing.T) {
	b := stockSetup(t)
	item := b.newItem(t, "Brass Sheet")

	const from, to = "2025-06-01", "2025-06-30"

	// Before the window: 10 @ 100.0000. It prices the issue but is not reported.
	b.post(t, "2025-05-01", stockLine(item, b.main, uuid.Nil, ledger.Inward, "10", "100", "0"))
	// EXACTLY on the lower bound.
	b.post(t, from, stockLine(item, b.main, uuid.Nil, ledger.Inward, "5", "100", "0"))
	// EXACTLY on the upper bound. State is 15 / 1500.0000 -> average 100.0000.
	b.post(t, to, stockLine(item, b.main, uuid.Nil, ledger.Outward, "3", "250", "0"))
	// After the window.
	b.post(t, "2025-07-15", stockLine(item, b.main, uuid.Nil, ledger.Inward, "7", "100", "0"))

	win := movementRow(t, b.movement(t, from, to), item, b.main, uuid.Nil)
	eq(t, "the inward dated exactly on `from` (it MUST be inside the window)", win.InwardQty, dec("5"))
	eq(t, "the value of the inward dated exactly on `from`", win.InwardValue, dec("500"))
	eq(t, "the outward dated exactly on `to` (it MUST be inside the window)", win.OutwardQty, dec("3"))
	eq(t, "the cost consumed by the outward dated exactly on `to`", win.OutwardValue, dec("300"))
	eq(t, "the window's net quantity", win.NetQty, dec("2"))
	eq(t, "the window's net value", win.NetValue, dec("200"))

	// A window one day narrower on each side must lose exactly those two events,
	// which is the other half of the same statement.
	narrow := b.movement(t, "2025-06-02", "2025-06-29")
	if len(narrow.Rows) != 0 {
		t.Errorf("the window 2025-06-02..2025-06-29 holds %d rows; the only two movements in June "+
			"sit ON the bounds of the wider window, so a narrower one must be empty", len(narrow.Rows))
	}

	// And the periods must be ADDITIVE: May..June plus July must equal May..July,
	// which is what makes it a period statement rather than a position.
	first := movementRow(t, b.movement(t, "2025-05-01", "2025-06-30"), item, b.main, uuid.Nil)
	second := movementRow(t, b.movement(t, "2025-07-01", "2025-07-31"), item, b.main, uuid.Nil)
	whole := movementRow(t, b.movement(t, "2025-05-01", "2025-07-31"), item, b.main, uuid.Nil)
	eq(t, "May-June inwards plus July inwards, against May-July inwards",
		first.InwardQty.Add(second.InwardQty), whole.InwardQty)
	eq(t, "May-June cost of sales plus July's, against May-July's",
		first.OutwardValue.Add(second.OutwardValue), whole.OutwardValue)
}

// TestStockFollowsAltersAndDeletes pins what the current-version join is FOR.
//
// Movements are versioned exactly like postings: an ALTER re-states the lines
// under a NEW voucher_version and the superseded rows stay in stock_movement
// forever. A report that joins voucher_head on current_version_id needs no
// reversal code at all — and a report that forgets it counts the item twice and
// invents stock the company never owned.
func TestStockFollowsAltersAndDeletes(t *testing.T) {
	b := stockSetup(t)
	item := b.newItem(t, "Zinc Ingot")

	id := b.post(t, "2025-05-01", stockLine(item, b.main, uuid.Nil, ledger.Inward, "10", "100", "0"))
	pos := positionRow(t, b.summary(t, ""), item, b.main, uuid.Nil)
	eq(t, "the closing quantity after posting 10", pos.ClosingQty, dec("10"))
	eq(t, "the closing value after posting 10 @ 100", pos.ClosingValue, dec("1000"))

	// ALTER 10 -> 4. An alter RESTATES; 14 would mean both versions counted.
	b.alter(t, id, "2025-05-01", stockLine(item, b.main, uuid.Nil, ledger.Inward, "4", "100", "0"))
	pos = positionRow(t, b.summary(t, ""), item, b.main, uuid.Nil)
	eq(t, "the closing quantity after altering 10 to 4 (14 would mean both versions counted)",
		pos.ClosingQty, dec("4"))
	eq(t, "the closing value after altering 10 to 4", pos.ClosingValue, dec("400"))
	eq(t, "the inward quantity after the alter", pos.InwardQty, dec("4"))

	// ...at a LATER as-at date too: reading historic versions would resurrect it.
	later := positionRow(t, b.summary(t, "2025-12-31"), item, b.main, uuid.Nil)
	eq(t, "the closing quantity as at a later date after the alter", later.ClosingQty, dec("4"))

	// DELETE: the tombstone version carries no movement rows at all, so the
	// position ceases to exist rather than going to zero.
	b.del(t, id)
	for _, asOn := range []string{"", "2025-05-31", "2025-12-31"} {
		s := b.summary(t, asOn)
		if rows := rowsForItem(s, item); len(rows) != 0 {
			t.Errorf("as on %q the DELETED voucher still leaves %d stock position(s) for the item "+
				"(closing %s) — a delete must remove the stock entirely",
				asOn, len(rows), rows[0].ClosingQty.StringFixed(4))
		}
		if !s.TotalValue.IsZero() {
			t.Errorf("as on %q the stock is valued at %s after the only voucher was deleted, want 0",
				asOn, s.TotalValue.StringFixed(4))
		}
	}
}

// TestPositionBeforeTheBooksBeganHasNoOpeningStock pins the rule a stock_opening
// row obeys. It has no date column: it is stated as at
// company.books_beginning_from, exactly as a ledger's opening_balance is. A
// position query that folded it in unconditionally would report stock on hand
// for a date before the entity's books existed — the live verifier caught that
// exact bug on the accounting side (a Trial Balance dated 2020 showing 520000 of
// opening capital on books beginning in 2026), and 40 units of stock reported as
// at 2020 is the same lie in a different unit.
func TestPositionBeforeTheBooksBeganHasNoOpeningStock(t *testing.T) {
	b := stockSetup(t) // the company's books begin 2025-04-01
	item := b.newItem(t, "Nickel Bar")
	b.opening(t, item, b.main, uuid.Nil, "40", "10")

	before := b.summary(t, "2020-01-01")
	if rows := rowsForItem(before, item); len(rows) != 0 {
		t.Errorf("a Stock Summary dated before books_beginning_from shows %s units valued %s, want nothing — "+
			"the opening position is stated AS AT the books beginning date, not before it",
			rows[0].ClosingQty.StringFixed(4), rows[0].ClosingValue.StringFixed(4))
	}
	if !before.TotalValue.IsZero() {
		t.Errorf("stock before the books began is valued at %s, want 0", before.TotalValue.StringFixed(4))
	}

	// ON the beginning date the opening IS included.
	on := positionRow(t, b.summary(t, "2025-04-01"), item, b.main, uuid.Nil)
	eq(t, "the opening quantity ON the books beginning date", on.OpeningQty, dec("40"))
	eq(t, "the closing value ON the books beginning date", on.ClosingValue, dec("400"))

	// The UNBOUNDED report is unaffected — no as-at date means no boundary.
	all := positionRow(t, b.summary(t, ""), item, b.main, uuid.Nil)
	eq(t, "the opening quantity on the unbounded report", all.OpeningQty, dec("40"))
	eq(t, "the total stock value on the unbounded report", b.summary(t, "").TotalValue, dec("400"))
}

// --------------------------------------------------------------------------
// The corpus oracle
// --------------------------------------------------------------------------

// corpusFixture is the slice of corpora/inventory/*.json this test needs. The
// fixture is the INDEPENDENT statement of the arithmetic: it is asserted by a
// pure unit test in internal/inventory with no database anywhere near it, and
// re-asserted here through the whole stack — post path, movement ledger,
// current-version join, replay. If the two ever disagree, one of them is lying
// about what the company owns.
type corpusFixture struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Movements   []struct {
		VoucherDate string `json:"voucherDate"`
		VoucherNo   string `json:"voucherNo"`
		Direction   string `json:"direction"`
		Qty         string `json:"qty"`
		Rate        string `json:"rate"`
		DiscountPct string `json:"discountPct"`
	} `json:"movements"`
	Expected struct {
		Positions []struct {
			OpeningQty   string `json:"openingQty"`
			OpeningValue string `json:"openingValue"`
			InwardQty    string `json:"inwardQty"`
			InwardValue  string `json:"inwardValue"`
			OutwardQty   string `json:"outwardQty"`
			OutwardValue string `json:"outwardValue"`
			ClosingQty   string `json:"closingQty"`
			ClosingValue string `json:"closingValue"`
			ClosingRate  string `json:"closingRate"`
		} `json:"positions"`
	} `json:"expected"`
}

// TestClosingValueEqualsTheCorpusOracle seeds the database with the scenario the
// rate-change corpus fixture states purely, and demands the same numbers.
//
// The fixture is the oracle because it was written from the arithmetic, not from
// the code: two purchases at different prices blend into one average, and the
// issue consumes at THAT average — not at the first purchase's price, not at the
// last one's, and above all not at the price it sold for. The outward line below
// carries a sale rate of 300.0000 precisely so that valuing an issue at its own
// rate produces a visibly different, visibly wrong answer.
func TestClosingValueEqualsTheCorpusOracle(t *testing.T) {
	raw, err := os.ReadFile("../../../corpora/inventory/02-rate-change-reprices-closing.json")
	if err != nil {
		t.Fatalf("read the corpus fixture that is this test's oracle: %v", err)
	}
	var fx corpusFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("parse corpus fixture: %v", err)
	}
	if len(fx.Expected.Positions) != 1 {
		t.Fatalf("fixture %s describes %d positions; this test seeds one item at one place",
			fx.ID, len(fx.Expected.Positions))
	}
	want := fx.Expected.Positions[0]

	b := stockSetup(t)
	// The fixture's positions carry a NULL godown and a NULL batch, so the DB
	// scenario must too: an untracked dimension is its own position and pinning
	// it here keeps the two sides comparing the same bucket.
	item := b.newItem(t, "widget")
	for _, m := range fx.Movements {
		dir := ledger.Inward
		if m.Direction == "OUT" {
			dir = ledger.Outward
		}
		b.post(t, m.VoucherDate,
			stockLine(item, uuid.Nil, uuid.Nil, dir, m.Qty, m.Rate, m.DiscountPct))
	}

	got := positionRow(t, b.summary(t, ""), item, uuid.Nil, uuid.Nil)
	t.Logf("oracle %s: %s", fx.ID, fx.Description)
	eq(t, "opening quantity against the corpus", got.OpeningQty, dec(want.OpeningQty))
	eq(t, "opening value against the corpus", got.OpeningValue, dec(want.OpeningValue))
	eq(t, "inward quantity against the corpus", got.InwardQty, dec(want.InwardQty))
	eq(t, "inward value against the corpus (the 4% discount is part of the landed cost)",
		got.InwardValue, dec(want.InwardValue))
	eq(t, "outward quantity against the corpus", got.OutwardQty, dec(want.OutwardQty))
	eq(t, "the COST the issue consumed against the corpus (the sale rate of 300 must not appear anywhere)",
		got.OutwardValue, dec(want.OutwardValue))
	eq(t, "closing quantity against the corpus", got.ClosingQty, dec(want.ClosingQty))
	eq(t, "closing value against the corpus", got.ClosingValue, dec(want.ClosingValue))
	eq(t, "closing rate against the corpus", got.ClosingRate, dec(want.ClosingRate))

	// The conservation identity the whole design rests on. A valuer that invented
	// or destroyed value — the classic symptom of consuming at the sale rate —
	// breaks it even if some individual column happens to look right.
	eq(t, "opening + inwards - outwards, against the closing value",
		got.OpeningValue.Add(got.InwardValue).Sub(got.OutwardValue), got.ClosingValue)
}

// --------------------------------------------------------------------------

// TestPerGodownAndPerBatchPositionsSumToTheItemTotal pins that an item's total
// is the FOLD OF ITS POSITIONS and nothing else.
//
// Every expected figure below is worked out by hand from the fixture, so this
// is not the vacuous "the sum of the rows equals the sum of the rows": the rows
// are checked against the arithmetic first, and only then against the total.
//
// Two things make a separately-queried item total wrong. In QUANTITY, the
// position with a NULL godown vanishes the moment anything joins the godown
// master for a name. In VALUE it is worse and unfixable: each position carries
// its own average cost, and blending four of them into one pool produces a
// number that belongs to none of the four. The average of the averages is not
// the average.
func TestPerGodownAndPerBatchPositionsSumToTheItemTotal(t *testing.T) {
	b := stockSetup(t)
	item := b.newItem(t, "Alloy Plate")
	warehouse := b.newGodown(t, "Warehouse B")
	batch := b.newBatch(t, item, "B-01")

	// P1 — Main Location, no batch. 10 @ 100 then 10 @ 120 blends to 20 / 2200
	// (average 110); issuing 5 consumes 550 and leaves 15 / 1650.
	b.post(t, "2025-05-01", stockLine(item, b.main, uuid.Nil, ledger.Inward, "10", "100", "0"))
	b.post(t, "2025-05-05", stockLine(item, b.main, uuid.Nil, ledger.Inward, "10", "120", "0"))
	b.post(t, "2025-05-10", stockLine(item, b.main, uuid.Nil, ledger.Outward, "5", "999", "0"))
	// P2 — a second location. Never blended with P1's cost.
	b.post(t, "2025-05-02", stockLine(item, warehouse, uuid.Nil, ledger.Inward, "4", "200", "0"))
	// P3 — the UNTRACKED location, bought and entirely issued.
	b.post(t, "2025-05-03", stockLine(item, uuid.Nil, uuid.Nil, ledger.Inward, "6", "50", "0"))
	b.post(t, "2025-05-11", stockLine(item, uuid.Nil, uuid.Nil, ledger.Outward, "6", "80", "0"))
	// P4 — Main Location, but a batch: a different position from P1.
	b.post(t, "2025-05-04", stockLine(item, b.main, batch, ledger.Inward, "2", "500", "0"))

	s := b.summary(t, "")

	p1 := positionRow(t, s, item, b.main, uuid.Nil)
	eq(t, "Main Location's closing quantity", p1.ClosingQty, dec("15"))
	eq(t, "Main Location's closing value (10@100 + 10@120 blends to 110, less 5 issued)",
		p1.ClosingValue, dec("1650"))
	eq(t, "Main Location's cost of goods issued", p1.OutwardValue, dec("550"))

	p2 := positionRow(t, s, item, warehouse, uuid.Nil)
	eq(t, "Warehouse B's closing value (its own cost, never blended with Main Location's)",
		p2.ClosingValue, dec("800"))

	p3 := positionRow(t, s, item, uuid.Nil, uuid.Nil)
	eq(t, "the untracked location's inward value", p3.InwardValue, dec("300"))
	eq(t, "the untracked location's cost of goods issued", p3.OutwardValue, dec("300"))
	eq(t, "the untracked location's closing quantity", p3.ClosingQty, decimal.Zero)

	p4 := positionRow(t, s, item, b.main, batch)
	eq(t, "the batched position's closing value (a batch is its own position)",
		p4.ClosingValue, dec("1000"))

	if n := len(rowsForItem(s, item)); n != 4 {
		t.Fatalf("the item holds %d positions, want 4 (Main, Warehouse B, untracked location, batch B-01): %s",
			n, describePositions(s))
	}

	// The hand-computed item total. Every one of these is the sum of the four
	// positions above and NOT what a single ungrouped replay would produce.
	g := itemRollup(t, s, item)
	eq(t, "the item's inward quantity across all positions", g.InwardQty, dec("32"))
	eq(t, "the item's inward value across all positions", g.InwardValue, dec("4300"))
	eq(t, "the item's outward quantity across all positions", g.OutwardQty, dec("11"))
	eq(t, "the item's cost of goods issued across all positions", g.OutwardValue, dec("850"))
	eq(t, "the item's closing quantity across all positions", g.ClosingQty, dec("21"))
	eq(t, "the item's closing value across all positions", g.ClosingValue, dec("3450"))

	// ...and the same statement mechanically, so a fifth position added to the
	// fixture later cannot drift out of the total unnoticed.
	sumQty, sumValue, sumOut := decimal.Zero, decimal.Zero, decimal.Zero
	for _, r := range rowsForItem(s, item) {
		sumQty = sumQty.Add(r.ClosingQty)
		sumValue = sumValue.Add(r.ClosingValue)
		sumOut = sumOut.Add(r.OutwardValue)
	}
	eq(t, "the item total's closing quantity against the sum of its own position rows", g.ClosingQty, sumQty)
	eq(t, "the item total's closing value against the sum of its own position rows", g.ClosingValue, sumValue)
	eq(t, "the item total's cost of goods issued against the sum of its own position rows", g.OutwardValue, sumOut)
	eq(t, "the report's total stock value against the item total", s.TotalValue, g.ClosingValue)

	// The drill-down children must be the same rows, or the tree and the flat
	// list would tell two different stories about one company.
	if len(g.Positions) != 4 {
		t.Errorf("the item rollup carries %d drill-down positions, want the same 4 rows the flat list holds",
			len(g.Positions))
	}
}

// TestZeroClosingWithNonZeroMovementIsStillReported forbids the zero suppression
// that buildGroupNode applies to the account tree.
//
// For accounts, hiding a zero node matches Tally's "exclude zero" default and
// costs nothing. For stock it hides the most interesting row on the report: an
// item bought and then entirely sold is where the gross profit is, and dropping
// it would also make the outward column of the item total unexplainable from the
// rows printed beneath it.
func TestZeroClosingWithNonZeroMovementIsStillReported(t *testing.T) {
	b := stockSetup(t)
	item := b.newItem(t, "Tin Coil")

	b.post(t, "2025-05-01", stockLine(item, b.main, uuid.Nil, ledger.Inward, "8", "125", "0"))
	b.post(t, "2025-05-20", stockLine(item, b.main, uuid.Nil, ledger.Outward, "8", "400", "0"))

	s := b.summary(t, "")
	row := positionRow(t, s, item, b.main, uuid.Nil)
	eq(t, "the fully-consumed position's closing quantity", row.ClosingQty, decimal.Zero)
	eq(t, "the fully-consumed position's closing value", row.ClosingValue, decimal.Zero)
	eq(t, "the fully-consumed position's closing rate (nothing on hand has no rate)",
		row.ClosingRate, decimal.Zero)
	eq(t, "what the fully-consumed position took in", row.InwardQty, dec("8"))
	eq(t, "what it took in, in money", row.InwardValue, dec("1000"))
	eq(t, "what it issued", row.OutwardQty, dec("8"))
	eq(t, "the cost the issue consumed (the WHOLE remaining value, leaving no residue)",
		row.OutwardValue, dec("1000"))

	g := itemRollup(t, s, item)
	eq(t, "the item total's inward value for a fully-consumed item", g.InwardValue, dec("1000"))
	eq(t, "the item total's cost of goods issued for a fully-consumed item", g.OutwardValue, dec("1000"))
	if len(g.Positions) != 1 {
		t.Errorf("the fully-consumed item has %d drill-down positions, want 1 — a row whose closing is "+
			"zero must still be reported, or the 1000.0000 of cost above it has nowhere to come from",
			len(g.Positions))
	}
}

// TestStockSummaryIsTenantScoped is the cheap, load-bearing check that a report
// which reads four tables (movements, openings, items, dimension names) picks up
// the tenant on every one of them. RLS is the authority; this proves the report
// does not route around it.
func TestStockSummaryIsTenantScoped(t *testing.T) {
	b := stockSetup(t)
	item := b.newItem(t, "Lead Sheet")
	b.post(t, "2025-05-01", stockLine(item, b.main, uuid.Nil, ledger.Inward, "3", "70", "0"))

	other, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new company id: %v", err)
	}
	if _, err := b.rep.StockSummary(db.WithCompany(context.Background(), other), b.company, ""); err == nil {
		t.Errorf("a caller scoped to company %s read company %s's stock; the scope check must refuse it",
			other, b.company)
	}

	// And the neighbouring tenant sees an empty warehouse rather than ours.
	ids := identity.NewService(b.pool)
	octx := db.WithCompany(context.Background(), other)
	if _, err := ids.CreateCompany(octx, identity.Company{
		ID: other, Name: "Neighbour " + other.String()[:8], FYStartDate: "2025-04-01",
	}); err != nil {
		t.Fatalf("create neighbouring company: %v", err)
	}
	// The neighbour is minted INLINE here rather than through stockSetup(), so it
	// needs its own registration. Cross-tenant tests are where cleanup retrofits
	// reliably go wrong: the shared harness gets tidied, the second company in
	// each isolation probe does not, and the leak simply halves instead of
	// stopping.
	testdb.Cleanup(t, other)
	s, err := b.rep.StockSummary(octx, other, "")
	if err != nil {
		t.Fatalf("neighbour's stock summary: %v", err)
	}
	if len(s.Rows) != 0 || !s.TotalValue.IsZero() {
		t.Errorf("a company that has never bought anything holds %d stock positions valued %s",
			len(s.Rows), s.TotalValue.StringFixed(4))
	}
}

// TestReplayOrdersByENTRY_NotByVoucherNumber pins defect D8 where it actually
// bites: through the SQL, against the database, on figures a person reads.
//
// ⛔⛔ WHY THIS TEST EXISTS AND THE UNIT TEST IS NOT ENOUGH. `inventory.less()` is
// pinned by TestReplayOrdersByEveryKeyInTurn, but that test builds Movements in
// memory. NOTHING proved that `queryStockMovements` actually PUTS the ordering key
// on them. A mutation proof deleted the `r.movement.VoucherID = …` assignment and
// the ENTIRE Go suite stayed green while the wire figure was still correct — because
// with every id equal the order fell through to `version_created_at`, which on a
// seed that posts each voucher in its own transaction happens to agree with entry
// order. That is D90's shape: an application key shadowed by a tiebreak, with no
// gate able to see it missing.
//
// ⛔ SO THE ALTER IS THE POINT OF THE SECOND HALF. An ALTER keeps `voucher_id` and
// re-mints BOTH `voucher_version.created_at` and `voucher_version.id`, so restating
// the RECEIPT makes it the NEWEST version while leaving it the EARLIER voucher.
// After that, voucher_id is the ONLY key that still orders it first — and restating
// a voucher without changing a single line must not re-price anything.
func TestReplayOrdersByENTRY_NotByVoucherNumber(t *testing.T) {
	b := stockSetup(t)
	item := b.newItem(t, "Copper Bar")

	// ⛔ THE NUMBERS ARE TYPED, NOT GENERATED, AND "10" < "9" AS STRINGS. That is the
	// whole defect and it needs no second series to show it: voucher_no is a STRING,
	// so one company's own counter breaks the ordering the moment it reaches double
	// digits. (Cross-series is the commoner case in the wild — cored mints a bare
	// integer per voucher TYPE — but same-series is the tighter statement.)
	//
	// ⚠️ THE DIRECTION MATTERS AND THE FIRST DRAFT OF THIS TEST HAD IT BACKWARDS: it
	// numbered the receipt 10 and the issue 2, and "10" < "2", so voucher_no order
	// put the receipt FIRST — the correct order — and the M1 mutation (less() back to
	// voucher_no) passed straight through. The issue must carry the number that sorts
	// EARLIER, which for entry order receipt-then-issue means 9 then 10.
	const day = "2025-08-11"

	// 1. The stock the issue will be costed against: 10 @ 100.0000, on an earlier day.
	b.postNo(t, "1", "2025-08-01", stockLine(item, b.main, uuid.Nil, ledger.Inward, "10", "100", "0"))

	// 2. On ONE day: a receipt that RAISES the average, keyed FIRST and numbered 10.
	//    20 @ 250.0000 -> 30 units / 6000.0000 -> average 200.0000.
	receipt := b.postNo(t, "9", day,
		stockLine(item, b.main, uuid.Nil, ledger.Inward, "20", "250", "0"))

	// 3. …and an issue keyed SECOND and numbered 10, so it sorts FIRST by number.
	//    Entry order costs it at the POST-receipt average: 5 * 200.0000 = 1000.0000,
	//    leaving 25 / 5000.0000 @ 200.0000.
	//    ⛔ Ordered by voucher_no it would consume 5 * 100.0000 = 500.0000 at the
	//    PRE-receipt average and leave 25 / 5500.0000 @ 220.0000 — 500.0000 too high
	//    on an issue nobody edited.
	b.postNo(t, "10", day, stockLine(item, b.main, uuid.Nil, ledger.Outward, "5", "400", "0"))

	pos := positionRow(t, b.summary(t, ""), item, b.main, uuid.Nil)
	eq(t, "the quantity issued (it is the VALUE that D8 moves, never the quantity)",
		pos.OutwardQty, dec("5"))
	eq(t, "the cost of an issue keyed AFTER the receipt that funds it, on the same day, "+
		"where the issue carries the LOWER number — 500.0000 would be the pre-receipt average",
		pos.OutwardValue, dec("1000"))
	eq(t, "closing value: 5500.0000 would mean the replay used voucher_no order",
		pos.ClosingValue, dec("5000"))
	eq(t, "closing quantity — unchanged by the ordering, which is what makes this a "+
		"VALUATION defect and not a units one",
		pos.ClosingQty, dec("25"))
	eq(t, "closing rate", pos.ClosingRate, dec("200"))

	// 4. ⛔⛔ THE ARM THE MUTATION PROOF BOUGHT. Restate the RECEIPT with the SAME
	//    lines. Its voucher_version.created_at and its voucher_version.id are now
	//    the NEWEST of the three, while its voucher_id is unchanged. Ordering by
	//    either of those would move the receipt after the issue and re-price it; the
	//    figures must not move by a paisa, because nothing about the goods changed.
	b.alterNo(t, receipt, "9", day,
		stockLine(item, b.main, uuid.Nil, ledger.Inward, "20", "250", "0"))

	after := positionRow(t, b.summary(t, ""), item, b.main, uuid.Nil)
	eq(t, "the cost of the issue after RESTATING the receipt unchanged — if this reads "+
		"500.0000 the replay is ordering by version_created_at or voucher_version_id, "+
		"both of which an ALTER re-mints, and neither of which is entry order",
		after.OutwardValue, dec("1000"))
	eq(t, "closing value after restating the receipt unchanged",
		after.ClosingValue, dec("5000"))
	eq(t, "closing quantity after restating the receipt unchanged",
		after.ClosingQty, dec("25"))
}

// postNo and alterNo are post/alter with an EXPLICIT voucher number. The ordinary
// helpers let the server allocate one, which on a single class allocates in entry
// order — so a test that needs the number to DISAGREE with entry order cannot use
// them. (`VoucherInput.VoucherNo` empty = server-allocated; set = as given.)
func (b *stockBooks) postNo(t *testing.T, no, date string, lines ...ledger.InventoryLine) uuid.UUID {
	t.Helper()
	in := b.input(date, lines...)
	in.VoucherNo = no
	res, err := b.led.PostVoucher(b.ctx, in)
	if err != nil {
		t.Fatalf("post inventory voucher %q on %s: %v", no, date, err)
	}
	return res.VoucherID
}

func (b *stockBooks) alterNo(t *testing.T, id uuid.UUID, no, date string, lines ...ledger.InventoryLine) {
	t.Helper()
	in := b.input(date, lines...)
	in.VoucherNo = no
	if _, err := b.led.AlterVoucher(b.ctx, id, in); err != nil {
		t.Fatalf("alter inventory voucher %q: %v", no, err)
	}
}
