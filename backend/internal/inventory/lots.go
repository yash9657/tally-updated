// Package inventory's lot-queue valuers: FIFO and LIFO (feature T9).
//
// WHY THIS IS A SEPARATE FILE FROM valuation.go: everything here is the SECOND
// costing model, and it exists to be read against the first. valuation.go holds
// the movement fold, the replay order and weighted average — the parts every
// method shares — and this file holds the one thing FIFO and LIFO add, which is
// that a position is not one blended rate but a QUEUE OF COSTED LAYERS.
//
// THE ONE FACT TO CARRY AWAY: the queue lives in the POSITION'S STATE, never in
// the valuer. ReplayAll hands ONE valuer to every position it replays, so a
// valuer that held a queue in a field would blend the layers of an item in the
// Delhi godown with the layers of the same item in Mumbai — silently, with both
// closing quantities still correct. Valuers stay stateless and pure; see
// ValuationState.Lots.

package inventory

import "github.com/shopspring/decimal"

// --------------------------------------------------------------------------
// T9 — FIFO and LIFO. THE LOT QUEUE.
// --------------------------------------------------------------------------

// MethodFIFO values a position by consuming its OLDEST costed layer first —
// Tally's "FIFO" costing method. MethodLIFO consumes the NEWEST first
// ("LIFO Perpetual"; see the LIFO type for why the annual variant is not this).
//
// Both strings are exactly what stock_item.valuation_method's CHECK allows
// (widened by migration 0078), and both are what a report prints as its basis.
const (
	MethodFIFO = "FIFO"
	MethodLIFO = "LIFO"
)

// Lot is one costed layer of a position: a quantity that arrived together and
// the value it arrived at. It is what makes FIFO and LIFO different from
// weighted average — an average has one rate for the whole position, a lot
// queue keeps every receipt's own rate until that receipt is issued.
//
// A lot carries VALUE rather than a rate, and that is deliberate: the rate is
// value/qty recomputed when a partial issue needs it (at rateScale, exactly as
// ValuationState.Rate does), so a lot of 3 units bought for 1000.0000 consumes
// its whole 1000.0000 across three issues of one unit with no residue. A stored
// 4-dp rate of 333.3333 would lose 0.0001 on the way through and leave a lot
// holding nothing but a paisa.
//
// Qty is ALWAYS strictly positive. A negative position has NO lots — see the
// FIFO type's doc for what happens when a position is issued below zero.
type Lot struct {
	Qty   decimal.Decimal
	Value decimal.Decimal
}

// lots returns the state's lot queue, oldest first, synthesising a single
// blended lot when the queue is empty but the position holds stock.
//
// ⛔ THE SYNTHESIS IS NOT A FALLBACK, IT IS THE OPENING. Replay seeds its state
// from Opening as {Qty, Value} with no lots — an opening position is ONE
// quantity at ONE rate, which is exactly one lot — so this is where a FIFO
// replay picks its first layer up. Doing it here rather than in Replay is what
// keeps the Valuer contract at "fold one movement" and keeps Replay from having
// to know which valuers have lots.
//
// It is also what makes a valuation method SWITCHABLE on a live item: the
// history of a position valued at weighted average until today has no layers,
// and the honest thing to carry forward is the one blended layer the average
// actually describes. See SetStockItemValuationMethod in internal/masters.
func (s ValuationState) lots() []Lot {
	if len(s.Lots) > 0 {
		return s.Lots
	}
	if s.Qty.IsPositive() {
		return []Lot{{Qty: s.Qty, Value: s.Value}}
	}
	return nil
}

// FIFO consumes the OLDEST lot first: the closing position is carried at what
// the most RECENT purchases cost, which is the whole point of the method (in a
// rising market FIFO reports a higher closing stock, and a lower cost of goods
// sold, than weighted average).
//
// ⛔ WHAT HAPPENS WHEN A POSITION GOES NEGATIVE, AND WHY IT IS NOT FIFO ANY
// MORE. With company.allow_negative_stock on, an issue may exceed everything
// the queue holds. The valuer then consumes EXACTLY what was there and no more
// — it never invents a cost from the sale rate, the same rule WeightedAverage
// states — the queue is emptied, and the position carries a negative quantity
// at zero value. The layers are GONE at that point and cannot be recovered:
// FIFO cannot say which layer a unit that was never received came from. So the
// next receipt into a negative position REBUILDS a single blended layer from
// the netted {Qty, Value} rather than pretending the history survived.
//
// That is a real, stated limitation of turning negative stock on, and it is why
// the two methods diverge exactly there: weighted average would have consumed
// qty*rate and driven the position's VALUE negative to match. Neither answer is
// right, because the missing purchase is the thing that is wrong; this one at
// least never reports a cost of goods sold that no receipt ever paid for.
type FIFO struct{}

