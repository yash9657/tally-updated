package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/durable"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/policy"
)

// runStore is the durable.StepStore for agent runs: agent_run_step under FORCE
// RLS (0094), reached through company-scoped transactions, with the run's
// advisory lock from durable.AdvisoryLocker.
//
// ⛔ WHY NOT statutory.PGStore: statutory_step carries no RLS by design, and an
// agent step records plans, previews and party names — tenant data. The engine
// is shared (durable); the table is not.
type runStore struct {
	pool   *db.Pool
	locker durable.AdvisoryLocker
}

var _ durable.StepStore = (*runStore)(nil)

func runIDFrom(workflowID string) (uuid.UUID, error) {
	id, err := uuid.Parse(workflowID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("agent: workflow id %q is not a run id", workflowID)
	}
	return id, nil
}

func (s *runStore) Load(ctx context.Context, workflowID string) ([]durable.StepResult, error) {
	runID, err := runIDFrom(workflowID)
	if err != nil {
		return nil, err
	}
	var out []durable.StepResult
	err = s.pool.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT step_name, output FROM agent_run_step
			  WHERE run_id = $1 ORDER BY seq`, runID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r durable.StepResult
			if err := rows.Scan(&r.Name, &r.Output); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("agent: load steps: %w", err)
	}
	return out, nil
}

func (s *runStore) Append(ctx context.Context, workflowID string, r durable.StepResult) error {
	runID, err := runIDFrom(workflowID)
	if err != nil {
		return err
	}
	if len(r.Output) == 0 {
		return fmt.Errorf("agent: step %q has empty output", r.Name)
	}
	companyID, _ := db.CompanyFromContext(ctx)
	return s.pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO agent_run_step (company_id, run_id, step_name, output)
			 VALUES ($1, $2, $3, $4)
			 ON CONFLICT (company_id, run_id, step_name) DO NOTHING`,
			companyID, runID, r.Name, r.Output)
		return err
	})
}

func (s *runStore) TryLock(ctx context.Context, workflowID string) (func(), bool, error) {
	return s.locker.TryLock(ctx, workflowID)
}

// ---------------------------------------------------------------------------
// agent_run
// ---------------------------------------------------------------------------

const runColumns = `id, created_by, created_by_subject, agent_id, channel, instruction, inputs,
	delegation, status, outcome, last_error, attempts, next_poll_at, created_at, updated_at, finished_at`

func scanRun(row pgx.Row, companyID uuid.UUID) (Run, error) {
	var r Run
	var inputs, delegation []byte
	var channel, status string
	err := row.Scan(&r.ID, &r.CreatedBy, &r.Subject, &r.AgentID, &channel, &r.Instruction, &inputs,
		&delegation, &status, &r.Outcome, &r.LastError, &r.Attempts, &r.NextPollAt, &r.CreatedAt,
		&r.UpdatedAt, &r.FinishedAt)
	if err != nil {
		return Run{}, err
	}
	r.CompanyID = companyID
	r.Channel, r.Status = Channel(channel), Status(status)
	if err := json.Unmarshal(inputs, &r.Inputs); err != nil {
		return Run{}, fmt.Errorf("agent: decode inputs: %w", err)
	}
	if err := json.Unmarshal(delegation, &r.Delegation); err != nil {
		return Run{}, fmt.Errorf("agent: decode delegation: %w", err)
	}
	return r, nil
}

func loadRun(ctx context.Context, tx pgx.Tx, companyID, runID uuid.UUID) (Run, error) {
	r, err := scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM agent_run WHERE id = $1`, runID), companyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	return r, err
}

func listRuns(ctx context.Context, tx pgx.Tx, companyID uuid.UUID, limit int) ([]Run, error) {
	rows, err := tx.Query(ctx, `SELECT `+runColumns+` FROM agent_run ORDER BY created_at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows, companyID)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// setRunState writes the poller-visible state. A finished status clears
// next_poll_at (0094's CHECK requires it); an unfinished one requires one.
func setRunState(ctx context.Context, tx pgx.Tx, runID uuid.UUID, st Status, outcome json.RawMessage,
	lastErr string, attempts int, nextPoll *time.Time, now time.Time) error {
	var finished *time.Time
	if st.Finished() {
		nextPoll = nil
		finished = &now
	}
	_, err := tx.Exec(ctx,
		`UPDATE agent_run
		    SET status = $2, outcome = COALESCE($3, outcome), last_error = $4, attempts = $5,
		        next_poll_at = $6, updated_at = $7, finished_at = COALESCE(finished_at, $8)
		  WHERE id = $1 AND status IN ('running', 'parked')`,
		runID, string(st), nullJSON(outcome), lastErr, attempts, nextPoll, now, finished)
	return err
}

func nullJSON(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	return []byte(b)
}

// ---------------------------------------------------------------------------
// approvals and decisions
// ---------------------------------------------------------------------------

