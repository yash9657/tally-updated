package narrate_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/insights"
	"github.com/khata/cored/internal/insights/narrate"
	"github.com/khata/cored/internal/llm"
	"github.com/khata/cored/internal/trajectory"
)

// items is a digest of three findings. The party name carries digits on
// purpose ("Shop 21 Traders"): a name is a {{party}} placeholder, so its digits
// reach the text only by substitution and must pass the rendered check.
func items() []insights.DigestItem {
	return []insights.DigestItem{
		{Type: insights.TypeCreditLimitBreach, Fingerprint: "a", Payload: map[string]string{
			"party": "Shop 21 Traders", "voucher_no": "SAL-007", "credit_limit": "10000.00",
			"balance": "69384.00", "excess": "59384.00"}},
		{Type: insights.TypeSupplierPriceCreep, Fingerprint: "b", Payload: map[string]string{
			"party": "Bharat Metals", "item": "Widget", "first_rate": "50.00", "last_rate": "55.00",
			"drift_pct": "10.00", "qty_window": "500", "extra_cost": "2500.00"}},
		{Type: insights.TypeTDSNotDeposited, Fingerprint: "c", Payload: map[string]string{
			"period": "2026-07", "due_date": "2026-08-07", "days_late": "53", "deductions": "1",
			"unpaid_tds": "1000.00"}},
	}
}

var good = narrate.Answer{Lines: []narrate.Line{
	{Ref: "i1", Text: "{{party}} is Rs {{excess}} over its Rs {{credit_limit}} limit after {{voucher_no}}."},
	{Ref: "i2", Text: "{{party}} has pushed {{item}} up {{drift_pct}}% — Rs {{extra_cost}} a year at this volume."},
	{Ref: "i3", Text: "TDS of Rs {{unpaid_tds}} from {{period}} is {{days_late}} days late; deposit it now."},
}}

func TestEveryTemplatePassesItsOwnProvenanceCheck(t *testing.T) {
	for _, td := range insights.Types {
		payload := map[string]string{}
		for i, f := range td.Fields {
			switch f.Unit {
			case insights.UnitText:
				payload[f.Key] = fmt.Sprintf("Name %d", 40+i)
			case insights.UnitDate:
				payload[f.Key] = "2026-09-1" + fmt.Sprint(i%10)
			default:
				payload[f.Key] = fmt.Sprintf("%d.25", 1000+i)
			}
		}
		it := insights.DigestItem{Type: td.Code, Payload: payload}
		if err := narrate.CheckRaw(td.Template, it); err != nil {
			t.Errorf("%s template fails the raw check: %v", td.Code, err)
		}
		rendered := insights.RenderTemplate(td.Template, payload)
		if err := narrate.CheckRendered(rendered, payload); err != nil {
			t.Errorf("%s template fails the rendered check: %v (%s)", td.Code, err, rendered)
		}
		for _, k := range insights.Placeholders(td.Template) {
			if _, ok := td.Field(k); !ok {
				t.Errorf("%s template names undeclared key %s", td.Code, k)
			}
		}
	}
}

func TestAGoodNarrationIsAcceptedAndRenderedFromThePayload(t *testing.T) {
	texts, err := narrate.Accept(good, items())
	if err != nil {
		t.Fatalf("a clean narration was refused: %v", err)
	}
	want := "Shop 21 Traders is Rs 59384.00 over its Rs 10000.00 limit after SAL-007."
	if texts[0] != want {
		t.Errorf("rendered %q, want %q", texts[0], want)
	}
}

