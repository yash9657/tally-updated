// auth_login_test.go is the HTTP gate for the first-party password login
// (WS-A1). It drives the real chi router through httptest against live Postgres.
//
// Two oracles are deliberately OUTSIDE the surface under test:
//   - app_user counts / locked_until are read over the ADMIN DSN, because
//     app_user is FORCE-RLS and the application role sees only its own row (a
//     count through the app pool would be trivially and vacuously 0 or 1).
//   - "the login token actually works" is asserted by using it on a real route,
//     not by decoding it.
package server_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/server"
)

const testPassword = "tally-demo-2026"

// provisionLogin creates a real identity and gives it a password, the way a
// deployment actually does it today: mint a dev token for the subject, then POST
// /auth/password (which is authn-only, so the middleware's
// CreateUserOnFirstLogin materialises the app_user row) to set the FIRST
// password. It returns the bootstrap token.
func provisionLogin(t *testing.T, srv *server.Server, subject, password string) string {
	t.Helper()
	tok := mintToken(t, srv, subject)
	rec, _ := doRaw(t, srv, http.MethodPost, "/auth/password", tok, map[string]any{
		"new_password": password,
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("provision password for %q status = %d, want 204 (body %s)", subject, rec.Code, rec.Body.String())
	}
	return tok
}

// adminConn opens a direct admin connection for the out-of-band oracles.
func adminConn(t *testing.T) *pgx.Conn {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

// rawUserCount counts app_user rows for a subject over the ADMIN DSN.
func rawUserCount(t *testing.T, conn *pgx.Conn, subject string) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(context.Background(),
		`SELECT count(*) FROM app_user WHERE subject = $1`, subject).Scan(&n); err != nil {
		t.Fatalf("count app_user: %v", err)
	}
	return n
}

// rawLockedUntil reads app_user.locked_until over the ADMIN DSN. ok=false when
// no row exists at all.
func rawLockedUntil(t *testing.T, conn *pgx.Conn, subject string) (until *time.Time, ok bool) {
	t.Helper()
	err := conn.QueryRow(context.Background(),
		`SELECT locked_until FROM app_user WHERE subject = $1`, subject).Scan(&until)
	if err == pgx.ErrNoRows {
		return nil, false
	}
	if err != nil {
		t.Fatalf("read locked_until: %v", err)
	}
	return until, true
}

// rawAttemptCount counts audited login attempts over the ADMIN DSN.
func rawAttemptCount(t *testing.T, conn *pgx.Conn, subject string) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(context.Background(),
		`SELECT count(*) FROM auth_login_attempt WHERE subject = $1`, subject).Scan(&n); err != nil {
		t.Fatalf("count auth_login_attempt: %v", err)
	}
	return n
}

// login POSTs /auth/login and returns the recorder + decoded body.
func login(t *testing.T, srv *server.Server, subject, password string) (int, map[string]any, string) {
	t.Helper()
	rec, body := doRaw(t, srv, http.MethodPost, "/auth/login", "", map[string]any{
		"subject": subject, "password": password,
	})
	return rec.Code, body, rec.Body.String()
}

// --------------------------------------------------------------------------

