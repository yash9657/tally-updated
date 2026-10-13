package ledger

// nonaccounting_test.go — the stock vouchers that post no accounting lines,
// against the LIVE database.
//
// In the internal test package (`ledger`, not `ledger_test`) for the same
// reason stock_integration_test.go is: these tests reach post()'s branches
// directly and reuse that file's stockEnv, which is the canonical inventory
// fixture for this package.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// --- fixture extensions -----------------------------------------------------

// newGodown plants a second stock location, so a Stock Journal has somewhere to
// transfer TO. Godowns are masters; the seed only creates 'Main Location'.
func (e *stockEnv) newGodown(t *testing.T, name string) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new godown id: %v", err)
	}
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO godown (id, company_id, name) VALUES ($1, $2, $3)`,
			id, e.company, name+"-"+id.String()[:8])
		return err
	}); err != nil {
		t.Fatalf("create godown %s: %v", name, err)
	}
	return id
}

// stockVoucher builds a NON-ACCOUNTING voucher: a class, some movements, and
// deliberately NO postings, no bill allocations, no GST. The voucher number is
// left empty so the server allocates from the series migration 0029 seeded for
// the class — which is itself part of what is under test, since a class with no
// voucher_type would post with a NULL voucher_type_id.
func (e *stockEnv) stockVoucher(class Class, lines ...InventoryLine) VoucherInput {
	return VoucherInput{
		Class:       class,
		VoucherDate: "2025-05-10",
		Narration:   string(class) + " under test",
		Party:       "Acme Supplies",
		CreatedBy:   "tester",
		Inventory:   lines,
	}
}

// lineAt builds a movement of the environment's item at an explicit godown.
func (e *stockEnv) lineAt(godown uuid.UUID, d Direction, qty, rate string) InventoryLine {
	q := decimal.RequireFromString(qty)
	r := decimal.RequireFromString(rate)
	return InventoryLine{
		StockItemID: e.item,
		GodownID:    godown,
		Direction:   d,
		Qty:         q,
		Rate:        r,
		Amount:      q.Mul(r),
	}
}

// positionQty is the closing quantity of one position as every report computes
// it: opening plus the net of every LIVE voucher's CURRENT version. It joins
// voucher_head.current_version_id because movements are versioned — a sum that
// forgets the join double-counts every altered voucher.
func (e *stockEnv) positionQty(t *testing.T, godown uuid.UUID) decimal.Decimal {
	t.Helper()
	var qty decimal.Decimal
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT COALESCE((SELECT SUM(so.qty) FROM stock_opening so
			                   WHERE so.company_id = $1 AND so.stock_item_id = $2
			                     AND so.godown_id IS NOT DISTINCT FROM $3), 0)
			      + COALESCE((SELECT SUM(CASE WHEN sm.direction = 'IN' THEN sm.qty ELSE -sm.qty END)
			                    FROM stock_movement sm
			                    JOIN voucher_head vh
			                      ON vh.company_id = sm.company_id
			                     AND vh.current_version_id = sm.voucher_version_id
			                   WHERE sm.company_id = $1 AND sm.stock_item_id = $2
			                     AND sm.godown_id IS NOT DISTINCT FROM $3
			                     AND vh.is_deleted = false), 0)`,
			e.company, e.item, godown).Scan(&qty)
	}); err != nil {
		t.Fatalf("read position: %v", err)
	}
	return qty
}

// itemTotalQty is the same figure summed across EVERY location.
func (e *stockEnv) itemTotalQty(t *testing.T) decimal.Decimal {
	t.Helper()
	var qty decimal.Decimal
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT COALESCE((SELECT SUM(so.qty) FROM stock_opening so
			                   WHERE so.company_id = $1 AND so.stock_item_id = $2), 0)
			      + COALESCE((SELECT SUM(CASE WHEN sm.direction = 'IN' THEN sm.qty ELSE -sm.qty END)
			                    FROM stock_movement sm
			                    JOIN voucher_head vh
			                      ON vh.company_id = sm.company_id
			                     AND vh.current_version_id = sm.voucher_version_id
			                   WHERE sm.company_id = $1 AND sm.stock_item_id = $2
			                     AND vh.is_deleted = false), 0)`,
			e.company, e.item).Scan(&qty)
	}); err != nil {
		t.Fatalf("read item total: %v", err)
	}
	return qty
}

// postingCount counts posting rows: for one version when versionID is non-nil,
// otherwise for the whole company.
func (e *stockEnv) postingCount(t *testing.T, versionID uuid.UUID) int {
	t.Helper()
	var n int
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		if versionID == uuid.Nil {
			return tx.QueryRow(ctx,
				`SELECT count(*) FROM posting WHERE company_id = $1`, e.company).Scan(&n)
		}
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM posting WHERE company_id = $1 AND voucher_version_id = $2`,
			e.company, versionID).Scan(&n)
	}); err != nil {
		t.Fatalf("count postings: %v", err)
	}
	return n
}

