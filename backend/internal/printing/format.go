package printing

import (
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// This file holds the FIVE formatters every template on paper goes through, and
// nothing else. They are PURE: a decimal (or a time) in, a string out, no state,
// no locale lookup, no error return and no panic on any input.
//
// WHY THE FORMATTERS ARE HERE AND NOT IN THE RENDERER. printing.Row carries
// ALREADY-FORMATTED cells (see doc.go), so the seam where a number becomes text
// is a caller's choice. If each caller reached for its own fmt.Sprintf, the
// Trial Balance and the invoice would eventually group, round or sign the same
// amount differently and a reconciliation would go looking for a bug that was
// only ever a format string. There is one place, and it is this one.
//
// NO FLOAT, ANYWHERE. Every function takes decimal.Decimal and renders it via
// StringFixed, which quantizes the stored big.Int exactly. There is no
// float64, no .Float64(), no strconv.ParseFloat and no %f in this package. A
// printed total that disagrees with the ledger by a paisa is an invoice a
// customer disputes.
//
// AND NO IntPart(). decimal.Decimal.IntPart() converts through int64 and
// SILENTLY WRAPS above 9.22e18 — decimal.New(1, 30).IntPart() returns
// 5076944270305263616, a plausible-looking wrong number with no error. Every
// function below works on the DIGIT STRING that StringFixed produces, so a
// value larger than int64 renders as itself instead of as garbage.

// FormatMoney renders a money amount the way it appears on an Indian invoice:
// INDIAN DIGIT GROUPING and exactly two decimal places, always both of them.
//
//	         0    -> "0.00"
//	      1000    -> "1,000.00"
//	    100000    -> "1,00,000.00"          (one lakh)
//	  10000000    -> "1,00,00,000.00"       (one crore)
//	1234567.89    -> "12,34,567.89"
//
// INDIAN GROUPING IS NOT THOUSANDS GROUPING. The last three digits form one
// group and everything to their left is grouped in TWOS, so a lakh reads
// 1,00,000 and never 100,000. This is the single most common wrong-looking
// number on an Indian invoice produced by software written elsewhere.
//
// SCALE. The ledger stores NUMERIC(19,4) and decimal.StringFixed(4) appears
// throughout this codebase, so the value handed here routinely carries four
// decimal places. FormatMoney quantizes to two using shopspring's rounding,
// which is HALF AWAY FROM ZERO: 1234.565 -> "1,234.57", -1234.565 ->
// "-1,234.57". AmountInWords quantizes IDENTICALLY, which is what keeps the
// figures and the words on the same invoice from disagreeing
// (TestWordsAndFiguresAgreeOnAFourDecimalLedgerValue pins that).
//
// NEGATIVES USE A LEADING MINUS: "-1,23,456.78". The accountant's parenthesis
// convention "(1,23,456.78)" was considered and rejected for two reasons: a
// bracketed figure is indistinguishable from a footnote marker in the
// uncompressed PDF text a golden test greps, and a credit note's total is
// legitimately negative on the face of the document, where a reader needs to see
// the sign rather than infer it. Pinned by
// TestACreditNoteTotalPrintsWithALeadingMinusNotParentheses.
//
// A value that rounds to zero from below prints "0.00", never "-0.00" — nothing
// on paper should display negative zero.
func FormatMoney(d decimal.Decimal) string {
	s := d.StringFixed(2) // half away from zero; never yields "-0.00"

	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")

	intDigits, frac := splitFixed(s)
	out := groupIndian(intDigits) + "." + frac
	if neg {
		out = "-" + out
	}
	return out
}

// FormatQty renders a quantity: up to THREE decimal places, with trailing zeros
// trimmed, so a count reads as a count and a weight reads as a weight.
//
//	   12     -> "12"
//	   12.5   -> "12.5"
//	    0.125 -> "0.125"
//	12.0000   -> "12"
//
// THREE PLACES, because that is the scale of the quantity columns in this
// codebase and the scale a delivery note needs for grams and metres. A fourth
// place is rounded half away from zero (0.1255 -> "0.126").
//
// NO DIGIT GROUPING, deliberately, and this is a decision worth stating: a
// quantity is a count of things, not an amount of money, and on a line where
// "1234567.5" sits next to "12,34,567.89" the reader can tell at a glance which
// column is rupees. Grouping both makes the two visually interchangeable, which
// is how a quantity gets read as a value. Pinned by
// TestALargeQuantityIsNotDigitGroupedSoItCannotBeMisreadAsMoney.
//
// A negative quantity (a return leg) keeps its leading minus.
func FormatQty(d decimal.Decimal) string {
	return trimTrailingZeros(d.StringFixed(3))
}

// FormatDate renders a document date as Tally and the GST portal show it:
// two-digit day, three-letter month, four-digit year, hyphen separated.
//
//	15 April 2026 -> "15-Apr-2026"
//
// It does NOT convert time zones. The value it is handed comes from a DATE
// column and carries the calendar date the voucher was posted on; shifting it
// into another location is how a 1st-of-April invoice lands in the previous
// financial year. The date printed is the date the value already holds.
//
// THE ZERO TIME RENDERS AS THE EMPTY STRING. time.Time{} formatted literally
// would print "01-Jan-0001", which reads like a real (if absurd) date and can
// survive review on a document nobody looks at twice; an empty date cell is
// visibly missing, and the Rule 46(b) validator refuses to print without a
// date anyway. Pinned by TestAMissingDatePrintsAsBlankNotAsTheYearOne.
func FormatDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("02-Jan-2006")
}

