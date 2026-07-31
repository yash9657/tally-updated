package payroll

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// Tests for payslip assembly and the pay run's posting legs.
//
// The one that matters most is TestAggregateLegs_BalancesOrErrors. The database
// has a posting_balanced CONSTRAINT TRIGGER, but it is a backstop and not a
// guard: it lives on the POSTING table, so a voucher that posts zero postings
// fires no balance check at all, and by the time it does fire the message names
// a constraint rather than the employee whose payslip did not add up.

func prDec(t *testing.T, s string) decimal.Decimal {
	t.Helper()
	v, err := decimal.NewFromString(s)
	if err != nil {
		t.Fatalf("%q is not a decimal: %v", s, err)
	}
	return v
}

func prMonth() WageMonth { return WageMonth{Year: 2026, Month: time.August} }

func prRuleSets(t *testing.T) (SocialSecurityRuleSet, IncomeTaxRuleSet) {
	t.Helper()
	ss, err := SocialSecurityForMonth(prMonth())
	if err != nil {
		t.Fatalf("SocialSecurityForMonth: %v", err)
	}
	it, err := IncomeTaxFor(prMonth().TaxYear())
	if err != nil {
		t.Fatalf("IncomeTaxFor: %v", err)
	}
	return ss, it
}

// prEmployee builds a complete, ordinary payslip input: a covered employee on a
// wage that produces a non-zero figure for EVERY leg, so that a leg dropped from
// AggregateLegs shows up as an imbalance rather than as a silent zero.
func prEmployee(t *testing.T, ref, basic, hra string) PayslipInput {
	t.Helper()
	gross := prDec(t, basic).Add(prDec(t, hra))
	return PayslipInput{
		EmployeeRef: ref,
		Month:       prMonth(),
		Wage: WageInput{
			Month: prMonth(),
			Components: []WageComponent{
				{Type: ComponentBasic, Amount: prDec(t, basic)},
				{Type: ComponentHouseRentAllowance, Amount: prDec(t, hra)},
			},
		},
		PF: PFElection{
			Applicable:    true,
			Base:          PFBaseCeiling,
			EPSMember:     true,
			FirstMemberOn: time.Date(2010, time.April, 1, 0, 0, 0, 0, time.UTC),
		},
		ESI: ESIElection{
			Applicable: true,
			Revisions: []WageRevision{
				{EffectiveFrom: time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC), MonthlyWages: gross},
			},
			MonthWages: gross,
			PaidDays:   31,
		},
		IncomeTax: IncomeTaxInput{
			TaxYear:                   prMonth().TaxYear(),
			Regime:                    RegimeNew,
			IsResident:                true,
			AgeBand:                   AgeBelow60,
			PANAvailable:              true,
			YTDTaxableThisEmployer:    gross.Mul(decimal.NewFromInt(4)),
			CurrentMonthTaxable:       gross,
			ProjectedRemainingTaxable: gross.Mul(decimal.NewFromInt(7)),
			MonthsRemainingInclusive:  8,
		},
	}
}

func TestComputePayslip_AssemblesEveryLeafAndCarriesDeferrals(t *testing.T) {
	ss, it := prRuleSets(t)

	// Gross 20,000 = basic 12,000 + HRA 8,000. HRA is 8,000 of 20,000, which is
	// UNDER half, so the deeming proviso does not bite and the statutory wage is
	// 12,000 — below the 15,000 ceiling.
	slip, err := ComputePayslip(prEmployee(t, "EMP-1", "12000", "8000"), ss, it)
	if err != nil {
		t.Fatalf("ComputePayslip: %v", err)
	}

	if !slip.GrossEarnings.Equal(prDec(t, "20000")) {
		t.Errorf("gross: got %s, want 20000. Gross is ALL remuneration, included and excluded",
			slip.GrossEarnings)
	}
	if !slip.Wage.StatutoryWage.Equal(prDec(t, "12000")) {
		t.Errorf("statutory wage: got %s, want 12000 (HRA is under half of the total, so no add-back)",
			slip.Wage.StatutoryWage)
	}
	if slip.PF == nil || slip.ESI == nil || slip.Coverage == nil {
		t.Fatal("a covered employee's payslip must carry the PF, coverage and ESI results")
	}
	// PF on 12,000: employee 1,440; EPS 8.33% of 12,000 = 999.60 -> 1,000;
	// employer EPF 1,440 - 1,000 = 440.
	if !slip.PF.EmployeeEPF.Equal(prDec(t, "1440")) {
		t.Errorf("employee PF: got %s, want 1440", slip.PF.EmployeeEPF)
	}
	if !slip.PF.EPS.Equal(prDec(t, "1000")) {
		t.Errorf("EPS: got %s, want 1000", slip.PF.EPS)
	}
	// ESI on 20,000: employee ceil(150) = 150; employer ceil(650) = 650.
	if !slip.ESI.EmployeeShare.Equal(prDec(t, "150")) || !slip.ESI.EmployerShare.Equal(prDec(t, "650")) {
		t.Errorf("ESI shares: got %s / %s, want 150 / 650", slip.ESI.EmployeeShare, slip.ESI.EmployerShare)
	}

	// NET PAY = gross - (employee PF + employee ESI + TDS). The employer's
	// contributions are a COST and must never be deducted from the employee.
	wantNet := slip.GrossEarnings.
		Sub(slip.PF.EmployeeEPF).
		Sub(slip.ESI.EmployeeShare).
		Sub(slip.Tax.TDSThisMonth)
	if !slip.NetPay.Equal(wantNet) {
		t.Errorf("net pay: got %s, want %s. The employer's PF and ESI shares are an employer COST and "+
			"must never reduce the employee's pay", slip.NetPay, wantNet)
	}
	if !slip.EmployerContributions.Equal(slip.PF.EmployerEPF.Add(slip.PF.EPS).Add(slip.ESI.EmployerShare)) {
		t.Errorf("employer contributions: got %s", slip.EmployerContributions)
	}

	// The deferrals must travel UP from the leaf. A payslip that dropped them
	// would show the charge as absent-and-unexplained rather than as deliberately
	// not computed.
	//
	// This watched the EDLI contribution until S.O. 3581(E) made it computable; it
	// now watches an ADMINISTRATIVE charge, which is still refused. The assertion is
	// about the PLUMBING, not about which rate happens to be unverified — so
	// retarget it at whatever is still deferred rather than deleting it.
	found := false
	for _, d := range slip.Deferrals {
		if d.Component == ComponentEPFAdminCharges {
			found = true
		}
	}
	if !found {
		t.Error("the EPF administrative-charge deferral did not reach the payslip. 'We could not compute this' " +
			"must travel with the numbers, or it becomes indistinguishable from a zero")
	}
	// The other direction, so this cannot pass against an engine that defers
	// everything: the EDLI premium is now COMPUTED and must NOT be deferred.
	for _, d := range slip.Deferrals {
		if d.Component == ComponentEDLIContribution {
			t.Error("the EDLI premium is deferred on the payslip, but S.O. 3581(E) put its rate on record. " +
				"A deferral here is an employer liability going unbooked")
		}
	}

	// The versions must be stamped, or a re-run years later cannot resolve the
	// same law.
	if slip.SocialSecurityRuleSetVersion != ss.Version || slip.IncomeTaxRuleSetVersion != it.Version {
		t.Errorf("ruleset versions not stamped: %q / %q", slip.SocialSecurityRuleSetVersion, slip.IncomeTaxRuleSetVersion)
	}
}

