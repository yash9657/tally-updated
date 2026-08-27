package ledger

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/platform/db"
)

// FrozenError is returned when an alter/delete targets a voucher pinned by an
// external reference (IRN / filed GSTR-1). Registering the reference pins the
// voucher's display number and HARD-BLOCKS any later alter/delete; the post
// path returns this error (rolling the transaction back) and the entry points
// route the attempt to the reconciliation queue. It carries the blocking
// reference so the HTTP layer can render the 409 body.
//
// The server maps it with errors.As(err, &fe) where fe is a *FrozenError.
type FrozenError struct {
	Kind      string // 'IRN' | 'FILED_GSTR1'
	Ref       string // the IRN / ARN string
	VoucherNo string // the pinned display number
}

func (e *FrozenError) Error() string {
	return fmt.Sprintf("ledger: voucher frozen by %s %s (no %s)", e.Kind, e.Ref, e.VoucherNo)
}

// ExternalRefResult is the outcome of pinning a voucher to an external
// reference. PinnedVoucherNo is the display number captured at pin time (and
// returned unchanged on an idempotent re-register).
type ExternalRefResult struct {
	VoucherID       uuid.UUID
	Kind            string
	Ref             string
	PinnedVoucherNo string
}

// ReconEntry is one open reconciliation-queue row: a refused alter/delete of a
// frozen voucher. Timestamps are rendered as RFC3339 strings for the HTTP layer.
type ReconEntry struct {
	ID              uuid.UUID
	VoucherID       uuid.UUID
	AttemptedOp     string
	Kind            string
	Ref             string
	FrozenVoucherNo string
	Detail          string
	CreatedAt       string
}

// RegisterExternalReference pins the voucher's CURRENT display number and
// records the external reference. It is idempotent on (company, voucher, kind,
// ref): a re-register returns the EXISTING pinned_voucher_no and writes no
// second row, so the number is never re-pinned. It errors with
// ErrVoucherNotFound when no live (non-deleted) head exists. The whole unit
// runs in one company-scoped transaction via s.runner.RunInTx.
func (s *Service) RegisterExternalReference(ctx context.Context, voucherID uuid.UUID, kind, ref, createdBy string) (ExternalRefResult, error) {
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return ExternalRefResult{}, db.ErrNoCompany
	}

	var out ExternalRefResult
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// Idempotency first: if this exact (voucher, kind, ref) is already
		// pinned, return the existing number without touching the head or
		// inserting a second row.
		var existingNo string
		err := tx.QueryRow(ctx,
			`SELECT pinned_voucher_no FROM voucher_external_reference
			  WHERE company_id = $1 AND voucher_id = $2 AND kind = $3 AND ref_value = $4`,
			companyID, voucherID, kind, ref).Scan(&existingNo)
		switch {
		case err == nil:
			out = ExternalRefResult{VoucherID: voucherID, Kind: kind, Ref: ref, PinnedVoucherNo: existingNo}
			return nil
		case errors.Is(err, pgx.ErrNoRows):
			// fall through to pin
		default:
			return fmt.Errorf("ledger: read existing external reference: %w", err)
		}

		// Pin the CURRENT display number. A missing/deleted head surfaces as
		// ErrVoucherNotFound (the head join excludes tombstoned vouchers).
		var pinnedNo string
		err = tx.QueryRow(ctx,
			`SELECT vv.voucher_no
			   FROM voucher_head vh
			   JOIN voucher_version vv ON vv.id = vh.current_version_id
			  WHERE vh.company_id = $1 AND vh.voucher_id = $2 AND vh.is_deleted = false`,
			companyID, voucherID).Scan(&pinnedNo)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return ErrVoucherNotFound
		case err != nil:
			return fmt.Errorf("ledger: read voucher for external reference: %w", err)
		}

		// Insert the pin. ON CONFLICT covers a racing concurrent register of
		// the same tuple: return the row that won, never a second insert.
		err = tx.QueryRow(ctx,
			`INSERT INTO voucher_external_reference
			        (company_id, voucher_id, kind, ref_value, pinned_voucher_no, created_by)
			 VALUES ($1, $2, $3, $4, $5, $6)
			 ON CONFLICT (company_id, voucher_id, kind, ref_value)
			   DO UPDATE SET pinned_voucher_no = voucher_external_reference.pinned_voucher_no
			 RETURNING pinned_voucher_no`,
			companyID, voucherID, kind, ref, pinnedNo, createdBy).Scan(&pinnedNo)
		if err != nil {
			return fmt.Errorf("ledger: insert external reference: %w", err)
		}
		out = ExternalRefResult{VoucherID: voucherID, Kind: kind, Ref: ref, PinnedVoucherNo: pinnedNo}
		return nil
	})
	if err != nil {
		return ExternalRefResult{}, err
	}
	return out, nil
}

