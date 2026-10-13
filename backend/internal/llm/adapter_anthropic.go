package llm

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// ---------------------------------------------------------------------------
// The Anthropic Messages API, behind the same Client.
// ---------------------------------------------------------------------------
//
// ⚠️ WHY THIS FILE EXISTS AT ALL, GIVEN D3 CHOSE OPEN WEIGHTS.
//
// internal/nlq speaks this wire shape today, and every deployment that has an
// ANTHROPIC_API_KEY set has a working NL→DSL translator built on it. When lane F
// re-points nlq at this package, that deployment must keep answering on the same
// key and the same model id. Without this adapter, "we refactored the LLM layer"
// would present in the field as "NL query stopped working the day we upgraded",
// with the cause three packages away from the symptom. PLAN-AGENTIC §5 also
// keeps Claude as a consent-gated fallback for the ~2% of planner tasks that
// fail validation twice, so this is not a compatibility shim with an expiry date.
//
// ⛔ IT IS NOT AN openaiAdapter WITH DIFFERENT CONSTANTS. The Messages API
// differs in four ways that all matter, and each one is a place where a
// "compatible enough" translation would produce a request the API accepts and
// answers wrongly:
//
//  1. the system prompt is a TOP-LEVEL FIELD, not a message with role "system";
//  2. max_tokens is REQUIRED, not optional;
//  3. tools carry `input_schema`, not `function.parameters`, and tool calls come
//     back as content BLOCKS rather than a tool_calls array;
//  4. there is no response_format at all — structured output is forced tool use,
//     which is exactly what internal/nlq does today.
//
// (4) is why this adapter does NOT claim CapJSONSchema. A caller that hands it a
// Schema gets a loud refusal naming the provider, rather than a request that
// quietly drops the constraint and a model that "sometimes returns prose".

const anthropicVersionHeader = "2023-06-01"

// anthropicDefaultMaxTokens is used only when the caller set none. The Messages
// API rejects a request without max_tokens, so SOMETHING has to be chosen — and
// choosing loudly here beats letting every caller discover the 400. It is
// deliberately generous: a truncated tool call is an ErrNoStructuredOutput that
// costs a repair round trip, which is more expensive than the tokens saved.
const anthropicDefaultMaxTokens = 4096

type anthropicAdapter struct{}

func newAnthropic() Adapter { return anthropicAdapter{} }

func (anthropicAdapter) Provider() Provider     { return ProviderAnthropic }
func (anthropicAdapter) DefaultBaseURL() string { return "https://api.anthropic.com" }
func (anthropicAdapter) Path() string           { return "/v1/messages" }

// Capabilities: no json_schema (see (4) above), but named tool choice — which is
// the forced-tool-use path internal/nlq relies on — parallel tool calls, and
// cache accounting (cache_read_input_tokens).
func (anthropicAdapter) Capabilities() Capability {
	return CapToolChoiceNamed | CapParallelTools | CapCacheMetrics
}

// SetAuth writes the two headers this API needs. ⛔ x-api-key, NOT
// Authorization: Bearer — the Messages API ignores a bearer token and answers
// 401, and the anthropic-version header is mandatory. Copied from
// internal/nlq/translate.go:184, which is the implementation this replaces.
func (anthropicAdapter) SetAuth(h http.Header, apiKey string) {
	h.Set("x-api-key", apiKey)
	h.Set("anthropic-version", anthropicVersionHeader)
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json")
}

// --- wire shapes ---

