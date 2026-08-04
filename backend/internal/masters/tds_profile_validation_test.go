package masters

// PURE tests for the deductor return profile — no database, no company, nothing
// to tear down. They are in package `masters` rather than `masters_test` because
// what they exercise is the validation and the column binding, both of which are
// unexported on purpose: the exported surface is SetTDSProfile, and a sentence
// an operator reads is not something to widen the API for.
//
// TWO KINDS OF ASSERTION LIVE HERE:
//
//  1. THE SENTENCES. One case per CHECK migration 0070 carries, asserting that
//     the refusal names what to do rather than what constraint fired. These are
//     the reason validateTDSProfile exists at all.
//  2. THE ANTI-DRIFT PINS THAT NEED NO DATABASE. The column list and the three
//     category vocabularies are read OUT OF migrations/0070_company_tds_profile.sql
//     — the file is the contract, so a pin against it runs in every environment,
//     including one with no Postgres. tds_profile_pin_test.go pins the same
//     vocabularies against the LIVE catalogue, which is a different failure: a
//     deployed database whose 0070 was applied from an older edition of the file.

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// check runs the exact pair SetTDSProfile runs, so a case cannot pass here and
// fail on save because of normalisation.
func checkProfile(in TDSProfileRecord) error {
	return validateTDSProfile(normalizeTDSProfile(in))
}

// validTDSProfile is a Company (Annexure 4 code K) deductor. K is chosen for the
// same reason testdb's fixture chooses it: it is the branch that FORBIDS both
// the Annexure 5 state name and the Annexure 3 ministry, so the conditional
// fields are empty and each test can set exactly the one it is about.
func validTDSProfile() TDSProfileRecord {
	return TDSProfileRecord{
		TAN:          "MUMK12345E",
		PAN:          "AAACK1234M",
		DeductorName: "Khata Traders Private Limited",
		Category:     "K",
		Address: TDSAddress{
			Line1:     "14 Marine Lines",
			Country:   "India",
			StateCode: "19",
			Pincode:   "400020",
		},
		Email:              "accounts@khata.example",
		ContactCountryCode: "91",
		ContactNumber:      "2226301234",
		Responsible: TDSResponsiblePerson{
			Name:        "Radhika Venkatesan",
			Designation: "Finance Director",
			PAN:         "AKQPV7821L",
			Address: TDSAddress{
				Line1:     "14 Marine Lines",
				Country:   "India",
				StateCode: "19",
				Pincode:   "400020",
			},
			ContactCountryCode: "91",
			ContactNumber:      "2226301299",
		},
	}
}

func TestAValidProfileIsAccepted(t *testing.T) {
	if err := checkProfile(validTDSProfile()); err != nil {
		t.Fatalf("the fixture must be valid or every other case here is untested: %v", err)
	}
}

// TestTheTANIsNotTheGSTIN is the first mistake an operator makes, because
// `company` holds a GSTIN and holds no TAN at all. A 15-character GSTIN in the
// TAN field must be refused with the reason, not stored.
func TestTheTANIsNotTheGSTIN(t *testing.T) {
	p := validTDSProfile()
	p.TAN = "27AAACK1234M1Z5"
	err := checkProfile(p)
	if !errors.Is(err, ErrInvalidTDSProfile) || !strings.Contains(err.Error(), "NOT the GSTIN") {
		t.Fatalf("want the TAN-is-not-the-GSTIN refusal, got %v", err)
	}
}

// TestStatutoryIdentifiersAreCaseFolded: a TAN keyed in lower case is the same
// TAN. The shape CHECK accepts capitals only, so without normalisation the
// operator would be shown a constraint violation for a value that is correct.
func TestStatutoryIdentifiersAreCaseFolded(t *testing.T) {
	p := validTDSProfile()
	p.TAN = strings.ToLower(p.TAN)
	p.PAN = strings.ToLower(p.PAN)
	p.Responsible.PAN = strings.ToLower(p.Responsible.PAN)
	if err := checkProfile(p); err != nil {
		t.Fatalf("lower-case identifiers should be normalised, not refused: %v", err)
	}
	// And the normalised record is what would reach the column.
	if got := normalizeTDSProfile(p).TAN; got != "MUMK12345E" {
		t.Errorf("normalised TAN is %q", got)
	}
}