// Approval is an agent_approval row joined to its decision, if any.
type Approval struct {
	ID          uuid.UUID         `json:"id"`
	RunID       uuid.UUID         `json:"run_id"`
	StepKey     string            `json:"step_key"`
	ActionID    string            `json:"action_id"`
	ACLKey      string            `json:"acl_key"`
	Risk        string            `json:"risk"`
	Args        json.RawMessage   `json:"args"`
	Preview     json.RawMessage   `json:"preview"`
	AmountPaise *int64            `json:"amount_paise,omitempty"`
	Reasons     []string          `json:"reasons"`
	Provenance  policy.Provenance `json:"provenance"`
	OnBehalfOf  uuid.UUID         `json:"on_behalf_of"`
	RequestedAt time.Time         `json:"requested_at"`
	DeadlineAt  time.Time         `json:"deadline_at"`
	// Instruction is the run's instruction, joined in so the inbox can show
	// what was asked without a second request.
	Instruction string    `json:"instruction"`
	Decision    *Decision `json:"decision,omitempty"`
	// State is derived: pending | approved | rejected | ask_back | expired.
	State string `json:"state"`
}

// Decision is an agent_decision row.
type Decision struct {
	ID              uuid.UUID       `json:"id"`
	Decision        string          `json:"decision"`
	EditedArgs      json.RawMessage `json:"edited_args,omitempty"`
	Note            string          `json:"note,omitempty"`
	DecidedBy       *uuid.UUID      `json:"decided_by,omitempty"`
	DecidedWebAuthn bool            `json:"decided_webauthn"`
	DecidedAt       time.Time       `json:"decided_at"`
}

const approvalSelect = `
	SELECT a.id, a.run_id, a.step_key, a.action_id, a.acl_key, a.risk, a.args, a.preview,
	       a.amount_paise, a.reasons, a.provenance, a.on_behalf_of, a.requested_at, a.deadline_at,
	       r.instruction,
	       d.id, d.decision, d.edited_args, d.note, d.decided_by, d.decided_webauthn, d.decided_at
	  FROM agent_approval a
	  JOIN agent_run r ON r.company_id = a.company_id AND r.id = a.run_id
	  LEFT JOIN agent_decision d ON d.company_id = a.company_id AND d.approval_id = a.id`

func scanApproval(row pgx.Row) (Approval, error) {
	var a Approval
	var reasons, prov []byte
	var dID *uuid.UUID
	var dDecision, dNote *string
	var dEdited []byte
	var dBy *uuid.UUID
	var dWebAuthn *bool
	var dAt *time.Time
	if err := row.Scan(&a.ID, &a.RunID, &a.StepKey, &a.ActionID, &a.ACLKey, &a.Risk, &a.Args, &a.Preview,
		&a.AmountPaise, &reasons, &prov, &a.OnBehalfOf, &a.RequestedAt, &a.DeadlineAt, &a.Instruction,
		&dID, &dDecision, &dEdited, &dNote, &dBy, &dWebAuthn, &dAt); err != nil {
		return Approval{}, err
	}
	if err := json.Unmarshal(reasons, &a.Reasons); err != nil {
		return Approval{}, err
	}
	if err := json.Unmarshal(prov, &a.Provenance); err != nil {
		return Approval{}, err
	}
	a.State = "pending"
	if dID != nil {
		a.Decision = &Decision{ID: *dID, Decision: *dDecision, EditedArgs: dEdited, Note: *dNote,
			DecidedBy: dBy, DecidedWebAuthn: *dWebAuthn, DecidedAt: *dAt}
		switch *dDecision {
		case "approve":
			a.State = "approved"
		case "reject":
			a.State = "rejected"
		default:
			a.State = *dDecision
		}
	}
	return a, nil
}

func loadApproval(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Approval, error) {
	a, err := scanApproval(tx.QueryRow(ctx, approvalSelect+` WHERE a.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Approval{}, ErrNotFound
	}
	return a, err
}

func listApprovals(ctx context.Context, tx pgx.Tx, where string, args ...any) ([]Approval, error) {
	rows, err := tx.Query(ctx, approvalSelect+` WHERE `+where+` ORDER BY a.requested_at DESC, a.id DESC LIMIT 200`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Approval
	for rows.Next() {
		a, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// insertDecision writes the ONE decision an approval may have. ok=false means a
// decision already existed (0094's UNIQUE), which is how a late approve after
// expiry is refused.
func insertDecision(ctx context.Context, tx pgx.Tx, companyID, approvalID uuid.UUID, decision string,
	edited json.RawMessage, note string, by *uuid.UUID, webauthn bool, at time.Time) (bool, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return false, err
	}
	tag, err := tx.Exec(ctx,
		`INSERT INTO agent_decision (id, company_id, approval_id, decision, edited_args, note,
		                             decided_by, decided_webauthn, decided_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 ON CONFLICT ON CONSTRAINT agent_decision_one_per_approval DO NOTHING`,
		id, companyID, approvalID, decision, nullJSON(edited), note, by, webauthn, at)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// lookupCommit reads a recorded commit by its idempotency key.
func lookupCommit(ctx context.Context, tx pgx.Tx, key string) ([]uuid.UUID, bool, error) {
	var ids []uuid.UUID
	err := tx.QueryRow(ctx, `SELECT voucher_ids FROM agent_commit WHERE idempotency_key = $1`, key).Scan(&ids)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	return ids, err == nil, err
}

// LookupCommit is lookupCommit for the dispatcher, which runs it under the
// caller's company scope before dispatching a commit.
func LookupCommit(ctx context.Context, tx pgx.Tx, key string) ([]uuid.UUID, bool, error) {
	return lookupCommit(ctx, tx, key)
}
