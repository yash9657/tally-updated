package payroll

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// ---------------------------------------------------------------------------
// WHY THE RATES LIVE IN GO AND NOT IN A MIGRATION
// ---------------------------------------------------------------------------
// The obvious alternative — rate rows in migration 0034, read at run time — was
// considered and REJECTED, for four reasons in descending weight:
//
//  1. The pure engine would need a database import to read them, which destroys
//     the whole point of a pure leaf. The only way out is a loader that reads
//     them INTO the engine, at which point the "table" is just a slower Go table
//     with no reviewer.
//  2. Rates are LAW. They are identical for every tenant. A company_id-scoped
//     table means two companies can compute different statutory tax from the same
//     wage, and there is no error path for that — it is simply a wrong number in
//     one tenant's books.
//  3. A migration's INSERT is not covered by the corpus oracle: corpora/
//     validate.py re-derives the arithmetic in Python and cannot re-derive
//     against SQL. Moving the rates into SQL would delete the duplication that IS
//     the oracle.
//  4. A Go table changes in a diff, in front of a reviewer, next to the citation
//     it rests on.
//
// What DOES go in the database (0034): the resolved ruleset VERSION STRING
// stamped on the pay_run row, so a re-run is reproducible, and the per-employee
// ELECTIONS (regime, PF base, VPF), which are genuinely company data and
// genuinely effective-dated.
//
// ---------------------------------------------------------------------------
// TWO INDEPENDENT WINDOW TABLES, NOT ONE BUNDLE
// ---------------------------------------------------------------------------
// Changes of social-security law land in the MIDDLE of tax years: the four
// Labour Codes commenced on 21 Nov 2025 (inside FY 2025-26) and the
// EPF/EPS/EDLI Schemes 2026 on 29 Jun 2026 (inside FY 2026-27), while the
// Income-tax Act 2025 runs from 1 Apr 2026. A single bundle keyed by date would
// force the income-tax ruleset to split on those scheme dates too, which is
// wrong — income tax is computed on an ANNUAL base and its ruleset must be
// CONSTANT across the tax year, or April's estimate and March's estimate stop
// being comparable and the year's deductions do not add up to the year's tax.
//
// So: social security resolves BY DATE (the last day of the wage month), income
// tax resolves BY TAX YEAR VALUE.

// window is one effective-dated ruleset over a HALF-OPEN [From, To) date range.
//
// Half-open is copied in spirit from migration 0030's price lists, which is the
// only real effective-dating mechanism already in this repo, and it is not a
// stylistic choice. With inclusive-on-both-ends ranges, adjacent windows
// [Apr 1, Jul 1] and [Jul 1, Oct 1] BOTH contain 1 July, and which ruleset a
// 1 July pay run resolves to then depends on iteration order. With [From, To)
// the boundary date belongs to exactly one window, always the later one.
//
// A zero To means OPEN-ENDED. Only the last window in a slice may be
// open-ended, and TestRateWindowsAreNonOverlappingAndGapFree enforces both that
// and the absence of gaps.
type window[T any] struct {
	From    time.Time
	To      time.Time // zero = open-ended
	Version string
	Set     T
}

// covers reports whether d falls in [From, To).
func (w window[T]) covers(d time.Time) bool {
	if d.Before(w.From) {
		return false
	}
	if w.To.IsZero() {
		return true
	}
	return d.Before(w.To)
}

// ---------------------------------------------------------------------------
// Social security: the wage definition, EPF/EPS/EDLI, and ESI.
// ---------------------------------------------------------------------------

// SocialSecurityRuleSet is everything the wage, PF and ESI leaves need for one
// effective-dated period. Every field is a Rate — including the ceilings and the
// daily limit — so that every statutory number in it carries a citation, an
// effective-from date and a confidence, and so that the reflect-walking
// provenance test cannot be evaded by declaring a number as a bare decimal.
type SocialSecurityRuleSet struct {
	// Version is the ruleset identifier stamped on the pay_run row, e.g.
	// "ss-2026-07".
	Version string

	// --- The wage definition (Code on Social Security, 2020 s.2(88)) ---

	// DeemingThresholdPct is the share of ALL REMUNERATION that the EXCLUDED
	// components may occupy before the excess is deemed back into wages. 50 under
	// the Code. A value of 100 means "no cap" and therefore no add-back, which is
	// how the pre-Code period is expressed without a special case in wages.go.
	DeemingThresholdPct Rate

	// --- EPF / EPS / EDLI ---

	// EPFEmployeePct and EPFEmployerPct are the two 12% shares.
	EPFEmployeePct Rate
	EPFEmployerPct Rate
	// EPSPct is the slice of the EMPLOYER's share diverted to the pension scheme.
	EPSPct Rate
	// EPFWageCeiling, EPSWageCeiling and EDLIWageCeiling are monthly wage
	// ceilings. They are three separate fields even though all three are 15,000
	// today, because they are three separate notifications under three separate
	// schemes and nothing guarantees they move together.
	EPFWageCeiling  Rate
	EPSWageCeiling  Rate
	EDLIWageCeiling Rate
	// EDLIContributionPct, EDLIAdminChargePct, EPFAdminChargePct and
	// EPFAdminChargeMin are ConfUnverified in the 2026 ruleset. See the block
	// comment on ss2026 below — this is the honest handling of a rate that the
	// scheme itself says will be notified separately.
	EDLIContributionPct Rate
	EDLIAdminChargePct  Rate
	EPFAdminChargePct   Rate
	EPFAdminChargeMin   Rate

	// --- ESI ---

	ESIEmployeePct Rate
	ESIEmployerPct Rate
	// ESIWageCeiling is the MONTHLY wage ceiling for coverage. It is tested at
	// the START of a contribution period and LATCHED (BasisContributionPeriod),
	// never re-tested monthly.
	ESIWageCeiling Rate
	// ESILowWageDailyLimit is the average daily wage at or below which the
	// EMPLOYEE's share is exempt. The EMPLOYER's share remains payable, and
	// getting that backwards costs the employee 0.75% of their wage.
	ESILowWageDailyLimit Rate
}

// ---------------------------------------------------------------------------
// Income tax.
// ---------------------------------------------------------------------------

// Regime is the closed vocabulary of income-tax regimes.
type Regime string

const (
	// RegimeNew is the s.202 (ex-115BAC) default regime.
	RegimeNew Regime = "NEW"
	// RegimeOld is the legacy regime, available only on an express election.
	RegimeOld Regime = "OLD"
)

// DefaultRegime is the regime that applies when an employee has made no
// election: s.202 of the Income-tax Act 2025 (ex-115BAC(1A)) makes the new
// regime the DEFAULT, and the old regime available only by opting out.
//
// The constant lives here so payrolldb, which owns all defaulting, does not have
// to spell the string itself. RegimeFor deliberately does NOT apply it: an empty
// regime reaching the engine means a caller forgot, and silently taxing under the
// new regime somebody who elected the old one is a wrong-number bug with a
// plausible-looking answer. The engine defaults nothing.
const DefaultRegime = RegimeNew

// RegimeFor maps a stored string to a Regime, or returns ErrUnknownRegime.
//
// The default arm ERRORS. It is the ValuerFor idiom: when a third regime appears
// (or a typo lands in the elections table), every deployment that has not shipped
// the arm must fail loudly rather than quietly taxing that employee under
// whichever regime happened to be first in the switch.
func RegimeFor(s string) (Regime, error) {
	switch Regime(s) {
	case RegimeNew:
		return RegimeNew, nil
	case RegimeOld:
		return RegimeOld, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownRegime, s)
	}
}

// PFBase is the closed vocabulary of provident-fund contribution bases.
type PFBase string

const (
	// PFBaseCeiling contributes on min(statutory wage, EPF wage ceiling) — the
	// statutory minimum.
	PFBaseCeiling PFBase = "CEILING"
	// PFBaseActual contributes on the full statutory wage, above the ceiling. It
	// is an election, and the part above the ceiling is TagVoluntary.
	PFBaseActual PFBase = "ACTUAL"
)

// PFBaseFor maps a stored string to a PFBase, or returns ErrUnknownPFBase. The
// default arm errors, for the same reason RegimeFor's does.
func PFBaseFor(s string) (PFBase, error) {
	switch PFBase(s) {
	case PFBaseCeiling:
		return PFBaseCeiling, nil
	case PFBaseActual:
		return PFBaseActual, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownPFBase, s)
	}
}

