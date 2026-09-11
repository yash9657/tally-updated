package llm

import (
	"errors"
	"fmt"
)

// The sentinel taxonomy. Each one names WHOSE FAULT the failure is, because the
// server maps them to different statuses and the on-call response to each is
// different. internal/nlq/translate.go:35 records the original version of this
// argument for one adapter; this is the same split generalised to five providers.

// ErrUpstreamUnavailable is an OPERATOR fault: a network error, a timeout, or
// ANY non-2xx status from the provider — 401 (dead key), 403, 404 (retired model
// id), 413, 429, 5xx, and Anthropic's 529. The caller should retry or fall back
// to a typed path; nobody should be told to rephrase.
//
// ⛔ IT COVERS *EVERY* NON-2XX, WITHOUT EXCEPTION. The tempting refinement — "a
// 400 means the model or the prompt was wrong, so that is a model fault" — is
// how a malformed tool schema we shipped gets reported as flaky inference for a
// week. A 400 is our bug or the provider's; either way it is not the model
// declining, and it is not the tenant's question.
var ErrUpstreamUnavailable = errors.New("llm: provider upstream unavailable")

// ErrNoStructuredOutput is a MODEL fault: the provider answered 2xx and billed
// us, and what came back cannot be turned into the value the caller asked for —
// a refusal, no tool call at all, a tool call whose arguments do not decode, or
// arguments carrying fields the target type does not declare, after the bounded
// repair attempts are spent.
//
// It is deliberately ONE error and not four. The caller's remedy is identical in
// every case (fall back to the typed path, or ask the human), and the diagnostic
// detail belongs in the trajectory span and the wrapped message, not in a
// sentinel the caller would have to switch on.
var ErrNoStructuredOutput = errors.New("llm: response carried no usable structured output")

// ErrBudgetExhausted is a TENANT fault: this company has spent its token budget
// for the period.
//
// ⛔ IT IS NEVER RETRIED AND NEVER SILENTLY DOWNGRADED. Both "corrections" are
// the same defect wearing different clothes — a retry spends the budget it was
// told not to spend, and a quiet fall-back to a cheaper model changes the
// answer's quality without changing anything the tenant can see. A budget that
// bends under load is not a budget; it is a log line. The refusal is loud, it
// reaches the caller, and the caller decides.
var ErrBudgetExhausted = errors.New("llm: tenant token budget exhausted")

// ErrNotConfigured is a DEPLOYMENT fault: New was handed a provider with no
// credential (or, for a self-hosted vLLM, no base URL — there is no default host
// for a server we run ourselves).
//
// It exists so that "the AI features do nothing" has a cause at boot rather than
// a mystery at 3am. internal/server/insightd_proxy.go:120 is the model this
// follows: an empty token is kept EMPTY and refused loudly, never defaulted to
// anything, because there is no safe default for a credential and a compiled-in
// one would be a credential published in a repository.
var ErrNotConfigured = errors.New("llm: no api key configured for provider")

// ErrInputTooLarge is a CALLER fault: the encoded request exceeds
// Request.MaxInputBytes (or DefaultMaxInputBytes).
//
// ⚠️ NOT IN THE ORIGINAL W0 API SKETCH, AND ADDED ON PURPOSE. The alternative
// was to reuse one of the four above, and each mislabels: ErrUpstreamUnavailable
// tells the caller to retry something that will fail identically forever;
// ErrNoStructuredOutput blames a model that was never asked. internal/nlq maps
// its own over-length case to query.ErrUntranslatable (a 422, a user fault) for
// exactly this reason — it needed a fifth meaning too, and borrowed one from the
// package it serves. This package serves several, so it declares its own.
var ErrInputTooLarge = errors.New("llm: request exceeds the configured input byte limit")

// ErrTenantIDInPrompt is a CALLER fault, and the one refusal in this package
// that is a SECURITY control rather than an ergonomic one.
//
// internal/nlq has TestToQuery_NoTenantDataInPrompt asserting the company id
// never reaches the wire. That is a good test and it is still ported here
// (client_test.go) — but a test only covers the prompts somebody thought to
// test, and this package will carry prompts from the agent loop, the insight
// narrator and five channels. So the invariant is also CHECKED, on the encoded
// bytes, immediately before the socket opens: if the tenant's UUID appears in
// the body, the call does not happen.
//
// ⛔ Do not relax this to a warning. A tenant id in a prompt is a cross-tenant
// correlation key sitting in a third party's retention window, and the point of
// catching it on the encoded body — rather than on the Request — is that it
// catches the id no matter which field smuggled it: a message, a tool schema, an
// exemplar, a stringified struct somebody logged into a system prompt.
var ErrTenantIDInPrompt = errors.New("llm: tenant id appears in the request body")

// UpstreamError carries the detail an operator needs to act on an
// ErrUpstreamUnavailable without a packet capture: which provider, what status,
// the provider's own request id (the string their support will ask for), and a
// short, bounded snippet of the body.
//
// ⚠️ Snippet IS BOUNDED AND IT IS NOT LOGGED BY THIS PACKAGE. An error body from
// a provider can echo part of the prompt, and the prompt can carry tenant text.
// Bounding it keeps a 4 MB HTML error page out of a log line; leaving the
// logging decision to the caller keeps this package from being the thing that
// wrote tenant text to stdout.
type UpstreamError struct {
	Provider  Provider
	Status    int
	RequestID string
	Snippet   string
}

func (e *UpstreamError) Error() string {
	if e.RequestID != "" {
		return fmt.Sprintf("llm: %s upstream status %d (request-id %s): %s",
			e.Provider, e.Status, e.RequestID, e.Snippet)
	}
	return fmt.Sprintf("llm: %s upstream status %d: %s", e.Provider, e.Status, e.Snippet)
}

// Unwrap makes errors.Is(err, ErrUpstreamUnavailable) true for every
// UpstreamError. Callers switch on the sentinel and only reach for the struct
// (via errors.As) when they want the status or the request id — so adding a
// field here can never change anybody's control flow.
func (e *UpstreamError) Unwrap() error { return ErrUpstreamUnavailable }

// maxSnippet bounds what we copy out of an error body. Long enough for a JSON
// error envelope, short enough that it cannot be a payload.
const maxSnippet = 512

func snippet(b []byte) string {
	if len(b) > maxSnippet {
		return string(b[:maxSnippet]) + "…"
	}
	return string(b)
}
