package server

// handler_forecast.go holds GET /v1/companies/{companyID}/cash-flow and
// .../anomalies (wave-2 WS5a).
//
// WIRING (the two-touch rule, IMPLEMENTATION_PLAN.md sec 1.5): these methods are
// deliberately NOT registered here. Until the serial wiring stage adds BOTH
//
//	(a) r.Get("/cash-flow", s.handleCashFlow)  and
//	    r.Get("/anomalies", s.handleAnomalies)  in server.go routes(), in the
//	    "// Reports." block next to r.Get("/outstandings", s.handleOutstandings), and
//	(b) case "/cash-flow": return "cash-flow"  and
//	    case "/anomalies": return "anomalies"   in reportName() in auth_middleware.go
//	    (routeAction's generic report arm then maps them to Display/report:<name>),
//
// the routes do not exist, and adding only (a) would make even an Owner get a 403
// because routeAction default-denies unmapped {companyID} sub-paths.
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
	}
}
