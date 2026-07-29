package payroll

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

// Unit tests for the s.392 annual-base engine.
//
// Every assertion below states the CONSEQUENCE of being wrong, because a test
// whose failure message is "got 4333, want 4334" tells a reader nothing about
// whether to trust the number or the test. Money is decimal throughout — there
// is not a float64 in this file.

func itYear() TaxYear { return TaxYear{StartYear: 2026} }

func itRuleSet(t *testing.T) IncomeTaxRuleSet {
	t.Helper()
	rs, err := IncomeTaxFor(itYear())
	if err != nil {
		t.Fatalf("IncomeTaxFor(2026-27): %v", err)
	}
	return rs
}

func itDec(t *testing.T, s string) decimal.Decimal {
	t.Helper()
	v, err := decimal.NewFromString(s)
	if err != nil {
		t.Fatalf("%q is not a decimal: %v", s, err)
	}
	return v
}

// itBase is a resident, PAN-on-record, new-regime employee at the start of the
// year with nothing behind them. Individual tests move ONE field, so a failure
// names the thing that moved.
func itBase(t *testing.T, annualSalary string) IncomeTaxInput {
	t.Helper()
	return IncomeTaxInput{
		TaxYear:                   itYear(),
		Regime:                    RegimeNew,
		IsResident:                true,
		AgeBand:                   AgeBelow60,
		PANAvailable:              true,
		CurrentMonthTaxable:       decimal.Zero,
		ProjectedRemainingTaxable: itDec(t, annualSalary),
		MonthsRemainingInclusive:  12,
	}
}

func itWant(t *testing.T, label string, got decimal.Decimal, want, consequence string) {
	t.Helper()
	w := itDec(t, want)
	if !got.Equal(w) {
		t.Errorf("%s: got %s, want %s. %s", label, got.StringFixed(4), w.StringFixed(4), consequence)
	}
}

// TestIncomeTax_RebateCliffMarginalRelief is the test the mutation proof aims
// at.
//
// s.156 (ex-87A) gives a full rebate up to a total income of 12,00,000 and its
// proviso caps the tax just above the cliff at the amount by which the income
// EXCEEDS 12,00,000. Without the relief the employee at 12,50,000 pays tax on
// 67,500 instead of on 50,000 — over-deducted by 17,500 of tax plus cess, every
// year, and the error is invisible because 67,500 is a perfectly plausible
// number.
//
// The three points pinned here are the cliff itself, the band where the relief
// bites, and the point where it stops biting. A test that pinned only the middle
// one would pass an implementation that applied the relief to everybody.
func TestIncomeTax_RebateCliffMarginalRelief(t *testing.T) {
	rs := itRuleSet(t)

	cases := []struct {
		name       string
		salary     string // gross salary; standard deduction of 75,000 comes off it
		wantIncome string
		wantBefore string
		wantRebate string
		wantRelief string
		wantAnnual string
	}{
		{
			// Exactly AT the limit: the rebate wipes the tax out entirely and no
			// marginal relief arises. 12,00,000 -> tax 60,000 -> rebate 60,000.
			name: "at the limit the rebate wipes the tax", salary: "1275000",
			wantIncome: "1200000", wantBefore: "60000", wantRebate: "60000",
			wantRelief: "0", wantAnnual: "0",
		},
		{
			// Just OVER: 12,00,010 -> tax 60,001.50, excess 10, relief 59,991.50,
			// tax 10, cess 0.40, annual 10. Without the relief the annual tax would
			// be 62,401 — a 6,240x difference on a ten-rupee raise.
			name: "one slab step over the cliff", salary: "1275010",
			wantIncome: "1200010", wantBefore: "60001.5", wantRebate: "0",
			wantRelief: "59991.5", wantAnnual: "10",
		},
		{
			// The corpus case: 12,50,000 -> tax 67,500, excess 50,000, relief
			// 17,500, cess 2,000, annual 52,000.
			name: "in the middle of the relief band", salary: "1325000",
			wantIncome: "1250000", wantBefore: "67500", wantRebate: "0",
			wantRelief: "17500", wantAnnual: "52000",
		},
		{
			// 12,70,580: tax = 60,000 + 70,580*0.15 = 70,587, excess = 70,580.
			// 70,587 > 70,580, so relief of 7 is still due. This is one slab step
			// below the break-even of 12,70,588.24 and it is here to pin that the
			// relief tapers to nothing rather than switching off early.
			name: "one step before the relief runs out", salary: "1345580",
			wantIncome: "1270580", wantBefore: "70587", wantRebate: "0",
			// Cess is on the tax AFTER relief (70,580), not on 70,587:
			// 70,580 + 2,823.20 = 73,403.20 -> 73,403.
			wantRelief: "7", wantAnnual: "73403",
		},
		{
			// 12,70,600: tax = 60,000 + 70,600*0.15 = 70,590, excess = 70,600.
			// 70,590 < 70,600, so NO relief. An implementation that kept applying
			// it would under-deduct from here upwards, for ever.
			name: "past the break-even no relief is due", salary: "1345600",
			wantIncome: "1270600", wantBefore: "70590", wantRebate: "0",
			wantRelief: "0", wantAnnual: "73414",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := IncomeTaxDerive(itBase(t, tc.salary), rs)
			if err != nil {
				t.Fatalf("IncomeTaxDerive: %v", err)
			}
			itWant(t, "estimatedAnnualTaxable", got.EstimatedAnnualTaxable, tc.wantIncome,
				"total income rounds to the nearest TEN before the slabs are walked")
			itWant(t, "taxBeforeRebate", got.TaxBeforeRebate, tc.wantBefore, "the slab walk is wrong")
			itWant(t, "rebate", got.Rebate, tc.wantRebate, "the s.156 rebate is wrong")
			itWant(t, "rebateMarginalRelief", got.RebateMarginalRelief, tc.wantRelief,
				"WITHOUT the s.156 rebate marginal relief every employee between 12,00,000 and about "+
					"12,70,589 of total income is OVER-TAXED — that is a short salary in twelve payslips")
			itWant(t, "annualTax", got.AnnualTax, tc.wantAnnual,
				"this is the tax that will be spread over the months remaining and taken out of a salary")
		})
	}

	// The relief must NEVER make the tax negative or exceed the tax itself.
	over, err := IncomeTaxDerive(itBase(t, "1325000"), rs)
	if err != nil {
		t.Fatalf("IncomeTaxDerive: %v", err)
	}
	if over.RebateMarginalRelief.GreaterThan(over.TaxBeforeRebate) {
		t.Errorf("relief %s exceeds the tax %s; a relief larger than the tax is a refund the employer "+
			"has no power to give", over.RebateMarginalRelief, over.TaxBeforeRebate)
	}
}

