package ledger

// draft_acceptance_test.go — §7.3 B, against the LIVE database.
//
// THE CLAIM UNDER TEST: accepting one insightd bill draft can produce AT MOST ONE
// voucher, and the loser writes NOTHING. Not "the client refuses to try twice" —
// the client has four layers that each refuse to try twice, and a browser drive
// still produced two real vouchers from one supplier bill, because every one of
// those layers is a check-then-act that a SECOND MACHINE walks straight through
// while the first machine's confirm to insightd is still owed.
//
// So every test here posts the second voucher THROUGH THE POST PATH, exactly as a
// second machine would, and asserts on the database rather than on a refusal
// message. In the INTERNAL test package (`ledger`, not `ledger_test`) because the
// interesting assertions read voucher_head.source_draft_id and the numbering
// counter directly — a test driven only through the public API could see that the
// second call errored without being able to show that it left no trace.

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/migrations"
)

type draftEnv struct {
	pool    *db.Pool
	ctx     context.Context
	company uuid.UUID
	led     *Service

	creditor uuid.UUID
	purchase uuid.UUID
	vtID     uuid.UUID
}

func setupDraftAcceptance(t *testing.T) *draftEnv {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	ctx := context.Background()

	adminConn, err := pgx.Connect(ctx, testdb.AdminDSNValue())
	if err != nil {
		t.Skipf("admin postgres unavailable, skipping integration test: %v", err)
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

	companyID := newDraftAcceptanceCompany(t, pool)

	e := &draftEnv{
		pool:    pool,
		ctx:     db.WithCompany(ctx, companyID),
		company: companyID,
		led:     NewService(pool),
	}
	ids := identity.NewService(pool)
	if e.creditor, err = ids.CreateLedger(e.ctx, "Pune Fabricators", "Sundry Creditors"); err != nil {
		t.Fatalf("create creditor: %v", err)
	}
	if e.purchase, err = ids.CreateLedger(e.ctx, "Local Purchases", "Purchase Accounts"); err != nil {
		t.Fatalf("create purchase ledger: %v", err)
	}
	if e.vtID, err = ids.VoucherTypeID(e.ctx, "Purchase"); err != nil {
		t.Fatalf("purchase voucher type: %v", err)
	}
	return e
}

// newDraftAcceptanceCompany creates a throwaway company and registers its
// teardown in the SAME breath — see newInwardCompany for why the pair may never
// be split, and why this is never a pre-existing company.
func newDraftAcceptanceCompany(t *testing.T, pool *db.Pool) uuid.UUID {
	t.Helper()
	companyID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new company id: %v", err)
	}
	cctx := db.WithCompany(context.Background(), companyID)
	if _, err := identity.NewService(pool).CreateCompany(cctx, identity.Company{
		ID:          companyID,
		Name:        "Draft Accept Co " + companyID.String()[:8],
		FYStartDate: "2025-04-01",
	}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	testdb.Cleanup(t, companyID)
	return companyID
}

// accept builds the body a Bill Inbox accept POSTs: a balanced purchase carrying
// the draft it came from. `draftID` empty means "hand-keyed", which is what nearly
// every voucher in a real book is.
func (e *draftEnv) accept(draftID, amount string) VoucherInput {
	return VoucherInput{
		Class:         Purchase,
		VoucherTypeID: e.vtID,
		PartyLedgerID: e.creditor,
		VoucherDate:   "2026-03-31",
		Narration:     "Bill from Pune Fabricators",
		Party:         "Pune Fabricators",
		CreatedBy:     "tester",
		Postings: []PostingInput{
			{LedgerID: e.purchase, DrCr: Debit, Amount: decimal.RequireFromString(amount)},
			{LedgerID: e.creditor, DrCr: Credit, Amount: decimal.RequireFromString(amount)},
		},
		SourceDraftID: draftID,
	}
}

// heads counts the LIVE-or-tombstoned voucher_head rows in this company. It is
// the assertion that matters: a refusal that still wrote a voucher is the defect,
// not the fix.
func (e *draftEnv) heads(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM voucher_head WHERE company_id = $1`, e.company).Scan(&n)
	}); err != nil {
		t.Fatalf("count heads: %v", err)
	}
	return n
}

// sourceDraft reads what a voucher records as its provenance ("" for none).
func (e *draftEnv) sourceDraft(t *testing.T, voucherID uuid.UUID) string {
	t.Helper()
	var got *string
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT source_draft_id FROM voucher_head
			  WHERE company_id = $1 AND voucher_id = $2`, e.company, voucherID).Scan(&got)
	}); err != nil {
		t.Fatalf("read source_draft_id: %v", err)
	}
	if got == nil {
		return ""
	}
	return *got
}

// TestASecondAcceptOfOneDraftPostsNothingAtAll is §7.3 B's whole point.
//
// It is deliberately driven the way the DEFECT was driven and not the way the
// client is written: the second accept carries no memory of the first — no
// session memo, no localStorage queue, no claim, and a DIFFERENT amount, so no
// day-book or content match could catch it either. Everything the browser knows
// is gone. What is left is the database, and the database has to be enough.
func TestASecondAcceptOfOneDraftPostsNothingAtAll(t *testing.T) {
	e := setupDraftAcceptance(t)

	first, err := e.led.PostVoucher(e.ctx, e.accept("draft-pune-001", "21240.0000"))
	if err != nil {
		t.Fatalf("first accept: %v", err)
	}
	if got := e.heads(t); got != 1 {
		t.Fatalf("after the first accept: got %d voucher heads, want 1", got)
	}

	// THE SECOND MACHINE. Different amount on purpose — if this were caught by a
	// content or day-book match the test would prove nothing about the index.
	_, err = e.led.PostVoucher(e.ctx, e.accept("draft-pune-001", "9999.0000"))
	var dup *DraftAlreadyAcceptedError
	if !errors.As(err, &dup) {
		t.Fatalf("second accept: got %v, want *DraftAlreadyAcceptedError", err)
	}
	// ⛔ THE ASSERTION THAT IS THE FEATURE. Not "an error was returned" — an error
	// that still left a payable and an input tax credit behind is the defect wearing
	// the fix's clothes.
	if got := e.heads(t); got != 1 {
		t.Fatalf("after the refused accept: got %d voucher heads, want 1 — the loser wrote a voucher", got)
	}
	// The refusal has to NAME the voucher, or the operator cannot tell a blocked
	// duplicate from a lost bill.
	if dup.VoucherID != first.VoucherID {
		t.Errorf("refusal names voucher %v, want the winner %v", dup.VoucherID, first.VoucherID)
	}
	if dup.VoucherNo != first.VoucherNo {
		t.Errorf("refusal names voucher_no %q, want %q", dup.VoucherNo, first.VoucherNo)
	}
	if dup.DraftID != "draft-pune-001" {
		t.Errorf("refusal names draft %q, want %q", dup.DraftID, "draft-pune-001")
	}

	// ⚠️ AND THE NUMBERING COUNTER DID NOT ADVANCE. numbering.go allocates the
	// display number inside this same transaction, so a rollback gives it back; a
	// refusal that burned a number would leave a hole in a statutory series that no
	// report could explain. The next genuinely different bill must get the number
	// the loser would have taken.
	next, err := e.led.PostVoucher(e.ctx, e.accept("draft-pune-002", "500.0000"))
	if err != nil {
		t.Fatalf("third accept (a different draft): %v", err)
	}
	if next.VoucherNo == first.VoucherNo {
		t.Fatalf("the second bill reused the first's number %q", next.VoucherNo)
	}
	if got := e.heads(t); got != 2 {
		t.Fatalf("after a genuinely different bill: got %d voucher heads, want 2", got)
	}
}

// TestAHandKeyedVoucherIsNeverConstrainedByTheDraftIndex guards the blast radius:
// nearly every voucher in a real book was typed by a human and has no draft, and
// this index must be invisible to all of them.
//
// ⚠️ WHAT ACTUALLY KEEPS THEM UNCONSTRAINED IS THE NULL, NOT THE PARTIAL CLAUSE,
// AND THAT WAS MEASURED RATHER THAN ASSUMED. The first draft of this comment
// claimed the test would catch a non-partial index. It does not: dropping the
// `WHERE source_draft_id IS NOT NULL` and re-running left every test here GREEN,
// because a Postgres unique index is NULLS DISTINCT by default and unlimited NULL
// rows never collide. 0077 keeps the clause to keep the index off ~every row in
// the book; correctness rests elsewhere.
//
// ⛔ SO THIS TEST PINS THE TWO THINGS THAT WOULD ACTUALLY BREAK IT, and the
// mutation that proves it is real: writing the draft id WITHOUT nullIfEmpty, so a
// hand-keyed voucher stores ” instead of NULL. Predicted 1 RED, observed THREE —
// 0077's non-empty CHECK also refuses the ” that an alter and a delete would
// restate. Either the ” reaches the index (every hand-keyed voucher in a company
// collides with every other) or the CHECK stops it first; both are fatal and both
// are visible here.
func TestAHandKeyedVoucherIsNeverConstrainedByTheDraftIndex(t *testing.T) {
	e := setupDraftAcceptance(t)

	for i, amount := range []string{"100.0000", "200.0000", "300.0000"} {
		if _, err := e.led.PostVoucher(e.ctx, e.accept("", amount)); err != nil {
			t.Fatalf("hand-keyed voucher %d: %v", i+1, err)
		}
	}
	if got := e.heads(t); got != 3 {
		t.Fatalf("got %d voucher heads, want 3 — a hand-keyed voucher was refused as a duplicate bill", got)
	}
}

