package profile

import (
	"sort"
	"time"

	"github.com/shopspring/decimal"
)

// stats.go is the PURE half of the feature jobs: no database, no clock, no
// map-order dependence. Every function here is a table test in stats_test.go,
// so the statistics can be argued about without seeding a book.
//
// ⛔ MONEY STAYS decimal.Decimal END TO END. Days and counts are ints; ratios
// are decimals rounded at the edge. There is no float64 anywhere in this
// file, and semgrep's money-must-not-be-float rule would say so if there were
// — but the reason is not the linter: a p50 of lags is fine as a float, an
// ABC share computed in float is a class boundary that moves with the order
// items were summed in.

// percentile returns the nearest-rank percentile (p in 1..100) of sorted
// ints. Nearest-rank, not interpolated, because the answer must be a lag that
// actually HAPPENED — "your median customer pays in 37.5 days" describes no
// invoice anyone can open.
func percentile(sorted []int, p int) int {
	if len(sorted) == 0 {
		return 0
	}
	// rank = ceil(p/100 * n), 1-based.
	rank := (p*len(sorted) + 99) / 100
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

// meanDays is the arithmetic mean of ints as a 1-dp decimal.
func meanDays(xs []int) decimal.Decimal {
	if len(xs) == 0 {
		return decimal.Zero
	}
	sum := 0
	for _, x := range xs {
		sum += x
	}
	return decimal.NewFromInt(int64(sum)).Div(decimal.NewFromInt(int64(len(xs)))).Round(1)
}

// bill is one bill-wise reference as the lag job sees it: the date and
// amount it was RAISED, and each settlement against it in date order.
type bill struct {
	raisedOn    time.Time
	amount      decimal.Decimal
	settlements []settlement
}

type settlement struct {
	on        time.Time
	amount    decimal.Decimal
	voucherID string
}

// settledLag returns the days from raising a bill to the settlement that
// FIRST brought the cumulative amount settled to at least the bill amount,
// and the voucher that did it. ok=false for a bill still open.
//
// A part-payment does not end the lag: a customer who pays 10% on day 3 and
// the rest on day 90 took 90 days, and a lag that read "3" would describe the
// customer the owner wishes they had.
func settledLag(b bill) (days int, voucherID string, ok bool) {
	ss := append([]settlement(nil), b.settlements...)
	sort.SliceStable(ss, func(i, j int) bool { return ss[i].on.Before(ss[j].on) })
	cum := decimal.Zero
	for _, s := range ss {
		cum = cum.Add(s.amount)
		if cum.GreaterThanOrEqual(b.amount) {
			d := int(s.on.Sub(b.raisedOn).Hours() / 24)
			if d < 0 {
				// A settlement dated before the bill (an advance adjusted later
				// is booked as ADVANCE, not AGAINST, so this is a data-entry
				// back-date). Count it as same-day rather than a negative lag.
				d = 0
			}
			return d, s.voucherID, true
		}
	}
	return 0, "", false
}

// ---------------------------------------------------------------------------
// ABC / XYZ
// ---------------------------------------------------------------------------

// abcItem is one item's consumption value for ABC.
type abcItem struct {
	key   string
	value decimal.Decimal
}

// ABC boundaries: the conventional 80/15/5 Pareto split on cumulative share
// of consumption value. An item is A while the cumulative share BEFORE it is
// under 80% — so the item that crosses the line is A, which is the usual
// reading ("the items that make up the first 80%").
var (
	abcA = decimal.NewFromInt(80)
	abcB = decimal.NewFromInt(95)
)

// abcClasses returns key -> "A"|"B"|"C". Items with zero value are C. Ties in
// value break on key, so the classification is independent of input order.
func abcClasses(items []abcItem) map[string]string {
	sorted := append([]abcItem(nil), items...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if !sorted[i].value.Equal(sorted[j].value) {
			return sorted[i].value.GreaterThan(sorted[j].value)
		}
		return sorted[i].key < sorted[j].key
	})
	total := decimal.Zero
	for _, it := range sorted {
		if it.value.IsPositive() {
			total = total.Add(it.value)
		}
	}
	out := make(map[string]string, len(sorted))
	cum := decimal.Zero
	for _, it := range sorted {
		if !it.value.IsPositive() || total.IsZero() {
			out[it.key] = "C"
			continue
		}
		before := cum.Mul(decimal.NewFromInt(100)).Div(total)
		switch {
		case before.LessThan(abcA):
			out[it.key] = "A"
		case before.LessThan(abcB):
			out[it.key] = "B"
		default:
			out[it.key] = "C"
		}
		cum = cum.Add(it.value)
	}
	return out
}

// coefficientOfVariation is stddev/mean of a series (population stddev), to 4
// dp. Computed in decimal with a Newton square root so the XYZ boundary does
// not depend on float rounding. ok=false when the mean is zero.
func coefficientOfVariation(xs []decimal.Decimal) (decimal.Decimal, bool) {
	if len(xs) == 0 {
		return decimal.Zero, false
	}
	n := decimal.NewFromInt(int64(len(xs)))
	sum := decimal.Zero
	for _, x := range xs {
		sum = sum.Add(x)
	}
	mean := sum.DivRound(n, 8)
	if mean.IsZero() {
		return decimal.Zero, false
	}
	ss := decimal.Zero
	for _, x := range xs {
		d := x.Sub(mean)
		ss = ss.Add(d.Mul(d))
	}
	variance := ss.DivRound(n, 8)
	return sqrtDecimal(variance).DivRound(mean, 4), true
}

