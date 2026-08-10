// Package authn provides authentication primitives for cored. This file
// (WS2-owned) implements account lockout, which is self-contained and does NOT
// depend on the token Claims/Signer/Verifier types owned by authn.go (WS1).
//
// Lockout policy: after MaxFailures failed login attempts within Window, the
// subject is locked for Lock. A successful login resets the failure counter and
// clears any lock. The audit trail lives in auth_login_attempt; the durable
// lock state lives on app_user.locked_until / app_user.failed_attempts
// (migration 0011, authored by WS1).
package authn

import (
	"context"
	"os"
	"strconv"
	"time"
)

// LockoutPolicy parameterises the account-lockout behaviour.
type LockoutPolicy struct {
	MaxFailures int           // failures within Window that trigger a lock.
	Window      time.Duration // sliding window over recent attempts.
	Lock        time.Duration // how long the FIRST lock lasts.
	// MaxLock caps the exponential backoff below. Zero disables backoff
	// entirely, so every lock lasts exactly Lock.
	MaxLock time.Duration
}

// Built-in defaults. They are the values this product shipped with, and they
// remain the answer when nothing is configured — an operator who sets no
// environment variable must get the behaviour that was pinned before D57.
const (
	defaultMaxFailures = 5
	defaultWindow      = 15 * time.Minute
	defaultLock        = 15 * time.Minute
	defaultMaxLock     = 24 * time.Hour
)

// DefaultLockoutPolicy is 5 failures within 15 minutes -> locked for 15
// minutes, doubling on each consecutive lock up to 24 hours.
//
// ⛔ THE NAME IS NOW A LIE ONLY IF YOU READ IT AS "CONSTANT". D57: the plan
// sells the threshold, window and duration as per-deployment configuration
// ("configurable failed-attempt lockout, exponential backoff" —
// RESEARCH.md:2285), and they were compile-time constants with NO configuration
// path at all, so "the account locks after the configured N" was a sentence
// nobody could make true. They are read from the environment here, at the ONE
// place the policy is constructed (server.go's authn.NewLocker call), so no
// caller has to change and no second source of truth exists:
//
//	AUTH_LOCKOUT_MAX_FAILURES   integer, failures inside the window (default 5)
//	AUTH_LOCKOUT_WINDOW         Go duration, the sliding window   (default 15m)
//	AUTH_LOCKOUT_DURATION       Go duration, the first lock        (default 15m)
//	AUTH_LOCKOUT_MAX_DURATION   Go duration, the backoff cap       (default 24h)
//	                            "0" turns backoff OFF (every lock is DURATION)
//
// ⛔⛔ EVERY KNOB FAILS CLOSED TOWARDS THE DEFAULT, AND THAT IS THE WHOLE
// SAFETY ARGUMENT FOR READING SECURITY POLICY OUT OF THE ENVIRONMENT. A typo in
// AUTH_LOCKOUT_MAX_FAILURES that parsed as 0 would lock every account on its
// FIRST failed attempt — including every account in the fleet, all at once,
// with no way in to fix it (D55's remedy needs somebody signed in). One that
// parsed as a negative or as garbage is the same class. So an unset, unparseable
// or out-of-range value is not an error and is not honoured: it is the default,
// and the deployment behaves exactly as it did before anyone touched it. There
// is deliberately NO per-COMPANY setting: a lock lives on app_user and is keyed
// on the SUBJECT, and one person's account is one account however many companies
// they can open — a per-company threshold would mean the strictest company in
// the fleet silently governed everyone's sign-in.
func DefaultLockoutPolicy() LockoutPolicy {
	return LockoutPolicy{
		// A threshold below 1 would lock on the zeroth failure; there is no
		// sane upper bound, so only the floor is enforced.
		MaxFailures: envInt("AUTH_LOCKOUT_MAX_FAILURES", defaultMaxFailures, 1),
		// A window of zero would mean "no failure is ever recent", i.e. lockout
		// off. Turning a security control off must be a deliberate act, not a
		// mistyped duration, so it is refused here.
		Window: envDuration("AUTH_LOCKOUT_WINDOW", defaultWindow, time.Second),
		Lock:   envDuration("AUTH_LOCKOUT_DURATION", defaultLock, time.Second),
		// The cap is the ONE knob where zero is meaningful: it means "do not
		// back off", which is a real deployment choice and not a mistake.
		MaxLock: envDuration("AUTH_LOCKOUT_MAX_DURATION", defaultMaxLock, 0),
	}
}