// TestPANNOTREQDIsTheDeductorsAlone. The format prescribes the literal for a
// deductor not required to hold a PAN — a real value with a meaning. Migration
// 0070 allows it in pan_shape and NOT in responsible_pan_shape, because the
// responsible person is a named human with their own PAN.
func TestPANNOTREQDIsTheDeductorsAlone(t *testing.T) {
	p := validTDSProfile()
	p.PAN = "PANNOTREQD"
	if err := checkProfile(p); err != nil {
		t.Fatalf("PANNOTREQD is valid for the deductor: %v", err)
	}

	p2 := validTDSProfile()
	p2.Responsible.PAN = "PANNOTREQD"
	err := checkProfile(p2)
	if !errors.Is(err, ErrInvalidTDSProfile) || !strings.Contains(err.Error(), "DIFFERENT field") {
		t.Fatalf("want the responsible person's PAN refused, got %v", err)
	}
}

// TestTheStatutoryEntityCategoryIsNotAnAnnexure4Code is decision A of this
// slice, asserted rather than commented. "COMPANY" is what
// company_deductor_status holds; "K" is what the return wants. Neither is
// derived from the other, and the refusal has to say so — otherwise the next
// reader adds the mapping.
func TestTheStatutoryEntityCategoryIsNotAnAnnexure4Code(t *testing.T) {
	p := validTDSProfile()
	p.Category = "COMPANY"
	err := checkProfile(p)
	if !errors.Is(err, ErrInvalidTDSProfile) || !strings.Contains(err.Error(), "Annexure 4") {
		t.Fatalf("want the Annexure 4 refusal for a statutory entity category, got %v", err)
	}
	if !strings.Contains(err.Error(), "not a derivation") {
		t.Errorf("the refusal must say the mapping is a decision, not a derivation: %v", err)
	}
}

// TestTheAnnexure5StateNameFollowsTheCategory — conditional in BOTH directions,
// which is the shape of 0070's state_name_follows_category CHECK.
func TestTheAnnexure5StateNameFollowsTheCategory(t *testing.T) {
	// Forbidden for K (Company).
	p := validTDSProfile()
	p.StateNameCode = "19"
	err := checkProfile(p)
	if !errors.Is(err, ErrInvalidTDSProfile) || !strings.Contains(err.Error(), "State-Government") {
		t.Fatalf("a state name on a Company deductor must be refused, got %v", err)
	}

	// Mandatory for S (State Government).
	p2 := validTDSProfile()
	p2.Category = "S"
	err = checkProfile(p2)
	if !errors.Is(err, ErrInvalidTDSProfile) || !strings.Contains(err.Error(), "Annexure 5") {
		t.Fatalf("a State Government deductor with no state name must be refused, got %v", err)
	}
	p2.StateNameCode = "19"
	if err := checkProfile(p2); err != nil {
		t.Fatalf("State Government with an Annexure 5 code: %v", err)
	}
}

