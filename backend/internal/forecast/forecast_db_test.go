package forecast_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/forecast"
	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/internal/reporting"
	"github.com/khata/cored/migrations"
)

// forecast_db_test.go is the LOADER gate (connect-or-skip). It proves three
// things the pure corpus cannot: (1) the credit_days branch is live — a bill with
// no due date is placed at voucher_date + the party's masters.CreateLedger
// credit_days (NOT the +30 default); (2) the invariance/conservation gate holds —
// the projected inflow total equals the INDEPENDENT reporting.Outstandings
// receivables total; (3) the anomaly loader reads voucher_date + created_at (a
// 2025-dated voucher entered now() is BACKDATED).

var (
	adminDSN = testdb.AdminDSNValue()
	appDSN   = testdb.AppDSNValue()
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func setup(t *testing.T) (*db.Pool, context.Context, uuid.UUID, *identity.Service) {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	ctx := context.Background()
	adminConn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
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

	companyID := uuid.Must(uuid.NewV7())
	cctx := db.WithCompany(ctx, companyID)
	ids := identity.NewService(pool)
	if _, err := ids.CreateCompany(cctx, identity.Company{
		ID: companyID, Name: "Forecast Co " + companyID.String()[:8], FYStartDate: "2025-04-01",
	}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	// Teardown, registered the moment the company exists and before the bills
	// below are posted. The forecast loader reads bill_allocation, whose rows are
	// immutable (BEFORE UPDATE OR DELETE trigger) and therefore unremovable by an
	// ordinary DELETE — testdb's replica-mode transaction is what gets them out.
	testdb.Cleanup(t, companyID)
	return pool, cctx, companyID, ids
}

func TestCashFlowInvarianceAndCreditDays(t *testing.T) {
	pool, ctx, companyID, ids := setup(t)
	led := ledger.NewService(pool)
	m := masters.NewService(pool)

	creditDays := 15
	// Debtor with credit_days via masters.CreateLedger (the ONLY path that sets it).
	acme, err := m.CreateLedger(ctx, masters.Ledger{Name: "Acme Corp", GroupName: "Sundry Debtors", CreditDays: &creditDays}, "tester")
	if err != nil {
		t.Fatalf("create debtor: %v", err)
	}
	mk := func(name, group string) uuid.UUID {
		id, err := ids.CreateLedger(ctx, name, group)
		if err != nil {
			t.Fatalf("create ledger %s: %v", name, err)
		}
		return id
	}
	sales := mk("Domestic Sales", "Sales Accounts")

	salesVT, err := ids.VoucherTypeID(ctx, "Sales")
	if err != nil {
		t.Fatalf("sales vt: %v", err)
	}

	// INV-1: receivable 1180, WITH a due date (2025-05-10).
	if _, err := led.PostVoucher(ctx, ledger.VoucherInput{
		Class: ledger.Sales, VoucherTypeID: salesVT, PartyLedgerID: acme,
		VoucherNo: "INV-1", VoucherDate: "2025-05-01", Party: "Acme Corp", CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: acme, DrCr: ledger.Debit, Amount: dec("1180")},
			{LedgerID: sales, DrCr: ledger.Credit, Amount: dec("1180")},
		},
		BillAllocations: []ledger.BillAllocationInput{
			{LedgerID: acme, RefName: "INV-1", RefType: ledger.RefNew, DrCr: ledger.Debit, Amount: dec("1180"), DueDate: "2025-05-10"},
		},
	}); err != nil {
		t.Fatalf("post INV-1: %v", err)
	}

	// INV-2: receivable 590, NO due date -> credit_days path (voucher 2025-05-01
	// + 15 = 2025-05-16).
	//
	// ⛔ IT WAS DATED 2025-06-01, A MONTH AFTER THE asOn THIS TEST PROJECTS FROM,
	// AND THAT WAS PINNING A DEFECT RATHER THAN TESTING ONE. reporting.Outstandings
	// honoured `as_on` for AGEING and not for INCLUSION, so an invoice raised a
	// month in the FUTURE was part of the receivables position "as at 2025-05-01"
	// — and this test's sanity line wrote that down as the expectation (1770).
	//
	// It matters here more than in most places: this is the CASH-FLOW PROJECTION.
	// A projection made on 1 May that already knows about an invoice raised on 1
	// June is not a forecast, it is hindsight — and every figure it produces would
	// be unreproducible on the day it claims to be about. The invoice is therefore
	// dated ON the asOn date, which tests the credit_days branch just as well: the
	// bill has no due date either way, which is the whole point of the branch.
	if _, err := led.PostVoucher(ctx, ledger.VoucherInput{
		Class: ledger.Sales, VoucherTypeID: salesVT, PartyLedgerID: acme,
		VoucherNo: "INV-2", VoucherDate: "2025-05-01", Party: "Acme Corp", CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: acme, DrCr: ledger.Debit, Amount: dec("590")},
			{LedgerID: sales, DrCr: ledger.Credit, Amount: dec("590")},
		},
		BillAllocations: []ledger.BillAllocationInput{
			{LedgerID: acme, RefName: "INV-2", RefType: ledger.RefNew, DrCr: ledger.Debit, Amount: dec("590")},
		},
	}); err != nil {
		t.Fatalf("post INV-2: %v", err)
	}

	svc := forecast.NewService(pool)

	// --- Invariance: projected inflow total == independent reporting receivables total.
	report := reporting.NewService(pool)
	recv, err := report.Outstandings(ctx, companyID, reporting.Receivables, "2025-05-01", nil)
	if err != nil {
		t.Fatalf("outstandings: %v", err)
	}
	// asOn 2025-05-01, week buckets, horizon 90 -> 13 buckets covering days 0..90.
	p, err := svc.CashFlow(ctx, companyID, "2025-05-01", 90, "week")
	if err != nil {
		t.Fatalf("cashflow: %v", err)
	}
	inflowTotal := p.Beyond.Inflow
	for _, b := range p.Buckets {
		inflowTotal = inflowTotal.Add(b.Inflow)
	}
	if !inflowTotal.Equal(recv.Total) {
		t.Fatalf("invariance: projected inflow total %s != reporting receivables total %s", inflowTotal, recv.Total)
	}
	// 1180 + 590, both raised ON the asOn date. See INV-2's note: the 1770 that
	// stood here was the same figure, but it was reached by including an invoice
	// dated a month AFTER the date being projected from.
	if !recv.Total.Equal(dec("1770")) {
		t.Fatalf("sanity: receivables total = %s, want 1770", recv.Total)
	}

	// --- credit_days branch is LIVE: INV-2 (no due date, credit_days 15) settles
	// 2025-05-16 = day 15 from asOn -> bucket 2 (15/7). With the +30 default it
	// would be 2025-05-31 = day 30 -> bucket 4. Asserting bucket 2 carries the 590
	// AND bucket 4 carries nothing proves credit_days was read, not defaulted —
	// the differential is the test, and it survives the re-dating above unchanged
	// because both arms move by the same month.
	if got := p.Buckets[2].Inflow; !got.Equal(dec("590")) {
		t.Fatalf("credit_days: bucket 2 inflow = %s, want 590 (credit_days not read?)", got)
	}
	if got := p.Buckets[4].Inflow; !got.IsZero() {
		t.Fatalf("credit_days: bucket 4 inflow = %s, want 0 (bill placed by the +30 default?)", got)
	}
	// INV-1 (due 2025-05-10 = day 9) -> bucket 1.
	if got := p.Buckets[1].Inflow; !got.Equal(dec("1180")) {
		t.Fatalf("bucket 1 inflow = %s, want 1180", got)
	}
}

