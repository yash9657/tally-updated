package trajectory

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// captureRecorder collects Records in memory. It is the fake every test in this
// file uses; it is NOT exported, because the whole point of the Recorder
// interface (D-B) is that no package imports another package's fake.
type captureRecorder struct{ got []Record }

func (c *captureRecorder) Record(sp Record) { c.got = append(c.got, sp) }

// install swaps in a recorder for one test and restores the discard recorder
// afterwards. The recorder is a process global (recorder.go explains why), so a
// test that forgot to restore it would leak spans into every later test in the
// package — and the alloc test below would then measure the wrong path and pass
// for the wrong reason.
func install(t *testing.T, r Recorder) {
	t.Helper()
	SetRecorder(r)
	t.Cleanup(func() { SetRecorder(Nop()) })
}

func testScope() Scope {
	return Scope{
		TenantID:  uuid.MustParse("3f2b1a5c-0000-4000-8000-000000000001"),
		UserID:    uuid.MustParse("3f2b1a5c-0000-4000-8000-0000000000aa"),
		Subject:   "user:priya",
		RequestID: "req-abc123",
		Feature:   "nlq.translate",
		Action:    "display",
		Resource:  "report:outstandings",
	}
}

// TestStartEndUnderNopAllocatesZero pins the fast path.
//
// WHY IT MATTERS ENOUGH TO PIN: every deployment today runs with no OTLP
// endpoint (HANDOFF-AFTER-T12.md:514 — backend/ has zero telemetry), and so does
// every test process. If Start allocated on that path, instrumenting a tool loop
// would become a performance decision someone has to make per call site, and the
// instrumentation that does not get written is the instrumentation that is not
// there when an agent misbehaves in production.
//
// MUTATION CHECK: make Start build its attrs map before the nop check, or make
// it call context.WithValue unconditionally, and this goes red.
func TestStartEndUnderNopAllocatesZero(t *testing.T) {
	SetRecorder(Nop())
	ctx := WithScope(context.Background(), testScope())
	// Hoisted: errors.New itself allocates, and the thing under measurement is
	// Start/End, not the fixture.
	boom := errors.New("ignored on the nop span")

	n := testing.AllocsPerRun(200, func() {
		_, sp := Start(ctx, KindChat, "chat glm-5.3-flash")
		sp.SetString(AttrGenAIRequestModel, "glm-5.3-flash")
		sp.SetInt(AttrGenAIUsageInputTokens, 812)
		sp.SetFloat(AttrGenAIRequestTemperature, 0.0)
		sp.SetBool(AttrScopeMissing, false)
		sp.SetModelRequest(ModelRequest{System: "fireworks", ModelID: "glm-5.3-flash"})
		sp.SetError(boom)
		sp.End()
	})
	if n != 0 {
		t.Fatalf("Start/End under Nop() allocated %v times per run, want 0 — the nop fast path in Start() has been broken", n)
	}

	// ⛔ THE CONTROL. Without it this test also passes on an implementation where
	// Start does nothing at all for EVERY recorder — i.e. on a trajectory
	// package that records no spans whatsoever. Asserting that the REAL path
	// does allocate is what proves the zero above came from the fast path and
	// not from a do-nothing implementation.
	cap := &captureRecorder{}
	install(t, cap)
	real := testing.AllocsPerRun(20, func() {
		_, sp := Start(ctx, KindChat, "chat glm-5.3-flash")
		sp.SetString(AttrGenAIRequestModel, "glm-5.3-flash")
		sp.End()
	})
	if real == 0 {
		t.Fatalf("the RECORDING path allocated 0 times per run — Start() is not building spans at all, so the zero measured above proves nothing")
	}
	if len(cap.got) == 0 {
		t.Fatal("the recording path produced no Records — the control is vacuous")
	}
}