// ComponentType is the closed vocabulary of salary components.
//
// The vocabulary is closed and the selector errors because the classification in
// wages.go turns on it: s.2(88) of the Code on Social Security, 2020 lists the
// exclusions EXHAUSTIVELY, and everything not listed IS wages. A component type
// that silently fell through to "excluded" would under-contribute PF and ESI for
// the whole of that employee's service, and nobody would ever see an error.
type ComponentType string

const (
	// --- Components that ARE wages under s.2(88) ---

	// ComponentBasic is basic pay.
	ComponentBasic ComponentType = "BASIC"
	// ComponentDearnessAllowance is dearness allowance, expressly included.
	ComponentDearnessAllowance ComponentType = "DA"
	// ComponentRetainingAllowance is retaining allowance, expressly included.
	ComponentRetainingAllowance ComponentType = "RETAINING_ALLOWANCE"
	// ComponentSpecialAllowance is a special allowance paid universally,
	// necessarily and ordinarily to all employees. It is NOT one of the s.2(88)
	// exclusions and therefore IS wages — the holding in Regional PF
	// Commissioner v. Vivekananda Vidyamandir (SC, 2019), which was the single
	// most common PF under-contribution before it.
	ComponentSpecialAllowance ComponentType = "SPECIAL_ALLOWANCE"
	// ComponentOtherAllowance is any other allowance not on the exclusion list.
	// It is wages, which is the SAFE default direction: over-contributing credits
	// the employee, under-contributing is a shortfall with interest and damages.
	ComponentOtherAllowance ComponentType = "OTHER_ALLOWANCE"

	// --- Components EXCLUDED from wages by s.2(88) ---

	// ComponentHouseRentAllowance is HRA.
	ComponentHouseRentAllowance ComponentType = "HRA"
	// ComponentConveyanceAllowance is conveyance allowance.
	ComponentConveyanceAllowance ComponentType = "CONVEYANCE_ALLOWANCE"
	// ComponentOvertimeAllowance is overtime allowance.
	ComponentOvertimeAllowance ComponentType = "OVERTIME_ALLOWANCE"
	// ComponentCommission is commission payable to the employee.
	ComponentCommission ComponentType = "COMMISSION"
	// ComponentBonus is a bonus payable under any law for the time being in
	// force (the Payment of Bonus Act, 1965).
	ComponentBonus ComponentType = "BONUS"
	// ComponentHouseAccommodationValue is the value of house accommodation, or
	// of the supply of light, water, medical attendance or other amenity.
	ComponentHouseAccommodationValue ComponentType = "HOUSE_ACCOMMODATION_VALUE"
	// ComponentSpecialExpenses is a sum paid to defray special expenses entailed
	// by the nature of the employment.
	ComponentSpecialExpenses ComponentType = "SPECIAL_EXPENSES"
	// ComponentAwardOrSettlement is remuneration payable under an award or
	// settlement between the parties or an order of a court or tribunal.
	ComponentAwardOrSettlement ComponentType = "AWARD_OR_SETTLEMENT"
	// ComponentEmployerPFContribution is the employer's own contribution to a
	// provident fund or pension scheme.
	ComponentEmployerPFContribution ComponentType = "EMPLOYER_PF_CONTRIBUTION"
	// ComponentGratuity is gratuity payable on termination.
	ComponentGratuity ComponentType = "GRATUITY"
	// ComponentRetrenchmentCompensation is retrenchment compensation or other
	// retirement benefit.
	ComponentRetrenchmentCompensation ComponentType = "RETRENCHMENT_COMPENSATION"
	// ComponentExGratia is an ex gratia payment.
	ComponentExGratia ComponentType = "EX_GRATIA"
)

// componentTypes is the closed vocabulary, in a form the selector and the tests
// can both walk.
var componentTypes = map[ComponentType]struct{}{
	ComponentBasic:                    {},
	ComponentDearnessAllowance:        {},
	ComponentRetainingAllowance:       {},
	ComponentSpecialAllowance:         {},
	ComponentOtherAllowance:           {},
	ComponentHouseRentAllowance:       {},
	ComponentConveyanceAllowance:      {},
	ComponentOvertimeAllowance:        {},
	ComponentCommission:               {},
	ComponentBonus:                    {},
	ComponentHouseAccommodationValue:  {},
	ComponentSpecialExpenses:          {},
	ComponentAwardOrSettlement:        {},
	ComponentEmployerPFContribution:   {},
	ComponentGratuity:                 {},
	ComponentRetrenchmentCompensation: {},
	ComponentExGratia:                 {},
}

// ComponentTypeFor maps a stored string to a ComponentType, or returns
// ErrUnknownComponentType. The default arm errors — see the type's doc for the
// under-contribution this prevents.
func ComponentTypeFor(s string) (ComponentType, error) {
	ct := ComponentType(s)
	if _, ok := componentTypes[ct]; !ok {
		return "", fmt.Errorf("%w: %q", ErrUnknownComponentType, s)
	}
	return ct, nil
}

// AgeBand selects the old regime's basic exemption limit. It has no effect under
// the new regime, whose exemption does not vary by age.
type AgeBand string

const (
	// AgeBelow60 is an individual under 60 at any time in the tax year.
	AgeBelow60 AgeBand = "BELOW_60"
	// AgeSenior is a resident senior citizen (60 or more, under 80).
	AgeSenior AgeBand = "SENIOR"
	// AgeSuperSenior is a resident very senior citizen (80 or more).
	AgeSuperSenior AgeBand = "SUPER_SENIOR"
)

// Slab is one income-tax rate band over a HALF-OPEN [From, To) income range, in
// rupees. A zero To means the top, open-ended band.
//
// Half-open for the same reason the date windows are: with inclusive bounds,
// income of exactly 8,00,000 falls in two bands and the answer depends on
// iteration order.
type Slab struct {
	From decimal.Decimal
	To   decimal.Decimal // zero = open-ended top band
	Rate Rate
}

// SurchargeBand is a surcharge rate that applies when TOTAL INCOME EXCEEDS
// Above. Bands are ordered ascending; the highest one whose Above is exceeded
// wins.
type SurchargeBand struct {
	Above decimal.Decimal
	Rate  Rate
}

// IncomeTaxRuleSet is everything incometax.go needs for one tax year.
type IncomeTaxRuleSet struct {
	// Version is the ruleset identifier stamped on the pay_run row, e.g.
	// "it-ty-2026-27".
	Version string
	// TaxYear is the year this ruleset is FOR. IncomeTaxFor cross-checks it
	// against the requested year, so a mis-ordered window table cannot silently
	// serve the wrong year's slabs.
	TaxYear TaxYear

	// NewRegimeSlabs is the s.202 slab table.
	NewRegimeSlabs []Slab
	// OldRegimeSlabs is keyed by AgeBand because the old regime's basic
	// exemption varies with age. Use SlabsFor rather than indexing it: a missing
	// key yields a nil slice, and a nil slab table computes a tax of zero.
	OldRegimeSlabs map[AgeBand][]Slab

	// StandardDeductionNew and StandardDeductionOld are the salary standard
	// deductions. They differ between regimes and that difference is exactly the
	// sort of thing a single field would erase.
	StandardDeductionNew Rate
	StandardDeductionOld Rate

	// RebateLimitNew / RebateMaxNew and RebateLimitOld / RebateMaxOld are the
	// s.156 (ex-87A) rebate. The NEW regime's rebate carries MARGINAL RELIEF at
	// its cliff, which is a separate mechanism from the surcharge marginal relief
	// below and is NOT optional: implementing only the surcharge relief
	// over-taxes every employee in the 12.00-12.75 lakh band.
	RebateLimitNew Rate
	RebateMaxNew   Rate
	RebateLimitOld Rate
	RebateMaxOld   Rate

	// SurchargeBandsNew and SurchargeBandsOld differ in their top band: the new
	// regime caps surcharge at 25% and has no 37% band.
	SurchargeBandsNew []SurchargeBand
	SurchargeBandsOld []SurchargeBand

	// CessPct is the Health and Education Cess on (tax + surcharge).
	CessPct Rate
}

