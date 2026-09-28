package masters

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/platform/db"
)

// ledger_block.go is the human half of "agent-created parties born blocked"
// (PLAN-AGENTIC W2, migration 0095): the unblock.
//
// ⛔ WHY A SEPARATE ACT AND NOT PART OF THE APPROVAL. Approving the agent's
// "create ledger Fraudster Two" is a decision about whether the master should
// EXIST; unblocking it is a decision about whether it may be PAID. Wave 1 had
// only the first, which meant that the one hurried click on a plausible new
// supplier also made it payable. Two acts, possibly two people, and the second
// one looks at the bank details a forged letter would have changed.

// BornBlockedReason is the reason an agent-created ledger carries until it is
// unblocked. It names the run, so the person unblocking it can open the run
// and read what the agent was asked and what it was shown.
func BornBlockedReason(runID uuid.UUID) string {
	return fmt.Sprintf("created by the agent in run %s; a person must review it and unblock it", runID)
}

// ErrNotBlocked is an unblock of a ledger that is not blocked.
var ErrNotBlocked = errors.New("masters: ledger is not blocked")

// UnblockLedger lifts a ledger's born-blocked state and appends a master_audit
// ALTER row recording who lifted it. by is the person's user id (stored on the
// ledger); byName is the audit's created_by text.
//
// The UPDATE is conditioned on blocked_at IS NOT NULL, so two people
// unblocking at once produce one unblock and one ErrNotBlocked, never two
// audit rows for one act.
func (s *Service) UnblockLedger(ctx context.Context, ledgerID, by uuid.UUID, byName string) error {
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return db.ErrNoCompany
	}
	if by == uuid.Nil {
		return errors.New("masters: an unblock needs the person who made it")
	}
	return s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var name, reason string
		var run *uuid.UUID
		err := tx.QueryRow(ctx,
			`UPDATE ledger l SET blocked_at = NULL, blocked_reason = NULL,
			        unblocked_by = $2, unblocked_at = now()
			   FROM ledger old
			  WHERE l.id = $1 AND old.id = l.id AND l.blocked_at IS NOT NULL
			 RETURNING l.name, old.blocked_reason, l.created_by_run`, ledgerID, by).Scan(&name, &reason, &run)
		if errors.Is(err, pgx.ErrNoRows) {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ledger WHERE id = $1)`, ledgerID).Scan(&exists); err != nil {
				return fmt.Errorf("masters: read ledger: %w", err)
			}
			if !exists {
				return fmt.Errorf("masters: %w: ledger %s", ErrNotFound, ledgerID)
			}
			return ErrNotBlocked
		}
		if err != nil {
			return fmt.Errorf("masters: unblock ledger: %w", err)
		}
		before := map[string]any{"name": name, "blocked": true, "blocked_reason": reason}
		after := map[string]any{"name": name, "blocked": false, "unblocked_by": by.String()}
		if run != nil {
			after["created_by_run"] = run.String()
		}
		return appendAudit(ctx, tx, companyID, "ledger", ledgerID, OpAlter, before, after, byName)
	})
}
