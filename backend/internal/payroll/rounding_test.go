package payroll

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"testing"

	"github.com/shopspring/decimal"
)

func d(t *testing.T, s string) decimal.Decimal {
	t.Helper()
	v, err := decimal.NewFromString(s)
	if err != nil {
		t.Fatalf("bad decimal literal %q in the test itself: %v", s, err)
	}
	return v
}

// TestRoundPaise_MatchesNumeric194 pins the ordinary money rounding against the
// NUMERIC(19,4) columns the values land in.
//
// The consequence if this drifts: a gross-pay figure computed at more than four
// decimal places is quantized by POSTGRES on the way in, using half-away-from-
// zero. If the engine rounded differently (truncation, or banker's rounding),
// the number in the payslip PDF and the number in the database would disagree by
// a paisa on some employees, and the pay run's debit and credit legs — computed
// in Go, stored in Postgres — would stop balancing.
func TestRoundPaise_MatchesNumeric194(t *testing.T) {
	cases := []struct {
		in, want string
		why      string
	}{
		{"1.00005", "1.0001", "half away from zero at the 5th dp, matching Postgres numeric"},
		{"1.00004", "1.0000", "below half rounds down"},
		{"0.00005", "0.0001", "the smallest possible round-up must not vanish"},
		{"0.00004", "0.0000", "sub-half at the smallest scale rounds to nothing"},
		{"84317.5000", "84317.5000", "an already-4dp value is untouched"},
		{"-1.00005", "-1.0001", "negatives round away from zero too (Postgres does the same)"},
		{"12345.678949", "12345.6789", "a long tail truncates to 4dp on the low side"},
		{"12345.678951", "12345.6790", "and rounds up on the high side"},
	}
	for _, c := range cases {
		got := RoundPaise(d(t, c.in))
		if !got.Equal(d(t, c.want)) {
			t.Errorf("RoundPaise(%s) = %s, want %s — %s; a payslip figure and its stored NUMERIC(19,4) would disagree",
				c.in, got, c.want, c.why)
		}
		if got.Exponent() < -4 {
			t.Errorf("RoundPaise(%s) = %s has exponent %d: more than 4 dp survived, so Postgres would round it a SECOND time",
				c.in, got, got.Exponent())
		}
	}
}

// TestRoundNearestRupee_StatutoryHalfUpAndSignBehaviour pins s.288B / s.516 and,
// just as importantly, pins the DIVERGENCE from the statutory rule for negative
// values so that nobody discovers it by shipping it.
//
// The consequence if the half-up half drifts: every employee's monthly TDS and
// every EPF/EPS component moves by a rupee in some months. The ECR carries EPF
// and EPS as whole-rupee integers and the portal re-adds them, so a rupee out is
// a rejected challan, not a rounding nicety.
func TestRoundNearestRupee_StatutoryHalfUpAndSignBehaviour(t *testing.T) {
	for _, c := range []struct{ in, want, why string }{
		{"100.50", "101", "s.288B: exactly half rounds UP"},
		{"100.49", "100", "just under half rounds down"},
		{"100.51", "101", "just over half rounds up"},
		{"0.50", "1", "half a rupee of tax is still a rupee of tax"},
		{"0.49", "0", "under half a rupee is nil"},
		{"1250.00", "1250", "the EPS cap is already whole and must not move"},
		{"1799.9999", "1800", "a paisa short of a rupee rounds to the rupee"},
	} {
		got := RoundNearestRupee(d(t, c.in))
		if !got.Equal(d(t, c.want)) {
			t.Errorf("RoundNearestRupee(%s) = %s, want %s — %s", c.in, got, c.want, c.why)
		}
		if got.Exponent() < 0 {
			t.Errorf("RoundNearestRupee(%s) = %s still carries decimals; the ECR field is an integer", c.in, got)
		}
	}

	// THE DIVERGENCE. shopspring rounds HALF AWAY FROM ZERO. The statutory rule
	// is half UP. For -100.50 those differ: away-from-zero gives -101, half-up
	// gives -100. This test asserts the ACTUAL behaviour rather than the
	// statutory one, because pretending the statutory rule is implemented for
	// negatives is how a negative statutory amount gets quietly accepted.
	if got := RoundNearestRupee(d(t, "-100.50")); !got.Equal(d(t, "-101")) {
		t.Fatalf("RoundNearestRupee(-100.50) = %s, want -101 (half AWAY FROM ZERO). "+
			"If this now returns -100 the implementation changed to half-up, and the doc comment "+
			"plus every caller's negative guard must be revisited", got)
	}
	if RoundNearestRupee(d(t, "-100.50")).Equal(RoundNearestRupee(d(t, "-100.49"))) {
		t.Error("the negative divergence case is vacuous: -100.50 and -100.49 round the same, " +
			"so this test would not notice a change of rounding mode")
	}

	// AND THEREFORE: a caller that must never see a negative statutory amount
	// REFUSES rather than rounding one under a rule that does not cover it.
	// StatutoryWages is that caller in this slice.
	_, err := StatutoryWages(WageInput{
		Month: WageMonth{Year: 2026, Month: 8},
		Components: []WageComponent{
			{Type: ComponentBasic, Amount: d(t, "30000")},
			{Type: ComponentOtherAllowance, Amount: d(t, "-100.50")},
		},
	}, ss202607)
	if !errors.Is(err, ErrNegativeStatutoryAmount) {
		t.Fatalf("StatutoryWages with a negative component: err = %v, want ErrNegativeStatutoryAmount. "+
			"Without this refusal a recovery entered as a negative earning would be rounded under a "+
			"rule written for non-negative amounts and would silently reduce PF and ESI", err)
	}
}

