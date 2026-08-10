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
	// outcome mirrors migration 0074's column: success | failure | locked |
	// unlocked. It is what FailuresInWindow filters on, because a 'locked'
	// attempt (D73) must be RECORDED and NOT COUNTED — a fake that counted it
	// would let the real store's bug pass here.
	outcome string
}

type memLockoutStore struct {
	mu       sync.Mutex
	attempts map[string][]memAttempt
	locks    map[string]time.Time
	// ghosts are subjects with no app_user row, for which SetLock's UPDATE
	// matches zero rows. Used to exercise the no-enumeration invariant.
	ghosts map[string]bool
	// The durable app_user columns the PG store writes, so the hermetic tests
	// can observe D74's running counter, D57's backoff exponent and D56's
	// revocation epoch without a database.
	failedAttempts map[string]int
	lockCount      map[string]int
	invalidBefore  map[string]time.Time
}

func newMemLockoutStore() *memLockoutStore {
	return &memLockoutStore{
		attempts:       map[string][]memAttempt{},
		locks:          map[string]time.Time{},
		failedAttempts: map[string]int{},
		lockCount:      map[string]int{},
		invalidBefore:  map[string]time.Time{},
	}
}

// now is settable so RecordAttempt timestamps align with the Locker's clock.
func (m *memLockoutStore) RecordAttemptAt(subject string, ok bool, at time.Time) {
	outcome := "failure"
	if ok {
		outcome = "success"
	}
	m.recordOutcomeAt(subject, ok, outcome, at)
}

func (m *memLockoutStore) recordOutcomeAt(subject string, ok bool, outcome string, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.attempts[subject] = append(m.attempts[subject], memAttempt{ok: ok, at: at, outcome: outcome})
}

// clock used to timestamp attempts; pointed at the Locker's clock by the test.
var memClock = func() time.Time { return time.Now() }

func (m *memLockoutStore) RecordAttempt(_ context.Context, subject string, ok bool, _ string) error {
	m.RecordAttemptAt(subject, ok, memClock())
	return nil
}

func (m *memLockoutStore) RecordLockedOutAttempt(_ context.Context, subject, _ string) error {
	m.recordOutcomeAt(subject, false, "locked", memClock())
	return nil
}

// FailuresInWindow mirrors the PG store's predicate EXACTLY, including the two
// outcomes that are recorded but not counted and the fact that an 'unlocked'
// row is a reset point. A looser fake here would make the hermetic tests agree
// with a store that does the wrong thing.
func (m *memLockoutStore) FailuresInWindow(_ context.Context, subject string, since time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var reset time.Time
	for _, a := range m.attempts[subject] {
		if (a.outcome == "success" || a.outcome == "unlocked") && a.at.After(reset) {
			reset = a.at
		}
	}
	n := 0
	for _, a := range m.attempts[subject] {
		if a.outcome != "failure" || a.at.Before(since) {
			continue
		}
		if !reset.IsZero() && !a.at.After(reset) {
			continue
		}
		n++
	}
	return n, nil
}

func (m *memLockoutStore) SetFailureCount(_ context.Context, subject string, failures int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ghosts[subject] {
		return nil // no row to count on.
	}
	m.failedAttempts[subject] = failures
	return nil
}

func (m *memLockoutStore) ConsecutiveLocks(_ context.Context, subject string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lockCount[subject], nil
}

func (m *memLockoutStore) SessionState(_ context.Context, subject string) (time.Time, time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.locks[subject], m.invalidBefore[subject], nil
}

// SetLock mirrors the PG store: it reports whether a row was actually updated.
// `ghosts` models subjects with NO app_user row (a login naming an account that
// does not exist), for which the real UPDATE matches zero rows. A nil/empty
// ghosts map means every subject exists, which is what every pre-existing test
// assumes.
func (m *memLockoutStore) SetLock(_ context.Context, subject string, at, until time.Time, failures int) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ghosts[subject] {
		return false, nil
	}
	m.locks[subject] = until
	m.failedAttempts[subject] = failures
	m.lockCount[subject]++
	// GREATEST(): the revocation epoch only ever moves forward (see the PG
	// store's SetLock for why rewinding it would resurrect dead sessions).
	if at.After(m.invalidBefore[subject]) {
		m.invalidBefore[subject] = at
	}
	return true, nil
}

