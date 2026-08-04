package server

// THE TDS DEDUCTOR RETURN PROFILE ROUTE — T6, migration 0070.
//
// ⚠️ WRITTEN BUT NOT EXECUTED IN THE SESSION THAT ADDED THEM: all worktrees in
// that session shared one Postgres on :5433 and another process held it. The
// first run against a free database is the evidence. A skip is indistinguishable
// from a pass in the exit code, so census the run:
// `go test ./internal/server/ -count=1 -run TDSProfile -v | grep -c -- '--- SKIP'`
// must be 0.
//
// THESE TESTS FAIL, BY DESIGN, UNTIL THE THREE SHARED-FILE REGISTRATIONS LAND.
// The store and the handlers are in this branch; the route (server.go), the
// routeAction arm (auth_middleware.go) and the resource token
// (authz.KnownResources) are on the integrator's manifest because those files are
// shared. Until all three exist the Owner cases below answer 403 ("unmapped
// route: access denied") rather than 200 — which is precisely the signal
// TestEveryCompanyRouteIsMappedAndInVocabulary was built to give, and the reason
// a stage that adds a route with an empty manifest is lying.

import (
	"net/http"
	"strings"
	"testing"
)

// tdsProfileBodyFixture is a Company (Annexure 4 code K) deductor. K forbids both
// the Annexure 5 state name and the Annexure 3 ministry, so the conditional
// fields stay empty and each case sets only the field it is about.
func tdsProfileBodyFixture() map[string]any {
	return map[string]any{
		"tan":               "MUMK12345E",
		"pan":               "AAACK1234M",
		"deductor_name":     "Khata Traders Private Limited",
		"deductor_category": "K",
		"address": map[string]any{
			"line1":      "14 Marine Lines",
			"country":    "India",
			"state_code": "19",
			"pincode":    "400020",
		},
		"email":                "accounts@khata.example",
		"contact_country_code": "91",
		"contact_number":       "2226301234",
		"responsible": map[string]any{
			"name":        "Radhika Venkatesan",
			"designation": "Finance Director",
			"pan":         "AKQPV7821L",
			"address": map[string]any{
				"line1":      "14 Marine Lines",
				"country":    "India",
				"state_code": "19",
				"pincode":    "400020",
			},
			"contact_country_code": "91",
			"contact_number":       "2226301299",
		},
		"recorded_by": "owner",
	}
}

// TestTDSProfileAuthMatrix pins the two-touch rule for both halves of the route
// and the ACL decision behind the resource token.
//
// ⚠️ THE DECISION, and it is sharper here than for the deductor status next door:
// this row carries a NAMED INDIVIDUAL'S PAN and their home-ish address, so the
// READ is personal data about a specific person. Data Entry's default_access is
// 'none' and no seeded rule names `tds-profile`, so both halves are refused with
// NO ACL migration — which only works because this is a plain resource token and
// not a `report:NAME`. A report name would be reached by migration 0011's coarse
// `allow display report` at specificity 1 and would need a 0068-style
// counterweight to take back. See handler_tds_profile.go.
func TestTDSProfileAuthMatrix(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID
	dataEntry := aclSeededDataEntryToken(t, s, base, ownerTok)
	otherTok := aclToken(t, s, "acl-outsider-"+companyID[:8])

	body := tdsProfileBodyFixture()

	// --- The WRITE. -------------------------------------------------------
	if rec, _ := aclRequest(t, s, http.MethodPut, base+"/tds-profile", "", body); rec.Code != http.StatusUnauthorized {
		t.Errorf("PUT tds-profile with no token = %d, want 401", rec.Code)
	}
	if rec, _ := aclRequest(t, s, http.MethodPut, base+"/tds-profile", otherTok, body); rec.Code != http.StatusForbidden {
		t.Errorf("PUT tds-profile as another tenant = %d, want 403", rec.Code)
	}
	if rec, _ := aclRequest(t, s, http.MethodPut, base+"/tds-profile", dataEntry, body); rec.Code != http.StatusForbidden {
		t.Errorf("PUT tds-profile as Data Entry = %d, want 403 — the profile names the "+
			"person personally answerable for the deduction and is what the return is "+
			"signed with", rec.Code)
	}
	if rec, out := aclRequest(t, s, http.MethodPut, base+"/tds-profile", ownerTok, body); rec.Code != http.StatusOK {
		t.Errorf("PUT tds-profile as Owner = %d, want 200 (%v)", rec.Code, out)
	}

	// --- The READ. Same token, so the same four answers. -------------------
	if rec, _ := aclRequest(t, s, http.MethodGet, base+"/tds-profile", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("GET tds-profile with no token = %d, want 401", rec.Code)
	}
	if rec, _ := aclRequest(t, s, http.MethodGet, base+"/tds-profile", otherTok, nil); rec.Code != http.StatusForbidden {
		t.Errorf("GET tds-profile as another tenant = %d, want 403", rec.Code)
	}
	if rec, _ := aclRequest(t, s, http.MethodGet, base+"/tds-profile", dataEntry, nil); rec.Code != http.StatusForbidden {
		t.Errorf("GET tds-profile as Data Entry = %d, want 403 — the read is a named "+
			"individual's PAN, and read and write share one resource token so they are "+
			"granted or refused together", rec.Code)
	}
	if rec, _ := aclRequest(t, s, http.MethodGet, base+"/tds-profile", ownerTok, nil); rec.Code != http.StatusOK {
		t.Errorf("GET tds-profile as Owner = %d, want 200", rec.Code)
	}
}

