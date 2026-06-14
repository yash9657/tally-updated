package authn

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
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
