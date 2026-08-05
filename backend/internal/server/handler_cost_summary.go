package server

// handler_cost_summary.go — the two cost reports over HTTP (T2, migration 0040).
//
//	GET .../cost-centre-summary?cost_category_id=&from=&to=
//	                                    -> Display / report:cost-centre-summary
//	GET .../cost-category-summary?from=&to=
//	                                    -> Display / report:cost-category-summary
//
// ===========================================================================
// WHY THE AXIS IS A QUERY PARAMETER AND NOT A PATH SEGMENT
// ===========================================================================
// A report's route path must be a FLAT LITERAL SPELLING ITS OWN NAME.
// TestReportVocabularyMatchesRouter asks routeAction for
// GET /v1/companies/{id}/<name> for every name in authz.KnownReportNames, so a
// report reached by /cost-categories/{id}/summary would be a vocabulary entry no
// route serves — a `disallow` an administrator can write that can never match,
// which is the silent-permit failure internal/authz/vocabulary.go exists to
// prevent. So the selector rides in the query string, exactly as
// stock-item-vouchers takes stock_item_id.
//
// ===========================================================================
// WHY A MISSING cost_category_id IS A 400 AND NOT A DEFAULT
// ===========================================================================
// ⛔ A COST CATEGORY IS AN AXIS AND THE SAME MONEY IS ALLOCATED IN FULL ON EVERY
// AXIS. Rs 1,00,000 of salary allocated across Departments and again across
// Branches is Rs 1,00,000 of cost described twice. So the two silent answers are
// both wrong in ways that look right:
//
//   - picking one axis renders a real, correctly-footed report for an axis the
//     operator did not choose, with nothing on it saying so;
//   - summing all of them reports a multiple of the cost actually borne.
//
// The refusal therefore NAMES THE AXES THIS COMPANY HAS, so the operator can act
// on it. reporting.CostAxisError builds that sentence (it has the list; this
// seam does not), and this handler's only job is to choose the status code:
// 400 when no axis was named at all (a malformed request) and 422 when one was
// named that these books do not contain (a request about something absent). The
// bank-reconciliation print route made the same call for the same reason — see
// handler_print_reports.go's header.
//
// ===========================================================================
// THERE IS NO PRINT LAYOUT FOR EITHER, SO THE PRINT ROUTE ANSWERS 501
// ===========================================================================
// Adding a name to authz.KnownReportNames AUTO-MOUNTS
// GET /print/reports/<name>, and both names are in it because an administrator
// must be able to write `disallow print report:cost-centre-summary`. Neither has
// a specified printed layout, so handlePrintReport's default arm answers 501 with
// the report named — exactly as bank-reconciliation once did and as tds-register
// still does. A blank page that looks like a filed cost statement is worse than a
// refusal, and a name the router does not emit is a rule that can never match.
// Nothing needs adding to handler_print_reports.go for that: its default arm
// already covers every name without a builder.
//
// MONEY GOES OUT AS FIXED-4 STRINGS. A JSON number is a float64 and a
// department's cost is not a thing to send through one.

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/reporting"
)

// --------------------------------------------------------------------------
// GET /cost-centre-summary — ONE axis, rolled up
// --------------------------------------------------------------------------

