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

// ErrLedgerNotInCompany is the refusal of a posting that names a ledger this
// company does not have.
//
// ⛔ posting.ledger_id's FK is SINGLE-COLUMN (ledger(id), not (company_id, id)),
// so the database alone accepted a voucher in company A whose legs pointed at
// company B's ledgers: 201, two postings under A referencing ids A's own RLS
// cannot see, ledger_balance rows for them, and every report that joins
// posting to ledger silently dropping both legs. Found at the wave-2 merge by
// W6's cross-tenant MCP gate (a human path did the same); the composite FK
// that would make it structural needs a migration and is OWED
// (HANDOFF-AGENTIC-WAVE-2 §4). Until then this check is the control: the
// SELECT runs under the post transaction's RLS scope AND names the company,
// so another tenant's ledger is simply absent.
var ErrLedgerNotInCompany = errors.New("ledger: no such ledger in this company")

// refuseBlockedLedgers refuses a voucher any of whose postings names a ledger
// this company does not have (ErrLedgerNotInCompany) or a blocked ledger. One query for the whole voucher, inside the post transaction, so a
// ledger unblocked concurrently is judged at the moment the postings are
// written.
func refuseBlockedLedgers(ctx context.Context, tx pgx.Tx, companyID uuid.UUID, postings []PostingInput) error {
	if len(postings) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(postings))
	seen := map[uuid.UUID]bool{}
	for _, p := range postings {
		ids = append(ids, p.LedgerID)
		seen[p.LedgerID] = true
	}
	var found int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM ledger WHERE company_id = $1 AND id = ANY($2)`,
		companyID, ids).Scan(&found); err != nil {
		return fmt.Errorf("ledger: read posting ledgers: %w", err)
	}
	if found != len(seen) {
		return fmt.Errorf("%w: %d of the %d ledger(s) this voucher posts to do not exist in this company",
			ErrLedgerNotInCompany, len(seen)-found, len(seen))
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
