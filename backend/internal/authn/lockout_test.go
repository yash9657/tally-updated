package authn

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---- in-memory LockoutStore (hermetic; no Postgres) ----------------------

type memAttempt struct {
	ok bool
	at time.Time
}

type memLockoutStore struct {
	mu       sync.Mutex
	attempts map[string][]memAttempt
	locks    map[string]time.Time
	// ghosts are subjects with no app_user row, for which SetLock's UPDATE
	// matches zero rows. Used to exercise the no-enumeration invariant.
	ghosts map[string]bool
}

func newMemLockoutStore() *memLockoutStore {
	return &memLockoutStore{attempts: map[string][]memAttempt{}, locks: map[string]time.Time{}}
}

// now is settable so RecordAttempt timestamps align with the Locker's clock.
func (m *memLockoutStore) RecordAttemptAt(subject string, ok bool, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.attempts[subject] = append(m.attempts[subject], memAttempt{ok: ok, at: at})
}

// clock used to timestamp attempts; pointed at the Locker's clock by the test.
var memClock = func() time.Time { return time.Now() }

func (m *memLockoutStore) RecordAttempt(_ context.Context, subject string, ok bool, _ string) error {
	m.RecordAttemptAt(subject, ok, memClock())
	return nil
}

func (m *memLockoutStore) FailuresInWindow(_ context.Context, subject string, since time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, a := range m.attempts[subject] {
		if !a.ok && !a.at.Before(since) {
			n++
		}
	}
	return n, nil
}

// SetLock mirrors the PG store: it reports whether a row was actually updated.
// `ghosts` models subjects with NO app_user row (a login naming an account that
// does not exist), for which the real UPDATE matches zero rows. A nil/empty
// ghosts map means every subject exists, which is what every pre-existing test
// assumes.
func (m *memLockoutStore) SetLock(_ context.Context, subject string, until time.Time, _ int) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ghosts[subject] {
		return false, nil
	}
	m.locks[subject] = until
	return true, nil
}

func (m *memLockoutStore) ClearLock(_ context.Context, subject string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.locks, subject)
	m.attempts[subject] = nil
	return nil
}

func (m *memLockoutStore) LockedUntil(_ context.Context, subject string) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.locks[subject], nil
}

// ---- policy tests (hermetic) ---------------------------------------------

func TestLockoutDefaultsPinned(t *testing.T) {
	p := DefaultLockoutPolicy()
	if p.MaxFailures != 5 || p.Window != 15*time.Minute || p.Lock != 15*time.Minute {
		t.Fatalf("default policy = %+v, want 5/15m/15m", p)
	}
}

func TestLockoutThresholdLocksAtFifthFailure(t *testing.T) {
	ctx := context.Background()
	store := newMemLockoutStore()
	clk := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	memClock = func() time.Time { return clk }
	defer func() { memClock = func() time.Time { return time.Now() } }()

	l := NewLocker(DefaultLockoutPolicy(), store, func() time.Time { return clk })

	// Failures 1..4 do NOT lock.
	for i := 1; i <= 4; i++ {
		locked, _, err := l.RecordFailure(ctx, "alice", "1.2.3.4")
		if err != nil {
			t.Fatalf("failure %d: %v", i, err)
		}
		if locked {
			t.Fatalf("locked prematurely at failure %d", i)
		}
		if isLocked, _, _ := l.IsLocked(ctx, "alice"); isLocked {
			t.Fatalf("IsLocked true prematurely at failure %d", i)
		}
	}

	// The 5th failure crosses the threshold and locks.
	locked, until, err := l.RecordFailure(ctx, "alice", "1.2.3.4")
	if err != nil {
		t.Fatalf("5th failure: %v", err)
	}
	if !locked {
		t.Fatal("expected lock at 5th failure")
	}
	if want := clk.Add(15 * time.Minute); !until.Equal(want) {
		t.Fatalf("lock until = %v, want %v", until, want)
	}
	isLocked, lu, _ := l.IsLocked(ctx, "alice")
	if !isLocked {
		t.Fatal("IsLocked should be true after threshold")
	}
	if !lu.Equal(until) {
		t.Fatalf("IsLocked until = %v, want %v", lu, until)
	}
}

