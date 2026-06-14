// Package webauthn implements a WebAuthn Level 2 "none" attestation
// registration + assertion ceremony using ONLY the Go standard library. No
// external WebAuthn/CBOR/JOSE dependency is used: the COSE public key is decoded
// by hand, signatures are verified with crypto/ecdsa (ES256, P-256) and
// crypto/ed25519 (EdDSA), and all hashing/encoding uses crypto/sha256,
// crypto/rand, crypto/subtle and encoding/base64 RawURLEncoding.
//
// The package is deliberately transport-agnostic: it exposes a Service that the
// server wires behind its own HTTP routes. It owns the webauthn_credential
// table via the CredentialStore interface (see store.go).
//
// Clone detection: the authenticator data carries a monotonically increasing
// signCount. On every successful assertion the stored sign count MUST strictly
// increase; a non-increasing counter signals a cloned authenticator and the
// assertion is rejected.
package webauthn

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"

	"github.com/google/uuid"
)

// COSE algorithm identifiers (RFC 9053 / IANA COSE Algorithms).
const (
	AlgES256 = -7 // ECDSA w/ SHA-256 over P-256.
	AlgEdDSA = -8 // EdDSA (Ed25519).
)

// authenticatorData flag bits (WebAuthn §6.1).
const (
	flagUserPresent  = 0x01 // UP
	flagUserVerified = 0x04 // UV
	flagAttestedCred = 0x40 // AT (attested credential data included)
)

// b64 is the base64url encoding WITHOUT padding used throughout WebAuthn.
var b64 = base64.RawURLEncoding

// Errors surfaced by the ceremony. They are intentionally coarse so callers do
// not leak which exact check failed to a remote attacker.
var (
	ErrChallengeMismatch = errors.New("webauthn: challenge mismatch")
	ErrTypeMismatch      = errors.New("webauthn: clientData type mismatch")
	ErrOriginMismatch    = errors.New("webauthn: origin mismatch")
	ErrRPIDMismatch      = errors.New("webauthn: rpIdHash mismatch")
	ErrUserNotPresent    = errors.New("webauthn: user-present flag not set")
	ErrBadSignature      = errors.New("webauthn: signature verification failed")
	ErrCloneDetected     = errors.New("webauthn: sign count did not increase (clone detected)")
	ErrUnknownCredential = errors.New("webauthn: unknown credential")
	ErrUnsupportedAlg    = errors.New("webauthn: unsupported COSE algorithm")
	ErrMalformedAuthData = errors.New("webauthn: malformed authenticator data")
	ErrMalformedCOSE     = errors.New("webauthn: malformed COSE key")
)

// Config binds the ceremony to a Relying Party.
type Config struct {
	RPID   string // e.g. "localhost" — its SHA-256 must equal authData.rpIdHash.
	Origin string // e.g. "http://localhost:5173" — exact-match against clientData.origin.
	// RequireUserVerification, when true, requires the UV flag during assertion.
	RequireUserVerification bool
}

// Credential is a stored WebAuthn credential for a user.
type Credential struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	CredentialID []byte // raw credential id (authenticator handle).
	PublicKeySPKI []byte // SPKI DER, decoded back into a crypto.PublicKey on use.
	Alg          int    // COSE alg id (AlgES256 / AlgEdDSA).
	SignCount    uint32
	AAGUID       []byte
}

// CredentialStore persists webauthn_credential rows. WS2 owns its pg
// implementation in store.go. RLS scopes every row to the user GUC.
type CredentialStore interface {
	Save(ctx context.Context, c Credential) error
	ByUser(ctx context.Context, userID uuid.UUID) ([]Credential, error)
	ByCredentialID(ctx context.Context, userID uuid.UUID, credentialID []byte) (Credential, bool, error)
	// UpdateSignCount persists newCount for the credential identified by
	// (userID, credID). userID is required because the webauthn_credential RLS
	// policy scopes rows to app_current_user_id(); the Service holds the
	// asserted Credential (and thus its UserID) at the call site.
	UpdateSignCount(ctx context.Context, userID, credID uuid.UUID, newCount uint32) error
}

