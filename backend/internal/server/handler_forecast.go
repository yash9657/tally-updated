package server

// handler_forecast.go holds GET /v1/companies/{companyID}/cash-flow and
// .../anomalies (wave-2 WS5a).
//
// WIRING (the two-touch rule, IMPLEMENTATION_PLAN.md sec 1.5). ⚠️ THIS COMMENT
// USED TO SAY THESE METHODS ARE "DELIBERATELY NOT REGISTERED", AND IT WAS STALE:
// both touches landed long ago and the routes are live. Corrected here because an
// agent reading it would conclude the server half of the cash-flow feature was
// unbuilt and build it again. The two touches, for the record:
//
//	(a) r.Get("/cash-flow", s.handleCashFlow)  and
//	    r.Get("/anomalies", s.handleAnomalies)  in server.go routes() — DONE
//	    (server.go:908-909), in the "// Reports." block next to
//	    r.Get("/outstandings", s.handleOutstandings); and
//	(b) case "/cash-flow": return "cash-flow"  and
//	    case "/anomalies": return "anomalies"   in reportName() in
//	    auth_middleware.go — DONE (auth_middleware.go:2014-2017); routeAction's
//	    generic report arm then maps them to Display/report:<name>.
//
// Doing only (a) would make even an Owner get a 403, because routeAction
// default-denies unmapped {companyID} sub-paths.
//
// ✅ THE CLIENT NOW EXISTS, AND THIS COMMENT USED TO SAY IT DID NOT. For a whole
// wave this block read "WHAT IS STILL MISSING IS THE CLIENT, NOT THE ROUTE — this
// handler is reachable by cURL and by nothing an operator can press", which was
// true and was register defect D22. Both routes now have a door:
// frontend/src/screens/reports/ForecastScreens.tsx renders them as the report
// names "Cash Flow" and "Anomalies", reachable from Alt+G and Display More
// Reports, with client methods cashFlow()/anomalies().
//
// ⛔ WHICH MATTERS TO THIS FILE FOR ONE REASON: the disclosure below now HAS a
// renderer. The long comment on cashFlowJSON ends "it is written so that the
// screen has something true to render the moment it exists" — that moment has
// arrived, and CashFlowScreen renders `estimate`, the `method` sentence, all three
// `assumed_settlement_date` sources SEPARATELY (never totalled into one "assumed"
// figure), the overdue fold with its money, and every `not_modelled` line
// verbatim. ⚠️ SO CHANGING A SENTENCE IN basisJSON NOW CHANGES WHAT AN OPERATOR
// READS. It is no longer server-side prose that nobody sees.
//
// The forecast Service is constructed per-request from s.pool (which satisfies
// both the read-only runner and reporting's tx runner). This diverges from the
// s.report/s.gstr struct-field convention deliberately, to keep the WS1-owned
// server.go diff to the two route lines — the service is cheap to build.

import (
	"net/http"
	"strconv"

	"github.com/khata/cored/internal/forecast"
)

// handleCashFlow projects cash movement from open bills.
//
// Query params: as_on (YYYY-MM-DD, empty = today); horizon_days (int, default
// 90); bucket ("week" | "month", default "week").
func (s *Server) handleCashFlow(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	asOn := r.URL.Query().Get("as_on")
	horizon := 90
	if h := r.URL.Query().Get("horizon_days"); h != "" {
		n, err := strconv.Atoi(h)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "horizon_days must be a positive integer")
			return
		}
		horizon = n
	}
	bucket := r.URL.Query().Get("bucket")

	p, err := forecast.NewService(s.pool).CashFlow(ctx, companyID, asOn, horizon, bucket)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cashFlowJSON(p))
}

// handleAnomalies returns rule-based anomaly findings over the version chain.
//
// Query params: from, to (YYYY-MM-DD, empty = unbounded; window keys on
// created_at — the "when was it entered" audit axis).
func (s *Server) handleAnomalies(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")

	findings, err := forecast.NewService(s.pool).Anomalies(ctx, companyID, from, to)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(findings))
	for _, f := range findings {
		out = append(out, map[string]any{
			"kind":       f.Kind,
			"voucher_id": f.VoucherID.String(),
			"voucher_no": f.VoucherNo,
			"severity":   f.Severity,
			"detail":     f.Detail,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "findings": out})
}

