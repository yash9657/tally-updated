package bankstatement

// PROPOSING A MAPPING — evidence from the file, never a profile from memory.
//
// Design memo: docs/design/t4-slice2-model.md §8.2.
//
// WHAT MAKES THIS LEGITIMATE WHEN A PER-BANK PROFILE TABLE WOULD NOT BE
// ---------------------------------------------------------------------
// A proposal derived from THE HEADER ROW IN FRONT OF US is evidence: the file
// says "Withdrawal Amt." and we read it. A built-in "HDFC layout" would be a
// column order recalled from memory, checkable against no real export in this
// environment, and wrong-by-one it reads the running balance as the amount —
// producing real numbers off a real statement that were never transactions.
//
// So everything here is a SUGGESTION THE OPERATOR CONFIRMS, and the three
// rules that keep it honest are:
//
//  1. A header this file cannot place is REPORTED UNPLACED, never guessed by
//     position. The operator maps it or the import is refused.
//  2. A field with TWO plausible headers is left unproposed and both are named.
//     Picking the first is how "Value Date" and "Txn Date" get swapped.
//  3. THE DATE LAYOUT IS PROPOSED ONLY WHEN THE FILE'S OWN DATES SETTLE IT.
//     03/04/2026 is the 3rd of April or the 4th of March; if every date in the
//     column fits both readings, this package says so and proposes neither.
//     That single ambiguity silently shifts a whole statement by up to eleven
//     months, and every amount in it stays perfectly plausible.

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// synonyms maps a field to header texts that mean it. Matching is
// case-insensitive on the header with punctuation and spaces removed, so
// "Withdrawal Amt." and "withdrawal amt" are one entry.
//
// THIS LIST IS A CONVENIENCE, NOT AN AUTHORITY. An unmatched header costs the
// operator one dropdown; a wrongly matched one costs them their reconciliation,
// which is why nothing here falls back to position.
var synonyms = map[string][]string{
	"value_date":      {"valuedate", "valuedt", "valuedate", "date", "postingdate", "postdate"},
	"txn_date":        {"transactiondate", "txndate", "trandate", "date2"},
	"description":     {"description", "narration", "particulars", "details", "remarks", "transactionremarks"},
	"deposit":         {"deposit", "depositamt", "depositamount", "credit", "creditamt", "creditamount", "cr"},
	"withdrawal":      {"withdrawal", "withdrawalamt", "withdrawalamount", "debit", "debitamt", "debitamount", "dr"},
	"amount":          {"amount", "amt", "transactionamount", "txnamount"},
	"direction":       {"drcr", "crdr", "type", "transactiontype", "debitcredit"},
	"running_balance": {"balance", "closingbalance", "runningbalance", "balanceamt", "availablebalance"},
	"bank_reference":  {"reference", "refno", "referencenumber", "utr", "utrnumber", "transactionid", "refnochequeno"},
	"cheque_number":   {"chequeno", "chequenumber", "chqno", "instrumentno", "instrumentnumber"},
}

// Proposal is what the client shows the operator to confirm.
type Proposal struct {
	// Header is the file's own header row, in order — the list the operator
	// picks from.
	Header []string
	// Mapping is the proposed interpretation. Fields this package would not
	// commit to are EMPTY, and Unresolved says why.
	Mapping ColumnMapping
	// Unresolved names every field left unproposed, with the reason. It is the
	// part the operator must answer; an import attempted with a required field
	// still unresolved is refused by Parse, not silently completed here.
	Unresolved []string
	// Notes are observations that do not block, e.g. a column nothing was
	// mapped to. They are worth showing because an unmapped column is often
	// the one the operator actually wanted.
	Notes []string
}

