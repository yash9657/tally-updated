package gstr

// gstr9_test.go is the PURE half of the GSTR-9 proof: no database, no fixtures,
// no service. Everything here exercises the arithmetic and the honesty rules
// directly on the types, because those are the parts a wrong answer hides in.
//
// ⛔ READ TestGrossIsNotTheAbsoluteValueOfTheNet FIRST. It is the one test in
// this file that protects the report's whole reason for existing, and it is the
// mutation to try when you want to know whether this suite is worth anything.
//
// ⚠️ AND NOTE WHAT EVERY TEST HERE DRIVES. The lesson T11 recorded, at the cost
// of a mutation that left a green suite: a guard tested through a path that
// makes its branch unreachable proves nothing. So each test below calls the
// function whose behaviour it names — divergence(), Validate9(), table4() —
// rather than asserting the property through Generate9, which would need a
// database and would reach several of these guards only by accident.

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

// dec9 is a terse decimal literal for the fixtures below.
func dec9(v string) decimal.Decimal {
	x, err := decimal.NewFromString(v)
	if err != nil {
		panic(err)
	}
	return x
}

// tt9 builds a TaxTotals from three strings, in CGST/SGST/IGST order.
func tt9(c, s, i string) TaxTotals {
	return TaxTotals{CGST: dec9(c), SGST: dec9(s), IGST: dec9(i)}
}

// twelveMonths returns a full year of empty months, so a test can set the one
// or two it cares about without hand-writing twelve literals. The FPs are the
// real ones for FY 2025-26, because Validate9 and the divergence months are
// asserted against them.
func twelveMonths() []Month9 {
	out := make([]Month9, 0, 12)
	for n := 0; n < 12; n++ {
		from, to, fp := monthWindow(2025, n)
		out = append(out, Month9{
			FP: fp, From: from, To: to,
			R1Txval: decimal.Zero, R1Tax: zeroTax(),
			R3BTxval: decimal.Zero, R3BTax: zeroTax(),
			BooksOutwardTax:  zeroTax(),
			R3BITCAvailable:  zeroTax(),
			R3BITCIneligible: zeroTax(),
			BooksITCTax:      zeroTax(),
			HSNTxval:         decimal.Zero,
			HSNTax:           zeroTax(),
			R1MatchesR3B:     true,
			R3BMatchesBooks:  true,
			ITCMatchesBooks:  true,
			HSNFootsToR1:     true,
		})
	}
	return out
}

// pickR1VsR3B is the comparison most of these tests drive.
func pickR1VsR3B(m Month9) (TaxTotals, TaxTotals) { return m.R1Tax, m.R3BTax }

// ---------------------------------------------------------------------------
// ⭐ THE CENTRAL TEST
// ---------------------------------------------------------------------------

// TestGrossIsNotTheAbsoluteValueOfTheNet is the reason gstr9.go exists in the
// shape it does.
//
// THE FIXTURE IS THE WHOLE ARGUMENT: GSTR-1 declares 40,000 of IGST MORE than
// GSTR-3B in May, and 40,000 LESS in September. Nothing else in the year moves.
//
//	Net   = 0        — and a filer shown only this concludes the year is clean
//	Gross = 80,000   — two months are wrong by 40,000 each
//
// ⛔ IF Gross EVER COMES BACK ZERO HERE, the accumulation has been written as
// |sum| instead of sum-of-|term|, the report has lost the only property that
// distinguishes it from adding twelve months up, and every offsetting pair of
// errors in every company's year becomes invisible. That is not a degraded
// report; it is a report that certifies the exact defect it was built to find.
func TestGrossIsNotTheAbsoluteValueOfTheNet(t *testing.T) {
	months := twelveMonths()
	months[1].R1Tax = tt9("0", "0", "40000")  // May 2025: GSTR-1 higher
	months[5].R3BTax = tt9("0", "0", "40000") // September 2025: GSTR-3B higher

	got := divergence("gstr1", "gstr3b", months, pickR1VsR3B)

	if !got.Net.IGST.IsZero() {
		t.Fatalf("net IGST = %s, want 0 — the fixture is built so the two months cancel exactly; "+
			"if they do not, the fixture is wrong and this test proves nothing",
			got.Net.IGST.StringFixed(4))
	}
	if !got.Gross.IGST.Equal(dec9("80000")) {
		t.Fatalf("gross IGST = %s, want 80000. The year contains TWO months wrong by 40,000 each "+
			"and the net is zero: a gross that also reads zero means every offsetting pair of "+
			"errors in a taxpayer's year is invisible, and this report certifies a clean year "+
			"over 80,000 of disagreement", got.Gross.IGST.StringFixed(4))
	}
	if !got.Offsetting {
		t.Fatalf("offsetting = false while gross (%s) and |net| (%s) differ — the flag is what "+
			"tells the screen to print the months instead of the reassuring zero",
			got.Gross.IGST.StringFixed(4), got.Net.IGST.Abs().StringFixed(4))
	}
	if got.Agrees {
		t.Fatalf("agrees = true over a year with two disagreeing months. Agreement is a PER-MONTH " +
			"property; reading it off the annual net is the defect this whole type exists to prevent")
	}
	wantMonths := []string{"052025", "092025"}
	if strings.Join(got.Months, ",") != strings.Join(wantMonths, ",") {
		t.Fatalf("disagreeing months = %v, want %v — the reader cannot go and look at a month "+
			"the report does not name", got.Months, wantMonths)
	}
}

