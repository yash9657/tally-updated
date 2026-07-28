package ledger

// item_invoice_test.go — THE ITEM INVOICE against the LIVE database.
//
// These live in the INTERNAL test package (`ledger`, not `ledger_test`) because
// the tie they prove is a property of the post path itself, and two of them
// reach past the public API on purpose: one inserts a stock_movement with RAW
// SQL to prove migration 0027's trigger holds when Go is bypassed entirely, and
// the orientation test resolves posted ledger ids back to NAMES rather than
// counting sides.
//
// WHY THE ORIENTATION ASSERTIONS LOOK PARANOID
// --------------------------------------------
// This project shipped a fully REVERSED purchase once (WS8b). It passed every
// check that existed: the double-entry rule only asks that the two sides be
// equal, and deriveAndVerifyGST sums posting amounts WITHOUT looking at dr_cr,
// so a purchase posted sales-shaped balances AND verifies AND files. A test
// that counted "two debits and two credits", or that summed by side, would have
// passed too. Only resolving the ids back to names — "Local Purchases is on
// DEBIT, the supplier is on CREDIT" — can see it.

import (
	"context"
	"os"
	"strings"
	"testing"

	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/compliance"
	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/internal/reporting"
	"github.com/khata/cored/migrations"
)

const (
	itemInvAdminDSN = "postgres://postgres:postgres@localhost:5433/tally?sslmode=disable"
	itemInvAppDSN   = "postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable"
)

// stateHome / stateAway are the two GST state codes every fixture here uses.
// The company is registered in 27 (Maharashtra); a supply to 27 is INTRA-state
// (CGST+SGST) and a supply to 29 is INTER-state (IGST).
const (
	stateHome = "27"
	stateAway = "29"
)

// itemEnv is one isolated company with everything an ITEM INVOICE needs: a
// customer and a supplier, the revenue and expense ledgers the goods are booked
// to, the four tax heads, the seeded Main Location, and two stock items whose
// MASTERS carry different HSN codes and different GST rates (which is the whole
// point — the invoice may not state either).
type itemEnv struct {
	pool    *db.Pool
	ctx     context.Context
	company uuid.UUID
	led     *Service
	rep     *reporting.Service

	customer  uuid.UUID
	supplier  uuid.UUID
	sales     uuid.UUID
	purchases uuid.UUID
	ocgst     uuid.UUID
	osgst     uuid.UUID
	oigst     uuid.UUID
	icgst     uuid.UUID
	isgst     uuid.UUID

	godown uuid.UUID
	// bolt is HSN 7318 @ 18%, grease is HSN 3403 @ 12%. Two rates on one invoice
	// is what makes the per-line GST rows load-bearing: a single aggregate
	// cannot express a mixed-rate invoice's HSN summary.
	bolt   uuid.UUID
	grease uuid.UUID
}

// ledger names, spelled once so the orientation assertions and the fixtures can
// never drift apart.
const (
	nameCustomer  = "Acme Corp"
	nameSupplier  = "Bolt Traders"
	nameSales     = "Domestic Sales"
	namePurchases = "Local Purchases"
	nameOutCGST   = "Output CGST"
	nameOutSGST   = "Output SGST"
	nameOutIGST   = "Output IGST"
	nameInCGST    = "Input CGST"
	nameInSGST    = "Input SGST"
)

