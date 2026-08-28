// jwks.go adds the PRODUCTION token verifier: JWKSVerifier fetches an OIDC
// provider's JWKS document (Keycloak: /realms/{r}/protocol/openid-connect/certs)
// over HTTP, caches the public keys by kid, and delegates the actual compact-JWS
// verification to StaticKeyVerifier so there is ZERO duplicated crypto/claim
// logic.
//
// NETWORK DISCLOSURE (this file is the ONLY networked code in package authn —
// the package doc's "never touches the network" sentence predates it and refers
// to authn.go): JWKSVerifier issues outbound GETs to its configured URL, at boot
// (Refresh) and lazily from Verify when the cache is empty/stale or a token
// carries an unknown kid. Every fetch is bounded: 5s client timeout, 1 MiB
// response cap, http/https only, and a MinRefetch throttle so an attacker
// spraying random kids cannot amplify one inbound request into one outbound
// request.
//
// FAIL-CLOSED POSTURE:
//   - No usable cached key + fetch failure => verification error. Never "allow".
//   - Only asymmetric JWKs are admitted: EC/P-256 (ES256) and OKP/Ed25519
//     (EdDSA). RSA, oct (HMAC) and anything else are SKIPPED at parse time, so a
//     symmetric key published in the JWKS can never become a verification key.
//   - JWKs with an EMPTY kid are SKIPPED: StaticKeyVerifier looks keys up by the
//     token header's kid, so a ""-kid entry would be reachable by any token that
//     omits kid.
//   - A token with NO exp is REJECTED (ErrMissingExpiry). StaticKeyVerifier
//     treats exp as optional (it also serves the hermetic dev issuer); for an
//     external IdP a missing exp would be a permanent credential.
//   - The issuer is REQUIRED by the caller (server.buildAuth refuses to start
//     when AUTH_ISSUER is empty), so a token minted by a DIFFERENT realm that
//     happens to share the signing key cannot be replayed here.
//
// KNOWN GAP (deliberate, v1) — REGISTERED AS D102 (HANDOFF-PARITY-AUDIT.md §7.1),
// 2026-08-27: the `aud` claim is NOT checked — Claims does not even parse it.
// Pinning `iss` scopes acceptance to one realm, but every CLIENT inside that
// realm issues tokens with the same `iss`, so any such token is accepted by
// cored. Deploy cored's realm with only trusted clients until an AUTH_AUDIENCE
// check lands.
//
// ⚠️ THAT LAST SENTENCE IS A DEPLOYMENT CONSTRAINT AND IT LIVES HERE, WHERE NO
// OPERATOR WILL READ IT. The production recipe operators are actually handed
// (docs/SESSION_HANDOFF_WAVE2.md) sets AUTH_JWKS_URL + AUTH_ISSUER and says
// nothing about the realm needing to be single-tenant, and nothing warns at
// boot. Whoever lands the audience check must move the constraint into that
// recipe; D102 carries the reasoning and the shape of the fix, including why
// `aud` must NOT be bound as a plain string (RFC 7519 permits an array, and
// that is the form Keycloak emits).
//
// (This note previously pointed at "HANDOFF §6" — a dangling reference: §6 of
// HANDOFF.md is the migration-numbering section and has never mentioned the
// audience claim. Corrected when D102 was registered.)
//
// PRODUCTION SHAPE: when AUTH_JWKS_URL is set there is NO in-process signer, so
// the dev endpoints POST /auth/token and POST /auth/login are NOT mounted
// (server.go routes()/authWebAuthnRoutes()) and POST /auth/webauthn/assert/finish
// returns 501 — WebAuthn step-up is performed by the external IdP, which stamps
// amr:["webauthn"] on the token it issues.
package authn

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// JWKS-specific errors. (The shared verification errors live in authn.go.)
var (
	// ErrMissingExpiry rejects a token without an exp claim. Asymmetric-signed
	// but eternal is not a credential this system accepts from an external IdP.
	ErrMissingExpiry = errors.New("authn: token has no exp claim")
	// ErrJWKSUnavailable reports that no usable signing key could be obtained.
	ErrJWKSUnavailable = errors.New("authn: JWKS unavailable and no usable cached keys")
	// ErrJWKSInvalid reports a JWKS document that yielded no usable key.
	ErrJWKSInvalid = errors.New("authn: JWKS contained no usable asymmetric key")
)

