package masters

// THE DEDUCTOR RETURN PROFILE — WHO IS FILING. Migration 0070.
//
// company_tds_profile is the batch-header (BH) deductor block of the prescribed
// Form 140 return: the TAN, the deductor's own PAN, the Annexure 4 deductor
// category, the statutory address as the return NUMBERS it, and the person
// personally answerable for the deduction. Migration 0069 gave the challan
// register its schema and internal/reporting gave it a reader; nothing ever
// wrote THIS table, so on a real deployment the deductor block could only be
// keyed by raw SQL. Until a row exists, reporting.ErrNoDeductorProfile
// pre-empts every other refusal the return can make — migration 0070's header
// states it as a design rule: "THE ABSENCE OF A ROW IS THE REFUSAL".
//
// ---------------------------------------------------------------------------
// ⚠️ THERE ARE TWO DEDUCTOR CONCEPTS IN THIS PACKAGE AND THEY ARE NOT THE SAME
// ---------------------------------------------------------------------------
// deductor_status.go writes company_deductor_status (migration 0049): the
// s.402(11)/(37) ENTITY CATEGORY ("COMPANY", "INDIVIDUAL", …), keyed per TAX
// YEAR, answering "must this company deduct at all". THIS file writes
// company_tds_profile (migration 0070): the Annexure 4 DEDUCTOR CATEGORY —
// sixteen single letters — keyed per COMPANY, answering "who is filing this
// return, and what letter goes in BH field 32".
//
// NEITHER IS DERIVED FROM THE OTHER, AND NOTHING HERE MAY DERIVE IT. Migration
// 0070 (lines 88-89 and 165-172) and form140's DeductorCategories say why in
// the same words: the vocabularies diverge concretely. This list splits
// Government into Central (A) and State (S) and carries "Branch / Division of
// Company" (M), neither of which the statute knows; the statute has one
// "individual or HUF" limb where the annexure has a single code Q for both.
// "Mapping between them is a DECISION, not a derivation." A convenience helper
// here that turned compliance.EntityCompany into "K" would be that decision
// made by accident, in the one file nobody would look in for it — so the two
// are keyed separately, validated separately, and an operator states both.
//
// ---------------------------------------------------------------------------
// WHOLE-ROW UPSERT, NEVER A PARTIAL PATCH — and that is forced by the schema
// ---------------------------------------------------------------------------
// Migration 0070 carries three CROSS-FIELD CHECKs:
//
//	state_name_follows_category   — the Annexure 5 state code is MANDATORY for
//	                                S, E, H, N and FORBIDDEN for every other
//	                                category (conditional in both directions).
//	ministry_follows_category     — the Annexure 3 ministry code is MANDATORY
//	                                for A, D, G, optional for E, H, L, N and
//	                                FORBIDDEN outside those seven.
//	ministry_other_iff_99         — field 58 names the ministry exactly when
//	                                field 57 is 99 ("Other").
//
// A PATCH that changed the category alone would leave the conditional fields as
// they were and violate all three; a PATCH that changed a conditional field
// alone would need to know the stored category to be checkable. So this layer
// takes the WHOLE row and UPSERTs it on company_id, the same shape
// SetDeductorStatus has on its own key. There is exactly one profile per
// company, and re-stating it CORRECTS it.
//
// ---------------------------------------------------------------------------
// IT IS CURRENT STATE, NOT HISTORY, AND THE CONSEQUENCE IS DISCLOSED
// ---------------------------------------------------------------------------
// Migration 0070 lines 37-50: a change OVERWRITES rather than appends, because
// the return asks who is responsible for deduction NOW, at the moment of filing
// — a Q1 return filed late in Q3 is signed by whoever is responsible in Q3.
//
// THE CONSEQUENCE, CARRIED FORWARD SO IT STAYS A DECISION AND NOT A DISCOVERY:
// re-emitting a return for an OLD quarter after the responsible person changes
// produces a file naming the NEW person. That is correct for a fresh filing and
// wrong as a reproduction of what was filed. The remedy is NOT to key this
// table by tax year — that is a schema change, it is not this layer's to make,
// and 0070 argues against it explicitly. If reproducing a past filing ever
// matters, the artefact to keep is the EMITTED FILE, not a history of this row.
//
// ---------------------------------------------------------------------------
// WHY THIS VALIDATES IN GO WHEN THE TABLE ALREADY HAS THE CHECKS
// ---------------------------------------------------------------------------
// Migration 0070's CHECK constraints are the authority and they stay. But a
// violation reaches an operator as "new row for relation company_tds_profile
// violates check constraint company_tds_profile_state_name_follows_category",
// which tells a person keying a form nothing about what to type. Every check
// below exists to produce a SENTENCE, names the constraint it pre-empts, and is
// pinned to it by the tests in tds_profile_pin_test.go. The constraints exist to
// be right even if this code is wrong.

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/platform/db"
)

