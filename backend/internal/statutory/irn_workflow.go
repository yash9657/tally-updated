package statutory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// permanentError marks a workflow abort that is NOT resumable (e.g. schema
// validation failure or a permanent IRP reject). Re-running the workflow will
// deterministically fail again at the same step.
type permanentError struct{ msg string }

func (e *permanentError) Error() string { return e.msg }

// ValidationError carries the conformance violations from step 2.
type ValidationError struct {
	Violations []string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("statutory: e-invoice validation failed: %v", e.Violations)
}

// GenerateIRNWorkflow runs the durable IRN workflow for one voucher:
//
//	1 build    -> BuildEInvoice(snapshot)                 (pure)
//	2 validate -> ValidateEInvoice; abort (permanent) if violations
//	3 generate -> provider.GenerateIRN(payload)           (the ONLY external call)
//	4 freeze   -> cored.RegisterExternalReference(IRN)    (the freeze hook)
//
// workflowID = "irn:"+companyID+":"+voucherID, so it is stable and idempotent. A
// transient provider/cored fault leaves the workflow resumable: re-running
// resumes at the failed step. Because step 3's result is stored, a failure in
// step 4 NEVER re-calls the provider — no double-IRN. The provider's GenerateIRN
// is reached exactly once across all resumes, and the freeze hook is ultimately
// invoked exactly once.
func GenerateIRNWorkflow(
	ctx context.Context,
	store StepStore,
	provider ProviderAdapter,
	cored CoredClient,
	companyID, voucherID string,
	snap VoucherSnapshot,
) (IRNResult, error) {
	workflowID := "irn:" + companyID + ":" + voucherID

	steps := []Step{
		{
			Name: "build",
			Run: func(_ context.Context, _ map[string]json.RawMessage) (json.RawMessage, error) {
				payload := BuildEInvoice(snap)
				return json.Marshal(payload)
			},
		},
		{
			Name: "validate",
			Run: func(_ context.Context, prev map[string]json.RawMessage) (json.RawMessage, error) {
				var payload EInvoicePayload
				if err := json.Unmarshal(prev["build"], &payload); err != nil {
					return nil, err
				}
				if violations := ValidateEInvoice(payload); len(violations) > 0 {
					return nil, &ValidationError{Violations: violations}
				}
				return json.Marshal(map[string]bool{"ok": true})
			},
		},
		{
			Name: "generate",
			Run: func(ctx context.Context, prev map[string]json.RawMessage) (json.RawMessage, error) {
				var payload EInvoicePayload
				if err := json.Unmarshal(prev["build"], &payload); err != nil {
					return nil, err
				}
				res, err := provider.GenerateIRN(ctx, payload)
				if err != nil {
					return nil, err
				}
				return json.Marshal(res)
			},
		},
		{
			Name: "freeze",
			Run: func(ctx context.Context, prev map[string]json.RawMessage) (json.RawMessage, error) {
				var res IRNResult
				if err := json.Unmarshal(prev["generate"], &res); err != nil {
					return nil, err
				}
				pinned, err := cored.RegisterExternalReference(ctx, companyID, voucherID, "IRN", res.IRN, "statutoryd")
				if err != nil {
					return nil, err
				}
				return json.Marshal(pinned)
			},
		},
	}

	state, err := Run(ctx, store, workflowID, steps)
	if err != nil {
		return IRNResult{}, err
	}

	var res IRNResult
	if err := json.Unmarshal(state["generate"], &res); err != nil {
		return IRNResult{}, err
	}
	return res, nil
}

// IsPermanent reports whether err is a non-resumable workflow abort: a schema
// validation failure or a permanent provider reject.
func IsPermanent(err error) bool {
	var ve *ValidationError
	if errors.As(err, &ve) {
		return true
	}
	var pe *ProviderError
	if errors.As(err, &pe) {
		return !pe.Retryable()
	}
	var perr *permanentError
	return errors.As(err, &perr)
}
