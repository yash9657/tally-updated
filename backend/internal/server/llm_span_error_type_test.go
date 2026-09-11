package server

// llm_span_error_type_test.go pins ONE attribute: error.type, on the span the
// composition root opens for an internal/llm call.
//
// ⛔ WHY THAT DESERVES A FILE. trajectory.Span.SetError deliberately records
// fmt.Sprintf("%T", err) and never the message, because an error string in this
// codebase routinely interpolates a party name (span.go says so, with four
// measured examples). The cost of that correct decision is that internal/llm,
// which wraps every sentinel with fmt.Errorf("%w: …"), reduced its whole failure
// taxonomy to the single Go type *fmt.wrapError — so the trace store could not
// tell an exhausted tenant budget from a dead provider from the tenant-id gate
// firing, and those three have three different people to wake up.
//
// The fix is a walk over the sentinels with errors.Is, in server.go's
// trajectorySpan.End; this file is what makes it load-bearing. Its controls are
// the important half: an UNCLASSIFIED error must still report %T (so the walk
// cannot quietly become a catch-all), and no wrapped MESSAGE may reach an
// attribute (so the classification does not become a leak).

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/llm"
	"github.com/khata/cored/internal/trajectory"
)

func TestTrajectorySpanRecordsTheLLMSentinelNotTheWrapperType(t *testing.T) {
	// The snippet a provider's error body might carry, and which the wraps below
	// interpolate. It must not appear on any attribute.
	const providerText = "Acme Traders outstanding balance question"

	cases := []struct {
		name string
		err  error
		want string
	}{
		{
			// The exact wrap client.go's do() applies.
			name: "wrapped upstream",
			err:  fmt.Errorf("%w: %v", llm.ErrUpstreamUnavailable, errors.New("dial tcp: connection refused")),
			want: "llm.ErrUpstreamUnavailable",
		},
		{
			// UpstreamError is a STRUCT that unwraps to the sentinel. Without the
			// errors.Is walk this reported "*llm.UpstreamError", which is at
			// least legible — the point of including it is that the taxonomy
			// must not split into two spellings of the same failure.
			name: "UpstreamError struct",
			err:  &llm.UpstreamError{Provider: llm.ProviderFireworks, Status: 503, RequestID: "req_1", Snippet: providerText},
			want: "llm.ErrUpstreamUnavailable",
		},
		{
			name: "wrapped no-structured-output",
			err:  fmt.Errorf("%w: after 2 repair attempt(s): %v", llm.ErrNoStructuredOutput, errors.New(`unknown field "filter"`)),
			want: "llm.ErrNoStructuredOutput",
		},
		{
			// ⛔ THE ONE THIS FILE EXISTS FOR. A tenant at its cap looks, from
			// every provider dashboard, exactly like a healthy deployment.
			name: "bare budget sentinel",
			err:  llm.ErrBudgetExhausted,
			want: "llm.ErrBudgetExhausted",
		},
		{
			name: "wrapped budget-not-configured",
			err:  fmt.Errorf("%w: PGBudget has no pool", llm.ErrBudgetNotConfigured),
			want: "llm.ErrBudgetNotConfigured",
		},
		{
			name: "wrapped scope mismatch",
			err:  fmt.Errorf("%w: context=%s argument=%s", llm.ErrBudgetScopeMismatch, uuid.New(), uuid.New()),
			want: "llm.ErrBudgetScopeMismatch",
		},
		{
			name: "wrapped not-configured",
			err:  fmt.Errorf("%w: provider %s", llm.ErrNotConfigured, llm.ProviderAnthropic),
			want: "llm.ErrNotConfigured",
		},
		{
			name: "wrapped input-too-large",
			err:  fmt.Errorf("%w: %d encoded bytes exceeds the %d-byte limit", llm.ErrInputTooLarge, 90000, 65536),
			want: "llm.ErrInputTooLarge",
		},
		{
			// A SECURITY control, so it must be legible in the trace store and
			// not only in a log line somebody has to go looking for.
			name: "wrapped tenant-id gate",
			err:  fmt.Errorf("%w (feature %q): remove it from the prompt", llm.ErrTenantIDInPrompt, "nlq.translate"),
			want: "llm.ErrTenantIDInPrompt",
		},
		{
			// ⛔ CONTROL ONE. An error from outside the taxonomy keeps %T. If the
			// walk ever became a catch-all — every failure reported as one
			// sentinel — this arm is what goes red.
			name: "unclassified plain error keeps its Go type",
			err:  errors.New("some other failure"),
			want: "*errors.errorString",
		},
		{
			// ⛔ CONTROL TWO, AND IT IS THE MEASUREMENT OF THE ORIGINAL DEFECT.
			// A wrapped error that names no sentinel reports *fmt.wrapError —
			// which is precisely what EVERY case above reported before the walk
			// existed. So this arm both proves the walk is not a no-op and
			// records what "not classified" looks like.
			name: "unclassified wrapped error still reports the wrapper type",
			err:  fmt.Errorf("decoding %s: %w", "chat response", errors.New("unexpected EOF")),
			want: "*fmt.wrapError",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cap := &capturingRecorder{}
			trajectory.SetRecorder(cap)
			// SetRecorder is process-global; the nop recorder must go back or
			// every later test in this package allocates spans.
			defer trajectory.SetRecorder(trajectory.Nop())

			_, span := trajectoryRecorder{}.StartLLMCall(t.Context(), llm.SpanStart{
				TenantID:      uuid.New(),
				Feature:       "nlq.translate",
				PromptVersion: "v1",
				PromptHash:    "0f1e2d3c",
				Provider:      llm.ProviderFireworks,
				ModelID:       "glm-5p3",
			})
			span.End(nil, tc.err)

			if len(cap.records) != 1 {
				t.Fatalf("recorded %d spans, want 1", len(cap.records))
			}
			rec := cap.records[0]
			got, ok := rec.Attributes[trajectory.AttrErrorType]
			if !ok {
				t.Fatalf("no %s attribute at all — an errored call with no classification is a "+
					"trace row that cannot answer why it failed", trajectory.AttrErrorType)
			}
			if got != tc.want {
				t.Errorf("%s = %v, want %q. The on-call response to an exhausted budget, a dead "+
					"provider and a refused prompt are three different things; one shared Go type "+
					"tells an operator none of them apart", trajectory.AttrErrorType, got, tc.want)
			}
			if rec.Status != "ERROR" {
				t.Errorf("status = %q, want ERROR", rec.Status)
			}

			// ⛔ CONTROL THREE: classifying must not become leaking. The wraps
			// above carry a provider snippet and a feature name; the sentinel
			// NAME is safe because each sentinel is a fixed string no caller
			// interpolates into, and nothing else from the error may appear.
			for k, v := range rec.Attributes {
				s, isStr := v.(string)
				if !isStr {
					continue
				}
				if strings.Contains(s, providerText) || strings.Contains(s, "Acme") {
					t.Errorf("attribute %s carries provider CONTENT: %q", k, s)
				}
			}
		})
	}
}
