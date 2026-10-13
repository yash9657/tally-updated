package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// TestComplete_WireShape — path, auth header, tool_choice, response_format.
// ---------------------------------------------------------------------------

func TestComplete_WireShape(t *testing.T) {
	var cap capture
	cap.respFunc = alwaysOK(chatOK("emit_query", `{"entity":"vouchers"}`))
	c := fakeAPI(t, &cap, Config{Provider: ProviderFireworks, Model: "glm-5p3"})

	req := baseRequest()
	req.Tools = []Tool{{
		Name:        "emit_query",
		Description: "Emit the typed query.",
		Parameters:  json.RawMessage(`{ "type" : "object" }`), // deliberately loose whitespace
	}}
	req.ToolChoice = ToolChoice{Mode: ToolChoiceNamed, Name: "emit_query"}

	resp, err := c.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if cap.method != http.MethodPost || cap.path != "/v1/chat/completions" {
		t.Errorf("request = %s %s, want POST /v1/chat/completions", cap.method, cap.path)
	}
	if got := cap.headers.Get("Authorization"); got != "Bearer dummy-key" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer dummy-key")
	}
	if got := cap.headers.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}

	var sent map[string]any
	if err := json.Unmarshal(cap.LastBody(), &sent); err != nil {
		t.Fatalf("sent body not decodable: %v", err)
	}
	tc, ok := sent["tool_choice"].(map[string]any)
	if !ok {
		t.Fatalf("tool_choice = %#v, want a named-function object", sent["tool_choice"])
	}
	if tc["type"] != "function" {
		t.Errorf("tool_choice.type = %v", tc["type"])
	}
	if fn, _ := tc["function"].(map[string]any); fn == nil || fn["name"] != "emit_query" {
		t.Errorf("tool_choice.function = %#v, want name emit_query", tc["function"])
	}
	// The schema was compacted, so the caller's whitespace does not move the
	// bytes a prefix cache matches on.
	if strings.Contains(string(cap.LastBody()), `{ "type" : "object" }`) {
		t.Error("tool parameters were not compacted; a reformatted schema file would cost every tenant a cache miss")
	}

	// The response side.
	if resp.Fingerprint.RequestedModel != "glm-5p3" {
		t.Errorf("RequestedModel = %q", resp.Fingerprint.RequestedModel)
	}
	if resp.Fingerprint.ServedModel != "served-model-x" {
		t.Errorf("ServedModel = %q, want the model the provider SAID it ran", resp.Fingerprint.ServedModel)
	}
	if resp.Fingerprint.SystemFingerprint != "fp_9" || resp.Fingerprint.ResponseID != "resp_1" {
		t.Errorf("fingerprint = %+v", resp.Fingerprint)
	}
	if resp.Fingerprint.RequestID != "req-abc123" {
		t.Errorf("RequestID = %q, want the header value", resp.Fingerprint.RequestID)
	}
	if resp.Usage.PromptTokens != 120 || resp.Usage.CompletionTokens != 30 || resp.Usage.CachedPromptTokens != 100 {
		t.Errorf("usage = %+v", resp.Usage)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "emit_query" {
		t.Errorf("tool calls = %+v", resp.ToolCalls)
	}
}

// TestComplete_ResponseFormatAndGrammarAreRefusedNotDropped is the loud-refusal
// half of the wire contract: a constraint a provider cannot honour comes back as
// an error rather than as an unconstrained generation that looks constrained.
//
// ⚠️ IT IS NOT A CONTROL FOR Capability, AND IT ONCE SAID IT WAS. That claim was
// measured and found false: with `func (c Capability) Has(want Capability) bool
// { return true }` — every provider claims everything — this test still PASSED
// in full. Two reasons, both worth knowing. The anthropic arms went through
// hard-coded refusals that never consulted the mask (now fixed, see
// adapter_anthropic.go). The deepinfra and sarvam arms are stopped by the
// adapter's `grammar` MODE being grammarNone, which is a second lock on the same
// door. The bitmask itself is pinned by
// TestProviderCapabilitiesAreExactlyDeclared, and that is the test to read when
// asking "what does this provider claim?".
func TestComplete_ResponseFormatAndGrammarAreRefusedNotDropped(t *testing.T) {
	schema := &ResponseSchema{Name: "q", Strict: true, Schema: json.RawMessage(`{"type":"object"}`)}

	t.Run("fireworks encodes both", func(t *testing.T) {
		var cap capture
		cap.respFunc = alwaysOK(chatText(`{"ok":true}`))
		c := fakeAPI(t, &cap, Config{Provider: ProviderFireworks})

		req := baseRequest()
		req.Schema = schema
		if _, err := c.Complete(context.Background(), req); err != nil {
			t.Fatalf("json_schema on fireworks: %v", err)
		}
		if !strings.Contains(string(cap.LastBody()), `"json_schema"`) {
			t.Errorf("response_format missing from body: %s", cap.LastBody())
		}

		req2 := baseRequest()
		req2.Grammar = `root ::= "yes" | "no"`
		if _, err := c.Complete(context.Background(), req2); err != nil {
			t.Fatalf("grammar on fireworks: %v", err)
		}
		body := string(cap.LastBody())
		if !strings.Contains(body, `"type":"grammar"`) {
			t.Errorf("fireworks grammar must ride in response_format, got: %s", body)
		}
		if strings.Contains(body, "guided_grammar") {
			t.Error("fireworks must NOT use vLLM's guided_grammar spelling")
		}
	})

	t.Run("vllm uses guided_grammar", func(t *testing.T) {
		var cap capture
		cap.respFunc = alwaysOK(chatText(`{"ok":true}`))
		c := fakeAPI(t, &cap, Config{Provider: ProviderVLLM, Model: "qwen", APIKey: ""})
		req := baseRequest()
		req.Grammar = `root ::= "yes" | "no"`
		if _, err := c.Complete(context.Background(), req); err != nil {
			t.Fatalf("grammar on vllm: %v", err)
		}
		body := string(cap.LastBody())
		if !strings.Contains(body, `"guided_grammar"`) {
			t.Errorf("vLLM grammar must ride in guided_grammar, got: %s", body)
		}
		if strings.Contains(body, `"type":"grammar"`) {
			t.Error("vLLM must NOT use Fireworks' response_format spelling — it would be accepted and ignored")
		}
	})

	// The refusal arms. Three providers cannot do these things, and the point is
	// that the caller finds out LOUDLY instead of receiving an unconstrained
	// generation that looks constrained. (What stops each one differs — see the
	// note on this function — so read these as "the door is shut", not as
	// evidence about which lock is holding it.)
	for _, tc := range []struct {
		provider Provider
		mutate   func(*Request)
		what     string
	}{
		{ProviderDeepInfra, func(r *Request) { r.Grammar = `root ::= "x"` }, "grammar"},
		{ProviderSarvam, func(r *Request) { r.Grammar = `root ::= "x"` }, "grammar"},
		{ProviderAnthropic, func(r *Request) { r.Schema = schema }, "json_schema"},
		{ProviderAnthropic, func(r *Request) { r.Grammar = `root ::= "x"` }, "grammar"},
	} {
		var cap capture
		cap.respFunc = alwaysOK(chatText("{}"))
		c := fakeAPI(t, &cap, Config{Provider: tc.provider, Model: "m"})
		req := baseRequest()
		tc.mutate(req)
		_, err := c.Complete(context.Background(), req)
		if err == nil {
			t.Errorf("%s: %s was accepted; it must be REFUSED, not silently dropped", tc.provider, tc.what)
			continue
		}
		if cap.Calls() != 0 {
			t.Errorf("%s: refusal must happen before the network, but the server saw %d call(s)", tc.provider, cap.Calls())
		}
		if !strings.Contains(err.Error(), string(tc.provider)) {
			t.Errorf("%s: error must name the provider, got %v", tc.provider, err)
		}
	}
}

// ---------------------------------------------------------------------------
// TestComplete_StaticPrefixIsStableAcrossCalls
// ---------------------------------------------------------------------------