// Method implements Valuer.
func (FIFO) Method() string { return MethodFIFO }

// Apply implements Valuer.
func (FIFO) Apply(state ValuationState, m Movement) (ValuationState, decimal.Decimal) {
	return applyLots(state, m, false)
}

// LIFO consumes the NEWEST lot first, so the closing position is carried at
// what the OLDEST stock cost. This is Tally's "LIFO Perpetual": the layer
// chosen is the newest one AS AT THE MOMENT OF THE ISSUE, which is the only
// variant a movement replay can express.
//
// ⚠️ IT IS NOT "LIFO ANNUAL", and the difference is not cosmetic. Annual LIFO
// picks the layer as at the YEAR END, so an issue in April is costed at a
// purchase made in the following March — a figure that cannot be stated until
// the year closes, and that changes retrospectively every time a later purchase
// is keyed. Offering it under the same name as this would silently restate
// every interim Profit & Loss the operator had already read.
type LIFO struct{}

// Method implements Valuer.
func (LIFO) Method() string { return MethodLIFO }

// Apply implements Valuer.
func (LIFO) Apply(state ValuationState, m Movement) (ValuationState, decimal.Decimal) {
	return applyLots(state, m, true)
}

// applyLots is the shared fold behind FIFO and LIFO. newestFirst picks which
// end of the queue an issue eats from, and it is the ONLY difference between
// the two methods: one flag, so the two can never drift apart in the arithmetic
// that is the same for both (the rounding, the empty-lot rule, the negative
// case).
//
// ⛔ VALUE MOVES BY EXACTLY WHAT THE LOTS MOVED BY. next.Value is
// state.Value - consumed, where consumed is the sum of what was actually taken
// out of the layers, so the conservation identity in Position's doc
// (opening + inward - outward == closing) holds by construction rather than by
// arithmetic that happens to agree. TestValueIsConserved is the guard.
func applyLots(state ValuationState, m Movement, newestFirst bool) (ValuationState, decimal.Decimal) {
	if m.Direction == DirectionIn {
		next := ValuationState{
			Qty:   state.Qty.Add(m.Qty),
			Value: state.Value.Add(m.Value()),
		}
		queue := state.lots()
		if len(queue) == 0 {
			// An empty queue is either a fresh position, a position that has
			// been issued flat, or one that has gone NEGATIVE. In the first two
			// the receipt is simply the first layer; in the third the layers
			// were destroyed and the netted position is one blended layer. All
			// three are "the position is now one lot of everything it holds",
			// and a still-negative position holds no layer at all.
			if next.Qty.IsPositive() {
				next.Lots = []Lot{{Qty: next.Qty, Value: next.Value}}
			}
			return next, decimal.Zero
		}
		next.Lots = append(append(make([]Lot, 0, len(queue)+1), queue...),
			Lot{Qty: m.Qty, Value: m.Value()})
		return next, decimal.Zero
	}

	nextQty := state.Qty.Sub(m.Qty)

	// An issue that empties the position — or drives it past empty — takes
	// EVERYTHING the layers held and nothing more. Both halves matter: leaving
	// a sub-paisa residue behind would carry value at zero quantity (the rule
	// WeightedAverage states), and consuming beyond the layers would invent a
	// cost for goods no receipt ever paid for.
	if !nextQty.IsPositive() {
		consumed := state.Value
		if consumed.IsNegative() {
			// A position whose value is already negative (weighted average can
			// leave one behind) has nothing to give; consuming a negative value
			// would CREDIT cost of goods sold.
			consumed = decimal.Zero
		}
		return ValuationState{Qty: nextQty, Value: state.Value.Sub(consumed)}, consumed
	}

	queue := state.lots()
	remaining := m.Qty
	consumed := decimal.Zero
	kept := make([]Lot, 0, len(queue))

	for i := range queue {
		// FIFO walks the queue oldest-first; LIFO walks it newest-first. Either
		// way the SURVIVING lots stay in arrival order, so a subsequent issue
		// sees the same queue this one would have.
		idx := i
		if newestFirst {
			idx = len(queue) - 1 - i
		}
		lot := queue[idx]
		switch {
		case remaining.IsZero():
			kept = append(kept, lot)
		case lot.Qty.LessThanOrEqual(remaining):
			// The whole layer goes, at exactly the value it held.
			consumed = consumed.Add(lot.Value)
			remaining = remaining.Sub(lot.Qty)
		default:
			// A partial issue out of one layer. The layer's own rate is
			// recomputed at rateScale for the same reason ValuationState.Rate
			// is: rounding the rate to 4 dp first loses value on every issue.
			take := round(remaining.Mul(lot.Value.DivRound(lot.Qty, rateScale)))
			consumed = consumed.Add(take)
			kept = append(kept, Lot{Qty: lot.Qty.Sub(remaining), Value: lot.Value.Sub(take)})
			remaining = decimal.Zero
		}
	}
	if newestFirst {
		// Restore arrival order: the walk above was backwards.
		for l, r := 0, len(kept)-1; l < r; l, r = l+1, r-1 {
			kept[l], kept[r] = kept[r], kept[l]
		}
	}

	return ValuationState{
		Qty:   nextQty,
		Value: state.Value.Sub(consumed),
		Lots:  kept,
	}, consumed
}

