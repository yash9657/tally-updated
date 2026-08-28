package server

// HTTP surface for T4 slice 2b — bank statement MATCHING.
//
// Design memo: docs/design/t4-slice2-model.md §0, §1.2, §6. Migration 0088.
//
// ===========================================================================
// THREE ROUTES, AND THE MOST IMPORTANT ONE IS NOT HERE
// ===========================================================================
//
//	GET  .../bank-statement/{ledgerID}/matches            -> Display/report:bank-statement
//	POST .../bank-statement/{ledgerID}/lines/{lineID}/set-aside -> Create/bank-statement
//	POST .../bank-statement/{ledgerID}/lines/{lineID}/restore   -> Create/bank-statement
//
// ⭐ ACCEPTING A MATCH IS NOT ONE OF THEM. It is the EXISTING
// PUT .../bank-reconciliation/{ledgerID}/{voucherID}/{legNo}, which slice 2b
// extends with an optional `statement_line_id` rather than adding a route of its
// own. banking.SetBankDateFromStatementLine's doc has the four properties that
// follow from sharing it — the ACL, the back-dated cutoff, the WebAuthn step-up
// and invariant B-2 — and the one-sentence version is that matching must not
// become a SECOND WAY TO ACQUIRE THE AUTHORITY TO ASSERT A CLEARANCE.
//
// ⚠️ ALL THREE ROUTES HERE ARE MAPPED BY routeAction's EXISTING
// `/bank-statement/` PREFIX ARM, so slice 2b writes no ACL migration. Migration
// 0088's footer records why that is a decision rather than an omission.
//
// BOTH WRITES ARE POST, INCLUDING `restore`, WHICH UNDOES ONE. A DELETE on this
// prefix falls through routeAction to default-deny, and widening that arm to
// carry a Delete action would give the prefix an authority nobody chose for it —
// the shape by which a route silently acquires one. `restore` is a write to the
// statement's annotations, governed by the authority that governs the statement,
// and the method it uses is not what decides that.

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/khata/cored/internal/bankstatement"
)

// candidateView is one leg the matcher believes a bank row might be.
type candidateView struct {
	VoucherID   string `json:"voucher_id"`
	LegNo       int    `json:"leg_no"`
	VoucherNo   string `json:"voucher_no"`
	Class       string `json:"class"`
	VoucherDate string `json:"voucher_date"`
	Narration   string `json:"narration"`
	Party       string `json:"party"`
	// Money is a fixed-4 decimal STRING, like every money value on this API.
	Amount string `json:"amount"`
	DrCr   string `json:"dr_cr"`
	Tier   string `json:"tier"`
	// Why is the evidence in the operator's words. It travels because a
	// candidate offered without a reason is a machine asking to be trusted.
	Why         string   `json:"why"`
	PartialWith []string `json:"partial_with,omitempty"`
}

// lineProposalView is one bank row and what the matcher found for it.
type lineProposalView struct {
	LineID        string `json:"line_id"`
	LineNo        int    `json:"line_no"`
	ValueDate     string `json:"value_date"`
	Description   string `json:"description"`
	Amount        string `json:"amount"`
	DrCr          string `json:"dr_cr"`
	BankReference string `json:"bank_reference,omitempty"`
	ChequeNumber  string `json:"cheque_number,omitempty"`

	BestTier   string          `json:"best_tier"`
	Candidates []candidateView `json:"candidates"`
	// Ambiguous is invariant B-1 on the wire. A client MUST NOT offer a
	// one-key acceptance for a line carrying it.
	Ambiguous   bool  `json:"ambiguous"`
	ContestedBy []int `json:"contested_by,omitempty"`
	// AcceptableInBulk is the ONLY field the bulk key may read.
	AcceptableInBulk bool `json:"acceptable_in_bulk"`
}

// resolutionView is one decision already recorded.
type resolutionView struct {
	LineID string `json:"line_id"`
	Status string `json:"status"`
	Tier   string `json:"tier"`
	Note   string `json:"note,omitempty"`
	By     string `json:"matched_by"`
	At     string `json:"matched_at"`

	VoucherID string `json:"voucher_id,omitempty"`
	LegNo     int    `json:"leg_no,omitempty"`
	VoucherNo string `json:"voucher_no,omitempty"`
	Class     string `json:"class,omitempty"`
	BankDate  string `json:"bank_date,omitempty"`
	// BankDateDiffers means somebody retyped the Bank Date cell after matching,
	// so the tick and the statement row no longer agree about WHEN. A client
	// must surface it; see bankstatement.Resolution.BankDateDiffers.
	BankDateDiffers bool   `json:"bank_date_differs,omitempty"`
	LineValueDate   string `json:"line_value_date,omitempty"`
}

