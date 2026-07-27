package server_test

// notify_outbox_test.go — INVITE DELIVERY (item C), against live Postgres.
//
// The outbox makes four claims, and each is tested for the reason it exists:
//
//   1. ATOMICITY — an invite and its notification are created together or not
//      at all. This is the whole reason the row is enqueued inside the invite's
//      transaction rather than sent from the handler: send-then-fail-to-commit
//      tells somebody about access they were not granted, and
//      commit-then-fail-to-send grants access nobody is told about (which was
//      the behaviour before this).
//   2. EXACTLY-ONCE-ISH DELIVERY under concurrency — two dispatchers running at
//      the same time must not both send the same message. FOR UPDATE SKIP
//      LOCKED is what makes that true, and a test that only ran one dispatcher
//      would never notice its absence.
//   3. DEAD-LETTERING — a transport that always fails must stop being retried,
//      or a broken sender hides behind a queue that never drains.
//   4. TENANT ISOLATION — one company's dispatcher pass must not send, or even
//      see, another's notifications.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/notify"
	"github.com/khata/cored/internal/server"
)

// invite creates an invite at the seeded Data Entry level and returns its id.
func invite(t *testing.T, srv *server.Server, companyID, ownerTok, subject, email string) string {
	t.Helper()
	body := map[string]any{"subject": subject, "security_level_name": "Data Entry"}
	if email != "" {
		body["email"] = email
	}
	rec, resp := doRaw(t, srv, http.MethodPost,
		"/v1/companies/"+companyID+"/members/invites", ownerTok, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite %s = %d (%s)", subject, rec.Code, rec.Body.String())
	}
	id, _ := resp["invite_id"].(string)
	return id
}

// inviteRow finds one invite in the members listing.
func inviteRow(t *testing.T, srv *server.Server, companyID, ownerTok, subject string) map[string]any {
	t.Helper()
	_, body := doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyID+"/members", ownerTok, nil)
	for _, iv := range list(t, body, "invites") {
		if iv["subject"] == subject {
			return iv
		}
	}
	return nil
}

// TestInviteEnqueuesItsOwnNotification is claim 1 plus the read-back.
func TestInviteEnqueuesItsOwnNotification(t *testing.T) {
	srv := newServer(t)
	companyID, ownerTok, _ := newAdminCompany(t, srv)
	subject := "invitee-" + uuid.NewString()[:8]

	invite(t, srv, companyID, ownerTok, subject, subject+"@example.test")

	// BEFORE any dispatch: invited, but nobody has been told. The two states are
	// reported separately on purpose.
	row := inviteRow(t, srv, companyID, ownerTok, subject)
	if row == nil {
		t.Fatal("the invite is not listed")
	}
	if row["status"] != "pending" {
		t.Errorf("invite status = %v, want pending", row["status"])
	}
	if row["notification_status"] != "pending" {
		t.Fatalf("notification_status = %v, want pending — an invite must enqueue "+
			"its own notification, in its own transaction", row["notification_status"])
	}

	sent, err := srv.DispatchNotifications(context.Background())
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if sent < 1 {
		t.Fatalf("dispatch sent %d messages, want at least 1", sent)
	}

	row = inviteRow(t, srv, companyID, ownerTok, subject)
	if row["notification_status"] != "sent" {
		t.Errorf("after dispatch notification_status = %v, want sent", row["notification_status"])
	}
	// A second pass must not re-send: the status transition is what makes
	// at-least-once delivery safe to repeat.
	again, err := srv.DispatchNotifications(context.Background())
	if err != nil {
		t.Fatalf("second dispatch: %v", err)
	}
	if again != 0 {
		t.Errorf("a second dispatch sent %d messages, want 0 — already-sent "+
			"notifications must not be delivered again", again)
	}
}

