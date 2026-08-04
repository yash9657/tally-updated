package server

// HTTP surface for the TDS DEDUCTOR RETURN PROFILE — T6, migration 0070.
//
//	GET .../tds-profile  -> Display / tds-profile
//	PUT .../tds-profile  -> Alter   / tds-profile
//
// This is the route whose absence made the return unbuildable on any real
// deployment: reporting's sourcing layer refuses a return with
// ErrNoDeductorProfile before it looks at anything else, and until this existed
// the only way to record the deductor block was raw SQL (which is exactly how
// the tests seeded it).
//
// ---------------------------------------------------------------------------
// PUT WITH NO KEY IN THE PATH, AND A WHOLE ROW IN THE BODY
// ---------------------------------------------------------------------------
// The company IS the key (company_tds_profile.company_id is its whole primary
// key) and the company already comes from the path prefix and the tenant
// context, so there is nothing left to name. PUT rather than PATCH is not a
// stylistic choice: migration 0070 carries three CROSS-FIELD CHECKs (the
// Annexure 5 state code and the Annexure 3 ministry are each mandatory for some
// deductor categories and FORBIDDEN for the rest), so a partial body is not
// checkable and can leave a stale conditional value behind. masters.SetTDSProfile
// takes the whole record for the same reason.
//
// ---------------------------------------------------------------------------
// ONE RESOURCE TOKEN FOR THE READ AND THE WRITE — and here that is the SAFE
// call rather than merely the tidy one
// ---------------------------------------------------------------------------
// The row carries a NAMED INDIVIDUAL'S PAN, so the read is sensitive and the
// bank-statement precedent (a sensitive read modelled as `report:NAME`, the
// write on its own token) is the obvious shape to reach for. IT IS THE WRONG ONE
// HERE, and the reason is migration 0011: internal/authz matches a bare `report`
// rule against ANY `report:NAME` at specificity 1, so the moment this became
// `report:tds-profile` it would be GRANTED to every seeded Data Entry level by a
// rule written years ago for a different purpose — and taking it back needs a
// 0068-style counterweight migration. As a plain resource token it is
// default-DENIED for Data Entry (default_access 'none', and no seeded rule names
// it), which is the authority this data should have, with no migration at all.
// `deductor-status` and `tcs-declaration` are refused for the same reason in the
// same way. TestTDSProfileAuthMatrix pins all three answers.
//
// ---------------------------------------------------------------------------
// ⚠️ WHAT THE GET SERVES BESIDES THE PROFILE, AND WHY IT MUST
// ---------------------------------------------------------------------------
// Annexure 4's sixteen deductor-category letters AND the two conditional rules
// that hang off them (which categories require the Annexure 5 state code; which
// require or forbid an Annexure 3 ministry). A client that hard-coded either
// would be a copy nothing can pin: it could offer a letter the CHECK refuses on
// save, or hide a field the return makes mandatory and then report a Postgres
// constraint name to the operator. masters serves both, pinned to migration 0070
// and to form140 by tests in that package.
//
// ⚠️ AND WHAT IT CANNOT SERVE: the Annexure 1 list of 37 STATE CODES. It lives in
// internal/compliance/form140, which neither internal/masters nor internal/server
// may import (archtest boundaries_test.go — form140 is a pure leaf and
// internal/reporting is its only permitted consumer). Migration 0070's header
// resolves the split deliberately: the table checks the two-digit SHAPE and
// form140 owns the list. So a screen built on this route MUST NOT tell an
// operator a state code is valid — "saved" is all this route earns. Whether a
// return can actually be built is what the TDS register's return_file status
// answers, and it surfaces form140's refusal (which names the annexure) verbatim.

import (
	"errors"
	"net/http"

	"github.com/khata/cored/internal/masters"
)

// tdsAddressBody is one statutory address block as the return numbers it: five
// lines, a country/region, a two-digit Annexure 1 CODE (never a state name) and
// a pincode. Lines 2-5 are optional.
type tdsAddressBody struct {
	Line1     string `json:"line1"`
	Line2     string `json:"line2"`
	Line3     string `json:"line3"`
	Line4     string `json:"line4"`
	Line5     string `json:"line5"`
	Country   string `json:"country"`
	StateCode string `json:"state_code"`
	Pincode   string `json:"pincode"`
}

