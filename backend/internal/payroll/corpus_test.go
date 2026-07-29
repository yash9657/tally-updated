package payroll

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// The golden-corpus gate for the payroll engine.
//
// ---------------------------------------------------------------------------
// WHY THIS IS NOT A TAUTOLOGY, AND WHAT WOULD MAKE IT ONE
// ---------------------------------------------------------------------------
// corpora/payroll/*.json is checked by TWO derivations that never meet each
// other. This test compares THE ENGINE's output to `expected`. corpora/
// validate.py compares ITS OWN, independently written, statute-derived
// arithmetic to the same `expected`. Neither ever reads the other's answer and
// neither ever reads the other's source. The fixture is the only meeting point,
// so a number that is wrong on one side fails on that side alone.
//
// The three ways this degrades into a metric that always passes, and the
// mechanism that stops each:
//
//  1. A fixture regenerated FROM the engine. Nothing in the code can stop that,
//     but validate.py's independent re-derivation will disagree the moment the
//     engine is wrong, and the mutation proof in the commit message shows both
//     sides failing independently.
//  2. A fixture that no longer demonstrates its claim, because the correct rule
//     and the naive rule have converged on the numbers chosen. FIVE fixtures
//     carry a `counterfactual` block that validate.py re-derives under the NAIVE
//     rule and fails if it AGREES with expected. That is the inventory family's
//     backdatedInward trick, which is the only thing that has ever caught this.
//  3. A rate or a ruleset shipped with no fixture at all. The CLOSURE checks at
//     the bottom of TestPayrollDerive_GoldenCorpus fail the build for an uncited
//     ruleset version, an unexercised slab band, or an unverified rate whose
//     refusal no fixture asserts.
//
// A FOURTH failure mode is subtler and is handled by comparing DECIMALS rather
// than STRINGS: validate.py's money() is a MAX-4-dp check, so "1234" passes it,
// while a Go StringFixed(4) rendering is "1234.0000" and a string comparison
// would call those unequal. Every comparison below is decimal.Equal.

// payrollCorporaDir locates <repo>/corpora/payroll by walking up from the
// package directory.
//
// It is a COPY of corporaSubdir in internal/compliance/tds_test.go, which is
// package-private there and cannot be imported. The t.Fatalf on failure is the
// load-bearing part: a copy that swallowed the lookup error and returned "" would
// make the whole corpus test read zero files and pass silently, which is exactly
// the shape of gate that a previous session shipped and an adversarial pass had
// to find.
func payrollCorporaDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 8; i++ {
		candidate := filepath.Join(dir, "corpora", "payroll")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		dir = filepath.Dir(dir)
	}
	t.Fatalf("could not locate corpora/payroll above %q — the corpus gate would otherwise read zero files and pass", dir)
	return ""
}

// payrollCorpusMinimum is the acceptance floor. It is deliberately BELOW the 14
// fixtures shipped, so two could be retired in a reviewed change — but not the
// twelve behaviours listed in MIN_COUNTS["payroll"] in corpora/validate.py.
const payrollCorpusMinimum = 12

type payrollCase struct {
	ID             string          `json:"id"`
	Description    string          `json:"description"`
	RulesetVersion string          `json:"rulesetVersion"`
	Input          json.RawMessage `json:"input"`
	Expected       json.RawMessage `json:"expected"`
	Counterfactual *struct {
		Rule string `json:"rule"`
		Why  string `json:"why"`
	} `json:"counterfactual"`
}

type wagesCaseInput struct {
	WageMonth  string `json:"wageMonth"`
	Components []struct {
		Type   string `json:"type"`
		Amount string `json:"amount"`
	} `json:"components"`
}

type wagesCaseExpected struct {
	TotalRemuneration  string `json:"totalRemuneration"`
	IncludedComponents string `json:"includedComponents"`
	ExcludedComponents string `json:"excludedComponents"`
	// ExcludedComponentsInDeemingTest is the clause (a)-(i) subset — the only
	// sum proviso 1 to s.2(88) measures against the one-half floor.
	ExcludedComponentsInDeemingTest string `json:"excludedComponentsInDeemingTest"`
	DeemingThresholdPct             string `json:"deemingThresholdPct"`
	DeemedAddBack                   string `json:"deemedAddBack"`
	StatutoryWage                   string `json:"statutoryWage"`
}

type pfCaseInput struct {
	WageMonth                string  `json:"wageMonth"`
	StatutoryWage            string  `json:"statutoryWage"`
	PFBase                   string  `json:"pfBase"`
	EmployeeVoluntaryBase    string  `json:"employeeVoluntaryBase"`
	EmployerMatchesVoluntary bool    `json:"employerMatchesVoluntary"`
	EPSMember                bool    `json:"epsMember"`
	FirstMemberOn            *string `json:"firstMemberOn"`
}

