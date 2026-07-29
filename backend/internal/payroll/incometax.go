package payroll

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
)

// Income tax on salary — s.392 of the Income-tax Act 2025 (ex-s.192 of the 1961
// Act). This is the ANNUAL-BASE leaf, and the word "annual" is the whole design.
//
// ---------------------------------------------------------------------------
// WHY THERE IS NO THRESHOLD IN THIS FILE
// ---------------------------------------------------------------------------
// s.392 does not say "deduct when this payment exceeds X". It says the deductor
// shall deduct income-tax on the ESTIMATED INCOME OF THE ASSESSEE UNDER THE HEAD
// SALARIES FOR THAT FINANCIAL YEAR, computed at the average rate, and shall do
// it at the time of payment. The base is the whole year and the deduction is
// that year's tax spread over the months that are left.
//
// internal/compliance's s.194x engine tests a threshold PER TRANSACTION, and
// that is a DISCLOSED APPROXIMATION of an annual limit — it under-deducts on a
// stream of small vendor payments, which for a vendor is a recoverable exposure
// for the deductor. Inheriting it here would not be an approximation of s.392;
// it would be a DIFFERENT COMPUTATION. An employee earning Rs 1,10,000 a month
// is under the monthly-equivalent exemption in every single month of the year
// and over the annual exemption by more than eleven lakh. A per-payslip test
// deducts NOTHING for eleven months and then either deducts a year's tax out of
// one March salary or, more likely, deducts nothing at all — a short salary and
// a Form 138 that the department's TRACES cannot reconcile against Form 130.
//
// So: no threshold field exists on IncomeTaxInput, no threshold is tested
// anywhere below, and TestIncomeTax_NoPerTransactionThresholdExists asserts both
// by walking the struct and by computing that exact employee.
//
// ---------------------------------------------------------------------------
// THE ENGINE CARRIES NO STATE
// ---------------------------------------------------------------------------
// Year-to-date figures arrive as INPUT FIELDS. This leaf never accumulates,
// never reads a database and never sums a history: payrolldb gathers the YTD
// position and hands it over. That is what makes a payslip recomputable years
// later from the row that recorded its inputs.
//
// ---------------------------------------------------------------------------
// THE ORDER OF OPERATIONS, WHICH IS ITSELF THE SPEC
// ---------------------------------------------------------------------------
//
//	RoundNearestTen(total income)      <- s.516 (ex-288A), nearest TEN rupees
//	  -> slabs
//	  -> rebate                        <- s.156 (ex-87A)
//	  -> REBATE MARGINAL RELIEF        <- s.156 proviso, NEW regime only
//	  -> surcharge
//	  -> SURCHARGE MARGINAL RELIEF     <- at each band threshold
//	  -> cess
//	  -> RoundNearestRupee(annual tax) <- s.516 (ex-288B), nearest RUPEE
//	  -> subtract prior-employer TDS and this employer's YTD TDS
//	  -> divide over the months remaining (inclusive of this one)
//	  -> RoundNearestRupee(this month's TDS)
//
// BOTH marginal reliefs are required and they are different mechanisms. The
// surcharge relief is the familiar one. The s.156 rebate-cliff relief is the one
// engines omit: without it every employee whose total income sits between
// 12,00,000 and about 12,70,589 is over-taxed, because at 12,50,000 the slab tax
// is 67,500 while the statute caps the tax at 50,000 (the amount by which the
// income exceeds the rebate limit). The corpus fixture
// it-new-regime-rebate-cliff-marginal-relief carries a counterfactual that
// re-derives WITHOUT the rebate relief and fails if the two agree.
//
// ---------------------------------------------------------------------------
// DOCUMENTED GAPS — read these before believing a number out of here
// ---------------------------------------------------------------------------
//   - s.157 (ex-s.89) RELIEF ON ARREARS is NOT computed. Arrears enter
//     CurrentMonthTaxable on the receipt basis, which is correct for s.392, but
//     an employee entitled to spread-back relief must claim it in the return.
//     Error direction: OVER-deduction, which the employee recovers as a refund.
//   - PERQUISITES are not valued here. They must already be inside the taxable
//     figures the caller supplies.
//   - Chapter VI-A is a single aggregate and is OLD-REGIME ONLY. The new
//     regime's own surviving deductions — notably the employer's NPS
//     contribution under ex-s.80CCD(2) — are NOT modelled, so a new-regime
//     employee with employer NPS is OVER-deducted here.
//   - PROFESSIONAL TAX (a s.19 / ex-16(iii) deduction) is not modelled; state
//     slab tables were not researched. Error direction: OVER-deduction.
//   - s.206AA (no PAN) is REFUSED rather than approximated — see ErrPANRequired.