// TestInviteWithoutAnEmailIsStillEnqueuedAndSaysSo. The admin API does not
// require an email, and inventing one would be worse than recording that there
// is no address. The notification is still queued so the gap is VISIBLE rather
// than silent.
func TestInviteWithoutAnEmailIsStillEnqueuedAndSaysSo(t *testing.T) {
	srv := newServer(t)
	companyID, ownerTok, _ := newAdminCompany(t, srv)
	subject := "no-email-" + uuid.NewString()[:8]

	invite(t, srv, companyID, ownerTok, subject, "")
	row := inviteRow(t, srv, companyID, ownerTok, subject)
	if row["notification_status"] != "pending" {
		t.Fatalf("notification_status = %v, want pending even with no email on file",
			row["notification_status"])
	}
	if _, err := srv.DispatchNotifications(context.Background()); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	row = inviteRow(t, srv, companyID, ownerTok, subject)
	// The log sender reports it as handled — it logged that there is nowhere to
	// send it. What must NOT happen is a silent success with no record at all.
	if row["notification_status"] != "sent" {
		t.Errorf("notification_status = %v, want sent (the log sender records the "+
			"missing address rather than failing)", row["notification_status"])
	}
}

// TestReInvitingEnqueuesAFreshNotification: re-inviting a pending subject
// upserts the invite, and the point of re-inviting is that they get told again.
func TestReInvitingEnqueuesAFreshNotification(t *testing.T) {
	srv := newServer(t)
	companyID, ownerTok, _ := newAdminCompany(t, srv)
	subject := "re-invitee-" + uuid.NewString()[:8]

	invite(t, srv, companyID, ownerTok, subject, subject+"@example.test")
	if _, err := srv.DispatchNotifications(context.Background()); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got := inviteRow(t, srv, companyID, ownerTok, subject)["notification_status"]; got != "sent" {
		t.Fatalf("first notification = %v, want sent", got)
	}

	invite(t, srv, companyID, ownerTok, subject, subject+"@example.test")
	if got := inviteRow(t, srv, companyID, ownerTok, subject)["notification_status"]; got != "pending" {
		t.Fatalf("after re-inviting, notification_status = %v, want pending — "+
			"re-inviting somebody means telling them again", got)
	}
}

// TestConcurrentDispatchersSendEachMessageOnce is claim 2.
//
// It asserts the claim DIRECTLY — no message id is handed to a Sender twice —
// rather than comparing a total against the number of invites. The first draft
// did compare totals and "found a duplicate": 9 sends for 8 invites. It was the
// TEST that was wrong. A dispatch pass drains EVERY tenant, and this suite runs
// against a shared database that other tests (and other runs) leave rows in, so
// the total is not this company's. A recording sender is immune to that, and it
// is what the guarantee is actually about.
func TestConcurrentDispatchersSendEachMessageOnce(t *testing.T) {
	srv := newServer(t)
	companyID, ownerTok, _ := newAdminCompany(t, srv)
	const n = 8
	for i := 0; i < n; i++ {
		s := fmt.Sprintf("concurrent-%d-%s", i, uuid.NewString()[:6])
		invite(t, srv, companyID, ownerTok, s, s+"@example.test")
	}

	rec := &recordingSender{}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each goroutine gets its OWN dispatcher over the same outbox, which
			// is what a multi-replica deployment looks like.
			if _, err := srv.WithNotificationSender(rec).DispatchOnce(context.Background()); err != nil {
				t.Errorf("dispatch: %v", err)
			}
		}()
	}
	wg.Wait()

	ids := rec.ids()
	seen := map[uuid.UUID]int{}
	for _, id := range ids {
		seen[id]++
	}
	for id, count := range seen {
		if count > 1 {
			t.Errorf("message %s was handed to the sender %d times — the claim is not "+
				"serialised, so an invitee would be mailed twice", id, count)
		}
	}
	// ...and this company's invites were all delivered by SOMEBODY.
	_, body := doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyID+"/members", ownerTok, nil)
	for _, iv := range list(t, body, "invites") {
		if iv["notification_status"] != "sent" {
			t.Errorf("invite %v ended at notification_status %v, want sent",
				iv["subject"], iv["notification_status"])
		}
	}
}

// recordingSender remembers every message id it was asked to send, so a
// duplicate is observable directly.
type recordingSender struct {
	mu   sync.Mutex
	seen []uuid.UUID
}

func (r *recordingSender) Send(_ context.Context, m notify.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, m.ID)
	return nil
}

func (r *recordingSender) ids() []uuid.UUID {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]uuid.UUID(nil), r.seen...)
}