type pfCaseExpected struct {
	EPFWages                string   `json:"epfWages"`
	EPSWages                string   `json:"epsWages"`
	EDLIWages               string   `json:"edliWages"`
	EmployeeStatutory       string   `json:"employeeStatutory"`
	EmployeeVoluntary       string   `json:"employeeVoluntary"`
	EmployerStatutory       string   `json:"employerStatutory"`
	EmployerVoluntary       string   `json:"employerVoluntary"`
	EPS                     string   `json:"eps"`
	EmployeeEPF             string   `json:"employeeEPF"`
	EmployerEPF             string   `json:"employerEPF"`
	EPSMemberApplied        bool     `json:"epsMemberApplied"`
	EDLIContributionRefused bool     `json:"edliContributionRefused"`
	DeferredComponents      []string `json:"deferredComponents"`
}

type esiCaseInput struct {
	WageMonth string  `json:"wageMonth"`
	JoinedOn  *string `json:"joinedOn"`
	LeftOn    *string `json:"leftOn"`
	Revisions []struct {
		EffectiveFrom string `json:"effectiveFrom"`
		MonthlyWages  string `json:"monthlyWages"`
	} `json:"revisions"`
	MonthWages string `json:"monthWages"`
	PaidDays   int    `json:"paidDays"`
}

type esiCaseExpected struct {
	Covered       bool   `json:"covered"`
	Applied       bool   `json:"applied"`
	WagesTestedOn string `json:"wagesTestedOn"`
	WagesTested   string `json:"wagesTested"`
	LowWageExempt bool   `json:"lowWageExempt"`
	EmployeeShare string `json:"employeeShare"`
	EmployerShare string `json:"employerShare"`
}

type itCaseInput struct {
	TaxYearStart              string `json:"taxYearStart"`
	Regime                    string `json:"regime"`
	IsResident                bool   `json:"isResident"`
	AgeBand                   string `json:"ageBand"`
	PANAvailable              bool   `json:"panAvailable"`
	PriorEmployerTaxable      string `json:"priorEmployerTaxable"`
	PriorEmployerTDS          string `json:"priorEmployerTDS"`
	YTDTaxableThisEmployer    string `json:"ytdTaxableThisEmployer"`
	YTDTDSThisEmployer        string `json:"ytdTDSThisEmployer"`
	CurrentMonthTaxable       string `json:"currentMonthTaxable"`
	ProjectedRemainingTaxable string `json:"projectedRemainingTaxable"`
	MonthsRemainingInclusive  int    `json:"monthsRemainingInclusive"`
	IsFinalMonth              bool   `json:"isFinalMonth"`
	HousePropertyLoss         string `json:"housePropertyLoss"`
	ChapterVIA                string `json:"chapterVIA"`
	OtherIncomeDeclared       string `json:"otherIncomeDeclared"`
}

type itCaseExpected struct {
	RegimeApplied           string `json:"regimeApplied"`
	EstimatedAnnualTaxable  string `json:"estimatedAnnualTaxable"`
	StandardDeduction       string `json:"standardDeduction"`
	TaxBeforeRebate         string `json:"taxBeforeRebate"`
	Rebate                  string `json:"rebate"`
	RebateMarginalRelief    string `json:"rebateMarginalRelief"`
	Surcharge               string `json:"surcharge"`
	SurchargeMarginalRelief string `json:"surchargeMarginalRelief"`
	Cess                    string `json:"cess"`
	AnnualTax               string `json:"annualTax"`
	CreditedAlready         string `json:"creditedAlready"`
	TDSThisMonth            string `json:"tdsThisMonth"`
	PeriodBasis             string `json:"periodBasis"`
}

func corpusDec(t *testing.T, field, s string) decimal.Decimal {
	t.Helper()
	v, err := decimal.NewFromString(s)
	if err != nil {
		t.Fatalf("%s: %q is not a decimal: %v", field, s, err)
	}
	return v
}

func corpusDate(t *testing.T, field, s string) time.Time {
	t.Helper()
	v, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("%s: %q is not a YYYY-MM-DD date: %v", field, s, err)
	}
	return v
}

func corpusMonth(t *testing.T, field, s string) WageMonth {
	t.Helper()
	v, err := time.Parse("2006-01", s)
	if err != nil {
		t.Fatalf("%s: %q is not a YYYY-MM wage month: %v", field, s, err)
	}
	return WageMonth{Year: v.Year(), Month: v.Month()}
}

