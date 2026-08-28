package server

// handler_budget_variance.go — budget vs actual over HTTP (T11, migrations
// 0085/0086).
//
//	GET .../budget-variance?budget_id=&dimension=[&cost_category_id=]
//	                                    -> Display / report:budget-variance
//
// ===========================================================================
// WHY EVERY SELECTOR IS A QUERY PARAMETER AND THE PATH IS A FLAT LITERAL
// ===========================================================================
// A report's route path must be a FLAT LITERAL SPELLING ITS OWN NAME.
// TestReportVocabularyMatchesRouter asks routeAction for
// GET /v1/companies/{id}/<name> for every name in authz.KnownReportNames, so a
// report reached by /budgets/{id}/variance would be a vocabulary entry no route
// serves — a `disallow` an administrator can write that can never match, which
// is the silent-permit failure internal/authz/vocabulary.go exists to prevent.
// So the budget, the grain and the axis all ride in the query string, exactly as
// cost-centre-summary takes cost_category_id and stock-item-vouchers takes
// stock_item_id.
//
// ⚠️ AND THE PATH IS `/budget-variance` WHILE THE MASTER IS `/budgets`, one
// character from a collision. routeAction's master arm is
// `strings.HasPrefix(rest, "/budgets")` and "/budget-variance" does not carry
// that prefix, so this path falls through to the reports arm and is served under
// `report:budget-variance` as intended. A master route at "/budget" would have
// swallowed it and served this report under the BARE `budget` token, which no
// `report:` rule could match — migration 0086 would have been dead text.
//
// ===========================================================================
// ⛔⛔ THREE REFUSALS, AND EACH ONE EXISTS BECAUSE THE SILENT ANSWER IS WORSE
// ===========================================================================
// This handler's whole job is choosing status codes; the SENTENCES belong to
// internal/reporting, which holds the lists a refusal must name. Each refusal is
// a case where a default would render a real, correctly-footed report for a
// question the operator did not ask:
//
//   - NO budget_id -> 400. There is no default plan. An empty variance report
//     reads as "nothing was planned and nothing was spent", which is a real and
//     reassuring claim about a company and would be a lie.
//   - NO dimension -> 400. The three grains answer different questions, and
//     rendering the LEDGER variance to someone who wanted GROUP is a page of
//     correct numbers about the wrong thing.
//   - NO cost_category_id when dimension=COST_CENTRE -> 400. A cost category is
//     an AXIS and the same money is allocated in full on every axis, so picking
//     one silently reports an axis the operator did not choose and summing them
//     reports a multiple of the cost borne. Reuses CostAxisError, which the Cost
//     Centre Summary already raises for the identical reason.
//
// 400 when nothing was named (a malformed request) and 422 when something was
// named that these books do not contain (a request about something absent) — the
// same split handler_cost_summary.go records.
//
// ===========================================================================
// THERE IS NO PRINT LAYOUT, SO THE PRINT ROUTE ANSWERS 501
// ===========================================================================
// Adding a name to authz.KnownReportNames AUTO-MOUNTS
// GET /print/reports/budget-variance, and the name is in it because an
// administrator must be able to write `disallow print report:budget-variance` —
// which migration 0086 does. No printed layout has been specified for a variance
// statement, so handlePrintReport's default arm answers 501 with the report
// named. A blank page that looks like a filed budget is worse than a refusal, and
// a name the router does not emit is a rule that can never match. Nothing needs
// adding to handler_print_reports.go: its default arm already covers every name
// without a builder.
//
// MONEY GOES OUT AS FIXED-4 STRINGS — and ⛔ variance_percent goes out as JSON
// null when there is no budget to be a percentage of. See the note on it below.

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/reporting"
)