// TestIncomeTax_SurchargeMarginalReliefAtEachBand pins the classic relief at
// EVERY threshold, not just the first.
//
// The trap at the second and later bands is the rate used AT the threshold: at
// exactly 1 crore the surcharge is 10% (the PREVIOUS band), because surcharge
// bites only where income EXCEEDS the threshold. Using the current band's 15%
// there overstates the cap and hands out relief that is not due; using zero
// understates it and denies relief that is.
func TestIncomeTax_SurchargeMarginalReliefAtEachBand(t *testing.T) {
	rs := itRuleSet(t)

	for _, tc := range []struct {
		name        string
		salary      string
		wantIncome  string
		wantSur     string
		wantRelief  string
		reliefIsDue bool
	}{
		// Just over 50 lakh: gross 51,25,000 -> income 50,50,000. Tax 10,95,000,
		// surcharge at 10% = 1,09,500. Tax at 50,00,000 = 10,80,000 with NIL
		// surcharge, so the cap is 10,80,000 + 50,000 = 11,30,000 and the relief
		// is 12,04,500 - 11,30,000 = 74,500.
		{"fifty lakh", "5125000", "5050000", "109500", "74500", true},
		// Just over 1 crore: gross 1,00,85,000 -> income 1,00,10,000. Tax =
		// 2,80,000 + (1,00,10,000-24,00,000)*0.30 = 22,83,000 -> 25,63,000... the
		// derivation is in the assertion below; what matters is that relief IS due
		// and that it is computed against a 10% surcharge at the threshold.
		{"one crore", "10085000", "10010000", "", "", true},
		// Just over 2 crore.
		{"two crore", "20075000", "20000000", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := IncomeTaxDerive(itBase(t, tc.salary), rs)
			if err != nil {
				t.Fatalf("IncomeTaxDerive: %v", err)
			}
			itWant(t, "estimatedAnnualTaxable", got.EstimatedAnnualTaxable, tc.wantIncome, "income rounding")
			if tc.wantSur != "" {
				itWant(t, "surcharge", got.Surcharge, tc.wantSur, "the wrong surcharge band was selected")
			}
			if tc.wantRelief != "" {
				itWant(t, "surchargeMarginalRelief", got.SurchargeMarginalRelief, tc.wantRelief,
					"without marginal relief an employee just over the threshold keeps LESS than one just under it")
			}
			if tc.reliefIsDue && !got.SurchargeMarginalRelief.IsPositive() {
				t.Errorf("no marginal relief at %s: an income %s just past the threshold must not attract "+
					"more additional tax than the additional income", tc.name, got.EstimatedAnnualTaxable)
			}
			if !tc.reliefIsDue && got.SurchargeMarginalRelief.IsPositive() {
				t.Errorf("relief of %s granted at exactly the threshold, where the surcharge has not yet "+
					"begun — that is relief against a liability that does not exist", got.SurchargeMarginalRelief)
			}
		})
	}

	// THE PROPERTY the relief exists to guarantee, asserted directly rather than
	// through a hand-computed number: crossing a threshold must never leave the
	// employee with LESS take-home than staying just below it. This is what
	// catches a relief computed against the wrong threshold rate — a bug that
	// produces a plausible number at every individual income.
	for _, threshold := range []string{"5000000", "10000000", "20000000"} {
		below := itDec(t, threshold)
		above := below.Add(itDec(t, "10"))
		lo, err := IncomeTaxDerive(itAtIncome(t, below), rs)
		if err != nil {
			t.Fatalf("derive at %s: %v", below, err)
		}
		hi, err := IncomeTaxDerive(itAtIncome(t, above), rs)
		if err != nil {
			t.Fatalf("derive at %s: %v", above, err)
		}
		extraIncome := above.Sub(below)
		extraTax := hi.AnnualTax.Sub(lo.AnnualTax)
		if extraTax.GreaterThan(extraIncome) {
			t.Errorf("crossing the %s threshold costs %s of tax on %s of extra income. That is the exact "+
				"cliff marginal relief exists to remove, and an employee would be worse off for the raise",
				threshold, extraTax, extraIncome)
		}
	}
}

