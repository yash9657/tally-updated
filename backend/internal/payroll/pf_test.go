package payroll

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// ---------------------------------------------------------------------------
// Fixtures shared by pf_test.go and esi_test.go.
//
// ssFixture builds a ruleset carrying the CANONICAL statutory numbers by hand,
// rather than reaching for the shipped ss-2026-07. That separation is
// deliberate: these tests assert what the ARITHMETIC does with a set of rates,
// and TestPFDerive_ShippedRuleSetProducesTheCanonicalNumbers separately asserts
// that the shipped rates are the ones we think they are. Folded together, a
// wrong rate in rates.go would silently rewrite the expectation of every
// arithmetic test at once and none of them would fail.
// ---------------------------------------------------------------------------

// ssRate builds a verified rate. The citation is present because Rate.Use is
// entitled to refuse an uncited row and a fixture must not rely on it not doing
// so.
func ssRate(t *testing.T, value, citation string) Rate {
	t.Helper()
	return Rate{
		Value:      d(t, value),
		Citation:   citation,
		From:       time.Date(2020, time.April, 1, 0, 0, 0, 0, time.UTC),
		Confidence: ConfHigh,
		VerifiedOn: time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC),
	}
}

// ssUnverified builds a row that Rate.Use must refuse.
func ssUnverified(citation string) Rate {
	return Rate{Value: decimal.Zero, Citation: citation, Confidence: ConfUnverified}
}

func ssFixture(t *testing.T) SocialSecurityRuleSet {
	t.Helper()
	return SocialSecurityRuleSet{
		Version:             "ss-test",
		DeemingThresholdPct: ssRate(t, "50", "Code on Social Security 2020 s.2(88) proviso — fixture"),

		EPFEmployeePct:  ssRate(t, "12", "EPF employee share 12% — fixture"),
		EPFEmployerPct:  ssRate(t, "12", "EPF employer share 12% — fixture"),
		EPSPct:          ssRate(t, "8.33", "EPS diversion 8.33% of pensionable salary — fixture"),
		EPFWageCeiling:  ssRate(t, "15000.0000", "EPF wage ceiling Rs 15,000 per month — fixture"),
		EPSWageCeiling:  ssRate(t, "15000.0000", "EPS pensionable-salary ceiling Rs 15,000 — fixture"),
		EDLIWageCeiling: ssRate(t, "15000.0000", "EDLI wage ceiling Rs 15,000 — fixture"),

		EDLIContributionPct: ssUnverified("EDLI Scheme 2026: rate to be notified separately — fixture"),
		EDLIAdminChargePct:  ssUnverified("EDLI Scheme 2026: admin charges to be notified separately — fixture"),
		EPFAdminChargePct:   ssUnverified("EPF Scheme 2026: admin charges to be notified separately — fixture"),
		EPFAdminChargeMin:   ssUnverified("EPF Scheme 2026: admin-charge minimum to be notified separately — fixture"),

		ESIEmployeePct:       ssRate(t, "0.75", "ESI (Central) Rules 1950 rule 51 employee 0.75% — fixture"),
		ESIEmployerPct:       ssRate(t, "3.25", "ESI (Central) Rules 1950 rule 51 employer 3.25% — fixture"),
		ESIWageCeiling:       ssRate(t, "21000.0000", "ESI (Central) Rules 1950 rule 50 ceiling Rs 21,000 — fixture"),
		ESILowWageDailyLimit: ssRate(t, "176.0000", "ESI (Central) Rules 1950 rule 52 daily limit Rs 176 — fixture"),
	}
}

// ssDay parses a date-valued day, matching the DATE columns and the dates
// rates.go builds with dateUTC.
func ssDay(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("bad date literal %q in the test itself: %v", s, err)
	}
	return v.UTC()
}

// wantMoney compares two money values as DECIMALS, never as strings. "1250" and
// "1250.0000" are the same rupee and a string comparison would call them
// different — the trap the corpus shape rules already call out.
func wantMoney(t *testing.T, label string, got decimal.Decimal, want string, consequence string) {
	t.Helper()
	w := d(t, want)
	if !got.Equal(w) {
		t.Errorf("%s = %s, want %s — %s", label, got.String(), w.String(), consequence)
	}
}

// ---------------------------------------------------------------------------
// The arithmetic.
// ---------------------------------------------------------------------------

// TestPFDerive_AtCeilingSplitsEPSAndResidual pins the single most common
// payslip in the country: an employee on exactly the wage ceiling.
//
// 12% of 15,000 = 1,800 from each side. 8.33% of 15,000 = 1,249.50, which
// rounds to 1,250 and is diverted out of the EMPLOYER's 1,800, leaving 550 in
// EPF. The employee's 1,800 is untouched by the pension — a pay run that took
// the diversion out of the employee's share instead would short the member's
// provident fund by 1,250 every month and nobody would notice until retirement.
func TestPFDerive_AtCeilingSplitsEPSAndResidual(t *testing.T) {
	rs := ssFixture(t)
	res, err := PFDerive(PFInput{
		Month:         WageMonth{Year: 2026, Month: time.August},
		StatutoryWage: d(t, "15000.0000"),
		Base:          PFBaseCeiling,
		EPSMember:     true,
		FirstMemberOn: ssDay(t, "2010-01-01"),
	}, rs)
	if err != nil {
		t.Fatalf("PFDerive: %v", err)
	}

	wantMoney(t, "EPFWages", res.EPFWages, "15000", "the ECR's EPF wage column")
	wantMoney(t, "EPSWages", res.EPSWages, "15000", "the ECR's EPS wage column")
	wantMoney(t, "EDLIWages", res.EDLIWages, "15000", "the ECR's EDLI wage column")
	wantMoney(t, "EmployeeEPF", res.EmployeeEPF, "1800", "12% of the ceiling; the pension is NOT taken out of the employee's share")
	wantMoney(t, "EPS", res.EPS, "1250", "8.33% of 15,000 is 1,249.50 and rounds to the nearest rupee")
	wantMoney(t, "EmployerEPF", res.EmployerEPF, "550", "the ECR's EPF-EPS Diff Remitted column")

	// The identity the EPFO's portal re-checks when it re-adds the columns.
	if got := res.EmployerEPF.Add(res.EPS); !got.Equal(d(t, "1800")) {
		t.Errorf("EmployerEPF + EPS = %s, want 1800 — the employer remits ONE 12%% and the portal re-adds the two columns; a mismatch is a rejected challan", got)
	}

	if !res.EPSMemberApplied {
		t.Error("EPSMemberApplied = false for a pre-2014 member on the ceiling")
	}
	if res.PeriodBasis != BasisMonthly {
		t.Errorf("PeriodBasis = %q, want %q — the EPF ceiling is a wage-month figure", res.PeriodBasis, BasisMonthly)
	}
	if res.RuleSetVersion != "ss-test" || !res.CeilingApplied.Equal(d(t, "15000")) {
		t.Errorf("echo drifted: version %q ceiling %s", res.RuleSetVersion, res.CeilingApplied)
	}

	// No voluntary rows at all: nothing was elected.
	for _, name := range []string{ComponentEPFEmployeeVoluntary, ComponentEPFEmployerVoluntary} {
		if c, ok := res.ComponentByName(name); ok {
			t.Errorf("unelected voluntary row %s present with %s — Form V would report a voluntary contribution nobody agreed to", name, c.Amount)
		}
	}
	for _, want := range []struct {
		name   string
		amount string
	}{
		{ComponentEPFEmployee, "1800"},
		{ComponentEPFEmployer, "550"},
		{ComponentEPS, "1250"},
	} {
		c, ok := res.ComponentByName(want.name)
		if !ok {
			t.Errorf("component %s missing", want.name)
			continue
		}
		wantMoney(t, "component "+want.name, c.Amount, want.amount, "the remittance line")
		if c.Tag != TagStatutory {
			t.Errorf("component %s tagged %q, want STATUTORY — a statutory contribution in the voluntary line misstates Form V", want.name, c.Tag)
		}
	}
}

