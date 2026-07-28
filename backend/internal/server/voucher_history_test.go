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
//
// HOW THE TRIGGER IS DEFEATED, AND WHY IT CHANGED. This file used to run an
// `ALTER TABLE voucher_version DISABLE-TRIGGER voucher_version_no_mutate`
// statement (spelled with a hyphen here on purpose — see the guard at the foot
// of this file, and the grep that backs it). That statement is a CATALOG
// change: it is database-wide, it is visible to every other connection
// instantly, and it SURVIVES the test — a panic or a killed test run between the
// disable and the re-enable leaves the append-only invariant switched off in the
// shared development database with nothing to announce it. It is the single
// reason the runbook forbade `go test ./internal/server` unscoped while any
// other agent was working.
//
// The replacement is `SET LOCAL session_replication_role = replica` inside a
// short ADMIN transaction. `SET LOCAL` is scoped to the transaction and cannot
// outlive it — not on COMMIT, not on ROLLBACK, not on a crashed backend — and it
// affects only the connection that set it, so a concurrent test on another
// connection still sees the trigger armed. Nothing global is ever touched, and
// TestServerTestsNeverDisableTriggersGlobally at the bottom of this file keeps
// the old form from creeping back.

import (
	"context"
	"net/http"
	"os"
	"strings"
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

// mutateAppendOnly runs ONE statement against an append-only table inside an
// ADMIN transaction with `SET LOCAL session_replication_role = replica`, which
// suppresses user triggers for that transaction on that connection only.
//
// It COMMITS, deliberately. The endpoint under test reads on a different
// connection out of the app pool, so an uncommitted tamper would be invisible to
// it and the test would assert nothing. Each tamper and each repair is therefore
// its own short transaction, and the repair is a separate call rather than a
// deferred closure — the sequence tamper -> observe -> repair -> observe is what
// proves the verifier is reading the data rather than answering from a cache.
//
// Contrast with the `ALTER TABLE ... DISABLE-TRIGGER` form this replaced: that
// is a persistent, database-wide catalog change that leaks the disarmed
// invariant to every other session and survives the process. `SET LOCAL` cannot
// leak: the transaction is the outer bound of its lifetime, so there is no
// failure mode in which the immutability guard is left off.
//
// Pattern copied from
// internal/compliance/gstr/alter_voucher_type_regression_test.go:254.
//
// It returns the number of rows the statement affected, because a tamper that
// silently matched zero rows would leave the chain intact and turn the assertion
// that follows into a test of nothing.
func mutateAppendOnly(t *testing.T, conn *pgx.Conn, sql string, args ...any) int64 {
	t.Helper()
	ctx := context.Background()

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin admin tx: %v", err)
	}
	// Any early return below must not leave the transaction open; a rollback
	// after a successful commit is a harmless no-op.
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
		t.Fatalf("suppress append-only triggers for this transaction: %v", err)
	}
	tag, err := tx.Exec(ctx, sql, args...)
	if err != nil {
		t.Fatalf("mutate append-only table: %v\nstatement: %s", err, sql)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit admin mutation (the endpoint reads on another connection, so this MUST commit): %v", err)
	}
	return tag.RowsAffected()
}

