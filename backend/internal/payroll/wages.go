package payroll

import (
	"fmt"

	"github.com/shopspring/decimal"
)

// StatutoryWages implements the "wages" definition of s.2(88) of the Code on
// Social Security, 2020 — INCLUDING the 50% deeming proviso.
//
// It is its own leaf, upstream of both PF and ESI, because it is where a SILENT
// SYSTEMATIC ERROR would live. Every EPF, EPS, EDLI and ESI figure this engine
// produces is a percentage of the number StatutoryWages returns; if that number
// is wrong by a component, every contribution for every employee is wrong by the
// same proportion, in the same direction, every month, and each individual
// payslip still looks internally consistent.
//
// THE RULE, in three steps:
//
//  1. "Wages" means all remuneration expressed in terms of money, INCLUDING
//     basic pay, dearness allowance and retaining allowance, but EXCLUDING an
//     exhaustive list — HRA, conveyance allowance, overtime, commission, bonus
//     payable under any law, the value of house accommodation and amenities,
//     sums to defray special expenses, remuneration under an award or
//     settlement, the employer's own PF/pension contribution, gratuity and
//     retrenchment compensation.
//
//  2. THE LIST IS EXHAUSTIVE. Anything not on it IS wages. This is the point the
//     Supreme Court settled for the 1952 Act in Regional PF Commissioner v.
//     Vivekananda Vidyamandir (2019): an allowance paid universally, necessarily
//     and ordinarily to all employees is basic wages whatever it is called. So
//     "special allowance" and any unclassified "other allowance" are WAGES here,
//     and a component type outside the closed vocabulary is an ERROR
//     (ErrUnknownComponentType) rather than a silent exclusion.
//
//  3. THE 50% DEEMING PROVISO. If the payments made under clauses (a) to (i)
//     exceed one-half of all remuneration, the excess is deemed remuneration and
//     added back into wages. Algebraically this makes the result
//
//     statutory wage = max(included, DeemingThresholdPct% of total remuneration)
//
//     whenever every exclusion in the month falls inside (a)-(i) — the invariant
//     TestStatutoryWages_FiftyPercentDeemingAddsBack asserts independently of the
//     add-back arithmetic. It is the whole reason the "structure the CTC as 20%
//     basic and 80% special allowance" trick does not work.
//
// WHICH EXCLUSIONS COUNT TOWARD THE 50% TEST — SETTLED, and it is not all of
// them. Proviso 1 to s.2(88), Code on Social Security, 2020:
//
//	"if payments made by the employer to the employee under clauses (a) to (i)
//	exceeds one-half, or such other per cent. as may be notified by the Central
//	Government, of the all remuneration calculated under this clause, the amount
//	which exceeds such one-half ... shall be deemed as remuneration and shall be
//	accordingly added in wages under this clause"
//
// The enumeration STOPS AT (i). Clause (j) — "any gratuity payable on the
// termination of employment" — and clause (k) — "any retrenchment compensation
// or other retirement benefit payable to the employee or any ex gratia payment
// made to him on the termination of employment" — are excluded from wages AND
// take no part in the "exceeds one-half" test. They are never added back.
//
// So there are TWO sets here and they are deliberately not one map with a flag:
// excludedFromWages (all of (a)-(k)) decides what is NOT a wage, and
// deemingTestClauses ((a)-(i) only) is the ONLY sum measured against the floor.
// An earlier cut of this file tested the whole exclusion sum, on the reasoning
// that a bigger sum over-contributes and over-contributing is the safe
// direction. It is not safe and it is not small: a leaver's final payslip
// carrying Rs 5,00,000 of gratuity and Rs 2,00,000 of retrenchment compensation
// on a Basic of 30,000 produced a statutory wage of Rs 3,72,500 instead of
// Rs 30,000, so Rs 41,100 was deducted from the departing employee's final
// salary and remitted to the EPFO on money the statute says is not a wage. It
// fired on every terminal settlement, which is the normal case for a leaver. The
// ESI leg is worse in kind because the ESI contribution is uncapped once the
// coverage latch is on.
//
// DOCUMENTED GAP — REMUNERATION IN KIND. s.2(88) also deems remuneration in kind
// up to fifteen per cent of the total wages payable to form part of wages. That
// proviso is SELF-REFERENTIAL (the cap is a share of the very total it feeds)
// and getting it half-right is worse than not having it, so v1 does NOT model
// it: every component here is money. There is no in-kind field on WageInput and
// no in-kind rate in the ruleset, precisely so that nobody can believe it is
// handled.
func StatutoryWages(in WageInput, rs SocialSecurityRuleSet) (WageResult, error) {
	if rs.Version == "" {
		return WageResult{}, fmt.Errorf("%w: empty social-security ruleset", ErrNoRuleSet)
	}

	total := decimal.Zero
	included := decimal.Zero
	excluded := decimal.Zero
	// inDeemingTest is the clause (a)-(i) subset of excluded. It is a SEPARATE
	// accumulator and not a filter applied to excluded afterwards, so that the
	// two sums can never be confused for one another downstream.
	inDeemingTest := decimal.Zero

	for i, c := range in.Components {
		if _, err := ComponentTypeFor(string(c.Type)); err != nil {
			return WageResult{}, fmt.Errorf("component %d: %w", i, err)
		}
		// A negative earning is not a wage adjustment, it is a recovery, and the
		// statutory roundings downstream do not cover negatives (see
		// RoundNearestRupee's sign note). Refuse rather than round one.
		if c.Amount.IsNegative() {
			return WageResult{}, fmt.Errorf("%w: component %d (%s) is %s",
				ErrNegativeStatutoryAmount, i, c.Type, c.Amount.String())
		}
		total = total.Add(c.Amount)
		if excludedFromWages[c.Type] {
			excluded = excluded.Add(c.Amount)
			if deemingTestClauses[c.Type] {
				inDeemingTest = inDeemingTest.Add(c.Amount)
			}
		} else {
			included = included.Add(c.Amount)
		}
	}

	thresholdPct, err := rs.DeemingThresholdPct.Use()
	if err != nil {
		return WageResult{}, fmt.Errorf("wage deeming threshold: %w", err)
	}

	// floor = thresholdPct% of all remuneration. The clause (a)-(i) payments may
	// occupy up to this much before the excess re-enters wages. Note the
	// ASYMMETRY the proviso itself writes: the FLOOR is a share of "all
	// remuneration calculated under this clause", i.e. every component in the
	// month INCLUDING gratuity and retrenchment compensation, while the sum
	// TESTED against it is only clauses (a)-(i). A terminal payment therefore
	// raises the floor and can only ever REDUCE the add-back — it can never
	// create one.
	//
	// Mul then Shift(-2), never Div: dividing by one hundred is EXACTLY a shift
	// of the decimal point, so the floor is exact and no rounding enters here at
	// all. Div would have consulted decimal.DivisionPrecision, a MUTABLE PACKAGE
	// GLOBAL that any other package's init() could change — the hazard
	// internal/inventory's rateScale comment spells out — and it would have
	// introduced a rounding step the statute does not authorise, at the exact
	// point where a paisa decides whether the add-back triggers.
	floor := total.Mul(thresholdPct).Shift(-2)

	addBack := decimal.Zero
	if inDeemingTest.GreaterThan(floor) {
		addBack = inDeemingTest.Sub(floor)
	}

	res := WageResult{
		Month:                           in.Month,
		TotalRemuneration:               RoundPaise(total),
		IncludedComponents:              RoundPaise(included),
		ExcludedComponents:              RoundPaise(excluded),
		ExcludedComponentsInDeemingTest: RoundPaise(inDeemingTest),
		DeemingThresholdPct:             thresholdPct,
		DeemedAddBack:                   RoundPaise(addBack),
		StatutoryWage:                   RoundPaise(included.Add(addBack)),
		RuleSetVersion:                  rs.Version,
		PeriodBasis:                     BasisMonthly,
	}
	return res, nil
}

