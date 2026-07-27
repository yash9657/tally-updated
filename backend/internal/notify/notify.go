// Package notify is the transactional OUTBOX for outbound notifications.
//
// WHY AN OUTBOX. Sending inside the request that creates an invite would couple
// a transactional write to a network call, and both failure orders are wrong:
// send-then-fail-to-commit tells somebody about access they were not granted,
// and commit-then-fail-to-send grants access nobody is told about — which is
// exactly the behaviour this replaces (an invite was a database row that
// notified nobody).
//
// So Enqueue runs INSIDE the caller's transaction: roll back the invite and the
// notification rolls back with it. A dispatcher delivers afterwards, at least
// once, and the status transition makes a re-delivery a no-op.
//
// REAL SMTP IS CONFIGURATION, NOT CODE. Dispatcher takes a Sender; the default
// writes to the log and says plainly that nothing left the building. Nothing in
// this package claims a message was received.
package notify

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/khata/cored/internal/platform/db"
)

// Kind classifies a notification. The dispatcher renders by kind.
type Kind string

// KindCompanyInvite is "you have been invited to open a company's books".
const KindCompanyInvite Kind = "company_invite"

// MaxAttempts is the point at which a notification is marked `failed` and stops
// being retried. It is deliberately small: a notification that has failed five
// times is not going to succeed on the sixth, and an outbox that retries
// forever hides a broken transport behind a queue that never drains.
const MaxAttempts = 5

// Message is one notification, as stored and as handed to a Sender.
type Message struct {
	ID        uuid.UUID
	CompanyID uuid.UUID
	Kind      Kind
	// RefID is the domain row this is about (an invite id).
	RefID uuid.UUID
	// Recipient is an email address when one is known, else the JWT subject.
	// The admin API does not require an email, and inventing one would be worse
	// than recording that there is no address to reach this person at.
	Recipient   string
	SubjectLine string
	Body        string
	Attempts    int
	LastError   string
	Status      string
	// claimedAt is the lease token this message was handed out under. settle
	// writes only if the row still carries it, so a dispatcher that stalled
	// past its lease — and whose row was reclaimed and delivered by someone
	// else — cannot write its own stale outcome over the newer one.
	claimedAt time.Time
}

// HasAddress reports whether Recipient looks like something a transport could
// actually deliver to. A subject is an identifier, not an address.
func (m Message) HasAddress() bool {
	at := -1
	for i, r := range m.Recipient {
		if r == '@' {
			at = i
			break
		}
	}
	return at > 0 && at < len(m.Recipient)-1
}

// Sender delivers one message. Implementations must be idempotent-tolerant: the
// dispatcher guarantees at-least-once, never exactly-once.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// LogSender is the default transport: it writes the notification to the log and
// nothing else. It is not a stand-in for a mail server that is "coming soon" —
// it is the honest statement that this deployment does not send mail, and it
// makes the outbox observable in development without pretending otherwise.
type LogSender struct{}

// Send logs the message. It NEVER fails: a logger that could fail would turn a
// development convenience into a source of spurious dead letters.
func (LogSender) Send(_ context.Context, m Message) error {
	if !m.HasAddress() {
		log.Printf("notify: [%s] NO EMAIL ON FILE for subject %q (company %s) — "+
			"nothing was sent; tell them out of band: %s",
			m.Kind, m.Recipient, m.CompanyID, m.SubjectLine)
		return nil
	}
	log.Printf("notify: [%s] to %s (company %s): %s | %s",
		m.Kind, m.Recipient, m.CompanyID, m.SubjectLine, m.Body)
	return nil
}

// Enqueue writes one notification INSIDE the caller's transaction. It is the
// only way a notification is created, precisely so it cannot exist without the
// domain row it describes (or vice versa).
func Enqueue(ctx context.Context, tx pgx.Tx, m Message) (uuid.UUID, error) {
	id := m.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO notification_outbox
		     (id, company_id, kind, ref_id, recipient, subject_line, body)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id, m.CompanyID, string(m.Kind), nullUUID(m.RefID),
		m.Recipient, m.SubjectLine, m.Body); err != nil {
		return uuid.Nil, fmt.Errorf("notify: enqueue: %w", err)
	}
	return id, nil
}

func nullUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

// InviteMessage renders the body for a company invite. Deliberately plain text
// and deliberately explicit about what an invite IS: the recipient does not have
// access yet, and nothing happens until they sign in — the same distinction the
// Users & Roles screen refuses to hide.
func InviteMessage(companyName, subject, email, levelName string) Message {
	recipient := email
	if recipient == "" {
		recipient = subject
	}
	return Message{
		Kind:        KindCompanyInvite,
		Recipient:   recipient,
		SubjectLine: fmt.Sprintf("You have been invited to %s", companyName),
		Body: fmt.Sprintf(
			"You have been invited to open the books of %q as %q.\n\n"+
				"Sign in with the user name %q. Your access is created the first "+
				"time you sign in — until then the invitation is pending and grants "+
				"nothing.\n\n"+
				"If you were not expecting this, ignore it: an unclaimed invitation "+
				"gives no access and can be revoked by the administrator.",
			companyName, levelName, subject),
	}
}

