package server_test

// HTTP contract tests for GET /v1/companies/{companyID}/cash-flow and .../anomalies
// (wave-2 WS5a).
//
// !! WIRING DEPENDENCY !!  These tests exercise routes that live in the SHARED
// wiring files (server.go routes() + auth_middleware.go reportName), which WS5a is
// not permitted to edit. Until the serial wiring stage applies BOTH of:
//
//	server.go, in the "// Reports." block:
//	    r.Get("/cash-flow", s.handleCashFlow)
//	    r.Get("/anomalies", s.handleAnomalies)
//
//	auth_middleware.go, in reportName()'s switch:
//	    case "/cash-flow": return "cash-flow"
//	    case "/anomalies": return "anomalies"
//	    (routeAction's generic report arm maps these to Display/report:<name>)
//
// ...the Owner-200 cases FAIL with 403 (routeAction default-denies unmapped
// {companyID} sub-paths). That failure is the intended two-touch signal (§1.5),
// not a bug in this file.
//
// Coverage note (copied from handler_company_get_test.go): the cross-tenant 403
// is served by the membership gate, which runs BEFORE routeAction, so ONLY the
// Owner-200 cases actually prove the report:cash-flow / report:anomalies PDP
// mappings exist.

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/server"
)

// createPlainCompany creates a company (the caller becomes Owner) and returns its
// base path.
func createPlainCompany(t *testing.T, srv *server.Server, token string) string {
	t.Helper()
	companyID := uuid.NewString()
	rec, _ := doRaw(t, srv, http.MethodPost, "/v1/companies", token, map[string]any{
		"id": companyID, "name": "Forecast Co " + companyID[:8], "fy_start_date": "2025-04-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create company status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	return "/v1/companies/" + companyID
}

func TestCashFlowOwner200(t *testing.T) {
	srv := newServer(t)
	owner := mintToken(t, srv, "owner-"+uuid.NewString(), "webauthn")
	base := createPlainCompany(t, srv, owner)

	rec, body := doRaw(t, srv, http.MethodGet, base+"/cash-flow?as_on=2025-05-01&horizon_days=30&bucket=week", owner, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("cash-flow status = %d, want 200 (body %s).\nIf 403/404, the two-touch wiring is missing — see this file's header.", rec.Code, rec.Body.String())
	}
	// A fresh company has no bills: buckets exist, all flows zero, opening 0.
	if _, ok := body["buckets"].([]any); !ok {
		t.Fatalf("cash-flow missing buckets (body %s)", rec.Body.String())
	}
	if got, _ := body["opening_cash"].(string); got != "0.0000" {
		t.Errorf("opening_cash = %q, want 0.0000", got)
	}
}

func TestAnomaliesOwner200(t *testing.T) {
	srv := newServer(t)
	owner := mintToken(t, srv, "owner-"+uuid.NewString(), "webauthn")
	base := createPlainCompany(t, srv, owner)

	rec, body := doRaw(t, srv, http.MethodGet, base+"/anomalies", owner, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("anomalies status = %d, want 200 (body %s).\nIf 403/404, the two-touch wiring is missing — see this file's header.", rec.Code, rec.Body.String())
	}
	if _, ok := body["findings"].([]any); !ok {
		t.Fatalf("anomalies missing findings array (body %s)", rec.Body.String())
	}
}

func TestForecastCrossTenantIs403(t *testing.T) {
	srv := newServer(t)
	tokenA := mintToken(t, srv, "tenantA-"+uuid.NewString(), "webauthn")
	baseA := createPlainCompany(t, srv, tokenA)
	tokenB := mintToken(t, srv, "tenantB-"+uuid.NewString(), "webauthn")

	for _, path := range []string{"/cash-flow", "/anomalies"} {
		rec, _ := doRaw(t, srv, http.MethodGet, baseA+path, tokenB, nil)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("cross-tenant %s status = %d, want 403 (body %s)", path, rec.Code, rec.Body.String())
		}
	}
}

func TestForecastNoTokenIs401(t *testing.T) {
	srv := newServer(t)
	owner := mintToken(t, srv, "owner-"+uuid.NewString(), "webauthn")
	base := createPlainCompany(t, srv, owner)

	for _, path := range []string{"/cash-flow", "/anomalies"} {
		rec, _ := doRaw(t, srv, http.MethodGet, base+path, "", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("no-token %s status = %d, want 401 (body %s)", path, rec.Code, rec.Body.String())
		}
	}
}
