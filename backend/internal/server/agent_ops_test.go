package server_test

// agent_ops_test.go — W2 breadth, priority 3: stock vouchers, sales/purchase
// orders and bank-reconciliation tick / un-tick, each previewed (the effect
// read back from the handler's own transaction, then rolled back), approved,
// and committed exactly once.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/khata/cored/internal/agent"
	"github.com/khata/cored/internal/server"
)

// previewThenCommit is the two-step script every op below uses.
func previewThenCommit(action string, params map[string]string, body map[string]any) script {
	return func(t *testing.T, turn int, res []json.RawMessage) agent.ModelDecision {
		switch turn {
		case 0:
			args := map[string]any{"action_id": action, "body": body}
			if params != nil {
				args["path_params"] = params
			}
			return decision(agent.ToolPreview, args)
		case 1:
			return decision(agent.ToolCommit, map[string]string{"preview": "tool:0"})
		default:
			return finish("Done.")
		}
	}
}

func createItem(t *testing.T, srv *server.Server, b agentBooks, name string) string {
	t.Helper()
	rec, resp := do(t, srv, http.MethodPost, b.base+"/stock-items", map[string]any{"name": name,
		"opening_qty": "100", "opening_rate": "50", "created_by": "owner"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d %s", rec.Code, rec.Body.String())
	}
	return resp["stock_item_id"].(string)
}

func TestAgentStockOrderAndBankRecThroughTheGate(t *testing.T) {
	srv := newAgentServer(t)
	b := newAgentBooks(t, srv)
	item := createItem(t, srv, b, "Widget")
	godown := adminString(t, `SELECT id::text FROM godown WHERE company_id = $1 AND name = 'Main Location'`, b.companyID)

	t.Run("a delivery note: its stock movement is the effect", func(t *testing.T) {
		before := fingerprint(t, b.companyID)
		runID, _ := runToQuiet(t, srv, b, previewThenCommit("post.vouchers.delivery-note", nil, map[string]any{
			"voucher_date": "2025-05-15", "narration": "dispatch to Acme",
			"inventory": []any{map[string]any{"stock_item_id": item, "godown_id": godown, "direction": "OUT", "qty": "5", "rate": "80", "amount": "400"}},
		}), map[string]any{"instruction": "Dispatch 5 Widgets to Acme on 15 May 2025.", "delegate": []string{"create delivery-note"}})
		a := onePending(t, srv, b, runID)
		if fingerprint(t, b.companyID) != before {
			t.Fatal("the stock-voucher preview left a trace")
		}
		st := asList(effectOf(t, a)["stock"])
		if len(st) != 1 || asMap(st[0])["item"] != "Widget" || asMap(st[0])["direction"] != "OUT" || asMap(st[0])["qty"] != "5.0000" {
			t.Fatalf("effect.stock = %v, want Widget OUT 5", st)
		}
		decideAndDrain(t, srv, b, a["id"].(string))
		if got := fingerprint(t, b.companyID); got.Stock != before.Stock+1 || got.Versions != before.Versions+1 || got.Commits != before.Commits+1 {
			t.Fatalf("after the approved delivery note: %+v (before %+v)", got, before)
		}
	})

	t.Run("a sales order: its head and lines are the effect", func(t *testing.T) {
		before := fingerprint(t, b.companyID)
		runID, _ := runToQuiet(t, srv, b, previewThenCommit("post.sales-orders", nil, map[string]any{
			"order_no": "SO-7", "order_date": "2025-05-16", "party_ledger_id": b.ledgers["Acme Retail"],
			"lines": []any{map[string]any{"stock_item_id": item, "qty": "20", "rate": "80"}},
		}), map[string]any{"instruction": "Book a sales order SO-7 for 20 Widgets at Rs 80 for Acme Retail.", "delegate": []string{"create sales-order"}})
		a := onePending(t, srv, b, runID)
		os := asList(effectOf(t, a)["orders"])
		if len(os) != 1 || asMap(os[0])["order_no"] != "SO-7" || len(asList(asMap(os[0])["lines"])) != 1 {
			t.Fatalf("effect.orders = %v, want SO-7 with one line", os)
		}
		if fingerprint(t, b.companyID) != before {
			t.Fatal("the order preview left a trace")
		}
		decideAndDrain(t, srv, b, a["id"].(string))
		if got := fingerprint(t, b.companyID); got.Orders != before.Orders+1 {
			t.Fatalf("orders after the approval: %+v (before %+v)", got, before)
		}
		if k := adminString(t, `SELECT object_kind FROM agent_commit WHERE company_id = $1 AND action_id = 'post.sales-orders'`, b.companyID); k != "order" {
			t.Fatalf("object_kind = %q", k)
		}
	})

	t.Run("bank reconciliation: tick then un-tick, each with a before and an after", func(t *testing.T) {
		vid := postHuman(t, srv, b, "payment", rentPayment(b, "25000.00", "rent"))
		params := map[string]string{"ledgerID": b.ledgers["HDFC Bank"], "voucherID": vid, "legNo": "0"}
		runID, _ := runToQuiet(t, srv, b, previewThenCommit("put.bank-reconciliation._ledgerid._voucherid._legno", params,
			map[string]any{"bank_date": "2025-05-12"}),
			map[string]any{"instruction": "The rent cheque " + vid + " cleared HDFC on 12 May 2025; reconcile it.",
				"delegate": []string{"alter bank-reconciliation"}})
		a := onePending(t, srv, b, runID)
		bd := asList(effectOf(t, a)["bank_dates"])
		if len(bd) != 1 || asMap(bd[0])["after"] != "2025-05-12" || asMap(bd[0])["before"] != nil {
			t.Fatalf("effect.bank_dates = %v, want unticked -> 2025-05-12", bd)
		}
		if n := adminInt(t, `SELECT count(*) FROM bank_reconciliation WHERE voucher_id = $1`, vid); n != 0 {
			t.Fatal("the tick preview left a row")
		}
		decideAndDrain(t, srv, b, a["id"].(string))
		if d := adminString(t, `SELECT to_char(bank_date, 'YYYY-MM-DD') FROM bank_reconciliation WHERE voucher_id = $1`, vid); d != "2025-05-12" {
			t.Fatalf("bank date after the approval = %q", d)
		}

		runID, _ = runToQuiet(t, srv, b, previewThenCommit("delete.bank-reconciliation._ledgerid._voucherid._legno", params,
			map[string]any{}), map[string]any{"instruction": "Un-reconcile the rent cheque " + vid + "; the bank reversed it.",
			"delegate": []string{"alter bank-reconciliation"}})
		a = onePending(t, srv, b, runID)
		bd = asList(effectOf(t, a)["bank_dates"])
		if len(bd) != 1 || asMap(bd[0])["before"] != "2025-05-12" || asMap(bd[0])["after"] != nil {
			t.Fatalf("un-tick effect.bank_dates = %v, want 2025-05-12 -> unticked", bd)
		}
		if n := adminInt(t, `SELECT count(*) FROM bank_reconciliation WHERE voucher_id = $1`, vid); n != 1 {
			t.Fatal("the un-tick preview deleted the row for real")
		}
		decideAndDrain(t, srv, b, a["id"].(string))
		if n := adminInt(t, `SELECT count(*) FROM bank_reconciliation WHERE voucher_id = $1`, vid); n != 0 {
			t.Fatal("the approved un-tick left the row")
		}
		if k := adminString(t, `SELECT object_kind FROM agent_commit WHERE company_id = $1 AND action_id LIKE 'delete.bank%'`, b.companyID); k != "bank_reconciliation" {
			t.Fatalf("object_kind = %q", k)
		}
	})
}