// TestTDSProfileGETServesTheVocabularyBeforeThereIsAProfile covers the state a
// screen actually opens in: nothing recorded yet. 200 with a null profile AND the
// sixteen Annexure 4 categories, because a 404 would leave the client with no
// list to draw the picker from and would report "not filled in" the way a
// mistyped URL is reported.
func TestTDSProfileGETServesTheVocabularyBeforeThereIsAProfile(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID

	rec, body := aclRequest(t, s, http.MethodGet, base+"/tds-profile", ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET with no profile recorded = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if body["profile"] != nil {
		t.Errorf("profile = %v, want null before anything is recorded", body["profile"])
	}
	if body["profile_recorded"] != false {
		t.Errorf("profile_recorded = %v, want false — this is the refusal the return "+
			"sourcing layer makes first", body["profile_recorded"])
	}
	cats, _ := body["deductor_categories"].([]any)
	if len(cats) != 16 {
		t.Fatalf("deductor_categories has %d entries, want Annexure 4's sixteen — a client "+
			"that hard-coded them could offer a letter the CHECK refuses", len(cats))
	}
	// The conditional RULES must travel with the list, or a screen decides for
	// itself when the Annexure 5 field is mandatory.
	byCode := map[string]map[string]any{}
	for _, c := range cats {
		m, _ := c.(map[string]any)
		code, _ := m["code"].(string)
		byCode[code] = m
	}
	if byCode["S"]["state_name_code_required"] != true {
		t.Errorf("category S (State Government) must be served as requiring the Annexure 5 "+
			"state code, got %v", byCode["S"]["state_name_code_required"])
	}
	if byCode["K"]["state_name_code_required"] != false {
		t.Errorf("category K (Company) FORBIDS the Annexure 5 state code, got %v",
			byCode["K"]["state_name_code_required"])
	}
	if byCode["A"]["ministry_required"] != true || byCode["Q"]["ministry_allowed"] != false {
		t.Errorf("the Annexure 3 ministry rules did not survive the wire: A=%v, Q=%v",
			byCode["A"], byCode["Q"])
	}
	if body["pan_not_required_literal"] != "PANNOTREQD" {
		t.Errorf("pan_not_required_literal = %v; the format's own literal is served so a "+
			"client never spells it itself", body["pan_not_required_literal"])
	}
}

// TestTDSProfileRoundTripsThroughTheWire — what goes in comes back, including the
// normalisation, and the correction path clears what the new category forbids.
func TestTDSProfileRoundTripsThroughTheWire(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID

	// Keyed in lower case, as an operator will. A TAN is defined in capitals and
	// migration 0070's shape CHECK accepts nothing else, so this must be
	// normalised rather than refused.
	in := tdsProfileBodyFixture()
	in["tan"] = "mumk12345e"
	rec, body := aclRequest(t, s, http.MethodPut, base+"/tds-profile", ownerTok, in)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	prof, _ := body["profile"].(map[string]any)
	if prof["tan"] != "MUMK12345E" {
		t.Errorf("tan = %v, want MUMK12345E (normalised, not refused)", prof["tan"])
	}
	if prof["recorded_at"] == nil || prof["recorded_at"] == "" {
		t.Error("recorded_at is empty; the response is read back from the row, not echoed")
	}

	// Correct it to a State Government deductor, which MAKES the Annexure 5 code
	// mandatory — the whole-row PUT is what allows both fields to change at once.
	in2 := tdsProfileBodyFixture()
	in2["deductor_category"] = "S"
	in2["state_name_code"] = "19"
	in2["recorded_by"] = "corrector"
	if rec, out := aclRequest(t, s, http.MethodPut, base+"/tds-profile", ownerTok, in2); rec.Code != http.StatusOK {
		t.Fatalf("correcting K -> S = %d, want 200 (%v)", rec.Code, out)
	}
	rec, body = aclRequest(t, s, http.MethodGet, base+"/tds-profile", ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d", rec.Code)
	}
	if body["profile_recorded"] != true {
		t.Errorf("profile_recorded = %v, want true", body["profile_recorded"])
	}
	prof, _ = body["profile"].(map[string]any)
	if prof["deductor_category"] != "S" || prof["state_name_code"] != "19" {
		t.Errorf("category/state name = %v/%v, want S/19",
			prof["deductor_category"], prof["state_name_code"])
	}
	if prof["recorded_by"] != "corrector" {
		t.Errorf("recorded_by = %v, want corrector", prof["recorded_by"])
	}
}

// TestTDSProfileRefusalsAre400WithASentence. A rejected profile is the operator's
// input being wrong, so it must come back as 400 carrying the sentence masters
// wrote — not a 500, and not a Postgres constraint name.
func TestTDSProfileRefusalsAre400WithASentence(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID

	for _, tc := range []struct {
		name  string
		mut   func(map[string]any)
		wants string
	}{
		{
			// The first mistake an operator makes: `company` holds a GSTIN and no
			// TAN at all.
			name:  "the GSTIN in the TAN field",
			mut:   func(b map[string]any) { b["tan"] = "27AAACK1234M1Z5" },
			wants: "NOT the GSTIN",
		},
		{
			// The two deductor vocabularies are not one vocabulary.
			name:  "an s.402(11) entity category in the Annexure 4 field",
			mut:   func(b map[string]any) { b["deductor_category"] = "COMPANY" },
			wants: "Annexure 4",
		},
		{
			name:  "an Annexure 5 state code on a Company deductor",
			mut:   func(b map[string]any) { b["state_name_code"] = "19" },
			wants: "State-Government",
		},
		{
			name: "neither e-mail address",
			mut: func(b map[string]any) {
				b["email"] = ""
				b["responsible"].(map[string]any)["email"] = ""
			},
			wants: "e-mail",
		},
	} {
		body := tdsProfileBodyFixture()
		tc.mut(body)
		rec, _ := aclRequest(t, s, http.MethodPut, base+"/tds-profile", ownerTok, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", tc.name, rec.Code)
			continue
		}
		if !strings.Contains(rec.Body.String(), tc.wants) {
			t.Errorf("%s: the refusal must say %q, got %s", tc.name, tc.wants, rec.Body.String())
		}
	}
}

// TestAShapeValidStateCodeThatIsNotAStateIsAcceptedByThisRoute documents the
// DISCLOSED GAP at the HTTP boundary, where a screen author will meet it.
//
// Annexure 1's 37 codes live in internal/compliance/form140, which neither
// internal/masters nor internal/server may import, and migration 0070's header
// splits the work deliberately: the table checks the two-digit SHAPE, form140
// owns the list. "08" is not a state — the workbook jumps from 07 to 09 — and it
// saves here.
//
// SO A SCREEN MUST NEVER REPORT "VALID". The refusal exists, it is form140's, and
// it reaches the operator through the TDS register's return_file status with
// Annexure 1 named. Do not close this gap by pasting the 37 codes into a handler
// or a .tsx file.
func TestAShapeValidStateCodeThatIsNotAStateIsAcceptedByThisRoute(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID

	body := tdsProfileBodyFixture()
	body["address"].(map[string]any)["state_code"] = "08"
	if rec, out := aclRequest(t, s, http.MethodPut, base+"/tds-profile", ownerTok, body); rec.Code != http.StatusOK {
		t.Fatalf("this route checks the SHAPE only (form140 owns Annexure 1): %d %v", rec.Code, out)
	}
}
