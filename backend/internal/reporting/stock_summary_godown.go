package reporting

// stock_summary_godown.go — STOCK SUMMARY DEPTH: godown columns, and the drill
// from an item down to the vouchers that moved it.
//
// WHY THESE REPORTS REPLAY INSTEAD OF AGGREGATING, AGAIN
// -----------------------------------------------------
// Everything stock_summary.go's header says applies verbatim here: quantity
// comes out of the movement ledger, VALUE is derived by replaying that ledger
// through internal/inventory's pure Valuer, and no SUM(qty * rate) appears
// anywhere. A per-godown breakdown is exactly where a SQL aggregate is most
// tempting and most wrong — each location carries its OWN weighted-average
// cost, so a `GROUP BY godown_id` over qty*rate would value Warehouse B's stock
// at a blend of what Main Location paid.
//
// WHY THE DEPTH REPORTS DERIVE FROM THE SAME REPLAY AS THE SUMMARY
// ---------------------------------------------------------------
// stockPositionsAsOn below performs the SAME four steps StockSummary performs,
// in the same order, from the same loaders. That is deliberate and it is the
// whole safety property of this slice: a Stock Summary and its own godown
// breakdown must never be able to disagree about how much stock the company
// owns. Two screens that disagree about one fact is worse than one screen that
// is wrong, because there is no way for a user to tell which to believe.
// TestGodownSummarySumsToStockSummary demands the identity across a five-date
// sweep rather than at one convenient date, so a divergence that only shows up
// mid-history (a back-dated voucher, an ALTER) cannot hide.
//
// The two things a per-godown fold gets wrong quietly:
//
//   1. THE UNTRACKED LOCATION. A movement may carry no godown at all (the
//      dimension is NULLable, and a company that never enables multi-location
//      posts every line that way). It is a POSITION like any other and it gets
//      a column like any other. The moment anything inner-joins the godown
//      master for a name — or writes `WHERE godown_id IS NOT NULL` — that stock
//      vanishes from the breakdown while remaining in the item total, and the
//      columns stop adding up to the row. That is the mutation
//      TestGodownSummarySumsToStockSummary exists to catch.
//   2. THE COLUMN TOTAL. Each column is a FOLD OF POSITION ROWS, never a
//      re-query, for the same reason StockItemRollup is: two batches inside one
//      godown carry two averages, and the average of the averages is not the
//      average. Which is why a fold carries no rate — ask a position row.
//
// THE DRILL (item -> vouchers) IS THE OTHER HALF OF THE SAME STATEMENT.
// RESEARCH sec 8: no report is a dead end. It repeats the current-version join
// verbatim, because a drill that showed a superseded or deleted voucher's lines
// while the summary above it did not would be the two-screens-disagreeing bug
// in its most confusing form: a total nobody can reconcile to the list that is
// supposed to explain it.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/inventory"
)

// --------------------------------------------------------------------------
// The shared fold shape
// --------------------------------------------------------------------------

// StockFigures is the Opening/Inward/Outward/Closing quartet in quantity and in
// money — the eight numbers every FOLD in the depth reports carries.
//
// It deliberately has NO closing rate. A fold spans several positions (a godown
// spans its batches, an item spans its godowns) and each of those positions
// carries its own weighted-average cost; blending them produces a rate that
// belongs to none of them and that nobody can act on. Ask a StockPositionRow
// for a rate — it is the only level at which one is meaningful.
type StockFigures struct {
	OpeningQty   decimal.Decimal
	OpeningValue decimal.Decimal
	InwardQty    decimal.Decimal
	InwardValue  decimal.Decimal
	OutwardQty   decimal.Decimal
	OutwardValue decimal.Decimal
	ClosingQty   decimal.Decimal
	ClosingValue decimal.Decimal
}

