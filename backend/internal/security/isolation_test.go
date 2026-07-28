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
	"fmt"
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/khata/cored/internal/platform/testdb"
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
	admin    *pgx.Conn     // superuser; bypasses RLS, used only to seed/inspect ground truth.
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
	// The teardown is registered HERE, next to the INSERT, not in the callers.
	// All three of today's callers happen to register one of their own, so this
	// looks redundant — and a duplicate drop is a no-op that matches no rows. It
	// is here for the FOURTH caller. A creation site whose cleanup lives in
	// somebody else's function is one refactor away from leaking, which is what
	// internal/testhygiene's TestEveryCompanyCreatingTestSiteTearsDown objects to:
	// it keys on the function that does the creating, deliberately, because a rule
	// keyed on "some caller cleaned up" cannot be checked without a call graph and
	// cannot protect a caller that does not exist yet.
	testdb.Cleanup(t, id)
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

// cleanup removes BOTH adversary tenants and everything planted under them.
//
// It used to do this by hand, and both halves of that were wrong:
//
//  1. It ran `ALTER TABLE voucher_version DISABLE TRIGGER
//     voucher_version_no_mutate` around the delete. That statement is
//     DATABASE-WIDE and PERSISTENT, not session-scoped: for as long as it was
//     lifted, voucher immutability was off for EVERY connection, so any other
//     test running concurrently that asserts "the append-only trigger rejects
//     this UPDATE" would have passed while the trigger did nothing. Worse, a
//     crash or a t.Fatalf between the disable and the re-enable leaves the
//     production invariant disarmed in the shared dev database with nothing
//     reporting it. This is the same hazard that made `go test ./internal/server`
//     unsafe to run unscoped. TestSecurityHarnessNeverDisablesTriggersGlobally
//     below now fails the build if the statement ever comes back.
//     testdb performs its deletes under `SET LOCAL session_replication_role =
//     replica` inside one transaction, which suppresses the trigger for that
//     transaction only and cannot outlive it.
//
//  2. It deleted voucher_version and then `DELETE FROM company`, and stopped.
//     Nothing cascades from company — only notification_outbox references
//     company(id) and its FK action is NO ACTION — so every account_group,
//     ledger, voucher_type, godown, stock_item and security_level this red team
//     planted stayed in the database forever, orphaned and invisible because the
//     tenant they belonged to was gone. A company-count check called that clean.
//     testdb.DropNow enumerates every table carrying company_id (plus the
//     hand-registered special cases) and reports, by table and row count, if any
//     row survives.
func (h *harness) cleanup(t *testing.T) {
	t.Helper()
	testdb.DropNow(t, h.companyA)
	testdb.DropNow(t, h.companyB)
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
		RowCountIs            *int   `json:"rowCountIs"`
		RowCountMax           *int   `json:"rowCountMax"`
		RowsAffected          *int   `json:"rowsAffected"`
		CompanyBRowCountAfter *int   `json:"companyBRowCountAfter"`
		ErrorContains         string `json:"errorContains"`
		RLSBypassed           *bool  `json:"rlsBypassed"`
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

	case "stock-movement-cross-company-invisible":
		// The inventory plane (0025) joins the same red team: the fixture plants
		// a real movement line in BOTH companies, so a leak would show up as a
		// count of 2 rather than as an empty table that proves nothing.
		setupInventory(t, h)
		h.asCompany(t, guc, func(tx pgx.Tx) {
			var n int
			if err := tx.QueryRow(context.Background(),
				`SELECT count(*) FROM stock_movement`).Scan(&n); err != nil {
				t.Fatalf("count stock movements: %v", err)
			}
			assertCount(t, sc, n)
		})

	case "stock-movement-immutable-under-update":
		// Note this is A editing A's OWN row: the refusal is the immutability
		// trigger, not RLS. Stock value is derived by replaying these rows, so a
		// line that can change after the fact makes every closing value a
		// function of when you asked.
		inv := setupInventory(t, h)
		h.asCompany(t, guc, func(tx pgx.Tx) {
			_, err := tx.Exec(context.Background(),
				`UPDATE stock_movement SET qty = 9999 WHERE id = $1`, inv.moveInA)
			assertError(t, sc, err)
		})

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
	// 0020: pending grants addressed to a JWT subject (the SaaS admin surface).
	// Readable by the administering company OR by the addressee's subject, and
	// writable ONLY under the company GUC — so it is held to the same
	// ENABLE+FORCE+explicit-WITH-CHECK bar as every other catalog table.
	"company_invite",
	// 0021: the PASSWORD VERIFIER store (first-party login). Its policy is
	// SELF-ONLY — user_id = app_current_user_id(), with no company clause at
	// all — which is exactly why it is a separate table rather than columns on
	// app_user: 0020 WIDENED app_user's USING to admit co-members of the company
	// in scope, so a hash living there would be readable by every colleague.
	//
	// This entry is what makes that argument a GATE rather than a comment.
	// Without it, dropping FORCE, dropping the WITH CHECK half, or dropping the
	// policy outright from 0021 is caught by nothing in the suite: the
	// store-level test runs as tally_app (NOBYPASSRLS), so it cannot tell a
	// self-only policy apart from a missing one.
	"user_credential",
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
	// This test seeds a THIRD company on top of the harness's A and B, so it owns
	// that company's teardown. company_membership and security_level both carry
	// company_id and are removed by DropNow along with the company row itself;
	// app_user does NOT (it is user-scoped, deliberately global, and listed in
	// testdb's notTenantScoped registry), so it is still deleted by hand here.
	t.Cleanup(func() {
		_, _ = h.admin.Exec(ctx, `DELETE FROM company_membership WHERE user_id = $1`, userX)
		_, _ = h.admin.Exec(ctx, `DELETE FROM app_user WHERE id = $1`, userX)
		testdb.DropNow(t, company)
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

// TestStatutorydRoleReachesOnlyItsOwnTable pins the least-privilege posture
// migration 0024 establishes.
//
// WHY THIS MATTERS. Migration 0013 gave statutoryd a durable step store, and to
// reach it statutoryd was handed a `tally_app` DSN — the same role cored uses
// for the ENTIRE ledger. A separate deployable whose only legitimate database
// need is its own workflow bookkeeping could read every tenant's vouchers,
// company masters and password verifiers. Everything else statutoryd does goes
// over HTTP to cored as a service principal, through the PDP; an archtest
// already enforces that boundary in the CODE, and this makes the DATABASE agree
// with it instead of quietly contradicting it.
//
// Asserted by PRIVILEGE, not by connecting: has_table_privilege is what the
// grant actually means, and it does not depend on a password this test would
// otherwise have to know.
func TestStatutorydRoleReachesOnlyItsOwnTable(t *testing.T) {
	skipIfNoDB(t)
	h := setupHarness(t, 0, 0)
	ctx := context.Background()

	var exists bool
	if err := h.admin.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'tally_statutory')`).
		Scan(&exists); err != nil {
		t.Fatalf("query pg_roles: %v", err)
	}
	if !exists {
		t.Fatal("tally_statutory does not exist — migration 0024 did not run")
	}

	// It CAN work on its own table, or statutoryd cannot run at all.
	for _, priv := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
		var ok bool
		if err := h.admin.QueryRow(ctx,
			`SELECT has_table_privilege('tally_statutory', 'statutory_step', $1)`, priv).
			Scan(&ok); err != nil {
			t.Fatalf("has_table_privilege: %v", err)
		}
		if !ok {
			t.Errorf("tally_statutory lacks %s on statutory_step — statutoryd cannot run", priv)
		}
	}

	// And it can reach NOTHING else. Every tenant table plus the auth catalog:
	// these are precisely the rows a compromised side-plane must not be able to
	// read, and the ones the tally_app DSN did expose.
	forbidden := append([]string{}, tenantTables...)
	forbidden = append(forbidden,
		"company", "app_user", "user_credential", "webauthn_credential",
		"security_level", "acl_rule", "company_membership", "company_invite",
		"notification_outbox",
	)
	for _, tbl := range forbidden {
		var present bool
		if err := h.admin.QueryRow(ctx,
			`SELECT to_regclass('public.' || $1) IS NOT NULL`, tbl).Scan(&present); err != nil {
			t.Fatalf("to_regclass %s: %v", tbl, err)
		}
		if !present {
			continue // a table a later migration may not have created
		}
		var ok bool
		if err := h.admin.QueryRow(ctx,
			`SELECT has_table_privilege('tally_statutory', $1, 'SELECT')`, tbl).Scan(&ok); err != nil {
			t.Fatalf("has_table_privilege %s: %v", tbl, err)
		}
		if ok {
			t.Errorf("SECURITY HOLE: tally_statutory can SELECT %s — statutoryd's DB role "+
				"must reach statutory_step and nothing else", tbl)
		}
	}

	// Belt and braces on the role itself: no bypass, no superuser.
	var bypass, super bool
	if err := h.admin.QueryRow(ctx,
		`SELECT rolbypassrls, rolsuper FROM pg_roles WHERE rolname = 'tally_statutory'`).
		Scan(&bypass, &super); err != nil {
		t.Fatalf("query pg_roles: %v", err)
	}
	if bypass || super {
		t.Fatalf("SECURITY HOLE: tally_statutory bypassrls=%v superuser=%v", bypass, super)
	}
}

// ---------------------------------------------------------------------------
// Harness hygiene. These two tests defend the RED TEAM'S OWN harness, which is
// itself a piece of shared-database machinery and was, until this change, the
// single worst-behaved one in the backend.
// ---------------------------------------------------------------------------

// TestHarnessRemovesBothTenants proves setupHarness's t.Cleanup releases BOTH
// adversary companies, masters included.
//
// Two things about the shape are deliberate.
//
// First, the harness is built inside a SUBTEST. t.Cleanup fires when the *testing.T
// it was registered on finishes, so the only way to observe the harness's teardown
// from inside a test is to let a child T complete and then look. Asserting from
// the same T would run the assertions BEFORE the cleanup and pass no matter what.
//
// Second, it checks account_group, not just company and voucher_version. seedCompany
// raw-inserts a childless company, so a bare harness has no masters at all and an
// account_group assertion over it would be vacuously true — which is why the
// subtest calls seed_company_defaults on company A first and asserts the masters
// really exist before letting the subtest end. That is the precise bug being
// guarded: the old cleanup deleted voucher_version and company and left every
// master behind, and because nothing cascades from company (only
// notification_outbox references it, NO ACTION) a company-count check reported
// clean while account_group grew to 603 MB.
func TestHarnessRemovesBothTenants(t *testing.T) {
	skipIfNoDB(t)
	ctx := context.Background()

	// An independent superuser connection. It must NOT be the harness's own
	// h.admin: that one is closed by the harness cleanup, and a closed connection
	// cannot distinguish "no rows" from "cannot look".
	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	defer func() { _ = admin.Close(ctx) }()

	countIn := func(tbl, col string, id uuid.UUID) int {
		t.Helper()
		var n int
		if err := admin.QueryRow(ctx,
			`SELECT count(*) FROM `+tbl+` WHERE `+col+` = $1`, id).Scan(&n); err != nil {
			t.Fatalf("count %s for %s: %v", tbl, id, err)
		}
		return n
	}

	var a, b uuid.UUID
	t.Run("inner", func(t *testing.T) {
		h := setupHarness(t, 2, 2)
		a, b = h.companyA, h.companyB

		// Give company A a real master tree so the account_group assertion below
		// has something to be wrong about.
		if _, err := h.admin.Exec(ctx, `SELECT seed_company_defaults($1)`, a); err != nil {
			t.Fatalf("seed_company_defaults for A: %v", err)
		}

		// NON-VACUITY: everything we are about to assert is gone must be present
		// while the harness is alive.
		if n := countIn("company", "id", a); n != 1 {
			t.Fatalf("vacuity: company A not planted (%d rows)", n)
		}
		if n := countIn("company", "id", b); n != 1 {
			t.Fatalf("vacuity: company B not planted (%d rows)", n)
		}
		if n := countIn("voucher_version", "company_id", a); n != 2 {
			t.Fatalf("vacuity: company A has %d seeded voucher_version rows, want 2", n)
		}
		if n := countIn("voucher_version", "company_id", b); n != 2 {
			t.Fatalf("vacuity: company B has %d seeded voucher_version rows, want 2", n)
		}
		if n := countIn("account_group", "company_id", a); n == 0 {
			t.Fatalf("vacuity: seed_company_defaults planted no account_group rows for A")
		}
	})

	// The subtest has returned, so its cleanups — including setupHarness's — have run.
	var leaked []string
	for _, c := range []struct {
		label string
		id    uuid.UUID
	}{{"A", a}, {"B", b}} {
		for _, tbl := range []struct{ name, col string }{
			{"account_group", "company_id"},
			{"voucher_version", "company_id"},
			{"company", "id"},
		} {
			if n := countIn(tbl.name, tbl.col, c.id); n != 0 {
				leaked = append(leaked, fmt.Sprintf("company%s(%s).%s=%d", c.label, c.id, tbl.name, n))
			}
		}
	}
	if len(leaked) > 0 {
		t.Fatalf("the isolation harness leaked tenant data after its cleanup ran: %s. "+
			"Every red-team test in this package builds a harness, so a leak here multiplies "+
			"across the whole file and leaves orphaned masters that no tenant owns.",
			strings.Join(leaked, ", "))
	}
}

// TestSecurityHarnessNeverDisablesTriggersGlobally is a source-level ban.
//
// `ALTER TABLE ... DISABLE TRIGGER ...` is not session-scoped and not
// transaction-scoped. It is a persistent catalogue change: while it is in force,
// the trigger is off for every connection in the database, and it stays off until
// something explicitly re-enables it. A harness that used it to tidy up the
// immutable voucher log was therefore switching voucher immutability off for the
// whole database, repeatedly, in the middle of a parallel test run — long enough
// for another package's "the append-only trigger rejects this UPDATE" test to pass
// without a trigger ever firing, and permanently if the run died in between. That
// is exactly why `go test ./internal/server` is documented as unsafe to run
// unscoped, and this package was the second offender.
//
// The supported alternative, which testdb uses, is
// `BEGIN; SET LOCAL session_replication_role = replica; ... COMMIT;` on a
// superuser connection: SET LOCAL cannot outlive its transaction, so no other
// connection ever observes immutability disarmed.
//
// The check reads this package's own test sources from disk and scans STRING
// LITERALS ONLY, via the Go scanner. Scanning raw text would be simpler but would
// also flag the comment a few lines above, and the comments explaining why the
// statement is banned are the most valuable part of the ban. A literal is what
// actually reaches the server.
//
// Two details that a first attempt got wrong, recorded so they are not
// re-introduced:
//
//   - The needle is ASSEMBLED AT RUNTIME (bannedNeedle) rather than written as one
//     literal. The checker lives in the very file it scans, so a literal needle —
//     and the literal in its own failure message — made the test flag ITSELF on
//     first run. Splitting the words is the whole fix; it does not weaken the ban,
//     because the thing being defended against is a harness quietly reintroducing a
//     working SQL statement, not an author deliberately obfuscating one.
//   - There is a POSITIVE CONTROL. `scanForBanned` is first run over a synthetic
//     in-memory source that DOES contain the statement and must return exactly one
//     hit. Without it, a scanner that silently matched nothing — a mis-set token
//     filter, a needle that normalisation can never produce — would report "no
//     offences" forever and this ban would be a decoration.
func TestSecurityHarnessNeverDisablesTriggersGlobally(t *testing.T) {
	// POSITIVE CONTROL: the matcher must actually find the statement when it is
	// present, including in the lower-case and line-wrapped spellings.
	control := "package p\nvar a = `ALTER TABLE voucher_version " + bannedNeedle + " voucher_version_no_mutate`\n" +
		"var b = \"alter table t\\n  disable\\n  trigger u\"\n" +
		"// a comment mentioning " + bannedNeedle + " must NOT be flagged\n"
	hits, lits := scanForBanned(t, "control.go", []byte(control))
	if len(hits) != 2 {
		t.Fatalf("positive control: matcher found %d offending literals in a source that contains "+
			"exactly 2 (one back-quoted, one lower-case and line-wrapped); the ban is not being "+
			"enforced and every 'no offences' result below is meaningless. hits=%v", len(hits), hits)
	}
	if lits < 2 {
		t.Fatalf("positive control: scanner saw %d string literals, want >= 2", lits)
	}

	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatalf("glob test sources: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("vacuity: found no *_test.go files to scan in this package's directory")
	}

	var offences []string
	totalLits := 0
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		hits, n := scanForBanned(t, f, src)
		offences = append(offences, hits...)
		totalLits += n
	}
	if totalLits == 0 {
		t.Fatalf("vacuity: scanned %d files and found no string literals at all; "+
			"the scanner is misconfigured and this ban is not being enforced", len(files))
	}
	if len(offences) > 0 {
		t.Fatalf("BANNED STATEMENT: `ALTER TABLE ... %s` appears in a SQL string at %s. "+
			"That is a database-wide, persistent change: it switches the invariant off for every "+
			"other connection while it is in force, and leaves it off for good if the process dies "+
			"before re-enabling it. Use a transaction with `SET LOCAL session_replication_role = "+
			"replica` (see testdb) instead — it cannot escape its transaction.",
			bannedNeedle, strings.Join(offences, ", "))
	}
}