// --------------------------------------------------------------------------
// Dispatcher
// --------------------------------------------------------------------------

// Runner is the database capability the dispatcher needs.
//
// Raw is here for exactly ONE query: "which companies have pending work". The
// pool's RunInTx requires a tenant scope (it sets the company GUC and refuses
// without one), which a dispatcher cannot have before it knows which tenant to
// adopt. That single call goes through the SECURITY DEFINER function
// notification_pending_companies(), which returns company ids and nothing else —
// see migration 0023. Every other read and write the dispatcher performs goes
// through RunInTx under a real company scope, subject to the ordinary policy.
type Runner interface {
	RunInTx(ctx context.Context, fn db.TxFunc) error
	Raw() *pgxpool.Pool
}

// Dispatcher delivers pending notifications. It is deliberately simple: find
// the companies with work, adopt each one's scope in turn, and claim rows with
// FOR UPDATE SKIP LOCKED so two dispatchers never send the same message twice.
type Dispatcher struct {
	runner Runner
	sender Sender
	// batch caps rows claimed per company per pass, so one busy tenant cannot
	// starve the others.
	batch int
	// lease is how long a claim is honoured before another dispatcher may take
	// the row back. It bounds how long a notification is stranded when a
	// dispatcher dies mid-send; it is NOT how retries are paced (a send that
	// fails and is recorded returns the row to 'pending' at once).
	lease time.Duration
}

// NewDispatcher builds a Dispatcher. A nil sender means LogSender.
func NewDispatcher(runner Runner, sender Sender) *Dispatcher {
	if sender == nil {
		sender = LogSender{}
	}
	return &Dispatcher{runner: runner, sender: sender, batch: 50, lease: 5 * time.Minute}
}

// DispatchOnce delivers one pass and returns how many messages were SENT.
//
// It is exported (rather than being only a ticker's private step) because a
// test that had to sleep for a background loop would be slow and flaky, and
// because an operator wants a way to drain the outbox on demand.
func (d *Dispatcher) DispatchOnce(ctx context.Context) (int, error) {
	companies, err := d.pendingCompanies(ctx)
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, companyID := range companies {
		n, err := d.dispatchCompany(ctx, companyID)
		sent += n
		if err != nil {
			// One tenant's failure must not stop the others.
			log.Printf("notify: dispatch company %s: %v", companyID, err)
		}
	}
	return sent, nil
}

// pendingCompanies asks WHICH tenants have work. This is the dispatcher's only
// cross-tenant read, and it goes through a SECURITY DEFINER function that
// returns company ids and nothing else — see migration 0023 for why that is
// preferable to giving the app role an RLS bypass.
func (d *Dispatcher) pendingCompanies(ctx context.Context) ([]uuid.UUID, error) {
	var out []uuid.UUID
	rows, err := d.runner.Raw().Query(ctx,
		`SELECT company_id FROM notification_pending_companies($1)`, 100)
	if err != nil {
		return nil, fmt.Errorf("notify: pending companies: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("notify: scan pending company: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("notify: pending companies: %w", err)
	}
	return out, nil
}

func (d *Dispatcher) dispatchCompany(ctx context.Context, companyID uuid.UUID) (int, error) {
	ctx = db.WithCompany(ctx, companyID)

	var claimed []Message
	// CLAIM: take a LEASE on the rows, in one transaction.
	//
	// SKIP LOCKED alone is NOT enough, and the difference is a real double-send.
	// A row lock lasts only as long as the claiming transaction, and the send
	// necessarily happens after that transaction commits — so a row left at
	// status 'pending' is immediately claimable again by the next dispatcher,
	// which then sends the same message. (Measured: four concurrent dispatchers
	// handed seven of eight messages to the sender twice.)
	//
	// SKIP LOCKED is a THROUGHPUT choice, not the correctness mechanism: with it
	// removed the concurrency test still passes, because what actually
	// serialises the send is the status transition below plus READ COMMITTED
	// re-evaluation. Without SKIP LOCKED the losers block instead of moving on.
	//
	// So the claim also moves the row to 'sending'. A dispatcher that dies
	// mid-send leaves a 'sending' row, which becomes claimable again once its
	// LEASE expires — that is what keeps a crash from stranding a notification
	// forever, and why attempts is bumped here rather than after the send.
	if err := d.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`WITH claimed AS (
			     SELECT id FROM notification_outbox
			      WHERE company_id = $1
			        AND (status = 'pending'
			             OR (status = 'sending' AND claimed_at < now() - $3::interval))
			      ORDER BY created_at
			      FOR UPDATE SKIP LOCKED
			      LIMIT $2
			 )
			 UPDATE notification_outbox n
			    SET attempts = n.attempts + 1,
			        status = 'sending',
			        claimed_at = now()
			   FROM claimed c
			  WHERE n.id = c.id
			 RETURNING n.id, n.company_id, n.kind, COALESCE(n.ref_id, '00000000-0000-0000-0000-000000000000'::uuid),
			           n.recipient, n.subject_line, n.body, n.attempts, n.claimed_at`,
			companyID, d.batch, d.lease.String())
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var m Message
			var kind string
			if err := rows.Scan(&m.ID, &m.CompanyID, &kind, &m.RefID,
				&m.Recipient, &m.SubjectLine, &m.Body, &m.Attempts, &m.claimedAt); err != nil {
				return err
			}
			m.Kind = Kind(kind)
			claimed = append(claimed, m)
		}
		return rows.Err()
	}); err != nil {
		return 0, fmt.Errorf("notify: claim: %w", err)
	}

	sent := 0
	for _, m := range claimed {
		sendErr := d.sender.Send(ctx, m)
		if err := d.settle(ctx, m, sendErr); err != nil {
			return sent, err
		}
		if sendErr == nil {
			sent++
		}
	}
	return sent, nil
}

