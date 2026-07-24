// auth_webauthn.go wires the already-built+tested internal/webauthn ceremony and
// internal/authn lockout into HTTP routes (Task D, WS1 AUTH FOLLOW-UP). It turns
// the library plumbing into a usable feature:
//
//   - WebAuthn step-up: a user registers a credential, then asserts it to obtain
//     a NEW token carrying amr:["webauthn"]. That token satisfies the PDP's
//     requires_webauthn gate on writes (the Owner level seeded at company create
//     sets requires_webauthn=true), so a write that was 403 STEPUP_REQUIRED with
//     the base token returns 201 with the stepped-up token.
//   - A real login flow exercising account lockout: POST /auth/login records
//     failures, locks the subject after the policy threshold (5/15m), and returns
//     423 Locked while locked.
//
// All routes here live OUTSIDE the /v1/companies/{companyID} subtree (they are
// pre-company / cross-company): the webauthn ceremony routes are guarded by the
// authn-only middleware (verify token, resolve user, set subject+user GUC in
// ctx); /auth/login is fully pre-auth (it IS the login).
//
// The begin->finish challenge handoff goes through the ChallengeStore interface
// (auth_challenge_store.go). Since WS6 the shipped implementation is Postgres-
// backed (migration 0012), so the ceremony is no longer single-instance: any
// replica can finish what another began. Production step-up itself is still
// performed by the external IdP, so assert/finish returns 501 when the
// in-process dev signer is absent.
package server

import (
	"encoding/base64"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/authn"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/webauthn"
)

// b64URL is the base64url (no padding) encoding used on the wire for all binary
// WebAuthn fields, matching the webauthn package's RawURLEncoding challenge.
var b64URL = base64.RawURLEncoding

// challengeTTL bounds how long a stashed ceremony challenge is valid between the
// begin and finish calls. It is the default lifetime of both ChallengeStore
// implementations (auth_challenge_store.go).
const challengeTTL = 5 * time.Minute

// stepUpTokenTTLSeconds is the lifetime of a minted stepped-up token (1h).
const stepUpTokenTTLSeconds = 3600

// devLoginPasswordEnv / devLoginPasswordDefault parameterise the dev /auth/login
// password check. Production login is performed by the external IdP; this is a
// dev-only stand-in to exercise the lockout flow.
const (
	devLoginPasswordEnv     = "AUTH_DEV_PASSWORD"
	devLoginPasswordDefault = "dev-password"
)

// --------------------------------------------------------------------------
// Route wiring (called from routes())
// --------------------------------------------------------------------------

// authWebAuthnRoutes mounts the WebAuthn ceremony routes (always — they only
// need the pool-backed services) and the dev /auth/login route (only when the
// in-process dev signer is present, mirroring /auth/token). It is invoked by
// routes() at the top level, outside the company subtree.
func (s *Server) authWebAuthnRoutes() {
	// Ceremony routes: authn-only (verify token + resolve user into ctx). They do
	// NOT require the signer, except assert/finish which 501s without it.
	s.router.With(s.authnOnlyMiddleware).Post("/auth/webauthn/register/begin", s.handleWebAuthnRegisterBegin)
	s.router.With(s.authnOnlyMiddleware).Post("/auth/webauthn/register/finish", s.handleWebAuthnRegisterFinish)
	s.router.With(s.authnOnlyMiddleware).Post("/auth/webauthn/assert/begin", s.handleWebAuthnAssertBegin)
	s.router.With(s.authnOnlyMiddleware).Post("/auth/webauthn/assert/finish", s.handleWebAuthnAssertFinish)

	// Dev login (lockout demonstration): pre-auth, mounted only in dev mode where
	// the signer can mint the resulting token. /auth/token stays unchanged.
	if s.signer != nil {
		s.router.Post("/auth/login", s.handleDevLogin)
	}
}

// --------------------------------------------------------------------------
// WebAuthn registration ceremony
// --------------------------------------------------------------------------

// handleWebAuthnRegisterBegin mints a registration challenge for the resolved
// user, stashes it, and returns the base64url challenge + rp_id + user_id the
// browser needs for navigator.credentials.create().
func (s *Server) handleWebAuthnRegisterBegin(w http.ResponseWriter, r *http.Request) {
	userID, ok := db.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "resolve user")
		return
	}
	ch, err := s.webauthn.RegistrationBegin(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "registration begin: "+err.Error())
		return
	}
	if err := s.challenges.Put(r.Context(), userID, ch); err != nil {
		writeError(w, http.StatusInternalServerError, "stash challenge: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"challenge": ch.Challenge,
		"rp_id":     s.webauthnRPID,
		"user_id":   userID.String(),
	})
}