// envInt reads an integer knob, falling back to def unless the value parses AND
// is at least min. See DefaultLockoutPolicy for why this is not an error path.
func envInt(key string, def, min int) int {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < min {
		return def
	}
	return n
}

// envDuration reads a Go duration knob (e.g. "30m", "2h"), falling back to def
// unless the value parses AND is at least min.
func envDuration(key string, def, min time.Duration) time.Duration {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < min {
		return def
	}
	return d
}

// lockDuration is how long the NEXT lock lasts given how many times this
// subject has already been locked without an intervening success or unlock.
//
// The doubling is the "exponential backoff" the design docs promise beside the
// threshold (RESEARCH.md:2303). It matters because a fixed 15-minute lock is a
// rate limit, not a stop: an attacker with a word list simply pauses and comes
// back, forever, at 5 guesses per quarter hour. Doubling turns the same
// campaign into hours and then days, while the FIRST lock — the one a real
// operator who mistyped their password actually experiences — is unchanged.
//
// ⚠️ THE CAP IS NOT A DETAIL. Without one, a subject under sustained attack
// reaches a lock measured in years, which is a permanent denial of service
// inflicted BY the attacker ON the victim; with MaxLock the worst an attacker
// can buy is a day, and D55's unlock ends it sooner.
func (p LockoutPolicy) lockDuration(priorLocks int) time.Duration {
	if p.MaxLock <= 0 || priorLocks <= 0 {
		return p.Lock
	}
	d := p.Lock
	for i := 0; i < priorLocks; i++ {
		d *= 2
		if d >= p.MaxLock {
			return p.MaxLock
		}
	}
	return d
}

// LockoutStore is the persistence boundary for lockout. It is backed by
// auth_login_attempt (audit) and app_user (locked_until/failed_attempts/
// lock_count/sessions_invalid_before).
type LockoutStore interface {
	// RecordAttempt appends a login attempt to the audit trail. ip may be empty.
	RecordAttempt(ctx context.Context, subject string, ok bool, ip string) error
	// RecordLockedOutAttempt appends an attempt that was refused BECAUSE the
	// account was already locked — no password was looked at.
	//
	// D73: this used not to exist and the 423 branch wrote nothing, so the
	// traffic during a lock window, which is the exact period an investigator
	// cares about, left NO trace. It is deliberately a separate method rather
	// than RecordAttempt(ok=false): these attempts must be RECORDED and NOT
	// COUNTED, or the lock renews itself off its own refusals (see the
	// migration's note on auth_login_attempt.outcome).
	RecordLockedOutAttempt(ctx context.Context, subject, ip string) error
	// FailuresInWindow returns the number of failed attempts for subject since
	// `since` (i.e. within the sliding window).
	FailuresInWindow(ctx context.Context, subject string, since time.Time) (int, error)
	// SetFailureCount writes the running failure count onto the subject's
	// app_user row. D74: failed_attempts used to be written ONLY at the instant
	// the lock tripped, so it read 0 through failures 1-4 and nothing —
	// no screen, no query, no investigator — could observe an account partway
	// to being locked. It reports nothing: an unknown subject simply has no row
	// to count on, which is not an error (see SetLock's `persisted`).
	SetFailureCount(ctx context.Context, subject string, failures int) error
	// ConsecutiveLocks reports how many times the subject has been locked since
	// their last success or administrative unlock, for the exponential backoff.
	// An unknown subject is 0.
	ConsecutiveLocks(ctx context.Context, subject string) (int, error)
	// SetLock locks the subject's app_user row until `until`, records the
	// observed failure count, counts the lock, and ADVANCES THE REVOCATION
	// EPOCH to `at` — which is the whole of D56's durable half: locking an
	// account must kill the bearers it already holds, not merely refuse the
	// next login. The epoch only ever moves forward.
	//
	// It reports whether the lock was actually PERSISTED. A subject with no
	// app_user row (a login attempt naming an account that does not exist) has
	// nothing to lock, so persisted is false. Callers must not report such a
	// subject as locked — see RecordFailure, where conflating the two is a
	// user-enumeration oracle.
	SetLock(ctx context.Context, subject string, at, until time.Time, failures int) (persisted bool, err error)
	// ClearLock resets failed_attempts and locked_until on success.
	ClearLock(ctx context.Context, subject string) error
	// LockedUntil reports the subject's current lock expiry (zero time if not
	// locked / unknown subject).
	LockedUntil(ctx context.Context, subject string) (time.Time, error)
	// SessionState reports (lock expiry, revocation epoch) in ONE round trip.
	// Both are the zero time for an unknown subject. It exists as its own
	// method, rather than two calls to the two above, because it is on the
	// AUTHENTICATED HOT PATH — every request in the product asks it once.
	SessionState(ctx context.Context, subject string) (lockedUntil, invalidBefore time.Time, err error)
}

