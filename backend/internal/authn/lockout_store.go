package authn

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGLockoutStore is the Postgres LockoutStore. It writes the audit trail to
// auth_login_attempt (no RLS; just a table+sequence grant to tally_app) and
// mutates lock state on app_user. Reads/writes of app_user run inside a
// transaction whose FIRST statement sets the app.current_subject GUC
// (transaction-local, mirroring db.RunInTx's company GUC) so the app_user
// FORCE-RLS policy `subject = app_current_subject() OR id = app_current_user_id()`
// admits exactly the acting subject's row.
//
// The store is self-contained: it does not import the WS1 db tx helpers and
// takes the raw pgx pool so WS2 needs no edit to internal/platform/db.
type PGLockoutStore struct {
	pool *pgxpool.Pool
}

// NewPGLockoutStore constructs a PGLockoutStore over an existing pgx pool.
func NewPGLockoutStore(pool *pgxpool.Pool) *PGLockoutStore {
	return &PGLockoutStore{pool: pool}
}

// RecordAttempt appends a row to the auth_login_attempt audit trail. This table
// is written during PRE-auth login (before any user/company GUC is known) and
// therefore carries no RLS; it only needs the table CRUD grant and the implicit
// sequence USAGE grant (auth_login_attempt_id_seq) for tally_app.
func (s *PGLockoutStore) RecordAttempt(ctx context.Context, subject string, ok bool, ip string) error {
	// auth_login_attempt.ip is NOT NULL DEFAULT '' in 0011, so pass the empty
	// string (never NULL) when no client IP is available.
	_, err := s.pool.Exec(ctx, `
		INSERT INTO auth_login_attempt (subject, succeeded, ip)
		VALUES ($1, $2, $3)`, subject, ok, ip)
	if err != nil {
		return fmt.Errorf("authn: record attempt: %w", err)
	}
	return nil
}

// FailuresInWindow counts failed attempts for subject at or after `since`.
func (s *PGLockoutStore) FailuresInWindow(ctx context.Context, subject string, since time.Time) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM auth_login_attempt
		WHERE subject = $1 AND succeeded = false AND at >= $2`, subject, since).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("authn: count failures: %w", err)
	}
	return n, nil
}

// withSubject runs fn inside a transaction with the subject GUC set
// transaction-local so app_user RLS admits the subject's row.
func (s *PGLockoutStore) withSubject(ctx context.Context, subject string, fn func(tx pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("authn: begin tx: %w", err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_subject', $1, true)", subject); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("authn: set subject guc: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("authn: commit: %w", err)
	}
	return nil
}

// SetLock locks the subject's app_user row until `until` and records the
// observed failure count on the row.
func (s *PGLockoutStore) SetLock(ctx context.Context, subject string, until time.Time, failures int) error {
	return s.withSubject(ctx, subject, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE app_user
			SET locked_until = $1, failed_attempts = $2
			WHERE subject = $3`, until, failures, subject)
		if err != nil {
			return fmt.Errorf("authn: set lock: %w", err)
		}
		return nil
	})
}

// ClearLock resets failure state and lock on success.
func (s *PGLockoutStore) ClearLock(ctx context.Context, subject string) error {
	return s.withSubject(ctx, subject, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE app_user
			SET locked_until = NULL, failed_attempts = 0
			WHERE subject = $1`, subject)
		if err != nil {
			return fmt.Errorf("authn: clear lock: %w", err)
		}
		return nil
	})
}

// LockedUntil returns the subject's lock expiry, or the zero time if the subject
// is unknown or not locked.
func (s *PGLockoutStore) LockedUntil(ctx context.Context, subject string) (time.Time, error) {
	var until time.Time
	err := s.withSubject(ctx, subject, func(tx pgx.Tx) error {
		var lu *time.Time
		err := tx.QueryRow(ctx, `
			SELECT locked_until FROM app_user WHERE subject = $1`, subject).Scan(&lu)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("authn: read lock: %w", err)
		}
		if lu != nil {
			until = *lu
		}
		return nil
	})
	return until, err
}
