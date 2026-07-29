package payroll

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// TestStatutoryWages_FiftyPercentDeemingAddsBack is the guard on the number
// every other payroll figure is a percentage of.
//
// The consequence if the deeming proviso is dropped: an employer structures a
// 50,000 CTC as 15,000 basic and 35,000 "special allowance / HRA / conveyance",
// and PF is computed on 15,000 instead of 25,000. That is 1,200 a month less
// into the employee's own provident fund, every month of their service, and it
// is a shortfall the employer owes with interest under s.14B when the inspector
// finds it. It is also completely invisible on the payslip.
func TestStatutoryWages_FiftyPercentDeemingAddsBack(t *testing.T) {
	rs, err := SocialSecurityForMonth(WageMonth{Year: 2026, Month: time.August})
	if err != nil {
		t.Fatalf("SocialSecurityForMonth: %v", err)
	}
	if rs.Version != "ss-2026-07" {
		t.Fatalf("resolved %q, want ss-2026-07 (the Code's 50%% proviso window)", rs.Version)
	}

	t.Run("the proviso bites", func(t *testing.T) {
		// Hand derivation, from the statute:
		//   included  = basic 15,000                                    = 15,000
		//   excluded  = HRA 12,000 + conveyance 5,000 + overtime 3,000
		//               + bonus 4,000 + commission 11,000               = 35,000
		//   total remuneration                                          = 50,000
		//   50% floor                                                   = 25,000
		//   excluded 35,000 > 25,000, so add back 35,000 - 25,000       = 10,000
		//   statutory wage = 15,000 + 10,000                            = 25,000
		got, err := StatutoryWages(WageInput{
			Month: WageMonth{Year: 2026, Month: time.August},
			Components: []WageComponent{
				{Type: ComponentBasic, Amount: d(t, "15000")},
				{Type: ComponentHouseRentAllowance, Amount: d(t, "12000")},
				{Type: ComponentConveyanceAllowance, Amount: d(t, "5000")},
				{Type: ComponentOvertimeAllowance, Amount: d(t, "3000")},
				{Type: ComponentBonus, Amount: d(t, "4000")},
				{Type: ComponentCommission, Amount: d(t, "11000")},
			},
		}, rs)
		if err != nil {
			t.Fatalf("StatutoryWages: %v", err)
		}
		assertMoney(t, "TotalRemuneration", got.TotalRemuneration, "50000")
		assertMoney(t, "IncludedComponents", got.IncludedComponents, "15000")
		assertMoney(t, "ExcludedComponents", got.ExcludedComponents, "35000")
		assertMoney(t, "DeemedAddBack", got.DeemedAddBack, "10000")
		assertMoney(t, "StatutoryWage", got.StatutoryWage, "25000")
		if got.RuleSetVersion != "ss-2026-07" {
			t.Errorf("RuleSetVersion = %q, want ss-2026-07", got.RuleSetVersion)
		}
		if got.PeriodBasis != BasisMonthly {
			t.Errorf("PeriodBasis = %q, want MONTHLY — the statutory wage is a wage-month quantity", got.PeriodBasis)
		}
		if got.Month != (WageMonth{Year: 2026, Month: time.August}) {
			t.Errorf("Month = %s, want 2026-08", got.Month)
		}
		// The naive derivation — wages = the included components only — gives
		// 15,000. If the two ever agree, this fixture has stopped demonstrating
		// the proviso.
		if got.StatutoryWage.Equal(got.IncludedComponents) {
			t.Error("fixture no longer demonstrates the 50% deeming: the statutory wage equals the included " +
				"components, so the add-back did not fire and PF would be computed on the structured-down basic")
		}
	})

	t.Run("the proviso does not bite when the exclusions are under half", func(t *testing.T) {
		//   included 30,000; excluded 10,000; total 40,000; floor 20,000.
		//   10,000 < 20,000, so no add-back and the wage is simply the included sum.
		got, err := StatutoryWages(WageInput{
			Month: WageMonth{Year: 2026, Month: time.August},
			Components: []WageComponent{
				{Type: ComponentBasic, Amount: d(t, "20000")},
				{Type: ComponentDearnessAllowance, Amount: d(t, "6000")},
				{Type: ComponentSpecialAllowance, Amount: d(t, "4000")},
				{Type: ComponentHouseRentAllowance, Amount: d(t, "10000")},
			},
		}, rs)
		if err != nil {
			t.Fatalf("StatutoryWages: %v", err)
		}
		assertMoney(t, "TotalRemuneration", got.TotalRemuneration, "40000")
		assertMoney(t, "DeemedAddBack", got.DeemedAddBack, "0")
		assertMoney(t, "StatutoryWage", got.StatutoryWage, "30000")
	})

	t.Run("exactly at the boundary does not add back", func(t *testing.T) {
		// The proviso says the excess applies where the exclusions EXCEED one
		// half. Exactly one half is not an excess. included 25,000, excluded
		// 25,000, floor 25,000 -> add-back zero, wage 25,000.
		got, err := StatutoryWages(WageInput{
			Month: WageMonth{Year: 2026, Month: time.August},
			Components: []WageComponent{
				{Type: ComponentBasic, Amount: d(t, "25000")},
				{Type: ComponentHouseRentAllowance, Amount: d(t, "25000")},
			},
		}, rs)
		if err != nil {
			t.Fatalf("StatutoryWages: %v", err)
		}
		assertMoney(t, "DeemedAddBack", got.DeemedAddBack, "0")
		assertMoney(t, "StatutoryWage", got.StatutoryWage, "25000")

		// One paisa over the half DOES add back — the boundary is tested on both
		// sides so a >= / > flip cannot survive.
		over, err := StatutoryWages(WageInput{
			Month: WageMonth{Year: 2026, Month: time.August},
			Components: []WageComponent{
				{Type: ComponentBasic, Amount: d(t, "24999.99")},
				{Type: ComponentHouseRentAllowance, Amount: d(t, "25000.01")},
			},
		}, rs)
		if err != nil {
			t.Fatalf("StatutoryWages: %v", err)
		}
		assertMoney(t, "DeemedAddBack (one paisa over half)", over.DeemedAddBack, "0.01")
		assertMoney(t, "StatutoryWage (one paisa over half)", over.StatutoryWage, "25000")
	})

	t.Run("the algebraic invariant holds for arbitrary splits", func(t *testing.T) {
		// statutory wage == max(included, threshold% of total), independently of
		// the add-back arithmetic. This is the check that would still catch a
		// sign error inside the add-back branch.
		for _, c := range []struct{ basic, hra string }{
			{"10000", "40000"}, {"25000", "25000"}, {"40000", "10000"},
			{"1", "99999"}, {"99999", "1"}, {"0", "50000"}, {"50000", "0"},
			{"33333.33", "16666.67"},
		} {
			got, err := StatutoryWages(WageInput{
				Month: WageMonth{Year: 2026, Month: time.August},
				Components: []WageComponent{
					{Type: ComponentBasic, Amount: d(t, c.basic)},
					{Type: ComponentHouseRentAllowance, Amount: d(t, c.hra)},
				},
			}, rs)
			if err != nil {
				t.Fatalf("StatutoryWages(basic %s, hra %s): %v", c.basic, c.hra, err)
			}
			half := got.TotalRemuneration.Shift(-1).Mul(decimal.NewFromInt(5))
			want := got.IncludedComponents
			if half.GreaterThan(want) {
				want = half
			}
			if !got.StatutoryWage.Equal(want) {
				t.Errorf("basic %s + HRA %s: statutory wage = %s, want max(included %s, half of total %s) = %s",
					c.basic, c.hra, got.StatutoryWage, got.IncludedComponents, half, want)
			}
			if got.StatutoryWage.GreaterThan(got.TotalRemuneration) {
				t.Errorf("basic %s + HRA %s: statutory wage %s exceeds total remuneration %s — the add-back "+
					"cannot create money", c.basic, c.hra, got.StatutoryWage, got.TotalRemuneration)
			}
		}
	})

	t.Run("the exclusion list is exhaustive: unlisted allowances ARE wages", func(t *testing.T) {
		// Vivekananda Vidyamandir (SC, 2019): a special allowance paid to all is
		// wages. Treating SPECIAL_ALLOWANCE or OTHER_ALLOWANCE as excluded is the
		// single most common PF under-contribution, so it is asserted directly.
		got, err := StatutoryWages(WageInput{
			Month: WageMonth{Year: 2026, Month: time.August},
			Components: []WageComponent{
				{Type: ComponentBasic, Amount: d(t, "10000")},
				{Type: ComponentSpecialAllowance, Amount: d(t, "20000")},
				{Type: ComponentOtherAllowance, Amount: d(t, "5000")},
				{Type: ComponentRetainingAllowance, Amount: d(t, "5000")},
			},
		}, rs)
		if err != nil {
			t.Fatalf("StatutoryWages: %v", err)
		}
		assertMoney(t, "ExcludedComponents", got.ExcludedComponents, "0")
		assertMoney(t, "StatutoryWage", got.StatutoryWage, "40000")
		if !got.StatutoryWage.Equal(got.TotalRemuneration) {
			t.Errorf("with no excluded component the statutory wage (%s) must equal total remuneration (%s); "+
				"anything less means a component fell through to the exclusion list",
				got.StatutoryWage, got.TotalRemuneration)
		}
	})

	t.Run("every excluded component really is excluded", func(t *testing.T) {
		// One case per exclusion, each paired with a large basic so the proviso
		// cannot fire and mask a misclassification.
		for _, ct := range []ComponentType{
			ComponentHouseRentAllowance, ComponentConveyanceAllowance, ComponentOvertimeAllowance,
			ComponentCommission, ComponentBonus, ComponentHouseAccommodationValue,
			ComponentSpecialExpenses, ComponentAwardOrSettlement, ComponentEmployerPFContribution,
			ComponentGratuity, ComponentRetrenchmentCompensation, ComponentExGratia,
		} {
			got, err := StatutoryWages(WageInput{
				Month: WageMonth{Year: 2026, Month: time.August},
				Components: []WageComponent{
					{Type: ComponentBasic, Amount: d(t, "90000")},
					{Type: ct, Amount: d(t, "10000")},
				},
			}, rs)
			if err != nil {
				t.Fatalf("StatutoryWages(%s): %v", ct, err)
			}
			if !got.ExcludedComponents.Equal(d(t, "10000")) {
				t.Errorf("%s: ExcludedComponents = %s, want 10000 — it is an s.2(88) exclusion and is being "+
					"counted as wages, over-contributing PF and ESI", ct, got.ExcludedComponents)
			}
			assertMoney(t, string(ct)+" statutory wage", got.StatutoryWage, "90000")
		}
	})

	t.Run("the pre-Code window has no add-back at all", func(t *testing.T) {
		// ss-2019-07 carries a DeemingThresholdPct of 100, which expresses "the
		// excluded share is never capped". A back-dated 2019 pay run must NOT be
		// recomputed under the Code's 2020 wage definition, which did not exist.
		old, err := SocialSecurityForMonth(WageMonth{Year: 2020, Month: time.January})
		if err != nil {
			t.Fatalf("SocialSecurityForMonth(2020-01): %v", err)
		}
		if old.Version != "ss-2019-07" {
			t.Fatalf("resolved %q, want ss-2019-07", old.Version)
		}
		got, err := StatutoryWages(WageInput{
			Month: WageMonth{Year: 2020, Month: time.January},
			Components: []WageComponent{
				{Type: ComponentBasic, Amount: d(t, "15000")},
				{Type: ComponentHouseRentAllowance, Amount: d(t, "35000")},
			},
		}, old)
		if err != nil {
			t.Fatalf("StatutoryWages: %v", err)
		}
		assertMoney(t, "DeemedAddBack (pre-Code)", got.DeemedAddBack, "0")
		assertMoney(t, "StatutoryWage (pre-Code)", got.StatutoryWage, "15000")
		if got.RuleSetVersion != "ss-2019-07" {
			t.Errorf("RuleSetVersion = %q, want ss-2019-07", got.RuleSetVersion)
		}
		// And the SAME input under the 2026 ruleset must differ, or this case is
		// not demonstrating the change of law.
		modern, err := StatutoryWages(WageInput{
			Month: WageMonth{Year: 2026, Month: time.August},
			Components: []WageComponent{
				{Type: ComponentBasic, Amount: d(t, "15000")},
				{Type: ComponentHouseRentAllowance, Amount: d(t, "35000")},
			},
		}, rs)
		if err != nil {
			t.Fatalf("StatutoryWages (modern): %v", err)
		}
		if modern.StatutoryWage.Equal(got.StatutoryWage) {
			t.Error("the pre-Code and Code wage definitions produce the same wage for a 30/70 structure, so the " +
				"effective-dated wage rule is doing nothing")
		}
	})
}

