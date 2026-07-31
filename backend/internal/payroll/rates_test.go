package payroll

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestRateWindowsAreNonOverlappingAndGapFree walks both sorted window tables and
// asserts that they tile the timeline exactly: each window's To is the next
// window's From, and only the LAST window may be open-ended.
//
// The consequence of a GAP: a wage month in the hole resolves to nothing and
// SocialSecurityFor errors — loud, recoverable, but a pay run that cannot run.
// The consequence of an OVERLAP is far worse and is why this test exists: two
// windows containing the same date means the answer depends on iteration order,
// so the same employee's same month computes one contribution today and a
// different one after an unrelated edit reorders the slice, with no error
// anywhere and two different numbers already filed.
func TestRateWindowsAreNonOverlappingAndGapFree(t *testing.T) {
	if len(socialSecurityWindows) == 0 || len(incomeTaxWindows) == 0 {
		t.Fatal("a window table is empty — this test would be vacuous")
	}

	checkSocial := func(t *testing.T) {
		for i, w := range socialSecurityWindows {
			if w.From.IsZero() {
				t.Fatalf("socialSecurityWindows[%d] (%s) has a zero From: a window with no start covers all of history", i, w.Version)
			}
			if w.Version == "" {
				t.Fatalf("socialSecurityWindows[%d] has no Version; the pay_run row could not record which rates it used", i)
			}
			if w.Set.Version != w.Version {
				t.Errorf("socialSecurityWindows[%d]: window Version %q != ruleset Version %q — the stamped version would not name the rates actually applied",
					i, w.Version, w.Set.Version)
			}
			if i == len(socialSecurityWindows)-1 {
				continue
			}
			if w.To.IsZero() {
				t.Fatalf("socialSecurityWindows[%d] (%s) is open-ended but is not the last window: every later window is unreachable",
					i, w.Version)
			}
			next := socialSecurityWindows[i+1]
			if !w.To.Equal(next.From) {
				t.Errorf("socialSecurityWindows: %s ends %s but %s starts %s — %s",
					w.Version, w.To.Format("2006-01-02"), next.Version, next.From.Format("2006-01-02"),
					gapOrOverlap(w.To, next.From))
			}
		}
	}
	checkIncome := func(t *testing.T) {
		for i, w := range incomeTaxWindows {
			if w.From.IsZero() {
				t.Fatalf("incomeTaxWindows[%d] (%s) has a zero From", i, w.Version)
			}
			if w.Set.Version != w.Version {
				t.Errorf("incomeTaxWindows[%d]: window Version %q != ruleset Version %q", i, w.Version, w.Set.Version)
			}
			if !w.From.Equal(w.Set.TaxYear.Start()) {
				t.Errorf("incomeTaxWindows[%d] (%s): window starts %s but its ruleset is for tax year %s (starting %s) — "+
					"a lookup by tax year would serve the wrong year's slabs",
					i, w.Version, w.From.Format("2006-01-02"), w.Set.TaxYear, w.Set.TaxYear.Start().Format("2006-01-02"))
			}
			if i == len(incomeTaxWindows)-1 {
				continue
			}
			if w.To.IsZero() {
				t.Fatalf("incomeTaxWindows[%d] (%s) is open-ended but is not the last window", i, w.Version)
			}
			next := incomeTaxWindows[i+1]
			if !w.To.Equal(next.From) {
				t.Errorf("incomeTaxWindows: %s ends %s but %s starts %s — %s",
					w.Version, w.To.Format("2006-01-02"), next.Version, next.From.Format("2006-01-02"),
					gapOrOverlap(w.To, next.From))
			}
		}
	}
	t.Run("socialSecurity", checkSocial)
	t.Run("incomeTax", checkIncome)

	// The income-tax windows must ALL be closed, unlike social security's. A
	// Finance Act re-enacts the rates every year; an open-ended income-tax window
	// would silently tax FY 2035-36 under FY 2026-27's slabs.
	last := incomeTaxWindows[len(incomeTaxWindows)-1]
	if last.To.IsZero() {
		t.Errorf("the last income-tax window (%s) is open-ended. Income-tax rates are re-enacted annually by a "+
			"Finance Act, so an open window means a future year resolves to a stale ruleset with no error — "+
			"the exact silent-wrong-number failure ErrNoRuleSet exists to prevent", last.Version)
	}
}

func gapOrOverlap(to, nextFrom time.Time) string {
	if to.Before(nextFrom) {
		return "that is a GAP: a wage month in the hole cannot be computed at all"
	}
	return "that is an OVERLAP: the same date matches two windows and the answer depends on iteration order"
}

