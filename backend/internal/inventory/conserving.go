// Package inventory's REPLAY ENGINE and the one arithmetic obligation a
// document can place on it: VALUE CONSERVATION (defect D103).
//
// ===========================================================================
// WHY THIS FILE EXISTS — D103, AND WHY IT IS A REGRESSION BY PREMISE
// ===========================================================================
// A Stock Journal moving 10 units of a FIFO item between two of the company's
// OWN godowns CREATED 33.30 of value, and the Balance Sheet moved with it and
// still footed. Measured in a browser on 2026-08-27
// (frontend/scripts/drive-l28-stock-restale.mjs, probe H):
//
//	Main Location held two layers, 10 @ 120.00 and 5 @ 130.00 — closing
//	15.0000 / 1,850.00. The Stock Summary shows a position Rate of 123.33
//	(closing value / closing qty), which is the ONLY rate the product displays
//	anywhere. The operator keyed it. The OUT leg then consumed the OLDEST
//	LAYER, 10 @ 120.00 = 1,200.00, while the IN leg carried the goods at the
//	keyed blend, 10 @ 123.33 = 1,233.30. Item closing value 1,850.00 ->
//	1,883.30. QUANTITY WAS UNCHANGED, so no quantity check anywhere could see
//	it, and the screen's own footer says the total holding is unchanged.
//
// ⭐ THE PREMISE THAT BROKE. A fix on 2026-08-07 forced a NON-ZERO inward rate
// on a transfer, and it was COMPLETE for the world it was written in: under
// weighted average the rate the report SHOWS is the same rate the outward leg
// consumes at, so keying it conserved EXACTLY. Migration 0078 added FIFO and
// LIFO and made that premise false for two methods out of three, and nothing
// downstream was revisited. Under a lot queue the displayed blend stops being
// the consumption rate the moment the queue holds more than one layer — and
// the direction is not fixed either: if the oldest layer is DEARER than the
// blend, the same keystrokes DESTROY value.
//
// ⭐ SO THE RATE STOPPED BEING THE ANSWER, AND THE FIX IS TO STOP ASKING FOR
// IT. A transfer between your own godowns HAS NO PRICE. Asking an operator to
// key one is asking them to BE the invariant, and they will key the only number
// on screen — which is exactly how this defect happens. The engine below
// DERIVES the outward leg's actual consumption cost and carries the inward leg
// at exactly that, so the voucher conserves BY CONSTRUCTION and no keystroke
// can make it otherwise.
//
// ===========================================================================
// WHY THE FIX LIVES IN THE REPLAY AND NOT IN THE POST PATH
// ===========================================================================
// Stock value is NEVER STORED (see the package doc): it is derived at report
// time by replaying the movement ledger, because valuation is ORDER-DEPENDENT.
// The consumption cost of a transfer's outward leg is therefore NOT KNOWN at
// post time and cannot be — a purchase BACK-DATED before the transfer changes
// which layers the transfer eats, and an ALTER of an April invoice changes what
// a July transfer consumed. A rate computed at post time and written into the
// movement row would be correct on the day and silently wrong forever after,
// which is the same class of defect one layer down. So the derivation happens
// here, in the replay, every time, from the state as it actually stands.
//
// ===========================================================================
// ⛔ WHY THE RULE IS NOT "THE INWARD RATE MUST EQUAL THE OUTWARD RATE"
// ===========================================================================
// StockJournal is ONE class serving TWO shapes (ledger/nonaccounting.go:110):
// a TRANSFER of the same item between godowns, and a MANUFACTURE that consumes
// several components and produces a DIFFERENT finished good. A per-line rule
// would refuse every legitimate manufacture, and a manufacture must be able to
// change per-item value — that is what manufacturing IS.
//
// ===========================================================================
// ⭐ THE RULE, AND WHY VOUCHER-LEVEL ALONE IS NOT IT
// ===========================================================================
// The INVARIANT is voucher-level: total inward carried value == total outward
// consumption cost. That is the right invariant for BOTH shapes, and the reason
// is that the class posts NO ACCOUNTING LINES AT ALL. A manufacture that
// produced a finished good worth more than the components it ate would raise
// closing stock with no credit anywhere — unbacked value, on the Balance Sheet,
// under a document with no money side. Conversion cost is real, but this build
// has no way to state it (there is no additional-cost ledger allocation on a
// stock voucher), so there is nothing to fund it with. When that arrives the
// rule extends naturally: inward == consumption + the allocations somebody
// posted.
//
// ⛔ BUT VOUCHER-LEVEL POOLING IS THE WRONG *ALLOCATION*, and the client proves
// it. frontend/src/screens/vouchers/stockVoucher.ts emits ONE OUT/IN PAIR PER
// KEYED ROW, and a Stock Journal may carry many rows — of DIFFERENT ITEMS. Pool
// the whole voucher and item A's closing value absorbs item B's cost: two
// perfectly ordinary transfers on one document would leak value between
// unrelated items, and the voucher total would still conserve, so the invariant
// would certify the corruption. The allocation must therefore be finer than the
// invariant.
//
// SO: ITEM MATCHES ITSELF FIRST, AND ONLY THE RESIDUE IS POOLED.
//
//  1. Every outward leg is applied first, in line order, and consumes at its own
//     position's real cost. Its consumption is banked PER ITEM.
//  2. Each item's own inward legs share that item's own consumption, pro-rata by
//     the keyed value of those legs. A transfer is exactly this case and the
//     keyed rate cancels out of it entirely.
//  3. The RESIDUE — the consumption of items that were eaten but not produced —
//     goes to the inward legs that got nothing in step 2 (the manufactured
//     goods), pro-rata by keyed value; or, if every leg already has its own
//     cost, across all of them (a rework that consumes a component INTO an item
//     it also produces). Either way the voucher total conserves.
//
// ⭐ WHAT THE KEYED RATE STILL MEANS. It is no longer a price; it is a RATIO —
// the operator's statement of how the consumed cost divides between two
// finished goods, or between a product and its by-product. On a transfer there
// is one inward leg per item, the ratio is 1, and the keyed number has no
// effect on any figure at all. That is the point.
//
// ⚠️ WHERE THE RULE HAS NO INPUT. A Stock Journal with NO outward leg is a
// Receipt Note keyed into the wrong screen: there is no consumption to derive
// from, so its keyed rate stands (ledger.validateStockVoucher already forces it
// non-zero). A Stock Journal with no INWARD leg is a write-off, and the value
// its outward legs consumed simply leaves — which is what the document says
// happened.