// ErrPANRequired is returned when a salary tax computation is asked for an
// employee whose PAN is not on record.
//
// THE JUDGEMENT, stated because it is a refusal and refusals need defending.
// s.206AA(1) requires deduction at the HIGHER of the rate in force and 20% when
// the deductee has furnished no PAN. Applying that to salary is not the same
// arithmetic as applying it to a s.194x payment: the "rate in force" for salary
// is the AVERAGE rate derived from the annual computation, and the interaction
// between the 20% floor, the rebate and the marginal reliefs is not something
// this build could read out of a primary text. The three options were:
//
//	(a) ignore the flag           -> UNDER-deduct, silently, on the employee
//	                                 whose paperwork is already incomplete;
//	(b) invent an implementation  -> a confidently wrong number in a Form 138;
//	(c) refuse                    -> the pay run stops and names the employee.
//
// (c) is chosen for the same reason ErrUnverifiedRate exists: this codebase
// refuses rather than guesses about statutory money. The operator's fix is one
// field of data entry, and until it is done the engine will not pretend to have
// computed a s.206AA deduction. If a future reading of s.206AA settles the
// salary interaction, implement it HERE and add a corpus fixture — do not
// quietly relax this into a zero.
var ErrPANRequired = errors.New("payroll: PAN is required to compute salary tax (s.206AA)")

// IncomeTaxInput is the complete, self-contained input to IncomeTaxDerive.
//
// Every year-to-date figure is an INPUT, never accumulated here. Note what is
// absent: any threshold, any per-transaction limit, and any identifier. PAN
// presence is a BOOLEAN, exactly as compliance/tds.go models s.206AA, so the
// leaf can never print, log or hash a real PAN.
type IncomeTaxInput struct {
	// TaxYear is the April-March year being estimated. It selects the ruleset
	// and is echoed into the result.
	TaxYear TaxYear
	// Regime is RegimeNew or RegimeOld. The engine DEFAULTS NOTHING — an empty
	// or unknown value is ErrUnknownRegime. payrolldb applies DefaultRegime
	// (RegimeNew, the s.202 default) before calling.
	Regime Regime
	// IsResident gates the age-band basic exemption AND the s.156 rebate, both of
	// which are available to a RESIDENT individual only.
	IsResident bool
	// AgeBand selects the old regime's basic exemption. It has no effect under
	// the new regime.
	AgeBand AgeBand
	// PANAvailable reports whether the employee's PAN is on record. False is
	// ErrPANRequired — see that sentinel.
	PANAvailable bool

	// PriorEmployerTaxable and PriorEmployerTDS are the ex-s.192(2) / Form 12B
	// particulars a mid-year joiner furnished about a FORMER employer. Ignoring
	// them is the commonest payroll-engine defect: the new employer then computes
	// on a part-year salary, the slabs come out low, and the employee owes the
	// difference plus interest at assessment.
	PriorEmployerTaxable decimal.Decimal
	PriorEmployerTDS     decimal.Decimal

	// YTDTaxableThisEmployer and YTDTDSThisEmployer are the months of this pay
	// year ALREADY RUN by this employer, EXCLUDING the current month.
	YTDTaxableThisEmployer decimal.Decimal
	YTDTDSThisEmployer     decimal.Decimal

	// CurrentMonthTaxable is this month's taxable salary INCLUDING any arrears
	// paid this month. Arrears are on the RECEIPT basis under s.392, which means
	// they raise the ANNUAL estimate and therefore retrospectively change every
	// remaining month's deduction. That is the correct behaviour, not a bug: the
	// year's tax is re-estimated and re-spread.
	CurrentMonthTaxable decimal.Decimal
	// ProjectedRemainingTaxable is the salary expected in the months AFTER this
	// one. A mid-year revision changes this figure and the excess or deficiency
	// spreads over the remaining months automatically.
	ProjectedRemainingTaxable decimal.Decimal

	// MonthsRemainingInclusive counts this month plus the months after it. It
	// must be at least 1 or the derivation returns ErrMonthsRemaining: dividing
	// by zero would panic and clamping to one would deduct a whole year's tax
	// out of a single payslip.
	MonthsRemainingInclusive int
	// IsFinalMonth marks a leaver's last payslip or March. The WHOLE remaining
	// balance is deducted with no division.
	IsFinalMonth bool

	// HousePropertyLoss is the loss under the head house property the employee
	// declared, as a POSITIVE number. It is the only ex-s.192(2B) reduction the
	// employer may give, it is capped at 2,00,000, and under the s.202 default
	// regime it may NOT be set off against salary at all — see the constant below.
	HousePropertyLoss decimal.Decimal
	// ChapterVIA is the aggregate Chapter VI-A deduction. OLD REGIME ONLY; it is
	// ignored under the new regime.
	ChapterVIA decimal.Decimal
	// OtherIncomeDeclared is income under other heads the employee reported to
	// the employer so it can be taken into account. It may only INCREASE the
	// deduction (a loss under another head cannot be given effect to here).
	OtherIncomeDeclared decimal.Decimal
}

