package printing

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// TestMeasureMatchesTheAdobeAFMWidthsForHandCheckedStrings pins Measure against
// widths added up by hand from the Adobe Core 14 AFM tables.
//
// If this test fails, every column on every printed table is the wrong width:
// Measure is what TextRight and Wrap are built on, so an error here right-aligns
// an amount into the column beside it and truncates a party name mid-word.
func TestMeasureMatchesTheAdobeAFMWidthsForHandCheckedStrings(t *testing.T) {
	cases := []struct {
		name string
		f    Font
		size float64
		s    string
		want float64
		hand string
	}{
		{
			name: "Helvetica 12pt \"Hello\"",
			f:    Helvetica, size: 12, s: "Hello", want: 27.336,
			hand: "H722 + e556 + l222 + l222 + o556 = 2278/1000 em x 12pt",
		},
		{
			name: "Helvetica-Bold 10pt \"Total\" (the label under every invoice)",
			f:    HelveticaBold, size: 10, s: "Total", want: 23.89,
			hand: "T611 + o611 + t333 + a556 + l278 = 2389/1000 em x 10pt",
		},
		{
			name: "Helvetica-Bold 14pt \"INVOICE\" (the document heading)",
			f:    HelveticaBold, size: 14, s: "INVOICE", want: 57.568,
			hand: "I278 + N722 + V667 + O778 + I278 + C722 + E667 = 4112/1000 em x 14pt",
		},
		{
			name: "Courier 10pt \"1234\" is monospaced",
			f:    Courier, size: 10, s: "1234", want: 24.0,
			hand: "4 glyphs x 600/1000 em x 10pt",
		},
	}
	for _, c := range cases {
		got := Measure(c.f, c.size, c.s)
		if diff := got - c.want; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("%s measures %.4fpt but the AFM widths say %.4fpt (%s); every column derived from this width is now wrong",
				c.name, got, c.want, c.hand)
		}
	}
}

// TestHelveticaObliqueMeasuresTheSameAsHelvetica records a real property of the
// base-14 metrics: shearing a face does not change its advance widths. If these
// ever diverge, one of the two tables has been edited by mistake.
func TestHelveticaObliqueMeasuresTheSameAsHelvetica(t *testing.T) {
	const s = "Mehta Trading Co. (Pvt) Ltd"
	up := Measure(Helvetica, 11, s)
	ob := Measure(HelveticaOblique, 11, s)
	if up != ob {
		t.Fatalf("the italic variant of a party name measures %.4fpt against the upright %.4fpt; a mixed-face line will now not line up", ob, up)
	}
}

// TestCourierBoldIsMonospacedLikeCourier guards the fixed-pitch assumption that
// every code column (voucher numbers, HSN codes) relies on for alignment.
func TestCourierBoldIsMonospacedLikeCourier(t *testing.T) {
	for _, f := range []Font{Courier, CourierBold} {
		w := Measure(f, 10, "M")
		for _, s := range []string{"i", "W", "1", ".", " "} {
			if got := Measure(f, 10, s); got != w {
				t.Errorf("%q is %.4fpt wide in a fixed-pitch font where \"M\" is %.4fpt; an HSN column will not line up", s, got, w)
			}
		}
	}
}

// TestWrapNeverReturnsALineWiderThanTheColumn is the whole contract of Wrap: a
// line that overflows its column prints ON TOP of the column beside it, and on a
// tax invoice that means an amount unreadable next to a description.
func TestWrapNeverReturnsALineWiderThanTheColumn(t *testing.T) {
	const maxWidth = 120.0
	inputs := []string{
		"Widget (Large) \\ 50% \"quoted\"",
		"Supply of manpower services for the month of June 2026 at the Bhiwandi warehouse",
		"HSN 998519 - manpower and employment placement services",
		strings.Repeat("a", 200),
		"one  two   three    four",
		"Freight\nInsurance\nLoading charges",
	}
	for _, in := range inputs {
		for _, f := range []Font{Helvetica, HelveticaBold, Courier} {
			for _, line := range Wrap(f, 9, in, maxWidth) {
				if w := Measure(f, 9, line); w > maxWidth+1e-9 {
					t.Errorf("wrapping %q produced the line %q at %.2fpt in a %.0fpt column; it will overprint the next column", in, line, w, maxWidth)
				}
			}
		}
	}
}