func sqrtDecimal(x decimal.Decimal) decimal.Decimal {
	if !x.IsPositive() {
		return decimal.Zero
	}
	g := x
	if g.LessThan(decimal.NewFromInt(1)) {
		g = decimal.NewFromInt(1)
	}
	two := decimal.NewFromInt(2)
	for i := 0; i < 60; i++ {
		next := g.Add(x.DivRound(g, 12)).DivRound(two, 12)
		if next.Sub(g).Abs().LessThan(decimal.New(1, -10)) {
			return next
		}
		g = next
	}
	return g
}

// XYZ boundaries on the coefficient of variation of MONTHLY outward quantity:
// X steady (<= 0.5), Y variable (<= 1.0), Z erratic. Months with no movement
// in the window count as zeros — an item sold in one month of twelve is
// erratic, and dropping the empty months would call it steady.
var (
	xyzX = decimal.RequireFromString("0.5")
	xyzY = decimal.RequireFromString("1.0")
)

func xyzClass(cv decimal.Decimal) string {
	switch {
	case cv.LessThanOrEqual(xyzX):
		return "X"
	case cv.LessThanOrEqual(xyzY):
		return "Y"
	default:
		return "Z"
	}
}

// ---------------------------------------------------------------------------
// Recurring streams
// ---------------------------------------------------------------------------

// occurrence is one dated amount on a candidate stream.
type occurrence struct {
	on        time.Time
	amount    decimal.Decimal
	voucherID string
}

// Stream-detection tolerances, straight from PLAN-AGENTIC W3: ±3 days on the
// interval, ±5% on the amount, at least 3 occurrences.
const (
	streamDayTolerance  = 3
	streamMinOccurrence = 3
)

var streamAmountTolerance = decimal.RequireFromString("0.05")

// detectedStream is a recurring stream found in a series.
type detectedStream struct {
	occurrences  []occurrence
	intervalDays int
	medianAmount decimal.Decimal
	total        decimal.Decimal
	cadence      string
}

// detectStream finds the LONGEST run of consecutive occurrences in which
// every interval is within ±3 days of the run's median interval and every
// amount within ±5% of the run's median amount, and returns it when it has
// at least 3 occurrences.
//
// WHY THE LONGEST RUN AND NOT "ALL OF THEM": a rent that was renegotiated in
// October is two streams, and the one the owner cares about is the current
// one. The run is searched over contiguous windows of the date-sorted series,
// so a one-off payment to the same ledger breaks the run rather than joining
// it — the tolerance test is applied to every member.
//
// Same-day duplicates (two payments on one date) are kept as separate
// occurrences and will break an interval test (interval 0), which is right:
// "we paid rent twice on the 5th" is not a monthly stream, it is an anomaly.
func detectStream(series []occurrence) (detectedStream, bool) {
	xs := append([]occurrence(nil), series...)
	sort.SliceStable(xs, func(i, j int) bool {
		if !xs[i].on.Equal(xs[j].on) {
			return xs[i].on.Before(xs[j].on)
		}
		return xs[i].voucherID < xs[j].voucherID
	})
	best := []occurrence(nil)
	for i := 0; i < len(xs); i++ {
		for j := len(xs); j-i >= streamMinOccurrence && j-i > len(best); j-- {
			if isStream(xs[i:j]) {
				best = xs[i:j]
				break
			}
		}
	}
	if len(best) < streamMinOccurrence {
		return detectedStream{}, false
	}
	ds := detectedStream{occurrences: best}
	ds.intervalDays = medianInt(intervals(best))
	ds.medianAmount = medianDecimal(amounts(best))
	for _, o := range best {
		ds.total = ds.total.Add(o.amount)
	}
	ds.cadence = cadenceOf(ds.intervalDays)
	return ds, true
}

func isStream(run []occurrence) bool {
	iv := intervals(run)
	mi := medianInt(iv)
	if mi <= 0 {
		return false
	}
	for _, d := range iv {
		if abs(d-mi) > streamDayTolerance {
			return false
		}
	}
	ma := medianDecimal(amounts(run))
	if !ma.IsPositive() {
		return false
	}
	band := ma.Mul(streamAmountTolerance)
	for _, o := range run {
		if o.amount.Sub(ma).Abs().GreaterThan(band) {
			return false
		}
	}
	return true
}

func intervals(run []occurrence) []int {
	out := make([]int, 0, len(run))
	for k := 1; k < len(run); k++ {
		out = append(out, int(run[k].on.Sub(run[k-1].on).Hours()/24))
	}
	return out
}

func amounts(run []occurrence) []decimal.Decimal {
	out := make([]decimal.Decimal, len(run))
	for k, o := range run {
		out[k] = o.amount
	}
	return out
}

func medianInt(xs []int) int {
	if len(xs) == 0 {
		return 0
	}
	s := append([]int(nil), xs...)
	sort.Ints(s)
	return s[(len(s)-1)/2]
}

// medianDecimal is the LOWER median (an amount that occurred), for the same
// reason percentile is nearest-rank.
func medianDecimal(xs []decimal.Decimal) decimal.Decimal {
	if len(xs) == 0 {
		return decimal.Zero
	}
	s := append([]decimal.Decimal(nil), xs...)
	sort.Slice(s, func(i, j int) bool { return s[i].LessThan(s[j]) })
	return s[(len(s)-1)/2]
}

// cadenceOf names an interval. The bands are wide enough for calendar
// months (28-31) and a ±3-day slip.
func cadenceOf(days int) string {
	switch {
	case days >= 5 && days <= 9:
		return "weekly"
	case days >= 12 && days <= 17:
		return "fortnightly"
	case days >= 25 && days <= 35:
		return "monthly"
	case days >= 85 && days <= 95:
		return "quarterly"
	case days >= 355 && days <= 375:
		return "yearly"
	default:
		return "other"
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