// receive stocks the shelf with a Receipt Note so the outward tests have
// something to ship. Returns the posted result.
func (e *stockEnv) receive(t *testing.T, godown uuid.UUID, qty, rate string) PostResult {
	t.Helper()
	res, err := e.led.PostVoucher(e.ctx, e.stockVoucher(ReceiptNote, e.lineAt(godown, Inward, qty, rate)))
	if err != nil {
		t.Fatalf("receipt note of %s: %v", qty, err)
	}
	return res
}

func qty(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// ---------------------------------------------------------------------------

// TestStockVouchersPostNoAccountingLines is the whole point of the slice: a
// Delivery Note commits with ZERO posting rows and real stock movements.
//
// It also pins the pairing that makes that safe — a Delivery Note that carries
// postings is REFUSED. Skipping the double-entry check without refusing
// postings would make these the one document in the system permitted to state
// unbalanced ledger lines, which is a bigger hole than the one the skip closes.
func TestStockVouchersPostNoAccountingLines(t *testing.T) {
	e := setupStock(t)
	e.receive(t, e.godown, "20.0000", "100.0000")

	dn, err := e.led.PostVoucher(e.ctx,
		e.stockVoucher(DeliveryNote, e.lineAt(e.godown, Outward, "8.0000", "100.0000")))
	if err != nil {
		t.Fatalf("a Delivery Note must post without any accounting lines: %v", err)
	}

	if n := e.postingCount(t, dn.VoucherVersionID); n != 0 {
		t.Fatalf("the Delivery Note wrote %d posting(s): goods that left the warehouse have booked revenue nobody invoiced", n)
	}
	if n := e.postingCount(t, uuid.Nil); n != 0 {
		t.Fatalf("the company has %d posting(s) after two stock-only vouchers: a Receipt/Delivery Note must not touch the ledger at all", n)
	}
	if got := e.movementsOf(t, dn.VoucherVersionID); len(got) != 1 || !got[0].Qty.Equal(qty("8")) || got[0].Direction != string(Outward) {
		t.Fatalf("the Delivery Note must record exactly one outward movement of 8, got %+v", got)
	}
	if got := e.positionQty(t, e.godown); !got.Equal(qty("12")) {
		t.Fatalf("after receiving 20 and delivering 8 the shelf holds %s, want 12", got)
	}
	// The number came from the Delivery Note's OWN series (migration 0029), not
	// from the invoice series and not from nowhere.
	if dn.VoucherNo == "" {
		t.Fatal("the Delivery Note was allocated no voucher number: its voucher_type is missing, which also NULLs voucher_version.voucher_type_id")
	}

	// The pairing. A Delivery Note that states ledger lines is refused, not
	// silently accepted with the double-entry check skipped.
	withPostings := e.stockVoucher(DeliveryNote, e.lineAt(e.godown, Outward, "1.0000", "100.0000"))
	withPostings.Postings = []PostingInput{
		{LedgerID: e.purchase, DrCr: Debit, Amount: qty("100")},
		{LedgerID: e.creditor, DrCr: Credit, Amount: qty("40")},
	}
	if _, err := e.led.PostVoucher(e.ctx, withPostings); err == nil {
		t.Fatal("a Delivery Note carrying (unbalanced) postings was accepted: skipping the double-entry check without refusing postings makes this the one voucher allowed to be unbalanced")
	} else if !strings.Contains(err.Error(), "posts no accounting lines") {
		t.Fatalf("the refusal must say the class posts no accounting lines, got %v", err)
	}
}

// TestDoubleEntryIsNeverRelaxedForAnAccountingClass is the reason
// accountingClasses is a compile-time set and not voucher_type.is_accounting.
//
// It flips the flag in the DATABASE for this throwaway company's Sales type and
// proves the post path does not care.
//
// READ THE TWO ARMS SEPARATELY, they are not equally strong:
//
//   - the UNBALANCED arm is also backstopped by the deferred posting_balanced
//     trigger (migration 0002), so it would fail even if the Go check were
//     skipped — it is here because it is the obvious case, not because it is
//     the load-bearing one;
//
//   - the ZERO-POSTINGS arm is the one no database can catch. posting_balanced
//     is a trigger on the posting TABLE: a voucher that inserts no posting rows
//     never fires it. A Sales invoice that ships stock and posts nothing leaves
//     a trial balance that still balances and is simply missing the sale. If
//     the accounting decision is ever keyed off the DB flag, THIS is what one
//     UPDATE buys.
func TestDoubleEntryIsNeverRelaxedForAnAccountingClass(t *testing.T) {
	e := setupStock(t)
	e.receive(t, e.godown, "20.0000", "100.0000")

	// Flip the flag on THIS company's Sales voucher type. (A throwaway company
	// created by setupStock — never a shared one.)
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE voucher_type SET is_accounting = false
			  WHERE company_id = $1 AND parent_class = 'SALES'`, e.company)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			t.Fatal("no Sales voucher type to flip: the test cannot demonstrate anything")
		}
		return nil
	}); err != nil {
		t.Fatalf("flip is_accounting: %v", err)
	}

	// Arm 1: an unbalanced Sales voucher.
	unbalanced := VoucherInput{
		Class: Sales, VoucherDate: "2025-05-11", CreatedBy: "tester",
		Postings: []PostingInput{
			{LedgerID: e.purchase, DrCr: Debit, Amount: qty("100")},
			{LedgerID: e.creditor, DrCr: Credit, Amount: qty("60")},
		},
	}
	if _, err := e.led.PostVoucher(e.ctx, unbalanced); err == nil {
		t.Fatal("an unbalanced Sales voucher posted after is_accounting was flipped in the database")
	} else if !strings.Contains(err.Error(), "double-entry violation") {
		t.Fatalf("the refusal must be the double-entry violation, got %v", err)
	}

	// Arm 2: a Sales voucher that ships goods and states NO postings at all.
	// Nothing in the database can refuse this one.
	before := e.voucherCount(t)
	silent := VoucherInput{
		Class: Sales, VoucherDate: "2025-05-11", CreatedBy: "tester",
		Inventory: []InventoryLine{e.lineAt(e.godown, Outward, "5.0000", "100.0000")},
	}
	if _, err := e.led.PostVoucher(e.ctx, silent); err == nil {
		t.Fatal("a Sales voucher shipped 5 units and posted NOTHING: no revenue, no receivable, no tax, and a trial balance that still balances while missing the sale — this is what keying double-entry off voucher_type.is_accounting costs")
	} else if !strings.Contains(err.Error(), "at least two postings") {
		t.Fatalf("the refusal must be the double-entry check demanding postings, got %v", err)
	}
	if after := e.voucherCount(t); after != before {
		t.Fatalf("the refused Sales voucher committed anyway: %d -> %d heads", before, after)
	}
	if got := e.positionQty(t, e.godown); !got.Equal(qty("20")) {
		t.Fatalf("the refused Sales voucher moved stock: shelf holds %s, want 20", got)
	}
}

// TestStockJournalMovesBetweenGodownsWithoutChangingTheTotal. A transfer is the
// one voucher that carries BOTH an outward and an inward — which is why
// stock_movement_not_negative is DEFERRABLE INITIALLY DEFERRED: checked per
// statement it would judge the OUT before the IN existed.
//
// The per-location positions must change and the item total must not: a
// transfer creates and destroys nothing.
func TestStockJournalMovesBetweenGodownsWithoutChangingTheTotal(t *testing.T) {
	e := setupStock(t)
	branch := e.newGodown(t, "Branch Store")

	e.receive(t, e.godown, "20.0000", "100.0000")
	totalBefore := e.itemTotalQty(t)

	sj, err := e.led.PostVoucher(e.ctx, e.stockVoucher(StockJournal,
		e.lineAt(e.godown, Outward, "6.0000", "100.0000"),
		e.lineAt(branch, Inward, "6.0000", "100.0000"),
	))
	if err != nil {
		t.Fatalf("a Stock Journal must be able to carry an OUT and an IN on the same voucher: %v", err)
	}
	if n := e.postingCount(t, sj.VoucherVersionID); n != 0 {
		t.Fatalf("the Stock Journal wrote %d posting(s): moving goods between our own shelves is not a transaction with anybody", n)
	}

	if got := e.positionQty(t, e.godown); !got.Equal(qty("14")) {
		t.Fatalf("Main Location holds %s after transferring 6 out of 20, want 14", got)
	}
	if got := e.positionQty(t, branch); !got.Equal(qty("6")) {
		t.Fatalf("the Branch Store holds %s after receiving the transfer, want 6", got)
	}
	if got := e.itemTotalQty(t); !got.Equal(totalBefore) {
		t.Fatalf("the transfer changed the item total from %s to %s: stock was created or destroyed by moving it between our own locations", totalBefore, got)
	}
}

// TestAManufactureIsStillPostable is the CONTROL that keeps D103's fix from
// being a one-liner.
//
// ⛔ StockJournal is ONE class serving TWO shapes (see the constant's doc): a
// TRANSFER of the same item between godowns, and a MANUFACTURE that consumes
// several components and produces a DIFFERENT finished good in a DIFFERENT
// quantity. The obvious spelling of "a transfer must conserve value" — refuse a
// voucher whose inward rate does not match its outward rate, or whose legs do
// not net to zero — would refuse every legitimate manufacture, and a manufacture
// MUST be able to change per-item value, because that is what manufacturing IS.
//
// ⭐ SO THE RULE REFUSES NOTHING. Conservation is achieved by DERIVING what the
// inward legs are carried at, in the replay, from what the outward legs actually
// consumed (inventory/conserving.go). Nothing about it can reject a document,
// which is exactly what this arm pins: three components out, one finished good
// in, unequal quantities, unrelated rates, and it commits.
func TestAManufactureIsStillPostable(t *testing.T) {
	e := setupStock(t)
	timber := e.newItem(t, "Timber")
	glue := e.newItem(t, "Glue")
	chair := e.newItem(t, "Chair")

	line := func(item uuid.UUID, d Direction, qty, rate string) InventoryLine {
		q := decimal.RequireFromString(qty)
		r := decimal.RequireFromString(rate)
		return InventoryLine{
			StockItemID: item, GodownID: e.godown, Direction: d,
			Qty: q, Rate: r, Amount: q.Mul(r),
		}
	}

	// The components have to be on the shelf first, or the refusal would be
	// negative stock and this test would be pinning the wrong thing.
	if _, err := e.led.PostVoucher(e.ctx, e.stockVoucher(ReceiptNote,
		line(timber, Inward, "50.0000", "40.0000"),
		line(glue, Inward, "10.0000", "15.0000"),
	)); err != nil {
		t.Fatalf("stock the components: %v", err)
	}

	res, err := e.led.PostVoucher(e.ctx, e.stockVoucher(StockJournal,
		line(timber, Outward, "20.0000", "40.0000"),
		line(glue, Outward, "3.0000", "15.0000"),
		// A DIFFERENT item, a DIFFERENT quantity, and a rate the operator guessed.
		line(chair, Inward, "4.0000", "250.0000"),
	))
	if err != nil {
		t.Fatalf("a MANUFACTURE was refused: %v — a Stock Journal serves two shapes and this is the "+
			"second one; a conservation rule that refuses it makes manufacturing unrecordable in this "+
			"product, which is a far larger hole than the one it closes", err)
	}
	if n := e.postingCount(t, res.VoucherVersionID); n != 0 {
		t.Fatalf("the manufacture wrote %d posting(s): turning our own goods into other goods is not a "+
			"transaction with anybody", n)
	}
	// ⭐ THE VALUE HALF OF THIS CLAIM IS PINNED IN internal/reporting, NOT HERE.
	// Value is never stored — there is no column to read — so what the finished
	// good is carried at can only be asserted through a report that replays the
	// ledger: see TestAManufacturePostedAsAStockJournalIsNotRefusedAndStillConserves.
}

// TestStockJournalRefusesAZeroRateInward covers what the test ABOVE cannot see.
//
// ⛔ THE TEST ABOVE SUMS QUANTITY, AND QUANTITY IS EXACTLY WHAT THIS DEFECT
// LEAVES ALONE. It passed throughout the entire life of the bug. A transfer keyed
// with the Rate column blank ships `rate: "0.0000"` — a STATED zero, which the
// RateUnstated backstop cannot see, because that flag means "the line said
// nothing about price" and this line said something. The inward leg then carries
// the goods at no cost and writes the position's weighted average DOWN, so
// company-wide closing stock VALUE falls by the full average cost of the goods
// moved while every quantity in the system stays put. Closing stock feeds the
// BALANCE SHEET. Measured in a browser on 2026-08-07: 8,890 off the sheet.
//
// This is the same rule buildPhysicalStockDeltas already applies to a counted
// surplus, and for the identical reason; the two paths are now symmetric.
func TestStockJournalRefusesAZeroRateInward(t *testing.T) {
	e := setupStock(t)
	branch := e.newGodown(t, "Branch Store")
	e.receive(t, e.godown, "20.0000", "100.0000")
	before := e.voucherCount(t)

	_, err := e.led.PostVoucher(e.ctx, e.stockVoucher(StockJournal,
		e.lineAt(e.godown, Outward, "6.0000", "0.0000"),
		e.lineAt(branch, Inward, "6.0000", "0.0000"),
	))
	if err == nil {
		t.Fatal("a transfer brought 6 units in at a rate of zero: the item's weighted average is now " +
			"written down and closing stock value has fallen, with the quantity unchanged so that " +
			"nothing counting units can see it")
	}
	if !strings.Contains(err.Error(), "rate") {
		t.Fatalf("the refusal must tell the operator to state the rate, got %v", err)
	}
	if after := e.voucherCount(t); after != before {
		t.Fatalf("the refused transfer committed anyway: %d -> %d heads", before, after)
	}

	// ⛔ TWO CONTROLS (§9.3), because there are two ways to make this check wrong:
	// refuse every transfer, or refuse every zero.
	//
	// 1. The same document PRICED must still post.
	if _, err := e.led.PostVoucher(e.ctx, e.stockVoucher(StockJournal,
		e.lineAt(e.godown, Outward, "6.0000", "100.0000"),
		e.lineAt(branch, Inward, "6.0000", "100.0000"),
	)); err != nil {
		t.Fatalf("a transfer priced at cost must still post: %v", err)
	}

	// 2. A zero on an OUTWARD is legitimate and must NOT be refused: an outward is
	// consumed at the position's running weighted average and its own rate is
	// IGNORED by the valuer, so no figure anybody reads depends on it.
	if _, err := e.led.PostVoucher(e.ctx, e.stockVoucher(DeliveryNote,
		e.lineAt(e.godown, Outward, "2.0000", "0.0000"),
	)); err != nil {
		t.Fatalf("a Delivery Note with no rate must still ship (the valuer ignores an outward's rate): %v", err)
	}

	// 3. ⛔ AND THE CONTROL THAT NEARLY DID NOT EXIST: A FREE SAMPLE STILL POSTS.
	//
	// The first draft of this rule refused a zero-rate inward on EVERY stock-only
	// class, and the server's read-back audit caught it — a Receipt Note at
	// 0.0000 is a free sample, a stated price rather than a missing one, and
	// carrying it at nothing is CORRECT (quantity rises, value does not). The
	// refusal belongs to the TRANSFER alone, because only there did the outward
	// leg already remove value that the inward has to carry back.
	if _, err := e.led.PostVoucher(e.ctx, e.stockVoucher(ReceiptNote,
		e.lineAt(e.godown, Inward, "4.0000", "0.0000"),
	)); err != nil {
		t.Fatalf("a free sample really does arrive at 0.0000 and must still post: %v", err)
	}
}

// TestDeliveryNoteRespectsNegativeStock. A stock-only voucher is not exempt
// from the inventory rules just because it is exempt from the accounting ones —
// a Delivery Note cannot ship goods that are not there.
//
// It must fail as a TYPED *NegativeStockError. The deferred trigger is the
// authority and would refuse the commit either way, but its message arrives as
// a raw SQLSTATE P0001 the HTTP layer cannot classify; the typed error is what
// names the item and the location for the operator who has to fix the entry.
func TestDeliveryNoteRespectsNegativeStock(t *testing.T) {
	e := setupStock(t)
	e.receive(t, e.godown, "3.0000", "100.0000")

	var itemName string
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT name FROM stock_item WHERE company_id = $1 AND id = $2`,
			e.company, e.item).Scan(&itemName)
	}); err != nil {
		t.Fatalf("read item name: %v", err)
	}

	_, err := e.led.PostVoucher(e.ctx,
		e.stockVoucher(DeliveryNote, e.lineAt(e.godown, Outward, "10.0000", "100.0000")))

	var ne *NegativeStockError
	if !errors.As(err, &ne) {
		t.Fatalf("shipping 10 units against a shelf of 3 must be refused as a typed *NegativeStockError so the operator is told WHICH item and location; got %v", err)
	}
	if ne.ItemName != itemName {
		t.Fatalf("the refusal names item %q, not %q", ne.ItemName, itemName)
	}
	if !ne.Closing.Equal(qty("-7")) {
		t.Fatalf("the refusal reports a closing balance of %s, want -7", ne.Closing)
	}
	if got := e.positionQty(t, e.godown); !got.Equal(qty("3")) {
		t.Fatalf("the refused Delivery Note moved stock anyway: shelf holds %s, want 3", got)
	}
}