// TestAbsoluteValueIsTakenHeadByHeadAndNotOnTheTotal pins the other half of the
// same idea. A month that is +100 CGST and -100 SGST is TWO errors: the company
// charged the wrong kind of tax, which is a real filing defect with real
// consequences (an intra-state supply reported as inter-state, or the reverse).
// Taking the absolute value of the three-head TOTAL would score it zero.
func TestAbsoluteValueIsTakenHeadByHeadAndNotOnTheTotal(t *testing.T) {
	months := twelveMonths()
	months[0].R1Tax = tt9("100", "0", "0")
	months[0].R3BTax = tt9("0", "100", "0")

	got := divergence("gstr1", "gstr3b", months, pickR1VsR3B)

	if !got.Gross.CGST.Equal(dec9("100")) || !got.Gross.SGST.Equal(dec9("100")) {
		t.Fatalf("gross = CGST %s / SGST %s, want 100 / 100. One month declared 100 of CGST where "+
			"the other return declared 100 of SGST — two heads wrong, not zero. A gross taken on "+
			"the three-head total would call this clean",
			got.Gross.CGST.StringFixed(4), got.Gross.SGST.StringFixed(4))
	}
	if got.Net.Total().IsZero() && got.Agrees {
		t.Fatalf("the three-head net totals zero AND agrees is true — this is exactly the case " +
			"where a net-based verdict certifies a wrong return")
	}
}

// TestDivergenceAgreesOnlyWhenEveryMonthAgrees is the positive control for the
// two tests above: with a genuinely clean year the report must say so, or the
// offsetting machinery would be a detector that fires on everything.
func TestDivergenceAgreesOnlyWhenEveryMonthAgrees(t *testing.T) {
	months := twelveMonths()
	for i := range months {
		months[i].R1Tax = tt9("500", "500", "0")
		months[i].R3BTax = tt9("500", "500", "0")
	}
	got := divergence("gstr1", "gstr3b", months, pickR1VsR3B)
	if !got.Agrees || got.Offsetting || len(got.Months) != 0 {
		t.Fatalf("a year in which all twelve months tie exactly reports agrees=%v offsetting=%v "+
			"months=%v — a reconciliation that never reports agreement is as useless as one that "+
			"always does", got.Agrees, got.Offsetting, got.Months)
	}
	if !got.Gross.Equal(zeroTax()) {
		t.Fatalf("gross = %v over a year with no differences, want zero", got.Gross)
	}
}

// TestASingleDisagreeingMonthIsNotOffsetting separates the two flags. One wrong
// month makes the sources DISAGREE but nothing CANCELS, so `offsetting` must be
// false and the annual net is a truthful summary. Conflating the two would fire
// the offset disclosure on every imperfect year and train readers to ignore it.
func TestASingleDisagreeingMonthIsNotOffsetting(t *testing.T) {
	months := twelveMonths()
	months[3].R1Tax = tt9("0", "0", "1500")

	got := divergence("gstr1", "gstr3b", months, pickR1VsR3B)

	if got.Agrees {
		t.Fatalf("agrees = true with one month out by 1500")
	}
	if got.Offsetting {
		t.Fatalf("offsetting = true with only ONE disagreeing month. Nothing can cancel against a "+
			"single term: gross %s and |net| %s are equal here, and firing the offset disclosure "+
			"on every imperfect year is how a reader learns to ignore it",
			got.Gross.IGST.StringFixed(4), got.Net.IGST.Abs().StringFixed(4))
	}
	if !got.Net.IGST.Equal(dec9("1500")) || !got.Gross.IGST.Equal(dec9("1500")) {
		t.Fatalf("net %s / gross %s, want 1500 / 1500",
			got.Net.IGST.StringFixed(4), got.Gross.IGST.StringFixed(4))
	}
}