func (b tdsAddressBody) toMasters() masters.TDSAddress {
	return masters.TDSAddress{
		Line1: b.Line1, Line2: b.Line2, Line3: b.Line3, Line4: b.Line4, Line5: b.Line5,
		Country: b.Country, StateCode: b.StateCode, Pincode: b.Pincode,
	}
}

func tdsAddressFrom(a masters.TDSAddress) tdsAddressBody {
	return tdsAddressBody{
		Line1: a.Line1, Line2: a.Line2, Line3: a.Line3, Line4: a.Line4, Line5: a.Line5,
		Country: a.Country, StateCode: a.StateCode, Pincode: a.Pincode,
	}
}

// tdsResponsibleBody is the person named on the return as answerable for the
// deduction. Their PAN is their OWN, a different field from the deductor's.
type tdsResponsibleBody struct {
	Name        string         `json:"name"`
	Designation string         `json:"designation"`
	PAN         string         `json:"pan"`
	Address     tdsAddressBody `json:"address"`
	// Email may be empty IF the deductor's is present: the format prints both
	// mandatory and then makes each conditional on the other.
	Email              string `json:"email"`
	ContactCountryCode string `json:"contact_country_code"`
	ContactNumber      string `json:"contact_number"`
}

// tdsProfileBody is the whole row, in and out. THERE IS NOT ONE MONEY FIELD IN
// IT — the deductor block is identity, address and contact — so nothing here is
// a number at all and the fixed-point-string rule has nothing to bite on.
type tdsProfileBody struct {
	// TAN is AAAA99999A. It is NOT the GSTIN and is not derivable from it.
	TAN string `json:"tan"`
	// PAN is the DEDUCTOR's own, or the literal served as
	// pan_not_required_literal.
	PAN string `json:"pan"`
	// DeductorName is the name held against the TAN, which must match the TAN
	// name in the .csi file downloaded from TIN — a different fact from the
	// company's display name.
	DeductorName string `json:"deductor_name"`
	// Category is an Annexure 4 code (one of deductor_categories below). It is
	// NOT the s.402(11) entity category on /deductor-status.
	Category           string             `json:"deductor_category"`
	Address            tdsAddressBody     `json:"address"`
	Email              string             `json:"email"`
	ContactCountryCode string             `json:"contact_country_code"`
	ContactNumber      string             `json:"contact_number"`
	StateNameCode      string             `json:"state_name_code"`
	MinistryCode       string             `json:"ministry_code"`
	MinistryOther      string             `json:"ministry_other"`
	AIN                string             `json:"ain"`
	GSTIN              string             `json:"gstin"`
	Responsible        tdsResponsibleBody `json:"responsible"`
	// RecordedBy is required on the way in and echoed on the way out;
	// RecordedAt is set by the database and ignored on input.
	RecordedBy string `json:"recorded_by"`
	RecordedAt string `json:"recorded_at,omitempty"`
}

func (b tdsProfileBody) toMasters() masters.TDSProfileRecord {
	return masters.TDSProfileRecord{
		TAN:                b.TAN,
		PAN:                b.PAN,
		DeductorName:       b.DeductorName,
		Category:           b.Category,
		Address:            b.Address.toMasters(),
		Email:              b.Email,
		ContactCountryCode: b.ContactCountryCode,
		ContactNumber:      b.ContactNumber,
		StateNameCode:      b.StateNameCode,
		MinistryCode:       b.MinistryCode,
		MinistryOther:      b.MinistryOther,
		AIN:                b.AIN,
		GSTIN:              b.GSTIN,
		Responsible: masters.TDSResponsiblePerson{
			Name:               b.Responsible.Name,
			Designation:        b.Responsible.Designation,
			PAN:                b.Responsible.PAN,
			Address:            b.Responsible.Address.toMasters(),
			Email:              b.Responsible.Email,
			ContactCountryCode: b.Responsible.ContactCountryCode,
			ContactNumber:      b.Responsible.ContactNumber,
		},
	}
}

