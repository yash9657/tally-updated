// Package nlq is the real-Claude NL->DSL translator adapter. It is the ONLY
// query-path package that touches the network. It depends on internal/query
// (for the typed Query it emits and the Catalog it advertises) and NEVER the
// reverse — internal/query stays a network-free pure leaf, pinned by archtest.
//
// SAFETY MODEL (unchanged from Phase 3). The LLM produces ONLY the typed DSL via
// forced tool-use; this package does no whitelist validation of its own. Whatever
// Query it returns is re-validated by internal/query's deterministic compiler
// (the sole authority) and executed in a read-only, RLS-scoped transaction, so a
// hallucinated column or an injection payload smuggled into a DSL string field is
// simply rejected/parameterized downstream — it can never widen the boundary.
//
// STDLIB-ONLY (deliberate, disclosed). Per IMPLEMENTATION_PLAN §1.1 this adapter
// speaks the Messages API over net/http with no Anthropic Go SDK — a conscious
// deviation from the usual "use the official SDK" guidance, taken to add zero new
// go.mod dependencies. Do not "fix" it by pulling in an SDK.
package nlq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/khata/cored/internal/query"
)

// ErrTranslatorUnavailable is returned when the upstream Messages API cannot be
// reached or answers with any non-200 status (network error, timeout, 401/403/404
// misconfiguration, 413, 429, 5xx). The server maps it to a 503 — an OPERATOR
// fault ("retry or POST a typed dsl"), distinct from query.ErrUntranslatable
// (422, a USER fault: "rephrase your question"). Mislabeling a dead API key as a
// user's bad question is the failure this split prevents.
var ErrTranslatorUnavailable = errors.New("nlq: translator upstream unavailable")

// maxNLChars caps the natural-language input. Anything longer is rejected as
// untranslatable up front (a 422), before any network call.
const maxNLChars = 500

const anthropicVersion = "2023-06-01"

// DefaultBaseURL is the Messages API host. Overridden in tests via BaseURL (and
// in production via QUERY_TRANSLATOR_BASE_URL for a proxy/gateway).
const DefaultBaseURL = "https://api.anthropic.com"

// DefaultModel is the translator model. claude-haiku-4-5 is a current, valid
// model id (verified against the Anthropic API reference). Overridable via
// QUERY_TRANSLATOR_MODEL.
const DefaultModel = "claude-haiku-4-5"

// ClaudeTranslator is the production Translator: it turns a natural-language
// question into a typed query.Query by asking Claude to call the emit_query tool
// (forced tool-use). It satisfies query.Translator, so it is a drop-in swap for
// FakeTranslator with the same downstream safety guarantees.
type ClaudeTranslator struct {
	APIKey  string       // ANTHROPIC_API_KEY (required for this translator to be selected)
	Model   string       // QUERY_TRANSLATOR_MODEL; "" -> DefaultModel
	BaseURL string       // "" -> DefaultBaseURL; QUERY_TRANSLATOR_BASE_URL overrides FOR TESTS/proxies
	Client  *http.Client // "" -> a 15s-timeout client

	// Now is the injected clock the PROMPT's resolved period windows are built
	// from; nil falls back to time.Now. ⛔ IT IS NOT OPTIONAL DECORATION: a
	// translator with no clock has to guess what "last quarter" means, which is
	// D21 on the production path. Tests freeze it so the prompt is reproducible.
	Now func() time.Time
	// FYStartMonth is the company financial-year start month (1-12); zero means
	// the Indian default of April, resolved by query.FYStartMonth so this
	// package never re-states the default.
	FYStartMonth int
}

// now returns the injected clock instant, or time.Now() when Now is nil.
func (c *ClaudeTranslator) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// compile-time assertion that ClaudeTranslator satisfies the query.Translator
// interface (ToQuery(ctx, companyID, nl) (query.Query, error)).
var _ query.Translator = (*ClaudeTranslator)(nil)

func (c *ClaudeTranslator) model() string {
	if c.Model != "" {
		return c.Model
	}
	return DefaultModel
}

func (c *ClaudeTranslator) baseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return DefaultBaseURL
}

func (c *ClaudeTranslator) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// --- Messages API wire shapes (minimal; only the fields we send/read) ---

