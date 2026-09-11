package llm

import (
	"encoding/json"
	"time"
)

// ToolCall is one call the model asked for. Arguments stays RawMessage: it is
// decoded ONCE, strictly, into the caller's target type (client.go DecodeInto),
// and a pre-decode into map[string]any here would be a second, lenient decode
// that silently accepts the shapes the strict one exists to reject.
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

// Usage is the token accounting for one call. Ints, not floats, and not money —
// see the package header: this package counts TOKENS and never rupees.
//
// CachedPromptTokens is a SUBSET of PromptTokens, not an addition to it. Every
// provider that reports it reports it that way (Fireworks and DeepSeek both nest
// it under prompt_tokens_details), and a caller that added the two would
// over-report input on exactly the calls the cache was working on — i.e. it
// would look worst when it was going best.
type Usage struct {
	PromptTokens       int
	CachedPromptTokens int
	CompletionTokens   int
}

// Total is what the budget settles against. Cached prompt tokens are NOT
// subtracted: they were served, they are billed (at a discount), and a budget
// that under-counted them would let a well-cached feature overrun its cap
// without ever tripping it. The DISCOUNT is a cost question; this is a volume
// cap.
func (u Usage) Total() int { return u.PromptTokens + u.CompletionTokens }

// Fingerprint is who actually answered.
//
// ⛔ IT IS CAPTURED ON EVERY RESPONSE — success, refusal, empty completion —
// and that is the whole point. D3 records that Fireworks gives two weeks'
// deprecation notice and NO weight-immutability promise: the model behind an id
// can change under us. The nightly canary (D9) detects that drift, and it can
// only attribute the drift to a change if every stored trajectory says which
// served model and which system fingerprint produced it. A fingerprint recorded
// only on success is a fingerprint missing from exactly the calls worth
// investigating.
type Fingerprint struct {
	Provider Provider
	// RequestedModel is what we asked for; ServedModel is what the provider
	// says it ran. They differ more often than one would like — aliases,
	// silent upgrades, a gateway rewriting the id.
	RequestedModel    string
	ServedModel       string
	SystemFingerprint string
	ResponseID        string
	// RequestID is the provider's own correlation id from the response
	// headers, which is what their support asks for. It is the one field here
	// that is NOT in the body.
	RequestID string
	// Extra carries provider-specific scraps worth keeping without giving each
	// one a field: a refusal string, a served-by header. Never tenant data.
	Extra map[string]string
}

// Response is one served call.
type Response struct {
	Text         string
	ToolCalls    []ToolCall
	FinishReason string
	Usage        Usage
	Fingerprint  Fingerprint
	// Repairs is how many schema-repair round trips DecodeInto spent before
	// this response. It is on the RESPONSE rather than in a metric because the
	// bound (DefaultMaxRepairs) has to be OBSERVABLE to be trusted: a repair
	// loop whose count nobody can read is a loop whose bound nobody can prove.
	// It also feeds the "schema-repair rate" the plan reports per model (§6).
	Repairs int
	// Latency is measured with the injected clock, so a test can assert on it
	// without sleeping.
	Latency time.Duration
}

// ToolCallNamed returns the first tool call with the given name.
//
// FIRST, not "the only one": several providers emit parallel tool calls, and a
// helper that insisted on exactly one would turn a capability into an error at
// the one call site that is only trying to read a value it asked to be forced.
func (r *Response) ToolCallNamed(name string) (ToolCall, bool) {
	for _, tc := range r.ToolCalls {
		if tc.Name == name {
			return tc, true
		}
	}
	return ToolCall{}, false
}
