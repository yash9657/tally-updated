// Package payroll is the PURE payroll calculation core: statutory wages, EPF /
// EPS / EDLI, ESI, and salary income tax under s.392 (ex-192). It performs no
// I/O — no database, no clock, no context, no logging — so a payslip is a
// deterministic function of its inputs and can be recomputed, byte for byte,
// years after it was paid. The DB-facing half lives in
// internal/payroll/payrolldb; the file emitters live in
// internal/payroll/payrollfile.
//
// ---------------------------------------------------------------------------
// RATE PROVENANCE — READ BEFORE RELEASE
// ---------------------------------------------------------------------------
// Every statutory rate, ceiling, slab and threshold in rates.go was CURATED
// FROM SECONDARY SOURCES — model knowledge and summaries — and NOT read out of
// a primary text. The primary texts that would settle them could NOT be
// retrieved from this environment (no network, and RESEARCH.md carries no
// payroll rate table):
//
//   - the EPFO's ContributionRate.pdf and the EPF / EPS / EDLI Schemes 2026,
//   - rule 50 / rule 51 of the ESI (Central) Rules, 1950,
//   - the Income Tax Department's Form 138 (ex-24Q) file-format data structure,
//   - the Finance Act 2026 (so the FY 2026-27 slabs below are the Finance Act
//     2025 values CARRIED FORWARD, which is a guess about a law, not a reading
//     of one).
//
// Each rate therefore carries its own Citation, its own effective-from date and
// its own Confidence, and a rate whose Confidence is ConfUnverified is REFUSED
// by the engine rather than used (see Rate.Use and ErrUnverifiedRate). Start any
// pre-release review with the ConfUnverified rows, then the ConfMedium ones.
//
// The Go table in rates.go and the independent Python re-derivation in
// corpora/validate.py are duplicated ON PURPOSE. The duplication catches a typo
// on one side. It CANNOT catch a rate that is wrong on both sides, because both
// were written from the same secondary reading. RE-VERIFY AGAINST THE BARE
// STATUTE BEFORE THIS ENGINE TOUCHES REAL BOOKS. A confidently wrong PF ceiling
// underpays somebody every month until someone notices.
//
// ---------------------------------------------------------------------------
// NOT VALIDATOR-VERIFIED
// ---------------------------------------------------------------------------
// This output has NOT been checked by the NSDL/Protean FVU, the EPFO Unified
// Portal or the ESIC employer portal. None of them can run in this environment
// (Java desktop utilities, and no network). The golden corpus in corpora/payroll
// is a SUBSTITUTE for validation, not evidence of it. Do not describe a return
// produced here as "validated"; describe it as "generated, and not yet passed
// through the department's validator".
//
// ---------------------------------------------------------------------------
// WHY THIS PACKAGE MUST NEVER IMPORT internal/compliance
// ---------------------------------------------------------------------------
// internal/compliance is the other pure tax leaf in this codebase and the
// temptation to reuse its vocabulary is real — it already has a ThresholdBasis,
// a PANAvailable flag and a rounding helper. Reusing it would be inheriting a
// DIFFERENT COMPUTATION, not sharing a definition:
//
//   - compliance.sectionRule.PerTxnThreshold and compliance.ThresholdBasis carry
//     a DISCLOSED APPROXIMATION IN THEIR NAMES: v1's s.194x engine tests every
//     threshold PER TRANSACTION even when the statute makes it annual, and
//     therefore under-deducts on a stream of small vendor payments. For a vendor
//     that is a recoverable exposure for the deductor.
//   - For SALARY the statute itself defines the base as ANNUAL (s.392, ex-192:
//     the average of the estimated income-tax for the whole financial year,
//     spread over the months remaining). A per-payslip threshold test is not an
//     approximation of s.392; it is a different calculation, and it deducts ZERO
//     for an employee whose monthly pay sits under the exemption but whose annual
//     pay sits far over it. That is a short salary in March and a wrong Form 138,
//     which the department's TRACES reconciles against Form 130.
//
// So payroll defines its OWN PeriodBasis with THREE members — annual for income
// tax, monthly for the PF ceiling, contribution-period for ESI — because a
// single threshold field cannot carry any of them. The archtest boundary for
// "internal/payroll" is the EMPTY MAP, which makes the inheritance mechanically
// impossible. Do not grant that edge "for reuse".
//
// ---------------------------------------------------------------------------
// PII — WHAT NEVER REACHES THIS LEAF
// ---------------------------------------------------------------------------
// No PAN, UAN, ESIC IP number, employee name, bank account or Aadhaar ever
// crosses into this package. Where the statute keys behaviour off a document the
// engine takes a BOOLEAN — PANAvailable, exactly as compliance/tds.go does for
// s.206AA — so the leaf can never print, log, hash or leak a real identifier.
// Nothing in these three packages may import "log" or "log/slog"
// (TestPayrollPackagesDoNotLog pins that).
package payroll