// TestWrapHardBreaksAWordLongerThanTheColumnAndTerminates covers the case that
// hangs a server: a single unbroken token wider than the column. The loop must
// consume at least one character per iteration.
func TestWrapHardBreaksAWordLongerThanTheColumnAndTerminates(t *testing.T) {
	done := make(chan []string, 1)
	go func() {
		// 1pt column: every single glyph is wider than the column, which is the
		// pathological case.
		done <- Wrap(Helvetica, 9, strings.Repeat("Z", 40), 1)
	}()
	select {
	case lines := <-done:
		if len(lines) != 40 {
			t.Fatalf("hard-breaking 40 glyphs into a column narrower than one glyph produced %d lines, not 40; characters were dropped from a printed description", len(lines))
		}
		if strings.Join(lines, "") != strings.Repeat("Z", 40) {
			t.Fatalf("hard-breaking lost or reordered characters: %q", strings.Join(lines, ""))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Wrap did not terminate on a word wider than its column; this hangs the print request and the server goroutine with it")
	}
}

// TestWrapOfAnEmptyDescriptionIsOneEmptyLine pins the documented contract that
// callers may always index [0], so an item with no description still occupies
// exactly one row and the table below it does not shift up.
func TestWrapOfAnEmptyDescriptionIsOneEmptyLine(t *testing.T) {
	for _, in := range []string{"", "   ", "\t\t", "\n"} {
		got := Wrap(Helvetica, 9, in, 100)
		if len(got) == 0 {
			t.Fatalf("Wrap(%q) returned no lines; a renderer indexing [0] panics and the print request 500s", in)
		}
		for _, l := range got {
			if strings.TrimSpace(l) != "" {
				t.Fatalf("Wrap(%q) invented the visible text %q", in, l)
			}
		}
	}
}

// TestWrapCollapsesRunsOfSpacesFromPastedText: a description pasted out of a
// spreadsheet carries double spaces, and printing them leaves gaps that look
// like missing data.
func TestWrapCollapsesRunsOfSpacesFromPastedText(t *testing.T) {
	got := Wrap(Helvetica, 9, "MS   Plate     10mm", 500)
	if len(got) != 1 || got[0] != "MS Plate 10mm" {
		t.Fatalf("runs of spaces were not collapsed: got %q", got)
	}
}

// TestWrapTreatsANewlineInAnAddressAsAHardBreak: party addresses are stored with
// embedded newlines, and a wrap that ignored them would print the whole address
// as one run-on line.
func TestWrapTreatsANewlineInAnAddressAsAHardBreak(t *testing.T) {
	got := Wrap(Helvetica, 9, "Unit 4, Bhiwandi\nThane 421302", 500)
	if len(got) != 2 || got[0] != "Unit 4, Bhiwandi" || got[1] != "Thane 421302" {
		t.Fatalf("an address with an embedded newline wrapped to %q, not to two lines", got)
	}
}

// TestAZeroWidthColumnDoesNotHangTheRenderer: a template override with a zero
// column weight must not be a denial of service.
func TestAZeroWidthColumnDoesNotHangTheRenderer(t *testing.T) {
	done := make(chan []string, 1)
	go func() { done <- Wrap(Helvetica, 9, "Opening Balance", 0) }()
	select {
	case got := <-done:
		if len(got) != 1 || got[0] != "Opening Balance" {
			t.Fatalf("a zero-width column returned %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Wrap hung on a zero-width column; a bad template override would take the print endpoint down")
	}
}

// TestTheRupeeSignIsPrintedAsRsBecauseWinAnsiHasNoRupeeGlyph pins the ONE
// documented substitution that matters on an Indian invoice. WinAnsi has no
// rupee code point and no base-14 font has the glyph, so a raw U+20B9 would
// print as a blank or a box beside the total.
func TestTheRupeeSignIsPrintedAsRsBecauseWinAnsiHasNoRupeeGlyph(t *testing.T) {
	if err := Representable("₹1,23,456.78"); err != nil {
		t.Fatalf("a rupee amount was rejected instead of substituted: %v", err)
	}
	got := Measure(Helvetica, 10, "₹1,234.50")
	want := Measure(Helvetica, 10, "Rs.1,234.50")
	if got != want {
		t.Fatalf("a rupee amount measures %.4fpt but prints as %q which measures %.4fpt; the right-aligned total will sit in the wrong place", got, "Rs.1,234.50", want)
	}

	d := NewDoc(A4Portrait)
	d.NewPage().TextRight(500, 700, Helvetica, 10, "₹1,234.50")
	pdf, err := d.Bytes()
	if err != nil {
		t.Fatalf("printing a rupee amount failed: %v", err)
	}
	lines, err := ExtractText(pdf)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(lines) != 1 || lines[0] != "Rs.1,234.50" {
		t.Fatalf("the rupee total printed as %q; it must print as \"Rs.1,234.50\"", lines)
	}
}

// TestANonLatinPartyNameIsRefusedRatherThanPrintedAsGarbage is the honest half of
// the encoding decision. A Devanagari name has no WinAnsi code point at all, and
// emitting the low byte of each rune would print a customer's name as random
// accented Latin letters on their own invoice. The document must refuse.
func TestANonLatinPartyNameIsRefusedRatherThanPrintedAsGarbage(t *testing.T) {
	const name = "मुंबई ट्रेडर्स"

	if err := Representable(name); err == nil {
		t.Fatal("Representable accepted a Devanagari party name, so a caller holding a transliteration would never be told to use it")
	}

	d := NewDoc(A4Portrait)
	d.NewPage().Text(50, 700, Helvetica, 11, name)
	pdf, err := d.Bytes()
	if pdf != nil {
		t.Fatalf("a document containing an unprintable party name still produced %d bytes; that file would reach a customer with their name mangled", len(pdf))
	}
	var encErr *EncodingError
	if !errors.As(err, &encErr) {
		t.Fatalf("Bytes returned %v, not an *EncodingError naming the offending rune", err)
	}
	if encErr.Rune != 'म' {
		t.Errorf("the error blames U+%04X, but the first unprintable rune in the name is U+%04X", encErr.Rune, 'म')
	}
	if !strings.Contains(encErr.Error(), "WinAnsi") {
		t.Errorf("the error message %q does not say which encoding refused it, so nobody can act on it", encErr.Error())
	}
}

// TestALatin1AccentedNameStillPrints: WinAnsi covers Latin-1, so a French or
// German supplier name must NOT be refused.
func TestALatin1AccentedNameStillPrints(t *testing.T) {
	const name = "Société Générale — Zürich"
	if err := Representable(name); err != nil {
		t.Fatalf("a Latin-1 name with an em dash was refused: %v", err)
	}
	d := NewDoc(A4Portrait)
	d.NewPage().Text(50, 700, Helvetica, 11, name)
	pdf, err := d.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	lines, err := ExtractText(pdf)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(lines) != 1 || lines[0] != name {
		t.Fatalf("a Latin-1 supplier name round-tripped as %q, not %q", lines, name)
	}
}
