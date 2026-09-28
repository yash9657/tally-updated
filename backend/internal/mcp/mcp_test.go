package mcp

// mcp_test.go: the pure half of the W6 gates — no database, no router. The
// DB-backed conformance and security gates are internal/server's mcp_*_test.go.

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/actions"
	"github.com/khata/cored/internal/agent"
	"github.com/khata/cored/internal/policy"
)

func TestParseMessageClassifiesAndRefuses(t *testing.T) {
	for _, c := range []struct {
		body string
		code int // 0 = accepted
		kind string
	}{
		{`{"jsonrpc":"2.0","id":1,"method":"ping"}`, 0, "request"},
		{`{"jsonrpc":"2.0","id":"a-1","method":"tools/list","params":{}}`, 0, "request"},
		{`{"jsonrpc":"2.0","method":"notifications/initialized"}`, 0, "notification"},
		{`{"jsonrpc":"2.0","id":4,"result":{}}`, 0, "response"},
		{`{"jsonrpc":"2.0","id":4,"error":{"code":-1,"message":"x"}}`, 0, "response"},
		{`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`, CodeInvalidRequest, ""},
		{`[]`, CodeInvalidRequest, ""},
		{`[{"jsonrpc":"2.0",`, CodeParseError, ""},
		{`{"jsonrpc":"2.0","id":1,"method":`, CodeParseError, ""},
		{``, CodeInvalidRequest, ""},
		{`42`, CodeInvalidRequest, ""},
		{`{"jsonrpc":"2.0","id":null,"method":"ping"}`, CodeInvalidRequest, ""},
		{`{"jsonrpc":"2.0","id":2.5,"method":"ping"}`, CodeInvalidRequest, ""},
		{`{"jsonrpc":"2.0","id":true,"method":"ping"}`, CodeInvalidRequest, ""},
		{`{"jsonrpc":"1.0","id":1,"method":"ping"}`, CodeInvalidRequest, ""},
		{`{"id":1,"method":"ping"}`, CodeInvalidRequest, ""},
		{`{"jsonrpc":"2.0","id":1}`, CodeInvalidRequest, ""},
	} {
		m, bad := parseMessage([]byte(c.body))
		if c.code != 0 {
			if bad == nil || bad.Error == nil || bad.Error.Code != c.code {
				t.Errorf("%s: want error %d, got %+v", c.body, c.code, bad)
			}
			continue
		}
		if bad != nil {
			t.Errorf("%s: refused %+v", c.body, bad.Error)
			continue
		}
		got := map[bool]string{m.isRequest(): "request", m.isNotification(): "notification", m.isResponse(): "response"}[true]
		if got != c.kind {
			t.Errorf("%s: classified %q, want %q", c.body, got, c.kind)
		}
	}
}

// TestToolNamesAreUniqueAndValid: the spec-id → tool-name mapping is injective
// over the WHOLE registry (not just what one grant sees) and every name is one
// the strictest client grammar accepts.
func TestToolNamesAreUniqueAndValid(t *testing.T) {
	re := regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	seen := map[string]string{}
	for _, name := range []string{ToolSearchActions, ToolQueryBooks, ToolGetDraft, ToolCommit, ToolRunStatus} {
		seen[name] = "meta"
	}
	for _, s := range actions.Specs {
		n := ToolName(s.ID)
		if !re.MatchString(n) {
			t.Errorf("%s -> %q is not a valid tool name", s.ID, n)
		}
		if prev, dup := seen[n]; dup {
			t.Errorf("%s and %s both map to %q", s.ID, prev, n)
		}
		seen[n] = s.ID
	}
	if len(seen) < 200 {
		t.Fatalf("only %d names checked; the registry walk is vacuous", len(seen))
	}
}

func grantPrincipal(keys []string, expires time.Time) policy.Principal {
	return policy.Principal{AgentID: agent.ExternalAgentPrefix + uuid.NewString(), OnBehalfOf: uuid.New(), CompanyID: uuid.New(),
		Delegation: policy.Delegation{ACLKeys: keys, ExpiresAt: expires}}
}

// TestManifestIsTheRegistryFilteredByPolicy: what tools/list shows is exactly
// the policy's answer over the registry — reads always, previews only for
// delegated AND previewable writes, nothing structural, ever.
func TestManifestIsTheRegistryFilteredByPolicy(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	p := grantPrincipal([]string{"create payment", "create master"}, now.Add(time.Hour))
	m := BuildManifest(actions.Specs, p, now)

	reads, previews := 0, 0
	for _, tl := range m.Tools {
		b := m.bindings[tl.Name]
		var schema map[string]any
		if err := json.Unmarshal(tl.InputSchema, &schema); err != nil || schema["type"] != "object" {
			t.Fatalf("%s: inputSchema is not an object schema: %s", tl.Name, tl.InputSchema)
		}
		switch b.kind {
		case bindRead:
			reads++
			if b.spec.Risk != actions.RiskRead || b.spec.Method != "GET" || !tl.Annotations.ReadOnlyHint {
				t.Errorf("%s is bound as a read but is %s %s", tl.Name, b.spec.Method, b.spec.Risk)
			}
		case bindPreview:
			previews++
			if !agent.Previewable(b.spec.ID) || !p.Delegation.Allows(b.spec.ACLKey, now) {
				t.Errorf("%s is a preview tool but is not previewable+delegated", tl.Name)
			}
		}
		if b.kind == bindRead || b.kind == bindPreview {
			if err := policy.Structural(b.spec); err != nil {
				t.Errorf("%s is structurally forbidden and listed: %v", tl.Name, err)
			}
			if tl.Meta[metaRisk] != string(b.spec.Risk) || tl.Meta[metaACLKey] != b.spec.ACLKey {
				t.Errorf("%s _meta %v does not carry its risk and key", tl.Name, tl.Meta)
			}
		}
	}
	// Every read spec the policy allows is listed (the filter is not
	// accidentally narrower than the agent's own).
	wantReads := 0
	for _, s := range actions.Specs {
		if s.Risk == actions.RiskRead && s.Method == "GET" && s.ID != draftSpecID && policy.EffectiveRights(p, s, now).Allow {
			wantReads++
		}
	}
	if reads != wantReads || reads < 60 {
		t.Fatalf("listed %d read tools, the policy allows %d", reads, wantReads)
	}
	// create master is delegated but no master create is previewable yet:
	// only the payment preview appears.
	if previews != 1 || m.bindings["post_vouchers_payment"].kind != bindPreview || m.bindings["post_ledgers"].kind != "" {
		t.Fatalf("previews = %d; payment %v; ledgers %v", previews, m.bindings["post_vouchers_payment"], m.bindings["post_ledgers"])
	}
	for _, meta := range []string{ToolCommit, ToolRunStatus, ToolSearchActions, ToolQueryBooks, ToolGetDraft} {
		if _, ok := m.bindings[meta]; !ok {
			t.Errorf("%s missing", meta)
		}
	}

	// A read-only grant has no commit tool at all.
	ro := BuildManifest(actions.Specs, grantPrincipal(nil, now.Add(time.Hour)), now)
	if _, ok := ro.bindings[ToolCommit]; ok {
		t.Fatal("a grant with no delegated write lists commit_preview")
	}
}

