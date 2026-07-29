package payroll

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/shopspring/decimal"
)

// Payslip assembly and the pay run's posting legs.
//
// ComputePayslip is the only place the four leaves are composed, and the order
// is not arbitrary: StatutoryWages -> PFDerive -> ESICoverage/ESIDerive ->
// IncomeTaxDerive. The statutory wage is UPSTREAM of the provident fund, so a
// silent error in it is systematic rather than isolated, and the income tax is
// last because it is the only figure computed on an ANNUAL base and it needs
// nothing the others produce.
//
// AggregateLegs turns a whole pay run into debit and credit legs AS DATA. It
// resolves NO ledger ids and opens no transaction — payrolldb maps the ledger
// NAMES below onto ids and hands the result to ledger.PostVoucher. That split is
// what keeps this package's archtest boundary the empty map.
//
// ---------------------------------------------------------------------------
// THE BALANCE IS ASSERTED HERE AS WELL AS IN THE DATABASE
// ---------------------------------------------------------------------------
// posting_balanced is a CONSTRAINT TRIGGER on the posting table, and it is the
// backstop, not the guard. Two reasons it cannot be the only check:
//
//   - a voucher that posts ZERO postings fires no balance check at all, so a pay
//     run that produced an empty leg set would sail through;
//   - by the time the trigger fires the numbers are already in a transaction
//     that is about to be committed, and the error names a constraint rather
//     than the employee whose payslip did not add up.
//
// So AggregateLegs refuses to return an unbalanced set, and it refuses on ANY
// difference — not on a tolerance. There is no rounding here: every figure was
// already rounded by the statute's own rule inside the leaf that produced it,
// and a tolerance would be a licence for a leaf to be wrong by a paisa an
// employee.
//
// ---------------------------------------------------------------------------
// DOCUMENTED GAPS
// ---------------------------------------------------------------------------
//   - NON-STATUTORY DEDUCTIONS (salary advances, loan recoveries, LWF,
//     professional tax) are NOT modelled. There is deliberately no field for
//     them: each one needs its own credit leg to a ledger this track does not
//     seed, and a deduction with nowhere to post is money taken off an employee
//     that appears nowhere in the books. Add the field, the leg and the seeded
//     ledger together or not at all.
//   - GRATUITY, BONUS and leave encashment are not accrued here.
//   - The ESI-reckonable wage is taken as an INPUT and is NOT the Code on Social
//     Security statutory wage — see ESIElection.MonthWages.

// Control-ledger names. They are resolved BY NAME by payrolldb, which is why
// migration 0034 must seed all seven in every company: a chart that differs per
// tenant would make the posting path differ per tenant.
//
// THE WIRING WAVE MUST SEED EXACTLY THESE SEVEN, spelled exactly like this.
const (
	// LedgerSalariesAndWages is debited with the gross earnings of every
	// employee in the run (an expense).
	LedgerSalariesAndWages = "Salaries & Wages"
	// LedgerEmployerPFContribution is debited with the employer's EPF share plus
	// the pension diversion (an expense, and NOT part of the employee's gross).
	LedgerEmployerPFContribution = "Employer PF Contribution"
	// LedgerEmployerESIContribution is debited with the employer's ESI share.
	LedgerEmployerESIContribution = "Employer ESI Contribution"
	// LedgerSalaryPayable is credited with net pay. ONE aggregate control ledger
	// by default — see the note on Payslip.PayableLedger for why that default is
	// a disclosure decision and not a convenience.
	LedgerSalaryPayable = "Salary Payable"
	// LedgerTDSOnSalaryPayable is credited with the s.392 deduction. It belongs
	// under Duties & Taxes.
	LedgerTDSOnSalaryPayable = "TDS on Salary Payable"
	// LedgerPFPayable is credited with the provident-fund remittance. Statutory
	// and voluntary are SEPARATE LEGS on this same ledger so that Form V and the
	// ECR can distinguish them without re-deriving anything.
	LedgerPFPayable = "PF Payable"
	// LedgerESIPayable is credited with the ESI remittance, both shares.
	LedgerESIPayable = "ESI Payable"
)

// Posting directions. Strings rather than a bool, because a bool called
// "isDebit" reads as a debit at half the call sites and as a credit at the other
// half, and a reversed leg still balances.
const (
	DirectionDebit  = "DR"
	DirectionCredit = "CR"
)