func TestComputePayslip_Refusals(t *testing.T) {
	ss, it := prRuleSets(t)

	noRef := prEmployee(t, "", "12000", "8000")
	if _, err := ComputePayslip(noRef, ss, it); err == nil {
		t.Error("a payslip with no employee reference must be refused: it cannot be split per employee, " +
			"reconciled against the register, or found again when it is wrong")
	}

	// The tax year must be the one the WAGE MONTH falls in. January to March
	// belong to the year that started in the previous calendar year, and an
	// engine that took the calendar year would compute March 2027 under FY
	// 2027-28's slabs.
	wrongYear := prEmployee(t, "EMP-1", "12000", "8000")
	wrongYear.IncomeTax.TaxYear = TaxYear{StartYear: 2025}
	if _, err := ComputePayslip(wrongYear, ss, it); !errors.Is(err, ErrNoRuleSet) {
		t.Errorf("mismatched tax year: want ErrNoRuleSet, got %v", err)
	}

	// An empty PF base must reach PFDerive and come back as the sentinel that
	// names it. The engine defaults NOTHING: silently choosing CEILING for an
	// employer that elected ACTUAL under-contributes for every high earner.
	noBase := prEmployee(t, "EMP-1", "12000", "8000")
	noBase.PF.Base = ""
	if _, err := ComputePayslip(noBase, ss, it); !errors.Is(err, ErrUnknownPFBase) {
		t.Errorf("empty PF base: want ErrUnknownPFBase, got %v", err)
	}

	// Deductions exceeding gross is a REFUSAL, not a clamp: a clamped net of zero
	// looks like an unpaid month and hides an over-deduction nobody chose.
	broke := prEmployee(t, "EMP-1", "12000", "8000")
	broke.IncomeTax.CurrentMonthTaxable = prDec(t, "20000")
	broke.IncomeTax.YTDTaxableThisEmployer = prDec(t, "9000000")
	broke.IncomeTax.ProjectedRemainingTaxable = prDec(t, "9000000")
	broke.IncomeTax.MonthsRemainingInclusive = 1
	broke.IncomeTax.IsFinalMonth = true
	if _, err := ComputePayslip(broke, ss, it); !errors.Is(err, ErrNegativeNetPay) {
		t.Errorf("deductions above gross: want ErrNegativeNetPay, got %v", err)
	}
}

// TestAggregateLegs_BalancesOrErrors is the double-entry guard in the pure leaf.
func TestAggregateLegs_BalancesOrErrors(t *testing.T) {
	ss, it := prRuleSets(t)

	// Two employees, deliberately different, chosen so that EVERY leg the
	// architecture names is NON-ZERO — a dropped leg then shows up as an
	// imbalance instead of hiding behind a zero that would have been omitted.
	// EMP-1 is below the PF ceiling and ESI-covered with no tax to pay; EMP-2 is
	// well above both, with a voluntary provident-fund election, and is the only
	// one who generates a TDS leg.
	a := prEmployee(t, "EMP-1", "12000", "8000")
	b := prEmployee(t, "EMP-2", "150000", "50000")
	b.PF.EmployeeVoluntaryBase = prDec(t, "20000")
	b.ESI.Applicable = false // above the coverage ceiling anyway

	slips := make([]Payslip, 0, 2)
	for _, in := range []PayslipInput{a, b} {
		s, err := ComputePayslip(in, ss, it)
		if err != nil {
			t.Fatalf("ComputePayslip(%s): %v", in.EmployeeRef, err)
		}
		slips = append(slips, s)
	}

	legs, err := AggregateLegs(slips)
	if err != nil {
		t.Fatalf("AggregateLegs: %v", err)
	}
	if !legs.DebitTotal.Equal(legs.CreditTotal) {
		t.Fatalf("legs do not balance: Dr %s, Cr %s", legs.DebitTotal, legs.CreditTotal)
	}

	// The identity the double entry rests on, asserted independently of the
	// implementation: total debits == gross + every employer contribution.
	//
	// EMPLOYER CONTRIBUTIONS ARE IN TWO PLACES, and that is why this sum reads the
	// components rather than just the field. Payslip.EmployerContributions is the
	// CALLER's figure (EPF + EPS); the employer's other statutory charges live only
	// as tagged components on the PF result, because PFResult carries no total for
	// them on purpose. The aggregator debits both. Since S.O. 3581(E) made the EDLI
	// premium computable, at least one of those charges is now real money in every
	// pay run, and a sum that ignored them would have this test asserting the wrong
	// identity — it read Rs 135 short across these two employees before it was fixed.
	wantDr := decimal.Zero
	for _, s := range slips {
		wantDr = wantDr.Add(s.GrossEarnings).Add(s.EmployerContributions)
		if s.PF == nil {
			continue
		}
		for _, name := range pfEmployerChargeComponents {
			if c, ok := s.PF.ComponentByName(name); ok {
				wantDr = wantDr.Add(c.Amount)
			}
		}
	}
	if !legs.DebitTotal.Equal(wantDr) {
		t.Errorf("debit total %s, want %s (gross + employer contributions + employer statutory charges)",
			legs.DebitTotal, wantDr)
	}

	// Every leg the architecture names must be present and positive, and NO leg
	// may carry a negative amount — a negative debit is a credit that still
	// balances and that no report reads correctly.
	seen := map[string]bool{}
	for _, l := range legs.Legs {
		if !l.Amount.IsPositive() {
			t.Errorf("leg %s %s carries a non-positive amount %s; the sign lives in Direction",
				l.Direction, l.Ledger, l.Amount)
		}
		seen[l.Direction+" "+l.Ledger+" "+string(l.Tag)] = true
	}
	for _, want := range []string{
		DirectionDebit + " " + LedgerSalariesAndWages + " " + string(TagNA),
		DirectionDebit + " " + LedgerEmployerPFContribution + " " + string(TagStatutory),
		DirectionDebit + " " + LedgerEmployerESIContribution + " " + string(TagStatutory),
		DirectionCredit + " " + LedgerSalaryPayable + " " + string(TagNA),
		DirectionCredit + " " + LedgerTDSOnSalaryPayable + " " + string(TagStatutory),
		DirectionCredit + " " + LedgerPFPayable + " " + string(TagStatutory),
		DirectionCredit + " " + LedgerPFPayable + " " + string(TagVoluntary),
		DirectionCredit + " " + LedgerESIPayable + " " + string(TagStatutory),
	} {
		if !seen[want] {
			t.Errorf("leg %q is missing. Statutory and voluntary PF must be SEPARATE legs so Form V and "+
				"the remittance can tell them apart without re-deriving anything", want)
		}
	}

	// --- the imbalance must be caught, and it must name the difference --------
	// Corrupt ONE payslip's net pay by a single paisa. The database's
	// posting_balanced trigger would catch this too, eventually, with a message
	// naming a constraint; this must catch it here, naming the amount.
	corrupted := append([]Payslip(nil), slips...)
	corrupted[0].NetPay = corrupted[0].NetPay.Add(prDec(t, "0.0001"))
	_, err = AggregateLegs(corrupted)
	if !errors.Is(err, ErrUnbalancedLegs) {
		t.Fatalf("a one-paisa imbalance was not caught: got %v. There is NO TOLERANCE here — every figure "+
			"was already rounded by its own statute, so a difference is an arithmetic defect", err)
	}
	if !strings.Contains(err.Error(), "0.0001") {
		t.Errorf("the imbalance error must state the difference so it can be traced; got %q", err)
	}

	// An empty run must be refused, not turned into a zero-posting voucher: the
	// balance trigger is on the POSTING table, so a voucher with no postings
	// fires no check at all.
	if _, err := AggregateLegs(nil); err == nil {
		t.Error("an empty pay run must be refused rather than posted as a voucher with no postings")
	}

	// Mixing rulesets must be refused: a total computed under two bodies of law
	// is not the tax of anything.
	mixed := append([]Payslip(nil), slips...)
	mixed[1].SocialSecurityRuleSetVersion = "ss-2019-07"
	if _, err := AggregateLegs(mixed); !errors.Is(err, ErrRuleSetMismatch) {
		t.Errorf("mixed rulesets: want ErrRuleSetMismatch, got %v", err)
	}
}

