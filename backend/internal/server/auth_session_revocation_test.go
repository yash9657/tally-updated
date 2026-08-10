// auth_session_revocation_test.go — D56: LOCKING AN ACCOUNT MUST REVOKE THE
// SESSIONS IT ALREADY HOLDS.
//
// The defect, driven: `locked_until` was consulted on exactly one code path
// (POST /auth/login), so locking somebody stopped them signing in AGAIN and did
// nothing at all about the bearer already in their hand. A locked account posted
// a write and got 201, and kept working for the remainder of the 3600-second
// token TTL.
//
// ⛔ EVERY TEST IN THIS FILE LOCKS A SUBJECT IT CREATED ITSELF, and no test
// anywhere may ever submit a wrong password for `browser-dev-owner`: the lock
// lives on app_user keyed by SUBJECT, not by company, so one wrong password
// against the shared browser subject locks every other lane out of the product.
// Each test cleans up the identity it locks.
package server_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/server"
)

// throwawaySubject makes a subject nobody else uses, and registers the teardown
// that removes the identity (and its audit trail) whatever the test does to it.
// The DELETE order is the FK order: company_membership -> user_credential ->
// app_user.
func throwawaySubject(t *testing.T, prefix string) string {
	t.Helper()
	subject := prefix + "-" + uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		conn, err := pgx.Connect(ctx, adminDSN)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close(ctx) }()
		_, _ = conn.Exec(ctx, `DELETE FROM company_membership WHERE user_id IN
			(SELECT id FROM app_user WHERE subject = $1)`, subject)
		_, _ = conn.Exec(ctx, `DELETE FROM user_credential WHERE user_id IN
			(SELECT id FROM app_user WHERE subject = $1)`, subject)
		_, _ = conn.Exec(ctx, `DELETE FROM auth_login_attempt WHERE subject = $1`, subject)
		_, _ = conn.Exec(ctx, `DELETE FROM app_user WHERE subject = $1`, subject)
	})
	return subject
}

// lockByFailedLogins locks a subject THE WAY AN ATTACKER WOULD — by driving the
// real POST /auth/login until the policy trips — rather than by an UPDATE. A
// test that locked the row directly would still pass if RecordFailure/SetLock
// had stopped agreeing about what locking means.
func lockByFailedLogins(t *testing.T, srv *server.Server, subject string) {
	t.Helper()
	for i := 0; i < 8; i++ {
		code, _, _ := login(t, srv, subject, "definitely-not-the-password")
		if code == http.StatusLocked {
			return
		}
		if code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: login status = %d, want 401 or 423", i, code)
		}
	}
	t.Fatal("eight wrong passwords did not lock the account: the lockout policy " +
		"itself is not working, so nothing this file asserts about REVOCATION " +
		"would mean anything")
}

// ledgerExists asks the DATABASE, over the admin DSN, whether a named ledger is
// in this company's books.
//
// ⛔ IT EXISTS BECAUSE A STATUS CODE IS NOT AN ORACLE. This repo's signature
// failure is a refusal that still WROTE, so every refusal below is checked
// against the books and not against the number on the wire.
func ledgerExists(t *testing.T, companyID, name string) bool {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var n int
	if err := conn.QueryRow(ctx,
		`SELECT count(*) FROM ledger WHERE company_id = $1 AND name = $2`,
		companyID, name).Scan(&n); err != nil {
		t.Fatalf("count ledger: %v", err)
	}
	return n > 0
}

// expireLock moves a live lock into the past over the admin DSN, WITHOUT
// touching sessions_invalid_before — i.e. it simulates the passage of the
// fifteen minutes. It is how the second clause of revocation (the epoch) is
// separated from the first (currently locked); without it a test would prove
// only that a lock refuses while it lasts.
func expireLock(t *testing.T, subject string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx,
		`UPDATE app_user SET locked_until = now() - interval '1 hour' WHERE subject = $1`,
		subject); err != nil {
		t.Fatalf("expire lock: %v", err)
	}
}