// TestRateWindowsAreHalfOpenAtTheBoundary places a lookup exactly ON each shared
// boundary date and on the day BEFORE it, which is the case an inclusive-both-ends
// range gets wrong.
//
// There are two boundaries, and the first one is a fixed BLOCKER. An earlier
// version of this table ran the pre-Code window all the way to 1 July 2026, so
// every wage month from Nov-2025 to Jun-2026 resolved to ss-2019-07 and was
// computed with DeemingThresholdPct=100 — no add-back at all. See
// TestCodeEraWindowOpensAtTheCommencementOfTheLabourCodes below for the money.
//
//   - 21 NOVEMBER 2025 — the Code on Social Security, 2020 and the Code on
//     Wages, 2019 came into force (S.O. 5319(E)). 20 Nov 2025 is the LAST
//     pre-Code day; 21 Nov 2025 belongs to the Code-era window.
//   - 29 JUNE 2026 — the EPF/EPS/EDLI Schemes 2026 commenced (G.S.R. 525(E) and
//     526(E), both dated 29.06.2026), superseding the 1952/1995/1976 Schemes.
//     NOT 1 July 2026, which is only when the rate notifications were published.
//     28 June 2026 is the last day under the saved schemes.
func TestRateWindowsAreHalfOpenAtTheBoundary(t *testing.T) {
	for _, c := range []struct {
		date time.Time
		want string
		why  string
	}{
		{dateUTC(2019, time.July, 1), "ss-2019-07", "first day of the first window is IN"},
		{dateUTC(2025, time.November, 20), "ss-2019-07", "the last day before the Labour Codes commenced"},
		{dateUTC(2025, time.November, 21), "ss-2025-11", "the commencement date itself belongs to the LATER window"},
		{dateUTC(2026, time.June, 28), "ss-2025-11", "the last day the saved 1952/1995/1976 Schemes were operative"},
		{dateUTC(2026, time.June, 29), "ss-2026-07", "the 2026 Schemes' commencement date belongs to the LATER window"},
		{dateUTC(2026, time.July, 1), "ss-2026-07", "the rate-notification date is INSIDE the 2026 window, not the start of it"},
		{dateUTC(2030, time.March, 31), "ss-2026-07", "open-ended tail"},
	} {
		rs, err := SocialSecurityFor(c.date)
		if err != nil {
			t.Fatalf("SocialSecurityFor(%s): unexpected error %v", c.date.Format("2006-01-02"), err)
		}
		if rs.Version != c.want {
			t.Errorf("SocialSecurityFor(%s) = %q, want %q (%s) — the [From, To) boundary is off by one window",
				c.date.Format("2006-01-02"), rs.Version, c.want, c.why)
		}
	}

	// And via the WageMonth sugar, which must resolve on the month's LAST day.
	for _, c := range []struct {
		m    WageMonth
		want string
	}{
		{WageMonth{2025, time.October}, "ss-2019-07"},  // wholly pre-Code
		{WageMonth{2025, time.November}, "ss-2025-11"}, // the Code commenced ON THE 21st: the month ends after it
		{WageMonth{2025, time.December}, "ss-2025-11"},
		{WageMonth{2026, time.March}, "ss-2025-11"}, // still FY 2025-26, still Code-era
		{WageMonth{2026, time.June}, "ss-2026-07"},  // ends 30 June, after the Schemes commenced on the 29th
		{WageMonth{2026, time.July}, "ss-2026-07"},
	} {
		rs, err := SocialSecurityForMonth(c.m)
		if err != nil {
			t.Fatalf("SocialSecurityForMonth(%s): %v", c.m, err)
		}
		if rs.Version != c.want {
			t.Errorf("SocialSecurityForMonth(%s) = %q, want %q", c.m, rs.Version, c.want)
		}
	}
}

// TestCodeEraWindowOpensAtTheCommencementOfTheLabourCodes is the regression test
// for the blocker, and it asserts the MONEY rather than the version string.
//
// A version-string assertion alone is too weak: a future edit could move the
// boundary and rename the window and the test would still pass. What actually
// matters is that a below-ceiling employee whose CTC is structured into
// allowances contributes on the DEEMED wage from the day the Codes commenced.
//
// The worked case is the one the adversarial review measured. BASIC 8,000 +
// HRA 12,000, total remuneration 20,000:
//
//   - Pre-Code (DeemingThresholdPct=100, so the excluded share is never capped):
//     statutory wage 8,000; employee EPF at 12% = 960.
//   - Code-era (DeemingThresholdPct=50): the excluded 12,000 exceeds one-half of
//     20,000, so 2,000 is deemed back; statutory wage 10,000; employee EPF 1,200.
//
// Rs 240 short on the employee side and Rs 240 short on the employer side, per
// employee per month. With the boundary at 1 July 2026 that ran for EIGHT wage
// months (Nov-2025 to Jun-2026) before anyone would have looked, and it carries
// s.14B interest and damages on top of the shortfall.
func TestCodeEraWindowOpensAtTheCommencementOfTheLabourCodes(t *testing.T) {
	components := []WageComponent{
		{Type: ComponentBasic, Amount: d(t, "8000")},
		{Type: ComponentHouseRentAllowance, Amount: d(t, "12000")},
	}

	for _, c := range []struct {
		month             WageMonth
		wantVersion       string
		wantThreshold     string
		wantStatutoryWage string
		wantEmployeeEPF   string
	}{
		// The last wholly pre-Code month. No add-back: the Code did not exist.
		{WageMonth{2025, time.October}, "ss-2019-07", "100", "8000", "960"},
		// The commencement month and every month after it, through the end of
		// FY 2025-26 and up to the 2026 Schemes.
		{WageMonth{2025, time.November}, "ss-2025-11", "50", "10000", "1200"},
		{WageMonth{2025, time.December}, "ss-2025-11", "50", "10000", "1200"},
		{WageMonth{2026, time.March}, "ss-2025-11", "50", "10000", "1200"},
		{WageMonth{2026, time.May}, "ss-2025-11", "50", "10000", "1200"},
		// And on past the scheme change, which does not touch the wage definition.
		{WageMonth{2026, time.August}, "ss-2026-07", "50", "10000", "1200"},
	} {
		rs, err := SocialSecurityForMonth(c.month)
		if err != nil {
			t.Fatalf("SocialSecurityForMonth(%s): %v", c.month, err)
		}
		if rs.Version != c.wantVersion {
			t.Errorf("%s resolves to %q, want %q. The Code on Social Security, 2020 and the Code on Wages, 2019 "+
				"were BROUGHT INTO FORCE on 21 November 2025 by S.O. 5319(E); a window boundary later than that "+
				"computes real payroll under a repealed wage definition", c.month, rs.Version, c.wantVersion)
		}
		if !rs.DeemingThresholdPct.Value.Equal(d(t, c.wantThreshold)) {
			t.Errorf("%s (%s): DeemingThresholdPct = %s, want %s", c.month, rs.Version, rs.DeemingThresholdPct.Value, c.wantThreshold)
		}

		got, err := StatutoryWages(WageInput{Month: c.month, Components: components}, rs)
		if err != nil {
			t.Fatalf("StatutoryWages(%s): %v", c.month, err)
		}
		if !got.StatutoryWage.Equal(d(t, c.wantStatutoryWage)) {
			t.Errorf("%s (%s): statutory wage on BASIC 8000 + HRA 12000 = %s, want %s. This is the base for EPF, "+
				"EPS, EDLI and ESI; understating it under-contributes on EVERY employee whose pay is structured "+
				"into allowances, which is precisely the avoidance the s.2(88) proviso exists to stop",
				c.month, rs.Version, got.StatutoryWage, c.wantStatutoryWage)
		}

		pf, err := PFDerive(PFInput{
			Month:         c.month,
			StatutoryWage: got.StatutoryWage,
			Base:          PFBaseCeiling,
			EPSMember:     true,
			FirstMemberOn: dateUTC(2010, time.January, 1),
		}, rs)
		if err != nil {
			t.Fatalf("PFDerive(%s): %v", c.month, err)
		}
		if !pf.EmployeeEPF.Equal(d(t, c.wantEmployeeEPF)) {
			t.Errorf("%s (%s): employee EPF = %s, want %s. Every rupee of the difference is a shortfall the "+
				"employer owes with s.14B interest and damages, and it is invisible on the payslip",
				c.month, rs.Version, pf.EmployeeEPF, c.wantEmployeeEPF)
		}
	}

	// The non-vacuity control: the two rulesets must actually DISAGREE on this
	// input. If a future edit gave the pre-Code window a threshold of 50 the loop
	// above would still pass every row while asserting nothing about the change
	// of law.
	pre, err := SocialSecurityForMonth(WageMonth{2025, time.October})
	if err != nil {
		t.Fatalf("SocialSecurityForMonth(2025-10): %v", err)
	}
	code, err := SocialSecurityForMonth(WageMonth{2025, time.November})
	if err != nil {
		t.Fatalf("SocialSecurityForMonth(2025-11): %v", err)
	}
	if pre.Version == code.Version {
		t.Fatal("October and November 2025 resolve to the SAME ruleset — the Codes commenced on 21 November 2025 " +
			"and this test is not demonstrating the boundary at all")
	}
	if pre.DeemingThresholdPct.Value.Equal(code.DeemingThresholdPct.Value) {
		t.Fatalf("both windows carry DeemingThresholdPct=%s; the pre-Code window must be 100 (no cap on the "+
			"excluded share) and the Code-era window 50, or this test proves nothing", pre.DeemingThresholdPct.Value)
	}
}