// TestTaxableDivergenceCarriesTheSameOffsettingRule. Taxable value is a
// separate type with its own arithmetic, so the property has to be proven on it
// SEPARATELY — proving it on Divergence says nothing about TaxableDivergence,
// which shares no code with it.
func TestTaxableDivergenceCarriesTheSameOffsettingRule(t *testing.T) {
	months := twelveMonths()
	months[2].R1Txval = dec9("250000")
	months[7].R3BTxval = dec9("250000")

	got := taxableDivergence("gstr1", "gstr3b", months)

	if !got.Net.IsZero() {
		t.Fatalf("net = %s, want 0 (the fixture cancels)", got.Net.StringFixed(4))
	}
	if !got.Gross.Equal(dec9("500000")) {
		t.Fatalf("gross = %s, want 500000 — two months out by 2,50,000 each. A taxable-value "+
			"comparison that reports a net alone hides the same thing the tax comparison does",
			got.Gross.StringFixed(4))
	}
	if !got.Offsetting || got.Agrees {
		t.Fatalf("offsetting=%v agrees=%v, want true/false", got.Offsetting, got.Agrees)
	}
}

// ---------------------------------------------------------------------------
// THE FINANCIAL YEAR
// ---------------------------------------------------------------------------

// TestGSTR9RefusesAWindowThatIsNotAFinancialYear. There is no such document as
// "the GSTR-9 for June to August", and producing one hands a filer a
// statutory-looking artifact for a period the law does not recognise.
func TestGSTR9RefusesAWindowThatIsNotAFinancialYear(t *testing.T) {
	for _, c := range []struct{ name, from, to string }{
		{"a calendar year", "2025-01-01", "2025-12-31"},
		{"a quarter", "2025-06-01", "2025-08-31"},
		{"one day short at the end", "2025-04-01", "2026-03-30"},
		{"one day late at the start", "2025-04-02", "2026-03-31"},
		{"the right length, wrong months", "2025-07-01", "2026-06-30"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := financialYear(c.from, c.to)
			if !errors.Is(err, ErrNotAFinancialYear) {
				t.Fatalf("financialYear(%q, %q) err = %v, want ErrNotAFinancialYear. An annual "+
					"return for a period that does not exist is a statutory-looking document "+
					"about nothing", c.from, c.to, err)
			}
			if !strings.Contains(err.Error(), "GSTR-3B") {
				t.Fatalf("the refusal does not say what to read instead: %v. A refusal with no "+
					"alternative sends the caller looking for a setting", err)
			}
		})
	}
}

// TestTheFinancialYearThatIsAcceptedIsTheRightOne is the control: if the check
// above refused everything, both tests would be vacuous.
func TestTheFinancialYearThatIsAcceptedIsTheRightOne(t *testing.T) {
	y, label, err := financialYear("2025-04-01", "2026-03-31")
	if err != nil {
		t.Fatalf("a real financial year was refused: %v", err)
	}
	if y != 2025 || label != "2025-26" {
		t.Fatalf("got year %d label %q, want 2025 / \"2025-26\" — the label is what the filer "+
			"reads at the top of the return", y, label)
	}
}

// TestTheSuggestedYearContainsTheDateTheCallerGave. A caller who asks for
// 2025-09-01 meant FY 2025-26; one who asks for 2026-02-01 meant the SAME year,
// because February falls in the year that STARTED the previous April. Getting
// that backwards sends a filer to the wrong twelve months.
func TestTheSuggestedYearContainsTheDateTheCallerGave(t *testing.T) {
	for _, c := range []struct{ from, wantLabel string }{
		{"2025-09-01", "2025-26"}, // after April: the year that started this April
		{"2026-02-01", "2025-26"}, // before April: the year that started LAST April
		{"2026-04-15", "2026-27"},
	} {
		_, _, err := financialYear(c.from, "2099-01-01")
		if err == nil {
			t.Fatalf("financialYear(%q, ...) unexpectedly succeeded", c.from)
		}
		if !strings.Contains(err.Error(), c.wantLabel) {
			t.Fatalf("asked about %s, the refusal suggests %v — want it to name %s, the financial "+
				"year that CONTAINS the date the caller gave", c.from, err, c.wantLabel)
		}
	}
}

