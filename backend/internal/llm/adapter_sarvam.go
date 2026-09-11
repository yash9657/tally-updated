package llm

// Sarvam is the India-hosted surface: D3's B-Indic tier (Sarvam-105B, the only
// open model trained natively on romanised Hinglish) and W9's residency path,
// which matters before the 13 May 2027 DPDP enforcement date rather than after.
//
// ⛔ NO CapParallelTools AND NO CapGrammar — the same rule adapter_deepinfra.go
// states: an unverified capability is not claimed. The consequence here is
// visible and intended: a caller that needs a grammar cannot silently get an
// Indic-hosted unconstrained generation instead, it gets a refusal naming
// Sarvam.
//
// ⚠️ AUTH IS `Authorization: Bearer`, WHICH IS *NOT* THE HEADER SARVAM'S OTHER
// APIS USE. Sarvam's speech and translation endpoints authenticate with
// `api-subscription-key`; the OpenAI-compatible chat surface takes a bearer
// token. If a deployment sees 401s from Sarvam and 200s from every other
// provider on the same key material, THIS is the line to check first — and the
// fix is to change authStyle here after confirming against the live API, not to
// send the credential in both headers on the theory that one of them will
// stick.
func newSarvam() Adapter {
	return openaiAdapter{
		provider: ProviderSarvam,
		baseURL:  "https://api.sarvam.ai",
		path:     "/v1/chat/completions",
		caps:     CapJSONSchema | CapToolChoiceNamed,
		grammar:  grammarNone,
		auth:     authBearer,
	}
}
