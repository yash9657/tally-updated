// Package trajectory is the agent plane's span recorder: every model call, tool
// step and agent step in cored becomes one Record carrying OpenTelemetry GenAI
// (`gen_ai.*`) attributes, exported OTLP/HTTP to a self-hosted Langfuse, and —
// for the Tier-0 CI gate — replayed from checked-in cassettes with no network at
// all. It is PLAN-AGENTIC W0 deliverable D2.
//
// # Why this is stdlib-only, and why that is not laziness
//
// IMPLEMENTATION_PLAN.md §1.1 is binding on this module: "Go (backend/): NO new
// module dependencies". backend/go.mod has had exactly six direct requirements
// since Phase 0. RESEARCH-AGENTIC.md §9.1 and r8 §1.3 recommend "the plain OTel
// Go SDK"; that recommendation loses to §1.1, and it loses CHEAPLY, because the
// only thing we need from the SDK is the wire format. Langfuse's OTLP endpoint
// is HTTP-only and accepts protobuf-JSON (r8 §1.2, and r8:34 "HTTP-only; do not
// plan on gRPC"), so the entire exporter is otlp.go: a POST of a JSON document.
// No protobuf runtime, no gRPC, no vendored SDK, no transitive dependency tree
// under a package that every future agent code path will import.
//
// internal/nlq paid this same price deliberately once already (see its package
// comment). ⛔ Do not "fix" this package by pulling in go.opentelemetry.io.
//
// # Why allowedDeps for this package is the EMPTY map
//
// archtest's allowedDeps (internal/archtest/boundaries_test.go) must carry
// `"internal/trajectory": {}`. Not "not yet decided" — empty on purpose. This
// package is imported by the LLM provider layer, the action layer, the agent
// loop and eventually every handler that wants a span. If it could import a
// sibling, that sibling would be transitively linked into all of them, and the
// first tempting edge (internal/platform/db, "so a span can look up the company
// name") would put a database round-trip on the telemetry path — where an
// outage becomes a voucher write that fails. A telemetry leaf that can only
// reach the stdlib, encoding/json and blake3 cannot do that.
//
// It is also why internal/llm does NOT import this package: it declares the
// narrow Recorder interface it consumes and this package satisfies it
// structurally. internal/server, the composition root, wires the two together.
//
// # ⛔ Why prompt and completion CONTENT is never an attribute
//
// PLAN-AGENTIC §2.7 and r8 §3: the traces of an accounting agent contain ledger
// data. A prompt for "post Acme's September bill" carries a party name, a
// narration, an amount and often a GSTIN; a completion carries the same values
// back. Attributes are the part of a span that fans out — into Langfuse's
// ClickHouse, into any OTel Collector the deployment puts in front of it, into
// every dashboard and every alert payload — and none of those places is inside
// the tenant boundary the DPDP posture draws in RESEARCH-AGENTIC §4 D10.
//
// So the typed setters here are content-free BY CONSTRUCTION. ModelRequest
// carries PromptHash, never the prompt; ToolCall carries ArgsHash and
// ResultHash, never the arguments. The TOOL hashes are BLAKE3 (zeebo/blake3,
// already a dependency and already the ledger's chain hash) and the PROMPT hash
// is SHA-256 (PromptHash — it is also a cassette's replay key and internal/llm,
// which cannot import this package, computes the same digest); either is enough
// to answer the
// two questions a trace is actually asked — "is this the same prompt as the one
// that worked?" and "did the tool see the same arguments on replay?" — without
// being able to answer "what did it say?".
//
// The one place content DOES live is a cassette file (cassette.go), on disk,
// under testdata/, written post-redaction by a caller that had to pass a
// redactor explicitly. TestNoContentEscapesThroughAttributes and
// TestTypedSettersCarryNoContentFields hold the line mechanically.
//
// SetString remains an escape hatch — any string key can be given any string
// value — so it caps values at MaxAttrValueLen and replaces an over-long one
// with a LOUD marker rather than truncating it silently. A pasted prompt does
// not quietly become a 4 KB attribute.
//
// # Semconv version pinning
//
// r8 §1.1: the GenAI semantic conventions are still "Development" status and the
// `gen_ai.*` names WILL change. Every span therefore carries
// gen_ai.semconv_version = SemconvVersion as a literal, and every attribute name
// this repo emits is a constant in attrs.go. When the convention moves, attrs.go
// and SemconvVersion move together and a dashboard can tell the two eras apart.
// ⛔ Do not spell a `gen_ai.*` string anywhere but attrs.go.
package trajectory