// TestPFDerive_AboveCeilingBaseIsCeilingByDefault pins what the two bases do to
// a wage above the ceiling, and pins that there IS no default: an unset base
// errors rather than quietly choosing one. Silently choosing CEILING for an
// employer who elected ACTUAL under-contributes for every high earner, in both
// directions, with no error anywhere.
func TestPFDerive_AboveCeilingBaseIsCeilingByDefault(t *testing.T) {
	rs := ssFixture(t)
	base := PFInput{
		Month:         WageMonth{Year: 2026, Month: time.August},
		StatutoryWage: d(t, "25000.0000"),
		EPSMember:     true,
		FirstMemberOn: ssDay(t, "2010-01-01"),
	}

	t.Run("ceiling", func(t *testing.T) {
		in := base
		in.Base = PFBaseCeiling
		res, err := PFDerive(in, rs)
		if err != nil {
			t.Fatalf("PFDerive: %v", err)
		}
		wantMoney(t, "EPFWages", res.EPFWages, "15000", "a CEILING election contributes on the ceiling, not the wage")
		wantMoney(t, "EmployeeEPF", res.EmployeeEPF, "1800", "12% of the ceiling")
		wantMoney(t, "EmployerEPF", res.EmployerEPF, "550", "1,800 less the 1,250 pension diversion")
		wantMoney(t, "EPS", res.EPS, "1250", "capped at the pension ceiling")
		if _, ok := res.ComponentByName(ComponentEPFEmployeeVoluntary); ok {
			t.Error("a CEILING election produced a voluntary row; nothing above the ceiling was contributed")
		}
	})

	t.Run("actual", func(t *testing.T) {
		in := base
		in.Base = PFBaseActual
		res, err := PFDerive(in, rs)
		if err != nil {
			t.Fatalf("PFDerive: %v", err)
		}
		wantMoney(t, "EPFWages", res.EPFWages, "25000", "an ACTUAL election contributes on the whole statutory wage")
		wantMoney(t, "EmployeeEPF", res.EmployeeEPF, "3000", "12% of 25,000")
		wantMoney(t, "EmployerEPF", res.EmployerEPF, "1750", "3,000 less the 1,250 pension diversion")
		wantMoney(t, "EPS", res.EPS, "1250", "still capped at the PENSION ceiling, not raised with the EPF base")
		wantMoney(t, "EPSWages", res.EPSWages, "15000", "the pension ceiling does not move with the employer's EPF election")

		// The above-ceiling half is para 26(6) VOLUNTARY on both sides.
		c, ok := res.ComponentByName(ComponentEPFEmployeeVoluntary)
		if !ok {
			t.Fatal("no employee voluntary row for the above-ceiling excess — Form V reports the statutory share separately and would over-state it")
		}
		wantMoney(t, "employee voluntary", c.Amount, "1200", "12% of the 10,000 above the ceiling")
		if c.Tag != TagVoluntary {
			t.Errorf("above-ceiling excess tagged %q, want VOLUNTARY", c.Tag)
		}
		stat, _ := res.ComponentByName(ComponentEPFEmployee)
		wantMoney(t, "employee statutory", stat.Amount, "1800", "the statutory share stays 12% of the CEILING even under an ACTUAL election")
	})

	t.Run("unset base errors", func(t *testing.T) {
		in := base
		in.Base = ""
		if _, err := PFDerive(in, rs); !errors.Is(err, ErrUnknownPFBase) {
			t.Fatalf("PFDerive with an empty base = %v, want ErrUnknownPFBase — the engine defaults NOTHING; payrolldb owns defaulting", err)
		}
	})
}

// TestPFDerive_VoluntaryIsTaggedAndEmployerNeedNotMatch proves the three
// election knobs are INDEPENDENT.
//
// Flipping EmployerMatchesVoluntary alone must change the employer's voluntary
// row and NOTHING else. Flipping Base alone must change the wage base and the
// voluntary rows and leave the statutory rows and the pension alone. If any one
// flip moves a second number, the three knobs have been folded into fewer than
// three and some employer's arrangement is now unrepresentable.
func TestPFDerive_VoluntaryIsTaggedAndEmployerNeedNotMatch(t *testing.T) {
	rs := ssFixture(t)
	base := PFInput{
		Month:                 WageMonth{Year: 2026, Month: time.August},
		StatutoryWage:         d(t, "25000.0000"),
		Base:                  PFBaseCeiling,
		EmployeeVoluntaryBase: d(t, "10000.0000"),
		EPSMember:             true,
		FirstMemberOn:         ssDay(t, "2010-01-01"),
	}

	noMatch, err := PFDerive(base, rs)
	if err != nil {
		t.Fatalf("PFDerive (no match): %v", err)
	}
	matched := base
	matched.EmployerMatchesVoluntary = true
	withMatch, err := PFDerive(matched, rs)
	if err != nil {
		t.Fatalf("PFDerive (matched): %v", err)
	}

	// The employee side is identical either way: whether the employer matches is
	// none of the employee's business and must not move a rupee of their
	// deduction.
	wantMoney(t, "employee EPF without a match", noMatch.EmployeeEPF, "3000", "1,800 statutory + 1,200 VPF")
	wantMoney(t, "employee EPF with a match", withMatch.EmployeeEPF, "3000", "the employer's decision must not change the employee's deduction")
	wantMoney(t, "EPS without a match", noMatch.EPS, "1250", "VPF never attracts a pension diversion")
	wantMoney(t, "EPS with a match", withMatch.EPS, "1250", "VPF never attracts a pension diversion")

	wantMoney(t, "employer EPF without a match", noMatch.EmployerEPF, "550", "the employer is under no obligation to match VPF")
	wantMoney(t, "employer EPF with a match", withMatch.EmployerEPF, "1750", "550 statutory + 1,200 matched")

	if _, ok := noMatch.ComponentByName(ComponentEPFEmployerVoluntary); ok {
		t.Error("an unmatched VPF produced an employer voluntary row — the employer would be billed for a contribution it never agreed to")
	}
	mc, ok := withMatch.ComponentByName(ComponentEPFEmployerVoluntary)
	if !ok {
		t.Fatal("a matched VPF produced no employer voluntary row")
	}
	wantMoney(t, "employer voluntary", mc.Amount, "1200", "12% of the 10,000 VPF base")
	if mc.Tag != TagVoluntary {
		t.Errorf("matched VPF tagged %q, want VOLUNTARY", mc.Tag)
	}

	// Nothing else moved. Compare every component EXCEPT the employer voluntary
	// row, so the assertion is "only that row changed" rather than "that row
	// changed".
	strip := func(res PFResult) map[string]string {
		out := map[string]string{}
		for _, c := range res.Components {
			if c.Name == ComponentEPFEmployerVoluntary {
				continue
			}
			out[c.Name] = c.Amount.String() + "/" + string(c.Tag)
		}
		return out
	}
	if !reflect.DeepEqual(strip(noMatch), strip(withMatch)) {
		t.Errorf("flipping EmployerMatchesVoluntary moved a second row:\n without: %v\n with:    %v\n"+
			"the three elections are not independent, and some employer's arrangement is now unrepresentable", strip(noMatch), strip(withMatch))
	}

	// The third knob, flipped alone: Base CEILING -> ACTUAL, no match.
	actual := base
	actual.Base = PFBaseActual
	act, err := PFDerive(actual, rs)
	if err != nil {
		t.Fatalf("PFDerive (actual base): %v", err)
	}
	wantMoney(t, "EPFWages under ACTUAL", act.EPFWages, "25000", "the base election governs the EPF wage")
	// 10,000 above-ceiling excess + 10,000 VPF = 20,000 voluntary base.
	vc, ok := act.ComponentByName(ComponentEPFEmployeeVoluntary)
	if !ok {
		t.Fatal("no employee voluntary row under an ACTUAL base with VPF")
	}
	wantMoney(t, "employee voluntary under ACTUAL", vc.Amount, "2400", "12% of (10,000 above-ceiling + 10,000 VPF)")
	sc, _ := act.ComponentByName(ComponentEPFEmployee)
	wantMoney(t, "employee statutory under ACTUAL", sc.Amount, "1800", "flipping the base must not move the STATUTORY row")
	wantMoney(t, "EPS under ACTUAL", act.EPS, "1250", "flipping the base must not move the pension")
	// The employer follows the base (its own election) but still does not match
	// the VPF.
	wantMoney(t, "employer EPF under ACTUAL", act.EmployerEPF, "1750", "550 statutory + 1,200 on the above-ceiling excess, and NOT the VPF")
}