func setupItemInvoice(t *testing.T) *itemEnv {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	ctx := context.Background()

	adminConn, err := pgx.Connect(ctx, itemInvAdminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable, skipping integration test: %v", err)
	}
	if err := migrations.Apply(ctx, adminConn); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("apply migrations: %v", err)
	}
	_ = adminConn.Close(ctx)

	pool, err := db.New(ctx, itemInvAppDSN)
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
		Name:        "Item Invoice Co " + companyID.String()[:8],
		FYStartDate: "2025-04-01",
		StateCode:   stateHome,
	}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	// Teardown before the item/batch/godown masters below exist, so that a
	// t.Fatal while building the fixture still cleans up what was made. The
	// inventory masters (stock_item, stock_batch, godown, unit) are all
	// company-scoped and all invisible to a company-only delete.
	testdb.Cleanup(t, companyID)

	e := &itemEnv{
		pool: pool, ctx: cctx, company: companyID,
		led: NewService(pool), rep: reporting.NewService(pool),
	}
	mk := func(name, group string) uuid.UUID {
		id, err := ids.CreateLedger(cctx, name, group)
		if err != nil {
			t.Fatalf("create ledger %s: %v", name, err)
		}
		return id
	}
	e.customer = mk(nameCustomer, "Sundry Debtors")
	e.supplier = mk(nameSupplier, "Sundry Creditors")
	e.sales = mk(nameSales, "Sales Accounts")
	e.purchases = mk(namePurchases, "Purchase Accounts")
	e.ocgst = mk(nameOutCGST, "Duties & Taxes")
	e.osgst = mk(nameOutSGST, "Duties & Taxes")
	e.oigst = mk(nameOutIGST, "Duties & Taxes")
	e.icgst = mk(nameInCGST, "Duties & Taxes")
	e.isgst = mk(nameInSGST, "Duties & Taxes")

	if err := pool.RunInTx(cctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT id FROM godown WHERE company_id = $1 AND name = 'Main Location'`,
			companyID).Scan(&e.godown)
	}); err != nil {
		t.Fatalf("the company seed must create a 'Main Location' for stock to sit in: %v", err)
	}

	e.bolt = e.newItem(t, "Bolt", "7318", "18")
	e.grease = e.newItem(t, "Grease", "3403", "12")
	// Stock on hand so an OUTWARD sale is not refused by the negative-stock
	// trigger. Deliberately an OPENING and not a purchase: it must not put any
	// movements or GST lines into the versions these tests assert over.
	e.openWith(t, e.bolt, "1000", "10")
	e.openWith(t, e.grease, "1000", "10")
	return e
}

// newItem plants a stock item master directly, with the HSN and GST rate that
// the item invoice is required to read from HERE and never from the request.
// (masters.CreateStockItem still writes the columns 0025 renamed — INV-E's job —
// so these tests must not depend on it.)
func (e *itemEnv) newItem(t *testing.T, name, hsn, ratePct string) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new stock item id: %v", err)
	}
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO stock_item (id, company_id, name, hsn, gst_rate_pct)
			 VALUES ($1, $2, $3, $4, $5)`,
			id, e.company, name+"-"+id.String()[:8], hsn, decimal.RequireFromString(ratePct))
		return err
	}); err != nil {
		t.Fatalf("create stock item %s: %v", name, err)
	}
	return id
}

