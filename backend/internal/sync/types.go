// Package sync is the OFFLINE cloud-sequencer slice of Task C. A desktop node
// holds a local append-only OUTBOX of voucher intents (each a node-partitioned
// UUIDv7 + a hybrid-logical-clock timestamp + a client-side provisional number).
// On reconnect the outbox COMMITS through the cloud sequencer (Service.Commit),
// which:
//
//   - assigns the FINAL display number via the ledger's atomic per-(company,
//     voucher_type, FY) numbering allocator (so offline CREATEs never collide —
//     each gets a distinct sequential number BY CONSTRUCTION);
//   - resolves document-level conflicts deterministically by a (HLC, NodeID)
//     total order (the loser is PARKED in the reconciliation queue, payload
//     preserved, and SURFACED);
//   - HARD-BLOCKS superseding an e-invoiced/filed voucher by reusing Task B's
//     freeze rule (an ALTER/DELETE of a frozen voucher is refused automatically).
//
// The package imports only ledger + platform/db (archtest-enforced). It never
// imports server or statutory.
//
// This file (types.go, WORKSTREAM 1) declares the SHARED sequencer types — the
// Intent/Outcome/CommitResult wire contract and the HLCTimestamp struct. The HLC
// CLOCK LOGIC lives in hlc.go (WORKSTREAM 2), which references the HLCTimestamp
// struct declared here; declaring the struct here (and the clock there) avoids a
// cross-file ownership cycle while keeping a single package.
package sync

import (
	"github.com/google/uuid"

	"github.com/khata/cored/internal/ledger"
)

// Op is the kind of change an offline node recorded for a voucher: a fresh
// CREATE, an ALTER (supersede the head version), or a DELETE (tombstone). It
// mirrors ledger.Op but is the sync-layer wire value carried on an Intent.
type Op string

const (
	// OpCreate records a brand-new voucher created offline.
	OpCreate Op = "CREATE"
	// OpAlter records an edit superseding the head version.
	OpAlter Op = "ALTER"
	// OpDelete records a tombstone of an existing voucher.
	OpDelete Op = "DELETE"
)

// HLCTimestamp is a hybrid-logical-clock reading: a wall-clock millisecond
// component, a logical counter that breaks ties within the same millisecond, and
// the originating node id. The total order over timestamps is
// (WallMillis, Counter, NodeID) ascending — see LessHLC in hlc.go (WS2). The
// struct is declared HERE (WS1) so both types.go and hlc.go reference one type;
// the clock that PRODUCES these values lives in hlc.go.
type HLCTimestamp struct {
	WallMillis int64
	Counter    int64
	NodeID     string
}

// Intent is one outbox entry from an offline node: a single recorded voucher
// change awaiting commit through the cloud sequencer.
type Intent struct {
	// IdempotencyKey is the exactly-once key. It is node-scoped and STABLE across
	// replays of the same logical change, so re-committing an outbox is a no-op.
	IdempotencyKey string
	// ClientVoucherID is the node-partitioned UUIDv7 minted offline. It IS the
	// committed identity (the ledger accepts a caller-supplied voucher id).
	ClientVoucherID uuid.UUID
	// Op is the change kind (CREATE / ALTER / DELETE).
	Op Op
	// Class is the accounting class (drives default voucher type + validation).
	Class ledger.Class
	// ProvisionalNo is the client-side 'INV/<fy>/PROV-<node>-<seq>' display
	// number. It is NEVER sent as the voucher_no — the server allocates the real
	// number — it is carried only so the loser of a conflict can be surfaced with
	// the label the user saw offline.
	ProvisionalNo string
	// HLC is the node's hybrid-logical-clock reading at the time of the change.
	// The cloud sequencer sorts intents by (HLC, NodeID) to pick a deterministic,
	// reproducible winner among conflicting changes.
	HLC HLCTimestamp
	// NodeID identifies the originating offline node.
	NodeID string
	// BaseVersion is the head version_no the node based an ALTER/DELETE on (0 for
	// a CREATE). The sequencer compares it against the live head to detect a
	// stale-base conflict.
	BaseVersion int
	// Payload carries the voucher body (postings/date/party/gst/etc.). Its
	// VoucherNo is left EMPTY; the server numbers the committed voucher.
	Payload ledger.VoucherInput
}

// OutcomeStatus is the disposition of one committed intent.
type OutcomeStatus string

const (
	// StatusCommitted: the intent applied (CREATE minted a new voucher + final
	// number, ALTER/DELETE moved the head forward).
	StatusCommitted OutcomeStatus = "COMMITTED"
	// StatusConflict: a stale-base or no-live-head ALTER/DELETE was refused and
	// the loser was PARKED in the reconciliation queue (payload preserved).
	StatusConflict OutcomeStatus = "CONFLICT"
	// StatusFrozen: the target voucher is pinned by an IRN / filed GSTR-1; the
	// supersede was HARD-BLOCKED (the ledger already raised the freeze recon row).
	StatusFrozen OutcomeStatus = "FROZEN"
	// StatusDuplicate: this idempotency key (or this CREATE id) was already
	// applied; the intent was not re-applied (exactly-once replay).
	StatusDuplicate OutcomeStatus = "DUPLICATE"
)

// Outcome is the per-intent result the sequencer returns to the node.
type Outcome struct {
	// IdempotencyKey echoes the intent's key so the node can match outcomes back.
	IdempotencyKey string
	// ClientVoucherID echoes the intent's voucher id.
	ClientVoucherID uuid.UUID
	// Status is the disposition (COMMITTED / CONFLICT / FROZEN / DUPLICATE).
	Status OutcomeStatus
	// FinalVoucherNo is the server-assigned number on a COMMITTED CREATE, or the
	// carried-forward number on a COMMITTED/DUPLICATE ALTER. Empty otherwise.
	FinalVoucherNo string
	// VersionNo is the head version after applying (or the existing head version
	// on a DUPLICATE).
	VersionNo int
	// ReconciliationID is set on CONFLICT (the parked loser's queue row) and on
	// FROZEN (the freeze queue row the ledger raised).
	ReconciliationID string
	// Detail is a human-readable explanation (e.g. the stale base vs head).
	Detail string
}

// CommitResult is the batch result: one Outcome per submitted intent, in the
// caller's submission order (NOT the internal sorted order).
type CommitResult struct {
	Outcomes []Outcome
}