// TestSocialSecurityFor_UncoveredDateIsAnError is the guard against the single
// most plausible-looking wrong fix in this package: "if no window matches, just
// use the current rates".
//
// A June 2019 pay run computed under the July 2026 scheme produces a number that
// looks completely normal on a payslip, reconciles internally, and is wrong — and
// nobody re-derives an old month by hand, so it would never be found. This is
// exactly what inventory.ValuerFor's erroring default arm exists to prevent and
// exactly the bug the compliance path's DefaultTDSRulesetVersion still has.
//
// The test asserts BOTH halves, because a fallback would satisfy neither: the
// error must be ErrNoRuleSet, AND the returned ruleset must be the ZERO VALUE
// rather than the newest window's.
func TestSocialSecurityFor_UncoveredDateIsAnError(t *testing.T) {
	newest := socialSecurityWindows[len(socialSecurityWindows)-1].Set
	oldest := socialSecurityWindows[0].Set

	for _, when := range []time.Time{
		dateUTC(2019, time.June, 30), // one day before the first window
		dateUTC(2015, time.March, 31),
		dateUTC(1999, time.December, 31),
	} {
		rs, err := SocialSecurityFor(when)
		if !errors.Is(err, ErrNoRuleSet) {
			t.Errorf("SocialSecurityFor(%s): err = %v, want ErrNoRuleSet. A silent fallback would compute this "+
				"pay run under rates that were not in force, producing a plausible, wrong, un-auditable number "+
				"that nobody would ever re-derive", when.Format("2006-01-02"), err)
		}
		if !reflect.DeepEqual(rs, SocialSecurityRuleSet{}) {
			t.Errorf("SocialSecurityFor(%s) returned a NON-ZERO ruleset (version %q) alongside its error. "+
				"A caller that checks the value before the error would silently compute under the wrong rates",
				when.Format("2006-01-02"), rs.Version)
		}
		if rs.Version == newest.Version {
			t.Errorf("SocialSecurityFor(%s) fell back to the NEWEST window %q — the 'just use the current rates' "+
				"bug this test exists for", when.Format("2006-01-02"), newest.Version)
		}
		if rs.Version == oldest.Version {
			t.Errorf("SocialSecurityFor(%s) fell back to the OLDEST window %q, which is equally wrong",
				when.Format("2006-01-02"), oldest.Version)
		}
		if !rs.EPFEmployeePct.Value.IsZero() {
			t.Errorf("SocialSecurityFor(%s) returned an EPF employee rate of %s with its error; the zero value "+
				"carries no rate at all", when.Format("2006-01-02"), rs.EPFEmployeePct.Value)
		}
	}

	// The negative control: the very next day IS covered, so the test above is
	// not passing merely because the resolver errors on everything.
	if _, err := SocialSecurityFor(dateUTC(2019, time.July, 1)); err != nil {
		t.Fatalf("SocialSecurityFor(2019-07-01) errored (%v) — the uncovered-date test above would be vacuous", err)
	}
}

