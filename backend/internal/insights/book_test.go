package insights_test

// book_test.go seeds ONE year of books, dated relative to TODAY (IST), in
// which every one of the fifteen detectors has something true to find. Dates
// are relative because one detector (back-dated entries) is judged against
// when a version was WRITTEN, and created_at is the database's clock.

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/compliance"
	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/insights"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/migrations"
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// setup: CORED_SKIP_DB -> skip; an unreachable DB while it is unset -> Fatal,
// because a skipped evidence gate is a green run that proved nothing.
func setup(t *testing.T) (*db.Pool, context.Context, uuid.UUID, *identity.Service) {
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
	pool, err := db.New(ctx, testdb.AppDSNValue())
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}
	t.Cleanup(pool.Close)
	cid := uuid.Must(uuid.NewV7())
	cctx := db.WithCompany(ctx, cid)
	ids := identity.NewService(pool)
	fy := time.Now().AddDate(-1, -2, 0)
	fyStart := fmt.Sprintf("%d-04-01", fy.Year())
	if fy.Month() < time.April {
		fyStart = fmt.Sprintf("%d-04-01", fy.Year()-1)
	}
	if _, err := ids.CreateCompany(cctx, identity.Company{ID: cid, Name: "Insights " + cid.String()[:8],
		FYStartDate: fyStart, StateCode: "29", GSTIN: "29AAACI1234K1Z1"}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	testdb.Cleanup(t, cid)
	return pool, cctx, cid, ids
}

func adminExec(t *testing.T, sql string, args ...any) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, testdb.AdminDSNValue())
	if err != nil {
		t.Fatalf("admin: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("admin exec: %v\n%s", err, sql)
	}
}

type book struct {
	t    *testing.T
	pool *db.Pool
	ctx  context.Context
	cid  uuid.UUID
	ids  *identity.Service
	led  *ledger.Service
	T    time.Time // today, IST business date
	n    int

	bank, capital, sales, purchases, expense, tdsPayable     uuid.UUID
	ocgst, osgst, icgst, isgst                               uuid.UUID
	kaveri, ganga, deccanRetail, bharat, deccanSteel, chitra uuid.UUID
	widget, gadget, bolt                                     uuid.UUID
	godown                                                   uuid.UUID
	vouchers                                                 map[string]uuid.UUID
}

func (b *book) day(n int) string   { return b.T.AddDate(0, 0, n).Format("2006-01-02") }
func (b *book) no(p string) string { b.n++; return fmt.Sprintf("%s-%03d", p, b.n) }

