package inventory

// conserving_test.go — DEFECT D103: a Stock Journal between the company's own
// godowns must neither create nor destroy value, on EVERY costing method.
//
// ⛔ THE ORACLE IS TOTAL VALUE ACROSS EVERY POSITION THE VOUCHER TOUCHES, AND
// NEVER QUANTITY. Quantity is exactly what this defect leaves alone: the
// transfer that put 33.30 onto a live Balance Sheet moved 10 units out and 10
// units in, so every quantity in the system stayed put and no quantity check
// anywhere could see it. A test that summed units passed throughout the whole
// life of the bug.
//
// ⭐ THE CONTROLS ARE HALF THE FILE, because there are two ways to get this
// wrong and both look like a fix: value every transfer at zero, or refuse
// transfers. The weighted-average arm and the single-layer FIFO arm both
// conserved BEFORE the fix and must still conserve after it — they are what
// locates the defect in the RATE rather than in "transfers" or in "FIFO" — and
// the non-conserving arm proves a Receipt Note can still bring value in from
// outside, which is the whole reason the rule is scoped to a class.

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// --------------------------------------------------------------------------
// Fixture helpers
// --------------------------------------------------------------------------

// leg is one line of a test voucher: where it lands, which way, how much, and
// at what rate the operator keyed.
type leg struct {
	item   string
	godown string
	dir    Direction
	qty    string
	rate   string
}

// voucher builds one voucher's movements. conserves is Movement.VoucherConserves
// — true for a Stock Journal, false for everything else — and it is a parameter
// rather than a constant so every test can state which document it is keying.
func voucher(t *testing.T, date, no string, conserves bool, legs ...leg) []Movement {
	t.Helper()
	out := make([]Movement, 0, len(legs))
	for i, l := range legs {
		out = append(out, Movement{
			VoucherDate:      day(t, date),
			VoucherNo:        no,
			VoucherID:        no,
			VersionCreatedAt: day(t, date).Add(9 * time.Hour),
			LineNo:           i + 1,
			VoucherVersionID: no + "/v1",
			Position:         PositionKey{StockItemID: l.item, GodownID: l.godown},
			Direction:        l.dir,
			Qty:              dec(t, l.qty),
			Rate:             dec(t, l.rate),
			DiscountPct:      decimal.Zero,
			VoucherConserves: conserves,
		})
	}
	return out
}

func joinMovements(sets ...[]Movement) []Movement {
	var out []Movement
	for _, s := range sets {
		out = append(out, s...)
	}
	return out
}

// totalValueOf is the company's closing stock value: every position, summed.
// It is the figure the Balance Sheet's Closing Stock asset is built from.
func totalValueOf(positions map[PositionKey]Position) decimal.Decimal {
	total := decimal.Zero
	for _, p := range positions {
		total = total.Add(p.ClosingValue)
	}
	return total
}

func valueOf(t *testing.T, positions map[PositionKey]Position, item, godown string) decimal.Decimal {
	t.Helper()
	return positions[PositionKey{StockItemID: item, GodownID: godown}].ClosingValue
}

// assertConserved is the whole point of the file, and its message states the
// CONSEQUENCE rather than the arithmetic.
func assertConserved(t *testing.T, what string, before, after decimal.Decimal) {
	t.Helper()
	if before.Equal(after) {
		return
	}
	verb := "created"
	if after.LessThan(before) {
		verb = "destroyed"
	}
	t.Fatalf("%s: %s %s of stock value on a voucher that moved goods between OUR OWN shelves — "+
		"closing stock went %s -> %s with the quantity unchanged, so nothing counting units can see it, "+
		"and closing stock feeds the BALANCE SHEET, which will move with no document saying why",
		what, verb, after.Sub(before).Abs().StringFixed(4),
		before.StringFixed(4), after.StringFixed(4))
}

// twoLayers is the exact position the browser probe built: 10 @ 120.00 arriving
// before 5 @ 130.00, closing 15.0000 / 1,850.00, whose DISPLAYED rate is the
// blend 123.3333 and whose FIFO consumption rate is 120.00. The gap between
// those two numbers is the defect.
func twoLayers(t *testing.T, item string) []Movement {
	t.Helper()
	return joinMovements(
		voucher(t, "2026-04-10", "RN-1", false, leg{item, "main", DirectionIn, "10", "120"}),
		voucher(t, "2026-04-15", "RN-2", false, leg{item, "main", DirectionIn, "5", "130"}),
	)
}