// TestIncomeTaxFor_UncoveredTaxYearIsAnError is the same guard on the annual
// side. Its consequence is larger, not smaller: an income-tax ruleset silently
// carried into a later year means an entire year of salary TDS computed under
// repealed slabs, discovered when the employee's Form 130 does not reconcile.
func TestIncomeTaxFor_UncoveredTaxYearIsAnError(t *testing.T) {
	newest := incomeTaxWindows[len(incomeTaxWindows)-1].Set

	for _, ty := range []TaxYear{
		{StartYear: 2027}, // the year AFTER the last shipped ruleset
		{StartYear: 2030},
		{StartYear: 2019}, // before the first
	} {
		rs, err := IncomeTaxFor(ty)
		if !errors.Is(err, ErrNoRuleSet) {
			t.Fatalf("IncomeTaxFor(%s): err = %v, want ErrNoRuleSet. Silently reusing %q would deduct a whole "+
				"year of salary TDS under slabs that were repealed by a Finance Act nobody read",
				ty, err, newest.Version)
		}
		if !reflect.DeepEqual(rs, IncomeTaxRuleSet{}) {
			t.Fatalf("IncomeTaxFor(%s) returned a non-zero ruleset (version %q, %d new-regime slabs) alongside "+
				"its error", ty, rs.Version, len(rs.NewRegimeSlabs))
		}
	}

	// Negative control.
	rs, err := IncomeTaxFor(TaxYear{StartYear: 2026})
	if err != nil {
		t.Fatalf("IncomeTaxFor(2026-27) errored (%v) — the uncovered-year test would be vacuous", err)
	}
	if rs.Version != "it-ty-2026-27" {
		t.Fatalf("IncomeTaxFor(2026-27) = %q, want it-ty-2026-27", rs.Version)
	}
	if rs.TaxYear != (TaxYear{StartYear: 2026}) {
		t.Fatalf("IncomeTaxFor(2026-27) served a ruleset stamped for %s", rs.TaxYear)
	}
}

// TestSelectorsRejectUnimplementedMembers pins the ValuerFor idiom on all three
// closed vocabularies: a plausible member that has NOT been implemented must
// ERROR, not quietly behave like some other member.
//
// The consequence otherwise, per selector:
//   - Regime: an unrecognised election taxed under whichever arm came first.
//   - PFBase: a "higher of" election contributing on the ceiling, understating
//     the employee's own PF balance for the life of their service.
//   - ComponentType: a component silently treated as excluded from wages,
//     under-contributing PF and ESI for everybody who has it.
func TestSelectorsRejectUnimplementedMembers(t *testing.T) {
	t.Run("regime", func(t *testing.T) {
		for _, s := range []string{"", "PRESUMPTIVE", "new", "New", "NEW ", "115BAC", "DEFAULT"} {
			got, err := RegimeFor(s)
			if !errors.Is(err, ErrUnknownRegime) {
				t.Errorf("RegimeFor(%q) = (%q, %v), want ErrUnknownRegime", s, got, err)
			}
			if got != "" {
				t.Errorf("RegimeFor(%q) returned regime %q alongside its error", s, got)
			}
		}
		// The engine defaults NOTHING: the empty string must error here even
		// though s.202 makes NEW the statutory default. payrolldb applies
		// DefaultRegime before it calls in; silently defaulting inside the
		// selector would tax an employee who elected OLD under the new regime.
		if _, err := RegimeFor(""); !errors.Is(err, ErrUnknownRegime) {
			t.Error("RegimeFor(\"\") must error: defaulting belongs to payrolldb, not to the engine")
		}
		if DefaultRegime != RegimeNew {
			t.Errorf("DefaultRegime = %q, want NEW (Income-tax Act 2025 s.202 makes the new regime the default)", DefaultRegime)
		}
		for _, s := range []string{"NEW", "OLD"} {
			if _, err := RegimeFor(s); err != nil {
				t.Errorf("RegimeFor(%q) errored (%v) — the rejection test above would be vacuous", s, err)
			}
		}
	})

	t.Run("pfBase", func(t *testing.T) {
		for _, s := range []string{"", "HIGHER_OF", "ceiling", "ACTUAL_WAGE", "BOTH"} {
			got, err := PFBaseFor(s)
			if !errors.Is(err, ErrUnknownPFBase) {
				t.Errorf("PFBaseFor(%q) = (%q, %v), want ErrUnknownPFBase", s, got, err)
			}
			if got != "" {
				t.Errorf("PFBaseFor(%q) returned base %q alongside its error", s, got)
			}
		}
		for _, s := range []string{"CEILING", "ACTUAL"} {
			if _, err := PFBaseFor(s); err != nil {
				t.Errorf("PFBaseFor(%q) errored (%v) — the rejection test would be vacuous", s, err)
			}
		}
	})

	t.Run("componentType", func(t *testing.T) {
		// Every one of these is a REAL payroll component that this build does not
		// yet classify. Each must error rather than fall through to "excluded".
		for _, s := range []string{
			"", "LEAVE_TRAVEL_ALLOWANCE", "PROFESSIONAL_TAX", "MEDICAL_ALLOWANCE",
			"CHILDREN_EDUCATION_ALLOWANCE", "LEAVE_ENCASHMENT", "basic", "Basic",
		} {
			got, err := ComponentTypeFor(s)
			if !errors.Is(err, ErrUnknownComponentType) {
				t.Errorf("ComponentTypeFor(%q) = (%q, %v), want ErrUnknownComponentType. A component that "+
					"falls through to 'not wages' under-contributes PF and ESI for every employee who has it",
					s, got, err)
			}
			if got != "" {
				t.Errorf("ComponentTypeFor(%q) returned type %q alongside its error", s, got)
			}
		}
		for _, s := range []string{"BASIC", "DA", "HRA", "SPECIAL_ALLOWANCE", "GRATUITY"} {
			if _, err := ComponentTypeFor(s); err != nil {
				t.Errorf("ComponentTypeFor(%q) errored (%v) — the rejection test would be vacuous", s, err)
			}
		}
	})
}