const (
	// defaultJWKSTTL is how long cached keys are considered fresh.
	defaultJWKSTTL = 5 * time.Minute
	// jwksStaleCap is the hard cap beyond which cached keys are NOT served even
	// when a refetch fails. Between TTL and this cap a stale cache may serve, so
	// a brief IdP outage does not take the API down.
	jwksStaleCap = 24 * time.Hour
	// defaultMinRefetch throttles Verify-triggered fetches. Without it, tokens
	// carrying random kids would give an unauthenticated caller one outbound
	// request per inbound request (DoS / SSRF amplification).
	defaultMinRefetch = 30 * time.Second
	// defaultJWKSTimeout bounds a single JWKS fetch.
	defaultJWKSTimeout = 5 * time.Second
	// maxJWKSBytes caps the JWKS response body.
	maxJWKSBytes = 1 << 20 // 1 MiB
)

// errThrottled is the internal sentinel for a suppressed refetch.
var errThrottled = errors.New("authn: JWKS refetch throttled")

// JWKSVerifier verifies tokens against the public keys published at a JWKS URL.
// The zero value is not usable; construct it with NewJWKSVerifier.
type JWKSVerifier struct {
	// URL is the JWKS endpoint (http/https only).
	URL string
	// Issuer, when non-empty, must equal the token's iss claim. Callers are
	// expected to REQUIRE it in production (see the file doc).
	Issuer string
	// TTL is the cache freshness window; zero means defaultJWKSTTL.
	TTL time.Duration
	// MinRefetch throttles Verify-triggered fetches; zero means
	// defaultMinRefetch. Tests may set a small value.
	MinRefetch time.Duration
	// Client performs the fetch; nil means a 5s-timeout http.Client.
	Client *http.Client
	// Now is overridable for deterministic tests; defaults to time.Now.
	Now func() time.Time

	mu          sync.Mutex
	keys        map[string]crypto.PublicKey
	fetchedAt   time.Time
	lastAttempt time.Time
}

// NewJWKSVerifier builds a verifier for a JWKS endpoint and an expected issuer.
// It performs NO I/O: call Refresh once at boot to fail closed on a misconfigured
// or unreachable IdP, after which Verify keeps the cache warm lazily.
func NewJWKSVerifier(jwksURL, issuer string) *JWKSVerifier {
	return &JWKSVerifier{URL: jwksURL, Issuer: issuer}
}

func (v *JWKSVerifier) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

func (v *JWKSVerifier) ttl() time.Duration {
	if v.TTL > 0 {
		return v.TTL
	}
	return defaultJWKSTTL
}

func (v *JWKSVerifier) minRefetch() time.Duration {
	if v.MinRefetch > 0 {
		return v.MinRefetch
	}
	return defaultMinRefetch
}

func (v *JWKSVerifier) client() *http.Client {
	if v.Client != nil {
		return v.Client
	}
	return &http.Client{Timeout: defaultJWKSTimeout}
}

// snapshot returns a copy of the cached keys plus their age. have is false when
// the cache has never been populated.
func (v *JWKSVerifier) snapshot() (keys map[string]crypto.PublicKey, age time.Duration, have bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.keys) == 0 {
		return nil, 0, false
	}
	out := make(map[string]crypto.PublicKey, len(v.keys))
	for k, pub := range v.keys {
		out[k] = pub
	}
	return out, v.now().Sub(v.fetchedAt), true
}

// Refresh fetches and parses the JWKS, replacing the cache on success. It is
// called ONCE at boot (server.buildAuth) so a dead or malformed JWKS endpoint
// is a hard startup failure instead of a per-request 401 discovered in prod.
// It bypasses the MinRefetch throttle (it is not attacker-triggerable).
func (v *JWKSVerifier) Refresh(ctx context.Context) error {
	v.mu.Lock()
	v.lastAttempt = v.now()
	v.mu.Unlock()

	keys, err := v.fetch(ctx)
	if err != nil {
		return err
	}
	v.mu.Lock()
	v.keys = keys
	v.fetchedAt = v.now()
	v.mu.Unlock()
	return nil
}

// throttledRefresh is the Verify-triggered fetch: at most one per MinRefetch.
func (v *JWKSVerifier) throttledRefresh(ctx context.Context) error {
	v.mu.Lock()
	if !v.lastAttempt.IsZero() && v.now().Sub(v.lastAttempt) < v.minRefetch() {
		v.mu.Unlock()
		return errThrottled
	}
	v.mu.Unlock()
	return v.Refresh(ctx)
}

