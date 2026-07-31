// WHETHER THERE IS A DUTY TO DEDUCT AT ALL — §3.1 gap 5.
//
// Design memo: docs/design/t6-gaps-model.md gap 5. Migration 0049 stores the
// facts this file reads.
//
// ---------------------------------------------------------------------------
// THE PROBLEM THIS FIXES
// ---------------------------------------------------------------------------
// TDSDerive derives a deduction for anyone who asks. But the obligation is NOT
// universal: every row of the s.393(1) Table names its payer in column C, and it
// is never "any person".
//
//	194C            Sl. No. 6(i)     "Any designated person."
//	194J-P/T/D      Sl. No. 6(iii)   "Specified person."
//	194H            Sl. No. 1(ii)    "Specified person."
//	194I-A/B        Sl. No. 2(ii)    "Specified person."
//
// A sole proprietor with ₹40 lakh of turnover is NOT required to deduct under
// any of these entries. Software that computes one, posts it and files it has
// taken money out of a supplier's payment that the supplier was entitled to
// receive — and the supplier has to reclaim it through a return.
//
// ---------------------------------------------------------------------------
// WHY THIS IS NOT A FIELD ON TDSInput
// ---------------------------------------------------------------------------
// It was tempting to make TDSDerive refuse when no status is supplied. It is the
// wrong seam, for two reasons:
//
//  1. TDSDerive answers "how much", and that arithmetic is the same whoever is
//     paying. Liability answers "at all", from facts about the PAYER that a pure
//     rate function has no other use for. Fusing them would make every corpus
//     fixture — all of which exist to pin arithmetic — carry an irrelevant
//     company status.
//
//  2. The refusal has to happen where a real posting is made, which is the
//     ledger's post transaction. That is the only place the failure is
//     reachable in production, and the only place with the company context to
//     look the status up.
//
// So: this file decides liability, the ledger calls it BEFORE TDSDerive, and
// ErrDeductorStatusUnknown is what an unrecorded company gets. A REFUSAL, never
// a zero — a zero is indistinguishable from "correctly below threshold", and it
// is the deductor who carries the liability plus interest.
package compliance

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
)

// ErrDeductorStatusUnknown is returned when a company's deductor status has not
// been recorded for the relevant tax year.
//
// It is a REFUSAL and callers must not translate it into "no deduction". The
// whole point of gap 5 is that "we do not know whether you must deduct" and
// "you need not deduct" are different answers with different consequences.
var ErrDeductorStatusUnknown = errors.New(
	"compliance: this company's deductor status is not recorded for the relevant tax year, " +
		"so whether tax must be deducted cannot be determined")

// ErrUnknownEntityCategory is returned for a category outside s.402(11)(a)–(l).
var ErrUnknownEntityCategory = errors.New("compliance: unknown deductor entity category")

// Entity categories, mapped 1:1 onto s.402(11)(a)–(l) and mirrored by migration
// 0049's company_deductor_status_category_known CHECK.
const (
	EntityGovernment           = "GOVERNMENT"            // (a)
	EntityLocalAuthority       = "LOCAL_AUTHORITY"       // (b)
	EntityStatutoryCorporation = "STATUTORY_CORPORATION" // (c)
	EntityCompany              = "COMPANY"               // (d)
	EntityCooperativeSociety   = "COOPERATIVE_SOCIETY"   // (e)
	EntityHousingAuthority     = "HOUSING_AUTHORITY"     // (f)
	EntityRegisteredSociety    = "REGISTERED_SOCIETY"    // (g)
	EntityTrust                = "TRUST"                 // (h)
	EntityUniversity           = "UNIVERSITY"            // (i)
	EntityForeign              = "FOREIGN_ENTITY"        // (j)
	EntityFirm                 = "FIRM"                  // (k)
	EntityIndividual           = "INDIVIDUAL"            // (l)
	EntityHUF                  = "HUF"                   // (l)
	EntityAOP                  = "AOP"                   // (l)
	EntityBOI                  = "BOI"                   // (l)
)