// TestPFDerive_EPSExcludedForPost2014HighWageNewMember pins the exclusion that
// costs an employee a pension.
//
// A person who FIRST became a provident-fund member on or after 1 September
// 2014, whose pay then exceeded the pension ceiling, was never admitted to the
// pension scheme. For them the whole employer 12% stays in EPF. Diverting 8.33%
// anyway builds a pension the scheme will never pay and permanently removes the
// money from their provident fund.
func TestPFDerive_EPSExcludedForPost2014HighWageNewMember(t *testing.T) {
	rs := ssFixture(t)
	base := PFInput{
		Month:         WageMonth{Year: 2026, Month: time.August},
		StatutoryWage: d(t, "25000.0000"),
		Base:          PFBaseActual,
		EPSMember:     true,
	}

	cases := []struct {
		name          string
		firstMemberOn string
		epsMember     bool
		wantApplied   bool
		wantEPS       string
		wantEPSWages  string
		wantEmployer  string
	}{
		{"first member ON the cutoff, above the ceiling", "2014-09-01", true, false, "0", "0", "3000"},
		{"first member the day BEFORE the cutoff", "2014-08-31", true, true, "1250", "15000", "1750"},
		{"first member date unknown (zero) — exclusion cannot be asserted", "", true, true, "1250", "15000", "1750"},
		{"first member well after the cutoff", "2020-06-15", true, false, "0", "0", "3000"},
		{"not a pension member at all", "2010-01-01", false, false, "0", "0", "3000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := base
			in.EPSMember = c.epsMember
			if c.firstMemberOn != "" {
				in.FirstMemberOn = ssDay(t, c.firstMemberOn)
			}
			res, err := PFDerive(in, rs)
			if err != nil {
				t.Fatalf("PFDerive: %v", err)
			}
			if res.EPSMemberApplied != c.wantApplied {
				t.Errorf("EPSMemberApplied = %v, want %v", res.EPSMemberApplied, c.wantApplied)
			}
			wantMoney(t, "EPS", res.EPS, c.wantEPS, "8.33% diverted, or nothing for an excluded member")
			wantMoney(t, "EPSWages", res.EPSWages, c.wantEPSWages, "an excluded member has NO EPS wages; reporting wages with no contribution against them is an ECR the portal rejects")
			wantMoney(t, "EmployerEPF", res.EmployerEPF, c.wantEmployer, "an excluded member's whole employer share stays in EPF")
			if !c.wantApplied {
				if _, ok := res.ComponentByName(ComponentEPS); ok {
					t.Error("an excluded member produced an EPS component row")
				}
			}
		})
	}
}

// TestPFDerive_BoundaryOperators pins the comparison operators, which is where
// the exclusion above is actually decided.
//
// "exceeds the ceiling" is GreaterThan, so somebody on EXACTLY 15,000 is a
// pension member. "on or after the cutoff" is !Before, so somebody first
// admitted ON 1 September 2014 IS excluded. Flip either and a real population
// gets the wrong answer: >= drops every employee on a round 15,000 out of the
// pension scheme, and > on the date lets in everybody admitted on the
// commencement day itself.
func TestPFDerive_BoundaryOperators(t *testing.T) {
	rs := ssFixture(t)
	base := PFInput{
		Month:         WageMonth{Year: 2026, Month: time.August},
		Base:          PFBaseActual,
		EPSMember:     true,
		FirstMemberOn: ssDay(t, "2014-09-01"),
	}

	cases := []struct {
		name        string
		wage        string
		firstMember string
		wantApplied bool
		why         string
	}{
		{"exactly at the pension ceiling", "15000.0000", "2014-09-01", true,
			"the scheme's word is EXCEEDS, so 15,000 exactly is still a member; >= would drop every employee on a round ceiling wage"},
		{"one paisa above the ceiling", "15000.0001", "2014-09-01", false,
			"a paisa above the ceiling excludes; > is the operator the scheme uses"},
		{"one paisa above, admitted the day before the cutoff", "15000.0001", "2014-08-31", true,
			"the cutoff is ON OR AFTER, so 31 August 2014 is inside the scheme however high the wage"},
		{"one paisa above, admitted the day after the cutoff", "15000.0001", "2014-09-02", false,
			"anything after the cutoff is excluded"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := base
			in.StatutoryWage = d(t, c.wage)
			in.FirstMemberOn = ssDay(t, c.firstMember)
			res, err := PFDerive(in, rs)
			if err != nil {
				t.Fatalf("PFDerive: %v", err)
			}
			if res.EPSMemberApplied != c.wantApplied {
				t.Errorf("EPSMemberApplied = %v, want %v — %s", res.EPSMemberApplied, c.wantApplied, c.why)
			}
		})
	}

	// At exactly the ceiling the two bases must agree to the rupee: there is no
	// excess for ACTUAL to contribute on.
	atCeiling := base
	atCeiling.StatutoryWage = d(t, "15000.0000")
	atCeiling.FirstMemberOn = ssDay(t, "2010-01-01")
	ceil := atCeiling
	ceil.Base = PFBaseCeiling
	a, err := PFDerive(atCeiling, rs)
	if err != nil {
		t.Fatalf("PFDerive (actual): %v", err)
	}
	b, err := PFDerive(ceil, rs)
	if err != nil {
		t.Fatalf("PFDerive (ceiling): %v", err)
	}
	if !a.EmployeeEPF.Equal(b.EmployeeEPF) || !a.EmployerEPF.Equal(b.EmployerEPF) || !a.EPS.Equal(b.EPS) {
		t.Errorf("at exactly the ceiling ACTUAL (%s/%s/%s) and CEILING (%s/%s/%s) disagree — there is no above-ceiling excess to differ over",
			a.EmployeeEPF, a.EmployerEPF, a.EPS, b.EmployeeEPF, b.EmployerEPF, b.EPS)
	}
}