// Challenge represents a server-issued ceremony challenge bound to a user. The
// caller is responsible for stashing it (session/cache) between begin and
// finish; the package only generates and compares it.
type Challenge struct {
	UserID    uuid.UUID
	Challenge string // base64url, no padding.
}

// Service drives both ceremonies against a CredentialStore.
type Service struct {
	cfg   Config
	store CredentialStore
}

// NewService constructs a Service.
func NewService(cfg Config, store CredentialStore) *Service {
	return &Service{cfg: cfg, store: store}
}

// newChallenge mints a cryptographically random 32-byte challenge.
func newChallenge() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("webauthn: rand: %w", err)
	}
	return b64.EncodeToString(buf), nil
}

// --- Registration ---------------------------------------------------------

// RegistrationBegin issues a challenge for a new credential registration.
func (s *Service) RegistrationBegin(_ context.Context, userID uuid.UUID) (Challenge, error) {
	c, err := newChallenge()
	if err != nil {
		return Challenge{}, err
	}
	return Challenge{UserID: userID, Challenge: c}, nil
}

// RegistrationInput is the attestation result from the authenticator (the
// browser navigator.credentials.create() PublicKeyCredential, "none" attestation).
type RegistrationInput struct {
	CredentialID       []byte // raw credential id.
	ClientDataJSON     []byte
	AuthenticatorData  []byte // attested-cred-data bearing authData (NOT the attestation object).
}

// RegistrationFinish verifies the registration ceremony and persists the
// credential. It returns the stored credential.
func (s *Service) RegistrationFinish(ctx context.Context, ch Challenge, in RegistrationInput) (Credential, error) {
	if err := s.verifyClientData(in.ClientDataJSON, "webauthn.create", ch.Challenge); err != nil {
		return Credential{}, err
	}
	ad, err := parseAuthenticatorData(in.AuthenticatorData)
	if err != nil {
		return Credential{}, err
	}
	if err := s.verifyRPAndPresence(ad); err != nil {
		return Credential{}, err
	}
	if !ad.hasAttestedCred {
		return Credential{}, ErrMalformedAuthData
	}
	alg, spki, err := coseToSPKI(ad.cosePublicKey)
	if err != nil {
		return Credential{}, err
	}
	credID := in.CredentialID
	if len(credID) == 0 {
		credID = ad.credentialID
	}
	cred := Credential{
		ID:            uuid.New(),
		UserID:        ch.UserID,
		CredentialID:  credID,
		PublicKeySPKI: spki,
		Alg:           alg,
		SignCount:     ad.signCount,
		AAGUID:        ad.aaguid,
	}
	if err := s.store.Save(ctx, cred); err != nil {
		return Credential{}, err
	}
	return cred, nil
}

// --- Assertion ------------------------------------------------------------

// AssertionBegin issues a challenge for a step-up assertion.
func (s *Service) AssertionBegin(_ context.Context, userID uuid.UUID) (Challenge, error) {
	c, err := newChallenge()
	if err != nil {
		return Challenge{}, err
	}
	return Challenge{UserID: userID, Challenge: c}, nil
}

// AssertionInput is the result of navigator.credentials.get().
type AssertionInput struct {
	CredentialID      []byte
	ClientDataJSON    []byte
	AuthenticatorData []byte
	Signature         []byte
}

