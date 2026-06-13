// Package server_test is the HTTP smoke test for the Phase 1 REST surface. It
// drives the real chi router through httptest (no socket) against the live
// Postgres, exercising the end-to-end path a frontend uses: health check,
// company creation, master creation, a balanced Sales voucher post, and a
// report read. It proves the composition root wires the modules together and
// that every write still flows through the tenant-scoped GUC guard.
package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/server"
	"github.com/khata/cored/migrations"
)

const (
	adminDSN = "postgres://postgres:postgres@localhost:5433/tally?sslmode=disable"
	appDSN   = "postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable"
)

// newServer applies migrations (idempotent) and returns a wired Server. It
// skips the test when Postgres is unavailable so the suite stays green offline.
func newServer(t *testing.T) *server.Server {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped (CORED_SKIP_DB set)")
	}
	ctx := context.Background()

	adminConn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable, skipping smoke test: %v", err)
	}
	if err := migrations.Apply(ctx, adminConn); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("apply migrations: %v", err)
	}
	_ = adminConn.Close(ctx)

	srv, err := server.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	t.Cleanup(srv.Close)
	return srv
}

// do issues an in-process request against the router and returns the response
// recorder plus the decoded JSON body.
func do(t *testing.T, srv *server.Server, method, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	var decoded map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("%s %s: decode response %q: %v", method, path, rec.Body.String(), err)
		}
	}
	return rec, decoded
}

// TestHealthz asserts the health endpoint reports ok against a live DB.
func TestHealthz(t *testing.T) {
	srv := newServer(t)
	rec, body := do(t, srv, http.MethodGet, "/healthz", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if body["status"] != "ok" {
		t.Fatalf("healthz status field = %v, want ok", body["status"])
	}
}

// TestPostSaleEndToEnd creates a company and ledgers, posts a balanced Sales
// voucher through the REST endpoint, and reads the Trial Balance back — the
// full Phase 1 happy path over HTTP.
func TestPostSaleEndToEnd(t *testing.T) {
	srv := newServer(t)

	// Unique company per run so tenant-isolated tables never collide.
	companyID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new company id: %v", err)
	}
	base := "/v1/companies/" + companyID.String()

	// 1. Create the company (seeds the 28 groups, Cash + P&L ledgers, Sales VT).
	rec, body := do(t, srv, http.MethodPost, "/v1/companies", map[string]any{
		"id":            companyID.String(),
		"name":          "Smoke Co " + companyID.String()[:8],
		"fy_start_date": "2025-04-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create company status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	if body["company_id"] != companyID.String() {
		t.Fatalf("create company returned %v, want %s", body["company_id"], companyID)
	}

	// 2. Create the debtor and a domestic-sales ledger.
	debtorID := createLedger(t, srv, base, "Acme Corp", "Sundry Debtors")
	salesID := createLedger(t, srv, base, "Domestic Sales", "Sales Accounts")

	// 3. Post a balanced Sales voucher: debtor Dr 1000 / sales Cr 1000.
	rec, body = do(t, srv, http.MethodPost, base+"/vouchers/sales", map[string]any{
		"voucher_no":   "S-1",
		"voucher_date": "2025-05-10",
		"narration":    "Smoke sale",
		"party":        "Acme Corp",
		"created_by":   "smoke",
		"postings": []map[string]any{
			{"ledger_id": debtorID, "dr_cr": "D", "amount": "1000.0000"},
			{"ledger_id": salesID, "dr_cr": "C", "amount": "1000.0000"},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("post sale status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	if body["voucher_id"] == nil || body["voucher_id"] == "" {
		t.Fatalf("post sale returned no voucher_id (body %s)", rec.Body.String())
	}
	if vn, ok := body["version_no"].(float64); !ok || vn != 1 {
		t.Fatalf("post sale version_no = %v, want 1", body["version_no"])
	}

	// 4. An unbalanced post must be rejected (422), proving validation is wired.
	rec, _ = do(t, srv, http.MethodPost, base+"/vouchers/sales", map[string]any{
		"voucher_no":   "S-2",
		"voucher_date": "2025-05-11",
		"created_by":   "smoke",
		"postings": []map[string]any{
			{"ledger_id": debtorID, "dr_cr": "D", "amount": "1000.0000"},
			{"ledger_id": salesID, "dr_cr": "C", "amount": "999.0000"},
		},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unbalanced post status = %d, want 422 (body %s)", rec.Code, rec.Body.String())
	}

	// 5. Read the Trial Balance back; it must balance and total 1000.
	rec, body = do(t, srv, http.MethodGet, base+"/trial-balance", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("trial balance status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if body["balanced"] != true {
		t.Fatalf("trial balance not balanced (body %s)", rec.Body.String())
	}
	if got := body["total_debit"]; got != "1000.0000" {
		t.Fatalf("trial balance total_debit = %v, want 1000.0000", got)
	}
}

// createLedger POSTs a ledger and returns its id, failing the test on a non-201.
func createLedger(t *testing.T, srv *server.Server, base, name, group string) string {
	t.Helper()
	rec, body := do(t, srv, http.MethodPost, base+"/ledgers", map[string]any{
		"name":       name,
		"group_name": group,
		"created_by": "smoke",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create ledger %q status = %d, want 201 (body %s)", name, rec.Code, rec.Body.String())
	}
	id, _ := body["ledger_id"].(string)
	if id == "" {
		t.Fatalf("create ledger %q returned no id (body %s)", name, rec.Body.String())
	}
	return id
}
