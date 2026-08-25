package inventory

import (
	"errors"
	"fmt"
	"sort"

	"github.com/shopspring/decimal"
)

// moneyScale is the number of decimal places every money and quantity value
// carries. It matches the NUMERIC(19,4) columns and the corpora's 4-dp strings.
const moneyScale int32 = 4

// rateScale is the working precision of the running weighted-average COST rate,
// which is the only place a division enters a replay.
//
// It is 16 rather than 4 on purpose. The average cost of 1400.0000 across 15
// units is 93.3333...; rounding that to the 4 dp of the rate column before
// multiplying it back out would lose value on every single issue and the
// conservation identity in Position's doc would fail by paisas that accumulate
// for the life of the item. The 4-dp rounding happens ONCE, at the end, on the
// consumed value — never on the rate that produces it.
//
// The number is spelled out here (with DivRound) rather than taken from
// decimal.DivisionPrecision because that variable is a mutable package global:
// any other package in the process could change it and silently re-price this
// company's stock. Valuation must not be reachable from another package's
// init().
const rateScale int32 = 16

var hundred = decimal.NewFromInt(100)

// MethodWeightedAverage is Tally's default costing method and this build's
// default for a new stock item. It is the exact string
// stock_item.valuation_method's CHECK constraint allows; the other two it
// allows are MethodFIFO and MethodLIFO, in lots.go.
const MethodWeightedAverage = "WEIGHTED_AVG"

// ErrUnknownValuation is returned by ValuerFor for a method it does not
// implement. It is an ERROR and never a fallback: silently valuing a FIFO item
// with weighted average would produce a plausible, wrong, un-auditable number
// that no one would ever notice.
var ErrUnknownValuation = errors.New("inventory: unknown valuation method")

// round applies half-away-from-zero rounding at the money scale, matching the
// corpora's decimal.ROUND_HALF_UP at 4 dp (the values are non-negative, where
// the two rules coincide) and compliance.round.
func round(d decimal.Decimal) decimal.Decimal {
	return d.Round(moneyScale)
}

// ValuationState is a position's running cost state during a replay: how much
// stock is on hand and what it is carried at. Weighted average needs exactly
// the first two fields and nothing more.
//
// That is why FIFO WAS an additive change and not a schema change, and it is
// worth recording how it actually landed, because the prediction here was only
// half right. The predicted shape — "a FIFO valuer carries its own lot queue in
// its own implementation of Valuer" — is WRONG, and would have been a defect:
// ReplayAll passes ONE valuer to EVERY position, so a queue held in the valuer
// would have blended the lots of an item in two godowns into one, silently, and
// a valuer that mutated itself would no longer be pure. The queue belongs to the
// POSITION, so it lives here in the position's state (Lots below), where the
// Valuer contract stays "fold one movement" and stays pure. No caller, no
// column and no corpus shape moved, which was the part that was right.
type ValuationState struct {
	Qty   decimal.Decimal
	Value decimal.Decimal
	// Lots is the position's COSTED LAYERS, oldest first, and it is what FIFO
	// and LIFO carry that weighted average does not need. It is nil for a
	// weighted-average replay and that is not an omission: an average has one
	// rate for the whole position, so a layer list would be a second, redundant
	// statement of the same fact and the two could disagree.
	//
	// The invariant, when the queue is non-empty: the lots' quantities sum to
	// Qty and their values sum to Value. A position with stock but no layers is
	// legal and means "one layer, blended" — see the lots method for the two
	// places that happens (an opening, and an item switched onto FIFO after
	// trading at average).
	Lots []Lot
}

// Rate is the current weighted-average cost per unit at rateScale precision, or
// zero when there is no stock to average over.
//
// It is RECOMPUTED from the state every time it is asked for, which is the
// whole point: caching the rate at first use would make a back-dated inward
// unable to re-price a later outward (TestBackdatedInwardRevaluesLaterOutward
// is the guard for exactly that mistake).
func (s ValuationState) Rate() decimal.Decimal {
	if s.Qty.IsZero() {
		return decimal.Zero
	}
	return s.Value.DivRound(s.Qty, rateScale)
}

