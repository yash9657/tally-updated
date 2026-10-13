package server

// handler_close_year.go — closing a financial year IN PLACE.
//
// ===========================================================================
// WIRING (the two-touch rule)
// ===========================================================================
//	(a) routes in server.go routes():
//	      r.Get("/books-closure",   s.handleGetBooksClosure)
//	      r.Post("/books-closure",  s.handleCloseBooks)
//	      r.Delete("/books-closure", s.handleReopenBooks)
//	(b) the routeAction arm in auth_middleware.go mapping /books-closure to
//	    (Display, "company") for the GET and (Alter, "company") for the writes.
//
// Without (b) an Owner gets 403 "unmapped route: access denied", because
// routeAction default-denies unmapped {companyID} sub-paths.
//
// ===========================================================================
// ⛔ WHY THE TOKEN IS `company` AND NOT A NEW ONE
// ===========================================================================
// Closing a year IS company configuration — the same authority as the F11
// feature settings (`PUT ""`) and the print templates, both of which already
// map to (Alter, "company"). Migration 0022 seeds the Data Entry level `allow
// display company` and nothing wider, and that level's default_access is
// 'none', so `alter company` is default-DENIED for every seeded non-Owner level
// the day these routes appear. That is why 0089 needs no ACL migration.
//
// ⛔ AND NOT report:NAME. Migration 0011 seeds Data Entry a coarse `allow
// display report`, so anything spelled report:<something> is GRANTED to every
// operator in every tenant the moment it exists. Year-end closure is not a
// report and must not inherit a report's audience.
//
// ⚠️ A NEW TOKEN WOULD BE WORSE THAN EITHER. An administrator who wrote
// `disallow alter company` to lock down configuration would discover it did not
// cover the one setting that decides whether a filed year can be edited.
//
// ===========================================================================
// THE WRITES TAKE THE WEBAUTHN STEP-UP AND NOT THE CUTOFF, AND BOTH ARE RIGHT
// ===========================================================================
// Being writes they inherit the Owner level's step-up (Action.IsWrite()) —
// correct: closing or re-opening a statutory year is exactly the kind of act
// that should cost a touch of the key.
//
// They do NOT inherit the back-dated cutoff, because voucherDateFromRequest
// returns nil for a route carrying no voucher date. ⛔ THAT IS LOAD-BEARING FOR
// THE RE-OPEN: if the year-end lock applied to the request that lifts the
// year-end lock, a closed year could never be re-opened by anybody, and the
// only recovery would be SQL. A one-way door in a statutory control is how
// people end up editing the database by hand.

import (
	"errors"
	"net/http"
	"time"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/platform/db"
)

// closureReq is the POST body: the inclusive last date of the closed period.
type closureReq struct {
	ClosedThrough string `json:"closed_through"`
}

// handleGetBooksClosure reports the current year-end lock.
//
// GET /v1/companies/{companyID}/books-closure
func (s *Server) handleGetBooksClosure(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	through, at, by, err := s.identity.BooksClosure(ctx, companyID)
	if err != nil {
		writeClosureError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, closureBody(through, at, by))
}

// handleCloseBooks locks the books through the supplied date.
//
// POST /v1/companies/{companyID}/books-closure  {"closed_through":"2027-03-31"}
func (s *Server) handleCloseBooks(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	var req closureReq
	if !decodeJSON(w, r, &req) {
		return
	}
	through, err := time.Parse("2006-01-02", req.ClosedThrough)
	if err != nil {
		// ⚠️ THE REFUSAL NAMES THE SHAPE AND THE MEANING. "Invalid date" would
		// leave an operator guessing whether the field wants the last day of the
		// closed year or the first day of the new one — a one-day error that
		// silently locks or unlocks a month.
		writeError(w, http.StatusBadRequest,
			"closed_through must be a date as YYYY-MM-DD, and it is the LAST day of the "+
				"period being closed — for the financial year 2026-27 that is 2027-03-31, "+
				"never 2027-04-01")
		return
	}
	// The acting identity, the same way handler_admin.go reads it. A closure
	// with no recorded author would answer "who decided this year was final?"
	// with silence, which is the first question an auditor asks.
	userID, _ := db.UserFromContext(ctx)
	if err := s.identity.CloseBooks(ctx, companyID, userID, through); err != nil {
		writeClosureError(w, err)
		return
	}
	gotThrough, at, by, err := s.identity.BooksClosure(ctx, companyID)
	if err != nil {
		writeClosureError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, closureBody(gotThrough, at, by))
}

// handleReopenBooks clears the year-end lock.
//
// DELETE /v1/companies/{companyID}/books-closure
func (s *Server) handleReopenBooks(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	if err := s.identity.ReopenBooks(ctx, companyID); err != nil {
		writeClosureError(w, err)
		return
	}
	through, at, by, err := s.identity.BooksClosure(ctx, companyID)
	if err != nil {
		writeClosureError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, closureBody(through, at, by))
}

// closureBody is the ONE shape all three routes answer with, so a client never
// has to reconcile two spellings of the same fact.
func closureBody(through, at *time.Time, by any) map[string]any {
	out := map[string]any{"closed": through != nil}
	if through != nil {
		out["closed_through"] = through.UTC().Format("2006-01-02")
	}
	if at != nil {
		out["closed_at"] = at.UTC().Format(time.RFC3339)
	}
	if by != nil {
		out["closed_by"] = by
	}
	return out
}

// writeClosureError maps the identity sentinels onto statuses.
//
// ⛔ ErrClosureMovesBackward IS A 409 AND NOT A 400. The request is
// well-formed and the date is a real date; what it CONFLICTS with is the state
// of the books. A 400 would send the operator to check their typing when what
// they need to do is decide whether to re-open.
func writeClosureError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, identity.ErrCompanyNotFound):
		writeError(w, http.StatusNotFound, "company not found")
	case errors.Is(err, identity.ErrClosureMovesBackward):
		writeCodedError(w, http.StatusConflict, "BOOKS_ALREADY_CLOSED_LATER",
			err.Error()+". Closing only ever moves the lock FORWARD; to unlock a "+
				"period that is already closed, re-open the books first — that is a "+
				"separate, deliberate act")
	case errors.Is(err, identity.ErrNothingClosed):
		writeCodedError(w, http.StatusConflict, "NOTHING_CLOSED", err.Error())
	case errors.Is(err, db.ErrNoCompany):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "books closure: "+err.Error())
	}
}
