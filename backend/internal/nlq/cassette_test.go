package nlq

// cassette_test.go — the per-feature replay half of the Tier-0 gate for
// nlq.translate (tier0_test.go's header: "a new cassette feature owes a
// per-feature replay test that starts from the input").
//
// Each case starts from a tau nl_query UTTERANCE, lets the production
// Translator build its prompt (static catalogue + entity guide, dynamic
// clock + CATALOGUE HINTS, the question), encode it for the provider, and
// send it. The fake provider re-derives the cassette key FROM THE WIRE BODY
// and answers with the recorded emit_query input. So a prompt edit — a
// catalogue entry, a rule, a hint format — moves the key and fails HERE with
// the regeneration command; Tier 0 alone replays by the stored key and could
// not see it.
//
// ⛔ THE RESPONSES ARE SCRIPTED, NOT A MODEL'S (SeedNote). No provider key
// exists (HANDOFF-AGENTIC-WAVE-1 §6.1). What these prove: the v4 prompt
// reaches the wire with the catalogue in it, a recorded answer replays by a
// key derived from that wire, and the decoded DSL compiles. What they do NOT
// prove: that any model would answer this way — that is W1b's bake-off.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/query"
	"github.com/khata/cored/internal/trajectory"
)

const (
	nlqCassetteRoot       = "testdata"
	nlqCassetteRecordedAt = "2026-09-28T00:00:00Z"
	nlqRegenerate         = "TRAJECTORY_RECORD=1 go test ./internal/nlq/ -run TestTranslatorReplaysItsCassettes"
)

// tauToday is the "today" every tau nl_query task is written against.
var tauToday = time.Date(2025, 6, 30, 9, 0, 0, 0, time.UTC)

type nlqCase struct {
	caseID    string // the tau task id it is taken from
	utterance string
	// hint is text the hints on the wire must contain — the proof the
	// catalogue reached the provider for THIS question.
	hint string
	// answer is the scripted emit_query input (recorded into the cassette).
	answer     string
	wantEntity query.Entity
}

var nlqCases = []nlqCase{
	{caseID: "nlq-bank-000-hinglish", utterance: "HDFC Bank mein abhi kitna balance hai?",
		hint:       "(report trial-balance) -> entities: ledger_balances",
		answer:     `{"entity":"ledger_balances","metrics":[{"func":"sum","column":"closing_net","as":"balance"}],"dimensions":["ledger_name"],"filters":[{"column":"ledger_name","op":"eq","values":["HDFC Bank"]}],"date_range":null,"order_by":[],"limit":100}`,
		wantEntity: query.EntityLedgerBalances},
	{caseID: "nlq-creditor-002-en", utterance: "How much do we still owe Kumar Packaging?",
		hint:       "1. Bills Payable (report bills-payable) -> entities: outstandings",
		answer:     `{"entity":"outstandings","metrics":[{"func":"sum","column":"pending","as":"pending"}],"dimensions":["ledger_name"],"filters":[{"column":"nature","op":"eq","values":["LIABILITY"]},{"column":"ledger_name","op":"eq","values":["Kumar Packaging"]}],"date_range":null,"order_by":[],"limit":100}`,
		wantEntity: query.EntityOutstandings},
	{caseID: "nlq-debtor-005-hinglish", utterance: "Sharma Textiles se kitna lena baaki hai abhi?",
		hint:       "(report outstandings) -> entities: outstandings",
		answer:     `{"entity":"outstandings","metrics":[{"func":"sum","column":"pending","as":"pending"}],"dimensions":["ledger_name"],"filters":[{"column":"nature","op":"eq","values":["ASSET"]},{"column":"ledger_name","op":"eq","values":["Sharma Textiles"]}],"date_range":null,"order_by":[],"limit":100}`,
		wantEntity: query.EntityOutstandings},
	{caseID: "nlq-sales-003-en", utterance: "What are our total sales (net of GST) booked in Sales @18% so far?",
		hint:       "(report profit-and-loss) -> entities: postings",
		answer:     `{"entity":"postings","metrics":[{"func":"sum","column":"natural_net","as":"net_sales"}],"dimensions":["ledger_name"],"filters":[{"column":"ledger_name","op":"eq","values":["Sales @18%"]}],"date_range":null,"order_by":[],"limit":100}`,
		wantEntity: query.EntityPostings},
}

