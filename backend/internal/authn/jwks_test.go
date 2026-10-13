package authn

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// hermetic JWKS server (no network egress: httptest binds loopback)
// ---------------------------------------------------------------------------

// jwksServer serves a mutable JWKS document and counts the GETs it receives, so
// tests can assert the cache/throttle behaviour non-vacuously.
type jwksServer struct {
	*httptest.Server
	mu     sync.Mutex
	doc    []byte
	hits   atomic.Int64
	status int
}

func newJWKSServer(t *testing.T, doc []byte) *jwksServer {
	t.Helper()
	js := &jwksServer{doc: doc, status: http.StatusOK}
	js.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		js.hits.Add(1)
		js.mu.Lock()
		body, status := js.doc, js.status
		js.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(js.Close)
	return js
}

func (js *jwksServer) serve(doc []byte, status int) {
	js.mu.Lock()
	defer js.mu.Unlock()
	js.doc, js.status = doc, status
}

// ---------------------------------------------------------------------------
// JWK builders (independent oracle: these encode the JWK by hand from the raw
// key material, they do NOT round-trip through parseJWKS)
// ---------------------------------------------------------------------------

func ecJWK(kid string, pub *ecdsa.PublicKey) map[string]any {
	x := make([]byte, 32)
	y := make([]byte, 32)
	pub.X.FillBytes(x)
	pub.Y.FillBytes(y)
	return map[string]any{
		"kty": "EC", "crv": "P-256", "kid": kid, "use": "sig", "alg": "ES256",
		"x": b64.EncodeToString(x), "y": b64.EncodeToString(y),
	}
}

func okpJWK(kid string, pub ed25519.PublicKey) map[string]any {
	return map[string]any{
		"kty": "OKP", "crv": "Ed25519", "kid": kid, "use": "sig", "alg": "EdDSA",
		"x": b64.EncodeToString(pub),
	}
}

func jwksDocOf(t *testing.T, keys ...map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"keys": keys})
	if err != nil {
		t.Fatalf("marshal jwks: %v", err)
	}
	return raw
}

func newECKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen ec key: %v", err)
	}
	return priv
}

func newEdKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen ed key: %v", err)
	}
	return pub, priv
}

const testIssuer = "https://idp.test/realms/tally"

// testAudience is the OIDC client id these tests pretend cored is registered as.
// EVERY NewJWKSVerifier call site in this file passes it — none passes "" — so no
// test in this file is silently exempt from the audience check, which is how a
// security check quietly stops being exercised.
const testAudience = "cored"

func validClaims(now time.Time, sub string) Claims {
	return Claims{
		Subject:   sub,
		Issuer:    testIssuer,
		Audience:  Audience{testAudience},
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(time.Hour).Unix(),
	}
}

// ---------------------------------------------------------------------------
// accept / reject matrix
// ---------------------------------------------------------------------------

