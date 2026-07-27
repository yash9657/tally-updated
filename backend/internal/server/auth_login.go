// auth_login.go is the REAL first-party password login (WS-A1). It replaces the
// dev credential check that used to live in auth_webauthn.go as handleDevLogin
// (a single shared env-var password, AUTH_DEV_PASSWORD, that authenticated ANY
// subject — see the deleted devLoginPassword()).
//
// Two routes:
//
//	POST /auth/login     pre-auth. subject + password -> a signed Bearer token.
//	POST /auth/password  authn-only. self-service set/change. The ONLY writer of
//	                     a password anywhere in the system.
//
// ==========================================================================
// BEHAVIOUR CHANGE — A LOGIN NEVER MINTS AN IDENTITY
// ==========================================================================
// handleDevLogin called authStore.CreateUserOnFirstLogin so the lockout state
// had a row to live on. handleLogin does NOT. A login must never create the
// account it is failing to authenticate.
//
// CONSEQUENCE: for an UNKNOWN subject nothing is persisted on app_user, so
// there is no durable lock to engage. Real, durable lockout only applies to a
// provisioned user. The attempts are still audited in auth_login_attempt either
// way. See TestLoginUnknownSubjectIsNeverCreated /
// TestLoginLockoutIsDurableForRealUser.
//
// FIXED DURING REVIEW — this used to be an enumeration oracle. Locker.
// RecordFailure originally decided nowLocked purely from
// FailuresInWindow >= MaxFailures over auth_login_attempt (no RLS, written for
// ANY subject) and ignored whether SetLock had touched a row, so an unknown
// subject observed 401 x4 then 423 forever. That 423 was a byte-observable
// user-existence oracle needing no timing measurement: a REAL account's `until`
// is the STORED locked_until and is identical on every subsequent request, while
// a ghost's was recomputed as now+15m and ADVANCED each time. Send six wrong
// passwords, watch whether `until` moves, and you have separated real subjects
// from ghosts. LockoutStore.SetLock now reports rows-affected and RecordFailure
// only reports a lock it actually persisted, so a ghost stays on the same
// byte-identical 401 as every other failure. Pinned by
// authn.TestLockoutGhostSubjectIsNeverReportedLocked (mutation-verified).
//
// SCOPE OF THE "NEVER MINTS AN IDENTITY" CLAIM: it is a property of
// POST /auth/login only. Identity creation stays exactly where it already was —
// authnOnlyMiddleware's CreateUserOnFirstLogin, reached by POST /v1/companies,
// GET /v1/me and (new) POST /auth/password. All of those require a
// validly-signed Bearer, and the only in-process minter of one is the dev-gated
// POST /auth/token, so an UNAUTHENTICATED attacker still cannot create an
// identity.
//
// ==========================================================================
// NO USER ENUMERATION
// ==========================================================================
// Four distinct failures — unknown subject, a user who has never set a password,
// a wrong password, and is_active=false — return a BYTE-IDENTICAL
// 401 {"error":"invalid credentials"}. The store distinguishes them internally
// (CredentialBySubject returns the user id when it has one) and never on the
// wire. Timing is levelled too: when there is no stored credential the handler
// still runs one PBKDF2 derivation against a decoy credential, so "unknown user"
// and "wrong password" cost the same. Pinned by
// TestLoginFourFailureModesAreByteIdentical.
//
// ==========================================================================
// TWO DISCLOSED LIMITS (integrator decisions, NOT bugs in this file)
// ==========================================================================
//
//  1. THE MINTED TOKEN CARRIES NO amr. That is the pinned contract, and it means
//     the token cannot WRITE to a company whose Owner level has
//     requires_webauthn=true — which migration 0011's seed_company_auth_defaults()
//     sets for EVERY company. Verified live: a browser-dev-owner token with no
//     amr POSTing a journal returns 403 {"code":"STEPUP_REQUIRED"}. The user must
//     step up through the existing /auth/webauthn/assert ceremony (already wired)
//     to obtain a writing token. A browser that only logs in can READ but not
//     WRITE. Resolving this is a product decision owned by the client
//     workstream: either build the browser WebAuthn step-up, or change the
//     seeded Owner default (which would require rewriting
//     TestWebAuthnStepUpUnlocksWrite and the verify-tenancy step-up checks that
//     pin the OPPOSITE behaviour today). Nothing in this file presumes either.
//
//  2. THERE IS NO PRODUCTION PATH TO SET A **FIRST** PASSWORD. POST /auth/password
//     needs a valid Bearer, and the only ways to obtain one are the dev-gated
//     POST /auth/token or an external IdP. In a deployment where signer == nil
//     and no IdP has issued the invitee a token, a newly invited subject can
//     never set a password and therefore can never log in. This is deliberate —
//     the pinned contract forbids an admin from setting another person's
//     password — but it means an admin-issued one-time set-password token is a
//     REQUIRED follow-up before this is a production login. Today's seeds and
//     verifiers work only because cored runs with AUTH_DEV_TOKEN_ENABLED=true.
//
//     THE OTHER HALF OF THAT SENTENCE, stated plainly because limit 2 above
//     reads like a safety property and is not one. In the DEV posture
//     (AUTH_DEV_TOKEN_ENABLED=true) the combination of an unauthenticated
//     POST /auth/token — which mints a validly-signed token for ANY subject,
//     no password required — with POST /auth/password means ANY caller who can
//     reach the port can PERMANENTLY SET ANY USER'S PASSWORD, and then log in
//     as them forever. Before first-party login the dev token granted only
//     transient impersonation that died with the process; it now writes durable
//     credential state, so the blast radius changed in kind, not just degree.
//     AUTH_DEV_TOKEN_ENABLED must never be set on a host reachable by anything
//     but the developer's own machine. This is not a new hole — /auth/token was
//     always a full-impersonation oracle — but it is newly PERSISTENT.
package server

