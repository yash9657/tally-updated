// Employees' State Insurance: the contribution-period coverage LATCH, and the
// contribution itself.
//
// Both functions are PURE — no I/O, no clock, no mutable package global.
//
// ---------------------------------------------------------------------------
// THE LATCH IS THE WHOLE POINT OF ESICoverage
// ---------------------------------------------------------------------------
// The naive implementation — and the one most payroll products actually ship —
// tests the wage ceiling every month. That is WRONG, and it is wrong in BOTH
// directions:
//
//   - An employee covered at the start of a contribution period who gets a
//     mid-period raise past the ceiling CONTINUES to be covered until the period
//     ends. A monthly test drops them in the month of the raise, which cuts off
//     their medical benefit mid-treatment and under-remits the employer's
//     liability — an ESIC inspection recovers it with interest and damages.
//   - An employee already above the ceiling at the period start stays OUT for
//     the whole period. A monthly test pulls them IN in any month their wages
//     happen to dip (a month of loss of pay, say), deducting a contribution that
//     was never due and registering a coverage the employee does not have.
//
// The authority is the proviso to Rule 50 of the ESI (Central) Rules, 1950: an
// employee whose wages exceed the prescribed limit "at any time after (and not
// before) the beginning of the contribution period shall continue to be an
// employee until the end of that period". The coverage test therefore has
// exactly ONE evaluation instant per period, and ESICoverage returns that
// instant (WagesTestedOn) so the decision can be audited without re-deriving
// it. A result whose WagesTestedOn falls inside the wage month is a monthly test
// masquerading as a latch.
//
// The two contribution periods are 1 April - 30 September and 1 October -
// 31 March. They are NOT the financial year and NOT the tax year: a pay run that
// reuses either latches the wrong boundary from October onwards, and the
// October-to-March period SPANS A YEAR BOUNDARY, which is the arithmetic that
// gets written wrong (see ContributionPeriodFor).
//
// ---------------------------------------------------------------------------
// ESI HAS A COVERAGE CEILING, NOT A CONTRIBUTION CEILING
// ---------------------------------------------------------------------------
// Once an employee is covered, the contribution is computed on the ACTUAL wages
// paid, with no cap at all. Capping the contribution at the coverage ceiling —
// the reflex carried over from the provident fund, where the ceiling really does
// cap the contribution — under-remits on every covered employee who gets a
// mid-period raise, which is precisely the population the latch creates.
//
// ---------------------------------------------------------------------------
// ROUNDING: BOTH SHARES GO UP
// ---------------------------------------------------------------------------
// Rule 51 drafts each share as "a sum (rounded to the next higher rupee) equal
// to <x> per cent. of the wages". The parenthesis appears in the employer's
// clause and in the employee's clause alike, so BOTH shares are ceiled. Several
// commercial payroll products ceil only the employee's share; the rule text is
// the higher authority. RoundUpRupee's doc records the same judgement, and
// TestESIDerive_BothSharesRoundUp pins it with a case where a half-up employer
// share would differ by a rupee.
//
// ---------------------------------------------------------------------------
// RATE PROVENANCE, AND A DOCUMENTED GAP
// ---------------------------------------------------------------------------
// This file contains no rates. Every rate and limit arrives in the
// SocialSecurityRuleSet carrying its own citation and Confidence, read through
// Rate.Use, curated offline (see rates.go's banner) and NOT read out of a
// gazette in this environment. Re-verify before real books.
//
// GAP: the higher coverage ceiling for a person with a disability (Rule 50's
// second proviso, Rs 25,000) is NOT modelled — rates.go's ESIWageCeiling
// citation says so too. Modelling it needs a disability flag on the employee
// master and a second ceiling row. Until both exist such an employee is tested
// against the ordinary ceiling, which errs towards EXCLUDING them from coverage.
// Do not paper over it by hand-editing a wage.
package payroll

