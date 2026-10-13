package llm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	// DefaultTimeout bounds one HTTP round trip. Longer than internal/nlq's 15s
	// because a planner step with a large static prefix and a reasoning budget
	// legitimately takes longer than a single-tool translation.
	DefaultTimeout = 60 * time.Second

	// DefaultMaxRepairs is the schema-repair bound from W0 ("retries with schema
	// repair (≤2)"). ⛔ It is a BOUND, not a target: each repair is a full
	// billed call, so an unbounded loop is an unbounded bill as well as an
	// unbounded latency. Response.Repairs makes the count observable, because a
	// bound nobody can read is a bound nobody can trust.
	DefaultMaxRepairs = 2

	// DefaultMaxInputBytes caps the ENCODED request at 64 KiB. Sized to be
	// generous for a system prompt plus tool schemas plus a question, and small
	// enough that a runaway caller (a whole ledger pasted into a prompt) is
	// refused here rather than discovered on an invoice.
	DefaultMaxInputBytes = 1 << 16

	// MaxResponseBytes bounds the read back. insightd_proxy.go:139 sets the
	// same 4 MiB for the same reason: anything past this is a misconfigured URL
	// answering with something else entirely, and that must not become an
	// unbounded read into cored's memory.
	MaxResponseBytes = 4 << 20
)

// Config is everything a Client needs. Every optional field has a documented
// default; every field with no safe default is refused by New.
type Config struct {
	Provider Provider
	Model    string
	APIKey   string
	// BaseURL overrides the adapter's default. Required for ProviderVLLM,
	// which has no default host — see adapter_vllm.go.
	BaseURL string

	HTTP *http.Client

	// Now is the injected clock. ⛔ NOT OPTIONAL DECORATION —
	// internal/nlq/translate.go:66 spells out why an LLM caller with no
	// injected clock produces prompts and traces nobody can reproduce. Here it
	// stamps span timing and Response.Latency, so a test asserts on a duration
	// without sleeping. Nil falls back to time.Now, matching nlq, so an
	// ordinary caller is not forced to state the obvious.
	Now func() time.Time

	// Budget nil means NoBudget — a named, greppable "this deployment has no
	// token cap" rather than an absent one. See budget.go.
	Budget Budget
	// Recorder nil means DiscardRecorder. See trace.go: the composition root is
	// expected to say out loud that it is discarding trajectories.
	Recorder Recorder

	// MaxRepairs zero means DefaultMaxRepairs. Negative means zero repairs,
	// which is a legitimate setting for a caller that would rather fail fast.
	MaxRepairs int
}

// Client is one configured (provider, model) pair. Safe for concurrent use: it
// holds no mutable state, and *http.Client is itself concurrent-safe.
type Client struct {
	adapter    Adapter
	model      string
	apiKey     string
	baseURL    string
	http       *http.Client
	now        func() time.Time
	budget     Budget
	recorder   Recorder
	maxRepairs int
}

