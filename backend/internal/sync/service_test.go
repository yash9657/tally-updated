package sync_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	syncpkg "github.com/khata/cored/internal/sync"
	"github.com/khata/cored/migrations"
)

const (
	adminDSN = "postgres://postgres:postgres@localhost:5433/tally?sslmode=disable"
	appDSN   = "postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable"
)

// newSyncCompany is setupSync without the t.Cleanup registration: callers that
// create hundreds of companies in a loop must release each one as it finishes
// rather than accumulating 400 companies and 400 pools until the test ends.
//
// This package is the single largest producer of test residue in the backend:
// TestFuzzPartition runs numSeeds seeds, each seed builds one env, and the
// determinism re-run inside every seed builds a SECOND one — 2x200 = 400 fully
// seeded companies (each with its own account_group tree, ledgers and voucher
// types) surviving to the end of the run, plus 400 open pgx pools. Registering
// the release on the top-level *testing.T is what made that pile-up invisible:
// t.Cleanup on a subtest's T runs when the SUBTEST ends, but setupSync was
// called with whichever T the seed happened to have, and the pool close was the
// only thing it ever released. Returning an explicit release lets the caller
// decide the lifetime, and the fuzz loop makes it exactly one seed long.
//
// The release is deliberately ordered DropNow-then-Close: the drop runs on
// testdb's own admin pool and does not use this pool at all, but closing first
// would leave a window where a caller could still hold *pool while the tenant's
// rows are gone, which reads as a confusing "0 rows" rather than a use-after-
// close. Neither half is skipped when the other fails — testdb.DropNow reports
// through t.Errorf rather than t.Fatalf precisely so the pool still gets closed.
func newSyncCompany(t *testing.T) (*db.Pool, context.Context, uuid.UUID, *identity.Service, func()) {
	t.Helper()
	ctx := context.Background()

	adminConn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable, skipping sync test: %v", err)
	}
	if err := migrations.Apply(ctx, adminConn); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("apply migrations: %v", err)
	}
	_ = adminConn.Close(ctx)

	pool, err := db.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}

	companyID, err := uuid.NewV7()
	if err != nil {
		pool.Close()
		t.Fatalf("new company id: %v", err)
	}
	cctx := db.WithCompany(ctx, companyID)

	ids := identity.NewService(pool)
	if _, err := ids.CreateCompany(cctx, identity.Company{
		ID:          companyID,
		Name:        "Sync Co " + companyID.String()[:8],
		FYStartDate: "2025-04-01",
	}); err != nil {
		pool.Close()
		t.Fatalf("create company: %v", err)
	}

	release := func() {
		testdb.DropNow(t, companyID)
		pool.Close()
	}
	return pool, cctx, companyID, ids, release
}

// setupSync mirrors the ledger harness: it migrates the live DB (idempotent,
// now including 0010), opens an app-scoped pool, creates a freshly seeded
// company, and returns the pool, scoped ctx, company id, and identity service.
// Helpers are package-private to ledger_test, so the shape is copied here.
//
// Use this from ordinary one-company tests. Anything that mints companies in a
// LOOP must call newSyncCompany and release each one itself; see the comment
// there for why t.Cleanup is the wrong lifetime in that case.
func setupSync(t *testing.T) (*db.Pool, context.Context, uuid.UUID, *identity.Service) {
	t.Helper()
	pool, cctx, cid, ids, release := newSyncCompany(t)
	t.Cleanup(release)
	return pool, cctx, cid, ids
}

// journalFixture seeds a Journal voucher type + two balanced ledgers and returns
// them for building intents.
func journalFixture(t *testing.T, ctx context.Context, ids *identity.Service) (vtID, dr, cr uuid.UUID) {
	t.Helper()
	var err error
	vtID, err = ids.VoucherTypeID(ctx, "Journal")
	if err != nil {
		t.Fatalf("voucher type: %v", err)
	}
	dr, err = ids.CreateLedger(ctx, "Sync Dr "+uuid.NewString()[:8], "Current Assets")
	if err != nil {
		t.Fatalf("create dr: %v", err)
	}
	cr, err = ids.CreateLedger(ctx, "Sync Cr "+uuid.NewString()[:8], "Current Liabilities")
	if err != nil {
		t.Fatalf("create cr: %v", err)
	}
	return vtID, dr, cr
}