import (
	"crypto/rand"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/authn"
	"github.com/khata/cored/internal/platform/db"
)

// loginTokenTTLSeconds is the lifetime of a token minted by a password login.
const loginTokenTTLSeconds = 3600

// invalidCredentials is the SINGLE message every authentication failure uses.
// Keeping it in one constant is what makes the four failure modes byte-identical
// by construction rather than by coincidence.
const invalidCredentials = "invalid credentials"

// decoyCredential is a real, correctly-parameterised Credential over a random
// password, derived once on first use. When a login names a subject that does
// not exist (or has no password), the handler verifies against this instead of
// skipping the KDF, so response time does not reveal whether the account exists.
// It can never match: the password is 32 random bytes nobody knows.
var decoyCredential = sync.OnceValue(func() authn.Credential {
	var b [32]byte
	// Any failure here degrades timing-levelling only; correctness is unaffected
	// because a zero Credential still verifies as false (VerifyPassword is total).
	if _, err := rand.Read(b[:]); err != nil {
		return authn.Credential{}
	}
	c, err := authn.HashPassword(string(b[:]))
	if err != nil {
		return authn.Credential{}
	}
	return c
})

// --------------------------------------------------------------------------
// Route wiring (called from routes())
// --------------------------------------------------------------------------

// authLoginRoutes mounts the password routes. It is DECLARATIVE ONLY: it
// registers handlers and middleware method values and dereferences no store, so
// route_vocabulary_test.go's `(&Server{}).routes()` on a zero-value Server still
// builds the router. Both routes live OUTSIDE /v1/companies/{companyID}, so they
// are not part of that test's walked subtree.
func (s *Server) authLoginRoutes() {
	// POST /auth/login is pre-auth and mints a token, so it is mounted only where
	// the in-process signer exists — mirroring /auth/token. In production tokens
	// come from the external IdP. handleLogin repeats the check as defence in
	// depth (404).
	if s.signer != nil {
		s.router.Post("/auth/login", s.handleLogin)
	}

	// POST /auth/password requires a valid Bearer and is mounted unconditionally:
	// changing your own password is not a dev affordance. The acting user is
	// resolved by the middleware into the request context.
	s.router.With(s.authnOnlyMiddleware).Post("/auth/password", s.handleSetPassword)
}