// mutateAppendOnlyRows is mutateAppendOnly with the row count asserted. A tamper
// that matched no rows is not a weaker test, it is a VACUOUS one: the chain
// stays intact and the "tampering was detected" assertion that follows would be
// asserting the opposite of what it claims.
func mutateAppendOnlyRows(t *testing.T, conn *pgx.Conn, want int64, sql string, args ...any) {
	t.Helper()
	if got := mutateAppendOnly(t, conn, sql, args...); got != want {
		t.Fatalf("statement affected %d rows, want %d — the tamper (or its repair) did not happen, "+
			"so every assertion after this point would be vacuous\nstatement: %s", got, want, sql)
	}
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
	mutateAppendOnlyRows(t, conn, 1,
		`UPDATE voucher_version
		    SET payload = jsonb_set(payload, '{narration}', '"forged by someone with database access"')
		  WHERE voucher_id = $1 AND version_no = 2`, voucherID)
	v := verify()
	if v["ok"] != false {
		t.Fatalf("an EDITED payload verified as intact: %v", v)
	}
	if v["broken_at_version"] != float64(2) {
		t.Errorf("broken_at_version = %v, want 2", v["broken_at_version"])
	}
	t.Logf("tamper detected: %v", v["reason"])

	// Restore, and prove the repair is real rather than the verifier being lax.
	mutateAppendOnlyRows(t, conn, 1,
		`UPDATE voucher_version SET payload = $2 WHERE voucher_id = $1 AND version_no = 2`,
		voucherID, originalPayload)
	if v = verify(); v["ok"] != true {
		t.Fatalf("chain still broken after restoring the original payload: %v", v["reason"])
	}

	// -- TAMPER 2: an AMOUNT, which is what a tamperer would actually change --
	mutateAppendOnlyRows(t, conn, 1,
		`UPDATE voucher_version
		    SET payload = jsonb_set(payload, '{postings,0,amount}', '"9999.0000"')
		  WHERE voucher_id = $1 AND version_no = 1`, voucherID)
	if v = verify(); v["ok"] != false || v["broken_at_version"] != float64(1) {
		t.Fatalf("an edited AMOUNT verified as intact or blamed the wrong version: %v", v)
	}

	// -- TAMPER 3: remove a version entirely ---------------------------------
	// Restore version 1 first so the only remaining damage is the removal.
	mutateAppendOnlyRows(t, conn, 1,
		`UPDATE voucher_version
		    SET payload = jsonb_set(payload, '{postings,0,amount}', '"400.0000"')
		  WHERE voucher_id = $1 AND version_no = 1`, voucherID)
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

	// Children first, then the version itself. Three separate transactions is
	// fine here: each leaves the FK graph consistent, and the endpoint is not
	// consulted between them.
	mutateAppendOnly(t, conn, `DELETE FROM bill_allocation WHERE voucher_version_id = $1`, removed.id)
	mutateAppendOnly(t, conn, `DELETE FROM posting WHERE voucher_version_id = $1`, removed.id)
	mutateAppendOnlyRows(t, conn, 1, `DELETE FROM voucher_version WHERE id = $1`, removed.id)
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
	// voucher_version's immutability trigger is BEFORE UPDATE OR DELETE only, so
	// this INSERT needs no suppression at all — but it goes through the same
	// helper so that the row count is asserted and every write in this file is
	// made on the same, auditable footing.
	mutateAppendOnlyRows(t, conn, 1,
		`INSERT INTO voucher_version
		   (id, company_id, voucher_id, version_no, op, node_id, prev_hash, hash, payload, created_by)
		 VALUES ($1, $2, $3, 2, $4::voucher_op, $5, $6, $7, $8, $9)`,
		removed.id, companyID, voucherID, removed.op, removed.nodeID,
		removed.prevHash, removed.hash, removed.payload, removed.by)
	if v = verify(); v["ok"] != true {
		t.Fatalf("chain still broken after restoring version 2: %v", v["reason"])
	}
}

// TestServerTestsNeverDisableTriggersGlobally is a source-level guard, and it is
// here because the thing it forbids is invisible at runtime until it has already
// done its damage.
//
// An `ALTER TABLE <t>` that switches a trigger off is a catalog change. It
// applies to the whole database, every session sees it immediately, and it
// persists until something explicitly re-enables it. A test that switches an
// append-only trigger off and then panics, times out, or is killed leaves the
// invariant OFF in the shared development database — and the next agent to run
// the suite gets a green result that proves nothing, because the guard those
// tests assert on is no longer armed. This package used to contain exactly that,
// which is why the runbook forbade running it unscoped alongside other work.
//
// The sanctioned replacement is `SET LOCAL session_replication_role = replica`
// inside a transaction (see mutateAppendOnly above): connection-scoped,
// transaction-scoped, and impossible to leak.
//
// This reads the package's own sources from DISK rather than inspecting
// pg_trigger, deliberately. A runtime check ("no trigger is currently switched
// off") only fires if the offending test happens to run AND happens to leave the
// trigger off; it cannot see a disable/enable pair that is balanced today and
// will be unbalanced the first time the test between them fails.
//
// The forbidden phrase is matched as raw text anywhere in the file, INCLUDING
// comments — which is why the prose above and in the file header spells it with
// a hyphen, and why the needle below is assembled from two pieces so that this
// guard's own source does not trip it. That is not squeamishness: it makes this
// test exactly equivalent to grepping the package sources for the phrase and
// expecting a count of zero, so the in-suite guard and the runbook's shell check
// can never disagree about what is allowed. A guard whose rule is narrower than
// the documented rule is a guard people learn to route around.
func TestServerTestsNeverDisableTriggersGlobally(t *testing.T) {
	forbidden := "DISABLE" + " " + "TRIGGER"

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	scanned := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		scanned++
		for i, line := range strings.Split(string(src), "\n") {
			if !strings.Contains(line, forbidden) {
				continue
			}
			t.Errorf("%s:%d switches a trigger off GLOBALLY:\n\t%s\n"+
				"That is a database-wide, persistent catalog change: it disarms the append-only "+
				"invariant for every other connection and survives a crashed test run. Use "+
				"mutateAppendOnly (SET LOCAL session_replication_role = replica inside a "+
				"transaction) instead — see voucher_history_test.go.",
				e.Name(), i+1, strings.TrimSpace(line))
		}
	}
	// A guard that scanned nothing would pass for the wrong reason — e.g. if the
	// working directory were ever not the package directory. `go test` chdirs
	// into the package, so the real number here is upwards of twenty.
	if scanned < 5 {
		t.Fatalf("scanned only %d _test.go files in this package; the guard is not looking at the sources it claims to", scanned)
	}
}
