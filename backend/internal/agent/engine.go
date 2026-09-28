package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/actions"
	"github.com/khata/cored/internal/durable"
	"github.com/khata/cored/internal/intentlog"
	"github.com/khata/cored/internal/llm"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/policy"
)

// Defaults. Each is overridable on Engine for tests.
const (
	// DefaultApprovalWindow is the "days-long timeout" of PLAN-AGENTIC W2.
	DefaultApprovalWindow = 72 * time.Hour
	// DefaultMaxDecisions bounds the model calls in one run. A loop that has
	// not finished in eight decisions is a loop that is not converging, and
	// every further call is spend with no evidence it will help.
	DefaultMaxDecisions = 8
	// DefaultParkedPoll is how soon a parked run is looked at again when
	// nothing nudges it. A decision written through the API nudges it at once
	// (next_poll_at = now), so this only bounds how late an EXPIRY is noticed.
	DefaultParkedPoll = time.Minute
	// DefaultMaxAttempts bounds TRANSIENT failures (provider down, database
	// blip) before a run is failed. Parks and lock contention do not count.
	DefaultMaxAttempts = 5
	// maxInputs / maxInputBytes / maxInstructionBytes bound what a caller can
	// put in front of the model before any socket opens.
	maxInputs           = 10
	maxInputBytes       = 20000
	maxInstructionBytes = 4000
)

// Engine runs agent runs.
type Engine struct {
	Pool     *db.Pool
	LLM      *llm.Client
	Dispatch Dispatcher
	// Profiles is W3's profile tools behind internal/server's ACL filter
	// (profiles.go). Nil means the tools answer "not available".
	Profiles ProfileReader
	// Now is the one clock: step deadlines, the poller's due query and the
	// intent log all read it, so the 72-hour test can drive it.
	Now            func() time.Time
	ApprovalWindow time.Duration
	MaxDecisions   int
	ParkedPoll     time.Duration
	MaxAttempts    int
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}

// Clock is the engine clock, exported for the dispatcher: the router judges a
// delegation expiry on the same clock the run was started and gated on.
func (e *Engine) Clock() time.Time { return e.now() }

func (e *Engine) window() time.Duration {
	if e.ApprovalWindow > 0 {
		return e.ApprovalWindow
	}
	return DefaultApprovalWindow
}

func (e *Engine) maxDecisions() int {
	if e.MaxDecisions > 0 {
		return e.MaxDecisions
	}
	return DefaultMaxDecisions
}

func (e *Engine) parkedPoll() time.Duration {
	if e.ParkedPoll > 0 {
		return e.ParkedPoll
	}
	return DefaultParkedPoll
}

func (e *Engine) maxAttempts() int {
	if e.MaxAttempts > 0 {
		return e.MaxAttempts
	}
	return DefaultMaxAttempts
}

func (e *Engine) store() *runStore {
	return &runStore{pool: e.Pool, locker: durable.AdvisoryLocker{Pool: e.Pool.Raw(), Namespace: "agent"}}
}

// StartRequest is what starting a run needs. CompanyID, UserID and Subject come
// from the AUTHENTICATED request, never from its body.
type StartRequest struct {
	CompanyID   uuid.UUID
	UserID      uuid.UUID
	Subject     string
	Channel     Channel
	Instruction string
	Inputs      []Input
	// DelegatedKeys are the ACL keys the human grants this run. Empty means
	// the run may read but not write.
	DelegatedKeys  []string
	MaxAmountPaise *int64
	// StepUp is whether the starting request carried a WebAuthn step-up.
	StepUp bool
}

