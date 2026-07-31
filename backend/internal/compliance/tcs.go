// TCS (Tax Collected at Source) derivation — the collection-side mirror of
// tds.go. Same purity contract: no I/O, safe inside the post transaction.
//
// ---------------------------------------------------------------------------
// RATE PROVENANCE — VERIFIED AGAINST THE AMENDED ACT
// ---------------------------------------------------------------------------
// Ruleset "tcs-2026-04" was originally curated offline from model knowledge. It
// has since been read against PRIMARY TEXT:
// docs/statutory/ita-2025-as-amended-fa-2026.pdf — the Income-tax Act 2025 as
// amended by the Finance Act 2026, s.394(1) Table "TAX COLLECTION AT SOURCE"
// and s.397(2). See docs/design/t6-gaps-model.md §0.
//
// THAT RE-READING FOUND A WRONG RATE. Scrap was shipped at 1%; s.394(1) Table
// Sl. No. 4 carries footnote 88, "Substituted for '1%' by the Finance Act,
// 2026, w.e.f. 1-4-2026", making it 2%. The engine under-collected by half on
// every scrap sale from 1 April 2026 until this correction.
//
// It is corrected IN PLACE rather than behind a new ruleset version: the
// version string names the table in force for a period, "tcs-2026-04" claims
// April 2026 onward, and 2% is what was in force across the whole of that
// period. Two values for one period would be the bug, not the fix.
//
// An earlier verification pass (docs/design/t6-model.md §9.1) marked this row
// correct because it read the Act AS ENACTED, before the Finance Act 2026
// amendments. When re-checking a rate, check the amended text.
//
// ---------------------------------------------------------------------------
// THREE THINGS THAT ARE NOT A MIRROR OF THE TDS SIDE — do not "simplify" them
// ---------------------------------------------------------------------------
//  1. s.206C(1H) (TCS on sale of goods above 50 lakh, 0.1%) is DELIBERATELY
//     ABSENT. It was omitted w.e.f. 1-Apr-2025 by Finance Act 2025 (the buyer's
//     s.194Q TDS obligation supersedes it). Shipping it would make the engine
//     collect a tax that no longer exists. CONFIRMED: there is no entry for
//     sale of goods anywhere in the s.394 Table of the amended Act.
//  2. The no-PAN rule for collection is s.397(2)(b)(ii) (ex-s.206CC), NOT the
//     deduction-side s.397(2)(b)(i) (ex-s.206AA). It is "higher of TWICE the
//     rate in force, or 5%" — NOT a flat 20% floor. Reusing the TDS no-PAN
//     constant here would be WRONG. Modelled as
//     max(NoPANMultiplier * rate, NoPANFloorPct) with a dedicated fixture.
//  3. ...but that maximum is CEILINGED. s.397(2)(b)(ii) reads "at the higher of
//     the following rates, NOT EXCEEDING 20%". The TDS side has a floor with no
//     ceiling; the TCS side has both. Latent for the rates we ship (2% doubles
//     to 4%, so the 5% floor wins), but s.394's own Table has a 20% row at
//     Sl. No. 7(b) where doubling would yield 40% against a statutory maximum
//     of 20%. NoPANCeilingPct exists so that is impossible rather than pending.
package compliance

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
)

// TCSInput is the complete, self-contained input to TCSDerive. Unlike TDS, no
// in-force TCS section here splits its rate by collectee type, so there is no
// DeducteeType field (adding one would imply a distinction that does not exist).
type TCSInput struct {
	// Section is the collection key, e.g. "206C(1)-SCRAP".
	Section string
	// PANAvailable reports whether the collectee's PAN is on record. When false,
	// s.206CC applies (see the package note).
	PANAvailable bool
	// TaxableAmount is the sale consideration the collection applies to.
	TaxableAmount decimal.Decimal
}

// TCSResult is the derived collection. Money fields are quantized to 4 dp.
type TCSResult struct {
	// TCS is the amount to collect; zero when Applied is false.
	TCS decimal.Decimal
	// RatePct is the rate actually applied, AFTER any s.206CC override.
	RatePct decimal.Decimal
	// Threshold is the limit that was tested.
	Threshold decimal.Decimal
	// Basis records how the threshold is statutorily measured.
	Basis ThresholdBasis
	// Applied is false when the amount did not exceed the threshold.
	Applied bool
}

// tcsSectionRule is one collection row of a TCSRuleSet.
type tcsSectionRule struct {
	RatePct   decimal.Decimal
	Threshold decimal.Decimal
	Basis     ThresholdBasis
}

// TCSRuleSet is the effective-dated TCS behaviour for a version.
type TCSRuleSet struct {
	// Version is the ruleset identifier, e.g. "tcs-2026-04".
	Version string
	// Sections maps a collection key to its rule.
	Sections map[string]tcsSectionRule
	// NoPANMultiplier / NoPANFloorPct implement s.397(2)(b)(ii): when no PAN is
	// on record the rate is the HIGHER of (multiplier * rate in force, floor).
	NoPANMultiplier decimal.Decimal
	NoPANFloorPct   decimal.Decimal
	// NoPANCeilingPct is the same clause's "not exceeding 20%" — the upper bound
	// on the no-PAN result. It applies ONLY to the no-PAN computation, never to
	// a section's own rate: s.394(1) Table Sl. No. 7(b) is itself 20% and a
	// PAN-bearing collectee pays exactly that.
	NoPANCeilingPct decimal.Decimal
}