// ErrInvalidTDSProfile is returned for a deductor profile this layer refuses to
// store. A sentinel so the HTTP layer answers 400 rather than 500.
var ErrInvalidTDSProfile = errors.New("masters: invalid TDS deductor profile")

// ErrNoTDSProfile is returned by TDSProfile when this company has no row.
//
// It is a SEPARATE sentinel from reporting.ErrNoDeductorProfile on purpose. That
// one means "a return cannot be built"; this one means "the form has not been
// filled in yet", which is the ordinary state of a screen an operator has just
// opened and must not be reported as a failure to file.
var ErrNoTDSProfile = errors.New("masters: no TDS deductor profile recorded")

// The shapes migration 0070 checks, one regexp per CHECK, transcribed from the
// constraint definitions above the column they guard.
//
// They are a SECOND statement of a rule the database already enforces, and that
// is the point (see the header): the CHECK stays the authority.
// TestTheConstraintIsStillTheAuthority drives a bad value straight past this
// layer and asserts Postgres still refuses it, so a regexp that drifted looser
// than its constraint cannot go unnoticed.
var (
	// company_tds_profile_tan_shape: four letters, five digits, a letter.
	tanShapeRe = regexp.MustCompile(`^[A-Z]{4}[0-9]{5}[A-Z]$`)
	// company_tds_profile_pan_shape / _responsible_pan_shape: five letters,
	// four digits, a letter.
	panShapeRe = regexp.MustCompile(`^[A-Z]{5}[0-9]{4}[A-Z]$`)
	// company_tds_profile_state_code_shape / _responsible_state_code_shape.
	twoDigitRe = regexp.MustCompile(`^[0-9]{2}$`)
	// company_tds_profile_pincode_shape / _responsible_pincode_shape.
	sixDigitRe = regexp.MustCompile(`^[0-9]{6}$`)
	// company_tds_profile_names_not_blank, expressed by the migration as "must
	// contain at least one alphanumeric".
	alphanumericRe = regexp.MustCompile(`[A-Za-z0-9]`)
)

// PANNotRequired is the literal the file format prescribes for a deductor not
// required to hold a PAN. Migration 0070's pan_shape CHECK allows it and its
// column comment calls it "a real value with a meaning, never a blank".
//
// It is allowed for the DEDUCTOR's PAN only. The responsible person is a named
// human and 0070's responsible_pan_shape CHECK has no such alternative.
const PANNotRequired = "PANNOTREQD"

// deductorCategoryCodes are Annexure 4's sixteen codes IN THE ORDER MIGRATION
// 0070's category_known CHECK lists them, which is the order form140's
// DeductorCategories literal lists them, which is the workbook's printed order.
// A Go map cannot hold an order, so the slice is what a picker renders from.
//
// ⚠️ THESE ARE NOT compliance.KnownEntityCategories. See the file header.
//
// This is the ONE closed vocabulary this file copies, and it is copied because
// the alternative is worse: internal/masters may not import
// internal/compliance/form140 (archtest boundaries_test.go:93), and a client
// that hard-coded the sixteen letters in TypeScript would be a copy nothing
// could pin. This copy IS pinned, in both directions, by
// TestDeductorCategoriesMatchTheMigration (against 0070's CHECK) and
// TestDeductorCategoriesMatchForm140 (against the annexure the file is emitted
// from), so a letter added on any one side fails the build.
var deductorCategoryCodes = []string{
	"A", "S", "D", "E", "G", "H", "L", "N",
	"K", "M", "P", "T", "J", "B", "Q", "F",
}

// deductorCategoryNames is the description beside each code, transcribed from
// form140's DeductorCategories (which transcribes the workbook's "Value for
// Category in database / FVU" column). Pinned to it verbatim by
// TestDeductorCategoriesMatchForm140 — an operator picking "Company" must get
// the letter the emitter will accept, and a description that drifted would be a
// different question being answered.
var deductorCategoryNames = map[string]string{
	"A": "Central Government",
	"S": "State Government",
	"D": "Statutory body (Central Govt.)",
	"E": "Statutory body (State Govt.)",
	"G": "Autonomous body (Central Govt.)",
	"H": "Autonomous body (State Govt.)",
	"L": "Local Authority (Central Govt.)",
	"N": "Local Authority (State Govt.)",
	"K": "Company",
	"M": "Branch / Division of Company",
	"P": "Association of Person (AOP)",
	"T": "Association of Person (Trust)",
	"J": "Artificial Juridical Person",
	"B": "Body of Individuals",
	"Q": "Individual/HUF",
	"F": "Firm",
}

// stateNameCodeCategories are the four State-Government categories for which
// the Annexure 5 state code is mandatory — and for which it is FORBIDDEN
// everywhere else. Mirrors 0070's state_name_follows_category CHECK; pinned by
// TestCategoryConditionalRulesMatchTheMigration.
var stateNameCodeCategories = map[string]bool{"S": true, "E": true, "H": true, "N": true}

