package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/platform/db"
)

// Service is the cloud sequencer. It commits batches of offline node intents
// through the ledger, assigning final numbers, resolving conflicts by a total
// (HLC, NodeID) order, and reusing Task B's freeze rule to hard-block
// superseding an e-invoiced/filed voucher. It holds the ledger Service for the
// post/alter/delete/head-version/freeze paths and the pool for its OWN
// tenant-scoped reads/writes of sync_applied + reconciliation_queue.
type Service struct {
	ledger *ledger.Service
	pool   *db.Pool
}

// NewService constructs a sequencer over the ledger Service and the (shared,
// tenant-scoped) connection pool.
func NewService(led *ledger.Service, pool *db.Pool) *Service {
	return &Service{ledger: led, pool: pool}
}

// Commit applies a batch of offline outbox intents through the cloud sequencer.
//
// Algorithm (the correctness core):
//
//  1. Sort a COPY of the intents by (HLC, NodeID) ascending — this is the cloud
//     total-order tiebreak, so the deterministic conflict WINNER is reproducible.
//  2. Process intents STRICTLY SEQUENTIALLY in that order (never parallelised):
//     a. Exactly-once: if sync_applied already has (company, idempotency_key),
//        return the stored outcome as StatusDuplicate (do NOT re-apply). A whole
//        re-Commit of the same batch is therefore a no-op.
//     b. OpCreate: if a head already exists for ClientVoucherID -> StatusDuplicate
//        (replay-safe even if a prior sync_applied insert was lost). Else
//        PostVoucherWithID with an EMPTY VoucherNo -> the allocator mints the
//        FINAL sequential number; StatusCommitted.
//     c. OpAlter/OpDelete: read HeadVersion(ClientVoucherID).
//          - no live head        -> CONFLICT "no live head" (park the loser);
//          - BaseVersion != head  -> STALE CONFLICT (park the loser);
//          - else apply Alter/Delete. *ledger.FrozenError -> StatusFrozen (the
//            ledger already raised the freeze recon row); else StatusCommitted.
//     d. Record sync_applied(company, idempotency_key, ...).
//
// Boundary: each intent's apply is its own ledger transaction and the
// sync_applied insert is a separate tenant-scoped transaction. The sequencer is
// therefore at-least-once; the sync_applied + head-exists dedupe (steps a + b)
// makes the whole thing exactly-once and crash-resumable: a crash between an
// apply and its sync_applied insert leaves a committed voucher whose CREATE
// replay is caught by the head-exists guard (and ALTER/DELETE replay by the
// BaseVersion check), so re-Commit is safe.
//
// Outcomes are returned in the caller's ORIGINAL submission order (not the
// internal sorted order), keyed back by idempotency key.
func (s *Service) Commit(ctx context.Context, intents []Intent) (CommitResult, error) {
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return CommitResult{}, db.ErrNoCompany
	}

	// Sort a COPY by (HLC, NodeID): the deterministic total order. The caller's
	// slice and submission order are preserved for the returned outcomes.
	order := make([]int, len(intents))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		ia, ib := intents[order[a]], intents[order[b]]
		if lessHLC(ia.HLC, ib.HLC) {
			return true
		}
		if lessHLC(ib.HLC, ia.HLC) {
			return false
		}
		return ia.NodeID < ib.NodeID
	})

	outcomes := make([]Outcome, len(intents))
	for _, idx := range order {
		in := intents[idx]
		out, err := s.applyOne(ctx, companyID, in)
		if err != nil {
			return CommitResult{}, err
		}
		outcomes[idx] = out
	}
	return CommitResult{Outcomes: outcomes}, nil
}

// applyOne processes a single intent in the sorted sequence. It is invoked
// strictly sequentially so the (HLC,node)-min winner is applied (moving the head
// to v2) BEFORE any later same-base intent is checked — that ordering is what
// makes the winner deterministic and every later same-base intent a CONFLICT.
func (s *Service) applyOne(ctx context.Context, companyID uuid.UUID, in Intent) (Outcome, error) {
	// (a) Exactly-once: a recorded idempotency key short-circuits to the stored
	// outcome. Re-Committing the same batch is a no-op.
	if prior, found, err := s.lookupApplied(ctx, companyID, in.IdempotencyKey); err != nil {
		return Outcome{}, err
	} else if found {
		prior.IdempotencyKey = in.IdempotencyKey
		prior.ClientVoucherID = in.ClientVoucherID
		prior.Status = StatusDuplicate
		return prior, nil
	}

	switch in.Op {
	case OpCreate:
		return s.applyCreate(ctx, companyID, in)
	case OpAlter, OpDelete:
		return s.applyEdit(ctx, companyID, in)
	default:
		return Outcome{}, fmt.Errorf("sync: unknown op %q", in.Op)
	}
}

