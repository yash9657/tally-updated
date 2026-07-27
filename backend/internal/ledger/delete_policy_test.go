package ledger_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/reporting"
)

// deletePolicyFixture creates a company + Journal voucher type + two ledgers and
// posts ONE balanced journal, returning everything the delete/alter-policy tests
// need. It deliberately does not reuse freezeFixture: these tests also need the
// pool (for raw version-count assertions) and the company id (for the Trial
// Balance oracle), neither of which freezeFixture returns.
type dpFixture struct {
	pool      *db.Pool
	ctx       context.Context
	companyID uuid.UUID
	led       *ledger.Service
	vtID      uuid.UUID
	dr, cr    uuid.UUID
	voucherID uuid.UUID
}

func deletePolicyFixture(t *testing.T, no, amount string) dpFixture {
	t.Helper()
	pool, ctx, companyID, ids := setup(t)

	vtID, err := ids.VoucherTypeID(ctx, "Journal")
	if err != nil {
		t.Fatalf("voucher type: %v", err)
	}
	dr, err := ids.CreateLedger(ctx, "DelPolicy Dr", "Current Assets")
	if err != nil {
		t.Fatalf("create dr ledger: %v", err)
	}
	cr, err := ids.CreateLedger(ctx, "DelPolicy Cr", "Current Liabilities")
	if err != nil {
		t.Fatalf("create cr ledger: %v", err)
	}
	led := ledger.NewService(pool)
	res, err := led.PostVoucher(ctx, mkJournal(vtID, dr, cr, no, amount))
	if err != nil {
		t.Fatalf("post journal: %v", err)
	}
	return dpFixture{
		pool: pool, ctx: ctx, companyID: companyID, led: led,
		vtID: vtID, dr: dr, cr: cr, voucherID: res.VoucherID,
	}
}

// dpVersionCount reads the RAW number of rows in the append-only version log for a
// voucher. This is the independent oracle for "no new tombstone was written" —
// it bypasses the ledger service entirely.
func dpVersionCount(t *testing.T, f dpFixture) int {
	t.Helper()
	var n int
	if err := f.pool.RunInTx(f.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM voucher_version WHERE voucher_id = $1`, f.voucherID).Scan(&n)
	}); err != nil {
		t.Fatalf("count versions: %v", err)
	}
	return n
}

// dpHeadState reads voucher_head's tombstone flag + head pointer directly.
func dpHeadState(t *testing.T, f dpFixture) (isDeleted bool, versionNo int, currentID uuid.UUID) {
	t.Helper()
	if err := f.pool.RunInTx(f.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT is_deleted, version_no, current_version_id
			   FROM voucher_head WHERE voucher_id = $1`, f.voucherID).
			Scan(&isDeleted, &versionNo, &currentID)
	}); err != nil {
		t.Fatalf("read head: %v", err)
	}
	return
}

// dpAssertTrialBalanceZero uses the INDEPENDENT reporting package (not the ledger
// service echoing itself) to prove the deleted voucher contributes nothing.
func dpAssertTrialBalanceZero(t *testing.T, f dpFixture, when string) {
	t.Helper()
	rep := reporting.NewService(f.pool)
	tb, err := rep.TrialBalance(f.ctx, f.companyID, "")
	if err != nil {
		t.Fatalf("trial balance (%s): %v", when, err)
	}
	if !tb.TotalDebit.Equal(decimal.Zero) || !tb.TotalCredit.Equal(decimal.Zero) {
		t.Fatalf("trial balance must be zero (%s): dr=%s cr=%s", when, tb.TotalDebit, tb.TotalCredit)
	}
}

// TestDeletePolicy_DoubleDeleteIsIdempotent pins the WS9 policy: re-deleting an
// already-tombstoned voucher writes NOTHING and echoes the existing tombstone.
// Before WS9 the second DELETE appended a third version (and a fourth, ...).
//
// Non-vacuity: the version-count and head-pointer assertions come from raw SQL,
// and the "nothing changed" claim is anchored on the FIRST delete's result, so
// deleting the guard makes this test fail on count (3 != 2) and on VersionNo.
func TestDeletePolicy_DoubleDeleteIsIdempotent(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := deletePolicyFixture(t, "DP-IDEM-1", "1500.0000")

	first, err := f.led.DeleteVoucher(f.ctx, f.voucherID, "deleter")
	if err != nil {
		t.Fatalf("first delete: %v", err)
	}
	if first.VersionNo != 2 {
		t.Fatalf("first delete should be version 2, got %d", first.VersionNo)
	}
	if got := dpVersionCount(t, f); got != 2 {
		t.Fatalf("after first delete: want 2 versions, got %d", got)
	}

	second, err := f.led.DeleteVoucher(f.ctx, f.voucherID, "deleter-retry")
	if err != nil {
		t.Fatalf("second delete must be an idempotent no-op, got error: %v", err)
	}

	// The echoed body must be byte-identical to the first tombstone's identity.
	if second.VoucherID != first.VoucherID {
		t.Fatalf("voucher_id changed: %s != %s", second.VoucherID, first.VoucherID)
	}
	if second.VoucherVersionID != first.VoucherVersionID {
		t.Fatalf("re-delete must echo the EXISTING tombstone version id: got %s want %s",
			second.VoucherVersionID, first.VoucherVersionID)
	}
	if second.VersionNo != first.VersionNo {
		t.Fatalf("re-delete must not bump version_no: got %d want %d", second.VersionNo, first.VersionNo)
	}
	if second.VoucherNo != first.VoucherNo {
		t.Fatalf("re-delete must echo voucher_no %q, got %q", first.VoucherNo, second.VoucherNo)
	}
	if !bytes.Equal(second.Hash, first.Hash) {
		t.Fatalf("re-delete must echo the existing tombstone hash")
	}

	// Nothing was appended and the head still points at the first tombstone.
	if got := dpVersionCount(t, f); got != 2 {
		t.Fatalf("double delete must NOT append a version: want 2, got %d", got)
	}
	isDeleted, headVer, curID := dpHeadState(t, f)
	if !isDeleted {
		t.Fatal("head must stay tombstoned after a re-delete")
	}
	if headVer != 2 || curID != first.VoucherVersionID {
		t.Fatalf("head moved: version_no=%d current_version_id=%s (want 2 / %s)",
			headVer, curID, first.VoucherVersionID)
	}
	dpAssertTrialBalanceZero(t, f, "after double delete")
}