var (
	// ErrUnbalancedLegs is returned when the debit and credit totals of a pay run
	// differ by any amount at all. See the file comment.
	ErrUnbalancedLegs = errors.New("payroll: pay-run legs do not balance")

	// ErrNegativeNetPay is returned when the deductions on a payslip exceed the
	// gross. It is a REFUSAL rather than a clamp: a clamped net pay of zero looks
	// like an unpaid month and hides the fact that the employee was over-deducted
	// by an amount nobody chose.
	ErrNegativeNetPay = errors.New("payroll: deductions exceed gross earnings")

	// ErrRuleSetMismatch is returned when the payslips handed to AggregateLegs
	// were not all computed under the same rulesets. Aggregating a run whose
	// members were computed under different law produces a total that is not the
	// tax of anything.
	ErrRuleSetMismatch = errors.New("payroll: pay run mixes ruleset versions")

	// ErrUnknownPFComponent is returned when a payslip carries a provident-fund
	// component that pfPostingSideOf has never been taught to place.
	//
	// It exists because of a shipped defect. The EDLI contribution and the
	// EPF/EDLI administrative charges were added to PFResult.Components and
	// credited to PF Payable, but no debit leg was ever taught about them, so
	// EVERY pay run in a window where those rates are notified failed the
	// double-entry check with no clue as to which component was unhoused. A
	// closed vocabulary with NO default makes the next component added to pf.go
	// fail loudly and by name here, rather than as an arithmetic difference in
	// the balance check several screens later.
	ErrUnknownPFComponent = errors.New("payroll: provident-fund component has no posting side")

	// ErrPFComponentsDisagreeWithTotals is returned when a payslip's tagged
	// provident-fund components do not sum to the totals on the same PFResult.
	//
	// AggregateLegs reads the CREDIT off the components (they are what the ECR
	// and Form V report) and the employer DEBIT off the totals, so the two must
	// agree or the run cannot balance. Detecting the divergence here names the
	// employee and the two figures; letting it fall through to the balance check
	// names only a difference, and letting it reach the database names only a
	// constraint.
	ErrPFComponentsDisagreeWithTotals = errors.New("payroll: provident-fund components do not sum to the result's totals")
)

// pfPostingSide is the side of the pay run's double entry that a provident-fund
// component belongs on.
//
// The three values are NOT two: "the employer contributes it" and "the employer
// is charged it" both need a debit, but only the first is carried by a PFResult
// TOTAL, and conflating them is exactly the bug this type was introduced to
// close.
type pfPostingSide int

const (
	// pfSideEmployeeDeduction is deducted from the employee's own wages. It needs
	// no debit leg of its own: the money is already inside the Salaries & Wages
	// debit, because the gross was expensed in full and the net pay credited was
	// reduced by the deduction.
	pfSideEmployeeDeduction pfPostingSide = iota + 1

	// pfSideEmployerContribution is the employer's own contribution INTO THE
	// MEMBER'S ACCOUNT — the EPF share and the pension diversion. It is carried
	// by PFResult.EmployerEPF and PFResult.EPS, which is where the Employer PF
	// Contribution debit has always read it from.
	pfSideEmployerContribution

	// pfSideEmployerCharge is an employer liability that goes to the FUND but not
	// to the member's account: the EDLI premium, the EDLI administrative charge
	// and the EPF administrative charge.
	//
	// NO PFResult TOTAL CARRIES IT. pf.go deliberately has no EDLI or
	// admin-charge field, so that an absent contribution can never be mistaken
	// for a zero one, and the only place these amounts exist is Components. That
	// is why they must be summed off the components here — and why they were
	// silently missing from the debit until a run in a window with a notified
	// EDLI rate refused to balance.
	pfSideEmployerCharge
)

// pfPostingSideOf places one provident-fund component on one side of the entry.
//
// A CLOSED VOCABULARY WITH NO DEFAULT. Every name pf.go can emit is listed, and
// an unlisted name is ErrUnknownPFComponent rather than a guess. A default of
// "employee deduction" would under-debit the employer by the whole amount; a
// default of "employer contribution" would post an expense the employer does not
// owe. Both still credit PF Payable, so both would look like a plain arithmetic
// error at the balance check and neither would say which component was wrong.
func pfPostingSideOf(component string) (pfPostingSide, error) {
	switch component {
	case ComponentEPFEmployee, ComponentEPFEmployeeVoluntary:
		return pfSideEmployeeDeduction, nil
	case ComponentEPFEmployer, ComponentEPFEmployerVoluntary, ComponentEPS:
		return pfSideEmployerContribution, nil
	case ComponentEDLIContribution, ComponentEDLIAdminCharges, ComponentEPFAdminCharges:
		return pfSideEmployerCharge, nil
	}
	return 0, fmt.Errorf("%w: %q. Every component pf.go emits must be placed on a side here, "+
		"or its PF Payable credit has no matching debit and the whole pay run becomes unpostable", ErrUnknownPFComponent, component)
}