func TestJWKSVerifierAcceptRejectMatrix(t *testing.T) {
	ecPriv := newECKey(t)
	edPub, edPriv := newEdKey(t)
	otherEC := newECKey(t) // same kid published, different key => bad signature

	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)

	doc := jwksDocOf(t,
		ecJWK("ec-1", &ecPriv.PublicKey),
		okpJWK("ed-1", edPub),
		// A symmetric key MUST be ignored entirely: never a verification key.
		map[string]any{"kty": "oct", "kid": "hmac-1", "k": b64.EncodeToString([]byte("super-secret-hmac-key-material!!"))},
		// RSA is not supported by this verifier and must be skipped, not error.
		map[string]any{"kty": "RSA", "kid": "rsa-1", "n": "AQAB", "e": "AQAB"},
		// An empty kid would be reachable by any kid-less token: skipped.
		map[string]any{"kty": "OKP", "crv": "Ed25519", "kid": "", "x": b64.EncodeToString(edPub)},
	)
	js := newJWKSServer(t, doc)

	newVerifier := func() *JWKSVerifier {
		v := NewJWKSVerifier(js.URL, testIssuer, testAudience)
		v.Now = func() time.Time { return now }
		v.MinRefetch = time.Nanosecond // do not throttle inside this matrix
		return v
	}

	edSigner := NewSigner(edPriv, "ed-1", testIssuer)

	mustSignES256 := func(t *testing.T, priv *ecdsa.PrivateKey, kid string, c Claims) string {
		t.Helper()
		tok, err := SignES256(priv, kid, testIssuer, c)
		if err != nil {
			t.Fatalf("SignES256: %v", err)
		}
		return tok
	}
	mustSignEd := func(t *testing.T, c Claims) string {
		t.Helper()
		tok, err := edSigner.Sign(c)
		if err != nil {
			t.Fatalf("Sign: %v", err)
		}
		return tok
	}

	// hmacToken forges an HS256 compact JWS keyed on the published oct key's kid.
	hmacToken := func(t *testing.T) string {
		t.Helper()
		hdr, err := json.Marshal(jwsHeader{Alg: "HS256", Kid: "hmac-1", Typ: "JWT"})
		if err != nil {
			t.Fatalf("marshal hdr: %v", err)
		}
		pl, err := json.Marshal(validClaims(now, "attacker"))
		if err != nil {
			t.Fatalf("marshal claims: %v", err)
		}
		return b64.EncodeToString(hdr) + "." + b64.EncodeToString(pl) + "." + b64.EncodeToString([]byte("sig"))
	}

	cases := []struct {
		name    string
		token   func(t *testing.T) string
		wantErr error // nil => must be accepted
		wantSub string
	}{
		{
			name:    "ES256 signed by the published EC key is accepted",
			token:   func(t *testing.T) string { return mustSignES256(t, ecPriv, "ec-1", validClaims(now, "alice")) },
			wantSub: "alice",
		},
		{
			name:    "EdDSA signed by the published OKP key is accepted",
			token:   func(t *testing.T) string { return mustSignEd(t, validClaims(now, "bob")) },
			wantSub: "bob",
		},
		{
			name: "tampered signature is rejected",
			token: func(t *testing.T) string {
				return tamperSig(t, mustSignES256(t, ecPriv, "ec-1", validClaims(now, "alice")))
			},
			wantErr: ErrBadSignature,
		},
		{
			name:    "right kid, wrong private key is rejected",
			token:   func(t *testing.T) string { return mustSignES256(t, otherEC, "ec-1", validClaims(now, "mallory")) },
			wantErr: ErrBadSignature,
		},
		{
			name:    "unpublished kid is rejected",
			token:   func(t *testing.T) string { return mustSignES256(t, ecPriv, "ec-999", validClaims(now, "alice")) },
			wantErr: ErrUnknownKey,
		},
		{
			name: "empty-kid JWK is not admitted, so a kid-less token is rejected",
			token: func(t *testing.T) string {
				return mustSignEdWithKid(t, edPriv, "", validClaims(now, "alice"))
			},
			wantErr: ErrUnknownKey,
		},
		{
			name: "expired token is rejected",
			token: func(t *testing.T) string {
				c := validClaims(now, "alice")
				c.ExpiresAt = now.Add(-10 * time.Minute).Unix()
				return mustSignES256(t, ecPriv, "ec-1", c)
			},
			wantErr: ErrExpired,
		},
		{
			name: "token WITHOUT exp is rejected (fail-closed: no eternal credentials)",
			token: func(t *testing.T) string {
				c := validClaims(now, "alice")
				c.ExpiresAt = 0
				return mustSignES256(t, ecPriv, "ec-1", c)
			},
			wantErr: ErrMissingExpiry,
		},
		{
			name: "wrong issuer is rejected",
			token: func(t *testing.T) string {
				c := validClaims(now, "alice")
				c.Issuer = "https://evil.test/realms/tally"
				return mustSignES256(t, ecPriv, "ec-1", c)
			},
			wantErr: ErrBadIssuer,
		},
		{
			name:    "HS256 token keyed on a published oct JWK is rejected (symmetric keys never admitted)",
			token:   hmacToken,
			wantErr: ErrUnsupportedAlg,
		},
		{
			name: "alg=none is rejected",
			token: func(t *testing.T) string {
				hdr, _ := json.Marshal(jwsHeader{Alg: "none", Kid: "ec-1", Typ: "JWT"})
				pl, _ := json.Marshal(validClaims(now, "attacker"))
				return b64.EncodeToString(hdr) + "." + b64.EncodeToString(pl) + "."
			},
			wantErr: ErrUnsupportedAlg,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := newVerifier()
			claims, err := v.Verify(context.Background(), tc.token(t))
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("Verify: unexpected error %v", err)
				}
				if claims.Subject != tc.wantSub {
					t.Fatalf("subject = %q, want %q", claims.Subject, tc.wantSub)
				}
				return
			}
			if err == nil {
				t.Fatalf("Verify accepted a token that must be rejected (claims %+v)", claims)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Verify error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// tamperSig flips a bit inside the DECODED signature (not the base64 text, whose
// trailing bits can be unused) so the corruption is guaranteed to be material.
func tamperSig(t *testing.T, tok string) string {
	t.Helper()
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("not a compact JWS: %q", tok)
	}
	sig, err := b64.DecodeString(parts[2])
	if err != nil || len(sig) == 0 {
		t.Fatalf("decode signature: %v", err)
	}
	sig[len(sig)/2] ^= 0xff
	return parts[0] + "." + parts[1] + "." + b64.EncodeToString(sig)
}

