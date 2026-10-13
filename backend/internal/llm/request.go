package llm

import (
	"encoding/json"

	"github.com/google/uuid"
)

// Role is the closed set of message roles. A string type rather than a bare
// string so a typo is a compile error: the adapters switch on it exhaustively
// and a fifth role must be a deliberate edit here, not a literal in a caller.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	// RoleTool carries a TOOL RESULT back to the model. It is the multi-turn
	// half of the agent loop (W2) and only the OpenAI-compatible adapters
	// encode it today — adapter_anthropic.go refuses it loudly rather than
	// guessing at a tool_result content block. See that file.
	RoleTool Role = "tool"
)

// Message is one turn. Content is plain text; the structured halves
// (ToolCalls on an assistant turn, ToolCallID on a tool turn) exist so a
// multi-step run can be replayed to the model exactly as it happened, which is
// what "recorded tool results as replayable cassettes" (W0) requires.
type Message struct {
	Role    Role
	Content string

	// ToolCalls echoes back what the model asked for on an assistant turn.
	// ⚠️ It must be echoed VERBATIM, ids included: several providers reject a
	// tool result whose tool_call_id they did not mint, and a loop that
	// re-generates ids "because they are opaque anyway" fails only on the
	// second turn, only in production, and only for the providers that check.
	ToolCalls []ToolCall
	// ToolCallID names which call this message is the result of (RoleTool).
	ToolCallID string
}

// Tool is one callable the model may choose. Parameters is a JSON Schema object,
// carried as RawMessage so it round-trips byte-for-byte — see Request.Static:
// a schema re-marshalled through a map would reorder its keys and break the
// provider's prefix cache on every call.
type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// ToolChoiceMode is how the model is allowed to pick.
type ToolChoiceMode string

const (
	// ToolChoiceUnset is the zero value: send no tool_choice at all and let the
	// provider apply its own default. Distinct from ToolChoiceAuto, which SAYS
	// "auto" on the wire — the difference matters for cache-prefix stability,
	// because adding a field changes the bytes.
	ToolChoiceUnset    ToolChoiceMode = ""
	ToolChoiceAuto     ToolChoiceMode = "auto"
	ToolChoiceNone     ToolChoiceMode = "none"
	ToolChoiceRequired ToolChoiceMode = "required"
	// ToolChoiceNamed forces one specific tool. This is the mode internal/nlq
	// depends on (forced emit_query) and the one D2 calls for on every decision
	// step; an adapter without CapToolChoiceNamed refuses it at Encode.
	ToolChoiceNamed ToolChoiceMode = "named"
)

// ToolChoice pairs the mode with the tool name that ToolChoiceNamed needs.
type ToolChoice struct {
	Mode ToolChoiceMode
	Name string
}

// ResponseSchema is `response_format: {type: "json_schema", ...}`. Strict asks
// the provider to CONSTRAIN decoding to the schema rather than merely prompt for
// it; providers that cannot do that are refused by capability, never downgraded.
//
// ⛔ THE SCHEMA MUST NOT DECLARE A MONEY FIELD THE MODEL FILLS IN. See the
// package header: the model emits references and operations, Go computes paise.
type ResponseSchema struct {
	Name   string
	Strict bool
	Schema json.RawMessage
}

// Request is one call.
//
// ⛔ Static/Dynamic IS THE CACHE CONTRACT, NOT A STYLE CHOICE.
//
// Every provider we use bills a cached prompt prefix at a fraction of a fresh
// one (D3: Fireworks default-on prefix caching, DeepSeek's cached reads), and
// every one of them matches the prefix BYTE FOR BYTE from the start of the
// request. So the ordering rule is not "system prompts usually go first" — it is
// that ONE byte of per-call data in front of the shared prefix invalidates the
// entire cache, silently, for every tenant, and the only symptom is the bill.
//
// Static is what is byte-identical across every call in a Feature: the system
// prompt, the tool schemas, the grammar, the few-shot exemplars for that prompt
// version. Dynamic is everything that changes: today's date, the resolved period
// window, the user's question, the retrieved rows.
//
// ⚠️ THE DATE BELONGS IN Dynamic EVEN THOUGH IT LOOKS LIKE PART OF THE SYSTEM
// PROMPT. That is the mistake this split exists to make impossible to hide, and
// TestComplete_StaticPrefixIsStableAcrossCalls is its detector: it runs the same
// Feature under two different clocks and requires identical leading bytes.
type Request struct {
	// TenantID scopes the budget and the trajectory span. ⛔ IT NEVER REACHES
	// THE WIRE — client.go refuses a body containing it (ErrTenantIDInPrompt).
	TenantID uuid.UUID
	// Feature names the call site ("nlq.translate", "bill.extract"). It keys
	// the budget, the eval suite and the span; an empty one is refused, because
	// an untraceable call is one nobody can regress-test.
	Feature string
	// PromptVersion is bumped by hand whenever Static changes meaningfully. It
	// is what the CI eval gate (W8 Tier 1) pins a score to.
	PromptVersion string

	Static  []Message
	Dynamic []Message

	Tools      []Tool
	ToolChoice ToolChoice
	Schema     *ResponseSchema
	// Grammar is a GBNF/EBNF grammar for constrained decoding (Fireworks
	// grammar mode, vLLM guided_grammar). Providers without CapGrammar refuse
	// it rather than ignoring it — an ignored grammar presents as a flaky
	// model, which is the most expensive possible way to learn about a missing
	// feature flag.
	Grammar string

	MaxTokens   int
	Temperature *float64
	Stop        []string

	// MaxInputBytes bounds the ENCODED request. Zero means
	// DefaultMaxInputBytes. Enforced before any socket is opened, in the shape
	// internal/nlq/translate.go:41 uses for its 500-character cap.
	MaxInputBytes int
}

// messages returns Static followed by Dynamic. The one place the ordering rule
// is implemented, so no adapter can get it wrong independently.
func (r *Request) messages() []Message {
	out := make([]Message, 0, len(r.Static)+len(r.Dynamic))
	out = append(out, r.Static...)
	out = append(out, r.Dynamic...)
	return out
}

// withRepair returns a copy of r with one extra Dynamic turn carrying the
// repair instruction.
//
// ⚠️ IT COPIES THE SLICE RATHER THAN APPENDING TO THE CALLER'S. `append` on a
// slice with spare capacity writes into the caller's backing array — so a caller
// that builds one Request and calls DecodeInto twice would find its second call
// carrying the first call's repair text. The bug is invisible in a single-call
// test and reproducible only under reuse, which is how it survives review.
//
// The repair goes in Dynamic, never Static, for the reason the type documents:
// a repair turn is per-call by definition, and prepending it would evict the
// prefix cache for every subsequent call in the feature.
func (r *Request) withRepair(instruction string) *Request {
	cp := *r
	cp.Dynamic = make([]Message, 0, len(r.Dynamic)+1)
	cp.Dynamic = append(cp.Dynamic, r.Dynamic...)
	cp.Dynamic = append(cp.Dynamic, Message{Role: RoleUser, Content: instruction})
	return &cp
}
