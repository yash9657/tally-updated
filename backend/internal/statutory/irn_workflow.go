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

// StepGenerate is the name of the workflow's ONE effectful step — the external
// IRP call. It is exported because it is the boundary the whole durability design
// turns on: everything before it is a pure function of the snapshot and is
// re-derived on every attempt; everything from it onward is memoized because
// re-running it would mint a SECOND IRN against a real government API.
//
// Named rather than inlined so a reader (and a test) can point at the boundary.
const StepGenerate = "generate"

// StepFreeze is the name of the step that pins the IRN onto cored's voucher.
// Effectful, and therefore memoized: a freeze that succeeded must never be
// replayed, and a freeze that failed must not re-call the provider.
const StepFreeze = "freeze"

// GenerateIRNWorkflow runs the durable IRN workflow for one voucher:
//
//	  build    -> BuildEInvoice(snapshot)                 (pure, NOT memoized)
//	  validate -> ValidateEInvoice; abort (permanent) if violations
//	                                                      (pure, NOT memoized)
//	1 generate -> provider.GenerateIRN(payload)           (the ONLY external call)
//	2 freeze   -> cored.RegisterExternalReference(IRN)    (the freeze hook)
//
// workflowID = "irn:"+companyID+":"+voucherID, so it is stable and idempotent. A
// transient provider/cored fault leaves the workflow resumable: re-running
// resumes at the failed step. Because `generate`'s result is stored, a failure in
// `freeze` NEVER re-calls the provider — no double-IRN. The provider's GenerateIRN
// is reached exactly once across all resumes, and the freeze hook is ultimately
// invoked exactly once.
//
// # ⛔⛔ ONLY THE EFFECTFUL STEPS ARE DURABLE, AND THAT IS A BUG FIX
//
// `build` and `validate` used to be `Step`s like the other two, so the runner
// memoized them identically. Because `build` runs FIRST and is recorded BEFORE
// `validate` runs, a first attempt with a bad snapshot wrote a bad payload into
// `statutory_step` and then failed validation — and every later attempt, INCLUDING
// ONE WITH CORRECT DATA, resumed from that memo and failed the same way. Forever.
// There was no reset path anywhere: no endpoint, no flag. The only cure found was
// `DELETE FROM statutory_step WHERE workflow_id = ...` by hand.
//
// The operator-facing consequence was the part that mattered: **a single mistyped
// first attempt made an invoice permanently un-invoiceable, with an error message
// that blamed the item list.** Proved by inspection of a real poisoned row, whose
// payload held `"ItemList": []` and which no retry could displace.
//
// THE DISTINCTION THE OLD STEP LIST WAS MISSING: "memoize because re-running is
// DANGEROUS" and "memoize because it is EXPENSIVE" are not the same reason, and
// only the first earns a durable record. `generate` wraps an exactly-once external
// call, so it earns it. `build` and `validate` are pure, cheap, deterministic
// functions of the snapshot — re-running them costs nothing and re-running them is
// the ONLY way a corrected attempt can succeed.
//
// # Why the pure prefix is SKIPPED once `generate` has completed
//
// It is not merely an optimisation. A settled workflow — one that has already
// minted an IRN — must return that IRN and must never be re-validated: the IRN is
// a fact about what was SUBMITTED, and a voucher altered into an invalid state
// afterwards cannot un-issue it. Re-validating would answer 422 to a voucher that
// demonstrably has an IRN, which is both wrong and unactionable.
//
// # Residue note
//
// Workflows poisoned before this fix still hold `build`/`validate` rows in
// `statutory_step`. They are inert: no step by either name is ever looked up
// again, so those rows are ignored and the corrected attempt proceeds. They need
// no migration and no cleanup.
func GenerateIRNWorkflow(
	ctx context.Context,
	store StepStore,
	provider ProviderAdapter,
	cored CoredClient,
	companyID, voucherID string,
	snap VoucherSnapshot,
) (IRNResult, error) {
	workflowID := "irn:" + companyID + ":" + voucherID

	// SETTLED-FIRST. Ask the store whether the external call has already been made
	// BEFORE deriving anything, so a workflow that already holds an IRN is never
	// re-built and never re-validated.
	done, err := store.Load(workflowID)
	if err != nil {
		return IRNResult{}, fmt.Errorf("statutory: load workflow %q: %w", workflowID, err)
	}
	generated := false
	for _, r := range done {
		if r.Name == StepGenerate {
			generated = true
			break
		}
	}

	// THE PURE PREFIX, re-derived on every unsettled attempt and stored nowhere.
	var payload EInvoicePayload
	if !generated {
		payload = BuildEInvoice(snap)
		if violations := ValidateEInvoice(payload); len(violations) > 0 {
			return IRNResult{}, &ValidationError{Violations: violations}
		}
	}

	steps := []Step{
		{
			Name: StepGenerate,
			Run: func(ctx context.Context, _ map[string]json.RawMessage) (json.RawMessage, error) {
				res, err := provider.GenerateIRN(ctx, payload)
				if err != nil {
					return nil, err
				}
				return json.Marshal(res)
			},
		},
		{
			Name: StepFreeze,
			Run: func(ctx context.Context, prev map[string]json.RawMessage) (json.RawMessage, error) {
				var res IRNResult
				if err := json.Unmarshal(prev[StepGenerate], &res); err != nil {
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
	if err := json.Unmarshal(state[StepGenerate], &res); err != nil {
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