// handleBankStatementMatches derives the match proposals for one bank account.
//
// IT WRITES NOTHING, and there is no parameter that would make it write.
func (s *Server) handleBankStatementMatches(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	ledgerID, err := uuid.Parse(chi.URLParam(r, "ledgerID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid ledgerID")
		return
	}

	// The window is an OPTIONAL search width. A malformed one is refused rather
	// than silently defaulted: an operator who typed it meant something by it,
	// and quietly using 7 instead would show them a different answer from the
	// one they asked for while looking like the one they asked for.
	window := 0
	if raw := r.URL.Query().Get("window_days"); raw != "" {
		window, err = strconv.Atoi(raw)
		if err != nil || window < 0 {
			writeError(w, http.StatusBadRequest,
				"window_days must be a non-negative whole number of days")
			return
		}
	}

	view, err := s.bankstmt.MatchReport(ctx, companyID, ledgerID,
		r.URL.Query().Get("from"), r.URL.Query().Get("to"), window)
	if err != nil {
		// A non-bank ledger, or one that does not exist, is a 400 that NAMES the
		// problem. An empty proposal set would read as "nothing to match".
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	proposals := make([]lineProposalView, 0, len(view.Proposals.Lines))
	for _, lp := range view.Proposals.Lines {
		cands := make([]candidateView, 0, len(lp.Candidates))
		for _, c := range lp.Candidates {
			cands = append(cands, candidateView{
				VoucherID:   c.Leg.VoucherID.String(),
				LegNo:       c.Leg.LegNo,
				VoucherNo:   c.Leg.VoucherNo,
				Class:       c.Leg.Class,
				VoucherDate: c.Leg.VoucherDate.Format("2006-01-02"),
				Narration:   c.Leg.Narration,
				Party:       c.Leg.Party,
				Amount:      c.Leg.Amount.StringFixed(4),
				DrCr:        c.Leg.DrCr,
				Tier:        string(c.Tier),
				Why:         c.Why,
				PartialWith: c.PartialWith,
			})
		}
		proposals = append(proposals, lineProposalView{
			LineID:           lp.Line.ID.String(),
			LineNo:           lp.Line.LineNo,
			ValueDate:        lp.Line.ValueDate.Format("2006-01-02"),
			Description:      lp.Line.Description,
			Amount:           lp.Line.Amount.StringFixed(4),
			DrCr:             lp.Line.DrCr,
			BankReference:    lp.Line.BankReference,
			ChequeNumber:     lp.Line.ChequeNumber,
			BestTier:         string(lp.BestTier),
			Candidates:       cands,
			Ambiguous:        lp.Ambiguous,
			ContestedBy:      lp.ContestedBy,
			AcceptableInBulk: lp.AcceptableInBulk,
		})
	}

	unmatchedLegs := make([]candidateView, 0, len(view.Proposals.UnmatchedLegs))
	for _, l := range view.Proposals.UnmatchedLegs {
		unmatchedLegs = append(unmatchedLegs, candidateView{
			VoucherID:   l.VoucherID.String(),
			LegNo:       l.LegNo,
			VoucherNo:   l.VoucherNo,
			Class:       l.Class,
			VoucherDate: l.VoucherDate.Format("2006-01-02"),
			Narration:   l.Narration,
			Party:       l.Party,
			Amount:      l.Amount.StringFixed(4),
			DrCr:        l.DrCr,
			// No tier: this leg was offered to nobody. Leaving Tier empty rather
			// than inventing a word for it is the same call the report layer
			// makes everywhere — a blank that means "not applicable" must not be
			// filled with a value that means something else.
		})
	}

	resolved := make([]resolutionView, 0, len(view.Resolved))
	for _, r := range view.Resolved {
		rv := resolutionView{
			LineID: r.LineID.String(),
			Status: r.Status,
			Tier:   string(r.Tier),
			Note:   r.Note,
			By:     r.By,
			At:     r.At,
			LegNo:  r.LegNo,

			VoucherNo:       r.VoucherNo,
			Class:           r.Class,
			BankDate:        r.BankDate,
			BankDateDiffers: r.BankDateDiffers,
			LineValueDate:   r.LineValueDate,
		}
		if r.VoucherID != uuid.Nil {
			rv.VoucherID = r.VoucherID.String()
		}
		resolved = append(resolved, rv)
	}

	c := view.Counts
	writeJSON(w, http.StatusOK, map[string]any{
		"ledger_id":        view.LedgerID.String(),
		"ledger_name":      view.LedgerName,
		"from":             view.From,
		"to":               view.To,
		"date_window_days": view.Proposals.DateWindowDays,
		"proposals":        proposals,
		// Books-only unreconciled (RESEARCH.md:873) — a cheque written and not
		// yet presented is the ordinary member, so it is a CATEGORY, not an error.
		"unmatched_legs": unmatchedLegs,
		"resolved":       resolved,
		// The exception report (Ctrl+J) is these counts over the same payload
		// rather than a second endpoint: two routes answering "what is
		// outstanding?" are two answers that can disagree.
		"counts": map[string]int{
			"lines":              c.Lines,
			"exact":              c.Exact,
			"potential":          c.Potential,
			"partial":            c.Partial,
			"ambiguous":          c.Ambiguous,
			"no_candidate":       c.NoCandidate,
			"acceptable_in_bulk": c.AcceptableInBulk,
			"unmatched_legs":     c.UnmatchedLegs,
		},
		// ⛔ THE DISCLOSURES ARE PART OF THE ANSWER. The first one says nothing
		// here has been reconciled, which is this screen's most dangerous
		// possible misreading.
		"disclosures": view.Proposals.Disclosures,
	})
}

// setAsideReq is the body of the set-aside POST.
type setAsideReq struct {
	// Note is why this bank row corresponds to nothing — "bank charge, no
	// voucher raised". Optional, and worth having: a set-aside with no reason
	// is indistinguishable next March from one made by mistake.
	Note      string `json:"note"`
	CreatedBy string `json:"created_by"`
}

// handleSetAsideStatementLine records that a bank row matches NOTHING in the
// books — the "bank-only unreconciled" category (RESEARCH.md:873).
//
// ⛔ IT WRITES NO CLEARANCE AND MOVES NO BALANCE. A set-aside is not a weaker
// match, and a client that rendered it as one would be asserting that the bank
// and the books agree about a row for which no voucher exists.
func (s *Server) handleSetAsideStatementLine(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	ledgerID, lineID, ok := statementLinePathParams(w, r)
	if !ok {
		return
	}
	var req setAsideReq
	if !decodeJSON(w, r, &req) {
		return
	}

	err := s.bankstmt.SetAside(ctx, companyID, ledgerID, lineID, req.Note, req.CreatedBy)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"status": "set-aside"})
	case errors.Is(err, bankstatement.ErrLineNotFound):
		// 404, not 403: the line genuinely is not on this account. A 403 would
		// confirm that some other line exists.
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, bankstatement.ErrLineAlreadyResolved):
		// 409, because the caller's intent CANNOT be satisfied — this line
		// already carries a decision, and silently replacing it would discard
		// an assertion a person made without telling them.
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

