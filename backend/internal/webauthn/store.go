package webauthn

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MaxStoredCredentials is the per-user RETENTION ceiling enforced by Save: after
// a successful enrolment the user keeps at most this many credentials, newest
// first, and anything older is deleted in the SAME transaction.
//
// WHY A CEILING AT ALL. Nothing used to prune or cap this table, and the client
// ENROLS a fresh credential whenever the device in front of the user holds none
// of the ones the server listed (frontend src/auth/webauthn.ts, the (b) branch of
// stepUp). Every fresh browser profile — and every CDP virtual authenticator —
// holds none, so the set grew without limit for the lifetime of a user. Measured
// live: one dev user reached 65 credentials, at which point Chromium refused the
// assertion outright ("The 'allowCredentials' attribute exceeds the maximum
// allowed size (64)") and EVERY WRITE IN THE PRODUCT became impossible on EVERY
// device.
//
// WHY 128 AND NOT 64. 64 is the browser's ceiling on the OFFERED list (see
// maxAllowCredentials in internal/server/auth_webauthn.go). Retaining exactly the
// offered window would mean every credential the server keeps is one it can
// offer — tidy, but it would also delete a credential the moment it fell out of
// the window, and a credential row IS a real passkey on a real device. 128 leaves
// deliberate headroom: a credential can drop out of the OFFERED window and still
// be VERIFIABLE (assertion resolves through ByCredentialID, which has no limit),
// so a device that still holds its passkey and asserts it directly is honoured
// rather than told "unknown credential".
//
// THE TRADE-OFF, STATED PLAINLY. Crossing the ceiling SILENTLY DE-REGISTERS the
// oldest passkey. The alternative — refusing further enrolment — was rejected
// because it strands the device in front of the user with no way out from inside
// the app: it does not remove the lockout, it relocates it onto whichever device
// arrives 129th. Pruning the oldest costs that device one extra enrolment prompt
// on its next step-up (exactly the flow stepUp already implements and documents),
// which is recoverable; a refusal is not.
const MaxStoredCredentials = 128

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

// Save inserts a credential row for the given user and then enforces the
// per-user retention ceiling (MaxStoredCredentials) IN THE SAME TRANSACTION,
// deleting the oldest rows beyond the newest MaxStoredCredentials.
//
// The prune is inside the insert's transaction on purpose: this is the ONLY write
// path for the table (Service.RegistrationFinish), so a ceiling enforced here
// cannot be bypassed, and two concurrent enrolments cannot each observe an
// under-ceiling count and both skip the prune.
//
// The DELETE is safe with respect to tenancy for two independent reasons: it is
// filtered on user_id, and webauthn_credential is FORCE-RLS under
// `user_id = app_current_user_id()` (migration 0011) with the GUC set to this
// user by withUser — so it cannot reach another user's rows even if the WHERE
// clause were wrong.
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
		// OFFSET past the newest MaxStoredCredentials and delete the remainder.
		// The ORDER BY must match RecentByUser's exactly (created_at DESC, id DESC)
		// or the row a reader considers "retained" and the row this considers
		// "prunable" could disagree on a created_at tie.
		if _, err := tx.Exec(ctx, `
			DELETE FROM webauthn_credential
			WHERE id IN (
				SELECT id
				FROM webauthn_credential
				WHERE user_id = $1
				ORDER BY created_at DESC, id DESC
				OFFSET $2
			)`, c.UserID, MaxStoredCredentials); err != nil {
			return fmt.Errorf("webauthn: prune credentials beyond %d: %w", MaxStoredCredentials, err)
		}
		return nil
	})
}

// ByUser returns ALL credentials registered to a user, OLDEST FIRST.
//
// ⚠ THE "ALL" IS PART OF THE CONTRACT — DO NOT ADD A LIMIT HERE. A caller that
// resolves an asserted credential by scanning this list would silently stop
// recognising older-but-perfectly-valid credentials, locking out a real device
// that holds a real passkey — a strictly worse failure than the oversized
// allow-list this cap exists to prevent. The allow-list path that DID need a cap
// uses RecentByUser instead.
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

// RecentByUser returns at most limit of the user's credentials, NEWEST FIRST.
//
// WHY THIS EXISTS AS A SEPARATE ACCESSOR. The assertion allow list must be capped
// (a browser refuses outright to evaluate more than 64 entries — see
// maxAllowCredentials in internal/server/auth_webauthn.go), but capping ByUser to
// achieve that was rejected: ByUser's documented contract is "all", and a future
// caller that resolved an asserted credential through it would inherit a silent
// truncation and start rejecting valid passkeys. Verification today goes through
// ByCredentialID (Service.AssertionFinish), which is deliberately unlimited, so a
// credential outside this window is still fully verifiable — it is merely not
// OFFERED.
//
// NEWEST FIRST IS THE WHOLE POINT, NOT A DETAIL. ByUser orders created_at
// ASCENDING, so bolting a LIMIT onto that ordering would have truncated away the
// NEWEST credential — the one the device in front of the user just enrolled —
// leaving that user's own current device the single device excluded from its own
// allow list. That is why this is DESC. id DESC is a stable tiebreak so a
// created_at tie (two enrolments inside the same clock tick) still yields a total
// order, and the same tiebreak is used by Save's prune.
//
// limit < 1 is an ERROR rather than "no limit": a zero value arriving here from a
// mis-wired caller would otherwise produce an EMPTY allow list, which the client
// reads as "this user has no passkeys" and answers by enrolling yet another one —
// the exact runaway this cap exists to stop.
func (s *PGStore) RecentByUser(ctx context.Context, userID uuid.UUID, limit int) ([]Credential, error) {
	if limit < 1 {
		return nil, fmt.Errorf("webauthn: RecentByUser limit must be >= 1, got %d", limit)
	}
	var creds []Credential
	err := s.withUser(ctx, userID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, user_id, credential_id, public_key, alg, sign_count, aaguid
			FROM webauthn_credential
			WHERE user_id = $1
			ORDER BY created_at DESC, id DESC
			LIMIT $2`, userID, limit)
		if err != nil {
			return fmt.Errorf("webauthn: query recent credentials: %w", err)
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