// TestStockVoucherAlterAndDeleteRestateMovements. These ride the same versioned
// children as an invoice, so they inherit the same property: an ALTER RESTATES
// the movements under a new version (the superseded rows stay in the log as the
// evidence of what the voucher used to say) and a DELETE tombstone carries NONE.
//
// The delete half is the one worth stating in domain terms: if a tombstone
// restated its lines, the delivered goods would stay off the shelf forever in
// every stock report while the voucher itself had vanished from the books.
func TestStockVoucherAlterAndDeleteRestateMovements(t *testing.T) {
	e := setupStock(t)
	e.receive(t, e.godown, "20.0000", "100.0000")

	dn, err := e.led.PostVoucher(e.ctx,
		e.stockVoucher(DeliveryNote, e.lineAt(e.godown, Outward, "8.0000", "100.0000")))
	if err != nil {
		t.Fatalf("post delivery note: %v", err)
	}
	if got := e.positionQty(t, e.godown); !got.Equal(qty("12")) {
		t.Fatalf("after delivering 8 of 20 the shelf holds %s, want 12", got)
	}

	v2, err := e.led.AlterVoucher(e.ctx, dn.VoucherID,
		e.stockVoucher(DeliveryNote, e.lineAt(e.godown, Outward, "3.0000", "100.0000")))
	if err != nil {
		t.Fatalf("alter delivery note: %v", err)
	}
	if head := e.movementsOf(t, v2.VoucherVersionID); len(head) != 1 || !head[0].Qty.Equal(qty("3")) {
		t.Fatalf("the altered version must restate exactly one movement of 3, got %+v", head)
	}
	if old := e.movementsOf(t, dn.VoucherVersionID); len(old) != 1 || !old[0].Qty.Equal(qty("8")) {
		t.Fatalf("the superseded version's movement was destroyed by the alter (%+v): the edit log can no longer show what changed", old)
	}
	if got := e.positionQty(t, e.godown); !got.Equal(qty("17")) {
		t.Fatalf("after correcting the delivery from 8 to 3 the shelf holds %s, want 17 — an alter that appends instead of restating ships the goods twice", got)
	}

	del, err := e.led.DeleteVoucher(e.ctx, dn.VoucherID, "deleter")
	if err != nil {
		t.Fatalf("delete delivery note: %v", err)
	}
	if tomb := e.movementsOf(t, del.VoucherVersionID); len(tomb) != 0 {
		t.Fatalf("the tombstone carries %d movement(s): the delivered goods would stay off the shelf forever while the voucher had vanished from the books", len(tomb))
	}
	if got := e.positionQty(t, e.godown); !got.Equal(qty("20")) {
		t.Fatalf("after deleting the Delivery Note the shelf holds %s, want the full 20 back", got)
	}

	// THE PHYSICAL STOCK HALF, which is where restatement is not free.
	//
	// A Delivery Note's ALTER restates a line the client sent. A Physical Stock
	// voucher's ALTER restates a line the SERVER derived — and it must derive it
	// against a book that EXCLUDES this voucher's own superseded version.
	// Movements are versioned and voucher_head has not moved yet at that point in
	// the transaction, so a differencing that counted them would compare the new
	// count against a book that already contains the old correction, and the
	// shelf would settle anywhere except on the counted figure. The re-count is
	// the ordinary case (the operator miskeyed, or counted again), so getting
	// this wrong is not exotic.
	ps, err := e.led.PostVoucher(e.ctx, VoucherInput{
		Class: PhysicalStock, VoucherDate: "2025-05-12", CreatedBy: "counter",
		PhysicalCount: []PhysicalCountLine{{
			StockItemID: e.item, GodownID: e.godown, Qty: qty("13.0000"), Rate: qty("100.0000"),
		}},
	})
	if err != nil {
		t.Fatalf("post physical stock: %v", err)
	}
	if got := e.positionQty(t, e.godown); !got.Equal(qty("13")) {
		t.Fatalf("after counting 13 the shelf holds %s, want 13", got)
	}

	psV2, err := e.led.AlterVoucher(e.ctx, ps.VoucherID, VoucherInput{
		Class: PhysicalStock, VoucherDate: "2025-05-12", CreatedBy: "counter",
		PhysicalCount: []PhysicalCountLine{{
			StockItemID: e.item, GodownID: e.godown, Qty: qty("16.0000"), Rate: qty("100.0000"),
		}},
	})
	if err != nil {
		t.Fatalf("alter physical stock: %v", err)
	}
	if got := e.positionQty(t, e.godown); !got.Equal(qty("16")) {
		t.Fatalf("re-counting the same shelf as 16 left it holding %s: the correction was differenced against a book that still contained the count it is replacing, so a re-count compounds instead of restating", got)
	}
	if moves := e.movementsOf(t, psV2.VoucherVersionID); len(moves) != 1 ||
		moves[0].Direction != string(Outward) || !moves[0].Qty.Equal(qty("4")) {
		t.Fatalf("the re-count must restate ONE correction of -4 against the book of 20 (NOT +3 against the 13 it is replacing), got %+v", moves)
	}

	// Deleting the stock-take gives the books back their own figure: the count
	// never happened, so neither did the correction.
	if _, err := e.led.DeleteVoucher(e.ctx, ps.VoucherID, "deleter"); err != nil {
		t.Fatalf("delete physical stock: %v", err)
	}
	if got := e.positionQty(t, e.godown); !got.Equal(qty("20")) {
		t.Fatalf("after deleting the stock-take the shelf holds %s, want the book figure of 20 back", got)
	}
}

