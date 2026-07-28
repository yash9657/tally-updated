package migration_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/migration"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/internal/reporting"
	migs "github.com/khata/cored/migrations"
)

const (
	adminDSN = "postgres://postgres:postgres@localhost:5433/tally?sslmode=disable"
	appDSN   = "postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable"
)

// repoRoot walks up from the test's working directory to the repository root
// (the directory that contains the corpora/ tree), so the migration corpus can
// be located independently of where `go test` is invoked.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "corpora", "migration")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not locate corpora/migration from working directory")
	return ""
}

// setup migrates the live DB (idempotent) and returns an app-scoped pool plus a
// freshly created, seeded company with a unique id per run.
func setup(t *testing.T) (*db.Pool, context.Context, uuid.UUID) {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	ctx := context.Background()

	adminConn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable, skipping integration test: %v", err)
	}
	if err := migs.Apply(ctx, adminConn); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("apply migrations: %v", err)
	}
	_ = adminConn.Close(ctx)

	pool, err := db.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}
	t.Cleanup(pool.Close)

	companyID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new company id: %v", err)
	}
	cctx := db.WithCompany(ctx, companyID)

	ids := identity.NewService(pool)
	if _, err := ids.CreateCompany(cctx, identity.Company{
		ID:          companyID,
		Name:        "Migration Co " + companyID.String()[:8],
		FYStartDate: "2025-04-01",
	}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	// Teardown, registered the moment the company exists. An import test is one
	// of the heaviest leakers in the tree: a single corpus import lands masters,
	// vouchers, postings, bill allocations and reconciliation_queue rows, and
	// reconciliation_queue alone accounts for 363 MB of the dev database.
	testdb.Cleanup(t, companyID)
	return pool, cctx, companyID
}

// TestImportSmoke3VoucherReconcilesToThePaisa is the switching-cost-killer gate:
// import the three-voucher Tally XML export and assert the resulting Trial
// Balance equals the expected JSON exactly (Dr/Cr 80000.0000 balanced, with the
// documented per-ledger balances).
func TestImportSmoke3VoucherReconcilesToThePaisa(t *testing.T) {
	pool, ctx, companyID := setup(t)
	root := repoRoot(t)

	xmlPath := filepath.Join(root, "corpora", "migration", "smoke-3-voucher.xml")
	jsonPath := filepath.Join(root, "corpora", "migration", "smoke-3-voucher.json")

	im := migration.NewImporter(masters.NewService(pool), ledger.NewService(pool))
	res, err := im.ImportFile(ctx, xmlPath, "tester")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.VouchersPosted != 3 {
		t.Fatalf("expected 3 vouchers posted, got %d", res.VouchersPosted)
	}
	if res.SourceCompany != "Smoke Test Traders" {
		t.Fatalf("unexpected source company %q", res.SourceCompany)
	}
	// Distinct ledgers across the three vouchers: Cash, Capital, Sales, Rent.
	for _, want := range []string{"Cash", "Capital", "Sales", "Rent"} {
		var found bool
		for _, n := range res.LedgersEnsured {
			if n == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("ledger %q not ensured during import (got %v)", want, res.LedgersEnsured)
		}
	}

	// Every imported voucher must be version 1 with a non-empty hash (it went
	// through the immutable hash-chained post path, not some side door).
	for i, p := range res.Posted {
		if p.VersionNo != 1 {
			t.Fatalf("voucher %d should be version 1, got %d", i+1, p.VersionNo)
		}
		if len(p.Hash) == 0 {
			t.Fatalf("voucher %d has empty hash", i+1)
		}
	}

	// THE GATE: reconcile to the paisa.
	expected, err := migration.LoadExpectedFile(jsonPath)
	if err != nil {
		t.Fatalf("load expected: %v", err)
	}
	rep := reporting.NewService(pool)
	if err := migration.Reconcile(ctx, rep, companyID, expected); err != nil {
		t.Fatalf("reconciliation failed:\n%v", err)
	}

	// Belt-and-braces: independently assert the Cash closing balance and the
	// balanced 80000 totals straight off the report.
	tb, err := rep.TrialBalance(ctx, companyID, "")
	if err != nil {
		t.Fatalf("trial balance: %v", err)
	}
	if !tb.IsBalanced() {
		t.Fatalf("trial balance not balanced: dr=%s cr=%s", tb.TotalDebit, tb.TotalCredit)
	}
	if !tb.TotalDebit.Equal(decimal.RequireFromString("80000.0000")) ||
		!tb.TotalCredit.Equal(decimal.RequireFromString("80000.0000")) {
		t.Fatalf("expected totals 80000/80000, got %s/%s", tb.TotalDebit, tb.TotalCredit)
	}
	var sawCash bool
	for _, r := range tb.Rows {
		if r.LedgerName == "Cash" {
			sawCash = true
			if !r.Debit.Equal(decimal.RequireFromString("72000.0000")) || !r.Credit.IsZero() {
				t.Fatalf("Cash closing expected Dr 72000.0000 / Cr 0, got Dr %s / Cr %s", r.Debit, r.Credit)
			}
		}
	}
	if !sawCash {
		t.Fatal("Cash ledger missing from trial balance")
	}
}

