package printingdb_test

// document_test.go — A STORED VOUCHER BECOMES A PRINTABLE DOCUMENT, against the
// live database.
//
// WHAT THESE TESTS ARE PROTECTING, IN ORDER OF WHAT IT COSTS WHEN IT BREAKS
// ------------------------------------------------------------------------
//  1. THE PRINTED FIGURE IS THE POSTED FIGURE. Every amount asserted below is
//     read back out of `posting`, `voucher_gst_line` and `stock_movement` with
//     an INDEPENDENT query and compared with decimal .Equal — never with ==,
//     never against a literal the assembly could also have produced from the
//     wrong column. An invoice that disagrees with the receivable by one paisa
//     is a dispute the business loses time on and cannot explain.
//  2. NOBODY PRINTS ON ANOTHER TENANT'S BOOKS. The cross-tenant tests are named
//     for the BREACH, not for the assertion, because what a reviewer sees when
//     the boundary breaks is a bare `--- FAIL:` line in somebody else's run and
//     that line has to state what leaked.
//  3. A MANDATORY PARTICULAR IS REFUSED, NOT BLANKED. A tax invoice missing the
//     recipient's GSTIN is not a cosmetic defect: the recipient cannot claim
//     credit against it, and the defect is invisible on the page.
//
// These tests share the package-level helpers in template_test.go (appDSN) and
// deliberately do NOT re-declare them.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/compliance"
	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/internal/printing"
	"github.com/khata/cored/internal/printing/printingdb"
	"github.com/khata/cored/migrations"
)

// The two GST state codes every fixture here uses. The company is registered in
// 27 (Maharashtra), so a supply to 27 is INTRA-state (CGST + SGST) and a supply
// to 29 (Karnataka) is INTER-state (IGST).
//
// The NAMES matter as much as the codes now. internal/gststate turns a code into
// the name of a State, which is what Rule 46(n) requires on an inter-State
// invoice and Rule 46(e) on a high-value supply to an unregistered person — so
// every fixture below can be checked against a name a reader would recognise, and
// a code the table will not name is refused rather than printed.
const (
	docStateHome     = "27"
	docStateHomeName = "Maharashtra"
	docStateAway     = "29"
	docStateAwayName = "Karnataka"
	// docStateRetired is code 25: the Union territory of Daman and Diu, which
	// ceased to exist when it merged into Dadra and Nagar Haveli and Daman and Diu
	// (code 26) on 2020-01-26. It is a code that still LOOKS like data on a master
	// and denotes no State that can be supplied to, which is exactly the case a
	// code-to-name map has to refuse rather than guess at.
	docStateRetired = "25"
)

// EVERY PARTY FIXTURE CARRIES AN ADDRESS, and that is not decoration. Rule 46(d)
// makes the recipient's address mandatory whether or not the recipient is
// registered, so a party master with an empty address is an invoice that cannot
// be issued at all — LoadInvoice refuses it and names the master to fix. The
// addresses are INVENTED but plausible and multi-line, because the column is a
// newline-separated block and a single-line fixture would not exercise the split.
const (
	docAddrCustomer = "Plot 14, Chakan Industrial Area\nPhase II, Nighoje"
	docAddrAway     = "22/3, Peenya 2nd Stage\nJalahalli West"
	docAddrWalkIn   = "Counter sale, Andheri showroom"
)

// GSTINs are FIXTURES. Nothing in the print path validates a GSTIN's structure
// or its check digit, and nothing in this environment can reach the GSTN portal
// to verify one, so these are plausible-shaped strings and are not claimed to be
// real registrations.
const (
	docCompanyGSTIN  = "27AABCT1234H1ZS"
	docCustomerGSTIN = "27AACCA1234B1ZQ"
	docAwayGSTIN     = "29AACCB5678C1Z5"
)

// Ledger and item names, spelled once so an assertion and a fixture cannot
// drift apart.
const (
	docNameCustomer = "Nandi Steels"
	docNameAway     = "Coorg Hardware"
	docNameWalkIn   = "Cash Sales (walk-in)"
	docNameSales    = "Domestic Sales"
	docNameOutCGST  = "Output CGST"
	docNameOutSGST  = "Output SGST"
	docNameOutIGST  = "Output IGST"
	docNameSalaries = "Staff Costs"
	docNameBolt     = "Hex Bolt M12"
	docNameGrease   = "Lithium Grease"
)

// docEnv is one isolated company with everything a printable document needs: a
// registered customer in the home state, a registered customer in another state,
// an UNREGISTERED walk-in customer, the revenue and tax ledgers, and two stock
// items at DIFFERENT GST rates — the mixed-rate case is the one a single
// aggregate tax figure cannot state, and Rule 46 requires the rate per head.
type docEnv struct {
	pool    *db.Pool
	store   *printingdb.Store
	ctx     context.Context // company-scoped, for the ledger service
	company uuid.UUID
	led     *ledger.Service

	customer uuid.UUID
	away     uuid.UUID
	walkIn   uuid.UUID
	sales    uuid.UUID
	ocgst    uuid.UUID
	osgst    uuid.UUID
	oigst    uuid.UUID
	cash     uuid.UUID
	salaries uuid.UUID
	godown   uuid.UUID
	bolt     uuid.UUID
	grease   uuid.UUID
}

// newDocEnv migrates the live database (idempotent), opens an APP-ROLE pool and
// builds the fixture.
//
// The pool is tally_app (NOBYPASSRLS) and never the superuser: under an admin
// pool every tenancy assertion in this file would pass for the wrong reason.
func newDocEnv(t *testing.T) *docEnv {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	ctx := context.Background()

	admin, err := pgx.Connect(ctx, testdb.AdminDSNValue())
	if err != nil {
		t.Skipf("admin postgres unavailable, skipping integration test: %v", err)
	}
	if err := migrations.Apply(ctx, admin); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("apply migrations: %v", err)
	}
	_ = admin.Close(ctx)

	pool, err := db.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}
	t.Cleanup(pool.Close)

	e := &docEnv{pool: pool, store: printingdb.NewStore(pool.Raw()), led: ledger.NewService(pool)}
	e.company = newPrintableCompany(t, pool, docCompanyGSTIN)
	e.ctx = db.WithCompany(ctx, e.company)

	ms := masters.NewService(pool)
	mk := func(l masters.Ledger) uuid.UUID {
		id, err := ms.CreateLedger(e.ctx, l, "fixture")
		if err != nil {
			t.Fatalf("create ledger %s: %v", l.Name, err)
		}
		return id
	}
	e.customer = mk(masters.Ledger{
		Name: docNameCustomer, GroupName: "Sundry Debtors",
		GSTIN: docCustomerGSTIN, StateCode: docStateHome,
		RegistrationType: masters.GSTRegular, MaintainBillByBill: true,
		Address: docAddrCustomer, City: "Pune", PIN: "410501",
	})
	e.away = mk(masters.Ledger{
		Name: docNameAway, GroupName: "Sundry Debtors",
		GSTIN: docAwayGSTIN, StateCode: docStateAway,
		RegistrationType: masters.GSTRegular,
		Address:          docAddrAway, City: "Bengaluru", PIN: "560058",
	})
	// An unregistered walk-in customer: legal, and the case where a blank GSTIN
	// is CORRECT rather than a defect (Rule 46(d) asks for the GSTIN "if
	// registered"). The ADDRESS is not conditional in the same way, so this
	// fixture carries one.
	e.walkIn = mk(masters.Ledger{
		Name: docNameWalkIn, GroupName: "Sundry Debtors",
		StateCode: docStateHome, RegistrationType: masters.GSTUnregistered,
		Address: docAddrWalkIn, City: "Mumbai", PIN: "400053",
	})
	e.sales = mk(masters.Ledger{Name: docNameSales, GroupName: "Sales Accounts"})
	e.ocgst = mk(masters.Ledger{Name: docNameOutCGST, GroupName: "Duties & Taxes"})
	e.osgst = mk(masters.Ledger{Name: docNameOutSGST, GroupName: "Duties & Taxes"})
	e.oigst = mk(masters.Ledger{Name: docNameOutIGST, GroupName: "Duties & Taxes"})
	e.salaries = mk(masters.Ledger{Name: docNameSalaries, GroupName: "Indirect Expenses"})

	if err := pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`SELECT id FROM ledger WHERE company_id = $1 AND name = 'Cash'`,
			e.company).Scan(&e.cash); err != nil {
			return err
		}
		return tx.QueryRow(ctx,
			`SELECT id FROM godown WHERE company_id = $1 AND name = 'Main Location'`,
			e.company).Scan(&e.godown)
	}); err != nil {
		t.Fatalf("the company seed must provide a Cash ledger and a Main Location: %v", err)
	}

	// HSN and rate live on the ITEM master, and the unit is joined through it —
	// so an invoice line's HSN and unit are only right if the assembly reads
	// both. 7318 @ 18% and 3403 @ 12%: two rates on one invoice.
	e.bolt = e.newStockItem(t, docNameBolt, "7318", "18")
	e.grease = e.newStockItem(t, docNameGrease, "3403", "12")
	e.openStock(t, e.bolt, "1000", "80")
	e.openStock(t, e.grease, "1000", "40")
	return e
}

// newPrintableCompany creates a throwaway company whose OWN particulars are
// complete — name, address, city, PIN, state code and GSTIN — because Rule 46(a)
// makes the supplier block mandatory and a fixture without it would prove only
// that the refusal fires.
//
// A THROWAWAY COMPANY EVERY TIME, never Demo Traders or the tenancy fixture:
// those are live seeds the browser gates depend on. testdb.Cleanup is mandatory
// (internal/testhygiene fails the build without it) and is registered the moment
// the company exists, so a t.Fatal later still tears it down.
func newPrintableCompany(t *testing.T, pool *db.Pool, gstin string) uuid.UUID {
	t.Helper()
	companyID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new company id: %v", err)
	}
	cctx := db.WithCompany(context.Background(), companyID)
	if _, err := identity.NewService(pool).CreateCompany(cctx, identity.Company{
		ID:          companyID,
		Name:        "Nandi Fasteners " + companyID.String()[:8],
		FYStartDate: "2026-04-01",
		StateCode:   docStateHome,
		GSTIN:       gstin,
		Address:     "Unit 7, Andheri Industrial Estate\nOff Veera Desai Road",
		City:        "Mumbai",
		Pincode:     "400053",
	}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	testdb.Cleanup(t, companyID)
	return companyID
}

// newStockItem plants an item master with the HSN, GST rate and unit the invoice
// must read from HERE and never from the request.
func (e *docEnv) newStockItem(t *testing.T, name, hsn, ratePct string) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new stock item id: %v", err)
	}
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO stock_item (id, company_id, name, unit_id, hsn, gst_rate_pct)
			 VALUES ($1, $2, $3,
			         (SELECT id FROM unit WHERE company_id = $2 AND symbol = 'Nos'),
			         $4, $5)`,
			id, e.company, name, hsn, decimal.RequireFromString(ratePct))
		return err
	}); err != nil {
		t.Fatalf("create stock item %s: %v", name, err)
	}
	return id
}

// openStock puts goods on the shelf so an outward sale is not refused by the
// negative-stock trigger. An OPENING and not a purchase, deliberately: it must
// not put movements or GST lines into the versions these tests assert over.
func (e *docEnv) openStock(t *testing.T, item uuid.UUID, qty, rate string) {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new opening id: %v", err)
	}
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO stock_opening (id, company_id, stock_item_id, godown_id, qty, rate)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			id, e.company, item, e.godown,
			decimal.RequireFromString(qty), decimal.RequireFromString(rate))
		return err
	}); err != nil {
		t.Fatalf("seed opening stock: %v", err)
	}
}

// itemLine builds one inventory line with NO amount: an item invoice derives the
// line total from qty/rate/discount inside the post, which is what a grid
// capturing only those three columns sends.
func (e *docEnv) itemLine(item uuid.UUID, qty, rate, discPct string) ledger.InventoryLine {
	return ledger.InventoryLine{
		StockItemID: item,
		GodownID:    e.godown,
		Direction:   ledger.Outward,
		Qty:         decimal.RequireFromString(qty),
		Rate:        decimal.RequireFromString(rate),
		DiscountPct: decimal.RequireFromString(discPct),
	}
}

// postMixedRateItemInvoice posts the canonical fixture invoice: an INTRA-state
// item invoice to the registered home-state customer, carrying two items at two
// different GST rates.
//
//	10 bolts   @ 100.00 = 1,000.00 @ 18% -> CGST 90.00 + SGST 90.00
//	 5 greases @ 100.00 =   500.00 @ 12% -> CGST 30.00 + SGST 30.00
//	                       --------                --------------
//	          taxable    1,500.00        tax  240.00   total 1,740.00
func (e *docEnv) postMixedRateItemInvoice(t *testing.T) ledger.PostResult {
	t.Helper()
	res, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class:         ledger.Sales,
		PartyLedgerID: e.customer,
		ItemLedgerID:  e.sales,
		VoucherDate:   "2026-05-11",
		Party:         docNameCustomer,
		CreatedBy:     "fixture",
		Inventory: []ledger.InventoryLine{
			e.itemLine(e.bolt, "10", "100", "0"),
			e.itemLine(e.grease, "5", "100", "0"),
		},
		GST: &ledger.GSTParams{
			SupplierStateCode:      docStateHome,
			PlaceOfSupplyStateCode: docStateHome,
			CGSTLedgerID:           e.ocgst,
			SGSTLedgerID:           e.osgst,
			IGSTLedgerID:           e.oigst,
		},
	})
	if err != nil {
		t.Fatalf("post the fixture item invoice: %v", err)
	}
	return res
}

// --- independent reads of what the books actually hold ----------------------

// postedLeg is one posting resolved back to the ledger NAME. The name is
// load-bearing: comparing a printed total against "some posting of 1740" cannot
// see an invoice whose total was taken from the wrong leg.
type postedLeg struct {
	debit  bool
	amount decimal.Decimal
}

// legsOf reads the postings of a version straight from `posting`, joined to
// `ledger` for the name. This is the INDEPENDENT source every amount assertion
// compares against — not a second call into the store.
func (e *docEnv) legsOf(t *testing.T, versionID uuid.UUID) map[string]postedLeg {
	t.Helper()
	out := map[string]postedLeg{}
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT l.name, p.dr_cr::text, p.amount
			   FROM posting p JOIN ledger l ON l.id = p.ledger_id
			  WHERE p.company_id = $1 AND p.voucher_version_id = $2`,
			e.company, versionID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				name string
				side string
				amt  decimal.Decimal
			)
			if err := rows.Scan(&name, &side, &amt); err != nil {
				return err
			}
			out[name] = postedLeg{debit: side == "D", amount: amt}
		}
		return rows.Err()
	}); err != nil {
		t.Fatalf("read posted legs: %v", err)
	}
	return out
}