// AssertionFinish verifies the assertion against the stored credential and, on
// success, enforces strictly-increasing signCount and persists the new count.
// It returns the credential that was asserted.
func (s *Service) AssertionFinish(ctx context.Context, ch Challenge, in AssertionInput) (Credential, error) {
	if err := s.verifyClientData(in.ClientDataJSON, "webauthn.get", ch.Challenge); err != nil {
		return Credential{}, err
	}
	cred, found, err := s.store.ByCredentialID(ctx, ch.UserID, in.CredentialID)
	if err != nil {
		return Credential{}, err
	}
	if !found {
		return Credential{}, ErrUnknownCredential
	}
	ad, err := parseAuthenticatorData(in.AuthenticatorData)
	if err != nil {
		return Credential{}, err
	}
	if err := s.verifyRPAndPresence(ad); err != nil {
		return Credential{}, err
	}

	// Signature is over authenticatorData || SHA-256(clientDataJSON).
	cdHash := sha256.Sum256(in.ClientDataJSON)
	signed := make([]byte, 0, len(in.AuthenticatorData)+len(cdHash))
	signed = append(signed, in.AuthenticatorData...)
	signed = append(signed, cdHash[:]...)

	pub, err := x509.ParsePKIXPublicKey(cred.PublicKeySPKI)
	if err != nil {
		return Credential{}, fmt.Errorf("webauthn: parse stored key: %w", err)
	}
	if err := verifySignature(cred.Alg, pub, signed, in.Signature); err != nil {
		return Credential{}, err
	}

	// Clone detection: when either the stored or the incoming counter is
	// non-zero, the incoming counter MUST be strictly greater. (Some
	// authenticators legitimately keep the counter at 0 forever; in that case
	// both are 0 and we accept.)
	if !(cred.SignCount == 0 && ad.signCount == 0) {
		if ad.signCount <= cred.SignCount {
			return Credential{}, ErrCloneDetected
		}
	}
	if err := s.store.UpdateSignCount(ctx, cred.UserID, cred.ID, ad.signCount); err != nil {
		return Credential{}, err
	}
	cred.SignCount = ad.signCount
	return cred, nil
}

// --- clientDataJSON & authenticatorData -----------------------------------

type clientData struct {
	Type      string `json:"type"`
	Challenge string `json:"challenge"`
	Origin    string `json:"origin"`
}

// verifyClientData parses clientDataJSON and checks type, challenge (constant
// time) and origin (constant time).
func (s *Service) verifyClientData(raw []byte, wantType, wantChallenge string) error {
	var cd clientData
	if err := json.Unmarshal(raw, &cd); err != nil {
		return fmt.Errorf("webauthn: clientData json: %w", err)
	}
	if cd.Type != wantType {
		return ErrTypeMismatch
	}
	if subtle.ConstantTimeCompare([]byte(cd.Challenge), []byte(wantChallenge)) != 1 {
		return ErrChallengeMismatch
	}
	if subtle.ConstantTimeCompare([]byte(cd.Origin), []byte(s.cfg.Origin)) != 1 {
		return ErrOriginMismatch
	}
	return nil
}

type authData struct {
	rpIDHash        []byte
	flags           byte
	signCount       uint32
	hasAttestedCred bool
	aaguid          []byte
	credentialID    []byte
	cosePublicKey   []byte // remaining COSE_Key bytes (registration only).
}

// parseAuthenticatorData decodes the fixed + optional attested-credential parts
// of the WebAuthn authenticator data structure (WebAuthn §6.1).
func parseAuthenticatorData(b []byte) (authData, error) {
	// 32 (rpIdHash) + 1 (flags) + 4 (signCount) = 37 minimum.
	if len(b) < 37 {
		return authData{}, ErrMalformedAuthData
	}
	var ad authData
	ad.rpIDHash = b[:32]
	ad.flags = b[32]
	ad.signCount = binary.BigEndian.Uint32(b[33:37])
	ad.hasAttestedCred = ad.flags&flagAttestedCred != 0

	if ad.hasAttestedCred {
		rest := b[37:]
		// aaguid(16) + credIdLen(2) + credId(L) + COSE key.
		if len(rest) < 18 {
			return authData{}, ErrMalformedAuthData
		}
		ad.aaguid = rest[:16]
		credIDLen := int(binary.BigEndian.Uint16(rest[16:18]))
		rest = rest[18:]
		if len(rest) < credIDLen {
			return authData{}, ErrMalformedAuthData
		}
		ad.credentialID = rest[:credIDLen]
		ad.cosePublicKey = rest[credIDLen:]
	}
	return ad, nil
}

