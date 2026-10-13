package inventory

import (
	"testing"

	"github.com/shopspring/decimal"
)

// lotsOf is a test helper: the (qty, value) pairs of a state's layers, oldest
// first, as fixed-4 strings so a failure reads as money rather than as structs.
func lotsOf(s ValuationState) [][2]string {
	out := make([][2]string, 0, len(s.Lots))
	for _, l := range s.Lots {
		out = append(out, [2]string{l.Qty.StringFixed(4), l.Value.StringFixed(4)})
	}
	return out
}

// assertLotInvariant is the one thing a lot queue must never break: the layers
// must add up to the position. A queue that drifts from Qty/Value reports a
// closing stock the Stock Summary and the Balance Sheet both believe and that
// no receipt or issue accounts for.
func assertLotInvariant(t *testing.T, why string, s ValuationState) {
	t.Helper()
	if len(s.Lots) == 0 {
		return
	}
	qty, value := decimal.Zero, decimal.Zero
	for _, l := range s.Lots {
		if !l.Qty.IsPositive() {
			t.Errorf("%s: a layer holds %s units — a lot is a POSITIVE quantity that arrived together; a zero or negative layer is a position the queue can never issue out of", why, l.Qty.StringFixed(4))
		}
		qty = qty.Add(l.Qty)
		value = value.Add(l.Value)
	}
	if !qty.Equal(s.Qty) {
		t.Errorf("%s: the layers hold %s units but the position says %s — the stock summary would report one and the drill-down the other", why, qty.StringFixed(4), s.Qty.StringFixed(4))
	}
	if !value.Equal(s.Value) {
		t.Errorf("%s: the layers hold %s but the position is carried at %s — %s of stock value exists in one of the two and not the other", why, value.StringFixed(4), s.Value.StringFixed(4), value.Sub(s.Value).StringFixed(4))
	}
}

// TestFIFOAndLIFOEatOppositeEndsOfTheQueue is the arithmetic of the two methods
// on ONE state, side by side: the same two layers and the same issue, and the
// cost of goods sold differs by the whole spread between the two purchases.
func TestFIFOAndLIFOEatOppositeEndsOfTheQueue(t *testing.T) {
	state := ValuationState{
		Qty:   dec(t, "20"),
		Value: dec(t, "2200"),
		Lots: []Lot{
			{Qty: dec(t, "10"), Value: dec(t, "1000")}, // bought at 100
			{Qty: dec(t, "10"), Value: dec(t, "1200")}, // bought at 120
		},
	}
	issue := mv(t, "2026-04-05", "SAL-1", 1, DirectionOut, "15", "500")

	fifoNext, fifoConsumed := (FIFO{}).Apply(state, issue)
	if want := dec(t, "1600"); !fifoConsumed.Equal(want) {
		t.Errorf("FIFO consumed %s, want %s — the oldest layer must go first, whole, before the newer one is touched", fifoConsumed.StringFixed(4), want.StringFixed(4))
	}
	if got := lotsOf(fifoNext); len(got) != 1 || got[0] != [2]string{"5.0000", "600.0000"} {
		t.Errorf("FIFO left the queue as %v, want one layer of 5.0000 at 600.0000 (what remains of the 120.0000 purchase)", got)
	}
	assertLotInvariant(t, "FIFO after a 15-unit issue", fifoNext)

	lifoNext, lifoConsumed := (LIFO{}).Apply(state, issue)
	if want := dec(t, "1700"); !lifoConsumed.Equal(want) {
		t.Errorf("LIFO consumed %s, want %s — the newest layer must go first", lifoConsumed.StringFixed(4), want.StringFixed(4))
	}
	if got := lotsOf(lifoNext); len(got) != 1 || got[0] != [2]string{"5.0000", "500.0000"} {
		t.Errorf("LIFO left the queue as %v, want one layer of 5.0000 at 500.0000 (what remains of the 100.0000 purchase)", got)
	}
	assertLotInvariant(t, "LIFO after a 15-unit issue", lifoNext)

	if fifoConsumed.Equal(lifoConsumed) {
		t.Fatal("FIFO and LIFO consumed the same value from the same layers — one of the two is not reading the queue from the end it says it does, and every gross-profit figure on the item would be the other method's")
	}
}