// handleBudgetVariance serves GET .../budget-variance.
func (s *Server) handleBudgetVariance(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()

	// uuid.Nil is passed through DELIBERATELY on both ids. The service owns the
	// refusal because it is the thing that can name the alternatives, and
	// duplicating the check here would give two different sentences for one fault.
	var budgetID uuid.UUID
	if raw := strings.TrimSpace(q.Get("budget_id")); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest,
				"budget_id must be a uuid: a variance report is a comparison against ONE "+
					"named plan, and there is no default plan to fall back on")
			return
		}
		budgetID = id
	}
	var categoryID uuid.UUID
	if raw := strings.TrimSpace(q.Get("cost_category_id")); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest,
				"cost_category_id must be a uuid: a cost category is the AXIS a cost-centre "+
					"variance is reported on, and it cannot be guessed")
			return
		}
		categoryID = id
	}

	rep, err := s.report.BudgetVarianceFor(ctx, companyID, budgetID, categoryID, q.Get("dimension"))
	if err != nil {
		writeBudgetVarianceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		// Echoed so a client cannot render a plan, a grain, a period or an axis it
		// did not ask for. This is not decoration: a screen that showed the LEDGER
		// numbers under a "By Group" heading, or one axis's figures under another's
		// name, is the exact failure the required parameters exist to prevent.
		"budget_id":   rep.BudgetID.String(),
		"budget_name": rep.BudgetName,
		"dimension":   rep.Dimension,
		// ⛔ THE PERIOD IS THE BUDGET'S OWN AND THE REPORT DOES NOT NARROW IT. See
		// the `notes` array, which says so on the artefact — comparing a full-period
		// budget against a shorter window needs a spread rule nobody has chosen.
		"from_date": rep.FromDate,
		"to_date":   rep.ToDate,

		"rows": budgetVarianceRowsJSON(rep.Rows),

		// ⛔ A SEPARATE ARRAY AND NOT ROWS WITH A ZERO BUDGET. An actual with no
		// budget is UNBUDGETED, not 100% over: the plan is silent about it rather
		// than exceeded. Keeping it out of `rows` is what stops the budget column
		// carrying figures nobody set, and it is the array a finance person reads
		// first — money leaving the business that no plan accounted for.
		"unbudgeted":        budgetVarianceRowsJSON(rep.Unbudgeted),
		"unbudgeted_count":  rep.UnbudgetedCount,
		"unbudgeted_actual": rep.UnbudgetedActual.StringFixed(4),

		// ⛔⛔ SUBTOTALS PER BASIS, ALWAYS. A CLOSING_BALANCE budget is a POSITION
		// and a NETT_TRANSACTIONS budget is a FLOW, and their sum is not a quantity.
		"totals": budgetBasisTotalsJSON(rep.Totals),
		// ⛔ AND totals_are_addable IS THE FIELD A UI MUST CHECK BEFORE DRAWING A
		// FOOTER. When a plan mixes bases the three grand totals below are ZERO and
		// this is false — not because the report failed, but because no single
		// number is correct. A footer that summed the two bases would be the most
		// believable wrong figure on the page.
		"totals_are_addable": rep.TotalsAreAddable,
		"total_budget":       rep.TotalBudget.StringFixed(4),
		"total_actual":       rep.TotalActual.StringFixed(4),
		"total_variance":     rep.TotalVariance.StringFixed(4),

		// Counts by status, so a screen can lead with "4 lines overspent, 11
		// untouched" without walking the rows.
		"status_counts": rep.StatusCounts,

		// The axis, for a cost-centre report. "" otherwise, never the nil uuid
		// spelled out.
		"cost_category_id":   uuidOrEmpty(rep.CategoryID),
		"cost_category_name": rep.CategoryName,

		// The caveats travel WITH the numbers — the mixed-basis refusal, the
		// no-apportionment statement, the cross-axis warning and the reminder that
		// OVER is arithmetic rather than bad news. A screen may style them; it may
		// not decide whether to show them.
		"notes": rep.Notes,
	})
}