// TestTheAnnexure3MinistryFollowsTheCategory — mandatory for A, D, G, optional
// for E, H, L, N, forbidden for the other nine, and field 58 exists exactly
// when the code is 99.
func TestTheAnnexure3MinistryFollowsTheCategory(t *testing.T) {
	// Forbidden for K.
	p := validTDSProfile()
	p.MinistryCode = "11"
	err := checkProfile(p)
	if !errors.Is(err, ErrInvalidTDSProfile) || !strings.Contains(err.Error(), "allows no ministry") {
		t.Fatalf("a ministry on a Company deductor must be refused, got %v", err)
	}

	// Mandatory for A (Central Government).
	p2 := validTDSProfile()
	p2.Category = "A"
	err = checkProfile(p2)
	if !errors.Is(err, ErrInvalidTDSProfile) || !strings.Contains(err.Error(), "Annexure 3 ministry") {
		t.Fatalf("Central Government with no ministry must be refused, got %v", err)
	}
	p2.MinistryCode = "99"
	err = checkProfile(p2)
	if !errors.Is(err, ErrInvalidTDSProfile) || !strings.Contains(err.Error(), "must name the ministry") {
		t.Fatalf("ministry code 99 with no name must be refused, got %v", err)
	}
	p2.MinistryOther = "Ministry of Sundry Affairs"
	if err := checkProfile(p2); err != nil {
		t.Fatalf("Central Government with ministry 99 and a name: %v", err)
	}

	// Named "other" against a code that is not 99 — the mirror of the same
	// equivalence, and the direction a screen produces by leaving a field behind.
	p3 := validTDSProfile()
	p3.Category = "A"
	p3.MinistryCode = "11"
	p3.MinistryOther = "Ministry of Sundry Affairs"
	err = checkProfile(p3)
	if !errors.Is(err, ErrInvalidTDSProfile) || !strings.Contains(err.Error(), "field 58 names") {
		t.Fatalf("want the field-58-without-99 refusal, got %v", err)
	}

	// Optional for E — accepted both ways. E also requires the Annexure 5 code.
	p4 := validTDSProfile()
	p4.Category = "E"
	p4.StateNameCode = "19"
	if err := checkProfile(p4); err != nil {
		t.Fatalf("statutory body (State Govt.) with no ministry is valid: %v", err)
	}
	p4.MinistryCode = "11"
	if err := checkProfile(p4); err != nil {
		t.Fatalf("statutory body (State Govt.) with a ministry is valid: %v", err)
	}
}

// TestOneEMailIsEnoughAndNoneIsNot — the format prints BH 28 and BH 42 both
// mandatory and then makes each conditional on the other.
func TestOneEMailIsEnoughAndNoneIsNot(t *testing.T) {
	p := validTDSProfile()
	p.Email = ""
	p.Responsible.Email = "radhika@khata.example"
	if err := checkProfile(p); err != nil {
		t.Fatalf("the responsible person's e-mail alone is enough: %v", err)
	}

	p.Responsible.Email = ""
	err := checkProfile(p)
	if !errors.Is(err, ErrInvalidTDSProfile) || !strings.Contains(err.Error(), "e-mail") {
		t.Fatalf("neither e-mail present must be refused, got %v", err)
	}
}

// TestANameOfOnlyPunctuationIsRefused — 0070's names_not_blank CHECK, which the
// format states as refusing a name that is only blanks, dots or specials.
func TestANameOfOnlyPunctuationIsRefused(t *testing.T) {
	for _, tc := range []struct {
		what  string
		apply func(*TDSProfileRecord)
	}{
		{"deductor name", func(p *TDSProfileRecord) { p.DeductorName = " . . " }},
		{"responsible name", func(p *TDSProfileRecord) { p.Responsible.Name = "---" }},
		{"designation", func(p *TDSProfileRecord) { p.Responsible.Designation = "" }},
	} {
		p := validTDSProfile()
		tc.apply(&p)
		err := checkProfile(p)
		if !errors.Is(err, ErrInvalidTDSProfile) || !strings.Contains(err.Error(), "no letter or digit") {
			t.Errorf("%s: want the blank-name refusal, got %v", tc.what, err)
		}
	}
}

