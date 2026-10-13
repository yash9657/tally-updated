package server_test

import (
	"net/http"
	"testing"
)

// TestInsightsOverHTTP drives the product path: a credit-limited party
// crosses its limit with a voucher posted over HTTP, "run now" drains the
// queue the trigger filled, the inbox lists the insight with its figures, the
// evidence re-runs, a verdict dismisses it, and the digest is composed once.
func TestInsightsOverHTTP(t *testing.T) {
	srv := newServer(t)
	_, base := newCompany(t, srv)
	sales := createLedger(t, srv, base, "Sales A", "Sales Accounts")
	rec, body := do(t, srv, http.MethodPost, base+"/ledgers", map[string]any{
		"name": "Ganga Stores", "group_name": "Sundry Debtors", "credit_limit": "1000", "created_by": "t",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create limited ledger = %d %s", rec.Code, rec.Body.String())
	}
	ganga, _ := body["ledger_id"].(string)
	if rec, _ := do(t, srv, http.MethodPost, base+"/ledgers", map[string]any{
		"name": "Bad Limit", "group_name": "Sundry Debtors", "credit_limit": "-5", "created_by": "t",
	}); rec.Code != http.StatusBadRequest {
		t.Errorf("a negative credit_limit = %d, want 400", rec.Code)
	}
	rec, _ = do(t, srv, http.MethodPost, base+"/vouchers/sales", map[string]any{
		"voucher_date": "2026-04-10", "created_by": "t", "party_ledger_id": ganga,
		"postings": []map[string]any{
			{"ledger_id": ganga, "dr_cr": "D", "amount": "1500.0000"},
			{"ledger_id": sales, "dr_cr": "C", "amount": "1500.0000"},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("post sale = %d %s", rec.Code, rec.Body.String())
	}
	rec, body = do(t, srv, http.MethodPost, base+"/insights/run", map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("run = %d %s", rec.Code, rec.Body.String())
	}
	rec, body = do(t, srv, http.MethodGet, base+"/insights", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d", rec.Code)
	}
	list, _ := body["insights"].([]any)
	var breach map[string]any
	for _, x := range list {
		m := x.(map[string]any)
		if m["type"] == "credit_limit_breach" {
			breach = m
		}
	}
	if breach == nil {
		t.Fatalf("no credit_limit_breach insight after the sale: %s", rec.Body.String())
	}
	payload := breach["payload"].(map[string]any)
	if payload["excess"] != "500.00" || payload["balance"] != "1500.00" || payload["credit_limit"] != "1000.00" {
		t.Errorf("payload %v, want balance 1500.00 limit 1000.00 excess 500.00", payload)
	}
	if owed, _ := body["owed"].([]any); len(owed) == 0 {
		t.Error("the list does not say what is owed")
	}
	id := breach["id"].(string)
	rec, body = do(t, srv, http.MethodGet, base+"/insights/"+id+"/evidence", nil)
	if rec.Code != http.StatusOK || body["reproduces"] != true {
		t.Fatalf("evidence = %d %v", rec.Code, body)
	}
	rec, body = do(t, srv, http.MethodPost, base+"/insights/digest", map[string]any{})
	if rec.Code != http.StatusOK || body["narrator"] != "template" {
		t.Fatalf("digest = %d %v", rec.Code, body)
	}
	first := body["id"]
	if items, _ := body["items"].([]any); len(items) == 0 || len(items) > 5 {
		t.Errorf("digest items %d", len(items))
	}
	if _, again := do(t, srv, http.MethodPost, base+"/insights/digest", map[string]any{}); again["id"] != first {
		t.Error("a second compose made a second digest")
	}
	rec, body = do(t, srv, http.MethodPost, base+"/insights/"+id+"/feedback", map[string]any{"verdict": "wrong"})
	if rec.Code != http.StatusOK || body["state"] != "dismissed" {
		t.Fatalf("feedback = %d %v", rec.Code, body)
	}
	if rec, _ := do(t, srv, http.MethodPost, base+"/insights/"+id+"/feedback", map[string]any{"verdict": "meh"}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("unknown verdict = %d, want 422", rec.Code)
	}
	rec, body = do(t, srv, http.MethodPost, base+"/insights/suppressions", map[string]any{"type": "credit_limit_breach", "reason": "reviewing limits"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("suppress = %d %s", rec.Code, rec.Body.String())
	}
	sid := body["suppression_id"].(string)
	if rec, _ := do(t, srv, http.MethodDelete, base+"/insights/suppressions/"+sid, nil); rec.Code != http.StatusNoContent {
		t.Errorf("unsuppress = %d", rec.Code)
	}
}
