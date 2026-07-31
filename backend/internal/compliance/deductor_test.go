package compliance

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"
)

func status(cat string, turnover string, activity string) DeductorStatus {
	st := DeductorStatus{TaxYearStart: 2026, EntityCategory: cat}
	if turnover != "" {
		st.PriorYearTurnoverRupees = decimal.RequireFromString(turnover)
		st.PriorYearActivity = activity
		st.TurnoverKnown = true
	}
	return st
}

// TestAOPIsSpecifiedButNotAlwaysDesignated is THE test for §3.1 gap 5, and it is
// the one a single "is this company liable?" boolean cannot pass.
//
// s.402(37)(a) makes "any person, not being an individual or Hindu undivided
// family" a specified person UNCONDITIONALLY — which catches an AOP. But
// s.402(11)(l) lists "an individual or a Hindu undivided family OR AN
// ASSOCIATION OF PERSONS OR A BODY OF INDIVIDUALS" and subjects all four to the
// turnover test.
//
// So one AOP, one turnover, two opposite answers depending on the section.
func TestAOPIsSpecifiedButNotAlwaysDesignated(t *testing.T) {
	// ₹40 lakh of business turnover: well under the ₹1 crore limit.
	smallAOP := status(EntityAOP, "4000000", ActivityBusiness)

	spec, err := IsSpecifiedPerson(smallAOP)
	if err != nil {
		t.Fatalf("IsSpecifiedPerson: %v", err)
	}
	if !spec {
		t.Errorf("an AOP is a SPECIFIED person unconditionally under s.402(37)(a) — it is not " +
			"an individual or HUF, so its turnover is irrelevant. Applying the turnover test " +
			"here means professional fees, commission and rent go undeducted")
	}

	desig, err := IsDesignatedPerson(smallAOP)
	if err != nil {
		t.Fatalf("IsDesignatedPerson: %v", err)
	}
	if desig {
		t.Errorf("an AOP under the turnover limit is NOT a designated person: s.402(11)(l) " +
			"names AOP and BOI explicitly and applies the test to them. Treating it as " +
			"designated deducts on contractor payments the statute does not reach")
	}

	// The same fact through the section-routing entry point, which is what the
	// ledger will actually call.
	profFees, err := MustDeduct("194J-P", smallAOP)
	if err != nil {
		t.Fatalf("MustDeduct 194J-P: %v", err)
	}
	contractor, err := MustDeduct("194C", smallAOP)
	if err != nil {
		t.Fatalf("MustDeduct 194C: %v", err)
	}
	if !profFees || contractor {
		t.Errorf("MustDeduct for a small AOP = (194J-P %v, 194C %v), want (true, false). "+
			"If these two agree, the engine is using ONE liability test for both column-C "+
			"definitions and one of the two sections is wrong", profFees, contractor)
	}

	// CONTROL: a BOI must behave identically, since it sits in the same limb.
	smallBOI := status(EntityBOI, "4000000", ActivityBusiness)
	bSpec, _ := IsSpecifiedPerson(smallBOI)
	bDesig, _ := IsDesignatedPerson(smallBOI)
	if !bSpec || bDesig {
		t.Errorf("a BOI under the limit = (specified %v, designated %v), want (true, false): "+
			"s.402(11)(l) names BOI alongside AOP", bSpec, bDesig)
	}
}

// TestTheSoleProprietorWhoNeedNotDeduct is the case the handoff calls the
// biggest product finding on this track.
func TestTheSoleProprietorWhoNeedNotDeduct(t *testing.T) {
	small := status(EntityIndividual, "4000000", ActivityBusiness) // ₹40 lakh

	for _, section := range []string{"194C", "194J-P", "194J-T", "194J-D", "194H", "194I-A", "194I-B"} {
		must, err := MustDeduct(section, small)
		if err != nil {
			t.Fatalf("MustDeduct %s: %v", section, err)
		}
		if must {
			t.Errorf("a sole proprietor with ₹40 lakh of business turnover is required to deduct "+
				"under %s. It is not: it is neither a specified nor a designated person, and "+
				"deducting takes money out of a supplier's payment they were entitled to receive",
				section)
		}
	}

	// CONTROL: the same individual over the limit IS liable. Without this, the
	// test above would pass for an engine that always answers false.
	big := status(EntityIndividual, "20000000", ActivityBusiness) // ₹2 crore
	for _, section := range []string{"194C", "194J-P"} {
		must, err := MustDeduct(section, big)
		if err != nil {
			t.Fatalf("MustDeduct %s: %v", section, err)
		}
		if !must {
			t.Errorf("an individual with ₹2 crore of business turnover MUST deduct under %s; "+
				"the turnover test is not being applied at all", section)
		}
	}
}