// KnownValuationMethods lists every method this build can value stock with, in
// the order a picker should offer them: the default first.
//
// ⛔ IT IS DERIVED FROM ValuerFor AND NOT WRITTEN OUT, so it cannot list a
// method no valuer implements — which is the drift that would put an
// unvaluable method in stock_item.valuation_method and fail every report for
// the whole company. A list maintained by hand beside a switch statement is the
// third copy of a vocabulary that is only allowed two (this build's valuers and
// migration 0078's CHECK), and it is the copy that goes stale first.
func KnownValuationMethods() []string {
	candidates := []string{MethodWeightedAverage, MethodFIFO, MethodLIFO}
	out := make([]string, 0, len(candidates))
	for _, m := range candidates {
		if _, err := ValuerFor(m); err == nil {
			out = append(out, m)
		}
	}
	return out
}

// MethodInfo describes one costing method for a form that has to offer them.
// The Label is Tally's own name for the method, so an operator migrating from
// Tally recognises what they are choosing, and Basis says in one line what the
// closing stock ends up carried at — which is the only thing that distinguishes
// the three to somebody who is not an accountant.
type MethodInfo struct {
	Method string
	Label  string
	Basis  string
}

// MethodCatalogue describes every implemented method, for the vocabulary a
// client builds its picker from.
//
// ⚠️ SERVED, NEVER HARDCODED IN A CLIENT. A form built from a list the server
// does not own can compose a value the server refuses, and the operator meets
// that refusal after keying the whole master. This is the same rule the payroll
// vocabulary route states, and here the stakes are a Balance Sheet.
func MethodCatalogue() []MethodInfo {
	byMethod := map[string]MethodInfo{
		MethodWeightedAverage: {
			Method: MethodWeightedAverage,
			Label:  "Avg. Cost (default)",
			Basis: "One blended rate for the whole position: every receipt re-prices " +
				"the stock already on hand, so a late purchase changes what earlier issues cost.",
		},
		MethodFIFO: {
			Method: MethodFIFO,
			Label:  "FIFO",
			Basis: "The oldest receipt is issued first, so closing stock is carried at " +
				"what the MOST RECENT purchases cost — higher than average in a rising market.",
		},
		MethodLIFO: {
			Method: MethodLIFO,
			Label:  "LIFO (Perpetual)",
			Basis: "The newest receipt at the time of each issue is consumed first, so closing " +
				"stock is carried at what the OLDEST purchases cost. Not LIFO Annual, which " +
				"cannot be stated until the year closes.",
		},
	}
	out := make([]MethodInfo, 0, 3)
	for _, m := range KnownValuationMethods() {
		out = append(out, byMethod[m])
	}
	return out
}
