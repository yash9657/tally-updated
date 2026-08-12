package server

// handler_print_period_test.go — WHAT THE PAPER SAYS ABOUT ITSELF.
//
// Three register rows, one theme: a printed statement that does not state, in
// full and truthfully, WHICH BOOK AND WHICH PERIOD it is.
//
//	D34 — Ctrl+P carried no from/to/as_on, the server reads an absent bound as
//	      "no bound", and the paper came back headed "For all dates in the books"
//	      while the screen it was printed from showed one quarter. The CLIENT half
//	      is closed in screens/printTarget.ts; this file is the server half, and it
//	      is the half that decides what is actually on the page.
//	D61 — the Day Book's footer, the only sentence on the page explaining why
//	      there is no amount total, was CLIPPED mid-word — put in cell 0 of a
//	      2-wide "Date" column, wrapped to four lines and ellipsised.
//	D75 — every printed voucher's download name carried 36 characters of UUID
//	      after its number.
//
// ⛔ THE ORACLE IS THE EXTRACTED TEXT OF A REAL PDF, never a status code and
// never a byte length. Each case below asserts that the file begins %PDF and
// ends %%EOF, reads its words back, and requires the document to NAME ITSELF —
// its own title, its own period, its own number. A 200 with a right-looking
// length is exactly what all three defects produced.