// zeroFigures starts a fold at an explicit zero on every field rather than
// relying on the decimal zero value, matching rollupStockItems: a report struct
// that reaches a JSON encoder with unset decimals is a source of "0" vs ""
// noise nobody wants to debug.
func zeroFigures() StockFigures {
	return StockFigures{
		OpeningQty:   decimal.Zero,
		OpeningValue: decimal.Zero,
		InwardQty:    decimal.Zero,
		InwardValue:  decimal.Zero,
		OutwardQty:   decimal.Zero,
		OutwardValue: decimal.Zero,
		ClosingQty:   decimal.Zero,
		ClosingValue: decimal.Zero,
	}
}

// add folds one replayed position into the running figures.
func (f *StockFigures) add(p inventory.Position) {
	f.OpeningQty = f.OpeningQty.Add(p.OpeningQty)
	f.OpeningValue = f.OpeningValue.Add(p.OpeningValue)
	f.InwardQty = f.InwardQty.Add(p.InwardQty)
	f.InwardValue = f.InwardValue.Add(p.InwardValue)
	f.OutwardQty = f.OutwardQty.Add(p.OutwardQty)
	f.OutwardValue = f.OutwardValue.Add(p.OutwardValue)
	f.ClosingQty = f.ClosingQty.Add(p.ClosingQty)
	f.ClosingValue = f.ClosingValue.Add(p.ClosingValue)
}

// --------------------------------------------------------------------------
// The shared derivation
// --------------------------------------------------------------------------

// resolvedStockAsOn is THE ONE normalisation of an as-on parameter in the stock
// plane: it returns the boundary the replay will ACTUALLY be bounded at, and
// "" only when the replay is genuinely UNBOUNDED.
//
// ⛔ WHY A REPORT MAY NOT ECHO THE CALLER'S STRING. Three reports here
// (StockSummary, GodownStockSummary, BatchStockSummary) publish an `AsOn` field
// that a client renders as a caption over the figures. They all echoed the
// caller's parameter verbatim, which is the same shape that made the Bank
// Reconciliation header render "As at " with nothing after it — and it is a
// caption asserting a fact about the FIGURES, so it must come from the same
// value the figures did or it is only an assumption in a serif font.
//
// AND THE TWO CONSUMERS OF THIS DATE DO NOT AGREE ABOUT WHITESPACE, which is
// the sharp edge under the cosmetic one:
//
//   - loadStockPositionMovements -> nullDate() -> Postgres, which TRIMS.
//   - loadStockOpenings compares it as a GO STRING against
//     company.books_beginning_from ("ISO dates compare correctly as strings",
//     which is true only of well-formed ones).
//
// So " 2026-06-30" bounds the MOVEMENTS at 2026-06-30 while making the openings
// guard read `" 2026-06-30" < "2025-04-01"` — TRUE, because a space sorts below
// a digit — and the entire opening position is silently dropped. The report
// still foots, still renders, and is short by exactly the stock the company
// began with, in QUANTITY AND IN VALUE. Resolving here, once, before either
// loader runs, is what makes the two consumers structurally incapable of
// disagreeing.
//
// ⚠️ STATED PLAINLY: no HTTP caller can reach that today — `reportDate` in
// package server trims and then rejects anything time.Parse will not take, so
// this is defence in depth for in-process callers (closing_stock.go's Balance
// Sheet fold-in among them) and a guarantee for the echo, not a driven bypass.
// The ECHO half IS driven: the Stock Summary screen renders this value.
//
// It deliberately does NOT default an empty date to today. That would change
// which vouchers the replay sees, which MOVES THE BALANCE SHEET (the Closing
// Stock asset comes off this same derivation) and needs a deliberate
// verify:demo re-pin. "" here means UNBOUNDED and every reader must say so.
func resolvedStockAsOn(asOn string) string { return strings.TrimSpace(asOn) }

