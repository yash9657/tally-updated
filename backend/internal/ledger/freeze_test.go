package ledger_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/ledger"
)

// mkJournal builds a balanced two-line Journal voucher between two ledgers. A
// Journal carries no GST and no class-specific posting rules beyond balance, so
// it is the cleanest fixture for the freeze tests (the gate is class-agnostic).
func mkJournal(vtID, dr, cr uuid.UUID, no, amount string) ledger.VoucherInput {
	a := decimal.RequireFromString(amount)
	return ledger.VoucherInput{
		Class:         ledger.Journal,
		VoucherTypeID: vtID,
		VoucherNo:     no,
		VoucherDate:   "2025-05-20",
		Narration:     "freeze fixture",
		CreatedBy:     "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: dr, DrCr: ledger.Debit, Amount: a},
			{LedgerID: cr, DrCr: ledger.Credit, Amount: a},
		},
	}
}

// freezeFixture creates a company, a Journal voucher type, two ledgers, and a
// posted voucher; it returns the ledger service, the scoped ctx, and the
// voucher id for the freeze tests.
func freezeFixture(t *testing.T, no, amount string) (*ledger.Service, context.Context, uuid.UUID) {
	t.Helper()
	pool, ctx, _, ids := setup(t)

	vtID, err := ids.VoucherTypeID(ctx, "Journal")
	if err != nil {
		t.Fatalf("voucher type: %v", err)
	}
	dr, err := ids.CreateLedger(ctx, "Freeze Dr", "Current Assets")
	if err != nil {
		t.Fatalf("create dr ledger: %v", err)
	}
	cr, err := ids.CreateLedger(ctx, "Freeze Cr", "Current Liabilities")
	if err != nil {
		t.Fatalf("create cr ledger: %v", err)
	}

	led := ledger.NewService(pool)
	res, err := led.PostVoucher(ctx, mkJournal(vtID, dr, cr, no, amount))
	if err != nil {
		t.Fatalf("post journal: %v", err)
	}
	return led, ctx, res.VoucherID
}

// TestRegisterExternalReference_PinsAndIsIdempotent verifies a register pins the
// current display number and a re-register with the same (kind,ref) returns the
// same number and writes no second row.
func TestRegisterExternalReference_PinsAndIsIdempotent(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	led, ctx, vid := freezeFixture(t, "FRZ-1", "500.0000")

	r1, err := led.RegisterExternalReference(ctx, vid, "IRN", "IRN-ABC-123", "statutoryd")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if r1.PinnedVoucherNo != "FRZ-1" {
		t.Fatalf("pinned number = %q, want FRZ-1", r1.PinnedVoucherNo)
	}
	if r1.Kind != "IRN" || r1.Ref != "IRN-ABC-123" {
		t.Fatalf("unexpected result %+v", r1)
	}

	// Idempotent re-register: same number, no error, no panic on a second row.
	r2, err := led.RegisterExternalReference(ctx, vid, "IRN", "IRN-ABC-123", "statutoryd")
	if err != nil {
		t.Fatalf("re-register: %v", err)
	}
	if r2.PinnedVoucherNo != r1.PinnedVoucherNo {
		t.Fatalf("re-register pinned %q, want %q (must be stable)", r2.PinnedVoucherNo, r1.PinnedVoucherNo)
	}
}

// TestRegisterExternalReference_UnknownVoucher verifies an unknown voucher id is
// ErrVoucherNotFound (the HTTP layer maps it to 404).
func TestRegisterExternalReference_UnknownVoucher(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	led, ctx, _ := freezeFixture(t, "FRZ-NF", "100.0000")

	unknown, _ := uuid.NewV7()
	_, err := led.RegisterExternalReference(ctx, unknown, "IRN", "IRN-X", "")
	if !errors.Is(err, ledger.ErrVoucherNotFound) {
		t.Fatalf("expected ErrVoucherNotFound, got %v", err)
	}
}