// IncomeTaxResult ECHOES everything the derivation used — the house rule
// internal/compliance's results follow. The echo is what lets a payslip explain
// itself years later without re-running the engine, and what lets the golden
// corpus assert the INTERMEDIATE steps rather than only the final number: a
// fixture that pinned nothing but TDSThisMonth would pass with the rebate and
// the surcharge both wrong in offsetting directions.
type IncomeTaxResult struct {
	// RegimeApplied is the regime actually used.
	RegimeApplied Regime
	// RuleSetVersion is the income-tax ruleset the figures came from.
	RuleSetVersion string
	// EstimatedAnnualTaxable is TOTAL INCOME after the standard deduction, the
	// house-property set-off, other income and Chapter VI-A, rounded to the
	// nearest TEN rupees under s.516 (ex-288A).
	EstimatedAnnualTaxable decimal.Decimal
	// StandardDeduction is the s.19 (ex-16(ia)) deduction actually allowed,
	// capped at the salary so it can never manufacture a negative income.
	StandardDeduction decimal.Decimal
	// TaxBeforeRebate is the slab tax, unrounded.
	TaxBeforeRebate decimal.Decimal
	// Rebate is the s.156 (ex-87A) rebate. Zero above the rebate limit.
	Rebate decimal.Decimal
	// RebateMarginalRelief is the s.156 proviso relief just above the cliff.
	// NEW regime only; the old regime's 5,00,000 cliff carries no relief.
	RebateMarginalRelief decimal.Decimal
	// Surcharge is the GROSS surcharge, before marginal relief.
	Surcharge decimal.Decimal
	// SurchargeMarginalRelief is the relief at the band threshold.
	SurchargeMarginalRelief decimal.Decimal
	// Cess is the Health and Education Cess on (tax after rebate + surcharge
	// after relief).
	Cess decimal.Decimal
	// AnnualTax is the year's tax, rounded to the nearest RUPEE under s.516
	// (ex-288B).
	AnnualTax decimal.Decimal
	// CreditedAlready is PriorEmployerTDS + YTDTDSThisEmployer.
	CreditedAlready decimal.Decimal
	// TDSThisMonth is the deduction for this payslip.
	TDSThisMonth decimal.Decimal
	// PeriodBasis is ALWAYS BasisAnnual. It is echoed rather than assumed so a
	// reader of a stored result can see that no per-transaction test was applied.
	PeriodBasis PeriodBasis
}