// TestRatesAreDecimalStringsNotFloats has two halves, because neither alone is
// enough.
//
// Half one, a REFLECT WALK over every shipped ruleset: every Rate must carry a
// citation of at least 40 characters, a non-zero effective-from date and a
// Confidence from the closed set, and no Rate's Value may carry the long
// fractional tail that a binary float leaves behind.
//
// Half two, an AST SCAN of the package's non-test source, because reflection
// CANNOT see how a decimal was constructed: decimal.NewFromFloat(0.0833) and
// decimal.RequireFromString("8.33") both produce a Decimal, and only the source
// distinguishes them. The nearest double to 8.33% is 0.0833000000000000018...,
// and a rate that is not exactly the statutory rate produces contributions off
// by paise in a direction nobody chose, on every employee, forever.
func TestRatesAreDecimalStringsNotFloats(t *testing.T) {
	seen := 0
	for _, w := range socialSecurityWindows {
		seen += walkRates(t, "socialSecurity/"+w.Version, reflect.ValueOf(w.Set))
	}
	for _, w := range incomeTaxWindows {
		seen += walkRates(t, "incomeTax/"+w.Version, reflect.ValueOf(w.Set))
	}
	if seen < 30 {
		t.Fatalf("the reflect walk found only %d Rate values across every shipped ruleset; it is not reaching "+
			"the tables (nested slices and maps must be walked too)", seen)
	}
	t.Logf("provenance checked on %d Rate values", seen)

	// --- half two: no float may be constructed anywhere in this package ---
	fset := token.NewFileSet()
	scanned := 0
	for _, path := range packageGoFiles(t, false) {
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		scanned++
		base := filepath.Base(path)
		ast.Inspect(f, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.BasicLit:
				if v.Kind == token.FLOAT {
					t.Errorf("%s:%d: float literal %s. Money and statutory rates are decimal.Decimal built from "+
						"STRING literals; a float literal here means somebody's contribution is off by the binary "+
						"representation error", base, fset.Position(v.Pos()).Line, v.Value)
				}
			case *ast.Ident:
				if v.Name == "float64" || v.Name == "float32" {
					t.Errorf("%s:%d: %s appears in the payroll engine. There is no float in this package by design",
						base, fset.Position(v.Pos()).Line, v.Name)
				}
			case *ast.SelectorExpr:
				if strings.HasPrefix(v.Sel.Name, "NewFromFloat") {
					t.Errorf("%s:%d: %s constructs a decimal FROM A FLOAT. decimal.NewFromFloat(0.0833) is not "+
						"8.33%%; use decimal.RequireFromString(\"8.33\")", base, fset.Position(v.Sel.Pos()).Line, v.Sel.Name)
				}
			}
			return true
		})
	}
	if scanned < 4 {
		t.Fatalf("the float scan read only %d non-test files; it is vacuous", scanned)
	}
	t.Logf("no float constructor or literal across %d source files", scanned)
}

// walkRates recursively finds every Rate inside a value and checks its
// provenance. It walks structs, slices, arrays and maps so that a rate hidden
// inside a slab table or an age-band map cannot escape the check.
func walkRates(t *testing.T, path string, v reflect.Value) int {
	t.Helper()
	rateType := reflect.TypeOf(Rate{})

	if v.Type() == rateType {
		r, ok := v.Interface().(Rate)
		if !ok {
			t.Fatalf("%s: value is not a Rate", path)
		}
		checkRate(t, path, r)
		return 1
	}

	n := 0
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if !v.Type().Field(i).IsExported() {
				continue
			}
			n += walkRates(t, path+"."+v.Type().Field(i).Name, v.Field(i))
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			n += walkRates(t, path+"["+itoa(i)+"]", v.Index(i))
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			n += walkRates(t, path+"["+k.String()+"]", v.MapIndex(k))
		}
	}
	return n
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func checkRate(t *testing.T, path string, r Rate) {
	t.Helper()
	if len(r.Citation) < 40 {
		t.Errorf("%s: citation is %d characters (%q). Every statutory number must name the statute, rule or "+
			"notification it came from and its date; a short citation is a rate nobody can check",
			path, len(r.Citation), r.Citation)
	}
	if r.From.IsZero() {
		t.Errorf("%s: no effective-from date. A rate without one cannot be placed in time, and a re-run of an "+
			"older month cannot tell whether it applied", path)
	}
	switch r.Confidence {
	case ConfHigh, ConfMedium, ConfUnverified:
	default:
		t.Errorf("%s: Confidence %q is outside the closed set {HIGH, MEDIUM, UNVERIFIED}", path, r.Confidence)
	}
	// A value with more than six decimal places is the signature of a float that
	// went through decimal.NewFromFloat: no statutory rate or amount in Indian
	// payroll law is written to more precision than that.
	if r.Value.Exponent() < -6 {
		t.Errorf("%s: value %s carries %d decimal places. No statutory rate is written to that precision — this "+
			"is the fractional tail a binary float leaves behind", path, r.Value, -r.Value.Exponent())
	}
	if r.Value.IsNegative() {
		t.Errorf("%s: value %s is negative; no rate, ceiling or limit here can be", path, r.Value)
	}
	// A VerifiedOn is a CLAIM that somebody read the primary text. It must never
	// sit on a row that is simultaneously marked unverified.
	if !r.VerifiedOn.IsZero() && r.Confidence == ConfUnverified {
		t.Errorf("%s: VerifiedOn is set (%s) but Confidence is UNVERIFIED — one of the two is a lie",
			path, r.VerifiedOn.Format("2006-01-02"))
	}
	// Rate.Use is the ONLY sanctioned way to consume a rate, and it must refuse
	// exactly the unverified ones.
	got, err := r.Use()
	if r.Confidence == ConfUnverified {
		if !errors.Is(err, ErrUnverifiedRate) {
			t.Errorf("%s: Confidence is UNVERIFIED but Use() returned (%s, %v) instead of ErrUnverifiedRate", path, got, err)
		}
	} else if err != nil {
		t.Errorf("%s: Use() errored on a %s-confidence rate: %v", path, r.Confidence, err)
	}
}

