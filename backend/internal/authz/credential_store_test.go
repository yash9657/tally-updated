package authz_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/authn"
	"github.com/khata/cored/internal/authz"
	"github.com/khata/cored/internal/platform/db"
)

// credUser provisions an app_user for a fresh subject and returns (subject, id).
func credUser(t *testing.T, store *authz.Store) (string, uuid.UUID) {
	t.Helper()
	subject := "cred-" + uuid.NewString()
	uid, err := store.CreateUserOnFirstLogin(context.Background(), subject, subject+"@example.test", "Cred User")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return subject, uid
}

// TestCredentialRoundTrip is the primary success oracle for the store: a password
// written under the USER GUC is readable back through the PRE-AUTH subject path,
// and it verifies.
//
// REGRESSION SENSITIVITY: this is the test that fails if the mid-transaction
// set_config('app.current_user_id', ...) in CredentialBySubject is removed —
// without it the user_credential SELECT is filtered out by RLS and found comes
// back false. It also fails if SetPassword's INSERT is aimed at the wrong column
// set, or if the two halves disagree about which user id owns the row.
func TestCredentialRoundTrip(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()

	subject, uid := credUser(t, store)

	c, err := authn.HashPassword("tally-demo-2026")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := store.SetPassword(ctx, uid, c); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}

	gotID, gotCred, active, found, err := store.CredentialBySubject(ctx, subject)
	if err != nil {
		t.Fatalf("CredentialBySubject: %v", err)
	}
	if !found {
		t.Fatal("found = false after SetPassword: the credential is not readable through the pre-auth subject path")
	}
	if gotID != uid {
		t.Fatalf("user id = %s, want %s", gotID, uid)
	}
	if !active {
		t.Fatal("a freshly created user must be active")
	}
	if !authn.VerifyPassword(gotCred, "tally-demo-2026") {
		t.Fatal("the round-tripped credential does not verify the original password (salt/hash/iterations were not stored faithfully)")
	}
	if authn.VerifyPassword(gotCred, "wrong-password-x") {
		t.Fatal("the round-tripped credential verified a WRONG password")
	}
	if gotCred.Iterations != authn.PBKDF2Iterations || gotCred.Algo != authn.PBKDF2Algo {
		t.Fatalf("round-tripped KDF params = %s/%d, want %s/%d", gotCred.Algo, gotCred.Iterations, authn.PBKDF2Algo, authn.PBKDF2Iterations)
	}
}

// TestCredentialBySubjectNotFoundCases pins the two found=false paths and the
// fact that they are distinguished ONLY internally.
//
// REGRESSION SENSITIVITY: if the pgx.ErrNoRows branches were dropped, an unknown
// subject would return an ERROR instead of found=false, and the login handler
// would answer 500 for a typo'd username — both a bad UX and a user-enumeration
// oracle. If found were ever set true for a user with no credential row, the
// zero-value Credential would reach VerifyPassword and (before the totality
// guards) could match.
func TestCredentialBySubjectNotFoundCases(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()

	t.Run("unknown subject", func(t *testing.T) {
		id, cred, active, found, err := store.CredentialBySubject(ctx, "no-such-subject-"+uuid.NewString())
		if err != nil {
			t.Fatalf("unknown subject must not error, got %v", err)
		}
		if found {
			t.Fatal("found = true for a subject that does not exist")
		}
		if id != uuid.Nil || active {
			t.Fatalf("unknown subject leaked state: id=%s active=%v", id, active)
		}
		if authn.VerifyPassword(cred, "anything-at-all") {
			t.Fatal("the zero credential returned for an unknown subject must never verify")
		}
	})

	t.Run("known subject with no credential", func(t *testing.T) {
		subject, uid := credUser(t, store)
		id, cred, active, found, err := store.CredentialBySubject(ctx, subject)
		if err != nil {
			t.Fatalf("CredentialBySubject: %v", err)
		}
		if found {
			t.Fatal("found = true for a user who has never set a password")
		}
		// Internally distinguishable (the user id IS known) — the wire must not be.
		if id != uid {
			t.Fatalf("internal user id = %s, want %s (the two not-found cases must be distinguishable INTERNALLY)", id, uid)
		}
		if !active {
			t.Fatal("a freshly created user must report active")
		}
		if authn.VerifyPassword(cred, "") {
			t.Fatal("a user with no credential must not be verifiable with the empty password")
		}
	})

	t.Run("empty subject", func(t *testing.T) {
		_, _, _, found, err := store.CredentialBySubject(ctx, "")
		if err != nil || found {
			t.Fatalf("empty subject: found=%v err=%v, want false/nil", found, err)
		}
	})
}

