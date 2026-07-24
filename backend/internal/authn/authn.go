// Package authn verifies and mints compact JWS (JWT) tokens for cored using the
// Go STANDARD LIBRARY ONLY — no jose/jwt dependency, because the deployment box
// is offline. It supports two asymmetric algorithms:
//
//   - EdDSA (Ed25519, crypto/ed25519) — the dev/test issuer's algorithm.
//   - ES256 (ECDSA P-256, crypto/ecdsa + crypto/elliptic) — Keycloak's default.
//
// alg=none and the HS* (HMAC) family are REJECTED unless explicitly configured,
// which they never are here (no symmetric keys are accepted). THIS FILE never
// touches the network; the sibling jwks.go adds the production JWKSVerifier,
// which DOES fetch an IdP's JWKS over HTTP (see its file doc).
//
// This file is owned by WS1 (Task D). The sibling lockout*.go files (WS2) live
// in this package but share NO symbols with this file.
package authn

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// Claims is the verified identity carried by a token. It is the only thing
// downstream authz code trusts about the caller.
type Claims struct {
	Subject   string   `json:"sub"`
	Email     string   `json:"email,omitempty"`
	Companies []string `json:"companies,omitempty"` // optional hint; NOT authoritative
	AMR       []string `json:"amr,omitempty"`       // contains "webauthn" after step-up
	Issuer    string   `json:"iss"`
	ExpiresAt int64    `json:"exp"`
	IssuedAt  int64    `json:"iat"`
	ID        string   `json:"jti,omitempty"`
}

// HasWebAuthn reports whether the token's AMR claim attests a WebAuthn step-up.
func (c *Claims) HasWebAuthn() bool {
	for _, m := range c.AMR {
		if m == "webauthn" {
			return true
		}
	}
	return false
}

// IsService reports whether this is a service principal (subject prefix "svc:",
// e.g. "svc:statutoryd"). The PDP grants service principals full access scoped
// to the targeted company. The prefix is the pinned marker the statutoryd client
// and the middleware both agree on.
func (c *Claims) IsService() bool { return strings.HasPrefix(c.Subject, ServicePrefix) }

// ServicePrefix is the pinned subject prefix for service tokens.
const ServicePrefix = "svc:"

// Common verification errors.
var (
	ErrMalformedToken = errors.New("authn: malformed token")
	ErrUnknownKey     = errors.New("authn: unknown signing key (kid)")
	ErrBadSignature   = errors.New("authn: bad signature")
	ErrUnsupportedAlg = errors.New("authn: unsupported or forbidden alg")
	ErrExpired        = errors.New("authn: token expired")
	ErrNotYetValid    = errors.New("authn: token not yet valid (iat in future)")
	ErrBadIssuer      = errors.New("authn: unexpected issuer")
)

// TokenVerifier verifies a compact JWS and returns its claims.
type TokenVerifier interface {
	Verify(ctx contextLike, rawJWT string) (*Claims, error)
}

// contextLike is the subset of context.Context Verify needs. Declared locally so
// the package does not need to import context just for an unused parameter; it is
// satisfied by context.Context.
type contextLike interface {
	Deadline() (deadline time.Time, ok bool)
	Done() <-chan struct{}
	Err() error
	Value(key any) any
}

// jwsHeader is the protected header of a compact JWS.
type jwsHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

// allowedSkew tolerates small clock differences when checking iat.
const allowedSkew = 60 * time.Second

// StaticKeyVerifier verifies tokens against a fixed set of public keys keyed by
// kid, requiring a fixed issuer. It is the hermetic verifier used by tests and
// the dev /auth/token endpoint; the production swap is a JWKSVerifier.
type StaticKeyVerifier struct {
	// Keys maps a kid to its public key (ed25519.PublicKey or *ecdsa.PublicKey).
	Keys map[string]crypto.PublicKey
	// Issuer, when non-empty, must equal the token's iss claim.
	Issuer string
	// Now is overridable for deterministic tests; defaults to time.Now.
	Now func() time.Time
}

// NewStaticKeyVerifier builds a verifier for one kid/public key pair.
func NewStaticKeyVerifier(kid string, pub crypto.PublicKey, issuer string) *StaticKeyVerifier {
	return &StaticKeyVerifier{
		Keys:   map[string]crypto.PublicKey{kid: pub},
		Issuer: issuer,
	}
}

