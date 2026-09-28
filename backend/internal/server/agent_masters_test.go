package server_test

// agent_masters_test.go — W2 breadth, priority 2: the agent creates masters
// (a party ledger, a stock item) through preview and approval, and an
// agent-created ledger is BORN BLOCKED: nothing can post to it until a
// PERSON unblocks it, and the agent can never unblock it (migration 0095).

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/agent"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/server"
)

const newPartyName = "Naya Supplier Pvt Ltd"

// newPartyScript creates a supplier, then — after the approval — tries to pay it.
func newPartyScript(t *testing.T, turn int, res []json.RawMessage) agent.ModelDecision {
	switch turn {
	case 0:
		return decision(agent.ToolPreview, map[string]any{"action_id": "post.ledgers",
			"body": map[string]any{"name": newPartyName, "group_name": "Sundry Creditors",
				"bank_account_number": "50100123456789", "bank_ifsc": "HDFC0001234", "created_by": "Ramesh"}})
	case 1:
		return decision(agent.ToolCommit, map[string]string{"preview": "tool:0"})
	case 2:
		return decision(agent.ToolRead, map[string]any{"action_id": "get.ledgers"})
	case 3:
		return decision(agent.ToolPreview, map[string]any{"action_id": "post.vouchers.payment",
			"body": voucherBody("2025-05-20", "first payment to the new supplier",
				[3]string{ledgerIDFromResults(t, res, newPartyName), "D", "1000.00"},
				[3]string{ledgerIDFromResults(t, res, "HDFC Bank"), "C", "1000.00"})})
	default:
		return finish("Created the supplier; it is blocked until reviewed.")
	}
}

func ledgerRow(t *testing.T, srv *server.Server, b agentBooks, name string) map[string]any {
	t.Helper()
	rec, resp := do(t, srv, http.MethodGet, b.base+"/ledgers", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list ledgers: %d", rec.Code)
	}
	for _, l := range asList(resp["ledgers"]) {
		if m := asMap(l); m["name"] == name {
			return m
		}
	}
	return nil
}

