package printing

import (
	"bytes"
	"strings"
	"testing"
)

// TestEveryStringDrawnComesBackOutOfTheDocument is the contract the invoice and
// report golden tests rest on: whatever was drawn with Text, TextRight or
// TextCenter must be readable back out of the finished bytes, in order. If a
// string can go in and not come out, a golden test cannot tell whether the total
// was printed at all.
func TestEveryStringDrawnComesBackOutOfTheDocument(t *testing.T) {
	drawn := []string{
		"TAX INVOICE",
		"Mehta Trading Co.",
		"27AAAPZ1234C1ZS",
		`Widget (Large) \ 50% "quoted"`,
		"Rs.1,23,456.78",
		"Société Générale — Zürich",
		"CGST 9%",
		"E. & O.E.",
		"100% advance – bill ref: MT/2026-27/0041",
	}

	d := NewDoc(A4Portrait)
	p := d.NewPage()
	y := 800.0
	for i, s := range drawn {
		switch i % 3 {
		case 0:
			p.Text(40, y, Helvetica, 9, s)
		case 1:
			p.TextRight(550, y, HelveticaBold, 9, s)
		default:
			p.TextCenter(297.64, y, Courier, 9, s)
		}
		y -= 14
	}

	pdf, err := d.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	got, err := ExtractText(pdf)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(got) != len(drawn) {
		t.Fatalf("drew %d strings and read back %d (%q)", len(drawn), len(got), got)
	}
	for i := range drawn {
		// Latin-1 letters and CP1252 punctuation (the en dash, the em dash) have
		// WinAnsi codes, so they must round-trip EXACTLY, not approximately.
		want := drawn[i]
		if got[i] != want {
			t.Errorf("string %d was drawn as %q and read back as %q", i, want, got[i])
		}
	}
}

// TestADescriptionContainingTheWordEndstreamDoesNotTruncateThePage. A writer that
// found the end of a content stream by searching for "endstream" would cut the
// page short the first time a description mentioned it. We use the object's own
// /Length instead, and this is the test that says so.
func TestADescriptionContainingTheWordEndstreamDoesNotTruncateThePage(t *testing.T) {
	const nasty = "See endstream note (draft) and 5 0 obj as filed"
	d := NewDoc(A4Portrait)
	p := d.NewPage()
	p.Text(40, 700, Helvetica, 9, nasty)
	p.Text(40, 686, Helvetica, 9, "the line after it must survive")
	pdf, err := d.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	got, err := ExtractText(pdf)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(got) != 2 || got[0] != nasty || got[1] != "the line after it must survive" {
		t.Fatalf("a description containing PDF keywords truncated the page: got %q", got)
	}
}

// TestAnOffByOneXrefOffsetIsReportedAsACorruptDocument. This is the check that
// makes the xref assertions load-bearing: if a broken offset were tolerated here,
// the tests that "verify" the xref would be verifying nothing.
func TestAnOffByOneXrefOffsetIsReportedAsACorruptDocument(t *testing.T) {
	pdf, err := sampleDoc().Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	if _, err := ExtractText(pdf); err != nil {
		t.Fatalf("the untouched document does not read back: %v", err)
	}

	// Find the xref table and bump the offset of the first in-use entry by one.
	xi := bytes.LastIndex(pdf, []byte("\nxref\n"))
	if xi < 0 {
		t.Fatal("no xref table")
	}
	body := xi + len("\nxref\n")
	nl := bytes.IndexByte(pdf[body:], '\n')
	firstEntry := body + nl + 1 + 20 // skip the subsection header and the free head

	broken := append([]byte(nil), pdf...)
	off := 0
	for i := 0; i < 10; i++ {
		off = off*10 + int(broken[firstEntry+i]-'0')
	}
	off++
	for i := 9; i >= 0; i-- {
		broken[firstEntry+i] = byte('0' + off%10)
		off /= 10
	}

	if _, err := ExtractText(broken); err == nil {
		t.Fatal("a document whose xref offset is one byte off was accepted; readers that reject it would show the customer a damaged file and we would never know")
	} else if !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("the error for a broken xref offset was %q, which does not say the document is corrupt", err)
	}
}

// TestATruncatedDocumentIsReportedAsCorrupt: a short write or a proxy that cut the
// response must not read back as a valid, merely-shorter invoice.
func TestATruncatedDocumentIsReportedAsCorrupt(t *testing.T) {
	pdf, err := sampleDoc().Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	if _, err := ExtractText(pdf[:len(pdf)/2]); err == nil {
		t.Fatal("half a document read back without complaint")
	}
	if _, err := ExtractText([]byte("not a pdf at all")); err == nil {
		t.Fatal("a non-PDF read back without complaint")
	}
}

// TestABrokenEscapeIsReportedRatherThanGuessedAt. If the writer ever stopped
// escaping a backslash, the reader must say so instead of quietly inventing a
// character — a silently-wrong description is worse than a refused one.
func TestABrokenEscapeIsReportedRatherThanGuessedAt(t *testing.T) {
	if _, _, err := readLiteral([]byte(`(bad \q escape) Tj`), 0); err == nil {
		t.Fatal("an unknown escape sequence was silently accepted")
	}
	if _, _, err := readLiteral([]byte(`(never closed`), 0); err == nil {
		t.Fatal("an unterminated string literal was silently accepted")
	}
	if _, _, err := readLiteral([]byte(`(trailing backslash \`), 0); err == nil {
		t.Fatal("a literal ending in a backslash was silently accepted")
	}
}

// TestOnlyShownTextIsExtracted: a parenthesised operand that is not shown (there
// are none today, but /Producer-style strings live in the Info dictionary) must
// not appear in a report's extracted text and be mistaken for printed content.
func TestOnlyShownTextIsExtracted(t *testing.T) {
	pdf, err := sampleDoc().Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	got, err := ExtractText(pdf)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	for _, s := range got {
		if strings.Contains(s, "hand-rolled PDF") {
			t.Fatalf("the /Producer string was extracted as if it were printed on the page: %q", s)
		}
	}
}
