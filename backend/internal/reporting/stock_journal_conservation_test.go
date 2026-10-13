package reporting_test

// stock_journal_conservation_test.go — DEFECT D103 THROUGH THE WHOLE STACK:
// ledger.PostVoucher writes a real Stock Journal, the Stock Summary reads it
// back, and the company's closing stock value must not have moved.
//
// ⛔ WHY THIS TEST EXISTS ALONGSIDE THE PURE ONES IN internal/inventory. The
// leaf tests prove the ARITHMETIC conserves. They cannot prove the report ever
// ASKS for conservation: the rule is switched on by a single string joined out
// of voucher_type.parent_class in stockMovementSQL, and there is no compiler
// anywhere on that seam. Drop the join, rename the class, let voucher_type_id
// go NULL, and every leaf test still passes while a FIFO transfer starts
// printing money onto the Balance Sheet again. This test is the only thing that
// fails.
//
// ⛔ AND THE ORACLE IS VALUE, NEVER QUANTITY. The defect moves 10 units out and
// 10 units in, so every quantity in the system is unchanged and the pre-existing
// quantity test (ledger.TestStockJournalMovesBetweenGodownsWithoutChangingTheTotal)
// passed for the entire life of the bug.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/ledger"
)

// valuedItem plants a stock item on a NAMED costing method. The default is
// WEIGHTED_AVG, which is precisely the method the defect does not appear on, so
// a fixture that forgot to set this would test nothing.
func (b *stockBooks) valuedItem(t *testing.T, name, method string) uuid.UUID {
	t.Helper()
	id := b.newItem(t, name)
	if err := b.pool.RunInTx(b.ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE stock_item SET valuation_method = $2 WHERE id = $1`, id, method)
		return err
	}); err != nil {
		t.Fatalf("put stock item %q on %s: %v", name, method, err)
	}
	return id
}

// stockJournal posts a REAL Stock Journal: the non-accounting class, with no
// postings at all, exactly as the Alt+F7 screen sends it. It must not go
// through stockBooks.post, which wraps its lines in a Purchase — the whole
// point here is the class, because the class is what carries the conservation
// obligation.
func (b *stockBooks) stockJournal(t *testing.T, date string, lines ...ledger.InventoryLine) uuid.UUID {
	t.Helper()
	res, err := b.led.PostVoucher(b.ctx, ledger.VoucherInput{
		Class:       ledger.StockJournal,
		VoucherDate: date,
		CreatedBy:   "stock-journal-conservation-test",
		Inventory:   lines,
	})
	if err != nil {
		t.Fatalf("post the Stock Journal on %s: %v", date, err)
	}
	return res.VoucherID
}

// TestAFifoTransferBetweenOurOwnGodownsConservesValue is D103, driven the way
// the browser drove it.
//
// Main Location holds TWO FIFO layers — 10 @ 120.00 then 5 @ 130.00, closing
// 15.0000 / 1,850.00 — so the position Rate the Stock Summary displays is the
// blend 123.3333 while the layer FIFO will actually consume cost 120.00. The
// operator transfers 10 units to a second store and keys the only rate the
// product shows them. Before the fix that put 33.30 of invented value onto a
// balanced Balance Sheet.
//
// ⭐ THE WEIGHTED-AVERAGE ARM RUNS FIRST AND IS THE CONTROL, so a failure on the
// FIFO arm is attributable to the COSTING METHOD and not to transfers as such.
func TestAFifoTransferBetweenOurOwnGodownsConservesValue(t *testing.T) {
	b := stockSetup(t)
	second := b.newGodown(t, "Second Store")

	avg := b.valuedItem(t, "Avg Widget", "WEIGHTED_AVG")
	fifo := b.valuedItem(t, "Fifo Widget", "FIFO")
	lifo := b.valuedItem(t, "Lifo Widget", "LIFO")

	// Two layers each, identical arithmetic on all three items, so the ONLY
	// difference between the arms below is stock_item.valuation_method.
	for _, item := range []uuid.UUID{avg, fifo, lifo} {
		b.post(t, "2026-04-10", stockLine(item, b.main, uuid.Nil, ledger.Inward, "10", "120", "0"))
		b.post(t, "2026-04-15", stockLine(item, b.main, uuid.Nil, ledger.Inward, "5", "130", "0"))
	}

	totals := func(t *testing.T) (decimal.Decimal, map[uuid.UUID]decimal.Decimal) {
		t.Helper()
		s := b.summary(t, "2026-04-30")
		byItem := map[uuid.UUID]decimal.Decimal{}
		for _, it := range s.Items {
			byItem[it.StockItemID] = it.ClosingValue
		}
		return s.TotalValue, byItem
	}

	companyBefore, itemsBefore := totals(t)
	if want := dec("5550"); !companyBefore.Equal(want) {
		t.Fatalf("the fixture is not the scenario: closing stock is %s, want %s (three items, "+
			"1,850.00 each)", companyBefore.StringFixed(4), want.StringFixed(4))
	}

	// The blend the Stock Summary displays, which is what an operator reaches
	// for because the Stock Journal screen leaves the Rate column blank.
	const displayedBlend = "123.3333"

	transfer := func(item uuid.UUID) {
		b.stockJournal(t, "2026-04-23",
			stockLine(item, b.main, uuid.Nil, ledger.Outward, "10", displayedBlend, "0"),
			stockLine(item, second, uuid.Nil, ledger.Inward, "10", displayedBlend, "0"),
		)
	}

	for _, arm := range []struct {
		name string
		item uuid.UUID
		// leftInMain is what the source godown must be left carrying, which is
		// what proves the value landed in the right PLACE and not merely that the
		// total added up.
		leftInMain string
		// carried is what the receiving godown must hold: the cost of the layers
		// that actually walked out, which differs per method and is never the
		// keyed rate.
		carried string
	}{
		// ⭐ CONTROL, RUN FIRST. Under weighted average the displayed rate IS the
		// consumption rate, so this arm conserved before the fix too. If it ever
		// moves, the fix broke the path that already worked.
		{"⭐ CONTROL WEIGHTED_AVG", avg, "616.6667", "1233.3333"},
		// ⛔ THE DEFECT. FIFO eats the OLDEST layer whole: 10 @ 120.00.
		{"⛔ FIFO", fifo, "650.0000", "1200.0000"},
		// LIFO eats the other end: 5 @ 130.00 then 5 @ 120.00.
		{"LIFO", lifo, "600.0000", "1250.0000"},
	} {
		t.Run(arm.name, func(t *testing.T) {
			transfer(arm.item)
			companyAfter, itemsAfter := totals(t)

			if got, want := itemsAfter[arm.item], itemsBefore[arm.item]; !got.Equal(want) {
				verb := "created"
				if got.LessThan(want) {
					verb = "destroyed"
				}
				t.Fatalf("moving 10 units between two of OUR OWN godowns %s %s of stock value: the item "+
					"was carried at %s and is now carried at %s, with the quantity unchanged so nothing "+
					"counting units can see it. Closing stock feeds the BALANCE SHEET, which will move "+
					"with no document saying why", verb, got.Sub(want).Abs().StringFixed(4),
					want.StringFixed(4), got.StringFixed(4))
			}
			if !companyAfter.Equal(companyBefore) {
				t.Fatalf("the COMPANY's closing stock moved %s -> %s on a transfer between its own "+
					"shelves; this is the figure the Balance Sheet's Closing Stock asset is built from",
					companyBefore.StringFixed(4), companyAfter.StringFixed(4))
			}

			// WHERE the value sits, per godown. A fix that balanced the total while
			// misplacing it would misstate every godown-wise report and mis-cost
			// every later issue out of the receiving store.
			byGodown := map[uuid.UUID]decimal.Decimal{}
			for _, row := range b.summary(t, "2026-04-30").Rows {
				if row.StockItemID == arm.item {
					byGodown[row.GodownID] = row.ClosingValue
				}
			}
			if got := byGodown[second]; got.StringFixed(4) != arm.carried {
				t.Errorf("the receiving godown carries %s, want %s: the goods must arrive at what they "+
					"actually cost on the way OUT, or every issue out of that store is costed wrong",
					got.StringFixed(4), arm.carried)
			}
			if got := byGodown[b.main]; got.StringFixed(4) != arm.leftInMain {
				t.Errorf("the source godown is left carrying %s, want %s: the layers the transfer "+
					"consumed are not the layers it removed", got.StringFixed(4), arm.leftInMain)
			}
		})
	}
}

// TestAManufacturePostedAsAStockJournalIsNotRefusedAndStillConserves.
//
// ⛔ THE CONTROL THAT KEEPS THE RULE FROM BEING A ONE-LINER. StockJournal is ONE
// class serving TWO shapes, and a manufacture MUST be able to change per-ITEM
// value — that is what manufacturing is. A naive "the inward rate must equal the
// outward rate" rule would refuse every legitimate manufacture; this arm proves
// the document still POSTS, that per-item value moved freely, and that the
// VOUCHER total nonetheless conserved, because the class posts no accounting
// lines and there is nothing to fund an increase with.
func TestAManufacturePostedAsAStockJournalIsNotRefusedAndStillConserves(t *testing.T) {
	b := stockSetup(t)
	steel := b.valuedItem(t, "Steel Bar", "FIFO")
	paint := b.valuedItem(t, "Paint", "WEIGHTED_AVG")
	chair := b.valuedItem(t, "Chair", "FIFO")

	b.post(t, "2026-04-01", stockLine(steel, b.main, uuid.Nil, ledger.Inward, "10", "100", "0"))
	b.post(t, "2026-04-02", stockLine(steel, b.main, uuid.Nil, ledger.Inward, "10", "150", "0"))
	b.post(t, "2026-04-03", stockLine(paint, b.main, uuid.Nil, ledger.Inward, "20", "25", "0"))

	before := b.summary(t, "2026-04-30").TotalValue
	if want := dec("3000"); !before.Equal(want) {
		t.Fatalf("the fixture is not the scenario: %s, want %s", before.StringFixed(4), want.StringFixed(4))
	}

	// Components consumed, a DIFFERENT finished good produced, keyed at a rate
	// the operator guessed. It must post.
	b.stockJournal(t, "2026-04-20",
		stockLine(steel, b.main, uuid.Nil, ledger.Outward, "10", "125", "0"),
		stockLine(paint, b.main, uuid.Nil, ledger.Outward, "4", "25", "0"),
		stockLine(chair, b.main, uuid.Nil, ledger.Inward, "2", "900", "0"),
	)

	s := b.summary(t, "2026-04-30")
	if !s.TotalValue.Equal(before) {
		t.Fatalf("a manufacture moved the company's closing stock %s -> %s. A Stock Journal posts NO "+
			"accounting lines, so the difference is stock value with nothing on the other side of it — "+
			"an unbacked Balance Sheet, and this build has no way to state a conversion cost that could "+
			"fund it", before.StringFixed(4), s.TotalValue.StringFixed(4))
	}

	byItem := map[uuid.UUID]decimal.Decimal{}
	for _, it := range s.Items {
		byItem[it.StockItemID] = it.ClosingValue
	}
	// 1,000.00 of steel off the oldest FIFO layer + 100.00 of paint at its average.
	if got, want := byItem[chair], dec("1100"); !got.Equal(want) {
		t.Errorf("the finished goods are carried at %s, want %s (what the components actually cost): "+
			"every chair sold from here will report the wrong gross profit",
			got.StringFixed(4), want.StringFixed(4))
	}
	// PER-ITEM value must still move freely — a rule that froze it would have
	// made manufacturing unrecordable.
	if got, want := byItem[paint], dec("400"); !got.Equal(want) {
		t.Errorf("the paint is carried at %s, want %s: a manufacture must be able to change what an "+
			"individual item is worth", got.StringFixed(4), want.StringFixed(4))
	}
}

// TestTwoTransfersOnOneStockJournalDoNotLeakValueBetweenItems is the arm that
// rejects pooling the voucher.
//
// The Alt+F7 screen emits one OUT/IN pair per KEYED ROW and a voucher may carry
// many rows, of different items — so a rule that pooled the whole voucher would
// let a cheap item absorb an expensive one's cost while the company total still
// footed, and nothing anywhere would report it.
func TestTwoTransfersOnOneStockJournalDoNotLeakValueBetweenItems(t *testing.T) {
	b := stockSetup(t)
	second := b.newGodown(t, "Second Store")
	cheap := b.valuedItem(t, "Cheap Widget", "FIFO")
	pricey := b.valuedItem(t, "Pricey Widget", "FIFO")

	b.post(t, "2026-04-01",
		stockLine(cheap, b.main, uuid.Nil, ledger.Inward, "10", "10", "0"),
		stockLine(pricey, b.main, uuid.Nil, ledger.Inward, "10", "500", "0"))

	b.stockJournal(t, "2026-04-20",
		stockLine(cheap, b.main, uuid.Nil, ledger.Outward, "10", "10", "0"),
		stockLine(cheap, second, uuid.Nil, ledger.Inward, "10", "10", "0"),
		stockLine(pricey, b.main, uuid.Nil, ledger.Outward, "10", "500", "0"),
		stockLine(pricey, second, uuid.Nil, ledger.Inward, "10", "500", "0"))

	s := b.summary(t, "2026-04-30")
	if want := dec("5100"); !s.TotalValue.Equal(want) {
		t.Fatalf("the company's closing stock is %s, want %s: two ordinary transfers on one document "+
			"changed what the company owns", s.TotalValue.StringFixed(4), want.StringFixed(4))
	}
	for _, row := range s.Rows {
		if row.GodownID != second {
			continue
		}
		want := dec("100")
		if row.StockItemID == pricey {
			want = dec("5000")
		}
		if !row.ClosingValue.Equal(want) {
			t.Errorf("%s arrives at the second store carrying %s, want %s: value has leaked between two "+
				"unrelated items on one voucher, and the company total still foots, so no report anywhere "+
				"can show it", row.ItemName, row.ClosingValue.StringFixed(4), want.StringFixed(4))
		}
	}
}
