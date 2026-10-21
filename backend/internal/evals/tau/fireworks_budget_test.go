package tau_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// spendTransport wraps every provider call, including the NLQ translator and
// repair attempts. Reserve conservatively BEFORE sending, never refund failed
// or timed-out requests (they may have been billed). The caller supplies the
// current highest per-token price for the chosen model; this is not a billing API.
type spendTransport struct {
	mu                  sync.Mutex
	next                http.RoundTripper
	cap, rate, reserved decimal.Decimal
	calls               int
	exhausted           bool
	records             []map[string]any
}

func (s *spendTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.URL.Scheme != "https" || req.URL.Host != "api.fireworks.ai" || req.URL.Path != "/inference/v1/chat/completions" {
		return nil, fmt.Errorf("evaluation refused unexpected network destination")
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	req.Body.Close()
	var args struct {
		MaxTokens int64           `json:"max_tokens"`
		Messages  json.RawMessage `json:"messages"`
	}
	if err = json.Unmarshal(body, &args); err != nil || args.MaxTokens <= 0 {
		return nil, fmt.Errorf("evaluation needs bounded completion tokens")
	}
	// UTF-8 bytes bound byte-level tokens; 4096 adds allowance for chat framing.
	tokens := int64(len(body)) + args.MaxTokens + 4096
	reserve := decimal.NewFromInt(tokens).Mul(s.rate).Div(decimal.NewFromInt(1000000))
	if s.calls >= 150 || s.reserved.Add(reserve).GreaterThan(s.cap) {
		s.exhausted = true
		return nil, fmt.Errorf("evaluation spending limit reached before provider call")
	}
	s.reserved = s.reserved.Add(reserve)
	s.calls++
	req.Body = io.NopCloser(bytes.NewReader(body))
	start := time.Now()
	resp, err := s.next.RoundTrip(req)
	record := map[string]any{"messages": args.Messages, "call": s.calls, "latency_ms": time.Since(start).Milliseconds(), "reserved_usd": reserve.String()}
	defer func() { s.records = append(s.records, record) }()
	if err != nil {
		record["transport_error"] = true
		return nil, err
	}
	record["status"] = resp.StatusCode
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	if readErr != nil {
		return nil, readErr
	}
	var metadata struct {
		Model   string          `json:"model"`
		ID      string          `json:"id"`
		Usage   json.RawMessage `json:"usage"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				ToolCalls json.RawMessage `json:"tool_calls"`
				Content   string          `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &metadata) == nil {
		record["model"] = metadata.Model
		record["response_id"] = metadata.ID
		record["usage"] = metadata.Usage
		record["choices"] = metadata.Choices
		record["error"] = metadata.Error
	}
	return resp, nil
}

type evaluationRoundTrip func(*http.Request) (*http.Response, error)

func (f evaluationRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestLiveEvaluationSpendingControls(t *testing.T) {
	calls := 0
	next := evaluationRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"model":"fixture","usage":{"prompt_tokens":20,"completion_tokens":10}}`)), Header: make(http.Header)}, nil
	})
	s := &spendTransport{next: next, cap: decimal.RequireFromString("0.01"), rate: decimal.NewFromInt(1)}
	request := func(host string) *http.Request {
		r, _ := http.NewRequestWithContext(context.Background(), "POST", host+"/inference/v1/chat/completions", strings.NewReader(`{"max_tokens":1024}`))
		return r
	}
	resp, err := s.RoundTrip(request("https://api.fireworks.ai"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if _, err = s.RoundTrip(request("https://api.fireworks.ai")); err == nil {
		t.Fatal("spending cap did not block second request")
	}
	if _, err = s.RoundTrip(request("https://example.org")); err == nil {
		t.Fatal("unexpected destination allowed")
	}
	if calls != 1 || len(s.records) != 1 {
		t.Fatalf("provider calls=%d records=%d", calls, len(s.records))
	}
	// A provider-side failure consumes the conservative reservation too.
	failed := &spendTransport{next: evaluationRoundTrip(func(*http.Request) (*http.Response, error) { return nil, fmt.Errorf("timeout") }), cap: s.cap, rate: s.rate}
	if _, err = failed.RoundTrip(request("https://api.fireworks.ai")); err == nil {
		t.Fatal("failure hidden")
	}
	if failed.reserved.IsZero() {
		t.Fatal("failed call reservation was refunded")
	}
}