// SlabsFor returns the slab table for a regime and age band.
//
// It exists so no caller indexes OldRegimeSlabs directly: a map miss returns a
// nil slice, and a nil slab table produces a tax of ZERO with no error — a
// full-year under-deduction that looks exactly like a correctly exempt employee.
func (rs IncomeTaxRuleSet) SlabsFor(regime Regime, band AgeBand) ([]Slab, error) {
	switch regime {
	case RegimeNew:
		if len(rs.NewRegimeSlabs) == 0 {
			return nil, fmt.Errorf("%w: ruleset %q carries no new-regime slab table", ErrNoRuleSet, rs.Version)
		}
		return rs.NewRegimeSlabs, nil
	case RegimeOld:
		slabs, ok := rs.OldRegimeSlabs[band]
		if !ok || len(slabs) == 0 {
			return nil, fmt.Errorf("%w: ruleset %q carries no old-regime slab table for age band %q", ErrNoRuleSet, rs.Version, band)
		}
		return slabs, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownRegime, regime)
	}
}

// SurchargeBandsFor returns the surcharge table for a regime.
func (rs IncomeTaxRuleSet) SurchargeBandsFor(regime Regime) ([]SurchargeBand, error) {
	switch regime {
	case RegimeNew:
		return rs.SurchargeBandsNew, nil
	case RegimeOld:
		return rs.SurchargeBandsOld, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownRegime, regime)
	}
}

// ---------------------------------------------------------------------------
// Resolvers.
// ---------------------------------------------------------------------------

// SocialSecurityFor returns the social-security ruleset in force for a wage
// month, resolved on the month's LAST DAY (pass WageMonth.Last(), or use
// SocialSecurityForMonth).
//
// The last day is the right key because a scheme that comes into force on a
// calendar date mid-month governs the wages payable FOR that whole month.
//
// A date no window covers is an ERROR. It NEVER falls back to the newest window
// and NEVER to the oldest. This is inventory.ValuerFor's reasoning applied to
// time: a June 2019 pay run computed under July 2026 rates is a plausible,
// wrong, un-auditable number that nobody would ever query — and it is exactly
// the bug the compliance path's DefaultTDSRulesetVersion still has.
func SocialSecurityFor(wageMonthEnd time.Time) (SocialSecurityRuleSet, error) {
	d := dateUTC(wageMonthEnd.Year(), wageMonthEnd.Month(), wageMonthEnd.Day())
	for _, w := range socialSecurityWindows {
		if w.covers(d) {
			return w.Set, nil
		}
	}
	return SocialSecurityRuleSet{}, fmt.Errorf("%w: no social-security ruleset covers wage month %s", ErrNoRuleSet, d.Format("2006-01"))
}

// SocialSecurityForMonth is SocialSecurityFor keyed by a WageMonth. It exists so
// that no caller has to remember that the resolution date is the month's LAST
// day; passing First() would resolve a July-effective scheme change to the June
// ruleset for the whole of July.
func SocialSecurityForMonth(m WageMonth) (SocialSecurityRuleSet, error) {
	return SocialSecurityFor(m.Last())
}

// IncomeTaxFor returns the income-tax ruleset for a tax year.
//
// It resolves on the TAX YEAR VALUE, not on a date, so the ruleset is constant
// for the whole year even when a scheme change lands inside it. An uncovered
// year is an ERROR for the same reason an uncovered wage month is.
//
// Note the deliberate asymmetry with SocialSecurityFor: the last social-security
// window is OPEN-ENDED, because a scheme stays in force until it is amended, but
// the income-tax windows are all CLOSED, because the rates are re-enacted every
// year by a Finance Act. Running FY 2028-29 payroll under FY 2026-27's slabs is
// not a conservative approximation; it is a guess about an Act that did not
// exist when this table was written.
func IncomeTaxFor(ty TaxYear) (IncomeTaxRuleSet, error) {
	for _, w := range incomeTaxWindows {
		if w.covers(ty.Start()) {
			if w.Set.TaxYear != ty {
				return IncomeTaxRuleSet{}, fmt.Errorf("%w: ruleset %q is for tax year %s, not %s",
					ErrNoRuleSet, w.Version, w.Set.TaxYear, ty)
			}
			return w.Set, nil
		}
	}
	return IncomeTaxRuleSet{}, fmt.Errorf("%w: no income-tax ruleset covers tax year %s", ErrNoRuleSet, ty)
}

// ---------------------------------------------------------------------------
// THE TABLES.
//
// Read the RATE PROVENANCE banner in payroll.go before changing anything below.
// Every Value is a decimal STRING literal; there is not a float anywhere in this
// package and TestRatesAreDecimalStringsNotFloats fails the build if one appears.
// Every VerifiedOn is the ZERO TIME: nothing here has been read against a
// primary text from this environment, and setting a VerifiedOn is a claim that
// somebody did.
// ---------------------------------------------------------------------------

// dec builds a decimal from a string literal, panicking at init on a malformed
// one. The panic is the point: a malformed rate must not become a zero.
func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// socialSecurityWindows is the effective-dated social-security table, SORTED
// ascending by From, contiguous, with only the last window open-ended.
//
// ss-2019-07 exists so that a re-run or a back-dated correction of an older wage
// month RESOLVES instead of erroring. Its From is 1 July 2019 because that is
// the date the ESI contribution rates dropped to 0.75%/3.25%; a pay run for June
// 2019 or earlier is deliberately NOT SERVED — this engine has never been given
// the pre-2019 ESI rates and must say so rather than guess.
//
// ---------------------------------------------------------------------------
// WHY THE CODE-ERA WINDOW OPENS ON 21 NOVEMBER 2025 AND NOT ON 1 JULY 2026
// ---------------------------------------------------------------------------
// This boundary was WRONG BY EIGHT MONTHS in the first cut of this table, which
// switched the s.2(88) wage definition on at 1 July 2026 — the date the
// contribution-rate notifications were published — rather than at the date the
// Code came into force. The consequence was not academic. A below-ceiling
// structure of BASIC 8,000 + HRA 12,000 gives, under the PRE-Code definition, a
// statutory wage of 8,000 and an employee EPF share of 960. Under the Code's
// definition the excluded HRA (12,000) exceeds one-half of the 20,000 total, so
// 2,000 is deemed back, the statutory wage is 10,000 and the employee share is
// 1,200. Rs 240 short on the EMPLOYEE side and Rs 240 short on the EMPLOYER
// side, per employee, for EVERY wage month from Nov-2025 to Jun-2026 — plus the
// matching understatement of the ESI base, plus s.14B interest and s.14
// damages on the shortfall when it is found. That is precisely the "structure
// the CTC down into allowances" avoidance the 50% proviso exists to stop, and
// the engine would have been performing it automatically.
//
// The dates, verified against the commencement notifications rather than taken
// from any one secondary summary:
//
//   - 21 NOVEMBER 2025 — the Code on Social Security, 2020 and the Code on
//     Wages, 2019 (with the other two Labour Codes) were BROUGHT INTO FORCE by
//     S.O. 5319(E) dated 21.11.2025 (e-Gazette CG-DL-E-21112025-267882), which
//     appointed that date for ss.1-14 (so s.2(88), the wage definition),
//     ss.15(1)-(2), s.16(1)(c) and ss.17-141. From this date the uniform "wages"
//     definition — with the proviso deeming the excess above one-half of all
//     remuneration back into wages — is the base for EPF and ESI.
//   - The Ministry's later contribution-rate notification for the EPF Scheme,
//     2026 is EXPRESSED TO COME INTO FORCE FROM 21 NOVEMBER 2025 even though it
//     was issued on 1 July 2026. It is retrospective to the Code's commencement,
//     which is independent confirmation that the Code's wage base — not the 1952
//     Act's "basic wages" — governs EPF from 21-11-2025.
//   - 29 JUNE 2026 — the EPF Scheme, 2026 (G.S.R. 525(E)), the EPS 2026 and the
//     EDLI Scheme, 2026 (G.S.R. 526(E), Gazette No. 474) commenced, superseding
//     the 1952, 1995 and 1976 Schemes. NOT 1 July 2026, which is only the date
//     the rate notifications were published.
//
// Hence THREE windows, not two. The middle one is the interim: the Code's wage
// definition is already in force, but the 1952/1995/1976 Schemes are still the
// operative schemes, saved by the Code's repeal-and-savings provision until
// they were superseded on 29 June 2026.
//
// Every one of the three dates above is SECONDARY SOURCE — no primary Gazette
// text could be read from this environment — which is why the rows that turn on
// them are ConfMedium and not ConfHigh. But the DIRECTION is not in doubt and a
// window boundary is not a rate: getting it wrong does not shade a number, it
// applies the wrong body of law to eight months of payroll.
var socialSecurityWindows = []window[SocialSecurityRuleSet]{
	{From: dateUTC(2019, time.July, 1), To: dateUTC(2025, time.November, 21), Version: "ss-2019-07", Set: ss201907},
	{From: dateUTC(2025, time.November, 21), To: dateUTC(2026, time.June, 29), Version: "ss-2025-11", Set: ss202511},
	{From: dateUTC(2026, time.June, 29), Version: "ss-2026-07", Set: ss202607},
}

