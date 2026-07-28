package reporting_test

// stock_summary_godown_test.go — the DEPTH reports against the LIVE database:
// godown columns, batch expiry ageing, and the item -> voucher drill.
//
// The four failures these pin are all "two screens disagree about one fact",
// which is worse than one screen being wrong because nothing tells the user
// which to believe:
//
//  1. A per-godown breakdown that quietly drops the UNTRACKED location. It is
//     one `IS NOT NULL` away at all times, and it leaves the columns short of
//     the row total they are supposed to explain. Asserted across a FIVE-DATE
//     sweep, because a divergence that only appears mid-history — a position
//     that has been bought but not yet sold — hides completely if the identity
//     is only checked at the end of the story.
//  2. An ageing band boundary off by one day. Every bound is inclusive on the
//     nearer band; flipping one `<=` to `<` tells a stock controller that a
//     batch is safe for three more months on the last day it could be sold.
//  3. Expired stock EXCLUDED rather than flagged. The stock is still in the
//     warehouse and still on the balance sheet; a filter makes it vanish from
//     the closing quantity and, the day closing stock is folded into the
//     Balance Sheet, understates the assets of the business.
//  4. A drill that forgets the current-version join, so a DELETED voucher's
//     lines reappear in the list that is supposed to explain a total they are
//     no longer part of.

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/reporting"
)

// --------------------------------------------------------------------------
// Fixture helpers this slice adds
// --------------------------------------------------------------------------

