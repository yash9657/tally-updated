package ledger

// idempotency_test.go — W5, migration 0100, against the LIVE database.
//
// THE CLAIM UNDER TEST: one REQUEST, delivered any number of times — in
// sequence or concurrently — is ONE voucher, and every delivery is answered
// with that voucher. The duplicate-post class of parity row 30 (and D108 on the
// hand-keyed path) is "the same request arriving twice", so every test posts
// the second delivery THROUGH THE POST PATH and counts voucher_head rows: an
// answer that looks right while a second payable sits in the books is the
// defect, not the fix.
//
// Reuses draft_acceptance_test.go's environment: a purchase with NO draft id
// and NO supplier invoice number is exactly the body that the Bill Inbox's
// residual window names ("no draft id resolvable") and that 0077 cannot see.

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
)

// numberless is the residual-window body: a hand-built purchase with no draft
// id and no supplier invoice number, so no layer before 0100 can tell a retry
// from a second bill.
func (e *draftEnv) numberless(key string) VoucherInput {
	in := e.accept("", "21240.0000")
	in.IdempotencyKey = key
	return in
}

func TestADoubleSubmitUnderOneKeyPostsOneVoucherAndReplaysIt(t *testing.T) {
	e := setupDraftAcceptance(t)

	first, err := e.led.PostVoucher(e.ctx, e.numberless("bill-inbox:retry-0001"))
	if err != nil {
		t.Fatalf("first submit: %v", err)
	}
	if first.Replayed {
		t.Fatal("the first delivery WROTE the voucher and must not be marked a replay")
	}
	second, err := e.led.PostVoucher(e.ctx, e.numberless("bill-inbox:retry-0001"))
	if err != nil {
		t.Fatalf("second submit of the SAME request must succeed as a replay, got %v", err)
	}
	// ⛔ THE ASSERTION THAT IS THE FEATURE.
	if got := e.heads(t); got != 1 {
		t.Fatalf("two deliveries of one request left %d vouchers, want 1", got)
	}
	if !second.Replayed {
		t.Error("the second delivery must be marked Replayed — a caller has to be able to say 'already posted'")
	}
	if second.VoucherID != first.VoucherID || second.VoucherVersionID != first.VoucherVersionID ||
		second.VoucherNo != first.VoucherNo || string(second.Hash) != string(first.Hash) {
		t.Errorf("the replay must BE the first result: got %+v, want %+v", second, first)
	}
}

// ⛔ THE RACE. Two browsers (or a browser and its own retry) hit the server at
// once, both past the pre-check. The key row inside the transaction is what
// serialises them: exactly one writes, every other is a replay of it.
func TestConcurrentDeliveriesOfOneRequestPostOneVoucher(t *testing.T) {
	e := setupDraftAcceptance(t)
	const n = 8

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []PostResult
		errs    []error
	)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r, err := e.led.PostVoucher(e.ctx, e.numberless("bill-inbox:race-0001"))
			mu.Lock()
			defer mu.Unlock()
			results = append(results, r)
			errs = append(errs, err)
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("delivery %d: %v", i, err)
		}
	}
	if got := e.heads(t); got != 1 {
		t.Fatalf("%d concurrent deliveries of one request left %d vouchers, want 1", n, got)
	}
	writers := 0
	for _, r := range results {
		if !r.Replayed {
			writers++
		}
		if r.VoucherID != results[0].VoucherID {
			t.Errorf("delivery answered with voucher %s, another with %s — one request is one voucher",
				r.VoucherID, results[0].VoucherID)
		}
	}
	if writers != 1 {
		t.Errorf("%d deliveries claim to have written; exactly one did", writers)
	}
}

// The same key with a DIFFERENT body is refused — never replayed, because
// replaying voucher A for request B tells the operator B was posted.
func TestTheSameKeyWithADifferentRequestIsRefusedNotReplayed(t *testing.T) {
	e := setupDraftAcceptance(t)
	if _, err := e.led.PostVoucher(e.ctx, e.numberless("bill-inbox:reuse-0001")); err != nil {
		t.Fatalf("first: %v", err)
	}
	other := e.accept("", "9999.0000")
	other.IdempotencyKey = "bill-inbox:reuse-0001"
	_, err := e.led.PostVoucher(e.ctx, other)
	if !errors.Is(err, ErrIdempotencyKeyReused) {
		t.Fatalf("want ErrIdempotencyKeyReused, got %v", err)
	}
	if got := e.heads(t); got != 1 {
		t.Fatalf("a refused reuse left %d vouchers, want 1", got)
	}
}

// THE CONTROL. Without a key the same body twice IS two vouchers — which is
// correct for bulk entry, and is what proves the protection above comes from
// the key and not from something else in the post path.
func TestWithoutAKeyTwoIdenticalPostsAreTwoVouchers(t *testing.T) {
	e := setupDraftAcceptance(t)
	for i := 0; i < 2; i++ {
		if _, err := e.led.PostVoucher(e.ctx, e.numberless("")); err != nil {
			t.Fatalf("post %d: %v", i, err)
		}
	}
	if got := e.heads(t); got != 2 {
		t.Fatalf("without a key two posts must be two vouchers (bulk entry); got %d", got)
	}
}