// journalPayload builds a balanced two-line Journal voucher body with an EMPTY
// voucher_no (the server numbers it). vtID is left nil so the create path
// resolves the company default — exactly the offline-sync shape.
func journalPayload(dr, cr uuid.UUID, amount string) ledger.VoucherInput {
	a := decimal.RequireFromString(amount)
	return ledger.VoucherInput{
		Class:       ledger.Journal,
		VoucherDate: "2025-05-20",
		Narration:   "sync fixture",
		CreatedBy:   "node",
		Postings: []ledger.PostingInput{
			{LedgerID: dr, DrCr: ledger.Debit, Amount: a},
			{LedgerID: cr, DrCr: ledger.Credit, Amount: a},
		},
	}
}

func hlc(node string, wall, ctr int64) syncpkg.HLCTimestamp {
	return syncpkg.HLCTimestamp{WallMillis: wall, Counter: ctr, NodeID: node}
}

// TestTwoOfflineCreatesGetDistinctNumbers proves zero-collision numbering: two
// offline CREATEs from different nodes commit to two DISTINCT sequential final
// numbers, both COMMITTED.
func TestTwoOfflineCreatesGetDistinctNumbers(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	pool, ctx, _, ids := setupSync(t)
	_, dr, cr := journalFixture(t, ctx, ids)
	led := ledger.NewService(pool)
	seq := syncpkg.NewService(led, pool)

	idA, _ := uuid.NewV7()
	idB, _ := uuid.NewV7()
	intents := []syncpkg.Intent{
		{IdempotencyKey: "A", ClientVoucherID: idA, Op: syncpkg.OpCreate, Class: ledger.Journal,
			HLC: hlc("nodeA", 100, 0), NodeID: "nodeA", Payload: journalPayload(dr, cr, "100.0000")},
		{IdempotencyKey: "B", ClientVoucherID: idB, Op: syncpkg.OpCreate, Class: ledger.Journal,
			HLC: hlc("nodeB", 101, 0), NodeID: "nodeB", Payload: journalPayload(dr, cr, "200.0000")},
	}
	res, err := seq.Commit(ctx, intents)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if len(res.Outcomes) != 2 {
		t.Fatalf("want 2 outcomes, got %d", len(res.Outcomes))
	}
	for _, o := range res.Outcomes {
		if o.Status != syncpkg.StatusCommitted {
			t.Fatalf("want COMMITTED, got %s (%s)", o.Status, o.Detail)
		}
		if o.FinalVoucherNo == "" {
			t.Fatalf("committed create must have a final number; got empty")
		}
	}
	if res.Outcomes[0].FinalVoucherNo == res.Outcomes[1].FinalVoucherNo {
		t.Fatalf("collision: both creates got %q", res.Outcomes[0].FinalVoucherNo)
	}
}

// TestReCommitIsDuplicate proves exactly-once replay: re-Committing the SAME
// batch yields all StatusDuplicate, no new versions, and no number burned.
func TestReCommitIsDuplicate(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	pool, ctx, _, ids := setupSync(t)
	_, dr, cr := journalFixture(t, ctx, ids)
	led := ledger.NewService(pool)
	seq := syncpkg.NewService(led, pool)

	idA, _ := uuid.NewV7()
	intents := []syncpkg.Intent{
		{IdempotencyKey: "dup-A", ClientVoucherID: idA, Op: syncpkg.OpCreate, Class: ledger.Journal,
			HLC: hlc("nodeA", 100, 0), NodeID: "nodeA", Payload: journalPayload(dr, cr, "100.0000")},
	}
	first, err := seq.Commit(ctx, intents)
	if err != nil {
		t.Fatalf("first commit: %v", err)
	}
	firstNo := first.Outcomes[0].FinalVoucherNo
	if first.Outcomes[0].Status != syncpkg.StatusCommitted {
		t.Fatalf("first must be COMMITTED, got %s", first.Outcomes[0].Status)
	}

	headBefore, _, _ := led.HeadVersion(ctx, idA)
	counterBefore := readCounter(t, pool, ctx)

	second, err := seq.Commit(ctx, intents)
	if err != nil {
		t.Fatalf("second commit: %v", err)
	}
	if second.Outcomes[0].Status != syncpkg.StatusDuplicate {
		t.Fatalf("re-commit must be DUPLICATE, got %s", second.Outcomes[0].Status)
	}
	if second.Outcomes[0].FinalVoucherNo != firstNo {
		t.Fatalf("duplicate must echo the same number %q, got %q", firstNo, second.Outcomes[0].FinalVoucherNo)
	}

	headAfter, _, _ := led.HeadVersion(ctx, idA)
	if headAfter != headBefore {
		t.Fatalf("duplicate must not bump head version: before %d after %d", headBefore, headAfter)
	}
	if counterAfter := readCounter(t, pool, ctx); counterAfter != counterBefore {
		t.Fatalf("duplicate must not burn a number: counter before %d after %d", counterBefore, counterAfter)
	}
}