// ministryMandatoryCategories are the three for which the Annexure 3 ministry
// code is mandatory, and ministryOptionalCategories the four more for which it
// is merely allowed. Outside those seven a ministry code is forbidden. Mirrors
// 0070's ministry_follows_category CHECK.
var (
	ministryMandatoryCategories = map[string]bool{"A": true, "D": true, "G": true}
	ministryOptionalCategories  = map[string]bool{"E": true, "H": true, "L": true, "N": true}
)

// ministryOther is Annexure 3's code for "Other", which field 58 exists to name.
const ministryOther = "99"

// TDSAddress is the statutory address as the RETURN numbers it: five separately
// numbered lines, a country/region, a two-digit Annexure 1 state CODE (never a
// name) and a pincode.
//
// It is not company.address and it is not ledger.address. Migration 0070's
// header explains why the address is re-stated rather than split out of the
// company's free-text blob: "Splitting a blob into 25-character lines is a
// guess, and a guess that silently truncates a deductor's address on a
// statutory filing."
type TDSAddress struct {
	// Line1 is mandatory; Line2..Line5 are optional and stored as '' rather
	// than NULL (the columns are NOT NULL DEFAULT '').
	Line1 string
	Line2 string
	Line3 string
	Line4 string
	Line5 string
	// Country is MANDATORY on this path.
	//
	// ⚠️ migration 0070 declares a column DEFAULT of 'India' (and '91' for the two
	// contact country codes) and THIS API CAN NEVER REACH IT: the write is a
	// whole-row upsert, so every column is always bound, and validateTDSProfile
	// refuses the empty string. The comment here used to say it "defaults to India
	// in the column", describing behaviour this path forbids.
	//
	// Left mandatory ON PURPOSE rather than defaulted in normalizeTDSProfile. The
	// country lands in the Batch Header of a filed return, and a value this
	// software chose is a value the deductor never stated — the same argument that
	// forbids defaulting the minor head. The client sends it; the form can prefill
	// it for a human to see and accept.
	Country string
	// StateCode is the two-digit Annexure 1 CODE. See the note on
	// SetTDSProfile: this layer checks the SHAPE, form140 owns the LIST.
	StateCode string
	Pincode   string
}

// TDSResponsiblePerson is the human named on the return as answerable for the
// deduction. Their PAN is their OWN, a different field from the deductor's, and
// it is personal data — which is why the profile is not readable by the seeded
// Data Entry level.
type TDSResponsiblePerson struct {
	Name        string
	Designation string
	PAN         string
	Address     TDSAddress
	// Email may be empty IF the deductor's e-mail is present. The format prints
	// both mandatory and then makes each conditional on the other, so ONE is
	// enough and NONE is not (0070's an_email_is_present CHECK).
	Email string
	// ContactCountryCode is split from the number by this format version.
	ContactCountryCode string
	ContactNumber      string
}

// TDSProfileRecord is the whole company_tds_profile row. There are no money
// fields in it at all — it is identity, address and contact — so nothing here
// is a decimal and nothing here may become a float.
type TDSProfileRecord struct {
	// TAN is the Tax Deduction and Collection Account Number, AAAA99999A. It is
	// NOT the GSTIN and is not derivable from it.
	TAN string
	// PAN is the DEDUCTOR's own PAN, or the literal PANNOTREQD.
	PAN string
	// DeductorName is the name held against the TAN, which is a distinct fact
	// from the company's display name: General Note 13 requires it to match the
	// TAN name in the .csi file downloaded from TIN, and a difference of
	// punctuation alone fails validation.
	DeductorName string
	// Category is an Annexure 4 code — one of DeductorCategories(). NOT a
	// compliance.Entity* category.
	Category string

	Address TDSAddress
	// Email may be empty IF Responsible.Email is present; see the pair rule.
	Email              string
	ContactCountryCode string
	ContactNumber      string

	// StateNameCode is the Annexure 5 code, mandatory for S, E, H, N and empty
	// for every other category.
	StateNameCode string
	// MinistryCode is the Annexure 3 code; MinistryOther names it when the code
	// is 99.
	MinistryCode  string
	MinistryOther string
	// AIN is the Account Office Identification Number — Government deductors
	// paying by book entry only.
	AIN string
	// GSTIN is optional here even though `company` holds one, because the
	// return's copy is the one that was filed and a company may file TDS
	// returns without being GST-registered at all.
	GSTIN string

	Responsible TDSResponsiblePerson

	// RecordedBy and RecordedAt are the audit trail. RecordedAt is set by the
	// database and ignored on write.
	RecordedBy string
	RecordedAt string
}

