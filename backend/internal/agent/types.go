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
	// Approver is the human whose "approve" a commit rides on (uuid.Nil for
	// an autonomous commit and every read or preview).
	//
	// ⛔ THE CHECKER'S OWN AUTHORITY IS JUDGED ON THE REAL BODY, IN THE ROUTER.
	// The request still authenticates as the run's creator (the voucher is
	// theirs, made on their delegation), but the router ALSO evaluates the
	// approver's level and rules against the same concrete request — the date
	// the body carries, the date already stored, the year-end lock — and
	// refuses if either human could not have made this write themselves.
	// Without it an approver whose cutoff forbids a back-dated voucher could
	// edit the date back and have it posted under the creator's cutoff.
	Approver uuid.UUID
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
	// MaxDebitPaise, on a commit, is the run's delegated ceiling
	// (Delegation.MaxAmountPaise). The dispatcher sums the debits the
	// handler's OWN transaction wrote and rolls it back above the ceiling —
	// the only place the amount of an approver-EDITED body is ever known,
	// since an edited body is not previewed again.
	MaxDebitPaise *int64
	// Params are the concrete path parameters (preview and commit), for a
	// dispatcher that must read the state a write is about to change BEFORE
	// the handler runs — a bank-reconciliation un-tick deletes its row, so
	// what it removed can only be read first.
	Params map[string]string
	// ExpectVersions, on the commit of a voucher ALTER, is the head version
	// the preview (or the approver's re-preview) restated, per logical
	// voucher. ⛔ The commit refuses if the voucher has moved on since: an
	// approved alteration of version 3 must not silently overwrite a version
	// 4 somebody keyed in the meantime (a lost update the ledger itself would
	// happily append).
	ExpectVersions map[uuid.UUID]int
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
//
// Postings / DebitPaise / CreditPaise are what THIS write posts (for an alter:
// the new version's legs). The fields below them are the wave-2 breadth: an
// alter's before-and-after, a stock voucher's movements, a master's audit
// row, an order, a bank date. ⛔ EVERY FIELD IS READ BACK FROM WHAT THE REAL
// HANDLER WROTE in its own transaction (internal/server's readTxEffect) —
// never derived from the request body — so the approver's diff is the books'
// answer, not the model's claim.
type Effect struct {
	Postings    []Posting `json:"postings"`
	DebitPaise  int64     `json:"debit_paise"`
	CreditPaise int64     `json:"credit_paise"`
	Vouchers    int       `json:"vouchers"`

	// Versions is every voucher version the write appended: its op (CREATE,
	// ALTER), and for an alter the version it supersedes.
	Versions []VersionWritten `json:"versions,omitempty"`
	// Replaces is, for an ALTER, the head version the new one supersedes and
	// its legs — the "before" of the diff. The superseded version is never
	// changed (voucher_version is append-only); this is a compensating version.
	Replaces []VersionRef `json:"replaces,omitempty"`
	// Delta is, for an ALTER, the net change per ledger (after − before, Dr
	// positive, paise). A ledger whose net is unchanged is omitted.
	Delta []LedgerDelta `json:"delta,omitempty"`
	// Stock is the stock movements the write recorded (stock vouchers, item
	// invoices).
	Stock []StockMove `json:"stock,omitempty"`
	// Masters is the master_audit rows the write appended: what a master
	// create or alter changed, before and after, in the audit's own words.
	Masters []MasterChange `json:"masters,omitempty"`
	// Orders is the order heads the write created or changed.
	Orders []OrderChange `json:"orders,omitempty"`
	// BankDates is the bank-reconciliation ticks the write set or cleared.
	BankDates []BankDateChange `json:"bank_dates,omitempty"`
}

// Empty reports whether the write produced nothing the preview can show —
// which is a preview that cannot be approved (there is nothing to approve).
func (e *Effect) Empty() bool {
	return e == nil || (len(e.Postings) == 0 && e.Vouchers == 0 && len(e.Stock) == 0 &&
		len(e.Masters) == 0 && len(e.Orders) == 0 && len(e.BankDates) == 0)
}

