package authn_test

import (
	"bytes"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/khata/cored/internal/authn"
)

const goodPassword = "tally-demo-2026"

// TestHashPasswordRoundTrip is the primary success oracle: a freshly hashed
// password verifies, and the WRONG password does not.
//
// REGRESSION SENSITIVITY: if HashPassword and VerifyPassword ever disagree on a
// parameter (salt, iteration count, key length, hash function) the first half
// fails. If VerifyPassword were short-circuited to `return true` the second half
// fails.
func TestHashPasswordRoundTrip(t *testing.T) {
	c, err := authn.HashPassword(goodPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !authn.VerifyPassword(c, goodPassword) {
		t.Fatal("the correct password must verify against its own credential")
	}
	if authn.VerifyPassword(c, goodPassword+"x") {
		t.Fatal("a wrong password must NOT verify")
	}
	if authn.VerifyPassword(c, "") {
		t.Fatal("an empty password must NOT verify")
	}
}

// TestHashPasswordParameters pins the stored KDF parameters. A silent downgrade
// (fewer iterations, a shorter salt/key, a different algo string) is exactly the
// kind of change that leaves every test green while destroying the security
// property, so it is asserted structurally.
//
// REGRESSION SENSITIVITY: dropping PBKDF2Iterations below 100000 also violates
// migration 0021's CHECK constraint, so this test and the database agree.
func TestHashPasswordParameters(t *testing.T) {
	c, err := authn.HashPassword(goodPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if c.Algo != "pbkdf2-sha256" {
		t.Fatalf("algo = %q, want pbkdf2-sha256 (migration 0021 CHECKs this literal)", c.Algo)
	}
	if c.Iterations != authn.PBKDF2Iterations {
		t.Fatalf("iterations = %d, want %d", c.Iterations, authn.PBKDF2Iterations)
	}
	if authn.PBKDF2Iterations < 100000 {
		t.Fatalf("PBKDF2Iterations = %d is below the OWASP floor AND below migration 0021's CHECK (>= 100000)", authn.PBKDF2Iterations)
	}
	if len(c.Salt) != 16 {
		t.Fatalf("salt length = %d, want 16", len(c.Salt))
	}
	if len(c.Hash) != 32 {
		t.Fatalf("hash length = %d, want 32", len(c.Hash))
	}
}

// TestHashPasswordSaltIsRandom proves the salt is per-credential, not global: two
// hashes of the SAME password must differ in both salt and hash.
//
// REGRESSION SENSITIVITY: replacing crypto/rand with a constant salt (a classic
// "make the tests deterministic" mistake) makes both halves fail, and would make
// the stored hashes a rainbow-table target.
func TestHashPasswordSaltIsRandom(t *testing.T) {
	a, err := authn.HashPassword(goodPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	b, err := authn.HashPassword(goodPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if bytes.Equal(a.Salt, b.Salt) {
		t.Fatal("two credentials for the same password share a salt: the salt is not random")
	}
	if bytes.Equal(a.Hash, b.Hash) {
		t.Fatal("two credentials for the same password share a hash: the salt is not mixed in")
	}
	// Cross-verification must still work: each hash is bound to its own salt.
	if !authn.VerifyPassword(a, goodPassword) || !authn.VerifyPassword(b, goodPassword) {
		t.Fatal("both independently salted credentials must verify the same password")
	}
	// ... and the hash of one must not verify under the salt of the other.
	mixed := authn.Credential{Algo: a.Algo, Iterations: a.Iterations, Salt: a.Salt, Hash: b.Hash}
	if authn.VerifyPassword(mixed, goodPassword) {
		t.Fatal("a hash paired with a foreign salt must not verify")
	}
}

// TestVerifyPasswordIsTotal is the panic/enumeration guard. VerifyPassword is
// called on whatever the database returned, including the ZERO Credential the
// login handler gets for a subject with no credential row. It must return false,
// never panic.
//
// REGRESSION SENSITIVITY: removing the algo guard makes the "unknown algo" case
// verify or panic (pbkdf2 over a nil salt); removing the zero-value guards makes
// the zero Credential case panic inside ConstantTimeCompare/pbkdf2, which on the
// pre-auth /auth/login path is an unauthenticated crash.
func TestVerifyPasswordIsTotal(t *testing.T) {
	valid, err := authn.HashPassword(goodPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	cases := []struct {
		name string
		c    authn.Credential
		pw   string
	}{
		{"zero value credential", authn.Credential{}, goodPassword},
		{"zero value credential, empty password", authn.Credential{}, ""},
		{"unknown algo", authn.Credential{Algo: "bcrypt", Iterations: 210000, Salt: valid.Salt, Hash: valid.Hash}, goodPassword},
		{"empty algo but real material", authn.Credential{Iterations: 210000, Salt: valid.Salt, Hash: valid.Hash}, goodPassword},
		{"zero iterations", authn.Credential{Algo: "pbkdf2-sha256", Iterations: 0, Salt: valid.Salt, Hash: valid.Hash}, goodPassword},
		{"negative iterations", authn.Credential{Algo: "pbkdf2-sha256", Iterations: -1, Salt: valid.Salt, Hash: valid.Hash}, goodPassword},
		{"nil salt", authn.Credential{Algo: "pbkdf2-sha256", Iterations: 210000, Salt: nil, Hash: valid.Hash}, goodPassword},
		{"nil hash", authn.Credential{Algo: "pbkdf2-sha256", Iterations: 210000, Salt: valid.Salt, Hash: nil}, goodPassword},
		{"oversized password", valid, strings.Repeat("a", authn.MaxPasswordBytes+1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("VerifyPassword panicked (%v) — on the pre-auth login path that is an unauthenticated crash", r)
				}
			}()
			if authn.VerifyPassword(tc.c, tc.pw) {
				t.Fatal("VerifyPassword must return false here")
			}
		})
	}
}

// TestValidatePasswordPolicy pins both bounds and the sentinel.
//
// REGRESSION SENSITIVITY: relaxing the minimum to <10 makes the 9-rune case
// pass; dropping the byte cap makes the oversized case pass (and removes the KDF
// DoS bound on an unauthenticated endpoint); returning a bare fmt.Errorf instead
// of wrapping ErrPasswordPolicy makes the errors.Is assertion fail, which is what
// the HTTP layer uses to choose 400 over 500.
func TestValidatePasswordPolicy(t *testing.T) {
	t.Run("accepts a compliant password", func(t *testing.T) {
		if err := authn.ValidatePasswordPolicy(goodPassword); err != nil {
			t.Fatalf("ValidatePasswordPolicy(%q) = %v, want nil", goodPassword, err)
		}
		// Exactly at the minimum.
		if err := authn.ValidatePasswordPolicy("0123456789"); err != nil {
			t.Fatalf("a 10-rune password must be accepted, got %v", err)
		}
	})

	t.Run("rejects short", func(t *testing.T) {
		err := authn.ValidatePasswordPolicy("012345678") // 9 runes
		if err == nil {
			t.Fatal("a 9-rune password must be rejected")
		}
		if !errors.Is(err, authn.ErrPasswordPolicy) {
			t.Fatalf("error %v does not wrap ErrPasswordPolicy", err)
		}
	})

	t.Run("counts RUNES not bytes", func(t *testing.T) {
		// 10 Devanagari runes = 30 UTF-8 bytes. Counting bytes would accept it too,
		// but counting bytes for the MINIMUM would wrongly accept a 4-rune emoji
		// password; this pins the rune semantics from the accepting side.
		pw := strings.Repeat("क", 10)
		if err := authn.ValidatePasswordPolicy(pw); err != nil {
			t.Fatalf("10 multi-byte runes must be accepted, got %v", err)
		}
		short := strings.Repeat("क", 9) // 27 bytes, only 9 runes
		if err := authn.ValidatePasswordPolicy(short); err == nil {
			t.Fatal("9 multi-byte runes (27 bytes) must be REJECTED: the minimum is counted in runes, not bytes")
		}
	})

	t.Run("rejects oversized", func(t *testing.T) {
		err := authn.ValidatePasswordPolicy(strings.Repeat("a", authn.MaxPasswordBytes+1))
		if err == nil {
			t.Fatalf("a %d-byte password must be rejected (KDF DoS bound)", authn.MaxPasswordBytes+1)
		}
		if !errors.Is(err, authn.ErrPasswordPolicy) {
			t.Fatalf("error %v does not wrap ErrPasswordPolicy", err)
		}
		// Exactly at the cap is fine.
		if err := authn.ValidatePasswordPolicy(strings.Repeat("a", authn.MaxPasswordBytes)); err != nil {
			t.Fatalf("a password of exactly %d bytes must be accepted, got %v", authn.MaxPasswordBytes, err)
		}
	})
}

// TestHashPasswordEnforcesPolicy proves the policy is enforced at the STORAGE
// boundary too, not only by the HTTP handler.
//
// REGRESSION SENSITIVITY: deleting the ValidatePasswordPolicy call from
// HashPassword makes this fail, and would let any future caller (a seeder, an
// admin tool) persist a 1-character password.
func TestHashPasswordEnforcesPolicy(t *testing.T) {
	if _, err := authn.HashPassword("short"); !errors.Is(err, authn.ErrPasswordPolicy) {
		t.Fatalf("HashPassword(short) error = %v, want ErrPasswordPolicy", err)
	}
	if _, err := authn.HashPassword(strings.Repeat("a", authn.MaxPasswordBytes+1)); !errors.Is(err, authn.ErrPasswordPolicy) {
		t.Fatalf("HashPassword(oversized) error = %v, want ErrPasswordPolicy", err)
	}
}

// --------------------------------------------------------------------------
// Known-answer tests (KATs)
//
// WHY THESE EXIST. An adversarial review of the first-party-login increment
// mutation-tested this package and found TWO mutations that the rest of the
// suite could not detect:
//
//	(1) change BOTH pbkdf2.Key calls (HashPassword and VerifyPassword) from the
//	    pinned iteration count to the literal 1 — the whole package still passed;
//	(2) change the PBKDF2Iterations constant from 210000 to 100000 (a silent
//	    downgrade to exactly migration 0021's CHECK floor) — still passed.
//
// Both shipped a catastrophically weakened KDF behind a green suite. The reason
// nothing bit is that every pre-existing assertion is expressed RELATIVE to the
// constant (`c.Iterations == authn.PBKDF2Iterations` is a tautology) or to the
// package's own round trip (self-consistent at any iteration count). Killing
// these mutations needs an oracle that is INDEPENDENT of this package.
// --------------------------------------------------------------------------

// katSalt/katVector210k are an INDEPENDENT PBKDF2-HMAC-SHA256 test vector,
// generated with Python's hashlib (a different implementation, on a different
// runtime) as:
//
//	hashlib.pbkdf2_hmac("sha256", b"tally-demo-2026", b"tally-kat-salt16", 210000, 32).hex()
//
// The 1-iteration and 100000-iteration digests of the SAME inputs are recorded
// beside it so the two known mutations are named explicitly and can never come
// back silently.
const (
	katSalt       = "tally-kat-salt16" // 16 bytes, exactly saltLen
	katVector210k = "c961f81b656185605ac196eb4a1071db6de52e189722beb23d959a2b50d20b2b"
	katVector100k = "1291406bb0f6dc0e1a7082770b7b8d110d8e8229b4fe449a2ed1c5e501a7845e"
	katVector1    = "91e53753bc7765678668fbd9526a7e0b009e2e7932e6d06bb8cd823f7de37c86"
)

// TestPBKDF2KnownAnswerVector pins VerifyPassword's derivation to REAL
// PBKDF2-HMAC-SHA256 at the iteration count the Credential declares, against a
// vector this package did not produce.
//
// REGRESSION SENSITIVITY: if VerifyPassword ignores c.Iterations and hardcodes a
// different count (mutation 1), it derives a different key and the correct
// password stops verifying — the first assertion fails. The two negative cases
// assert the converse directly: a credential whose Hash is the 1-iteration or
// 100000-iteration digest must NOT verify at 210000, so an implementation that
// silently ran the weaker KDF is caught by name.
func TestPBKDF2KnownAnswerVector(t *testing.T) {
	mustHex := func(s string) []byte {
		b, err := hex.DecodeString(s)
		if err != nil {
			t.Fatalf("decode vector: %v", err)
		}
		return b
	}

	good := authn.Credential{
		Algo:       authn.PBKDF2Algo,
		Iterations: 210000,
		Salt:       []byte(katSalt),
		Hash:       mustHex(katVector210k),
	}
	if !authn.VerifyPassword(good, goodPassword) {
		t.Fatal("VerifyPassword rejected an INDEPENDENTLY generated PBKDF2-HMAC-SHA256 " +
			"vector at 210000 iterations: the derivation does not match the declared parameters")
	}

	// The same password and salt at a WEAKER iteration count must not verify at
	// 210000. These are the two mutations that previously survived.
	for name, vec := range map[string]string{
		"1 iteration (mutation 1)":       katVector1,
		"100000 iterations (mutation 2)": katVector100k,
	} {
		weak := good
		weak.Hash = mustHex(vec)
		if authn.VerifyPassword(weak, goodPassword) {
			t.Fatalf("a %s digest verified at 210000 iterations — the KDF is not "+
				"honouring the iteration count", name)
		}
	}
}

// TestHashPasswordDerivationMatchesStoredParameters re-derives the key from the
// parameters the Credential ITSELF reports and requires it to equal the stored
// hash. This is what makes the stored metadata trustworthy: it is not enough for
// HashPassword to be internally self-consistent, the row it writes must actually
// DESCRIBE the derivation a future VerifyPassword will repeat.
//
// REGRESSION SENSITIVITY: mutation 1 (derive at 1 iteration while still storing
// Iterations: PBKDF2Iterations) makes the re-derivation disagree with the stored
// hash and this fails — even though the package's own round trip still passes,
// which is precisely how that mutation escaped before.
func TestHashPasswordDerivationMatchesStoredParameters(t *testing.T) {
	c, err := authn.HashPassword(goodPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	want, err := pbkdf2.Key(sha256.New, goodPassword, c.Salt, c.Iterations, len(c.Hash))
	if err != nil {
		t.Fatalf("re-derive: %v", err)
	}
	if !bytes.Equal(want, c.Hash) {
		t.Fatal("the stored hash was NOT produced by the stored (salt, iterations): " +
			"the credential's own metadata does not describe its derivation")
	}
}

// TestPBKDF2IterationsIsPinnedAbsolutely asserts the LITERAL value of the tuned
// cost. Every other assertion in this file compares against the constant, which
// cannot detect the constant itself changing.
//
// If you are deliberately raising this (OWASP guidance moves), change the literal
// here AND regenerate katVector210k — the KAT above will otherwise fail, which is
// the intended speed bump.
func TestPBKDF2IterationsIsPinnedAbsolutely(t *testing.T) {
	if authn.PBKDF2Iterations != 210000 {
		t.Fatalf("PBKDF2Iterations = %d, want the pinned 210000 (OWASP 2023 floor "+
			"for PBKDF2-HMAC-SHA256); a silent downgrade is a security regression",
			authn.PBKDF2Iterations)
	}
}
