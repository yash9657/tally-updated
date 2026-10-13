// Package nlq is the NL->DSL translator adapter. It is the ONLY query-path
// package that touches the network — and since W0 it no longer speaks a
// provider's wire protocol itself: it hands a fully-formed prompt to
// internal/llm, which owns the transport, the five provider adapters, the
// per-tenant token budget and the trajectory span. nlq's remaining job is the
// one nothing else can do: it knows what the query catalogue is, what the
// compiler will reject, and which failures are the operator's fault.
//
// It depends on internal/query (for the typed Query it emits and the Catalog it
// advertises) and internal/llm, and NEVER the reverse — internal/query stays a
// network-free pure leaf, pinned by archtest, and internal/llm knows nothing
// about accounting.
//
// SAFETY MODEL (unchanged from Phase 3, and unchanged by the move). The LLM
// produces ONLY the typed DSL via forced tool-use; NEITHER this package NOR
// internal/llm does any whitelist validation of its own — internal/llm validates
// SHAPE (strict JSON decode against query.Query), never MEANING. Whatever Query
// this returns is re-validated by internal/query's deterministic compiler (the
// sole authority) and executed in a read-only, RLS-scoped transaction, so a
// hallucinated column or an injection payload smuggled into a DSL string field is
// simply rejected/parameterized downstream — it can never widen the boundary.
// TestToQuery_InjectionThroughFakeLLM_CompilerRejects proves that end to end by
// spraying hostile DSLs through a fake provider and requiring the COMPILER to be
// the gate.
//
// STDLIB-ONLY (deliberate, disclosed). Per IMPLEMENTATION_PLAN §1.1 this path
// speaks its provider's API over net/http with no vendor SDK — a conscious
// deviation from the usual "use the official SDK" guidance, taken to add zero new
// go.mod dependencies. Do not "fix" it by pulling in an SDK. The net/http code
// itself now lives one package down, in internal/llm; the rule is the same there.
package nlq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/llm"
	"github.com/khata/cored/internal/query"
)

// ErrTranslatorUnavailable is returned when the upstream Messages API cannot be
// reached or answers with any non-200 status (network error, timeout, 401/403/404
// misconfiguration, 413, 429, 5xx). The server maps it to a 503 — an OPERATOR
// fault ("retry or POST a typed dsl"), distinct from query.ErrUntranslatable
// (422, a USER fault: "rephrase your question"). Mislabeling a dead API key as a
// user's bad question is the failure this split prevents.
//
// ⚠️ W0 WIDENED WHAT PRODUCES IT WITHOUT CHANGING WHAT IT MEANS. The wording
// above says "Messages API" because that was the only upstream when it was
// written; the same sentence now covers whichever of internal/llm's five
// providers this deployment selected, plus two conditions that did not exist
// before — a tenant whose token budget is exhausted, and a deployment whose
// provider/model/credential triple internal/llm refused at wiring time. All of
// them are the same kind of fault to the same on-call person, and none of them
// is the question's fault. mapLLMError is where that judgement is made, once.
var ErrTranslatorUnavailable = errors.New("nlq: translator upstream unavailable")

// maxNLChars caps the natural-language input. Anything longer is rejected as
// untranslatable up front (a 422), before any network call.
//
// ⚠️ IT IS NOT MADE REDUNDANT BY llm.ErrInputTooLarge, and deleting it in favour
// of that check would change the answer a user gets. llm's limit is on the
// ENCODED REQUEST — prompt plus catalogue plus tool schema plus question, ~8.5 KB
// of which is ours before the user types a character — and blowing it is a
// DEPLOYMENT fault that maps to 503. A 900-character question is a USER fault
// that maps to 422, and it has to be recognised as one here, where the question
// is still a question rather than a slice of a JSON body.
const maxNLChars = 500

// DefaultBaseURL is the Messages API host. Overridden in tests via BaseURL (and
// in production via QUERY_TRANSLATOR_BASE_URL for a proxy/gateway).
//
// ⚠️ IT IS ANTHROPIC'S HOST AND IT IS STILL A CONSTANT OF THIS PACKAGE ONLY
// BECAUSE server.go PASSES IT UNCONDITIONALLY (`envOr("QUERY_TRANSLATOR_BASE_URL",
// nlq.DefaultBaseURL)`). Once the composition root learns to select a provider,
// it should pass "" instead and let the chosen llm adapter supply its own default
// host — otherwise selecting Fireworks would send a Fireworks request to
// api.anthropic.com and the failure would read as a 404 from a model id.
const DefaultBaseURL = "https://api.anthropic.com"