// TestAggregateLegs_PerEmployeePayableSplitsAndStillBalances pins the opt-in
// per-employee net-pay ledger.
//
// The DEFAULT is one aggregate control ledger, and that default is a DISCLOSURE
// decision rather than a convenience: a ledger named after a person carries that
// person's salary into the Day Book, the Trial Balance and the Balance Sheet,
// none of which have per-ledger access control.
func TestAggregateLegs_PerEmployeePayableSplitsAndStillBalances(t *testing.T) {
	ss, it := prRuleSets(t)

	a := prEmployee(t, "EMP-1", "12000", "8000")
	a.PayableLedger = "Salary Payable - EMP-1"
	b := prEmployee(t, "EMP-2", "14000", "6000")

	slips := make([]Payslip, 0, 2)
	for _, in := range []PayslipInput{a, b} {
		s, err := ComputePayslip(in, ss, it)
		if err != nil {
			t.Fatalf("ComputePayslip(%s): %v", in.EmployeeRef, err)
		}
		slips = append(slips, s)
	}
	legs, err := AggregateLegs(slips)
	if err != nil {
		t.Fatalf("AggregateLegs: %v", err)
	}
	if !legs.DebitTotal.Equal(legs.CreditTotal) {
		t.Fatalf("splitting the net-pay leg unbalanced the run: Dr %s, Cr %s", legs.DebitTotal, legs.CreditTotal)
	}

	var split, aggregate *PostingLeg
	for i := range legs.Legs {
		switch legs.Legs[i].Ledger {
		case "Salary Payable - EMP-1":
			split = &legs.Legs[i]
		case LedgerSalaryPayable:
			aggregate = &legs.Legs[i]
		}
	}
	if split == nil {
		t.Fatal("the per-employee payable ledger produced no leg")
	}
	if split.EmployeeRef != "EMP-1" {
		t.Errorf("a split leg must carry its employee reference, got %q", split.EmployeeRef)
	}
	if !split.Amount.Equal(slips[0].NetPay) {
		t.Errorf("split leg %s, want %s", split.Amount, slips[0].NetPay)
	}
	if aggregate == nil || !aggregate.Amount.Equal(slips[1].NetPay) {
		t.Error("the employee with no payable ledger must still credit the aggregate control ledger")
	}
	if aggregate != nil && aggregate.EmployeeRef != "" {
		t.Errorf("the AGGREGATE leg must carry no employee reference — that is the whole point of it — "+
			"got %q", aggregate.EmployeeRef)
	}
}

// pfEmployerChargeComponents are the three employer liabilities that no
// PFResult TOTAL carries: they exist only as tagged components (pf.go has no
// EDLI or admin-charge field on purpose), and they are what the pay run used to
// credit to PF Payable with no matching debit.
var pfEmployerChargeComponents = []string{
	ComponentEDLIContribution,
	ComponentEDLIAdminCharges,
	ComponentEPFAdminCharges,
}

// pfDeferredChargeComponents are the charges still REFUSED under the real
// ss-2026-07 rates — the two administrative ones.
//
// It is a separate list from pfEmployerChargeComponents on purpose. That list is
// about the LEG-BALANCING shape and is exercised against a synthetic ruleset where
// all three rates are notified, so it must keep all three. This one is about what
// the SHIPPED table refuses, and the EDLI contribution left it when S.O. 3581(E)
// was identified. Collapsing the two would either make the balancing test vacuous
// or silently re-refuse a rate that is now on record.
var pfDeferredChargeComponents = []string{
	ComponentEDLIAdminCharges,
	ComponentEPFAdminCharges,
}

// prChargeRuleSet returns a ruleset in which all THREE employer charge rates are
// NOTIFIED and NON-ZERO, so that every one of them is visible in the legs.
//
// The three rates are SYNTHETIC TEST RATES and are labelled as such in their
// citations: they are not a claim about the law, and nothing outside this test
// may read them. Two reasons the test overrides rather than picking a wage month
// whose real rates happen to be non-zero:
//
//   - the EDLI administrative charge is 0.00 in every window this build carries
//     (waived w.e.f. 1 April 2017), and a zero component cannot show an imbalance;
//   - which window a date falls in is a property of the effective-dated table,
//     which is re-cut whenever a notification is found. A regression test for the
//     POSTING LEGS must not start passing vacuously because a boundary moved.
//
// TestAggregateLegs_EveryNotifiedChargeWindowStillBalances covers the real table
// with its real rates, and is the test that would notice if the rates all became
// deferrals.
func prChargeRuleSet(t *testing.T) SocialSecurityRuleSet {
	t.Helper()
	ss, err := SocialSecurityForMonth(prMonth())
	if err != nil {
		t.Fatalf("SocialSecurityForMonth: %v", err)
	}
	synthetic := func(pct, what string) Rate {
		return Rate{
			Value: prDec(t, pct),
			Citation: "SYNTHETIC TEST RATE, NOT LAW: a non-zero " + what + " so that the employer charge is " +
				"visible in the posting legs. See prChargeRuleSet in payrun_test.go",
			From:       time.Date(2017, time.April, 1, 0, 0, 0, 0, time.UTC),
			Confidence: ConfHigh,
		}
	}
	ss.EDLIContributionPct = synthetic("0.50", "EDLI contribution")
	ss.EDLIAdminChargePct = synthetic("0.20", "EDLI administrative charge")
	ss.EPFAdminChargePct = synthetic("0.50", "EPF administrative charge")
	return ss
}

