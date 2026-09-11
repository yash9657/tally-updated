package llm

import (
	"fmt"
	"net/http"
	"strings"
)

// Provider is the closed set of inference providers cored knows how to talk to.
// A string type so it can be stored and logged, closed by AdapterFor so an
// unknown value is a refusal at New rather than a nil dereference in a handler.
type Provider string

const (
	// ProviderFireworks is the D3 primary for the Tier-A planner: grammar mode,
	// default-on prefix caching, Batch at 50%.
	ProviderFireworks Provider = "fireworks"
	// ProviderDeepInfra is the cheap secondary.
	ProviderDeepInfra Provider = "deepinfra"
	// ProviderSarvam is the India-hosted surface (D3 B-Indic, W9 residency).
	ProviderSarvam Provider = "sarvam"
	// ProviderVLLM is a self-hosted vLLM on our own GPU. It has NO default
	// base URL, deliberately — see vllmAdapter.
	ProviderVLLM Provider = "vllm"
	// ProviderAnthropic keeps the Messages API alive behind this same Client.
	//
	// ⚠️ IT IS HERE FOR A MIGRATION, NOT A PREFERENCE. internal/nlq speaks the
	// Messages API today and every deployment with an ANTHROPIC_API_KEY set has
	// a working NL→DSL translator. When lane F re-points nlq at this package,
	// that deployment must keep working on the same key and the same model id —
	// otherwise "we refactored the LLM layer" reads, in the field, as "NL query
	// stopped answering". PLAN-AGENTIC §5 keeps Claude as a consent-gated
	// fallback anyway, so this adapter is not dead weight after the migration.
	ProviderAnthropic Provider = "anthropic"
)

// Capability is a bit set of what a provider's chat surface can actually do.
//
// ⛔ IT EXISTS SO THAT AN UNSUPPORTED FEATURE IS A REFUSAL AND NOT A SILENT
// DROP. The alternative — encode what the provider understands and quietly omit
// the rest — produces the worst failure mode in this whole package: a grammar or
// a json_schema that was ignored presents as a model that "sometimes returns
// prose", which is indistinguishable from an unreliable model and gets
// diagnosed by lowering the temperature for a fortnight. Encode returns an error
// instead, at the first call, naming the provider and the missing capability.
type Capability uint8

const (
	// CapJSONSchema: response_format {type: "json_schema"} with real
	// constrained decoding, not "we put the schema in the prompt".
	CapJSONSchema Capability = 1 << iota
	// CapGrammar: GBNF/EBNF constrained decoding.
	CapGrammar
	// CapToolChoiceNamed: forcing one specific tool by name.
	CapToolChoiceNamed
	// CapParallelTools: more than one tool call in a single response.
	CapParallelTools
	// CapCacheMetrics: the response reports how much of the prompt was served
	// from a cached prefix. Without it, Usage.CachedPromptTokens is always 0 —
	// and the Static/Dynamic split's payoff is unmeasurable on that provider,
	// which is worth knowing before choosing it for a high-volume feature.
	CapCacheMetrics
)

// Has reports whether c includes every bit in want.
func (c Capability) Has(want Capability) bool { return c&want == want }

// String renders the set for error messages. Ordinary code should use Has.
func (c Capability) String() string {
	names := []struct {
		bit  Capability
		name string
	}{
		{CapJSONSchema, "json_schema"},
		{CapGrammar, "grammar"},
		{CapToolChoiceNamed, "tool_choice_named"},
		{CapParallelTools, "parallel_tools"},
		{CapCacheMetrics, "cache_metrics"},
	}
	var out []string
	for _, n := range names {
		if c&n.bit != 0 {
			out = append(out, n.name)
		}
	}
	if len(out) == 0 {
		return "none"
	}
	return strings.Join(out, "|")
}

// Adapter is the per-provider seam.
//
// ⭐ Encode AND Decode ARE PURE — no context, no *http.Client, no I/O of any
// kind. That is the design decision the whole test suite rests on: every
// provider's wire shape becomes a table test over ([]byte in, []byte out) with
// no httptest server, no goroutine and no timing, so "does Fireworks put the
// grammar in response_format and vLLM in guided_grammar?" is answered by a
// string comparison rather than by a mock that could be wrong in the same
// direction as the code.
//
// The Client owns the transport; an Adapter owns the bytes.
type Adapter interface {
	Provider() Provider
	// DefaultBaseURL is the host used when Config.BaseURL is empty. It may be
	// "" for a provider that HAS no sensible default (vLLM), in which case New
	// refuses rather than inventing one.
	DefaultBaseURL() string
	// Path is the request path, joined to the base URL. It differs across
	// nominally "OpenAI-compatible" providers — which is precisely why it is
	// on the interface and not a constant.
	Path() string
	SetAuth(h http.Header, apiKey string)
	Capabilities() Capability
	Encode(model string, req *Request) ([]byte, error)
	Decode(status int, hdr http.Header, body []byte) (*Response, error)
}

// adapters is the registry. A map literal rather than a switch so
// TestEveryProviderHasAnAdapter can enumerate it and prove no constant above is
// missing an implementation — a switch would let a new Provider const compile
// and fail only at the first call.
var adapters = map[Provider]Adapter{
	ProviderFireworks: newFireworks(),
	ProviderDeepInfra: newDeepInfra(),
	ProviderSarvam:    newSarvam(),
	ProviderVLLM:      newVLLM(),
	ProviderAnthropic: newAnthropic(),
}

// KnownProviders is the closed vocabulary, in a stable order. Exported for the
// completeness test and for whatever enumerates providers in config validation.
var KnownProviders = []Provider{
	ProviderFireworks,
	ProviderDeepInfra,
	ProviderSarvam,
	ProviderVLLM,
	ProviderAnthropic,
}

// AdapterFor returns the adapter for p. The bool is false for an unknown
// provider — callers turn that into ErrNotConfigured at New, which is the only
// place a provider string crosses from configuration into code.
func AdapterFor(p Provider) (Adapter, bool) {
	a, ok := adapters[p]
	return a, ok
}

// capabilityError is the loud refusal Capability exists to produce.
func capabilityError(p Provider, have Capability, want Capability, what string) error {
	return fmt.Errorf("llm: provider %s cannot %s (capabilities: %s) — "+
		"choose a provider that can, or drop the requirement; it will NOT be silently ignored",
		p, what, have)
}