// DefaultModel is the translator model. claude-haiku-4-5 is a current, valid
// model id (verified against the Anthropic API reference). Overridable via
// QUERY_TRANSLATOR_MODEL.
//
// ⛔ IT IS NOT A BET ON CLAUDE, AND IT IS ALSO NOT A BET AGAINST IT. PLAN-AGENTIC
// W0 says NL→DSL translation moves to a cheap open-weight flash model, and W1's
// bake-off — not this file — decides which one. Until that decision has NUMBERS
// behind it, the default stays the model that every existing deployment is
// already answering questions with, because the alternative is that "we
// refactored the LLM layer" presents in the field as "NL query got worse the day
// we upgraded", with the cause two packages away from the symptom. Change this
// constant when the bake-off has a result, and change DefaultProvider with it.
const DefaultModel = "claude-haiku-4-5"

// DefaultProvider is the inference provider used when Translator.Provider is
// unset. See DefaultModel: the pair moves together or not at all, because a
// model id is meaningless without the provider that serves it.
const DefaultProvider = llm.ProviderAnthropic

// translatorFeature keys this call site in the token budget, the trajectory span
// and the W8 eval suite. llm.Client refuses a request without one.
const translatorFeature = "nlq.translate"

// translatorPromptVersion is what the CI eval gate pins an accuracy score to.
//
// ⚠️ BUMP IT WHENEVER staticPrompt() CHANGES MEANINGFULLY. v1 was the single
// undivided prompt string shipped by wave-2 WS1; v2 is the same text split into
// its cacheable and per-call halves (schema.go). The text the model reads is
// byte-identical between them — the version moved anyway, because the REQUEST
// shape changed and a score measured against one shape should not be silently
// credited to the other. llm.SpanStart also records a hash of the static half,
// which is the evidence when somebody edits a prompt and forgets this line.
// v3 (W3) is v2 plus three catalogue entities (stock_movements,
// bank_statement_lines, profile_facts) and rules 9-10: the text the model reads
// changed, so a v2 score must not be credited to it.
// v4 (W3 wave 2) feeds the semantic catalogue in: an ENTITY GUIDE and rule 11
// in Static (constant text, still cacheable), and per-question CATALOGUE
// HINTS — the reports catalogue.Select ranks, with an example shape — as a
// new Dynamic system message. This is the "with catalogue + selector" arm of
// W3's +15 pt gate; a v3 score is the "without" arm and must stay credited
// to v3.
const translatorPromptVersion = "nlq-dsl-v4"

// emitQueryTool is the single tool the model is forced to call. The name is
// referenced by the schema comment, the prompt text and the response decoder;
// it is a constant so those three cannot drift.
const emitQueryTool = "emit_query"

// translatorMaxTokens bounds the completion. A typed DSL for this catalogue is a
// few hundred tokens; 1024 is the figure this path has always used and a
// truncated tool call costs a repair round trip, which is dearer than the tokens.
const translatorMaxTokens = 1024

// translatorMaxRepairs is DELIBERATELY 1, not llm's default of 2.
//
// ⚠️ THIS IS AN INTERACTIVE PATH: a human is watching a query box. Every repair
// is a full billed round trip AND a full round trip of latency, so the bound that
// is right for a background agent step is not the bound that is right here. One
// repair buys the case that repair actually fixes — a model that answered with an
// extra field or a fenced code block — and stops short of spending 45 seconds
// discovering that a model which refused twice will refuse a third time.
const translatorMaxRepairs = 1

// translatorHTTPTimeout bounds one round trip. 15s, the figure this path shipped
// with, rather than llm.DefaultTimeout's 60s: see translatorMaxRepairs — a
// person is waiting.
const translatorHTTPTimeout = 15 * time.Second