// mustSignEdWithKid mints an EdDSA token with an arbitrary (possibly empty) kid.
func mustSignEdWithKid(t *testing.T, priv ed25519.PrivateKey, kid string, c Claims) string {
	t.Helper()
	tok, err := NewSigner(priv, kid, testIssuer).Sign(c)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return tok
}

// ---------------------------------------------------------------------------
// fetch / cache / fail-closed behaviour
// ---------------------------------------------------------------------------

func TestJWKSVerifierFailsClosedWhenEndpointDown(t *testing.T) {
	ecPriv := newECKey(t)
	js := newJWKSServer(t, jwksDocOf(t, ecJWK("ec-1", &ecPriv.PublicKey)))
	url := js.URL
	js.Close() // the endpoint is now dead; the cache was never populated.

	v := NewJWKSVerifier(url, testIssuer, testAudience)
	v.MinRefetch = time.Nanosecond
	now := time.Now()
	tok, err := SignES256(ecPriv, "ec-1", testIssuer, validClaims(now, "alice"))
	if err != nil {
		t.Fatalf("SignES256: %v", err)
	}
	if _, err := v.Verify(context.Background(), tok); err == nil {
		t.Fatal("Verify must fail closed when the JWKS endpoint is unreachable and the cache is empty")
	} else if !errors.Is(err, ErrJWKSUnavailable) {
		t.Fatalf("Verify error = %v, want ErrJWKSUnavailable", err)
	}

	if err := v.Refresh(context.Background()); err == nil {
		t.Fatal("Refresh must fail against a dead endpoint")
	}
}

func TestJWKSVerifierRefreshRejectsBadDocuments(t *testing.T) {
	edPub, _ := newEdKey(t)
	cases := []struct {
		name   string
		doc    []byte
		status int
	}{
		{name: "not json", doc: []byte("<html>nope</html>"), status: http.StatusOK},
		{name: "no usable key (RSA + oct only)", status: http.StatusOK, doc: []byte(
			`{"keys":[{"kty":"RSA","kid":"r","n":"AQAB","e":"AQAB"},{"kty":"oct","kid":"h","k":"c2VjcmV0"}]}`)},
		{name: "empty keys array", doc: []byte(`{"keys":[]}`), status: http.StatusOK},
		{name: "http 500", doc: []byte(`{"keys":[]}`), status: http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			js := newJWKSServer(t, tc.doc)
			js.serve(tc.doc, tc.status)
			v := NewJWKSVerifier(js.URL, testIssuer, testAudience)
			if err := v.Refresh(context.Background()); err == nil {
				t.Fatal("Refresh must reject this JWKS document")
			}
		})
	}

	// Control: a good document DOES refresh, proving the cases above fail for the
	// right reason (the test is not vacuously green).
	js := newJWKSServer(t, jwksDocOf(t, okpJWK("ed-1", edPub)))
	v := NewJWKSVerifier(js.URL, testIssuer, testAudience)
	if err := v.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh of a valid JWKS: %v", err)
	}
}

