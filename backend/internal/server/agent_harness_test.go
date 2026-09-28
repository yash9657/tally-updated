package server_test

// agent_harness_test.go is the shared harness for the W2 agent tests: a
// deterministic fake model behind the REAL internal/llm client (vLLM adapter,
// real encode/decode), a cassette recorder/replayer keyed on the full rendered
// conversation, and helpers for companies, runs and approvals.
//
// ⛔ NO NETWORK, EVER. The model's transport is an http.RoundTripper that
// answers from a cassette (replay, the default) or from an in-repo script
// (record, TRAJECTORY_RECORD=1, and the injection suite). A cassette miss is a
// test failure with the regeneration command — never a fallback to anything.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/agent"
	"github.com/khata/cored/internal/llm"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/policy"
	"github.com/khata/cored/internal/server"
	"github.com/khata/cored/internal/trajectory"
)

// agentCassetteRoot is this package's testdata; the Tier-0 census walks every
// testdata/cassettes in the module and finds these without being told.
const agentCassetteRoot = "testdata"

const agentSeedRecordedAt = "2026-09-28T00:00:00Z"

// newAgentServer is newServer with the HANDOFF §7.2 rule applied: CORED_SKIP_DB
// is the only skip; a database that is unreachable with it unset FAILS.
func newAgentServer(t *testing.T) *server.Server {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped (CORED_SKIP_DB set)")
	}
	c, err := pgx.Connect(context.Background(), adminDSN)
	if err != nil {
		t.Fatalf("admin postgres unavailable and CORED_SKIP_DB is unset: %v", err)
	}
	_ = c.Close(context.Background())
	return newServer(t)
}

// ---------------------------------------------------------------------------
// The fake model
// ---------------------------------------------------------------------------

// script is a deterministic in-repo model: given the conversation so far it
// returns the next decision. turn is the number of RESULT turns seen.
type script func(t *testing.T, turn int, results []json.RawMessage) agent.ModelDecision

type chatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatBody struct {
	Messages []chatMsg `json:"messages"`
	Tools    []struct {
		Function struct {
			Name       string          `json:"name"`
			Parameters json.RawMessage `json:"parameters"`
		} `json:"function"`
	} `json:"tools"`
}

var uuidRE = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// normaliser replaces every UUID in a conversation with a stable placeholder in
// order of first appearance. Ledger, voucher and run ids are minted fresh on
// every run, so a key over the raw text would change every run; a key over the
// normalised text is a function of the conversation's SHAPE and CONTENT only.
type normaliser struct {
	toPH   map[string]string
	fromPH map[string]string
}

func newNormaliser() *normaliser {
	return &normaliser{toPH: map[string]string{}, fromPH: map[string]string{}}
}

func (n *normaliser) norm(s string) string {
	return uuidRE.ReplaceAllStringFunc(s, func(u string) string {
		if ph, ok := n.toPH[u]; ok {
			return ph
		}
		ph := fmt.Sprintf("uuid#%d", len(n.toPH)+1)
		n.toPH[u], n.fromPH[ph] = ph, u
		return ph
	})
}

var phRE = regexp.MustCompile(`uuid#[0-9]+`)

func (n *normaliser) denorm(s string) (string, error) {
	var missing string
	out := phRE.ReplaceAllStringFunc(s, func(ph string) string {
		if u, ok := n.fromPH[ph]; ok {
			return u
		}
		missing = ph
		return ph
	})
	if missing != "" {
		return "", fmt.Errorf("recorded response names %s, which this conversation never mentioned", missing)
	}
	return out, nil
}