func TestLockoutSuccessResetsCounter(t *testing.T) {
	ctx := context.Background()
	store := newMemLockoutStore()
	clk := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	memClock = func() time.Time { return clk }
	defer func() { memClock = func() time.Time { return time.Now() } }()
	l := NewLocker(DefaultLockoutPolicy(), store, func() time.Time { return clk })

	// 4 failures, then a success resets.
	for i := 0; i < 4; i++ {
		if _, _, err := l.RecordFailure(ctx, "bob", ""); err != nil {
			t.Fatalf("failure: %v", err)
		}
	}
	if err := l.RecordSuccess(ctx, "bob", ""); err != nil {
		t.Fatalf("success: %v", err)
	}
	// After reset, 4 fresh failures must NOT lock (counter was zeroed).
	for i := 0; i < 4; i++ {
		locked, _, err := l.RecordFailure(ctx, "bob", "")
		if err != nil {
			t.Fatalf("post-reset failure %d: %v", i, err)
		}
		if locked {
			t.Fatalf("locked after only %d post-reset failures", i+1)
		}
	}
	if isLocked, _, _ := l.IsLocked(ctx, "bob"); isLocked {
		t.Fatal("should not be locked after reset + 4 failures")
	}
}

func TestLockoutSuccessClearsActiveLock(t *testing.T) {
	ctx := context.Background()
	store := newMemLockoutStore()
	clk := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	memClock = func() time.Time { return clk }
	defer func() { memClock = func() time.Time { return time.Now() } }()
	l := NewLocker(DefaultLockoutPolicy(), store, func() time.Time { return clk })

	for i := 0; i < 5; i++ {
		_, _, _ = l.RecordFailure(ctx, "carol", "")
	}
	if isLocked, _, _ := l.IsLocked(ctx, "carol"); !isLocked {
		t.Fatal("expected lock")
	}
	if err := l.RecordSuccess(ctx, "carol", ""); err != nil {
		t.Fatalf("success: %v", err)
	}
	if isLocked, _, _ := l.IsLocked(ctx, "carol"); isLocked {
		t.Fatal("success must clear the active lock")
	}
}

func TestLockoutWindowExpiresOldFailures(t *testing.T) {
	ctx := context.Background()
	store := newMemLockoutStore()
	clk := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	cur := clk
	memClock = func() time.Time { return cur }
	defer func() { memClock = func() time.Time { return time.Now() } }()
	l := NewLocker(DefaultLockoutPolicy(), store, func() time.Time { return cur })

	// 4 failures now.
	for i := 0; i < 4; i++ {
		_, _, _ = l.RecordFailure(ctx, "dave", "")
	}
	// Advance past the 15m window; those 4 failures fall out of scope.
	cur = clk.Add(16 * time.Minute)
	// A single fresh failure is the ONLY one in-window -> no lock.
	locked, _, err := l.RecordFailure(ctx, "dave", "")
	if err != nil {
		t.Fatalf("failure: %v", err)
	}
	if locked {
		t.Fatal("stale failures outside the window must not contribute to lockout")
	}
}

func TestLockoutExpiresAfterLockDuration(t *testing.T) {
	ctx := context.Background()
	store := newMemLockoutStore()
	clk := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	cur := clk
	memClock = func() time.Time { return cur }
	defer func() { memClock = func() time.Time { return time.Now() } }()
	l := NewLocker(DefaultLockoutPolicy(), store, func() time.Time { return cur })

	for i := 0; i < 5; i++ {
		_, _, _ = l.RecordFailure(ctx, "erin", "")
	}
	if isLocked, _, _ := l.IsLocked(ctx, "erin"); !isLocked {
		t.Fatal("expected lock")
	}
	// Advance past the lock duration.
	cur = clk.Add(16 * time.Minute)
	if isLocked, _, _ := l.IsLocked(ctx, "erin"); isLocked {
		t.Fatal("lock should have expired after the lock duration")
	}
}