// tcsRegistry holds the known, effective-dated TCS rulesets keyed by version.
// Separate from both `registry` (GST) and `tdsRegistry`.
//
// Section keys and citations. The internal keys still use the REPEALED 1961-Act
// nomenclature while the statute and the return key off s.394(1) Table Sl. Nos.
// The migration of these names is deliberately held for T6 so names and return
// codes move in one reviewable step (PLAN-TALLY-PARITY.md open item 10).
//
//	206C(1)-SCRAP  sale of scrap                 2%, no per-transaction
//	                                             exemption limit (threshold 0)
//	                                             — s.394(1) [Table: Sl. No. 4],
//	                                             2% w.e.f. 1-4-2026 (fn. 88).
//	                                             Return code 1073.
//	206C(1F)-MV    sale consideration exceeding  1%, 10,00,000 — s.394(1)
//	               ten lakh rupees, motor        [Table: Sl. No. 6.D(a)].
//	               vehicle                       Unchanged by FA 2026.
//	                                             Return code 1075.
//
// s.397(2)(b)(ii) no-PAN: higher of 2x the rate in force or 5%, NOT EXCEEDING
// 20%.
//
// NOT SHIPPED (deliberate): 206C(1H) — omitted w.e.f. 1-Apr-2025, and confirmed
// absent from the amended s.394 Table.
var tcsRegistry = map[string]TCSRuleSet{
	"tcs-2026-04": {
		Version: "tcs-2026-04",
		Sections: map[string]tcsSectionRule{
			"206C(1)-SCRAP": {RatePct: pct("2"), Threshold: pct("0.0000"), Basis: BasisPerTxn},
			"206C(1F)-MV":   {RatePct: pct("1"), Threshold: pct("1000000.0000"), Basis: BasisPerTxn},
		},
		NoPANMultiplier: pct("2"),
		NoPANFloorPct:   pct("5"),
		NoPANCeilingPct: pct("20"),
	},
}

// LoadTCSRuleSet returns the registered TCSRuleSet for the given version, or
// ErrUnknownRuleset.
func LoadTCSRuleSet(version string) (TCSRuleSet, error) {
	rs, ok := tcsRegistry[version]
	if !ok {
		return TCSRuleSet{}, fmt.Errorf("%w: %q", ErrUnknownRuleset, version)
	}
	return rs, nil
}

// TCSDerive derives the collection for input under the supplied ruleset. It is
// pure and deterministic.
//
// Rules, in order:
//  1. rate = the section rate.
//  2. s.397(2)(b)(ii): with no PAN on record,
//     rate = min(max(2 * rate, 5%), 20%). The ceiling is part of the same
//     sentence as the floor and is applied LAST, so it bounds the whole no-PAN
//     result rather than either limb alone.
//  3. The collection applies only when the amount EXCEEDS the threshold
//     ("exceeding" at Sl. No. 6); a threshold of 0 therefore means any positive
//     amount attracts collection.
//  4. TCS = round_half_up(amount * rate / 100, 4dp).
func TCSDerive(in TCSInput, rs TCSRuleSet) (TCSResult, error) {
	if rs.Version == "" {
		return TCSResult{}, fmt.Errorf("%w: empty ruleset", ErrUnknownRuleset)
	}
	if in.TaxableAmount.IsNegative() {
		return TCSResult{}, errors.New("compliance: TCS taxable amount must be non-negative")
	}
	rule, ok := rs.Sections[in.Section]
	if !ok {
		return TCSResult{}, fmt.Errorf("%w: %q", ErrUnknownSection, in.Section)
	}

	rate := rule.RatePct
	if !in.PANAvailable {
		// s.397(2)(b)(ii): higher of twice the rate in force, or 5%. NOT the
		// deduction side's flat 20% floor.
		doubled := rate.Mul(rs.NoPANMultiplier)
		rate = doubled
		if rs.NoPANFloorPct.GreaterThan(rate) {
			rate = rs.NoPANFloorPct
		}
		// ...and the same clause's "not exceeding 20%". Applied after the floor
		// so it bounds the result, not either limb. A zero ceiling means the
		// ruleset did not declare one, and is treated as "no ceiling" rather
		// than as "collect nothing" — the alternative silently zeroes every
		// no-PAN collection for any ruleset that omits the field.
		if rs.NoPANCeilingPct.IsPositive() && rate.GreaterThan(rs.NoPANCeilingPct) {
			rate = rs.NoPANCeilingPct
		}
	}

	res := TCSResult{
		TCS:       decimal.Zero,
		RatePct:   rate,
		Threshold: rule.Threshold,
		Basis:     rule.Basis,
	}
	if !in.TaxableAmount.GreaterThan(rule.Threshold) {
		return res, nil
	}
	res.Applied = true
	res.TCS = round(in.TaxableAmount.Mul(rate).Div(hundred))
	return res, nil
}