// TestStartNeverReturnsNil covers the call shape `ctx, sp := Start(...); defer sp.End()`.
func TestStartNeverReturnsNil(t *testing.T) {
	SetRecorder(Nop())
	ctx, sp := Start(context.Background(), KindTool, "tool")
	if sp == nil {
		t.Fatal("Start returned a nil *Span with tracing off; every call site would be one deferred End() away from a panic")
	}
	if ctx == nil {
		t.Fatal("Start returned a nil context")
	}
	// Every method must be safe on it.
	sp.SetString("k", "v")
	sp.SetStrings("ks", []string{"v"})
	sp.SetError(errors.New("boom"))
	sp.End()
	sp.End()
	if sp.TraceID() != "" || sp.SpanID() != "" {
		t.Fatalf("the nop span invented ids (%q/%q); a caller logging trace_id would print a fabricated one", sp.TraceID(), sp.SpanID())
	}
}

// TestAmbientScopeIsOnEverySpan checks the seven ambient attributes plus the
// pinned semconv version.
func TestAmbientScopeIsOnEverySpan(t *testing.T) {
	cap := &captureRecorder{}
	install(t, cap)

	ctx := WithScope(context.Background(), testScope())
	for _, k := range []Kind{KindChat, KindTool, KindAgentStep} {
		_, sp := Start(ctx, k, "s")
		sp.End()
	}
	if len(cap.got) != 3 {
		t.Fatalf("recorded %d spans, want 3", len(cap.got))
	}
	wantOps := map[string]string{"chat": OpChat, "tool": OpExecuteTool, "agent_step": OpInvokeAgent}
	for _, rec := range cap.got {
		for k, want := range map[string]any{
			AttrTenantID:       "3f2b1a5c-0000-4000-8000-000000000001",
			AttrEnduserID:      "3f2b1a5c-0000-4000-8000-0000000000aa",
			AttrEnduserSubject: "user:priya",
			AttrHTTPRequestID:  "req-abc123",
			AttrFeature:        "nlq.translate",
			AttrAction:         "display",
			AttrResource:       "report:outstandings",
			AttrSemconvVersion: SemconvVersion,
		} {
			if got := rec.Attributes[k]; got != want {
				t.Errorf("span %q: attribute %s = %v, want %v", rec.Kind, k, got, want)
			}
		}
		if got := rec.Attributes[AttrGenAIOperationName]; got != wantOps[rec.Kind] {
			t.Errorf("span %q: %s = %v, want %q", rec.Kind, AttrGenAIOperationName, got, wantOps[rec.Kind])
		}
		if rec.Status != "OK" {
			t.Errorf("span %q: status %q, want OK", rec.Kind, rec.Status)
		}
		if len(rec.TraceID) != 32 || len(rec.SpanID) != 16 {
			t.Errorf("span %q: ids are %d/%d hex chars, want 32/16", rec.Kind, len(rec.TraceID), len(rec.SpanID))
		}
	}
	// The tool span carries the convention's default tool type without anyone
	// having set it.
	if got := cap.got[1].Attributes[AttrGenAIToolType]; got != ToolTypeFunction {
		t.Errorf("tool span: %s = %v, want %q", AttrGenAIToolType, got, ToolTypeFunction)
	}
}

// TestServicePrincipalOmitsEnduserID: uuid.Nil is a `svc:` principal, not a bug.
// Emitting the all-zero UUID would put a fake user in every group-by.
func TestServicePrincipalOmitsEnduserID(t *testing.T) {
	cap := &captureRecorder{}
	install(t, cap)

	sc := testScope()
	sc.UserID = uuid.Nil
	sc.Subject = "svc:insightd"
	_, sp := Start(WithScope(context.Background(), sc), KindChat, "s")
	sp.End()

	rec := cap.got[0]
	if _, present := rec.Attributes[AttrEnduserID]; present {
		t.Fatalf("%s was emitted for a service principal (value %v); uuid.Nil must be OMITTED, not exported as 00000000-...", AttrEnduserID, rec.Attributes[AttrEnduserID])
	}
	if rec.Attributes[AttrEnduserSubject] != "svc:insightd" {
		t.Fatalf("the subject is the only identity a service principal has and it is missing: %v", rec.Attributes[AttrEnduserSubject])
	}
	if rec.Attributes[AttrScopeMissing] == true {
		t.Fatal("a service principal was flagged as a missing scope; it is a legitimate principal")
	}
}