// TestRoundUpRupee_BothESISharesCeil pins rule 51's "rounded to the next higher
// rupee" AND the judgement that it applies to BOTH shares.
//
// The consequence if the ceiling becomes a nearest-rupee: every ESI
// contribution where the fraction is below half drops by a rupee, the monthly
// challan under-pays, and ESIC raises a shortfall notice against the employer.
// The consequence if only the employee share is ceiled: the employer share is
// short on every employee, every month.
func TestRoundUpRupee_BothESISharesCeil(t *testing.T) {
	for _, c := range []struct{ in, want, why string }{
		{"60.00", "60", "an exact rupee does not gain one"},
		{"60.0075", "61", "any fraction at all goes to the next higher rupee"},
		{"60.0001", "61", "even a hundredth of a paisa"},
		{"0.0001", "1", "and a near-zero contribution is still a rupee"},
		{"0", "0", "zero stays zero — an uncovered employee owes nothing"},
		{"260.0325", "261", "the employer share ceils exactly as the employee share does"},
	} {
		got := RoundUpRupee(d(t, c.in))
		if !got.Equal(d(t, c.want)) {
			t.Errorf("RoundUpRupee(%s) = %s, want %s — %s", c.in, got, c.want, c.why)
		}
	}

	// The two shares of one wage, ceiled independently. Wage 8,001:
	//   employee 0.75% = 60.0075 -> 61
	//   employer 3.25% = 260.0325 -> 261
	// Both fractions are far below half, so a nearest-rupee rounding would give
	// 60 and 260 — this is the case that separates the two rules.
	wage := d(t, "8001")
	employee := RoundUpRupee(wage.Mul(d(t, "0.75")).Shift(-2))
	employer := RoundUpRupee(wage.Mul(d(t, "3.25")).Shift(-2))
	if !employee.Equal(d(t, "61")) || !employer.Equal(d(t, "261")) {
		t.Fatalf("ESI on a wage of 8001: employee = %s (want 61), employer = %s (want 261). "+
			"Both shares must CEIL per rule 51's own drafting; rounding to nearest would give 60 and 260 "+
			"and the monthly challan would be 2 rupees short for this employee alone", employee, employer)
	}
	if RoundNearestRupee(wage.Mul(d(t, "0.75")).Shift(-2)).Equal(employee) {
		t.Error("the ESI case is vacuous: nearest-rupee and ceiling agree on it, so this test " +
			"would not notice RoundUpRupee being replaced by RoundNearestRupee")
	}
}