// ClearLock mirrors the PG store, INCLUDING the deliberate omission: the
// revocation epoch survives. Clearing it here would let this fake agree with a
// store that hands an attacker's dead session back the moment the real owner
// signs in.
func (m *memLockoutStore) ClearLock(_ context.Context, subject string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.locks, subject)
	m.attempts[subject] = nil
	m.failedAttempts[subject] = 0
	m.lockCount[subject] = 0
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

// ===========================================================================
// D57 — THE POLICY IS CONFIGURABLE, AND MISCONFIGURING IT CANNOT LOCK THE FLEET
// ===========================================================================

// TestLockoutPolicyIsConfigurableFromTheEnvironment is D57's pin. The threshold,
// window and duration were compile-time constants with NO configuration path,
// while the plan sells them as per-deployment settings — so "the account locks
// after the configured N" was a sentence nobody could make true.
func TestLockoutPolicyIsConfigurableFromTheEnvironment(t *testing.T) {
	t.Setenv("AUTH_LOCKOUT_MAX_FAILURES", "3")
	t.Setenv("AUTH_LOCKOUT_WINDOW", "45m")
	t.Setenv("AUTH_LOCKOUT_DURATION", "2h")
	t.Setenv("AUTH_LOCKOUT_MAX_DURATION", "8h")

	p := DefaultLockoutPolicy()
	if p.MaxFailures != 3 || p.Window != 45*time.Minute || p.Lock != 2*time.Hour || p.MaxLock != 8*time.Hour {
		t.Fatalf("configured policy = %+v, want 3/45m/2h/8h — the knobs are not read", p)
	}

	// And the configured N is the N that actually LOCKS, which is the half a
	// struct-field assertion cannot reach: a policy that parsed the environment
	// and then locked at 5 anyway would pass the check above.
	ctx := context.Background()
	store := newMemLockoutStore()
	clk := time.Now()
	memClock = func() time.Time { return clk }
	// memClock is package-level: restore it, or a later test inherits this
	// test's frozen clock and fails for a reason that has nothing to do with it.
	defer func() { memClock = func() time.Time { return time.Now() } }()
	l := NewLocker(p, store, func() time.Time { return clk })
	for i := 0; i < 2; i++ {
		if locked, _, err := l.RecordFailure(ctx, "cfg-subject", ""); err != nil || locked {
			t.Fatalf("failure %d locked=%v err=%v, want not locked before the configured 3", i+1, locked, err)
		}
	}
	locked, until, err := l.RecordFailure(ctx, "cfg-subject", "")
	if err != nil || !locked {
		t.Fatalf("the CONFIGURED third failure did not lock (locked=%v err=%v)", locked, err)
	}
	if got := until.Sub(clk); got != 2*time.Hour {
		t.Fatalf("lock lasted %v, want the configured 2h", got)
	}
}

// TestLockoutPolicyRefusesAConfigurationThatWouldLockEverybody is the safety
// half of D57, and it is the reason reading security policy out of the
// environment is defensible at all.
//
// A threshold that parsed as 0 would lock EVERY account on its zeroth failed
// attempt — the entire fleet at once, with no way in to fix it, since the
// remedy (D55) needs somebody signed in. Garbage, negative and zero values must
// therefore be the DEFAULT and not an error, and not the value.
func TestLockoutPolicyRefusesAConfigurationThatWouldLockEverybody(t *testing.T) {
	for _, bad := range []string{"0", "-1", "", "five", "3.5"} {
		t.Setenv("AUTH_LOCKOUT_MAX_FAILURES", bad)
		if got := DefaultLockoutPolicy().MaxFailures; got != defaultMaxFailures {
			t.Fatalf("AUTH_LOCKOUT_MAX_FAILURES=%q gave MaxFailures=%d, want the default %d. "+
				"A threshold of %d locks every account in the fleet on its %s failed attempt",
				bad, got, defaultMaxFailures, got, map[bool]string{true: "zeroth", false: "next"}[got <= 0])
		}
	}
	for _, bad := range []string{"0", "-5m", "", "15", "fifteen"} {
		t.Setenv("AUTH_LOCKOUT_WINDOW", bad)
		t.Setenv("AUTH_LOCKOUT_DURATION", bad)
		p := DefaultLockoutPolicy()
		if p.Window != defaultWindow {
			t.Fatalf("AUTH_LOCKOUT_WINDOW=%q gave %v, want the default %v. A zero window "+
				"means no failure is ever recent, i.e. lockout silently OFF", bad, p.Window, defaultWindow)
		}
		if p.Lock != defaultLock {
			t.Fatalf("AUTH_LOCKOUT_DURATION=%q gave %v, want the default %v. A zero lock "+
				"is a lock that has already expired", bad, p.Lock, defaultLock)
		}
	}
}

