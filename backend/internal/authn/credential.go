// credential.go implements the first-party PASSWORD verifier (WS-A1: real
// login). It is the crypto half of POST /auth/login and POST /auth/password; the
// persistence half lives in internal/authz (credential_store.go) and the HTTP
// half in internal/server (auth_login.go).
//
// STDLIB ONLY. Go 1.26 ships crypto/pbkdf2, so this adds NO module dependency
// (in particular NOT golang.org/x/crypto). Deliberate: the whole point of doing
// password login before SSO is that it is buildable and verifiable offline.
//
// !! INVARIANT — internal/authn MUST STAY FREE OF FIRST-PARTY IMPORTS !!
// `go list -deps ./internal/authn | grep khata` prints exactly one line (the
// package itself). internal/authz imports internal/authn, so the moment anything
// here imports internal/authz (e.g. to reuse authz.ErrNotFound) there is an
// import CYCLE. Keep this file on the standard library.
package authn

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"unicode/utf8"
)

const (
	// PBKDF2Algo is the single supported KDF identifier. It is also CHECK-pinned
	// on user_credential.algo (migration 0021), so an unknown algo can neither be
	// stored nor (see VerifyPassword) ever verify.
	PBKDF2Algo = "pbkdf2-sha256"

	// PBKDF2Iterations is the OWASP 2023 floor for PBKDF2-HMAC-SHA256 (210,000).
	// migration 0021 CHECKs iterations >= 100000 so a downgrade cannot be written
	// even by a future caller that forgets this constant.
	PBKDF2Iterations = 210000

	// saltLen / keyLen are the per-credential random salt and derived-key sizes.
	saltLen = 16
	keyLen  = 32

	// MinPasswordRunes is the minimum password length, counted in RUNES so a
	// non-ASCII password is not penalised by UTF-8 byte inflation.
	MinPasswordRunes = 10

	// MaxPasswordBytes bounds the KDF's input. PBKDF2-HMAC-SHA256 costs are
	// dominated by the iteration count, but HMAC re-keys per block on a key longer
	// than the block size, so an unbounded password is still a (small) amplifier
	// on an UNAUTHENTICATED endpoint. 256 bytes is far above any real passphrase.
	MaxPasswordBytes = 256
)

// ErrPasswordPolicy is the stable sentinel returned (wrapped, with a
// human-readable reason) by ValidatePasswordPolicy. Callers match it with
// errors.Is; the HTTP layer turns it into a 400 carrying the reason.
var ErrPasswordPolicy = errors.New("password does not meet the policy")

// Credential is a stored password verifier: the KDF parameters plus the salt and
// the derived key. It carries NO plaintext, ever.
type Credential struct {
	Algo       string
	Iterations int
	Salt       []byte
	Hash       []byte
}

// HashPassword derives a fresh Credential for password using a crypto/rand salt
// and the pinned PBKDF2 parameters. It enforces ValidatePasswordPolicy first, so
// a policy-violating password can never reach storage even if a caller forgets
// to validate.
func HashPassword(password string) (Credential, error) {
	if err := ValidatePasswordPolicy(password); err != nil {
		return Credential{}, err
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return Credential{}, fmt.Errorf("authn: read salt: %w", err)
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, PBKDF2Iterations, keyLen)
	if err != nil {
		return Credential{}, fmt.Errorf("authn: derive key: %w", err)
	}
	return Credential{
		Algo:       PBKDF2Algo,
		Iterations: PBKDF2Iterations,
		Salt:       salt,
		Hash:       key,
	}, nil
}

// VerifyPassword reports whether password matches c.
//
// It is TOTAL: an unknown algo, a zero-value Credential, a non-positive
// iteration count, an empty salt or an empty hash all return FALSE rather than
// panicking or erroring. That matters because the login handler calls it on
// whatever came back from the database, including the zero Credential returned
// when the subject has no credential row at all — a panic there would be an
// unauthenticated DoS, and an error path would be a user-enumeration oracle.
//
// The comparison is crypto/subtle.ConstantTimeCompare.
func VerifyPassword(c Credential, password string) bool {
	if c.Algo != PBKDF2Algo {
		return false
	}
	if c.Iterations <= 0 || len(c.Salt) == 0 || len(c.Hash) == 0 {
		return false
	}
	// Same DoS bound as the policy: never run the KDF over an unbounded input on
	// a pre-auth path.
	if len(password) > MaxPasswordBytes {
		return false
	}
	key, err := pbkdf2.Key(sha256.New, password, c.Salt, c.Iterations, len(c.Hash))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(key, c.Hash) == 1
}

// ValidatePasswordPolicy enforces the password policy: at least
// MinPasswordRunes runes and at most MaxPasswordBytes bytes. It returns an error
// wrapping ErrPasswordPolicy (so errors.Is identifies it) whose message is safe
// to show the user.
//
// The byte bound is checked FIRST: it is the DoS guard, and a 10-megabyte
// password should be rejected without counting its runes.
func ValidatePasswordPolicy(password string) error {
	if len(password) > MaxPasswordBytes {
		return fmt.Errorf("%w: password must be at most %d bytes", ErrPasswordPolicy, MaxPasswordBytes)
	}
	if utf8.RuneCountInString(password) < MinPasswordRunes {
		return fmt.Errorf("%w: password must be at least %d characters", ErrPasswordPolicy, MinPasswordRunes)
	}
	return nil
}