// TestUnverifiedRatesAreMarked pins the ADMINISTRATIVE-CHARGE blocker in place.
//
// It used to pin the EDLI contribution too. That row was promoted once
// S.O. 3581(E) of 1 July 2026 was identified (0.50% w.e.f. 29 June 2026), so it
// is asserted the OTHER way now — see the consumable check below, which is what
// stops a later change quietly demoting it and re-opening the unbooked liability.
//
// The three rows that remain: no standalone administrative-charge rate
// notification exists under the 2026 Schemes as at 30 July 2026. The honest
// handling is a REFUSAL carrying the citation. The dishonest one has TWO forms
// now, and both look like a one-line fix that makes a failing test pass —
// copying the superseded 1952/1976 0.50% / Rs 500 up from ss-2019-07, or copying
// S.O. 2701(E)'s INSPECTION charges (EPF 0.35% min Rs 8,750), which are a
// different levy on exempted establishments only. This test makes both fail.
func TestUnverifiedRatesAreMarked(t *testing.T) {
	rs, err := SocialSecurityFor(dateUTC(2026, time.July, 1))
	if err != nil {
		t.Fatalf("SocialSecurityFor(2026-07-01): %v", err)
	}
	if rs.Version != "ss-2026-07" {
		t.Fatalf("resolved %q, want ss-2026-07", rs.Version)
	}

	for _, c := range []struct {
		name string
		rate Rate
	}{
		{"EDLIAdminChargePct", rs.EDLIAdminChargePct},
		{"EPFAdminChargePct", rs.EPFAdminChargePct},
		{"EPFAdminChargeMin", rs.EPFAdminChargeMin},
	} {
		if c.rate.Confidence != ConfUnverified {
			t.Errorf("ss-2026-07.%s has Confidence %q, want UNVERIFIED. No standalone administrative-charge rate "+
				"notification exists under the 2026 Schemes as at 30 July 2026; the three notifications of "+
				"1 July 2026 set CONTRIBUTION rates only. Promoting this row means charging an employer real money "+
				"at a figure nobody has. If a notification HAS now been issued, promote it with its number and "+
				"together with the pf- corpus fixtures and PF_DEFERRED_COMPONENTS — but do NOT reach for "+
				"S.O. 2701(E) (inspection charges, exempted establishments, a different levy) or the superseded "+
				"1952/1976 figures",
				c.name, c.rate.Confidence)
		}
		if _, err := c.rate.Use(); !errors.Is(err, ErrUnverifiedRate) {
			t.Errorf("ss-2026-07.%s: Use() = %v, want ErrUnverifiedRate — the engine must emit a Deferral, not a number", c.name, err)
		}
		if !c.rate.Value.IsZero() {
			t.Errorf("ss-2026-07.%s carries a non-zero value %s while marked UNVERIFIED. An unverified row must "+
				"hold nothing usable, so that code which bypasses Use() cannot pick up a plausible number",
				c.name, c.rate.Value)
		}
	}

	// THE EDLI CONTRIBUTION MUST STAY CONSUMABLE IN THE 2026 WINDOW.
	//
	// This is the opposite assertion from the loop above, and it is here because
	// this row has been wrong in BOTH directions. It was once an unverified zero
	// described as a settled nil (a liability silently unbooked); it was then a
	// correct refusal that still left about Rs 75 per member per month accruing,
	// which the handoff called a release blocker. Now that S.O. 3581(E) has been
	// identified it computes — and a change that demoted it back to UNVERIFIED
	// "to be safe" would re-open the blocker, so it fails here instead.
	//
	// VerifiedOn is deliberately NOT asserted non-zero: no primary Gazette text
	// was read, and setting VerifiedOn to satisfy a test is exactly what the Rate
	// doc forbids. ConfMedium is the honest level.
	if rs.EDLIContributionPct.Confidence == ConfUnverified {
		t.Error("ss-2026-07.EDLIContributionPct is UNVERIFIED again. It is 0.50% of EDLI wages w.e.f. " +
			"29 June 2026 under S.O. 3581(E) dated 1 July 2026 — note the rate is NOT in G.S.R. 526(E), whose " +
			"para 5(1)-(2) leaves it to a separate notification. Refusing it does not fail safe: it leaves about " +
			"Rs 75 per member per month of employer liability accruing unbooked, with s.14B interest on top")
	}
	if v, err := rs.EDLIContributionPct.Use(); err != nil {
		t.Errorf("ss-2026-07.EDLIContributionPct: Use() = %v, want the rate — the premium must be booked", err)
	} else if v.String() != "0.5" {
		t.Errorf("ss-2026-07.EDLIContributionPct = %s, want 0.5 (per cent of EDLI wages)", v)
	}

	// The negative control, on BOTH pre-2026 windows: the EDLI rate IS known
	// there (the 1976 Scheme was in force, and remained in force through the
	// Code-era interim window until the 2026 Scheme superseded it on 29 June
	// 2026), so it must remain usable. Without this, the refusal above would be
	// indistinguishable from "EDLI is never computed".
	//
	// The ss-2025-11 half also guards the other direction of the boundary fix:
	// extending the 2026 window's refusal back over Nov-2025 to Jun-2026 would
	// REFUSE a contribution that was actually payable at 0.50% for eight months.
	for _, when := range []struct {
		date    time.Time
		version string
	}{
		{dateUTC(2025, time.November, 20), "ss-2019-07"}, // last pre-Code day
		{dateUTC(2026, time.June, 28), "ss-2025-11"},     // last day under the saved 1976 Scheme
	} {
		old, err := SocialSecurityFor(when.date)
		if err != nil {
			t.Fatalf("SocialSecurityFor(%s): %v", when.date.Format("2006-01-02"), err)
		}
		if old.Version != when.version {
			t.Fatalf("SocialSecurityFor(%s) = %q, want %q", when.date.Format("2006-01-02"), old.Version, when.version)
		}
		v, err := old.EDLIContributionPct.Use()
		if err != nil {
			t.Fatalf("%s.EDLIContributionPct.Use() = %v; the 1976 Scheme's 0.50%% is known and was in force in "+
				"this window, so it must remain usable — otherwise the 2026 refusal is untestable and a real "+
				"contribution goes unbooked", when.version, err)
		}
		if !v.Equal(d(t, "0.50")) {
			t.Errorf("%s.EDLIContributionPct = %s, want 0.50 (EDLI Scheme 1976 para 7)", when.version, v)
		}
	}

	// And the rates the engine relies on every month must NOT be unverified, or
	// no pay run could ever be computed.
	for _, c := range []struct {
		name string
		rate Rate
	}{
		{"EPFEmployeePct", rs.EPFEmployeePct},
		{"EPFEmployerPct", rs.EPFEmployerPct},
		{"EPSPct", rs.EPSPct},
		{"EPFWageCeiling", rs.EPFWageCeiling},
		{"ESIEmployeePct", rs.ESIEmployeePct},
		{"ESIEmployerPct", rs.ESIEmployerPct},
		{"ESIWageCeiling", rs.ESIWageCeiling},
		{"DeemingThresholdPct", rs.DeemingThresholdPct},
	} {
		if _, err := c.rate.Use(); err != nil {
			t.Errorf("ss-2026-07.%s is unusable (%v); no pay run could be computed", c.name, err)
		}
	}
}