// corpusEqual compares DECIMALS, never strings. validate.py's money() is a
// MAX-4-dp check, so a fixture may legitimately carry "1234" where the engine
// renders "1234.0000"; a string comparison would fail a correct fixture and the
// cheapest apparent fix would be to edit the golden value.
func corpusEqual(t *testing.T, field string, got decimal.Decimal, want string, consequence string) {
	t.Helper()
	w := corpusDec(t, field, want)
	if !got.Equal(w) {
		t.Errorf("%s: got %s, want %s. %s", field, got.StringFixed(4), w.StringFixed(4), consequence)
	}
}

func TestPayrollDerive_GoldenCorpus(t *testing.T) {
	dir := payrollCorporaDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read corpora dir: %v", err)
	}

	count := 0
	citedRuleSets := map[string]bool{}
	// exercisedSlabRates[regime][rate string] — for the closure check that every
	// shipped band is demonstrated by at least one fixture.
	exercisedSlabRates := map[Regime]map[string]bool{
		RegimeNew: {},
		RegimeOld: {},
	}
	// deferredByFixture is the union of every deferral a PF fixture DECLARES, per
	// ruleset. It is compared against what the engine actually defers.
	deferredByFixture := map[string]map[string]bool{}
	sawRefusalFixture := false

	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var c payrollCase
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatalf("unmarshal %s: %v", path, err)
		}
		count++
		citedRuleSets[c.RulesetVersion] = true

		t.Run(c.ID, func(t *testing.T) {
			if c.ID != strings.TrimSuffix(e.Name(), ".json") {
				t.Fatalf("id %q does not match the filename %q", c.ID, e.Name())
			}
			// A payroll fixture is simultaneously a LEGAL CLAIM and a COMPUTATION.
			// Without the citation the rate is unreviewable; without the hand
			// derivation the number is.
			if len(c.Description) < 40 {
				t.Errorf("description too short to carry both the citation and the derivation: %q", c.Description)
			}

			switch {
			case strings.HasPrefix(c.ID, "wages-"):
				runWagesCase(t, c)
			case strings.HasPrefix(c.ID, "pf-"):
				exp := runPFCase(t, c)
				m := deferredByFixture[c.RulesetVersion]
				if m == nil {
					m = map[string]bool{}
					deferredByFixture[c.RulesetVersion] = m
				}
				for _, d := range exp.DeferredComponents {
					m[d] = true
				}
				if exp.EDLIContributionRefused {
					sawRefusalFixture = true
				}
			case strings.HasPrefix(c.ID, "esi-"):
				runESICase(t, c)
			case strings.HasPrefix(c.ID, "it-"):
				runIncomeTaxCase(t, c, exercisedSlabRates)
			default:
				t.Fatalf("id %q has no known prefix (wages-, pf-, esi-, it-); it would be silently skipped", c.ID)
			}
		})
	}

	if count == 0 {
		t.Fatal("no payroll corpus cases were read — the gate is vacuous")
	}
	if count < payrollCorpusMinimum {
		t.Fatalf("only %d payroll corpus cases; the contract requires at least %d. "+
			"A fixture must not be deleted to make a red gate green", count, payrollCorpusMinimum)
	}

	// --- CLOSURE 1: every shipped ruleset must be cited by a fixture ----------
	// A ruleset nobody cites is a body of law nobody has ever checked, sitting in
	// the resolver waiting for a back-dated pay run to pick it up.
	for _, w := range socialSecurityWindows {
		if !citedRuleSets[w.Version] {
			t.Errorf("social-security ruleset %q is shipped but no corpus fixture cites it; "+
				"a back-dated pay run would resolve to rates nobody has ever checked", w.Version)
		}
	}
	for _, w := range incomeTaxWindows {
		if !citedRuleSets[w.Version] {
			t.Errorf("income-tax ruleset %q is shipped but no corpus fixture cites it", w.Version)
		}
	}

	// --- CLOSURE 2: every slab band must be exercised -------------------------
	// A band with no fixture is a rate nobody has checked against a citation.
	// Bands are matched by RATE, not by index: the old regime's tables differ
	// only in the basic exemption (rates.go builds all three from one list), so
	// one age band's fixtures demonstrate every rate in that regime.
	it, err := IncomeTaxFor(TaxYear{StartYear: 2026})
	if err != nil {
		t.Fatalf("load it-ty-2026-27: %v", err)
	}
	for regime, slabs := range map[Regime][]Slab{
		RegimeNew: it.NewRegimeSlabs,
		RegimeOld: it.OldRegimeSlabs[AgeBelow60],
	} {
		for _, s := range slabs {
			r := s.Rate.Value.String()
			if !exercisedSlabRates[regime][r] {
				t.Errorf("%s-regime slab band [%s,%s) at %s%% is shipped but no corpus fixture reaches into it",
					regime, s.From, s.To, r)
			}
		}
	}

	// --- CLOSURE 3: every unverified rate must be refused, with a fixture -----
	// Two halves, and both are needed. The first is mechanical: reflect over every
	// shipped ruleset and assert that a rate marked ConfUnverified cannot be
	// consumed. The second is behavioural: the engine's actual deferral set must
	// be exactly what the fixtures declare, so adding an unverified rate that
	// produces a NEW deferral fails the build until a fixture names it.
	unverified := 0
	for _, w := range socialSecurityWindows {
		unverified += assertUnverifiedRefuse(t, "socialSecurityWindows["+w.Version+"]", reflect.ValueOf(w.Set))
	}
	for _, w := range incomeTaxWindows {
		unverified += assertUnverifiedRefuse(t, "incomeTaxWindows["+w.Version+"]", reflect.ValueOf(w.Set))
	}
	if unverified == 0 {
		t.Error("no ConfUnverified rate was found in any shipped ruleset. Either every rate has been " +
			"verified against a primary text — in which case say so in the provenance banner — or the " +
			"reflect walk has stopped seeing them and this closure check is vacuous")
	}
	if !sawRefusalFixture {
		t.Error("no corpus fixture asserts that an unverified rate is REFUSED (edliContributionRefused). " +
			"Without one, an engine that invented the EDLI rate would pass every other fixture")
	}
	for version, declared := range deferredByFixture {
		rs, err := socialSecurityByVersion(version)
		if err != nil {
			t.Fatalf("%v", err)
		}
		got := engineDeferrals(t, rs)
		if !reflect.DeepEqual(sortedSet(declared), sortedSet(got)) {
			t.Errorf("ruleset %q: the engine defers %v but the fixtures declare %v. "+
				"A deferral with no fixture is a component nobody has decided about; a fixture with no "+
				"deferral is a number the engine is now inventing",
				version, sortedSet(got), sortedSet(declared))
		}
	}

	t.Logf("validated %d payroll corpus cases across %d rulesets", count, len(citedRuleSets))
}