package inventory

import (
	"fmt"
	"sort"

	"github.com/shopspring/decimal"
)

// ReplayCompany folds EVERY position of EVERY item in ONE interleaved pass, in
// the single global movement order, choosing each item's valuer through
// valuerFor. It is the entry point every report goes through.
//
// ⭐ WHY ONE PASS AND NOT ONE PASS PER POSITION (which is what this used to be).
// A conserving voucher's legs live in DIFFERENT positions — that is what a
// transfer is — and often in different ITEMS with different valuers, which is
// what a manufacture is. Valuing each position in its own private replay makes
// the outward's consumption cost unavailable to the inward that has to carry
// it, so the coupling has to happen in one pass or not at all.
//
// ⚠️ ONE PASS DOES NOT CHANGE ANY NON-CONSERVING FIGURE. A movement only ever
// touches its own position's state, so interleaving positions in the global
// order leaves each position's own subsequence — and therefore its whole
// replay — exactly as it was. TestReplayIsOrderDeterministic and the corpora
// are the guard.
//
// valuerFor is called once per item id and its error is propagated verbatim: a
// report that quietly fell back to weighted average for a FIFO item would print
// a plausible number under the wrong heading, which is the failure ValuerFor's
// default arm exists to prevent.
func ReplayCompany(
	openings []Opening, movements []Movement,
	valuerFor func(stockItemID string) (Valuer, error),
) (map[PositionKey]Position, error) {
	e := &engine{
		byKey:    map[PositionKey]*livePosition{},
		valuers:  map[string]Valuer{},
		valuerOf: valuerFor,
	}

	for _, o := range openings {
		if _, dup := e.byKey[o.Position]; dup {
			// stock_opening's UNIQUE NULLS NOT DISTINCT position key makes this
			// impossible in the database; a caller that built the slice by hand
			// would otherwise lose one of the two silently.
			panic("inventory: duplicate opening for the same position key")
		}
		l := e.ensure(o.Position)
		l.pos.OpeningQty = o.Qty
		l.pos.OpeningValue = o.Value()
		l.state = ValuationState{Qty: o.Qty, Value: o.Value()}
	}

	// The caller's slice is never reordered — callers legitimately hold one
	// slice covering many positions, and a function that shuffled its argument
	// would make the caller's next call depend on its previous one.
	ordered := make([]Movement, len(movements))
	copy(ordered, movements)
	sort.SliceStable(ordered, func(i, j int) bool { return less(ordered[i], ordered[j]) })

	// THE VOUCHER RUN. Every movement of one voucher VERSION shares the first
	// three sort keys (date, voucher id, version created_at) and the last one,
	// differing only in line_no — so `less` necessarily lands them contiguously
	// and a run can be found by a single scan rather than by a second grouping
	// pass that could disagree with the sort.
	for i := 0; i < len(ordered); {
		j := i + 1
		for j < len(ordered) && sameVoucherVersion(ordered[i], ordered[j]) {
			j++
		}
		if err := e.applyVoucher(ordered[i:j]); err != nil {
			return nil, err
		}
		i = j
	}

	out := make(map[PositionKey]Position, len(e.byKey))
	for key, l := range e.byKey {
		l.pos.ClosingQty = l.state.Qty
		l.pos.ClosingValue = round(l.state.Value)
		l.pos.ClosingRate = round(l.state.Rate())
		out[key] = l.pos
	}
	return out, nil
}