// --------------------------------------------------------------------------
// The defect
// --------------------------------------------------------------------------

// TestAMultiLayerFifoTransferBetweenGodownsConservesValue is D103 itself.
//
// Main Location holds two FIFO layers. The operator transfers 10 units to a
// second store and keys 123.33 — the position Rate the Stock Summary displays,
// which is the ONLY rate the product shows anywhere and which the Stock Journal
// screen deliberately leaves blank. The OUT leg consumes the OLDEST LAYER at
// 120.00; the keyed blend is 123.33; the gap is pure invention.
//
// ⭐ The keyed rate is varied across the sub-tests on purpose: after the fix NO
// keystroke can move the total, because the inward leg is carried at what the
// outward leg actually consumed and the keyed number is not consulted at all.
func TestAMultiLayerFifoTransferBetweenGodownsConservesValue(t *testing.T) {
	for _, keyed := range []string{"123.33", "0.01", "9999", "120"} {
		t.Run("keyed_"+keyed, func(t *testing.T) {
			movements := joinMovements(
				twoLayers(t, "widget"),
				voucher(t, "2026-04-23", "SJ-1", true,
					leg{"widget", "main", DirectionOut, "10", keyed},
					leg{"widget", "second", DirectionIn, "10", keyed},
				),
			)
			positions := ReplayAll(nil, movements, FIFO{})

			assertConserved(t, "a FIFO transfer keyed at "+keyed,
				dec(t, "1850"), totalValueOf(positions))

			// AND IT MUST LAND IN THE RIGHT PLACE, not merely add up. A fix that
			// balanced the total while misplacing it between the two godowns would
			// misstate every godown-wise report and every later issue's cost.
			if got, want := valueOf(t, positions, "widget", "second"), dec(t, "1200"); !got.Equal(want) {
				t.Errorf("the receiving godown carries %s; the goods that arrived cost %s on the way out "+
					"(the whole oldest layer, 10 @ 120.00), so every issue out of that store will now be "+
					"costed wrong", got.StringFixed(4), want.StringFixed(4))
			}
			if got, want := valueOf(t, positions, "widget", "main"), dec(t, "650"); !got.Equal(want) {
				t.Errorf("Main Location is left carrying %s; after its oldest layer walked out it holds "+
					"5 units of the 130.00 layer and nothing else, which is %s",
					got.StringFixed(4), want.StringFixed(4))
			}
		})
	}
}

// TestALifoTransferBetweenGodownsConservesValue. LIFO eats the OTHER end of the
// queue, so the same document consumes 1,250.00 rather than 1,200.00 — proving
// the inward leg follows the ITEM'S OWN VALUER and not a hard-coded rule that
// happens to agree with FIFO.
func TestALifoTransferBetweenGodownsConservesValue(t *testing.T) {
	movements := joinMovements(
		twoLayers(t, "widget"),
		voucher(t, "2026-04-23", "SJ-1", true,
			leg{"widget", "main", DirectionOut, "10", "123.33"},
			leg{"widget", "second", DirectionIn, "10", "123.33"},
		),
	)
	positions := ReplayAll(nil, movements, LIFO{})

	assertConserved(t, "a LIFO transfer", dec(t, "1850"), totalValueOf(positions))

	// 5 @ 130 = 650 newest-first, then 5 out of the 120 layer = 600.
	if got, want := valueOf(t, positions, "widget", "second"), dec(t, "1250"); !got.Equal(want) {
		t.Errorf("the receiving godown carries %s under LIFO, but the layers that actually left cost %s "+
			"(5 @ 130.00 then 5 @ 120.00): the inward leg is not following the item's own valuer",
			got.StringFixed(4), want.StringFixed(4))
	}
}

