package agent

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/actions"
	"github.com/khata/cored/internal/intentlog"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/policy"
)

// admin.go is what the owner-facing routes need beyond runs and approvals: the
// autonomy table (0093) and the intent log's anchor verification (0092),
// neither of which had a caller in wave 1 (D121 §9 items 8 and 11).

// VerifyAnchors recomputes every intent-log anchor of the company from the
// intent rows (intentlog.VerifyAnchors): a truncated run tail, a deleted anchor
// or an edited head hash fails here even when every surviving per-run chain is
// valid. It returns how many anchors were checked.
func (e *Engine) VerifyAnchors(ctx context.Context, companyID uuid.UUID) (int, error) {
	var n int
	var verr error
	err := e.Pool.RunInReadOnlyTx(db.WithCompany(ctx, companyID), func(ctx context.Context, tx pgx.Tx) error {
		n, verr = intentlog.VerifyAnchors(ctx, tx, companyID)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, verr
}

// ListAutonomy returns the company's autonomy rows.
func (e *Engine) ListAutonomy(ctx context.Context, companyID uuid.UUID) ([]policy.AutonomyRow, error) {
	var out []policy.AutonomyRow
	err := e.Pool.RunInReadOnlyTx(db.WithCompany(ctx, companyID), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = policy.ListLevels(ctx, tx)
		return err
	})
	return out, err
}

// AutonomyInput is one row to set.
type AutonomyInput struct {
	// UserID uuid.Nil is the tenant-wide row.
	UserID         uuid.UUID
	ACLKey         string
	AmountMaxPaise *int64
	Level          policy.Level
	SetBy          uuid.UUID
}

// SetAutonomy validates and upserts one autonomy row. The caller has checked
// that a per-user row names a member of the company.
//
// ⛔ ONLY A KEY AN AGENT COULD EVER COMMIT UNDER IS SETTABLE. A level on
// `delete voucher` or `alter security` would be a row that reads as permission
// and grants nothing (the structural rules refuse those whatever the table
// says) — exactly the kind of setting an owner misreads later. A read has no
// commit to gate. So the key must be one some write ActionSpec evaluates to,
// and at least one such spec must survive policy.Structural.
func (e *Engine) SetAutonomy(ctx context.Context, companyID uuid.UUID, in AutonomyInput) error {
	if in.Level < policy.L0 || in.Level > policy.L3 {
		return fmt.Errorf("%w: level must be 0..3", ErrInvalid)
	}
	if in.SetBy == uuid.Nil {
		return fmt.Errorf("%w: no deciding user", ErrInvalid)
	}
	if in.AmountMaxPaise != nil && *in.AmountMaxPaise < 0 {
		return fmt.Errorf("%w: amount_max_paise must not be negative", ErrInvalid)
	}
	specs := actions.ByACLKey(in.ACLKey)
	writable := false
	for _, s := range specs {
		if s.IsWrite && policy.Structural(s) == nil {
			writable = true
			break
		}
	}
	if !writable {
		return fmt.Errorf("%w: %q is not an action an agent can ever commit, so it has no autonomy level", ErrInvalid, in.ACLKey)
	}
	return e.Pool.RunInTx(db.WithCompany(ctx, companyID), func(ctx context.Context, tx pgx.Tx) error {
		return policy.SetLevel(ctx, tx, companyID, in.UserID, in.ACLKey, in.AmountMaxPaise, in.Level, in.SetBy)
	})
}
