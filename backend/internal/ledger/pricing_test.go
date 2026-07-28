package ledger

// pricing_test.go — PRICE LISTS IN THE VOUCHER PATH, and the single-sourced
// closing-position query, against the LIVE database.
//
// Internal test package (`ledger`, not `ledger_test`) for two reasons that are
// not convenience: closingPosition is unexported and the whole point of the
// hoist is that ONE unexported function is what both callers use, and
// resolvePrice is unexported and has to be compared field-for-field against
// masters.ResolveRate, which is the duplication this slice knowingly took on.
//
// EVERY ASSERTION HERE IS ABOUT A NUMBER THAT REACHED THE DATABASE, not about a
// return value. A rate that resolves correctly in Go and is written to
// stock_movement as something else is the failure this feature would actually
// have; reading the movement row back is the only way to see it.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/masters"
)

// --- price-list fixtures ----------------------------------------------------

// plSlab is one quantity slab of a price-list fixture. QtyTo == "" is the top
// slab ("and everything above"), spelt as an explicit empty string rather than
// as a zero because zero is a legitimate lower bound two fields away.
type plSlab struct {
	item     uuid.UUID
	qtyFrom  string
	qtyTo    string // "" = unbounded
	rate     string
	discount string
}

// newPriceList plants a price list and its entries with raw SQL, for the same
// reason newItem does: these tests must fail when the VOUCHER path is wrong, not
// when a master-creation helper is. partyGroup == uuid.Nil is the company-wide
// list.
//
// The bounds are '[)' — lower inclusive, upper exclusive — which is what
// migration 0030's price_list_entry_qty_half_open enforces and what makes a
// quantity exactly on a slab boundary land in exactly one slab.
func (e *itemEnv) newPriceList(t *testing.T, name string, partyGroup uuid.UUID, from, to string, slabs ...plSlab) uuid.UUID {
	t.Helper()
	listID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new price list id: %v", err)
	}
	var group any
	if partyGroup != uuid.Nil {
		group = partyGroup
	}
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO price_list (id, company_id, name, party_group_id, effective)
			 VALUES ($1, $2, $3, $4, daterange($5::date, NULLIF($6,'')::date, '[)'))`,
			listID, e.company, name+"-"+listID.String()[:8], group, from, to); err != nil {
			return err
		}
		for _, s := range slabs {
			entryID, err := uuid.NewV7()
			if err != nil {
				return err
			}
			var upper any
			if s.qtyTo != "" {
				upper = decimal.RequireFromString(s.qtyTo)
			}
			disc := "0"
			if s.discount != "" {
				disc = s.discount
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO price_list_entry
				   (id, company_id, price_list_id, stock_item_id, qty_range, rate, discount_pct)
				 VALUES ($1, $2, $3, $4, numrange($5::numeric, $6::numeric, '[)'), $7, $8)`,
				entryID, e.company, listID, s.item,
				decimal.RequireFromString(s.qtyFrom), upper,
				decimal.RequireFromString(s.rate), decimal.RequireFromString(disc)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("plant price list %s: %v", name, err)
	}
	return listID
}

// unpricedLine is a line that says NOTHING about price — "price it for me".
func (e *itemEnv) unpricedLine(item uuid.UUID, d Direction, qty string) InventoryLine {
	return InventoryLine{
		StockItemID:  item,
		GodownID:     e.godown,
		Direction:    d,
		Qty:          decimal.RequireFromString(qty),
		RateUnstated: true,
	}
}

// movementRows reads the movement rows of one voucher VERSION in keyed order.
// Reading the ROW and not the input is the point: this is what the warehouse,
// the valuer and every stock report will see.
func (e *itemEnv) movementRows(t *testing.T, versionID uuid.UUID) []InventoryLineView {
	t.Helper()
	var out []InventoryLineView
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = loadInventoryLines(ctx, tx, e.company, versionID)
		return err
	}); err != nil {
		t.Fatalf("read movements of version %s: %v", versionID, err)
	}
	return out
}

