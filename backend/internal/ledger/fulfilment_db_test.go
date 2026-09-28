package ledger

// fulfilment_db_test.go — THE ORDER LINK AGAINST THE LIVE DATABASE.
//
// ⚠️ UNLIKE cost_allocation_db_test.go, THESE WERE RUN IN THE SESSION THAT WROTE
// THEM, serially, on the shared :5433 Postgres. That file's header records why its
// five tests were handed over unexecuted and why all five then failed on the
// integrator's first run — a lane's green is a CLAIM about untested code. This
// file was written by the integrator, so the claim and the observation are the
// same act.
//
// In the INTERNAL test package (`ledger`, not `ledger_test`) for the reason
// inward_doc_integration_test.go and cost_allocation_db_test.go both record: the
// DELETE guard lives inside the unexported post(), and DeleteVoucher builds its
// OWN empty VoucherInput. A test driven only through the public API would prove
// that DeleteVoucher does not INVENT fulfilments — not that a tombstone REFUSES TO
// RESTATE ones it is handed. Those are different claims and only the second is the
// guard.
//
// ===========================================================================
// WHAT EACH TEST IS FOR
// ===========================================================================
//  1. the round trip: the ROWS and the hashed PAYLOAD agree, and a partial
//     delivery leaves the rest outstanding;
//  2. ⛔ THE ALTER — the load-bearing one. Fulfilments are VERSIONED, so a
//     correction leaves the superseded version's rows in the table forever. A
//     reader without the voucher_head.current_version_id join counts BOTH and an
//     order delivered 5 then corrected to 8 reads as 13. That is a defect no unit
//     test can see, because it needs two versions in a real table;
//  3. the tombstone: it carries none, so a deleted delivery returns the order to
//     fully outstanding;
//  4. over-fulfilment refused by the deferred trigger, WITH A CONTROL LEG. A
//     refusal that fired for the wrong reason would prove nothing — the session
//     that built T2 had two probe cases prove nothing by tripping a uniqueness
//     constraint before reaching the invariant under test;
//  5. the three invariants only the DATABASE can see, each with the control that
//     proves the refusal was for the reason claimed: wrong direction, wrong item,
//     and one voucher line discharging more commitment than it moved.

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/migrations"
)

// fulfilEnv is one isolated company with enough stock to deliver, a SALES order
// for 20 Tables and a PURCHASE order for 50 Legs — one of each direction, because
// half of what is under test is that the two cannot be crossed.
type fulfilEnv struct {
	pool    *db.Pool
	ctx     context.Context
	company uuid.UUID
	led     *Service
	ids     *identity.Service

	godown             uuid.UUID
	table, leg         uuid.UUID
	debtor, sales      uuid.UUID
	creditor, purchase uuid.UUID

	// The sales order for 20 Tables, and its single line.
	salesOrder, salesLine uuid.UUID
	// The purchase order for 50 Legs, and its single line.
	purchOrder, purchLine uuid.UUID
}