// incomeTaxWindows is the effective-dated income-tax table, keyed by the FIRST
// DAY of the tax year. Every window is CLOSED — see IncomeTaxFor.
var incomeTaxWindows = []window[IncomeTaxRuleSet]{
	{From: dateUTC(2026, time.April, 1), To: dateUTC(2027, time.April, 1), Version: "it-ty-2026-27", Set: itTY202627},
}

// ---------------------------------------------------------------------------
// ss-2019-07 — the PRE-CODE window, [1 Jul 2019, 21 Nov 2025).
//
// It CLOSES on 21 November 2025, the day the Code on Social Security, 2020 and
// the Code on Wages, 2019 were brought into force by S.O. 5319(E). Leaving it
// open past that date — as the first cut of this table did, running it to
// 1 July 2026 — computes eight months of payroll under a repealed wage
// definition. See the block comment on socialSecurityWindows for the arithmetic
// of that shortfall.
//
// The wage definition here is NOT the Code on Social Security, 2020's. Under
// s.2(b) of the Employees' Provident Funds and Miscellaneous Provisions Act,
// 1952, "basic wages" excluded HRA, overtime, bonus and commission and there was
// NO 50% DEEMING PROVISO at all. That is expressed as a DeemingThresholdPct of
// 100: the excluded components may occupy up to 100% of remuneration before any
// add-back, so the add-back can never trigger. Writing 0 would have meant the
// opposite (every excluded rupee added back), and writing a special case in
// wages.go would have put a second wage rule in a leaf that must have one.
// ---------------------------------------------------------------------------
var ss201907 = SocialSecurityRuleSet{
	Version: "ss-2019-07",

	DeemingThresholdPct: Rate{
		Value:      dec("100"),
		Citation:   "EPF & MP Act 1952 s.2(b): before the Code on Social Security, 2020 there was NO deeming proviso; 100% expresses 'the excluded share is never capped', so no add-back can trigger in this window",
		From:       dateUTC(1952, time.March, 4),
		Confidence: ConfMedium,
	},

	EPFEmployeePct: Rate{
		Value:      dec("12"),
		Citation:   "EPF & MP Act 1952 s.6 read with the notified contribution rate: employee's share 12% of EPF wages; 12% has stood since the 1997 enhancement from 10%",
		From:       dateUTC(1997, time.April, 22),
		Confidence: ConfHigh,
	},
	EPFEmployerPct: Rate{
		Value:      dec("12"),
		Citation:   "EPF & MP Act 1952 s.6: employer's matching share 12% of EPF wages, of which the EPS slice below is diverted to the pension fund",
		From:       dateUTC(1997, time.April, 22),
		Confidence: ConfHigh,
	},
	EPSPct: Rate{
		Value:      dec("8.33"),
		Citation:   "Employees' Pension Scheme 1995 para 3: 8.33% of pensionable salary is diverted from the employer's 12% share, capped at the pensionable-salary ceiling",
		From:       dateUTC(1995, time.November, 16),
		Confidence: ConfHigh,
	},
	EPFWageCeiling: Rate{
		Value:      dec("15000.0000"),
		Citation:   "EPF wage ceiling Rs 15,000 per month, notified w.e.f. 1 September 2014 (S.O. 2226(E)); raised from Rs 6,500 — SECONDARY SOURCE, re-verify",
		From:       dateUTC(2014, time.September, 1),
		Confidence: ConfMedium,
	},
	EPSWageCeiling: Rate{
		Value:      dec("15000.0000"),
		Citation:   "EPS 1995 pensionable-salary ceiling Rs 15,000 per month w.e.f. 1 September 2014; the EPS contribution is therefore capped at 8.33% of 15,000 = Rs 1,250",
		From:       dateUTC(2014, time.September, 1),
		Confidence: ConfMedium,
	},
	EDLIWageCeiling: Rate{
		Value:      dec("15000.0000"),
		Citation:   "EDLI Scheme 1976 wage ceiling Rs 15,000 per month w.e.f. 1 September 2014, aligned with the EPF ceiling — SECONDARY SOURCE, re-verify",
		From:       dateUTC(2014, time.September, 1),
		Confidence: ConfMedium,
	},
	EDLIContributionPct: Rate{
		Value:      dec("0.50"),
		Citation:   "EDLI Scheme 1976 para 7: employer's contribution 0.50% of EDLI wages. Applies ONLY to this pre-2026 window; the EDLI Scheme 2026 rate is to be notified separately and must NOT be taken from here",
		From:       dateUTC(2014, time.September, 1),
		Confidence: ConfMedium,
	},
	EDLIAdminChargePct: Rate{
		Value:      dec("0"),
		Citation:   "EDLI administrative charges (account 22) were WAIVED w.e.f. 1 April 2017 by the EPFO; a zero here is the waiver, not an unknown — SECONDARY SOURCE, re-verify",
		From:       dateUTC(2017, time.April, 1),
		Confidence: ConfMedium,
	},
	EPFAdminChargePct: Rate{
		Value:      dec("0.50"),
		Citation:   "EPF administrative charges (account 2) 0.50% of EPF wages w.e.f. 1 June 2018, subject to the monthly minimum below — SECONDARY SOURCE, re-verify",
		From:       dateUTC(2018, time.June, 1),
		Confidence: ConfMedium,
	},
	EPFAdminChargeMin: Rate{
		Value:      dec("500.0000"),
		Citation:   "EPF administrative charges minimum Rs 500 per month per establishment (Rs 75 for an establishment with no contributing member) w.e.f. 1 June 2018 — SECONDARY SOURCE",
		From:       dateUTC(2018, time.June, 1),
		Confidence: ConfMedium,
	},

	ESIEmployeePct: Rate{
		Value:      dec("0.75"),
		Citation:   "ESI (Central) Rules 1950 rule 51: employee's contribution 0.75% of wages, reduced from 1.75% w.e.f. 1 July 2019 by G.S.R. 423(E) dated 13 June 2019",
		From:       dateUTC(2019, time.July, 1),
		Confidence: ConfHigh,
	},
	ESIEmployerPct: Rate{
		Value:      dec("3.25"),
		Citation:   "ESI (Central) Rules 1950 rule 51: employer's contribution 3.25% of wages, reduced from 4.75% w.e.f. 1 July 2019 by G.S.R. 423(E) dated 13 June 2019",
		From:       dateUTC(2019, time.July, 1),
		Confidence: ConfHigh,
	},
	ESIWageCeiling: Rate{
		Value:      dec("21000.0000"),
		Citation:   "ESI (Central) Rules 1950 rule 50: monthly wage ceiling for coverage Rs 21,000 w.e.f. 1 January 2017. The Rs 25,000 ceiling for a person with disability is NOT modelled in v1",
		From:       dateUTC(2017, time.January, 1),
		Confidence: ConfMedium,
	},
	ESILowWageDailyLimit: Rate{
		Value:      dec("176.0000"),
		Citation:   "ESI (Central) Rules 1950 rule 52 / proviso to ESI Act s.42(1): an employee whose average daily wage does not exceed Rs 176 is exempt from the EMPLOYEE share; the employer share stays payable",
		From:       dateUTC(2019, time.September, 1),
		Confidence: ConfMedium,
	},
}

