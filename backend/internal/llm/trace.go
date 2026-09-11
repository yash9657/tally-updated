package llm

import (
	"context"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// The trajectory seam.
// ---------------------------------------------------------------------------
//
// ⛔ THESE ARE INTERFACES DECLARED HERE RATHER THAN AN IMPORT OF
// internal/trajectory, AND THAT IS WHAT KEEPS THIS PACKAGE A LEAF.
//
// internal/trajectory owns the OTLP/HTTP exporter, the gen_ai.* attribute
// vocabulary, the Langfuse endpoint and the cassette store. None of that is this
// package's business, and an import edge would drag every one of those concerns
// into anything that wants to make an LLM call — including a test, which would
// then need a telemetry package to assert on a tool call.
//
// So this package declares the narrow interface it CONSUMES; internal/trajectory
// satisfies it structurally; internal/server, the composition root, wires the
// two together. Accept interfaces, return structs — and archtest holds the line:
// internal/llm's allowed dependency set is {internal/platform/db} and does not
// contain internal/trajectory.
//
// The corollary is a rule, not a preference: ⛔ do NOT widen these interfaces to
// match whatever internal/trajectory happens to expose. Every method added here
// is a method every future Recorder must implement, and the moment the seam is
// wide enough to be inconvenient somebody will "simplify" it into an import.

// SpanStart is everything the trajectory logger needs to open a span, and
// nothing more.
//
// ⚠️ PromptHash is the hash of the STATIC prefix — the system prompt, the tool
// schemas, the grammar — and it is separate from PromptVersion on purpose.
// PromptVersion is what a human bumped; PromptHash is what actually went over
// the wire. When an eval score moves and the version did not, the hash is the
// evidence that somebody edited a prompt without bumping it, which is the single
// most common way an LLMOps gate is defeated by accident.
//
// ⛔ NO PROMPT TEXT AND NO TENANT DATA CROSSES THIS SEAM. TenantID is here
// because the trace store is tenant-scoped; the prompt itself is not, because
// the redaction pipeline (D9) runs before egress and this package is not the
// place that decides what has been redacted.
type SpanStart struct {
	TenantID      uuid.UUID
	Feature       string
	PromptVersion string
	PromptHash    string
	Provider      Provider
	ModelID       string
}

// LLMSpan is one open span. End is called EXACTLY ONCE per Complete, from a
// defer, so it runs on the error and panic paths too — a trajectory that records
// only successful calls is a trajectory that cannot explain an incident.
//
// resp may be non-nil WITH a non-nil err: that is the refusal case, where the
// call was served and billed and the fingerprint is worth keeping. It may also
// be nil with a non-nil err (a transport failure, where there is nothing to
// record but the error).
type LLMSpan interface {
	End(resp *Response, err error)
}

// Recorder is the trajectory seam. It returns a derived context so an
// implementation can propagate its own span context to nested work without this
// package knowing anything about how.
type Recorder interface {
	StartLLMCall(ctx context.Context, s SpanStart) (context.Context, LLMSpan)
}

// DiscardRecorder is the default when Config.Recorder is nil.
//
// ⚠️ IT IS A REAL TYPE AND NOT A nil CHECK SCATTERED THROUGH client.go. Two
// reasons, and the second is the one that matters: a nil-check per call site is
// one forgotten `if` away from a nil dereference on the panic path, where the
// deferred End runs; and a named type makes "this deployment is not recording
// trajectories" a thing the wiring code says out loud rather than an absence.
//
// ⚠️ IT IS NOT A SILENT DEGRADATION. Discarding trajectories is fine in a unit
// test and NOT fine in production, so internal/server is expected to log when it
// wires this one — the same discipline insightd_proxy.go applies to its optional
// knobs. This package cannot make that decision for the composition root, so it
// documents it here instead of pretending the default is always right.
type DiscardRecorder struct{}

// StartLLMCall returns the context unchanged and a span that does nothing.
func (DiscardRecorder) StartLLMCall(ctx context.Context, _ SpanStart) (context.Context, LLMSpan) {
	return ctx, discardSpan{}
}

type discardSpan struct{}

func (discardSpan) End(*Response, error) {}

// Compile-time proof that the default satisfies the seam. If someone widens
// Recorder, this line is the first thing that breaks — which is the intent.
var _ Recorder = DiscardRecorder{}