// stockPositionsAsOn is the position of every (item, godown, batch) bucket as at
// `asOn`, in exactly the form StockSummary builds it: the same names, the same
// books-beginning-guarded openings, the same movement ledger bounded by one
// date and no lower bound, replayed through the same per-item valuers and sorted
// into the same order.
//
// It exists so the DEPTH reports in this slice and the summary itself cannot
// drift apart. `asOn` empty means "everything ever posted", as everywhere else
// on this seam.
func stockPositionsAsOn(ctx context.Context, tx pgx.Tx, asOn string) ([]StockPositionRow, stockNames, error) {
	// ⛔ RESOLVE ONCE, HERE, BEFORE ANY LOADER SEES IT. See resolvedStockAsOn:
	// the two loaders below consume this date in two DIFFERENT ways (one hands
	// it to Postgres, one compares it as a Go string), and the raw parameter
	// makes them disagree.
	asOn = resolvedStockAsOn(asOn)

	names, err := loadStockNames(ctx, tx)
	if err != nil {
		return nil, names, err
	}
	openings, err := loadStockOpenings(ctx, tx, asOn)
	if err != nil {
		return nil, names, err
	}
	movements, err := loadStockPositionMovements(ctx, tx, asOn)
	if err != nil {
		return nil, names, err
	}
	positions, err := replayPositions(openings, movementsOf(movements, true), names)
	if err != nil {
		return nil, names, err
	}

	rows := make([]StockPositionRow, 0, len(positions))
	for key, pos := range positions {
		rows = append(rows, stockRowOf(key, pos, names))
	}
	sortStockRows(rows)
	return rows, names, nil
}

// --------------------------------------------------------------------------
// Godown columns
// --------------------------------------------------------------------------

// GodownColumn is ONE godown's slice of ONE item: the fold of every position of
// that item held at that location, with those positions kept as drill-down
// children.
//
// GodownID is uuid.Nil (and GodownName "") for the UNTRACKED location — stock
// posted on a line that named no godown. That is a real column holding real
// stock, not a placeholder to filter away; a UI renders it as "-".
type GodownColumn struct {
	GodownID   uuid.UUID
	GodownName string

	StockFigures

	// Positions are the (item, godown, batch) rows this column folds, which is
	// where a batch-level rate can still be read.
	Positions []StockPositionRow
}

// Untracked reports whether this column is the no-godown bucket, so callers do
// not have to know that uuid.Nil is how that is spelled.
func (c GodownColumn) Untracked() bool { return c.GodownID == uuid.Nil }

// GodownItemRow is one item across every location that holds it: the item total
// plus one column per godown.
//
// The item total is the fold of the COLUMNS and is never re-queried, so the row
// and the cells printed across it are the same arithmetic by construction. It
// must also equal the StockItemRollup the plain Stock Summary reports for the
// same item on the same date — that is the identity
// TestGodownSummarySumsToStockSummary pins.
type GodownItemRow struct {
	StockItemID uuid.UUID
	ItemName    string
	UnitSymbol  string

	StockFigures

	Columns []GodownColumn
}

// GodownTotal is one location's column footer: everything that location holds,
// across every item.
//
// There is no quantity in a location total that spans items and there must
// never be: adding kilograms of one item to litres of another produces a number
// with no unit. Only VALUE is additive across items, which is the same reason
// StockSummary carries a TotalValue and no total quantity.
type GodownTotal struct {
	GodownID   uuid.UUID
	GodownName string

	OpeningValue decimal.Decimal
	InwardValue  decimal.Decimal
	OutwardValue decimal.Decimal
	ClosingValue decimal.Decimal
}

// Untracked reports whether this footer is the no-godown bucket.
func (g GodownTotal) Untracked() bool { return g.GodownID == uuid.Nil }

// GodownStockSummary is the Stock Summary broken out BY LOCATION as at one
// date: item-major rows with one cell per godown, plus the column footers.
//
// It is a POSITION and takes ONE date, for the reason stock_summary.go's header
// gives — a lower bound would drop the stock the company already owned and
// still produce a tidy table.
type GodownStockSummary struct {
	// AsOn is the boundary the replay was ACTUALLY bounded at — always
	// RESOLVED, never the caller's parameter (see resolvedStockAsOn). Empty
	// means UNBOUNDED: every voucher ever posted, INCLUDING ones dated after
	// today. It does NOT mean "today", and a client that renders it as one is
	// stating something this report did not do.
	AsOn string

	// Items are the rows: one per stock item that holds or has ever held stock.
	Items []GodownItemRow
	// Godowns are the column footers, in the order the columns should print:
	// named locations alphabetically, and the untracked column LAST (a column
	// with no header belongs at the end of the sheet, not at the front of it).
	Godowns []GodownTotal
	// TotalValue is the whole inventory, and must equal StockSummary.TotalValue
	// for the same date.
	TotalValue decimal.Decimal
}

