// auth_webauthn_test.go is the hermetic HTTP test for the WS1 AUTH FOLLOW-UP: it
// drives the real chi router through httptest (no socket) against the live
// Postgres, exercising the FULL killer step-up path and the account-lockout flow.
//
// It does NOT import the webauthn package's test harness (softauth_test.go is
// test-only and unexported). Instead it builds a MINIMAL inline Ed25519 (OKP)
// software authenticator here: it crafts the COSE_Key, clientDataJSON, and
// authenticatorData by hand and signs over authData || sha256(clientData) exactly
// as a real authenticator would, so the ceremony is verified end-to-end with no
// hardware and no cross-package test dependency.
package server_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/platform/testdb"
)

// b64u is the base64url (no padding) encoding the HTTP wire + JWT use.
var b64u = base64.RawURLEncoding

// --------------------------------------------------------------------------
// inline Ed25519 (OKP) software authenticator
// --------------------------------------------------------------------------

// softAuthenticator is a minimal hermetic WebAuthn authenticator over Ed25519.
type softAuthenticator struct {
	rpID      string
	origin    string
	priv      ed25519.PrivateKey
	pub       ed25519.PublicKey
	credID    []byte
	aaguid    []byte
	signCount uint32
}

func newSoftAuthenticator(t *testing.T, rpID, origin string) *softAuthenticator {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen ed25519 key: %v", err)
	}
	credID := make([]byte, 16)
	if _, err := rand.Read(credID); err != nil {
		t.Fatalf("rand credID: %v", err)
	}
	return &softAuthenticator{
		rpID:   rpID,
		origin: origin,
		priv:   priv,
		pub:    pub,
		credID: credID,
		aaguid: make([]byte, 16),
	}
}

// coseKey returns the COSE_Key CBOR for EdDSA: map{1:1(OKP), 3:-8(EdDSA),
// -1:6(Ed25519), -2:<32-byte pub>}.
func (a *softAuthenticator) coseKey() []byte {
	var b []byte
	b = append(b, 0xa4) // map of 4
	b = append(b, cborInt(1)...)
	b = append(b, cborInt(1)...) // kty OKP
	b = append(b, cborInt(3)...)
	b = append(b, cborInt(-8)...) // alg EdDSA
	b = append(b, cborInt(-1)...)
	b = append(b, cborInt(6)...) // crv Ed25519
	b = append(b, cborInt(-2)...)
	b = append(b, cborBytes(a.pub)...)
	return b
}

// authData builds the authenticator data: sha256(rpID) || flags || signCount
// [|| aaguid || credIdLen || credId || COSE_Key when attested]. flags = UP(0x01),
// plus AT(0x40) on registration.
func (a *softAuthenticator) authData(attested bool) []byte {
	rpHash := sha256.Sum256([]byte(a.rpID))
	flags := byte(0x01) // UP
	if attested {
		flags |= 0x40 // AT
	}
	var b []byte
	b = append(b, rpHash[:]...)
	b = append(b, flags)
	var sc [4]byte
	binary.BigEndian.PutUint32(sc[:], a.signCount)
	b = append(b, sc[:]...)
	if attested {
		b = append(b, a.aaguid...)
		var l [2]byte
		binary.BigEndian.PutUint16(l[:], uint16(len(a.credID)))
		b = append(b, l[:]...)
		b = append(b, a.credID...)
		b = append(b, a.coseKey()...)
	}
	return b
}

// clientData builds clientDataJSON {type, challenge (verbatim base64url), origin}.
func (a *softAuthenticator) clientData(typ, challenge string) []byte {
	raw, _ := json.Marshal(struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge"`
		Origin    string `json:"origin"`
	}{Type: typ, Challenge: challenge, Origin: a.origin})
	return raw
}

// sign produces the Ed25519 signature over authData || sha256(clientDataJSON).
func (a *softAuthenticator) sign(authData, clientDataJSON []byte) []byte {
	cdHash := sha256.Sum256(clientDataJSON)
	signed := append(append([]byte{}, authData...), cdHash[:]...)
	return ed25519.Sign(a.priv, signed)
}