// TestConcurrentAlterPicksHLCMinWinner proves deterministic conflict resolution:
// two ALTERs of the same voucher with the same BaseVersion=1 — the (HLC,node)-min
// wins (COMMITTED, head v2), the other is CONFLICT, PARKED with its payload, and
// the head stays at v2.
func TestConcurrentAlterPicksHLCMinWinner(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	pool, ctx, _, ids := setupSync(t)
	_, dr, cr := journalFixture(t, ctx, ids)
	led := ledger.NewService(pool)
	seq := syncpkg.NewService(led, pool)

	// Commit a base CREATE so there is a v1 to ALTER.
	target, _ := uuid.NewV7()
	if _, err := seq.Commit(ctx, []syncpkg.Intent{{
		IdempotencyKey: "base", ClientVoucherID: target, Op: syncpkg.OpCreate, Class: ledger.Journal,
		HLC: hlc("nodeS", 1, 0), NodeID: "nodeS", Payload: journalPayload(dr, cr, "500.0000"),
	}}); err != nil {
		t.Fatalf("base create: %v", err)
	}

	// Two ALTERs, same BaseVersion=1. winner has the smaller HLC.
	winnerHLC := hlc("nodeW", 50, 0)
	loserHLC := hlc("nodeL", 60, 0)
	alterW := journalPayload(dr, cr, "600.0000")
	alterL := journalPayload(dr, cr, "700.0000")
	intents := []syncpkg.Intent{
		// Submit loser FIRST to prove sort (not arrival) decides the winner.
		{IdempotencyKey: "loser", ClientVoucherID: target, Op: syncpkg.OpAlter, Class: ledger.Journal,
			BaseVersion: 1, HLC: loserHLC, NodeID: "nodeL", Payload: alterL},
		{IdempotencyKey: "winner", ClientVoucherID: target, Op: syncpkg.OpAlter, Class: ledger.Journal,
			BaseVersion: 1, HLC: winnerHLC, NodeID: "nodeW", Payload: alterW},
	}
	res, err := seq.Commit(ctx, intents)
	if err != nil {
		t.Fatalf("commit alters: %v", err)
	}

	byKey := map[string]syncpkg.Outcome{}
	for _, o := range res.Outcomes {
		byKey[o.IdempotencyKey] = o
	}
	if byKey["winner"].Status != syncpkg.StatusCommitted {
		t.Fatalf("winner must be COMMITTED, got %s", byKey["winner"].Status)
	}
	if byKey["winner"].VersionNo != 2 {
		t.Fatalf("winner head must be v2, got %d", byKey["winner"].VersionNo)
	}
	if byKey["loser"].Status != syncpkg.StatusConflict {
		t.Fatalf("loser must be CONFLICT, got %s", byKey["loser"].Status)
	}
	if byKey["loser"].ReconciliationID == "" {
		t.Fatalf("CONFLICT loser must carry a reconciliation id")
	}

	// Head stays at v2 (the loser did not apply).
	headVer, exists, _ := led.HeadVersion(ctx, target)
	if !exists || headVer != 2 {
		t.Fatalf("head must remain v2, got exists=%v ver=%d", exists, headVer)
	}

	// The loser's payload round-trips from the parked reconciliation row.
	reconID := uuid.MustParse(byKey["loser"].ReconciliationID)
	got, ok, err := seq.ConflictPayload(ctx, reconID)
	if err != nil {
		t.Fatalf("conflict payload: %v", err)
	}
	if !ok {
		t.Fatalf("parked conflict must preserve its payload")
	}
	if got.IdempotencyKey != "loser" || got.ClientVoucherID != target || got.Op != syncpkg.OpAlter {
		t.Fatalf("preserved payload mismatch: %+v", got)
	}

	// The recon row is kind=CONFLICT, surfaced via ListConflicts.
	entries, err := seq.ListConflicts(ctx)
	if err != nil {
		t.Fatalf("list conflicts: %v", err)
	}
	var foundConflict bool
	for _, e := range entries {
		if e.VoucherID == target && e.Kind == "CONFLICT" && e.AttemptedOp == "ALTER" {
			foundConflict = true
		}
	}
	if !foundConflict {
		t.Fatalf("no kind=CONFLICT recon row for the loser; got %+v", entries)
	}
}

