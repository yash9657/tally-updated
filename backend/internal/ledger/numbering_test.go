package ledger_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/platform/db"
)

// setVoucherTypeNumbering overrides numbering_method/prefix/suffix/width on a
// voucher type so the DB-backed tests assert a deterministic format/sequence.
func setVoucherTypeNumbering(t *testing.T, pool *db.Pool, ctx context.Context, vtID uuid.UUID, method, prefix string, width *int) {
	t.Helper()
	err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE voucher_type SET numbering_method=$2, prefix=$3, suffix='', width=$4 WHERE id=$1`,
			vtID, method, prefix, width)
		return err
	})
	if err != nil {
		t.Fatalf("set numbering: %v", err)
	}
}

// counterNext reads next_no for a series; returns -1 if the row is absent.
func counterNext(t *testing.T, pool *db.Pool, ctx context.Context, vtID uuid.UUID, finYear int) int64 {
	t.Helper()
	var n int64 = -1
	err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var v int64
		err := tx.QueryRow(ctx,
			`SELECT next_no FROM voucher_number_counter
			  WHERE voucher_type_id=$1 AND fin_year=$2`, vtID, finYear).Scan(&v)
		if err == pgx.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		n = v
		return nil
	})
	if err != nil {
		t.Fatalf("read counter: %v", err)
	}
	return n
}

// numberingFixture sets up a Sales voucher type with the given numbering config
// and a debtor/sales ledger pair, returning a maker for balanced inputs.
func numberingFixture(t *testing.T, method, prefix string, width *int) (
	*ledger.Service, *db.Pool, context.Context, uuid.UUID,
	func(no, date string) ledger.VoucherInput,
) {
	t.Helper()
	pool, ctx, _, ids := setup(t)
	vtID, err := ids.VoucherTypeID(ctx, "Sales")
	if err != nil {
		t.Fatalf("voucher type: %v", err)
	}
	setVoucherTypeNumbering(t, pool, ctx, vtID, method, prefix, width)

	debtor, _ := ids.CreateLedger(ctx, "Numbering Debtor", "Sundry Debtors")
	sales, _ := ids.CreateLedger(ctx, "Numbering Sales", "Sales Accounts")
	led := ledger.NewService(pool)

	mk := func(no, date string) ledger.VoucherInput {
		a := decimal.RequireFromString("100.0000")
		return ledger.VoucherInput{
			Class: ledger.Sales, VoucherTypeID: vtID, VoucherNo: no, VoucherDate: date,
			Narration: "n", Party: "Numbering Debtor", CreatedBy: "tester",
			Postings: []ledger.PostingInput{
				{LedgerID: debtor, DrCr: ledger.Debit, Amount: a},
				{LedgerID: sales, DrCr: ledger.Credit, Amount: a},
			},
		}
	}
	return led, pool, ctx, vtID, mk
}

func skipNoDB(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
}

// (a) Concurrency: two concurrent CREATEs of the same series get distinct
// consecutive numbers (no collision, no gap).
func TestNumbering_ConcurrentCreatesAreDistinctConsecutive(t *testing.T) {
	skipNoDB(t)
	w4 := 4
	led, _, ctx, _, mk := numberingFixture(t, "AUTOMATIC", "INV/", &w4)

	const n = 8
	results := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := led.PostVoucher(ctx, mk("", "2026-05-10"))
			errs[i] = err
			if err == nil {
				results[i] = res.VoucherNo
			}
		}(i)
	}
	wg.Wait()

	seen := map[string]bool{}
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("concurrent post %d: %v", i, errs[i])
		}
		if seen[results[i]] {
			t.Fatalf("duplicate voucher_no allocated: %q", results[i])
		}
		seen[results[i]] = true
	}
	// The set must be exactly INV/0001..INV/0008 (consecutive, gap-free).
	for i := 1; i <= n; i++ {
		want := fmt.Sprintf("INV/%04d", i)
		if !seen[want] {
			t.Fatalf("missing %q in allocated set %v", want, seen)
		}
	}
}

// (b) Alter preserves the number and does not bump the counter.
func TestNumbering_AlterPreservesNumber(t *testing.T) {
	skipNoDB(t)
	w4 := 4
	led, pool, ctx, vtID, mk := numberingFixture(t, "AUTOMATIC", "INV/", &w4)

	created, err := led.PostVoucher(ctx, mk("", "2026-05-10"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.VoucherNo != "INV/0001" {
		t.Fatalf("first number = %q, want INV/0001", created.VoucherNo)
	}
	before := counterNext(t, pool, ctx, vtID, 2026)

	// Alter with EMPTY voucher_no => must carry forward INV/0001.
	altIn := mk("", "2026-05-10")
	altered, err := led.AlterVoucher(ctx, created.VoucherID, altIn)
	if err != nil {
		t.Fatalf("alter: %v", err)
	}
	if altered.VoucherNo != "INV/0001" {
		t.Fatalf("altered number = %q, want INV/0001 (carry-forward)", altered.VoucherNo)
	}
	if altered.VersionNo != 2 {
		t.Fatalf("altered version_no = %d, want 2", altered.VersionNo)
	}
	if after := counterNext(t, pool, ctx, vtID, 2026); after != before {
		t.Fatalf("counter advanced on alter: before=%d after=%d", before, after)
	}
}

// (c) Delete leaves a gap: create A,B,C (1,2,3), delete B, create D => 4.
func TestNumbering_DeleteLeavesGap(t *testing.T) {
	skipNoDB(t)
	w4 := 4
	led, _, ctx, _, mk := numberingFixture(t, "AUTOMATIC", "INV/", &w4)

	a, _ := led.PostVoucher(ctx, mk("", "2026-05-10"))
	b, _ := led.PostVoucher(ctx, mk("", "2026-05-10"))
	c, _ := led.PostVoucher(ctx, mk("", "2026-05-10"))
	if a.VoucherNo != "INV/0001" || b.VoucherNo != "INV/0002" || c.VoucherNo != "INV/0003" {
		t.Fatalf("creates = %q,%q,%q want 0001,0002,0003", a.VoucherNo, b.VoucherNo, c.VoucherNo)
	}
	if _, err := led.DeleteVoucher(ctx, b.VoucherID, "tester"); err != nil {
		t.Fatalf("delete B: %v", err)
	}
	d, err := led.PostVoucher(ctx, mk("", "2026-05-10"))
	if err != nil {
		t.Fatalf("create D: %v", err)
	}
	if d.VoucherNo != "INV/0004" {
		t.Fatalf("D number = %q, want INV/0004 (no reuse of 0002)", d.VoucherNo)
	}
}

// (d) Manual override on an AUTOMATIC type is honored and does NOT bump the
// counter; a subsequent empty create gets the next AUTO number (not override+1).
func TestNumbering_ManualOverrideHonored(t *testing.T) {
	skipNoDB(t)
	w4 := 4
	led, pool, ctx, vtID, mk := numberingFixture(t, "AUTOMATIC", "INV/", &w4)

	first, _ := led.PostVoucher(ctx, mk("", "2026-05-10")) // INV/0001
	if first.VoucherNo != "INV/0001" {
		t.Fatalf("first = %q want INV/0001", first.VoucherNo)
	}
	before := counterNext(t, pool, ctx, vtID, 2026)

	ov, err := led.PostVoucher(ctx, mk("OVERRIDE/7", "2026-05-10"))
	if err != nil {
		t.Fatalf("override create: %v", err)
	}
	if ov.VoucherNo != "OVERRIDE/7" {
		t.Fatalf("override = %q, want OVERRIDE/7 (verbatim)", ov.VoucherNo)
	}
	if after := counterNext(t, pool, ctx, vtID, 2026); after != before {
		t.Fatalf("override bumped counter: before=%d after=%d", before, after)
	}

	next, err := led.PostVoucher(ctx, mk("", "2026-05-10"))
	if err != nil {
		t.Fatalf("post after override: %v", err)
	}
	if next.VoucherNo != "INV/0002" {
		t.Fatalf("post-override auto = %q, want INV/0002 (not 8)", next.VoucherNo)
	}
}

// (e) MANUAL method + empty voucher_no => validation error.
func TestNumbering_ManualMethodRequiresNumber(t *testing.T) {
	skipNoDB(t)
	led, _, ctx, _, mk := numberingFixture(t, "MANUAL", "", nil)

	_, err := led.PostVoucher(ctx, mk("", "2026-05-10"))
	if err == nil {
		t.Fatal("expected error for MANUAL type with empty voucher_no")
	}
	if !strings.Contains(err.Error(), "manual numbering requires a voucher number") {
		t.Fatalf("unexpected error: %v", err)
	}

	// MANUAL with a supplied number must succeed and echo verbatim.
	res, err := led.PostVoucher(ctx, mk("HAND/42", "2026-05-10"))
	if err != nil {
		t.Fatalf("manual with number: %v", err)
	}
	if res.VoucherNo != "HAND/42" {
		t.Fatalf("manual number = %q, want HAND/42", res.VoucherNo)
	}
}

// (f) FY restart: dates in different FYs draw from different counters (both 1).
func TestNumbering_FYRestart(t *testing.T) {
	skipNoDB(t)
	w4 := 4
	led, _, ctx, _, mk := numberingFixture(t, "AUTOMATIC", "INV/", &w4)

	// 2026-03-31 => FY 2025; 2026-04-01 => FY 2026. Both start at 1.
	prevFY, err := led.PostVoucher(ctx, mk("", "2026-03-31"))
	if err != nil {
		t.Fatalf("create prev FY: %v", err)
	}
	newFY, err := led.PostVoucher(ctx, mk("", "2026-04-01"))
	if err != nil {
		t.Fatalf("create new FY: %v", err)
	}
	if prevFY.VoucherNo != "INV/0001" {
		t.Fatalf("prev-FY number = %q, want INV/0001", prevFY.VoucherNo)
	}
	if newFY.VoucherNo != "INV/0001" {
		t.Fatalf("new-FY number = %q, want INV/0001 (separate counter)", newFY.VoucherNo)
	}
	// A second create in the new FY continues at 2 (proves separate series).
	newFY2, _ := led.PostVoucher(ctx, mk("", "2026-05-01"))
	if newFY2.VoucherNo != "INV/0002" {
		t.Fatalf("new-FY second = %q, want INV/0002", newFY2.VoucherNo)
	}
}