// TDSDeductorCategory is one Annexure 4 code served to a client, with the two
// conditional rules that depend on it.
//
// The RULES travel with the list deliberately. A screen has to know that
// choosing "State Government" makes the Annexure 5 state code mandatory and
// that choosing "Company" FORBIDS it — and if the client derives that from its
// own copy of the four letters, that copy is a fifth statement of a rule the
// migration, this file and form140 already make.
type TDSDeductorCategory struct {
	// Code is the single letter that goes in BH field 32.
	Code string
	// Description is the workbook's own wording for it.
	Description string
	// StateNameCodeRequired is true for S, E, H, N — for which the Annexure 5
	// state code is mandatory. For every other code it must be EMPTY, so this
	// flag is both a "show the field" and a "clear the field" signal.
	StateNameCodeRequired bool
	// MinistryRequired is true for A, D, G. MinistryAllowed is true for those
	// three plus E, H, L, N; outside them a ministry code is forbidden.
	MinistryRequired bool
	MinistryAllowed  bool
}

// DeductorCategories returns Annexure 4's sixteen codes in the workbook's
// printed order, with the category-conditional rules attached.
//
// SERVED, NEVER HARD-CODED IN THE CLIENT — the same call handler_deductor_status.go
// makes for the entity categories, and for a sharper reason here: a client copy
// of these letters could offer a code the CHECK refuses on save, or (worse) omit
// one and make a real deductor category unreachable. Serving it means the list
// has exactly the copies that are mechanically pinned to each other.
func DeductorCategories() []TDSDeductorCategory {
	out := make([]TDSDeductorCategory, 0, len(deductorCategoryCodes))
	for _, c := range deductorCategoryCodes {
		out = append(out, TDSDeductorCategory{
			Code:                  c,
			Description:           deductorCategoryNames[c],
			StateNameCodeRequired: stateNameCodeCategories[c],
			MinistryRequired:      ministryMandatoryCategories[c],
			MinistryAllowed:       ministryMandatoryCategories[c] || ministryOptionalCategories[c],
		})
	}
	return out
}

// IsKnownDeductorCategory reports whether code is one of Annexure 4's sixteen.
func IsKnownDeductorCategory(code string) bool {
	_, ok := deductorCategoryNames[code]
	return ok
}

// normalizeTDSProfile trims every field and case-folds the three statutory
// identifiers.
//
// CASE-FOLDING IS NORMALISATION HERE AND NOT A GUESS: a TAN, a PAN and a GSTIN
// are defined in capitals — 0070's shape CHECKs accept nothing else, and
// form140 refuses a TAN that is not equal to its own upper-case form. Storing
// "aaaa12345b" so the database can reject it teaches an operator nothing. What
// is NOT normalised is anything with meaning in its punctuation: the deductor
// name must match the TAN name in the .csi file, so it is trimmed and otherwise
// left exactly as keyed.
func normalizeTDSProfile(in TDSProfileRecord) TDSProfileRecord {
	up := func(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }
	tr := strings.TrimSpace

	addr := func(a TDSAddress) TDSAddress {
		return TDSAddress{
			Line1: tr(a.Line1), Line2: tr(a.Line2), Line3: tr(a.Line3),
			Line4: tr(a.Line4), Line5: tr(a.Line5),
			Country: tr(a.Country), StateCode: tr(a.StateCode), Pincode: tr(a.Pincode),
		}
	}

	return TDSProfileRecord{
		TAN:                up(in.TAN),
		PAN:                up(in.PAN),
		DeductorName:       tr(in.DeductorName),
		Category:           up(in.Category),
		Address:            addr(in.Address),
		Email:              tr(in.Email),
		ContactCountryCode: tr(in.ContactCountryCode),
		ContactNumber:      tr(in.ContactNumber),
		StateNameCode:      tr(in.StateNameCode),
		MinistryCode:       tr(in.MinistryCode),
		MinistryOther:      tr(in.MinistryOther),
		AIN:                tr(in.AIN),
		GSTIN:              up(in.GSTIN),
		Responsible: TDSResponsiblePerson{
			Name:               tr(in.Responsible.Name),
			Designation:        tr(in.Responsible.Designation),
			PAN:                up(in.Responsible.PAN),
			Address:            addr(in.Responsible.Address),
			Email:              tr(in.Responsible.Email),
			ContactCountryCode: tr(in.Responsible.ContactCountryCode),
			ContactNumber:      tr(in.Responsible.ContactNumber),
		},
	}
}