// webauthnFinishReq is the shared body for register/finish and assert/finish.
// Binary fields are base64url (RawURLEncoding); signature_b64 is only present on
// assert/finish.
type webauthnFinishReq struct {
	CredentialIDB64      string `json:"credential_id_b64"`
	ClientDataJSONB64    string `json:"client_data_json_b64"`
	AuthenticatorDataB64 string `json:"authenticator_data_b64"`
	SignatureB64         string `json:"signature_b64"`
}

// handleWebAuthnRegisterFinish verifies the registration attestation against the
// stashed challenge and persists the credential.
func (s *Server) handleWebAuthnRegisterFinish(w http.ResponseWriter, r *http.Request) {
	userID, ok := db.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "resolve user")
		return
	}
	var req webauthnFinishReq
	if !decodeJSON(w, r, &req) {
		return
	}
	// A store FAILURE is a 500; found=false (never begun / already consumed /
	// expired) is the 400. Conflating them would make a DB blip look like a
	// single-use violation.
	ch, found, err := s.challenges.Take(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "take challenge: "+err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusBadRequest, "no pending registration challenge (begin first; it may have expired)")
		return
	}
	credID, ok := decodeB64URL(w, req.CredentialIDB64, "credential_id_b64")
	if !ok {
		return
	}
	clientData, ok := decodeB64URL(w, req.ClientDataJSONB64, "client_data_json_b64")
	if !ok {
		return
	}
	authData, ok := decodeB64URL(w, req.AuthenticatorDataB64, "authenticator_data_b64")
	if !ok {
		return
	}
	cred, err := s.webauthn.RegistrationFinish(r.Context(), ch, webauthn.RegistrationInput{
		CredentialID:      credID,
		ClientDataJSON:    clientData,
		AuthenticatorData: authData,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "registration finish: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"credential_id": b64URL.EncodeToString(cred.CredentialID),
		"registered":    true,
	})
}

// --------------------------------------------------------------------------
// WebAuthn assertion ceremony (the step-up)
// --------------------------------------------------------------------------

// handleWebAuthnAssertBegin mints an assertion challenge, stashes it, and returns
// the challenge + rp_id + the user's allowed credential ids.
func (s *Server) handleWebAuthnAssertBegin(w http.ResponseWriter, r *http.Request) {
	userID, ok := db.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "resolve user")
		return
	}
	ch, err := s.webauthn.AssertionBegin(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "assertion begin: "+err.Error())
		return
	}
	creds, err := s.webauthnStore.ByUser(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load credentials: "+err.Error())
		return
	}
	allow := make([]string, 0, len(creds))
	for _, c := range creds {
		allow = append(allow, b64URL.EncodeToString(c.CredentialID))
	}
	if err := s.challenges.Put(r.Context(), userID, ch); err != nil {
		writeError(w, http.StatusInternalServerError, "stash challenge: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"challenge":         ch.Challenge,
		"rp_id":             s.webauthnRPID,
		"allow_credentials": allow,
	})
}

// handleWebAuthnAssertFinish verifies the assertion and, on success, mints a NEW
// token for the SAME subject/email carrying amr:["webauthn"] — THIS is the
// step-up. It REQUIRES the in-process dev signer; without it (production), 501 is
// returned because the external IdP performs step-up.
func (s *Server) handleWebAuthnAssertFinish(w http.ResponseWriter, r *http.Request) {
	// Step-up minting requires the dev signer. In production the IdP does it.
	if s.signer == nil {
		writeError(w, http.StatusNotImplemented, "webauthn step-up token minting is not available without the dev signer; production step-up is performed by the external IdP")
		return
	}
	// Re-authenticate to obtain the caller's claims (subject/email) for the new
	// token. The authn-only middleware already verified the token and resolved the
	// user into the context, so this Verify cannot fail here; we use it only to
	// read the subject/email to copy onto the stepped-up token.
	claims, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	userID, ok := db.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "resolve user")
		return
	}
	var req webauthnFinishReq
	if !decodeJSON(w, r, &req) {
		return
	}
	// Store failure => 500; found=false => 400 (see register/finish).
	ch, found, err := s.challenges.Take(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "take challenge: "+err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusBadRequest, "no pending assertion challenge (begin first; it may have expired)")
		return
	}
	credID, ok := decodeB64URL(w, req.CredentialIDB64, "credential_id_b64")
	if !ok {
		return
	}
	clientData, ok := decodeB64URL(w, req.ClientDataJSONB64, "client_data_json_b64")
	if !ok {
		return
	}
	authData, ok := decodeB64URL(w, req.AuthenticatorDataB64, "authenticator_data_b64")
	if !ok {
		return
	}
	sig, ok := decodeB64URL(w, req.SignatureB64, "signature_b64")
	if !ok {
		return
	}
	if _, err := s.webauthn.AssertionFinish(r.Context(), ch, webauthn.AssertionInput{
		CredentialID:      credID,
		ClientDataJSON:    clientData,
		AuthenticatorData: authData,
		Signature:         sig,
	}); err != nil {
		writeError(w, http.StatusBadRequest, "assertion finish: "+err.Error())
		return
	}

	// Mint the stepped-up token: same subject/email, AMR upgraded to webauthn.
	now := time.Now()
	tok, err := s.signer.Sign(authn.Claims{
		Subject:   claims.Subject,
		Email:     claims.Email,
		AMR:       []string{"webauthn"},
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(stepUpTokenTTLSeconds * time.Second).Unix(),
		ID:        uuid.NewString(),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "sign step-up token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":      tok,
		"token_type": "Bearer",
		"expires_in": stepUpTokenTTLSeconds,
	})
}