// TestLoginSuccess is the primary success oracle: a real password authenticates,
// the response carries the pinned shape, and the token WORKS on a real route.
//
// REGRESSION SENSITIVITY: if VerifyPassword/CredentialBySubject stopped agreeing
// (the mid-tx GUC promotion, the KDF parameters, the salt storage) the correct
// password would 401 and this fails at the first assertion. If the token were
// minted with the wrong subject, or not signed by the server's signer, the
// GET /v1/me leg fails with 401. If the `user` block were dropped or renamed the
// client contract assertions fail.
func TestLoginSuccess(t *testing.T) {
	srv := newServer(t)
	subject := "login-ok-" + uuid.NewString()
	provisionLogin(t, srv, subject, testPassword)

	code, body, raw := login(t, srv, subject, testPassword)
	if code != http.StatusOK {
		t.Fatalf("login status = %d, want 200 (body %s)", code, raw)
	}
	tok, _ := body["token"].(string)
	if tok == "" {
		t.Fatalf("login returned no token (body %s)", raw)
	}
	if tt, _ := body["token_type"].(string); tt != "Bearer" {
		t.Fatalf("token_type = %q, want Bearer", tt)
	}
	if exp, _ := body["expires_in"].(float64); exp != 3600 {
		t.Fatalf("expires_in = %v, want 3600", body["expires_in"])
	}
	user, _ := body["user"].(map[string]any)
	if user == nil {
		t.Fatalf("login response has no user block (body %s)", raw)
	}
	if got, _ := user["subject"].(string); got != subject {
		t.Fatalf("user.subject = %q, want %q", got, subject)
	}
	if id, _ := user["id"].(string); id == "" {
		t.Fatal("user.id is empty: the client cannot key a session on it")
	} else if _, err := uuid.Parse(id); err != nil {
		t.Fatalf("user.id %q is not a uuid: %v", id, err)
	}
	if _, present := user["display_name"]; !present {
		t.Fatal("user.display_name key is missing (pinned client contract)")
	}
	if _, present := user["email"]; !present {
		t.Fatal("user.email key is missing (pinned client contract)")
	}

	// The token is REAL: it authorizes an authn-only route.
	rec, _ := doRaw(t, srv, http.MethodGet, "/v1/me", tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/me with the login token status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestLoginTokenCarriesNoAMR pins the disclosed contract that a password login
// does NOT satisfy WebAuthn step-up: the minted token has no amr, so a write to a
// company whose Owner level requires step-up still 403s STEPUP_REQUIRED and the
// user must run the assert ceremony.
//
// REGRESSION SENSITIVITY: if somebody "fixed" the browser-cannot-write problem by
// quietly stamping amr:["webauthn"] on the login token, this fails — which is the
// point: that would silently defeat the step-up control for every company.
func TestLoginTokenCarriesNoAMR(t *testing.T) {
	srv := newServer(t)
	subject := "login-amr-" + uuid.NewString()
	provisionLogin(t, srv, subject, testPassword)

	code, body, raw := login(t, srv, subject, testPassword)
	if code != http.StatusOK {
		t.Fatalf("login status = %d, want 200 (body %s)", code, raw)
	}
	tok, _ := body["token"].(string)

	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("token is not a three-part JWT: %q", tok)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode token payload: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("unmarshal token payload: %v", err)
	}
	if v, present := claims["amr"]; present && v != nil {
		if arr, ok := v.([]any); !ok || len(arr) > 0 {
			t.Fatalf("login token carries amr = %v; a password login must NOT satisfy WebAuthn step-up", v)
		}
	}
	if sub, _ := claims["sub"].(string); sub != subject {
		t.Fatalf("token sub = %q, want %q", sub, subject)
	}
}

// TestLoginFourFailureModesAreByteIdentical is the no-user-enumeration gate. All
// four distinguishable server-side conditions must produce the SAME status and
// the SAME response bytes.
//
// REGRESSION SENSITIVITY: any future "helpful" message ("no such user", "password
// not set", "account disabled") makes this fail, naming the two bodies that
// diverged. Without it, /auth/login becomes a free user-directory oracle for an
// unauthenticated attacker.
func TestLoginFourFailureModesAreByteIdentical(t *testing.T) {
	srv := newServer(t)
	conn := adminConn(t)
	ctx := context.Background()

	// (1) unknown subject.
	unknown := "login-unknown-" + uuid.NewString()

	// (2) known subject with NO credential: provision the app_user row via an
	// authn-only route, but never set a password.
	noCred := "login-nocred-" + uuid.NewString()
	tok := mintToken(t, srv, noCred)
	if rec, _ := doRaw(t, srv, http.MethodGet, "/v1/me", tok, nil); rec.Code != http.StatusOK {
		t.Fatalf("provision no-credential user: GET /v1/me = %d (body %s)", rec.Code, rec.Body.String())
	}
	if n := rawUserCount(t, conn, noCred); n != 1 {
		t.Fatalf("no-credential fixture: app_user rows = %d, want 1 (the case would not be the one intended)", n)
	}

	// (3) wrong password against a real credential.
	wrongPw := "login-wrongpw-" + uuid.NewString()
	provisionLogin(t, srv, wrongPw, testPassword)

	// (4) correct password but is_active = false.
	inactive := "login-inactive-" + uuid.NewString()
	provisionLogin(t, srv, inactive, testPassword)
	if _, err := conn.Exec(ctx, `UPDATE app_user SET is_active = false WHERE subject = $1`, inactive); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	type probe struct {
		name    string
		subject string
		pw      string
	}
	probes := []probe{
		{"unknown subject", unknown, testPassword},
		{"known subject, no credential", noCred, testPassword},
		{"wrong password", wrongPw, "definitely-not-the-password"},
		{"inactive user, correct password", inactive, testPassword},
	}

	var firstBody string
	for i, p := range probes {
		code, _, raw := login(t, srv, p.subject, p.pw)
		if code != http.StatusUnauthorized {
			t.Fatalf("%s: status = %d, want 401 (body %s)", p.name, code, raw)
		}
		if !strings.Contains(raw, "invalid credentials") {
			t.Fatalf("%s: body %q does not carry the single shared message", p.name, raw)
		}
		if i == 0 {
			firstBody = raw
			continue
		}
		if raw != firstBody {
			t.Fatalf("%s produced a DIFFERENT 401 body than %q:\n  first: %q\n  this:  %q\nthat difference is a user-enumeration oracle",
				p.name, probes[0].name, firstBody, raw)
		}
	}
}

// TestLoginValidation pins the two 400s.
//
// REGRESSION SENSITIVITY: dropping the empty-subject guard would send "" into the
// lockout counter and the store (harmless but noisy); dropping the empty-password
// guard would let a blank password reach VerifyPassword, and for a user with NO
// credential the four-mode 401 would then hide a genuinely blank-password login
// if the totality guards were ever relaxed.
func TestLoginValidation(t *testing.T) {
	srv := newServer(t)

	code, body, raw := login(t, srv, "", testPassword)
	if code != http.StatusBadRequest {
		t.Fatalf("empty subject status = %d, want 400 (body %s)", code, raw)
	}
	if msg, _ := body["error"].(string); msg != "subject is required" {
		t.Fatalf("empty subject error = %q, want %q", msg, "subject is required")
	}

	code, body, raw = login(t, srv, "login-novalidate-"+uuid.NewString(), "")
	if code != http.StatusBadRequest {
		t.Fatalf("empty password status = %d, want 400 (body %s)", code, raw)
	}
	if msg, _ := body["error"].(string); msg != "password is required" {
		t.Fatalf("empty password error = %q, want %q", msg, "password is required")
	}
}

// TestLoginUnknownSubjectIsNeverCreated is the CRITICAL behaviour-change gate: a
// login must never mint the identity it is failing to authenticate.
//
// The oracle is the ADMIN DSN, deliberately: app_user is FORCE-RLS, so counting
// through the application pool would see at most the caller's own row and the
// assertion would pass vacuously no matter what the handler did.
//
// REGRESSION SENSITIVITY: re-adding CreateUserOnFirstLogin to handleLogin (which
// is exactly what the deleted handleDevLogin did, and it is a tempting "fix" for
// the non-durable-lock disclosure) makes this fail immediately.
func TestLoginUnknownSubjectIsNeverCreated(t *testing.T) {
	srv := newServer(t)
	conn := adminConn(t)

	subject := "login-ghost-" + uuid.NewString()
	if n := rawUserCount(t, conn, subject); n != 0 {
		t.Fatalf("fixture subject already exists (%d rows)", n)
	}

	for i := 0; i < 3; i++ {
		if code, _, raw := login(t, srv, subject, testPassword); code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401 (body %s)", i+1, code, raw)
		}
	}

	if n := rawUserCount(t, conn, subject); n != 0 {
		t.Fatalf("POST /auth/login CREATED an app_user for an unknown subject (%d rows): a login must never mint an identity", n)
	}

	// The attempts ARE audited, though — that is the half that must keep working.
	if n := rawAttemptCount(t, conn, subject); n != 3 {
		t.Fatalf("auth_login_attempt rows = %d, want 3: the failures must still be audited", n)
	}

	// SENSITIVITY CONTROL — without this, "the count is 0" could pass vacuously
	// (wrong table, wrong column, an oracle that can never see a row). The SAME
	// subject, taken through the route that IS allowed to provision, must make the
	// count go to 1. A deliberately global `count(*) FROM app_user` before/after is
	// NOT used here: other test packages run concurrently against this database and
	// move it.
	tok := mintToken(t, srv, subject)
	if rec, _ := doRaw(t, srv, http.MethodGet, "/v1/me", tok, nil); rec.Code != http.StatusOK {
		t.Fatalf("control: GET /v1/me = %d (body %s)", rec.Code, rec.Body.String())
	}
	if n := rawUserCount(t, conn, subject); n != 1 {
		t.Fatalf("control failed: after an authn-only request the subject should have exactly 1 app_user row, got %d — the zero above was vacuous", n)
	}
}