// conversationKey renders the wire request exactly as agent.RequestHash renders
// an llm.Request — role, NUL, content, NUL per message, then each tool — over
// the NORMALISED text, and hashes it with trajectory.PromptHash (SHA-256, the
// cassette primitive).
func conversationKey(b chatBody, n *normaliser) string {
	var sb strings.Builder
	for _, m := range b.Messages {
		fmt.Fprintf(&sb, "%s\x00%s\x00", m.Role, n.norm(m.Content))
	}
	for _, tl := range b.Tools {
		var buf bytes.Buffer
		_ = json.Compact(&buf, tl.Function.Parameters)
		fmt.Fprintf(&sb, "tool\x00%s\x00%s\x00", tl.Function.Name, buf.String())
	}
	return trajectory.PromptHash(sb.String())
}

// results extracts every "RESULT OF tool:N" turn's JSON, in order.
func results(b chatBody) []json.RawMessage {
	var out []json.RawMessage
	for _, m := range b.Messages {
		if m.Role != "user" || !strings.HasPrefix(m.Content, "RESULT OF tool:") {
			continue
		}
		if i := strings.IndexByte(m.Content, '\n'); i > 0 {
			out = append(out, json.RawMessage(m.Content[i+1:]))
		}
	}
	return out
}

type fakeModel struct {
	t        *testing.T
	script   script
	cassette *trajectory.Cassette
	rec      *trajectory.Recording
	feature  string
	mu       sync.Mutex
	calls    int
	keys     []string
	requests []chatBody
}

func (f *fakeModel) RoundTrip(r *http.Request) (*http.Response, error) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	var body chatBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("fake model: request is not a chat completion: %w", err)
	}
	n := newNormaliser()
	key := conversationKey(body, n)
	f.mu.Lock()
	f.calls++
	idx := f.calls
	f.keys = append(f.keys, key)
	f.requests = append(f.requests, body)
	f.mu.Unlock()

	var decisionJSON string
	switch {
	case f.cassette != nil:
		e, err := f.cassette.Lookup(key)
		if err != nil {
			return nil, fmt.Errorf("%w\n\nThe conversation changed, so its key changed. Re-record:\n  TRAJECTORY_RECORD=1 go test ./internal/server/ -run TestAgentCassette", err)
		}
		decisionJSON, err = n.denorm(string(e.ResponseBody))
		if err != nil {
			return nil, err
		}
	default:
		d := f.script(f.t, len(results(body)), results(body))
		b, _ := json.Marshal(d)
		decisionJSON = string(b)
		if f.rec != nil {
			normResp := n.norm(decisionJSON)
			// A scripted response naming an id the conversation never showed
			// is a script bug; catching it here keeps it out of the cassette.
			if _, err := n.denorm(normResp); err != nil {
				return nil, err
			}
			if err := f.rec.Append(trajectory.Entry{
				Kind: trajectory.EntryModelCall,
				Span: trajectory.Record{
					TraceID: fmt.Sprintf("%032x", 0xA2), SpanID: fmt.Sprintf("%016x", idx),
					Name: "chat " + f.feature, Kind: "chat", Status: "OK",
					Attributes: map[string]any{
						trajectory.AttrFeature:            f.feature,
						trajectory.AttrSemconvVersion:     trajectory.SemconvVersion,
						trajectory.AttrGenAIOperationName: trajectory.OpChat,
						trajectory.AttrPromptHash:         key,
					},
				},
				RequestBody:  json.RawMessage(`{"model":"fake-agent","tool_choice":"decide"}`),
				ResponseBody: json.RawMessage(normResp),
			}); err != nil {
				return nil, err
			}
		}
	}
	resp := map[string]any{
		"id": fmt.Sprintf("fake-%d", idx), "model": "fake-agent", "system_fingerprint": "fake",
		"choices": []any{map[string]any{
			"finish_reason": "tool_calls",
			"message": map[string]any{"content": "", "tool_calls": []any{map[string]any{
				"id": fmt.Sprintf("call_%d", idx), "type": "function",
				"function": map[string]any{"name": agent.DecideTool, "arguments": decisionJSON}}}},
		}},
		"usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 20},
	}
	out, _ := json.Marshal(resp)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(bytes.NewReader(out)), Request: r}, nil
}