// TestStatutoryWages_TerminalPaymentsAreOutsideTheDeemingTest pins proviso 1 to
// s.2(88) of the Code on Social Security, 2020 to the clause range it actually
// names — "payments made by the employer to the employee UNDER CLAUSES (a) TO
// (i)".
//
// Clause (j) is gratuity and clause (k) is retrenchment compensation, other
// retirement benefit and ex gratia. All three are excluded from wages AND take
// no part in the "exceeds one-half" test, so they can never be deemed back in.
//
// The consequence when they are wrongly included, which is what this test caught:
// it fires on EVERY TERMINAL SETTLEMENT, i.e. the normal final payslip of every
// leaver, and it takes money out of a departing employee's last salary on the
// strength of the very gratuity they are owed.
func TestStatutoryWages_TerminalPaymentsAreOutsideTheDeemingTest(t *testing.T) {
	month := WageMonth{Year: 2026, Month: time.March}
	rs, err := SocialSecurityForMonth(month)
	if err != nil {
		t.Fatalf("SocialSecurityForMonth: %v", err)
	}
	if rs.Version != "ss-2025-11" {
		t.Fatalf("resolved %q, want ss-2025-11 (Code-era wage definition, pre-2026 schemes)", rs.Version)
	}

	t.Run("a leaver's final payslip", func(t *testing.T) {
		// Hand derivation, from the statute:
		//   total remuneration = 30,000 + 15,000 + 5,00,000 + 2,00,000 = 7,45,000
		//   ALL exclusions, clauses (a)-(k) = 15,000 + 5,00,000 + 2,00,000
		//                                                          = 7,15,000
		//   exclusions WITHIN clauses (a)-(i): HRA only            =   15,000
		//   50% floor on all remuneration                          = 3,72,500
		//   15,000 does NOT exceed 3,72,500, so the add-back is zero
		//   statutory wage = included 30,000 + 0                   =   30,000
		got, err := StatutoryWages(WageInput{
			Month: month,
			Components: []WageComponent{
				{Type: ComponentBasic, Amount: d(t, "30000")},
				{Type: ComponentHouseRentAllowance, Amount: d(t, "15000")},
				{Type: ComponentGratuity, Amount: d(t, "500000")},
				{Type: ComponentRetrenchmentCompensation, Amount: d(t, "200000")},
			},
		}, rs)
		if err != nil {
			t.Fatalf("StatutoryWages: %v", err)
		}

		assertMoney(t, "TotalRemuneration", got.TotalRemuneration, "745000")
		assertMoney(t, "IncludedComponents", got.IncludedComponents, "30000")
		// Both sums are echoed and they MUST differ here by exactly the terminal
		// payments, or the payslip cannot explain the absent add-back later.
		assertMoney(t, "ExcludedComponents (clauses (a)-(k))", got.ExcludedComponents, "715000")
		assertMoney(t, "ExcludedComponentsInDeemingTest (clauses (a)-(i))",
			got.ExcludedComponentsInDeemingTest, "15000")
		gap := got.ExcludedComponents.Sub(got.ExcludedComponentsInDeemingTest)
		if !gap.Equal(d(t, "700000")) {
			t.Errorf("ExcludedComponents - ExcludedComponentsInDeemingTest = %s, want 700000 — the gap between "+
				"the two echoed sums must be exactly the month's gratuity (5,00,000) and retrenchment "+
				"compensation (2,00,000), s.2(88)(j) and (k)", gap)
		}

		if !got.DeemedAddBack.IsZero() || !got.StatutoryWage.Equal(d(t, "30000")) {
			t.Fatalf("a leaver's final payslip carrying Rs 5,00,000 gratuity and Rs 2,00,000 retrenchment "+
				"compensation produced a statutory wage of Rs %s (add-back Rs %s) instead of Rs 30,000: the "+
				"deeming test in proviso 1 to s.2(88) is being run over ALL the exclusions instead of over "+
				"clauses (a) to (i) only, so the employee's own terminal payments are being deemed back into "+
				"their wages. That is Rs 41,100 deducted from a departing employee's final salary and remitted "+
				"to the EPFO on money s.2(88)(j) and (k) say is not a wage",
				got.StatutoryWage, got.DeemedAddBack)
		}

		// The PF leg, because the rupees in the message above are PF rupees.
		pf, err := PFDerive(PFInput{
			Month:         month,
			StatutoryWage: got.StatutoryWage,
			Base:          PFBaseActual,
			EPSMember:     true,
		}, rs)
		if err != nil {
			t.Fatalf("PFDerive: %v", err)
		}
		assertMoney(t, "EPFWages", pf.EPFWages, "30000")
		if !pf.EmployeeEPF.Equal(d(t, "3600")) {
			t.Errorf("EmployeeEPF = Rs %s, want Rs 3600. On the wrong base of Rs 3,72,500 this is Rs 44,700 — "+
				"Rs 41,100 taken out of the leaver's final salary, against a gratuity that is not a wage",
				pf.EmployeeEPF)
		}
		if !pf.EmployerEPF.Equal(d(t, "2350")) {
			t.Errorf("EmployerEPF = Rs %s, want Rs 2350. On the wrong base it is Rs 43,450 — the employer "+
				"over-remits Rs 41,100 of its own money on the same non-wage", pf.EmployerEPF)
		}
	})

	t.Run("the ESI leg, where the contribution is uncapped once coverage latches", func(t *testing.T) {
		// An employee latched covered at Rs 18,000 whose final month carries a
		// Rs 2,00,000 gratuity. total 2,18,000; exclusions within (a)-(i) are
		// NIL, so no add-back at all and the ESI wage is the Rs 18,000 basic.
		got, err := StatutoryWages(WageInput{
			Month: month,
			Components: []WageComponent{
				{Type: ComponentBasic, Amount: d(t, "18000")},
				{Type: ComponentGratuity, Amount: d(t, "200000")},
			},
		}, rs)
		if err != nil {
			t.Fatalf("StatutoryWages: %v", err)
		}
		assertMoney(t, "ExcludedComponentsInDeemingTest", got.ExcludedComponentsInDeemingTest, "0")
		if !got.StatutoryWage.Equal(d(t, "18000")) {
			t.Fatalf("a leaver with Rs 2,00,000 gratuity got a statutory wage of Rs %s instead of Rs 18,000",
				got.StatutoryWage)
		}

		esi, err := ESIDerive(ESIInput{
			Month:    month,
			Covered:  true,
			Wages:    got.StatutoryWage,
			PaidDays: 31,
		}, rs)
		if err != nil {
			t.Fatalf("ESIDerive: %v", err)
		}
		if !esi.EmployeeShare.Equal(d(t, "135")) {
			t.Errorf("ESI employee share = Rs %s, want Rs 135 (0.75%% of Rs 18,000). Deeming the gratuity back "+
				"in gives a wage of Rs 1,09,000 and a share of Rs 818 — and ESI has NO contribution ceiling, so "+
				"nothing downstream caps the error", esi.EmployeeShare)
		}
		if !esi.EmployerShare.Equal(d(t, "585")) {
			t.Errorf("ESI employer share = Rs %s, want Rs 585 (3.25%% of Rs 18,000); on the deemed Rs 1,09,000 "+
				"wage it is Rs 3,543", esi.EmployerShare)
		}
	})

	t.Run("each terminal payment on its own cannot trigger an add-back", func(t *testing.T) {
		// One case per clause (j)/(k) member, each large enough that it WOULD have
		// dominated the old whole-exclusion test on its own.
		for _, ct := range []ComponentType{
			ComponentGratuity, ComponentRetrenchmentCompensation, ComponentExGratia,
		} {
			got, err := StatutoryWages(WageInput{
				Month: month,
				Components: []WageComponent{
					{Type: ComponentBasic, Amount: d(t, "20000")},
					{Type: ct, Amount: d(t, "400000")},
				},
			}, rs)
			if err != nil {
				t.Fatalf("StatutoryWages(%s): %v", ct, err)
			}
			if !got.DeemedAddBack.IsZero() {
				t.Errorf("%s of Rs 4,00,000 produced a deemed add-back of Rs %s and a statutory wage of Rs %s "+
					"instead of Rs 20,000 — a payment outside clauses (a)-(i) is being tested against the "+
					"one-half floor, so a leaver's terminal settlement is inflating the PF and ESI base it is "+
					"itself exempt from", ct, got.DeemedAddBack, got.StatutoryWage)
			}
			assertMoney(t, string(ct)+" statutory wage", got.StatutoryWage, "20000")
		}
	})

	t.Run("a terminal payment can only ever REDUCE an add-back, never create one", func(t *testing.T) {
		// The asymmetry the proviso writes: the FLOOR is a share of all
		// remuneration (which the gratuity enlarges) while the sum TESTED is only
		// (a)-(i) (which it does not). So adding a terminal payment to a month
		// that already has an add-back must shrink it, never grow it.
		base := []WageComponent{
			{Type: ComponentBasic, Amount: d(t, "15000")},
			{Type: ComponentHouseRentAllowance, Amount: d(t, "35000")},
		}
		without, err := StatutoryWages(WageInput{Month: month, Components: base}, rs)
		if err != nil {
			t.Fatalf("StatutoryWages: %v", err)
		}
		assertMoney(t, "DeemedAddBack without gratuity", without.DeemedAddBack, "10000")

		with, err := StatutoryWages(WageInput{
			Month:      month,
			Components: append(append([]WageComponent{}, base...), WageComponent{Type: ComponentGratuity, Amount: d(t, "50000")}),
		}, rs)
		if err != nil {
			t.Fatalf("StatutoryWages: %v", err)
		}
		// total 1,00,000; floor 50,000; (a)-(i) sum is HRA 35,000 <= 50,000 -> nil.
		assertMoney(t, "DeemedAddBack with gratuity", with.DeemedAddBack, "0")
		if with.DeemedAddBack.GreaterThan(without.DeemedAddBack) {
			t.Errorf("adding a Rs 50,000 gratuity RAISED the add-back from Rs %s to Rs %s; a payment that is "+
				"not in the deeming test can only raise the floor and shrink the add-back",
				without.DeemedAddBack, with.DeemedAddBack)
		}
	})
}