func (e *itemEnv) openWith(t *testing.T, item uuid.UUID, qty, rate string) {
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

// line builds one inventory line WITHOUT an amount: an item invoice derives the
// line total from qty/rate/discount, which is exactly what a grid that captures
// only those three columns sends.
func (e *itemEnv) line(item uuid.UUID, d Direction, qty, rate, discPct string) InventoryLine {
	return InventoryLine{
		StockItemID: item,
		GodownID:    e.godown,
		Direction:   d,
		Qty:         decimal.RequireFromString(qty),
		Rate:        decimal.RequireFromString(rate),
		DiscountPct: decimal.RequireFromString(discPct),
	}
}

// salesInvoice / purchaseInvoice build the ITEM-INVOICE request shape: NO
// postings and NO gst.lines — the party, the item ledger, the tax ledgers, the
// place of supply, and the goods. Everything else is generated.
func (e *itemEnv) salesInvoice(placeOfSupply, date string, lines ...InventoryLine) VoucherInput {
	return VoucherInput{
		Class:         Sales,
		PartyLedgerID: e.customer,
		ItemLedgerID:  e.sales,
		VoucherDate:   date,
		Party:         nameCustomer,
		CreatedBy:     "tester",
		Inventory:     lines,
		GST: &GSTParams{
			SupplierStateCode:      stateHome,
			PlaceOfSupplyStateCode: placeOfSupply,
			CGSTLedgerID:           e.ocgst,
			SGSTLedgerID:           e.osgst,
			IGSTLedgerID:           e.oigst,
		},
	}
}

func (e *itemEnv) purchaseInvoice(placeOfSupply, date string, lines ...InventoryLine) VoucherInput {
	return VoucherInput{
		Class:         Purchase,
		PartyLedgerID: e.supplier,
		ItemLedgerID:  e.purchases,
		VoucherDate:   date,
		Party:         nameSupplier,
		CreatedBy:     "tester",
		Inventory:     lines,
		GST: &GSTParams{
			SupplierStateCode:      stateHome,
			PlaceOfSupplyStateCode: placeOfSupply,
			CGSTLedgerID:           e.icgst,
			SGSTLedgerID:           e.isgst,
		},
	}
}

// --- assertions -------------------------------------------------------------

// postedLeg is one posting resolved back to the NAME of the ledger it hit. The
// name is the load-bearing field: an id-only or side-count assertion cannot see
// a reversed voucher (see the file header).
type postedLeg struct {
	drcr   string
	amount decimal.Decimal
}

func side(drcr string) string {
	if drcr == string(Debit) {
		return "DEBIT"
	}
	return "CREDIT"
}

func (e *itemEnv) legsOf(t *testing.T, versionID uuid.UUID) map[string]postedLeg {
	t.Helper()
	out := map[string]postedLeg{}
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT l.name, p.dr_cr::text, p.amount
			   FROM posting p
			   JOIN ledger l ON l.id = p.ledger_id
			  WHERE p.company_id = $1 AND p.voucher_version_id = $2`,
			e.company, versionID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				name string
				leg  postedLeg
			)
			if err := rows.Scan(&name, &leg.drcr, &leg.amount); err != nil {
				return err
			}
			out[name] = leg
		}
		return rows.Err()
	}); err != nil {
		t.Fatalf("read posted legs: %v", err)
	}
	return out
}

// assertLegs compares the posted legs to the expected {ledger name -> side,
// amount} map. Failure messages name the LEDGER and the SIDE in the accountant's
// vocabulary, because "want D got C on 1180" is not a sentence anybody can act
// on.
func (e *itemEnv) assertLegs(t *testing.T, what string, versionID uuid.UUID, want map[string]postedLeg) {
	t.Helper()
	got := e.legsOf(t, versionID)
	for name, w := range want {
		g, ok := got[name]
		if !ok {
			t.Fatalf("%s: nothing was posted to %q at all; the voucher hit %v", what, name, namesOf(got))
			continue
		}
		if g.drcr != w.drcr {
			t.Errorf("%s: %q landed on %s, want %s — the voucher is posted the wrong way round, "+
				"which still balances and still passes GST verification (the WS8b reversed-purchase bug)",
				what, name, side(g.drcr), side(w.drcr))
		}
		if !g.amount.Equal(w.amount) {
			t.Errorf("%s: %q posted %s, want %s", what, name, g.amount.StringFixed(4), w.amount.StringFixed(4))
		}
	}
	if len(got) != len(want) {
		t.Errorf("%s: the voucher posted %d legs (%v), want exactly %d (%v)",
			what, len(got), namesOf(got), len(want), namesOf(want))
	}
}

func namesOf(m map[string]postedLeg) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+" "+side(v.drcr)+" "+v.amount.StringFixed(4))
	}
	return out
}

// gstRow is one persisted voucher_gst_line.
type gstRow struct {
	lineNo  int
	hsn     string
	rate    decimal.Decimal
	taxable decimal.Decimal
}

func (e *itemEnv) gstRowsOf(t *testing.T, versionID uuid.UUID) []gstRow {
	t.Helper()
	var out []gstRow
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT line_no, hsn, gst_rate_pct, taxable_value
			   FROM voucher_gst_line
			  WHERE company_id = $1 AND voucher_version_id = $2
			  ORDER BY line_no`,
			e.company, versionID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r gstRow
			if err := rows.Scan(&r.lineNo, &r.hsn, &r.rate, &r.taxable); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	}); err != nil {
		t.Fatalf("read gst lines: %v", err)
	}
	return out
}