import (
	"net/http"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// D34 — the printed statement is for the period that was asked for
// ---------------------------------------------------------------------------

// TestAPrintedStatementIsForAllDatesWhateverPeriodWasAskedFor is named for the
// breach. Ctrl+P sent the slug and nothing else, so every one of these came back
// with the whole book under a heading the operator read as their own period.
//
// The pairs below are the two SHAPES a report has, and they are not
// interchangeable: a POSITION (Trial Balance, Balance Sheet) takes only an upper
// bound, because bounding a position below drops the entity's earlier history
// and the statement still foots — a wrong answer that looks right. A FLOW (P&L,
// Day Book) takes both.
func TestAPrintedStatementIsForAllDatesWhateverPeriodWasAskedFor(t *testing.T) {
	s := payrollServer(t)
	f := newPrintFixture(t, s)

	for _, tc := range []struct {
		slug, query, wantSubtitle, unbounded string
	}{
		{
			slug: "trial-balance", query: "?as_on=2026-07-31",
			wantSubtitle: "As at 31-Jul-2026",
			unbounded:    "As at the latest date in the books",
		},
		{
			slug: "balance-sheet", query: "?as_on=2026-07-31",
			wantSubtitle: "As at 31-Jul-2026",
			unbounded:    "As at the latest date in the books",
		},
		{
			slug: "profit-and-loss", query: "?from=2026-04-01&to=2026-07-31",
			wantSubtitle: "From 01-Apr-2026 to 31-Jul-2026",
			unbounded:    "For all dates in the books",
		},
		{
			slug: "day-book", query: "?from=2026-07-01&to=2026-07-31",
			wantSubtitle: "From 01-Jul-2026 to 31-Jul-2026",
			unbounded:    "For all dates in the books",
		},
	} {
		t.Run(tc.slug, func(t *testing.T) {
			rec := printGet(t, s, f.base+"/print/reports/"+tc.slug+tc.query, f.ownerTok)
			if rec.Code != http.StatusOK {
				t.Fatalf("print %s = %d, want 200 (%s)", tc.slug, rec.Code, rec.Body.String())
			}
			assertIsAPDFFile(t, rec.Body.Bytes(), "the printed "+tc.slug)
			text := pdfText(t, rec, "the printed "+tc.slug)

			if !strings.Contains(text, tc.wantSubtitle) {
				t.Errorf("the printed %s does not say it is %q. A statement that does not state "+
					"its own period is signed for whatever period the reader assumes.\n%s",
					tc.slug, tc.wantSubtitle, text)
			}
			// ⛔ AND IT MUST NOT ALSO CARRY THE UNBOUNDED WORDING. This is the
			// assertion that fails when the query is dropped on the floor: the
			// figures would still be real, the page would still be a valid PDF, and
			// the only visible difference is this sentence.
			if strings.Contains(text, tc.unbounded) {
				t.Errorf("the printed %s was asked for %s and came back headed %q: every figure "+
					"on it is real, which is what makes it dangerous — it is the whole book "+
					"under the operator's own heading.\n%s",
					tc.slug, tc.query, tc.unbounded, text)
			}
		})
	}

	// THE CONTROL: a request that names NO period must still print, and must say
	// so in the words that are true for it. A path that refuses everything looks
	// identical to a working refusal, and a page that always claims a period is
	// as wrong as one that never does.
	t.Run("no period asked for is stated as such", func(t *testing.T) {
		rec := printGet(t, s, f.base+"/print/reports/day-book", f.ownerTok)
		if rec.Code != http.StatusOK {
			t.Fatalf("print an unbounded day-book = %d, want 200 (%s)", rec.Code, rec.Body.String())
		}
		text := pdfText(t, rec, "the unbounded day-book")
		if !strings.Contains(text, "For all dates in the books") {
			t.Errorf("a Day Book printed with no bounds does not say it covers all dates:\n%s", text)
		}
	})
}

// ---------------------------------------------------------------------------
// D61 — the Day Book's footer was cut off mid-sentence
// ---------------------------------------------------------------------------

// TestTheDayBooksFooterIsCutOffMidSentence is named for the breach.
//
// The footer is the only sentence on the page that explains why a column of
// amounts has no total. It was one long cell in the 2-wide "Date" column, so the
// renderer wrapped it to four lines and ellipsised it at "No column total is…" —
// present on the page, unreadable, and unnoticed because nobody re-reads a
// footer.
//
// ⚠️ IT IS ASSERTED ON THE EXTRACTED TEXT AND NOT ON THE SOURCE STRING, which is
// the whole point: the string in handler_print_reports.go was always complete.
// What was wrong was the geometry, and only the rendered page knows about that.
func TestTheDayBooksFooterIsCutOffMidSentence(t *testing.T) {
	s := payrollServer(t)
	f := newPrintFixture(t, s)

	rec := printGet(t, s, f.base+"/print/reports/day-book?from=2026-07-01&to=2026-07-31", f.ownerTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("print the day book = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	lines := pdfLines(t, rec, "the printed day-book")
	// The note is laid out one LINE per row, so it is reassembled the way a
	// reader's eye does before it is compared.
	joined := strings.Join(lines, " ")
	collapsed := strings.Join(strings.Fields(joined), " ")

	for _, want := range []string{
		// The sentence, in full. If any part of it is wrapped away, the run below
		// no longer appears.
		"No column total is printed — these rows are vouchers of every class, so a sale and " +
			"the receipt that settles it both appear here, and adding them would count the same " +
			"money twice.",
		// And the clause the OLD footer got factually wrong: it claimed an altered
		// voucher appears twice, which is false for this build (DayBook joins on
		// current_version_id and filters is_deleted). A footer that explains an
		// absence with a reason that does not apply teaches the operator something
		// wrong about their own Day Book.
		"One line per voucher, at its CURRENT version",
	} {
		if !strings.Contains(collapsed, want) {
			t.Errorf("the printed Day Book's footer does not carry %q in full. What is actually "+
				"on the page:\n%s", want, strings.Join(lines, "\n"))
		}
	}
	// The ellipsis the renderer inserts when it gives up on a cell. Its presence
	// anywhere in this document means some sentence was cut off.
	if strings.Contains(collapsed, "…") {
		t.Errorf("the printed Day Book contains an ellipsis, so a cell was truncated rather than "+
			"laid out:\n%s", strings.Join(lines, "\n"))
	}
	// The count row survives beside the note, and it counts VOUCHERS — the old
	// wording said "voucher version(s)", which is a different and wrong claim
	// about what the report lists.
	if !strings.Contains(collapsed, "voucher(s) in this period") {
		t.Errorf("the printed Day Book does not state how many vouchers it lists:\n%s",
			strings.Join(lines, "\n"))
	}
}

// ---------------------------------------------------------------------------
// D75 — the download name carried the voucher's UUID
// ---------------------------------------------------------------------------

// TestThePrintedVouchersFilenameCarriesItsMachineIdentity is named for the
// breach: pdfFilename JOINS the parts it is given, its doc comment claimed it
// used "the FIRST non-empty" one, and the caller passed class, number AND id —
// so every invoice downloaded as "Sales-INV-1-0f0b4c31-….pdf".
//
// The id is a FALLBACK and must still be there for an unnumbered voucher, or two
// of them would overwrite each other in a download folder. Both halves are
// asserted, because a fix that simply dropped the id would trade one defect for
// a worse one.
func TestThePrintedVouchersFilenameCarriesItsMachineIdentity(t *testing.T) {
	s := payrollServer(t)
	f := newPrintFixture(t, s)

	rec := printGet(t, s, f.base+"/print/vouchers/receipt/"+f.receiptID, f.ownerTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("print the receipt = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	assertIsAPDFFile(t, rec.Body.Bytes(), "the printed receipt")

	disp := rec.Header().Get("Content-Disposition")
	// The fixture's number is "RCPT/2026/007" — slashes and all, because that is
	// what a real Tally series looks like — and pdfFilename sanitises it.
	const want = `attachment; filename="receipt-RCPT-2026-007.pdf"`
	if disp != want {
		t.Errorf("Content-Disposition = %q, want %q. The voucher NUMBER is what an operator "+
			"filing a folder of documents reads; 36 characters of machine identity after it "+
			"push the number out of every truncated column it is rendered in", disp, want)
	}
	if strings.Contains(disp, f.receiptID) {
		t.Errorf("the download name carries the voucher's UUID (%s): %q", f.receiptID, disp)
	}

	// AND THE DOCUMENT NAMES ITSELF — the filename is not evidence on its own,
	// because a right name over the wrong bytes is the worse failure.
	text := pdfText(t, rec, "the printed receipt")
	if !strings.Contains(text, f.receiptNo) {
		t.Errorf("the printed receipt does not carry its own number %q:\n%s", f.receiptNo, text)
	}
	if !strings.Contains(text, f.partyName) {
		t.Errorf("the printed receipt does not name the party it was received from:\n%s", text)
	}
}

// assertIsAPDFFile is the file-level half of the oracle: a real PDF starts %PDF
// and ends %%EOF. printing.ExtractText also validates the cross-reference table
// on the way in, so between them a file that some readers open and others reject
// fails here rather than in somebody's hands.
func assertIsAPDFFile(t *testing.T, body []byte, what string) {
	t.Helper()
	if len(body) == 0 {
		t.Fatalf("%s is zero bytes", what)
	}
	if !strings.HasPrefix(string(body), "%PDF") {
		t.Fatalf("%s does not begin with %%PDF (%.40q)", what, body)
	}
	if !strings.HasSuffix(strings.TrimRight(string(body), "\r\n"), "%%EOF") {
		t.Fatalf("%s does not end with %%%%EOF, so it is a truncated file (%.40q)",
			what, body[max(0, len(body)-40):])
	}
}