// TestLoginUnknownSubjectNeverLeaks423 pins the NO-ENUMERATION invariant for an
// unprovisioned subject: no matter how many times you fail against a name that
// does not exist, the answer stays the SAME byte-identical 401 that a wrong
// password gets. A ghost subject must never see a 423.
//
// THIS TEST INVERTS WHAT IT ORIGINALLY ASSERTED, deliberately. As first written
// it pinned the 5th attempt returning 423 and called that a "disclosed
// oscillation". Adversarial review showed the oscillation was not a quirk to
// disclose but a USER-ENUMERATION ORACLE, observable in bytes with no timing
// measurement at all: for a REAL account writeLocked renders the STORED
// app_user.locked_until, so `until` is identical on every subsequent request;
// for a ghost it was recomputed as now+15m and therefore ADVANCED each time.
// Send six wrong passwords, watch whether `until` moves, and you have separated
// real subjects from ghosts. Locker.RecordFailure now only reports a lock that
// SetLock actually persisted, so this case can no longer be distinguished.
//
// REGRESSION SENSITIVITY: revert RecordFailure to ignore SetLock's rows-affected
// and attempt 5 becomes a 423, failing here immediately. The body comparison is
// the second lock: any future refinement that gives ghosts a distinguishable
// message (a "user not found", a different code) fails the byte-equality check
// rather than shipping a quieter oracle. The final SQL assertion keeps the whole
// thing honest — it proves nothing was provisioned along the way, so this cannot
// be "fixed" by creating the user (which TestLoginUnknownSubjectIsNeverCreated
// independently forbids).
func TestLoginUnknownSubjectNeverLeaks423(t *testing.T) {
	srv := newServer(t)
	conn := adminConn(t)

	subject := "login-ghostlock-" + uuid.NewString()

	// Well past the 5-failure threshold.
	var bodies []string
	for i := 0; i < 8; i++ {
		code, _, raw := login(t, srv, subject, "wrong-password-here")
		if code == http.StatusLocked {
			t.Fatalf("ENUMERATION ORACLE: attempt %d for a NON-EXISTENT subject returned 423 "+
				"(body %s). A ghost account must be indistinguishable from a wrong password; "+
				"a 423 — and especially its `until`, which advances for a ghost but is fixed "+
				"for a real account — reveals that the account does not exist", i+1, raw)
		}
		if code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401 (body %s)", i+1, code, raw)
		}
		bodies = append(bodies, raw)
	}
	for i, b := range bodies {
		if b != bodies[0] {
			t.Fatalf("attempt %d body %q differs from attempt 1 body %q — every failure for a "+
				"ghost subject must be byte-identical", i+1, b, bodies[0])
		}
	}

	// Nothing was persisted: no app_user row, so no durable lock either.
	if _, ok := rawLockedUntil(t, conn, subject); ok {
		t.Fatal("an app_user row exists for a subject that only ever failed to log in")
	}
}

