package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/khata/cored/internal/durable"
	"github.com/khata/cored/internal/intentlog"
	"github.com/khata/cored/internal/llm"
	"github.com/khata/cored/internal/policy"
)

const (
	stepStart  = "start"
	stepFinish = "finish"
)

// Step outputs. Each is what a durable step RECORDS; next reads them back.
// ⛔ Every field that the model sees is listed here, so review of what reaches
// a prompt is review of these structs.

type startOut struct {
	ContextSources []policy.Source `json:"context_sources"`
}

type decideOut struct {
	Decision   *ModelDecision `json:"decision,omitempty"`
	Invalid    string         `json:"invalid,omitempty"`
	PromptHash string         `json:"prompt_hash"`
	Model      string         `json:"served_model,omitempty"`
	Repairs    int            `json:"repairs"`
}

type toolOut struct {
	Tool   string          `json:"tool"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
	// Sources are the provenance classes of the data this result put in the
	// model's context.
	Sources []policy.Source `json:"sources,omitempty"`
	// Preview is set for actions.preview: the exact request that was
	// previewed, which a later commit replays.
	Preview *previewRecord `json:"preview,omitempty"`
	// VerifierError marks a preview the real validators refused.
	VerifierError bool `json:"verifier_error,omitempty"`
	// Terminal ends the run (the bounded retry was spent).
	Terminal bool `json:"terminal,omitempty"`
}

type previewRecord struct {
	ActionID string            `json:"action_id"`
	Params   map[string]string `json:"path_params,omitempty"`
	Body     json.RawMessage   `json:"body"`
	Status   int               `json:"status"`
	Effect   *Effect           `json:"effect,omitempty"`
	// ReversalOf is set when the preview is actions.reverse's: the voucher
	// the previewed body cancels.
	ReversalOf string `json:"reversal_of,omitempty"`
}

type gateOut struct {
	PreviewRef  string      `json:"preview_ref"`
	ActionID    string      `json:"action_id"`
	ACLKey      string      `json:"acl_key"`
	Gate        policy.Gate `json:"gate"`
	AmountPaise *int64      `json:"amount_paise,omitempty"`
	ApprovalID  string      `json:"approval_id,omitempty"`
	DeadlineAt  *time.Time  `json:"deadline_at,omitempty"`
	Error       string      `json:"error,omitempty"`
}

type awaitOut struct {
	ApprovalID string `json:"approval_id"`
	Decision   string `json:"decision"`
	Edited     bool   `json:"edited"`
	Note       string `json:"note,omitempty"`
}

type execOut struct {
	Status     int             `json:"status"`
	OK         bool            `json:"ok"`
	VoucherIDs []string        `json:"voucher_ids,omitempty"`
	ObjectKind string          `json:"object_kind,omitempty"`
	ObjectIDs  []string        `json:"object_ids,omitempty"`
	Replayed   bool            `json:"replayed,omitempty"`
	Body       json.RawMessage `json:"body,omitempty"`
}

type finishOut struct {
	Status string `json:"status"`
	Final  string `json:"final,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type loop struct {
	e   *Engine
	run Run
	// supplied is the decision an EXTERNAL planner handed this Resume
	// (Engine.Submit). It is consumed by the first decide step that runs, and
	// consumedK records which one. Nil for every in-app run.
	supplied  *ModelDecision
	consumedK int
	// locked is set when another process held the run's lock, so nothing ran.
	locked bool
}

// maxDecisions is the engine's bound for this run's planner.
func (l *loop) maxDecisions() int {
	if l.run.External() {
		return l.e.externalMaxDecisions()
	}
	return l.e.maxDecisions()
}

func splitStep(name string) (string, int) {
	i := strings.IndexByte(name, ':')
	if i < 0 {
		return name, -1
	}
	k, err := strconv.Atoi(name[i+1:])
	if err != nil {
		return name, -1
	}
	return name[:i], k
}

func stepName(kind string, k int) string { return fmt.Sprintf("%s:%d", kind, k) }

func decode[T any](raw json.RawMessage) (T, error) {
	var v T
	err := json.Unmarshal(raw, &v)
	return v, err
}

