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
	outcome := "failure"
	if ok {
		outcome = "success"
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO auth_login_attempt (subject, succeeded, ip, outcome)
		VALUES ($1, $2, $3, $4)`, subject, ok, ip, outcome)
	if err != nil {
		return fmt.Errorf("authn: record attempt: %w", err)
	}
	return nil
}

// RecordLockedOutAttempt audits a sign-in attempt refused because the account
// was ALREADY locked (D73). succeeded=false because it certainly did not
// succeed; outcome='locked' because it was never a password check, and the
// failure count that decides locking reads `outcome = 'failure'` only.
func (s *PGLockoutStore) RecordLockedOutAttempt(ctx context.Context, subject, ip string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO auth_login_attempt (subject, succeeded, ip, outcome)
		VALUES ($1, false, $2, 'locked')`, subject, ip)
	if err != nil {
		return fmt.Errorf("authn: record locked-out attempt: %w", err)
	}
	return nil
}

// FailuresInWindow counts failed attempts for subject at or after `since` AND
// after the subject's most recent SUCCESSFUL login.
//
// The "since the last success" clause is what makes the package doc's promise
// ("a successful login resets the failure counter") true of the DATABASE and not
// only of the in-memory test fake. Without it, ClearLock's
// `UPDATE app_user SET failed_attempts = 0` was cosmetic: the number that
// actually decides locking is this count over auth_login_attempt, which
// ClearLock never touches. The observable bug was a self-inflicted denial of
// service — four typos, then a correct sign-in, then ONE more typo would lock a
// legitimate user out for 15 minutes, because the four old failures were still
// inside the sliding window.
//
// COALESCE(..., '-infinity') keeps the predicate total for a subject that has
// never succeeded, in which case this degrades to the plain window count.
//
// ⛔ IT COUNTS outcome='failure', NOT succeeded=false, AND THE RESET POINT IS A
// SUCCESS **OR AN ADMINISTRATIVE UNLOCK** (migration 0074). Two rows in this
// table are refusals that must never count toward locking:
//
//	'locked'   an attempt made while the account was already locked, refused
//	           before a password was looked at (D73). Counting it would let a
//	           brute-forcer renew their victim's lock indefinitely just by
//	           continuing to knock, and would re-lock an operator who kept
//	           typing during their fifteen minutes the instant it expired.
//	'unlocked' an administrator clearing the lock (D55). It is not a login, but
//	           it IS a reset point: without it, an account unlocked while five
//	           failures are still inside the sliding window re-locks on the very
//	           next typo and the remedy looks like it never worked.
//
// Migration 0074 backfilled `outcome` from `succeeded`, so for every row that
// existed before it, this predicate is EXACTLY the one it replaces.
func (s *PGLockoutStore) FailuresInWindow(ctx context.Context, subject string, since time.Time) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM auth_login_attempt
		WHERE subject = $1
		  AND outcome = 'failure'
		  AND at >= $2
		  AND at > COALESCE(
		      (SELECT max(at) FROM auth_login_attempt
		        WHERE subject = $1 AND outcome IN ('success', 'unlocked')),
		      '-infinity'::timestamptz)`, subject, since).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("authn: count failures: %w", err)
	}
	return n, nil
}

// ConsecutiveLocks counts the locks this subject has collected since their last
// success or unlock — the exponent for the backoff. It is app_user.lock_count,
// which SetLock increments and ClearLock (and the admin unlock) reset.
func (s *PGLockoutStore) ConsecutiveLocks(ctx context.Context, subject string) (int, error) {
	var n int
	err := s.withSubject(ctx, subject, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`SELECT lock_count FROM app_user WHERE subject = $1`, subject).Scan(&n)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // unknown subject: nothing to back off from.
		}
		if err != nil {
			return fmt.Errorf("authn: read lock count: %w", err)
		}
		return nil
	})
	return n, err
}

// SetFailureCount writes the RUNNING failure count (D74). An unknown subject
// updates no row, which is not an error: a login naming an account that does
// not exist has nothing to count on, and saying so out loud here would hand
// back the very existence oracle RecordFailure is careful not to give.
func (s *PGLockoutStore) SetFailureCount(ctx context.Context, subject string, failures int) error {
	return s.withSubject(ctx, subject, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE app_user SET failed_attempts = $1 WHERE subject = $2`,
			failures, subject); err != nil {
			return fmt.Errorf("authn: set failure count: %w", err)
		}
		return nil
	})
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
// observed failure count on the row. It reports whether a row was actually
// updated.
//
// persisted=false means the subject has no app_user row — a login naming an
// account that does not exist. The Locker turns that into "not locked" so the
// response cannot distinguish a real account from a ghost (see RecordFailure's
// no-enumeration invariant). Reporting rows-affected here, rather than guessing
// upstream, keeps the single source of truth at the write itself.
// ⛔⛔ IT ALSO ADVANCES THE REVOCATION EPOCH, AND THAT IS D56. Before this,
// locking an account revoked NOTHING it already held: `locked_until` was read on
// exactly one path (POST /auth/login), so the bearer in the attacker's hand kept
// working for the full 3600-second TTL and a locked account posted a write and
// got 201. Setting sessions_invalid_before = the lock instant kills every token
// minted at or before it, on EVERY authenticated route (see
// server.Server.sessionRevoked).
//
// GREATEST(...) is not decoration: the epoch must only ever move FORWARD.
// Assigning `at` unconditionally would let a clock skew, a replayed call, or a
// second lock computed from an earlier `now` REWIND the epoch and resurrect
// sessions that were already dead — a revocation you can undo by locking
// somebody again is not a revocation.
func (s *PGLockoutStore) SetLock(ctx context.Context, subject string, at, until time.Time, failures int) (bool, error) {
	var persisted bool
	err := s.withSubject(ctx, subject, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE app_user
			SET locked_until = $1,
			    failed_attempts = $2,
			    lock_count = lock_count + 1,
			    sessions_invalid_before =
			        GREATEST(COALESCE(sessions_invalid_before, $4), $4)
			WHERE subject = $3`, until, failures, subject, at)
		if err != nil {
			return fmt.Errorf("authn: set lock: %w", err)
		}
		persisted = tag.RowsAffected() > 0
		return nil
	})
	if err != nil {
		return false, err
	}
	return persisted, nil
}

// ClearLock resets failure state and lock on success.
//
// ⛔ IT DOES NOT CLEAR sessions_invalid_before, and that asymmetry is the point.
// The epoch says "every token minted before this instant is dead"; a later
// successful login mints a token AFTER it, so the person who proved they own
// the password is let straight back in — while the sessions that were live when
// the account was locked stay dead for good. Clearing the epoch here would mean
// an attacker holding a stolen bearer only had to wait for the real owner to
// sign in successfully, and their session would come back to life.
func (s *PGLockoutStore) ClearLock(ctx context.Context, subject string) error {
	return s.withSubject(ctx, subject, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE app_user
			SET locked_until = NULL, failed_attempts = 0, lock_count = 0
			WHERE subject = $1`, subject)
		if err != nil {
			return fmt.Errorf("authn: clear lock: %w", err)
		}
		return nil
	})
}