// TestAggregateLegs_EDLIAndAdminChargesAreDebitedNotDropped is the regression
// test for a blocker that made EVERY pay run in a window with a notified EDLI
// rate unpostable.
//
// PFDerive emits EDLI_CONTRIBUTION, EDLI_ADMIN_CHARGES and EPF_ADMIN_CHARGES as
// TagStatutory components whenever their rates are notified, and AggregateLegs
// credits PF Payable by walking the components — but it built the Employer PF
// Contribution debit from PFResult.EmployerEPF + PFResult.EPS, and NEITHER TOTAL
// CONTAINS THOSE THREE. The employer's EDLI and administrative liability was
// credited with no matching debit, so the run refused to balance by exactly the
// charges: on a 15,000 EPF wage, EDLI 0.50% + EPF admin 0.50% is a 150-rupee
// hole per employee per month, and every back-dated run, re-run and correction
// in that window hit it.
//
// The test asserts BOTH halves of the correct fix, because there is a wrong fix
// that also balances: deleting the three components from the PF Payable credit.
// That pool IS the EPFO remittance, so dropping them removes a real statutory
// liability from the books — a worse defect than the imbalance. Hence the second
// assertion, that the statutory PF Payable credit still exceeds the EPF+EPS
// remittance by exactly the charges.
func TestAggregateLegs_EDLIAndAdminChargesAreDebitedNotDropped(t *testing.T) {
	ss := prChargeRuleSet(t)
	it, err := IncomeTaxFor(prMonth().TaxYear())
	if err != nil {
		t.Fatalf("IncomeTaxFor: %v", err)
	}

	// EMP-1 is below every EPF ceiling this build carries, so its charges are
	// computed on its whole statutory wage; EMP-2 is far above it, so its charges
	// are computed on the CEILED wage. One of each proves the charge follows the
	// ceiled wage figure and not the gross.
	a := prEmployee(t, "EMP-1", "12000", "8000")
	b := prEmployee(t, "EMP-2", "150000", "50000")
	b.PF.EmployeeVoluntaryBase = prDec(t, "20000")
	b.ESI.Applicable = false

	slips := make([]Payslip, 0, 2)
	for _, in := range []PayslipInput{a, b} {
		s, err := ComputePayslip(in, ss, it)
		if err != nil {
			t.Fatalf("ComputePayslip(%s): %v", in.EmployeeRef, err)
		}
		slips = append(slips, s)
	}

	// EMP-1's charges INDEPENDENTLY, from the statute's arithmetic rather than
	// from the engine's own output. Without this the rest of the test would pass
	// just as happily against a build that computed no charges at all.
	//
	//	EPF and EDLI wages 12,000 -> EDLI 0.50% = 60, EDLI admin 0.20% = 24,
	//	EPF admin 0.50% = 60. Total 144.
	if !slips[0].PF.EPFWages.Equal(prDec(t, "12000")) || !slips[0].PF.EDLIWages.Equal(prDec(t, "12000")) {
		t.Fatalf("EMP-1 EPF/EDLI wages are %s/%s, want 12000/12000. The hand-computed charges below are "+
			"derived from that wage, so they no longer mean anything", slips[0].PF.EPFWages, slips[0].PF.EDLIWages)
	}
	wantEMP1 := map[string]string{
		ComponentEDLIContribution: "60",
		ComponentEDLIAdminCharges: "24",
		ComponentEPFAdminCharges:  "60",
	}

	// EMP-2 is the CEILING case. Its ceiling is read off the ruleset rather than
	// hard-coded — the EPF ceiling is a notified figure that moves — but the test
	// insists the ceiling actually bit, or the case is not the case it claims.
	if !slips[1].PF.EDLIWages.LessThan(slips[1].Wage.StatutoryWage) {
		t.Fatalf("EMP-2's EDLI wages %s were not capped below its statutory wage %s; this employee was chosen "+
			"to exercise the CEILED charge and no longer does", slips[1].PF.EDLIWages, slips[1].Wage.StatutoryWage)
	}

	totalCharges := decimal.Zero
	for i, s := range slips {
		if s.PF == nil {
			t.Fatalf("%s: no PF result", s.EmployeeRef)
		}
		for _, name := range pfEmployerChargeComponents {
			c, ok := s.PF.ComponentByName(name)
			if !ok {
				t.Fatalf("%s: component %s is absent. Its rate is notified in this ruleset, so it must be "+
					"COMPUTED and not deferred — otherwise this test is vacuous", s.EmployeeRef, name)
			}
			if !c.Amount.IsPositive() {
				t.Fatalf("%s %s is %s; a zero charge cannot show an imbalance and would make this test vacuous",
					s.EmployeeRef, name, c.Amount)
			}
			if i == 0 && !c.Amount.Equal(prDec(t, wantEMP1[name])) {
				t.Errorf("EMP-1 %s = %s, want %s", name, c.Amount, wantEMP1[name])
			}
			totalCharges = totalCharges.Add(c.Amount)
		}
	}

	legs, err := AggregateLegs(slips)
	if err != nil {
		// This is the blocker itself: before the fix the difference was exactly
		// the charges above, and no pay run in the window could be posted at all.
		t.Fatalf("AggregateLegs: %v\nThe EDLI premium and the EPF/EDLI administrative charges are EMPLOYER "+
			"liabilities credited to PF Payable; they must carry their own DEBIT", err)
	}
	if !legs.DebitTotal.Equal(legs.CreditTotal) {
		t.Fatalf("legs do not balance: Dr %s, Cr %s", legs.DebitTotal, legs.CreditTotal)
	}

	// (1) The employer's debit must have GROWN by the charges — not merely
	// balance, which deleting the credit would also have achieved.
	wantEmployerPF := decimal.Zero
	for _, s := range slips {
		wantEmployerPF = wantEmployerPF.Add(s.PF.EmployerEPF).Add(s.PF.EPS)
	}
	wantEmployerPF = wantEmployerPF.Add(totalCharges)

	var employerPFLeg *PostingLeg
	var pfStatutoryCredit *PostingLeg
	for i := range legs.Legs {
		l := &legs.Legs[i]
		switch {
		case l.Ledger == LedgerEmployerPFContribution && l.Direction == DirectionDebit:
			employerPFLeg = l
		case l.Ledger == LedgerPFPayable && l.Direction == DirectionCredit && l.Tag == TagStatutory:
			pfStatutoryCredit = l
		}
	}
	if employerPFLeg == nil {
		t.Fatal("no Employer PF Contribution debit leg at all")
	}
	if !employerPFLeg.Amount.Equal(wantEmployerPF) {
		t.Errorf("Employer PF Contribution debit = %s, want %s (employer EPF + EPS + %s of EDLI and "+
			"administrative charges). The charges are an employer liability and need their own debit",
			employerPFLeg.Amount, wantEmployerPF, totalCharges)
	}

	// (2) And the PF Payable credit must STILL carry them. Balancing the books by
	// dropping the charges from the remittance pool deletes a real EPFO liability
	// from the accounts.
	if pfStatutoryCredit == nil {
		t.Fatal("no statutory PF Payable credit leg at all")
	}
	wantRemittance := decimal.Zero
	for _, s := range slips {
		for _, name := range []string{ComponentEPFEmployee, ComponentEPFEmployer, ComponentEPS} {
			if c, ok := s.PF.ComponentByName(name); ok {
				wantRemittance = wantRemittance.Add(c.Amount)
			}
		}
	}
	if !pfStatutoryCredit.Amount.Equal(wantRemittance.Add(totalCharges)) {
		t.Errorf("statutory PF Payable credit = %s, want %s (EPF + EPS remittance %s plus charges %s). "+
			"The EDLI premium and the administrative charges are OWED to the EPFO; they must never be dropped "+
			"from the payable pool to make the legs balance", pfStatutoryCredit.Amount,
			wantRemittance.Add(totalCharges), wantRemittance, totalCharges)
	}

	// (3) The run-level identity, stated independently of the legs: the debits
	// are gross, plus every employer contribution, plus the employer charges that
	// no Payslip total carries.
	wantDr := totalCharges
	for _, s := range slips {
		wantDr = wantDr.Add(s.GrossEarnings).Add(s.EmployerContributions)
	}
	if !legs.DebitTotal.Equal(wantDr) {
		t.Errorf("debit total %s, want %s (gross + employer contributions + employer charges)", legs.DebitTotal, wantDr)
	}
}

