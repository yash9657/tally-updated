package reporting_test

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
	"github.com/khata/cored/internal/reporting"
	"github.com/khata/cored/migrations"
)

const (
	adminDSN = "postgres://postgres:postgres@localhost:5433/tally?sslmode=disable"
	appDSN   = "postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable"
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// setup migrates the live DB (idempotent) and returns an app-scoped pool plus a
// freshly created, seeded company in a unique tenant scope.
func setup(t *testing.T) (*db.Pool, context.Context, uuid.UUID, *identity.Service) {
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
		Name:        "Report Co " + companyID.String()[:8],
		FYStartDate: "2025-04-01",
	}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	return pool, cctx, companyID, ids
}

// seedBooks posts a representative set of vouchers and returns the ids the tests
// assert against. The scenario:
//   - Sale of 1000 + 18% GST (CGST 90 + SGST 90) to Acme on credit, bill "INV-1".
//   - Purchase of 500 + 18% GST (90 split) from Globex on credit, bill "PB-1".
//   - Receipt of 600 from Acme against INV-1 (partial settle).
//   - Cash payment of 200 of indirect expense (Rent).
func seedBooks(t *testing.T, pool *db.Pool, ctx context.Context, ids *identity.Service) ledgerIDs {
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
	receiptVT, err := ids.VoucherTypeID(ctx, "Receipt")
	if err != nil {
		t.Fatalf("receipt vt: %v", err)
	}
	paymentVT, err := ids.VoucherTypeID(ctx, "Payment")
	if err != nil {
		t.Fatalf("payment vt: %v", err)
	}

	var l ledgerIDs
	mk := func(name, group string) uuid.UUID {
		id, err := ids.CreateLedger(ctx, name, group)
		if err != nil {
			t.Fatalf("create ledger %s: %v", name, err)
		}
		return id
	}
	l.acme = mk("Acme Corp", "Sundry Debtors")
	l.sales = mk("Domestic Sales", "Sales Accounts")
	l.ocgst = mk("Output CGST", "Duties & Taxes")
	l.osgst = mk("Output SGST", "Duties & Taxes")
	l.globex = mk("Globex Ltd", "Sundry Creditors")
	l.purchase = mk("Local Purchase", "Purchase Accounts")
	l.icgst = mk("Input CGST", "Duties & Taxes")
	l.isgst = mk("Input SGST", "Duties & Taxes")
	l.rent = mk("Rent Paid", "Indirect Expenses")

	cash, err := ids.LedgerID(ctx, "Cash")
	if err != nil {
		t.Fatalf("cash ledger: %v", err)
	}
	l.cash = cash

	// 1) Sale to Acme: Dr Acme 1180; Cr Sales 1000, CGST 90, SGST 90. New bill.
	if _, err := led.PostVoucher(ctx, ledger.VoucherInput{
		Class:         ledger.Sales,
		VoucherTypeID: salesVT,
		PartyLedgerID: l.acme,
		VoucherNo:     "INV-1",
		VoucherDate:   "2025-04-10",
		Party:         "Acme Corp",
		Narration:     "Sale to Acme",
		CreatedBy:     "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: l.acme, DrCr: ledger.Debit, Amount: dec("1180")},
			{LedgerID: l.sales, DrCr: ledger.Credit, Amount: dec("1000")},
			{LedgerID: l.ocgst, DrCr: ledger.Credit, Amount: dec("90")},
			{LedgerID: l.osgst, DrCr: ledger.Credit, Amount: dec("90")},
		},
		BillAllocations: []ledger.BillAllocationInput{
			{LedgerID: l.acme, RefName: "INV-1", RefType: ledger.RefNew,
				DrCr: ledger.Debit, Amount: dec("1180"), DueDate: "2025-05-10"},
		},
	}); err != nil {
		t.Fatalf("post sale: %v", err)
	}

	// 2) Purchase from Globex: Dr Purchase 500, CGST 45, SGST 45; Cr Globex 590.
	if _, err := led.PostVoucher(ctx, ledger.VoucherInput{
		Class:         ledger.Purchase,
		VoucherTypeID: purchaseVT,
		PartyLedgerID: l.globex,
		VoucherNo:     "PB-1",
		VoucherDate:   "2025-04-12",
		Party:         "Globex Ltd",
		Narration:     "Purchase from Globex",
		CreatedBy:     "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: l.purchase, DrCr: ledger.Debit, Amount: dec("500")},
			{LedgerID: l.icgst, DrCr: ledger.Debit, Amount: dec("45")},
			{LedgerID: l.isgst, DrCr: ledger.Debit, Amount: dec("45")},
			{LedgerID: l.globex, DrCr: ledger.Credit, Amount: dec("590")},
		},
		BillAllocations: []ledger.BillAllocationInput{
			{LedgerID: l.globex, RefName: "PB-1", RefType: ledger.RefNew,
				DrCr: ledger.Credit, Amount: dec("590"), DueDate: "2025-05-12"},
		},
	}); err != nil {
		t.Fatalf("post purchase: %v", err)
	}

	// 3) Receipt of 600 from Acme against INV-1: Dr Cash 600; Cr Acme 600.
	if _, err := led.PostVoucher(ctx, ledger.VoucherInput{
		Class:         ledger.Receipt,
		VoucherTypeID: receiptVT,
		PartyLedgerID: l.acme,
		VoucherNo:     "RCPT-1",
		VoucherDate:   "2025-04-20",
		Party:         "Acme Corp",
		Narration:     "Part receipt from Acme",
		CreatedBy:     "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: l.cash, DrCr: ledger.Debit, Amount: dec("600")},
			{LedgerID: l.acme, DrCr: ledger.Credit, Amount: dec("600")},
		},
		BillAllocations: []ledger.BillAllocationInput{
			{LedgerID: l.acme, RefName: "INV-1", RefType: ledger.RefAgainst,
				DrCr: ledger.Credit, Amount: dec("600")},
		},
	}); err != nil {
		t.Fatalf("post receipt: %v", err)
	}

	// 4) Cash payment of 200 rent: Dr Rent 200; Cr Cash 200.
	if _, err := led.PostVoucher(ctx, ledger.VoucherInput{
		Class:         ledger.Payment,
		VoucherTypeID: paymentVT,
		VoucherNo:     "PMT-1",
		VoucherDate:   "2025-04-25",
		Narration:     "Rent paid",
		CreatedBy:     "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: l.rent, DrCr: ledger.Debit, Amount: dec("200")},
			{LedgerID: l.cash, DrCr: ledger.Credit, Amount: dec("200")},
		},
	}); err != nil {
		t.Fatalf("post payment: %v", err)
	}

	return l
}