// A refused post consumes no key: the retry is evaluated, not replayed as the
// refusal (or, worse, refused as a reuse).
func TestARefusedPostConsumesNoKey(t *testing.T) {
	e := setupDraftAcceptance(t)
	bad := e.numberless("bill-inbox:refused-0001")
	bad.Postings = bad.Postings[:1] // unbalanced: refused before any write
	if _, err := e.led.PostVoucher(e.ctx, bad); err == nil {
		t.Fatal("control: an unbalanced voucher must be refused")
	}
	res, err := e.led.PostVoucher(e.ctx, e.numberless("bill-inbox:refused-0001"))
	if err != nil {
		t.Fatalf("the corrected request under the same key must post: %v", err)
	}
	if res.Replayed || e.heads(t) != 1 {
		t.Fatalf("want one freshly-written voucher; replayed=%v heads=%d", res.Replayed, e.heads(t))
	}
}

// ⛔ ROW 30'S AMBIGUOUS-TIMEOUT WINDOW, WITH A DRAFT ID. The voucher landed and
// the client never heard; its retry carries the same draft AND the same key.
// Before 0100 the retry was answered 409 draft_already_accepted — a refusal for
// a request that had in fact succeeded. Now it is the success, replayed.
func TestARetryOfADraftAcceptIsAReplayNotADraftConflict(t *testing.T) {
	e := setupDraftAcceptance(t)
	in := e.accept("draft-timeout-001", "21240.0000")
	in.IdempotencyKey = "bill-inbox:draft-timeout-001"
	first, err := e.led.PostVoucher(e.ctx, in)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	again, err := e.led.PostVoucher(e.ctx, in)
	if err != nil {
		t.Fatalf("the retry must be a replay, got %v", err)
	}
	if !again.Replayed || again.VoucherID != first.VoucherID || e.heads(t) != 1 {
		t.Fatalf("replayed=%v id=%s (want %s) heads=%d", again.Replayed, again.VoucherID, first.VoucherID, e.heads(t))
	}
	// And 0077 still stands behind it: a SECOND machine with the same draft but
	// no knowledge of the key is refused by the draft index, exactly as before.
	other := e.accept("draft-timeout-001", "21240.0000")
	other.IdempotencyKey = "bill-inbox:another-browser-001"
	_, err = e.led.PostVoucher(e.ctx, other)
	var dup *DraftAlreadyAcceptedError
	if !errors.As(err, &dup) {
		t.Fatalf("a different key on the same draft must still meet 0077's refusal, got %v", err)
	}
}

// ⛔ THE SAME WINDOW, CONCURRENTLY. Both deliveries pass the pre-check; the
// DRAFT index (0077) refuses the loser before the key row is ever reached. The
// loser must still be answered with the winner's voucher, not "already
// accepted" — it IS the winner's request.
func TestConcurrentDeliveriesOfOneDraftAcceptAllReplay(t *testing.T) {
	e := setupDraftAcceptance(t)
	in := e.accept("draft-race-001", "21240.0000")
	in.IdempotencyKey = "bill-inbox:draft-race-001"
	const n = 6
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
		ids  = map[string]int{}
	)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r, err := e.led.PostVoucher(e.ctx, in)
			mu.Lock()
			defer mu.Unlock()
			errs = append(errs, err)
			if err == nil {
				ids[r.VoucherID.String()]++
			}
		}()
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("delivery %d of ONE request was refused: %v", i, err)
		}
	}
	if e.heads(t) != 1 || len(ids) != 1 {
		t.Fatalf("heads=%d distinct vouchers answered=%d; want 1 and 1", e.heads(t), len(ids))
	}
}

func TestAMalformedKeyIsRefusedAndWritesNothing(t *testing.T) {
	e := setupDraftAcceptance(t)
	for _, k := range []string{"short", "has space in it", "tab\tinside-it"} {
		if _, err := e.led.PostVoucher(e.ctx, e.numberless(k)); !errors.Is(err, ErrBadIdempotencyKey) {
			t.Errorf("key %q: want ErrBadIdempotencyKey, got %v", k, err)
		}
	}
	if got := e.heads(t); got != 0 {
		t.Fatalf("a refused key left %d vouchers", got)
	}
}

// ⛔ INSERT-ONLY, BOTH HALVES. The app role cannot UPDATE or DELETE a key: a
// deleted key is a request that posts again.
func TestTheAppRoleCannotRewriteOrDeleteAKey(t *testing.T) {
	e := setupDraftAcceptance(t)
	if _, err := e.led.PostVoucher(e.ctx, e.numberless("bill-inbox:immutable-01")); err != nil {
		t.Fatalf("post: %v", err)
	}
	for _, stmt := range []string{
		`UPDATE voucher_idempotency SET request_sha256 = repeat('0', 64) WHERE company_id = $1`,
		`DELETE FROM voucher_idempotency WHERE company_id = $1`,
	} {
		var execErr error
		_ = e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
			_, execErr = tx.Exec(ctx, stmt, e.company)
			return execErr
		})
		if execErr == nil {
			t.Errorf("the app role ran %q; the key table must be insert-only", stmt)
		}
	}
	// The key still replays after the attempts.
	res, err := e.led.PostVoucher(e.ctx, e.numberless("bill-inbox:immutable-01"))
	if err != nil || !res.Replayed || e.heads(t) != 1 {
		t.Fatalf("after the refused rewrites the key must still replay: err=%v replayed=%v heads=%d",
			err, res.Replayed, e.heads(t))
	}
}
