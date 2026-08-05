package server

// handler_orderbook.go — the two order books over HTTP (T3, migration 0042).
//
//	GET .../sales-order-book?as_on=    -> Display / report:sales-order-book
//	GET .../purchase-order-book?as_on= -> Display / report:purchase-order-book
//
// ===========================================================================
// ⛔⛔ TWO ROUTES AND TWO NAMES, NOT ONE ROUTE WITH ?direction=
// ===========================================================================
// This is the decision most likely to be "simplified" by a later author, so the
// reason is stated at the top: internal/authz decides on the report NAME, and A
// QUERY PARAMETER IS INVISIBLE TO THE PDP. One `report:order-book` serving both
// directions would make
//
//	"the salesperson may see our sales pipeline but not what we have committed to
//	 pay suppliers"
//
// an UNWRITABLE RULE — an ordinary rule an ordinary business wants, because the
// two lists belong to two different jobs. Migration 0073 writes the refusals per
// name and the cost reports reached the same conclusion from the same place.
//
// It also satisfies the rule that a report's route path must be a FLAT LITERAL
// SPELLING ITS OWN NAME: TestReportVocabularyMatchesRouter asks routeAction for
// GET /v1/companies/{id}/<name> for every name in authz.KnownReportNames, so a
// report reached by /orders/book?direction=sales would be a vocabulary entry no
// route serves — a `disallow` an administrator can write that can never match,
// which is the silent-permit failure the closed vocabulary exists to prevent.
//
// ===========================================================================
// as_on IS OPTIONAL AND ITS ABSENCE MEANS "EVERYTHING"
// ===========================================================================
// An outstanding balance is a POSITION, so this report takes a single date rather
// than the from/to period every other statement in internal/reporting takes — see
// reporting/orderbook.go's header for why a period cannot express it. Omitting it
// is legal and gives the position from every order ever placed; the report SAYS SO
// in its notes rather than leaving a reader to assume a default they were not
// given.
//
// ===========================================================================
// THERE IS NO PRINT LAYOUT, SO THE PRINT ROUTE ANSWERS 501
// ===========================================================================
// Adding a name to authz.KnownReportNames AUTO-MOUNTS GET /print/reports/<name>,
// and both names are in it because an administrator must be able to write
// `disallow print report:sales-order-book`. Neither has a specified printed
// layout, so handlePrintReport's default arm answers 501 with the report named —
// exactly as tds-register and the two cost summaries do. Nothing needs adding to
// handler_print_reports.go beyond its header's deferred list: the default arm
// already covers every name without a builder.
//
// MONEY AND QUANTITIES GO OUT AS FIXED-4 STRINGS. A JSON number is a float64 and
// an outstanding commitment is not a thing to send through one.

import (
	"net/http"

	"github.com/khata/cored/internal/reporting"
)

// handleOrderBook returns a handler bound to a fixed direction. The same body
// serves both reports; the ROUTE decides the direction, never a parameter — see
// the header.
func (s *Server) handleOrderBook(direction reporting.OrderDirection) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, companyID, ok := s.scopeCtxID(w, r)
		if !ok {
			return
		}
		// An unparseable date is a 400 rather than a silent fall back to "no
		// bound": falling back would answer with the position from all time under
		// a heading the operator believes says "as at 31 July", which is a real
		// report of the wrong thing.
		asOn, ok := reportDate(w, r, "as_on")
		if !ok {
			return
		}

		book, err := s.report.OrderBookFor(ctx, companyID, direction, asOn)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}

		rows := make([]map[string]any, 0, len(book.Rows))
		for _, row := range book.Rows {
			rows = append(rows, map[string]any{
				"order_id":      row.OrderID.String(),
				"order_no":      row.OrderNo,
				"order_date":    row.OrderDate,
				"party":         row.Party,
				"order_line_id": row.OrderLineID.String(),
				"line_no":       row.LineNo,
				"stock_item":    row.StockItem,
				// Committed, delivered and outstanding are ALL THREE served, and
				// none is dropped as derivable. An operator's question is not "how
				// much is left" alone but "how much of what we promised" — a line
				// showing 5 outstanding reads completely differently against a
				// commitment of 6 than against one of 500.
				"qty":               row.Qty.StringFixed(4),
				"fulfilled_qty":     row.Fulfilled.StringFixed(4),
				"outstanding_qty":   row.Outstanding().StringFixed(4),
				"rate":              row.Rate.StringFixed(4),
				"outstanding_value": row.OutstandingValue().StringFixed(4),
				// "" when no date was agreed. NOT null and not a zero date: a
				// client that received 0001-01-01 would render it, and a client
				// that received null would have to decide what that means.
				"due_date": row.DueDate,
				// Computed server-side against the same as_on the rest of the
				// report used, so a client cannot reach a different answer by
				// comparing against its own clock in another timezone.
				"is_overdue": row.Overdue(book.AsOn),
			})
		}

		writeJSON(w, http.StatusOK, map[string]any{
			// Echoed so a screen cannot render the purchase pipeline under a
			// "Sales Orders" heading. Not decoration: the two are opposite
			// obligations and confusing them inverts what the business is owed.
			"direction": string(book.Direction),
			"as_on":     book.AsOn,
			"rows":      rows,
			// Meaningful BECAUSE this report is one direction. A total across both
			// would net a receivable against a payable into a number describing
			// nothing — the same reason there are two reports at all.
			"total_outstanding_value": book.TotalOutstandingValue.StringFixed(4),
			// The notes carry onto the artefact what a reader would otherwise have
			// to know from the source: that an order is not a movement, what the
			// as-at bound covered, and how many lines carry no agreed due date and
			// are therefore not overdue however old.
			"notes": book.Notes,
		})
	}
}