// applyCreate commits a fresh offline CREATE. It first checks for an existing
// head under the client id: a present head means this CREATE already applied
// (replay-safe even if sync_applied was lost), so it returns StatusDuplicate and
// NEVER re-posts (re-posting an existing id would mint an ALTER-like version 2).
// Otherwise it posts with an EMPTY VoucherNo so the allocator mints the final
// sequential number, guaranteeing zero collisions across nodes.
func (s *Service) applyCreate(ctx context.Context, companyID uuid.UUID, in Intent) (Outcome, error) {
	// head-exists guard (handles any version, live or tombstoned would also count
	// as already-created; HeadVersion only returns live heads, so additionally
	// probe a raw head row to be fully replay-safe).
	if no, ver, exists, err := s.headAny(ctx, companyID, in.ClientVoucherID); err != nil {
		return Outcome{}, err
	} else if exists {
		out := Outcome{
			IdempotencyKey:  in.IdempotencyKey,
			ClientVoucherID: in.ClientVoucherID,
			Status:          StatusDuplicate,
			FinalVoucherNo:  no,
			VersionNo:       ver,
			Detail:          "create already applied",
		}
		if err := s.recordApplied(ctx, companyID, in, out); err != nil {
			return Outcome{}, err
		}
		return out, nil
	}

	payload := in.Payload
	payload.Class = in.Class
	payload.VoucherNo = "" // server assigns the FINAL number
	res, err := s.ledger.PostVoucherWithID(ctx, in.ClientVoucherID, payload)
	if err != nil {
		return Outcome{}, fmt.Errorf("sync: post create %s: %w", in.ClientVoucherID, err)
	}
	out := Outcome{
		IdempotencyKey:  in.IdempotencyKey,
		ClientVoucherID: in.ClientVoucherID,
		Status:          StatusCommitted,
		FinalVoucherNo:  res.VoucherNo,
		VersionNo:       res.VersionNo,
		Detail:          "created",
	}
	if err := s.recordApplied(ctx, companyID, in, out); err != nil {
		return Outcome{}, err
	}
	return out, nil
}

// applyEdit commits an ALTER/DELETE intent. It reads the live head, parks the
// loser on a stale-base or no-live-head conflict, applies otherwise, and maps a
// *FrozenError to StatusFrozen (the freeze recon row was already raised by the
// ledger).
func (s *Service) applyEdit(ctx context.Context, companyID uuid.UUID, in Intent) (Outcome, error) {
	headVer, exists, err := s.ledger.HeadVersion(ctx, in.ClientVoucherID)
	if err != nil {
		return Outcome{}, fmt.Errorf("sync: head version %s: %w", in.ClientVoucherID, err)
	}

	// CONFLICT sub-case 1: no live head (unknown or tombstoned). Park with an
	// empty frozen_voucher_no (there is no current number to record).
	if !exists {
		return s.parkConflict(ctx, companyID, in, "", "no live head")
	}
	// CONFLICT sub-case 2: stale base. The node based its edit on a version the
	// head has moved past (the (HLC,node)-min winner already advanced it).
	if in.BaseVersion != headVer {
		curNo, _ := s.headVoucherNo(ctx, companyID, in.ClientVoucherID)
		detail := fmt.Sprintf("CONFLICT: stale base v%d != head v%d", in.BaseVersion, headVer)
		return s.parkConflict(ctx, companyID, in, curNo, detail)
	}

	// Base matches the head: apply the edit. A frozen target is hard-blocked by
	// the ledger (Task B) which returns *FrozenError after raising the freeze
	// recon row.
	var res ledger.PostResult
	if in.Op == OpDelete {
		res, err = s.ledger.DeleteVoucher(ctx, in.ClientVoucherID, in.Payload.CreatedBy)
	} else {
		payload := in.Payload
		payload.Class = in.Class
		res, err = s.ledger.AlterVoucher(ctx, in.ClientVoucherID, payload)
	}
	if err != nil {
		var fe *ledger.FrozenError
		if errors.As(err, &fe) {
			return s.frozenOutcome(ctx, companyID, in, fe)
		}
		return Outcome{}, fmt.Errorf("sync: apply %s %s: %w", in.Op, in.ClientVoucherID, err)
	}

	out := Outcome{
		IdempotencyKey:  in.IdempotencyKey,
		ClientVoucherID: in.ClientVoucherID,
		Status:          StatusCommitted,
		FinalVoucherNo:  res.VoucherNo,
		VersionNo:       res.VersionNo,
		Detail:          string(in.Op) + " applied",
	}
	if err := s.recordApplied(ctx, companyID, in, out); err != nil {
		return Outcome{}, err
	}
	return out, nil
}

