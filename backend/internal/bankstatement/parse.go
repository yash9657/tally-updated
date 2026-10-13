// Package bankstatement reads a bank's own CSV export into the rows this
// product can reason about, and REFUSES every file it cannot read honestly.
//
// Design memo: docs/design/t4-slice2-model.md. Read §8 before changing
// anything here — it settles the two product calls this file implements.
//
// THE ONE RULE THIS PACKAGE EXISTS TO HONOUR
// ------------------------------------------
// A bank statement is the one input where a WRONG READ LOOKS EXACTLY LIKE A
// RIGHT ONE. Every value in the file is a real number off a real statement, so
// a mis-mapped column does not produce garbage — it produces plausible money,
// on plausible dates, that was never a transaction. Nothing raises, nothing
// looks odd, and the operator reconciles against it.
//
// So every ambiguity in this file is resolved by REFUSING, never by guessing:
//
//   - THERE ARE NO BUILT-IN PER-BANK PROFILES (memo §8.2). The column mapping
//     is an INPUT the operator confirms. A mapping may be PROPOSED from the
//     file's own header row, because that is evidence in front of us; a layout
//     recalled from memory is not, and no real HDFC/ICICI/SBI export exists in
//     this environment to check one against.
//   - A HEADER THAT CANNOT BE PLACED IS NAMED, NOT POSITIONALLY GUESSED.
//     "Column 4 is probably the amount" is the silent mis-parse above.
//   - THE DATE LAYOUT IS DECLARED, NOT SNIFFED. 03/04/2026 is the 3rd of April
//     or the 4th of March and the file cannot tell you which. A proposal is
//     offered only when the file's OWN dates disambiguate it.
//   - DIRECTION IS NORMALISED AT THIS BOUNDARY and is never a sign afterwards.
//
// ⚠️ DIRECTION IS INVERTED RELATIVE TO THE BANK'S WORDING, AND THIS IS THE
// SHARPEST EDGE IN THE PACKAGE. A DEPOSIT into our account is a DEBIT to the
// bank ledger in our books — the asset rose — though the bank calls it a
// credit, because in the BANK's books we are a liability. Everything leaving
// this package is in OUR orientation, so the matcher can compare it directly
// against a posting's dr_cr. A comparison that had to remember the inversion
// would eventually forget, and the result is ZERO MATCHES, which reads as
// "nothing to reconcile" rather than as a bug.
package bankstatement

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// The refusals. They are sentinels so the HTTP layer can choose a status and a
// test can assert WHICH refusal fired rather than "some error happened" —
// HANDOFF-SESSION-ITEM-ZERO.md §4.2: two HTTP tests once passed while sending a
// malformed body, because every domain refusal on that route was a 422 and a
// bare status check cannot tell them apart.
var (
	// ErrEmptyFile is an import with no transaction rows. It is a refusal and
	// not a successful import of nothing, because "imported 0 lines" reads as
	// "your bank had no transactions that month".
	ErrEmptyFile = errors.New("bankstatement: the file has no transaction rows")
	// ErrUnmappedColumn is a required field no column was mapped to.
	ErrUnmappedColumn = errors.New("bankstatement: a required column is not mapped")
	// ErrUnknownColumn is a mapping naming a header the file does not have —
	// usually a mapping confirmed against a different export of the same bank.
	ErrUnknownColumn = errors.New("bankstatement: the file has no such column")
	// ErrBadDate is a date cell the declared layout cannot read.
	ErrBadDate = errors.New("bankstatement: date does not match the declared layout")
	// ErrBadAmount is an amount cell that is not a number.
	ErrBadAmount = errors.New("bankstatement: amount is not a number")
	// ErrBothColumnsFilled is a two-column file with a deposit AND a withdrawal
	// on one row. See the normalisation comment: it is not summable.
	ErrBothColumnsFilled = errors.New("bankstatement: a row has both a deposit and a withdrawal")
	// ErrNoAmount is a row with no amount in any direction column.
	ErrNoAmount = errors.New("bankstatement: a row has no amount")
	// ErrBalanceWalk is B-4: the running balances do not walk. The file was
	// mis-parsed, or a row is missing or duplicated.
	ErrBalanceWalk = errors.New("bankstatement: the running balances do not walk")
	// ErrUnsupportedFormat is a file this build does not parse (memo §8.1).
	ErrUnsupportedFormat = errors.New("bankstatement: unsupported file format")
)

