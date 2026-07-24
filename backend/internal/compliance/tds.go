// TDS (Tax Deducted at Source) derivation.
//
// TDSDerive is a PURE function beside TaxDerive: given a nature-of-payment
// section, the deductee's type, whether a PAN is on record and the amount the
// deduction applies to, plus a versioned TDSRuleSet, it deterministically
// derives the amount to deduct. It performs no I/O and is safe to call inside
// the atomic voucher-post transaction.
//
// ---------------------------------------------------------------------------
// RATE PROVENANCE — READ BEFORE RELEASE
// ---------------------------------------------------------------------------
// The section/rate/threshold table below (ruleset "tds-2026-04", FY 2026-27 /
// AY 2027-28) was curated OFFLINE from model knowledge with a training cutoff
// of January 2026. It was NOT read out of a statutory text, a gazette, or a
// live source: this build machine has no network access and RESEARCH.md carries
// no TDS rate table (it documents only TallyPrime's TDS *setup UI*).
//
// Each fixture in corpora/tds/*.json carries the statutory citation in its
// `description` field. The rates MUST be re-verified against the bare Act /
// CBDT rate chart before this engine is used on real books. A per-rule
// confidence annotation is recorded in the tdsRegistry doc comment below;
// start any review with the MEDIUM-confidence rows.
//
// ---------------------------------------------------------------------------
// v1 SCOPE / DISCLOSED APPROXIMATION
// ---------------------------------------------------------------------------
// v1 applies every threshold PER TRANSACTION. Only s.194C(5)'s 30,000 limit is
// genuinely a per-single-payment limit. 194J and 194H limits are statutorily
// ANNUAL aggregates and 194I's is a MONTHLY limit, so v1 UNDER-deducts on a
// stream of small payments that aggregate past the limit. Annual/monthly
// cumulation is a disclosed deferral (IMPLEMENTATION_PLAN.md §7.5). The
// ThresholdBasis field makes the approximation machine-visible so a future
// cumulative engine has the metadata it needs.
//
// v1 also PERSISTS NOTHING: the ledger derives and verifies a posted TDS leg
// but no schema records the section / deductee / derived amount, so no report
// (26Q, challan) can see it. Disclosed deferral.
package compliance

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
)

// ErrUnknownSection is returned when a nature-of-payment section is not in the
// loaded ruleset. It is deliberately distinct from ErrUnknownRuleset so callers
// can tell "no such ruleset version" from "ruleset does not cover this section".
var ErrUnknownSection = errors.New("compliance: unknown TDS/TCS section")

// Deductee types recognised by TDSDerive. Only 194C's rate splits by deductee
// type; every other section carries the same rate for both, so the split is
// still modelled uniformly (RateIndHUF == RateOther) rather than special-cased.
const (
	DeducteeIndHUF = "IND_HUF"
	DeducteeOther  = "OTHER"
)

// ThresholdBasis records what the threshold in a rule is statutorily measured
// over. v1 applies EVERY basis per transaction (see the package-level note);
// the value exists so the approximation is visible to callers and to the future
// cumulative engine.
type ThresholdBasis string

const (
	// BasisPerTxn: the limit really is per single payment (s.194C(5)).
	BasisPerTxn ThresholdBasis = "PER_TXN"
	// BasisAnnual: the limit is an aggregate over the financial year; v1
	// approximates it per transaction and therefore under-deducts on splits.
	BasisAnnual ThresholdBasis = "ANNUAL"
	// BasisMonthly: the limit is per month (194I after Finance Act 2025); v1
	// approximates it per transaction.
	BasisMonthly ThresholdBasis = "MONTHLY"
)

// TDSInput is the complete, self-contained input to TDSDerive.
type TDSInput struct {
	// Section is the nature-of-payment key, e.g. "194C", "194J-P", "194I-B".
	Section string
	// DeducteeType is DeducteeIndHUF or DeducteeOther.
	DeducteeType string
	// PANAvailable reports whether the deductee's PAN is on record. When false,
	// s.206AA raises the rate to the higher of (rate in force, 20%).
	PANAvailable bool
	// TaxableAmount is the amount the deduction applies to (NUMERIC(19,4)).
	TaxableAmount decimal.Decimal
}

