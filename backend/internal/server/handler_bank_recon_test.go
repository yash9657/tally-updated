package server

// T4 bank reconciliation — the HTTP surface and its ACL.
//
// The interesting property here is that ONE feature carries TWO different
// authorities in opposite directions, and migration 0046 has to get both right:
// the seeded Data Entry level MAY tick an item off (clerical, moves nothing) and
// MAY NOT read the statement (the company's whole bank position). A test that
// only checked one of them would pass against a migration that granted or
// refused both.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// jsonList pulls an array of objects out of a decoded response body. The
// identically-named helper in handler_admin_test.go lives in package
// server_test and is not reachable from this internal test file.
func jsonList(t *testing.T, body map[string]any, key string) []map[string]any {
	t.Helper()
	raw, ok := body[key].([]any)
	if !ok {
		t.Fatalf("response has no %q array: %v", key, body)
	}
	out := make([]map[string]any, 0, len(raw))
	for _, e := range raw {
		m, ok := e.(map[string]any)
		if !ok {
			t.Fatalf("%q contains a non-object element: %v", key, e)
		}
		out = append(out, m)
	}
	return out
}

// bankACLFixture is a company with a bank ledger and one posted payment, plus
// both tokens.
type bankACLFixture struct {
	base      string
	ownerTok  string
	dataEntry string
	bankID    string
	voucherID string
}