// TestSupersedeFrozenVoucherIsHardBlocked proves the freeze hard-block: an
// ALTER/DELETE against an IRN-frozen voucher returns StatusFrozen, the voucher is
// unchanged, and a freeze recon row (kind=IRN) is present.
func TestSupersedeFrozenVoucherIsHardBlocked(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	pool, ctx, _, ids := setupSync(t)
	_, dr, cr := journalFixture(t, ctx, ids)
	led := ledger.NewService(pool)
	seq := syncpkg.NewService(led, pool)

	target, _ := uuid.NewV7()
	base, err := seq.Commit(ctx, []syncpkg.Intent{{
		IdempotencyKey: "frz-base", ClientVoucherID: target, Op: syncpkg.OpCreate, Class: ledger.Journal,
		HLC: hlc("nodeS", 1, 0), NodeID: "nodeS", Payload: journalPayload(dr, cr, "800.0000"),
	}})
	if err != nil {
		t.Fatalf("base create: %v", err)
	}
	frozenNo := base.Outcomes[0].FinalVoucherNo

	// Pin an IRN (Task B freeze).
	if _, err := led.RegisterExternalReference(ctx, target, "IRN", "IRN-SYNC-1", "statutoryd"); err != nil {
		t.Fatalf("register external reference: %v", err)
	}

	// An ALTER (BaseVersion=1, matches head) is hard-blocked by the freeze rule.
	res, err := seq.Commit(ctx, []syncpkg.Intent{{
		IdempotencyKey: "frz-alter", ClientVoucherID: target, Op: syncpkg.OpAlter, Class: ledger.Journal,
		BaseVersion: 1, HLC: hlc("nodeX", 5, 0), NodeID: "nodeX", Payload: journalPayload(dr, cr, "999.0000"),
	}})
	if err != nil {
		t.Fatalf("commit frozen alter: %v", err)
	}
	if res.Outcomes[0].Status != syncpkg.StatusFrozen {
		t.Fatalf("supersede of frozen voucher must be FROZEN, got %s (%s)", res.Outcomes[0].Status, res.Outcomes[0].Detail)
	}

	// Voucher unchanged: still v1, still the pinned number.
	v, err := led.GetVoucher(ctx, target)
	if err != nil {
		t.Fatalf("get frozen voucher: %v", err)
	}
	if v.VersionNo != 1 || v.VoucherNo != frozenNo {
		t.Fatalf("frozen voucher changed: ver=%d no=%q (want 1/%q)", v.VersionNo, v.VoucherNo, frozenNo)
	}

	// A freeze recon row (kind=IRN) exists.
	entries, _ := led.ListReconciliationQueue(ctx)
	var foundFreeze bool
	for _, e := range entries {
		if e.VoucherID == target && e.Kind == "IRN" && e.AttemptedOp == "ALTER" {
			foundFreeze = true
		}
	}
	if !foundFreeze {
		t.Fatalf("no IRN freeze recon row for %s; got %+v", target, entries)
	}
}

// readCounter sums the voucher_number_counter next_no across the company's series
// — a proxy for "numbers burned". A duplicate replay must leave it unchanged.
func readCounter(t *testing.T, pool *db.Pool, ctx context.Context) int64 {
	t.Helper()
	companyID, _ := db.CompanyFromContext(ctx)
	var sum int64
	err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT COALESCE(SUM(next_no), 0) FROM voucher_number_counter WHERE company_id = $1`,
			companyID).Scan(&sum)
	})
	if err != nil {
		t.Fatalf("read counter: %v", err)
	}
	return sum
}