// TestManifestHashPinsContent: the hash is a function of the content — stable
// across builds, different when the delegation, the expiry state or the
// registry differs.
func TestManifestHashPinsContent(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	p := grantPrincipal([]string{"create payment"}, now.Add(time.Hour))
	h := BuildManifest(actions.Specs, p, now).Hash
	if len(h) != 64 {
		t.Fatalf("hash %q", h)
	}
	p2 := p
	p2.AgentID, p2.OnBehalfOf, p2.CompanyID = "mcp:other", uuid.New(), uuid.New()
	if BuildManifest(actions.Specs, p2, now).Hash != h {
		t.Fatal("the hash depends on who, not on what is listed")
	}
	if BuildManifest(actions.Specs, grantPrincipal([]string{"create payment", "create receipt"}, now.Add(time.Hour)), now).Hash == h {
		t.Fatal("a wider delegation produced the same hash")
	}
	if BuildManifest(actions.Specs, p, now.Add(2*time.Hour)).Hash == h {
		t.Fatal("an expired delegation produced the same hash")
	}
	var fewer []actions.ActionSpec
	for _, s := range actions.Specs {
		if s.ID != "get.ledgers" {
			fewer = append(fewer, s)
		}
	}
	if BuildManifest(fewer, p, now).Hash == h {
		t.Fatal("a registry without get.ledgers produced the same hash")
	}
	// A spec the grant can never see is not content of ITS manifest.
	if policy.Structural(actions.Specs[0]) == nil || BuildManifest(actions.Specs[1:], p, now).Hash != h {
		t.Fatal("removing a structurally forbidden (never listed) spec changed the hash")
	}
	// Order of the registry is not content.
	rev := append([]actions.ActionSpec{}, actions.Specs...)
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	if BuildManifest(rev, p, now).Hash != h {
		t.Fatal("reordering the registry changed the hash")
	}
}

// TestMCPReachesNoHandlerExceptThroughTheAgent pins, from the source, what the
// import set cannot: this package never calls a Dispatch method, never builds
// an agent.Request or agent.Caller, never names agent.Dispatcher, and never
// builds an in-process HTTP request. Its only way to act is Engine.Submit.
func TestMCPReachesNoHandlerExceptThroughTheAgent(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	scanned, submits := 0, 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		scanned++
		for _, imp := range file.Imports {
			if p := strings.Trim(imp.Path.Value, `"`); p == "net/http/httptest" {
				t.Errorf("%s imports %s: an in-process request is the dispatcher's job", f, p)
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "Dispatch", "Dispatcher", "Request", "Caller", "ServeHTTP", "Router":
				if x, ok := sel.X.(*ast.Ident); ok && x.Name == "http" && sel.Sel.Name == "Request" {
					return true // *http.Request, the transport's own parameter
				}
				t.Errorf("%s: %s reaches around the agent loop (.%s)", fset.Position(sel.Pos()), exprString(sel), sel.Sel.Name)
			case "Submit":
				submits++
			}
			return true
		})
	}
	if scanned < 4 || submits == 0 {
		t.Fatalf("scanned %d files, %d Submit calls: the scan is vacuous", scanned, submits)
	}
}

func exprString(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return exprString(x.X) + "." + x.Sel.Name
	}
	return "?"
}

func TestGrantUsability(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	g := Grant{ExpiresAt: now.Add(time.Minute)}
	if g.Usable(now) != "" {
		t.Fatal("a live grant is unusable")
	}
	if g.Usable(now.Add(time.Minute)) == "" {
		t.Fatal("a grant is usable at its expiry instant")
	}
	r := now
	g.RevokedAt = &r
	if !strings.Contains(g.Usable(now), "revoked") {
		t.Fatal("a revoked grant is usable")
	}
	if _, err := ValidateKeys([]string{"create payment", "create payment", " "}); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"delete voucher", "alter security", "create agent-mcp", "display agent-mcp", "create nothing"} {
		if _, err := ValidateKeys([]string{k}); err == nil {
			t.Errorf("%q was accepted as a delegable key", k)
		}
	}
	if !strings.HasPrefix(Grant{ID: uuid.New()}.AgentID(), agent.ExternalAgentPrefix) {
		t.Fatal("a grant's agent id is not an external agent id, so its run would call the model")
	}
}