// currentVersion resolves the head version of a logical voucher.
func (e *docEnv) currentVersion(t *testing.T, voucherID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT current_version_id FROM voucher_head
			  WHERE company_id = $1 AND voucher_id = $2`,
			e.company, voucherID).Scan(&id)
	}); err != nil {
		t.Fatalf("read current version: %v", err)
	}
	return id
}

// storedTaxableValue reads voucher_version.taxable_value: the taxable base the
// books recorded, which is what the invoice's Subtotal must state.
func (e *docEnv) storedTaxableValue(t *testing.T, versionID uuid.UUID) decimal.Decimal {
	t.Helper()
	var v decimal.Decimal
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT COALESCE(taxable_value, 0) FROM voucher_version
			  WHERE company_id = $1 AND id = $2`,
			e.company, versionID).Scan(&v)
	}); err != nil {
		t.Fatalf("read stored taxable value: %v", err)
	}
	return v
}

// storedMovementAmount reads one stock line's amount, so the printed line total
// can be compared with the movement that took the goods out of the warehouse.
func (e *docEnv) storedMovementAmount(t *testing.T, versionID uuid.UUID, lineNo int) decimal.Decimal {
	t.Helper()
	var v decimal.Decimal
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT amount FROM stock_movement
			  WHERE company_id = $1 AND voucher_version_id = $2 AND line_no = $3`,
			e.company, versionID, lineNo).Scan(&v)
	}); err != nil {
		t.Fatalf("read stock movement %d: %v", lineNo, err)
	}
	return v
}

// storedDiscountPct reads one stock line's discount_pct: the PERCENTAGE the
// column holds (it CHECKs 0..100), which is the figure the printed line must
// carry. Read independently so the unit assertion cannot be satisfied by the same
// wrong column the assembly might have used.
func (e *docEnv) storedDiscountPct(t *testing.T, versionID uuid.UUID, lineNo int) decimal.Decimal {
	t.Helper()
	var v decimal.Decimal
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT discount_pct FROM stock_movement
			  WHERE company_id = $1 AND voucher_version_id = $2 AND line_no = $3`,
			e.company, versionID, lineNo).Scan(&v)
	}); err != nil {
		t.Fatalf("read the discount percentage of stock movement %d: %v", lineNo, err)
	}
	return v
}

// headTotal sums the tax heads of one name ("CGST"), which is the figure that
// must equal the amount posted to that tax ledger.
func headTotal(heads []printing.TaxHead, name string) decimal.Decimal {
	sum := decimal.Zero
	for _, h := range heads {
		if h.Name == name {
			sum = sum.Add(h.Amount)
		}
	}
	return sum
}

// ---------------------------------------------------------------------------
// 1. The printed figure IS the posted figure
// ---------------------------------------------------------------------------

// TestThePrintedInvoiceDisagreesWithTheLedgerItWasPostedFrom is named for the
// failure, because that is the sentence a reviewer needs to see: if this test
// fails, an invoice states an amount the books do not hold, and the customer is
// the one who finds out.
//
// Every comparison is against a value read back out of `posting`,
// `voucher_version` and `stock_movement` with an independent query, and every
// comparison uses decimal .Equal — not ==, which compares shopspring's internal
// representation and would call 1740 and 1740.00 different.
func TestThePrintedInvoiceDisagreesWithTheLedgerItWasPostedFrom(t *testing.T) {
	e := newDocEnv(t)
	res := e.postMixedRateItemInvoice(t)
	versionID := e.currentVersion(t, res.VoucherID)
	legs := e.legsOf(t, versionID)

	inv, err := e.store.LoadInvoice(context.Background(), e.company, res.VoucherID)
	if err != nil {
		t.Fatalf("a complete sales voucher could not be assembled into an invoice: %v", err)
	}

	// The document it is: tax was charged, so Rule 46 governs and this is a tax
	// invoice, not a bill of supply.
	if inv.Kind != printing.KindTaxInvoice {
		t.Errorf("a sale that charged CGST and SGST was assembled as %q, not a tax invoice: "+
			"a bill of supply may not state a rate or an amount of tax at all (Rule 49)", inv.Kind)
	}

	// SUBTOTAL: the stored taxable base.
	wantTaxable := e.storedTaxableValue(t, versionID)
	if !inv.Subtotal.Equal(wantTaxable) {
		t.Errorf("the printed taxable value is %s but the voucher records %s: "+
			"the invoice and the GST return would state different bases for the same supply",
			inv.Subtotal.StringFixed(2), wantTaxable.StringFixed(2))
	}

	// TAX, PER HEAD: the sum of the printed CGST heads must equal what was posted
	// to the Output CGST ledger, and likewise SGST. This is the assertion a
	// mixed-rate invoice needs — the two heads are split across two rate slabs on
	// the page and land as one figure in the ledger.
	for _, tc := range []struct {
		head, ledgerName string
	}{
		{"CGST", docNameOutCGST},
		{"SGST", docNameOutSGST},
	} {
		posted, ok := legs[tc.ledgerName]
		if !ok {
			t.Fatalf("the fixture invoice did not post to %s at all", tc.ledgerName)
		}
		if got := headTotal(inv.Taxes, tc.head); !got.Equal(posted.amount) {
			t.Errorf("the invoice states %s of %s but %s of %s was posted to %q: "+
				"the tax on the customer's copy is not the tax in the books",
				tc.head, got.StringFixed(2), tc.head, posted.amount.StringFixed(2), tc.ledgerName)
		}
	}
	if got := headTotal(inv.Taxes, "IGST"); !got.IsZero() {
		t.Errorf("an INTRA-state supply printed IGST of %s: the recipient would claim "+
			"integrated credit on a supply that attracted central and state tax",
			got.StringFixed(2))
	}

	// TOTAL: what the customer owes, i.e. the party leg of the postings.
	party, ok := legs[docNameCustomer]
	if !ok {
		t.Fatalf("the fixture invoice did not post to the customer %q at all", docNameCustomer)
	}
	if !party.debit {
		t.Fatalf("the fixture invoice credited the customer: the fixture itself is wrong")
	}
	if !inv.Total.Equal(party.amount) {
		t.Errorf("the invoice total is %s but %s was debited to the customer's account: "+
			"the amount demanded on paper is not the amount receivable",
			inv.Total.StringFixed(2), party.amount.StringFixed(2))
	}

	// AND THE DOCUMENT ADDS UP. Subtotal + every tax head + round-off must equal
	// the total, exactly — a total that is right while the lines above it do not
	// sum to it is the invoice a customer refuses to pay.
	sum := inv.Subtotal.Add(inv.RoundOff)
	for _, h := range inv.Taxes {
		sum = sum.Add(h.Amount)
	}
	if !sum.Equal(inv.Total) {
		t.Errorf("the invoice does not add up: taxable %s + tax + round-off %s = %s, "+
			"but the stated total is %s",
			inv.Subtotal.StringFixed(2), inv.RoundOff.StringFixed(2),
			sum.StringFixed(2), inv.Total.StringFixed(2))
	}
	if !inv.RoundOff.IsZero() {
		t.Errorf("a voucher with no round-off leg printed a round-off of %s: "+
			"the residual is absorbing a difference between the lines and the total",
			inv.RoundOff.StringFixed(2))
	}

	// EVERY LINE: the amount printed against the goods is the amount the movement
	// recorded, and the HSN and unit come from the masters.
	if len(inv.Items) != 2 {
		t.Fatalf("a two-item invoice printed %d lines", len(inv.Items))
	}
	for _, it := range inv.Items {
		want := e.storedMovementAmount(t, versionID, it.SerialNo)
		if !it.Amount.Equal(want) {
			t.Errorf("line %d (%s) is printed at %s but the stock movement records %s: "+
				"the goods that left the warehouse are billed at a different value",
				it.SerialNo, it.Description, it.Amount.StringFixed(2), want.StringFixed(2))
		}
		if it.Unit != "Nos" {
			t.Errorf("line %d (%s) printed unit %q: Rule 46 requires the quantity AND its "+
				"unit, and the item master says Nos", it.SerialNo, it.Description, it.Unit)
		}
	}
	byName := map[string]printing.LineItem{}
	for _, it := range inv.Items {
		byName[it.Description] = it
	}
	for name, wantHSN := range map[string]string{docNameBolt: "7318", docNameGrease: "3403"} {
		it, ok := byName[name]
		if !ok {
			t.Fatalf("the invoice has no line for %q", name)
		}
		if it.HSN != wantHSN {
			t.Errorf("%q is printed under HSN %q but the books recorded %q: the invoice and "+
				"the GSTR-1 table 12 summary would classify the same goods differently",
				name, it.HSN, wantHSN)
		}
	}

	// The particulars that make it a Rule 46 document at all.
	if inv.Number == "" || inv.Number != res.VoucherNo {
		t.Errorf("the invoice serial number is %q but the voucher was numbered %q",
			inv.Number, res.VoucherNo)
	}
	if inv.Date.Format("2006-01-02") != "2026-05-11" {
		t.Errorf("the invoice is dated %s but the voucher is dated 2026-05-11",
			inv.Date.Format("2006-01-02"))
	}
	if inv.Supplier.GSTIN != docCompanyGSTIN {
		t.Errorf("the invoice states the supplier's GSTIN as %q, not the company's %q",
			inv.Supplier.GSTIN, docCompanyGSTIN)
	}
	if inv.Recipient.GSTIN != docCustomerGSTIN {
		t.Errorf("the invoice states the recipient's GSTIN as %q, not the customer's %q: "+
			"the recipient cannot claim credit against a wrong GSTIN",
			inv.Recipient.GSTIN, docCustomerGSTIN)
	}
	if inv.PlaceOfSupplyCode != docStateHome {
		t.Errorf("the invoice states the place of supply as %q, not %q",
			inv.PlaceOfSupplyCode, docStateHome)
	}
	if inv.Supplier.Address1 == "" {
		t.Error("the invoice carries no supplier address, which Rule 46(a) makes mandatory")
	}
}

// TestTheTaxBlockStatesOneRateForBothHalvesOfAMixedRateInvoice is named for the
// failure it guards: collapsing a mixed-rate invoice into one CGST figure states
// a rate that is true of neither line.
//
// Rule 46 requires the RATE and the AMOUNT of tax per head. On an 18% intra-state
// supply the heads are CGST 9% and SGST 9%; on a 12% supply they are 6% and 6%.
// One invoice carrying both must therefore print four heads, and the rates must
// be the halves.
func TestTheTaxBlockStatesOneRateForBothHalvesOfAMixedRateInvoice(t *testing.T) {
	e := newDocEnv(t)
	res := e.postMixedRateItemInvoice(t)

	inv, err := e.store.LoadInvoice(context.Background(), e.company, res.VoucherID)
	if err != nil {
		t.Fatalf("load invoice: %v", err)
	}
	type head struct {
		name string
		rate string
	}
	var got []head
	for _, h := range inv.Taxes {
		got = append(got, head{h.Name, h.Rate.String()})
	}
	want := []head{
		// Rates ascending, so the same invoice prints byte-identically twice.
		{"CGST", "6"}, {"SGST", "6"},
		{"CGST", "9"}, {"SGST", "9"},
	}
	if len(got) != len(want) {
		t.Fatalf("a mixed-rate invoice printed %d tax heads (%v); an 18%% and a 12%% line "+
			"need four: CGST and SGST at each rate", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tax head %d is %s @ %s%%, want %s @ %s%%: the rate stated against a "+
				"head is not the rate that was charged",
				i+1, got[i].name, got[i].rate, want[i].name, want[i].rate)
		}
	}
}