// --------------------------------------------------------------------------
// Dev login (lockout demonstration)
// --------------------------------------------------------------------------

// devLoginReq is the dev /auth/login body.
type devLoginReq struct {
	Subject  string `json:"subject"`
	Password string `json:"password"`
}

// handleDevLogin is the DEV real-login flow that exercises account lockout:
//
//  1. IsLocked(subject) -> 423 Locked {error, code:"LOCKED", until}.
//  2. password check against the dev policy (AUTH_DEV_PASSWORD, default
//     "dev-password"); wrong -> RecordFailure; if that locks -> 423; else 401.
//  3. correct -> RecordSuccess + mint+return a token (no webauthn amr — the user
//     then steps up via the assert ceremony).
//
// /auth/token (no-password dev shortcut) stays UNCHANGED so existing tests/e2e
// keep working.
func (s *Server) handleDevLogin(w http.ResponseWriter, r *http.Request) {
	if s.signer == nil { // defense in depth: only mounted in dev mode.
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	var req devLoginReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Subject == "" {
		writeError(w, http.StatusBadRequest, "subject is required")
		return
	}
	ctx := r.Context()
	ip := clientIP(r)

	// Ensure an app_user row exists for the subject so the lockout state (which
	// lives on app_user.locked_until/failed_attempts) has a home row to persist
	// to. A real login provisions the user (create-on-first-login); without the
	// row, SetLock would UPDATE zero rows and the lock would never stick. This is
	// idempotent.
	if _, err := s.authStore.CreateUserOnFirstLogin(ctx, req.Subject, "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, "resolve user")
		return
	}

	// (1) Already locked? 423 before touching the password.
	locked, until, err := s.lockout.IsLocked(ctx, req.Subject)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "lockout check")
		return
	}
	if locked {
		writeLocked(w, until)
		return
	}

	// (2) Password check.
	if req.Password != devLoginPassword() {
		nowLocked, lockUntil, ferr := s.lockout.RecordFailure(ctx, req.Subject, ip)
		if ferr != nil {
			writeError(w, http.StatusInternalServerError, "record failure")
			return
		}
		if nowLocked {
			writeLocked(w, lockUntil)
			return
		}
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	// (3) Success: clear lock state + mint a base (no-webauthn) token.
	if err := s.lockout.RecordSuccess(ctx, req.Subject, ip); err != nil {
		writeError(w, http.StatusInternalServerError, "record success")
		return
	}
	now := time.Now()
	tok, err := s.signer.Sign(authn.Claims{
		Subject:   req.Subject,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(stepUpTokenTTLSeconds * time.Second).Unix(),
		ID:        uuid.NewString(),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "sign token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":      tok,
		"token_type": "Bearer",
		"expires_in": stepUpTokenTTLSeconds,
	})
}

// writeLocked writes the 423 Locked response carrying the lock expiry.
func writeLocked(w http.ResponseWriter, until time.Time) {
	writeJSON(w, http.StatusLocked, map[string]any{
		"error": "account locked",
		"code":  "LOCKED",
		"until": until.UTC().Format(time.RFC3339),
	})
}

// devLoginPassword returns the configured dev login password, or the default.
func devLoginPassword() string {
	if p := os.Getenv(devLoginPasswordEnv); p != "" {
		return p
	}
	return devLoginPasswordDefault
}

// clientIP best-effort extracts the remote IP for the audit trail. It returns ""
// when unavailable (the audit column defaults to ”).
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	return r.RemoteAddr
}

// --------------------------------------------------------------------------
// base64url helpers
// --------------------------------------------------------------------------

// decodeB64URL decodes a base64url (RawURLEncoding) field, writing a 400 and
// returning ok=false on a malformed value.
func decodeB64URL(w http.ResponseWriter, s, field string) ([]byte, bool) {
	b, err := b64URL.DecodeString(s)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid base64url in "+field)
		return nil, false
	}
	return b, true
}