// TestPhysicalStockPostsTheDeltaNotTheCount pins the design decision: a counted
// figure becomes a CORRECTION, so the movement ledger stays a plain
// order-independent sum. An absolute row would make every reader of
// stock_movement — the negative-stock trigger, the pre-check, the valuer, every
// report — apply movements IN ORDER, which quantity has never had to be.
//
// It also pins the two boundary cases: a count that AGREES with the books
// records no movement at all, and a count that exceeds them brings the surplus
// in at the stated cost.
func TestPhysicalStockPostsTheDeltaNotTheCount(t *testing.T) {
	e := setupStock(t)
	e.receive(t, e.godown, "20.0000", "100.0000")

	count := func(q, rate string) VoucherInput {
		return VoucherInput{
			Class: PhysicalStock, VoucherDate: "2025-05-12", CreatedBy: "counter",
			PhysicalCount: []PhysicalCountLine{{
				StockItemID: e.item, GodownID: e.godown,
				Qty: qty(q), Rate: qty(rate),
			}},
		}
	}

	// Counted 13 against a book of 20: one OUTWARD correction of 7, leaving
	// exactly the counted figure.
	short, err := e.led.PostVoucher(e.ctx, count("13.0000", "100.0000"))
	if err != nil {
		t.Fatalf("physical stock (shortage): %v", err)
	}
	moves := e.movementsOf(t, short.VoucherVersionID)
	if len(moves) != 1 || moves[0].Direction != string(Outward) || !moves[0].Qty.Equal(qty("7")) {
		t.Fatalf("counting 13 against a book of 20 must post ONE outward correction of 7, got %+v — an absolute movement of 13 would leave the shelf at 33", moves)
	}
	if got := e.positionQty(t, e.godown); !got.Equal(qty("13")) {
		t.Fatalf("after the count the shelf holds %s, want the counted 13", got)
	}
	if n := e.postingCount(t, short.VoucherVersionID); n != 0 {
		t.Fatalf("the Physical Stock voucher wrote %d posting(s); a stock-take posts nothing", n)
	}

	// Counted 25 against a book of 13: an INWARD correction of 12 at the rate
	// the extra units are carried at.
	over, err := e.led.PostVoucher(e.ctx, count("25.0000", "90.0000"))
	if err != nil {
		t.Fatalf("physical stock (surplus): %v", err)
	}
	moves = e.movementsOf(t, over.VoucherVersionID)
	if len(moves) != 1 || moves[0].Direction != string(Inward) || !moves[0].Qty.Equal(qty("12")) {
		t.Fatalf("counting 25 against a book of 13 must post ONE inward correction of 12, got %+v", moves)
	}
	if got := e.positionQty(t, e.godown); !got.Equal(qty("25")) {
		t.Fatalf("after the second count the shelf holds %s, want the counted 25", got)
	}

	// Counted 25 against a book of 25: the books were right, so there is nothing
	// to correct and no movement to record.
	agrees, err := e.led.PostVoucher(e.ctx, count("25.0000", "90.0000"))
	if err != nil {
		t.Fatalf("physical stock (agrees): %v", err)
	}
	if moves := e.movementsOf(t, agrees.VoucherVersionID); len(moves) != 0 {
		t.Fatalf("a count that agrees with the books recorded %d movement(s): a zero correction is not something that happened", len(moves))
	}
	if got := e.positionQty(t, e.godown); !got.Equal(qty("25")) {
		t.Fatalf("a confirming count changed the shelf to %s, want 25", got)
	}
}