func (v *StaticKeyVerifier) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

// Verify parses the compact JWS, checks the signature against the keyed public
// key, and validates alg/iss/exp/iat. It NEVER trusts alg=none or HS*.
func (v *StaticKeyVerifier) Verify(_ contextLike, rawJWT string) (*Claims, error) {
	parts := strings.Split(rawJWT, ".")
	if len(parts) != 3 {
		return nil, ErrMalformedToken
	}
	headerBytes, err := b64.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("%w: header b64", ErrMalformedToken)
	}
	var hdr jwsHeader
	if err := json.Unmarshal(headerBytes, &hdr); err != nil {
		return nil, fmt.Errorf("%w: header json", ErrMalformedToken)
	}

	// Reject everything except the two asymmetric algs we explicitly support.
	switch hdr.Alg {
	case algEdDSA, algES256:
		// ok
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedAlg, hdr.Alg)
	}

	pub, ok := v.Keys[hdr.Kid]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownKey, hdr.Kid)
	}

	signingInput := parts[0] + "." + parts[1]
	sig, err := b64.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("%w: signature b64", ErrMalformedToken)
	}
	if err := verifySignature(hdr.Alg, pub, []byte(signingInput), sig); err != nil {
		return nil, err
	}

	payloadBytes, err := b64.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: payload b64", ErrMalformedToken)
	}
	var claims Claims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, fmt.Errorf("%w: payload json", ErrMalformedToken)
	}

	if v.Issuer != "" && claims.Issuer != v.Issuer {
		return nil, fmt.Errorf("%w: got %q want %q", ErrBadIssuer, claims.Issuer, v.Issuer)
	}
	now := v.now()
	if claims.ExpiresAt != 0 && now.After(time.Unix(claims.ExpiresAt, 0).Add(allowedSkew)) {
		return nil, ErrExpired
	}
	if claims.IssuedAt != 0 && now.Add(allowedSkew).Before(time.Unix(claims.IssuedAt, 0)) {
		return nil, ErrNotYetValid
	}
	return &claims, nil
}

// verifySignature checks sig over signingInput using pub for the given alg.
func verifySignature(alg string, pub crypto.PublicKey, signingInput, sig []byte) error {
	switch alg {
	case algEdDSA:
		edPub, ok := pub.(ed25519.PublicKey)
		if !ok {
			return fmt.Errorf("%w: key is not ed25519", ErrUnsupportedAlg)
		}
		if !ed25519.Verify(edPub, signingInput, sig) {
			return ErrBadSignature
		}
		return nil
	case algES256:
		ecPub, ok := pub.(*ecdsa.PublicKey)
		if !ok {
			return fmt.Errorf("%w: key is not ecdsa", ErrUnsupportedAlg)
		}
		// JWS ES256 signature is the raw r||s, each 32 bytes, big-endian.
		if len(sig) != 64 {
			return ErrBadSignature
		}
		r := new(big.Int).SetBytes(sig[:32])
		s := new(big.Int).SetBytes(sig[32:])
		h := sha256.Sum256(signingInput)
		if !ecdsa.Verify(ecPub, h[:], r, s) {
			return ErrBadSignature
		}
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedAlg, alg)
	}
}

// --------------------------------------------------------------------------
// Signer (test/dev issuer)
// --------------------------------------------------------------------------

const (
	algEdDSA = "EdDSA"
	algES256 = "ES256"
)

// b64 is the RawURL (no padding) base64 encoding JWS uses everywhere.
var b64 = base64.RawURLEncoding

// Signer mints compact JWS tokens with an Ed25519 private key. It is used by
// tests AND by the dev /auth/token endpoint. The matching public key (under the
// same kid) verifies the token via a StaticKeyVerifier.
type Signer struct {
	priv   ed25519.PrivateKey
	kid    string
	issuer string
}

// NewSigner builds a Signer over an existing Ed25519 private key.
func NewSigner(priv ed25519.PrivateKey, kid, issuer string) *Signer {
	return &Signer{priv: priv, kid: kid, issuer: issuer}
}

