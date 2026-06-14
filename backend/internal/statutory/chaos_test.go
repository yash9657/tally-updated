package statutory

import (
	"context"
	"errors"
	"testing"
)

// runUntilDone repeatedly invokes the workflow, resuming on transient errors,
// until it succeeds or hits a permanent error. It bounds the attempts so a bug
// cannot loop forever. Returns the final result and total attempts.
func runUntilDone(t *testing.T, store StepStore, prov ProviderAdapter, cored CoredClient, snap VoucherSnapshot) (IRNResult, int) {
	t.Helper()
	var last IRNResult
	for attempt := 1; attempt <= 20; attempt++ {
		res, err := GenerateIRNWorkflow(context.Background(), store, prov, cored, "co1", "v1", snap)
		if err == nil {
			return res, attempt
		}
		if IsPermanent(err) {
			t.Fatalf("unexpected permanent error on attempt %d: %v", attempt, err)
		}
		last = res
	}
	t.Fatalf("workflow did not converge within 20 attempts (last=%+v)", last)
	return last, 0
}

func TestChaosTransientProviderFailuresExactlyOnce(t *testing.T) {
	cases := []struct {
		name string
		plan []error
	}{
		{"outage", []error{ErrProviderOutage, nil}},
		{"token-expired", []error{ErrTokenExpired, nil}},
		{"rate-limited", []error{ErrRateLimited, nil}},
		{"all-three-then-ok", []error{ErrProviderOutage, ErrTokenExpired, ErrRateLimited, nil}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMemStore()
			chaos := NewChaosProvider(FakeProvider{}, tc.plan...)
			cored := &FakeCoredClient{Snapshot: conformantSnapshot()}

			res, _ := runUntilDone(t, store, chaos, cored, conformantSnapshot())

			if res.IRN == "" {
				t.Fatal("no IRN minted")
			}
			// The inner provider must have been reached EXACTLY ONCE despite the
			// scripted transient failures consumed before it.
			if got := chaos.InnerCalls(); got != 1 {
				t.Fatalf("inner provider must be called exactly once, got %d", got)
			}
			// Freeze hook called exactly once.
			if len(cored.Registered) != 1 {
				t.Fatalf("freeze hook must be called once, got %d", len(cored.Registered))
			}
			if cored.Registered[0].Kind != "IRN" || cored.Registered[0].Ref != res.IRN {
				t.Fatalf("freeze recorded wrong ref: %+v", cored.Registered[0])
			}

			// Re-running a now-complete workflow must mint the SAME IRN and add
			// no further provider/freeze calls.
			res2, err := GenerateIRNWorkflow(context.Background(), store, chaos, cored, "co1", "v1", conformantSnapshot())
			if err != nil {
				t.Fatalf("re-run: %v", err)
			}
			if res2.IRN != res.IRN {
				t.Fatalf("IRN changed across resume: %q vs %q", res.IRN, res2.IRN)
			}
			if chaos.InnerCalls() != 1 {
				t.Fatalf("inner provider called again on resume: %d", chaos.InnerCalls())
			}
			if len(cored.Registered) != 1 {
				t.Fatalf("freeze called again on resume: %d", len(cored.Registered))
			}
		})
	}
}

func TestChaosStep4FreezeFailureResumesNoDoubleIRN(t *testing.T) {
	store := NewMemStore()
	chaos := NewChaosProvider(FakeProvider{}) // provider always succeeds
	// First two freeze attempts fail (transient), third succeeds.
	cored := &FakeCoredClient{Snapshot: conformantSnapshot(), FailRegisterTimes: 2}

	res, _ := runUntilDone(t, store, chaos, cored, conformantSnapshot())

	if res.IRN == "" {
		t.Fatal("no IRN minted")
	}
	// Provider hit exactly once even though step 4 failed and resumed twice:
	// step 3's result is stored, so resume never re-calls the provider.
	if chaos.InnerCalls() != 1 {
		t.Fatalf("provider must be called exactly once across freeze resumes, got %d", chaos.InnerCalls())
	}
	if len(cored.Registered) != 1 {
		t.Fatalf("freeze must ultimately be recorded exactly once, got %d", len(cored.Registered))
	}
	if cored.Registered[0].Ref != res.IRN {
		t.Fatalf("frozen ref mismatch")
	}
}

func TestChaosPermanentRejectAbortsNoFreeze(t *testing.T) {
	store := NewMemStore()
	chaos := NewChaosProvider(FakeProvider{}, ErrPermanentReject)
	cored := &FakeCoredClient{Snapshot: conformantSnapshot()}

	_, err := GenerateIRNWorkflow(context.Background(), store, chaos, cored, "co1", "v1", conformantSnapshot())
	if err == nil {
		t.Fatal("expected permanent reject error")
	}
	if !IsPermanent(err) {
		t.Fatalf("expected permanent error, got: %v", err)
	}
	var pe *ProviderError
	if !errors.As(err, &pe) || pe.Retryable() {
		t.Fatalf("expected non-retryable provider error, got: %v", err)
	}
	if len(cored.Registered) != 0 {
		t.Fatalf("freeze must NOT be called on permanent reject, got %d", len(cored.Registered))
	}
	// The chaos plan consumed the permanent error before reaching the inner
	// provider, so the inner provider was never hit.
	if chaos.InnerCalls() != 0 {
		t.Fatalf("inner provider should not be reached, got %d", chaos.InnerCalls())
	}
}
