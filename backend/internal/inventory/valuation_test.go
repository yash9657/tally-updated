package inventory

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func dec(t *testing.T, s string) decimal.Decimal {
	t.Helper()
	d, err := decimal.NewFromString(s)
	if err != nil {
		t.Fatalf("parse decimal %q: %v", s, err)
	}
	return d
}

// day returns a voucher date, the way the ledger passes one (a DATE at midnight).
func day(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parse date %q: %v", s, err)
	}
	return d
}

// mv builds a movement with a plausible ordering key so tests can concentrate
// on the arithmetic.
func mv(t *testing.T, date string, no string, line int, dir Direction, qty, rate string) Movement {
	t.Helper()
	return Movement{
		VoucherDate:      day(t, date),
		VoucherNo:        no,
		VersionCreatedAt: day(t, date).Add(9 * time.Hour),
		LineNo:           line,
		VoucherVersionID: no + "/v1",
		Position:         PositionKey{StockItemID: "widget"},
		Direction:        dir,
		Qty:              dec(t, qty),
		Rate:             dec(t, rate),
		DiscountPct:      decimal.Zero,
	}
}

func TestValuerForRejectsUnknownMethod(t *testing.T) {
	cases := []struct {
		method  string
		wantErr bool
	}{
		{MethodWeightedAverage, false},
		{"FIFO", true},
		{"LIFO", true},
		{"weighted_avg", true}, // the CHECK constraint is case-sensitive; so are we.
		{"", true},
	}
	for _, c := range cases {
		t.Run(c.method, func(t *testing.T) {
			v, err := ValuerFor(c.method)
			if c.wantErr {
				if err == nil {
					t.Fatalf("ValuerFor(%q) returned valuer %T with no error — an item whose valuation_method this build does not implement would be silently reported on the WRONG basis, and the number would look perfectly plausible", c.method, v)
				}
				if !errors.Is(err, ErrUnknownValuation) {
					t.Fatalf("ValuerFor(%q) failed with %v, which callers cannot distinguish from a real failure with errors.Is(ErrUnknownValuation)", c.method, err)
				}
				if v != nil {
					t.Fatalf("ValuerFor(%q) returned BOTH an error and a valuer %T — a caller that ignored the error would value stock with it", c.method, v)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValuerFor(%q): %v", c.method, err)
			}
			if v.Method() != c.method {
				t.Fatalf("valuer for %q reports its basis as %q; a report would print the wrong valuation basis", c.method, v.Method())
			}
		})
	}
}

func TestWeightedAverageApplyArithmetic(t *testing.T) {
	wa := WeightedAverage{}
	cases := []struct {
		name         string
		state        ValuationState
		movement     Movement
		wantQty      string
		wantValue    string
		wantConsumed string
		why          string
	}{
		{
			name:         "inward into an empty position sets the cost",
			state:        ValuationState{Qty: decimal.Zero, Value: decimal.Zero},
			movement:     mv(t, "2026-04-01", "PUR-1", 1, DirectionIn, "10", "100"),
			wantQty:      "10",
			wantValue:    "1000",
			wantConsumed: "0",
			why:          "the first purchase is the cost of the stock",
		},
		{
			name:         "inward at a new price re-prices every unit on hand",
			state:        ValuationState{Qty: dec(t, "10"), Value: dec(t, "1000")},
			movement:     mv(t, "2026-04-08", "PUR-2", 1, DirectionIn, "10", "120"),
			wantQty:      "20",
			wantValue:    "2200",
			wantConsumed: "0",
			why:          "20 units at an average of 110 — not 10 at 100 plus 10 at 120 kept apart",
		},
		{
			name:         "outward consumes at cost, never at the sale price",
			state:        ValuationState{Qty: dec(t, "20"), Value: dec(t, "2200")},
			movement:     mv(t, "2026-04-20", "SAL-1", 1, DirectionOut, "5", "300"),
			wantQty:      "15",
			wantValue:    "1650",
			wantConsumed: "550",
			why:          "consuming at the 300 sale rate would book 1500 of cost and zero the gross profit",
		},
		{
			name:         "outward that empties the position takes the whole value",
			state:        ValuationState{Qty: dec(t, "3"), Value: dec(t, "1000")},
			movement:     mv(t, "2026-04-21", "SAL-2", 1, DirectionOut, "3", "300"),
			wantQty:      "0",
			wantValue:    "0",
			wantConsumed: "1000",
			why:          "3 * round(1000/3) would leave 0.0001 of value on a position holding nothing",
		},
		{
			name:         "outward from an empty position consumes nothing",
			state:        ValuationState{Qty: decimal.Zero, Value: decimal.Zero},
			movement:     mv(t, "2026-04-22", "SAL-3", 1, DirectionOut, "2", "300"),
			wantQty:      "-2",
			wantValue:    "0",
			wantConsumed: "0",
			why:          "negative stock (the allow_negative_stock opt-in) has no cost to consume yet; it must not invent one from the sale rate",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, consumed := wa.Apply(c.state, c.movement)
			if !got.Qty.Equal(dec(t, c.wantQty)) {
				t.Errorf("quantity on hand became %s, but %s: expected %s", got.Qty.StringFixed(4), c.why, dec(t, c.wantQty).StringFixed(4))
			}
			if !got.Value.Equal(dec(t, c.wantValue)) {
				t.Errorf("stock value became %s, but %s: expected %s", got.Value.StringFixed(4), c.why, dec(t, c.wantValue).StringFixed(4))
			}
			if !consumed.Equal(dec(t, c.wantConsumed)) {
				t.Errorf("the movement was costed at %s, but %s: expected %s", consumed.StringFixed(4), c.why, dec(t, c.wantConsumed).StringFixed(4))
			}
		})
	}
}

