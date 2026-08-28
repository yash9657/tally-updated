package authn

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	signer, verifier, err := NewTestIssuer()
	if err != nil {
		t.Fatalf("new test issuer: %v", err)
	}
	now := time.Now()
	tok, err := signer.Sign(Claims{
		Subject:   "alice",
		Email:     "alice@example.com",
		AMR:       []string{"pwd", "webauthn"},
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	claims, err := verifier.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.Subject != "alice" || claims.Email != "alice@example.com" {
		t.Fatalf("claims mismatch: %+v", claims)
	}
	if !claims.HasWebAuthn() {
		t.Fatal("expected HasWebAuthn from AMR")
	}
	if claims.Issuer != "cored-dev" {
		t.Fatalf("issuer = %q, want cored-dev", claims.Issuer)
	}
}

func TestServicePrefix(t *testing.T) {
	c := Claims{Subject: "svc:statutoryd"}
	if !c.IsService() {
		t.Fatal("svc: subject must be a service principal")
	}
	if (&Claims{Subject: "alice"}).IsService() {
		t.Fatal("a normal subject is not a service principal")
	}
}

func TestRejectAlgNone(t *testing.T) {
	signer, verifier, _ := NewTestIssuer()
	tok, _ := signer.Sign(Claims{Subject: "x", ExpiresAt: time.Now().Add(time.Hour).Unix()})
	parts := strings.Split(tok, ".")
	// Forge an alg=none header keeping the same payload, empty signature.
	noneHdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","kid":"test-ed25519","typ":"JWT"}`))
	forged := noneHdr + "." + parts[1] + "."
	if _, err := verifier.Verify(context.Background(), forged); err == nil {
		t.Fatal("SECURITY: alg=none token was accepted")
	}
}

func TestRejectHS256(t *testing.T) {
	_, verifier, _ := NewTestIssuer()
	hsHdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","kid":"test-ed25519","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"x"}`))
	forged := hsHdr + "." + payload + "." + base64.RawURLEncoding.EncodeToString([]byte("anything"))
	if _, err := verifier.Verify(context.Background(), forged); err == nil {
		t.Fatal("SECURITY: HS256 token was accepted")
	}
}

func TestRejectTamperedPayload(t *testing.T) {
	signer, verifier, _ := NewTestIssuer()
	tok, _ := signer.Sign(Claims{Subject: "alice", ExpiresAt: time.Now().Add(time.Hour).Unix()})
	parts := strings.Split(tok, ".")
	// Swap the payload to a different subject; signature no longer matches.
	tampered := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"attacker","iss":"cored-dev"}`))
	forged := parts[0] + "." + tampered + "." + parts[2]
	if _, err := verifier.Verify(context.Background(), forged); err == nil {
		t.Fatal("SECURITY: tampered payload was accepted")
	}
}

func TestRejectExpired(t *testing.T) {
	signer, verifier, _ := NewTestIssuer()
	tok, _ := signer.Sign(Claims{Subject: "alice", ExpiresAt: time.Now().Add(-2 * time.Hour).Unix()})
	if _, err := verifier.Verify(context.Background(), tok); err == nil {
		t.Fatal("expired token must be rejected")
	}
}

func TestRejectWrongIssuer(t *testing.T) {
	signer, _, _ := NewTestIssuer()
	tok, _ := signer.Sign(Claims{Subject: "alice", Issuer: "evil", ExpiresAt: time.Now().Add(time.Hour).Unix()})
	v := NewStaticKeyVerifier(signer.Kid(), signer.Public(), "cored-dev")
	if _, err := v.Verify(context.Background(), tok); err == nil {
		t.Fatal("wrong issuer must be rejected")
	}
}

