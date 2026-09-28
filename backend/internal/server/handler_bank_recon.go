package server

// HTTP surface for T4 bank reconciliation.
//
// Design memo: docs/design/t4-model.md.
//
// T1 shipped a printing pipeline reachable over HTTP only, with no key anywhere
// in the client — the handoff calls that a parity gap rather than a nicety. This
// file is the server half; the screen is the next slice, and the shape here is
// chosen so that screen is a thin one: a GET that returns everything the
// reconciliation view needs in one call, and an idempotent PUT/DELETE per line.

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/khata/cored/internal/banking"
	"github.com/khata/cored/internal/bankstatement"
	"github.com/khata/cored/internal/reporting"
)

// setBankDateReq is the body of PUT .../bank-reconciliation/{ledgerID}/{voucherID}/{legNo}.
type setBankDateReq struct {
	// BankDate is the date the BANK cleared the item, ISO yyyy-mm-dd. Required:
	// there is deliberately no "clear it as of today" default, because a
	// reconciliation is an assertion copied off a statement and defaulting it
	// would invent the one fact the whole feature exists to record.
	BankDate  string `json:"bank_date"`
	CreatedBy string `json:"created_by"`
	// StatementLineID is the imported bank row this tick came from — T4 slice
	// 2b. OPTIONAL, and its absence is the ordinary manual reconciliation this
	// route has always served.
	//
	// ⭐ ACCEPTING A PROPOSED MATCH IS THIS ROUTE, NOT A ROUTE OF ITS OWN, and
	// that is slice 2b's load-bearing decision — see
	// banking.SetBankDateFromStatementLine for the four properties that follow
	// (the ACL, the back-dated cutoff, the WebAuthn step-up, invariant B-2).
	//
	// ⛔ THE DATE IS STILL SENT AND STILL REQUIRED even when a line id is
	// present, though the server could read it off that line. Two reasons, and
	// the second is load-bearing:
	//
	//   1. The operator is asserting a date, and an assertion they never see is
	//      not one they made. The screen shows the date it is about to write.
	//   2. peekBankDate — the back-dated cutoff's wire-date probe — reads
	//      `bank_date` FROM THIS BODY. A resolve that carried only a line id
	//      would leave the PDP with no date, cutoffDeny would skip, and a
	//      clearance could be asserted into a filed period. That is byte for
	//      byte the defect routeAction's bank-reconciliation comment records
	//      shipping once already, and it would have shipped again here.
	//
	// The server REFUSES a date that disagrees with the line's own value date
	// rather than preferring either — see handleSetBankDate.
	StatementLineID string `json:"statement_line_id"`
}

// bankReconLineView is the wire shape of one statement line. Money is a
// fixed-4 decimal STRING, like every money value on this API — never a float.
type bankReconLineView struct {
	VoucherID     string `json:"voucher_id"`
	LegNo         int    `json:"leg_no"`
	VoucherNo     string `json:"voucher_no"`
	Class         string `json:"class"`
	VoucherDate   string `json:"voucher_date"`
	Narration     string `json:"narration"`
	Party         string `json:"party"`
	Amount        string `json:"amount"`
	DrCr          string `json:"dr_cr"`
	BankDate      string `json:"bank_date"`
	AgeDays       int    `json:"age_days"`
	BrokenByAlter bool   `json:"broken_by_alter"`
	ClearedAmount string `json:"cleared_amount,omitempty"`
	ClearedDrCr   string `json:"cleared_dr_cr,omitempty"`
}

func viewBankReconLines(in []reporting.BankReconLine) []bankReconLineView {
	out := make([]bankReconLineView, 0, len(in))
	for _, l := range in {
		v := bankReconLineView{
			VoucherID:     l.VoucherID.String(),
			LegNo:         l.LegNo,
			VoucherNo:     l.VoucherNo,
			Class:         l.Class,
			VoucherDate:   l.VoucherDate,
			Narration:     l.Narration,
			Party:         l.Party,
			Amount:        l.Amount.StringFixed(4),
			DrCr:          l.DrCr,
			BankDate:      l.BankDate,
			AgeDays:       l.AgeDays,
			BrokenByAlter: l.BrokenByAlter,
		}
		if l.BrokenByAlter {
			// Present ONLY when broken, where they are the EVIDENCE of what
			// changed: what the operator cleared against, versus what the
			// voucher now says.
			v.ClearedAmount = l.ClearedAmount.StringFixed(4)
			v.ClearedDrCr = l.ClearedDrCr
		}
		out = append(out, v)
	}
	return out
}