// TestAdversarialNarrationsAreRejected is the W4 gate for the narrator: each
// case is a way a model could smuggle a number that is not in payload_json
// into the owner's morning — and each must be refused.
func TestAdversarialNarrationsAreRejected(t *testing.T) {
	cases := []struct{ name, line string }{
		{"a literal amount", "{{party}} owes Rs 59,384 over the limit."},
		{"a literal that equals a payload value", "{{party}} is Rs 59384.00 over."},
		{"a rounded restatement", "{{party}} is about 60k over."},
		{"a number in words", "{{party}} is nearly sixty thousand over."},
		{"a magnitude word on a placeholder", "{{party}} is {{excess}} lakh over."},
		{"crore", "{{party}} owes {{balance}} crore."},
		{"Hinglish number word", "{{party}} ka das hazaar baaki hai."},
		{"Devanagari digits", "{{party}} पर ५९३८४ बकाया है।"},
		{"Devanagari number word", "{{party}} पर दस हज़ार बकाया है।"},
		{"full-width digits", "{{party}} owes Rs ５９３８４."},
		{"superscript", "{{party}} is {{excess}}² over."},
		{"a vulgar fraction", "{{party}} is ½ over the limit."},
		{"arithmetic by the model", "{{party}} owes double its limit."},
		{"a percentage on a money key", "{{party}} is {{excess}}% over."},
		{"a bare percentage", "{{party}} is 500% over."},
		{"an invented key", "{{party}} has a {{risk_score}} risk."},
		{"a key from another finding", "{{party}} raised {{drift_pct}}."},
		{"broken braces", "{{party}} owes {{excess}."},
		{"a stray brace", "{{party}} owes { excess }."},
		{"a date written out", "{{party}} crossed it on 28 September."},
		{"a year", "{{party}} crossed it in 2026."},
	}
	for _, c := range cases {
		ans := narrate.Answer{Lines: append([]narrate.Line{{Ref: "i1", Text: c.line}}, good.Lines[1:]...)}
		if _, err := narrate.Accept(ans, items()); err == nil {
			t.Errorf("%s: ACCEPTED %q", c.name, c.line)
		}
	}
	// Structure: wrong count, wrong order.
	if _, err := narrate.Accept(narrate.Answer{Lines: good.Lines[:2]}, items()); err == nil {
		t.Error("two lines for three findings accepted")
	}
	swapped := narrate.Answer{Lines: []narrate.Line{good.Lines[1], good.Lines[0], good.Lines[2]}}
	if _, err := narrate.Accept(swapped, items()); err == nil {
		t.Error("lines out of order accepted")
	}
	t.Logf("%d adversarial narrations, every one refused", len(cases)+2)
}

// TestTheRenderedCheckIsASecondWall: a text built WITHOUT placeholders (as a
// buggy template or a bypassed raw check would produce) is refused by the
// rendered check alone when it carries a figure the payload does not.
func TestTheRenderedCheckIsASecondWall(t *testing.T) {
	p := items()[0].Payload
	if err := narrate.CheckRendered("Shop 21 Traders is Rs 59,384.00 over.", p); err != nil {
		t.Errorf("an Indian-grouped payload figure was refused: %v", err)
	}
	for _, bad := range []string{"Shop 21 Traders is Rs 59,385.00 over.", "Shop 22 Traders", "over by 6 %", "५९३८४"} {
		if err := narrate.CheckRendered(bad, p); err == nil {
			t.Errorf("rendered check accepted %q", bad)
		}
	}
}

// ---------------------------------------------------------------------------
// Through the real llm.Client, against a fake provider
// ---------------------------------------------------------------------------

type fakeProvider struct {
	mu     sync.Mutex
	answer func() string
	bodies []string
}

func (f *fakeProvider) RoundTrip(r *http.Request) (*http.Response, error) {
	b, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.bodies = append(f.bodies, string(b))
	n := len(f.bodies)
	f.mu.Unlock()
	resp := map[string]any{
		"id": fmt.Sprintf("fake-%d", n), "model": "fake-narrator", "system_fingerprint": "fake",
		"choices": []any{map[string]any{"finish_reason": "tool_calls",
			"message": map[string]any{"content": "", "tool_calls": []any{map[string]any{
				"id": "call_1", "type": "function",
				"function": map[string]any{"name": narrate.Tool, "arguments": f.answer()}}}}}},
		"usage": map[string]any{"prompt_tokens": 200, "completion_tokens": 60},
	}
	out, _ := json.Marshal(resp)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(bytes.NewReader(out)), Request: r}, nil
}

