// auth_challenge_store_test.go is the ChallengeStore CONFORMANCE suite (WS6).
//
// It is deliberately an INTERNAL test (package server, not server_test): the two
// implementations and their constructors are unexported and must stay that way —
// exporting them purely to reach them from an external test package would widen
// the server's API for no operational reason. It is the first internal test file
// in this package; it compiles alongside the existing package server_test files.
//
// The suite is TABLE-DRIVEN OVER THE INTERFACE: every behavioural invariant runs
// against BOTH the in-memory store and the Postgres store, so the two can never
// drift. The PG rows are keyed to real app_user ids (FK) seeded through the admin
// connection; the store itself runs on the least-privilege tally_app pool, so the
// FORCE-RLS policy on webauthn_challenge is exercised for real.
//
// The top-level name starts with "TestAuth" on purpose: the project's focused
// gate is `go test ./internal/server -run 'Auth|Tenant|Token'`, and a name like
// TestChallengeStoreConformance would silently not run under it.
package server

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/webauthn"
	"github.com/khata/cored/migrations"
)

const (
	challengeAdminDSN = "postgres://postgres:postgres@localhost:5433/tally?sslmode=disable"
	challengeAppDSN   = "postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable"
)

// testClock is a manually advanced clock so TTL expiry is proven deterministically
// on BOTH stores (no time.Sleep, no shortened production TTL).
type testClock struct{ t time.Time }