// DirectionEncoding is HOW a file says which way money moved. The three forms
// are the ones §1.1 of the memo found across real formats.
//
// IT IS AN EXPLICIT CHOICE, NOT AN INFERENCE FROM WHICH FIELDS ARE SET. A
// mapping that set both an Amount and a Deposit column would otherwise be
// half-specified, and this package would have to pick — which is the guessing
// the package exists to avoid.
type DirectionEncoding string

const (
	// TwoColumns: separate Deposit and Withdrawal columns, one filled per row.
	// The commonest Indian retail layout.
	TwoColumns DirectionEncoding = "two-columns"
	// SignedAmount: one Amount column whose SIGN carries the direction, in the
	// BANK's orientation (a deposit is positive).
	SignedAmount DirectionEncoding = "signed-amount"
	// AmountAndFlag: one Amount column plus a flag column holding C or D, in
	// the BANK's wording (C = credit = a deposit to us). MT940's encoding.
	AmountAndFlag DirectionEncoding = "amount-and-flag"
)

// DateLayout is a NAMED date layout, not a raw Go layout string.
//
// Named because the layout arrives from a client. An arbitrary layout string
// would let a caller pass something that parses the wrong field — "2006" alone
// happily reads "15" as a year — and the resulting dates would be wrong without
// being invalid.
type DateLayout string

const (
	DMYSlash  DateLayout = "dd/mm/yyyy"
	DMYDash   DateLayout = "dd-mm-yyyy"
	DMonY     DateLayout = "dd-Mon-yyyy"
	YMDDash   DateLayout = "yyyy-mm-dd"
	DMYSlash2 DateLayout = "dd/mm/yy"
	MDYSlash  DateLayout = "mm/dd/yyyy"
	// DMonYSpace is "01 Apr 2026", State Bank of India's statement layout
	// (profiles.go). Month by NAME, so it can never be confused with mm/dd.
	DMonYSpace DateLayout = "dd Mon yyyy"
)

// goLayout maps a named layout to Go's reference time.
var goLayout = map[DateLayout]string{
	DMYSlash:   "02/01/2006",
	DMYDash:    "02-01-2006",
	DMonY:      "02-Jan-2006",
	YMDDash:    "2006-01-02",
	DMYSlash2:  "02/01/06",
	MDYSlash:   "01/02/2006",
	DMonYSpace: "02 Jan 2006",
}