// sameVoucherVersion reports whether two movements were written by the same
// voucher version. It is the run boundary, and it names all four identifying
// keys rather than just the voucher id: a report normally sees only the CURRENT
// version of each voucher, but a caller that handed us two versions of one
// voucher must get two runs, not one merged document.
func sameVoucherVersion(a, b Movement) bool {
	return a.VoucherID == b.VoucherID &&
		a.VoucherVersionID == b.VoucherVersionID &&
		a.VersionCreatedAt.Equal(b.VersionCreatedAt) &&
		a.VoucherDate.Equal(b.VoucherDate)
}

// livePosition is one position mid-replay: the running cost state and the
// Opening/Inward/Outward figures accumulated so far.
type livePosition struct {
	state ValuationState
	pos   Position
}

type engine struct {
	byKey    map[PositionKey]*livePosition
	valuers  map[string]Valuer
	valuerOf func(stockItemID string) (Valuer, error)
}

func (e *engine) ensure(key PositionKey) *livePosition {
	if l, ok := e.byKey[key]; ok {
		return l
	}
	l := &livePosition{pos: Position{
		Key:          key,
		OpeningQty:   decimal.Zero,
		OpeningValue: decimal.Zero,
		InwardQty:    decimal.Zero,
		InwardValue:  decimal.Zero,
		OutwardQty:   decimal.Zero,
		OutwardValue: decimal.Zero,
	}}
	e.byKey[key] = l
	return l
}

func (e *engine) valuer(itemID string) (Valuer, error) {
	if v, ok := e.valuers[itemID]; ok {
		return v, nil
	}
	v, err := e.valuerOf(itemID)
	if err != nil {
		return nil, err
	}
	e.valuers[itemID] = v
	return v, nil
}

