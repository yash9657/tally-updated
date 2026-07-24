// TCS (Tax Collected at Source) derivation — the collection-side mirror of
// tds.go. Same purity contract: no I/O, safe inside the post transaction.
//
// ---------------------------------------------------------------------------
// RATE PROVENANCE — READ BEFORE RELEASE
// ---------------------------------------------------------------------------
// Same provenance caveat as tds.go: ruleset "tcs-2026-04" was curated OFFLINE
// from model knowledge (cutoff January 2026), not from a statutory text. Every
// fixture in corpora/tcs/*.json carries its citation. Re-verify before release.
//
// ---------------------------------------------------------------------------
// TWO THINGS THAT ARE NOT A MIRROR OF THE TDS SIDE — do not "simplify" them
// ---------------------------------------------------------------------------
//  1. s.206C(1H) (TCS on sale of goods above 50 lakh, 0.1%) is DELIBERATELY
//     ABSENT. It was omitted w.e.f. 1-Apr-2025 by Finance Act 2025 (the buyer's
//     s.194Q TDS obligation supersedes it). Shipping it would make the engine
//     collect a tax that no longer exists. Confidence MEDIUM-HIGH — this is the
//     single most important item for the release spot-check.
//  2. The no-PAN rule for collection is s.206CC, NOT s.206AA. 206CC is "higher
//     of TWICE the rate in force, or 5%" — it is NOT a flat 20% floor. Reusing
//     the TDS no-PAN constant here would be WRONG. It is modelled as
//     max(NoPANMultiplier * rate, NoPANFloorPct) and covered by a dedicated
//     fixture.
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
	// NoPANMultiplier / NoPANFloorPct implement s.206CC: when no PAN is on
	// record the rate is the HIGHER of (multiplier * rate in force, floor).
	NoPANMultiplier decimal.Decimal
	NoPANFloorPct   decimal.Decimal
}

// tcsRegistry holds the known, effective-dated TCS rulesets keyed by version.
// Separate from both `registry` (GST) and `tdsRegistry`.
//
// Section keys and citations:
//
//	206C(1)-SCRAP  sale of scrap                 1%, no per-transaction
//	                                             exemption limit (threshold 0)
//	                                             — s.206C(1) Table. HIGH.
//	206C(1F)-MV    sale of a motor vehicle of    1%, 10,00,000 — s.206C(1F).
//	               value exceeding Rs 10,00,000  HIGH.
//
// s.206CC no-PAN: higher of 2x the rate in force or 5%. HIGH.
//
// NOT SHIPPED (deliberate): 206C(1H) — omitted w.e.f. 1-Apr-2025.
var tcsRegistry = map[string]TCSRuleSet{
	"tcs-2026-04": {
		Version: "tcs-2026-04",
		Sections: map[string]tcsSectionRule{
			"206C(1)-SCRAP": {RatePct: pct("1"), Threshold: pct("0.0000"), Basis: BasisPerTxn},
			"206C(1F)-MV":   {RatePct: pct("1"), Threshold: pct("1000000.0000"), Basis: BasisPerTxn},
		},
		NoPANMultiplier: pct("2"),
		NoPANFloorPct:   pct("5"),
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
//  2. s.206CC: with no PAN on record, rate = max(2 * rate, 5%).
//  3. The collection applies only when the amount EXCEEDS the threshold
//     ("exceeding" in s.206C(1F)); a threshold of 0 therefore means any
//     positive amount attracts collection.
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
		// s.206CC: higher of twice the rate in force, or 5%. NOT 206AA's 20%.
		doubled := rate.Mul(rs.NoPANMultiplier)
		rate = doubled
		if rs.NoPANFloorPct.GreaterThan(rate) {
			rate = rs.NoPANFloorPct
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
