package server

// handler_gstr3b.go holds the GET /v1/companies/{companyID}/gstr3b handler.
//
// WIRING (the two-touch rule, IMPLEMENTATION_PLAN.md sec 1.5): this method is
// deliberately NOT registered here. Until the serial wiring stage adds BOTH
//
//	(a) r.Get("/gstr3b", s.handleGSTR3B)  in server.go routes(), next to the
//	    existing r.Get("/gstr1", s.handleGSTR1), and
//	(b) case "/gstr3b": return "gstr3b"   in reportName() in auth_middleware.go,
//
// the route does not exist, and adding only (a) would make even an Owner get a
// 403 because routeAction default-denies unmapped {companyID} sub-paths.
//
// Response shape mirrors handleGSTR1: the bare return when no tax-ledger ids are
// supplied, or {"gstr3b": ..., "reconciliation_ok": bool} when any are, so the
// caller can see whether the return ties to the posted tax to the paisa.
//
// The generated 3B carries DISCLOSED v1 limitations — most importantly ITC is
// reported ALL-ELIGIBLE (no eligibility/reversal modelling) and the JSON field
// names are NOT verified against the GSTN 3B offline-utility schema, so portal
// upload compatibility is not claimed. See the doc comment at the top of
// internal/compliance/gstr/gstr3b.go for the full list.

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/compliance/gstr"
)

// handleGSTR3B generates the GSTR-3B summary for [from, to].
//
// Query params: from, to (required, YYYY-MM-DD); gstin; the output-tax ledger
// ids cgst_ledger_id / sgst_ledger_id / igst_ledger_id (used for the outward
// reconciliation, same names as /gstr1); and the input-tax ledger ids
// input_cgst_ledger_id / input_sgst_ledger_id / input_igst_ledger_id (used for
// the table-4 ITC reconciliation).
func (s *Server) handleGSTR3B(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	if from == "" || to == "" {
		writeError(w, http.StatusBadRequest, "from and to query params are required (YYYY-MM-DD)")
		return
	}

	opts := gstr.Options3B{Options: gstr.Options{GSTIN: r.URL.Query().Get("gstin")}}
	var err error
	for _, p := range []struct {
		param string
		dst   *uuid.UUID
	}{
		{"cgst_ledger_id", &opts.CGSTLedgerID},
		{"sgst_ledger_id", &opts.SGSTLedgerID},
		{"igst_ledger_id", &opts.IGSTLedgerID},
		{"input_cgst_ledger_id", &opts.InputCGSTLedgerID},
		{"input_sgst_ledger_id", &opts.InputSGSTLedgerID},
		{"input_igst_ledger_id", &opts.InputIGSTLedgerID},
	} {
		if *p.dst, err = optionalUUID(r.URL.Query().Get(p.param)); err != nil {
			writeError(w, http.StatusBadRequest, "invalid "+p.param)
			return
		}
	}

	// When the caller supplied ANY tax ledger, reconcile the return against the
	// books and report whether both halves tie out to the paisa.
	anyLedger := opts.CGSTLedgerID != uuid.Nil || opts.SGSTLedgerID != uuid.Nil || opts.IGSTLedgerID != uuid.Nil ||
		opts.InputCGSTLedgerID != uuid.Nil || opts.InputSGSTLedgerID != uuid.Nil || opts.InputIGSTLedgerID != uuid.Nil
	if anyLedger {
		g, rec, err := s.gstr.Reconcile3B(ctx, from, to, opts)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"gstr3b":            g,
			"reconciliation_ok": rec.Balanced(),
		})
		return
	}

	g, err := s.gstr.Generate3B(ctx, from, to, opts)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, g)
}
