// TDS (Tax Deducted at Source) derivation.
//
// TDSDerive is a PURE function beside TaxDerive: given a nature-of-payment
// section, the deductee's type, whether a PAN is on record and the amount the
// deduction applies to, plus a versioned TDSRuleSet, it deterministically
// derives the amount to deduct. It performs no I/O and is safe to call inside
// the atomic voucher-post transaction.
//
// ---------------------------------------------------------------------------
// RATE PROVENANCE — VERIFIED AGAINST THE AMENDED ACT
// ---------------------------------------------------------------------------
// The table below (ruleset "tds-2026-04") was originally curated offline from
// model knowledge. It has since been read against PRIMARY TEXT:
// docs/statutory/ita-2025-as-amended-fa-2026.pdf — the Income-tax Act 2025 as
// amended by the Finance Act 2026 — s.393(1) Table "FOR PAYMENTS TO RESIDENT"
// and s.397(2). Working: docs/design/t6-model.md §9 and t6-gaps-model.md §0.
//
// ALL SIX ORIGINAL TDS RATES ARE CONFIRMED, including the two the original
// annotation trusted least (194H's 2%/20,000 and 194I's monthly 50,000). None
// of the rows we ship carries a Finance Act 2026 footnote, so the amendment did
// not move them. The COLLECTION side was not so lucky — scrap TCS was wrong;
// see tcs.go.
//
// Each fixture in corpora/tds/*.json carries the statutory citation in its
// `description` field.
//
// ⚠️ A VERIFICATION IS ONLY AS CURRENT AS THE EDITION IT READ. An earlier pass
// checked these against the Act AS ENACTED and reported "eight of eight rules
// right"; the Finance Act 2026 had by then changed one. Record the edition, and
// re-read on every Finance Act.
//
// ⚠️ THE SECTION KEYS BELOW NAME A REPEALED ACT. "194C", "194J-P" and the rest
// are 1961-Act nomenclature; the statute in force is the 2025 Act and the return
// keys off s.393/394 Table Sl. Nos. The four-digit codes in tdsReturnCodes are
// the bridge. Renaming is held for T6 so names and codes move in one reviewable
// step (PLAN-TALLY-PARITY.md open item 10) — do not do it piecemeal here.
//
// ---------------------------------------------------------------------------
// v1 SCOPE / DISCLOSED APPROXIMATION
// ---------------------------------------------------------------------------
// v1 applies every threshold PER TRANSACTION, and this UNDER-DEDUCTS.
//
// THE APPROXIMATION IS WIDER THAN THIS COMMENT USED TO CLAIM. It said only
// 194C's limit was genuinely per-payment and the others were annual or monthly.
// The chapeau of s.393(1)(a) is broader than that: tax is due
//
//	"on the ENTIRE amount of such income or sum, where the amount OR AGGREGATE
//	 OF AMOUNTS exceeds the threshold limit specified in column D"
//
// — which makes aggregation the rule for EVERY row of the s.393 Table, not a
// property of particular ones. Sl. No. 6(i) (contractors) is merely the only row
// stating TWO limits: ₹30,000 for any single sum AND ₹1,00,000 in the aggregate.
//
// Two consequences worth stating plainly, because both cost the deductor money:
//
//   - Twenty ₹20,000 contractor bills attract NOTHING here, where the statute
//     demands deduction once the aggregate passes ₹1,00,000.
//   - "On the entire amount" means the crossing payment must CATCH UP the
//     earlier under-deducted base, not merely start deducting prospectively.
//
// Cumulation needs stored state (an aggregate per deductee, section and tax
// year), which is why it is T6 work and not a change to this pure function.
// ThresholdBasis makes the approximation machine-visible in the meantime.
//
// v1 also PERSISTS NOTHING: the ledger derives and verifies a posted TDS leg
// but no schema records the section / deductee / derived amount, so no report
// (26Q, challan) can see it. Disclosed deferral.
package compliance