func TestRejectUnknownKid(t *testing.T) {
	signer, _, _ := NewTestIssuer()
	tok, _ := signer.Sign(Claims{Subject: "alice", ExpiresAt: time.Now().Add(time.Hour).Unix()})
	v := NewStaticKeyVerifier("other-kid", signer.Public(), "cored-dev")
	if _, err := v.Verify(context.Background(), tok); err == nil {
		t.Fatal("unknown kid must be rejected")
	}
}

// TestES256RoundTrip exercises the ES256 (Keycloak default) verify path with a
// stdlib ECDSA P-256 key, non-vacuously: a tampered token must fail.
func TestES256RoundTrip(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa keygen: %v", err)
	}
	const kid = "es256-kid"
	const issuer = "kc"
	tok, err := SignES256(priv, kid, issuer, Claims{
		Subject: "bob", ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign es256: %v", err)
	}
	v := NewStaticKeyVerifier(kid, &priv.PublicKey, issuer)
	claims, err := v.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("verify es256: %v", err)
	}
	if claims.Subject != "bob" {
		t.Fatalf("subject = %q, want bob", claims.Subject)
	}
	// Tamper: flip a byte in the signature -> must reject.
	parts := strings.Split(tok, ".")
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sig[0] ^= 0xff
	forged := parts[0] + "." + parts[1] + "." + base64.RawURLEncoding.EncodeToString(sig)
	if _, err := v.Verify(context.Background(), forged); err == nil {
		t.Fatal("SECURITY: tampered ES256 signature accepted")
	}
}

// TestMalformedToken covers structurally bad tokens.
func TestMalformedToken(t *testing.T) {
	_, verifier, _ := NewTestIssuer()
	for _, bad := range []string{"", "a.b", "a.b.c.d", "not-base64!.x.y"} {
		if _, err := verifier.Verify(context.Background(), bad); err == nil {
			t.Fatalf("malformed token %q must be rejected", bad)
		}
	}
}

// sanity: a signed token decodes to the same claims JSON we put in.
func TestPayloadShape(t *testing.T) {
	signer, _, _ := NewTestIssuer()
	tok, _ := signer.Sign(Claims{Subject: "x", Companies: []string{"c1"}, ExpiresAt: time.Now().Add(time.Hour).Unix()})
	payload := strings.Split(tok, ".")[1]
	raw, _ := base64.RawURLEncoding.DecodeString(payload)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("payload not json: %v", err)
	}
	if m["sub"] != "x" {
		t.Fatalf("sub = %v, want x", m["sub"])
	}
}

// ---------------------------------------------------------------------------
// D102 — the `aud` claim
// ---------------------------------------------------------------------------

// TestAudienceAcceptsBothRFC7519Forms is the unit-level guard on the dual-form
// trap, independent of any verifier.
//
// ⛔ THIS TEST IS THE ONE THAT FAILS IF `aud` IS EVER BOUND AS A PLAIN string.
// The array subtests would not merely mismatch — encoding/json would refuse the
// array outright, which is why the bug was invisible in dev (where nothing sets
// aud) and would only have surfaced against a real Keycloak.
func TestAudienceAcceptsBothRFC7519Forms(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{name: "bare string (RFC 7519 single-audience special case)", raw: `{"aud":"cored"}`, want: []string{"cored"}},
		{name: "one-element array", raw: `{"aud":["cored"]}`, want: []string{"cored"}},
		{name: "multi-element array (the shape Keycloak actually emits)", raw: `{"aud":["account","cored"]}`, want: []string{"account", "cored"}},
		{name: "absent", raw: `{"sub":"alice"}`, want: nil},
		{name: "explicit null", raw: `{"aud":null}`, want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var c Claims
			if err := json.Unmarshal([]byte(tc.raw), &c); err != nil {
				t.Fatalf("unmarshal %s: %v — a legal RFC 7519 aud form was rejected, so every token an IdP emits in this shape would be refused as malformed", tc.raw, err)
			}
			if len(c.Audience) != len(tc.want) {
				t.Fatalf("aud from %s = %v (len %d), want %v (len %d)", tc.raw, []string(c.Audience), len(c.Audience), tc.want, len(tc.want))
			}
			for i := range tc.want {
				if c.Audience[i] != tc.want[i] {
					t.Fatalf("aud from %s = %v, want %v", tc.raw, []string(c.Audience), tc.want)
				}
			}
		})
	}

	// Non-string shapes must ERROR rather than silently yielding an empty
	// audience — an empty audience is what an emptiness-skipping check treats as
	// "nothing to compare", i.e. a vacuous pass.
	for _, raw := range []string{`{"aud":42}`, `{"aud":{"x":1}}`, `{"aud":["ok",7]}`, `{"aud":true}`} {
		var c Claims
		if err := json.Unmarshal([]byte(raw), &c); err == nil {
			t.Fatalf("unmarshal %s succeeded with aud %v; a non-string aud must be an error, not an empty audience that later checks skip", raw, []string(c.Audience))
		}
	}

	// And a Claims with no audience still marshals WITHOUT an aud key, so the
	// dev signer's token shape is byte-for-byte unchanged by this field.
	enc, err := json.Marshal(Claims{Subject: "dev"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(enc), "aud") {
		t.Fatalf("Claims{Subject:\"dev\"} marshalled to %s; omitempty must keep aud out of dev tokens", enc)
	}
}

