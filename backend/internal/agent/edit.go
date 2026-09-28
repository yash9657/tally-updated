package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/actions"
)

// edit.go is the re-preview of an approver's edited body (wave-1 review
// finding #2's owed half, D121 "Wave 2 breadth").
//
// ⛔ AN EDITED BODY IS PREVIEWED BEFORE IT IS APPROVED, BY THE SAME HANDLER.
// Wave 1 committed an approver's edit without ever showing its effect: the
// approver changed "25,000" to "2,50,000" on a form and the inbox still drew
// the model's 25,000 legs, so the one human check on the commit was a check of
// a body that would not be posted. Now the decision handler runs the edit
// through ModePreview — the real handler, rolled back — and records the effect
// with the decision. A body the validators refuse is refused AT the decision
// (a 422 the approver sees on the form), not discovered afterwards as a
// commit_refused event in a side panel.

// ErrEditRefused is an edited body the real handler refused in preview.
type ErrEditRefused struct {
	Status  int
	Message string
}

func (e *ErrEditRefused) Error() string {
	return fmt.Sprintf("agent: the edited body was refused (status %d): %s", e.Status, e.Message)
}

// PreviewEdit dry-runs body as the approval's action, on the run's behalf, and
// returns the effect. It writes nothing and records nothing; the caller records
// the result with the decision.
//
// The dispatch carries the run's own principal and delegation — the same
// caller the run's own preview used — so an edit cannot reach an action, a
// path or a right the proposal did not already have: the action and its path
// parameters are the approval's, only the body is the approver's.
func (e *Engine) PreviewEdit(ctx context.Context, companyID, approvalID uuid.UUID, body json.RawMessage) (EditedPreview, error) {
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		return EditedPreview{}, fmt.Errorf("%w: edited body must be a JSON object", ErrInvalid)
	}
	a, err := e.GetApproval(ctx, companyID, approvalID)
	if err != nil {
		return EditedPreview{}, err
	}
	run, err := e.GetRun(ctx, companyID, a.RunID)
	if err != nil {
		return EditedPreview{}, err
	}
	spec, ok := actions.ByID(a.ActionID)
	if !ok {
		return EditedPreview{}, fmt.Errorf("%w: the approval's action %q is no longer in the registry", ErrInvalid, a.ActionID)
	}
	var args struct {
		Params map[string]string `json:"path_params"`
	}
	if err := json.Unmarshal(a.Args, &args); err != nil {
		return EditedPreview{}, fmt.Errorf("agent: decode approval args: %w", err)
	}
	path, err := concretePath(spec, companyID, args.Params)
	if err != nil {
		return EditedPreview{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	caller := Caller{Principal: run.Principal(), Subject: run.Subject, IssuedAt: run.CreatedAt, WebAuthn: run.Delegation.StepUp}
	res, err := e.Dispatch.Dispatch(ctx, caller, Request{Mode: ModePreview, Spec: spec, Path: path, Body: body,
		RunID: run.ID, Params: args.Params})
	if err != nil {
		return EditedPreview{}, err
	}
	ep := EditedPreview{Status: res.Status, Effect: res.Effect}
	if !res.OK() {
		return ep, &ErrEditRefused{Status: res.Status, Message: verifierMessage(res.Body)}
	}
	if res.Effect.Empty() {
		return ep, &ErrEditRefused{Status: 422, Message: "the edited body has no effect on the books"}
	}
	// The delegated ceiling is the run's; the edit is held to it here, where
	// the approver can still change it, as well as in the commit's own
	// transaction (agent_dispatch.go), which is the control.
	if c := run.Delegation.MaxAmountPaise; c != nil && res.Effect.DebitPaise > *c {
		return ep, &ErrEditRefused{Status: 422, Message: fmt.Sprintf(
			"the edited body debits %d paise, above this run's delegated ceiling of %d paise", res.Effect.DebitPaise, *c)}
	}
	return ep, nil
}
