package trajectory

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fixedSpan is the one span the wire-format test pins. Its ids are hand-written
// hex rather than crypto/rand output so the expected document below can be a
// literal.
func fixedSpan() Record {
	return Record{
		TraceID:       "0102030405060708090a0b0c0d0e0f10",
		SpanID:        "1112131415161718",
		ParentID:      "2122232425262728",
		Name:          "chat glm-5.3-flash",
		Kind:          "chat",
		StartUnixNano: 1757308800000000000,
		EndUnixNano:   1757308800123456789,
		Status:        "OK",
		Attributes: map[string]any{
			AttrGenAIOperationName:         OpChat,
			AttrGenAIRequestMaxTokens:      int64(1024),
			AttrGenAIRequestTemperature:    float64(0),
			AttrGenAIResponseFinishReasons: []string{"stop"},
			AttrSemconvVersion:             SemconvVersion,
			AttrGenAIUsageOutputTokens:     int64(0),
			"khata.agent.retry":            true,
			AttrSchemaRepairAttempts:       int64(2),
			AttrTenantID:                   "3f2b1a5c-0000-4000-8000-000000000001",
		},
	}
}

// wantBody is the OTLP ExportTraceServiceRequest for fixedSpan, written by hand
// from the protobuf-JSON rules, NOT pasted from this package's output.
//
// ⛔ WHY THIS TEST IS A BYTE COMPARISON AND NOT `json.Valid(body)`. Every one of
// the three encoding rules produces a body that is PERFECTLY VALID JSON when you
// get it wrong:
//
//   - "startTimeUnixNano": 1757308800000000000 (a number) is valid JSON and is
//     rejected by an OTLP collector, which encodes uint64 as a string.
//   - "intValue": 1024 (a number) is valid JSON and is likewise rejected.
//   - a base64 traceId is valid JSON and silently indexes as a different trace.
//
// So a test that asserted "it parses" would be green against an exporter whose
// every batch the collector throws away — and the symptom of that is an empty
// Langfuse project, which looks exactly like "nobody used the agent today".
const wantBody = `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"cored"}}]},"scopeSpans":[{"scope":{"name":"github.com/khata/cored/internal/trajectory","version":"0.1.0"},"spans":[{"traceId":"0102030405060708090a0b0c0d0e0f10","spanId":"1112131415161718","parentSpanId":"2122232425262728","name":"chat glm-5.3-flash","kind":3,"startTimeUnixNano":"1757308800000000000","endTimeUnixNano":"1757308800123456789","attributes":[{"key":"gen_ai.operation.name","value":{"stringValue":"chat"}},{"key":"gen_ai.request.max_tokens","value":{"intValue":"1024"}},{"key":"gen_ai.request.temperature","value":{"doubleValue":0}},{"key":"gen_ai.response.finish_reasons","value":{"arrayValue":{"values":[{"stringValue":"stop"}]}}},{"key":"gen_ai.semconv_version","value":{"stringValue":"1.38.0-dev"}},{"key":"gen_ai.usage.output_tokens","value":{"intValue":"0"}},{"key":"khata.agent.retry","value":{"boolValue":true}},{"key":"khata.schema.repair_attempts","value":{"intValue":"2"}},{"key":"tenant.id","value":{"stringValue":"3f2b1a5c-0000-4000-8000-000000000001"}}],"status":{"code":1}}]}]}]}`

func TestOTLPBodyIsProtobufJSON(t *testing.T) {
	got, err := encodeTraces("cored", []Record{fixedSpan()})
	if err != nil {
		t.Fatalf("encodeTraces: %v", err)
	}
	if string(got) != wantBody {
		t.Fatalf("OTLP body does not match the hand-written protobuf-JSON document.\n--- got  ---\n%s\n--- want ---\n%s", got, wantBody)
	}

	// The three rules, stated individually, so a failure says WHICH one moved.
	s := string(got)
	for _, bad := range []string{
		`"startTimeUnixNano":1757308800000000000`,
		`"endTimeUnixNano":1757308800123456789`,
		`"intValue":1024`,
		`"intValue":0,`,
		`"intValue":2`,
	} {
		if strings.Contains(s, bad) {
			t.Errorf("64-bit integer emitted as a JSON number (%s); protobuf-JSON requires a decimal string and a collector rejects the number", bad)
		}
	}
	if !strings.Contains(s, `"traceId":"0102030405060708090a0b0c0d0e0f10"`) {
		t.Error("traceId is not lowercase hex")
	}
	if strings.Contains(s, `"doubleValue":"0"`) {
		t.Error("a double was emitted as a string; only 64-bit integers are stringified")
	}
	if strings.Contains(s, `"stringValue":null`) {
		t.Error("an AnyValue emitted more than one variant; it is a one-of")
	}
}