// New validates the configuration and refuses anything it cannot honour.
//
// ⛔ EVERY REFUSAL HERE IS LOUD AND HAPPENS AT WIRING TIME. The alternative —
// tolerate a missing key and fail at the first call — turns a deployment mistake
// into a runtime incident that reads like a provider outage, at whatever hour
// the first tenant asks a question. insightd_proxy.go:120 makes the same choice
// for insightd's service token.
func New(cfg Config) (*Client, error) {
	adapter, ok := AdapterFor(cfg.Provider)
	if !ok {
		return nil, fmt.Errorf("%w: unknown provider %q (known: %v)",
			ErrNotConfigured, cfg.Provider, KnownProviders)
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("%w: provider %s with no model id", ErrNotConfigured, cfg.Provider)
	}

	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = strings.TrimRight(adapter.DefaultBaseURL(), "/")
	}
	if baseURL == "" {
		// Only vLLM reaches here, and only when nobody said where it runs.
		return nil, fmt.Errorf("%w: provider %s has no default base URL — set one "+
			"(there is no such thing as \"the\" host for a server we run ourselves)",
			ErrNotConfigured, cfg.Provider)
	}

	// ⚠️ THE ONE PROVIDER EXEMPTED FROM NEEDING A CREDENTIAL IS vLLM, and it is
	// exempted because a vLLM started without --api-key genuinely has none —
	// not because an empty key is ever acceptable for a hosted provider. The
	// exemption is narrow and named so it cannot quietly widen.
	if strings.TrimSpace(cfg.APIKey) == "" && cfg.Provider != ProviderVLLM {
		return nil, fmt.Errorf("%w: provider %s", ErrNotConfigured, cfg.Provider)
	}

	c := &Client{
		adapter:    adapter,
		model:      cfg.Model,
		apiKey:     cfg.APIKey,
		baseURL:    baseURL,
		http:       cfg.HTTP,
		now:        cfg.Now,
		budget:     cfg.Budget,
		recorder:   cfg.Recorder,
		maxRepairs: cfg.MaxRepairs,
	}
	if c.http == nil {
		c.http = &http.Client{Timeout: DefaultTimeout}
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.budget == nil {
		c.budget = NoBudget{}
	}
	if c.recorder == nil {
		c.recorder = DiscardRecorder{}
	}
	if c.maxRepairs == 0 {
		c.maxRepairs = DefaultMaxRepairs
	}
	if c.maxRepairs < 0 {
		c.maxRepairs = 0
	}
	return c, nil
}

// Provider and Model expose what this Client was wired to, for the caller that
// wants to log or route on it. Read-only: a Client is immutable after New.
func (c *Client) Provider() Provider { return c.adapter.Provider() }
func (c *Client) Model() string      { return c.model }

// Capabilities is what the provider behind this Client can do. A caller that
// wants to CHOOSE between a schema and a grammar asks here; a caller that just
// sets one and hopes gets a refusal from Encode.
func (c *Client) Capabilities() Capability { return c.adapter.Capabilities() }