// Locker enforces a LockoutPolicy over a LockoutStore. Time is injected via Now
// so tests are deterministic.
type Locker struct {
	policy LockoutPolicy
	store  LockoutStore
	Now    func() time.Time
}

// NewLocker constructs a Locker with the given policy and store. If now is nil,
// time.Now is used.
func NewLocker(policy LockoutPolicy, store LockoutStore, now func() time.Time) *Locker {
	if now == nil {
		now = time.Now
	}
	return &Locker{policy: policy, store: store, Now: now}
}

// IsLocked reports whether the subject is currently locked and, if so, until
// when. A lock whose expiry is in the past is treated as not locked.
func (l *Locker) IsLocked(ctx context.Context, subject string) (bool, time.Time, error) {
	until, err := l.store.LockedUntil(ctx, subject)
	if err != nil {
		return false, time.Time{}, err
	}
	if until.IsZero() {
		return false, time.Time{}, nil
	}
	if !until.After(l.Now()) {
		return false, until, nil
	}
	return true, until, nil
}

// SessionRevoked answers, for ONE verified token, the question every
// authenticated route must ask before it does anything: may the credential
// behind this token still act?
//
// ==========================================================================
// D56 — LOCKING AN ACCOUNT DID NOT REVOKE THE SESSIONS IT ALREADY HELD
// ==========================================================================
// `locked_until` was consulted on exactly ONE code path, POST /auth/login. So
// locking an account stopped the attacker signing in AGAIN and did nothing
// whatever about the bearer already in their hand: it kept working for the full
// 3600-second TTL, on reads AND on authenticated WRITES — a locked account
// posted a voucher and got 201.
//
// ⛔ THE PROPERTY, NOT A LIST OF ROUTES. "A locked subject can perform no
// authenticated request" is asserted HERE, at the single seam every
// authenticated route passes through (server.Server.authenticate), rather than
// as a check added to the handlers somebody remembered. A route added tomorrow
// inherits it by construction; a route that did not call a list-based check
// would not.
//
// TWO CLAUSES, AND BOTH ARE LOAD-BEARING:
//
//	CURRENTLY LOCKED   refuses everything for as long as the lock lasts,
//	                   whatever minted the token — including a token minted
//	                   AFTER the lock by some other door (the dev-gated
//	                   POST /auth/token, an external IdP, a WebAuthn step-up).
//	                   Without it, the epoch alone would let a freshly minted
//	                   token walk past a live lock.
//	ISSUED BEFORE THE  refuses the sessions the lock killed even after the lock
//	EPOCH              EXPIRES. Without it, a 15-minute lock would merely PAUSE
//	                   a stolen session for 15 minutes and then hand it back —
//	                   which is not revocation, it is an inconvenience. The
//	                   epoch is never rewound, so those tokens are dead for
//	                   good; the owner simply signs in again and gets a new one.
//
// SECOND-GRANULARITY, AND IT FAILS CLOSED. `issuedAt` is a JWT `iat`, i.e.
// whole seconds, while the epoch is a real timestamp; comparing with <= against
// the epoch's floor means a token minted in the SAME second as the lock is
// treated as older than it. That is the safe direction: the alternative
// keeps a session alive across a lock that happened the same second.
func (l *Locker) SessionRevoked(ctx context.Context, subject string, issuedAt int64) (bool, error) {
	lockedUntil, invalidBefore, err := l.store.SessionState(ctx, subject)
	if err != nil {
		return false, err
	}
	if !lockedUntil.IsZero() && lockedUntil.After(l.Now()) {
		return true, nil
	}
	if !invalidBefore.IsZero() && issuedAt <= invalidBefore.Unix() {
		return true, nil
	}
	return false, nil
}

