package statutory

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"encoding/hex"
	"fmt"

	"github.com/zeebo/blake3"
)

// FakeProvider is a deterministic ProviderAdapter for tests and the local
// statutoryd binary. The IRN is the 64-char hex BLAKE3-256 digest of the
// canonical payload JSON, so the same invoice always mints the same IRN
// (exactly-once and resume tests rely on this determinism). AckNo is derived
// from the first 8 bytes of the same digest, Status is "ACT".
type FakeProvider struct{}

// Name identifies the adapter in logs and the workflow record.
func (FakeProvider) Name() string { return "fake-nic" }

// GenerateIRN returns a deterministic IRNResult for the payload.
func (FakeProvider) GenerateIRN(_ context.Context, p EInvoicePayload) (IRNResult, error) {
	canon, err := json.Marshal(p)
	if err != nil {
		return IRNResult{}, fmt.Errorf("statutory: canonicalize payload: %w", err)
	}
	sum := blake3.Sum256(canon)
	irn := hex.EncodeToString(sum[:]) // 64 hex chars
	ackCounter := binary.BigEndian.Uint64(sum[:8]) % 1_000_000_000
	return IRNResult{
		IRN:          irn,
		AckNo:        fmt.Sprintf("%d", ackCounter),
		AckDt:        p.DocDtls.Dt,
		SignedQRCode: "QR-" + irn[:16],
		Status:       "ACT",
	}, nil
}

// ChaosProvider wraps a ProviderAdapter with a scripted failure plan. Each call
// to GenerateIRN consumes the next outcome from the plan: a non-nil error short
// circuits BEFORE the inner provider is reached, while a nil outcome delegates
// to the inner provider. This models transient faults (outage/token/rate-limit)
// that the durable workflow retries on resume.
//
// CRUCIAL: innerCalls counts how many times the inner provider was actually
// invoked. Tests assert innerCalls == 1 across all retries/resumes (no
// double-IRN). The runner's step memoization is what enforces that, since a
// failure in a LATER step never re-runs the (already-stored) generate step.
type ChaosProvider struct {
	inner      ProviderAdapter
	plan       []error
	idx        int
	innerCalls int
}

// NewChaosProvider builds a chaos wrapper. The plan is consumed in order; a nil
// entry means "delegate to inner this time". Once the plan is exhausted all
// further calls delegate to the inner provider.
func NewChaosProvider(inner ProviderAdapter, plan ...error) *ChaosProvider {
	return &ChaosProvider{inner: inner, plan: plan}
}

// Name reports the inner provider's name (the chaos layer is transparent).
func (c *ChaosProvider) Name() string { return c.inner.Name() }

// GenerateIRN consumes the next scripted outcome.
func (c *ChaosProvider) GenerateIRN(ctx context.Context, p EInvoicePayload) (IRNResult, error) {
	if c.idx < len(c.plan) {
		out := c.plan[c.idx]
		c.idx++
		if out != nil {
			return IRNResult{}, out
		}
	}
	c.innerCalls++
	return c.inner.GenerateIRN(ctx, p)
}

// InnerCalls reports how many times the inner provider was actually reached.
func (c *ChaosProvider) InnerCalls() int { return c.innerCalls }
