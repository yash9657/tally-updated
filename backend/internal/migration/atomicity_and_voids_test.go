package migration_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/migration"
	"github.com/khata/cored/internal/platform/db"
)

// liveVouchers counts the LIVE voucher heads in the company in ctx. It is the
// measurement both defects below turn on, and it deliberately reads the table
// rather than the ImportResult: the whole point of D98 was that the RESULT and
// the DATABASE disagreed.
func liveVouchers(t *testing.T, pool *db.Pool, ctx context.Context) int {
	t.Helper()
	var n int
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM voucher_head WHERE is_deleted = false`).Scan(&n)
	}); err != nil {
		t.Fatalf("count vouchers: %v", err)
	}
	return n
}

// TestARefusedImportCommitsNothing is D98's mutation proof.
//
// ⛔ THE FAILURE THIS PREDICTS, AND IT WAS OBSERVED BEFORE THE FIX: the fixture's
// THIRD voucher carries "<AMOUNT>$ 1000.00</AMOUNT>", which no money reader will
// take. The import is refused with a good sentence naming the voucher, the
// ledger and the value — and vouchers ONE and TWO were in the books, visible in
// the operator's own Day Book, with nothing on any screen saying so.
//
// ⭐ THE ASSERTION IS THE WHOLE FIX: after a refusal the company holds ZERO
// vouchers. Not "two, and we told you" — none.
func TestARefusedImportCommitsNothing(t *testing.T) {
	pool, ctx, _ := setup(t)
	root := repoRoot(t)
	im := migration.NewImporter(masters.NewService(pool), ledger.NewService(pool), pool)

	res, err := im.ImportFile(ctx,
		filepath.Join(root, "corpora", "migration", "08-refusal-decorated-amount.xml"), "tester")
	if err == nil {
		t.Fatal("a file whose third voucher carries '$ 1000.00' was IMPORTED. The money readers " +
			"refuse a decorated amount, so this means the refusal stopped happening — check that " +
			"before assuming the atomicity fix is what changed")
	}
	// The refusal must still be the SAME good sentence. Atomicity that costs the
	// operator the diagnosis is not an improvement.
	if !strings.Contains(err.Error(), "voucher 3") {
		t.Errorf("the refusal no longer names the offending voucher ordinal: %v", err)
	}

	if got := liveVouchers(t, pool, ctx); got != 0 {
		t.Fatalf("⛔ D98: a REFUSED import left %d voucher(s) in the books. The whole voucher pass "+
			"runs in one transaction precisely so that a refusal leaves the books as they were; "+
			"%d rows means the rollback did not reach them", got, got)
	}
	// And the result must not claim posts the rollback took back.
	if res.VouchersPosted != 0 {
		t.Errorf("⛔ the ImportResult reports %d vouchers posted after a rollback that removed "+
			"them. A caller rendering this would be reporting vouchers that do not exist — the "+
			"same silence D98 is about", res.VouchersPosted)
	}
}

// TestTheNaturalRecoveryDoesNotDoublePost is the OTHER half of D98, and it is
// the half that costs money.
//
// ⛔ MEASURED BEFORE THE FIX: refuse on the decorated amount, fix the file, import
// again — and a GUID-LESS export ended with FIVE vouchers. The two that landed
// during the refused run were posted a SECOND time, real money duplicated, with
// no delete path anywhere in this product. GUID-less is the blast radius on
// purpose: hand-built exports, a Day Book export, anything a migration
// consultant assembles. A GUID-bearing file was always saved by the Create arm's
// idempotency check, which is why this fixture carries no GUIDs.
func TestTheNaturalRecoveryDoesNotDoublePost(t *testing.T) {
	pool, ctx, _ := setup(t)
	root := repoRoot(t)
	fx := func(n string) string { return filepath.Join(root, "corpora", "migration", n) }
	im := migration.NewImporter(masters.NewService(pool), ledger.NewService(pool), pool)

	if _, err := im.ImportFile(ctx, fx("08-refusal-decorated-amount.xml"), "tester"); err == nil {
		t.Fatal("the decorated-amount file imported cleanly; this test can prove nothing")
	}
	res, err := im.ImportFile(ctx, fx("08-refusal-corrected.xml"), "tester")
	if err != nil {
		t.Fatalf("the CORRECTED file was refused: %v", err)
	}

	got := liveVouchers(t, pool, ctx)
	if got != res.VouchersPosted {
		t.Errorf("the report says %d posted and the books hold %d", res.VouchersPosted, got)
	}
	if got != 3 {
		t.Fatalf("⛔ D98: after a refusal and a corrected re-import the books hold %d vouchers, "+
			"want 3. FIVE is the pre-fix number and means the two vouchers from the refused run "+
			"were committed and then posted a second time — duplicated money with no delete path", got)
	}
}

// TestVoucherFlagsTallyExcludesFromTheBooks is D101's proof.
//
// ⛔ WHAT IT REPORTED BEFORE THE FIX: "Vouchers posted: 3", "Warnings (0)", and
// both voided vouchers in the operator's own Day Book beside the real one —
// 30,000.00 of sales the source books show as VOIDED, and a provisional Journal
// the source books exclude by definition. Tally keeps both out (RESEARCH.md:2225
// — only Cash Flow's "Include Scenarios" pulls optional entries in), and once
// they are in there is nothing on any screen that tells them from real money.
func TestVoucherFlagsTallyExcludesFromTheBooks(t *testing.T) {
	pool, ctx, _ := setup(t)
	root := repoRoot(t)
	im := migration.NewImporter(masters.NewService(pool), ledger.NewService(pool), pool)

	res, err := im.ImportFile(ctx,
		filepath.Join(root, "corpora", "migration", "10-voided-vouchers.xml"), "tester")
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	// ⭐ THE CONTROL MATTERS: the fixture's first voucher is an ordinary one, so
	// "nothing imported" cannot be mistaken for "the voided ones were skipped".
	if res.VouchersPosted != 1 {
		t.Errorf("⛔ D101: %d vouchers posted, want 1 (the live Receipt only). 3 is the pre-fix "+
			"number and means the cancelled Sales and the optional Journal went into the books",
			res.VouchersPosted)
	}
	if got := liveVouchers(t, pool, ctx); got != 1 {
		t.Errorf("⛔ D101: the books hold %d vouchers, want 1", got)
	}

	// ⛔ AND IT MUST BE REPORTED, NOT SILENTLY DROPPED. A skip an operator cannot
	// see is the same defect wearing the other mask.
	if res.VouchersVoided != 2 {
		t.Fatalf("⛔ D101: VouchersVoided = %d, want 2. An import that excludes money without "+
			"saying so is not better than one that includes it without saying so", res.VouchersVoided)
	}
	if res.Skipped != 0 {
		t.Errorf("⛔ voided vouchers were counted in Skipped, which means \"a voucher with this "+
			"GUID was already imported\". An operator reading that would think they were duplicates")
	}
	joined := strings.Join(res.VoidedVouchers, "\n")
	for _, want := range []string{"cancelled", "optional"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the report does not name a %s voucher — an operator reconciling this "+
				"migration against Tally has to find them. It said:\n%s", want, joined)
		}
	}
	// The ISDELETED ledger master must not have been created either.
	if len(res.LedgersTombstoned) == 0 {
		t.Errorf("⛔ D101: the fixture's ISDELETED ledger master was created. Five other master " +
			"kinds already honour this flag")
	}
}