import (
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// ---------------------------------------------------------------------------
// Sentinels. Every one is wrapped at the point of use with %w and the offending
// value quoted, so a caller can both errors.Is the class and read the specific
// value out of the message — the pattern compliance/tds.go established.
// ---------------------------------------------------------------------------
var (
	// ErrNoRuleSet is returned when no effective-dated window covers the wage
	// month or tax year asked for. It is an ERROR and NEVER a fallback to the
	// newest (or oldest) window: computing a June 2019 pay run under July 2026
	// rates produces a plausible, wrong, un-auditable number that nobody would
	// ever query. This is the same reasoning as inventory.ValuerFor's erroring
	// default arm.
	ErrNoRuleSet = errors.New("payroll: no ruleset covers this period")

	// ErrUnverifiedRate is returned when a computation would consume a Rate whose
	// Confidence is ConfUnverified. The canonical case is the EDLI contribution
	// under the EDLI Scheme 2026, whose rate is expressly "to be notified
	// separately": shipping the superseded 1976 Scheme's 0.50% would be INVENTING
	// A RATE and charging an employer for it.
	ErrUnverifiedRate = errors.New("payroll: rate is not verified against a primary source")

	// ErrUnknownRegime is returned by RegimeFor for anything outside the closed
	// {NEW, OLD} vocabulary, including the empty string. The engine defaults
	// NOTHING; payrolldb applies DefaultRegime.
	ErrUnknownRegime = errors.New("payroll: unknown income-tax regime")

	// ErrUnknownPFBase is returned by PFBaseFor for anything outside
	// {CEILING, ACTUAL}.
	ErrUnknownPFBase = errors.New("payroll: unknown PF contribution base")

	// ErrUnknownComponentType is returned for a salary component type outside the
	// closed vocabulary. It must never degrade to "treat it as an allowance": a
	// component silently classified as excluded from wages under-contributes PF
	// and ESI for the whole of that employee's service.
	ErrUnknownComponentType = errors.New("payroll: unknown salary component type")

	// ErrNegativeStatutoryAmount is returned when a statutory amount that cannot
	// be negative is negative. It exists because RoundNearestRupee rounds HALF
	// AWAY FROM ZERO, which equals the statutory half-up rule only for
	// non-negative values — so rather than round a negative statutory figure
	// under a rule that does not cover it, the callers refuse.
	ErrNegativeStatutoryAmount = errors.New("payroll: statutory amount must not be negative")

	// ErrMonthsRemaining is returned when the s.392 spread is asked to divide the
	// annual tax over fewer than one month. Dividing by zero months would panic;
	// clamping it to one would silently deduct a full year's tax in a single
	// payslip.
	ErrMonthsRemaining = errors.New("payroll: months remaining in the pay year must be at least 1")
)

// ---------------------------------------------------------------------------
// Calendar value types.
//
// Every time.Time in this package is DATE-VALUED: midnight UTC on the calendar
// date, matching the DATE columns in the schema. Nothing here reads a clock.
// ---------------------------------------------------------------------------

// dateUTC builds a date-valued time from calendar parts.
func dateUTC(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// WageMonth is the calendar month whose wages are being computed — the month the
// wages are PAYABLE FOR, which is what every social-security scheme keys off.
//
// It is a value, not a time.Time, because "August 2026" is not a moment and
// carrying it as one invites the two bugs this type exists to prevent: a caller
// resolving a scheme by the FIRST of the month when the scheme changed on the
// 15th, and a caller comparing a month against a timestamp in a different zone.
type WageMonth struct {
	Year  int
	Month time.Month
}

// First returns the first day of the wage month.
func (m WageMonth) First() time.Time { return dateUTC(m.Year, m.Month, 1) }

// Last returns the LAST day of the wage month.
//
// This is the value social-security rulesets resolve on (see SocialSecurityFor):
// a scheme that comes into force mid-month governs the wages payable for that
// whole month, so the month's last day is the date that selects the window.
func (m WageMonth) Last() time.Time { return m.First().AddDate(0, 1, -1) }

// String renders the month as "2006-01", the form used in error messages and in
// the corpus fixtures.
func (m WageMonth) String() string { return m.First().Format("2006-01") }

// TaxYear returns the Indian tax year (April-March) the wage month falls in.
func (m WageMonth) TaxYear() TaxYear { return TaxYearOf(m.First()) }

// TaxYear is an Indian tax year: 1 April of StartYear to 31 March of the next.
// TaxYear{StartYear: 2026} is "2026-27".
//
// Income tax resolves on this VALUE and never on a date, and that is a
// deliberate asymmetry with social security. Its ruleset must be CONSTANT ACROSS
// THE WHOLE YEAR because s.392 estimates the tax on the year's total income; a
// ruleset that changed mid-year would make March's estimate incomparable with
// April's and the year's deductions would not add up to the year's tax.
type TaxYear struct {
	StartYear int
}

// Start returns 1 April of the tax year.
func (ty TaxYear) Start() time.Time { return dateUTC(ty.StartYear, time.April, 1) }

// End returns 31 March, the last day of the tax year.
func (ty TaxYear) End() time.Time { return dateUTC(ty.StartYear+1, time.March, 31) }

// String renders the tax year as "2026-27".
func (ty TaxYear) String() string {
	return fmt.Sprintf("%d-%02d", ty.StartYear, (ty.StartYear+1)%100)
}

// Contains reports whether a date falls inside the tax year (both ends
// inclusive — 1 April and 31 March are both IN).
func (ty TaxYear) Contains(t time.Time) bool {
	d := dateUTC(t.Year(), t.Month(), t.Day())
	return !d.Before(ty.Start()) && !d.After(ty.End())
}

// TaxYearOf returns the tax year a date falls in. January to March belong to the
// tax year that STARTED in the previous calendar year.
func TaxYearOf(t time.Time) TaxYear {
	if t.Month() < time.April {
		return TaxYear{StartYear: t.Year() - 1}
	}
	return TaxYear{StartYear: t.Year()}
}

// ---------------------------------------------------------------------------
// Closed vocabularies that are payroll's own.
// ---------------------------------------------------------------------------

// PeriodBasis records what period a limit is statutorily measured over.
//
// This is payroll's OWN type and it deliberately does not reuse
// compliance.ThresholdBasis — see the package doc. There are THREE bases here
// and each one is genuinely different machinery, not three labels on one
// mechanism:
//
//   - BasisAnnual   — income tax. s.392 estimates the whole year and spreads it.
//   - BasisMonthly  — the EPF wage ceiling, applied to each month's wages.
//   - BasisContributionPeriod — ESI. Coverage LATCHES at the start of a
//     contribution period (Apr-Sep, Oct-Mar) and does not change mid-period even
//     if the wage crosses the ceiling.
type PeriodBasis string

const (
	// BasisMonthly is a limit measured over one wage month (the EPF ceiling).
	BasisMonthly PeriodBasis = "MONTHLY"
	// BasisAnnual is a limit measured over the whole tax year (income tax).
	BasisAnnual PeriodBasis = "ANNUAL"
	// BasisContributionPeriod is a limit measured over an ESI contribution
	// period, and LATCHED at its start.
	BasisContributionPeriod PeriodBasis = "CONTRIBUTION_PERIOD"
)

// ComponentTag distinguishes a contribution the statute compels from one the
// employee or employer volunteered.
//
// It exists because the posting legs and the statutory returns must be able to
// tell them apart: Form V and the PF remittance report the STATUTORY share, and
// a voluntary provident-fund contribution credited into the statutory line would
// misstate both. TagNA is for values where the distinction does not arise.
type ComponentTag string

const (
	// TagStatutory marks an amount the statute compels.
	TagStatutory ComponentTag = "STATUTORY"
	// TagVoluntary marks an amount elected over and above the statutory minimum
	// (VPF, an employer contribution on actual wages above the ceiling).
	TagVoluntary ComponentTag = "VOLUNTARY"
	// TagNA marks a value where the statutory/voluntary split does not apply.
	TagNA ComponentTag = "NA"
)

// Deferral is a component the engine DELIBERATELY DID NOT COMPUTE, carried out
// alongside the numbers it did compute.
//
// It exists so that "we could not compute this" travels with the payslip instead
// of being a zero indistinguishable from a genuine nil. The EDLI contribution
// under the 2026 Scheme is the case that forced it: the rate is expressly to be
// notified separately, so the honest output is a stated refusal carrying the
// citation, not 0.00 and not the superseded 1976 figure.
type Deferral struct {
	// Component names what was not computed, e.g. "EDLI_CONTRIBUTION".
	Component string
	// Reason states why, in a sentence a user can act on.
	Reason string
	// Citation names the statute or notification the reason rests on.
	Citation string
}

// ---------------------------------------------------------------------------
// Provenanced statutory numbers.
// ---------------------------------------------------------------------------

// Confidence records how well a statutory number is evidenced.
type Confidence string

const (
	// ConfHigh: stable for years, cross-checked, and unlikely to be misremembered
	// (EPF 12%, EPS 8.33%, ESI 0.75/3.25, the 4% cess).
	ConfHigh Confidence = "HIGH"
	// ConfMedium: believed correct but curated from a secondary source, or
	// carried forward across a law change whose text could not be read here.
	// Every ceiling and every income-tax slab in this build is ConfMedium.
	ConfMedium Confidence = "MEDIUM"
	// ConfUnverified: NOT ESTABLISHED. A computation that would consume such a
	// rate returns ErrUnverifiedRate instead of a number. See Rate.Use.
	ConfUnverified Confidence = "UNVERIFIED"
)

// Rate is one provenanced statutory NUMBER.
//
// It covers percentages AND amounts (a wage ceiling, a rebate cap, a slab
// boundary), which is why the type is not called Percentage: a confidently wrong
// ceiling is exactly as damaging as a confidently wrong percentage, and putting
// ceilings in bare decimals would have exempted them from the provenance and
// float checks that this struct is what makes possible.
//
// Value is ALWAYS built from a decimal STRING literal, never from a float.
// decimal.NewFromFloat("0.0833") looks harmless and is not: the binary double
// nearest 8.33% is 0.0833000000000000018..., and a rate that is not exactly the
// statutory rate produces contributions that are off by paise in a direction
// nobody chose. TestRatesAreDecimalStringsNotFloats forbids every float
// constructor and every float literal in this package.
type Rate struct {
	// Value is the statutory number: a percentage as a plain number (12 means
	// 12%), or an amount in rupees.
	Value decimal.Decimal
	// Citation names the statute, rule or notification and its date. It must be
	// at least 40 characters — long enough to be a real reference rather than
	// "the Act" — and is pinned by TestRatesAreDecimalStringsNotFloats.
	Citation string
	// From is the effective-from date of THIS value, which may well predate the
	// window that carries it (EPF's 12% dates from 1997 and is carried into every
	// later ruleset).
	From time.Time
	// Confidence is how well the value is evidenced. ConfUnverified REFUSES.
	Confidence Confidence
	// VerifiedOn is the date the value was checked against a PRIMARY text. It is
	// the ZERO TIME for every row in this build, because no primary text could be
	// retrieved from this environment. A non-zero VerifiedOn is a claim somebody
	// read the statute; it must never be set to make a test pass.
	VerifiedOn time.Time
}

// Use returns the rate's Value for a computation, or ErrUnverifiedRate wrapped
// with the citation.
//
// Every consumption of a Rate inside an engine goes through this method. That is
// the whole enforcement mechanism for ConfUnverified: reading r.Value directly
// bypasses it, so a code review of this package should treat a bare `.Value` in
// a calculation as a defect.
func (r Rate) Use() (decimal.Decimal, error) {
	if r.Confidence == ConfUnverified {
		return decimal.Zero, fmt.Errorf("%w: %s", ErrUnverifiedRate, r.Citation)
	}
	return r.Value, nil
}

// ---------------------------------------------------------------------------
// The bridge back from a STORE'S ROWS to a Payslip.
//
// ===========================================================================
// WHY THIS EXISTS: THERE MAY BE EXACTLY ONE LEG BUILDER
// ===========================================================================
// A pay run is posted from the payslips a store already wrote, not from the
// Payslip values that were in memory when the month was computed — the posting
// runs in a LATER transaction, and crash recovery re-posts from the rows alone
// (see internal/payroll/payrolldb/payrun.go's header). The store therefore has
// to turn its flat columns back into something AggregateLegs can read.
//
// Before this type existed, the store instead DERIVED ITS OWN LEGS from those
// columns, and the two derivations disagreed exactly where it hurts: the pure
// one debited the EDLI premium and the EPF/EDLI administrative charges, the
// store's one had no column for them and dropped all three. Every pay run the
// PRODUCT posted was therefore short of the whole EPFO charge — around Rs 150
// per member per month wherever those rates are notified — while the pure
// package's tests proved a fix that nothing called.
//
// So the split is: PersistedPayslip carries the FIGURES a store holds,
// PayslipFromPersisted turns them back into a Payslip, and AggregateLegs — the
// ONE leg builder — decides the amounts and the sides. A store maps the ledger
// NAMES onto ids and does no arithmetic of its own.
// ---------------------------------------------------------------------------

// ErrPersistedPayslipInconsistent is returned when a stored payslip's own
// figures do not reconcile — the withheld heads do not explain the difference
// between gross and net, or a statutory figure is negative.
//
// It is checked at the bridge rather than left to the aggregate balance check
// because a payroll error is always about ONE PERSON: the aggregate says the
// run is short by a number, this says which employee's row is wrong.
var ErrPersistedPayslipInconsistent = errors.New("payroll: stored payslip does not reconcile with its own withheld heads")

// PersistedPayslip is one employee's payslip AS A STORE HOLDS IT: flat money
// figures, no pointers and no component list.
//
// EVERY FIELD THAT REACHES A POSTING LEG MUST BE HERE. A figure the store
// cannot supply is a figure the voucher cannot carry, which is precisely how
// the EDLI premium and the two administrative charges went unbooked: they exist
// in the engine ONLY as tagged components (pf.go carries no total for them on
// purpose), so a store with no column for them silently posted zero.
type PersistedPayslip struct {
	// EmployeeRef is an OPAQUE TOKEN — a row id or a payroll number, never a
	// name, a PAN or a UAN. It is used only to name the employee in an error.
	EmployeeRef string
	// PayableLedger is the ledger this employee's NET PAY is credited to. Empty
	// means the aggregate LedgerSalaryPayable. A store that keys its chart by id
	// may put the id's string form here: AggregateLegs treats it as an opaque
	// grouping key and echoes it back on the leg.
	PayableLedger string
	// Month is the wage month the figures belong to.
	Month WageMonth
	// The ruleset versions the row was STAMPED with. AggregateLegs refuses a run
	// that mixes them; passing the run's stamped versions is what makes that
	// check meaningful for a re-post.
	SocialSecurityRuleSetVersion string
	IncomeTaxRuleSetVersion      string

	// GrossEarnings, NetPay and TDS are the payslip's headline figures.
	GrossEarnings decimal.Decimal
	NetPay        decimal.Decimal
	TDS           decimal.Decimal

	// Provident fund, statutory and voluntary kept apart on both sides.
	EPFEmployeeStatutory decimal.Decimal
	EPFEmployeeVoluntary decimal.Decimal
	EPFEmployerStatutory decimal.Decimal
	EPFEmployerVoluntary decimal.Decimal
	EPSEmployer          decimal.Decimal

	// THE THREE EMPLOYER CHARGES. They go to the FUND but not into the member's
	// account, no PFResult total carries them, and they are the components the
	// store used to drop. They are a debit on the employer's PF expense AND a
	// credit to PF Payable; omitting them understates the EPFO liability and the
	// challan does not reconcile.
	//
	// A ZERO IS NOT NECESSARILY A ZERO RATE. Where the rate is unnotified the
	// engine emits a Deferral and no component at all, and a store that keeps
	// deferrals separately (payrolldb keeps them in a JSONB column) is the only
	// place that distinction survives. For POSTING the two are identical — an
	// uncomputed charge and a charge computed as zero both book nothing — so
	// this type deliberately does not try to carry the difference.
	EDLIContribution decimal.Decimal
	EDLIAdminCharges decimal.Decimal
	EPFAdminCharges  decimal.Decimal

	ESIEmployee decimal.Decimal
	ESIEmployer decimal.Decimal
}

// PayslipFromPersisted rebuilds the Payslip that AggregateLegs reads from the
// figures a store holds.
//
// IT IS A MAPPING AND NOT A COMPUTATION. Nothing here is re-derived from a
// wage, re-rounded or re-rated: the only arithmetic is the SUM of figures the
// engine already produced, and the two identities it checks are the ones that
// decide whether the voucher can balance at all.
//
// The reconstructed PFResult carries a component row for every NON-ZERO figure
// and none for a zero one, which is what the engine itself does for the
// voluntary and charge rows. It is not a substitute for the stored component
// lines: the ECR and Form V are itemised from those, and this reconstruction
// exists only to feed the leg builder.
func PayslipFromPersisted(p PersistedPayslip) (Payslip, error) {
	if p.EmployeeRef == "" {
		// Without a reference an error here could not name anybody, and the
		// per-employee net-pay split would collapse into the aggregate.
		return Payslip{}, fmt.Errorf("payroll: stored payslip has no employee reference")
	}

	slip := Payslip{
		EmployeeRef:                  p.EmployeeRef,
		PayableLedger:                p.PayableLedger,
		Month:                        p.Month,
		SocialSecurityRuleSetVersion: p.SocialSecurityRuleSetVersion,
		IncomeTaxRuleSetVersion:      p.IncomeTaxRuleSetVersion,
		GrossEarnings:                p.GrossEarnings,
		NetPay:                       p.NetPay,
		Tax: IncomeTaxResult{
			RuleSetVersion: p.IncomeTaxRuleSetVersion,
			TDSThisMonth:   p.TDS,
			PeriodBasis:    BasisAnnual,
		},
	}

	// The provident-fund rows, in the order pf.go emits them. The Tag is what
	// decides which PF Payable leg the amount lands on, so it is stated here
	// exactly once per component name and never inferred from a total.
	rows := []StatutoryComponent{
		{Name: ComponentEPFEmployee, Amount: p.EPFEmployeeStatutory, Tag: TagStatutory},
		{Name: ComponentEPFEmployer, Amount: p.EPFEmployerStatutory, Tag: TagStatutory},
		{Name: ComponentEPS, Amount: p.EPSEmployer, Tag: TagStatutory},
		{Name: ComponentEPFEmployeeVoluntary, Amount: p.EPFEmployeeVoluntary, Tag: TagVoluntary},
		{Name: ComponentEPFEmployerVoluntary, Amount: p.EPFEmployerVoluntary, Tag: TagVoluntary},
		{Name: ComponentEDLIContribution, Amount: p.EDLIContribution, Tag: TagStatutory},
		{Name: ComponentEDLIAdminCharges, Amount: p.EDLIAdminCharges, Tag: TagStatutory},
		{Name: ComponentEPFAdminCharges, Amount: p.EPFAdminCharges, Tag: TagStatutory},
	}
	pf := PFResult{
		RuleSetVersion: p.SocialSecurityRuleSetVersion,
		Month:          p.Month,
		// EmployeeEPF and EmployerEPF are TOTALS INCLUDING the voluntary rows,
		// exactly as PFDerive builds them; AggregateLegs cross-checks the tagged
		// components against these two and refuses any divergence.
		EmployeeEPF: p.EPFEmployeeStatutory.Add(p.EPFEmployeeVoluntary),
		EmployerEPF: p.EPFEmployerStatutory.Add(p.EPFEmployerVoluntary),
		EPS:         p.EPSEmployer,
		PeriodBasis: BasisMonthly,
	}
	inScheme := false
	for _, r := range rows {
		if r.Amount.IsNegative() {
			return Payslip{}, fmt.Errorf("%w: employee %s: %s is %s. A negative provident-fund figure is a sign error, "+
				"and posting it would put the amount on the wrong side of the entry",
				ErrPersistedPayslipInconsistent, p.EmployeeRef, r.Name, r.Amount.String())
		}
		if r.Amount.IsZero() {
			continue
		}
		inScheme = true
		pf.Components = append(pf.Components, r)
	}
	// A nil PF is "this employee is outside the scheme" and an all-zero one is
	// "in the scheme and contributed nothing"; both post nothing, and the nil is
	// the honest reading of a row with no provident-fund figure on it at all.
	if inScheme {
		slip.PF = &pf
	}

	for name, v := range map[string]decimal.Decimal{
		"gross earnings": p.GrossEarnings, "net pay": p.NetPay, "TDS": p.TDS,
		"employee ESI": p.ESIEmployee, "employer ESI": p.ESIEmployer,
	} {
		if v.IsNegative() {
			return Payslip{}, fmt.Errorf("%w: employee %s: %s is %s, and a negative statutory amount would post on the wrong side of the entry",
				ErrPersistedPayslipInconsistent, p.EmployeeRef, name, v.String())
		}
	}
	if !p.ESIEmployee.IsZero() || !p.ESIEmployer.IsZero() {
		slip.ESI = &ESIResult{
			RuleSetVersion: p.SocialSecurityRuleSetVersion,
			Month:          p.Month,
			Applied:        true,
			EmployeeShare:  p.ESIEmployee,
			EmployerShare:  p.ESIEmployer,
			PeriodBasis:    BasisContributionPeriod,
		}
	}

	// The withheld heads. This is the identity the whole entry rests on: the
	// debit side carries GROSS, the credit side carries NET plus each withheld
	// head, so if the heads do not explain gross - net the voucher is short by
	// the difference and no arrangement of the legs will fix it.
	slip.EmployeeDeductions = p.TDS.
		Add(p.EPFEmployeeStatutory).Add(p.EPFEmployeeVoluntary).Add(p.ESIEmployee)
	slip.EmployerContributions = p.EPFEmployerStatutory.
		Add(p.EPFEmployerVoluntary).Add(p.EPSEmployer).Add(p.ESIEmployer)
	if want := p.GrossEarnings.Sub(slip.EmployeeDeductions); !want.Equal(p.NetPay) {
		return Payslip{}, fmt.Errorf("%w: employee %s, wage month %s: net pay is %s but gross %s less the withheld heads "+
			"(TDS %s + employee PF %s + employee ESI %s = %s) is %s. The voucher would be short by %s",
			ErrPersistedPayslipInconsistent, p.EmployeeRef, p.Month,
			p.NetPay.String(), p.GrossEarnings.String(), p.TDS.String(),
			p.EPFEmployeeStatutory.Add(p.EPFEmployeeVoluntary).String(),
			p.ESIEmployee.String(), slip.EmployeeDeductions.String(),
			want.String(), want.Sub(p.NetPay).String())
	}
	return slip, nil
}