// PFElection is an employee's provident-fund election for a wage month.
//
// Applicable is separate from the rest because "this employer is not covered by
// the EPF Act" and "this employee contributes zero" are different facts, and
// only the first should produce no ECR row at all.
type PFElection struct {
	// Applicable is false for an employee outside the scheme. Every PF figure on
	// the payslip is then absent, not zero.
	Applicable bool
	// Base is PFBaseCeiling or PFBaseActual. There is NO default: an empty value
	// reaches PFDerive and returns ErrUnknownPFBase.
	Base PFBase
	// EmployeeVoluntaryBase is the additional wage the employee elected to
	// contribute on (VPF).
	EmployeeVoluntaryBase decimal.Decimal
	// EmployerMatchesVoluntary reports a separate agreement under para 26(6).
	EmployerMatchesVoluntary bool
	// EPSMember reports pension-scheme membership.
	EPSMember bool
	// FirstMemberOn is the date the employee first became a provident-fund member
	// with ANY employer. See PFInput.FirstMemberOn.
	FirstMemberOn time.Time
}

// ESIElection is an employee's ESI position for a wage month.
type ESIElection struct {
	// Applicable is false for an employee in a non-implemented area or otherwise
	// outside the scheme.
	Applicable bool
	// JoinedOn and LeftOn bound employment. Zero values mean "before this period"
	// and "still employed".
	JoinedOn time.Time
	LeftOn   time.Time
	// Revisions is the effective-dated wage history the coverage LATCH is tested
	// against. A current wage alone cannot implement the latch — the test instant
	// is in the past — which is why this is a history.
	Revisions []WageRevision
	// MonthWages is the ESI-RECKONABLE wage for the month, and it is taken as an
	// input rather than reused from the Code on Social Security statutory wage
	// ON PURPOSE. s.2(22) of the ESI Act 1948 and s.2(88) of the Code are
	// DIFFERENT DEFINITIONS: the ESI wage includes several allowances the Code
	// excludes. Feeding the PF base into ESI under-contributes for most
	// employees, and it is invisible because both numbers are plausible.
	MonthWages decimal.Decimal
	// PaidDays is the number of days wages were paid for, used by rule 52 and by
	// the ESI return's own column.
	PaidDays int
}

// PayslipInput is the complete, self-contained input to ComputePayslip.
type PayslipInput struct {
	// EmployeeRef is an OPAQUE TOKEN — a row id or a payroll number — and never
	// a name, a PAN, a UAN or an ESIC IP number. It exists so a pay run's legs
	// can be split per employee without this package ever seeing an identifier
	// it could print or log.
	EmployeeRef string
	// PayableLedger is the ledger the employee's NET PAY is credited to. Empty
	// means the aggregate LedgerSalaryPayable, which is the DEFAULT and is a
	// disclosure decision: a ledger named after a person carries that person's
	// salary into the Day Book, the Trial Balance and the Balance Sheet, none of
	// which have per-ledger access control. Set it only where a company has
	// deliberately chosen per-employee outstandings.
	PayableLedger string

	// Month is the wage month.
	Month WageMonth
	// Wage is the month's remuneration, which StatutoryWages turns into the PF
	// base. Its TotalRemuneration is also the payslip's GROSS EARNINGS.
	Wage WageInput

	PF        PFElection
	ESI       ESIElection
	IncomeTax IncomeTaxInput
}

// Payslip is one employee's computed month, carrying every leaf's result rather
// than only the totals.
//
// The sub-results are pointers so that "not in the scheme" is distinguishable
// from "in the scheme and computed zero". A zero PFResult would post a zero leg
// and appear in the ECR as a member who contributed nothing.
type Payslip struct {
	EmployeeRef   string
	PayableLedger string
	Month         WageMonth

	// SocialSecurityRuleSetVersion and IncomeTaxRuleSetVersion are stamped so a
	// re-run years later resolves the same law.
	SocialSecurityRuleSetVersion string
	IncomeTaxRuleSetVersion      string

	Wage     WageResult
	PF       *PFResult
	Coverage *CoverageResult
	ESI      *ESIResult
	Tax      IncomeTaxResult

	// GrossEarnings is every component of remuneration, included and excluded —
	// what the employee was promised before any deduction.
	GrossEarnings decimal.Decimal
	// EmployeeDeductions is the employee's PF (statutory + voluntary) plus the
	// employee's ESI share plus the s.392 TDS.
	EmployeeDeductions decimal.Decimal
	// NetPay is GrossEarnings - EmployeeDeductions. Never negative: see
	// ErrNegativeNetPay.
	NetPay decimal.Decimal
	// EmployerContributions is the employer's PF (including the pension
	// diversion) plus the employer's ESI share. It is a COST, not a deduction,
	// and it is never subtracted from the employee's pay.
	EmployerContributions decimal.Decimal

	// Deferrals carries every component the engine deliberately did not compute,
	// gathered from all the leaves. It travels with the payslip so that "we could
	// not compute this" cannot be mistaken for a zero.
	Deferrals []Deferral
}

