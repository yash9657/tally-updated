package payroll

import (
	"errors"
	"testing"
	"time"
)

// wageHistory builds an effective-dated wage history from date/amount pairs.
func wageHistory(t *testing.T, pairs ...string) []WageRevision {
	t.Helper()
	if len(pairs)%2 != 0 {
		t.Fatalf("wageHistory needs date/amount pairs, got %d values", len(pairs))
	}
	out := make([]WageRevision, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, WageRevision{
			EffectiveFrom: ssDay(t, pairs[i]),
			MonthlyWages:  d(t, pairs[i+1]),
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// The latch.
// ---------------------------------------------------------------------------

// TestESICoverage_MidPeriodRaiseDoesNotEndCoverage is THE test for the proviso
// to Rule 50, and the one the naive monthly implementation fails.
//
// An employee on 20,000 at 1 April is covered. A raise to 26,000 effective
// 1 July takes them past the 21,000 ceiling MID-PERIOD. They remain covered
// until 30 September, and the contribution for July, August and September is
// computed on the ACTUAL 26,000 — ESI has a coverage ceiling, not a contribution
// ceiling. Coverage lapses only from 1 October.
//
// What breaks without this: the monthly test drops them in July, which cuts off
// medical benefit mid-treatment for the employee and under-remits the employer,
// recoverable by an ESIC inspection with interest and damages.
func TestESICoverage_MidPeriodRaiseDoesNotEndCoverage(t *testing.T) {
	rs := ssFixture(t)
	revisions := wageHistory(t, "2026-04-01", "20000.0000", "2026-07-01", "26000.0000")

	// Every month of the April period, including the three after the raise.
	for _, m := range []time.Month{time.April, time.May, time.June, time.July, time.August, time.September} {
		t.Run("covered in "+m.String(), func(t *testing.T) {
			res, err := ESICoverage(CoverageInput{
				Month:     WageMonth{Year: 2026, Month: m},
				Revisions: revisions,
			}, rs)
			if err != nil {
				t.Fatalf("ESICoverage: %v", err)
			}
			if !res.Covered {
				t.Errorf("Covered = false for %s (wages tested %s on %s) — the proviso to Rule 50 keeps an employee covered to the END of the contribution period once they were covered at its start; a monthly test cuts off medical benefit mid-treatment and under-remits the employer",
					m, res.WagesTested, res.WagesTestedOn.Format("2006-01-02"))
			}
			// The single evaluation instant is the PERIOD START, never this month.
			if !res.WagesTestedOn.Equal(ssDay(t, "2026-04-01")) {
				t.Errorf("WagesTestedOn = %s, want 2026-04-01 — a test instant inside the wage month is a MONTHLY test masquerading as a latch",
					res.WagesTestedOn.Format("2006-01-02"))
			}
			if !res.WagesTested.Equal(d(t, "20000")) {
				t.Errorf("WagesTested = %s, want 20000 (the wage in force at the period start, not this month's)", res.WagesTested)
			}
			if res.Reason != CoverageReasonAtOrBelowLimit {
				t.Errorf("Reason = %q, want %q", res.Reason, CoverageReasonAtOrBelowLimit)
			}
			if res.PeriodBasis != BasisContributionPeriod {
				t.Errorf("PeriodBasis = %q, want %q", res.PeriodBasis, BasisContributionPeriod)
			}
			if !res.PeriodStart.Equal(ssDay(t, "2026-04-01")) || !res.PeriodEnd.Equal(ssDay(t, "2026-09-30")) {
				t.Errorf("period = [%s, %s], want [2026-04-01, 2026-09-30]",
					res.PeriodStart.Format("2006-01-02"), res.PeriodEnd.Format("2006-01-02"))
			}
		})
	}

	// The contribution for July is on the ACTUAL 26,000, uncapped.
	t.Run("July contribution is on the actual wage, not the ceiling", func(t *testing.T) {
		cov, err := ESICoverage(CoverageInput{
			Month:     WageMonth{Year: 2026, Month: time.July},
			Revisions: revisions,
		}, rs)
		if err != nil {
			t.Fatalf("ESICoverage: %v", err)
		}
		res, err := ESIDerive(ESIInput{
			Month:    WageMonth{Year: 2026, Month: time.July},
			Covered:  cov.Covered,
			Wages:    d(t, "26000.0000"),
			PaidDays: 31,
		}, rs)
		if err != nil {
			t.Fatalf("ESIDerive: %v", err)
		}
		wantMoney(t, "employee share", res.EmployeeShare, "195",
			"0.75% of the ACTUAL 26,000; capping at the 21,000 coverage ceiling would give 158 and under-remit")
		wantMoney(t, "employer share", res.EmployerShare, "845",
			"3.25% of the ACTUAL 26,000; capping at the coverage ceiling would give 683")
		wantMoney(t, "Wages", res.Wages, "26000", "ESI has a COVERAGE ceiling, not a CONTRIBUTION ceiling")
	})

	// The next period re-tests, and coverage lapses.
	t.Run("coverage lapses from the next period", func(t *testing.T) {
		res, err := ESICoverage(CoverageInput{
			Month:     WageMonth{Year: 2026, Month: time.October},
			Revisions: revisions,
		}, rs)
		if err != nil {
			t.Fatalf("ESICoverage: %v", err)
		}
		if res.Covered {
			t.Error("Covered = true in October — the latch holds only to the END of the period; it does not make coverage permanent")
		}
		if !res.WagesTestedOn.Equal(ssDay(t, "2026-10-01")) {
			t.Errorf("WagesTestedOn = %s, want 2026-10-01", res.WagesTestedOn.Format("2006-01-02"))
		}
		if res.Reason != CoverageReasonExceedsLimit {
			t.Errorf("Reason = %q, want %q", res.Reason, CoverageReasonExceedsLimit)
		}
	})
}

// TestESICoverage_AboveLimitAtPeriodStartStaysOutAllPeriod is the SAME latch in
// the opposite direction, and it is why the latch cannot be implemented as
// "once covered, stay covered".
//
// An employee on 26,000 at 1 April is outside the scheme. A reduction to 18,000
// effective 1 July does NOT bring them in: a monthly test would, deducting a
// contribution that was never due and registering a coverage the employee does
// not have — which then fails when they try to claim against it.
func TestESICoverage_AboveLimitAtPeriodStartStaysOutAllPeriod(t *testing.T) {
	rs := ssFixture(t)
	revisions := wageHistory(t, "2026-04-01", "26000.0000", "2026-07-01", "18000.0000")

	for _, m := range []time.Month{time.April, time.May, time.June, time.July, time.August, time.September} {
		t.Run("not covered in "+m.String(), func(t *testing.T) {
			res, err := ESICoverage(CoverageInput{
				Month:     WageMonth{Year: 2026, Month: m},
				Revisions: revisions,
			}, rs)
			if err != nil {
				t.Fatalf("ESICoverage: %v", err)
			}
			if res.Covered {
				t.Errorf("Covered = true for %s (wages tested %s on %s) — an employee above the ceiling at the period START is out for the WHOLE period; a monthly test pulls them in on any dip, deducting a contribution that was never due and registering a coverage they cannot claim against",
					m, res.WagesTested, res.WagesTestedOn.Format("2006-01-02"))
			}
			if !res.WagesTestedOn.Equal(ssDay(t, "2026-04-01")) {
				t.Errorf("WagesTestedOn = %s, want 2026-04-01 — the latch has one instant per period", res.WagesTestedOn.Format("2006-01-02"))
			}
			if !res.WagesTested.Equal(d(t, "26000")) {
				t.Errorf("WagesTested = %s, want 26000", res.WagesTested)
			}
			if res.Reason != CoverageReasonExceedsLimit {
				t.Errorf("Reason = %q, want %q", res.Reason, CoverageReasonExceedsLimit)
			}
		})
	}

	t.Run("and comes in from the next period", func(t *testing.T) {
		res, err := ESICoverage(CoverageInput{
			Month:     WageMonth{Year: 2026, Month: time.October},
			Revisions: revisions,
		}, rs)
		if err != nil {
			t.Fatalf("ESICoverage: %v", err)
		}
		if !res.Covered {
			t.Error("Covered = false in October — the reduction takes effect from the NEXT period start, and an employee permanently excluded by one high month is the mirror bug")
		}
	})

	t.Run("an uncovered employee gets no contribution at all", func(t *testing.T) {
		res, err := ESIDerive(ESIInput{
			Month:    WageMonth{Year: 2026, Month: time.July},
			Covered:  false,
			Wages:    d(t, "18000.0000"),
			PaidDays: 31,
		}, rs)
		if err != nil {
			t.Fatalf("ESIDerive: %v", err)
		}
		if res.Applied {
			t.Error("Applied = true for an uncovered employee")
		}
		wantMoney(t, "employee share", res.EmployeeShare, "0", "there is no partial ESI")
		wantMoney(t, "employer share", res.EmployerShare, "0", "there is no partial ESI")
		wantMoney(t, "Wages", res.Wages, "0", "an uncovered month contributes nothing to the ESI wage base")
		if len(res.Components) != 0 {
			t.Errorf("uncovered employee produced %d component rows, want none", len(res.Components))
		}
	})
}

// TestESICoverage_BoundaryDates pins the dates the latch turns on, including the
// year boundary that the October-to-March period crosses.
func TestESICoverage_BoundaryDates(t *testing.T) {
	rs := ssFixture(t)

	t.Run("a raise effective exactly on 1 April is in force at the test instant", func(t *testing.T) {
		// [EffectiveFrom, next) is half-open and INCLUSIVE at the start, so the
		// 1 April revision governs the 1 April test. An exclusive reading would
		// test the OLD wage and cover somebody who was never eligible.
		res, err := ESICoverage(CoverageInput{
			Month:     WageMonth{Year: 2026, Month: time.April},
			Revisions: wageHistory(t, "2025-04-01", "20000.0000", "2026-04-01", "26000.0000"),
		}, rs)
		if err != nil {
			t.Fatalf("ESICoverage: %v", err)
		}
		if res.Covered {
			t.Error("Covered = true — a raise effective ON the period start is in force at the test instant, so this employee was never eligible for the April period")
		}
		if !res.WagesTested.Equal(d(t, "26000")) {
			t.Errorf("WagesTested = %s, want 26000 (the revision effective ON 1 April)", res.WagesTested)
		}
	})

	t.Run("a raise effective exactly on 1 October leaves the April period intact", func(t *testing.T) {
		revisions := wageHistory(t, "2026-04-01", "20000.0000", "2026-10-01", "26000.0000")
		sep, err := ESICoverage(CoverageInput{Month: WageMonth{Year: 2026, Month: time.September}, Revisions: revisions}, rs)
		if err != nil {
			t.Fatalf("ESICoverage(Sep): %v", err)
		}
		if !sep.Covered {
			t.Error("September uncovered — a raise dated 1 October cannot reach back into the April period")
		}
		oct, err := ESICoverage(CoverageInput{Month: WageMonth{Year: 2026, Month: time.October}, Revisions: revisions}, rs)
		if err != nil {
			t.Fatalf("ESICoverage(Oct): %v", err)
		}
		if oct.Covered {
			t.Error("October covered — the new period tests the wage in force on 1 October, which is the raised one")
		}
	})

	t.Run("a joiner on the LAST day of a period is tested on the joining date", func(t *testing.T) {
		joined := ssDay(t, "2026-09-30")
		revisions := wageHistory(t, "2026-09-30", "20000.0000")

		sep, err := ESICoverage(CoverageInput{
			Month:     WageMonth{Year: 2026, Month: time.September},
			JoinedOn:  joined,
			Revisions: revisions,
		}, rs)
		if err != nil {
			t.Fatalf("ESICoverage(Sep): %v", err)
		}
		if !sep.Covered {
			t.Error("a joiner at 20,000 on the last day of the period is not covered — there were no wages of theirs at the period start, so the joining date is the only instant the test can use")
		}
		if !sep.WagesTestedOn.Equal(joined) {
			t.Errorf("WagesTestedOn = %s, want the joining date 2026-09-30", sep.WagesTestedOn.Format("2006-01-02"))
		}

		// The month before they joined is NOT a coverage question, and — this is
		// the ordering that matters — it must not fail for want of a wage
		// history that did not exist yet.
		aug, err := ESICoverage(CoverageInput{
			Month:     WageMonth{Year: 2026, Month: time.August},
			JoinedOn:  joined,
			Revisions: revisions,
		}, rs)
		if err != nil {
			t.Fatalf("ESICoverage(Aug) errored (%v) — employment must be tested BEFORE wages, or every pay run fails on the months before a joiner started", err)
		}
		if aug.Covered || aug.Reason != CoverageReasonNotEmployed {
			t.Errorf("August = (covered %v, reason %q), want (false, %q)", aug.Covered, aug.Reason, CoverageReasonNotEmployed)
		}
	})

	t.Run("a leaver on the FIRST day of a period is still employed that month", func(t *testing.T) {
		left := ssDay(t, "2026-10-01")
		revisions := wageHistory(t, "2026-04-01", "20000.0000")

		oct, err := ESICoverage(CoverageInput{
			Month:     WageMonth{Year: 2026, Month: time.October},
			LeftOn:    left,
			Revisions: revisions,
		}, rs)
		if err != nil {
			t.Fatalf("ESICoverage(Oct): %v", err)
		}
		if !oct.Covered {
			t.Error("somebody whose last working day is 1 October worked that day and is covered for it; excluding them denies benefit for a day they were insured")
		}
		if !oct.WagesTestedOn.Equal(ssDay(t, "2026-10-01")) {
			t.Errorf("WagesTestedOn = %s, want the 1 October period start", oct.WagesTestedOn.Format("2006-01-02"))
		}

		nov, err := ESICoverage(CoverageInput{
			Month:     WageMonth{Year: 2026, Month: time.November},
			LeftOn:    left,
			Revisions: revisions,
		}, rs)
		if err != nil {
			t.Fatalf("ESICoverage(Nov): %v", err)
		}
		if nov.Covered || nov.Reason != CoverageReasonNotEmployed {
			t.Errorf("November = (covered %v, reason %q), want (false, %q)", nov.Covered, nov.Reason, CoverageReasonNotEmployed)
		}
	})

	t.Run("January resolves to the PREVIOUS year's October period", func(t *testing.T) {
		// The year-boundary trap: a naive ContributionPeriodFor reads the
		// CURRENT year's 1 October for January, February and March, which is
		// seven months in the future and finds no wage revision at all.
		for _, c := range []struct {
			month            WageMonth
			wantStart        string
			wantEnd          string
			wantTestedOnWage string
		}{
			{WageMonth{Year: 2027, Month: time.January}, "2026-10-01", "2027-03-31", "26000"},
			{WageMonth{Year: 2027, Month: time.March}, "2026-10-01", "2027-03-31", "26000"},
			{WageMonth{Year: 2026, Month: time.December}, "2026-10-01", "2027-03-31", "26000"},
			{WageMonth{Year: 2026, Month: time.June}, "2026-04-01", "2026-09-30", "20000"},
		} {
			res, err := ESICoverage(CoverageInput{
				Month:     c.month,
				Revisions: wageHistory(t, "2026-04-01", "20000.0000", "2026-10-01", "26000.0000"),
			}, rs)
			if err != nil {
				t.Fatalf("ESICoverage(%s): %v", c.month, err)
			}
			if !res.PeriodStart.Equal(ssDay(t, c.wantStart)) || !res.PeriodEnd.Equal(ssDay(t, c.wantEnd)) {
				t.Errorf("%s period = [%s, %s], want [%s, %s]", c.month,
					res.PeriodStart.Format("2006-01-02"), res.PeriodEnd.Format("2006-01-02"), c.wantStart, c.wantEnd)
			}
			if !res.WagesTested.Equal(d(t, c.wantTestedOnWage)) {
				t.Errorf("%s WagesTested = %s, want %s", c.month, res.WagesTested, c.wantTestedOnWage)
			}
		}
	})
}

// TestESICoverage_ExactlyAtLimitIsCovered pins the comparison operator.
//
// Rule 50 speaks of wages that EXCEED the prescribed limit, so an employee on
// exactly 21,000 IS covered. Employees on a round ceiling wage are a real and
// large population, and >= instead of > drops every one of them out of the
// scheme without any error anywhere.
func TestESICoverage_ExactlyAtLimitIsCovered(t *testing.T) {
	rs := ssFixture(t)
	for _, c := range []struct {
		wage        string
		wantCovered bool
		why         string
	}{
		{"20999.9999", true, "a paisa below the ceiling"},
		{"21000.0000", true, "EXACTLY at the ceiling: Rule 50's word is EXCEED, so >= would drop every employee on a round 21,000"},
		{"21000.0001", false, "a paisa above the ceiling excludes"},
	} {
		t.Run(c.wage, func(t *testing.T) {
			res, err := ESICoverage(CoverageInput{
				Month:     WageMonth{Year: 2026, Month: time.April},
				Revisions: wageHistory(t, "2026-04-01", c.wage),
			}, rs)
			if err != nil {
				t.Fatalf("ESICoverage: %v", err)
			}
			if res.Covered != c.wantCovered {
				t.Errorf("Covered = %v for wages %s, want %v — %s", res.Covered, c.wage, c.wantCovered, c.why)
			}
			if !res.Limit.Equal(d(t, "21000")) {
				t.Errorf("Limit = %s, want 21000", res.Limit)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The contribution.
// ---------------------------------------------------------------------------

// TestESIDerive_LowWageEmployeeShareExemptEmployerStillPays pins Rule 52.
//
// An employee whose average daily wage does not exceed the prescribed limit is
// exempt from the EMPLOYEE's contribution. The EMPLOYER's 3.25% remains payable
// in full. Getting that backwards costs the employee 0.75% of a wage they were
// exempted from paying on, or short-pays the corporation by the employer share
// for the lowest-paid people on the roll.
func TestESIDerive_LowWageEmployeeShareExemptEmployerStillPays(t *testing.T) {
	rs := ssFixture(t)
	cases := []struct {
		name         string
		wages        string
		paidDays     int
		wantExempt   bool
		wantEmployee string
		wantEmployer string
		why          string
	}{
		{"well under the daily limit", "5000.0000", 30, true, "0", "163",
			"5,000 over 30 days is 166.67 a day, under 176: employee nil, employer ceil(162.50) = 163"},
		{"EXACTLY at the daily limit", "5280.0000", 30, true, "0", "172",
			"176 x 30 = 5,280 exactly; Rule 52 says NOT MORE THAN, so exactly at the limit is exempt — the OPPOSITE direction from Rule 50's EXCEED test"},
		{"one rupee over the daily limit", "5281.0000", 30, false, "40", "172",
			"5,281 over 30 days is 176.03 a day: the employee share revives at ceil(39.6075) = 40"},
		{"a short month keeps the threshold proportional", "5000.0000", 28, false, "38", "163",
			"176 x 28 = 4,928, so 5,000 over 28 days is 178.57 a day and NOT exempt; a threshold hard-coded to 30 days would wrongly exempt this employee"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := ESIDerive(ESIInput{
				Month:    WageMonth{Year: 2026, Month: time.August},
				Covered:  true,
				Wages:    d(t, c.wages),
				PaidDays: c.paidDays,
			}, rs)
			if err != nil {
				t.Fatalf("ESIDerive: %v", err)
			}
			if res.LowWageExempt != c.wantExempt {
				t.Errorf("LowWageExempt = %v, want %v — %s", res.LowWageExempt, c.wantExempt, c.why)
			}
			wantMoney(t, "employee share", res.EmployeeShare, c.wantEmployee, c.why)
			wantMoney(t, "employer share", res.EmployerShare, c.wantEmployer,
				"the EMPLOYER's 3.25% is payable whether or not the employee is exempt")

			// Both rows are always present for a covered employee: a nil employee
			// share is a REAL statutory fact the return must show, which is not
			// the same as pf.go's deferred EDLI contribution, where the rate is
			// unknown and no row exists at all.
			empRow, ok := res.ComponentByName(ComponentESIEmployee)
			if !ok {
				t.Fatal("no ESI_EMPLOYEE component row — the return must show an exempt employee with a nil contribution, not omit them")
			}
			wantMoney(t, "ESI_EMPLOYEE row", empRow.Amount, c.wantEmployee, "the register line")
			if empRow.Tag != TagStatutory {
				t.Errorf("ESI_EMPLOYEE tagged %q, want STATUTORY", empRow.Tag)
			}
			if _, ok := res.ComponentByName(ComponentESIEmployer); !ok {
				t.Fatal("no ESI_EMPLOYER component row")
			}
		})
	}
}

// TestESIDerive_BothSharesRoundUp pins the judgement recorded in RoundUpRupee's
// doc: Rule 51 writes "(rounded to the next higher rupee)" into the employer's
// clause and the employee's clause alike, so BOTH shares are ceiled.
//
// Several commercial payroll products ceil only the employee share and leave the
// employer share at two decimals or round it half-up. The cases below are chosen
// so a half-up employer share differs by a whole rupee, which is what makes this
// test able to fail.
func TestESIDerive_BothSharesRoundUp(t *testing.T) {
	rs := ssFixture(t)
	cases := []struct {
		wages        string
		wantEmployee string
		wantEmployer string
		halfUpWould  string
	}{
		// 0.75% of 20,001 = 150.0075 -> 151. 3.25% = 650.0325 -> 651.
		{"20001.0000", "151", "651", "650 for the employer under half-up"},
		// 0.75% of 10,000.04 = 75.0003 -> 76. 3.25% = 325.0013 -> 326.
		{"10000.0400", "76", "326", "325 for the employer under half-up"},
		// Both fractions land EXACTLY on the half, where half-up and the ceiling
		// agree: 0.75% of 20,200 = 151.50 and 3.25% = 656.50. This row proves the
		// test is not simply asserting "always one rupee more than half-up".
		{"20200.0000", "152", "657", "657 for the employer under half-up too — the ceiling is not merely +1"},
		// Exact multiples: the ceiling must be a no-op, or every ESI figure in
		// the country would be a rupee too high.
		{"20000.0000", "150", "650", "650 exactly; a ceiling applied to a whole rupee must not add one"},
	}
	for _, c := range cases {
		t.Run(c.wages, func(t *testing.T) {
			res, err := ESIDerive(ESIInput{
				Month:    WageMonth{Year: 2026, Month: time.August},
				Covered:  true,
				Wages:    d(t, c.wages),
				PaidDays: 30,
			}, rs)
			if err != nil {
				t.Fatalf("ESIDerive: %v", err)
			}
			wantMoney(t, "employee share", res.EmployeeShare, c.wantEmployee, "Rule 51: rounded to the NEXT HIGHER rupee")
			wantMoney(t, "employer share", res.EmployerShare, c.wantEmployer,
				"Rule 51 applies the same parenthesis to the EMPLOYER's clause; "+c.halfUpWould)
			if res.LowWageExempt {
				t.Fatal("these wages are far above the Rule 52 daily limit; the exemption fired and the rounding assertions are testing the wrong branch")
			}
		})
	}
}

// TestESIDerive_Rejects pins every refusal.
func TestESIDerive_Rejects(t *testing.T) {
	good := ESIInput{
		Month:    WageMonth{Year: 2026, Month: time.August},
		Covered:  true,
		Wages:    d(t, "20000.0000"),
		PaidDays: 30,
	}
	cases := []struct {
		name    string
		mutate  func(*ESIInput, *SocialSecurityRuleSet)
		want    error
		because string
	}{
		{"empty ruleset", func(_ *ESIInput, rs *SocialSecurityRuleSet) { rs.Version = "" }, ErrNoRuleSet,
			"a zero ruleset computes every contribution as nil and looks like a month nobody was covered"},
		{"negative wages", func(in *ESIInput, _ *SocialSecurityRuleSet) { in.Wages = d(t, "-1.0000") }, ErrNegativeStatutoryAmount,
			"RoundUpRupee on a negative gives the wrong direction and the statute does not contemplate one"},
		{"covered with zero paid days", func(in *ESIInput, _ *SocialSecurityRuleSet) { in.PaidDays = 0 }, ErrInvalidPaidDays,
			"Rule 52's average daily wage has no meaning over zero days, and a wage that belongs to no day is a bug in the pay run"},
		{"covered with negative paid days", func(in *ESIInput, _ *SocialSecurityRuleSet) { in.PaidDays = -1 }, ErrInvalidPaidDays, "same"},
		{"unverified employee rate", func(_ *ESIInput, rs *SocialSecurityRuleSet) {
			rs.ESIEmployeePct = ssUnverified("an ESI employee rate that was never verified against a primary source — fixture")
		}, ErrUnverifiedRate, "an unverified rate produces a plausible contribution nobody can tell from a correct one"},
		{"unverified employer rate", func(_ *ESIInput, rs *SocialSecurityRuleSet) {
			rs.ESIEmployerPct = ssUnverified("an ESI employer rate that was never verified — fixture")
		}, ErrUnverifiedRate, "same"},
		{"unverified low-wage limit", func(_ *ESIInput, rs *SocialSecurityRuleSet) {
			rs.ESILowWageDailyLimit = ssUnverified("a Rule 52 daily limit that was never verified — fixture")
		}, ErrUnverifiedRate, "an unverified exemption limit silently decides whether the lowest-paid employees pay at all"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := good
			rs := ssFixture(t)
			c.mutate(&in, &rs)
			res, err := ESIDerive(in, rs)
			if !errors.Is(err, c.want) {
				t.Fatalf("ESIDerive = (%+v, %v), want %v — %s", res, err, c.want, c.because)
			}
		})
	}
	if _, err := ESIDerive(good, ssFixture(t)); err != nil {
		t.Fatalf("the unmutated fixture errored (%v) — every rejection above would be vacuous", err)
	}
}

// TestESICoverage_Rejects pins the latch's refusals. Every one of them exists
// because the alternative is a coverage decision that depends on slice order or
// on a rate nobody verified.
func TestESICoverage_Rejects(t *testing.T) {
	good := CoverageInput{
		Month:     WageMonth{Year: 2026, Month: time.April},
		Revisions: wageHistory(t, "2026-04-01", "20000.0000"),
	}
	cases := []struct {
		name    string
		mutate  func(*CoverageInput, *SocialSecurityRuleSet)
		want    error
		because string
	}{
		{"empty ruleset", func(_ *CoverageInput, rs *SocialSecurityRuleSet) { rs.Version = "" }, ErrNoRuleSet,
			"a zero ceiling would exclude everybody, silently"},
		{"unverified coverage ceiling", func(_ *CoverageInput, rs *SocialSecurityRuleSet) {
			rs.ESIWageCeiling = ssUnverified("an ESI coverage ceiling that was never verified against a primary source — fixture")
		}, ErrUnverifiedRate, "an unverified ceiling decides who is in the scheme at all"},
		{"no wage revision in force at the test instant", func(in *CoverageInput, _ *SocialSecurityRuleSet) {
			in.Revisions = wageHistory(t, "2026-05-01", "20000.0000")
		}, ErrInvalidWageRevisions,
			"guessing a wage for the period start would latch a coverage decision on a number nobody supplied"},
		{"two revisions claiming the same effective date", func(in *CoverageInput, _ *SocialSecurityRuleSet) {
			in.Revisions = wageHistory(t, "2026-04-01", "20000.0000", "2026-04-01", "26000.0000")
		}, ErrInvalidWageRevisions,
			"picking one silently makes an employee's medical benefit depend on slice order"},
		{"a negative wage in the history", func(in *CoverageInput, _ *SocialSecurityRuleSet) {
			in.Revisions = wageHistory(t, "2026-04-01", "-1.0000")
		}, ErrInvalidWageRevisions, "a negative wage would compare below the ceiling and cover somebody on a data-entry error"},
		{"an empty history", func(in *CoverageInput, _ *SocialSecurityRuleSet) { in.Revisions = nil }, ErrInvalidWageRevisions,
			"same as no revision in force"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := good
			rs := ssFixture(t)
			c.mutate(&in, &rs)
			res, err := ESICoverage(in, rs)
			if !errors.Is(err, c.want) {
				t.Fatalf("ESICoverage = (%+v, %v), want %v — %s", res, err, c.want, c.because)
			}
		})
	}
	if _, err := ESICoverage(good, ssFixture(t)); err != nil {
		t.Fatalf("the unmutated fixture errored (%v) — every rejection above would be vacuous", err)
	}
}

// TestESI_ShippedRuleSetProducesTheCanonicalNumbers is the registry-drift guard
// for the ESI path, run end to end so a rate that drifts in a way the field-level
// check in rates_test.go tolerates still fails here.
func TestESI_ShippedRuleSetProducesTheCanonicalNumbers(t *testing.T) {
	for _, w := range []struct {
		month       WageMonth
		wantVersion string
	}{
		{WageMonth{Year: 2026, Month: time.August}, "ss-2026-07"},
		{WageMonth{Year: 2020, Month: time.August}, "ss-2019-07"},
	} {
		t.Run(w.wantVersion, func(t *testing.T) {
			rs, err := SocialSecurityFor(w.month.Last())
			if err != nil {
				t.Fatalf("SocialSecurityFor(%s): %v", w.month, err)
			}
			if rs.Version != w.wantVersion {
				t.Fatalf("resolved %q, want %q", rs.Version, w.wantVersion)
			}
			cov, err := ESICoverage(CoverageInput{
				Month:     w.month,
				Revisions: []WageRevision{{EffectiveFrom: ssDay(t, "2019-01-01"), MonthlyWages: d(t, "21000.0000")}},
			}, rs)
			if err != nil {
				t.Fatalf("ESICoverage: %v", err)
			}
			if !cov.Covered {
				t.Errorf("an employee on exactly 21,000 is not covered under %s — the shipped ceiling or the shipped operator has drifted", rs.Version)
			}
			if !cov.Limit.Equal(d(t, "21000")) {
				t.Errorf("shipped coverage ceiling = %s, want 21000", cov.Limit)
			}
			res, err := ESIDerive(ESIInput{
				Month:    w.month,
				Covered:  true,
				Wages:    d(t, "21000.0000"),
				PaidDays: 30,
			}, rs)
			if err != nil {
				t.Fatalf("ESIDerive: %v", err)
			}
			wantMoney(t, "employee rate", res.EmployeeRatePct, "0.75", "ESI (Central) Rules 1950 rule 51")
			wantMoney(t, "employer rate", res.EmployerRatePct, "3.25", "ESI (Central) Rules 1950 rule 51")
			wantMoney(t, "employee share", res.EmployeeShare, "158", "0.75% of 21,000 = 157.50, ceiled")
			wantMoney(t, "employer share", res.EmployerShare, "683", "3.25% of 21,000 = 682.50, ceiled")
			wantMoney(t, "low-wage threshold", res.LowWageThreshold, "5280", "176 x 30 paid days")
			if res.RuleSetVersion != w.wantVersion {
				t.Errorf("RuleSetVersion = %q, want %q", res.RuleSetVersion, w.wantVersion)
			}
		})
	}
}

// TestContributionPeriodFor covers the period arithmetic directly, month by
// month, including both year boundaries.
func TestContributionPeriodFor(t *testing.T) {
	cases := []struct {
		date      string
		wantStart string
		wantEnd   string
	}{
		{"2026-04-01", "2026-04-01", "2026-09-30"},
		{"2026-09-30", "2026-04-01", "2026-09-30"},
		{"2026-10-01", "2026-10-01", "2027-03-31"},
		{"2026-12-31", "2026-10-01", "2027-03-31"},
		{"2027-01-01", "2026-10-01", "2027-03-31"},
		{"2027-03-31", "2026-10-01", "2027-03-31"},
		{"2027-04-01", "2027-04-01", "2027-09-30"},
	}
	for _, c := range cases {
		t.Run(c.date, func(t *testing.T) {
			start, end := ContributionPeriodFor(ssDay(t, c.date))
			if !start.Equal(ssDay(t, c.wantStart)) || !end.Equal(ssDay(t, c.wantEnd)) {
				t.Errorf("ContributionPeriodFor(%s) = [%s, %s], want [%s, %s] — the October period SPANS the year boundary and reading the current year's 1 October for a January run tests a date seven months in the future",
					c.date, start.Format("2006-01-02"), end.Format("2006-01-02"), c.wantStart, c.wantEnd)
			}
			// The period must contain the date it was derived from, always.
			day := ssDay(t, c.date)
			if day.Before(start) || day.After(end) {
				t.Errorf("ContributionPeriodFor(%s) returned a period that does not contain it", c.date)
			}
		})
	}
}