// ---- Postgres-backed store test (skips if 0011 not applied) ---------------

const (
	pgAdminDSN = "postgres://postgres:postgres@localhost:5433/tally?sslmode=disable"
	pgAppDSN   = "postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable"
)

// tablePresent reports whether a table exists (used to skip when WS1's 0011
// migration is not yet applied).
func tablePresent(ctx context.Context, conn *pgx.Conn, name string) bool {
	var ok bool
	_ = conn.QueryRow(ctx, `SELECT to_regclass('public.'||$1) IS NOT NULL`, name).Scan(&ok)
	return ok
}

func TestLockoutPGStoreThresholdAndReset(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped (CORED_SKIP_DB set)")
	}
	ctx := context.Background()

	admin, err := pgx.Connect(ctx, pgAdminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	defer admin.Close(ctx)

	if !tablePresent(ctx, admin, "auth_login_attempt") || !tablePresent(ctx, admin, "app_user") {
		t.Skip("migration 0011 (auth_login_attempt/app_user) not present; WS1 authors it — skipping pg lockout test")
	}

	pool, err := pgxpool.New(ctx, pgAppDSN)
	if err != nil {
		t.Skipf("app pool unavailable: %v", err)
	}
	defer pool.Close()

	subject := "lockout-pgtest-" + time.Now().Format("150405.000000")
	// Seed an app_user row as admin so SetLock/ClearLock have a target row.
	if _, err := admin.Exec(ctx, `
		INSERT INTO app_user (id, subject, email, display_name)
		VALUES (gen_random_uuid(), $1, $2, 'Lockout PG Test')`,
		subject, subject+"@example.test"); err != nil {
		t.Fatalf("seed app_user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(ctx, `DELETE FROM auth_login_attempt WHERE subject = $1`, subject)
		_, _ = admin.Exec(ctx, `DELETE FROM app_user WHERE subject = $1`, subject)
	})

	clk := time.Now()
	store := NewPGLockoutStore(pool)
	l := NewLocker(DefaultLockoutPolicy(), store, func() time.Time { return clk })

	// 4 failures -> no lock.
	for i := 0; i < 4; i++ {
		locked, _, err := l.RecordFailure(ctx, subject, "10.0.0.1")
		if err != nil {
			t.Fatalf("pg failure %d: %v", i, err)
		}
		if locked {
			t.Fatalf("pg locked prematurely at %d", i)
		}
	}
	// 5th failure -> lock.
	locked, until, err := l.RecordFailure(ctx, subject, "10.0.0.1")
	if err != nil {
		t.Fatalf("pg 5th failure: %v", err)
	}
	if !locked {
		t.Fatal("pg: expected lock at 5th failure")
	}
	if until.IsZero() {
		t.Fatal("pg: lock until is zero")
	}
	if isLocked, _, err := l.IsLocked(ctx, subject); err != nil || !isLocked {
		t.Fatalf("pg: IsLocked=%v err=%v, want locked", isLocked, err)
	}

	// Success resets and clears the lock durably.
	if err := l.RecordSuccess(ctx, subject, "10.0.0.1"); err != nil {
		t.Fatalf("pg success: %v", err)
	}
	if isLocked, _, err := l.IsLocked(ctx, subject); err != nil || isLocked {
		t.Fatalf("pg: still locked after success: isLocked=%v err=%v", isLocked, err)
	}
	// Verify durable columns via admin.
	var failed int
	var lockedUntil *time.Time
	if err := admin.QueryRow(ctx, `SELECT failed_attempts, locked_until FROM app_user WHERE subject=$1`, subject).
		Scan(&failed, &lockedUntil); err != nil {
		t.Fatalf("pg read app_user: %v", err)
	}
	if failed != 0 || lockedUntil != nil {
		t.Fatalf("pg: after success failed_attempts=%d locked_until=%v, want 0/nil", failed, lockedUntil)
	}
}

