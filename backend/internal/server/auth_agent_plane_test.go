package server

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// TestDataEntryIsRefusedTheAgentPlane pins what the two bare tokens promise
// (authz/vocabulary.go, 0094's header): with ZERO acl_rule rows, the seeded
// Data Entry level can neither start agent runs nor read or decide the
// approval inbox — the checker's seat is an Owner's until an owner grants it.
//
// The CONTROL comes first, for the reason auth_budget_report_test.go gives: a
// token that is not really a member is 403 everywhere and would satisfy every
// refusal below while proving nothing.
func TestDataEntryIsRefusedTheAgentPlane(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID
	dataEntry := aclSeededDataEntryToken(t, s, base, ownerTok)

	if rec, _ := aclRequest(t, s, http.MethodGet, base+"/trial-balance?as_on=2026-07-31", dataEntry, nil); rec.Code != http.StatusOK {
		t.Fatalf("CONTROL: Data Entry must read the Trial Balance = %d; the token is not a member and the refusals below would be vacuous", rec.Code)
	}
	someID := uuid.NewString()
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, base + "/agent/runs"},
		{http.MethodGet, base + "/agent/runs"},
		{http.MethodGet, base + "/agent/runs/" + someID},
		{http.MethodGet, base + "/agent/runs/" + someID + "/events"},
		{http.MethodPost, base + "/agent/runs/" + someID + "/cancel"},
		{http.MethodGet, base + "/agent/approvals"},
		{http.MethodGet, base + "/agent/approvals/" + someID},
		{http.MethodPost, base + "/agent/approvals/" + someID + "/decision"},
	} {
		if rec, _ := aclRequest(t, s, c.method, c.path, dataEntry, map[string]any{}); rec.Code != http.StatusForbidden {
			t.Errorf("Data Entry %s %s = %d, want 403", c.method, c.path, rec.Code)
		}
	}
	// And the Owner reaches the same doors (a 404 for an unknown run is the
	// handler answering, i.e. the PDP let it through).
	if rec, _ := aclRequest(t, s, http.MethodGet, base+"/agent/approvals", ownerTok, nil); rec.Code != http.StatusOK {
		t.Errorf("Owner GET /agent/approvals = %d, want 200", rec.Code)
	}
	if rec, _ := aclRequest(t, s, http.MethodGet, base+"/agent/runs/"+someID, ownerTok, nil); rec.Code != http.StatusNotFound {
		t.Errorf("Owner GET an unknown run = %d, want 404", rec.Code)
	}
}
