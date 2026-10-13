package llm

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The hermetic harness. Shape copied from internal/nlq/translate_test.go:45-81:
// an httptest server that RECORDS what the client sent and replays a canned
// body, so every assertion below has the real bytes as its oracle rather than a
// mock's expectation.
//
// ⛔ NO TEST IN THIS FILE OR ITS SIBLINGS TOUCHES THE NETWORK OR NEEDS AN API
// KEY. The only DB-gated test in the package is budget_db_test.go, which
// skips when Postgres is unavailable.

// capture records every request the client made.
type capture struct {
	mu       sync.Mutex
	calls    int
	method   string
	path     string
	headers  http.Header
	bodies   [][]byte
	respFunc func(n int) (int, string) // n is the 0-based call index
}

func (c *capture) Calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *capture) Body(i int) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if i >= len(c.bodies) {
		return nil
	}
	return c.bodies[i]
}

func (c *capture) LastBody() []byte { return c.Body(c.Calls() - 1) }

// fakeAPI stands up the server and returns a Client pointed at it.
func fakeAPI(t *testing.T, cap *capture, cfg Config) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.mu.Lock()
		n := cap.calls
		cap.calls++
		cap.method = r.Method
		cap.path = r.URL.Path
		cap.headers = r.Header.Clone()
		body, _ := io.ReadAll(r.Body)
		cap.bodies = append(cap.bodies, body)
		fn := cap.respFunc
		cap.mu.Unlock()

		status, payload := http.StatusOK, `{}`
		if fn != nil {
			status, payload = fn(n)
		}
		// A provider request id, so the fingerprint assertions have something
		// real to read rather than an empty string that would pass vacuously.
		w.Header().Set("x-request-id", "req-abc123")
		w.Header().Set("request-id", "req-abc123")
		w.WriteHeader(status)
		io.WriteString(w, payload)
	}))
	t.Cleanup(srv.Close)

	if cfg.Provider == "" {
		cfg.Provider = ProviderFireworks
	}
	if cfg.Model == "" {
		cfg.Model = "accounts/fireworks/models/glm-5p3"
	}
	if cfg.APIKey == "" {
		cfg.APIKey = "dummy-key"
	}
	cfg.BaseURL = srv.URL
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// chatOK builds an OpenAI-compatible 200 body with one tool call.
func chatOK(toolName, argsJSON string) string {
	tc := ""
	if toolName != "" {
		b, _ := json.Marshal(argsJSON)
		tc = `,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"` + toolName +
			`","arguments":` + string(b) + `}}]`
	}
	return `{"id":"resp_1","model":"served-model-x","system_fingerprint":"fp_9",` +
		`"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":""` + tc + `}}],` +
		`"usage":{"prompt_tokens":120,"completion_tokens":30,"prompt_tokens_details":{"cached_tokens":100}}}`
}

// chatText builds an OpenAI-compatible 200 body whose message is plain text.
func chatText(text string) string {
	b, _ := json.Marshal(text)
	return `{"id":"resp_1","model":"served-model-x","system_fingerprint":"fp_9",` +
		`"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":` + string(b) + `}}],` +
		`"usage":{"prompt_tokens":10,"completion_tokens":5}}`
}

// alwaysOK is the common respFunc.
func alwaysOK(body string) func(int) (int, string) {
	return func(int) (int, string) { return http.StatusOK, body }
}

// fixedClock returns a Now func frozen at t. Every test that cares about the
// clock injects one — see Config.Now's comment for why this is not decoration.
func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

// baseRequest is a minimally valid Request: a static system turn, a dynamic
// user turn, a Feature and a PromptVersion.
func baseRequest() *Request {
	return &Request{
		TenantID:      uuid.MustParse("11111111-2222-3333-4444-555555555555"),
		Feature:       "test.feature",
		PromptVersion: "v1",
		Static: []Message{
			{Role: RoleSystem, Content: "You emit typed queries. Never compute money."},
		},
		Dynamic: []Message{
			{Role: RoleUser, Content: "sales by month"},
		},
		MaxTokens: 256,
	}
}
