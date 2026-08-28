package server

// T4 SLICE 2b — WHO MAY MATCH, AND THE ROUTE-IDENTITY PROOF THAT GOES WITH IT.
//
// Slice 2b writes NO ACL migration (migration 0088's footer says why), so these
// tests are the assertion that the absence is a decision rather than an
// omission: every 2b route lands on an authority some EXISTING migration
// already governs, and the seeded Data Entry level meets each one as intended.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/khata/cored/internal/authz"
)

// mapOf is routeAction's answer for one method and path, with the chi route
// context the middleware would have seen.
func mapOf(t *testing.T, method, route, companyID string) (authz.Action, authz.Resource, string, bool) {
	t.Helper()
	path, rctx := synthPath(route, companyID)
	req := httptest.NewRequest(method, path, nil).
		WithContext(context.WithValue(context.Background(), chi.RouteCtxKey, rctx))
	return routeAction(req)
}

// TestMatchingBorrowsTheAuthoritiesItAlreadyHasAndInventsNone is the load-bearing
// route-identity assertion of slice 2b.
//
// ⛔ THE THIRD CASE IS THE ONE THAT MATTERS. Accepting a proposed match is the
// EXISTING bank-reconciliation PUT, so it is judged Alter/bank-reconciliation —
// the same authority as ticking a bank date by hand. If a future change gave
// matching a route of its own, MATCHING WOULD BECOME A SECOND WAY TO ACQUIRE THE
// AUTHORITY TO ASSERT A CLEARANCE: a level refused the tick could reconcile by
// matching what it may not reconcile by typing, and the rule list would say the
// opposite of what the product did.
func TestMatchingBorrowsTheAuthoritiesItAlreadyHasAndInventsNone(t *testing.T) {
	companyID := uuid.NewString()
	const prefix = "/v1/companies/{companyID}"

	cases := []struct {
		what    string
		method  string
		route   string
		act     authz.Action
		res     authz.Resource
		resName string
		why     string
	}{
		{
			what: "the proposals read", method: http.MethodGet,
			route: prefix + "/bank-statement/{ledgerID}/matches",
			act:   authz.Display, res: "report", resName: "bank-statement",
			why: "proposing a match discloses the bank's own file, so it is governed by " +
				"the rule that governs the bank's own file — migration 0063's disallow",
		},
		{
			what: "the set-aside write", method: http.MethodPost,
			route: prefix + "/bank-statement/{ledgerID}/lines/{lineID}/set-aside",
			act:   authz.Create, res: "bank-statement", resName: "",
			why: "it annotates the imported statement and writes no clearance",
		},
		{
			what: "undoing a set-aside", method: http.MethodPost,
			route: prefix + "/bank-statement/{ledgerID}/lines/{lineID}/restore",
			act:   authz.Create, res: "bank-statement", resName: "",
			why: "the same authority as making one; the METHOD is not what decides that",
		},
		{
			what: "ACCEPTING A MATCH", method: http.MethodPut,
			route: prefix + "/bank-reconciliation/{ledgerID}/{voucherID}/{legNo}",
			act:   authz.Alter, res: "bank-reconciliation", resName: "",
			why: "matching must not become a SECOND way to acquire the authority to " +
				"assert a clearance — it is the same route as the manual tick",
		},
	}

	for _, c := range cases {
		act, res, resName, mapped := mapOf(t, c.method, c.route, companyID)
		if !mapped {
			t.Errorf("%s (%s %s) is NOT mapped — it default-denies for every level",
				c.what, c.method, c.route)
			continue
		}
		if act != c.act || res != c.res || resName != c.resName {
			t.Errorf("%s (%s %s) maps to %s/%s:%s, want %s/%s:%s — %s",
				c.what, c.method, c.route, act, res, resName, c.act, c.res, c.resName, c.why)
		}
	}
}