// fetch performs one bounded GET and parses the document.
func (v *JWKSVerifier) fetch(ctx context.Context) (map[string]crypto.PublicKey, error) {
	u, err := url.Parse(v.URL)
	if err != nil {
		return nil, fmt.Errorf("authn: parse JWKS url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("authn: JWKS url scheme %q not allowed (http/https only)", u.Scheme)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("authn: build JWKS request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := v.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("authn: fetch JWKS: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("authn: fetch JWKS: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxJWKSBytes))
	if err != nil {
		return nil, fmt.Errorf("authn: read JWKS: %w", err)
	}
	return parseJWKS(body)
}

// jwksDoc / jwkKey are the subset of RFC 7517 we consume.
type jwksDoc struct {
	Keys []jwkKey `json:"keys"`
}

type jwkKey struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// parseJWKS turns a JWKS document into a kid -> public key map, admitting ONLY
// EC/P-256 and OKP/Ed25519 keys with a non-empty kid. Everything else (RSA,
// oct/HMAC, unknown curves, use != "sig") is skipped, so an operator or attacker
// who gets a symmetric key into the document cannot make it a verification key.
// First entry wins on a duplicate kid.
func parseJWKS(body []byte) (map[string]crypto.PublicKey, error) {
	var doc jwksDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("authn: parse JWKS json: %w", err)
	}
	keys := map[string]crypto.PublicKey{}
	for _, k := range doc.Keys {
		if k.Kid == "" {
			continue // unaddressable by kid lookup; would match a kid-less token.
		}
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		if _, dup := keys[k.Kid]; dup {
			continue
		}
		switch {
		case k.Kty == "EC" && k.Crv == "P-256":
			xb, err := b64.DecodeString(k.X)
			if err != nil || len(xb) != 32 {
				continue
			}
			yb, err := b64.DecodeString(k.Y)
			if err != nil || len(yb) != 32 {
				continue
			}
			x := new(big.Int).SetBytes(xb)
			y := new(big.Int).SetBytes(yb)
			if !elliptic.P256().IsOnCurve(x, y) {
				continue
			}
			keys[k.Kid] = &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}
		case k.Kty == "OKP" && k.Crv == "Ed25519":
			xb, err := b64.DecodeString(k.X)
			if err != nil || len(xb) != ed25519.PublicKeySize {
				continue
			}
			keys[k.Kid] = ed25519.PublicKey(xb)
		default:
			// RSA, oct (symmetric), unknown curves: never admitted.
			continue
		}
	}
	if len(keys) == 0 {
		return nil, ErrJWKSInvalid
	}
	return keys, nil
}

// Verify verifies a compact JWS against the cached JWKS.
//
// Cache policy: fresh cache -> no I/O. Stale (older than TTL) or empty -> one
// throttled fetch; on fetch failure a cache younger than jwksStaleCap still
// serves, an empty or older-than-cap cache fails closed. An unknown kid
// (key rotation) triggers ONE throttled refetch and a single retry.
func (v *JWKSVerifier) Verify(ctx contextLike, rawJWT string) (*Claims, error) {
	// contextLike has exactly context.Context's method set, so this conversion
	// is a compile-time no-op — no adapter type needed.
	var fetchCtx context.Context = ctx
	if fetchCtx == nil {
		fetchCtx = context.Background()
	}

	keys, age, have := v.snapshot()
	if !have || age > v.ttl() {
		err := v.throttledRefresh(fetchCtx)
		switch {
		case err == nil:
			keys, age, have = v.snapshot()
		case !have:
			return nil, fmt.Errorf("%w: %v", ErrJWKSUnavailable, err)
		case age > jwksStaleCap:
			return nil, fmt.Errorf("%w: cached keys are %s old: %v", ErrJWKSUnavailable, age, err)
		}
	}
	if !have || len(keys) == 0 {
		return nil, ErrJWKSUnavailable
	}

	claims, err := v.delegate(keys).Verify(ctx, rawJWT)
	if errors.Is(err, ErrUnknownKey) {
		// Possible key rotation: refetch once (throttled) and retry.
		if rerr := v.throttledRefresh(fetchCtx); rerr == nil {
			if rotated, _, ok := v.snapshot(); ok {
				claims, err = v.delegate(rotated).Verify(ctx, rawJWT)
			}
		}
	}
	if err != nil {
		return nil, err
	}
	// StaticKeyVerifier treats exp as optional (it also serves the hermetic dev
	// issuer). For an external IdP a missing exp is an eternal credential.
	if claims.ExpiresAt == 0 {
		return nil, ErrMissingExpiry
	}
	return claims, nil
}

// delegate builds the StaticKeyVerifier that performs the actual alg/signature/
// iss/exp/iat checks over a key snapshot. All verification logic lives there.
func (v *JWKSVerifier) delegate(keys map[string]crypto.PublicKey) *StaticKeyVerifier {
	return &StaticKeyVerifier{Keys: keys, Issuer: v.Issuer, Now: v.Now}
}

// Compile-time assertion that JWKSVerifier is a drop-in TokenVerifier.
var _ TokenVerifier = (*JWKSVerifier)(nil)