// TestATransferWhoseOldestLayerIsDearerDestroysNothing. ⚠️ THE DIRECTION OF THE
// DEFECT WAS NEVER FIXED. With the dear layer FIRST, the same keystrokes on the
// broken build DESTROYED value instead of creating it — so a fix validated only
// against "the total must not rise" would be half a fix.
func TestATransferWhoseOldestLayerIsDearerDestroysNothing(t *testing.T) {
	movements := joinMovements(
		voucher(t, "2026-04-10", "RN-1", false, leg{"widget", "main", DirectionIn, "10", "200"}),
		voucher(t, "2026-04-15", "RN-2", false, leg{"widget", "main", DirectionIn, "10", "100"}),
		// The displayed blend is 150.00; FIFO will consume at 200.00.
		voucher(t, "2026-04-23", "SJ-1", true,
			leg{"widget", "main", DirectionOut, "10", "150"},
			leg{"widget", "second", DirectionIn, "10", "150"},
		),
	)
	positions := ReplayAll(nil, movements, FIFO{})

	assertConserved(t, "a FIFO transfer whose oldest layer is the DEARER one",
		dec(t, "3000"), totalValueOf(positions))
	if got, want := valueOf(t, positions, "widget", "second"), dec(t, "2000"); !got.Equal(want) {
		t.Errorf("the receiving godown carries %s, but the layer that left cost %s: keying the blend "+
			"would have written 500.00 off the Balance Sheet", got.StringFixed(4), want.StringFixed(4))
	}
}

// --------------------------------------------------------------------------
// ⭐ THE CONTROLS — the arms that were ALREADY GREEN before the fix
// --------------------------------------------------------------------------

// TestAWeightedAverageTransferStillConserves is the control that proves the fix
// did not break the path that already worked. Under weighted average the
// DISPLAYED rate IS the consumption rate, so keying it always conserved; if this
// arm ever moves, the fix broke the majority of real companies to repair the
// minority.
func TestAWeightedAverageTransferStillConserves(t *testing.T) {
	movements := joinMovements(
		twoLayers(t, "widget"),
		voucher(t, "2026-04-23", "SJ-1", true,
			leg{"widget", "main", DirectionOut, "10", "123.3333"},
			leg{"widget", "second", DirectionIn, "10", "123.3333"},
		),
	)
	positions := ReplayAll(nil, movements, WeightedAverage{})
	assertConserved(t, "⭐ CONTROL: a WEIGHTED_AVG transfer, the path that already worked",
		dec(t, "1850"), totalValueOf(positions))
}

// TestASingleLayerFifoTransferStillConserves is the control that LOCATES the
// defect. One layer means the displayed blend and the consumption rate
// coincide, and the identical keystrokes always conserved — so the fault was
// never "transfers" and never "FIFO", it was the rate ceasing to be the
// consumption rate the moment a queue holds more than one layer.
func TestASingleLayerFifoTransferStillConserves(t *testing.T) {
	movements := joinMovements(
		voucher(t, "2026-04-15", "RN-1", false, leg{"widget", "main", DirectionIn, "5", "130"}),
		voucher(t, "2026-04-23", "SJ-1", true,
			leg{"widget", "main", DirectionOut, "5", "130"},
			leg{"widget", "second", DirectionIn, "5", "130"},
		),
	)
	positions := ReplayAll(nil, movements, FIFO{})
	assertConserved(t, "⭐ CONTROL: a SINGLE-LAYER FIFO transfer, where the offered rate is the consumption rate",
		dec(t, "650"), totalValueOf(positions))
}

// TestAReceiptNoteStillBringsValueInFromOutside is the control that keeps the
// rule SCOPED. Conservation is right only where both sides of the document are
// our own stock. A Receipt Note has no outward leg to derive from and MUST be
// able to raise closing stock — a build that "conserved" every voucher would
// make it impossible to buy anything.
func TestAReceiptNoteStillBringsValueInFromOutside(t *testing.T) {
	movements := joinMovements(
		voucher(t, "2026-04-10", "RN-1", false, leg{"widget", "main", DirectionIn, "10", "120"}),
		// A free sample: a STATED zero, which is a fact and not a missing price.
		voucher(t, "2026-04-11", "RN-2", false, leg{"widget", "main", DirectionIn, "2", "0"}),
	)
	positions := ReplayAll(nil, movements, FIFO{})
	if got, want := totalValueOf(positions), dec(t, "1200"); !got.Equal(want) {
		t.Fatalf("goods bought from a supplier are carried at %s instead of %s: the conservation rule has "+
			"escaped the Stock Journal and is now valuing purchases against an outward leg that does not exist, "+
			"so nothing the company buys can be brought onto the books",
			got.StringFixed(4), want.StringFixed(4))
	}
}