type ledgerIDs struct {
	acme, sales, ocgst, osgst      uuid.UUID
	globex, purchase, icgst, isgst uuid.UUID
	rent, cash                     uuid.UUID
}

func TestTrialBalance_BalancesAndGroups(t *testing.T) {
	pool, ctx, companyID, ids := setup(t)
	seedBooks(t, pool, ctx, ids)
	rep := reporting.NewService(pool)

	tb, err := rep.TrialBalance(ctx, companyID, "")
	if err != nil {
		t.Fatalf("trial balance: %v", err)
	}
	if !tb.IsBalanced() {
		t.Fatalf("trial balance not balanced: dr=%s cr=%s", tb.TotalDebit, tb.TotalCredit)
	}
	if len(tb.Groups) == 0 {
		t.Fatal("expected group-wise rows")
	}
	// Every group's debit/credit must equal the sum of its ledger rows.
	for _, g := range tb.Groups {
		dr, cr := decimal.Zero, decimal.Zero
		for _, r := range g.Ledgers {
			dr = dr.Add(r.Debit)
			cr = cr.Add(r.Credit)
		}
		if !dr.Equal(g.Debit) || !cr.Equal(g.Credit) {
			t.Fatalf("group %s rollup mismatch", g.GroupName)
		}
	}
}

