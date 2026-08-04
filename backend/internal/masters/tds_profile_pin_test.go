package masters_test

// ANTI-DRIFT for the Annexure 4 deductor-category vocabulary — the LIVE half.
//
// Migration 0070's header names the test that is supposed to exist:
//
//	"Annexure 4, all sixteen. Pinned against form140.DeductorCategories by
//	 TestDeductorCategoriesMatchTheMigration"
//
// IT DID NOT EXIST. Nothing in this repo compared the sixteen letters in the
// CHECK against the sixteen the emitter accepts; the migration asserted a pin
// that had never been written. It is written here, under the name 0070 uses, so
// the header stops being a claim.
//
// THREE PLACES NAME THESE LETTERS and each pair can drift:
//
//	migration 0070's category_known CHECK   — what the database will store
//	masters.DeductorCategories()            — what a screen may offer (served)
//	form140.DeductorCategories              — what the return file will accept
//
// Drift between the first two is a picker option that fails on save. Drift
// between the last two is worse and it is silent: a category saves, the operator
// believes the profile is complete, and the REFUSAL ARRIVES AT FILING TIME with
// the quarter already closed.
//
// WHY THIS FILE IMPORTS form140 WHEN internal/masters MAY NOT.
// archtest boundaries_test.go:93 allows internal/masters exactly
// {platform/db, compliance} — form140 is a pure leaf whose only permitted
// consumer is internal/reporting. But the archtest walker SKIPS _test.go files
// (its own comment says so, and printing/printingdb relies on the same thing),
// so a TEST may compare the two copies without granting the PRODUCTION edge
// that would let a master-data save depend on a file format. That asymmetry is
// the whole design: the copy in masters exists so the list can be SERVED, and
// this test is what makes the copy safe.
//
// The migration-file half of these pins lives in tds_profile_validation_test.go
// and needs no database. This file pins the LIVE CATALOGUE, which is a different
// failure: a deployed database whose 0070 was applied from an earlier edition of
// the file. checkClause is shared with deductor_status_test.go.

import (
	"os"
	"strings"
	"testing"

	"github.com/khata/cored/internal/compliance"
	"github.com/khata/cored/internal/compliance/form140"
	"github.com/khata/cored/internal/masters"
)

// TestDeductorCategoriesMatchTheMigration is the name migration 0070 promises.
// It asserts BOTH directions against the CHECK as the database actually holds it.
func TestDeductorCategoriesMatchTheMigration(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	clause := checkClause(t, "company_tds_profile_category_known")

	served := masters.DeductorCategories()
	if len(served) != 16 {
		t.Fatalf("Annexure 4 has sixteen categories; masters serves %d", len(served))
	}
	for _, c := range served {
		if !strings.Contains(clause, "'"+c.Code+"'") {
			t.Errorf("masters serves category %q but migration 0070's category_known CHECK "+
				"does not allow it — the picker offers a code that fails on save", c.Code)
		}
	}
	// The other direction: a code the database accepts that masters cannot serve
	// is a real deductor category no screen can reach, so the profile cannot be
	// recorded at all and reporting.ErrNoDeductorProfile blocks every return.
	for _, lit := range strings.Split(clause, "'") {
		if len(lit) != 1 || lit != strings.ToUpper(lit) || lit < "A" || lit > "Z" {
			continue
		}
		if !masters.IsKnownDeductorCategory(lit) {
			t.Errorf("migration 0070's CHECK allows category %q but masters does not know "+
				"it — a category the return needs that no screen can offer", lit)
		}
	}
}

// TestDeductorCategoriesMatchForm140 is the pair whose drift is SILENT.
//
// The database and the picker disagreeing produces an error on save. The picker
// and the EMITTER disagreeing produces a saved profile and a refusal at filing
// time — so this asserts the codes AND the descriptions, verbatim, in both
// directions. It needs no database.
func TestDeductorCategoriesMatchForm140(t *testing.T) {
	served := masters.DeductorCategories()
	if len(served) != len(form140.DeductorCategories) {
		t.Fatalf("masters serves %d categories, form140 accepts %d",
			len(served), len(form140.DeductorCategories))
	}
	for _, c := range served {
		want, ok := form140.DeductorCategories[c.Code]
		if !ok {
			t.Errorf("masters serves %q, which form140.validateDeductor refuses at emission — "+
				"the profile would save and the return would not build", c.Code)
			continue
		}
		if c.Description != want {
			t.Errorf("category %q: masters describes it as %q, the workbook (via form140) as "+
				"%q. An operator picks by the description, so a drifted one means they pick "+
				"a different category than they read", c.Code, c.Description, want)
		}
	}
	for code := range form140.DeductorCategories {
		if !masters.IsKnownDeductorCategory(code) {
			t.Errorf("form140 accepts Annexure 4 code %q but masters cannot serve it", code)
		}
	}
}