// excludedFromWages is the s.2(88) exclusion list, and it is a DENYLIST on
// purpose: the statute's list is exhaustive, so the safe default for anything
// not on it is INCLUSION. An allowlist would silently exclude every component
// type added later, which is the under-contribution this whole file exists to
// prevent.
//
// Each entry names the exclusion in the statute's own words AND its clause
// letter. The letters are load-bearing, not decoration: proviso 1 tests only
// clauses (a) to (i), so which letter a component carries decides whether it can
// trigger a deeming add-back. See deemingTestClauses immediately below.
// [CITED-PRIMARY] Code on Social Security, 2020 s.2(88) clauses (a)-(k), in
// force from 21 November 2025 per S.O. 5319(E) — the same instrument cited on
// SocialSecurityRuleSet.DeemingThresholdPct in rates.go.
var excludedFromWages = map[ComponentType]bool{
	// (a) "any bonus payable under any law for the time being in force, which
	// does not form part of the remuneration payable under the terms of
	// employment"
	ComponentBonus: true,
	// (b) "the value of any house-accommodation, or of the supply of light,
	// water, medical attendance or other amenity"
	ComponentHouseAccommodationValue: true,
	// (c) "the contribution paid by the employer to any pension or provident
	// fund, and the interest which may have accrued thereon"
	ComponentEmployerPFContribution: true,
	// (d) "any conveyance allowance or the value of any travelling concession"
	ComponentConveyanceAllowance: true,
	// (e) "any sum paid to the employed person to defray special expenses
	// entailed on him by the nature of his employment"
	ComponentSpecialExpenses: true,
	// (f) "house rent allowance"
	ComponentHouseRentAllowance: true,
	// (g) "remuneration payable under any award or settlement between the
	// parties or order of a court or Tribunal"
	ComponentAwardOrSettlement: true,
	// (h) "any overtime allowance"
	ComponentOvertimeAllowance: true,
	// (i) "any commission payable to the employee"
	ComponentCommission: true,
	// --- BELOW THE (a)-(i) LINE: outside the deeming test ---
	// (j) "any gratuity payable on the termination of employment"
	ComponentGratuity: true,
	// (k) "any retrenchment compensation or other retirement benefit payable to
	// the employee or any ex gratia payment made to him on the termination of
	// employment"
	ComponentRetrenchmentCompensation: true,
	ComponentExGratia:                 true,
}