func TestJWKSVerifierRejectsNonHTTPScheme(t *testing.T) {
	v := NewJWKSVerifier("file:///etc/passwd", testIssuer, testAudience)
	err := v.Refresh(context.Background())
	if err == nil {
		t.Fatal("Refresh must reject a non-http(s) JWKS url")
	}
	if !strings.Contains(err.Error(), "scheme") {
		t.Fatalf("error = %v, want a scheme rejection", err)
	}
}

func TestJWKSVerifierCachesAndThrottles(t *testing.T) {
	ecPriv := newECKey(t)
	js := newJWKSServer(t, jwksDocOf(t, ecJWK("ec-1", &ecPriv.PublicKey)))

	clock := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	v := NewJWKSVerifier(js.URL, testIssuer, testAudience)
	v.Now = func() time.Time { return clock }

	tok, err := SignES256(ecPriv, "ec-1", testIssuer, validClaims(clock, "alice"))
	if err != nil {
		t.Fatalf("SignES256: %v", err)
	}

	// First Verify populates the cache: exactly one fetch.
	if _, err := v.Verify(context.Background(), tok); err != nil {
		t.Fatalf("first Verify: %v", err)
	}
	if got := js.hits.Load(); got != 1 {
		t.Fatalf("fetches after first Verify = %d, want 1", got)
	}

	// 20 more Verifies inside the TTL must issue ZERO further fetches (the
	// assert/finish route verifies twice per request; a refetch per call would
	// make step-up a network amplifier).
	for i := 0; i < 20; i++ {
		if _, err := v.Verify(context.Background(), tok); err != nil {
			t.Fatalf("cached Verify %d: %v", i, err)
		}
	}
	if got := js.hits.Load(); got != 1 {
		t.Fatalf("fetches with a warm cache = %d, want 1", got)
	}

	// 50 tokens with random unknown kids must NOT produce 50 outbound fetches:
	// the MinRefetch throttle caps them (30s default, clock frozen => 1 extra at
	// most... and lastAttempt is already set, so zero).
	before := js.hits.Load()
	for i := 0; i < 50; i++ {
		bad, err := SignES256(ecPriv, fmt.Sprintf("rotated-%d", i), testIssuer, validClaims(clock, "alice"))
		if err != nil {
			t.Fatalf("SignES256: %v", err)
		}
		if _, err := v.Verify(context.Background(), bad); !errors.Is(err, ErrUnknownKey) {
			t.Fatalf("unknown-kid Verify error = %v, want ErrUnknownKey", err)
		}
	}
	if got := js.hits.Load() - before; got > 1 {
		t.Fatalf("unknown-kid fetches = %d, want <= 1 (throttle is load-bearing)", got)
	}

	// After the throttle window elapses, an unknown kid DOES trigger exactly one
	// refetch — and if the IdP has rotated, the new key verifies.
	rotated := newECKey(t)
	js.serve(jwksDocOf(t, ecJWK("ec-2", &rotated.PublicKey)), http.StatusOK)
	clock = clock.Add(time.Hour)
	rotTok, err := SignES256(rotated, "ec-2", testIssuer, validClaims(clock, "carol"))
	if err != nil {
		t.Fatalf("SignES256: %v", err)
	}
	before = js.hits.Load()
	claims, err := v.Verify(context.Background(), rotTok)
	if err != nil {
		t.Fatalf("rotated-key Verify: %v", err)
	}
	if claims.Subject != "carol" {
		t.Fatalf("subject = %q, want carol", claims.Subject)
	}
	if got := js.hits.Load() - before; got != 1 {
		t.Fatalf("rotation fetches = %d, want exactly 1", got)
	}
}