// TestStatutoryWages_DeemingTestCoversClausesAToI pins the two sets against each
// other directly, so a component added to one map and not the other cannot pass.
func TestStatutoryWages_DeemingTestCoversClausesAToI(t *testing.T) {
	// Clauses (j) and (k): outside the deeming test by the terms of proviso 1.
	outside := map[ComponentType]bool{
		ComponentGratuity:                 true,
		ComponentRetrenchmentCompensation: true,
		ComponentExGratia:                 true,
	}

	for ct := range deemingTestClauses {
		if !excludedFromWages[ct] {
			t.Errorf("%s is in the deeming test but is not an s.2(88) exclusion at all; the proviso tests "+
				"PAYMENTS UNDER CLAUSES (a) TO (i), all of which are exclusions", ct)
		}
		if outside[ct] {
			t.Errorf("%s is a clause (j)/(k) terminal payment and must NOT be in the deeming test — including "+
				"it deems a leaver's gratuity back into their wages and deducts PF and ESI from it", ct)
		}
	}

	for ct := range excludedFromWages {
		if outside[ct] {
			if deemingTestClauses[ct] {
				t.Errorf("%s (clause (j)/(k)) is in deemingTestClauses", ct)
			}
			continue
		}
		if !deemingTestClauses[ct] {
			t.Errorf("%s is an s.2(88)(a)-(i) exclusion that is NOT in the deeming test; leaving it out "+
				"lets an employer park remuneration there and escape the one-half floor, under-contributing "+
				"PF and ESI for every employee who has it", ct)
		}
	}

	if len(excludedFromWages)-len(deemingTestClauses) != len(outside) {
		t.Fatalf("excludedFromWages has %d members and deemingTestClauses has %d; they must differ by exactly "+
			"the %d clause (j)/(k) terminal payments", len(excludedFromWages), len(deemingTestClauses), len(outside))
	}
	if len(deemingTestClauses) != 9 {
		t.Errorf("deemingTestClauses has %d members, want 9 — one per clause (a) to (i)", len(deemingTestClauses))
	}
}