// TestAnInterStateSupplyPrintsCentralAndStateTaxInsteadOfIntegratedTax is named
// for the breach. A supply to another State attracts IGST alone; printing CGST
// and SGST on it tells the recipient to claim two credits that do not exist and
// leaves the integrated credit they are entitled to unclaimed.
func TestAnInterStateSupplyPrintsCentralAndStateTaxInsteadOfIntegratedTax(t *testing.T) {
	e := newDocEnv(t)
	res, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class:         ledger.Sales,
		PartyLedgerID: e.away,
		ItemLedgerID:  e.sales,
		VoucherDate:   "2026-05-12",
		Party:         docNameAway,
		CreatedBy:     "fixture",
		Inventory:     []ledger.InventoryLine{e.itemLine(e.bolt, "4", "250", "0")},
		GST: &ledger.GSTParams{
			SupplierStateCode:      docStateHome,
			PlaceOfSupplyStateCode: docStateAway,
			CGSTLedgerID:           e.ocgst,
			SGSTLedgerID:           e.osgst,
			IGSTLedgerID:           e.oigst,
		},
	})
	if err != nil {
		t.Fatalf("post inter-state invoice: %v", err)
	}
	versionID := e.currentVersion(t, res.VoucherID)
	legs := e.legsOf(t, versionID)

	inv, err := e.store.LoadInvoice(context.Background(), e.company, res.VoucherID)
	if err != nil {
		t.Fatalf("load invoice: %v", err)
	}
	posted := legs[docNameOutIGST]
	if got := headTotal(inv.Taxes, "IGST"); !got.Equal(posted.amount) {
		t.Errorf("the invoice states IGST of %s but %s was posted to %q",
			got.StringFixed(2), posted.amount.StringFixed(2), docNameOutIGST)
	}
	if got := headTotal(inv.Taxes, "CGST"); !got.IsZero() {
		t.Errorf("an inter-state supply printed CGST of %s", got.StringFixed(2))
	}
	if got := headTotal(inv.Taxes, "SGST"); !got.IsZero() {
		t.Errorf("an inter-state supply printed SGST of %s", got.StringFixed(2))
	}
	if len(inv.Taxes) != 1 || inv.Taxes[0].Rate.String() != "18" {
		t.Errorf("the integrated-tax head is %v: an 18%% inter-state supply carries ONE head "+
			"at the whole 18%%, not the halves", inv.Taxes)
	}
	if inv.PlaceOfSupplyCode != docStateAway {
		t.Errorf("the place of supply is printed as %q, not %q — the field that decides "+
			"which State the tax belongs to", inv.PlaceOfSupplyCode, docStateAway)
	}
}

// ---------------------------------------------------------------------------
// 2. Tenancy
// ---------------------------------------------------------------------------

// TestCompanyBCanPrintCompanyAsTaxInvoice is named for the breach: if it fails,
// one business printed another business's invoice — its customer, its amounts,
// its GSTIN — and there is nothing on the page to show whose books it came from.
func TestCompanyBCanPrintCompanyAsTaxInvoice(t *testing.T) {
	e := newDocEnv(t)
	res := e.postMixedRateItemInvoice(t)
	other := newPrintableCompany(t, e.pool, "27AAACO9999Q1ZT")

	inv, err := e.store.LoadInvoice(context.Background(), other, res.VoucherID)
	if err == nil {
		t.Fatalf("company B was handed company A's invoice: number %q, recipient %q, total %s",
			inv.Number, inv.Recipient.Name, inv.Total.StringFixed(2))
	}
	if !errors.Is(err, printingdb.ErrVoucherNotFound) {
		t.Errorf("asking for another company's voucher failed with %v; it must be a plain "+
			"not-found, so a caller cannot distinguish \"exists elsewhere\" from \"does not exist\"", err)
	}
	if inv.Number != "" || !inv.Total.IsZero() || inv.Recipient.Name != "" {
		t.Errorf("the refused call still returned data from company A: number %q, recipient %q, total %s",
			inv.Number, inv.Recipient.Name, inv.Total.StringFixed(2))
	}
}

// TestCompanyBCanReadTheClassOfCompanyAsVoucher is named for the breach too, and
// it is a smaller leak that matters more than it looks: VoucherClass is what the
// HTTP layer compares the path's ACL token against, so a class readable across
// tenants would let one company probe which of another's voucher ids exist and
// what they are.
func TestCompanyBCanReadTheClassOfCompanyAsVoucher(t *testing.T) {
	e := newDocEnv(t)
	res := e.postMixedRateItemInvoice(t)
	other := newPrintableCompany(t, e.pool, "27AAACO8888Q1ZU")

	class, err := e.store.VoucherClass(context.Background(), other, res.VoucherID)
	if err == nil {
		t.Fatalf("company B learned that company A's voucher %s is a %q", res.VoucherID, class)
	}
	if !errors.Is(err, printingdb.ErrVoucherNotFound) {
		t.Errorf("reading another company's voucher class failed with %v, want a plain not-found", err)
	}
	if class != "" {
		t.Errorf("the refused call still returned the class %q", class)
	}
}

// TestATransactionWithNoTenantGUCReadsTheWholeFleetsVouchers is named for the
// breach, and it exists because the two tests above CANNOT tell you WHY they
// pass.
//
// Neutralising the `company_id = $1` predicate in this slice's own queries does
// NOT make those tests fail — the row-level security policy is what makes
// another tenant's voucher invisible, since inTenantTx binds
// app.current_company_id and every one of these tables is ENABLE + FORCE RLS
// with USING pinned to app_current_company_id(). So the predicate is belt and
// RLS is braces, and this test is the one that proves the braces exist: with NO
// GUC set at all, the six tables the print path reads must return NOTHING.
//
// If this ever fails, the print path's tenancy rests entirely on a WHERE clause
// somebody could delete in a refactor without a single test going red.
func TestATransactionWithNoTenantGUCReadsTheWholeFleetsVouchers(t *testing.T) {
	e := newDocEnv(t)
	// A real invoice exists in SOME company, so a zero count cannot pass for the
	// trivial reason that nothing was posted.
	e.postMixedRateItemInvoice(t)

	// The APP role (tally_app, NOBYPASSRLS), and deliberately NO set_config call.
	conn, err := pgx.Connect(context.Background(), appDSN)
	if err != nil {
		t.Skipf("app postgres unavailable: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()

	for _, table := range []string{
		"voucher_head", "voucher_version", "posting",
		"voucher_gst_line", "stock_movement", "bill_allocation",
	} {
		var n int
		if err := conn.QueryRow(context.Background(),
			`SELECT count(*) FROM `+table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Errorf("a transaction with no tenant scope can see %d rows of %s: every company's "+
				"invoices are readable by any session that forgets to scope itself", n, table)
		}
	}
}

// TestPrintingWithNoTenantScopeAtAllReadsSomebodyAsVoucher pins the other half
// of the tenancy story: the company argument is not decoration, and an empty one
// is refused BEFORE any query runs (db.ErrNoCompany's argument — "denied by RLS"
// and "genuinely absent" are indistinguishable in a result set, and only one of
// them is a bug).
func TestPrintingWithNoTenantScopeAtAllReadsSomebodyAsVoucher(t *testing.T) {
	e := newDocEnv(t)
	res := e.postMixedRateItemInvoice(t)

	if _, err := e.store.LoadInvoice(context.Background(), uuid.Nil, res.VoucherID); !errors.Is(err, printingdb.ErrNoCompany) {
		t.Errorf("an unscoped print returned %v; it must refuse with ErrNoCompany before it queries anything", err)
	}
	if _, err := e.store.VoucherClass(context.Background(), uuid.Nil, res.VoucherID); !errors.Is(err, printingdb.ErrNoCompany) {
		t.Errorf("an unscoped class read returned %v; want ErrNoCompany", err)
	}
	if _, err := e.store.LoadVoucherDoc(context.Background(), uuid.Nil, res.VoucherID); !errors.Is(err, printingdb.ErrNoCompany) {
		t.Errorf("an unscoped voucher-document print returned %v; want ErrNoCompany", err)
	}
}

// ---------------------------------------------------------------------------
// 3. The stored class decides the document
// ---------------------------------------------------------------------------

// TestVoucherClassReportsAClassTheVoucherIsNot guards the route's ACL check. The
// print path is /print/vouchers/{class}/{voucherID} and {class} is the token
// authz resolves, so a caller allowed to print `receipt` and DISALLOWED `sales`
// could ask for .../receipt/<a sales voucher id>. Comparing the path class with
// the STORED class is what refuses that, and this is the stored half.
func TestVoucherClassReportsAClassTheVoucherIsNot(t *testing.T) {
	e := newDocEnv(t)
	sale := e.postMixedRateItemInvoice(t)
	receipt := e.postReceipt(t)

	for _, tc := range []struct {
		voucher uuid.UUID
		want    string
	}{
		{sale.VoucherID, "Sales"},
		{receipt.VoucherID, "Receipt"},
	} {
		got, err := e.store.VoucherClass(context.Background(), e.company, tc.voucher)
		if err != nil {
			t.Fatalf("read stored class: %v", err)
		}
		if got != tc.want {
			t.Errorf("voucher %s is stored as a %q but reports as %q: the print route would "+
				"authorise the request against the wrong voucher class", tc.voucher, tc.want, got)
		}
	}
}

// TestAPayrollVoucherPrintsAsATaxInvoice is named for the breach, and the breach
// is not hypothetical: a pay run is an ordinary balanced voucher, so an assembly
// that switched on "has postings" rather than on the CLASS would happily print
// one — and hand somebody a tax invoice whose lines are the salary bill.
func TestAPayrollVoucherPrintsAsATaxInvoice(t *testing.T) {
	e := newDocEnv(t)
	res, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class:       ledger.Payroll,
		VoucherDate: "2026-05-31",
		CreatedBy:   "fixture",
		Narration:   "May 2026 pay run",
		Postings: []ledger.PostingInput{
			{LedgerID: e.salaries, DrCr: ledger.Debit, Amount: decimal.RequireFromString("125000")},
			{LedgerID: e.cash, DrCr: ledger.Credit, Amount: decimal.RequireFromString("125000")},
		},
	})
	if err != nil {
		t.Fatalf("post payroll voucher: %v", err)
	}

	if class, err := e.store.VoucherClass(context.Background(), e.company, res.VoucherID); err != nil {
		t.Fatalf("read stored class: %v", err)
	} else if class != "Payroll" {
		t.Errorf("the pay run's stored class reads as %q, not Payroll", class)
	}

	inv, err := e.store.LoadInvoice(context.Background(), e.company, res.VoucherID)
	if err == nil {
		t.Fatalf("a pay run was assembled into a %s totalling %s", inv.Kind, inv.Total.StringFixed(2))
	}
	if !errors.Is(err, printingdb.ErrNoDocumentForClass) {
		t.Errorf("printing a pay run failed with %v; it must be refused as a class with no "+
			"printable document, so the message names the class instead of looking like a bug", err)
	}
	if _, err := e.store.LoadVoucherDoc(context.Background(), e.company, res.VoucherID); !errors.Is(err, printingdb.ErrNoDocumentForClass) {
		t.Errorf("a pay run was accepted by the voucher-document loader (%v): a payslip is a "+
			"per-employee document, not this voucher's", err)
	}
}

// TestAPurchaseVoucherPrintsAsOurOwnTaxInvoice is named for the breach because
// that is exactly what the breach would be: the SUPPLIER issues a purchase
// invoice. This company printing one under its own letterhead and GSTIN is
// issuing a document it has no authority to issue.
func TestAPurchaseVoucherPrintsAsOurOwnTaxInvoice(t *testing.T) {
	e := newDocEnv(t)
	res, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class:         ledger.Purchase,
		PartyLedgerID: e.customer,
		VoucherDate:   "2026-05-13",
		Party:         docNameCustomer,
		CreatedBy:     "fixture",
		Postings: []ledger.PostingInput{
			{LedgerID: e.salaries, DrCr: ledger.Debit, Amount: decimal.RequireFromString("900")},
			{LedgerID: e.customer, DrCr: ledger.Credit, Amount: decimal.RequireFromString("900")},
		},
	})
	if err != nil {
		t.Fatalf("post purchase voucher: %v", err)
	}
	inv, err := e.store.LoadInvoice(context.Background(), e.company, res.VoucherID)
	if err == nil {
		t.Fatalf("a purchase voucher was assembled into a %s under our own GSTIN %q",
			inv.Kind, inv.Supplier.GSTIN)
	}
	if !errors.Is(err, printingdb.ErrNoDocumentForClass) {
		t.Errorf("printing a purchase failed with %v, want ErrNoDocumentForClass", err)
	}
}

// TestADeletedInvoiceCanStillBePrinted is named for the breach. voucher_head
// keeps the head row of a deleted voucher — that is what makes the edit log
// readable — so an assembly that read the current version without the is_deleted
// filter would hand out a fresh, indistinguishable copy of a CANCELLED invoice.
func TestADeletedInvoiceCanStillBePrinted(t *testing.T) {
	e := newDocEnv(t)
	res := e.postMixedRateItemInvoice(t)
	if _, err := e.led.DeleteVoucher(e.ctx, res.VoucherID, "fixture"); err != nil {
		t.Fatalf("delete the voucher: %v", err)
	}

	inv, err := e.store.LoadInvoice(context.Background(), e.company, res.VoucherID)
	if err == nil {
		t.Fatalf("a DELETED invoice printed anyway: number %q, total %s",
			inv.Number, inv.Total.StringFixed(2))
	}
	if !errors.Is(err, printingdb.ErrVoucherNotFound) {
		t.Errorf("printing a deleted voucher failed with %v, want ErrVoucherNotFound", err)
	}
	if _, err := e.store.VoucherClass(context.Background(), e.company, res.VoucherID); !errors.Is(err, printingdb.ErrVoucherNotFound) {
		t.Errorf("a deleted voucher still reports a class (%v): the route would authorise a "+
			"print of a cancelled document", err)
	}
}

// ---------------------------------------------------------------------------
// 4. A mandatory particular is refused, not blanked
// ---------------------------------------------------------------------------

