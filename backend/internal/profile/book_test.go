package profile_test

// book_test.go seeds ONE year of books a trading company would recognise, so
// the feature jobs have real shapes to find: a monthly customer who pays in 30
// days and a slow one who pays in 60-95; a rent stream with the ±2-day slips
// real rent has; a supplier whose rate drifts +10% over the year and one whose
// rate falls; an A item sold every month and a C item sold once.

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/orders"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/migrations"
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func appDSN() string {
	if v := os.Getenv("DATABASE_URL"); v != "" {
		return v
	}
	return testdb.AppDSN
}

// setup migrates and mints a company. The handler_itc_test.go shape:
// CORED_SKIP_DB set -> skip; a database that is UNREACHABLE while it is unset
// -> Fatal, because a skipped reconciliation gate is a green run that
// reconciled nothing.
func setup(t *testing.T, name string) (*db.Pool, context.Context, uuid.UUID, *identity.Service) {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("CORED_SKIP_DB is set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, testdb.AdminDSNValue())
	if err != nil {
		t.Fatalf("admin postgres unreachable and CORED_SKIP_DB is not set: %v", err)
	}
	if err := migrations.Apply(ctx, admin); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("apply migrations: %v", err)
	}
	_ = admin.Close(ctx)
	pool, err := db.New(ctx, appDSN())
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}
	t.Cleanup(pool.Close)
	cid := uuid.Must(uuid.NewV7())
	cctx := db.WithCompany(ctx, cid)
	ids := identity.NewService(pool)
	if _, err := ids.CreateCompany(cctx, identity.Company{ID: cid, Name: name + " " + cid.String()[:8], FYStartDate: "2025-04-01"}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	testdb.Cleanup(t, cid)
	return pool, cctx, cid, ids
}

type book struct {
	pool *db.Pool
	ctx  context.Context
	cid  uuid.UUID
	led  *ledger.Service

	kaveri, ganga, bharat, deccan, sales, purchases, rent, bank, capital uuid.UUID
	widget, gadget, bolt                                                 uuid.UUID
	n                                                                    int
}

func (b *book) no(prefix string) string { b.n++; return fmt.Sprintf("%s-%03d", prefix, b.n) }

func newBook(t *testing.T, name string) *book {
	t.Helper()
	pool, ctx, cid, ids := setup(t, name)
	b := &book{pool: pool, ctx: ctx, cid: cid, led: ledger.NewService(pool)}
	mk := func(n, g string) uuid.UUID {
		id, err := ids.CreateLedger(ctx, n, g)
		if err != nil {
			t.Fatalf("create ledger %s: %v", n, err)
		}
		return id
	}
	b.kaveri = mk("Kaveri Traders", "Sundry Debtors")
	b.ganga = mk("Ganga Stores", "Sundry Debtors")
	b.bharat = mk("Bharat Metals", "Sundry Creditors")
	b.deccan = mk("Deccan Steel", "Sundry Creditors")
	b.sales = mk("Sale of Goods", "Sales Accounts")
	b.purchases = mk("Purchase of Goods", "Purchase Accounts")
	b.rent = mk("Rent Paid", "Indirect Expenses")
	b.bank = mk("HDFC Current", "Bank Accounts")
	b.capital = mk("Owner Capital", "Capital Account")
	b.widget = b.item(t, "Widget")
	b.gadget = b.item(t, "Gadget")
	b.bolt = b.item(t, "Bolt")
	return b
}