// TestATransportThatAlwaysFailsIsEventuallyDeadLettered is claim 3.
func TestATransportThatAlwaysFailsIsEventuallyDeadLettered(t *testing.T) {
	srv := newServer(t)
	companyID, ownerTok, _ := newAdminCompany(t, srv)
	subject := "doomed-" + uuid.NewString()[:8]
	invite(t, srv, companyID, ownerTok, subject, subject+"@example.test")

	failing := srv.WithNotificationSender(brokenSender{})
	for i := 0; i < 10; i++ {
		if _, err := failing.DispatchOnce(context.Background()); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
	}

	row := inviteRow(t, srv, companyID, ownerTok, subject)
	if row["notification_status"] != "failed" {
		t.Fatalf("after repeated transport failures notification_status = %v, want failed — "+
			"a broken sender must not hide behind a queue that never drains", row["notification_status"])
	}
	if row["notification_error"] == nil || row["notification_error"] == "" {
		t.Errorf("a dead-lettered notification records no error; an operator cannot act on that")
	}
	// And it stops being retried: attempts must not run away.
	att, _ := row["notification_attempts"].(float64)
	if att > 6 {
		t.Errorf("attempts = %v — a failed notification is still being retried", att)
	}
}

// brokenSender always fails, so the retry-then-dead-letter path is exercised
// deterministically rather than waiting for a real transport to misbehave.
type brokenSender struct{}

func (brokenSender) Send(context.Context, notify.Message) error {
	return errors.New("smtp: connection refused")
}

// TestOneCompanysDispatchDoesNotTouchAnothers is claim 4.
func TestOneCompanysDispatchDoesNotTouchAnothers(t *testing.T) {
	srv := newServer(t)
	companyA, ownerA, _ := newAdminCompany(t, srv)
	companyB, ownerB, _ := newAdminCompany(t, srv)

	subjA := "tenant-a-" + uuid.NewString()[:8]
	subjB := "tenant-b-" + uuid.NewString()[:8]
	invite(t, srv, companyA, ownerA, subjA, subjA+"@example.test")
	invite(t, srv, companyB, ownerB, subjB, subjB+"@example.test")

	if _, err := srv.DispatchNotifications(context.Background()); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	// Both were delivered — the dispatcher covers every tenant...
	if got := inviteRow(t, srv, companyA, ownerA, subjA)["notification_status"]; got != "sent" {
		t.Errorf("company A notification = %v, want sent", got)
	}
	if got := inviteRow(t, srv, companyB, ownerB, subjB)["notification_status"]; got != "sent" {
		t.Errorf("company B notification = %v, want sent", got)
	}
	// ...and neither company can SEE the other's, which is RLS, not the SQL text.
	_, bodyA := doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyA+"/members", ownerA, nil)
	for _, iv := range list(t, bodyA, "invites") {
		if iv["subject"] == subjB {
			t.Fatalf("company A's invite list leaked company B's invitee %s", subjB)
		}
	}
}

// TestAStrandedClaimIsReclaimedWhenItsLeaseExpires covers the crash case.
//
// A claim is a LEASE, not a lock: the row leaves 'pending' so no second
// dispatcher can send it, which means a dispatcher that DIES between claiming
// and sending would strand the notification forever if nothing took it back.
// The lease is what bounds that. Without a reclaim the fix for double-sending
// would simply have traded one failure for a quieter one.
func TestAStrandedClaimIsReclaimedWhenItsLeaseExpires(t *testing.T) {
	srv := newServer(t)
	companyID, ownerTok, _ := newAdminCompany(t, srv)
	subject := "stranded-" + uuid.NewString()[:8]
	inviteID := invite(t, srv, companyID, ownerTok, subject, subject+"@example.test")

	conn := superuser(t)
	ctx := context.Background()
	strand := func(age string) {
		t.Helper()
		if _, err := conn.Exec(ctx,
			`UPDATE notification_outbox
			    SET status = 'sending', claimed_at = now() - $2::interval
			  WHERE ref_id = $1`, inviteID, age); err != nil {
			t.Fatalf("strand the claim: %v", err)
		}
	}
	status := func() string {
		t.Helper()
		return fmt.Sprint(inviteRow(t, srv, companyID, ownerTok, subject)["notification_status"])
	}

	// CONTROL FIRST: a claim still inside its lease must NOT be taken back, or
	// the lease would be decorative and the double-send would be back.
	strand("10 seconds")
	if _, err := srv.DispatchNotifications(ctx); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got := status(); got != "sending" {
		t.Fatalf("a FRESH claim was taken back (status %v) — the lease is not being "+
			"honoured, so two dispatchers could send the same message", got)
	}

	// Now age it past the lease: the stranded notification is delivered.
	strand("1 hour")
	if _, err := srv.DispatchNotifications(ctx); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got := status(); got != "sent" {
		t.Fatalf("an EXPIRED claim was not reclaimed (status %v) — a dispatcher that "+
			"died mid-send would strand the invitee forever", got)
	}
}