// TestMonthWindowsCoverTheYearExactlyAndHandleFebruary. The month ends are
// computed as "first of next month minus a day" precisely so nobody has to
// remember that 2028 is a leap year; this asserts they got it right.
func TestMonthWindowsCoverTheYearExactlyAndHandleFebruary(t *testing.T) {
	// FY 2027-28 contains February 2028, which has 29 days.
	wantFirst, wantLast := "2027-04-01", "2028-03-31"
	from0, _, fp0 := monthWindow(2027, 0)
	_, to11, fp11 := monthWindow(2027, 11)
	if from0 != wantFirst || to11 != wantLast {
		t.Fatalf("the twelve windows span %s..%s, want %s..%s", from0, to11, wantFirst, wantLast)
	}
	if fp0 != "042027" || fp11 != "032028" {
		t.Fatalf("FPs run %s..%s, want 042027..032028 — the FP is how a reader matches a row "+
			"here against the monthly return they filed", fp0, fp11)
	}
	_, febTo, febFP := monthWindow(2027, 10) // February 2028
	if febTo != "2028-02-29" {
		t.Fatalf("February %s ends %s, want 2028-02-29. 2028 is a leap year and a 28-day window "+
			"silently drops every voucher dated the 29th out of the annual return", febFP, febTo)
	}
}

// ---------------------------------------------------------------------------
// THE TABLES AND THEIR HONESTY RULES
// ---------------------------------------------------------------------------

// TestTable4SuppressesItsTotalAndSaysWhy. 4N totals rows this build cannot
// produce, so it is not emitted. The suppression must carry a reason, or the
// screen prints a blank where a statutory figure belongs — which a reader reads
// as zero.
func TestTable4SuppressesItsTotalAndSaysWhy(t *testing.T) {
	got := table4(tt9("1000", "1000", "0"), dec9("50000"))
	if !got.TotalSuppressed {
		t.Fatalf("table 4 emits a total. 4N sums rows 4A to 4L and five of them are NOT DERIVED " +
			"here — a total over a set with known-missing members is an UNDERSTATEMENT THAT FOOTS, " +
			"which is the most dangerous shape a figure on a tax return can have")
	}
	if strings.TrimSpace(got.TotalSuppressedReason) == "" {
		t.Fatalf("the total is suppressed with nothing printed in its place")
	}
	if got.Status != TablePartial {
		t.Fatalf("table 4 status = %q, want %q", got.Status, TablePartial)
	}
	if len(got.Omitted) == 0 {
		t.Fatalf("table 4 is PARTIAL but names nothing omitted — PARTIAL without a list of what " +
			"is missing tells the reader LESS than DERIVED would")
	}
	for _, o := range got.Omitted {
		if strings.TrimSpace(o.Reason) == "" {
			t.Fatalf("omitted row %s (%s) carries no reason", o.Label, o.Title)
		}
	}
	// The derived row must still be there: a table that suppresses its total AND
	// its rows is just NOT_DERIVED wearing a different label.
	if len(got.Rows) == 0 {
		t.Fatalf("table 4 is PARTIAL with no derived rows at all")
	}
}

// TestEveryUndrivedTableNamesItsMissingInput. This is the rule that keeps the
// report honest at scale: eleven of the fourteen tables cannot be produced, and
// eleven blank tables would be indistinguishable from eleven empty ones.
func TestEveryUndrivedTableNamesItsMissingInput(t *testing.T) {
	g := GSTR9{Months: twelveMonths()}
	tables := buildTables9(g)
	if len(tables) == 0 {
		t.Fatalf("no tables were built")
	}
	seenNotDerived := 0
	for _, tb := range tables {
		switch tb.Status {
		case TableDerived:
			// A derived table needs no reason, and must not carry a lonely one.
		case TablePartial, TableNotDerived:
			if strings.TrimSpace(tb.Reason) == "" {
				t.Fatalf("table %s (%s) is %s with no reason. A blank space where a statutory "+
					"figure belongs is read as zero", tb.No, tb.Title, tb.Status)
			}
			if tb.Status == TableNotDerived {
				seenNotDerived++
				if len(tb.Rows) != 0 {
					t.Fatalf("table %s is NOT_DERIVED but carries %d rows. NOT_DERIVED means NO "+
						"rows — a zero row here says the taxpayer had nothing to report, which "+
						"is a much stronger claim than 'this build cannot tell'",
						tb.No, len(tb.Rows))
				}
			}
		default:
			t.Fatalf("table %s has unknown status %q", tb.No, tb.Status)
		}
	}
	if seenNotDerived == 0 {
		t.Fatalf("no table reported NOT_DERIVED. This build cannot produce tables 5, 7, 8, 9, " +
			"10-14, 15, 16, 18 or 19, and a GSTR-9 that claims otherwise is the smoothing the " +
			"PLAN row forbids")
	}
}

