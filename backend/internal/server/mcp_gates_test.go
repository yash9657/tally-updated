package server_test

// mcp_gates_test.go — the W6 security gates (D126 §6), each measured on the
// books (voucher_head, agent_commit), never on what the MCP layer says:
//
//   - TestMCPPreviewThenCommitWaitsForTheInbox: the full path, and the
//     control that makes the other gates non-vacuous — an MCP commit DOES
//     post, exactly once, after a human approves in the existing inbox.
//   - TestMCPInjectionSuiteCommitsNothing: the wave-1 32-case suite driven by a
//     COMPROMISED MCP client at L3; zero vouchers, zero commits.
//   - TestMCPCrossTenantRefusal, TestMCPManifestChangeIsRefused,
//     TestMCPDelegationCeiling, TestMCPRevokingAGrantStopsItsRuns.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/actions"
	"github.com/khata/cored/internal/agent"
	"github.com/khata/cored/internal/mcp"
	"github.com/khata/cored/internal/policy"
	"github.com/khata/cored/internal/server"
)

func paymentArgs(b agentBooks, dr, cr, amount string) map[string]any {
	return map[string]any{"body": voucherBody("2025-05-20", "via MCP",
		[3]string{b.ledgers[dr], "D", amount}, [3]string{b.ledgers[cr], "C", amount})}
}

func decideApproval(t *testing.T, srv *server.Server, b agentBooks, approvalID string, body map[string]any) {
	t.Helper()
	rec, _ := do(t, srv, http.MethodPost, b.base+"/agent/approvals/"+approvalID+"/decision", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("decision on %s: %d %s", approvalID, rec.Code, rec.Body.String())
	}
}