func runWagesCase(t *testing.T, c payrollCase) {
	t.Helper()
	var in wagesCaseInput
	var exp wagesCaseExpected
	mustUnmarshal(t, c.Input, &in)
	mustUnmarshal(t, c.Expected, &exp)

	month := corpusMonth(t, "input.wageMonth", in.WageMonth)
	rs := resolveSocialSecurity(t, c, month)

	wi := WageInput{Month: month}
	for i, comp := range in.Components {
		ct, err := ComponentTypeFor(comp.Type)
		if err != nil {
			t.Fatalf("input.components[%d].type: %v", i, err)
		}
		wi.Components = append(wi.Components, WageComponent{
			Type:   ct,
			Amount: corpusDec(t, "input.components[].amount", comp.Amount),
		})
	}
	got, err := StatutoryWages(wi, rs)
	if err != nil {
		t.Fatalf("StatutoryWages: %v", err)
	}
	corpusEqual(t, "totalRemuneration", got.TotalRemuneration, exp.TotalRemuneration, "gross earnings would be wrong on the payslip")
	corpusEqual(t, "includedComponents", got.IncludedComponents, exp.IncludedComponents, "the wage classification has drifted from s.2(88)")
	corpusEqual(t, "excludedComponents", got.ExcludedComponents, exp.ExcludedComponents, "the wage classification has drifted from s.2(88)")
	corpusEqual(t, "excludedComponentsInDeemingTest", got.ExcludedComponentsInDeemingTest, exp.ExcludedComponentsInDeemingTest,
		"the deeming test is being run over the wrong clause set — clause (j) gratuity and clause (k) retrenchment "+
			"compensation are outside it, and deeming them back in takes PF and ESI out of a leaver's final salary")
	corpusEqual(t, "deemingThresholdPct", got.DeemingThresholdPct, exp.DeemingThresholdPct, "the wrong ruleset's deeming threshold was applied")
	corpusEqual(t, "deemedAddBack", got.DeemedAddBack, exp.DeemedAddBack, "the 50% proviso is mis-applied; every PF and ESI figure downstream is wrong")
	corpusEqual(t, "statutoryWage", got.StatutoryWage, exp.StatutoryWage,
		"this is the base for PF, EPS, EDLI and the ECR — an error here is systematic across the whole workforce")
	if got.PeriodBasis != BasisMonthly {
		t.Errorf("periodBasis: got %q, want MONTHLY", got.PeriodBasis)
	}
}

