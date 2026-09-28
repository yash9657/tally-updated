package payrolldb_test

// service_test.go — the shared harness, plus the AUTHORIZATION tests.
//
// The authorization half is here rather than in internal/authz because the
// thing under test is not the PDP (which has its own tests) but the rules
// MIGRATION 0035 SEEDS. The PDP is correct and the rules are what decide
// whether an operator can read a payroll register, so the assertion has to run
// against a company seeded by the real migration.

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/authz"
	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/payroll/payrolldb"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/migrations"
)

var (
	adminDSN = testdb.AdminDSNValue()
	appDSN   = testdb.AppDSNValue()
)

// setup migrates the live DB (idempotent) and returns an app-scoped pool plus a
// freshly created, seeded THROWAWAY company scoped into the returned context.
//
// A throwaway every time, and never Demo Traders or the tenancy company: those
// two are live fixtures the integrator's browser gates depend on, and a test
// that edited their ACL rules or their chart of accounts would surface as a
// flaky e2e run in somebody else's session.
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
	if err := migrations.Apply(ctx, adminConn); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("apply migrations: %v", err)
	}
	_ = adminConn.Close(ctx)

	pool, err := db.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}
	t.Cleanup(pool.Close)

	companyID, cctx := newCompany(t, pool)
	return pool, cctx, companyID
}

// newCompany creates one throwaway company on an existing pool and registers
// its teardown the moment it exists.
//
// testdb.Cleanup is MANDATORY and internal/testhygiene is a static gate that
// fails the build if a company-creating test function does not reference
// `testdb.` — the payroll tables carry a real company_id column, so the
// schema-derived teardown plan picks all six of them up with no registry edit.
func newCompany(t *testing.T, pool *db.Pool) (uuid.UUID, context.Context) {
	t.Helper()
	companyID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new company id: %v", err)
	}
	cctx := db.WithCompany(context.Background(), companyID)
	ids := identity.NewService(pool)
	if _, err := ids.CreateCompany(cctx, identity.Company{
		ID:          companyID,
		Name:        "Payroll Co " + companyID.String()[:8],
		FYStartDate: "2026-04-01",
		StateCode:   "29",
	}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	testdb.Cleanup(t, companyID)
	return companyID, cctx
}

// newService builds the payroll service over the pool and the real ledger.
func newService(pool *db.Pool) *payrolldb.Service {
	return payrolldb.NewService(pool, ledger.NewService(pool))
}

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// ---------------------------------------------------------------------------
// Authorization: the rules migration 0035 seeds.
// ---------------------------------------------------------------------------

// dataEntryLevelAndRules reads the seeded Data Entry level and its ACL rules
// through the real authz store, for a company created and auth-seeded here.
func dataEntryLevelAndRules(t *testing.T, pool *db.Pool, companyID uuid.UUID) (authz.Level, []authz.Rule) {
	t.Helper()
	store := authz.NewStore(pool)
	userID, err := store.CreateUserOnFirstLogin(context.Background(),
		"payroll-acl-"+companyID.String(), "", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := store.SeedCompanyAuthDefaults(context.Background(), companyID, userID); err != nil {
		t.Fatalf("seed auth defaults: %v", err)
	}
	var levelID uuid.UUID
	if err := pool.RunInTx(db.WithCompany(context.Background(), companyID),
		func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx,
				`SELECT id FROM security_level WHERE name = 'Data Entry'`).Scan(&levelID)
		}); err != nil {
		t.Fatalf("read Data Entry level: %v", err)
	}
	lvl, err := store.Level(context.Background(), companyID, levelID)
	if err != nil {
		t.Fatalf("read level: %v", err)
	}
	rules, err := store.Rules(context.Background(), companyID, levelID)
	if err != nil {
		t.Fatalf("read rules: %v", err)
	}
	return lvl, rules
}