// TestAnInwardOnlyStockJournalKeepsItsKeyedRate. ⚠️ A Stock Journal with no
// outward leg is a Receipt Note keyed into the wrong screen: there is no
// consumption to derive anything from, so the keyed rate stands. Pinned so the
// absence of a rule is a decision on the record rather than a gap somebody
// closes by carrying the goods at nothing.
func TestAnInwardOnlyStockJournalKeepsItsKeyedRate(t *testing.T) {
	movements := voucher(t, "2026-04-23", "SJ-1", true,
		leg{"widget", "main", DirectionIn, "4", "50"})
	positions := ReplayAll(nil, movements, FIFO{})
	if got, want := totalValueOf(positions), dec(t, "200"); !got.Equal(want) {
		t.Fatalf("an inward-only Stock Journal carries %s instead of the %s it was keyed at: with no outward "+
			"leg there is nothing to derive a cost from, and carrying it at nothing would write real goods "+
			"onto the shelf for free", got.StringFixed(4), want.StringFixed(4))
	}
}

// --------------------------------------------------------------------------
// ⛔ THE ALLOCATION — why voucher-level pooling alone is NOT the rule
// --------------------------------------------------------------------------

// TestTwoTransfersOnOneVoucherDoNotLeakValueBetweenItems is the arm that
// rejects the obvious rule.
//
// ⛔ "TOTAL INWARD == TOTAL OUTWARD, POOLED ACROSS THE VOUCHER" is the right
// INVARIANT and the wrong ALLOCATION. The Stock Journal screen emits one OUT/IN
// pair per KEYED ROW and a voucher may carry many rows, of different items — so
// a pooled fix would let a cheap item absorb an expensive one's cost while the
// voucher total still conserved, and the invariant would certify the
// corruption. Each item must match ITSELF first.
func TestTwoTransfersOnOneVoucherDoNotLeakValueBetweenItems(t *testing.T) {
	movements := joinMovements(
		voucher(t, "2026-04-10", "RN-1", false,
			leg{"cheap", "main", DirectionIn, "10", "10"},   // 100.00
			leg{"pricey", "main", DirectionIn, "10", "500"}, // 5,000.00
		),
		// One document, two ordinary transfers — exactly what two keyed rows do.
		voucher(t, "2026-04-23", "SJ-1", true,
			leg{"cheap", "main", DirectionOut, "10", "10"},
			leg{"cheap", "second", DirectionIn, "10", "10"},
			leg{"pricey", "main", DirectionOut, "10", "500"},
			leg{"pricey", "second", DirectionIn, "10", "500"},
		),
	)
	positions := ReplayAll(nil, movements, FIFO{})

	assertConserved(t, "two transfers on one voucher", dec(t, "5100"), totalValueOf(positions))

	if got, want := valueOf(t, positions, "cheap", "second"), dec(t, "100"); !got.Equal(want) {
		t.Fatalf("the cheap item arrives at the second store carrying %s instead of %s: the expensive item's "+
			"cost has leaked onto it because the voucher was pooled instead of matched item by item — the "+
			"company total still foots, so nothing anywhere would report the corruption",
			got.StringFixed(4), want.StringFixed(4))
	}
	if got, want := valueOf(t, positions, "pricey", "second"), dec(t, "5000"); !got.Equal(want) {
		t.Fatalf("the expensive item arrives carrying %s instead of %s: value has leaked between two "+
			"unrelated items on one voucher", got.StringFixed(4), want.StringFixed(4))
	}
}

// --------------------------------------------------------------------------
// The MANUFACTURE shape — the other half of the class
// --------------------------------------------------------------------------

// perItem is the valuer selector a real report builds from
// stock_item.valuation_method: a manufacture consumes and produces DIFFERENT
// items, which may be on different costing methods.
func perItem(t *testing.T, byItem map[string]Valuer) func(string) (Valuer, error) {
	t.Helper()
	return func(item string) (Valuer, error) {
		if v, ok := byItem[item]; ok {
			return v, nil
		}
		return nil, ErrUnknownValuation
	}
}

