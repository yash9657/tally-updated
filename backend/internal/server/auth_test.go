package server_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/server"
)

// TestNoTokenIs401 proves an authed route with NO Authorization header returns
// 401 (the unauthenticated contract), distinct from the 403 used for denials.
func TestNoTokenIs401(t *testing.T) {
	srv := newServer(t)
	companyID := uuid.NewString()
	// doRaw with empty token = no Authorization header.
	rec, _ := doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyID+"/trial-balance", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no-token request status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
	}
	// POST /v1/companies also requires a token.
	rec, _ = doRaw(t, srv, http.MethodPost, "/v1/companies", "", map[string]any{"name": "X", "fy_start_date": "2025-04-01"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("create company no-token status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestInvalidTokenIs401 proves a garbage/forged Bearer is 401, not 403.
func TestInvalidTokenIs401(t *testing.T) {
	srv := newServer(t)
	companyID := uuid.NewString()
	rec, _ := doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyID+"/trial-balance", "not-a-jwt", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("invalid-token status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestUnprovisionedUserTokenIs403 proves a VALID token whose subject has never
// been provisioned in cored (no app_user row, e.g. a brand-new IdP user who has
// not created a company) is 403, NOT 401: a cryptographically valid token is
// authenticated, so an access failure is an authorization (403) outcome. 401 is
// reserved for missing/invalid/expired tokens. This is the contract the live
// statutoryd e2e exercises with its "intruder" subject.
func TestUnprovisionedUserTokenIs403(t *testing.T) {
	srv := newServer(t)

	// A real company exists (created by some other Owner).
	owner := mintToken(t, srv, "owner-"+uuid.NewString(), "webauthn")
	companyID := createCompanyAs(t, srv, owner)

	// A valid token for a subject that has NEVER touched cored.
	intruder := mintToken(t, srv, "intruder-"+uuid.NewString(), "webauthn")
	rec, _ := doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyID+"/reconciliation-queue", intruder, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unprovisioned-user status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestCrossTenantTokenIs403 proves a VALID token for a user who is NOT a member
// of the target company is 403 (membership gate), distinct from the 401 above.
func TestCrossTenantTokenIs403(t *testing.T) {
	srv := newServer(t)

	// Owner A creates company A (becomes Owner member).
	subjectA := "tenantA-" + uuid.NewString()
	tokenA := mintToken(t, srv, subjectA, "webauthn")
	companyA := createCompanyAs(t, srv, tokenA)

	// Owner B creates company B.
	subjectB := "tenantB-" + uuid.NewString()
	tokenB := mintToken(t, srv, subjectB, "webauthn")
	companyB := createCompanyAs(t, srv, tokenB)

	// A's token against company B -> 403 (not a member).
	rec, _ := doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyB+"/trial-balance", tokenA, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant A->B status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	// Symmetric: B's token against company A -> 403.
	rec, _ = doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyA+"/trial-balance", tokenB, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant B->A status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	// And each token works against its OWN company.
	rec, _ = doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyA+"/trial-balance", tokenA, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("own-tenant A->A status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestServiceTokenBypass proves a svc:* token gets full access to any company it
// targets (so statutoryd's freeze hook + sync work), scoped to that company.
func TestServiceTokenBypass(t *testing.T) {
	srv := newServer(t)

	subjectA := "svc-owner-" + uuid.NewString()
	tokenA := mintToken(t, srv, subjectA, "webauthn")
	companyA := createCompanyAs(t, srv, tokenA)

	// Create ledgers + a sale as the owner so there's a voucher to freeze.
	base := "/v1/companies/" + companyA
	debtorID := createLedgerAs(t, srv, base, tokenA, "Acme Corp", "Sundry Debtors")
	salesID := createLedgerAs(t, srv, base, tokenA, "Domestic Sales", "Sales Accounts")
	rec, body := doRaw(t, srv, http.MethodPost, base+"/vouchers/sales", tokenA, map[string]any{
		"voucher_no": "SVC-1", "voucher_date": "2025-05-10", "party": "Acme Corp", "created_by": "owner",
		"postings": []map[string]any{
			{"ledger_id": debtorID, "dr_cr": "D", "amount": "1000.0000"},
			{"ledger_id": salesID, "dr_cr": "C", "amount": "1000.0000"},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("post sale status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	voucherID, _ := body["voucher_id"].(string)

	// A service token (NOT a member of company A) can freeze the voucher.
	svcToken := mintToken(t, srv, "svc:statutoryd")
	rec, _ = doRaw(t, srv, http.MethodPost, base+"/vouchers/"+voucherID+"/external-reference", svcToken, map[string]any{
		"kind": "IRN", "ref_value": "IRN-SVC-1", "created_by": "statutoryd",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("svc freeze status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
}

// createCompanyAs creates a unique company with the given token and returns its
// id (the caller becomes Owner).
func createCompanyAs(t *testing.T, srv *server.Server, token string) string {
	t.Helper()
	companyID := uuid.NewString()
	rec, body := doRaw(t, srv, http.MethodPost, "/v1/companies", token, map[string]any{
		"id":            companyID,
		"name":          "Auth Co " + companyID[:8],
		"fy_start_date": "2025-04-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create company status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	if body["company_id"] != companyID {
		t.Fatalf("create company returned %v, want %s", body["company_id"], companyID)
	}
	return companyID
}

// createLedgerAs POSTs a ledger with an explicit token.
func createLedgerAs(t *testing.T, srv *server.Server, base, token, name, group string) string {
	t.Helper()
	rec, body := doRaw(t, srv, http.MethodPost, base+"/ledgers", token, map[string]any{
		"name": name, "group_name": group, "created_by": "owner",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create ledger %q status = %d, want 201 (body %s)", name, rec.Code, rec.Body.String())
	}
	id, _ := body["ledger_id"].(string)
	return id
}

// TestGSTR3BRouteAuthMatrix pins the two-touch rule (IMPLEMENTATION_PLAN §1.5)
// for GET /v1/companies/{companyID}/gstr3b: no token -> 401, a valid token for a
// DIFFERENT tenant -> 403, Owner -> 200. If only the route registration lands and
// the reportName() entry is missing, the Owner case is 403 and this test fails.
func TestGSTR3BRouteAuthMatrix(t *testing.T) {
	srv := newServer(t)

	owner := ownerToken(t, srv)
	companyID := createCompanyAs(t, srv, owner)
	path := "/v1/companies/" + companyID + "/gstr3b?from=2025-05-01&to=2025-05-31"

	// No Authorization header -> 401.
	if rec, _ := doRaw(t, srv, http.MethodGet, path, "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no-token gstr3b status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
	}

	// A valid token for a user who is not a member of companyID -> 403.
	otherToken := mintToken(t, srv, "gstr3b-other-"+uuid.NewString(), "webauthn")
	_ = createCompanyAs(t, srv, otherToken) // provision the other subject
	if rec, _ := doRaw(t, srv, http.MethodGet, path, otherToken, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant gstr3b status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}

	// Owner -> 200 with the 3B shape.
	rec, body := doRaw(t, srv, http.MethodGet, path, owner, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner gstr3b status = %d, want 200 (body %s) — a 403 here means the reportName() /gstr3b entry is missing", rec.Code, rec.Body.String())
	}
	if body["fp"] != "052025" {
		t.Fatalf("gstr3b fp = %v, want 052025 (body %s)", body["fp"], rec.Body.String())
	}
	if _, ok := body["sup_details"]; !ok {
		t.Fatalf("gstr3b response missing sup_details: %s", rec.Body.String())
	}

	// from/to are required.
	if rec, _ := doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyID+"/gstr3b", owner, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("gstr3b without from/to = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
}