// TestReconcileFailsLoudlyOnMismatch proves the gate is real: feeding a doctored
// expectation must produce a non-nil, descriptive error rather than passing.
func TestReconcileFailsLoudlyOnMismatch(t *testing.T) {
	pool, ctx, companyID := setup(t)
	root := repoRoot(t)
	xmlPath := filepath.Join(root, "corpora", "migration", "smoke-3-voucher.xml")

	im := migration.NewImporter(masters.NewService(pool), ledger.NewService(pool))
	if _, err := im.ImportFile(ctx, xmlPath, "tester"); err != nil {
		t.Fatalf("import: %v", err)
	}

	// Deliberately wrong expectation: claim Cash is 99999 Dr.
	bad := migration.ExpectedTrialBalance{
		ExpectedTrialBalance: []migration.ExpectedBalance{
			{Ledger: "Cash", Debit: "99999.0000", Credit: "0.0000"},
		},
		ExpectedTotals: migration.ExpectedTotals{Debit: "80000.0000", Credit: "80000.0000", Balanced: true},
	}
	rep := reporting.NewService(pool)
	err := migration.Reconcile(ctx, rep, companyID, bad)
	if err == nil {
		t.Fatal("expected reconciliation to FAIL on a doctored expectation, got nil")
	}
	if !strings.Contains(err.Error(), "Cash") {
		t.Fatalf("reconcile error should name the offending ledger, got: %v", err)
	}
}

// TestTallySignConvention unit-tests the Tally Dr/Cr sign decoding in isolation
// (no DB): ISDEEMEDPOSITIVE=Yes => debit, magnitude is the absolute AMOUNT.
func TestImportRequiresCompanyScope(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, appDSN)
	if err != nil {
		t.Skipf("pool unavailable: %v", err)
	}
	defer pool.Close()

	im := migration.NewImporter(masters.NewService(pool), ledger.NewService(pool))
	root := repoRoot(t)
	xmlPath := filepath.Join(root, "corpora", "migration", "smoke-3-voucher.xml")
	// No company in context: the GUC guard must refuse before posting anything.
	if _, err := im.ImportFile(context.Background(), xmlPath, "t"); err == nil {
		t.Fatal("expected import to fail without a company in context")
	}
}

// TestMigrationHarnessRemovesItsCompany is the anti-rot guard on setup()'s
// teardown. A harness that stops cleaning up fails nothing visible — it just
// buries another imported company on every run, and this package's companies are
// the fat ones: a corpus import brings masters, vouchers, postings, bill
// allocations and a reconciliation_queue row along with it.
//
// The child tables are counted, not only `company`. Only notification_outbox
// references company(id), and its foreign-key action is NO ACTION, so removing
// the parent row alone orphans everything beneath it silently. An assertion that
// checked `company` on its own would have called that bug fixed.
func TestMigrationHarnessRemovesItsCompany(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	// Cleanups registered on the inner *testing.T have all run by the time t.Run
	// returns, so the harness has had its chance before the next line.
	var cid uuid.UUID
	t.Run("harness", func(t *testing.T) { _, _, cid = setup(t) })
	if cid == uuid.Nil {
		t.Skip("harness skipped (no database), nothing to assert about teardown")
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, testdb.AdminDSNValue())
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	defer conn.Close(ctx)

	// ADMIN, deliberately: the app role is FORCE-RLS'd to a single company and
	// would answer 0 for every table whatever survived — a vacuous green.
	for _, tbl := range []string{"company", "account_group", "ledger", "voucher_type", "acl_rule"} {
		var n int
		if err := conn.QueryRow(ctx, "SELECT count(*) FROM "+tbl+" WHERE company_id = $1", cid).Scan(&n); err != nil {
			t.Fatalf("count %s for company %s: %v", tbl, cid, err)
		}
		if n != 0 {
			t.Errorf("%s still holds %d rows for company %s after the harness cleaned up: "+
				"the migration harness leaks them on EVERY run and nothing will ever delete them",
				tbl, n, cid)
		}
	}
}