// TestAggregateLegs_EveryNotifiedChargeWindowStillBalances runs the REAL
// effective-dated rate table over every wage month it serves and refuses to let
// any of them produce a pay run that cannot be posted.
//
// The blocker was not hypothetical: with the rates as shipped, a wage month in
// the pre-Code window computes an EDLI premium and an EPF administrative charge,
// and the run was short of debits by exactly their sum. This test is what
// notices if a future window re-introduces the hole — or if somebody "fixes" a
// failure by turning the charge rates back into deferrals, which would silently
// stop the EPFO liability being accrued at all.
//
// It sweeps months rather than naming one because the window boundaries are
// re-cut whenever a notification is found, and a test pinned to a date starts
// passing vacuously the day the boundary moves.
func TestAggregateLegs_EveryNotifiedChargeWindowStillBalances(t *testing.T) {
	// A statutory wage far above every EPF ceiling the table has carried, so the
	// charges are computed on the ceiling and are as large as they get.
	statutoryWage := prDec(t, "60000")
	gross := statutoryWage

	monthsSwept, monthsWithCharges, topUpMonths := 0, 0, 0
	versionsWithCharges := map[string]bool{}

	for y := 2019; y <= 2027; y++ {
		for m := time.January; m <= time.December; m++ {
			wm := WageMonth{Year: y, Month: m}
			ss, err := SocialSecurityForMonth(wm)
			if err != nil {
				continue // outside the range the engine serves; SocialSecurityFor says so
			}
			monthsSwept++

			pf, err := PFDerive(PFInput{
				Month:         wm,
				StatutoryWage: statutoryWage,
				Base:          PFBaseCeiling,
				EPSMember:     true,
			}, ss)
			if err != nil {
				t.Fatalf("%s: PFDerive: %v", wm, err)
			}

			charges := decimal.Zero
			for _, name := range pfEmployerChargeComponents {
				if c, ok := pf.ComponentByName(name); ok {
					charges = charges.Add(c.Amount)
				}
			}
			if !charges.IsPositive() {
				continue // every charge rate in this window is deferred or zero
			}
			monthsWithCharges++
			versionsWithCharges[ss.Version] = true

			// A payslip built by hand, and deliberately not through ComputePayslip:
			// the income-tax table serves one year and this sweep covers eight, and
			// the provident fund is the only leaf this test is about. The identity
			// it must satisfy is the real one — net pay is gross less the employee's
			// deduction, and the employer's contributions are a cost on top.
			slip := Payslip{
				EmployeeRef:                  "SWEEP",
				Month:                        wm,
				SocialSecurityRuleSetVersion: ss.Version,
				GrossEarnings:                gross,
				EmployeeDeductions:           pf.EmployeeEPF,
				NetPay:                       gross.Sub(pf.EmployeeEPF),
				EmployerContributions:        pf.EmployerEPF.Add(pf.EPS),
				PF:                           &pf,
			}

			legs, err := AggregateLegs([]Payslip{slip})
			if err != nil {
				t.Fatalf("%s (%s): AggregateLegs: %v\nThe charges total %s and are credited to PF Payable; "+
					"a window whose charges are notified must still be postable", wm, ss.Version, err, charges)
			}
			if !legs.DebitTotal.Equal(legs.CreditTotal) {
				t.Fatalf("%s (%s): Dr %s, Cr %s", wm, ss.Version, legs.DebitTotal, legs.CreditTotal)
			}
			// The ESTABLISHMENT floor on the EPF administrative charge. This sweep
			// posts a ONE-MEMBER run, so wherever the minimum is notified the run
			// is below it and carries a top-up; derived here from the ruleset,
			// independently of AggregateLegs.
			wantTopUp := decimal.Zero
			if _, ok := pf.ComponentByName(ComponentEPFAdminCharges); ok {
				if min, err := ss.EPFAdminChargeMin.Use(); err == nil {
					epfAdm := decimal.Zero
					if c, ok := pf.ComponentByName(ComponentEPFAdminCharges); ok {
						epfAdm = c.Amount
					}
					if epfAdm.LessThan(min) {
						wantTopUp = min.Sub(epfAdm)
					}
				}
			}
			if !legs.EPFAdminChargeTopUp.Equal(wantTopUp) {
				t.Errorf("%s (%s): EPF administrative-charge top-up %s, want %s. The minimum is charged per "+
					"ESTABLISHMENT per month; a one-member run below it is short on the challan by the difference",
					wm, ss.Version, legs.EPFAdminChargeTopUp, wantTopUp)
			}
			if wantTopUp.IsPositive() {
				topUpMonths++
			}
			wantDr := gross.Add(pf.EmployerEPF).Add(pf.EPS).Add(charges).Add(wantTopUp)
			if !legs.DebitTotal.Equal(wantDr) {
				t.Errorf("%s (%s): debit total %s, want %s (gross + employer EPF + EPS + charges %s + admin-charge floor top-up %s)",
					wm, ss.Version, legs.DebitTotal, wantDr, charges, wantTopUp)
			}
		}
	}

	if monthsSwept < 12 {
		t.Fatalf("the sweep resolved only %d wage months; it is vacuous", monthsSwept)
	}
	if monthsWithCharges == 0 {
		t.Fatalf("not one of the %d wage months served by this build computes a positive EDLI or "+
			"administrative charge. Either every charge rate has become a deferral — in which case a real EPFO "+
			"liability is no longer being accrued and that is a finding, not a pass — or this sweep no longer "+
			"exercises the blocker it was written for", monthsSwept)
	}
	if topUpMonths == 0 {
		t.Fatalf("not one of the %d wage months with notified charges applied the establishment's EPF "+
			"administrative-charge MINIMUM, even though every run in this sweep has ONE member and cannot reach "+
			"Rs 500 on its own. Either the minimum has stopped being consumed — which is the Rs 275-a-month "+
			"shortfall this floor was added for — or the sweep no longer exercises it", monthsWithCharges)
	}
	t.Logf("swept %d wage months, %d of them carrying notified employer charges (%d of those topped up to the "+
		"establishment administrative-charge minimum), across rulesets %v",
		monthsSwept, monthsWithCharges, topUpMonths, sortedKeys(versionsWithCharges))
}

// TestAggregateLegs_RefusesUnplaceableAndInconsistentPFComponents pins the two
// guards that stop the EDLI blocker from recurring in a new form.
//
// Both failures used to surface as a bare arithmetic difference at the balance
// check — or, for a component whose amount happened to cancel, not at all.
func TestAggregateLegs_RefusesUnplaceableAndInconsistentPFComponents(t *testing.T) {
	ss, it := prRuleSets(t)
	base, err := ComputePayslip(prEmployee(t, "EMP-1", "12000", "8000"), ss, it)
	if err != nil {
		t.Fatalf("ComputePayslip: %v", err)
	}

	// A component name AggregateLegs has never been taught to place. A default
	// side would post the employer an expense it does not owe, or under-debit it
	// by the whole amount, and either way the message would name a difference
	// rather than the component.
	unknown := base
	unknown.PF = &PFResult{}
	*unknown.PF = *base.PF
	unknown.PF.Components = append(append([]StatutoryComponent(nil), base.PF.Components...),
		StatutoryComponent{Name: "PF_INSPECTION_CHARGES", Amount: prDec(t, "10"), Tag: TagStatutory})
	if _, err := AggregateLegs([]Payslip{unknown}); !errors.Is(err, ErrUnknownPFComponent) {
		t.Errorf("an unplaceable PF component: want ErrUnknownPFComponent, got %v. Every component pf.go emits "+
			"is credited to PF Payable, so every one of them must be placed on a debit side by name", err)
	}

	// Components that do not sum to the totals the debit is built from. The
	// credit comes from the components and the debit from the totals, so the two
	// disagreeing is an unbalanced run every time.
	inconsistent := base
	inconsistent.PF = &PFResult{}
	*inconsistent.PF = *base.PF
	inconsistent.PF.EmployerEPF = base.PF.EmployerEPF.Add(prDec(t, "1"))
	if _, err := AggregateLegs([]Payslip{inconsistent}); !errors.Is(err, ErrPFComponentsDisagreeWithTotals) {
		t.Errorf("components disagreeing with totals: want ErrPFComponentsDisagreeWithTotals, got %v", err)
	}
}

// TestAggregateLegs_ZeroLegsAreOmitted pins that a run with no ESI-covered
// employee does not carry two zero ESI lines into the Day Book.
func TestAggregateLegs_ZeroLegsAreOmitted(t *testing.T) {
	ss, it := prRuleSets(t)

	in := prEmployee(t, "EMP-1", "12000", "8000")
	in.ESI.Applicable = false
	slip, err := ComputePayslip(in, ss, it)
	if err != nil {
		t.Fatalf("ComputePayslip: %v", err)
	}
	legs, err := AggregateLegs([]Payslip{slip})
	if err != nil {
		t.Fatalf("AggregateLegs: %v", err)
	}
	for _, l := range legs.Legs {
		if l.Ledger == LedgerESIPayable || l.Ledger == LedgerEmployerESIContribution {
			t.Errorf("a run with no ESI-covered employee produced an ESI leg of %s", l.Amount)
		}
	}
	if !legs.DebitTotal.Equal(legs.CreditTotal) {
		t.Fatalf("omitting zero legs unbalanced the run: Dr %s, Cr %s", legs.DebitTotal, legs.CreditTotal)
	}
}

