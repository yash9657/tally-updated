package orders

// bom_test.go — the BOM arithmetic and the two validators, WITH NO DATABASE.
//
// These are pure-function tests on purpose. The scaling is the one thing in this
// package that is wrong in a way no integration test would notice: it is off by a
// tenth of a gram, always in the same direction, and it lands in closing stock as
// phantom material that reconciles against nothing.

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// TestScaleComponentMultipliesBeforeDividing is the load-bearing test of this
// package.
//
// ⛔ THE CASES ARE CHOSEN TO TELL THE TWO IMPLEMENTATIONS APART, WHICH IS THE
// WHOLE DIFFICULTY. Every WHOLE-MULTIPLE batch (make 3 of a 3-yield recipe, make
// 6, make 9) is exact under BOTH `qty*wanted/output` and the wrong
// `qty*(wanted/output)`, because the ratio is a whole number and survives
// rounding. A test that only tried those would pass on the broken version.
//
// So the table below leads with the batches whose ratio is NOT representable at
// four places — make 1 of 3, make 7 of 3 — and states the wrong answer in the
// failure message so a regression is recognisable rather than merely red.
func TestScaleComponentMultipliesBeforeDividing(t *testing.T) {
	cases := []struct {
		name                string
		qty, wanted, output string
		want                string
		// wrongIfRatioFirst is what `qty.Mul(wanted.Div(output))` produces. Stated
		// so the failure message can say "you have the operations in the wrong
		// order" rather than just printing two numbers.
		wrongIfRatioFirst string
	}{
		{
			name: "10kg makes 3, make ONE — the ratio is 0.3333 and is not representable",
			qty:  "10", wanted: "1", output: "3",
			want: "3.3333", wrongIfRatioFirst: "3.3330",
		},
		{
			name: "10kg makes 3, make SEVEN — ratio 2.3333, still not representable",
			qty:  "10", wanted: "7", output: "3",
			want: "23.3333", wrongIfRatioFirst: "23.3330",
		},
		{
			name: "10kg makes 3, make THREE — exact under both forms, so this is the CONTROL",
			qty:  "10", wanted: "3", output: "3",
			want: "10.0000", wrongIfRatioFirst: "10.0000",
		},
		{
			name: "10kg makes 3, make SIX — also exact under both; the second control",
			qty:  "10", wanted: "6", output: "3",
			want: "20.0000", wrongIfRatioFirst: "20.0000",
		},
		{
			name: "the ordinary shape: 4 legs per table, make 25 tables",
			qty:  "4", wanted: "25", output: "1",
			want: "100.0000", wrongIfRatioFirst: "100.0000",
		},
		{
			name: "a fractional component: 200g of screws per table, make 7",
			qty:  "0.2", wanted: "7", output: "1",
			want: "1.4000", wrongIfRatioFirst: "1.4000",
		},
		{
			name: "a seven-yield recipe scaled to one — the worst repeating case",
			qty:  "1", wanted: "1", output: "7",
			want: "0.1429", wrongIfRatioFirst: "0.1429",
		},
		{
			name: "a partial batch: 10kg makes 3, make half of one",
			qty:  "10", wanted: "0.5", output: "3",
			want: "1.6667", wrongIfRatioFirst: "1.6665",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ScaleComponent(d(c.qty), d(c.wanted), d(c.output))
			if got.StringFixed(4) != c.want {
				msg := "ScaleComponent(%s, wanted %s, yield %s) = %s, want %s"
				if got.StringFixed(4) == c.wrongIfRatioFirst && c.wrongIfRatioFirst != c.want {
					msg += " — that is exactly what computing the RATIO FIRST produces, so the " +
						"multiplication and the division are in the wrong order. Every batch would be " +
						"short by this much, always downward, accumulating in closing stock as phantom " +
						"material that reconciles against nothing"
				}
				t.Errorf(msg, c.qty, c.wanted, c.output, got.StringFixed(4), c.want)
			}
		})
	}
}

// A non-positive yield returns zero rather than panicking. Unreachable through
// Explode, and asserted because the alternative to a clear zero is a
// division-by-zero panic surfacing as an opaque 500.
func TestScaleComponentDoesNotPanicOnAZeroYield(t *testing.T) {
	for _, output := range []string{"0", "-3"} {
		if got := ScaleComponent(d("10"), d("5"), d(output)); !got.IsZero() {
			t.Errorf("ScaleComponent with yield %s = %s, want 0", output, got)
		}
	}
}

// ---------------------------------------------------------------------------
// The validators
// ---------------------------------------------------------------------------