// ---------------------------------------------------------------------------
// ss-2025-11 — the CODE-ERA INTERIM window, [21 Nov 2025, 29 Jun 2026).
//
// This window is the fix for a blocker, and it is worth being explicit about
// what makes it a window of its own rather than a date moved on an existing one.
// Two different instruments changed on two different days:
//
//  1. On 21 NOVEMBER 2025 the Code on Social Security, 2020 and the Code on
//     Wages, 2019 came into force (S.O. 5319(E)). That changed the WAGE
//     DEFINITION — s.2(88) and its 50% deeming proviso — and therefore the base
//     for every EPF and ESI contribution, immediately.
//  2. On 29 JUNE 2026 the EPF, EPS and EDLI Schemes 2026 commenced and
//     superseded the 1952, 1995 and 1976 Schemes. That changed the SCHEMES.
//
// Between those two dates the new base was already law while the old schemes
// were still the operative ones, saved by the Code's repeal-and-savings
// provision and continuing "as if made under" the Code. Collapsing the two
// events into one boundary gets one of the eight intervening months wrong
// whichever date is chosen: put the boundary at 29 Jun 2026 and Nov-2025 to
// Jun-2026 under-contribute on the pre-Code base; put it at 21 Nov 2025 for
// everything and those months are charged under scheme rates that had not been
// notified yet, which for EDLI would mean REFUSING a contribution that was in
// fact payable at the 1976 Scheme's 0.50%.
//
// So: this window carries the CODE's DeemingThresholdPct of 50 with the
// PRE-2026 schemes' rates. Every rate row below says which of the two it is.
// ---------------------------------------------------------------------------
var ss202511 = SocialSecurityRuleSet{
	Version: "ss-2025-11",

	DeemingThresholdPct: Rate{
		Value: dec("50"),
		Citation: "Code on Social Security, 2020 s.2(88) proviso, IN FORCE from 21 November 2025 per S.O. 5319(E) dated 21.11.2025 " +
			"(e-Gazette CG-DL-E-21112025-267882, appointing that date for ss.1-14 and therefore for the definitions): " +
			"'if payments made by the employer to the employee UNDER CLAUSES (a) TO (i) exceeds one-half, or such other per cent. " +
			"as may be notified by the Central Government, of the all remuneration calculated under this clause, the amount which " +
			"exceeds such one-half ... shall be deemed as remuneration and shall be accordingly added in wages under this clause'. " +
			"NOTE THE CLAUSE RANGE: (j) gratuity and (k) retrenchment compensation / other retirement benefit / ex gratia are " +
			"OUTSIDE the test — see deemingTestClauses in wages.go. An earlier version of this citation said 'the excluded " +
			"payments', i.e. all twelve exclusions, and that wording is what let a leaver's gratuity be deemed back into their " +
			"wages; do not restore it. " +
			"SECONDARY SOURCE for the commencement date — re-verify against the Gazette before real books",
		From:       dateUTC(2025, time.November, 21),
		Confidence: ConfMedium,
	},

	EPFEmployeePct: Rate{
		Value: dec("12"),
		Citation: "Employee's share 12% of provident-fund wages. The EPF Scheme, 2026 contribution-rate notification (issued 1 July 2026) is " +
			"EXPRESSED TO TAKE EFFECT FROM 21 NOVEMBER 2025, i.e. retrospectively to the Code's commencement, and for this interim " +
			"window the saved EPF Scheme 1952 charged the same 12%; the rate is not in doubt, only the base changed",
		From:       dateUTC(2025, time.November, 21),
		Confidence: ConfHigh,
	},
	EPFEmployerPct: Rate{
		Value: dec("12"),
		Citation: "Employer's matching share 12% of provident-fund wages, on the same footing as the employee's share above: the EPF Scheme 2026 " +
			"rate notification runs from 21 November 2025 and the saved EPF Scheme 1952 charged 12% throughout this window. The EPS " +
			"slice below is diverted out of this 12%, not added to it",
		From:       dateUTC(2025, time.November, 21),
		Confidence: ConfHigh,
	},
	EPSPct: Rate{
		Value: dec("8.33"),
		Citation: "Employees' Pension Scheme 1995 para 3, SAVED by the Code on Social Security, 2020's repeal-and-savings provision and still the " +
			"operative pension scheme throughout this window: 8.33% of pensionable salary diverted from the employer's 12% share, capped " +
			"at the pensionable-salary ceiling. The EPS 2026 notified the same 8.33% from 29 June 2026",
		From:       dateUTC(1995, time.November, 16),
		Confidence: ConfHigh,
	},
	EPFWageCeiling: Rate{
		Value: dec("15000.0000"),
		Citation: "EPF wage ceiling Rs 15,000 per month, notified w.e.f. 1 September 2014 (S.O. 2226(E)) and unchanged by the Codes; the Ministry " +
			"separately declared Rs 15,000 as the Chapter III wage ceiling under the Code on 29 May 2026 — SECONDARY SOURCE, re-verify",
		From:       dateUTC(2014, time.September, 1),
		Confidence: ConfMedium,
	},
	EPSWageCeiling: Rate{
		Value: dec("15000.0000"),
		Citation: "EPS 1995 pensionable-salary ceiling Rs 15,000 per month w.e.f. 1 September 2014, carried unchanged through the Code's " +
			"commencement; the EPS contribution is therefore capped at 8.33% of 15,000 = Rs 1,250 — SECONDARY SOURCE, re-verify",
		From:       dateUTC(2014, time.September, 1),
		Confidence: ConfMedium,
	},
	EDLIWageCeiling: Rate{
		Value: dec("15000.0000"),
		Citation: "EDLI Scheme 1976 wage ceiling Rs 15,000 per month w.e.f. 1 September 2014, aligned with the EPF ceiling and unchanged by the " +
			"Code's commencement — SECONDARY SOURCE, re-verify",
		From:       dateUTC(2014, time.September, 1),
		Confidence: ConfMedium,
	},
	EDLIContributionPct: Rate{
		Value: dec("0.50"),
		Citation: "EDLI Scheme 1976 para 7: employer's contribution 0.50% of EDLI wages. It is USABLE in this window — unlike in ss-2026-07 — " +
			"because the 1976 Scheme was still in force here, saved by the Code until the EDLI Scheme 2026 superseded it on 29 June 2026. " +
			"Deferring it here would refuse a contribution that was actually payable",
		From:       dateUTC(2014, time.September, 1),
		Confidence: ConfMedium,
	},
	EDLIAdminChargePct: Rate{
		Value: dec("0"),
		Citation: "EDLI administrative charges (account 22) were WAIVED w.e.f. 1 April 2017 by the EPFO and the waiver continued while the saved " +
			"1976 Scheme remained operative. A zero here is the WAIVER, not an unknown — contrast ss-2026-07, where the same zero is an " +
			"UNVERIFIED refusal — SECONDARY SOURCE, re-verify",
		From:       dateUTC(2017, time.April, 1),
		Confidence: ConfMedium,
	},
	EPFAdminChargePct: Rate{
		Value: dec("0.50"),
		Citation: "EPF administrative charges (account 2) 0.50% of EPF wages w.e.f. 1 June 2018 under the saved EPF Scheme 1952, subject to the " +
			"monthly minimum below; the Code's commencement changed the wage BASE these are charged on but not the charge — SECONDARY SOURCE",
		From:       dateUTC(2018, time.June, 1),
		Confidence: ConfMedium,
	},
	EPFAdminChargeMin: Rate{
		Value: dec("500.0000"),
		Citation: "EPF administrative-charge minimum Rs 500 per month per establishment (Rs 75 where no member contributes) w.e.f. 1 June 2018 " +
			"under the saved EPF Scheme 1952 — SECONDARY SOURCE",
		From:       dateUTC(2018, time.June, 1),
		Confidence: ConfMedium,
	},

	ESIEmployeePct: Rate{
		Value: dec("0.75"),
		Citation: "ESI (Central) Rules 1950 rule 51: employee's contribution 0.75% of wages, w.e.f. 1 July 2019 by G.S.R. 423(E) dated 13 June 2019. " +
			"The Code on Social Security, 2020 Chapter IV replaced the ESI Act 1948 on 21 November 2025 but did NOT change this rate — what " +
			"changed on that date is the WAGE it is charged on",
		From:       dateUTC(2019, time.July, 1),
		Confidence: ConfHigh,
	},
	ESIEmployerPct: Rate{
		Value: dec("3.25"),
		Citation: "ESI (Central) Rules 1950 rule 51: employer's contribution 3.25% of wages, w.e.f. 1 July 2019 by G.S.R. 423(E) dated 13 June 2019, " +
			"unchanged by the Code's commencement on 21 November 2025; only the wage base moved",
		From:       dateUTC(2019, time.July, 1),
		Confidence: ConfHigh,
	},
	ESIWageCeiling: Rate{
		Value: dec("21000.0000"),
		Citation: "ESI (Central) Rules 1950 rule 50: monthly wage ceiling for coverage Rs 21,000 w.e.f. 1 January 2017, carried through the Code's " +
			"commencement. The Rs 25,000 ceiling for a person with disability is NOT modelled in v1",
		From:       dateUTC(2017, time.January, 1),
		Confidence: ConfMedium,
	},
	ESILowWageDailyLimit: Rate{
		Value: dec("176.0000"),
		Citation: "ESI (Central) Rules 1950 rule 52 / proviso to ESI Act s.42(1): an employee whose average daily wage does not exceed Rs 176 is " +
			"exempt from the EMPLOYEE share only; the employer's 3.25% remains payable. Getting that backwards costs the employee 0.75% of wages",
		From:       dateUTC(2019, time.September, 1),
		Confidence: ConfMedium,
	},
}