// TestLockoutGhostSubjectIsNeverReportedLocked pins the NO-ENUMERATION
// invariant: a subject with no app_user row must never be reported as locked,
// no matter how many failures it accumulates.
//
// WHY THIS EXISTS. An adversarial review of the first-party-login increment
// found that RecordFailure decided `locked` purely from the auth_login_attempt
// counter — a table with no RLS that is written for ANY subject, existing or
// not — and ignored whether SetLock had actually updated a row. The result was a
// byte-observable user-existence oracle needing no timing measurement: from the
// 6th wrong password onward, a REAL account's 423 carries the STORED
// locked_until (identical on every subsequent request), while a ghost account's
// carries a freshly computed now+15m that ADVANCES each time. Watching whether
// `until` moves separates real subjects from ghosts.
//
// REGRESSION SENSITIVITY: revert RecordFailure to ignore SetLock's persisted
// flag and the 5th-failure assertion below fails immediately (locked=true for a
// subject that was never stored). The `real` half is the control: it proves the
// harness genuinely reaches the threshold, so this test cannot pass by simply
// never locking anything.
func TestLockoutGhostSubjectIsNeverReportedLocked(t *testing.T) {
	ctx := context.Background()
	store := newMemLockoutStore()
	store.ghosts = map[string]bool{"ghost": true}
	clk := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	memClock = func() time.Time { return clk }
	defer func() { memClock = func() time.Time { return time.Now() } }()

	l := NewLocker(DefaultLockoutPolicy(), store, func() time.Time { return clk })

	// Well past the threshold: 8 failures against a subject that does not exist.
	for i := 1; i <= 8; i++ {
		locked, until, err := l.RecordFailure(ctx, "ghost", "1.2.3.4")
		if err != nil {
			t.Fatalf("ghost failure %d: %v", i, err)
		}
		if locked {
			t.Fatalf("ENUMERATION ORACLE: ghost subject reported LOCKED at failure %d "+
				"— a 423 for a non-existent account reveals that it does not exist", i)
		}
		if !until.IsZero() {
			t.Fatalf("ghost failure %d leaked an `until` (%v); it must be the zero time "+
				"so no lock expiry is ever rendered for a subject that was not stored", i, until)
		}
	}
	if isLocked, _, _ := l.IsLocked(ctx, "ghost"); isLocked {
		t.Fatal("IsLocked must stay false for a subject with no app_user row")
	}

	// CONTROL: the very same harness DOES lock a real subject at the 5th failure,
	// so the assertions above are not passing merely because nothing ever locks.
	for i := 1; i <= 4; i++ {
		if locked, _, _ := l.RecordFailure(ctx, "real", "1.2.3.4"); locked {
			t.Fatalf("control: real subject locked prematurely at failure %d", i)
		}
	}
	locked, until, err := l.RecordFailure(ctx, "real", "1.2.3.4")
	if err != nil {
		t.Fatalf("control 5th failure: %v", err)
	}
	if !locked {
		t.Fatal("control: a REAL subject must lock at the 5th failure — the harness " +
			"never reached the threshold, so the ghost assertions proved nothing")
	}
	if want := clk.Add(15 * time.Minute); !until.Equal(want) {
		t.Fatalf("control: lock until = %v, want %v", until, want)
	}
}