// handleBankReconciliation serves the Bank Reconciliation Statement for one bank
// ledger as at an optional `as_on` date.
func (s *Server) handleBankReconciliation(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	ledgerID, err := uuid.Parse(chi.URLParam(r, "ledgerID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid ledgerID")
		return
	}

	brs, err := s.report.BankReconciliationStatement(ctx, companyID, ledgerID, r.URL.Query().Get("as_on"))
	if err != nil {
		// A non-bank ledger, or one that does not exist, is a 400 that NAMES the
		// problem — T1's diagnostic-refusal habit. An empty statement would read
		// as "nothing to reconcile" and send the operator hunting.
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ledger_id":            brs.LedgerID.String(),
		"ledger_name":          brs.LedgerName,
		"as_on":                brs.AsOn,
		"balance_as_per_books": brs.BalanceAsPerBooks.StringFixed(4),
		"uncleared_net":        brs.UnclearedNet.StringFixed(4),
		"balance_as_per_bank":  brs.BalanceAsPerBank.StringFixed(4),
		"difference":           brs.Difference.StringFixed(4),
		"balanced":             brs.IsBalanced(),
		// Non-zero means somebody altered a voucher AFTER reconciling it. A
		// client must surface this; it is not a detail.
		"broken_count": brs.BrokenCount,
		"uncleared":    viewBankReconLines(brs.Uncleared),
		"cleared":      viewBankReconLines(brs.Cleared),
	})
}

// handleSetBankDate records that the bank cleared one leg.
func (s *Server) handleSetBankDate(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	ledgerID, voucherID, legNo, ok := bankReconPathParams(w, r)
	if !ok {
		return
	}
	var req setBankDateReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.BankDate == "" {
		writeError(w, http.StatusBadRequest, "bank_date is required")
		return
	}

	// T4 slice 2b. An ABSENT statement_line_id is the manual reconciliation this
	// route has always served; a PRESENT one additionally records which imported
	// bank row the tick came from. A malformed one is refused rather than
	// ignored: silently dropping it would write the clearance WITHOUT the link,
	// and the operator would be looking at a match that the statement screen
	// still showed as unmatched.
	var err error
	if req.StatementLineID == "" {
		err = s.banking.SetBankDate(ctx, companyID, voucherID, ledgerID, legNo, req.BankDate, attributedTo(r, req.CreatedBy))
	} else {
		lineID, perr := uuid.Parse(req.StatementLineID)
		if perr != nil {
			writeError(w, http.StatusBadRequest, "invalid statement_line_id")
			return
		}
		err = s.banking.SetBankDateFromStatementLine(
			ctx, companyID, voucherID, ledgerID, legNo, req.BankDate, attributedTo(r, req.CreatedBy), lineID)
	}
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"status": "reconciled"})
	case errors.Is(err, bankstatement.ErrLineAlreadyResolved):
		// 409: this bank row already carries a decision. The clearance was NOT
		// written — LinkMatch runs inside the same transaction, so its refusal
		// rolls the tick back with it, which is what stops a failed match from
		// leaving a tick nothing points at.
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, bankstatement.ErrLineNotFound),
		errors.Is(err, bankstatement.ErrDateDisagrees):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, banking.ErrLegNotFound):
		// 404, not 403: the leg genuinely is not on the voucher's current
		// version. 403 would confirm that some other leg exists.
		writeError(w, http.StatusNotFound, err.Error())
	default:
		// Migration 0045's trigger refusals (a non-bank ledger, a bank date
		// before the voucher date) arrive here. They are the CALLER's error and
		// they name the field, so pass the message through as a 400 rather than
		// flattening it to "internal error".
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

// handleClearBankDate un-reconciles one leg. Idempotent by design: clearing an
// already-unreconciled leg is a 200, because the caller's intent is satisfied
// either way and making a double-click an error is friction with no safety gain.
func (s *Server) handleClearBankDate(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	ledgerID, voucherID, legNo, ok := bankReconPathParams(w, r)
	if !ok {
		return
	}
	if err := s.banking.ClearBankDate(ctx, companyID, voucherID, ledgerID, legNo); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "unreconciled"})
}

// bankReconPathParams parses the three path parameters the write routes share,
// writing a 400 and returning ok=false on any malformed one.
func bankReconPathParams(w http.ResponseWriter, r *http.Request) (
	ledgerID, voucherID uuid.UUID, legNo int, ok bool,
) {
	var err error
	if ledgerID, err = uuid.Parse(chi.URLParam(r, "ledgerID")); err != nil {
		writeError(w, http.StatusBadRequest, "invalid ledgerID")
		return uuid.Nil, uuid.Nil, 0, false
	}
	if voucherID, err = uuid.Parse(chi.URLParam(r, "voucherID")); err != nil {
		writeError(w, http.StatusBadRequest, "invalid voucherID")
		return uuid.Nil, uuid.Nil, 0, false
	}
	legNo, err = strconv.Atoi(chi.URLParam(r, "legNo"))
	if err != nil || legNo < 0 {
		writeError(w, http.StatusBadRequest, "invalid legNo")
		return uuid.Nil, uuid.Nil, 0, false
	}
	return ledgerID, voucherID, legNo, true
}