// ---------------------------------------------------------------------------
// ss-2026-07 — the EPF/EPS/EDLI Schemes 2026 window, [29 Jun 2026, open).
//
// THE VERSION STRING AND THE WINDOW'S From DELIBERATELY DISAGREE, and that must
// not be quietly "tidied up". The three Schemes COMMENCED on 29 JUNE 2026 (EPF
// Scheme 2026 G.S.R. 525(E); EDLI Scheme 2026 G.S.R. 526(E), Gazette No. 474,
// both dated 29.06.2026), which is why From is 29 June. The version string was
// coined from 1 July 2026, the date the contribution-rate notifications were
// published, and is now stamped on pay_run rows, quoted by corpus fixtures and
// asserted in tests outside this file. Renaming it to ss-2026-06 is the right
// end state but it is a RENAME ACROSS THE PACKAGE, not an edit to this table;
// doing it here alone would leave the stamped version naming a ruleset that no
// longer exists. The From is the number that decides which law applies, and the
// From is correct.
//
// A window that opened on 1 July 2026 — as the first cut of this table did —
// puts the wage month JUNE 2026 (resolved on its last day, 30 June) under the
// superseded 1976/1952 Schemes even though the 2026 Schemes had been in force
// for two days by then.
//
// THE THREE UNNOTIFIED-RATE ROWS. Three rows here are ConfUnverified WITH A ZERO
// VALUE:
//
//	EDLIAdminChargePct, EPFAdminChargePct, EPFAdminChargeMin
//
// The zero is not a rate; it is a value that can never be consumed, because
// Rate.Use refuses a ConfUnverified row and the engine returns ErrUnverifiedRate
// wrapped with the citation. PFDerive still computes the WAGE BASES —
// min(statutory wage, ceiling) is arithmetic, not a rate — and emits a named
// Deferral where each number would have gone.
//
// EDLIContributionPct WAS THE FOURTH AND IS NOW CONSUMABLE. The history is worth
// keeping, because the row was wrong in two opposite directions before it was
// right:
//
//  1. It first asserted AS A FACT that the 2026 rate was "to be notified
//     separately" and had not been notified. An unverified zero dressed up as a
//     settled nil is how a real liability goes unbooked.
//  2. It was then corrected to say a rate was REPORTED at 0.50% but unread, and
//     kept refusing — the safe direction, but it left roughly Rs 75 per member
//     per month accruing unbooked and called that a release blocker.
//
// It is now 0.50% at ConfMedium, on S.O. 3581(E) of 1 July 2026. THE REASON THE
// RATE WAS SO HARD TO FIND IS WORTH REMEMBERING: it is not in the Scheme.
// G.S.R. 526(E) para 5(1)-(2) leaves the rate to a separate notification decided
// on actuarial valuation, so reading the Scheme text finds nothing and invites
// exactly mistake (1).
//
// WHY ConfMedium AND NOT ConfHigh, and why consumable at all. ConfMedium's own
// definition is "believed correct but curated from a secondary source, or carried
// forward across a law change whose text could not be read here" — and BOTH limbs
// apply: the notification was read only through secondary reporting, and 0.50% is
// the superseded 1976 Scheme para 7 rate carried forward unchanged. That is the
// same footing the EPF 12% and EPS 8.33% rows already stand on. VerifiedOn stays
// ZERO because no primary Gazette text was read; do not set it to make a test
// pass. Upgrade to ConfHigh only when somebody reads S.O. 3581(E) itself.
//
// The three administrative-charge rows below did NOT move with it, and the
// difference is evidential rather than a matter of degree — see their block.
//
// The ADMIN-CHARGE rows are a different case and remain deferred, but the reason
// is now a DOCUMENTED NEGATIVE rather than an unexamined gap: as at 30 July 2026
// no standalone administrative-charge rate notification has been issued under the
// 2026 Schemes. The three notifications of 1 July 2026 set contribution rates only.
//
// THEY ARE PENDING, NOT ABOLISHED, and the scheme text proves it: EPF Scheme 2026
// para 29(2) caps a Rs 500/day late-return fee at "a maximum equal to the
// administrative charges payable by the employer for the relevant month". A cap
// expressed in terms of the charge presupposes the charge. So the zero must stay a
// refusal and must never be reasoned into a settled nil.
//
// TWO NEAR-MISSES, EITHER OF WHICH WOULD BOOK A WRONG NUMBER:
//
//   - S.O. 2701(E) of 29 May 2026 sets INSPECTION charges under s.143(6) for
//     EXEMPTED establishments (EPF 0.35% min Rs 8,750; EDLI 0.005% min Rs 1,250).
//     A DIFFERENT LEVY on a different population. It is the likeliest thing to be
//     mistaken for an administrative charge, because the words are adjacent.
//   - The superseded 1952/1976 Schemes' 0.50% / Rs 500 / account-22 waiver. Do not
//     "fix" a failing test by copying the ss-2019-07 rows up here.
// ---------------------------------------------------------------------------
var ss202607 = SocialSecurityRuleSet{
	Version: "ss-2026-07",

	DeemingThresholdPct: Rate{
		Value: dec("50"),
		Citation: "Code on Social Security, 2020 s.2(88) proviso: 'if payments made by the employer to the employee UNDER CLAUSES (a) TO (i) " +
			"exceeds one-half, or such other per cent. as may be notified by the Central Government, of the all remuneration calculated " +
			"under this clause, the amount which exceeds such one-half ... shall be deemed as remuneration and shall be accordingly added " +
			"in wages under this clause'. NOTE THE CLAUSE RANGE: (j) gratuity and (k) retrenchment compensation / other retirement benefit " +
			"/ ex gratia are OUTSIDE the test — see deemingTestClauses in wages.go. An earlier version of this citation said 'the excluded " +
			"payments', i.e. all twelve exclusions, and that wording is what let a leaver's gratuity be deemed back into their wages; do " +
			"not restore it. " +
			"The From is the COMMENCEMENT date (S.O. 5319(E), 21 November 2025), NOT the Act's " +
			"enactment date of 28 September 2020 — the definition had no legal effect for those five years and dating it to enactment " +
			"invites exactly the eight-month boundary error this table already made once",
		From:       dateUTC(2025, time.November, 21),
		Confidence: ConfMedium,
	},

	EPFEmployeePct: Rate{
		Value: dec("12"),
		Citation: "EPF Scheme, 2026 contribution-rate notification (issued 1 July 2026, expressed to take effect from 21 November 2025) read with " +
			"Code on Social Security, 2020 s.16: employee's share 12% of provident-fund wages. Same 12% as the 1952 Scheme charged; what the " +
			"Code changed is the WAGE BASE, not the rate — SECONDARY SOURCE for the notification",
		From:       dateUTC(2025, time.November, 21),
		Confidence: ConfHigh,
	},
	EPFEmployerPct: Rate{
		Value: dec("12"),
		Citation: "EPF Scheme, 2026 contribution-rate notification (issued 1 July 2026, effective from 21 November 2025) read with Code on Social " +
			"Security, 2020 s.16: employer's matching share 12% of provident-fund wages, of which the EPS slice is diverted to the pension " +
			"fund rather than added on top — SECONDARY SOURCE for the notification",
		From:       dateUTC(2025, time.November, 21),
		Confidence: ConfHigh,
	},
	EPSPct: Rate{
		Value: dec("8.33"),
		Citation: "Employees' Pension Scheme, 2026 contribution-rate notification (issued 1 July 2026, effective from 29 June 2026, the Scheme's " +
			"commencement): 8.33% of wages diverted from the employer's 12% share, capped at the pensionable-salary ceiling. Identical to " +
			"the superseded EPS 1995 para 3 rate — SECONDARY SOURCE for the notification",
		From:       dateUTC(2026, time.June, 29),
		Confidence: ConfHigh,
	},
	EPFWageCeiling: Rate{
		Value: dec("15000.0000"),
		Citation: "EPF wage ceiling Rs 15,000 per month, notified w.e.f. 1 September 2014 (S.O. 2226(E)) and NOT changed by the Code: the Ministry " +
			"separately declared Rs 15,000 as the Chapter III wage ceiling under the Code on Social Security, 2020 by notification dated " +
			"29 May 2026 — SECONDARY SOURCE, RE-VERIFY BEFORE REAL BOOKS",
		From:       dateUTC(2014, time.September, 1),
		Confidence: ConfMedium,
	},
	EPSWageCeiling: Rate{
		Value: dec("15000.0000"),
		Citation: "EPS pensionable-salary ceiling Rs 15,000 per month, CARRIED FORWARD from the 1 September 2014 notification; the EPS 2026 text " +
			"could not be read from this environment and no change to the ceiling was reported — RE-VERIFY",
		From:       dateUTC(2014, time.September, 1),
		Confidence: ConfMedium,
	},
	EDLIWageCeiling: Rate{
		Value: dec("15000.0000"),
		Citation: "EDLI wage ceiling Rs 15,000 per month, CARRIED FORWARD from the 1 September 2014 notification; the EDLI Scheme 2026 text could " +
			"not be read from this environment and no change to the ceiling was reported — RE-VERIFY",
		From:       dateUTC(2014, time.September, 1),
		Confidence: ConfMedium,
	},
	EDLIContributionPct: Rate{
		Value: dec("0.50"),
		Citation: "EDLI Scheme, 2026 employer contribution 0.50% of EDLI wages, w.e.f. 29 June 2026 (the Scheme's commencement), by " +
			"S.O. 3581(E) dated 1 July 2026. THE RATE IS NOT IN THE SCHEME ITSELF: G.S.R. 526(E) para 5(1)-(2) leaves it to be notified " +
			"separately in consultation with the Central Board on actuarial valuation of the Insurance Fund, which is why looking only at the " +
			"Scheme text finds no rate. S.O. 3581(E) sits in a same-day sequential block with S.O. 3580(E) (EPS 8.33%) and S.O. 3582(E) " +
			"(EPF 12%), both independently confirmed and both matching the effective dates this table already held. Employer-funded only; it " +
			"may not be deducted from or recovered from wages (para 8). Ceiling-capped at Rs 15,000/month, so the maximum is Rs 75 per member " +
			"per month. SECONDARY SOURCE for the notification — no primary Gazette text could be read here (egazette.gov.in presents a " +
			"certificate chain automated fetchers reject), so this is ConfMedium and VerifiedOn stays ZERO. It is nonetheless CONSUMABLE, on " +
			"the same footing as the EPF 12% and EPS 8.33% rows and by ConfMedium's own definition (secondary-sourced, or carried across a " +
			"law change whose text could not be read): 0.50% is not a new figure, it is the superseded 1976 Scheme para 7 rate carried " +
			"forward unchanged. See docs/statutory/README.md",
		From:       dateUTC(2026, time.June, 29),
		Confidence: ConfMedium,
	},
	EDLIAdminChargePct: Rate{
		Value: dec("0"),
		Citation: "EDLI administrative charges under the EDLI Scheme, 2026: NO STANDALONE RATE NOTIFICATION EXISTS as at 30 July 2026 — a " +
			"searched-for negative, not an unexamined gap. The charges are PENDING, NOT ABOLISHED: the EPF Scheme 2026 para 29(2) caps a " +
			"Rs 500/day late-return fee at 'a maximum equal to the administrative charges payable by the employer for the relevant month', " +
			"and a cap presupposes the charge exists. DO NOT SUBSTITUTE EITHER NEAR-MISS: S.O. 2701(E) of 29 May 2026 is INSPECTION charges " +
			"under s.143(6) for EXEMPTED establishments only (EDLI 0.005%, min Rs 1,250), a different levy; and the pre-2026 account-22 " +
			"waiver does not survive the scheme it was granted under. Row stays UNVERIFIED and the component is DEFERRED",
		From:       dateUTC(2026, time.June, 29),
		Confidence: ConfUnverified,
	},
	EPFAdminChargePct: Rate{
		Value: dec("0"),
		Citation: "EPF administrative charges under the EPF Scheme, 2026: NO STANDALONE RATE NOTIFICATION EXISTS as at 30 July 2026. The three " +
			"notifications of 1 July 2026 (S.O. 3580(E)/3581(E)/3582(E)) set the CONTRIBUTION rates only and are silent on administrative " +
			"charges. PENDING, NOT ABOLISHED — see the EDLI admin row for para 29(2)'s cap, which presupposes the charge. DO NOT SUBSTITUTE: " +
			"S.O. 2701(E) of 29 May 2026 is INSPECTION charges under s.143(6) for EXEMPTED establishments (EPF 0.35%, min Rs 8,750) and is " +
			"the likeliest wrong number to be booked here; the superseded 1952 Scheme's 0.50% of EPF wages does not survive either. Row " +
			"stays UNVERIFIED and the component is DEFERRED",
		From:       dateUTC(2026, time.June, 29),
		Confidence: ConfUnverified,
	},
	EPFAdminChargeMin: Rate{
		Value: dec("0"),
		Citation: "EPF administrative-charge monthly minimum under the EPF Scheme, 2026: NO AMOUNT EXISTS as at 30 July 2026, for the same reason " +
			"as EPFAdminChargePct — the 1 July 2026 notifications set contribution rates only. The superseded Rs 500 per establishment " +
			"minimum must not be assumed to survive, and S.O. 2701(E)'s inspection-charge minima (Rs 8,750 EPF / Rs 1,250 EDLI) are a " +
			"DIFFERENT levy on exempted establishments and must not be copied here. Row stays UNVERIFIED and the component is DEFERRED",
		From:       dateUTC(2026, time.June, 29),
		Confidence: ConfUnverified,
	},

	ESIEmployeePct: Rate{
		Value:      dec("0.75"),
		Citation:   "ESI (Central) Rules 1950 rule 51: employee's contribution 0.75% of wages, w.e.f. 1 July 2019 by G.S.R. 423(E) dated 13 June 2019; unchanged by the 2026 EPF schemes",
		From:       dateUTC(2019, time.July, 1),
		Confidence: ConfHigh,
	},
	ESIEmployerPct: Rate{
		Value:      dec("3.25"),
		Citation:   "ESI (Central) Rules 1950 rule 51: employer's contribution 3.25% of wages, w.e.f. 1 July 2019 by G.S.R. 423(E) dated 13 June 2019; unchanged by the 2026 EPF schemes",
		From:       dateUTC(2019, time.July, 1),
		Confidence: ConfHigh,
	},
	ESIWageCeiling: Rate{
		Value:      dec("21000.0000"),
		Citation:   "ESI (Central) Rules 1950 rule 50: monthly wage ceiling for coverage Rs 21,000 w.e.f. 1 January 2017. The Rs 25,000 ceiling for a person with disability is NOT modelled in v1",
		From:       dateUTC(2017, time.January, 1),
		Confidence: ConfMedium,
	},
	ESILowWageDailyLimit: Rate{
		Value:      dec("176.0000"),
		Citation:   "ESI (Central) Rules 1950 rule 52 / proviso to ESI Act s.42(1): average daily wage up to Rs 176 exempts the EMPLOYEE share only; the employer's 3.25% remains payable",
		From:       dateUTC(2019, time.September, 1),
		Confidence: ConfMedium,
	},
}