func TestProfitAndLoss_NetsCorrectly(t *testing.T) {
	pool, ctx, companyID, ids := setup(t)
	seedBooks(t, pool, ctx, ids)
	rep := reporting.NewService(pool)

	pl, err := rep.ProfitAndLoss(ctx, companyID, "", "")
	if err != nil {
		t.Fatalf("p&l: %v", err)
	}
	// Income: Sales 1000. Expenses: Purchase 500 + Rent 200 = 700.
	if !pl.Income.Total.Equal(dec("1000")) {
		t.Fatalf("income expected 1000, got %s", pl.Income.Total)
	}
	if !pl.Expenses.Total.Equal(dec("700")) {
		t.Fatalf("expenses expected 700, got %s", pl.Expenses.Total)
	}
	// Net profit = 1000 - 700 = 300.
	if !pl.NetProfit.Equal(dec("300")) {
		t.Fatalf("net profit expected 300, got %s", pl.NetProfit)
	}
}

func TestBalanceSheet_Balances(t *testing.T) {
	pool, ctx, companyID, ids := setup(t)
	seedBooks(t, pool, ctx, ids)
	rep := reporting.NewService(pool)

	bs, err := rep.BalanceSheet(ctx, companyID, "")
	if err != nil {
		t.Fatalf("balance sheet: %v", err)
	}
	if !bs.IsBalanced() {
		t.Fatalf("balance sheet not balanced: assets=%s liabilities=%s",
			bs.Assets.Total, bs.Liabilities.Total)
	}
	// Net profit folded into liabilities must match the P&L.
	if !bs.NetProfit.Equal(dec("300")) {
		t.Fatalf("BS net profit expected 300, got %s", bs.NetProfit)
	}

	// Manually reconcile the assets side.
	// Acme debtor: 1180 sale - 600 receipt = 580.
	// Cash: 600 received - 200 rent = 400.
	// Input CGST+SGST (Duties & Taxes, debit) = 90 (asset side via current liab? no:
	// Duties & Taxes is a LIABILITY group). So input taxes net against output.
	// Assets = Acme 580 + Cash 400 = 980.
	if !bs.Assets.Total.Equal(dec("980")) {
		t.Fatalf("assets expected 980, got %s", bs.Assets.Total)
	}
	// Liabilities = Globex 590 + Duties&Taxes (Output 180 - Input 90 = 90) + Profit 300 = 980.
	if !bs.Liabilities.Total.Equal(dec("980")) {
		t.Fatalf("liabilities expected 980, got %s", bs.Liabilities.Total)
	}
}

func TestBalanceSheet_DrillDownReachesVoucher(t *testing.T) {
	pool, ctx, companyID, ids := setup(t)
	l := seedBooks(t, pool, ctx, ids)
	rep := reporting.NewService(pool)

	bs, err := rep.BalanceSheet(ctx, companyID, "")
	if err != nil {
		t.Fatalf("balance sheet: %v", err)
	}
	// Find the Acme ledger node anywhere in the Assets tree.
	var found bool
	var walk func(nodes []reporting.StatementNode)
	walk = func(nodes []reporting.StatementNode) {
		for _, n := range nodes {
			if n.Kind == reporting.NodeLedger && n.ID == l.acme {
				found = true
				if !n.Amount.Equal(dec("580")) {
					t.Fatalf("acme node amount expected 580, got %s", n.Amount)
				}
			}
			walk(n.Children)
		}
	}
	walk(bs.Assets.Nodes)
	if !found {
		t.Fatal("acme ledger node not found in balance sheet tree")
	}

	// Drill to the underlying vouchers for Acme: sale (1180 Dr) + receipt (600 Cr).
	vouchers, err := rep.LedgerVouchers(ctx, companyID, l.acme, "", "")
	if err != nil {
		t.Fatalf("ledger vouchers: %v", err)
	}
	if len(vouchers) != 2 {
		t.Fatalf("expected 2 vouchers for acme, got %d", len(vouchers))
	}
	if vouchers[0].VoucherNo != "INV-1" || vouchers[1].VoucherNo != "RCPT-1" {
		t.Fatalf("unexpected voucher order: %s, %s", vouchers[0].VoucherNo, vouchers[1].VoucherNo)
	}
	// Each voucher must carry its balanced postings.
	for _, v := range vouchers {
		if len(v.Postings) < 2 {
			t.Fatalf("voucher %s has too few postings", v.VoucherNo)
		}
		if !v.Debit.Equal(v.Credit) {
			t.Fatalf("voucher %s not balanced: dr=%s cr=%s", v.VoucherNo, v.Debit, v.Credit)
		}
	}
}

