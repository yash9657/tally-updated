package server_test

// voucher_history_test.go — THE EDIT LOG over HTTP, and the tamper gate.
//
// The claim GET .../vouchers/{id}/history makes is not "here are some rows". It
// is "these rows are what was recorded, and nothing has changed them". A test
// that only checks the endpoint returns three versions with ok=true proves
// nothing about the second half of that sentence — it would pass just as
// happily against an implementation that returned ok=true unconditionally.
//
// So the gate here DEFEATS THE APPEND-ONLY TRIGGER as the postgres superuser,
// edits a stored payload, and asserts the endpoint reports the chain BROKEN at
// the right version — then restores the row byte-for-byte and asserts it
// verifies again. That is tamper detection demonstrated rather than asserted.
//
// Everything this file damages, it repairs, and it works on a company it
// created itself.

import (
	"context"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/server"
)

// superuser opens an admin connection (postgres), which is the only way to get
// past the append-only trigger — which is the point being made.
func superuser(t *testing.T) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

// appendOnlyTriggers is every guard that has to come off to fake a tamper.
// posting has its own — discovered by this test failing with "posting is
// immutable; DELETE is not permitted", which is the invariant working.
var appendOnlyTriggers = [][2]string{
	{"voucher_version", "voucher_version_no_mutate"},
	{"posting", "posting_no_mutate"},
}

// withTriggerDisabled runs fn with the append-only triggers off, re-enabling
// them even if fn fails. Leaving one off would silently disarm the invariant for
// every later test in the run, so the re-enable is a t.Fatalf, not a log line.
func withTriggerDisabled(t *testing.T, conn *pgx.Conn, fn func()) {
	t.Helper()
	ctx := context.Background()
	for _, tg := range appendOnlyTriggers {
		if _, err := conn.Exec(ctx, `ALTER TABLE `+tg[0]+` DISABLE TRIGGER `+tg[1]); err != nil {
			t.Fatalf("disable %s: %v", tg[1], err)
		}
	}
	defer func() {
		for _, tg := range appendOnlyTriggers {
			if _, err := conn.Exec(ctx, `ALTER TABLE `+tg[0]+` ENABLE TRIGGER `+tg[1]); err != nil {
				t.Fatalf("RE-ENABLE %s (the invariant is now off!): %v", tg[1], err)
			}
		}
	}()
	fn()
}

