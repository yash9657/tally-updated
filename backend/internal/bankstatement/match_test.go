package bankstatement

// Tests for the matcher. Every one is phrased so the CORRECT behaviour is the
// PASS, and the three that pin invariants say which mutation must break them.

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal {
	v, err := decimal.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return v
}

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func row(no int, date, amount, drCr string) StatementRow {
	return StatementRow{
		ID: uuid.New(), LineNo: no, ValueDate: day(date),
		Description: "TEST ROW", Amount: d(amount), DrCr: drCr,
	}
}

func leg(no, date, amount, drCr string) BookLeg {
	return BookLeg{
		VoucherID: uuid.New(), LegNo: 0, VoucherNo: no, Class: "Payment",
		VoucherDate: day(date), Amount: d(amount), DrCr: drCr,
	}
}

// ===========================================================================
// B-1 — THE INVARIANT THE WHOLE TRACK EXISTS FOR
// ===========================================================================

// TestTwoVouchersOfTheSameAmountOnTheSameDayAreAmbiguousAndNeitherIsChosen is
// invariant B-1.
//
// ⛔ MUTATION: make rankCandidates set AcceptableInBulk without consulting
// Ambiguous, or make it keep only Candidates[0]. This test must fail.
func TestTwoVouchersOfTheSameAmountOnTheSameDayAreAmbiguousAndNeitherIsChosen(t *testing.T) {
	line := row(1, "2026-05-14", "25000.0000", "C")
	legs := []BookLeg{
		leg("101", "2026-05-14", "25000.0000", "C"),
		leg("102", "2026-05-14", "25000.0000", "C"),
	}

	got := ProposeMatches([]StatementRow{line}, legs, 0)
	lp := got.Lines[0]

	if !lp.Ambiguous {
		t.Error("two vouchers of the same amount on the same day must be AMBIGUOUS; " +
			"picking one is right half the time and the half it gets wrong is invisible")
	}
	if lp.AcceptableInBulk {
		t.Error("an ambiguous line must never be acceptable in bulk")
	}
	// It surfaces BOTH — the memo's "ambiguity is a first-class outcome, it has
	// its own status, it surfaces both candidates and waits for a human".
	if len(lp.Candidates) != 2 {
		t.Errorf("candidates = %d, want both surfaced", len(lp.Candidates))
	}
}

// TestTheMirrorAmbiguityIsCaughtTooWhenTwoBankRowsWantOneVoucher is the
// direction that is easy to miss: each line has exactly ONE candidate and looks
// perfectly unambiguous on its own.
//
// ⛔ MUTATION: delete the markContestedLegs call from ProposeMatches. This test
// must fail. (TestTwoVouchers... above will NOT catch it — that is the point.)
func TestTheMirrorAmbiguityIsCaughtTooWhenTwoBankRowsWantOneVoucher(t *testing.T) {
	lines := []StatementRow{
		row(1, "2026-05-14", "25000.0000", "C"),
		row(2, "2026-05-14", "25000.0000", "C"),
	}
	legs := []BookLeg{leg("101", "2026-05-14", "25000.0000", "C")}

	got := ProposeMatches(lines, legs, 0)

	for i, lp := range got.Lines {
		if len(lp.Candidates) != 1 {
			t.Fatalf("line %d: candidates = %d, want 1 (the setup is what makes this test meaningful)",
				i, len(lp.Candidates))
		}
		if !lp.Ambiguous {
			t.Errorf("line %d: one leg claimed by two bank rows must make BOTH ambiguous; "+
				"accepting the first asserts a clearance that is wrong half the time", i)
		}
		if lp.AcceptableInBulk {
			t.Errorf("line %d: a contested leg must never be acceptable in bulk", i)
		}
		if len(lp.ContestedBy) == 0 {
			t.Errorf("line %d: must name the competing line, or the screen says "+
				"\"ambiguous\" beside a single candidate and reads as a bug", i)
		}
	}
}

// ===========================================================================
// B-7 — ZERO TOLERANCE
// ===========================================================================

// TestAPaisaOutIsNotAMatchAtAnyTier is invariant B-7.
//
// ⛔ MUTATION: compare amounts with a 1-paisa tolerance. This test must fail.
func TestAPaisaOutIsNotAMatchAtAnyTier(t *testing.T) {
	line := row(1, "2026-05-14", "25000.0000", "C")
	legs := []BookLeg{leg("101", "2026-05-14", "25000.0100", "C")}

	got := ProposeMatches([]StatementRow{line}, legs, 0)

	if len(got.Lines[0].Candidates) != 0 {
		t.Errorf("a one-paisa difference produced %d candidate(s); it must produce none — "+
			"it is a bank charge or a different transaction, and a tolerance is how a real "+
			"difference hides for a year", len(got.Lines[0].Candidates))
	}
	if got.Lines[0].BestTier != "" {
		t.Errorf("BestTier = %q, want empty", got.Lines[0].BestTier)
	}
}