// TestRoundNearestTen_StatutoryExamples pins s.288A / s.516 — TOTAL INCOME
// rounds to the nearest multiple of TEN rupees, not to the nearest rupee.
//
// The consequence if this becomes a nearest-rupee rounding: the slab arithmetic
// runs on a different total income for every employee whose income does not
// already end in a zero, and near the s.156 rebate cliff at 12,00,000 it changes
// whether the rebate applies at all — which is a difference of tens of thousands
// of rupees of annual tax for one employee.
func TestRoundNearestTen_StatutoryExamples(t *testing.T) {
	for _, c := range []struct{ in, want, why string }{
		{"1234565", "1234570", "last figure 5 goes UP to the next multiple of ten"},
		{"1234564", "1234560", "last figure below 5 goes down"},
		{"1234", "1230", "a plain rupee figure still moves — this is what a nearest-RUPEE rounding would leave alone"},
		{"1236", "1240", "and up when the units digit is 6"},
		{"100005", "100010", "the boundary case at a round lakh plus five"},
		{"499999", "500000", "a rupee under 5 lakh rounds to the old-regime rebate limit exactly"},
		{"1200004", "1200000", "just over the new-regime rebate limit rounds back TO the limit"},
		{"1200005", "1200010", "and five rupees over rounds AWAY from it, past the cliff"},
		{"1234560", "1234560", "an exact multiple of ten is untouched"},
		{"0", "0", "zero income rounds to zero"},
	} {
		got := RoundNearestTen(d(t, c.in))
		if !got.Equal(d(t, c.want)) {
			t.Errorf("RoundNearestTen(%s) = %s, want %s — %s", c.in, got, c.want, c.why)
		}
		if !got.Mod(decimal.NewFromInt(10)).IsZero() {
			t.Errorf("RoundNearestTen(%s) = %s is not a multiple of ten; s.288A requires one", c.in, got)
		}
	}

	// The whole point of this function is that it is NOT RoundNearestRupee.
	// If a mutation makes it d.Round(0), every one of these must diverge, and at
	// least one case above must already have caught it — this assertion states
	// the requirement directly so the failure message names the consequence.
	for _, in := range []string{"1234", "1236", "1234565", "1200004"} {
		if RoundNearestTen(d(t, in)).Equal(RoundNearestRupee(d(t, in))) {
			t.Errorf("RoundNearestTen(%s) agrees with RoundNearestRupee(%s): the TOTAL-INCOME rounding "+
				"has collapsed into the TAX rounding. s.288A rounds income to ten rupees and s.288B rounds "+
				"tax to one; running the slabs on the wrong base changes the annual tax for every employee "+
				"whose income does not end in a zero", in, in)
		}
	}

	// Sign behaviour, stated so it is not discovered later: half away from zero.
	if got := RoundNearestTen(d(t, "-1234565")); !got.Equal(d(t, "-1234570")) {
		t.Errorf("RoundNearestTen(-1234565) = %s, want -1234570 (half away from zero, as documented)", got)
	}
}