// TestLockoutPGStoreSuccessResetsFailureCounter pins, AGAINST POSTGRES, the
// promise this package's doc comment has always made: "A successful login resets
// the failure counter."
//
// WHY THIS EXISTS. It was true of the in-memory fake and FALSE of the database.
// The fake's ClearLock does `m.attempts[subject] = nil`, so every hermetic test
// above agreed with the doc. The PG store's ClearLock only does
// `UPDATE app_user SET locked_until = NULL, failed_attempts = 0` — but the number
// that actually decides locking is FailuresInWindow's count over
// auth_login_attempt, which ClearLock never touched. Real users would hit a
// self-inflicted denial of service: four typos, a correct sign-in, then ONE more
// typo locked the account for 15 minutes, because the four stale failures were
// still inside the sliding window.
//
// This is the shape of bug a fake can never reveal, which is why it is asserted
// here and not upstairs.
//
// REGRESSION SENSITIVITY: drop the "AND at > (last success)" clause from
// FailuresInWindow and the FIRST post-success failure returns locked=true, so
// this fails on the very next assertion.
func TestLockoutPGStoreSuccessResetsFailureCounter(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped (CORED_SKIP_DB set)")
	}
	ctx := context.Background()

	admin, err := pgx.Connect(ctx, pgAdminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	defer admin.Close(ctx)
	if !tablePresent(ctx, admin, "auth_login_attempt") || !tablePresent(ctx, admin, "app_user") {
		t.Skip("migration 0011 not present")
	}

	pool, err := pgxpool.New(ctx, pgAppDSN)
	if err != nil {
		t.Skipf("app pool unavailable: %v", err)
	}
	defer pool.Close()

	subject := "lockout-reset-" + time.Now().Format("150405.000000")
	if _, err := admin.Exec(ctx, `
		INSERT INTO app_user (id, subject, email, display_name)
		VALUES (gen_random_uuid(), $1, $2, 'Lockout Reset Test')`,
		subject, subject+"@example.test"); err != nil {
		t.Fatalf("seed app_user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(ctx, `DELETE FROM auth_login_attempt WHERE subject = $1`, subject)
		_, _ = admin.Exec(ctx, `DELETE FROM app_user WHERE subject = $1`, subject)
	})

	clk := time.Now()
	l := NewLocker(DefaultLockoutPolicy(), NewPGLockoutStore(pool), func() time.Time { return clk })

	// Four typos — one short of the threshold.
	for i := 0; i < 4; i++ {
		if locked, _, err := l.RecordFailure(ctx, subject, "10.0.0.2"); err != nil || locked {
			t.Fatalf("failure %d: locked=%v err=%v, want not locked", i, locked, err)
		}
	}
	// The user remembers their password.
	if err := l.RecordSuccess(ctx, subject, "10.0.0.2"); err != nil {
		t.Fatalf("success: %v", err)
	}

	// A single later typo must NOT lock: the pre-success failures are spent.
	locked, _, err := l.RecordFailure(ctx, subject, "10.0.0.2")
	if err != nil {
		t.Fatalf("post-success failure: %v", err)
	}
	if locked {
		t.Fatal("SELF-INFLICTED DoS: one typo after a SUCCESSFUL login locked the " +
			"account — the failure counter did not reset in the DATABASE, so stale " +
			"pre-success failures still count toward the threshold")
	}

	// And the threshold still works from the reset baseline: it should take a
	// full five NEW failures (one already spent above) to lock.
	for i := 0; i < 3; i++ {
		if locked, _, err := l.RecordFailure(ctx, subject, "10.0.0.2"); err != nil || locked {
			t.Fatalf("post-success failure %d: locked=%v err=%v, want not locked", i+2, locked, err)
		}
	}
	locked, _, err = l.RecordFailure(ctx, subject, "10.0.0.2")
	if err != nil {
		t.Fatalf("5th post-success failure: %v", err)
	}
	if !locked {
		t.Fatal("CONTROL FAILED: five failures after the reset must still lock — " +
			"otherwise the test above proves nothing (lockout could simply be dead)")
	}
}
