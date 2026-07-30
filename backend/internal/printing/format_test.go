package printing

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// dec is a test helper: a decimal from its literal string, so no test in this
// file ever writes a float literal.
func dec(t *testing.T, s string) decimal.Decimal {
	t.Helper()
	d, err := decimal.NewFromString(s)
	if err != nil {
		t.Fatalf("test fixture %q is not a decimal: %v", s, err)
	}
	return d
}

func TestPrintedMoneyUsesIndianGroupingAtEveryBoundary(t *testing.T) {
	// Indian grouping: the last three digits, then TWOS. A lakh is 1,00,000.
	// Thousands-grouping (1,234,567.89) is the wrong-country bug this table
	// exists to catch, so every boundary where the two conventions diverge is
	// present.
	cases := []struct{ in, want string }{
		{"0", "0.00"},
		{"0.05", "0.05"},
		{"1", "1.00"},
		{"99.99", "99.99"},
		{"100", "100.00"},
		{"999", "999.00"},
		{"1000", "1,000.00"},
		{"9999", "9,999.00"},
		{"10000", "10,000.00"},
		{"99999", "99,999.00"},
		{"100000", "1,00,000.00"},   // one lakh
		{"1000000", "10,00,000.00"}, // ten lakh
		{"10000000", "1,00,00,000.00"} /* one crore */, {"123456789.99", "12,34,56,789.99"},
		{"1234567.89", "12,34,567.89"},
		// 15 integer digits: the last three, then SIX groups of two.
		{"999999999999999.99", "99,99,99,99,99,99,999.99"},
	}
	for _, c := range cases {
		if got := FormatMoney(dec(t, c.in)); got != c.want {
			t.Errorf("the printed amount for %s reads %q; an Indian invoice must show %q (last three digits, then groups of two)", c.in, got, c.want)
		}
	}
}

func TestACreditNoteTotalPrintsWithALeadingMinusNotParentheses(t *testing.T) {
	// A credit note's total is negative on the face of the document. The pinned
	// convention is a leading minus; if anyone switches to the accountant's
	// parenthesis this fails and they must change the doc comment with it.
	got := FormatMoney(dec(t, "-123456.78"))
	if got != "-1,23,456.78" {
		t.Fatalf("a credit note total of -123456.78 prints as %q; the pinned convention is %q", got, "-1,23,456.78")
	}
	if strings.ContainsAny(got, "()") {
		t.Errorf("the printed amount %q uses the parenthesis convention; a bracketed figure cannot be told from a footnote marker in the extracted PDF text", got)
	}
}

func TestAnAmountThatRoundsToZeroFromBelowNeverPrintsNegativeZero(t *testing.T) {
	for _, in := range []string{"-0.001", "-0.004", "-0.0001"} {
		if got := FormatMoney(dec(t, in)); got != "0.00" {
			t.Errorf("%s prints as %q; nothing on a document may display negative zero, so it must read %q", in, got, "0.00")
		}
	}
}

func TestPrintedMoneyQuantizesTheLedgersFourDecimalPlacesHalfAwayFromZero(t *testing.T) {
	// The ledger stores NUMERIC(19,4). What lands on paper is two places, and
	// the half-way case must go AWAY FROM ZERO in both directions so a debit and
	// its mirroring credit round by the same amount.
	cases := []struct{ in, want string }{
		{"1234.5650", "1,234.57"},
		{"-1234.5650", "-1,234.57"},
		{"1234.5649", "1,234.56"},
		{"999.9950", "1,000.00"}, // the carry crosses the grouping boundary
		{"0.0050", "0.01"},
	}
	for _, c := range cases {
		if got := FormatMoney(dec(t, c.in)); got != c.want {
			t.Errorf("the ledger value %s prints as %q but the printed total must be %q, or the document disagrees with the books by 0.01", c.in, got, c.want)
		}
	}
}

func TestPrintedQuantityTrimsTrailingZerosAndKeepsAtMostThreePlaces(t *testing.T) {
	cases := []struct{ in, want string }{
		{"12", "12"},
		{"12.0000", "12"},
		{"12.5", "12.5"},
		{"12.500", "12.5"},
		{"0.125", "0.125"},
		{"0.1255", "0.126"}, // half away from zero at the third place
		{"0", "0"},
		{"-2.5", "-2.5"},
		{"1000.000", "1000"}, // trimming must never touch digits left of the point
	}
	for _, c := range cases {
		if got := FormatQty(dec(t, c.in)); got != c.want {
			t.Errorf("a quantity of %s prints as %q; the delivery note must read %q", c.in, got, c.want)
		}
	}
}

func TestALargeQuantityIsNotDigitGroupedSoItCannotBeMisreadAsMoney(t *testing.T) {
	got := FormatQty(dec(t, "1234567.5"))
	if got != "1234567.5" {
		t.Fatalf("a quantity of 1234567.5 prints as %q; quantities are deliberately ungrouped so a count cannot be read as an amount, so it must be %q", got, "1234567.5")
	}
}

func TestATaxRateNeverPrintsWithTrailingZeros(t *testing.T) {
	// Rule 46(l) puts the rate of tax per head on the invoice. These are the
	// rates the NUMERIC(19,4) column hands over as "18.0000" and "0.2500".
	cases := []struct{ in, want string }{
		{"0", "0"},
		{"0.1000", "0.1"},  // concessional rate, gems and jewellery
		{"0.2500", "0.25"}, // concessional rate, rough diamonds
		{"1.5000", "1.5"},
		{"2.5000", "2.5"},
		{"6.0000", "6"},
		{"9.0000", "9"},
		{"14.0000", "14"},
		{"18.0000", "18"},
		{"28.0000", "28"},
	}
	for _, c := range cases {
		if got := FormatRate(dec(t, c.in)); got != c.want {
			t.Errorf("the tax rate %s prints as %q; the rate column must read %q", c.in, got, c.want)
		}
	}
	if got := FormatRate(dec(t, "18")); strings.Contains(got, "%") {
		t.Errorf("FormatRate returned %q; the per-cent sign belongs to the template, and printing it here doubles it under a column already headed \"Rate (%%)\"", got)
	}
}