// TestADepositIsNeverMatchedToAPayment pins the direction comparison. A line
// and a leg agreeing on amount but not side is a receipt matched to a payment,
// which would balance and be entirely wrong.
func TestADepositIsNeverMatchedToAPayment(t *testing.T) {
	line := row(1, "2026-05-14", "25000.0000", "D")
	legs := []BookLeg{leg("101", "2026-05-14", "25000.0000", "C")}

	if got := ProposeMatches([]StatementRow{line}, legs, 0); len(got.Lines[0].Candidates) != 0 {
		t.Errorf("a deposit matched a withdrawal: %d candidate(s)", len(got.Lines[0].Candidates))
	}
}

// ===========================================================================
// THE TIERS
// ===========================================================================

// TestSameDayAndSameAmountIsExactAndAcceptableInBulk is the ordinary happy path
// — and it asserts that even an EXACT match is only ACCEPTABLE, never accepted.
func TestSameDayAndSameAmountIsExactAndAcceptableInBulk(t *testing.T) {
	line := row(1, "2026-05-14", "25000.0000", "C")
	legs := []BookLeg{leg("101", "2026-05-14", "25000.0000", "C")}

	lp := ProposeMatches([]StatementRow{line}, legs, 0).Lines[0]
	if lp.BestTier != TierExact {
		t.Errorf("BestTier = %q, want exact", lp.BestTier)
	}
	if !lp.AcceptableInBulk {
		t.Error("an unambiguous exact match must be offered for bulk acceptance")
	}
	if lp.Candidates[0].Why == "" {
		t.Error("a candidate offered without a reason is a machine asking to be trusted")
	}
}

// TestADateTwoDaysOutIsPotentialAndNotExact is the memo's edge-case ledger:
// "Amount matches, date off by two days -> POTENTIAL, not exact — cheques clear
// late; that is the normal case."
func TestADateTwoDaysOutIsPotentialAndNotExact(t *testing.T) {
	line := row(1, "2026-05-16", "25000.0000", "C")
	legs := []BookLeg{leg("101", "2026-05-14", "25000.0000", "C")}

	lp := ProposeMatches([]StatementRow{line}, legs, 0).Lines[0]
	if lp.BestTier != TierPotential {
		t.Errorf("BestTier = %q, want potential", lp.BestTier)
	}
	// ⛔ AND IT IS NOT OFFERED FOR BULK ACCEPTANCE. The date disagreeing is
	// exactly the thing a human should look at.
	if lp.AcceptableInBulk {
		t.Error("a potential match must not be acceptable in bulk — the dates disagree, " +
			"which is the fact a human is being asked to weigh")
	}
}

// TestAChequeNumberOnTheVoucherMakesItExactDespiteTheDate — a strong key is
// evidence about THIS transaction, where a nearby amount is evidence only about
// its shape.
func TestAChequeNumberOnTheVoucherMakesItExactDespiteTheDate(t *testing.T) {
	line := row(1, "2026-05-20", "25000.0000", "C")
	line.ChequeNumber = "483921"
	l := leg("101", "2026-05-14", "25000.0000", "C")
	l.Narration = "Paid by cheque 483921 to Acme"

	lp := ProposeMatches([]StatementRow{line}, []BookLeg{l}, 0).Lines[0]
	if lp.BestTier != TierExact {
		t.Errorf("BestTier = %q, want exact — the cheque number ties the two", lp.BestTier)
	}
}

// TestAChequeNumberMatchesAWholeTokenAndNeverASubstring. "1234" is a substring
// of "512345", and a substring match would promote a coincidence to the tier
// that means "certain".
func TestAChequeNumberMatchesAWholeTokenAndNeverASubstring(t *testing.T) {
	line := row(1, "2026-05-20", "25000.0000", "C")
	line.ChequeNumber = "1234"
	l := leg("101", "2026-05-14", "25000.0000", "C")
	l.Narration = "against invoice 512345 and 12345"

	lp := ProposeMatches([]StatementRow{line}, []BookLeg{l}, 0).Lines[0]
	if lp.BestTier == TierExact {
		t.Error("a cheque number found only as a SUBSTRING was promoted to exact")
	}
	if lp.BestTier != TierPotential {
		t.Errorf("BestTier = %q, want potential (amount agrees, date is 6 days out)", lp.BestTier)
	}
}