// TestLockoutBacksOffExponentiallyAndIsCapped pins the other half of D57: the
// design docs promise "configurable failed-attempt lockout, exponential
// backoff" and there was none, so a fixed fifteen-minute lock was a rate limit
// rather than a stop — an attacker with a word list simply came back, forever,
// at five guesses a quarter hour.
//
// The FIRST lock is deliberately unchanged (that is the one a real operator who
// mistyped their password experiences), and the cap is asserted because an
// uncapped doubling is a permanent denial of service an ATTACKER inflicts on a
// VICTIM.
func TestLockoutBacksOffExponentiallyAndIsCapped(t *testing.T) {
	p := LockoutPolicy{MaxFailures: 2, Window: time.Hour, Lock: 15 * time.Minute, MaxLock: time.Hour}
	for _, tc := range []struct {
		priorLocks int
		want       time.Duration
	}{
		{0, 15 * time.Minute},  // the honest typo: unchanged
		{1, 30 * time.Minute},
		{2, time.Hour},         // reaches the cap
		{3, time.Hour},         // and stays there
		{50, time.Hour},        // no overflow, no lock measured in years
	} {
		if got := p.lockDuration(tc.priorLocks); got != tc.want {
			t.Fatalf("lockDuration(%d) = %v, want %v", tc.priorLocks, got, tc.want)
		}
	}

	// MaxLock == 0 means "do not back off", which is a real deployment choice.
	off := LockoutPolicy{MaxFailures: 2, Window: time.Hour, Lock: 15 * time.Minute}
	if got := off.lockDuration(9); got != 15*time.Minute {
		t.Fatalf("with backoff off, lockDuration(9) = %v, want the flat 15m", got)
	}
}

// TestSecondLockLastsLongerThanTheFirst drives the backoff through the Locker,
// because lockDuration being right is not the same as the Locker CALLING it
// with the right exponent.
func TestSecondLockLastsLongerThanTheFirst(t *testing.T) {
	ctx := context.Background()
	store := newMemLockoutStore()
	clk := time.Now()
	memClock = func() time.Time { return clk }
	// memClock is package-level: restore it, or a later test inherits this
	// test's frozen clock and fails for a reason that has nothing to do with it.
	defer func() { memClock = func() time.Time { return time.Now() } }()
	l := NewLocker(LockoutPolicy{MaxFailures: 2, Window: time.Hour, Lock: 10 * time.Minute, MaxLock: time.Hour},
		store, func() time.Time { return clk })

	_, _, _ = l.RecordFailure(ctx, "backoff", "")
	locked, first, _ := l.RecordFailure(ctx, "backoff", "")
	if !locked || first.Sub(clk) != 10*time.Minute {
		t.Fatalf("first lock = %v (locked=%v), want 10m", first.Sub(clk), locked)
	}

	// The lock expires and the attacker comes straight back. ONE more failure is
	// enough to re-lock, because the earlier failures are still inside the
	// sliding window — which is the sliding window working, not a bug, and is
	// exactly why the SECOND lock must cost the attacker more than the first.
	clk = clk.Add(11 * time.Minute)
	locked, second, _ := l.RecordFailure(ctx, "backoff", "")
	if !locked {
		t.Fatal("the second campaign did not lock at all")
	}
	if got := second.Sub(clk); got != 20*time.Minute {
		t.Fatalf("second lock = %v, want 20m. Without backoff a fixed lock is a rate "+
			"limit, not a stop: the same word list simply resumes every quarter hour", got)
	}
}

