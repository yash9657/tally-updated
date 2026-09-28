package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// embed_test.go: Embed holds Complete's walls, in Complete's order, and
// refuses a response whose shape does not match the request.

func embedOK(n, dims int) string {
	type d struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	}
	var data []d
	// Reverse order on the wire: decode must sort by index.
	for i := n - 1; i >= 0; i-- {
		v := make([]float32, dims)
		v[i%dims] = 1
		data = append(data, d{Index: i, Embedding: v})
	}
	b, _ := json.Marshal(map[string]any{"model": "served-embed", "data": data, "usage": map[string]int{"prompt_tokens": 7 * n, "total_tokens": 7 * n}})
	return string(b)
}

func embedReq(inputs ...string) *EmbedRequest {
	return &EmbedRequest{TenantID: uuid.New(), Feature: EmbedFeature, Version: "emb-v1", Inputs: inputs, Dimensions: 4}
}

func TestEmbedWireShapePathAndOrder(t *testing.T) {
	for _, tc := range []struct {
		p    Provider
		path string
	}{{ProviderFireworks, "/v1/embeddings"}, {ProviderDeepInfra, "/v1/openai/embeddings"}, {ProviderVLLM, "/v1/embeddings"}} {
		var cap capture
		cap.respFunc = alwaysOK(embedOK(3, 4))
		budget := &countingBudget{}
		spans := &spanLog{}
		c := fakeAPI(t, &cap, Config{Provider: tc.p, Model: "m-embed", Budget: budget, Recorder: spans})
		res, err := c.Embed(context.Background(), embedReq("bank me aaya", "NEFT Kaveri", "rent may"))
		if err != nil {
			t.Fatalf("%s: %v", tc.p, err)
		}
		if cap.path != tc.path {
			t.Errorf("%s: path %q, want %q", tc.p, cap.path, tc.path)
		}
		var sent embedWireRequest
		if err := json.Unmarshal(cap.LastBody(), &sent); err != nil {
			t.Fatal(err)
		}
		if sent.Model != "m-embed" || len(sent.Input) != 3 || sent.Dimensions != 4 || sent.EncodingFormat != "float" {
			t.Errorf("%s: wire request %+v", tc.p, sent)
		}
		for i, v := range res.Vectors {
			if len(v) != 4 || v[i%4] != 1 {
				t.Errorf("%s: vector %d is %v — not in INPUT order", tc.p, i, v)
			}
		}
		if r, s, rel := budget.counts(); r != 1 || s != 1 || rel != 0 {
			t.Errorf("%s: budget reserve/settle/release = %d/%d/%d, want 1/1/0", tc.p, r, s, rel)
		}
		if len(spans.starts) != 1 || spans.starts[0].PromptHash != EmbedInputsHash("m-embed", 4, []string{"bank me aaya", "NEFT Kaveri", "rent may"}) {
			t.Errorf("%s: span %+v does not carry the inputs hash", tc.p, spans.starts)
		}
		for _, s := range spans.starts {
			if strings.Contains(fmt.Sprintf("%+v", s), "Kaveri") {
				t.Errorf("%s: span start carries input text: %+v", tc.p, s)
			}
		}
		if res.Fingerprint.ServedModel != "served-embed" || res.Fingerprint.RequestedModel != "m-embed" {
			t.Errorf("%s: fingerprint %+v", tc.p, res.Fingerprint)
		}
	}
}

// TestEmbedRefusesBeforeTheNetwork: every free refusal costs no socket and no
// budget.
func TestEmbedRefusesBeforeTheNetwork(t *testing.T) {
	tenant := uuid.New()
	cases := []struct {
		name string
		p    Provider
		req  *EmbedRequest
		want error
	}{
		{"anthropic has no embeddings", ProviderAnthropic, embedReq("x"), ErrNotConfigured},
		{"sarvam is not OpenAI-shaped here", ProviderSarvam, embedReq("x"), ErrNotConfigured},
		{"tenant id in a text", ProviderFireworks, &EmbedRequest{TenantID: tenant, Feature: EmbedFeature, Version: "v", Inputs: []string{"ledger " + tenant.String()}}, ErrTenantIDInPrompt},
		{"bare tenant id", ProviderFireworks, &EmbedRequest{TenantID: tenant, Feature: EmbedFeature, Version: "v", Inputs: []string{strings.ReplaceAll(tenant.String(), "-", "")}}, ErrTenantIDInPrompt},
		{"oversized", ProviderFireworks, &EmbedRequest{Feature: EmbedFeature, Version: "v", Inputs: []string{strings.Repeat("a", 2000)}, MaxInputBytes: 1024}, ErrInputTooLarge},
		{"no feature", ProviderFireworks, &EmbedRequest{Version: "v", Inputs: []string{"a"}}, nil},
		{"no version", ProviderFireworks, &EmbedRequest{Feature: EmbedFeature, Inputs: []string{"a"}}, nil},
		{"empty input", ProviderFireworks, &EmbedRequest{Feature: EmbedFeature, Version: "v", Inputs: []string{"a", "  "}}, nil},
		{"no inputs", ProviderFireworks, &EmbedRequest{Feature: EmbedFeature, Version: "v"}, nil},
	}
	for _, tc := range cases {
		var cap capture
		cap.respFunc = alwaysOK(embedOK(1, 4))
		budget := &countingBudget{}
		c := fakeAPI(t, &cap, Config{Provider: tc.p, Model: "m", Budget: budget})
		_, err := c.Embed(context.Background(), tc.req)
		if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
		if cap.Calls() != 0 {
			t.Errorf("%s: %d network calls on a refusal", tc.name, cap.Calls())
		}
		if r, _, _ := budget.counts(); r != 0 {
			t.Errorf("%s: a refused request reserved budget", tc.name)
		}
	}
}

// TestEmbedRefusesAMismatchedResponse: a bad shape is refused, never stored.
func TestEmbedRefusesAMismatchedResponse(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		want       error
	}{
		{"fewer vectors than inputs", embedOK(1, 4), 200, ErrNoStructuredOutput},
		{"wrong dimensions", embedOK(2, 3), 200, ErrNoStructuredOutput},
		{"duplicate index", `{"data":[{"index":0,"embedding":[1,0,0,0]},{"index":0,"embedding":[0,1,0,0]}],"usage":{"prompt_tokens":3}}`, 200, ErrNoStructuredOutput},
		{"empty embedding", `{"data":[{"index":0,"embedding":[]},{"index":1,"embedding":[0,1,0,0]}],"usage":{"prompt_tokens":3}}`, 200, ErrNoStructuredOutput},
		{"not json", `<html>`, 200, ErrNoStructuredOutput},
		{"a 429", `{"error":"slow down"}`, 429, ErrUpstreamUnavailable},
	} {
		var cap capture
		cap.respFunc = func(int) (int, string) { return tc.status, tc.body }
		budget := &countingBudget{}
		c := fakeAPI(t, &cap, Config{Provider: ProviderFireworks, Model: "m", Budget: budget})
		res, err := c.Embed(context.Background(), embedReq("a", "b"))
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
		if res != nil && res.Vectors != nil {
			t.Errorf("%s: vectors returned alongside the refusal", tc.name)
		}
		r, s, rel := budget.counts()
		if r != 1 || s+rel != 1 {
			t.Errorf("%s: reserve/settle/release = %d/%d/%d — exactly one of settle or release", tc.name, r, s, rel)
		}
	}
	_ = http.StatusOK
}
