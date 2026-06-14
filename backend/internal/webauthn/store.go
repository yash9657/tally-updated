package webauthn

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore is the Postgres-backed CredentialStore for the webauthn_credential
// table (migration 0011, authored by WS1). Every operation runs inside a
// transaction whose FIRST statement sets the app.current_user_id GUC
// (transaction-local, mirroring db.RunInTx's company GUC) so the FORCE-RLS
// policy `user_id = app_current_user_id()` scopes the row to the acting user.
//
// The store is self-contained: it does not depend on the WS1 db tx helpers, so
// WS2 needs no edit to internal/platform/db. It takes the raw pgx pool.
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore constructs a PGStore over an existing pgx pool (the same
// tally_app-authenticated pool the server uses).
func NewPGStore(pool *pgxpool.Pool) *PGStore {
	return &PGStore{pool: pool}
}

// withUser runs fn inside a transaction with the user GUC set transaction-local.
func (s *PGStore) withUser(ctx context.Context, userID uuid.UUID, fn func(tx pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("webauthn: begin tx: %w", err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_user_id', $1, true)", userID.String()); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("webauthn: set user guc: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("webauthn: commit: %w", err)
	}
	return nil
}

// Save inserts a credential row for the given user.
func (s *PGStore) Save(ctx context.Context, c Credential) error {
	return s.withUser(ctx, c.UserID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO webauthn_credential
				(id, user_id, credential_id, public_key, alg, sign_count, aaguid)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			c.ID, c.UserID, c.CredentialID, c.PublicKeySPKI, c.Alg, int64(c.SignCount), c.AAGUID)
		if err != nil {
			return fmt.Errorf("webauthn: save credential: %w", err)
		}
		return nil
	})
}

// ByUser returns all credentials registered to a user.
func (s *PGStore) ByUser(ctx context.Context, userID uuid.UUID) ([]Credential, error) {
	var creds []Credential
	err := s.withUser(ctx, userID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, user_id, credential_id, public_key, alg, sign_count, aaguid
			FROM webauthn_credential
			WHERE user_id = $1
			ORDER BY created_at`, userID)
		if err != nil {
			return fmt.Errorf("webauthn: query credentials: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scanCredential(rows)
			if err != nil {
				return err
			}
			creds = append(creds, c)
		}
		return rows.Err()
	})
	return creds, err
}

// ByCredentialID returns the credential matching (user, credentialID).
func (s *PGStore) ByCredentialID(ctx context.Context, userID uuid.UUID, credentialID []byte) (Credential, bool, error) {
	var (
		cred  Credential
		found bool
	)
	err := s.withUser(ctx, userID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT id, user_id, credential_id, public_key, alg, sign_count, aaguid
			FROM webauthn_credential
			WHERE user_id = $1 AND credential_id = $2`, userID, credentialID)
		c, err := scanCredential(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("webauthn: lookup credential: %w", err)
		}
		cred = c
		found = true
		return nil
	})
	return cred, found, err
}

// UpdateSignCount persists a new (strictly greater) sign count for a credential
// owned by userID. The RLS GUC is set to userID so the FORCE-RLS policy admits
// the row. The Service holds the asserted Credential (and thus its UserID) when
// it calls this on assertion success.
func (s *PGStore) UpdateSignCount(ctx context.Context, userID, credID uuid.UUID, newCount uint32) error {
	return s.withUser(ctx, userID, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `
			UPDATE webauthn_credential
			SET sign_count = $1
			WHERE id = $2 AND user_id = $3`, int64(newCount), credID, userID)
		if err != nil {
			return fmt.Errorf("webauthn: update sign count: %w", err)
		}
		if ct.RowsAffected() == 0 {
			return ErrUnknownCredential
		}
		return nil
	})
}

type scannable interface {
	Scan(dest ...any) error
}

func scanCredential(row scannable) (Credential, error) {
	var (
		c      Credential
		alg    int64
		signC  int64
		aaguid []byte
	)
	if err := row.Scan(&c.ID, &c.UserID, &c.CredentialID, &c.PublicKeySPKI, &alg, &signC, &aaguid); err != nil {
		return Credential{}, err
	}
	c.Alg = int(alg)
	c.SignCount = uint32(signC)
	c.AAGUID = aaguid
	return c, nil
}