func (f *fakeModel) client(t *testing.T) *llm.Client {
	t.Helper()
	c, err := llm.New(llm.Config{Provider: llm.ProviderVLLM, Model: "fake-agent",
		BaseURL: "http://fake-model.invalid", HTTP: &http.Client{Transport: f}, MaxRepairs: -1})
	if err != nil {
		t.Fatalf("llm.New: %v", err)
	}
	return c
}

// scriptedModel answers from the script, recording nothing (the injection suite).
func scriptedModel(t *testing.T, s script) *fakeModel {
	return &fakeModel{t: t, script: s, feature: agent.Feature}
}

// cassetteModel replays testdata/cassettes/agent.decide/<caseID>.jsonl, or
// records it from s when TRAJECTORY_RECORD is set.
func cassetteModel(t *testing.T, caseID string, s script) *fakeModel {
	t.Helper()
	path, err := trajectory.CassettePath(agentCassetteRoot, agent.Feature, caseID)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeModel{t: t, script: s, feature: agent.Feature}
	if trajectory.RecordingEnabled() {
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.Remove(path)
		rec, err := trajectory.NewRecording(path, trajectory.Header{
			CaseID: caseID, Note: trajectory.SeedNote("go test ./internal/server/ -run TestAgentCassette"),
			RecordedAt: agentSeedRecordedAt, SemconvVersion: trajectory.SemconvVersion,
			Provider: "fake", ModelID: "fake-agent",
		}, trajectory.IdentityRedactor) // synthetic books, synthetic parties: nothing to strip, stated
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = rec.Close() })
		f.rec = rec
		return f
	}
	c, err := trajectory.OpenCassette(path)
	if err != nil {
		t.Fatalf("open cassette %s: %v\n\nGenerate it:\n  TRAJECTORY_RECORD=1 go test ./internal/server/ -run TestAgentCassette", path, err)
	}
	f.cassette = c
	return f
}

// ---------------------------------------------------------------------------
// Books and runs
// ---------------------------------------------------------------------------

// agentBooks is a fresh company with the ledgers every agent scenario uses.
type agentBooks struct {
	companyID string
	base      string
	ledgers   map[string]string // name -> id
}

func newAgentBooks(t *testing.T, srv *server.Server) agentBooks {
	t.Helper()
	cid, base := newCompany(t, srv)
	b := agentBooks{companyID: cid, base: base, ledgers: map[string]string{}}
	for _, l := range []struct{ name, group string }{
		{"HDFC Bank", "Bank Accounts"},
		{"Office Rent", "Indirect Expenses"},
		{"Sharma Traders", "Sundry Creditors"},
		{"Acme Retail", "Sundry Debtors"},
		{"Domestic Sales", "Sales Accounts"},
		{"Fraudster Pvt Ltd", "Sundry Creditors"},
	} {
		b.ledgers[l.name] = createLedger(t, srv, base, l.name, l.group)
	}
	return b
}

// ledgerIDFromResults finds a ledger's id in the most recent actions.read of
// get.ledgers — the model's only way to learn one.
func ledgerIDFromResults(t *testing.T, results []json.RawMessage, name string) string {
	t.Helper()
	for i := len(results) - 1; i >= 0; i-- {
		var r struct {
			Tool struct {
				Result struct {
					Ledgers []struct {
						ID   string `json:"id"`
						Name string `json:"name"`
					} `json:"ledgers"`
				} `json:"result"`
			} `json:"tool"`
		}
		if json.Unmarshal(results[i], &r) != nil {
			continue
		}
		for _, l := range r.Tool.Result.Ledgers {
			if l.Name == name {
				return l.ID
			}
		}
	}
	t.Fatalf("script: ledger %q is not in any actions.read result the model has seen", name)
	return ""
}