// validateTDSProfile produces the sentence an operator can act on, one per CHECK
// migration 0070 carries. It takes an ALREADY NORMALISED record.
func validateTDSProfile(in TDSProfileRecord) error {
	// ---- Identity ---------------------------------------------------------
	//
	// THE TAN IS NOT THE GSTIN, and this is the first thing an operator gets
	// wrong: `company` holds a GSTIN, holds no TAN, and the two have different
	// shapes and different issuing processes. Pre-empts
	// company_tds_profile_tan_shape.
	if !tanShapeRe.MatchString(in.TAN) {
		return fmt.Errorf("%w: %q is not a TAN. A Tax Deduction and Collection Account "+
			"Number is four letters, five digits and a letter (AAAA99999A) — it is NOT "+
			"the GSTIN and cannot be derived from it, and a wrong one is rejected against "+
			"the .csi challan file after the operator believes they have filed",
			ErrInvalidTDSProfile, in.TAN)
	}
	// Pre-empts company_tds_profile_pan_shape, which allows the format's own
	// literal as well as a real PAN.
	if !panShapeRe.MatchString(in.PAN) && in.PAN != PANNotRequired {
		return fmt.Errorf("%w: the deductor's PAN %q is not five letters, four digits and a "+
			"letter (AAAAA9999A). Where a deductor is not required to hold one the format "+
			"prescribes the literal %s — a real value with a meaning, never a blank",
			ErrInvalidTDSProfile, in.PAN, PANNotRequired)
	}
	// Pre-empts company_tds_profile_responsible_pan_shape. Note there is NO
	// PANNOTREQD alternative here: the responsible person is a named human and
	// the constraint gives them no exemption.
	if !panShapeRe.MatchString(in.Responsible.PAN) {
		return fmt.Errorf("%w: the responsible person's own PAN %q is not five letters, four "+
			"digits and a letter. It is a DIFFERENT field from the deductor's PAN (BH field "+
			"59 against field 15) and %s is not accepted for it",
			ErrInvalidTDSProfile, in.Responsible.PAN, PANNotRequired)
	}
	// Pre-empts company_tds_profile_category_known.
	if !IsKnownDeductorCategory(in.Category) {
		return fmt.Errorf("%w: %q is not one of Annexure 4's sixteen deductor category codes "+
			"(%s). It is a single letter that goes in BH field 32, and it is NOT the "+
			"s.402(11) entity category recorded against the tax year — mapping between the "+
			"two is a decision, not a derivation",
			ErrInvalidTDSProfile, in.Category, strings.Join(deductorCategoryCodes, ", "))
	}
	// Pre-empts company_tds_profile_names_not_blank, which the format states as
	// "only blank values or only special characters or only dots" being refused.
	for _, f := range []struct{ what, value string }{
		{"the deductor's name (BH field 19)", in.DeductorName},
		{"the responsible person's name (BH field 33)", in.Responsible.Name},
		{"the responsible person's designation (BH field 34)", in.Responsible.Designation},
	} {
		if !alphanumericRe.MatchString(f.value) {
			return fmt.Errorf("%w: %s is %q, which contains no letter or digit. The format "+
				"refuses a name that is only blanks, dots or special characters",
				ErrInvalidTDSProfile, f.what, f.value)
		}
	}
	// The deductor name is a separate fact from company.name and this is the
	// place to say so, because the temptation is to leave it blank and let the
	// company's name do: it must match the TAN name in the .csi file downloaded
	// from TIN (General Note 13), where a difference of punctuation alone fails
	// validation.

	// ---- Address, both blocks ---------------------------------------------
	//
	// Pre-empts company_tds_profile_address_present and the two state-code and
	// pincode shape CHECKs. Both blocks are checked by the same code because
	// the return numbers them identically and a divergence between the two
	// messages would be a divergence nobody chose.
	if err := validateTDSAddress("the deductor", in.Address); err != nil {
		return err
	}
	if err := validateTDSAddress("the responsible person", in.Responsible.Address); err != nil {
		return err
	}

	// ---- Contact ----------------------------------------------------------
	//
	// Pre-empts company_tds_profile_contacts_present. The country code is a
	// separate field from the number in this format version, so a blank one is
	// not a detail — it is a mandatory field left empty.
	for _, f := range []struct{ what, value string }{
		{"the deductor's contact country code (BH field 29)", in.ContactCountryCode},
		{"the deductor's contact number (BH field 30)", in.ContactNumber},
		{"the responsible person's contact country code (BH field 44)", in.Responsible.ContactCountryCode},
		{"the responsible person's contact number (BH field 45)", in.Responsible.ContactNumber},
	} {
		if f.value == "" {
			return fmt.Errorf("%w: %s is mandatory; this format version splits the calling "+
				"code from the number, so both are required", ErrInvalidTDSProfile, f.what)
		}
	}
	// Pre-empts company_tds_profile_an_email_is_present: the format prints both
	// e-mail fields mandatory and then makes each conditional on the other, so
	// ONE is enough and NONE is not.
	if in.Email == "" && in.Responsible.Email == "" {
		return fmt.Errorf("%w: an e-mail address is required — the format asks for the "+
			"deductor's (BH field 28) OR the responsible person's (BH field 42), each "+
			"mandatory only if the other is absent, and neither was given",
			ErrInvalidTDSProfile)
	}

	// ---- The three category-conditional rules -----------------------------
	//
	// These are the reason this layer takes the WHOLE row: each is a rule
	// BETWEEN the category and another field, so none of them is checkable
	// against a partial update.
	//
	// Pre-empts company_tds_profile_state_name_follows_category, which is
	// conditional in BOTH directions — which is why emptiness is tested as well
	// as presence.
	if stateNameCodeCategories[in.Category] {
		if !twoDigitRe.MatchString(in.StateNameCode) {
			return fmt.Errorf("%w: deductor category %s (%s) requires the Annexure 5 state "+
				"code in BH field 54, and %q is not two digits",
				ErrInvalidTDSProfile, in.Category, deductorCategoryNames[in.Category],
				in.StateNameCode)
		}
	} else if in.StateNameCode != "" {
		return fmt.Errorf("%w: BH field 54 (State Name) carries %q, but the format allows a "+
			"value only for the four State-Government categories S, E, H and N — not %s "+
			"(%s). Clear it rather than choosing a code that will be refused",
			ErrInvalidTDSProfile, in.StateNameCode, in.Category,
			deductorCategoryNames[in.Category])
	}
	// Pre-empts company_tds_profile_ministry_follows_category: mandatory for
	// A, D, G; optional for E, H, L, N; forbidden for the other nine.
	switch {
	case ministryMandatoryCategories[in.Category]:
		if in.MinistryCode == "" {
			return fmt.Errorf("%w: deductor category %s (%s) requires an Annexure 3 ministry "+
				"code in BH field 57", ErrInvalidTDSProfile, in.Category,
				deductorCategoryNames[in.Category])
		}
	case !ministryOptionalCategories[in.Category] && in.MinistryCode != "":
		return fmt.Errorf("%w: BH field 57 (Ministry Name) carries %q, but the format allows "+
			"no ministry for deductor category %s (%s)", ErrInvalidTDSProfile,
			in.MinistryCode, in.Category, deductorCategoryNames[in.Category])
	}
	// Pre-empts company_tds_profile_ministry_other_iff_99, an equivalence: the
	// code is 99 exactly when field 58 names the ministry.
	switch {
	case in.MinistryCode == ministryOther && in.MinistryOther == "":
		return fmt.Errorf("%w: ministry code %s is \"Other\", so BH field 58 must name the "+
			"ministry", ErrInvalidTDSProfile, ministryOther)
	case in.MinistryCode != ministryOther && in.MinistryOther != "":
		return fmt.Errorf("%w: BH field 58 names the ministry %q, but field 57 is %q rather "+
			"than the code %s (\"Other\") that field 58 exists to expand",
			ErrInvalidTDSProfile, in.MinistryOther, in.MinistryCode, ministryOther)
	}
	return nil
}

