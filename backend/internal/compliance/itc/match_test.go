package itc

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ---------------------------------------------------------------------------
// Test fixtures. Deliberately minimal and explicit: every figure a test asserts
// on is written in the test, not derived by a helper, so a reader can check the
// arithmetic without leaving the file.
// ---------------------------------------------------------------------------

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// day parses an ISO date AS AN IST DAY, which is how every date in this package
// is compared.
func day(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.ParseInLocation("2006-01-02", s, IST)
	if err != nil {
		t.Fatalf("parse day %q: %v", s, err)
	}
	return v
}

const testGSTIN = "29ABCDE1234F1Z5"

// booksInvoice builds an ordinary intra-state purchase at 18% (9 + 9).
func booksInvoice(t *testing.T, docNo, docDate, taxable string) BooksRow {
	t.Helper()
	tx := d(taxable)
	half := tx.Mul(d("9")).Div(d("100")).Round(4)
	return BooksRow{
		VoucherID:       uuid.MustParse("00000000-0000-0000-0000-" + pad12(docNo)),
		VoucherNo:       "PU-" + docNo,
		VoucherDate:     day(t, docDate),
		ParentClass:     "PURCHASE",
		DocType:         DocInvoice,
		PartyGSTIN:      testGSTIN,
		SupplierDocNo:   docNo,
		SupplierDocDate: day(t, docDate),
		Taxable:         tx,
		Tax:             Tax{CGST: half, SGST: half, IGST: decimal.Zero},
		ITCEligibility:  EligibilityEligible,
		HasGSTParams:    true,
	}
}

// booksCreditNote builds a purchase-side CREDIT adjustment: a note raised on the
// supplier. Its money is NEGATIVE, exactly as store.go's sign convention leaves
// it.
func booksCreditNote(t *testing.T, docNo, docDate, taxable string) BooksRow {
	t.Helper()
	b := booksInvoice(t, docNo, docDate, taxable)
	b.ParentClass = "DEBIT_NOTE"
	b.DocType = DocCreditNote
	b.VoucherNo = "DN-" + docNo
	b.Taxable = b.Taxable.Neg()
	b.Tax = b.Tax.Neg()
	return b
}

// stmtInvoice builds the statement counterpart of booksInvoice. Statement money
// is always the POSITIVE magnitude the file reports.
func stmtInvoice(t *testing.T, id, docNo, docDate, taxable string) StatementRow {
	t.Helper()
	tx := d(taxable)
	half := tx.Mul(d("9")).Div(d("100")).Round(4)
	return StatementRow{
		ID:                uuid.MustParse("11111111-0000-0000-0000-" + pad12(id)),
		Section:           SectionB2B,
		SupplierGSTIN:     testGSTIN,
		SupplierTradeName: "Test Supplier Pvt Ltd",
		SupplierFiledDate: day(t, docDate),
		DocNo:             docNo,
		DocDate:           day(t, docDate),
		DocType:           DocInvoice,
		Taxable:           tx,
		Tax:               Tax{CGST: half, SGST: half, IGST: decimal.Zero},
		Cess:              decimal.Zero,
		DocValue:          tx.Add(half).Add(half),
		ITCAvailable:      true,
	}
}

func stmtCreditNote(t *testing.T, id, docNo, docDate, taxable string) StatementRow {
	t.Helper()
	s := stmtInvoice(t, id, docNo, docDate, taxable)
	s.Section = SectionCDNR
	s.DocType = DocCreditNote
	return s
}

// pad12 turns a short label into the last twelve hex digits of a UUID so every
// fixture has a stable, readable id.
func pad12(s string) string {
	const hexes = "0123456789abcdef"
	out := make([]byte, 12)
	for i := range out {
		out[i] = '0'
	}
	j := 11
	for i := len(s) - 1; i >= 0 && j >= 0; i-- {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			out[j] = c
		case c >= 'a' && c <= 'f':
			out[j] = c
		case c >= 'A' && c <= 'F':
			out[j] = c - 'A' + 'a'
		default:
			out[j] = hexes[int(c)%16]
		}
		j--
	}
	return string(out)
}

// testPolicy is a policy whose clock is FAR from every boundary, so a test that
// is not about the clock cannot accidentally be about the clock. asOn is inside
// the s.16(4) window for every 2026 document in these fixtures.
func testPolicy(t *testing.T) Policy {
	t.Helper()
	asOn := mustTime(t, "2026-03-01T10:00:00+05:30")
	return Policy{
		AsOn:       asOn,
		Resolution: mustTime(t, "2026-02-14T00:00:00+05:30"),
	}
}