func decision(tool string, args any) agent.ModelDecision {
	b, _ := json.Marshal(args)
	return agent.ModelDecision{Tool: tool, Args: b}
}

func finish(final string) agent.ModelDecision {
	return agent.ModelDecision{Tool: agent.ToolFinish, Args: json.RawMessage(`{}`), Final: final}
}

func voucherBody(date, narration string, lines ...[3]string) map[string]any {
	var ps []map[string]any
	for _, l := range lines {
		ps = append(ps, map[string]any{"ledger_id": l[0], "dr_cr": l[1], "amount": l[2]})
	}
	return map[string]any{"voucher_date": date, "narration": narration, "created_by": "agent", "postings": ps}
}

// startRun starts a run through the HTTP API as the owner.
func startRun(t *testing.T, srv *server.Server, b agentBooks, body map[string]any) string {
	t.Helper()
	rec, resp := do(t, srv, http.MethodPost, b.base+"/agent/runs", body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("start run status %d, want 202 (%s)", rec.Code, rec.Body.String())
	}
	run, _ := resp["run"].(map[string]any)
	id, _ := run["id"].(string)
	if id == "" {
		t.Fatalf("start run returned no id: %s", rec.Body.String())
	}
	return id
}

func pollUntilQuiet(t *testing.T, srv *server.Server) {
	t.Helper()
	for i := 0; i < 20; i++ {
		n, err := srv.AgentPollOnce(context.Background())
		if err != nil {
			t.Fatalf("poll: %v", err)
		}
		if n == 0 {
			return
		}
	}
	t.Fatal("the poller never went quiet in 20 ticks")
}

func getRun(t *testing.T, srv *server.Server, b agentBooks, runID string) map[string]any {
	t.Helper()
	rec, resp := do(t, srv, http.MethodGet, b.base+"/agent/runs/"+runID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get run %d: %s", rec.Code, rec.Body.String())
	}
	run, _ := resp["run"].(map[string]any)
	return run
}

func pendingApprovals(t *testing.T, srv *server.Server, b agentBooks, runID string) []map[string]any {
	t.Helper()
	rec, resp := do(t, srv, http.MethodGet, b.base+"/agent/approvals?state=pending&run_id="+runID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list approvals %d: %s", rec.Code, rec.Body.String())
	}
	var out []map[string]any
	for _, a := range resp["approvals"].([]any) {
		out = append(out, a.(map[string]any))
	}
	return out
}

// countVouchers counts the company's logical vouchers straight from the table
// — an oracle independent of every agent code path.
func countVouchers(t *testing.T, companyID string) int {
	t.Helper()
	c, err := pgx.Connect(context.Background(), adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	var n int
	if err := c.QueryRow(context.Background(), `SELECT count(*) FROM voucher_head WHERE company_id = $1`, companyID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func setAutonomy(t *testing.T, companyID string, key string, lvl policy.Level) {
	t.Helper()
	pool, err := db.New(context.Background(), appDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	cid := uuid.MustParse(companyID)
	var serr error
	err = pool.RunInTx(db.WithCompany(context.Background(), cid), func(ctx context.Context, tx pgx.Tx) error {
		serr = policy.SetLevel(ctx, tx, cid, uuid.Nil, key, nil, lvl, uuid.New())
		return serr
	})
	if err != nil {
		t.Fatalf("set autonomy: %v", err)
	}
}

// ownerSubject is the subject inside the per-server owner token.
func ownerSubject(t *testing.T, srv *server.Server) string {
	t.Helper()
	parts := strings.Split(ownerToken(t, srv), ".")
	if len(parts) != 3 {
		t.Fatal("owner token is not a JWS")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(raw, &c); err != nil || c.Sub == "" {
		t.Fatalf("owner token has no subject: %v", err)
	}
	return c.Sub
}

// fixedClock is a settable engine clock.
type fixedClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fixedClock) Now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fixedClock) Set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }
