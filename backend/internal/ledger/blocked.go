package ledger

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrLedgerBlocked is the refusal of a posting to a BLOCKED ledger (migration
// 0095): one the agent created, which no voucher may touch until a person has
// unblocked it.
//
// ⛔ THIS IS THE OPERATOR'S SENTENCE; THE TRIGGER IS THE CONTROL. 0095's
// posting_ledger_not_blocked refuses the INSERT whatever wrote it — a sync
// replay, an import, a future service that forgets this check — but a plpgsql
// exception names a SQLSTATE and not what to do. This check runs first, inside
// the same transaction, and says which ledger and why, so the agent's preview
// (and a human keying a payment to the new party) reads a reason and the way
// out of it rather than a constraint.
var ErrLedgerBlocked = errors.New("ledger: blocked")

// refuseBlockedLedgers refuses a voucher any of whose postings names a blocked
// ledger. One query for the whole voucher, inside the post transaction, so a
// ledger unblocked concurrently is judged at the moment the postings are
// written.
func refuseBlockedLedgers(ctx context.Context, tx pgx.Tx, postings []PostingInput) error {
	if len(postings) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(postings))
	for _, p := range postings {
		ids = append(ids, p.LedgerID)
	}
	var name, reason string
	err := tx.QueryRow(ctx,
		`SELECT name, blocked_reason FROM ledger
		  WHERE id = ANY($1) AND blocked_at IS NOT NULL
		  ORDER BY name LIMIT 1`, ids).Scan(&name, &reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("ledger: read blocked ledgers: %w", err)
	}
	return fmt.Errorf("%w: ledger %q cannot take a posting until a person unblocks it "+
		"(Ledgers, Unblock; %s)", ErrLedgerBlocked, name, reason)
}
