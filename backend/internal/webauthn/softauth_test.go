package webauthn

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// ---- in-memory CredentialStore (hermetic; no Postgres) -------------------

type memStore struct {
	mu    sync.Mutex
	creds map[uuid.UUID]Credential // keyed by Credential.ID
}

func newMemStore() *memStore { return &memStore{creds: map[uuid.UUID]Credential{}} }

func (m *memStore) Save(_ context.Context, c Credential) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.creds[c.ID] = c
	return nil
}

func (m *memStore) ByUser(_ context.Context, userID uuid.UUID) ([]Credential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Credential
	for _, c := range m.creds {
		if c.UserID == userID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (m *memStore) ByCredentialID(_ context.Context, userID uuid.UUID, credentialID []byte) (Credential, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.creds {
		if c.UserID == userID && string(c.CredentialID) == string(credentialID) {
			return c, true, nil
		}
	}
	return Credential{}, false, nil
}

func (m *memStore) UpdateSignCount(_ context.Context, userID, credID uuid.UUID, newCount uint32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.creds[credID]
	if !ok || c.UserID != userID {
		return ErrUnknownCredential
	}
	c.SignCount = newCount
	m.creds[credID] = c
	return nil
}

// ---- software authenticator ----------------------------------------------

// softAuth is a hermetic software authenticator. It generates a key pair,
// produces COSE-encoded public keys, crafts clientDataJSON + authenticatorData,
// and signs assertions exactly as a real authenticator would, so the ceremony
// is verified end-to-end with no hardware.
type softAuth struct {
	rpID      string
	origin    string
	credID    []byte
	aaguid    []byte
	alg       int
	signCount uint32

	ecKey *ecdsa.PrivateKey
	edKey ed25519.PrivateKey
}

func newSoftAuthES256(t *testing.T, rpID, origin string) *softAuth {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen ec key: %v", err)
	}
	credID := make([]byte, 16)
	_, _ = rand.Read(credID)
	return &softAuth{rpID: rpID, origin: origin, credID: credID, aaguid: make([]byte, 16), alg: AlgES256, ecKey: k}
}

func newSoftAuthEdDSA(t *testing.T, rpID, origin string) *softAuth {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen ed key: %v", err)
	}
	credID := make([]byte, 16)
	_, _ = rand.Read(credID)
	return &softAuth{rpID: rpID, origin: origin, credID: credID, aaguid: make([]byte, 16), alg: AlgEdDSA, edKey: priv}
}

// coseKey returns the COSE_Key CBOR encoding of the public key.
func (s *softAuth) coseKey(t *testing.T) []byte {
	t.Helper()
	switch s.alg {
	case AlgES256:
		x := s.ecKey.PublicKey.X.FillBytes(make([]byte, 32))
		y := s.ecKey.PublicKey.Y.FillBytes(make([]byte, 32))
		// map{1:2(EC2), 3:-7(ES256), -1:1(P-256), -2:x, -3:y}
		var b []byte
		b = append(b, 0xa5) // map of 5
		b = append(b, cborInt(1)...)
		b = append(b, cborInt(2)...)
		b = append(b, cborInt(3)...)
		b = append(b, cborInt(AlgES256)...)
		b = append(b, cborInt(-1)...)
		b = append(b, cborInt(1)...) // crv P-256
		b = append(b, cborInt(-2)...)
		b = append(b, cborBytes(x)...)
		b = append(b, cborInt(-3)...)
		b = append(b, cborBytes(y)...)
		return b
	case AlgEdDSA:
		pub := s.edKey.Public().(ed25519.PublicKey)
		// map{1:1(OKP), 3:-8(EdDSA), -1:6(Ed25519), -2:x}
		var b []byte
		b = append(b, 0xa4) // map of 4
		b = append(b, cborInt(1)...)
		b = append(b, cborInt(1)...)
		b = append(b, cborInt(3)...)
		b = append(b, cborInt(AlgEdDSA)...)
		b = append(b, cborInt(-1)...)
		b = append(b, cborInt(6)...) // crv Ed25519
		b = append(b, cborInt(-2)...)
		b = append(b, cborBytes(pub)...)
		return b
	}
	t.Fatalf("unknown alg %d", s.alg)
	return nil
}