// TestPFDerive_EDLIContributionIsDeferredNotGuessed pins the honest handling of
// an unnotified rate.
//
// Under ss-2026-07 the EDLI rate is expressly "to be notified separately". The
// engine must compute the EDLI WAGES (arithmetic) and refuse the CONTRIBUTION
// (a rate), emitting a Deferral carrying the citation. It must not emit 0.00,
// because a downstream reader cannot tell 0.00-because-unknown from
// 0.00-because-nil, and it must not carry the superseded 1976 Scheme's 0.50%
// forward, because that is inventing a rate and charging an employer for it.
//
// The second half of the test is the non-vacuity proof: run the SAME code under
// ss-2019-07, where the rate IS on record, and the number appears. Without it a
// PFDerive that had simply forgotten EDLI entirely would pass.
func TestPFDerive_EDLIContributionIsDeferredNotGuessed(t *testing.T) {
	in := PFInput{
		Month:         WageMonth{Year: 2026, Month: time.August},
		StatutoryWage: d(t, "25000.0000"),
		Base:          PFBaseCeiling,
		EPSMember:     true,
		FirstMemberOn: ssDay(t, "2010-01-01"),
	}

	res, err := PFDerive(in, ssFixture(t))
	if err != nil {
		t.Fatalf("PFDerive: %v", err)
	}

	// The arithmetic IS done.
	wantMoney(t, "EDLIWages", res.EDLIWages, "15000",
		"min(statutory wage, ceiling) is arithmetic and does not depend on the unnotified rate")

	for _, name := range []string{ComponentEDLIContribution, ComponentEDLIAdminCharges, ComponentEPFAdminCharges} {
		def, ok := res.DeferralFor(name)
		if !ok {
			t.Errorf("no Deferral for %s — an unnotified rate must travel with the payslip as a stated refusal", name)
			continue
		}
		if len(def.Citation) < 40 {
			t.Errorf("Deferral %s citation is too short to be a citation: %q", name, def.Citation)
		}
		if len(def.Reason) < 40 {
			t.Errorf("Deferral %s reason is too short to act on: %q", name, def.Reason)
		}
		if c, present := res.ComponentByName(name); present {
			t.Errorf("deferred component %s ALSO appeared as a number (%s) — that number is invented", name, c.Amount)
		}
	}
	// The EDLI deferral's WORDING is pinned, and what it must NOT say is pinned
	// harder than what it must. See
	// TestPFDerive_EDLIDeferralReasonDoesNotRepeatTheRetractedClaim.
	if got, ok := res.DeferralFor(ComponentEDLIContribution); !ok {
		t.Error("no Deferral for EDLI_CONTRIBUTION at all")
	} else {
		for _, want := range []string{"0.50%", "SECONDARY SOURCES ONLY", "Rs 75 per member per month", "G.S.R."} {
			if !strings.Contains(got.Reason, want) {
				t.Errorf("EDLI deferral reason does not mention %q. It is the user-facing explanation persisted on "+
					"every payslip, and it must tell the employer that a rate IS reported and a liability IS "+
					"accruing. Reason = %q", want, got.Reason)
			}
		}
	}

	// NO numeric EDLI or admin contribution anywhere in the result: not as a
	// component, and not as a struct field either. The field check is what stops
	// somebody "helpfully" adding EDLIContribution decimal.Decimal later, which
	// would be a zero every consumer would read as a real nil contribution.
	for _, c := range res.Components {
		if strings.Contains(c.Name, "EDLI") || strings.Contains(c.Name, "ADMIN") {
			t.Errorf("component %s = %s present under an unnotified rate", c.Name, c.Amount)
		}
	}
	rt := reflect.TypeOf(PFResult{})
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		if name == "EDLIWages" {
			continue
		}
		if strings.Contains(name, "EDLI") || strings.Contains(name, "Admin") {
			t.Errorf("PFResult has a field %q — an amount field for an unnotified rate is a zero that reads as a real nil contribution", name)
		}
	}

	// Non-vacuity: the same code path under a ruleset where the rate IS on
	// record produces a number and no deferral.
	pre, err := SocialSecurityFor(ssDay(t, "2019-08-31"))
	if err != nil {
		t.Fatalf("SocialSecurityFor(2019-08-31): %v", err)
	}
	old, err := PFDerive(in, pre)
	if err != nil {
		t.Fatalf("PFDerive under %s: %v", pre.Version, err)
	}
	c, ok := old.ComponentByName(ComponentEDLIContribution)
	if !ok {
		t.Fatalf("under %s the EDLI rate is on record (%s%%) but no component appeared — the deferral test above would be vacuous",
			pre.Version, pre.EDLIContributionPct.Value)
	}
	wantMoney(t, "EDLI contribution under "+pre.Version, c.Amount, "75", "0.50% of the 15,000 EDLI wage")
	if _, deferred := old.DeferralFor(ComponentEDLIContribution); deferred {
		t.Error("a notified rate still produced a deferral")
	}
}

// TestPFDerive_EPFEPSEDLIWagesAreThreeDifferentNumbers is the guard against the
// single most likely wrong-number bug in pf.go: collapsing two of the three
// wage figures into one variable.
//
// The ECR has three separate columns and the portal reconciles each against its
// own contribution. Any collapse produces a file that is internally consistent
// and statutorily wrong.
func TestPFDerive_EPFEPSEDLIWagesAreThreeDifferentNumbers(t *testing.T) {
	rs := ssFixture(t)

	t.Run("EPS is capped while EPF is not", func(t *testing.T) {
		res, err := PFDerive(PFInput{
			Month:         WageMonth{Year: 2026, Month: time.August},
			StatutoryWage: d(t, "25000.0000"),
			Base:          PFBaseActual,
			EPSMember:     true,
			FirstMemberOn: ssDay(t, "2010-01-01"),
		}, rs)
		if err != nil {
			t.Fatalf("PFDerive: %v", err)
		}
		wantMoney(t, "EPFWages", res.EPFWages, "25000", "an ACTUAL election contributes on the whole wage")
		wantMoney(t, "EPSWages", res.EPSWages, "15000", "the pension ceiling is INDEPENDENT of the EPF base")
		if res.EPSWages.Equal(res.EPFWages) {
			t.Errorf("EPSWages == EPFWages == %s — the pension ceiling has been read off the EPF base; the employer would divert 8.33%% of an uncapped wage into a pension computed on capped pensionable salary, and the member never gets it back",
				res.EPSWages)
		}
	})

	t.Run("all three differ", func(t *testing.T) {
		// A separate EPS ceiling makes all three genuinely distinct WITH a
		// pension member, which is the case a collapse would hide.
		local := rs
		local.EPSWageCeiling = ssRate(t, "12000.0000", "a distinct pensionable-salary ceiling — fixture, so that the three columns cannot coincide")
		res, err := PFDerive(PFInput{
			Month:         WageMonth{Year: 2026, Month: time.August},
			StatutoryWage: d(t, "25000.0000"),
			Base:          PFBaseActual,
			EPSMember:     true,
			FirstMemberOn: ssDay(t, "2010-01-01"),
		}, local)
		if err != nil {
			t.Fatalf("PFDerive: %v", err)
		}
		wantMoney(t, "EPFWages", res.EPFWages, "25000", "ECR column 4")
		wantMoney(t, "EPSWages", res.EPSWages, "12000", "ECR column 5, at its OWN ceiling")
		wantMoney(t, "EDLIWages", res.EDLIWages, "15000", "ECR column 6, at its OWN ceiling")
		for _, p := range []struct {
			a, b   string
			av, bv decimal.Decimal
		}{
			{"EPFWages", "EPSWages", res.EPFWages, res.EPSWages},
			{"EPFWages", "EDLIWages", res.EPFWages, res.EDLIWages},
			{"EPSWages", "EDLIWages", res.EPSWages, res.EDLIWages},
		} {
			if p.av.Equal(p.bv) {
				t.Errorf("%s == %s == %s — two of the three ECR wage columns have been collapsed into one variable", p.a, p.b, p.av)
			}
		}
		// The pension is computed on ITS wage, not on the EPF wage.
		wantMoney(t, "EPS", res.EPS, "1000", "8.33% of 12,000 = 999.60, rounded to the nearest rupee")
	})

	// This subtest exists because of what the mutation proof found. Capping the
	// EPS wage at the EPF base under an ACTUAL election is caught by the
	// negative-employer-share guard at the real 12/8.33 rates — 8.33% of 25,000
	// is 2,083, which is more than the 1,800 statutory employer share, so the
	// engine refuses before the three wage columns are ever compared. That is a
	// good defence but a WEAK PROOF: it means the assertion below was not
	// actually the thing catching the bug, and a collapse at a rate combination
	// where the guard does not fire would have gone straight through.
	//
	// A synthetic 4% pension rate keeps the employer share comfortably positive,
	// so the collapse shows up as what it is — EPSWages equal to EPFWages —
	// rather than as an unrelated refusal.
	t.Run("the pension ceiling holds where the employer-share guard cannot fire", func(t *testing.T) {
		local := rs
		local.EPSPct = ssRate(t, "4", "synthetic pension rate low enough that a collapsed EPS wage still leaves a positive employer share")
		res, err := PFDerive(PFInput{
			Month:         WageMonth{Year: 2026, Month: time.August},
			StatutoryWage: d(t, "25000.0000"),
			Base:          PFBaseActual,
			EPSMember:     true,
			FirstMemberOn: ssDay(t, "2010-01-01"),
		}, local)
		if err != nil {
			t.Fatalf("PFDerive: %v", err)
		}
		wantMoney(t, "EPFWages", res.EPFWages, "25000", "an ACTUAL election contributes on the whole wage")
		wantMoney(t, "EPSWages", res.EPSWages, "15000", "the pension ceiling is read from its OWN notification, not from the EPF base")
		if res.EPSWages.Equal(res.EPFWages) {
			t.Errorf("EPSWages == EPFWages == %s — the pension wage has been capped at the EPF base. 8.33%% of an uncapped wage is diverted into a pension computed on CAPPED pensionable salary: the member loses the difference out of their provident fund permanently and the ECR's columns 4 and 5 stop being two different numbers",
				res.EPSWages)
		}
		wantMoney(t, "EPS", res.EPS, "600", "4% of the 15,000 pensionable salary; 4% of 25,000 would be 1,000")
		wantMoney(t, "EmployerEPF", res.EmployerEPF, "2400",
			"1,800 statutory less the 600 diversion, plus 1,200 on the above-ceiling excess — all still positive, so the negative-share guard cannot mask a collapse here")
	})

	t.Run("an excluded member has no EPS wages", func(t *testing.T) {
		res, err := PFDerive(PFInput{
			Month:         WageMonth{Year: 2026, Month: time.August},
			StatutoryWage: d(t, "25000.0000"),
			Base:          PFBaseActual,
			EPSMember:     true,
			FirstMemberOn: ssDay(t, "2015-04-01"),
		}, rs)
		if err != nil {
			t.Fatalf("PFDerive: %v", err)
		}
		wantMoney(t, "EPFWages", res.EPFWages, "25000", "ECR column 4")
		wantMoney(t, "EPSWages", res.EPSWages, "0", "an excluded member has no pensionable salary at all")
		wantMoney(t, "EDLIWages", res.EDLIWages, "15000", "EDLI does not care about pension membership")
	})
}