func (s *Server) handleCostCentreSummary(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	// A PERIOD, both bounds inclusive, either optional — the convention
	// period_test.go pins for every statement in internal/reporting. An
	// unparseable date is a 400 rather than a silent fall back to "no bound".
	from, ok := reportDate(w, r, "from")
	if !ok {
		return
	}
	to, ok := reportDate(w, r, "to")
	if !ok {
		return
	}

	var categoryID uuid.UUID
	if raw := strings.TrimSpace(r.URL.Query().Get("cost_category_id")); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest,
				"cost_category_id must be a uuid: a cost category is the AXIS this summary is "+
					"reported on, and it cannot be guessed")
			return
		}
		categoryID = id
	}
	// uuid.Nil is passed through DELIBERATELY. The service owns the refusal
	// because it is the thing that can name the alternatives, and duplicating the
	// check here would give two different sentences for one fault.

	sum, err := s.report.CostCentreSummaryFor(ctx, companyID, categoryID, from, to)
	if err != nil {
		if ax, isAxis := reporting.AsCostAxisError(err); isAxis {
			status := http.StatusUnprocessableEntity
			if ax.Missing {
				// No axis named at all: the REQUEST is malformed, so 400 — the same
				// status stock-item-vouchers answers a missing stock_item_id with.
				status = http.StatusBadRequest
			}
			writeError(w, status, ax.Error())
			return
		}
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		// Echoed so a client cannot render an axis or a period it did not get.
		// This is not decoration: a screen that showed the Branch numbers under a
		// "Department" heading would be the exact failure the required parameter
		// exists to prevent.
		"cost_category_id": sum.CategoryID.String(),
		"cost_category":    sum.CategoryName,
		// The axis's own rule, so a reader can see WHAT this axis is allowed to
		// carry without a second request.
		"allocate_non_revenue": sum.AllocateNonRevenue,
		"from":                 sum.FromDate,
		"to":                   sum.ToDate,

		"centres": costCentreNodesJSON(sum.Roots),
		// Centres the roll-up could not place (a parent chain that loops or runs
		// past the depth bound). A FIELD and not an omission: a cost centre that
		// silently vanished from a cost report would take its money with it.
		"unattached": costCentreNodesJSON(sum.Unattached),

		"total_debit":  sum.TotalDebit.StringFixed(4),
		"total_credit": sum.TotalCredit.StringFixed(4),
		"total_net":    sum.Net().StringFixed(4),

		"unattached_debit":  sum.UnattachedDebit.StringFixed(4),
		"unattached_credit": sum.UnattachedCredit.StringFixed(4),

		// The FLAT sum of every centre's own figures — the report's independent
		// oracle — and the residual between it and the roll-up. Both are on the
		// wire so a client (or a live verifier) can re-assert the identity rather
		// than trust a boolean.
		"allocated_debit":   sum.AllocatedDebit.StringFixed(4),
		"allocated_credit":  sum.AllocatedCredit.StringFixed(4),
		"difference_debit":  sum.DifferenceDebit.StringFixed(4),
		"difference_credit": sum.DifferenceCredit.StringFixed(4),
		"reconciles":        sum.Reconciles(),

		// The caveats travel WITH the numbers, including the one that says this
		// total must never be added to another axis's. A screen may style them; it
		// may not decide whether to show them.
		"warnings": sum.Warnings,
	})
}

// costCentreNodesJSON renders the centre tree. Nested rather than flat because
// the roll-up IS the report: a flat list would leave a client to re-derive which
// figure contains which, and that derivation is the thing most likely to be got
// wrong twice.
func costCentreNodesJSON(nodes []reporting.CostCentreNode) []map[string]any {
	out := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, map[string]any{
			"cost_centre_id": n.CentreID.String(),
			"name":           n.Name,
			// "" for a top-level centre, never the nil uuid spelled out — a
			// client comparing against "00000000-..." is a client that will one
			// day compare against something else.
			"parent_id": uuidOrEmpty(n.ParentID),
			"depth":     n.Depth,
			// OWN and TOTAL are both here and neither is derivable from the
			// other: a parent may hold allocations of its own AND have children,
			// and only the pair tells an operator whether "Sales 60,000" was
			// keyed against Sales or against its sub-teams.
			"own_debit":    n.OwnDebit.StringFixed(4),
			"own_credit":   n.OwnCredit.StringFixed(4),
			"own_net":      n.OwnNet().StringFixed(4),
			"total_debit":  n.TotalDebit.StringFixed(4),
			"total_credit": n.TotalCredit.StringFixed(4),
			"total_net":    n.TotalNet().StringFixed(4),
			"children":     costCentreNodesJSON(n.Children),
		})
	}
	return out
}