// TDSResult is the derived deduction. Money fields are quantized to 4 dp.
type TDSResult struct {
	// TDS is the amount to deduct; zero when Applied is false.
	TDS decimal.Decimal
	// RatePct is the rate actually applied, AFTER any s.206AA override.
	RatePct decimal.Decimal
	// Threshold is the limit that was tested (see ThresholdBasis on the rule).
	Threshold decimal.Decimal
	// Basis records how the threshold is statutorily measured. v1 always tests
	// it per transaction regardless.
	Basis ThresholdBasis
	// Applied is false when the amount did not exceed the threshold.
	Applied bool
}

// sectionRule is one nature-of-payment row of a TDSRuleSet.
//
// PerTxnThreshold: v1 applies this limit PER TRANSACTION. For 194J and 194H it
// is statutorily an ANNUAL aggregate and for 194I a MONTHLY limit — v1
// therefore UNDER-deducts on split payments. Annual/monthly cumulation is a
// disclosed deferral (IMPLEMENTATION_PLAN.md §7.5). Basis records the truth.
type sectionRule struct {
	RateIndHUF      decimal.Decimal
	RateOther       decimal.Decimal
	PerTxnThreshold decimal.Decimal
	Basis           ThresholdBasis
}

// TDSRuleSet is the effective-dated TDS behaviour for a version.
type TDSRuleSet struct {
	// Version is the ruleset identifier, e.g. "tds-2026-04".
	Version string
	// Sections maps a nature-of-payment key to its rule.
	Sections map[string]sectionRule
	// NoPANFloorPct is s.206AA's floor: when no PAN is on record the rate is
	// the HIGHER of (rate in force, this floor). Modelled as a floor rather
	// than a flat replacement so a hypothetical >20% section is not silently
	// reduced.
	NoPANFloorPct decimal.Decimal
}

