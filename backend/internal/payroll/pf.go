// Provident-fund derivation: EPF, EPS and EDLI.
//
// PFDerive is a PURE function. It performs no I/O, reads no clock, and touches
// no mutable package global, so it is safe inside the atomic pay-run
// transaction and reproducible in any process. Every defaulting decision
// belongs to payrolldb; this leaf defaults NOTHING and errors instead — the
// split ledger/tds.go already uses.
//
// ---------------------------------------------------------------------------
// THE THREE WAGE FIGURES ARE THREE DIFFERENT NUMBERS
// ---------------------------------------------------------------------------
// EPF wages, EPS wages and EDLI wages are computed independently and are NOT
// interchangeable. The EPFO's ECR has a separate column for each precisely
// because they diverge:
//
//   - EPF wages follow the employer's election. PFBaseCeiling caps them at the
//     EPF wage ceiling; PFBaseActual contributes on the whole statutory wage.
//   - EPS wages are ALWAYS capped at the PENSION ceiling, even when EPF is paid
//     on the full wage, and are ZERO for a non-member. The pension is a defined
//     benefit computed on pensionable salary; diverting 8.33% of an uncapped
//     wage into it would over-fund a pension the scheme will never pay, and the
//     money would be lost to the member's provident fund for good.
//   - EDLI wages are always capped at the EDLI ceiling, independent of both.
//
// Collapsing any two of them into one variable is the single most likely
// wrong-number bug in this file, and
// TestPFDerive_EPFEPSEDLIWagesAreThreeDifferentNumbers exists to catch it.
//
// ---------------------------------------------------------------------------
// THE THREE ELECTIONS ARE ORTHOGONAL
// ---------------------------------------------------------------------------
// Base, EmployeeVoluntaryBase and EmployerMatchesVoluntary are three separate
// knobs, not one "voluntary PF" flag. Real employers combine them freely —
// "contribute on the ceiling, but let this employee top up, and no we are not
// matching the top-up" is an ordinary arrangement — and folding them together
// makes that arrangement unrepresentable and silently costs either the employee
// or the employer money every month.
//
// What is STATUTORY and what is VOLUNTARY follows from them, and the tag is
// load-bearing rather than decorative: para 26(6) of the Scheme makes any
// contribution above the ceiling a matter of AGREEMENT, and Form V and the
// remittance report the statutory share separately. Concretely:
//
//   - 12% of min(statutory wage, EPF ceiling)  -> TagStatutory
//   - 12% of the above-ceiling excess, when Base is ACTUAL  -> TagVoluntary
//   - 12% of EmployeeVoluntaryBase (VPF)  -> TagVoluntary
//
// A voluntary contribution credited into the statutory line misstates both the
// remittance and Form V, and the error is not recoverable downstream.
//
// ---------------------------------------------------------------------------
// EDLI AND THE ADMINISTRATIVE CHARGES: DEFERRED WHEN UNNOTIFIED, COMPONENTS
// WHEN NOTIFIED
// ---------------------------------------------------------------------------
// Where the rate row is ConfUnverified — see rates.go for what is and is not on
// record for each of the three, and note that "unverified" is NOT the same claim
// as "unnotified": the EDLI contribution rate is REPORTED at 0.50% from 29 June
// 2026 on secondary sources this build has not been able to check against the
// primary G.S.R., while the two administrative charges are genuinely unknown —
// the amount is NOT derived. PFDerive still
// computes the EDLI WAGES (min(statutory wage, ceiling) is arithmetic, not a
// rate) and emits a Deferral naming the component where the number would have
// gone. Shipping the superseded 1976 Scheme's 0.50% would be inventing a rate,
// and an invented rate is indistinguishable downstream from a correct one.
//
// Where the rate IS notified — every pre-2026 window carries one — the same loop
// emits EDLI_CONTRIBUTION, EDLI_ADMIN_CHARGES and EPF_ADMIN_CHARGES as ordinary
// TagStatutory components, with no other change. That is the design, and it is
// the reason the paragraph below matters to every consumer.
//
// THEY ARE EMPLOYER LIABILITIES THAT NO TOTAL ON THIS STRUCT CARRIES. There is
// NO EDLI or admin-charge amount FIELD on PFResult, so that a caller can never
// mistake an absent contribution for a zero one — but the consequence is that
// EmployerEPF + EPS is NOT the employer's whole provident-fund cost, and a
// consumer that treats it as such loses the EDLI premium and the administrative
// charges entirely. That is not theoretical: the pay run's posting legs credited
// these three to PF Payable while debiting only EmployerEPF + EPS, which made
// every pay run in a window with a notified EDLI rate fail its double-entry
// check by exactly their sum. payrun.go now places every component by name
// (pfPostingSideOf) and refuses a component it has not been taught, so a fourth
// one added here cannot repeat it silently. Any NEW consumer of Components must
// make the same distinction.
//
// EPFAdminChargeMin is DELIBERATELY NOT APPLIED here, in either window. It is a
// monthly minimum for an ESTABLISHMENT, not for an employee: applying a Rs 500
// floor per employee per month would multiply an establishment's admin charge by
// its headcount. The floor belongs to whatever aggregates the establishment's
// remittance, and this leaf must not silently supply half of a rule it cannot
// see the other half of. Documented gap; flagged for the wiring wave.
//
// ---------------------------------------------------------------------------
// TWO STATUTORY NUMBERS LIVE HERE AND NOT IN rates.go — DISCLOSED
// ---------------------------------------------------------------------------
// Every rate and ceiling this file consumes arrives in the
// SocialSecurityRuleSet, carrying its own citation, effective-from date and
// Confidence, and is read through Rate.Use so an unverified row can never be
// silently consumed. TWO EXCEPTIONS, both declared below with their citations
// because SocialSecurityRuleSet has no field for them:
//
//   - deductionCapPct, the s.18(2) Code on Wages, 2019 fifty-per-cent cap on
//     total deductions from wages;
//   - epsNewMemberCutoff, the date on and after which a first-time member above
//     the pension ceiling is excluded from the pension scheme.
//
// Both belong in the ruleset the moment a second effective-dated value for
// either exists; until then a window field carrying one value in both windows
// would be provenance theatre. Flagged for the wiring wave.
package payroll