// TestCOSERejectsMismatchedCurve proves coseToSPKI rejects a COSE key whose crv
// (label -1) does not match its kty/alg, rather than silently treating it as
// P-256/Ed25519. Non-vacuous: the same key with the correct crv decodes fine.
func TestCOSERejectsMismatchedCurve(t *testing.T) {
	// EC2/ES256 key claiming crv=6 (Ed25519) instead of 1 (P-256).
	x := make([]byte, 32)
	y := make([]byte, 32)
	x[31], y[31] = 1, 1
	build := func(crv int64) []byte {
		var b []byte
		b = append(b, 0xa5)
		b = append(b, cborInt(1)...)
		b = append(b, cborInt(2)...) // kty EC2
		b = append(b, cborInt(3)...)
		b = append(b, cborInt(AlgES256)...)
		b = append(b, cborInt(-1)...)
		b = append(b, cborInt(crv)...) // crv under test
		b = append(b, cborInt(-2)...)
		b = append(b, cborBytes(x)...)
		b = append(b, cborInt(-3)...)
		b = append(b, cborBytes(y)...)
		return b
	}
	if _, _, err := coseToSPKI(build(6)); err == nil {
		t.Fatal("coseToSPKI accepted an EC2 key claiming crv=6 (Ed25519); want rejection")
	}
	// Control: crv=1 (P-256) reaches the on-curve check (these dummy x/y are not
	// on the curve, so x509 marshalling rejects them — but NOT with ErrUnsupportedAlg).
	if _, _, err := coseToSPKI(build(1)); err == ErrUnsupportedAlg {
		t.Fatal("crv=1 must pass the curve gate (failure should be downstream, not ErrUnsupportedAlg)")
	}
}

// authData builds the authenticator data. When attested is true it appends the
// attested credential data (aaguid + credId + COSE key) for registration.
func (s *softAuth) authData(t *testing.T, attested, userVerified bool) []byte {
	t.Helper()
	rpHash := sha256.Sum256([]byte(s.rpID))
	flags := byte(flagUserPresent)
	if userVerified {
		flags |= flagUserVerified
	}
	if attested {
		flags |= flagAttestedCred
	}
	var b []byte
	b = append(b, rpHash[:]...)
	b = append(b, flags)
	var sc [4]byte
	binary.BigEndian.PutUint32(sc[:], s.signCount)
	b = append(b, sc[:]...)
	if attested {
		b = append(b, s.aaguid...)
		var l [2]byte
		binary.BigEndian.PutUint16(l[:], uint16(len(s.credID)))
		b = append(b, l[:]...)
		b = append(b, s.credID...)
		b = append(b, s.coseKey(t)...)
	}
	return b
}

func (s *softAuth) clientData(t *testing.T, typ, challenge string) []byte {
	t.Helper()
	cd := clientData{Type: typ, Challenge: challenge, Origin: s.origin}
	raw, err := json.Marshal(cd)
	if err != nil {
		t.Fatalf("marshal clientData: %v", err)
	}
	return raw
}

// sign produces the assertion signature over authData || SHA-256(clientDataJSON).
func (s *softAuth) sign(t *testing.T, authData, clientDataJSON []byte) []byte {
	t.Helper()
	cdHash := sha256.Sum256(clientDataJSON)
	signed := append(append([]byte{}, authData...), cdHash[:]...)
	switch s.alg {
	case AlgES256:
		h := sha256.Sum256(signed)
		sig, err := ecdsa.SignASN1(rand.Reader, s.ecKey, h[:])
		if err != nil {
			t.Fatalf("ec sign: %v", err)
		}
		return sig
	case AlgEdDSA:
		return ed25519.Sign(s.edKey, signed)
	}
	t.Fatalf("unknown alg %d", s.alg)
	return nil
}

// register runs a full registration ceremony and returns the stored credential.
func (s *softAuth) register(t *testing.T, svc *Service, userID uuid.UUID) Credential {
	t.Helper()
	ch, err := svc.RegistrationBegin(context.Background(), userID)
	if err != nil {
		t.Fatalf("RegistrationBegin: %v", err)
	}
	in := RegistrationInput{
		CredentialID:      s.credID,
		ClientDataJSON:    s.clientData(t, "webauthn.create", ch.Challenge),
		AuthenticatorData: s.authData(t, true, true),
	}
	cred, err := svc.RegistrationFinish(context.Background(), ch, in)
	if err != nil {
		t.Fatalf("RegistrationFinish: %v", err)
	}
	return cred
}

// assertOnce runs one assertion ceremony at the authenticator's current count.
func (s *softAuth) assertOnce(svc *Service, userID uuid.UUID) (Credential, error) {
	ch, err := svc.AssertionBegin(context.Background(), userID)
	if err != nil {
		return Credential{}, err
	}
	authData := s.authDataNoT(false, true)
	clientDataJSON := s.clientDataNoT("webauthn.get", ch.Challenge)
	in := AssertionInput{
		CredentialID:      s.credID,
		ClientDataJSON:    clientDataJSON,
		AuthenticatorData: authData,
		Signature:         s.signNoT(authData, clientDataJSON),
	}
	return svc.AssertionFinish(context.Background(), ch, in)
}

