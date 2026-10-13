package llm

// vLLM is our own server on our own GPU (W9: GLM-5.3-Flash / Qwen3.8-27B on
// E2E or Yotta, with XGrammar structured outputs).
//
// ⛔ DefaultBaseURL IS DELIBERATELY EMPTY, AND New REFUSES WHEN Config.BaseURL
// IS ALSO EMPTY. There is no such thing as "the" vLLM host: it is whatever we
// started, on whatever address. A compiled-in localhost:8000 would be the worst
// of both worlds — it would let a misconfigured production deployment boot
// cleanly and then fail every call with a connection refused that reads as an
// outage at the provider, while on a developer laptop it would silently talk to
// whatever else happened to be on that port. internal/server/insightd_proxy.go's
// header makes the same argument for insightd's port: the location IS
// configuration.
//
// ⚠️ AN EMPTY API KEY IS LEGITIMATE HERE AND ONLY HERE. A vLLM started without
// --api-key accepts any bearer, so New does not require a credential for this
// provider — but it does require the base URL, so "no configuration at all"
// still cannot boot.
//
// The grammar goes in `guided_grammar` (vLLM's guided-decoding extra body), NOT
// in response_format. vLLM ignores an unrecognised response_format type, which
// would be an unconstrained generation wearing a constrained request's clothes.
func newVLLM() Adapter {
	return openaiAdapter{
		provider: ProviderVLLM,
		baseURL:  "",
		path:     "/v1/chat/completions",
		caps:     CapJSONSchema | CapGrammar | CapToolChoiceNamed | CapParallelTools,
		grammar:  grammarGuided,
		auth:     authBearer,
	}
}