// TestAlterFrozenVoucher_BlockedAndRaisesRecon verifies altering a frozen
// voucher returns *FrozenError, leaves the current version unchanged (no new
// version), and raises a reconciliation row (ALTER).
func TestAlterFrozenVoucher_BlockedAndRaisesRecon(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	led, ctx, vid := freezeFixture(t, "FRZ-A", "700.0000")

	if _, err := led.RegisterExternalReference(ctx, vid, "IRN", "IRN-ALTER", "statutoryd"); err != nil {
		t.Fatalf("register: %v", err)
	}

	before, err := led.GetVoucher(ctx, vid)
	if err != nil {
		t.Fatalf("get before: %v", err)
	}

	// Carry the existing head's type/number forward with a changed amount so the
	// only difference is the (refused) edit.
	in := ledger.VoucherInput{
		Class:         ledger.Journal,
		VoucherTypeID: before.VoucherTypeID,
		VoucherNo:     before.VoucherNo,
		VoucherDate:   before.VoucherDate,
		Narration:     "attempted alter",
		CreatedBy:     "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: before.Postings[0].LedgerID, DrCr: ledger.DrCr(before.Postings[0].DrCr), Amount: decimal.RequireFromString("999.0000")},
			{LedgerID: before.Postings[1].LedgerID, DrCr: ledger.DrCr(before.Postings[1].DrCr), Amount: decimal.RequireFromString("999.0000")},
		},
	}
	_, err = led.AlterVoucher(ctx, vid, in)
	var fe *ledger.FrozenError
	if !errors.As(err, &fe) {
		t.Fatalf("expected *FrozenError, got %v", err)
	}
	if fe.Kind != "IRN" || fe.Ref != "IRN-ALTER" || fe.VoucherNo != "FRZ-A" {
		t.Fatalf("FrozenError = %+v, want IRN/IRN-ALTER/FRZ-A", fe)
	}

	// Current version unchanged: same version_no, same number.
	after, err := led.GetVoucher(ctx, vid)
	if err != nil {
		t.Fatalf("get after: %v", err)
	}
	if after.VersionNo != before.VersionNo {
		t.Fatalf("version changed: before %d, after %d (must be unchanged)", before.VersionNo, after.VersionNo)
	}
	if after.VoucherNo != before.VoucherNo {
		t.Fatalf("voucher_no changed: before %q after %q", before.VoucherNo, after.VoucherNo)
	}

	// A reconciliation row exists for this voucher (ALTER).
	entries, err := led.ListReconciliationQueue(ctx)
	if err != nil {
		t.Fatalf("list recon: %v", err)
	}
	if !hasRecon(entries, vid, "ALTER", "IRN", "FRZ-A") {
		t.Fatalf("no ALTER reconciliation row for %s; got %+v", vid, entries)
	}
}

// TestDeleteFrozenVoucher_BlockedAndRaisesRecon verifies deleting a frozen
// voucher returns *FrozenError and raises a reconciliation row (DELETE).
func TestDeleteFrozenVoucher_BlockedAndRaisesRecon(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	led, ctx, vid := freezeFixture(t, "FRZ-D", "300.0000")

	if _, err := led.RegisterExternalReference(ctx, vid, "IRN", "IRN-DEL", "statutoryd"); err != nil {
		t.Fatalf("register: %v", err)
	}

	_, err := led.DeleteVoucher(ctx, vid, "tester")
	var fe *ledger.FrozenError
	if !errors.As(err, &fe) {
		t.Fatalf("expected *FrozenError on delete, got %v", err)
	}

	// Still alive (not tombstoned): a GET still succeeds.
	if _, err := led.GetVoucher(ctx, vid); err != nil {
		t.Fatalf("frozen voucher must remain live after blocked delete: %v", err)
	}

	entries, err := led.ListReconciliationQueue(ctx)
	if err != nil {
		t.Fatalf("list recon: %v", err)
	}
	if !hasRecon(entries, vid, "DELETE", "IRN", "FRZ-D") {
		t.Fatalf("no DELETE reconciliation row for %s; got %+v", vid, entries)
	}
}

// TestNonFrozenVoucher_AltersAndDeletesFine verifies the gate only blocks frozen
// vouchers: an unfrozen voucher still alters and deletes normally.
func TestNonFrozenVoucher_AltersAndDeletesFine(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	led, ctx, vid := freezeFixture(t, "FREE-1", "250.0000")

	before, _ := led.GetVoucher(ctx, vid)
	in := ledger.VoucherInput{
		Class:         ledger.Journal,
		VoucherTypeID: before.VoucherTypeID,
		VoucherNo:     before.VoucherNo,
		VoucherDate:   before.VoucherDate,
		Narration:     "ordinary alter",
		CreatedBy:     "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: before.Postings[0].LedgerID, DrCr: ledger.DrCr(before.Postings[0].DrCr), Amount: decimal.RequireFromString("260.0000")},
			{LedgerID: before.Postings[1].LedgerID, DrCr: ledger.DrCr(before.Postings[1].DrCr), Amount: decimal.RequireFromString("260.0000")},
		},
	}
	res, err := led.AlterVoucher(ctx, vid, in)
	if err != nil {
		t.Fatalf("alter unfrozen voucher: %v", err)
	}
	if res.VersionNo != before.VersionNo+1 {
		t.Fatalf("alter should bump version to %d, got %d", before.VersionNo+1, res.VersionNo)
	}

	if _, err := led.DeleteVoucher(ctx, vid, "tester"); err != nil {
		t.Fatalf("delete unfrozen voucher: %v", err)
	}
	if _, err := led.GetVoucher(ctx, vid); !errors.Is(err, ledger.ErrVoucherNotFound) {
		t.Fatalf("deleted voucher should be 404/NotFound, got %v", err)
	}

	// No reconciliation rows for an ordinary edit/delete.
	entries, err := led.ListReconciliationQueue(ctx)
	if err != nil {
		t.Fatalf("list recon: %v", err)
	}
	if hasRecon(entries, vid, "ALTER", "IRN", "FREE-1") || hasRecon(entries, vid, "DELETE", "IRN", "FREE-1") {
		t.Fatalf("unfrozen edit must not raise a reconciliation row; got %+v", entries)
	}
}

func hasRecon(entries []ledger.ReconEntry, vid uuid.UUID, op, kind, frozenNo string) bool {
	for _, e := range entries {
		if e.VoucherID == vid && e.AttemptedOp == op && e.Kind == kind && e.FrozenVoucherNo == frozenNo {
			return true
		}
	}
	return false
}