// TestOTLPZeroValuedAttributesSurvive: with non-pointer AnyValue fields plus
// omitempty, `gen_ai.usage.output_tokens = 0` and `temperature = 0` would vanish
// from the wire, and "the model produced 0 output tokens" would look identical
// to "we never recorded output tokens" on a per-tenant cost report.
func TestOTLPZeroValuedAttributesSurvive(t *testing.T) {
	got, err := encodeTraces("cored", []Record{{
		TraceID: "aa", SpanID: "bb", Kind: "tool", Status: "OK",
		Attributes: map[string]any{"z.int": int64(0), "z.float": float64(0), "z.bool": false, "z.string": ""},
	}})
	if err != nil {
		t.Fatalf("encodeTraces: %v", err)
	}
	for _, want := range []string{`{"key":"z.bool","value":{"boolValue":false}}`, `{"key":"z.float","value":{"doubleValue":0}}`, `{"key":"z.int","value":{"intValue":"0"}}`, `{"key":"z.string","value":{"stringValue":""}}`} {
		if !strings.Contains(string(got), want) {
			t.Errorf("zero-valued attribute vanished from the body: expected %s in\n%s", want, got)
		}
	}
}

// TestOTLPStatusAndKindMapping.
func TestOTLPStatusAndKindMapping(t *testing.T) {
	for _, tc := range []struct {
		kind, status         string
		wantKind, wantStatus int
	}{
		{"chat", "OK", otlpSpanKindClient, otlpStatusOK},
		{"tool", "ERROR", otlpSpanKindInternal, otlpStatusError},
		{"agent_step", "", otlpSpanKindInternal, otlpStatusUnset},
	} {
		if got := otlpKindFor(tc.kind); got != tc.wantKind {
			t.Errorf("otlpKindFor(%q) = %d, want %d", tc.kind, got, tc.wantKind)
		}
		if got := otlpStatusFor(tc.status); got != tc.wantStatus {
			t.Errorf("otlpStatusFor(%q) = %d, want %d", tc.status, got, tc.wantStatus)
		}
	}
}

// blockingTransport never answers until release is closed. It stands in for a
// collector that is down, wedged, or behind a saturated link.
type blockingTransport struct{ release chan struct{} }

func (b blockingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	<-b.release
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header)}, nil
}