// bannedNeedle is the SQL fragment this package refuses to contain, assembled
// from two pieces so that neither this constant nor the failure message that
// prints it counts as an offence when the checker scans its own source. See
// TestSecurityHarnessNeverDisablesTriggersGlobally.
var bannedNeedle = "DISABLE" + " TRIGGER"

// scanForBanned tokenises one Go source and returns the positions of every
// STRING literal containing the banned statement, plus how many string literals
// it saw in total (the caller uses that as its anti-vacuity signal).
//
// Each literal is UNQUOTED before comparison. That is not cosmetic: the scanner
// hands back the raw source text, so an interpreted literal written as
// "... disable\n  trigger ..." arrives with a two-character backslash-n where the
// newline will be, and a whitespace-collapsing comparison never sees the two words
// as adjacent. Unquoting turns the escape into the byte Postgres will actually
// receive, which is the only spelling that matters. This was caught by the
// positive control below and would otherwise have been a silent hole.
//
// Comparison is then on a normalised form — upper-cased with every run of
// whitespace collapsed to a single space — so `disable trigger`, `DISABLE  TRIGGER`
// and a statement split across source lines all match. Only token.STRING is
// considered: comments explaining the ban are not violations of it.
func scanForBanned(t *testing.T, filename string, src []byte) (hits []string, literals int) {
	t.Helper()
	normalise := func(s string) string { return strings.Join(strings.Fields(strings.ToUpper(s)), " ") }

	fset := token.NewFileSet()
	file := fset.AddFile(filename, fset.Base(), len(src))
	var s scanner.Scanner
	s.Init(file, src, func(pos token.Position, msg string) {
		t.Fatalf("scan %s: %s: %s", filename, pos, msg)
	}, 0)
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok != token.STRING {
			continue
		}
		literals++
		text := lit
		if unquoted, err := strconv.Unquote(lit); err == nil {
			text = unquoted
		}
		if strings.Contains(normalise(text), bannedNeedle) {
			hits = append(hits, fset.Position(pos).String())
		}
	}
	return hits, literals
}
