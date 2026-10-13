package server_test

// voucher_party_scope_test.go — wave-2 review, finding 4: the cross-tenant
// and born-blocked checks cover a voucher's PARTY and its bill allocations,
// not only its posting legs. voucher_version.party_ledger_id (and
// bill_allocation.ledger_id) are single-column FKs to ledger(id), so the
// database alone accepted company B's ledger, or a blocked one, as A's
// voucher party.
//
// Mutation proofs (run for the review): refuseBlockedLedgers given no party
// turns the cross-tenant party case 201; with the first case skipped, the
// blocked-party case is 201 too. The bill-allocation case is NOT independently proven by this
// check: dropping the bills from it leaves the case 422, because
// validateBillAllocationsSumToPosting already refuses an allocation on a
// ledger the voucher does not post to. It stays as a pin.

import (
	"context"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestAVoucherPartyMustBeAnUnblockedLedgerOfItsCompany(t *testing.T) {
	srv := newAgentServer(t)
	a := newAgentBooks(t, srv)
	bb := newAgentBooks(t, srv) // the same owner is a member of both
	pay := func(party string) map[string]any {
		body := voucherBody("2025-05-20", "rent", [3]string{a.ledgers["Office Rent"], "D", "100.00"},
			[3]string{a.ledgers["HDFC Bank"], "C", "100.00"})
		body["party_ledger_id"] = party
		return body
	}

	rec, _ := do(t, srv, http.MethodPost, a.base+"/vouchers/payment", pay(bb.ledgers["HDFC Bank"]))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("A's payment naming B's ledger as its party = %d %s, want 422", rec.Code, rec.Body.String())
	}
	if n := adminInt(t, `SELECT count(*) FROM voucher_version WHERE company_id = $1 AND party_ledger_id = $2`,
		a.companyID, bb.ledgers["HDFC Bank"]); n != 0 {
		t.Fatalf("%d voucher_version row(s) of A name B's ledger", n)
	}

	adminExecSQL(t, `UPDATE ledger SET blocked_at = now(), blocked_reason = 'review test' WHERE id = $1`, a.ledgers["Fraudster Pvt Ltd"])
	rec, _ = do(t, srv, http.MethodPost, a.base+"/vouchers/payment", pay(a.ledgers["Fraudster Pvt Ltd"]))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a payment whose party is a blocked ledger = %d %s, want 422", rec.Code, rec.Body.String())
	}

	// A bill allocation on another company's ledger is refused too.
	body := pay(a.ledgers["HDFC Bank"])
	body["bill_allocations"] = []any{map[string]any{"ledger_id": bb.ledgers["Sharma Traders"], "ref_name": "X-1",
		"ref_type": "NEW", "dr_cr": "D", "amount": "100.00"}}
	rec, _ = do(t, srv, http.MethodPost, a.base+"/vouchers/payment", body)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a bill allocation on B's ledger = %d %s, want 422", rec.Code, rec.Body.String())
	}

	// CONTROL: the same payment with A's own unblocked party posts.
	rec, _ = do(t, srv, http.MethodPost, a.base+"/vouchers/payment", pay(a.ledgers["HDFC Bank"]))
	if rec.Code != http.StatusCreated {
		t.Fatalf("CONTROL: A's payment with its own party = %d %s, want 201", rec.Code, rec.Body.String())
	}
}

// adminExecSQL runs one statement as the admin role.
func adminExecSQL(t *testing.T, sql string, args ...any) {
	t.Helper()
	c, err := pgx.Connect(context.Background(), adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	if _, err := c.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("admin exec %q: %v", sql, err)
	}
}
