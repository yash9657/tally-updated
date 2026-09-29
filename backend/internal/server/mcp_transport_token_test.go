package server_test

// mcp_transport_token_test.go — wave-2 review, finding 3: the MCP client's
// credential cannot act as the person. Before 0106 the client was configured
// with the human's (stepped-up) JWT, and that same token could decide the
// approval the client's own proposal had parked on.
//
// Mutation proofs (run for the review): dropping authenticate's
// "!onTransport" refusal lets the transport token reach the decision route
// (the decision case goes 403 step-up / 200 instead of 401); dropping the
// "onTransport + JWT" refusal lets the owner's JWT drive the transport again
// (the JWT case goes 200).

import (
	"net/http"
	"testing"

	"github.com/khata/cored/internal/mcp"
	"github.com/khata/cored/internal/policy"
)

func TestMCPTransportTokenCannotActAsThePerson(t *testing.T) {
	srv := newAgentServer(t)
	b := newAgentBooks(t, srv)
	setAutonomy(t, b.companyID, "create payment", policy.L3)
	grant := createMCPGrant(t, srv, b.base, []string{"create payment"}, nil)
	tok := grantToken(t, grant)
	c := newMCPClient(t, srv, b.base, grant, tok)
	c.initialize("2025-11-25")
	if pv := c.tool("post_vouchers_payment", paymentArgs(b, "Office Rent", "HDFC Bank", "4900.00")); pv.isError() {
		t.Fatalf("preview: %s", pv.raw)
	}
	cm := c.tool(mcp.ToolCommit, map[string]any{"preview": "tool:0"})
	gate, _ := cm.structured()["gate"].(map[string]any)
	approvalID, _ := gate["approval_id"].(string)
	if approvalID == "" {
		t.Fatalf("no approval parked: %s", cm.raw)
	}

	t.Run("the client's token cannot decide its own approval", func(t *testing.T) {
		rec, _ := doRaw(t, srv, http.MethodPost, b.base+"/agent/approvals/"+approvalID+"/decision", tok,
			map[string]any{"decision": "approve"})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("decision with the transport token = %d %s, want 401", rec.Code, rec.Body.String())
		}
		pollUntilQuiet(t, srv)
		if n := countVouchers(t, b.companyID); n != 0 {
			t.Fatalf("%d voucher(s) posted after the client tried to approve itself", n)
		}
	})
	t.Run("nor post a voucher around the agent, nor make a grant", func(t *testing.T) {
		rec, _ := doRaw(t, srv, http.MethodPost, b.base+"/vouchers/payment", tok,
			voucherBody("2025-05-20", "direct", [3]string{b.ledgers["Fraudster Pvt Ltd"], "D", "4900.00"},
				[3]string{b.ledgers["HDFC Bank"], "C", "4900.00"}))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("POST /vouchers/payment with the transport token = %d, want 401", rec.Code)
		}
		rec, _ = doRaw(t, srv, http.MethodPost, b.base+"/agent/mcp/grants", tok, map[string]any{"client_name": "x"})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("POST /agent/mcp/grants with the transport token = %d, want 401", rec.Code)
		}
		if n := countVouchers(t, b.companyID); n != 0 {
			t.Fatalf("%d voucher(s)", n)
		}
	})
	t.Run("the person's own JWT does not drive the transport", func(t *testing.T) {
		jwt := newMCPClient(t, srv, b.base, grant, ownerToken(t, srv))
		if r := jwt.call("initialize", map[string]any{"protocolVersion": "2025-11-25"}); r.code != http.StatusUnauthorized {
			t.Fatalf("transport with the owner's JWT = %d %s, want 401", r.code, r.raw)
		}
	})
	t.Run("a wrong token on the right grant is refused", func(t *testing.T) {
		bad := newMCPClient(t, srv, b.base, grant, mcp.TransportTokenPrefix+"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
		if r := bad.call("initialize", map[string]any{"protocolVersion": "2025-11-25"}); r.code != http.StatusUnauthorized {
			t.Fatalf("a forged transport token = %d, want 401", r.code)
		}
	})
	t.Run("CONTROL: the person, with their own JWT, decides it", func(t *testing.T) {
		decideApproval(t, srv, b, approvalID, map[string]any{"decision": "approve"})
		pollUntilQuiet(t, srv)
		if n := countVouchers(t, b.companyID); n != 1 {
			t.Fatalf("after the human's approval: %d vouchers, want 1", n)
		}
	})
}
