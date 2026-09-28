package notify_test

// dispatch_undeliverable_test.go — "DELIVERED TO NOBODY" IS NOT A SEND.
//
// The bug this pins, measured in the live database: 52 company_invite
// notifications all sitting at status='sent', 41 of them with a recipient
// containing no '@' at all. The chain was mechanical — the invite form collects
// no email, so InviteMessage falls back to the JWT subject as the recipient;
// LogSender sees !HasAddress(), logs "NO EMAIL ON FILE ... nothing was sent" and
// returns nil (it must never fail); settle reads nil as success and writes
// 'sent'; the members screen renders 'sent' as "invitation sent". Every part
// behaved as documented and the product told 41 lies.
//
// So the claim under test is about the OUTBOX ROW, not about a Sender: after a
// dispatch pass, an addressless notification must be terminal, must carry
// NoAddressReason, must never have been handed to a transport at all, and must
// not be retried — and an ADDRESSED one beside it must still be delivered, so
// the fix cannot be "nothing gets sent any more".

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/notify"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/migrations"
)

// setup connects-or-skips, mints a company to hang notifications off (the
// outbox is FK'd to company and RLS-scoped on it), and registers its teardown.
func setup(t *testing.T) (*db.Pool, context.Context, uuid.UUID) {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	ctx := context.Background()
	adminConn, err := pgx.Connect(ctx, testdb.AdminDSNValue())
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	if err := migrations.Apply(ctx, adminConn); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("apply migrations: %v", err)
	}
	_ = adminConn.Close(ctx)

	pool, err := db.New(ctx, testdb.AppDSNValue())
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}
	t.Cleanup(pool.Close)

	companyID := uuid.Must(uuid.NewV7())
	cctx := db.WithCompany(ctx, companyID)
	if _, err := identity.NewService(pool).CreateCompany(cctx, identity.Company{
		ID: companyID, Name: "Notify Co " + companyID.String()[:8], FYStartDate: "2025-04-01",
	}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	testdb.Cleanup(t, companyID)
	return pool, cctx, companyID
}

// recordingSender remembers what it was ASKED to deliver, so "was this handed to
// a transport at all?" is observable — the addressless message must not be.
type recordingSender struct {
	mu   sync.Mutex
	seen []string
}

func (r *recordingSender) Send(_ context.Context, m notify.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, m.Recipient)
	return nil
}

func (r *recordingSender) recipients() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.seen...)
}

type outboxRow struct {
	Status    string
	Attempts  int
	LastError string
	SentAt    *time.Time
}