func TestJWKSVerifierServesStaleCacheWithinCapThenFailsClosed(t *testing.T) {
	ecPriv := newECKey(t)
	js := newJWKSServer(t, jwksDocOf(t, ecJWK("ec-1", &ecPriv.PublicKey)))

	clock := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	v := NewJWKSVerifier(js.URL, testIssuer, testAudience)
	v.Now = func() time.Time { return clock }
	v.MinRefetch = time.Nanosecond

	if err := v.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	js.Close() // IdP outage AFTER the cache was warmed.

	// 1h later (past the 5m TTL, inside the 24h hard cap): stale cache serves so
	// a brief IdP outage does not take the whole API down.
	clock = clock.Add(time.Hour)
	tok, err := SignES256(ecPriv, "ec-1", testIssuer, validClaims(clock, "alice"))
	if err != nil {
		t.Fatalf("SignES256: %v", err)
	}
	if _, err := v.Verify(context.Background(), tok); err != nil {
		t.Fatalf("stale-but-within-cap Verify: %v", err)
	}

	// 48h later (past the hard cap): fail closed.
	clock = clock.Add(48 * time.Hour)
	tok2, err := SignES256(ecPriv, "ec-1", testIssuer, validClaims(clock, "alice"))
	if err != nil {
		t.Fatalf("SignES256: %v", err)
	}
	if _, err := v.Verify(context.Background(), tok2); !errors.Is(err, ErrJWKSUnavailable) {
		t.Fatalf("beyond-cap Verify error = %v, want ErrJWKSUnavailable", err)
	}
}

