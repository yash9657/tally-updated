package query_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/internal/query"
	"github.com/khata/cored/migrations"
)

// The DSNs honour ADMIN_DATABASE_URL / DATABASE_URL (the names the Makefile
// already exports) so a lane can run this package against its OWN database.
// Every test here calls migrations.Apply, so a hardcoded DSN meant that merely
// running the query tests with a new migration on disk migrated the shared
// development database — a side effect nobody asked for, invisible until
// another checkout found a table its branch does not know about.
var (
	adminDSN = testdb.AdminDSNValue()
	appDSN   = testdb.AppDSNValue()
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// setupCompany migrates the live DB (idempotent) and returns an app pool plus a
// fresh seeded company in a unique tenant scope. Mirrors reporting_test.setup.
func setupCompany(t *testing.T, name string) (*db.Pool, context.Context, uuid.UUID, *identity.Service) {
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

	companyID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new company id: %v", err)
	}
	cctx := db.WithCompany(ctx, companyID)

	ids := identity.NewService(pool)
	if _, err := ids.CreateCompany(cctx, identity.Company{
		ID:          companyID,
		Name:        name + " " + companyID.String()[:8],
		FYStartDate: "2025-04-01",
	}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	// Teardown, registered the moment the company exists. setupCompany is called
	// from BOTH execute_test.go and the adversarial query_security_test.go, so
	// this single registration covers every company the package mints through a
	// helper — the one it mints inline (tenant B of the cross-tenant probe) is
	// registered at its own site.
	testdb.Cleanup(t, companyID)
	return pool, cctx, companyID, ids
}

// seedSales posts two Sales vouchers (Apr + May 2025) and a Purchase, returning
// the sales taxable totals for assertions.
func seedSales(t *testing.T, pool *db.Pool, ctx context.Context, ids *identity.Service) {
	t.Helper()
	led := ledger.NewService(pool)

	salesVT, err := ids.VoucherTypeID(ctx, "Sales")
	if err != nil {
		t.Fatalf("sales vt: %v", err)
	}
	purchaseVT, err := ids.VoucherTypeID(ctx, "Purchase")
	if err != nil {
		t.Fatalf("purchase vt: %v", err)
	}

	mk := func(name, group string) uuid.UUID {
		id, err := ids.CreateLedger(ctx, name, group)
		if err != nil {
			t.Fatalf("create ledger %s: %v", name, err)
		}
		return id
	}
	acme := mk("Acme Corp", "Sundry Debtors")
	sales := mk("Domestic Sales", "Sales Accounts")
	ocgst := mk("Output CGST", "Duties & Taxes")
	osgst := mk("Output SGST", "Duties & Taxes")
	globex := mk("Globex Ltd", "Sundry Creditors")
	purchase := mk("Local Purchase", "Purchase Accounts")

	post := func(vt uuid.UUID, class ledger.Class, party uuid.UUID, no, date string, taxable, tax decimal.Decimal,
		ps []ledger.PostingInput, ba []ledger.BillAllocationInput) {
		if _, err := led.PostVoucher(ctx, ledger.VoucherInput{
			Class: class, VoucherTypeID: vt, PartyLedgerID: party, VoucherNo: no, VoucherDate: date,
			Party: "p", Narration: "n", CreatedBy: "tester", Postings: ps, BillAllocations: ba,
		}); err != nil {
			t.Fatalf("post %s: %v", no, err)
		}
	}

	// April sale: taxable 1000 + 18% (90+90).
	post(salesVT, ledger.Sales, acme, "INV-1", "2025-04-10", dec("1000"), dec("180"),
		[]ledger.PostingInput{
			{LedgerID: acme, DrCr: ledger.Debit, Amount: dec("1180")},
			{LedgerID: sales, DrCr: ledger.Credit, Amount: dec("1000")},
			{LedgerID: ocgst, DrCr: ledger.Credit, Amount: dec("90")},
			{LedgerID: osgst, DrCr: ledger.Credit, Amount: dec("90")},
		},
		[]ledger.BillAllocationInput{{LedgerID: acme, RefName: "INV-1", RefType: ledger.RefNew, DrCr: ledger.Debit, Amount: dec("1180"), DueDate: "2025-05-10"}})

	// May sale: taxable 2000 + 18% (180+180).
	post(salesVT, ledger.Sales, acme, "INV-2", "2025-05-15", dec("2000"), dec("360"),
		[]ledger.PostingInput{
			{LedgerID: acme, DrCr: ledger.Debit, Amount: dec("2360")},
			{LedgerID: sales, DrCr: ledger.Credit, Amount: dec("2000")},
			{LedgerID: ocgst, DrCr: ledger.Credit, Amount: dec("180")},
			{LedgerID: osgst, DrCr: ledger.Credit, Amount: dec("180")},
		},
		[]ledger.BillAllocationInput{{LedgerID: acme, RefName: "INV-2", RefType: ledger.RefNew, DrCr: ledger.Debit, Amount: dec("2360"), DueDate: "2025-06-15"}})

	// A purchase (so a Sales filter must exclude it).
	post(purchaseVT, ledger.Purchase, globex, "PB-1", "2025-04-12", dec("590"), dec("0"),
		[]ledger.PostingInput{
			{LedgerID: purchase, DrCr: ledger.Debit, Amount: dec("590")},
			{LedgerID: globex, DrCr: ledger.Credit, Amount: dec("590")},
		},
		[]ledger.BillAllocationInput{{LedgerID: globex, RefName: "PB-1", RefType: ledger.RefNew, DrCr: ledger.Credit, Amount: dec("590"), DueDate: "2025-05-12"}})
}

// TestExecute_SalesByMonth executes the pinned flagship query (vouchers grouped
// by month, Sales only) and asserts per-month counts. (taxable_value is only
// populated when GST derivation runs; this seed posts plain vouchers, so the
// reliable, version-level fact is the per-month voucher count for Sales.)
func TestExecute_SalesByMonth(t *testing.T) {
	pool, ctx, _, ids := setupCompany(t, "Exec Co")
	seedSales(t, pool, ctx, ids)

	q := query.Query{
		Entity:     query.EntityVouchers,
		Metrics:    []query.Metric{{Func: query.AggCount, Column: "*", As: "n"}},
		Dimensions: []string{"month"},
		Filters:    []query.Filter{{Column: "voucher_type_name", Op: query.OpEq, Values: []string{"Sales"}}},
		OrderBy:    []query.OrderTerm{{Column: "month"}},
		Limit:      100,
	}
	res, err := query.Execute(ctx, pool, q)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(res.Columns) != 2 || res.Columns[0].Name != "month" || res.Columns[1].Name != "n" {
		t.Fatalf("unexpected columns: %+v", res.Columns)
	}
	got := map[string]string{}
	for _, r := range res.Rows {
		got[r[0]] = r[1]
	}
	if got["2025-04"] != "1.0000" {
		t.Errorf("April sales count = %q, want 1.0000", got["2025-04"])
	}
	if got["2025-05"] != "1.0000" {
		t.Errorf("May sales count = %q, want 1.0000", got["2025-05"])
	}
	// The purchase must NOT appear as a sales row.
	if len(res.Rows) != 2 {
		t.Errorf("expected exactly 2 month rows (Sales only), got %d: %+v", len(res.Rows), res.Rows)
	}
}

// TestExecute_PostingsSumByLedger asserts paisa-exact money via the postings
// entity (posting.amount is always populated, independent of GST), proving the
// StringFixed(4) money rendering matches the seeded amounts.
func TestExecute_PostingsSumByLedger(t *testing.T) {
	pool, ctx, _, ids := setupCompany(t, "Postings Co")
	seedSales(t, pool, ctx, ids)

	q := query.Query{
		Entity:     query.EntityPostings,
		Metrics:    []query.Metric{{Func: query.AggSum, Column: "amount", As: "total"}},
		Dimensions: []string{"ledger_name"},
		Filters: []query.Filter{
			{Column: "ledger_name", Op: query.OpEq, Values: []string{"Domestic Sales"}},
			{Column: "dr_cr", Op: query.OpEq, Values: []string{"C"}},
		},
		Limit: 100,
	}
	res, err := query.Execute(ctx, pool, q)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Domestic Sales credited 1000 (Apr) + 2000 (May) = 3000.
	if len(res.Rows) != 1 || res.Rows[0][1] != "3000.0000" {
		t.Fatalf("Domestic Sales credit total = %+v, want one row 3000.0000", res.Rows)
	}
}

// TestExecute_LimitCapsRows proves the row cap is enforced even if the SQL would
// return more (defense-in-depth alongside the SQL LIMIT).
func TestExecute_LimitCapsRows(t *testing.T) {
	pool, ctx, _, ids := setupCompany(t, "Limit Co")
	seedSales(t, pool, ctx, ids)

	q := query.Query{
		Entity:     query.EntityVouchers,
		Metrics:    []query.Metric{{Func: query.AggCount, Column: "*", As: "n"}},
		Dimensions: []string{"month"},
		Limit:      1,
	}
	res, err := query.Execute(ctx, pool, q)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("limit 1 should cap to 1 row, got %d", len(res.Rows))
	}
	if !res.HasMore || res.Limit != 1 {
		t.Fatalf("missing incomplete-result metadata: %+v", res)
	}
	q.Limit = 2
	res, err = query.Execute(ctx, pool, q)
	if err != nil || res.HasMore || len(res.Rows) != 2 {
		t.Fatalf("exact-limit result incorrectly incomplete: %+v %v", res, err)
	}
}