// ComputePayslip composes the four leaves for one employee for one wage month.
//
// It is pure. It defaults nothing: an empty PFBase, an empty Regime or a
// mismatched ruleset reaches the leaf that owns that vocabulary and comes back
// as the sentinel that names it. payrolldb is where defaulting lives.
func ComputePayslip(in PayslipInput, ss SocialSecurityRuleSet, it IncomeTaxRuleSet) (Payslip, error) {
	if ss.Version == "" {
		return Payslip{}, fmt.Errorf("%w: empty social-security ruleset", ErrNoRuleSet)
	}
	if in.EmployeeRef == "" {
		// A payslip with no reference cannot be split per employee, cannot be
		// reconciled against the register and cannot be found again when it is
		// wrong.
		return Payslip{}, fmt.Errorf("payroll: payslip has no employee reference")
	}
	if in.IncomeTax.TaxYear != in.Month.TaxYear() {
		// The tax year MUST be the one the wage month falls in. January to March
		// belong to the year that started in the PREVIOUS calendar year, and an
		// engine that took the calendar year would compute March 2027's salary
		// under FY 2027-28's slabs.
		return Payslip{}, fmt.Errorf("%w: wage month %s is in tax year %s, not %s",
			ErrNoRuleSet, in.Month, in.Month.TaxYear(), in.IncomeTax.TaxYear)
	}

	// (1) The statutory wage. Upstream of everything: an error here would be
	// systematic across the whole workforce, not isolated to one payslip.
	wage, err := StatutoryWages(in.Wage, ss)
	if err != nil {
		return Payslip{}, fmt.Errorf("employee %s: statutory wages: %w", in.EmployeeRef, err)
	}

	slip := Payslip{
		EmployeeRef:                  in.EmployeeRef,
		PayableLedger:                in.PayableLedger,
		Month:                        in.Month,
		SocialSecurityRuleSetVersion: ss.Version,
		IncomeTaxRuleSetVersion:      it.Version,
		Wage:                         wage,
		GrossEarnings:                wage.TotalRemuneration,
	}

	employeeDeductions := decimal.Zero
	employerContributions := decimal.Zero

	// (2) The provident fund, on the statutory wage from (1) — never on basic pay
	// and never on gross.
	if in.PF.Applicable {
		pf, err := PFDerive(PFInput{
			Month:                    in.Month,
			StatutoryWage:            wage.StatutoryWage,
			Base:                     in.PF.Base,
			EmployeeVoluntaryBase:    in.PF.EmployeeVoluntaryBase,
			EmployerMatchesVoluntary: in.PF.EmployerMatchesVoluntary,
			EPSMember:                in.PF.EPSMember,
			FirstMemberOn:            in.PF.FirstMemberOn,
		}, ss)
		if err != nil {
			return Payslip{}, fmt.Errorf("employee %s: provident fund: %w", in.EmployeeRef, err)
		}
		slip.PF = &pf
		employeeDeductions = employeeDeductions.Add(pf.EmployeeEPF)
		employerContributions = employerContributions.Add(pf.EmployerEPF).Add(pf.EPS)
		slip.Deferrals = append(slip.Deferrals, pf.Deferrals...)
	}

	// (3) ESI: the coverage LATCH first, then the contribution, and the
	// contribution is told the latch's answer rather than re-testing it. That is
	// what makes it impossible for a caller to run the contribution against a
	// fresh monthly test and quietly implement the wrong rule.
	if in.ESI.Applicable {
		cov, err := ESICoverage(CoverageInput{
			Month:     in.Month,
			JoinedOn:  in.ESI.JoinedOn,
			LeftOn:    in.ESI.LeftOn,
			Revisions: in.ESI.Revisions,
		}, ss)
		if err != nil {
			return Payslip{}, fmt.Errorf("employee %s: ESI coverage: %w", in.EmployeeRef, err)
		}
		slip.Coverage = &cov
		esi, err := ESIDerive(ESIInput{
			Month:    in.Month,
			Covered:  cov.Covered,
			Wages:    in.ESI.MonthWages,
			PaidDays: in.ESI.PaidDays,
		}, ss)
		if err != nil {
			return Payslip{}, fmt.Errorf("employee %s: ESI: %w", in.EmployeeRef, err)
		}
		slip.ESI = &esi
		employeeDeductions = employeeDeductions.Add(esi.EmployeeShare)
		employerContributions = employerContributions.Add(esi.EmployerShare)
	}

	// (4) Income tax, on the ANNUAL base. Nothing above feeds it: the taxable
	// salary figures are the caller's, because what is taxable under the
	// Income-tax Act is not what is reckonable under the Code or the ESI Act.
	tax, err := IncomeTaxDerive(in.IncomeTax, it)
	if err != nil {
		return Payslip{}, fmt.Errorf("employee %s: income tax: %w", in.EmployeeRef, err)
	}
	slip.Tax = tax
	employeeDeductions = employeeDeductions.Add(tax.TDSThisMonth)

	slip.EmployeeDeductions = RoundPaise(employeeDeductions)
	slip.EmployerContributions = RoundPaise(employerContributions)
	net := slip.GrossEarnings.Sub(slip.EmployeeDeductions)
	if net.IsNegative() {
		return Payslip{}, fmt.Errorf("%w: employee %s gross %s, deductions %s",
			ErrNegativeNetPay, in.EmployeeRef, slip.GrossEarnings.String(), slip.EmployeeDeductions.String())
	}
	slip.NetPay = RoundPaise(net)
	return slip, nil
}