// GodownStockSummary returns the per-location stock position as at `asOn`
// (empty means everything ever posted).
//
// NOTHING IS SUPPRESSED, exactly as in StockSummary: a location whose closing
// quantity is zero but which received and issued stock during its life keeps
// its column, because a zero cell under a non-zero outward total is the row a
// stock controller actually wants to look at.
func (s *Service) GodownStockSummary(ctx context.Context, companyID uuid.UUID, asOn string) (GodownStockSummary, error) {
	if err := scope(ctx, companyID); err != nil {
		return GodownStockSummary{}, err
	}

	// The echo is the RESOLVED boundary, and it is produced by the same function
	// stockPositionsAsOn resolves with — so the caption and the figures cannot
	// come from two different dates.
	out := GodownStockSummary{AsOn: resolvedStockAsOn(asOn), TotalValue: decimal.Zero}

	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, _, err := stockPositionsAsOn(ctx, tx, asOn)
		if err != nil {
			return err
		}
		out.Items, out.Godowns, out.TotalValue = foldByGodown(rows)
		return nil
	})
	if err != nil {
		return GodownStockSummary{}, err
	}
	return out, nil
}

// foldByGodown folds position rows into item rows, godown cells and column
// footers in ONE pass over the rows.
//
// EVERY ROW IS FOLDED. There is no filter here, and in particular no
// `if r.GodownID == uuid.Nil { continue }` — the untracked location holds real
// stock, it is counted in the item total the plain Stock Summary prints, and
// dropping it here would leave the columns quietly short of the row total they
// are supposed to explain.
func foldByGodown(rows []StockPositionRow) ([]GodownItemRow, []GodownTotal, decimal.Decimal) {
	var items []GodownItemRow
	itemIdx := map[uuid.UUID]int{}
	// colIdx locates a godown's cell WITHIN one item's columns.
	colIdx := map[uuid.UUID]map[uuid.UUID]int{}

	var totals []GodownTotal
	totalIdx := map[uuid.UUID]int{}
	grand := decimal.Zero

	for _, r := range rows {
		i, ok := itemIdx[r.StockItemID]
		if !ok {
			i = len(items)
			itemIdx[r.StockItemID] = i
			colIdx[r.StockItemID] = map[uuid.UUID]int{}
			items = append(items, GodownItemRow{
				StockItemID:  r.StockItemID,
				ItemName:     r.ItemName,
				UnitSymbol:   r.UnitSymbol,
				StockFigures: zeroFigures(),
			})
		}
		item := &items[i]
		item.add(r.Position)

		c, ok := colIdx[r.StockItemID][r.GodownID]
		if !ok {
			c = len(item.Columns)
			colIdx[r.StockItemID][r.GodownID] = c
			item.Columns = append(item.Columns, GodownColumn{
				GodownID:     r.GodownID,
				GodownName:   r.GodownName,
				StockFigures: zeroFigures(),
			})
		}
		col := &item.Columns[c]
		col.add(r.Position)
		col.Positions = append(col.Positions, r)

		g, ok := totalIdx[r.GodownID]
		if !ok {
			g = len(totals)
			totalIdx[r.GodownID] = g
			totals = append(totals, GodownTotal{
				GodownID:     r.GodownID,
				GodownName:   r.GodownName,
				OpeningValue: decimal.Zero,
				InwardValue:  decimal.Zero,
				OutwardValue: decimal.Zero,
				ClosingValue: decimal.Zero,
			})
		}
		t := &totals[g]
		t.OpeningValue = t.OpeningValue.Add(r.OpeningValue)
		t.InwardValue = t.InwardValue.Add(r.InwardValue)
		t.OutwardValue = t.OutwardValue.Add(r.OutwardValue)
		t.ClosingValue = t.ClosingValue.Add(r.ClosingValue)

		grand = grand.Add(r.ClosingValue)
	}

	for i := range items {
		sortGodownColumns(items[i].Columns)
	}
	sort.SliceStable(totals, func(a, b int) bool {
		return godownColumnBefore(totals[a].GodownID, totals[a].GodownName,
			totals[b].GodownID, totals[b].GodownName)
	})
	return items, totals, grand
}

