package rules

// rule138.go — THE ONE PLACE THE e-WAY BILL FIGURES LIVE, AND THE PROVENANCE OF
// EVERY ONE OF THEM.
//
// ===========================================================================
// ⛔⛔ EVERY FIGURE IN THIS FILE IS SECONDARY MATERIAL. NONE IS VERIFIED AGAINST
// THE BARE RULE. Read this before quoting any of them anywhere.
// ===========================================================================
// PLAN-TALLY-PARITY.md §2.3 is unconditional: "Every statutory rate carries a
// source citation and an effective-from date. Cannot verify it? Mark it
// UNVERIFIED. Never invent a rate to make a test pass."
//
// `docs/statutory/` holds ONLY Form 140 (TDS) material. There is no copy of CGST
// Rule 138 in this repository, and an attempt to fetch the official CBIC text on
// 2026-08-05 FAILED TLS CHAIN VERIFICATION from this environment — both the HTML
// and the PDF endpoints on taxinformation.cbic.gov.in. That failure is recorded
// here rather than in a commit message because a commit message is not read by
// the next person to touch the numbers.
//
// So the figures below are CORROBORATED ACROSS SECONDARY SOURCES and carry that
// caveat in the code, in the payload, and on the printed artefact.
//
// ===========================================================================
// WHY THEY ARE IN GO AND DELIBERATELY NOT IN A MIGRATION CHECK
// ===========================================================================
// This was decided with the user and is not a default. A threshold in a CHECK
// constraint makes re-verification a SCHEMA CHANGE — a migration, a number from a
// reserved block, a backfill decision — where in Go it is an ordinary code change
// with a test. Since the whole point is that these numbers are PENDING
// RE-VERIFICATION against a primary source nobody in this environment can reach,
// the cheaper-to-correct location is the correct one.
//
// The repo's own precedent says the same thing: CanonicalInward.ITCBlockedClause
// is unconstrained in both Go and the schema for exactly this reason, and its
// comment says so.
//
// ⛔ AND THERE IS ONE COPY. Not one per caller, not one in the validator and
// another in the payload builder. A threshold with two copies is a threshold that
// disagrees with itself the first time one is corrected, and the disagreement is
// invisible because both look authoritative.

import "fmt"

// SourceGrade records how well a figure is attested. It exists so the distinction
// cannot be dropped by paraphrase: a value's grade travels with the value.
type SourceGrade string

const (
	// GradePrimary means the published text has been read in this repository.
	// ⛔ NOTHING IN THIS FILE IS GradePrimary. Form 140's TDS material is the only
	// primary statutory source in the tree (docs/statutory/form-140.pdf).
	GradePrimary SourceGrade = "PRIMARY"
	// GradeSecondary means corroborated across independent commentaries but NOT
	// checked against the bare rule. Every figure below is this.
	GradeSecondary SourceGrade = "SECONDARY_UNVERIFIED"
)

// Rule138Basis is the provenance of one figure. Carried alongside the figure and
// emitted into the payload's disclosures, so a reader of the artefact learns what
// a reader of the source would.
type Rule138Basis struct {
	// Citation is the rule and sub-rule the figure is claimed to come from.
	Citation string
	// Grade is how well attested it is. See SourceGrade.
	Grade SourceGrade
	// EffectiveFrom is the date the figure is believed to apply from, or "" when
	// the secondary sources do not state one — and a BLANK IS NOT A ZERO. An
	// unknown effective date is recorded as unknown rather than guessed at, because
	// a wrong effective date is how a correct rate gets applied to the wrong
	// period.
	EffectiveFrom string
	// Sources are the URLs actually consulted.
	Sources []string
	// Caveat is the sentence that must reach the artefact.
	Caveat string
}

// rule138Caveat is the one sentence every figure in this file carries. Written
// once so it cannot drift between figures.
const rule138Caveat = "Not verified against the published rule: CBIC's rule repository failed TLS chain verification from this environment on 2026-08-05, so this figure is corroborated across secondary commentaries only and is pending re-verification."

var (
	// cbicRule138 is the primary source, and it is UNREACHABLE from here. Kept in
	// the citation anyway: the next person to try needs the URL, and its presence
	// beside a SECONDARY grade is what makes the gap legible rather than implied.
	cbicRule138 = "https://taxinformation.cbic.gov.in/content/html/tax_repository/gst/rules/cgst_rules/active/chapter16/rule138_v1.00.html"
	clearTax    = "https://cleartax.in/s/eway-bill-gst-rules-compliance"
	taxmann     = "https://www.taxmann.com/post/blog/process-of-generating-e-way-bill"
)

