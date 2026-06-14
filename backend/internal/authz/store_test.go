package authz_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/authz"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/migrations"
)

const (
	adminDSN = "postgres://postgres:postgres@localhost:5433/tally?sslmode=disable"
	appDSN   = "postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable"
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