// itAtIncome builds an input whose TOTAL INCOME (after the standard deduction)
// is exactly the figure given.
func itAtIncome(t *testing.T, totalIncome decimal.Decimal) IncomeTaxInput {
	t.Helper()
	in := itBase(t, "0")
	in.ProjectedRemainingTaxable = totalIncome.Add(itDec(t, "75000"))
	return in
}

// TestIncomeTax_RoundingOrderIsIncomeThenTax pins BOTH statutory roundings and
// the order they happen in.
//
// s.516 carries forward two rules with DIFFERENT UNITS: total income to the
// nearest TEN, the tax finally payable to the nearest RUPEE. Applying the rupee
// rounding to income (the natural half-implementation) changes the slab
// arithmetic for anyone whose income does not already end in a zero.
func TestIncomeTax_RoundingOrderIsIncomeThenTax(t *testing.T) {
	rs := itRuleSet(t)

	// Total income 15,00,006 raw -> 15,00,010 after s.516. The 15% band then
	// applies to 3,00,010 rather than 3,00,006.
	in := itBase(t, "1575006")
	in.MonthsRemainingInclusive = 1
	in.IsFinalMonth = true
	got, err := IncomeTaxDerive(in, rs)
	if err != nil {
		t.Fatalf("IncomeTaxDerive: %v", err)
	}
	itWant(t, "estimatedAnnualTaxable", got.EstimatedAnnualTaxable, "1500010",
		"total income must round to the nearest TEN (s.516, ex-288A), not to the rupee")
	itWant(t, "taxBeforeRebate", got.TaxBeforeRebate, "105001.5",
		"the slabs are walked on the ROUNDED income; the un-rounded 15,00,006 would give 1,05,000.90")
	itWant(t, "cess", got.Cess, "4200.06",
		"cess is computed BEFORE the tax is rounded to the rupee, on the exact figure")
	itWant(t, "annualTax", got.AnnualTax, "109202",
		"the tax rounds to the nearest RUPEE (s.516, ex-288B) once, at the end — 1,09,201.56 -> 1,09,202")

	// The intermediate must NOT be pre-rounded: a paise-carrying tax before cess
	// is the evidence that no rounding happened between the slabs and the cess.
	if got.TaxBeforeRebate.Equal(got.TaxBeforeRebate.Truncate(0)) {
		t.Error("taxBeforeRebate has no fractional part; this fixture was chosen so that it does, and its " +
			"absence means a rounding step has been inserted between the slabs and the cess")
	}

	// And the counter-case: rounding income to the RUPEE would give a different
	// annual tax. Assert the two really differ, so the fixture is not proving a
	// distinction without a difference.
	rupeeRounded := itBase(t, "1575000")
	rupeeRounded.ProjectedRemainingTaxable = itDec(t, "1575006")
	alt, err := IncomeTaxDerive(rupeeRounded, rs)
	if err != nil {
		t.Fatalf("IncomeTaxDerive: %v", err)
	}
	if alt.EstimatedAnnualTaxable.Equal(itDec(t, "1500006")) {
		t.Error("income was rounded to the rupee, not to the nearest ten")
	}
}