// TestCredentialBySubjectReportsInactive proves is_active travels with the
// credential, so the login handler can refuse a disabled account.
//
// REGRESSION SENSITIVITY: dropping is_active from the SELECT (or hard-coding
// active=true) makes this fail — and would let a deactivated employee keep
// logging in with a password nobody rotated.
func TestCredentialBySubjectReportsInactive(t *testing.T) {
	store, pool := newStore(t)
	ctx := context.Background()

	subject, uid := credUser(t, store)
	c, err := authn.HashPassword("tally-demo-2026")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := store.SetPassword(ctx, uid, c); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}

	// Deactivate the user under its own subject GUC (app_user's WITH CHECK admits
	// `subject = app_current_subject()`).
	if err := pool.RunInAuthTx(db.WithSubject(ctx, subject), func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE app_user SET is_active = false WHERE subject = $1`, subject)
		return err
	}); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	_, _, active, found, err := store.CredentialBySubject(ctx, subject)
	if err != nil {
		t.Fatalf("CredentialBySubject: %v", err)
	}
	if !found {
		t.Fatal("the credential must still be found for a deactivated user (the handler, not the store, decides the policy)")
	}
	if active {
		t.Fatal("active = true for a user whose is_active is false")
	}
}

// TestSetPasswordIsUpsert proves a password CHANGE replaces the verifier rather
// than inserting a duplicate or silently no-oping.
//
// REGRESSION SENSITIVITY: dropping the ON CONFLICT clause makes the second
// SetPassword fail with a PK violation; making it DO NOTHING leaves the OLD
// password working, which is the worst possible outcome of "I changed my
// password because it leaked".
func TestSetPasswordIsUpsert(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()

	subject, uid := credUser(t, store)

	first, err := authn.HashPassword("first-password-1")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := store.SetPassword(ctx, uid, first); err != nil {
		t.Fatalf("SetPassword (first): %v", err)
	}
	second, err := authn.HashPassword("second-password-2")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := store.SetPassword(ctx, uid, second); err != nil {
		t.Fatalf("SetPassword (second) must upsert, got: %v", err)
	}

	_, cred, _, found, err := store.CredentialBySubject(ctx, subject)
	if err != nil || !found {
		t.Fatalf("CredentialBySubject after change: found=%v err=%v", found, err)
	}
	if authn.VerifyPassword(cred, "first-password-1") {
		t.Fatal("the OLD password still verifies after a password change")
	}
	if !authn.VerifyPassword(cred, "second-password-2") {
		t.Fatal("the NEW password does not verify after a password change")
	}
}

// TestCredentialRLSIsSelfOnly is the migration-0021 policy gate, and the reason
// the hash lives in its own table at all.
//
// It asserts, with RAW SQL through the app pool (so RLS — not the query text — is
// what isolates):
//
//	(1) under user A's GUC, a SELECT for user B's credential returns NOTHING,
//	    even though the row physically exists and the query names B's id;
//	(2) under user A's GUC, an INSERT of a credential for user B is REFUSED by
//	    the WITH CHECK half of the policy;
//	(3) under ONLY the subject GUC (no user GUC), a user's OWN credential is
//	    invisible — which is precisely why CredentialBySubject must promote to the
//	    user GUC mid-transaction.
//
// REGRESSION SENSITIVITY: (1)+(2) fail if 0021's policy ever grows a company
// clause (the exact mistake that putting the hash on app_user would have made,
// since 0020 widened app_user to co-members). (3) fails if the policy were keyed
// on the subject instead of the user id, which would make the mid-tx dance
// pointless AND make the table readable in any subject-scoped transaction.
func TestCredentialRLSIsSelfOnly(t *testing.T) {
	store, pool := newStore(t)
	ctx := context.Background()

	subjectA, uidA := credUser(t, store)
	_, uidB := credUser(t, store)

	c, err := authn.HashPassword("tally-demo-2026")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := store.SetPassword(ctx, uidA, c); err != nil {
		t.Fatalf("SetPassword A: %v", err)
	}
	if err := store.SetPassword(ctx, uidB, c); err != nil {
		t.Fatalf("SetPassword B: %v", err)
	}

	// (1) A cannot READ B's credential.
	var visible int
	if err := pool.RunInAuthTx(db.WithUser(ctx, uidA), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM user_credential WHERE user_id = $1`, uidB).Scan(&visible)
	}); err != nil {
		t.Fatalf("read B under A's GUC: %v", err)
	}
	if visible != 0 {
		t.Fatalf("user A can see %d of user B's credential rows: user_credential is NOT self-only", visible)
	}
	// Control: A CAN see its own row, so the zero above is RLS and not an empty table.
	var own int
	if err := pool.RunInAuthTx(db.WithUser(ctx, uidA), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM user_credential WHERE user_id = $1`, uidA).Scan(&own)
	}); err != nil {
		t.Fatalf("read own under A's GUC: %v", err)
	}
	if own != 1 {
		t.Fatalf("user A sees %d of its OWN credential rows, want 1 (the isolation check above would be vacuous)", own)
	}

	// (2) A cannot WRITE a credential for B.
	err = pool.RunInAuthTx(db.WithUser(ctx, uidA), func(ctx context.Context, tx pgx.Tx) error {
		_, execErr := tx.Exec(ctx, `
			INSERT INTO user_credential (user_id, algo, iterations, salt, hash)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (user_id) DO UPDATE SET hash = EXCLUDED.hash`,
			uidB, c.Algo, c.Iterations, c.Salt, c.Hash)
		return execErr
	})
	if err == nil {
		t.Fatal("user A wrote a credential for user B: the policy's WITH CHECK half is missing or wrong")
	}
	if !strings.Contains(err.Error(), "row-level security") {
		t.Fatalf("expected an RLS violation writing B's row, got: %v", err)
	}

	// (3) The SUBJECT GUC alone is not enough — this is what makes the mid-tx
	// promotion in CredentialBySubject load-bearing rather than decorative.
	var undersubject int
	if err := pool.RunInAuthTx(db.WithSubject(ctx, subjectA), func(ctx context.Context, tx pgx.Tx) error {
		// Sanity: the subject GUC DOES admit the app_user row (step (b) works).
		var n int
		if e := tx.QueryRow(ctx, `SELECT count(*) FROM app_user WHERE subject = $1`, subjectA).Scan(&n); e != nil {
			return e
		}
		if n != 1 {
			t.Fatalf("subject GUC admits %d app_user rows, want 1 — step (b) of the dance is broken", n)
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM user_credential WHERE user_id = $1`, uidA).Scan(&undersubject)
	}); err != nil {
		t.Fatalf("read own credential under subject GUC only: %v", err)
	}
	if undersubject != 0 {
		t.Fatalf("the credential is visible with only the SUBJECT GUC set (%d rows): the two-step dance would be unnecessary and the table is broader than self-only", undersubject)
	}
}