// Complete makes exactly one provider call.
//
// The order of operations is the contract, and each step is where it is because
// of what goes wrong if it moves:
//
//  1. validate         — an untraceable call is refused before anything else.
//  2. encode           — PURE. Doing it first means a capability refusal (no
//     grammar mode, no json_schema) costs no budget and no
//     socket.
//  3. size gate        — BEFORE the network, in the shape nlq/translate.go:41
//     uses. Proved by TestOversizedInputMakesNoNetworkCall.
//  4. tenant-id gate   — on the ENCODED BYTES, so it catches the id whichever
//     field smuggled it. See ErrTenantIDInPrompt.
//  5. reserve          — after every free refusal, so a rejected request never
//     spends a tenant's cap.
//  6. span             — opened around the network call only, PLUS one span
//     that opens and closes immediately when step 5 REFUSES
//     (see there: a tenant at its cap is otherwise the one
//     failure the trace store cannot show).
//  7. the call         — bounded read, adapter decode.
//  8. settle or release — exactly one, deferred, on every path.
func (c *Client) Complete(ctx context.Context, req *Request) (*Response, error) {
	if req == nil {
		return nil, fmt.Errorf("llm: nil request")
	}
	if err := validateRequest(req); err != nil {
		return nil, err
	}

	body, err := c.adapter.Encode(c.model, req)
	if err != nil {
		return nil, err
	}

	limit := req.MaxInputBytes
	if limit <= 0 {
		limit = DefaultMaxInputBytes
	}
	if len(body) > limit {
		return nil, fmt.Errorf("%w: %d encoded bytes exceeds the %d-byte limit for feature %q",
			ErrInputTooLarge, len(body), limit, req.Feature)
	}

	// ⛔ THE TENANT-ID GATE. Scanned on the encoded body rather than on the
	// Request, so it sees an id smuggled through a message, a tool schema, an
	// exemplar or a stringified struct. Both spellings, because a UUID reaches
	// a prompt as often through fmt.Sprint of a struct (hyphenated) as through
	// a database key that dropped the hyphens.
	if req.TenantID != uuid.Nil {
		hyphenated := req.TenantID.String()
		bare := strings.ReplaceAll(hyphenated, "-", "")
		if bytes.Contains(body, []byte(hyphenated)) || bytes.Contains(body, []byte(bare)) {
			return nil, fmt.Errorf("%w (feature %q): remove it from the prompt — "+
				"a tenant id in a third party's retention window is a cross-tenant correlation key",
				ErrTenantIDInPrompt, req.Feature)
		}
	}

	est := estimateTokens(len(body), req.MaxTokens)
	reservation, err := c.budget.Reserve(ctx, req.TenantID, req.Feature, est)
	if err != nil {
		// ErrBudgetExhausted travels out untouched: never retried, never
		// downgraded. See errors.go.
		//
		// ⛔ BUT IT LEAVES A SPAN, OPENED AND CLOSED RIGHT HERE. A refused
		// reservation used to return before any span existed, so the ONE
		// failure an operator goes to the trace store to diagnose — "this
		// tenant's assistant stopped answering and every provider dashboard is
		// green" — was the one failure with no row in it. The refusal was
		// visible only in the caller's HTTP status, which is the same status a
		// dozen other faults produce.
		//
		// ⚠️ IT IS A SEPARATE, IMMEDIATELY-CLOSED SPAN RATHER THAN MOVING THE
		// SPAN ABOVE Reserve, and the difference is what a chat span's duration
		// MEANS. Wrapping Reserve would fold a database round trip into every
		// served call's model latency, so a p99 built on these spans would
		// blend provider latency with budget-table contention — two numbers with
		// two different remedies. A refusal span carries no ModelResponse and no
		// duration anybody reads; a served span still measures exactly the
		// network call it always measured.
		_, span := c.recorder.StartLLMCall(ctx, c.spanStart(req))
		span.End(nil, err)
		return nil, err
	}

	// ⛔ EXACTLY ONE OF Settle OR Release, ON EVERY PATH INCLUDING PANIC.
	//
	// `settled` is flipped only by the success path below, so this defer covers
	// the transport error, the non-2xx, the decode failure, the cancelled
	// context and the panic. It is a defer rather than a call at each return
	// site because there are seven return sites and the eighth one somebody
	// adds is the one that leaks a reservation forever.
	//
	// ⚠️ context.WithoutCancel: releasing a reservation is bookkeeping that
	// MUST run after the request's context has been cancelled — which is
	// precisely when it matters most, because a cancelled call is the one whose
	// tokens would otherwise stay reserved until the month rolled over.
	settled := false
	defer func() {
		if settled {
			return
		}
		if rerr := reservation.Release(context.WithoutCancel(ctx)); rerr != nil {
			// A stated degradation, never a silent one: the reservation is
			// stuck and somebody has to know. It is not returned as the
			// request's error because the request's own failure is the more
			// useful thing to report.
			log.Printf("llm: budget release failed for feature %q: %v", req.Feature, rerr)
		}
	}()

	spanCtx, span := c.recorder.StartLLMCall(ctx, c.spanStart(req))

	var (
		resp    *Response
		callErr error
	)
	// The span closes with whatever we ended up with — including a Response
	// that carries only a fingerprint alongside an error. See LLMSpan.
	defer func() { span.End(resp, callErr) }()

	started := c.now()
	resp, callErr = c.do(spanCtx, body)
	if resp != nil {
		resp.Latency = c.now().Sub(started)
		resp.Fingerprint.RequestedModel = c.model
		if resp.Fingerprint.Provider == "" {
			resp.Fingerprint.Provider = c.adapter.Provider()
		}
	}
	if callErr != nil {
		// A served-but-unusable response (a refusal) still settles: the tokens
		// were spent. A transport failure has no usage to settle, so it
		// releases. The discriminator is whether we got a body back at all.
		if resp != nil && resp.Usage.Total() > 0 {
			c.settle(ctx, reservation, resp.Usage, req.Feature)
			settled = true
		}
		return resp, callErr
	}

	c.settle(ctx, reservation, resp.Usage, req.Feature)
	settled = true
	return resp, nil
}