// TestTable7IsNotDerivedRatherThanZero is called out on its own because it is
// the single most dangerous cell in this report. Table 7 is "ITC reversed and
// ineligible ITC". Every 3B behind this year ran WITHOUT an inward statement, so
// nothing was reversed — and a zero there does not mean "nothing was reversed",
// it means "nothing was checked". Those are different claims and only one of
// them is true.
func TestTable7IsNotDerivedRatherThanZero(t *testing.T) {
	tables := buildTables9(GSTR9{Months: twelveMonths()})
	var t7 *Table9
	for i := range tables {
		if tables[i].No == "7" {
			t7 = &tables[i]
		}
	}
	if t7 == nil {
		t.Fatalf("table 7 is absent from the list entirely. A table that is simply MISSING reads " +
			"as 'nothing to report', which is the smoothing this report exists not to do")
	}
	if t7.Status != TableNotDerived {
		t.Fatalf("table 7 status = %q, want NOT_DERIVED", t7.Status)
	}
	if len(t7.Rows) != 0 {
		t.Fatalf("table 7 carries %d rows. A zero in 'ITC reversed' asserts the taxpayer reversed "+
			"NOTHING; what is true is that nothing was checked", len(t7.Rows))
	}
	if !strings.Contains(t7.Reason, "inward statement") {
		t.Fatalf("table 7's reason does not name the missing input: %q", t7.Reason)
	}
}

// TestTable17IsDerivedFromTheYearsHSNBuckets. Table 17 is the one fully derived
// table, and it must actually roll up rather than be a stub — including a
// bucket with no HSN code, which is real taxable value that must not vanish.
func TestTable17IsDerivedFromTheYearsHSNBuckets(t *testing.T) {
	months := twelveMonths()
	months[0].HSN = []HSNItem{
		{HsnSc: "8471", Rt: dec9("18"), Txval: dec9("10000"), Camt: dec9("900"), Samt: dec9("900")},
		{HsnSc: "", Rt: dec9("18"), Txval: dec9("5000"), Camt: dec9("450"), Samt: dec9("450")},
	}
	months[6].HSN = []HSNItem{
		{HsnSc: "8471", Rt: dec9("18"), Txval: dec9("20000"), Camt: dec9("1800"), Samt: dec9("1800")},
	}

	var t17 Table9
	for _, tb := range buildTables9(GSTR9{Months: months}) {
		if tb.No == "17" {
			t17 = tb
		}
	}
	if t17.Status != TableDerived {
		t.Fatalf("table 17 status = %q, want DERIVED — the per-line HSN detail is present and is "+
			"reconciled against the GSTR-1 sections by HSNVsR1", t17.Status)
	}
	if len(t17.Rows) != 2 {
		t.Fatalf("table 17 has %d rows, want 2 — one per (hsn, rate) bucket across the year, so "+
			"8471 in April and 8471 in October must MERGE and the blank-code bucket must survive "+
			"on its own", len(t17.Rows))
	}
	var merged, blank *Row9
	for i := range t17.Rows {
		if t17.Rows[i].Label == "8471" {
			merged = &t17.Rows[i]
		}
		if strings.Contains(t17.Rows[i].Label, "no HSN") {
			blank = &t17.Rows[i]
		}
	}
	if merged == nil || !merged.Txval.Equal(dec9("30000")) {
		t.Fatalf("the 8471 bucket did not merge across months: %+v", merged)
	}
	if blank == nil {
		t.Fatalf("the blank-HSN bucket was DROPPED. It carries 5,000 of real taxable value; " +
			"dropping it silently shrinks the table until it no longer foots to the return")
	}
	if !blank.Txval.Equal(dec9("5000")) {
		t.Fatalf("blank-HSN bucket txval = %s, want 5000", blank.Txval.StringFixed(4))
	}
}

// ---------------------------------------------------------------------------
// DISCLOSURES
// ---------------------------------------------------------------------------

