package server_test

// HTTP contract tests for GET /v1/companies/{companyID} (WS8a).
//
// !! WIRING DEPENDENCY !!  These tests exercise a route that lives in the
// SHARED wiring files (server.go routes() + auth_middleware.go routeAction),
// which WS8a is not permitted to edit. Until the wave-1 serial wiring stage
// applies BOTH of:
//
//	server.go, inside r.Route("/v1/companies/{companyID}", ...):
//	    r.Get("/", s.handleGetCompany)
//
//	auth_middleware.go, inside routeAction()'s switch:
//	    case method == http.MethodGet && (rest == "" || rest == "/"):
//	        return authz.Display, "company", "", true
//
// ...TestGetCompanyOwner200 FAILS with 403 (routeAction default-denies unmapped
// routes) or 404. That failure is the intended signal that the two-touch rule
// (§1.5) has not been satisfied yet — it is not a bug in this file.
//
// Note on coverage: the cross-tenant 403 case below is satisfied by the
// membership gate, which runs BEFORE routeAction, so ONLY the Owner-200 case
// actually proves the PDP mapping exists.

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/internal/server"
)

// createCompanyWithState creates a company carrying an explicit state_code +
// gstin and returns its id. The caller becomes Owner.
func createCompanyWithState(t *testing.T, srv *server.Server, token, stateCode, gstin string) string {
	t.Helper()
	cid := uuid.New()
	companyID := cid.String()
	rec, body := doRaw(t, srv, http.MethodPost, "/v1/companies", token, map[string]any{
		"id":            companyID,
		"name":          "State Co " + companyID[:8],
		"fy_start_date": "2025-04-01",
		"base_currency": "INR",
		"state_code":    stateCode,
		"gstin":         gstin,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create company status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	if body["company_id"] != companyID {
		t.Fatalf("create company returned %v, want %s", body["company_id"], companyID)
	}
	testdb.Cleanup(t, cid)
	return companyID
}

// TestGetCompanyOwner200 proves an Owner can READ the company master back —
// specifically the state_code the frontend needs to derive the intra/inter-state
// GST split. The oracle is INDEPENDENT of the handler twice over: the expected
// values are the ones the test POSTed, and they are additionally re-read
// straight from Postgres with an admin (RLS-bypassing) connection.
func TestGetCompanyOwner200(t *testing.T) {
	srv := newServer(t)
	owner := mintToken(t, srv, "owner-"+uuid.NewString(), "webauthn")
	const wantState, wantGSTIN = "27", "27AABCT1234M1Z5"
	companyID := createCompanyWithState(t, srv, owner, wantState, wantGSTIN)

	// NOTE: no trailing slash — the exact form the frontend REST client builds.
	rec, body := doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyID, owner, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET company status = %d, want 200 (body %s).\n"+
			"If this is 403/404 the wave-1 wiring stage has not registered the route + "+
			"routeAction mapping yet — see the header comment in this file.",
			rec.Code, rec.Body.String())
	}

	for _, tc := range []struct{ field, want string }{
		{"id", companyID},
		{"fy_start_date", "2025-04-01"},
		{"base_currency", "INR"},
		{"state_code", wantState},
		{"gstin", wantGSTIN},
	} {
		if got, _ := body[tc.field].(string); got != tc.want {
			t.Errorf("GET company %s = %q, want %q (body %s)", tc.field, got, tc.want, rec.Body.String())
		}
	}
	if name, _ := body["name"].(string); name == "" {
		t.Errorf("GET company name is empty (body %s)", rec.Body.String())
	}

	// Independent oracle: read the row directly from Postgres as the admin
	// (BYPASSRLS superuser), i.e. without going through the server at all.
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable for the independent oracle: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var dbState, dbGSTIN, dbCurrency string
	if err := conn.QueryRow(ctx,
		`SELECT state_code, gstin, base_currency FROM company WHERE id = $1`, companyID).
		Scan(&dbState, &dbGSTIN, &dbCurrency); err != nil {
		t.Fatalf("independent oracle read: %v", err)
	}
	if got, _ := body["state_code"].(string); got != dbState {
		t.Errorf("state_code over HTTP = %q, but the DB row holds %q", got, dbState)
	}
	if got, _ := body["gstin"].(string); got != dbGSTIN {
		t.Errorf("gstin over HTTP = %q, but the DB row holds %q", got, dbGSTIN)
	}
	if got, _ := body["base_currency"].(string); got != dbCurrency {
		t.Errorf("base_currency over HTTP = %q, but the DB row holds %q", got, dbCurrency)
	}
}

// TestGetCompanyCrossTenantIs403 proves a valid token for a NON-member is 403
// (membership gate), so the new read cannot be used to enumerate other tenants'
// masters (name / GSTIN / state).
func TestGetCompanyCrossTenantIs403(t *testing.T) {
	srv := newServer(t)
	tokenA := mintToken(t, srv, "tenantA-"+uuid.NewString(), "webauthn")
	companyA := createCompanyWithState(t, srv, tokenA, "29", "")
	tokenB := mintToken(t, srv, "tenantB-"+uuid.NewString(), "webauthn")

	rec, _ := doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyA, tokenB, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant GET company status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	// And the body must not have leaked the master.
	if got := rec.Body.String(); strings.Contains(got, "State Co") || strings.Contains(got, `"state_code"`) {
		t.Fatalf("cross-tenant 403 body leaked company data: %s", got)
	}
}

// TestGetCompanyNoTokenIs401 proves the unauthenticated contract (401, not 403).
func TestGetCompanyNoTokenIs401(t *testing.T) {
	srv := newServer(t)
	owner := mintToken(t, srv, "owner-"+uuid.NewString(), "webauthn")
	companyID := createCompanyWithState(t, srv, owner, "29", "")

	rec, _ := doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyID, "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no-token GET company status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
	}
}