func toTDSProfileBody(r masters.TDSProfileRecord) tdsProfileBody {
	return tdsProfileBody{
		TAN:                r.TAN,
		PAN:                r.PAN,
		DeductorName:       r.DeductorName,
		Category:           r.Category,
		Address:            tdsAddressFrom(r.Address),
		Email:              r.Email,
		ContactCountryCode: r.ContactCountryCode,
		ContactNumber:      r.ContactNumber,
		StateNameCode:      r.StateNameCode,
		MinistryCode:       r.MinistryCode,
		MinistryOther:      r.MinistryOther,
		AIN:                r.AIN,
		GSTIN:              r.GSTIN,
		Responsible: tdsResponsibleBody{
			Name:               r.Responsible.Name,
			Designation:        r.Responsible.Designation,
			PAN:                r.Responsible.PAN,
			Address:            tdsAddressFrom(r.Responsible.Address),
			Email:              r.Responsible.Email,
			ContactCountryCode: r.Responsible.ContactCountryCode,
			ContactNumber:      r.Responsible.ContactNumber,
		},
		RecordedBy: r.RecordedBy,
		RecordedAt: r.RecordedAt,
	}
}

// deductorCategoryView is one Annexure 4 code as the client receives it, rules
// included. See the file header for why the rules travel with the list.
type deductorCategoryView struct {
	Code        string `json:"code"`
	Description string `json:"description"`
	// StateNameCodeRequired is true for S, E, H, N and means BOTH "show the
	// Annexure 5 field" and "for every other category, CLEAR it" — the CHECK is
	// conditional in both directions.
	StateNameCodeRequired bool `json:"state_name_code_required"`
	MinistryRequired      bool `json:"ministry_required"`
	MinistryAllowed       bool `json:"ministry_allowed"`
}

func deductorCategoryViews() []deductorCategoryView {
	cats := masters.DeductorCategories()
	out := make([]deductorCategoryView, 0, len(cats))
	for _, c := range cats {
		out = append(out, deductorCategoryView{
			Code:                  c.Code,
			Description:           c.Description,
			StateNameCodeRequired: c.StateNameCodeRequired,
			MinistryRequired:      c.MinistryRequired,
			MinistryAllowed:       c.MinistryAllowed,
		})
	}
	return out
}

// handleGetTDSProfile serves GET .../tds-profile.
//
// A COMPANY WITH NO PROFILE IS 200 WITH A NULL PROFILE, NOT 404, and that is a
// decision rather than laziness: the absence of a row is the ordinary state of a
// company that has not filled the form in, and the response still has to carry
// the vocabulary the empty form is rendered from. A 404 would give a screen
// nothing to draw the picker with, and would report "not filled in yet" in the
// same status code the router uses for a mistyped URL.
func (s *Server) handleGetTDSProfile(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{
		"deductor_categories": deductorCategoryViews(),
		// The literal for a deductor not required to hold a PAN. Served for the
		// same reason the categories are: it is a prescribed value with a
		// meaning, and a client that spelled it itself would spell it wrong once.
		"pan_not_required_literal": masters.PANNotRequired,
	}
	rec, err := s.masters.TDSProfile(r.Context())
	switch {
	case errors.Is(err, masters.ErrNoTDSProfile):
		out["profile"] = nil
		// Stated rather than inferred from a null: this is the refusal the
		// return sourcing layer makes FIRST, before it looks at a challan or a
		// deduction, so a screen can say why no return can be filed yet.
		out["profile_recorded"] = false
		writeJSON(w, http.StatusOK, out)
		return
	case err != nil:
		writeMastersError(w, err)
		return
	}
	out["profile"] = toTDSProfileBody(rec)
	out["profile_recorded"] = true
	writeJSON(w, http.StatusOK, out)
}

// handleSetTDSProfile serves PUT .../tds-profile.
func (s *Server) handleSetTDSProfile(w http.ResponseWriter, r *http.Request) {
	var req tdsProfileBody
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.masters.SetTDSProfile(r.Context(), req.toMasters(), req.RecordedBy); err != nil {
		// A rejected profile is the OPERATOR'S input being wrong, not the
		// server's — 400 with the sentence masters wrote, which names the field,
		// the BH number and what to supply instead of a constraint name.
		if errors.Is(err, masters.ErrInvalidTDSProfile) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeMastersError(w, err)
		return
	}
	// Read back rather than echoing the request: what the operator gets is what
	// the row holds, including the normalisation (a TAN keyed in lower case is
	// stored in capitals) and the audit timestamp.
	rec, err := s.masters.TDSProfile(r.Context())
	if err != nil {
		writeMastersError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"profile":          toTDSProfileBody(rec),
		"profile_recorded": true,
	})
}