// TestStaticKeyVerifierWithNoAudienceConfiguredAcceptsTokensWithoutAud is the
// explicit regression pin that the DEV PATH is untouched: devIssuer and
// NewTestIssuer leave StaticKeyVerifier.Audience empty, so the audience check
// must not fire and /auth/token, /auth/login and the local loop keep working.
func TestStaticKeyVerifierWithNoAudienceConfiguredAcceptsTokensWithoutAud(t *testing.T) {
	signer, verifier, err := NewTestIssuer()
	if err != nil {
		t.Fatalf("new test issuer: %v", err)
	}
	if verifier.Audience != "" {
		t.Fatalf("NewTestIssuer produced a verifier with Audience %q; the dev issuer must not pin an audience", verifier.Audience)
	}
	tok, err := signer.Sign(Claims{Subject: "alice", ExpiresAt: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	claims, err := verifier.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("verify: %v — the audience check fired on a verifier with NO audience configured, which breaks the dev issuer, /auth/token, /auth/login and every existing test in the tree", err)
	}
	if claims.Subject != "alice" {
		t.Fatalf("subject = %q, want alice", claims.Subject)
	}
}

// TestStaticKeyVerifierAudienceEnforcement pins the check at the layer it lives
// at, beside the issuer check, without any JWKS machinery in the way.
func TestStaticKeyVerifierAudienceEnforcement(t *testing.T) {
	signer, verifier, err := NewTestIssuer()
	if err != nil {
		t.Fatalf("new test issuer: %v", err)
	}
	verifier.Audience = "cored"

	exp := time.Now().Add(time.Hour).Unix()
	cases := []struct {
		name    string
		aud     Audience
		wantErr error
	}{
		{name: "exact audience", aud: Audience{"cored"}},
		{name: "audience among several", aud: Audience{"account", "cored", "realm-management"}},
		{name: "audience for another client is rejected", aud: Audience{"other-app"}, wantErr: ErrBadAudience},
		{name: "no audience at all is rejected", aud: nil, wantErr: ErrBadAudience},
		{name: "empty audience list is rejected", aud: Audience{}, wantErr: ErrBadAudience},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tok, err := signer.Sign(Claims{Subject: "alice", Audience: tc.aud, ExpiresAt: exp})
			if err != nil {
				t.Fatalf("sign: %v", err)
			}
			_, err = verifier.Verify(context.Background(), tok)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("verify: %v — a token correctly minted for this application was refused", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("a token with aud %v was ACCEPTED by a verifier pinned to %q — a credential minted for another application is being honoured as one for the books (D102)", []string(tc.aud), verifier.Audience)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("verify error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}