// TestLoginLockoutIsDurableForRealUser is the successor to the deleted
// TestDevLoginLockout, rewritten so it cannot pass for the wrong reason.
//
// It provisions a REAL user with a REAL password, then asserts:
//   - the correct password is 200 before any failures;
//   - five wrong passwords produce a 423;
//   - while locked, even the CORRECT password is 423 (this leg goes through
//     Locker.IsLocked, which reads app_user — it can only pass if the lock
//     actually PERSISTED);
//   - app_user.locked_until is non-null over the ADMIN DSN.
//
// REGRESSION SENSITIVITY: if SetLock stopped persisting (or the handler stopped
// calling IsLocked before the password check), the correct-password-while-locked
// leg returns 200 and the locked_until assertion fails. The final SQL check is
// the independent oracle: the HTTP surface cannot fake it.
func TestLoginLockoutIsDurableForRealUser(t *testing.T) {
	srv := newServer(t)
	conn := adminConn(t)

	subject := "login-lock-" + uuid.NewString()
	provisionLogin(t, srv, subject, testPassword)

	if code, _, raw := login(t, srv, subject, testPassword); code != http.StatusOK {
		t.Fatalf("correct password before any failure: status = %d, want 200 (body %s)", code, raw)
	}

	var got423 bool
	for i := 0; i < 5; i++ {
		code, _, raw := login(t, srv, subject, "wrong-password-here")
		switch code {
		case http.StatusUnauthorized:
		case http.StatusLocked:
			got423 = true
		default:
			t.Fatalf("failure %d status = %d, want 401 or 423 (body %s)", i+1, code, raw)
		}
	}
	if !got423 {
		t.Fatal("five wrong passwords did not lock a provisioned account")
	}

	// While locked, the CORRECT password is refused — via IsLocked, i.e. durable state.
	code, body, raw := login(t, srv, subject, testPassword)
	if code != http.StatusLocked {
		t.Fatalf("correct password while locked: status = %d, want 423 (body %s)", code, raw)
	}
	if c, _ := body["code"].(string); c != "LOCKED" {
		t.Fatalf("locked response code = %q, want LOCKED (body %s)", c, raw)
	}
	untilStr, _ := body["until"].(string)
	if _, err := time.Parse(time.RFC3339, untilStr); err != nil {
		t.Fatalf("locked response until = %q is not RFC3339: %v", untilStr, err)
	}

	// Independent oracle: the lock is really on the row.
	until, ok := rawLockedUntil(t, conn, subject)
	if !ok {
		t.Fatal("no app_user row for a provisioned subject")
	}
	if until == nil {
		t.Fatal("app_user.locked_until is NULL: the 423 came from the failure counter, not a durable lock")
	}
	if !until.After(time.Now()) {
		t.Fatalf("app_user.locked_until = %s is not in the future", until)
	}
}