// TestPFDerive_SyntheticRulesetProvesOrderOfOperations pins the sequence, using
// deliberately awkward synthetic rates chosen so that each wrong order gives a
// DIFFERENT answer. With the real 12/12/8.33 several wrong orders coincide with
// the right one, which is exactly why a test on the real rates cannot pin the
// order at all.
func TestPFDerive_SyntheticRulesetProvesOrderOfOperations(t *testing.T) {
	// The rates are chosen per subtest, because a single set cannot make every
	// wrong order visible at once: the pair that separates "round then subtract"
	// from "subtract then round" is not the pair that separates "cap the wage"
	// from "cap the contribution".
	synthetic := func(employee, employer, eps string) SocialSecurityRuleSet {
		rs := ssFixture(t)
		rs.EPFEmployeePct = ssRate(t, employee, "synthetic employee rate chosen so a wrong order gives a DIFFERENT answer")
		rs.EPFEmployerPct = ssRate(t, employer, "synthetic employer rate chosen so a wrong order gives a DIFFERENT answer")
		rs.EPSPct = ssRate(t, eps, "synthetic pension rate chosen so a wrong order gives a DIFFERENT answer")
		rs.EPFWageCeiling = ssRate(t, "12000.0000", "synthetic EPF ceiling")
		rs.EPSWageCeiling = ssRate(t, "10000.0000", "synthetic pension ceiling, deliberately BELOW the EPF ceiling")
		return rs
	}

	t.Run("residual is rounded minus rounded", func(t *testing.T) {
		// 18.004% of 10,000 = 1,800.40, which rounds DOWN to 1,800.
		// 12.506% of 10,000 = 1,250.60, which rounds UP to 1,251.
		//
		// rounded(total) - rounded(EPS) = 1,800 - 1,251 = 549   <- correct
		// rounded(total - EPS)          = round(549.80) = 550   <- wrong
		//
		// The two roundings go in OPPOSITE directions, which is the only shape
		// that separates the orders. With the real 12 / 8.33 they happen to
		// agree, which is why a test on the real rates cannot pin this at all.
		rs := synthetic("18.004", "18.004", "12.506")
		res, err := PFDerive(PFInput{
			Month:         WageMonth{Year: 2026, Month: time.August},
			StatutoryWage: d(t, "10000.0000"),
			Base:          PFBaseActual,
			EPSMember:     true,
			FirstMemberOn: ssDay(t, "2010-01-01"),
		}, rs)
		if err != nil {
			t.Fatalf("PFDerive: %v", err)
		}
		wantMoney(t, "EPS", res.EPS, "1251", "12.506% of 10,000 is 1,250.60 and each component is rounded on its own")
		wantMoney(t, "EmployerEPF", res.EmployerEPF, "549",
			"rounded(employer total) - rounded(EPS); rounding the DIFFERENCE instead gives 550 and the two ECR columns then sum to 1,801 against a 1,800 challan")
		if got := res.EmployerEPF.Add(res.EPS); !got.Equal(d(t, "1800")) {
			t.Errorf("EmployerEPF + EPS = %s, want the 1800 actually remitted — deriving the residual from UNROUNDED values leaves the two ECR columns not summing to the challan, which the EPFO portal rejects", got)
		}
		wantMoney(t, "EmployeeEPF", res.EmployeeEPF, "1800", "the employee share is 18.004% of the wage rounded on its own, never the employer's figure reused")
	})

	t.Run("the wage is capped BEFORE the rate is applied", func(t *testing.T) {
		rs := synthetic("18.004", "20", "12.506")
		res, err := PFDerive(PFInput{
			Month:         WageMonth{Year: 2026, Month: time.August},
			StatutoryWage: d(t, "20000.0000"),
			Base:          PFBaseCeiling,
			EPSMember:     true,
			FirstMemberOn: ssDay(t, "2010-01-01"),
		}, rs)
		if err != nil {
			t.Fatalf("PFDerive: %v", err)
		}
		wantMoney(t, "EPFWages", res.EPFWages, "12000", "the CEILING election caps the WAGE")
		// 18.004% of 12,000 = 2,160.48 -> 2,160. Applying the rate to 20,000
		// first (3,600.80 -> 3,601) and then capping the CONTRIBUTION at the
		// employer's 20% of the ceiling (2,400) would give 2,400 here.
		wantMoney(t, "EmployeeEPF", res.EmployeeEPF, "2160",
			"rate applied to the CAPPED wage; capping the contribution instead gives 2,400 because the employee and employer rates differ")
		// employer total = 20% of 12,000 = 2,400; EPS = 12.506% of 10,000 = 1,251.
		wantMoney(t, "EmployerEPF", res.EmployerEPF, "1149", "2,400 less the 1,251 diversion")
	})

	t.Run("the two ceilings are read independently", func(t *testing.T) {
		rs := synthetic("18.004", "20", "12.506")
		res, err := PFDerive(PFInput{
			Month:         WageMonth{Year: 2026, Month: time.August},
			StatutoryWage: d(t, "11000.0000"),
			Base:          PFBaseActual,
			EPSMember:     true,
			FirstMemberOn: ssDay(t, "2010-01-01"),
		}, rs)
		if err != nil {
			t.Fatalf("PFDerive: %v", err)
		}
		wantMoney(t, "EPFWages", res.EPFWages, "11000", "below the 12,000 EPF ceiling")
		wantMoney(t, "EPSWages", res.EPSWages, "10000", "at the 10,000 pension ceiling — a DIFFERENT notification under a DIFFERENT scheme")
	})
}

// TestPFDerive_Rejects pins every refusal. Each one exists because the
// alternative is a plausible wrong number that nobody would query.
func TestPFDerive_Rejects(t *testing.T) {
	good := PFInput{
		Month:         WageMonth{Year: 2026, Month: time.August},
		StatutoryWage: d(t, "15000.0000"),
		Base:          PFBaseCeiling,
		EPSMember:     true,
		FirstMemberOn: ssDay(t, "2010-01-01"),
	}

	cases := []struct {
		name    string
		mutate  func(*PFInput, *SocialSecurityRuleSet)
		want    error
		because string
	}{
		{"empty ruleset", func(_ *PFInput, rs *SocialSecurityRuleSet) { rs.Version = "" }, ErrNoRuleSet,
			"a zero ruleset would compute every contribution as zero and look like a month nobody was paid"},
		{"negative statutory wage", func(in *PFInput, _ *SocialSecurityRuleSet) { in.StatutoryWage = d(t, "-1.0000") }, ErrNegativeStatutoryAmount,
			"RoundNearestRupee is half AWAY from zero and does not implement the statutory rule for negatives"},
		{"negative voluntary base", func(in *PFInput, _ *SocialSecurityRuleSet) { in.EmployeeVoluntaryBase = d(t, "-1.0000") }, ErrNegativeStatutoryAmount,
			"a negative VPF election is a refund dressed as a contribution"},
		{"unset base", func(in *PFInput, _ *SocialSecurityRuleSet) { in.Base = "" }, ErrUnknownPFBase,
			"the engine defaults nothing; payrolldb owns defaulting"},
		{"base with trailing space", func(in *PFInput, _ *SocialSecurityRuleSet) { in.Base = PFBase("CEILING ") }, ErrUnknownPFBase,
			"the vocabulary is exact, as the CHECK constraint will be"},
		{"unverified EPF ceiling", func(_ *PFInput, rs *SocialSecurityRuleSet) {
			rs.EPFWageCeiling = ssUnverified("a ceiling that was never verified against a primary source — fixture")
		}, ErrUnverifiedRate,
			"an unverified ceiling produces a plausible contribution indistinguishable from a correct one"},
		{"unverified employee rate", func(_ *PFInput, rs *SocialSecurityRuleSet) {
			rs.EPFEmployeePct = ssUnverified("a contribution rate that was never verified — fixture")
		}, ErrUnverifiedRate, "same"},
		{"unverified pension rate", func(_ *PFInput, rs *SocialSecurityRuleSet) {
			rs.EPSPct = ssUnverified("a pension rate that was never verified — fixture")
		}, ErrUnverifiedRate, "same"},
		{"VPF breaching the s.18(2) cap", func(in *PFInput, _ *SocialSecurityRuleSet) {
			// 12% of 15,000 statutory + 12% of 50,000 VPF = 7,800 out of a
			// 15,000 wage, comfortably past the 7,500 half.
			in.EmployeeVoluntaryBase = d(t, "50000.0000")
		}, ErrDeductionCapExceeded,
			"clamping instead would quietly pay less into the member's account than they elected, and they would find out from a passbook years later"},
		{"pension rate above the employer rate", func(_ *PFInput, rs *SocialSecurityRuleSet) {
			rs.EPSPct = ssRate(t, "15", "a malformed pension rate above the employer's 12% — fixture")
		}, ErrNegativeStatutoryAmount,
			"a negative employer EPF leg posts a debit where the ledger expects a credit and the voucher still balances"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := good
			rs := ssFixture(t)
			c.mutate(&in, &rs)
			res, err := PFDerive(in, rs)
			if !errors.Is(err, c.want) {
				t.Fatalf("PFDerive = (%+v, %v), want %v — %s", res, err, c.want, c.because)
			}
		})
	}

	// Non-vacuity: the unmutated input must SUCCEED, or every rejection above is
	// passing for the wrong reason.
	if _, err := PFDerive(good, ssFixture(t)); err != nil {
		t.Fatalf("the unmutated fixture errored (%v) — every rejection above would be vacuous", err)
	}
}

