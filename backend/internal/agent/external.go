package agent

// external.go is the W6 entry point (D126): a run whose PLANNER is outside the
// server — an MCP client — driven one decision at a time through the SAME loop
// an in-app run uses.
//
// # WHY THE OUTSIDE PLANNER TAKES THE MODEL'S SEAT AND NOTHING ELSE
//
// An in-app run is start → decide:k (the model) → tool:k | gate:k → await:k →
// exec:k → … . For an MCP session the client's tools/call IS decide:k: it names
// one tool and its arguments, exactly the ModelDecision the model returns. So
// Submit records that decision as decide:k and then lets loop.next derive every
// later step as it always does — preview through the dispatcher, the policy
// gate, the approval park, the idempotent commit, the intent log. There is no
// second preview path, no second gate and no second commit path for the MCP
// server to get wrong, and internal/mcp holds no Dispatcher: the only way its
// tool calls reach a handler is this file handing them to the loop.
//
// The only differences, each deliberate:
//
//   - decide:k never calls a model. With no supplied decision it PARKS
//     ("awaiting the external planner"), so the poller visiting an idle MCP
//     run records nothing and asks nobody anything.
//   - the run is born with policy.SourceMCPClient in its context: the client's
//     model saw things this server never did, so the provenance gate treats
//     every MCP run as tainted, at every autonomy level.
//   - the decision bound is ExternalMaxDecisions, not eight.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/durable"
	"github.com/khata/cored/internal/intentlog"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/policy"
)

// ExternalAgentPrefix marks a run whose planner is an outside MCP client. The
// agent_id is "mcp:<grant id>", so the intent log names WHICH standing grant a
// decision was made under.
//
// ⛔ THE AGENT ID, NOT THE CHANNEL, DECIDES "EXTERNAL". POST /agent/runs takes a
// caller-chosen channel label (including "mcp"); it never takes an agent id,
// which Start always sets to policy.AgentID. So no in-app run can be made to
// park waiting for a planner that does not exist.
const ExternalAgentPrefix = "mcp:"

// DefaultExternalMaxDecisions bounds one MCP session's tool calls. A session
// that needs more starts a new one (a new run, born tainted the same way).
const DefaultExternalMaxDecisions = 200

// External reports whether this run's planner is outside the server.
func (r Run) External() bool { return strings.HasPrefix(r.AgentID, ExternalAgentPrefix) }

func (e *Engine) externalMaxDecisions() int {
	if e.ExternalMaxDecisions > 0 {
		return e.ExternalMaxDecisions
	}
	return DefaultExternalMaxDecisions
}

// ExternalStartRequest starts a run for an outside planner. Every identity
// field comes from the authenticated request and the human's stored grant,
// never from the client's JSON-RPC body.
type ExternalStartRequest struct {
	CompanyID uuid.UUID
	UserID    uuid.UUID
	Subject   string
	// AgentID must carry ExternalAgentPrefix.
	AgentID string
	// Instruction is what the approver reads as "what was asked": the client
	// and the grant, since an MCP client sends no instruction of its own.
	Instruction    string
	DelegatedKeys  []string
	MaxAmountPaise *int64
	ExpiresAt      time.Time
	StepUp         bool
}

// externalContextLabel labels the born-tainted input.
const externalContextLabel = "the MCP client's own model context (not visible to this server)"

// StartExternal creates a run whose decisions will come from Submit. It needs
// no model. The delegation is validated by Start's own rules.
func (e *Engine) StartExternal(ctx context.Context, req ExternalStartRequest) (Run, error) {
	if !strings.HasPrefix(req.AgentID, ExternalAgentPrefix) || len(req.AgentID) == len(ExternalAgentPrefix) {
		return Run{}, fmt.Errorf("%w: an external agent id must start with %q", ErrInvalid, ExternalAgentPrefix)
	}
	if req.ExpiresAt.IsZero() {
		return Run{}, fmt.Errorf("%w: an external run needs the grant's expiry", ErrInvalid)
	}
	return e.start(ctx, StartRequest{
		CompanyID: req.CompanyID, UserID: req.UserID, Subject: req.Subject, Channel: ChannelMCP,
		Instruction: req.Instruction,
		// ⛔ BORN TAINTED. Empty text: there is nothing of the client's context
		// to store, only the fact that it exists and was not seen.
		Inputs:        []Input{{Label: externalContextLabel, Source: policy.SourceMCPClient}},
		DelegatedKeys: req.DelegatedKeys, MaxAmountPaise: req.MaxAmountPaise, StepUp: req.StepUp,
	}, req.AgentID, req.ExpiresAt)
}

