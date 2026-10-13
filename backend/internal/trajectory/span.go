package trajectory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Scope is the ambient identity every span in a request inherits.
//
// It is carried in the context, not passed as an argument, because the callers
// that create spans (internal/llm, the tool implementations, the agent loop) are
// four or five frames below the middleware that knows who the request belongs
// to, and a Scope parameter threaded through all of them is a parameter someone
// eventually forgets to pass — producing an unattributed trace row that looks
// exactly like a correct one.
type Scope struct {
	// TenantID is the company id (db.CompanyFromContext). It is the partition
	// key of the whole trace store and the unit of a DPDP erasure request.
	TenantID uuid.UUID

	// UserID is uuid.Nil for a `svc:` principal, and that is NOT an error.
	// insightd's draft proxy and the statutory workers authenticate as services;
	// they have a subject and no user row. Start() therefore OMITS enduser.id
	// when it is Nil rather than emitting the all-zero UUID, which would look
	// like a real user id in every group-by on the dashboard.
	UserID uuid.UUID

	// Subject is the JWT subject — the only identity a `svc:` principal has.
	Subject string

	// RequestID is chi middleware.GetReqID.
	//
	// ⚠️ CLIENT-SUPPLIABLE, THEREFORE NEVER TRUSTED. chi's RequestID middleware
	// honours an inbound X-Request-Id header, so this value is attacker-chosen
	// for any caller who wants it to be. It is a correlation hint for a human
	// reading two logs side by side and NOTHING ELSE: never join on it to decide
	// authority, never let it name a file, never let it be a cassette key. It
	// gets the same 512-byte cap as any other string attribute.
	RequestID string

	// Feature is the coarse product surface: "nlq.translate", "extraction",
	// "recon", "agent", "insight". It is also the cassette directory name, so a
	// cassette's location on disk is derivable from the spans inside it.
	Feature string

	// Action and Resource are the authz pair routeAction() produced for this
	// request — the SAME pair the PDP decided on. Putting the PDP's own verdict
	// inputs on the span is what lets an auditor ask "show me every model call
	// made while the caller held `create voucher:purchase`" without joining to
	// the access log.
	Action   string
	Resource string
}

type scopeCtxKey struct{}
type spanCtxKey struct{}

// WithScope installs the ambient identity for every span started under ctx.
func WithScope(ctx context.Context, s Scope) context.Context {
	return context.WithValue(ctx, scopeCtxKey{}, s)
}

// ScopeFromContext returns the installed Scope. The bool is false when nothing
// installed one — which Start() treats as a defect worth flagging, not as a
// normal state.
func ScopeFromContext(ctx context.Context) (Scope, bool) {
	s, ok := ctx.Value(scopeCtxKey{}).(Scope)
	return s, ok
}

// Kind selects the span shape: which gen_ai.operation.name it gets, which OTLP
// span kind it exports as, and which typed setter applies to it.
type Kind uint8

const (
	// KindChat is one outbound model call.
	KindChat Kind = iota
	// KindTool is one tool execution inside a run.
	KindTool
	// KindAgentStep is one step of the plan-and-execute loop.
	KindAgentStep
)

// String is the Record.Kind value. It is deliberately NOT the operation name:
// Kind says what the span is, gen_ai.operation.name says what the convention
// calls it, and the two are allowed to drift when the convention moves.
func (k Kind) String() string {
	switch k {
	case KindChat:
		return "chat"
	case KindTool:
		return "tool"
	case KindAgentStep:
		return "agent_step"
	default:
		return "unknown"
	}
}

func (k Kind) operationName() string {
	switch k {
	case KindChat:
		return OpChat
	case KindTool:
		return OpExecuteTool
	case KindAgentStep:
		return OpInvokeAgent
	default:
		return ""
	}
}

// Span is one in-flight span. It is opaque: everything about it is set through
// the methods below, so there is no field a caller can assign a prompt to.
type Span struct {
	// noop marks the shared sentinel returned when no recorder is installed.
	// Every method returns immediately on it, and nothing ever writes to it, so
	// the single shared value is safe under any amount of concurrency.
	noop bool

	mu       sync.Mutex
	kind     Kind
	name     string
	traceID  string
	spanID   string
	parentID string
	start    time.Time
	attrs    map[string]any
	status   string
	ended    bool
}