// TestPFDerive_ShippedRuleSetProducesTheCanonicalNumbers is the registry-drift
// guard for the PF path specifically.
//
// rates_test.go checks the rate FIELDS. This one runs them end to end and pins
// the numbers a payslip actually prints, so a rate that drifts in a way the
// field test tolerates (a ceiling moved into the wrong field, say) still fails
// here.
func TestPFDerive_ShippedRuleSetProducesTheCanonicalNumbers(t *testing.T) {
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
				t.Fatalf("SocialSecurityFor(%s) resolved %q, want %q", w.month, rs.Version, w.wantVersion)
			}
			res, err := PFDerive(PFInput{
				Month:         w.month,
				StatutoryWage: d(t, "15000.0000"),
				Base:          PFBaseCeiling,
				EPSMember:     true,
				FirstMemberOn: ssDay(t, "2010-01-01"),
			}, rs)
			if err != nil {
				t.Fatalf("PFDerive: %v", err)
			}
			wantMoney(t, "CeilingApplied", res.CeilingApplied, "15000", "the EPF wage ceiling")
			wantMoney(t, "EmployeeEPF", res.EmployeeEPF, "1800", "12% of 15,000 — the number on millions of payslips")
			wantMoney(t, "EPS", res.EPS, "1250", "8.33% of 15,000 rounded")
			wantMoney(t, "EmployerEPF", res.EmployerEPF, "550", "1,800 less the diversion")
			if res.RuleSetVersion != w.wantVersion {
				t.Errorf("RuleSetVersion = %q, want %q — the pay_run row stamps this and a re-run resolves by it", res.RuleSetVersion, w.wantVersion)
			}
		})
	}
}

// TestPFAndESIIntroduceNoRounderOutsideRoundingGo is this slice's own half of
// the package-wide rounding police.
//
// The four named helpers in rounding.go implement four DIFFERENT statutory
// rules — nearest paise, nearest rupee, next higher rupee, nearest ten — and the
// most likely wrong-number bug in the whole track is somebody six months from
// now applying one of them everywhere, or reaching for decimal's own rounders
// directly. This test parses pf.go and esi.go and fails on any rounding or
// truncating selector call in either, so the only way to round in these two
// files is to call a helper whose doc comment states which statute it
// implements.
func TestPFAndESIIntroduceNoRounderOutsideRoundingGo(t *testing.T) {
	banned := map[string]bool{
		"Round": true, "RoundUp": true, "RoundDown": true, "RoundBank": true,
		"RoundCash": true, "Ceil": true, "Floor": true, "Truncate": true,
		"RoundFloor": true, "RoundCeil": true, "Div": true, "DivRound": true,
	}
	files := []string{"pf.go", "esi.go"}
	for _, name := range files {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if banned[sel.Sel.Name] {
				t.Errorf("%s:%d calls .%s() directly. Rounding is a STATUTORY ACT: use one of the four named helpers in rounding.go, whose doc comments say which rule each implements. Div is banned alongside them because it consults decimal.DivisionPrecision, a mutable package global, and because dividing by one hundred is exactly Shift(-2) with no rounding at all.",
					name, fset.Position(sel.Sel.Pos()).Line, sel.Sel.Name)
			}
			return true
		})
	}

	// Non-vacuity: the scanner must actually find the calls that ARE there.
	// pf.go and esi.go call RoundNearestRupee and RoundUpRupee, which are plain
	// identifiers rather than selectors, so prove the walker sees selector calls
	// at all by checking it finds the Rate.Use() calls.
	found := 0
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "pf.go", nil, 0)
	if err != nil {
		t.Fatalf("parse pf.go: %v", err)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Use" {
			found++
		}
		return true
	})
	if found < 6 {
		t.Fatalf("the AST walker found only %d Rate.Use() calls in pf.go; it is not seeing selector calls and the ban above is vacuous", found)
	}
}

// ---------------------------------------------------------------------------
// THE PER-SITE ROUNDING SWEEP
// ---------------------------------------------------------------------------
// WHY THIS TEST EXISTS, and it is not a hypothetical. An adversarial pass ran
// the mandated review mutation — apply an income-tax rounding to a PF figure —
// by replacing RoundNearestRupee with RoundNearestTen on the employee's
// statutory EPF deduction, and THE ENTIRE TRACK STAYED GREEN: every payroll unit
// test, all fourteen golden-corpus fixtures, and the independent Python oracle.
// A per-site sweep of all twelve rounding call sites in pf.go found that TEN of
// them were blind.
//
// The mutation is a real wrong number and a silent one. On a statutory wage of
// 12,345 the employee's share is 12% = 1,481.40, which is 1,481 under the
// nearest-RUPEE rule the EPFO applies and 1,480 under the nearest-TEN rule that
// belongs to TOTAL INCOME alone. That rupee is deducted from a real person every
// month, it lands in the ECR's integer column where nothing carries a fraction
// to make it look odd, and across a workforce it is a reconciliation nobody can
// close.
//
// THE ROOT CAUSE WAS THE FIXTURE NUMBERS, NOT THE TESTS. Every wage the corpus
// and the unit tests used produced percentages that landed on a multiple of ten
// — 15,000 at the ceiling gives 1,800 and 1,250 — so the two rounders agreed
// everywhere and no assertion could tell them apart. The fix is arithmetic, not
// coverage: this table uses 12,345 and 2,345 precisely because NOTHING they
// produce is a multiple of ten.
//
// HOW TO READ THE TABLE. Each row names one rounding call site in pf.go, an
// input that reaches it, the value the statutory nearest-RUPEE rule produces,
// and the value the nearest-TEN rule would produce AT THAT SITE ALONE. The test
// asserts the engine gives `want`, and separately asserts want != underNearestTen
// so that a future edit which makes the two agree fails LOUDLY here instead of
// quietly turning the row into a tautology. That second assertion is the whole
// point: a briefed float-drift fixture in an earlier wave did not bite because
// the numbers chosen happened to agree, and nothing noticed.
//
// The empirical proof that each row bites is a mutation sweep run against a
// snapshot of the tree: each of the twelve sites replaced with RoundNearestTen
// in isolation, and each one required to make a NAMED test fail.