// TestSetPasswordRejectsMalformedCredentials proves the store refuses to persist
// anything the schema's CHECKs would reject, with a clear Go error.
//
// REGRESSION SENSITIVITY: removing the guards turns these into raw constraint
// violations (or, for a would-be future algo, a silently unverifiable row).
func TestSetPasswordRejectsMalformedCredentials(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()
	_, uid := credUser(t, store)

	good, err := authn.HashPassword("tally-demo-2026")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	cases := map[string]authn.Credential{
		"unknown algo":   {Algo: "bcrypt", Iterations: good.Iterations, Salt: good.Salt, Hash: good.Hash},
		"zero value":     {},
		"low iterations": {Algo: authn.PBKDF2Algo, Iterations: 1000, Salt: good.Salt, Hash: good.Hash},
		"missing salt":   {Algo: authn.PBKDF2Algo, Iterations: good.Iterations, Hash: good.Hash},
		"missing hash":   {Algo: authn.PBKDF2Algo, Iterations: good.Iterations, Salt: good.Salt},
	}
	for name, c := range cases {
		if err := store.SetPassword(ctx, uid, c); err == nil {
			t.Fatalf("SetPassword accepted a %s credential", name)
		}
	}
	if err := store.SetPassword(ctx, uuid.Nil, good); err == nil {
		t.Fatal("SetPassword accepted an empty user id")
	}
}

// TestProfileByUserID proves the login response's `user` block is read from the
// real directory row, self-scoped.
//
// REGRESSION SENSITIVITY: fails if the read is not user-GUC-scoped (the row
// becomes invisible and the method errors), or if the columns are transposed.
func TestProfileByUserID(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()

	subject, uid := credUser(t, store)
	gotSubject, displayName, email, err := store.ProfileByUserID(ctx, uid)
	if err != nil {
		t.Fatalf("ProfileByUserID: %v", err)
	}
	if gotSubject != subject {
		t.Fatalf("subject = %q, want %q", gotSubject, subject)
	}
	if displayName != "Cred User" {
		t.Fatalf("display_name = %q, want %q", displayName, "Cred User")
	}
	if email != subject+"@example.test" {
		t.Fatalf("email = %q, want %q", email, subject+"@example.test")
	}
	if _, _, _, err := store.ProfileByUserID(ctx, uuid.New()); err == nil {
		t.Fatal("ProfileByUserID must fail for a user id that does not exist / is not the caller")
	}
}