// hijackSender runs an arbitrary hook in the middle of a send, so a test can
// interleave a SECOND dispatcher between one dispatcher's claim and its settle.
type hijackSender struct {
	during func(m notify.Message)
	err    error
}

func (h hijackSender) Send(_ context.Context, m notify.Message) error {
	if h.during != nil {
		h.during(m)
	}
	return h.err
}

// TestAStaleSettleCannotResurrectAMessageAnotherDispatcherAlreadySent closes the
// hole the lease left open at the OTHER end.
//
// The claim is serialised and a dead dispatcher's row is reclaimed after its
// lease — but the dispatcher that stalled is not necessarily dead. It can come
// back, after its row has been reclaimed and DELIVERED by someone else, and
// record the outcome of its own long-dead attempt. `settle` used to UPDATE by
// id alone, so that late failure would push an already-'sent' row back to
// 'pending', the next tick would send it again, and the invitee would be mailed
// a third time — reintroducing the exact duplicate the lease exists to prevent,
// via the one path the concurrency test does not exercise (it never stalls a
// dispatcher past its lease).
//
// Every settle is therefore guarded by the lease token the message was claimed
// under. This test makes a dispatcher lose that race deliberately.
func TestAStaleSettleCannotResurrectAMessageAnotherDispatcherAlreadySent(t *testing.T) {
	srv := newServer(t)
	companyID, ownerTok, _ := newAdminCompany(t, srv)
	subject := "stale-settle-" + uuid.NewString()[:8]
	inviteID := invite(t, srv, companyID, ownerTok, subject, subject+"@example.test")

	conn := superuser(t)
	ctx := context.Background()
	status := func() string {
		t.Helper()
		return fmt.Sprint(inviteRow(t, srv, companyID, ownerTok, subject)["notification_status"])
	}

	var reclaimed int
	stalled := hijackSender{
		// Called while THIS dispatcher holds the claim. Standing in for "an hour
		// passed inside Send": age the claim so the lease has expired, then let a
		// second dispatcher take the row and deliver it. That reclaim rewrites
		// claimed_at, which is precisely what makes our token stale — the same
		// thing that would happen if the clock had really advanced.
		during: func(notify.Message) {
			if _, err := conn.Exec(ctx,
				`UPDATE notification_outbox SET claimed_at = now() - '1 hour'::interval
				  WHERE ref_id = $1`, inviteID); err != nil {
				t.Errorf("age the claim: %v", err)
				return
			}
			n, err := srv.DispatchNotifications(ctx)
			if err != nil {
				t.Errorf("second dispatcher: %v", err)
			}
			reclaimed = n
		},
		// ...and only now does the stalled dispatcher's own send fail.
		err: errors.New("stalled transport gave up"),
	}

	if _, err := srv.WithNotificationSender(stalled).DispatchOnce(ctx); err != nil {
		t.Fatalf("stalled dispatcher: %v", err)
	}
	if reclaimed != 1 {
		t.Fatalf("the second dispatcher delivered %d messages, want 1 — the setup did "+
			"not actually reclaim the row, so this test proves nothing", reclaimed)
	}

	if got := status(); got != "sent" {
		t.Fatalf("after a stale settle the notification is %q, want \"sent\" — a dispatcher "+
			"that lost its lease overwrote the outcome of the one that actually "+
			"delivered, and the invitee will be mailed again", got)
	}

	// And nothing is left claimable: a following tick must find nothing to do.
	n, err := srv.DispatchNotifications(ctx)
	if err != nil {
		t.Fatalf("follow-up dispatch: %v", err)
	}
	if n != 0 {
		t.Fatalf("a follow-up tick sent %d more copies of an already-delivered message", n)
	}
}