// housePropertyLossCap is the ex-s.71(3A) cap on the loss under the head house
// property that may be set off against other heads in a year.
//
// It is a bare decimal and not a Rate because it is not consumed as a statutory
// RATE in a contribution: it is a ceiling on an input the EMPLOYEE declares, and
// the direction of an error is over-deduction (a recoverable refund), not an
// under-remittance. It is still written from a string literal, never a float.
var housePropertyLossCap = decimal.RequireFromString("200000")

// IncomeTaxDerive computes the salary TDS for one payslip on an ANNUAL base.
//
// It is pure: no clock, no context, no I/O, no accumulation.
func IncomeTaxDerive(in IncomeTaxInput, rs IncomeTaxRuleSet) (IncomeTaxResult, error) {
	if rs.Version == "" {
		return IncomeTaxResult{}, fmt.Errorf("%w: empty income-tax ruleset", ErrNoRuleSet)
	}
	if rs.TaxYear != in.TaxYear {
		// A ruleset for the wrong year is worse than no ruleset: the arithmetic
		// succeeds and produces a plausible number under last year's slabs.
		return IncomeTaxResult{}, fmt.Errorf("%w: ruleset %q is for tax year %s, not %s",
			ErrNoRuleSet, rs.Version, rs.TaxYear, in.TaxYear)
	}
	if in.Regime != RegimeNew && in.Regime != RegimeOld {
		return IncomeTaxResult{}, fmt.Errorf("%w: %q", ErrUnknownRegime, in.Regime)
	}
	if !in.PANAvailable {
		return IncomeTaxResult{}, fmt.Errorf("%w", ErrPANRequired)
	}
	if in.MonthsRemainingInclusive < 1 {
		return IncomeTaxResult{}, fmt.Errorf("%w: got %d", ErrMonthsRemaining, in.MonthsRemainingInclusive)
	}
	// Every money input is non-negative by construction. A negative "taxable
	// salary" is a recovery, not a wage adjustment, and the statutory roundings
	// below do not cover negatives (see RoundNearestRupee's sign note).
	for _, f := range []struct {
		name string
		val  decimal.Decimal
	}{
		{"PriorEmployerTaxable", in.PriorEmployerTaxable},
		{"PriorEmployerTDS", in.PriorEmployerTDS},
		{"YTDTaxableThisEmployer", in.YTDTaxableThisEmployer},
		{"YTDTDSThisEmployer", in.YTDTDSThisEmployer},
		{"CurrentMonthTaxable", in.CurrentMonthTaxable},
		{"ProjectedRemainingTaxable", in.ProjectedRemainingTaxable},
		{"HousePropertyLoss", in.HousePropertyLoss},
		{"ChapterVIA", in.ChapterVIA},
		{"OtherIncomeDeclared", in.OtherIncomeDeclared},
	} {
		if f.val.IsNegative() {
			return IncomeTaxResult{}, fmt.Errorf("%w: %s is %s", ErrNegativeStatutoryAmount, f.name, f.val.String())
		}
	}

	// --- 1. the annual salary estimate -------------------------------------
	// ALL FOUR terms. Dropping PriorEmployerTaxable is the mid-year-joiner bug;
	// dropping ProjectedRemainingTaxable turns the estimate into a year-to-date
	// figure and back-loads the whole year's tax into March.
	salary := in.PriorEmployerTaxable.
		Add(in.YTDTaxableThisEmployer).
		Add(in.CurrentMonthTaxable).
		Add(in.ProjectedRemainingTaxable)

	stdRate := rs.StandardDeductionNew
	if in.Regime == RegimeOld {
		stdRate = rs.StandardDeductionOld
	}
	stdDeduction, err := stdRate.Use()
	if err != nil {
		return IncomeTaxResult{}, fmt.Errorf("standard deduction: %w", err)
	}
	// Capped at the salary: a part-year employee earning 40,000 must not end up
	// with a NEGATIVE salary income that then absorbs their other income.
	if stdDeduction.GreaterThan(salary) {
		stdDeduction = salary
	}
	netSalary := salary.Sub(stdDeduction)

	// House-property loss. UNDER THE NEW REGIME IT IS NOT ALLOWED AGAINST SALARY
	// AT ALL — the s.202 regime bars the set-off, and an engine that allowed it
	// would UNDER-deduct by up to 2,00,000 worth of slab tax for anyone who
	// declared a home loan. Under the old regime it is capped at 2,00,000.
	hpLoss := decimal.Zero
	chapterVIA := decimal.Zero
	if in.Regime == RegimeOld {
		hpLoss = in.HousePropertyLoss
		if hpLoss.GreaterThan(housePropertyLossCap) {
			hpLoss = housePropertyLossCap
		}
		chapterVIA = in.ChapterVIA
	}

	totalIncome := netSalary.Add(in.OtherIncomeDeclared).Sub(hpLoss).Sub(chapterVIA)
	if totalIncome.IsNegative() {
		totalIncome = decimal.Zero
	}

	// --- 2. s.516 (ex-288A): total income to the nearest TEN ----------------
	// This happens BEFORE the slabs, not after, and to TEN rupees, not one.
	// Rounding to the rupee here shifts the slab arithmetic for anyone whose
	// income does not already end in a zero — pinned by the corpus fixture
	// it-income-rounds-to-nearest-ten and its counterfactual.
	taxable := RoundNearestTen(totalIncome)

	// --- 3. slabs -> rebate -> rebate marginal relief ------------------------
	before, rebate, rebateRelief, err := taxAfterRebate(taxable, in, rs)
	if err != nil {
		return IncomeTaxResult{}, err
	}
	afterRebate := before.Sub(rebate).Sub(rebateRelief)

	// --- 4. surcharge -> surcharge marginal relief ---------------------------
	surcharge, surchargeRelief, err := surchargeFor(taxable, afterRebate, in, rs)
	if err != nil {
		return IncomeTaxResult{}, err
	}

	// --- 5. cess -------------------------------------------------------------
	netTax := afterRebate.Add(surcharge).Sub(surchargeRelief)
	cessPct, err := rs.CessPct.Use()
	if err != nil {
		return IncomeTaxResult{}, fmt.Errorf("cess: %w", err)
	}
	// Mul then Shift(-2) rather than Div(100): a shift of the decimal point is
	// EXACT and consults no divisor precision. decimal.DivisionPrecision is a
	// MUTABLE PACKAGE GLOBAL that any other package's init() could change.
	cess := netTax.Mul(cessPct).Shift(-2)

	// --- 6. s.516 (ex-288B): the annual tax to the nearest RUPEE -------------
	annualTax := RoundNearestRupee(netTax.Add(cess))
	if annualTax.IsNegative() {
		// Unreachable with non-negative inputs, but a negative annual tax would
		// silently become a REFUND paid out of the next employee's deduction.
		return IncomeTaxResult{}, fmt.Errorf("%w: annual tax computed as %s", ErrNegativeStatutoryAmount, annualTax)
	}

	// --- 7. spread the balance over the months that are left -----------------
	credited := in.PriorEmployerTDS.Add(in.YTDTDSThisEmployer)
	balance := annualTax.Sub(credited)
	if balance.IsNegative() {
		// Over-deducted so far (a mid-year revision downwards, or a leaver whose
		// projection did not materialise). s.392 gives the employer no power to
		// REFUND tax already paid to the credit of the Central Government; the
		// employee claims it in the return. Clamp at zero.
		balance = decimal.Zero
	}

	var tdsThisMonth decimal.Decimal
	if in.IsFinalMonth {
		// A leaver's last payslip, or March. The whole balance goes now — there
		// is no later month to spread it into, and dividing by the nominal 1
		// would be the same answer by accident rather than by decision.
		tdsThisMonth = RoundNearestRupee(balance)
	} else {
		months := decimal.NewFromInt(int64(in.MonthsRemainingInclusive))
		// QuoRem at 8 dp rather than Div: Div consults decimal.DivisionPrecision,
		// the mutable package global noted above. Truncating at 8 dp cannot move
		// a nearest-rupee decision — the only way it could is if the exact
		// quotient were exactly x.5, and an exact x.5 terminates and survives the
		// truncation intact.
		quotient, _ := balance.QuoRem(months, 8)
		tdsThisMonth = RoundNearestRupee(quotient)
	}

	return IncomeTaxResult{
		RegimeApplied:           in.Regime,
		RuleSetVersion:          rs.Version,
		EstimatedAnnualTaxable:  taxable,
		StandardDeduction:       RoundPaise(stdDeduction),
		TaxBeforeRebate:         RoundPaise(before),
		Rebate:                  RoundPaise(rebate),
		RebateMarginalRelief:    RoundPaise(rebateRelief),
		Surcharge:               RoundPaise(surcharge),
		SurchargeMarginalRelief: RoundPaise(surchargeRelief),
		Cess:                    RoundPaise(cess),
		AnnualTax:               annualTax,
		CreditedAlready:         RoundPaise(credited),
		TDSThisMonth:            tdsThisMonth,
		PeriodBasis:             BasisAnnual,
	}, nil
}