func budgetVarianceRowsJSON(rows []reporting.BudgetVarianceRow) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		m := map[string]any{
			"dimension":   row.Dimension,
			"target_id":   row.TargetID.String(),
			"target_name": row.TargetName,
			// The target's nature, and "" for a cost centre which has none. ⛔ IT IS
			// HERE BECAUSE THIS REPORT REFUSES TO SAY "FAVOURABLE": spending more
			// than budget is bad, EARNING more than budget is good, and both are
			// OVER. The nature is what lets a reader tell which — the report will
			// not guess, because it would guess wrong for a whole class of ledgers.
			"nature": row.Nature,
			"basis":  row.Basis,
			"dr_cr":  row.DrCr,

			"budget": row.Budget.StringFixed(4),
			// BOTH RAW SIDES, not only the nett. They are what distinguish UNSPENT
			// (nothing posted) from NETTED_TO_ZERO (movement that cancelled), and
			// they let a client — or a live verifier — re-derive `actual` rather
			// than trust it.
			"actual_debit":  row.ActualDebit.StringFixed(4),
			"actual_credit": row.ActualCredit.StringFixed(4),
			"actual":        row.Actual.StringFixed(4),
			"variance":      row.Variance.StringFixed(4),

			// ⛔ ONE OF SEVEN DISTINCT FACTS, NEVER A ROUNDING OF ANOTHER:
			// ON_TARGET, OVER, UNDER, UNSPENT, NETTED_TO_ZERO, CONTRA, UNBUDGETED.
			"status": row.Status,
			// The operator's own note from the budget line.
			"note": row.Note,
			// The REPORT's caveats about this row, which travel with the number.
			"notes": row.Notes,
		}
		// ⛔⛔ null AND NEVER A NUMBER WHEN THERE IS NO BUDGET TO DIVIDE BY. A 0, a
		// 100 or an Inf here is a figure a reader will believe. JSON null forces a
		// client to render "no budget set", which is the truth.
		if row.VariancePercent != nil {
			m["variance_percent"] = row.VariancePercent.StringFixed(2)
		} else {
			m["variance_percent"] = nil
			m["variance_percent_absent_because"] = "no budget was set for this target, so there " +
				"is nothing for the actual to be a percentage of"
		}
		if row.CostCategoryID != uuid.Nil {
			m["cost_category_id"] = row.CostCategoryID.String()
			m["cost_category_name"] = row.CostCategoryName
		}
		out = append(out, m)
	}
	return out
}

func budgetBasisTotalsJSON(totals []reporting.BudgetBasisTotal) []map[string]any {
	out := make([]map[string]any, 0, len(totals))
	for _, t := range totals {
		out = append(out, map[string]any{
			"basis":    t.Basis,
			"rows":     t.Rows,
			"budget":   t.Budget.StringFixed(4),
			"actual":   t.Actual.StringFixed(4),
			"variance": t.Variance.StringFixed(4),
		})
	}
	return out
}

// writeBudgetVarianceError chooses the status code. The SENTENCES belong to
// internal/reporting, which holds the lists a refusal must name.
func writeBudgetVarianceError(w http.ResponseWriter, err error) {
	if be, ok := reporting.AsBudgetSelectionError(err); ok {
		status := http.StatusUnprocessableEntity
		if be.Missing {
			// No plan named at all: the REQUEST is malformed, so 400.
			status = http.StatusBadRequest
		}
		writeError(w, status, be.Error())
		return
	}
	if de, ok := reporting.AsBudgetDimensionError(err); ok {
		// An absent or unknown grain is malformed either way — unlike a budget or an
		// axis, the dimension is a CLOSED SET of three known strings, so a bad one
		// is a bad request rather than a request about something absent.
		writeError(w, http.StatusBadRequest, de.Error())
		return
	}
	if ax, ok := reporting.AsCostAxisError(err); ok {
		status := http.StatusUnprocessableEntity
		if ax.Missing {
			status = http.StatusBadRequest
		}
		writeError(w, status, ax.Error())
		return
	}
	writeError(w, http.StatusUnprocessableEntity, err.Error())
}