func (b *book) item(t *testing.T, name string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if err := b.pool.RunInTx(b.ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO stock_item (id, company_id, name) VALUES ($1, $2, $3)`, id, b.cid, name)
		return err
	}); err != nil {
		t.Fatalf("item %s: %v", name, err)
	}
	return id
}

func (b *book) post(t *testing.T, in ledger.VoucherInput) uuid.UUID {
	t.Helper()
	in.CreatedBy = "profile-test"
	res, err := b.led.PostVoucher(b.ctx, in)
	if err != nil {
		t.Fatalf("post %s %s on %s: %v", in.Class, in.VoucherNo, in.VoucherDate, err)
	}
	return res.VoucherID
}

func line(item uuid.UUID, dir ledger.Direction, qty, rate string) ledger.InventoryLine {
	q, r := dec(qty), dec(rate)
	return ledger.InventoryLine{StockItemID: item, Direction: dir, Qty: q, Rate: r, Amount: q.Mul(r).Round(4)}
}

// purchaseOrder places a one-line PURCHASE order and returns the line's id,
// for a later purchase to fulfil.
func (b *book) purchaseOrder(t *testing.T, supplier, item uuid.UUID, date, qty, rate string) uuid.UUID {
	t.Helper()
	svc := orders.NewService(b.pool)
	id, err := svc.Create(b.ctx, orders.Input{Direction: orders.Purchase, OrderNo: b.no("PO"), OrderDate: date,
		PartyLedgerID: supplier, CreatedBy: "profile-test",
		Lines: []orders.LineInput{{StockItemID: item, Qty: dec(qty), Rate: dec(rate)}}})
	if err != nil {
		t.Fatalf("purchase order on %s: %v", date, err)
	}
	o, err := svc.Get(b.ctx, id)
	if err != nil || len(o.Lines) != 1 {
		t.Fatalf("read back purchase order: %v", err)
	}
	return o.Lines[0].ID
}

// fulfil links a purchase's single inventory line to an order line.
func fulfil(orderLine uuid.UUID, qty string) ledger.FulfilmentInput {
	return ledger.FulfilmentInput{LineNo: 1, OrderLineID: orderLine, Qty: dec(qty)}
}

func (b *book) purchase(t *testing.T, supplier uuid.UUID, supplierName, date, ref string, l ledger.InventoryLine, fs ...ledger.FulfilmentInput) uuid.UUID {
	t.Helper()
	return b.post(t, b.purchaseInput(supplier, supplierName, date, ref, l, fs...))
}

func (b *book) purchaseInput(supplier uuid.UUID, supplierName, date, ref string, l ledger.InventoryLine, fs ...ledger.FulfilmentInput) ledger.VoucherInput {
	return ledger.VoucherInput{
		Class: ledger.Purchase, VoucherNo: b.no("PUR"), VoucherDate: date, Party: supplierName, PartyLedgerID: supplier,
		Postings: []ledger.PostingInput{
			{LedgerID: b.purchases, DrCr: ledger.Debit, Amount: l.Amount},
			{LedgerID: supplier, DrCr: ledger.Credit, Amount: l.Amount},
		},
		BillAllocations: []ledger.BillAllocationInput{{LedgerID: supplier, RefName: ref, RefType: ledger.RefNew, DrCr: ledger.Credit, Amount: l.Amount}},
		Inventory:       []ledger.InventoryLine{l},
		Fulfilments:     fs,
	}
}

func (b *book) sale(t *testing.T, customer uuid.UUID, customerName, date, ref string, l ledger.InventoryLine) {
	t.Helper()
	b.post(t, ledger.VoucherInput{
		Class: ledger.Sales, VoucherNo: b.no("INV"), VoucherDate: date, Party: customerName, PartyLedgerID: customer,
		Postings: []ledger.PostingInput{
			{LedgerID: customer, DrCr: ledger.Debit, Amount: l.Amount},
			{LedgerID: b.sales, DrCr: ledger.Credit, Amount: l.Amount},
		},
		BillAllocations: []ledger.BillAllocationInput{{LedgerID: customer, RefName: ref, RefType: ledger.RefNew, DrCr: ledger.Debit, Amount: l.Amount}},
		Inventory:       []ledger.InventoryLine{l},
	})
}

func (b *book) receipt(t *testing.T, customer uuid.UUID, date, ref, amount string) {
	t.Helper()
	a := dec(amount)
	b.post(t, ledger.VoucherInput{
		Class: ledger.Receipt, VoucherNo: b.no("RCP"), VoucherDate: date, PartyLedgerID: customer,
		Postings: []ledger.PostingInput{
			{LedgerID: b.bank, DrCr: ledger.Debit, Amount: a},
			{LedgerID: customer, DrCr: ledger.Credit, Amount: a},
		},
		BillAllocations: []ledger.BillAllocationInput{{LedgerID: customer, RefName: ref, RefType: ledger.RefAgainst, DrCr: ledger.Credit, Amount: a}},
	})
}

func (b *book) payBill(t *testing.T, supplier uuid.UUID, date, ref, amount string) {
	t.Helper()
	a := dec(amount)
	b.post(t, ledger.VoucherInput{
		Class: ledger.Payment, VoucherNo: b.no("PAY"), VoucherDate: date, PartyLedgerID: supplier,
		Postings: []ledger.PostingInput{
			{LedgerID: supplier, DrCr: ledger.Debit, Amount: a},
			{LedgerID: b.bank, DrCr: ledger.Credit, Amount: a},
		},
		BillAllocations: []ledger.BillAllocationInput{{LedgerID: supplier, RefName: ref, RefType: ledger.RefAgainst, DrCr: ledger.Debit, Amount: a}},
	})
}

func (b *book) payRent(t *testing.T, date, amount string) {
	t.Helper()
	a := dec(amount)
	b.post(t, ledger.VoucherInput{
		Class: ledger.Payment, VoucherNo: b.no("PAY"), VoucherDate: date,
		Postings: []ledger.PostingInput{
			{LedgerID: b.rent, DrCr: ledger.Debit, Amount: a},
			{LedgerID: b.bank, DrCr: ledger.Credit, Amount: a},
		},
	})
}

// seed writes FY 2025-26. The hand-computed expectations the DB test pins are
// derived in comments beside each block.
func (b *book) seed(t *testing.T) {
	t.Helper()
	// Capital into the bank so no payment drives it negative.
	b.post(t, ledger.VoucherInput{Class: ledger.Journal, VoucherNo: b.no("CAP"), VoucherDate: "2025-04-01",
		Postings: []ledger.PostingInput{
			{LedgerID: b.bank, DrCr: ledger.Debit, Amount: dec("1000000")},
			{LedgerID: b.capital, DrCr: ledger.Credit, Amount: dec("1000000")},
		}})

	// SUPPLIERS. Bharat: Widget 100@50, 100@52, 100@55 -> drift +10.00%,
	// amount 15700; each bill paid after 45 days -> payment lag 45.
	// Deccan: Gadget 50@200, 50@190 -> -5.00%; Bolt 1000@2 (C item); never paid.
	//
	// LEAD TIMES (W3 wave 2): every purchase fulfils a purchase order line.
	// Bharat's three orders, each received in full by one purchase:
	//   PO 2025-03-25 -> BM-1 2025-04-02 =  8 days (ordered BEFORE the window;
	//                                           completed inside it, so counted)
	//   PO 2025-06-20 -> BM-2 2025-07-02 = 12 days
	//   PO 2025-09-12 -> BM-3 2025-10-02 = 20 days
	//   -> p50 12 (rank ceil(1.5) = 2), p90 20 (rank ceil(2.7) = 3), 3 lines.
	// Deccan's Gadget order of 100 (2025-04-01) arrives in TWO drops, 50 on
	// 04-10 and 50 on 09-10: COMPLETED 09-10 -> 162 days, one line — never the
	// 9 days of the first drop. Its Bolt order of 2000 got 1000: never
	// completed, so no lead time. DS-1 is then ALTERED (re-stated, same
	// content): with the current-version join missing, its fulfilment would
	// count twice (50 + 50 on 04-10) and the Gadget line would "complete" in
	// 9 days — the mutation that join exists for.
	b.purchase(t, b.bharat, "Bharat Metals", "2025-04-02", "BM-1", line(b.widget, ledger.Inward, "100", "50"),
		fulfil(b.purchaseOrder(t, b.bharat, b.widget, "2025-03-25", "100", "50"), "100"))
	b.purchase(t, b.bharat, "Bharat Metals", "2025-07-02", "BM-2", line(b.widget, ledger.Inward, "100", "52"),
		fulfil(b.purchaseOrder(t, b.bharat, b.widget, "2025-06-20", "100", "52"), "100"))
	b.purchase(t, b.bharat, "Bharat Metals", "2025-10-02", "BM-3", line(b.widget, ledger.Inward, "100", "55"),
		fulfil(b.purchaseOrder(t, b.bharat, b.widget, "2025-09-12", "100", "55"), "100"))
	b.payBill(t, b.bharat, "2025-05-17", "BM-1", "5000")
	b.payBill(t, b.bharat, "2025-08-16", "BM-2", "5200")
	b.payBill(t, b.bharat, "2025-11-16", "BM-3", "5500")
	gadgetPO := b.purchaseOrder(t, b.deccan, b.gadget, "2025-04-01", "100", "200")
	boltPO := b.purchaseOrder(t, b.deccan, b.bolt, "2025-04-25", "2000", "2")
	ds1In := b.purchaseInput(b.deccan, "Deccan Steel", "2025-04-10", "DS-1", line(b.gadget, ledger.Inward, "50", "200"), fulfil(gadgetPO, "50"))
	ds1 := b.post(t, ds1In)
	b.purchase(t, b.deccan, "Deccan Steel", "2025-09-10", "DS-2", line(b.gadget, ledger.Inward, "50", "190"), fulfil(gadgetPO, "50"))
	b.purchase(t, b.deccan, "Deccan Steel", "2025-05-01", "DS-3", line(b.bolt, ledger.Inward, "1000", "2"), fulfil(boltPO, "1000"))
	ds1In.CreatedBy = "profile-test"
	ds1In.Narration = "re-stated"
	if _, err := b.led.AlterVoucher(b.ctx, ds1, ds1In); err != nil {
		t.Fatalf("alter DS-1: %v", err)
	}

	// CUSTOMERS. Kaveri: 20 Widget @80 = 1600 on the 15th of every month,
	// Apr..Mar (12 invoices, 19200); each paid 30 days later EXCEPT Feb and
	// Mar -> receivable 3200, lags ten 30s (Apr->May 15 is 30 days, and every
	// 15th-to-15th gap is 28..31: measured per bill below), DSO = 3200 * 365 /
	// 19200 = 60.8.
	months := []string{"2025-04", "2025-05", "2025-06", "2025-07", "2025-08", "2025-09",
		"2025-10", "2025-11", "2025-12", "2026-01", "2026-02", "2026-03"}
	for i, mo := range months {
		ref := fmt.Sprintf("KT-%02d", i+1)
		b.sale(t, b.kaveri, "Kaveri Traders", mo+"-15", ref, line(b.widget, ledger.Outward, "20", "80"))
		if i < 10 {
			b.receipt(t, b.kaveri, months[i+1]+"-15", ref, "1600")
		}
	}
	// Ganga: Gadget 10@300 on Apr 20 (paid Jun 19: 60 days), Jul 20 (paid
	// Oct 23: 95 days), Dec 20 (open) -> receivable 3000, lags {60, 95}.
	b.sale(t, b.ganga, "Ganga Stores", "2025-04-20", "GS-1", line(b.gadget, ledger.Outward, "10", "300"))
	b.receipt(t, b.ganga, "2025-06-19", "GS-1", "3000")
	b.sale(t, b.ganga, "Ganga Stores", "2025-07-20", "GS-2", line(b.gadget, ledger.Outward, "10", "300"))
	b.receipt(t, b.ganga, "2025-10-23", "GS-2", "3000")
	b.sale(t, b.ganga, "Ganga Stores", "2025-12-20", "GS-3", line(b.gadget, ledger.Outward, "10", "300"))
	// One Bolt sale (C item, one month of twelve -> Z).
	b.sale(t, b.kaveri, "Kaveri Traders", "2025-08-01", "KT-B", line(b.bolt, ledger.Outward, "100", "3"))

	// RENT: 25,000 on the 5th, with real slips (+2, -1, +1 days) and one
	// 25,500 (+2%), Apr..Mar = 12 occurrences -> one monthly stream.
	rentDays := []string{"05", "07", "05", "04", "05", "06", "05", "05", "05", "05", "05", "05"}
	for i, mo := range months {
		amt := "25000"
		if i == 6 {
			amt = "25500"
		}
		b.payRent(t, mo+"-"+rentDays[i], amt)
	}
	// A ONE-OFF to the same ledger (a repair, 20 March): it must break the
	// run, never join it — and it gives the stream reconciler a neighbour, so
	// "grouped a one-off in" is a failure the gate can actually see.
	b.payRent(t, "2026-03-20", "8000")
}
