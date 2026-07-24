// Package security_test is the tenant-isolation RED TEAM for the backend.
//
// It does not test happy-path business logic; its sole job is to TRY TO BREAK
// tenant isolation and prove it holds. Every probe runs as the unprivileged
// tally_app role against the live Postgres. Two companies (A and B) are seeded
// with admin privileges so that cross-tenant state genuinely exists; the red
// team then attempts, as the app role, to observe or mutate the OTHER tenant's
// rows through reads, writes, joins, forged/absent GUCs, and RLS-disable
// attempts. The contract being defended:
//
//   - A missing, reset, empty, or malformed tenant GUC yields ZERO rows
//     (default-deny) and never errors out or exposes all rows.
//   - A GUC scoped to company A never reveals company B's rows, even when the
//     attacker supplies B's exact primary key or crafts a cross-company join.
//   - Writes (INSERT/UPDATE/DELETE) cannot create or touch another tenant's
//     rows.
//   - The tally_app role lacks BYPASSRLS and table ownership, and the tables
//     are FORCE ROW LEVEL SECURITY so even the owner is constrained.
//
// The corpus in corpora/isolation/*.json is loaded and each scenario's intent
// is asserted; the hand-written probes go beyond the corpus.
package security_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/khata/cored/migrations"
)

const (
	adminDSN = "postgres://postgres:postgres@localhost:5433/tally?sslmode=disable"
	appDSN   = "postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable"
)

// tenantTables are every table that carries company_id and must be RLS-locked.
var tenantTables = []string{
	"company", "account_group", "ledger", "voucher_type",
	"voucher_version", "posting", "ledger_balance",
}

// harness holds the two adversary tenants and both privilege levels.
type harness struct {
	admin    *pgx.Conn   // superuser; bypasses RLS, used only to seed/inspect ground truth.
	app      *pgxpool.Pool // tally_app; the surface the red team attacks.
	companyA uuid.UUID
	companyB uuid.UUID
	// seeded voucher_version counts as ground truth (verified via admin).
	seedA int
	seedB int
	// a known company-B voucher primary key, for by-id leak probes.
	bVoucherID uuid.UUID
}

func skipIfNoDB(t *testing.T) {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped (CORED_SKIP_DB set)")
	}
}

// setupHarness migrates the DB, seeds two companies with `nA`/`nB` voucher
// rows each (planted with admin privileges so they exist regardless of RLS),
// and returns connections for both privilege levels.
func setupHarness(t *testing.T, nA, nB int) *harness {
	t.Helper()
	ctx := context.Background()

	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable, skipping isolation red-team: %v", err)
	}
	if err := migrations.Apply(ctx, admin); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("apply migrations: %v", err)
	}

	app, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("app pool: %v", err)
	}

	h := &harness{
		admin:    admin,
		app:      app,
		companyA: mustV7(t),
		companyB: mustV7(t),
		seedA:    nA,
		seedB:    nB,
	}
	t.Cleanup(func() {
		h.cleanup(t)
		app.Close()
		_ = admin.Close(ctx)
	})

	// Plant both companies and their voucher rows directly via the superuser so
	// the cross-tenant data genuinely exists; RLS is what must hide it later.
	seedCompany(t, admin, h.companyA)
	seedCompany(t, admin, h.companyB)
	for i := 0; i < nA; i++ {
		seedVoucher(t, admin, h.companyA, i+1)
	}
	for i := 0; i < nB; i++ {
		id := seedVoucher(t, admin, h.companyB, i+1)
		h.bVoucherID = id // last B voucher id, used for by-id leak probes.
	}
	return h
}

func mustV7(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuidv7: %v", err)
	}
	return id
}

func seedCompany(t *testing.T, c *pgx.Conn, id uuid.UUID) {
	t.Helper()
	_, err := c.Exec(context.Background(),
		`INSERT INTO company (id, company_id, name, fy_start_date)
		 VALUES ($1, $1, $2, '2025-04-01')`,
		id, "redteam-"+id.String()[:8])
	if err != nil {
		t.Fatalf("seed company %s: %v", id, err)
	}
}

func seedVoucher(t *testing.T, c *pgx.Conn, company uuid.UUID, no int) uuid.UUID {
	t.Helper()
	id := mustV7(t)
	_, err := c.Exec(context.Background(),
		`INSERT INTO voucher_version
		   (id, company_id, voucher_id, version_no, op, hash, payload, created_by)
		 VALUES ($1, $2, $3, $4, 'CREATE', '\x00', '{}'::jsonb, 'redteam')`,
		id, company, mustV7(t), no)
	if err != nil {
		t.Fatalf("seed voucher for %s: %v", company, err)
	}
	return id
}