func TestAgentCreatedLedgerIsBornBlocked(t *testing.T) {
	srv := newAgentServer(t)
	b := newAgentBooks(t, srv)
	runID, _ := runToQuiet(t, srv, b, newPartyScript, map[string]any{
		"instruction": "Add our new supplier " + newPartyName + " (HDFC a/c 50100123456789, IFSC HDFC0001234) and pay them Rs 1,000.",
		"delegate":    []string{"create master", "create payment"},
	})

	// ---- the create waits for a person, whatever the level, and shows the master
	a := onePending(t, srv, b, runID)
	reasons, _ := json.Marshal(a["reasons"])
	if !strings.Contains(string(reasons), "born only through a human decision") {
		t.Fatalf("the master create parked for %s", reasons)
	}
	if ledgerRow(t, srv, b, newPartyName) != nil {
		t.Fatal("the preview left the ledger behind")
	}
	ms := asList(effectOf(t, a)["masters"])
	if len(ms) != 1 || asMap(ms[0])["entity"] != "ledger" || asMap(ms[0])["op"] != "CREATE" {
		t.Fatalf("effect.masters = %v, want one ledger CREATE", ms)
	}
	after := asMap(asMap(ms[0])["after"])
	if after["bank_account_number"] != "50100123456789" || after["blocked"] != true {
		t.Fatalf("the approver's diff does not show the bank details and the block: %v", after)
	}

	// ---- approved: it exists, BLOCKED; the agent's own payment to it is refused
	decideAndDrain(t, srv, b, a["id"].(string))
	row := ledgerRow(t, srv, b, newPartyName)
	if row == nil {
		t.Fatal("the approved ledger does not exist")
	}
	if row["blocked"] != true || !strings.Contains(row["blocked_reason"].(string), runID) {
		t.Fatalf("the agent-created ledger is not blocked (or does not name its run): %v", row)
	}
	lid := row["id"].(string)
	if p := pendingApprovals(t, srv, b, runID); len(p) != 0 {
		t.Fatalf("a payment to the blocked party reached the approval inbox: %v", p)
	}
	if kinds := intentKinds(t, b.companyID, runID); !strings.Contains(kinds, "verifier_error") {
		t.Fatalf("the payment preview to the blocked party was not refused by the validators: %s", kinds)
	}
	want := server.AgentAttribution(mustUUID(t, runID), mustUUID(t, ownerUserID(t, srv, b, runID)))
	if got := adminString(t, `SELECT created_by FROM master_audit WHERE entity_id = $1 AND op = 'CREATE'`, lid); got != want {
		t.Fatalf("the master's audit row is attributed to %q, want %q (the body said Ramesh)", got, want)
	}

	// ---- a PERSON's payment to it is refused too (the block is not agent-only)
	pay := voucherBody("2025-05-20", "human pays the new party", [3]string{lid, "D", "1000.00"},
		[3]string{b.ledgers["HDFC Bank"], "C", "1000.00"})
	rec, _ := do(t, srv, http.MethodPost, b.base+"/vouchers/payment", pay)
	// The OPERATOR'S sentence (internal/ledger), not the trigger's floor: it
	// names the ledger and where the unblock is.
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Ledgers, Unblock") ||
		!strings.Contains(rec.Body.String(), newPartyName) {
		t.Fatalf("a person's payment to a blocked ledger = %d %s, want 422 naming the ledger and the unblock", rec.Code, rec.Body.String())
	}

	// ---- the agent can never unblock it: refused by the router's own gate
	code, msg := server.ProbeAgentRouterGate(context.Background(), srv, b.companyID, ownerSubject(t, srv),
		"post.ledgers._ledgerid.unblock", map[string]string{"ledgerID": lid})
	if code != http.StatusForbidden || !strings.Contains(msg, "agent") {
		t.Fatalf("an agent's unblock through the router = %d %s, want 403 from the agent gate", code, msg)
	}

	// ---- Data Entry may not unblock (bare token, zero rules)
	deTok := dataEntryMember(t, srv, b)
	if rec, _ := doRaw(t, srv, http.MethodPost, b.base+"/ledgers/"+lid+"/unblock", deTok, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("Data Entry unblock = %d, want 403", rec.Code)
	}

	// ---- the owner unblocks; then the payment posts; a second unblock is 409
	if rec, _ := do(t, srv, http.MethodPost, b.base+"/ledgers/"+lid+"/unblock", nil); rec.Code != http.StatusOK {
		t.Fatalf("owner unblock = %d %s", rec.Code, rec.Body.String())
	}
	if rec, _ := do(t, srv, http.MethodPost, b.base+"/vouchers/payment", pay); rec.Code != http.StatusCreated {
		t.Fatalf("payment after the unblock = %d %s", rec.Code, rec.Body.String())
	}
	if rec, resp := do(t, srv, http.MethodPost, b.base+"/ledgers/"+lid+"/unblock", nil); rec.Code != http.StatusConflict || resp["code"] != "NOT_BLOCKED" {
		t.Fatalf("second unblock = %d %v, want 409 NOT_BLOCKED", rec.Code, resp)
	}
	if n := adminInt(t, `SELECT count(*) FROM master_audit WHERE entity_id = $1 AND op = 'ALTER' AND before_doc->>'blocked' = 'true'`, lid); n != 1 {
		t.Fatalf("the unblock left %d audit rows, want 1", n)
	}
	if row := ledgerRow(t, srv, b, newPartyName); row["blocked"] != false {
		t.Fatalf("still blocked after the unblock: %v", row)
	}
}