// Valuer folds a movement into a running cost state. Implementations MUST be
// pure: same state plus same movement yields the same result, every time, in
// any process. A valuer that consulted a clock, a database or a package global
// would make a report unreproducible, and reproducibility is the only reason
// value is not materialised.
type Valuer interface {
	// Method returns the stock_item.valuation_method string this valuer
	// implements, so a report can name the basis it used.
	Method() string
	// Apply folds m into state and returns the new state plus the value the
	// movement CONSUMED. consumedValue is zero for an inward and positive for an
	// outward; it is the cost of goods sold for that line, which is what makes
	// a gross-profit report possible without a second pass.
	Apply(state ValuationState, m Movement) (next ValuationState, consumedValue decimal.Decimal)
}

// WeightedAverage is v1's valuer: the position carries a single blended cost.
//
//   - inward: Qty += m.Qty, Value += m.Value() (the discounted landed cost).
//   - outward: consume m.Qty at the CURRENT average cost — Value/Qty recomputed
//     at this instant — never at m.Rate, which on a sale is the SELLING price.
//
// Applying it is the whole of the "weighted" part: an inward at a new price
// re-prices every unit on hand, including the ones bought earlier, and that is
// what makes the closing value of an unchanged item change when an older
// purchase is inserted or altered.
type WeightedAverage struct{}

// Method implements Valuer.
func (WeightedAverage) Method() string { return MethodWeightedAverage }

// Apply implements Valuer.
func (WeightedAverage) Apply(state ValuationState, m Movement) (ValuationState, decimal.Decimal) {
	if m.Direction == DirectionIn {
		return ValuationState{
			Qty:   state.Qty.Add(m.Qty),
			Value: state.Value.Add(m.Value()),
		}, decimal.Zero
	}

	nextQty := state.Qty.Sub(m.Qty)

	// An issue that empties the position must take the whole remaining value
	// with it. Consuming qty*rate instead would leave a sub-paisa residue
	// carried at zero quantity — a position worth 0.0003 with nothing in it,
	// which no report can explain and which the next inward would blend into
	// its cost. Tally shows a nil closing here and so do we.
	if nextQty.IsZero() {
		consumed := state.Value
		return ValuationState{Qty: decimal.Zero, Value: decimal.Zero}, consumed
	}

	// The rate is read from the state as it stands right now — see Rate.
	consumed := round(m.Qty.Mul(state.Rate()))
	return ValuationState{
		Qty:   nextQty,
		Value: state.Value.Sub(consumed),
	}, consumed
}

// ValuerFor selects the valuer for a stock_item.valuation_method string.
//
// The default arm returns ErrUnknownValuation rather than a working valuer.
// That is deliberate and load-bearing, and it has now been paid for twice over:
// migration 0078 widened the CHECK to admit 'FIFO' and 'LIFO', so rows with
// those methods exist, and any deployment running a binary that predates them
// FAILS LOUDLY on the Stock Summary and the Balance Sheet instead of quietly
// reporting weighted-average numbers under a FIFO heading. The next method
// (Tally ships eight) inherits the same protection for free.
func ValuerFor(method string) (Valuer, error) {
	switch method {
	case MethodWeightedAverage:
		return WeightedAverage{}, nil
	case MethodFIFO:
		return FIFO{}, nil
	case MethodLIFO:
		return LIFO{}, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownValuation, method)
	}
}

// less orders two movements by the canonical replay key
// (voucher_date, voucher_id, version_created_at, line_no, voucher_version_id).
//
// ⛔⛔ THE SECOND KEY IS THE VOUCHER'S ID, NOT ITS NUMBER, AND THAT CHANGE IS
// DEFECT D8. A voucher_no is a string from a PER-SERIES counter — and on a
// purchase it is the supplier's own reference — so it carries no ordering
// relationship to a number from another series. On one date "DN-1" sorted before
// "GRN-2" regardless of which was keyed first, and because weighted average is
// order-dependent an issue entered AFTER a receipt was costed at the PRE-receipt
// average. Measured in a browser: closing stock 601.4681 too high, on an issue
// nobody had edited.
//
// voucher_id is a UUIDv7 minted at CREATE, so it is entry order per company
// across every series, and it is stable: an ALTER keeps it. ⚠️ THE OBVIOUS
// ALTERNATIVE IS WRONG — VersionCreatedAt is the CURRENT version's timestamp, so
// an ALTER moves it, and batched posts in one transaction share now() and tie.
// VoucherVersionID is already a UUIDv7 but is RE-MINTED by an ALTER, so it is a
// tiebreak and not entry order either.
func less(a, b Movement) bool {
	if !a.VoucherDate.Equal(b.VoucherDate) {
		return a.VoucherDate.Before(b.VoucherDate)
	}
	if a.VoucherID != b.VoucherID {
		return a.VoucherID < b.VoucherID
	}
	if !a.VersionCreatedAt.Equal(b.VersionCreatedAt) {
		return a.VersionCreatedAt.Before(b.VersionCreatedAt)
	}
	if a.LineNo != b.LineNo {
		return a.LineNo < b.LineNo
	}
	return a.VoucherVersionID < b.VoucherVersionID
}