// cborInt / cborBytes / cborHead: the same minimal CBOR encoder shape the
// webauthn package's by-hand decoder expects.
func cborInt(v int64) []byte {
	if v >= 0 {
		return cborHead(0, uint64(v))
	}
	return cborHead(1, uint64(-1-v))
}

func cborBytes(b []byte) []byte {
	return append(cborHead(2, uint64(len(b))), b...)
}

func cborHead(major byte, n uint64) []byte {
	mt := major << 5
	switch {
	case n < 24:
		return []byte{mt | byte(n)}
	case n < 256:
		return []byte{mt | 24, byte(n)}
	case n < 65536:
		return []byte{mt | 25, byte(n >> 8), byte(n)}
	default:
		return []byte{mt | 26, byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}
	}
}

// --------------------------------------------------------------------------
// helpers
// --------------------------------------------------------------------------

// tokenAMR decodes the AMR claim of a compact JWS WITHOUT verifying it (the
// server's verifier already accepts it; here we only need to read the claim).
func tokenAMR(t *testing.T, tok string) []string {
	t.Helper()
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("token is not a compact JWS: %q", tok)
	}
	payload, err := b64u.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode token payload: %v", err)
	}
	var claims struct {
		AMR []string `json:"amr"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("unmarshal token payload: %v", err)
	}
	return claims.AMR
}

// --------------------------------------------------------------------------
// the killer step-up test
// --------------------------------------------------------------------------

// TestWebAuthnStepUpUnlocksWrite proves the full WS1 killer path end to end:
//
//  1. mint a BASE Owner token (no webauthn amr) via /auth/token;
//  2. create a company with it (Owner seed: requires_webauthn on writes);
//  3. a WRITE (post a balanced sales voucher) returns 403 STEPUP_REQUIRED;
//  4. register a credential (begin -> finish) using the base token;
//  5. assert (begin -> finish) -> get a stepped-up token carrying amr:["webauthn"];
//  6. the SAME write with the stepped-up token returns 201.
func TestWebAuthnStepUpUnlocksWrite(t *testing.T) {
	srv := newServer(t)

	// (1) Base Owner token: explicitly NO webauthn amr (the default ownerToken
	// helper mints with amr:["webauthn"], which would defeat the test).
	subject := "stepup-owner-" + uuid.NewString()
	baseTok := mintToken(t, srv, subject) // no amr

	// (2) Create a company with the base token. create-on-first-login resolves the
	// user; the Owner level is seeded (requires_webauthn on writes).
	companyID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new company id: %v", err)
	}
	base := "/v1/companies/" + companyID.String()
	rec, _ := doRaw(t, srv, http.MethodPost, "/v1/companies", baseTok, map[string]any{
		"id":            companyID.String(),
		"name":          "StepUp Co " + companyID.String()[:8],
		"fy_start_date": "2025-04-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create company status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	testdb.Cleanup(t, companyID)

	// Ledgers (Create/master also requires webauthn under the Owner level, so use
	// a one-shot stepped-up token to set up the fixtures, then re-test the WRITE
	// step-up explicitly with the base token). First do the step-up ceremony.
	rpID := "localhost"
	origin := "http://localhost:5173"
	auth := newSoftAuthenticator(t, rpID, origin)

	// (4) Register a credential with the BASE token (registration is not a PDP
	// write — it is an /auth/webauthn route guarded by authn-only).
	regBeginRec, regBeginBody := doRaw(t, srv, http.MethodPost, "/auth/webauthn/register/begin", baseTok, nil)
	if regBeginRec.Code != http.StatusOK {
		t.Fatalf("register/begin status = %d, want 200 (body %s)", regBeginRec.Code, regBeginRec.Body.String())
	}
	regChallenge, _ := regBeginBody["challenge"].(string)
	if regChallenge == "" {
		t.Fatalf("register/begin returned no challenge (body %s)", regBeginRec.Body.String())
	}
	if rp, _ := regBeginBody["rp_id"].(string); rp != rpID {
		t.Fatalf("register/begin rp_id = %q, want %q", rp, rpID)
	}

	auth.signCount = 0 // register at 0
	regFinishRec, regFinishBody := doRaw(t, srv, http.MethodPost, "/auth/webauthn/register/finish", baseTok, map[string]any{
		"credential_id_b64":      b64u.EncodeToString(auth.credID),
		"client_data_json_b64":   b64u.EncodeToString(auth.clientData("webauthn.create", regChallenge)),
		"authenticator_data_b64": b64u.EncodeToString(auth.authData(true)),
	})
	if regFinishRec.Code != http.StatusOK {
		t.Fatalf("register/finish status = %d, want 200 (body %s)", regFinishRec.Code, regFinishRec.Body.String())
	}
	if reg, _ := regFinishBody["registered"].(bool); !reg {
		t.Fatalf("register/finish registered = %v, want true (body %s)", regFinishBody["registered"], regFinishRec.Body.String())
	}

	// (3) BEFORE step-up: a WRITE with the base token must be 403 STEPUP_REQUIRED.
	// Use a master create (Create/master), which the Owner level gates on
	// webauthn just like a voucher post.
	preRec, preBody := doRaw(t, srv, http.MethodPost, base+"/ledgers", baseTok, map[string]any{
		"name": "Acme Corp", "group_name": "Sundry Debtors", "created_by": "stepup",
	})
	if preRec.Code != http.StatusForbidden {
		t.Fatalf("pre-stepup write status = %d, want 403 STEPUP_REQUIRED (body %s)", preRec.Code, preRec.Body.String())
	}
	if code, _ := preBody["code"].(string); code != "STEPUP_REQUIRED" {
		t.Fatalf("pre-stepup write code = %q, want STEPUP_REQUIRED (body %s)", code, preRec.Body.String())
	}

	// (5) Assert: begin -> finish -> stepped-up token.
	assertBeginRec, assertBeginBody := doRaw(t, srv, http.MethodPost, "/auth/webauthn/assert/begin", baseTok, nil)
	if assertBeginRec.Code != http.StatusOK {
		t.Fatalf("assert/begin status = %d, want 200 (body %s)", assertBeginRec.Code, assertBeginRec.Body.String())
	}
	assertChallenge, _ := assertBeginBody["challenge"].(string)
	if assertChallenge == "" {
		t.Fatalf("assert/begin returned no challenge (body %s)", assertBeginRec.Body.String())
	}
	// allow_credentials must include our registered credential id.
	allow, _ := assertBeginBody["allow_credentials"].([]any)
	wantCred := b64u.EncodeToString(auth.credID)
	foundCred := false
	for _, c := range allow {
		if s, _ := c.(string); s == wantCred {
			foundCred = true
		}
	}
	if !foundCred {
		t.Fatalf("assert/begin allow_credentials = %v, want to contain %q", allow, wantCred)
	}

	auth.signCount = 1 // strictly greater than the registered 0 (clone detection)
	assertAuthData := auth.authData(false)
	assertClientData := auth.clientData("webauthn.get", assertChallenge)
	assertFinishRec, assertFinishBody := doRaw(t, srv, http.MethodPost, "/auth/webauthn/assert/finish", baseTok, map[string]any{
		"credential_id_b64":      b64u.EncodeToString(auth.credID),
		"client_data_json_b64":   b64u.EncodeToString(assertClientData),
		"authenticator_data_b64": b64u.EncodeToString(assertAuthData),
		"signature_b64":          b64u.EncodeToString(auth.sign(assertAuthData, assertClientData)),
	})
	if assertFinishRec.Code != http.StatusOK {
		t.Fatalf("assert/finish status = %d, want 200 (body %s)", assertFinishRec.Code, assertFinishRec.Body.String())
	}
	steppedTok, _ := assertFinishBody["token"].(string)
	if steppedTok == "" {
		t.Fatalf("assert/finish returned no token (body %s)", assertFinishRec.Body.String())
	}
	// The stepped-up token must carry amr:["webauthn"].
	amr := tokenAMR(t, steppedTok)
	hasWebAuthn := false
	for _, m := range amr {
		if m == "webauthn" {
			hasWebAuthn = true
		}
	}
	if !hasWebAuthn {
		t.Fatalf("stepped-up token amr = %v, want to contain \"webauthn\"", amr)
	}

	// (6) The SAME write with the stepped-up token now SUCCEEDS (201).
	postRec, postBody := doRaw(t, srv, http.MethodPost, base+"/ledgers", steppedTok, map[string]any{
		"name": "Acme Corp", "group_name": "Sundry Debtors", "created_by": "stepup",
	})
	if postRec.Code != http.StatusCreated {
		t.Fatalf("post-stepup write status = %d, want 201 (body %s)", postRec.Code, postRec.Body.String())
	}
	if id, _ := postBody["ledger_id"].(string); id == "" {
		t.Fatalf("post-stepup write returned no ledger_id (body %s)", postRec.Body.String())
	}
}

// TestWebAuthnAssertFinishRejectsForgedSignature proves the assert/finish route
// is non-vacuous: a tampered signature is rejected (400) and yields NO token, so
// the 201 in the killer test is not a free pass.
func TestWebAuthnAssertFinishRejectsForgedSignature(t *testing.T) {
	srv := newServer(t)

	subject := "stepup-forge-" + uuid.NewString()
	baseTok := mintToken(t, srv, subject)

	auth := newSoftAuthenticator(t, "localhost", "http://localhost:5173")

	// Register.
	_, regBeginBody := doRaw(t, srv, http.MethodPost, "/auth/webauthn/register/begin", baseTok, nil)
	regChallenge, _ := regBeginBody["challenge"].(string)
	auth.signCount = 0
	regRec, _ := doRaw(t, srv, http.MethodPost, "/auth/webauthn/register/finish", baseTok, map[string]any{
		"credential_id_b64":      b64u.EncodeToString(auth.credID),
		"client_data_json_b64":   b64u.EncodeToString(auth.clientData("webauthn.create", regChallenge)),
		"authenticator_data_b64": b64u.EncodeToString(auth.authData(true)),
	})
	if regRec.Code != http.StatusOK {
		t.Fatalf("register/finish status = %d, want 200 (body %s)", regRec.Code, regRec.Body.String())
	}

	// Assert begin, then submit a TAMPERED signature.
	_, assertBeginBody := doRaw(t, srv, http.MethodPost, "/auth/webauthn/assert/begin", baseTok, nil)
	assertChallenge, _ := assertBeginBody["challenge"].(string)
	auth.signCount = 1
	ad := auth.authData(false)
	cd := auth.clientData("webauthn.get", assertChallenge)
	sig := auth.sign(ad, cd)
	sig[len(sig)-1] ^= 0xff // tamper

	rec, body := doRaw(t, srv, http.MethodPost, "/auth/webauthn/assert/finish", baseTok, map[string]any{
		"credential_id_b64":      b64u.EncodeToString(auth.credID),
		"client_data_json_b64":   b64u.EncodeToString(cd),
		"authenticator_data_b64": b64u.EncodeToString(ad),
		"signature_b64":          b64u.EncodeToString(sig),
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("forged assert/finish status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if _, ok := body["token"]; ok {
		t.Fatalf("forged assert/finish must not mint a token (body %s)", rec.Body.String())
	}
}

// TestWebAuthnAssertOnlyLatestChallengeIsLive drives the HTTP ceremony through
// the (now Postgres-backed) ChallengeStore and pins the overwrite + single-use
// semantics AT THE ROUTE LEVEL, which the store-level conformance suite cannot
// do: two sequential assert/begin calls leave exactly ONE live challenge — the
// latest — and finishing consumes it.
func TestWebAuthnAssertOnlyLatestChallengeIsLive(t *testing.T) {
	srv := newServer(t)

	subject := "stepup-latest-" + uuid.NewString()
	baseTok := mintToken(t, srv, subject)
	auth := newSoftAuthenticator(t, "localhost", "http://localhost:5173")

	// Register a credential so assertions can be verified.
	_, regBeginBody := doRaw(t, srv, http.MethodPost, "/auth/webauthn/register/begin", baseTok, nil)
	regChallenge, _ := regBeginBody["challenge"].(string)
	auth.signCount = 0
	regRec, _ := doRaw(t, srv, http.MethodPost, "/auth/webauthn/register/finish", baseTok, map[string]any{
		"credential_id_b64":      b64u.EncodeToString(auth.credID),
		"client_data_json_b64":   b64u.EncodeToString(auth.clientData("webauthn.create", regChallenge)),
		"authenticator_data_b64": b64u.EncodeToString(auth.authData(true)),
	})
	if regRec.Code != http.StatusOK {
		t.Fatalf("register/finish status = %d, want 200 (body %s)", regRec.Code, regRec.Body.String())
	}

	beginChallenge := func(t *testing.T) string {
		t.Helper()
		rec, body := doRaw(t, srv, http.MethodPost, "/auth/webauthn/assert/begin", baseTok, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("assert/begin status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		ch, _ := body["challenge"].(string)
		if ch == "" {
			t.Fatalf("assert/begin returned no challenge (body %s)", rec.Body.String())
		}
		return ch
	}
	finishWith := func(t *testing.T, challenge string) (int, map[string]any) {
		t.Helper()
		auth.signCount++
		ad := auth.authData(false)
		cd := auth.clientData("webauthn.get", challenge)
		rec, body := doRaw(t, srv, http.MethodPost, "/auth/webauthn/assert/finish", baseTok, map[string]any{
			"credential_id_b64":      b64u.EncodeToString(auth.credID),
			"client_data_json_b64":   b64u.EncodeToString(cd),
			"authenticator_data_b64": b64u.EncodeToString(ad),
			"signature_b64":          b64u.EncodeToString(auth.sign(ad, cd)),
		})
		return rec.Code, body
	}

	// (a) Two begins, finish with the LATEST -> 200 + a stepped-up token.
	_ = beginChallenge(t) // ch1: overwritten, must become dead
	ch2 := beginChallenge(t)
	code, body := finishWith(t, ch2)
	if code != http.StatusOK {
		t.Fatalf("finish with the LATEST challenge status = %d, want 200 (body %+v)", code, body)
	}
	if tok, _ := body["token"].(string); tok == "" {
		t.Fatalf("finish with the latest challenge returned no token (body %+v)", body)
	}

	// (b) Two begins, finish with the SUPERSEDED one -> rejected, no token.
	ch3 := beginChallenge(t)
	_ = beginChallenge(t) // ch4 replaces ch3
	code, body = finishWith(t, ch3)
	if code != http.StatusBadRequest {
		t.Fatalf("finish with a SUPERSEDED challenge status = %d, want 400 (body %+v)", code, body)
	}
	if _, ok := body["token"]; ok {
		t.Fatalf("finish with a superseded challenge must not mint a token (body %+v)", body)
	}

	// (c) That failed finish still CONSUMED the live challenge (single use), so a
	// follow-up finish has nothing to work with.
	code, body = finishWith(t, ch3)
	if code != http.StatusBadRequest {
		t.Fatalf("finish after the challenge was consumed status = %d, want 400 (body %+v)", code, body)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "no pending assertion challenge") {
		t.Fatalf("expected a no-pending-challenge error, got %q", msg)
	}
}

// NOTE: TestDevLoginLockout USED to live here. It was deleted with
// handleDevLogin. It could not be ported mechanically: it drove two subjects
// that DO NOT EXIST ("lockout-<uuid>", "lockout-fail-<uuid>") and depended on
// handleDevLogin calling CreateUserOnFirstLogin both to make its "correct
// password -> 200 + token" assertion possible and to give the lock a row to
// persist to. The real login creates nobody, so its successor lives in
// auth_login_test.go (TestLoginLockoutIsDurableForRealUser), provisions a REAL
// user first, and asserts app_user.locked_until over the admin DSN rather than
// trusting the 423 — because for an UNPROVISIONED subject a 423 is returned by
// the failure counter alone, with no durable lock (see
// TestLoginUnknownSubjectLockIsNotDurable).