// ---------------------------------------------------------------------------
// it-ty-2026-27 — income tax for FY 2026-27 / AY 2027-28.
//
// THE HONEST CAVEAT, and it is a big one: the Finance Act 2026 could NOT be
// retrieved from this environment. Every slab, deduction, rebate and surcharge
// band below is the Finance Act 2025 value CARRIED FORWARD into FY 2026-27. That
// is a guess about a law, not a reading of one, and it is why every one of these
// rows is ConfMedium and why the income-tax window is CLOSED at 1 April 2027
// rather than open-ended.
//
// Section numbers are given in the Income-tax Act 2025 numbering with the 1961
// Act's section in brackets, per the architecture note. The mapping itself is
// secondary-source and should be checked alongside the rates.
// ---------------------------------------------------------------------------

// newRegimeSlabRate builds one new-regime slab band with its own citation.
func newRegimeSlabRate(pct, band string) Rate {
	return Rate{
		Value:      dec(pct),
		Citation:   "Income-tax Act 2025 s.202 (ex-115BAC) new-regime slab " + band + ", as substituted by the Finance Act 2025 and CARRIED FORWARD to FY 2026-27 (Finance Act 2026 not retrievable here)",
		From:       dateUTC(2026, time.April, 1),
		Confidence: ConfMedium,
	}
}