// TestTheUnconditionalDisclosuresAreAlwaysPresent pins the set as a SET, so
// adding or losing one has to be a deliberate edit. This is the shape
// handler_gstr3b_test.go's seven-token test takes, and for the same reason: a
// frontend switches on these strings, and a token that silently stops being
// emitted is a limitation that stops being shown.
func TestTheUnconditionalDisclosuresAreAlwaysPresent(t *testing.T) {
	// A perfectly reconciled year — the case most likely to drop tokens, because
	// nothing is wrong with it.
	g := GSTR9{Months: twelveMonths()}
	g.OutwardR1VsR3B = divergence("a", "b", g.Months, pickR1VsR3B)
	g.OutwardR3BVsBooks = g.OutwardR1VsR3B
	g.OutwardR1VsBooks = g.OutwardR1VsR3B
	g.ITCVsBooks = g.OutwardR1VsR3B
	g.HSNVsR1 = g.OutwardR1VsR3B
	g.TaxableR1VsR3B = taxableDivergence("a", "b", g.Months)

	got := disclosures9(g)
	// In emitted (sorted-by-token) order, so a mismatch is a real difference and
	// not an ordering artefact.
	want := []string{
		Disc9AdvancesNotModelled,      // advances-not-modelled
		Disc9AmendmentsNotModelled,    // amendments-not-modelled
		Disc9TableStructureUnverified, // annual-table-structure-unverified
		Disc9BooksCarryNoTaxableValue, // books-carry-no-taxable-value
		Disc9ExemptNotMarked,          // exempt-and-zero-rated-not-marked
		Disc9ReversalsNotComputed,     // itc-reversals-not-computed
		Disc9NoAnnual2A,               // no-annual-2a
		Disc9NoCashLedger,             // no-cash-or-credit-ledger
		Disc9NotFilingReady,           // not-filing-ready
		Disc9PortalSchemaUnverified,   // portal-schema-unverified
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("a fully reconciled year discloses\n  %v\nwant\n  %v\nThese ten are "+
			"UNCONDITIONAL. not-filing-ready above all: this build has never seen the portal "+
			"schema and cannot claim a filing-ready annual return", got, want)
	}
	// The two conditional tokens must be ABSENT here, or they mean nothing when
	// they appear.
	for _, absent := range []string{Disc9MonthsOffset, Disc9ReturnsDisagree} {
		for _, g := range got {
			if g == absent {
				t.Fatalf("%q was disclosed on a year where every month ties. A conditional "+
					"disclosure that fires unconditionally is noise", absent)
			}
		}
	}
}

// TestTheConditionalDisclosuresFireOnlyWhenTheyShould is the other half.
func TestTheConditionalDisclosuresFireOnlyWhenTheyShould(t *testing.T) {
	months := twelveMonths()
	months[1].R1Tax = tt9("0", "0", "40000")
	months[5].R3BTax = tt9("0", "0", "40000")

	g := GSTR9{Months: months}
	g.OutwardR1VsR3B = divergence("gstr1", "gstr3b", months, pickR1VsR3B)
	g.OutwardR3BVsBooks = divergence("gstr3b", "books", months,
		func(m Month9) (TaxTotals, TaxTotals) { return m.R3BTax, m.BooksOutwardTax })
	g.OutwardR1VsBooks = divergence("gstr1", "books", months,
		func(m Month9) (TaxTotals, TaxTotals) { return m.R1Tax, m.BooksOutwardTax })
	g.ITCVsBooks = divergence("itc", "books", months,
		func(m Month9) (TaxTotals, TaxTotals) { return m.R3BITCAvailable, m.BooksITCTax })
	g.HSNVsR1 = divergence("hsn", "r1", months,
		func(m Month9) (TaxTotals, TaxTotals) { return m.HSNTax, m.R1Tax })
	g.TaxableR1VsR3B = taxableDivergence("gstr1", "gstr3b", months)

	got := strings.Join(disclosures9(g), ",")
	for _, want := range []string{Disc9MonthsOffset, Disc9ReturnsDisagree, Disc9NotFilingReady} {
		if !strings.Contains(got, want) {
			t.Fatalf("a year whose May and September disagreements CANCEL did not disclose %q. "+
				"That is the one case where the annual net reads clean and is not: got %s",
				want, got)
		}
	}
}

// TestEveryEmittedDisclosureIsInTheClosedVocabulary. A token the frontend does
// not know is silently invisible, which is the exact failure the closed
// vocabulary exists to prevent.
func TestEveryEmittedDisclosureIsInTheClosedVocabulary(t *testing.T) {
	known := map[string]bool{}
	for _, d := range KnownDisclosures9 {
		if known[d] {
			t.Fatalf("KnownDisclosures9 lists %q twice", d)
		}
		known[d] = true
	}
	months := twelveMonths()
	months[0].R1Tax = tt9("1", "0", "0")
	g := GSTR9{Months: months}
	g.OutwardR1VsR3B = divergence("a", "b", months, pickR1VsR3B)
	g.TaxableR1VsR3B = taxableDivergence("a", "b", months)
	for _, d := range disclosures9(g) {
		if !known[d] {
			t.Fatalf("disclosure %q is emitted but is not in KnownDisclosures9. A frontend "+
				"switches on these strings, so an unrecognised one is silently invisible", d)
		}
	}
}

// ---------------------------------------------------------------------------
// VALIDATION
// ---------------------------------------------------------------------------