// TestATaxInvoiceToARegisteredCustomerPrintsWithABlankGSTIN is named for the
// breach. Rule 46(d) requires the recipient's GSTIN when they are registered,
// and a blank one is invisible on the page: the recipient discovers it when
// their credit is denied, months later, and the supplier has to reissue.
func TestATaxInvoiceToARegisteredCustomerPrintsWithABlankGSTIN(t *testing.T) {
	e := newDocEnv(t)
	// A customer whose master SAYS it is registered and carries no number. That
	// contradiction is the case worth refusing: an UNREGISTERED party with no
	// GSTIN is perfectly legal and prints (see the walk-in test below).
	id, err := masters.NewService(e.pool).CreateLedger(e.ctx, masters.Ledger{
		Name: "Registered But Unnumbered Traders", GroupName: "Sundry Debtors",
		StateCode: docStateHome, RegistrationType: masters.GSTRegular,
		// An address, so the refusal this test asserts is about the GSTIN and not
		// about the address check that precedes it.
		Address: "5, Kalbadevi Road", City: "Mumbai", PIN: "400002",
	}, "fixture")
	if err != nil {
		t.Fatalf("create ledger: %v", err)
	}
	res, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class:         ledger.Sales,
		PartyLedgerID: id,
		ItemLedgerID:  e.sales,
		VoucherDate:   "2026-05-14",
		Party:         "Registered But Unnumbered Traders",
		CreatedBy:     "fixture",
		Inventory:     []ledger.InventoryLine{e.itemLine(e.bolt, "2", "100", "0")},
		GST: &ledger.GSTParams{
			SupplierStateCode:      docStateHome,
			PlaceOfSupplyStateCode: docStateHome,
			CGSTLedgerID:           e.ocgst,
			SGSTLedgerID:           e.osgst,
			IGSTLedgerID:           e.oigst,
		},
	})
	if err != nil {
		t.Fatalf("post invoice: %v", err)
	}

	inv, err := e.store.LoadInvoice(context.Background(), e.company, res.VoucherID)
	if err == nil {
		t.Fatalf("a B2B tax invoice was assembled with the recipient's GSTIN blank: "+
			"recipient %q, total %s", inv.Recipient.Name, inv.Total.StringFixed(2))
	}
	var missing *printingdb.MissingParticularError
	if !errors.As(err, &missing) {
		t.Fatalf("the missing GSTIN surfaced as %v, not as a typed MissingParticularError: "+
			"a caller cannot tell an incomplete voucher from a database fault", err)
	}
	if !errors.Is(err, printingdb.ErrMissingParticular) {
		t.Error("the typed error does not unwrap to ErrMissingParticular, so errors.Is cannot classify it")
	}
	if !strings.Contains(missing.Field, "GSTIN") {
		t.Errorf("the refusal names the particular %q; it must name the recipient's GSTIN so "+
			"somebody knows which field to fill in", missing.Field)
	}
	if !strings.Contains(missing.Source, "Registered But Unnumbered Traders") {
		t.Errorf("the refusal's source is %q; it must name the MASTER RECORD to fix, not just "+
			"a struct field", missing.Source)
	}
	if missing.NotModelled {
		t.Error("the missing GSTIN is reported as a schema gap; ledger.gstin exists and is " +
			"empty, so the fix is data entry and the error must say so")
	}
}

// TestAWalkInSaleToAnUnregisteredCustomerIsRefusedForHavingNoGSTIN is the
// COMPLEMENT, and it is the test that stops the refusal above from being
// implemented as "always require a GSTIN". An unregistered customer has no GSTIN
// to state — Rule 46(d) asks for it "if registered" — and a shop that could not
// print a counter sale would be unusable.
func TestAWalkInSaleToAnUnregisteredCustomerIsRefusedForHavingNoGSTIN(t *testing.T) {
	e := newDocEnv(t)
	res, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class:         ledger.Sales,
		PartyLedgerID: e.walkIn,
		ItemLedgerID:  e.sales,
		VoucherDate:   "2026-05-15",
		Party:         docNameWalkIn,
		CreatedBy:     "fixture",
		Inventory:     []ledger.InventoryLine{e.itemLine(e.bolt, "1", "150", "0")},
		GST: &ledger.GSTParams{
			SupplierStateCode:      docStateHome,
			PlaceOfSupplyStateCode: docStateHome,
			CGSTLedgerID:           e.ocgst,
			SGSTLedgerID:           e.osgst,
			IGSTLedgerID:           e.oigst,
		},
	})
	if err != nil {
		t.Fatalf("post walk-in invoice: %v", err)
	}
	inv, err := e.store.LoadInvoice(context.Background(), e.company, res.VoucherID)
	if err != nil {
		t.Fatalf("a counter sale to an unregistered customer could not be printed: %v", err)
	}
	if inv.Recipient.GSTIN != "" {
		t.Errorf("an unregistered customer was given the GSTIN %q", inv.Recipient.GSTIN)
	}
	if inv.Kind != printing.KindTaxInvoice {
		t.Errorf("a taxed counter sale printed as %q rather than a tax invoice", inv.Kind)
	}
}