// findLine returns the single line whose books voucher number matches, failing
// if there is not exactly one.
func findBooksLine(t *testing.T, lines []Line, voucherNo string) Line {
	t.Helper()
	var found []Line
	for _, l := range lines {
		if l.Books != nil && l.Books.VoucherNo == voucherNo {
			found = append(found, l)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly 1 line for books voucher %s, got %d: %s", voucherNo, len(found), dump(lines))
	}
	return found[0]
}

func findStmtLine(t *testing.T, lines []Line, docNo string, dt DocType) Line {
	t.Helper()
	var found []Line
	for _, l := range lines {
		if l.Statement != nil && l.Statement.DocNo == docNo && l.Statement.DocType == dt {
			found = append(found, l)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly 1 line for statement doc %s/%s, got %d: %s", docNo, dt, len(found), dump(lines))
	}
	return found[0]
}

func dump(lines []Line) string {
	s := "\n"
	for _, l := range lines {
		s += "  " + string(l.Status)
		if l.Books != nil {
			s += " books=" + l.Books.VoucherNo + "/" + l.Books.SupplierDocNo + "/" + string(l.Books.DocType) +
				" tax=" + l.Books.Tax.CGST.String() + "+" + l.Books.Tax.SGST.String()
		}
		if l.Statement != nil {
			s += " stmt=" + l.Statement.DocNo + "/" + string(l.Statement.DocType)
		}
		s += " avail=" + l.Effect.Avail.CGST.String() +
			" revOther=" + l.Effect.RevOther.CGST.String() +
			" revRule=" + l.Effect.RevRule.CGST.String() +
			" inelig=" + l.Effect.Inelig.CGST.String() + "\n"
	}
	return s
}

// ---------------------------------------------------------------------------
// Pass 1 and 2
// ---------------------------------------------------------------------------

func TestMatch_ExactAndRounding(t *testing.T) {
	p := testPolicy(t)

	t.Run("exact", func(t *testing.T) {
		lines := Match(
			[]BooksRow{booksInvoice(t, "INV-1", "2026-01-05", "10000.0000")},
			[]StatementRow{stmtInvoice(t, "1", "INV-1", "2026-01-05", "10000.0000")},
			p)
		if len(lines) != 1 {
			t.Fatalf("want 1 paired line, got %d: %s", len(lines), dump(lines))
		}
		if lines[0].Status != Matched {
			t.Fatalf("status %s, want %s: %s", lines[0].Status, Matched, dump(lines))
		}
		if !lines[0].Effect.RevOther.IsZero() {
			t.Fatalf("a MATCHED line reversed %v in 4(B)(2)", lines[0].Effect.RevOther)
		}
		if !lines[0].Effect.Avail.CGST.Equal(d("900")) {
			t.Fatalf("4(A) CGST %s, want 900", lines[0].Effect.Avail.CGST)
		}
	})

	t.Run("rounding_within_tolerance_takes_the_books_figure", func(t *testing.T) {
		// Taxable EXACT to the paisa; the supplier's CGST is fifty paise lower
		// because they rounded at invoice level and we round per line.
		s := stmtInvoice(t, "1", "INV-1", "2026-01-05", "10000.0000")
		s.Tax.CGST = s.Tax.CGST.Sub(d("0.50"))

		lines := Match([]BooksRow{booksInvoice(t, "INV-1", "2026-01-05", "10000.0000")}, []StatementRow{s}, p)
		if len(lines) != 1 || lines[0].Status != MatchedRounding {
			t.Fatalf("want one MATCHED_ROUNDING line, got: %s", dump(lines))
		}
		// The DELTA is carried — it is a distinct reported status, not a silent
		// MATCHED — and the ITC is taken at the BOOKS figure.
		if !lines[0].Delta.Tax.CGST.Equal(d("0.5")) {
			t.Fatalf("delta CGST %s, want 0.50", lines[0].Delta.Tax.CGST)
		}
		if !lines[0].Effect.Avail.CGST.Equal(d("900")) {
			t.Fatalf("4(A) CGST %s, want the BOOKS figure 900", lines[0].Effect.Avail.CGST)
		}
		if !lines[0].Effect.RevOther.IsZero() {
			t.Fatalf("a sub-rupee rounding difference produced a 4(B)(2) reversal of %v — "+
				"a reversal line of fifty paise that nobody can explain",
				lines[0].Effect.RevOther)
		}

		// One paisa OVER the tolerance is a MISMATCH, not rounding.
		s2 := stmtInvoice(t, "1", "INV-1", "2026-01-05", "10000.0000")
		s2.Tax.CGST = s2.Tax.CGST.Sub(d("1.01"))
		lines = Match([]BooksRow{booksInvoice(t, "INV-1", "2026-01-05", "10000.0000")}, []StatementRow{s2}, p)
		if lines[0].Status != Mismatched {
			t.Fatalf("a 1.01 difference gave %s, want %s — the tolerance is not absolute",
				lines[0].Status, Mismatched)
		}
	})

	t.Run("value_mismatch_names_the_fields_and_reverses_only_the_excess", func(t *testing.T) {
		// Books claim 10000 taxable / 900+900 tax; the supplier declared 8000 /
		// 720+720. The excess claimed is 180 per head, and ONLY that is reversed.
		lines := Match(
			[]BooksRow{booksInvoice(t, "INV-1", "2026-01-05", "10000.0000")},
			[]StatementRow{stmtInvoice(t, "1", "INV-1", "2026-01-05", "8000.0000")},
			p)
		if len(lines) != 1 || lines[0].Status != Mismatched {
			t.Fatalf("want one MISMATCHED line, got: %s", dump(lines))
		}
		want := map[MismatchField]bool{FieldTaxableValue: true, FieldCGST: true, FieldSGST: true}
		if len(lines[0].Fields) != len(want) {
			t.Fatalf("fields %v, want %v", lines[0].Fields, want)
		}
		for _, f := range lines[0].Fields {
			if !want[f] {
				t.Fatalf("unexpected field %s in %v", f, lines[0].Fields)
			}
		}
		if !lines[0].Effect.Avail.CGST.Equal(d("900")) {
			t.Fatalf("4(A) CGST %s, want the full posted 900 (model (b))", lines[0].Effect.Avail.CGST)
		}
		if !lines[0].Effect.RevOther.CGST.Equal(d("180")) {
			t.Fatalf("4(B)(2) CGST %s, want the excess 180", lines[0].Effect.RevOther.CGST)
		}
	})

	t.Run("books_below_statement_reverses_nothing", func(t *testing.T) {
		// We claimed LESS than the supplier declared. Under-claiming is not an
		// overclaim and must not be "corrected" by a reversal.
		lines := Match(
			[]BooksRow{booksInvoice(t, "INV-1", "2026-01-05", "8000.0000")},
			[]StatementRow{stmtInvoice(t, "1", "INV-1", "2026-01-05", "10000.0000")},
			p)
		if lines[0].Status != Mismatched {
			t.Fatalf("want MISMATCHED, got %s", lines[0].Status)
		}
		if !lines[0].Effect.RevOther.IsZero() {
			t.Fatalf("claiming LESS than the supplier declared produced a reversal of %v",
				lines[0].Effect.RevOther)
		}
	})

	t.Run("date_difference_is_a_mismatch_not_a_pairing", func(t *testing.T) {
		// THE FINANCIAL-YEAR BOUNDARY. A "+/- 7 days" fuzz would pair these two,
		// and their s.16(4) expiries are a YEAR apart.
		books := booksInvoice(t, "INV-1", "2026-03-31", "10000.0000")
		stmt := stmtInvoice(t, "1", "INV-1", "2026-04-01", "10000.0000")
		lines := Match([]BooksRow{books}, []StatementRow{stmt}, p)
		if len(lines) != 2 {
			t.Fatalf("a one-day date difference produced %d line(s); it must NOT pair, "+
				"because 31-Mar and 01-Apr are different financial years with s.16(4) "+
				"expiries a year apart: %s", len(lines), dump(lines))
		}
		be := Sec16_4Expiry(books.SupplierDocDate, nil)
		se := Sec16_4Expiry(stmt.DocDate, nil)
		if be.Year() == se.Year() {
			t.Fatalf("test premise broken: both dates expire in %d", be.Year())
		}
	})
}

// ---------------------------------------------------------------------------
// THE THREE NON-NEGOTIABLE PAIRING RULES
// ---------------------------------------------------------------------------

// TestMatch_CreditNoteIsNotPairedToItsInvoice is the gate on document type being
// part of the match key.
//
// Real suppliers number a credit note with the number of the invoice it adjusts.
// In GSTR-2B they are two documents with two IMS actions; in our own SQL the
// note is folded into PURCHASE with sgn = -1. A matcher that leaves document
// type out of the key pairs them, sees a difference of zero, reports MATCHED and
// silently CANCELS the reversal that credit note is supposed to cause.
func TestMatch_CreditNoteIsNotPairedToItsInvoice(t *testing.T) {
	p := testPolicy(t)

	books := []BooksRow{booksInvoice(t, "INV-7", "2026-01-05", "10000.0000")}
	stmt := []StatementRow{
		stmtInvoice(t, "1", "INV-7", "2026-01-05", "10000.0000"),
		// The supplier's CREDIT NOTE, numbered IDENTICALLY to the invoice, for
		// the SAME value and the SAME date. Everything except the type matches.
		stmtCreditNote(t, "2", "INV-7", "2026-01-05", "10000.0000"),
	}

	lines := Match(books, stmt, p)

	if len(lines) != 2 {
		t.Fatalf("got %d line(s), want 2 — the invoice and the supplier's CREDIT NOTE "+
			"landed in the SAME match bucket, which means document type is not in the "+
			"key. Both outcomes are wrong and one of them is silent: paired, the "+
			"difference is zero and the reversal the credit note exists to cause is "+
			"CANCELLED by a clean verdict; bucketed together, three real documents go "+
			"AMBIGUOUS for a reason that is not ambiguity: %s",
			len(lines), dump(lines))
	}

	// The invoice must be paired to the INVOICE, not to the note.
	inv := findStmtLine(t, lines, "INV-7", DocInvoice)
	if inv.Status != Matched {
		t.Fatalf("the invoice line is %s, want %s: %s", inv.Status, Matched, dump(lines))
	}
	if inv.Books == nil || inv.Books.VoucherNo != "PU-INV-7" {
		t.Fatalf("the invoice did not pair with the books invoice: %s", dump(lines))
	}

	// The credit note must stand alone, unpaired, as MISSING_IN_BOOKS — we never
	// recorded the supplier's note.
	cn := findStmtLine(t, lines, "INV-7", DocCreditNote)
	if cn.Status != MissingInBooks {
		t.Fatalf("the credit note is %s, want %s: %s", cn.Status, MissingInBooks, dump(lines))
	}
	if cn.Books != nil {
		t.Fatalf("the credit note was PAIRED to books voucher %s — a CDNR must never "+
			"pair with the invoice it adjusts", cn.Books.VoucherNo)
	}
}

// TestMatch_NearMatchIsNeverPaired is the gate on the loose key being CANDIDACY
// only.
//
// "INV-001" and "INV1" normalise to the same LOOSE key and to different STRICT
// keys. They are plausibly the same invoice and plausibly two invoices from a
// supplier who pads. Nothing in the data decides which, so nothing is paired:
// both sides stay unmatched, each carrying a pointer to the other, and NEITHER
// contributes to the ITC arithmetic as though it were confirmed.
func TestMatch_NearMatchIsNeverPaired(t *testing.T) {
	p := testPolicy(t)

	books := []BooksRow{booksInvoice(t, "INV-001", "2026-01-05", "10000.0000")}
	stmt := []StatementRow{stmtInvoice(t, "1", "INV1", "2026-01-05", "10000.0000")}

	// Premise: the two keys really do differ strictly and agree loosely.
	bs, bl := NormalizeDocNo("INV-001")
	ss, sl := NormalizeDocNo("INV1")
	if bs == ss {
		t.Fatalf("test premise broken: strict keys agree (%q)", bs)
	}
	if bl != sl {
		t.Fatalf("test premise broken: loose keys differ (%q vs %q)", bl, sl)
	}

	lines := Match(books, stmt, p)
	if len(lines) != 2 {
		t.Fatalf("got %d line(s), want 2 — a LOOSE-key candidate was promoted to a "+
			"pairing. \"INV-001\" and \"INV1\" may be two different invoices from a "+
			"supplier that pads, and pairing them claims ITC against an invoice "+
			"nobody confirmed: %s", len(lines), dump(lines))
	}

	bl0 := findBooksLine(t, lines, "PU-INV-001")
	if bl0.Status != NearMatch {
		t.Fatalf("books line is %s, want %s: %s", bl0.Status, NearMatch, dump(lines))
	}
	if bl0.Statement != nil {
		t.Fatalf("the books line carries a PAIRED statement row (%s) — a near match is "+
			"never a pairing", bl0.Statement.DocNo)
	}
	if len(bl0.Candidates) != 1 || bl0.Candidates[0].DocNo != "INV1" {
		t.Fatalf("books line candidates %v, want exactly the statement row INV1", bl0.Candidates)
	}

	sl0 := findStmtLine(t, lines, "INV1", DocInvoice)
	if sl0.Status != NearMatch {
		t.Fatalf("statement line is %s, want %s: %s", sl0.Status, NearMatch, dump(lines))
	}
	if sl0.Books != nil {
		t.Fatalf("the statement line carries a PAIRED books row (%s)", sl0.Books.VoucherNo)
	}
	if len(sl0.Candidates) != 1 || sl0.Candidates[0].DocNo != "INV-001" {
		t.Fatalf("statement line candidates %v, want exactly the books row INV-001", sl0.Candidates)
	}

	// AND THE ARITHMETIC. The statement side contributes nothing at all; the
	// books side is treated as UNMATCHED — full availment, full reversal —
	// exactly as it would be if the candidate did not exist. A near match must
	// not quietly reduce the haircut.
	if !sl0.Effect.Avail.IsZero() || !sl0.Effect.RevOther.IsZero() || !sl0.Effect.Inelig.IsZero() {
		t.Fatalf("the statement-side NEAR_MATCH moved a figure: avail=%v revOther=%v inelig=%v",
			sl0.Effect.Avail, sl0.Effect.RevOther, sl0.Effect.Inelig)
	}
	if !bl0.Effect.Avail.CGST.Equal(d("900")) {
		t.Fatalf("books-side 4(A) CGST %s, want the full posted 900", bl0.Effect.Avail.CGST)
	}
	if !bl0.Effect.RevOther.CGST.Equal(d("900")) {
		t.Fatalf("books-side 4(B)(2) CGST %s, want the full 900 — a NEAR_MATCH is "+
			"treated as unmatched, conservatively, until a human confirms it",
			bl0.Effect.RevOther.CGST)
	}
	// Net effect on 4(C) is zero, which is the point: unconfirmed credit is not
	// claimed.
	if !bl0.Effect.Avail.Sub(bl0.Effect.RevOther).IsZero() {
		t.Fatalf("a NEAR_MATCH produced a non-zero net credit")
	}
}

// TestMatch_AmbiguousPairsNothing is the gate on ambiguity being an error-grade
// finding rather than a tie-break.
//
// Two statement rows share a normalised key. Any deterministic pick — lowest id,
// earliest date, largest value — claims ITC against ONE of two invoices with no
// evidence which, and the filer is never told a choice was made.
func TestMatch_AmbiguousPairsNothing(t *testing.T) {
	p := testPolicy(t)

	books := []BooksRow{booksInvoice(t, "INV-9", "2026-01-05", "10000.0000")}
	stmt := []StatementRow{
		stmtInvoice(t, "a", "INV-9", "2026-01-05", "10000.0000"),
		stmtInvoice(t, "b", "INV/9", "2026-01-05", "10000.0000"), // same STRICT key
	}
	if a, _ := NormalizeDocNo("INV-9"); a != mustStrict(t, "INV/9") {
		t.Fatalf("test premise broken: the two statement rows do not share a strict key")
	}

	lines := Match(books, stmt, p)
	if len(lines) != 3 {
		t.Fatalf("got %d line(s), want 3 (one books + two statement, none paired): %s",
			len(lines), dump(lines))
	}
	for _, l := range lines {
		if l.Status != Ambiguous {
			t.Fatalf("line %s is %s, want %s — one of two indistinguishable candidates "+
				"was PICKED, which claims ITC against an invoice nobody chose: %s",
				describe(l), l.Status, Ambiguous, dump(lines))
		}
		if l.Books != nil && l.Statement != nil {
			t.Fatalf("an AMBIGUOUS line is PAIRED: %s", describe(l))
		}
	}

	// Every participant names the alternatives, so a human can resolve it.
	bl0 := findBooksLine(t, lines, "PU-INV-9")
	if len(bl0.Candidates) != 2 {
		t.Fatalf("the books line names %d candidate(s), want 2", len(bl0.Candidates))
	}

	// The books row is still availed in full and reversed in full — the
	// conservative treatment of unconfirmed credit.
	if !bl0.Effect.Avail.CGST.Equal(d("900")) || !bl0.Effect.RevOther.CGST.Equal(d("900")) {
		t.Fatalf("ambiguous books line: avail=%s revOther=%s, want 900 / 900",
			bl0.Effect.Avail.CGST, bl0.Effect.RevOther.CGST)
	}

	// The two statement rows contribute nothing.
	for _, l := range lines {
		if l.Statement != nil && !l.Effect.Avail.IsZero() {
			t.Fatalf("an ambiguous statement row contributed %v to 4(A)", l.Effect.Avail)
		}
	}
}

func mustStrict(t *testing.T, s string) string {
	t.Helper()
	strict, _ := NormalizeDocNo(s)
	return strict
}

func describe(l Line) string {
	switch {
	case l.Books != nil:
		return "books:" + l.Books.VoucherNo
	case l.Statement != nil:
		return "stmt:" + l.Statement.DocNo + "/" + string(l.Statement.DocType)
	default:
		return "<empty>"
	}
}

// TestMatch_PriorFYInvoiceInACurrentStatementIsNotADateMismatch pins the 2B
// window rule: the window is on the SUPPLIER'S FILING DATE, not the document
// date (2B Advisory Q.4). An invoice dated 15-05-2025 that the supplier first
// filed in the January-2026 GSTR-1 lands in the JANUARY 2026 2B, and must match
// our May-2025 purchase normally.
func TestMatch_PriorFYInvoiceInACurrentStatementIsNotADateMismatch(t *testing.T) {
	p := testPolicy(t)
	// asOn is inside the s.16(4) window for a FY2025-26 document (expires
	// 30-Nov-2026), so nothing here is time-barred.

	books := []BooksRow{booksInvoice(t, "INV-OLD", "2025-05-15", "10000.0000")}
	s := stmtInvoice(t, "1", "INV-OLD", "2025-05-15", "10000.0000")
	s.SupplierReturnPeriod = "012026" // filed eight months late
	s.SupplierFiledDate = day(t, "2026-02-11")
	stmt := []StatementRow{s}

	lines := Match(books, stmt, p)
	if len(lines) != 1 || lines[0].Status != Matched {
		t.Fatalf("a prior-year invoice appearing in a current 2B was not matched "+
			"normally: %s — the matcher is requiring the document date to fall inside "+
			"the statement's return period, which the 2B advisory expressly says it "+
			"does not", dump(lines))
	}
	if !lines[0].Effect.RevOther.IsZero() {
		t.Fatalf("a late-filed but present invoice was reversed: %v", lines[0].Effect.RevOther)
	}
}

// TestMatch_IsDeterministic runs the same inputs in different slice orders and
// requires byte-identical verdicts. A reconciliation that changes when you
// re-run it cannot be signed off.
func TestMatch_IsDeterministic(t *testing.T) {
	p := testPolicy(t)
	books := []BooksRow{
		booksInvoice(t, "INV-3", "2026-01-09", "3000.0000"),
		booksInvoice(t, "INV-1", "2026-01-05", "1000.0000"),
		booksInvoice(t, "INV-2", "2026-01-07", "2000.0000"),
	}
	stmt := []StatementRow{
		stmtInvoice(t, "2", "INV-2", "2026-01-07", "2000.0000"),
		stmtInvoice(t, "1", "INV-1", "2026-01-05", "1000.0000"),
	}
	want := Match(books, stmt, p)

	got := Match(reversed(books), reversed(stmt), p)

	if len(got) != len(want) {
		t.Fatalf("reordering the inputs changed the line count: %d vs %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Status != want[i].Status || describe(got[i]) != describe(want[i]) {
			t.Fatalf("line %d differs after reordering the inputs: %s/%s vs %s/%s",
				i, got[i].Status, describe(got[i]), want[i].Status, describe(want[i]))
		}
	}
}

// reversed returns a reversed copy. It is generic because Match takes two
// different slice types and the determinism test must reorder both.
func reversed[T any](in []T) []T {
	out := make([]T, len(in))
	for i := range in {
		out[i] = in[len(in)-1-i]
	}
	return out
}