// RaiseReconciliation inserts an open reconciliation_queue row recording a
// refused alter/delete of a frozen voucher. It runs in its OWN transaction so
// the refusal is durably logged even though the blocked post rolled back.
// Called by AlterVoucher/DeleteVoucher when post() returns a *FrozenError.
func (s *Service) RaiseReconciliation(ctx context.Context, voucherID uuid.UUID, attemptedOp string, fe *FrozenError) (ReconEntry, error) {
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return ReconEntry{}, db.ErrNoCompany
	}

	detail := fmt.Sprintf("%s blocked: voucher frozen by %s %s (no %s)", attemptedOp, fe.Kind, fe.Ref, fe.VoucherNo)
	var e ReconEntry
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`INSERT INTO reconciliation_queue
			        (company_id, voucher_id, attempted_op, kind, ref_value, frozen_voucher_no, detail)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)
			 RETURNING id, voucher_id, attempted_op, kind, ref_value, frozen_voucher_no, detail,
			           to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')`,
			companyID, voucherID, attemptedOp, fe.Kind, fe.Ref, fe.VoucherNo, detail).Scan(
			&e.ID, &e.VoucherID, &e.AttemptedOp, &e.Kind, &e.Ref, &e.FrozenVoucherNo, &e.Detail, &e.CreatedAt)
	})
	if err != nil {
		return ReconEntry{}, fmt.Errorf("ledger: raise reconciliation: %w", err)
	}
	return e, nil
}

// ListReconciliationQueue returns the OPEN (resolved_at IS NULL) entries for the
// company, newest first.
func (s *Service) ListReconciliationQueue(ctx context.Context) ([]ReconEntry, error) {
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return nil, db.ErrNoCompany
	}

	var entries []ReconEntry
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT id, voucher_id, attempted_op, kind, ref_value, frozen_voucher_no, detail,
			        to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')
			   FROM reconciliation_queue
			  WHERE company_id = $1 AND resolved_at IS NULL
			  ORDER BY created_at DESC, id DESC`,
			companyID)
		if err != nil {
			return fmt.Errorf("ledger: query reconciliation queue: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var e ReconEntry
			if err := rows.Scan(&e.ID, &e.VoucherID, &e.AttemptedOp, &e.Kind, &e.Ref,
				&e.FrozenVoucherNo, &e.Detail, &e.CreatedAt); err != nil {
				return fmt.Errorf("ledger: scan reconciliation row: %w", err)
			}
			entries = append(entries, e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// ResolveReconciliation marks ONE open reconciliation_queue row handled by
// stamping resolved_at. It reports whether a row was actually stamped: false
// means no OPEN row with that id exists in this company — an unknown id, or one
// a concurrent request already resolved. The caller turns that into a 404; the
// two cases are deliberately NOT distinguished, because telling an operator
// "that id exists but is already resolved" leaks the existence of a row they
// cannot see (ListReconciliationQueue returns open rows only).
//
// ⛔ WHY THIS FUNCTION IS NEW AND WHAT ITS ABSENCE MEANT. resolved_at has been
// in the schema since migration 0009 and EVERY read filters on `resolved_at IS
// NULL`, but nothing anywhere ever wrote it — no UPDATE of this table existed in
// the codebase, in Go or in SQL. So a reconciliation row was IMMORTAL: every
// refused alter/delete a company ever attempted stayed open forever, the queue
// only ever grew, and an operator who dealt with an item had nowhere to record
// that they had. A queue that cannot be emptied is one operators stop reading,
// which is the failure mode that matters: the entry is how somebody finds out
// their work was refused.
//
// The WHERE clause carries `resolved_at IS NULL` as well as the id, so this is
// idempotent under a double-submit: the second call stamps nothing and reports
// false rather than moving the timestamp forward.
func (s *Service) ResolveReconciliation(ctx context.Context, reconID uuid.UUID) (bool, error) {
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return false, db.ErrNoCompany
	}

	var stamped bool
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE reconciliation_queue
			    SET resolved_at = now()
			  WHERE company_id = $1 AND id = $2 AND resolved_at IS NULL`,
			companyID, reconID)
		if err != nil {
			return fmt.Errorf("ledger: resolve reconciliation: %w", err)
		}
		stamped = tag.RowsAffected() == 1
		return nil
	})
	if err != nil {
		return false, err
	}
	return stamped, nil
}

// frozenRef reads voucher_external_reference for the voucher and returns the
// first blocking reference as a *FrozenError, or nil when the voucher is not
// frozen. It takes the post transaction so the check joins that transaction
// (the freeze gate inside post() reads the same consistent snapshot before it
// reverses any balance). A row that does not yet have a pinned number is
// impossible (pinned_voucher_no is NOT NULL).
func frozenRef(ctx context.Context, tx pgx.Tx, companyID, voucherID uuid.UUID) (*FrozenError, error) {
	var fe FrozenError
	err := tx.QueryRow(ctx,
		`SELECT kind, ref_value, pinned_voucher_no
		   FROM voucher_external_reference
		  WHERE company_id = $1 AND voucher_id = $2
		  ORDER BY created_at ASC, id ASC
		  LIMIT 1`,
		companyID, voucherID).Scan(&fe.Kind, &fe.Ref, &fe.VoucherNo)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("ledger: read external reference for freeze gate: %w", err)
	}
	return &fe, nil
}