// TestAManufactureCarriesTheFinishedGoodAtWhatItsComponentsCost.
//
// ⭐ WHY A MANUFACTURE CONSERVES TOO, EVEN THOUGH MANUFACTURING IS EXACTLY THE
// EVENT THAT CHANGES PER-ITEM VALUE. It changes per-ITEM value freely — that is
// what the arm below shows, two components going to zero and one finished good
// appearing — but it cannot change the TOTAL, because a Stock Journal posts NO
// ACCOUNTING LINES. Conversion cost is real, and when this build can state it
// (an additional-cost ledger allocation) the rule extends to "consumption plus
// what somebody posted". Until then there is nothing to fund an increase with,
// and a finished good worth more than its inputs is unbacked value on the
// Balance Sheet under a document with no money side.
//
// The components are on DIFFERENT costing methods on purpose: this can only
// work if every position is replayed in ONE coupled pass with a per-item
// valuer, which is what ReplayCompany is for.
func TestAManufactureCarriesTheFinishedGoodAtWhatItsComponentsCost(t *testing.T) {
	valuers := perItem(t, map[string]Valuer{
		"steel": FIFO{}, "paint": WeightedAverage{}, "chair": FIFO{},
	})
	movements := joinMovements(
		voucher(t, "2026-04-01", "RN-1", false, leg{"steel", "main", DirectionIn, "10", "100"}), // 1,000
		voucher(t, "2026-04-02", "RN-2", false, leg{"steel", "main", DirectionIn, "10", "150"}), // 1,500
		voucher(t, "2026-04-03", "RN-3", false, leg{"paint", "main", DirectionIn, "20", "25"}),  // 500
		// Consume 10 steel (FIFO: the 100 layer, 1,000) and 4 paint (avg 25, 100),
		// producing 2 chairs. The keyed rate on the output is a guess and must not
		// be what the chairs end up carried at.
		voucher(t, "2026-04-20", "SJ-1", true,
			leg{"steel", "main", DirectionOut, "10", "125"},
			leg{"paint", "main", DirectionOut, "4", "25"},
			leg{"chair", "main", DirectionIn, "2", "900"},
		),
	)

	positions, err := ReplayCompany(nil, movements, valuers)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}

	assertConserved(t, "a manufacture", dec(t, "3000"), totalValueOf(positions))

	if got, want := valueOf(t, positions, "chair", "main"), dec(t, "1100"); !got.Equal(want) {
		t.Fatalf("the finished goods are carried at %s but the components that went into them cost %s "+
			"(1,000.00 of steel off the oldest FIFO layer plus 100.00 of paint at its average). A Stock "+
			"Journal posts no accounting lines, so the difference is stock value with nothing on the "+
			"other side of it, and every chair sold will report the wrong gross profit",
			got.StringFixed(4), want.StringFixed(4))
	}
	// PER-ITEM VALUE MUST STILL MOVE FREELY — that is what manufacturing IS, and
	// a rule that froze it would have refused the document.
	if got := valueOf(t, positions, "paint", "main"); !got.Equal(dec(t, "400")) {
		t.Errorf("the paint position is %s, want 400.0000: a manufacture must be able to change what an "+
			"individual item is worth", got.StringFixed(4))
	}
}

