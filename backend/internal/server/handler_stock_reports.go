package server

// handler_stock_reports.go — the four inventory reports that existed in
// internal/reporting but were unreachable over HTTP:
//
//	GET .../stock-movement       what MOVED in a period (a register)
//	GET .../godown-summary       the position broken out BY LOCATION
//	GET .../batch-summary        the position BY BATCH, banded by expiry
//	GET .../stock-item-vouchers  the drill from one item to the vouchers that moved it
//
// THE SAME ONE THING AS handler_stock_summary.go: nothing here is stored.
// Quantity is a signed sum over the movement ledger and VALUE is derived by
// replaying that ledger through internal/inventory's pure valuer, so every one
// of these is a fresh replay rather than a read of a balances table. That is
// what makes a per-godown or per-batch cell trustworthy: each location and each
// batch carries its OWN running average cost, and a SUM(qty * rate) grouped by
// location would value one warehouse's stock at a blend of what another paid.
//
// WHY A HANDLER SHIPPED WITHOUT ITS MAPPING IS A 403, NOT A 404. routeAction()
// default-denies any unmapped path under {companyID}, so mounting a report is a
// THREE-touch change and all three are load-bearing:
//
//  1. routes() — the chi route;
//  2. reportName() in auth_middleware.go — the (Display, report:NAME) mapping;
//  3. authz.KnownReportNames — so an administrator can write a rule naming it.
//
// Miss (2) and even an Owner gets "unmapped route: access denied"; miss (3) and
// the rule an administrator writes to govern it is rejected at write time.
// TestEveryCompanyRouteIsMappedAndInVocabulary and
// TestReportVocabularyMatchesRouter pin both directions.
//
// AUTHORIZATION. All four are a Display, so none of them inherits the
// back-dated write cutoff or the WebAuthn step-up (both gate on
// Action.IsWrite()). All four print COST, so migration 0028 seeds
// `disallow display report:NAME` on the seeded Data Entry level for each —
// see that migration's header for the per-report reasoning.
//
// Every quantity and every money value goes out as a fixed-4 decimal STRING.
// A JSON number is a float64, and the closing value of a warehouse is not a
// thing to send through a float.

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/inventory"
	"github.com/khata/cored/internal/reporting"
)

// --------------------------------------------------------------------------
// GET /stock-movement — a PERIOD
// --------------------------------------------------------------------------

// handleStockMovement returns the movement register for [from, to], both bounds
// INCLUSIVE, either may be empty for unbounded.
//
// It takes TWO dates where /stock-summary takes one, and the difference is not
// cosmetic: a period reports what MOVED and a position reports what IS. There
// is deliberately no opening and no closing column here — asking this endpoint
// for "what do we own" would give an answer that ignores every unit bought
// before `from`.
//
// The outward column is COST CONSUMED, priced off the running weighted average
// including everything that happened before the window opened. It is not what
// the goods sold for.
func (s *Server) handleStockMovement(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	from, ok := reportDate(w, r, "from")
	if !ok {
		return
	}
	to, ok := reportDate(w, r, "to")
	if !ok {
		return
	}

	rep, err := s.report.StockMovementReport(ctx, companyID, from, to)
	if err != nil {
		writeStockReportError(w, err)
		return
	}

	items := make([]map[string]any, 0, len(rep.Items))
	for _, it := range rep.Items {
		items = append(items, map[string]any{
			"stock_item_id": it.StockItemID.String(),
			"item":          it.ItemName,
			"unit":          it.UnitSymbol,
			"inward_qty":    it.InwardQty.StringFixed(4),
			"inward_value":  it.InwardValue.StringFixed(4),
			"outward_qty":   it.OutwardQty.StringFixed(4),
			"outward_value": it.OutwardValue.StringFixed(4),
			"net_qty":       it.NetQty.StringFixed(4),
			"net_value":     it.NetValue.StringFixed(4),
			"positions":     stockMovementRowsJSON(it.Positions),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		// Echoed so a client cannot render a period it did not get.
		"from":  rep.From,
		"to":    rep.To,
		"items": items,
		"rows":  stockMovementRowsJSON(rep.Rows),
	})
}

// stockMovementRowsJSON renders the (item, godown, batch) movement rows.
//
// There is no closing column and no rate. A movement row spans a window, and a
// rate on it would have to be an average of the window's own inwards, which is
// neither the cost the stock is carried at nor a price anything was traded at.
func stockMovementRowsJSON(rows []reporting.StockMovementRow) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"stock_item_id": row.StockItemID.String(),
			"item":          row.ItemName,
			"unit":          row.UnitSymbol,
			// An untracked dimension is "" here, never "-": the placeholder is a
			// PRESENTATION choice belonging to whoever draws the table.
			"godown_id": uuidOrEmpty(row.GodownID),
			"godown":    row.GodownName,
			"batch_id":  uuidOrEmpty(row.BatchID),
			"batch":     row.BatchName,

			"inward_qty":   row.InwardQty.StringFixed(4),
			"inward_value": row.InwardValue.StringFixed(4),
			// COST CONSUMED (cost of goods sold for the window), never revenue.
			"outward_qty":   row.OutwardQty.StringFixed(4),
			"outward_value": row.OutwardValue.StringFixed(4),
			// NetValue is the change in the position's CARRIED value across the
			// window, which is what reconciles two Stock Summaries taken at the
			// two ends of it. It is not a profit.
			"net_qty":   row.NetQty.StringFixed(4),
			"net_value": row.NetValue.StringFixed(4),
		})
	}
	return out
}

