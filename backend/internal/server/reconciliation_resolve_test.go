package server_test

// reconciliation_resolve_test.go — the two routes track D's conflict compare
// screen cannot be built without, and the formatting trap that nearly shipped
// inside one of them.
//
// WHAT WAS MISSING BEFORE THESE ROUTES
//
//	the PAYLOAD read   sync.Service.ConflictPayload has read the preserved intent
//	                   since migration 0010 and had NO caller outside two Go test
//	                   files. A client could learn THAT its offline work was
//	                   refused and never WHAT it had keyed, so the compare screen
//	                   could only ever render one of its two panes.
//	the RESOLVE write  resolved_at has been in the schema since migration 0009 and
//	                   every read filters on it, but NOTHING in the codebase ever
//	                   wrote it — no UPDATE of the table existed in Go or in SQL.
//	                   A reconciliation row was immortal and the queue could only
//	                   grow.
//
// ⛔ EVERY CONFLICT BELOW IS A REAL ONE, produced by committing a stale
// base_version through POST /sync/commit. None is stubbed, and none asserts a
// non-terminal status name.
//
// ⚠️ THE TEST THAT MATTERS MOST IS TestParkedMoneyIsFormattedLikeTheLiveVoucher.
// The column stores json.Marshal(sync.Intent) verbatim and returning those bytes
// is the obvious implementation — but ledger.VoucherInput carries no json tags
// and decimal.Decimal marshals bare, so a posting of 1180 stores as "1180" while
// the LIVE side of the comparison renders "1180.0000". A compare screen fed the
// raw bytes shows those two side by side and invites an operator to discard
// their work over a difference that does not exist. That test is the only thing
// standing between this route and that regression.

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/server"
)