// TestMissingScopeIsFlaggedNotSwallowed. NEVER A SILENT FALLBACK: an
// unattributed span must be visible as a countable attribute, not just absent
// data that looks like low traffic.
func TestMissingScopeIsFlaggedNotSwallowed(t *testing.T) {
	cap := &captureRecorder{}
	install(t, cap)

	_, sp := Start(context.Background(), KindChat, "s")
	sp.End()

	rec := cap.got[0]
	if rec.Attributes[AttrScopeMissing] != true {
		t.Fatalf("a span started with no Scope did not carry %s=true; it would sit in the trace store indistinguishable from an attributed one", AttrScopeMissing)
	}
	if _, present := rec.Attributes[AttrTenantID]; present {
		t.Fatalf("a scopeless span invented a %s", AttrTenantID)
	}
}

// TestChildInheritsTraceAndNamesParent — one agent run must be one trace.
func TestChildInheritsTraceAndNamesParent(t *testing.T) {
	cap := &captureRecorder{}
	install(t, cap)

	root := WithScope(context.Background(), testScope())
	ctx, parent := Start(root, KindAgentStep, "plan")
	_, child := Start(ctx, KindTool, "query_books")
	child.End()
	parent.End()

	if len(cap.got) != 2 {
		t.Fatalf("recorded %d spans, want 2", len(cap.got))
	}
	c, p := cap.got[0], cap.got[1]
	if c.TraceID != p.TraceID {
		t.Fatalf("child trace %s != parent trace %s; the run would appear as two unrelated traces", c.TraceID, p.TraceID)
	}
	if c.ParentID != p.SpanID {
		t.Fatalf("child parent_id %q != parent span_id %q", c.ParentID, p.SpanID)
	}
	if p.ParentID != "" {
		t.Fatalf("root span has parent_id %q, want empty", p.ParentID)
	}
}

// TestEndIsIdempotent. `defer sp.End()` beside an explicit End in an early
// return is normal Go; a duplicate chat span double-counts tokens everywhere
// downstream.
func TestEndIsIdempotent(t *testing.T) {
	cap := &captureRecorder{}
	install(t, cap)
	_, sp := Start(WithScope(context.Background(), testScope()), KindChat, "s")
	sp.End()
	sp.End()
	sp.End()
	if len(cap.got) != 1 {
		t.Fatalf("End() emitted %d spans, want 1", len(cap.got))
	}
	// A setter after End must be dropped, not panic and not mutate the Record
	// the recorder already holds.
	sp.SetString("late", "value")
	if _, present := cap.got[0].Attributes["late"]; present {
		t.Fatal("an attribute set AFTER End mutated the Record the Recorder already holds — the exporter marshals batches minutes later, on another goroutine")
	}
}

// TestSetErrorRecordsTypeNotMessage. Error strings in this codebase interpolate
// ledger and party names; a message is content by the same argument as a prompt.
func TestSetErrorRecordsTypeNotMessage(t *testing.T) {
	cap := &captureRecorder{}
	install(t, cap)

	secret := "ledger \"Acme Traders (Pune)\" is over its credit limit by 4,52,000"
	_, sp := Start(WithScope(context.Background(), testScope()), KindTool, "post")
	sp.SetError(errors.New(secret))
	sp.End()

	rec := cap.got[0]
	if rec.Status != "ERROR" {
		t.Fatalf("status %q, want ERROR", rec.Status)
	}
	if rec.Attributes[AttrErrorType] != "*errors.errorString" {
		t.Fatalf("%s = %v, want the Go type", AttrErrorType, rec.Attributes[AttrErrorType])
	}
	for k, v := range rec.Attributes {
		if s, ok := v.(string); ok && strings.Contains(s, "Acme Traders") {
			t.Fatalf("attribute %s leaked the error message (%q) into the trace store", k, s)
		}
	}
}

// TestOverLongStringIsReplacedLoudly. MaxAttrValueLen is a content control:
// truncating silently would put the first 512 bytes of a bill in the store and
// tell nobody.
func TestOverLongStringIsReplacedLoudly(t *testing.T) {
	cap := &captureRecorder{}
	install(t, cap)

	prompt := "You are an accountant. " + strings.Repeat("Bill from Acme Traders for 45200 rupees. ", 40)
	if len(prompt) <= MaxAttrValueLen {
		t.Fatalf("test fixture is only %d bytes; it must exceed MaxAttrValueLen (%d) or this test proves nothing", len(prompt), MaxAttrValueLen)
	}
	_, sp := Start(WithScope(context.Background(), testScope()), KindChat, "s")
	sp.SetString("khata.oops", prompt)
	sp.End()

	got, _ := cap.got[0].Attributes["khata.oops"].(string)
	if got != TruncatedMarker {
		t.Fatalf("over-long value was kept or truncated instead of replaced: %q", got)
	}
	if strings.Contains(got, "Acme") {
		t.Fatal("a fragment of the content survived the replacement")
	}
}