// TestDataEntryCannotReadPayrollReports drives the PDP against the level
// migration 0035 seeds and proves all five payroll reports are refused.
//
// THIS IS THE ONE THAT CHANGES AN ANSWER. Migration 0011 seeds the Data Entry
// level a COARSE `allow display report` that matches EVERY report:NAME, so
// WITHOUT 0035's explicit rows every payroll report — including the four
// statutory outputs carrying PAN, UAN and IP number per person — is readable by
// every operator in every tenant BY INHERITANCE, with nobody having decided it.
//
// The control assertion at the end is what makes the test meaningful: the Trial
// Balance must STILL be allowed. A blanket disallow at the coarse `report`
// grain would pass the first half of this test and take the whole reporting
// surface away from the level.
func TestDataEntryCannotReadPayrollReports(t *testing.T) {
	pool, _, companyID := setup(t)
	lvl, rules := dataEntryLevelAndRules(t, pool, companyID)

	for _, name := range []string{
		"payroll-register", "payroll-payslip", "payroll-form-138",
		"payroll-pf-ecr", "payroll-esi-return",
	} {
		d := authz.Evaluate(lvl, rules, authz.Request{
			CompanyID:    companyID,
			Action:       authz.Display,
			Resource:     "report",
			ResourceName: name,
		})
		if d.Allow {
			t.Errorf("Data Entry may display report:%s — migration 0035's explicit disallow is missing or out-scored, "+
				"so every operator in every tenant can read this payroll report by inheritance from 0011's coarse `allow display report` "+
				"(for the statutory outputs that means PAN, UAN and ESIC IP number, per person)", name)
		}
	}

	// The control: an ordinary report an operator legitimately works from is
	// still reachable. If this fails, the refusal above was written at the
	// coarse grain and cost the level its whole reporting surface.
	if d := authz.Evaluate(lvl, rules, authz.Request{
		CompanyID:    companyID,
		Action:       authz.Display,
		Resource:     "report",
		ResourceName: "trial-balance",
	}); !d.Allow {
		t.Errorf("Data Entry may no longer display report:trial-balance (%s) — the payroll refusal was written too coarsely", d.Reason)
	}
}