// non-*testing.T variants for use inside assertOnce.
func (s *softAuth) authDataNoT(attested, uv bool) []byte {
	rpHash := sha256.Sum256([]byte(s.rpID))
	flags := byte(flagUserPresent)
	if uv {
		flags |= flagUserVerified
	}
	if attested {
		flags |= flagAttestedCred
	}
	b := append([]byte{}, rpHash[:]...)
	b = append(b, flags)
	var sc [4]byte
	binary.BigEndian.PutUint32(sc[:], s.signCount)
	return append(b, sc[:]...)
}

func (s *softAuth) clientDataNoT(typ, challenge string) []byte {
	raw, _ := json.Marshal(clientData{Type: typ, Challenge: challenge, Origin: s.origin})
	return raw
}

func (s *softAuth) signNoT(authData, clientDataJSON []byte) []byte {
	cdHash := sha256.Sum256(clientDataJSON)
	signed := append(append([]byte{}, authData...), cdHash[:]...)
	switch s.alg {
	case AlgES256:
		h := sha256.Sum256(signed)
		sig, _ := ecdsa.SignASN1(rand.Reader, s.ecKey, h[:])
		return sig
	default:
		return ed25519.Sign(s.edKey, signed)
	}
}

// ---- tests ---------------------------------------------------------------

func testService() *Service {
	return NewService(Config{RPID: "localhost", Origin: "http://localhost:5173"}, newMemStore())
}

func TestWebAuthnRegisterAndAssertES256(t *testing.T) {
	svc := testService()
	user := uuid.New()
	sa := newSoftAuthES256(t, "localhost", "http://localhost:5173")

	cred := sa.register(t, svc, user)
	if cred.Alg != AlgES256 {
		t.Fatalf("alg = %d, want %d", cred.Alg, AlgES256)
	}
	if len(cred.PublicKeySPKI) == 0 {
		t.Fatal("empty SPKI stored")
	}

	// First assertion: authenticator increments to 1.
	sa.signCount = 1
	if _, err := sa.assertOnce(svc, user); err != nil {
		t.Fatalf("first assertion: %v", err)
	}
	// Second assertion: increments to 2 (strictly increasing).
	sa.signCount = 2
	if _, err := sa.assertOnce(svc, user); err != nil {
		t.Fatalf("second assertion: %v", err)
	}
}

func TestWebAuthnRegisterAndAssertEdDSA(t *testing.T) {
	svc := testService()
	user := uuid.New()
	sa := newSoftAuthEdDSA(t, "localhost", "http://localhost:5173")

	cred := sa.register(t, svc, user)
	if cred.Alg != AlgEdDSA {
		t.Fatalf("alg = %d, want %d", cred.Alg, AlgEdDSA)
	}
	sa.signCount = 1
	if _, err := sa.assertOnce(svc, user); err != nil {
		t.Fatalf("eddsa assertion: %v", err)
	}
}

func TestWebAuthnAssertCloneDetection(t *testing.T) {
	svc := testService()
	user := uuid.New()
	sa := newSoftAuthES256(t, "localhost", "http://localhost:5173")
	sa.register(t, svc, user)

	// Advance to 5 and assert OK.
	sa.signCount = 5
	if _, err := sa.assertOnce(svc, user); err != nil {
		t.Fatalf("assertion at 5: %v", err)
	}
	// A cloned authenticator replays an OLD (<=) counter -> must be rejected.
	sa.signCount = 5
	if _, err := sa.assertOnce(svc, user); !errors.Is(err, ErrCloneDetected) {
		t.Fatalf("expected clone detection at equal count, got %v", err)
	}
	sa.signCount = 3
	if _, err := sa.assertOnce(svc, user); !errors.Is(err, ErrCloneDetected) {
		t.Fatalf("expected clone detection at lower count, got %v", err)
	}
}

func TestWebAuthnAssertSignCountStrictlyIncreases(t *testing.T) {
	svc := testService()
	user := uuid.New()
	sa := newSoftAuthES256(t, "localhost", "http://localhost:5173")
	sa.register(t, svc, user)

	for i := uint32(1); i <= 4; i++ {
		sa.signCount = i
		if _, err := sa.assertOnce(svc, user); err != nil {
			t.Fatalf("assertion at count %d: %v", i, err)
		}
	}
	// Re-using the last accepted count must fail.
	sa.signCount = 4
	if _, err := sa.assertOnce(svc, user); !errors.Is(err, ErrCloneDetected) {
		t.Fatalf("expected clone detection on stale count, got %v", err)
	}
}