// TestACompanyWithNoGSTINOfItsOwnPrintsTaxInvoices is named for the breach: a
// document that charges CGST and SGST without stating the supplier's own GSTIN
// is not a tax invoice at all, and the recipient can claim nothing against it.
func TestACompanyWithNoGSTINOfItsOwnPrintsTaxInvoices(t *testing.T) {
	e := newDocEnv(t)
	res := e.postMixedRateItemInvoice(t)

	// Clear the company's GSTIN AFTER the post (the post defaults the supplier
	// state code from it), so the refusal is about the printed particular and not
	// about a voucher that could never have been posted.
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE company SET gstin = '' WHERE id = $1`, e.company)
		return err
	}); err != nil {
		t.Fatalf("clear the company GSTIN: %v", err)
	}

	_, err := e.store.LoadInvoice(context.Background(), e.company, res.VoucherID)
	var missing *printingdb.MissingParticularError
	if !errors.As(err, &missing) {
		t.Fatalf("an invoice from a company with no GSTIN was refused with %v, not a typed "+
			"MissingParticularError", err)
	}
	if !strings.Contains(strings.ToLower(missing.Field), "supplier") {
		t.Errorf("the refusal names %q; it must name the SUPPLIER's GSTIN so the fix is "+
			"unambiguous (the recipient's is a different field)", missing.Field)
	}
}

// TestTheGapsThisSchemaCannotFillAreLeftUndeclared is named for the breach it
// prevents: a particular that nothing stores, quietly printed as a blank, with
// no record anywhere that the system knows it is missing.
//
// UnmodelledInvoiceParticulars is that record, and every entry must be flagged as
// a SCHEMA gap so nobody sends an operator to look for a data-entry field that
// does not exist. It must NO LONGER name the recipient's address, which migration
// 0039 models, and it must NO LONGER name the state NAME, which internal/gststate
// now supplies from the code the books already hold — an entry that stays on this
// list after the gap closes tells an operator a migration is needed when the fix
// is a keystroke or is already done, and the particular never gets filled in.
func TestTheGapsThisSchemaCannotFillAreLeftUndeclared(t *testing.T) {
	gaps := printingdb.UnmodelledInvoiceParticulars()
	if len(gaps) == 0 {
		t.Fatal("no unmodelled Rule 46 particulars are declared at all, yet this schema still has " +
			"no per-voucher delivery address, no e-invoice acknowledgement number and no cess " +
			"column: those gaps would print as blanks with nothing recording them")
	}
	for _, g := range gaps {
		if !g.NotModelled {
			t.Errorf("%q is listed as a schema gap but not flagged NotModelled, so it reads as "+
				"something an operator could type in", g.Field)
		}
		if g.Rule == "" || g.Source == "" {
			t.Errorf("the gap %q cites no rule (%q) or names no source (%q); an undated, "+
				"uncited gap cannot be acted on", g.Field, g.Rule, g.Source)
		}
		if strings.Contains(strings.ToLower(g.Field), "name of the state") {
			t.Errorf("the NAME OF THE STATE is still declared as a SCHEMA gap (%q), but "+
				"internal/gststate maps the two-digit code the books already hold to the name: an "+
				"operator reading this is told to wait for a migration, when in fact an inter-State "+
				"invoice prints today and a stale code is refused with the code named", g.Field)
		}
		if strings.Contains(strings.ToLower(g.Field), "recipient address") {
			t.Errorf("the recipient's address is still declared as a SCHEMA gap (%q), but "+
				"migration 0039 added ledger.address/city/pincode: an operator reading this is "+
				"told to wait for a migration when the fix is to type the address in, so the "+
				"invoice stays unprintable", g.Field)
		}
		if !errors.Is(g, printingdb.ErrMissingParticular) {
			t.Errorf("the gap %q does not unwrap to ErrMissingParticular", g.Field)
		}
	}
}

// TestATaxInvoiceIsIssuedToAPartyWhoseAddressNobodyRecorded is named for the
// breach. Rule 46(d) makes the recipient's address mandatory whether or not they
// are registered, and a blank one is invisible to the person issuing the invoice:
// the recipient's credit is what gets questioned, months later.
//
// The refusal must name the MASTER RECORD and must NOT be flagged NotModelled —
// migration 0039 added the column, so the fix is a keystroke, and reporting it as
// a schema gap would send somebody to wait for a migration that already landed.
func TestATaxInvoiceIsIssuedToAPartyWhoseAddressNobodyRecorded(t *testing.T) {
	e := newDocEnv(t)
	const name = "Addressless Traders"
	id, err := masters.NewService(e.pool).CreateLedger(e.ctx, masters.Ledger{
		Name: name, GroupName: "Sundry Debtors",
		GSTIN: docCustomerGSTIN, StateCode: docStateHome,
		RegistrationType: masters.GSTRegular,
	}, "fixture")
	if err != nil {
		t.Fatalf("create ledger: %v", err)
	}
	res, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class:         ledger.Sales,
		PartyLedgerID: id,
		ItemLedgerID:  e.sales,
		VoucherDate:   "2026-05-19",
		Party:         name,
		CreatedBy:     "fixture",
		Inventory:     []ledger.InventoryLine{e.itemLine(e.bolt, "3", "100", "0")},
		GST: &ledger.GSTParams{
			SupplierStateCode:      docStateHome,
			PlaceOfSupplyStateCode: docStateHome,
			CGSTLedgerID:           e.ocgst,
			SGSTLedgerID:           e.osgst,
			IGSTLedgerID:           e.oigst,
		},
	})
	if err != nil {
		t.Fatalf("post invoice: %v", err)
	}

	inv, err := e.store.LoadInvoice(context.Background(), e.company, res.VoucherID)
	if err == nil {
		t.Fatalf("a tax invoice was assembled with the recipient's address blank: recipient %q, "+
			"total %s", inv.Recipient.Name, inv.Total.StringFixed(2))
	}
	var missing *printingdb.MissingParticularError
	if !errors.As(err, &missing) {
		t.Fatalf("the missing address surfaced as %v, not as a typed MissingParticularError", err)
	}
	if !strings.Contains(strings.ToLower(missing.Field), "address") {
		t.Errorf("the refusal names the particular %q; it must name the recipient's ADDRESS",
			missing.Field)
	}
	if !strings.Contains(missing.Source, name) {
		t.Errorf("the refusal's source is %q; it must name the party master to open (%q), not "+
			"just a struct field", missing.Source, name)
	}
	if missing.NotModelled {
		t.Error("the missing address is reported as a schema gap, but migration 0039 added " +
			"ledger.address: the fix is data entry and the error must say so, or nobody types it in")
	}
}

// TestTheRecipientBlockOnTheInvoiceIsNotTheAddressOnTheMaster is named for the
// failure: an invoice posted to the wrong address is an invoice the recipient may
// never receive, and Rule 46(d) makes the address a mandatory particular rather
// than a delivery convenience.
//
// It asserts the whole block — both address lines, the city and the PIN — because
// the column is a newline-separated BLOCK and a loader that read only its first
// line would look correct on a one-line fixture.
func TestTheRecipientBlockOnTheInvoiceIsNotTheAddressOnTheMaster(t *testing.T) {
	e := newDocEnv(t)
	res := e.postMixedRateItemInvoice(t)

	inv, err := e.store.LoadInvoice(context.Background(), e.company, res.VoucherID)
	if err != nil {
		t.Fatalf("a sales voucher to a party with a full address on file could not be printed: %v", err)
	}
	for _, tc := range []struct {
		what, got, want string
	}{
		{"first address line", inv.Recipient.Address1, "Plot 14, Chakan Industrial Area"},
		{"remaining address lines", inv.Recipient.Address2, "Phase II, Nighoje"},
		{"city", inv.Recipient.City, "Pune"},
		{"PIN", inv.Recipient.PIN, "410501"},
	} {
		if tc.got != tc.want {
			t.Errorf("the invoice addresses the recipient's %s as %q, but the party master records "+
				"%q: the invoice would be sent somewhere the customer is not",
				tc.what, tc.got, tc.want)
		}
	}
	// The state NAME, which is a particular in its own right (Rule 46(e)) and is
	// NOT the code: "State: 27" where the statute asks for the name of the State
	// reads as correct to everybody who does not know the difference, and a party
	// block with the name missing refuses every high-value counter sale.
	if inv.Recipient.State != docStateHomeName {
		t.Errorf("the recipient's State is printed as %q, but ledger.state_code is %q, which is "+
			"%s: an invoice that does not name the recipient's State cannot carry the Rule 46(e) "+
			"particulars of a supply of fifty thousand rupees or more to an unregistered person",
			inv.Recipient.State, docStateHome, docStateHomeName)
	}
	if inv.Recipient.StateCode != docStateHome {
		t.Errorf("the recipient's state code is %q, want %q", inv.Recipient.StateCode, docStateHome)
	}
}

// TestTheInvoiceStatesADiscountOfTwentyRupeesOnALineDiscountedByTwentyPerCent is
// named for the consequence of the unit bug it pins. stock_movement stores
// discount_pct — a PERCENTAGE, CHECKed 0..100 — and for one wave this assembly
// handed it to a renderer field called Discount, which printed it through
// FormatMoney as rupees. A line of 10 bolts at Rs 100 less 20% then printed
// "Rate 100.00, Qty 10, Less: Discount 20.00, Amount 800.00": four figures that
// cannot all be true, understating the discount by Rs 180.
//
// So this asserts the UNIT of the number crossing the seam: it must equal the
// stored PERCENTAGE (20), read back with an independent query, and NOT the rupee
// discount (200.00) that a reader of the printed line would compute.
func TestTheInvoiceStatesADiscountOfTwentyRupeesOnALineDiscountedByTwentyPerCent(t *testing.T) {
	e := newDocEnv(t)
	res, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class:         ledger.Sales,
		PartyLedgerID: e.customer,
		ItemLedgerID:  e.sales,
		VoucherDate:   "2026-05-21",
		Party:         docNameCustomer,
		CreatedBy:     "fixture",
		Inventory:     []ledger.InventoryLine{e.itemLine(e.bolt, "10", "100", "20")},
		GST: &ledger.GSTParams{
			SupplierStateCode:      docStateHome,
			PlaceOfSupplyStateCode: docStateHome,
			CGSTLedgerID:           e.ocgst,
			SGSTLedgerID:           e.osgst,
			IGSTLedgerID:           e.oigst,
		},
	})
	if err != nil {
		t.Fatalf("post a discounted item invoice: %v", err)
	}
	versionID := e.currentVersion(t, res.VoucherID)

	inv, err := e.store.LoadInvoice(context.Background(), e.company, res.VoucherID)
	if err != nil {
		t.Fatalf("load the discounted invoice: %v", err)
	}
	if len(inv.Items) != 1 {
		t.Fatalf("a one-line invoice printed %d lines", len(inv.Items))
	}
	line := inv.Items[0]

	wantPct := e.storedDiscountPct(t, versionID, line.SerialNo)
	if !line.DiscountPct.Equal(wantPct) {
		t.Errorf("the invoice line carries a discount of %s but the stock movement records "+
			"%s per cent: the figure on the paper is not the figure in the books",
			line.DiscountPct.String(), wantPct.String())
	}

	// THE UNIT, stated as the two figures that cannot both be right. The rupee
	// discount on this line is qty × rate × pct ÷ 100 = 200.00; the percentage is
	// 20. A value of 200 here means the seam is carrying rupees under a field named
	// for a percentage, and the renderer would print "Less: Discount 200%".
	switch {
	case line.DiscountPct.Equal(decimal.RequireFromString("200")):
		t.Errorf("the discount crossing the seam is %s, which is the RUPEE discount on this line, "+
			"not the 20 per cent the books recorded: the renderer prints this field as a "+
			"percentage, so the invoice would state a discount of 200%%",
			line.DiscountPct.String())
	case !line.DiscountPct.Equal(decimal.RequireFromString("20")):
		t.Errorf("a line entered at 20 per cent off carries %s: neither the percentage nor the "+
			"rupee figure, so the paper cannot be reconciled with the movement at all",
			line.DiscountPct.String())
	}

	// And the line amount is the ALREADY-NET value the movement holds, so the
	// percentage and the amount together tell one story.
	wantAmount := e.storedMovementAmount(t, versionID, line.SerialNo)
	if !line.Amount.Equal(wantAmount) {
		t.Errorf("the discounted line is billed at %s but the movement records %s",
			line.Amount.StringFixed(2), wantAmount.StringFixed(2))
	}
	if !wantAmount.Equal(decimal.RequireFromString("800")) {
		t.Fatalf("the fixture itself is wrong: 10 at 100 less 20%% must net to 800.00, not %s",
			wantAmount.StringFixed(2))
	}
}

// ---------------------------------------------------------------------------
// 5. The documents that are not invoices
// ---------------------------------------------------------------------------

// postReceipt posts a Receipt that settles a named bill, which is what a receipt
// has to state: not just an amount, but WHICH bill it paid.
func (e *docEnv) postReceipt(t *testing.T) ledger.PostResult {
	t.Helper()
	res, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class:         ledger.Receipt,
		PartyLedgerID: e.customer,
		VoucherDate:   "2026-05-20",
		Party:         docNameCustomer,
		Narration:     "Cheque 004512 against INV/2026/0001",
		CreatedBy:     "fixture",
		Postings: []ledger.PostingInput{
			{LedgerID: e.cash, DrCr: ledger.Debit, Amount: decimal.RequireFromString("1740")},
			{LedgerID: e.customer, DrCr: ledger.Credit, Amount: decimal.RequireFromString("1740")},
		},
		BillAllocations: []ledger.BillAllocationInput{{
			LedgerID: e.customer, RefName: "INV/2026/0001", RefType: ledger.RefAgainst,
			DrCr: ledger.Credit, Amount: decimal.RequireFromString("1740"),
		}},
	})
	if err != nil {
		t.Fatalf("post receipt: %v", err)
	}
	return res
}

// TestAReceiptDoesNotSayWhichBillItSettled is named for the failure. A receipt
// that names an amount but not the bill is the receipt a customer produces to
// claim a DIFFERENT invoice was paid, and the books cannot contradict them from
// the paper.
func TestAReceiptDoesNotSayWhichBillItSettled(t *testing.T) {
	e := newDocEnv(t)
	res := e.postReceipt(t)
	versionID := e.currentVersion(t, res.VoucherID)
	legs := e.legsOf(t, versionID)

	doc, err := e.store.LoadVoucherDoc(context.Background(), e.company, res.VoucherID)
	if err != nil {
		t.Fatalf("a receipt could not be assembled: %v", err)
	}
	if doc.Kind != printing.KindReceipt {
		t.Errorf("a Receipt voucher printed as %q", doc.Kind)
	}
	if len(doc.Lines) != len(legs) {
		t.Fatalf("the receipt prints %d legs but the voucher posted %d", len(doc.Lines), len(legs))
	}
	var sawBillRef bool
	for _, l := range doc.Lines {
		posted, ok := legs[l.Ledger]
		if !ok {
			t.Errorf("the receipt prints a leg against %q, which this voucher never posted to", l.Ledger)
			continue
		}
		if !l.Amount.Equal(posted.amount) {
			t.Errorf("the receipt states %s against %q but %s was posted: the acknowledgement "+
				"a customer holds is not the amount received",
				l.Amount.StringFixed(2), l.Ledger, posted.amount.StringFixed(2))
		}
		if l.IsDebit != posted.debit {
			t.Errorf("the receipt shows %q on the wrong side: a receipt that reverses the "+
				"sides reads as money paid out", l.Ledger)
		}
		if l.Ledger == docNameCustomer && l.BillRef == "INV/2026/0001" {
			sawBillRef = true
		}
	}
	if !sawBillRef {
		t.Error("the receipt does not name the bill it settled, though the voucher carries an " +
			"AGAINST allocation for INV/2026/0001")
	}
	if party, ok := legs[docNameCustomer]; ok && !doc.Total.Equal(party.amount) {
		t.Errorf("the receipt total is %s but %s was credited to the customer",
			doc.Total.StringFixed(2), party.amount.StringFixed(2))
	}
	if doc.Narration == "" {
		t.Error("the receipt carries no narration, though the voucher recorded one: the cheque " +
			"number is the only thing tying the paper to the bank")
	}
}

// TestAskingTheWrongLoaderLooksLikeAMissingFeature pins the two-loader contract.
// A Receipt HAS a document; it is simply not an Invoice. Reporting that as
// "no document for this class" would send the next reader looking for a feature
// that is already there.
func TestAskingTheWrongLoaderLooksLikeAMissingFeature(t *testing.T) {
	e := newDocEnv(t)
	sale := e.postMixedRateItemInvoice(t)
	receipt := e.postReceipt(t)

	if _, err := e.store.LoadInvoice(context.Background(), e.company, receipt.VoucherID); !errors.Is(err, printingdb.ErrNotAnInvoice) {
		t.Errorf("LoadInvoice on a Receipt returned %v, want ErrNotAnInvoice", err)
	}
	if _, err := e.store.LoadVoucherDoc(context.Background(), e.company, sale.VoucherID); !errors.Is(err, printingdb.ErrNotAVoucherDoc) {
		t.Errorf("LoadVoucherDoc on a Sales invoice returned %v, want ErrNotAVoucherDoc", err)
	}
}

// TestASaleThatChargedNoTaxPrintsAsATaxInvoice is named for the breach. Rule 49
// governs a supply on which no tax is charged, and a bill of supply may not state
// a rate or an amount of tax — printing "CGST 0.00" on an exempt supply invites
// the recipient to claim credit that does not exist.
func TestASaleThatChargedNoTaxPrintsAsATaxInvoice(t *testing.T) {
	e := newDocEnv(t)
	res, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class:         ledger.Sales,
		PartyLedgerID: e.customer,
		VoucherDate:   "2026-05-16",
		Party:         docNameCustomer,
		CreatedBy:     "fixture",
		Postings: []ledger.PostingInput{
			{LedgerID: e.customer, DrCr: ledger.Debit, Amount: decimal.RequireFromString("2500")},
			{LedgerID: e.sales, DrCr: ledger.Credit, Amount: decimal.RequireFromString("2500")},
		},
	})
	if err != nil {
		t.Fatalf("post untaxed sale: %v", err)
	}

	inv, err := e.store.LoadInvoice(context.Background(), e.company, res.VoucherID)
	if err != nil {
		t.Fatalf("an exempt supply could not be printed at all: %v", err)
	}
	if inv.Kind != printing.KindBillOfSupply {
		t.Errorf("a sale charging no tax was assembled as %q, not a bill of supply", inv.Kind)
	}
	if len(inv.Taxes) != 0 {
		t.Errorf("a bill of supply states %d tax heads (%v); Rule 49 permits none",
			len(inv.Taxes), inv.Taxes)
	}
	if !inv.Total.Equal(decimal.RequireFromString("2500")) {
		t.Errorf("the bill of supply totals %s, but 2500.00 was debited to the customer",
			inv.Total.StringFixed(2))
	}
	if !inv.Subtotal.Equal(inv.Total) || !inv.RoundOff.IsZero() {
		t.Errorf("an untaxed supply printed a subtotal of %s and a round-off of %s against a "+
			"total of %s: the three cannot disagree when no tax was charged",
			inv.Subtotal.StringFixed(2), inv.RoundOff.StringFixed(2), inv.Total.StringFixed(2))
	}
	// The line description of an accounting (non-item) invoice is the revenue
	// ledger, which is how Tally prints one — the only description the books hold.
	if len(inv.Items) != 0 {
		for _, it := range inv.Items {
			if it.Description == "" {
				t.Error("an accounting invoice line printed with no description at all, though " +
					"the voucher has exactly one revenue leg to name")
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 6. The template travels with the document
// ---------------------------------------------------------------------------

// TestACustomisedInvoicePrintsOnTheDefaultTemplateAnyway is named for the
// failure: a company that changed its declaration, its terms and its bank block
// and then found none of it on the invoice.
//
// The second half is the more important one. A company with ZERO template rows
// must still assemble a document — the built-in default lives in code — because
// that is the state EVERY company is in on its first day.
func TestACustomisedInvoicePrintsOnTheDefaultTemplateAnyway(t *testing.T) {
	e := newDocEnv(t)
	res := e.postMixedRateItemInvoice(t)
	ctx := context.Background()

	// Zero rows first: the ordinary case.
	inv, err := e.store.LoadInvoice(ctx, e.company, res.VoucherID)
	if err != nil {
		t.Fatalf("a company with no template row could not print at all: %v", err)
	}
	if inv.Overrides.Title != "" || inv.Overrides.Declaration != "" {
		t.Errorf("a company that customised nothing was handed overrides %+v", inv.Overrides)
	}

	want := printing.TemplateOverrides{
		Title:           "TAX INVOICE (ORIGINAL FOR RECIPIENT)",
		Declaration:     "Certified that the particulars given above are true and correct.",
		Terms:           "Payment due within 30 days.",
		SignatureLabel:  "Authorised Signatory",
		HeaderLines:     []string{"An ISO 9001:2015 certified company"},
		ShowBankDetails: true,
		BankLines:       []string{"HDFC Bank, Andheri East", "A/c 50200012345678", "IFSC HDFC0000123"},
	}
	if err := e.store.SaveOverrides(ctx, e.company, printing.KindTaxInvoice, want); err != nil {
		t.Fatalf("save overrides: %v", err)
	}

	inv, err = e.store.LoadInvoice(ctx, e.company, res.VoucherID)
	if err != nil {
		t.Fatalf("load invoice after customisation: %v", err)
	}
	if inv.Overrides.Title != want.Title || inv.Overrides.Declaration != want.Declaration {
		t.Errorf("the customised title/declaration did not reach the document: got %+v", inv.Overrides)
	}
	if !inv.Overrides.ShowBankDetails || len(inv.Overrides.BankLines) != 3 {
		t.Errorf("the bank block a company staged for its invoices did not reach the document: %+v",
			inv.Overrides)
	}
}

// TestTheOverridesOfTheWrongDocumentKindReachTheDocument is named for the
// breach: overrides are keyed per (company, DocKind), so a receipt's declaration
// appearing on an invoice would mean the kind resolved by the assembly is not
// the kind the overrides were read for. A bill of supply is the sharp case —
// it is a DIFFERENT kind from a tax invoice, from the same voucher class.
func TestTheOverridesOfTheWrongDocumentKindReachTheDocument(t *testing.T) {
	e := newDocEnv(t)
	ctx := context.Background()

	taxed := e.postMixedRateItemInvoice(t)
	untaxed, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class:         ledger.Sales,
		PartyLedgerID: e.customer,
		VoucherDate:   "2026-05-17",
		Party:         docNameCustomer,
		CreatedBy:     "fixture",
		Postings: []ledger.PostingInput{
			{LedgerID: e.customer, DrCr: ledger.Debit, Amount: decimal.RequireFromString("400")},
			{LedgerID: e.sales, DrCr: ledger.Credit, Amount: decimal.RequireFromString("400")},
		},
	})
	if err != nil {
		t.Fatalf("post untaxed sale: %v", err)
	}

	if err := e.store.SaveOverrides(ctx, e.company, printing.KindTaxInvoice,
		printing.TemplateOverrides{Title: "TAX INVOICE"}); err != nil {
		t.Fatalf("save tax-invoice overrides: %v", err)
	}
	if err := e.store.SaveOverrides(ctx, e.company, printing.KindBillOfSupply,
		printing.TemplateOverrides{Title: "BILL OF SUPPLY"}); err != nil {
		t.Fatalf("save bill-of-supply overrides: %v", err)
	}

	for _, tc := range []struct {
		what    string
		voucher uuid.UUID
		want    string
	}{
		{"a taxed sale", taxed.VoucherID, "TAX INVOICE"},
		{"an exempt sale", untaxed.VoucherID, "BILL OF SUPPLY"},
	} {
		inv, err := e.store.LoadInvoice(ctx, e.company, tc.voucher)
		if err != nil {
			t.Fatalf("load %s: %v", tc.what, err)
		}
		if inv.Overrides.Title != tc.want {
			t.Errorf("%s printed under the title %q, want %q: the document was assembled as one "+
				"kind and its template read for another", tc.what, inv.Overrides.Title, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// 7. The e-invoice reference
// ---------------------------------------------------------------------------

// TestARegisteredEInvoiceLosesItsIRNOnThePrintedCopy is named for the breach.
// Rule 48(4) makes the IRN part of what a registered invoice IS: a copy without
// it is not the document that was registered, and the recipient cannot verify it
// against the portal.
func TestARegisteredEInvoiceLosesItsIRNOnThePrintedCopy(t *testing.T) {
	e := newDocEnv(t)
	res := e.postMixedRateItemInvoice(t)
	const irn = "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"

	// The freeze hook records the IRN here (migration 0009). Written directly:
	// this slice reads, and the registration path is statutoryd's.
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO voucher_external_reference
			   (company_id, voucher_id, kind, ref_value, pinned_voucher_no, created_by)
			 VALUES ($1, $2, 'IRN', $3, $4, 'statutoryd')`,
			e.company, res.VoucherID, irn, res.VoucherNo)
		return err
	}); err != nil {
		t.Fatalf("record the IRN: %v", err)
	}

	inv, err := e.store.LoadInvoice(context.Background(), e.company, res.VoucherID)
	if err != nil {
		t.Fatalf("load invoice: %v", err)
	}
	if inv.IRN != irn {
		t.Errorf("the printed copy states the IRN as %q, but the invoice was registered as %q",
			inv.IRN, irn)
	}
}