// TestRecordNeverBlocksAndCountsDrops is the invariant that keeps a Langfuse
// outage from becoming a refused voucher.
//
// The transport never answers, so the drain goroutine wedges on its first POST
// and the bounded queue fills. Every Record() must still return promptly, and
// every span it could not take must be COUNTED — a silent drop is a trace store
// that looks like low traffic instead of a broken exporter.
//
// ⛔ THE LIVENESS PROPERTY IS MEASURED PER CALL, NOT OVER THE WHOLE FAN-OUT.
// The version that shipped timed the 8×250 fan-out against a 10-second
// wall-clock and, on expiry, Fatal'd with "Record() BLOCKED" — a CAUSE it could
// not distinguish from 2000 sends on a machine whose goroutines were simply not
// being scheduled (it failed once on a loaded laptop and passed the eight runs
// after). A test that names the wrong cause teaches people to re-run it, and a
// re-run flake in the package that carries the "telemetry must never stall a
// voucher" invariant is worse than no test at all.
//
// So the assertion below is the SLOWEST SINGLE Record(), which is what the
// invariant is actually about: Record is a non-blocking channel send with a
// `default:` arm, so its own duration is bounded no matter how contended the
// process is, while the sum of 2000 of them is not. CPU starvation still
// inflates a single measurement (the goroutine can be descheduled mid-call), so
// the per-call budget is set orders of magnitude above the microseconds this
// costs and orders of magnitude below "wedged".
func TestRecordNeverBlocksAndCountsDrops(t *testing.T) {
	release := make(chan struct{})
	r, err := NewOTLPRecorderWith(OTLPOptions{
		Endpoint:   "http://collector.invalid/v1/traces",
		QueueSize:  8,
		BatchSize:  4,
		FlushEvery: time.Hour,
		Client:     &http.Client{Transport: blockingTransport{release: release}},
	})
	if err != nil {
		t.Fatalf("NewOTLPRecorderWith: %v", err)
	}

	const goroutines, each = 8, 250
	// perCallBudget is the ceiling on ONE Record() against a wedged exporter.
	// A blocking send would exceed it by "for ever", not by a factor.
	const perCallBudget = 2 * time.Second
	// hangGuard only exists so a genuinely blocking Record() reports a failure
	// instead of hanging until `go test` kills the binary with a stack dump. It
	// is deliberately far past any plausible scheduling delay, and its message
	// says what it OBSERVED rather than what it guessed.
	const hangGuard = 60 * time.Second

	slowest := make([]time.Duration, goroutines) // one slot per goroutine: no shared writes, so no lock
	done := make(chan struct{})
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				start := time.Now()
				r.Record(Record{TraceID: "ffffffff0000000000000000000000ff", SpanID: "0000000000000001", Kind: "tool", Status: "OK"})
				if d := time.Since(start); d > slowest[g] {
					slowest[g] = d
				}
			}
		}(g)
	}
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(hangGuard):
		close(release)
		t.Fatalf("Record() did not return within %s with a wedged transport. %d goroutines were "+
			"still inside their %d calls; on a channel send with a default: arm the only way to "+
			"spend that long is to have lost the arm, and a telemetry backend being down would "+
			"then stall request goroutines inside voucher transactions", hangGuard, goroutines, each)
	}

	var worst time.Duration
	for _, d := range slowest {
		if d > worst {
			worst = d
		}
	}
	if worst > perCallBudget {
		close(release)
		t.Fatalf("the slowest single Record() took %s against a wedged transport, over the %s "+
			"budget. Record must be a non-blocking send: a telemetry backend being down must "+
			"never be able to stall a request goroutine inside a voucher transaction", worst, perCallBudget)
	}
	t.Logf("slowest single Record() against a wedged transport: %s (budget %s)", worst, perCallBudget)

	if got := r.Dropped(); got == 0 {
		t.Fatalf("queue of 8 absorbed %d spans against a wedged transport with 0 drops; the drop counter is not being advanced", goroutines*each)
	} else {
		t.Logf("dropped %d of %d spans against a wedged transport, as designed", got, goroutines*each)
	}

	close(release)
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestHealthyTransportDropsNothing is the CONTROL for the test above.
//
// Without it, an implementation whose Record() was `func(Record) {}` — dropping
// everything, always — would pass the never-blocks assertion and the
// drops-were-counted assertion (if it also incremented the counter). This proves
// the drops above were caused by the wedged transport and nothing else, and that
// the exporter actually delivers.
func TestHealthyTransportDropsNothing(t *testing.T) {
	var spans atomic.Int64
	var bodies atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if ct := req.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		if got := req.Header.Get("X-Langfuse-Key"); got != "sk-test" {
			t.Errorf("OTEL_EXPORTER_OTLP_HEADERS did not reach the request: X-Langfuse-Key = %q", got)
		}
		var doc otlpRequest
		if err := json.NewDecoder(req.Body).Decode(&doc); err != nil {
			t.Errorf("collector could not parse the body: %v", err)
		}
		for _, rs := range doc.ResourceSpans {
			for _, ss := range rs.ScopeSpans {
				spans.Add(int64(len(ss.Spans)))
			}
		}
		bodies.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r, err := NewOTLPRecorderWith(OTLPOptions{
		Endpoint:   srv.URL + "/v1/traces",
		Headers:    map[string]string{"X-Langfuse-Key": "sk-test"},
		QueueSize:  4096,
		BatchSize:  512,
		FlushEvery: time.Hour,
		Client:     srv.Client(),
	})
	if err != nil {
		t.Fatalf("NewOTLPRecorderWith: %v", err)
	}
	const n = 500
	for i := 0; i < n; i++ {
		r.Record(Record{TraceID: "ffffffff0000000000000000000000ff", SpanID: "0000000000000001", Kind: "chat", Status: "OK"})
	}
	if err := r.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := r.Dropped(); got != 0 {
		t.Fatalf("a healthy transport dropped %d spans; the drops in the wedged test were not caused by the wedge", got)
	}
	if got := spans.Load(); got != n {
		t.Fatalf("collector received %d spans in %d request(s), want %d", got, bodies.Load(), n)
	}
	if got := r.Exported(); got != n {
		t.Fatalf("Exported() = %d, want %d", got, n)
	}
	if got := r.Failed(); got != 0 {
		t.Fatalf("Failed() = %d, want 0", got)
	}
}

// TestUnconfiguredIsNilNilAndTracingStaysOff — the process must boot exactly as
// it does today.
func TestUnconfiguredIsNilNilAndTracingStaysOff(t *testing.T) {
	t.Setenv(otlpEndpointEnv, "")
	r, err := NewOTLPRecorder(context.Background())
	if err != nil {
		t.Fatalf("an unset %s must not be an error: %v", otlpEndpointEnv, err)
	}
	if r != nil {
		t.Fatalf("an unset %s must yield a nil recorder so the process keeps Nop()", otlpEndpointEnv)
	}
	// The nil recorder's methods must be safe: server code will hold it in a
	// *OTLPRecorder variable and may still call Close on shutdown.
	r.Record(fixedSpan())
	if err := r.Flush(context.Background()); err != nil {
		t.Fatalf("Flush on a nil recorder: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close on a nil recorder: %v", err)
	}
}

// TestMalformedConfigurationIsRefusedNotDefaulted. There is no safe guess for
// where to send tenant trace data.
func TestMalformedConfigurationIsRefusedNotDefaulted(t *testing.T) {
	cases := []struct{ name, endpoint, headers, wantSubstr string }{
		{"relative endpoint", "/v1/traces", "", "absolute http(s) URL"},
		{"no host", "http://", "", "absolute http(s) URL"},
		{"grpc scheme", "grpc://collector:4317", "", "HTTP-only"},
		{"headers not k=v", "http://collector:4318/v1/traces", "Authorization", "k=v form"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(otlpEndpointEnv, tc.endpoint)
			t.Setenv(otlpHeadersEnv, tc.headers)
			r, err := NewOTLPRecorder(context.Background())
			if err == nil {
				if r != nil {
					_ = r.Close()
				}
				t.Fatalf("%s=%q %s=%q was accepted; a malformed telemetry destination must be a loud refusal, never a quiet default", otlpEndpointEnv, tc.endpoint, otlpHeadersEnv, tc.headers)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("error %q does not name the problem (%q)", err, tc.wantSubstr)
			}
			if r != nil {
				t.Fatal("a refused configuration returned a non-nil recorder")
			}
		})
	}
}

// TestHeaderValuesNeverReachTheError: OTEL_EXPORTER_OTLP_HEADERS carries a
// credential, so a parse failure must name the key and not echo the value.
func TestHeaderValuesNeverReachTheError(t *testing.T) {
	_, err := parseOTLPHeaders("=sk-lf-super-secret-value")
	if err == nil {
		t.Fatal("an entry with an empty key was accepted")
	}
	if strings.Contains(err.Error(), "sk-lf-super-secret-value") {
		t.Fatalf("the credential was echoed into the error: %v", err)
	}
}