func TestDayBook_ChronologicalOrder(t *testing.T) {
	pool, ctx, companyID, ids := setup(t)
	seedBooks(t, pool, ctx, ids)
	rep := reporting.NewService(pool)

	dbk, err := rep.DayBook(ctx, companyID, "2025-04-01", "2025-04-30")
	if err != nil {
		t.Fatalf("day book: %v", err)
	}
	if len(dbk.Entries) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(dbk.Entries))
	}
	wantOrder := []string{"INV-1", "PB-1", "RCPT-1", "PMT-1"}
	for i, want := range wantOrder {
		if dbk.Entries[i].VoucherNo != want {
			t.Fatalf("entry %d expected %s, got %s", i, want, dbk.Entries[i].VoucherNo)
		}
		if dbk.Entries[i].Date == "" {
			t.Fatalf("entry %d missing date", i)
		}
		if len(dbk.Entries[i].Postings) < 2 {
			t.Fatalf("entry %d missing postings (drill-down)", i)
		}
	}

	// Period filter excludes everything outside the window.
	empty, err := rep.DayBook(ctx, companyID, "2025-06-01", "2025-06-30")
	if err != nil {
		t.Fatalf("day book empty period: %v", err)
	}
	if len(empty.Entries) != 0 {
		t.Fatalf("expected no entries in June, got %d", len(empty.Entries))
	}
}

