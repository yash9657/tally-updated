package server

// handler_stock_summary.go — GET /v1/companies/{companyID}/stock-summary.
//
// THE ONE THING TO KNOW ABOUT THIS ENDPOINT: nothing it returns is stored.
// Closing quantity is a signed sum over the movement ledger; closing VALUE is
// derived by replaying that ledger through internal/inventory's pure valuer,
// because valuation is order-dependent (a back-dated purchase re-prices every
// issue that already happened after it, and altering an April invoice changes
// the July closing value). There is deliberately no column anywhere holding a
// stock value for this handler to read, which is why `as_on` is a REPLAY
// boundary rather than a filter on a balances table.
//
// It is a Display over report:stock-summary (see reportName in
// auth_middleware.go), so it does not inherit the back-dated write cutoff or
// the WebAuthn step-up — an Owner who has not stepped up reads the stock
// position exactly as they read the Trial Balance.
//
// Every quantity and every money value goes out as a fixed-4 decimal STRING,
// like every other number on this surface. A JSON number is a float64, and the
// closing value of a warehouse is not a thing to send through a float.

import (
	"errors"
	"net/http"

	"github.com/khata/cored/internal/inventory"
	"github.com/khata/cored/internal/reporting"
)

// handleStockSummary returns the position of every stock item as at one date.
//
// Query param: as_on (YYYY-MM-DD; empty = unbounded, i.e. everything posted).
// There is deliberately NO `from`: a position has no lower bound, and applying
// one would drop the stock the company already owned while still rendering a
// tidy-looking table — the worst kind of wrong answer. The period question
// ("what MOVED between two dates") is a different report with a different
// arity.
func (s *Server) handleStockSummary(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	asOn, ok := reportDate(w, r, "as_on")
	if !ok {
		return
	}

	summary, err := s.report.StockSummary(ctx, companyID, asOn)
	if err != nil {
		// An item whose valuation_method has no valuer is an OPERATOR fault, not
		// a user fault: it means stock_item.valuation_method's CHECK was widened
		// (to FIFO, say) without the matching valuer arm shipping. Reporting that
		// as a 422 would tell an accountant to fix their data; it is the server
		// that is mis-built, so it is a 500.
		if errors.Is(err, inventory.ErrUnknownValuation) {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stockSummaryJSON(summary))
}

// stockSummaryJSON renders a StockSummary to the wire.
//
// BOTH the item tree and the flat row list are emitted, and they are the SAME
// StockPositionRow values — the reporting layer folds one from the other, so a
// UI can render either an item-wise summary or a location-wise grid without a
// second round trip and without the two disagreeing.
// baseQtyOrEmpty renders the compound-unit restatement, or "" when the item's
// unit is simple. ⛔ NOT "0.0000": a client cannot tell a zero conversion from an
// absent one, and one of the two is a lie about the stock.
func baseQtyOrEmpty(it reporting.StockItemRollup) string {
	if it.BaseUnitSymbol == "" || !it.ConversionFactor.IsPositive() {
		return ""
	}
	return it.ClosingBaseQty.StringFixed(4)
}

func stockSummaryJSON(sum reporting.StockSummary) map[string]any {
	items := make([]map[string]any, 0, len(sum.Items))
	for _, it := range sum.Items {
		items = append(items, map[string]any{
			"stock_item_id": it.StockItemID.String(),
			"item":          it.ItemName,
			"unit":          it.UnitSymbol,
			// THE BASIS, on the ITEM, because that is what stock_item carries and
			// what a client's "change the costing method" control has to address.
			// Without it a screen showing three items on three different bases can
			// only print values that cannot be compared.
			"valuation_method": it.ValuationMethod,
			// T10 — the item's GROUP, and the closing quantity RESTATED in the
			// unit's base unit when the unit is compound (5 BOX -> 60 NOS).
			//
			// ⚠️ base_qty AND base_unit ARE EMPTY FOR A SIMPLE UNIT, and a client
			// must render nothing rather than "0.0000": a simple unit has no base
			// to restate into, and a zero would read as "this stock is worth no
			// pieces". The STORED quantity is closing_qty; this is derived at read
			// time, exactly as value is.
			"stock_group":   it.StockGroupName,
			"base_unit":     it.BaseUnitSymbol,
			"base_qty":      baseQtyOrEmpty(it),
			"opening_qty":   it.OpeningQty.StringFixed(4),
			"opening_value": it.OpeningValue.StringFixed(4),
			"inward_qty":    it.InwardQty.StringFixed(4),
			"inward_value":  it.InwardValue.StringFixed(4),
			"outward_qty":   it.OutwardQty.StringFixed(4),
			"outward_value": it.OutwardValue.StringFixed(4),
			"closing_qty":   it.ClosingQty.StringFixed(4),
			"closing_value": it.ClosingValue.StringFixed(4),
			// DELIBERATELY NO closing_rate on an item total: two godowns of the
			// same item carry two different average costs, and the average of
			// the averages is not the average. Ask a position for its rate.
			"positions": stockRowsJSON(it.Positions),
		})
	}
	// T10 — THE GROUP SUBTOTALS, in tree order with the ungrouped bucket last.
	//
	// ⛔ THEY DO NOT SUM TO total_value AND THE KEY SAYS SO IN ITS OWN COMMENT:
	// every item counts in EVERY ANCESTOR of its group, which is what makes a
	// branch total worth reading and what makes adding the rows up wrong. The
	// company's closing stock is the sum of the POSITIONS.
	groups := make([]map[string]any, 0, len(sum.Groups))
	for _, g := range sum.Groups {
		groups = append(groups, map[string]any{
			// "" is the UNGROUPED bucket — a real bucket holding every item that
			// predates T10, which a client must LABEL rather than skip.
			"group":         g.GroupName,
			"depth":         g.Depth,
			"item_count":    g.ItemCount,
			"opening_value": g.OpeningValue.StringFixed(4),
			"inward_value":  g.InwardValue.StringFixed(4),
			"outward_value": g.OutwardValue.StringFixed(4),
			"closing_value": g.ClosingValue.StringFixed(4),
		})
	}
	return map[string]any{
		"as_on":  sum.AsOn,
		"groups": groups,
		"items":  items,
		"rows":   stockRowsJSON(sum.Rows),
		// The closing value of the whole inventory as at as_on: what a
		// Stock-in-Hand line on the Balance Sheet is worth on that date.
		"total_value": sum.TotalValue.StringFixed(4),
	}
}

// stockRowsJSON renders the (item, godown, batch) position rows.
func stockRowsJSON(rows []reporting.StockPositionRow) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"stock_item_id": row.StockItemID.String(),
			"item":          row.ItemName,
			"unit":          row.UnitSymbol,
			// An untracked dimension is uuid.Nil with an empty name. It renders
			// as "" here rather than as "-": the placeholder is a PRESENTATION
			// choice and belongs to whoever draws the table, and a client that
			// re-sends this row must send "" (meaning NULL), never "-".
			"godown_id": uuidOrEmpty(row.GodownID),
			"godown":    row.GodownName,
			// The item's group on the FLAT row as well as on the rollup, so a
			// caller that reads `rows` (the CSV export does) can group without a
			// second lookup.
			"stock_group": row.StockGroupName,
			"batch_id":    uuidOrEmpty(row.BatchID),
			"batch":       row.BatchName,
			// The basis the figures were derived on, so a printed report can
			// state its own valuation basis.
			"valuation_method": row.ValuationMethod,

			"opening_qty":   row.OpeningQty.StringFixed(4),
			"opening_value": row.OpeningValue.StringFixed(4),
			"inward_qty":    row.InwardQty.StringFixed(4),
			"inward_value":  row.InwardValue.StringFixed(4),
			// COST CONSUMED (cost of goods sold), never what the goods sold
			// for. Booking the sale price as the cost is the single most common
			// inventory bug and it makes gross profit come out as zero.
			"outward_qty":   row.OutwardQty.StringFixed(4),
			"outward_value": row.OutwardValue.StringFixed(4),
			"closing_qty":   row.ClosingQty.StringFixed(4),
			"closing_value": row.ClosingValue.StringFixed(4),
			// PRESENTATION ONLY, 4 dp. Never multiply it back out by the closing
			// quantity to re-derive the value: the running average carries 16 dp
			// and the rounded rate does not reproduce it.
			"closing_rate": row.ClosingRate.StringFixed(4),
		})
	}
	return out
}
