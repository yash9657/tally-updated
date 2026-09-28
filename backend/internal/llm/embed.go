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
	"sort"
	"strings"

	"github.com/google/uuid"
)

// embed.go is the EMBEDDINGS surface (W3 wave 2: halfvec(1024) retrieval over
// narrations, parties, items, catalogue and golden queries). It is a second
// entry point on the same Client, not a second client, so every wall Complete
// has holds here too, in the same order: validate → encode (pure) → size gate
// → tenant-id gate on the ENCODED bytes → reserve → span → call → settle or
// release, exactly one, on every path.
//
// ⛔ THE SURFACE IS PER PROVIDER AND CLOSED. Only the providers in embedPaths
// serve an OpenAI-compatible /embeddings route; Anthropic has none, and
// Sarvam's is not OpenAI-shaped. Embed on any other provider is ErrNotConfigured
// at the first call, naming the provider — never a chat call pretending.
//
// ⛔ CONTENT IS NEVER A SPAN ATTRIBUTE (the trajectory rule). The span carries
// a hash of the inputs as its "prompt hash", so a cassette can key on it and
// nobody can read a narration back out of a trace.
//
// ⚠️ WHICH MODEL IS W1a's CALL, NOT THIS FILE'S. The Hinglish retrieval eval
// (corpora/embedding) decides it; nothing here names a default model.

// EmbedFeature is the feature key embedding calls are budgeted and traced
// under unless the caller names a narrower one.
const EmbedFeature = "embed.index"

// embedPaths is the per-provider embeddings route, joined to the same base URL
// as the provider's chat route. Written down per provider rather than derived
// from the chat path, because "OpenAI-compatible" differs on the path first.
var embedPaths = map[Provider]string{
	ProviderFireworks: "/v1/embeddings",
	ProviderDeepInfra: "/v1/openai/embeddings",
	ProviderVLLM:      "/v1/embeddings",
}

// EmbedRequest is one batch of texts to embed.
type EmbedRequest struct {
	// TenantID scopes the budget and the span, and arms the tenant-id needle
	// check on the encoded body (the same as Request.TenantID).
	TenantID uuid.UUID
	// Feature keys the budget and trace; "" is refused (use EmbedFeature).
	Feature string
	// Version pins the embedding scheme (model + dimensions + preprocessing)
	// the way PromptVersion pins a prompt: a vector made under one version is
	// not comparable with one made under another.
	Version string
	Inputs  []string
	// Dimensions requests a truncated (Matryoshka) output; 0 means the model's
	// native size. When set, every returned vector must have exactly this many
	// components or the response is refused.
	Dimensions int
	// MaxInputBytes bounds the encoded body; 0 means DefaultMaxInputBytes.
	MaxInputBytes int
}

// EmbedResponse is the served batch, in INPUT order.
type EmbedResponse struct {
	Vectors     [][]float32
	Usage       Usage
	Fingerprint Fingerprint
}

// EmbedInputsHash is the content-free key of a batch: what the span records
// as its prompt hash and what a cassette keys on. Model and dimensions are in
// it because the same texts under another model are another call.
func EmbedInputsHash(model string, dims int, inputs []string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%d", model, dims)
	for _, in := range inputs {
		h.Write([]byte{0})
		h.Write([]byte(in))
	}
	return hex.EncodeToString(h.Sum(nil))
}

type embedWireRequest struct {
	Model          string   `json:"model"`
	Input          []string `json:"input"`
	Dimensions     int      `json:"dimensions,omitempty"`
	EncodingFormat string   `json:"encoding_format"`
}