// validGSTR9 builds a report that Validate9 must accept, so every rejection
// test below differs from it in exactly one way.
func validGSTR9() GSTR9 {
	g := GSTR9{FY: "2025-26", From: "2025-04-01", To: "2026-03-31", Months: twelveMonths()}
	g.OutwardR1VsR3B = divergence("gstr1", "gstr3b", g.Months, pickR1VsR3B)
	g.OutwardR3BVsBooks = g.OutwardR1VsR3B
	g.OutwardR1VsBooks = g.OutwardR1VsR3B
	g.ITCVsBooks = g.OutwardR1VsR3B
	g.HSNVsR1 = g.OutwardR1VsR3B
	g.TaxableR1VsR3B = taxableDivergence("gstr1", "gstr3b", g.Months)
	g.Tables = buildTables9(g)
	g.Disclosures = disclosures9(g)
	return g
}

// TestValidate9AcceptsAWellFormedReturn is the control. Without it every
// rejection test below could pass against a Validate9 that refuses everything.
func TestValidate9AcceptsAWellFormedReturn(t *testing.T) {
	if err := Validate9(validGSTR9()); err != nil {
		t.Fatalf("Validate9 refused a well-formed return: %v", err)
	}
}

func TestValidate9RefusesAYearThatIsNotTwelveMonths(t *testing.T) {
	g := validGSTR9()
	g.Months = g.Months[:11]
	err := Validate9(g)
	if !errors.Is(err, ErrInvalid9) {
		t.Fatalf("an eleven-month year was accepted (err = %v). A month missing from the grid is "+
			"a month NOBODY CHECKED, and it is invisible in a report whose totals still foot", err)
	}
}

func TestValidate9RefusesASuppressedTotalWithNoReason(t *testing.T) {
	g := validGSTR9()
	for i := range g.Tables {
		if g.Tables[i].TotalSuppressed {
			g.Tables[i].TotalSuppressedReason = ""
		}
	}
	if err := Validate9(g); !errors.Is(err, ErrInvalid9) {
		t.Fatalf("a suppressed total with no reason was accepted (err = %v). The screen then "+
			"prints a blank where a statutory figure belongs, and a blank is read as zero", err)
	}
}

func TestValidate9RefusesANotDerivedTableThatCarriesRows(t *testing.T) {
	g := validGSTR9()
	for i := range g.Tables {
		if g.Tables[i].Status == TableNotDerived {
			g.Tables[i].Rows = []Row9{{Label: "7A", Title: "reversed", Tax: zeroTax()}}
			break
		}
	}
	if err := Validate9(g); !errors.Is(err, ErrInvalid9) {
		t.Fatalf("a NOT_DERIVED table carrying a zero row was accepted (err = %v). That row says "+
			"the taxpayer reported nothing, when the truth is that nothing was computed", err)
	}
}

func TestValidate9RefusesAnUnknownDisclosure(t *testing.T) {
	g := validGSTR9()
	g.Disclosures = append(g.Disclosures, "everything-is-fine")
	if err := Validate9(g); !errors.Is(err, ErrInvalid9) {
		t.Fatalf("an off-vocabulary disclosure was accepted (err = %v). The frontend switches on "+
			"these strings and would render nothing at all for it", err)
	}
}

// TestValidate9RefusesANetLargerThanItsGross catches the arithmetic being wired
// backwards. Gross is a sum of absolute values of the same monthly terms, so it
// can never be smaller than |net| — and a report that claims otherwise has had
// one of the two accumulations replaced.
func TestValidate9RefusesANetLargerThanItsGross(t *testing.T) {
	g := validGSTR9()
	g.OutwardR1VsR3B.Net = tt9("0", "0", "5000")
	g.OutwardR1VsR3B.Gross = tt9("0", "0", "10")
	if err := Validate9(g); !errors.Is(err, ErrInvalid9) {
		t.Fatalf("a net of 5000 against a gross of 10 was accepted (err = %v) — arithmetically "+
			"impossible, and the shape a swapped accumulation takes", err)
	}
}

// TestValidate9RefusesAgreementReadOffTheAnnualNet is the guard against the one
// mistake a later maintainer is most likely to make: simplifying `Agrees` to
// `Net.IsZero()`. That is the question a net cannot answer.
func TestValidate9RefusesAgreementReadOffTheAnnualNet(t *testing.T) {
	g := validGSTR9()
	g.OutwardR1VsR3B.Months = []string{"052025", "092025"}
	g.OutwardR1VsR3B.Agrees = true // as `Net.IsZero()` would have it
	g.OutwardR1VsR3B.Gross = tt9("0", "0", "80000")
	g.OutwardR1VsR3B.Offsetting = true
	if err := Validate9(g); !errors.Is(err, ErrInvalid9) {
		t.Fatalf("a divergence naming two disagreeing months while claiming agreement was "+
			"accepted (err = %v). This is exactly what `Agrees = Net.IsZero()` produces on an "+
			"offsetting year, and it certifies a wrong return as clean", err)
	}
}