type messagesRequest struct {
	Model      string           `json:"model"`
	MaxTokens  int              `json:"max_tokens"`
	System     string           `json:"system"`
	Tools      []toolDef        `json:"tools"`
	ToolChoice toolChoice       `json:"tool_choice"`
	Messages   []messageContent `json:"messages"`
}

type toolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type toolChoice struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

type messageContent struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type messagesResponse struct {
	StopReason string         `json:"stop_reason"`
	Content    []contentBlock `json:"content"`
}

type contentBlock struct {
	Type  string          `json:"type"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ToQuery asks Claude to emit a typed query.Query for the question nl. companyID
// is used ONLY for potential logging/tracing — NO tenant data (company id,
// ledger or party names) is ever placed in the prompt; the only per-request input
// is the question text, carried verbatim in the user message.
//
// Error contract:
//   - nl empty or > maxNLChars                       -> query.ErrUntranslatable (422)
//   - network error / timeout / any non-200 status   -> ErrTranslatorUnavailable (503)
//   - 200 but no emit_query tool_use block (refusal), unknown fields, undecodable
//     input, or entity == ""                          -> query.ErrUntranslatable (422)
func (c *ClaudeTranslator) ToQuery(ctx context.Context, _ uuid.UUID, nl string) (query.Query, error) {
	if l := len(nl); l == 0 || l > maxNLChars {
		return query.Query{}, query.ErrUntranslatable
	}

	reqBody := messagesRequest{
		Model:     c.model(),
		MaxTokens: 1024,
		System:    systemPrompt(c.now(), c.FYStartMonth),
		Tools: []toolDef{{
			Name:        "emit_query",
			Description: "Emit the single typed query that answers the user's question. Use entity \"\" if the catalog cannot answer it.",
			InputSchema: json.RawMessage(emitQuerySchema),
		}},
		ToolChoice: toolChoice{Type: "tool", Name: "emit_query"},
		Messages:   []messageContent{{Role: "user", Content: nl}},
	}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		// A marshal failure is an internal fault, not a user one.
		return query.Query{}, fmt.Errorf("%w: %v", ErrTranslatorUnavailable, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL()+"/v1/messages", bytes.NewReader(raw))
	if err != nil {
		return query.Query{}, fmt.Errorf("%w: %v", ErrTranslatorUnavailable, err)
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("x-api-key", c.APIKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	resp, err := c.client().Do(httpReq)
	if err != nil {
		return query.Query{}, fmt.Errorf("%w: %v", ErrTranslatorUnavailable, err)
	}
	defer resp.Body.Close()

	// ANY non-200 is an operator/config fault -> 503. 422 is reserved strictly for
	// a 200 whose body cannot be turned into a valid Query. (401/403/404/413 are
	// bad key / permission / bad model id / oversized request — never a user's
	// fault to be told to "rephrase".)
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return query.Query{}, fmt.Errorf("%w: upstream status %d", ErrTranslatorUnavailable, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return query.Query{}, fmt.Errorf("%w: %v", ErrTranslatorUnavailable, err)
	}

	var mr messagesResponse
	if err := json.Unmarshal(body, &mr); err != nil {
		// A 200 with an undecodable envelope is treated as untranslatable (the
		// model produced nothing usable), not an upstream outage.
		return query.Query{}, query.ErrUntranslatable
	}

	// Find the single emit_query tool_use block. A refusal (stop_reason
	// "refusal") or any 200 without one lands as untranslatable -> 422.
	var input json.RawMessage
	for _, blk := range mr.Content {
		if blk.Type == "tool_use" && blk.Name == "emit_query" {
			input = blk.Input
			break
		}
	}
	if len(input) == 0 {
		return query.Query{}, query.ErrUntranslatable
	}

	// Strict decode: unknown fields are rejected so a hallucinated shape does not
	// silently drop data. The compiler remains the authority on identifiers.
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()
	var q query.Query
	if err := dec.Decode(&q); err != nil {
		return query.Query{}, query.ErrUntranslatable
	}
	// The documented escape hatch: entity "" means "cannot answer from the
	// catalog".
	if q.Entity == "" {
		return query.Query{}, query.ErrUntranslatable
	}
	return q, nil
}
