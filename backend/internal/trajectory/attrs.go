package trajectory

// attrs.go is the ONLY place in this repository that spells an OpenTelemetry
// GenAI attribute name.
//
// WHY A MAPPING LAYER RATHER THAN STRING LITERALS AT THE CALL SITES. r8 §1.1:
// the GenAI semantic conventions live in their own repo
// (open-telemetry/semantic-conventions-genai) and are still marked
// **Development**, which is the OTel word for "these names may change without a
// major version". They already have: `gen_ai.system` became
// `gen_ai.provider.name`. If handlers spelled the names inline, the day the
// convention moves is the day we grep the whole backend, miss two, and ship a
// trace store with both spellings in it and no way to tell which era a row came
// from. Here it is one file plus one bumped SemconvVersion literal.
//
// ⛔ EVERY span carries AttrSemconvVersion. A dashboard that has to guess which
// naming era a row belongs to is a dashboard that silently charts half the data.

// SemconvVersion is the GenAI semantic-convention release whose attribute names
// the constants below reproduce. It is emitted verbatim on every span.
//
// The "-dev" suffix is not a typo and not a placeholder: it is the honest label
// for a convention that is not Stable. Bump this literal in the same commit that
// changes any Attr* constant, never separately.
const SemconvVersion = "1.38.0-dev"

// InstrumentationScopeName / InstrumentationVersion identify this hand-rolled
// instrumentation in the OTLP `scope` object, exactly as an SDK-based tracer
// would. A collector operator seeing an unfamiliar attribute needs to be able to
// find the code that emitted it; this is that pointer.
const (
	InstrumentationScopeName = "github.com/khata/cored/internal/trajectory"
	InstrumentationVersion   = "0.1.0"
)

// Ambient attributes — set on EVERY span from the Scope in the context.
//
// tenant.id is the company id. It is the single most important attribute in the
// store: without it a trace row cannot be attributed to a tenant, cannot be
// deleted on a DPDP erasure request, and cannot be excluded from a cross-tenant
// aggregate. Start() therefore flags its absence loudly (AttrScopeMissing)
// rather than emitting an unattributed span that looks fine.
const (
	AttrTenantID       = "tenant.id"
	AttrEnduserID      = "enduser.id"
	AttrEnduserSubject = "enduser.subject"
	AttrHTTPRequestID  = "http.request.id"
	AttrFeature        = "khata.feature"
	AttrAction         = "khata.action"
	AttrResource       = "khata.resource"
	AttrSemconvVersion = "gen_ai.semconv_version"
)

// KindChat attributes — one outbound model call.
//
// AttrGenAIProviderName is set from ModelRequest.System. The field is called
// System because that is what the convention called this concept until it was
// renamed; the WIRE name is the current one. That mismatch is deliberate and is
// exactly the sort of thing this file exists to absorb.
const (
	AttrGenAIOperationName         = "gen_ai.operation.name"
	AttrGenAIProviderName          = "gen_ai.provider.name"
	AttrGenAIRequestModel          = "gen_ai.request.model"
	AttrGenAIRequestTemperature    = "gen_ai.request.temperature"
	AttrGenAIRequestMaxTokens      = "gen_ai.request.max_tokens"
	AttrGenAIOutputType            = "gen_ai.output.type"
	AttrGenAIResponseModel         = "gen_ai.response.model"
	AttrGenAIResponseID            = "gen_ai.response.id"
	AttrGenAIResponseFinishReasons = "gen_ai.response.finish_reasons"
	AttrGenAIUsageInputTokens      = "gen_ai.usage.input_tokens"
	AttrGenAIUsageOutputTokens     = "gen_ai.usage.output_tokens"
)