type embedWireResponse struct {
	Model string `json:"model"`
	Data  []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

// encodeEmbed is PURE, like Adapter.Encode.
func encodeEmbed(model string, req *EmbedRequest) ([]byte, error) {
	return json.Marshal(embedWireRequest{Model: model, Input: req.Inputs, Dimensions: req.Dimensions, EncodingFormat: "float"})
}

// decodeEmbed is PURE. ⛔ A SHAPE THAT DOES NOT MATCH THE REQUEST IS REFUSED,
// NOT REPAIRED: a missing index, a duplicate, a vector of the wrong length or
// a non-finite component would be stored and then silently rank every search
// wrong — there is no downstream compiler to catch a bad vector the way
// query.Compile catches a bad DSL.
func decodeEmbed(p Provider, status int, hdr http.Header, body []byte, n, dims int) (*EmbedResponse, error) {
	fp := Fingerprint{Provider: p, RequestID: requestIDFrom(hdr)}
	if status < 200 || status > 299 {
		return &EmbedResponse{Fingerprint: fp}, &UpstreamError{Provider: p, Status: status, RequestID: fp.RequestID, Snippet: snippet(body)}
	}
	var wr embedWireResponse
	if err := json.Unmarshal(body, &wr); err != nil {
		return &EmbedResponse{Fingerprint: fp}, fmt.Errorf("%w: %s: undecodable embeddings envelope: %v", ErrNoStructuredOutput, p, err)
	}
	fp.ServedModel = wr.Model
	out := &EmbedResponse{Fingerprint: fp, Usage: Usage{PromptTokens: wr.Usage.PromptTokens}}
	if len(wr.Data) != n {
		return out, fmt.Errorf("%w: %s returned %d embeddings for %d inputs", ErrNoStructuredOutput, p, len(wr.Data), n)
	}
	sort.SliceStable(wr.Data, func(i, j int) bool { return wr.Data[i].Index < wr.Data[j].Index })
	vecs := make([][]float32, n)
	for i, d := range wr.Data {
		if d.Index != i {
			return out, fmt.Errorf("%w: %s embeddings indices are not 0..%d (saw %d at %d)", ErrNoStructuredOutput, p, n-1, d.Index, i)
		}
		if len(d.Embedding) == 0 || (dims > 0 && len(d.Embedding) != dims) {
			return out, fmt.Errorf("%w: %s embedding %d has %d components, want %d", ErrNoStructuredOutput, p, i, len(d.Embedding), dims)
		}
		for _, v := range d.Embedding {
			if v != v || v > 3.4e38 || v < -3.4e38 { // NaN or ±Inf
				return out, fmt.Errorf("%w: %s embedding %d has a non-finite component", ErrNoStructuredOutput, p, i)
			}
		}
		vecs[i] = d.Embedding
	}
	// Assigned only when every vector passed: a refusal never carries a
	// partial batch a caller could store by mistake.
	out.Vectors = vecs
	return out, nil
}

// Embed makes exactly one provider call for the batch.
func (c *Client) Embed(ctx context.Context, req *EmbedRequest) (*EmbedResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("llm: nil embed request")
	}
	if strings.TrimSpace(req.Feature) == "" {
		return nil, fmt.Errorf("llm: embed request has no Feature — it would be untraceable and unbillable")
	}
	if strings.TrimSpace(req.Version) == "" {
		return nil, fmt.Errorf("llm: embed request for feature %q has no Version — vectors from two schemes would be compared", req.Feature)
	}
	if len(req.Inputs) == 0 {
		return nil, fmt.Errorf("llm: embed request for feature %q has no inputs", req.Feature)
	}
	for i, in := range req.Inputs {
		if strings.TrimSpace(in) == "" {
			return nil, fmt.Errorf("llm: embed input %d is empty — an empty text has no meaning to embed", i)
		}
	}
	path, ok := embedPaths[c.adapter.Provider()]
	if !ok {
		return nil, fmt.Errorf("%w: provider %s has no embeddings surface", ErrNotConfigured, c.adapter.Provider())
	}
	body, err := encodeEmbed(c.model, req)
	if err != nil {
		return nil, err
	}
	limit := req.MaxInputBytes
	if limit <= 0 {
		limit = DefaultMaxInputBytes
	}
	if len(body) > limit {
		return nil, fmt.Errorf("%w: %d encoded bytes exceeds the %d-byte limit for feature %q", ErrInputTooLarge, len(body), limit, req.Feature)
	}
	if req.TenantID != uuid.Nil {
		hyphenated := req.TenantID.String()
		bare := strings.ReplaceAll(hyphenated, "-", "")
		if bytes.Contains(body, []byte(hyphenated)) || bytes.Contains(body, []byte(bare)) {
			return nil, fmt.Errorf("%w (feature %q): remove it from the texts being embedded", ErrTenantIDInPrompt, req.Feature)
		}
	}

	start := SpanStart{
		TenantID: req.TenantID, Feature: req.Feature, PromptVersion: req.Version,
		PromptHash: EmbedInputsHash(c.model, req.Dimensions, req.Inputs),
		Provider:   c.adapter.Provider(), ModelID: c.model,
	}
	reservation, err := c.budget.Reserve(ctx, req.TenantID, req.Feature, len(body)/4+1)
	if err != nil {
		_, span := c.recorder.StartLLMCall(ctx, start)
		span.End(nil, err)
		return nil, err
	}
	settled := false
	defer func() {
		if settled {
			return
		}
		if rerr := reservation.Release(context.WithoutCancel(ctx)); rerr != nil {
			log.Printf("llm: budget release failed for feature %q: %v", req.Feature, rerr)
		}
	}()

	spanCtx, span := c.recorder.StartLLMCall(ctx, start)
	var (
		out     *EmbedResponse
		callErr error
	)
	defer func() {
		var r *Response
		if out != nil {
			r = &Response{Usage: out.Usage, Fingerprint: out.Fingerprint}
		}
		span.End(r, callErr)
	}()

	out, callErr = c.doEmbed(spanCtx, path, body, len(req.Inputs), req.Dimensions)
	if out != nil {
		out.Fingerprint.RequestedModel = c.model
	}
	if out != nil && out.Usage.Total() > 0 {
		c.settle(ctx, reservation, out.Usage, req.Feature)
		settled = true
	}
	if callErr != nil {
		return out, callErr
	}
	return out, nil
}

func (c *Client) doEmbed(ctx context.Context, path string, body []byte, n, dims int) (*EmbedResponse, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUpstreamUnavailable, err)
	}
	c.adapter.SetAuth(httpReq.Header, c.apiKey)
	httpResp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUpstreamUnavailable, err)
	}
	defer httpResp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(httpResp.Body, MaxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("%w: reading response body: %v", ErrUpstreamUnavailable, err)
	}
	return decodeEmbed(c.adapter.Provider(), httpResp.StatusCode, httpResp.Header, raw, n, dims)
}