// TestDataEntryCannotCreatePayrollVouchers proves the voucher class and the
// employee master are refused for create, alter and display.
//
// For these BARE tokens, silence would already deny (default_access 'none'), so
// this test also asserts the rows EXIST — otherwise it would pass on a database
// where migration 0035 had never run, which is exactly the kind of vacuously
// green test this project has been bitten by. The existence check is the part
// that fails if the grant table loses a row.
func TestDataEntryCannotCreatePayrollVouchers(t *testing.T) {
	pool, _, companyID := setup(t)
	lvl, rules := dataEntryLevelAndRules(t, pool, companyID)

	for _, res := range []authz.Resource{"payroll", "employee"} {
		for _, act := range []authz.Action{authz.Create, authz.Alter, authz.Display} {
			d := authz.Evaluate(lvl, rules, authz.Request{
				CompanyID: companyID, Action: act, Resource: res,
			})
			if d.Allow {
				t.Errorf("Data Entry may %s %s — the wage bill and the employee master (PAN, UAN, IP number, IFSC) are not operator-reachable by default", act, res)
			}
			// The explicit rule must be PRESENT, not merely implied by the
			// default. An implied deny evaporates the day an administrator
			// writes `allow create *`, which needs no code change and is a
			// natural thing to do.
			found := false
			for _, r := range rules {
				if r.Effect == authz.Disallow && r.Action == act && r.Resource == res {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("no explicit `disallow %s %s` rule was seeded; the deny currently rests on default_access alone and would evaporate under a later `allow %s *`", act, res, act)
			}
		}
	}
}

// TestPayrollTablesAreTenantIsolated proves a second company sees ZERO payroll
// rows through tally_app, for every one of the six tables.
//
// It reads as the APPLICATION role and never as the superuser: postgres bypasses
// RLS entirely, so the same query run as the migration role would return the
// other tenant's rows and prove nothing. FORCE ROW LEVEL SECURITY is what stops
// the table OWNER bypassing the policy, and that is the half a missing FORCE
// would silently remove.
func TestPayrollTablesAreTenantIsolated(t *testing.T) {
	pool, ctxA, companyA := setup(t)
	svc := newService(pool)

	empA := createEmployee(t, svc, ctxA, "E-ISO-1", "Isolated Employee")
	setStructureAndElection(t, svc, ctxA, empA, "2026-04-01")
	if _, err := svc.RunPayRun(ctxA, samplePayRun(t, svc, ctxA, empA)); err != nil {
		t.Fatalf("run pay run in company A: %v", err)
	}

	_, ctxB := newCompany(t, pool)

	for _, table := range []string{
		"employee", "employee_salary_structure", "employee_statutory_election",
		"pay_run", "payslip", "payslip_component",
	} {
		var n int
		if err := pool.RunInTx(ctxB, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n)
		}); err != nil {
			t.Fatalf("count %s as company B: %v", table, err)
		}
		if n != 0 {
			t.Errorf("company B sees %d row(s) in %s belonging to company %s — RLS is not confining payroll data, "+
				"so one tenant's salaries, PANs and UANs are readable by another", n, table, companyA)
		}
	}

	// And the positive control: company A still sees its own rows. Without this,
	// a policy of `USING (false)` would pass the assertion above while hiding
	// every tenant's data from itself.
	var mine int
	if err := pool.RunInTx(ctxA, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM payslip`).Scan(&mine)
	}); err != nil {
		t.Fatalf("count payslip as company A: %v", err)
	}
	if mine == 0 {
		t.Fatal("company A cannot see its own payslips; the isolation assertion above is vacuous")
	}
}

// TestPayrollTablesForceRowLevelSecurity asserts the SCHEMA FLAGS on all six
// payroll tables: RLS enabled AND forced, with a tenant_isolation policy that
// has BOTH a USING clause and a WITH CHECK clause.
//
// WHY THIS IS A SEPARATE TEST FROM THE ISOLATION ONE ABOVE, AND WHY IT HAD TO
// BE WRITTEN. The obvious mutation for "prove FORCE matters" is to drop FORCE
// from a table and expect TestPayrollTablesAreTenantIsolated to fail. IT DOES
// NOT FAIL, and that is not a bug in the mutation — it is what FORCE means.
// FORCE ROW LEVEL SECURITY only affects the table OWNER (here: postgres, the
// migration role). tally_app is not the owner, so the policy applies to it with
// or without FORCE, and a test that reads as tally_app cannot observe the
// difference.
//
// So the FORCE half needs its own assertion, and this is it. What FORCE
// actually buys is that a cross-tenant read is impossible in exactly the
// sessions most likely to perform one: migrations, backfills and by-hand psql,
// all of which run as the owner. Dropping it would leave every payroll table
// wide open to the role that every schema change runs as, and no application
// test anywhere would notice.
//
// The WITH CHECK half is the write side: USING alone hides other tenants' rows
// from a SELECT but permits an INSERT that WRITES a row for another company.
func TestPayrollTablesForceRowLevelSecurity(t *testing.T) {
	pool, ctx, _ := setup(t)

	for _, table := range []string{
		"employee", "employee_salary_structure", "employee_statutory_election",
		"pay_run", "payslip", "payslip_component",
	} {
		var enabled, forced bool
		var hasUsing, hasWithCheck bool
		if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			if err := tx.QueryRow(ctx,
				`SELECT relrowsecurity, relforcerowsecurity
				   FROM pg_class WHERE relname = $1 AND relnamespace = 'public'::regnamespace`,
				table).Scan(&enabled, &forced); err != nil {
				return err
			}
			return tx.QueryRow(ctx,
				`SELECT qual IS NOT NULL, with_check IS NOT NULL
				   FROM pg_policies
				  WHERE schemaname = 'public' AND tablename = $1 AND policyname = 'tenant_isolation'`,
				table).Scan(&hasUsing, &hasWithCheck)
		}); err != nil {
			t.Fatalf("read RLS flags for %s: %v", table, err)
		}
		if !enabled {
			t.Errorf("%s does not have ROW LEVEL SECURITY enabled; every tenant's payroll data is readable by every other tenant", table)
		}
		if !forced {
			t.Errorf("%s does not have FORCE ROW LEVEL SECURITY; the table OWNER bypasses the policy, and the owner is the role every "+
				"migration, every backfill and every by-hand psql session runs as — so a cross-tenant read would be invisible in exactly "+
				"the sessions most likely to perform one", table)
		}
		if !hasUsing {
			t.Errorf("%s has no tenant_isolation policy with a USING clause", table)
		}
		if !hasWithCheck {
			t.Errorf("%s's tenant_isolation policy has no WITH CHECK clause; USING alone hides another tenant's rows from a SELECT "+
				"but still permits an INSERT that WRITES a row belonging to somebody else", table)
		}
	}
}

// TestControlLedgerMissingIsRefusedNotInvented proves that a company whose
// chart lacks a payroll control ledger gets a REFUSAL naming the ledger, rather
// than a ledger invented under whatever group happened to resolve.
//
// A payable filed under an expense group is a balance sheet wrong by the whole
// payroll liability, and nothing would report it: the trial balance still
// balances.
func TestControlLedgerMissingIsRefusedNotInvented(t *testing.T) {
	pool, ctx, _ := setup(t)
	svc := newService(pool)

	emp := createEmployee(t, svc, ctx, "E-NOLED-1", "No Ledger")
	setStructureAndElection(t, svc, ctx, emp, "2026-04-01")

	// Rename the control ledger out from under the run. Renaming rather than
	// deleting because ledger rows are referenced by postings elsewhere; the
	// resolution is BY NAME, so a rename is exactly the "missing" the resolver
	// must cope with.
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE ledger SET name = 'PF Payable (renamed)' WHERE name = $1`,
			payrolldb.LedgerPFPayable)
		return err
	}); err != nil {
		t.Fatalf("rename control ledger: %v", err)
	}

	_, err := svc.RunPayRun(ctx, samplePayRun(t, svc, ctx, emp))
	if err == nil {
		t.Fatal("a pay run posted with a control ledger missing; the liability landed somewhere nobody chose")
	}
	if !errors.Is(err, payrolldb.ErrControlLedgerMissing) {
		t.Fatalf("wrong error for a missing control ledger: %v", err)
	}
}
