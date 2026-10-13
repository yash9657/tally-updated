package trajectory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// otlp.go is a complete OTLP/HTTP trace exporter in ~250 lines of standard
// library.
//
// WHY NOT go.opentelemetry.io/otel. IMPLEMENTATION_PLAN.md §1.1: "Go (backend/):
// NO new module dependencies." The OTel Go SDK plus the OTLP HTTP exporter plus
// the protobuf runtime is a dependency tree an order of magnitude larger than
// everything backend/go.mod contains today, imported by a package that will end
// up linked into every binary. r8 §1.2 records the fact that makes the trade
// unnecessary: **Langfuse's OTLP endpoint is HTTP-only and accepts protobuf-JSON**
// ("OTLP/HTTP (JSON + protobuf)", and r8:34 "HTTP-only; do not plan on gRPC"),
// and so does the OTel Collector. Protobuf-JSON is just JSON with three encoding
// rules, all of them handled below. The SDK's remaining value — samplers,
// processors, propagators — is a page of code we would configure and then never
// touch.
//
// ⛔ THE THREE PROTOBUF-JSON RULES, AND WHY GETTING THEM WRONG IS SILENT:
//
//  1. 64-bit integers are encoded as JSON **strings**. startTimeUnixNano,
//     endTimeUnixNano and every intValue are decimal strings, not numbers. A
//     collector rejects numeric ones — and a body that is perfectly valid JSON
//     sails past any test that only asserts json.Valid. That is why
//     TestOTLPBodyIsProtobufJSON byte-compares against a hand-written document.
//  2. traceId and spanId are **lowercase hex strings** (16 and 8 bytes, so 32 and
//     16 characters), not base64 and not byte arrays. Protobuf-JSON's default for
//     `bytes` is base64; OTLP's own JSON mapping overrides it to hex for these
//     two fields specifically.
//  3. An AnyValue is a ONE-OF: exactly one of stringValue / intValue /
//     doubleValue / boolValue / arrayValue is present. Emitting two, or emitting
//     none for a zero value, produces a span the collector drops.

// otlpEndpointEnv and friends are the standard OTel environment variables. They
// are read through the same envOr shape internal/server uses.
const (
	otlpEndpointEnv    = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
	otlpHeadersEnv     = "OTEL_EXPORTER_OTLP_HEADERS"
	otlpServiceNameEnv = "OTEL_SERVICE_NAME"
	sampleRatioEnv     = "TRAJECTORY_SAMPLE_RATIO"

	defaultServiceName = "cored"
	defaultQueueSize   = 2048
	defaultBatchSize   = 512
	defaultFlushEvery  = 5 * time.Second
	defaultHTTPTimeout = 10 * time.Second
)

// OTLPOptions is the explicit form of the exporter's configuration.
// NewOTLPRecorder builds one from the environment; tests and any future
// non-environment caller build one directly. It exists so the HTTP client can be
// supplied BEFORE the drain goroutine starts — assigning a field on a running
// recorder would be a data race with the goroutine reading it.
type OTLPOptions struct {
	Endpoint    string
	Headers     map[string]string
	ServiceName string
	// SampleRatio is a POINTER because 0 is a MEANING, not an absence.
	//
	// ⛔ nil = "not configured, export everything"; Ratio(0) = "EXPORT NOTHING".
	// As a float64 those two were the same value, and NewOTLPRecorderWith
	// rewrote it to 1.0 — so an operator who set TRAJECTORY_SAMPLE_RATIO=0 to
	// stop trace egress got 100% of it, with nothing in the log saying the value
	// had been overridden. That is a silent fallback in the unsafe direction:
	// tenant trace data crossing the boundary after somebody asked that it not.
	SampleRatio *float64
	QueueSize   int
	BatchSize   int
	FlushEvery  time.Duration
	Client      *http.Client
}

