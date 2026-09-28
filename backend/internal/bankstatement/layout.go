package bankstatement

// READING THE FILE AS A BANK ACTUALLY EXPORTS IT — delimiter, preamble, header,
// footer — without guessing at any of them.
//
// Design memo: docs/design/t4-slice2-model.md §8 and docs/design/d123-w5-documents-bank.md §2.
//
// WHY THIS FILE EXISTS
// --------------------
// Slice 2a read "a CSV whose first non-blank row is the header" and told the
// operator to trim everything else by hand. No Indian bank exports that. Every
// retail download carries a PREAMBLE above the table (account holder, account
// number, IFSC, the period, sometimes the opening balance), many carry a FOOTER
// under it (a statement summary, "this is a computer generated statement"), one
// puts a row of asterisks between the header and the first transaction, and the
// ".xls" several banks offer is TAB-separated text with a spreadsheet extension.
// An operator asked to hand-trim a statement is an operator who deletes a row
// by mistake, and that is the one error this package exists to catch.
//
// THE RULE IS THE SAME ONE parse.go STATES: EVERY DECISION HERE IS MADE FROM
// EVIDENCE IN THE FILE, AND AMBIGUITY IS REFUSED.
//
//   - THE HEADER ROW IS FOUND BY WHAT IT SAYS, never by where it is. A row is
//     the header only when its own cells name a date, a narration and an amount
//     column (the synonym list in propose.go). Row 7 is not "probably the
//     header"; it is the header because it reads "Value Dt, Narration,
//     Withdrawal Amt.". A file with no such row keeps slice 2a's behaviour —
//     the first non-blank row — and resolve() then refuses it by naming the
//     columns, so the fallback can refuse but never mis-read.
//   - THE DELIMITER IS CHOSEN BY WHICH ONE PRODUCES THAT HEADER. Counting commas
//     is not evidence: a narration full of commas in a tab file would win.
//   - THE FOOTER STARTS AT A BLANK OR SEPARATOR ROW, AND ONLY IF NOTHING DATED
//     FOLLOWS IT. A trailing row with a malformed date is NOT quietly moved into
//     the footer — that would drop the last transaction, which the running
//     balance walk cannot see (nothing comes after it to disagree). It is parsed,
//     and refused by name.
//   - A SPREADSHEET OR PDF IS REFUSED BY NAME. Binary XLS/XLSX need a reader this
//     build does not have (§1.1: no new dependency to parse a bank statement),
//     and a best-effort read of one would populate plausible fields with bytes.

import (
	"encoding/csv"
	"fmt"
	"strings"
)

// table is one file split into the parts a statement has.
type table struct {
	delimiter rune
	// preamble is every row above the header, verbatim. It is where banks print
	// the opening balance, and summary.go reads it for exactly that.
	preamble [][]string
	header   []string
	// body is every row between the header and the footer. Separator rows
	// directly under the header have been dropped; nothing else has.
	body [][]string
	// offset is added to a body row's index to give the row number an operator
	// sees. It is zero when the header is the first non-blank row, which keeps
	// every row number slice 2a ever reported unchanged.
	offset int
}

// candidateDelimiters is tried in this order, and the first that yields a
// recognisable header row wins. Comma first because it is the commonest and the
// only one slice 2a read; a file that parses as a header under comma is read
// exactly as it always was.
var candidateDelimiters = []rune{',', '\t', ';', '|'}

// headerScanLimit bounds how far down the file the header is looked for. Real
// preambles run to a dozen rows; forty is generous, and the bound exists so a
// ten-thousand-row file with no header is not scanned twice over per delimiter.
const headerScanLimit = 40