// TestStatutoryWages_Refusals pins every input the wage leaf must reject rather
// than interpret.
func TestStatutoryWages_Refusals(t *testing.T) {
	rs, err := SocialSecurityForMonth(WageMonth{Year: 2026, Month: time.August})
	if err != nil {
		t.Fatalf("SocialSecurityForMonth: %v", err)
	}

	t.Run("an unknown component type", func(t *testing.T) {
		_, err := StatutoryWages(WageInput{
			Month: WageMonth{Year: 2026, Month: time.August},
			Components: []WageComponent{
				{Type: ComponentBasic, Amount: d(t, "30000")},
				{Type: ComponentType("LEAVE_TRAVEL_ALLOWANCE"), Amount: d(t, "5000")},
			},
		}, rs)
		if !errors.Is(err, ErrUnknownComponentType) {
			t.Fatalf("err = %v, want ErrUnknownComponentType. A component silently treated as excluded would "+
				"under-contribute PF and ESI for every employee who has it, with no error anywhere", err)
		}
	})

	t.Run("a negative amount", func(t *testing.T) {
		_, err := StatutoryWages(WageInput{
			Month:      WageMonth{Year: 2026, Month: time.August},
			Components: []WageComponent{{Type: ComponentBasic, Amount: d(t, "-1")}},
		}, rs)
		if !errors.Is(err, ErrNegativeStatutoryAmount) {
			t.Fatalf("err = %v, want ErrNegativeStatutoryAmount", err)
		}
	})

	t.Run("an empty ruleset", func(t *testing.T) {
		_, err := StatutoryWages(WageInput{
			Month:      WageMonth{Year: 2026, Month: time.August},
			Components: []WageComponent{{Type: ComponentBasic, Amount: d(t, "30000")}},
		}, SocialSecurityRuleSet{})
		if !errors.Is(err, ErrNoRuleSet) {
			t.Fatalf("err = %v, want ErrNoRuleSet. The zero ruleset carries a zero deeming threshold, which "+
				"would add back EVERY excluded rupee; refusing it is what stops a resolver error from becoming "+
				"a wrong number", err)
		}
	})

	t.Run("an unverified deeming threshold", func(t *testing.T) {
		// If the wage rule itself were ever unverified, the engine must refuse
		// rather than compute a wage under a rate nobody has checked.
		broken := rs
		broken.DeemingThresholdPct.Confidence = ConfUnverified
		_, err := StatutoryWages(WageInput{
			Month:      WageMonth{Year: 2026, Month: time.August},
			Components: []WageComponent{{Type: ComponentBasic, Amount: d(t, "30000")}},
		}, broken)
		if !errors.Is(err, ErrUnverifiedRate) {
			t.Fatalf("err = %v, want ErrUnverifiedRate", err)
		}
	})

	t.Run("no components at all is zero, not an error", func(t *testing.T) {
		// An employee on unpaid leave for a whole month has no remuneration. That
		// is a legitimate zero and must not be an error, or the pay run for the
		// rest of the company fails with them.
		got, err := StatutoryWages(WageInput{Month: WageMonth{Year: 2026, Month: time.August}}, rs)
		if err != nil {
			t.Fatalf("StatutoryWages with no components: %v", err)
		}
		assertMoney(t, "StatutoryWage", got.StatutoryWage, "0")
		assertMoney(t, "TotalRemuneration", got.TotalRemuneration, "0")
	})
}

func assertMoney(t *testing.T, name string, got decimal.Decimal, want string) {
	t.Helper()
	w := d(t, want)
	if !got.Equal(w) {
		t.Errorf("%s = %s, want %s", name, got, w)
	}
	if got.Exponent() < -4 {
		t.Errorf("%s = %s carries more than 4 dp; NUMERIC(19,4) would round it a second time", name, got)
	}
}