// --------------------------------------------------------------------------
// GET /godown-summary — a POSITION, by location
// --------------------------------------------------------------------------

// handleGodownSummary returns the stock position as at one date, broken out by
// location: item-major rows with one cell per godown, plus the column footers.
//
// Query param: as_on (empty = everything ever posted), exactly as
// /stock-summary. No `from`, for the same reason: a position has no lower bound
// and applying one would drop the stock the company already owned while still
// rendering a tidy table.
func (s *Server) handleGodownSummary(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	asOn, ok := reportDate(w, r, "as_on")
	if !ok {
		return
	}

	sum, err := s.report.GodownStockSummary(ctx, companyID, asOn)
	if err != nil {
		writeStockReportError(w, err)
		return
	}

	items := make([]map[string]any, 0, len(sum.Items))
	for _, it := range sum.Items {
		cols := make([]map[string]any, 0, len(it.Columns))
		for _, c := range it.Columns {
			cell := stockFiguresJSON(c.StockFigures)
			cell["godown_id"] = uuidOrEmpty(c.GodownID)
			cell["godown"] = c.GodownName
			// The UNTRACKED column — stock posted on a line that named no
			// location — is a real column holding real stock, and is flagged
			// rather than filtered so a UI can label it ("-") without having to
			// know that uuid.Nil is how "nowhere in particular" is spelled.
			// Dropping it would leave the columns quietly short of the item
			// total they are supposed to explain.
			cell["untracked"] = c.Untracked()
			// The batch-grain positions inside this cell, which is the only
			// level at which a closing RATE is meaningful.
			cell["positions"] = stockRowsJSON(c.Positions)
			cols = append(cols, cell)
		}
		row := stockFiguresJSON(it.StockFigures)
		row["stock_item_id"] = it.StockItemID.String()
		row["item"] = it.ItemName
		row["unit"] = it.UnitSymbol
		// DELIBERATELY NO closing_rate on a fold: a row spans locations, each
		// carrying its own weighted average, and the average of the averages is
		// not the average.
		row["columns"] = cols
		items = append(items, row)
	}

	godowns := make([]map[string]any, 0, len(sum.Godowns))
	for _, g := range sum.Godowns {
		godowns = append(godowns, map[string]any{
			"godown_id": uuidOrEmpty(g.GodownID),
			"godown":    g.GodownName,
			"untracked": g.Untracked(),
			// VALUE ONLY. There is no quantity in a column footer and there must
			// never be: adding kilograms of one item to litres of another
			// produces a number with no unit.
			"opening_value": g.OpeningValue.StringFixed(4),
			"inward_value":  g.InwardValue.StringFixed(4),
			"outward_value": g.OutwardValue.StringFixed(4),
			"closing_value": g.ClosingValue.StringFixed(4),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"as_on":   sum.AsOn,
		"items":   items,
		"godowns": godowns,
		// Must equal /stock-summary's total_value for the same date. Two screens
		// that disagree about how much stock a company owns is worse than one
		// screen that is wrong, because nobody can tell which to believe.
		"total_value": sum.TotalValue.StringFixed(4),
	})
}

// stockFiguresJSON renders the Opening/Inward/Outward/Closing quartet a fold
// carries, in the same field names and the same fixed-4 strings the position
// rows use, so a client folds nothing itself and the two cannot disagree.
func stockFiguresJSON(f reporting.StockFigures) map[string]any {
	return map[string]any{
		"opening_qty":   f.OpeningQty.StringFixed(4),
		"opening_value": f.OpeningValue.StringFixed(4),
		"inward_qty":    f.InwardQty.StringFixed(4),
		"inward_value":  f.InwardValue.StringFixed(4),
		"outward_qty":   f.OutwardQty.StringFixed(4),
		"outward_value": f.OutwardValue.StringFixed(4),
		"closing_qty":   f.ClosingQty.StringFixed(4),
		"closing_value": f.ClosingValue.StringFixed(4),
	}
}

// --------------------------------------------------------------------------
// GET /batch-summary — a POSITION, by batch, banded by expiry
// --------------------------------------------------------------------------

// handleBatchSummary returns the batch-wise position as at as_on, with every
// position classified into exactly one expiry band.
//
// as_on is REQUIRED here where it is optional everywhere else, and a missing
// one is a 400 rather than a default to today: a band is defined RELATIVE to a
// date, and defaulting to the clock would make the report non-reproducible (the
// same books printed on two days give different bands with no column saying
// why).
//
// EXPIRED STOCK IS FLAGGED, NEVER EXCLUDED. It is still in the warehouse and
// still on the Balance Sheet at cost until somebody writes it off with a
// voucher, so it is inside every total on this response and additionally
// reported as expired_value. A response that netted it off would understate the
// assets of the business.
func (s *Server) handleBatchSummary(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	asOn, ok := reportDate(w, r, "as_on")
	if !ok {
		return
	}

	sum, err := s.report.BatchStockSummary(ctx, companyID, asOn)
	if err != nil {
		writeStockReportError(w, err)
		return
	}

	items := make([]map[string]any, 0, len(sum.Items))
	for _, it := range sum.Items {
		row := stockFiguresJSON(it.StockFigures)
		row["stock_item_id"] = it.StockItemID.String()
		row["item"] = it.ItemName
		row["unit"] = it.UnitSymbol
		// A SUBSET of the closing figures above, never a deduction from them.
		row["expired_qty"] = it.ExpiredQty.StringFixed(4)
		row["expired_value"] = it.ExpiredValue.StringFixed(4)
		row["rows"] = batchRowsJSON(it.Rows)
		items = append(items, row)
	}

	bands := make([]map[string]any, 0, len(sum.Bands))
	for _, b := range sum.Bands {
		bands = append(bands, map[string]any{
			"bucket": string(b.Bucket),
			"label":  b.Label,
			// VALUE ONLY: a band spans items, so a quantity here would be adding
			// kilograms to litres. row_count is the only count that means
			// anything across items.
			"closing_value": b.ClosingValue.StringFixed(4),
			"row_count":     b.RowCount,
			// The band's own rows are NOT repeated here. Every row in `rows`
			// below carries its `bucket`, so a client filters rather than
			// receiving the whole position list three times (flat, by item, and
			// by band) in one response.
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"as_on": sum.AsOn,
		"rows":  batchRowsJSON(sum.Rows),
		"items": items,
		// ALL FIVE bands, in nearest-deadline-first order, empty ones included:
		// a band that vanished when empty would make "no expired stock"
		// indistinguishable from "the expired band was dropped somewhere".
		"bands":       bands,
		"total_value": sum.TotalValue.StringFixed(4),
		// The headline: money sitting in stock that can no longer be sold. It is
		// PART of total_value, not netted off it.
		"expired_value": sum.ExpiredValue.StringFixed(4),
	})
}

// batchRowsJSON renders the (item, godown, batch) rows with their dates and
// band. Unlike a fold, a batch row IS a single position, so it carries a
// closing_rate.
func batchRowsJSON(rows []reporting.BatchStockRow) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"stock_item_id": row.StockItemID.String(),
			"item":          row.ItemName,
			"unit":          row.UnitSymbol,
			"godown_id":     uuidOrEmpty(row.GodownID),
			"godown":        row.GodownName,
			"batch_id":      uuidOrEmpty(row.BatchID),
			"batch":         row.BatchName,

			// "" when the batch carries no date, and always "" for a position
			// with no batch at all. Most stock simply does not perish.
			"mfg_date":    row.MfgDate,
			"expiry_date": row.ExpiryDate,
			// SIGNED: negative once the date has passed. Meaningful only when
			// expiry_date is non-empty.
			"days_to_expiry": row.DaysToExpiry,
			"expired":        row.Expired,
			"bucket":         string(row.Bucket),

			"opening_qty":   row.OpeningQty.StringFixed(4),
			"opening_value": row.OpeningValue.StringFixed(4),
			"inward_qty":    row.InwardQty.StringFixed(4),
			"inward_value":  row.InwardValue.StringFixed(4),
			"outward_qty":   row.OutwardQty.StringFixed(4),
			"outward_value": row.OutwardValue.StringFixed(4),
			"closing_qty":   row.ClosingQty.StringFixed(4),
			"closing_value": row.ClosingValue.StringFixed(4),
			// PRESENTATION ONLY, 4 dp. Never multiply it back out by the closing
			// quantity to re-derive the value: the running average carries 16 dp.
			"closing_rate": row.ClosingRate.StringFixed(4),
		})
	}
	return out
}