// commonPrefixLen is the oracle: how many leading bytes two encoded bodies
// share. A provider's prefix cache matches on exactly this.
func commonPrefixLen(a, b []byte) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// TestComplete_StaticPrefixIsStableAcrossCalls proves the cache contract: two
// calls of the same feature under DIFFERENT CLOCKS must share every byte up to
// the first Dynamic message.
//
// ⛔ MUTATION, AND IT IS RUN AS A SUBTEST RATHER THAN LEFT AS A COMMENT: the
// "date in Static" case below is the same two requests with the per-call date
// moved into the static prefix, and it MUST fail the same check. Without that
// control this test would pass on an implementation that emitted the messages in
// any order at all, because a stable prefix is trivially true of two identical
// bodies — which is why both halves also assert the bodies actually DIFFER.
func TestComplete_StaticPrefixIsStableAcrossCalls(t *testing.T) {
	const systemPrompt = "You emit typed queries. Never compute money. Cite ledger ids."

	encode := func(t *testing.T, static, dynamic []Message) []byte {
		t.Helper()
		var cap capture
		cap.respFunc = alwaysOK(chatText("{}"))
		c := fakeAPI(t, &cap, Config{Now: fixedClock(time.Unix(0, 0))})
		req := baseRequest()
		req.Static = static
		req.Dynamic = dynamic
		if _, err := c.Complete(context.Background(), req); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		return cap.LastBody()
	}

	dayOne := "2026-09-08"
	dayTwo := "2026-11-30"

	t.Run("date in Dynamic (correct)", func(t *testing.T) {
		a := encode(t,
			[]Message{{Role: RoleSystem, Content: systemPrompt}},
			[]Message{{Role: RoleUser, Content: "Today is " + dayOne + ". sales by month"}})
		b := encode(t,
			[]Message{{Role: RoleSystem, Content: systemPrompt}},
			[]Message{{Role: RoleUser, Content: "Today is " + dayTwo + ". sales by month"}})

		if string(a) == string(b) {
			t.Fatal("the two bodies are identical, so this test proves nothing — the clock must change the DYNAMIC half")
		}
		// The whole static prefix, plus the JSON scaffolding around it, must be
		// shared. We locate the first dynamic byte by the user turn's role key.
		dynStart := strings.Index(string(a), `"role":"user"`)
		if dynStart < 0 {
			t.Fatalf("no user turn in the encoded body: %s", a)
		}
		if n := commonPrefixLen(a, b); n < dynStart {
			t.Errorf("common prefix is %d bytes but the dynamic half starts at %d — "+
				"per-call data has moved in front of the cacheable prefix", n, dynStart)
		}
		if !strings.Contains(string(a[:dynStart]), systemPrompt) {
			t.Error("the system prompt is not inside the shared prefix")
		}
	})

	t.Run("date in Static (the mutation — must NOT hold)", func(t *testing.T) {
		a := encode(t,
			[]Message{{Role: RoleSystem, Content: systemPrompt + " Today is " + dayOne + "."}},
			[]Message{{Role: RoleUser, Content: "sales by month"}})
		b := encode(t,
			[]Message{{Role: RoleSystem, Content: systemPrompt + " Today is " + dayTwo + "."}},
			[]Message{{Role: RoleUser, Content: "sales by month"}})

		dynStart := strings.Index(string(a), `"role":"user"`)
		if dynStart < 0 {
			t.Fatalf("no user turn in the encoded body: %s", a)
		}
		if n := commonPrefixLen(a, b); n >= dynStart {
			t.Fatalf("the mutation was not detected: moving the date into Static left the prefix "+
				"stable (%d >= %d), which means the check above is vacuous", n, dynStart)
		}
	})
}

// ---------------------------------------------------------------------------
// TestComplete_NonOK_MapsToUnavailable
// ---------------------------------------------------------------------------

// The status table is internal/nlq/translate_test.go:150's, verbatim, plus 400.
// ⛔ 400 IS IN THE LIST DELIBERATELY: see ErrUpstreamUnavailable's comment on why
// "a 400 means the prompt was wrong" is the refinement that costs a week.
func TestComplete_NonOK_MapsToUnavailable(t *testing.T) {
	for _, status := range []int{
		http.StatusBadRequest,            // 400 our bug or theirs — never the tenant's
		http.StatusUnauthorized,          // 401 bad key
		http.StatusForbidden,             // 403 permission
		http.StatusNotFound,              // 404 retired model id
		http.StatusRequestEntityTooLarge, // 413
		http.StatusTooManyRequests,       // 429
		http.StatusInternalServerError,   // 500
		http.StatusBadGateway,            // 502
		http.StatusServiceUnavailable,    // 503
		529,                              // overloaded_error
	} {
		var cap capture
		cap.respFunc = func(int) (int, string) {
			return status, `{"error":{"type":"x","message":"y"}}`
		}
		c := fakeAPI(t, &cap, Config{})
		_, err := c.Complete(context.Background(), baseRequest())
		if !errors.Is(err, ErrUpstreamUnavailable) {
			t.Errorf("status %d: err = %v, want ErrUpstreamUnavailable", status, err)
		}
		if errors.Is(err, ErrNoStructuredOutput) {
			t.Errorf("status %d: must NOT be ErrNoStructuredOutput — that would blame the model "+
				"for an operator fault", status)
		}
		var ue *UpstreamError
		if !errors.As(err, &ue) {
			t.Errorf("status %d: err does not carry an *UpstreamError: %v", status, err)
			continue
		}
		if ue.Status != status {
			t.Errorf("UpstreamError.Status = %d, want %d", ue.Status, status)
		}
		if ue.RequestID != "req-abc123" {
			t.Errorf("status %d: UpstreamError.RequestID = %q, want the header value — "+
				"it is the string the provider's support asks for", status, ue.RequestID)
		}
		if ue.Provider != ProviderFireworks {
			t.Errorf("status %d: UpstreamError.Provider = %q", status, ue.Provider)
		}
	}
}

