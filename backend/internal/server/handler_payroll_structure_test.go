package server

// handler_payroll_structure_test.go — THE PROBE FOR THE GAP THAT MADE THE WHOLE
// PAYROLL MODULE UNUSABLE OVER HTTP.
//
// ===========================================================================
// WHAT THIS ASSERTS, AND WHY IT IS A PROPERTY AND NOT A LIST
// ===========================================================================
// THE PROPERTY: **an employee created over HTTP can be paid over HTTP.**
//
// Not "the salary-component route returns 201" — that is a list of one, and the
// next thing missing would be off the list, which is the mistake HANDOFF §8 rule
// 3 names. This test performs the entire task an operator performs, end to end,
// with EVERY step going through a handler and NOT ONE `s.payroll.*` call
// anywhere in it. If any seam in that chain has no route, this fails.
//
// ⛔ IT WAS RUN AGAINST THE BROKEN BUILD FIRST AND ITS FAILURE WAS PREDICTED,
// THEN OBSERVED (HANDOFF §8 rule 2 — a probe that has never failed proves
// nothing). Predicted: with `handleSetSalaryComponent` and
// `handleSetStatutoryElection` absent there is no way to lay down either child,
// so the pay run answers 422 "no statutory election is in force for wage month
// 2026-07". Observed: exactly that.
//
// The gap it closes: Track 3 mounted eight payroll routes and mapped every one
// of them, while `payrolldb.SetSalaryComponent` and `SetStatutoryElection` — the
// two writers the pay run cannot proceed without — had no route at all. Every
// existing payroll test in this package sets both up by calling the SERVICE
// (handler_payroll_test.go's fixture says so in a comment), which is precisely
// why a green suite never noticed: the tests bypassed the seam that was missing.
//
// ===========================================================================
// THE ORACLE IS HAND-DERIVED AND SHARES NO CODE WITH THE ENGINE
// ===========================================================================
// The figures below are computed from the published rates, not read back from
// the thing under test — the same discipline the real-Tally-file gate follows.
// ONE employee, a single BASIC of 18,000 from 1 April 2026, July 2026 run,
// ruleset "ss-2026-07" (EPF 12%, EPS 8.33%, wage ceilings 15,000; ESI
// 0.75/3.25, coverage ceiling 21,000):
//
//	STATUTORY WAGE 18,000; EPF/EPS wages min(18,000, 15,000) = 15,000
//	  employee EPF 12% x 15,000                              = 1,800
//	  EPS          8.33% x 15,000 = 1,249.50                 -> 1,250
//	ESI covered (18,000 <= 21,000) on 18,000:
//	  employee 0.75%                                         =   135
//	s.392: 18,000 x 9 months less the 75,000 standard deduction is far below the
//	  s.156 rebate limit                                     -> TDS = 0
//	GROSS 18,000 less (1,800 + 135)                          -> NET 16,065

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/khata/cored/internal/payroll"
)

// structureCall builds the request chi would hand a handler that takes BOTH
// {companyID} and {employeeID}, and runs it.
//
// Written here rather than reusing payrollCall because the employee id in the
// PATH is the whole point of these routes: it is not the operator's to type — it
// comes from the master they are looking at — and a helper that could not carry
// it would be testing a different shape from the one that is mounted.
func structureCall(
	t *testing.T,
	h http.HandlerFunc,
	method string,
	companyID, employeeID uuid.UUID,
	suffix string,
	body any,
) *httptest.ResponseRecorder {
	t.Helper()
	rec := payrollCall(t, func(w http.ResponseWriter, r *http.Request) {
		rctx := chi.RouteContext(r.Context())
		rctx.URLParams.Add("employeeID", employeeID.String())
		h(w, r)
	}, method, "/employees/"+employeeID.String()+suffix, companyID, body)
	return rec
}