// PostingLeg is one debit or credit of the pay run's voucher, as DATA.
//
// It carries a ledger NAME and not an id: resolving an id needs a company and a
// database, and this package has neither. payrolldb resolves the seven control
// ledgers by name and rejects a company whose chart is missing one, rather than
// creating it silently — a control ledger conjured at posting time would sit
// outside whatever group the chart intended and land in the wrong half of the
// Balance Sheet.
type PostingLeg struct {
	// Ledger is the control-ledger name, or an employee's own payable ledger.
	Ledger string
	// Direction is DirectionDebit or DirectionCredit.
	Direction string
	// Amount is always POSITIVE. The sign lives in Direction; a negative amount
	// on a debit leg is a credit that still balances and that no report reads
	// correctly.
	Amount decimal.Decimal
	// Tag separates the statutory remittance from the voluntary one on a shared
	// ledger, so Form V and the PF challan can tell them apart without
	// re-deriving anything.
	Tag ComponentTag
	// EmployeeRef is set ONLY on a net-pay leg that was split per employee. It is
	// empty on every aggregate leg.
	EmployeeRef string
}

// PostingLegs is a whole pay run's double entry.
type PostingLegs struct {
	Legs        []PostingLeg
	DebitTotal  decimal.Decimal
	CreditTotal decimal.Decimal

	// EPFAdminChargeTopUp is the amount by which the establishment's EPF
	// administrative charge for the month was raised to reach the statutory
	// MONTHLY MINIMUM (SocialSecurityRuleSet.EPFAdminChargeMin). It is zero in
	// every window where the minimum is not notified, and zero whenever the
	// per-member charges already reach it.
	//
	// It is reported rather than merely folded into the legs because it is the
	// one figure in the voucher that belongs to NO EMPLOYEE: the ECR reconciles
	// per member, the challan does not, and an operator comparing the two needs
	// to see the establishment-level difference by name instead of deducing it
	// from a leg that does not add up member by member.
	EPFAdminChargeTopUp decimal.Decimal
}