// next is the run's state machine, a PURE function of the recorded steps
// (durable.Next's contract). It never reads the database, the clock or the
// model; the steps it returns do.
func (l *loop) next(done []durable.StepResult) (durable.Step, bool, error) {
	if len(done) == 0 {
		return l.startStep(), true, nil
	}
	last := done[len(done)-1]
	kind, k := splitStep(last.Name)
	switch kind {
	case stepFinish:
		return durable.Step{}, false, nil
	case stepStart:
		return l.decideStep(0), true, nil
	case "decide":
		d, err := decode[decideOut](last.Output)
		if err != nil {
			return durable.Step{}, false, err
		}
		switch {
		case d.Decision == nil:
			return l.finishStep(finishOut{Status: string(StatusFailed), Reason: "the model produced no usable decision: " + d.Invalid}), true, nil
		case d.Decision.Tool == ToolFinish:
			return l.finishStep(finishOut{Status: string(StatusCompleted), Final: d.Decision.Final}), true, nil
		case d.Decision.Tool == ToolCommit:
			return l.gateStep(k), true, nil
		default:
			return l.toolStep(k), true, nil
		}
	case "tool":
		t, err := decode[toolOut](last.Output)
		if err != nil {
			return durable.Step{}, false, err
		}
		if t.Terminal {
			return l.finishStep(finishOut{Status: string(StatusFailed), Reason: t.Error}), true, nil
		}
		return l.decideOrStop(k + 1), true, nil
	case "gate":
		g, err := decode[gateOut](last.Output)
		if err != nil {
			return durable.Step{}, false, err
		}
		switch g.Gate.Mode {
		case policy.ModeApproval:
			return l.awaitStep(k), true, nil
		case policy.ModeAutonomous:
			return l.execStep(k), true, nil
		default:
			return l.decideOrStop(k + 1), true, nil
		}
	case "await":
		a, err := decode[awaitOut](last.Output)
		if err != nil {
			return durable.Step{}, false, err
		}
		switch a.Decision {
		case "approve":
			return l.execStep(k), true, nil
		case "expired":
			// ⛔ TERMINAL. An expired approval ends the run without asking the
			// model anything: a run that could re-propose after expiry would
			// turn "the approver did not answer" into "ask again until they do".
			return l.finishStep(finishOut{Status: string(StatusExpired), Reason: "the approval window closed with no decision"}), true, nil
		default:
			return l.decideOrStop(k + 1), true, nil
		}
	case "exec":
		return l.decideOrStop(k + 1), true, nil
	}
	return durable.Step{}, false, fmt.Errorf("agent: unknown recorded step %q", last.Name)
}

func (l *loop) decideOrStop(k int) durable.Step {
	if k >= l.maxDecisions() {
		return l.finishStep(finishOut{Status: string(StatusFailed),
			Reason: fmt.Sprintf("the run did not finish within %d decisions", l.maxDecisions())})
	}
	return l.decideStep(k)
}

// contextSources is every provenance class whose content reached the model in
// this run so far: the instruction (user), every input, and every tool result.
func (l *loop) contextSources(prev map[string]json.RawMessage) []policy.Source {
	seen := map[policy.Source]bool{policy.SourceUser: true}
	out := []policy.Source{policy.SourceUser}
	add := func(s policy.Source) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, in := range l.run.Inputs {
		add(in.Source)
	}
	for name, raw := range prev {
		if kind, _ := splitStep(name); kind != "tool" {
			continue
		}
		if t, err := decode[toolOut](raw); err == nil {
			for _, s := range t.Sources {
				add(s)
			}
		}
	}
	return out
}

func (l *loop) startStep() durable.Step {
	return durable.Step{Name: stepStart, Run: func(ctx context.Context, prev map[string]json.RawMessage) (json.RawMessage, error) {
		return json.Marshal(startOut{ContextSources: l.contextSources(prev)})
	}}
}

