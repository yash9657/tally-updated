package bankstatement

// THE MATCHER (T4 slice 2b) — proposing which voucher leg a bank row IS, and
// refusing to decide when more than one answer fits.
//
// Design memo: docs/design/t4-slice2-model.md §0, §1.2, §4 (B-1, B-7) and §5.
// Tiers are RESEARCH.md:879's own: Exact, Potential, Partial, plus manual.
//
// ===========================================================================
// ⛔ THE RULE THE WHOLE FILE EXISTS TO HONOUR: NOTHING HERE CLEARS ANYTHING
// ===========================================================================
// This file is PURE and it PROPOSES. It writes nothing, it reads no database,
// and there is no code path in this package by which a proposal becomes a
// clearance without a person asking for it by name.
//
// That is stronger than the memo's §0, which forbids auto-clearing an AMBIGUOUS
// match, and the difference is deliberate. The value of a bank reconciliation is
// that a human asserted "this line is that voucher". A matcher that ticks its own
// unambiguous exact matches produces a reconciliation that attests to nothing,
// and it fails in the direction that foots perfectly: every figure is a real
// figure off a real statement, the difference is zero, and the one wrong link is
// invisible until somebody chases a supplier for a payment the books say cleared.
//
// So an EXACT match is still only a proposal. What the product offers instead of
// auto-clearing is AcceptableInBulk — the operator sees how many lines are
// unambiguous and exact, and presses one key to assert all of them. The
// assertion is still theirs; only the typing is saved.
//
// ===========================================================================
// ⛔ AMOUNT AND DATE ARE NOT A KEY, AND THE AMBIGUITY IS TWO-SIDED
// ===========================================================================
// Two identical payments to the same supplier on the same day are ordinary, not
// pathological, and BOTH directions of that collision have to be caught:
//
//   1. ONE LINE, TWO CANDIDATE LEGS — the memo's B-1. The line is AMBIGUOUS; it
//      surfaces both and waits.
//   2. ONE LEG, TWO CANDIDATE LINES — the mirror, and the one that is easy to
//      miss because each line looks perfectly unambiguous ON ITS OWN. Two
//      statement rows of ₹25,000 against a single ₹25,000 payment: each row has
//      exactly one candidate, each would be offered for acceptance, and the
//      second acceptance is refused by the database's one-match-per-clearance
//      index — after the first has already been asserted, wrongly, half the time.
//
// Case 2 is handled by markContestedLegs below, and it is why proposals are
// computed for the whole statement at once rather than a line at a time.