// ---------------------------------------------------------------------------
// THE ESTABLISHMENT'S EPF ADMINISTRATIVE-CHARGE MINIMUM
// ---------------------------------------------------------------------------

// adminFloorMonth is a wage month in a window where the EPF administrative
// charge AND its monthly minimum are both notified. May 2026 is ss-2025-11, the
// saved EPF Scheme 1952: 0.50% of EPF wages, minimum Rs 500 per establishment
// per month.
var adminFloorMonth = WageMonth{Year: 2026, Month: time.May}

// TestAggregateLegs_EPFAdminChargeReachesTheEstablishmentMinimum is the
// regression test for an unbooked EPFO liability that had NO CONSUMER AT ALL.
//
// EPFAdminChargeMin sat in the rate table, with a citation and an effective-from
// date, and not one line of non-test code read it. pf.go declined to apply it —
// correctly, because a Rs 500 floor per EMPLOYEE would multiply an
// establishment's administrative charge by its headcount — and flagged the gap
// for a wiring wave that never closed it. AggregateLegs is the only place in the
// pure engine that sees a whole run at once, so it is the only place that can.
//
// The consequence was not marginal. The floor bites on every establishment whose
// EPF wages for the month are under Rs 1,00,000 — six or fewer members at the
// ceiling, which is most small employers in the country. Three members at 15,000
// book 0.50% of 45,000 = Rs 225 against Rs 500 payable: Rs 275 a month short on
// the challan, every month, with interest under s.14B and damages under s.14 on
// the shortfall.
func TestAggregateLegs_EPFAdminChargeReachesTheEstablishmentMinimum(t *testing.T) {
	const members = 3
	month := adminFloorMonth
	ss, err := SocialSecurityForMonth(month)
	if err != nil {
		t.Fatalf("SocialSecurityForMonth(%s): %v", month, err)
	}

	// ANTI-VACUITY GATE. Both rows must be notified in this window, or every
	// assertion below holds for the wrong reason. If a re-cut of the table moves
	// the boundary, move adminFloorMonth rather than deleting the test.
	floor, err := ss.EPFAdminChargeMin.Use()
	if err != nil {
		t.Fatalf("the %s ruleset (%s) does not notify an EPF administrative-charge MINIMUM, so this test would "+
			"assert nothing: %v. Move adminFloorMonth to a window that notifies one", month, ss.Version, err)
	}
	if _, err := ss.EPFAdminChargePct.Use(); err != nil {
		t.Fatalf("the %s ruleset (%s) does not notify an EPF administrative-charge RATE: %v", month, ss.Version, err)
	}
	if !floor.Equal(prDec(t, "500")) {
		t.Fatalf("the %s minimum is Rs %s, not Rs 500; the hand derivation below is pinned to Rs 500 and must be "+
			"re-done against the notification that changed it", ss.Version, floor)
	}

	// One member's derivation, at the ceiling. Hand figures, not the engine's:
	// EPF wages 15,000; employee 12% = 1,800; EPS 8.33% = 1,249.50 -> 1,250;
	// employer residual 1,800 - 1,250 = 550; EDLI 0.50% of 15,000 = 75; EDLI
	// administrative charge waived (0.00); EPF administrative charge 0.50% of
	// 15,000 = 75.
	pf, err := PFDerive(PFInput{
		Month:         month,
		StatutoryWage: prDec(t, "15000"),
		Base:          PFBaseCeiling,
		EPSMember:     true,
		FirstMemberOn: time.Date(2010, time.April, 1, 0, 0, 0, 0, time.UTC),
	}, ss)
	if err != nil {
		t.Fatalf("PFDerive: %v", err)
	}
	amountOf := func(name string) decimal.Decimal {
		c, ok := pf.ComponentByName(name)
		if !ok {
			t.Fatalf("component %s is absent under %s, so this test cannot see the charge it is about", name, ss.Version)
		}
		return c.Amount
	}
	for _, want := range []struct{ name, amount string }{
		{ComponentEPFEmployee, "1800"},
		{ComponentEPFEmployer, "550"},
		{ComponentEPS, "1250"},
		{ComponentEDLIContribution, "75"},
		{ComponentEPFAdminCharges, "75"},
	} {
		if got := amountOf(want.name); !got.Equal(prDec(t, want.amount)) {
			t.Fatalf("one member at the ceiling: %s = %s, want %s. The hand derivation this test rests on has moved",
				want.name, got, want.amount)
		}
	}

	perMemberCharges := amountOf(ComponentEDLIContribution).
		Add(amountOf(ComponentEDLIAdminCharges)).
		Add(amountOf(ComponentEPFAdminCharges))
	n := decimal.NewFromInt(members)
	establishmentAdmin := amountOf(ComponentEPFAdminCharges).Mul(n) // 225
	wantTopUp := floor.Sub(establishmentAdmin)                      // 275
	if !wantTopUp.IsPositive() {
		t.Fatalf("%d members at the ceiling already book Rs %s of EPF administrative charge, which reaches the "+
			"Rs %s minimum. This test cannot see the floor being applied; reduce the headcount", members,
			establishmentAdmin, floor)
	}

	gross := prDec(t, "15000")
	slips := make([]Payslip, 0, members)
	for i := 0; i < members; i++ {
		p := pf
		slips = append(slips, Payslip{
			EmployeeRef:                  "MEMBER-" + string(rune('A'+i)),
			Month:                        month,
			SocialSecurityRuleSetVersion: ss.Version,
			IncomeTaxRuleSetVersion:      "it-ty-2026-27",
			GrossEarnings:                gross,
			EmployeeDeductions:           p.EmployeeEPF,
			NetPay:                       gross.Sub(p.EmployeeEPF),
			EmployerContributions:        p.EmployerEPF.Add(p.EPS),
			PF:                           &p,
		})
	}

	legs, err := AggregateLegs(slips)
	if err != nil {
		t.Fatalf("the %s pay run for %d members will not post: %v", month, members, err)
	}

	// (1) THE FLOOR IS APPLIED, AND IT IS REPORTED BY NAME.
	if !legs.EPFAdminChargeTopUp.Equal(wantTopUp) {
		t.Errorf("the %s pay run for %d members at the ceiling books Rs %s of EPF administrative charge and tops it "+
			"up by Rs %s, but the statutory minimum is Rs %s PER ESTABLISHMENT PER MONTH, so the top-up must be "+
			"Rs %s. The establishment remits Rs %s where Rs %s is payable — Rs %s short on the challan every month, "+
			"with interest under s.14B and damages under s.14 of the EPF & MP Act on the shortfall",
			month, members, establishmentAdmin, legs.EPFAdminChargeTopUp, floor, wantTopUp,
			establishmentAdmin.Add(legs.EPFAdminChargeTopUp), floor,
			floor.Sub(establishmentAdmin.Add(legs.EPFAdminChargeTopUp)))
	}

	legAmount := func(ledgerName, direction string, tag ComponentTag) decimal.Decimal {
		total := decimal.Zero
		for _, l := range legs.Legs {
			if l.Ledger == ledgerName && l.Direction == direction && l.Tag == tag {
				total = total.Add(l.Amount)
			}
		}
		return total
	}

	// (2) THE ESTABLISHMENT'S ADMINISTRATIVE CHARGE FOR THE MONTH IS EXACTLY THE
	// MINIMUM — stated as the challan figure rather than as a delta, because the
	// challan is what is actually paid.
	bookedAdmin := establishmentAdmin.Add(legs.EPFAdminChargeTopUp)
	if !bookedAdmin.Equal(floor) {
		t.Errorf("%s: the establishment books Rs %s of EPF administrative charge for the month; the statutory "+
			"minimum is Rs %s", month, bookedAdmin, floor)
	}

	// (3) THE DEBIT grew by the top-up: the floor is an employer EXPENSE, not a
	// reclassification.
	wantDebit := pf.EmployerEPF.Add(pf.EPS).Add(perMemberCharges).Mul(n).Add(wantTopUp)
	if got := legAmount(LedgerEmployerPFContribution, DirectionDebit, TagStatutory); !got.Equal(wantDebit) {
		t.Errorf("%s: Employer PF Contribution debit Rs %s, want Rs %s (employer EPF + EPS + charges over %d "+
			"members, plus the Rs %s establishment top-up). The employer's wage cost is understated by the "+
			"difference", month, got, wantDebit, members, wantTopUp)
	}

	// (4) AND THE CREDIT grew with it: the top-up is OWED TO THE FUND. Debiting
	// the expense without crediting the payable would balance the voucher while
	// leaving the challan short, which is the defect wearing a different hat.
	wantCredit := pf.EmployeeEPF.Add(pf.EmployerEPF).Add(pf.EPS).Add(perMemberCharges).Mul(n).Add(wantTopUp)
	if got := legAmount(LedgerPFPayable, DirectionCredit, TagStatutory); !got.Equal(wantCredit) {
		t.Errorf("%s: PF Payable credit Rs %s, want Rs %s. The Rs %s administrative-charge top-up is OWED to the "+
			"EPFO and must sit in the remittance pool the challan is paid from", month, got, wantCredit, wantTopUp)
	}

	// (5) AND THE VOUCHER STILL BALANCES with the top-up in it.
	if !legs.DebitTotal.Equal(legs.CreditTotal) {
		t.Fatalf("%s: the voucher does not balance after the administrative-charge top-up: Dr %s, Cr %s, "+
			"difference %s", month, legs.DebitTotal, legs.CreditTotal, legs.DebitTotal.Sub(legs.CreditTotal))
	}

	t.Logf("%s (%s): %d members at the ceiling book Rs %s of EPF administrative charge, topped up by Rs %s to the "+
		"Rs %s establishment minimum; Employer PF debit Rs %s, PF Payable credit Rs %s, voucher balances at Rs %s",
		month, ss.Version, members, establishmentAdmin, legs.EPFAdminChargeTopUp, floor,
		legAmount(LedgerEmployerPFContribution, DirectionDebit, TagStatutory),
		legAmount(LedgerPFPayable, DirectionCredit, TagStatutory), legs.DebitTotal)
}

