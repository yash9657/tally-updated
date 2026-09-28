package server_test

// ledger_tenant_test.go — a voucher may post only to ITS company's ledgers.
//
// Found at the wave-2 merge (HANDOFF-AGENTIC-WAVE-2 §4): posting.ledger_id's
// FK is single-column, and the ledger service never asked whether a posting's
// ledger belonged to the company, so a member of two companies could post a
// voucher in A whose legs named B's ledgers — 201, and two postings A's own
// reports could never join back. W6's cross-tenant MCP gate caught it once
// W2 breadth's effect readback stopped failing on the missing ledger names.
//
// Mutation: drop the count check in ledger.refuseBlockedLedgers -> this test
// fails with 201 and 2 postings under A.

import (
	"net/http"
	"strings"
	"testing"
)

func TestAVoucherCannotPostToAnotherCompanysLedgers(t *testing.T) {
	srv := newAgentServer(t)
	a := newAgentBooks(t, srv)
	bb := newAgentBooks(t, srv) // the same owner is a member of both
	rec, _ := do(t, srv, http.MethodPost, a.base+"/vouchers/payment", voucherBody("2025-05-20", "cross-tenant",
		[3]string{bb.ledgers["Office Rent"], "D", "700.00"}, [3]string{bb.ledgers["HDFC Bank"], "C", "700.00"}))
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "do not exist in this company") {
		t.Fatalf("a payment in A between B's ledgers = %d %s, want 422 naming the missing ledgers", rec.Code, rec.Body.String())
	}
	if n := adminInt(t, `SELECT count(*) FROM posting WHERE company_id = $1`, a.companyID); n != 0 {
		t.Fatalf("%d posting(s) written under A", n)
	}
	// One of A's own ledgers and one of B's: still refused, and nothing written.
	rec, _ = do(t, srv, http.MethodPost, a.base+"/vouchers/payment", voucherBody("2025-05-20", "half cross-tenant",
		[3]string{a.ledgers["Office Rent"], "D", "700.00"}, [3]string{bb.ledgers["HDFC Bank"], "C", "700.00"}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a payment in A crediting B's bank = %d %s, want 422", rec.Code, rec.Body.String())
	}
	// The control: the same body on A's own ledgers posts.
	rec, _ = do(t, srv, http.MethodPost, a.base+"/vouchers/payment", voucherBody("2025-05-20", "own",
		[3]string{a.ledgers["Office Rent"], "D", "700.00"}, [3]string{a.ledgers["HDFC Bank"], "C", "700.00"}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("CONTROL: A's own payment = %d %s", rec.Code, rec.Body.String())
	}
	if na, nb := countVouchers(t, a.companyID), countVouchers(t, bb.companyID); na != 1 || nb != 0 {
		t.Fatalf("vouchers: A %d (want 1), B %d (want 0)", na, nb)
	}
}