func setupFulfil(t *testing.T) *fulfilEnv {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	ctx := context.Background()

	adminConn, err := pgx.Connect(ctx, testdb.AdminDSNValue())
	if err != nil {
		t.Skipf("admin postgres unavailable, skipping integration test: %v", err)
	}
	if err := migrations.Apply(ctx, adminConn); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("apply migrations: %v", err)
	}
	_ = adminConn.Close(ctx)

	pool, err := db.New(ctx, testdb.AppDSNValue())
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}
	t.Cleanup(pool.Close)

	// newInwardCompany creates the company AND registers testdb.Cleanup for it in
	// the same breath — internal/testhygiene fails the BUILD for a
	// company-creating helper that splits the pair.
	companyID := newInwardCompany(t, pool, "Order Book Co")

	e := &fulfilEnv{
		pool:    pool,
		ctx:     db.WithCompany(ctx, companyID),
		company: companyID,
		led:     NewService(pool),
		ids:     identity.NewService(pool),
	}

	// FIND-OR-CREATE for every ledger, which is the lesson cost_allocation_db_test
	// paid for: several of these names are already seeded per company (migration
	// 0034's payroll set, the default chart), so a bare CreateLedger trips
	// ledger_company_id_name_key and every test in the file dies in setup.
	if e.debtor, err = ensureLedger(e.ctx, e.ids, "Acme Retail", "Sundry Debtors"); err != nil {
		t.Fatalf("debtor ledger: %v", err)
	}
	if e.sales, err = ensureLedger(e.ctx, e.ids, "Sales Accounts", "Sales Accounts"); err != nil {
		t.Fatalf("sales ledger: %v", err)
	}
	if e.creditor, err = ensureLedger(e.ctx, e.ids, "Timber Supplies", "Sundry Creditors"); err != nil {
		t.Fatalf("creditor ledger: %v", err)
	}
	if e.purchase, err = ensureLedger(e.ctx, e.ids, "Purchase Accounts", "Purchase Accounts"); err != nil {
		t.Fatalf("purchase ledger: %v", err)
	}

	e.table = e.newFulfilItem(t, "Table")
	e.leg = e.newFulfilItem(t, "Leg")
	if err := pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT id FROM godown WHERE company_id = $1 AND name = 'Main Location'`,
			companyID).Scan(&e.godown)
	}); err != nil {
		t.Fatalf("the company seed must create a 'Main Location': %v", err)
	}

	// OPENING STOCK, so a Delivery Note does not trip stock_movement_not_negative
	// before it ever reaches the fulfilment trigger. That would be a refusal for
	// the wrong reason — the §7.4 failure this file is careful about throughout.
	e.seedOpening(t, e.table, "1000")
	e.seedOpening(t, e.leg, "1000")

	// The orders are seeded by SQL rather than through a service, deliberately: the
	// order-master write path is not built yet, and what is under test here is the
	// LEDGER SPINE plus migration 0042's triggers. The same call
	// cost_allocation_db_test made about the cost masters.
	e.salesOrder, e.salesLine = e.seedOrder(t, "SALES", "SO-14", e.debtor, e.table, "20")
	e.purchOrder, e.purchLine = e.seedOrder(t, "PURCHASE", "PO-9", e.creditor, e.leg, "50")
	return e
}

func (e *fulfilEnv) newFulfilItem(t *testing.T, name string) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new stock item id: %v", err)
	}
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO stock_item (id, company_id, name) VALUES ($1, $2, $3)`,
			id, e.company, name+"-"+id.String()[:8])
		return err
	}); err != nil {
		t.Fatalf("create stock item %s: %v", name, err)
	}
	return id
}