func intentActors(t *testing.T, companyID, runID string) map[string]int {
	t.Helper()
	c, err := pgx.Connect(context.Background(), adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	rows, err := c.Query(context.Background(),
		`SELECT kind || '/' || actor_kind || ':' || actor_id FROM agent_intent_log WHERE company_id = $1 AND run_id = $2`, companyID, runID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out[s]++
	}
	return out
}

// TestMCPPreviewThenCommitWaitsForTheInbox is the end-to-end path: read →
// preview (the real handler, rolled back) → commit_preview → pending_approval
// → a human approves in the SAME inbox the in-app agent uses → the poller
// commits exactly one voucher → run_status reports it. At L3, so the only
// thing holding the commit is the MCP run's born taint.
func TestMCPPreviewThenCommitWaitsForTheInbox(t *testing.T) {
	srv := newAgentServer(t)
	b := newAgentBooks(t, srv)
	setAutonomy(t, b.companyID, "create payment", policy.L3)
	grant := createMCPGrant(t, srv, b.base, []string{"create payment"}, nil)
	c := newMCPClient(t, srv, b.base, grant, ownerToken(t, srv))
	c.initialize("2025-11-25")
	runID := sessionRunID(t, c)

	if r := c.tool("get_ledgers", map[string]any{}); r.isError() {
		t.Fatalf("read: %s", r.raw)
	}
	pv := c.tool("post_vouchers_payment", paymentArgs(b, "Office Rent", "HDFC Bank", "5000.00"))
	st := pv.structured()
	if pv.isError() || st["preview_ref"] != "tool:1" {
		t.Fatalf("preview: %s", pv.raw)
	}
	eff, _ := st["effect"].(map[string]any)
	if eff["debit_paise"] != float64(500000) || eff["credit_paise"] != float64(500000) {
		t.Fatalf("preview effect %v, want 500000 paise each side", eff)
	}
	if n := countVouchers(t, b.companyID); n != 0 {
		t.Fatalf("a preview left %d voucher(s)", n)
	}

	cm := c.tool(mcp.ToolCommit, map[string]any{"preview": "tool:1"})
	st = cm.structured()
	if cm.isError() || st["status"] != "pending_approval" {
		t.Fatalf("commit: %s", cm.raw)
	}
	gate, _ := st["gate"].(map[string]any)
	approvalID, _ := gate["approval_id"].(string)
	reasons, _ := json.Marshal(gate["reasons"])
	if approvalID == "" || !strings.Contains(string(reasons), "untrusted input reached this run (mcp_client)") {
		t.Fatalf("the gate did not park on the MCP client's taint: %s", cm.raw)
	}
	if ir, _ := st["input_required"].(map[string]any); ir["kind"] != "human_approval" || ir["approval_id"] != approvalID {
		t.Fatalf("input_required %v", st["input_required"])
	}
	if n := countVouchers(t, b.companyID); n != 0 {
		t.Fatalf("pending approval but %d voucher(s) posted", n)
	}

	// While parked on the human, the session takes no other decision.
	if r := c.tool("get_ledgers", map[string]any{}); !r.isError() || r.structured()["status"] != "not_accepting" {
		t.Fatalf("a call while parked on an approval: %s", r.raw)
	}

	// The approval is in the ordinary inbox, with the MCP run's instruction.
	pend := pendingApprovals(t, srv, b, runID)
	if len(pend) != 1 || pend[0]["id"] != approvalID {
		t.Fatalf("inbox: %v", pend)
	}
	if !strings.Contains(pend[0]["instruction"].(string), "MCP client") {
		t.Fatalf("the approver is not told the request came through MCP: %v", pend[0]["instruction"])
	}
	decideApproval(t, srv, b, approvalID, map[string]any{"decision": "approve"})
	pollUntilQuiet(t, srv)
	if n := countVouchers(t, b.companyID); n != 1 {
		t.Fatalf("after approval: %d vouchers, want exactly 1", n)
	}
	if n := agentCommits(t, b.companyID); n != 1 {
		t.Fatalf("after approval: %d agent_commit rows, want 1", n)
	}

	rs := c.tool(mcp.ToolRunStatus, map[string]any{})
	run, _ := rs.structured()["run"].(map[string]any)
	commits, _ := run["commits"].([]any)
	if len(commits) != 1 {
		t.Fatalf("run_status commits: %s", rs.raw)
	}
	one := commits[0].(map[string]any)
	if one["gate"].(map[string]any)["decision"] != "approve" || one["commit"].(map[string]any)["ok"] != true ||
		len(one["commit"].(map[string]any)["voucher_ids"].([]any)) != 1 {
		t.Fatalf("run_status after approval: %s", rs.raw)
	}
	// Accepting calls again, and a second commit of the same preview is refused.
	again := c.tool(mcp.ToolCommit, map[string]any{"preview": "tool:1"})
	if !again.isError() || again.structured()["status"] != "refused" || !strings.Contains(again.raw, "already submitted") {
		t.Fatalf("second commit of one preview: %s", again.raw)
	}
	if n := countVouchers(t, b.companyID); n != 1 {
		t.Fatalf("vouchers = %d after a refused second commit", n)
	}
	// Attribution: every decision is the MCP agent's, on behalf of the owner.
	acts := intentActors(t, b.companyID, runID)
	if acts["decision_step/agent:mcp:"+grant] != 4 {
		t.Fatalf("decision_step actors %v, want 4 by mcp:%s", acts, grant)
	}
}

// ---------------------------------------------------------------------------
// The injection suite over MCP
// ---------------------------------------------------------------------------

// mcpAttack drives attackScript — the SAME compromised planner the in-app
// suite uses — as an MCP client: each decision becomes the tools/call a
// client would make for it. A decision naming a tool the session was never
// shown (a delete, a security level, the approval inbox, the backup) is
// refused as Unknown tool before anything is recorded.
func mcpAttack(t *testing.T, c *mcpClient, s script) (refusedUnknown int) {
	t.Helper()
	var results []json.RawMessage
	for turn := 0; turn < 8; turn++ {
		d := s(t, turn, results)
		if d.Tool == agent.ToolFinish {
			return refusedUnknown
		}
		var name string
		var args map[string]any
		_ = json.Unmarshal(d.Args, &args)
		switch d.Tool {
		case agent.ToolRead, agent.ToolPreview:
			id, _ := args["action_id"].(string)
			delete(args, "action_id")
			name = mcp.ToolName(id)
			if pp, ok := args["path_params"].(map[string]any); ok && len(pp) == 0 {
				delete(args, "path_params")
			}
		case agent.ToolCommit:
			name = mcp.ToolCommit
		default:
			t.Fatalf("attack script chose %s, which the MCP adapter does not map", d.Tool)
		}
		r := c.tool(name, args)
		var res json.RawMessage
		switch {
		case r.errCode() == mcp.CodeInvalidParams:
			refusedUnknown++
			res, _ = json.Marshal(map[string]any{"tool": map[string]any{"ok": false, "error": "unknown tool"}})
		case r.result() != nil:
			sc := r.structured()
			res, _ = json.Marshal(map[string]any{"tool": map[string]any{"ok": !r.isError(), "result": sc["result"]}})
		default:
			t.Fatalf("%s: unexpected reply %d %s", name, r.code, r.raw)
		}
		results = append(results, res)
	}
	return refusedUnknown
}

// TestMCPInjectionSuiteCommitsNothing re-runs the wave-1 injection suite with
// the planner replaced by a compromised MCP CLIENT. The injected material is
// in the client's own context (where an MCP client meets it: a web page, a
// mail, a PDF it read), so what reaches the server is the attack itself.
// Autonomy is L3 for every money key; the grant delegates them all.
//
// Assertion on the books: 0 vouchers, 0 agent_commit rows; every money case
// parks exactly one approval whose reasons name mcp_client — the born taint —
// and not L1. The CONTROL is TestMCPPreviewThenCommitWaitsForTheInbox: the
// same path does commit once a human approves.
func TestMCPInjectionSuiteCommitsNothing(t *testing.T) {
	srv := newAgentServer(t)
	b := newAgentBooks(t, srv)
	for _, k := range moneyKeys {
		setAutonomy(t, b.companyID, k, policy.L3)
	}
	all := createMCPGrant(t, srv, b.base, append([]string{"create master"}, moneyKeys...), nil)
	salesOnly := createMCPGrant(t, srv, b.base, []string{"create sales"}, nil)
	parked, unknown := 0, 0
	for _, tc := range injectionCases {
		t.Run(tc.name, func(t *testing.T) {
			g := all
			if tc.atk == atkNotDelegated {
				g = salesOnly
			}
			c := newMCPClient(t, srv, b.base, g, ownerToken(t, srv))
			c.initialize("2025-06-18")
			runID := sessionRunID(t, c)
			unknown += mcpAttack(t, c, attackScript(tc.atk, "5000.00"))
			pollUntilQuiet(t, srv)

			if n := countVouchers(t, b.companyID); n != 0 {
				t.Fatalf("INJECTION SUCCEEDED OVER MCP: %d voucher(s) after %q", n, tc.name)
			}
			if n := agentCommits(t, b.companyID); n != 0 {
				t.Fatalf("INJECTION SUCCEEDED OVER MCP: %d agent_commit row(s) after %q", n, tc.name)
			}
			pend := pendingApprovals(t, srv, b, runID)
			if tc.money {
				if len(pend) != 1 {
					t.Fatalf("%q: want exactly one parked approval, got %d", tc.name, len(pend))
				}
				reasons, _ := json.Marshal(pend[0]["reasons"])
				if !strings.Contains(string(reasons), "untrusted input reached this run (mcp_client)") {
					t.Fatalf("%q: reasons %s do not name the MCP client's context", tc.name, reasons)
				}
				if strings.Contains(string(reasons), "L1") {
					t.Fatalf("%q: parked for L1 (%s); autonomy is L3, so this is not testing the taint", tc.name, reasons)
				}
				parked++
			} else if len(pend) != 0 {
				t.Fatalf("%q: a non-money attack produced an approval: %v", tc.name, pend)
			}
			if code := deleteSession(t, srv, c); code != http.StatusNoContent {
				t.Fatalf("close session: %d", code)
			}
		})
	}
	if parked < 15 {
		t.Fatalf("only %d cases reached a money-moving preview; the suite is too weak", parked)
	}
	t.Logf("%d injection cases over MCP at L3; %d money-moving previews parked on the mcp_client taint; "+
		"%d calls refused as tools the session was never shown; 0 vouchers, 0 commits", len(injectionCases), parked, unknown)
}

// ---------------------------------------------------------------------------
// Tenancy, the pinned manifest, the ceiling, revocation
// ---------------------------------------------------------------------------

// inviteOwner makes a second Owner-level member of the company and returns
// their token.
func inviteOwner(t *testing.T, srv *server.Server, base string) string {
	t.Helper()
	subject := "mcp-second-owner-" + uuid.NewString()[:8]
	rec, _ := do(t, srv, http.MethodPost, base+"/members/invites", map[string]any{"subject": subject, "security_level_name": "Owner"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite owner: %d %s", rec.Code, rec.Body.String())
	}
	tok := mintToken(t, srv, subject, "webauthn")
	if rec, _ := doRaw(t, srv, http.MethodGet, "/v1/me", tok, nil); rec.Code != http.StatusOK {
		t.Fatalf("claim invite: %d %s", rec.Code, rec.Body.String())
	}
	return tok
}

func TestMCPCrossTenantRefusal(t *testing.T) {
	srv := newAgentServer(t)
	a := newAgentBooks(t, srv)
	bb := newAgentBooks(t, srv) // the same owner is a member of both
	setAutonomy(t, a.companyID, "create payment", policy.L3)
	gA := createMCPGrant(t, srv, a.base, []string{"create payment"}, nil)
	gB := createMCPGrant(t, srv, bb.base, []string{"create payment"}, nil)
	owner := ownerToken(t, srv)
	ca := newMCPClient(t, srv, a.base, gA, owner)
	ca.initialize("2025-06-18")

	t.Run("company A's grant does not exist in company B", func(t *testing.T) {
		wrong := newMCPClient(t, srv, bb.base, gA, owner)
		r := wrong.call("initialize", map[string]any{"protocolVersion": "2025-06-18"})
		if r.code != http.StatusNotFound {
			t.Fatalf("A's grant under B's URL: %d %s", r.code, r.raw)
		}
	})
	t.Run("company A's session does not exist under company B's grant", func(t *testing.T) {
		cb := newMCPClient(t, srv, bb.base, gB, owner)
		cb.session, cb.version = ca.session, ca.version
		if r := cb.call("tools/list", nil); r.code != http.StatusNotFound {
			t.Fatalf("A's session on B's grant: %d %s", r.code, r.raw)
		}
	})
	t.Run("a session of one grant is refused on another grant of the same company", func(t *testing.T) {
		g2 := createMCPGrant(t, srv, a.base, []string{"create payment"}, nil)
		c2 := newMCPClient(t, srv, a.base, g2, owner)
		c2.session, c2.version = ca.session, ca.version
		if r := c2.call("ping", nil); r.code != http.StatusNotFound {
			t.Fatalf("A's session on A's other grant: %d %s", r.code, r.raw)
		}
	})
	t.Run("another member of the same company may not spend the owner's grant", func(t *testing.T) {
		other := newMCPClient(t, srv, a.base, gA, inviteOwner(t, srv, a.base))
		if r := other.call("initialize", map[string]any{"protocolVersion": "2025-06-18"}); r.code != http.StatusForbidden {
			t.Fatalf("second owner on the first owner's grant: %d %s", r.code, r.raw)
		}
	})
	t.Run("a non-member is refused by the router before the MCP server runs", func(t *testing.T) {
		stranger := newMCPClient(t, srv, a.base, gA, mintToken(t, srv, "mcp-stranger-"+uuid.NewString()[:8], "webauthn"))
		if r := stranger.call("initialize", map[string]any{"protocolVersion": "2025-06-18"}); r.code != http.StatusForbidden {
			t.Fatalf("non-member: %d %s", r.code, r.raw)
		}
	})
	t.Run("B's ledgers are invisible to a session in A, and cannot be posted to", func(t *testing.T) {
		// A voucher that exists, in B, posted by B's owner by hand.
		hv, hb := do(t, srv, http.MethodPost, bb.base+"/vouchers/payment", voucherBody("2025-05-20", "B's own payment",
			[3]string{bb.ledgers["Office Rent"], "D", "10.00"}, [3]string{bb.ledgers["HDFC Bank"], "C", "10.00"}))
		if hv.Code != http.StatusCreated {
			t.Fatalf("B's human voucher: %d %s", hv.Code, hv.Body.String())
		}
		bVoucher, _ := hb["voucher_id"].(string)
		r := ca.tool("get_vouchers__voucherid", map[string]any{"path_params": map[string]string{"voucherID": bVoucher}})
		if !r.isError() || strings.Contains(r.raw, "B's own payment") {
			t.Fatalf("A's session read B's voucher: %s", r.raw)
		}
		// The control: B's session reads the same id.
		cbc := newMCPClient(t, srv, bb.base, gB, owner)
		cbc.initialize("2025-06-18")
		if r := cbc.tool("get_vouchers__voucherid", map[string]any{"path_params": map[string]string{"voucherID": bVoucher}}); r.isError() || !strings.Contains(r.raw, "B's own payment") {
			t.Fatalf("CONTROL: B's own session cannot read B's voucher, so the refusal above proves nothing: %s", r.raw)
		}
		pv := ca.tool("post_vouchers_payment", paymentArgs(bb, "Office Rent", "HDFC Bank", "700.00"))
		if !pv.isError() || pv.structured()["preview_ref"] != nil {
			t.Fatalf("A's session previewed a payment between B's ledgers: %s", pv.raw)
		}
		if cm := ca.tool(mcp.ToolCommit, map[string]any{"preview": "tool:1"}); !cm.isError() {
			t.Fatalf("a commit of the refused preview: %s", cm.raw)
		}
		pollUntilQuiet(t, srv)
		if na, nb := countVouchers(t, a.companyID), countVouchers(t, bb.companyID); na != 0 || nb != 1 {
			t.Fatalf("vouchers: A %d (want 0), B %d (want only B's own 1)", na, nb)
		}
	})
}

// TestMCPManifestChangeIsRefused: the manifest shown at initialize is pinned;
// a registry that changes under a live session (a deploy, a new route) is
// refused on every later tools/list and tools/call, and nothing is recorded.
// Restoring the registry restores the session — the pin is the manifest's
// content, not a one-way latch.
func TestMCPManifestChangeIsRefused(t *testing.T) {
	srv := newAgentServer(t)
	b := newAgentBooks(t, srv)
	grant := createMCPGrant(t, srv, b.base, []string{"create payment"}, nil)
	c := newMCPClient(t, srv, b.base, grant, ownerToken(t, srv))
	init := c.initialize("2025-06-18")
	pinned, _ := init.result()["_meta"].(map[string]any)["khata/manifest_hash"].(string)
	runID := sessionRunID(t, c)
	if r := c.tool("get_ledgers", map[string]any{}); r.isError() {
		t.Fatalf("before the change: %s", r.raw)
	}
	before := runDecisions(t, b.companyID, runID)

	// A deploy that ADDS nothing dangerous and removes one report still
	// changes what the client was shown.
	t.Cleanup(func() { srv.ConfigureMCP(server.MCPOptions{}) })
	srv.ConfigureMCP(server.MCPOptions{Specs: func() []actions.ActionSpec {
		var out []actions.ActionSpec
		for _, s := range actions.Specs {
			if s.ID != "get.trial-balance" {
				out = append(out, s)
			}
		}
		return out
	}})
	for _, r := range []mcpReply{c.call("tools/list", nil), c.tool("get_ledgers", map[string]any{}),
		c.tool(mcp.ToolCommit, map[string]any{"preview": "tool:0"})} {
		if r.errCode() != mcp.CodeManifestChanged {
			t.Fatalf("after the change: %d %s", r.code, r.raw)
		}
		data, _ := r.body["error"].(map[string]any)["data"].(map[string]any)
		if data["pinned"] != pinned || data["current"] == pinned {
			t.Fatalf("the refusal does not name both hashes: %v", data)
		}
	}
	if n := runDecisions(t, b.companyID, runID); n != before {
		t.Fatalf("refused calls recorded %d decision(s)", n-before)
	}
	// A new session sees (and pins) the new manifest.
	c2 := newMCPClient(t, srv, b.base, grant, ownerToken(t, srv))
	h2, _ := c2.initialize("2025-06-18").result()["_meta"].(map[string]any)["khata/manifest_hash"].(string)
	if h2 == pinned {
		t.Fatal("the changed registry produced the same manifest hash")
	}
	if _, listed := c2.toolNames()["get_trial-balance"]; listed {
		t.Fatal("the removed tool is still listed")
	}
	srv.ConfigureMCP(server.MCPOptions{})
	if r := c.tool("get_ledgers", map[string]any{}); r.isError() || r.errCode() != 0 {
		t.Fatalf("after restoring the registry: %s", r.raw)
	}
}

// TestMCPDelegationCeiling: the grant's paise ceiling and key list are the
// run's delegation. At L3 with a CLEAN autonomy table the ceiling still parks
// an over-ceiling commit; an approver's edit above it is rolled back by the
// commit hook; a key not delegated is not a tool; a structural key cannot be
// granted at all.
func TestMCPDelegationCeiling(t *testing.T) {
	srv := newAgentServer(t)
	b := newAgentBooks(t, srv)
	setAutonomy(t, b.companyID, "create payment", policy.L3)
	grant := createMCPGrant(t, srv, b.base, []string{"create payment"}, int64p(30000_00)) // Rs 30,000

	t.Run("a structural key is refused at the grant", func(t *testing.T) {
		for _, key := range []string{"delete voucher", "alter security", "create agent-mcp", "no such key"} {
			rec, _ := do(t, srv, http.MethodPost, b.base+"/agent/mcp/grants", map[string]any{"client_name": "x",
				"delegate": []string{key}, "expires_at": "2099-01-01T00:00:00Z"})
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("grant of %q: %d %s", key, rec.Code, rec.Body.String())
			}
		}
	})
	t.Run("the grant's rows cannot be widened after the fact", func(t *testing.T) {
		conn, err := pgx.Connect(context.Background(), adminDSN)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close(context.Background())
		_, err = conn.Exec(context.Background(), `UPDATE agent_mcp_grant SET max_amount_paise = NULL, acl_keys = acl_keys || '{create receipt}' WHERE id = $1`, grant)
		if err == nil || !strings.Contains(err.Error(), "only revocation may change a grant") {
			t.Fatalf("widening a grant: %v", err)
		}
	})

	c := newMCPClient(t, srv, b.base, grant, ownerToken(t, srv))
	c.initialize("2025-06-18")
	runID := sessionRunID(t, c)
	t.Run("an undelegated write is not a tool", func(t *testing.T) {
		names := c.toolNames()
		if names["post_vouchers_receipt"] != nil || names["post_vouchers_payment"] == nil {
			t.Fatal("the manifest does not follow the delegation")
		}
		if r := c.tool("post_vouchers_receipt", paymentArgs(b, "HDFC Bank", "Acme Retail", "10.00")); r.errCode() != mcp.CodeInvalidParams {
			t.Fatalf("undelegated receipt: %s", r.raw)
		}
	})

	pv := c.tool("post_vouchers_payment", paymentArgs(b, "Sharma Traders", "HDFC Bank", "50000.00"))
	ref, _ := pv.structured()["preview_ref"].(string)
	cm := c.tool(mcp.ToolCommit, map[string]any{"preview": ref})
	gate, _ := cm.structured()["gate"].(map[string]any)
	reasons, _ := json.Marshal(gate["reasons"])
	if cm.structured()["status"] != "pending_approval" ||
		!strings.Contains(string(reasons), "exceeds this run's delegated ceiling of 3000000 paise") {
		t.Fatalf("over-ceiling commit: %s", cm.raw)
	}
	// The approver EDITS the amount to Rs 45,000 — still above the ceiling.
	// The commit hook sums what the handler's own transaction wrote and
	// rolls it back.
	decideApproval(t, srv, b, gate["approval_id"].(string), map[string]any{"decision": "approve",
		"edited_body": voucherBody("2025-05-20", "edited", [3]string{b.ledgers["Sharma Traders"], "D", "45000.00"},
			[3]string{b.ledgers["HDFC Bank"], "C", "45000.00"})})
	pollUntilQuiet(t, srv)
	if n := countVouchers(t, b.companyID); n != 0 {
		t.Fatalf("an approved edit above the grant's ceiling posted %d voucher(s)", n)
	}
	// The refusal must be the CEILING's (403 naming it), not any other
	// failure of the commit: a commit hook that errored for an unrelated
	// reason would also leave 0 vouchers, and did (D126 §8).
	rs := c.tool(mcp.ToolRunStatus, map[string]any{})
	commits, _ := rs.structured()["run"].(map[string]any)["commits"].([]any)
	if len(commits) != 1 {
		t.Fatalf("run_status: %s", rs.raw)
	}
	refused, _ := commits[0].(map[string]any)["commit"].(map[string]any)
	if refused["status"] != float64(http.StatusForbidden) || !strings.Contains(fmt.Sprint(refused["body"]), "exceeds this run's delegated ceiling") {
		t.Fatalf("the over-ceiling edit was refused, but not by the ceiling: %v", refused)
	}

	// Within the ceiling: still parked (born taint), and commits once approved.
	pv = c.tool("post_vouchers_payment", paymentArgs(b, "Sharma Traders", "HDFC Bank", "20000.00"))
	ref, _ = pv.structured()["preview_ref"].(string)
	cm = c.tool(mcp.ToolCommit, map[string]any{"preview": ref})
	gate, _ = cm.structured()["gate"].(map[string]any)
	reasons, _ = json.Marshal(gate["reasons"])
	if cm.structured()["status"] != "pending_approval" || strings.Contains(string(reasons), "ceiling") {
		t.Fatalf("within-ceiling commit: %s", cm.raw)
	}
	decideApproval(t, srv, b, gate["approval_id"].(string), map[string]any{"decision": "approve"})
	pollUntilQuiet(t, srv)
	if n := countVouchers(t, b.companyID); n != 1 {
		t.Fatalf("within the ceiling: %d vouchers, want 1 (preview %s; commit %s; status %s)", n, pv.raw, cm.raw,
			c.tool(mcp.ToolRunStatus, map[string]any{}).raw)
	}
	if st := getRun(t, srv, b, runID)["status"]; st != "parked" {
		t.Fatalf("the session's run should be parked for its next call, got %v", st)
	}
}

// TestMCPRevokingAGrantStopsItsRuns: revocation closes the grant's sessions and
// cancels their runs, so an approval a human grants AFTER the revocation posts
// nothing, and the transport refuses the grant.
func TestMCPRevokingAGrantStopsItsRuns(t *testing.T) {
	srv := newAgentServer(t)
	b := newAgentBooks(t, srv)
	grant := createMCPGrant(t, srv, b.base, []string{"create payment"}, nil)
	c := newMCPClient(t, srv, b.base, grant, ownerToken(t, srv))
	c.initialize("2025-06-18")
	runID := sessionRunID(t, c)
	pv := c.tool("post_vouchers_payment", paymentArgs(b, "Office Rent", "HDFC Bank", "900.00"))
	cm := c.tool(mcp.ToolCommit, map[string]any{"preview": pv.structured()["preview_ref"]})
	approvalID, _ := cm.structured()["gate"].(map[string]any)["approval_id"].(string)
	if approvalID == "" {
		t.Fatalf("no approval: %s", cm.raw)
	}
	rec, _ := do(t, srv, http.MethodPost, b.base+"/agent/mcp/grants/"+grant+"/revoke", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body.String())
	}
	decideApproval(t, srv, b, approvalID, map[string]any{"decision": "approve"})
	pollUntilQuiet(t, srv)
	if n := countVouchers(t, b.companyID); n != 0 {
		t.Fatalf("an approval decided after revocation posted %d voucher(s)", n)
	}
	if st := getRun(t, srv, b, runID)["status"]; st != "cancelled" {
		t.Fatalf("run status after revocation = %v, want cancelled", st)
	}
	if r := c.call("ping", nil); r.code != http.StatusForbidden || !strings.Contains(r.raw, "revoked") {
		t.Fatalf("transport after revocation: %d %s", r.code, r.raw)
	}
	_, list := do(t, srv, http.MethodGet, b.base+"/agent/mcp/grants", nil)
	gs, _ := list["grants"].([]any)
	if len(gs) != 1 || gs[0].(map[string]any)["revoked_at"] == nil {
		t.Fatalf("grants after revocation: %v", list)
	}
}