// --------------------------------------------------------------------------
// GET /stock-item-vouchers — the drill, item -> the vouchers that moved it
// --------------------------------------------------------------------------

// handleStockItemVouchers lists every CURRENT, non-deleted voucher line that
// moved one item inside [from, to], both bounds inclusive.
//
// RESEARCH sec 8: no report is a dead end. This is what a cell on the Stock
// Summary drills into, and it repeats the summary's current-version join, so a
// superseded or deleted voucher cannot appear here while being absent from the
// total above it.
//
// stock_item_id is REQUIRED and arrives as a query param rather than as a path
// segment. See the comment on reportName(): the route path has to spell the
// report name exactly, or the vocabulary closure test fails and an ACL rule
// naming this report could never match.
func (s *Server) handleStockItemVouchers(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	raw := strings.TrimSpace(r.URL.Query().Get("stock_item_id"))
	if raw == "" {
		// A drill with no subject is a MALFORMED request, not an empty register.
		// Answering it with every movement in the company would be a plausible,
		// tidy, entirely different report than the one the user asked for.
		writeError(w, http.StatusBadRequest, "missing stock_item_id")
		return
	}
	itemID, err := uuid.Parse(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid stock_item_id (want a uuid)")
		return
	}
	from, ok := reportDate(w, r, "from")
	if !ok {
		return
	}
	to, ok := reportDate(w, r, "to")
	if !ok {
		return
	}

	reg, err := s.report.StockItemVouchers(ctx, companyID, itemID, from, to)
	if err != nil {
		writeStockReportError(w, err)
		return
	}

	lines := make([]map[string]any, 0, len(reg.Lines))
	for _, l := range reg.Lines {
		lines = append(lines, map[string]any{
			"voucher_id": l.VoucherID.String(),
			// The VERSION the line belongs to, so a drill can be re-fetched
			// against the same reading of the books it was printed from.
			"voucher_version_id": l.VoucherVersionID.String(),
			"version_no":         l.VersionNo,
			"voucher_type":       l.VoucherTypeName,
			"voucher_no":         l.VoucherNo,
			"date":               l.Date,
			"party":              l.Party,
			"narration":          l.Narration,

			"godown_id": uuidOrEmpty(l.GodownID),
			"godown":    l.GodownName,
			"batch_id":  uuidOrEmpty(l.BatchID),
			"batch":     l.BatchName,

			"line_no":   l.LineNo,
			"direction": l.Direction,
			"qty":       l.Qty.StringFixed(4),
			// RATE AND AMOUNT ARE THE LINE AS ENTERED — inputs to valuation, not
			// a valuation. On an OUT line the rate is what the goods were SOLD
			// for and is emphatically not what they cost; on an IN line it is
			// the purchase price, which is the cost book stated outright (and
			// the reason migration 0028 keeps this report away from Data Entry).
			// A UI must never total an amount column here and call the result
			// cost of goods sold — that figure only exists at the position
			// level, on /stock-summary's outward_value.
			"rate":         l.Rate.StringFixed(4),
			"discount_pct": l.DiscountPct.StringFixed(4),
			"amount":       l.Amount.StringFixed(4),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"stock_item_id": reg.StockItemID.String(),
		"item":          reg.ItemName,
		"unit":          reg.UnitSymbol,
		"from":          reg.From,
		"to":            reg.To,
		"lines":         lines,
		// QUANTITY TOTALS ONLY, and deliberately no value total: see the note on
		// rate above.
		"inward_qty":  reg.InwardQty.StringFixed(4),
		"outward_qty": reg.OutwardQty.StringFixed(4),
	})
}

// --------------------------------------------------------------------------
// Errors
// --------------------------------------------------------------------------

// writeStockReportError maps a reporting error to a status, on the same terms
// handleStockSummary already uses for the Stock Summary.
//
//   - ErrAgeingNeedsAsOn is the CALLER omitting the one parameter an ageing
//     report cannot be computed without: a 400, naming it, rather than a report
//     silently relative to the server's clock.
//   - ErrUnknownValuation is an OPERATOR fault, not a user fault: it means
//     stock_item.valuation_method's CHECK was widened without the matching
//     valuer arm shipping. Reporting it as a 4xx would tell an accountant to fix
//     their data when it is the server that is mis-built.
//   - everything else is a 422, which includes an unknown stock_item_id on the
//     drill. Note the cost: a DB failure inside the replay also lands here. That
//     is the behaviour handleStockSummary already has, and narrowing it needs a
//     typed error in internal/reporting rather than a string match here.
func writeStockReportError(w http.ResponseWriter, err error) {
	if errors.Is(err, reporting.ErrAgeingNeedsAsOn) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if errors.Is(err, inventory.ErrUnknownValuation) {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeError(w, http.StatusUnprocessableEntity, err.Error())
}