// TestAggregateLegs_EPFAdminChargeFloorIsNotChargedPerEmployee is the other half
// of the floor: it must be applied ONCE to the establishment, never once per
// member.
//
// This is the failure mode pf.go refused to risk, and it is much larger than the
// one being fixed. Twenty members at the ceiling book Rs 1,500 of administrative
// charge between them, which is over the minimum and needs no top-up at all; a
// per-employee floor would charge Rs 10,000 — the employer would over-remit by
// Rs 8,500 a month and the ECR would not reconcile against the challan.
func TestAggregateLegs_EPFAdminChargeFloorIsNotChargedPerEmployee(t *testing.T) {
	const members = 20
	month := adminFloorMonth
	ss, err := SocialSecurityForMonth(month)
	if err != nil {
		t.Fatalf("SocialSecurityForMonth(%s): %v", month, err)
	}
	floor, err := ss.EPFAdminChargeMin.Use()
	if err != nil {
		t.Fatalf("no EPF administrative-charge minimum in %s: %v", ss.Version, err)
	}

	pf, err := PFDerive(PFInput{
		Month:         month,
		StatutoryWage: prDec(t, "15000"),
		Base:          PFBaseCeiling,
		EPSMember:     true,
		FirstMemberOn: time.Date(2010, time.April, 1, 0, 0, 0, 0, time.UTC),
	}, ss)
	if err != nil {
		t.Fatalf("PFDerive: %v", err)
	}
	adminPerMember, ok := pf.ComponentByName(ComponentEPFAdminCharges)
	if !ok {
		t.Fatalf("no EPF_ADMIN_CHARGES component under %s", ss.Version)
	}
	n := decimal.NewFromInt(members)
	establishmentAdmin := adminPerMember.Amount.Mul(n)
	if establishmentAdmin.LessThan(floor) {
		t.Fatalf("%d members book only Rs %s, under the Rs %s minimum; this test needs an establishment that is "+
			"ALREADY over the floor", members, establishmentAdmin, floor)
	}

	gross := prDec(t, "15000")
	slips := make([]Payslip, 0, members)
	for i := 0; i < members; i++ {
		p := pf
		slips = append(slips, Payslip{
			EmployeeRef:                  "MEMBER-" + decimal.NewFromInt(int64(i)).String(),
			Month:                        month,
			SocialSecurityRuleSetVersion: ss.Version,
			IncomeTaxRuleSetVersion:      "it-ty-2026-27",
			GrossEarnings:                gross,
			EmployeeDeductions:           p.EmployeeEPF,
			NetPay:                       gross.Sub(p.EmployeeEPF),
			EmployerContributions:        p.EmployerEPF.Add(p.EPS),
			PF:                           &p,
		})
	}
	legs, err := AggregateLegs(slips)
	if err != nil {
		t.Fatalf("AggregateLegs: %v", err)
	}
	if !legs.EPFAdminChargeTopUp.IsZero() {
		t.Errorf("%d members at the ceiling already book Rs %s of EPF administrative charge, over the Rs %s "+
			"ESTABLISHMENT minimum, yet the run adds a top-up of Rs %s. The minimum is charged once per "+
			"establishment per month; applying it per member would over-remit Rs %s and the ECR would not "+
			"reconcile against the challan",
			members, establishmentAdmin, floor, legs.EPFAdminChargeTopUp,
			floor.Mul(n).Sub(establishmentAdmin))
	}
}

// ---------------------------------------------------------------------------
// WHAT THIS BUILD BOOKS FROM JUNE 2026 ONWARD: THE EDLI PREMIUM, NOT THE
// ADMINISTRATIVE CHARGES
// ---------------------------------------------------------------------------