// verifyRPAndPresence checks rpIdHash and the user-present flag (and UV when
// required).
func (s *Service) verifyRPAndPresence(ad authData) error {
	rpHash := sha256.Sum256([]byte(s.cfg.RPID))
	if subtle.ConstantTimeCompare(ad.rpIDHash, rpHash[:]) != 1 {
		return ErrRPIDMismatch
	}
	if ad.flags&flagUserPresent == 0 {
		return ErrUserNotPresent
	}
	if s.cfg.RequireUserVerification && ad.flags&flagUserVerified == 0 {
		return ErrUserNotPresent
	}
	return nil
}

// --- COSE key decoding (by hand) ------------------------------------------

// coseToSPKI decodes a COSE_Key (a CBOR map) for ES256 or EdDSA into an SPKI
// (PKIX) DER encoding plus the COSE alg id. Only the small subset of CBOR used
// by WebAuthn COSE keys is implemented.
func coseToSPKI(cose []byte) (int, []byte, error) {
	m, err := decodeCOSEMap(cose)
	if err != nil {
		return 0, nil, err
	}
	kty, ok := m[1] // 1 = kty
	if !ok {
		return 0, nil, ErrMalformedCOSE
	}
	alg, ok := m[3] // 3 = alg
	if !ok {
		return 0, nil, ErrMalformedCOSE
	}
	switch ktyInt(kty) {
	case 2: // EC2
		if algInt(alg) != AlgES256 {
			return 0, nil, ErrUnsupportedAlg
		}
		// crv (COSE label -1) MUST be P-256 (1) for ES256. Reject a key that
		// claims a different curve rather than silently treating it as P-256.
		if crv, ok := m[-1]; !ok || algInt(crv) != 1 {
			return 0, nil, ErrUnsupportedAlg
		}
		x, okx := m[-2].([]byte)
		y, oky := m[-3].([]byte)
		if !okx || !oky || len(x) != 32 || len(y) != 32 {
			return 0, nil, ErrMalformedCOSE
		}
		pub := &ecdsa.PublicKey{
			Curve: elliptic.P256(),
			X:     new(big.Int).SetBytes(x),
			Y:     new(big.Int).SetBytes(y),
		}
		spki, err := x509.MarshalPKIXPublicKey(pub)
		if err != nil {
			return 0, nil, fmt.Errorf("webauthn: marshal ec key: %w", err)
		}
		return AlgES256, spki, nil
	case 1: // OKP (Ed25519)
		if algInt(alg) != AlgEdDSA {
			return 0, nil, ErrUnsupportedAlg
		}
		// crv (COSE label -1) MUST be Ed25519 (6) for EdDSA/OKP.
		if crv, ok := m[-1]; !ok || algInt(crv) != 6 {
			return 0, nil, ErrUnsupportedAlg
		}
		x, okx := m[-2].([]byte) // -2 = x (the 32-byte public key)
		if !okx || len(x) != ed25519.PublicKeySize {
			return 0, nil, ErrMalformedCOSE
		}
		pub := ed25519.PublicKey(x)
		spki, err := x509.MarshalPKIXPublicKey(pub)
		if err != nil {
			return 0, nil, fmt.Errorf("webauthn: marshal ed25519 key: %w", err)
		}
		return AlgEdDSA, spki, nil
	default:
		return 0, nil, ErrUnsupportedAlg
	}
}

// decodeCOSEMap decodes the minimal CBOR needed for a COSE_Key: a single
// top-level map whose keys are (negative or unsigned) integers and whose values
// are integers or byte strings. This is implemented by hand to avoid a CBOR
// dependency (the box is offline). Only the value kinds used by ES256/EdDSA
// COSE keys are supported; anything else is rejected as malformed.
func decodeCOSEMap(b []byte) (map[int64]any, error) {
	d := &cborDecoder{buf: b}
	major, n, err := d.readHead()
	if err != nil {
		return nil, err
	}
	if major != 5 { // 5 = map
		return nil, ErrMalformedCOSE
	}
	out := make(map[int64]any, n)
	for i := uint64(0); i < n; i++ {
		key, err := d.readInt()
		if err != nil {
			return nil, err
		}
		val, err := d.readValue()
		if err != nil {
			return nil, err
		}
		out[key] = val
	}
	return out, nil
}

