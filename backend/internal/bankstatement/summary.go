package bankstatement

// THE BANK'S OWN SUMMARY — what the preamble and footer DECLARE the opening and
// closing balance to be, read by its label and never by its position.
//
// Design memo: docs/design/d123-w5-documents-bank.md §2.3.
//
// WHY A DECLARED FIGURE IS WORTH READING AT ALL
// ---------------------------------------------
// The running-balance walk (B-4) proves every row agrees with the one before
// it. It CANNOT see the two rows at the edges: a statement exported without
// its first or last transaction still walks, because nothing is left to
// disagree with the missing row. The bank's printed opening and closing balance
// are the only evidence in the file that bounds the table from outside, and a
// statement that carries no running balance at all has nothing else.
//
// WHAT MAKES READING THEM SAFE
// ----------------------------
//   - A LABEL THE FILE PRINTS, matched exactly (after normalisation) against a
//     short list. "Balance as on 01-04-2026" is not an opening-balance label;
//     it is ambiguous (as on the START or the END of the day?) and is left
//     alone. A label nobody recognised costs a disclosure; a wrongly recognised
//     one costs a refused-or-accepted import on false evidence.
//   - THE SAME LABEL TWICE WITH TWO VALUES IS REFUSED, not resolved. Two
//     "Opening Balance" figures in one file is a concatenated download or a
//     multi-account statement, and picking one of them is the guess this
//     package does not make.
//   - THE VALUE IS A BALANCE, SO "Dr" MEANS NEGATIVE (parseBalance). An
//     overdrawn account prints "12,500.00 Dr"; read as a plain amount it would
//     be a positive balance and the proof would fail for a correct file — or,
//     worse, pass for a wrong one that happened to net out.

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

// summaryLabel names what a label declares.
type summaryLabel int

const (
	labelOpening summaryLabel = iota
	labelClosing
	labelDebitTotal
	labelCreditTotal
	labelDebitCount
	labelCreditCount
)

// summaryLabels are NORMALISED label texts (letters and digits only, lower
// case). Exact match only: see the header on why a prefix match is not safe.
var summaryLabels = map[string]summaryLabel{
	"openingbalance":        labelOpening,
	"openingbal":            labelOpening,
	"openingbalanceinr":     labelOpening,
	"balancebroughtforward": labelOpening,
	"broughtforward":        labelOpening,
	"balancebf":             labelOpening,

	"closingbalance":        labelClosing,
	"closingbal":            labelClosing,
	"closingbalanceinr":     labelClosing,
	"balancecarriedforward": labelClosing,
	"carriedforward":        labelClosing,
	"balancecf":             labelClosing,

	// The bank's column totals. The words are the BANK's ("Debits" is money
	// OUT of the account), and they are compared against our withdrawals — the
	// inversion is applied at the comparison, in proof.go, not here.
	"debits":           labelDebitTotal,
	"totaldebits":      labelDebitTotal,
	"totalwithdrawals": labelDebitTotal,
	"credits":          labelCreditTotal,
	"totalcredits":     labelCreditTotal,
	"totaldeposits":    labelCreditTotal,

	"drcount":     labelDebitCount,
	"debitcount":  labelDebitCount,
	"crcount":     labelCreditCount,
	"creditcount": labelCreditCount,
}

// Summary is what the file itself declared outside its transaction table.
// Every field is nil when the file did not say it, which is DIFFERENT from the
// file saying zero.
type Summary struct {
	Opening     *decimal.Decimal
	Closing     *decimal.Decimal
	DebitTotal  *decimal.Decimal
	CreditTotal *decimal.Decimal
	DebitCount  *int64
	CreditCount *int64
}