// TestTheTwoDeductorVocabulariesDoNotOverlap is decision A of this slice,
// mechanically.
//
// company_deductor_status holds an s.402(11) ENTITY CATEGORY ("COMPANY") per tax
// year; company_tds_profile holds an Annexure 4 code ("K") per company. If a
// single token were ever valid in both, the two would look interchangeable and
// somebody would map one onto the other — which migration 0070 and
// form140/annexure.go both say is a DECISION, not a derivation. form140 has its
// own copy of this assertion (TestAnnexure4IsNotTheStatutoryEntityCategory); it
// is restated here because THIS is the package that holds both writers, and it
// is here that the tempting helper would be written.
func TestTheTwoDeductorVocabulariesDoNotOverlap(t *testing.T) {
	for _, e := range compliance.KnownEntityCategories {
		if masters.IsKnownDeductorCategory(e) {
			t.Errorf("%q is both an s.402(11) entity category and an Annexure 4 code; if that "+
				"is ever true, the two vocabularies have been merged by accident", e)
		}
	}
	for _, c := range masters.DeductorCategories() {
		if compliance.IsKnownEntityCategory(c.Code) {
			t.Errorf("Annexure 4 code %q is also an entity category", c.Code)
		}
	}
}

// TestTheCategoryConditionalRulesMatchTheLiveConstraints pins the served rules —
// which categories require the Annexure 5 state code and which require or allow
// an Annexure 3 ministry — against the two cross-field CHECKs as the database
// holds them.
//
// The rules are SERVED to the client (TDSDeductorCategory carries them) so a
// screen never decides for itself when to show the field. That makes them a
// vocabulary like any other, and a drifted one produces the worst kind of form:
// one that hides a mandatory field and then reports a constraint violation.
func TestTheCategoryConditionalRulesMatchTheLiveConstraints(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	stateClause := checkClause(t, "company_tds_profile_state_name_follows_category")
	ministryClause := checkClause(t, "company_tds_profile_ministry_follows_category")

	// The mandatory limb of each CHECK is the one that comes before the first
	// AND; both limbs of the state-name CHECK name the same four categories, so
	// membership in the clause text is enough for it.
	for _, c := range masters.DeductorCategories() {
		named := strings.Contains(stateClause, "'"+c.Code+"'")
		if named != c.StateNameCodeRequired {
			t.Errorf("category %q: named in state_name_follows_category = %v, but masters "+
				"serves StateNameCodeRequired = %v", c.Code, named, c.StateNameCodeRequired)
		}
		namedMinistry := strings.Contains(ministryClause, "'"+c.Code+"'")
		if namedMinistry != c.MinistryAllowed {
			t.Errorf("category %q: named in ministry_follows_category = %v, but masters "+
				"serves MinistryAllowed = %v — a screen would hide a mandatory field or "+
				"show a forbidden one", c.Code, namedMinistry, c.MinistryAllowed)
		}
	}
}

// TestMinorHeadsMatchForm140 is the THIRD vocabulary this feature copies, and
// the one that shipped without a pin.
//
// Annexure 7's minor heads are named in three places, exactly as the deductor
// categories are:
//
//	migration 0069's tds_challan_minor_head_known CHECK — what will store
//	masters.TDSChallanMinorHeads()                      — what a screen offers
//	form140.MinorHeads                                  — what the file accepts
//
// TestChallanShapeRulesMatchTheMigration already ties the first two. NOTHING tied
// either to the emitter, so a fourth minor head added to Annexure 7 (in form140
// and in 0069's CHECK) would leave the served picker at three, and the code would
// be refused on save with a raw Postgres constraint name — the exact outcome
// tds_challan.go's header says its Go validation exists to prevent.
//
// ⚠️ IT PINS THE CODE SET AND DELIBERATELY NOT THE DESCRIPTIONS, which is the
// opposite of TestDeductorCategoriesMatchForm140 above, so the difference is
// argued rather than assumed.
//
// The workbook's own wording for 200 is "TDS payable by taxpayer". For a
// DEDUCTOR choosing where their deposit goes, that is actively misleading — the
// "taxpayer" is the deductor, and the distinction that matters to them is
// whether they are paying over of their own motion (200) or answering a demand
// (400). masters therefore serves the plainer meaning, and form140 keeps the
// transcription: the workbook's exact words stay reachable at
// form140.MinorHeads, one grep away, and the file that writes the return uses
// them.
//
// The categories are the other way round because there the workbook's wording IS
// the clearest available ("Company", "Central Government"), so paraphrasing it
// could only lose fidelity.
//
// What must never drift is the SET, because that is the drift that saves a value
// the return will reject. It needs no database.
func TestMinorHeadsMatchForm140(t *testing.T) {
	served := masters.TDSChallanMinorHeads()
	if len(served) != len(form140.MinorHeads) {
		t.Fatalf("masters serves %d minor heads, form140 accepts %d — a challan could be "+
			"stored under a code the return refuses, or a legal code could be unofferable",
			len(served), len(form140.MinorHeads))
	}
	for _, m := range served {
		if _, ok := form140.MinorHeads[m.Code]; !ok {
			t.Errorf("masters serves minor head %q, which the emitter does not accept: the "+
				"challan would save and the return would not build", m.Code)
		}
		// A code with no meaning beside it is a picker of bare numbers, which is
		// what the served Description exists to prevent.
		if strings.TrimSpace(m.Description) == "" {
			t.Errorf("minor head %q is served with no description; there is no default on this "+
				"path, so an operator must be able to tell the three apart", m.Code)
		}
	}
	for code := range form140.MinorHeads {
		found := false
		for _, m := range served {
			if m.Code == code {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("form140 accepts Annexure 7 minor head %q but masters cannot serve it — "+
				"an operator cannot record a deposit the return would have accepted", code)
		}
	}
}