// newDatedBatch plants a batch master WITH manufacturing and expiry dates.
// Either may be "" for a batch that does not carry one — which is the normal
// case outside pharma, food and chemicals.
func (b *stockBooks) newDatedBatch(t *testing.T, item uuid.UUID, name, mfg, expiry string) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new batch id: %v", err)
	}
	if err := b.pool.RunInTx(b.ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO stock_batch (id, company_id, stock_item_id, name, mfg_date, expiry_date)
			 VALUES ($1, $2, $3, $4, $5::date, $6::date)`,
			id, b.company, item, name, nullIfEmpty(mfg), nullIfEmpty(expiry))
		return err
	}); err != nil {
		t.Fatalf("create dated batch %q: %v", name, err)
	}
	return id
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (b *stockBooks) godownSummary(t *testing.T, asOn string) reporting.GodownStockSummary {
	t.Helper()
	g, err := b.rep.GodownStockSummary(b.ctx, b.company, asOn)
	if err != nil {
		t.Fatalf("godown stock summary as on %q: %v", asOn, err)
	}
	return g
}

func (b *stockBooks) batchSummary(t *testing.T, asOn string) reporting.BatchStockSummary {
	t.Helper()
	s, err := b.rep.BatchStockSummary(b.ctx, b.company, asOn)
	if err != nil {
		t.Fatalf("batch stock summary as on %q: %v", asOn, err)
	}
	return s
}

func (b *stockBooks) drill(t *testing.T, item uuid.UUID, from, to string) reporting.StockItemVouchers {
	t.Helper()
	d, err := b.rep.StockItemVouchers(b.ctx, b.company, item, from, to)
	if err != nil {
		t.Fatalf("drill from item %s over %q..%q: %v", item, from, to, err)
	}
	return d
}

func godownItemRow(t *testing.T, g reporting.GodownStockSummary, item uuid.UUID) reporting.GodownItemRow {
	t.Helper()
	for _, r := range g.Items {
		if r.StockItemID == item {
			return r
		}
	}
	t.Fatalf("the godown summary as on %q holds no row for item %s; it holds %s",
		g.AsOn, item, describeGodownRows(g))
	return reporting.GodownItemRow{}
}

func godownColumnOf(t *testing.T, row reporting.GodownItemRow, godown uuid.UUID) reporting.GodownColumn {
	t.Helper()
	for _, c := range row.Columns {
		if c.GodownID == godown {
			return c
		}
	}
	t.Fatalf("item %q has no column for godown %s; it has %d columns: %s",
		row.ItemName, godown, len(row.Columns), describeColumns(row))
	return reporting.GodownColumn{}
}

func describeGodownRows(g reporting.GodownStockSummary) string {
	if len(g.Items) == 0 {
		return "no items at all"
	}
	var sb strings.Builder
	for _, r := range g.Items {
		sb.WriteString("\n    " + r.ItemName + " closing " + r.ClosingQty.StringFixed(4) +
			" valued " + r.ClosingValue.StringFixed(4) + describeColumns(r))
	}
	return sb.String()
}

func describeColumns(r reporting.GodownItemRow) string {
	var sb strings.Builder
	for _, c := range r.Columns {
		name := c.GodownName
		if c.Untracked() {
			name = "(untracked location)"
		}
		sb.WriteString("\n        " + name + " closing " + c.ClosingQty.StringFixed(4) +
			" valued " + c.ClosingValue.StringFixed(4))
	}
	return sb.String()
}

func batchItemRollup(t *testing.T, s reporting.BatchStockSummary, item uuid.UUID) reporting.BatchItemRollup {
	t.Helper()
	for _, r := range s.Items {
		if r.StockItemID == item {
			return r
		}
	}
	t.Fatalf("the batch summary as on %q holds no rollup for item %s (%d rows)", s.AsOn, item, len(s.Rows))
	return reporting.BatchItemRollup{}
}

func batchRowOf(t *testing.T, s reporting.BatchStockSummary, batch uuid.UUID) reporting.BatchStockRow {
	t.Helper()
	for _, r := range s.Rows {
		if r.BatchID == batch {
			return r
		}
	}
	t.Fatalf("the batch summary as on %q holds no row for batch %s; it holds %s",
		s.AsOn, batch, describeBatchRows(s))
	return reporting.BatchStockRow{}
}

func describeBatchRows(s reporting.BatchStockSummary) string {
	if len(s.Rows) == 0 {
		return "no batch positions at all"
	}
	var sb strings.Builder
	for _, r := range s.Rows {
		name := r.BatchName
		if name == "" {
			name = "(no batch)"
		}
		expiry := r.ExpiryDate
		if expiry == "" {
			expiry = "(no expiry)"
		}
		sb.WriteString("\n    " + r.ItemName + " / " + name + " expiring " + expiry +
			" -> " + string(r.Bucket) + " closing " + r.ClosingQty.StringFixed(4))
	}
	return sb.String()
}

func bandOf(t *testing.T, s reporting.BatchStockSummary, bucket reporting.ExpiryBucket) reporting.ExpiryBandRollup {
	t.Helper()
	for _, band := range s.Bands {
		if band.Bucket == bucket {
			return band
		}
	}
	t.Fatalf("the batch summary carries no %q band; every report must carry all five bands, "+
		"empty ones included, or \"nothing has expired\" is indistinguishable from \"the expired band was dropped\"",
		bucket)
	return reporting.ExpiryBandRollup{}
}

// --------------------------------------------------------------------------
// 1. Godown columns
// --------------------------------------------------------------------------

// TestGodownSummarySumsToStockSummary is the identity the whole godown
// breakdown rests on: for EVERY item on EVERY date, the per-location cells add
// up to exactly the item total the plain Stock Summary reports — in quantity and
// in money.
//
// The five dates are the point. A breakdown that drops the untracked location
// looks perfectly correct as at the end of the story below, because that
// position was bought and entirely issued and closes at zero; it only shows up
// while the stock is still on hand, or in the inward/outward columns. One
// convenient as-on date is how this bug ships.
//
// The expected totals per date are worked out BY HAND from the fixture, so this
// is not "the sum of the rows equals the sum of the rows": the report is pinned
// to arithmetic first and to the other report second.
func TestGodownSummarySumsToStockSummary(t *testing.T) {
	b := stockSetup(t)
	plate := b.newItem(t, "Alloy Plate")
	salt := b.newItem(t, "Rock Salt")
	warehouse := b.newGodown(t, "Warehouse B")
	batch := b.newBatch(t, plate, "B-01")

	// Rock Salt exists only as an OPENING at Main Location: 40 @ 10 = 400. It
	// keeps a position in the sweep that has no movement at all, which is the
	// one a movement-driven breakdown loses first.
	b.opening(t, salt, b.main, uuid.Nil, "40", "10")

	// Alloy Plate, four positions across three locations:
	//   Main / no batch : 10 @ 100 then 10 @ 120 blends to 20 / 2200 (avg 110);
	//                     issuing 5 consumes 550 and leaves 15 / 1650.
	b.post(t, "2025-05-01", stockLine(plate, b.main, uuid.Nil, ledger.Inward, "10", "100", "0"))
	b.post(t, "2025-05-05", stockLine(plate, b.main, uuid.Nil, ledger.Inward, "10", "120", "0"))
	b.post(t, "2025-05-10", stockLine(plate, b.main, uuid.Nil, ledger.Outward, "5", "999", "0"))
	//   Warehouse B     : 4 @ 200 = 800, never blended with Main's cost.
	b.post(t, "2025-05-02", stockLine(plate, warehouse, uuid.Nil, ledger.Inward, "4", "200", "0"))
	//   UNTRACKED       : 6 @ 50 in, all 6 out again — closes at zero, which is
	//                     exactly why it is easy to lose without noticing.
	b.post(t, "2025-05-03", stockLine(plate, uuid.Nil, uuid.Nil, ledger.Inward, "6", "50", "0"))
	b.post(t, "2025-05-11", stockLine(plate, uuid.Nil, uuid.Nil, ledger.Outward, "6", "80", "0"))
	//   Main / B-01     : 2 @ 500 = 1000. A batch is its own position but the
	//                     same COLUMN as the un-batched Main stock.
	b.post(t, "2025-05-04", stockLine(plate, b.main, batch, ledger.Inward, "2", "500", "0"))

	sweep := []struct {
		asOn       string
		totalValue string
		why        string
	}{
		{"2025-04-30", "400", "before any movement: only Rock Salt's opening, which the books DO carry from 2025-04-01"},
		{"2025-05-02", "2200", "Main 10@100=1000 + Warehouse B 4@200=800 + salt 400"},
		{"2025-05-06", "4700", "Main 20/2200 + batch 1000 + Warehouse B 800 + untracked 300 + salt 400"},
		{"2025-05-10", "4150", "the issue of 5 consumed 550 at the 110 average; the untracked 300 is still on hand"},
		{"2025-12-31", "3850", "the untracked position has been entirely issued and closes at zero"},
	}

	for _, tc := range sweep {
		asOn := tc.asOn
		flat := b.summary(t, asOn)
		byGodown := b.godownSummary(t, asOn)

		eq(t, "the whole inventory's value as on "+asOn+" ("+tc.why+")",
			byGodown.TotalValue, dec(tc.totalValue))
		eq(t, "the godown breakdown's total against the Stock Summary's total as on "+asOn,
			byGodown.TotalValue, flat.TotalValue)

		if len(byGodown.Items) != len(flat.Items) {
			t.Fatalf("as on %s the godown breakdown reports %d items and the Stock Summary reports %d; "+
				"the two must describe the same books: %s", asOn, len(byGodown.Items), len(flat.Items),
				describeGodownRows(byGodown))
		}

		// Every item, both reports, all eight figures.
		for _, want := range flat.Items {
			got := godownItemRow(t, byGodown, want.StockItemID)
			label := " for " + want.ItemName + " as on " + asOn
			eq(t, "the godown breakdown's opening quantity"+label, got.OpeningQty, want.OpeningQty)
			eq(t, "the godown breakdown's opening value"+label, got.OpeningValue, want.OpeningValue)
			eq(t, "the godown breakdown's inward quantity"+label, got.InwardQty, want.InwardQty)
			eq(t, "the godown breakdown's inward value"+label, got.InwardValue, want.InwardValue)
			eq(t, "the godown breakdown's outward quantity"+label, got.OutwardQty, want.OutwardQty)
			eq(t, "the godown breakdown's cost of goods issued"+label, got.OutwardValue, want.OutwardValue)
			eq(t, "the godown breakdown's closing quantity"+label, got.ClosingQty, want.ClosingQty)
			eq(t, "the godown breakdown's closing value"+label, got.ClosingValue, want.ClosingValue)

			// ...and the cells must add up to the row printed across them.
			cellQty, cellValue, cellIn, cellOut := decimal.Zero, decimal.Zero, decimal.Zero, decimal.Zero
			for _, c := range got.Columns {
				cellQty = cellQty.Add(c.ClosingQty)
				cellValue = cellValue.Add(c.ClosingValue)
				cellIn = cellIn.Add(c.InwardValue)
				cellOut = cellOut.Add(c.OutwardValue)
			}
			eq(t, "the sum of the location cells against the item's closing quantity"+label, cellQty, want.ClosingQty)
			eq(t, "the sum of the location cells against the item's closing value"+label, cellValue, want.ClosingValue)
			eq(t, "the sum of the location cells against the item's inward value"+label, cellIn, want.InwardValue)
			eq(t, "the sum of the location cells against the item's cost of goods issued"+label, cellOut, want.OutwardValue)
		}

		// The column FOOTERS are the other axis of the same table, and they must
		// foot to the same grand total.
		footer := decimal.Zero
		for _, g := range byGodown.Godowns {
			footer = footer.Add(g.ClosingValue)
		}
		eq(t, "the sum of the column footers against the whole inventory as on "+asOn,
			footer, byGodown.TotalValue)
	}

	// The end-state columns, by hand. The untracked column must be PRESENT even
	// though it closes at zero — its 300 of purchases and 300 of issues are part
	// of the item row above it and have to come from somewhere.
	end := b.godownSummary(t, "2025-12-31")
	row := godownItemRow(t, end, plate)
	if len(row.Columns) != 3 {
		t.Fatalf("Alloy Plate has %d location columns, want 3 (Main Location, Warehouse B, untracked): %s",
			len(row.Columns), describeColumns(row))
	}

	main := godownColumnOf(t, row, b.main)
	eq(t, "Main Location's closing quantity (un-batched 15 plus batch B-01's 2)", main.ClosingQty, dec("17"))
	eq(t, "Main Location's closing value (1650 un-batched plus 1000 in B-01)", main.ClosingValue, dec("2650"))
	if len(main.Positions) != 2 {
		t.Errorf("Main Location's cell folds %d positions, want 2 (un-batched and B-01) — the cell is a "+
			"FOLD of position rows and must keep them for the drill", len(main.Positions))
	}

	wh := godownColumnOf(t, row, warehouse)
	eq(t, "Warehouse B's closing value (its own cost, never blended with Main Location's)",
		wh.ClosingValue, dec("800"))

	untracked := godownColumnOf(t, row, uuid.Nil)
	if !untracked.Untracked() {
		t.Errorf("the column for uuid.Nil does not report itself as the untracked location")
	}
	eq(t, "the untracked location's closing quantity", untracked.ClosingQty, decimal.Zero)
	eq(t, "the untracked location's inward value (it is real stock that was really bought)",
		untracked.InwardValue, dec("300"))
	eq(t, "the untracked location's cost of goods issued", untracked.OutwardValue, dec("300"))

	// A nameless column belongs at the END of the sheet, not at the front.
	if last := row.Columns[len(row.Columns)-1]; !last.Untracked() {
		t.Errorf("the last column of the row is %q; the untracked column has no header and must print last",
			last.GodownName)
	}
}

// --------------------------------------------------------------------------
// 2. Expiry ageing
// --------------------------------------------------------------------------

// TestBatchExpiryAgeingBuckets walks a batch across every band boundary and
// pins that each one lands in EXACTLY ONE band.
//
// Every boundary case in the table sits exactly ON a bound, because that is the
// only place the bug lives: `<= 30` and `< 30` agree about every batch in the
// world except the one expiring in exactly 30 days, and that batch is precisely
// the one a stock controller needed to be told about. The same off-by-one on the
// accounting period bounds survived an entire phase of green tests for want of a
// fixture sitting on the line.
func TestBatchExpiryAgeingBuckets(t *testing.T) {
	b := stockSetup(t)
	item := b.newItem(t, "Vaccine Vial")

	const asOn = "2025-06-01"

	cases := []struct {
		name   string
		expiry string
		qty    string
		rate   string
		bucket reporting.ExpiryBucket
		days   int
		why    string
	}{
		{"E-PAST", "2025-05-31", "3", "100", reporting.ExpiryExpired, -1,
			"expired yesterday: the date has PASSED"},
		{"E-TODAY", "2025-06-01", "4", "100", reporting.ExpiryDueIn30, 0,
			"expires today, which is still sellable today — expiry is not retroactive"},
		{"E-30", "2025-07-01", "5", "100", reporting.ExpiryDueIn30, 30,
			"EXACTLY 30 days out: the 30-day bound is inclusive"},
		{"E-31", "2025-07-02", "6", "100", reporting.ExpiryDueIn90, 31,
			"one day past the 30-day bound"},
		{"E-90", "2025-08-30", "7", "100", reporting.ExpiryDueIn90, 90,
			"EXACTLY 90 days out: the 90-day bound is inclusive"},
		{"E-91", "2025-08-31", "8", "100", reporting.ExpiryBeyond90, 91,
			"one day past the 90-day bound"},
		{"NO-DATE", "", "9", "100", reporting.ExpiryNotTracked, 0,
			"a batch that carries no expiry date at all"},
	}

	batches := map[string]uuid.UUID{}
	for _, tc := range cases {
		id := b.newDatedBatch(t, item, tc.name, "", tc.expiry)
		batches[tc.name] = id
		b.post(t, "2025-04-15", stockLine(item, b.main, id, ledger.Inward, tc.qty, tc.rate, "0"))
	}
	// ...and one position with NO BATCH AT ALL, which is not perishable stock
	// with a missing date but ordinary stock that nobody batches.
	b.post(t, "2025-04-15", stockLine(item, b.main, uuid.Nil, ledger.Inward, "2", "100", "0"))

	s := b.batchSummary(t, asOn)

	for _, tc := range cases {
		row := batchRowOf(t, s, batches[tc.name])
		if row.Bucket != tc.bucket {
			t.Errorf("batch %s (expiring %q, %s) is banded %q, want %q",
				tc.name, tc.expiry, tc.why, row.Bucket, tc.bucket)
		}
		if row.DaysToExpiry != tc.days {
			t.Errorf("batch %s is %d days from expiry as on %s, want %d",
				tc.name, row.DaysToExpiry, asOn, tc.days)
		}
		if want := tc.bucket == reporting.ExpiryExpired; row.Expired != want {
			t.Errorf("batch %s reports Expired=%v, want %v (%s)", tc.name, row.Expired, want, tc.why)
		}
		if row.ExpiryDate != tc.expiry {
			t.Errorf("batch %s reports expiry date %q, want %q", tc.name, row.ExpiryDate, tc.expiry)
		}
	}

	// EXACTLY ONE band. Counted by walking the bands rather than trusting the
	// row's own label: a report that filed a row twice (overlapping bounds) or
	// nowhere (a gap between them) would still print a plausible label on the
	// row itself while the band totals stopped adding up.
	seen := map[uuid.UUID]int{}
	banded, bandValue := 0, decimal.Zero
	for _, band := range s.Bands {
		if band.RowCount != len(band.Rows) {
			t.Errorf("the %q band counts %d rows but carries %d", band.Bucket, band.RowCount, len(band.Rows))
		}
		for _, r := range band.Rows {
			seen[r.BatchID]++
			banded++
		}
		bandValue = bandValue.Add(band.ClosingValue)
	}
	if banded != len(s.Rows) {
		t.Errorf("the bands hold %d rows between them and the report holds %d; every position must land "+
			"in exactly one band: %s", banded, len(s.Rows), describeBatchRows(s))
	}
	for _, tc := range cases {
		if n := seen[batches[tc.name]]; n != 1 {
			t.Errorf("batch %s appears in %d bands, want exactly 1", tc.name, n)
		}
	}
	eq(t, "the sum of the bands against the whole inventory's value", bandValue, s.TotalValue)

	// All five bands, in nearest-deadline-first order, empty ones included.
	wantOrder := []reporting.ExpiryBucket{
		reporting.ExpiryExpired, reporting.ExpiryDueIn30, reporting.ExpiryDueIn90,
		reporting.ExpiryBeyond90, reporting.ExpiryNotTracked,
	}
	if len(s.Bands) != len(wantOrder) {
		t.Fatalf("the report carries %d bands, want %d", len(s.Bands), len(wantOrder))
	}
	for i, want := range wantOrder {
		if s.Bands[i].Bucket != want {
			t.Errorf("band %d is %q, want %q — the bands print nearest deadline first",
				i, s.Bands[i].Bucket, want)
		}
		if s.Bands[i].Label == "" {
			t.Errorf("band %q has no human label; a printed statement must be able to name its own bands", want)
		}
	}

	// The un-batched position and the dateless batch share the NO_EXPIRY band:
	// 9 units of NO-DATE at 100 plus 2 un-batched at 100.
	noExpiry := bandOf(t, s, reporting.ExpiryNotTracked)
	eq(t, "the no-expiry band's value (the dateless batch AND the position with no batch at all)",
		noExpiry.ClosingValue, dec("1100"))

	// An ageing report with no date to be relative to is refused rather than
	// silently aged against the machine's clock.
	if _, err := b.rep.BatchStockSummary(b.ctx, b.company, ""); err == nil {
		t.Errorf("a batch ageing report with no as-on date was accepted; a band is defined RELATIVE to a date")
	}
}

// TestExpiredStockStillCountsInClosingQuantity is the rule the whole file
// exists to protect: expired stock is FLAGGED, never excluded.
//
// It is still physically in the warehouse and still the company's property. A
// report that filtered it out would show a smaller, tidier, fictional
// inventory — and the same filter, the day closing stock is folded into the
// Balance Sheet, understates the assets of the business. Writing stock off is a
// VOUCHER somebody signs, not a WHERE clause in a report.
func TestExpiredStockStillCountsInClosingQuantity(t *testing.T) {
	b := stockSetup(t)
	item := b.newItem(t, "Serum Ampoule")

	good := b.newDatedBatch(t, item, "GOOD", "2025-01-01", "2026-01-01")
	bad := b.newDatedBatch(t, item, "BAD", "2024-01-01", "2025-05-01")

	// 10 @ 50 = 500 still good, 4 @ 25 = 100 already expired.
	b.post(t, "2025-04-10", stockLine(item, b.main, good, ledger.Inward, "10", "50", "0"))
	b.post(t, "2025-04-11", stockLine(item, b.main, bad, ledger.Inward, "4", "25", "0"))

	const asOn = "2025-06-01"
	s := b.batchSummary(t, asOn)
	flat := b.summary(t, asOn)

	// THE QUANTITY FIRST, because that is the claim in this test's name and it
	// must be the failure a reader sees: the expired stock is inside the item
	// total, and that total is what the plain Stock Summary already says.
	got := batchItemRollup(t, s, item)
	want := itemRollup(t, flat, item)
	eq(t, "the item's closing quantity including the expired batch", got.ClosingQty, dec("14"))
	eq(t, "the item's closing value including the expired batch", got.ClosingValue, dec("600"))
	eq(t, "the batch report's closing quantity against the Stock Summary's", got.ClosingQty, want.ClosingQty)
	eq(t, "the batch report's closing value against the Stock Summary's", got.ClosingValue, want.ClosingValue)
	eq(t, "the whole inventory's value against the Stock Summary's", s.TotalValue, flat.TotalValue)

	expired := batchRowOf(t, s, bad)
	if !expired.Expired || expired.Bucket != reporting.ExpiryExpired {
		t.Fatalf("the batch that expired on 2025-05-01 is banded %q (Expired=%v) as on %s, want EXPIRED",
			expired.Bucket, expired.Expired, asOn)
	}
	eq(t, "the EXPIRED batch's closing quantity (it is still in the warehouse)", expired.ClosingQty, dec("4"))
	eq(t, "the EXPIRED batch's closing value (still carried at cost until somebody writes it off)",
		expired.ClosingValue, dec("100"))

	// The expired part is reported as a SUBSET, never as a deduction.
	eq(t, "the item's expired quantity", got.ExpiredQty, dec("4"))
	eq(t, "the item's expired value", got.ExpiredValue, dec("100"))
	eq(t, "the report's headline expired value", s.ExpiredValue, dec("100"))
	if got.ClosingValue.LessThan(got.ExpiredValue) {
		t.Errorf("the expired value %s exceeds the closing value %s; the expired figure is a SUBSET of the "+
			"position, not a deduction from it",
			got.ExpiredValue.StringFixed(4), got.ClosingValue.StringFixed(4))
	}

	// And the bands still foot: 500 good + 100 expired.
	eq(t, "the expired band's value", bandOf(t, s, reporting.ExpiryExpired).ClosingValue, dec("100"))
	eq(t, "the beyond-90-days band's value", bandOf(t, s, reporting.ExpiryBeyond90).ClosingValue, dec("500"))

	// Expired stock sorts to the TOP of the report — the only place it can do
	// its job.
	if len(s.Rows) == 0 || s.Rows[0].BatchID != bad {
		t.Errorf("the first row of the ageing report is not the expired batch: %s", describeBatchRows(s))
	}
}

// --------------------------------------------------------------------------
// 3. The drill
// --------------------------------------------------------------------------

// TestDrillFromItemToVouchersUsesTheCurrentVersionJoin pins that the drill and
// the summary above it tell the same story.
//
// Movements are versioned children: an ALTER re-states them under a NEW
// voucher_version and the superseded rows stay in stock_movement forever, and a
// DELETE writes a tombstone version carrying no movement rows at all. Drop the
// voucher_head join from the drill and BOTH of those come back — a deleted
// voucher's line reappears in the list that is supposed to explain a total it is
// no longer part of, and the user is left with two screens disagreeing about
// whether a purchase happened.
func TestDrillFromItemToVouchersUsesTheCurrentVersionJoin(t *testing.T) {
	b := stockSetup(t)
	item := b.newItem(t, "Ferrite Core")

	kept := b.post(t, "2025-05-01", stockLine(item, b.main, uuid.Nil, ledger.Inward, "10", "100", "0"))
	removed := b.post(t, "2025-05-10", stockLine(item, b.main, uuid.Nil, ledger.Inward, "5", "120", "0"))

	// The first voucher is RESTATED at 4 units; the second is DELETED.
	b.alter(t, kept, "2025-05-01", stockLine(item, b.main, uuid.Nil, ledger.Inward, "4", "100", "0"))
	b.del(t, removed)

	d := b.drill(t, item, "", "")
	if len(d.Lines) != 1 {
		t.Fatalf("the drill lists %d lines, want exactly 1: the altered voucher's SUPERSEDED line (10 units) "+
			"and the DELETED voucher's line (5 units) must both be absent. Lines: %s", len(d.Lines), describeDrill(d))
	}
	line := d.Lines[0]
	if line.VoucherID != kept {
		t.Errorf("the drill's only line belongs to voucher %s, want the surviving voucher %s", line.VoucherID, kept)
	}
	if line.VoucherID == removed {
		t.Errorf("the DELETED voucher %s is still listed in the drill", removed)
	}
	eq(t, "the drilled line's quantity (the ALTER restated 10 as 4; 10 would mean the superseded version was read)",
		line.Qty, dec("4"))
	if line.VersionNo != 2 {
		t.Errorf("the drilled line is version %d, want version 2 — the drill must read the CURRENT version",
			line.VersionNo)
	}
	eq(t, "the drill's total inward quantity", d.InwardQty, dec("4"))
	eq(t, "the drill's total outward quantity", d.OutwardQty, decimal.Zero)

	// THE POINT: the drill and the summary it hangs off must agree.
	pos := positionRow(t, b.summary(t, ""), item, b.main, uuid.Nil)
	eq(t, "the drill's inward quantity against the Stock Summary's inward quantity for the same item",
		d.InwardQty, pos.InwardQty)

	// The line carries what a register needs to be readable, including its
	// dimensions and the document it came from.
	if line.Date != "2025-05-01" {
		t.Errorf("the drilled line is dated %q, want 2025-05-01", line.Date)
	}
	if line.GodownID != b.main {
		t.Errorf("the drilled line's godown is %s, want Main Location %s", line.GodownID, b.main)
	}
	if line.Party != "Stock Supplies" {
		t.Errorf("the drilled line's party is %q, want %q", line.Party, "Stock Supplies")
	}
	if line.VoucherTypeName == "" {
		t.Errorf("the drilled line has no voucher type name; the drill must resolve it for the register")
	}
	if line.Direction != string(ledger.Inward) {
		t.Errorf("the drilled line's direction is %q, want IN", line.Direction)
	}

	// It is a PERIOD, both bounds inclusive: the surviving voucher is dated
	// exactly on the bound of the first window and outside the second.
	if n := len(b.drill(t, item, "2025-05-01", "2025-05-01").Lines); n != 1 {
		t.Errorf("a window of exactly the voucher's own date lists %d lines, want 1 — both bounds are inclusive", n)
	}
	if n := len(b.drill(t, item, "2025-05-02", "2025-12-31").Lines); n != 0 {
		t.Errorf("a window that starts after the only surviving voucher lists %d lines, want 0", n)
	}

	// An item that is not in these books is an ERROR, not an empty register:
	// "this item never moved" and "that id is not yours" are different answers,
	// and conflating them lets a caller probe another tenant's ids by the shape
	// of the reply.
	stranger, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new id: %v", err)
	}
	if _, err := b.rep.StockItemVouchers(b.ctx, b.company, stranger, "", ""); err == nil {
		t.Errorf("the drill answered for stock item %s, which does not exist in these books", stranger)
	}
}

// TestDepthReportsRefuseACallerScopedToAnotherCompany is the cheap,
// load-bearing check every new entry point in this package owes.
//
// RLS is the authority and db.RunInTx's GUC guard is the backstop, but a report
// that skipped the scope() call would let a caller scoped to company A ask for
// company B's books and get an EMPTY answer rather than a refusal — which reads
// as "B owns no stock" and is a far more dangerous reply than an error.
func TestDepthReportsRefuseACallerScopedToAnotherCompany(t *testing.T) {
	b := stockSetup(t)
	item := b.newItem(t, "Cobalt Powder")
	b.post(t, "2025-05-01", stockLine(item, b.main, uuid.Nil, ledger.Inward, "3", "70", "0"))

	other, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new company id: %v", err)
	}
	octx := db.WithCompany(context.Background(), other)

	if _, err := b.rep.GodownStockSummary(octx, b.company, ""); err == nil {
		t.Errorf("a caller scoped to company %s read company %s's godown breakdown", other, b.company)
	}
	if _, err := b.rep.BatchStockSummary(octx, b.company, "2025-06-01"); err == nil {
		t.Errorf("a caller scoped to company %s read company %s's batch ageing", other, b.company)
	}
	if _, err := b.rep.StockItemVouchers(octx, b.company, item, "", ""); err == nil {
		t.Errorf("a caller scoped to company %s drilled into company %s's stock item", other, b.company)
	}
}

func describeDrill(d reporting.StockItemVouchers) string {
	if len(d.Lines) == 0 {
		return "none"
	}
	var sb strings.Builder
	for _, l := range d.Lines {
		sb.WriteString("\n    " + l.Date + " " + l.VoucherTypeName + " " + l.VoucherNo +
			" v" + decimal.NewFromInt(int64(l.VersionNo)).String() +
			" " + l.Direction + " " + l.Qty.StringFixed(4) + " (voucher " + l.VoucherID.String() + ")")
	}
	return sb.String()
}