// TestValidate9RefusesAnOffsettingFlagThatContradictsTheFigures. The flag is
// what the screen switches on; a flag that disagrees with its own numbers is
// worse than no flag, because the reader trusts it.
func TestValidate9RefusesAnOffsettingFlagThatContradictsTheFigures(t *testing.T) {
	g := validGSTR9()
	g.ITCVsBooks.Offsetting = true // net and gross are both zero: nothing offsets
	if err := Validate9(g); !errors.Is(err, ErrInvalid9) {
		t.Fatalf("offsetting=true was accepted on a divergence whose gross equals its |net| "+
			"(err = %v)", err)
	}
}

// ---------------------------------------------------------------------------
// THE LEDGER REFUSAL
// ---------------------------------------------------------------------------

// TestGSTR9RefusesWithoutTheLedgerIdsAndNamesThemAll. Without them
// PostingTaxTotals returns zero for every head, so every month appears to
// disagree with the returns by exactly the tax charged — a wrong number that
// looks like a finding, which is worse than an error.
func TestGSTR9RefusesWithoutTheLedgerIdsAndNamesThemAll(t *testing.T) {
	err := requireLedgers(Options3B{})
	if !errors.Is(err, ErrLedgersRequired) {
		t.Fatalf("a GSTR-9 with no ledger ids was allowed through: %v", err)
	}
	for _, want := range []string{
		"cgst_ledger_id", "sgst_ledger_id", "igst_ledger_id",
		"input_cgst_ledger_id", "input_sgst_ledger_id", "input_igst_ledger_id",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not name %s: %v. Naming one per round trip makes six "+
				"round trips out of one mistake", want, err)
		}
	}
}

// TestKnownDisclosures9IsSortedByToken. disclosures9 emits a sorted list, and
// several tests compare against KnownDisclosures9 or a hand-written slice; a
// vocabulary in a different order turns every one of those into a failure with
// no real difference in it, which is how a suite gets its assertions loosened.
func TestKnownDisclosures9IsSortedByToken(t *testing.T) {
	for i := 1; i < len(KnownDisclosures9); i++ {
		if KnownDisclosures9[i-1] >= KnownDisclosures9[i] {
			t.Fatalf("KnownDisclosures9 is not sorted: %q comes before %q",
				KnownDisclosures9[i-1], KnownDisclosures9[i])
		}
	}
}

// TestOperatorMessageFlattensThePackagePrefixStack is the D93-class guard on
// the annual return's failure sentence.
//
// The error a failing monthly 3B produced reached the operator as
//
//	gstr: GSTR-9 2026-27, period 072026: gstr3b: gstr: generated GSTR-3B
//	violates a validation rule: table 3.2 taxable 0.0000 exceeds 3.1(a) ...
//
// — three package prefixes in front of the only clause that says anything the
// reader can act on. This asserts the trimming keeps the statutory words and
// drops the Go ones.
func TestOperatorMessageFlattensThePackagePrefixStack(t *testing.T) {
	// Built the way month9 builds it, from the real sentinel.
	inner := fmt.Errorf("%w: table 3.1(a) taxable value is -12000.0000", ErrInvalid3B)
	stacked := fmt.Errorf("gstr3b: %w", inner)

	got := operatorMessage(stacked)

	for _, bad := range []string{"gstr3b: ", "gstr: ", "gstr1: "} {
		if strings.HasPrefix(got, bad) {
			t.Fatalf("a package prefix survived: %q", got)
		}
	}
	// The statutory clause and the figure must BOTH survive — trimming that
	// ate the message would be the same defect facing the other way.
	if !strings.HasPrefix(got, "generated GSTR-3B violates a validation rule") {
		t.Fatalf("the statutory clause must lead the sentence, got: %q", got)
	}
	if !strings.Contains(got, "-12000.0000") {
		t.Fatalf("the offending figure must survive, got: %q", got)
	}
}

// TestOperatorMessageLeavesADescriptiveWrapAlone is the CONTROL: month9 also
// wraps with "output-tax postings" and "input-tax postings", which name a real
// thing rather than a package, and must be untouched.
func TestOperatorMessageLeavesADescriptiveWrapAlone(t *testing.T) {
	err := fmt.Errorf("output-tax postings: %w", errors.New("ledger 'Output CGST' not found"))
	if got := operatorMessage(err); got != err.Error() {
		t.Fatalf("a descriptive wrap must survive verbatim.\n got: %q\nwant: %q", got, err.Error())
	}
}