// refuseNonText names the file formats this build does not read. Each is
// identified by its own magic bytes or markup, never by the file name, which an
// operator (or a bank) can make say anything.
func refuseNonText(content string) error {
	head := content
	if len(head) > 512 {
		head = head[:512]
	}
	lower := strings.ToLower(strings.TrimSpace(head))
	switch {
	case strings.HasPrefix(head, "\xD0\xCF\x11\xE0"):
		return fmt.Errorf("%w: this is a binary Excel (.xls) workbook; save it from Excel as CSV, "+
			"or download the bank's CSV / delimited-text export instead", ErrUnsupportedFormat)
	case strings.HasPrefix(head, "PK\x03\x04"):
		return fmt.Errorf("%w: this is an Excel (.xlsx) workbook; save it as CSV first", ErrUnsupportedFormat)
	case strings.HasPrefix(head, "%PDF"):
		return fmt.Errorf("%w: this is a PDF; this build reads CSV and delimited-text statements only", ErrUnsupportedFormat)
	case strings.HasPrefix(lower, "<html") || strings.HasPrefix(lower, "<!doctype html") ||
		strings.HasPrefix(lower, "<table"):
		// Several banks serve an HTML table under an .xls name. It is readable in
		// principle, but a half-built HTML table reader is precisely the
		// "best-effort parse" memo §8.1 refuses.
		return fmt.Errorf("%w: this is an HTML page saved with a spreadsheet extension; open it in "+
			"Excel and save it as CSV", ErrUnsupportedFormat)
	case strings.HasPrefix(strings.TrimSpace(head), ":20:"):
		return fmt.Errorf("%w: MT940 is not supported in this build", ErrUnsupportedFormat)
	}
	return nil
}

// readTable splits content into preamble, header and body.
//
// It never drops a row that could be a transaction: the only rows it removes
// are leading blank rows, the preamble (above a header the file itself names),
// and separator rows directly under the header. The footer is split off later,
// in splitFooter, because only the mapping says which column holds the date
// that decides it.
func readTable(content string) (table, error) {
	if err := refuseNonText(content); err != nil {
		return table{}, err
	}
	// A UTF-8 byte-order mark is invisible in every editor and would make the
	// first header "\ufeffDate", which matches no synonym and no mapping — a
	// refusal the operator could stare at for an hour.
	content = strings.TrimPrefix(content, "\ufeff")

	var fallback *table
	var firstErr error
	for _, d := range candidateDelimiters {
		rows, err := splitRows(content, d)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		t, located := locate(rows, d)
		if located {
			return t, nil
		}
		if d == ',' {
			// Slice 2a's reading, kept as the answer when no delimiter produces a
			// header this package can name. resolve() refuses it by naming the
			// columns if it is wrong, which is a refusal, not a mis-read.
			tt := t
			fallback = &tt
		}
	}
	if fallback != nil {
		return *fallback, nil
	}
	if firstErr != nil {
		return table{}, firstErr
	}
	return table{}, fmt.Errorf("%w: the file is empty", ErrEmptyFile)
}

// splitRows reads every record under one delimiter.
//
// LazyQuotes is on for every delimiter but the comma. A tab-separated export
// routinely carries a bare quote inside a narration ("12\" PIPE"), and strict
// quoting would refuse the file for a character the bank did not mean as
// syntax. The comma keeps strict quoting because slice 2a shipped it that way
// and a comma file with a stray quote is genuinely ambiguous.
func splitRows(content string, d rune) ([][]string, error) {
	r := csv.NewReader(strings.NewReader(content))
	r.Comma = d
	r.FieldsPerRecord = -1
	// ⛔ NOT FOR TAB. encoding/csv trims leading white space "even if the field
	// delimiter is white space", so under a tab delimiter an EMPTY cell vanishes
	// into its neighbour's separator and every later column shifts left by one:
	// SBI's empty Debit cell made the Credit read as the Debit and the Balance
	// as the Credit. cell() trims every value anyway.
	r.TrimLeadingSpace = d != '\t'
	r.LazyQuotes = d != ','
	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupportedFormat, err)
	}
	return rows, nil
}