// spanStart is the content-free description of this call, and it exists so the
// SERVED span and the budget-REFUSAL span above cannot describe the same call
// differently. Two struct literals five lines apart is how one of them ends up
// missing the prompt hash after somebody adds a field.
func (c *Client) spanStart(req *Request) SpanStart {
	return SpanStart{
		TenantID:      req.TenantID,
		Feature:       req.Feature,
		PromptVersion: req.PromptVersion,
		PromptHash:    StaticPromptHash(req),
		Provider:      c.adapter.Provider(),
		ModelID:       c.model,
	}
}

// settle records the served usage. A settle failure is a METERING fault, not a
// request failure: the call succeeded and the caller is owed its answer. So it
// is logged as a stated degradation rather than swallowed (rule: never a silent
// fallback) and never turned into an error that would discard a paid-for
// response.
func (c *Client) settle(ctx context.Context, r Reservation, u Usage, feature string) {
	if err := r.Settle(context.WithoutCancel(ctx), u); err != nil {
		log.Printf("llm: budget settle failed for feature %q (%d prompt + %d completion tokens went uncounted): %v",
			feature, u.PromptTokens, u.CompletionTokens, err)
	}
}

// do performs one HTTP round trip and hands the bytes to the adapter.
func (c *Client) do(ctx context.Context, body []byte) (*Response, error) {
	url := c.baseURL + c.adapter.Path()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUpstreamUnavailable, err)
	}
	c.adapter.SetAuth(httpReq.Header, c.apiKey)

	httpResp, err := c.http.Do(httpReq)
	if err != nil {
		// Transport failures and context cancellation both land here. Both are
		// operator-visible, neither is the model's fault.
		return nil, fmt.Errorf("%w: %v", ErrUpstreamUnavailable, err)
	}
	defer httpResp.Body.Close()

	// Bounded read. insightd_proxy.go:139 is the precedent; the failure it
	// prevents is a misconfigured URL answering with a 4 GB file.
	raw, err := io.ReadAll(io.LimitReader(httpResp.Body, MaxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("%w: reading response body: %v", ErrUpstreamUnavailable, err)
	}
	return c.adapter.Decode(httpResp.StatusCode, httpResp.Header, raw)
}

// DecodeInto runs Complete and decodes the model's structured output into v,
// repairing at most MaxRepairs times.
//
// toolName selects where the payload comes from: a named tool call when it is
// non-empty (the forced-tool-use path internal/nlq uses today), otherwise the
// message text (the json_schema path).
//
// v must be a non-nil pointer. On success v holds the decoded value; ⛔ ON
// FAILURE v IS UNTOUCHED — see decodeStrict. The returned *Response is the LAST
// response, which is non-nil even when the error is ErrNoStructuredOutput,
// because its Fingerprint is what a canary needs to attribute the failure to a
// model. Callers check the error first.
func (c *Client) DecodeInto(ctx context.Context, req *Request, toolName string, v any) (*Response, error) {
	if v == nil {
		return nil, fmt.Errorf("llm: DecodeInto with a nil target")
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return nil, fmt.Errorf("llm: DecodeInto needs a non-nil pointer, got %T", v)
	}

	attempt := req
	var last *Response
	var lastErr error

	// maxRepairs REPAIRS means maxRepairs+1 CALLS. The off-by-one is worth
	// stating: TestDecodeInto_RepairsAtMostTwice counts requests on the server
	// and asserts exactly 3, which is the property an operator cares about
	// (how many times will this bill me?) rather than the one the variable is
	// named after.
	for i := 0; i <= c.maxRepairs; i++ {
		resp, err := c.Complete(ctx, attempt)
		if resp != nil {
			resp.Repairs = i
		}
		last = resp
		if err != nil {
			// ⛔ A TRANSPORT FAILURE IS NOT REPAIRED. Repair exists for a model
			// that produced the wrong SHAPE; re-sending after a 503 or a
			// cancelled context is a retry policy, and a retry policy hidden
			// inside a repair loop is how a provider outage turns into three
			// times the load at the worst moment. Budget exhaustion likewise
			// travels straight out.
			return resp, err
		}

		payload, perr := structuredPayload(resp, toolName)
		if perr == nil {
			if derr := decodeStrict(payload, v); derr == nil {
				return resp, nil
			} else {
				perr = derr
			}
		}
		lastErr = perr
		if i == c.maxRepairs {
			break
		}
		attempt = req.withRepair(repairInstruction(toolName, perr))
	}

	if last != nil {
		last.Repairs = c.maxRepairs
	}
	return last, fmt.Errorf("%w: after %d repair attempt(s): %v", ErrNoStructuredOutput, c.maxRepairs, lastErr)
}