// TestShippedRateValues pins the actual numbers. It is deliberately a SECOND
// statement of them, written from the citations rather than copied from the
// table, so that an accidental edit to rates.go — a transposed digit in a
// ceiling, a 3.25 becoming 3.52 — fails here rather than in somebody's payslip.
func TestShippedRateValues(t *testing.T) {
	ss, err := SocialSecurityFor(dateUTC(2026, time.August, 31))
	if err != nil {
		t.Fatalf("SocialSecurityFor: %v", err)
	}
	for _, c := range []struct {
		name, want string
		rate       Rate
	}{
		{"EPF employee %", "12", ss.EPFEmployeePct},
		{"EPF employer %", "12", ss.EPFEmployerPct},
		{"EPS %", "8.33", ss.EPSPct},
		{"EPF wage ceiling", "15000", ss.EPFWageCeiling},
		{"EPS wage ceiling", "15000", ss.EPSWageCeiling},
		{"EDLI wage ceiling", "15000", ss.EDLIWageCeiling},
		{"ESI employee %", "0.75", ss.ESIEmployeePct},
		{"ESI employer %", "3.25", ss.ESIEmployerPct},
		{"ESI wage ceiling", "21000", ss.ESIWageCeiling},
		{"ESI low-wage daily limit", "176", ss.ESILowWageDailyLimit},
		{"deeming threshold %", "50", ss.DeemingThresholdPct},
	} {
		if !c.rate.Value.Equal(d(t, c.want)) {
			t.Errorf("ss-2026-07 %s = %s, want %s", c.name, c.rate.Value, c.want)
		}
	}

	it, err := IncomeTaxFor(TaxYear{StartYear: 2026})
	if err != nil {
		t.Fatalf("IncomeTaxFor: %v", err)
	}
	wantNew := []struct{ from, to, rate string }{
		{"0", "400000", "0"},
		{"400000", "800000", "5"},
		{"800000", "1200000", "10"},
		{"1200000", "1600000", "15"},
		{"1600000", "2000000", "20"},
		{"2000000", "2400000", "25"},
		{"2400000", "0", "30"},
	}
	if len(it.NewRegimeSlabs) != len(wantNew) {
		t.Fatalf("new-regime slab table has %d bands, want %d", len(it.NewRegimeSlabs), len(wantNew))
	}
	for i, w := range wantNew {
		s := it.NewRegimeSlabs[i]
		if !s.From.Equal(d(t, w.from)) || !s.To.Equal(d(t, w.to)) || !s.Rate.Value.Equal(d(t, w.rate)) {
			t.Errorf("new-regime slab %d = [%s, %s) at %s%%, want [%s, %s) at %s%%",
				i, s.From, s.To, s.Rate.Value, w.from, w.to, w.rate)
		}
		if i > 0 && !s.From.Equal(it.NewRegimeSlabs[i-1].To) {
			t.Errorf("new-regime slab %d starts at %s but slab %d ended at %s — a gap or overlap in the slab "+
				"table taxes some incomes twice or not at all", i, s.From, i-1, it.NewRegimeSlabs[i-1].To)
		}
	}

	// The old regime's three age bands, and specifically that the super-senior
	// band has NO 5% slab: its exemption of 5,00,000 swallows it, and a
	// zero-width [5,00,000, 5,00,000) band left in the table would be a band
	// that can never be hit sitting in the middle of the arithmetic.
	for _, c := range []struct {
		band      AgeBand
		exemption string
		bands     int
	}{
		{AgeBelow60, "250000", 4},
		{AgeSenior, "300000", 4},
		{AgeSuperSenior, "500000", 3},
	} {
		slabs, err := it.SlabsFor(RegimeOld, c.band)
		if err != nil {
			t.Fatalf("SlabsFor(OLD, %s): %v", c.band, err)
		}
		if len(slabs) != c.bands {
			t.Errorf("old regime %s has %d bands, want %d", c.band, len(slabs), c.bands)
		}
		if !slabs[0].To.Equal(d(t, c.exemption)) {
			t.Errorf("old regime %s basic exemption = %s, want %s", c.band, slabs[0].To, c.exemption)
		}
		if !slabs[0].Rate.Value.IsZero() {
			t.Errorf("old regime %s taxes the basic exemption at %s%%", c.band, slabs[0].Rate.Value)
		}
		for i := 1; i < len(slabs); i++ {
			if !slabs[i].From.Equal(slabs[i-1].To) {
				t.Errorf("old regime %s: slab %d starts at %s but slab %d ended at %s",
					c.band, i, slabs[i].From, i-1, slabs[i-1].To)
			}
		}
	}

	// The new regime has NO 37% surcharge band; the old regime does. Handing the
	// old table to a new-regime employee over 5 crore over-taxes them by 12
	// percentage points of surcharge.
	newBands, err := it.SurchargeBandsFor(RegimeNew)
	if err != nil {
		t.Fatalf("SurchargeBandsFor(NEW): %v", err)
	}
	oldBands, err := it.SurchargeBandsFor(RegimeOld)
	if err != nil {
		t.Fatalf("SurchargeBandsFor(OLD): %v", err)
	}
	if len(newBands) != 3 || len(oldBands) != 4 {
		t.Fatalf("surcharge tables: new has %d bands (want 3), old has %d (want 4)", len(newBands), len(oldBands))
	}
	if !newBands[len(newBands)-1].Rate.Value.Equal(d(t, "25")) {
		t.Errorf("the new regime's top surcharge is %s%%, want 25%% — the new regime is capped and has no 37%% band",
			newBands[len(newBands)-1].Rate.Value)
	}
	if !oldBands[len(oldBands)-1].Rate.Value.Equal(d(t, "37")) {
		t.Errorf("the old regime's top surcharge is %s%%, want 37%%", oldBands[len(oldBands)-1].Rate.Value)
	}

	for _, c := range []struct {
		name, want string
		rate       Rate
	}{
		{"standard deduction (new)", "75000", it.StandardDeductionNew},
		{"standard deduction (old)", "50000", it.StandardDeductionOld},
		{"rebate limit (new)", "1200000", it.RebateLimitNew},
		{"rebate cap (new)", "60000", it.RebateMaxNew},
		{"rebate limit (old)", "500000", it.RebateLimitOld},
		{"rebate cap (old)", "12500", it.RebateMaxOld},
		{"cess %", "4", it.CessPct},
	} {
		if !c.rate.Value.Equal(d(t, c.want)) {
			t.Errorf("it-ty-2026-27 %s = %s, want %s", c.name, c.rate.Value, c.want)
		}
	}
}