func TestBomInputValidateRefusesTheRecipesThatCannotWork(t *testing.T) {
	item, other := uuid.New(), uuid.New()
	good := func() BomInput {
		return BomInput{
			StockItemID: item,
			Name:        "Standard",
			OutputQty:   d("3"),
			Components:  []ComponentInput{{StockItemID: other, Qty: d("10")}},
		}
	}

	// THE CONTROL FIRST. Without it every refusal below is consistent with
	// Validate refusing everything.
	if err := good().Validate(); err != nil {
		t.Fatalf("CONTROL: a well-formed recipe must be accepted, got: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*BomInput)
		want   string
	}{
		{"no finished good", func(b *BomInput) { b.StockItemID = uuid.Nil }, "finished good"},
		{"no name", func(b *BomInput) { b.Name = "  " }, "needs a name"},
		{"zero yield", func(b *BomInput) { b.OutputQty = d("0") }, "yield must be more than zero"},
		{"negative yield", func(b *BomInput) { b.OutputQty = d("-3") }, "yield must be more than zero"},
		{"no components", func(b *BomInput) { b.Components = nil }, "consumes nothing"},
		{"a zero component", func(b *BomInput) {
			b.Components = []ComponentInput{{StockItemID: other, Qty: d("0")}}
		}, "more than zero"},
		{
			// ⛔ The one that matters: exploding an item into itself would issue and
			// receive the same goods with nothing converted.
			name: "the finished good as its own component",
			mutate: func(b *BomInput) {
				b.Components = append(b.Components, ComponentInput{StockItemID: item, Qty: d("1")})
			},
			want: "its own recipe",
		},
		{
			name: "the same component twice",
			mutate: func(b *BomInput) {
				b.Components = append(b.Components, ComponentInput{StockItemID: other, Qty: d("2")})
			},
			want: "twice",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := good()
			c.mutate(&in)
			err := in.Validate()
			if err == nil {
				t.Fatalf("a recipe with %s was ACCEPTED", c.name)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("the refusal for %s does not say why (want %q): %v", c.name, c.want, err)
			}
		})
	}
}

func TestOrderInputValidateRefusesTheOrdersThatCannotWork(t *testing.T) {
	party, item := uuid.New(), uuid.New()
	good := func() Input {
		return Input{
			Direction:     Sales,
			OrderNo:       "SO-1",
			OrderDate:     "2026-06-01",
			PartyLedgerID: party,
			Lines:         []LineInput{{StockItemID: item, Qty: d("20"), Rate: d("500")}},
		}
	}
	if err := good().Validate(); err != nil {
		t.Fatalf("CONTROL: a well-formed order must be accepted, got: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Input)
		want   string
	}{
		{"no direction", func(o *Input) { o.Direction = "" }, "direction must be"},
		{"a made-up direction", func(o *Input) { o.Direction = "TRANSFER" }, "direction must be"},
		{"no order number", func(o *Input) { o.OrderNo = " " }, "needs a number"},
		{"no date", func(o *Input) { o.OrderDate = "" }, "needs a date"},
		{"no party", func(o *Input) { o.PartyLedgerID = uuid.Nil }, "party ledger"},
		{"no lines", func(o *Input) { o.Lines = nil }, "commits nothing"},
		{"a zero quantity", func(o *Input) { o.Lines[0].Qty = d("0") }, "more than zero"},
		{"a negative quantity", func(o *Input) { o.Lines[0].Qty = d("-5") }, "more than zero"},
		{"a negative rate", func(o *Input) { o.Lines[0].Rate = d("-1") }, "cannot be negative"},
		{"no stock item", func(o *Input) { o.Lines[0].StockItemID = uuid.Nil }, "stock item"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := good()
			c.mutate(&in)
			err := in.Validate()
			if err == nil {
				t.Fatalf("an order with %s was ACCEPTED", c.name)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("the refusal for %s does not say why (want %q): %v", c.name, c.want, err)
			}
		})
	}
}

// ⛔ THE SAME ITEM TWICE IS LEGAL ON AN ORDER AND ILLEGAL IN A RECIPE, and the
// asymmetry is deliberate enough to need a test that would fail if somebody
// "fixed" it for consistency.
//
// Two order lines for the same item at two agreed rates — a hundred at the old
// price, fifty at the new — is an ordinary commercial order, and fulfilment is per
// LINE so the two stay separately outstanding. Two recipe lines for one component
// is the same requirement keyed twice, which can only make a total come out right
// by making the other line wrong.
func TestTheSameItemTwiceIsLegalOnAnOrderAndNotInARecipe(t *testing.T) {
	party, item := uuid.New(), uuid.New()
	order := Input{
		Direction: Sales, OrderNo: "SO-2", OrderDate: "2026-06-01", PartyLedgerID: party,
		Lines: []LineInput{
			{StockItemID: item, Qty: d("100"), Rate: d("500")},
			{StockItemID: item, Qty: d("50"), Rate: d("550")},
		},
	}
	if err := order.Validate(); err != nil {
		t.Errorf("two order lines for the same item at different agreed rates must be ACCEPTED — "+
			"it is an ordinary order and fulfilment is per line: %v", err)
	}

	recipe := BomInput{
		StockItemID: uuid.New(), Name: "Standard", OutputQty: d("1"),
		Components: []ComponentInput{
			{StockItemID: item, Qty: d("2")},
			{StockItemID: item, Qty: d("2")},
		},
	}
	if err := recipe.Validate(); err == nil {
		t.Error("two recipe components for the same item must be REFUSED — an operator who wants " +
			"4 legs writes 4, not two lines of 2")
	}
}
