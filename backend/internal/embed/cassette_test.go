package embed

// cassette_test.go — the per-feature replay half of Tier 0 for embed.index:
// the production Embedder (LLMEmbedder → llm.Client.Embed → the Fireworks
// wire shape) replays a recorded embeddings response by a key RE-DERIVED from
// the encoded request body (model, dimensions, inputs). A change to what is
// sent — a preprocessing step, the dimensions, the model — moves the key and
// fails here with the regeneration command.
//
// ⛔ THE VECTORS ARE THE TEST DOUBLE's (trigramVector), recorded against a
// fake (SeedNote). No provider key exists; which model embeds is W1a's
// retrieval eval's decision. What this proves is the wire path and the
// replay key, not retrieval quality.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/llm"
	"github.com/khata/cored/internal/trajectory"
)

const (
	embCassetteRoot = "testdata"
	embCaseID       = "hinglish-narrations"
	embRegenerate   = "TRAJECTORY_RECORD=1 go test ./internal/embed/ -run TestLLMEmbedderReplaysItsCassette"
	embModel        = "accounts/fireworks/models/test-embed"
)

var embInputs = []string{"UPI/BIJLI BILL MSEDCL/ELECTRICITY", "NEFT-HDFC-KAVERI TRADERS-INV 0412"}

type embProvider struct {
	cassette *trajectory.Cassette
	rec      *trajectory.Recording
	calls    int
	path     string
}

func (p *embProvider) RoundTrip(r *http.Request) (*http.Response, error) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	p.calls++
	p.path = r.URL.Path
	var wire struct {
		Model      string   `json:"model"`
		Input      []string `json:"input"`
		Dimensions int      `json:"dimensions"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, fmt.Errorf("fake embeddings provider: %w", err)
	}
	key := llm.EmbedInputsHash(wire.Model, wire.Dimensions, wire.Input)
	var body []byte
	if p.cassette != nil {
		e, err := p.cassette.Lookup(key)
		if err != nil {
			return nil, fmt.Errorf("%w\n\nThe request changed, so its key changed. Re-record:\n  %s", err, embRegenerate)
		}
		body = e.ResponseBody
	} else {
		type d struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		}
		var data []d
		for i, in := range wire.Input {
			data = append(data, d{Index: i, Embedding: trigramVector(in)})
		}
		body, _ = json.Marshal(map[string]any{"model": wire.Model, "data": data, "usage": map[string]int{"prompt_tokens": 24, "total_tokens": 24}})
		if err := p.rec.Append(trajectory.Entry{
			Kind: trajectory.EntryModelCall,
			Span: trajectory.Record{
				TraceID: fmt.Sprintf("%032x", 0xE1), SpanID: fmt.Sprintf("%016x", p.calls),
				Name: "embeddings " + llm.EmbedFeature, Kind: "chat", Status: "OK",
				Attributes: map[string]any{
					trajectory.AttrFeature:            llm.EmbedFeature,
					trajectory.AttrSemconvVersion:     trajectory.SemconvVersion,
					trajectory.AttrGenAIOperationName: "embeddings",
					trajectory.AttrPromptHash:         key,
				},
			},
			RequestBody:  json.RawMessage(`{"model":"` + wire.Model + `","dimensions":1024,"encoding_format":"float"}`),
			ResponseBody: json.RawMessage(body),
		}); err != nil {
			return nil, err
		}
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(string(body))), Request: r}, nil
}

func TestLLMEmbedderReplaysItsCassette(t *testing.T) {
	path, err := trajectory.CassettePath(embCassetteRoot, llm.EmbedFeature, embCaseID)
	if err != nil {
		t.Fatal(err)
	}
	p := &embProvider{}
	if trajectory.RecordingEnabled() {
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.Remove(path)
		rec, err := trajectory.NewRecording(path, trajectory.Header{
			CaseID: embCaseID, Note: trajectory.SeedNote("go test ./internal/embed/ -run TestLLMEmbedderReplaysItsCassette"),
			RecordedAt: "2026-09-28T00:00:00Z", SemconvVersion: trajectory.SemconvVersion,
			Provider: "fake", ModelID: embModel,
		}, trajectory.IdentityRedactor) // synthetic narrations; nothing to strip, stated
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = rec.Close() })
		p.rec = rec
	} else {
		c, err := trajectory.OpenCassette(path)
		if err != nil {
			t.Fatalf("open cassette %s: %v\n\nGenerate it:\n  %s", path, err, embRegenerate)
		}
		p.cassette = c
	}
	client, err := llm.New(llm.Config{Provider: llm.ProviderFireworks, Model: embModel, APIKey: "cassette",
		BaseURL: "http://cassette.invalid", HTTP: &http.Client{Transport: p}})
	if err != nil {
		t.Fatal(err)
	}
	e := LLMEmbedder{Client: client, Scheme: "test-embed@1024/v1"}
	vecs, err := e.Embed(context.Background(), uuid.New(), embInputs)
	if err != nil {
		t.Fatal(err)
	}
	if p.calls != 1 || p.path != "/v1/embeddings" {
		t.Errorf("provider calls %d on %q, want 1 on /v1/embeddings", p.calls, p.path)
	}
	if len(vecs) != 2 || len(vecs[0]) != Dim {
		t.Fatalf("got %d vectors of %d", len(vecs), len(vecs[0]))
	}
	// The replayed vectors are the ones recorded for THESE inputs.
	for i, in := range embInputs {
		want := trigramVector(in)
		for j := range want {
			if vecs[i][j] != want[j] {
				t.Fatalf("vector %d component %d: %v, recorded %v", i, j, vecs[i][j], want[j])
			}
		}
	}
	if p.cassette != nil && p.cassette.Replayed() != p.cassette.Len() {
		t.Errorf("replayed %d of %d", p.cassette.Replayed(), p.cassette.Len())
	}
}