// TestDeletePolicy_AlterAfterDeleteIs404 pins the other half of the policy:
// altering a tombstoned voucher is ErrVoucherNotFound, NOT a silent
// resurrection. Before WS9 the head upsert flipped is_deleted back to false and
// the deleted amounts reappeared in the Trial Balance.
func TestDeletePolicy_AlterAfterDeleteIs404(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := deletePolicyFixture(t, "DP-ALT-1", "2200.0000")

	del, err := f.led.DeleteVoucher(f.ctx, f.voucherID, "deleter")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err = f.led.AlterVoucher(f.ctx, f.voucherID,
		mkJournal(f.vtID, f.dr, f.cr, "DP-ALT-1", "9999.0000"))
	if !errors.Is(err, ledger.ErrVoucherNotFound) {
		t.Fatalf("alter of a deleted voucher must be ErrVoucherNotFound, got %v", err)
	}

	// The refusal must be a full rollback: no version appended, head untouched,
	// and the books still net to zero (a resurrection would put 9999 back).
	if got := dpVersionCount(t, f); got != 2 {
		t.Fatalf("refused alter must NOT append a version: want 2, got %d", got)
	}
	isDeleted, headVer, curID := dpHeadState(t, f)
	if !isDeleted {
		t.Fatal("voucher must STAY deleted after a refused alter (no resurrection)")
	}
	if headVer != del.VersionNo || curID != del.VoucherVersionID {
		t.Fatalf("head moved after a refused alter: version_no=%d current=%s", headVer, curID)
	}
	dpAssertTrialBalanceZero(t, f, "after refused alter")
}

// TestDeletePolicy_AlterOfLiveVoucherStillWorks is the SCOPE test for the guard:
// it must reject only tombstoned heads. If the post() guard were widened to all
// ops (or dropped the is_deleted condition), this test fails — which is exactly
// the mutation this pins.
func TestDeletePolicy_AlterOfLiveVoucherStillWorks(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := deletePolicyFixture(t, "DP-LIVE-1", "700.0000")

	alt, err := f.led.AlterVoucher(f.ctx, f.voucherID,
		mkJournal(f.vtID, f.dr, f.cr, "DP-LIVE-1", "800.0000"))
	if err != nil {
		t.Fatalf("alter of a LIVE voucher must still succeed: %v", err)
	}
	if alt.VersionNo != 2 {
		t.Fatalf("alter should produce version 2, got %d", alt.VersionNo)
	}
	if got := dpVersionCount(t, f); got != 2 {
		t.Fatalf("want 2 versions after alter, got %d", got)
	}
	isDeleted, _, _ := dpHeadState(t, f)
	if isDeleted {
		t.Fatal("an alter must not tombstone the voucher")
	}

	// Independent oracle: the altered amount is what the reporting package sees.
	rep := reporting.NewService(f.pool)
	tb, err := rep.TrialBalance(f.ctx, f.companyID, "")
	if err != nil {
		t.Fatalf("trial balance: %v", err)
	}
	want := decimal.RequireFromString("800.0000")
	if !tb.TotalDebit.Equal(want) {
		t.Fatalf("altered amount must replace the original: dr=%s want %s", tb.TotalDebit, want)
	}

	// And the live voucher can still be deleted afterwards (version 3).
	del, err := f.led.DeleteVoucher(f.ctx, f.voucherID, "deleter")
	if err != nil {
		t.Fatalf("delete after alter: %v", err)
	}
	if del.VersionNo != 3 {
		t.Fatalf("delete after alter should be version 3, got %d", del.VersionNo)
	}
	dpAssertTrialBalanceZero(t, f, "after alter+delete")
}

// TestDeletePolicy_GuardIsAlterOnly pins that the post() guard does NOT leak
// into the OpCreate path used by the offline sequencer (PostVoucherWithID). That
// behaviour is intentionally unchanged by WS9: the sequencer's applyCreate
// already reports a duplicate for ANY existing head (live or tombstoned), so a
// create never legitimately reaches a tombstoned id in production. This test
// exists so a future widening of the guard is a deliberate, visible change.
func TestDeletePolicy_GuardIsAlterOnly(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := deletePolicyFixture(t, "DP-CREATE-1", "300.0000")
	if _, err := f.led.DeleteVoucher(f.ctx, f.voucherID, "deleter"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	in := mkJournal(f.vtID, f.dr, f.cr, "", "300.0000")
	if _, err := f.led.PostVoucherWithID(f.ctx, f.voucherID, in); err != nil {
		t.Fatalf("OpCreate on a tombstoned id must keep its pre-WS9 behaviour, got: %v", err)
	}
}