// Prior-year activity. The two carry DIFFERENT limits, so the activity is
// stored and never inferred from the turnover.
const (
	ActivityBusiness   = "BUSINESS"
	ActivityProfession = "PROFESSION"
)

// The turnover limits of s.402(11)(l)(ii) and s.402(37)(b), verbatim: "exceeding
// one crore rupees in case of business or fifty lakh rupees in case of
// profession during the tax year immediately preceding".
//
// Named with their unit, and built from string literals: a float here would be
// a money value in a binary fraction.
var (
	turnoverLimitBusinessRupees   = decimal.RequireFromString("10000000")
	turnoverLimitProfessionRupees = decimal.RequireFromString("5000000")
)

// DeductorStatus is one company's own status for one tax year — the facts of
// migration 0049's company_deductor_status row, never a verdict.
type DeductorStatus struct {
	// TaxYearStart is the calendar year the tax year begins in: 2026 = 2026-27.
	// The statute reads "the tax year IMMEDIATELY PRECEDING the tax year in
	// which such income or sum is credited or paid", so a payment in 2027-28 is
	// tested against the status row for 2026.
	TaxYearStart int
	// EntityCategory is one of the Entity* constants.
	EntityCategory string
	// PriorYearTurnoverRupees is the turnover test's operand, in rupees.
	// Meaningful only when TurnoverKnown.
	PriorYearTurnoverRupees decimal.Decimal
	// PriorYearActivity is ActivityBusiness or ActivityProfession.
	PriorYearActivity string
	// TurnoverKnown distinguishes "not applicable" from a turnover of zero.
	//
	// A company is a specified and designated person WHATEVER its turnover, so
	// its row records none — and a zero would mean "this company turned over
	// nothing", which is a different and false claim. Migration 0049's
	// company_deductor_status_turnover_pair CHECK keeps the two columns in step
	// so this flag can be trusted.
	TurnoverKnown bool
}

// meetsTurnoverTest evaluates s.402(11)(l)(ii) / s.402(37)(b).
//
// "EXCEED one crore rupees" — a turnover exactly at the limit does NOT meet the
// test. Same strict comparison as every threshold in this package.
func (st DeductorStatus) meetsTurnoverTest() (bool, error) {
	if !st.TurnoverKnown {
		// Unreachable for a row that satisfies migration 0049's
		// turnover_required_for_natural_persons CHECK, and an error rather than
		// a false because a natural person with no turnover recorded is a status
		// we cannot evaluate, not one that fails.
		return false, fmt.Errorf("%w: category %q is subject to the turnover test but no "+
			"prior-year turnover is recorded", ErrDeductorStatusUnknown, st.EntityCategory)
	}
	switch st.PriorYearActivity {
	case ActivityBusiness:
		return st.PriorYearTurnoverRupees.GreaterThan(turnoverLimitBusinessRupees), nil
	case ActivityProfession:
		return st.PriorYearTurnoverRupees.GreaterThan(turnoverLimitProfessionRupees), nil
	default:
		return false, fmt.Errorf("%w: prior-year activity %q is neither %s nor %s, and the two "+
			"carry different limits", ErrDeductorStatusUnknown, st.PriorYearActivity,
			ActivityBusiness, ActivityProfession)
	}
}