// validateTDSAddress checks one address block. `whose` names the block so the
// sentence says which of the two identical-looking forms is wrong.
func validateTDSAddress(whose string, a TDSAddress) error {
	if a.Line1 == "" {
		return fmt.Errorf("%w: %s's address line 1 is mandatory. The return wants five "+
			"separately numbered lines rather than one block of text, and line 1 is the "+
			"flat/door/block number", ErrInvalidTDSProfile, whose)
	}
	if a.Country == "" {
		return fmt.Errorf("%w: %s's country/region is mandatory", ErrInvalidTDSProfile, whose)
	}
	// Pre-empts the two state_code_shape CHECKs. THE FIELD IS A CODE, NOT A
	// NAME — see the gap stated on SetTDSProfile: this is a shape check only.
	if !twoDigitRe.MatchString(a.StateCode) {
		return fmt.Errorf("%w: %s's state code %q is not two digits. The field carries the "+
			"two-digit Annexure 1 CODE, not the name of the state",
			ErrInvalidTDSProfile, whose, a.StateCode)
	}
	if !sixDigitRe.MatchString(a.Pincode) {
		return fmt.Errorf("%w: %s's pincode %q is not six digits",
			ErrInvalidTDSProfile, whose, a.Pincode)
	}
	return nil
}

// tdsProfileColumn is one bound column of the whole-row UPSERT: the column name
// and its value in the same place.
//
// The binding is a list rather than a hand-written INSERT because a whole-row
// upsert of 36 columns has one specific failure mode: a column present in the
// INSERT list and forgotten in the ON CONFLICT SET list keeps its OLD value
// through an update. For ministry_code that is a stale value the CHECK then
// rejects with a constraint name; for responsible_email it is a return naming
// the wrong person's address and nothing failing at all. Deriving the column
// list, the placeholders and the SET clause from ONE list makes that
// unexpressible.
type tdsProfileColumn struct {
	name  string
	value any
}