// normaliseHeader reduces a header cell to its comparison form.
func normaliseHeader(h string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(h) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ProposeMapping reads a CSV's header (and its dates) and suggests how to read
// it. It writes nothing and decides nothing: the operator confirms.
func ProposeMapping(content string) (Proposal, error) {
	header, records, err := readCSV(content)
	if err != nil {
		return Proposal{}, err
	}
	if len(records) == 0 {
		return Proposal{}, fmt.Errorf("%w: it has a header row and nothing under it", ErrEmptyFile)
	}

	p := Proposal{Header: header}
	matched := map[int]string{}    // header position -> field it was matched to
	ambiguous := map[string]bool{} // fields left unproposed because SEVERAL columns fitted

	pick := func(field string) string {
		var hits []string
		var hitPos []int
		for i, h := range header {
			n := normaliseHeader(h)
			for _, s := range synonyms[field] {
				if n == s {
					hits = append(hits, h)
					hitPos = append(hitPos, i)
					break
				}
			}
		}
		switch len(hits) {
		case 0:
			return ""
		case 1:
			matched[hitPos[0]] = field
			return hits[0]
		default:
			// RULE 2: two plausible headers, so neither is chosen. Naming both
			// is the useful part — the operator knows instantly which is which
			// and this package never could.
			//
			// The field is recorded as ambiguous so the "nothing looked like
			// it" message below is not ALSO emitted. Both sentences would be
			// present, one of them false, and an operator reading "no column
			// looked like the value date" under a list of two candidates has
			// been told the tool is broken rather than that it needs an answer.
			ambiguous[field] = true
			p.Unresolved = append(p.Unresolved, fmt.Sprintf(
				"%s: %s could all be it, so none was chosen — pick one",
				field, strings.Join(hits, ", ")))
			return ""
		}
	}

	m := ColumnMapping{}
	m.ValueDate = pick("value_date")
	m.TxnDate = pick("txn_date")
	m.Description = pick("description")
	m.Deposit = pick("deposit")
	m.Withdrawal = pick("withdrawal")
	m.Amount = pick("amount")
	m.Direction = pick("direction")
	m.RunningBalance = pick("running_balance")
	m.BankReference = pick("bank_reference")
	m.ChequeNumber = pick("cheque_number")

	// THE ENCODING FOLLOWS FROM WHICH COLUMNS EXIST, and only when the answer
	// is unambiguous. A file with a deposit column AND a lone amount column has
	// been read wrongly by this function, not by the bank.
	switch {
	case m.Deposit != "" && m.Withdrawal != "" && m.Amount == "":
		m.Encoding = TwoColumns
	case m.Amount != "" && m.Direction != "" && m.Deposit == "" && m.Withdrawal == "":
		m.Encoding = AmountAndFlag
	case m.Amount != "" && m.Direction == "" && m.Deposit == "" && m.Withdrawal == "":
		// A lone amount column MIGHT be signed — but it might equally be a
		// magnitude whose direction lives in a column this list did not
		// recognise. Proposing SignedAmount would turn every withdrawal into a
		// deposit if that guess were wrong, so it is offered as the likely
		// answer and still flagged for confirmation.
		m.Encoding = SignedAmount
		p.Unresolved = append(p.Unresolved,
			"direction: the file has one amount column and no credit/debit column, so it is being read as a SIGNED amount "+
				"(a negative is money out). Confirm that, or map the column that carries the direction.")
	default:
		p.Unresolved = append(p.Unresolved,
			"direction: could not tell how this file says which way money moved — map either a deposit and a withdrawal column, "+
				"or an amount column with a credit/debit column beside it.")
	}

	if m.ValueDate == "" {
		if !ambiguous["value_date"] {
			p.Unresolved = append(p.Unresolved,
				"value_date: no column looked like the value date — map it.")
		}
		// The layout is not proposed either: it is read FROM the value-date
		// column, and there is not yet one to read.
	} else {
		m.DateLayout = proposeDateLayout(header, records, m.ValueDate, &p)
	}
	if m.Description == "" && !ambiguous["description"] {
		p.Unresolved = append(p.Unresolved,
			"description: no column looked like the narration — map it.")
	}

	for i, h := range header {
		if _, ok := matched[i]; !ok && strings.TrimSpace(h) != "" {
			p.Notes = append(p.Notes, fmt.Sprintf("column %q was not mapped to anything", h))
		}
	}
	if m.RunningBalance == "" {
		// Not a refusal — many exports omit it — but the operator should know
		// what they lose, because it is the ONLY check that catches a dropped
		// row (B-4).
		p.Notes = append(p.Notes,
			"no running-balance column was found, so the balance walk cannot run: a row dropped by the export would not be detected.")
	}

	p.Mapping = m
	return p, nil
}

// proposeDateLayout returns the ONE layout that reads every date in the column,
// or "" when the file cannot settle it.
//
// RULE 3, and it is the one worth the most. Two layouts that both parse every
// value are not a tie to break — they are two different statements, up to
// eleven months apart, both of which look completely ordinary.
func proposeDateLayout(header []string, records [][]string, col string, p *Proposal) DateLayout {
	pos := -1
	for i, h := range header {
		if h == col {
			pos = i
			break
		}
	}
	if pos < 0 {
		return ""
	}

	var values []string
	for _, rec := range records {
		if v := cell(rec, pos); v != "" {
			values = append(values, v)
		}
	}
	if len(values) == 0 {
		return ""
	}

	var fits []DateLayout
	for name, layout := range goLayout {
		ok := true
		for _, v := range values {
			if _, err := time.Parse(layout, v); err != nil {
				ok = false
				break
			}
		}
		if ok {
			fits = append(fits, name)
		}
	}
	sort.Slice(fits, func(i, j int) bool { return fits[i] < fits[j] })

	switch len(fits) {
	case 0:
		p.Unresolved = append(p.Unresolved, fmt.Sprintf(
			"date_layout: none of the layouts this build reads (%v) fits every date in %q — the first is %q",
			KnownDateLayouts(), col, values[0]))
		return ""
	case 1:
		return fits[0]
	default:
		p.Unresolved = append(p.Unresolved, fmt.Sprintf(
			"date_layout: %v ALL fit every date in %q, so the file cannot say which it means — "+
				"%q is the same string either way, and choosing wrongly moves the whole statement by up to eleven months. Pick one",
			fits, col, values[0]))
		return ""
	}
}