// noopSpan is returned by Start when tracing is off.
//
// ⛔ START NEVER RETURNS NIL, EVEN WHEN TRACING IS OFF. The call site is
// `ctx, sp := trajectory.Start(...); defer sp.End()`. A nil return would make
// every one of those sites a nil-pointer panic away from taking down a request
// the instant somebody unset an environment variable — a telemetry knob that can
// crash the server is worse than no telemetry.
var noopSpan = &Span{noop: true}

// scopeMissingOnce keeps the "no Scope in context" warning to one line per
// process. It is a defect, so it must be said; it is also potentially on a hot
// path, so it must not be said ten thousand times a second. The dashboard-side
// signal is AttrScopeMissing on the span itself.
var scopeMissingOnce sync.Once

// Start begins a span and returns it together with a context that makes it the
// parent of anything started under it.
//
// ⚠️ THE NOP FAST PATH ALLOCATES NOTHING AND IS LOAD-BEARING. When no Recorder
// is installed — which is every process today and every test process — Start
// returns the caller's own ctx and the shared noopSpan. It does not read the
// clock, does not touch crypto/rand, does not build a map and does not call
// context.WithValue. TestStartEndUnderNopAllocatesZero pins that at exactly 0
// allocations per run, with a control asserting the real path allocates more.
// The point is that instrumenting a hot loop must never be a decision anyone has
// to think about.
func Start(ctx context.Context, kind Kind, name string) (context.Context, *Span) {
	ref := current.Load()
	if ref == nil || ref.noop {
		return ctx, noopSpan
	}

	sp := &Span{
		kind:   kind,
		name:   name,
		start:  time.Now(),
		attrs:  make(map[string]any, 16),
		status: "OK",
		spanID: newID(8),
	}

	// Trace continuity: a child inherits its parent's trace id, so one agent run
	// is one trace no matter how many packages it crosses. A root span mints a
	// new trace id.
	if parent, ok := ctx.Value(spanCtxKey{}).(*Span); ok && parent != nil && !parent.noop {
		sp.traceID = parent.traceID
		sp.parentID = parent.spanID
	} else {
		sp.traceID = newID(16)
	}

	if op := kind.operationName(); op != "" {
		sp.attrs[AttrGenAIOperationName] = op
	}
	if kind == KindTool {
		// The convention's default, set here so no call site has to remember it
		// and so a future "extension"/"datastore" tool is a deliberate override.
		sp.attrs[AttrGenAIToolType] = ToolTypeFunction
	}
	sp.attrs[AttrSemconvVersion] = SemconvVersion

	if scope, ok := ScopeFromContext(ctx); ok {
		sp.applyScope(scope)
	} else {
		// NEVER A SILENT FALLBACK. An unattributed span is a row that cannot be
		// filtered per tenant, cannot be erased on request, and cannot be
		// excluded from an aggregate. Say it once in the log and mark it on the
		// span so it is countable.
		sp.attrs[AttrScopeMissing] = true
		scopeMissingOnce.Do(func() {
			log.Printf("trajectory: span %q started with no Scope in context — spans will be emitted with %s=true and no tenant.id; the caller is missing a trajectory.WithScope", name, AttrScopeMissing)
		})
	}

	return context.WithValue(ctx, spanCtxKey{}, sp), sp
}

func (s *Span) applyScope(sc Scope) {
	if sc.TenantID != uuid.Nil {
		s.attrs[AttrTenantID] = sc.TenantID.String()
	} else {
		s.attrs[AttrScopeMissing] = true
	}
	// uuid.Nil is a service principal, not a bug: omit rather than emit
	// 00000000-0000-0000-0000-000000000000 as if it were a person.
	if sc.UserID != uuid.Nil {
		s.attrs[AttrEnduserID] = sc.UserID.String()
	}
	setIfNonEmpty(s.attrs, AttrEnduserSubject, sc.Subject)
	setIfNonEmpty(s.attrs, AttrHTTPRequestID, capString(sc.RequestID))
	setIfNonEmpty(s.attrs, AttrFeature, sc.Feature)
	setIfNonEmpty(s.attrs, AttrAction, sc.Action)
	setIfNonEmpty(s.attrs, AttrResource, sc.Resource)
}

func setIfNonEmpty(m map[string]any, k, v string) {
	if v != "" {
		m[k] = v
	}
}