func sortGodownColumns(cols []GodownColumn) {
	sort.SliceStable(cols, func(a, b int) bool {
		return godownColumnBefore(cols[a].GodownID, cols[a].GodownName,
			cols[b].GodownID, cols[b].GodownName)
	})
}

// godownColumnBefore is the column order: the UNTRACKED column last, then by
// name, then by id.
//
// Untracked sorts LAST rather than first (which is where a plain name compare
// would put an empty string) because it is the only column with no header, and
// a nameless first column reads as a rendering fault. The id tiebreak keeps the
// order TOTAL so two runs of the same report cannot differ.
func godownColumnBefore(aID uuid.UUID, aName string, bID uuid.UUID, bName string) bool {
	aUntracked, bUntracked := aID == uuid.Nil, bID == uuid.Nil
	if aUntracked != bUntracked {
		return bUntracked
	}
	if aName != bName {
		return aName < bName
	}
	return aID.String() < bID.String()
}

// --------------------------------------------------------------------------
// The drill: item -> the vouchers that moved it
// --------------------------------------------------------------------------

// StockVoucherLine is ONE inventory line of ONE voucher, as the item's voucher
// register shows it.
//
// Rate and Amount are THE LINE AS ENTERED — they are inputs to valuation, not a
// valuation. On an OUTWARD line Rate is what the goods were SOLD for, which is
// emphatically not what they cost: the cost consumed is a replayed figure that
// only exists at the position level (StockPositionRow.OutwardValue), because it
// depends on every movement that came before. A UI must never total an Amount
// column here and call the result cost of goods sold.
type StockVoucherLine struct {
	VoucherID        uuid.UUID
	VoucherVersionID uuid.UUID
	VersionNo        int
	VoucherTypeName  string
	VoucherNo        string
	Date             string // ISO date (YYYY-MM-DD)
	Party            string
	Narration        string

	GodownID   uuid.UUID
	GodownName string
	BatchID    uuid.UUID
	BatchName  string

	LineNo      int
	Direction   string // IN / OUT
	Qty         decimal.Decimal
	Rate        decimal.Decimal
	DiscountPct decimal.Decimal
	Amount      decimal.Decimal
}

// StockItemVouchers is the drill-down from a stock item to every voucher line
// that moved it inside [From, To], both bounds INCLUSIVE and either may be
// empty for unbounded.
//
// It is a PERIOD and takes TWO dates, for the same reason StockMovementReport
// does: a register lists EVENTS, and an event either happened in the window or
// it did not. It reports no opening and no closing — those are a position, and
// the position is what GodownStockSummary and StockSummary answer.
//
// InwardQty/OutwardQty are the totals of the listed lines and are quantities
// only. There is no value total, deliberately: see StockVoucherLine.
type StockItemVouchers struct {
	StockItemID uuid.UUID
	ItemName    string
	UnitSymbol  string
	From        string
	To          string

	Lines []StockVoucherLine

	InwardQty  decimal.Decimal
	OutwardQty decimal.Decimal
}