// pfRoundingProbe is one row of the sweep.
type pfRoundingProbe struct {
	// site names the call site in pf.go, in the words of the code.
	site string
	// in reaches that site with values that are not multiples of ten.
	in PFInput
	// ruleset is nil for the canonical ssFixture.
	ruleset func(*testing.T) SocialSecurityRuleSet
	// read pulls the observable field out of the result.
	read func(PFResult) decimal.Decimal
	// field names it, for the failure message.
	field string
	// want is the statutory nearest-RUPEE answer.
	want string
	// underNearestTen is what this site would produce if somebody applied the
	// s.288A total-income rounding here. It must DIFFER from want, or the row
	// proves nothing.
	underNearestTen string
	// consequence says who is hurt and by how much.
	consequence string
}

func pfProbeComponent(name string) func(PFResult) decimal.Decimal {
	return func(r PFResult) decimal.Decimal {
		c, ok := r.ComponentByName(name)
		if !ok {
			// Zero rather than a panic: the assertion below will report the
			// mismatch against `want` and name the component.
			return decimal.Zero
		}
		return c.Amount
	}
}

// TestPFDerive_EveryRoundingSiteIsObservable pins one observable consequence for
// EVERY rounding call site in pf.go. See the block comment above.
func TestPFDerive_EveryRoundingSiteIsObservable(t *testing.T) {
	// Statutory wage 12,345: below the 15,000 ceiling, and NOTHING it produces
	// is a multiple of ten. 12% = 1,481.40 and 8.33% = 1,028.3385.
	ceilingProbe := PFInput{
		Month:         WageMonth{Year: 2026, Month: time.August},
		StatutoryWage: d(t, "12345.0000"),
		Base:          PFBaseCeiling,
		EPSMember:     true,
		FirstMemberOn: ssDay(t, "2010-04-01"),
	}
	// The same wage with a VOLUNTARY election the employer has agreed to match
	// under para 26(6). The base 2,345 is already whole rupees and must survive
	// its rounding UNCHANGED; 12% of it is 281.40.
	voluntaryProbe := ceilingProbe
	voluntaryProbe.EmployeeVoluntaryBase = d(t, "2345.0000")
	voluntaryProbe.EmployerMatchesVoluntary = true
	// Base ACTUAL on 27,345: the statutory base is the 15,000 ceiling and the
	// above-ceiling EXCESS is 12,345. The excess is the only way to reach its
	// own rounding site — at the ceiling it is zero, and zero rounds identically
	// under every rule in the world.
	actualProbe := ceilingProbe
	actualProbe.StatutoryWage = d(t, "27345.0000")
	actualProbe.Base = PFBaseActual

	// The unnotified components have no amount today, so their rounding site is
	// only reachable once a rate IS notified. This ruleset simulates that
	// notification with the superseded 1976 Scheme's 0.50% — which is why it is
	// a TEST fixture and never rates.go: shipping that rate as if it were the
	// 2026 Scheme's would be inventing a number.
	notifiedEDLI := func(t *testing.T) SocialSecurityRuleSet {
		rs := ssFixture(t)
		rs.EDLIContributionPct = ssRate(t, "0.50",
			"EDLI Scheme 1976 para 7(1) 0.50% — used HERE ONLY to reach the code path that a future "+
				"notification will switch on; it is deliberately NOT in rates.go, where it would be an invented rate")
		return rs
	}

	probes := []pfRoundingProbe{
		{
			site:  "statutoryBase reduced to whole rupees before any percentage",
			in:    ceilingProbe,
			read:  func(r PFResult) decimal.Decimal { return r.EPFWages },
			field: "EPFWages",
			// 12,345 is already whole, so the nearest-rupee rounding leaves it
			// alone; the nearest-ten rounding moves it.
			want:            "12345",
			underNearestTen: "12350",
			consequence: "the ECR's EPF Wages column is an integer an inspector recomputes the contribution FROM; " +
				"printing 12,350 against a 12,345 wage makes every derived figure on the return unverifiable",
		},
		{
			site:            "excessBase reduced to whole rupees (the above-ceiling EPF wage)",
			in:              actualProbe,
			read:            func(r PFResult) decimal.Decimal { return r.EPFWages },
			field:           "EPFWages",
			want:            "27345",
			underNearestTen: "27350",
			consequence: "a para 26A(2) employer contributing on full wages would report 27,350 of EPF wages against a " +
				"27,345 wage and over-contribute 12% of the difference on both sides, every month",
		},
		{
			site:            "epsWages reduced to whole rupees",
			in:              ceilingProbe,
			read:            func(r PFResult) decimal.Decimal { return r.EPSWages },
			field:           "EPSWages",
			want:            "12345",
			underNearestTen: "12350",
			consequence: "EPS wages are a SEPARATE ECR column and the pensionable salary a defined benefit is computed " +
				"from; inflating them inflates a pension liability the scheme did not price",
		},
		{
			site:            "edliWages reduced to whole rupees",
			in:              ceilingProbe,
			read:            func(r PFResult) decimal.Decimal { return r.EDLIWages },
			field:           "EDLIWages",
			want:            "12345",
			underNearestTen: "12350",
			consequence:     "EDLI wages are a THIRD ECR column and the base the insurance benefit is computed on",
		},
		{
			site:            "the employee's statutory 12% share",
			in:              ceilingProbe,
			read:            pfProbeComponent(ComponentEPFEmployee),
			field:           "component " + ComponentEPFEmployee,
			want:            "1481",
			underNearestTen: "1480",
			consequence: "THIS IS THE MUTATION THE REVIEW RAN. 12% of 12,345 is 1,481.40. A rupee less is deducted from " +
				"the employee and a rupee less reaches their provident fund, every month, per employee, and the ECR " +
				"carries it as an integer so nothing looks wrong",
		},
		{
			site:            "the employer's statutory 12% total, before the pension diversion",
			in:              ceilingProbe,
			read:            pfProbeComponent(ComponentEPFEmployer),
			field:           "component " + ComponentEPFEmployer,
			want:            "453",
			underNearestTen: "452",
			consequence: "the employer's EPF line is the residual 1,481 - 1,028; rounding its total to ten makes the two " +
				"remitted components stop summing to the challan, which the EPFO portal rejects outright",
		},
		{
			site:            "the 8.33% EPS diversion",
			in:              ceilingProbe,
			read:            func(r PFResult) decimal.Decimal { return r.EPS },
			field:           "EPS",
			want:            "1028",
			underNearestTen: "1030",
			consequence: "8.33% of 12,345 is 1,028.3385. Two rupees a month diverted out of the member's provident fund " +
				"into a pension computed on a different base is money they do not get back",
		},
		{
			site:            "the employee's VOLUNTARY BASE reduced to whole rupees",
			in:              voluntaryProbe,
			read:            pfProbeComponent(ComponentEPFEmployeeVoluntary),
			field:           "component " + ComponentEPFEmployeeVoluntary,
			want:            "281",
			underNearestTen: "282",
			consequence: "the member ELECTED a base of 2,345; rounding the election itself to 2,350 deducts more from " +
				"their pay than they agreed to, which is a s.18(2) question and not a rounding one",
		},
		{
			site:            "the employer's matched VOLUNTARY BASE reduced to whole rupees",
			in:              voluntaryProbe,
			read:            pfProbeComponent(ComponentEPFEmployerVoluntary),
			field:           "component " + ComponentEPFEmployerVoluntary,
			want:            "281",
			underNearestTen: "282",
			consequence: "the para 26(6) matching agreement is on the base the member elected; the employer over-remits " +
				"on a base nobody agreed to",
		},
		{
			site:            "the employee's voluntary CONTRIBUTION",
			in:              voluntaryProbe,
			read:            pfProbeComponent(ComponentEPFEmployeeVoluntary),
			field:           "component " + ComponentEPFEmployeeVoluntary,
			want:            "281",
			underNearestTen: "280",
			consequence: "12% of 2,345 is 281.40; the VPF line is reported separately in Form V and a rupee out there is " +
				"a rupee the member's passbook will not reconcile",
		},
		{
			site:            "the employer's voluntary CONTRIBUTION",
			in:              voluntaryProbe,
			read:            pfProbeComponent(ComponentEPFEmployerVoluntary),
			field:           "component " + ComponentEPFEmployerVoluntary,
			want:            "281",
			underNearestTen: "280",
			consequence:     "the same rupee on the employer's side of the same voluntary line",
		},
		{
			site:            "an unnotified component's amount, once its rate IS notified",
			in:              ceilingProbe,
			ruleset:         notifiedEDLI,
			read:            pfProbeComponent(ComponentEDLIContribution),
			field:           "component " + ComponentEDLIContribution,
			want:            "62",
			underNearestTen: "60",
			consequence: "0.50% of 12,345 EDLI wages is 61.7250. This site computes NOTHING today because all three " +
				"rates are ConfUnverified, so it is the one rounding in this file that a corpus fixture cannot reach; " +
				"the day a notification arrives it must already be right",
		},
	}

	for _, p := range probes {
		p := p
		t.Run(p.site, func(t *testing.T) {
			// NON-VACUITY FIRST. If the two rules agree on the numbers chosen,
			// this row demonstrates nothing at all and must say so rather than
			// passing.
			if p.want == p.underNearestTen {
				t.Fatalf("the probe for %q is VACUOUS: the nearest-rupee and nearest-ten rules both give %s, "+
					"so nothing here could tell them apart. Choose an input whose percentages do not land on a "+
					"multiple of ten — that convergence is exactly how ten of these twelve sites went blind",
					p.site, p.want)
			}
			rs := ssFixture(t)
			if p.ruleset != nil {
				rs = p.ruleset(t)
			}
			res, err := PFDerive(p.in, rs)
			if err != nil {
				t.Fatalf("PFDerive: %v", err)
			}
			got := p.read(res)
			if !got.Equal(d(t, p.want)) {
				t.Errorf("%s = %s, want %s (nearest RUPEE, s.288B/s.516 and the EPFO's integer ECR columns). "+
					"Applying the s.288A nearest-TEN rounding at this site instead gives %s — %s",
					p.field, got.String(), p.want, p.underNearestTen, p.consequence)
			}
		})
	}

	// --- the tripwire: a THIRTEENTH rounding site must not go unswept ---------
	// The table above is a hand-built map from call site to observable
	// consequence, and nothing in Go forces it to stay complete. This count is
	// what forces it: add a rounding call to pf.go and the build fails until the
	// sweep table has a row for it and the row is proved to bite.
	sites := roundingCallSitesIn(t, "pf.go", "RoundNearestRupee")
	if len(sites) != len(probes) {
		t.Fatalf("pf.go has %d RoundNearestRupee call sites (lines %v) but the sweep table covers %d. "+
			"EVERY rounding site in this file must have a row above that observes it, and the row must be proved "+
			"to bite by mutating that one site to RoundNearestTen and watching a NAMED test fail. Ten of these "+
			"sites were silently blind once already", len(sites), sites, len(probes))
	}
}