// ---------------------------------------------------------------------------
// THE IDENTITY GATE
// ---------------------------------------------------------------------------
// TestRoundingHelpersAreTheOnlyRounders (purity_test.go) pins WHERE rounding
// may live: only rounding.go may call decimal's own Round/Ceil/Truncate. It says
// NOTHING about WHICH of the four named helpers a call site chose, and that gap
// is not theoretical — an adversarial pass replaced RoundNearestRupee with
// RoundNearestTen on the employee's statutory EPF deduction and the ENTIRE
// track stayed green: every unit test, every corpus fixture, and the independent
// Python oracle. Ten of the twelve rounding call sites in pf.go were blind.
//
// A location gate cannot catch that, because the mutation moves nothing: it
// stays inside a named helper, in the file the location gate already allows. The
// two roundings differ only in their UNIT, and the numbers every fixture had
// been written with happened to be multiples of ten, so both rounders agreed.
//
// So this test pins IDENTITY. Each source file declares the rounder(s) its
// statute permits, and the declaration is exact in BOTH directions:
//
//   - a call to a rounder the file is not permitted FAILS. pf.go may not reach
//     for the s.288A nearest-TEN rounding, which belongs to TOTAL INCOME alone;
//     esi.go may not reach for anything but rule 51's ceiling; nothing outside
//     incometax.go may round to ten at all.
//   - a PERMISSION THE FILE DOES NOT USE also fails. Without that half the
//     policy could be widened to "pf.go may use any rounder" and the gate would
//     go quiet — which is precisely how a gate stops gating. The allowed set is
//     therefore forced to equal the used set, so reading it tells you exactly
//     which statutory rounding each file performs.
//
// WHAT THIS DOES NOT CATCH, stated so nobody assumes otherwise: swapping two
// call sites WITHIN one file, e.g. rounding the EPS diversion with the rounder
// that belongs on the EPF share. pf.go legitimately uses one rounder at twelve
// sites, so identity-per-file cannot separate them. That is what the per-site
// mutation sweep behind TestPFDerive_EveryRoundingSiteIsObservable exists for,
// and the two tests are complementary rather than overlapping.
var roundersPermittedPerFile = map[string][]string{
	// rounding.go DEFINES the four helpers and calls none of them. Its own
	// decimal.Round/Ceil calls are the ones purity_test.go exempts.
	"rounding.go": {},

	// Types, vocabularies and errors. No arithmetic at all.
	"payroll.go": {},
	// The rate registry. Rates are DATA; rounding a rate would silently
	// re-price every contribution derived from it.
	"rates.go": {},

	// wages.go produces ordinary money — gross, included, excluded, the deemed
	// add-back, the statutory wage — all of which land in NUMERIC(19,4). It
	// must NOT round to the rupee: the statutory wage is the BASE that pf.go
	// and esi.go then round, and rounding it here would be a first rounding
	// that moves every downstream answer.
	"wages.go": {"RoundPaise"},

	// pf.go: s.288B / s.516 nearest RUPEE, which is also what the EPFO expects
	// for EPF, EPS and the ECR's integer wage columns. NOT nearest ten (that is
	// total income), NOT a ceiling (that is ESI rule 51), NOT paise (the ECR
	// has no paisa field and a paisa in a remittance is a rejected challan).
	"pf.go": {"RoundNearestRupee"},

	// esi.go: rule 51's "rounded to the next higher rupee", on BOTH shares.
	// Nothing else. A nearest-rupee here under-pays the challan on every
	// employee whose fraction is below half and ESIC raises a shortfall.
	"esi.go": {"RoundUpRupee"},

	// incometax.go is the ONLY file that legitimately uses three, because the
	// statute prescribes three at three different points: s.516 (ex-288A)
	// rounds TOTAL INCOME to the nearest TEN, s.516 (ex-288B) rounds the TAX
	// and the monthly TDS to the nearest RUPEE, and the intermediate
	// disclosures echoed on the result are ordinary NUMERIC(19,4) money.
	"incometax.go": {"RoundNearestTen", "RoundNearestRupee", "RoundPaise"},

	// payrun.go assembles the payslip: deductions, employer cost, net pay. Each
	// is a sum of already-rounded statutory components plus ordinary money, so
	// paise is the only correct scale. Rounding net pay to the rupee here would
	// quietly change what lands in the employee's bank account.
	"payrun.go": {"RoundPaise"},
}

// allRounders is the complete set of named helpers rounding.go exports. It is
// written out rather than derived so that ADDING a fifth rounder forces a
// decision about which files may use it.
var allRounders = []string{"RoundPaise", "RoundNearestRupee", "RoundUpRupee", "RoundNearestTen"}