func (e *fulfilEnv) seedOpening(t *testing.T, item uuid.UUID, qty string) {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new opening id: %v", err)
	}
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO stock_opening (id, company_id, stock_item_id, godown_id, qty, rate)
			 VALUES ($1, $2, $3, $4, $5, 100)`,
			id, e.company, item, e.godown, decimal.RequireFromString(qty))
		return err
	}); err != nil {
		t.Fatalf("seed opening stock: %v", err)
	}
}

// seedOrder plants one order with one line and returns (orderID, orderLineID).
func (e *fulfilEnv) seedOrder(
	t *testing.T, direction, orderNo string, party, item uuid.UUID, qty string,
) (uuid.UUID, uuid.UUID) {
	t.Helper()
	orderID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new order id: %v", err)
	}
	lineID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new order line id: %v", err)
	}
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO order_head
			   (id, company_id, direction, order_no, order_date, party_ledger_id, created_by)
			 VALUES ($1, $2, $3::order_direction, $4, DATE '2025-05-01', $5, 'tester')`,
			orderID, e.company, direction, orderNo+"-"+orderID.String()[:8], party); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO order_line
			   (id, company_id, order_id, line_no, stock_item_id, qty, rate, due_date)
			 VALUES ($1, $2, $3, 1, $4, $5, 500, DATE '2025-06-30')`,
			lineID, e.company, orderID, item, decimal.RequireFromString(qty))
		return err
	}); err != nil {
		t.Fatalf("seed %s order: %v", direction, err)
	}
	return orderID, lineID
}

// deliveryOf is a Delivery Note: goods OUT, no postings (a stock-only class),
// with the supplied fulfilment links.
func (e *fulfilEnv) deliveryOf(item uuid.UUID, qty string, fs []FulfilmentInput) VoucherInput {
	return VoucherInput{
		Class:       DeliveryNote,
		VoucherDate: "2025-06-10",
		Narration:   "Part delivery",
		Party:       "Acme Retail",
		CreatedBy:   "tester",
		// ⚠️ NO LineNo FIELD: line_no is derived from the SLICE POSITION by
		// writeStockMovements, so Inventory[0] is line 1 — which is exactly why a
		// FulfilmentInput.LineNo of 1 names it and an array index of 0 would not.
		Inventory: []InventoryLine{{
			StockItemID: item,
			GodownID:    e.godown,
			Direction:   Outward,
			Qty:         decimal.RequireFromString(qty),
			Rate:        decimal.RequireFromString("500"),
			Amount:      decimal.RequireFromString(qty).Mul(decimal.RequireFromString("500")),
		}},
		Fulfilments: fs,
	}
}

// receiptOf is a Receipt Note: goods IN, the mirror of deliveryOf.
func (e *fulfilEnv) receiptOf(item uuid.UUID, qty string, fs []FulfilmentInput) VoucherInput {
	return VoucherInput{
		Class:       ReceiptNote,
		VoucherDate: "2025-06-11",
		Narration:   "Part receipt",
		Party:       "Timber Supplies",
		CreatedBy:   "tester",
		Inventory: []InventoryLine{{
			StockItemID: item,
			GodownID:    e.godown,
			Direction:   Inward,
			Qty:         decimal.RequireFromString(qty),
			Rate:        decimal.RequireFromString("100"),
			Amount:      decimal.RequireFromString(qty).Mul(decimal.RequireFromString("100")),
		}},
		Fulfilments: fs,
	}
}

// fulfilledOn is THE READER UNDER TEST, and it is written the way the order book
// writes it: summed across CURRENT, non-deleted voucher versions only.
//
// ⛔ THE voucher_head JOIN IS THE ASSERTION. Fulfilment rows are versioned and the
// superseded version's rows stay in the table forever, so a sum without the join
// counts a corrected delivery twice. It is spelled out here rather than borrowed
// from reporting.OrderBookFor so that a change to one is not silently blessed by
// the other: this is the independent statement of what the number means.
func (e *fulfilEnv) fulfilledOn(t *testing.T, orderLine uuid.UUID) decimal.Decimal {
	t.Helper()
	var got decimal.Decimal
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT COALESCE(SUM(f.qty), 0)
			   FROM order_fulfilment f
			   JOIN voucher_head vh
			     ON vh.company_id         = f.company_id
			    AND vh.current_version_id = f.voucher_version_id
			  WHERE f.company_id    = $1
			    AND f.order_line_id = $2
			    AND vh.is_deleted   = false`,
			e.company, orderLine).Scan(&got)
	}); err != nil {
		t.Fatalf("read fulfilled qty: %v", err)
	}
	return got
}

// allFulfilmentRowsFor counts EVERY row for an order line, current version or
// not. The difference between this and fulfilledOn is the whole point of test 2.
func (e *fulfilEnv) allFulfilmentRowsFor(t *testing.T, orderLine uuid.UUID) (rows int, sum decimal.Decimal) {
	t.Helper()
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*), COALESCE(SUM(qty), 0) FROM order_fulfilment
			  WHERE company_id = $1 AND order_line_id = $2`,
			e.company, orderLine).Scan(&rows, &sum)
	}); err != nil {
		t.Fatalf("count all fulfilment rows: %v", err)
	}
	return rows, sum
}

// payloadFulfilments reads the hashed payload of a voucher's CURRENT version.
// The rows and the signature are supposed to be two renderings of ONE value, and
// a test that read only the rows could not tell whether that still held.
func (e *fulfilEnv) payloadFulfilments(t *testing.T, voucherID uuid.UUID) []CanonicalFulfilment {
	t.Helper()
	var payload CanonicalPayload
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT vv.payload FROM voucher_version vv
			   JOIN voucher_head vh
			     ON vh.company_id = vv.company_id AND vh.current_version_id = vv.id
			  WHERE vv.company_id = $1 AND vh.voucher_id = $2`,
			e.company, voucherID).Scan(&payload)
	}); err != nil {
		t.Fatalf("read current payload: %v", err)
	}
	return payload.Fulfilments
}