import (
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// ErrDeductionCapExceeded is returned when the employee's total provident-fund
// deduction for the wage period would exceed the s.18(2) cap.
//
// It is an ERROR and never a silent clamp. Clamping would quietly pay less into
// the member's account than the employee elected, and the employee would find
// out years later from a passbook, with no record anywhere of the decision
// having been taken. Refusing forces the operator to fix the election, which is
// the only correct resolution.
var ErrDeductionCapExceeded = errors.New("payroll: employee deduction exceeds the s.18(2) cap")

// deductionCapPct is the s.18(2) Code on Wages, 2019 ceiling on the TOTAL
// deductions that may be made from an employee's wages in any wage period:
// "the total amount of deductions ... in any wage period from the wages of an
// employee shall not exceed fifty per cent of such wages".
//
// SECONDARY SOURCE, ConfMedium in spirit: the bare text of s.18(2) could not be
// retrieved from this environment. Re-verify before real books. It is applied
// to the STATUTORY WAGE (which is what the section speaks of) and not to the
// EPF wage, which may have been capped at the ceiling and would make the cap
// bite absurdly early on a high earner.
var deductionCapPct = decimal.RequireFromString("50")

// epsNewMemberCutoff is the date on and after which a person FIRST becoming a
// provident-fund member, whose pay at that time exceeds the pension ceiling, is
// not admitted to the Employees' Pension Scheme at all — so the whole employer
// share stays in EPF and no pension accrues.
//
// 1 September 2014, the commencement of G.S.R. 609(E) which both raised the
// pensionable-salary ceiling to Rs 15,000 and closed the scheme to new
// high-wage entrants. SECONDARY SOURCE; the notification could not be read
// here. Re-verify before real books: getting this date or its comparison
// wrong diverts 8.33% of an employee's employer share into a pension they can
// never draw, or denies a pension to someone entitled to one.
var epsNewMemberCutoff = time.Date(2014, time.September, 1, 0, 0, 0, 0, time.UTC)

// pctOfWage returns wage * pct / 100 EXACTLY, and is the ONLY way a percentage
// is applied anywhere in pf.go or esi.go.
//
// Mul then Shift(-2), never Div — the same reasoning wages.go spells out.
// Shifting the decimal exponent by two places is not a division and discards
// nothing, so 15000 * 8.33 becomes exactly 1249.50 rather than a value that
// depends on a working precision. Two consequences that matter:
//
//  1. No rounding happens here at all. Rounding is a statutory act, performed
//     once, by a named helper in rounding.go, at the point the statute says.
//     TestRoundingHelpersAreTheOnlyRounders enforces that mechanically, and a
//     Div here would also have been a second, unauthorised rounding step.
//  2. The result cannot be changed by another package's init(): Div consults
//     decimal.DivisionPrecision, a MUTABLE PACKAGE GLOBAL. What an employee is
//     paid must not be reachable from another package.
func pctOfWage(wage, pct decimal.Decimal) decimal.Decimal {
	return wage.Mul(pct).Shift(-2)
}

// minWage returns the smaller of a and b. Named for its only use — capping a
// wage figure at a ceiling — so a reader at the call site sees the statutory
// act rather than a generic minimum.
func minWage(a, b decimal.Decimal) decimal.Decimal {
	if a.LessThan(b) {
		return a
	}
	return b
}

// Component names. These strings reach the ECR, the payroll register and the
// posting legs, so they are a closed vocabulary and never free text.
const (
	ComponentEPFEmployee          = "EPF_EMPLOYEE"
	ComponentEPFEmployeeVoluntary = "EPF_EMPLOYEE_VOLUNTARY"
	ComponentEPFEmployer          = "EPF_EMPLOYER"
	ComponentEPFEmployerVoluntary = "EPF_EMPLOYER_VOLUNTARY"
	ComponentEPS                  = "EPS_EMPLOYER"
	ComponentEDLIContribution     = "EDLI_CONTRIBUTION"
	ComponentEDLIAdminCharges     = "EDLI_ADMIN_CHARGES"
	ComponentEPFAdminCharges      = "EPF_ADMIN_CHARGES"
)

// StatutoryComponent is one named money row of a social-security derivation,
// carrying the statutory/voluntary distinction the remittance and Form V need.
//
// It is deliberately NOT WageComponent: that type is an INPUT (a line of
// remuneration classified by ComponentType), this one is an OUTPUT (a derived
// contribution classified by ComponentTag). Sharing one struct between the two
// would let an input type leak into a remittance line.
type StatutoryComponent struct {
	// Name is one of the Component* constants above.
	Name string
	// Amount is whole rupees for every component this file produces.
	Amount decimal.Decimal
	// Tag is TagStatutory or TagVoluntary. See the package doc — it decides
	// which line of the remittance and of Form V the amount lands on.
	Tag ComponentTag
}

// PFInput is the complete, self-contained input to PFDerive.
//
// Note what is NOT here, following wages.go: no employee identity, no PAN, no
// UAN, no name. The provident fund is a function of the money and the
// elections, and the leaf is kept unable to see an identifier so it can never
// print, log or hash one.
type PFInput struct {
	// Month is the wage month the wages are payable for. It selects nothing here
	// (the caller has already resolved the ruleset by date); it is carried into
	// the result for traceability.
	Month WageMonth

	// StatutoryWage is WageResult.StatutoryWage from wages.go — the Code on
	// Social Security, 2020 s.2(88) figure INCLUDING the 50% deeming add-back.
	// It is not "basic pay" and it is not gross; computing PF on either is the
	// classic under- or over-contribution bug this whole package exists to
	// prevent.
	StatutoryWage decimal.Decimal

	// Base decides the EPF wage when the statutory wage exceeds the ceiling.
	// There is no default: an empty value is ErrUnknownPFBase, because silently
	// choosing CEILING for an employer who elected ACTUAL under-contributes for
	// every high earner and nobody would see an error.
	Base PFBase

	// EmployeeVoluntaryBase is the ADDITIONAL wage figure the employee elected
	// to contribute on (VPF), at the statutory rate, over and above whatever
	// Base produces. It is a BASE and not an amount, so the election survives a
	// change in the statutory rate without anybody re-entering it.
	EmployeeVoluntaryBase decimal.Decimal

	// EmployerMatchesVoluntary reports whether the employer also contributes on
	// EmployeeVoluntaryBase. It is INDEPENDENT of the employee's election: the
	// employer is under no obligation to match a voluntary contribution, and
	// para 26(6) requires a separate agreement for one. It does NOT govern the
	// above-ceiling excess under PFBaseActual — that excess is the EMPLOYER's
	// own election, expressed by Base, and both sides contribute on it.
	EmployerMatchesVoluntary bool

	// EPSMember reports whether the employee is a member of the Employees'
	// Pension Scheme at all — false for an excluded employee, or for an
	// international worker outside the scheme.
	EPSMember bool

	// FirstMemberOn is the date the employee FIRST became a provident-fund
	// member with ANY employer — not the date of joining this one. A person who
	// first became a member on or after epsNewMemberCutoff, whose pay then
	// exceeded the pension ceiling, was never admitted to the pension scheme. A
	// zero value means "first became a member before the cutoff", so the
	// exclusion does not apply.
	FirstMemberOn time.Time
}

// PFResult is the derived provident-fund position for one employee for one wage
// month, plus an ECHO of everything it used (the house rule
// internal/compliance's results follow).
//
// Every money field is a whole number of rupees: the ECR's fields are integers
// and a paisa in a remittance is a rejected challan.
//
// There is deliberately NO EDLI contribution field and no admin-charge field.
// See the package doc.
type PFResult struct {
	// RuleSetVersion echoes the ruleset that produced these numbers, so a re-run
	// months later can be reproduced exactly.
	RuleSetVersion string
	// BaseApplied echoes the election actually used.
	BaseApplied PFBase
	// CeilingApplied is the EPF wage ceiling in force under this ruleset.
	CeilingApplied decimal.Decimal
	// Month echoes the wage month.
	Month WageMonth

	// EPFWages, EPSWages and EDLIWages are THREE DIFFERENT NUMBERS and map
	// one-to-one onto three separate ECR columns. See the package doc.
	EPFWages  decimal.Decimal
	EPSWages  decimal.Decimal
	EDLIWages decimal.Decimal

	// EmployeeEPF is the employee's TOTAL provident-fund contribution: statutory
	// plus any above-ceiling excess plus any VPF. The split is in Components,
	// and it is the split — not this total — that the remittance reports.
	EmployeeEPF decimal.Decimal
	// EmployerEPF is the employer's total contribution INTO THE MEMBER'S ACCOUNT:
	// the residual left after the pension diversion, plus any above-ceiling
	// excess, plus any voluntary match. The ECR calls the statutory part "EPF-EPS
	// Diff Remitted".
	//
	// IT IS NOT THE EMPLOYER'S WHOLE PF COST. The EDLI premium and the EPF/EDLI
	// administrative charges are employer liabilities that appear ONLY in
	// Components — see the package doc — and EmployerEPF + EPS is short of the
	// employer's real outlay by their sum whenever those rates are notified.
	EmployerEPF decimal.Decimal
	// EPS is the pension diversion out of the employer's statutory share.
	EPS decimal.Decimal

	// EPSMemberApplied is the membership the engine ACTUALLY applied, which is
	// not simply the input: the new-member exclusion can turn a claimed
	// membership off. Echoed so a missing pension contribution explains itself
	// without anybody re-deriving it.
	EPSMemberApplied bool

	// Components carries every money row with its statutory/voluntary tag.
	Components []StatutoryComponent
	// Deferrals names the components this engine deliberately did not compute,
	// each with the reason and the citation of the unnotified rate.
	Deferrals []Deferral

	// PeriodBasis is always BasisMonthly: the EPF ceiling is a wage-month
	// figure, unlike ESI coverage (a contribution period) and income tax
	// (annual). One field could not carry all three, which is why there are
	// three.
	PeriodBasis PeriodBasis
}

// ComponentByName returns the named component row, if the derivation produced
// one.
//
// ABSENCE IS MEANINGFUL. A component that was DEFERRED has no row at all (see
// DeferralFor), which is NOT the same as a component that is legitimately zero.
// A caller that treats "not found" as 0.00 has re-introduced exactly the
// invented number Deferral exists to prevent.
func (r PFResult) ComponentByName(name string) (StatutoryComponent, bool) {
	for _, c := range r.Components {
		if c.Name == name {
			return c, true
		}
	}
	return StatutoryComponent{}, false
}

// DeferralFor returns the deferral recorded for a component, if any.
func (r PFResult) DeferralFor(component string) (Deferral, bool) {
	for _, d := range r.Deferrals {
		if d.Component == component {
			return d, true
		}
	}
	return Deferral{}, false
}

// PFDerive derives the provident-fund position for one employee for one wage
// month under the supplied ruleset. It is pure and deterministic.
//
// ORDER OF OPERATIONS. It is statutory, it is not obvious, and it is pinned by
// TestPFDerive_SyntheticRulesetProvesOrderOfOperations:
//
//  1. Cap the wage FIRST, apply the percentage SECOND. Applying 12% to the full
//     wage and then capping the contribution at 12% of the ceiling happens to
//     agree today only because both use the same rate; it stops agreeing the
//     moment the employee and employer rates diverge.
//  2. Resolve the three wage figures independently, each against its own
//     ceiling, and reduce each to whole rupees before any percentage touches it
//     (the ECR's wage columns are integers and an inspector recomputes the
//     contribution FROM the printed wage).
//  3. Apply the new-member pension exclusion. The scheme says "exceeds", so a
//     wage EXACTLY at the ceiling is still admitted; the cutoff is "on or
//     after", so a member admitted ON the cutoff date IS excluded.
//  4. Round each contribution to the nearest rupee INDIVIDUALLY, then derive
//     the employer's EPF share as rounded(employer total) - rounded(EPS).
//     Deriving it from the unrounded values instead leaves
//     EPF + EPS != the amount actually remitted, which the EPFO rejects.
//  5. Apply the s.18(2) deduction cap to the employee's total.
func PFDerive(in PFInput, rs SocialSecurityRuleSet) (PFResult, error) {
	if rs.Version == "" {
		return PFResult{}, fmt.Errorf("%w: empty social-security ruleset", ErrNoRuleSet)
	}
	if in.StatutoryWage.IsNegative() {
		return PFResult{}, fmt.Errorf("%w: PF statutory wage %s", ErrNegativeStatutoryAmount, in.StatutoryWage.String())
	}
	if in.EmployeeVoluntaryBase.IsNegative() {
		return PFResult{}, fmt.Errorf("%w: PF voluntary base %s", ErrNegativeStatutoryAmount, in.EmployeeVoluntaryBase.String())
	}

	epfCeiling, err := rs.EPFWageCeiling.Use()
	if err != nil {
		return PFResult{}, fmt.Errorf("EPF wage ceiling: %w", err)
	}
	epsCeiling, err := rs.EPSWageCeiling.Use()
	if err != nil {
		return PFResult{}, fmt.Errorf("EPS wage ceiling: %w", err)
	}
	edliCeiling, err := rs.EDLIWageCeiling.Use()
	if err != nil {
		return PFResult{}, fmt.Errorf("EDLI wage ceiling: %w", err)
	}
	employeePct, err := rs.EPFEmployeePct.Use()
	if err != nil {
		return PFResult{}, fmt.Errorf("EPF employee rate: %w", err)
	}
	employerPct, err := rs.EPFEmployerPct.Use()
	if err != nil {
		return PFResult{}, fmt.Errorf("EPF employer rate: %w", err)
	}
	epsPct, err := rs.EPSPct.Use()
	if err != nil {
		return PFResult{}, fmt.Errorf("EPS rate: %w", err)
	}

	// (1) and (2). The statutory base is ALWAYS the wage capped at the EPF
	// ceiling; PFBaseActual adds an above-ceiling EXCESS on top of it rather
	// than replacing it, which is what keeps the statutory and voluntary halves
	// separable for Form V. PFBaseCeiling simply has a zero excess.
	statutoryBase := minWage(in.StatutoryWage, epfCeiling)
	excessBase := decimal.Zero
	switch in.Base {
	case PFBaseCeiling:
		// excessBase stays zero.
	case PFBaseActual:
		excessBase = in.StatutoryWage.Sub(statutoryBase)
	default:
		return PFResult{}, fmt.Errorf("%w: %q", ErrUnknownPFBase, string(in.Base))
	}

	// EPS wages are derived from the STATUTORY WAGE against the PENSION ceiling,
	// never from the EPF wage. Reading them off the EPF wage would make an
	// ACTUAL-base employer divert 8.33% of an uncapped wage into a pension
	// computed on capped pensionable salary — money the member never gets back.
	epsWages := minWage(in.StatutoryWage, epsCeiling)
	edliWages := minWage(in.StatutoryWage, edliCeiling)

	// Whole rupees BEFORE any percentage, per (2) above. RoundNearestRupee is
	// idempotent, so payrollfile/ecr.go rounding again is harmless.
	statutoryBase = RoundNearestRupee(statutoryBase)
	excessBase = RoundNearestRupee(excessBase)
	epsWages = RoundNearestRupee(epsWages)
	edliWages = RoundNearestRupee(edliWages)
	epfWages := statutoryBase.Add(excessBase)

	// (3) The new-member pension exclusion. GreaterThan, never
	// GreaterThanOrEqual: the scheme's word is "exceeds", so somebody on exactly
	// the ceiling IS a member. !Before, never After: the cutoff is "on or
	// after", so somebody first admitted ON the cutoff date IS excluded.
	epsApplies := in.EPSMember
	if epsApplies && !in.FirstMemberOn.IsZero() &&
		!in.FirstMemberOn.Before(epsNewMemberCutoff) &&
		in.StatutoryWage.GreaterThan(epsCeiling) {
		epsApplies = false
	}
	if !epsApplies {
		// Not a pension member: no EPS wages to report at all, and the whole
		// employer share stays in EPF. Reporting the wages but a zero
		// contribution would show the ECR an EPS wage with no EPS against it.
		epsWages = decimal.Zero
	}

	// (4) Each contribution rounded to the nearest rupee individually.
	employeeStatutory := RoundNearestRupee(pctOfWage(statutoryBase, employeePct))
	employerStatutoryTotal := RoundNearestRupee(pctOfWage(statutoryBase, employerPct))
	eps := decimal.Zero
	if epsApplies {
		eps = RoundNearestRupee(pctOfWage(epsWages, epsPct))
	}
	employerStatutory := employerStatutoryTotal.Sub(eps)
	if employerStatutory.IsNegative() {
		// Only reachable from a malformed ruleset — a pension rate above the
		// employer's rate, or a pension ceiling above the EPF ceiling. A refusal
		// rather than a clamp, because a negative employer EPF line posts a debit
		// where the ledger expects a credit and the pay run would still balance.
		return PFResult{}, fmt.Errorf("%w: EPS %s exceeds the employer statutory share %s",
			ErrNegativeStatutoryAmount, eps.String(), employerStatutoryTotal.String())
	}

	// The voluntary halves. The employer follows Base (the above-ceiling excess
	// is the employer's own election and both sides contribute on it) but
	// matches the employee's VPF top-up only when it has agreed to.
	employeeVoluntaryBase := excessBase.Add(RoundNearestRupee(in.EmployeeVoluntaryBase))
	employerVoluntaryBase := excessBase
	if in.EmployerMatchesVoluntary {
		employerVoluntaryBase = employerVoluntaryBase.Add(RoundNearestRupee(in.EmployeeVoluntaryBase))
	}
	employeeVoluntary := decimal.Zero
	if employeeVoluntaryBase.IsPositive() {
		employeeVoluntary = RoundNearestRupee(pctOfWage(employeeVoluntaryBase, employeePct))
	}
	employerVoluntary := decimal.Zero
	if employerVoluntaryBase.IsPositive() {
		employerVoluntary = RoundNearestRupee(pctOfWage(employerVoluntaryBase, employerPct))
	}

	// (5) s.18(2) Code on Wages, 2019.
	employeeTotal := employeeStatutory.Add(employeeVoluntary)
	deductionCap := pctOfWage(in.StatutoryWage, deductionCapPct)
	if employeeTotal.GreaterThan(deductionCap) {
		return PFResult{}, fmt.Errorf("%w: employee PF deduction %s exceeds %s%% of statutory wage %s (cap %s)",
			ErrDeductionCapExceeded, employeeTotal.String(), deductionCapPct.String(),
			in.StatutoryWage.String(), deductionCap.String())
	}

	res := PFResult{
		RuleSetVersion:   rs.Version,
		BaseApplied:      in.Base,
		CeilingApplied:   epfCeiling,
		Month:            in.Month,
		EPFWages:         epfWages,
		EPSWages:         epsWages,
		EDLIWages:        edliWages,
		EmployeeEPF:      employeeTotal,
		EmployerEPF:      employerStatutory.Add(employerVoluntary),
		EPS:              eps,
		EPSMemberApplied: epsApplies,
		PeriodBasis:      BasisMonthly,
	}

	res.Components = append(res.Components,
		StatutoryComponent{Name: ComponentEPFEmployee, Amount: employeeStatutory, Tag: TagStatutory},
		StatutoryComponent{Name: ComponentEPFEmployer, Amount: employerStatutory, Tag: TagStatutory},
	)
	if epsApplies {
		res.Components = append(res.Components,
			StatutoryComponent{Name: ComponentEPS, Amount: eps, Tag: TagStatutory})
	}
	// The voluntary rows exist ONLY when there is a voluntary election. An
	// always-present zero row would be indistinguishable in Form V from a
	// voluntary contribution that was agreed and then computed as zero.
	if employeeVoluntary.IsPositive() {
		res.Components = append(res.Components,
			StatutoryComponent{Name: ComponentEPFEmployeeVoluntary, Amount: employeeVoluntary, Tag: TagVoluntary})
	}
	if employerVoluntary.IsPositive() {
		res.Components = append(res.Components,
			StatutoryComponent{Name: ComponentEPFEmployerVoluntary, Amount: employerVoluntary, Tag: TagVoluntary})
	}

	// The unnotified rows. An unverified rate becomes a NAMED DEFERRAL; only a
	// verified one becomes a component. Nothing here ever invents a number, and
	// the loop is written so that a future notification turns the deferral into
	// a component with no other change.
	for _, u := range []struct {
		name   string
		rate   Rate
		wages  decimal.Decimal
		reason string
	}{
		{
			name:  ComponentEDLIContribution,
			rate:  rs.EDLIContributionPct,
			wages: edliWages,
			// THIS REASON IS LATENT UNDER EVERY SHIPPED RULESET, and deliberately
			// kept. EDLIContributionPct is ConfMedium in ss-2026-07 (S.O. 3581(E))
			// and in both pre-2026 windows, so Use() does not error and this string
			// is never read today. It stays because the rate is notified "from time
			// to time ... taking into account the actuarial valuation of the
			// Insurance Fund" (EDLI Scheme 2026 para 5(2)) — so a future window can
			// legitimately open with the next rate unread, and this is the sentence
			// the employer would then see.
			//
			// Keep it about the STATE, not about a particular figure: the last two
			// versions of this string had to be rewritten because they asserted
			// specifics that changed.
			reason: "EDLI Scheme 2026 para 5(1)-(2): the employer's contribution rate is not fixed in the Scheme and is " +
				"notified separately from time to time on actuarial valuation of the Insurance Fund. The rate applicable to " +
				"this wage month could not be verified from a source this build has, so the premium is DEFERRED rather than " +
				"computed. Do NOT read this as 'no EDLI is payable' — the charge is employer-funded, ceiling-capped at " +
				"Rs 15,000 of wages, and was Rs 75 per member per month at 0.50%; find the current rate notification and " +
				"verify it before the next remittance",
		},
		{
			name:  ComponentEDLIAdminCharges,
			rate:  rs.EDLIAdminChargePct,
			wages: edliWages,
			reason: "EDLI Scheme 2026: NO administrative-charge rate has been notified. This is PENDING, NOT ABOLISHED — " +
				"EPF Scheme 2026 para 29(2) caps a late-return fee at the administrative charges payable for the month, " +
				"which presupposes the charge exists — so the charge is DEFERRED and no amount is booked. Do NOT substitute " +
				"S.O. 2701(E) of 29 May 2026: that sets INSPECTION charges for EXEMPTED establishments, a different levy. " +
				"The pre-2026 account-22 waiver does not survive the scheme it was granted under either",
		},
		{
			name:  ComponentEPFAdminCharges,
			rate:  rs.EPFAdminChargePct,
			wages: epfWages,
			reason: "EPF Scheme 2026: NO administrative-charge rate has been notified — the notifications of 1 July 2026 set " +
				"CONTRIBUTION rates only. This is PENDING, NOT ABOLISHED (see para 29(2), which caps a late-return fee at " +
				"the administrative charges payable for the month), so the charge is DEFERRED and no amount is booked. An " +
				"amount that was Rs 500 per establishment per month under the superseded 1952 Scheme is therefore accruing " +
				"unbooked. Do NOT substitute S.O. 2701(E)'s inspection charges (0.35%, min Rs 8,750) — different levy, " +
				"exempted establishments only",
		},
	} {
		v, err := u.rate.Use()
		if err != nil {
			res.Deferrals = append(res.Deferrals, Deferral{
				Component: u.name,
				Reason:    u.reason,
				Citation:  u.rate.Citation,
			})
			continue
		}
		res.Components = append(res.Components, StatutoryComponent{
			Name:   u.name,
			Amount: RoundNearestRupee(pctOfWage(u.wages, v)),
			Tag:    TagStatutory,
		})
	}

	return res, nil
}