// TestIncomeTax_MidYearJoinerAggregatesPriorEmployer pins ex-s.192(2) /
// Form 12B: the new employer deducts on the AGGREGATE of both employments.
//
// This is the single most common payroll-engine defect. Computing on the
// part-year salary alone puts the employee in a lower slab, deducts a fraction
// of what is due, and leaves them with a demand plus interest at assessment.
func TestIncomeTax_MidYearJoinerAggregatesPriorEmployer(t *testing.T) {
	rs := itRuleSet(t)

	joiner := IncomeTaxInput{
		TaxYear:                   itYear(),
		Regime:                    RegimeOld,
		IsResident:                true,
		AgeBand:                   AgeBelow60,
		PANAvailable:              true,
		PriorEmployerTaxable:      itDec(t, "900000"),
		PriorEmployerTDS:          itDec(t, "45000"),
		CurrentMonthTaxable:       itDec(t, "180000"),
		ProjectedRemainingTaxable: itDec(t, "900000"),
		MonthsRemainingInclusive:  6,
		ChapterVIA:                itDec(t, "150000"),
	}
	with, err := IncomeTaxDerive(joiner, rs)
	if err != nil {
		t.Fatalf("with prior employer: %v", err)
	}
	itWant(t, "estimatedAnnualTaxable", with.EstimatedAnnualTaxable, "1780000",
		"19,80,000 of aggregate salary less 50,000 standard deduction less 1,50,000 Chapter VI-A")
	itWant(t, "creditedAlready", with.CreditedAlready, "45000",
		"the former employer's TDS is credited, so the balance is not deducted twice")
	itWant(t, "tdsThisMonth", with.TDSThisMonth, "52560",
		"the balance of 3,15,360 spread over the six months from October to March")

	// The same employee with the prior-employer particulars dropped. This is not
	// a hypothetical: it is what an engine that ignores Form 12B computes.
	ignored := joiner
	ignored.PriorEmployerTaxable = decimal.Zero
	without, err := IncomeTaxDerive(ignored, rs)
	if err != nil {
		t.Fatalf("without prior employer: %v", err)
	}
	if without.TDSThisMonth.Equal(with.TDSThisMonth) {
		t.Fatal("dropping the former employer's salary did not change the deduction — this test is not " +
			"exercising the aggregation at all and would pass an engine that ignored Form 12B")
	}
	shortfall := with.TDSThisMonth.Sub(without.TDSThisMonth).Mul(itDec(t, "6"))
	if !shortfall.IsPositive() {
		t.Fatalf("ignoring the former employer must UNDER-deduct, not over-deduct; shortfall %s", shortfall)
	}
	t.Logf("ignoring Form 12B under-deducts %s over the six remaining months", shortfall.StringFixed(0))
}