// TestSlabsForRefusesAnUnknownRegimeOrBand pins the accessor that exists so no
// caller indexes OldRegimeSlabs directly. A map miss yields a nil slice, and a
// nil slab table computes a tax of ZERO with no error — a full-year
// under-deduction that looks exactly like a correctly exempt employee.
func TestSlabsForRefusesAnUnknownRegimeOrBand(t *testing.T) {
	it, err := IncomeTaxFor(TaxYear{StartYear: 2026})
	if err != nil {
		t.Fatalf("IncomeTaxFor: %v", err)
	}
	if _, err := it.SlabsFor(Regime("PRESUMPTIVE"), AgeBelow60); !errors.Is(err, ErrUnknownRegime) {
		t.Errorf("SlabsFor(PRESUMPTIVE) = %v, want ErrUnknownRegime", err)
	}
	if _, err := it.SlabsFor(RegimeOld, AgeBand("CENTENARIAN")); !errors.Is(err, ErrNoRuleSet) {
		t.Errorf("SlabsFor(OLD, CENTENARIAN) = %v, want ErrNoRuleSet; a nil slab table would compute zero tax "+
			"for that employee for the whole year", err)
	}
	if _, err := it.SurchargeBandsFor(Regime("")); !errors.Is(err, ErrUnknownRegime) {
		t.Errorf("SurchargeBandsFor(\"\") = %v, want ErrUnknownRegime", err)
	}
}

// TestCalendarValueTypes pins the two date types, whose boundaries decide which
// ruleset a pay run resolves to.
func TestCalendarValueTypes(t *testing.T) {
	m := WageMonth{Year: 2026, Month: time.February}
	if got := m.First().Format("2006-01-02"); got != "2026-02-01" {
		t.Errorf("WageMonth{2026, Feb}.First() = %s, want 2026-02-01", got)
	}
	if got := m.Last().Format("2006-01-02"); got != "2026-02-28" {
		t.Errorf("WageMonth{2026, Feb}.Last() = %s, want 2026-02-28", got)
	}
	// A leap February, because "add a month then subtract a day" is where an
	// off-by-one in Last() would hide.
	if got := (WageMonth{Year: 2028, Month: time.February}).Last().Format("2006-01-02"); got != "2028-02-29" {
		t.Errorf("WageMonth{2028, Feb}.Last() = %s, want 2028-02-29", got)
	}
	if got := (WageMonth{Year: 2026, Month: time.December}).Last().Format("2006-01-02"); got != "2026-12-31" {
		t.Errorf("WageMonth{2026, Dec}.Last() = %s, want 2026-12-31", got)
	}
	if got := m.String(); got != "2026-02" {
		t.Errorf("WageMonth.String() = %q, want 2026-02", got)
	}

	ty := TaxYear{StartYear: 2026}
	if got := ty.Start().Format("2006-01-02"); got != "2026-04-01" {
		t.Errorf("TaxYear{2026}.Start() = %s, want 2026-04-01", got)
	}
	if got := ty.End().Format("2006-01-02"); got != "2027-03-31" {
		t.Errorf("TaxYear{2026}.End() = %s, want 2027-03-31", got)
	}
	if got := ty.String(); got != "2026-27" {
		t.Errorf("TaxYear{2026}.String() = %q, want 2026-27", got)
	}
	// Both ends INCLUSIVE — the bound-flip §4.3 item 3 warns about.
	for _, c := range []struct {
		day  time.Time
		want bool
	}{
		{dateUTC(2026, time.March, 31), false},
		{dateUTC(2026, time.April, 1), true},
		{dateUTC(2027, time.March, 31), true},
		{dateUTC(2027, time.April, 1), false},
	} {
		if got := ty.Contains(c.day); got != c.want {
			t.Errorf("TaxYear{2026}.Contains(%s) = %v, want %v", c.day.Format("2006-01-02"), got, c.want)
		}
	}
	// January to March belong to the tax year that STARTED the previous calendar
	// year. Getting this wrong files a March payslip against the wrong year.
	for _, c := range []struct {
		day  time.Time
		want int
	}{
		{dateUTC(2027, time.January, 15), 2026},
		{dateUTC(2027, time.March, 31), 2026},
		{dateUTC(2027, time.April, 1), 2027},
		{dateUTC(2026, time.December, 31), 2026},
	} {
		if got := TaxYearOf(c.day); got.StartYear != c.want {
			t.Errorf("TaxYearOf(%s) = %s, want a tax year starting %d", c.day.Format("2006-01-02"), got, c.want)
		}
	}
	if got := (WageMonth{Year: 2027, Month: time.March}).TaxYear(); got.StartYear != 2026 {
		t.Errorf("WageMonth{2027, Mar}.TaxYear() = %s, want 2026-27", got)
	}
}

// packageGoFiles lists this package's .go files. withTests selects whether
// _test.go files are included.
func packageGoFiles(t *testing.T, withTests bool) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		if !withTests && strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		out = append(out, e.Name())
	}
	return out
}
