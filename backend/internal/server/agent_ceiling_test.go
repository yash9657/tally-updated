package server_test

// agent_ceiling_test.go — wave-2 review, finding 1: a run's delegated ceiling
// (max_amount_paise) holds for writes that post no ledger legs. Before the
// fix the gate's amount was the preview's DEBIT, zero for an order or a
// stock voucher, so a run capped at Rs 1,000 committed a Rs 10 crore
// purchase order at L3 with nobody asked.
//
// Mutation proofs (run for the review): gate() back to DebitPaise-only turns
// the "over the ceiling" case red (0 pending, the order committed); dropping
// policy.Decide's unmeasurable-ceiling rule turns the stock-journal-free
// master case red (the valuation-method alter commits); ValuePaise returning ok=false for orders turns the
// "under the ceiling" case red (it waits instead of committing).

import (
	"testing"

	"github.com/khata/cored/internal/policy"
)

func TestAgentDelegatedCeilingHoldsForOrdersAndMasters(t *testing.T) {
	srv := newAgentServer(t)
	b := newAgentBooks(t, srv)
	item := createItem(t, srv, b, "Widget")
	setAutonomy(t, b.companyID, "create purchase-order", policy.L3)
	setAutonomy(t, b.companyID, "alter master", policy.L3)
	const ceiling = 100000 // Rs 1,000

	po := func(no, qty, rate string) map[string]any {
		return map[string]any{"order_no": no, "order_date": "2025-05-16", "party_ledger_id": b.ledgers["Sharma Traders"],
			"lines": []any{map[string]any{"stock_item_id": item, "qty": qty, "rate": rate}}}
	}

	t.Run("a Rs 10 crore purchase order in a Rs 1,000 run waits for a human", func(t *testing.T) {
		before := fingerprint(t, b.companyID)
		runID, _ := runToQuiet(t, srv, b, previewThenCommit("post.purchase-orders", nil, po("PO-9", "100000", "1000")),
			map[string]any{"instruction": "Raise PO-9 on Sharma Traders.", "delegate": []string{"create purchase-order"},
				"max_amount_paise": ceiling})
		p := pendingApprovals(t, srv, b, runID)
		if len(p) != 1 {
			t.Fatalf("pending approvals = %d, want 1 (run %v)", len(p), getRun(t, srv, b, runID)["status"])
		}
		if got := fingerprint(t, b.companyID); got.Orders != before.Orders {
			t.Fatalf("orders %d -> %d: an over-ceiling order committed with no human", before.Orders, got.Orders)
		}
		if amt, _ := p[0]["amount_paise"].(float64); int64(amt) != 100000*1000*100 {
			t.Fatalf("approval amount_paise = %v, want the order's value %d", p[0]["amount_paise"], int64(100000*1000*100))
		}
	})

	t.Run("an order under the ceiling still commits at L3", func(t *testing.T) {
		before := fingerprint(t, b.companyID)
		runID, _ := runToQuiet(t, srv, b, previewThenCommit("post.purchase-orders", nil, po("PO-10", "2", "400")),
			map[string]any{"instruction": "Raise PO-10 on Sharma Traders.", "delegate": []string{"create purchase-order"},
				"max_amount_paise": ceiling})
		if p := pendingApprovals(t, srv, b, runID); len(p) != 0 {
			t.Fatalf("a Rs 800 order in a Rs 1,000 run waited: %v", p[0]["reasons"])
		}
		if got := fingerprint(t, b.companyID); got.Orders != before.Orders+1 {
			t.Fatalf("orders %d -> %d, want one autonomous commit", before.Orders, got.Orders)
		}
	})

	t.Run("a master alter, which has no value to measure, waits under a ceiling", func(t *testing.T) {
		before := fingerprint(t, b.companyID)
		runID, _ := runToQuiet(t, srv, b, previewThenCommit("put.stock-items._stockitemid.valuation-method",
			map[string]string{"stockItemID": item}, map[string]any{"valuation_method": "FIFO"}),
			map[string]any{"instruction": "Value Widget on FIFO.", "delegate": []string{"alter master"},
				"max_amount_paise": ceiling})
		p := pendingApprovals(t, srv, b, runID)
		if len(p) != 1 {
			t.Fatalf("pending approvals = %d, want 1 (run %v)", len(p), getRun(t, srv, b, runID))
		}
		if got := fingerprint(t, b.companyID); got.Masters != before.Masters {
			t.Fatalf("master_audit %d -> %d: the alter committed under an unmeasurable ceiling", before.Masters, got.Masters)
		}
	})
}