// Translator is the production query.Translator: it turns a natural-language
// question into a typed query.Query by asking a model to call the emit_query tool
// (forced tool-use), through internal/llm. It is a drop-in swap for
// query.FakeTranslator with the same downstream safety guarantees.
//
// ⛔ IT MUST NOT BE COPIED after first use (it memoises its llm.Client behind a
// sync.Once). Every caller holds a *Translator — server.go builds one with &, the
// query.Translator assertion below is on the pointer — and `go vet`'s copylocks
// check is what enforces it if somebody stops.
type Translator struct {
	APIKey  string       // provider credential (required for this translator to be selected)
	Model   string       // QUERY_TRANSLATOR_MODEL; "" -> DefaultModel
	BaseURL string       // "" -> the provider adapter's default; QUERY_TRANSLATOR_BASE_URL overrides FOR TESTS/proxies
	Client  *http.Client // nil -> a translatorHTTPTimeout client

	// Provider selects which of internal/llm's adapters speaks for this
	// translator; "" means DefaultProvider. It is a field rather than a
	// constant because W1's bake-off will move it and because a deployment
	// running a self-hosted vLLM has to be able to say so.
	Provider llm.Provider

	// Recorder and Budget are the W0 seams, passed straight through to
	// llm.Config.
	//
	// ⚠️ NIL IS NOT A SILENT DEFAULT HERE ANY MORE THAN IT IS THERE: llm
	// resolves them to the NAMED types llm.DiscardRecorder and llm.NoBudget,
	// which exist precisely so that "this deployment records no trajectories
	// and caps no tokens" is a sentence somebody wrote in the composition root
	// rather than a field nobody filled in. This package cannot make that
	// decision for the composition root, so it only forwards.
	Recorder llm.Recorder
	Budget   llm.Budget

	// Now is the injected clock the PROMPT's resolved period windows are built
	// from; nil falls back to time.Now. ⛔ IT IS NOT OPTIONAL DECORATION: a
	// translator with no clock has to guess what "last quarter" means, which is
	// D21 on the production path. Tests freeze it so the prompt is reproducible.
	Now func() time.Time
	// FYStartMonth is the company financial-year start month (1-12); zero means
	// the Indian default of April, resolved by query.FYStartMonth so this
	// package never re-states the default.
	FYStartMonth int

	// once memoises the llm.Client (and the error from building it).
	//
	// ⚠️ THE ERROR IS MEMOISED TOO, ON PURPOSE. Every input to llm.New here is
	// process configuration — a key, a model id, a host — none of which changes
	// while the process runs, so a missing credential is a permanent condition
	// and re-deriving the same refusal on every question would only make the
	// failure look intermittent. Building once is also what stops this path
	// from minting a fresh *http.Client, and therefore a fresh connection pool,
	// on every question a tenant asks — which is what the code this replaced
	// did.
	once   sync.Once
	llmc   *llm.Client
	llmErr error
}

// ClaudeTranslator is the name this type shipped under, kept as an ALIAS so
// nothing outside the package breaks.
//
// ⚠️ THE RENAME IS THE HONEST HALF AND THE ALIAS IS THE COMPATIBLE HALF. The type
// is no longer Claude-specific — the same struct now drives Fireworks, DeepInfra,
// Sarvam or a self-hosted vLLM by setting one field — and a name that says
// "Claude" would have every future reader believe the provider is fixed. But
// internal/server constructs it by name and internal/server is another lane's
// file, so the old spelling must keep compiling and keep meaning exactly the same
// type. An alias (=) does that; a defined type (type ClaudeTranslator
// Translator) would NOT — it would be a distinct type with no methods, and
// server.go would fail to satisfy query.Translator.
type ClaudeTranslator = Translator

// now returns the injected clock instant, or time.Now() when Now is nil.
func (c *Translator) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// compile-time assertion that ClaudeTranslator satisfies the query.Translator
// interface (ToQuery(ctx, companyID, nl) (query.Query, error)).
var _ query.Translator = (*ClaudeTranslator)(nil)

func (c *Translator) model() string {
	if c.Model != "" {
		return c.Model
	}
	return DefaultModel
}

func (c *Translator) provider() llm.Provider {
	if c.Provider != "" {
		return c.Provider
	}
	return DefaultProvider
}