// cashFlowJSON renders a Projection to the wire shape. All money is fixed-4
// strings (paisa-exact, matching handleOutstandings).
//
// ⛔⛔ THIS RESPONSE MUST SAY THAT IT IS A PROJECTION AND WHAT IT ASSUMED.
// It used to carry exactly seven keys — as_on, horizon_days, bucket_days,
// opening_cash, buckets, beyond_horizon, overdue_folded_into_first_bucket — and
// not one of them contained the words projection, estimate, basis, assumed or
// method. Every money value in it is fixed-4 and reads exactly like a posted
// ledger figure, while the engine INVENTS a settlement date for every unpaid
// bill: due_date if the bill has one, else the party's credit_days, else a
// 30-day convention compiled into internal/forecast. A closing_cash of
// 4,12,300.0000 in a bucket three months out is not a fact, and nothing on the
// wire said so.
//
// So `estimate`, `method` and `basis` are part of the CONTRACT, not decoration.
// `basis` splits the projected money by WHERE ITS DATE CAME FROM, per side, so a
// reader can see at a glance how much of the answer is observed (a real due date)
// and how much is the 30-day convention talking.
//
// ⚠️ A DISCLOSURE WITH NO RENDERER IS NOTHING (RULE 3). There is still no client
// screen calling this route; see the report on D22. This half is the half that
// belongs to the server, and it is written so that the screen has something true
// to render the moment it exists — the alternative, shipping the screen first
// against an undisclosed projection, is how a guess gets printed as a balance.
func cashFlowJSON(p forecast.Projection) map[string]any {
	buckets := make([]map[string]any, 0, len(p.Buckets))
	for _, b := range p.Buckets {
		buckets = append(buckets, map[string]any{
			"from":         b.From.Format("2006-01-02"),
			"to":           b.To.Format("2006-01-02"),
			"inflow":       b.Inflow.StringFixed(4),
			"outflow":      b.Outflow.StringFixed(4),
			"nett":         b.Nett.StringFixed(4),
			"opening_cash": b.OpeningCash.StringFixed(4),
			"closing_cash": b.ClosingCash.StringFixed(4),
		})
	}
	return map[string]any{
		"as_on":        p.AsOn.Format("2006-01-02"),
		"horizon_days": p.HorizonDays,
		"bucket_days":  p.BucketDays,
		"opening_cash": p.OpeningCash.StringFixed(4),
		"buckets":      buckets,
		"beyond_horizon": map[string]any{
			"inflow":  p.Beyond.Inflow.StringFixed(4),
			"outflow": p.Beyond.Outflow.StringFixed(4),
			"nett":    p.Beyond.Nett.StringFixed(4),
		},
		"overdue_folded_into_first_bucket": p.OverdueFolded,

		// ---- THE DISCLOSURE ------------------------------------------------
		// `estimate` is first and is a plain bool so no client can miss it or
		// have to parse prose to find out.
		"estimate": true,
		"method":   "open-bill settlement projection (deterministic; no statistics, no model)",
		"basis":    basisJSON(p.Basis),
	}
}

// basisJSON renders the projection's disclosure. `opening_cash` is a real ledger
// position and the BILL AMOUNTS are real pending balances (they come from
// reporting.Outstandings, the same netting the Outstandings report uses) — what
// is assumed is WHEN each of them lands, which is what this block reports.
func basisJSON(b forecast.Basis) map[string]any {
	count := func(c forecast.BasisCount) map[string]any {
		return map[string]any{
			"bills":   c.Bills,
			"inflow":  c.Inflow.StringFixed(4),
			"outflow": c.Outflow.StringFixed(4),
		}
	}
	return map[string]any{
		"what_is_projected": "the DATE each open bill settles, not its amount. " +
			"Amounts are the pending balances the Outstandings report shows; the " +
			"bucket a bill falls into is derived.",
		"assumed_settlement_date": map[string]any{
			// OBSERVED — the bill carried an explicit due date.
			"from_due_date": count(b.FromDueDate),
			// ASSUMED — the party's configured credit period.
			"from_party_credit_days": count(b.FromPartyCreditDays),
			// ⛔ ASSUMED, and by a constant in this build rather than by anything
			// the tenant said.
			"from_default_credit_days": count(b.FromDefaultCreditDays),
			"default_credit_days":      b.DefaultCreditDays,
		},
		"overdue_folded_into_first_bucket": count(b.Overdue),
		"not_modelled": []string{
			"vouchers not yet entered — this reads the books, not the business",
			"part payments arriving earlier or later than the projected date",
			"bills raised after as_on",
			"cash movements with no bill behind them (salaries, rent, taxes, drawings)",
		},
	}
}