func TestADocumentDateIsPrintedTheWayTheGSTPortalShowsIt(t *testing.T) {
	cases := []struct {
		in   time.Time
		want string
	}{
		{time.Date(2026, time.April, 15, 0, 0, 0, 0, time.UTC), "15-Apr-2026"},
		{time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC), "02-Jan-2026"}, // day is zero-padded
		{time.Date(2025, time.December, 31, 23, 59, 59, 0, time.UTC), "31-Dec-2025"},
	}
	for _, c := range cases {
		if got := FormatDate(c.in); got != c.want {
			t.Errorf("the document date %s prints as %q; it must read %q", c.in.Format(time.RFC3339), got, c.want)
		}
	}
}

func TestAMissingDatePrintsAsBlankNotAsTheYearOne(t *testing.T) {
	if got := FormatDate(time.Time{}); got != "" {
		t.Fatalf("a voucher with no date prints its date as %q; that reads like a real date and can survive review, so a missing date must render blank and be caught by the Rule 46(b) validator", got)
	}
}

func TestPrintingADateNeverShiftsItIntoAnotherFinancialYear(t *testing.T) {
	// 1 April is the first day of the financial year. FormatDate must print the
	// calendar date the value carries and never convert its zone: a shift of a
	// few hours moves this invoice into the PREVIOUS financial year.
	ist := time.FixedZone("IST", 5*3600+1800)
	if got := FormatDate(time.Date(2026, time.April, 1, 0, 30, 0, 0, ist)); got != "01-Apr-2026" {
		t.Errorf("a 1-Apr-2026 IST voucher prints as %q; printing it as 31-Mar-2026 files it in the wrong financial year", got)
	}
	if got := FormatDate(time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC)); got != "01-Apr-2026" {
		t.Errorf("a 1-Apr-2026 UTC voucher prints as %q; want 01-Apr-2026", got)
	}
}

func TestNoFormatterPanicsOnAnAbsurdValue(t *testing.T) {
	// A formatter that panics takes out the print handler for a real invoice, so
	// these are asserted rather than assumed. The largest value the ledger's
	// NUMERIC(19,4) can hold is 15 integer digits; the 1e30 and 1e-30 cases are
	// beyond anything storable and still must not panic. Note in particular that
	// 1e30 exceeds int64, which is why no formatter here uses decimal.IntPart().
	huge := dec(t, "999999999999999.99")
	if got := FormatMoney(huge); got != "99,99,99,99,99,99,999.99" {
		t.Errorf("the largest storable amount prints as %q; want %q", got, "99,99,99,99,99,99,999.99")
	}
	// 1e30 is 31 integer digits: the last three, then FOURTEEN groups of two.
	beyondInt64 := decimal.New(1, 30)
	if got := FormatMoney(beyondInt64); got != "10,00,00,00,00,00,00,00,00,00,00,00,00,00,000.00" {
		t.Errorf("1e30 prints as %q; an amount past int64 must render its own digits, not int64 wraparound (decimal.IntPart() would give 5076944270305263616)", got)
	}
	for _, in := range []string{"999999999999999.99", "-999999999999999.99", "1e-30", "-1e-30", "0"} {
		d := dec(t, in)
		_ = FormatMoney(d)
		_ = FormatQty(d)
		_ = FormatRate(d)
		_ = AmountInWords(d)
	}
}

// TestThePrintingFormattersContainNoFloatingPointArithmetic is a STATIC gate over
// this slice's own source. It parses format.go and words.go WITHOUT comments (so
// the prose that explains why floats are banned cannot trip it) and fails on any
// float type, any float-producing decimal method, and on importing fmt at all —
// %v on a decimal prints its internal form and %f converts through float64.
//
// It is here rather than in a review checklist because the failure it prevents is
// a paisa of drift between the printed total and the ledger, which nobody finds
// by reading a diff six months later.
func TestThePrintingFormattersContainNoFloatingPointArithmetic(t *testing.T) {
	banned := map[string]string{
		"float32":        "a float type",
		"float64":        "a float type",
		"ParseFloat":     "parses money through a float",
		"FormatFloat":    "renders money through a float",
		"Float64":        "converts a decimal to a float",
		"InexactFloat64": "converts a decimal to a float, inexactly and by name",
		"IntPart":        "converts a decimal through int64 and SILENTLY WRAPS above 9.22e18",
		"NewFromFloat":   "builds a decimal from a float",
	}
	for _, file := range []string{"format.go", "words.go"} {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, file, nil, 0 /* comments discarded */)
		if err != nil {
			t.Fatalf("cannot parse %s: %v", file, err)
		}
		for _, imp := range f.Imports {
			if imp.Path.Value == `"fmt"` {
				t.Errorf("%s imports fmt: money must be rendered with decimal.StringFixed, never %%v (which prints the decimal's internal form) or %%f (which converts through float64)", file)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			if why, bad := banned[id.Name]; bad {
				t.Errorf("%s:%d uses %s, which %s; the printed amount must be derived from the decimal's own digits", file, fset.Position(id.Pos()).Line, id.Name, why)
			}
			return true
		})
	}
}