func TestAnomaliesLoader(t *testing.T) {
	pool, ctx, companyID, ids := setup(t)
	led := ledger.NewService(pool)

	acme, err := ids.CreateLedger(ctx, "Acme Corp", "Sundry Debtors")
	if err != nil {
		t.Fatalf("debtor: %v", err)
	}
	sales, err := ids.CreateLedger(ctx, "Domestic Sales", "Sales Accounts")
	if err != nil {
		t.Fatalf("sales: %v", err)
	}
	salesVT, err := ids.VoucherTypeID(ctx, "Sales")
	if err != nil {
		t.Fatalf("sales vt: %v", err)
	}
	// A 2025-dated voucher entered now() (created_at defaults to now()) is > 7 days
	// backdated -> a deterministic BACKDATED finding regardless of wall clock.
	if _, err := led.PostVoucher(ctx, ledger.VoucherInput{
		Class: ledger.Sales, VoucherTypeID: salesVT, PartyLedgerID: acme,
		VoucherNo: "OLD-1", VoucherDate: "2025-05-01", Party: "Acme Corp", CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: acme, DrCr: ledger.Debit, Amount: dec("100")},
			{LedgerID: sales, DrCr: ledger.Credit, Amount: dec("100")},
		},
	}); err != nil {
		t.Fatalf("post OLD-1: %v", err)
	}

	findings, err := forecast.NewService(pool).Anomalies(ctx, companyID, "", "")
	if err != nil {
		t.Fatalf("anomalies: %v", err)
	}
	var backdated bool
	for _, f := range findings {
		if f.Kind == forecast.KindBackdated && f.VoucherNo == "OLD-1" {
			backdated = true
		}
	}
	if !backdated {
		t.Fatalf("expected a BACKDATED finding for OLD-1; got %+v", findings)
	}
}