// syncConflict drives a REAL stale-base conflict and returns the base path, the
// voucher id, the reconciliation id, and the provisional number the loser
// carried. Three commits: a CREATE (head v1), a well-based ALTER (head v2), then
// an ALTER still based on v1 — which the sequencer parks.
func syncConflict(t *testing.T, srv *server.Server) (base, voucherID, reconID, provisional string) {
	t.Helper()
	_, base = newCompany(t, srv)
	drID := createLedger(t, srv, base, "Recon Dr", "Current Assets")
	crID := createLedger(t, srv, base, "Recon Cr", "Current Liabilities")

	cvid := uuid.NewString()
	intent := func(key, op string, baseVersion int, wall int64, amount string) map[string]any {
		return map[string]any{
			"idempotency_key":   key,
			"client_voucher_id": cvid,
			"op":                op,
			"class":             "Journal",
			"node_id":           "nodeA",
			"provisional_no":    "INV/2025-26/PROV-nodeA-" + key,
			"hlc":               map[string]any{"wall_millis": wall, "counter": 0, "node_id": "nodeA"},
			"base_version":      baseVersion,
			"payload": map[string]any{
				"voucher_date": "2025-05-20",
				"narration":    "offline " + op + " " + key,
				"created_by":   "nodeA",
				"postings": []map[string]any{
					{"ledger_id": drID, "dr_cr": "D", "amount": amount},
					{"ledger_id": crID, "dr_cr": "C", "amount": amount},
				},
			},
		}
	}

	commit := func(in map[string]any) map[string]any {
		t.Helper()
		rec, decoded := do(t, srv, http.MethodPost, base+"/sync/commit",
			map[string]any{"intents": []map[string]any{in}})
		if rec.Code != http.StatusOK {
			t.Fatalf("sync commit status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		outcomes, ok := decoded["outcomes"].([]any)
		if !ok || len(outcomes) != 1 {
			t.Fatalf("want 1 outcome, got %v", decoded["outcomes"])
		}
		return outcomes[0].(map[string]any)
	}

	if got := commit(intent("c1", "CREATE", 0, 100, "1180.0000")); got["status"] != "COMMITTED" {
		t.Fatalf("CREATE status = %v, want COMMITTED (%v)", got["status"], got)
	}
	if got := commit(intent("a1", "ALTER", 1, 200, "1180.0000")); got["status"] != "COMMITTED" {
		t.Fatalf("well-based ALTER status = %v, want COMMITTED (%v)", got["status"], got)
	}

	// Still based on version 1, which the commit above moved past: a real stale
	// base, refused and parked.
	loser := intent("a2", "ALTER", 1, 300, "9999.0000")
	got := commit(loser)
	if got["status"] != "CONFLICT" {
		t.Fatalf("stale-base ALTER status = %v, want CONFLICT (%v)", got["status"], got)
	}
	reconID, _ = got["reconciliation_id"].(string)
	if reconID == "" {
		t.Fatalf("CONFLICT carried no reconciliation_id (%v)", got)
	}
	return base, cvid, reconID, loser["provisional_no"].(string)
}

// TestConflictPayloadIsReadableOverHTTP is the route's reason to exist: the
// operator's own keyed intent, which no client could reach before.
func TestConflictPayloadIsReadableOverHTTP(t *testing.T) {
	srv := newServer(t)
	base, voucherID, reconID, provisional := syncConflict(t, srv)

	rec, body := do(t, srv, http.MethodGet, base+"/reconciliation-queue/"+reconID+"/payload", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("payload status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if body["has_payload"] != true {
		t.Fatalf("has_payload = %v, want true — a parked CONFLICT preserves the intent (body %s)",
			body["has_payload"], rec.Body.String())
	}
	if body["voucher_id"] != voucherID {
		t.Fatalf("voucher_id = %v, want %s", body["voucher_id"], voucherID)
	}
	if body["attempted_op"] != "ALTER" {
		t.Fatalf("attempted_op = %v, want ALTER", body["attempted_op"])
	}

	intent, ok := body["intent"].(map[string]any)
	if !ok {
		t.Fatalf("no intent object in body %s", rec.Body.String())
	}
	// The provisional number is the label the operator SAW offline; it is the
	// only thing tying the queue row to what they were looking at.
	if intent["provisional_no"] != provisional {
		t.Fatalf("provisional_no = %v, want %s", intent["provisional_no"], provisional)
	}
	if intent["op"] != "ALTER" || intent["node_id"] != "nodeA" {
		t.Fatalf("intent op/node = %v/%v", intent["op"], intent["node_id"])
	}
	if bv, _ := intent["base_version"].(float64); int(bv) != 1 {
		t.Fatalf("base_version = %v, want 1 — the stale base is what explains the refusal", intent["base_version"])
	}

	payload, ok := intent["payload"].(map[string]any)
	if !ok {
		t.Fatalf("no payload object in intent %v", intent)
	}
	if payload["narration"] != "offline ALTER a2" {
		t.Fatalf("narration = %v — the parked body must be the LOSER's, not the winner's", payload["narration"])
	}
	postings, ok := payload["postings"].([]any)
	if !ok || len(postings) != 2 {
		t.Fatalf("want 2 parked postings, got %v", payload["postings"])
	}
}

// TestParkedMoneyIsFormattedLikeTheLiveVoucher is the oracle for the trap in
// this route's header comment. It compares the two panes the compare screen puts
// side by side and requires the SAME spelling of the same figure.
//
// It fails if the handler ever echoes the stored JSONB instead of re-formatting:
// stored, the amount is "9999"; live, it is "9999.0000".
func TestParkedMoneyIsFormattedLikeTheLiveVoucher(t *testing.T) {
	srv := newServer(t)
	base, voucherID, reconID, _ := syncConflict(t, srv)

	// The PARKED pane.
	_, parkedBody := do(t, srv, http.MethodGet, base+"/reconciliation-queue/"+reconID+"/payload", nil)
	intent, _ := parkedBody["intent"].(map[string]any)
	payload, _ := intent["payload"].(map[string]any)
	parked, _ := payload["postings"].([]any)
	if len(parked) == 0 {
		t.Fatalf("no parked postings (body %v)", parkedBody)
	}
	parkedAmount, _ := parked[0].(map[string]any)["amount"].(string)

	// The LIVE pane — the same route the compare screen's other half uses.
	recLive, liveBody := do(t, srv, http.MethodGet, base+"/vouchers/"+voucherID, nil)
	if recLive.Code != http.StatusOK {
		t.Fatalf("live voucher status = %d, want 200 (body %s)", recLive.Code, recLive.Body.String())
	}
	live, _ := liveBody["postings"].([]any)
	if len(live) == 0 {
		t.Fatalf("no live postings (body %v)", liveBody)
	}
	liveAmount, _ := live[0].(map[string]any)["amount"].(string)

	// A PROPERTY, not a literal: both sides must carry four decimal places, so
	// the same figure reads the same way in both panes.
	if got := decimalPlaces(parkedAmount); got != 4 {
		t.Errorf("parked amount %q has %d decimal places, want 4 — the LIVE pane renders "+
			"%q, and a compare screen showing these two side by side invents a "+
			"difference that does not exist (this is what echoing the stored JSONB does)",
			parkedAmount, got, liveAmount)
	}
	if decimalPlaces(parkedAmount) != decimalPlaces(liveAmount) {
		t.Errorf("parked %q and live %q are formatted differently; the two panes are not comparable",
			parkedAmount, liveAmount)
	}
}

// decimalPlaces counts the digits after the decimal point, or 0 if there is none.
func decimalPlaces(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			return len(s) - i - 1
		}
	}
	return 0
}

// TestResolveEmptiesTheQueueAndIsNotRepeatable proves the immortality fix. The
// oracle is the QUEUE LISTING, not the UPDATE's own return value: a resolve that
// stamped nothing would still look successful from inside the handler.
func TestResolveEmptiesTheQueueAndIsNotRepeatable(t *testing.T) {
	srv := newServer(t)
	base, _, reconID, _ := syncConflict(t, srv)

	// The entry is OPEN and listed.
	_, before := do(t, srv, http.MethodGet, base+"/reconciliation-queue", nil)
	if !queueLists(before, reconID) {
		t.Fatalf("the parked conflict is not in the open queue (body %v)", before)
	}

	rec, body := do(t, srv, http.MethodPost, base+"/reconciliation-queue/"+reconID+"/resolve", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if body["resolved"] != true {
		t.Fatalf("resolved = %v, want true", body["resolved"])
	}

	// ⛔ THE REAL ORACLE: it is gone from the queue every read filters on.
	_, after := do(t, srv, http.MethodGet, base+"/reconciliation-queue", nil)
	if queueLists(after, reconID) {
		t.Fatalf("the entry is STILL in the open queue after a 200 resolve — resolved_at "+
			"was not stamped, which is the defect this route exists to fix (body %v)", after)
	}

	// A second resolve is a 404: the row is no longer open. The timestamp must
	// not move forward on a double-submit.
	rec2, _ := do(t, srv, http.MethodPost, base+"/reconciliation-queue/"+reconID+"/resolve", nil)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("second resolve status = %d, want 404 (body %s)", rec2.Code, rec2.Body.String())
	}
}

// queueLists reports whether the reconciliation-queue listing contains reconID.
func queueLists(body map[string]any, reconID string) bool {
	entries, _ := body["entries"].([]any)
	for _, raw := range entries {
		if e, ok := raw.(map[string]any); ok && e["id"] == reconID {
			return true
		}
	}
	return false
}

// TestFrozenEntryHasNoParkedPayloadAndSaysWhy pins the asymmetry that a 404
// would have hidden. A freeze row is raised by the ledger, not by
// sync.parkConflict, so it carries NO payload — and "this entry never held one"
// must not be spelled the same way as "no such entry", because the client's next
// move differs: for a freeze it reads the operator's version from its own
// outbox instead.
func TestFrozenEntryHasNoParkedPayloadAndSaysWhy(t *testing.T) {
	srv := newServer(t)
	base, voucherID := postFrozenSale(t, srv)

	// Pin an IRN: the freeze rule now hard-blocks any alter/delete.
	rec, _ := do(t, srv, http.MethodPost, base+"/vouchers/"+voucherID+"/external-reference", map[string]any{
		"kind": "IRN", "ref_value": "IRN-RECON-1", "created_by": "statutoryd",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register external reference status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}

	// A refused DELETE raises the freeze reconciliation row.
	recDel, _ := do(t, srv, http.MethodDelete, base+"/vouchers/"+voucherID, nil)
	if recDel.Code == http.StatusOK || recDel.Code == http.StatusNoContent {
		t.Fatalf("delete of a frozen voucher succeeded (%d) — the freeze rule did not bite", recDel.Code)
	}

	_, queue := do(t, srv, http.MethodGet, base+"/reconciliation-queue", nil)
	entries, _ := queue["entries"].([]any)
	if len(entries) == 0 {
		t.Fatalf("no freeze entry was raised (body %v)", queue)
	}
	freezeID, _ := entries[0].(map[string]any)["id"].(string)

	recP, body := do(t, srv, http.MethodGet, base+"/reconciliation-queue/"+freezeID+"/payload", nil)
	if recP.Code != http.StatusOK {
		t.Fatalf("freeze payload status = %d, want 200 — the entry EXISTS, so a 404 would "+
			"spell 'never held a payload' the same way as 'no such entry' (body %s)",
			recP.Code, recP.Body.String())
	}
	if body["has_payload"] != false {
		t.Fatalf("has_payload = %v, want false — only sync.parkConflict writes the payload column",
			body["has_payload"])
	}
	if reason, _ := body["payload_absent_because"].(string); reason == "" {
		t.Fatalf("has_payload=false with no reason: a client that cannot tell this from a "+
			"fault will retry (body %s)", recP.Body.String())
	}
	if body["kind"] != "IRN" {
		t.Fatalf("kind = %v, want IRN — the row must name what pinned the voucher, because "+
			"'frozen' without 'because an IRN is on it' is unactionable", body["kind"])
	}
}

// TestUnknownReconciliationIDIs404 covers both new routes: an id that names no
// open entry is a 404, not a 200 with an empty body.
func TestUnknownReconciliationIDIs404(t *testing.T) {
	srv := newServer(t)
	_, base := newCompany(t, srv)
	unknown := uuid.NewString()

	recGet, _ := do(t, srv, http.MethodGet, base+"/reconciliation-queue/"+unknown+"/payload", nil)
	if recGet.Code != http.StatusNotFound {
		t.Errorf("payload of unknown id = %d, want 404", recGet.Code)
	}
	recPost, _ := do(t, srv, http.MethodPost, base+"/reconciliation-queue/"+unknown+"/resolve", nil)
	if recPost.Code != http.StatusNotFound {
		t.Errorf("resolve of unknown id = %d, want 404", recPost.Code)
	}

	// A malformed id is a 400, distinguishable from "not found".
	recBad, _ := do(t, srv, http.MethodGet, base+"/reconciliation-queue/not-a-uuid/payload", nil)
	if recBad.Code != http.StatusBadRequest {
		t.Errorf("payload of malformed id = %d, want 400", recBad.Code)
	}
}
