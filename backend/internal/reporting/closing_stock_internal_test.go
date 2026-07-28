package reporting

// closing_stock_internal_test.go — the two pieces of the closing-stock
// derivation that are PURE, tested directly and with no database.
//
// This is the only file in internal/reporting that lives in `package reporting`
// rather than `package reporting_test`. That is deliberate, not an oversight:
// previousDay and totalClosingValue are unexported and the external test package
// cannot see them, and routing these assertions through BalanceSheet instead
// would test them only at whatever dates a fixture happened to use — which for a
// date function means never at a leap boundary and never at the century rule.
// The two packages coexist legally in one directory; everything that needs a
// database stays in reporting_test where the fixtures are.

import (
	"math"
	"testing"

	"github.com/shopspring/decimal"
)

// TestPreviousDayCrossesMonthYearAndLeapBoundaries pins the date arithmetic the
// P&L's opening-stock term rests on.
//
// A P&L over [d0,d1] must take its opening stock from d0-1, so every one of
// these rows is a real reporting date somebody will ask for: 1 April is the
// Indian financial-year start and therefore the single most common `from` in
// this product, 1 January is the year boundary, and 1 March is the one that
// separates a correct implementation from a hand-rolled decrement — including
// the century rule, where 1900 is NOT a leap year and 2000 IS.
func TestPreviousDayCrossesMonthYearAndLeapBoundaries(t *testing.T) {
	t.Parallel()

	ok := []struct {
		name string
		in   string
		want string
		why  string
	}{
		{"financial year start", "2026-04-01", "2026-03-31", "the FY opening stock would be read from the wrong month"},
		{"calendar year start", "2026-01-01", "2025-12-31", "the January P&L would open on a day in the wrong year"},
		{"leap February", "2024-03-01", "2024-02-29", "29 February would be skipped and a whole day of movement lost"},
		{"non-leap February", "2023-03-01", "2023-02-28", "a 29 February that does not exist would be reported on"},
		{"divisible by 400 is a leap year", "2000-03-01", "2000-02-29", "the 400-year exception to the century rule would be missed"},
		{"the century rule", "1900-03-01", "1900-02-28", "1900 would be treated as a leap year, which it is not"},
		{"ordinary day", "2026-12-31", "2026-12-30", "even the easy case would be wrong"},
		{"padded input off the wire", "  2026-04-01  ", "2026-03-31", "a date with surrounding whitespace would be refused, matching nullDate's tolerance"},
	}
	for _, tc := range ok {
		t.Run(tc.name, func(t *testing.T) {
			got, err := previousDay(tc.in)
			if err != nil {
				t.Fatalf("previousDay(%q) failed with %v; a P&L starting %s could not take its opening stock at all (%s)", tc.in, err, tc.in, tc.why)
			}
			if got != tc.want {
				t.Fatalf("the day before %q came out as %q, want %q — so a P&L starting %s would take its opening stock from the wrong day: %s",
					tc.in, got, tc.want, tc.in, tc.why)
			}
			if len(got) != 10 {
				t.Fatalf("previousDay(%q) = %q is %d characters, not the 10 zero-padded ones loadStockOpenings' lexical `asOn < begins` comparison depends on; a short date sorts before every real one and would silently drop the opening position",
					tc.in, got, len(got))
			}
		})
	}

	bad := []struct {
		name string
		in   string
		why  string
	}{
		{"empty means unbounded", "", "\"\" means UNBOUNDED on this seam; passing it through would ask for the position of everything ever posted and subtract the entire warehouse as opening stock"},
		{"day out of range", "2026-04-31", "31 April does not exist; normalising it to 1 May would silently move the period bound"},
		{"not zero padded", "2026-4-1", "an unpadded date breaks the lexical comparison in loadStockOpenings even when it parses"},
		{"day-first order", "01-04-2026", "a day-first date read as ISO would name a completely different day"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			got, err := previousDay(tc.in)
			if err == nil {
				t.Fatalf("previousDay(%q) returned %q with no error, but %s", tc.in, got, tc.why)
			}
		})
	}
}

// TestTotalClosingValueIsExactWhereFloatWouldDrift pins that the closing-stock
// fold is decimal arithmetic and stays decimal.
//
// The test does not merely assert the right total; it also computes the float64
// sum of the SAME values and asserts the two DISAGREE. A float-drift fixture in
// this project once failed to bite because the numbers chosen happened to add up
// the same way in binary, so the test proved nothing while looking like it
// proved everything. These values are chosen so the divergence is real, and the
// divergence assertion fails loudly if a future edit picks friendlier numbers.
func TestTotalClosingValueIsExactWhereFloatWouldDrift(t *testing.T) {
	t.Parallel()

	// 0.1 + 0.2 + 1234567890.1234 - 0.3 + 0.0001, derived by hand in exact
	// decimal: 0.1 + 0.2 - 0.3 = 0 exactly, leaving 1234567890.1234 + 0.0001.
	const want = "1234567890.1235"
	values := []string{"0.1", "0.2", "1234567890.1234", "-0.3", "0.0001"}

	rows := make([]StockPositionRow, 0, len(values))
	for _, v := range values {
		var row StockPositionRow
		row.ClosingValue = decimal.RequireFromString(v)
		rows = append(rows, row)
	}

	got := totalClosingValue(rows)
	if got.String() != want {
		t.Fatalf("closing stock folded to %s, want %s — the Balance Sheet would carry that asset at the wrong value and the Stock Summary would print a different total for the same position",
			got.String(), want)
	}

	// The divergence proof. float64 is used ONLY here, ONLY to demonstrate that
	// it is the wrong tool; it must never appear in the derivation itself.
	var floatSum float64
	for _, v := range values {
		f, _ := decimal.RequireFromString(v).Float64()
		floatSum += f
	}
	exact, _ := got.Float64()
	if floatSum == exact {
		t.Fatalf("the float64 sum (%.17g) equals the decimal total (%s), so this fixture CANNOT detect a float64 accumulator — the fixture is the finding: pick values that actually diverge",
			floatSum, got.String())
	}
	if d := math.Abs(floatSum - exact); d == 0 {
		t.Fatalf("float64 and decimal agreed to the bit; this fixture proves nothing")
	} else {
		t.Logf("float64 sum %.17g differs from the exact decimal total %s by %g — which is the paisa a float accumulator would lose", floatSum, got.String(), d)
	}
}
