// credential_store.go is the persistence half of first-party password login
// (WS-A1). It reads and writes user_credential (migration 0021) through the same
// tenant-scoped pool every other catalog read uses — no pool.Raw(), no hand-rolled
// Begin/Commit.
//
// The dependency direction is authz -> authn (for authn.Credential). authn has
// ZERO first-party imports and must keep it that way, or this edge becomes a
// cycle.
package authz

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/authn"
	"github.com/khata/cored/internal/platform/db"
)

// SetPassword upserts the caller's OWN password verifier. It runs under the USER
// GUC (db.WithUser + RunInAuthTx), which is exactly what user_credential's
// self-only policy admits: the INSERT's WITH CHECK and the ON CONFLICT probe's
// USING both resolve to `user_id = app_current_user_id()`, so aiming this
// statement at ANOTHER user's row fails with
//
//	ERROR: new row violates row-level security policy for table "user_credential"
//
// rather than silently succeeding. The database is therefore the second line of
// defence behind the HTTP layer's rule that the acting user comes from the
// request context and never from the request body.
func (s *Store) SetPassword(ctx context.Context, userID uuid.UUID, c authn.Credential) error {
	if userID == uuid.Nil {
		return fmt.Errorf("authz: set password: empty user id")
	}
	// Refuse to persist anything the schema's CHECK would reject anyway, so a
	// caller gets a clear Go error instead of a constraint violation.
	if c.Algo != authn.PBKDF2Algo {
		return fmt.Errorf("authz: set password: unsupported algo %q", c.Algo)
	}
	if c.Iterations < 100000 || len(c.Salt) == 0 || len(c.Hash) == 0 {
		return fmt.Errorf("authz: set password: incomplete credential")
	}
	ctx = db.WithUser(ctx, userID)
	err := s.pool.RunInAuthTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO user_credential (user_id, algo, iterations, salt, hash)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (user_id) DO UPDATE
			   SET algo       = EXCLUDED.algo,
			       iterations = EXCLUDED.iterations,
			       salt       = EXCLUDED.salt,
			       hash       = EXCLUDED.hash,
			       updated_at = now()`,
			userID, c.Algo, c.Iterations, c.Salt, c.Hash)
		return err
	})
	if err != nil {
		return fmt.Errorf("authz: set password: %w", err)
	}
	return nil
}

// CredentialBySubject is the PRE-AUTH read behind POST /auth/login: at that point
// the caller has offered only a subject, so no user id is known yet.
//
// THE TWO-STEP GUC DANCE (this is the crux, and it must stay in ONE transaction):
//
//	a. RunInAuthTx sets app.current_subject from ctx (db.WithSubject).
//	b. SELECT id, is_active FROM app_user WHERE subject = $1  -- admitted by
//	   app_user's `subject = app_current_subject()` clause.
//	c. set_config('app.current_user_id', <that id>, true) in the SAME tx.
//	   app_current_user_id() is STABLE, not IMMUTABLE, so the next statement
//	   observes it.
//	d. SELECT ... FROM user_credential WHERE user_id = $1 -- now admitted by
//	   user_credential's self-only policy.
//
// WHY (c) DOES NOT WIDEN ANYTHING: 0020's app_user USING clause is
// `subject = app_current_subject() OR id = app_current_user_id() OR EXISTS(co-member
// of app_current_company_id())`. Setting the user GUC mid-transaction only adds
// the second disjunct, and it resolves to the SAME row step (b) already returned,
// so visibility widens by exactly zero rows. The dangerous third disjunct is
// inert because RunInAuthTx NEVER sets the company GUC.
//
// !! HARD CONSTRAINT !! That argument only holds while NO company is in scope.
// This method must never be reached through a ctx carrying db.WithCompany, and
// step (b) must keep its explicit `WHERE subject = $1` so it stays correct even
// if a company ever were.
//
// found=false covers BOTH "no such user" and "user with no credential row". The
// two are distinguished only internally (never on the wire) — POST /auth/login
// returns a byte-identical 401 for both, so the endpoint is not a user-existence
// oracle.
func (s *Store) CredentialBySubject(ctx context.Context, subject string) (userID uuid.UUID, c authn.Credential, active bool, found bool, err error) {
	if subject == "" {
		return uuid.Nil, authn.Credential{}, false, false, nil
	}
	// Deliberately db.WithSubject and NOT db.WithUser: the user id is unknown at
	// call time. It is set mid-transaction, below.
	ctx = db.WithSubject(ctx, subject)

	var (
		uid       uuid.UUID
		isActive  bool
		cred      authn.Credential
		haveUser  bool
		haveCreds bool
	)
	err = s.pool.RunInAuthTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// (b) resolve the user under the SUBJECT GUC.
		scanErr := tx.QueryRow(ctx,
			`SELECT id, is_active FROM app_user WHERE subject = $1`, subject,
		).Scan(&uid, &isActive)
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return nil // no such user: found stays false.
		}
		if scanErr != nil {
			return scanErr
		}
		haveUser = true

		// (c) SAME transaction: promote to the USER GUC so the self-only
		// user_credential policy admits this user's own row.
		if _, execErr := tx.Exec(ctx,
			"SELECT set_config('app.current_user_id', $1, true)", uid.String()); execErr != nil {
			return execErr
		}

		// (d) read the credential.
		scanErr = tx.QueryRow(ctx,
			`SELECT algo, iterations, salt, hash FROM user_credential WHERE user_id = $1`, uid,
		).Scan(&cred.Algo, &cred.Iterations, &cred.Salt, &cred.Hash)
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return nil // user exists but has never set a password.
		}
		if scanErr != nil {
			return scanErr
		}
		haveCreds = true
		return nil
	})
	if err != nil {
		return uuid.Nil, authn.Credential{}, false, false, fmt.Errorf("authz: credential by subject: %w", err)
	}
	if !haveUser || !haveCreds {
		// Return the user id when we have it (the caller may want it for auditing)
		// but keep found=false so no caller can accidentally treat a missing
		// credential as an empty-password match.
		if haveUser {
			return uid, authn.Credential{}, isActive, false, nil
		}
		return uuid.Nil, authn.Credential{}, false, false, nil
	}
	return uid, cred, isActive, true, nil
}

// ProfileByUserID reads the acting user's own directory row (subject, display
// name, email) under the USER GUC. POST /auth/login's 200 body carries a `user`
// block, and CredentialBySubject's pinned signature has nowhere to return it.
//
// The read is self-only by construction: `id = app_current_user_id()` is the
// second disjunct of app_user's policy and no company is in scope, so this can
// never return somebody else's row.
func (s *Store) ProfileByUserID(ctx context.Context, userID uuid.UUID) (subject, displayName, email string, err error) {
	if userID == uuid.Nil {
		return "", "", "", fmt.Errorf("authz: profile by user id: empty user id")
	}
	ctx = db.WithUser(ctx, userID)
	err = s.pool.RunInAuthTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		scanErr := tx.QueryRow(ctx,
			`SELECT subject, display_name, email FROM app_user WHERE id = $1`, userID,
		).Scan(&subject, &displayName, &email)
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return fmt.Errorf("%w: app_user %s", ErrNotFound, userID)
		}
		return scanErr
	})
	if err != nil {
		return "", "", "", fmt.Errorf("authz: profile by user id: %w", err)
	}
	return subject, displayName, email, nil
}
