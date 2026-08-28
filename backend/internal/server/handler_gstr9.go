package server

// handler_gstr9.go holds the GET /v1/companies/{companyID}/gstr9 handler — the
// GSTR-9 ANNUAL RECONCILIATION (T12, migration 0087).
//
// WIRING (the two-touch rule, IMPLEMENTATION_PLAN.md sec 1.5): the route IS
// MOUNTED, and it took BOTH touches —
//
//	(a) r.Get("/gstr9", s.handleGSTR9)  in server.go routes(), next to the
//	    existing r.Get("/gstr1", ...) and r.Get("/gstr3b", ...), and
//	(b) case "/gstr9": return "gstr9"   in reportName() in auth_middleware.go.
//
// Keep both if this handler is ever renamed or moved: with only (a), even an
// Owner gets a 403, because routeAction default-denies unmapped {companyID}
// sub-paths.
//
// ---------------------------------------------------------------------------
// WHY THIS HANDLER REFUSES MORE THAN ITS TWO NEIGHBOURS DO
// ---------------------------------------------------------------------------
// /gstr1 and /gstr3b are permissive by design: any window, tax ledgers
// optional. Both are legitimate — a RETURN is a thing you may want to look at
// without reconciling it, and a mid-month preview is a real question.
//
// GSTR-9 is neither of those things, and this handler refuses two requests they
// would accept:
//
//	400  THE WINDOW IS NOT A FINANCIAL YEAR. There is no such document as "the
//	     GSTR-9 for June to August". Producing one would put a real-looking
//	     statutory artifact in a filer's hands for a period the law does not
//	     recognise. The message names the financial year that CONTAINS the date
//	     asked for, and says to read the monthly GSTR-3B for a shorter window.
//
//	400  THE TAX LEDGER IDS ARE MISSING. Without them the books column is zero
//	     for all twelve months, every period appears to disagree with the returns
//	     by exactly the tax charged, and the filer takes away a wrong number that
//	     LOOKS LIKE A FINDING. All six are named in one message rather than one
//	     per round trip.
//
// Both refusals live in compliance/gstr (financialYear and requireLedgers), not
// here: they are properties of the document, and a second copy in the handler is
// how the API and the service come to disagree about what a GSTR-9 is.
//
// ---------------------------------------------------------------------------
// ⛔ IT IS NOT FILING-READY AND EVERY RESPONSE SAYS SO
// ---------------------------------------------------------------------------
// `disclosures` always carries TEN tokens on a clean year, and up to twelve when
// the year does not reconcile. A frontend must switch on ALL of them; rendering
// some and ignoring the rest is the closed-vocabulary failure the field exists
// to prevent. The ten unconditional ones, in emitted order:
//
//	advances-not-modelled              amendments-not-modelled
//	annual-table-structure-unverified  books-carry-no-taxable-value
//	exempt-and-zero-rated-not-marked   itc-reversals-not-computed
//	no-annual-2a                       no-cash-or-credit-ledger
//	not-filing-ready                   portal-schema-unverified
//
// and the two conditional ones:
//
//	monthly-differences-offset   the year's disagreements CANCEL, so the annual
//	                             net reads clean and is not. This is the single
//	                             most important token in the set.
//	returns-and-books-disagree   at least one comparison is non-zero in at least
//	                             one month.
//
// `annual-table-structure-unverified` is the one that must never be dropped in a
// UI: RESEARCH.md contains no GSTR-9 material at all, so the table numbering and
// titles were written from the return's published structure as generally
// understood and have NOT been checked against the CBIC notification or the GSTN
// utility. The portal is geo-fenced from this build, so they could not be.
//
// THIS IS A RECONCILIATION WORKPAPER, and a genuinely useful one: it finds the
// month where the returns and the books parted company, which is the job an
// accountant does before filing. It is not an upload artifact.
//
// ---------------------------------------------------------------------------
// ⚠️ NO statement_id, AND THAT IS A DECISION RATHER THAN AN OMISSION
// ---------------------------------------------------------------------------
// /gstr3b takes a `statement_id` and turns on the 2A/2B-backed table 4. This
// route does not, because a statement belongs to ONE period and this covers
// twelve — compliance/itc refuses a window longer than one period for exactly
// that reason. Accepting one and applying it to the year would reconcile eleven
// months against a statement that has nothing to do with them.
//
// The consequence is DISCLOSED, not hidden: the twelve 3Bs behind the year are
// the bare ones, GSTR-9 table 7 ("ITC reversed and ineligible") is NOT_DERIVED
// rather than zero, and `itc-reversals-not-computed` is unconditional. A
// statement-backed annual ITC needs a statement PER MONTH, which is a different
// signature and a different decision.

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/compliance/gstr"
)

// handleGSTR9 builds the annual reconciliation for the financial year
// [from, to].
//
// Query params:
//
//	from, to        REQUIRED, YYYY-MM-DD, and REQUIRED to be a financial year:
//	                YYYY-04-01 to (YYYY+1)-03-31.
//	gstin           filer GSTIN.
//	cgst_ledger_id / sgst_ledger_id / igst_ledger_id
//	                OUTPUT-tax ledgers. REQUIRED here (optional on /gstr1 and
//	                /gstr3b) — see the file header.
//	input_cgst_ledger_id / input_sgst_ledger_id / input_igst_ledger_id
//	                INPUT-tax ledgers. REQUIRED, same reason.
func (s *Server) handleGSTR9(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	from := q.Get("from")
	to := q.Get("to")
	if from == "" || to == "" {
		writeError(w, http.StatusBadRequest,
			"from and to query params are required (YYYY-MM-DD), and must be a financial year: "+
				"1 April to 31 March")
		return
	}

	opts := gstr.Options3B{Options: gstr.Options{GSTIN: q.Get("gstin")}}
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
		if *p.dst, err = optionalUUID(q.Get(p.param)); err != nil {
			writeError(w, http.StatusBadRequest, "invalid "+p.param)
			return
		}
	}

	g, err := s.gstr.Generate9(ctx, from, to, opts)
	if err != nil {
		writeGSTR9Error(w, err)
		return
	}
	// `reconciled` is lifted OUT of the document and onto the envelope for the
	// reason handleGSTR3B lifts `reconciliation_ok`: it is the one thing a
	// caller wants without walking five divergences, and a client that has to
	// compute it will compute it from the annual nets, which is precisely the
	// question a net cannot answer.
	writeJSON(w, http.StatusOK, map[string]any{
		"gstr9":      g,
		"reconciled": g.Reconciled(),
	})
}

// writeGSTR9Error maps the service's sentinels onto statuses.
//
// BOTH SENTINELS ARE 400 AND NOT 422, deliberately. A 422 says "the request was
// well-formed but the books cannot answer it"; both of these say the REQUEST was
// wrong and name the parameter to change. ErrInvalid9 keeps the 422 that
// generation failures get everywhere else in this package — it means the
// generated document violated one of the report's own honesty rules, which is a
// server-side defect the caller cannot fix by editing the query.
func writeGSTR9Error(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, gstr.ErrNotAFinancialYear), errors.Is(err, gstr.ErrLedgersRequired):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	}
}
