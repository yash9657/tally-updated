package server

// HTTP surface for WHERE REALISED EXCHANGE DIFFERENCES GO — T8 step 5, migration 0080.
//
//	GET .../forex-settings  -> Display / forex-settings
//	PUT .../forex-settings  -> Alter   / forex-settings
//
// ⛔ THIS IS THE ROUTE WHOSE ABSENCE MADE STEP 5 UNREACHABLE. ledger's settlement
// engine refuses any voucher that would realise an exchange difference at a company
// with no gain/loss ledger chosen, naming the setting — and until this existed the
// setting could only be written with raw SQL (which is exactly how the ledger tests
// seed it). A refusal an operator cannot satisfy is an outage, not a guard.
//
// ---------------------------------------------------------------------------
// PUT WITH NO KEY IN THE PATH, AND THE WHOLE SETTING IN THE BODY
// ---------------------------------------------------------------------------
// The company IS the key (company_forex_settings.company_id is its whole primary
// key) and the company already comes from the path prefix and the tenant context, so
// there is nothing left to name. It is an Alter rather than a Create for the reason
// tds-profile is: restating the choice CORRECTS the one row, it never adds a second.
//
// ⭐ AND AN EMPTY LEDGER ID CLEARS THE CHOICE rather than being rejected as a missing
// field. 0080 made the column nullable on purpose so that "not chosen" stays
// expressible, and an operator who picked the wrong ledger must be able to un-pick it
// without a DELETE route that would also destroy any future setting on the row.
//
// ---------------------------------------------------------------------------
// ONE RESOURCE TOKEN FOR THE READ AND THE WRITE, AND A BARE ONE
// ---------------------------------------------------------------------------
// ⛔ NOT `report:forex-settings`, AND THE REASON IS MIGRATION 0011, NOT TASTE.
// internal/authz matches 0011's coarse `allow display report` against ANY report name
// at specificity 1 (defect D95), so the moment this became a report token the read
// would be GRANTED to every seeded Data Entry level by a rule written years ago for a
// different purpose — and taking it back needs a 0068-style counterweight migration.
// As a bare token both halves are default-DENIED for Data Entry (default_access
// 'none', and no seeded rule names it), which is the authority a setting that decides
// where a company's realised gains land should have, WITH NO ACL MIGRATION AT ALL.
// handler_tds_profile.go reaches the same answer for the same reason, and
// TestForexSettingsAuthMatrix pins it.
//
// ⚠️ THE READ IS NOT SENSITIVE THE WAY tds-profile's IS — no personal data — but it
// shares the token anyway, because a level that may not CHOOSE the ledger has no use
// for knowing which one is chosen, and two tokens would let a rule grant the read and
// then read as though it granted the write.

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/masters"
)

// forexSettingsBody is the setting, both ways.
//
// ⚠️ THE LEDGER ID IS A STRING AND NOT A uuid.UUID, so that "" can mean CLEARED. A
// uuid.UUID field would decode a missing key as the nil uuid and be indistinguishable
// from an explicit clear — which is fine here only because the two mean the SAME
// thing, and is written down so the next field added to this struct does not inherit
// the assumption.
type forexSettingsBody struct {
	GainLossLedgerID   string `json:"gain_loss_ledger_id"`
	GainLossLedgerName string `json:"gain_loss_ledger_name,omitempty"`
}

func (s *Server) handleGetForexSettings(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	set, err := s.masters.ForexSettings(ctx)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	out := forexSettingsBody{GainLossLedgerName: set.GainLossLedgerName}
	// ⛔ ABSENT, NOT THE NIL UUID. "00000000-0000-0000-0000-000000000000" is a
	// well-formed uuid that a client would round-trip straight back into the PUT, and
	// the setter would then have to special-case it; an empty string is what the body
	// documents as "cleared" and it is what the read emits.
	if set.GainLossLedgerID != uuid.Nil {
		out.GainLossLedgerID = set.GainLossLedgerID.String()
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSetForexSettings(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	var req forexSettingsBody
	if !decodeJSON(w, r, &req) {
		return
	}
	// An empty id CLEARS the choice; anything else must parse.
	ledgerID := uuid.Nil
	if req.GainLossLedgerID != "" {
		parsed, err := uuid.Parse(req.GainLossLedgerID)
		if err != nil {
			writeError(w, http.StatusBadRequest,
				"invalid gain_loss_ledger_id (send a ledger uuid, or \"\" to clear the choice)")
			return
		}
		ledgerID = parsed
	}
	if err := s.masters.SetForexSettings(ctx, ledgerID); err != nil {
		// 422 rather than 400: the body was well-formed and the REQUEST was refused
		// on a domain rule — the ledger is not one a gain or loss can go to. The
		// sentence names what to choose instead.
		if errors.Is(err, masters.ErrForexLedgerUnsuitable) {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