// oldRegimeSlabRate builds one old-regime slab band with its own citation.
func oldRegimeSlabRate(pct, band string) Rate {
	return Rate{
		Value:      dec(pct),
		Citation:   "Income-tax Act 2025 Schedule (ex-Finance Act rate schedule, Part III) old-regime slab " + band + ", carried forward unchanged since AY 2015-16 — SECONDARY SOURCE, re-verify",
		From:       dateUTC(2026, time.April, 1),
		Confidence: ConfMedium,
	}
}

// oldRegimeSlabs builds the old-regime table for a basic exemption limit. The
// 5%, 20% and 30% bands are identical across the three age bands; only the
// exemption moves, which is why they are built rather than written out three
// times (three copies is three places for a typo to hide).
//
// An empty band is SKIPPED: at the super-senior exemption of 5,00,000 the 5%
// band [5,00,000, 5,00,000) contains nothing, and a zero-width slab in the table
// would be a band that can never be hit sitting in the middle of the arithmetic.
func oldRegimeSlabs(exemptUpTo decimal.Decimal) []Slab {
	bounds := []struct {
		from, to decimal.Decimal
		rate     Rate
	}{
		{decimal.Zero, exemptUpTo, oldRegimeSlabRate("0", "up to the basic exemption limit")},
		{exemptUpTo, dec("500000"), oldRegimeSlabRate("5", "basic exemption to Rs 5,00,000 at 5%")},
		{dec("500000"), dec("1000000"), oldRegimeSlabRate("20", "Rs 5,00,001 to Rs 10,00,000 at 20%")},
		{dec("1000000"), decimal.Zero, oldRegimeSlabRate("30", "above Rs 10,00,000 at 30%")},
	}
	out := make([]Slab, 0, len(bounds))
	for _, b := range bounds {
		if !b.to.IsZero() && !b.to.GreaterThan(b.from) {
			continue // zero-width band (super-senior's 5% band)
		}
		out = append(out, Slab{From: b.from, To: b.to, Rate: b.rate})
	}
	return out
}

var itTY202627 = IncomeTaxRuleSet{
	Version: "it-ty-2026-27",
	TaxYear: TaxYear{StartYear: 2026},

	NewRegimeSlabs: []Slab{
		{From: decimal.Zero, To: dec("400000"), Rate: newRegimeSlabRate("0", "up to Rs 4,00,000 at nil")},
		{From: dec("400000"), To: dec("800000"), Rate: newRegimeSlabRate("5", "Rs 4,00,001 to Rs 8,00,000 at 5%")},
		{From: dec("800000"), To: dec("1200000"), Rate: newRegimeSlabRate("10", "Rs 8,00,001 to Rs 12,00,000 at 10%")},
		{From: dec("1200000"), To: dec("1600000"), Rate: newRegimeSlabRate("15", "Rs 12,00,001 to Rs 16,00,000 at 15%")},
		{From: dec("1600000"), To: dec("2000000"), Rate: newRegimeSlabRate("20", "Rs 16,00,001 to Rs 20,00,000 at 20%")},
		{From: dec("2000000"), To: dec("2400000"), Rate: newRegimeSlabRate("25", "Rs 20,00,001 to Rs 24,00,000 at 25%")},
		{From: dec("2400000"), To: decimal.Zero, Rate: newRegimeSlabRate("30", "above Rs 24,00,000 at 30%")},
	},

	OldRegimeSlabs: map[AgeBand][]Slab{
		AgeBelow60:     oldRegimeSlabs(dec("250000")),
		AgeSenior:      oldRegimeSlabs(dec("300000")),
		AgeSuperSenior: oldRegimeSlabs(dec("500000")),
	},

	StandardDeductionNew: Rate{
		Value:      dec("75000.0000"),
		Citation:   "Income-tax Act 2025 s.19 (ex-16(ia)) standard deduction from salary Rs 75,000 under the new regime, raised from Rs 50,000 by the Finance (No.2) Act 2024",
		From:       dateUTC(2026, time.April, 1),
		Confidence: ConfMedium,
	},
	StandardDeductionOld: Rate{
		Value:      dec("50000.0000"),
		Citation:   "Income-tax Act 2025 s.19 (ex-16(ia)) standard deduction from salary Rs 50,000 under the old regime, unchanged since the Finance Act 2019",
		From:       dateUTC(2026, time.April, 1),
		Confidence: ConfMedium,
	},

	RebateLimitNew: Rate{
		Value:      dec("1200000.0000"),
		Citation:   "Income-tax Act 2025 s.156 (ex-87A): a resident individual whose total income under the new regime does not exceed Rs 12,00,000 gets a rebate of the whole tax, with marginal relief above it",
		From:       dateUTC(2026, time.April, 1),
		Confidence: ConfMedium,
	},
	RebateMaxNew: Rate{
		Value:      dec("60000.0000"),
		Citation:   "Income-tax Act 2025 s.156 (ex-87A) new-regime rebate cap Rs 60,000, which is exactly the tax on Rs 12,00,000 under the new-regime slabs",
		From:       dateUTC(2026, time.April, 1),
		Confidence: ConfMedium,
	},
	RebateLimitOld: Rate{
		Value:      dec("500000.0000"),
		Citation:   "Income-tax Act 2025 s.156 (ex-87A) old-regime rebate: total income up to Rs 5,00,000, unchanged since the Finance Act 2019; NO marginal relief applies to this cliff",
		From:       dateUTC(2026, time.April, 1),
		Confidence: ConfMedium,
	},
	RebateMaxOld: Rate{
		Value:      dec("12500.0000"),
		Citation:   "Income-tax Act 2025 s.156 (ex-87A) old-regime rebate cap Rs 12,500, which is exactly the tax on Rs 5,00,000 under the old-regime slabs",
		From:       dateUTC(2026, time.April, 1),
		Confidence: ConfMedium,
	},

	// The NEW regime has NO 37% band: surcharge is capped at 25%. Handing the
	// old regime's table to a new-regime employee over Rs 5 crore would over-tax
	// them by 12 percentage points of surcharge, which is why the two tables are
	// separate fields rather than one table with a flag.
	SurchargeBandsNew: []SurchargeBand{
		{Above: dec("5000000"), Rate: surchargeRate("10", "total income above Rs 50 lakh")},
		{Above: dec("10000000"), Rate: surchargeRate("15", "total income above Rs 1 crore")},
		{Above: dec("20000000"), Rate: surchargeRate("25", "total income above Rs 2 crore; the new regime is CAPPED here and has no 37% band")},
	},
	SurchargeBandsOld: []SurchargeBand{
		{Above: dec("5000000"), Rate: surchargeRate("10", "total income above Rs 50 lakh")},
		{Above: dec("10000000"), Rate: surchargeRate("15", "total income above Rs 1 crore")},
		{Above: dec("20000000"), Rate: surchargeRate("25", "total income above Rs 2 crore")},
		{Above: dec("50000000"), Rate: surchargeRate("37", "total income above Rs 5 crore; OLD REGIME ONLY")},
	},

	CessPct: Rate{
		Value:      dec("4"),
		Citation:   "Health and Education Cess at 4% of income-tax plus surcharge, levied by the annual Finance Act and unchanged since AY 2019-20 (it replaced the 3% education cess)",
		From:       dateUTC(2018, time.April, 1),
		Confidence: ConfHigh,
	},
}

// surchargeRate builds one surcharge band's rate with its own citation.
func surchargeRate(pct, band string) Rate {
	return Rate{
		Value:      dec(pct),
		Citation:   "Surcharge on income-tax at " + pct + "% where " + band + ", per the Finance Act rate schedule CARRIED FORWARD to FY 2026-27; marginal relief applies at each threshold",
		From:       dateUTC(2026, time.April, 1),
		Confidence: ConfMedium,
	}
}