func (e *itemEnv) itemName(t *testing.T, id uuid.UUID) string {
	t.Helper()
	var name string
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT name FROM stock_item WHERE company_id = $1 AND id = $2`,
			e.company, id).Scan(&name)
	}); err != nil {
		t.Fatalf("read stock item name: %v", err)
	}
	return name
}

func (e *itemEnv) voucherCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM voucher_head WHERE company_id = $1`, e.company).Scan(&n)
	}); err != nil {
		t.Fatalf("count vouchers: %v", err)
	}
	return n
}

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// ---------------------------------------------------------------------------

// TestSendingBothInventoryAndGSTLinesIsRefused is the refusal the whole slice
// rests on. Before it, deriveAndVerifyGST verified only the three tax amounts
// and never the base they came from, so a client could declare a taxable value
// of Rs 1 while shipping 100 units: the derived tax matched the posted tax
// (both computed from the same wrong 1), the voucher balanced, and NOTHING
// noticed that the warehouse and the ledger now disagreed forever.
//
// Merging the two inputs cannot fix that — whichever one we believed, the other
// would be silently discarded. So it is an error.
func TestSendingBothInventoryAndGSTLinesIsRefused(t *testing.T) {
	e := setupItemInvoice(t)

	// 100 units at Rs 100 = Rs 10,000 of goods, declared as a Rs 1 invoice.
	in := e.salesInvoice(stateHome, "2025-05-10",
		e.line(e.bolt, Outward, "100", "100", "0"))
	in.GST.Lines = []compliance.Line{
		{TaxableValue: dec("1"), GSTRatePct: dec("18"), HSN: "9999"},
	}

	_, err := e.led.PostVoucher(e.ctx, in)
	if err == nil {
		t.Fatal("a voucher that ships 100 units of stock while declaring a Rs 1 taxable base was ACCEPTED; " +
			"the books and the stock now disagree with nothing in the log to show it")
	}
	if !errors.Is(err, ErrInventoryAndGSTLines) {
		t.Fatalf("refusal was %v, want ErrInventoryAndGSTLines so the HTTP layer can 422 it with a message "+
			"that tells the client to send one or the other", err)
	}
	if n := e.voucherCount(t); n != 0 {
		t.Fatalf("the refused invoice left %d voucher(s) behind; a rejected post must write nothing", n)
	}
}