func client(t *testing.T, f *fakeProvider) *llm.Client {
	t.Helper()
	c, err := llm.New(llm.Config{Provider: llm.ProviderVLLM, Model: "fake-narrator",
		BaseURL: "http://fake-model.invalid", HTTP: &http.Client{Transport: f}, MaxRepairs: -1})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAModelThatWritesANumberFallsBackToTheTemplate(t *testing.T) {
	bad := narrate.Answer{Lines: append([]narrate.Line{{Ref: "i1", Text: "{{party}} owes Rs 59,384."}}, good.Lines[1:]...)}
	raw, _ := json.Marshal(bad)
	f := &fakeProvider{answer: func() string { return string(raw) }}
	n, err := narrate.Model{Client: client(t, f)}.Narrate(context.Background(), uuid.New(), items())
	if err != nil {
		t.Fatal(err)
	}
	if n.Narrator != "template" || !strings.Contains(n.Rejected, "literal numeral") {
		t.Fatalf("narrator %s rejected %q", n.Narrator, n.Rejected)
	}
	td, _ := insights.ByCode(insights.TypeCreditLimitBreach)
	if n.Texts[0] != insights.RenderTemplate(td.Template, items()[0].Payload) {
		t.Errorf("fallback text %q is not the template", n.Texts[0])
	}
	// ⛔ No value reached the provider: not a figure, not a name. (Values
	// shorter than four characters, "1", are skipped: they occur in any JSON.)
	for _, it := range items() {
		for k, v := range it.Payload {
			if len(v) >= 4 && strings.Contains(f.bodies[0], v) {
				t.Errorf("payload value %s=%q was sent to the model", k, v)
			}
		}
	}
}

func TestNoModelMeansTheTemplateWithNoReason(t *testing.T) {
	n, err := narrate.Model{}.Narrate(context.Background(), uuid.New(), items())
	if err != nil || n.Narrator != "template" || n.Rejected != "" || len(n.Texts) != 3 {
		t.Fatalf("%+v %v", n, err)
	}
}

// ---------------------------------------------------------------------------
// The cassette, and its per-feature replay FROM THE INPUT
// ---------------------------------------------------------------------------

const cassetteRoot = "testdata/cassettes"

// requestKey is the replay key, derived from the request BuildRequest makes
// for the items — the input, not the recording.
func requestKey(req *llm.Request) string {
	h := sha256.New()
	for _, m := range append(append([]llm.Message{}, req.Static...), req.Dynamic...) {
		fmt.Fprintf(h, "%s\x00%s\x00", m.Role, m.Content)
	}
	for _, tl := range req.Tools {
		var buf bytes.Buffer
		_ = json.Compact(&buf, tl.Parameters)
		fmt.Fprintf(h, "tool\x00%s\x00%s\x00", tl.Name, buf.String())
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TestNarrateCassette replays testdata/cassettes/insight.narrate/three-findings
// (recorded from the scripted fake with TRAJECTORY_RECORD=1) through the real
// client and the real provenance gate. The key is re-derived from the input,
// so a prompt or schema change misses loudly and must be re-recorded.
func TestNarrateCassette(t *testing.T) {
	cid := uuid.MustParse("018f0000-0000-7000-8000-00000000c0de")
	req := narrate.BuildRequest(cid, items())
	key := requestKey(req)
	path, err := trajectory.CassettePath(cassetteRoot, narrate.Feature, "three-findings")
	if err != nil {
		t.Fatal(err)
	}
	goodRaw, _ := json.Marshal(good)
	if trajectory.RecordingEnabled() {
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.Remove(path)
		rec, err := trajectory.NewRecording(path, trajectory.Header{CaseID: "three-findings",
			Note:       trajectory.SeedNote("go test ./internal/insights/narrate/ -run TestNarrateCassette"),
			RecordedAt: "2026-09-28T00:00:00Z", SemconvVersion: trajectory.SemconvVersion,
			Provider: "fake", ModelID: "fake-narrator"}, trajectory.IdentityRedactor) // keys only, no tenant data
		if err != nil {
			t.Fatal(err)
		}
		if err := rec.Append(trajectory.Entry{Kind: trajectory.EntryModelCall,
			Span: trajectory.Record{TraceID: fmt.Sprintf("%032x", 0xA4), SpanID: fmt.Sprintf("%016x", 1),
				Name: "chat " + narrate.Feature, Kind: "chat", Status: "OK",
				Attributes: map[string]any{trajectory.AttrFeature: narrate.Feature,
					trajectory.AttrSemconvVersion:     trajectory.SemconvVersion,
					trajectory.AttrGenAIOperationName: trajectory.OpChat, trajectory.AttrPromptHash: key}},
			RequestBody: json.RawMessage(`{"model":"fake-narrator","tool_choice":"` + narrate.Tool + `"}`), ResponseBody: goodRaw}); err != nil {
			t.Fatal(err)
		}
		if err := rec.Close(); err != nil {
			t.Fatal(err)
		}
	}
	c, err := trajectory.OpenCassette(path)
	if err != nil {
		t.Fatalf("open %s: %v\n\nGenerate it:\n  TRAJECTORY_RECORD=1 go test ./internal/insights/narrate/ -run TestNarrateCassette", path, err)
	}
	e, err := c.Lookup(key)
	if err != nil {
		t.Fatalf("%v\n\nThe narration request changed, so its key changed. Re-record:\n  TRAJECTORY_RECORD=1 go test ./internal/insights/narrate/ -run TestNarrateCassette", err)
	}
	f := &fakeProvider{answer: func() string { return string(e.ResponseBody) }}
	n, err := narrate.Model{Client: client(t, f)}.Narrate(context.Background(), cid, items())
	if err != nil {
		t.Fatal(err)
	}
	if n.Narrator != "model" || n.Rejected != "" || n.PromptVersion != narrate.PromptVersion {
		t.Fatalf("replayed narration: %+v", n)
	}
	if len(f.bodies) != 1 || !strings.Contains(f.bodies[0], "keys only; no values") {
		t.Errorf("the client sent %d requests; the replay must be the request BuildRequest makes", len(f.bodies))
	}
	t.Logf("replayed %s: %q", key[:12], n.Texts[1])
}
