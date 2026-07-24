package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/server"
)

// dpSeedSale posts a 400.0000 sales voucher over HTTP and returns its id. It is
// the shared fixture for the two delete/alter-policy HTTP tests.
func dpSeedSale(t *testing.T, srv *server.Server, base string) (voucherID, debtorID, salesID string) {
	t.Helper()
	debtorID = createLedger(t, srv, base, "Acme Corp", "Sundry Debtors")
	salesID = createLedger(t, srv, base, "Domestic Sales", "Sales Accounts")

	rec, body := do(t, srv, http.MethodPost, base+"/vouchers/sales", map[string]any{
		"voucher_no": "DP-1", "voucher_date": "2025-04-10", "party": "Acme Corp", "created_by": "smoke",
		"postings": []map[string]any{
			{"ledger_id": debtorID, "dr_cr": "D", "amount": "400.0000"},
			{"ledger_id": salesID, "dr_cr": "C", "amount": "400.0000"},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("post sale status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	voucherID, _ = body["voucher_id"].(string)
	if voucherID == "" {
		t.Fatalf("post sale returned no voucher_id (body %s)", rec.Body.String())
	}
	return voucherID, debtorID, salesID
}

// dpRawVersionCount counts voucher_version rows for a voucher using a DIRECT
// admin connection — an oracle completely outside the HTTP surface under test,
// so "no new tombstone was written" cannot be satisfied by the handler echoing
// itself.
func dpRawVersionCount(t *testing.T, voucherID string) int {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var n int
	if err := conn.QueryRow(ctx,
		`SELECT count(*) FROM voucher_version WHERE voucher_id = $1`, voucherID).Scan(&n); err != nil {
		t.Fatalf("count versions: %v", err)
	}
	return n
}

// dpRawIsDeleted reads voucher_head.is_deleted directly (same rationale).
func dpRawIsDeleted(t *testing.T, voucherID string) bool {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var deleted bool
	if err := conn.QueryRow(ctx,
		`SELECT is_deleted FROM voucher_head WHERE voucher_id = $1`, voucherID).Scan(&deleted); err != nil {
		t.Fatalf("read head: %v", err)
	}
	return deleted
}

// TestHTTPDeletePolicy_DoubleDeleteIsIdempotent200 pins the HTTP contract for a
// retried DELETE: 200 with a body byte-identical to the first delete's, and NO
// extra version in the log. This is the retry-safety guarantee flaky clients and
// offline replay depend on.
func TestHTTPDeletePolicy_DoubleDeleteIsIdempotent200(t *testing.T) {
	srv := newServer(t)
	_, base := newCompany(t, srv)
	voucherID, _, _ := dpSeedSale(t, srv, base)

	rec, first := do(t, srv, http.MethodDelete, base+"/vouchers/"+voucherID+"?created_by=deleter", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("first delete status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if first["op"] != "DELETE" {
		t.Fatalf("first delete op = %v, want DELETE", first["op"])
	}
	if vn, ok := first["version_no"].(float64); !ok || vn != 2 {
		t.Fatalf("first delete version_no = %v, want 2", first["version_no"])
	}
	if n := dpRawVersionCount(t, voucherID); n != 2 {
		t.Fatalf("after first delete: %d versions, want 2", n)
	}

	rec, second := do(t, srv, http.MethodDelete, base+"/vouchers/"+voucherID+"?created_by=deleter-retry", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-delete status = %d, want 200 idempotent (body %s)", rec.Code, rec.Body.String())
	}
	for _, k := range []string{"voucher_id", "voucher_version_id", "version_no", "voucher_no", "op"} {
		if second[k] != first[k] {
			t.Fatalf("re-delete body[%s] = %v, want the first tombstone's %v", k, second[k], first[k])
		}
	}
	if n := dpRawVersionCount(t, voucherID); n != 2 {
		t.Fatalf("re-delete must NOT append a version: %d versions, want 2", n)
	}
	if !dpRawIsDeleted(t, voucherID) {
		t.Fatal("head must stay tombstoned after a re-delete")
	}

	// The books stay flat and the voucher stays invisible.
	rec, tb := do(t, srv, http.MethodGet, base+"/trial-balance", nil)
	if rec.Code != http.StatusOK || tb["total_debit"] != "0.0000" {
		t.Fatalf("trial balance after double delete = %v, want 0.0000 (body %s)", tb["total_debit"], rec.Body.String())
	}
	rec, _ = do(t, srv, http.MethodGet, base+"/vouchers/"+voucherID, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET after double delete = %d, want 404", rec.Code)
	}
}

// TestHTTPDeletePolicy_AlterAfterDelete404 pins the other half over HTTP: a PUT
// against a tombstoned voucher is a 404 (writeVoucherEditError's mapping of
// ledger.ErrVoucherNotFound), the voucher STAYS deleted, and the trial balance
// does not resurrect the amount.
func TestHTTPDeletePolicy_AlterAfterDelete404(t *testing.T) {
	srv := newServer(t)
	_, base := newCompany(t, srv)
	voucherID, debtorID, salesID := dpSeedSale(t, srv, base)

	rec, del := do(t, srv, http.MethodDelete, base+"/vouchers/"+voucherID+"?created_by=deleter", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	rec, _ = do(t, srv, http.MethodPut, base+"/vouchers/sales/"+voucherID, map[string]any{
		"voucher_no": "DP-1", "voucher_date": "2025-04-10", "party": "Acme Corp", "created_by": "smoke",
		"postings": []map[string]any{
			{"ledger_id": debtorID, "dr_cr": "D", "amount": "9999.0000"},
			{"ledger_id": salesID, "dr_cr": "C", "amount": "9999.0000"},
		},
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("alter of a DELETED voucher = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}

	if n := dpRawVersionCount(t, voucherID); n != 2 {
		t.Fatalf("refused alter must NOT append a version: %d versions, want 2", n)
	}
	if !dpRawIsDeleted(t, voucherID) {
		t.Fatal("a refused alter must not resurrect the voucher")
	}
	rec, tb := do(t, srv, http.MethodGet, base+"/trial-balance", nil)
	if rec.Code != http.StatusOK || tb["total_debit"] != "0.0000" {
		t.Fatalf("trial balance after refused alter = %v, want 0.0000 (a resurrection would show 9999)", tb["total_debit"])
	}
	rec, _ = do(t, srv, http.MethodGet, base+"/vouchers/"+voucherID, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET after refused alter = %d, want 404", rec.Code)
	}

	// A second DELETE still echoes the ORIGINAL tombstone (the refused alter
	// changed nothing about the head).
	rec, again := do(t, srv, http.MethodDelete, base+"/vouchers/"+voucherID+"?created_by=deleter", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-delete after refused alter = %d, want 200", rec.Code)
	}
	if again["voucher_version_id"] != del["voucher_version_id"] {
		t.Fatalf("re-delete echoed %v, want the original tombstone %v",
			again["voucher_version_id"], del["voucher_version_id"])
	}
}

// TestHTTPDeletePolicy_UnknownVoucherStill404 guards the boundary: the new
// idempotency path must not turn a DELETE of a NEVER-created id into a 200.
func TestHTTPDeletePolicy_UnknownVoucherStill404(t *testing.T) {
	srv := newServer(t)
	_, base := newCompany(t, srv)

	unknown, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new uuid: %v", err)
	}
	rec, _ := do(t, srv, http.MethodDelete, base+"/vouchers/"+unknown.String()+"?created_by=deleter", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete of an unknown voucher = %d, want 404 (idempotency must not swallow it)", rec.Code)
	}
}