// --------------------------------------------------------------------------
// GET /cost-category-summary — one row per axis, and the gap
// --------------------------------------------------------------------------

// handleCostCategorySummary answers the question the Cost Centre Summary cannot:
// "what am I missing".
//
// ⛔ THE RESPONSE CARRIES NO GRAND TOTAL AND MUST NEVER BE GIVEN ONE. Each row is
// a different AXIS through the SAME money; a total across them is a multiple of
// the cost borne. The `notes` array says so on the artefact itself, because the
// absence of a field only informs a programmer.
//
// It takes NO cost_category_id: this report IS the list of axes. That is why it
// is a separate report name rather than a mode of the other one — an
// administrator may reasonably let a clerk see WHICH analysis is incomplete
// without letting them see what each department cost.
func (s *Server) handleCostCategorySummary(w http.ResponseWriter, r *http.Request) {
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

	sum, err := s.report.CostCategorySummaryFor(ctx, companyID, from, to)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	rows := make([]map[string]any, 0, len(sum.Rows))
	for _, row := range sum.Rows {
		rows = append(rows, map[string]any{
			"cost_category_id":     row.CategoryID.String(),
			"cost_category":        row.CategoryName,
			"allocate_non_revenue": row.AllocateNonRevenue,

			// THE DENOMINATOR. Which postings this axis was measured against
			// depends on allocate_non_revenue, and a coverage figure whose
			// denominator is unstated is a percentage of nothing in particular —
			// so it is on the wire beside the numerator, not implied.
			"allocable_debit":    row.AllocableDebit.StringFixed(4),
			"allocable_credit":   row.AllocableCredit.StringFixed(4),
			"allocable_postings": row.AllocablePostings,

			"allocated_debit":    row.AllocatedDebit.StringFixed(4),
			"allocated_credit":   row.AllocatedCredit.StringFixed(4),
			"allocated_postings": row.AllocatedPostings,

			// THE GAP. This is what the report is for: cost that is in the Profit
			// & Loss and attached to nothing on this axis. No database constraint
			// can see it — the sum trigger fires on allocations, and there are
			// none to fire on.
			"unallocated_debit":    row.UnallocatedDebit.StringFixed(4),
			"unallocated_credit":   row.UnallocatedCredit.StringFixed(4),
			"unallocated_postings": row.UnallocatedPostings,
			"fully_allocated":      row.IsFullyAllocated(),

			// Two faults that should be impossible and are reported rather than
			// absorbed: a part-allocated posting (the trigger forbids it, so a
			// non-zero here means the guarantee is gone) and an allocation on a
			// ledger this axis may not carry (the database does not enforce
			// allocate_non_revenue; only this report can see it).
			"partly_allocated_postings":  row.PartlyAllocatedPostings,
			"partly_allocated_shortfall": row.PartlyAllocatedShortfall.StringFixed(4),
			"off_axis_debit":             row.OffAxisDebit.StringFixed(4),
			"off_axis_credit":            row.OffAxisCredit.StringFixed(4),
			"off_axis_postings":          row.OffAxisPostings,

			// Reconciled TO THE PAISA: allocable less allocated less unallocated
			// must be zero, and the residual is published so it cannot be hidden.
			"difference_debit":  row.DifferenceDebit.StringFixed(4),
			"difference_credit": row.DifferenceCredit.StringFixed(4),
			"reconciles":        row.Reconciles(),

			"notes": row.Notes,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"from": sum.FromDate,
		"to":   sum.ToDate,
		// ⛔ `rows` and no total. See the function comment.
		"rows":       rows,
		"reconciles": sum.Reconciles(),
		// The first note is the no-cross-axis-total statement, and it is the one
		// line on this response that keeps a UI from inventing a footer.
		"notes": sum.Notes,
	})
}