// TestForecastDoesNotOverCountAlteredOrDeletedBills settles a documented — and,
// it turns out, STALE — disclosure.
//
// `service.go` warned that the projection's amounts come from
// reporting.Outstandings "which sums bill_allocation across ALL versions (no
// voucher_head current-version join)", so a bill on an ALTERed voucher was
// counted twice (old + new) and a DELETED one aged forever. That was true when
// the forecast was written; the demo-remediation session then fixed
// reporting.Outstandings itself (it now joins voucher_head on
// current_version_id with is_deleted = false), which silently fixed the
// forecast too — and the comment stayed.
//
// A stale comment claiming a defect that no longer exists is not harmless: it
// sends the next engineer to fix working code, or makes them distrust correct
// numbers. So rather than delete the warning on the strength of a grep, this
// asserts the property directly — and it will FAIL if anyone reintroduces the
// all-versions universe.
func TestForecastDoesNotOverCountAlteredOrDeletedBills(t *testing.T) {
	pool, ctx, companyID, ids := setup(t)
	led := ledger.NewService(pool)

	acme, err := ids.CreateLedger(ctx, "Acme Corp", "Sundry Debtors")
	if err != nil {
		t.Fatalf("create debtor: %v", err)
	}
	sales, err := ids.CreateLedger(ctx, "Domestic Sales", "Sales Accounts")
	if err != nil {
		t.Fatalf("create sales: %v", err)
	}
	salesVT, err := ids.VoucherTypeID(ctx, "Sales")
	if err != nil {
		t.Fatalf("sales vt: %v", err)
	}

	post := func(no, amount string) uuid.UUID {
		res, err := led.PostVoucher(ctx, ledger.VoucherInput{
			Class: ledger.Sales, VoucherTypeID: salesVT, PartyLedgerID: acme,
			VoucherNo: no, VoucherDate: "2025-05-01", Party: "Acme Corp", CreatedBy: "tester",
			Postings: []ledger.PostingInput{
				{LedgerID: acme, DrCr: ledger.Debit, Amount: dec(amount)},
				{LedgerID: sales, DrCr: ledger.Credit, Amount: dec(amount)},
			},
			BillAllocations: []ledger.BillAllocationInput{
				{LedgerID: acme, RefName: no, RefType: ledger.RefNew,
					DrCr: ledger.Debit, Amount: dec(amount), DueDate: "2025-05-10"},
			},
		})
		if err != nil {
			t.Fatalf("post %s: %v", no, err)
		}
		return res.VoucherID
	}

	inflow := func() decimal.Decimal {
		p, err := forecast.NewService(pool).CashFlow(ctx, companyID, "2025-05-01", 90, "week")
		if err != nil {
			t.Fatalf("cashflow: %v", err)
		}
		total := p.Beyond.Inflow
		for _, b := range p.Buckets {
			total = total.Add(b.Inflow)
		}
		return total
	}

	// ALTERED: 1000 restated to 400. The projection must show 400, NOT 1400.
	altered := post("INV-A", "1000")
	if got := inflow(); !got.Equal(dec("1000")) {
		t.Fatalf("after posting 1000 the projected inflow is %s, want 1000", got)
	}
	if _, err := led.AlterVoucher(ctx, altered, ledger.VoucherInput{
		Class: ledger.Sales, VoucherTypeID: salesVT, PartyLedgerID: acme,
		VoucherNo: "INV-A", VoucherDate: "2025-05-01", Party: "Acme Corp", CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: acme, DrCr: ledger.Debit, Amount: dec("400")},
			{LedgerID: sales, DrCr: ledger.Credit, Amount: dec("400")},
		},
		BillAllocations: []ledger.BillAllocationInput{
			{LedgerID: acme, RefName: "INV-A", RefType: ledger.RefNew,
				DrCr: ledger.Debit, Amount: dec("400"), DueDate: "2025-05-10"},
		},
	}); err != nil {
		t.Fatalf("alter: %v", err)
	}
	if got := inflow(); !got.Equal(dec("400")) {
		t.Fatalf("after restating 1000 -> 400 the projected inflow is %s, want 400 "+
			"(1400 means both versions were counted — the documented over-count)", got)
	}

	// DELETED: the bill leaves the projection entirely, rather than ageing forever.
	deleted := post("INV-B", "250")
	if got := inflow(); !got.Equal(dec("650")) {
		t.Fatalf("after adding 250 the projected inflow is %s, want 650", got)
	}
	if _, err := led.DeleteVoucher(ctx, deleted, "tester"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got := inflow(); !got.Equal(dec("400")) {
		t.Fatalf("after DELETING the 250 bill the projected inflow is %s, want 400 "+
			"(a deleted voucher must not go on ageing)", got)
	}

	// The invariance the forecast is built on still holds — the projection and
	// the Outstandings report agree, now that both use current versions.
	recv, err := reporting.NewService(pool).Outstandings(
		ctx, companyID, reporting.Receivables, "2025-05-01", nil)
	if err != nil {
		t.Fatalf("outstandings: %v", err)
	}
	if !recv.Total.Equal(dec("400")) {
		t.Fatalf("reporting receivables total = %s, want 400", recv.Total)
	}
	if !inflow().Equal(recv.Total) {
		t.Fatalf("invariance broken: projected %s vs reporting %s", inflow(), recv.Total)
	}
}

// TestForecastHarnessRemovesItsCompany is the anti-rot guard on setup()'s
// teardown: it proves the harness still registers it. Forgetting the call breaks
// no assertion anywhere — it simply buries another company and its whole seeded
// chart of accounts on every run, which is how the shared dev database reached
// 43,419 companies and 603 MB of account_group.
//
// The child tables are counted as well as `company`, because notification_outbox
// is the ONLY foreign key referencing company(id) and its action is NO ACTION:
// dropping the parent row alone orphans account_group, ledger, voucher_type and
// acl_rule with no error at all.
func TestForecastHarnessRemovesItsCompany(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	// Cleanups registered on the inner *testing.T have all run by the time t.Run
	// returns, so the harness has had its chance before the next line.
	var cid uuid.UUID
	t.Run("harness", func(t *testing.T) { _, _, cid, _ = setup(t) })
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
				"the forecast harness leaks them on EVERY run and nothing will ever delete them",
				tbl, n, cid)
		}
	}
}