// TestSampleRatioIsHeadBasedOnTheTraceID. Sampling per span would give 100% of
// traces with holes in them, which reads as an agent that stopped.
func TestSampleRatioIsHeadBasedOnTheTraceID(t *testing.T) {
	r := &OTLPRecorder{sampleRatio: 0.5}
	const lowTrace = "00000001000000000000000000000000"  // ~0.0000002 of the space
	const highTrace = "fffffffe000000000000000000000000" // ~0.9999999
	if !r.sampled(lowTrace) {
		t.Error("a trace id at the bottom of the space was not sampled at ratio 0.5")
	}
	if r.sampled(highTrace) {
		t.Error("a trace id at the top of the space was sampled at ratio 0.5")
	}
	// Same id, same answer, every time: a trace is kept whole or dropped whole.
	for i := 0; i < 50; i++ {
		if r.sampled(highTrace) {
			t.Fatal("sampling is not deterministic in the trace id; a trace would be exported with holes in it")
		}
	}
	// ⛔ THE TWO EXTREMES ARE DRIVEN THROUGH THE REAL CONSTRUCTOR, NOT BUILT AS
	// STRUCT LITERALS. The version of this test that shipped asserted "0.0 must
	// keep nothing" against `&OTLPRecorder{sampleRatio: 0}` — a state
	// NewOTLPRecorderWith could not produce, because it read a zero SampleRatio
	// as "unset" and rewrote it to 1.0. So the control passed while the only path
	// an operator can actually reach did the exact opposite of what it asserts.
	// A control that tests an unreachable state is not a control.
	all, err := NewOTLPRecorderWith(OTLPOptions{Endpoint: "http://127.0.0.1:1/v1/traces", SampleRatio: Ratio(1), FlushEvery: time.Hour})
	if err != nil {
		t.Fatalf("NewOTLPRecorderWith(ratio 1): %v", err)
	}
	defer all.Close()
	none, err := NewOTLPRecorderWith(OTLPOptions{Endpoint: "http://127.0.0.1:1/v1/traces", SampleRatio: Ratio(0), FlushEvery: time.Hour})
	if err != nil {
		t.Fatalf("NewOTLPRecorderWith(ratio 0): %v", err)
	}
	defer none.Close()
	if !all.sampled(highTrace) {
		t.Fatal("ratio 1.0 must keep everything")
	}
	if none.sampled(lowTrace) || none.sampled(highTrace) {
		t.Fatal("Ratio(0) must keep NOTHING. An operator who sets TRAJECTORY_SAMPLE_RATIO=0 " +
			"is asking that no tenant trace data leave the boundary; silently exporting it " +
			"all is the unsafe direction of a silent default")
	}

	// And the UNSET case stays 1.0 — the distinction Ratio(0) exists to make.
	unset, err := NewOTLPRecorderWith(OTLPOptions{Endpoint: "http://127.0.0.1:1/v1/traces", FlushEvery: time.Hour})
	if err != nil {
		t.Fatalf("NewOTLPRecorderWith(unset): %v", err)
	}
	defer unset.Close()
	if !unset.sampled(highTrace) || unset.sampleRatio != 1 {
		t.Fatalf("an OTLPOptions with no SampleRatio must export everything; sampleRatio=%v", unset.sampleRatio)
	}
}

// TestBadSampleRatioFallsBackLoudly — a stated degradation, per the
// INSIGHTD_TIMEOUT precedent.
func TestBadSampleRatioFallsBackLoudly(t *testing.T) {
	for _, raw := range []string{"banana", "-1", "2", "NaN"} {
		t.Setenv(sampleRatioEnv, raw)
		got := sampleRatioFromEnv()
		if got == nil || *got != 1.0 {
			t.Fatalf("%s=%q gave %v, want the stated 1.0 fallback", sampleRatioEnv, raw, got)
		}
	}
	// UNSET is nil — "no preference" — and NOT the same value as the fallback.
	// The constructor turns nil into 1.0; what must never happen again is nil,
	// zero and one being one indistinguishable value at this seam.
	t.Setenv(sampleRatioEnv, "")
	if got := sampleRatioFromEnv(); got != nil {
		t.Fatalf("an unset %s gave %v, want nil (unconfigured)", sampleRatioEnv, *got)
	}
	t.Setenv(sampleRatioEnv, "0.25")
	if got := sampleRatioFromEnv(); got == nil || *got != 0.25 {
		t.Fatalf("a valid ratio was not honoured: %v", got)
	}
	// ⛔ AND ZERO SURVIVES THE READ. This is the value the coercion ate.
	t.Setenv(sampleRatioEnv, "0")
	if got := sampleRatioFromEnv(); got == nil || *got != 0 {
		t.Fatalf("%s=0 gave %v, want an explicit 0 — \"export no traces\" must reach the constructor as itself", sampleRatioEnv, got)
	}
}

// TestCollectorRejectionIsCountedNotFatal: a 4xx must not panic, must not block
// and must be counted.
func TestCollectorRejectionIsCountedNotFatal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unsupported: startTimeUnixNano must be a string", http.StatusBadRequest)
	}))
	defer srv.Close()
	r, err := NewOTLPRecorderWith(OTLPOptions{Endpoint: srv.URL, FlushEvery: time.Hour, Client: srv.Client()})
	if err != nil {
		t.Fatalf("NewOTLPRecorderWith: %v", err)
	}
	r.Record(fixedSpan())
	if err := r.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if r.Failed() != 1 || r.Exported() != 0 {
		t.Fatalf("Failed()=%d Exported()=%d, want 1 and 0", r.Failed(), r.Exported())
	}
}