func newBook(t *testing.T) *book {
	t.Helper()
	pool, ctx, cid, ids := setup(t)
	b := &book{t: t, pool: pool, ctx: ctx, cid: cid, ids: ids, led: ledger.NewService(pool),
		T: insights.BusinessDate(time.Now()), vouchers: map[string]uuid.UUID{}}
	mk := func(n, g string) uuid.UUID {
		id, err := ids.CreateLedger(ctx, n, g)
		if err != nil {
			t.Fatalf("ledger %s: %v", n, err)
		}
		return id
	}
	b.bank = mk("HDFC Current", "Bank Accounts")
	b.capital = mk("Owner Capital", "Capital Account")
	b.sales = mk("Sale of Goods", "Sales Accounts")
	b.purchases = mk("Purchase of Goods", "Purchase Accounts")
	b.expense = mk("Contract Charges", "Indirect Expenses")
	b.tdsPayable = mk("TDS Payable", "Duties & Taxes")
	b.ocgst = mk("Output CGST", "Duties & Taxes")
	b.osgst = mk("Output SGST", "Duties & Taxes")
	b.icgst = mk("Input CGST", "Duties & Taxes")
	b.isgst = mk("Input SGST", "Duties & Taxes")
	b.kaveri = mk("Kaveri Traders", "Sundry Debtors")
	b.deccanRetail = mk("Deccan Retail", "Sundry Debtors")
	b.bharat = mk("Bharat Metals", "Sundry Creditors")
	b.deccanSteel = mk("Deccan Steel", "Sundry Creditors")
	b.chitra = mk("Chitra Contractors", "Sundry Creditors")
	// Ganga through the MASTERS path, with a credit limit (0103): the field
	// is what the breach detector reads, and it is set the way an operator
	// sets it.
	limit := dec("10000")
	var err error
	b.ganga, err = masters.NewService(pool).CreateLedger(ctx, masters.Ledger{Name: "Ganga Stores",
		GroupName: "Sundry Debtors", OpeningDC: "D", CreditLimit: &limit, GSTIN: "29AAGCG1111L1Z3", StateCode: "29", RegistrationType: masters.GSTRegistrationType("REGULAR")}, "insights-test")
	if err != nil {
		t.Fatalf("ganga: %v", err)
	}
	b.exec(`UPDATE ledger SET gstin = '27ABCDE1234F1Z5', state_code = '27' WHERE id = $1`, b.deccanRetail)
	b.exec(`UPDATE ledger SET gstin = '29AAAPB1234C1Z5', state_code = '29' WHERE id = $1`, b.bharat)
	b.exec(`UPDATE ledger SET gstin = '29AABCD5678E1Z2', state_code = '29' WHERE id = $1`, b.deccanSteel)
	b.exec(`UPDATE ledger SET pan = 'AAAPC1234C' WHERE id = $1`, b.chitra)
	b.widget = b.item("Widget", "8471", "18")
	b.gadget = b.item("Gadget", "8517", "18")
	b.bolt = b.item("Bolt", "7318", "18")
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id FROM godown WHERE name = 'Main Location'`).Scan(&b.godown)
	}); err != nil {
		t.Fatalf("godown: %v", err)
	}
	return b
}

func (b *book) exec(sql string, args ...any) {
	b.t.Helper()
	if err := b.pool.RunInTx(b.ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, sql, args...)
		return err
	}); err != nil {
		b.t.Fatalf("exec %s: %v", sql, err)
	}
}

func (b *book) item(name, hsn, rate string) uuid.UUID {
	id := uuid.Must(uuid.NewV7())
	b.exec(`INSERT INTO stock_item (id, company_id, name, hsn, gst_rate_pct) VALUES ($1, $2, $3, $4, $5)`,
		id, b.cid, name, hsn, dec(rate))
	return id
}

func (b *book) post(key string, in ledger.VoucherInput) uuid.UUID {
	b.t.Helper()
	in.CreatedBy = "insights-test"
	if in.VoucherNo == "" {
		in.VoucherNo = b.no(string(in.Class)[:3])
	}
	res, err := b.led.PostVoucher(b.ctx, in)
	if err != nil {
		b.t.Fatalf("post %s (%s %s): %v", key, in.Class, in.VoucherDate, err)
	}
	if key != "" {
		b.vouchers[key] = res.VoucherID
	}
	return res.VoucherID
}

func (b *book) inv(item uuid.UUID, d ledger.Direction, qty, rate string) ledger.InventoryLine {
	return ledger.InventoryLine{StockItemID: item, GodownID: b.godown, Direction: d, Qty: dec(qty), Rate: dec(rate)}
}

// sale is an intra-State item invoice at 18%, bill-wise.
func (b *book) sale(key string, party uuid.UUID, name, date, ref, due, pos string, l ledger.InventoryLine) uuid.UUID {
	total := l.Qty.Mul(l.Rate).Mul(dec("1.18")).Round(2)
	return b.post(key, ledger.VoucherInput{Class: ledger.Sales, VoucherDate: date, Party: name,
		PartyLedgerID: party, ItemLedgerID: b.sales, Inventory: []ledger.InventoryLine{l},
		BillAllocations: []ledger.BillAllocationInput{{LedgerID: party, RefName: ref, RefType: ledger.RefNew,
			DrCr: ledger.Debit, Amount: total, DueDate: due}},
		GST: &ledger.GSTParams{SupplierStateCode: "29", PlaceOfSupplyStateCode: pos,
			CGSTLedgerID: b.ocgst, SGSTLedgerID: b.osgst}})
}

func (b *book) purchase(key string, party uuid.UUID, name, date, ref, due, supplierNo string, l ledger.InventoryLine) uuid.UUID {
	total := l.Qty.Mul(l.Rate).Mul(dec("1.18")).Round(2)
	in := ledger.VoucherInput{Class: ledger.Purchase, VoucherDate: date, Party: name,
		PartyLedgerID: party, ItemLedgerID: b.purchases, Inventory: []ledger.InventoryLine{l},
		BillAllocations: []ledger.BillAllocationInput{{LedgerID: party, RefName: ref, RefType: ledger.RefNew,
			DrCr: ledger.Credit, Amount: total, DueDate: due}},
		GST: &ledger.GSTParams{SupplierStateCode: "29", PlaceOfSupplyStateCode: "29",
			CGSTLedgerID: b.icgst, SGSTLedgerID: b.isgst}}
	if supplierNo != "" {
		in.Inward = &ledger.InwardDocument{SupplierInvoiceNo: supplierNo, SupplierInvoiceDate: date}
	}
	return b.post(key, in)
}

func (b *book) settle(class ledger.Class, party uuid.UUID, date, ref string, amount decimal.Decimal) {
	dr, cr, side := b.bank, party, ledger.Credit
	if class == ledger.Payment {
		dr, cr, side = party, b.bank, ledger.Debit
	}
	b.post("", ledger.VoucherInput{Class: class, VoucherDate: date, PartyLedgerID: party,
		Postings: []ledger.PostingInput{{LedgerID: dr, DrCr: ledger.Debit, Amount: amount},
			{LedgerID: cr, DrCr: ledger.Credit, Amount: amount}},
		BillAllocations: []ledger.BillAllocationInput{{LedgerID: party, RefName: ref, RefType: ledger.RefAgainst,
			DrCr: side, Amount: amount}}})
}

// seed writes the year. What each block is FOR is beside it.
func (b *book) seed() {
	t := b.t
	t.Helper()
	b.post("capital", ledger.VoucherInput{Class: ledger.Journal, VoucherDate: b.day(-364),
		Postings: []ledger.PostingInput{{LedgerID: b.bank, DrCr: ledger.Debit, Amount: dec("20000")},
			{LedgerID: b.capital, DrCr: ledger.Credit, Amount: dec("20000")}}})

	// PRICE CREEP: Bharat's Widget 50 -> 52 -> 55 (+10%). The first two are
	// paid; the third and the two duplicates below stay open and fall due
	// inside the cash horizon.
	b.purchase("bm1", b.bharat, "Bharat Metals", b.day(-330), "BM-1", "", "BM/1", b.inv(b.widget, ledger.Inward, "100", "50"))
	b.purchase("bm2", b.bharat, "Bharat Metals", b.day(-240), "BM-2", "", "BM/2", b.inv(b.widget, ledger.Inward, "100", "52"))
	b.purchase("bm3", b.bharat, "Bharat Metals", b.day(-120), "BM-3", b.day(10), "BM/3", b.inv(b.widget, ledger.Inward, "100", "55"))
	b.settle(ledger.Payment, b.bharat, b.day(-300), "BM-1", dec("5900"))
	b.settle(ledger.Payment, b.bharat, b.day(-200), "BM-2", dec("6136"))
	// DUPLICATE BILL: one supplier document keyed twice, spelled two ways.
	b.purchase("dup1", b.bharat, "Bharat Metals", b.day(-10), "BM-77A", b.day(5), "BM/77", b.inv(b.widget, ledger.Inward, "100", "55"))
	b.purchase("dup2", b.bharat, "Bharat Metals", b.day(-9), "BM-77B", b.day(5), "bm-77", b.inv(b.widget, ledger.Inward, "100", "55"))

	// REORDER NOW: 100 Gadgets bought, 98 sold (to Ganga, below).
	b.purchase("gad", b.deccanSteel, "Deccan Steel", b.day(-300), "DS-1", "", "DS/1", b.inv(b.gadget, ledger.Inward, "100", "200"))
	b.settle(ledger.Payment, b.deccanSteel, b.day(-290), "DS-1", dec("23600"))
	// DEAD STOCK: 1000 Bolts bought and never sold.
	b.purchase("bolt", b.deccanSteel, "Deccan Steel", b.day(-200), "DS-2", b.day(200), "DS/2", b.inv(b.bolt, ledger.Inward, "1000", "2"))
	// SUPPLIER NOT IN 2B: a Deccan Steel purchase dated in LAST month, whose
	// 2B (imported below) carries only Bharat.
	m0 := time.Date(b.T.Year(), b.T.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	b.purchase("ds2b", b.deccanSteel, "Deccan Steel", m0.Format("2006-01-02"), "DS-3", b.day(200), "DS/3", b.inv(b.bolt, ledger.Inward, "100", "2"))
	stmt := uuid.Must(uuid.NewV7())
	b.exec(`INSERT INTO gst_inward_statement (id, company_id, kind, return_period, filer_gstin, source_sha256, raw, ingested_by)
	        VALUES ($1, $2, 'GSTR2B', $3, '29AAACI1234K1Z1', repeat('b', 64), '{}'::jsonb, 'insights-test')`,
		stmt, b.cid, m0.Format("012006"))
	b.exec(`INSERT INTO gst_inward_document (id, company_id, statement_id, section, supplier_gstin, doc_no, doc_date, doc_type,
	          itc_available, taxable_value, cgst, sgst, igst)
	        VALUES ($1, $2, $3, 'B2B', '29AAAPB1234C1Z5', 'BM/9', $4, 'I', true, 100, 9, 9, 0)`,
		uuid.Must(uuid.NewV7()), b.cid, stmt, m0.Format("2006-01-02"))

	// CUSTOMER SLOWER + REORDER OVERDUE: Kaveri buys every 30 days, pays the
	// first four in 20 days (p90 20), then stops ordering and paying.
	for i := 0; i < 6; i++ {
		d := -300 + 30*i
		ref := fmt.Sprintf("KT-%d", i+1)
		b.sale(fmt.Sprintf("kt%d", i+1), b.kaveri, "Kaveri Traders", b.day(d), ref, "", "29", b.inv(b.widget, ledger.Outward, "10", "80"))
		if i < 4 {
			b.settle(ledger.Receipt, b.kaveri, b.day(d+20), ref, dec("944"))
		}
	}

	// CREDIT LIMIT + E-WAY BILL: Ganga (limit 10,000) takes 98 Gadgets,
	// 58,800 + GST, due beyond the cash horizon.
	b.sale("ganga", b.ganga, "Ganga Stores", b.day(0), "GS-1", b.day(120), "29", b.inv(b.gadget, ledger.Outward, "98", "600"))
	// PLACE OF SUPPLY: Deccan Retail's GSTIN is Maharashtra (27), the
	// invoice says Karnataka (29).
	b.sale("pos", b.deccanRetail, "Deccan Retail", b.day(0), "DR-1", b.day(120), "29", b.inv(b.widget, ledger.Outward, "5", "100"))
	// HSN RATE: a hand-keyed GST sale at 12% on HSN 7318, which the Bolt
	// master carries at 18%.
	b.post("hsn", ledger.VoucherInput{Class: ledger.Sales, VoucherDate: b.day(0), PartyLedgerID: b.deccanRetail,
		Party: "Deccan Retail",
		Postings: []ledger.PostingInput{
			{LedgerID: b.deccanRetail, DrCr: ledger.Debit, Amount: dec("1120")},
			{LedgerID: b.sales, DrCr: ledger.Credit, Amount: dec("1000")},
			{LedgerID: b.ocgst, DrCr: ledger.Credit, Amount: dec("60")},
			{LedgerID: b.osgst, DrCr: ledger.Credit, Amount: dec("60")}},
		BillAllocations: []ledger.BillAllocationInput{{LedgerID: b.deccanRetail, RefName: "DR-2", RefType: ledger.RefNew,
			DrCr: ledger.Debit, Amount: dec("1120"), DueDate: b.day(120)}},
		GST: &ledger.GSTParams{SupplierStateCode: "29", PlaceOfSupplyStateCode: "29",
			Lines:        []compliance.Line{{TaxableValue: dec("1000"), GSTRatePct: dec("12"), HSN: "7318"}},
			CGSTLedgerID: b.ocgst, SGSTLedgerID: b.osgst}})

	// TDS NOT DEPOSITED: 2% under 194C on a 50,000 contract, two months ago.
	b.tdsDeduction()
}

func (b *book) tdsDeduction() {
	on := time.Date(b.T.Year(), b.T.Month(), 10, 0, 0, 0, 0, time.UTC).AddDate(0, -2, 0)
	ty := on.Year()
	if on.Month() < time.April {
		ty--
	}
	adminExec(b.t, `INSERT INTO company_deductor_status (id, company_id, tax_year_start, entity_category, recorded_by)
		VALUES (gen_random_uuid(), $1, $2, 'COMPANY', 'test')`, b.cid, ty-1)
	b.post("tds", ledger.VoucherInput{Class: ledger.Purchase, VoucherDate: on.Format("2006-01-02"),
		PartyLedgerID: b.chitra,
		Postings: []ledger.PostingInput{
			{LedgerID: b.expense, DrCr: ledger.Debit, Amount: dec("50000")},
			{LedgerID: b.chitra, DrCr: ledger.Credit, Amount: dec("49000")},
			{LedgerID: b.tdsPayable, DrCr: ledger.Credit, Amount: dec("1000")}},
		TDS: &ledger.TDSParams{Section: "194C", DeducteeLedgerID: b.chitra, DeducteeType: "OTHER",
			PANAvailable: true, TaxableAmount: dec("50000"), TDSLedgerID: b.tdsPayable}})
}
