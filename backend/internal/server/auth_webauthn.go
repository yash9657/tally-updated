// auth_webauthn.go wires the already-built+tested internal/webauthn ceremony and
// internal/authn lockout into HTTP routes (Task D, WS1 AUTH FOLLOW-UP). It turns
// the library plumbing into a usable feature:
//
//   - WebAuthn step-up: a user registers a credential, then asserts it to obtain
//     a NEW token carrying amr:["webauthn"]. That token satisfies the PDP's
//     requires_webauthn gate on writes (the Owner level seeded at company create
//     sets requires_webauthn=true), so a write that was 403 STEPUP_REQUIRED with
//     the base token returns 201 with the stepped-up token.
//
// The DEV login that used to live here (handleDevLogin + devLoginPassword, a
// single shared AUTH_DEV_PASSWORD that authenticated ANY subject) is GONE. The
// real first-party password login lives in auth_login.go; writeLocked and
// clientIP stay here because the ceremony code and the login share them.
//
// All routes here live OUTSIDE the /v1/companies/{companyID} subtree (they are
// pre-company / cross-company) and are guarded by the authn-only middleware
// (verify token, resolve user, set subject+user GUC in ctx).
//
// The begin->finish challenge handoff goes through the ChallengeStore interface
// (auth_challenge_store.go). Since WS6 the shipped implementation is Postgres-
// backed (migration 0012), so the ceremony is no longer single-instance: any
// replica can finish what another began. Production step-up itself is still
// performed by the external IdP, so assert/finish returns 501 when the
// in-process dev signer is absent.
package server

import (
	"context"
	"encoding/base64"
	"net/http"
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

// maxAllowCredentials caps how many credential ids assert/begin will put in
// allow_credentials.
//
// THIS IS A BROWSER LIMIT, NOT A PREFERENCE. Chromium refuses outright to
// evaluate a larger list: navigator.credentials.get() throws, BEFORE it ever
// consults an authenticator,
//
//	RangeError: The 'allowCredentials' attribute exceeds the maximum allowed size (64).
//
// Measured live today: a dev user had accumulated 65 credentials, this route
// returned all 65, and so EVERY WRITE IN THE PRODUCT became permanently
// impossible on EVERY device — the client turned the RangeError into a hard
// "PASSKEY STEP-UP FAILED — this change was not saved". A sweep against a CDP
// virtual authenticator put the boundary at 65 for RangeError (and found a
// NotAllowedError from 32 upward with that authenticator), so 64 is the ceiling
// and there is no headroom to spend.
//
// The list is NEWEST FIRST (see webauthn.PGStore.RecentByUser). That ordering is
// load-bearing: ByUser orders created_at ASCENDING, so truncating THAT order
// would have dropped the newest credential — the one the device in front of the
// user just enrolled — making the user's own current device the one device
// excluded from its own allow list.
//
// A credential outside this window is still VERIFIABLE: assert/finish resolves it
// through ByCredentialID, which has no limit. Retention is a separate, larger
// ceiling (webauthn.MaxStoredCredentials) precisely so that stays true.
const maxAllowCredentials = 64

// recentCredentialLister is the OPTIONAL store capability the allow-list path
// wants: "the newest N credentials". It is declared here, as a consumer-side
// interface, rather than added to webauthn.CredentialStore, so that a store which
// only implements the four-method interface still works (see the fallback in
// allowCredentialsFor) — the cap does not become a hard dependency of every
// CredentialStore implementation, including the hermetic test doubles.
type recentCredentialLister interface {
	RecentByUser(ctx context.Context, userID uuid.UUID, limit int) ([]webauthn.Credential, error)
}

// allowCredentialsFor returns the base64url credential ids to offer this user,
// NEWEST FIRST and never more than maxAllowCredentials of them.
//
// The fast path pushes the cap into SQL (ORDER BY created_at DESC LIMIT n). The
// fallback exists only for a CredentialStore without RecentByUser: it reads
// ByUser — documented as ALL, OLDEST FIRST — takes the TAIL (the newest) and
// reverses it. Taking the HEAD would be the bug described above.
func allowCredentialsFor(ctx context.Context, store webauthn.CredentialStore, userID uuid.UUID) ([]string, error) {
	if recent, ok := store.(recentCredentialLister); ok {
		creds, err := recent.RecentByUser(ctx, userID, maxAllowCredentials)
		if err != nil {
			return nil, err
		}
		allow := make([]string, 0, len(creds))
		for _, c := range creds {
			allow = append(allow, b64URL.EncodeToString(c.CredentialID))
		}
		return allow, nil
	}
	creds, err := store.ByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(creds) > maxAllowCredentials {
		creds = creds[len(creds)-maxAllowCredentials:]
	}
	allow := make([]string, 0, len(creds))
	for i := len(creds) - 1; i >= 0; i-- { // ascending -> newest first
		allow = append(allow, b64URL.EncodeToString(creds[i].CredentialID))
	}
	return allow, nil
}

// --------------------------------------------------------------------------
// Route wiring (called from routes())
// --------------------------------------------------------------------------

// authWebAuthnRoutes mounts the WebAuthn ceremony routes (always — they only
// need the pool-backed services). It is invoked by routes() at the top level,
// outside the company subtree. The password routes are mounted separately by
// authLoginRoutes (auth_login.go).
func (s *Server) authWebAuthnRoutes() {
	// Ceremony routes: authn-only (verify token + resolve user into ctx). They do
	// NOT require the signer, except assert/finish which 501s without it.
	s.router.With(s.authnOnlyMiddleware).Post("/auth/webauthn/register/begin", s.handleWebAuthnRegisterBegin)
	s.router.With(s.authnOnlyMiddleware).Post("/auth/webauthn/register/finish", s.handleWebAuthnRegisterFinish)
	s.router.With(s.authnOnlyMiddleware).Post("/auth/webauthn/assert/begin", s.handleWebAuthnAssertBegin)
	s.router.With(s.authnOnlyMiddleware).Post("/auth/webauthn/assert/finish", s.handleWebAuthnAssertFinish)
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
	// CAPPED, NEWEST FIRST. This used to be an unbounded loop over ByUser, which
	// is what made a 65-credential user unable to write anything, anywhere — see
	// maxAllowCredentials for the measurement.
	allow, err := allowCredentialsFor(r.Context(), s.webauthnStore, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load credentials: "+err.Error())
		return
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

// writeLocked writes the 423 Locked response carrying the lock expiry.
func writeLocked(w http.ResponseWriter, until time.Time) {
	writeJSON(w, http.StatusLocked, map[string]any{
		"error": "account locked",
		"code":  "LOCKED",
		"until": until.UTC().Format(time.RFC3339),
	})
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
