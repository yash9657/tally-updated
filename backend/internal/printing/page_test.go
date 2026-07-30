package printing

import (
	"math"
	"strings"
	"testing"
)

// TestADescriptionWithParenthesesAndABackslashDoesNotBreakTheDocument is the
// escaping test, and it is not hypothetical: "Widget (Large) \ 50%" is the kind
// of description a shop types in. In a PDF, an unescaped ")" CLOSES the string
// literal and everything after it on the page is parsed as operators, so the
// reader reports a damaged file or silently drops the rest of the invoice.
func TestADescriptionWithParenthesesAndABackslashDoesNotBreakTheDocument(t *testing.T) {
	const desc = `Widget (Large) \ 50% "quoted"`

	d := NewDoc(A4Portrait)
	p := d.NewPage()
	p.Text(50, 700, Helvetica, 10, desc)
	p.Text(50, 680, Helvetica, 10, "the line after it must survive")

	pdf, err := d.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	// The raw bytes must carry the escapes, not the bare delimiters.
	if !strings.Contains(string(pdf), `(Widget \(Large\) \\ 50% "quoted") Tj`) {
		t.Fatalf("the description was not escaped in the content stream; the emitted operator is not the expected literal")
	}

	lines, err := ExtractText(pdf)
	if err != nil {
		t.Fatalf("the document with a parenthesised description is unreadable: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 printed strings, got %d (%q): text after an unescaped delimiter is lost", len(lines), lines)
	}
	if lines[0] != desc {
		t.Errorf("the description printed as %q, not %q", lines[0], desc)
	}
	if lines[1] != "the line after it must survive" {
		t.Errorf("the line following a parenthesised description printed as %q; it was swallowed by the previous string", lines[1])
	}
}

// TestAControlByteFromAnOCRImportIsRefusedNotPrinted: a stray 0x01 in a scanned
// description has no glyph in any font. Printing it would put an unpredictable
// byte in the stream, so the document refuses and the caller has to clean the
// text — which is the only place that decision can be made honestly.
func TestAControlByteFromAnOCRImportIsRefusedNotPrinted(t *testing.T) {
	d := NewDoc(A4Portrait)
	d.NewPage().Text(10, 10, Helvetica, 8, "a\x01b\x1fc")
	if _, err := d.Bytes(); err == nil {
		t.Fatal("a description carrying control bytes printed without complaint")
	}
}

// TestTheStringLiteralWriterNeverEmitsARawControlByte tests the escaping layer
// directly, below the encode() refusal above: even handed a control byte, it must
// produce a three-digit octal escape rather than a raw byte that would make the
// file unparseable.
func TestTheStringLiteralWriterNeverEmitsARawControlByte(t *testing.T) {
	got := escapeLiteral([]byte("a\x01b\x1fc"))
	if string(got) != `(a\001b\037c)` {
		t.Fatalf("the literal writer produced %q; a raw control byte in a stream makes the document unportable", got)
	}
	back, _, err := readLiteral(got, 0)
	if err != nil {
		t.Fatalf("our own octal escapes do not read back: %v", err)
	}
	if back != "a\x01b\x1fc" {
		t.Fatalf("octal escapes round-tripped as %q", back)
	}
}

// TestRightAlignedAmountsEndAtTheColumnEdge checks the geometry that every money
// column depends on: TextRight must place the string so its right edge is at the
// column edge, computed from the same metrics Measure reports.
func TestRightAlignedAmountsEndAtTheColumnEdge(t *testing.T) {
	const xRight, y = 500.0, 700.0
	const amount = "1,23,456.78"
	w := Measure(HelveticaBold, 10, amount)

	d := NewDoc(A4Portrait)
	p := d.NewPage()
	p.TextRight(xRight, y, HelveticaBold, 10, amount)
	pdf, err := d.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	wantX := fmtNum(xRight - w)
	want := "1 0 0 1 " + wantX + " " + fmtNum(y) + " Tm"
	if !strings.Contains(string(pdf), want) {
		t.Fatalf("a right-aligned amount was not placed at x = %s (right edge %.2f minus its %.4fpt width); the column would not line up", wantX, xRight, w)
	}
}