// TestBothAddressBlocksAreChecked. The return numbers the deductor's address and
// the responsible person's address identically, and an operator fills two forms
// that look the same — so the sentence must say WHICH one is wrong.
func TestBothAddressBlocksAreChecked(t *testing.T) {
	p := validTDSProfile()
	p.Address.Line1 = "   "
	err := checkProfile(p)
	if !errors.Is(err, ErrInvalidTDSProfile) || !strings.Contains(err.Error(), "the deductor's address line 1") {
		t.Fatalf("want the deductor's address refused by name, got %v", err)
	}

	p2 := validTDSProfile()
	p2.Responsible.Address.Pincode = "40002"
	err = checkProfile(p2)
	if !errors.Is(err, ErrInvalidTDSProfile) ||
		!strings.Contains(err.Error(), "the responsible person's pincode") {
		t.Fatalf("want the responsible person's pincode refused by name, got %v", err)
	}

	p3 := validTDSProfile()
	p3.Address.Country = ""
	if err := checkProfile(p3); !errors.Is(err, ErrInvalidTDSProfile) {
		t.Fatalf("a missing country must be refused, got %v", err)
	}
}

// TestTheStateFieldIsACodeNotAName. "Maharashtra" in a two-digit CODE field is
// the mistake the column comment exists for.
func TestTheStateFieldIsACodeNotAName(t *testing.T) {
	p := validTDSProfile()
	p.Address.StateCode = "Maharashtra"
	err := checkProfile(p)
	if !errors.Is(err, ErrInvalidTDSProfile) || !strings.Contains(err.Error(), "not the name of the state") {
		t.Fatalf("want the code-not-a-name refusal, got %v", err)
	}
}

// TestAShapeValidStateCodeThatIsNotAStateIsACCEPTED — the DISCLOSED GAP, asserted
// so it is a decision and not a surprise.
//
// Migration 0070's header (lines 60-71) splits the work deliberately: the table
// checks SHAPE and internal/compliance/form140 owns the Annexure 1 LIST, because
// internal/masters may not import form140 (archtest boundaries_test.go:93) and a
// third copy of 37 codes would drift. There IS no code 08 — the workbook jumps
// from 07 to 09 — so "08" saves here and form140.validateDeductor refuses it at
// emission, naming Annexure 1.
//
// THIS TEST EXISTS TO STOP THE GAP BEING CLOSED BY ACCIDENT IN THE WRONG PLACE.
// If a later slice wants entry-time membership, the fix is either an archtest
// edge with a written reason or a served list — not a fourth copy of the codes
// pasted in here to make this test go away.
func TestAShapeValidStateCodeThatIsNotAStateIsACCEPTED(t *testing.T) {
	p := validTDSProfile()
	p.Address.StateCode = "08" // two digits, and not a state
	if err := checkProfile(p); err != nil {
		t.Fatalf("this layer checks the SHAPE only; form140 owns the list. Got %v", err)
	}
	// Same for the Annexure 5 code on a State-Government deductor.
	p2 := validTDSProfile()
	p2.Category = "S"
	p2.StateNameCode = "08"
	if err := checkProfile(p2); err != nil {
		t.Fatalf("Annexure 5 membership is form140's too. Got %v", err)
	}
}

// TestTheSplitContactIsTwoMandatoryFields — this format version separates the
// calling code from the number, so an empty country code is a mandatory field
// left blank rather than a formatting detail.
func TestTheSplitContactIsTwoMandatoryFields(t *testing.T) {
	for _, tc := range []struct {
		what  string
		apply func(*TDSProfileRecord)
	}{
		{"deductor country code", func(p *TDSProfileRecord) { p.ContactCountryCode = "" }},
		{"deductor number", func(p *TDSProfileRecord) { p.ContactNumber = "" }},
		{"responsible country code", func(p *TDSProfileRecord) { p.Responsible.ContactCountryCode = " " }},
		{"responsible number", func(p *TDSProfileRecord) { p.Responsible.ContactNumber = "" }},
	} {
		p := validTDSProfile()
		tc.apply(&p)
		if err := checkProfile(p); !errors.Is(err, ErrInvalidTDSProfile) {
			t.Errorf("%s: want a refusal, got %v", tc.what, err)
		}
	}
}