// khata.* extensions to the chat span.
//
// These five have no `gen_ai.*` equivalent in 1.38.0-dev and are namespaced
// under khata. so that a future convention release cannot collide with them.
//
// ⛔ AttrCachedInputTokens IS A TOKEN COUNT, NOT A COST. There is no
// `*_cost float64` field anywhere in this package and there must never be one:
// the repo's semgrep rule `money-must-not-be-float` is an ERROR on
// (amount|debit|credit|taxable|cgst|sgst|igst|total)\s+float(32|64) under
// backend/**, and more to the point a rupee figure computed from a float token
// price is a rupee figure that disagrees with the invoice. Money is derived
// downstream, from these integers, in paise.
//
// AttrModelFingerprint is r8 §7's drift alarm: providers change weights behind a
// stable model id, so the fingerprint the provider returns is the only evidence
// that yesterday's `glm-5.3-flash` and today's are the same artefact.
//
// AttrPromptHash is SHA-256 of the rendered prompt, lowercase hex — the digest
// PromptHash computes and the one internal/llm's StaticPromptHash computes
// independently (it may not import this package; see PromptHash for why the two
// implementations exist and why they must not diverge). ⛔ It is a hash and not
// the prompt for the reason the package comment gives at length. ⚠️ It is NOT
// Hash/HashString, which are BLAKE3 and are what the TOOL hashes below use: this
// attribute is also a cassette's replay key, and a key computed with two
// different primitives that both render as 64 hex characters fails as a miss
// that names the wrong cause.
const (
	AttrCachedInputTokens    = "khata.usage.cached_input_tokens"
	AttrModelFingerprint     = "khata.model.fingerprint"
	AttrPromptVersion        = "khata.prompt.version"
	AttrPromptHash           = "khata.prompt.hash"
	AttrSchemaRepairAttempts = "khata.schema.repair_attempts"
)

// KindTool attributes — one tool execution.
//
// The two hashes are what makes a cassette replayable: Lookup() keys on them, so
// a replayed run that reaches a tool with arguments the recording never saw
// MISSES and fails the test rather than reaching for the network.
const (
	AttrGenAIToolName   = "gen_ai.tool.name"
	AttrGenAIToolCallID = "gen_ai.tool.call.id"
	AttrGenAIToolType   = "gen_ai.tool.type"
	AttrToolArgsHash    = "khata.tool.args_hash"
	AttrToolResultHash  = "khata.tool.result_hash"
)

// KindAgentStep attributes — one step of the plan-and-execute loop.
const (
	AttrGenAIAgentName      = "gen_ai.agent.name"
	AttrGenAIConversationID = "gen_ai.conversation.id"
)

// Operation-name values. gen_ai.operation.name is a closed enum in the
// convention; these three are the only members this repo emits, and Start()
// derives the value from Kind so a caller cannot pick a fourth.
const (
	OpChat        = "chat"
	OpExecuteTool = "execute_tool"
	OpInvokeAgent = "invoke_agent"
)

// ToolTypeFunction is the only gen_ai.tool.type we emit. The convention also
// allows "extension" and "datastore"; when the action registry grows a tool that
// is genuinely one of those, add the constant here rather than at the call site.
const ToolTypeFunction = "function"

// Diagnostic attributes — this package describing its own degradations.
//
// NEVER A SILENT FALLBACK (the repo rule, modelled on insightd_proxy.go:120-127).
// A span with no Scope in its context is a real defect — some code path started
// a model call outside the middleware that installs the scope — and it must be
// visible IN THE TRACE, not only in a log line nobody greps. AttrScopeMissing is
// how it becomes a filterable count on a dashboard.
const (
	AttrErrorType    = "error.type"
	AttrScopeMissing = "khata.scope.missing"
)

// MaxAttrValueLen caps a string attribute's length.
//
// ⚠️ THIS IS A CONTENT CONTROL, NOT A SIZE OPTIMISATION. Every legitimate string
// this package emits is an id, a hash, a model name or an enum member — none of
// them reaches 512 bytes. A value that does is overwhelmingly likely to be a
// prompt, a completion, a tool result or an error string with a narration in it,
// arriving through the SetString escape hatch. Truncating it silently would put
// the first 512 bytes of a supplier's bill into the trace store and tell nobody;
// replacing it with TruncatedMarker throws the defect onto the dashboard.
const MaxAttrValueLen = 512

// TruncatedMarker replaces an over-long attribute value. It is deliberately
// unmistakable and contains no fragment of the value it replaced.
const TruncatedMarker = "<dropped: value exceeded trajectory.MaxAttrValueLen; content must never be an attribute>"