// TestAManufactureSplitsTheConsumedCostBetweenItsOutputsByTheKeyedRatio.
//
// ⭐ THIS IS WHAT THE KEYED RATE STILL MEANS. It is no longer a price — the
// total is fixed by what was consumed — but on a voucher producing more than
// one thing it is the operator's statement of how that total DIVIDES: a product
// and its by-product are not worth the same per unit, and nobody but the
// operator can say what the split is. On a plain transfer there is one inward
// leg per item, the ratio is 1, and the number has no effect at all.
// TestAThreeWayEvenSplitLosesNoPaisaToRounding pins the LAST-LEG REMAINDER.
//
// ⛔ THIS TEST EXISTS BECAUSE A MUTATION SURVIVED. The independent proof pass for
// D103 replaced allocate()'s `out[len(idx)-1] = total.Sub(running)` with a plain
// rounded pro-rata share and THE WHOLE SUITE STAYED GREEN — including
// TestAManufactureSplitsTheConsumedCostBetweenItsOutputsByTheKeyedRatio, which
// was believed to pin exactness. It does not: its 2400/2600 + 200/2600 split
// happens to round to 923.0769 + 76.9231 = 1000.0000 either way.
//
// ⭐ A THREE-WAY EVEN SPLIT IS THE SMALLEST CASE THAT CANNOT ROUND CLEANLY.
// 1,000.00 into three equal parts is 333.3333... — a plain rounded share gives
// three legs of 333.3333 totalling 999.9999 and leaks 0.0001 PER VOUCHER into
// nothing. That is the same class of defect as D103 itself, four decimal places
// down: quantity unchanged, nothing counting units can see it, and it reaches
// the Balance Sheet.
func TestAThreeWayEvenSplitLosesNoPaisaToRounding(t *testing.T) {
	valuers := perItem(t, map[string]Valuer{
		"blend": FIFO{}, "a": FIFO{}, "b": FIFO{}, "c": FIFO{},
	})
	movements := joinMovements(
		voucher(t, "2026-04-01", "RN-1", false, leg{"blend", "main", DirectionIn, "3", "333.3333333"}),
		// One lot worth exactly 1,000.00 split into three outputs keyed
		// IDENTICALLY, so the true share is 333.33333... on every leg and no
		// rounding of the share alone can add back to the whole.
		voucher(t, "2026-04-20", "SJ-1", true,
			leg{"blend", "main", DirectionOut, "3", "333.3333333"},
			leg{"a", "main", DirectionIn, "1", "100"},
			leg{"b", "main", DirectionIn, "1", "100"},
			leg{"c", "main", DirectionIn, "1", "100"},
		),
	)
	positions, err := ReplayCompany(nil, movements, valuers)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}

	// ⛔ THE ASSERTION IS THE TOTAL, TO THE LAST DECIMAL THE SCHEMA CARRIES.
	// Per-leg equality would pass under the mutant; only the sum catches it.
	got := totalValueOf(positions)
	want := dec(t, "1000")
	if !got.Equal(want) {
		t.Errorf("a three-way even split carried %s, want %s — a difference of %s was lost to "+
			"rounding on a voucher that moved goods between our own shelves. allocate() must give "+
			"the LAST leg the remainder rather than its own rounded share, or every such voucher "+
			"leaks a fraction into nothing and the Balance Sheet drifts with no document behind it",
			got.StringFixed(4), want.StringFixed(4), want.Sub(got).StringFixed(6))
	}
}

func TestAManufactureSplitsTheConsumedCostBetweenItsOutputsByTheKeyedRatio(t *testing.T) {
	valuers := perItem(t, map[string]Valuer{
		"log": FIFO{}, "plank": FIFO{}, "sawdust": FIFO{},
	})
	movements := joinMovements(
		voucher(t, "2026-04-01", "RN-1", false, leg{"log", "main", DirectionIn, "10", "100"}),
		// 1,000.00 of logs become 8 planks and 2 sacks of sawdust, keyed 3:1 per
		// unit — so 24 parts to the planks and 2 to the sawdust out of 26.
		voucher(t, "2026-04-20", "SJ-1", true,
			leg{"log", "main", DirectionOut, "10", "100"},
			leg{"plank", "main", DirectionIn, "8", "300"},
			leg{"sawdust", "main", DirectionIn, "2", "100"},
		),
	)
	positions, err := ReplayCompany(nil, movements, valuers)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}

	assertConserved(t, "a manufacture with a by-product", dec(t, "1000"), totalValueOf(positions))

	// 2,400 / 2,600 of 1,000.00 = 923.0769; the remainder lands on the last leg.
	if got, want := valueOf(t, positions, "plank", "main"), dec(t, "923.0769"); !got.Equal(want) {
		t.Errorf("the planks carry %s, want %s — the keyed rates say the planks are worth three times a "+
			"sack of sawdust per unit, and that ratio is the only thing that can decide the split",
			got.StringFixed(4), want.StringFixed(4))
	}
	if got, want := valueOf(t, positions, "sawdust", "main"), dec(t, "76.9231"); !got.Equal(want) {
		t.Errorf("the sawdust carries %s, want %s — the LAST leg takes the rounding remainder so the two "+
			"outputs sum to the consumption EXACTLY; a share lost here is a Balance Sheet that drifts a "+
			"paisa per voucher forever", got.StringFixed(4), want.StringFixed(4))
	}
}