// deemingTestClauses is clauses (a) to (i) ONLY — the sole sum proviso 1 to
// s.2(88) measures against the one-half floor.
//
// It is a proper subset of excludedFromWages: every member here is also excluded
// from wages, but (j) gratuity and (k) retrenchment compensation / other
// retirement benefit / ex gratia are NOT here, because the proviso's enumeration
// stops at (i). Those three are terminal payments. Including them made every
// leaver's final settlement fabricate an add-back out of their gratuity — see
// the StatutoryWages doc for the Rs 41,100 that came out of one departing
// employee's last payslip.
//
// This map is written out in full rather than derived from excludedFromWages by
// deleting three keys. A derivation would silently re-admit any exclusion added
// later, and a new clause (l) is exactly the kind of thing an amendment adds.
// TestStatutoryWages_DeemingTestCoversClausesAToI asserts the two sets differ by
// precisely those three members.
//
// [CITED-PRIMARY] Code on Social Security, 2020, proviso 1 to s.2(88): "if
// payments made by the employer to the employee under clauses (a) to (i) exceeds
// one-half, or such other per cent. as may be notified by the Central
// Government, of the all remuneration calculated under this clause, the amount
// which exceeds such one-half ... shall be deemed as remuneration and shall be
// accordingly added in wages under this clause". In force from 21 November 2025
// per S.O. 5319(E) dated 21.11.2025.
var deemingTestClauses = map[ComponentType]bool{
	ComponentBonus:                   true, // (a)
	ComponentHouseAccommodationValue: true, // (b)
	ComponentEmployerPFContribution:  true, // (c)
	ComponentConveyanceAllowance:     true, // (d)
	ComponentSpecialExpenses:         true, // (e)
	ComponentHouseRentAllowance:      true, // (f)
	ComponentAwardOrSettlement:       true, // (g)
	ComponentOvertimeAllowance:       true, // (h)
	ComponentCommission:              true, // (i)
}

// WageComponent is one line of a month's remuneration.
type WageComponent struct {
	// Type is a member of the closed ComponentType vocabulary. A type outside it
	// is ErrUnknownComponentType, never a silent exclusion.
	Type ComponentType
	// Amount is the money paid or payable for the wage month, NUMERIC(19,4).
	// It must not be negative — see ErrNegativeStatutoryAmount.
	Amount decimal.Decimal
}

// WageInput is the complete, self-contained input to StatutoryWages.
//
// Note what is NOT here: no employee identity, no PAN, no UAN, no name. The
// statutory wage is a function of the money and nothing else, and the leaf is
// kept unable to see an identifier so it can never print or log one.
type WageInput struct {
	// Month is the wage month the components are payable for. It is carried into
	// the result for traceability and is not used in the arithmetic.
	Month WageMonth
	// Components is the month's remuneration, one line per component.
	Components []WageComponent
}

// WageResult is the derived statutory wage plus an ECHO of everything it used —
// the house rule internal/compliance's results follow. The echo is what lets a
// payslip explain itself years later without re-running the engine, and what
// lets the golden corpus assert the intermediate steps rather than only the
// final number.
type WageResult struct {
	// Month echoes the wage month.
	Month WageMonth
	// TotalRemuneration is every component, included and excluded.
	TotalRemuneration decimal.Decimal
	// IncludedComponents is the sum of the components that are wages on their
	// own terms, BEFORE any deeming add-back.
	IncludedComponents decimal.Decimal
	// ExcludedComponents is the sum of ALL the s.2(88) exclusions, clauses (a)
	// to (k). It is what is NOT a wage.
	ExcludedComponents decimal.Decimal
	// ExcludedComponentsInDeemingTest is the clause (a)-(i) part of the above —
	// the ONLY sum proviso 1 measures against the one-half floor.
	//
	// Both sums are echoed because the difference between them is the whole
	// content of the proviso's "under clauses (a) to (i)", and a payslip that
	// prints only one of them cannot explain years later why a leaver's Rs
	// 5,00,000 gratuity produced no add-back. When they differ, the gap is
	// exactly the month's gratuity, retrenchment compensation and ex gratia.
	ExcludedComponentsInDeemingTest decimal.Decimal
	// DeemingThresholdPct echoes the threshold actually applied (50 under the
	// Code, 100 in the pre-Code window, where it disables the add-back).
	DeemingThresholdPct decimal.Decimal
	// DeemedAddBack is the excess of the exclusions over the threshold, added
	// back into wages. Zero when the proviso does not bite.
	DeemedAddBack decimal.Decimal
	// StatutoryWage is IncludedComponents + DeemedAddBack: the base every PF,
	// EPS, EDLI and ESI figure is computed on.
	StatutoryWage decimal.Decimal
	// RuleSetVersion echoes the ruleset the wage was derived under.
	RuleSetVersion string
	// PeriodBasis is always BasisMonthly: the statutory wage is a wage-month
	// quantity, never an annual one.
	PeriodBasis PeriodBasis
}