// TestAnEmployeeCreatedOverHTTPCanBePaidOverHTTP is the probe. See the header.
func TestAnEmployeeCreatedOverHTTPCanBePaidOverHTTP(t *testing.T) {
	s := payrollServer(t)
	companyID, _ := payrollCompany(t, s)

	// ---- (1) The master. is_active omitted: an omitted flag must mean ACTIVE.
	rec := payrollCall(t, s.handleCreateEmployee, http.MethodPost, "/employees", companyID, map[string]any{
		"code": "E-WIRE-1", "name": "Wired Employee", "doj": "2025-06-01",
		"designation": "Analyst", "pan": "ABCPD1234E", "uan": "100200300422",
		"esic_ip_number": "1234567822", "bank_ifsc": "HDFC0001234",
		"bank_account_last4": "4321",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /employees = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	empID, err := uuid.Parse(payrollJSON(t, rec)["employee_id"].(string))
	if err != nil {
		t.Fatalf("employee_id is not a uuid: %v", err)
	}

	// ---- (2) The salary structure, OVER HTTP.
	rec = structureCall(t, s.handleSetSalaryComponent, http.MethodPost, companyID, empID,
		"/salary-components", map[string]any{
			"effective_from": "2026-04-01",
			"code":           "BASIC",
			"type":           string(payroll.ComponentBasic),
			// A STRING. A JSON number would arrive as a float64 and re-price this
			// person's salary in the last paisa — and every other figure on the
			// payslip is derived from this one.
			"amount": "18000",
		})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST salary-components = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}

	// ---- (3) The statutory election, OVER HTTP.
	rec = structureCall(t, s.handleSetStatutoryElection, http.MethodPost, companyID, empID,
		"/statutory-elections", map[string]any{
			"effective_from": "2026-04-01",
			"pf_base":        string(payroll.PFBaseCeiling),
			"eps_member":     true,
			"esi_applicable": true,
			"regime":         string(payroll.RegimeNew),
		})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST statutory-elections = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}

	// ---- (4) The pay run, OVER HTTP. This is the step that answered 422 before
	// the two routes above existed, and the message named the election.
	rec = payrollCall(t, s.handlePayrollRun, http.MethodPost, "/vouchers/payroll", companyID, map[string]any{
		"wage_month": payrollWageMonth.String(),
		"created_by": "payroll-wire-test",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /vouchers/payroll = %d, want 201 (%s)\n"+
			"THIS IS THE DEFECT THIS FILE EXISTS FOR: an employee created over HTTP could not be paid over HTTP, "+
			"because the salary structure and the statutory election had no route.",
			rec.Code, rec.Body.String())
	}
	if got := payrollJSON(t, rec)["status"]; got != "POSTED" {
		t.Fatalf("pay run status = %v, want POSTED", got)
	}

	// ---- (5) The register, OVER HTTP, against the HAND-DERIVED oracle.
	rec = payrollCall(t, s.handlePayrollRegister, http.MethodGet,
		"/payroll-register?month="+payrollWageMonth.String(), companyID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /payroll-register = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	reg := payrollJSON(t, rec)
	rows, _ := reg["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("register has %d rows, want 1", len(rows))
	}
	row, _ := rows[0].(map[string]any)
	for _, want := range []struct{ key, value string }{
		{"gross", "18000.0000"},
		{"epf_employee", "1800.0000"},
		{"esi_employee", "135.0000"},
		{"tds", "0.0000"},
		{"net", "16065.0000"},
	} {
		if got := row[want.key]; got != want.value {
			t.Errorf("register row %s = %v, want %s (hand-derived from the published rates, not read back from the engine)",
				want.key, got, want.value)
		}
	}
	// ⛔ THE RULESET VERSIONS ARE ON THE REPORT AND MUST NOT BE EMPTY. A payroll
	// figure with no statement of which law produced it cannot be checked by
	// anybody who was not present when it was run.
	if reg["ss_ruleset_version"] == "" || reg["it_ruleset_version"] == "" {
		t.Errorf("the register carries empty ruleset versions: %v / %v",
			reg["ss_ruleset_version"], reg["it_ruleset_version"])
	}

	// ---- (6) Both children read BACK over HTTP. A screen that cannot read these
	// can only discover an incomplete master by attempting a pay run — which is
	// the one operation an operator most wants to be sure about beforehand.
	rec = structureCall(t, s.handleListSalaryComponents, http.MethodGet, companyID, empID,
		"/salary-components", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET salary-components = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	comps, _ := payrollJSON(t, rec)["salary_components"].([]any)
	if len(comps) != 1 {
		t.Fatalf("salary_components has %d rows, want 1", len(comps))
	}
	comp, _ := comps[0].(map[string]any)
	if comp["amount"] != "18000.0000" {
		t.Errorf("salary component amount = %v, want 18000.0000", comp["amount"])
	}
	// BASIC is wages under s.2(88) — the classification travels WITH the row so a
	// client never has to join two responses to learn the single most
	// consequential fact about a component.
	if comp["is_wages"] != true {
		t.Errorf("BASIC is_wages = %v, want true (s.2(88) exclusions are exhaustive and BASIC is not on the list)", comp["is_wages"])
	}

	rec = structureCall(t, s.handleListStatutoryElections, http.MethodGet, companyID, empID,
		"/statutory-elections", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET statutory-elections = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	elections, _ := payrollJSON(t, rec)["statutory_elections"].([]any)
	if len(elections) != 1 {
		t.Fatalf("statutory_elections has %d rows, want 1", len(elections))
	}
	el, _ := elections[0].(map[string]any)
	if el["pf_base"] != string(payroll.PFBaseCeiling) {
		t.Errorf("election pf_base = %v, want CEILING", el["pf_base"])
	}
}

// TestSalaryComponentRefusesWhatWouldBeSilentlyWrong pins the four refusals that
// exist because their absence would be invisible rather than noisy.
func TestSalaryComponentRefusesWhatWouldBeSilentlyWrong(t *testing.T) {
	s := payrollServer(t)
	companyID, _ := payrollCompany(t, s)

	rec := payrollCall(t, s.handleCreateEmployee, http.MethodPost, "/employees", companyID, map[string]any{
		"code": "E-WIRE-2", "name": "Refusal Employee", "doj": "2025-06-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /employees = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	empID := uuid.MustParse(payrollJSON(t, rec)["employee_id"].(string))

	cases := []struct {
		name string
		body map[string]any
		why  string
	}{
		{
			"no effective_from",
			map[string]any{"code": "BASIC", "type": "BASIC", "amount": "18000"},
			"a date this handler invented would decide which months the figure applies to",
		},
		{
			"a component type outside the closed vocabulary",
			map[string]any{"effective_from": "2026-04-01", "code": "X", "type": "TIFFIN", "amount": "1"},
			"a type that fell through to \"excluded\" would under-contribute PF and ESI for the whole of that employee's service, with no error anywhere",
		},
		{
			"an amount that is not a decimal",
			map[string]any{"effective_from": "2026-04-01", "code": "BASIC", "type": "BASIC", "amount": "eighteen thousand"},
			"money is a string end to end and is parsed, never coerced",
		},
		{
			"no amount at all",
			map[string]any{"effective_from": "2026-04-01", "code": "BASIC", "type": "BASIC"},
			"an absent amount is not zero pay; it is a field nobody filled in",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := structureCall(t, s.handleSetSalaryComponent, http.MethodPost, companyID, empID,
				"/salary-components", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("= %d, want 400 — %s (%s)", rec.Code, tc.why, rec.Body.String())
			}
		})
	}

	// The PF base has no safe default: CEILING and ACTUAL differ for anybody paid
	// above the EPF wage ceiling, so an unstated one is a contribution figure
	// nobody chose.
	rec = structureCall(t, s.handleSetStatutoryElection, http.MethodPost, companyID, empID,
		"/statutory-elections", map[string]any{"effective_from": "2026-04-01", "eps_member": true})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("election with no pf_base = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
}

// TestPayrollVocabularyIsTheStatutesAndNotTheTenants pins the served vocabulary.
//
// It exists because the client would otherwise write this list down a second
// time, in another language, updated by somebody else — and `is_wages` is a
// LEGAL classification carrying the Vivekananda Vidyamandir holding on special
// allowance, which is the single most common PF under-contribution there is.
func TestPayrollVocabularyIsTheStatutesAndNotTheTenants(t *testing.T) {
	s := payrollServer(t)
	companyID, _ := payrollCompany(t, s)

	rec := payrollCall(t, s.handlePayrollVocabulary, http.MethodGet, "/payroll-vocabulary", companyID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /payroll-vocabulary = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	body := payrollJSON(t, rec)
	types, _ := body["component_types"].([]any)
	if len(types) != len(payroll.ComponentTypes()) {
		t.Fatalf("component_types has %d entries, want %d — the served vocabulary must be the CLOSED set, or a form can compose a value the server refuses",
			len(types), len(payroll.ComponentTypes()))
	}
	byCode := map[string]map[string]any{}
	for _, raw := range types {
		m, _ := raw.(map[string]any)
		byCode[m["code"].(string)] = m
	}
	// The three that decide whether the classification is right at all.
	for _, want := range []struct {
		code    string
		isWages bool
		why     string
	}{
		{"BASIC", true, "basic pay is not on the s.2(88) exclusion list, and the list is exhaustive"},
		{"SPECIAL_ALLOWANCE", true, "Vivekananda Vidyamandir (SC, 2019) — a universally paid special allowance IS wages"},
		{"HRA", false, "s.2(88)(f) excludes house rent allowance expressly"},
	} {
		got, ok := byCode[want.code]
		if !ok {
			t.Fatalf("%s is missing from the served vocabulary", want.code)
		}
		if got["is_wages"] != want.isWages {
			t.Errorf("%s is_wages = %v, want %v — %s", want.code, got["is_wages"], want.isWages, want.why)
		}
	}
	// Gratuity is EXCLUDED but is NOT in the deeming test: clauses (a)-(i) stop
	// short of it, and including it fabricated an add-back out of a leaver's
	// final settlement.
	if byCode["GRATUITY"]["in_deeming_test"] != false {
		t.Errorf("GRATUITY in_deeming_test = %v, want false — proviso 1's enumeration stops at clause (i)",
			byCode["GRATUITY"]["in_deeming_test"])
	}
	if byCode["HRA"]["in_deeming_test"] != true {
		t.Errorf("HRA in_deeming_test = %v, want true — s.2(88)(f) is inside clauses (a)-(i)",
			byCode["HRA"]["in_deeming_test"])
	}
	if body["default_regime"] != string(payroll.RegimeNew) {
		t.Errorf("default_regime = %v, want NEW (Income-tax Act 2025 s.202)", body["default_regime"])
	}
}