// TestAnUnregisteredInvoicePrintsAnEmptyIRNField pins the ordinary case: most
// taxpayers are below the Rule 48(4) turnover band, have no IRN, and must still
// print. An error here would take e-invoicing's absence and turn it into an
// outage.
func TestAnUnregisteredInvoicePrintsAnEmptyIRNField(t *testing.T) {
	e := newDocEnv(t)
	res := e.postMixedRateItemInvoice(t)
	inv, err := e.store.LoadInvoice(context.Background(), e.company, res.VoucherID)
	if err != nil {
		t.Fatalf("an invoice with no e-invoice registration could not be printed: %v", err)
	}
	if inv.IRN != "" {
		t.Errorf("an invoice that was never registered carries the IRN %q", inv.IRN)
	}
	if inv.AckNo != "" {
		t.Errorf("an acknowledgement number %q appeared from a schema that has no column for one",
			inv.AckNo)
	}
}

// TestTheDueDateOnTheInvoiceWasNeverAgreedWithTheCustomer is named for the
// failure. The due date is READ from the bill-wise allocation, never computed
// from ledger.credit_days: a date the books do not hold is a date nobody agreed
// to, and it is the date a customer would be chased on.
func TestTheDueDateOnTheInvoiceWasNeverAgreedWithTheCustomer(t *testing.T) {
	e := newDocEnv(t)
	res, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class:         ledger.Sales,
		PartyLedgerID: e.customer,
		VoucherDate:   "2026-05-18",
		Party:         docNameCustomer,
		CreatedBy:     "fixture",
		Postings: []ledger.PostingInput{
			{LedgerID: e.customer, DrCr: ledger.Debit, Amount: decimal.RequireFromString("1000")},
			{LedgerID: e.sales, DrCr: ledger.Credit, Amount: decimal.RequireFromString("1000")},
		},
		BillAllocations: []ledger.BillAllocationInput{{
			LedgerID: e.customer, RefName: "SUP/2026/0009", RefType: ledger.RefNew,
			DrCr: ledger.Debit, Amount: decimal.RequireFromString("1000"),
			DueDate: "2026-06-17",
		}},
	})
	if err != nil {
		t.Fatalf("post invoice with a bill: %v", err)
	}
	inv, err := e.store.LoadInvoice(context.Background(), e.company, res.VoucherID)
	if err != nil {
		t.Fatalf("load invoice: %v", err)
	}
	if inv.DueDate == nil {
		t.Fatal("the invoice carries no due date, though its bill-wise allocation records 2026-06-17")
	}
	if got := inv.DueDate.Format("2006-01-02"); got != "2026-06-17" {
		t.Errorf("the invoice is due on %s but the bill says 2026-06-17", got)
	}

	// And an invoice with no allocation must carry NO due date rather than an
	// invented one.
	plain := e.postMixedRateItemInvoice(t)
	inv, err = e.store.LoadInvoice(context.Background(), e.company, plain.VoucherID)
	if err != nil {
		t.Fatalf("load invoice: %v", err)
	}
	if inv.DueDate != nil {
		t.Errorf("an invoice with no agreed due date printed one: %s",
			inv.DueDate.Format("2006-01-02"))
	}
}

// ---------------------------------------------------------------------------
// 8. A round-off is a rounding, not a place to put the difference
// ---------------------------------------------------------------------------

// postAccountingInvoiceWithResidual posts a Sales voucher whose GST is derived on
// a taxable base of 1,000.00 at 18% (CGST 90.00 + SGST 90.00) while the REVENUE
// leg and the PARTY leg are stated separately, so the caller chooses the residual.
//
// It is an accounting (non-item) invoice on purpose. deriveAndVerifyGST checks the
// posted tax against the derived split and takes the taxable BASE as given — that
// hole is documented in ledger.go and closed only for item invoices, where the
// base is generated from the stock lines. So this is exactly the shape of voucher a
// real mis-key produces: tax that verifies, against a base the rest of the voucher
// does not agree with.
func (e *docEnv) postAccountingInvoiceWithResidual(t *testing.T, date, partyDebit, revenueCredit string) ledger.PostResult {
	t.Helper()
	res, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class:         ledger.Sales,
		PartyLedgerID: e.customer,
		VoucherDate:   date,
		Party:         docNameCustomer,
		CreatedBy:     "fixture",
		Postings: []ledger.PostingInput{
			{LedgerID: e.customer, DrCr: ledger.Debit, Amount: decimal.RequireFromString(partyDebit)},
			{LedgerID: e.sales, DrCr: ledger.Credit, Amount: decimal.RequireFromString(revenueCredit)},
			{LedgerID: e.ocgst, DrCr: ledger.Credit, Amount: decimal.RequireFromString("90")},
			{LedgerID: e.osgst, DrCr: ledger.Credit, Amount: decimal.RequireFromString("90")},
		},
		GST: &ledger.GSTParams{
			SupplierStateCode:      docStateHome,
			PlaceOfSupplyStateCode: docStateHome,
			Lines: []compliance.Line{{
				TaxableValue: decimal.RequireFromString("1000"),
				GSTRatePct:   decimal.RequireFromString("18"),
				HSN:          "7318",
			}},
			CGSTLedgerID: e.ocgst,
			SGSTLedgerID: e.osgst,
			IGSTLedgerID: e.oigst,
		},
	})
	if err != nil {
		t.Fatalf("post an accounting sales voucher (party %s, revenue %s): %v",
			partyDebit, revenueCredit, err)
	}
	return res
}

// TestFiveHundredRupeesOfUnexplainedValuePrintsOnTheInvoiceAsARoundOff is named
// for the breach, and the breach is the LABEL rather than the arithmetic.
//
// inv.RoundOff is a residual — Total - Subtotal - tax — which is what makes the
// printed total equal the receivable by construction. But the renderer labels it
// "Round Off", and a reader checks a round-off by glancing at it. So Rs 500 of
// freight billed with no line behind it, or a mis-keyed taxable value, would go
// out on a tax invoice under the one label that stops anybody adding it up. A
// round-off cannot exceed a rupee, so this must be refused, and the refusal must
// name all THREE figures somebody has to reconcile.
func TestFiveHundredRupeesOfUnexplainedValuePrintsOnTheInvoiceAsARoundOff(t *testing.T) {
	e := newDocEnv(t)
	// Party debited 1,680.00 against a taxable value of 1,000.00 plus 180.00 of
	// tax: a residual of 500.00.
	res := e.postAccountingInvoiceWithResidual(t, "2026-05-22", "1680", "1500")

	inv, err := e.store.LoadInvoice(context.Background(), e.company, res.VoucherID)
	if err == nil {
		t.Fatalf("an invoice stating a taxable value of %s and tax of %s printed a total of %s "+
			"with %s under \"Round Off\"",
			inv.Subtotal.StringFixed(2), headTotal(inv.Taxes, "CGST").Add(headTotal(inv.Taxes, "SGST")).StringFixed(2),
			inv.Total.StringFixed(2), inv.RoundOff.StringFixed(2))
	}

	var residual *printingdb.ResidualNotARoundOffError
	if !errors.As(err, &residual) {
		t.Fatalf("the oversized round-off surfaced as %v, not as a typed "+
			"ResidualNotARoundOffError: a caller cannot tell an unbalanced voucher from a "+
			"database fault", err)
	}
	if !errors.Is(err, printingdb.ErrResidualNotARoundOff) {
		t.Error("the typed error does not unwrap to ErrResidualNotARoundOff, so errors.Is cannot " +
			"classify it")
	}
	// It must ALSO classify as a missing particular, because that is what the HTTP
	// layer maps to 422. Without it the operator's mis-keyed voucher reaches them
	// as a 500 — our bug rather than their data.
	if !errors.Is(err, printingdb.ErrMissingParticular) {
		t.Error("the refusal does not unwrap to ErrMissingParticular, so the print route would " +
			"return 500 for a voucher an operator can fix, instead of 422")
	}

	for _, want := range []struct {
		what   string
		figure decimal.Decimal
	}{
		{"the taxable value", decimal.RequireFromString("1000")},
		{"the tax charged", decimal.RequireFromString("180")},
		{"the amount posted to the party", decimal.RequireFromString("1680")},
		{"the residual", decimal.RequireFromString("500")},
	} {
		if !strings.Contains(err.Error(), want.figure.StringFixed(2)) {
			t.Errorf("the refusal does not state %s (%s): %q — somebody has to reconcile three "+
				"figures and the message must name all of them",
				want.what, want.figure.StringFixed(2), err.Error())
		}
	}
	if !residual.Residual.Equal(decimal.RequireFromString("500")) {
		t.Errorf("the typed error reports a residual of %s, want 500.00",
			residual.Residual.StringFixed(2))
	}
	if !residual.TaxableValue.Equal(decimal.RequireFromString("1000")) ||
		!residual.TaxTotal.Equal(decimal.RequireFromString("180")) ||
		!residual.PartyLeg.Equal(decimal.RequireFromString("1680")) {
		t.Errorf("the typed error carries taxable %s, tax %s, party leg %s; want 1000.00, "+
			"180.00 and 1680.00", residual.TaxableValue.StringFixed(2),
			residual.TaxTotal.StringFixed(2), residual.PartyLeg.StringFixed(2))
	}
	if !strings.Contains(residual.Rule, "46(j)") || !strings.Contains(residual.Rule, "46(k)") {
		t.Errorf("the refusal cites %q; the total value of supply and the taxable value are what "+
			"disagree, so it must cite Rule 46(j) and 46(k)", residual.Rule)
	}
}

