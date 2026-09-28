package server_test

// mcp_harness_test.go is the shared harness for the W6 MCP tests (D126): an
// MCP client that speaks raw JSON-RPC over the real router — the SAME
// companyAuthMiddleware, the same bearer tokens — so every gate below is
// measured on the wire a real client would use, not on a Go call into the
// package.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/khata/cored/internal/mcp"
	"github.com/khata/cored/internal/server"
)

type mcpClient struct {
	t       *testing.T
	srv     *server.Server
	url     string
	token   string
	session string
	version string
	nextID  int
}

// mcpReply is one HTTP exchange: status, headers and the decoded JSON body.
type mcpReply struct {
	code   int
	header http.Header
	raw    string
	body   map[string]any
}

func (r mcpReply) result() map[string]any {
	m, _ := r.body["result"].(map[string]any)
	return m
}

func (r mcpReply) errCode() int {
	e, _ := r.body["error"].(map[string]any)
	if e == nil {
		return 0
	}
	c, _ := e["code"].(float64)
	return int(c)
}

// structured is a tools/call result's structuredContent.
func (r mcpReply) structured() map[string]any {
	m, _ := r.result()["structuredContent"].(map[string]any)
	return m
}

func (r mcpReply) isError() bool {
	v, _ := r.result()["isError"].(bool)
	return v
}

// createMCPGrant makes a grant through the human route and returns its id.
func createMCPGrant(t *testing.T, srv *server.Server, base string, keys []string, ceilingPaise *int64) string {
	t.Helper()
	body := map[string]any{"client_name": "Claude Desktop (test)", "delegate": keys,
		"expires_at": time.Now().Add(7 * 24 * time.Hour).UTC().Format(time.RFC3339)}
	if ceilingPaise != nil {
		body["max_amount_paise"] = *ceilingPaise
	}
	rec, resp := do(t, srv, http.MethodPost, base+"/agent/mcp/grants", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create MCP grant: %d %s", rec.Code, rec.Body.String())
	}
	g, _ := resp["grant"].(map[string]any)
	id, _ := g["id"].(string)
	if id == "" {
		t.Fatalf("grant has no id: %s", rec.Body.String())
	}
	if p, _ := resp["mcp_path"].(string); p != base+"/agent/mcp/"+id {
		t.Fatalf("mcp_path = %q, want %q", p, base+"/agent/mcp/"+id)
	}
	return id
}

func newMCPClient(t *testing.T, srv *server.Server, base, grantID, token string) *mcpClient {
	return &mcpClient{t: t, srv: srv, url: base + "/agent/mcp/" + grantID, token: token}
}

// rawPost sends body verbatim with the given headers (nil = the defaults a
// conforming client sends).
func (c *mcpClient) rawPost(body []byte, hdr map[string]string) mcpReply {
	c.t.Helper()
	req := httptest.NewRequest(http.MethodPost, c.url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.session != "" {
		req.Header.Set(mcp.HeaderSession, c.session)
	}
	if c.version != "" {
		req.Header.Set(mcp.HeaderProtocol, c.version)
	}
	for k, v := range hdr {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	rec := httptest.NewRecorder()
	c.srv.Router().ServeHTTP(rec, req)
	out := mcpReply{code: rec.Code, header: rec.Header(), raw: rec.Body.String()}
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out.body)
	}
	return out
}

func (c *mcpClient) send(msg map[string]any) mcpReply {
	c.t.Helper()
	b, _ := json.Marshal(msg)
	return c.rawPost(b, nil)
}

func (c *mcpClient) call(method string, params any) mcpReply {
	c.t.Helper()
	c.nextID++
	msg := map[string]any{"jsonrpc": "2.0", "id": c.nextID, "method": method}
	if params != nil {
		msg["params"] = params
	}
	return c.send(msg)
}

func (c *mcpClient) notify(method string) mcpReply {
	c.t.Helper()
	return c.send(map[string]any{"jsonrpc": "2.0", "method": method})
}

// initialize performs the lifecycle handshake and keeps the session id.
func (c *mcpClient) initialize(version string) mcpReply {
	c.t.Helper()
	c.session, c.version = "", ""
	r := c.call("initialize", map[string]any{"protocolVersion": version, "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "khata-test-client", "version": "1.0"}})
	if r.code != http.StatusOK || r.result() == nil {
		c.t.Fatalf("initialize: %d %s", r.code, r.raw)
	}
	c.session = r.header.Get(mcp.HeaderSession)
	if c.session == "" {
		c.t.Fatalf("initialize returned no %s header", mcp.HeaderSession)
	}
	c.version, _ = r.result()["protocolVersion"].(string)
	if n := c.notify("notifications/initialized"); n.code != http.StatusAccepted {
		c.t.Fatalf("notifications/initialized: %d %s", n.code, n.raw)
	}
	return r
}

func (c *mcpClient) tool(name string, args any) mcpReply {
	c.t.Helper()
	return c.call("tools/call", map[string]any{"name": name, "arguments": args})
}

func (c *mcpClient) toolNames() map[string]map[string]any {
	c.t.Helper()
	r := c.call("tools/list", nil)
	if r.code != http.StatusOK || r.result() == nil {
		c.t.Fatalf("tools/list: %d %s", r.code, r.raw)
	}
	out := map[string]map[string]any{}
	for _, x := range r.result()["tools"].([]any) {
		m := x.(map[string]any)
		out[m["name"].(string)] = m
	}
	return out
}

func int64p(v int64) *int64 { return &v }

// deleteSession sends the transport's DELETE for c's session.
func deleteSession(t *testing.T, srv *server.Server, c *mcpClient) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, c.url, nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set(mcp.HeaderSession, c.session)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	return rec.Code
}

// sessionRunID is the agent run behind c's session, read from run_status.
func sessionRunID(t *testing.T, c *mcpClient) string {
	t.Helper()
	r := c.tool(mcp.ToolRunStatus, map[string]any{})
	run, _ := r.structured()["run"].(map[string]any)
	id, _ := run["run_id"].(string)
	if id == "" {
		t.Fatalf("run_status gave no run id: %s", r.raw)
	}
	return id
}