// OTLPRecorder batches finished spans and POSTs them as OTLP/HTTP protobuf-JSON.
//
// ⛔ Record() NEVER BLOCKS AND NEVER FAILS. Spans arrive on request goroutines
// that are in the middle of writing vouchers. The queue is bounded; when it is
// full a span is DROPPED and counted (Dropped()), never waited on. A telemetry
// backend that is down must not take a voucher write down — the same rule
// internal/notify/notify.go:434-435 states for the notification transport.
type OTLPRecorder struct {
	// Endpoint and Headers are read-only after construction. Headers is where a
	// Langfuse basic-auth key arrives, via OTEL_EXPORTER_OTLP_HEADERS.
	Endpoint string
	Headers  map[string]string

	serviceName string
	sampleRatio float64
	batchSize   int
	flushEvery  time.Duration
	client      *http.Client

	queue    chan Record
	flushReq chan chan struct{}
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once

	dropped  atomic.Int64
	exported atomic.Int64
	failed   atomic.Int64
}

// NewOTLPRecorder builds the exporter from the environment.
//
// ⛔ (nil, nil) WHEN UNCONFIGURED IS THE DOCUMENTED, NON-SILENT BEHAVIOUR.
// OTEL_EXPORTER_OTLP_TRACES_ENDPOINT unset means "this deployment has no trace
// backend", which is every deployment today (HANDOFF-AFTER-T12.md:514 records
// that backend/ has zero telemetry). The process must boot EXACTLY as it does
// now, keeping Nop(). But it must not boot in silence: one line goes to the log
// saying tracing is off and naming the variable that would turn it on. The
// failure this prevents is the one that costs a day — an operator who set
// LANGFUSE_HOST, believes traces are flowing, and finds an empty project.
//
// A MALFORMED endpoint is a different case and is a REFUSAL, not a fallback:
// there is no safe guess for where to send tenant trace data, and quietly
// dropping it would be a control that reports success and does nothing.
//
// ctx bounds the drain goroutine: cancelling it flushes and stops, so a server
// shutdown does not need to remember to call Close (it should anyway, to wait).
func NewOTLPRecorder(ctx context.Context) (*OTLPRecorder, error) {
	endpoint := strings.TrimSpace(os.Getenv(otlpEndpointEnv))
	if endpoint == "" {
		log.Printf("trajectory: tracing is OFF (%s is unset); model and tool spans are discarded at Nop(). Set it to your collector's /v1/traces URL to enable.", otlpEndpointEnv)
		return nil, nil
	}

	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("trajectory: %s=%q is not an absolute http(s) URL (expected e.g. http://collector:4318/v1/traces)", otlpEndpointEnv, endpoint)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("trajectory: %s=%q has scheme %q; only http and https are supported (r8 §1.2: Langfuse's OTLP endpoint is HTTP-only, there is no gRPC path)", otlpEndpointEnv, endpoint, u.Scheme)
	}

	headers, err := parseOTLPHeaders(os.Getenv(otlpHeadersEnv))
	if err != nil {
		return nil, err
	}

	opts := OTLPOptions{
		Endpoint:    endpoint,
		Headers:     headers,
		ServiceName: envOr(otlpServiceNameEnv, defaultServiceName),
		SampleRatio: sampleRatioFromEnv(),
	}
	r, err := NewOTLPRecorderWith(opts)
	if err != nil {
		return nil, err
	}
	log.Printf("trajectory: tracing ON -> %s (service=%s, sample_ratio=%.3f, queue=%d, batch=%d, flush=%s, headers=%d)",
		redactURL(u), opts.ServiceName, r.sampleRatio, cap(r.queue), r.batchSize, r.flushEvery, len(headers))
	// ⛔ A CONFIGURED ZERO IS HONOURED AND SAID OUT LOUD, in that order. It is a
	// legitimate setting — "stand the exporter up, export nothing" — and it is
	// also indistinguishable at a glance from a collector that is down, so the
	// line above ("tracing ON") must not be the last word on it. House rule 3:
	// never a silent default, and never a stated capability the deployment does
	// not have.
	if r.sampleRatio == 0 {
		log.Printf("trajectory: %s=0 — the exporter is running and will DROP EVERY TRACE. Nothing will reach %s. Unset %s to export all traces.",
			sampleRatioEnv, redactURL(u), sampleRatioEnv)
	}

	// Bind the exporter's lifetime to ctx when there is one to bind to.
	//
	// ⚠️ ctx.Done() IS nil FOR context.Background(), and a receive on a nil
	// channel blocks for ever — so without this check the overwhelmingly common
	// call, NewOTLPRecorder(context.Background()), would leak one goroutine per
	// call. Harmless in a server that constructs it once; a slow leak in any
	// test or tool that constructs it in a loop, and exactly the kind of thing
	// that is found six months later in a heap profile.
	if ctx != nil && ctx.Done() != nil {
		go func() {
			<-ctx.Done()
			_ = r.Close()
		}()
	}
	return r, nil
}