// Public returns the verifying public key for this signer.
func (s *Signer) Public() ed25519.PublicKey { return s.priv.Public().(ed25519.PublicKey) }

// Kid returns the key id stamped into every token header.
func (s *Signer) Kid() string { return s.kid }

// Issuer returns the iss this signer stamps (and the verifier should expect).
func (s *Signer) Issuer() string { return s.issuer }

// Sign produces a compact JWS over the claims. If Issuer/IssuedAt are unset they
// default to the signer's issuer / now. The header alg is EdDSA.
func (s *Signer) Sign(c Claims) (string, error) {
	if c.Issuer == "" {
		c.Issuer = s.issuer
	}
	if c.IssuedAt == 0 {
		c.IssuedAt = time.Now().Unix()
	}
	hdr := jwsHeader{Alg: algEdDSA, Kid: s.kid, Typ: "JWT"}
	hb, err := json.Marshal(hdr)
	if err != nil {
		return "", err
	}
	pb, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	signingInput := b64.EncodeToString(hb) + "." + b64.EncodeToString(pb)
	sig := ed25519.Sign(s.priv, []byte(signingInput))
	return signingInput + "." + b64.EncodeToString(sig), nil
}

// NewTestIssuer returns a matched Signer + StaticKeyVerifier sharing a kid and
// issuer, with a freshly generated Ed25519 key. Tests use this to mint tokens
// the server's verifier accepts without any network or external key material.
func NewTestIssuer() (*Signer, *StaticKeyVerifier, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("authn: generate test key: %w", err)
	}
	const kid = "test-ed25519"
	const issuer = "cored-dev"
	signer := NewSigner(priv, kid, issuer)
	verifier := NewStaticKeyVerifier(kid, pub, issuer)
	return signer, verifier, nil
}

// --------------------------------------------------------------------------
// ES256 helpers (used by a future JWKSVerifier and exercised by tests so the
// ES256 verify path is non-vacuously covered without a network).
// --------------------------------------------------------------------------

// signES256Raw signs h (a 32-byte digest) producing the raw r||s JWS encoding.
// Exposed for tests that mint an ES256 token to exercise the verify path.
func signES256Raw(priv *ecdsa.PrivateKey, signingInput []byte) (string, error) {
	h := sha256.Sum256(signingInput)
	der, err := ecdsa.SignASN1(rand.Reader, priv, h[:])
	if err != nil {
		return "", err
	}
	// Convert ASN.1 DER (SEQUENCE { r INTEGER, s INTEGER }) to raw r||s.
	var parsed struct{ R, S *big.Int }
	if _, err := asn1.Unmarshal(der, &parsed); err != nil {
		return "", err
	}
	raw := make([]byte, 64)
	parsed.R.FillBytes(raw[:32])
	parsed.S.FillBytes(raw[32:])
	return b64.EncodeToString(raw), nil
}

// SignES256 mints a compact JWS using an ECDSA P-256 key (ES256). Provided so a
// JWKSVerifier swap (Keycloak default alg) and tests can produce ES256 tokens
// with stdlib only. The curve MUST be P-256.
func SignES256(priv *ecdsa.PrivateKey, kid, issuer string, c Claims) (string, error) {
	if priv.Curve != elliptic.P256() {
		return "", fmt.Errorf("%w: ES256 requires P-256", ErrUnsupportedAlg)
	}
	if c.Issuer == "" {
		c.Issuer = issuer
	}
	if c.IssuedAt == 0 {
		c.IssuedAt = time.Now().Unix()
	}
	hdr := jwsHeader{Alg: algES256, Kid: kid, Typ: "JWT"}
	hb, err := json.Marshal(hdr)
	if err != nil {
		return "", err
	}
	pb, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	signingInput := b64.EncodeToString(hb) + "." + b64.EncodeToString(pb)
	sigPart, err := signES256Raw(priv, []byte(signingInput))
	if err != nil {
		return "", err
	}
	return signingInput + "." + sigPart, nil
}

// ConstantTimeEqual compares two byte slices in constant time. Used by callers
// (e.g. WebAuthn challenge checks) that must avoid timing leaks.
func ConstantTimeEqual(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}