// TestGSTLinesAreGeneratedFromStockLines pins the derivation direction: the
// per-line GST rows come from the stock lines and their MASTERS, one row per
// inventory line, at whatever rate the item master says.
//
// HSN and rate are the tax IDENTITY of the goods and belong to the item, not to
// the document that mentions it. If the request could state them, two invoices
// for the same bolt could file under two HSN codes at two rates in one GSTR-1 —
// with the books, the stock and the arithmetic all internally consistent, so
// only a human reading table 12 would ever see it.
func TestGSTLinesAreGeneratedFromStockLines(t *testing.T) {
	e := setupItemInvoice(t)

	// Three lines, two rates, and the same item twice: 18% bolts, 12% grease,
	// 18% bolts again. Repeating an item proves the rows are NOT rolled up —
	// the HSN summary is a REPORT-time rollup, not a storage decision.
	in := e.salesInvoice(stateHome, "2025-05-10",
		e.line(e.bolt, Outward, "10", "100", "0"),  // 1000.0000 @ 18
		e.line(e.grease, Outward, "5", "200", "0"), // 1000.0000 @ 12
		e.line(e.bolt, Outward, "2", "50", "0"),    // 100.0000  @ 18
	)
	res, err := e.led.PostVoucher(e.ctx, in)
	if err != nil {
		t.Fatalf("post item invoice: %v", err)
	}

	want := []gstRow{
		{lineNo: 1, hsn: "7318", rate: dec("18"), taxable: dec("1000")},
		{lineNo: 2, hsn: "3403", rate: dec("12"), taxable: dec("1000")},
		{lineNo: 3, hsn: "7318", rate: dec("18"), taxable: dec("100")},
	}
	got := e.gstRowsOf(t, res.VoucherVersionID)
	if len(got) != len(want) {
		t.Fatalf("a %d-line item invoice produced %d voucher_gst_line rows, want %d — "+
			"the HSN summary is keyed per (hsn, rate), so a missing or merged row misstates GSTR-1 table 12",
			len(want), len(got), len(want))
	}
	for i, w := range want {
		g := got[i]
		if g.lineNo != w.lineNo {
			t.Errorf("gst row %d has line_no %d, want %d", i+1, g.lineNo, w.lineNo)
		}
		if g.hsn != w.hsn {
			t.Errorf("gst row %d carries HSN %q, want %q from the stock item MASTER — "+
				"an HSN the invoice chose rather than the item owns files the same goods under two codes",
				i+1, g.hsn, w.hsn)
		}
		if !g.rate.Equal(w.rate) {
			t.Errorf("gst row %d carries rate %s%%, want %s%% from the stock item MASTER — "+
				"a rate the invoice chose is a rate the tenant can under-declare at will",
				i+1, g.rate.StringFixed(2), w.rate.StringFixed(2))
		}
		if !g.taxable.Equal(w.taxable) {
			t.Errorf("gst row %d taxable value %s, want %s (qty x rate less discount)",
				i+1, g.taxable.StringFixed(4), w.taxable.StringFixed(4))
		}
	}

	// And the tax actually derived from those rows: 18% of 1100 split half/half
	// (99 + 99) plus 12% of 1000 split half/half (60 + 60).
	e.assertLegs(t, "3-line mixed-rate sale", res.VoucherVersionID, map[string]postedLeg{
		nameCustomer: {drcr: string(Debit), amount: dec("2418")},  // 2100 + 318
		nameSales:    {drcr: string(Credit), amount: dec("2100")}, // 1000 + 1000 + 100
		nameOutCGST:  {drcr: string(Credit), amount: dec("159")},  // 99 + 60
		nameOutSGST:  {drcr: string(Credit), amount: dec("159")},
	})
}

// TestMovementValueTiesToTaxableValueAtCommit proves migration 0027's trigger
// with a RAW SQL INSERT that never touches Go: the movement ledger and the taxed
// base of a voucher version must agree at COMMIT even when the application layer
// is bypassed entirely. That is the difference between an invariant and a
// convention — the Go generator can only protect the paths it is on.
//
// It also pins DEFERRAL, in the passing half: post() writes the GST lines first
// and the movements one at a time, so a 3-line invoice is INCONSISTENT after
// each of the first two movement rows. A non-deferred trigger would reject every
// multi-line invoice while cheerfully accepting single-line ones.
func TestMovementValueTiesToTaxableValueAtCommit(t *testing.T) {
	e := setupItemInvoice(t)

	// A perfectly legitimate multi-line invoice must commit. If this fails, the
	// constraint is firing per-statement instead of at commit.
	in := e.purchaseInvoice(stateHome, "2025-05-10",
		e.line(e.bolt, Inward, "10", "100", "0"),
		e.line(e.grease, Inward, "5", "200", "0"),
		e.line(e.bolt, Inward, "2", "50", "0"),
	)
	res, err := e.led.PostVoucher(e.ctx, in)
	if err != nil {
		t.Fatalf("a legitimate 3-line item invoice was refused: %v — "+
			"the tie must be checked at COMMIT, because a multi-line invoice is inconsistent "+
			"after every movement row but the last", err)
	}

	// Now bypass Go: append a fourth movement to the SAME version by hand. The
	// version's GST lines still total 2100 while its movements now total 2200.
	var insertErr error
	err = e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		_, insertErr = tx.Exec(ctx,
			`INSERT INTO stock_movement
			   (id, company_id, voucher_version_id, line_no, stock_item_id, godown_id,
			    direction, qty, rate, discount_pct, amount)
			 VALUES ($1, $2, $3, 99, $4, $5, 'IN', 1, 100, 0, 100)`,
			id, e.company, res.VoucherVersionID, e.bolt, e.godown)
		return insertErr
	})
	if insertErr != nil {
		t.Fatalf("the raw INSERT itself failed (%v) — the tie must be DEFERRED, "+
			"so this must survive the statement and die at commit", insertErr)
	}
	if err == nil {
		t.Fatal("raw SQL smuggled 100 rupees of goods onto an invoice that is taxed on 2100 and it COMMITTED; " +
			"migration 0027's tie is the only thing standing between the books and the stock " +
			"on any path that is not ledger.post()")
	}
	if !strings.Contains(err.Error(), "item invoice tie broken") {
		t.Fatalf("commit failed with %v, want the item-invoice tie violation", err)
	}
}

