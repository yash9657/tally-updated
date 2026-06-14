package server_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/server"
)

// postFrozenSale creates a company + ledgers and posts a balanced Sales voucher,
// returning the base path and the voucher id. Shared by the freeze HTTP tests.
func postFrozenSale(t *testing.T, srv *server.Server) (string, string) {
	t.Helper()
	_, base := newCompany(t, srv)
	debtorID := createLedger(t, srv, base, "Acme Corp", "Sundry Debtors")
	salesID := createLedger(t, srv, base, "Domestic Sales", "Sales Accounts")

	rec, body := do(t, srv, http.MethodPost, base+"/vouchers/sales", map[string]any{
		"voucher_no": "EINV-1", "voucher_date": "2025-05-10", "party": "Acme Corp", "created_by": "smoke",
		"postings": []map[string]any{
			{"ledger_id": debtorID, "dr_cr": "D", "amount": "1000.0000"},
			{"ledger_id": salesID, "dr_cr": "C", "amount": "1000.0000"},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("post sale status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	voucherID, _ := body["voucher_id"].(string)
	if voucherID == "" {
		t.Fatalf("post sale returned no voucher_id (body %s)", rec.Body.String())
	}
	return base, voucherID
}

// TestRegisterExternalReference_HTTP verifies the freeze-hook POST pins the
// number (201), is idempotent, and that an unknown voucher is a 404.
func TestRegisterExternalReference_HTTP(t *testing.T) {
	srv := newServer(t)
	base, voucherID := postFrozenSale(t, srv)

	rec, body := do(t, srv, http.MethodPost, base+"/vouchers/"+voucherID+"/external-reference", map[string]any{
		"kind": "IRN", "ref_value": "IRN-HTTP-1", "created_by": "statutoryd",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	if body["pinned_voucher_no"] != "EINV-1" {
		t.Fatalf("pinned_voucher_no = %v, want EINV-1", body["pinned_voucher_no"])
	}
	if body["kind"] != "IRN" || body["ref_value"] != "IRN-HTTP-1" {
		t.Fatalf("register body = %v", body)
	}

	// Idempotent re-POST returns the same pinned number.
	rec2, body2 := do(t, srv, http.MethodPost, base+"/vouchers/"+voucherID+"/external-reference", map[string]any{
		"kind": "IRN", "ref_value": "IRN-HTTP-1",
	})
	if rec2.Code != http.StatusCreated {
		t.Fatalf("re-register status = %d, want 201 (body %s)", rec2.Code, rec2.Body.String())
	}
	if body2["pinned_voucher_no"] != body["pinned_voucher_no"] {
		t.Fatalf("re-register pinned %v, want %v", body2["pinned_voucher_no"], body["pinned_voucher_no"])
	}

	// Unknown voucher -> 404.
	unknown, _ := uuid.NewV7()
	rec3, _ := do(t, srv, http.MethodPost, base+"/vouchers/"+unknown.String()+"/external-reference", map[string]any{
		"kind": "IRN", "ref_value": "IRN-X",
	})
	if rec3.Code != http.StatusNotFound {
		t.Fatalf("register unknown voucher status = %d, want 404 (body %s)", rec3.Code, rec3.Body.String())
	}

	// Bad kind -> 400.
	rec4, _ := do(t, srv, http.MethodPost, base+"/vouchers/"+voucherID+"/external-reference", map[string]any{
		"kind": "NONSENSE", "ref_value": "x",
	})
	if rec4.Code != http.StatusBadRequest {
		t.Fatalf("register bad kind status = %d, want 400 (body %s)", rec4.Code, rec4.Body.String())
	}
}

// TestAlterDeleteFrozenVoucher_HTTP verifies a PUT/DELETE of a frozen voucher is
// 409 with a frozen{} body, and the reconciliation-queue GET then lists it.
func TestAlterDeleteFrozenVoucher_HTTP(t *testing.T) {
	srv := newServer(t)
	base, voucherID := postFrozenSale(t, srv)

	// Freeze it.
	rec, _ := do(t, srv, http.MethodPost, base+"/vouchers/"+voucherID+"/external-reference", map[string]any{
		"kind": "IRN", "ref_value": "IRN-409", "created_by": "statutoryd",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}

	// Need the ledger ids to send a full alter restatement. Re-fetch the voucher.
	_, vbody := do(t, srv, http.MethodGet, base+"/vouchers/"+voucherID, nil)
	postings, _ := vbody["postings"].([]any)
	if len(postings) < 2 {
		t.Fatalf("expected >=2 postings on fetched voucher, got %v", vbody["postings"])
	}
	p0 := postings[0].(map[string]any)
	p1 := postings[1].(map[string]any)

	// PUT alter -> 409 with frozen{}.
	rec, body := do(t, srv, http.MethodPut, base+"/vouchers/sales/"+voucherID, map[string]any{
		"voucher_no": "EINV-1", "voucher_date": "2025-05-10", "party": "Acme Corp", "created_by": "smoke",
		"postings": []map[string]any{
			{"ledger_id": p0["ledger_id"], "dr_cr": p0["dr_cr"], "amount": "1200.0000"},
			{"ledger_id": p1["ledger_id"], "dr_cr": p1["dr_cr"], "amount": "1200.0000"},
		},
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("alter frozen status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	frozen, _ := body["frozen"].(map[string]any)
	if frozen == nil || frozen["kind"] != "IRN" || frozen["ref_value"] != "IRN-409" || frozen["pinned_voucher_no"] != "EINV-1" {
		t.Fatalf("alter 409 frozen body = %v", body["frozen"])
	}

	// DELETE -> 409 too.
	rec, body = do(t, srv, http.MethodDelete, base+"/vouchers/"+voucherID, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete frozen status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	frozen, _ = body["frozen"].(map[string]any)
	if frozen == nil || frozen["kind"] != "IRN" {
		t.Fatalf("delete 409 frozen body = %v", body["frozen"])
	}

	// Reconciliation queue lists the refused attempts (ALTER + DELETE).
	rec, body = do(t, srv, http.MethodGet, base+"/reconciliation-queue", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("recon queue status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	entries, _ := body["entries"].([]any)
	if len(entries) < 2 {
		t.Fatalf("recon queue should list >=2 entries (ALTER+DELETE), got %v", body["entries"])
	}
	var sawAlter, sawDelete bool
	for _, raw := range entries {
		e := raw.(map[string]any)
		if e["voucher_id"] != voucherID {
			continue
		}
		if e["frozen_voucher_no"] != "EINV-1" || e["kind"] != "IRN" || e["ref_value"] != "IRN-409" {
			t.Fatalf("recon entry has wrong frozen ref: %v", e)
		}
		switch e["attempted_op"] {
		case "ALTER":
			sawAlter = true
		case "DELETE":
			sawDelete = true
		}
	}
	if !sawAlter || !sawDelete {
		t.Fatalf("recon queue missing ALTER (%v) or DELETE (%v): %v", sawAlter, sawDelete, entries)
	}
}