// TestDeductorCategoriesAreServedWithTheirRules. The client must not hold the
// sixteen letters, and it must not hold the conditional rules either — a screen
// that decides for itself when to show the Annexure 5 field is a fifth copy of
// a rule the migration already makes.
func TestDeductorCategoriesAreServedWithTheirRules(t *testing.T) {
	cats := DeductorCategories()
	if len(cats) != 16 {
		t.Fatalf("Annexure 4 has sixteen categories, got %d", len(cats))
	}
	byCode := map[string]TDSDeductorCategory{}
	for _, c := range cats {
		if c.Description == "" {
			t.Errorf("category %q has no description; a picker would show a bare letter", c.Code)
		}
		byCode[c.Code] = c
	}
	for _, code := range []string{"S", "E", "H", "N"} {
		if !byCode[code].StateNameCodeRequired {
			t.Errorf("%s is a State-Government category and requires the Annexure 5 code", code)
		}
	}
	if byCode["K"].StateNameCodeRequired {
		t.Error("K (Company) forbids the Annexure 5 code, so it must not be marked required")
	}
	for _, code := range []string{"A", "D", "G"} {
		if !byCode[code].MinistryRequired || !byCode[code].MinistryAllowed {
			t.Errorf("%s requires an Annexure 3 ministry", code)
		}
	}
	for _, code := range []string{"E", "H", "L", "N"} {
		if byCode[code].MinistryRequired || !byCode[code].MinistryAllowed {
			t.Errorf("%s allows a ministry but does not require one", code)
		}
	}
	if byCode["Q"].MinistryAllowed {
		t.Error("Q (Individual/HUF) may not carry a ministry at all")
	}
}

// ---------------------------------------------------------------------------
// The pins that read migration 0070 itself
// ---------------------------------------------------------------------------

// migration0070Text reads 0070 as the CONTRACT it is. A pin against the file runs
// with no database at all, so it cannot be silently skipped — which is the
// failure mode §1.3 of the handoff describes: a skip is indistinguishable from a
// pass in the exit code.
func migration0070Text(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "migrations", "0070_company_tds_profile.sql"))
	if err != nil {
		t.Fatalf("read migration 0070: %v", err)
	}
	return string(b)
}

// sqlQuotedLiterals returns every single-quoted token in a snippet of SQL.
func sqlQuotedLiterals(s string) []string {
	var out []string
	for i := 0; i < len(s); i++ {
		if s[i] != '\'' {
			continue
		}
		j := strings.IndexByte(s[i+1:], '\'')
		if j < 0 {
			break
		}
		out = append(out, s[i+1:i+1+j])
		i += 1 + j
	}
	return out
}