// TestPartyLegEqualsStockValuePlusTax: what the party owes is the value of the
// goods plus the tax charged on them — intra-state (CGST+SGST) and inter-state
// (IGST), with and without a line discount.
//
// The DISCOUNTED case is the one that matters. A generator that derived the
// party leg from SUM(qty x rate) and forgot the discount would still balance
// (the goods leg would be equally wrong), would still pass GST verification (the
// tax legs are derived per line and unaffected), and would still satisfy
// migration 0027's tie (both sides of it carry the discounted value). It would
// simply overcharge every discounted customer, forever, silently.
func TestPartyLegEqualsStockValuePlusTax(t *testing.T) {
	cases := []struct {
		what          string
		placeOfSupply string
		line          func(e *itemEnv) InventoryLine
		want          func(e *itemEnv) map[string]postedLeg
	}{
		{
			what:          "intra-state, no discount: 10 x 100 @ 18% = 1000 + 90 + 90",
			placeOfSupply: stateHome,
			line:          func(e *itemEnv) InventoryLine { return e.line(e.bolt, Outward, "10", "100", "0") },
			want: func(e *itemEnv) map[string]postedLeg {
				return map[string]postedLeg{
					nameCustomer: {drcr: string(Debit), amount: dec("1180")},
					nameSales:    {drcr: string(Credit), amount: dec("1000")},
					nameOutCGST:  {drcr: string(Credit), amount: dec("90")},
					nameOutSGST:  {drcr: string(Credit), amount: dec("90")},
				}
			},
		},
		{
			what:          "inter-state, no discount: the same 1000 carries IGST 180 and no CGST/SGST at all",
			placeOfSupply: stateAway,
			line:          func(e *itemEnv) InventoryLine { return e.line(e.bolt, Outward, "10", "100", "0") },
			want: func(e *itemEnv) map[string]postedLeg {
				return map[string]postedLeg{
					nameCustomer: {drcr: string(Debit), amount: dec("1180")},
					nameSales:    {drcr: string(Credit), amount: dec("1000")},
					nameOutIGST:  {drcr: string(Credit), amount: dec("180")},
				}
			},
		},
		{
			what:          "intra-state WITH a 10% line discount: the taxable base is 900, not 1000",
			placeOfSupply: stateHome,
			line:          func(e *itemEnv) InventoryLine { return e.line(e.bolt, Outward, "10", "100", "10") },
			want: func(e *itemEnv) map[string]postedLeg {
				return map[string]postedLeg{
					nameCustomer: {drcr: string(Debit), amount: dec("1062")},
					nameSales:    {drcr: string(Credit), amount: dec("900")},
					nameOutCGST:  {drcr: string(Credit), amount: dec("81")},
					nameOutSGST:  {drcr: string(Credit), amount: dec("81")},
				}
			},
		},
		{
			what:          "inter-state WITH a 10% line discount: IGST 162 on 900",
			placeOfSupply: stateAway,
			line:          func(e *itemEnv) InventoryLine { return e.line(e.bolt, Outward, "10", "100", "10") },
			want: func(e *itemEnv) map[string]postedLeg {
				return map[string]postedLeg{
					nameCustomer: {drcr: string(Debit), amount: dec("1062")},
					nameSales:    {drcr: string(Credit), amount: dec("900")},
					nameOutIGST:  {drcr: string(Credit), amount: dec("162")},
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.what, func(t *testing.T) {
			e := setupItemInvoice(t)
			res, err := e.led.PostVoucher(e.ctx,
				e.salesInvoice(tc.placeOfSupply, "2025-05-10", tc.line(e)))
			if err != nil {
				t.Fatalf("post item invoice: %v", err)
			}
			e.assertLegs(t, tc.what, res.VoucherVersionID, tc.want(e))

			// The movement's stored amount is the SAME number as the taxed base.
			// That equality is what migration 0027 enforces; asserting it here
			// says which number it is.
			gst := e.gstRowsOf(t, res.VoucherVersionID)
			if len(gst) != 1 {
				t.Fatalf("want exactly 1 gst line, got %d", len(gst))
			}
			var moves []InventoryLineView
			if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
				var err error
				moves, err = loadInventoryLines(ctx, tx, e.company, res.VoucherVersionID)
				return err
			}); err != nil {
				t.Fatalf("read movements: %v", err)
			}
			if len(moves) != 1 {
				t.Fatalf("want exactly 1 movement, got %d", len(moves))
			}
			if !moves[0].Amount.Equal(gst[0].taxable) {
				t.Errorf("the movement is worth %s but the invoice is taxed on %s — "+
					"the goods and the tax on them must be the same number",
					moves[0].Amount.StringFixed(4), gst[0].taxable.StringFixed(4))
			}
		})
	}
}