// IsSpecifiedPerson evaluates s.402(37), verbatim:
//
//	"(a) any person, NOT BEING an individual or Hindu undivided family; or
//	 (b) an individual or a Hindu undivided family, whose total sales, gross
//	 receipts or turnover ... exceed one crore rupees in case of business or
//	 fifty lakh rupees in case of profession during the tax year immediately
//	 preceding ..."
//
// Note limb (a): AOP and BOI are NOT individuals or HUFs, so they are specified
// persons UNCONDITIONALLY. This is where the definition parts company with
// "designated person" — see IsDesignatedPerson.
func IsSpecifiedPerson(st DeductorStatus) (bool, error) {
	switch st.EntityCategory {
	case EntityIndividual, EntityHUF:
		return st.meetsTurnoverTest()
	case EntityGovernment, EntityLocalAuthority, EntityStatutoryCorporation,
		EntityCompany, EntityCooperativeSociety, EntityHousingAuthority,
		EntityRegisteredSociety, EntityTrust, EntityUniversity, EntityForeign,
		EntityFirm, EntityAOP, EntityBOI:
		// Limb (a): "any person, not being an individual or HUF".
		return true, nil
	default:
		return false, fmt.Errorf("%w: %q", ErrUnknownEntityCategory, st.EntityCategory)
	}
}

// IsDesignatedPerson evaluates s.402(11), which applies ONLY to s.393(1)
// [Table: Sl. No. 6(i)] — contractor payments.
//
// Sub-clauses (a)–(k) are an explicit list of entities, all unconditional. Then:
//
//	"(l) any person, being an individual or a Hindu undivided family OR AN
//	 ASSOCIATION OF PERSONS OR A BODY OF INDIVIDUALS, if such person,— (i) does
//	 not fall under any of the preceding sub-clauses; and (ii) has total sales,
//	 gross receipts or turnover ... exceeding one crore rupees in case of
//	 business or fifty lakh rupees in case of profession during the tax year
//	 immediately preceding ..."
//
// ⚠️ AOP AND BOI ARE IN (l), so under THIS definition they take the turnover
// test — whereas s.402(37)(a) catches them unconditionally. An AOP with ₹40 lakh
// of turnover must therefore deduct on professional fees and must NOT deduct on
// contractor payments. One boolean cannot express that, which is why migration
// 0049 stores the category rather than a verdict.
func IsDesignatedPerson(st DeductorStatus) (bool, error) {
	switch st.EntityCategory {
	case EntityIndividual, EntityHUF, EntityAOP, EntityBOI:
		return st.meetsTurnoverTest()
	case EntityGovernment, EntityLocalAuthority, EntityStatutoryCorporation,
		EntityCompany, EntityCooperativeSociety, EntityHousingAuthority,
		EntityRegisteredSociety, EntityTrust, EntityUniversity, EntityForeign,
		EntityFirm:
		return true, nil
	default:
		return false, fmt.Errorf("%w: %q", ErrUnknownEntityCategory, st.EntityCategory)
	}
}

// payerTest records which of the two definitions a section's column C names.
//
// A TABLE, NOT A DEFAULT. An unmapped section is an error, because "we do not
// know which payer test applies" must not silently become "the common one" —
// that is how a new section quietly acquires the wrong liability rule.
var payerTest = map[string]string{
	"194C":   "DESIGNATED", // Sl. No. 6(i):   "Any designated person."
	"194J-P": "SPECIFIED",  // Sl. No. 6(iii): "Specified person."
	"194J-T": "SPECIFIED",  // Sl. No. 6(iii)
	"194J-D": "SPECIFIED",  // Sl. No. 6(iii)
	"194H":   "SPECIFIED",  // Sl. No. 1(ii)
	"194I-A": "SPECIFIED",  // Sl. No. 2(ii)
	"194I-B": "SPECIFIED",  // Sl. No. 2(ii)
}

// MustDeduct reports whether this company is required to deduct under a section.
//
// It REFUSES — ErrDeductorStatusUnknown, ErrUnknownEntityCategory or
// ErrUnknownSection — rather than answering false, in every case where the
// answer is not known. Callers must not collapse an error into "no deduction".
func MustDeduct(section string, st DeductorStatus) (bool, error) {
	test, ok := payerTest[section]
	if !ok {
		return false, fmt.Errorf("%w: %q has no payer test; which persons must deduct under it "+
			"is not recorded", ErrUnknownSection, section)
	}
	if test == "DESIGNATED" {
		return IsDesignatedPerson(st)
	}
	return IsSpecifiedPerson(st)
}