func runPFCase(t *testing.T, c payrollCase) pfCaseExpected {
	t.Helper()
	var in pfCaseInput
	var exp pfCaseExpected
	mustUnmarshal(t, c.Input, &in)
	mustUnmarshal(t, c.Expected, &exp)

	month := corpusMonth(t, "input.wageMonth", in.WageMonth)
	rs := resolveSocialSecurity(t, c, month)

	base, err := PFBaseFor(in.PFBase)
	if err != nil {
		t.Fatalf("input.pfBase: %v", err)
	}
	pi := PFInput{
		Month:                    month,
		StatutoryWage:            corpusDec(t, "input.statutoryWage", in.StatutoryWage),
		Base:                     base,
		EmployeeVoluntaryBase:    corpusDec(t, "input.employeeVoluntaryBase", in.EmployeeVoluntaryBase),
		EmployerMatchesVoluntary: in.EmployerMatchesVoluntary,
		EPSMember:                in.EPSMember,
	}
	if in.FirstMemberOn != nil {
		pi.FirstMemberOn = corpusDate(t, "input.firstMemberOn", *in.FirstMemberOn)
	}
	got, err := PFDerive(pi, rs)
	if err != nil {
		t.Fatalf("PFDerive: %v", err)
	}

	corpusEqual(t, "epfWages", got.EPFWages, exp.EPFWages, "the ECR's EPF Wages column would be wrong")
	corpusEqual(t, "epsWages", got.EPSWages, exp.EPSWages, "EPS wages are a DIFFERENT number from EPF wages and have their own ECR column")
	corpusEqual(t, "edliWages", got.EDLIWages, exp.EDLIWages, "EDLI wages are a THIRD number and have their own ECR column")
	corpusEqual(t, "eps", got.EPS, exp.EPS, "a wrong pension diversion is money the member cannot get back")
	corpusEqual(t, "employeeEPF", got.EmployeeEPF, exp.EmployeeEPF, "this is deducted from the employee's pay")
	corpusEqual(t, "employerEPF", got.EmployerEPF, exp.EmployerEPF, "this is remitted by the employer")
	if got.EPSMemberApplied != exp.EPSMemberApplied {
		t.Errorf("epsMemberApplied: got %v, want %v. The para 6(a) exclusion decides whether 8.33%% is "+
			"diverted into a pension the member may never be able to draw", got.EPSMemberApplied, exp.EPSMemberApplied)
	}

	componentEqual(t, got.ComponentByName, ComponentEPFEmployee, exp.EmployeeStatutory, "the employee's statutory share")
	componentEqual(t, got.ComponentByName, ComponentEPFEmployer, exp.EmployerStatutory, "the employer's statutory share")
	componentOrZero(t, got.ComponentByName, ComponentEPFEmployeeVoluntary, exp.EmployeeVoluntary, "VPF")
	componentOrZero(t, got.ComponentByName, ComponentEPFEmployerVoluntary, exp.EmployerVoluntary, "the employer's voluntary match")

	// The deferral set must match exactly. ABSENCE IS MEANINGFUL: a deferred
	// component has no row at all, which is NOT the same as a component that is
	// legitimately zero, and a caller that read "not found" as 0.00 would have
	// re-invented the number the deferral exists to refuse.
	gotDeferred := map[string]bool{}
	for _, d := range got.Deferrals {
		gotDeferred[d.Component] = true
		if _, ok := got.ComponentByName(d.Component); ok {
			t.Errorf("%s is BOTH deferred and carries a component amount; one of them is an invented number", d.Component)
		}
		if len(d.Citation) < 40 {
			t.Errorf("deferral %s carries a citation too short to be a reference: %q", d.Component, d.Citation)
		}
	}
	wantDeferred := map[string]bool{}
	for _, d := range exp.DeferredComponents {
		wantDeferred[d] = true
	}
	if !reflect.DeepEqual(sortedSet(gotDeferred), sortedSet(wantDeferred)) {
		t.Errorf("deferrals: engine defers %v, fixture declares %v", sortedSet(gotDeferred), sortedSet(wantDeferred))
	}
	if exp.EDLIContributionRefused {
		if _, err := rs.EDLIContributionPct.Use(); !errors.Is(err, ErrUnverifiedRate) {
			t.Errorf("the fixture asserts the EDLI contribution is REFUSED, but the rate is consumable: %v. "+
				"Shipping the superseded 1976 Scheme's 0.50%% would be inventing a rate and charging an employer for it", err)
		}
	}
	return exp
}