// FormatRate renders a TAX RATE with no trailing zeros: "18", "2.5", "0.25".
//
// Rule 46(l) requires the rate of tax per head to appear on the invoice, and
// every GST rate in force is expressible in at most three decimal places (the
// 0.1% and 0.25% concessional rates for gems and diamonds are the awkward ones).
// A rate printed as "18.00" is not wrong, it just is not how a rate is written,
// and "18.0000" — which is what the NUMERIC(19,4) column would hand over
// verbatim — makes a tax column unreadable.
//
// It does NOT append "%". The template owns the symbol, because the same rate
// value appears in a column already headed "Rate (%)" and doubling the sign
// there is worse than omitting it.
func FormatRate(d decimal.Decimal) string {
	return trimTrailingZeros(d.StringFixed(3))
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// splitFixed splits the output of StringFixed(n) into its integer digits and its
// fractional digits. StringFixed always emits the point for n > 0, but the
// no-point case is handled rather than assumed: a formatter that indexes past
// the end of a string takes out the print handler for a real invoice.
func splitFixed(s string) (intPart, frac string) {
	if i := strings.IndexByte(s, '.'); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// groupIndian inserts commas into a string of digits using the Indian system:
// the last three digits, then groups of TWO to the left.
//
//	"999"       -> "999"
//	"1000"      -> "1,000"
//	"100000"    -> "1,00,000"
//	"123456789" -> "12,34,56,789"
//
// It is string surgery on purpose: the digits arrive from StringFixed, so no
// int64 is involved and a fifteen- or thirty-digit amount groups correctly
// instead of wrapping.
func groupIndian(digits string) string {
	if len(digits) <= 3 {
		return digits
	}
	head, tail := digits[:len(digits)-3], digits[len(digits)-3:]

	// Walk head right-to-left in pairs.
	var parts []string
	for len(head) > 2 {
		parts = append(parts, head[len(head)-2:])
		head = head[:len(head)-2]
	}
	if head != "" {
		parts = append(parts, head)
	}

	var b strings.Builder
	for i := len(parts) - 1; i >= 0; i-- {
		b.WriteString(parts[i])
		b.WriteByte(',')
	}
	b.WriteString(tail)
	return b.String()
}

// trimTrailingZeros drops trailing fractional zeros, and the point with them
// when nothing is left after it. It only ever trims to the RIGHT of the point,
// so "1000.000" becomes "1000" and never "1".
func trimTrailingZeros(s string) string {
	if !strings.ContainsRune(s, '.') {
		return s
	}
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	switch s {
	case "", "-", "-0":
		// Belt and braces. shopspring's StringFixed collapses a value that
		// quantizes to zero to an unsigned "0.000", so -0.0001 does not reach
		// here today; if that ever changes, a delivery note must still print
		// "0" and never "-0", which reads as a data-entry error.
		return "0"
	}
	return s
}