// ===========================================================================
// D74 — failed_attempts IS A RUNNING COUNTER, NOT A TOMBSTONE
// ===========================================================================

// TestFailedAttemptsIsARunningCounter pins D74. The column was written ONLY
// inside SetLock, i.e. only at the instant the lock tripped, so it read 0
// through failures 1-4: an account four fifths of the way to being locked was
// indistinguishable from one nobody had ever touched, and the admin screen that
// now shows this number would have shown a zero throughout an attack.
func TestFailedAttemptsIsARunningCounter(t *testing.T) {
	ctx := context.Background()
	store := newMemLockoutStore()
	clk := time.Now()
	memClock = func() time.Time { return clk }
	// memClock is package-level: restore it, or a later test inherits this
	// test's frozen clock and fails for a reason that has nothing to do with it.
	defer func() { memClock = func() time.Time { return time.Now() } }()
	l := NewLocker(LockoutPolicy{MaxFailures: 5, Window: time.Hour, Lock: time.Hour},
		store, func() time.Time { return clk })

	for i := 1; i <= 4; i++ {
		if _, _, err := l.RecordFailure(ctx, "counter", ""); err != nil {
			t.Fatalf("failure %d: %v", i, err)
		}
		store.mu.Lock()
		got := store.failedAttempts["counter"]
		store.mu.Unlock()
		if got != i {
			t.Fatalf("after failure %d the durable counter reads %d, want %d. Nothing "+
				"can observe an account partway to being locked", i, got, i)
		}
	}

	// A success resets it, or the number would be a lifetime total rather than
	// "how close is this account to being locked right now".
	if err := l.RecordSuccess(ctx, "counter", ""); err != nil {
		t.Fatalf("success: %v", err)
	}
	store.mu.Lock()
	got := store.failedAttempts["counter"]
	store.mu.Unlock()
	if got != 0 {
		t.Fatalf("after a successful login the counter reads %d, want 0", got)
	}
}

// ===========================================================================
// D73 — ATTEMPTS MADE WHILE LOCKED ARE AUDITED, AND NEVER COUNTED
// ===========================================================================

// TestLockedOutAttemptsAreRecordedButNeverCounted is the two-sided pin. The
// RECORDING is D73 (the traffic during a lock window left no trace at all); the
// NOT-COUNTING is what stops the fix creating a worse bug than the one it
// closes — an attempt refused before any password was looked at must not renew
// the lock, or a brute-forcer keeps their victim locked out forever simply by
// continuing to knock.
func TestLockedOutAttemptsAreRecordedButNeverCounted(t *testing.T) {
	ctx := context.Background()
	store := newMemLockoutStore()
	clk := time.Now()
	memClock = func() time.Time { return clk }
	// memClock is package-level: restore it, or a later test inherits this
	// test's frozen clock and fails for a reason that has nothing to do with it.
	defer func() { memClock = func() time.Time { return time.Now() } }()
	l := NewLocker(LockoutPolicy{MaxFailures: 3, Window: time.Hour, Lock: 10 * time.Minute},
		store, func() time.Time { return clk })

	before, _ := store.FailuresInWindow(ctx, "knocker", clk.Add(-time.Hour))
	for i := 0; i < 20; i++ {
		if err := l.RecordLockedOutAttempt(ctx, "knocker", "10.0.0.9"); err != nil {
			t.Fatalf("record locked-out attempt %d: %v", i, err)
		}
	}

	// RECORDED.
	store.mu.Lock()
	n := len(store.attempts["knocker"])
	store.mu.Unlock()
	if n != 20 {
		t.Fatalf("the audit trail holds %d of 20 attempts made during the lock window. "+
			"The exact period an investigator cares about must not be silent", n)
	}

	// NOT COUNTED.
	after, _ := store.FailuresInWindow(ctx, "knocker", clk.Add(-time.Hour))
	if after != before {
		t.Fatalf("twenty attempts made WHILE LOCKED moved the failure count from %d to %d. "+
			"They never reached a password check; counting them lets an attacker renew "+
			"their victim's lock indefinitely just by knocking, and re-locks an operator "+
			"who kept typing during their fifteen minutes the moment it expires", before, after)
	}
}