// TestBlockedLedgerTriggerIsTheFloor proves the database refuses a posting to
// a blocked ledger WITHOUT the ledger service's check — the floor under every
// writer that is not internal/ledger (a sync replay, an import, a future
// service).
func TestBlockedLedgerTriggerIsTheFloor(t *testing.T) {
	srv := newAgentServer(t)
	b := newAgentBooks(t, srv)
	vid := postHuman(t, srv, b, "payment", rentPayment(b, "10.00", "any voucher, for its version id"))
	cid := mustUUID(t, b.companyID)
	lid := mustUUID(t, b.ledgers["Fraudster Pvt Ltd"])
	pool, err := db.New(context.Background(), appDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx := db.WithCompany(context.Background(), cid)
	block := func(on bool) {
		t.Helper()
		sql := `UPDATE ledger SET blocked_at = now(), blocked_reason = 'test' WHERE id = $1`
		if !on {
			sql = `UPDATE ledger SET blocked_at = NULL, blocked_reason = NULL WHERE id = $1`
		}
		if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, sql, lid)
			return err
		}); err != nil {
			t.Fatalf("set block %v: %v", on, err)
		}
	}
	insertPosting := func() error {
		var versionID uuid.UUID
		return pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			if err := tx.QueryRow(ctx, `SELECT current_version_id FROM voucher_head WHERE voucher_id = $1`, vid).Scan(&versionID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO posting (id, company_id, voucher_version_id, ledger_id, dr_cr, amount)
				VALUES ($1, $2, $3, $4, 'D', 1), ($5, $2, $3, $6, 'C', 1)`,
				uuid.New(), cid, versionID, lid, uuid.New(), mustUUID(t, b.ledgers["HDFC Bank"])); err != nil {
				return err
			}
			return errRollback
		})
	}
	block(true)
	if err := insertPosting(); err == nil || !strings.Contains(err.Error(), "is blocked") {
		t.Fatalf("a raw posting to a blocked ledger: %v, want the trigger's refusal", err)
	}
	// CONTROL: unblocked, the same raw insert reaches the (rolled-back) end.
	block(false)
	if err := insertPosting(); err != errRollback {
		t.Fatalf("CONTROL: a raw posting to an unblocked ledger: %v, want only the test's own rollback", err)
	}
}

var errRollback = rollbackErr("rolled back by the test")

type rollbackErr string

func (e rollbackErr) Error() string { return string(e) }

// dataEntryMember invites a member at the seeded Data Entry level and returns
// their (step-up) token.
func dataEntryMember(t *testing.T, srv *server.Server, b agentBooks) string {
	t.Helper()
	subject := "clerk-" + uuid.NewString()[:8]
	rec, _ := do(t, srv, http.MethodPost, b.base+"/members/invites",
		map[string]any{"subject": subject, "security_level_name": "Data Entry"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite Data Entry = %d (%s)", rec.Code, rec.Body.String())
	}
	tok := mintToken(t, srv, subject, "webauthn")
	if rec, _ := doRaw(t, srv, http.MethodGet, "/v1/me", tok, nil); rec.Code != http.StatusOK {
		t.Fatalf("claim invite = %d", rec.Code)
	}
	return tok
}

// stockItemScript creates a stock item.
func stockItemScript(t *testing.T, turn int, res []json.RawMessage) agent.ModelDecision {
	switch turn {
	case 0:
		return decision(agent.ToolPreview, map[string]any{"action_id": "post.stock-items",
			"body": map[string]any{"name": "Steel Rod 12mm", "hsn": "7214", "gst_rate_pct": "18"}})
	case 1:
		return decision(agent.ToolCommit, map[string]string{"preview": "tool:0"})
	default:
		return finish("Created the item.")
	}
}

func TestAgentCreatesAStockItemThroughApproval(t *testing.T) {
	srv := newAgentServer(t)
	b := newAgentBooks(t, srv)
	runID, _ := runToQuiet(t, srv, b, stockItemScript, map[string]any{
		"instruction": "Create a stock item Steel Rod 12mm, HSN 7214, GST 18%.", "delegate": []string{"create master"},
	})
	a := onePending(t, srv, b, runID)
	ms := asList(effectOf(t, a)["masters"])
	if len(ms) != 1 || asMap(ms[0])["entity"] != "stock_item" {
		t.Fatalf("effect.masters = %v, want one stock_item", ms)
	}
	if n := adminInt(t, `SELECT count(*) FROM stock_item WHERE company_id = $1 AND name = 'Steel Rod 12mm'`, b.companyID); n != 0 {
		t.Fatal("the preview left the item behind")
	}
	decideAndDrain(t, srv, b, a["id"].(string))
	if n := adminInt(t, `SELECT count(*) FROM stock_item WHERE company_id = $1 AND name = 'Steel Rod 12mm'`, b.companyID); n != 1 {
		t.Fatalf("items after the approval = %d, want 1", n)
	}
	if k := adminString(t, `SELECT object_kind FROM agent_commit WHERE company_id = $1`, b.companyID); k != "master" {
		t.Fatalf("agent_commit.object_kind = %q, want master", k)
	}
}