// stockItemVoucherSQL lists the movement lines of ONE item.
//
// THE voucher_head JOIN IS THE ENTIRE REASON THIS QUERY IS NOT A SELECT * .
// Movements are versioned children: an ALTER re-states them under a NEW
// voucher_version and the superseded rows stay in stock_movement forever, and a
// DELETE writes a tombstone version carrying no movement rows at all. Joining
// voucher_head on current_version_id (and requiring is_deleted = false) is what
// makes this drill agree with the summary that links to it. Drop it and a
// deleted voucher's lines reappear HERE while staying absent from the summary
// above — two screens disagreeing about whether a sale happened, which is the
// failure TestDrillFromItemToVouchersUsesTheCurrentVersionJoin forbids.
//
// Both date bounds are INCLUSIVE, matching every other period on this seam.
const stockItemVoucherSQL = `
	SELECT vv.voucher_id, vv.id, vv.version_no,
	       COALESCE(vt.name, ''), COALESCE(vv.voucher_no, ''),
	       to_char(vv.voucher_date, 'YYYY-MM-DD'),
	       COALESCE(vv.payload->>'party', ''),
	       COALESCE(vv.payload->>'narration', ''),
	       sm.godown_id, sm.batch_id, sm.line_no, sm.direction::text,
	       sm.qty, sm.rate, sm.discount_pct, sm.amount
	  FROM stock_movement sm
	  JOIN voucher_version vv ON vv.id = sm.voucher_version_id
	  JOIN voucher_head vh
	    ON vh.company_id = vv.company_id
	   AND vh.voucher_id = vv.voucher_id
	   AND vh.current_version_id = vv.id
	  LEFT JOIN voucher_type vt ON vt.id = vv.voucher_type_id
	 WHERE vh.is_deleted = false
	   AND sm.stock_item_id = $1
	   AND ($2::date IS NULL OR vv.voucher_date >= $2::date)
	   AND ($3::date IS NULL OR vv.voucher_date <= $3::date)
	 ORDER BY vv.voucher_date NULLS LAST, vv.created_at, sm.line_no`

// StockItemVouchers returns every current, non-deleted voucher line that moved
// `itemID` inside [from, to] (empty bounds mean unbounded on that side).
//
// The item must exist in this company's books; an unknown id is an ERROR rather
// than an empty register, because "this item never moved" and "you asked about
// an item that is not yours" are different answers and a drill that conflated
// them would let a caller probe another tenant's ids by the shape of the reply.
func (s *Service) StockItemVouchers(ctx context.Context, companyID, itemID uuid.UUID, from, to string) (StockItemVouchers, error) {
	if err := scope(ctx, companyID); err != nil {
		return StockItemVouchers{}, err
	}

	out := StockItemVouchers{
		StockItemID: itemID,
		From:        from,
		To:          to,
		InwardQty:   decimal.Zero,
		OutwardQty:  decimal.Zero,
	}

	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		names, err := loadStockNames(ctx, tx)
		if err != nil {
			return err
		}
		meta, ok := names.items[itemID]
		if !ok {
			// RLS is what makes this a tenant statement and not just a
			// existence check: another company's item is simply not visible.
			return fmt.Errorf("reporting: no stock item %s in these books", itemID)
		}
		out.ItemName = meta.Name
		out.UnitSymbol = meta.UnitSymbol

		rows, err := tx.Query(ctx, stockItemVoucherSQL, itemID, nullDate(from), nullDate(to))
		if err != nil {
			return fmt.Errorf("reporting: query stock item vouchers: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var (
				l        StockVoucherLine
				date     *string
				godownID *uuid.UUID
				batchID  *uuid.UUID
			)
			if err := rows.Scan(&l.VoucherID, &l.VoucherVersionID, &l.VersionNo,
				&l.VoucherTypeName, &l.VoucherNo, &date, &l.Party, &l.Narration,
				&godownID, &batchID, &l.LineNo, &l.Direction,
				&l.Qty, &l.Rate, &l.DiscountPct, &l.Amount); err != nil {
				return fmt.Errorf("reporting: scan stock item voucher line: %w", err)
			}
			if date != nil {
				l.Date = *date
			}
			if godownID != nil {
				l.GodownID = *godownID
			}
			if batchID != nil {
				l.BatchID = *batchID
			}
			l.GodownName = names.godowns[l.GodownID]
			l.BatchName = names.batches[l.BatchID]

			if inventory.Direction(l.Direction) == inventory.DirectionOut {
				out.OutwardQty = out.OutwardQty.Add(l.Qty)
			} else {
				out.InwardQty = out.InwardQty.Add(l.Qty)
			}
			out.Lines = append(out.Lines, l)
		}
		return rows.Err()
	})
	if err != nil {
		return StockItemVouchers{}, err
	}
	return out, nil
}