import (
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// ErrInvalidWageRevisions is returned when the effective-dated wage history a
// caller supplies cannot resolve to exactly one wage on the date the coverage
// latch tests.
//
// It is an error and never a guess. Silently picking one of two rows claiming
// the same effective date would make an employee's coverage — and therefore
// their medical benefit — depend on slice order, and the resulting return would
// be irreproducible.
var ErrInvalidWageRevisions = errors.New("payroll: invalid wage revisions")

// ErrInvalidPaidDays is returned when a covered employee's month carries no
// positive number of paid days. Rule 52's average-daily-wage test has no
// meaning over zero days, and a wage figure that cannot be attributed to any day
// is a bug in the pay run rather than a zero contribution.
var ErrInvalidPaidDays = errors.New("payroll: paid days must be positive for a covered employee")

// ESI coverage reasons — a CLOSED vocabulary, because the reason reaches the ESI
// return and the payroll register and free text there cannot be reconciled.
const (
	// CoverageReasonAtOrBelowLimit — covered: wages on the test date did not
	// EXCEED the ceiling. Wages exactly equal to the ceiling fall here, because
	// Rule 50's word is "exceed".
	CoverageReasonAtOrBelowLimit = "WAGES_AT_OR_BELOW_LIMIT_ON_TEST_DATE"
	// CoverageReasonExceedsLimit — not covered: wages on the test date exceeded
	// the ceiling. A later reduction does NOT bring the employee back in before
	// the next period begins.
	CoverageReasonExceedsLimit = "WAGES_EXCEED_LIMIT_ON_TEST_DATE"
	// CoverageReasonNotEmployed — not covered: the employee was not in
	// employment at any point in the wage month.
	CoverageReasonNotEmployed = "NOT_EMPLOYED_IN_WAGE_MONTH"
)

// ESI component names.
const (
	ComponentESIEmployee = "ESI_EMPLOYEE"
	ComponentESIEmployer = "ESI_EMPLOYER"
)

// WageRevision is one row of an employee's effective-dated wage history.
//
// The latch needs a HISTORY and not a current wage: it must answer "what were
// this employee's wages on 1 October", which is a question about the past, in a
// pay run that may be executing in February. A caller that can pass only the
// current wage cannot implement the latch at all, and will silently implement
// the monthly test instead — which is why this is a slice and not a decimal.
type WageRevision struct {
	// EffectiveFrom is the first date this wage applies, INCLUSIVE. Half-open
	// [EffectiveFrom, next EffectiveFrom), the same convention rates.go's
	// windows and migration 0030's price lists use.
	EffectiveFrom time.Time
	// MonthlyWages is the ESI-reckonable monthly wage under this revision.
	MonthlyWages decimal.Decimal
}

// CoverageInput is the complete, self-contained input to the coverage latch.
//
// As with WageInput and PFInput: no identity, no IP number, no name. Coverage is
// a function of dates and money.
type CoverageInput struct {
	// Month is the wage month being run. It selects the contribution period, and
	// NOTHING else — in particular it is not the date the ceiling is tested on.
	Month WageMonth
	// JoinedOn is the date of joining. A zero value means the employee was
	// already in employment before the contribution period began, which is the
	// ordinary case and the reason it is not required.
	JoinedOn time.Time
	// LeftOn is the last working day. Zero means still employed.
	LeftOn time.Time
	// Revisions is the effective-dated wage history. It need not be sorted, but
	// it must contain exactly one row in force on the test date and no two rows
	// sharing an effective date.
	Revisions []WageRevision
}

// CoverageResult is the latch decision plus everything needed to audit it.
type CoverageResult struct {
	// PeriodStart and PeriodEnd bound the contribution period containing the
	// wage month: 1 Apr - 30 Sep, or 1 Oct - 31 Mar.
	PeriodStart time.Time
	PeriodEnd   time.Time
	// WagesTestedOn is the SINGLE instant at which the ceiling was applied — the
	// period start, or the joining date for a mid-period joiner. See the package
	// doc: echoing it is what makes the latch auditable.
	WagesTestedOn time.Time
	// WagesTested is the wage in force at that instant. It is NOT this month's
	// wage, and the two differ for exactly the employees the latch exists for.
	WagesTested decimal.Decimal
	// Limit is the coverage ceiling that was applied.
	Limit decimal.Decimal
	// Covered is the decision, held for the WHOLE period.
	Covered bool
	// Reason is one of the CoverageReason* constants.
	Reason string
	// PeriodBasis is always BasisContributionPeriod. ESI's period is neither the
	// month (EPF) nor the year (income tax); a single threshold field could not
	// carry all three, which is why there are three.
	PeriodBasis PeriodBasis
	// RuleSetVersion echoes the ruleset that produced the decision.
	RuleSetVersion string
}

// ContributionPeriodFor returns the ESI contribution period containing d:
// [1 April, 30 September] or [1 October, 31 March].
//
// The October-to-March period SPANS A YEAR BOUNDARY, and that is the arithmetic
// that gets written wrong: for January, February and March the period started in
// the PREVIOUS calendar year, so a latch that reads the current year's 1 October
// is testing a date seven months in the future and will find no wage revision at
// all — or, worse, the wrong one.
func ContributionPeriodFor(d time.Time) (start, end time.Time) {
	y := d.Year()
	switch m := d.Month(); {
	case m >= time.April && m <= time.September:
		start = time.Date(y, time.April, 1, 0, 0, 0, 0, time.UTC)
		end = time.Date(y, time.September, 30, 0, 0, 0, 0, time.UTC)
	case m >= time.October:
		start = time.Date(y, time.October, 1, 0, 0, 0, 0, time.UTC)
		end = time.Date(y+1, time.March, 31, 0, 0, 0, 0, time.UTC)
	default: // January, February, March — the previous year's October period.
		start = time.Date(y-1, time.October, 1, 0, 0, 0, 0, time.UTC)
		end = time.Date(y, time.March, 31, 0, 0, 0, 0, time.UTC)
	}
	return start, end
}

// wageInForceOn resolves an effective-dated wage history to the single wage in
// force on d.
//
// It ERRORS rather than guessing. No row in force, or two rows claiming the same
// effective date, both mean the caller's history is wrong; picking one silently
// would make a coverage decision depend on slice order.
func wageInForceOn(revisions []WageRevision, d time.Time) (decimal.Decimal, error) {
	var best WageRevision
	found := false
	for _, r := range revisions {
		if r.MonthlyWages.IsNegative() {
			return decimal.Zero, fmt.Errorf("%w: negative wage %s effective %s",
				ErrInvalidWageRevisions, r.MonthlyWages.String(), r.EffectiveFrom.Format("2006-01-02"))
		}
		if r.EffectiveFrom.After(d) {
			continue
		}
		if found && r.EffectiveFrom.Equal(best.EffectiveFrom) {
			return decimal.Zero, fmt.Errorf("%w: two wage revisions effective %s",
				ErrInvalidWageRevisions, r.EffectiveFrom.Format("2006-01-02"))
		}
		if !found || r.EffectiveFrom.After(best.EffectiveFrom) {
			best = r
			found = true
		}
	}
	if !found {
		return decimal.Zero, fmt.Errorf("%w: no wage revision in force on %s",
			ErrInvalidWageRevisions, d.Format("2006-01-02"))
	}
	return best.MonthlyWages, nil
}

// ESICoverage decides whether an employee is covered for the contribution period
// containing the wage month, per the proviso to Rule 50.
//
// The decision is taken ONCE PER PERIOD, at PeriodStart — or at the joining date
// for someone who joined mid-period, since there were no wages of theirs to test
// at the period start. It is NEVER re-taken mid-period, in either direction.
//
// Order of operations, and it matters: EMPLOYMENT is tested BEFORE wages. A
// month in which the employee was not employed has no wage history to resolve,
// and testing wages first would fail a perfectly ordinary pay run with
// ErrInvalidWageRevisions for every month before a joiner started.
func ESICoverage(in CoverageInput, rs SocialSecurityRuleSet) (CoverageResult, error) {
	if rs.Version == "" {
		return CoverageResult{}, fmt.Errorf("%w: empty social-security ruleset", ErrNoRuleSet)
	}
	limit, err := rs.ESIWageCeiling.Use()
	if err != nil {
		return CoverageResult{}, fmt.Errorf("ESI coverage wage ceiling: %w", err)
	}

	monthStart := in.Month.First()
	monthEnd := in.Month.Last()
	periodStart, periodEnd := ContributionPeriodFor(monthStart)

	res := CoverageResult{
		PeriodStart:    periodStart,
		PeriodEnd:      periodEnd,
		Limit:          limit,
		PeriodBasis:    BasisContributionPeriod,
		RuleSetVersion: rs.Version,
	}

	// Employment first. Coverage is a question about an employee, and someone
	// who joined after this month ended, or left before it began, is not one.
	// This is deliberately NOT folded into the wage test: "not employed" and
	// "above the ceiling" are different facts and the ESI return distinguishes
	// them.
	if !in.JoinedOn.IsZero() && in.JoinedOn.After(monthEnd) {
		res.Reason = CoverageReasonNotEmployed
		return res, nil
	}
	if !in.LeftOn.IsZero() && in.LeftOn.Before(monthStart) {
		res.Reason = CoverageReasonNotEmployed
		return res, nil
	}

	// THE LATCH. The test instant is the PERIOD START, not the wage month. A
	// mid-period joiner is tested on the day they joined, because there were no
	// wages of theirs in existence at the period start; from that instant the
	// same latch applies for the remainder of the period.
	testOn := periodStart
	if !in.JoinedOn.IsZero() && in.JoinedOn.After(periodStart) {
		testOn = in.JoinedOn
	}

	wages, err := wageInForceOn(in.Revisions, testOn)
	if err != nil {
		return CoverageResult{}, err
	}
	res.WagesTestedOn = testOn
	res.WagesTested = wages

	// Rule 50 says "exceed". Wages EXACTLY at the ceiling are therefore COVERED
	// — GreaterThan, never GreaterThanOrEqual. At the current ceiling that is a
	// real population of employees on a round Rs 21,000, and flipping the
	// operator drops every one of them out of the scheme.
	if wages.GreaterThan(limit) {
		res.Covered = false
		res.Reason = CoverageReasonExceedsLimit
		return res, nil
	}
	res.Covered = true
	res.Reason = CoverageReasonAtOrBelowLimit
	return res, nil
}

// ESIInput is the complete, self-contained input to ESIDerive.
type ESIInput struct {
	// Month is the wage month the wages are payable for.
	Month WageMonth
	// Covered is the latch decision from ESICoverage. It is PASSED IN rather
	// than re-derived, so the contribution and the coverage are provably the
	// same decision and a caller cannot accidentally run the contribution
	// against a fresh monthly test.
	Covered bool
	// Wages are the ACTUAL ESI-reckonable wages payable for the month. There is
	// no contribution ceiling: this figure is used whole, even when it exceeds
	// the coverage ceiling. See the package doc.
	Wages decimal.Decimal
	// PaidDays is the number of days for which wages were paid in the month. It
	// serves the Rule 52 average-daily-wage test and the ESI return's "No. of
	// Days for which wages paid" column.
	PaidDays int
}

// ESIResult is the derived ESI position for one employee for one wage month,
// plus an echo of everything it used. Both shares are whole rupees, both
// rounded UP (Rule 51).
type ESIResult struct {
	// RuleSetVersion echoes the ruleset that produced these numbers.
	RuleSetVersion string
	// Month echoes the wage month.
	Month WageMonth
	// Applied is false when the employee is not covered for the period. Every
	// money field is then zero and Components is empty: there is no partial ESI.
	Applied bool
	// Wages echoes the UNCAPPED wage the contribution was computed on.
	Wages decimal.Decimal
	// PaidDays echoes the days the Rule 52 test used.
	PaidDays int

	EmployeeRatePct decimal.Decimal
	EmployerRatePct decimal.Decimal
	EmployeeShare   decimal.Decimal
	EmployerShare   decimal.Decimal

	// LowWageExempt reports the Rule 52 exemption: the EMPLOYEE's share is nil
	// and the EMPLOYER's share is still payable in full. Getting that backwards
	// costs the employee 0.75% of their wage and short-pays the corporation.
	LowWageExempt bool
	// LowWageThreshold is limit * PaidDays — the monthly wage at or below which
	// the Rule 52 exemption applies for this many paid days. It is published
	// instead of an average daily wage because it is the number the test
	// actually compares against, and because it is EXACT (see ESIDerive).
	LowWageThreshold decimal.Decimal

	Components  []StatutoryComponent
	PeriodBasis PeriodBasis
}

// ComponentByName returns the named component row, if the derivation produced
// one.
func (r ESIResult) ComponentByName(name string) (StatutoryComponent, bool) {
	for _, c := range r.Components {
		if c.Name == name {
			return c, true
		}
	}
	return StatutoryComponent{}, false
}

// ESIDerive computes the ESI contribution for one employee for one wage month.
// It is pure and deterministic.
//
// Order of operations:
//
//  1. Not covered -> Applied false and every figure zero. There is no partial
//     ESI and no pro-rating of coverage within a period.
//  2. Rule 52: if the average daily wage does NOT EXCEED the prescribed limit,
//     the employee's share is nil and the employer's share is unaffected. The
//     test is done as wages <= limit * paidDays rather than as
//     wages / paidDays <= limit. It is the SAME inequality, with no division —
//     so there is no working precision to argue about and no rounding step that
//     could push an employee across the boundary in either direction.
//  3. Rule 51: each share is a sum rounded to the NEXT HIGHER rupee equal to its
//     percentage of wages. Both shares, independently, upward.
func ESIDerive(in ESIInput, rs SocialSecurityRuleSet) (ESIResult, error) {
	if rs.Version == "" {
		return ESIResult{}, fmt.Errorf("%w: empty social-security ruleset", ErrNoRuleSet)
	}
	if in.Wages.IsNegative() {
		return ESIResult{}, fmt.Errorf("%w: ESI wages %s", ErrNegativeStatutoryAmount, in.Wages.String())
	}

	employeePct, err := rs.ESIEmployeePct.Use()
	if err != nil {
		return ESIResult{}, fmt.Errorf("ESI employee rate: %w", err)
	}
	employerPct, err := rs.ESIEmployerPct.Use()
	if err != nil {
		return ESIResult{}, fmt.Errorf("ESI employer rate: %w", err)
	}
	lowWageDaily, err := rs.ESILowWageDailyLimit.Use()
	if err != nil {
		return ESIResult{}, fmt.Errorf("ESI low-wage daily limit: %w", err)
	}

	res := ESIResult{
		RuleSetVersion:  rs.Version,
		Month:           in.Month,
		PaidDays:        in.PaidDays,
		EmployeeRatePct: employeePct,
		EmployerRatePct: employerPct,
		PeriodBasis:     BasisContributionPeriod,
	}

	// (1) Not covered: nothing is payable and nothing is echoed as though it
	// were. Wages stays zero so that a caller summing ESIResult.Wages over a
	// register gets the ESI wage base and not the payroll.
	if !in.Covered {
		return res, nil
	}
	if in.PaidDays <= 0 {
		return ESIResult{}, fmt.Errorf("%w: got %d", ErrInvalidPaidDays, in.PaidDays)
	}

	res.Applied = true
	res.Wages = in.Wages
	res.LowWageThreshold = lowWageDaily.Mul(decimal.NewFromInt(int64(in.PaidDays)))

	// (3) Rule 51(a): the employer's share. Always payable, always ceiled — it
	// is not affected by the Rule 52 exemption below.
	res.EmployerShare = RoundUpRupee(pctOfWage(in.Wages, employerPct))

	// (2) Rule 52 says "does not exceed", so an employee EXACTLY at the limit IS
	// exempt: !GreaterThan, deliberately. That is the OPPOSITE direction from
	// Rule 50's "exceed" test in ESICoverage, where exactly-at-the-ceiling means
	// covered. The two boundaries point opposite ways and copying one into the
	// other is a bug in both places at once.
	if !in.Wages.GreaterThan(res.LowWageThreshold) {
		res.LowWageExempt = true
		res.EmployeeShare = decimal.Zero
	} else {
		res.EmployeeShare = RoundUpRupee(pctOfWage(in.Wages, employeePct))
	}

	// Both rows are emitted even when the employee's share is nil. A zero
	// employee share is a REAL statutory fact — the exemption was applied, and
	// the ESI return has to show the employee with a nil contribution. That is
	// NOT the same as the EDLI contribution in pf.go, where the rate is unknown
	// and therefore no row exists at all.
	res.Components = append(res.Components,
		StatutoryComponent{Name: ComponentESIEmployee, Amount: res.EmployeeShare, Tag: TagStatutory},
		StatutoryComponent{Name: ComponentESIEmployer, Amount: res.EmployerShare, Tag: TagStatutory},
	)
	return res, nil
}