func newBankACLFixture(t *testing.T, s *Server) bankACLFixture {
	t.Helper()
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID
	sub := "bank-acl-" + uuid.NewString()[:8]

	mk := func(name, group string) string {
		t.Helper()
		rec, body := aclRequest(t, s, http.MethodPost, base+"/ledgers", ownerTok,
			map[string]any{"name": name, "group_name": group, "created_by": sub})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create ledger %q = %d, want 201 (%s)", name, rec.Code, rec.Body.String())
		}
		id, _ := body["ledger_id"].(string)
		return id
	}
	// "Bank Accounts" is a system group seeded with every company. A ledger under
	// it is what makes this reconcilable at all.
	bankID := mk("HDFC Current", "Bank Accounts")
	rentID := mk("Rent Paid", "Indirect Expenses")

	rec, body := aclRequest(t, s, http.MethodPost, base+"/vouchers/payment", ownerTok, map[string]any{
		"voucher_date": "2026-07-10",
		"created_by":   sub,
		"narration":    "cheque 000123",
		"postings": []map[string]any{
			{"ledger_id": rentID, "dr_cr": "D", "amount": "5000.0000"},
			{"ledger_id": bankID, "dr_cr": "C", "amount": "5000.0000"},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("post payment = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	voucherID, _ := body["voucher_id"].(string)

	return bankACLFixture{
		base:      base,
		ownerTok:  ownerTok,
		dataEntry: aclSeededDataEntryToken(t, s, base, ownerTok),
		bankID:    bankID,
		voucherID: voucherID,
	}
}

// TestTheOwnerCanReconcileOverHTTPEndToEnd drives the whole loop through the
// routes: read the statement, clear an item, see it move from un-cleared to
// cleared, un-clear it, see it move back. Without this the engine is reachable
// only from Go — which is exactly what T1 shipped and the handoff calls a parity
// gap rather than a nicety.
func TestTheOwnerCanReconcileOverHTTPEndToEnd(t *testing.T) {
	s := payrollServer(t)
	f := newBankACLFixture(t, s)
	stmt := f.base + "/bank-reconciliation/" + f.bankID
	leg := stmt + "/" + f.voucherID + "/0"

	rec, body := aclRequest(t, s, http.MethodGet, stmt+"?as_on=2026-07-31", f.ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET statement = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if got := len(jsonList(t, body, "uncleared")); got != 1 {
		t.Fatalf("uncleared = %d, want 1 before reconciling (%s)", got, rec.Body.String())
	}
	if bal, _ := body["balanced"].(bool); !bal {
		t.Errorf("statement is not balanced before reconciling: %s", rec.Body.String())
	}
	if books, _ := body["balance_as_per_books"].(string); books != "-5000.0000" {
		t.Errorf("balance_as_per_books = %q, want -5000.0000", books)
	}

	rec, _ = aclRequest(t, s, http.MethodPut, leg, f.ownerTok,
		map[string]any{"bank_date": "2026-07-14", "created_by": "bank-acl"})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT bank date = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	rec, body = aclRequest(t, s, http.MethodGet, stmt+"?as_on=2026-07-31", f.ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET statement after clearing = %d (%s)", rec.Code, rec.Body.String())
	}
	if got := len(jsonList(t, body, "cleared")); got != 1 {
		t.Errorf("cleared = %d, want 1 after reconciling (%s)", got, rec.Body.String())
	}
	if got := len(jsonList(t, body, "uncleared")); got != 0 {
		t.Errorf("uncleared = %d, want 0 after reconciling (%s)", got, rec.Body.String())
	}
	// The bank now agrees with the books, so the un-cleared net is zero.
	if net, _ := body["uncleared_net"].(string); net != "0.0000" {
		t.Errorf("uncleared_net = %q, want 0.0000 once everything has cleared", net)
	}

	rec, _ = aclRequest(t, s, http.MethodDelete, leg, f.ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE bank date = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	rec, body = aclRequest(t, s, http.MethodGet, stmt+"?as_on=2026-07-31", f.ownerTok, nil)
	if got := len(jsonList(t, body, "uncleared")); got != 1 {
		t.Errorf("uncleared = %d, want 1 after un-reconciling (%s)", got, rec.Body.String())
	}
}

// TestDataEntryMayTickOffButMayNotReadTheStatement is migration 0046's gate, and
// it is a DIFFERENTIAL test: the same token, the same company, two routes,
// opposite answers.
//
// The CONTROL is what makes it worth having. Every "403" assertion here would be
// satisfied by a token that is refused everything — an unclaimed invite, a level
// with no rules. So the test first shows the token reaching the Trial Balance,
// which migration 0011's coarse `allow display report` grants and which is
// precisely the grant 0046's disallow has to counterweight.
func TestDataEntryMayTickOffButMayNotReadTheStatement(t *testing.T) {
	s := payrollServer(t)
	f := newBankACLFixture(t, s)
	stmt := f.base + "/bank-reconciliation/" + f.bankID
	leg := stmt + "/" + f.voucherID + "/0"

	// CONTROL 1 — the token is not refused everything.
	if rec, _ := aclRequest(t, s, http.MethodGet,
		f.base+"/trial-balance?as_on=2026-07-31", f.dataEntry, nil); rec.Code != http.StatusOK {
		t.Fatalf("CONTROL: Data Entry GET /trial-balance = %d, want 200 — this token is refused "+
			"everything, so the 403 below would prove nothing (%s)", rec.Code, rec.Body.String())
	}

	// THE GRANT: ticking an item off is clerical and moves no balance, so the
	// operator who keys the cheques can reconcile them. Without 0046's
	// `allow alter bank-reconciliation` this is 403 and the feature reaches
	// nobody.
	if rec, _ := aclRequest(t, s, http.MethodPut, leg, f.dataEntry,
		map[string]any{"bank_date": "2026-07-14", "created_by": "de"}); rec.Code != http.StatusOK {
		t.Errorf("Data Entry PUT bank date = %d, want 200 — 0046's "+
			"`allow alter bank-reconciliation` is missing, so the clerk who keys the "+
			"cheques cannot tick them off (%s)", rec.Code, rec.Body.String())
	}

	// THE REFUSAL: the statement is the company's entire bank position. 0011's
	// coarse `allow display report` matches ANY report:NAME at specificity 1, so
	// without 0046's explicit disallow at specificity 2 this would be GRANTED by
	// a rule written years earlier for a different purpose.
	if rec, _ := aclRequest(t, s, http.MethodGet, stmt, f.dataEntry, nil); rec.Code != http.StatusForbidden {
		t.Errorf("Data Entry GET the bank reconciliation statement = %d, want 403 — the seeded "+
			"level can now read every un-cleared cheque and counterparty in the company, because "+
			"0011's coarse `allow display report` reaches report:bank-reconciliation and 0046's "+
			"disallow is missing or too weak (%s)", rec.Code, rec.Body.String())
	}
}

// TestAnUnmappedBankReconMethodIsRefused pins the default-deny arm of
// routeAction for this prefix: a method nobody mapped must be 403, not served.
func TestAnUnmappedBankReconMethodIsRefused(t *testing.T) {
	s := payrollServer(t)
	f := newBankACLFixture(t, s)
	path := f.base + "/bank-reconciliation/" + f.bankID + "/" + f.voucherID + "/0"

	// POST is deliberately not mapped. Even for an OWNER it must be refused —
	// default-deny is a property of the whole {companyID} subtree, not a
	// privilege check.
	rec, _ := aclRequest(t, s, http.MethodPost, path, f.ownerTok, map[string]any{"bank_date": "2026-07-14"})
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST to an unmapped bank-reconciliation method = %d, want 403 or 405 (%s)",
			rec.Code, rec.Body.String())
	}
}

// TestABankDateBeforeTheVoucherDateIs400OverHTTP proves migration 0045's trigger
// message survives to the caller instead of being flattened into a 500. A
// refusal that does not say which field is wrong is how the T1 demo failure
// became a debugging session instead of a one-line fix.
func TestABankDateBeforeTheVoucherDateIs400OverHTTP(t *testing.T) {
	s := payrollServer(t)
	f := newBankACLFixture(t, s)
	leg := f.base + "/bank-reconciliation/" + f.bankID + "/" + f.voucherID + "/0"

	// The voucher is dated 2026-07-10.
	rec, _ := aclRequest(t, s, http.MethodPut, leg, f.ownerTok,
		map[string]any{"bank_date": "2026-07-09", "created_by": "x"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT a bank date before the voucher date = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "cheque cannot clear") {
		t.Errorf("the 400 body does not explain the refusal: %s", body)
	}
}