// End finishes the span and hands it to the installed Recorder.
//
// Idempotent: a second End is ignored. `defer sp.End()` next to an explicit
// sp.End() in an early-return branch is a normal thing to write and must not
// produce a duplicate span — a duplicated chat span double-counts tokens on
// every usage dashboard downstream.
func (s *Span) End() {
	if s == nil || s.noop {
		return
	}
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended = true
	rec := Record{
		TraceID:       s.traceID,
		SpanID:        s.spanID,
		ParentID:      s.parentID,
		Name:          s.name,
		Kind:          s.kind.String(),
		StartUnixNano: s.start.UnixNano(),
		EndUnixNano:   time.Now().UnixNano(),
		Status:        s.status,
		Attributes:    s.attrs,
	}
	// Hand the map over rather than copying it, then drop our reference: the
	// Recorder queues this value and may marshal it minutes later on another
	// goroutine, so the span must not be able to mutate it afterwards. A late
	// SetString on an ended span is a no-op (see setAttr).
	s.attrs = nil
	s.mu.Unlock()

	if ref := activeRecorder(); ref != nil && !ref.noop {
		ref.r.Record(rec)
	}
}

// setAttr is the single write path for every attribute.
func (s *Span) setAttr(key string, value any) {
	if s == nil || s.noop || key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended || s.attrs == nil {
		// A setter after End is a caller bug, but a panic or a data race here
		// would be a telemetry call taking down a request. Drop it.
		return
	}
	s.attrs[key] = value
}

// SetString records a string attribute.
//
// ⛔ THIS IS THE ESCAPE HATCH AND IT IS NOT FOR CONTENT. Ids, hashes, model
// names, enum members and version strings only. Never a prompt, a completion, a
// tool result, a narration, a party name or an amount — see the package comment
// for why an attribute is the worst possible place for tenant data. Values over
// MaxAttrValueLen are REPLACED (not truncated) with TruncatedMarker, because the
// only realistic way to exceed 512 bytes here is to have passed content.
//
// ⚠️ THE `noop` CHECK IS REPEATED HERE RATHER THAN LEFT TO setAttr, IN EVERY
// SETTER BELOW, AND IT IS NOT REDUNDANT. setAttr takes an `any`, so the
// conversion from string/int64/float64 to the interface happens AT THE CALL SITE
// — before setAttr can decline. Boxing an int64 outside the small-integer cache
// or a float64 costs one heap allocation each, so without these early returns
// `sp.SetInt(k, 812)` on the discard path allocates and
// TestStartEndUnderNopAllocatesZero goes red. Instrumenting a hot loop must cost
// nothing when tracing is off.
func (s *Span) SetString(key, value string) {
	if s == nil || s.noop {
		return
	}
	s.setAttr(key, capString(value))
}

// SetInt records an integer attribute. Token counts, retry counts, sizes.
func (s *Span) SetInt(key string, value int64) {
	if s == nil || s.noop {
		return
	}
	s.setAttr(key, value)
}

// SetFloat records a float attribute.
//
// ⛔ NOT FOR MONEY. Temperature, ratios and latencies only. Every rupee figure in
// this codebase is shopspring/decimal or integer paise; a float64 that reached a
// ledger via a trace attribute would be a rounding error nobody could trace back.
func (s *Span) SetFloat(key string, value float64) {
	if s == nil || s.noop {
		return
	}
	s.setAttr(key, value)
}

// SetBool records a boolean attribute.
func (s *Span) SetBool(key string, value bool) {
	if s == nil || s.noop {
		return
	}
	s.setAttr(key, value)
}

// SetStrings records a string-array attribute (gen_ai.response.finish_reasons is
// the only one today). The elements are individually capped.
func (s *Span) SetStrings(key string, values []string) {
	if s == nil || s.noop || len(values) == 0 {
		return
	}
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = capString(v)
	}
	s.setAttr(key, out)
}