// Start creates a run (status running, due now) and writes its run_started
// intent in the same transaction. It does not execute any step; the poller (or
// the caller, via Resume) does.
func (e *Engine) Start(ctx context.Context, req StartRequest) (Run, error) {
	if e.LLM == nil {
		return Run{}, ErrNoModel
	}
	instr := strings.TrimSpace(req.Instruction)
	switch {
	case instr == "":
		return Run{}, fmt.Errorf("%w: instruction is empty", ErrInvalid)
	case len(instr) > maxInstructionBytes:
		return Run{}, fmt.Errorf("%w: instruction exceeds %d bytes", ErrInvalid, maxInstructionBytes)
	case len(req.Inputs) > maxInputs:
		return Run{}, fmt.Errorf("%w: at most %d inputs", ErrInvalid, maxInputs)
	case req.UserID == uuid.Nil || req.Subject == "":
		return Run{}, fmt.Errorf("%w: no authenticated user", ErrInvalid)
	}
	if req.Channel == "" {
		req.Channel = ChannelPanel
	}
	for i, in := range req.Inputs {
		if !policy.ValidSource(in.Source) || in.Source.Trusted() {
			// ⛔ AN INPUT IS EXTERNAL BY DEFINITION. Accepting provenance
			// "user" or "books" on one would let a caller launder a bank
			// narration into trusted context by labelling it.
			return Run{}, fmt.Errorf("%w: input %d has provenance %q; inputs must be one of document, bank, whatsapp, tally_xml, email", ErrInvalid, i, in.Source)
		}
		if len(in.Text) > maxInputBytes {
			return Run{}, fmt.Errorf("%w: input %d exceeds %d bytes", ErrInvalid, i, maxInputBytes)
		}
	}
	seen := map[string]bool{}
	var keys []string
	for _, k := range req.DelegatedKeys {
		if seen[k] {
			continue
		}
		seen[k] = true
		specs := actions.ByACLKey(k)
		if len(specs) == 0 {
			return Run{}, fmt.Errorf("%w: %q is not an ACL key any action evaluates to", ErrInvalid, k)
		}
		// Delegating a key an agent can never use is refused at the door
		// rather than carried silently: the human should learn NOW that
		// "delete voucher" cannot be delegated, not at commit time.
		if allStructural(specs) {
			return Run{}, fmt.Errorf("%w: %s cannot be delegated to an agent: %v", ErrInvalid, k, policy.Structural(specs[0]))
		}
		keys = append(keys, k)
	}
	now := e.now()
	inputs := req.Inputs
	if inputs == nil {
		inputs = []Input{}
	}
	run := Run{
		CompanyID: req.CompanyID, CreatedBy: req.UserID, Subject: req.Subject, AgentID: policy.AgentID,
		Channel: req.Channel, Instruction: instr, Inputs: inputs, Status: StatusRunning, NextPollAt: &now,
		Delegation: policy.Delegation{ACLKeys: keys, MaxAmountPaise: req.MaxAmountPaise, StepUp: req.StepUp,
			// Long enough for the approval window plus a working day, so a
			// commit approved in the window's last hour is not then refused
			// because the grant lapsed.
			ExpiresAt: now.Add(e.window() + 24*time.Hour)},
		CreatedAt: now, UpdatedAt: now,
	}
	if run.Delegation.ACLKeys == nil {
		run.Delegation.ACLKeys = []string{}
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Run{}, err
	}
	run.ID = id
	inputsJSON, _ := json.Marshal(run.Inputs)
	delegJSON, _ := json.Marshal(run.Delegation)
	ctx = db.WithCompany(ctx, req.CompanyID)
	err = e.Pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO agent_run (id, company_id, created_by, created_by_subject, agent_id, channel,
			                        instruction, inputs, delegation, status, next_poll_at, created_at, updated_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'running',$10,$10,$10)`,
			run.ID, run.CompanyID, run.CreatedBy, run.Subject, run.AgentID, string(run.Channel),
			run.Instruction, inputsJSON, delegJSON, now); err != nil {
			return err
		}
		_, err := intentlog.Append(ctx, tx, run.CompanyID, intentlog.Event{
			RunID: run.ID, Kind: intentlog.KindRunStarted, ActorKind: intentlog.ActorUser,
			ActorID: run.CreatedBy.String(),
			Payload: map[string]any{
				"instruction_hash": hashText(run.Instruction),
				"channel":          run.Channel,
				"inputs":           inputDigests(run.Inputs),
				"delegated":        run.Delegation.ACLKeys,
				"expires_at":       run.Delegation.ExpiresAt.Format(time.RFC3339),
			},
		}, now)
		return err
	})
	if err != nil {
		return Run{}, fmt.Errorf("agent: start run: %w", err)
	}
	return run, nil
}

func allStructural(specs []actions.ActionSpec) bool {
	for _, s := range specs {
		if policy.Structural(s) == nil {
			return false
		}
	}
	return true
}

// Resume advances a run as far as it will go: to completion, to a park, or to
// a transient error. It is what the poller calls, and it is safe to call from
// any number of processes at once — durable.RunDynamic's advisory lock makes
// all but one of them return immediately.
func (e *Engine) Resume(ctx context.Context, companyID, runID uuid.UUID) (Status, error) {
	ctx = db.WithCompany(ctx, companyID)
	var run Run
	if err := e.Pool.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		run, err = loadRun(ctx, tx, companyID, runID)
		return err
	}); err != nil {
		return "", err
	}
	if run.Status.Finished() {
		return run.Status, nil
	}
	lp := &loop{e: e, run: run}
	done, err := durable.RunDynamic(ctx, e.store(), runID.String(), e.stopWhenEnded(companyID, runID, lp.next))
	now := e.now()
	switch {
	case errors.Is(err, errRunEnded):
		// Cancelled (or otherwise finished) while this Resume held the run:
		// the row already says so, and nothing further was executed.
		if r, gerr := e.GetRun(ctx, companyID, runID); gerr == nil {
			return r.Status, nil
		}
		return StatusCancelled, nil
	case errors.Is(err, durable.ErrLocked):
		// Another poller holds the run; it is doing the work.
		return run.Status, nil
	case errors.Is(err, durable.ErrParked):
		next := now.Add(e.parkedPoll())
		return StatusParked, e.setState(ctx, runID, StatusParked, nil, "", run.Attempts, &next)
	case err != nil:
		attempts := run.Attempts + 1
		if attempts >= e.maxAttempts() {
			log.Printf("agent: run %s failed after %d attempts: %v", runID, attempts, err)
			e.appendIntent(ctx, run, intentlog.Event{Kind: intentlog.KindRunFailed, ActorKind: intentlog.ActorSystem,
				ActorID: "poller", Payload: map[string]any{"reason": "transient failures exhausted", "error_class": errorClass(err)}})
			out, _ := json.Marshal(map[string]string{"status": string(StatusFailed), "reason": "transient failures exhausted"})
			return StatusFailed, e.setState(ctx, runID, StatusFailed, out, truncate(err.Error(), 500), attempts, nil)
		}
		// Exponential backoff: 10s, 20s, 40s, ... — a provider that is down
		// is not helped by being asked again at once.
		next := now.Add(time.Duration(10<<uint(attempts-1)) * time.Second)
		if serr := e.setState(ctx, runID, StatusRunning, nil, truncate(err.Error(), 500), attempts, &next); serr != nil {
			return StatusRunning, serr
		}
		return StatusRunning, err
	}
	var fin finishOut
	if len(done) > 0 && done[len(done)-1].Name == stepFinish {
		_ = json.Unmarshal(done[len(done)-1].Output, &fin)
	}
	st := Status(fin.Status)
	if !st.Finished() {
		st = StatusFailed
	}
	out, _ := json.Marshal(fin)
	return st, e.setState(ctx, runID, st, out, "", run.Attempts, nil)
}

// errRunEnded stops a Resume whose run was finished by someone else — a
// Cancel — while it was executing.
var errRunEnded = errors.New("agent: the run was ended while executing")

// stopWhenEnded wraps next so that EVERY step re-reads the run's status before
// it runs.
//
// ⛔ CANCEL DOES NOT WAIT FOR THE RUN LOCK, SO THE RUNNING RESUME MUST NOTICE.
// Resume checks the status once, before RunDynamic takes the advisory lock;
// a Cancel that lands while a decide:k model call is in flight (seconds to
// minutes) used to be answered 200 "cancelled" while the same Resume went on
// to gate and commit. Cancel deliberately does not take the lock — it would
// block the user behind that model call — so the check is here, per step, and
// the commit itself re-checks under a row lock in the voucher's own
// transaction (agent_dispatch.go), which closes the gap between this read
// and the write. next stays pure; the check is in the step, not in next.
func (e *Engine) stopWhenEnded(companyID, runID uuid.UUID, next durable.Next) durable.Next {
	return func(done []durable.StepResult) (durable.Step, bool, error) {
		step, ok, err := next(done)
		if err != nil || !ok {
			return step, ok, err
		}
		run := step.Run
		step.Run = func(ctx context.Context, prev map[string]json.RawMessage) (json.RawMessage, error) {
			var st Status
			if err := e.Pool.RunInReadOnlyTx(db.WithCompany(ctx, companyID), func(ctx context.Context, tx pgx.Tx) error {
				return tx.QueryRow(ctx, `SELECT status FROM agent_run WHERE id = $1`, runID).Scan(&st)
			}); err != nil {
				return nil, err
			}
			if st.Finished() {
				return nil, errRunEnded
			}
			return run(ctx, prev)
		}
		return step, true, nil
	}
}

func (e *Engine) setState(ctx context.Context, runID uuid.UUID, st Status, outcome json.RawMessage, lastErr string, attempts int, next *time.Time) error {
	now := e.now()
	return e.Pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return setRunState(ctx, tx, runID, st, outcome, lastErr, attempts, next, now)
	})
}

// appendIntent writes one intent event in its own transaction. A failure is
// returned to the step (which then does not record), so the event is retried
// with the step: the log is at-least-once per attempt, which is what "one row
// per attempt" means.
func (e *Engine) appendIntentTx(ctx context.Context, run Run, ev intentlog.Event) error {
	ev.RunID = run.ID
	if ev.ActorKind == intentlog.ActorAgent {
		ev.ActorID = run.AgentID
		ev.OnBehalfOf = run.CreatedBy
	}
	now := e.now()
	return e.Pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := intentlog.Append(ctx, tx, run.CompanyID, ev, now)
		return err
	})
}

func (e *Engine) appendIntent(ctx context.Context, run Run, ev intentlog.Event) {
	if err := e.appendIntentTx(ctx, run, ev); err != nil {
		log.Printf("agent: intent log append for run %s: %v", run.ID, err)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// errorClass is an error's TYPE for the intent log — never its message, which
// can carry party and item names (the trajectory package's SetError rule).
func errorClass(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, llm.ErrUpstreamUnavailable):
		return "llm_upstream_unavailable"
	case errors.Is(err, llm.ErrBudgetExhausted):
		return "llm_budget_exhausted"
	case errors.Is(err, llm.ErrNoStructuredOutput):
		return "llm_no_structured_output"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "context"
	default:
		return fmt.Sprintf("%T", errors.Unwrap(err))
	}
}

// ---------------------------------------------------------------------------
// Reads for the HTTP layer
// ---------------------------------------------------------------------------

// GetRun returns one run.
func (e *Engine) GetRun(ctx context.Context, companyID, runID uuid.UUID) (Run, error) {
	var run Run
	err := e.Pool.RunInReadOnlyTx(db.WithCompany(ctx, companyID), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		run, err = loadRun(ctx, tx, companyID, runID)
		return err
	})
	return run, err
}

// ListRuns returns the newest runs.
func (e *Engine) ListRuns(ctx context.Context, companyID uuid.UUID, limit int) ([]Run, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var runs []Run
	err := e.Pool.RunInReadOnlyTx(db.WithCompany(ctx, companyID), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		runs, err = listRuns(ctx, tx, companyID, limit)
		return err
	})
	return runs, err
}

// Events returns a run's intent-log events after seq.
func (e *Engine) Events(ctx context.Context, companyID, runID uuid.UUID, after int) ([]intentlog.Entry, error) {
	var out []intentlog.Entry
	err := e.Pool.RunInReadOnlyTx(db.WithCompany(ctx, companyID), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := loadRun(ctx, tx, companyID, runID); err != nil {
			return err
		}
		var err error
		out, err = intentlog.List(ctx, tx, companyID, runID, after)
		return err
	})
	return out, err
}

// VerifyRun recomputes a run's intent chain.
func (e *Engine) VerifyRun(ctx context.Context, companyID, runID uuid.UUID) (int, error) {
	var n int
	var verr error
	err := e.Pool.RunInReadOnlyTx(db.WithCompany(ctx, companyID), func(ctx context.Context, tx pgx.Tx) error {
		n, verr = intentlog.Verify(ctx, tx, companyID, runID)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, verr
}

// ListApprovals returns the inbox. state "" or "pending" returns undecided
// approvals; "all" returns every approval; runID non-nil scopes to one run.
func (e *Engine) ListApprovals(ctx context.Context, companyID uuid.UUID, state string, runID uuid.UUID) ([]Approval, error) {
	var out []Approval
	err := e.Pool.RunInReadOnlyTx(db.WithCompany(ctx, companyID), func(ctx context.Context, tx pgx.Tx) error {
		where, args := "true", []any{}
		if state == "" || state == "pending" {
			where = "d.id IS NULL"
		}
		if runID != uuid.Nil {
			args = append(args, runID)
			where += fmt.Sprintf(" AND a.run_id = $%d", len(args))
		}
		var err error
		out, err = listApprovals(ctx, tx, where, args...)
		return err
	})
	return out, err
}

// GetApproval returns one approval.
func (e *Engine) GetApproval(ctx context.Context, companyID, approvalID uuid.UUID) (Approval, error) {
	var a Approval
	err := e.Pool.RunInReadOnlyTx(db.WithCompany(ctx, companyID), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		a, err = loadApproval(ctx, tx, approvalID)
		return err
	})
	return a, err
}

// Cancel ends an unfinished run on a human's word. A pending approval of a
// cancelled run can still be decided, but nothing will ever commit it: Resume
// returns at once for a finished run.
func (e *Engine) Cancel(ctx context.Context, companyID, runID, by uuid.UUID) (Run, error) {
	now := e.now()
	ctx = db.WithCompany(ctx, companyID)
	var run Run
	err := e.Pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		run, err = loadRun(ctx, tx, companyID, runID)
		if err != nil {
			return err
		}
		if run.Status.Finished() {
			return nil
		}
		// ⚠️ THE INTENT APPEND FIRST, THEN THE ROW UPDATE — the same order
		// Decide takes them (intentlog's per-run lock, then agent_run), so a
		// cancel and a decision on one run cannot deadlock each other.
		if _, err := intentlog.Append(ctx, tx, companyID, intentlog.Event{RunID: runID, Kind: intentlog.KindRunFailed,
			ActorKind: intentlog.ActorUser, ActorID: by.String(), Payload: map[string]any{"status": "cancelled"}}, now); err != nil {
			return err
		}
		out, _ := json.Marshal(finishOut{Status: string(StatusCancelled), Reason: "cancelled by the user"})
		if err := setRunState(ctx, tx, runID, StatusCancelled, out, "", run.Attempts, nil, now); err != nil {
			return err
		}
		run, err = loadRun(ctx, tx, companyID, runID)
		return err
	})
	return run, err
}

// DecisionInput is a human's decision on an approval.
type DecisionInput struct {
	// Decision is approve | reject | ask_back. ("expired" is the system's.)
	Decision string
	// EditedBody, for edit-then-approve, replaces the previewed body. The
	// commit runs it through the same handler and validators.
	EditedBody json.RawMessage
	Note       string
	DecidedBy  uuid.UUID
	WebAuthn   bool
	// EditedPreview is the dry run of EditedBody the caller made before
	// deciding (internal/server's decision handler), recorded with the
	// decision so the effect the approver saw is the one on record — and the
	// one an ALTER's commit is held to.
	EditedPreview *EditedPreview
}

// Decide records a human decision and nudges the run so the poller picks it
// up at once.
//
// ⛔ WHO MAY CALL THIS IS DECIDED BY THE CALLER (internal/server): an agent
// principal never (policy.CanDecide), and a human only if their own ACL allows
// the underlying action — the checker must hold the authority being exercised.
func (e *Engine) Decide(ctx context.Context, companyID, approvalID uuid.UUID, in DecisionInput) (Approval, error) {
	switch in.Decision {
	case "approve", "reject", "ask_back":
	default:
		return Approval{}, fmt.Errorf("%w: decision must be approve, reject or ask_back", ErrInvalid)
	}
	if in.Decision == "ask_back" && strings.TrimSpace(in.Note) == "" {
		return Approval{}, fmt.Errorf("%w: ask_back needs a note saying what to ask", ErrInvalid)
	}
	if len(in.EditedBody) > 0 {
		if in.Decision != "approve" {
			return Approval{}, fmt.Errorf("%w: an edited body is only meaningful with approve", ErrInvalid)
		}
		var obj map[string]any
		if err := json.Unmarshal(in.EditedBody, &obj); err != nil {
			return Approval{}, fmt.Errorf("%w: edited body must be a JSON object", ErrInvalid)
		}
	}
	if in.DecidedBy == uuid.Nil {
		return Approval{}, fmt.Errorf("%w: no deciding user", ErrInvalid)
	}
	now := e.now()
	ctx = db.WithCompany(ctx, companyID)
	var out Approval
	var outcome error
	err := e.Pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		a, err := loadApproval(ctx, tx, approvalID)
		if err != nil {
			return err
		}
		if a.Decision != nil {
			outcome = ErrAlreadyDecided
			out = a
			return nil
		}
		decision, edited, note, by := in.Decision, in.EditedBody, in.Note, &in.DecidedBy
		if !now.Before(a.DeadlineAt) {
			// ⛔ D120 F2: THE LATE DECISION IS NOT RECORDED; EXPIRY IS. Writing
			// 'expired' here (rather than refusing and writing nothing) means
			// the run's next poll reads a terminal decision, whoever gets
			// there first.
			decision, edited, note, by = "expired", nil, "", nil
			outcome = ErrApprovalExpired
		}
		preview := in.EditedPreview
		if decision != "approve" {
			preview = nil
		}
		ok, err := insertDecision(ctx, tx, companyID, approvalID, decision, edited, note, by, in.WebAuthn && decision == "approve", now, preview)
		if err != nil {
			return err
		}
		if !ok {
			outcome = ErrAlreadyDecided
		}
		actor, actorID := intentlog.ActorUser, in.DecidedBy.String()
		if decision == "expired" {
			actor, actorID = intentlog.ActorSystem, "approval-api"
		}
		if ok {
			if _, err := intentlog.Append(ctx, tx, companyID, intentlog.Event{
				RunID: a.RunID, Kind: intentlog.KindApprovalDecided, ActorKind: actor, ActorID: actorID,
				ActionID: a.ActionID, ACLKey: a.ACLKey,
				Payload: map[string]any{"approval_id": a.ID, "decision": decision, "edited": len(edited) > 0,
					"edited_body_hash": hashText(string(edited)), "note_hash": hashText(note), "webauthn": in.WebAuthn},
			}, now); err != nil {
				return err
			}
			// Nudge: the run is due NOW.
			if _, err := tx.Exec(ctx,
				`UPDATE agent_run SET status = 'running', next_poll_at = $2, updated_at = $2
				  WHERE id = $1 AND status IN ('running', 'parked')`, a.RunID, now); err != nil {
				return err
			}
		}
		out, err = loadApproval(ctx, tx, approvalID)
		return err
	})
	if err != nil {
		return Approval{}, err
	}
	return out, outcome
}