// TestTurnoverLimitsAreStrictAndActivityDependent pins both limits and the
// "exceed" comparison.
func TestTurnoverLimitsAreStrictAndActivityDependent(t *testing.T) {
	for _, tc := range []struct {
		name, turnover, activity string
		want                     bool
	}{
		// s.402(37)(b): "EXCEED one crore rupees in case of business".
		{"business exactly ₹1 crore", "10000000", ActivityBusiness, false},
		{"business a paisa over", "10000000.0001", ActivityBusiness, true},
		{"business ₹2 crore", "20000000", ActivityBusiness, true},
		// "...or fifty lakh rupees in case of profession".
		{"profession exactly ₹50 lakh", "5000000", ActivityProfession, false},
		{"profession a paisa over", "5000000.0001", ActivityProfession, true},
		// THE ONE THAT CATCHES A SINGLE SHARED LIMIT: ₹60 lakh is under the
		// business limit and over the profession one. An engine using one number
		// gets exactly one of these two rows wrong.
		{"₹60 lakh of BUSINESS", "6000000", ActivityBusiness, false},
		{"₹60 lakh of PROFESSION", "6000000", ActivityProfession, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := IsSpecifiedPerson(status(EntityIndividual, tc.turnover, tc.activity))
			if err != nil {
				t.Fatalf("IsSpecifiedPerson: %v", err)
			}
			if got != tc.want {
				t.Errorf("%s: liable = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// TestEntitiesThatAreLiableWhateverTheirTurnover pins limb (a) of s.402(37) and
// sub-clauses (a)–(k) of s.402(11).
func TestEntitiesThatAreLiableWhateverTheirTurnover(t *testing.T) {
	unconditional := []string{
		EntityGovernment, EntityLocalAuthority, EntityStatutoryCorporation,
		EntityCompany, EntityCooperativeSociety, EntityHousingAuthority,
		EntityRegisteredSociety, EntityTrust, EntityUniversity, EntityForeign,
		EntityFirm,
	}
	for _, cat := range unconditional {
		// No turnover recorded at all — the point being that none is needed.
		st := status(cat, "", "")
		for _, section := range []string{"194C", "194J-P"} {
			must, err := MustDeduct(section, st)
			if err != nil {
				t.Errorf("%s under %s: %v — an entity that is liable whatever its turnover must "+
					"not need one recorded", cat, section, err)
				continue
			}
			if !must {
				t.Errorf("%s is not required to deduct under %s. It is: s.402(37)(a) covers "+
					"every person that is not an individual or HUF, and s.402(11)(a)-(k) lists "+
					"it explicitly", cat, section)
			}
		}
	}
}

// TestUnknownStatusRefusesRatherThanReturningFalse is the invariant that makes
// gap 5 safe rather than merely modelled.
//
// A zero deduction and an unknown liability look identical on a voucher. The
// error is the only thing that distinguishes them, and a caller that collapses
// it into "no deduction" reintroduces the whole bug.
func TestUnknownStatusRefusesRatherThanReturningFalse(t *testing.T) {
	// A natural person with no turnover recorded: the status row is incomplete.
	incomplete := DeductorStatus{TaxYearStart: 2026, EntityCategory: EntityIndividual}
	if _, err := MustDeduct("194C", incomplete); !errors.Is(err, ErrDeductorStatusUnknown) {
		t.Errorf("an individual with no turnover recorded returned %v, want "+
			"ErrDeductorStatusUnknown. Answering false here silently exempts a company that may "+
			"well be liable", err)
	}

	// An activity that is neither BUSINESS nor PROFESSION: the limit to compare
	// against is unknown, so the answer is unknown.
	badActivity := DeductorStatus{
		TaxYearStart: 2026, EntityCategory: EntityIndividual,
		PriorYearTurnoverRupees: decimal.RequireFromString("20000000"),
		PriorYearActivity:       "TRADE", TurnoverKnown: true,
	}
	if _, err := MustDeduct("194C", badActivity); !errors.Is(err, ErrDeductorStatusUnknown) {
		t.Errorf("an unrecognised prior-year activity returned %v, want ErrDeductorStatusUnknown: "+
			"the ₹1 crore and ₹50 lakh limits differ and picking one would be a guess", err)
	}

	// An unknown category.
	if _, err := MustDeduct("194C", status("PARTNERSHIP_LLP", "", "")); !errors.Is(err, ErrUnknownEntityCategory) {
		t.Errorf("an unknown entity category returned %v, want ErrUnknownEntityCategory", err)
	}

	// An unknown section: which payer test applies is not recorded.
	if _, err := MustDeduct("194Q", status(EntityCompany, "", "")); !errors.Is(err, ErrUnknownSection) {
		t.Errorf("an unmapped section returned %v, want ErrUnknownSection: defaulting to the "+
			"common payer test is how a new section acquires the wrong liability rule", err)
	}
}

// TestEveryShippedSectionHasAPayerTest keeps payerTest in step with the ruleset.
//
// A section in the rate table with no payer test is one whose liability question
// has never been asked — and MustDeduct would refuse it at runtime, on a real
// voucher, rather than here.
func TestEveryShippedSectionHasAPayerTest(t *testing.T) {
	rs, err := LoadTDSRuleSet("tds-2026-04")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for name := range rs.Sections {
		if _, ok := payerTest[name]; !ok {
			t.Errorf("section %q is in the ruleset but payerTest does not say WHO must deduct "+
				"under it. Column C of the s.393(1) Table names either a 'designated person' "+
				"or a 'specified person' for every row", name)
		}
	}
}