// TestIncomeTax_FinalMonthDeductsWholeBalance pins the leaver / March case: the
// remaining balance goes in one deduction, with NO division.
//
// Dividing by the nominal one month would give the same answer by accident. The
// distinction matters for the case where the balance is not divisible: an engine
// that always divided would leave a residue behind in March, and the year's
// deductions would not add up to the year's tax on Form 138.
func TestIncomeTax_FinalMonthDeductsWholeBalance(t *testing.T) {
	rs := itRuleSet(t)

	in := itBase(t, "1325000")
	in.ProjectedRemainingTaxable = decimal.Zero
	in.YTDTaxableThisEmployer = itDec(t, "1214000")
	in.CurrentMonthTaxable = itDec(t, "111000")
	in.YTDTDSThisEmployer = itDec(t, "47667")
	in.MonthsRemainingInclusive = 1
	in.IsFinalMonth = true

	got, err := IncomeTaxDerive(in, rs)
	if err != nil {
		t.Fatalf("IncomeTaxDerive: %v", err)
	}
	itWant(t, "annualTax", got.AnnualTax, "52000", "the year's tax on a total income of 12,50,000")
	itWant(t, "tdsThisMonth", got.TDSThisMonth, "4333",
		"the WHOLE remaining balance (52,000 - 47,667) must be deducted in the final month, or the year's "+
			"deductions will not add up to the year's tax on Form 138 and TRACES will not reconcile")

	// Over-deducted so far: the employer has no power under s.392 to refund tax
	// already paid to the credit of the Central Government, so the deduction is
	// zero and never negative.
	over := in
	over.YTDTDSThisEmployer = itDec(t, "60000")
	res, err := IncomeTaxDerive(over, rs)
	if err != nil {
		t.Fatalf("over-deducted: %v", err)
	}
	if !res.TDSThisMonth.IsZero() {
		t.Errorf("an over-deducted employee's final month must deduct zero, got %s. A negative deduction "+
			"would be the employer refunding tax it has already remitted", res.TDSThisMonth)
	}
}

// TestIncomeTax_ArrearsRaiseTheAnnualEstimateNotAMonthlyThreshold pins the
// receipt basis: arrears paid this month enter CurrentMonthTaxable, re-estimate
// the WHOLE YEAR, and therefore change every remaining month's deduction.
//
// That retrospective effect is the correct s.392 behaviour and it is what a
// per-month engine cannot express: a monthly engine would tax the arrears once,
// in the month they were paid, at whatever rate that month's pay implied.
func TestIncomeTax_ArrearsRaiseTheAnnualEstimateNotAMonthlyThreshold(t *testing.T) {
	rs := itRuleSet(t)

	// Six months in, five months to run after this one, on 1,00,000 a month.
	base := IncomeTaxInput{
		TaxYear:                   itYear(),
		Regime:                    RegimeNew,
		IsResident:                true,
		AgeBand:                   AgeBelow60,
		PANAvailable:              true,
		YTDTaxableThisEmployer:    itDec(t, "600000"),
		YTDTDSThisEmployer:        itDec(t, "10000"),
		CurrentMonthTaxable:       itDec(t, "100000"),
		ProjectedRemainingTaxable: itDec(t, "500000"),
		MonthsRemainingInclusive:  6,
	}
	plain, err := IncomeTaxDerive(base, rs)
	if err != nil {
		t.Fatalf("without arrears: %v", err)
	}

	arrears := base
	arrears.CurrentMonthTaxable = arrears.CurrentMonthTaxable.Add(itDec(t, "300000"))
	withArrears, err := IncomeTaxDerive(arrears, rs)
	if err != nil {
		t.Fatalf("with arrears: %v", err)
	}

	if !withArrears.EstimatedAnnualTaxable.Sub(plain.EstimatedAnnualTaxable).Equal(itDec(t, "300000")) {
		t.Errorf("arrears of 3,00,000 changed the ANNUAL estimate by %s; on the receipt basis they must "+
			"raise it by exactly their own amount",
			withArrears.EstimatedAnnualTaxable.Sub(plain.EstimatedAnnualTaxable))
	}
	if !withArrears.TDSThisMonth.GreaterThan(plain.TDSThisMonth) {
		t.Fatal("arrears did not raise this month's deduction; the annual estimate is not being re-spread")
	}
	// The arrears are spread over the SIX remaining months, not taken whole out of
	// this one. If the extra tax appeared entirely in this payslip, the engine
	// would be taxing the arrears as an event rather than re-estimating the year.
	extraThisMonth := withArrears.TDSThisMonth.Sub(plain.TDSThisMonth)
	extraAnnual := withArrears.AnnualTax.Sub(plain.AnnualTax)
	if !extraThisMonth.LessThan(extraAnnual) {
		t.Errorf("the whole additional annual tax of %s appeared in ONE payslip (%s). s.392 spreads the "+
			"re-estimated year over the months remaining", extraAnnual, extraThisMonth)
	}
	t.Logf("arrears raised the annual tax by %s and this month's deduction by %s across 6 months",
		extraAnnual.StringFixed(0), extraThisMonth.StringFixed(0))
}