// structuredPayload pulls the raw JSON the caller asked for out of a response.
func structuredPayload(resp *Response, toolName string) (json.RawMessage, error) {
	if resp == nil {
		return nil, fmt.Errorf("no response")
	}
	if toolName != "" {
		tc, ok := resp.ToolCallNamed(toolName)
		if !ok {
			// The refusal case, and the "model answered in prose" case, and
			// the "model invented a different tool" case. All three are the
			// same remedy.
			return nil, fmt.Errorf("no tool call named %q (finish_reason %q, %d tool call(s))",
				toolName, resp.FinishReason, len(resp.ToolCalls))
		}
		return tc.Arguments, nil
	}
	if strings.TrimSpace(resp.Text) == "" {
		return nil, fmt.Errorf("empty text response (finish_reason %q)", resp.FinishReason)
	}
	return json.RawMessage(resp.Text), nil
}

// decodeStrict decodes raw into v with DisallowUnknownFields, into a SCRATCH
// value that is copied over v only on success.
//
// ⛔ DisallowUnknownFields IS THE PROPERTY THAT MUST SURVIVE THE MOVE FROM
// internal/nlq (translate.go:229). A hallucinated shape must not silently drop
// data: a model that answers {"entity":"vouchers","filter":…} when the type says
// "filters" would otherwise produce a perfectly valid Query with NO filters —
// an answer about the whole ledger, presented as an answer about one party.
//
// ⛔ THE SCRATCH VALUE IS THE SECOND HALF OF THE SAME GUARANTEE. encoding/json
// populates fields as it walks, so a decode that fails on field seven has
// already written fields one to six. Returning an error while leaving a
// half-filled struct behind is how "on exhaustion, never a partial value"
// becomes a partial value that a caller with a sloppy error check ships to a
// tenant.
func decodeStrict(raw json.RawMessage, v any) error {
	scratch := reflect.New(reflect.TypeOf(v).Elem())
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(scratch.Interface()); err != nil {
		return err
	}
	// Trailing content after the value is a malformed answer, not a valid one
	// with a suffix: a model that emitted two JSON objects has produced
	// something nobody can act on, and taking the first silently picks one.
	if dec.More() {
		return fmt.Errorf("trailing content after the JSON value")
	}
	reflect.ValueOf(v).Elem().Set(scratch.Elem())
	return nil
}

// repairInstruction is the turn appended to Dynamic on a repair.
//
// It quotes the DECODER's complaint, which is the model's own output described
// back to it — never tenant data, because the error text comes from
// encoding/json and names fields, not values.
func repairInstruction(toolName string, cause error) string {
	var b strings.Builder
	b.WriteString("Your previous answer could not be used. ")
	if toolName != "" {
		b.WriteString("You must call the tool named ")
		b.WriteString(toolName)
		b.WriteString(" with arguments that match its schema exactly. ")
	} else {
		b.WriteString("You must answer with a single JSON value matching the schema exactly. ")
	}
	b.WriteString("Use only the fields the schema declares — no extra fields, no commentary, no code fences. ")
	b.WriteString("The error was: ")
	b.WriteString(cause.Error())
	return b.String()
}