import (
	"errors"
	"fmt"
	"sort"

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
	// BasisNil: there is NO threshold — s.393(1) Table col. D reads "Nil" — so
	// any positive sum attracts deduction and no cumulation question arises.
	//
	// It is a distinct value rather than "PER_TXN with a limit of zero" because
	// those two are different statements about the law. PER_TXN asserts that the
	// statute measures a per-payment limit, which for a Nil-threshold row it does
	// not; and a future cumulative engine reading Basis must be able to tell
	// "this row needs no aggregate" from "this row's aggregate is a per-payment
	// one". Directors' fees (Sl. No. 6(iii)(c)) is the first such row.
	BasisNil ThresholdBasis = "NIL"
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
	// ReturnCode is the Form 140 Annexure 2 payment code for this deduction —
	// see TDSReturnCode for why it is neither derived from the section nor
	// derived from the section alone.
	//
	// It is carried on the RESULT so that the code and the amount are decided at
	// the same moment, from the same inputs. A return builder that looked the
	// code up later, from a stored section, would be re-making a decision this
	// function already made, and the two could disagree.
	//
	// Empty only when the section has no code, which TestEveryTDSSectionHasA
	// ReturnCode makes unreachable for a shipped ruleset.
	ReturnCode string

	// ----- Cumulation (aggregate.go). Zero-valued from plain TDSDerive, which
	// knows nothing about prior payments; populated by TDSDeriveCumulative.

	// AggregateBaseRupees is the cumulative amount paid or credited to this
	// deductee under this section in this tax year, INCLUDING this payment.
	AggregateBaseRupees decimal.Decimal
	// AggregateThresholdRupees is the aggregate limit that was tested. For
	// contractors this is limb (b)'s ₹1,00,000; for every other row it is the
	// section's single stated threshold, which the s.393(1)(a) chapeau applies
	// to "the amount or aggregate of amounts" alike.
	AggregateThresholdRupees decimal.Decimal
	// AggregateCrossed is true when THIS payment took the cumulative total past
	// the aggregate limit — i.e. when a catch-up was charged.
	//
	// It matters to the RETURN and not only to the arithmetic: Annexure 6 flag
	// `Y` means "payment below the threshold limit", and a sub-₹30,000 payment
	// that crosses the ₹1,00,000 aggregate stops being a Y.
	AggregateCrossed bool
	// CatchUpBaseRupees is the EARLIER base this payment reached back to tax.
	CatchUpBaseRupees decimal.Decimal
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
//	194J-D  remuneration, fees or       10%, threshold NIL — s.393(1) [Table:
//	        commission to a DIRECTOR    Sl. No. 6(iii)(c)], rate limb (b) "10% of
//	        of a company, other than    such sum in cases other than (a)",
//	        salary under s.392          threshold "(ii) for (c) of Col. B: Nil."
//	                                    VERIFIED against the amended Act.
//
// s.206AA no-PAN floor 20% — HIGH confidence.
//
// ⚠️ 194J-D SHARES ITS STATUTORY LIMB WITH 194J-P. Both are Sl. No. 6(iii).D(b)
// at 10%, and they differ ONLY in the nature of the payment — but they have
// DIFFERENT return codes (1028 vs 1027). An engine deriving the code from the
// section files directors' fees as professional fees. See TDSReturnCode.
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
			"194J-D": {RateIndHUF: pct("10"), RateOther: pct("10"), PerTxnThreshold: pct("0.0000"), Basis: BasisNil},
		},
		NoPANFloorPct: pct("20"),
	},
}

// ErrNoReturnCode is returned when a section has no Form 140 payment code. It is
// distinct from ErrUnknownSection: "this section is not in the ruleset" and
// "this section cannot be reported in the return" are different faults, and the
// second one must never be answered with a plausible-looking code.
var ErrNoReturnCode = errors.New("compliance: no Form 140 payment code for this section")

// tdsReturnCodes maps a nature of payment to its Form 140 Annexure 2 payment
// code.
//
// SOURCE: docs/statutory/form-140-file-format.xlsx, Annexure 2 ("Nature of
// Payment | Section | Section code to be used in the return"), prescribed
// format version 1.0, TY 2026-27 onwards. Tabulated in
// docs/statutory/README.md. NOT from a secondary list.
//
// TWO REASONS THIS IS A TABLE AND NOT A DERIVATION, both load-bearing:
//
//  1. THE CODE IS NOT A FUNCTION OF THE SECTION. 194J-P (professional services)
//     and 194J-D (directors' remuneration) are BOTH s.393(1) [Table: Sl. No.
//     6(iii).D(b)] at 10% — same limb, same rate — and carry the codes 1027 and
//     1028 respectively. Deriving from the section alone files a director's
//     sitting fee as a professional fee, under a code the department maps to a
//     different nature of payment.
//
//  2. THE CODE IS NOT A FUNCTION OF THE SECTION ALONE EITHER. Contractor
//     payments split by DEDUCTEE TYPE: 1023 for individual/HUF, 1024 for any
//     other person. That is the same split the rate already makes, but it has to
//     be made twice because nothing guarantees the two stay aligned.
//
// So the key is (section, deductee type) and the map is keyed on the pair.
var tdsReturnCodes = map[string]map[string]string{
	// s.393(1) [Table: Sl. No. 6(i).D(a)] and (b).
	"194C": {DeducteeIndHUF: "1023", DeducteeOther: "1024"},
	// Sl. No. 6(iii).D(a) — technical services, cinematographic-film royalty,
	// call-centre operator.
	"194J-T": {DeducteeIndHUF: "1026", DeducteeOther: "1026"},
	// Sl. No. 6(iii).D(b) — professional services, or a sum under s.26(2)(h).
	"194J-P": {DeducteeIndHUF: "1027", DeducteeOther: "1027"},
	// Sl. No. 6(iii).D(b) AS WELL — see reason 1 above. Different code.
	"194J-D": {DeducteeIndHUF: "1028", DeducteeOther: "1028"},
	// Sl. No. 1(ii) — commission or brokerage, OTHERS. Insurance commission is
	// Sl. No. 1(i) and code 1005; this engine does not model it, so a 1005
	// payment must not be routed here.
	"194H": {DeducteeIndHUF: "1006", DeducteeOther: "1006"},
	// Sl. No. 2(ii).D(a) — machinery, plant or equipment.
	"194I-A": {DeducteeIndHUF: "1008", DeducteeOther: "1008"},
	// Sl. No. 2(ii).D(b) — land, building, furniture or fittings.
	"194I-B": {DeducteeIndHUF: "1009", DeducteeOther: "1009"},
}