// transcript rebuilds the past turns from the recorded steps.
func (l *loop) transcript(prev map[string]json.RawMessage, upTo int) []turn {
	var turns []turn
	for j := 0; j < upTo; j++ {
		d, err := decode[decideOut](prev[stepName("decide", j)])
		if err != nil || d.Decision == nil {
			continue
		}
		dec, _ := json.Marshal(d.Decision)
		res := map[string]json.RawMessage{}
		for _, kind := range []string{"tool", "gate", "await", "exec"} {
			if raw, ok := prev[stepName(kind, j)]; ok {
				res[kind] = modelView(kind, raw)
			}
		}
		out, _ := json.Marshal(res)
		turns = append(turns, turn{decision: dec, result: out})
	}
	return turns
}

// modelView is what of a recorded step the MODEL is shown. A gate's reasons
// and an approval's state are shown; approval ids are not needed and are not.
func modelView(kind string, raw json.RawMessage) json.RawMessage {
	switch kind {
	case "gate":
		g, err := decode[gateOut](raw)
		if err != nil {
			return raw
		}
		v, _ := json.Marshal(map[string]any{"mode": g.Gate.Mode, "reasons": g.Gate.Reasons, "error": g.Error})
		return v
	case "await":
		a, err := decode[awaitOut](raw)
		if err != nil {
			return raw
		}
		v, _ := json.Marshal(map[string]any{"decision": a.Decision, "note": a.Note, "edited": a.Edited})
		return v
	}
	return raw
}

func (l *loop) decideStep(k int) durable.Step {
	name := stepName("decide", k)
	return durable.Step{Name: name, Run: func(ctx context.Context, prev map[string]json.RawMessage) (json.RawMessage, error) {
		if l.run.External() {
			return l.externalDecision(ctx, name, k)
		}
		req := buildRequest(l.run, l.transcript(prev, k))
		var md ModelDecision
		resp, err := l.e.LLM.DecodeInto(ctx, req, DecideTool, &md)
		out := decideOut{PromptHash: RequestHash(req)}
		if resp != nil {
			out.Model, out.Repairs = resp.Fingerprint.ServedModel, resp.Repairs
		}
		switch {
		case errors.Is(err, llm.ErrNoStructuredOutput):
			// The model could not produce the shape even after repairs: a
			// terminal outcome, recorded, not retried by the poller.
			out.Invalid = "no structured decision after repairs"
		case err != nil:
			// Transport, budget, provider: not recorded, so the poller
			// retries this step with backoff.
			return nil, err
		case !validTool(md.Tool):
			out.Invalid = "the decision named a tool that does not exist"
		default:
			if len(md.Args) == 0 {
				md.Args = json.RawMessage(`{}`)
			}
			out.Decision = &md
		}
		tool := ""
		if out.Decision != nil {
			tool = out.Decision.Tool
		}
		var argsHash string
		if out.Decision != nil {
			argsHash = hashText(string(out.Decision.Args))
		}
		if err := l.e.appendIntentTx(ctx, l.run, intentlog.Event{Kind: intentlog.KindDecisionStep,
			ActorKind: intentlog.ActorAgent, Payload: map[string]any{
				"k": k, "tool": tool, "args_hash": argsHash, "prompt_hash": out.PromptHash,
				"served_model": out.Model, "repairs": out.Repairs, "invalid": out.Invalid != ""}}); err != nil {
			return nil, err
		}
		return json.Marshal(out)
	}}
}

func (l *loop) decision(prev map[string]json.RawMessage, k int) (ModelDecision, error) {
	d, err := decode[decideOut](prev[stepName("decide", k)])
	if err != nil {
		return ModelDecision{}, err
	}
	if d.Decision == nil {
		return ModelDecision{}, errors.New("agent: no decision recorded")
	}
	return *d.Decision, nil
}

func (l *loop) finishStep(f finishOut) durable.Step {
	return durable.Step{Name: stepFinish, Run: func(ctx context.Context, prev map[string]json.RawMessage) (json.RawMessage, error) {
		kind := intentlog.KindRunFinished
		if f.Status != string(StatusCompleted) {
			kind = intentlog.KindRunFailed
		}
		if err := l.e.appendIntentTx(ctx, l.run, intentlog.Event{Kind: kind, ActorKind: intentlog.ActorAgent,
			Payload: map[string]any{"status": f.Status, "reason": f.Reason, "final_hash": hashText(f.Final)}}); err != nil {
			return nil, err
		}
		return json.Marshal(f)
	}}
}