type cborDecoder struct {
	buf []byte
	pos int
}

// readHead reads a CBOR initial byte and returns the major type (top 3 bits)
// and the decoded argument n (length / value).
func (d *cborDecoder) readHead() (major byte, n uint64, err error) {
	if d.pos >= len(d.buf) {
		return 0, 0, ErrMalformedCOSE
	}
	ib := d.buf[d.pos]
	d.pos++
	major = ib >> 5
	ai := ib & 0x1f
	switch {
	case ai < 24:
		n = uint64(ai)
	case ai == 24:
		if d.pos+1 > len(d.buf) {
			return 0, 0, ErrMalformedCOSE
		}
		n = uint64(d.buf[d.pos])
		d.pos++
	case ai == 25:
		if d.pos+2 > len(d.buf) {
			return 0, 0, ErrMalformedCOSE
		}
		n = uint64(d.buf[d.pos])<<8 | uint64(d.buf[d.pos+1])
		d.pos += 2
	case ai == 26:
		if d.pos+4 > len(d.buf) {
			return 0, 0, ErrMalformedCOSE
		}
		for i := 0; i < 4; i++ {
			n = n<<8 | uint64(d.buf[d.pos+i])
		}
		d.pos += 4
	default:
		return 0, 0, ErrMalformedCOSE
	}
	return major, n, nil
}

// readInt reads a CBOR unsigned (major 0) or negative (major 1) integer.
func (d *cborDecoder) readInt() (int64, error) {
	major, n, err := d.readHead()
	if err != nil {
		return 0, err
	}
	switch major {
	case 0: // unsigned
		return int64(n), nil
	case 1: // negative: value is -1-n
		return -1 - int64(n), nil
	default:
		return 0, ErrMalformedCOSE
	}
}

// readValue reads a COSE map value: integer or byte string.
func (d *cborDecoder) readValue() (any, error) {
	if d.pos >= len(d.buf) {
		return nil, ErrMalformedCOSE
	}
	major := d.buf[d.pos] >> 5
	switch major {
	case 0, 1:
		return d.readInt()
	case 2: // byte string
		_, n, err := d.readHead()
		if err != nil {
			return nil, err
		}
		if d.pos+int(n) > len(d.buf) {
			return nil, ErrMalformedCOSE
		}
		bs := make([]byte, n)
		copy(bs, d.buf[d.pos:d.pos+int(n)])
		d.pos += int(n)
		return bs, nil
	default:
		return nil, ErrMalformedCOSE
	}
}

func ktyInt(v any) int64 { return algInt(v) }

func algInt(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int:
		return int64(t)
	default:
		return 0
	}
}

// --- signature verification -----------------------------------------------

func verifySignature(alg int, pub crypto.PublicKey, signed, sig []byte) error {
	switch alg {
	case AlgES256:
		ecpub, ok := pub.(*ecdsa.PublicKey)
		if !ok {
			return ErrUnsupportedAlg
		}
		h := sha256.Sum256(signed)
		// WebAuthn ES256 signatures are ASN.1 DER (r,s) sequences.
		if !ecdsa.VerifyASN1(ecpub, h[:], sig) {
			return ErrBadSignature
		}
		return nil
	case AlgEdDSA:
		edpub, ok := pub.(ed25519.PublicKey)
		if !ok {
			return ErrUnsupportedAlg
		}
		if !ed25519.Verify(edpub, signed, sig) {
			return ErrBadSignature
		}
		return nil
	default:
		return ErrUnsupportedAlg
	}
}
