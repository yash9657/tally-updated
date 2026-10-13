package authz_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/authz"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/migrations"
)

var (
	adminDSN = testdb.AdminDSNValue()
	appDSN   = testdb.AppDSNValue()
)

// newStore migrates and returns an authz.Store over the app pool, skipping if no
// DB is available so the suite stays green offline.
func newStore(t *testing.T) (*authz.Store, *db.Pool) {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped (CORED_SKIP_DB set)")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	if err := migrations.Apply(ctx, admin); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("apply migrations: %v", err)
	}
	_ = admin.Close(ctx)

	pool, err := db.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return authz.NewStore(pool), pool
}

// seedCompanyWithOwner inserts a company (RLS-subject under its own GUC),
// creates the user, and seeds the Owner level + membership. Returns the new
// user id and Owner level id.
func seedCompanyWithOwner(t *testing.T, store *authz.Store, pool *db.Pool, subject string) (companyID, userID, levelID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	companyID = mustV7(t)

	// Insert the company under its own company GUC (mirrors handleCreateCompany).
	cctx := db.WithCompany(ctx, companyID)
	if err := pool.RunInTx(cctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO company (id, company_id, name, fy_start_date)
			 VALUES ($1, $1, $2, '2025-04-01')`, companyID, "authz-"+companyID.String()[:8])
		return err
	}); err != nil {
		t.Fatalf("insert company: %v", err)
	}
	// Registered here even though this company is CHILDLESS at this point — the
	// raw INSERT deliberately skips seed_company_defaults, so there is no chart of
	// accounts to orphan. The teardown is still correct and still required: the
	// SeedCompanyAuthDefaults call below plants a security_level tree, the ACL
	// rules under it and a company_membership row, all company-scoped, and every
	// one of them is deleted by company_id. Registering before the seed means a
	// t.Fatal partway through it still cleans up what was written.
	//
	// What deliberately SURVIVES is the app_user row: users are not tenant-scoped
	// (one login can belong to several companies), so testdb classifies app_user
	// as not-tenant-scoped and leaves it alone rather than deleting a principal
	// that another company may still reference.
	testdb.Cleanup(t, companyID)

	uid, err := store.CreateUserOnFirstLogin(ctx, subject, "", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	userID = uid

	if err := store.SeedCompanyAuthDefaults(ctx, companyID, userID); err != nil {
		t.Fatalf("seed auth defaults: %v", err)
	}

	lvl, found, err := store.Membership(ctx, userID, companyID)
	if err != nil || !found {
		t.Fatalf("membership after seed: found=%v err=%v", found, err)
	}
	levelID = lvl
	return companyID, userID, levelID
}

func mustV7(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuidv7: %v", err)
	}
	return id
}

// TestStoreOwnerSeedAndEvaluate proves the seeded Owner level evaluates to a
// full-access allow through the real catalog (level/rules read under the company
// GUC), and a member can read their own membership.
func TestStoreOwnerSeedAndEvaluate(t *testing.T) {
	store, pool := newStore(t)
	ctx := context.Background()

	companyID, userID, levelID := seedCompanyWithOwner(t, store, pool, "owner-"+uuid.NewString())

	level, err := store.Level(ctx, companyID, levelID)
	if err != nil {
		t.Fatalf("level: %v", err)
	}
	if !level.IsOwner || level.DefaultAccess != "full" || !level.RequiresWebAuthn {
		t.Fatalf("seeded Owner level wrong: %+v", level)
	}
	rules, err := store.Rules(ctx, companyID, levelID)
	if err != nil {
		t.Fatalf("rules: %v", err)
	}
	// Owner full-access write WITH webauthn -> allow.
	d := authz.Evaluate(level, rules, authz.Request{
		UserID: userID, CompanyID: companyID, Action: authz.Create, Resource: "sales", HasWebAuthn: true,
	})
	if !d.Allow {
		t.Fatalf("owner create should allow, got %+v", d)
	}
}

// TestStoreCrossTenantMembershipDenied is the cross-tenant gate at the STORE
// level: a member of company A has NO membership in company B, so Membership(A's
// user, B) returns found=false (which the middleware maps to 403). This holds
// even though the row physically exists, because RLS scopes by the user GUC.
func TestStoreCrossTenantMembershipDenied(t *testing.T) {
	store, pool := newStore(t)
	ctx := context.Background()

	companyA, userA, _ := seedCompanyWithOwner(t, store, pool, "ownerA-"+uuid.NewString())
	companyB, userB, _ := seedCompanyWithOwner(t, store, pool, "ownerB-"+uuid.NewString())

	// User A is a member of A but NOT of B.
	if _, found, err := store.Membership(ctx, userA, companyA); err != nil || !found {
		t.Fatalf("user A should be a member of A: found=%v err=%v", found, err)
	}
	if _, found, err := store.Membership(ctx, userA, companyB); err != nil || found {
		t.Fatalf("SECURITY: user A must NOT be a member of B: found=%v err=%v", found, err)
	}
	// And symmetric for B.
	if _, found, err := store.Membership(ctx, userB, companyA); err != nil || found {
		t.Fatalf("SECURITY: user B must NOT be a member of A: found=%v err=%v", found, err)
	}
}

// TestStoreUserSeesOnlyOwnRow proves app_user RLS: looking up by another user's
// subject returns not-found under your own subject scope is not the concern here;
// the concern is that LookupUserBySubject scopes the read to that subject.
func TestStoreUserSeesOnlyOwnRow(t *testing.T) {
	store, pool := newStore(t)
	ctx := context.Background()

	subjectA := "soloA-" + uuid.NewString()
	_, userA, _ := seedCompanyWithOwner(t, store, pool, subjectA)

	uid, active, found, err := store.LookupUserBySubject(ctx, subjectA)
	if err != nil || !found {
		t.Fatalf("lookup own subject: found=%v err=%v", found, err)
	}
	if uid != userA || !active {
		t.Fatalf("lookup returned uid=%v active=%v, want %v active", uid, active, userA)
	}
	// A subject that was never created is not found.
	if _, _, found, err := store.LookupUserBySubject(ctx, "never-"+uuid.NewString()); err != nil || found {
		t.Fatalf("unknown subject must be not-found: found=%v err=%v", found, err)
	}
}

// TestAuthzHarnessRemovesItsCompany is the anti-rot guard on
// seedCompanyWithOwner()'s teardown. Forgetting the registration breaks no
// assertion in this package — it just buries another company, its security-level
// tree, its ACL rules and its membership row on every run, which is how the
// shared dev database reached 43,419 companies.
//
// The tables checked here are chosen for THIS harness, not copied blindly from
// its siblings. seedCompanyWithOwner uses a raw INSERT that deliberately skips
// seed_company_defaults, so this company never has a chart of accounts;
// account_group / ledger / voucher_type would be trivially zero whether or not
// the teardown ran, and asserting on them alone would be vacuous. What this
// harness actually writes is security_level, acl_rule and company_membership,
// so those are what must come back empty. They are also the rows that made
// acl_rule big enough to need an index of its own (added in eacf62e after a
// backfill ran 1h57m against the accumulated fleet).
//
// app_user is deliberately NOT checked: a login is not tenant-scoped — one user
// can belong to several companies — so testdb classifies app_user as
// not-tenant-scoped and leaves the principal in place.
func TestAuthzHarnessRemovesItsCompany(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped (CORED_SKIP_DB set)")
	}
	// Cleanups registered on the inner *testing.T have all run by the time t.Run
	// returns, so the harness has had its chance before the next line.
	var cid uuid.UUID
	t.Run("harness", func(t *testing.T) {
		store, pool := newStore(t)
		cid, _, _ = seedCompanyWithOwner(t, store, pool, "teardown-probe-"+uuid.NewString())
	})
	if cid == uuid.Nil {
		t.Skip("harness skipped (no database), nothing to assert about teardown")
	}

	ctx := context.Background()
	// ADMIN, deliberately: the app role is FORCE-RLS'd to a single company and
	// would answer 0 for every table whatever survived — a vacuous green.
	conn, err := pgx.Connect(ctx, testdb.AdminDSNValue())
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	defer conn.Close(ctx)

	for _, tbl := range []string{"company", "security_level", "acl_rule", "company_membership"} {
		var n int
		if err := conn.QueryRow(ctx, "SELECT count(*) FROM "+tbl+" WHERE company_id = $1", cid).Scan(&n); err != nil {
			t.Fatalf("count %s for company %s: %v", tbl, cid, err)
		}
		if n != 0 {
			t.Errorf("%s still holds %d rows for company %s after the harness cleaned up: "+
				"the authz harness leaks them on EVERY run and nothing will ever delete them",
				tbl, n, cid)
		}
	}
}
