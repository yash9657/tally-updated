package llm

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Pure adapter table tests. NO httptest server, NO goroutine, NO timing.
// ---------------------------------------------------------------------------
//
// This is what Encode/Decode being pure buys: "does Fireworks put the grammar in
// response_format and vLLM in guided_grammar?" is answered by a string
// comparison over real bytes rather than by a mock that could be wrong in the
// same direction as the code it checks.

// TestEveryProviderHasAnAdapter is the COMPLETENESS control. Without it a new
// Provider const compiles happily and fails at the first call in production;
// with it, adding a const and forgetting the adapter is a red test.
func TestEveryProviderHasAnAdapter(t *testing.T) {
	if len(KnownProviders) == 0 {
		t.Fatal("KnownProviders is empty — this test would be vacuous")
	}
	seen := map[Provider]bool{}
	for _, p := range KnownProviders {
		a, ok := AdapterFor(p)
		if !ok {
			t.Errorf("provider %q has no adapter", p)
			continue
		}
		if a.Provider() != p {
			t.Errorf("adapter registered under %q reports Provider() = %q", p, a.Provider())
		}
		if a.Path() == "" {
			t.Errorf("provider %q has an empty Path()", p)
		}
		seen[p] = true
	}
	// And the reverse: nothing in the registry is missing from the exported
	// vocabulary, or config validation would print a list that lies.
	for p := range adapters {
		if !seen[p] {
			t.Errorf("adapter %q is registered but absent from KnownProviders", p)
		}
	}
	if _, ok := AdapterFor("openai"); ok {
		t.Error("AdapterFor answered for an unknown provider; the vocabulary is not closed")
	}
}

// TestAdapterEndpoints pins the three things that genuinely differ between
// nominally "OpenAI-compatible" hosts. ⛔ Each row was a deliberate reading of a
// provider's docs, not a copy of its neighbour: a tidied Fireworks base
// (dropping /inference) or a DeepInfra path without /v1/openai answers 404 for
// every call, and a 404 reads exactly like a retired model id.
func TestAdapterEndpoints(t *testing.T) {
	for _, tc := range []struct {
		provider Provider
		base     string
		path     string
	}{
		{ProviderFireworks, "https://api.fireworks.ai/inference", "/v1/chat/completions"},
		{ProviderDeepInfra, "https://api.deepinfra.com", "/v1/openai/chat/completions"},
		{ProviderSarvam, "https://api.sarvam.ai", "/v1/chat/completions"},
		{ProviderVLLM, "", "/v1/chat/completions"},
		{ProviderAnthropic, "https://api.anthropic.com", "/v1/messages"},
	} {
		a, _ := AdapterFor(tc.provider)
		if got := a.DefaultBaseURL(); got != tc.base {
			t.Errorf("%s DefaultBaseURL = %q, want %q", tc.provider, got, tc.base)
		}
		if got := a.Path(); got != tc.path {
			t.Errorf("%s Path = %q, want %q", tc.provider, got, tc.path)
		}
	}
}

// TestAdapterAuthHeaders: the credential goes where each API expects it. The
// Anthropic row is the one that matters — the Messages API ignores a bearer
// token and answers 401, and it also requires anthropic-version.
func TestAdapterAuthHeaders(t *testing.T) {
	for _, tc := range []struct {
		provider Provider
		check    func(t *testing.T, h http.Header)
	}{
		{ProviderFireworks, func(t *testing.T, h http.Header) {
			if h.Get("Authorization") != "Bearer k" {
				t.Errorf("Authorization = %q", h.Get("Authorization"))
			}
			if h.Get("x-api-key") != "" {
				t.Error("must not send x-api-key to an OpenAI-compatible host")
			}
		}},
		{ProviderDeepInfra, func(t *testing.T, h http.Header) {
			if h.Get("Authorization") != "Bearer k" {
				t.Errorf("Authorization = %q", h.Get("Authorization"))
			}
		}},
		{ProviderSarvam, func(t *testing.T, h http.Header) {
			if h.Get("Authorization") != "Bearer k" {
				t.Errorf("Authorization = %q", h.Get("Authorization"))
			}
		}},
		{ProviderVLLM, func(t *testing.T, h http.Header) {
			if h.Get("Authorization") != "Bearer k" {
				t.Errorf("Authorization = %q", h.Get("Authorization"))
			}
		}},
		{ProviderAnthropic, func(t *testing.T, h http.Header) {
			if h.Get("x-api-key") != "k" {
				t.Errorf("x-api-key = %q — the Messages API does not read a bearer token", h.Get("x-api-key"))
			}
			if h.Get("anthropic-version") != anthropicVersionHeader {
				t.Errorf("anthropic-version = %q", h.Get("anthropic-version"))
			}
			if h.Get("Authorization") != "" {
				t.Error("must not send Authorization to the Messages API")
			}
		}},
	} {
		a, _ := AdapterFor(tc.provider)
		h := http.Header{}
		a.SetAuth(h, "k")
		t.Run(string(tc.provider), func(t *testing.T) { tc.check(t, h) })
	}
}