// TestMovementValueIsNetOfDiscount pins the one definition of what a movement
// is worth. stock_movement.amount must equal it; if the two ever disagree, a
// discounted purchase enters stock at a cost the invoice never charged.
func TestMovementValueIsNetOfDiscount(t *testing.T) {
	cases := []struct {
		qty, rate, discount, want string
	}{
		{"10", "125", "0", "1250.0000"},
		{"10", "125", "4", "1200.0000"},
		{"3", "99.99", "10", "269.9730"},
		{"1", "0", "0", "0.0000"},
		{"7", "100", "100", "0.0000"}, // a 100% discount is a free sample, not a negative cost
	}
	for _, c := range cases {
		m := Movement{
			Qty:         dec(t, c.qty),
			Rate:        dec(t, c.rate),
			DiscountPct: dec(t, c.discount),
		}
		got := m.Value()
		if got.StringFixed(4) != c.want {
			t.Errorf("%s units at %s less %s%% enter stock at %s; the invoice charged %s", c.qty, c.rate, c.discount, got.StringFixed(4), c.want)
		}
	}
}

// TestStateRateIsRecomputedNotStored is the unit-level guard for the mutation
// that TestBackdatedInwardRevaluesLaterOutward catches at corpus level: the
// average cost must be a FUNCTION of the current state, so that changing the
// state changes it.
func TestStateRateIsRecomputedNotStored(t *testing.T) {
	first := ValuationState{Qty: dec(t, "10"), Value: dec(t, "1000")}
	if got := first.Rate(); !got.Equal(dec(t, "100")) {
		t.Fatalf("10 units costing 1000 average %s, not 100", got.String())
	}
	// The same position after an older purchase is folded in ahead of it.
	revalued := ValuationState{Qty: dec(t, "20"), Value: dec(t, "3000")}
	if got := revalued.Rate(); !got.Equal(dec(t, "150")) {
		t.Fatalf("20 units costing 3000 average %s, not 150", got.String())
	}
	if (ValuationState{}).Rate().IsZero() == false {
		t.Fatal("an empty position must average zero, not divide by zero")
	}
	// A recurring average keeps working precision, not the 4 dp of the column:
	// 1400/15 = 93.3333333333333333, and 6 * that rounds to 560.0000 rather
	// than the 559.9998 a 4-dp rate would produce.
	recurring := ValuationState{Qty: dec(t, "15"), Value: dec(t, "1400")}
	consumed := recurring.Rate().Mul(dec(t, "6")).Round(4)
	if !consumed.Equal(dec(t, "560")) {
		t.Fatalf("issuing 6 of 15 units costing 1400 consumed %s; rounding the average to the rate column's 4 dp before multiplying loses value on every issue (expected 560.0000)", consumed.StringFixed(4))
	}
}