// TestPurchaseItemInvoiceIsNotSalesShaped. A purchase is the MIRROR of a sale,
// not a copy of it: the supplier is CREDITED (we owe them) and the purchases
// ledger is DEBITED (the expense), with the input-tax heads on DEBIT (the
// government owes us).
//
// THIS TEST EXISTS BECAUSE THE REVERSED PURCHASE ALREADY SHIPPED (WS8b). A
// sales-shaped purchase:
//   - BALANCES (debits still equal credits),
//   - PASSES GST verification (deriveAndVerifyGST sums posting amounts without
//     ever looking at dr_cr),
//   - PASSES migration 0027's tie (the goods and the taxed base are untouched),
//   - and turns the company's largest liability into its largest asset.
//
// Nothing except resolving the posted ledger ids back to NAMES can see it. A
// test that counted two debits and two credits would pass on the reversed
// voucher; so would one that summed by side.
func TestPurchaseItemInvoiceIsNotSalesShaped(t *testing.T) {
	e := setupItemInvoice(t)

	res, err := e.led.PostVoucher(e.ctx,
		e.purchaseInvoice(stateHome, "2025-05-10",
			e.line(e.bolt, Inward, "10", "100", "0")))
	if err != nil {
		t.Fatalf("post purchase item invoice: %v", err)
	}

	e.assertLegs(t, "purchase item invoice", res.VoucherVersionID, map[string]postedLeg{
		// We owe the supplier the goods plus the tax they charged.
		nameSupplier: {drcr: string(Credit), amount: dec("1180")},
		// The goods are an expense.
		namePurchases: {drcr: string(Debit), amount: dec("1000")},
		// Input tax is recoverable — an asset, on DEBIT.
		nameInCGST: {drcr: string(Debit), amount: dec("90")},
		nameInSGST: {drcr: string(Debit), amount: dec("90")},
	})

	// The controls that the reversed voucher would ALSO have passed, asserted
	// explicitly so a future reader can see they are not what caught it.
	legs := e.legsOf(t, res.VoucherVersionID)
	debit, credit := decimal.Zero, decimal.Zero
	for _, l := range legs {
		if l.drcr == string(Debit) {
			debit = debit.Add(l.amount)
		} else {
			credit = credit.Add(l.amount)
		}
	}
	if !debit.Equal(credit) {
		t.Fatalf("purchase does not balance: debit %s credit %s", debit.StringFixed(4), credit.StringFixed(4))
	}
	if !debit.Equal(dec("1180")) {
		t.Fatalf("purchase totals %s per side, want 1180", debit.StringFixed(4))
	}
}