// validateRequest refuses a call nobody could trace or bill.
//
// ⚠️ Feature AND PromptVersion ARE MANDATORY, and that is not paperwork. Feature
// is the budget key and the eval-suite key; PromptVersion is what the CI gate
// (W8 Tier 1) pins a score to. A call with neither is a call that cannot be
// regression-tested, cannot be attributed to a tenant's spend, and cannot be
// rolled back when a prompt change moves an accuracy number — and the moment
// they are optional, the first caller written in a hurry omits them.
func validateRequest(req *Request) error {
	if strings.TrimSpace(req.Feature) == "" {
		return fmt.Errorf("llm: request has no Feature — it would be untraceable and unbillable")
	}
	if strings.TrimSpace(req.PromptVersion) == "" {
		return fmt.Errorf("llm: request for feature %q has no PromptVersion — the eval gate has nothing to pin a score to", req.Feature)
	}
	if len(req.Static) == 0 && len(req.Dynamic) == 0 {
		return fmt.Errorf("llm: request for feature %q has no messages", req.Feature)
	}
	return nil
}

// estimateTokens is the pre-call estimate the budget reserves against.
//
// ⚠️ FOUR BYTES PER TOKEN IS A RULE OF THUMB AND IS DOCUMENTED AS ONE. It
// over-counts English and under-counts Devanagari, which matters for a Hinglish
// product — a Hindi prompt can run closer to two bytes per token, so this
// UNDER-reserves there. That is tolerable only because Settle replaces the
// estimate with the served figure within a second or two; it would not be
// tolerable as a final count. If the bake-off (W1) shows the drift costing real
// over-runs, the fix is a per-provider tokeniser estimate on the Adapter, not a
// fudge factor here.
func estimateTokens(encodedBytes, maxTokens int) int {
	est := encodedBytes / 4
	if maxTokens > 0 {
		est += maxTokens
	} else {
		// An unbounded completion still has to reserve something for the
		// output, or a feature that never sets MaxTokens reserves only its
		// input and overruns on every call.
		est += 1024
	}
	return est
}

// StaticPromptHash is the fingerprint of the cacheable prefix: the Static
// messages, the tool schemas, the grammar and the schema name. Exported because
// the trajectory logger records it and an eval harness compares it.
//
// ⚠️ SHA-256 AND NOT blake3, ALTHOUGH THE REPO HAS blake3 AS A DEPENDENCY. The
// voucher hash chain uses blake3 because it is a tamper-evidence structure with
// a stated algorithm; this is a content fingerprint for debugging and drift
// detection, where the value is that anybody can recompute it with the standard
// library in any language. They are not the same job and should not share a
// primitive by proximity.
//
// It deliberately does NOT hash the Dynamic messages: the point of the number is
// to answer "did the shared prefix change?", and a hash that changed on every
// call would answer nothing.
func StaticPromptHash(req *Request) string {
	h := sha256.New()
	for _, m := range req.Static {
		fmt.Fprintf(h, "%s\x00%s\x00", m.Role, m.Content)
	}
	for _, t := range req.Tools {
		fmt.Fprintf(h, "tool\x00%s\x00%s\x00%s\x00", t.Name, t.Description, compactJSON(t.Parameters))
	}
	if req.Schema != nil {
		fmt.Fprintf(h, "schema\x00%s\x00%t\x00%s\x00", req.Schema.Name, req.Schema.Strict, compactJSON(req.Schema.Schema))
	}
	if req.Grammar != "" {
		fmt.Fprintf(h, "grammar\x00%s\x00", req.Grammar)
	}
	return hex.EncodeToString(h.Sum(nil))
}