// tdsProfileBinding maps a record onto every column of company_tds_profile
// except recorded_at, which the database sets.
//
// Order matches migration 0070's column order so the two read side by side.
func tdsProfileBinding(companyID any, in TDSProfileRecord, recordedBy string) []tdsProfileColumn {
	r := in.Responsible
	return []tdsProfileColumn{
		{"company_id", companyID},
		{"tan", in.TAN},
		{"pan", in.PAN},
		{"deductor_name", in.DeductorName},
		{"deductor_category", in.Category},
		{"address1", in.Address.Line1},
		{"address2", in.Address.Line2},
		{"address3", in.Address.Line3},
		{"address4", in.Address.Line4},
		{"address5", in.Address.Line5},
		{"country", in.Address.Country},
		{"state_code", in.Address.StateCode},
		{"pincode", in.Address.Pincode},
		{"email", in.Email},
		{"contact_country_code", in.ContactCountryCode},
		{"contact_number", in.ContactNumber},
		{"state_name_code", in.StateNameCode},
		{"ministry_code", in.MinistryCode},
		{"ministry_other", in.MinistryOther},
		{"ain", in.AIN},
		{"gstin", in.GSTIN},
		{"responsible_name", r.Name},
		{"responsible_designation", r.Designation},
		{"responsible_pan", r.PAN},
		{"responsible_address1", r.Address.Line1},
		{"responsible_address2", r.Address.Line2},
		{"responsible_address3", r.Address.Line3},
		{"responsible_address4", r.Address.Line4},
		{"responsible_address5", r.Address.Line5},
		{"responsible_country", r.Address.Country},
		{"responsible_state_code", r.Address.StateCode},
		{"responsible_pincode", r.Address.Pincode},
		{"responsible_email", r.Email},
		{"responsible_contact_country_code", r.ContactCountryCode},
		{"responsible_contact_number", r.ContactNumber},
		{"recorded_by", recordedBy},
	}
}

// tdsProfileUpsertSQL renders the whole-row UPSERT for a binding. company_id is
// the conflict key and never assigns to itself; recorded_at is always now().
func tdsProfileUpsertSQL(cols []tdsProfileColumn) (string, []any) {
	names := make([]string, 0, len(cols))
	holders := make([]string, 0, len(cols))
	sets := make([]string, 0, len(cols))
	args := make([]any, 0, len(cols))
	for i, c := range cols {
		names = append(names, c.name)
		holders = append(holders, "$"+strconv.Itoa(i+1))
		args = append(args, c.value)
		if c.name == "company_id" {
			continue
		}
		sets = append(sets, c.name+" = EXCLUDED."+c.name)
	}
	sets = append(sets, "recorded_at = now()")
	return "INSERT INTO company_tds_profile (" + strings.Join(names, ", ") + ")\n" +
		"VALUES (" + strings.Join(holders, ", ") + ")\n" +
		"ON CONFLICT (company_id) DO UPDATE SET " + strings.Join(sets, ", "), args
}