// SetError marks the span failed.
//
// ⛔ IT RECORDS THE ERROR'S TYPE AND NOT ITS MESSAGE, DELIBERATELY. Error strings
// in this codebase routinely interpolate tenant MASTER NAMES — measured, not
// assumed: `masters/price_list.go:186` says `price list %q entry %d names no
// stock item`, `:230` says `party group %q`, `:253` says `stock item %q`, and
// `masters/stock_group.go:85` says `parent stock group %q`. A party group name
// is a customer's name. So an error message is content by exactly the same
// argument as a prompt, and content is never an attribute.
//
// The type answers "which failure mode?", which is what a dashboard is for; the
// message stays in the caller's own log, inside the tenant boundary, where it
// belongs. If a failure mode ever needs more than a type to diagnose, give it
// its own khata.* enum attribute rather than widening this one.
func (s *Span) SetError(err error) {
	if s == nil || s.noop || err == nil {
		return
	}
	s.mu.Lock()
	if !s.ended {
		s.status = "ERROR"
	}
	s.mu.Unlock()
	s.setAttr(AttrErrorType, fmt.Sprintf("%T", err))
}

// ModelRequest is the content-free description of one outbound model call.
//
// ⛔ THERE IS NO PROMPT FIELD, NO MESSAGES FIELD AND NO SYSTEM-TEXT FIELD, AND
// THAT IS THE POINT. `System` is the PROVIDER name ("fireworks", "sarvam",
// "anthropic"), matching what the GenAI convention called the concept before it
// renamed the wire attribute to gen_ai.provider.name. PromptHash is SHA-256 of
// the rendered prompt (trajectory.PromptHash, and llm.StaticPromptHash on the
// other side of the seam — see PromptHash for why one value has two
// implementations); the prompt itself goes in a cassette or nowhere.
// TestTypedSettersCarryNoContentFields fails if a field named Prompt, Messages,
// Completion, Text or Content is added to this struct.
type ModelRequest struct {
	System  string
	ModelID string
	// Temperature is a POINTER so that "the caller did not set one" and "the
	// caller set 0" stay different facts.
	//
	// ⛔ A float64 HERE FABRICATED A VALUE ON EVERY PRODUCTION SPAN. The only
	// caller in the product (internal/server's trajectoryRecorder) populates
	// System, ModelID, PromptVersion and PromptHash and nothing else, so every
	// chat span carried gen_ai.request.temperature = 0 — a number a dashboard
	// reads as "this deployment runs greedy decoding", which is a meaningful
	// claim and was not true. llm.Request.Temperature is a *float64 for exactly
	// this reason; flattening it at the seam threw the distinction away and then
	// invented a value on the other side.
	Temperature   *float64
	MaxTokens     int
	PromptVersion string
	PromptHash    string
	OutputType    string
}

// Temp is the one-liner for handing a literal temperature to ModelRequest.
// `Temp(0)` means "greedy, and I mean it"; leaving the field nil means "not
// specified", and the two must not render the same.
func Temp(f float64) *float64 { return &f }

// SetModelRequest applies a ModelRequest to a KindChat span.
//
// ⛔ EVERY LINE IS GUARDED, THE WAY applyScope's setIfNonEmpty IS. An attribute
// this package emits is an attribute somebody charts, and an unset field emitted
// as its zero value is not a missing datum — it is a WRONG one, asserted with the
// same confidence as a real measurement. gen_ai.output.type = "" was the visible
// half of that (an empty enum member the semconv has no name for); the
// temperature was the dangerous half, because 0.0 is a value the field can
// legitimately take.
func (s *Span) SetModelRequest(r ModelRequest) {
	if s == nil || s.noop {
		return
	}
	if r.System != "" {
		s.SetString(AttrGenAIProviderName, r.System)
	}
	if r.ModelID != "" {
		s.SetString(AttrGenAIRequestModel, r.ModelID)
	}
	if r.Temperature != nil {
		s.SetFloat(AttrGenAIRequestTemperature, *r.Temperature)
	}
	if r.MaxTokens > 0 {
		s.SetInt(AttrGenAIRequestMaxTokens, int64(r.MaxTokens))
	}
	if r.OutputType != "" {
		s.SetString(AttrGenAIOutputType, r.OutputType)
	}
	if r.PromptVersion != "" {
		s.SetString(AttrPromptVersion, r.PromptVersion)
	}
	if r.PromptHash != "" {
		s.SetString(AttrPromptHash, r.PromptHash)
	}
}