// AggregateLegs turns a pay run's payslips into its posting legs.
//
// The legs, and why each exists:
//
//	Dr Salaries & Wages              gross earnings — the employer's expense
//	Dr Employer PF Contribution      employer EPF + the pension diversion
//	                                 + EDLI + the EPF/EDLI admin charges
//	Dr Employer ESI Contribution     the employer's 3.25% share
//	  Cr Salary Payable              net pay, aggregate unless split per employee
//	  Cr TDS on Salary Payable       the s.392 deduction
//	  Cr PF Payable (STATUTORY)      employee + employer statutory + EPS
//	                                 + EDLI + the EPF/EDLI admin charges
//	  Cr PF Payable (VOLUNTARY)      VPF + any employer match, as a SEPARATE leg
//	  Cr ESI Payable                 both shares
//
// WHY EDLI AND THE ADMIN CHARGES SHARE THE EMPLOYER PF DEBIT. They are employer
// liabilities to the fund, and they are NOT part of PFResult.EmployerEPF or
// PFResult.EPS — pf.go carries them only as tagged components, on purpose. They
// were credited to PF Payable from the day they were computed but never debited
// anywhere, which made every pay run in a window with a notified EDLI rate
// refuse to balance (in ss-2019-07, EDLI 0.50% and EPF admin 0.50% on a
// 15,000 EPF wage is a 150-rupee hole per employee per month). The fix is a
// DEBIT, never dropping them from the PF Payable pool: that pool IS the EPFO
// remittance, and a liability deleted from the books to make them balance is a
// worse defect than the imbalance it hides.
//
// THE EPF ADMINISTRATIVE CHARGE HAS AN ESTABLISHMENT-LEVEL FLOOR, AND THIS IS
// WHERE IT IS APPLIED. The monthly minimum (EPFAdminChargeMin, Rs 500 under the
// saved 1952 Scheme) is charged on the ESTABLISHMENT and not on the member, so
// pf.go cannot apply it — a per-employee floor would multiply the charge by
// headcount. This function is the only place in the pure engine that sees a
// whole run at once. applyEPFAdminChargeFloor tops the aggregate up, on BOTH
// sides of the entry, and reports the adjustment as
// PostingLegs.EPFAdminChargeTopUp.
//
// DISCLOSED: they land on the same expense ledger as the employer's EPF/EPS
// cost rather than on a "PF Admin Charges" expense of their own. A separate
// expense would be the better chart, and it needs an EIGHTH control ledger
// seeded for every company — migration 0034 seeds seven and payrolldb resolves
// exactly those seven by name, so a leg naming an unseeded ledger would make
// the whole run unpostable. The split must be made with the seed and the
// resolver together or not at all. Flagged for the wiring wave.
//
// Zero legs are omitted: a run with no ESI-covered employee should not carry two
// zero ESI lines into the Day Book. The legs are returned in a DETERMINISTIC
// order (debits then credits, then by ledger, then by employee reference) so
// that two runs of the same pay run produce the same voucher and the hash chain
// over it means something.
func AggregateLegs(slips []Payslip) (PostingLegs, error) {
	if len(slips) == 0 {
		// An empty run must not become a zero-posting voucher: posting_balanced is
		// a constraint trigger on the POSTING table, so a voucher with no postings
		// fires no balance check at all.
		return PostingLegs{}, fmt.Errorf("payroll: pay run has no payslips")
	}

	ssVersion := slips[0].SocialSecurityRuleSetVersion
	itVersion := slips[0].IncomeTaxRuleSetVersion

	gross := decimal.Zero
	employerPF := decimal.Zero
	employerESI := decimal.Zero
	tds := decimal.Zero
	pfStatutory := decimal.Zero
	pfVoluntary := decimal.Zero
	esiPayable := decimal.Zero
	// Net pay grouped by the ledger it is credited to. The empty key is the
	// aggregate control ledger.
	netByLedger := map[string]decimal.Decimal{}
	netEmployeeRef := map[string]string{}

	// The ESTABLISHMENT's EPF administrative charge for the month, and whether
	// the charge was computed at all. Both are needed for the statutory monthly
	// MINIMUM below; see applyEPFAdminChargeFloor.
	epfAdminCharges := decimal.Zero
	epfAdminComputed := false
	runMonth := slips[0].Month
	monthsDiffer := false

	for i, s := range slips {
		if s.SocialSecurityRuleSetVersion != ssVersion || s.IncomeTaxRuleSetVersion != itVersion {
			return PostingLegs{}, fmt.Errorf("%w: payslip %d used %q/%q, the run uses %q/%q",
				ErrRuleSetMismatch, i, s.SocialSecurityRuleSetVersion, s.IncomeTaxRuleSetVersion, ssVersion, itVersion)
		}
		if s.NetPay.IsNegative() || s.GrossEarnings.IsNegative() {
			return PostingLegs{}, fmt.Errorf("%w: payslip %d (%s)", ErrNegativeNetPay, i, s.EmployeeRef)
		}
		if s.Month != runMonth {
			monthsDiffer = true
		}
		gross = gross.Add(s.GrossEarnings)
		tds = tds.Add(s.Tax.TDSThisMonth)

		if s.PF != nil {
			// The statutory/voluntary split is read off the tagged components, not
			// recomputed from the totals: the components are what the ECR and Form V
			// report, and a second derivation here could disagree with them.
			//
			// EVERY component is credited to PF Payable, so every component must be
			// placed on a debit side too. The three sums below are the audit of that:
			// the employee's is funded out of gross, the employer's contribution is
			// cross-checked against the totals the debit is built from, and the
			// employer's CHARGES (EDLI and administration) exist in no total at all
			// and are the amount that used to go undebited.
			employeeSide := decimal.Zero
			employerSide := decimal.Zero
			employerCharges := decimal.Zero
			for _, c := range s.PF.Components {
				side, err := pfPostingSideOf(c.Name)
				if err != nil {
					return PostingLegs{}, fmt.Errorf("payslip %d (%s): %w", i, s.EmployeeRef, err)
				}
				switch side {
				case pfSideEmployeeDeduction:
					employeeSide = employeeSide.Add(c.Amount)
				case pfSideEmployerContribution:
					employerSide = employerSide.Add(c.Amount)
				case pfSideEmployerCharge:
					employerCharges = employerCharges.Add(c.Amount)
				}
				if c.Name == ComponentEPFAdminCharges {
					// The presence of the component — not its amount — is what says
					// the administrative-charge RATE was notified in this window. A
					// charge that rounds to zero still emits the component, and an
					// establishment whose members' charges all round to zero is
					// precisely the one the monthly minimum exists for.
					epfAdminComputed = true
					epfAdminCharges = epfAdminCharges.Add(c.Amount)
				}
				switch c.Tag {
				case TagVoluntary:
					pfVoluntary = pfVoluntary.Add(c.Amount)
				default:
					pfStatutory = pfStatutory.Add(c.Amount)
				}
			}
			// The two cross-checks. They are not belt-and-braces: the credit is built
			// from the components and the debit from the totals, so a divergence
			// between them is an unbalanced pay run every time, and it is worth
			// naming the employee and the two figures rather than a difference.
			if !employeeSide.Equal(s.PF.EmployeeEPF) {
				return PostingLegs{}, fmt.Errorf("%w: payslip %d (%s) employee components %s, EmployeeEPF %s. "+
					"The employee's deduction is funded out of gross, so the two must agree or the run cannot balance",
					ErrPFComponentsDisagreeWithTotals, i, s.EmployeeRef, employeeSide.String(), s.PF.EmployeeEPF.String())
			}
			if !employerSide.Equal(s.PF.EmployerEPF.Add(s.PF.EPS)) {
				return PostingLegs{}, fmt.Errorf("%w: payslip %d (%s) employer contribution components %s, EmployerEPF+EPS %s",
					ErrPFComponentsDisagreeWithTotals, i, s.EmployeeRef,
					employerSide.String(), s.PF.EmployerEPF.Add(s.PF.EPS).String())
			}
			// The employer's debit: its contribution to the member's account, PLUS
			// the EDLI premium and the administrative charges. Removing the third
			// term re-opens the blocker — see
			// TestAggregateLegs_EDLIAndAdminChargesAreDebitedNotDropped.
			employerPF = employerPF.Add(s.PF.EmployerEPF).Add(s.PF.EPS).Add(employerCharges)
		}
		if s.ESI != nil {
			employerESI = employerESI.Add(s.ESI.EmployerShare)
			esiPayable = esiPayable.Add(s.ESI.EmployeeShare).Add(s.ESI.EmployerShare)
		}

		key := s.PayableLedger
		netByLedger[key] = netByLedger[key].Add(s.NetPay)
		if key != "" {
			netEmployeeRef[key] = s.EmployeeRef
		}
	}

	// The establishment-level floor. It is applied HERE and nowhere else: this is
	// the only point in the engine that can see the whole establishment's charge
	// for the month. See applyEPFAdminChargeFloor.
	topUp, err := applyEPFAdminChargeFloor(runMonth, ssVersion, monthsDiffer, epfAdminComputed, epfAdminCharges)
	if err != nil {
		return PostingLegs{}, err
	}
	// Both sides, so the voucher still balances: the charge is an employer
	// EXPENSE (debit) and an amount OWED to the fund (credit). Adding it to only
	// one side would be an imbalance; adding it to neither is the defect.
	employerPF = employerPF.Add(topUp)
	pfStatutory = pfStatutory.Add(topUp)

	out := PostingLegs{EPFAdminChargeTopUp: topUp}
	add := func(ledger, direction string, amount decimal.Decimal, tag ComponentTag, ref string) {
		if amount.IsZero() {
			return
		}
		out.Legs = append(out.Legs, PostingLeg{
			Ledger: ledger, Direction: direction, Amount: amount, Tag: tag, EmployeeRef: ref,
		})
	}

	add(LedgerSalariesAndWages, DirectionDebit, gross, TagNA, "")
	add(LedgerEmployerPFContribution, DirectionDebit, employerPF, TagStatutory, "")
	add(LedgerEmployerESIContribution, DirectionDebit, employerESI, TagStatutory, "")

	// Net pay: the aggregate control ledger first, then any per-employee ledgers
	// in a sorted, deterministic order.
	if amt, ok := netByLedger[""]; ok {
		add(LedgerSalaryPayable, DirectionCredit, amt, TagNA, "")
	}
	splitLedgers := make([]string, 0, len(netByLedger))
	for k := range netByLedger {
		if k != "" {
			splitLedgers = append(splitLedgers, k)
		}
	}
	sort.Strings(splitLedgers)
	for _, k := range splitLedgers {
		add(k, DirectionCredit, netByLedger[k], TagNA, netEmployeeRef[k])
	}

	add(LedgerTDSOnSalaryPayable, DirectionCredit, tds, TagStatutory, "")
	add(LedgerPFPayable, DirectionCredit, pfStatutory, TagStatutory, "")
	add(LedgerPFPayable, DirectionCredit, pfVoluntary, TagVoluntary, "")
	add(LedgerESIPayable, DirectionCredit, esiPayable, TagStatutory, "")

	for _, l := range out.Legs {
		if l.Direction == DirectionDebit {
			out.DebitTotal = out.DebitTotal.Add(l.Amount)
		} else {
			out.CreditTotal = out.CreditTotal.Add(l.Amount)
		}
	}

	// NO TOLERANCE. Every figure above was rounded once, by the statute's own
	// rule, inside the leaf that produced it; a difference here is an arithmetic
	// defect, not a rounding artefact, and allowing a paisa of slack would allow
	// a paisa per employee per month to leave the books unexplained.
	if !out.DebitTotal.Equal(out.CreditTotal) {
		return PostingLegs{}, fmt.Errorf("%w: debits %s, credits %s, difference %s",
			ErrUnbalancedLegs,
			out.DebitTotal.String(), out.CreditTotal.String(),
			out.DebitTotal.Sub(out.CreditTotal).String())
	}
	if out.DebitTotal.IsZero() {
		// Every payslip was zero. Posting a voucher with no postings would fire NO
		// balance check at all (the trigger is on the posting table), so refuse
		// rather than emit one.
		return PostingLegs{}, fmt.Errorf("payroll: pay run produced no posting legs")
	}
	return out, nil
}