// ErrNotAwaitingPlanner is Submit's answer when the run cannot take a decision
// now: it is parked on a human's approval, it has finished, or another process
// is executing it. Nothing was recorded.
var ErrNotAwaitingPlanner = errors.New("agent: the run is not waiting for the planner's next call")

// ExternalOutcome is what one submitted decision produced: the recorded steps
// for its k, read back from the durable record (never from in-memory state, so
// a restart between the call and this read changes nothing).
type ExternalOutcome struct {
	K      int    `json:"k"`
	Tool   string `json:"tool"`
	Status Status `json:"run_status"`
	// Tool result, for every tool but commit: what the model would have seen.
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
	// PreviewRef is "tool:k" when this call produced a successful preview;
	// it is the only thing a commit may name.
	PreviewRef string  `json:"preview_ref,omitempty"`
	Effect     *Effect `json:"effect,omitempty"`
	// Gate, Approval and Commit are set for a commit call.
	Gate   *ExternalGate   `json:"gate,omitempty"`
	Commit *ExternalCommit `json:"commit,omitempty"`
	// Finished is the run's end, when this call ended it.
	Finished *ExternalFinish `json:"finished,omitempty"`
}

// ExternalGate is a gate:k record as an outside planner sees it.
type ExternalGate struct {
	Mode        policy.Mode `json:"mode"`
	Level       string      `json:"level,omitempty"`
	Reasons     []string    `json:"reasons"`
	ApprovalID  string      `json:"approval_id,omitempty"`
	DeadlineAt  *time.Time  `json:"deadline_at,omitempty"`
	AmountPaise *int64      `json:"amount_paise,omitempty"`
	ActionID    string      `json:"action_id,omitempty"`
	PreviewRef  string      `json:"preview_ref,omitempty"`
	// Decision is the approval's recorded decision once a human made one
	// (approve | reject | ask_back | expired); empty while pending.
	Decision string `json:"decision,omitempty"`
	Note     string `json:"note,omitempty"`
}

// ExternalCommit is an exec:k record.
type ExternalCommit struct {
	K          int             `json:"k"`
	Status     int             `json:"status"`
	OK         bool            `json:"ok"`
	VoucherIDs []string        `json:"voucher_ids,omitempty"`
	Replayed   bool            `json:"replayed,omitempty"`
	Body       json.RawMessage `json:"body,omitempty"`
}

// ExternalFinish is the finish record.
type ExternalFinish struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// ExternalCommitView is one commit attempt of a run, for the planner's status
// read: its gate, the human's decision if any, and the commit if it ran.
type ExternalCommitView struct {
	Gate   ExternalGate    `json:"gate"`
	K      int             `json:"k"`
	Commit *ExternalCommit `json:"commit,omitempty"`
}

// ExternalStatus is the run's record as its outside planner may see it.
type ExternalStatus struct {
	RunID    uuid.UUID            `json:"run_id"`
	Status   Status               `json:"run_status"`
	Commits  []ExternalCommitView `json:"commits"`
	Finished *ExternalFinish      `json:"finished,omitempty"`
}

