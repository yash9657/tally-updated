// auth_unlock_test.go — D55 (there was NO way to unlock an account), plus the
// two audit defects that live on the same rows: D73 (attempts made while locked
// were never recorded) and D74 (failed_attempts was not a running counter).
//
// ⛔ EVERY SUBJECT LOCKED HERE IS CREATED BY THE TEST AND DELETED AFTERWARDS.
// The lock lives on app_user keyed by SUBJECT, so a wrong password submitted
// for `browser-dev-owner` would lock every other lane out of the whole product.
package server_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/server"
)

// lockState reads the durable lock columns over the ADMIN DSN — app_user is
// FORCE-RLS, so the application role can only ever see its own row and an
// oracle through it would be vacuous.
func lockState(t *testing.T, subject string) (until *time.Time, failed, lockCount int, epoch *time.Time) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if err := conn.QueryRow(ctx, `
		SELECT locked_until, failed_attempts, lock_count, sessions_invalid_before
		  FROM app_user WHERE subject = $1`, subject).
		Scan(&until, &failed, &lockCount, &epoch); err != nil {
		t.Fatalf("read lock state for %q: %v", subject, err)
	}
	return until, failed, lockCount, epoch
}

// attemptsByOutcome counts audited attempts per outcome.
func attemptsByOutcome(t *testing.T, subject string) map[string]int {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	rows, err := conn.Query(ctx,
		`SELECT outcome, count(*) FROM auth_login_attempt WHERE subject = $1 GROUP BY outcome`, subject)
	if err != nil {
		t.Fatalf("count attempts: %v", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var o string
		var n int
		if err := rows.Scan(&o, &n); err != nil {
			t.Fatalf("scan attempts: %v", err)
		}
		out[o] = n
	}
	return out
}

// TestAnAdministratorCanUnlockALockedMember is D55's pin, end to end over the
// real router: an Owner sees the lock on the members list and clears it.
//
// Before this, a locked account was a dead end. `ClearLock` was reachable ONLY
// from a successful login, which a locked account cannot perform; Users & Roles
// — the only member-admin surface in the product — rendered no lock state at
// all, so the administrator's screen said the person had access while the
// person could not sign in; and POST /auth/password, the one route that might
// have been a remedy, has no caller anywhere in the app and needs a Bearer the
// locked user can no longer use.
//
//	SETUP    invite a throwaway subject, who claims it via GET /v1/me
//	LOCK     five wrong passwords for THAT subject           -> 423
//	SEE IT   GET /members                                    -> locked_until set
//	FIX IT   PUT /members/{userID} {"unlock":true}           -> 200
//	ORACLE   the DATABASE says locked_until IS NULL, and the audit trail holds
//	         the 'unlocked' row, and the person can SIGN IN AGAIN
func TestAnAdministratorCanUnlockALockedMember(t *testing.T) {
	srv := newServer(t)
	owner := ownerToken(t, srv)
	companyID := createCompanyAs(t, srv, owner)
	base := "/v1/companies/" + companyID

	member := throwawaySubject(t, "unlock-me")
	memberTok := mintToken(t, srv, member, "webauthn")

	// The invitee materialises their own membership on their next GET /v1/me —
	// an admin never writes another person's identity row.
	rec, _ := doRaw(t, srv, http.MethodPost, base+"/members/invites", owner, map[string]any{
		"subject": member, "security_level_name": "Data Entry",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	rec, _ = doRaw(t, srv, http.MethodGet, "/v1/me", memberTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("claim (GET /v1/me) = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	// The member's password, so the sign-in leg at the end is a REAL sign-in
	// rather than an assertion about a status code.
	provisionLogin(t, srv, member, testPassword)

	// ---- CONTROL: they can sign in, and the screen shows no lock ----------
	if code, _, raw := login(t, srv, member, testPassword); code != http.StatusOK {
		t.Fatalf("CONTROL: the member cannot sign in even before being locked: %d (%s)", code, raw)
	}
	if m := memberRow(t, srv, base, owner, member); m["locked_until"] != nil {
		t.Fatalf("CONTROL: the members list already reports a lock (%v) before anything locked them", m["locked_until"])
	}

	// ---- LOCK -------------------------------------------------------------
	lockByFailedLogins(t, srv, member)

	// ---- THE ADMINISTRATOR CAN SEE IT (D55's first half) ------------------
	m := memberRow(t, srv, base, owner, member)
	if m["locked_until"] == nil || m["locked_until"] == "" {
		t.Fatal("D55: the member is LOCKED OUT and Users & Roles' own data says nothing " +
			"about it. The administrator's screen reports access the person does not have")
	}
	if fa, _ := m["failed_attempts"].(float64); fa < 1 {
		t.Fatalf("D74: the members list reports failed_attempts=%v for an account that "+
			"was just locked by repeated failures", m["failed_attempts"])
	}

	// ---- THE ADMINISTRATOR CAN FIX IT (D55's second half) -----------------
	rec, body := doRaw(t, srv, http.MethodPut, base+"/members/"+memberUserID(t, srv, base, owner, member),
		owner, map[string]any{"unlock": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("D55: PUT /members/{userID} {\"unlock\":true} = %d, want 200 (%s)",
			rec.Code, rec.Body.String())
	}
	if body["lock"] != "cleared" {
		t.Fatalf("unlock response = %v, want lock=cleared", body)
	}

	// ---- ORACLE: THE DATABASE, NOT THE STATUS CODE ------------------------
	until, failed, lockCount, epoch := lockState(t, member)
	if until != nil {
		t.Fatalf("⛔ THE 200 CHANGED NOTHING: locked_until is still %v. A route that "+
			"answers 200 and leaves the lock in place is worse than no route", until)
	}
	if failed != 0 || lockCount != 0 {
		t.Fatalf("after the unlock failed_attempts=%d lock_count=%d, want 0/0 — an "+
			"account unlocked with its counters intact is one typo from re-locking",
			failed, lockCount)
	}
	// D56's asymmetry: the sessions the lock revoked stay revoked.
	if epoch == nil {
		t.Fatal("the unlock cleared the REVOCATION EPOCH. Unlocking means \"you may " +
			"sign in again\", not \"the bearer somebody stole from you works again\"")
	}
	if outcomes := attemptsByOutcome(t, member); outcomes["unlocked"] != 1 {
		t.Fatalf("the administrative unlock left no audit row: outcomes = %v", outcomes)
	}

	// ---- AND THE PERSON CAN ACTUALLY GET BACK IN --------------------------
	if code, _, raw := login(t, srv, member, testPassword); code != http.StatusOK {
		t.Fatalf("after the unlock the member still cannot sign in: %d (%s)", code, raw)
	}
	// ONE typo must not put them straight back in jail: the unlock is a reset
	// point for the failure window, or the five failures still sitting inside it
	// would re-lock the account instantly and the remedy would look broken.
	if code, _, _ := login(t, srv, member, "one-more-typo"); code == http.StatusLocked {
		t.Fatal("a SINGLE typo after an administrative unlock re-locked the account: " +
			"the unlock did not reset the failure window, so the remedy lasts exactly " +
			"one mistake")
	}
}

// memberRow finds one member in GET /members.
func memberRow(t *testing.T, srv *server.Server, base, token, subject string) map[string]any {
	t.Helper()
	rec, body := doRaw(t, srv, http.MethodGet, base+"/members", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /members = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	members, _ := body["members"].([]any)
	for _, raw := range members {
		m, _ := raw.(map[string]any)
		if m["subject"] == subject {
			return m
		}
	}
	t.Fatalf("subject %q is not in the members list (%s)", subject, rec.Body.String())
	return nil
}

func memberUserID(t *testing.T, srv *server.Server, base, token, subject string) string {
	t.Helper()
	id, _ := memberRow(t, srv, base, token, subject)["user_id"].(string)
	if id == "" {
		t.Fatalf("no user_id for %q", subject)
	}
	return id
}

// TestUnlockRefusesAMemberOfAnotherCompany is the containment pin for the
// SECURITY DEFINER function the unlock goes through. It bypasses RLS by
// construction, so if it took the caller's word for which company they were
// administering it would be a GLOBAL unlock oracle wearing an admin's clothes:
// any Owner of any company could free any locked account in the deployment.
func TestUnlockRefusesAMemberOfAnotherCompany(t *testing.T) {
	srv := newServer(t)
	owner := ownerToken(t, srv)
	mine := createCompanyAs(t, srv, owner)

	// A locked subject who is a member of NO company of mine.
	stranger := throwawaySubject(t, "unlock-stranger")
	strangerTok := mintToken(t, srv, stranger, "webauthn")
	_ = createCompanyAs(t, srv, strangerTok) // gives them an app_user row + their own company
	lockByFailedLogins(t, srv, stranger)

	before, _, _, _ := lockState(t, stranger)
	if before == nil {
		t.Fatal("fixture: the stranger is not locked, so the refusal below proves nothing")
	}

	strangerID := userIDOf(t, stranger)
	rec, _ := doRaw(t, srv, http.MethodPut, "/v1/companies/"+mine+"/members/"+strangerID,
		owner, map[string]any{"unlock": true})
	if rec.Code == http.StatusOK {
		t.Fatal("an Owner unlocked an account belonging to a company they do not " +
			"administer: the SECURITY DEFINER unlock is a global oracle")
	}
	after, _, _, _ := lockState(t, stranger)
	if after == nil {
		t.Fatal("⛔ THE REFUSAL STILL WROTE: the response was not a 200 and the lock " +
			"is gone anyway")
	}
}

// userIDOf resolves a subject to its app_user id over the admin DSN.
func userIDOf(t *testing.T, subject string) string {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var id string
	if err := conn.QueryRow(ctx, `SELECT id::text FROM app_user WHERE subject = $1`, subject).Scan(&id); err != nil {
		t.Fatalf("resolve user id for %q: %v", subject, err)
	}
	return id
}

// TestAttemptsDuringALockAreAuditedAndDoNotExtendIt pins D73 on the wire, with
// the half that stops the fix being worse than the defect.
//
// D73: handleLogin's IsLocked branch returned 423 without calling RecordAttempt,
// so the traffic during the lock window — the exact period an investigator cares
// about — left no trace whatever.
//
// The second assertion is the one that keeps the audit honest: these attempts
// never reached a password check, so counting them would let an attacker renew
// their victim's lock indefinitely simply by continuing to knock.
func TestAttemptsDuringALockAreAuditedAndDoNotExtendIt(t *testing.T) {
	srv := newServer(t)
	subject := throwawaySubject(t, "locked-audit")
	tok := mintToken(t, srv, subject, "webauthn")
	_ = createCompanyAs(t, srv, tok) // materialise the app_user row

	lockByFailedLogins(t, srv, subject)
	lockedAt, _, _, _ := lockState(t, subject)
	if lockedAt == nil {
		t.Fatal("fixture: the account is not locked")
	}
	base := attemptsByOutcome(t, subject)

	// Knock six more times while locked.
	for i := 0; i < 6; i++ {
		if code, _, _ := login(t, srv, subject, "still-not-the-password"); code != http.StatusLocked {
			t.Fatalf("knock %d: status = %d, want 423", i, code)
		}
	}

	after := attemptsByOutcome(t, subject)
	if after["locked"]-base["locked"] != 6 {
		t.Fatalf("D73: six sign-in attempts during the lock window left %d audit rows "+
			"(outcomes before %v, after %v). The period an investigator most wants to "+
			"see is the one the product does not record",
			after["locked"]-base["locked"], base, after)
	}
	if after["failure"] != base["failure"] {
		t.Fatalf("attempts made WHILE LOCKED were recorded as password FAILURES "+
			"(%d -> %d). They never reached a password check, and counting them lets a "+
			"brute-forcer keep their victim locked out forever just by knocking",
			base["failure"], after["failure"])
	}

	stillLockedAt, _, _, _ := lockState(t, subject)
	if stillLockedAt == nil || !stillLockedAt.Equal(*lockedAt) {
		t.Fatalf("knocking on a locked account MOVED the lock expiry (%v -> %v). "+
			"The lock must not renew itself off its own refusals", lockedAt, stillLockedAt)
	}
}

// TestFailedAttemptsIsVisibleBeforeTheLockTrips pins D74 on the wire. The column
// was written only inside SetLock, so it read 0 through failures 1-4: an account
// four fifths of the way to being locked looked exactly like one nobody had ever
// touched, and Users & Roles' new counter would have shown a zero throughout an
// attack.
func TestFailedAttemptsIsVisibleBeforeTheLockTrips(t *testing.T) {
	srv := newServer(t)
	subject := throwawaySubject(t, "partway")
	tok := mintToken(t, srv, subject, "webauthn")
	_ = createCompanyAs(t, srv, tok)

	for i := 1; i <= 3; i++ {
		if code, _, _ := login(t, srv, subject, "wrong"); code != http.StatusUnauthorized {
			t.Fatalf("failure %d: status = %d, want 401", i, code)
		}
		until, failed, _, _ := lockState(t, subject)
		if until != nil {
			t.Fatalf("locked prematurely at failure %d", i)
		}
		if failed != i {
			t.Fatalf("D74: after %d failed sign-ins app_user.failed_attempts = %d, want %d. "+
				"Nothing — no screen, no query, no investigator — can see that this "+
				"account is partway to being locked", i, failed, i)
		}
	}
}