// NewOTLPRecorderWith builds the exporter from an explicit configuration and
// starts its single drain goroutine.
func NewOTLPRecorderWith(opts OTLPOptions) (*OTLPRecorder, error) {
	if strings.TrimSpace(opts.Endpoint) == "" {
		return nil, errors.New("trajectory: OTLPOptions.Endpoint is required")
	}
	if opts.ServiceName == "" {
		opts.ServiceName = defaultServiceName
	}
	if opts.QueueSize <= 0 {
		opts.QueueSize = defaultQueueSize
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = defaultBatchSize
	}
	if opts.FlushEvery <= 0 {
		opts.FlushEvery = defaultFlushEvery
	}
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: defaultHTTPTimeout}
	}
	// nil means the caller expressed no preference; every other value is honoured
	// as written, INCLUDING zero. See OTLPOptions.SampleRatio.
	ratio := 1.0
	if opts.SampleRatio != nil {
		ratio = clampRatio(*opts.SampleRatio)
	}

	r := &OTLPRecorder{
		Endpoint:    opts.Endpoint,
		Headers:     opts.Headers,
		serviceName: opts.ServiceName,
		sampleRatio: ratio,
		batchSize:   opts.BatchSize,
		flushEvery:  opts.FlushEvery,
		client:      opts.Client,
		queue:       make(chan Record, opts.QueueSize),
		flushReq:    make(chan chan struct{}),
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
	}
	go r.run()
	return r, nil
}

// Record enqueues a finished span. It never blocks and never returns an error.
//
// ⛔ THE `default:` ARM IS THE WHOLE DESIGN. A blocking send here would park a
// request goroutine — one that is quite possibly inside a voucher transaction —
// until a collector on the other side of the internet drained. Dropping is the
// correct behaviour for telemetry under back-pressure; SILENTLY dropping is not,
// which is why every drop increments a counter that Dropped() and the
// close-time log line report.
func (r *OTLPRecorder) Record(sp Record) {
	if r == nil {
		return
	}
	if !r.sampled(sp.TraceID) {
		return
	}
	select {
	case r.queue <- sp:
	default:
		r.dropped.Add(1)
	}
}

// Dropped is the number of spans discarded because the queue was full (or
// because Record was called after Close). It is the health metric for this
// exporter: a non-zero and rising value means the collector cannot keep up and
// the traces are incomplete — which matters, because an incomplete trace looks
// exactly like an agent run that stopped early.
func (r *OTLPRecorder) Dropped() int64 {
	if r == nil {
		return 0
	}
	return r.dropped.Load()
}

// Exported and Failed count spans successfully POSTed and spans lost to a
// transport error. Exposed for the same reason as Dropped: a silent exporter is
// indistinguishable from an idle one.
func (r *OTLPRecorder) Exported() int64 {
	if r == nil {
		return 0
	}
	return r.exported.Load()
}

// Failed counts spans lost to transport errors.
func (r *OTLPRecorder) Failed() int64 {
	if r == nil {
		return 0
	}
	return r.failed.Load()
}