// TestReplayDoesNotReorderCallerSlice guards a shared-slice aliasing bug: a
// report replays many positions from ONE slice read out of the database, so a
// Replay that sorted its argument in place would leave the caller's next call
// operating on a slice the previous call had shuffled.
func TestReplayDoesNotReorderCallerSlice(t *testing.T) {
	movements := []Movement{
		mv(t, "2026-04-20", "SAL-1", 1, DirectionOut, "5", "300"),
		mv(t, "2026-04-01", "PUR-1", 1, DirectionIn, "10", "100"),
		mv(t, "2026-04-08", "PUR-2", 1, DirectionIn, "10", "120"),
	}
	order := make([]string, len(movements))
	for i, m := range movements {
		order[i] = m.VoucherNo
	}

	Replay(Opening{Position: PositionKey{StockItemID: "widget"}}, movements, WeightedAverage{})

	for i, m := range movements {
		if m.VoucherNo != order[i] {
			t.Fatalf("Replay reordered the caller's slice (position %d is now %s, was %s); the next position replayed from the same slice would see shuffled input", i, m.VoucherNo, order[i])
		}
	}
}

// recordingValuer is a Valuer that folds nothing and remembers everything: it
// exposes the ORDER Replay applied movements in, which is otherwise invisible
// from a closing position (the final value of two receipts is the same either
// way round — it is the cost of the issue BETWEEN them that differs, and that
// is exactly the bug that hides).
type recordingValuer struct {
	seen *[]string
}

func (recordingValuer) Method() string { return "RECORDER" }

func (r recordingValuer) Apply(state ValuationState, m Movement) (ValuationState, decimal.Decimal) {
	*r.seen = append(*r.seen, m.VoucherVersionID)
	return state, decimal.Zero
}

// TestReplayOrdersByEveryKeyInTurn pins each level of the canonical replay key
// (voucher_date, voucher_id, version_created_at, line_no, voucher_version_id).
// Each case feeds two movements that differ at exactly ONE level in the WRONG
// order and demands that Replay put them right.
func TestReplayOrdersByEveryKeyInTurn(t *testing.T) {
	stamp := func(s string) time.Time {
		ts, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return ts
	}
	// The reference movement every case perturbs at one level only.
	ref := Movement{
		VoucherDate:      day(t, "2026-04-01"),
		VoucherNo:        "SAL-1",
		VoucherID:        "0195c000-0000-7000-8000-000000000001",
		VersionCreatedAt: stamp("2026-04-01T10:00:00Z"),
		LineNo:           1,
		VoucherVersionID: "aaa",
		Position:         PositionKey{StockItemID: "widget"},
		Direction:        DirectionIn,
		Qty:              dec(t, "1"),
		Rate:             dec(t, "100"),
	}

	cases := []struct {
		key     string
		perturb func(Movement) Movement
		why     string
	}{
		{
			key:     "voucher_date",
			perturb: func(m Movement) Movement { m.VoucherDate = day(t, "2026-04-02"); return m },
			why:     "a back-dated voucher must be re-valued into its own place in history, not appended",
		},
		{
			// ⛔ THIS SUBCASE READ `voucher_no` AND ITS `why` WAS FALSE (defect D8):
			// "two vouchers on one day replay in number order, the order the day's
			// book was written in" — a per-series counter is not the order the book
			// was written in, and a purchase number is the supplier's string. It is
			// INVERTED rather than deleted (§5.16): the key is the voucher's ID.
			key:     "voucher_id",
			perturb: func(m Movement) Movement { m.VoucherID = "0195c000-0000-7000-8000-00000000000f"; return m },
			why:     "two vouchers on one day replay in ENTRY order, and voucher_id is a UUIDv7 minted at CREATE — which the number is not",
		},
		{
			// ⛔⛔ AND THE OTHER HALF OF THE INVERSION, WHICH IS THE ARM THAT ACTUALLY
			// GUARDS D8. The perturbed movement is LATER by voucher_id and EARLIER by
			// voucher_no, so it can only be ordered correctly if the number is not
			// consulted above the id. Without this arm, re-adding a `voucher_no`
			// comparison above `voucher_id` would leave every other test in this file
			// green — which is exactly the state the defect shipped in.
			key: "voucher_no is NOT an ordering key",
			perturb: func(m Movement) Movement {
				m.VoucherNo = "AAA-0" // sorts BEFORE the reference's "SAL-1"
				m.VoucherID = "0195c000-0000-7000-8000-00000000000f"
				return m
			},
			why:     "a per-series number carries no ordering relationship to another series' number, and on a purchase it is the SUPPLIER's string — this is D8",
		},
		{
			key:     "version_created_at",
			perturb: func(m Movement) Movement { m.VersionCreatedAt = stamp("2026-04-05T10:00:00Z"); return m },
			why:     "an ALTER must land after the version it replaced, or the alteration would be undone by its own predecessor",
		},
		{
			key:     "line_no",
			perturb: func(m Movement) Movement { m.LineNo = 2; return m },
			why:     "lines of one invoice replay top to bottom, the order the operator keyed them",
		},
		{
			key:     "voucher_version_id",
			perturb: func(m Movement) Movement { m.VoucherVersionID = "bbb"; return m },
			why:     "the four business keys are not provably unique, so the id is the tiebreak that makes the order TOTAL and the replay reproducible",
		},
	}

	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			early := ref
			late := c.perturb(ref)
			late.VoucherVersionID += "-late"
			if c.key == "voucher_version_id" {
				late.VoucherVersionID = "bbb"
			}

			var seen []string
			Replay(
				Opening{Position: PositionKey{StockItemID: "widget"}},
				[]Movement{late, early}, // deliberately backwards
				recordingValuer{seen: &seen},
			)
			if len(seen) != 2 {
				t.Fatalf("Replay applied %d movements, expected 2", len(seen))
			}
			if seen[0] != early.VoucherVersionID {
				t.Fatalf(
					"movements were applied in %v — Replay did not order them by %s, so %s",
					seen, c.key, c.why,
				)
			}
		})
	}
}

