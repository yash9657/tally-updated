package server_test

// mcp_profiles_test.go — the wave-2 merge's join of W6 and W3/W2 breadth: an
// MCP client sees W3's five profile tools under the agent's own names, and a
// call reaches the SAME ProfileReader the in-app agent uses (through
// agent.Engine.Submit, recorded as decide:k), so the per-fact ACL and the
// stored_text label proved by TestAgentProfileToolsAreRegisteredAndACLFiltered
// hold for both planners by construction.
//
// Mutation: drop the profile-tool loop from mcp.BuildManifest -> the listing
// check fails and every call is "Unknown tool".

import (
	"net/http"
	"testing"

	"github.com/khata/cored/internal/agent"
	"github.com/khata/cored/internal/mcp"
)

func TestMCPProfileToolsReachW3ThroughTheAgent(t *testing.T) {
	srv := newAgentServer(t)
	b := profiledBooks(t, srv)
	g := createMCPGrant(t, srv, b.base, nil, nil) // read-only: profiles need no write key
	c := newMCPClient(t, srv, b.base, g, grantToken(t, g))
	c.initialize("2025-06-18")

	names := c.toolNames()
	for _, n := range agent.ProfileTools {
		if names[n] == nil {
			t.Fatalf("tools/list has no %s", n)
		}
	}
	if names[mcp.ToolReverseVoucher] != nil {
		t.Fatal("a read-only grant was shown reverse_voucher")
	}

	r := c.tool(agent.ToolCustomerProfile, map[string]any{"name": "Bharat Stores"})
	if r.isError() || r.errCode() != 0 {
		t.Fatalf("customer profile over MCP: %d %s", r.code, r.raw)
	}
	prof := asMap(asMap(r.structured()["result"])["profile"])
	found := false
	for _, f := range asList(prof["facts"]) {
		if asMap(f)["fact_key"] == "receivable_outstanding" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the customer profile over MCP has no receivable fact: %s", r.raw)
	}
	// The agent's own argument check answers, not a second one here.
	if r := c.tool(agent.ToolSupplierProfile, map[string]any{}); !r.isError() {
		t.Fatalf("a supplier profile with no name: %s", r.raw)
	}
	if r := c.tool(agent.ToolTenantProfile, map[string]any{}); r.isError() {
		t.Fatalf("tenant profile over MCP: %s", r.raw)
	}
	if code := deleteSession(t, srv, c); code != http.StatusNoContent {
		t.Fatalf("close session: %d", code)
	}
}
