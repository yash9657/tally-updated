package server

import (
	"errors"
	"net/http"

	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/platform/db"
)

// POST /v1/companies/{companyID}/ledgers/{ledgerID}/unblock — the person's half
// of "agent-created parties born blocked" (migration 0095, D121 "Wave 2
// breadth").
//
// ⛔ THE BARE TOKEN `master-unblock`, CLASSED GOVERNANCE. Three consequences,
// each deliberate:
//   - Data Entry is refused with zero acl_rule rows (default_access 'none'), so
//     only an Owner — or a level an owner explicitly grants — can release an
//     agent-made party for payment. `alter master` would not do: it is the
//     authority that CREATES ledgers, and the point is that creating and
//     releasing are different seats.
//   - internal/actions classes it governance, so internal/policy refuses it to
//     every agent structurally, whatever is delegated: the agent can never
//     lift its own block. AgentRules deny the token as well.
//   - It is an Alter, so a level with RequiresWebAuthn must step up.
//
// No body. 200 {"ledger_id", "blocked": false}; 404 unknown ledger; 409 when it
// is not blocked (a second person unblocking the same party is told so rather
// than answered with a success that did nothing).
func (s *Server) handleUnblockLedger(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	ledgerID, ok := pathUUID(w, r, "ledgerID", "ledger id")
	if !ok {
		return
	}
	userID, ok := db.UserFromContext(ctx)
	if !ok || agentCallFrom(ctx) != nil {
		// The router's policy gate already refuses an agent here; this is the
		// same rule said where the block is lifted.
		writeError(w, http.StatusForbidden, "only a person can unblock a ledger")
		return
	}
	err := s.masters.UnblockLedger(ctx, ledgerID, userID, "user:"+userID.String())
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"ledger_id": ledgerID.String(), "blocked": false})
	case errors.Is(err, masters.ErrNotBlocked):
		writeCodedError(w, http.StatusConflict, "NOT_BLOCKED", "this ledger is not blocked")
	default:
		writeMastersError(w, err)
	}
}