// TestLIFOKeepsSurvivingLayersInArrivalOrder pins the part of a backwards walk
// that is easy to get wrong: LIFO chooses from the NEW end, but the layers it
// does not touch must stay in ARRIVAL order, or the next issue eats them in the
// wrong sequence and the error compounds for the life of the item.
func TestLIFOKeepsSurvivingLayersInArrivalOrder(t *testing.T) {
	state := ValuationState{
		Qty:   dec(t, "30"),
		Value: dec(t, "3300"),
		Lots: []Lot{
			{Qty: dec(t, "10"), Value: dec(t, "1000")}, // oldest, at 100
			{Qty: dec(t, "10"), Value: dec(t, "1100")}, // at 110
			{Qty: dec(t, "10"), Value: dec(t, "1200")}, // newest, at 120
		},
	}
	// Take 12: all of the newest layer, then 2 out of the middle one.
	next, consumed := (LIFO{}).Apply(state, mv(t, "2026-04-06", "SAL-2", 1, DirectionOut, "12", "500"))
	if want := dec(t, "1420"); !consumed.Equal(want) {
		t.Errorf("LIFO consumed %s, want %s (1200.0000 + 2 * 110.0000)", consumed.StringFixed(4), want.StringFixed(4))
	}
	got := lotsOf(next)
	want := [][2]string{{"10.0000", "1000.0000"}, {"8.0000", "880.0000"}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("surviving layers are %v, want %v — oldest FIRST. Reversed, the next issue would eat the 100.0000 stock before the 110.0000 stock and LIFO would silently become FIFO for the tail of this item", got, want)
	}
	assertLotInvariant(t, "LIFO after a partial middle-layer issue", next)
}

// TestALotIsFullyConsumedWithNoResidue is the lot-level statement of the rule
// WeightedAverage's Apply makes for a whole position: three issues of one unit
// out of a layer of three bought for 1000.0000 must consume exactly 1000.0000.
// A layer carrying a 4-dp RATE of 333.3333 would consume 999.9999 and leave a
// layer holding 0.0000 units worth 0.0001 — which the next issue cannot reach
// and no report can explain.
func TestALotIsFullyConsumedWithNoResidue(t *testing.T) {
	state := ValuationState{
		Qty:   dec(t, "3"),
		Value: dec(t, "1000"),
		Lots:  []Lot{{Qty: dec(t, "3"), Value: dec(t, "1000")}},
	}
	total := decimal.Zero
	for i := 0; i < 3; i++ {
		next, consumed := (FIFO{}).Apply(state, mv(t, "2026-04-07", "SAL-3", i+1, DirectionOut, "1", "500"))
		total = total.Add(consumed)
		assertLotInvariant(t, "FIFO mid-layer", next)
		state = next
	}
	if want := dec(t, "1000"); !total.Equal(want) {
		t.Errorf("three single-unit issues out of a 1000.0000 layer consumed %s in total, want %s", total.StringFixed(4), want.StringFixed(4))
	}
	if !state.Qty.IsZero() || !state.Value.IsZero() {
		t.Errorf("the emptied position carries %s units at %s — a position holding nothing must be worth nothing, and the next receipt would blend this residue into its cost", state.Qty.StringFixed(4), state.Value.StringFixed(4))
	}
	if len(state.Lots) != 0 {
		t.Errorf("the emptied position still has %d layer(s): %v", len(state.Lots), lotsOf(state))
	}
}