// fold applies ONE movement to its position and returns what an outward
// consumed. carried, when non-nil, is the DERIVED value an inward leg is folded
// in at instead of its own qty x rate.
func (e *engine) fold(m Movement, carried *decimal.Decimal) (decimal.Decimal, error) {
	v, err := e.valuer(m.Position.StockItemID)
	if err != nil {
		return decimal.Zero, err
	}
	if carried != nil {
		m.carriedValue = *carried
		m.hasCarried = true
	}
	l := e.ensure(m.Position)
	next, consumed := v.Apply(l.state, m)
	if m.Direction == DirectionIn {
		l.pos.InwardQty = l.pos.InwardQty.Add(m.Qty)
		// m.Value() is the CARRIED value when one was derived, so the reported
		// inward figure and the figure folded into the state are the same number
		// by construction. They must be: the conservation identity in Position's
		// doc (opening + inward - outward == closing) is checked against these.
		l.pos.InwardValue = l.pos.InwardValue.Add(m.Value())
	} else {
		l.pos.OutwardQty = l.pos.OutwardQty.Add(m.Qty)
		l.pos.OutwardValue = l.pos.OutwardValue.Add(consumed)
	}
	l.state = next
	return consumed, nil
}

// applyVoucher folds one voucher's movements. A voucher that does not conserve
// — or that has nothing to conserve BETWEEN, because one of the two directions
// is missing — is folded line by line exactly as it always was.
func (e *engine) applyVoucher(run []Movement) error {
	if !conserves(run) {
		for _, m := range run {
			if _, err := e.fold(m, nil); err != nil {
				return err
			}
		}
		return nil
	}
	return e.applyConserving(run)
}

// conserves reports whether this run must be value-conserving: the voucher says
// so AND it actually carries both directions.
//
// The flag is read off run[0] because it is a property of the DOCUMENT and
// every line of one voucher carries the same answer (see
// Movement.VoucherConserves).
func conserves(run []Movement) bool {
	if !run[0].VoucherConserves {
		return false
	}
	var hasIn, hasOut bool
	for _, m := range run {
		if m.Direction == DirectionIn {
			hasIn = true
		} else {
			hasOut = true
		}
	}
	return hasIn && hasOut
}

// applyConserving is the rule stated in the file header.
//
// ⛔ THE OUTWARD LEGS GO FIRST, AHEAD OF LINE ORDER, and that is deliberate
// rather than convenient. Their consumption is the INPUT to every inward leg,
// so it has to be known before any of them lands; and it is also the correct
// reading of the document — a manufacture must not be able to eat the goods it
// is in the act of producing. It changes no figure except in the one case where
// an OUT and an IN of the same voucher touch the SAME position, which is the
// case where eating your own output was possible.
func (e *engine) applyConserving(run []Movement) error {
	consumedByItem := map[string]decimal.Decimal{}
	totalConsumed := decimal.Zero
	for _, m := range run {
		if m.Direction == DirectionIn {
			continue
		}
		c, err := e.fold(m, nil)
		if err != nil {
			return err
		}
		item := m.Position.StockItemID
		consumedByItem[item] = consumedByItem[item].Add(c)
		totalConsumed = totalConsumed.Add(c)
	}

	// The inward legs, in line order — which is the order the operator keyed
	// them, and the only order that makes the allocation reproducible.
	ins := make([]Movement, 0, len(run))
	for _, m := range run {
		if m.Direction == DirectionIn {
			ins = append(ins, m)
		}
	}

	// Group them by item, keeping FIRST-APPEARANCE order. ⛔ Never map iteration
	// order: the rounding remainder lands on the LAST leg of each group, so a
	// map walk would move a paisa between legs from one run of the same report
	// to the next.
	var itemOrder []string
	legsOf := map[string][]int{}
	for k, m := range ins {
		item := m.Position.StockItemID
		if _, seen := legsOf[item]; !seen {
			itemOrder = append(itemOrder, item)
		}
		legsOf[item] = append(legsOf[item], k)
	}

	carried := make([]decimal.Decimal, len(ins))
	for k := range carried {
		carried[k] = decimal.Zero
	}
	hasOwn := make([]bool, len(ins))

	// STEP 2 — every item takes its OWN consumption first. This is the whole of
	// the transfer case, and it is what stops one item's cost landing on
	// another when a voucher carries several transfers.
	spent := decimal.Zero
	for _, item := range itemOrder {
		own, ok := consumedByItem[item]
		if !ok {
			continue
		}
		idx := legsOf[item]
		for k, share := range allocate(own, ins, idx) {
			carried[idx[k]] = carried[idx[k]].Add(share)
			hasOwn[idx[k]] = true
		}
		spent = spent.Add(own)
	}

	// STEP 3 — the residue: what was consumed of items this voucher did not
	// produce. It belongs to the goods that WERE produced out of it.
	if residue := totalConsumed.Sub(spent); !residue.IsZero() {
		targets := make([]int, 0, len(ins))
		for k := range ins {
			if !hasOwn[k] {
				targets = append(targets, k)
			}
		}
		if len(targets) == 0 {
			// Every inward leg already carries its own item's cost, so this is a
			// rework: the voucher consumed a component INTO an item it also
			// produces. The component's cost goes onto that output — anywhere
			// else and the voucher would destroy it.
			for k := range ins {
				targets = append(targets, k)
			}
		}
		for k, share := range allocate(residue, ins, targets) {
			carried[targets[k]] = carried[targets[k]].Add(share)
		}
	}

	for k, m := range ins {
		v := carried[k]
		if _, err := e.fold(m, &v); err != nil {
			return err
		}
	}
	return nil
}

