// Package inventory owns the PURE stock-valuation leaf: the movement/position
// value types and the valuers that replay a movement ledger into a closing
// position. It imports nothing from this repo (internal/archtest pins its dep
// set to the empty map) for the same reason internal/compliance does: valuation
// must be callable from a report, from a test, and one day from an offline
// client, with no database, no clock and no tenant context in the way.
//
// THE ONE ARCHITECTURAL FACT THIS PACKAGE EXISTS TO EXPRESS: stock value is
// NEVER stored. No column anywhere holds a derived stock value or a valuation
// rate. Value is derived at REPORT time by replaying the movement ledger
// through a valuer, because valuation is ORDER-DEPENDENT:
//
//   - a back-dated inward must re-value every outward that already happened
//     after it (corpora/inventory/backdated-inward-revalues-outward.json is the
//     executable statement of this — the same outward consumes 1000.0000 before
//     the back-dated purchase exists and 750.0000 after);
//   - an ALTER of an April invoice must change the July closing value.
//
// A materialised value cannot express either without a full recompute, so we
// skipped the materialisation and kept only the recompute. This mirrors what
// the accounting side already chose: reports derive from postings joined to
// voucher_head.current_version_id, not from ledger_balance.
//
// The transactional half lives elsewhere. A stock movement is an immutable
// sibling child of voucher_version, written ONLY inside ledger.post()'s
// transaction; nothing in this package writes anything.
package inventory

import (
	"time"

	"github.com/shopspring/decimal"
)

// Direction is the sign carrier of a movement. The database column is an ENUM
// (CREATE TYPE stock_direction AS ENUM ('IN','OUT')) and qty is CHECK (qty > 0),
// so a quantity is NEVER signed anywhere in the system — direction alone says
// whether stock arrived or left. Callers that "helpfully" negate an outward qty
// will be rejected by the CHECK before they can corrupt a valuation.
type Direction string

const (
	// DirectionIn is a receipt into a position (purchase, credit note, opening,
	// stock journal destination).
	DirectionIn Direction = "IN"
	// DirectionOut is an issue out of a position (sale, debit note, delivery
	// note, stock journal source).
	DirectionOut Direction = "OUT"
)

// PositionKey identifies the (item, godown, batch) bucket a movement belongs
// to. godown and batch are NULLABLE dimensions in the schema, and the empty
// string here means exactly that SQL NULL.
//
// The dimensions are strings rather than *uuid.UUID for two reasons: it keeps
// this package free of any dependency (see the package doc), and it makes the
// key directly usable as a Go map key, which a pointer-bearing struct is not —
// two *uuid.UUID pointing at equal UUIDs would hash differently and silently
// split one position into two.
//
// Grouping by this struct with "" standing in for NULL reproduces the closing
// balance query's IS NOT DISTINCT FROM semantics on the nullable dimensions.
type PositionKey struct {
	StockItemID string
	// GodownID is "" when the movement carries no godown dimension (SQL NULL).
	GodownID string
	// BatchID is "" when the movement carries no batch dimension (SQL NULL).
	BatchID string
}