func (c *testClock) now() time.Time          { return c.t }
func (c *testClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// newUserFn mints a user id that the store under test will accept (the PG store
// needs a real app_user row because of the FK).
type newUserFn func(t *testing.T) uuid.UUID

// storeFactory builds a store bound to the given clock and TTL.
type storeFactory func(t *testing.T, clock *testClock, ttl time.Duration) (ChallengeStore, newUserFn)

func memStoreFactory(t *testing.T, clock *testClock, ttl time.Duration) (ChallengeStore, newUserFn) {
	t.Helper()
	s := newMemChallengeStore()
	s.now = clock.now
	s.ttl = ttl
	return s, func(*testing.T) uuid.UUID { return uuid.New() }
}

func pgStoreFactory(t *testing.T, clock *testClock, ttl time.Duration) (ChallengeStore, newUserFn) {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped (CORED_SKIP_DB set)")
	}
	ctx := context.Background()

	admin, err := pgx.Connect(ctx, challengeAdminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable, skipping: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	if err := migrations.Apply(ctx, admin); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	pool, err := db.New(ctx, challengeAppDSN)
	if err != nil {
		t.Skipf("app postgres unavailable, skipping: %v", err)
	}
	t.Cleanup(pool.Close)

	store := &pgChallengeStore{pool: pool.Raw(), now: clock.now, ttl: ttl}
	mkUser := func(t *testing.T) uuid.UUID {
		t.Helper()
		id := uuid.New()
		// Seeded through the SUPERUSER admin connection (bypasses RLS) so the
		// fixture does not depend on the code under test.
		if _, err := admin.Exec(ctx,
			`INSERT INTO app_user (id, subject) VALUES ($1, $2)`,
			id, "challenge-conformance-"+id.String()); err != nil {
			t.Fatalf("seed app_user: %v", err)
		}
		return id
	}
	return store, mkUser
}

// TestAuthChallengeStoreRLSIsolatesUsers proves the guarantee the conformance
// suite CANNOT prove: webauthn_challenge is FORCE-RLS'd on app_current_user_id(),
// so isolation does not depend on the store remembering to write
// `WHERE user_id = $1`. It queries the table directly on the least-privilege
// tally_app pool with the GUC set (or not set) by hand.
func TestAuthChallengeStoreRLSIsolatesUsers(t *testing.T) {
	clock := &testClock{t: time.Date(2026, 7, 23, 9, 0, 0, 0, time.UTC)}
	storeIface, newUser := pgStoreFactory(t, clock, time.Minute)
	store := storeIface.(*pgChallengeStore)
	ctx := context.Background()

	alice := newUser(t)
	bob := newUser(t)
	if err := store.Put(ctx, alice, chal(alice, "alice-secret")); err != nil {
		t.Fatalf("Put alice: %v", err)
	}
	if err := store.Put(ctx, bob, chal(bob, "bob-secret")); err != nil {
		t.Fatalf("Put bob: %v", err)
	}

	// (a) With NO user GUC set, the policy resolves to NULL -> default deny.
	var n int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM webauthn_challenge`).Scan(&n); err != nil {
		t.Fatalf("count without GUC: %v", err)
	}
	if n != 0 {
		t.Fatalf("rows visible without the user GUC = %d, want 0 (RLS is not default-deny)", n)
	}

	// (b) Under Alice's GUC, an UNFILTERED select must see ONLY Alice's row.
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_user_id', $1, true)", alice.String()); err != nil {
		t.Fatalf("set guc: %v", err)
	}
	var gotUser uuid.UUID
	var gotChallenge string
	rows, err := tx.Query(ctx, `SELECT user_id, challenge FROM webauthn_challenge`)
	if err != nil {
		t.Fatalf("select under alice: %v", err)
	}
	seen := 0
	for rows.Next() {
		if err := rows.Scan(&gotUser, &gotChallenge); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
		seen++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if seen != 1 || gotUser != alice || gotChallenge != "alice-secret" {
		t.Fatalf("unfiltered select under alice's GUC saw %d row(s) (%s / %q); want exactly alice's", seen, gotUser, gotChallenge)
	}

	// (c) Alice cannot WRITE a row owned by Bob (the WITH CHECK half).
	if _, err := tx.Exec(ctx,
		`INSERT INTO webauthn_challenge (user_id, challenge, expires_at) VALUES ($1, $2, now() + interval '5 min')`,
		bob, "forged-by-alice"); err == nil {
		t.Fatal("alice inserted a challenge for bob — the RLS WITH CHECK is missing")
	}
}

func chal(userID uuid.UUID, s string) webauthn.Challenge {
	return webauthn.Challenge{UserID: userID, Challenge: s}
}

// TestAuthChallengeStoreConformance runs every ChallengeStore invariant against
// both implementations.
func TestAuthChallengeStoreConformance(t *testing.T) {
	base := time.Date(2026, 7, 23, 9, 0, 0, 0, time.UTC)

	impls := []struct {
		name    string
		factory storeFactory
	}{
		{"mem", memStoreFactory},
		{"pg", pgStoreFactory},
	}

	for _, impl := range impls {
		impl := impl
		t.Run(impl.name, func(t *testing.T) {
			// ------------------------------------------------------------------
			t.Run("put then take returns the stashed challenge", func(t *testing.T) {
				ctx := context.Background()
				clock := &testClock{t: base}
				store, newUser := impl.factory(t, clock, time.Minute)
				user := newUser(t)

				if err := store.Put(ctx, user, chal(user, "challenge-alpha")); err != nil {
					t.Fatalf("Put: %v", err)
				}
				got, found, err := store.Take(ctx, user)
				if err != nil {
					t.Fatalf("Take: %v", err)
				}
				if !found {
					t.Fatal("Take found=false right after Put")
				}
				if got.Challenge != "challenge-alpha" {
					t.Fatalf("challenge = %q, want %q", got.Challenge, "challenge-alpha")
				}
				if got.UserID != user {
					t.Fatalf("UserID = %s, want %s", got.UserID, user)
				}
			})

			// ------------------------------------------------------------------
			t.Run("single use: a second take finds nothing", func(t *testing.T) {
				ctx := context.Background()
				clock := &testClock{t: base}
				store, newUser := impl.factory(t, clock, time.Minute)
				user := newUser(t)

				if err := store.Put(ctx, user, chal(user, "one-shot")); err != nil {
					t.Fatalf("Put: %v", err)
				}
				if _, found, err := store.Take(ctx, user); err != nil || !found {
					t.Fatalf("first Take: found=%v err=%v, want found=true", found, err)
				}
				got, found, err := store.Take(ctx, user)
				if err != nil {
					t.Fatalf("second Take: %v", err)
				}
				if found {
					t.Fatalf("second Take found a consumed challenge %q — replay is possible", got.Challenge)
				}
			})

			// ------------------------------------------------------------------
			t.Run("a second put overwrites the first", func(t *testing.T) {
				ctx := context.Background()
				clock := &testClock{t: base}
				store, newUser := impl.factory(t, clock, time.Minute)
				user := newUser(t)

				if err := store.Put(ctx, user, chal(user, "first")); err != nil {
					t.Fatalf("Put first: %v", err)
				}
				if err := store.Put(ctx, user, chal(user, "second")); err != nil {
					t.Fatalf("Put second: %v", err)
				}
				got, found, err := store.Take(ctx, user)
				if err != nil || !found {
					t.Fatalf("Take: found=%v err=%v, want found=true", found, err)
				}
				if got.Challenge != "second" {
					t.Fatalf("challenge = %q, want %q (only the LATEST begin may be finished)", got.Challenge, "second")
				}
				// Exactly one live challenge per user: nothing left behind.
				if _, found, err := store.Take(ctx, user); err != nil || found {
					t.Fatalf("post-overwrite Take: found=%v err=%v, want found=false", found, err)
				}
			})

			// ------------------------------------------------------------------
			t.Run("expired challenge is not returned and is consumed anyway", func(t *testing.T) {
				ctx := context.Background()
				clock := &testClock{t: base}
				const ttl = 5 * time.Minute
				store, newUser := impl.factory(t, clock, ttl)
				user := newUser(t)

				if err := store.Put(ctx, user, chal(user, "stale")); err != nil {
					t.Fatalf("Put: %v", err)
				}
				// Control: still live just before the TTL boundary.
				clock.advance(ttl - time.Second)
				if _, found, err := store.Take(ctx, user); err != nil || !found {
					t.Fatalf("pre-expiry Take: found=%v err=%v, want found=true (TTL must not be too short)", found, err)
				}

				// Now the real case.
				clock.t = base
				if err := store.Put(ctx, user, chal(user, "stale")); err != nil {
					t.Fatalf("Put: %v", err)
				}
				clock.advance(ttl + time.Second)
				if got, found, err := store.Take(ctx, user); err != nil || found {
					t.Fatalf("expired Take: found=%v challenge=%q err=%v, want found=false", found, got.Challenge, err)
				}
				// Single-use holds even for an expired challenge: the row is gone,
				// so rewinding the clock cannot resurrect it.
				clock.t = base
				if got, found, err := store.Take(ctx, user); err != nil || found {
					t.Fatalf("post-expiry Take with the clock rewound: found=%v challenge=%q err=%v — the expired challenge was not consumed", found, got.Challenge, err)
				}
			})

			// ------------------------------------------------------------------
			t.Run("take on an empty store is a clean miss", func(t *testing.T) {
				ctx := context.Background()
				clock := &testClock{t: base}
				store, newUser := impl.factory(t, clock, time.Minute)
				user := newUser(t)

				got, found, err := store.Take(ctx, user)
				if err != nil {
					t.Fatalf("Take on empty store must not error (a miss is a client 400, an error is a 500): %v", err)
				}
				if found {
					t.Fatalf("Take on empty store found %q", got.Challenge)
				}
			})

			// ------------------------------------------------------------------
			t.Run("challenges are isolated per user", func(t *testing.T) {
				ctx := context.Background()
				clock := &testClock{t: base}
				store, newUser := impl.factory(t, clock, time.Minute)
				alice := newUser(t)
				bob := newUser(t)

				if err := store.Put(ctx, alice, chal(alice, "alice-challenge")); err != nil {
					t.Fatalf("Put alice: %v", err)
				}
				if err := store.Put(ctx, bob, chal(bob, "bob-challenge")); err != nil {
					t.Fatalf("Put bob: %v", err)
				}
				// Bob's take must not consume or reveal Alice's challenge.
				got, found, err := store.Take(ctx, bob)
				if err != nil || !found {
					t.Fatalf("Take bob: found=%v err=%v", found, err)
				}
				if got.Challenge != "bob-challenge" {
					t.Fatalf("bob got %q, want %q", got.Challenge, "bob-challenge")
				}
				got, found, err = store.Take(ctx, alice)
				if err != nil || !found {
					t.Fatalf("Take alice: found=%v err=%v — bob's take must not have consumed it", found, err)
				}
				if got.Challenge != "alice-challenge" {
					t.Fatalf("alice got %q, want %q", got.Challenge, "alice-challenge")
				}
			})
		})
	}
}
