package llm

// Fireworks is the D3 primary for the Tier-A planner. It is the only provider in
// this set with ALL FIVE capabilities: json_schema, grammar mode (GBNF), named
// tool_choice, parallel tool calls, and prefix-cache accounting in usage.
//
// ⚠️ THE BASE URL ENDS IN /inference AND THE PATH BEGINS AT /v1. That looks like
// a typo next to the other three and is not: Fireworks serves the
// OpenAI-compatible surface under an /inference prefix, so a "tidied" base of
// https://api.fireworks.ai answers 404 for every call — an
// ErrUpstreamUnavailable that reads exactly like a retired model id. This is why
// Path() is on the Adapter interface rather than being a package constant.
//
// ⚠️ THE GRAMMAR GOES IN response_format, NOT IN guided_grammar. Sending vLLM's
// spelling here would be accepted-and-ignored, i.e. an unconstrained generation
// that the caller believes is constrained. grammarStyle exists for this one
// difference.
func newFireworks() Adapter {
	return openaiAdapter{
		provider: ProviderFireworks,
		baseURL:  "https://api.fireworks.ai/inference",
		path:     "/v1/chat/completions",
		caps: CapJSONSchema | CapGrammar | CapToolChoiceNamed |
			CapParallelTools | CapCacheMetrics,
		grammar: grammarResponseFormat,
		auth:    authBearer,
	}
}