// TestAReworkThatConsumesAComponentIntoItsOwnOutputConservesTheWholeVoucher
// covers the residue path: every inward leg already carries its OWN item's
// consumption, and there is still consumption left over from an item this
// voucher ate but did not produce. It belongs on the goods that were produced —
// anywhere else and the voucher quietly destroys it.
func TestAReworkThatConsumesAComponentIntoItsOwnOutputConservesTheWholeVoucher(t *testing.T) {
	valuers := perItem(t, map[string]Valuer{"frame": FIFO{}, "bolt": FIFO{}})
	movements := joinMovements(
		voucher(t, "2026-04-01", "RN-1", false, leg{"frame", "main", DirectionIn, "10", "100"}),
		voucher(t, "2026-04-02", "RN-2", false, leg{"bolt", "main", DirectionIn, "100", "5"}),
		// Strip 4 frames (400.00) and 20 bolts (100.00), rebuild 3 frames.
		voucher(t, "2026-04-20", "SJ-1", true,
			leg{"frame", "main", DirectionOut, "4", "100"},
			leg{"bolt", "main", DirectionOut, "20", "5"},
			leg{"frame", "main", DirectionIn, "3", "160"},
		),
	)
	positions, err := ReplayCompany(nil, movements, valuers)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	assertConserved(t, "a rework that consumes a component into an item it also produces",
		dec(t, "1500"), totalValueOf(positions))
	// 600 left in frames (6 untouched) + 400 + 100 rebuilt onto 3 units.
	if got, want := valueOf(t, positions, "frame", "main"), dec(t, "1100"); !got.Equal(want) {
		t.Fatalf("the frames carry %s, want %s: the bolts were eaten by this voucher and produced nothing "+
			"of their own, so their cost has to land on the goods that WERE produced or it disappears",
			got.StringFixed(4), want.StringFixed(4))
	}
}

// TestAStockJournalWithNoInwardLegIsAWriteOff. The mirror of the inward-only
// case: consumption with nothing to carry it into is goods leaving the company,
// and the value leaves with them. Pinned so the conservation rule is not
// mistaken for "a Stock Journal can never change the total".
func TestAStockJournalWithNoInwardLegIsAWriteOff(t *testing.T) {
	movements := joinMovements(
		voucher(t, "2026-04-01", "RN-1", false, leg{"widget", "main", DirectionIn, "10", "100"}),
		voucher(t, "2026-04-20", "SJ-1", true, leg{"widget", "main", DirectionOut, "4", "0"}),
	)
	positions := ReplayAll(nil, movements, FIFO{})
	if got, want := totalValueOf(positions), dec(t, "600"); !got.Equal(want) {
		t.Fatalf("a Stock Journal that issued 4 units and brought nothing back leaves %s on the books "+
			"instead of %s: the goods are gone and their cost has to go with them",
			got.StringFixed(4), want.StringFixed(4))
	}
}

// TestConservationSurvivesABackDatedPurchase is why the fix CANNOT live in the
// post path.
//
// ⛔ VALUE IS NEVER STORED. A purchase keyed AFTER the transfer but DATED BEFORE
// it changes which layer the transfer consumed, and therefore what the inward
// leg must be carried at. A rate derived once at post time and written into the
// movement row would be right on the day and silently wrong from the moment
// anybody keys a late invoice — the same defect one layer down, and much harder
// to see.
func TestConservationSurvivesABackDatedPurchase(t *testing.T) {
	base := joinMovements(
		twoLayers(t, "widget"),
		voucher(t, "2026-04-23", "SJ-1", true,
			leg{"widget", "main", DirectionOut, "10", "123.33"},
			leg{"widget", "second", DirectionIn, "10", "123.33"},
		),
	)
	// Keyed later, dated FIRST: 8 @ 50.00 now sits at the head of the queue, so
	// the transfer eats 8 @ 50 + 2 @ 120 = 640.00 instead of 1,200.00.
	backDated := joinMovements(base,
		voucher(t, "2026-04-05", "RN-0", false, leg{"widget", "main", DirectionIn, "8", "50"}))

	positions := ReplayAll(nil, backDated, FIFO{})
	assertConserved(t, "a FIFO transfer after a back-dated purchase re-priced it",
		dec(t, "2250"), totalValueOf(positions))
	if got, want := valueOf(t, positions, "widget", "second"), dec(t, "640"); !got.Equal(want) {
		t.Fatalf("the transferred goods are carried at %s, want %s: the back-dated purchase changed which "+
			"layers the transfer consumed, and any figure frozen at post time would now be wrong with "+
			"nothing in the product able to say so", got.StringFixed(4), want.StringFixed(4))
	}
}