// TestRunInReadOnlyTx_RejectsWrite proves the read-only transaction is the
// write-impossibility backstop: an INSERT inside it errors.
func TestRunInReadOnlyTx_RejectsWrite(t *testing.T) {
	pool, ctx, _, _ := setupCompany(t, "RO Co")

	err := pool.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "INSERT INTO account_group(id,company_id,name,nature,is_pl) VALUES (gen_random_uuid(), app_current_company_id(), 'x','ASSET',false)")
		return e
	})
	if err == nil {
		t.Fatal("expected read-only tx to reject INSERT")
	}
}

// TestRunInReadOnlyTx_NoCompanyGuard proves the ErrNoCompany short-circuit is
// preserved (no DB round-trip without a tenant scope).
func TestRunInReadOnlyTx_NoCompanyGuard(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, appDSN)
	if err != nil {
		t.Skipf("app pool unavailable: %v", err)
	}
	defer pool.Close()
	if err := pool.RunInReadOnlyTx(ctx, func(context.Context, pgx.Tx) error { return nil }); err != db.ErrNoCompany {
		t.Fatalf("expected ErrNoCompany, got %v", err)
	}
}

// TestQueryHarnessRemovesItsCompany is the anti-rot guard on setupCompany()'s
// teardown. A harness that quietly stops cleaning up breaks no assertion — it
// just buries another company and its whole chart of accounts on every run,
// which is how the shared dev database reached 43,419 of them.
//
// It counts the child tables, not only `company`, because notification_outbox is
// the ONLY foreign key referencing company(id) and its action is NO ACTION:
// deleting the parent row alone leaves account_group, ledger, voucher_type and
// acl_rule behind with no error and no cascade. A company-only assertion passed
// against precisely that bug once already.
func TestQueryHarnessRemovesItsCompany(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	// Cleanups registered on the inner *testing.T have all run by the time t.Run
	// returns, so the harness has had its chance before the next line.
	var cid uuid.UUID
	t.Run("harness", func(t *testing.T) { _, _, cid, _ = setupCompany(t, "Teardown Probe") })
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
				"the query harness leaks them on EVERY run and nothing will ever delete them",
				tbl, n, cid)
		}
	}
}