func TestWebAuthnAssertChallengeMismatch(t *testing.T) {
	svc := testService()
	user := uuid.New()
	sa := newSoftAuthES256(t, "localhost", "http://localhost:5173")
	sa.register(t, svc, user)
	sa.signCount = 1

	ch, _ := svc.AssertionBegin(context.Background(), user)
	authData := sa.authDataNoT(false, true)
	// Sign over a DIFFERENT challenge's clientData.
	bad := sa.clientDataNoT("webauthn.get", "tampered-challenge-value")
	in := AssertionInput{
		CredentialID:      sa.credID,
		ClientDataJSON:    bad,
		AuthenticatorData: authData,
		Signature:         sa.signNoT(authData, bad),
	}
	if _, err := svc.AssertionFinish(context.Background(), ch, in); !errors.Is(err, ErrChallengeMismatch) {
		t.Fatalf("expected challenge mismatch, got %v", err)
	}
}

func TestWebAuthnAssertOriginMismatch(t *testing.T) {
	svc := testService()
	user := uuid.New()
	sa := newSoftAuthES256(t, "localhost", "http://localhost:5173")
	sa.register(t, svc, user)
	// Authenticator now forges a different origin on assertion.
	sa.origin = "https://evil.example"
	sa.signCount = 1
	if _, err := sa.assertOnce(svc, user); !errors.Is(err, ErrOriginMismatch) {
		t.Fatalf("expected origin mismatch, got %v", err)
	}
}

func TestWebAuthnAssertRPIDMismatch(t *testing.T) {
	svc := testService()
	user := uuid.New()
	// Register with the correct RP id so the credential stores fine.
	sa := newSoftAuthES256(t, "localhost", "http://localhost:5173")
	sa.register(t, svc, user)
	// Now the authenticator forges a different rpIdHash on assertion.
	sa.rpID = "evil.example"
	sa.signCount = 1
	if _, err := sa.assertOnce(svc, user); !errors.Is(err, ErrRPIDMismatch) {
		t.Fatalf("expected rpId mismatch, got %v", err)
	}
}

func TestWebAuthnAssertTamperedSignatureRejected(t *testing.T) {
	svc := testService()
	user := uuid.New()
	sa := newSoftAuthES256(t, "localhost", "http://localhost:5173")
	sa.register(t, svc, user)
	sa.signCount = 1

	ch, _ := svc.AssertionBegin(context.Background(), user)
	authData := sa.authDataNoT(false, true)
	cdJSON := sa.clientDataNoT("webauthn.get", ch.Challenge)
	sig := sa.signNoT(authData, cdJSON)
	sig[len(sig)-1] ^= 0xff // tamper
	in := AssertionInput{
		CredentialID:      sa.credID,
		ClientDataJSON:    cdJSON,
		AuthenticatorData: authData,
		Signature:         sig,
	}
	if _, err := svc.AssertionFinish(context.Background(), ch, in); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("expected bad signature, got %v", err)
	}
}

func TestWebAuthnAssertUnknownCredential(t *testing.T) {
	svc := testService()
	user := uuid.New()
	sa := newSoftAuthES256(t, "localhost", "http://localhost:5173")
	// Do NOT register; assert should fail to find the credential.
	sa.signCount = 1
	if _, err := sa.assertOnce(svc, user); !errors.Is(err, ErrUnknownCredential) {
		t.Fatalf("expected unknown credential, got %v", err)
	}
}

func TestWebAuthnRegisterTypeMismatch(t *testing.T) {
	svc := testService()
	user := uuid.New()
	sa := newSoftAuthES256(t, "localhost", "http://localhost:5173")
	ch, _ := svc.RegistrationBegin(context.Background(), user)
	in := RegistrationInput{
		CredentialID:      sa.credID,
		ClientDataJSON:    sa.clientData(t, "webauthn.get", ch.Challenge), // wrong type for create
		AuthenticatorData: sa.authData(t, true, true),
	}
	if _, err := svc.RegistrationFinish(context.Background(), ch, in); !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("expected type mismatch, got %v", err)
	}
}

// cborInt encodes a (possibly negative) integer per CBOR rules, matching what
// the by-hand decoder in webauthn.go expects.
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