// handleRestoreStatementLine undoes a set-aside, returning the row to the
// unmatched list.
//
// ⛔ IT REFUSES A row THAT IS MATCHED RATHER THAN DELETING IT, and says which
// key withdraws a match instead. See bankstatement.UndoSetAside: deleting a
// matched row here would leave the clearance behind with nothing pointing at it,
// so the leg would stay ticked while the same bank row returned to the unmatched
// list and could be reconciled a second time against a second voucher — and both
// would look right.
func (s *Server) handleRestoreStatementLine(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	ledgerID, lineID, ok := statementLinePathParams(w, r)
	if !ok {
		return
	}

	err := s.bankstmt.UndoSetAside(ctx, companyID, ledgerID, lineID)
	switch {
	case err == nil:
		// Idempotent: restoring a line that was never set aside is a 200,
		// because the caller's intent is satisfied either way.
		writeJSON(w, http.StatusOK, map[string]any{"status": "unmatched"})
	case errors.Is(err, bankstatement.ErrLineNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

// statementLinePathParams parses the two path parameters the set-aside routes
// share, writing a 400 and returning ok=false on either malformed one.
func statementLinePathParams(w http.ResponseWriter, r *http.Request) (
	ledgerID, lineID uuid.UUID, ok bool,
) {
	var err error
	if ledgerID, err = uuid.Parse(chi.URLParam(r, "ledgerID")); err != nil {
		writeError(w, http.StatusBadRequest, "invalid ledgerID")
		return uuid.Nil, uuid.Nil, false
	}
	if lineID, err = uuid.Parse(chi.URLParam(r, "lineID")); err != nil {
		writeError(w, http.StatusBadRequest, "invalid lineID")
		return uuid.Nil, uuid.Nil, false
	}
	return ledgerID, lineID, true
}