// SetTDSProfile records (or corrects) this company's deductor return profile.
//
// It is a WHOLE-ROW UPSERT on company_id — never a partial patch. See the file
// header: 0070's three category-conditional CHECKs are rules BETWEEN fields, so
// a partial update is not checkable and can leave a stale conditional value
// behind. There is one profile per company and re-stating it corrects it.
//
// IT OVERWRITES RATHER THAN APPENDS, and the disclosed consequence is that
// re-emitting a return for an OLD quarter after the responsible person changes
// produces a file naming the NEW person (0070 lines 46-50). Correct for a fresh
// filing, wrong as a reproduction of one. Keep the emitted file if that matters.
//
// ---------------------------------------------------------------------------
// ⚠️ WHAT A SUCCESSFUL SAVE DOES *NOT* PROMISE
// ---------------------------------------------------------------------------
// THIS CALL CHECKS SHAPES AND CROSS-FIELD RULES. IT DOES NOT CERTIFY THAT THE
// RETURN WILL BUILD. Three closed vocabularies live in
// internal/compliance/form140, which internal/masters MAY NOT IMPORT (archtest
// boundaries_test.go:93 — form140 is a pure leaf whose only permitted consumer
// is internal/reporting), and migration 0070's header (lines 60-71) resolves the
// split deliberately: the table checks SHAPE, form140 owns the LISTS.
//
// So the following save cleanly here and are refused later, at emission, by
// form140.validateDeductor — BY NAME, with the annexure quoted:
//
//   - A two-digit state_code or responsible_state_code that is not one of
//     Annexure 1's 37 codes. There is no code 08, for instance: the workbook
//     jumps from 07 to 09, so "08" passes this call and cannot be filed.
//   - A two-digit state_name_code that is not an Annexure 5 code.
//   - Any field longer than the layout's size for it (BH 19 is 75 characters,
//     each address line 25). form140 refuses it naming the field and the size.
//
// AND ONE THAT NOTHING REFUSES AT ALL, stated because it is the sharp edge:
// ministry_code has no shape CHECK in 0070 and no membership check in form140
// (Annexure 3's list is not transcribed anywhere in this repo), so a nonsense
// ministry code reaches the emitted file. Same for ain and gstin. Whoever
// transcribes Annexure 3 should add the membership check to form140 — beside
// StateCodes and DeductorCategories, where the other closed lists live — and
// NOT here, because a fourth copy of a closed vocabulary is what 0070's header
// warns against.
//
// The consequence for a SCREEN is one sentence: it must never tell an operator a
// code is valid. "Saved" is all this call earns; whether a return can be built
// is what the TDS register's return_file status answers, and it surfaces
// form140's refusal verbatim.
func (s *Service) SetTDSProfile(ctx context.Context, in TDSProfileRecord, recordedBy string) error {
	norm := normalizeTDSProfile(in)
	if err := validateTDSProfile(norm); err != nil {
		return err
	}
	if strings.TrimSpace(recordedBy) == "" {
		return fmt.Errorf("%w: recorded_by is required", ErrInvalidTDSProfile)
	}
	// The company comes from the CONTEXT and never from a URL path: RLS is
	// ENABLE + FORCE on this table and db.Pool.RunInTx sets
	// app.current_company_id as the transaction's first statement, so a
	// company_id built from anywhere else would be refused by the policy — or,
	// if it happened to match, would be a tenancy decision made by a router.
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return db.ErrNoCompany
	}
	// company_tds_profile has NO id column at all: the key IS the company
	// (0070 line 78), so unlike tds_challan there is no UUID to generate here.
	sql, args := tdsProfileUpsertSQL(tdsProfileBinding(companyID, norm, strings.TrimSpace(recordedBy)))
	return s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// No WHERE company_id anywhere: RLS supplies tenancy through the GUC,
		// and a hand-written predicate here would be a second, weaker copy of
		// the policy.
		if _, err := tx.Exec(ctx, sql, args...); err != nil {
			return fmt.Errorf("masters: upsert tds deductor profile: %w", err)
		}
		return nil
	})
}

// TDSProfile returns this company's deductor profile, or ErrNoTDSProfile when
// none has been recorded.
//
// THE ABSENCE OF A ROW IS NOT AN ERROR CONDITION HERE, it is the ordinary state
// of a company that has not filled the form in — which is why it comes back as
// its own sentinel rather than an empty record. An empty record would be
// indistinguishable from a profile whose every field an operator had blanked,
// and it would let a screen render a form that looks saved.
func (s *Service) TDSProfile(ctx context.Context) (TDSProfileRecord, error) {
	if _, ok := db.CompanyFromContext(ctx); !ok {
		return TDSProfileRecord{}, db.ErrNoCompany
	}
	var out TDSProfileRecord
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		r := &out.Responsible
		// One row per company and RLS confines the read to it, so no WHERE and
		// no LIMIT: a second row is not expressible (company_id is the primary
		// key) and a WHERE would be a duplicate of the policy.
		err := tx.QueryRow(ctx,
			`SELECT tan, pan, deductor_name, deductor_category,
			        address1, address2, address3, address4, address5, country,
			        state_code, pincode, email, contact_country_code, contact_number,
			        state_name_code, ministry_code, ministry_other, ain, gstin,
			        responsible_name, responsible_designation, responsible_pan,
			        responsible_address1, responsible_address2, responsible_address3,
			        responsible_address4, responsible_address5, responsible_country,
			        responsible_state_code, responsible_pincode, responsible_email,
			        responsible_contact_country_code, responsible_contact_number,
			        recorded_by,
			        to_char(recorded_at, 'YYYY-MM-DD"T"HH24:MI:SSOF:00')
			   FROM company_tds_profile`).
			Scan(&out.TAN, &out.PAN, &out.DeductorName, &out.Category,
				&out.Address.Line1, &out.Address.Line2, &out.Address.Line3,
				&out.Address.Line4, &out.Address.Line5, &out.Address.Country,
				&out.Address.StateCode, &out.Address.Pincode,
				&out.Email, &out.ContactCountryCode, &out.ContactNumber,
				&out.StateNameCode, &out.MinistryCode, &out.MinistryOther,
				&out.AIN, &out.GSTIN,
				&r.Name, &r.Designation, &r.PAN,
				&r.Address.Line1, &r.Address.Line2, &r.Address.Line3,
				&r.Address.Line4, &r.Address.Line5, &r.Address.Country,
				&r.Address.StateCode, &r.Address.Pincode, &r.Email,
				&r.ContactCountryCode, &r.ContactNumber,
				&out.RecordedBy, &out.RecordedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoTDSProfile
		}
		if err != nil {
			return fmt.Errorf("masters: read tds deductor profile: %w", err)
		}
		return nil
	})
	if err != nil {
		return TDSProfileRecord{}, err
	}
	return out, nil
}
