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
	"time"
)

// LockoutPolicy parameterises the account-lockout behaviour.
type LockoutPolicy struct {
	MaxFailures int           // failures within Window that trigger a lock.
	Window      time.Duration // sliding window over recent attempts.
	Lock        time.Duration // how long the account stays locked.
}

// DefaultLockoutPolicy is 5 failures within 15 minutes -> locked for 15 minutes.
func DefaultLockoutPolicy() LockoutPolicy {
	return LockoutPolicy{
		MaxFailures: 5,
		Window:      15 * time.Minute,
		Lock:        15 * time.Minute,
	}
}

// LockoutStore is the persistence boundary for lockout. It is backed by
// auth_login_attempt (audit) and app_user (locked_until/failed_attempts).
type LockoutStore interface {
	// RecordAttempt appends a login attempt to the audit trail. ip may be empty.
	RecordAttempt(ctx context.Context, subject string, ok bool, ip string) error
	// FailuresInWindow returns the number of failed attempts for subject since
	// `since` (i.e. within the sliding window).
	FailuresInWindow(ctx context.Context, subject string, since time.Time) (int, error)
	// SetLock locks the subject's app_user row until `until` and records the
	// observed failure count.
	//
	// It reports whether the lock was actually PERSISTED. A subject with no
	// app_user row (a login attempt naming an account that does not exist) has
	// nothing to lock, so persisted is false. Callers must not report such a
	// subject as locked — see RecordFailure, where conflating the two is a
	// user-enumeration oracle.
	SetLock(ctx context.Context, subject string, until time.Time, failures int) (persisted bool, err error)
	// ClearLock resets failed_attempts and locked_until on success.
	ClearLock(ctx context.Context, subject string) error
	// LockedUntil reports the subject's current lock expiry (zero time if not
	// locked / unknown subject).
	LockedUntil(ctx context.Context, subject string) (time.Time, error)
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

// RecordSuccess records a successful login and clears any failure state/lock.
func (l *Locker) RecordSuccess(ctx context.Context, subject, ip string) error {
	if err := l.store.RecordAttempt(ctx, subject, true, ip); err != nil {
		return err
	}
	return l.store.ClearLock(ctx, subject)
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
	if failures >= l.policy.MaxFailures {
		until = now.Add(l.policy.Lock)
		persisted, err := l.store.SetLock(ctx, subject, until, failures)
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