// groupOf resolves the account group a ledger sits under — the scope a
// party-narrowed price list is matched on.
func (e *itemEnv) groupOf(t *testing.T, ledgerID uuid.UUID) uuid.UUID {
	t.Helper()
	var g uuid.UUID
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT group_id FROM ledger WHERE company_id = $1 AND id = $2`,
			e.company, ledgerID).Scan(&g)
	}); err != nil {
		t.Fatalf("read party group: %v", err)
	}
	return g
}

// ===========================================================================
// TASK 1 — the closing-position query has ONE Go spelling, and it agrees with
// the trigger.
// ===========================================================================

// TestTheGoBookFigureAndTheTriggerAgree pins the hoisted closingPosition
// against migration 0025's check_stock_not_negative body, over a position whose
// voucher has been ALTERED.
//
// THE ALTER IS THE WHOLE FIXTURE. Movements are versioned and the superseded
// version's rows stay in stock_movement forever, so a book figure that does not
// join through voucher_head.current_version_id counts the goods TWICE — once as
// they were first keyed and once as they were corrected to. That is invisible on
// a voucher nobody has edited, which is why seeding "IN 10, altered to IN 4" and
// not simply "IN 4" is what makes this test able to fail.
//
// The trigger's expression is spelt out here VERBATIM from migration 0025 rather
// than referenced, because a shared spelling would agree with itself by
// construction and prove nothing. Two independent texts that must produce the
// same number is the only shape that can catch a drift.
func TestTheGoBookFigureAndTheTriggerAgree(t *testing.T) {
	e := setupItemInvoice(t)

	// IN 10 of bolt, then corrected to IN 4. Opening stock is 1000 (setup), so
	// the book must read 1004 — never 1014.
	in := e.purchaseInvoice(stateHome, "2025-05-10", e.line(e.bolt, Inward, "10", "50", "0"))
	res, err := e.led.PostVoucher(e.ctx, in)
	if err != nil {
		t.Fatalf("post purchase: %v", err)
	}
	alt := e.purchaseInvoice(stateHome, "2025-05-10", e.line(e.bolt, Inward, "4", "50", "0"))
	if _, err := e.led.AlterVoucher(e.ctx, res.VoucherID, alt); err != nil {
		t.Fatalf("alter purchase to 4: %v", err)
	}

	p := stockPosition{item: e.bolt, godown: e.godown}

	var goFigure, triggerFigure decimal.Decimal
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		// uuid.Nil excludes nothing — which is exactly what the deferred trigger
		// sees when it fires at COMMIT, after voucher_head's pointer has moved.
		goFigure, err = closingPosition(ctx, tx, e.company, p, uuid.Nil)
		if err != nil {
			return err
		}
		// check_stock_not_negative's body, migration 0025 lines 367-385.
		return tx.QueryRow(ctx,
			`SELECT COALESCE((SELECT SUM(so.qty)
			                    FROM stock_opening so
			                   WHERE so.company_id    = $1
			                     AND so.stock_item_id = $2
			                     AND so.godown_id IS NOT DISTINCT FROM $3
			                     AND so.batch_id  IS NOT DISTINCT FROM $4), 0)
			      + COALESCE((SELECT SUM(CASE WHEN sm.direction = 'IN' THEN sm.qty
			                                  ELSE -sm.qty END)
			                    FROM stock_movement sm
			                    JOIN voucher_head vh
			                      ON vh.company_id         = sm.company_id
			                     AND vh.current_version_id = sm.voucher_version_id
			                   WHERE sm.company_id    = $1
			                     AND sm.stock_item_id = $2
			                     AND sm.godown_id IS NOT DISTINCT FROM $3
			                     AND sm.batch_id  IS NOT DISTINCT FROM $4
			                     AND vh.is_deleted = false), 0)`,
			e.company, p.item, p.godown, nil).Scan(&triggerFigure)
	}); err != nil {
		t.Fatalf("read the two book figures: %v", err)
	}

	if !goFigure.Equal(triggerFigure) {
		t.Fatalf("the Go book figure and the negative-stock trigger disagree: Go says %s, the trigger's own expression says %s — "+
			"a Physical Stock voucher would difference its count against one book and be judged against the other",
			goFigure.StringFixed(4), triggerFigure.StringFixed(4))
	}
	if want := dec("1004"); !goFigure.Equal(want) {
		t.Fatalf("book figure is %s, want %s (opening 1000 + the CURRENT version's 4); "+
			"%s would mean the superseded version's 10 was counted as well",
			goFigure.StringFixed(4), want.StringFixed(4), dec("1014").StringFixed(4))
	}
}

// TestTheOwnVoucherExclusionIsAParameterAndNotAConstant proves the hoisted
// query's one parameter actually changes the answer — that "exclude this
// voucher" and "exclude nothing" are genuinely different reads of the same
// position, which is what let the two former copies drift.
func TestTheOwnVoucherExclusionIsAParameterAndNotAConstant(t *testing.T) {
	e := setupItemInvoice(t)

	res, err := e.led.PostVoucher(e.ctx,
		e.purchaseInvoice(stateHome, "2025-05-10", e.line(e.bolt, Inward, "7", "50", "0")))
	if err != nil {
		t.Fatalf("post purchase: %v", err)
	}
	p := stockPosition{item: e.bolt, godown: e.godown}

	var withIt, withoutIt decimal.Decimal
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if withIt, err = closingPosition(ctx, tx, e.company, p, uuid.Nil); err != nil {
			return err
		}
		withoutIt, err = closingPosition(ctx, tx, e.company, p, res.VoucherID)
		return err
	}); err != nil {
		t.Fatalf("read book figures: %v", err)
	}

	if !withIt.Equal(dec("1007")) {
		t.Fatalf("excluding nothing gives %s, want 1007 (opening 1000 + this voucher's 7)", withIt.StringFixed(4))
	}
	if !withoutIt.Equal(dec("1000")) {
		t.Fatalf("excluding the voucher gives %s, want 1000 (the opening alone) — the exclusion parameter is not reaching the query, "+
			"so an ALTER would be judged against a book that still contains the version it is replacing",
			withoutIt.StringFixed(4))
	}
}

// ===========================================================================
// TASK 2 — price lists in the voucher path.
// ===========================================================================

// TestOmittedRateIsResolvedFromThePriceList is the feature: a line that states
// no rate is priced from the list that is in force, AT THE SLAB ITS OWN
// QUANTITY FALLS IN.
//
// The list is slabbed 100.00 below a hundred units and 90.00 at a hundred and
// above, and the invoice is for 150. Any lookup that asks on a quantity other
// than the line's own — zero, one, a total across lines — returns 100.00, which
// is a rate the customer was never offered and a 10% overcharge that reconciles
// perfectly against itself.
func TestOmittedRateIsResolvedFromThePriceList(t *testing.T) {
	e := setupItemInvoice(t)
	e.newPriceList(t, "Trade", uuid.Nil, "2025-04-01", "",
		plSlab{item: e.bolt, qtyFrom: "0", qtyTo: "100", rate: "100"},
		plSlab{item: e.bolt, qtyFrom: "100", qtyTo: "", rate: "90"},
	)

	res, err := e.led.PostVoucher(e.ctx,
		e.salesInvoice(stateHome, "2025-05-10", e.unpricedLine(e.bolt, Outward, "150")))
	if err != nil {
		t.Fatalf("post an invoice with an unpriced line: %v", err)
	}

	rows := e.movementRows(t, res.VoucherVersionID)
	if len(rows) != 1 {
		t.Fatalf("want 1 movement, got %d", len(rows))
	}
	if want := dec("90"); !rows[0].Rate.Equal(want) {
		t.Fatalf("the movement was written at rate %s, want %s — 150 units falls in the [100, ) slab, "+
			"so a rate of 100.0000 means the lookup asked on a quantity that is not the line's",
			rows[0].Rate.StringFixed(4), want.StringFixed(4))
	}
	if want := dec("13500"); !rows[0].Amount.Equal(want) {
		t.Fatalf("line amount is %s, want %s (150 x 90)", rows[0].Amount.StringFixed(4), want.StringFixed(4))
	}

	// The resolved rate is not a display detail: it is the taxable base the GST
	// lines and the party leg are both derived from, so it must have reached
	// them too. 13500 + 18% = 15930.
	e.assertLegs(t, "an invoice priced from the list", res.VoucherVersionID, map[string]postedLeg{
		nameCustomer: {drcr: string(Debit), amount: dec("15930")},
		nameSales:    {drcr: string(Credit), amount: dec("13500")},
		nameOutCGST:  {drcr: string(Credit), amount: dec("1215")},
		nameOutSGST:  {drcr: string(Credit), amount: dec("1215")},
	})
}

// TestTheListsRateAndDiscountArriveTogether pins that resolving a price takes
// BOTH columns. Migration 0030 stores rate and discount apart so the invoice can
// print "1,000.00 less 5%"; taking the rate alone would invoice at 1,000.00 flat
// — a 5% overcharge with a perfectly consistent set of books behind it.
func TestTheListsRateAndDiscountArriveTogether(t *testing.T) {
	e := setupItemInvoice(t)
	e.newPriceList(t, "Trade", uuid.Nil, "2025-04-01", "",
		plSlab{item: e.bolt, qtyFrom: "0", qtyTo: "", rate: "1000", discount: "5"},
	)

	res, err := e.led.PostVoucher(e.ctx,
		e.salesInvoice(stateHome, "2025-05-10", e.unpricedLine(e.bolt, Outward, "2")))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	rows := e.movementRows(t, res.VoucherVersionID)
	if !rows[0].DiscountPct.Equal(dec("5")) {
		t.Fatalf("the list's 5%% discount did not reach the movement (got %s%%): the invoice would go out at the gross rate",
			rows[0].DiscountPct.String())
	}
	if want := dec("1900"); !rows[0].Amount.Equal(want) {
		t.Fatalf("line amount is %s, want %s (2 x 1000 less 5%%)", rows[0].Amount.StringFixed(4), want.StringFixed(4))
	}
}

// TestAStatedZeroRateIsNotRepriced is the distinction this whole wire shape
// exists for. A rate of ZERO is a stated fact — a free sample — and repricing it
// would put a price on goods the operator deliberately gave away, on an invoice
// that balances and files perfectly while doing it.
//
// The invoice carries a paid line as well as the free one, so the voucher is an
// ordinary two-line sale rather than a degenerate zero-total document, and so a
// failure is visibly "the free sample acquired a price" rather than "nothing
// posted".
func TestAStatedZeroRateIsNotRepriced(t *testing.T) {
	e := setupItemInvoice(t)
	e.newPriceList(t, "Trade", uuid.Nil, "2025-04-01", "",
		plSlab{item: e.bolt, qtyFrom: "0", qtyTo: "", rate: "100"},
	)

	res, err := e.led.PostVoucher(e.ctx, e.salesInvoice(stateHome, "2025-05-10",
		e.line(e.bolt, Outward, "10", "100", "0"), // sold
		e.line(e.bolt, Outward, "2", "0", "0"),    // FREE SAMPLE, stated as zero
	))
	if err != nil {
		t.Fatalf("post an invoice carrying a free sample: %v", err)
	}

	rows := e.movementRows(t, res.VoucherVersionID)
	if len(rows) != 2 {
		t.Fatalf("want 2 movements, got %d", len(rows))
	}
	if !rows[1].Rate.IsZero() {
		t.Fatalf("the free sample was written at rate %s: a stated rate of zero was treated as an omitted one and silently repriced from the list, "+
			"so goods that were given away have been invoiced", rows[1].Rate.StringFixed(4))
	}
	if !rows[1].Amount.IsZero() {
		t.Fatalf("the free sample's line amount is %s, want 0.0000", rows[1].Amount.StringFixed(4))
	}
	// And the taxable base follows it: 1000 for the sale, nothing for the gift.
	e.assertLegs(t, "an invoice with a free sample", res.VoucherVersionID, map[string]postedLeg{
		nameCustomer: {drcr: string(Debit), amount: dec("1180")},
		nameSales:    {drcr: string(Credit), amount: dec("1000")},
		nameOutCGST:  {drcr: string(Credit), amount: dec("90")},
		nameOutSGST:  {drcr: string(Credit), amount: dec("90")},
	})
}

// TestNoEffectivePriceIsANamedErrorNotZero: an omitted rate that no list covers
// must REFUSE the voucher.
//
// The alternative — falling back to zero — is the worst failure this feature
// can have. The invoice posts, it balances, the goods leave the warehouse, the
// GST on a taxable value of nothing is nothing, the return files clean, and the
// customer is billed nothing. There is no error anywhere to notice, because the
// arithmetic is perfect over a price that was never a price. So the test asserts
// the typed error AND that no voucher and no movement were left behind.
func TestNoEffectivePriceIsANamedErrorNotZero(t *testing.T) {
	e := setupItemInvoice(t)
	// A list that covers BOLT and says nothing about GREASE: the lookup has to
	// fail on the item, not on the absence of price lists altogether.
	e.newPriceList(t, "Trade", uuid.Nil, "2025-04-01", "",
		plSlab{item: e.bolt, qtyFrom: "0", qtyTo: "", rate: "100"},
	)
	before := e.voucherCount(t)

	_, err := e.led.PostVoucher(e.ctx,
		e.salesInvoice(stateHome, "2025-05-10", e.unpricedLine(e.grease, Outward, "5")))
	if err == nil {
		t.Fatal("a line asking to be priced, with no price list covering it, POSTED — " +
			"which means the goods have been invoiced at whatever the fallback rate was")
	}
	if !errors.Is(err, ErrNoEffectivePrice) {
		t.Fatalf("refusal is not ErrNoEffectivePrice, so the HTTP layer cannot classify it: %v", err)
	}
	// The message has to name the item by NAME: an operator told "no price list
	// covers 018f2c…" cannot act on it.
	if name := e.itemName(t, e.grease); !strings.Contains(err.Error(), name) {
		t.Fatalf("refusal does not name the item %q, so the operator cannot tell which line to fix: %v", name, err)
	}
	if after := e.voucherCount(t); after != before {
		t.Fatalf("the refused invoice left %d voucher(s) behind (was %d, now %d)", after-before, before, after)
	}
}

// TestThePriceListDoesNotOverrideAStatedRate: an operator's rate wins, even when
// it disagrees with the list. Overriding a price is an ordinary act of business
// — a negotiated deal, a clearance — and a price list that could not be
// overridden would be a cage rather than a default.
func TestThePriceListDoesNotOverrideAStatedRate(t *testing.T) {
	e := setupItemInvoice(t)
	e.newPriceList(t, "Trade", uuid.Nil, "2025-04-01", "",
		plSlab{item: e.bolt, qtyFrom: "0", qtyTo: "", rate: "100", discount: "5"},
	)

	res, err := e.led.PostVoucher(e.ctx,
		e.salesInvoice(stateHome, "2025-05-10", e.line(e.bolt, Outward, "4", "250", "0")))
	if err != nil {
		t.Fatalf("post an invoice at a negotiated rate: %v", err)
	}
	rows := e.movementRows(t, res.VoucherVersionID)
	if want := dec("250"); !rows[0].Rate.Equal(want) {
		t.Fatalf("the stated rate %s was replaced by the price list's (movement says %s): the invoice no longer says what the operator keyed",
			want.StringFixed(4), rows[0].Rate.StringFixed(4))
	}
	if !rows[0].DiscountPct.IsZero() {
		t.Fatalf("the list's discount was applied to a line that stated its own rate (got %s%%)", rows[0].DiscountPct.String())
	}
	if want := dec("1000"); !rows[0].Amount.Equal(want) {
		t.Fatalf("line amount is %s, want %s (4 x 250, no discount)", rows[0].Amount.StringFixed(4), want.StringFixed(4))
	}
}

// TestAPartyScopedListBeatsTheCompanyWideOne pins the one precedence rule in
// this design, and pins that it is decided by the PARTY LEDGER's group and not
// by whichever list the planner returned first.
func TestAPartyScopedListBeatsTheCompanyWideOne(t *testing.T) {
	e := setupItemInvoice(t)
	debtors := e.groupOf(t, e.customer)
	e.newPriceList(t, "Company Wide", uuid.Nil, "2025-04-01", "",
		plSlab{item: e.bolt, qtyFrom: "0", qtyTo: "", rate: "100"},
	)
	e.newPriceList(t, "Debtor Deal", debtors, "2025-04-01", "",
		plSlab{item: e.bolt, qtyFrom: "0", qtyTo: "", rate: "80"},
	)

	res, err := e.led.PostVoucher(e.ctx,
		e.salesInvoice(stateHome, "2025-05-10", e.unpricedLine(e.bolt, Outward, "3")))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	rows := e.movementRows(t, res.VoucherVersionID)
	if want := dec("80"); !rows[0].Rate.Equal(want) {
		t.Fatalf("the customer was charged %s, want %s — the group-scoped list did not beat the company-wide one, "+
			"so a negotiated group rate is being ignored", rows[0].Rate.StringFixed(4), want.StringFixed(4))
	}
}

// TestAnUnpricedLineNeverReachesTheMovementWriter pins the backstop in
// writeStockMovements — the single non-test INSERT path into stock_movement, and
// therefore the one place every class funnels through.
//
// Only the item-invoice generator prices a line. A Delivery Note, a Stock
// Journal, or (as here) a plain Purchase keyed with explicit postings and NO GST
// is not an item invoice, so nothing would resolve the rate and the movement
// would be written at ZERO — writing the item's weighted-average cost down and
// understating closing stock for every period after it, silently.
func TestAnUnpricedLineNeverReachesTheMovementWriter(t *testing.T) {
	e := setupItemInvoice(t)
	e.newPriceList(t, "Trade", uuid.Nil, "2025-04-01", "",
		plSlab{item: e.bolt, qtyFrom: "0", qtyTo: "", rate: "100"},
	)

	// A purchase with inventory but no GST params: NOT an item invoice, so
	// buildItemInvoice never runs.
	in := e.purchaseInvoice(stateHome, "2025-05-10", e.unpricedLine(e.bolt, Inward, "5"))
	in.GST = nil
	in.ItemLedgerID = uuid.Nil
	in.Postings = []PostingInput{
		{LedgerID: e.purchases, DrCr: Debit, Amount: dec("500")},
		{LedgerID: e.supplier, DrCr: Credit, Amount: dec("500")},
	}

	if _, err := e.led.PostVoucher(e.ctx, in); err == nil {
		t.Fatal("a voucher that is not an item invoice accepted a line asking to be priced: " +
			"the movement was written at a rate of zero and the item's weighted-average cost has been written down")
	} else if !strings.Contains(err.Error(), "price") {
		t.Fatalf("refusal does not mention the unresolved price, so nobody can tell what to fix: %v", err)
	}

	if n := e.voucherCount(t); n != 0 {
		t.Fatalf("the refused voucher left %d voucher(s) behind", n)
	}
}

// TestAStockOnlyVoucherIsRefusedByCLASS_NotOnlyByTheBackstop pins the earlier,
// more useful refusal: a Delivery Note asking to be priced is turned away
// NAMING THE CLASS, before the transaction opens.
//
// The backstop in writeStockMovements would catch it too, but it does not know
// what the operator was keying and can only say "unresolved price". An operator
// in the Delivery Note screen needs to be told that THIS SCREEN does not price
// goods — a delivery note ships stock the invoice will price later, and a stock
// journal moves it between our own shelves at our own cost, so a selling rate
// resolved onto either would carry inventory at its selling price and book the
// margin as stock.
func TestAStockOnlyVoucherIsRefusedByCLASS_NotOnlyByTheBackstop(t *testing.T) {
	e := setupItemInvoice(t)
	e.newPriceList(t, "Trade", uuid.Nil, "2025-04-01", "",
		plSlab{item: e.bolt, qtyFrom: "0", qtyTo: "", rate: "100"},
	)

	_, err := e.led.PostVoucher(e.ctx, VoucherInput{
		Class:       DeliveryNote,
		VoucherDate: "2025-05-10",
		Party:       nameCustomer,
		CreatedBy:   "tester",
		Inventory:   []InventoryLine{e.unpricedLine(e.bolt, Outward, "5")},
	})
	if err == nil {
		t.Fatal("a Delivery Note accepted a line asking to be priced from a price list")
	}
	if !strings.Contains(err.Error(), string(DeliveryNote)) {
		t.Fatalf("the refusal does not name the class, so the operator is not told that this screen does not price goods "+
			"(only the movement-writer backstop fired): %v", err)
	}
}

// TestTheLedgerPriceMatchesTheMastersResolver is the pin on the duplication this
// slice knowingly took on.
//
// masters.ResolveRate opens its OWN transaction, so post() cannot call it from
// inside its own without taking a second pooled connection; the lookup is
// therefore spelt twice. Duplicated queries drift, and a drift here would price
// the quote screen and the invoice differently — the customer is shown one
// number and billed another. This asserts the two agree over a fixture with
// both slabs and both precedence levels, so a change to either that is not made
// to both fails here instead of in somebody's accounts.
func TestTheLedgerPriceMatchesTheMastersResolver(t *testing.T) {
	e := setupItemInvoice(t)
	debtors := e.groupOf(t, e.customer)
	e.newPriceList(t, "Company Wide", uuid.Nil, "2025-04-01", "",
		plSlab{item: e.bolt, qtyFrom: "0", qtyTo: "100", rate: "100", discount: "2"},
		plSlab{item: e.bolt, qtyFrom: "100", qtyTo: "", rate: "90"},
		plSlab{item: e.grease, qtyFrom: "0", qtyTo: "", rate: "40"},
	)
	e.newPriceList(t, "Debtor Deal", debtors, "2025-04-01", "",
		plSlab{item: e.bolt, qtyFrom: "0", qtyTo: "", rate: "80", discount: "1"},
	)

	ms := masters.NewService(e.pool)

	cases := []struct {
		what  string
		item  uuid.UUID
		group uuid.UUID
		qty   string
	}{
		{"company-wide, first slab", e.bolt, uuid.Nil, "50"},
		{"company-wide, top slab", e.bolt, uuid.Nil, "150"},
		{"company-wide, exactly on the boundary", e.bolt, uuid.Nil, "100"},
		{"party-scoped beats company-wide", e.bolt, debtors, "50"},
		{"party group with no list of its own", e.grease, debtors, "7"},
	}
	for _, c := range cases {
		qty := dec(c.qty)

		want, err := ms.ResolveRate(e.ctx, masters.PriceQuery{
			StockItemID: c.item, PartyGroupID: c.group, On: "2025-05-10", Qty: qty,
		})
		if err != nil {
			t.Fatalf("%s: masters.ResolveRate: %v", c.what, err)
		}

		var got priceQuote
		if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			got, err = resolvePrice(ctx, tx, e.company, c.item, c.group, "2025-05-10", qty)
			return err
		}); err != nil {
			t.Fatalf("%s: ledger resolvePrice: %v", c.what, err)
		}

		if got.listID != want.PriceListID || got.partyScoped != want.PartyScoped ||
			!got.rate.Equal(want.Rate) || !got.discountPct.Equal(want.DiscountPct) {
			t.Fatalf("%s: the voucher path and the masters resolver disagree — "+
				"ledger says list %s (party-scoped %v) rate %s less %s%%, masters says list %s (party-scoped %v) rate %s less %s%%; "+
				"the customer would be quoted one number and billed another",
				c.what, got.listID, got.partyScoped, got.rate.StringFixed(4), got.discountPct.String(),
				want.PriceListID, want.PartyScoped, want.Rate.StringFixed(4), want.DiscountPct.String())
		}
	}
}