// llmClient builds (once) the configured provider client.
//
// ⛔ A CONFIGURATION REFUSAL FROM llm.New IS AN OPERATOR FAULT AND MUST REACH THE
// CALLER AS ONE. llm.New refuses an unknown provider, an empty model id and a
// missing credential — all of them deployment mistakes — and every one of them
// travels out of ToQuery as ErrTranslatorUnavailable (503, "retry or POST a typed
// dsl"), never as query.ErrUntranslatable (422, "rephrase your question"). There
// is no third option where the translator quietly degrades to FakeTranslator:
// that would answer a question with a deterministic keyword match while the
// operator believed an LLM was running, and nobody downstream could tell the two
// apart.
func (c *Translator) llmClient() (*llm.Client, error) {
	c.once.Do(func() {
		httpc := c.Client
		if httpc == nil {
			httpc = &http.Client{Timeout: translatorHTTPTimeout}
		}
		c.llmc, c.llmErr = llm.New(llm.Config{
			Provider:   c.provider(),
			Model:      c.model(),
			APIKey:     c.APIKey,
			BaseURL:    c.BaseURL,
			HTTP:       httpc,
			Now:        c.now,
			Budget:     c.Budget,
			Recorder:   c.Recorder,
			MaxRepairs: translatorMaxRepairs,
		})
	})
	return c.llmc, c.llmErr
}

// request builds the one llm.Request this translator ever makes.
//
// It is a separate method from ToQuery so a test can inspect the Static/Dynamic
// split without a network call — see TestStaticPromptIsClockIndependent, which is
// the detector for the cache contract llm.Request documents.
//
// ⛔ THE ONLY PER-REQUEST INPUT IS THE QUESTION, AND IT GOES IN THE USER MESSAGE.
// companyID is passed as llm.Request.TenantID, which scopes the budget and the
// trajectory span — and which llm.Client then uses as a NEEDLE: it scans the
// encoded body for that UUID and refuses to open a socket if it finds it. So
// handing the id over here does not put it in the prompt; it arms the check that
// proves it is not.
func (c *Translator) request(companyID uuid.UUID, nl string) *llm.Request {
	return &llm.Request{
		TenantID:      companyID,
		Feature:       translatorFeature,
		PromptVersion: translatorPromptVersion,

		// Static first and byte-identical across calls; Dynamic second. See
		// schema.go's header for why the examples are in the dynamic half.
		Static: []llm.Message{{Role: llm.RoleSystem, Content: staticPrompt()}},
		Dynamic: []llm.Message{
			{Role: llm.RoleSystem, Content: dynamicPrompt(c.now(), c.FYStartMonth)},
			// Per-question catalogue hints (schema.go hintsPrompt): after the
			// clock half, before the question — the last system text the
			// model reads is the part chosen for this question.
			{Role: llm.RoleSystem, Content: hintsPrompt(nl)},
			{Role: llm.RoleUser, Content: nl},
		},

		Tools: []llm.Tool{{
			Name:        emitQueryTool,
			Description: "Emit the single typed query that answers the user's question. Use entity \"\" if the catalog cannot answer it.",
			// Parameters, not a hand-rolled input_schema: the anthropic adapter
			// renames it on the wire, the OpenAI-compatible ones nest it under
			// function.parameters, and this package should never have known
			// which. It also arrives compacted, so a reformatted schema file
			// cannot cost every tenant a cache miss.
			Parameters: json.RawMessage(emitQuerySchema),
		}},
		// Forced tool use, not a json_schema: the Anthropic Messages API has no
		// response_format at all, and llm refuses a Schema on that provider
		// rather than dropping it silently. Forcing the tool is the shape that
		// works on every provider in the set.
		ToolChoice: llm.ToolChoice{Mode: llm.ToolChoiceNamed, Name: emitQueryTool},
		MaxTokens:  translatorMaxTokens,
	}
}