// Flush blocks until everything queued at the moment of the call has been
// POSTed (or has failed). Used by tests and by a graceful shutdown.
func (r *OTLPRecorder) Flush(ctx context.Context) error {
	if r == nil {
		return nil
	}
	reply := make(chan struct{})
	select {
	case r.flushReq <- reply:
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-reply:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close stops the drain goroutine after a final flush.
//
// It never closes the span queue. Closing it would make a concurrent Record()
// panic on send-to-closed-channel — a telemetry call crashing the process during
// shutdown, which is exactly the class of failure this package refuses to have.
// Post-Close spans simply fill the buffer and then count as drops.
func (r *OTLPRecorder) Close() error {
	if r == nil {
		return nil
	}
	r.stopOnce.Do(func() { close(r.stop) })
	<-r.done
	if d := r.dropped.Load(); d > 0 {
		log.Printf("trajectory: exporter closed; %d span(s) were DROPPED (queue full or post-close) and %d failed to export — traces for those runs are incomplete", d, r.failed.Load())
	}
	return nil
}

// run is the single drain goroutine.
//
// ONE goroutine, not a pool: the batches must not interleave partial traces, and
// a single writer means the batch slice needs no lock. Back-pressure is handled
// by the bounded queue in Record, not by adding senders.
func (r *OTLPRecorder) run() {
	defer close(r.done)
	batch := make([]Record, 0, r.batchSize)
	t := time.NewTicker(r.flushEvery)
	defer t.Stop()

	for {
		select {
		case sp := <-r.queue:
			batch = append(batch, sp)
			if len(batch) >= r.batchSize {
				r.send(batch)
				batch = batch[:0]
			}
		case <-t.C:
			if len(batch) > 0 {
				r.send(batch)
				batch = batch[:0]
			}
		case reply := <-r.flushReq:
			batch = r.drainInto(batch)
			if len(batch) > 0 {
				r.send(batch)
				batch = batch[:0]
			}
			close(reply)
		case <-r.stop:
			batch = r.drainInto(batch)
			if len(batch) > 0 {
				r.send(batch)
			}
			return
		}
	}
}

// drainInto pulls everything currently buffered without blocking.
func (r *OTLPRecorder) drainInto(batch []Record) []Record {
	for {
		select {
		case sp := <-r.queue:
			batch = append(batch, sp)
		default:
			return batch
		}
	}
}

// send POSTs one batch. Errors are logged and counted, never propagated: there
// is no caller left to return them to, and there must not be.
func (r *OTLPRecorder) send(batch []Record) {
	if len(batch) == 0 {
		return
	}
	body, err := encodeTraces(r.serviceName, batch)
	if err != nil {
		r.failed.Add(int64(len(batch)))
		log.Printf("trajectory: encoding %d span(s) failed: %v", len(batch), err)
		return
	}
	req, err := http.NewRequest(http.MethodPost, r.Endpoint, bytes.NewReader(body))
	if err != nil {
		r.failed.Add(int64(len(batch)))
		log.Printf("trajectory: building export request failed: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range r.Headers {
		req.Header.Set(k, v)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		r.failed.Add(int64(len(batch)))
		log.Printf("trajectory: exporting %d span(s) failed: %v", len(batch), err)
		return
	}
	defer resp.Body.Close()
	// Drain so the connection can be reused; cap it because a misconfigured
	// endpoint (an HTML login page, say) must not be read into memory wholesale.
	slurp, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		r.failed.Add(int64(len(batch)))
		// The response body of a REJECTED batch is the collector's own error
		// text, not tenant data, and it is the only way to learn that (say) the
		// nanos were numbers. Cap it hard anyway.
		log.Printf("trajectory: collector rejected %d span(s): HTTP %d %s", len(batch), resp.StatusCode, strings.TrimSpace(string(slurp[:min(len(slurp), 512)])))
		return
	}
	r.exported.Add(int64(len(batch)))
}

// sampled is head-based, deterministic and keyed on the TRACE id.
//
// ⚠️ IT MUST BE THE TRACE ID AND NOT A COIN FLIP PER SPAN. Sampling each span
// independently at 0.3 does not give you 30% of traces; it gives you 100% of
// traces with 70% of their spans missing, which is worse than no data because a
// gap looks like an agent that stopped. Hashing the trace id keeps a trace whole
// or drops it whole.
func (r *OTLPRecorder) sampled(traceID string) bool {
	if r.sampleRatio >= 1 {
		return true
	}
	if r.sampleRatio <= 0 {
		return false
	}
	if len(traceID) < 8 {
		// No id to key on. Keep the span: losing telemetry is the cheaper error
		// than losing it invisibly, and this only happens for a hand-built
		// Record, never for one Start() produced.
		return true
	}
	v, err := strconv.ParseUint(traceID[:8], 16, 32)
	if err != nil {
		return true
	}
	return float64(v)/float64(math.MaxUint32) < r.sampleRatio
}

func clampRatio(f float64) float64 {
	if math.IsNaN(f) || f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

// sampleRatioFromEnv reads TRAJECTORY_SAMPLE_RATIO.
//
// It returns nil when the variable is UNSET — "the operator expressed no
// preference", which NewOTLPRecorderWith turns into 1.0 — and a non-nil value
// for everything else, including an explicit 0.
//
// ⛔ THE nil IS NOT A SYNONYM FOR Ratio(1). It is the distinction that makes
// Ratio(0) reachable at all: while this returned a plain float64, "unset" and
// "zero" were the same bits, the constructor coerced the pair of them to 1.0,
// and an operator who asked for no trace egress got all of it.
//
// An unparseable or out-of-range value falls back to 1.0 and SAYS SO, following
// the INSIGHTD_TIMEOUT precedent (insightd_proxy.go:127): refusing to boot the
// whole server over one optional telemetry knob is disproportionate, but a
// deployment that meant 0.05 and is paying for 100% of traces must be able to
// find out why from the log.
func sampleRatioFromEnv() *float64 {
	raw := strings.TrimSpace(os.Getenv(sampleRatioEnv))
	if raw == "" {
		return nil
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(f) || f < 0 || f > 1 {
		log.Printf("trajectory: %s=%q is not a ratio in [0,1]; using 1.0 (every trace exported)", sampleRatioEnv, raw)
		return Ratio(1.0)
	}
	return Ratio(f)
}

// Ratio is the one-liner for setting OTLPOptions.SampleRatio to a literal.
// `Ratio(0)` means EXPORT NOTHING; leaving the field nil means "unconfigured,
// export everything". They are different requests and this package keeps them
// different.
func Ratio(f float64) *float64 { return &f }

// parseOTLPHeaders reads the OTel-standard "k=v,k=v" form. This is how a
// self-hosted Langfuse's basic-auth key arrives.
//
// A malformed entry is a REFUSAL, not a skip: the realistic malformed entry is a
// truncated credential, and continuing without it produces a 401 per batch
// forever with the reason two layers away in a collector log.
func parseOTLPHeaders(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	out := make(map[string]string)
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if !ok || k == "" {
			// ⛔ The value is a credential. Name the KEY and the position, never
			// echo the value into a log file.
			return nil, fmt.Errorf("trajectory: %s entry %q is not in k=v form", otlpHeadersEnv, k)
		}
		out[k] = v
	}
	return out, nil
}

// redactURL prints an endpoint without its userinfo. An operator who put
// basic-auth credentials in the URL should not have them appear in the boot log.
func redactURL(u *url.URL) string {
	c := *u
	c.User = nil
	return c.String()
}

// envOr mirrors internal/server's helper (server.go:441). Duplicated rather than
// imported because this package's allowedDeps is the empty map — see doc.go.
func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// ---------------------------------------------------------------------------
// protobuf-JSON encoding
// ---------------------------------------------------------------------------

type otlpRequest struct {
	ResourceSpans []otlpResourceSpans `json:"resourceSpans"`
}

type otlpResourceSpans struct {
	Resource   otlpResource     `json:"resource"`
	ScopeSpans []otlpScopeSpans `json:"scopeSpans"`
}

type otlpResource struct {
	Attributes []otlpKeyValue `json:"attributes"`
}

type otlpScopeSpans struct {
	Scope otlpScope  `json:"scope"`
	Spans []otlpSpan `json:"spans"`
}

type otlpScope struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type otlpSpan struct {
	TraceID           string         `json:"traceId"`
	SpanID            string         `json:"spanId"`
	ParentSpanID      string         `json:"parentSpanId,omitempty"`
	Name              string         `json:"name"`
	Kind              int            `json:"kind"`
	StartTimeUnixNano string         `json:"startTimeUnixNano"`
	EndTimeUnixNano   string         `json:"endTimeUnixNano"`
	Attributes        []otlpKeyValue `json:"attributes"`
	Status            otlpStatus     `json:"status"`
}

type otlpStatus struct {
	Code int `json:"code"`
}

type otlpKeyValue struct {
	Key   string       `json:"key"`
	Value otlpAnyValue `json:"value"`
}

// otlpAnyValue is protobuf-JSON's one-of. Every field is a POINTER so that a
// zero value (0, 0.0, false, "") still marshals as a present field: with plain
// types plus omitempty, `gen_ai.usage.output_tokens = 0` would vanish from the
// wire and the collector would see an attribute with no value at all.
type otlpAnyValue struct {
	StringValue *string         `json:"stringValue,omitempty"`
	IntValue    *string         `json:"intValue,omitempty"`
	DoubleValue *float64        `json:"doubleValue,omitempty"`
	BoolValue   *bool           `json:"boolValue,omitempty"`
	ArrayValue  *otlpArrayValue `json:"arrayValue,omitempty"`
}

type otlpArrayValue struct {
	Values []otlpAnyValue `json:"values"`
}

// OTLP status codes (opentelemetry.proto.trace.v1.Status.StatusCode).
const (
	otlpStatusUnset = 0
	otlpStatusOK    = 1
	otlpStatusError = 2
)

// OTLP span kinds (opentelemetry.proto.trace.v1.Span.SpanKind).
const (
	otlpSpanKindInternal = 1
	otlpSpanKindClient   = 3
)

// encodeTraces renders a batch as one OTLP ExportTraceServiceRequest document.
//
// Attributes are emitted in SORTED key order. Go map iteration is randomised, so
// without the sort the same span would produce different bytes on every call —
// which makes the byte-level test in otlp_test.go impossible to write and makes
// any future response-diffing or request-hashing impossible too.
func encodeTraces(serviceName string, batch []Record) ([]byte, error) {
	spans := make([]otlpSpan, 0, len(batch))
	for _, rec := range batch {
		spans = append(spans, otlpSpan{
			TraceID:           rec.TraceID,
			SpanID:            rec.SpanID,
			ParentSpanID:      rec.ParentID,
			Name:              rec.Name,
			Kind:              otlpKindFor(rec.Kind),
			StartTimeUnixNano: strconv.FormatInt(rec.StartUnixNano, 10),
			EndTimeUnixNano:   strconv.FormatInt(rec.EndUnixNano, 10),
			Attributes:        encodeAttributes(rec.Attributes),
			Status:            otlpStatus{Code: otlpStatusFor(rec.Status)},
		})
	}
	doc := otlpRequest{ResourceSpans: []otlpResourceSpans{{
		Resource: otlpResource{Attributes: []otlpKeyValue{
			{Key: "service.name", Value: stringValue(serviceName)},
		}},
		ScopeSpans: []otlpScopeSpans{{
			Scope: otlpScope{Name: InstrumentationScopeName, Version: InstrumentationVersion},
			Spans: spans,
		}},
	}}}
	return json.Marshal(doc)
}

func otlpKindFor(kind string) int {
	// A model call leaves the process, so it is CLIENT; a tool step and an agent
	// step happen inside it, so they are INTERNAL. Getting this wrong does not
	// lose data but it does put agent steps in the "outbound dependency" panel
	// of every generic APM view, where they are meaningless.
	if kind == KindChat.String() {
		return otlpSpanKindClient
	}
	return otlpSpanKindInternal
}

func otlpStatusFor(status string) int {
	switch status {
	case "OK":
		return otlpStatusOK
	case "ERROR":
		return otlpStatusError
	default:
		return otlpStatusUnset
	}
}

func encodeAttributes(attrs map[string]any) []otlpKeyValue {
	if len(attrs) == 0 {
		return []otlpKeyValue{}
	}
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]otlpKeyValue, 0, len(keys))
	for _, k := range keys {
		v, ok := anyValue(attrs[k])
		if !ok {
			// An unrepresentable attribute value is a programming error in a
			// caller. Emit its Go type as a string rather than dropping the key:
			// a missing attribute is invisible, a `<unsupported ...>` value is a
			// grep away.
			v = stringValue(fmt.Sprintf("<unsupported %T>", attrs[k]))
		}
		out = append(out, otlpKeyValue{Key: k, Value: v})
	}
	return out
}

func anyValue(v any) (otlpAnyValue, bool) {
	switch t := v.(type) {
	case string:
		return stringValue(t), true
	case bool:
		b := t
		return otlpAnyValue{BoolValue: &b}, true
	case int64:
		s := strconv.FormatInt(t, 10)
		return otlpAnyValue{IntValue: &s}, true
	case int:
		s := strconv.FormatInt(int64(t), 10)
		return otlpAnyValue{IntValue: &s}, true
	case float64:
		f := t
		return otlpAnyValue{DoubleValue: &f}, true
	case []string:
		vals := make([]otlpAnyValue, 0, len(t))
		for _, e := range t {
			vals = append(vals, stringValue(e))
		}
		return otlpAnyValue{ArrayValue: &otlpArrayValue{Values: vals}}, true
	default:
		return otlpAnyValue{}, false
	}
}

func stringValue(s string) otlpAnyValue {
	v := s
	return otlpAnyValue{StringValue: &v}
}