// TestDayBook_ShowsOnlyCurrentVersion proves the current-version filter: after
// an ALTER the Day Book lists ONE entry carrying the NEW amount (not the
// original CREATE plus the ALTER as two rows), and after a DELETE the voucher
// disappears from the default Day Book entirely.
func TestDayBook_ShowsOnlyCurrentVersion(t *testing.T) {
	pool, ctx, companyID, ids := setup(t)
	led := ledger.NewService(pool)
	rep := reporting.NewService(pool)

	vtID, _ := ids.VoucherTypeID(ctx, "Sales")
	debtor, _ := ids.CreateLedger(ctx, "Delta Co", "Sundry Debtors")
	sales, _ := ids.CreateLedger(ctx, "Online Sales", "Sales Accounts")

	mk := func(amount string) ledger.VoucherInput {
		a := dec(amount)
		return ledger.VoucherInput{
			Class: ledger.Sales, VoucherTypeID: vtID, PartyLedgerID: debtor,
			VoucherNo: "INV-9", VoucherDate: "2025-04-15", Party: "Delta Co",
			Narration: "Delta sale", CreatedBy: "tester",
			Postings: []ledger.PostingInput{
				{LedgerID: debtor, DrCr: ledger.Debit, Amount: a},
				{LedgerID: sales, DrCr: ledger.Credit, Amount: a},
			},
		}
	}

	v1, err := led.PostVoucher(ctx, mk("400"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if _, err := led.AlterVoucher(ctx, v1.VoucherID, mk("650")); err != nil {
		t.Fatalf("alter: %v", err)
	}

	dbk, err := rep.DayBook(ctx, companyID, "2025-04-01", "2025-04-30")
	if err != nil {
		t.Fatalf("day book: %v", err)
	}
	var found int
	for _, e := range dbk.Entries {
		if e.VoucherID == v1.VoucherID {
			found++
			if !e.Debit.Equal(dec("650")) {
				t.Fatalf("day book entry must show the altered amount 650, got %s", e.Debit)
			}
			if e.VersionNo != 2 || e.VoucherNo != "INV-9" {
				t.Fatalf("day book entry must be the current version (no=INV-9, v2), got no=%s v=%d", e.VoucherNo, e.VersionNo)
			}
		}
	}
	if found != 1 {
		t.Fatalf("after alter the day book must list exactly ONE entry for the voucher, got %d", found)
	}

	// Delete it: it must vanish from the default Day Book.
	if _, err := led.DeleteVoucher(ctx, v1.VoucherID, "deleter"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	dbk, err = rep.DayBook(ctx, companyID, "2025-04-01", "2025-04-30")
	if err != nil {
		t.Fatalf("day book after delete: %v", err)
	}
	for _, e := range dbk.Entries {
		if e.VoucherID == v1.VoucherID {
			t.Fatalf("deleted voucher must be absent from the day book, but it appeared (op=%s)", e.Op)
		}
	}
}

func TestOutstandings_ReceivablesAndPayables(t *testing.T) {
	pool, ctx, companyID, ids := setup(t)
	l := seedBooks(t, pool, ctx, ids)
	rep := reporting.NewService(pool)

	// Receivables: Acme INV-1 = 1180 sale - 600 receipt = 580 pending.
	recv, err := rep.Outstandings(ctx, companyID, reporting.Receivables, "2025-06-13", nil)
	if err != nil {
		t.Fatalf("receivables: %v", err)
	}
	if !recv.Total.Equal(dec("580")) {
		t.Fatalf("receivables total expected 580, got %s", recv.Total)
	}
	if len(recv.Parties) != 1 || recv.Parties[0].LedgerID != l.acme {
		t.Fatalf("expected one receivable party (acme), got %+v", recv.Parties)
	}
	p := recv.Parties[0]
	if len(p.Bills) != 1 || p.Bills[0].RefName != "INV-1" {
		t.Fatalf("expected INV-1 bill, got %+v", p.Bills)
	}
	if !p.Bills[0].Pending.Equal(dec("580")) {
		t.Fatalf("INV-1 pending expected 580, got %s", p.Bills[0].Pending)
	}
	// Due 2025-05-10, as-on 2025-06-13 => 34 days overdue => 0-45 slab.
	if p.Bills[0].OverdueDays != 34 {
		t.Fatalf("expected 34 overdue days, got %d", p.Bills[0].OverdueDays)
	}
	if p.Bills[0].SlabLabel != "0-45 days" {
		t.Fatalf("expected 0-45 slab, got %s", p.Bills[0].SlabLabel)
	}

	// Payables: Globex PB-1 = 590 pending (unsettled).
	pay, err := rep.Outstandings(ctx, companyID, reporting.Payables, "2025-06-13", nil)
	if err != nil {
		t.Fatalf("payables: %v", err)
	}
	if !pay.Total.Equal(dec("590")) {
		t.Fatalf("payables total expected 590, got %s", pay.Total)
	}
	if len(pay.Parties) != 1 || pay.Parties[0].LedgerID != l.globex {
		t.Fatalf("expected one payable party (globex), got %+v", pay.Parties)
	}
}

func TestReports_RejectWrongCompanyScope(t *testing.T) {
	pool, ctx, companyID, ids := setup(t)
	seedBooks(t, pool, ctx, ids)
	rep := reporting.NewService(pool)

	other, _ := uuid.NewV7()
	if _, err := rep.TrialBalance(ctx, other, ""); err == nil {
		t.Fatal("expected scope error for mismatched company on trial balance")
	}
	if _, err := rep.BalanceSheet(ctx, other, ""); err == nil {
		t.Fatal("expected scope error for mismatched company on balance sheet")
	}
	_ = companyID
}
