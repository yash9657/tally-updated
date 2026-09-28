package ledger_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/internal/reporting"
	"github.com/khata/cored/migrations"
)

var (
	adminDSN = testdb.AdminDSNValue()
	appDSN   = testdb.AppDSNValue()
)

// setup migrates the live DB (idempotent) and returns an app-scoped pool plus a
// freshly created, seeded company. Each test gets its own company id so the
// tenant-isolated tables never collide across runs.
func setup(t *testing.T) (*db.Pool, context.Context, uuid.UUID, *identity.Service) {
	t.Helper()
	ctx := context.Background()

	// Apply migrations as admin (creates role, schema, seed function).
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

	companyID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new company id: %v", err)
	}
	cctx := db.WithCompany(ctx, companyID)

	ids := identity.NewService(pool)
	if _, err := ids.CreateCompany(cctx, identity.Company{
		ID:          companyID,
		Name:        "Test Co " + companyID.String()[:8],
		FYStartDate: "2025-04-01",
	}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	// Register the teardown the instant the company exists, and BEFORE any child
	// master is made, so that a t.Fatal anywhere below still leaves a registered
	// cleanup behind. t.Cleanup is LIFO, so this runs before the pool.Close above
	// — which is correct, and in any case testdb dials its own admin pool rather
	// than borrowing this RLS-restricted one.
	//
	// Without this line the run leaks the company AND the ~28 account_group rows
	// the seed plants under it. Nothing ever collects them: notification_outbox is
	// the ONLY foreign key that references company(id) and its action is NO ACTION,
	// so deleting the company row alone orphans the whole chart of accounts without
	// raising a single error. That is how this dev database reached 43,419
	// companies and 603 MB of account_group nobody will ever read.
	testdb.Cleanup(t, companyID)
	return pool, cctx, companyID, ids
}

// TestNoCompanyGuardRefusesQuery verifies the db layer refuses to touch the DB
// when no company is in context (the GUC guard contract).
func TestNoCompanyGuardRefusesQuery(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	ctx := context.Background()
	adminConn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	_ = adminConn.Close(ctx)

	pool, err := db.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	err = pool.RunInTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		t.Fatal("fn must not run when no company is in context")
		return nil
	})
	if err != db.ErrNoCompany {
		t.Fatalf("expected ErrNoCompany, got %v", err)
	}
}

func TestPostSalesVoucher_BalancesAndAppears(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	pool, ctx, companyID, ids := setup(t)

	// Resolve seeded ledgers / create the F8 sale ledgers.
	vtID, err := ids.VoucherTypeID(ctx, "Sales")
	if err != nil {
		t.Fatalf("voucher type: %v", err)
	}
	debtor, err := ids.CreateLedger(ctx, "Acme Corp", "Sundry Debtors")
	if err != nil {
		t.Fatalf("create debtor: %v", err)
	}
	sales, err := ids.CreateLedger(ctx, "Domestic Sales", "Sales Accounts")
	if err != nil {
		t.Fatalf("create sales: %v", err)
	}
	cgst, err := ids.CreateLedger(ctx, "Output CGST", "Duties & Taxes")
	if err != nil {
		t.Fatalf("create cgst: %v", err)
	}
	sgst, err := ids.CreateLedger(ctx, "Output SGST", "Duties & Taxes")
	if err != nil {
		t.Fatalf("create sgst: %v", err)
	}

	led := ledger.NewService(pool)
	rep := reporting.NewService(pool)

	// Intra-state @18% on 1000: CGST 90 + SGST 90; debtor pays 1180.
	in := ledger.SalesVoucherInput{
		VoucherTypeID: vtID,
		VoucherNo:     "1",
		VoucherDate:   "2025-05-10",
		Narration:     "Sale to Acme",
		Party:         "Acme Corp",
		CreatedBy:     "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: debtor, DrCr: ledger.Debit, Amount: decimal.RequireFromString("1180.0000")},
			{LedgerID: sales, DrCr: ledger.Credit, Amount: decimal.RequireFromString("1000.0000")},
			{LedgerID: cgst, DrCr: ledger.Credit, Amount: decimal.RequireFromString("90.0000")},
			{LedgerID: sgst, DrCr: ledger.Credit, Amount: decimal.RequireFromString("90.0000")},
		},
	}
	res, err := led.PostSalesVoucher(ctx, in)
	if err != nil {
		t.Fatalf("post sale: %v", err)
	}
	if res.VersionNo != 1 {
		t.Fatalf("first version should be 1, got %d", res.VersionNo)
	}
	if len(res.Hash) == 0 {
		t.Fatal("expected a non-empty hash")
	}

	// Trial balance must balance and include the debtor.
	tb, err := rep.TrialBalance(ctx, companyID, "")
	if err != nil {
		t.Fatalf("trial balance: %v", err)
	}
	if !tb.IsBalanced() {
		t.Fatalf("trial balance not balanced: dr=%s cr=%s", tb.TotalDebit, tb.TotalCredit)
	}
	if !tb.TotalDebit.Equal(decimal.RequireFromString("1180.0000")) {
		t.Fatalf("expected total debit 1180, got %s", tb.TotalDebit)
	}
	var foundDebtor bool
	for _, r := range tb.Rows {
		if r.LedgerID == debtor {
			foundDebtor = true
			if !r.Debit.Equal(decimal.RequireFromString("1180.0000")) {
				t.Fatalf("debtor debit expected 1180, got %s", r.Debit)
			}
		}
	}
	if !foundDebtor {
		t.Fatal("sale's debtor ledger did not appear in trial balance")
	}
}