// TestJWKSVerifierIsATokenVerifier pins that JWKSVerifier is usable exactly where
// StaticKeyVerifier is (the buildAuth seam), including through the unexported
// contextLike parameter.
func TestJWKSVerifierIsATokenVerifier(t *testing.T) {
	ecPriv := newECKey(t)
	js := newJWKSServer(t, jwksDocOf(t, ecJWK("ec-1", &ecPriv.PublicKey)))
	var tv TokenVerifier = NewJWKSVerifier(js.URL, testIssuer, testAudience)
	tok, err := SignES256(ecPriv, "ec-1", testIssuer, validClaims(time.Now(), "alice"))
	if err != nil {
		t.Fatalf("SignES256: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := tv.Verify(ctx, tok); err != nil {
		t.Fatalf("Verify through the TokenVerifier interface: %v", err)
	}
}

// ---------------------------------------------------------------------------
// D102 — audience matrix through the full JWKS path
// ---------------------------------------------------------------------------

// mintRawES256 signs an ARBITRARY claims OBJECT, bypassing the Claims struct.
//
// ⛔ THIS HELPER IS LOAD-BEARING, NOT CONVENIENCE. A token minted through Claims
// can only ever serialise `aud` as an ARRAY, so a test limited to that path could
// not express the bare-string form, nor `"aud":42`, nor an absent aud — i.e. it
// could not express any of the shapes a plain-string binding would have
// mishandled, and would have reported green over the D102 hole.
func mintRawES256(t *testing.T, priv *ecdsa.PrivateKey, kid string, payload map[string]any) string {
	t.Helper()
	hb, err := json.Marshal(jwsHeader{Alg: algES256, Kid: kid, Typ: "JWT"})
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	pb, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	signingInput := b64.EncodeToString(hb) + "." + b64.EncodeToString(pb)
	sigPart, err := signES256Raw(priv, []byte(signingInput))
	if err != nil {
		t.Fatalf("signES256Raw: %v", err)
	}
	return signingInput + "." + sigPart
}

func TestJWKSVerifierAudienceMatrix(t *testing.T) {
	ecPriv := newECKey(t)
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	js := newJWKSServer(t, jwksDocOf(t, ecJWK("ec-1", &ecPriv.PublicKey)))

	newVerifier := func(audience string) *JWKSVerifier {
		v := NewJWKSVerifier(js.URL, testIssuer, audience)
		v.Now = func() time.Time { return now }
		v.MinRefetch = time.Nanosecond
		return v
	}

	// base is a fully valid payload MINUS aud; each case supplies its own aud (or
	// none), so the ONLY variable across the matrix is the audience claim and an
	// accept/reject difference cannot be attributed to anything else.
	base := func() map[string]any {
		return map[string]any{
			"sub": "alice",
			"iss": testIssuer,
			"iat": now.Unix(),
			"exp": now.Add(time.Hour).Unix(),
		}
	}

	cases := []struct {
		name    string
		aud     any // nil => omit the claim entirely
		omit    bool
		wantErr error
	}{
		{name: "aud as a bare string matching (RFC 7519 single-audience form)", aud: "cored"},
		{name: "aud as a one-element array matching", aud: []any{"cored"}},
		{name: "aud among several (the shape Keycloak actually emits)", aud: []any{"account", "cored", "realm-management"}},
		{name: "aud for a DIFFERENT client in the same realm is rejected", aud: []any{"other-app"}, wantErr: ErrBadAudience},
		{name: "aud as a bare string for a different client is rejected", aud: "other-app", wantErr: ErrBadAudience},
		{name: "aud listing several other clients but not this one is rejected", aud: []any{"account", "other-app"}, wantErr: ErrBadAudience},
		{name: "NO aud claim at all is rejected", omit: true, wantErr: ErrBadAudience},
		{name: "aud as a number is rejected as malformed", aud: 42, wantErr: ErrMalformedToken},
		{name: "aud as an object is rejected as malformed", aud: map[string]any{"x": 1}, wantErr: ErrMalformedToken},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := base()
			if !tc.omit {
				payload["aud"] = tc.aud
			}
			// Correct issuer, correct kid, valid signature, unexpired: every other
			// gate is deliberately satisfied so the audience is the only thing
			// that can reject this token.
			tok := mintRawES256(t, ecPriv, "ec-1", payload)
			claims, err := newVerifier(testAudience).Verify(context.Background(), tok)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("Verify: %v — a token legitimately minted FOR this application was refused, which locks real users out", err)
				}
				if !claims.Audience.Contains(testAudience) {
					t.Fatalf("accepted token parsed aud as %v, which does not contain %q — the check passed for the wrong reason", []string(claims.Audience), testAudience)
				}
				return
			}
			if err == nil {
				t.Fatalf("Verify ACCEPTED a token whose aud is %v (claims %+v) — a credential minted for ANOTHER CLIENT in the same realm is being honoured as a credential for the books (D102)", tc.aud, claims)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Verify error = %v, want %v", err, tc.wantErr)
			}
		})
	}

	// ⭐ ANTI-VACUITY ROW. Everything above would still pass if the ARRAY form
	// were silently DROPPED to an empty audience: every array case would then hit
	// the "does not contain" branch, and the three accept rows would just look
	// like ordinary failures someone might "fix" by loosening the check. This row
	// pins the other direction — the array form must be PARSED, not merely
	// rejected — by verifying that an array aud round-trips to its exact contents
	// and that a mismatched verifier reports the REAL list it saw.
	t.Run("array aud is parsed, not dropped", func(t *testing.T) {
		payload := base()
		payload["aud"] = []any{"account", "cored"}
		tok := mintRawES256(t, ecPriv, "ec-1", payload)

		claims, err := newVerifier(testAudience).Verify(context.Background(), tok)
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		got := []string(claims.Audience)
		if len(got) != 2 || got[0] != "account" || got[1] != "cored" {
			t.Fatalf("parsed aud = %v, want [account cored] — an array aud that does not round-trip means the claim is being dropped, and a dropped claim makes the audience check pass vacuously for every real Keycloak token", got)
		}

		_, err = newVerifier("some-other-app").Verify(context.Background(), tok)
		if !errors.Is(err, ErrBadAudience) {
			t.Fatalf("verifier pinned to some-other-app returned %v, want ErrBadAudience", err)
		}
		if !strings.Contains(err.Error(), "account") || !strings.Contains(err.Error(), "cored") {
			t.Fatalf("rejection message %q does not name the audiences actually seen; an empty list here would mean the array was dropped rather than compared", err)
		}
	})
}