// parkConflict inserts a reconciliation_queue row preserving the losing intent's
// full payload (the new 0010 JSONB column) and returns a StatusConflict outcome
// carrying the row id. frozenNo is the current head's display number for the
// stale-base case, or "" for the no-live-head case (the column is NOT NULL but
// an empty string is valid). attempted_op is the intent's Op (ALTER or DELETE —
// a CONFLICT loser is never a CREATE), satisfying the CHECK IN('ALTER','DELETE').
func (s *Service) parkConflict(ctx context.Context, companyID uuid.UUID, in Intent, frozenNo, detail string) (Outcome, error) {
	payloadJSON, err := json.Marshal(in)
	if err != nil {
		return Outcome{}, fmt.Errorf("sync: marshal conflict payload: %w", err)
	}
	var reconID uuid.UUID
	err = s.pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`INSERT INTO reconciliation_queue
			        (company_id, voucher_id, attempted_op, kind, ref_value,
			         frozen_voucher_no, detail, payload)
			 VALUES ($1, $2, $3, 'CONFLICT', $4, $5, $6, $7)
			 RETURNING id`,
			companyID, in.ClientVoucherID, string(in.Op), in.NodeID,
			frozenNo, detail, payloadJSON).Scan(&reconID)
	})
	if err != nil {
		return Outcome{}, fmt.Errorf("sync: park conflict: %w", err)
	}
	out := Outcome{
		IdempotencyKey:   in.IdempotencyKey,
		ClientVoucherID:  in.ClientVoucherID,
		Status:           StatusConflict,
		ReconciliationID: reconID.String(),
		Detail:           detail,
	}
	if err := s.recordApplied(ctx, companyID, in, out); err != nil {
		return Outcome{}, err
	}
	return out, nil
}

// frozenOutcome builds the StatusFrozen outcome for a supersede that the ledger
// hard-blocked. The ledger already raised the freeze recon row (kind 'IRN' /
// 'FILED_GSTR1') — the sequencer does NOT insert a second row; it locates that
// row's id by listing the open queue and matching the newest entry for this
// voucher with this attempted_op (so the node can surface it).
func (s *Service) frozenOutcome(ctx context.Context, companyID uuid.UUID, in Intent, fe *ledger.FrozenError) (Outcome, error) {
	reconID := s.findFreezeRecon(ctx, in.ClientVoucherID, string(in.Op), fe.Kind)
	out := Outcome{
		IdempotencyKey:   in.IdempotencyKey,
		ClientVoucherID:  in.ClientVoucherID,
		Status:           StatusFrozen,
		FinalVoucherNo:   fe.VoucherNo,
		ReconciliationID: reconID,
		Detail:           fe.Error(),
	}
	if err := s.recordApplied(ctx, companyID, in, out); err != nil {
		return Outcome{}, err
	}
	return out, nil
}

// findFreezeRecon returns the id of the newest open freeze reconciliation row for
// the voucher with the given attempted_op and kind, or "" if none is found
// (best-effort: the outcome is authoritative regardless).
func (s *Service) findFreezeRecon(ctx context.Context, voucherID uuid.UUID, op, kind string) string {
	entries, err := s.ledger.ListReconciliationQueue(ctx)
	if err != nil {
		return ""
	}
	for _, e := range entries { // ListReconciliationQueue is newest-first
		if e.VoucherID == voucherID && e.AttemptedOp == op && e.Kind == kind {
			return e.ID.String()
		}
	}
	return ""
}

// ListConflicts returns the open reconciliation-queue rows (both CONFLICT losers
// and freeze rows) for the company in context, newest first. It reuses the
// ledger's lister so the fuzzer/inspection path reads losers consistently. The
// preserved payload JSONB is read via ConflictPayload below.
func (s *Service) ListConflicts(ctx context.Context) ([]ledger.ReconEntry, error) {
	return s.ledger.ListReconciliationQueue(ctx)
}