// TestDataEntryIsRefusedEveryMatchingRoute is migration 0063 reaching slice 2b's
// routes without slice 2b writing a rule.
//
// ⚠️ IT CREATES ITS COMPANY AT TEST TIME, so it exercises
// seed_company_auth_defaults rather than a backfill — §5.2 of
// HANDOFF-FEATURE-LOCK.md.
func TestDataEntryIsRefusedEveryMatchingRoute(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID
	dataEntry := aclSeededDataEntryToken(t, s, base, ownerTok)

	_, body := aclRequest(t, s, http.MethodPost, base+"/ledgers", ownerTok, map[string]any{
		"name": "HDFC 2b", "group_name": "Bank Accounts", "created_by": "owner",
	})
	bank, _ := body["ledger_id"].(string)

	// ⚠️ THE CONTROL, twice over. Without both, every refusal below could pass
	// for the wrong reason.
	//
	// (1) Data Entry can still read SOME report — 0011's coarse
	//     `allow display report` is alive, so the refusal is 0063's specificity-2
	//     disallow rather than "this level reads nothing".
	if rec, _ := aclRequest(t, s, http.MethodGet,
		base+"/trial-balance?as_on=2026-07-31", dataEntry, nil); rec.Code != http.StatusOK {
		t.Fatalf("CONTROL: Data Entry must still read the Trial Balance = %d, want 200", rec.Code)
	}
	// (2) Data Entry can still RECONCILE — 0046 grants `alter
	//     bank-reconciliation`, deliberately, because ticking one cheque off is
	//     clerical. This is what makes the matching refusals meaningful: the
	//     level is not simply locked out of the bank screen.
	if rec, _ := aclRequest(t, s, http.MethodGet,
		base+"/bank-reconciliation/"+bank+"?as_on=2026-07-31", dataEntry, nil); rec.Code == http.StatusOK {
		t.Fatalf("CONTROL: 0046 disallows the BRS read to Data Entry; got 200, so the " +
			"seeded rules are not what this test assumes")
	}

	// THE PROPOSALS READ — refused by 0063's `disallow display
	// report:bank-statement` at specificity 2.
	if rec, _ := aclRequest(t, s, http.MethodGet,
		base+"/bank-statement/"+bank+"/matches", dataEntry, nil); rec.Code != http.StatusForbidden {
		t.Errorf("Data Entry read the match proposals = %d, want 403 — the proposals "+
			"contain the bank's own rows, including the ones no voucher exists for", rec.Code)
	}

	// THE SET-ASIDE WRITES — refused by default_access = 'none' with no coarse
	// rule reaching a BARE `bank-statement`. 0063 writes no allow.
	line := uuid.NewString()
	for _, verb := range []string{"set-aside", "restore"} {
		if rec, _ := aclRequest(t, s, http.MethodPost,
			base+"/bank-statement/"+bank+"/lines/"+line+"/"+verb, dataEntry,
			map[string]any{"created_by": "clerk"}); rec.Code != http.StatusForbidden {
			t.Errorf("Data Entry called %s = %d, want 403", verb, rec.Code)
		}
	}

	// AND THE OWNER REACHES ALL THREE. A refusal test whose allow arm is untested
	// cannot tell "correctly refused" from "broken for everyone".
	if rec, _ := aclRequest(t, s, http.MethodGet,
		base+"/bank-statement/"+bank+"/matches", ownerTok, nil); rec.Code != http.StatusOK {
		t.Errorf("Owner read the match proposals = %d, want 200", rec.Code)
	}
	// The two writes reach the HANDLER for an Owner and fail there on the
	// unknown line id — 404, NOT 403. That distinction is the whole point: 403
	// is the PDP, 404 is the handler, and seeing 404 proves the Owner passed the
	// PDP rather than that the route is broken.
	for _, verb := range []string{"set-aside", "restore"} {
		rec, _ := aclRequest(t, s, http.MethodPost,
			base+"/bank-statement/"+bank+"/lines/"+line+"/"+verb, ownerTok,
			map[string]any{"created_by": "owner"})
		if rec.Code == http.StatusForbidden {
			t.Errorf("Owner was refused %s = 403", verb)
		}
		if rec.Code != http.StatusNotFound {
			t.Errorf("Owner called %s on an unknown line = %d, want 404 (past the PDP, "+
				"refused by the handler)", verb, rec.Code)
		}
	}
}