func runESICase(t *testing.T, c payrollCase) {
	t.Helper()
	var in esiCaseInput
	var exp esiCaseExpected
	mustUnmarshal(t, c.Input, &in)
	mustUnmarshal(t, c.Expected, &exp)

	month := corpusMonth(t, "input.wageMonth", in.WageMonth)
	rs := resolveSocialSecurity(t, c, month)

	ci := CoverageInput{Month: month}
	if in.JoinedOn != nil {
		ci.JoinedOn = corpusDate(t, "input.joinedOn", *in.JoinedOn)
	}
	if in.LeftOn != nil {
		ci.LeftOn = corpusDate(t, "input.leftOn", *in.LeftOn)
	}
	for i, r := range in.Revisions {
		ci.Revisions = append(ci.Revisions, WageRevision{
			EffectiveFrom: corpusDate(t, "input.revisions[].effectiveFrom", r.EffectiveFrom),
			MonthlyWages:  corpusDec(t, "input.revisions[].monthlyWages", r.MonthlyWages),
		})
		_ = i
	}
	cov, err := ESICoverage(ci, rs)
	if err != nil {
		t.Fatalf("ESICoverage: %v", err)
	}
	if cov.Covered != exp.Covered {
		t.Errorf("covered: got %v, want %v. Coverage LATCHES at the contribution-period start; "+
			"re-testing it monthly drops an employee out mid-treatment and under-remits", cov.Covered, exp.Covered)
	}
	if got := cov.WagesTestedOn.Format("2006-01-02"); got != exp.WagesTestedOn {
		t.Errorf("wagesTestedOn: got %s, want %s. The test INSTANT is what makes the latch auditable — "+
			"a coverage decision taken on this month's wage is the monthly test wearing the latch's name",
			got, exp.WagesTestedOn)
	}
	corpusEqual(t, "wagesTested", cov.WagesTested, exp.WagesTested, "the ceiling was applied to the wrong wage")

	esi, err := ESIDerive(ESIInput{
		Month:    month,
		Covered:  cov.Covered,
		Wages:    corpusDec(t, "input.monthWages", in.MonthWages),
		PaidDays: in.PaidDays,
	}, rs)
	if err != nil {
		t.Fatalf("ESIDerive: %v", err)
	}
	if esi.Applied != exp.Applied {
		t.Errorf("applied: got %v, want %v", esi.Applied, exp.Applied)
	}
	if esi.LowWageExempt != exp.LowWageExempt {
		t.Errorf("lowWageExempt: got %v, want %v. Rule 52 exempts the EMPLOYEE's share only; "+
			"zeroing both under-remits and zeroing neither short-pays the employee", esi.LowWageExempt, exp.LowWageExempt)
	}
	corpusEqual(t, "employeeShare", esi.EmployeeShare, exp.EmployeeShare, "this is deducted from the employee's pay")
	corpusEqual(t, "employerShare", esi.EmployerShare, exp.EmployerShare, "rule 51 rounds BOTH shares to the next higher rupee")

	// A case that does not apply must derive exactly zero, never a stale amount.
	if !esi.Applied && (!esi.EmployeeShare.IsZero() || !esi.EmployerShare.IsZero()) {
		t.Errorf("not covered but derived employee %s / employer %s", esi.EmployeeShare, esi.EmployerShare)
	}
}