func TestIncomeTax_RejectsZeroMonthsRemaining(t *testing.T) {
	rs := itRuleSet(t)
	for _, months := range []int{0, -1} {
		in := itBase(t, "1325000")
		in.MonthsRemainingInclusive = months
		if _, err := IncomeTaxDerive(in, rs); !errors.Is(err, ErrMonthsRemaining) {
			t.Errorf("months=%d: want ErrMonthsRemaining, got %v. Dividing by zero months panics and "+
				"clamping to one silently deducts a whole year's tax out of a single payslip", months, err)
		}
	}
}

// TestIncomeTax_RejectsOtherBadInput covers the remaining refusals, each of
// which exists because the alternative is a plausible wrong number.
func TestIncomeTax_RejectsOtherBadInput(t *testing.T) {
	rs := itRuleSet(t)

	if _, err := IncomeTaxDerive(itBase(t, "100000"), IncomeTaxRuleSet{}); !errors.Is(err, ErrNoRuleSet) {
		t.Errorf("empty ruleset: want ErrNoRuleSet, got %v", err)
	}

	wrongYear := itBase(t, "100000")
	wrongYear.TaxYear = TaxYear{StartYear: 2029}
	if _, err := IncomeTaxDerive(wrongYear, rs); !errors.Is(err, ErrNoRuleSet) {
		t.Errorf("mismatched tax year: want ErrNoRuleSet, got %v. A ruleset for the wrong year succeeds "+
			"and produces a plausible number under last year's slabs", err)
	}

	badRegime := itBase(t, "100000")
	badRegime.Regime = Regime("DEFAULT")
	if _, err := IncomeTaxDerive(badRegime, rs); !errors.Is(err, ErrUnknownRegime) {
		t.Errorf("unknown regime: want ErrUnknownRegime, got %v", err)
	}
	emptyRegime := itBase(t, "100000")
	emptyRegime.Regime = ""
	if _, err := IncomeTaxDerive(emptyRegime, rs); !errors.Is(err, ErrUnknownRegime) {
		t.Errorf("empty regime must NOT default to NEW inside the engine: got %v. Defaulting lives in "+
			"payrolldb; an engine that defaults hides a missing election", err)
	}

	noPAN := itBase(t, "1325000")
	noPAN.PANAvailable = false
	if _, err := IncomeTaxDerive(noPAN, rs); !errors.Is(err, ErrPANRequired) {
		t.Errorf("no PAN: want ErrPANRequired, got %v. s.206AA's interaction with the average rate, the "+
			"rebate and the marginal reliefs could not be read from a primary text here, so the engine "+
			"REFUSES rather than under-deducting or inventing an implementation", err)
	}

	negative := itBase(t, "1325000")
	negative.CurrentMonthTaxable = itDec(t, "-1")
	if _, err := IncomeTaxDerive(negative, rs); !errors.Is(err, ErrNegativeStatutoryAmount) {
		t.Errorf("negative taxable salary: want ErrNegativeStatutoryAmount, got %v", err)
	}
}

// TestIncomeTax_NoPerTransactionThresholdExists is the structural guard against
// inheriting internal/compliance's per-transaction approximation.
//
// It asserts the absence TWO ways, because either alone is weak: a struct-shape
// check would pass an engine that tested a hard-coded threshold, and a
// behavioural check alone would pass an engine that had the field but happened
// not to use it on this input.
func TestIncomeTax_NoPerTransactionThresholdExists(t *testing.T) {
	rs := itRuleSet(t)

	// (a) BY CONSTRUCTION: no field of IncomeTaxInput names a threshold.
	ty := reflect.TypeOf(IncomeTaxInput{})
	for i := 0; i < ty.NumField(); i++ {
		name := strings.ToLower(ty.Field(i).Name)
		for _, banned := range []string{"threshold", "pertxn", "limit", "basis"} {
			if strings.Contains(name, banned) {
				t.Errorf("IncomeTaxInput.%s looks like a threshold. s.392 has NO per-transaction or "+
					"per-month threshold: the base is the estimated income for the whole financial year, "+
					"and a threshold field here is the compliance engine's disclosed approximation leaking "+
					"into a computation the statute defines differently", ty.Field(i).Name)
			}
		}
	}

	// (b) BEHAVIOURALLY: the employee whose MONTHLY pay is under every plausible
	// monthly-equivalent exemption but whose ANNUAL pay is far over the annual
	// one. 1,10,000 a month is 13,20,000 a year. A per-payslip engine deducts
	// nothing for eleven months; s.392 deducts every month from April.
	monthly := itDec(t, "110000")
	in := IncomeTaxInput{
		TaxYear:                   itYear(),
		Regime:                    RegimeNew,
		IsResident:                true,
		AgeBand:                   AgeBelow60,
		PANAvailable:              true,
		CurrentMonthTaxable:       monthly,
		ProjectedRemainingTaxable: monthly.Mul(itDec(t, "11")),
		MonthsRemainingInclusive:  12,
	}
	got, err := IncomeTaxDerive(in, rs)
	if err != nil {
		t.Fatalf("IncomeTaxDerive: %v", err)
	}
	if !got.TDSThisMonth.IsPositive() {
		t.Fatalf("an employee on %s a month (%s a year) was deducted NOTHING in April. That is a "+
			"per-transaction threshold test wearing s.392's name: the employee gets eleven months of "+
			"untaxed salary and a demand in March",
			monthly.StringFixed(0), got.EstimatedAnnualTaxable.StringFixed(0))
	}
	if got.PeriodBasis != BasisAnnual {
		t.Errorf("periodBasis: got %q, want ANNUAL", got.PeriodBasis)
	}
	t.Logf("monthly pay %s, annual taxable %s, April deduction %s",
		monthly.StringFixed(0), got.EstimatedAnnualTaxable.StringFixed(0), got.TDSThisMonth.StringFixed(0))
}

