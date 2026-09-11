// Package llm is cored's provider abstraction: one Client that speaks to
// Fireworks, DeepInfra, Sarvam, a self-hosted vLLM, or the Anthropic Messages
// API, behind one Go API, with per-tenant token budgets and a trajectory seam.
//
// It is PLAN-AGENTIC.md W0 deliverable D1. Everything above it (nlq, the agent
// loop, the insight narrator) talks to this package; nothing in this package
// talks back up.
//
// # STDLIB-ONLY (deliberate, disclosed)
//
// Per IMPLEMENTATION_PLAN.md §1.1 — "Go (backend/): NO new module dependencies"
// — this package speaks /v1/chat/completions and /v1/messages over net/http with
// no vendor SDK. That is a conscious deviation from the usual "use the official
// SDK" guidance, taken for the same reason internal/nlq took it (translate.go:13):
// the whole wire surface we need is four JSON objects, and an SDK per provider
// would be five new module graphs to keep pinned, audited and India-resident.
// ⛔ Do not "fix" it by pulling in an SDK. `go build ./...` must keep succeeding
// with GOPROXY=off.
//
// # WHY THIS PACKAGE MAY NEVER LOOK A FACT UP FOR ITSELF
//
// A Request arrives fully formed: the caller has already resolved the period
// windows, chosen the exemplars, redacted the PII and built the prompt. This
// package adds nothing to it except the model id. That is the property that
// makes an LLM call reproducible, and it is worth stating because the tempting
// alternative — "just let the client fetch the tenant's fiscal year / today's
// date / the catalogue" — is what turns a trajectory into a recording that
// cannot be replayed. Two consequences follow, and both are enforced:
//
//   - The clock is INJECTED (Config.Now). internal/nlq/translate.go:66 spells
//     out why: a caller with no clock has to guess what "last quarter" means,
//     and a wall-clock read inside the request path makes the prompt
//     irreproducible. Here the clock's job is span timestamps and latency;
//     the prompt's own dates are the CALLER's, in Request.Dynamic.
//   - There is no database handle on the Client. The only db edge in the
//     package is Budget's (budget.go), and it counts tokens — it never reads a
//     ledger, a party or a voucher. archtest pins the edge set to
//     {internal/platform/db}.
//
// # ⛔ NO LLM-COMPUTED MONEY (PLAN-AGENTIC §2.2)
//
// A json_schema or tool schema a caller registers here MUST NOT contain a field
// the model computes as money. The model emits REFERENCES and OPERATIONS —
// ledger ids, voucher ids, rates by name, "apply 18% IGST to line 3" — and Go
// computes the rupees, in shopspring/decimal or paise integers, in the same
// validators the F8/F9 forms already run.
//
// The failure this forbids is not "the model is bad at arithmetic". It is that a
// number the model produced is INDISTINGUISHABLE, downstream, from a number the
// ledger produced: it lands in the same JSON field, prints on the same invoice
// and reconciles against nothing. A `total` the model filled in is a figure with
// no provenance, and the number-provenance check in W4 exists precisely because
// such a figure reads as authoritative forever. Schemas here therefore carry
// identifiers, enums, quantities-as-entered and free text — and every rupee is
// computed on this side of the wire.
//
// # SAFETY MODEL
//
// This package validates SHAPE, never MEANING. A decoded tool call is a
// hypothesis; the deterministic engine that receives it (query.Compile, the
// voucher validators, the GST rate table) remains the sole authority on whether
// it is legal, exactly as internal/nlq's header records for the DSL. So a
// hallucinated ledger name or an injection payload smuggled into a string field
// is somebody else's rejection, not this package's — and this package must never
// grow a whitelist of its own, because a second authority drifts from the first.
//
// What IS enforced here, because nothing downstream can see it:
//
//   - The tenant id never reaches the wire (client.go, ErrTenantIDInPrompt).
//   - Oversized input is refused BEFORE any socket is opened.
//   - A capability the provider does not have is refused at Encode rather than
//     silently dropped, so "the grammar was ignored" cannot present as "the
//     model is unreliable" (provider.go).
//   - Reads are bounded at MaxResponseBytes.
//   - Every token spent is reserved before the call and settled after it, on
//     every exit path including panic and context cancellation (budget.go).
//
// # ERROR TAXONOMY IN ONE LINE
//
// ErrUpstreamUnavailable is an OPERATOR fault (retry, fix the key, page someone);
// ErrNoStructuredOutput is a MODEL fault (the call was served and produced
// nothing usable); ErrBudgetExhausted is a TENANT fault and is never retried nor
// silently downgraded to a cheaper model; ErrNotConfigured, ErrInputTooLarge and
// ErrTenantIDInPrompt are CALLER/DEPLOYMENT faults and are all refusals before
// the network. Mislabelling a dead API key as "the model refused" is the exact
// confusion this split exists to prevent — see errors.go.
package llm