// TestTheConservationIdentityHoldsOnEveryTransferredPosition. Position's doc
// states opening + inward - outward == closing, and the Stock Summary prints
// all four columns. A fix that adjusted the closing value without adjusting the
// INWARD column would balance the company total while making every transfer's
// own row fail to add up on screen.
func TestTheConservationIdentityHoldsOnEveryTransferredPosition(t *testing.T) {
	movements := joinMovements(
		twoLayers(t, "widget"),
		voucher(t, "2026-04-23", "SJ-1", true,
			leg{"widget", "main", DirectionOut, "10", "123.33"},
			leg{"widget", "second", DirectionIn, "10", "123.33"},
		),
	)
	for key, p := range ReplayAll(nil, movements, FIFO{}) {
		want := p.OpeningValue.Add(p.InwardValue).Sub(p.OutwardValue)
		if !want.Equal(p.ClosingValue) {
			t.Errorf("position %+v prints opening %s + inward %s - outward %s = %s but closes at %s: "+
				"the Stock Summary row does not add up to its own total, so an operator reading the "+
				"screen cannot reconcile it to anything", key,
				p.OpeningValue.StringFixed(4), p.InwardValue.StringFixed(4),
				p.OutwardValue.StringFixed(4), want.StringFixed(4), p.ClosingValue.StringFixed(4))
		}
	}
}

// TestTheConservationFlagIsWhatDoesIt reproduces the DEFECT, to the paisa, by
// clearing one boolean.
//
// ⛔ IT ASSERTS BROKEN ARITHMETIC ON PURPOSE, and it is the only test here that
// does. Everything above proves the fixed path conserves; none of them can
// prove the fix is what makes it conserve, or that the switch is still wired.
// Movement.VoucherConserves is set from a STRING joined out of
// voucher_type.parent_class in one line of SQL, with no compiler anywhere on
// that seam — so the failure mode is not "the arithmetic broke", it is "the
// flag stopped arriving and nothing noticed". This arm pins the exact figure
// the browser measured on 2026-08-27 (1,850.00 -> 1,883.30, a delta of 33.30
// on a Balance Sheet that still footed), so a reader can see what the flag is
// worth.
func TestTheConservationFlagIsWhatDoesIt(t *testing.T) {
	legs := []leg{
		{"widget", "main", DirectionOut, "10", "123.33"},
		{"widget", "second", DirectionIn, "10", "123.33"},
	}
	unflagged := joinMovements(twoLayers(t, "widget"),
		voucher(t, "2026-04-23", "SJ-1", false, legs...))
	flagged := joinMovements(twoLayers(t, "widget"),
		voucher(t, "2026-04-23", "SJ-1", true, legs...))

	// Without the flag the inward leg carries the keyed blend, 10 x 123.33.
	if got, want := totalValueOf(ReplayAll(nil, unflagged, FIFO{})), dec(t, "1883.30"); !got.Equal(want) {
		t.Fatalf("the UNCONSERVED transfer totals %s, not the %s measured in a browser: this arm no "+
			"longer reproduces D103, so the arm below is no longer evidence that the flag is what "+
			"prevents it", got.StringFixed(4), want.StringFixed(4))
	}
	if got, want := totalValueOf(ReplayAll(nil, flagged, FIFO{})), dec(t, "1850"); !got.Equal(want) {
		t.Fatalf("with VoucherConserves set the same document totals %s, want %s: the flag is no longer "+
			"the switch, so whatever sets it in the report can be deleted without any test failing",
			got.StringFixed(4), want.StringFixed(4))
	}
}