// TestReplayAllKeepsPositionsApart pins that the position key — not the item —
// is the valuation bucket, including the NULL-dimension case (an item with no
// godown is a DIFFERENT position from the same item in a godown, exactly as
// IS NOT DISTINCT FROM grouping in the closing-balance query gives).
func TestReplayAllKeepsPositionsApart(t *testing.T) {
	inMain := mv(t, "2026-04-01", "PUR-1", 1, DirectionIn, "10", "100")
	inMain.Position = PositionKey{StockItemID: "widget", GodownID: "main"}
	inNowhere := mv(t, "2026-04-01", "PUR-1", 2, DirectionIn, "10", "200")
	inNowhere.Position = PositionKey{StockItemID: "widget"}

	positions := ReplayAll(nil, []Movement{inMain, inNowhere}, WeightedAverage{})
	if len(positions) != 2 {
		t.Fatalf("one item received into a godown and into no godown produced %d positions, not 2 — a NULL dimension was folded into a named one", len(positions))
	}
	if got := positions[PositionKey{StockItemID: "widget", GodownID: "main"}].ClosingRate; !got.Equal(dec(t, "100")) {
		t.Errorf("the Main Location position is carried at %s instead of its own 100.0000 cost", got.StringFixed(4))
	}
	if got := positions[PositionKey{StockItemID: "widget"}].ClosingRate; !got.Equal(dec(t, "200")) {
		t.Errorf("the godown-less position is carried at %s instead of its own 200.0000 cost", got.StringFixed(4))
	}
}

// TestReplayAllReportsAnOpeningWithNoMovements guards the stock-summary row
// that is easiest to lose: an item bought last year and untouched this year
// still holds value, and must appear.
func TestReplayAllReportsAnOpeningWithNoMovements(t *testing.T) {
	key := PositionKey{StockItemID: "widget"}
	positions := ReplayAll([]Opening{{Position: key, Qty: dec(t, "4"), Rate: dec(t, "25")}}, nil, WeightedAverage{})
	pos, ok := positions[key]
	if !ok {
		t.Fatal("a position with an opening balance and no movements vanished from the replay — 100.0000 of stock would be missing from the balance sheet")
	}
	if !pos.ClosingValue.Equal(dec(t, "100")) || !pos.ClosingQty.Equal(dec(t, "4")) {
		t.Fatalf("opening-only position closed at %s over %s units, expected 100.0000 over 4.0000", pos.ClosingValue.StringFixed(4), pos.ClosingQty.StringFixed(4))
	}
}