// allocate divides `total` across the inward legs named by idx, pro-rata by the
// value those legs were KEYED at, and returns one share per named leg in the
// order idx names them.
//
// ⭐ THE LAST LEG TAKES THE REMAINDER, so the shares sum to `total` EXACTLY, to
// the paisa, whatever the rounding did to the others. That exactness is the
// whole deliverable: a fix that conserved to within a rounding error would
// still move the Balance Sheet, just more slowly and with no test able to say
// by how much.
//
// ⚠️ THE BASIS FALLS BACK TO QUANTITY when every named leg was keyed at zero.
// ledger.validateStockVoucher refuses a zero-rate inward on this class, so a
// voucher keyed through the product cannot reach it — but imported and
// legacy rows can, and "divide by zero, therefore carry nothing" would be the
// original defect with a different sign.
func allocate(total decimal.Decimal, ins []Movement, idx []int) []decimal.Decimal {
	out := make([]decimal.Decimal, len(idx))
	if len(idx) == 0 {
		return out
	}
	if len(idx) == 1 {
		out[0] = total
		return out
	}

	basis := make([]decimal.Decimal, len(idx))
	sum := decimal.Zero
	for i, k := range idx {
		basis[i] = ins[k].Value()
		sum = sum.Add(basis[i])
	}
	if sum.IsZero() {
		for i, k := range idx {
			basis[i] = ins[k].Qty
			sum = sum.Add(basis[i])
		}
	}
	if sum.IsZero() {
		// Unreachable: stock_movement CHECKs qty > 0. An equal split is the only
		// answer left, and it still conserves.
		for i := range basis {
			basis[i] = decimal.NewFromInt(1)
			sum = sum.Add(basis[i])
		}
	}

	running := decimal.Zero
	for i := 0; i < len(idx)-1; i++ {
		// rateScale, not moneyScale, on the division — the same reason
		// ValuationState.Rate uses it: rounding the proportion before
		// multiplying it out loses value on every leg.
		share := round(total.Mul(basis[i]).DivRound(sum, rateScale))
		out[i] = share
		running = running.Add(share)
	}
	out[len(idx)-1] = total.Sub(running)
	return out
}

// errUnreachableValuer is what ReplayAll would have to do with an error its own
// selector cannot produce.
func errUnreachableValuer(err error) string {
	return fmt.Sprintf("inventory: ReplayAll's constant valuer selector failed, which cannot happen: %v", err)
}