// TestAdapterEncodeDecode_OpenAICompatible is the shared encode/decode table for
// the four providers that speak /v1/chat/completions.
func TestAdapterEncodeDecode_OpenAICompatible(t *testing.T) {
	req := &Request{
		Feature:       "f",
		PromptVersion: "v1",
		Static:        []Message{{Role: RoleSystem, Content: "S"}},
		Dynamic:       []Message{{Role: RoleUser, Content: "D"}},
		Tools:         []Tool{{Name: "emit", Description: "e", Parameters: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice:    ToolChoice{Mode: ToolChoiceNamed, Name: "emit"},
		MaxTokens:     64,
	}

	// ⛔ wantCached IS A LITERAL PER PROVIDER, NOT `if Has(CapCacheMetrics)`.
	// Deriving the expectation from the code under test made this assertion a
	// tautology: Decode zeroes the field on exactly the predicate the expectation
	// used, so adding CapCacheMetrics to sarvam's or deepinfra's mask left the
	// whole package green — measured. The number has to come from the test
	// author, i.e. from someone who checked what the provider actually returns.
	for _, tc := range []struct {
		p Provider
		// wantCached is what Usage.CachedPromptTokens must be after decoding a
		// body that reports prompt_tokens_details.cached_tokens = 4. Only
		// Fireworks promises the number; for the rest a 0 here means "this
		// provider tells us nothing", which is exactly what must NOT be reported
		// as "the cache did nothing".
		wantCached int
	}{
		{ProviderFireworks, 4},
		{ProviderDeepInfra, 0},
		{ProviderSarvam, 0},
		{ProviderVLLM, 0},
	} {
		p := tc.p
		a, _ := AdapterFor(p)
		raw, err := a.Encode("model-x", req)
		if err != nil {
			t.Fatalf("%s Encode: %v", p, err)
		}
		// The static turn precedes the dynamic one, and the body opens with
		// model then messages — the ordering the cache contract rests on.
		if !strings.HasPrefix(string(raw), `{"model":"model-x","messages":[`) {
			t.Errorf("%s: body must open with model then messages, got %.60s", p, raw)
		}
		si, di := strings.Index(string(raw), `"content":"S"`), strings.Index(string(raw), `"content":"D"`)
		if si < 0 || di < 0 || si > di {
			t.Errorf("%s: static turn must precede dynamic (S at %d, D at %d)", p, si, di)
		}

		// Decode a canonical 200.
		body := []byte(`{"id":"r1","model":"served-y","system_fingerprint":"fp",` +
			`"choices":[{"finish_reason":"tool_calls","message":{"content":"","tool_calls":[` +
			`{"id":"c1","type":"function","function":{"name":"emit","arguments":"{\"a\":1}"}}]}}],` +
			`"usage":{"prompt_tokens":9,"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":4}}}`)
		hdr := http.Header{}
		hdr.Set("x-request-id", "rid-1")
		resp, err := a.Decode(200, hdr, body)
		if err != nil {
			t.Fatalf("%s Decode: %v", p, err)
		}
		if resp.Fingerprint.ServedModel != "served-y" || resp.Fingerprint.ResponseID != "r1" ||
			resp.Fingerprint.RequestID != "rid-1" {
			t.Errorf("%s fingerprint = %+v", p, resp.Fingerprint)
		}
		if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "emit" ||
			string(resp.ToolCalls[0].Arguments) != `{"a":1}` {
			t.Errorf("%s tool calls = %+v", p, resp.ToolCalls)
		}
		if resp.Usage.PromptTokens != 9 || resp.Usage.CompletionTokens != 3 {
			t.Errorf("%s usage = %+v", p, resp.Usage)
		}
		// ⭐ Cache metrics ONLY where the adapter claims CapCacheMetrics.
		// Reporting a number we were not promised is how a 0 that means "no
		// data" gets read as "the cache is not working".
		wantCached := tc.wantCached
		if resp.Usage.CachedPromptTokens != wantCached {
			t.Errorf("%s CachedPromptTokens = %d, want %d (CapCacheMetrics=%v)",
				p, resp.Usage.CachedPromptTokens, wantCached, a.Capabilities().Has(CapCacheMetrics))
		}

		// Non-2xx is an UpstreamError naming the provider.
		_, derr := a.Decode(503, hdr, []byte(`{"error":"down"}`))
		var ue *UpstreamError
		if !errors.As(derr, &ue) || ue.Provider != p || ue.Status != 503 {
			t.Errorf("%s non-2xx decode = %v", p, derr)
		}
		if !errors.Is(derr, ErrUpstreamUnavailable) {
			t.Errorf("%s: non-2xx must unwrap to ErrUpstreamUnavailable", p)
		}
	}
}

// TestAdapterEncodeDecode_Anthropic is the Messages API's own table. It is a
// SEPARATE test because the shape is genuinely different in four ways, and a
// shared table would have hidden all four behind an if.
func TestAdapterEncodeDecode_Anthropic(t *testing.T) {
	a, _ := AdapterFor(ProviderAnthropic)

	req := &Request{
		Feature:       "f",
		PromptVersion: "v1",
		Static:        []Message{{Role: RoleSystem, Content: "S1"}, {Role: RoleSystem, Content: "S2"}},
		Dynamic:       []Message{{Role: RoleUser, Content: "D"}},
		Tools:         []Tool{{Name: "emit_query", Description: "e", Parameters: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice:    ToolChoice{Mode: ToolChoiceNamed, Name: "emit_query"},
	}
	raw, err := a.Encode("claude-haiku-4-5", req)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	var sent map[string]any
	if err := json.Unmarshal(raw, &sent); err != nil {
		t.Fatalf("undecodable: %v", err)
	}
	// (1) the system prompt is a TOP-LEVEL FIELD, and two system turns are
	// concatenated rather than one silently overwriting the other.
	if sent["system"] != "S1\n\nS2" {
		t.Errorf("system = %#v, want both static turns concatenated", sent["system"])
	}
	msgs, _ := sent["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages = %#v, want only the dynamic user turn", sent["messages"])
	}
	// (2) max_tokens is REQUIRED and defaulted rather than omitted.
	if sent["max_tokens"] != float64(anthropicDefaultMaxTokens) {
		t.Errorf("max_tokens = %#v, want the default %d", sent["max_tokens"], anthropicDefaultMaxTokens)
	}
	// (3) tools carry input_schema, not function.parameters.
	tools, _ := sent["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %#v", sent["tools"])
	}
	tool, _ := tools[0].(map[string]any)
	if _, ok := tool["input_schema"]; !ok {
		t.Errorf("tool = %#v, want input_schema", tool)
	}
	if _, ok := tool["function"]; ok {
		t.Error("the Messages API has no function wrapper")
	}
	tc, _ := sent["tool_choice"].(map[string]any)
	if tc["type"] != "tool" || tc["name"] != "emit_query" {
		t.Errorf("tool_choice = %#v", sent["tool_choice"])
	}

	// "required" is spelled "any" on this API.
	req2 := *req
	req2.ToolChoice = ToolChoice{Mode: ToolChoiceRequired}
	raw2, err := a.Encode("m", &req2)
	if err != nil {
		t.Fatalf("Encode required: %v", err)
	}
	if !strings.Contains(string(raw2), `"type":"any"`) {
		t.Errorf(`ToolChoiceRequired must encode as {"type":"any"}, got %s`, raw2)
	}

	// (4) tool calls come back as content BLOCKS.
	body := []byte(`{"id":"msg_1","model":"claude-haiku-4-5","stop_reason":"tool_use",` +
		`"content":[{"type":"text","text":"ok"},{"type":"tool_use","id":"tu_1","name":"emit_query","input":{"entity":"vouchers"}}],` +
		`"usage":{"input_tokens":50,"output_tokens":8,"cache_read_input_tokens":40,"cache_creation_input_tokens":7}}`)
	hdr := http.Header{}
	hdr.Set("request-id", "req_anthropic") // NOT x-request-id
	resp, err := a.Decode(200, hdr, body)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if resp.Fingerprint.RequestID != "req_anthropic" {
		t.Errorf("RequestID = %q — the Messages API spells the header request-id", resp.Fingerprint.RequestID)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "emit_query" ||
		string(resp.ToolCalls[0].Arguments) != `{"entity":"vouchers"}` {
		t.Errorf("tool calls = %+v", resp.ToolCalls)
	}
	if resp.Text != "ok" {
		t.Errorf("Text = %q", resp.Text)
	}
	// ⛔ cache_READ, not cache_creation: a creation write is input billed at a
	// PREMIUM, and counting it as cached would make the first call of every
	// prompt version look like the cheapest one.
	if resp.Usage.CachedPromptTokens != 40 {
		t.Errorf("CachedPromptTokens = %d, want 40 (cache_read only)", resp.Usage.CachedPromptTokens)
	}
	if resp.Usage.PromptTokens != 50 || resp.Usage.CompletionTokens != 8 {
		t.Errorf("usage = %+v", resp.Usage)
	}

	// RoleTool is refused loudly rather than guessed at.
	req3 := *req
	req3.Dynamic = []Message{{Role: RoleTool, ToolCallID: "tu_1", Content: "42"}}
	if _, err := a.Encode("m", &req3); err == nil {
		t.Error("RoleTool must be refused by the Messages API adapter, not translated by guesswork")
	} else if !strings.Contains(err.Error(), "tool_result") {
		t.Errorf("the refusal must say what the correct encoding is, got: %v", err)
	}
}

// TestOpenAIAdapter_ParallelToolCallsHonourCapability: an adapter that does not
// claim CapParallelTools keeps the first call and RECORDS that it dropped the
// rest — silently discarding them would make a real provider behaviour
// invisible.
func TestOpenAIAdapter_ParallelToolCallsHonourCapability(t *testing.T) {
	body := []byte(`{"id":"r","model":"m","choices":[{"finish_reason":"tool_calls","message":{"content":"",` +
		`"tool_calls":[{"id":"a","type":"function","function":{"name":"x","arguments":"{}"}},` +
		`{"id":"b","type":"function","function":{"name":"y","arguments":"{}"}}]}}],"usage":{}}`)

	fw, _ := AdapterFor(ProviderFireworks) // has CapParallelTools
	resp, err := fw.Decode(200, http.Header{}, body)
	if err != nil {
		t.Fatalf("fireworks: %v", err)
	}
	if len(resp.ToolCalls) != 2 {
		t.Errorf("fireworks kept %d tool calls, want 2", len(resp.ToolCalls))
	}

	sv, _ := AdapterFor(ProviderSarvam) // does NOT claim CapParallelTools
	resp2, err := sv.Decode(200, http.Header{}, body)
	if err != nil {
		t.Fatalf("sarvam: %v", err)
	}
	if len(resp2.ToolCalls) != 1 {
		t.Errorf("sarvam kept %d tool calls, want 1", len(resp2.ToolCalls))
	}
	if resp2.Fingerprint.Extra["dropped_parallel_tool_calls"] != "1" {
		t.Errorf("the drop was not recorded: %+v", resp2.Fingerprint.Extra)
	}
}

// TestCapabilityString keeps the error messages readable — a refusal that says
// "capabilities: 21" helps nobody.
func TestCapabilityString(t *testing.T) {
	if got := (CapJSONSchema | CapGrammar).String(); got != "json_schema|grammar" {
		t.Errorf("String() = %q", got)
	}
	if got := Capability(0).String(); got != "none" {
		t.Errorf("empty String() = %q", got)
	}
}

// TestSchemaAndGrammarAreMutuallyExclusive: providers apply ONE decoding
// constraint, so sending both leaves nobody able to say which was in force.
func TestSchemaAndGrammarAreMutuallyExclusive(t *testing.T) {
	a, _ := AdapterFor(ProviderFireworks)
	req := &Request{
		Feature: "f", PromptVersion: "v1",
		Static:  []Message{{Role: RoleSystem, Content: "s"}},
		Schema:  &ResponseSchema{Name: "n", Schema: json.RawMessage(`{"type":"object"}`)},
		Grammar: `root ::= "x"`,
	}
	if _, err := a.Encode("m", req); err == nil {
		t.Error("Schema + Grammar together must be refused")
	}
}

// TestProviderCapabilitiesAreExactlyDeclared pins every adapter's Capability
// bitmask, bit for bit.
//
// ⛔ THIS IS THE TEST THAT MAKES THE MASK LOAD-BEARING, AND UNTIL IT EXISTED THE
// MASK WAS DECORATIVE. Measured, not supposed: `| CapCacheMetrics` added to
// sarvam's declaration alone, or to deepinfra's, left `go test ./internal/llm/`
// green; so did claiming CapJSONSchema|CapGrammar on anthropic. Every adapter's
// declaration is what the Client, the budget accounting and every future planner
// reason about when they ask "can this provider constrain output?" — an
// over-claim is a silent drop, which is the one failure mode this package's
// Capability doc says it exists to prevent.
//
// ⚠️ THE EXPECTATIONS ARE WRITTEN OUT AS BITS RATHER THAN COMPARED TO
// a.Capabilities(). An assertion phrased against the value under test is the
// tautology this file already had once (see wantCached above). Changing a
// provider's real capabilities is meant to be an edit HERE, with the evidence —
// a provider doc, a measured response — in the commit message.
func TestProviderCapabilitiesAreExactlyDeclared(t *testing.T) {
	want := map[Provider]Capability{
		// Grammar mode, json_schema, forced tools, parallel tools and
		// prompt-cache accounting: the D3 primary, and the only provider here
		// that reports cached_tokens.
		ProviderFireworks: CapJSONSchema | CapGrammar | CapToolChoiceNamed | CapParallelTools | CapCacheMetrics,
		// No grammar, no cache metrics.
		ProviderDeepInfra: CapJSONSchema | CapToolChoiceNamed | CapParallelTools,
		// The narrowest surface: no grammar, no parallel tool calls, no cache
		// metrics.
		ProviderSarvam: CapJSONSchema | CapToolChoiceNamed,
		// Self-hosted: grammar via guided_grammar, but nothing upstream reports
		// a cache hit rate.
		ProviderVLLM: CapJSONSchema | CapGrammar | CapToolChoiceNamed | CapParallelTools,
		// The Messages API: no json_schema (a forced tool call is the way), no
		// grammar; cache_read_input_tokens IS reported.
		ProviderAnthropic: CapToolChoiceNamed | CapParallelTools | CapCacheMetrics,
	}

	checked := 0
	for _, p := range KnownProviders {
		a, ok := AdapterFor(p)
		if !ok {
			t.Errorf("no adapter for %s, which is in KnownProviders", p)
			continue
		}
		w, listed := want[p]
		if !listed {
			t.Errorf("provider %s has an adapter but no expectation in this test. A new "+
				"provider's capability claim must be written down here, or it is a claim "+
				"nothing checks", p)
			continue
		}
		if got := a.Capabilities(); got != w {
			t.Errorf("%s Capabilities() = %s (%#b), want %s (%#b). A bit ADDED is an "+
				"over-claim — the Client will encode a constraint the provider ignores, "+
				"which presents as a model that 'sometimes returns prose'. A bit DROPPED "+
				"turns a working feature into a refusal.", p, got, uint8(got), w, uint8(w))
		}
		checked++
	}
	if checked != len(want) {
		t.Fatalf("checked %d of %d providers; the walk over KnownProviders missed some, so "+
			"this test is partly vacuous", checked, len(want))
	}

	// ⛔ THE CONTROL FOR Has ITSELF. Every assertion above compares whole masks,
	// so they would all survive `func (c Capability) Has(...) bool { return true }`
	// — the do-nothing implementation that makes every capability check in the
	// package pass. Has is what the adapters actually call, so it is asserted in
	// both directions on a mask whose bits are known.
	m := CapJSONSchema | CapToolChoiceNamed
	if !m.Has(CapJSONSchema) || !m.Has(CapJSONSchema|CapToolChoiceNamed) {
		t.Error("Has() denied a bit that is set; every capability gate in this package would refuse work the provider can do")
	}
	if m.Has(CapGrammar) || m.Has(CapJSONSchema|CapGrammar) {
		t.Fatal("Has() ACCEPTED a bit that is not set. Every refusal in every adapter is now " +
			"unreachable, and an unsupported grammar or schema will be dropped on the wire " +
			"instead of refused — which the Capability doc names as the worst failure mode " +
			"in this package.")
	}
}

func TestNamedFireworksToolDisablesParallelDecisions(t *testing.T) {
	a, _ := AdapterFor(ProviderFireworks)
	body, err := a.Encode("fixture", &Request{ToolChoice: ToolChoice{Mode: ToolChoiceNamed, Name: "decide"}})
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	if v, ok := wire["parallel_tool_calls"]; !ok || v != false {
		t.Fatalf("single decision not enforced: %s", body)
	}
	body, err = a.Encode("fixture", &Request{ToolChoice: ToolChoice{Mode: ToolChoiceAuto}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "parallel_tool_calls") {
		t.Fatal("automatic tool mode was narrowed")
	}
}
