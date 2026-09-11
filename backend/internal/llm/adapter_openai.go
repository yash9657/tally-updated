package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

// ---------------------------------------------------------------------------
// The shared OpenAI-compatible /v1/chat/completions encode/decode.
// ---------------------------------------------------------------------------
//
// Four of the five providers speak this shape, and the parts they do NOT share
// are exactly three: the path, the auth header, and where a grammar goes. Each
// is a field on openaiAdapter below, so the wire logic exists once.
//
// ⚠️ "OpenAI-compatible" IS A CLAIM, NOT A CONTRACT, and D3 says so in terms:
// "keep a provider abstraction with per-model golden tests because chat
// templates and tool-call parsers differ". This file is the shared 90%; the
// per-provider files are where the differences are written down, one adapter at
// a time, each with its own table test.

// grammarStyle is where a provider expects a constrained-decoding grammar.
type grammarStyle uint8

const (
	// grammarNone: the provider has no grammar mode. Encode refuses.
	grammarNone grammarStyle = iota
	// grammarResponseFormat: `response_format: {"type":"grammar","grammar":…}`
	// (Fireworks).
	grammarResponseFormat
	// grammarGuided: a top-level `guided_grammar` extra body field (vLLM's
	// guided-decoding backend). ⛔ NOT interchangeable with the above: vLLM
	// ignores an unknown response_format type, which would be a silently
	// unconstrained generation — the exact failure Capability exists to stop.
	grammarGuided
)

// authStyle is how the credential is presented.
type authStyle uint8

const (
	authBearer authStyle = iota // Authorization: Bearer <key>
)

// openaiAdapter is the shared implementation. The per-provider types below embed
// it and override only what genuinely differs.
type openaiAdapter struct {
	provider Provider
	baseURL  string
	path     string
	caps     Capability
	grammar  grammarStyle
	auth     authStyle
}

func (a openaiAdapter) Provider() Provider       { return a.provider }
func (a openaiAdapter) DefaultBaseURL() string   { return a.baseURL }
func (a openaiAdapter) Path() string             { return a.path }
func (a openaiAdapter) Capabilities() Capability { return a.caps }

func (a openaiAdapter) SetAuth(h http.Header, apiKey string) {
	switch a.auth {
	case authBearer:
		// ⚠️ SET EVEN WHEN apiKey IS EMPTY, for vLLM: a self-hosted server
		// started without --api-key accepts any bearer, and one started WITH
		// one must see the header. Sending "Bearer " with nothing after it is
		// the honest encoding of "we were given no credential" — and New has
		// already refused the case where that is a misconfiguration rather
		// than a deliberate open server.
		h.Set("Authorization", "Bearer "+apiKey)
	}
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json")
}

// --- wire shapes: only the fields we send or read ---

// chatRequest's FIELD ORDER IS LOAD-BEARING. encoding/json emits struct fields
// in declaration order, so `model` and `messages` lead every body — which is
// what makes the static prefix the literal leading bytes of the request, and
// what makes TestComplete_StaticPrefixIsStableAcrossCalls a byte comparison
// rather than a semantic one. ⛔ Do not reorder these fields for tidiness: doing
// so moves per-call data (max_tokens, temperature) in front of the shared
// prefix on some encoder version and quietly halves the cache hit rate.
type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`

	Tools          []chatTool `json:"tools,omitempty"`
	ToolChoice     any        `json:"tool_choice,omitempty"`
	ResponseFormat any        `json:"response_format,omitempty"`
	// GuidedGrammar is vLLM's extra-body field. omitempty so it never appears
	// for the providers that would reject an unknown key.
	GuidedGrammar string `json:"guided_grammar,omitempty"`

	MaxTokens   int      `json:"max_tokens,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	Stop        []string `json:"stop,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// Emitted only on the turns that carry them, so an ordinary system/user
	// prefix encodes to exactly the bytes it did last call.
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type chatTool struct {
	Type     string       `json:"type"`
	Function chatToolFunc `json:"function"`
}

type chatToolFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type chatToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function chatToolCallFunc `json:"function"`
}

// Arguments is a STRING on this wire, not an object. That is the OpenAI shape
// and every compatible provider follows it; a model that emits invalid JSON
// inside it produces a decode failure at DecodeInto — which is a MODEL fault
// (ErrNoStructuredOutput), correctly, rather than a transport one.
type chatToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatResponse struct {
	ID                string       `json:"id"`
	Model             string       `json:"model"`
	SystemFingerprint string       `json:"system_fingerprint"`
	Choices           []chatChoice `json:"choices"`
	Usage             chatUsage    `json:"usage"`
}