// --------------------------------------------------------------------------
// POST /auth/login
// --------------------------------------------------------------------------

// loginReq is the POST /auth/login body.
type loginReq struct {
	Subject  string `json:"subject"`
	Password string `json:"password"`
}

// handleLogin authenticates a subject with a password. The ORDER is pinned:
//
//	(a) decode + validate the body;
//	(b) lockout.IsLocked -> 423 BEFORE the password is touched (so a locked
//	    account cannot be probed for password correctness);
//	(c) CredentialBySubject (the pre-auth two-step GUC read);
//	(d) authn.VerifyPassword; wrong -> lockout.RecordFailure (nowLocked -> 423,
//	    else 401);
//	(e) correct -> lockout.RecordSuccess + sign a token.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.signer == nil { // defence in depth: only mounted in dev mode.
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	// (a)
	var req loginReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Subject == "" {
		writeError(w, http.StatusBadRequest, "subject is required")
		return
	}
	if req.Password == "" {
		writeError(w, http.StatusBadRequest, "password is required")
		return
	}

	ctx := r.Context()
	ip := clientIP(r)

	// (b) Already locked? 423 before anything else. Note this is the ONLY path
	// that consults durable lock state, and it is false for a subject with no
	// app_user row (see the header disclosure).
	locked, until, err := s.lockout.IsLocked(ctx, req.Subject)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "lockout check")
		return
	}
	if locked {
		writeLocked(w, until)
		return
	}

	// (c) Pre-auth credential read. ctx carries NO company (this route is outside
	// the company subtree and no middleware set one), which is the precondition
	// the two-step GUC dance depends on.
	userID, cred, active, found, err := s.credentials.CredentialBySubject(ctx, req.Subject)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "credential lookup")
		return
	}

	// (d) One verification, always. When there is no credential we verify against
	// the decoy so the KDF cost is paid either way.
	var ok bool
	if found {
		ok = authn.VerifyPassword(cred, req.Password) && active
	} else {
		_ = authn.VerifyPassword(decoyCredential(), req.Password)
		ok = false
	}
	if !ok {
		nowLocked, lockUntil, ferr := s.lockout.RecordFailure(ctx, req.Subject, ip)
		if ferr != nil {
			writeError(w, http.StatusInternalServerError, "record failure")
			return
		}
		if nowLocked {
			writeLocked(w, lockUntil)
			return
		}
		writeError(w, http.StatusUnauthorized, invalidCredentials)
		return
	}

	// (e) Success.
	if err := s.lockout.RecordSuccess(ctx, req.Subject, ip); err != nil {
		writeError(w, http.StatusInternalServerError, "record success")
		return
	}

	// The `user` block makes "who am I" answerable by the client without a second
	// round trip. A failure here is a 500, not a silent empty block: an
	// unreadable directory row means something is wrong with the GUC scoping.
	subject, displayName, email, err := s.credentials.ProfileByUserID(ctx, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load user profile")
		return
	}

	now := time.Now()
	tok, err := s.signer.Sign(authn.Claims{
		Subject: subject,
		Email:   email,
		// NO amr. See the header: step-up is a separate ceremony.
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(loginTokenTTLSeconds * time.Second).Unix(),
		ID:        uuid.NewString(),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "sign token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":      tok,
		"token_type": "Bearer",
		"expires_in": loginTokenTTLSeconds,
		"user": map[string]any{
			"id":           userID.String(),
			"subject":      subject,
			"display_name": displayName,
			"email":        email,
		},
	})
}

// --------------------------------------------------------------------------
// POST /auth/password
// --------------------------------------------------------------------------