// SessionState reads (locked_until, sessions_invalid_before) for one subject in
// ONE round trip, through migration 0074's SECURITY DEFINER
// auth_session_state().
//
// ⛔ WHY IT DOES NOT USE withSubject LIKE EVERYTHING ELSE IN THIS FILE. This is
// the AUTHENTICATED HOT PATH — Server.authenticate calls it once for every
// request in the product. withSubject is BEGIN + set_config + the query +
// COMMIT, four round trips, because app_user's RLS needs a transaction-local
// GUC before the app role may see even its own row. Paying four round trips per
// request to read two timestamps would make revocation expensive enough that
// somebody would eventually cache it, and a cached revocation is a revocation
// with a window in it. The function is the narrowest possible hole (two
// timestamps for one named subject, nothing else) and its only caller has
// already verified the token's signature for that subject — see the migration.
//
// An UNKNOWN subject returns no row, i.e. both zero times, i.e. "nothing
// revoked". That is correct and is not a fail-open: a subject with no app_user
// row has never been locked, because SetLock's UPDATE would match no row either.
func (s *PGLockoutStore) SessionState(ctx context.Context, subject string) (time.Time, time.Time, error) {
	var lockedUntil, invalidBefore *time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT locked_until, sessions_invalid_before FROM auth_session_state($1)`,
		subject).Scan(&lockedUntil, &invalidBefore)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("authn: session state: %w", err)
	}
	var lu, ib time.Time
	if lockedUntil != nil {
		lu = *lockedUntil
	}
	if invalidBefore != nil {
		ib = *invalidBefore
	}
	return lu, ib, nil
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