// TestAGenuineFortyNinePaiseRoundOffIsRefusedAsIfItWereFreight is the other side
// of the boundary, and it is the test that stops the refusal above from being
// implemented as "the totals must close exactly".
//
// Rounding a bill to the nearest rupee is ordinary — the round-off line exists
// for it — and a business that could not print an invoice carrying 49 paise of
// rounding would be unable to print most of its invoices.
func TestAGenuineFortyNinePaiseRoundOffIsRefusedAsIfItWereFreight(t *testing.T) {
	e := newDocEnv(t)
	// Party debited 1,180.49 against 1,000.00 taxable plus 180.00 tax: a residual
	// of 0.49, which is a rounding and nothing else.
	res := e.postAccountingInvoiceWithResidual(t, "2026-05-23", "1180.49", "1000.49")

	inv, err := e.store.LoadInvoice(context.Background(), e.company, res.VoucherID)
	if err != nil {
		t.Fatalf("an invoice carrying 49 paise of rounding could not be printed at all: %v", err)
	}
	if !inv.RoundOff.Equal(decimal.RequireFromString("0.49")) {
		t.Errorf("the invoice states a round-off of %s, but the books make the residual 0.49",
			inv.RoundOff.StringFixed(2))
	}
	// And it still adds up, which is the property the residual exists to preserve.
	sum := inv.Subtotal.Add(inv.RoundOff)
	for _, h := range inv.Taxes {
		sum = sum.Add(h.Amount)
	}
	if !sum.Equal(inv.Total) {
		t.Errorf("the invoice does not add up: taxable %s + tax + round-off %s = %s against a "+
			"stated total of %s", inv.Subtotal.StringFixed(2), inv.RoundOff.StringFixed(2),
			sum.StringFixed(2), inv.Total.StringFixed(2))
	}
}

// ---------------------------------------------------------------------------
// 9. A credit note is a document, not a code-reading conclusion
// ---------------------------------------------------------------------------

// TestACreditNoteCannotBePrintedAtAll is named for the breach it closes, and the
// breach was total: printing.validateVoucherDoc refuses a note that does not name
// the invoice it adjusts AND that invoice's date, and this loader supplied neither
// the date nor a reason — so every credit note in the system was unprintable, and
// nothing said so.
//
// This test EXERCISES it end to end rather than reading the code: it posts an
// invoice that opens a bill, posts a credit note against that bill, assembles the
// note, and then RENDERS it. A load that succeeds and a render that refuses is
// still a note nobody can hand to a customer.
func TestACreditNoteCannotBePrintedAtAll(t *testing.T) {
	e := newDocEnv(t)
	const (
		billRef     = "INV/2026/0031"
		invoiceDate = "2026-05-24"
		reason      = "Sales return: 4 bolts rejected at the customer's incoming inspection."
	)

	// The invoice the note will adjust, opening a bill by that reference.
	if _, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class:         ledger.Sales,
		PartyLedgerID: e.customer,
		VoucherDate:   invoiceDate,
		Party:         docNameCustomer,
		CreatedBy:     "fixture",
		Postings: []ledger.PostingInput{
			{LedgerID: e.customer, DrCr: ledger.Debit, Amount: decimal.RequireFromString("1740")},
			{LedgerID: e.sales, DrCr: ledger.Credit, Amount: decimal.RequireFromString("1740")},
		},
		BillAllocations: []ledger.BillAllocationInput{{
			LedgerID: e.customer, RefName: billRef, RefType: ledger.RefNew,
			DrCr: ledger.Debit, Amount: decimal.RequireFromString("1740"),
		}},
	}); err != nil {
		t.Fatalf("post the invoice the note adjusts: %v", err)
	}

	note, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class:         ledger.CreditNote,
		PartyLedgerID: e.customer,
		VoucherDate:   "2026-06-02",
		Party:         docNameCustomer,
		Narration:     reason,
		CreatedBy:     "fixture",
		Postings: []ledger.PostingInput{
			{LedgerID: e.sales, DrCr: ledger.Debit, Amount: decimal.RequireFromString("400")},
			{LedgerID: e.customer, DrCr: ledger.Credit, Amount: decimal.RequireFromString("400")},
		},
		BillAllocations: []ledger.BillAllocationInput{{
			LedgerID: e.customer, RefName: billRef, RefType: ledger.RefAgainst,
			DrCr: ledger.Credit, Amount: decimal.RequireFromString("400"),
		}},
	})
	if err != nil {
		t.Fatalf("post the credit note: %v", err)
	}

	doc, err := e.store.LoadVoucherDoc(context.Background(), e.company, note.VoucherID)
	if err != nil {
		t.Fatalf("a credit note against a named bill could not be assembled: %v", err)
	}
	if doc.Kind != printing.KindCreditNote {
		t.Fatalf("a Credit Note voucher was assembled as %q", doc.Kind)
	}
	if doc.AgainstNumber != billRef {
		t.Errorf("the note says it adjusts %q, but it is allocated against %q: a note nobody can "+
			"match to an invoice cannot be applied to one", doc.AgainstNumber, billRef)
	}
	if doc.AgainstDate == nil {
		t.Fatalf("the note names invoice %q and no date for it: an invoice serial number repeats "+
			"every financial year, so the recipient still cannot tell which supply the credit "+
			"belongs to", doc.AgainstNumber)
	}
	if got := doc.AgainstDate.Format("2006-01-02"); got != invoiceDate {
		t.Errorf("the note dates the original invoice %s, but that bill was opened by the voucher "+
			"dated %s: the date printed is one on which no invoice was issued", got, invoiceDate)
	}
	if doc.Reason != reason {
		t.Errorf("the note states its reason as %q, but the voucher's narration records %q: the "+
			"reason is where the operator typed it and it is what the recipient reads",
			doc.Reason, reason)
	}
	// The same sentence must not appear twice on one page: the narration was
	// PROMOTED into the statutory reason, not copied into it.
	if doc.Narration != "" {
		t.Errorf("the note carries the reason %q AND a narration %q; the renderer draws both, so "+
			"the same sentence would print twice and read as a rendering fault",
			doc.Reason, doc.Narration)
	}
	// The party block, which Rule 53(1A) needs as much as an invoice does.
	if doc.Party == nil || doc.Party.Address1 == "" {
		t.Errorf("the credit note names the party with no address: %+v", doc.Party)
	}

	// AND IT ACTUALLY PRINTS. This is the assertion that makes the rest of the
	// test more than a struct comparison: the renderer's own validation is what was
	// refusing every note.
	pdf, err := printing.RenderVoucherDoc(doc)
	if err != nil {
		t.Fatalf("the assembled credit note was REFUSED by the renderer, so no credit note can "+
			"be handed to a customer at all: %v", err)
	}
	if len(pdf) == 0 {
		t.Fatal("the credit note rendered to zero bytes")
	}
}

// TestACreditNoteCitesADeletedInvoiceAsIfItStillStood is named for the breach.
// The date of the adjusted invoice is read from the voucher that opened the bill,
// and voucher_head KEEPS the head row of a deleted voucher — so a lookup without
// the is_deleted filter would date a live credit note from a CANCELLED invoice,
// which is a document the recipient can match to nothing.
func TestACreditNoteCitesADeletedInvoiceAsIfItStillStood(t *testing.T) {
	e := newDocEnv(t)
	const billRef = "INV/2026/0032"

	invoice, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class:         ledger.Sales,
		PartyLedgerID: e.customer,
		VoucherDate:   "2026-05-25",
		Party:         docNameCustomer,
		CreatedBy:     "fixture",
		Postings: []ledger.PostingInput{
			{LedgerID: e.customer, DrCr: ledger.Debit, Amount: decimal.RequireFromString("900")},
			{LedgerID: e.sales, DrCr: ledger.Credit, Amount: decimal.RequireFromString("900")},
		},
		BillAllocations: []ledger.BillAllocationInput{{
			LedgerID: e.customer, RefName: billRef, RefType: ledger.RefNew,
			DrCr: ledger.Debit, Amount: decimal.RequireFromString("900"),
		}},
	})
	if err != nil {
		t.Fatalf("post the invoice: %v", err)
	}
	note, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class:         ledger.CreditNote,
		PartyLedgerID: e.customer,
		VoucherDate:   "2026-06-03",
		Party:         docNameCustomer,
		Narration:     "Full return.",
		CreatedBy:     "fixture",
		Postings: []ledger.PostingInput{
			{LedgerID: e.sales, DrCr: ledger.Debit, Amount: decimal.RequireFromString("900")},
			{LedgerID: e.customer, DrCr: ledger.Credit, Amount: decimal.RequireFromString("900")},
		},
		BillAllocations: []ledger.BillAllocationInput{{
			LedgerID: e.customer, RefName: billRef, RefType: ledger.RefAgainst,
			DrCr: ledger.Credit, Amount: decimal.RequireFromString("900"),
		}},
	})
	if err != nil {
		t.Fatalf("post the credit note: %v", err)
	}
	if _, err := e.led.DeleteVoucher(e.ctx, invoice.VoucherID, "fixture"); err != nil {
		t.Fatalf("delete the invoice: %v", err)
	}

	doc, err := e.store.LoadVoucherDoc(context.Background(), e.company, note.VoucherID)
	if err != nil {
		t.Fatalf("assemble the note: %v", err)
	}
	if doc.AgainstDate != nil {
		t.Errorf("the note is dated against an invoice of %s that has been CANCELLED: the "+
			"recipient would be handed a credit against a document that no longer exists",
			doc.AgainstDate.Format("2006-01-02"))
	}
	// And with no date it must be REFUSED rather than printed with a blank there.
	if _, err := printing.RenderVoucherDoc(doc); err == nil {
		t.Error("a credit note naming an invoice it cannot date was rendered anyway")
	}
}

// ---------------------------------------------------------------------------
// 10. The NAME of the State (Rule 46(n) and Rule 46(e))
// ---------------------------------------------------------------------------

// invoiceText renders an assembled invoice and returns the text a reader would
// see, one show-text operand per line joined with newlines.
//
// RENDERING IS THE POINT OF THESE TESTS AND NOT A FLOURISH. LoadInvoice succeeded
// for an inter-State supply long before the place of supply had a name — the
// refusal was in printing.ValidateRule46, which RenderInvoice calls, so a test
// that only asserted on the assembled struct would have reported the whole
// feature working while every IGST sale in the product answered 4xx.
func invoiceText(t *testing.T, inv printing.Invoice) string {
	t.Helper()
	pdf, err := printing.RenderInvoice(inv)
	if err != nil {
		t.Fatalf("the assembled %s was REFUSED by the renderer, so no such invoice can be issued "+
			"to a customer at all: %v", inv.Kind, err)
	}
	if len(pdf) == 0 {
		t.Fatal("the invoice rendered to zero bytes")
	}
	lines, err := printing.ExtractText(pdf)
	if err != nil {
		t.Fatalf("extract the text of the rendered invoice: %v", err)
	}
	return strings.Join(lines, "\n")
}

// TestNoInterStateTaxInvoiceCanBeIssuedAtAll is named for the outage it closes,
// because that is what it was: printingdb never filled Invoice.PlaceOfSupply (the
// NAME of the State), printing.ValidateRule46 requires it on a supply involving
// inter-State trade — Rule 46(n), "the place of supply along with the name of the
// State" — and so EVERY IGST sale answered 4xx, including the one the demo seed
// posts. There was no field an operator could fill in either: the name was never
// data anybody keys, it is a property of the two-digit code.
//
// This drives the whole path: post an inter-State sale, assemble it, RENDER it,
// and read the name of the State back out of the PDF.
func TestNoInterStateTaxInvoiceCanBeIssuedAtAll(t *testing.T) {
	e := newDocEnv(t)
	res, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class:         ledger.Sales,
		PartyLedgerID: e.away,
		ItemLedgerID:  e.sales,
		VoucherDate:   "2026-05-26",
		Party:         docNameAway,
		CreatedBy:     "fixture",
		Inventory:     []ledger.InventoryLine{e.itemLine(e.bolt, "4", "250", "0")},
		GST: &ledger.GSTParams{
			SupplierStateCode:      docStateHome,
			PlaceOfSupplyStateCode: docStateAway,
			CGSTLedgerID:           e.ocgst,
			SGSTLedgerID:           e.osgst,
			IGSTLedgerID:           e.oigst,
		},
	})
	if err != nil {
		t.Fatalf("post the inter-state invoice: %v", err)
	}

	inv, err := e.store.LoadInvoice(context.Background(), e.company, res.VoucherID)
	if err != nil {
		t.Fatalf("an inter-state sale could not even be assembled: %v", err)
	}
	if inv.PlaceOfSupply != docStateAwayName {
		t.Errorf("the place of supply is named %q; the voucher records state code %q, which is %s. "+
			"Rule 46(n) requires the place of supply ALONG WITH THE NAME OF THE STATE, and without "+
			"the name the renderer refuses the document",
			inv.PlaceOfSupply, inv.PlaceOfSupplyCode, docStateAwayName)
	}
	if inv.PlaceOfSupplyCode != docStateAway {
		t.Errorf("the place of supply code is %q, want %q: the code is what GSTR-1 reports the "+
			"inter-State supply under and it is printed NEXT TO the name, not replaced by it",
			inv.PlaceOfSupplyCode, docStateAway)
	}

	// AND IT PRINTS, with the name on the face of the document.
	text := invoiceText(t, inv)
	want := "Place of Supply: " + docStateAwayName + " (" + docStateAway + ")"
	if !strings.Contains(text, want) {
		t.Errorf("the rendered invoice does not carry %q. A GST officer reads the place of supply "+
			"off the face of the document, and an inter-State invoice that does not name the State "+
			"is defective.\nExtracted text:\n%s", want, text)
	}
	// The recipient's block names their State too, and the SUPPLIER's block names
	// ours — so a reader can tell the two States apart, which is the whole point of
	// an inter-State document.
	if !strings.Contains(text, "State: "+docStateAwayName+" ("+docStateAway+")") {
		t.Errorf("the recipient block does not name %s (%s):\n%s",
			docStateAwayName, docStateAway, text)
	}
	if !strings.Contains(text, docStateHomeName) {
		t.Errorf("the supplier's letterhead does not name the company's own State (%s), so the "+
			"document does not show the supply crossing a State line at all:\n%s",
			docStateHomeName, text)
	}
}