// VersionWritten is one voucher_version a write appended.
type VersionWritten struct {
	VoucherID uuid.UUID `json:"voucher_id"`
	VersionNo int       `json:"version_no"`
	Op        string    `json:"op"`
	Class     string    `json:"class"`
	VoucherNo string    `json:"voucher_no,omitempty"`
	Date      string    `json:"date,omitempty"`
}

// VersionRef is a superseded head: the "before" of an alteration.
type VersionRef struct {
	VoucherID   uuid.UUID `json:"voucher_id"`
	VersionNo   int       `json:"version_no"`
	Postings    []Posting `json:"postings"`
	DebitPaise  int64     `json:"debit_paise"`
	CreditPaise int64     `json:"credit_paise"`
}

// LedgerDelta is one ledger's net change under an alteration.
type LedgerDelta struct {
	LedgerID    uuid.UUID `json:"ledger_id"`
	Ledger      string    `json:"ledger"`
	BeforePaise int64     `json:"before_paise"`
	AfterPaise  int64     `json:"after_paise"`
	ChangePaise int64     `json:"change_paise"`
}

// StockMove is one stock_movement row a write recorded.
type StockMove struct {
	VoucherID uuid.UUID `json:"voucher_id"`
	LineNo    int       `json:"line_no"`
	Item      string    `json:"item"`
	Godown    string    `json:"godown,omitempty"`
	Direction string    `json:"direction"`
	Qty       string    `json:"qty"`
	Rate      string    `json:"rate"`
	Amount    string    `json:"amount"`
}

// MasterChange is one master_audit row a write appended.
type MasterChange struct {
	Entity   string          `json:"entity"`
	EntityID uuid.UUID       `json:"entity_id"`
	Op       string          `json:"op"`
	Before   json.RawMessage `json:"before,omitempty"`
	After    json.RawMessage `json:"after,omitempty"`
}

// OrderChange is one order head a write created or changed.
type OrderChange struct {
	OrderID   uuid.UUID   `json:"order_id"`
	Direction string      `json:"direction"`
	OrderNo   string      `json:"order_no"`
	Date      string      `json:"date"`
	Closed    bool        `json:"closed"`
	Lines     []OrderLine `json:"lines,omitempty"`
}

// OrderLine is one line of an order a write created.
type OrderLine struct {
	LineNo int    `json:"line_no"`
	Item   string `json:"item"`
	Qty    string `json:"qty"`
	Rate   string `json:"rate"`
}

// BankDateChange is one bank-reconciliation tick set, changed or cleared.
type BankDateChange struct {
	VoucherID uuid.UUID `json:"voucher_id"`
	LedgerID  uuid.UUID `json:"ledger_id"`
	LegNo     int       `json:"leg_no"`
	Before    string    `json:"before,omitempty"`
	After     string    `json:"after,omitempty"`
}

// ObjectKind is what a commit wrote (0095's agent_commit.object_kind).
type ObjectKind string

const (
	ObjectVoucher            ObjectKind = "voucher"
	ObjectMaster             ObjectKind = "master"
	ObjectOrder              ObjectKind = "order"
	ObjectBankReconciliation ObjectKind = "bank_reconciliation"
)

// Result is what a dispatch produced.
type Result struct {
	Status     int             `json:"status"`
	Body       json.RawMessage `json:"body,omitempty"`
	Effect     *Effect         `json:"effect,omitempty"`
	VoucherIDs []uuid.UUID     `json:"voucher_ids,omitempty"`
	// Replayed is true when a commit found its idempotency key already
	// recorded and answered from it without running the handler.
	Replayed bool `json:"replayed,omitempty"`
	// ObjectKind / ObjectIDs are what the commit wrote when it was not (only)
	// vouchers: a master, an order, a bank reconciliation. For a voucher
	// commit ObjectIDs equals VoucherIDs.
	ObjectKind ObjectKind  `json:"object_kind,omitempty"`
	ObjectIDs  []uuid.UUID `json:"object_ids,omitempty"`
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
