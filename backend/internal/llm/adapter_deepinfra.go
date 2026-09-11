package llm

// DeepInfra is the cheap secondary (D3: it undercuts Fireworks on 30 of 38
// shared models). Its OpenAI-compatible surface lives UNDER an /v1/openai prefix
// — the same shape-of-mistake as Fireworks' /inference, in a different place,
// which is the argument for Path() being per-adapter in one sentence.
//
// ⛔ NO CapGrammar AND NO CapCacheMetrics, AND BOTH OMISSIONS ARE DELIBERATE
// RATHER THAN UNRESEARCHED. Claiming a capability we have not verified is
// strictly worse than omitting one we have: an over-claim ships a silently
// unconstrained generation to production, while an under-claim produces a loud
// refusal at the first call with the provider and the missing feature named in
// it. W1's bake-off measures these providers head to head; if DeepInfra's
// grammar support and cached-token reporting check out there, the bits get added
// HERE, with the measurement cited, and adapter_deepinfra_test.go's table is the
// thing that changes.
func newDeepInfra() Adapter {
	return openaiAdapter{
		provider: ProviderDeepInfra,
		baseURL:  "https://api.deepinfra.com",
		path:     "/v1/openai/chat/completions",
		caps:     CapJSONSchema | CapToolChoiceNamed | CapParallelTools,
		grammar:  grammarNone,
		auth:     authBearer,
	}
}
