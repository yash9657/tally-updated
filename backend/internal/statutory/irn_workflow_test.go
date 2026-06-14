package statutory

import (
	"context"
	"testing"
)

// countingProvider records how many times GenerateIRN was invoked.
type countingProvider struct {
	inner ProviderAdapter
	calls int
}

func (c *countingProvider) Name() string { return c.inner.Name() }
func (c *countingProvider) GenerateIRN(ctx context.Context, p EInvoicePayload) (IRNResult, error) {
	c.calls++
	return c.inner.GenerateIRN(ctx, p)
}

func TestWorkflowHappyPath(t *testing.T) {
	store := NewMemStore()
	prov := &countingProvider{inner: FakeProvider{}}
	cored := &FakeCoredClient{Snapshot: conformantSnapshot()}

	res, err := GenerateIRNWorkflow(context.Background(), store, prov, cored, "co1", "v1", conformantSnapshot())
	if err != nil {
		t.Fatalf("workflow: %v", err)
	}
	if len(res.IRN) != 64 {
		t.Fatalf("expected 64-char IRN, got %d (%q)", len(res.IRN), res.IRN)
	}
	if res.Status != "ACT" {
		t.Fatalf("expected status ACT, got %q", res.Status)
	}
	if prov.calls != 1 {
		t.Fatalf("provider should be called once, got %d", prov.calls)
	}
	if len(cored.Registered) != 1 {
		t.Fatalf("expected one RegisterExternalReference, got %d", len(cored.Registered))
	}
	r := cored.Registered[0]
	if r.Kind != "IRN" || r.Ref != res.IRN || r.VoucherID != "v1" {
		t.Fatalf("unexpected freeze record: %+v", r)
	}
}

func TestWorkflowValidationFailureNoProviderNoFreeze(t *testing.T) {
	store := NewMemStore()
	prov := &countingProvider{inner: FakeProvider{}}

	bad := conformantSnapshot()
	bad.BuyerGstin = "" // missing buyer GSTIN -> validation fails at step 2
	cored := &FakeCoredClient{Snapshot: bad}

	_, err := GenerateIRNWorkflow(context.Background(), store, prov, cored, "co1", "v1", bad)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !IsPermanent(err) {
		t.Fatalf("validation failure should be permanent, got: %v", err)
	}
	if prov.calls != 0 {
		t.Fatalf("provider must NOT be called when validation fails, got %d", prov.calls)
	}
	if len(cored.Registered) != 0 {
		t.Fatalf("freeze must NOT be called when validation fails, got %d", len(cored.Registered))
	}
}