// taxAfterRebate runs slabs -> rebate -> rebate marginal relief on a taxable
// figure and returns the three components separately.
//
// It is its own function because the SURCHARGE marginal relief has to re-run
// EXACTLY this computation at the band threshold. Two copies of it would drift,
// and the drift would show up only for the handful of employees who sit within a
// few thousand rupees of 50 lakh.
func taxAfterRebate(taxable decimal.Decimal, in IncomeTaxInput, rs IncomeTaxRuleSet) (before, rebate, relief decimal.Decimal, err error) {
	// THE ENHANCED BASIC EXEMPTION IS FOR A RESIDENT ONLY. The Finance Act's rate
	// schedule gives the higher exemption to a "resident individual" aged 60 or
	// 80 and over; a non-resident of any age gets the ordinary limit. Passing the
	// stored age band straight through would hand a non-resident senior an extra
	// 50,000 (or 2,50,000) of exempt income and UNDER-deduct on it every month —
	// a plausible number nobody would query, since the employee really is 62.
	band := in.AgeBand
	if !in.IsResident {
		band = AgeBelow60
	}
	slabs, err := rs.SlabsFor(in.Regime, band)
	if err != nil {
		return decimal.Zero, decimal.Zero, decimal.Zero, err
	}
	before, err = slabTax(taxable, slabs)
	if err != nil {
		return decimal.Zero, decimal.Zero, decimal.Zero, err
	}

	rebate, relief = decimal.Zero, decimal.Zero
	// s.156 is available to a RESIDENT individual only. A non-resident gets
	// neither the rebate nor its marginal relief.
	if !in.IsResident {
		return before, rebate, relief, nil
	}

	limitRate, maxRate := rs.RebateLimitNew, rs.RebateMaxNew
	hasMarginalRelief := true
	if in.Regime == RegimeOld {
		limitRate, maxRate = rs.RebateLimitOld, rs.RebateMaxOld
		// THE OLD REGIME'S 5,00,000 CLIFF CARRIES NO MARGINAL RELIEF. That is not
		// an omission: the relief was legislated for the new regime's cliff only.
		// Granting it here would UNDER-deduct from every old-regime employee just
		// over 5,00,000.
		hasMarginalRelief = false
	}
	limit, err := limitRate.Use()
	if err != nil {
		return decimal.Zero, decimal.Zero, decimal.Zero, fmt.Errorf("rebate limit: %w", err)
	}
	maxRebate, err := maxRate.Use()
	if err != nil {
		return decimal.Zero, decimal.Zero, decimal.Zero, fmt.Errorf("rebate cap: %w", err)
	}

	if !taxable.GreaterThan(limit) {
		// At or below the limit: rebate the whole tax, capped.
		rebate = decimal.Min(before, maxRebate)
		return before, rebate, relief, nil
	}
	if !hasMarginalRelief {
		return before, rebate, relief, nil
	}
	// THE s.156 PROVISO — the marginal relief engines omit. Just above the cliff
	// the tax may not exceed the amount by which total income exceeds the limit.
	// At a total income of 12,50,000 the slab tax is 67,500 and the excess is
	// 50,000, so 17,500 of relief is due. Delete this block and every employee
	// between 12,00,000 and about 12,70,589 is over-taxed by up to 17,500 a year.
	excess := taxable.Sub(limit)
	if before.GreaterThan(excess) {
		relief = before.Sub(excess)
	}
	return before, rebate, relief, nil
}