// RecordSuccess records a successful login and clears any failure state/lock.
func (l *Locker) RecordSuccess(ctx context.Context, subject, ip string) error {
	if err := l.store.RecordAttempt(ctx, subject, true, ip); err != nil {
		return err
	}
	return l.store.ClearLock(ctx, subject)
}

// RecordLockedOutAttempt audits a sign-in attempt made WHILE the account is
// locked (D73). It changes no lock state and returns nothing to decide with:
// the caller has already decided to refuse, and this only makes the refusal
// visible afterwards.
//
// ⛔ IT IS NOT RecordFailure. A locked-out attempt never reached a password
// check, so counting it toward the threshold would let a brute-forcer keep
// their victim locked forever by continuing to knock, and would re-lock an
// operator who kept typing during their fifteen minutes the moment the lock
// expired. Recorded, not counted — see the migration's outcome column.
func (l *Locker) RecordLockedOutAttempt(ctx context.Context, subject, ip string) error {
	return l.store.RecordLockedOutAttempt(ctx, subject, ip)
}

// RecordFailure records a failed login. If the number of failures within the
// window reaches MaxFailures, the subject is locked for Lock. It returns whether
// the account is now locked and until when.
//
// NO-ENUMERATION INVARIANT: a lock is only REPORTED when the store confirms it
// was persisted. A subject with no app_user row has nothing to lock, and saying
// "locked" for it would be a user-existence oracle that needs no timing
// measurement at all — the caller writes 423 with an `until`, and for a
// non-existent account that `until` would advance on every request (freshly
// computed each time) while a real account's stays fixed at the stored value.
// An attacker could separate real subjects from ghosts by sending six wrong
// passwords and watching whether `until` moves. Gating on `persisted` keeps a
// ghost subject on the same byte-identical 401 as every other failure.
func (l *Locker) RecordFailure(ctx context.Context, subject, ip string) (locked bool, until time.Time, err error) {
	if err := l.store.RecordAttempt(ctx, subject, false, ip); err != nil {
		return false, time.Time{}, err
	}
	now := l.Now()
	since := now.Add(-l.policy.Window)
	failures, err := l.store.FailuresInWindow(ctx, subject, since)
	if err != nil {
		return false, time.Time{}, err
	}
	// D74 — THE COUNTER IS RUNNING, NOT A TOMBSTONE. failed_attempts used to be
	// written only inside SetLock, i.e. only at the instant the lock tripped, so
	// it read 0 through failures 1-4 and an account four fifths of the way to
	// being locked was indistinguishable from one nobody had ever touched. The
	// admin screen now shows this number, and "3 failed sign-in attempts" is the
	// difference between noticing an attack and reading about it afterwards.
	//
	// A failure here is deliberately NOT fatal: this write is OBSERVABILITY, and
	// refusing the login with a 500 because the counter could not be updated
	// would turn a bookkeeping problem into an outage on the sign-in path. The
	// count that DECIDES locking is FailuresInWindow over auth_login_attempt,
	// which is already committed above.
	_ = l.store.SetFailureCount(ctx, subject, failures)
	if failures >= l.policy.MaxFailures {
		// How long this lock lasts depends on how many times we have locked this
		// subject already (exponential backoff, D57). An unknown subject, or a
		// store that cannot answer, backs off to the base duration — never
		// longer, so a read failure can never lengthen somebody's lock.
		priorLocks, cerr := l.store.ConsecutiveLocks(ctx, subject)
		if cerr != nil {
			priorLocks = 0
		}
		until = now.Add(l.policy.lockDuration(priorLocks))
		persisted, err := l.store.SetLock(ctx, subject, now, until, failures)
		if err != nil {
			return false, time.Time{}, err
		}
		if !persisted {
			// Unknown subject: nothing was locked, so report nothing.
			return false, time.Time{}, nil
		}
		return true, until, nil
	}
	return false, time.Time{}, nil
}
