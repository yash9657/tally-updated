package server_test

// query_test.go drives the Phase-3 conversational query endpoint
// (POST /v1/companies/{companyID}/query) end-to-end over httptest, hermetic
// against the live :5433 Postgres the rest of this suite uses. It proves the
// WS2 contract: a typed DSL is compiled+executed correctly, an NL question is
// translated, an untranslatable question and an invalid DSL both 422 (never
// 500, never leaking SQL/internal text), the company is resolved from the
// authz-verified context (an Owner is allowed), and the sql_preview never
// contains an interpolated user value.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/internal/server"
)

// seedSalesCompany creates a company with a debtor + sales ledger and posts two
// Sales vouchers (1000 + 2000 taxable, both dated 2025-05), returning the base
// path. It is the fixture every query test reuses.
func seedSalesCompany(t *testing.T, srv *server.Server) string {
	t.Helper()
	cid := uuid.New()
	companyID := cid.String()
	base := "/v1/companies/" + companyID
	rec, _ := do(t, srv, http.MethodPost, "/v1/companies", map[string]any{
		"id":            companyID,
		"name":          "Query Co " + companyID[:8],
		"fy_start_date": "2025-04-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create company status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	testdb.Cleanup(t, cid)
	debtorID := createLedger(t, srv, base, "Acme Corp", "Sundry Debtors")
	salesID := createLedger(t, srv, base, "Domestic Sales", "Sales Accounts")
	cgstID := createLedger(t, srv, base, "Output CGST", "Duties & Taxes")
	sgstID := createLedger(t, srv, base, "Output SGST", "Duties & Taxes")
	igstID := createLedger(t, srv, base, "Output IGST", "Duties & Taxes")

	// Post GST sales so vv.taxable_value is populated (the query sums it). Taxable
	// X at 18% intra-state -> CGST/SGST X*0.09 each, invoice X*1.18.
	post := func(no, date, taxable, half, total string) {
		rec, _ := do(t, srv, http.MethodPost, base+"/vouchers/sales", map[string]any{
			"voucher_no": no, "voucher_date": date, "party": "Acme Corp", "created_by": "q",
			"postings": []map[string]any{
				{"ledger_id": debtorID, "dr_cr": "D", "amount": total},
				{"ledger_id": salesID, "dr_cr": "C", "amount": taxable},
				{"ledger_id": cgstID, "dr_cr": "C", "amount": half},
				{"ledger_id": sgstID, "dr_cr": "C", "amount": half},
			},
			"gst": map[string]any{
				"supplier_state_code":        "29",
				"place_of_supply_state_code": "29",
				"lines":                      []map[string]any{{"taxable_value": taxable, "gst_rate_pct": "18"}},
				"cgst_ledger_id":             cgstID,
				"sgst_ledger_id":             sgstID,
				"igst_ledger_id":             igstID,
			},
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("post sale %s status = %d, want 201 (body %s)", no, rec.Code, rec.Body.String())
		}
	}
	post("Q-1", "2025-05-10", "1000.0000", "90.0000", "1180.0000")
	post("Q-2", "2025-05-20", "2000.0000", "180.0000", "2360.0000")
	return base
}

// salesByMonthDSL builds a typed-DSL request body mirroring the pinned wire shape.
func salesByMonthDSL() map[string]any {
	return map[string]any{
		"dsl": map[string]any{
			"entity":     "vouchers",
			"metrics":    []map[string]any{{"func": "sum", "column": "taxable_value", "as": "total_sales"}},
			"dimensions": []string{"month"},
			"filters":    []map[string]any{{"column": "voucher_type_name", "op": "eq", "values": []string{"Sales"}}},
			"date_range": map[string]any{"column": "voucher_date", "from": "2025-04-01", "to": "2026-03-31"},
			"order_by":   []map[string]any{{"column": "month", "desc": false}},
			"limit":      100,
		},
	}
}

// TestQueryDSLSumByMonth posts a typed DSL directly and asserts the correct
// per-month sales total for the seeded data (1000 + 2000 in 2025-05 = 3000).
func TestQueryDSLSumByMonth(t *testing.T) {
	srv := newServer(t)
	base := seedSalesCompany(t, srv)

	rec, body := do(t, srv, http.MethodPost, base+"/query", salesByMonthDSL())
	if rec.Code != http.StatusOK {
		t.Fatalf("query DSL status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	cols, _ := body["columns"].([]any)
	if len(cols) != 2 {
		t.Fatalf("columns = %v, want 2", body["columns"])
	}

	rows, _ := body["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("rows = %v, want 1 month bucket", body["rows"])
	}
	r0, _ := rows[0].([]any)
	if len(r0) != 2 {
		t.Fatalf("row = %v, want [month,total]", r0)
	}
	if got, _ := r0[0].(string); got != "2025-05" {
		t.Fatalf("month = %q, want 2025-05", got)
	}
	if got, _ := r0[1].(string); got != "3000.0000" {
		t.Fatalf("total_sales = %q, want 3000.0000 (paisa-exact)", got)
	}

	// sql_preview is present and contains ONLY $N placeholders, never the filter
	// value "Sales" interpolated into the SQL text.
	preview, _ := body["sql_preview"].(string)
	if preview == "" {
		t.Fatalf("sql_preview empty, want the compiled SQL with placeholders")
	}
	if strings.Contains(preview, "Sales") {
		t.Fatalf("sql_preview leaked the filter value: %q", preview)
	}
	if !strings.Contains(preview, "$1") {
		t.Fatalf("sql_preview missing $N placeholder: %q", preview)
	}
}

// TestQueryNLTrialBalance posts the NL question "trial balance" and asserts it
// translates + executes to a 200 with rows.
func TestQueryNLTrialBalance(t *testing.T) {
	srv := newServer(t)
	base := seedSalesCompany(t, srv)

	rec, body := do(t, srv, http.MethodPost, base+"/query", map[string]any{"nl": "trial balance"})
	if rec.Code != http.StatusOK {
		t.Fatalf("nl trial balance status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if _, ok := body["rows"].([]any); !ok {
		t.Fatalf("nl trial balance missing rows (body %s)", rec.Body.String())
	}
	dsl, _ := body["dsl"].(map[string]any)
	if ent, _ := dsl["entity"].(string); ent != "ledger_balances" {
		t.Fatalf("nl trial balance dsl.entity = %q, want ledger_balances", ent)
	}
}

// TestQueryNLSalesThisYear posts "sales this year" and asserts it answers. The
// FY range derives from the server clock; this asserts a well-formed 200
// regardless of the exact total so the test is not calendar-flaky (the
// deterministic date math is covered in the package-level translate_test.go).
func TestQueryNLSalesThisYear(t *testing.T) {
	srv := newServer(t)
	base := seedSalesCompany(t, srv)

	rec, body := do(t, srv, http.MethodPost, base+"/query", map[string]any{"nl": "sales this year"})
	if rec.Code != http.StatusOK {
		t.Fatalf("nl sales this year status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if _, ok := body["rows"].([]any); !ok {
		t.Fatalf("nl sales this year missing rows (body %s)", rec.Body.String())
	}
}

// TestQueryUnknownNLIs422 posts gibberish and asserts a 422 carrying the example
// questions, NOT a 500 and NOT any internal error text.
func TestQueryUnknownNLIs422(t *testing.T) {
	srv := newServer(t)
	base := seedSalesCompany(t, srv)

	rec, body := do(t, srv, http.MethodPost, base+"/query", map[string]any{"nl": "what is the meaning of life"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown nl status = %d, want 422 (body %s)", rec.Code, rec.Body.String())
	}
	if _, ok := body["examples"].([]any); !ok {
		t.Fatalf("422 missing examples list (body %s)", rec.Body.String())
	}
	if msg, _ := body["error"].(string); msg == "" || strings.Contains(strings.ToLower(msg), "sql") {
		t.Fatalf("422 error message %q is empty or leaks sql", msg)
	}
}

// TestQueryInvalidDSLIs422 posts a DSL with an unknown column and asserts a 422
// (a compile error) rather than a 500, and that the SQL/internal text never
// leaks into the response.
func TestQueryInvalidDSLIs422(t *testing.T) {
	srv := newServer(t)
	base := seedSalesCompany(t, srv)

	rec, body := do(t, srv, http.MethodPost, base+"/query", map[string]any{
		"dsl": map[string]any{
			"entity":     "vouchers",
			"metrics":    []map[string]any{{"func": "sum", "column": "taxable_value", "as": "t"}},
			"dimensions": []string{"not_a_real_column"},
			"limit":      100,
		},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid dsl status = %d, want 422 (body %s)", rec.Code, rec.Body.String())
	}
	if msg, _ := body["error"].(string); msg == "" || strings.Contains(strings.ToLower(msg), "select") {
		t.Fatalf("invalid dsl error %q is empty or leaks sql", msg)
	}
}

// TestQueryUnknownEntityIs422 posts a DSL naming an entity outside the closed
// set (a forbidden auth table) and asserts a 422.
func TestQueryUnknownEntityIs422(t *testing.T) {
	srv := newServer(t)
	base := seedSalesCompany(t, srv)

	rec, _ := do(t, srv, http.MethodPost, base+"/query", map[string]any{
		"dsl": map[string]any{
			"entity":  "app_user",
			"metrics": []map[string]any{{"func": "count", "column": "*", "as": "n"}},
			"limit":   100,
		},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown entity status = %d, want 422", rec.Code)
	}
}

// TestQueryNLTranslatorUnavailableIs503 is the wave-2 WS1 boot-matrix gate: with
// a (dummy) ANTHROPIC_API_KEY set, buildTranslator selects the real-Claude
// adapter; pointing QUERY_TRANSLATOR_BASE_URL at a guaranteed-dead loopback port
// makes every NL translation fail with nlq.ErrTranslatorUnavailable, which the
// handler must map to 503 (an operator fault) — distinct from the 422 an
// untranslatable question returns. A typed DSL bypasses the translator entirely,
// so it still succeeds (200), proving the escape hatch works when the LLM is down.
//
// NON-PARALLEL by construction: t.Setenv mutates process-wide env (ANTHROPIC_API_KEY,
// QUERY_TRANSLATOR_BASE_URL) that must be set BEFORE newServer builds the Service.
// Port 1 on loopback refuses immediately (copying the JWKS dead-endpoint idiom),
// so the client cannot hang on its 15s timeout.
func TestQueryNLTranslatorUnavailableIs503(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "dummy-key-for-test")
	t.Setenv("QUERY_TRANSLATOR_BASE_URL", "http://127.0.0.1:1")

	srv := newServer(t)
	base := seedSalesCompany(t, srv)

	// NL path -> translator called -> dead upstream -> 503.
	rec, body := do(t, srv, http.MethodPost, base+"/query", map[string]any{"nl": "total sales this quarter"})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nl with dead translator status = %d, want 503 (body %s)", rec.Code, rec.Body.String())
	}
	if msg, _ := body["error"].(string); msg == "" || strings.Contains(strings.ToLower(msg), "sql") {
		t.Fatalf("503 error message %q is empty or leaks sql", msg)
	}

	// DSL path -> translator NOT called -> still 200 (typed DSL is the documented
	// fallback when the LLM is unavailable).
	rec2, _ := do(t, srv, http.MethodPost, base+"/query", salesByMonthDSL())
	if rec2.Code != http.StatusOK {
		t.Fatalf("dsl with dead translator status = %d, want 200 (DSL bypasses the LLM) (body %s)", rec2.Code, rec2.Body.String())
	}
}

// TestQueryOwnerAllowed asserts an Owner (default_access full) is permitted to
// query — proving the routeAction POST /query -> Display/query mapping is wired
// (an unmapped route default-denies with 403). A custom level that DISALLOWS
// resource "query" returning 403 is the symmetric case; it requires direct
// security_level/acl_rule seeding (no HTTP route mints a restricted level in
// this suite), so it is documented here and exercised by the live integrator
// gate rather than asserted in-process.
func TestQueryOwnerAllowed(t *testing.T) {
	srv := newServer(t)
	base := seedSalesCompany(t, srv)

	rec, _ := do(t, srv, http.MethodPost, base+"/query", map[string]any{"nl": "trial balance"})
	if rec.Code == http.StatusForbidden {
		t.Fatalf("owner query got 403 — routeAction POST /query mapping is missing")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("owner query status = %d, want 200", rec.Code)
	}
}

// TestQueryCrossTenantQuestionIsRefusedInWORDS is D50's disclosure half at the
// HANDLER. The translator already refuses a question that selects another scope,
// but its typed reason was routed through the generic untranslatable arm, so the
// operator was told "could not understand the question" — which is the same
// non-statement D50 records: someone who asks for every company's sales and is
// shown one company's sales has nothing to tell the two apart.
//
// The assertion is on the WORDS, not the status: a 422 both arms already share
// proves nothing. It also pins the CONTRAST — gibberish must NOT get the scope
// wording — so a lazy fix that gives every 422 this message fails here.
func TestQueryCrossTenantQuestionIsRefusedInWords(t *testing.T) {
	srv := newServer(t)
	base := seedSalesCompany(t, srv)

	for _, nl := range []string{
		"total sales for every company",
		"receivables across all tenants",
		"closing balances for company 11111111-2222-3333-4444-555555555555",
	} {
		rec, body := do(t, srv, http.MethodPost, base+"/query", map[string]any{"nl": nl})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%q status = %d, want 422 (body %s)", nl, rec.Code, rec.Body.String())
		}
		msg, _ := body["error"].(string)
		if !strings.Contains(msg, "another company") {
			t.Errorf("%q was refused WITHOUT saying the named scope was not read: %q", nl, msg)
		}
		if _, ok := body["examples"].([]any); !ok {
			t.Errorf("%q: scope refusal dropped the examples list (body %s)", nl, rec.Body.String())
		}
	}

	// CONTRAST: an ordinary unrecognised question keeps the generic wording.
	_, body := do(t, srv, http.MethodPost, base+"/query", map[string]any{"nl": "what is the meaning of life"})
	if msg, _ := body["error"].(string); strings.Contains(msg, "another company") {
		t.Errorf("gibberish got the cross-tenant wording — the branch is not discriminating: %q", msg)
	}
}