// slabTax walks the half-open [From, To) slab table. It rounds NOTHING: the
// statutory roundings happen at the two points the statute names and nowhere
// else, and an intermediate rounding here would be a third one.
func slabTax(taxable decimal.Decimal, slabs []Slab) (decimal.Decimal, error) {
	tax := decimal.Zero
	for i, s := range slabs {
		if !taxable.GreaterThan(s.From) {
			continue
		}
		top := taxable
		if !s.To.IsZero() && s.To.LessThan(top) {
			top = s.To
		}
		width := top.Sub(s.From)
		if !width.IsPositive() {
			continue
		}
		pct, err := s.Rate.Use()
		if err != nil {
			return decimal.Zero, fmt.Errorf("slab %d [%s,%s): %w", i, s.From, s.To, err)
		}
		tax = tax.Add(width.Mul(pct).Shift(-2))
	}
	return tax, nil
}

// surchargeFor computes the gross surcharge and its marginal relief.
//
// The applicable band is the HIGHEST one whose Above the taxable income exceeds
// (strictly — the statute says "exceeds"). Marginal relief at that band's
// threshold T compares:
//
//	actual = tax_after_rebate(income) + surcharge at this band's rate
//	cap    = tax_after_rebate(T) * (1 + the PREVIOUS band's rate) + (income - T)
//
// The PREVIOUS band's rate is what applies AT exactly T, because surcharge bites
// only where income EXCEEDS the threshold. Using this band's rate at T would
// overstate the cap and hand out relief that is not due; using zero at every
// band would understate it at the 1-crore and 2-crore thresholds and deny relief
// that is.
func surchargeFor(taxable, afterRebate decimal.Decimal, in IncomeTaxInput, rs IncomeTaxRuleSet) (surcharge, relief decimal.Decimal, err error) {
	bands, err := rs.SurchargeBandsFor(in.Regime)
	if err != nil {
		return decimal.Zero, decimal.Zero, err
	}
	idx := -1
	for i, b := range bands {
		if taxable.GreaterThan(b.Above) {
			idx = i
		}
	}
	if idx < 0 {
		return decimal.Zero, decimal.Zero, nil
	}
	band := bands[idx]
	pct, err := band.Rate.Use()
	if err != nil {
		return decimal.Zero, decimal.Zero, fmt.Errorf("surcharge band above %s: %w", band.Above, err)
	}
	surcharge = afterRebate.Mul(pct).Shift(-2)

	prevPct := decimal.Zero
	if idx > 0 {
		prevPct, err = bands[idx-1].Rate.Use()
		if err != nil {
			return decimal.Zero, decimal.Zero, fmt.Errorf("surcharge band above %s: %w", bands[idx-1].Above, err)
		}
	}
	atThresholdBefore, atThresholdRebate, atThresholdRelief, err := taxAfterRebate(band.Above, in, rs)
	if err != nil {
		return decimal.Zero, decimal.Zero, err
	}
	atThreshold := atThresholdBefore.Sub(atThresholdRebate).Sub(atThresholdRelief)
	hundred := decimal.NewFromInt(100)
	reliefCap := atThreshold.Mul(hundred.Add(prevPct)).Shift(-2).Add(taxable.Sub(band.Above))

	actual := afterRebate.Add(surcharge)
	if actual.GreaterThan(reliefCap) {
		relief = actual.Sub(reliefCap)
	}
	return surcharge, relief, nil
}