type chatChoice struct {
	FinishReason string          `json:"finish_reason"`
	Message      chatRespMessage `json:"message"`
}

type chatRespMessage struct {
	Content   string         `json:"content"`
	Refusal   string         `json:"refusal"`
	ToolCalls []chatToolCall `json:"tool_calls"`
}

// chatUsage reads the cached-prefix count from BOTH places providers put it.
// Fireworks and OpenAI nest it under prompt_tokens_details; some compatible
// gateways hoist it to the top level. Reading one and not the other would report
// a 0% cache hit rate on a provider where caching was working perfectly, and the
// conclusion drawn from that number would be to stop paying for it.
type chatUsage struct {
	PromptTokens        int                `json:"prompt_tokens"`
	CompletionTokens    int                `json:"completion_tokens"`
	CachedTokens        int                `json:"cached_tokens"`
	PromptTokensDetails *chatUsageCacheDet `json:"prompt_tokens_details"`
}

type chatUsageCacheDet struct {
	CachedTokens int `json:"cached_tokens"`
}

func (u chatUsage) cached() int {
	n := u.CachedTokens
	if u.PromptTokensDetails != nil && u.PromptTokensDetails.CachedTokens > n {
		n = u.PromptTokensDetails.CachedTokens
	}
	return n
}

// --- Encode ---

func (a openaiAdapter) Encode(model string, req *Request) ([]byte, error) {
	msgs := req.messages()
	out := make([]chatMessage, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case RoleSystem, RoleUser, RoleAssistant, RoleTool:
		default:
			return nil, fmt.Errorf("llm: %s: unknown message role %q", a.provider, m.Role)
		}
		cm := chatMessage{Role: string(m.Role), Content: m.Content, ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			cm.ToolCalls = append(cm.ToolCalls, chatToolCall{
				ID:       tc.ID,
				Type:     "function",
				Function: chatToolCallFunc{Name: tc.Name, Arguments: string(tc.Arguments)},
			})
		}
		out = append(out, cm)
	}

	cr := chatRequest{
		Model:       model,
		Messages:    out,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		Stop:        req.Stop,
	}

	for _, t := range req.Tools {
		cr.Tools = append(cr.Tools, chatTool{
			Type: "function",
			// compactJSON so a schema that arrived pretty-printed encodes to
			// the same bytes as one that did not: the prefix cache matches on
			// bytes, and a caller reformatting its own schema file must not
			// cost every tenant a cache miss.
			Function: chatToolFunc{Name: t.Name, Description: t.Description, Parameters: compactJSON(t.Parameters)},
		})
	}

	switch req.ToolChoice.Mode {
	case ToolChoiceUnset:
		// Send nothing. See ToolChoiceUnset's comment: absence and "auto" are
		// different bytes, and different bytes are a different cache prefix.
	case ToolChoiceAuto, ToolChoiceNone, ToolChoiceRequired:
		cr.ToolChoice = string(req.ToolChoice.Mode)
	case ToolChoiceNamed:
		if !a.caps.Has(CapToolChoiceNamed) {
			return nil, capabilityError(a.provider, a.caps, CapToolChoiceNamed, "force a named tool")
		}
		if req.ToolChoice.Name == "" {
			return nil, fmt.Errorf("llm: %s: ToolChoiceNamed with an empty tool name", a.provider)
		}
		cr.ToolChoice = map[string]any{
			"type":     "function",
			"function": map[string]string{"name": req.ToolChoice.Name},
		}
	default:
		return nil, fmt.Errorf("llm: %s: unknown tool choice mode %q", a.provider, req.ToolChoice.Mode)
	}

	if req.Schema != nil {
		if !a.caps.Has(CapJSONSchema) {
			return nil, capabilityError(a.provider, a.caps, CapJSONSchema, "constrain output to a json_schema")
		}
		if req.Grammar != "" {
			// Both at once is not "belt and braces": providers apply one
			// decoding constraint, so the caller does not know which one is in
			// force, and neither would a reviewer reading the trajectory.
			return nil, fmt.Errorf("llm: %s: Schema and Grammar are mutually exclusive", a.provider)
		}
		cr.ResponseFormat = map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   req.Schema.Name,
				"strict": req.Schema.Strict,
				"schema": compactJSON(req.Schema.Schema),
			},
		}
	}

	if req.Grammar != "" {
		if !a.caps.Has(CapGrammar) || a.grammar == grammarNone {
			return nil, capabilityError(a.provider, a.caps, CapGrammar, "decode against a grammar")
		}
		switch a.grammar {
		case grammarResponseFormat:
			cr.ResponseFormat = map[string]any{"type": "grammar", "grammar": req.Grammar}
		case grammarGuided:
			cr.GuidedGrammar = req.Grammar
		}
	}

	return json.Marshal(cr)
}