// Field order is load-bearing for the same reason chatRequest's is: model and
// system lead, then messages. Anthropic's prompt cache is explicit (cache_control
// breakpoints) rather than automatic, but the Static/Dynamic ordering still has
// to hold for the day a caller adds one.
type anthropicRequest struct {
	Model      string             `json:"model"`
	System     string             `json:"system,omitempty"`
	Messages   []anthropicMessage `json:"messages"`
	Tools      []anthropicTool    `json:"tools,omitempty"`
	ToolChoice any                `json:"tool_choice,omitempty"`
	MaxTokens  int                `json:"max_tokens"`
	// Temperature is a pointer on Request so "unset" and "0" are different;
	// 0 is a legitimate and common setting for tool steps (D2).
	Temperature *float64 `json:"temperature,omitempty"`
	Stop        []string `json:"stop_sequences,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicResponse struct {
	ID         string                  `json:"id"`
	Model      string                  `json:"model"`
	StopReason string                  `json:"stop_reason"`
	Content    []anthropicContentBlock `json:"content"`
	Usage      anthropicUsage          `json:"usage"`
}

type anthropicContentBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Text  string          `json:"text"`
	Input json.RawMessage `json:"input"`
}

type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

// --- Encode ---

func (a anthropicAdapter) Encode(model string, req *Request) ([]byte, error) {
	ar := anthropicRequest{
		Model:       model,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		Stop:        req.Stop,
	}
	if ar.MaxTokens == 0 {
		ar.MaxTokens = anthropicDefaultMaxTokens
	}

	for _, m := range req.messages() {
		switch m.Role {
		case RoleSystem:
			// Concatenated rather than overwritten: a caller with two system
			// turns means both, and silently keeping the last one would drop
			// half a prompt with no error anywhere.
			if ar.System != "" {
				ar.System += "\n\n"
			}
			ar.System += m.Content
		case RoleUser, RoleAssistant:
			ar.Messages = append(ar.Messages, anthropicMessage{Role: string(m.Role), Content: m.Content})
		case RoleTool:
			// ⛔ A LOUD REFUSAL RATHER THAN A GUESS. The Messages API carries a
			// tool result as a `tool_result` CONTENT BLOCK inside a user turn,
			// keyed by tool_use_id — not as a message with role "tool". A
			// best-effort translation to a user message would be accepted by
			// the API and would break the model's tool-call bookkeeping on the
			// SECOND turn only, which is the hardest kind of bug to attribute.
			//
			// Nothing in W0 sends a tool result: internal/nlq is single-turn
			// forced tool use. The agent loop (W2) is where multi-turn arrives,
			// and that is the lane that should implement the content-block
			// encoding — with a test that replays a real two-turn trajectory.
			return nil, fmt.Errorf("llm: %s: RoleTool is not encoded yet — the Messages API "+
				"needs a tool_result content block, not a role; implement it with the W2 agent loop",
				ProviderAnthropic)
		default:
			return nil, fmt.Errorf("llm: %s: unknown message role %q", ProviderAnthropic, m.Role)
		}
	}

	for _, t := range req.Tools {
		ar.Tools = append(ar.Tools, anthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: compactJSON(t.Parameters),
		})
	}

	// ⛔ BOTH REFUSALS ASK Capabilities(), THEY DO NOT RESTATE IT. They used to
	// be unconditional `if req.Schema != nil { refuse }`, which happened to be
	// right and made the bitmask decorative: an adapter could claim
	// CapJSONSchema|CapGrammar and every test in the package still passed, so the
	// declaration the Client and every caller reason about was pinned by nothing.
	// Reading the mask here is what makes an over-claim a behaviour change and
	// therefore a test failure (TestProviderCapabilitiesAreExactlyDeclared).
	if req.Schema != nil && !a.Capabilities().Has(CapJSONSchema) {
		return nil, capabilityError(ProviderAnthropic, a.Capabilities(), CapJSONSchema,
			"constrain output to a json_schema (use a forced tool call instead)")
	}
	if req.Grammar != "" && !a.Capabilities().Has(CapGrammar) {
		return nil, capabilityError(ProviderAnthropic, a.Capabilities(), CapGrammar,
			"decode against a grammar")
	}

	switch req.ToolChoice.Mode {
	case ToolChoiceUnset:
	case ToolChoiceAuto:
		ar.ToolChoice = map[string]string{"type": "auto"}
	case ToolChoiceNone:
		ar.ToolChoice = map[string]string{"type": "none"}
	case ToolChoiceRequired:
		// "any" is this API's spelling of "you must call some tool".
		ar.ToolChoice = map[string]string{"type": "any"}
	case ToolChoiceNamed:
		if req.ToolChoice.Name == "" {
			return nil, fmt.Errorf("llm: %s: ToolChoiceNamed with an empty tool name", ProviderAnthropic)
		}
		ar.ToolChoice = map[string]string{"type": "tool", "name": req.ToolChoice.Name}
	default:
		return nil, fmt.Errorf("llm: %s: unknown tool choice mode %q", ProviderAnthropic, req.ToolChoice.Mode)
	}

	return json.Marshal(ar)
}

// --- Decode ---

func (a anthropicAdapter) Decode(status int, hdr http.Header, body []byte) (*Response, error) {
	fp := Fingerprint{Provider: ProviderAnthropic, RequestID: requestIDFrom(hdr)}

	// Every non-2xx, including 529 overloaded_error — the same rule and the
	// same reason as the OpenAI-compatible adapter.
	if status < 200 || status > 299 {
		return &Response{Fingerprint: fp}, &UpstreamError{
			Provider:  ProviderAnthropic,
			Status:    status,
			RequestID: fp.RequestID,
			Snippet:   snippet(body),
		}
	}

	var ar anthropicResponse
	if err := json.Unmarshal(body, &ar); err != nil {
		return &Response{Fingerprint: fp}, fmt.Errorf("%w: %s: undecodable 200 envelope: %v",
			ErrNoStructuredOutput, ProviderAnthropic, err)
	}

	fp.ServedModel = ar.Model
	fp.ResponseID = ar.ID

	resp := &Response{
		Fingerprint:  fp,
		FinishReason: ar.StopReason,
		Usage: Usage{
			PromptTokens: ar.Usage.InputTokens,
			// ⚠️ cache_read, NOT cache_creation. A creation write is input
			// billed at a PREMIUM; counting it as "cached" would make the
			// first call of every prompt version look like the cheapest one.
			CachedPromptTokens: ar.Usage.CacheReadInputTokens,
			CompletionTokens:   ar.Usage.OutputTokens,
		},
	}

	for _, blk := range ar.Content {
		switch blk.Type {
		case "text":
			resp.Text += blk.Text
		case "tool_use":
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{
				ID:        blk.ID,
				Name:      blk.Name,
				Arguments: blk.Input,
			})
		}
	}

	// A refusal is a 200 with stop_reason "refusal" and no tool_use block. It
	// is recorded on the FINGERPRINT — which is populated above, before this
	// branch — and reported as a model fault, exactly as
	// internal/nlq/translate.go maps it today.
	if ar.StopReason == "refusal" {
		if resp.Fingerprint.Extra == nil {
			resp.Fingerprint.Extra = map[string]string{}
		}
		resp.Fingerprint.Extra["refusal"] = resp.Text
		return resp, fmt.Errorf("%w: %s: stop_reason refusal", ErrNoStructuredOutput, ProviderAnthropic)
	}
	if len(ar.Content) == 0 {
		return resp, fmt.Errorf("%w: %s: 200 with no content blocks", ErrNoStructuredOutput, ProviderAnthropic)
	}
	return resp, nil
}