// readSummary scans the rows above and below the table for declared figures.
//
// A value is looked for in three places, in order, and the first that parses
// wins: after a colon in the label's own cell ("Opening Balance : 1,50,000.00");
// in the next non-empty cell to the right on the same row; and in the SAME
// COLUMN of the next row, which is the shape of a summary printed as a small
// table (a row of labels over a row of figures).
func readSummary(rows [][]string) (Summary, error) {
	var s Summary
	type seen struct {
		value decimal.Decimal
		text  string
	}
	found := map[summaryLabel]seen{}

	for ri, row := range rows {
		for ci, c := range row {
			label, inline := splitLabel(c)
			which, ok := summaryLabels[normaliseHeader(label)]
			if !ok {
				continue
			}
			raw, v, ok := summaryValue(rows, ri, ci, inline)
			if !ok {
				continue
			}
			if prev, dup := found[which]; dup && !prev.value.Equal(v) {
				return Summary{}, fmt.Errorf(
					"%w: the file states %q twice with different figures (%s and %s); "+
						"a statement with two of them is two statements, and this package does not choose one",
					ErrDeclaredConflict, strings.TrimSpace(label), prev.text, raw)
			}
			found[which] = seen{value: v, text: raw}
		}
	}

	for which, f := range found {
		v := f.value
		switch which {
		case labelOpening:
			s.Opening = &v
		case labelClosing:
			s.Closing = &v
		case labelDebitTotal:
			s.DebitTotal = &v
		case labelCreditTotal:
			s.CreditTotal = &v
		case labelDebitCount, labelCreditCount:
			// A count must be a whole number. "12.5 debits" is a figure this
			// package misread, and a count it cannot trust is dropped rather
			// than rounded into agreement.
			if !v.IsInteger() {
				continue
			}
			n := v.IntPart()
			if which == labelDebitCount {
				s.DebitCount = &n
			} else {
				s.CreditCount = &n
			}
		}
	}
	return s, nil
}

// splitLabel separates "Opening Balance : 1,00,000.00" into its label and the
// text after the colon.
func splitLabel(c string) (string, string) {
	if i := strings.IndexRune(c, ':'); i >= 0 {
		return c[:i], strings.TrimSpace(c[i+1:])
	}
	return c, ""
}

func summaryValue(rows [][]string, ri, ci int, inline string) (string, decimal.Decimal, bool) {
	if inline != "" {
		if v, err := parseBalance(inline); err == nil {
			return inline, v, true
		}
	}
	row := rows[ri]
	for j := ci + 1; j < len(row); j++ {
		c := strings.TrimSpace(row[j])
		if c == "" {
			continue
		}
		if v, err := parseBalance(c); err == nil {
			return c, v, true
		}
		// The first non-empty cell to the right is THE value or there is none:
		// skipping a non-number to find a number further along is how the
		// account number becomes the opening balance.
		break
	}
	if ri+1 < len(rows) {
		c := cell(rows[ri+1], ci)
		if c != "" {
			if v, err := parseBalance(c); err == nil {
				return c, v, true
			}
		}
	}
	return "", decimal.Zero, false
}

// parseBalance reads a BALANCE cell, where a Dr/Cr suffix is a SIGN.
//
// ⚠️ THIS IS NOT parseAmount AND MUST NOT BE MERGED WITH IT. In an amount cell a
// trailing Cr/Dr restates the direction column and is stripped. In a balance
// cell it is the only thing saying whether the account is in credit or
// overdrawn: "5,000.00 Dr" is MINUS five thousand, in the bank's orientation
// (where our deposit balance is a credit). Slice 2a read every balance with
// parseAmount, so an overdraft account's balances all came out positive and its
// walk failed on a correct file.
func parseBalance(raw string) (decimal.Decimal, error) {
	s := strings.TrimSpace(raw)
	up := strings.ToUpper(strings.NewReplacer(" ", "", ".", "").Replace(s))
	neg := strings.HasSuffix(up, "DR")
	d, err := parseAmount(s)
	if err != nil {
		return decimal.Zero, err
	}
	if neg {
		d = d.Abs().Neg()
	}
	return d, nil
}
