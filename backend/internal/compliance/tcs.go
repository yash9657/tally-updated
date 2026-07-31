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

// ErrDeclarationNotAvailable is returned when a s.394(2) declaration is
// furnished against a collection it cannot reach, or does not meet the
// subsection's own conditions.
//
// A REFUSAL, not an ignore. An operator who filed a declaration believes their
// buyer is exempt; silently collecting anyway would contradict a document they
// hold, and silently exempting would forgo tax that is due.
var ErrDeclarationNotAvailable = errors.New(
	"compliance: this s.394(2) declaration does not exempt this collection")

// declarationEligibleSections is the set s.394(2) reaches: "Irrespective of
// anything contained in sub-section (1) (Table: Sl. Nos. 1 to 5) ...".
//
// Of the two sections this build ships, SCRAP is Sl. No. 4 and is reachable;
// MOTOR VEHICLES are Sl. No. 6 and are NOT. The two fall on opposite sides of
// the line, which is exactly why this is a set and not an assumption.
var declarationEligibleSections = map[string]bool{
	"206C(1)-SCRAP": true,
}

// DeclarationOutcome records what a furnished declaration actually did.
//
// Three values and not a boolean, because the third one is the whole point:
// a declaration can be furnished and INVALID, which is worse for the buyer than
// not furnishing one at all.
type DeclarationOutcome string

const (
	// DeclarationNone: none was furnished.
	DeclarationNone DeclarationOutcome = "NONE"
	// DeclarationExempt: a valid declaration, so no tax is collected.
	DeclarationExempt DeclarationOutcome = "EXEMPT"
	// DeclarationInvalidNoPAN: furnished, but the buyer gave no valid PAN, so
	// s.397(2)(f)(i) makes it invalid and (g) requires the PENAL rate.
	DeclarationInvalidNoPAN DeclarationOutcome = "INVALID_NO_PAN"
)

// TCSDeclaration is a s.394(2) manufacturing declaration as the engine sees it —
// the facts of a migration 0049 tcs_collection_declaration row.
type TCSDeclaration struct {
	// Section is what the declaration was furnished against. Compared with the
	// collection's own section: a declaration for scrap does not exempt a motor
	// vehicle.
	Section string
	// The three conditions of s.394(2), all required.
	BuyerIsResident  bool
	ForManufacturing bool
	NotForTrading    bool
	// BuyerPANValid is s.397(2)(f)(i)'s condition. False makes the declaration
	// INVALID, and (g) then requires the penal no-PAN rate — NOT the base rate,
	// and NOT the exemption.
	BuyerPANValid bool
}

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
	// Declaration is the buyer's s.394(2) declaration, or nil when none was
	// furnished. nil is the ordinary case and means "collect normally".
	Declaration *TCSDeclaration
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
	// Applied is false when the amount did not exceed the threshold, AND when a
	// valid s.394(2) declaration exempted the collection. Declaration
	// distinguishes the two — they are the same zero on the voucher and very
	// different facts in the return.
	Applied bool
	// Declaration records what a furnished s.394(2) declaration did.
	Declaration DeclarationOutcome
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

	// s.394(2), and its coupling to s.397(2)(f)(i)+(g). Evaluated BEFORE the
	// rate, because an invalid declaration changes which rate applies.
	outcome, err := declarationOutcome(in)
	if err != nil {
		return TCSResult{}, err
	}

	// A valid declaration removes the collection entirely. Returned here rather
	// than as a zero at the end so the rate on the result is the section's own —
	// reporting a rate for tax that was not collected would put a figure in the
	// return that never applied.
	if outcome == DeclarationExempt {
		return TCSResult{
			TCS:         decimal.Zero,
			RatePct:     rule.RatePct,
			Threshold:   rule.Threshold,
			Basis:       rule.Basis,
			Applied:     false,
			Declaration: DeclarationExempt,
		}, nil
	}

	// s.397(2)(g): "if any declaration becomes invalid under clause (f)(i), then
	// the deductor or collector shall deduct or collect tax as per the
	// provisions of clause (b)(i) or (ii)". So an invalid declaration forces the
	// no-PAN path REGARDLESS of what PANAvailable says about the collectee — the
	// missing PAN is the reason the declaration failed in the first place.
	noPAN := !in.PANAvailable || outcome == DeclarationInvalidNoPAN

	rate := rule.RatePct
	if noPAN {
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
		TCS:         decimal.Zero,
		RatePct:     rate,
		Threshold:   rule.Threshold,
		Basis:       rule.Basis,
		Declaration: outcome,
	}
	if !in.TaxableAmount.GreaterThan(rule.Threshold) {
		return res, nil
	}
	res.Applied = true
	res.TCS = round(in.TaxableAmount.Mul(rate).Div(hundred))
	return res, nil
}

// declarationOutcome applies s.394(2) and s.397(2)(f)(i).
//
// It REFUSES on a declaration that cannot exempt this collection rather than
// ignoring it, for the reason on ErrDeclarationNotAvailable: an operator holding
// a declaration believes their buyer is exempt, and a silent collection
// contradicts a document in their hands.
func declarationOutcome(in TCSInput) (DeclarationOutcome, error) {
	d := in.Declaration
	if d == nil {
		return DeclarationNone, nil
	}
	// s.394(2) reaches Table Sl. Nos. 1 to 5 only. Motor vehicles are Sl. No. 6.
	if !declarationEligibleSections[in.Section] {
		return "", fmt.Errorf("%w: s.394(2) covers Table Sl. Nos. 1 to 5, and %q is not among "+
			"them, so no declaration can exempt it", ErrDeclarationNotAvailable, in.Section)
	}
	// A declaration furnished for one collection does not exempt another.
	if d.Section != in.Section {
		return "", fmt.Errorf("%w: the declaration was furnished for %q, not %q",
			ErrDeclarationNotAvailable, d.Section, in.Section)
	}
	if !d.BuyerIsResident {
		return "", fmt.Errorf("%w: s.394(2) requires the buyer to be resident in India",
			ErrDeclarationNotAvailable)
	}
	// (a) AND (b), conjunctive: goods for manufacturing that are ALSO for
	// trading do not qualify.
	if !d.ForManufacturing || !d.NotForTrading {
		return "", fmt.Errorf("%w: s.394(2) requires the goods to be for manufacturing, "+
			"processing, producing or generating power AND not for trading; this declaration "+
			"states manufacturing=%v, not-for-trading=%v",
			ErrDeclarationNotAvailable, d.ForManufacturing, d.NotForTrading)
	}
	// s.397(2)(f)(i): no valid PAN makes the declaration invalid. NOT an error —
	// this is a real, reportable state with a defined consequence in (g).
	if !d.BuyerPANValid {
		return DeclarationInvalidNoPAN, nil
	}
	return DeclarationExempt, nil
}