// TestAnIssueBeyondTheQueueInventsNoCost is the negative-stock rule, which is
// reachable in production: company.allow_negative_stock is a switchable setting
// (register rows D5/D43). The rule is the one WeightedAverage states — stock
// that was never received has no cost to consume — and it must not be satisfied
// by helpfully valuing the shortfall at the SALE rate, which is the single most
// common inventory-valuation bug and silently zeroes gross profit.
func TestAnIssueBeyondTheQueueInventsNoCost(t *testing.T) {
	for _, v := range []Valuer{FIFO{}, LIFO{}} {
		t.Run(v.Method(), func(t *testing.T) {
			state := ValuationState{
				Qty:   dec(t, "4"),
				Value: dec(t, "400"),
				Lots:  []Lot{{Qty: dec(t, "4"), Value: dec(t, "400")}},
			}
			next, consumed := v.Apply(state, mv(t, "2026-04-08", "SAL-4", 1, DirectionOut, "10", "900"))
			if want := dec(t, "400"); !consumed.Equal(want) {
				t.Errorf("consumed %s for 10 units when only 4 had ever been received; the cost of goods sold may only ever be the %s that was actually there", consumed.StringFixed(4), want.StringFixed(4))
			}
			if want := dec(t, "-6"); !next.Qty.Equal(want) {
				t.Errorf("closing quantity %s, want %s — the shortfall is REPORTED, not clamped: a negative position is how an operator finds the missing purchase", next.Qty.StringFixed(4), want.StringFixed(4))
			}
			if !next.Value.IsZero() {
				t.Errorf("a position holding -6 units is carried at %s; there is no stock to carry a value", next.Value.StringFixed(4))
			}
			if len(next.Lots) != 0 {
				t.Errorf("a negative position still has layers %v — FIFO cannot say which layer a unit that was never received came from", lotsOf(next))
			}

			// The receipt that finally arrives rebuilds ONE blended layer. This
			// is the documented cost of turning negative stock on: the layer
			// history is destroyed and cannot be recovered, so the figures for
			// this item stop being FIFO figures until the position is flat.
			back, zero := v.Apply(next, mv(t, "2026-04-09", "PUR-9", 1, DirectionIn, "10", "150"))
			if !zero.IsZero() {
				t.Errorf("a receipt consumed %s — an inward consumes nothing, ever", zero.StringFixed(4))
			}
			if want := dec(t, "4"); !back.Qty.Equal(want) {
				t.Errorf("after a 10-unit receipt into a -6 position the closing quantity is %s, want %s", back.Qty.StringFixed(4), want.StringFixed(4))
			}
			if want := dec(t, "1500"); !back.Value.Equal(want) {
				t.Errorf("value %s, want %s — the whole receipt's money stays on the position; it is the QUANTITY that was short, and the money follows the money", back.Value.StringFixed(4), want.StringFixed(4))
			}
			if got := lotsOf(back); len(got) != 1 || got[0] != [2]string{"4.0000", "1500.0000"} {
				t.Errorf("the rebuilt queue is %v, want one blended layer of 4.0000 at 1500.0000", got)
			}
			assertLotInvariant(t, "after a receipt into a negative position", back)
		})
	}
}

// TestApplyDoesNotMutateTheStateItWasGiven is a purity test, and it is here
// because the obvious one-line way to write the inward arm —
// append(state.Lots, newLot) — passes every arithmetic test above and is a
// defect: append may write THROUGH the caller's slice, so two reports replaying
// the same position (or a caller holding a state to compare a what-if against)
// would see each other's layers. The Valuer doc requires purity; this is what
// purity means when the state stopped being two scalars.
func TestApplyDoesNotMutateTheStateItWasGiven(t *testing.T) {
	for _, v := range []Valuer{FIFO{}, LIFO{}} {
		t.Run(v.Method(), func(t *testing.T) {
			// Capacity deliberately exceeds the length: this is exactly the
			// slice an append will write into rather than copy.
			lots := make([]Lot, 1, 4)
			lots[0] = Lot{Qty: dec(t, "10"), Value: dec(t, "1000")}
			state := ValuationState{Qty: dec(t, "10"), Value: dec(t, "1000"), Lots: lots}

			receipt := mv(t, "2026-04-10", "PUR-A", 1, DirectionIn, "5", "200")
			firstNext, _ := v.Apply(state, receipt)

			// A second, DIFFERENT receipt folded into the SAME state. If Apply
			// wrote through, this call would have overwritten the first result.
			if _, _ = v.Apply(state, mv(t, "2026-04-10", "PUR-B", 1, DirectionIn, "5", "999")); true {
				if got := lotsOf(firstNext); len(got) != 2 || got[1] != [2]string{"5.0000", "1000.0000"} {
					t.Fatalf("the first replay's layers became %v after a second, unrelated replay of the same position — one report would print the other report's stock", got)
				}
			}
			if len(state.Lots) != 1 {
				t.Fatalf("Apply grew the caller's own layer list to %d — the state a caller holds must be immutable", len(state.Lots))
			}

			issue := mv(t, "2026-04-11", "SAL-A", 1, DirectionOut, "4", "500")
			beforeQty := state.Lots[0].Qty
			if _, _ = v.Apply(state, issue); !state.Lots[0].Qty.Equal(beforeQty) {
				t.Fatalf("an issue shrank the caller's layer from %s to %s in place", beforeQty.StringFixed(4), state.Lots[0].Qty.StringFixed(4))
			}
		})
	}
}

