package server_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// TestABankLedgerUnderAUserSubGroupIsStillMarkedAsBank is invariant I-8 of
// docs/design/t1t4-ui-model.md, and it is the test that stopped a silent bug
// from shipping.
//
// A bank-reconciliation screen has to ask the operator WHICH bank account. The
// obvious source is GET /ledgers, which returns `group_name`. But group_name is
// the ledger's IMMEDIATE group, and bank-ness is defined by ltree ANCESTRY —
// `g.path <@ bg.path` against 'Bank Accounts' / 'Bank OD A/c', in
// reporting.BankLedgerPredicate. Since masters.CreateGroup accepts a
// ParentName, a company can perfectly well keep its accounts in
// "Current Accounts" UNDER "Bank Accounts", and such a ledger has
// group_name = "Current Accounts".
//
// So a client-side filter `group_name IN ('Bank Accounts','Bank OD A/c')` omits
// real bank accounts. It omits them SILENTLY — the operator sees a shorter list,
// not an error, and concludes the product does not know about the account they
// need to reconcile. That is why is_bank is derived on the SERVER, from the same
// predicate the statement endpoint uses.
//
// The test has three arms, and the middle one is the whole point:
//
//   - a ledger directly under "Bank Accounts"      => is_bank true  (the easy case)
//   - a ledger under a SUB-GROUP of "Bank Accounts" => is_bank true  (THE BUG)
//   - a Sundry Debtors ledger                       => is_bank false (the CONTROL,
//     without which a predicate hard-coded to `true` would pass)
//
// MUTATION PROOF: replace the predicate with a match on the immediate group name
// and the sub-group arm fails while the other two still pass — which is exactly
// the shape the bug would have had in production.
func TestABankLedgerUnderAUserSubGroupIsStillMarkedAsBank(t *testing.T) {
	srv := newServer(t)
	companyID, ownerTok, _ := newAdminCompany(t, srv)
	base := "/v1/companies/" + companyID
	sub := "isbank-" + uuid.NewString()[:8]

	// A user sub-group under the seeded primary "Bank Accounts". Nature and
	// is_pl are inherited from the parent, so neither is sent.
	const subGroup = "Current Accounts"
	rec, _ := doRaw(t, srv, http.MethodPost, base+"/groups", ownerTok, map[string]any{
		"name":        subGroup,
		"parent_name": "Bank Accounts",
		"created_by":  sub,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create sub-group %q = %d, want 201 (%s)", subGroup, rec.Code, rec.Body.String())
	}

	mk := func(name, group string) {
		t.Helper()
		rec, _ := doRaw(t, srv, http.MethodPost, base+"/ledgers", ownerTok, map[string]any{
			"name": name, "group_name": group, "created_by": sub,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create ledger %q in %q = %d, want 201 (%s)",
				name, group, rec.Code, rec.Body.String())
		}
	}

	direct := "HDFC Current " + uuid.NewString()[:6]
	nested := "ICICI Current " + uuid.NewString()[:6]
	debtor := "Acme Traders " + uuid.NewString()[:6]

	mk(direct, "Bank Accounts")
	mk(nested, subGroup)
	mk(debtor, "Sundry Debtors")

	_, listBody := doRaw(t, srv, http.MethodGet, base+"/ledgers", ownerTok, nil)
	rows := list(t, listBody, "ledgers")

	byName := map[string]map[string]any{}
	for _, r := range rows {
		n, _ := r["name"].(string)
		byName[n] = r
	}

	for _, c := range []struct {
		name, group string
		want        bool
		why         string
	}{
		{direct, "Bank Accounts", true,
			"a ledger directly under Bank Accounts is a bank account"},
		{nested, subGroup, true,
			"BANK-NESS IS ANCESTRY, NOT THE IMMEDIATE GROUP NAME — this ledger's " +
				"group_name is " + subGroup + ", and a client filtering on group_name " +
				"would silently hide a real bank account from the reconciliation picker"},
		{debtor, "Sundry Debtors", false,
			"the CONTROL: a receivable is not a bank account, so a predicate " +
				"hard-coded to true would be caught here"},
	} {
		row, ok := byName[c.name]
		if !ok {
			t.Fatalf("ledger %q missing from the chart of accounts", c.name)
		}
		v, present := row["is_bank"]
		if !present {
			t.Fatalf("listLedgers omits is_bank entirely — the client cannot "+
				"determine which ledgers are bank ledgers without it (%s)", c.why)
		}
		got, isBool := v.(bool)
		if !isBool {
			t.Errorf("is_bank for %q = %#v, want a JSON boolean", c.name, v)
			continue
		}
		if got != c.want {
			t.Errorf("is_bank for %q (group %q) = %v, want %v — %s",
				c.name, c.group, got, c.want, c.why)
		}
	}
}

// TestIsBankAgreesWithTheStatementEndpoint is invariant I-9, pinned from BOTH
// sides of the boundary as HANDOFF-T1-PRINTING.md §3.2(4) requires.
//
// Two places decide "is this a bank ledger": the list projection (so the picker
// can offer it) and reporting.BankReconciliationStatement (which refuses a
// non-bank ledger by name). If they ever disagree, one of two things happens,
// and both are bad: a ledger the picker offers is refused by the statement, or a
// ledger the picker hides is one the statement would have served.
//
// They share reporting.BankLedgerPredicate, so this test asserts the AGREEMENT
// rather than re-deriving the SQL: every ledger the list marks is_bank must get
// a 200 from the statement route, and every ledger it does not must get a 400.
// A drift in EITHER direction breaks it.
func TestIsBankAgreesWithTheStatementEndpoint(t *testing.T) {
	srv := newServer(t)
	companyID, ownerTok, _ := newAdminCompany(t, srv)
	base := "/v1/companies/" + companyID
	sub := "isbank-agree-" + uuid.NewString()[:8]

	rec, _ := doRaw(t, srv, http.MethodPost, base+"/groups", ownerTok, map[string]any{
		"name": "Deposit Accounts", "parent_name": "Bank Accounts", "created_by": sub,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create sub-group = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	// An OD account is a LIABILITY and still a bank account — the sign trap of
	// docs/design/t4-model.md §3.1. Included so the agreement is asserted across
	// both natures, not just the asset one.
	for _, l := range []struct{ name, group string }{
		{"Axis Deposit " + uuid.NewString()[:6], "Deposit Accounts"},
		{"SBI OD " + uuid.NewString()[:6], "Bank OD A/c"},
		{"Petty Cash " + uuid.NewString()[:6], "Cash-in-Hand"},
		{"Beta Supplies " + uuid.NewString()[:6], "Sundry Creditors"},
	} {
		rec, _ := doRaw(t, srv, http.MethodPost, base+"/ledgers", ownerTok, map[string]any{
			"name": l.name, "group_name": l.group, "created_by": sub,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create ledger %q = %d, want 201 (%s)", l.name, rec.Code, rec.Body.String())
		}
	}

	_, listBody := doRaw(t, srv, http.MethodGet, base+"/ledgers", ownerTok, nil)
	rows := list(t, listBody, "ledgers")

	var checkedBank, checkedNonBank int
	for _, r := range rows {
		name, _ := r["name"].(string)
		id, _ := r["id"].(string)
		isBank, _ := r["is_bank"].(bool)

		rec, _ := doRaw(t, srv, http.MethodGet,
			base+"/bank-reconciliation/"+id, ownerTok, nil)

		if isBank {
			checkedBank++
			if rec.Code != http.StatusOK {
				t.Errorf("ledger %q is_bank=true but the statement route answered %d "+
					"(%s) — the picker would offer an account the statement refuses",
					name, rec.Code, rec.Body.String())
			}
			continue
		}
		checkedNonBank++
		if rec.Code != http.StatusBadRequest {
			t.Errorf("ledger %q is_bank=false but the statement route answered %d — "+
				"the picker would hide an account the statement serves",
				name, rec.Code)
		}
	}

	// Guard the guard: a projection returning is_bank=false for everything would
	// make the loop above vacuous on its bank arm.
	if checkedBank < 2 {
		t.Errorf("only %d is_bank ledgers checked, want >= 2 (an asset bank account "+
			"and an OD one) — the agreement is not being exercised", checkedBank)
	}
	if checkedNonBank < 2 {
		t.Errorf("only %d non-bank ledgers checked, want >= 2", checkedNonBank)
	}
}
