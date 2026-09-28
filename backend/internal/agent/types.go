// Package agent is the W2 agent loop (PLAN-AGENTIC §3 W2, D121): a
// plan-and-execute loop over internal/llm whose every write goes through the
// same HTTP handlers the UI uses, gated by internal/policy, recorded in
// internal/intentlog, and run as a durable workflow on internal/durable so an
// approval can wait for days across any number of restarts.
//
// # THE SHAPE OF A RUN
//
//	start                         context sources recorded, run_started intent
//	decide:k   (model)            one structured decision: a tool and its args
//	tool:k     (read / preview)   actions.search, actions.preview, query_books, ...
//	gate:k     (policy)           for actions.commit: autonomy, provenance, rights;
//	                              an approval row if a human is needed
//	await:k    (parks)            reads agent_decision; durable.ErrParked if none;
//	                              writes an 'expired' decision past the deadline
//	exec:k     (commit)           the real handler, in process, idempotency-keyed
//	finish                        completed | failed | expired
//
// Each is a durable step (durable.RunDynamic). The sequence is a PURE function
// of what is recorded (loop.go's next), so a resumed run — after a restart,
// three days later, on a different cored instance — derives the same next step
// and nothing recorded runs twice. The model is asked once per decide:k, ever.
//
// # WHAT THE AGENT CAN NEVER DO, AND WHERE THAT IS ENFORCED
//
//   - Write through a side door. It has no ledger service, no masters service
//     and no SQL for the books; its only write path is Dispatcher, which
//     internal/server implements by running the real router in process.
//   - Commit what it did not preview. actions.commit takes a REFERENCE to a
//     successful preview step, and the committed body is that step's recorded
//     body (or the approver's edit of it), never a fresh model output
//     (PLAN-AGENTIC §2.1: "replayed from an approved plan id, never
//     regenerated").
//   - Compute money. Amounts come from the postings the real handler derived
//     during the preview (Effect), in paise, by Go.
//   - Decide its own approvals, change the ACL, delete, lift a period lock:
//     internal/policy's structural rules, applied again in the router.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/actions"
	"github.com/khata/cored/internal/policy"
)

// Status is an agent_run status (0094's CHECK).
type Status string

const (
	StatusRunning   Status = "running"
	StatusParked    Status = "parked"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusExpired   Status = "expired"
	StatusCancelled Status = "cancelled"
)

// Finished reports whether a run will never be polled again.
func (s Status) Finished() bool {
	return s == StatusCompleted || s == StatusFailed || s == StatusExpired || s == StatusCancelled
}

// Channel is where a run was started (0094's CHECK).
type Channel string

const (
	ChannelPanel    Channel = "panel"
	ChannelAPI      Channel = "api"
	ChannelWhatsApp Channel = "whatsapp"
	ChannelVoice    Channel = "voice"
	ChannelMCP      Channel = "mcp"
	ChannelEmail    Channel = "email"
)

// Input is one piece of UNTRUSTED material a run is handed.
type Input struct {
	Label  string        `json:"label"`
	Source policy.Source `json:"provenance"`
	Text   string        `json:"text"`
}