// TestAnAlterCannotRepointAVoucherAtAnotherDraft.
//
// An ALTER that carried a draft id would CONSUME that draft — the inbox would
// stop offering the bill — without a single line of it reaching the books. The
// draft must still be acceptable afterwards, and the voucher must still record
// where it actually came from.
func TestAnAlterCannotRepointAVoucherAtAnotherDraft(t *testing.T) {
	e := setupDraftAcceptance(t)

	first, err := e.led.PostVoucher(e.ctx, e.accept("draft-A", "1000.0000"))
	if err != nil {
		t.Fatalf("accept draft-A: %v", err)
	}

	amended := e.accept("draft-B", "1200.0000")
	amended.VoucherNo = first.VoucherNo
	if _, err := e.led.AlterVoucher(e.ctx, first.VoucherID, amended); err != nil {
		t.Fatalf("alter: %v", err)
	}
	if got := e.sourceDraft(t, first.VoucherID); got != "draft-A" {
		t.Fatalf("after the alter the voucher records draft %q, want draft-A", got)
	}
	// draft-B was never accepted, so accepting it must still work.
	if _, err := e.led.PostVoucher(e.ctx, e.accept("draft-B", "1200.0000")); err != nil {
		t.Fatalf("accepting draft-B after the alter: %v", err)
	}
	if got := e.heads(t); got != 2 {
		t.Fatalf("got %d voucher heads, want 2", got)
	}
}

// TestDeletingTheVoucherLeavesTheDraftSpent pins a decision rather than a
// mechanism, so that changing it is deliberate.
//
// Acceptance is TERMINAL, across a delete, matching insightd's own `accepted`
// state. The alternative — a delete frees the draft — reads as helpful and is
// how the same bill gets posted twice by an operator who deleted a mis-keyed
// purchase and then found the inbox offering it again.
func TestDeletingTheVoucherLeavesTheDraftSpent(t *testing.T) {
	e := setupDraftAcceptance(t)

	first, err := e.led.PostVoucher(e.ctx, e.accept("draft-once", "750.0000"))
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if _, err := e.led.DeleteVoucher(e.ctx, first.VoucherID, "tester"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = e.led.PostVoucher(e.ctx, e.accept("draft-once", "750.0000"))
	var dup *DraftAlreadyAcceptedError
	if !errors.As(err, &dup) {
		t.Fatalf("re-accept after delete: got %v, want *DraftAlreadyAcceptedError", err)
	}
	// The lookup must still name the voucher even though it is tombstoned — an
	// operator told "already accepted" with no number cannot find what happened.
	if dup.VoucherID != first.VoucherID {
		t.Errorf("refusal names %v, want the tombstoned winner %v", dup.VoucherID, first.VoucherID)
	}
}

// TestVoucherBySourceDraftIDAnswersNoForAnUnknownDraft — the read half, and the
// "" case, which must never scan.
func TestVoucherBySourceDraftIDAnswersNoForAnUnknownDraft(t *testing.T) {
	e := setupDraftAcceptance(t)

	if _, _, found, err := e.led.VoucherBySourceDraftID(e.ctx, "nobody"); err != nil || found {
		t.Fatalf("unknown draft: found=%v err=%v, want false/nil", found, err)
	}
	if _, _, found, err := e.led.VoucherBySourceDraftID(e.ctx, ""); err != nil || found {
		t.Fatalf("empty draft id: found=%v err=%v, want false/nil", found, err)
	}
}