// TestLockingAnAccountRevokesTheSessionsItAlreadyHolds is D56's primary pin.
//
// The shape is CONTROL -> LOCK -> PROBE, and the control is what makes the probe
// mean anything: the very same token, on the very same route, wrote successfully
// moments earlier. Without it a 401 after the lock could as easily be a bad
// fixture as a working revocation.
//
//	CONTROL  POST .../ledgers with the token          -> 201, ledger IN the books
//	LOCK     five wrong passwords for THIS subject    -> 423
//	PROBE 1  POST .../ledgers with the SAME token     -> 401, and NOTHING written
//	PROBE 2  GET  .../ledgers with the SAME token     -> 401 (a locked account
//	                                                    must not go on reading
//	                                                    the books either)
//	PROBE 3  the lock EXPIRES; the same token again   -> STILL 401, nothing
//	                                                    written (the revocation
//	                                                    outlives the lock)
func TestLockingAnAccountRevokesTheSessionsItAlreadyHolds(t *testing.T) {
	srv := newServer(t)
	subject := throwawaySubject(t, "revoke-write")

	// amr=webauthn so the Owner level's step-up is satisfied and the write is a
	// real 201 rather than a 403 STEPUP_REQUIRED — the point of the test is the
	// lock, not the second factor.
	tok := mintToken(t, srv, subject, "webauthn")
	companyID := createCompanyAs(t, srv, tok)
	base := "/v1/companies/" + companyID

	// ---- CONTROL ---------------------------------------------------------
	const before = "Revocation Control Ledger"
	rec, _ := doRaw(t, srv, http.MethodPost, base+"/ledgers", tok, map[string]any{
		"name": before, "group_name": "Sundry Debtors", "created_by": "owner",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("CONTROL: POST /ledgers with an unlocked session = %d, want 201 (%s)",
			rec.Code, rec.Body.String())
	}
	if !ledgerExists(t, companyID, before) {
		t.Fatal("CONTROL: the 201 wrote nothing, so this fixture cannot prove a " +
			"later refusal actually refused")
	}

	// ---- LOCK ------------------------------------------------------------
	lockByFailedLogins(t, srv, subject)

	// ---- PROBE 1: the WRITE that used to return 201 -----------------------
	const after = "Revocation Probe Ledger"
	rec, _ = doRaw(t, srv, http.MethodPost, base+"/ledgers", tok, map[string]any{
		"name": after, "group_name": "Sundry Debtors", "created_by": "owner",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("D56: a LOCKED subject's existing bearer POSTed a ledger and got %d "+
			"(want 401). Locking an account must revoke the sessions it already "+
			"holds, not merely refuse the next sign-in (%s)", rec.Code, rec.Body.String())
	}
	if ledgerExists(t, companyID, after) {
		t.Fatal("⛔ THE REFUSAL STILL WROTE: the response was a 401 and the ledger " +
			"is in the books. A status code is not an oracle")
	}

	// ---- PROBE 2: the READ ------------------------------------------------
	rec, _ = doRaw(t, srv, http.MethodGet, base+"/ledgers", tok, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("D56: a LOCKED subject's existing bearer READ the chart of accounts "+
			"and got %d (want 401). The lock exists to protect exactly this data",
			rec.Code)
	}

	// ---- PROBE 3: the lock expires, the revocation does not ---------------
	expireLock(t, subject)
	const afterExpiry = "Revocation Post Expiry Ledger"
	rec, _ = doRaw(t, srv, http.MethodPost, base+"/ledgers", tok, map[string]any{
		"name": afterExpiry, "group_name": "Sundry Debtors", "created_by": "owner",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("D56: the lock EXPIRED and the token that was live when the account "+
			"was locked came back to life (%d, want 401). A fifteen-minute lock that "+
			"merely PAUSES a stolen session for fifteen minutes has revoked nothing — "+
			"the revocation epoch (app_user.sessions_invalid_before) must outlive the "+
			"lock", rec.Code)
	}
	if ledgerExists(t, companyID, afterExpiry) {
		t.Fatal("⛔ THE POST-EXPIRY REFUSAL STILL WROTE")
	}

	// ---- AND THE WAY BACK IN STILL WORKS ----------------------------------
	// Revocation must not be a wedge: a NEW token, minted after the lock
	// expired, is a new session and must be admitted. Otherwise the fix would
	// have replaced a security hole with a permanent denial of service.
	//
	// ⚠️ THE SLEEP IS LOAD-BEARING AND IT IS NOT FLAKE-PADDING. A JWT `iat` is
	// WHOLE SECONDS while the epoch is a real timestamp, so a token minted in
	// the SAME SECOND as the lock is genuinely ambiguous — it may have been
	// signed a millisecond before the lock or a millisecond after. Locker.
	// SessionRevoked resolves that ambiguity CLOSED (`iat <= epoch`), because
	// the alternative is a one-second window in which a stolen session survives
	// its own revocation. This test ran fast enough to land inside that second
	// and correctly got a 401; a real deployment cannot, because the account is
	// locked for fifteen minutes and /auth/login is the only production minter.
	// Waiting past the boundary asserts the property that actually matters —
	// "a session started AFTER the lock works" — instead of quietly relaxing
	// the comparison to make a millisecond-fast test go green.
	time.Sleep(1100 * time.Millisecond)
	fresh := mintToken(t, srv, subject, "webauthn")
	rec, _ = doRaw(t, srv, http.MethodGet, base+"/ledgers", fresh, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("a FRESH session for the same subject after the lock expired = %d, "+
			"want 200. Revocation must kill the old sessions, not the account (%s)",
			rec.Code, rec.Body.String())
	}
}

// TestLockingRevokesEveryTokenDoorNotOnlyTheCompanySubtree walks the OTHER
// middleware. companyAuthMiddleware and authnOnlyMiddleware are two different
// functions, and only the routes under /v1/companies/{companyID} go through the
// first — so a revocation implemented in the PDP path would leave the entire
// authn-only surface open: GET /v1/me, POST /v1/companies, POST /auth/password,
// and (worst) the four /auth/webauthn/* routes, whose assert ceremony MINTS A
// FRESH, WRITE-CAPABLE TOKEN. A revoked session that can step up is not revoked.
//
// Both middlewares call Server.authenticate, which is why the check lives there;
// this test is what would notice if it moved.
func TestLockingRevokesEveryTokenDoorNotOnlyTheCompanySubtree(t *testing.T) {
	srv := newServer(t)
	subject := throwawaySubject(t, "revoke-doors")
	tok := mintToken(t, srv, subject, "webauthn")

	// CONTROL: the authn-only surface works for this session.
	rec, _ := doRaw(t, srv, http.MethodGet, "/v1/me", tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("CONTROL: GET /v1/me = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	lockByFailedLogins(t, srv, subject)

	// Every authn-only door, including the two that MINT or REPLACE credentials.
	for _, door := range []struct {
		name, method, path string
		body               any
	}{
		{"GET /v1/me", http.MethodGet, "/v1/me", nil},
		{"POST /auth/password", http.MethodPost, "/auth/password",
			map[string]any{"new_password": "a-brand-new-password-2026"}},
		{"POST /auth/webauthn/assert/begin", http.MethodPost, "/auth/webauthn/assert/begin",
			map[string]any{}},
		{"POST /auth/webauthn/register/begin", http.MethodPost, "/auth/webauthn/register/begin",
			map[string]any{}},
		{"POST /v1/companies", http.MethodPost, "/v1/companies", map[string]any{
			"id": uuid.NewString(), "name": "Should Never Exist", "fy_start_date": "2025-04-01",
		}},
	} {
		rec, _ := doRaw(t, srv, door.method, door.path, tok, door.body)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("D56: %s answered %d for a LOCKED subject's existing bearer, want 401. "+
				"Revocation must be a property of every authenticated route, not of the "+
				"company subtree only (%s)", door.name, rec.Code, rec.Body.String())
		}
	}

	// The password door deserves its own oracle: if it had run, the account
	// would now have a password the attacker chose, and the 401 above would be
	// a lie told after the fact.
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var creds int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM user_credential c
		JOIN app_user u ON u.id = c.user_id WHERE u.subject = $1`, subject).Scan(&creds); err != nil {
		t.Fatalf("count credentials: %v", err)
	}
	if creds != 0 {
		t.Fatal("⛔ THE REFUSED POST /auth/password STILL WROTE A CREDENTIAL: a revoked " +
			"session set the account's password and the 401 said otherwise")
	}
}

// TestServicePrincipalsAreNotSubjectToAccountRevocation is the CONTROL for the
// one deliberate exemption. A `svc:` token has no app_user row, no password and
// no way to be locked, and statutoryd's freeze hook must keep working while
// somebody, somewhere, is locked out. If this ever fails, the revocation read
// has started answering "revoked" for a subject it knows nothing about — which
// would take the whole side plane down.
func TestServicePrincipalsAreNotSubjectToAccountRevocation(t *testing.T) {
	srv := newServer(t)
	owner := ownerToken(t, srv)
	companyID := createCompanyAs(t, srv, owner)
	base := "/v1/companies/" + companyID

	svc := mintToken(t, srv, "svc:statutoryd-"+uuid.NewString())
	rec, _ := doRaw(t, srv, http.MethodGet, base+"/ledgers", svc, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("a service principal reading a company = %d, want 200. Revocation "+
			"must not touch tokens that cannot be locked (%s)", rec.Code, rec.Body.String())
	}
}