// TestNoCounterSaleOfFiftyThousandCanBeIssuedToAnUnregisteredCustomer is named for
// the second outage the same root cause caused.
//
// Rule 46(e) requires the recipient's address, the NAME OF THE STATE and its code
// on a supply of fifty thousand rupees or more to an unregistered person — there
// is no GSTIN on such a document to infer the State from. partyBlock left
// Party.State empty, so printing.ValidateRule46 refused every high-value counter
// sale, which for a shop is the largest sale of the day.
func TestNoCounterSaleOfFiftyThousandCanBeIssuedToAnUnregisteredCustomer(t *testing.T) {
	e := newDocEnv(t)
	// 500 bolts at 100.00 = 50,000.00 taxable, comfortably over the threshold once
	// tax is added, to an UNREGISTERED walk-in customer in the home State.
	res, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class:         ledger.Sales,
		PartyLedgerID: e.walkIn,
		ItemLedgerID:  e.sales,
		VoucherDate:   "2026-05-27",
		Party:         docNameWalkIn,
		CreatedBy:     "fixture",
		Inventory:     []ledger.InventoryLine{e.itemLine(e.bolt, "500", "100", "0")},
		GST: &ledger.GSTParams{
			SupplierStateCode:      docStateHome,
			PlaceOfSupplyStateCode: docStateHome,
			CGSTLedgerID:           e.ocgst,
			SGSTLedgerID:           e.osgst,
			IGSTLedgerID:           e.oigst,
		},
	})
	if err != nil {
		t.Fatalf("post the high-value counter sale: %v", err)
	}

	inv, err := e.store.LoadInvoice(context.Background(), e.company, res.VoucherID)
	if err != nil {
		t.Fatalf("a counter sale of fifty thousand rupees could not be assembled: %v", err)
	}
	if inv.Recipient.GSTIN != "" {
		t.Fatalf("the walk-in fixture carries the GSTIN %q, so this test is not exercising Rule "+
			"46(e) at all", inv.Recipient.GSTIN)
	}
	if inv.Total.LessThan(decimal.RequireFromString("50000")) {
		t.Fatalf("the fixture invoice totals %s, which is under the fifty thousand rupee threshold: "+
			"Rule 46(e) would not apply and this test would pass vacuously",
			inv.Total.StringFixed(2))
	}
	if inv.Recipient.State != docStateHomeName {
		t.Errorf("the recipient's State is named %q; ledger.state_code is %q, which is %s, and "+
			"Rule 46(e) requires the State BY NAME on a supply of fifty thousand rupees or more to "+
			"an unregistered person", inv.Recipient.State, docStateHome, docStateHomeName)
	}

	text := invoiceText(t, inv)
	want := "State: " + docStateHomeName + " (" + docStateHome + ")"
	if !strings.Contains(text, want) {
		t.Errorf("the rendered invoice does not state the recipient's State as %q: there is no "+
			"GSTIN on a B2C invoice to infer the State from, which is why the clause asks for "+
			"it.\nExtracted text:\n%s", want, text)
	}
}

// TestAPartyWhoseStateCodeWasRetiredIsBilledInAStateThatNoLongerExists is named
// for the breach a code-to-name map invites: filling the gap for codes it cannot
// stand behind.
//
// Code 25 was Daman and Diu until the Union territory merged into Dadra and Nagar
// Haveli and Daman and Diu (code 26) on 2020-01-26. A master still carrying 25
// must NOT print "Daman and Diu" — that is a place of supply that has not existed
// for six years — and must not print a blank either. It is refused, and the
// refusal names the code AND what to correct it to, because an operator told only
// that something is wrong has nothing to do next.
func TestAPartyWhoseStateCodeWasRetiredIsBilledInAStateThatNoLongerExists(t *testing.T) {
	e := newDocEnv(t)
	const name = "Diu Marine Supplies"
	id, err := masters.NewService(e.pool).CreateLedger(e.ctx, masters.Ledger{
		Name: name, GroupName: "Sundry Debtors",
		StateCode: docStateRetired, RegistrationType: masters.GSTUnregistered,
		Address: "Fort Road", City: "Diu", PIN: "362520",
	}, "fixture")
	if err != nil {
		t.Fatalf("create the ledger: %v", err)
	}
	res, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class:         ledger.Sales,
		PartyLedgerID: id,
		ItemLedgerID:  e.sales,
		VoucherDate:   "2026-05-28",
		Party:         name,
		CreatedBy:     "fixture",
		Inventory:     []ledger.InventoryLine{e.itemLine(e.bolt, "2", "300", "0")},
		GST: &ledger.GSTParams{
			SupplierStateCode:      docStateHome,
			PlaceOfSupplyStateCode: docStateHome,
			CGSTLedgerID:           e.ocgst,
			SGSTLedgerID:           e.osgst,
			IGSTLedgerID:           e.oigst,
		},
	})
	if err != nil {
		t.Fatalf("post the invoice: %v", err)
	}

	inv, err := e.store.LoadInvoice(context.Background(), e.company, res.VoucherID)
	if err == nil {
		t.Fatalf("an invoice was assembled naming the recipient's State as %q from the retired "+
			"code %s: the Union territory of Daman and Diu ceased to exist on 2020-01-26",
			inv.Recipient.State, docStateRetired)
	}
	var unknown *printingdb.UnknownStateCodeError
	if !errors.As(err, &unknown) {
		t.Fatalf("the retired state code surfaced as %v, not as a typed UnknownStateCodeError: a "+
			"caller cannot tell a stale master from a database fault", err)
	}
	if !errors.Is(err, printingdb.ErrUnknownStateCode) {
		t.Error("the refusal does not unwrap to ErrUnknownStateCode, so errors.Is cannot classify it")
	}
	// And it must classify as a missing particular, which is what the HTTP layer
	// maps to 422. Without it a stale master reaches the operator as a 500 — our
	// bug rather than their data.
	if !errors.Is(err, printingdb.ErrMissingParticular) {
		t.Error("the refusal does not unwrap to ErrMissingParticular, so the print route would " +
			"answer 500 for a master an operator can correct, instead of 422")
	}
	if unknown.Code != docStateRetired || !strings.Contains(err.Error(), docStateRetired) {
		t.Errorf("the refusal carries the code %q and reads %q; it must NAME the offending code "+
			"(%s) or nobody can find it", unknown.Code, err.Error(), docStateRetired)
	}
	if !strings.Contains(err.Error(), name) {
		t.Errorf("the refusal is %q; it must name the party master to open (%q) rather than a "+
			"struct field", err.Error(), name)
	}
	if !strings.Contains(err.Error(), "26") {
		t.Errorf("the refusal is %q; it must say what to correct the code TO (26, the merged Union "+
			"territory), or an operator has nothing to act on", err.Error())
	}
	if strings.Contains(inv.Recipient.State, "Daman") {
		t.Errorf("the refused call still filled the recipient's State with %q", inv.Recipient.State)
	}
}

// TestAPlaceOfSupplyThisSystemCannotNamePrintsAnywayIsRefused covers the other
// half of the same rule, on the voucher rather than the master.
//
// Code 96 is BELIEVED to mean "Other Country" — the place of supply on an export —
// and neither the code, the wording nor the date it came into use could be
// verified from this environment, so internal/gststate refuses to name it. The
// consequence has to be a refusal that names the code: printing an unverified name
// on the place-of-supply line of a tax invoice asserts something this codebase
// cannot stand behind, and printing the bare code where the statute asks for a
// name reads as correct to anybody who does not know the difference.
func TestAPlaceOfSupplyThisSystemCannotNamePrintsAnywayIsRefused(t *testing.T) {
	e := newDocEnv(t)
	const unnameable = "96"
	res, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class:         ledger.Sales,
		PartyLedgerID: e.away,
		ItemLedgerID:  e.sales,
		VoucherDate:   "2026-05-29",
		Party:         docNameAway,
		CreatedBy:     "fixture",
		Inventory:     []ledger.InventoryLine{e.itemLine(e.bolt, "3", "200", "0")},
		GST: &ledger.GSTParams{
			SupplierStateCode:      docStateHome,
			PlaceOfSupplyStateCode: unnameable,
			CGSTLedgerID:           e.ocgst,
			SGSTLedgerID:           e.osgst,
			IGSTLedgerID:           e.oigst,
		},
	})
	if err != nil {
		t.Fatalf("post a sale whose place of supply is code %s: %v", unnameable, err)
	}

	inv, err := e.store.LoadInvoice(context.Background(), e.company, res.VoucherID)
	if err == nil {
		t.Fatalf("a tax invoice was assembled with the place of supply named %q from the "+
			"unverified code %s", inv.PlaceOfSupply, unnameable)
	}
	var unknown *printingdb.UnknownStateCodeError
	if !errors.As(err, &unknown) {
		t.Fatalf("the unnameable place of supply surfaced as %v, not as a typed "+
			"UnknownStateCodeError", err)
	}
	if unknown.Code != unnameable || !strings.Contains(err.Error(), unnameable) {
		t.Errorf("the refusal carries code %q and reads %q; it must name the code %s",
			unknown.Code, err.Error(), unnameable)
	}
	if !strings.Contains(strings.ToLower(unknown.Field), "place of supply") {
		t.Errorf("the refusal names the particular %q; the fault is on the VOUCHER's place of "+
			"supply, not on the party master, and the two have different fixes", unknown.Field)
	}
	if !strings.Contains(unknown.Source, "place_of_supply") {
		t.Errorf("the refusal's source is %q; it must name voucher_version.place_of_supply",
			unknown.Source)
	}
	if inv.PlaceOfSupply != "" {
		t.Errorf("the refused call still named the place of supply %q", inv.PlaceOfSupply)
	}
}

// TestTheRoundOffBoundIsWhateverTheCodeHappensToSay is named for the review
// finding it closes: roundOffBound was ONE RUPEE by comment only. Raising it to
// 100 broke no test at all, so the product promise — "a residual is a rounding
// only if it lives in the paise" — was documented and unenforced, and a hundred
// rupees of unexplained value would have gone out on a tax invoice under the label
// "Round Off".
//
// The boundary is fixed from BOTH sides, which is the only way a bound can be
// pinned: exactly one rupee prints (a bill rounded up from 1,180.00 to 1,181.00 is
// ordinary arithmetic on paise, and refusing it would refuse ordinary books), and
// one paisa more is refused.
func TestTheRoundOffBoundIsWhateverTheCodeHappensToSay(t *testing.T) {
	e := newDocEnv(t)

	// Taxable 1,000.00 + tax 180.00 = 1,180.00. A party leg of 1,181.00 is a
	// residual of exactly 1.00: the inclusive edge.
	atTheBound := e.postAccountingInvoiceWithResidual(t, "2026-05-30", "1181", "1001")
	inv, err := e.store.LoadInvoice(context.Background(), e.company, atTheBound.VoucherID)
	if err != nil {
		t.Fatalf("an invoice rounded up by exactly one rupee was REFUSED: %v.\nA rounding reaches "+
			"a whole rupee (1,180.00 up to 1,181.00 is arithmetic on paise), so refusing at the "+
			"bound refuses ordinary books", err)
	}
	if !inv.RoundOff.Equal(decimal.RequireFromString("1")) {
		t.Fatalf("the fixture's residual is %s, not 1.00, so this test is not standing on the "+
			"boundary at all", inv.RoundOff.StringFixed(2))
	}

	// One paisa more is not a rounding. 1,181.01 against 1,180.00 of stated value
	// is a residual of 1.01.
	pastTheBound := e.postAccountingInvoiceWithResidual(t, "2026-05-31", "1181.01", "1001.01")
	inv, err = e.store.LoadInvoice(context.Background(), e.company, pastTheBound.VoucherID)
	if err == nil {
		t.Fatalf("a residual of %s printed under the label \"Round Off\": the bound is not one "+
			"rupee, so unexplained value of any size the bound happens to allow — freight keyed "+
			"as a party leg, a mis-keyed taxable value — goes out on the face of a tax invoice "+
			"under the one label a reader checks by glancing at it",
			inv.RoundOff.StringFixed(2))
	}
	var residual *printingdb.ResidualNotARoundOffError
	if !errors.As(err, &residual) {
		t.Fatalf("a residual of 1.01 was refused with %v, not a typed "+
			"ResidualNotARoundOffError", err)
	}
	if !residual.Residual.Equal(decimal.RequireFromString("1.01")) {
		t.Errorf("the refusal reports a residual of %s, want 1.01", residual.Residual.StringFixed(2))
	}
	// The bound the refusal STATES is the bound it applied, and it is one rupee. A
	// message quoting a different figure from the one enforced is worse than no
	// message: the operator adjusts the voucher to the figure they were told.
	if !residual.Bound.Equal(decimal.RequireFromString("1")) {
		t.Errorf("the refusal states a bound of %s; a round-off lives in the paise and cannot "+
			"exceed one rupee, so any other figure is licensing unexplained value on a tax invoice",
			residual.Bound.StringFixed(2))
	}
}