// ErrAdminChargeFloorUnresolvable is returned when a pay run computes an EPF
// administrative charge but the run's own wage month and stamped ruleset version
// cannot be reconciled, so the establishment's monthly MINIMUM cannot be looked
// up. It is an error and never a silent zero: skipping the floor understates the
// challan, which is the whole defect this guard exists to stop recurring.
var ErrAdminChargeFloorUnresolvable = errors.New("payroll: cannot resolve the EPF administrative-charge minimum for this pay run")

// applyEPFAdminChargeFloor returns the amount by which the ESTABLISHMENT's EPF
// administrative charge for the month must be topped up to reach the statutory
// monthly minimum.
//
// ---------------------------------------------------------------------------
// WHY THE FLOOR IS A RUN-LEVEL ADJUSTMENT AND NOT A PER-EMPLOYEE ONE
// ---------------------------------------------------------------------------
// The minimum is charged on the ESTABLISHMENT, once a month — Rs 500 under the
// saved EPF Scheme 1952 — and NOT on the member. pf.go therefore deliberately
// refuses to apply it (see the block comment there): a Rs 500 floor applied per
// employee would multiply an establishment's administrative charge by its
// headcount, which is a far larger error than the one it would be fixing.
//
// AggregateLegs is the only place in the pure engine that sees every payslip of
// a run at once, so it is the only place that can see the establishment total.
// It was ALSO, until this function existed, a place that saw the total and did
// nothing with it: the minimum sat in the rate table with no consumer anywhere,
// and an establishment of six or fewer members at the ceiling booked 0.50% of
// its EPF wages instead of the floor. Three members at 15,000 book Rs 225
// against Rs 500 payable — Rs 275 a month short on the challan, in default with
// interest under s.14B and damages under s.14. See
// TestAggregateLegs_EPFAdminChargeReachesTheEstablishmentMinimum.
//
// ---------------------------------------------------------------------------
// WHEN IT DOES NOTHING, AND WHY EACH SILENCE IS CORRECT
// ---------------------------------------------------------------------------
//   - The administrative-charge RATE is not notified in the run's window, so no
//     member carries an EPF_ADMIN_CHARGES component (epfAdminComputed is false).
//     Charging a floor on a charge whose rate this build has refused to invent
//     would be inventing the charge itself. This is the ss-2026-07 case and it
//     is a DISCLOSED GAP, not a settled zero — see the caveat on Deferral.
//   - The MINIMUM row is ConfUnverified. Rate.Use refuses it and the floor is not
//     applied. Carrying the superseded Rs 500 forward into a scheme that has not
//     notified one would be inventing a statutory amount.
//   - The establishment's members already reach the minimum between them, which
//     is every establishment of any size.
//
// The top-up is never negative: a floor raises a charge and never reduces one.
func applyEPFAdminChargeFloor(
	runMonth WageMonth,
	ssVersion string,
	monthsDiffer bool,
	epfAdminComputed bool,
	epfAdminCharges decimal.Decimal,
) (decimal.Decimal, error) {
	if !epfAdminComputed {
		return decimal.Zero, nil
	}
	if monthsDiffer {
		// The minimum is per establishment PER MONTH. A run spanning two wage
		// months has two minima and this function cannot say how to split them, so
		// it refuses rather than applying one month's floor to both.
		return decimal.Zero, fmt.Errorf("%w: the run's payslips are not all for the same wage month, and the "+
			"minimum is charged per establishment per MONTH", ErrAdminChargeFloorUnresolvable)
	}
	// Resolved from the run's own wage month, and cross-checked against the
	// version the payslips were STAMPED with. The stamp is what makes a re-run
	// years later reproduce the same figures; if the table has since been re-cut
	// so that the month now resolves elsewhere, the right answer is to say so,
	// not to apply a floor from a ruleset the payslips were not computed under.
	rs, err := SocialSecurityForMonth(runMonth)
	if err != nil {
		return decimal.Zero, fmt.Errorf("%w: %v", ErrAdminChargeFloorUnresolvable, err)
	}
	if rs.Version != ssVersion {
		return decimal.Zero, fmt.Errorf("%w: the payslips are stamped %q but wage month %s now resolves to %q",
			ErrAdminChargeFloorUnresolvable, ssVersion, runMonth, rs.Version)
	}
	monthlyMinimum, err := rs.EPFAdminChargeMin.Use()
	if err != nil {
		// UNVERIFIED minimum. The charge itself stands; only the floor is absent.
		return decimal.Zero, nil
	}
	if epfAdminCharges.GreaterThanOrEqual(monthlyMinimum) {
		return decimal.Zero, nil
	}
	return monthlyMinimum.Sub(epfAdminCharges), nil
}