// ConflictPayload reads back the preserved intent payload for a parked conflict
// row (the 0010 JSONB column). It returns (intent, true, nil) when the row has a
// non-null payload, (zero, false, nil) when the payload is NULL (e.g. a freeze
// row), so the fuzzer can round-trip a loser's intent.
func (s *Service) ConflictPayload(ctx context.Context, reconID uuid.UUID) (Intent, bool, error) {
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return Intent{}, false, db.ErrNoCompany
	}
	var raw []byte
	err := s.pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT payload FROM reconciliation_queue
			  WHERE company_id = $1 AND id = $2`,
			companyID, reconID).Scan(&raw)
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Intent{}, false, nil
	case err != nil:
		return Intent{}, false, fmt.Errorf("sync: read conflict payload: %w", err)
	}
	if len(raw) == 0 {
		return Intent{}, false, nil
	}
	var in Intent
	if err := json.Unmarshal(raw, &in); err != nil {
		return Intent{}, false, fmt.Errorf("sync: unmarshal conflict payload: %w", err)
	}
	return in, true, nil
}

// lookupApplied reads a recorded outcome for (company, idempotency_key). It
// returns (outcome, true, nil) when the key was already applied.
func (s *Service) lookupApplied(ctx context.Context, companyID uuid.UUID, key string) (Outcome, bool, error) {
	var (
		outcome string
		finalNo string
	)
	err := s.pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT outcome, final_voucher_no FROM sync_applied
			  WHERE company_id = $1 AND idempotency_key = $2`,
			companyID, key).Scan(&outcome, &finalNo)
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Outcome{}, false, nil
	case err != nil:
		return Outcome{}, false, fmt.Errorf("sync: lookup applied: %w", err)
	}
	return Outcome{FinalVoucherNo: finalNo, Detail: "duplicate: " + outcome}, true, nil
}

// recordApplied inserts the exactly-once dedupe row. It is idempotent on
// (company, idempotency_key): a racing/duplicate insert is swallowed (ON CONFLICT
// DO NOTHING), so a re-run never errors on the unique constraint.
func (s *Service) recordApplied(ctx context.Context, companyID uuid.UUID, in Intent, out Outcome) error {
	return s.pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO sync_applied
			        (company_id, idempotency_key, client_voucher_id, outcome, final_voucher_no)
			 VALUES ($1, $2, $3, $4, $5)
			 ON CONFLICT (company_id, idempotency_key) DO NOTHING`,
			companyID, in.IdempotencyKey, in.ClientVoucherID, string(out.Status), out.FinalVoucherNo)
		if err != nil {
			return fmt.Errorf("sync: record applied: %w", err)
		}
		return nil
	})
}

// headAny reads ANY head row (live or tombstoned) for the voucher: the
// CREATE-replay guard must treat an already-created id as a duplicate even if it
// was subsequently deleted. It returns the current number, version, and exists.
func (s *Service) headAny(ctx context.Context, companyID, voucherID uuid.UUID) (string, int, bool, error) {
	var (
		no  string
		ver int
	)
	err := s.pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT COALESCE(vv.voucher_no, ''), vh.version_no
			   FROM voucher_head vh
			   JOIN voucher_version vv ON vv.id = vh.current_version_id
			  WHERE vh.company_id = $1 AND vh.voucher_id = $2`,
			companyID, voucherID).Scan(&no, &ver)
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "", 0, false, nil
	case err != nil:
		return "", 0, false, fmt.Errorf("sync: read head row: %w", err)
	}
	return no, ver, true, nil
}

// headVoucherNo reads the current LIVE head's display number for a voucher (for
// the stale-conflict's frozen_voucher_no). Empty string when no live head.
func (s *Service) headVoucherNo(ctx context.Context, companyID, voucherID uuid.UUID) (string, error) {
	var no string
	err := s.pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT COALESCE(vv.voucher_no, '')
			   FROM voucher_head vh
			   JOIN voucher_version vv ON vv.id = vh.current_version_id
			  WHERE vh.company_id = $1 AND vh.voucher_id = $2 AND vh.is_deleted = false`,
			companyID, voucherID).Scan(&no)
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("sync: read head voucher_no: %w", err)
	}
	return no, nil
}

// lessHLC reports whether HLC a precedes b in the (WallMillis, Counter, NodeID)
// total order. WS2's hlc.go exports the public LessHLC with the SAME ordering;
// this private mirror keeps service.go independently buildable without colliding
// with that declaration.
func lessHLC(a, b HLCTimestamp) bool {
	if a.WallMillis != b.WallMillis {
		return a.WallMillis < b.WallMillis
	}
	if a.Counter != b.Counter {
		return a.Counter < b.Counter
	}
	return a.NodeID < b.NodeID
}