// locate finds the header row in rows, returning whether it was found BY ITS
// CONTENT (true) or fell back to the first non-blank row (false).
func locate(rows [][]string, d rune) (table, bool) {
	for len(rows) > 0 && allBlank(rows[0]) {
		rows = rows[1:]
	}
	t := table{delimiter: d}
	if len(rows) == 0 {
		return t, false
	}
	at, found := -1, false
	for i := 0; i < len(rows) && i < headerScanLimit; i++ {
		if looksLikeHeader(rows[i]) {
			at, found = i, true
			break
		}
	}
	if !found {
		at = 0
	}
	t.preamble = rows[:at]
	t.header = dedupeHeader(rows[at])
	t.offset = at
	body := rows[at+1:]
	// A separator row UNDER THE HEADER is part of the header's typography, not a
	// transaction. Anywhere else it is handled by splitFooter.
	for len(body) > 0 && isSeparatorRow(body[0]) {
		body = body[1:]
		t.offset++
	}
	t.body = body
	return t, found
}

// looksLikeHeader is the evidence test: the row's OWN cells must name a date,
// a narration, and at least one money column. Three independent fields, because
// one or two matching words appear in preambles ("Date of statement",
// "Balance as on").
func looksLikeHeader(row []string) bool {
	var date, desc, money bool
	for _, c := range row {
		n := normaliseHeader(c)
		if n == "" {
			continue
		}
		switch {
		case matchesAny(n, "value_date", "txn_date"):
			date = true
		case matchesAny(n, "description"):
			desc = true
		case matchesAny(n, "deposit", "withdrawal", "amount"):
			money = true
		}
	}
	return date && desc && money
}

func matchesAny(n string, fields ...string) bool {
	for _, f := range fields {
		for _, s := range synonyms[f] {
			if n == s {
				return true
			}
		}
	}
	return false
}

// dedupeHeader trims each header and renames a REPEATED one "Name (2)",
// "Name (3)", compared case-insensitively.
//
// ⛔ SLICE 2a RESOLVED A REPEATED HEADER TO ITS LAST OCCURRENCE, SILENTLY.
// resolve() builds a name→position map, so a file with two "Dr / Cr" columns
// (one for the amount, one for the balance) mapped "Dr / Cr" to the BALANCE's
// flag, and every amount's direction was read off the balance's sign — which
// for an ordinary in-credit account is "Cr" on every row, turning every
// withdrawal into a deposit. Renaming makes both columns visible in the list the
// operator picks from, and makes a mapping name exactly one of them.
func dedupeHeader(row []string) []string {
	out := make([]string, len(row))
	seen := map[string]int{}
	for i, h := range row {
		h = strings.TrimSpace(h)
		key := strings.ToLower(h)
		seen[key]++
		if n := seen[key]; n > 1 && h != "" {
			h = fmt.Sprintf("%s (%d)", h, n)
		}
		out[i] = h
	}
	return out
}

// isSeparatorRow is a row made only of rule characters — "*****", "-----",
// "=====" — with no letters or digits in any cell.
func isSeparatorRow(row []string) bool {
	sawRule := false
	for _, c := range row {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		for _, r := range c {
			if !strings.ContainsRune("*-=_~.", r) {
				return false
			}
		}
		sawRule = true
	}
	return sawRule
}

// splitFooter separates trailing non-transaction rows from the body.
//
// The footer starts at the first blank or separator row AFTER WHICH NO ROW
// CARRIES A PARSEABLE DATE in the value-date column. Both halves matter:
//
//   - "Blank or separator" rather than "not a date": a last transaction whose
//     date cell is malformed must reach parseRow and be REFUSED. Moved into the
//     footer it would be a dropped row, and a dropped LAST row is invisible to
//     the running-balance walk because nothing after it disagrees.
//   - "No dated row after it": a blank row followed by more transactions is the
//     mis-saved export slice 2a refuses (TestABlankRowInTheMiddleIsRefused), and
//     it still is, because the gap stays in the body.
func splitFooter(body [][]string, dateCol int, parseDate func(string) bool) ([][]string, [][]string) {
	for i, row := range body {
		if !allBlank(row) && !isSeparatorRow(row) {
			continue
		}
		dated := false
		for _, later := range body[i+1:] {
			if parseDate(cell(later, dateCol)) {
				dated = true
				break
			}
		}
		if !dated {
			return body[:i], body[i:]
		}
	}
	return body, nil
}
