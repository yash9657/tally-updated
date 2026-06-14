package server_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// TestSyncCommitEndToEnd drives POST /sync/commit: two offline node CREATEs of
// the same Journal series commit through the cloud sequencer and come back with
// two DISTINCT sequential final numbers, both COMMITTED. It proves the route is
// wired and the sequencer is reachable over HTTP.
func TestSyncCommitEndToEnd(t *testing.T) {
	srv := newServer(t)
	_, base := newCompany(t, srv)

	drID := createLedger(t, srv, base, "Sync Dr", "Current Assets")
	crID := createLedger(t, srv, base, "Sync Cr", "Current Liabilities")

	idA := uuid.NewString()
	idB := uuid.NewString()
	mkIntent := func(key, cvid, node string, wall int64, amount string) map[string]any {
		return map[string]any{
			"idempotency_key":   key,
			"client_voucher_id": cvid,
			"op":                "CREATE",
			"class":             "Journal",
			"node_id":           node,
			"hlc":               map[string]any{"wall_millis": wall, "counter": 0, "node_id": node},
			"base_version":      0,
			"payload": map[string]any{
				"voucher_date": "2025-05-20",
				"narration":    "offline create",
				"created_by":   node,
				"postings": []map[string]any{
					{"ledger_id": drID, "dr_cr": "D", "amount": amount},
					{"ledger_id": crID, "dr_cr": "C", "amount": amount},
				},
			},
		}
	}

	body := map[string]any{"intents": []map[string]any{
		mkIntent("A", idA, "nodeA", 100, "100.0000"),
		mkIntent("B", idB, "nodeB", 101, "200.0000"),
	}}

	rec, decoded := do(t, srv, http.MethodPost, base+"/sync/commit", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("sync commit status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	outcomes, ok := decoded["outcomes"].([]any)
	if !ok || len(outcomes) != 2 {
		t.Fatalf("expected 2 outcomes, got %v (body %s)", decoded["outcomes"], rec.Body.String())
	}

	nums := map[string]bool{}
	for _, raw := range outcomes {
		o := raw.(map[string]any)
		if o["status"] != "COMMITTED" {
			t.Fatalf("outcome status = %v, want COMMITTED (body %s)", o["status"], rec.Body.String())
		}
		no, _ := o["final_voucher_no"].(string)
		if no == "" {
			t.Fatalf("committed create has no final_voucher_no (body %s)", rec.Body.String())
		}
		if nums[no] {
			t.Fatalf("collision: final number %q allocated twice", no)
		}
		nums[no] = true
	}

	// Re-POST the SAME batch: exactly-once replay yields all DUPLICATE.
	rec, decoded = do(t, srv, http.MethodPost, base+"/sync/commit", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-commit status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	outcomes, _ = decoded["outcomes"].([]any)
	for _, raw := range outcomes {
		o := raw.(map[string]any)
		if o["status"] != "DUPLICATE" {
			t.Fatalf("re-commit outcome status = %v, want DUPLICATE (body %s)", o["status"], rec.Body.String())
		}
	}
}
