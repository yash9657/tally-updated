package server

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// TestDataEntryIsRefusedTheInsightEngine pins the bare `insights` token's
// promise (0102's header): with ZERO acl_rule rows the seeded Data Entry level
// reaches none of the ten routes, and the Owner reaches all of them. The
// control comes first (auth_agent_plane_test.go's reason).
func TestDataEntryIsRefusedTheInsightEngine(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID
	dataEntry := aclSeededDataEntryToken(t, s, base, ownerTok)

	if rec, _ := aclRequest(t, s, http.MethodGet, base+"/trial-balance?as_on=2026-07-31", dataEntry, nil); rec.Code != http.StatusOK {
		t.Fatalf("CONTROL: Data Entry must read the Trial Balance = %d", rec.Code)
	}
	someID := uuid.NewString()
	routes := []struct{ method, path string }{
		{http.MethodGet, base + "/insights"},
		{http.MethodGet, base + "/insights/" + someID},
		{http.MethodGet, base + "/insights/" + someID + "/evidence"},
		{http.MethodPost, base + "/insights/" + someID + "/feedback"},
		{http.MethodGet, base + "/insights/digest"},
		{http.MethodPost, base + "/insights/digest"},
		{http.MethodGet, base + "/insights/suppressions"},
		{http.MethodPost, base + "/insights/suppressions"},
		{http.MethodDelete, base + "/insights/suppressions/" + someID},
		{http.MethodPost, base + "/insights/run"},
	}
	for _, c := range routes {
		if rec, _ := aclRequest(t, s, c.method, c.path, dataEntry, map[string]any{}); rec.Code != http.StatusForbidden {
			t.Errorf("Data Entry %s %s = %d, want 403", c.method, c.path, rec.Code)
		}
	}
	// The Owner reaches every door: a 403 here would mean a routeAction arm is
	// missing. Unknown ids answer 404 (the handler ran), bad bodies 4xx.
	for _, c := range routes {
		rec, _ := aclRequest(t, s, c.method, c.path, ownerTok, map[string]any{})
		if rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
			t.Errorf("Owner %s %s = %d; the PDP refused the owner", c.method, c.path, rec.Code)
		}
	}
}