func TestAlterCreatesNewVersionWithHashChain(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	pool, ctx, companyID, ids := setup(t)

	vtID, _ := ids.VoucherTypeID(ctx, "Sales")
	debtor, _ := ids.CreateLedger(ctx, "Beta Ltd", "Sundry Debtors")
	sales, _ := ids.CreateLedger(ctx, "Export Sales", "Sales Accounts")

	led := ledger.NewService(pool)
	rep := reporting.NewService(pool)

	mk := func(no, amount string) ledger.SalesVoucherInput {
		a := decimal.RequireFromString(amount)
		return ledger.SalesVoucherInput{
			VoucherTypeID: vtID, VoucherNo: no, VoucherDate: "2025-05-11",
			Narration: "Beta sale", Party: "Beta Ltd", CreatedBy: "tester",
			Postings: []ledger.PostingInput{
				{LedgerID: debtor, DrCr: ledger.Debit, Amount: a},
				{LedgerID: sales, DrCr: ledger.Credit, Amount: a},
			},
		}
	}

	v1, err := led.PostSalesVoucher(ctx, mk("1", "500.0000"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	v2, err := led.AlterSalesVoucher(ctx, v1.VoucherID, mk("1", "750.0000"))
	if err != nil {
		t.Fatalf("alter: %v", err)
	}

	if v2.VersionNo != 2 {
		t.Fatalf("alter should produce version 2, got %d", v2.VersionNo)
	}
	if v2.VoucherID != v1.VoucherID {
		t.Fatal("alter must reuse the logical voucher id")
	}
	if v2.VoucherVersionID == v1.VoucherVersionID {
		t.Fatal("alter must create a NEW immutable version row (different id)")
	}

	// Verify the hash chain: version 2's prev_hash must equal version 1's hash,
	// and both versions must still exist (immutability).
	err = pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM voucher_version WHERE voucher_id = $1`, v1.VoucherID).Scan(&count); err != nil {
			return err
		}
		if count != 2 {
			t.Fatalf("expected 2 versions to coexist, got %d", count)
		}

		var v1Hash, v2Prev []byte
		if err := tx.QueryRow(ctx,
			`SELECT hash FROM voucher_version WHERE voucher_id = $1 AND version_no = 1`, v1.VoucherID).Scan(&v1Hash); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx,
			`SELECT prev_hash FROM voucher_version WHERE voucher_id = $1 AND version_no = 2`, v1.VoucherID).Scan(&v2Prev); err != nil {
			return err
		}
		if string(v1Hash) != string(v2Prev) {
			t.Fatalf("hash chain broken: v2.prev_hash != v1.hash")
		}
		if string(v2Prev) == "" {
			t.Fatal("v2 prev_hash must be non-empty")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("verify chain: %v", err)
	}

	// post() nets out the prior version, so the debtor balance reflects only the
	// CURRENT version (750), not the sum 500 + 750.
	tb, err := rep.TrialBalance(ctx, companyID, "")
	if err != nil {
		t.Fatalf("trial balance: %v", err)
	}
	if !tb.IsBalanced() {
		t.Fatalf("trial balance not balanced after alter")
	}
	if !tb.TotalDebit.Equal(decimal.RequireFromString("750.0000")) {
		t.Fatalf("alter must restate, not accumulate: total debit = %s, want 750", tb.TotalDebit)
	}
}

// TestDeleteVoucherNetsBalancesToZero proves the highest-risk correctness item:
// a DELETE tombstone reverses the prior current version out of ledger_balance
// and adds NOTHING, so the voucher's net contribution becomes exactly zero and
// voucher_head.is_deleted flips to true (while the versions remain in the log).
func TestDeleteVoucherNetsBalancesToZero(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	pool, ctx, companyID, ids := setup(t)

	vtID, _ := ids.VoucherTypeID(ctx, "Sales")
	debtor, _ := ids.CreateLedger(ctx, "Gamma Inc", "Sundry Debtors")
	sales, _ := ids.CreateLedger(ctx, "Service Sales", "Sales Accounts")

	led := ledger.NewService(pool)
	rep := reporting.NewService(pool)

	a := decimal.RequireFromString("1000.0000")
	v1, err := led.PostSalesVoucher(ctx, ledger.SalesVoucherInput{
		VoucherTypeID: vtID, VoucherNo: "DEL-1", VoucherDate: "2025-05-12",
		Narration: "Gamma sale", Party: "Gamma Inc", CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: debtor, DrCr: ledger.Debit, Amount: a},
			{LedgerID: sales, DrCr: ledger.Credit, Amount: a},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	del, err := led.DeleteVoucher(ctx, v1.VoucherID, "deleter")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if del.VoucherID != v1.VoucherID {
		t.Fatal("delete must reuse the logical voucher id")
	}
	if del.VersionNo != 2 {
		t.Fatalf("delete should produce version 2, got %d", del.VersionNo)
	}

	// Both ledger balances must net to zero after the delete.
	tb, err := rep.TrialBalance(ctx, companyID, "")
	if err != nil {
		t.Fatalf("trial balance: %v", err)
	}
	if !tb.TotalDebit.Equal(decimal.Zero) || !tb.TotalCredit.Equal(decimal.Zero) {
		t.Fatalf("delete must net balances to zero: dr=%s cr=%s", tb.TotalDebit, tb.TotalCredit)
	}

	// voucher_head.is_deleted must be true and the version log must still hold
	// both versions (immutable audit trail), the tombstone carrying no postings.
	err = pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var isDeleted bool
		if err := tx.QueryRow(ctx,
			`SELECT is_deleted FROM voucher_head WHERE voucher_id = $1`, v1.VoucherID).Scan(&isDeleted); err != nil {
			return err
		}
		if !isDeleted {
			t.Fatal("voucher_head.is_deleted must be true after delete")
		}
		var versions int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM voucher_version WHERE voucher_id = $1`, v1.VoucherID).Scan(&versions); err != nil {
			return err
		}
		if versions != 2 {
			t.Fatalf("expected 2 versions to coexist after delete, got %d", versions)
		}
		var tombPostings int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM posting WHERE voucher_version_id = $1`, del.VoucherVersionID).Scan(&tombPostings); err != nil {
			return err
		}
		if tombPostings != 0 {
			t.Fatalf("DELETE tombstone must carry zero postings, got %d", tombPostings)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("verify delete state: %v", err)
	}
}

// TestDeleteUnknownVoucherErrors proves the phantom-create guard: deleting an
// id with no head row returns ErrVoucherNotFound rather than minting version 1.
func TestDeleteUnknownVoucherErrors(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	pool, ctx, _, _ := setup(t)
	led := ledger.NewService(pool)

	unknown, _ := uuid.NewV7()
	if _, err := led.DeleteVoucher(ctx, unknown, "deleter"); !errors.Is(err, ledger.ErrVoucherNotFound) {
		t.Fatalf("expected ErrVoucherNotFound, got %v", err)
	}

	// AlterVoucher on an unknown id must also refuse to phantom-create. The
	// postings reuse the unknown id as a stand-in ledger; the not-found guard
	// fires inside the transaction before any posting is inserted.
	if _, err := led.AlterVoucher(ctx, unknown, ledger.VoucherInput{
		Class: ledger.Sales, VoucherNo: "X", VoucherDate: "2025-05-12", CreatedBy: "t",
		Postings: []ledger.PostingInput{
			{LedgerID: unknown, DrCr: ledger.Debit, Amount: decimal.RequireFromString("1")},
			{LedgerID: unknown, DrCr: ledger.Credit, Amount: decimal.RequireFromString("1")},
		},
	}); !errors.Is(err, ledger.ErrVoucherNotFound) {
		t.Fatalf("alter unknown: expected ErrVoucherNotFound, got %v", err)
	}
}

// TestLedgerHarnessRemovesItsCompany is the anti-rot guard on setup()'s teardown.
//
// It is deliberately NOT a test of testdb — testdb carries its own. It is a test
// that THIS package's harness still registers the teardown, because forgetting
// it is a silent failure: every other test in the package goes on passing while
// each run buries another company and its whole chart of accounts in the shared
// database. That is not hypothetical; it is the state Track 0 was created to
// clean up.
//
// WHY THE CHILD TABLES ARE CHECKED, NOT JUST `company`
// ----------------------------------------------------
// The cleanup this replaced deleted the company row and nothing else, and it
// LOOKED right. Only notification_outbox references company(id), and its foreign
// key action is NO ACTION, so Postgres happily removed the parent and orphaned
// the 28 account_group rows, the seeded ledgers, the voucher types and the ACL
// rules underneath it — no error, no cascade, no trace. An assertion that
// counted only `company` would have blessed that bug for a second time, so this
// one counts the five tables the company seed actually populates.
//
// WHY THERE IS A SECOND PHASE
// ---------------------------
// A guard that only exercises setup() is exactly half a guard, and the missing
// half is the dangerous one. This package mints companies at TWO sites: setup(),
// and an inline `CreateCompany` for tenant B inside TestTenantIsolation. Deleting
// the teardown at the inline site was tried while this test was being written:
// the whole package still exited 0, every assertion still passed, and the run
// quietly left one more company behind. Only a count taken from OUTSIDE the test
// binary saw it. So the second phase drives TestTenantIsolation and looks for its
// footprint directly, and the package now fails on its own when either site
// regresses rather than waiting for a nightly residue script to notice.
func TestLedgerHarnessRemovesItsCompany(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, testdb.AdminDSNValue())
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	defer conn.Close(ctx)

	// The app role is FORCE-RLS'd to one company and would report 0 for every
	// table regardless of what survived, which is exactly the vacuous green this
	// test exists to prevent. Every count below is taken as the admin (a
	// superuser bypasses row-level security).
	childTables := []string{"company", "account_group", "ledger", "voucher_type", "acl_rule"}
	assertGone := func(id uuid.UUID, site string) {
		for _, tbl := range childTables {
			var n int
			if err := conn.QueryRow(ctx, "SELECT count(*) FROM "+tbl+" WHERE company_id = $1", id).Scan(&n); err != nil {
				t.Fatalf("count %s for company %s: %v", tbl, id, err)
			}
			if n != 0 {
				t.Errorf("%s still holds %d rows for company %s after %s finished: "+
					"the ledger package leaks them on EVERY run and nothing will ever delete them",
					tbl, n, id, site)
			}
		}
	}

	// --- Phase 1: the shared harness, setup(). -----------------------------
	// Mint the company inside a subtest: every t.Cleanup registered on the inner
	// *testing.T has already run by the time t.Run returns, so from the next line
	// onwards the harness has had its full chance to tidy up. Reading the id out
	// through a closure variable is what lets the parent test see what the child
	// created.
	var cid uuid.UUID
	t.Run("setup", func(t *testing.T) { _, _, cid, _ = setup(t) })
	if cid == uuid.Nil {
		t.Skip("harness skipped (no database), nothing to assert about teardown")
	}
	assertGone(cid, "setup()")

	// --- Phase 2: the INLINE second tenant inside TestTenantIsolation. ------
	// This site hands its id to nobody, so there is no id to ask for. What it does
	// leave is a fingerprint: tenant B is the only company in the schema named
	// exactly "Company B". Snapshotting that set before and after, and blaming
	// only ids that are NEW, is what makes this safe on a database that already
	// holds thousands of "Company B" rows from the years before any teardown
	// existed — those are pre-existing residue, not this run's doing.
	//
	// The one thing this cannot distinguish is a SECOND, concurrent run of this
	// same package writing its own "Company B" at the same moment. That is the
	// same limitation the residue script carries, and the project already forbids
	// running the suite against the shared dev database concurrently for it.
	tenantBs := func(when string) map[uuid.UUID]bool {
		rows, err := conn.Query(ctx, `SELECT id FROM company WHERE name = 'Company B'`)
		if err != nil {
			t.Fatalf("list tenant-B companies %s: %v", when, err)
		}
		defer rows.Close()
		out := map[uuid.UUID]bool{}
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				t.Fatalf("scan tenant-B id %s: %v", when, err)
			}
			out[id] = true
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate tenant-B companies %s: %v", when, err)
		}
		return out
	}

	before := tenantBs("before")
	t.Run("inline second tenant", func(t *testing.T) { TestTenantIsolation(t) })
	for id := range tenantBs("after") {
		if before[id] {
			continue // pre-existing residue from before this teardown existed
		}
		assertGone(id, "TestTenantIsolation's inline company B")
	}
}