// TestComplete_TransportFailureIsUnavailable covers the other half: nothing
// answered at all.
func TestComplete_TransportFailureIsUnavailable(t *testing.T) {
	c, err := New(Config{Provider: ProviderFireworks, Model: "m", APIKey: "k",
		// A port nothing listens on, with a short timeout so the test is quick.
		BaseURL: "http://127.0.0.1:1",
		HTTP:    &http.Client{Timeout: 2 * time.Second}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.Complete(context.Background(), baseRequest())
	if !errors.Is(err, ErrUpstreamUnavailable) {
		t.Errorf("err = %v, want ErrUpstreamUnavailable", err)
	}
}

// ---------------------------------------------------------------------------
// Schema repair
// ---------------------------------------------------------------------------

type queryShape struct {
	Entity string   `json:"entity"`
	Group  []string `json:"group"`
}

// TestDecodeInto_RepairsAtMostTwice counts the REQUESTS, not the repairs,
// because "how many times will this bill me?" is the property an operator cares
// about. DefaultMaxRepairs is 2, so the answer must be exactly 3.
func TestDecodeInto_RepairsAtMostTwice(t *testing.T) {
	var cap capture
	// Always unusable: no tool call at all.
	cap.respFunc = alwaysOK(chatText("I'm afraid I can't do that."))
	c := fakeAPI(t, &cap, Config{})

	var out queryShape
	resp, err := c.DecodeInto(context.Background(), baseRequest(), "emit_query", &out)
	if !errors.Is(err, ErrNoStructuredOutput) {
		t.Fatalf("err = %v, want ErrNoStructuredOutput", err)
	}
	// ⛔ THE BOUND ITSELF, AS A LITERAL. Every other assertion here is written
	// relative to DefaultMaxRepairs, so raising the constant from 2 to 5 left
	// internal/llm AND internal/nlq entirely green — measured, not supposed —
	// while quietly authorising two and a half times the billed calls and latency
	// on every unusable response. W0's deliverable is "retries with schema repair
	// (≤2)"; the number belongs in a test, so that moving it is an edit somebody
	// signs rather than a one-character diff nothing notices.
	if DefaultMaxRepairs != 2 {
		t.Fatalf("DefaultMaxRepairs = %d, want 2. W0 pins the repair bound at two "+
			"(RESEARCH-AGENTIC r8 §4). Raising it means more billed provider calls per "+
			"question: change it here, in the same commit, with the measurement in the "+
			"message.", DefaultMaxRepairs)
	}
	if got, want := cap.Calls(), 3; got != want {
		t.Errorf("provider was called %d time(s), want exactly %d (1 attempt + %d repairs)",
			got, want, DefaultMaxRepairs)

	}
	if resp == nil {
		t.Fatal("DecodeInto returned no response on exhaustion; the fingerprint is what attributes the failure to a model")
	}
	if resp.Repairs != DefaultMaxRepairs {
		t.Errorf("Response.Repairs = %d, want %d — the bound must be OBSERVABLE", resp.Repairs, DefaultMaxRepairs)
	}
	if resp.Fingerprint.ServedModel != "served-model-x" {
		t.Errorf("fingerprint not captured on the exhausted path: %+v", resp.Fingerprint)
	}
	if out.Entity != "" || out.Group != nil {
		t.Errorf("target was written on failure (%+v) — DecodeInto must never leave a partial value", out)
	}

	// The repair turns must have been APPENDED to Dynamic and must carry the
	// decoder's complaint; a repair loop that re-sends the identical body is a
	// retry loop wearing a repair loop's name.
	first, third := string(cap.Body(0)), string(cap.Body(2))
	if first == third {
		t.Error("the third request is byte-identical to the first: no repair instruction was added")
	}
	if !strings.Contains(third, "could not be used") {
		t.Errorf("third request carries no repair instruction: %s", third)
	}
}

// TestDecodeInto_RepairSucceedsOnSecondAttempt is the CONTROL for the test
// above: an implementation that never repaired at all (one call, then give up)
// would pass the "at most twice" bound trivially. This one only passes if the
// repair round trip actually happens AND its result is used.
func TestDecodeInto_RepairSucceedsOnSecondAttempt(t *testing.T) {
	var cap capture
	cap.respFunc = func(n int) (int, string) {
		if n == 0 {
			return http.StatusOK, chatOK("emit_query", `{"entity":"vouchers","nonsense":1}`)
		}
		return http.StatusOK, chatOK("emit_query", `{"entity":"vouchers","group":["month"]}`)
	}
	c := fakeAPI(t, &cap, Config{})

	var out queryShape
	resp, err := c.DecodeInto(context.Background(), baseRequest(), "emit_query", &out)
	if err != nil {
		t.Fatalf("DecodeInto: %v", err)
	}
	if cap.Calls() != 2 {
		t.Errorf("provider called %d time(s), want 2 (one bad, one repaired)", cap.Calls())
	}
	if resp.Repairs != 1 {
		t.Errorf("Repairs = %d, want 1", resp.Repairs)
	}
	if out.Entity != "vouchers" || len(out.Group) != 1 || out.Group[0] != "month" {
		t.Errorf("decoded = %+v", out)
	}
}

// TestDecodeInto_UnknownFieldsRejected is internal/nlq/translate.go:229's
// property, ported. A hallucinated shape must not silently drop data.
func TestDecodeInto_UnknownFieldsRejected(t *testing.T) {
	var cap capture
	// "groupp" is the classic near-miss: valid JSON, valid-looking, and a
	// lenient decoder would accept the body and leave Group EMPTY — an answer
	// aggregated over the whole ledger, presented as an answer grouped by
	// month. Nothing downstream can tell the difference.
	cap.respFunc = alwaysOK(chatOK("emit_query", `{"entity":"vouchers","groupp":["month"]}`))
	c := fakeAPI(t, &cap, Config{})

	var out queryShape
	_, err := c.DecodeInto(context.Background(), baseRequest(), "emit_query", &out)
	if !errors.Is(err, ErrNoStructuredOutput) {
		t.Fatalf("err = %v, want ErrNoStructuredOutput", err)
	}
	if out.Entity != "" {
		t.Errorf("a field was accepted from a body with an unknown field: %+v", out)
	}

	// CONTROL: the same body without the unknown field must succeed, or the
	// assertion above would also pass on an implementation that rejected
	// everything.
	var cap2 capture
	cap2.respFunc = alwaysOK(chatOK("emit_query", `{"entity":"vouchers","group":["month"]}`))
	c2 := fakeAPI(t, &cap2, Config{})
	var ok queryShape
	if _, err := c2.DecodeInto(context.Background(), baseRequest(), "emit_query", &ok); err != nil {
		t.Fatalf("control: a well-formed body was rejected: %v", err)
	}
	if ok.Entity != "vouchers" {
		t.Errorf("control decoded = %+v", ok)
	}
}

// TestDecodeInto_TransportFailureIsNotRepaired: repair is for a bad SHAPE, not
// for a dead endpoint. Re-sending after a 503 is a retry policy, and a retry
// policy hidden in a repair loop triples the load on a provider that is already
// failing.
func TestDecodeInto_TransportFailureIsNotRepaired(t *testing.T) {
	var cap capture
	cap.respFunc = func(int) (int, string) { return http.StatusServiceUnavailable, `{"error":"down"}` }
	c := fakeAPI(t, &cap, Config{})

	var out queryShape
	_, err := c.DecodeInto(context.Background(), baseRequest(), "emit_query", &out)
	if !errors.Is(err, ErrUpstreamUnavailable) {
		t.Fatalf("err = %v, want ErrUpstreamUnavailable", err)
	}
	if cap.Calls() != 1 {
		t.Errorf("provider called %d time(s) on a 503, want exactly 1", cap.Calls())
	}
}

// ---------------------------------------------------------------------------
// Input bounds and the tenant-id gate
// ---------------------------------------------------------------------------

// TestOversizedInputMakesNoNetworkCall: the oracle is the SERVER, which must
// never have been reached. internal/nlq/translate.go:41 is the shape.
func TestOversizedInputMakesNoNetworkCall(t *testing.T) {
	var cap capture
	cap.respFunc = alwaysOK(chatText("{}"))
	c := fakeAPI(t, &cap, Config{})

	req := baseRequest()
	req.MaxInputBytes = 1024
	req.Dynamic = []Message{{Role: RoleUser, Content: strings.Repeat("a whole ledger pasted in. ", 200)}}

	_, err := c.Complete(context.Background(), req)
	if !errors.Is(err, ErrInputTooLarge) {
		t.Fatalf("err = %v, want ErrInputTooLarge", err)
	}
	if cap.Calls() != 0 {
		t.Errorf("the server was reached %d time(s); an oversized request must be refused before the socket opens", cap.Calls())
	}

	// CONTROL: the same client with the same limit and a small body must go
	// through, or "never reached the server" would be trivially true.
	small := baseRequest()
	small.MaxInputBytes = 1024
	if _, err := c.Complete(context.Background(), small); err != nil {
		t.Fatalf("control: an in-bounds request was refused: %v", err)
	}
	if cap.Calls() != 1 {
		t.Errorf("control: server saw %d call(s), want 1", cap.Calls())
	}
}

// TestNoTenantDataInRequestBody is internal/nlq's TestToQuery_NoTenantDataInPrompt,
// ported. The oracle is the captured bytes, not a code comment.
func TestNoTenantDataInRequestBody(t *testing.T) {
	var cap capture
	cap.respFunc = alwaysOK(chatText("{}"))
	c := fakeAPI(t, &cap, Config{})

	req := baseRequest()
	tenant := uuid.New()
	req.TenantID = tenant
	if _, err := c.Complete(context.Background(), req); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	body := string(cap.LastBody())
	if strings.Contains(body, tenant.String()) {
		t.Error("request body leaks the tenant id into the prompt")
	}
	for _, secret := range []string{"Acme Traders", "Sundry Debtors"} {
		if strings.Contains(body, secret) {
			t.Errorf("request body unexpectedly contains %q", secret)
		}
	}
}

// TestTenantIDInPromptIsRefused is the structural half: a caller that DOES
// smuggle the id into a message is stopped before the socket opens.
//
// ⛔ THIS IS THE NON-VACUOUS PART. The test above passes on any implementation
// that simply does not add the id — including one that would happily forward an
// id a caller put there. This one fails on that implementation.
func TestTenantIDInPromptIsRefused(t *testing.T) {
	tenant := uuid.New()
	for _, spelling := range []string{
		tenant.String(),
		strings.ReplaceAll(tenant.String(), "-", ""),
	} {
		var cap capture
		cap.respFunc = alwaysOK(chatText("{}"))
		c := fakeAPI(t, &cap, Config{})

		req := baseRequest()
		req.TenantID = tenant
		req.Dynamic = []Message{{Role: RoleUser, Content: "for company " + spelling + ", sales by month"}}

		_, err := c.Complete(context.Background(), req)
		if !errors.Is(err, ErrTenantIDInPrompt) {
			t.Errorf("spelling %q: err = %v, want ErrTenantIDInPrompt", spelling, err)
		}
		if cap.Calls() != 0 {
			t.Errorf("spelling %q: the id reached the provider (%d call(s))", spelling, cap.Calls())
		}
	}
}

// ---------------------------------------------------------------------------
// Fingerprint on refusal
// ---------------------------------------------------------------------------

// TestFingerprintCapturedOnRefusal: a 200 the model refused still tells us WHO
// refused. D3 records that a provider may change the weights behind a model id
// with no notice; a fingerprint recorded only on success is missing from exactly
// the calls worth investigating.
func TestFingerprintCapturedOnRefusal(t *testing.T) {
	t.Run("openai-compatible refusal field", func(t *testing.T) {
		var cap capture
		cap.respFunc = alwaysOK(`{"id":"resp_r","model":"served-model-x","system_fingerprint":"fp_r",` +
			`"choices":[{"finish_reason":"content_filter","message":{"role":"assistant","content":"","refusal":"I cannot help with that."}}],` +
			`"usage":{"prompt_tokens":11,"completion_tokens":2}}`)
		c := fakeAPI(t, &cap, Config{})

		resp, err := c.Complete(context.Background(), baseRequest())
		if err != nil {
			t.Fatalf("a refusal is a served 200 and must not be an error from Complete: %v", err)
		}
		if resp.Fingerprint.ServedModel != "served-model-x" || resp.Fingerprint.SystemFingerprint != "fp_r" ||
			resp.Fingerprint.ResponseID != "resp_r" || resp.Fingerprint.RequestID != "req-abc123" {
			t.Errorf("fingerprint on refusal = %+v", resp.Fingerprint)
		}
		if resp.Fingerprint.Extra["refusal"] != "I cannot help with that." {
			t.Errorf("refusal text not captured: %+v", resp.Fingerprint.Extra)
		}
		if resp.FinishReason != "content_filter" {
			t.Errorf("FinishReason = %q", resp.FinishReason)
		}
		// And the tokens were still spent, so they are still reported.
		if resp.Usage.Total() != 13 {
			t.Errorf("usage on refusal = %+v — a refusal is billed", resp.Usage)
		}
	})

	t.Run("no choices at all", func(t *testing.T) {
		var cap capture
		cap.respFunc = alwaysOK(`{"id":"resp_e","model":"served-model-x","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":0}}`)
		c := fakeAPI(t, &cap, Config{})
		resp, err := c.Complete(context.Background(), baseRequest())
		if !errors.Is(err, ErrNoStructuredOutput) {
			t.Fatalf("err = %v, want ErrNoStructuredOutput", err)
		}
		if resp == nil || resp.Fingerprint.ServedModel != "served-model-x" {
			t.Fatalf("fingerprint missing on the empty-choices path: %+v", resp)
		}
		if resp.Fingerprint.RequestID != "req-abc123" {
			t.Errorf("RequestID = %q", resp.Fingerprint.RequestID)
		}
	})

	t.Run("anthropic stop_reason refusal", func(t *testing.T) {
		var cap capture
		cap.respFunc = alwaysOK(`{"id":"msg_1","model":"claude-haiku-4-5","stop_reason":"refusal",` +
			`"content":[{"type":"text","text":"I can't help with that."}],` +
			`"usage":{"input_tokens":9,"output_tokens":4,"cache_read_input_tokens":5}}`)
		c := fakeAPI(t, &cap, Config{Provider: ProviderAnthropic, Model: "claude-haiku-4-5"})
		resp, err := c.Complete(context.Background(), baseRequest())
		if !errors.Is(err, ErrNoStructuredOutput) {
			t.Fatalf("err = %v, want ErrNoStructuredOutput", err)
		}
		if resp == nil || resp.Fingerprint.ServedModel != "claude-haiku-4-5" || resp.Fingerprint.ResponseID != "msg_1" {
			t.Fatalf("fingerprint on anthropic refusal = %+v", resp)
		}
		if resp.Fingerprint.Extra["refusal"] == "" {
			t.Error("refusal text not captured")
		}
		if resp.Usage.CachedPromptTokens != 5 {
			t.Errorf("cache_read_input_tokens not read: %+v", resp.Usage)
		}
	})
}

// ---------------------------------------------------------------------------
// New()'s refusals
// ---------------------------------------------------------------------------

func TestNew_RefusesIncompleteConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  Config
	}{
		{"unknown provider", Config{Provider: "openai", Model: "m", APIKey: "k"}},
		{"no model", Config{Provider: ProviderFireworks, APIKey: "k"}},
		{"no api key", Config{Provider: ProviderFireworks, Model: "m"}},
		{"vllm with no base url", Config{Provider: ProviderVLLM, Model: "m", APIKey: "k"}},
	} {
		if _, err := New(tc.cfg); !errors.Is(err, ErrNotConfigured) {
			t.Errorf("%s: err = %v, want ErrNotConfigured", tc.name, err)
		}
	}

	// CONTROL: the one legitimate empty credential. A vLLM we started ourselves
	// with no --api-key has none, and refusing it would make the self-hosted
	// path (W9 residency) unwireable.
	if _, err := New(Config{Provider: ProviderVLLM, Model: "m", BaseURL: "http://gpu-1:8000"}); err != nil {
		t.Errorf("vllm with a base url and no key must be allowed: %v", err)
	}
}