// TestSocialSecurity_FromJune2026EDLIIsBookedButAdminChargesAreNot pins the split
// between a charge that is now on record and two that are not.
//
// SocialSecurityFor resolves a wage month on its LAST day, and the ss-2026-07
// window opens on 29 June 2026, so the whole of June 2026 resolves to ss-2026-07
// even though the saved 1976 Scheme was operative for 1-28 June. That boundary is
// a DELIBERATE choice, not an accident of last-day resolution, and this test still
// pins it.
//
// WHAT CHANGED. This test used to assert that June booked NOTHING, and its own
// log called that a release blocker: about Rs 75 per member per month of EDLI
// premium accruing unbooked, with s.14B interest on top. S.O. 3581(E) of 1 July
// 2026 (0.50% w.e.f. 29 June 2026) closed it, so the EDLI premium is now BOOKED
// and this test asserts that instead — which is what stops a later "let's be safe"
// change from re-opening the liability.
//
// WHAT DID NOT CHANGE. The two administrative charges are still refused: no
// standalone rate notification exists for them as at 30 July 2026. That is a
// DISCLOSED GAP and the deferral is the honest behaviour — inventing a rate would
// charge a real employer a figure nobody has. Note they are PENDING, not
// abolished: EPF Scheme 2026 para 29(2) caps a late-return fee at "the
// administrative charges payable ... for the relevant month", which presupposes
// the charge exists.
func TestSocialSecurity_FromJune2026EDLIIsBookedButAdminChargesAreNot(t *testing.T) {
	wage := prDec(t, "15000")
	derive := func(m WageMonth) (SocialSecurityRuleSet, PFResult) {
		t.Helper()
		ss, err := SocialSecurityForMonth(m)
		if err != nil {
			t.Fatalf("SocialSecurityForMonth(%s): %v", m, err)
		}
		pf, err := PFDerive(PFInput{
			Month:         m,
			StatutoryWage: wage,
			Base:          PFBaseCeiling,
			EPSMember:     true,
			FirstMemberOn: time.Date(2010, time.April, 1, 0, 0, 0, 0, time.UTC),
		}, ss)
		if err != nil {
			t.Fatalf("PFDerive(%s): %v", m, err)
		}
		return ss, pf
	}

	// THE CONTRAST, and the anti-vacuity gate: MAY 2026, one month earlier, is
	// still ss-2025-11 and books both charges. Without this the test would pass
	// just as well against an engine that had forgotten EDLI entirely.
	may := WageMonth{Year: 2026, Month: time.May}
	mayRS, mayPF := derive(may)
	mayEDLI, ok := mayPF.ComponentByName(ComponentEDLIContribution)
	if !ok || !mayEDLI.Amount.Equal(prDec(t, "75")) {
		t.Fatalf("%s (%s) must book an EDLI premium of Rs 75 for a member at the ceiling (got present=%v amount=%s). "+
			"Without a month that DOES book the charge, this test cannot show that June books nothing",
			may, mayRS.Version, ok, mayEDLI.Amount)
	}
	mayAdmin, ok := mayPF.ComponentByName(ComponentEPFAdminCharges)
	if !ok || !mayAdmin.Amount.Equal(prDec(t, "75")) {
		t.Fatalf("%s (%s) must book an EPF administrative charge of Rs 75 for a member at the ceiling "+
			"(got present=%v amount=%s)", may, mayRS.Version, ok, mayAdmin.Amount)
	}

	// JUNE 2026 and every month after it. June is the boundary month — the one
	// the last-day rule moves wholesale into the new window — and August is an
	// ordinary month well inside it.
	for _, m := range []WageMonth{
		{Year: 2026, Month: time.June},
		{Year: 2026, Month: time.July},
		{Year: 2026, Month: time.August},
		{Year: 2027, Month: time.January},
	} {
		ss, pf := derive(m)
		if ss.Version == mayRS.Version {
			t.Fatalf("%s still resolves to %s. The window boundary this test is about has moved; re-derive the "+
				"expectations rather than deleting the test", m, ss.Version)
		}
		// THE EDLI PREMIUM IS BOOKED. Rs 75 for a member at the ceiling — the same
		// figure May books under the superseded Scheme, because S.O. 3581(E)
		// carried the 1976 rate forward unchanged.
		edli, present := pf.ComponentByName(ComponentEDLIContribution)
		if !present {
			t.Errorf("%s (%s) does not book the EDLI premium. S.O. 3581(E) of 1 July 2026 sets it at 0.50%% of "+
				"EDLI wages w.e.f. 29 June 2026; refusing it leaves the employer liability accruing unbooked",
				m, ss.Version)
		} else if !edli.Amount.Equal(prDec(t, "75")) {
			t.Errorf("%s (%s) books EDLI = Rs %s, want Rs 75 (0.50%% of the Rs 15,000 ceiling)",
				m, ss.Version, edli.Amount)
		}
		if _, deferred := pf.DeferralFor(ComponentEDLIContribution); deferred {
			t.Errorf("%s (%s) DEFERS the EDLI premium as well as/instead of booking it. The rate is on record",
				m, ss.Version)
		}

		// THE ADMINISTRATIVE CHARGES ARE NOT.
		for _, name := range pfDeferredChargeComponents {
			if c, present := pf.ComponentByName(name); present {
				t.Errorf("%s (%s) books %s = Rs %s. No standalone administrative-charge rate notification exists "+
					"under the 2026 Schemes; a number here is an invented rate charged to a real employer. Do NOT "+
					"reach for S.O. 2701(E) — that is inspection charges on exempted establishments",
					m, ss.Version, name, c.Amount)
			}
			def, ok := pf.DeferralFor(name)
			if !ok {
				t.Errorf("%s (%s) neither books %s nor DEFERS it. An absent charge that says nothing is "+
					"indistinguishable from a charge of nil, and the employer never learns a liability is accruing",
					m, ss.Version, name)
				continue
			}
			if len(def.Reason) < 40 || len(def.Citation) < 40 {
				t.Errorf("%s (%s) %s deferral is too thin to act on: reason %q, citation %q",
					m, ss.Version, name, def.Reason, def.Citation)
			}
		}

		// The legs confirm the consequence in money. EmployerContributions below
		// is deliberately EmployerEPF + EPS only — the caller's figure — so the
		// employer's PF debit is the member's account plus whatever tagged
		// statutory components the aggregator adds, which now includes the EDLI
		// premium and still excludes both administrative charges.
		slip := Payslip{
			EmployeeRef:                  "MEMBER-A",
			Month:                        m,
			SocialSecurityRuleSetVersion: ss.Version,
			IncomeTaxRuleSetVersion:      "it-ty-2026-27",
			GrossEarnings:                wage,
			EmployeeDeductions:           pf.EmployeeEPF,
			NetPay:                       wage.Sub(pf.EmployeeEPF),
			EmployerContributions:        pf.EmployerEPF.Add(pf.EPS),
			PF:                           &pf,
		}
		legs, err := AggregateLegs([]Payslip{slip})
		if err != nil {
			t.Fatalf("%s: AggregateLegs: %v", m, err)
		}
		var employerPF decimal.Decimal
		for _, l := range legs.Legs {
			if l.Ledger == LedgerEmployerPFContribution && l.Direction == DirectionDebit {
				employerPF = employerPF.Add(l.Amount)
			}
		}
		// The member's account PLUS the EDLI premium, and nothing else: the two
		// administrative charges are still deferred, so they add nothing here.
		wantEmployerPF := pf.EmployerEPF.Add(pf.EPS).Add(prDec(t, "75"))
		if !employerPF.Equal(wantEmployerPF) {
			t.Errorf("%s (%s): Employer PF Contribution debit Rs %s, want Rs %s — the member's account plus the "+
				"Rs 75 EDLI premium. Both administrative charges are still deferred and must add nothing",
				m, ss.Version, employerPF, wantEmployerPF)
		}
		if !legs.EPFAdminChargeTopUp.IsZero() {
			t.Errorf("%s (%s): the run tops the EPF administrative charge up by Rs %s, but no administrative-charge "+
				"rate OR minimum is verified in this window. That top-up is an invented statutory amount",
				m, ss.Version, legs.EPFAdminChargeTopUp)
		}
	}

	t.Logf("PARTIALLY CLOSED: from June 2026 (window %s) this build now books the EDLI premium of Rs %s per member "+
		"per month, on S.O. 3581(E) of 1 July 2026 — the same figure the superseded %s window books, because the "+
		"1976 rate was carried forward unchanged. STILL A DISCLOSED GAP: both EPF/EDLI administrative charges are "+
		"refused, so an amount that was Rs 500 per establishment per month under the superseded Scheme is accruing "+
		"unbooked, with s.14B interest and s.14 damages. They are PENDING, not abolished — para 29(2) caps a late "+
		"fee at them. No standalone rate notification existed as at 30 July 2026",
		"ss-2026-07", mayEDLI.Amount, mayRS.Version)
}
