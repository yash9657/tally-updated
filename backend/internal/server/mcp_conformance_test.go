package server_test

// mcp_conformance_test.go — the W6 protocol gate (D126 §6): the lifecycle,
// capabilities, tools/list, tools/call, the error codes and the Streamable
// HTTP transport rules, for protocol versions 2025-11-25 and 2025-06-18, over
// the real router with the grant's real transport token (0106).
//
// Every assertion names the spec rule it checks. "Batching" is checked as
// REFUSED: both versions removed JSON-RPC batches, so an array is one Invalid
// Request with id null and nothing in it runs (the run records no decision).

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/mcp"
)

// runDecisions counts the recorded decide:k steps of a session's run — the
// oracle for "nothing was recorded".
func runDecisions(t *testing.T, companyID, runID string) int {
	t.Helper()
	c, err := pgx.Connect(context.Background(), adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	var n int
	if err := c.QueryRow(context.Background(),
		`SELECT count(*) FROM agent_run_step WHERE company_id = $1 AND run_id = $2 AND step_name LIKE 'decide:%'`,
		companyID, runID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestMCPProtocolConformance(t *testing.T) {
	srv := newAgentServer(t)
	b := newAgentBooks(t, srv)
	grant := createMCPGrant(t, srv, b.base, []string{"create payment"}, nil)
	c := newMCPClient(t, srv, b.base, grant, grantToken(t, grant))

	// ---- lifecycle ---------------------------------------------------------
	t.Run("initialize negotiates each supported version", func(t *testing.T) {
		for _, v := range mcp.SupportedVersions {
			r := c.initialize(v)
			res := r.result()
			if res["protocolVersion"] != v {
				t.Fatalf("asked %s, got %v", v, res["protocolVersion"])
			}
			caps, _ := res["capabilities"].(map[string]any)
			tools, _ := caps["tools"].(map[string]any)
			if tools == nil || tools["listChanged"] != false {
				t.Fatalf("capabilities.tools.listChanged must be declared false: %v", caps)
			}
			if len(caps) != 1 {
				t.Fatalf("only the tools capability is offered, got %v", caps)
			}
			info, _ := res["serverInfo"].(map[string]any)
			if info["name"] != "khata-cored" || info["version"] == "" {
				t.Fatalf("serverInfo %v", info)
			}
			if s, _ := res["instructions"].(string); !strings.Contains(s, "approval inbox") {
				t.Fatalf("instructions do not say commits wait for the approval inbox: %q", s)
			}
		}
	})
	t.Run("an unsupported version is answered with the latest", func(t *testing.T) {
		r := c.initialize("2024-11-05")
		if r.result()["protocolVersion"] != mcp.LatestVersion {
			t.Fatalf("got %v, want %s", r.result()["protocolVersion"], mcp.LatestVersion)
		}
	})
	t.Run("initialize sent as a notification is refused", func(t *testing.T) {
		r := c.send(map[string]any{"jsonrpc": "2.0", "method": "initialize", "params": map[string]any{"protocolVersion": "2025-06-18"}})
		if r.code != http.StatusBadRequest || r.errCode() != mcp.CodeInvalidRequest {
			t.Fatalf("%d %s", r.code, r.raw)
		}
	})
	t.Run("initialize without params is invalid params", func(t *testing.T) {
		saved := c.session
		c.session = ""
		r := c.call("initialize", nil)
		c.session = saved
		if r.errCode() != mcp.CodeInvalidParams || r.header.Get(mcp.HeaderSession) != "" {
			t.Fatalf("%d %s", r.code, r.raw)
		}
	})

	c.initialize("2025-06-18")
	runID := func() string {
		r := c.call("tools/call", map[string]any{"name": mcp.ToolRunStatus, "arguments": map[string]any{}})
		run, _ := r.structured()["run"].(map[string]any)
		id, _ := run["run_id"].(string)
		if id == "" {
			t.Fatalf("run_status gave no run id: %s", r.raw)
		}
		return id
	}()

	// ---- transport -----------------------------------------------------------
	t.Run("a request without a session is 400", func(t *testing.T) {
		r := c.rawPost([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`), map[string]string{mcp.HeaderSession: ""})
		if r.code != http.StatusBadRequest {
			t.Fatalf("%d %s", r.code, r.raw)
		}
	})
	t.Run("an unknown session is 404 (re-initialize)", func(t *testing.T) {
		r := c.rawPost([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`), map[string]string{mcp.HeaderSession: "5e1f5d5a-0000-4000-8000-000000000000"})
		if r.code != http.StatusNotFound {
			t.Fatalf("%d %s", r.code, r.raw)
		}
	})
	t.Run("a protocol header that differs from the negotiated version is 400", func(t *testing.T) {
		r := c.rawPost([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`), map[string]string{mcp.HeaderProtocol: "2025-11-25"})
		if r.code != http.StatusBadRequest {
			t.Fatalf("%d %s", r.code, r.raw)
		}
	})
	t.Run("the protocol header may be omitted", func(t *testing.T) {
		r := c.rawPost([]byte(`{"jsonrpc":"2.0","id":7,"method":"ping"}`), map[string]string{mcp.HeaderProtocol: ""})
		if r.code != http.StatusOK || r.result() == nil {
			t.Fatalf("%d %s", r.code, r.raw)
		}
	})
	t.Run("notifications and responses are 202 with no body", func(t *testing.T) {
		for _, body := range []string{
			`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
			`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":3}}`,
			`{"jsonrpc":"2.0","id":99,"result":{}}`,
		} {
			r := c.rawPost([]byte(body), nil)
			if r.code != http.StatusAccepted || r.raw != "" {
				t.Fatalf("%s: %d %q", body, r.code, r.raw)
			}
		}
	})
	t.Run("a non-JSON content type is 415, an Accept without JSON is 406", func(t *testing.T) {
		if r := c.rawPost([]byte(`{}`), map[string]string{"Content-Type": "text/plain"}); r.code != http.StatusUnsupportedMediaType {
			t.Fatalf("content-type: %d", r.code)
		}
		if r := c.rawPost([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`), map[string]string{"Accept": "text/event-stream"}); r.code != http.StatusNotAcceptable {
			t.Fatalf("accept: %d", r.code)
		}
	})
	t.Run("a foreign Origin is 403 (DNS-rebinding rule)", func(t *testing.T) {
		r := c.rawPost([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`), map[string]string{"Origin": "https://evil.example"})
		if r.code != http.StatusForbidden {
			t.Fatalf("%d %s", r.code, r.raw)
		}
	})
	t.Run("GET (a server stream) is 405, which the transport allows", func(t *testing.T) {
		rec, _ := doRaw(t, srv, http.MethodGet, b.base+"/agent/mcp/"+grant, grantToken(t, grant), nil)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("GET = %d, want 405", rec.Code)
		}
	})

	// ---- JSON-RPC ------------------------------------------------------------
	before := runDecisions(t, b.companyID, runID)
	t.Run("a batch is one Invalid Request, id null, and nothing runs", func(t *testing.T) {
		batch := `[{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_ledgers","arguments":{}}},` +
			`{"jsonrpc":"2.0","id":2,"method":"ping"}]`
		r := c.rawPost([]byte(batch), nil)
		if r.code != http.StatusBadRequest || r.errCode() != mcp.CodeInvalidRequest || r.body["id"] != nil {
			t.Fatalf("%d %s", r.code, r.raw)
		}
		if n := runDecisions(t, b.companyID, runID); n != before {
			t.Fatalf("a refused batch recorded %d decision(s)", n-before)
		}
	})
	t.Run("malformed JSON is a parse error, id null", func(t *testing.T) {
		r := c.rawPost([]byte(`{"jsonrpc":"2.0",`), nil)
		if r.code != http.StatusBadRequest || r.errCode() != mcp.CodeParseError || r.body["id"] != nil {
			t.Fatalf("%d %s", r.code, r.raw)
		}
	})
	t.Run("ids must be a string or an integer, never null or a fraction", func(t *testing.T) {
		for _, body := range []string{
			`{"jsonrpc":"2.0","id":null,"method":"ping"}`,
			`{"jsonrpc":"2.0","id":1.5,"method":"ping"}`,
			`{"jsonrpc":"2.0","id":{},"method":"ping"}`,
			`{"jsonrpc":"1.0","id":1,"method":"ping"}`,
			`"ping"`,
		} {
			if r := c.rawPost([]byte(body), nil); r.code != http.StatusBadRequest || r.errCode() != mcp.CodeInvalidRequest {
				t.Fatalf("%s: %d %s", body, r.code, r.raw)
			}
		}
		if r := c.rawPost([]byte(`{"jsonrpc":"2.0","id":"abc","method":"ping"}`), nil); r.body["id"] != "abc" || r.result() == nil {
			t.Fatalf("a string id must be echoed: %s", r.raw)
		}
	})
	t.Run("ping answers an empty result", func(t *testing.T) {
		r := c.call("ping", nil)
		if r.code != http.StatusOK || r.result() == nil || len(r.result()) != 0 {
			t.Fatalf("%d %s", r.code, r.raw)
		}
	})
	t.Run("an unoffered method is Method Not Found", func(t *testing.T) {
		for _, m := range []string{"resources/list", "prompts/list", "sampling/createMessage", "no/such"} {
			if r := c.call(m, nil); r.errCode() != mcp.CodeMethodNotFound {
				t.Fatalf("%s: %s", m, r.raw)
			}
		}
	})

	// ---- tools ---------------------------------------------------------------
	t.Run("tools/list is the registry filtered by the grant, with schemas and risk", func(t *testing.T) {
		r := c.call("tools/list", map[string]any{})
		tools, _ := r.result()["tools"].([]any)
		if len(tools) < 40 {
			t.Fatalf("only %d tools listed", len(tools))
		}
		byName := map[string]map[string]any{}
		for _, x := range tools {
			m := x.(map[string]any)
			if _, ok := m["inputSchema"].(map[string]any); !ok {
				t.Fatalf("tool %v has no inputSchema object", m["name"])
			}
			if s, _ := m["inputSchema"].(map[string]any)["type"].(string); s != "object" {
				t.Fatalf("tool %v inputSchema.type = %q", m["name"], s)
			}
			byName[m["name"].(string)] = m
		}
		pay := byName["post_vouchers_payment"]
		if pay == nil {
			t.Fatal("the delegated payment preview is not listed")
		}
		meta, _ := pay["_meta"].(map[string]any)
		if meta["khata/risk"] != "money" || meta["khata/acl_key"] != "create payment" || meta["khata/mode"] != "preview" {
			t.Fatalf("payment tool _meta %v", meta)
		}
		for _, want := range []string{"commit_preview", "run_status", "search_actions", "query_books", "get_ledgers", "get_trial-balance"} {
			if byName[want] == nil {
				t.Fatalf("%s is not listed", want)
			}
		}
		for _, absent := range []string{"post_vouchers_sales", "post_ledgers", "delete_vouchers__voucherid", "get_backup",
			"get_agent_approvals", "post_agent_approvals__approvalid_decision", "put_security-levels__levelid", "post_agent_mcp_grants"} {
			if byName[absent] != nil {
				t.Fatalf("%s is listed, but is not delegated or is structurally forbidden", absent)
			}
		}
		if h, _ := r.result()["_meta"].(map[string]any)["khata/manifest_hash"].(string); len(h) != 64 {
			t.Fatalf("tools/list carries no manifest hash: %v", r.result()["_meta"])
		}
	})
	t.Run("an issued-by-nobody cursor is invalid params", func(t *testing.T) {
		if r := c.call("tools/list", map[string]any{"cursor": "page2"}); r.errCode() != mcp.CodeInvalidParams {
			t.Fatalf("%s", r.raw)
		}
	})
	t.Run("an unknown tool is a protocol error (-32602)", func(t *testing.T) {
		for _, name := range []string{"no_such_tool", "post_vouchers_sales", "delete_vouchers__voucherid"} {
			if r := c.tool(name, map[string]any{}); r.errCode() != mcp.CodeInvalidParams || !strings.Contains(r.raw, "Unknown tool") {
				t.Fatalf("%s: %s", name, r.raw)
			}
		}
		if r := c.call("tools/call", map[string]any{}); r.errCode() != mcp.CodeInvalidParams {
			t.Fatalf("no name: %s", r.raw)
		}
	})
	t.Run("invalid arguments are a tool result with isError (2025-11-25)", func(t *testing.T) {
		before := runDecisions(t, b.companyID, runID)
		for _, args := range []any{map[string]any{"surprise": 1}, []any{1}, "x"} {
			r := c.tool("get_ledgers", args)
			if r.code != http.StatusOK || !r.isError() || r.structured()["status"] != "invalid_arguments" {
				t.Fatalf("%v: %s", args, r.raw)
			}
		}
		if n := runDecisions(t, b.companyID, runID); n != before {
			t.Fatalf("an argument refusal recorded %d decision(s)", n-before)
		}
	})
	t.Run("a read returns the books, as text and as structured content", func(t *testing.T) {
		r := c.tool("get_ledgers", map[string]any{})
		if r.isError() || r.structured()["status"] != "ok" {
			t.Fatalf("%s", r.raw)
		}
		content, _ := r.result()["content"].([]any)
		if len(content) != 1 || content[0].(map[string]any)["type"] != "text" ||
			!strings.Contains(content[0].(map[string]any)["text"].(string), "HDFC Bank") {
			t.Fatalf("the text block does not carry the result: %v", content)
		}
		if !strings.Contains(r.raw, b.ledgers["HDFC Bank"]) {
			t.Fatalf("the read did not return the ledgers: %s", r.raw)
		}
	})

	// ---- teardown --------------------------------------------------------------
	t.Run("DELETE ends the session; its id is then unknown", func(t *testing.T) {
		rec := deleteSession(t, srv, c)
		if rec != http.StatusNoContent {
			t.Fatalf("DELETE = %d", rec)
		}
		if r := c.call("ping", nil); r.code != http.StatusNotFound {
			t.Fatalf("after DELETE: %d %s", r.code, r.raw)
		}
		if st := getRun(t, srv, b, runID)["status"]; st != "completed" {
			t.Fatalf("closing a session waiting for its client should finish the run, got %v", st)
		}
	})
}