func TestComplete_RefusesUntraceableRequest(t *testing.T) {
	var cap capture
	cap.respFunc = alwaysOK(chatText("{}"))
	c := fakeAPI(t, &cap, Config{})

	for _, tc := range []struct {
		name   string
		mutate func(*Request)
	}{
		{"no feature", func(r *Request) { r.Feature = "" }},
		{"no prompt version", func(r *Request) { r.PromptVersion = "" }},
		{"no messages", func(r *Request) { r.Static = nil; r.Dynamic = nil }},
	} {
		req := baseRequest()
		tc.mutate(req)
		if _, err := c.Complete(context.Background(), req); err == nil {
			t.Errorf("%s: accepted; it must be refused", tc.name)
		}
	}
	if cap.Calls() != 0 {
		t.Errorf("an untraceable request reached the provider (%d call(s))", cap.Calls())
	}
}

// TestStaticPromptHash_IgnoresDynamic pins what the trajectory logger records:
// the hash answers "did the shared prefix change?", so it must NOT move when the
// question does — and it MUST move when the system prompt does.
func TestStaticPromptHash_IgnoresDynamic(t *testing.T) {
	a := baseRequest()
	b := baseRequest()
	b.Dynamic = []Message{{Role: RoleUser, Content: "something else entirely"}}
	if StaticPromptHash(a) != StaticPromptHash(b) {
		t.Error("the prompt hash moved when only the Dynamic half changed")
	}
	c := baseRequest()
	c.Static = []Message{{Role: RoleSystem, Content: "a different system prompt"}}
	if StaticPromptHash(a) == StaticPromptHash(c) {
		t.Error("the prompt hash did NOT move when the static prefix changed — " +
			"it would not detect a prompt edited without a version bump")
	}
	d := baseRequest()
	d.Tools = []Tool{{Name: "t", Parameters: json.RawMessage(`{"type":"object"}`)}}
	if StaticPromptHash(a) == StaticPromptHash(d) {
		t.Error("the prompt hash ignores tool schemas, which are part of the cacheable prefix")
	}
}