// TestEachFileCallsOnlyTheRounderItsStatutePermits is the identity half of the
// rounding gate. See roundersPermittedPerFile for why it exists.
func TestEachFileCallsOnlyTheRounderItsStatutePermits(t *testing.T) {
	known := map[string]bool{}
	for _, r := range allRounders {
		known[r] = true
	}

	files := packageGoFiles(t, false)
	if len(files) < 4 {
		t.Fatalf("scanned %d non-test files; the identity scan is vacuous", len(files))
	}

	usedByRounder := map[string]int{}
	totalCalls := 0

	for _, name := range files {
		allowed, declared := roundersPermittedPerFile[name]
		if !declared {
			// A NEW FILE IS A FAILURE, not a default-allow. A file with no policy
			// entry could round however it liked and this gate would say nothing,
			// which is exactly the hole that let the nearest-ten mutation through.
			t.Errorf("%s has no entry in roundersPermittedPerFile. Every non-test file in this package "+
				"must declare which statutory rounding it is permitted to perform (use an empty list for a "+
				"file that does no arithmetic); a file with no policy is a file this gate does not gate", name)
			continue
		}
		allowedSet := map[string]bool{}
		for _, r := range allowed {
			if !known[r] {
				t.Errorf("roundersPermittedPerFile[%q] names %q, which is not one of the helpers in rounding.go %v. "+
					"A typo here silently grants nothing and forbids everything", name, r, allRounders)
			}
			allowedSet[r] = true
		}

		found := roundersCalledIn(t, name, known)
		for r, n := range found {
			totalCalls += n
			usedByRounder[r] += n
			if !allowedSet[r] {
				t.Errorf("%s calls %s() %d time(s), which its statute does not permit — permitted here: %v. "+
					"The four helpers implement FOUR DIFFERENT STATUTORY RULES with THREE DIFFERENT UNITS "+
					"(paise, whole rupee, next higher rupee, nearest ten). Substituting one for another does not "+
					"move any code, does not trip the location gate in purity_test.go, and changes a real "+
					"person's pay: nearest-ten on the employee's EPF share turns 12%% of a 12,345 wage from "+
					"1,481 into 1,480, every month, silently, in the ECR's integer column",
					name, r, n, allowed)
			}
		}
		for _, r := range allowed {
			if found[r] == 0 {
				t.Errorf("roundersPermittedPerFile[%q] permits %s() but %s never calls it. An UNUSED permission "+
					"is how this gate stops gating: widen every entry to all four helpers and the test goes "+
					"quiet forever. The allowed set must equal the used set, so that reading it tells you "+
					"exactly which statutory rounding the file performs", name, r, name)
			}
		}
	}

	// --- non-vacuity 1: the walk must actually be finding calls ---------------
	if totalCalls < 15 {
		t.Fatalf("the identity walk found only %d rounder calls across the package; there are more than that "+
			"in pf.go alone. The AST walk is broken and every assertion above is passing on an empty set", totalCalls)
	}
	// --- non-vacuity 2: every shipped rounder must be used somewhere ----------
	// A helper nobody calls is either dead code or a rule that has quietly
	// stopped being applied. RoundNearestTen is the one that matters: it is used
	// at exactly ONE site in the whole codebase, and if it ever falls to zero the
	// total-income rounding has been replaced by the tax rounding.
	for _, r := range allRounders {
		if usedByRounder[r] == 0 {
			t.Errorf("%s() is declared in rounding.go but called nowhere in the package. Either the statutory "+
				"rule it implements has stopped being applied, or it is dead code that will be reached for by "+
				"somebody who does not know which statute it belongs to", r)
		}
	}
	// --- non-vacuity 3: a POSITIVE CONTROL for the detector -------------------
	// Without this, a bug in roundersCalledIn (a wrong node type, a missed
	// *ast.Ident) would leave every file reporting "no forbidden calls" and the
	// whole gate would pass forever on nothing. The control is a source file that
	// is deliberately in breach.
	const inBreach = `package payroll
func breach(d decimal.Decimal) decimal.Decimal {
	_ = RoundNearestTen(d)
	_ = RoundUpRupee(d)
	return RoundNearestTen(RoundPaise(d))
}`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "synthetic-breach.go", inBreach, 0)
	if err != nil {
		t.Fatalf("parse the synthetic breach: %v", err)
	}
	control := map[string]int{}
	collectRounderCalls(f, known, control)
	if control["RoundNearestTen"] != 2 || control["RoundUpRupee"] != 1 || control["RoundPaise"] != 1 {
		t.Fatalf("the detector scored the synthetic breach as %v, want RoundNearestTen=2 RoundUpRupee=1 RoundPaise=1. "+
			"It is not seeing rounder calls, so every per-file assertion above is vacuous", control)
	}
	if control["RoundNearestRupee"] != 0 {
		t.Fatalf("the detector reported RoundNearestRupee in a source that does not call it: %v", control)
	}

	t.Logf("rounder identity pinned across %d files, %d calls: %v", len(files), totalCalls, sortedCounts(usedByRounder))
}

// roundersCalledIn parses one file of this package and counts the calls to each
// of the named rounding helpers.
//
// It matches *ast.Ident and not *ast.SelectorExpr because the helpers are
// package-local functions called unqualified. A FuncDecl NAME is not a CallExpr,
// so rounding.go's own declarations are not counted — which is what lets
// rounding.go declare an empty permitted set.
func roundersCalledIn(t *testing.T, name string, known map[string]bool) map[string]int {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	out := map[string]int{}
	collectRounderCalls(f, known, out)
	return out
}

func collectRounderCalls(f *ast.File, known map[string]bool, into map[string]int) {
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); ok && known[id.Name] {
			into[id.Name]++
		}
		return true
	})
}

func sortedCounts(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+strconv.Itoa(v))
	}
	sort.Strings(out)
	return out
}