// fq is this file's shorthand for a fulfilment quantity. Named fq rather than
// dec because item_invoice_test.go already declares a package-level dec().
func fq(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// ---------------------------------------------------------------------------
// 1. The round trip, and partial fulfilment as the normal case
// ---------------------------------------------------------------------------

func TestAPartialDeliveryLinksToItsOrderInBothTheRowsAndTheHash(t *testing.T) {
	e := setupFulfil(t)

	// THE CONTROL, first: nothing is fulfilled before the delivery. Without it
	// every assertion below is consistent with the reader being broken in a way
	// that always returns the expected number.
	if got := e.fulfilledOn(t, e.salesLine); !got.Equal(decimal.Zero) {
		t.Fatalf("CONTROL: a fresh order line is %s fulfilled, want 0", got)
	}

	res, err := e.led.PostVoucher(e.ctx, e.deliveryOf(e.table, "5", []FulfilmentInput{
		{LineNo: 1, OrderLineID: e.salesLine, Qty: fq("5")},
	}))
	if err != nil {
		t.Fatalf("post delivery note with a fulfilment: %v", err)
	}

	// THE ROWS.
	if got := e.fulfilledOn(t, e.salesLine); !got.Equal(fq("5")) {
		t.Errorf("after delivering 5 of 20, fulfilled = %s, want 5", got)
	}

	// THE HASHED PAYLOAD, which must say the same thing. All-string fields, the
	// quantity at fixed-4 scale, and the LINE NUMBER 1-based — the spelling
	// VerifyChain's re-derivation depends on.
	fs := e.payloadFulfilments(t, res.VoucherID)
	if len(fs) != 1 {
		t.Fatalf("the payload carries %d fulfilment(s), want 1: the rows and the signature are "+
			"supposed to be two renderings of one value", len(fs))
	}
	if fs[0].LineNo != "1" {
		t.Errorf("payload line_no = %q, want \"1\": stock_movement.line_no is 1-based and the "+
			"payload must spell the same voucher line the same way", fs[0].LineNo)
	}
	if fs[0].OrderLineID != e.salesLine.String() {
		t.Errorf("payload order_line_id = %q, want %q", fs[0].OrderLineID, e.salesLine)
	}
	if fs[0].Qty != "5.0000" {
		t.Errorf("payload qty = %q, want \"5.0000\": the SCALE has to survive or VerifyChain's "+
			"jsonb -> struct -> bytes re-derivation is not exact", fs[0].Qty)
	}

	// AND THE STOCK MOVED, which is a SEPARATE fact from the link. 5 units really
	// left the building; the order was consumed by 5. This test asserts both
	// because the entire model rests on them being different facts that happen to
	// agree here.
	var moved decimal.Decimal
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT COALESCE(SUM(sm.qty), 0) FROM stock_movement sm
			   JOIN voucher_head vh ON vh.company_id = sm.company_id
			                       AND vh.current_version_id = sm.voucher_version_id
			  WHERE sm.company_id = $1 AND sm.stock_item_id = $2 AND sm.direction = 'OUT'`,
			e.company, e.table).Scan(&moved)
	}); err != nil {
		t.Fatalf("read movement: %v", err)
	}
	if !moved.Equal(fq("5")) {
		t.Errorf("stock moved OUT = %s, want 5", moved)
	}

	// A SECOND delivery: partial fulfilment is the normal case and the two must
	// accumulate rather than replace.
	if _, err := e.led.PostVoucher(e.ctx, e.deliveryOf(e.table, "12", []FulfilmentInput{
		{LineNo: 1, OrderLineID: e.salesLine, Qty: fq("12")},
	})); err != nil {
		t.Fatalf("post second delivery: %v", err)
	}
	if got := e.fulfilledOn(t, e.salesLine); !got.Equal(fq("17")) {
		t.Errorf("after 5 then 12 of a 20-unit order, fulfilled = %s, want 17 (3 outstanding)", got)
	}
}

// ---------------------------------------------------------------------------
// 2. ⛔ THE ALTER. The one defect no unit test can see.
// ---------------------------------------------------------------------------

func TestAlteringADeliveryRestatesItsFulfilmentAndDoesNotDoubleCount(t *testing.T) {
	e := setupFulfil(t)

	res, err := e.led.PostVoucher(e.ctx, e.deliveryOf(e.table, "5", []FulfilmentInput{
		{LineNo: 1, OrderLineID: e.salesLine, Qty: fq("5")},
	}))
	if err != nil {
		t.Fatalf("post delivery: %v", err)
	}
	if got := e.fulfilledOn(t, e.salesLine); !got.Equal(fq("5")) {
		t.Fatalf("CONTROL before the alter: fulfilled = %s, want 5", got)
	}

	// The correction: it was 8, not 5.
	alter := e.deliveryOf(e.table, "8", []FulfilmentInput{
		{LineNo: 1, OrderLineID: e.salesLine, Qty: fq("8")},
	})
	if _, err := e.led.AlterVoucher(e.ctx, res.VoucherID, alter); err != nil {
		t.Fatalf("alter the delivery to 8: %v", err)
	}

	// ⛔ THE ASSERTION THIS FILE EXISTS FOR. Both versions' rows are in the table —
	// that is proved on the next line — and the reader must count only the current
	// one. 13 here is the defect: an order delivered 5 and corrected to 8 reporting
	// itself 13 delivered and 7 outstanding, with every figure looking plausible
	// and nothing to reconcile it against.
	if got := e.fulfilledOn(t, e.salesLine); !got.Equal(fq("8")) {
		t.Errorf("after altering 5 -> 8, fulfilled = %s, want 8. If this says 13 the reader is "+
			"missing the voucher_head.current_version_id join and every corrected delivery in "+
			"the product is counted twice", got)
	}

	// THE CONTROL THAT MAKES THE ABOVE MEAN SOMETHING: the superseded row really
	// is still there, so the join is doing work rather than the table happening to
	// hold one row.
	rows, sum := e.allFulfilmentRowsFor(t, e.salesLine)
	if rows != 2 {
		t.Errorf("the table holds %d fulfilment row(s) for this order line, want 2 (the superseded "+
			"version's and the current one's). With only one row the assertion above would pass "+
			"even without the version join, and would prove nothing", rows)
	}
	if !sum.Equal(fq("13")) {
		t.Errorf("the UNFILTERED sum is %s, want 13 — that is exactly the wrong answer a reader "+
			"without the join produces, and it must remain reachable so this test keeps its teeth", sum)
	}
}

// ---------------------------------------------------------------------------
// 3. The tombstone carries none
// ---------------------------------------------------------------------------

func TestDeletingADeliveryReturnsTheOrderToOutstanding(t *testing.T) {
	e := setupFulfil(t)

	res, err := e.led.PostVoucher(e.ctx, e.deliveryOf(e.table, "5", []FulfilmentInput{
		{LineNo: 1, OrderLineID: e.salesLine, Qty: fq("5")},
	}))
	if err != nil {
		t.Fatalf("post delivery: %v", err)
	}
	if got := e.fulfilledOn(t, e.salesLine); !got.Equal(fq("5")) {
		t.Fatalf("CONTROL before the delete: fulfilled = %s, want 5", got)
	}

	if _, err := e.led.DeleteVoucher(e.ctx, res.VoucherID, "tester"); err != nil {
		t.Fatalf("delete the delivery: %v", err)
	}

	// A deleted delivery fulfils nothing: the customer is still owed all 20. Were
	// the tombstone to restate the link, a salesperson would see nothing
	// outstanding while the customer waited, with the two facts in different
	// tables and nothing to reconcile them.
	if got := e.fulfilledOn(t, e.salesLine); !got.Equal(decimal.Zero) {
		t.Errorf("after deleting the delivery, fulfilled = %s, want 0", got)
	}
	// And the tombstone's own payload carries none.
	if fs := e.payloadFulfilments(t, res.VoucherID); len(fs) != 0 {
		t.Errorf("the tombstone's payload carries %d fulfilment(s), want 0", len(fs))
	}
}

// ---------------------------------------------------------------------------
// 4. Over-fulfilment, refused at COMMIT, with a control
// ---------------------------------------------------------------------------

func TestOverFulfilmentIsRefusedAcrossVouchersAndACorrectOneStillCommits(t *testing.T) {
	e := setupFulfil(t)

	// THE CONTROL LEG. A correct fulfilment of the same order line commits. Without
	// this, the refusal below is consistent with the whole path being broken.
	if _, err := e.led.PostVoucher(e.ctx, e.deliveryOf(e.table, "18", []FulfilmentInput{
		{LineNo: 1, OrderLineID: e.salesLine, Qty: fq("18")},
	})); err != nil {
		t.Fatalf("CONTROL: delivering 18 of 20 must be accepted, got: %v", err)
	}
	if got := e.fulfilledOn(t, e.salesLine); !got.Equal(fq("18")) {
		t.Fatalf("CONTROL: fulfilled = %s, want 18", got)
	}

	// Now 5 more against a 20-unit order that already has 18 against it. Neither
	// voucher is individually over-fulfilling, so ONLY the cumulative check in the
	// deferred trigger can see this — ledger.validateFulfilments cannot, because it
	// holds one request and this needs the other voucher's rows.
	_, err := e.led.PostVoucher(e.ctx, e.deliveryOf(e.table, "5", []FulfilmentInput{
		{LineNo: 1, OrderLineID: e.salesLine, Qty: fq("5")},
	}))
	if err == nil {
		t.Fatalf("delivering 5 more against an order with 18 of 20 already fulfilled was ACCEPTED; " +
			"the order book would report a negative outstanding balance")
	}
	// The message must name the order and the excess, not a constraint.
	if !strings.Contains(err.Error(), "23") || !strings.Contains(strings.ToLower(err.Error()), "order") {
		t.Errorf("the refusal does not name the order and the 23 now claimed against it: %v", err)
	}

	// AND THE REFUSAL LEFT NOTHING BEHIND. A deferred trigger fires at COMMIT, so
	// the whole transaction rolls back — including the stock movement. If this
	// reads 23 the rollback did not happen and the books hold a delivery the order
	// book will not admit to.
	if got := e.fulfilledOn(t, e.salesLine); !got.Equal(fq("18")) {
		t.Errorf("after the refused voucher, fulfilled = %s, want 18 — the transaction did not "+
			"roll back cleanly", got)
	}
}

// ---------------------------------------------------------------------------
// 5. The three invariants only the database can see
// ---------------------------------------------------------------------------

// A SALES order is fulfilled by goods going OUT. A Receipt Note brings them IN,
// so it cannot satisfy a promise to deliver — and no foreign key can express
// that, because it is an equality between two tables' columns.
func TestAReceiptCannotFulfilASalesOrderAndADeliveryCan(t *testing.T) {
	e := setupFulfil(t)

	_, err := e.led.PostVoucher(e.ctx, e.receiptOf(e.table, "5", []FulfilmentInput{
		{LineNo: 1, OrderLineID: e.salesLine, Qty: fq("5")},
	}))
	if err == nil {
		t.Fatalf("a Receipt Note (goods IN) was allowed to fulfil a SALES order (a promise to " +
			"deliver); the order book would report the commitment met while the goods came the " +
			"other way")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "sales") {
		t.Errorf("the refusal does not say which direction was wrong: %v", err)
	}

	// THE CONTROL: the very same order line, fulfilled by a DELIVERY, is accepted.
	// This is what proves the refusal above was about the direction and not about
	// the order, the item or the fixture.
	if _, err := e.led.PostVoucher(e.ctx, e.deliveryOf(e.table, "5", []FulfilmentInput{
		{LineNo: 1, OrderLineID: e.salesLine, Qty: fq("5")},
	})); err != nil {
		t.Fatalf("CONTROL: a Delivery Note fulfilling the same sales order must be accepted: %v", err)
	}

	// And the mirror, so neither direction is accidentally permissive: a Receipt
	// Note DOES fulfil a purchase order.
	if _, err := e.led.PostVoucher(e.ctx, e.receiptOf(e.leg, "10", []FulfilmentInput{
		{LineNo: 1, OrderLineID: e.purchLine, Qty: fq("10")},
	})); err != nil {
		t.Fatalf("MIRROR CONTROL: a Receipt Note fulfilling a PURCHASE order must be accepted: %v", err)
	}
}

// Delivering Legs cannot discharge an order for Tables. Every quantity would be
// internally consistent and the customer would not have what they ordered.
func TestDeliveringADifferentItemCannotFulfilTheOrder(t *testing.T) {
	e := setupFulfil(t)

	_, err := e.led.PostVoucher(e.ctx, e.deliveryOf(e.leg, "5", []FulfilmentInput{
		{LineNo: 1, OrderLineID: e.salesLine, Qty: fq("5")},
	}))
	if err == nil {
		t.Fatalf("delivering Legs was allowed to fulfil a sales order for Tables; the order book " +
			"would report the order met")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "item") &&
		!strings.Contains(err.Error(), "Table") {
		t.Errorf("the refusal does not name the item mismatch: %v", err)
	}

	// THE CONTROL: the same order line and the same quantity, with the RIGHT item.
	if _, err := e.led.PostVoucher(e.ctx, e.deliveryOf(e.table, "5", []FulfilmentInput{
		{LineNo: 1, OrderLineID: e.salesLine, Qty: fq("5")},
	})); err != nil {
		t.Fatalf("CONTROL: the same delivery with the ordered item must be accepted: %v", err)
	}
}

// ⛔ THE DOUBLE-COUNT HOLE. One voucher line may fulfil SEVERAL order lines, so
// neither order individually looks over-fulfilled — but twenty units of goods
// cannot discharge forty units of promise. This is the cost-centre model's
// parallel-axis double count reached from the other direction, and only a check
// grouped on the VOUCHER LINE can see it.
func TestOneDeliveryLineCannotDischargeMoreCommitmentThanItMoved(t *testing.T) {
	e := setupFulfil(t)

	// A second sales order for the same item, so one delivery line can name two.
	_, secondLine := e.seedOrder(t, "SALES", "SO-15", e.debtor, e.table, "20")

	// THE CONTROL FIRST, and it is the ordinary case this feature must permit: one
	// 20-unit delivery line legitimately covering a 12-unit claim and an 8-unit one.
	if _, err := e.led.PostVoucher(e.ctx, e.deliveryOf(e.table, "20", []FulfilmentInput{
		{LineNo: 1, OrderLineID: e.salesLine, Qty: fq("12")},
		{LineNo: 1, OrderLineID: secondLine, Qty: fq("8")},
	})); err != nil {
		t.Fatalf("CONTROL: one 20-unit line covering a 12 and an 8 must be accepted — splitting a "+
			"shipment across two orders is the ordinary case: %v", err)
	}

	// Now the violation: 20 units moved, 20 claimed against each of two orders.
	_, thirdLine := e.seedOrder(t, "SALES", "SO-16", e.debtor, e.table, "20")
	_, fourthLine := e.seedOrder(t, "SALES", "SO-17", e.debtor, e.table, "20")
	_, err := e.led.PostVoucher(e.ctx, e.deliveryOf(e.table, "20", []FulfilmentInput{
		{LineNo: 1, OrderLineID: thirdLine, Qty: fq("20")},
		{LineNo: 1, OrderLineID: fourthLine, Qty: fq("20")},
	}))
	if err == nil {
		t.Fatalf("one 20-unit delivery line was allowed to discharge 20 units against EACH of two " +
			"orders — forty units of commitment met by twenty units of goods, with neither order " +
			"looking over-fulfilled and nothing else in the system able to notice")
	}
	if !strings.Contains(err.Error(), "40") {
		t.Errorf("the refusal does not state the 40 claimed against 20 moved: %v", err)
	}
}
