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

func (m *memLockoutStore) SetLock(_ context.Context, subject string, until time.Time, _ int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.locks[subject] = until
	return nil
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