// --------------------------------------------------------------------------
// POST /auth/password
// --------------------------------------------------------------------------

// TestSetPasswordRequiresAuthentication proves the route is not pre-auth.
//
// REGRESSION SENSITIVITY: mounting it without s.authnOnlyMiddleware makes this
// return 500/204 instead of 401 — and would let anyone set anyone's password.
func TestSetPasswordRequiresAuthentication(t *testing.T) {
	srv := newServer(t)
	rec, _ := doRaw(t, srv, http.MethodPost, "/auth/password", "", map[string]any{
		"new_password": testPassword,
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated POST /auth/password status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestSetPasswordFirstSetIgnoresCurrent pins the first-set path: with no
// credential yet, current_password is ignored.
//
// REGRESSION SENSITIVITY: requiring current_password unconditionally would make
// it impossible for an invited user to ever set a first password (there is no
// admin-set route by design), i.e. a hard lockout of every new account.
func TestSetPasswordFirstSetIgnoresCurrent(t *testing.T) {
	srv := newServer(t)
	subject := "pw-first-" + uuid.NewString()
	tok := mintToken(t, srv, subject)

	rec, _ := doRaw(t, srv, http.MethodPost, "/auth/password", tok, map[string]any{
		"current_password": "total-nonsense",
		"new_password":     testPassword,
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("first set-password status = %d, want 204 (body %s)", rec.Code, rec.Body.String())
	}
	if code, _, raw := login(t, srv, subject, testPassword); code != http.StatusOK {
		t.Fatalf("login after first set: status = %d, want 200 (body %s)", code, raw)
	}
}

// TestSetPasswordChangeRequiresCurrent pins the change path.
//
// REGRESSION SENSITIVITY: dropping the current_password check turns a stolen (or
// merely borrowed, unlocked-laptop) session into a permanent account takeover.
// The final leg proves the change actually took effect, so the 401 cannot be
// satisfied by a handler that simply never writes.
func TestSetPasswordChangeRequiresCurrent(t *testing.T) {
	srv := newServer(t)
	subject := "pw-change-" + uuid.NewString()
	tok := provisionLogin(t, srv, subject, testPassword)

	// Wrong current -> 401, and NOTHING changes.
	rec, body := doRaw(t, srv, http.MethodPost, "/auth/password", tok, map[string]any{
		"current_password": "not-the-current-one",
		"new_password":     "brand-new-password-1",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong current_password status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
	}
	if msg, _ := body["error"].(string); msg != "invalid credentials" {
		t.Fatalf("wrong current_password error = %q, want %q", msg, "invalid credentials")
	}
	if code, _, raw := login(t, srv, subject, "brand-new-password-1"); code != http.StatusUnauthorized {
		t.Fatalf("the rejected new password must NOT have been written; login status = %d, want 401 (body %s)", code, raw)
	}

	// Correct current -> 204, old password dies, new one works.
	rec, _ = doRaw(t, srv, http.MethodPost, "/auth/password", tok, map[string]any{
		"current_password": testPassword,
		"new_password":     "brand-new-password-1",
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("password change status = %d, want 204 (body %s)", rec.Code, rec.Body.String())
	}
	if code, _, raw := login(t, srv, subject, testPassword); code != http.StatusUnauthorized {
		t.Fatalf("the OLD password still works after a change; status = %d, want 401 (body %s)", code, raw)
	}
	if code, _, raw := login(t, srv, subject, "brand-new-password-1"); code != http.StatusOK {
		t.Fatalf("the NEW password does not work after a change; status = %d, want 200 (body %s)", code, raw)
	}
}

// TestSetPasswordEnforcesPolicy pins the 400.
//
// REGRESSION SENSITIVITY: without it a one-character password is storable, and
// the 400-vs-500 distinction (errors.Is on authn.ErrPasswordPolicy) would rot
// silently into a 500 that the client renders as "server error" instead of a
// fixable message.
func TestSetPasswordEnforcesPolicy(t *testing.T) {
	srv := newServer(t)
	subject := "pw-policy-" + uuid.NewString()
	tok := mintToken(t, srv, subject)

	rec, body := doRaw(t, srv, http.MethodPost, "/auth/password", tok, map[string]any{
		"new_password": "short",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("short password status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	msg, _ := body["error"].(string)
	if !strings.Contains(msg, "at least") {
		t.Fatalf("policy error = %q, want a message naming the minimum length", msg)
	}

	rec, body = doRaw(t, srv, http.MethodPost, "/auth/password", tok, map[string]any{
		"new_password": strings.Repeat("a", 257),
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized password status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if msg, _ = body["error"].(string); !strings.Contains(msg, "at most") {
		t.Fatalf("policy error = %q, want a message naming the byte cap", msg)
	}

	// Nothing was written: the account still has no password.
	if code, _, raw := login(t, srv, subject, "short"); code != http.StatusUnauthorized {
		t.Fatalf("a policy-rejected password must not be stored; login status = %d, want 401 (body %s)", code, raw)
	}
}

// TestSetPasswordCannotTargetAnotherUser is the privilege gate: the acting user
// comes from the TOKEN, never from the body. An attacker (here: a second, fully
// legitimate account) sends every plausible impersonation field in the body and
// must still only be able to change their own password.
//
// REGRESSION SENSITIVITY: if handleSetPassword ever read a subject/user_id from
// the body, the victim's password would change and their original one would stop
// working — both halves are asserted. user_credential's self-only RLS WITH CHECK
// is the second line of defence behind this.
func TestSetPasswordCannotTargetAnotherUser(t *testing.T) {
	srv := newServer(t)

	victim := "pw-victim-" + uuid.NewString()
	provisionLogin(t, srv, victim, testPassword)
	code, victimBody, raw := login(t, srv, victim, testPassword)
	if code != http.StatusOK {
		t.Fatalf("victim login status = %d, want 200 (body %s)", code, raw)
	}
	victimUser, _ := victimBody["user"].(map[string]any)
	victimID, _ := victimUser["id"].(string)
	if victimID == "" {
		t.Fatal("could not resolve the victim's user id")
	}

	attacker := "pw-attacker-" + uuid.NewString()
	attackerTok := provisionLogin(t, srv, attacker, testPassword)

	rec, _ := doRaw(t, srv, http.MethodPost, "/auth/password", attackerTok, map[string]any{
		// Every field an impersonating body might try.
		"subject":          victim,
		"user_id":          victimID,
		"user":             map[string]any{"id": victimID, "subject": victim},
		"current_password": testPassword,
		"new_password":     "attacker-chosen-pw-1",
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("attacker's own password change status = %d, want 204 (body %s)", rec.Code, rec.Body.String())
	}

	// The VICTIM is untouched.
	if code, _, raw := login(t, srv, victim, testPassword); code != http.StatusOK {
		t.Fatalf("the victim's original password stopped working; status = %d, want 200 (body %s)", code, raw)
	}
	if code, _, raw := login(t, srv, victim, "attacker-chosen-pw-1"); code != http.StatusUnauthorized {
		t.Fatalf("the ATTACKER set the victim's password; login status = %d, want 401 (body %s)", code, raw)
	}
	// The ATTACKER's own credential did change (so the 204 was not a no-op).
	if code, _, raw := login(t, srv, attacker, "attacker-chosen-pw-1"); code != http.StatusOK {
		t.Fatalf("the attacker's own password was not changed; status = %d, want 200 (body %s)", code, raw)
	}
}

// TestDevTokenRouteStillWorks is the do-not-regress guard for the explicitly
// out-of-scope route: /auth/token is untouched, because the e2e scripts,
// statutoryd and every seeder depend on it.
//
// REGRESSION SENSITIVITY: any refactor that accidentally re-mounts or re-gates
// /auth/token (they are adjacent in routes()) fails here rather than in a live
// script.
func TestDevTokenRouteStillWorks(t *testing.T) {
	srv := newServer(t)
	rec, body := doRaw(t, srv, http.MethodPost, "/auth/token", "", map[string]any{
		"subject": "token-route-" + uuid.NewString(),
		"amr":     []string{"webauthn"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /auth/token status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if tok, _ := body["token"].(string); tok == "" {
		t.Fatalf("POST /auth/token returned no token (body %s)", rec.Body.String())
	}
}