// TestIncomeTax_OldRegimeSpecifics pins the three behaviours that differ between
// the regimes and that a single shared code path would erase.
func TestIncomeTax_OldRegimeSpecifics(t *testing.T) {
	rs := itRuleSet(t)

	// (1) A house-property loss may NOT be set off against salary under the s.202
	// default regime. Allowing it would UNDER-deduct by the slab tax on up to
	// 2,00,000 for everyone with a home loan.
	newRegime := itBase(t, "1600000")
	newRegime.HousePropertyLoss = itDec(t, "200000")
	got, err := IncomeTaxDerive(newRegime, rs)
	if err != nil {
		t.Fatalf("new regime with HP loss: %v", err)
	}
	itWant(t, "estimatedAnnualTaxable", got.EstimatedAnnualTaxable, "1525000",
		"the new regime bars the house-property set-off against salary; allowing it under-deducts")

	// (2) Under the old regime it IS allowed, and it is CAPPED at 2,00,000.
	oldRegime := itBase(t, "1600000")
	oldRegime.Regime = RegimeOld
	oldRegime.HousePropertyLoss = itDec(t, "500000")
	gotOld, err := IncomeTaxDerive(oldRegime, rs)
	if err != nil {
		t.Fatalf("old regime with HP loss: %v", err)
	}
	itWant(t, "estimatedAnnualTaxable", gotOld.EstimatedAnnualTaxable, "1350000",
		"16,00,000 less the 50,000 old-regime standard deduction less the 2,00,000 CAP — an uncapped "+
			"set-off would under-deduct on the excess")

	// (3) Chapter VI-A is old-regime only.
	newVIA := itBase(t, "1600000")
	newVIA.ChapterVIA = itDec(t, "150000")
	gotNewVIA, err := IncomeTaxDerive(newVIA, rs)
	if err != nil {
		t.Fatalf("new regime with Chapter VI-A: %v", err)
	}
	itWant(t, "estimatedAnnualTaxable", gotNewVIA.EstimatedAnnualTaxable, "1525000",
		"Chapter VI-A is not available under the s.202 default regime; allowing it under-deducts")

	// (4) The senior-citizen basic exemption applies to a RESIDENT only.
	senior := itBase(t, "1000000")
	senior.Regime = RegimeOld
	senior.AgeBand = AgeSenior
	res, err := IncomeTaxDerive(senior, rs)
	if err != nil {
		t.Fatalf("resident senior: %v", err)
	}
	nonRes := senior
	nonRes.IsResident = false
	nr, err := IncomeTaxDerive(nonRes, rs)
	if err != nil {
		t.Fatalf("non-resident senior: %v", err)
	}
	if !nr.TaxBeforeRebate.GreaterThan(res.TaxBeforeRebate) {
		t.Errorf("a NON-RESIDENT aged 60+ must not get the resident senior-citizen exemption: "+
			"resident tax %s, non-resident tax %s", res.TaxBeforeRebate, nr.TaxBeforeRebate)
	}
}