import (
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// MatchTier is the evidence a proposal rests on. The four values are
// RESEARCH.md:879's and they are also the `tier` column's CHECK in migration
// 0088 — one vocabulary, so a value this package can produce is always a value
// the database will store.
type MatchTier string

const (
	// TierExact — amount and direction agree to the paisa AND either the bank's
	// own reference/cheque number is found in the voucher, or the dates are the
	// same day. It is the only tier offered for bulk acceptance.
	TierExact MatchTier = "exact"
	// TierPotential — amount and direction agree to the paisa, but the dates
	// differ and no reference ties them. THE ORDINARY CASE FOR A CHEQUE, which
	// is why it is a tier and not a failure: a cheque written on the 3rd and
	// cleared on the 9th is one transaction, not two.
	TierPotential MatchTier = "potential"
	// TierPartial — the amounts do NOT agree one-for-one: several bank rows make
	// up one voucher, or one bank row nets several. NEVER offered for
	// acceptance in bulk and never sole-candidate: a part payment resolved
	// wrongly leaves both halves plausible.
	TierPartial MatchTier = "partial"
	// TierManual — a person's say-so alone. This package never PRODUCES it; it
	// is what the resolve path stores when the operator names a leg this
	// matcher did not offer at any tier.
	TierManual MatchTier = "manual"
)

// DefaultDateWindowDays is how far apart a bank row and a voucher may be dated
// and still be considered the same transaction.
//
// ⚠️ IT IS A SEARCH WIDTH, NOT A CITED RULE, AND THE DIFFERENCE IS STATED
// RATHER THAN BURIED. HANDOFF-FEATURE-LOCK.md §1.1 forbids inventing a rate, a
// threshold or a clause letter and passing it off as a fact; no authority
// anywhere says how late a cheque may clear, so this build does not pretend one
// does. What makes the number safe to choose is that widening or narrowing it
// CANNOT CLEAR ANYTHING — it only changes how many proposals a human is shown,
// and every proposal still waits for that human. The screen states the window
// it used and lets the operator change it.
//
// Seven days covers a cheque written on a Friday and cleared after a weekend and
// a bank holiday, which is the shape the window exists for.
const DefaultDateWindowDays = 7

// maxPartialCombination bounds the subset search for a part payment.
//
// ⛔ THE BOUND IS DISCLOSED, NOT SILENT. Finding which rows sum to which
// vouchers is subset-sum, and an unbounded search over a year's statement does
// not terminate in a useful time. This build looks at PAIRS only, and
// Proposals.Disclosures says so in the operator's words — because a partial
// matcher that quietly gave up on triples would leave the operator believing
// "no partial match found" meant "there is none", which is a different and
// false statement.
const maxPartialCombination = 2

// StatementRow is one imported bank line as the matcher sees it. It is a
// narrowed Line: the matcher has no business with the running balance, the raw
// row or the import batch.
type StatementRow struct {
	ID          uuid.UUID
	LineNo      int
	ValueDate   time.Time
	Description string
	// Amount is a MAGNITUDE; DrCr carries direction, in OUR orientation — so it
	// is compared DIRECTLY against a posting's dr_cr with no inversion. See
	// migration 0062's column comment on why the parser normalises here.
	Amount        decimal.Decimal
	DrCr          string
	BankReference string
	ChequeNumber  string
}

// BookLeg is one un-cleared, un-matched bank-ledger posting.
//
// ⚠️ THE CALLER FILTERS. A leg that is already cleared (by the manual loop or by
// an earlier match) must never reach this function — memo §5, "already-cleared
// legs are not candidates; no double-clearing". Doing that filtering in SQL
// rather than here keeps this file pure and keeps the filter in the one place
// that can see the bank_reconciliation table.
type BookLeg struct {
	VoucherID   uuid.UUID
	LegNo       int
	VoucherNo   string
	Class       string
	VoucherDate time.Time
	Narration   string
	Party       string
	Amount      decimal.Decimal
	DrCr        string
}

// Candidate is one leg this matcher believes a line might be, with the reason.
type Candidate struct {
	Leg  BookLeg
	Tier MatchTier
	// Why is the evidence, in the operator's words, and it is not decoration:
	// a candidate offered without a reason is a machine asking to be trusted.
	Why string
	// PartialWith names the OTHER lines or legs that make up the sum, when the
	// tier is partial. Empty otherwise.
	PartialWith []string
}

// LineProposal is what the matcher found for one bank row.
type LineProposal struct {
	Line       StatementRow
	Candidates []Candidate
	// BestTier is the strongest tier any candidate reached; "" when there are
	// none at all.
	BestTier MatchTier
	// Ambiguous is TRUE when more than one candidate sits at BestTier, or when
	// a candidate at BestTier is also the best candidate of another line (see
	// markContestedLegs). It is the memo's B-1 and it is a FIRST-CLASS OUTCOME,
	// not a failure: an ambiguous line surfaces every candidate and waits.
	Ambiguous bool
	// ContestedBy names the other statement lines competing for this line's best
	// candidate, when Ambiguous is true for that reason rather than for having
	// several candidates of its own. Without it the screen would say "ambiguous"
	// beside a line showing exactly ONE candidate, which reads as a bug.
	ContestedBy []int
	// AcceptableInBulk is the ONE flag the bulk key reads: exactly one
	// candidate, at TierExact, uncontested. Everything else is an individual
	// human decision.
	AcceptableInBulk bool
}

// Proposals is the whole statement's matching picture.
type Proposals struct {
	DateWindowDays int
	Lines          []LineProposal
	// UnmatchedLegs are book legs no line proposed — "books-only unreconciled"
	// (RESEARCH.md:873). A cheque written and not yet presented is the ordinary
	// member of this list, so it is a category and not an error.
	UnmatchedLegs []BookLeg
	// Disclosures are what this matcher could not do, in an operator's words.
	Disclosures []string
}

// Counts summarises a Proposals for the exception report (Ctrl+J).
type Counts struct {
	Lines            int
	Exact            int
	Potential        int
	Partial          int
	Ambiguous        int
	NoCandidate      int
	AcceptableInBulk int
	UnmatchedLegs    int
}

// Summarise counts the categories the exception report shows.
func (p Proposals) Summarise() Counts {
	c := Counts{Lines: len(p.Lines), UnmatchedLegs: len(p.UnmatchedLegs)}
	for _, lp := range p.Lines {
		if lp.Ambiguous {
			c.Ambiguous++
		}
		if lp.AcceptableInBulk {
			c.AcceptableInBulk++
		}
		switch lp.BestTier {
		case TierExact:
			c.Exact++
		case TierPotential:
			c.Potential++
		case TierPartial:
			c.Partial++
		default:
			c.NoCandidate++
		}
	}
	return c
}

// ProposeMatches is the matcher. It is TOTAL and PURE: same inputs, same
// proposals, no clock, no database, no writes.
//
// windowDays <= 0 means DefaultDateWindowDays.
func ProposeMatches(lines []StatementRow, legs []BookLeg, windowDays int) Proposals {
	if windowDays <= 0 {
		windowDays = DefaultDateWindowDays
	}
	out := Proposals{DateWindowDays: windowDays}

	for _, ln := range lines {
		out.Lines = append(out.Lines, proposeOne(ln, legs, windowDays))
	}

	// The two-sided ambiguity of the file header. MUST run before
	// AcceptableInBulk is trusted by anything.
	markContestedLegs(out.Lines)

	// Partial matching is a SECOND pass, over the lines nothing whole matched.
	// It runs last because a line with an exact candidate is not a part payment,
	// and offering both readings would make the stronger one look uncertain.
	addPartialProposals(out.Lines, legs, windowDays)

	out.UnmatchedLegs = legsNobodyProposed(out.Lines, legs)
	out.Disclosures = disclosures(out)
	return out
}

// proposeOne scores every leg against one line and keeps those that fit whole.
func proposeOne(ln StatementRow, legs []BookLeg, windowDays int) LineProposal {
	lp := LineProposal{Line: ln}

	for _, leg := range legs {
		// ⛔ ZERO TOLERANCE, AND IT IS INVARIANT B-7. An off-by-one-paisa line is
		// NOT this voucher — it is a bank charge, a rounding difference or a
		// different transaction, and every one of those is something the
		// operator must see rather than have absorbed. A tolerance here is how a
		// real error hides for a year, which is the same call
		// BankReconciliation.Difference makes when it refuses to balance
		// "within a rupee".
		if !leg.Amount.Equal(ln.Amount) {
			continue
		}
		// Direction is compared directly: 0062's parser already stored the line
		// in OUR orientation, so a deposit is 'D' on both sides. A line that
		// agreed on amount but not direction is a receipt matched to a payment —
		// which would balance and be entirely wrong.
		if leg.DrCr != ln.DrCr {
			continue
		}

		switch {
		case strongKey(ln, leg) != "":
			lp.Candidates = append(lp.Candidates, Candidate{
				Leg:  leg,
				Tier: TierExact,
				Why: "amount and direction agree to the paisa, and " +
					strongKey(ln, leg) + " appears on the voucher",
			})
		case sameDay(ln.ValueDate, leg.VoucherDate):
			lp.Candidates = append(lp.Candidates, Candidate{
				Leg:  leg,
				Tier: TierExact,
				Why:  "amount, direction and date all agree to the paisa and the day",
			})
		case withinDays(ln.ValueDate, leg.VoucherDate, windowDays):
			lp.Candidates = append(lp.Candidates, Candidate{
				Leg:  leg,
				Tier: TierPotential,
				Why: "amount and direction agree to the paisa; the voucher is dated " +
					leg.VoucherDate.Format("2006-01-02") + " and the bank " +
					ln.ValueDate.Format("2006-01-02") + " — a cheque clearing late is ordinary",
			})
		}
	}

	rankCandidates(&lp)
	return lp
}

// rankCandidates orders a line's candidates strongest-first and settles whether
// the line is ambiguous ON ITS OWN account (the B-1 direction).
func rankCandidates(lp *LineProposal) {
	sort.SliceStable(lp.Candidates, func(i, j int) bool {
		return tierRank(lp.Candidates[i].Tier) < tierRank(lp.Candidates[j].Tier)
	})
	if len(lp.Candidates) == 0 {
		lp.BestTier = ""
		lp.AcceptableInBulk = false
		return
	}
	lp.BestTier = lp.Candidates[0].Tier

	best := 0
	for _, c := range lp.Candidates {
		if c.Tier == lp.BestTier {
			best++
		}
	}
	// ⛔ B-1. Two candidates at the same strength is the ordinary ambiguous case
	// — two suppliers paid ₹25,000 on the 14th — and it is a FIRST-CLASS
	// OUTCOME. It surfaces both and clears neither.
	lp.Ambiguous = best > 1
	lp.AcceptableInBulk = !lp.Ambiguous && lp.BestTier == TierExact
}

// markContestedLegs closes the SECOND direction of ambiguity: one leg that is
// the best candidate of more than one line.
//
// ⛔ EACH OF THOSE LINES LOOKS UNAMBIGUOUS ON ITS OWN, which is exactly why this
// pass exists. Two ₹25,000 bank rows against a single ₹25,000 payment give each
// row one candidate and one tier; without this, both would be offered for bulk
// acceptance, the first would be asserted, and the second would fail on
// migration 0088's one-match-per-clearance index — having already written a
// clearance that is wrong half the time.
func markContestedLegs(lines []LineProposal) {
	type legKey struct {
		voucher uuid.UUID
		leg     int
	}
	claimants := map[legKey][]int{}
	for i, lp := range lines {
		for _, c := range lp.Candidates {
			if c.Tier != lp.BestTier {
				continue
			}
			k := legKey{c.Leg.VoucherID, c.Leg.LegNo}
			claimants[k] = append(claimants[k], i)
		}
	}
	for _, idxs := range claimants {
		if len(idxs) < 2 {
			continue
		}
		for _, i := range idxs {
			lines[i].Ambiguous = true
			lines[i].AcceptableInBulk = false
			for _, other := range idxs {
				if other != i {
					lines[i].ContestedBy = append(lines[i].ContestedBy, lines[other].Line.LineNo)
				}
			}
		}
	}
	for i := range lines {
		sort.Ints(lines[i].ContestedBy)
	}
}

// addPartialProposals looks for the two shapes the memo names: several bank
// rows making up one voucher, and one bank row netting several vouchers.
//
// ⛔ A PARTIAL IS NEVER ACCEPTABLE IN BULK AND NEVER MAKES A LINE UNAMBIGUOUS.
// It is offered as INFORMATION — "these two rows add up to that voucher" — so
// the operator can go and look. Resolving a part payment changes what the
// voucher means, and no arithmetic coincidence is evidence enough for that.
func addPartialProposals(lines []LineProposal, legs []BookLeg, windowDays int) {
	// ⛔ SNAPSHOT FIRST. "Does this line already fit something whole?" must be
	// asked of the state BEFORE this pass, not of the state it is building: the
	// loop below adds partial candidates as it goes, and reading Candidates
	// live would make a line invisible as a PARTNER the moment it acquired a
	// partial of its own. That is not hypothetical — it silently hid the second
	// half of every two-row part payment, so the first row was explained and the
	// second showed "no candidate", which is the worse direction of the same
	// error the pass exists to correct.
	fitsWhole := make([]bool, len(lines))
	for i := range lines {
		fitsWhole[i] = len(lines[i].Candidates) > 0
	}

	for i := range lines {
		// A line something already fits whole is not a part payment.
		if fitsWhole[i] {
			continue
		}
		ln := lines[i].Line

		// SHAPE A: this ONE bank row nets several vouchers.
		for a := 0; a < len(legs); a++ {
			for b := a + 1; b < len(legs); b++ {
				if legs[a].DrCr != ln.DrCr || legs[b].DrCr != ln.DrCr {
					continue
				}
				if !legs[a].Amount.Add(legs[b].Amount).Equal(ln.Amount) {
					continue
				}
				if !withinDays(ln.ValueDate, legs[a].VoucherDate, windowDays) ||
					!withinDays(ln.ValueDate, legs[b].VoucherDate, windowDays) {
					continue
				}
				with := []string{legDescription(legs[a]), legDescription(legs[b])}
				lines[i].Candidates = append(lines[i].Candidates,
					Candidate{
						Leg:  legs[a],
						Tier: TierPartial,
						Why: "this bank row does not match any single voucher, but it is the exact sum of " +
							with[0] + " and " + with[1],
						PartialWith: with,
					},
					Candidate{
						Leg:  legs[b],
						Tier: TierPartial,
						Why: "this bank row does not match any single voucher, but it is the exact sum of " +
							with[0] + " and " + with[1],
						PartialWith: with,
					})
			}
		}

		// SHAPE B: several bank rows make up ONE voucher. Reported against the
		// leg, on each contributing line, so an operator reading either row
		// sees the same explanation.
		for _, leg := range legs {
			if leg.DrCr != ln.DrCr {
				continue
			}
			for j := range lines {
				if j == i || fitsWhole[j] {
					continue
				}
				other := lines[j].Line
				if other.DrCr != ln.DrCr {
					continue
				}
				if !ln.Amount.Add(other.Amount).Equal(leg.Amount) {
					continue
				}
				if !withinDays(ln.ValueDate, leg.VoucherDate, windowDays) {
					continue
				}
				lines[i].Candidates = append(lines[i].Candidates, Candidate{
					Leg:  leg,
					Tier: TierPartial,
					Why: "this bank row is not " + legDescription(leg) +
						" on its own, but together with the row on file line " +
						itoa(other.LineNo) + " it comes to exactly that amount",
					PartialWith: []string{legDescription(leg)},
				})
			}
		}

		if len(lines[i].Candidates) > 0 {
			lines[i].BestTier = TierPartial
			// ⛔ NOT "unambiguous because there is one". A partial is never
			// acceptable in bulk however few of them there are.
			lines[i].AcceptableInBulk = false
			lines[i].Ambiguous = len(lines[i].Candidates) > 1
		}
	}
}

// legsNobodyProposed lists the book legs no line offered at any tier — the
// "books-only unreconciled" category.
func legsNobodyProposed(lines []LineProposal, legs []BookLeg) []BookLeg {
	proposed := map[uuid.UUID]map[int]bool{}
	for _, lp := range lines {
		for _, c := range lp.Candidates {
			if proposed[c.Leg.VoucherID] == nil {
				proposed[c.Leg.VoucherID] = map[int]bool{}
			}
			proposed[c.Leg.VoucherID][c.Leg.LegNo] = true
		}
	}
	var out []BookLeg
	for _, leg := range legs {
		if !proposed[leg.VoucherID][leg.LegNo] {
			out = append(out, leg)
		}
	}
	return out
}

// disclosures states what the matcher did NOT do. Each one is a question an
// operator would otherwise answer wrongly from silence.
func disclosures(p Proposals) []string {
	c := p.Summarise()
	out := []string{
		"NOTHING ON THIS SCREEN HAS BEEN RECONCILED. Every row below is a PROPOSAL; " +
			"a bank date is written only when you accept one, and accepting is the assertion " +
			"that this bank row IS that voucher.",
		"Candidates were sought within " + itoa(p.DateWindowDays) +
			" days either side of the bank's date. That window is this build's search width, " +
			"not a rule from anywhere — widening it only shows you more proposals, because none of them clears itself.",
		"Amounts must agree TO THE PAISA. A row that is one paisa out is not shown as a match to anything, " +
			"deliberately: it is a bank charge or a different transaction, and absorbing it into a tolerance " +
			"is how a real difference hides for a year.",
	}
	if c.Partial > 0 || len(p.Lines) > 0 {
		out = append(out, "Part payments and netted rows were sought over PAIRS ONLY — "+
			"a bank row made up of three or more vouchers is NOT detected and will show as having no candidate. "+
			"\"No match found\" here means \"none found by this search\", not \"none exists\".")
	}
	if c.Ambiguous > 0 {
		out = append(out, itoa(c.Ambiguous)+
			" row(s) fit more than one voucher, or compete with another bank row for the same voucher. "+
			"None of them can be accepted in bulk and none has been chosen for you — "+
			"two payments of the same amount on the same day are ordinary, and picking one is right half the time.")
	}
	return out
}

// ---------------------------------------------------------------------------
// The comparisons themselves.
// ---------------------------------------------------------------------------

// strongKey reports which bank identifier ties this line to this leg, or "".
//
// It is what separates an EXACT match from a POTENTIAL one when the dates
// disagree: a UTR or a cheque number written on the voucher is evidence about
// THIS transaction, where a matching amount on a nearby day is only evidence
// about its shape.
//
// ⚠️ THE MATCH IS ON A WHOLE TOKEN, NEVER A SUBSTRING. Cheque number "123" is a
// substring of "1234" and of the narration "invoice 51236", and a substring
// match would promote a coincidence to the tier that means "certain".
func strongKey(ln StatementRow, leg BookLeg) string {
	hay := tokenSet(leg.Narration + " " + leg.VoucherNo + " " + leg.Party)
	for _, k := range []struct{ label, value string }{
		{"cheque number " + ln.ChequeNumber, ln.ChequeNumber},
		{"bank reference " + ln.BankReference, ln.BankReference},
	} {
		v := strings.TrimSpace(k.value)
		// A one- or two-character "reference" is not evidence; it is a coincidence
		// waiting to happen against any narration containing a short number.
		if len(v) < 3 {
			continue
		}
		if hay[strings.ToUpper(v)] {
			return k.label
		}
	}
	return ""
}

// tokenSet splits on everything that is not a letter or a digit, upper-cased.
func tokenSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, f := range strings.FieldsFunc(strings.ToUpper(s), func(r rune) bool {
		return !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9')
	}) {
		out[f] = true
	}
	return out
}

func sameDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day()
}

// withinDays compares CALENDAR DAYS, not elapsed hours: both sides are dates
// off a statement and a voucher, and a duration comparison would make the answer
// depend on the zero time each was parsed with.
func withinDays(a, b time.Time, days int) bool {
	da := time.Date(a.Year(), a.Month(), a.Day(), 0, 0, 0, 0, time.UTC)
	db := time.Date(b.Year(), b.Month(), b.Day(), 0, 0, 0, 0, time.UTC)
	diff := int(da.Sub(db).Hours() / 24)
	if diff < 0 {
		diff = -diff
	}
	return diff <= days
}

func tierRank(t MatchTier) int {
	switch t {
	case TierExact:
		return 0
	case TierPotential:
		return 1
	case TierPartial:
		return 2
	default:
		return 3
	}
}

func legDescription(l BookLeg) string {
	d := strings.TrimSpace(l.Class + " " + l.VoucherNo)
	if d == "" {
		d = "voucher " + l.VoucherID.String()
	}
	if l.Party != "" {
		d += " (" + l.Party + ")"
	}
	return d
}

// itoa avoids pulling strconv in for two call sites that can never see a
// negative number.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

// TierFor re-derives the tier for ONE (line, leg) pair, which is what the
// resolve path stores.
//
// ⛔ IT EXISTS SO THE SERVER NEVER TAKES A TIER FROM A CLIENT. A tier supplied
// in a request records what the caller CLAIMED the evidence was; stored beside
// a real clearance it would read as evidence while being assertion. A leg this
// matcher does not offer at any tier is TierManual — which is the honest word
// for "a person said so", and is a perfectly good reason to reconcile.
func TierFor(ln StatementRow, leg BookLeg, windowDays int) MatchTier {
	lp := proposeOne(ln, []BookLeg{leg}, windowDays)
	if len(lp.Candidates) == 0 {
		return TierManual
	}
	return lp.Candidates[0].Tier
}