func runIncomeTaxCase(t *testing.T, c payrollCase, exercised map[Regime]map[string]bool) {
	t.Helper()
	var in itCaseInput
	var exp itCaseExpected
	mustUnmarshal(t, c.Input, &in)
	mustUnmarshal(t, c.Expected, &exp)

	start := corpusDate(t, "input.taxYearStart", in.TaxYearStart)
	ty := TaxYearOf(start)
	rs, err := IncomeTaxFor(ty)
	if err != nil {
		t.Fatalf("IncomeTaxFor(%s): %v", ty, err)
	}
	if rs.Version != c.RulesetVersion {
		t.Fatalf("tax year %s resolves to ruleset %q but the fixture cites %q — the effective-dating "+
			"and the fixture disagree about which law applies", ty, rs.Version, c.RulesetVersion)
	}
	regime, err := RegimeFor(in.Regime)
	if err != nil {
		t.Fatalf("input.regime: %v", err)
	}

	got, err := IncomeTaxDerive(IncomeTaxInput{
		TaxYear:                   ty,
		Regime:                    regime,
		IsResident:                in.IsResident,
		AgeBand:                   AgeBand(in.AgeBand),
		PANAvailable:              in.PANAvailable,
		PriorEmployerTaxable:      corpusDec(t, "input.priorEmployerTaxable", in.PriorEmployerTaxable),
		PriorEmployerTDS:          corpusDec(t, "input.priorEmployerTDS", in.PriorEmployerTDS),
		YTDTaxableThisEmployer:    corpusDec(t, "input.ytdTaxableThisEmployer", in.YTDTaxableThisEmployer),
		YTDTDSThisEmployer:        corpusDec(t, "input.ytdTDSThisEmployer", in.YTDTDSThisEmployer),
		CurrentMonthTaxable:       corpusDec(t, "input.currentMonthTaxable", in.CurrentMonthTaxable),
		ProjectedRemainingTaxable: corpusDec(t, "input.projectedRemainingTaxable", in.ProjectedRemainingTaxable),
		MonthsRemainingInclusive:  in.MonthsRemainingInclusive,
		IsFinalMonth:              in.IsFinalMonth,
		HousePropertyLoss:         corpusDec(t, "input.housePropertyLoss", in.HousePropertyLoss),
		ChapterVIA:                corpusDec(t, "input.chapterVIA", in.ChapterVIA),
		OtherIncomeDeclared:       corpusDec(t, "input.otherIncomeDeclared", in.OtherIncomeDeclared),
	}, rs)
	if err != nil {
		t.Fatalf("IncomeTaxDerive: %v", err)
	}

	if string(got.RegimeApplied) != exp.RegimeApplied {
		t.Errorf("regimeApplied: got %q, want %q", got.RegimeApplied, exp.RegimeApplied)
	}
	if got.RuleSetVersion != c.RulesetVersion {
		t.Errorf("ruleSetVersion: got %q, want %q", got.RuleSetVersion, c.RulesetVersion)
	}
	if string(got.PeriodBasis) != exp.PeriodBasis || got.PeriodBasis != BasisAnnual {
		t.Errorf("periodBasis: got %q, want ANNUAL. Salary tax is annual by statute; anything else here "+
			"means a per-transaction threshold has crept in", got.PeriodBasis)
	}
	corpusEqual(t, "estimatedAnnualTaxable", got.EstimatedAnnualTaxable, exp.EstimatedAnnualTaxable,
		"total income rounds to the nearest TEN (s.516, ex-288A); rounding it to the rupee shifts the slab arithmetic")
	corpusEqual(t, "standardDeduction", got.StandardDeduction, exp.StandardDeduction, "the two regimes have different standard deductions")
	corpusEqual(t, "taxBeforeRebate", got.TaxBeforeRebate, exp.TaxBeforeRebate, "the slab walk is wrong")
	corpusEqual(t, "rebate", got.Rebate, exp.Rebate, "the s.156 rebate is wrong")
	corpusEqual(t, "rebateMarginalRelief", got.RebateMarginalRelief, exp.RebateMarginalRelief,
		"WITHOUT the s.156 rebate marginal relief every employee between 12,00,000 and about 12,70,589 is over-taxed")
	corpusEqual(t, "surcharge", got.Surcharge, exp.Surcharge, "the surcharge band is wrong")
	corpusEqual(t, "surchargeMarginalRelief", got.SurchargeMarginalRelief, exp.SurchargeMarginalRelief,
		"without surcharge marginal relief an employee just over a threshold pays more tax than the raise was worth")
	corpusEqual(t, "cess", got.Cess, exp.Cess, "the cess base is (tax after rebate + surcharge after relief)")
	corpusEqual(t, "annualTax", got.AnnualTax, exp.AnnualTax, "the year's tax is wrong")
	corpusEqual(t, "creditedAlready", got.CreditedAlready, exp.CreditedAlready,
		"prior-employer TDS plus this employer's YTD; dropping either double-deducts or under-deducts")
	corpusEqual(t, "tdsThisMonth", got.TDSThisMonth, exp.TDSThisMonth, "this is the money taken out of the employee's salary this month")

	// Record which slab bands this fixture reached into, for the closure check.
	slabs, err := rs.SlabsFor(regime, AgeBand(in.AgeBand))
	if err != nil {
		t.Fatalf("SlabsFor: %v", err)
	}
	for _, s := range slabs {
		if got.EstimatedAnnualTaxable.GreaterThan(s.From) {
			exercised[regime][s.Rate.Value.String()] = true
		}
	}
}