// TestPhysicalStockShortageIsNotNegativeStock. A count LOWER than the books,
// with company.allow_negative_stock false — the default — must COMMIT.
//
// This is the case a naive implementation refuses. The correction is an
// outward, and outwards are what the negative-stock rule polices; but the
// position a count leaves behind is the COUNTED quantity, which cannot be
// negative because a count cannot be negative. Refusing it would leave the
// books permanently asserting stock that a human has just physically confirmed
// is not there, and the only way to fix it would be to turn the safety off.
func TestPhysicalStockShortageIsNotNegativeStock(t *testing.T) {
	e := setupStock(t)

	var allow bool
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT allow_negative_stock FROM company WHERE id = $1`, e.company).Scan(&allow)
	}); err != nil {
		t.Fatalf("read allow_negative_stock: %v", err)
	}
	if allow {
		t.Fatal("this test is only meaningful with negative stock REFUSED, which is the default")
	}

	e.receive(t, e.godown, "10.0000", "100.0000")

	// The shelf is empty. That is a fact, not a withdrawal.
	res, err := e.led.PostVoucher(e.ctx, VoucherInput{
		Class: PhysicalStock, VoucherDate: "2025-05-12", CreatedBy: "counter",
		PhysicalCount: []PhysicalCountLine{{
			StockItemID: e.item, GodownID: e.godown, Qty: decimal.Zero,
		}},
	})
	if err != nil {
		t.Fatalf("a stock-take that found nothing on the shelf was refused as negative stock: %v", err)
	}
	moves := e.movementsOf(t, res.VoucherVersionID)
	if len(moves) != 1 || moves[0].Direction != string(Outward) || !moves[0].Qty.Equal(qty("10")) {
		t.Fatalf("counting 0 against a book of 10 must write off exactly 10, got %+v", moves)
	}
	if got := e.positionQty(t, e.godown); !got.Equal(decimal.Zero) {
		t.Fatalf("after counting an empty shelf the position is %s, want 0", got)
	}
}

// TestPhysicalStockSurplusNeedsARate. The surplus inward is INVENTED by the
// server — no supplier priced it — so letting the rate default to zero would
// bring units in at no cost and quietly write the item's STOCK VALUE down. A
// stock-take that misstates the balance sheet is the opposite of what a
// stock-take is for, so it is refused with a message that says what to supply.
//
// ⚠️ THE REFUSAL MUST NOT NAME A COSTING METHOD (D104). It used to say
// "weighted-average value" on every item, including the FIFO and LIFO ones
// migration 0078 made possible, where there is no weighted average to write
// down — the rule was right on all three, only its reason named one. The arm
// below pins the absence, so a future edit cannot put a single method's
// vocabulary back into a message every method sees.
func TestPhysicalStockSurplusNeedsARate(t *testing.T) {
	e := setupStock(t)
	e.receive(t, e.godown, "10.0000", "100.0000")
	before := e.voucherCount(t)

	_, err := e.led.PostVoucher(e.ctx, VoucherInput{
		Class: PhysicalStock, VoucherDate: "2025-05-12", CreatedBy: "counter",
		PhysicalCount: []PhysicalCountLine{{
			StockItemID: e.item, GodownID: e.godown, Qty: qty("14.0000"),
		}},
	})
	if err == nil {
		t.Fatal("a surplus of 4 units was brought in at zero cost: the item's stock value is now understated and every later issue overstates cost of goods sold")
	}
	if !strings.Contains(err.Error(), "zero cost") {
		t.Fatalf("the refusal must explain that a zero-cost surplus writes the valuation down, got %v", err)
	}
	if strings.Contains(err.Error(), "weighted-average") {
		t.Fatalf("the surplus refusal names ONE costing method to an operator whose item may be on any of three, "+
			"so on a FIFO or LIFO item it explains the refusal with a number that does not exist: %v", err)
	}
	if after := e.voucherCount(t); after != before {
		t.Fatalf("the refused count committed anyway: %d -> %d heads", before, after)
	}

	// A shortage on the same shelf needs NO rate: an outward is consumed at
	// whatever the item's own valuer says it cost, and its own rate is ignored.
	if _, err := e.led.PostVoucher(e.ctx, VoucherInput{
		Class: PhysicalStock, VoucherDate: "2025-05-12", CreatedBy: "counter",
		PhysicalCount: []PhysicalCountLine{{
			StockItemID: e.item, GodownID: e.godown, Qty: qty("6.0000"),
		}},
	}); err != nil {
		t.Fatalf("a shortage needs no rate (an outward consumes at the running average): %v", err)
	}
}

// TestOrderVouchersAreRefusedUntilCommittedQuantityExists pins the decision to
// DEFER Sales/Purchase Orders rather than model them wrong.
//
// An order is a COMMITMENT and stock_movement can only express a movement. Had
// the order been accepted as a movement, this quotation would have emptied the
// warehouse: the refusal is what stops a promise from being a withdrawal.
func TestOrderVouchersAreRefusedUntilCommittedQuantityExists(t *testing.T) {
	e := setupStock(t)
	e.receive(t, e.godown, "20.0000", "100.0000")
	before := e.voucherCount(t)

	for _, class := range []Class{SalesOrder, PurchaseOrder} {
		_, err := e.led.PostVoucher(e.ctx,
			e.stockVoucher(class, e.lineAt(e.godown, Outward, "20.0000", "100.0000")))
		if !errors.Is(err, ErrOrderVouchersNotModelled) {
			t.Fatalf("%s must be refused with ErrOrderVouchersNotModelled (an order recorded as a movement lets a quotation deplete the warehouse); got %v", class, err)
		}
	}

	if after := e.voucherCount(t); after != before {
		t.Fatalf("an order voucher committed: %d -> %d heads", before, after)
	}
	if got := e.positionQty(t, e.godown); !got.Equal(qty("20")) {
		t.Fatalf("an order moved stock: the shelf holds %s, want the full 20 still there to sell", got)
	}
}

// TestReceiptNoteRefusesOutwardLines. A stock-only class with a FIXED direction
// must enforce it: a Receipt Note carrying an outward line is a Delivery Note
// keyed into the wrong screen, and accepting it would remove goods from the
// warehouse under a document that says they arrived.
func TestReceiptNoteRefusesOutwardLines(t *testing.T) {
	e := setupStock(t)
	e.receive(t, e.godown, "20.0000", "100.0000")

	_, err := e.led.PostVoucher(e.ctx,
		e.stockVoucher(ReceiptNote, e.lineAt(e.godown, Outward, "5.0000", "100.0000")))
	if err == nil {
		t.Fatal("a Receipt Note shipped goods OUT: the document says they arrived and the warehouse says they left")
	}
	if !strings.Contains(err.Error(), "moves stock IN") {
		t.Fatalf("the refusal must name the direction the class moves, got %v", err)
	}

	// A Stock Journal, by contrast, is REQUIRED to be able to carry both: a
	// transfer is an OUT and an IN, and a manufacture consumes and produces.
	if _, err := e.led.PostVoucher(e.ctx, e.stockVoucher(StockJournal,
		e.lineAt(e.godown, Outward, "5.0000", "100.0000"),
		e.lineAt(e.godown, Inward, "5.0000", "100.0000"),
	)); err != nil {
		t.Fatalf("a Stock Journal must accept both directions on one voucher: %v", err)
	}
}