func pct(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// tdsRegistry holds the known, effective-dated TDS rulesets keyed by version.
// It is deliberately SEPARATE from the GST `registry` in tax.go.
//
// Section keys and citations (see the package doc for provenance):
//
//	194C    contractor payments        1% IND_HUF / 2% other, 30,000 per payment
//	                                   s.194C(5) single sum; the 1,00,000 annual
//	                                   aggregate is deferred (§7.5). RATE: HIGH
//	                                   confidence.
//	194J-P  professional fees          10%, 50,000 (annual) — s.194J(1)(a);
//	                                   threshold raised 30,000 -> 50,000 by
//	                                   Finance Act 2025. RATE: HIGH. THRESHOLD:
//	                                   MEDIUM — spot-check.
//	194J-T  technical services /        2%, 50,000 (annual) — s.194J proviso
//	        call-centre / cinematographic
//	        film royalty               RATE: HIGH. THRESHOLD: MEDIUM.
//	194H    commission or brokerage     2%, 20,000 (annual) — rate cut 5% -> 2%
//	                                   w.e.f. 1-Oct-2024 by Finance (No.2) Act
//	                                   2024; threshold 15,000 -> 20,000 by
//	                                   Finance Act 2025. BOTH: MEDIUM — this is
//	                                   the FIRST thing to spot-check.
//	194I-A  rent of plant/machinery/    2%, 50,000 per MONTH — s.194I(a);
//	        equipment                  Finance Act 2025 replaced the 2,40,000/yr
//	                                   limit with 50,000/month. RATE: HIGH.
//	                                   THRESHOLD: MEDIUM.
//	194I-B  rent of land/building/     10%, 50,000 per MONTH — s.194I(b). RATE:
//	        furniture/fittings         HIGH. THRESHOLD: MEDIUM.
//
// s.206AA no-PAN floor 20% — HIGH confidence.
var tdsRegistry = map[string]TDSRuleSet{
	"tds-2026-04": {
		Version: "tds-2026-04",
		Sections: map[string]sectionRule{
			"194C":   {RateIndHUF: pct("1"), RateOther: pct("2"), PerTxnThreshold: pct("30000.0000"), Basis: BasisPerTxn},
			"194J-P": {RateIndHUF: pct("10"), RateOther: pct("10"), PerTxnThreshold: pct("50000.0000"), Basis: BasisAnnual},
			"194J-T": {RateIndHUF: pct("2"), RateOther: pct("2"), PerTxnThreshold: pct("50000.0000"), Basis: BasisAnnual},
			"194H":   {RateIndHUF: pct("2"), RateOther: pct("2"), PerTxnThreshold: pct("20000.0000"), Basis: BasisAnnual},
			"194I-A": {RateIndHUF: pct("2"), RateOther: pct("2"), PerTxnThreshold: pct("50000.0000"), Basis: BasisMonthly},
			"194I-B": {RateIndHUF: pct("10"), RateOther: pct("10"), PerTxnThreshold: pct("50000.0000"), Basis: BasisMonthly},
		},
		NoPANFloorPct: pct("20"),
	},
}

// LoadTDSRuleSet returns the registered TDSRuleSet for the given version, or
// ErrUnknownRuleset.
func LoadTDSRuleSet(version string) (TDSRuleSet, error) {
	rs, ok := tdsRegistry[version]
	if !ok {
		return TDSRuleSet{}, fmt.Errorf("%w: %q", ErrUnknownRuleset, version)
	}
	return rs, nil
}

// TDSSections returns the section keys covered by a ruleset, for catalogs and
// tests. Order is unspecified.
func TDSSections(rs TDSRuleSet) []string {
	out := make([]string, 0, len(rs.Sections))
	for k := range rs.Sections {
		out = append(out, k)
	}
	return out
}

// TDSDerive derives the deduction for input under the supplied ruleset. It is
// pure and deterministic.
//
// Rules, in order:
//  1. rate = RateIndHUF or RateOther, by DeducteeType.
//  2. s.206AA: when no PAN is on record, rate = max(rate, NoPANFloorPct).
//  3. The deduction applies only when the amount EXCEEDS the threshold. The
//     statutes are worded "does not exceed" / "exceeding", so an amount exactly
//     equal to the limit attracts NO deduction.
//  4. TDS = round_half_up(amount * rate / 100, 4dp).
func TDSDerive(in TDSInput, rs TDSRuleSet) (TDSResult, error) {
	if rs.Version == "" {
		return TDSResult{}, fmt.Errorf("%w: empty ruleset", ErrUnknownRuleset)
	}
	if in.TaxableAmount.IsNegative() {
		return TDSResult{}, errors.New("compliance: TDS taxable amount must be non-negative")
	}
	rule, ok := rs.Sections[in.Section]
	if !ok {
		return TDSResult{}, fmt.Errorf("%w: %q", ErrUnknownSection, in.Section)
	}

	var rate decimal.Decimal
	switch in.DeducteeType {
	case DeducteeIndHUF:
		rate = rule.RateIndHUF
	case DeducteeOther:
		rate = rule.RateOther
	default:
		return TDSResult{}, fmt.Errorf("compliance: invalid deductee type %q", in.DeducteeType)
	}

	// s.206AA(1): in the absence of a PAN, deduct at the HIGHER of the rate in
	// force and 20%. A floor, never a replacement.
	if !in.PANAvailable && rs.NoPANFloorPct.GreaterThan(rate) {
		rate = rs.NoPANFloorPct
	}

	res := TDSResult{
		TDS:       decimal.Zero,
		RatePct:   rate,
		Threshold: rule.PerTxnThreshold,
		Basis:     rule.Basis,
	}
	if !in.TaxableAmount.GreaterThan(rule.PerTxnThreshold) {
		return res, nil
	}
	res.Applied = true
	res.TDS = round(in.TaxableAmount.Mul(rate).Div(hundred))
	return res, nil
}