// ModelResponse is the content-free description of what came back.
//
// ⛔ NO COMPLETION FIELD, for the same reason ModelRequest has no prompt field.
//
// Fingerprint is r8 §7's drift alarm: Fireworks and every other provider change
// weights behind a stable model id, and the fingerprint is the only evidence
// that the artefact answering today is the artefact the golden set was measured
// against. RepairAttempts is the schema-repair count (r8 §4, cap 2) — the single
// best early warning that a model's structured-output behaviour has moved.
type ModelResponse struct {
	ModelID           string
	Fingerprint       string
	ResponseID        string
	FinishReasons     []string
	InputTokens       int
	OutputTokens      int
	CachedInputTokens int
	RepairAttempts    int
}

// SetModelResponse applies a ModelResponse to a KindChat span.
func (s *Span) SetModelResponse(r ModelResponse) {
	if s == nil || s.noop {
		return
	}
	s.SetString(AttrGenAIResponseModel, r.ModelID)
	s.SetString(AttrModelFingerprint, r.Fingerprint)
	s.SetString(AttrGenAIResponseID, r.ResponseID)
	s.SetStrings(AttrGenAIResponseFinishReasons, r.FinishReasons)
	// Token counts are always emitted, including zero: "the provider reported 0
	// output tokens" and "we forgot to record output tokens" are different
	// facts, and an omitempty here would make them look identical on a
	// per-tenant cost report.
	s.SetInt(AttrGenAIUsageInputTokens, int64(r.InputTokens))
	s.SetInt(AttrGenAIUsageOutputTokens, int64(r.OutputTokens))
	s.SetInt(AttrCachedInputTokens, int64(r.CachedInputTokens))
	s.SetInt(AttrSchemaRepairAttempts, int64(r.RepairAttempts))
}

// ToolCall is the content-free description of one tool execution.
//
// ⛔ ArgsHash AND ResultHash, NEVER THE ARGUMENTS OR THE RESULT. Tool arguments
// in this product are party ids, amounts and dates; tool results are ledger rows.
// The hashes are also the cassette's replay key (see cassette.go), so recording
// them is not a consolation prize — it is what makes a Tier-0 replay possible.
type ToolCall struct {
	Name       string
	CallID     string
	ArgsHash   string
	ResultHash string
}

// SetToolCall applies a ToolCall to a KindTool span.
func (s *Span) SetToolCall(c ToolCall) {
	if s == nil || s.noop {
		return
	}
	s.SetString(AttrGenAIToolName, c.Name)
	s.SetString(AttrGenAIToolCallID, c.CallID)
	s.SetString(AttrGenAIToolType, ToolTypeFunction)
	s.SetString(AttrToolArgsHash, c.ArgsHash)
	s.SetString(AttrToolResultHash, c.ResultHash)
}

// AgentStep is the content-free description of one plan-and-execute step.
type AgentStep struct {
	AgentName      string
	ConversationID string
}

// SetAgentStep applies an AgentStep to a KindAgentStep span.
func (s *Span) SetAgentStep(a AgentStep) {
	if s == nil || s.noop {
		return
	}
	s.SetString(AttrGenAIAgentName, a.AgentName)
	s.SetString(AttrGenAIConversationID, a.ConversationID)
}

// TraceID/SpanID expose the ids so a caller can correlate a log line or write a
// cassette entry. They return "" on the nop span, which is what a caller that
// logs `trace_id=%s` should print when tracing is off — not a fabricated id.
func (s *Span) TraceID() string {
	if s == nil || s.noop {
		return ""
	}
	return s.traceID
}

// SpanID is this span's own id.
func (s *Span) SpanID() string {
	if s == nil || s.noop {
		return ""
	}
	return s.spanID
}

// capString enforces MaxAttrValueLen. It REPLACES rather than truncates: see the
// constant's comment. A truncated prompt is still a prompt.
func capString(v string) string {
	if len(v) <= MaxAttrValueLen {
		return v
	}
	return TruncatedMarker
}

// newID mints n random bytes as lowercase hex — 16 bytes for a trace id, 8 for a
// span id, exactly as the OTLP wire format wants them.
//
// crypto/rand, not math/rand. Trace ids are joined across processes and appear
// in support tickets and audit exports; a predictable id lets an attacker who can
// see one trace guess the ids of neighbouring ones, and rand.Read is not a
// measurable cost next to an HTTP call to a model provider.
//
// rand.Read is documented never to fail on any supported platform (it panics
// internally on a broken OS entropy source), so there is no error to handle here
// and no half-formed id can escape.
func newID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