// Movement is one immutable stock_movement row in the form the valuer needs:
// its replay-ordering key, its position, and its quantity/price.
//
// It deliberately does NOT carry the row id, the company id, or the amount
// column. The valuer must not be able to prefer a stored amount over the one it
// derives (see Value) — that is how a materialised value creeps back in.
type Movement struct {
	// --- replay ordering key -------------------------------------------------
	// Replay sorts by exactly (VoucherDate, VoucherNo, VersionCreatedAt, LineNo,
	// VoucherVersionID) and applies that itself, so correctness can never depend
	// on the order a caller happened to read rows in.

	// VoucherDate is voucher_version.voucher_date (a DATE; pass it as midnight).
	VoucherDate time.Time
	// VoucherNo is voucher_version.voucher_no.
	VoucherNo string
	// VersionCreatedAt is voucher_version.created_at — it is what makes an ALTER
	// land after the version it replaced on the same date and number.
	VersionCreatedAt time.Time
	// LineNo is stock_movement.line_no, UNIQUE per voucher_version.
	LineNo int
	// VoucherVersionID is the final tiebreak. It exists because the four keys
	// above are NOT provably unique: two versions of two different vouchers can
	// share a date, a number (imported data repeats numbers across years) and a
	// created_at truncated to the same microsecond. Without a total order, two
	// replays of the same data could disagree, which is precisely the bug
	// TestReplayIsOrderDeterministic exists to forbid.
	VoucherVersionID string

	// --- position ------------------------------------------------------------
	Position PositionKey

	// --- quantity and price --------------------------------------------------
	// Direction is IN or OUT; Qty is ALWAYS positive.
	Direction Direction
	// Qty is stock_movement.qty, NUMERIC(19,4), always > 0.
	Qty decimal.Decimal
	// Rate is stock_movement.rate, the gross per-unit price before the line
	// discount. It is meaningful on an inward (it is what the stock cost) and
	// IGNORED on an outward: an outward consumes at the position's current
	// weighted-average cost, never at the price it was sold for. Booking the
	// sale price as the cost is the single most common inventory-valuation bug
	// and it silently zeroes gross profit.
	Rate decimal.Decimal
	// DiscountPct is stock_movement.discount_pct, 0..100 inclusive.
	DiscountPct decimal.Decimal
}

// Value is the movement's own money value: quantity times rate, net of the line
// discount, rounded to the 4 decimal places the NUMERIC(19,4) columns carry.
//
// This is THE definition of what a movement is worth, and stock_movement.amount
// must equal it. It lives here rather than being read back from the amount
// column so that there is exactly one arithmetic — a report that trusted the
// stored amount and a valuer that recomputed it would disagree by a paisa on
// every discounted line and nobody would be able to say which was right.
func (m Movement) Value() decimal.Decimal {
	gross := m.Qty.Mul(m.Rate)
	if m.DiscountPct.IsZero() {
		return round(gross)
	}
	return round(gross.Mul(hundred.Sub(m.DiscountPct)).Div(hundred))
}

// Opening is the stock_opening row for a position: the quantity and cost the
// books started with, before any movement. It is folded into a replay as the
// FIRST inward (see Replay), which is why it needs no direction of its own.
//
// A zero-quantity Opening is the normal case and contributes nothing.
type Opening struct {
	Position PositionKey
	Qty      decimal.Decimal
	Rate     decimal.Decimal
}

// Value is the opening's money value at 4 dp — quantity times rate. Openings
// carry no discount.
func (o Opening) Value() decimal.Decimal {
	return round(o.Qty.Mul(o.Rate))
}

// Position is one replayed (item, godown, batch) bucket: the Stock Summary row.
// Every field is DERIVED — none of them is read from or written to any column.
//
// The Opening/Inward/Outward/Closing quartet is the shape TallyPrime's Stock
// Summary shows, and it satisfies the conservation identity
//
//	OpeningValue + InwardValue - OutwardValue == ClosingValue
//
// which TestValueIsConserved pins. A valuer that invented or destroyed value
// (the classic symptom of consuming an outward at the sale rate) breaks it.
type Position struct {
	Key PositionKey

	// OpeningQty/OpeningValue are the stock_opening row, zero when absent.
	OpeningQty   decimal.Decimal
	OpeningValue decimal.Decimal

	// InwardQty/InwardValue are the sum of the inward movements (NOT including
	// the opening, which is reported separately above).
	InwardQty   decimal.Decimal
	InwardValue decimal.Decimal

	// OutwardQty is the quantity issued; OutwardValue is what those issues
	// CONSUMED at the running weighted-average cost — not what they sold for.
	OutwardQty   decimal.Decimal
	OutwardValue decimal.Decimal

	// ClosingQty/ClosingValue are the position after the last movement.
	ClosingQty   decimal.Decimal
	ClosingValue decimal.Decimal
	// ClosingRate is ClosingValue/ClosingQty rounded to 4 dp for display, and
	// zero when ClosingQty is zero. It is a PRESENTATION number: never multiply
	// it back out to re-derive a value, because 4 dp of rate times a large
	// quantity does not reproduce ClosingValue.
	ClosingRate decimal.Decimal
}
