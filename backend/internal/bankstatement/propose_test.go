package bankstatement

// Tests for the mapping proposal. Design memo §8.2.
//
// The proposal is a SUGGESTION, so these tests are as much about what it
// REFUSES to suggest as about what it gets right. A proposal that silently
// picks between two readings is worse than no proposal at all: the operator
// confirms a dropdown that was already filled in, and the confirmation step
// this design rests on becomes a rubber stamp.

import (
	"strings"
	"testing"
)

func TestAnOrdinaryHDFCShapedHeaderIsProposedInFull(t *testing.T) {
	// Column names as they appear in the file — nothing here is a built-in
	// profile; the proposal reads this header and nothing else.
	csv := "Value Date,Narration,Chq/Ref No.,Withdrawal Amt.,Deposit Amt.,Closing Balance\n" +
		"01/04/2026,OPENING,,,\"1,00,000.00\",\"2,50,000.00\"\n" +
		"15/04/2026,NEFT ACME,004312,\"25,000.00\",,\"2,25,000.00\"\n"

	p, err := ProposeMapping(csv)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	m := p.Mapping
	if m.Encoding != TwoColumns {
		t.Errorf("a file with deposit and withdrawal columns is a two-column file, got %q", m.Encoding)
	}
	if m.ValueDate != "Value Date" || m.Description != "Narration" {
		t.Errorf("value date/description mis-proposed: %q / %q", m.ValueDate, m.Description)
	}
	if m.Deposit != "Deposit Amt." || m.Withdrawal != "Withdrawal Amt." {
		t.Errorf("deposit/withdrawal mis-proposed: %q / %q", m.Deposit, m.Withdrawal)
	}
	if m.RunningBalance != "Closing Balance" {
		t.Errorf("running balance mis-proposed: %q", m.RunningBalance)
	}
	// The 15th settles it: only dd/mm can read "15/04/2026".
	if m.DateLayout != DMYSlash {
		t.Errorf("a file containing 15/04/2026 can only be dd/mm/yyyy, got %q", m.DateLayout)
	}
	if len(p.Unresolved) != 0 {
		t.Errorf("this header is fully determined; nothing should be unresolved, got %v", p.Unresolved)
	}
	// And the proposal must actually WORK — a suggestion that Parse then
	// refuses would be a dropdown full of wrong answers.
	if _, err := Parse(csv, m); err != nil {
		t.Errorf("the proposed mapping must parse the file it was proposed from: %v", err)
	}
}

// ⚠️ THE ONE WORTH THE MOST. Every date in this file fits dd/mm AND mm/dd, so
// the file cannot say which it means — and choosing wrongly moves the whole
// statement by up to eleven months while every amount stays plausible.
func TestAnAmbiguousDateColumnIsNotGuessed(t *testing.T) {
	csv := "Value Date,Narration,Amount\n" +
		"03/04/2026,ONE,-100.00\n" +
		"05/06/2026,TWO,-200.00\n"

	p, err := ProposeMapping(csv)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if p.Mapping.DateLayout != "" {
		t.Fatalf("03/04/2026 is the 3rd of April OR the 4th of March and this file cannot tell you which; "+
			"proposing %q silently shifts the statement", p.Mapping.DateLayout)
	}
	var found string
	for _, u := range p.Unresolved {
		if strings.HasPrefix(u, "date_layout:") {
			found = u
		}
	}
	if found == "" {
		t.Fatalf("the ambiguity must be REPORTED, not merely left blank; unresolved was %v", p.Unresolved)
	}
	for _, want := range []string{"dd/mm/yyyy", "mm/dd/yyyy", "03/04/2026"} {
		if !strings.Contains(found, want) {
			t.Errorf("the report must name both readings and the value; missing %q in %q", want, found)
		}
	}
}

// The control for the test above: one unambiguous date in the column is enough
// to settle it, and then the layout IS proposed. Without this, a proposal
// function that never proposed a layout at all would pass the ambiguity test.
func TestASingleUnambiguousDateSettlesTheColumn(t *testing.T) {
	csv := "Value Date,Narration,Amount\n" +
		"03/04/2026,ONE,-100.00\n" +
		"25/06/2026,TWO,-200.00\n" // the 25th cannot be a month
	p, err := ProposeMapping(csv)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if p.Mapping.DateLayout != DMYSlash {
		t.Errorf("25/06/2026 rules out mm/dd, so the layout is determined; got %q", p.Mapping.DateLayout)
	}
}

// Two headers that both look like the same field: neither is chosen, and both
// are named. Picking the first is how "Value Date" and "Txn Date" get swapped —
// silently, since both are real dates a few days apart.
func TestTwoPlausibleHeadersForOneFieldAreBothReported(t *testing.T) {
	csv := "Date,Posting Date,Narration,Amount\n" +
		"03/04/2026,04/04/2026,ONE,-100.00\n"
	p, err := ProposeMapping(csv)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if p.Mapping.ValueDate != "" {
		t.Errorf("two columns could be the value date; %q was chosen anyway", p.Mapping.ValueDate)
	}
	var msg string
	for _, u := range p.Unresolved {
		if strings.HasPrefix(u, "value_date:") {
			msg = u
		}
	}
	if !strings.Contains(msg, "Date") || !strings.Contains(msg, "Posting Date") {
		t.Errorf("both candidates must be named so the operator can pick; got %q", msg)
	}
}

// A header nothing recognises is REPORTED UNPLACED, never guessed by position.
func TestAnUnrecognisedHeaderIsReportedNotGuessed(t *testing.T) {
	csv := "Value Date,Narration,Amount,Cost Centre Tag\n" +
		"25/04/2026,ONE,-100.00,MUM\n"
	p, err := ProposeMapping(csv)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	var noted bool
	for _, n := range p.Notes {
		if strings.Contains(n, "Cost Centre Tag") {
			noted = true
		}
	}
	if !noted {
		t.Errorf("an unmapped column must be surfaced — it is often the one the operator wanted; notes were %v", p.Notes)
	}
}

// A file with no running balance is importable, but the operator is told what
// they lose: the balance walk is the ONLY check that catches a dropped row.
func TestAMissingBalanceColumnIsNotedAsALostCheck(t *testing.T) {
	csv := "Value Date,Narration,Amount\n25/04/2026,ONE,-100.00\n"
	p, err := ProposeMapping(csv)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	var noted bool
	for _, n := range p.Notes {
		if strings.Contains(n, "balance walk cannot run") {
			noted = true
		}
	}
	if !noted {
		t.Errorf("the lost check must be stated; notes were %v", p.Notes)
	}
}

// A lone amount column is read as signed — but flagged, because if the
// direction actually lives in a column the synonym list did not recognise, that
// reading turns every withdrawal into a deposit.
func TestALoneAmountColumnIsProposedSignedButFlagged(t *testing.T) {
	csv := "Value Date,Narration,Amount\n25/04/2026,ONE,-100.00\n"
	p, err := ProposeMapping(csv)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if p.Mapping.Encoding != SignedAmount {
		t.Errorf("want SignedAmount, got %q", p.Mapping.Encoding)
	}
	var flagged bool
	for _, u := range p.Unresolved {
		if strings.HasPrefix(u, "direction:") {
			flagged = true
		}
	}
	if !flagged {
		t.Error("the signed reading is a GUESS about direction and must be flagged for confirmation")
	}
}

func TestAHeaderOnlyFileIsRefused(t *testing.T) {
	if _, err := ProposeMapping("Value Date,Narration,Amount\n"); err == nil {
		t.Fatal("a file with no rows cannot be proposed against — there are no dates to read")
	}
}