// ---------------------------------------------------------------------------
// The trajectory seam: a REFUSED reservation still leaves a span.
// ---------------------------------------------------------------------------

// spanLog records every span this package opened, and what it was closed with.
type spanLog struct {
	starts []SpanStart
	errs   []error
	resps  []*Response
}

func (l *spanLog) StartLLMCall(ctx context.Context, s SpanStart) (context.Context, LLMSpan) {
	l.starts = append(l.starts, s)
	return ctx, &loggedSpan{l: l}
}

type loggedSpan struct{ l *spanLog }

func (s *loggedSpan) End(resp *Response, err error) {
	s.l.resps = append(s.l.resps, resp)
	s.l.errs = append(s.l.errs, err)
}

// TestBudgetRefusalIsRecordedAsASpan.
//
// ⛔ THE FAILURE THIS PINS IS AN ABSENCE, WHICH IS WHY IT NEEDS A TEST AT ALL.
// A tenant at its cap is the one llm failure whose cause is invisible from
// outside: the provider is healthy, the credential is fine, and the caller sees
// a status that a dozen other faults also produce. Before Complete opened a
// span on the refusal path, the trace store — the place an operator goes with
// "why did this company's assistant stop answering?" — held nothing at all for
// it, so the question could only be answered by reading the tenant's own logs.
//
// The second arm is the control: the same assertions would pass on a Client that
// opened a span for EVERY call and recorded no error, so the served call is
// checked to close its span with a nil error and a real fingerprint.
func TestBudgetRefusalIsRecordedAsASpan(t *testing.T) {
	t.Run("refused reservation leaves exactly one closed span", func(t *testing.T) {
		var cap capture
		cap.respFunc = alwaysOK(chatText("{}"))
		log := &spanLog{}
		c := fakeAPI(t, &cap, Config{Budget: &countingBudget{refuse: true}, Recorder: log})

		_, err := c.Complete(context.Background(), baseRequest())
		if !errors.Is(err, ErrBudgetExhausted) {
			t.Fatalf("err = %v, want ErrBudgetExhausted", err)
		}
		if len(log.starts) != 1 || len(log.errs) != 1 {
			t.Fatalf("recorded %d span start(s) and %d end(s) for a refused reservation, want 1 and 1 — "+
				"a refusal with no span is a cap nobody can see from the trace store", len(log.starts), len(log.errs))
		}
		if !errors.Is(log.errs[0], ErrBudgetExhausted) {
			t.Errorf("the span closed with %v, want the budget sentinel — the trace store "+
				"classifies the failure by this error", log.errs[0])
		}
		if log.resps[0] != nil {
			t.Errorf("a refused call has no response and must not invent one: %+v", log.resps[0])
		}
		// The span must still describe WHICH call was refused, or the row cannot
		// be attributed to a feature or a prompt version.
		if log.starts[0].Feature != "test.feature" || log.starts[0].PromptVersion != "v1" {
			t.Errorf("refusal span = %+v, want the feature and prompt version of the refused call", log.starts[0])
		}
		if log.starts[0].PromptHash == "" {
			t.Error("refusal span carries no prompt hash; spanStart is the single source for both " +
				"span shapes precisely so this cannot drift")
		}
		if cap.Calls() != 0 {
			t.Errorf("the provider was called %d time(s) on a refused reservation", cap.Calls())
		}
	})

	t.Run("control: a served call opens one span and closes it clean", func(t *testing.T) {
		var cap capture
		cap.respFunc = alwaysOK(chatOK("emit_query", `{"entity":"vouchers"}`))
		log := &spanLog{}
		c := fakeAPI(t, &cap, Config{Recorder: log})

		if _, err := c.Complete(context.Background(), baseRequest()); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if len(log.starts) != 1 {
			t.Fatalf("recorded %d spans for one served call, want 1 — the refusal span must not "+
				"double up on the path that already had one", len(log.starts))
		}
		if log.errs[0] != nil {
			t.Errorf("a served call closed its span with %v", log.errs[0])
		}
		if log.resps[0] == nil || log.resps[0].Fingerprint.ServedModel != "served-model-x" {
			t.Errorf("served span carries no fingerprint: %+v", log.resps[0])
		}
	})
}