// TestCentredTitlesAreCentredOnTheGivenAxis pins TextCenter the same way: the
// document heading straddling the page centre is the first thing a reader checks.
func TestCentredTitlesAreCentredOnTheGivenAxis(t *testing.T) {
	const xCenter = 297.64 // half of A4 portrait
	const title = "TAX INVOICE"
	w := Measure(HelveticaBold, 14, title)

	d := NewDoc(A4Portrait)
	d.NewPage().TextCenter(xCenter, 800, HelveticaBold, 14, title)
	pdf, err := d.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	want := "1 0 0 1 " + fmtNum(xCenter-w/2) + " " + fmtNum(800)
	if !strings.Contains(string(pdf), want) {
		t.Fatalf("the centred heading was not placed at %q; the title sits off-centre on every invoice", want)
	}
}

// TestTheOriginIsTheBottomLeftCornerOfThePage guards the coordinate convention.
// A renderer that assumed a top-left origin would draw the letterhead below the
// bottom edge and the document would look blank; here we assert that the
// MediaBox is [0 0 W H] and that a y near the page height is emitted unchanged
// (i.e. we never flip it behind the caller's back).
func TestTheOriginIsTheBottomLeftCornerOfThePage(t *testing.T) {
	d := NewDoc(A4Portrait)
	p := d.NewPage()
	p.Text(20, 820, Helvetica, 9, "letterhead") // near the TOP of the page
	pdf, err := d.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	s := string(pdf)
	if !strings.Contains(s, "/MediaBox [0 0 595.28 841.89]") {
		t.Fatal("the A4 portrait MediaBox is not [0 0 595.28 841.89]")
	}
	if !strings.Contains(s, "1 0 0 1 20 820 Tm") {
		t.Fatal("the y coordinate was transformed on its way to the content stream; with a bottom-left origin, y=820 on an 841.89pt page must be emitted as 820 or the letterhead lands off the page")
	}
	if p.Size() != A4Portrait {
		t.Fatalf("Page.Size reported %v, not the document's A4 portrait size", p.Size())
	}
}

// TestRulesAndBoxesAreStrokedWithTheWidthTheCallerAsked: the box around the
// items table and the rule under a total are the only structure a printed
// statement has.
func TestRulesAndBoxesAreStrokedWithTheWidthTheCallerAsked(t *testing.T) {
	d := NewDoc(A4Portrait)
	p := d.NewPage()
	p.Line(40, 100, 555, 100, 0.5)
	p.Rect(40, 200, 515, 300, 1)
	pdf, err := d.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	s := string(pdf)
	if !strings.Contains(s, "0.5 w 40 100 m 555 100 l S") {
		t.Fatal("the rule under the totals block was not stroked as a 0.5pt line")
	}
	if !strings.Contains(s, "1 w 40 200 515 300 re S") {
		t.Fatal("the box around the items table was not stroked as a rectangle with its lower-left corner at (40,200)")
	}
}

// TestAnEmptyStringDrawsNothing: rendering an absent optional field (no GSTIN, no
// due date) must not put an empty text object on the page.
func TestAnEmptyStringDrawsNothing(t *testing.T) {
	d := NewDoc(A4Portrait)
	d.NewPage().Text(10, 10, Helvetica, 9, "")
	pdf, err := d.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	if strings.Contains(string(pdf), "() Tj") {
		t.Fatal("an absent field emitted an empty text-showing operator")
	}
}

// TestCoordinatesAreFormattedIdenticallyForIdenticalGeometry guards the one place
// float formatting could make two identical documents differ byte for byte:
// negative zero, and trailing zeros.
func TestCoordinatesAreFormattedIdenticallyForIdenticalGeometry(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{100, "100"},
		{100.50, "100.5"},
		{841.89, "841.89"},
		{-12.5, "-12.5"},
		{0.001, "0"},
		{-0.001, "0"},
		{math.Copysign(0, -1), "0"},
	}
	for _, c := range cases {
		if got := fmtNum(c.in); got != c.want {
			t.Errorf("fmtNum(%v) = %q, want %q; an unstable coordinate format breaks every golden byte comparison", c.in, got, c.want)
		}
	}
	if fmtNum(math.Copysign(0, -1)) != fmtNum(0) {
		t.Error("negative zero formats differently from zero, so two arithmetically identical documents would not be byte-identical")
	}
}