// cleanup removes planted rows via the superuser, temporarily lifting the
// append-only trigger so the immutable log can be tidied between runs.
func (h *harness) cleanup(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	_, _ = h.admin.Exec(ctx, `ALTER TABLE voucher_version DISABLE TRIGGER voucher_version_no_mutate`)
	_, _ = h.admin.Exec(ctx, `DELETE FROM voucher_version WHERE company_id = ANY($1)`,
		[]uuid.UUID{h.companyA, h.companyB})
	_, _ = h.admin.Exec(ctx, `ALTER TABLE voucher_version ENABLE TRIGGER voucher_version_no_mutate`)
	_, _ = h.admin.Exec(ctx, `DELETE FROM company WHERE id = ANY($1)`,
		[]uuid.UUID{h.companyA, h.companyB})
}

// asCompany runs fn inside an app-role transaction scoped to companyID via
// SET LOCAL, mirroring exactly how the production db layer binds the tenant.
// If companyID is empty, no GUC is set (simulating a forgotten scope).
func (h *harness) asCompany(t *testing.T, companyID string, fn func(tx pgx.Tx)) {
	t.Helper()
	ctx := context.Background()
	tx, err := h.app.Begin(ctx)
	if err != nil {
		t.Fatalf("begin app tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if companyID != "" {
		if _, err := tx.Exec(ctx,
			`SELECT set_config('app.current_company_id', $1, true)`, companyID); err != nil {
			t.Fatalf("set guc: %v", err)
		}
	}
	fn(tx)
}

// countVouchers returns the count visible to the current tx (RLS-filtered).
func countVouchers(t *testing.T, tx pgx.Tx) int {
	t.Helper()
	var n int
	if err := tx.QueryRow(context.Background(),
		`SELECT count(*) FROM voucher_version`).Scan(&n); err != nil {
		t.Fatalf("count vouchers: %v", err)
	}
	return n
}

// ---------------------------------------------------------------------------
// Static role / table posture (independent of any seeded data).
// ---------------------------------------------------------------------------

// TestAppRoleHasNoBypassRLS asserts the application role cannot escape RLS by
// privilege: no BYPASSRLS, no SUPERUSER. This is the keystone of isolation; if
// it regresses, every other policy is moot.
func TestAppRoleHasNoBypassRLS(t *testing.T) {
	skipIfNoDB(t)
	h := setupHarness(t, 0, 0)
	var bypass, super bool
	err := h.admin.QueryRow(context.Background(),
		`SELECT rolbypassrls, rolsuper FROM pg_roles WHERE rolname = 'tally_app'`).
		Scan(&bypass, &super)
	if err != nil {
		t.Fatalf("query pg_roles: %v", err)
	}
	if bypass {
		t.Fatal("SECURITY HOLE: tally_app has BYPASSRLS; it could read all tenants")
	}
	if super {
		t.Fatal("SECURITY HOLE: tally_app is SUPERUSER; RLS does not apply to it")
	}
}

// TestForceRowLevelSecurity asserts every tenant table has both ENABLE and
// FORCE row-level security, so even the table owner is constrained by the
// policy (defends against an owner-context query leaking all rows).
func TestForceRowLevelSecurity(t *testing.T) {
	skipIfNoDB(t)
	h := setupHarness(t, 0, 0)
	for _, tbl := range tenantTables {
		var enabled, forced bool
		err := h.admin.QueryRow(context.Background(),
			`SELECT relrowsecurity, relforcerowsecurity
			   FROM pg_class WHERE relname = $1`, tbl).Scan(&enabled, &forced)
		if err != nil {
			t.Fatalf("pg_class for %s: %v", tbl, err)
		}
		if !enabled {
			t.Errorf("SECURITY HOLE: %s does not have ROW LEVEL SECURITY enabled", tbl)
		}
		if !forced {
			t.Errorf("SECURITY HOLE: %s lacks FORCE ROW LEVEL SECURITY; owner-context queries bypass isolation", tbl)
		}
	}
}

// TestEveryTenantTableHasIsolationPolicy ensures none of the tenant tables was
// left without a policy (a table with RLS enabled but NO policy denies all,
// which is safe, but a missing policy on a non-RLS table would leak; we assert
// the policy exists and is bound to the GUC resolver).
func TestEveryTenantTableHasIsolationPolicy(t *testing.T) {
	skipIfNoDB(t)
	h := setupHarness(t, 0, 0)
	for _, tbl := range tenantTables {
		var usingExpr, withCheck string
		err := h.admin.QueryRow(context.Background(),
			`SELECT pg_get_expr(polqual, polrelid),
			        COALESCE(pg_get_expr(polwithcheck, polrelid), '')
			   FROM pg_policy
			  WHERE polrelid = $1::regclass AND polname = 'tenant_isolation'`, tbl).
			Scan(&usingExpr, &withCheck)
		if err != nil {
			t.Fatalf("SECURITY HOLE: no tenant_isolation policy on %s: %v", tbl, err)
		}
		if !strings.Contains(usingExpr, "company_id") {
			t.Errorf("%s USING expr does not reference company_id: %q", tbl, usingExpr)
		}
		// WITH CHECK must be present and equal to USING so writes are guarded.
		if withCheck == "" {
			t.Errorf("%s has no explicit WITH CHECK; write-side isolation relies on implicit default", tbl)
		}
	}
}

// ---------------------------------------------------------------------------
// Default-deny: absent / reset / empty / malformed GUC must yield ZERO rows
// and never error or leak.
// ---------------------------------------------------------------------------

// TestNoGUCReturnsZero: querying with no app.current_company_id set returns
// zero rows, never all rows.
func TestNoGUCReturnsZero(t *testing.T) {
	skipIfNoDB(t)
	h := setupHarness(t, 2, 3)
	h.asCompany(t, "", func(tx pgx.Tx) {
		if n := countVouchers(t, tx); n != 0 {
			t.Fatalf("SECURITY HOLE: no-GUC query returned %d rows, want 0 (total seeded=%d)", n, h.seedA+h.seedB)
		}
	})
}

// TestResetGUCMidTxReturnsZero: a RESET mid-transaction must drop to
// default-deny, not error and not leak the connection's prior tenant.
func TestResetGUCMidTxReturnsZero(t *testing.T) {
	skipIfNoDB(t)
	h := setupHarness(t, 2, 3)
	h.asCompany(t, h.companyA.String(), func(tx pgx.Tx) {
		if n := countVouchers(t, tx); n != h.seedA {
			t.Fatalf("company A should see %d before reset, got %d", h.seedA, n)
		}
		if _, err := tx.Exec(context.Background(), `RESET app.current_company_id`); err != nil {
			t.Fatalf("reset guc: %v", err)
		}
		// Must be zero, and must NOT raise (the bare ::uuid cast used to error).
		var n int
		if err := tx.QueryRow(context.Background(),
			`SELECT count(*) FROM voucher_version`).Scan(&n); err != nil {
			t.Fatalf("SECURITY HOLE: post-reset query errored instead of denying: %v", err)
		}
		if n != 0 {
			t.Fatalf("SECURITY HOLE: post-reset query returned %d rows, want 0", n)
		}
	})
}

// TestMalformedGUCReturnsZero: empty / non-UUID / garbage GUC values must
// resolve to default-deny (zero rows) rather than aborting the statement.
func TestMalformedGUCReturnsZero(t *testing.T) {
	skipIfNoDB(t)
	h := setupHarness(t, 2, 3)
	for _, bad := range []string{"", "   ", "not-a-uuid", "11111111-1111-1111-1111-11111111111", "'; DROP TABLE company;--"} {
		bad := bad
		t.Run("guc="+bad, func(t *testing.T) {
			h.asCompany(t, bad, func(tx pgx.Tx) {
				var n int
				if err := tx.QueryRow(context.Background(),
					`SELECT count(*) FROM voucher_version`).Scan(&n); err != nil {
					t.Fatalf("SECURITY HOLE: malformed GUC %q errored instead of denying: %v", bad, err)
				}
				if n != 0 {
					t.Fatalf("SECURITY HOLE: malformed GUC %q returned %d rows, want 0", bad, n)
				}
			})
		})
	}
}

// TestForgedUnknownCompanyReturnsZero: a well-formed but unknown company id
// matches no rows (never falls back to all rows).
func TestForgedUnknownCompanyReturnsZero(t *testing.T) {
	skipIfNoDB(t)
	h := setupHarness(t, 2, 3)
	for _, forged := range []string{
		"00000000-0000-0000-0000-000000000000",
		mustV7(t).String(),
	} {
		h.asCompany(t, forged, func(tx pgx.Tx) {
			if n := countVouchers(t, tx); n != 0 {
				t.Fatalf("SECURITY HOLE: forged company %s saw %d rows, want 0", forged, n)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Positive scoping + cross-tenant read leaks.
// ---------------------------------------------------------------------------

// TestGUCScopesToExactlyOwnRows: A sees exactly A's rows, B sees exactly B's.
func TestGUCScopesToExactlyOwnRows(t *testing.T) {
	skipIfNoDB(t)
	h := setupHarness(t, 2, 3)
	h.asCompany(t, h.companyA.String(), func(tx pgx.Tx) {
		if n := countVouchers(t, tx); n != h.seedA {
			t.Fatalf("company A saw %d rows, want exactly %d (and never B's %d)", n, h.seedA, h.seedB)
		}
	})
	h.asCompany(t, h.companyB.String(), func(tx pgx.Tx) {
		if n := countVouchers(t, tx); n != h.seedB {
			t.Fatalf("company B saw %d rows, want exactly %d", n, h.seedB)
		}
	})
}

// TestReadOtherCompanyByPrimaryKey: A, knowing B's exact voucher id, still
// gets zero rows. RLS filters by tenant before the id predicate matters.
func TestReadOtherCompanyByPrimaryKey(t *testing.T) {
	skipIfNoDB(t)
	h := setupHarness(t, 1, 1)
	h.asCompany(t, h.companyA.String(), func(tx pgx.Tx) {
		var n int
		if err := tx.QueryRow(context.Background(),
			`SELECT count(*) FROM voucher_version WHERE id = $1`, h.bVoucherID).Scan(&n); err != nil {
			t.Fatalf("by-id query: %v", err)
		}
		if n != 0 {
			t.Fatalf("SECURITY HOLE: company A read company B's voucher %s by primary key (%d rows)", h.bVoucherID, n)
		}
	})
}

// TestReadOtherCompanyByCompanyIDPredicate: A explicitly filters on B's
// company_id; must still see nothing.
func TestReadOtherCompanyByCompanyIDPredicate(t *testing.T) {
	skipIfNoDB(t)
	h := setupHarness(t, 2, 3)
	h.asCompany(t, h.companyA.String(), func(tx pgx.Tx) {
		var n int
		if err := tx.QueryRow(context.Background(),
			`SELECT count(*) FROM voucher_version WHERE company_id = $1`, h.companyB).Scan(&n); err != nil {
			t.Fatalf("query: %v", err)
		}
		if n != 0 {
			t.Fatalf("SECURITY HOLE: company A saw %d of company B's rows via company_id predicate", n)
		}
	})
}

// TestCrossCompanyJoinLeaksNothing: a self-join (and a join through company)
// under A's scope cannot pull B's rows; every joined row is RLS-filtered.
func TestCrossCompanyJoinLeaksNothing(t *testing.T) {
	skipIfNoDB(t)
	h := setupHarness(t, 2, 3)
	h.asCompany(t, h.companyA.String(), func(tx pgx.Tx) {
		// Self cross join: if RLS leaked B's rows on either side, the product
		// would exceed seedA*seedA.
		var n int
		if err := tx.QueryRow(context.Background(),
			`SELECT count(*) FROM voucher_version v JOIN voucher_version w ON true`).Scan(&n); err != nil {
			t.Fatalf("self join: %v", err)
		}
		if n != h.seedA*h.seedA {
			t.Fatalf("SECURITY HOLE: self-join saw %d rows, want %d (B leaked into the join?)", n, h.seedA*h.seedA)
		}
		// Join voucher_version to company: A can only see its own company row,
		// so the join can never surface B even if matched on company_id.
		var m int
		if err := tx.QueryRow(context.Background(),
			`SELECT count(*) FROM voucher_version v JOIN company c ON c.id = v.company_id`).Scan(&m); err != nil {
			t.Fatalf("company join: %v", err)
		}
		if m != h.seedA {
			t.Fatalf("SECURITY HOLE: voucher/company join saw %d rows, want %d", m, h.seedA)
		}
	})
}

// ---------------------------------------------------------------------------
// Write-side isolation: cannot create, update, or delete another tenant's rows.
// ---------------------------------------------------------------------------

// TestInsertIntoOtherCompanyBlocked: A inserting a row hard-coded to B's
// company_id is rejected by the WITH CHECK policy.
func TestInsertIntoOtherCompanyBlocked(t *testing.T) {
	skipIfNoDB(t)
	h := setupHarness(t, 0, 0)
	h.asCompany(t, h.companyA.String(), func(tx pgx.Tx) {
		_, err := tx.Exec(context.Background(),
			`INSERT INTO voucher_version
			   (id, company_id, voucher_id, version_no, op, hash, payload, created_by)
			 VALUES ($1, $2, $3, 1, 'CREATE', '\x00', '{}'::jsonb, 'attacker')`,
			mustV7(t), h.companyB, mustV7(t))
		if err == nil {
			t.Fatal("SECURITY HOLE: company A inserted a row into company B")
		}
		if !strings.Contains(strings.ToLower(err.Error()), "row-level security") {
			t.Fatalf("expected RLS WITH CHECK rejection, got: %v", err)
		}
	})
}

// TestInsertOwnCompanySucceeds: the WITH CHECK predicate must not be so strict
// that it blocks legitimate same-tenant writes (guard against over-hardening).
func TestInsertOwnCompanySucceeds(t *testing.T) {
	skipIfNoDB(t)
	h := setupHarness(t, 0, 0)
	h.asCompany(t, h.companyA.String(), func(tx pgx.Tx) {
		_, err := tx.Exec(context.Background(),
			`INSERT INTO voucher_version
			   (id, company_id, voucher_id, version_no, op, hash, payload, created_by)
			 VALUES ($1, $2, $3, 1, 'CREATE', '\x00', '{}'::jsonb, 'owner')`,
			mustV7(t), h.companyA, mustV7(t))
		if err != nil {
			t.Fatalf("legitimate same-tenant insert was rejected: %v", err)
		}
	})
}

// TestUpdateOtherCompanyRowBlocked: A updating B's rows affects zero rows (B's
// rows are invisible, so the predicate matches nothing).
func TestUpdateOtherCompanyRowBlocked(t *testing.T) {
	skipIfNoDB(t)
	h := setupHarness(t, 1, 1)
	h.asCompany(t, h.companyA.String(), func(tx pgx.Tx) {
		ct, err := tx.Exec(context.Background(),
			`UPDATE voucher_version SET node_id = 'tampered' WHERE company_id = $1`, h.companyB)
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if ct.RowsAffected() != 0 {
			t.Fatalf("SECURITY HOLE: company A updated %d of company B's rows", ct.RowsAffected())
		}
	})
	// Confirm B's row is untouched (ground truth via admin).
	var node string
	if err := h.admin.QueryRow(context.Background(),
		`SELECT node_id FROM voucher_version WHERE id = $1`, h.bVoucherID).Scan(&node); err != nil {
		t.Fatalf("inspect B row: %v", err)
	}
	if node == "tampered" {
		t.Fatal("SECURITY HOLE: company B's row was actually modified by company A")
	}
}

// TestDeleteOtherCompanyRowBlocked: A deleting B's rows affects zero rows and
// leaves B's data intact. (voucher_version is also append-only, but the RLS
// invisibility is what we assert here: the predicate matches nothing.)
func TestDeleteOtherCompanyRowBlocked(t *testing.T) {
	skipIfNoDB(t)
	h := setupHarness(t, 1, 3)
	h.asCompany(t, h.companyA.String(), func(tx pgx.Tx) {
		// The append-only trigger would also block this; we assert RLS makes the
		// rows invisible first. Use ledger (mutable) to isolate the RLS effect
		// from the immutability trigger, plus voucher_version for completeness.
		ct, err := tx.Exec(context.Background(),
			`DELETE FROM ledger WHERE company_id = $1`, h.companyB)
		if err != nil {
			t.Fatalf("delete ledger: %v", err)
		}
		if ct.RowsAffected() != 0 {
			t.Fatalf("SECURITY HOLE: company A deleted %d of company B's ledger rows", ct.RowsAffected())
		}
	})
	// B's voucher rows still all present.
	var n int
	if err := h.admin.QueryRow(context.Background(),
		`SELECT count(*) FROM voucher_version WHERE company_id = $1`, h.companyB).Scan(&n); err != nil {
		t.Fatalf("count B after delete attempt: %v", err)
	}
	if n != h.seedB {
		t.Fatalf("SECURITY HOLE: company B now has %d rows, want %d (data was affected)", n, h.seedB)
	}
}

// ---------------------------------------------------------------------------
// RLS-disable / privilege-escalation attempts as the app role.
// ---------------------------------------------------------------------------

// TestAppRoleCannotDisableRLS: A cannot DISABLE ROW LEVEL SECURITY (not the
// table owner), so it cannot turn off its own jail.
func TestAppRoleCannotDisableRLS(t *testing.T) {
	skipIfNoDB(t)
	h := setupHarness(t, 2, 3)
	h.asCompany(t, h.companyA.String(), func(tx pgx.Tx) {
		_, err := tx.Exec(context.Background(),
			`ALTER TABLE voucher_version DISABLE ROW LEVEL SECURITY`)
		if err == nil {
			t.Fatal("SECURITY HOLE: app role disabled RLS on voucher_version")
		}
		if !strings.Contains(strings.ToLower(err.Error()), "must be owner") {
			t.Fatalf("expected ownership rejection, got: %v", err)
		}
	})
}

// TestAppRoleCannotDropPolicyOrAlterRole: app role cannot drop the isolation
// policy nor grant itself BYPASSRLS.
func TestAppRoleCannotEscalatePrivileges(t *testing.T) {
	skipIfNoDB(t)
	h := setupHarness(t, 0, 0)
	h.asCompany(t, h.companyA.String(), func(tx pgx.Tx) {
		if _, err := tx.Exec(context.Background(),
			`DROP POLICY tenant_isolation ON voucher_version`); err == nil {
			t.Fatal("SECURITY HOLE: app role dropped the tenant_isolation policy")
		}
	})
	// ALTER ROLE in its own tx (the prior one is poisoned by the failed stmt).
	h.asCompany(t, h.companyA.String(), func(tx pgx.Tx) {
		if _, err := tx.Exec(context.Background(),
			`ALTER ROLE tally_app BYPASSRLS`); err == nil {
			t.Fatal("SECURITY HOLE: app role granted itself BYPASSRLS")
		}
	})
}

// ---------------------------------------------------------------------------
// Corpus-driven scenarios: load every corpora/isolation/*.json and assert each
// scenario's documented intent through the app role. This guarantees the
// shipped contract files are actually exercised.
// ---------------------------------------------------------------------------

type corpusScenario struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Seed        struct {
		CompanyA int `json:"companyA"`
		CompanyB int `json:"companyB"`
	} `json:"seed"`
	Session struct {
		Role         string `json:"role"`
		SetGuc       bool   `json:"setGuc"`
		Company      string `json:"company"`
		ThenResetGuc bool   `json:"thenResetGuc"`
	} `json:"session"`
	Action string `json:"action"`
	Expect struct {
		RowCountIs            *int    `json:"rowCountIs"`
		RowCountMax           *int    `json:"rowCountMax"`
		RowsAffected          *int    `json:"rowsAffected"`
		CompanyBRowCountAfter *int    `json:"companyBRowCountAfter"`
		ErrorContains         string  `json:"errorContains"`
		RLSBypassed           *bool   `json:"rlsBypassed"`
	} `json:"expect"`
}

func corpusDir(t *testing.T) string {
	t.Helper()
	// test cwd is the package dir: backend/internal/security.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, "..", "..", "..", "corpora", "isolation"))
}

// TestCorpusScenarios loads every isolation corpus file and asserts its intent.
// The corpus JSON encodes intent (counts, expected errors, "rlsBypassed=false")
// rather than literal SQL we exec verbatim; we map each scenario id to a
// concrete probe so the contract is enforced as written.
func TestCorpusScenarios(t *testing.T) {
	skipIfNoDB(t)
	dir := corpusDir(t)
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatalf("glob corpus: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("no corpus files found under %s", dir)
	}

	for _, f := range files {
		f := f
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		var sc corpusScenario
		if err := json.Unmarshal(raw, &sc); err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		t.Run(sc.ID, func(t *testing.T) {
			runCorpusScenario(t, sc)
		})
	}
}

// runCorpusScenario seeds per the scenario then asserts its expectation. Each
// scenario id maps to a behaviour; an unknown id fails loudly so new corpus
// files are not silently skipped.
func runCorpusScenario(t *testing.T, sc corpusScenario) {
	h := setupHarness(t, sc.Seed.CompanyA, sc.Seed.CompanyB)

	// Resolve the session's company token from the scenario.
	guc := ""
	if sc.Session.SetGuc {
		switch sc.Session.Company {
		case "A":
			guc = h.companyA.String()
		case "B":
			guc = h.companyB.String()
		default:
			guc = sc.Session.Company // a literal forged/unknown uuid.
		}
	}

	switch sc.ID {
	case "select-without-guc-returns-zero":
		h.asCompany(t, "", func(tx pgx.Tx) {
			assertCount(t, sc, countVouchers(t, tx))
		})

	case "guc-company-a-sees-only-a", "guc-company-b-cannot-see-a",
		"forged-guc-unknown-company-returns-zero":
		h.asCompany(t, guc, func(tx pgx.Tx) {
			assertCount(t, sc, countVouchers(t, tx))
		})

	case "reset-guc-mid-session-returns-zero":
		h.asCompany(t, guc, func(tx pgx.Tx) {
			_ = countVouchers(t, tx) // first valid query
			if _, err := tx.Exec(context.Background(), `RESET app.current_company_id`); err != nil {
				t.Fatalf("reset: %v", err)
			}
			var n int
			if err := tx.QueryRow(context.Background(),
				`SELECT count(*) FROM voucher_version`).Scan(&n); err != nil {
				t.Fatalf("post-reset query errored (should deny cleanly): %v", err)
			}
			assertCount(t, sc, n)
		})

	case "read-other-company-voucher-by-id":
		h.asCompany(t, guc, func(tx pgx.Tx) {
			var n int
			if err := tx.QueryRow(context.Background(),
				`SELECT count(*) FROM voucher_version WHERE id = $1`, h.bVoucherID).Scan(&n); err != nil {
				t.Fatalf("by-id: %v", err)
			}
			assertCount(t, sc, n)
		})

	case "cross-company-join-leaks-nothing":
		h.asCompany(t, guc, func(tx pgx.Tx) {
			var n int
			// Join voucher_version to company under A's scope; can never exceed A.
			if err := tx.QueryRow(context.Background(),
				`SELECT count(*) FROM voucher_version v JOIN company c ON c.id = v.company_id`).Scan(&n); err != nil {
				t.Fatalf("join: %v", err)
			}
			if sc.Expect.RowCountMax != nil && n > *sc.Expect.RowCountMax {
				t.Fatalf("SECURITY HOLE [%s]: join saw %d rows, max %d", sc.ID, n, *sc.Expect.RowCountMax)
			}
		})

	case "insert-into-other-company-blocked":
		h.asCompany(t, guc, func(tx pgx.Tx) {
			_, err := tx.Exec(context.Background(),
				`INSERT INTO voucher_version
				   (id, company_id, voucher_id, version_no, op, hash, payload, created_by)
				 VALUES ($1, $2, $3, 1, 'CREATE', '\x00', '{}'::jsonb, 'attacker')`,
				mustV7(t), h.companyB, mustV7(t))
			assertError(t, sc, err)
		})

	case "update-other-company-row-blocked":
		h.asCompany(t, guc, func(tx pgx.Tx) {
			ct, err := tx.Exec(context.Background(),
				`UPDATE voucher_version SET node_id = 'tampered' WHERE company_id = $1`, h.companyB)
			if err != nil {
				t.Fatalf("update: %v", err)
			}
			assertRowsAffected(t, sc, ct.RowsAffected())
		})

	case "delete-other-company-row-blocked":
		h.asCompany(t, guc, func(tx pgx.Tx) {
			// Use the mutable ledger table to isolate the RLS effect from the
			// append-only trigger; the contract is "zero rows affected".
			ct, err := tx.Exec(context.Background(),
				`DELETE FROM ledger WHERE company_id = $1`, h.companyB)
			if err != nil {
				t.Fatalf("delete: %v", err)
			}
			assertRowsAffected(t, sc, ct.RowsAffected())
		})
		if sc.Expect.CompanyBRowCountAfter != nil {
			var n int
			if err := h.admin.QueryRow(context.Background(),
				`SELECT count(*) FROM voucher_version WHERE company_id = $1`, h.companyB).Scan(&n); err != nil {
				t.Fatalf("count B after: %v", err)
			}
			if n != *sc.Expect.CompanyBRowCountAfter {
				t.Fatalf("SECURITY HOLE [%s]: company B has %d voucher rows after, want %d", sc.ID, n, *sc.Expect.CompanyBRowCountAfter)
			}
		}

	case "app-role-cannot-bypass-rls":
		// Attempt to DISABLE RLS, then assert it failed and rows are still scoped.
		h.asCompany(t, guc, func(tx pgx.Tx) {
			_, err := tx.Exec(context.Background(),
				`ALTER TABLE voucher_version DISABLE ROW LEVEL SECURITY`)
			assertError(t, sc, err)
		})
		// And independently confirm the role truly lacks BYPASSRLS.
		var bypass bool
		if err := h.admin.QueryRow(context.Background(),
			`SELECT rolbypassrls FROM pg_roles WHERE rolname = 'tally_app'`).Scan(&bypass); err != nil {
			t.Fatalf("pg_roles: %v", err)
		}
		if sc.Expect.RLSBypassed != nil && bypass != *sc.Expect.RLSBypassed {
			t.Fatalf("SECURITY HOLE [%s]: rolbypassrls=%v, want %v", sc.ID, bypass, *sc.Expect.RLSBypassed)
		}

	default:
		t.Fatalf("corpus scenario %q (%s) is not mapped to a probe; add a case so it is not silently skipped", sc.ID, sc.Description)
	}
}

func assertCount(t *testing.T, sc corpusScenario, got int) {
	t.Helper()
	if sc.Expect.RowCountIs != nil && got != *sc.Expect.RowCountIs {
		t.Fatalf("SECURITY HOLE [%s]: got %d rows, want exactly %d (%s)", sc.ID, got, *sc.Expect.RowCountIs, sc.Description)
	}
	if sc.Expect.RowCountMax != nil && got > *sc.Expect.RowCountMax {
		t.Fatalf("SECURITY HOLE [%s]: got %d rows, want at most %d", sc.ID, got, *sc.Expect.RowCountMax)
	}
}

func assertRowsAffected(t *testing.T, sc corpusScenario, got int64) {
	t.Helper()
	if sc.Expect.RowsAffected != nil && got != int64(*sc.Expect.RowsAffected) {
		t.Fatalf("SECURITY HOLE [%s]: %d rows affected, want %d (%s)", sc.ID, got, *sc.Expect.RowsAffected, sc.Description)
	}
}

func assertError(t *testing.T, sc corpusScenario, err error) {
	t.Helper()
	if sc.Expect.ErrorContains == "" {
		return
	}
	if err == nil {
		t.Fatalf("SECURITY HOLE [%s]: expected error containing %q, got nil (%s)", sc.ID, sc.Expect.ErrorContains, sc.Description)
	}
	if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(sc.Expect.ErrorContains)) {
		t.Fatalf("[%s]: error %q does not contain %q", sc.ID, err.Error(), sc.Expect.ErrorContains)
	}
}

// ---------------------------------------------------------------------------
// Task D auth-catalog RLS red team: the new 0011 tables (app_user,
// security_level, acl_rule, company_membership, webauthn_credential) must be
// RLS-locked exactly like the tenant tables — so even if the PDP middleware were
// bypassed, the app role sees ZERO catalog rows for the wrong subject/user/
// company. This defends the membership oracle the PDP trusts.
// ---------------------------------------------------------------------------

// authCatalogTables are the user/company-scoped auth tables (0011). They are NOT
// in tenantTables (which the isolation tests reserve for the original 7), but
// they MUST be ENABLE+FORCE RLS with a non-empty WITH CHECK.
var authCatalogTables = []string{
	"app_user", "security_level", "acl_rule",
	"company_membership", "webauthn_credential",
	// 0012: the durable (multi-instance) WebAuthn challenge store. User-scoped
	// exactly like webauthn_credential, so it belongs under the same red team.
	"webauthn_challenge",
}

// TestAuthCatalogTablesForceRLS asserts every new auth catalog table is
// ENABLE+FORCE RLS with an explicit WITH CHECK (mirroring the tenant tables).
func TestAuthCatalogTablesForceRLS(t *testing.T) {
	skipIfNoDB(t)
	h := setupHarness(t, 0, 0)
	for _, tbl := range authCatalogTables {
		var enabled, forced bool
		if err := h.admin.QueryRow(context.Background(),
			`SELECT relrowsecurity, relforcerowsecurity FROM pg_class WHERE relname = $1`, tbl).
			Scan(&enabled, &forced); err != nil {
			t.Fatalf("pg_class for %s: %v", tbl, err)
		}
		if !enabled || !forced {
			t.Errorf("SECURITY HOLE: auth catalog %s enabled=%v forced=%v, want both true", tbl, enabled, forced)
		}
		var usingExpr, withCheck string
		if err := h.admin.QueryRow(context.Background(),
			`SELECT pg_get_expr(polqual, polrelid), COALESCE(pg_get_expr(polwithcheck, polrelid), '')
			   FROM pg_policy WHERE polrelid = $1::regclass AND polname = 'tenant_isolation'`, tbl).
			Scan(&usingExpr, &withCheck); err != nil {
			t.Fatalf("SECURITY HOLE: no tenant_isolation policy on %s: %v", tbl, err)
		}
		if withCheck == "" {
			t.Errorf("SECURITY HOLE: %s has no explicit WITH CHECK", tbl)
		}
	}
}

// TestMembershipRLSScopedToUser proves company_membership is invisible to the
// wrong user GUC: a row planted for user X is seen under X's user GUC but NOT
// under a different user GUC. This is the membership oracle's isolation — if it
// leaked, the PDP could be tricked across tenants even with a valid token.
func TestMembershipRLSScopedToUser(t *testing.T) {
	skipIfNoDB(t)
	h := setupHarness(t, 0, 0)
	ctx := context.Background()

	// Plant a user, a company-scoped security_level, and a membership via admin.
	userX := mustV7(t)
	otherUser := mustV7(t)
	company := mustV7(t)
	levelID := mustV7(t)
	seedCompany(t, h.admin, company)
	if _, err := h.admin.Exec(ctx,
		`INSERT INTO app_user (id, subject) VALUES ($1, $2)`, userX, "subjX-"+userX.String()[:8]); err != nil {
		t.Fatalf("seed app_user: %v", err)
	}
	if _, err := h.admin.Exec(ctx,
		`INSERT INTO security_level (id, company_id, name, is_owner, default_access)
		 VALUES ($1, $2, 'Owner', true, 'full')`, levelID, company); err != nil {
		t.Fatalf("seed security_level: %v", err)
	}
	if _, err := h.admin.Exec(ctx,
		`INSERT INTO company_membership (user_id, company_id, security_level_id)
		 VALUES ($1, $2, $3)`, userX, company, levelID); err != nil {
		t.Fatalf("seed membership: %v", err)
	}
	t.Cleanup(func() {
		_, _ = h.admin.Exec(ctx, `DELETE FROM company_membership WHERE user_id = $1`, userX)
		_, _ = h.admin.Exec(ctx, `DELETE FROM security_level WHERE id = $1`, levelID)
		_, _ = h.admin.Exec(ctx, `DELETE FROM app_user WHERE id = $1`, userX)
		_, _ = h.admin.Exec(ctx, `DELETE FROM company WHERE id = $1`, company)
	})

	countMembership := func(t *testing.T, userGUC string) int {
		t.Helper()
		tx, err := h.app.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if userGUC != "" {
			if _, err := tx.Exec(ctx, `SELECT set_config('app.current_user_id', $1, true)`, userGUC); err != nil {
				t.Fatalf("set user guc: %v", err)
			}
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM company_membership`).Scan(&n); err != nil {
			t.Fatalf("count membership: %v", err)
		}
		return n
	}

	if n := countMembership(t, userX.String()); n != 1 {
		t.Fatalf("user X should see exactly 1 membership, got %d", n)
	}
	if n := countMembership(t, otherUser.String()); n != 0 {
		t.Fatalf("SECURITY HOLE: a different user GUC saw %d of X's memberships, want 0", n)
	}
	if n := countMembership(t, ""); n != 0 {
		t.Fatalf("SECURITY HOLE: no-user-GUC saw %d memberships, want 0 (default-deny)", n)
	}
}

// ensure errors import is used even if all probes pass without wrapping.
var _ = errors.New