// TestItemInvoiceAlterRevaluesTheStockSummary is the payoff of "stock value is
// never stored": altering an APRIL invoice's quantity changes the JULY closing
// value, and there is no reversal code anywhere to make that happen. The alter
// appends a version, the head pointer moves, and the report replays what the
// head now says.
//
// A materialised stock value could not do this. It could only be stale.
func TestItemInvoiceAlterRevaluesTheStockSummary(t *testing.T) {
	e := setupItemInvoice(t)

	// A dedicated item with NO opening, so the numbers below are entirely this
	// test's doing.
	widget := e.newItem(t, "Widget", "8482", "18")
	name := e.itemName(t, widget)

	// April: buy 100 @ 10 = 1000.
	april, err := e.led.PostVoucher(e.ctx,
		e.purchaseInvoice(stateHome, "2025-04-05",
			e.line(widget, Inward, "100", "10", "0")))
	if err != nil {
		t.Fatalf("post april purchase: %v", err)
	}

	// June: sell 40 @ 25. The COST consumed is 40 x the weighted-average 10.
	if _, err := e.led.PostVoucher(e.ctx,
		e.salesInvoice(stateHome, "2025-06-10",
			e.line(widget, Outward, "40", "25", "0"))); err != nil {
		t.Fatalf("post june sale: %v", err)
	}

	closing := func(what string) (decimal.Decimal, decimal.Decimal) {
		t.Helper()
		ss, err := e.rep.StockSummary(e.ctx, e.company, "2025-07-31")
		if err != nil {
			t.Fatalf("stock summary (%s): %v", what, err)
		}
		for _, it := range ss.Items {
			if it.StockItemID == widget {
				return it.ClosingQty, it.ClosingValue
			}
		}
		t.Fatalf("%s: %q is absent from the July stock summary entirely", what, name)
		return decimal.Zero, decimal.Zero
	}

	qty, value := closing("before the alter")
	if !qty.Equal(dec("60")) || !value.Equal(dec("600")) {
		t.Fatalf("July closing for %s is %s units worth %s, want 60 units worth 600 "+
			"(100 in at 10, 40 out at the weighted-average 10)",
			name, qty.StringFixed(4), value.StringFixed(4))
	}

	// ALTER the APRIL invoice: 100 units became 200. Nothing about June changes,
	// and nothing reverses anything.
	altered, err := e.led.AlterVoucher(e.ctx, april.VoucherID,
		e.purchaseInvoice(stateHome, "2025-04-05",
			e.line(widget, Inward, "200", "10", "0")))
	if err != nil {
		t.Fatalf("alter april purchase: %v", err)
	}

	qty, value = closing("after the alter")
	if !qty.Equal(dec("160")) || !value.Equal(dec("1600")) {
		t.Fatalf("after altering the APRIL invoice to 200 units, the JULY closing for %s is "+
			"%s units worth %s, want 160 units worth 1600 — a back-dated edit must re-value "+
			"every later period, which is exactly what a stored stock value cannot do",
			name, qty.StringFixed(4), value.StringFixed(4))
	}

	// And the accounting moved with it, from the SAME restated stock lines: the
	// April purchase now books 2000 of goods and 180+180 of input tax against
	// the supplier's 2360. The alter regenerated the postings; the client never
	// sent any.
	if altered.VersionNo != 2 {
		t.Fatalf("altered invoice is at version %d, want 2", altered.VersionNo)
	}
	e.assertLegs(t, "altered april purchase", altered.VoucherVersionID, map[string]postedLeg{
		nameSupplier:  {drcr: string(Credit), amount: dec("2360")},
		namePurchases: {drcr: string(Debit), amount: dec("2000")},
		nameInCGST:    {drcr: string(Debit), amount: dec("180")},
		nameInSGST:    {drcr: string(Debit), amount: dec("180")},
	})
}