// TestOneValuerReplaysManyPositionsWithoutBlendingTheirLayers is the defect the
// package doc in lots.go warns about, made executable.
//
// ⛔ ReplayAll hands ONE valuer to EVERY position. The design note that
// predicted FIFO said the valuer would "carry its own lot queue", and a valuer
// that did would have blended the layers of the same item in two godowns —
// silently, with both closing QUANTITIES still perfectly correct, so nothing
// but the value would be wrong and nothing would be out of balance.
func TestOneValuerReplaysManyPositionsWithoutBlendingTheirLayers(t *testing.T) {
	item := "widget"
	delhi := PositionKey{StockItemID: item, GodownID: "delhi"}
	mumbai := PositionKey{StockItemID: item, GodownID: "mumbai"}

	at := func(key PositionKey, date, no string, dir Direction, qty, rate string) Movement {
		m := mv(t, date, no, 1, dir, qty, rate)
		m.Position = key
		m.VoucherID = no
		return m
	}

	// Delhi buys cheap then dear; Mumbai buys dear then cheap. Each then issues
	// 10. Under FIFO each must consume ITS OWN oldest layer.
	movements := []Movement{
		at(delhi, "2026-04-01", "PUR-D1", DirectionIn, "10", "100"),
		at(delhi, "2026-04-02", "PUR-D2", DirectionIn, "10", "300"),
		at(mumbai, "2026-04-03", "PUR-M1", DirectionIn, "10", "300"),
		at(mumbai, "2026-04-04", "PUR-M2", DirectionIn, "10", "100"),
		at(delhi, "2026-04-05", "SAL-D1", DirectionOut, "10", "999"),
		at(mumbai, "2026-04-06", "SAL-M1", DirectionOut, "10", "999"),
	}

	got := ReplayAll(nil, movements, FIFO{})
	if len(got) != 2 {
		t.Fatalf("replayed %d positions, want 2 — the same item in two godowns is two positions", len(got))
	}
	for _, c := range []struct {
		key      PositionKey
		consumed string
		closing  string
		why      string
	}{
		{delhi, "1000.0000", "3000.0000", "Delhi bought at 100.0000 first, so Delhi's issue costs 1000.0000 and Delhi keeps the 300.0000 stock"},
		{mumbai, "3000.0000", "1000.0000", "Mumbai bought at 300.0000 first, so Mumbai's issue costs 3000.0000 and Mumbai keeps the 100.0000 stock"},
	} {
		pos := got[c.key]
		if want := dec(t, c.consumed); !pos.OutwardValue.Equal(want) {
			t.Errorf("%v consumed %s, want %s — %s", c.key, pos.OutwardValue.StringFixed(4), want.StringFixed(4), c.why)
		}
		if want := dec(t, c.closing); !pos.ClosingValue.Equal(want) {
			t.Errorf("%v closes at %s, want %s — %s", c.key, pos.ClosingValue.StringFixed(4), want.StringFixed(4), c.why)
		}
	}
	// Blending the two queues would have made both positions consume the same
	// 2000.0000 and both close at 2000.0000, which is what "both quantities are
	// right and both values are wrong" looks like.
	if got[delhi].OutwardValue.Equal(got[mumbai].OutwardValue) {
		t.Fatal("both godowns consumed the same value from opposite purchase histories — the layers are shared across positions")
	}
}