// TestAVeryShortReferenceIsNotEvidence — a one- or two-character "reference"
// collides with any narration carrying a short number.
func TestAVeryShortReferenceIsNotEvidence(t *testing.T) {
	line := row(1, "2026-05-20", "25000.0000", "C")
	line.BankReference = "12"
	l := leg("101", "2026-05-14", "25000.0000", "C")
	l.Narration = "bill 12 of April"

	if lp := ProposeMatches([]StatementRow{line}, []BookLeg{l}, 0).Lines[0]; lp.BestTier == TierExact {
		t.Error("a two-character reference was treated as a strong key")
	}
}

// TestOutsideTheWindowThereIsNoCandidateAtAll — the window is a search width,
// and beyond it the matcher says nothing rather than guessing.
func TestOutsideTheWindowThereIsNoCandidateAtAll(t *testing.T) {
	line := row(1, "2026-06-30", "25000.0000", "C")
	legs := []BookLeg{leg("101", "2026-05-14", "25000.0000", "C")}

	got := ProposeMatches([]StatementRow{line}, legs, 0)
	if len(got.Lines[0].Candidates) != 0 {
		t.Error("a voucher 47 days away was offered as a candidate")
	}
	// And the leg is reported as books-only unreconciled rather than vanishing.
	if len(got.UnmatchedLegs) != 1 {
		t.Errorf("UnmatchedLegs = %d, want 1 — a cheque written and not presented "+
			"is a real category, not an error", len(got.UnmatchedLegs))
	}
}

// TestTheWindowIsAParameterAndWideningItFindsThePotentialMatch proves the
// window is genuinely a search width rather than a constant.
func TestTheWindowIsAParameterAndWideningItFindsThePotentialMatch(t *testing.T) {
	line := row(1, "2026-06-01", "25000.0000", "C")
	legs := []BookLeg{leg("101", "2026-05-14", "25000.0000", "C")}

	if got := ProposeMatches([]StatementRow{line}, legs, 7); len(got.Lines[0].Candidates) != 0 {
		t.Error("18 days apart matched inside a 7-day window")
	}
	got := ProposeMatches([]StatementRow{line}, legs, 30)
	if len(got.Lines[0].Candidates) != 1 {
		t.Fatal("18 days apart did not match inside a 30-day window")
	}
	// ⛔ AND WIDENING THE WINDOW STILL CLEARS NOTHING. That is what makes the
	// number safe to choose at all.
	if got.Lines[0].AcceptableInBulk {
		t.Error("widening the window promoted a potential match to bulk-acceptable")
	}
	if got.DateWindowDays != 30 {
		t.Errorf("DateWindowDays = %d, want the window actually used", got.DateWindowDays)
	}
}

// ===========================================================================
// PARTIAL
// ===========================================================================

// TestOneBankRowNettingTwoVouchersIsPartialAndNeverAcceptable is the memo's
// "Two vouchers, one statement line (netted) -> PARTIAL; never auto-cleared".
func TestOneBankRowNettingTwoVouchersIsPartialAndNeverAcceptable(t *testing.T) {
	line := row(1, "2026-05-14", "30000.0000", "C")
	legs := []BookLeg{
		leg("101", "2026-05-14", "10000.0000", "C"),
		leg("102", "2026-05-13", "20000.0000", "C"),
	}

	lp := ProposeMatches([]StatementRow{line}, legs, 0).Lines[0]
	if lp.BestTier != TierPartial {
		t.Fatalf("BestTier = %q, want partial", lp.BestTier)
	}
	if lp.AcceptableInBulk {
		t.Error("a partial match must NEVER be acceptable in bulk — resolving a part " +
			"payment changes what the voucher means, and an arithmetic coincidence is not evidence enough")
	}
	if len(lp.Candidates[0].PartialWith) == 0 {
		t.Error("a partial candidate must name what makes up the sum")
	}
}

// TestTwoBankRowsMakingUpOneVoucherIsPartial is the other shape: "One voucher,
// two statement lines (part payment) -> PARTIAL tier; never auto-cleared".
func TestTwoBankRowsMakingUpOneVoucherIsPartial(t *testing.T) {
	lines := []StatementRow{
		row(1, "2026-05-14", "10000.0000", "C"),
		row(2, "2026-05-15", "20000.0000", "C"),
	}
	legs := []BookLeg{leg("101", "2026-05-14", "30000.0000", "C")}

	got := ProposeMatches(lines, legs, 0)
	for i, lp := range got.Lines {
		if lp.BestTier != TierPartial {
			t.Errorf("line %d: BestTier = %q, want partial", i, lp.BestTier)
		}
		if lp.AcceptableInBulk {
			t.Errorf("line %d: partial offered for bulk acceptance", i)
		}
	}
}