// settle records the outcome of one send: sent, still pending (retry), or
// failed for good once MaxAttempts is reached.
//
// EVERY write is guarded by the lease this message was claimed under
// (`status = 'sending' AND claimed_at = $N`), because a settle is not
// necessarily still the authority on the row. A dispatcher that stalls past the
// 5-minute lease has its row reclaimed and delivered by another; when it
// finally comes back to record its own failure, an unguarded UPDATE would push
// an already-'sent' row back to 'pending' and the invitee would be mailed a
// third time — the very duplicate the lease exists to prevent. Losing that race
// means the outcome is stale and dropping it is correct.
func (d *Dispatcher) settle(ctx context.Context, m Message, sendErr error) error {
	return d.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if sendErr == nil {
			_, err := tx.Exec(ctx,
				`UPDATE notification_outbox
				    SET status = 'sent', sent_at = now(), last_error = ''
				  WHERE id = $1 AND status = 'sending' AND claimed_at = $2`,
				m.ID, m.claimedAt)
			return err
		}
		status := "pending"
		if m.Attempts >= MaxAttempts {
			// Dead-letter rather than retry forever: an outbox that never drains
			// hides a broken transport behind a queue.
			status = "failed"
		}
		// Back to 'pending' releases the lease immediately, so a retry does not
		// have to wait it out — the lease exists for CRASHES, not for failures
		// the dispatcher lived to record.
		_, err := tx.Exec(ctx,
			`UPDATE notification_outbox SET status = $2, last_error = $3
			  WHERE id = $1 AND status = 'sending' AND claimed_at = $4`,
			m.ID, status, truncate(sendErr.Error(), 500), m.claimedAt)
		return err
	})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// Run delivers on a ticker until ctx is cancelled. Errors are logged, never
// fatal: a broken transport must not take the server down.
func (d *Dispatcher) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := d.DispatchOnce(ctx); err != nil {
				log.Printf("notify: dispatch pass: %v", err)
			}
		}
	}
}

// --------------------------------------------------------------------------
// Read-back (for the admin surface)
// --------------------------------------------------------------------------

// Status is the delivery state of the newest notification for a domain row.
type Status struct {
	Status    string
	Attempts  int
	LastError string
	SentAt    *time.Time
}

// StatusesByRef returns the newest notification status per ref_id for a company.
// Used by the members list so an administrator can see whether an invitee was
// actually told — the distinction between "invited" and "notified" is exactly
// the sort of thing this product refuses to blur.
func StatusesByRef(ctx context.Context, tx pgx.Tx, companyID uuid.UUID) (map[uuid.UUID]Status, error) {
	rows, err := tx.Query(ctx,
		`SELECT DISTINCT ON (ref_id)
		        ref_id, status, attempts, last_error, sent_at
		   FROM notification_outbox
		  WHERE company_id = $1 AND ref_id IS NOT NULL
		  ORDER BY ref_id, created_at DESC`, companyID)
	if err != nil {
		return nil, fmt.Errorf("notify: statuses: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]Status{}
	for rows.Next() {
		var (
			ref uuid.UUID
			st  Status
		)
		if err := rows.Scan(&ref, &st.Status, &st.Attempts, &st.LastError, &st.SentAt); err != nil {
			return nil, fmt.Errorf("notify: scan status: %w", err)
		}
		out[ref] = st
	}
	return out, rows.Err()
}