// Run is an agent_run row.
type Run struct {
	ID          uuid.UUID         `json:"id"`
	CompanyID   uuid.UUID         `json:"-"`
	CreatedBy   uuid.UUID         `json:"created_by"`
	Subject     string            `json:"-"`
	AgentID     string            `json:"agent_id"`
	Channel     Channel           `json:"channel"`
	Instruction string            `json:"instruction"`
	Inputs      []Input           `json:"inputs"`
	Delegation  policy.Delegation `json:"delegation"`
	Status      Status            `json:"status"`
	Outcome     json.RawMessage   `json:"outcome,omitempty"`
	LastError   string            `json:"last_error,omitempty"`
	Attempts    int               `json:"attempts"`
	NextPollAt  *time.Time        `json:"next_poll_at,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	FinishedAt  *time.Time        `json:"finished_at,omitempty"`
}

// Principal is the policy principal this run acts as.
func (r Run) Principal() policy.Principal {
	return policy.Principal{AgentID: r.AgentID, OnBehalfOf: r.CreatedBy, CompanyID: r.CompanyID,
		RunID: r.ID, Delegation: r.Delegation}
}

// ---------------------------------------------------------------------------
// The dispatcher seam — implemented by internal/server.
// ---------------------------------------------------------------------------

// Mode is what a dispatch does at the end of the handler's transactions.
type Mode string

const (
	// ModeRead runs a read (display / print) as the principal.
	ModeRead Mode = "read"
	// ModePreview runs the real write handler with every transaction ROLLED
	// BACK (platform/db TxHook.DryRun), capturing the ledger effect first.
	ModePreview Mode = "preview"
	// ModeCommit runs the real write handler for real, with the idempotency
	// record inserted in the SAME transaction as the write.
	ModeCommit Mode = "commit"
)

// Caller is who a dispatch authenticates as: the run's human, carried by the
// agent, under the run's delegation.
type Caller struct {
	Principal policy.Principal
	// Subject is the human's JWT subject; the router authenticates the
	// in-process request as this subject.
	Subject string
	// IssuedAt stands in for the token's iat: the run's start. A user locked
	// out AFTER starting a run has the run's requests revoked by the router's
	// ordinary session-revocation check, as their own token would be.
	IssuedAt time.Time
	// WebAuthn is true only for a commit whose approving human stepped up.
	WebAuthn bool
}

// Request is one in-process call.
type Request struct {
	Mode Mode
	// Spec is the ActionSpec this request claims to be. ⛔ The router
	// re-derives (action, resource) from the concrete request and refuses a
	// mismatch, so a path cannot smuggle a different door under this spec.
	Spec actions.ActionSpec
	// Path is the concrete company-scoped path, "/v1/companies/<id>/...".
	Path  string
	Query string
	Body  json.RawMessage
	// IdempotencyKey and ApprovalID are ModeCommit only.
	IdempotencyKey string
	ApprovalID     uuid.UUID
	RunID          uuid.UUID
}

// Posting is one line of a ledger effect, read back from the handler's own
// transaction before the preview rolled it back.
type Posting struct {
	LedgerID    uuid.UUID `json:"ledger_id"`
	Ledger      string    `json:"ledger"`
	DrCr        string    `json:"dr_cr"`
	Amount      string    `json:"amount"`
	AmountPaise int64     `json:"amount_paise"`
}

// Effect is the ledger-effect diff of a write: what the real validators and
// derivations would post. Totals are paise, computed by Go.
type Effect struct {
	Postings    []Posting `json:"postings"`
	DebitPaise  int64     `json:"debit_paise"`
	CreditPaise int64     `json:"credit_paise"`
	Vouchers    int       `json:"vouchers"`
}

// Result is what a dispatch produced.
type Result struct {
	Status     int             `json:"status"`
	Body       json.RawMessage `json:"body,omitempty"`
	Effect     *Effect         `json:"effect,omitempty"`
	VoucherIDs []uuid.UUID     `json:"voucher_ids,omitempty"`
	// Replayed is true when a commit found its idempotency key already
	// recorded and answered from it without running the handler.
	Replayed bool `json:"replayed,omitempty"`
}

// OK reports a 2xx.
func (r Result) OK() bool { return r.Status >= 200 && r.Status < 300 }

// Dispatcher runs one request through the real router, in process.
type Dispatcher interface {
	Dispatch(ctx context.Context, c Caller, req Request) (Result, error)
}

// Errors the HTTP layer maps.
var (
	ErrNotFound         = errors.New("agent: not found")
	ErrAlreadyDecided   = errors.New("agent: approval already decided")
	ErrApprovalExpired  = errors.New("agent: approval window has closed")
	ErrInvalid          = errors.New("agent: invalid request")
	ErrNoModel          = errors.New("agent: no model configured")
	ErrDecisionRejected = errors.New("agent: decision refused")
)