// KnownDateLayouts lists every layout this build can read, for the client's
// picker. Sorted so the list is stable.
func KnownDateLayouts() []DateLayout {
	out := make([]DateLayout, 0, len(goLayout))
	for l := range goLayout {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ColumnMapping is the operator-confirmed interpretation of one file (memo
// §8.2). Every field except Encoding and DateLayout is a HEADER NAME as it
// appears in the file; an empty name means "this file has no such column".
//
// It is stored verbatim on the import batch, so the question "how was this file
// read?" stays answerable months later.
type ColumnMapping struct {
	Encoding   DirectionEncoding `json:"encoding"`
	DateLayout DateLayout        `json:"date_layout"`

	ValueDate   string `json:"value_date"`
	TxnDate     string `json:"txn_date,omitempty"`
	Description string `json:"description"`

	// Exactly which of these matter depends on Encoding — see validate().
	Deposit    string `json:"deposit,omitempty"`
	Withdrawal string `json:"withdrawal,omitempty"`
	Amount     string `json:"amount,omitempty"`
	Direction  string `json:"direction,omitempty"`

	RunningBalance string `json:"running_balance,omitempty"`
	// BalanceSign is a Cr/Dr flag column carrying the RUNNING BALANCE's sign,
	// for exports that print the balance unsigned beside its own flag (Kotak;
	// see profiles.go). "Dr" there means overdrawn — a NEGATIVE balance in the
	// bank's orientation. Optional; a balance cell with its own suffix needs none.
	BalanceSign   string `json:"balance_sign,omitempty"`
	BankReference string `json:"bank_reference,omitempty"`
	ChequeNumber  string `json:"cheque_number,omitempty"`
}

// Line is one transaction as the bank reported it, normalised.
type Line struct {
	// LineNo is the 1-based position in the FILE, counting the header as 0.
	// Every refusal quotes it, because a row number an operator can find in
	// their spreadsheet is the difference between a fixable import and a
	// support call.
	LineNo    int
	ValueDate time.Time
	// TxnDate is the zero time when the file carries only one date. STORED,
	// NEVER CONFLATED with ValueDate.
	TxnDate     time.Time
	Description string
	// Amount is a MAGNITUDE. Direction is DrCr and never a sign.
	Amount decimal.Decimal
	// DrCr is in OUR orientation: "D" for a deposit (our bank asset rose),
	// "C" for a withdrawal. See the package comment — this inverts the word
	// the statement itself uses.
	DrCr string
	// RunningBalance is the bank's own arithmetic, kept for VERIFICATION ONLY.
	// HasBalance distinguishes "the file said zero" from "the file said
	// nothing", which a bare decimal.Zero cannot.
	RunningBalance decimal.Decimal
	HasBalance     bool
	BankReference  string
	ChequeNumber   string
	// Raw is the original row keyed by header, preserved verbatim: a field we
	// did not model today is still recoverable tomorrow without asking the
	// operator to re-download a statement their bank may no longer serve.
	Raw map[string]string
	// Hash identifies the TRANSACTION for re-import detection, and Occurrence
	// separates two that are identical in every modelled field. See the
	// bank_statement_line column comments in migration 0062.
	Hash       string
	Occurrence int
	// Narration is the structured reading of Description (narration.go). An
	// ANNOTATION: it is never an input to the amount, direction or date, and it
	// is deliberately excluded from Hash, so improving the parser can never
	// make a re-import of the same file look like new transactions.
	Narration Narration
}

// Statement is a parsed file.
type Statement struct {
	Lines []Line
	// OpeningBalance is derived from the FIRST line: its running balance less
	// its own movement. It is evidence, never an input to a derivation.
	OpeningBalance decimal.Decimal
	ClosingBalance decimal.Decimal
	// BalanceWalked is false when the file carries no running balance, in which
	// case B-4 could not run. THE IMPORT MUST SAY SO rather than let silence
	// read as "the walk passed" — a check that did not run and a check that
	// passed are the same shape from the outside, and only one of them is safe.
	BalanceWalked bool
	PeriodFrom    time.Time
	PeriodTo      time.Time
	// Order is the row order the running balances revealed; OrderUnknown when
	// the file carries none. A newest-first file is walked bottom-up.
	Order Order
	// Summary is what the file's preamble and footer DECLARED (summary.go).
	Summary Summary
	// Proof is set by ParseStatement, and is the zero value after a bare Parse.
	Proof BalanceProof
	// Delimiter is the one the file was read with, reported so a proposal can
	// say "read as tab-separated" rather than leave the operator wondering why
	// an ".xls" parsed.
	Delimiter rune
}

// Parse reads a CSV bank statement under a confirmed mapping.
//
// It is total on refusals: every return path is either a complete Statement or
// an error naming the row and the column that stopped it.
func Parse(content string, m ColumnMapping) (Statement, error) {
	t, err := readTable(content)
	if err != nil {
		return Statement{}, err
	}
	header := t.header
	idx, err := m.resolve(header)
	if err != nil {
		return Statement{}, err
	}
	layout := goLayout[m.DateLayout]
	records, footer := splitFooter(t.body, idx.valueDate, func(v string) bool {
		if v == "" {
			return false
		}
		_, perr := time.Parse(layout, v)
		return perr == nil
	})
	if len(records) == 0 {
		return Statement{}, fmt.Errorf("%w: it has a header row and nothing under it", ErrEmptyFile)
	}

	summaryRows := make([][]string, 0, len(t.preamble)+len(footer))
	summaryRows = append(summaryRows, t.preamble...)
	summaryRows = append(summaryRows, footer...)
	summary, err := readSummary(summaryRows)
	if err != nil {
		return Statement{}, err
	}

	st := Statement{Lines: make([]Line, 0, len(records)), Summary: summary, Delimiter: t.delimiter}
	for i, rec := range records {
		// +2: one for the header row, one because operators count from 1. The
		// offset is the preamble (and any separator row under the header), so
		// the number is the row the operator sees in their spreadsheet.
		lineNo := t.offset + i + 2
		ln, err := m.parseRow(rec, header, idx, lineNo)
		if err != nil {
			return Statement{}, err
		}
		st.Lines = append(st.Lines, ln)
	}

	assignOccurrences(st.Lines)

	if err := walkBalances(&st); err != nil {
		return Statement{}, err
	}
	st.PeriodFrom, st.PeriodTo = period(st.Lines)
	return st, nil
}

// ParseStatement is Parse plus the MANDATORY balance proof (proof.go): the
// entry point every import goes through. A statement that cannot be proved is
// refused here, before any transaction opens.
func ParseStatement(content string, m ColumnMapping, operator Declared) (Statement, error) {
	st, err := Parse(content, m)
	if err != nil {
		return Statement{}, err
	}
	if err := prove(&st, st.Summary, operator); err != nil {
		return Statement{}, err
	}
	return st, nil
}

// readCSV splits the file into its header row and its records.
//
// FieldsPerRecord is set to -1 so a ragged file reaches parseRow, where a
// missing cell is refused against the COLUMN THAT IS MISSING. csv's own
// "wrong number of fields" error names neither the column nor anything the
// operator can act on.
//
// ⚠️ IT NO LONGER ASSUMES THE HEADER IS THE FIRST ROW. readTable (layout.go)
// finds it by what it says, detects the delimiter, and strips the preamble;
// this wrapper keeps propose.go's call site. Trailing blank rows are trimmed
// here because the proposal has no mapping to split a footer with.
func readCSV(content string) ([]string, [][]string, error) {
	t, err := readTable(content)
	if err != nil {
		return nil, nil, err
	}
	body := t.body
	for len(body) > 0 && allBlank(body[len(body)-1]) {
		body = body[:len(body)-1]
	}
	return t.header, body, nil
}

func allBlank(rec []string) bool {
	for _, c := range rec {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

// colIndex is the resolved header position of each mapped field. -1 means the
// mapping did not name a column for it.
type colIndex struct {
	valueDate, txnDate, description             int
	deposit, withdrawal, amount, direction      int
	runningBalance, bankReference, chequeNumber int
	balanceSign                                 int
}

// resolve validates the mapping against the file's actual header and turns
// header NAMES into positions.
//
// Both halves matter. A mapping that names a column the file does not have is
// usually a mapping confirmed against a different export of the same bank, and
// it must be refused loudly — under a positional fallback it would read a
// neighbouring column and produce plausible money.
func (m ColumnMapping) resolve(header []string) (colIndex, error) {
	pos := make(map[string]int, len(header))
	for i, h := range header {
		pos[strings.ToLower(h)] = i
	}
	idx := colIndex{-1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1}

	find := func(name, field string, required bool) (int, error) {
		if strings.TrimSpace(name) == "" {
			if required {
				return -1, fmt.Errorf("%w: no column is mapped to %s; the file's columns are %s",
					ErrUnmappedColumn, field, strings.Join(header, ", "))
			}
			return -1, nil
		}
		i, ok := pos[strings.ToLower(strings.TrimSpace(name))]
		if !ok {
			return -1, fmt.Errorf("%w: %s is mapped to %q, which this file does not have; its columns are %s",
				ErrUnknownColumn, field, name, strings.Join(header, ", "))
		}
		return i, nil
	}

	if _, ok := goLayout[m.DateLayout]; !ok {
		return idx, fmt.Errorf("%w: date layout %q is not one this build reads; it reads %v",
			ErrUnmappedColumn, m.DateLayout, KnownDateLayouts())
	}

	var err error
	if idx.valueDate, err = find(m.ValueDate, "the value date", true); err != nil {
		return idx, err
	}
	if idx.txnDate, err = find(m.TxnDate, "the transaction date", false); err != nil {
		return idx, err
	}
	if idx.description, err = find(m.Description, "the description", true); err != nil {
		return idx, err
	}

	// WHICH COLUMNS ARE REQUIRED DEPENDS ON THE ENCODING, and each branch
	// requires ALL of its own. A two-column mapping missing the withdrawal
	// column would otherwise import every payment as having no amount.
	switch m.Encoding {
	case TwoColumns:
		if idx.deposit, err = find(m.Deposit, "the deposit amount", true); err != nil {
			return idx, err
		}
		if idx.withdrawal, err = find(m.Withdrawal, "the withdrawal amount", true); err != nil {
			return idx, err
		}
	case SignedAmount:
		if idx.amount, err = find(m.Amount, "the amount", true); err != nil {
			return idx, err
		}
	case AmountAndFlag:
		if idx.amount, err = find(m.Amount, "the amount", true); err != nil {
			return idx, err
		}
		if idx.direction, err = find(m.Direction, "the credit/debit flag", true); err != nil {
			return idx, err
		}
	default:
		return idx, fmt.Errorf("%w: direction encoding %q is not one of %s, %s, %s",
			ErrUnmappedColumn, m.Encoding, TwoColumns, SignedAmount, AmountAndFlag)
	}

	if idx.runningBalance, err = find(m.RunningBalance, "the running balance", false); err != nil {
		return idx, err
	}
	if idx.balanceSign, err = find(m.BalanceSign, "the balance's Cr/Dr flag", false); err != nil {
		return idx, err
	}
	if idx.balanceSign >= 0 && idx.runningBalance < 0 {
		return idx, fmt.Errorf("%w: a balance Cr/Dr flag is mapped but no running balance is", ErrUnmappedColumn)
	}
	if idx.bankReference, err = find(m.BankReference, "the bank reference", false); err != nil {
		return idx, err
	}
	if idx.chequeNumber, err = find(m.ChequeNumber, "the cheque number", false); err != nil {
		return idx, err
	}
	return idx, nil
}

// cell reads a column from a record, tolerating a short row.
func cell(rec []string, i int) string {
	if i < 0 || i >= len(rec) {
		return ""
	}
	return strings.TrimSpace(rec[i])
}

func (m ColumnMapping) parseRow(rec, header []string, idx colIndex, lineNo int) (Line, error) {
	// A blank row in the MIDDLE of a statement is refused. It is exactly what a
	// mis-saved export looks like, and silently skipping it would remove a row
	// the balance walk is counting on — turning B-4's clean refusal into a
	// mysterious one further down.
	if allBlank(rec) {
		return Line{}, fmt.Errorf("%w: row %d is blank; a statement has no blank rows in the middle of it",
			ErrEmptyFile, lineNo)
	}

	ln := Line{LineNo: lineNo, Raw: map[string]string{}}
	for i, h := range header {
		ln.Raw[h] = cell(rec, i)
	}

	var err error
	if ln.ValueDate, err = m.date(cell(rec, idx.valueDate), "the value date", lineNo); err != nil {
		return Line{}, err
	}
	if idx.txnDate >= 0 {
		if raw := cell(rec, idx.txnDate); raw != "" {
			if ln.TxnDate, err = m.date(raw, "the transaction date", lineNo); err != nil {
				return Line{}, err
			}
		}
	}
	ln.Description = cell(rec, idx.description)
	ln.BankReference = cell(rec, idx.bankReference)
	ln.ChequeNumber = cell(rec, idx.chequeNumber)

	if ln.Amount, ln.DrCr, err = m.direction(rec, idx, lineNo); err != nil {
		return Line{}, err
	}

	if idx.runningBalance >= 0 {
		if raw := cell(rec, idx.runningBalance); raw != "" {
			// parseBalance, NOT parseAmount: in a balance cell "Dr" is a SIGN.
			// See summary.go's parseBalance.
			bal, berr := parseBalance(raw)
			if berr != nil {
				return Line{}, fmt.Errorf("%w: row %d, the running balance %q", ErrBadAmount, lineNo, raw)
			}
			if idx.balanceSign >= 0 {
				switch flag := strings.ToUpper(strings.TrimRight(cell(rec, idx.balanceSign), ".")); flag {
				case "DR", "D", "DEBIT":
					bal = bal.Abs().Neg()
				case "CR", "C", "CREDIT", "":
					// An empty flag leaves the cell's own reading, which is
					// positive unless the cell itself said Dr.
				default:
					return Line{}, fmt.Errorf("%w: row %d has %q in the balance's Cr/Dr column, which is neither",
						ErrBadAmount, lineNo, flag)
				}
			}
			ln.RunningBalance = bal
			ln.HasBalance = true
		}
	}

	ln.Hash = hashLine(ln)
	ln.Narration = ParseNarration(ln.Description)
	return ln, nil
}

// direction normalises all three encodings to (magnitude, our dr_cr).
//
// ⚠️ THE INVERSION HAPPENS HERE AND NOWHERE ELSE. A deposit — the bank's
// "credit" — is a DEBIT to our bank ledger. Every one of the three branches
// below does that flip, which is the whole reason this function exists rather
// than three call sites doing it inline.
func (m ColumnMapping) direction(rec []string, idx colIndex, lineNo int) (decimal.Decimal, string, error) {
	switch m.Encoding {
	case TwoColumns:
		dep, wdr := cell(rec, idx.deposit), cell(rec, idx.withdrawal)
		depZero, wdrZero := isBlankOrZero(dep), isBlankOrZero(wdr)
		if !depZero && !wdrZero {
			// NOT NETTED. Netting them would invent a transaction that is on no
			// statement, and the two amounts are the evidence that the mapping
			// or the file is wrong.
			return decimal.Zero, "", fmt.Errorf(
				"%w: row %d has %q in the deposit column and %q in the withdrawal column; "+
					"one row is one movement, so either the columns are mapped the wrong way round or the file is not what it seems",
				ErrBothColumnsFilled, lineNo, dep, wdr)
		}
		if depZero && wdrZero {
			return decimal.Zero, "", fmt.Errorf("%w: row %d has no deposit and no withdrawal", ErrNoAmount, lineNo)
		}
		raw, drcr := dep, "D" // a DEPOSIT is a DEBIT to our bank ledger
		if depZero {
			raw, drcr = wdr, "C"
		}
		amt, err := parseAmount(raw)
		if err != nil {
			return decimal.Zero, "", fmt.Errorf("%w: row %d, %q", ErrBadAmount, lineNo, raw)
		}
		return amt.Abs(), drcr, nil

	case SignedAmount:
		raw := cell(rec, idx.amount)
		if isBlankOrZero(raw) {
			return decimal.Zero, "", fmt.Errorf("%w: row %d has no amount", ErrNoAmount, lineNo)
		}
		amt, err := parseAmount(raw)
		if err != nil {
			return decimal.Zero, "", fmt.Errorf("%w: row %d, %q", ErrBadAmount, lineNo, raw)
		}
		// Positive is a deposit in the BANK's sign convention, which is a debit
		// in ours. The sign is consumed HERE and never travels.
		drcr := "D"
		if amt.IsNegative() {
			drcr = "C"
		}
		return amt.Abs(), drcr, nil

	case AmountAndFlag:
		raw, flag := cell(rec, idx.amount), strings.ToUpper(cell(rec, idx.direction))
		if isBlankOrZero(raw) {
			return decimal.Zero, "", fmt.Errorf("%w: row %d has no amount", ErrNoAmount, lineNo)
		}
		amt, err := parseAmount(raw)
		if err != nil {
			return decimal.Zero, "", fmt.Errorf("%w: row %d, %q", ErrBadAmount, lineNo, raw)
		}
		switch {
		// The bank's C (credit to us = a deposit) is OUR debit. Getting this
		// backwards is the single easiest way to import a whole statement that
		// matches nothing.
		case flag == "C" || flag == "CR" || flag == "CREDIT":
			return amt.Abs(), "D", nil
		case flag == "D" || flag == "DR" || flag == "DEBIT":
			return amt.Abs(), "C", nil
		default:
			return decimal.Zero, "", fmt.Errorf(
				"%w: row %d has %q in the credit/debit column, which is neither a credit nor a debit",
				ErrNoAmount, lineNo, flag)
		}
	}
	return decimal.Zero, "", fmt.Errorf("%w: encoding %q", ErrUnmappedColumn, m.Encoding)
}

func (m ColumnMapping) date(raw, field string, lineNo int) (time.Time, error) {
	if raw == "" {
		return time.Time{}, fmt.Errorf("%w: row %d has no value in %s", ErrBadDate, lineNo, field)
	}
	t, err := time.Parse(goLayout[m.DateLayout], raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: row %d, %s is %q but the mapping declares %s",
			ErrBadDate, lineNo, field, raw, m.DateLayout)
	}
	return t, nil
}

// parseAmount reads an Indian-format money cell into a decimal.
//
// NO float64, ANYWHERE. Commas are lakh grouping ("1,25,000.00"), not
// thousands, so they are stripped rather than interpreted; a trailing Cr/Dr
// suffix is stripped because some exports carry it INSIDE the amount cell,
// where it is a restatement of the direction column and not an amount.
func parseAmount(raw string) (decimal.Decimal, error) {
	s := strings.TrimSpace(raw)
	s = strings.NewReplacer(",", "", " ", "", " ", "", "₹", "").Replace(s)
	// A parenthesised amount is the accounting negative: (1,200.00).
	neg := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		neg, s = true, strings.TrimSuffix(strings.TrimPrefix(s, "("), ")")
	}
	up := strings.ToUpper(s)
	// "Dr." with a full stop is as common as "Dr" in exports.
	for _, suffix := range []string{"CR.", "DR.", "CR", "DR"} {
		if strings.HasSuffix(up, suffix) {
			s = s[:len(s)-len(suffix)]
			break
		}
	}
	if s == "" {
		return decimal.Zero, fmt.Errorf("%w: %q", ErrBadAmount, raw)
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero, fmt.Errorf("%w: %q", ErrBadAmount, raw)
	}
	if neg {
		d = d.Neg()
	}
	return d, nil
}

func isBlankOrZero(raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return true
	}
	d, err := parseAmount(raw)
	return err == nil && d.IsZero()
}

// signed returns the line's effect on the balance in the BANK's own direction:
// a deposit (our "D") increases it. Used only by the balance walk, which
// compares against the bank's own figures.
func (l Line) signed() decimal.Decimal {
	if l.DrCr == "D" {
		return l.Amount
	}
	return l.Amount.Neg()
}

// walkBalances is invariant B-4: the imported lines must walk from one running
// balance to the next, to the paisa.
//
// THIS IS THE ONLY CHECK THAT CATCHES A DROPPED OR DUPLICATED ROW, and it is
// why the running balance is worth importing at all. It is a CHECK, never an
// input: nothing downstream derives anything from these figures.
//
// ZERO TOLERANCE, DELIBERATELY (memo §3, and reporting/bankrecon.go's header:
// "a tolerance is how a real error hides for a year"). If this ever needs one
// it is a named, configured, displayed number — never a constant here.
//
// When the file carries no running balance the walk cannot run, and
// BalanceWalked stays false so the import can SAY that. A check that did not
// run must never be reported the same way as a check that passed.
//
// ⭐ BOTH DIRECTIONS ARE TRIED, AND THE BALANCES DECIDE WHICH. Several banks
// export newest-first. Slice 2a walked in file order only, so a correct
// newest-first statement was refused as "a row is missing" at row 3. The
// forward walk runs first and ITS error is the one reported when neither
// direction holds, so every refusal slice 2a produced is unchanged. The dates
// do not choose the direction — two rows on one day cannot be ordered by date
// at all, and the balance is the bank's own statement of the order — EXCEPT
// in the one case the balances cannot decide: the mirror image (see below).
func walkBalances(st *Statement) error {
	for _, ln := range st.Lines {
		if !ln.HasBalance {
			st.BalanceWalked = false
			st.Order = OrderUnknown
			return nil
		}
	}
	rev := make([]Line, len(st.Lines))
	for i, l := range st.Lines {
		rev[len(st.Lines)-1-i] = l
	}
	fwdErr := walk(st.Lines, false)
	var order Order
	var oldestFirst []Line
	switch {
	case fwdErr == nil:
		order, oldestFirst = OldestFirst, st.Lines
	case walk(rev, false) == nil:
		order, oldestFirst = NewestFirst, rev
	default:
		return fwdErr
	}
	// ⛔ THE MIRROR IMAGE. A statement read in the OTHER order with EVERY
	// sign inverted walks too exactly when each row's movement equals the
	// next one's — a run of identical EMIs, SIP debits, salary credits, any
	// one-row file. For such a file the balances cannot tell "oldest-first,
	// withdrawals" from "newest-first, deposits", which is precisely what a
	// Deposit/Withdrawal mapping entered backwards looks like; accepting the
	// walk imported every direction flipped under proof "running". So the
	// dates decide, and only when they are unambiguous; otherwise the running
	// balance has proved nothing about direction and says so (BalanceWalked
	// false: the import then needs declared balances, proof.go).
	var mirror []Line
	if order == OldestFirst {
		mirror = rev
	} else {
		mirror = st.Lines
	}
	if walk(mirror, true) == nil {
		switch datesSay(st.Lines) {
		case OldestFirst:
			if order != OldestFirst {
				return mirroredErr(OldestFirst)
			}
		case NewestFirst:
			if order != NewestFirst {
				return mirroredErr(NewestFirst)
			}
		default:
			st.BalanceWalked = false
			st.Order = OrderUnknown
			return nil
		}
	}
	oldest := oldestFirst[0]
	st.OpeningBalance = oldest.RunningBalance.Sub(oldest.signed())
	st.ClosingBalance = oldestFirst[len(oldestFirst)-1].RunningBalance
	st.BalanceWalked, st.Order = true, order
	return nil
}

func mirroredErr(dates Order) error {
	return fmt.Errorf("%w: the running balances only walk if the file is read %s, but its dates run %s. "+
		"Every row's direction would be inverted: the deposit and withdrawal columns (or the amount's sign) "+
		"are mapped the wrong way round — the statement is NOT imported",
		ErrBalanceWalk, map[Order]string{OldestFirst: "newest-first", NewestFirst: "oldest-first"}[dates], dates)
}

// datesSay is the row order the dates alone establish: OldestFirst when they
// never fall and rise at least once, NewestFirst when they never rise and fall
// at least once, OrderUnknown otherwise (one date throughout, or both ways).
// The transaction date is used where the file has one — it is what banks sort
// by; the value date can legitimately run out of order.
func datesSay(lines []Line) Order {
	at := func(l Line) time.Time {
		if !l.TxnDate.IsZero() {
			return l.TxnDate
		}
		return l.ValueDate
	}
	up, down := false, false
	for i := 1; i < len(lines); i++ {
		a, b := at(lines[i-1]), at(lines[i])
		switch {
		case b.After(a):
			up = true
		case b.Before(a):
			down = true
		}
	}
	switch {
	case up && !down:
		return OldestFirst
	case down && !up:
		return NewestFirst
	}
	return OrderUnknown
}

// walk checks that each line's balance is the previous balance plus its own
// movement, in the order given.
//
// invert walks every row with its sign flipped — the mirror-image check only.
func walk(lines []Line, invert bool) error {
	prev := lines[0].RunningBalance
	for _, ln := range lines[1:] {
		mv := ln.signed()
		if invert {
			mv = mv.Neg()
		}
		want := prev.Add(mv)
		if !want.Equal(ln.RunningBalance) {
			return fmt.Errorf(
				"%w: at row %d the balance should be %s (%s %s %s) but the file says %s, a difference of %s. "+
					"A row is missing, duplicated, or a column is mapped wrongly — the statement is NOT imported",
				ErrBalanceWalk, ln.LineNo, want.StringFixed(2), prev.StringFixed(2),
				plusMinus(ln.DrCr), ln.Amount.StringFixed(2),
				ln.RunningBalance.StringFixed(2), ln.RunningBalance.Sub(want).StringFixed(2))
		}
		prev = ln.RunningBalance
	}
	return nil
}

func plusMinus(drCr string) string {
	if drCr == "D" {
		return "+"
	}
	return "-"
}

// assignOccurrences numbers lines that are identical in every modelled field,
// 0-based, in file order. See migration 0062's column comment: without it a
// second identical transaction would collide with the first on re-import and be
// silently DROPPED, which is an under-import and shows nothing wrong on screen.
func assignOccurrences(lines []Line) {
	seen := map[string]int{}
	for i := range lines {
		h := lines[i].Hash
		lines[i].Occurrence = seen[h]
		seen[h]++
	}
}

// hashLine identifies the TRANSACTION for re-import detection.
//
// The running balance is DELIBERATELY EXCLUDED: the same rows re-exported with
// the balance column switched off must still deduplicate, or an overlapping
// download silently doubles the statement. Two truly identical transactions are
// separated by Occurrence instead.
//
// The unit separator is what makes the join unambiguous — without it,
// ("AB","C") and ("A","BC") hash the same, and a description ending in a digit
// beside an amount is exactly that collision.
func hashLine(l Line) string {
	var b strings.Builder
	for _, part := range []string{
		l.ValueDate.Format("2006-01-02"),
		formatDateOrEmpty(l.TxnDate),
		l.Description,
		l.Amount.StringFixed(4),
		l.DrCr,
		l.BankReference,
		l.ChequeNumber,
	} {
		b.WriteString(part)
		b.WriteByte(0x1f)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

func formatDateOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

// period returns the earliest and latest VALUE date. It does not assume the
// file is sorted: several banks export newest-first, and a period read off the
// first and last rows would then be backwards.
func period(lines []Line) (time.Time, time.Time) {
	from, to := lines[0].ValueDate, lines[0].ValueDate
	for _, l := range lines[1:] {
		if l.ValueDate.Before(from) {
			from = l.ValueDate
		}
		if l.ValueDate.After(to) {
			to = l.ValueDate
		}
	}
	return from, to
}