// TDSReturnCode returns the Form 140 Annexure 2 payment code for a nature of
// payment and deductee type.
//
// It REFUSES rather than guessing. A return filed under a wrong payment code is
// filed against the wrong nature of payment for every deductee in it, and the
// deductee's credit is what breaks.
func TDSReturnCode(section, deducteeType string) (string, error) {
	byType, ok := tdsReturnCodes[section]
	if !ok {
		return "", fmt.Errorf("%w: section %q", ErrNoReturnCode, section)
	}
	code, ok := byType[deducteeType]
	if !ok {
		return "", fmt.Errorf("%w: section %q has no code for deductee type %q",
			ErrNoReturnCode, section, deducteeType)
	}
	return code, nil
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

// SectionDoc is ONE section of a ruleset, described in NAMES AND FIGURES ONLY —
// no rule object, no SQL, nothing a caller could evaluate for itself.
//
// ⛔ IT EXISTS SO NO CLIENT EVER COPIES THIS TABLE. Register row D3 is that a
// deduction cannot be keyed at all; the form that fixes it has to offer the
// sections, and a TypeScript array of "194C", "194J-P", … would be a second copy
// of a statutory table that changes with every Finance Act — the drift bug the
// TDS challan screen's rule (3) records for the minor heads, on a list ten times
// the size and with rates attached. The rate and threshold travel WITH the key
// because an operator choosing between 194J-P and 194J-T is choosing between 10%
// and 2%, and a bare key makes that invisible.
//
// The figures are STRINGS, fixed to the precision the engine holds them at, for
// the reason every money and rate on this seam is a string: a JSON number is a
// float64.
type SectionDoc struct {
	Section string `json:"section"`
	// RateIndHUFPct / RateOtherPct differ only on 194C in the shipped ruleset;
	// both are carried always, so a caller never has to know which sections split
	// by deductee type.
	RateIndHUFPct string `json:"rate_ind_huf_pct"`
	RateOtherPct  string `json:"rate_other_pct"`
	// ThresholdRupees is the limit the AMOUNT MUST EXCEED — an amount exactly
	// equal to it attracts NO deduction (the statutes read "exceeding").
	ThresholdRupees string `json:"threshold_rupees"`
	// Basis is what the threshold is statutorily measured over. ⚠️ v1 applies
	// EVERY basis per transaction, so a caller that displays this is displaying an
	// approximation it must not describe as the law — see the package note.
	Basis string `json:"basis"`
}

// TDSSectionCatalog describes every section in a ruleset, in KEY ORDER, for the
// entry form's picker. Sorted so the served list is deterministic: an
// unspecified order would reshuffle a picker between requests.
func TDSSectionCatalog(rs TDSRuleSet) []SectionDoc {
	out := make([]SectionDoc, 0, len(rs.Sections))
	for k, r := range rs.Sections {
		out = append(out, SectionDoc{
			Section:         k,
			RateIndHUFPct:   r.RateIndHUF.String(),
			RateOtherPct:    r.RateOther.String(),
			ThresholdRupees: r.PerTxnThreshold.StringFixed(4),
			Basis:           string(r.Basis),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Section < out[j].Section })
	return out
}

// TDSNoPANFloorPct is the s.206AA floor a ruleset applies when no PAN is on
// record. Served alongside the catalog because it CHANGES THE RATE the operator
// is looking at — a form that shows 2% for 194C and then posts 20% has told them
// something untrue.
func TDSNoPANFloorPct(rs TDSRuleSet) string { return rs.NoPANFloorPct.String() }

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

	// Best-effort: a section with no code yields an empty string rather than an
	// error, because the DEDUCTION is still correct and refusing to compute it
	// would be the wrong remedy for a reporting-table gap. The invariant that
	// every shipped section HAS a code is pinned by a test instead, which is
	// where a gap in a static table belongs.
	code, _ := TDSReturnCode(in.Section, in.DeducteeType)

	res := TDSResult{
		TDS:        decimal.Zero,
		RatePct:    rate,
		Threshold:  rule.PerTxnThreshold,
		Basis:      rule.Basis,
		ReturnCode: code,
	}
	if !in.TaxableAmount.GreaterThan(rule.PerTxnThreshold) {
		return res, nil
	}
	res.Applied = true
	res.TDS = round(in.TaxableAmount.Mul(rate).Div(hundred))
	return res, nil
}