// --- Decode ---

func (a openaiAdapter) Decode(status int, hdr http.Header, body []byte) (*Response, error) {
	fp := Fingerprint{Provider: a.provider, RequestID: requestIDFrom(hdr)}

	// ANY non-2xx is an operator fault. ⛔ Including 400 — see
	// ErrUpstreamUnavailable's comment for why "a 400 means the prompt was
	// wrong" is the refinement that costs a week.
	if status < 200 || status > 299 {
		return &Response{Fingerprint: fp}, &UpstreamError{
			Provider:  a.provider,
			Status:    status,
			RequestID: fp.RequestID,
			Snippet:   snippet(body),
		}
	}

	var cr chatResponse
	if err := json.Unmarshal(body, &cr); err != nil {
		// A 2xx whose envelope does not parse is NOT an upstream outage — the
		// call was served and billed. It is "nothing usable came back", which
		// is what ErrNoStructuredOutput means, and DecodeInto may repair it.
		return &Response{Fingerprint: fp}, fmt.Errorf("%w: %s: undecodable 200 envelope: %v",
			ErrNoStructuredOutput, a.provider, err)
	}

	fp.ServedModel = cr.Model
	fp.SystemFingerprint = cr.SystemFingerprint
	fp.ResponseID = cr.ID

	resp := &Response{
		Fingerprint: fp,
		Usage: Usage{
			PromptTokens:       cr.Usage.PromptTokens,
			CachedPromptTokens: cr.Usage.cached(),
			CompletionTokens:   cr.Usage.CompletionTokens,
		},
	}
	// Cache metrics we were not promised are not reported as zero-that-means-
	// something: a provider without CapCacheMetrics simply has no number here,
	// and a dashboard reading 0 for "no data" is how a cache regression hides.
	if !a.caps.Has(CapCacheMetrics) {
		resp.Usage.CachedPromptTokens = 0
	}

	if len(cr.Choices) == 0 {
		// Fingerprint is already populated: this is exactly the refusal-shaped
		// response the Fingerprint doc comment is about.
		return resp, fmt.Errorf("%w: %s: 200 with no choices", ErrNoStructuredOutput, a.provider)
	}
	ch := cr.Choices[0]
	resp.FinishReason = ch.FinishReason
	resp.Text = ch.Message.Content
	if ch.Message.Refusal != "" {
		if resp.Fingerprint.Extra == nil {
			resp.Fingerprint.Extra = map[string]string{}
		}
		resp.Fingerprint.Extra["refusal"] = ch.Message.Refusal
	}
	for _, tc := range ch.Message.ToolCalls {
		resp.ToolCalls = append(resp.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: json.RawMessage(tc.Function.Arguments),
		})
	}
	if !a.caps.Has(CapParallelTools) && len(resp.ToolCalls) > 1 {
		// Keep the first and say so, rather than pretending a provider we do
		// not believe supports parallel calls just did. The trajectory records
		// the discrepancy; nothing is silently discarded without a trace.
		if resp.Fingerprint.Extra == nil {
			resp.Fingerprint.Extra = map[string]string{}
		}
		resp.Fingerprint.Extra["dropped_parallel_tool_calls"] = fmt.Sprint(len(resp.ToolCalls) - 1)
		resp.ToolCalls = resp.ToolCalls[:1]
	}
	return resp, nil
}

// requestIDFrom reads the provider's correlation id. Two spellings, because the
// OpenAI-compatible hosts use x-request-id and Anthropic uses request-id, and an
// operator opening a support ticket needs whichever one their provider minted.
func requestIDFrom(hdr http.Header) string {
	if hdr == nil {
		return ""
	}
	if v := hdr.Get("x-request-id"); v != "" {
		return v
	}
	return hdr.Get("request-id")
}

// compactJSON normalises caller-supplied raw JSON so Encode's output depends on
// the schema's CONTENT and not on its formatting. An invalid fragment is passed
// through unchanged rather than swallowed — json.Marshal will surface it, and a
// helper that silently emptied a malformed schema would send an unconstrained
// request that looked constrained.
func compactJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return raw
	}
	return json.RawMessage(buf.Bytes())
}