// wireKey is the cassette key: a hash of everything the model reads, taken
// from the ENCODED request (system text, then each message), so it is the
// production encoder's output that is keyed — not a test-side re-rendering
// that could agree with itself while the wire drifted.
func wireKey(body []byte) (key, system string, err error) {
	var req struct {
		System   string `json:"system"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return "", "", fmt.Errorf("fake provider: not a Messages request: %w", err)
	}
	var b strings.Builder
	b.WriteString(req.System)
	for _, m := range req.Messages {
		b.WriteString("\x00")
		b.WriteString(m.Role)
		b.WriteString("\x00")
		b.WriteString(m.Content)
	}
	return trajectory.PromptHash(b.String()), req.System, nil
}

// cassetteProvider is an http.RoundTripper speaking the Anthropic Messages
// shape: in record mode it answers with the case's scripted input and appends
// the entry; in replay mode it answers ONLY from the cassette.
type cassetteProvider struct {
	t        *testing.T
	c        nlqCase
	cassette *trajectory.Cassette
	rec      *trajectory.Recording
	mu       sync.Mutex
	calls    int
	system   string
}

func (p *cassetteProvider) RoundTrip(r *http.Request) (*http.Response, error) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	key, system, err := wireKey(raw)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.calls++
	p.system = system
	p.mu.Unlock()

	var input string
	if p.cassette != nil {
		e, err := p.cassette.Lookup(key)
		if err != nil {
			return nil, fmt.Errorf("%w\n\nThe prompt changed, so its key changed. Re-record:\n  %s", err, nlqRegenerate)
		}
		input = string(e.ResponseBody)
	} else {
		input = p.c.answer
		if err := p.rec.Append(trajectory.Entry{
			Kind: trajectory.EntryModelCall,
			Span: trajectory.Record{
				TraceID: fmt.Sprintf("%032x", 0xB3), SpanID: fmt.Sprintf("%016x", 1),
				Name: "chat " + translatorFeature, Kind: "chat", Status: "OK",
				Attributes: map[string]any{
					trajectory.AttrFeature:            translatorFeature,
					trajectory.AttrSemconvVersion:     trajectory.SemconvVersion,
					trajectory.AttrGenAIOperationName: trajectory.OpChat,
					trajectory.AttrPromptHash:         key,
				},
			},
			RequestBody:  json.RawMessage(`{"model":"fake-nlq","tool_choice":"emit_query","prompt_version":"` + translatorPromptVersion + `"}`),
			ResponseBody: json.RawMessage(input),
		}); err != nil {
			return nil, err
		}
	}
	body := toolUse(input)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    r,
	}, nil
}

func openNLQCassette(t *testing.T, c nlqCase) *cassetteProvider {
	t.Helper()
	path, err := trajectory.CassettePath(nlqCassetteRoot, translatorFeature, c.caseID)
	if err != nil {
		t.Fatal(err)
	}
	p := &cassetteProvider{t: t, c: c}
	if trajectory.RecordingEnabled() {
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.Remove(path)
		rec, err := trajectory.NewRecording(path, trajectory.Header{
			CaseID: c.caseID, Note: trajectory.SeedNote("go test ./internal/nlq/ -run TestTranslatorReplaysItsCassettes"),
			RecordedAt: nlqCassetteRecordedAt, SemconvVersion: trajectory.SemconvVersion,
			Provider: "fake", ModelID: "fake-nlq",
		}, trajectory.IdentityRedactor) // synthetic tau parties; nothing to strip, stated
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = rec.Close() })
		p.rec = rec
		return p
	}
	cs, err := trajectory.OpenCassette(path)
	if err != nil {
		t.Fatalf("open cassette %s: %v\n\nGenerate it:\n  %s", path, err, nlqRegenerate)
	}
	p.cassette = cs
	return p
}

// TestTranslatorReplaysItsCassettes is the gate.
func TestTranslatorReplaysItsCassettes(t *testing.T) {
	for _, c := range nlqCases {
		t.Run(c.caseID, func(t *testing.T) {
			p := openNLQCassette(t, c)
			tr := &Translator{
				APIKey: "cassette", BaseURL: "http://cassette.invalid",
				Client: &http.Client{Transport: p, Timeout: 5 * time.Second},
				Now:    func() time.Time { return tauToday },
			}
			q, err := tr.ToQuery(t.Context(), uuid.New(), c.utterance)
			if err != nil {
				t.Fatalf("ToQuery(%q): %v", c.utterance, err)
			}
			if p.calls != 1 {
				t.Fatalf("provider calls = %d, want exactly 1 (no repair on a recorded answer)", p.calls)
			}
			// The catalogue reached the wire, for THIS question.
			for _, want := range []string{"ENTITY GUIDE", "CATALOGUE HINTS for this question", c.hint, "TODAY IS 2025-06-30."} {
				if !strings.Contains(p.system, want) {
					t.Errorf("the encoded system prompt lacks %q", want)
				}
			}
			if q.Entity != c.wantEntity {
				t.Errorf("replayed entity %q, want %q", q.Entity, c.wantEntity)
			}
			if _, _, err := query.Compile(q); err != nil {
				t.Errorf("replayed DSL does not compile: %v", err)
			}
			if p.cassette != nil && p.cassette.Replayed() != p.cassette.Len() {
				t.Errorf("replayed %d of %d recorded interactions", p.cassette.Replayed(), p.cassette.Len())
			}
		})
	}
}
