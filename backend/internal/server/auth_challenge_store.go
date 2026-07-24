// auth_challenge_store.go owns the WebAuthn ceremony challenge handoff (WS6,
// auth prod-hardening).
//
// The webauthn package deliberately does NOT persist challenges between `begin`
// and `finish`; the caller (this server) must stash them. Two implementations
// share one interface and one conformance suite
// (auth_challenge_store_test.go):
//
//   - memChallengeStore — a mutex-guarded map. Retained for hermetic unit tests
//     and CORED_SKIP_DB runs; it is SINGLE-INSTANCE only.
//   - pgChallengeStore  — the shipped one (migration 0012). Any cored replica can
//     finish a ceremony any other replica began, which is what makes WebAuthn
//     step-up deployable behind more than one instance.
//
// The three behavioural invariants both must satisfy:
//
//  1. SINGLE USE  — Take consumes the challenge, even when it turns out to be
//     expired. A finish can never be replayed.
//  2. OVERWRITE   — a second Put for the same user replaces the first, so only
//     the LATEST begin can be finished.
//  3. TTL         — a challenge older than the store's TTL is not returned.
//
// Both stores evaluate the TTL in Go against an injectable clock (`now`), so the
// conformance suite can prove expiry on Postgres by advancing a fake clock
// instead of sleeping.
package server

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/khata/cored/internal/webauthn"
)

// ChallengeStore stashes a ceremony challenge between begin and finish.
//
// Take reports found=false when there is no live challenge (never begun, already
// consumed, or expired) — that is a CLIENT error (400). A non-nil error is an
// INFRASTRUCTURE failure (500); handlers must not conflate the two, or a DB blip
// would be indistinguishable from a single-use violation.
type ChallengeStore interface {
	Put(ctx context.Context, userID uuid.UUID, ch webauthn.Challenge) error
	Take(ctx context.Context, userID uuid.UUID) (webauthn.Challenge, bool, error)
}

// --------------------------------------------------------------------------
// in-memory implementation
// --------------------------------------------------------------------------

// memChallengeStore is the in-memory ChallengeStore. Single-instance only; kept
// for hermetic tests and as the conformance suite's reference implementation.
type memChallengeStore struct {
	mu  sync.Mutex
	now func() time.Time
	ttl time.Duration
	m   map[uuid.UUID]stashedChallenge
}

type stashedChallenge struct {
	ch      webauthn.Challenge
	expires time.Time
}

func newMemChallengeStore() *memChallengeStore {
	return &memChallengeStore{m: map[uuid.UUID]stashedChallenge{}}
}

func (c *memChallengeStore) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *memChallengeStore) lifetime() time.Duration {
	if c.ttl > 0 {
		return c.ttl
	}
	return challengeTTL
}

// Put stashes a challenge for a user, replacing any prior one.
func (c *memChallengeStore) Put(_ context.Context, userID uuid.UUID, ch webauthn.Challenge) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[userID] = stashedChallenge{ch: ch, expires: c.clock().Add(c.lifetime())}
	return nil
}

// Take returns and removes the stashed challenge for a user, reporting whether a
// non-expired one was present (single-use: a finish consumes its begin).
func (c *memChallengeStore) Take(_ context.Context, userID uuid.UUID) (webauthn.Challenge, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.m[userID]
	if !ok {
		return webauthn.Challenge{}, false, nil
	}
	delete(c.m, userID) // consumed even when expired.
	if c.clock().After(s.expires) {
		return webauthn.Challenge{}, false, nil
	}
	return s.ch, true, nil
}

// --------------------------------------------------------------------------
// Postgres implementation (migration 0012)
// --------------------------------------------------------------------------

// pgChallengeStore is the shipped ChallengeStore, backed by webauthn_challenge.
// Every operation runs inside a transaction whose FIRST statement sets the
// app.current_user_id GUC transaction-locally, so the table's FORCE-RLS policy
// (`user_id = app_current_user_id()`) scopes the row to the acting user. This
// mirrors webauthn.PGStore.withUser verbatim and takes the same raw pgx pool.
type pgChallengeStore struct {
	pool *pgxpool.Pool
	// now/ttl are injectable for the conformance suite; zero values mean
	// time.Now and challengeTTL.
	now func() time.Time
	ttl time.Duration
}

func newPGChallengeStore(pool *pgxpool.Pool) *pgChallengeStore {
	return &pgChallengeStore{pool: pool}
}

func (s *pgChallengeStore) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *pgChallengeStore) lifetime() time.Duration {
	if s.ttl > 0 {
		return s.ttl
	}
	return challengeTTL
}

// withUser runs fn inside a transaction with the user GUC set transaction-local.
func (s *pgChallengeStore) withUser(ctx context.Context, userID uuid.UUID, fn func(tx pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("challenge store: begin tx: %w", err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_user_id', $1, true)", userID.String()); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("challenge store: set user guc: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("challenge store: commit: %w", err)
	}
	return nil
}

// Put upserts the user's single live challenge (a second begin overwrites).
func (s *pgChallengeStore) Put(ctx context.Context, userID uuid.UUID, ch webauthn.Challenge) error {
	expires := s.clock().Add(s.lifetime())
	return s.withUser(ctx, userID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO webauthn_challenge (user_id, challenge, expires_at)
			VALUES ($1, $2, $3)
			ON CONFLICT (user_id) DO UPDATE
			SET challenge = EXCLUDED.challenge, expires_at = EXCLUDED.expires_at`,
			userID, ch.Challenge, expires)
		if err != nil {
			return fmt.Errorf("challenge store: put challenge: %w", err)
		}
		return nil
	})
}

// Take deletes and returns the user's challenge. The DELETE runs whether or not
// the row has expired, so a stale challenge is consumed rather than left to be
// retried; expiry is then evaluated in Go against the injectable clock.
func (s *pgChallengeStore) Take(ctx context.Context, userID uuid.UUID) (webauthn.Challenge, bool, error) {
	var (
		raw     string
		expires time.Time
		found   bool
	)
	err := s.withUser(ctx, userID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			DELETE FROM webauthn_challenge
			WHERE user_id = $1
			RETURNING challenge, expires_at`, userID)
		switch err := row.Scan(&raw, &expires); {
		case errors.Is(err, pgx.ErrNoRows):
			return nil
		case err != nil:
			return fmt.Errorf("challenge store: take challenge: %w", err)
		}
		found = true
		return nil
	})
	if err != nil {
		return webauthn.Challenge{}, false, err
	}
	if !found || s.clock().After(expires) {
		return webauthn.Challenge{}, false, nil
	}
	return webauthn.Challenge{UserID: userID, Challenge: raw}, true, nil
}

// Compile-time assertions that both implementations satisfy the interface.
var (
	_ ChallengeStore = (*memChallengeStore)(nil)
	_ ChallengeStore = (*pgChallengeStore)(nil)
)