// sqlINLists returns the first n `IN (...)` lists that follow a named constraint.
func sqlINLists(t *testing.T, text, constraint string, n int) [][]string {
	t.Helper()
	at := strings.Index(text, constraint)
	if at < 0 {
		t.Fatalf("migration 0070 has no constraint named %s", constraint)
	}
	rest := text[at:]
	var out [][]string
	for len(out) < n {
		open := strings.Index(rest, "IN (")
		if open < 0 {
			t.Fatalf("%s: found %d of %d IN lists", constraint, len(out), n)
		}
		rest = rest[open+len("IN ("):]
		end := strings.Index(rest, ")")
		if end < 0 {
			t.Fatalf("%s: unterminated IN list", constraint)
		}
		out = append(out, sqlQuotedLiterals(rest[:end]))
		rest = rest[end:]
	}
	return out
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestDeductorCategoryCodesMatchMigration0070 pins the sixteen letters AND
// THEIR ORDER against the category_known CHECK.
//
// The order is asserted, not just the set, and that is deliberate: this slice
// claims the slice is "the order 0070 lists them, which is the workbook's
// printed order", and a picker that silently re-sorted into alphabetical order
// would present Annexure 4 in an order the workbook does not use.
func TestDeductorCategoryCodesMatchMigration0070(t *testing.T) {
	got := sqlINLists(t, migration0070Text(t), "company_tds_profile_category_known", 1)[0]
	if !sameStrings(got, deductorCategoryCodes) {
		t.Errorf("migration 0070's category_known CHECK lists %v; masters has %v.\n"+
			"A code in Go but not in the CHECK is a picker option that fails on save; a "+
			"code in the CHECK but not in Go is a category no screen can offer.",
			got, deductorCategoryCodes)
	}
	if len(deductorCategoryNames) != len(deductorCategoryCodes) {
		t.Errorf("%d codes but %d descriptions", len(deductorCategoryCodes), len(deductorCategoryNames))
	}
}

// TestCategoryConditionalRulesMatchMigration0070 pins the three cross-field
// rules — the ones that make this a whole-row UPSERT.
func TestCategoryConditionalRulesMatchMigration0070(t *testing.T) {
	text := migration0070Text(t)

	// state_name_follows_category names the same four twice: once for the
	// mandatory limb and once (NOT IN) for the forbidden one.
	stateLists := sqlINLists(t, text, "company_tds_profile_state_name_follows_category", 2)
	for i, got := range stateLists {
		for _, c := range got {
			if !stateNameCodeCategories[c] {
				t.Errorf("0070 limb %d names %q as a State-Government category; masters does not", i, c)
			}
		}
		if len(got) != len(stateNameCodeCategories) {
			t.Errorf("0070 limb %d lists %v; masters has %d categories",
				i, got, len(stateNameCodeCategories))
		}
	}

	// ministry_follows_category: mandatory, then optional, then the union in the
	// NOT IN limb.
	ministryLists := sqlINLists(t, text, "company_tds_profile_ministry_follows_category", 3)
	for _, c := range ministryLists[0] {
		if !ministryMandatoryCategories[c] {
			t.Errorf("0070 makes a ministry MANDATORY for %q; masters does not", c)
		}
	}
	if len(ministryLists[0]) != len(ministryMandatoryCategories) {
		t.Errorf("0070's mandatory limb is %v; masters has %d", ministryLists[0], len(ministryMandatoryCategories))
	}
	for _, c := range ministryLists[1] {
		if !ministryOptionalCategories[c] {
			t.Errorf("0070 makes a ministry OPTIONAL for %q; masters does not", c)
		}
	}
	if len(ministryLists[1]) != len(ministryOptionalCategories) {
		t.Errorf("0070's optional limb is %v; masters has %d", ministryLists[1], len(ministryOptionalCategories))
	}
	for _, c := range ministryLists[2] {
		if !ministryMandatoryCategories[c] && !ministryOptionalCategories[c] {
			t.Errorf("0070's forbidden limb excludes %q, which masters forbids a ministry for", c)
		}
	}

	// ministry_other_iff_99: the code the migration couples field 58 to.
	if !strings.Contains(text, "ministry_code = '"+ministryOther+"'") {
		t.Errorf("0070's ministry_other_iff_99 CHECK does not couple field 58 to code %s", ministryOther)
	}
}

// TestTheUpsertBindsEveryColumnOfTheTable is the pin that protects the WHOLE-ROW
// upsert from the next migration.
//
// A column added to company_tds_profile that this binding does not write is not
// a compile error and not a constraint violation: it is a field an operator
// fills in on a screen and that never reaches the row, or (worse) a value that
// SURVIVES a correction because the ON CONFLICT clause never mentions it. The
// column list is read out of the migration's CREATE TABLE so a later ALTER in a
// higher-numbered migration fails here by name.
func TestTheUpsertBindsEveryColumnOfTheTable(t *testing.T) {
	text := migration0070Text(t)
	start := strings.Index(text, "CREATE TABLE IF NOT EXISTS company_tds_profile (")
	if start < 0 {
		t.Fatal("migration 0070 does not create company_tds_profile")
	}
	body := text[start:]
	if end := strings.Index(body, "\n);"); end > 0 {
		body = body[:end]
	}

	declared := map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "--") || strings.HasPrefix(line, "CREATE") ||
			strings.HasPrefix(line, "CONSTRAINT") || strings.HasPrefix(line, "CHECK") ||
			strings.HasPrefix(line, "FOREIGN") || strings.HasPrefix(line, "(") ||
			strings.HasPrefix(line, "OR") || strings.HasPrefix(line, "AND") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		// A column declaration is `name TYPE ...`; the type is upper case.
		if name != strings.ToLower(name) || fields[1] != strings.ToUpper(fields[1]) {
			continue
		}
		declared[name] = true
	}
	if len(declared) < 30 {
		t.Fatalf("only parsed %d columns out of 0070; the parser, not the code, is wrong", len(declared))
	}

	bound := map[string]bool{}
	for _, c := range tdsProfileBinding("company", validTDSProfile(), "tester") {
		if bound[c.name] {
			t.Errorf("column %s is bound twice", c.name)
		}
		bound[c.name] = true
	}
	// recorded_at is the one column the database owns.
	bound["recorded_at"] = true

	for name := range declared {
		if !bound[name] {
			t.Errorf("company_tds_profile.%s is never written by SetTDSProfile — an operator "+
				"could fill it in and it would not reach the row, and a correction would "+
				"leave the old value behind", name)
		}
	}
	for name := range bound {
		if !declared[name] {
			t.Errorf("the upsert binds %q, which migration 0070 does not declare", name)
		}
	}
}