// editedVoucher posts a sale, alters it, then deletes it — a three-version
// chain of CREATE / ALTER / DELETE, which is the whole story an edit log tells.
func editedVoucher(t *testing.T, srv *server.Server) (base, voucherID string) {
	t.Helper()
	_, base = newCompany(t, srv)
	debtorID := createLedger(t, srv, base, "Acme Corp", "Sundry Debtors")
	salesID := createLedger(t, srv, base, "Domestic Sales", "Sales Accounts")

	rec, body := do(t, srv, http.MethodPost, base+"/vouchers/sales", map[string]any{
		"voucher_no": "INV-1", "voucher_date": "2025-04-10", "party": "Acme Corp",
		"created_by": "asha", "narration": "as first entered",
		"postings": []map[string]any{
			{"ledger_id": debtorID, "dr_cr": "D", "amount": "400.0000"},
			{"ledger_id": salesID, "dr_cr": "C", "amount": "400.0000"},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("post sale = %d (%s)", rec.Code, rec.Body.String())
	}
	voucherID, _ = body["voucher_id"].(string)

	rec, _ = do(t, srv, http.MethodPut, base+"/vouchers/sales/"+voucherID, map[string]any{
		"voucher_no": "INV-1", "voucher_date": "2025-04-10", "party": "Acme Corp",
		"created_by": "bhaskar", "narration": "corrected after the client called",
		"postings": []map[string]any{
			{"ledger_id": debtorID, "dr_cr": "D", "amount": "650.0000"},
			{"ledger_id": salesID, "dr_cr": "C", "amount": "650.0000"},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("alter sale = %d (%s)", rec.Code, rec.Body.String())
	}

	rec, _ = do(t, srv, http.MethodDelete, base+"/vouchers/"+voucherID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete sale = %d (%s)", rec.Code, rec.Body.String())
	}
	return base, voucherID
}

// TestVoucherHistoryShowsEveryVersion is the feature: the log exists, it is
// readable, and it shows what actually changed and who changed it.
func TestVoucherHistoryShowsEveryVersion(t *testing.T) {
	srv := newServer(t)
	base, voucherID := editedVoucher(t, srv)

	rec, body := do(t, srv, http.MethodGet, base+"/vouchers/"+voucherID+"/history", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET history = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	versions := list(t, body, "versions")
	if len(versions) != 3 {
		t.Fatalf("history has %d versions, want 3 (create, alter, delete)", len(versions))
	}

	// Ops in order, and the ATTRIBUTION — an edit log whose entries are not
	// attributable to a person is not an edit log.
	wantOps := []string{"CREATE", "ALTER", "DELETE"}
	wantBy := []string{"asha", "bhaskar"}
	for i, v := range versions {
		if v["op"] != wantOps[i] {
			t.Errorf("version %d op = %v, want %s", i+1, v["op"], wantOps[i])
		}
		if got := v["version_no"]; got != float64(i+1) {
			t.Errorf("version index %d has version_no %v, want %d", i, got, i+1)
		}
		if i < len(wantBy) && v["created_by"] != wantBy[i] {
			t.Errorf("version %d created_by = %v, want %s", i+1, v["created_by"], wantBy[i])
		}
		if v["hash"] == "" || v["hash"] == nil {
			t.Errorf("version %d carries no hash", i+1)
		}
	}

	// The amounts as RECORDED at each version — 400 then 650. This is the thing
	// GET /vouchers/{id} cannot show: the head has only ever seen 650.
	amountAt := func(i int) string {
		ps, _ := versions[i]["postings"].([]any)
		if len(ps) == 0 {
			return "(none)"
		}
		p, _ := ps[0].(map[string]any)
		s, _ := p["amount"].(string)
		return s
	}
	if amountAt(0) != "400.0000" || amountAt(1) != "650.0000" {
		t.Errorf("recorded amounts = %s then %s, want 400.0000 then 650.0000", amountAt(0), amountAt(1))
	}

	// Version 1 begins the chain; the rest link to their predecessor.
	if versions[0]["prev_hash"] != "" {
		t.Errorf("version 1 prev_hash = %v, want empty", versions[0]["prev_hash"])
	}
	for i := 1; i < 3; i++ {
		if versions[i]["prev_hash"] != versions[i-1]["hash"] {
			t.Errorf("version %d prev_hash does not equal version %d hash", i+1, i)
		}
	}

	verification, _ := body["verification"].(map[string]any)
	if verification["ok"] != true {
		t.Errorf("an untouched chain verified as %v: %v", verification["ok"], verification["reason"])
	}

	// A DELETED voucher still has a history — the whole point. Its head fetch
	// 404s, and the two must disagree.
	if rec, _ = do(t, srv, http.MethodGet, base+"/vouchers/"+voucherID, nil); rec.Code != http.StatusNotFound {
		t.Errorf("GET deleted voucher head = %d, want 404", rec.Code)
	}
	if body["is_deleted"] != true {
		t.Errorf("history is_deleted = %v, want true", body["is_deleted"])
	}
}

// TestVoucherHistoryDetectsTampering is THE gate.
//
// It edits a stored payload behind the append-only trigger's back and asserts
// the endpoint says so, naming the right version — then repairs the row and
// asserts the chain verifies again. Without the repair this test would leave a
// permanently-broken voucher in the shared database.
func TestVoucherHistoryDetectsTampering(t *testing.T) {
	srv := newServer(t)
	base, voucherID := editedVoucher(t, srv)
	conn := superuser(t)
	ctx := context.Background()

	verify := func() map[string]any {
		t.Helper()
		rec, body := do(t, srv, http.MethodGet, base+"/vouchers/"+voucherID+"/history", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET history = %d (%s)", rec.Code, rec.Body.String())
		}
		v, _ := body["verification"].(map[string]any)
		return v
	}

	if v := verify(); v["ok"] != true {
		t.Fatalf("baseline chain is already broken: %v", v["reason"])
	}

	// FIRST: the trigger really is armed. Tampering is not merely detected
	// after the fact — it is refused outright unless you are the superuser
	// turning the guard off, which is what the rest of this test does.
	if _, err := conn.Exec(ctx,
		`UPDATE voucher_version SET payload = jsonb_set(payload, '{narration}', '"forged"')
		  WHERE voucher_id = $1 AND version_no = 2`, voucherID); err == nil {
		t.Fatal("UPDATE on voucher_version succeeded with the append-only trigger ENABLED")
	}

	// Snapshot the row so it can be restored byte-for-byte.
	var originalPayload []byte
	if err := conn.QueryRow(ctx,
		`SELECT payload FROM voucher_version WHERE voucher_id = $1 AND version_no = 2`,
		voucherID).Scan(&originalPayload); err != nil {
		t.Fatalf("snapshot version 2 payload: %v", err)
	}

	// -- TAMPER 1: edit a stored payload -------------------------------------
	withTriggerDisabled(t, conn, func() {
		if _, err := conn.Exec(ctx,
			`UPDATE voucher_version
			    SET payload = jsonb_set(payload, '{narration}', '"forged by someone with database access"')
			  WHERE voucher_id = $1 AND version_no = 2`, voucherID); err != nil {
			t.Fatalf("tamper: %v", err)
		}
	})
	v := verify()
	if v["ok"] != false {
		t.Fatalf("an EDITED payload verified as intact: %v", v)
	}
	if v["broken_at_version"] != float64(2) {
		t.Errorf("broken_at_version = %v, want 2", v["broken_at_version"])
	}
	t.Logf("tamper detected: %v", v["reason"])

	// Restore, and prove the repair is real rather than the verifier being lax.
	withTriggerDisabled(t, conn, func() {
		if _, err := conn.Exec(ctx,
			`UPDATE voucher_version SET payload = $2 WHERE voucher_id = $1 AND version_no = 2`,
			voucherID, originalPayload); err != nil {
			t.Fatalf("restore: %v", err)
		}
	})
	if v = verify(); v["ok"] != true {
		t.Fatalf("chain still broken after restoring the original payload: %v", v["reason"])
	}

	// -- TAMPER 2: an AMOUNT, which is what a tamperer would actually change --
	withTriggerDisabled(t, conn, func() {
		if _, err := conn.Exec(ctx,
			`UPDATE voucher_version
			    SET payload = jsonb_set(payload, '{postings,0,amount}', '"9999.0000"')
			  WHERE voucher_id = $1 AND version_no = 1`, voucherID); err != nil {
			t.Fatalf("tamper amount: %v", err)
		}
	})
	if v = verify(); v["ok"] != false || v["broken_at_version"] != float64(1) {
		t.Fatalf("an edited AMOUNT verified as intact or blamed the wrong version: %v", v)
	}

	// -- TAMPER 3: remove a version entirely ---------------------------------
	// Restore version 1 first so the only remaining damage is the removal.
	var v1Payload []byte
	withTriggerDisabled(t, conn, func() {
		if err := conn.QueryRow(ctx,
			`SELECT payload FROM voucher_version WHERE voucher_id = $1 AND version_no = 1`,
			voucherID).Scan(&v1Payload); err != nil {
			t.Fatalf("read tampered v1: %v", err)
		}
		if _, err := conn.Exec(ctx,
			`UPDATE voucher_version
			    SET payload = jsonb_set(payload, '{postings,0,amount}', '"400.0000"')
			  WHERE voucher_id = $1 AND version_no = 1`, voucherID); err != nil {
			t.Fatalf("restore amount: %v", err)
		}
	})
	if v = verify(); v["ok"] != true {
		t.Fatalf("chain not repaired before the removal test: %v", v["reason"])
	}

	var removed struct {
		id       string
		payload  []byte
		hash     []byte
		prevHash []byte
		op       string
		nodeID   string
		by       string
	}
	if err := conn.QueryRow(ctx,
		`SELECT id, payload, hash, prev_hash, op::text, node_id, created_by
		   FROM voucher_version WHERE voucher_id = $1 AND version_no = 2`, voucherID).Scan(
		&removed.id, &removed.payload, &removed.hash, &removed.prevHash,
		&removed.op, &removed.nodeID, &removed.by); err != nil {
		t.Fatalf("snapshot version 2: %v", err)
	}
	// posting rows reference the version, so drop them for the duration too.
	var companyID string
	if err := conn.QueryRow(ctx, `SELECT company_id::text FROM voucher_version WHERE id = $1`,
		removed.id).Scan(&companyID); err != nil {
		t.Fatalf("read company id: %v", err)
	}

	withTriggerDisabled(t, conn, func() {
		if _, err := conn.Exec(ctx, `DELETE FROM bill_allocation WHERE voucher_version_id = $1`, removed.id); err != nil {
			t.Fatalf("drop bill allocations: %v", err)
		}
		if _, err := conn.Exec(ctx, `DELETE FROM posting WHERE voucher_version_id = $1`, removed.id); err != nil {
			t.Fatalf("drop postings: %v", err)
		}
		if _, err := conn.Exec(ctx, `DELETE FROM voucher_version WHERE id = $1`, removed.id); err != nil {
			t.Fatalf("remove version 2: %v", err)
		}
	})
	if v = verify(); v["ok"] != false {
		t.Fatalf("a chain with version 2 REMOVED verified as intact: %v", v)
	}
	if v["broken_at_version"] != float64(3) {
		t.Errorf("broken_at_version = %v, want 3 (the row now sitting where 2 should be)", v["broken_at_version"])
	}
	t.Logf("removal detected: %v", v["reason"])

	// Put version 2 back. Its postings are NOT restored (the ledger balances are
	// derived from voucher_head's current version, which is the tombstone), but
	// the version row is what the chain is over.
	withTriggerDisabled(t, conn, func() {
		if _, err := conn.Exec(ctx,
			`INSERT INTO voucher_version
			   (id, company_id, voucher_id, version_no, op, node_id, prev_hash, hash, payload, created_by)
			 VALUES ($1, $2, $3, 2, $4::voucher_op, $5, $6, $7, $8, $9)`,
			removed.id, companyID, voucherID, removed.op, removed.nodeID,
			removed.prevHash, removed.hash, removed.payload, removed.by); err != nil {
			t.Fatalf("restore version 2: %v", err)
		}
	})
	if v = verify(); v["ok"] != true {
		t.Fatalf("chain still broken after restoring version 2: %v", v["reason"])
	}
}