// setPasswordReq is the POST /auth/password body. It deliberately carries NO
// subject/user id: an administrator must not be able to set another person's
// password, so the acting user is taken from the authenticated context ONLY.
type setPasswordReq struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// handleSetPassword is the self-service set/change route and the ONLY place a
// password is ever written.
//
//   - 400 when new_password fails the policy (message from authn.ErrPasswordPolicy).
//   - 401 "invalid credentials" when a credential already exists and
//     current_password does not verify.
//   - 423 when those failures trip the lockout policy (see below).
//   - When NO credential exists yet, current_password is IGNORED (first-set path).
//   - 204 No Content on success.
//
// THROTTLING: a wrong current_password calls lockout.RecordFailure under the
// SAME subject key /auth/login uses, so guesses here consume the same 5-in-15m
// budget and appear in the same auth_login_attempt audit trail. Without that
// this route would be an unthrottled brute-force oracle sitting beside a
// throttled one. Deliberately asymmetric with /auth/login in two ways:
// (a) it does NOT call IsLocked up front, because being locked out of
// /auth/login should not also prevent an already-authenticated user from
// completing a password change; and (b) it does NOT call RecordSuccess, so
// changing a password is not a way to clear an active lock.
//
// The acting identity comes from db.UserFromContext / db.SubjectFromContext,
// populated by authnOnlyMiddleware from the verified token. The store's write is
// additionally constrained by user_credential's self-only RLS WITH CHECK, so
// even a handler bug could not write somebody else's row.
func (s *Server) handleSetPassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID, ok := db.UserFromContext(ctx)
	if !ok {
		writeError(w, http.StatusInternalServerError, "resolve user")
		return
	}
	subject, ok := db.SubjectFromContext(ctx)
	if !ok {
		writeError(w, http.StatusInternalServerError, "resolve subject")
		return
	}

	var req setPasswordReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := authn.ValidatePasswordPolicy(req.NewPassword); err != nil {
		if errors.Is(err, authn.ErrPasswordPolicy) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "validate password")
		return
	}

	// Read the ACTING user's existing credential (if any). The subject comes from
	// the token, and the resolved user id is cross-checked below, so this cannot
	// be steered at another account.
	existingID, existing, _, found, err := s.credentials.CredentialBySubject(ctx, subject)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "credential lookup")
		return
	}
	if found && existingID != userID {
		// Structurally impossible (both come from the same middleware) — but if it
		// ever happened it would mean a token's subject and resolved user had
		// diverged, and writing a password would be the worst time to find out.
		writeError(w, http.StatusInternalServerError, "identity mismatch")
		return
	}
	if found && !authn.VerifyPassword(existing, req.CurrentPassword) {
		// SHARE THE LOGIN'S LOCKOUT BUDGET. current_password is a password guess
		// like any other, so an unthrottled change-password route would be a
		// brute-force oracle that bypasses the 5-in-15m policy /auth/login
		// enforces — and the Bearer it needs is not a high bar, since the token
		// lives in sessionStorage and is readable by any script on the origin.
		// Routing failures through the SAME subject key means guesses here count
		// toward the same lock and land in the same auth_login_attempt audit
		// trail, rather than being invisible.
		locked, until, ferr := s.lockout.RecordFailure(ctx, subject, clientIP(r))
		if ferr != nil {
			writeError(w, http.StatusInternalServerError, "record failure")
			return
		}
		if locked {
			writeLocked(w, until)
			return
		}
		writeError(w, http.StatusUnauthorized, invalidCredentials)
		return
	}

	c, err := authn.HashPassword(req.NewPassword)
	if err != nil {
		// ValidatePasswordPolicy already ran, so this is an entropy-source failure.
		writeError(w, http.StatusInternalServerError, "hash password")
		return
	}
	if err := s.credentials.SetPassword(ctx, userID, c); err != nil {
		writeError(w, http.StatusInternalServerError, "set password")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