// ---------------------------------------------------------------------------
// THE FIGURES
// ---------------------------------------------------------------------------

// ConsignmentThresholdRupees is the consignment value at or above which an e-way
// bill is required for a movement of goods.
//
// ⚠️ SECONDARY. Rule 138(1) is cited by commentaries as Rs 50,000. NOT verified.
//
// ⚠️ AND IT IS A SIMPLIFICATION THIS BUILD STATES RATHER THAN HIDES: the rule's
// threshold interacts with intra-State exemptions that individual States have
// notified at higher figures, and with the "consignment value" definition in the
// rule's own explanation (invoice value including tax, excluding exempt goods).
// This build applies the bare figure and DISCLOSES that it has not applied any
// State-specific relaxation — see Assess.
const ConsignmentThresholdRupees = 50000

// PartBExemptDistanceKm is the distance below which Part B (the vehicle number)
// is not required for a movement within the State.
//
// ⚠️ SECONDARY. Commentaries cite 50 km within the State, between the consignor's
// place of business and the transporter's, or the transporter's and the
// consignee's. NOT verified, and the sub-rule number is not asserted here because
// the secondary sources disagree about which proviso carries it — an asserted
// citation that turns out to name the wrong proviso is worse than a general one.
const PartBExemptDistanceKm = 50

// ValidityKmPerDayNormal and ValidityKmPerDayODC are the distances that buy one
// day of validity.
//
// ⚠️ SECONDARY. Rule 138(10) is cited as one day per 200 km for ordinary cargo
// and one day per 20 km for OVER-DIMENSIONAL CARGO. NOT verified.
const (
	ValidityKmPerDayNormal = 200
	ValidityKmPerDayODC    = 20
)

// Rule138Figures returns the provenance of every figure this package applies, so
// a caller can print it without restating any number.
//
// A FUNCTION AND NOT A PACKAGE VARIABLE, so no caller can mutate the record of
// where the numbers came from.
func Rule138Figures() []struct {
	Name  string
	Value string
	Basis Rule138Basis
} {
	secondary := func(citation, from string) Rule138Basis {
		return Rule138Basis{
			Citation:      citation,
			Grade:         GradeSecondary,
			EffectiveFrom: from,
			Sources:       []string{cbicRule138 + " (UNREACHABLE from this environment)", clearTax, taxmann},
			Caveat:        rule138Caveat,
		}
	}
	return []struct {
		Name  string
		Value string
		Basis Rule138Basis
	}{
		{
			Name:  "consignment value threshold",
			Value: fmt.Sprintf("Rs %d", ConsignmentThresholdRupees),
			// "" effective-from, deliberately: the secondary sources do not agree a
			// date, and inventing one would let a correct figure be applied to a
			// period it did not govern.
			Basis: secondary("CGST Rule 138(1)", ""),
		},
		{
			Name:  "Part B exemption distance, within the State",
			Value: fmt.Sprintf("%d km", PartBExemptDistanceKm),
			Basis: secondary("CGST Rule 138, proviso (sub-rule not asserted)", ""),
		},
		{
			Name:  "validity, ordinary cargo",
			Value: fmt.Sprintf("1 day per %d km", ValidityKmPerDayNormal),
			Basis: secondary("CGST Rule 138(10)", ""),
		},
		{
			Name:  "validity, over-dimensional cargo",
			Value: fmt.Sprintf("1 day per %d km", ValidityKmPerDayODC),
			Basis: secondary("CGST Rule 138(10)", ""),
		},
	}
}

// Rule138Disclosures is the block that must reach any artefact quoting these
// figures. It restates NO number — it names each one and its provenance, so the
// figures have exactly one home.
func Rule138Disclosures() []string {
	out := []string{
		"⚠️ THE e-WAY BILL RULES APPLIED HERE ARE SECONDARY MATERIAL. " + rule138Caveat,
	}
	for _, f := range Rule138Figures() {
		from := f.Basis.EffectiveFrom
		if from == "" {
			from = "effective date not stated by the sources consulted"
		} else {
			from = "effective from " + from
		}
		out = append(out, fmt.Sprintf("%s: %s — claimed from %s (%s, %s)",
			f.Name, f.Value, f.Basis.Citation, f.Basis.Grade, from))
	}
	out = append(out,
		"To make these figures CITED rather than corroborated, place the published text of CGST Rule 138 "+
			"and Rule 138(10) into docs/statutory/ — as form-140.pdf already is — and re-verify each figure "+
			"against it. That is a human-only step: CBIC fails TLS verification from this environment.")
	return out
}