func readRow(t *testing.T, pool *db.Pool, ctx context.Context, id uuid.UUID) outboxRow {
	t.Helper()
	var row outboxRow
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT status, attempts, last_error, sent_at
			   FROM notification_outbox WHERE id = $1`, id).
			Scan(&row.Status, &row.Attempts, &row.LastError, &row.SentAt)
	}); err != nil {
		t.Fatalf("read outbox row %s: %v", id, err)
	}
	return row
}

// enqueue writes one notification through the ordinary path (Enqueue is the only
// way a notification is created, on purpose).
func enqueue(t *testing.T, pool *db.Pool, ctx context.Context, m notify.Message) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		id, err = notify.Enqueue(ctx, tx, m)
		return err
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	return id
}

func TestAnAddresslessNotificationIsNotRecordedAsSent(t *testing.T) {
	pool, ctx, companyID := setup(t)

	// EXACTLY what the admin API produces when the invite form carries no email:
	// InviteMessage falls back to the subject, which is an identifier, not an
	// address.
	noAddr := notify.InviteMessage("Notify Co", "subject-with-no-at-sign", "", "Data Entry")
	noAddr.CompanyID = companyID
	noAddr.RefID = uuid.Must(uuid.NewV7())
	noAddrID := enqueue(t, pool, ctx, noAddr)

	// A deliverable one beside it, so the fix cannot be "stop sending".
	addressed := notify.InviteMessage("Notify Co", "reachable-subject", "reachable@firm.test", "Data Entry")
	addressed.CompanyID = companyID
	addressed.RefID = uuid.Must(uuid.NewV7())
	addressedID := enqueue(t, pool, ctx, addressed)

	rec := &recordingSender{}
	if _, err := notify.NewDispatcher(pool, rec).DispatchOnce(ctx); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	got := readRow(t, pool, ctx, noAddrID)
	if got.Status == "sent" {
		t.Errorf("an addressless notification was recorded as status=%q — this is the "+
			"defect: 41 rows in the live database claim 'invitation sent' for a "+
			"recipient with no '@'", got.Status)
	}
	if got.Status != "failed" {
		t.Errorf("addressless notification status = %q, want failed (terminal: "+
			"'pending' would be retried and 'sending' would read as under way)", got.Status)
	}
	if got.LastError != notify.NoAddressReason {
		t.Errorf("addressless last_error = %q, want %q — the screen renders this "+
			"reason in its own loud words", got.LastError, notify.NoAddressReason)
	}
	if got.SentAt != nil {
		t.Errorf("addressless sent_at = %v, want NULL — nothing was sent", got.SentAt)
	}

	// It was never offered to a transport: no address is not a transport question,
	// and every honest Sender would either succeed vacuously or invent an error.
	for _, r := range rec.recipients() {
		if r == "subject-with-no-at-sign" {
			t.Errorf("the addressless message was handed to a Sender (%q) — the "+
				"dispatcher must settle it itself, for every Sender", r)
		}
	}

	// ...and the deliverable one beside it still went.
	if ok := readRow(t, pool, ctx, addressedID); ok.Status != "sent" {
		t.Errorf("addressed notification status = %q, want sent — the fix must not "+
			"stop real delivery", ok.Status)
	}
}

// TestAnAddresslessNotificationIsNotRetried: terminal on the FIRST pass.
// Attempts do not conjure an address, and a row that stayed 'pending' would be
// re-claimed forever while the screen said "invitation sending…".
func TestAnAddresslessNotificationIsNotRetried(t *testing.T) {
	pool, ctx, companyID := setup(t)

	m := notify.InviteMessage("Notify Co", "still-no-at-sign", "", "Data Entry")
	m.CompanyID = companyID
	m.RefID = uuid.Must(uuid.NewV7())
	id := enqueue(t, pool, ctx, m)

	d := notify.NewDispatcher(pool, &recordingSender{})
	if _, err := d.DispatchOnce(ctx); err != nil {
		t.Fatalf("first dispatch: %v", err)
	}
	first := readRow(t, pool, ctx, id)
	if _, err := d.DispatchOnce(ctx); err != nil {
		t.Fatalf("second dispatch: %v", err)
	}
	second := readRow(t, pool, ctx, id)

	if first.Attempts != 1 || second.Attempts != first.Attempts {
		t.Errorf("attempts went %d -> %d, want 1 -> 1: an addressless notification "+
			"must not be claimed a second time", first.Attempts, second.Attempts)
	}
	if second.Status != "failed" {
		t.Errorf("status after a second pass = %q, want failed", second.Status)
	}
}

// TestHasAddressIsTheDefinitionOfDeliverable pins the predicate the dispatcher
// now decides on. "A subject is an identifier, not an address."
func TestHasAddressIsTheDefinitionOfDeliverable(t *testing.T) {
	for _, tc := range []struct {
		recipient string
		want      bool
	}{
		{"someone@firm.test", true},
		{"a@b", true},
		{"browser-dev-owner", false}, // a JWT subject: the 41-row case
		{"", false},
		{"@firm.test", false}, // no local part
		{"someone@", false},   // no domain
	} {
		if got := (notify.Message{Recipient: tc.recipient}).HasAddress(); got != tc.want {
			t.Errorf("HasAddress(%q) = %v, want %v", tc.recipient, got, tc.want)
		}
	}
}