// roundingCallSitesIn reports the source lines on which `fn` is CALLED in a file
// of this package.
//
// It matches *ast.Ident rather than text, so a mention of the name in a doc
// comment — pf.go's comments name RoundNearestRupee several times — is not
// counted as a call site. A text-based count would drift the moment somebody
// wrote a sentence about rounding.
func roundingCallSitesIn(t *testing.T, file, fn string) []int {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	var lines []int
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); ok && id.Name == fn {
			lines = append(lines, fset.Position(id.Pos()).Line)
		}
		return true
	})
	if len(lines) == 0 {
		t.Fatalf("no calls to %s found in %s; the AST walk is broken and the tripwire below is vacuous", fn, file)
	}
	return lines
}

// TestPFDerive_EDLIDeferralReasonDoesNotRepeatTheRetractedClaim pins the half of
// a deferral that a human actually reads.
//
// A Deferral has two strings. The Citation is provenance and is read by whoever
// goes back to the law; the REASON is the sentence that is persisted onto every
// payslip and shown to the employer. They drifted apart: rates.go RETRACTED, in
// EDLIContributionPct's citation, the assertion that the EDLI Scheme 2026 rate
// "is to be notified separately" and had not been notified — a rate IS reported
// at 0.50% of EDLI wages from 29 June 2026 on secondary sources — but the Reason
// went on saying the retracted thing.
//
// That is not a cosmetic mismatch. An employer told the rate DOES NOT EXIST has
// no reason to go and read the G.S.R., and reading the G.S.R. is the one action
// the deferral exists to provoke. Meanwhile roughly Rs 75 per member per month of
// EDLI premium accrues unbooked.
//
// The two ADMINISTRATIVE-CHARGE reasons are deliberately NOT covered by the
// negative assertion below: no 2026-Scheme administrative-charge rate of any kind
// could be found, so for those two "not notified" is the true state of knowledge.
func TestPFDerive_EDLIDeferralReasonDoesNotRepeatTheRetractedClaim(t *testing.T) {
	month := WageMonth{Year: 2026, Month: time.August}
	ss, err := SocialSecurityForMonth(month)
	if err != nil {
		t.Fatalf("SocialSecurityForMonth(%s): %v", month, err)
	}
	res, err := PFDerive(PFInput{
		Month:         month,
		StatutoryWage: d(t, "15000.0000"),
		Base:          PFBaseCeiling,
		EPSMember:     true,
		FirstMemberOn: ssDay(t, "2010-01-01"),
	}, ss)
	if err != nil {
		t.Fatalf("PFDerive: %v", err)
	}

	def, ok := res.DeferralFor(ComponentEDLIContribution)
	if !ok {
		t.Fatalf("%s (%s) neither books nor defers the EDLI contribution; this test has nothing to read",
			month, ss.Version)
	}

	// (1) IT MUST NOT REPEAT THE RETRACTED CLAIM. Stating as a fact that no rate
	// exists is what the rate row's own comment calls "an unverified zero dressed
	// up as a settled nil".
	for _, retracted := range []string{
		"to be notified separately",
		"had not been notified",
		"has not been notified",
		"actuarial valuation",
	} {
		if strings.Contains(strings.ToLower(def.Reason), strings.ToLower(retracted)) {
			t.Errorf("the EDLI_CONTRIBUTION deferral reason still says %q. rates.go RETRACTED that claim: a rate IS "+
				"reported at 0.50%% of EDLI wages from 29 June 2026. An employer who reads this on the payslip is "+
				"told the rate does not exist, will not go and read the G.S.R., and about Rs 75 per member per "+
				"month of EDLI premium keeps accruing unbooked. Reason = %q", retracted, def.Reason)
		}
	}

	// (2) IT MUST SAY WHAT THE CITATION SAYS. Each of these is a fact the
	// employer needs in order to act: a rate is reported; only on secondary
	// sources; the primary text is unread; money is accruing.
	for _, want := range []string{
		"0.50%",
		"29 June 2026",
		"SECONDARY SOURCES ONLY",
		"G.S.R.",
		"Rs 75 per member per month",
	} {
		if !strings.Contains(def.Reason, want) {
			t.Errorf("the EDLI_CONTRIBUTION deferral reason does not mention %q. The Reason is the half of the "+
				"deferral that reaches the employer, and it must carry what the citation carries. Reason = %q",
				want, def.Reason)
		}
	}

	// (3) AND IT MUST STILL BE A DEFERRAL. The correction is to the WORDING; the
	// rate stays ConfUnverified and the charge stays uncomputed, because
	// promoting a rate on a news report would charge an employer real money at a
	// figure nobody here has verified.
	if c, present := res.ComponentByName(ComponentEDLIContribution); present {
		t.Errorf("EDLI_CONTRIBUTION is now booked at Rs %s. Correcting the deferral's WORDING must not promote the "+
			"rate: no primary G.S.R. has been read and the row is still ConfUnverified", c.Amount)
	}

	// (4) The two administrative-charge reasons are a different state of
	// knowledge and are left alone. This is asserted so that a future sweep-edit
	// of "notified separately" does not quietly rewrite a sentence that is true.
	for _, name := range []string{ComponentEDLIAdminCharges, ComponentEPFAdminCharges} {
		adm, ok := res.DeferralFor(name)
		if !ok {
			t.Errorf("no deferral for %s under %s", name, ss.Version)
			continue
		}
		if !strings.Contains(adm.Reason, "notified separately") {
			t.Errorf("the %s deferral reason no longer says the charge is to be notified separately: %q. For the "+
				"ADMINISTRATIVE CHARGES that statement is true — no 2026-Scheme rate of any kind could be found — "+
				"and it must not be rewritten along with the EDLI one", name, adm.Reason)
		}
	}
}