// ToQuery asks the configured model to emit a typed query.Query for the question
// nl. companyID scopes the budget and the trace — NO tenant data (company id,
// ledger or party names) is ever placed in the prompt; the only per-request input
// is the question text, carried verbatim in the user message.
//
// Error contract:
//   - nl empty or > maxNLChars                       -> query.ErrUntranslatable (422)
//   - misconfiguration, network error / timeout / any non-2xx status, or an
//     exhausted tenant token budget                  -> ErrTranslatorUnavailable (503)
//   - 2xx but no emit_query tool call (refusal), unknown fields, undecodable
//     input, after the bounded repair attempts       -> query.ErrUntranslatable (422)
//   - entity == ""                                    -> query.ErrUntranslatable (422)
func (c *Translator) ToQuery(ctx context.Context, companyID uuid.UUID, nl string) (query.Query, error) {
	// ⛔ BEFORE ANY NETWORK CALL, AND BEFORE THE CLIENT IS EVEN BUILT. An
	// oversized or empty question is answerable without asking anybody, and
	// TestToQuery_NLTooLong asserts the fake provider is never hit.
	if l := len(nl); l == 0 || l > maxNLChars {
		return query.Query{}, query.ErrUntranslatable
	}

	client, err := c.llmClient()
	if err != nil {
		return query.Query{}, mapLLMError(err)
	}

	// DecodeInto is where the strict decode lives: DisallowUnknownFields into a
	// scratch value, so a hallucinated shape is rejected rather than silently
	// dropping data, and a failed decode leaves q untouched rather than half
	// filled. Both properties moved from this file into llm.decodeStrict with
	// the comments that explain them.
	var q query.Query
	if _, err := client.DecodeInto(ctx, c.request(companyID, nl), emitQueryTool, &q); err != nil {
		return query.Query{}, mapLLMError(err)
	}

	// The documented escape hatch: entity "" means "cannot answer from the
	// catalog". ⛔ IT IS CHECKED HERE AND NOT REPAIRED. An empty entity is a
	// well-formed answer that decodes perfectly, so llm has no way to know it is
	// a refusal — and if it did know, repairing it would be badgering the model
	// into answering a question the prompt's own REFUSE rules told it not to.
	if q.Entity == "" {
		return query.Query{}, query.ErrUntranslatable
	}
	return q, nil
}

// mapLLMError translates internal/llm's fault taxonomy into this package's
// two-way split. It is the whole of the 503-vs-422 contract, in one place.
//
// ⛔ THE ONE ERROR THAT BECOMES A 422 IS llm.ErrNoStructuredOutput, because it is
// the only one that means "the call was served, was billed, and produced nothing
// usable" — a MODEL fault, which from the user's seat is indistinguishable from a
// question the catalogue cannot express, and for which "rephrase" is genuinely
// the right advice. Everything else — a dead key, a retired model id, a 429, a
// transport failure, an exhausted tenant budget, a prompt that outgrew the input
// cap, a tenant id that leaked into the body — is an OPERATOR or DEPLOYMENT
// fault, and telling the user to rephrase a perfectly good question because
// somebody let a credential expire is the exact failure ErrTranslatorUnavailable
// was introduced to prevent.
//
// ⚠️ THE DEFAULT ARM IS 503, DELIBERATELY. An error this function does not
// recognise is, by definition, not evidence that the user's question was bad. So
// the unclassified case fails toward the answer that says "something on our side
// went wrong, retry or POST a typed dsl" and names itself in the message, rather
// than toward the answer that blames the person typing. If a new llm sentinel
// ever belongs on the 422 side, it has to be added above, on purpose.
func mapLLMError(err error) error {
	if errors.Is(err, llm.ErrNoStructuredOutput) {
		// Deliberately NOT wrapped with the llm detail: this is the arm that
		// reaches a user as "I could not understand that question", and the
		// provider's decoder complaint is trajectory material, not UI copy.
		return query.ErrUntranslatable
	}
	switch {
	case errors.Is(err, llm.ErrUpstreamUnavailable),
		errors.Is(err, llm.ErrBudgetExhausted),
		errors.Is(err, llm.ErrNotConfigured),
		errors.Is(err, llm.ErrInputTooLarge),
		errors.Is(err, llm.ErrTenantIDInPrompt):
		return fmt.Errorf("%w: %v", ErrTranslatorUnavailable, err)
	default:
		return fmt.Errorf("%w: unclassified llm failure: %v", ErrTranslatorUnavailable, err)
	}
}