// Replay folds one position's opening and movements into a Position.
//
// It SORTS ITS OWN INPUT into replay order before folding. That is not a
// convenience: weighted average is order-dependent, so a caller who passed rows
// back in a different order — a different ORDER BY, a map iteration, a parallel
// fetch — would get a different, equally confident closing value. Sorting here
// means the ordering rule is stated once, in one place, and cannot be forgotten
// at a call site. TestReplayIsOrderDeterministic shuffles every corpus fixture
// twenty ways and demands one answer.
//
// The opening is folded in as the FIRST inward, before every movement,
// regardless of dates. It is by definition the state the books opened with; a
// movement dated before the opening date is a data error for the migration
// importer to reject, not something valuation should try to interpret.
//
// The caller's slice is never reordered — Replay sorts a copy, because callers
// legitimately hold one slice covering many positions (see ReplayAll) and a
// method that shuffled its argument would make the caller's next call depend on
// its previous one.
func Replay(opening Opening, movements []Movement, valuer Valuer) Position {
	ordered := make([]Movement, len(movements))
	copy(ordered, movements)
	sort.SliceStable(ordered, func(i, j int) bool { return less(ordered[i], ordered[j]) })

	key := opening.Position
	if key == (PositionKey{}) && len(ordered) > 0 {
		key = ordered[0].Position
	}

	pos := Position{
		Key:          key,
		OpeningQty:   opening.Qty,
		OpeningValue: opening.Value(),
		InwardQty:    decimal.Zero,
		InwardValue:  decimal.Zero,
		OutwardQty:   decimal.Zero,
		OutwardValue: decimal.Zero,
	}

	state := ValuationState{Qty: opening.Qty, Value: opening.Value()}

	for _, m := range ordered {
		next, consumed := valuer.Apply(state, m)
		if m.Direction == DirectionIn {
			pos.InwardQty = pos.InwardQty.Add(m.Qty)
			pos.InwardValue = pos.InwardValue.Add(m.Value())
		} else {
			pos.OutwardQty = pos.OutwardQty.Add(m.Qty)
			pos.OutwardValue = pos.OutwardValue.Add(consumed)
		}
		state = next
	}

	pos.ClosingQty = state.Qty
	pos.ClosingValue = round(state.Value)
	pos.ClosingRate = round(state.Rate())
	return pos
}

// ReplayAll groups openings and movements by PositionKey and replays each
// position independently, returning one Position per key that appears in
// either input (a position with an opening and no movements still reports).
//
// Positions are replayed SEPARATELY and never blended: the same item in two
// godowns, or in two batches, carries two different average costs, and the
// average of the averages is not the average. This is why the position key —
// not the item id — is what the closing-balance query groups by.
func ReplayAll(openings []Opening, movements []Movement, valuer Valuer) map[PositionKey]Position {
	byKey := make(map[PositionKey][]Movement)
	for _, m := range movements {
		byKey[m.Position] = append(byKey[m.Position], m)
	}
	openingByKey := make(map[PositionKey]Opening, len(openings))
	for _, o := range openings {
		if _, dup := openingByKey[o.Position]; dup {
			// stock_opening's UNIQUE NULLS NOT DISTINCT position key makes this
			// impossible in the database; a caller that built the slice by hand
			// would otherwise lose one of the two silently.
			panic("inventory: duplicate opening for the same position key")
		}
		openingByKey[o.Position] = o
		if _, ok := byKey[o.Position]; !ok {
			byKey[o.Position] = nil
		}
	}

	out := make(map[PositionKey]Position, len(byKey))
	for key, ms := range byKey {
		// A key with no stock_opening row replays from a zero state — the zero
		// value of Opening is exactly that, so no special case is needed.
		opening := openingByKey[key]
		opening.Position = key
		out[key] = Replay(opening, ms, valuer)
	}
	return out
}