// TestSetModelRequestInventsNothing pins the guard on every field.
//
// ⛔ IT DRIVES THE EXACT SHAPE THE PRODUCT SENDS. internal/server's
// trajectoryRecorder.StartLLMCall populates System, ModelID, PromptVersion and
// PromptHash and NOTHING else, because llm.SpanStart carries nothing else. The
// version of SetModelRequest that shipped emitted gen_ai.request.temperature
// unconditionally and gen_ai.output.type as "", so every chat span in a
// production trace asserted a temperature of 0.0 — which a dashboard reads as
// "greedy decoding", a claim nobody made — and an output type the semantic
// conventions have no member for. Both were invisible to the suite because every
// existing test populated every field.
//
// ⛔ THE CONTROL IS THE SECOND HALF: a SetModelRequest that emitted NOTHING at
// all would satisfy the absences above. So the same test asserts that a fully
// populated request still carries all seven attributes, temperature included,
// and that Temp(0) — "greedy, and I mean it" — is emitted rather than swallowed
// as if it were unset.
func TestSetModelRequestInventsNothing(t *testing.T) {
	cap := &captureRecorder{}
	install(t, cap)

	// (1) The production shape.
	_, sp := Start(context.Background(), KindChat, "chat claude-haiku-4-5")
	sp.SetModelRequest(ModelRequest{
		System:        "anthropic",
		ModelID:       "claude-haiku-4-5",
		PromptVersion: "nlq.v1",
		PromptHash:    PromptHash("p"),
	})
	sp.End()
	if len(cap.got) != 1 {
		t.Fatalf("recorded %d spans, want 1 — the rest of this test would assert nothing", len(cap.got))
	}
	attrs := cap.got[0].Attributes
	for _, key := range []string{
		AttrGenAIRequestTemperature,
		AttrGenAIRequestMaxTokens,
		AttrGenAIOutputType,
	} {
		if v, ok := attrs[key]; ok {
			t.Errorf("%s = %#v on a request that did not set it. An unset field emitted as its "+
				"zero value is not a missing datum, it is a wrong one stated with the same "+
				"confidence as a measurement", key, v)
		}
	}
	if attrs[AttrGenAIRequestModel] != "claude-haiku-4-5" || attrs[AttrGenAIProviderName] != "anthropic" {
		t.Errorf("the fields that WERE set did not survive: %#v", attrs)
	}

	// (2) THE CONTROL: everything supplied, everything emitted.
	cap.got = nil
	_, full := Start(context.Background(), KindChat, "chat glm-5.3-flash")
	full.SetModelRequest(ModelRequest{
		System:        "fireworks",
		ModelID:       "glm-5.3-flash",
		Temperature:   Temp(0),
		MaxTokens:     1024,
		PromptVersion: "purchase.v3",
		PromptHash:    PromptHash("q"),
		OutputType:    "json",
	})
	full.End()
	if len(cap.got) != 1 {
		t.Fatalf("recorded %d spans on the control, want 1", len(cap.got))
	}
	got := cap.got[0].Attributes
	if v, ok := got[AttrGenAIRequestTemperature]; !ok || v != float64(0) {
		t.Errorf("%s = %#v (present=%t); Temp(0) is an EXPLICIT greedy setting and must be "+
			"recorded — if it were dropped, the guard would have turned one bug into the "+
			"opposite one", AttrGenAIRequestTemperature, v, ok)
	}
	if got[AttrGenAIRequestMaxTokens] != int64(1024) || got[AttrGenAIOutputType] != "json" ||
		got[AttrPromptVersion] != "purchase.v3" {
		t.Errorf("a supplied field was dropped: %#v", got)
	}
}