// resolveSocialSecurity resolves the ruleset for a wage month and asserts it is
// the one the fixture cites.
//
// Resolving rather than looking up by version is the point: it proves the
// EFFECTIVE-DATING agrees with the fixture's own claim about which law applied.
// A fixture for May 2020 that resolved to the 2026 ruleset would otherwise pass
// with 2026 rates and a 2019 citation.
func resolveSocialSecurity(t *testing.T, c payrollCase, m WageMonth) SocialSecurityRuleSet {
	t.Helper()
	rs, err := SocialSecurityForMonth(m)
	if err != nil {
		t.Fatalf("SocialSecurityForMonth(%s): %v", m, err)
	}
	if rs.Version != c.RulesetVersion {
		t.Fatalf("wage month %s resolves to ruleset %q but the fixture cites %q — the effective-dating "+
			"and the fixture disagree about which law applied", m, rs.Version, c.RulesetVersion)
	}
	return rs
}

func socialSecurityByVersion(version string) (SocialSecurityRuleSet, error) {
	for _, w := range socialSecurityWindows {
		if w.Version == version {
			return w.Set, nil
		}
	}
	return SocialSecurityRuleSet{}, errors.New("no social-security window with version " + version)
}

// engineDeferrals runs a canonical PF derivation under a ruleset and reports
// which components it refused to compute.
func engineDeferrals(t *testing.T, rs SocialSecurityRuleSet) map[string]bool {
	t.Helper()
	res, err := PFDerive(PFInput{
		Month:         WageMonth{Year: 2026, Month: time.August},
		StatutoryWage: decimal.RequireFromString("15000"),
		Base:          PFBaseCeiling,
		EPSMember:     true,
	}, rs)
	if err != nil {
		t.Fatalf("PFDerive under %q: %v", rs.Version, err)
	}
	out := map[string]bool{}
	for _, d := range res.Deferrals {
		out[d.Component] = true
	}
	return out
}

// assertUnverifiedRefuse reflect-walks a ruleset and asserts that every Rate
// marked ConfUnverified refuses to be consumed. Returns how many it found.
//
// The reflect walk is what makes this survive a field being added: a new
// unverified rate is covered the moment it is declared, with no test to update.
func assertUnverifiedRefuse(t *testing.T, path string, v reflect.Value) int {
	t.Helper()
	found := 0
	rateType := reflect.TypeOf(Rate{})
	var walk func(string, reflect.Value)
	walk = func(p string, v reflect.Value) {
		switch v.Kind() {
		case reflect.Struct:
			if v.Type() == rateType {
				r, _ := v.Interface().(Rate)
				if r.Confidence == ConfUnverified {
					found++
					if _, err := r.Use(); !errors.Is(err, ErrUnverifiedRate) {
						t.Errorf("%s is ConfUnverified but Use() returned %v instead of ErrUnverifiedRate. "+
							"An unverified rate that can be consumed is a number this build invented", p, err)
					}
				}
				return
			}
			for i := 0; i < v.NumField(); i++ {
				if v.Type().Field(i).PkgPath != "" {
					continue // unexported
				}
				walk(p+"."+v.Type().Field(i).Name, v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(p+"[]", v.Index(i))
			}
		case reflect.Map:
			for _, k := range v.MapKeys() {
				walk(p+"["+k.String()+"]", v.MapIndex(k))
			}
		}
	}
	walk(path, v)
	return found
}

func componentEqual(t *testing.T, lookup func(string) (StatutoryComponent, bool), name, want, what string) {
	t.Helper()
	c, ok := lookup(name)
	if !ok {
		t.Errorf("component %s (%s) is missing from the derivation", name, what)
		return
	}
	corpusEqual(t, "component "+name, c.Amount, want, what+" is wrong")
}

// componentOrZero handles the components that exist ONLY when there is an
// election. An always-present zero row would be indistinguishable in Form V from
// a voluntary contribution that was agreed and then computed as zero, so the
// engine omits it — and the fixture declares 0.0000 for that case.
func componentOrZero(t *testing.T, lookup func(string) (StatutoryComponent, bool), name, want, what string) {
	t.Helper()
	c, ok := lookup(name)
	amount := decimal.Zero
	if ok {
		amount = c.Amount
	}
	w := corpusDec(t, "expected."+name, want)
	if ok && w.IsZero() {
		t.Errorf("component %s (%s) is present with %s but the fixture declares zero; a zero voluntary row "+
			"is not the same fact as no voluntary election", name, what, c.Amount)
		return
	}
	if !amount.Equal(w) {
		t.Errorf("component %s (%s): got %s, want %s", name, what, amount.StringFixed(4), w.StringFixed(4))
	}
}

func mustUnmarshal(t *testing.T, raw json.RawMessage, into any) {
	t.Helper()
	if len(raw) == 0 {
		t.Fatal("fixture section is empty")
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("unmarshal fixture section: %v", err)
	}
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		if v {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