// Submit hands one decision from the run's outside planner to the loop and
// advances the run as far as it goes: a read or preview completes; a commit is
// gated and either committed (autonomous) or parked on an approval.
//
// ⛔ THE DECISION IS RECORDED AS decide:k AND NOTHING ELSE. Every later step is
// loop.next's, from the record — so a commit's body is the recorded preview's
// (or the approver's edit), never anything the client sends with the commit.
func (e *Engine) Submit(ctx context.Context, companyID, runID uuid.UUID, d ModelDecision) (ExternalOutcome, error) {
	if len(d.Args) == 0 {
		d.Args = json.RawMessage(`{}`)
	}
	run, err := e.GetRun(ctx, companyID, runID)
	if err != nil {
		return ExternalOutcome{}, err
	}
	if !run.External() {
		return ExternalOutcome{}, fmt.Errorf("%w: run %s is not driven by an external planner", ErrInvalid, runID)
	}
	// A poller holding the run is at most one cheap park away from releasing
	// it; wait for that rather than bouncing the client.
	var lp *loop
	var st Status
	for attempt := 0; ; attempt++ {
		dec := d
		st, lp, err = e.resume(ctx, companyID, runID, &dec)
		if err != nil && (lp == nil || lp.consumedK < 0) {
			return ExternalOutcome{Status: st}, err
		}
		if lp == nil || !lp.locked || attempt >= 60 {
			break
		}
		select {
		case <-ctx.Done():
			return ExternalOutcome{}, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	if lp == nil || lp.consumedK < 0 {
		return ExternalOutcome{Status: st}, ErrNotAwaitingPlanner
	}
	out, rerr := e.readOutcome(ctx, companyID, runID, lp.consumedK)
	if rerr != nil {
		return ExternalOutcome{}, rerr
	}
	out.Status = st
	// A transient failure AFTER the decision was recorded: the poller retries
	// the step under the same record; the planner is told so.
	return out, err
}

func (e *Engine) loadSteps(ctx context.Context, companyID, runID uuid.UUID) (map[string]json.RawMessage, error) {
	done, err := e.store().Load(db.WithCompany(ctx, companyID), runID.String())
	if err != nil {
		return nil, err
	}
	m := make(map[string]json.RawMessage, len(done))
	for _, s := range done {
		m[s.Name] = s.Output
	}
	return m, nil
}

func (e *Engine) readOutcome(ctx context.Context, companyID, runID uuid.UUID, k int) (ExternalOutcome, error) {
	steps, err := e.loadSteps(ctx, companyID, runID)
	if err != nil {
		return ExternalOutcome{}, err
	}
	out := ExternalOutcome{K: k}
	if d, err := decode[decideOut](steps[stepName("decide", k)]); err == nil && d.Decision != nil {
		out.Tool = d.Decision.Tool
	}
	if raw, ok := steps[stepName("tool", k)]; ok {
		t, err := decode[toolOut](raw)
		if err != nil {
			return ExternalOutcome{}, err
		}
		out.OK, out.Result, out.Error = t.OK, t.Result, t.Error
		if t.Preview != nil && t.OK {
			out.PreviewRef = stepName("tool", k)
			out.Effect = t.Preview.Effect
		}
	}
	if raw, ok := steps[stepName("gate", k)]; ok {
		g, err := e.gateView(ctx, companyID, raw, steps, k)
		if err != nil {
			return ExternalOutcome{}, err
		}
		out.Gate = &g
		out.OK, out.Error = true, ""
		if g.Mode == policy.ModeRefuse {
			out.OK, out.Error = false, strings.Join(g.Reasons, "; ")
		}
	}
	if raw, ok := steps[stepName("exec", k)]; ok {
		c, err := execView(k, raw)
		if err != nil {
			return ExternalOutcome{}, err
		}
		out.Commit = &c
		out.OK = c.OK
	}
	if raw, ok := steps[stepFinish]; ok {
		f, err := decode[finishOut](raw)
		if err == nil {
			out.Finished = &ExternalFinish{Status: f.Status, Reason: f.Reason}
		}
	}
	return out, nil
}

func (e *Engine) gateView(ctx context.Context, companyID uuid.UUID, raw json.RawMessage, steps map[string]json.RawMessage, k int) (ExternalGate, error) {
	g, err := decode[gateOut](raw)
	if err != nil {
		return ExternalGate{}, err
	}
	v := ExternalGate{Mode: g.Gate.Mode, Level: g.Gate.Level, Reasons: g.Gate.Reasons, ApprovalID: g.ApprovalID,
		DeadlineAt: g.DeadlineAt, AmountPaise: g.AmountPaise, ActionID: g.ActionID, PreviewRef: g.PreviewRef}
	if a, ok := steps[stepName("await", k)]; ok {
		aw, err := decode[awaitOut](a)
		if err == nil {
			v.Decision, v.Note = aw.Decision, aw.Note
		}
	} else if g.ApprovalID != "" {
		// Not yet read by the run: the inbox may already hold a decision the
		// poller has not acted on. Read it so the planner is not told
		// "pending" about an approval a human has decided.
		if id, err := uuid.Parse(g.ApprovalID); err == nil {
			if ap, err := e.GetApproval(ctx, companyID, id); err == nil && ap.Decision != nil {
				v.Decision, v.Note = ap.Decision.Decision, ap.Decision.Note
			}
		}
	}
	return v, nil
}

func execView(k int, raw json.RawMessage) (ExternalCommit, error) {
	x, err := decode[execOut](raw)
	if err != nil {
		return ExternalCommit{}, err
	}
	return ExternalCommit{K: k, Status: x.Status, OK: x.OK, VoucherIDs: x.VoucherIDs, Replayed: x.Replayed, Body: x.Body}, nil
}

// StatusExternal reads an external run's commit attempts from its record. It
// is a read of the agent plane's own tables, not a tool: it reaches no handler
// and records no decision, so it answers even while the run is parked on an
// approval (when Submit cannot take a decision).
func (e *Engine) StatusExternal(ctx context.Context, companyID, runID uuid.UUID) (ExternalStatus, error) {
	run, err := e.GetRun(ctx, companyID, runID)
	if err != nil {
		return ExternalStatus{}, err
	}
	if !run.External() {
		return ExternalStatus{}, fmt.Errorf("%w: run %s is not driven by an external planner", ErrInvalid, runID)
	}
	steps, err := e.loadSteps(ctx, companyID, runID)
	if err != nil {
		return ExternalStatus{}, err
	}
	out := ExternalStatus{RunID: runID, Status: run.Status, Commits: []ExternalCommitView{}}
	for k := 0; k < e.externalMaxDecisions(); k++ {
		if _, ok := steps[stepName("decide", k)]; !ok {
			break
		}
		raw, ok := steps[stepName("gate", k)]
		if !ok {
			continue
		}
		g, err := e.gateView(ctx, companyID, raw, steps, k)
		if err != nil {
			return ExternalStatus{}, err
		}
		cv := ExternalCommitView{Gate: g, K: k}
		if x, ok := steps[stepName("exec", k)]; ok {
			c, err := execView(k, x)
			if err != nil {
				return ExternalStatus{}, err
			}
			cv.Commit = &c
		}
		out.Commits = append(out.Commits, cv)
	}
	if raw, ok := steps[stepFinish]; ok {
		if f, err := decode[finishOut](raw); err == nil {
			out.Finished = &ExternalFinish{Status: f.Status, Reason: f.Reason}
		}
	}
	return out, nil
}

// externalDecision is decide:k for an external run: record the supplied
// decision, or park until one is supplied.
func (l *loop) externalDecision(ctx context.Context, name string, k int) (json.RawMessage, error) {
	if l.supplied == nil {
		// Nothing recorded: the run waits, costing a poll and nothing else.
		return nil, durable.Park(name, "awaiting the external planner's next call")
	}
	md := *l.supplied
	l.supplied = nil
	l.consumedK = k
	out := decideOut{Model: l.run.AgentID}
	if !validTool(md.Tool) {
		out.Invalid = "the decision named a tool that does not exist"
	} else {
		if len(md.Args) == 0 {
			md.Args = json.RawMessage(`{}`)
		}
		out.Decision = &md
	}
	tool, argsHash := "", ""
	if out.Decision != nil {
		tool, argsHash = out.Decision.Tool, hashText(string(out.Decision.Args))
	}
	if err := l.e.appendIntentTx(ctx, l.run, intentlog.Event{Kind: intentlog.KindDecisionStep,
		ActorKind: intentlog.ActorAgent, Payload: map[string]any{
			"k": k, "tool": tool, "args_hash": argsHash, "planner": "external",
			"served_model": out.Model, "invalid": out.Invalid != ""}}); err != nil {
		// The decision is not recorded; hand it back so a retry of this
		// Submit is not silently a no-op.
		l.supplied, l.consumedK = &md, -1
		return nil, err
	}
	return json.Marshal(out)
}