// TestTheUpsertRestatesEveryColumnItInserts. The conflict key must not assign to
// itself, recorded_at must be refreshed by the database, and every other bound
// column must appear in the SET list — the failure this shape exists to prevent.
func TestTheUpsertRestatesEveryColumnItInserts(t *testing.T) {
	cols := tdsProfileBinding("company", validTDSProfile(), "tester")
	sql, args := tdsProfileUpsertSQL(cols)

	if len(args) != len(cols) {
		t.Fatalf("%d columns but %d args", len(cols), len(args))
	}
	if !strings.Contains(sql, "ON CONFLICT (company_id) DO UPDATE SET ") {
		t.Fatalf("the upsert must conflict on company_id, the table's whole key:\n%s", sql)
	}
	if strings.Contains(sql, "company_id = EXCLUDED.company_id") {
		t.Error("the conflict key must not assign to itself")
	}
	if !strings.Contains(sql, "recorded_at = now()") {
		t.Error("recorded_at must be refreshed by the database on a correction")
	}
	for _, c := range cols {
		if c.name == "company_id" {
			continue
		}
		// ⚠️ ANCHORED ON A LEADING DELIMITER, AND IT MUST BE. The bare form
		// `c.name+" = EXCLUDED."+c.name` is a SUBSTRING of the responsible_*
		// assignment for thirteen of these columns — "pan = EXCLUDED.pan" sits
		// inside "responsible_pan = EXCLUDED.responsible_pan" — so dropping pan,
		// email, country, state_code, pincode, contact_number,
		// contact_country_code or address1..5 from the SET list left this test
		// GREEN, satisfied by the deductee-side entry. That is exactly the defect
		// this test's own message describes, inverted onto the deductor side.
		if !strings.Contains(sql, " "+c.name+" = EXCLUDED."+c.name) {
			t.Errorf("column %s is inserted but never updated: a correction would keep its "+
				"old value, which for ministry_code is a stale conditional the CHECK then "+
				"rejects, and for responsible_email is a return naming the wrong contact "+
				"with nothing failing at all", c.name)
		}
	}
	// The placeholders must be positional and contiguous, or an argument lands
	// in the wrong column.
	if !strings.Contains(sql, "VALUES ($1, $2,") ||
		!strings.Contains(sql, "$"+strconv.Itoa(len(cols))+")") {
		t.Errorf("placeholders are not $1..$%d:\n%s", len(cols), sql)
	}
}
