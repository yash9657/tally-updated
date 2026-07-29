package itc

import "testing"

// TestNormalizeDocNo pins the two keys. The rule is stated in exactly one place
// (NormalizeDocNo) and this is the only test of it, so every case here is a case
// somebody could get wrong.
func TestNormalizeDocNo(t *testing.T) {
	cases := []struct {
		in     string
		strict string
		loose  string
		note   string
	}{
		{"INV-001", "INV001", "INV1", "separators dropped; leading zeros survive the STRICT key"},
		{"inv001", "INV001", "INV1", "ASCII case folded"},
		{"INV 001 / 24-25", "INV0012425", "INV12425", "spaces and slashes dropped"},
		{"INV1", "INV1", "INV1", "no padding to strip"},
		{"INV0012A007", "INV0012A007", "INV12A7", "EVERY maximal digit run is stripped, not just the first"},
		{"INV000", "INV000", "INV0", "an all-zero run collapses to a single 0, never to the empty string"},
		{"INV0", "INV0", "INV0", "and INV000 and INV0 therefore agree loosely"},
		{"２０２５/１", "20251", "20251", "NFKC folds full-width digits before anything else"},
		{"", "", "", "empty in, empty out"},
		{"-/-", "", "", "punctuation only reduces to empty"},
	}
	for _, c := range cases {
		strict, loose := NormalizeDocNo(c.in)
		if strict != c.strict {
			t.Fatalf("NormalizeDocNo(%q) strict = %q, want %q (%s)", c.in, strict, c.strict, c.note)
		}
		if loose != c.loose {
			t.Fatalf("NormalizeDocNo(%q) loose = %q, want %q (%s)", c.in, loose, c.loose, c.note)
		}
	}
}

// TestNormalizeDocNo_StrictKeepsPaddingApart is the property the two-key design
// exists for, stated as an assertion rather than as a comment: the strict key
// must SEPARATE a padded number from an unpadded one (so they are never falsely
// paired) while the loose key BRINGS THEM TOGETHER (so they are still offered to
// a human as candidates).
func TestNormalizeDocNo_StrictKeepsPaddingApart(t *testing.T) {
	s1, l1 := NormalizeDocNo("INV001")
	s2, l2 := NormalizeDocNo("INV01")
	if s1 == s2 {
		t.Fatalf("INV001 and INV01 share the strict key %q — stripping leading zeros in "+
			"the PAIRING key is the single commonest source of false pairs, and a false "+
			"pair claims ITC against the wrong invoice", s1)
	}
	if l1 != l2 {
		t.Fatalf("INV001 and INV01 do not share a loose key (%q vs %q) — they would never "+
			"be offered as candidates, which is the single commonest source of false "+
			"non-matches, and a false non-match reverses good credit", l1, l2)
	}
}

// TestNormalizeDocNo_IsASCIIUppercaseNotLocaleCasing pins the deliberate use of
// an explicit ASCII fold rather than strings.ToUpper.
//
// Unicode casing is locale-dependent for a handful of scripts, and a key whose
// value depends on the environment's locale pairs differently on the server and
// on a developer's laptop. Everything rule 46(b) allows in an invoice number is
// ASCII, so the restricted fold loses nothing real.
func TestNormalizeDocNo_IsASCIIUppercaseNotLocaleCasing(t *testing.T) {
	// A dotless i (U+0131). Under Turkish locale rules strings.ToUpper maps it
	// to "I"; the ASCII fold drops it, because it is not [0-9A-Za-z].
	strict, _ := NormalizeDocNo("ıNV1")
	if strict != "NV1" {
		t.Fatalf("strict key for %q is %q, want %q — a non-ASCII letter must be DROPPED, "+
			"deterministically, rather than case-folded by a locale-dependent rule",
			"ıNV1", strict, "NV1")
	}
}

// TestMaxInvoiceNoLenIsReportedNotEnforced pins that an over-long number is a
// FINDING and not a match failure: refusing to reconcile a real invoice because
// the supplier numbered it badly helps nobody.
func TestMaxInvoiceNoLenIsReportedNotEnforced(t *testing.T) {
	long := "ABCDEFGHIJKLMNOPQRSTUVWXYZ/2025-26/0001"
	strict, _ := NormalizeDocNo(long)
	if len(strict) <= MaxInvoiceNoLen {
		t.Fatalf("test premise broken: %q normalises to %d characters", long, len(strict))
	}

	p := testPolicy(t)
	b := booksInvoice(t, long, "2026-01-05", "10000.0000")
	s := stmtInvoice(t, "1", long, "2026-01-05", "10000.0000")
	lines := Match([]BooksRow{b}, []StatementRow{s}, p)

	if len(lines) != 1 || lines[0].Status != Matched {
		t.Fatalf("an over-long document number blocked a genuine match: %s", dump(lines))
	}
	found := false
	for _, f := range lines[0].Findings {
		if f.Code == FindingDocNoInvalid {
			found = true
		}
	}
	if !found {
		t.Fatalf("a document number of %d characters exceeds rule 46(b)'s 16 and was not "+
			"reported: findings %v", len(strict), lines[0].Findings)
	}
}

func TestNormalizeGSTIN(t *testing.T) {
	cases := []struct{ in, want string }{
		{" 29abcde1234f1z5 ", "29ABCDE1234F1Z5"},
		{"29ABCDE1234F1Z5", "29ABCDE1234F1Z5"},
		{"29 ABCDE 1234 F1Z5", "29ABCDE1234F1Z5"},
		{"", ""},
		// A MALFORMED GSTIN IS RETURNED, NOT DROPPED. Silently discarding it
		// would turn "the supplier's GSTIN is wrong in our master" into "the
		// supplier did not file", which reverses credit against an innocent
		// supplier. Validity is reported separately.
		{"nonsense", "NONSENSE"},
	}
	for _, c := range cases {
		if got := NormalizeGSTIN(c.in); got != c.want {
			t.Fatalf("NormalizeGSTIN(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