// TestAPartialIsNotOfferedWhenSomethingFitsWhole — a line one voucher matches
// exactly is not a part payment, and offering both readings would make the
// stronger one look uncertain.
func TestAPartialIsNotOfferedWhenSomethingFitsWhole(t *testing.T) {
	line := row(1, "2026-05-14", "30000.0000", "C")
	legs := []BookLeg{
		leg("101", "2026-05-14", "30000.0000", "C"),
		leg("102", "2026-05-14", "10000.0000", "C"),
		leg("103", "2026-05-14", "20000.0000", "C"),
	}

	lp := ProposeMatches([]StatementRow{line}, legs, 0).Lines[0]
	if lp.BestTier != TierExact {
		t.Errorf("BestTier = %q, want exact", lp.BestTier)
	}
	for _, c := range lp.Candidates {
		if c.Tier == TierPartial {
			t.Error("a partial reading was offered beside an exact one")
		}
	}
}

// TestTheThreeWaySplitIsNotDetectedAndTheDisclosureSaysSo. The bound is real
// and it is DISCLOSED — "no match found" must not read as "none exists".
func TestTheThreeWaySplitIsNotDetectedAndTheDisclosureSaysSo(t *testing.T) {
	line := row(1, "2026-05-14", "60000.0000", "C")
	legs := []BookLeg{
		leg("101", "2026-05-14", "10000.0000", "C"),
		leg("102", "2026-05-14", "20000.0000", "C"),
		leg("103", "2026-05-14", "30000.0000", "C"),
	}

	got := ProposeMatches([]StatementRow{line}, legs, 0)
	if len(got.Lines[0].Candidates) != 0 {
		t.Fatal("a three-way split was detected; this test encodes the CURRENT bound " +
			"and should be updated deliberately if the search widens")
	}
	if !hasDisclosure(got.Disclosures, "PAIRS ONLY") {
		t.Error("the pair-only bound must be disclosed: a silent cap reads as \"covered everything\"")
	}
}

// ===========================================================================
// THE DISCLOSURES AND THE SUMMARY
// ===========================================================================

// TestEveryProposalSetSaysNothingHasBeenReconciled. The screen's most
// dangerous possible reading is that these rows are done.
func TestEveryProposalSetSaysNothingHasBeenReconciled(t *testing.T) {
	got := ProposeMatches([]StatementRow{row(1, "2026-05-14", "1.0000", "C")}, nil, 0)
	if !hasDisclosure(got.Disclosures, "NOTHING ON THIS SCREEN HAS BEEN RECONCILED") {
		t.Error("a proposal set must state that it is a proposal set")
	}
	if !hasDisclosure(got.Disclosures, "TO THE PAISA") {
		t.Error("zero tolerance must be stated, not merely implemented")
	}
}

// TestTierForNeverInventsATierForALegTheMatcherWouldNotOffer is what stops the
// server storing a client's claim about its own evidence.
func TestTierForNeverInventsATierForALegTheMatcherWouldNotOffer(t *testing.T) {
	line := row(1, "2026-05-14", "25000.0000", "C")
	// A leg of a different amount: the matcher offers it at no tier.
	other := leg("999", "2026-05-14", "999.0000", "C")

	if got := TierFor(line, other, 0); got != TierManual {
		t.Errorf("TierFor = %q, want manual — a leg the matcher does not offer was "+
			"resolved anyway, and the stored tier must say it was a person's say-so", got)
	}
	same := leg("101", "2026-05-14", "25000.0000", "C")
	if got := TierFor(line, same, 0); got != TierExact {
		t.Errorf("TierFor = %q, want exact", got)
	}
}

// TestSummariseCountsTheCategoriesTheExceptionReportShows.
func TestSummariseCountsTheCategoriesTheExceptionReportShows(t *testing.T) {
	lines := []StatementRow{
		row(1, "2026-05-14", "100.0000", "C"), // exact, uncontested
		row(2, "2026-05-14", "777.0000", "C"), // nothing
	}
	legs := []BookLeg{leg("101", "2026-05-14", "100.0000", "C")}

	c := ProposeMatches(lines, legs, 0).Summarise()
	if c.Lines != 2 || c.Exact != 1 || c.NoCandidate != 1 || c.AcceptableInBulk != 1 {
		t.Errorf("Counts = %+v", c)
	}
}

func hasDisclosure(ds []string, needle string) bool {
	for _, s := range ds {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
