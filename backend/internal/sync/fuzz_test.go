package sync_test

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/reporting"
	syncpkg "github.com/khata/cored/internal/sync"
)

// adminDSN / appDSN and the setupSync harness are declared in service_test.go
// (same package sync_test) and REUSED here — they are not re-declared.

const (
	// All fuzzer creates are pinned to ONE class + ONE FY so the per-(company,
	// type, FY) allocator yields a SINGLE series whose suffixes must be a gap-free
	// 1..N run (invariant 1). FY2025-26: 2025-04-01 .. 2026-03-31.
	fuzzClass = ledger.Journal
	fuzzDate  = "2025-06-15"
	fuzzFYTag = "2025-26"
	baseSeed  = 0xC0FFEE
	numSeeds  = 200
)

// fuzzEnv spins up a fresh seeded company and returns the scoped ctx, ledger,
// sync, and reporting services, plus two ledgers usable for balanced Journal lines.
//
// release tears the company back down and closes its pool. It is NOT registered
// with t.Cleanup by newFuzzEnv, and that is the point: see close().
type fuzzEnv struct {
	ctx     context.Context
	pool    *db.Pool
	led     *ledger.Service
	syncS   *syncpkg.Service
	rep     *reporting.Service
	cid     uuid.UUID
	vtID    uuid.UUID
	dr, cr  uuid.UUID
	release func()
}

// close releases the env's company and pool NOW, rather than at the end of the
// enclosing test.
//
// Why this exists rather than a t.Cleanup inside newFuzzEnv: TestFuzzPartition
// runs numSeeds (200) seeds, and every seed builds TWO envs — one for the main
// run and one inside assertSeedDeterminism, which must re-run the identical seed
// on a company whose voucher-number allocator starts at 1 again. Deferring the
// teardown to the test would keep 400 seeded companies and 400 pgx pools alive
// simultaneously; that is where 404 of the backend's 685 leaked companies per
// full run came from, and the pool count is why this package's DB errors under
// load looked like "unexpected EOF" rather than "too many connections".
//
// Reusing ONE company across seeds is NOT an option and must not be attempted
// as a "simpler" fix: assertGapFreeSeries, maxJournalNumber and journalNumberSet
// all assert that the company's Journal series is a gap-free 1..N run. Those
// oracles are only meaningful because each company's allocator starts at zero.
// Share a company between seeds and the series becomes 1..N+M with the previous
// seed's numbers in it, the gap check passes on data it never examined, and the
// gate quietly stops testing numbering at all. Per-seed teardown keeps the
// oracle honest AND keeps the residue at zero.
//
// close is called via defer, so it still runs when a seed fails through
// t.Fatalf: Fatalf is runtime.Goexit, which unwinds deferred calls.
func (e fuzzEnv) close() { e.release() }

// newFuzzEnv builds a fresh seeded company (its own pool + tenant) via
// newSyncCompany — deliberately NOT setupSync, whose t.Cleanup would defer the
// teardown to the end of the enclosing test. The caller owns the lifetime and
// MUST `defer e.close()`.
func newFuzzEnv(t *testing.T) fuzzEnv {
	t.Helper()
	pool, cctx, cid, ids, release := newSyncCompany(t)

	vtID, err := ids.VoucherTypeID(cctx, "Journal")
	if err != nil {
		release()
		t.Fatalf("voucher type: %v", err)
	}
	dr, err := ids.CreateLedger(cctx, "Fuzz Dr", "Current Assets")
	if err != nil {
		release()
		t.Fatalf("dr ledger: %v", err)
	}
	cr, err := ids.CreateLedger(cctx, "Fuzz Cr", "Current Liabilities")
	if err != nil {
		release()
		t.Fatalf("cr ledger: %v", err)
	}
	led := ledger.NewService(pool)
	return fuzzEnv{
		ctx:     cctx,
		pool:    pool,
		led:     led,
		syncS:   syncpkg.NewService(led, pool),
		rep:     reporting.NewService(pool),
		cid:     cid,
		vtID:    vtID,
		dr:      dr,
		cr:      cr,
		release: release,
	}
}

// journalPayload builds a balanced two-line Journal VoucherInput with an EMPTY
// voucher_no (the server numbers it) at the pinned class/FY date.
func (e fuzzEnv) journalPayload(amount string) ledger.VoucherInput {
	a := decimal.RequireFromString(amount)
	return ledger.VoucherInput{
		Class:         fuzzClass,
		VoucherTypeID: e.vtID,
		VoucherDate:   fuzzDate,
		Narration:     "fuzz",
		CreatedBy:     "fuzz",
		Postings: []ledger.PostingInput{
			{LedgerID: e.dr, DrCr: ledger.Debit, Amount: a},
			{LedgerID: e.cr, DrCr: ledger.Credit, Amount: a},
		},
	}
}

// commitAll commits a flat list of intents (already in a randomized arrival order)
// through the sequencer and returns the outcomes keyed by idempotency key.
func commitAll(t *testing.T, e fuzzEnv, intents []syncpkg.Intent, seed int64) map[string]syncpkg.Outcome {
	t.Helper()
	res, err := e.syncS.Commit(e.ctx, intents)
	if err != nil {
		t.Fatalf("seed %d: commit: %v", seed, err)
	}
	out := make(map[string]syncpkg.Outcome, len(res.Outcomes))
	for _, o := range res.Outcomes {
		out[o.IdempotencyKey] = o
	}
	return out
}

// TestFuzzPartition is the GATE: a seeded, reproducible, property-based
// network-partition fuzzer that drives WS1's sync.Service.Commit and asserts the
// six invariants. It is DB-backed; it skips when CORED_SKIP_DB is set.
func TestFuzzPartition(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}

	for s := 0; s < numSeeds; s++ {
		seed := int64(baseSeed + s)
		ok := t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runFuzzSeed(t, seed)
		})
		if !ok {
			// Surface the seed prominently so a single failure reproduces.
			t.Logf("FUZZ FAILED at seed %d (re-run: go test ./internal/sync -run 'TestFuzzPartition/seed=%d')", seed, seed)
			t.FailNow()
		}
	}
}

// committed tracks one COMMITTED create for the contiguity/uniqueness check.
type committedCreate struct {
	id     uuid.UUID
	number string
}

// confIntent is one intent in the forced same-target same-base conflict set.
type confIntent struct {
	key string
	hlc syncpkg.HLCTimestamp
}

// schedule is the deterministic, seed-derived plan: the arrival-ordered intents to
// commit, plus the bookkeeping needed to assert the invariants. It is built by
// buildSchedule so the main run and the determinism re-run are byte-identical.
type schedule struct {
	arrival        []syncpkg.Intent
	byKey          map[string]syncpkg.Intent
	conflictSet    []confIntent // forced same-target(base v1) intents — exactly one wins
	frozenKeys     []string     // intents superseding the IRN-frozen target — all FROZEN
	frozenTarget   uuid.UUID
	conflictTarget uuid.UUID
}

// buildSchedule deterministically constructs the whole partition schedule for a
// seed on the supplied fresh env. It commits dedicated base vouchers, IRN-freezes
// one, spins up nodes with varied wall-clock skew, generates a random op mix
// against a SCRATCH set (never the conflict/frozen targets), then FORCES the
// non-vacuity cases: >=2 nodes superseding the conflict target at base v1, and a
// node ALTER + a node DELETE against the frozen target. The conflict/frozen
// targets are touched ONLY by their forced blocks, so the forced conflict set's
// (HLC,node)-min is the true global winner and every frozen supersede is blocked.
func buildSchedule(t *testing.T, e fuzzEnv, seed int64) schedule {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))

	// --- Phase 0: dedicated base vouchers, committed to a known head v1. ---
	// One conflict target, one frozen target, and a scratch pool that random
	// Phase-1 edits may churn without disturbing the two forced targets.
	post := func(label int) uuid.UUID {
		res, err := e.led.PostVoucher(e.ctx, e.journalPayload(fmt.Sprintf("%d.0000", 100+label)))
		if err != nil {
			t.Fatalf("seed %d: post base voucher: %v", seed, err)
		}
		return res.VoucherID
	}
	conflictTarget := post(0)
	frozenTarget := post(1)
	numScratch := 1 + rng.Intn(2) // 1..2 scratch vouchers for random edits
	scratch := make([]uuid.UUID, 0, numScratch)
	for i := 0; i < numScratch; i++ {
		scratch = append(scratch, post(2+i))
	}
	freezeRef := fmt.Sprintf("IRN-%d", seed)

	// --- Build N offline nodes with VARIED wall-clock skew. ---
	numNodes := 3 + rng.Intn(4) // 3..6
	nodes := make([]*syncpkg.Node, numNodes)
	skews := make([]int64, numNodes)
	for i := range nodes {
		nodes[i] = syncpkg.NewNode(fmt.Sprintf("node-%c", 'A'+i), fuzzFYTag, uint64(seed)*100+uint64(i))
		skews[i] = int64(rng.Intn(5000)) - 2000 // -2000..+2999 ms skew
	}
	wallOf := func(i int, base int64) int64 { return base + skews[i] }

	var conflictSet []confIntent
	var frozenKeys []string
	baseWall := int64(1_700_000_000_000)

	// --- Phase 1 (offline): each node performs a random mix of operations. The
	// random ALTER/DELETE only ever target the SCRATCH set — NEVER the conflict or
	// frozen targets — so those two are governed solely by their forced blocks. ---
	for i, n := range nodes {
		ops := 2 + rng.Intn(4) // 2..5 ops per node
		for op := 0; op < ops; op++ {
			baseWall += int64(rng.Intn(50))
			w := wallOf(i, baseWall)
			switch rng.Intn(3) {
			case 0:
				// Fresh CREATE (pinned class/FY) — feeds the contiguity invariant.
				n.CreateVoucher(w, e.journalPayload(fmt.Sprintf("%d.0000", 10+op)))
			case 1:
				// ALTER a scratch voucher at base v1 (may go stale among scratch edits).
				n.AlterVoucher(w, scratch[rng.Intn(len(scratch))], 1, e.journalPayload(fmt.Sprintf("%d.0000", 20+op)))
			case 2:
				// DELETE a scratch voucher at base v1.
				n.DeleteVoucher(w, scratch[rng.Intn(len(scratch))], 1, fuzzClass)
			}
		}
	}

	// --- FORCED non-vacuity (corrections (a)): >=2 nodes ALTER/DELETE the SAME
	// conflictTarget at the SAME BaseVersion=1 => guaranteed stale conflict. Since
	// nothing else touches conflictTarget, exactly the (HLC,node)-min commits. ---
	confNodes := 2 + rng.Intn(numNodes-1) // at least 2
	for i := 0; i < confNodes; i++ {
		n := nodes[i]
		baseWall += int64(rng.Intn(50))
		w := wallOf(i, baseWall)
		if rng.Intn(2) == 0 {
			n.AlterVoucher(w, conflictTarget, 1, e.journalPayload(fmt.Sprintf("%d.0000", 500+i)))
		} else {
			n.DeleteVoucher(w, conflictTarget, 1, fuzzClass)
		}
		last := n.Outbox()[len(n.Outbox())-1]
		conflictSet = append(conflictSet, confIntent{key: last.IdempotencyKey, hlc: last.HLC})
	}

	// --- FORCED freeze (corrections (b)): IRN-pin the frozen target mid-schedule,
	// then have a node ALTER and another DELETE it => guaranteed StatusFrozen. ---
	if _, err := e.led.RegisterExternalReference(e.ctx, frozenTarget, "IRN", freezeRef, "fuzz"); err != nil {
		t.Fatalf("seed %d: register IRN: %v", seed, err)
	}
	{
		na := nodes[0]
		baseWall += 10
		na.AlterVoucher(wallOf(0, baseWall), frozenTarget, 1, e.journalPayload("777.0000"))
		frozenKeys = append(frozenKeys, na.Outbox()[len(na.Outbox())-1].IdempotencyKey)

		nb := nodes[1%numNodes]
		baseWall += 10
		nb.DeleteVoucher(wallOf(1%numNodes, baseWall), frozenTarget, 1, fuzzClass)
		frozenKeys = append(frozenKeys, nb.Outbox()[len(nb.Outbox())-1].IdempotencyKey)
	}

	// --- Collect every outbox, then shuffle into a RANDOMIZED arrival order. The
	// sequencer's (HLC,node) sort makes the result independent of arrival order. ---
	var arrival []syncpkg.Intent
	for _, n := range nodes {
		arrival = append(arrival, n.Outbox()...)
	}
	rng.Shuffle(len(arrival), func(a, b int) { arrival[a], arrival[b] = arrival[b], arrival[a] })

	byKey := make(map[string]syncpkg.Intent, len(arrival))
	for _, in := range arrival {
		byKey[in.IdempotencyKey] = in
	}
	return schedule{
		arrival:        arrival,
		byKey:          byKey,
		conflictSet:    conflictSet,
		frozenKeys:     frozenKeys,
		frozenTarget:   frozenTarget,
		conflictTarget: conflictTarget,
	}
}

func runFuzzSeed(t *testing.T, seed int64) {
	e := newFuzzEnv(t)
	// Release this seed's company as soon as the seed finishes — including when
	// it fails, since t.Fatalf unwinds defers. Combined with the matching defer
	// in assertSeedDeterminism (which overlaps this env for the length of the
	// re-run) peak concurrent companies is 2, not 400.
	defer e.close()
	sch := buildSchedule(t, e, seed)

	arrival := sch.arrival
	byKey := sch.byKey
	conflictSet := sch.conflictSet
	frozenKeys := sch.frozenKeys
	frozenTarget := sch.frozenTarget

	// ================= FIRST COMMIT =================
	outcomes := commitAll(t, e, arrival, seed)

	// ---- Invariant (2) DETERMINISTIC WINNER: the (HLC,node)-min of the conflict
	// set is the single COMMITTED; the rest are CONFLICT. ----
	if len(conflictSet) < 2 {
		t.Fatalf("seed %d: vacuity: conflict set has %d (<2) intents", seed, len(conflictSet))
	}
	wantWinner := conflictSet[0]
	for _, c := range conflictSet[1:] {
		if syncpkg.LessHLC(c.hlc, wantWinner.hlc) {
			wantWinner = c
		}
	}
	committedCount, conflictCount := 0, 0
	for _, c := range conflictSet {
		o := outcomes[c.key]
		switch o.Status {
		case syncpkg.StatusCommitted:
			committedCount++
			if c.key != wantWinner.key {
				t.Fatalf("seed %d: non-min intent %q committed; winner should be %q", seed, c.key, wantWinner.key)
			}
		case syncpkg.StatusConflict:
			conflictCount++
			if o.ReconciliationID == "" {
				t.Fatalf("seed %d: conflict %q has no reconciliation id", seed, c.key)
			}
		default:
			t.Fatalf("seed %d: conflict intent %q got unexpected status %q", seed, c.key, o.Status)
		}
	}
	if committedCount != 1 {
		t.Fatalf("seed %d: expected exactly 1 conflict-winner committed, got %d", seed, committedCount)
	}
	if conflictCount < 1 {
		t.Fatalf("seed %d: NO conflict materialised (vacuity) — invariant 2/3 would pass vacuously", seed)
	}

	// ---- Invariant (4) FROZEN HARD-BLOCK: every supersede of the frozen target is
	// StatusFrozen, the voucher is unchanged, a freeze recon row (kind=IRN) exists.
	frozenBefore, err := e.led.GetVoucher(e.ctx, frozenTarget)
	if err != nil {
		t.Fatalf("seed %d: get frozen target: %v", seed, err)
	}
	if len(frozenKeys) < 1 {
		t.Fatalf("seed %d: vacuity: no frozen-target supersede intents", seed)
	}
	for _, k := range frozenKeys {
		o := outcomes[k]
		if o.Status != syncpkg.StatusFrozen {
			t.Fatalf("seed %d: supersede of frozen voucher %q got %q, want FROZEN", seed, k, o.Status)
		}
	}
	if frozenBefore.VoucherNo != "" && !hasFreezeRecon(t, e, frozenTarget) {
		t.Fatalf("seed %d: no IRN freeze recon row for frozen target", seed)
	}

	// ---- Invariant (3) LOSER PRESERVED + SURFACED: every CONFLICT loser has a
	// reconciliation row (kind=CONFLICT) whose payload round-trips to the intent. ----
	recons, err := e.led.ListReconciliationQueue(e.ctx)
	if err != nil {
		t.Fatalf("seed %d: list recon: %v", seed, err)
	}
	for key, o := range outcomes {
		if o.Status != syncpkg.StatusConflict {
			continue
		}
		losing := byKey[key]
		payloadIntent, ok := findConflictPayload(t, e, o.ReconciliationID)
		if !ok {
			t.Fatalf("seed %d: conflict loser %q has no payload-bearing recon row %s", seed, key, o.ReconciliationID)
		}
		if payloadIntent.IdempotencyKey != losing.IdempotencyKey ||
			payloadIntent.ClientVoucherID != losing.ClientVoucherID ||
			payloadIntent.Op != losing.Op {
			t.Fatalf("seed %d: conflict payload mismatch: got %+v want key=%s id=%s op=%s",
				seed, payloadIntent, losing.IdempotencyKey, losing.ClientVoucherID, losing.Op)
		}
	}
	_ = recons

	// ---- Invariant (1) ZERO collisions + gap-free run over the SINGLE series. ----
	var committedCreates []committedCreate
	for key, o := range outcomes {
		if o.Status == syncpkg.StatusCommitted && byKey[key].Op == syncpkg.OpCreate {
			committedCreates = append(committedCreates, committedCreate{id: o.ClientVoucherID, number: o.FinalVoucherNo})
		}
	}
	assertGapFreeSeries(t, seed, e, committedCreates)

	// ---- Invariant (6) LEDGER INTEGRITY: trial balance balanced; every live head
	// is a single current version. ----
	tb, err := e.rep.TrialBalance(e.ctx, e.cid, "")
	if err != nil {
		t.Fatalf("seed %d: trial balance: %v", seed, err)
	}
	if !tb.IsBalanced() {
		t.Fatalf("seed %d: trial balance not balanced: dr=%s cr=%s", seed, tb.TotalDebit, tb.TotalCredit)
	}
	assertSingleHeadVersions(t, seed, e)

	// ================= SECOND COMMIT (exactly-once replay) =================
	// Snapshot the state that must NOT change.
	reconCountBefore := openReconCount(t, e)
	headsBefore := snapshotHeads(t, e)
	maxNumBefore := maxJournalNumber(t, e)

	out2 := commitAll(t, e, arrival, seed)
	for key, o := range out2 {
		if o.Status != syncpkg.StatusDuplicate {
			t.Fatalf("seed %d: replay of %q returned %q, want DUPLICATE", seed, key, o.Status)
		}
	}

	// ---- Invariant (5): nothing changed on replay. ----
	if got := openReconCount(t, e); got != reconCountBefore {
		t.Fatalf("seed %d: replay changed open recon count: %d -> %d", seed, reconCountBefore, got)
	}
	headsAfter := snapshotHeads(t, e)
	if len(headsAfter) != len(headsBefore) {
		t.Fatalf("seed %d: replay changed head count: %d -> %d", seed, len(headsBefore), len(headsAfter))
	}
	for id, v := range headsBefore {
		if headsAfter[id] != v {
			t.Fatalf("seed %d: replay changed head version for %s: %d -> %d", seed, id, v, headsAfter[id])
		}
	}
	if got := maxJournalNumber(t, e); got != maxNumBefore {
		t.Fatalf("seed %d: replay burned a number: max %d -> %d", seed, maxNumBefore, got)
	}

	// ================= DETERMINISM ACROSS A FRESH IDENTICAL-SEED RUN =================
	// Re-run the SAME seed on a fresh company and assert the same winner + same
	// final numbers for the conflict set and the committed-create multiset.
	assertSeedDeterminism(t, seed, wantWinner.hlc, sortedNumbers(committedCreates))
}

// hasFreezeRecon reports whether an open IRN freeze recon row exists for vid.
func hasFreezeRecon(t *testing.T, e fuzzEnv, vid uuid.UUID) bool {
	t.Helper()
	entries, err := e.led.ListReconciliationQueue(e.ctx)
	if err != nil {
		t.Fatalf("list recon: %v", err)
	}
	for _, r := range entries {
		if r.VoucherID == vid && r.Kind == "IRN" {
			return true
		}
	}
	return false
}

// findConflictPayload reads the recon row by id via WS1's ConflictPayload helper,
// round-tripping the preserved intent payload JSONB back into a sync.Intent.
func findConflictPayload(t *testing.T, e fuzzEnv, reconID string) (syncpkg.Intent, bool) {
	t.Helper()
	rid, err := uuid.Parse(reconID)
	if err != nil {
		t.Fatalf("bad recon id %q: %v", reconID, err)
	}
	in, ok, err := e.syncS.ConflictPayload(e.ctx, rid)
	if err != nil {
		t.Fatalf("read conflict payload: %v", err)
	}
	return in, ok
}

// assertGapFreeSeries asserts the COMMITTED creates' final numbers' integer
// suffixes form a gap-free ascending run with no dups (single class+FY series).
func assertGapFreeSeries(t *testing.T, seed int64, e fuzzEnv, creates []committedCreate) {
	t.Helper()
	nums := make([]int, 0, len(creates))
	seen := map[int]bool{}
	for _, c := range creates {
		n := suffixInt(t, c.number)
		if seen[n] {
			t.Fatalf("seed %d: DUPLICATE final number %d (%q)", seed, n, c.number)
		}
		seen[n] = true
		nums = append(nums, n)
	}
	if len(nums) == 0 {
		return // no creates committed this seed; nothing to check
	}
	// The series includes the Phase-0 base vouchers (which used PostVoucher and
	// share the same allocator). So the committed-create suffixes are a SUBSET of
	// the full 1..max run; assert the full DB series is gap-free 1..max instead.
	maxN := maxJournalNumber(t, e)
	full := journalNumberSet(t, e)
	for i := 1; i <= maxN; i++ {
		if !full[i] {
			t.Fatalf("seed %d: GAP in journal number series at %d (max=%d)", seed, i, maxN)
		}
	}
	// And every committed create's number is within the series and unique (checked above).
	for _, n := range nums {
		if n < 1 || n > maxN {
			t.Fatalf("seed %d: committed number %d out of series range 1..%d", seed, n, maxN)
		}
	}
}

// suffixInt parses the trailing integer of a voucher number (the allocator emits a
// bare integer or prefix-padded form; the seeded Journal type emits a plain int).
func suffixInt(t *testing.T, no string) int {
	t.Helper()
	// Take the trailing run of digits.
	i := len(no)
	for i > 0 && no[i-1] >= '0' && no[i-1] <= '9' {
		i--
	}
	digits := no[i:]
	if digits == "" {
		t.Fatalf("voucher number %q has no integer suffix", no)
	}
	n, err := strconv.Atoi(digits)
	if err != nil {
		t.Fatalf("parse number suffix %q: %v", no, err)
	}
	return n
}

// maxJournalNumber returns the largest integer suffix among live Journal voucher
// numbers for the company.
func maxJournalNumber(t *testing.T, e fuzzEnv) int {
	t.Helper()
	set := journalNumberSet(t, e)
	max := 0
	for n := range set {
		if n > max {
			max = n
		}
	}
	return max
}

// journalNumberSet returns the set of integer suffixes of every ALLOCATED Journal
// voucher number for the company — the allocator's footprint, read across ALL
// voucher_version rows (not just live heads). A delete tombstones a voucher but
// the number it consumed stays allocated (carried forward on the tombstone
// version), so this set is exactly the monotonic allocator run 1..N and must be
// gap-free even when conflict winners DELETE a base voucher. Scoped to the single
// Journal voucher type so it is one series.
func journalNumberSet(t *testing.T, e fuzzEnv) map[int]bool {
	t.Helper()
	out := map[int]bool{}
	err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT DISTINCT vv.voucher_no
			   FROM voucher_version vv
			   JOIN voucher_head vh ON vh.voucher_id = vv.voucher_id AND vh.company_id = vv.company_id
			  WHERE vv.company_id = $1 AND vv.voucher_type_id = $2 AND vv.voucher_no <> ''`,
			e.cid, e.vtID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var no string
			if err := rows.Scan(&no); err != nil {
				return err
			}
			out[suffixInt(t, no)] = true
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("journal number set: %v", err)
	}
	return out
}

// assertSingleHeadVersions verifies every live head points to exactly one current
// version (no fan-out / duplicate current).
func assertSingleHeadVersions(t *testing.T, seed int64, e fuzzEnv) {
	t.Helper()
	err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		var bad int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM voucher_head WHERE company_id = $1 AND current_version_id IS NULL`,
			e.cid).Scan(&bad); err != nil {
			return err
		}
		if bad != 0 {
			t.Fatalf("seed %d: %d heads with NULL current_version_id", seed, bad)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed %d: single-head check: %v", seed, err)
	}
}

// snapshotHeads returns voucher_id -> head version_no for live heads.
func snapshotHeads(t *testing.T, e fuzzEnv) map[uuid.UUID]int {
	t.Helper()
	out := map[uuid.UUID]int{}
	err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT voucher_id, version_no FROM voucher_head WHERE company_id = $1 AND is_deleted = false`, e.cid)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var v int
			if err := rows.Scan(&id, &v); err != nil {
				return err
			}
			out[id] = v
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("snapshot heads: %v", err)
	}
	return out
}

// openReconCount returns the number of open reconciliation rows.
func openReconCount(t *testing.T, e fuzzEnv) int {
	t.Helper()
	entries, err := e.led.ListReconciliationQueue(e.ctx)
	if err != nil {
		t.Fatalf("list recon: %v", err)
	}
	return len(entries)
}

func sortedNumbers(creates []committedCreate) []string {
	out := make([]string, 0, len(creates))
	for _, c := range creates {
		out = append(out, c.number)
	}
	// stable string sort
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// assertSeedDeterminism re-runs the IDENTICAL seed on a FRESH company and checks
// the conflict resolution + numbering are reproducible. Note the base-voucher ids
// are server-minted (random UUIDv7) so they DIFFER across runs — therefore the
// winner is compared on its run-INDEPENDENT identity: the (HLC, NodeID) of the
// conflict-set min, which is purely seed-derived (deterministic node-id gen + HLC).
// The committed-create FINAL NUMBER multiset must also match exactly (each company
// starts its allocator series at 1, so the numbers are identical run-to-run).
func assertSeedDeterminism(t *testing.T, seed int64, wantWinnerHLC syncpkg.HLCTimestamp, wantNumbers []string) {
	t.Helper()
	e := newFuzzEnv(t)
	// The determinism company exists only for the length of this re-run. It is
	// the SECOND of the two companies each seed mints, so forgetting this defer
	// costs 200 companies on its own.
	defer e.close()
	sch := buildSchedule(t, e, seed)

	outcomes := commitAll(t, e, sch.arrival, seed)

	// Recompute the (HLC,node)-min of THIS run's conflict set and assert it is the
	// same timestamp as run 1's winner AND that it committed (exactly one winner).
	if len(sch.conflictSet) < 2 {
		t.Fatalf("seed %d (det): conflict set has %d (<2) intents", seed, len(sch.conflictSet))
	}
	min := sch.conflictSet[0]
	for _, c := range sch.conflictSet[1:] {
		if syncpkg.LessHLC(c.hlc, min.hlc) {
			min = c
		}
	}
	if min.hlc != wantWinnerHLC {
		t.Fatalf("seed %d (det): winner HLC diverged: run1=%+v run2=%+v", seed, wantWinnerHLC, min.hlc)
	}
	committed := 0
	for _, c := range sch.conflictSet {
		if outcomes[c.key].Status == syncpkg.StatusCommitted {
			committed++
			if c.hlc != min.hlc {
				t.Fatalf("seed %d (det): non-min intent committed (hlc %+v, min %+v)", seed, c.hlc, min.hlc)
			}
		}
	}
	if committed != 1 {
		t.Fatalf("seed %d (det): expected exactly 1 winner committed, got %d", seed, committed)
	}

	// Same committed-create FINAL NUMBER multiset.
	var gotCreates []committedCreate
	for key, o := range outcomes {
		if o.Status == syncpkg.StatusCommitted && sch.byKey[key].Op == syncpkg.OpCreate {
			gotCreates = append(gotCreates, committedCreate{id: o.ClientVoucherID, number: o.FinalVoucherNo})
		}
	}
	got := sortedNumbers(gotCreates)
	if strings.Join(got, ",") != strings.Join(wantNumbers, ",") {
		t.Fatalf("seed %d (det): non-deterministic final numbers:\n run1=%v\n run2=%v", seed, wantNumbers, got)
	}
}

// TestFuzzEnvReleasesItsCompany is the guard on the whole per-seed-teardown
// change. It is deliberately NOT a check that "the fuzz passes": the fuzz passed
// before this change too, while leaving 404 fully seeded companies behind on
// every run of the backend suite.
//
// The shape matters. It builds exactly what a fuzz seed builds, PROVES the
// company is really there first (a teardown test that never had anything to tear
// down is the classic vacuous green), calls close(), and then asserts through an
// INDEPENDENT superuser connection — not the env's own RLS-scoped app pool,
// which would report 0 rows for a company that still exists simply because the
// pool is closed or the GUC is gone — that the tenant root AND its master tables
// are empty.
//
// account_group/ledger/voucher_type are checked as well as company because the
// bug this replaces did delete the company row: the OLD security harness passed
// a company-count check while orphaning 603 MB of account_group. Only
// notification_outbox references company(id), and its FK action is NO ACTION, so
// deleting a company does not cascade to a single master. A company-only
// assertion here would bless exactly that bug.
func TestFuzzEnvReleasesItsCompany(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	ctx := context.Background()

	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	defer func() { _ = admin.Close(ctx) }()

	// The tables a seeded sync company actually populates. company is last so a
	// failure message reads "the masters leaked" before "the tenant leaked".
	type scoped struct{ table, col string }
	tables := []scoped{
		{"account_group", "company_id"},
		{"ledger", "company_id"},
		{"voucher_type", "company_id"},
		{"company", "id"},
	}
	countRows := func(tbl, col string, cid uuid.UUID) int {
		var n int
		if err := admin.QueryRow(ctx,
			`SELECT count(*) FROM `+tbl+` WHERE `+col+` = $1`, cid).Scan(&n); err != nil {
			t.Fatalf("count %s for %s: %v", tbl, cid, err)
		}
		return n
	}

	e := newFuzzEnv(t)
	cid := e.cid

	// --- NON-VACUITY: the company and its masters must exist before we release.
	// Without this the test would still be green if newFuzzEnv silently created
	// nothing at all, which is how a "cleanup works" test rots into a no-op.
	for _, s := range tables {
		if n := countRows(s.table, s.col, cid); n == 0 {
			t.Fatalf("vacuity: freshly built fuzz env has 0 rows in %s for company %s — "+
				"this test would prove nothing about teardown", s.table, cid)
		}
	}

	e.close()

	var leaked []string
	for _, s := range tables {
		if n := countRows(s.table, s.col, cid); n != 0 {
			leaked = append(leaked, fmt.Sprintf("%s=%d", s.table, n))
		}
	}
	if len(leaked) > 0 {
		t.Fatalf("fuzzEnv.close() did not release company %s: %s still has rows. "+
			"TestFuzzPartition builds two such companies per seed across %d seeds, so a leak "+
			"here means %d abandoned tenants per backend test run — the residue bound is 0.",
			cid, strings.Join(leaked, " "), numSeeds, numSeeds*2)
	}
}

// explicitClosers lists the functions allowed to call e.close() WITHOUT a defer,
// with the reason. There is exactly one, and it is the guard test above: it has
// to observe the database on both sides of the release, so the call must happen
// at a point it chooses rather than when the function returns.
//
// Anything else that calls newFuzzEnv must defer, because a non-deferred close
// is skipped by every t.Fatalf in the function — and the fuzz seeds are full of
// them. A seed that fails would then ALSO leak its company, which is the worst
// possible time to start leaking: a red run is the one you re-run repeatedly.
var explicitClosers = map[string]string{
	"TestFuzzEnvReleasesItsCompany": "must count rows before and after the release, so it calls close() at a chosen point",
}

// TestEveryFuzzEnvCallSiteReleasesIt is the guard on the CALL SITES, and it
// exists because a mutation proved TestFuzzEnvReleasesItsCompany cannot see them.
//
// Deleting `defer e.close()` from runFuzzSeed reintroduces 200 abandoned
// companies per backend run — the single largest source of the 685 this work
// exists to remove — and TestFuzzEnvReleasesItsCompany still PASSES, because it
// calls close() itself and therefore only ever proves that close() works, never
// that anybody calls it. A test that cannot fail for the change it is supposed to
// guard is worth nothing, so this second, structural test was added: it asserts
// the SHAPE of every call site instead of the behaviour of the callee.
//
// It parses this package's own test sources and, for every function containing a
// call to newFuzzEnv, requires:
//
//   - the result to be bound to a plain identifier (a call whose env is dropped
//     on the floor can never be released), and
//   - a `defer <that ident>.close()` in the same function — unless the function
//     is named in explicitClosers, which must then close it explicitly.
//
// Being source-level, it costs no database and cannot be defeated by load, and
// it covers a call site written tomorrow in a file that does not exist today.
//
// A POSITIVE CONTROL runs first over a synthetic source that is missing exactly
// one defer. Without it, a walker that silently matched nothing — a renamed
// helper, a shape the AST walk does not reach — would report "no offences"
// forever and this guard would be decoration, which is precisely the failure it
// was written to fix.
func TestEveryFuzzEnvCallSiteReleasesIt(t *testing.T) {
	control := `package p

func good(t *testing.T) {
	e := newFuzzEnv(t)
	defer e.close()
	_ = e
}

func bad(t *testing.T) {
	e := newFuzzEnv(t)
	_ = e
}

func discarded(t *testing.T) {
	_ = newFuzzEnv(t)
}
`
	ctrl, sites := auditFuzzEnvCallSites(t, "control.go", []byte(control))
	if sites != 3 {
		t.Fatalf("positive control: walker found %d newFuzzEnv call sites in a source with "+
			"exactly 3; it is not seeing the code it claims to audit, so every clean result "+
			"below is meaningless", sites)
	}
	if len(ctrl) != 2 {
		t.Fatalf("positive control: walker reported %d offences in a source with exactly 2 "+
			"(one missing defer, one discarding the env); the guard is not enforcing. got=%v",
			len(ctrl), ctrl)
	}

	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatalf("glob test sources: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("vacuity: no *_test.go files found in this package's directory")
	}

	var offences []string
	total := 0
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		hits, n := auditFuzzEnvCallSites(t, f, src)
		offences = append(offences, hits...)
		total += n
	}
	if total == 0 {
		t.Fatalf("vacuity: scanned %d files and found no newFuzzEnv call sites at all. Either the "+
			"helper was renamed and this guard silently stopped guarding, or the fuzz was deleted; "+
			"either way do not leave this test asserting nothing", len(files))
	}
	if len(offences) > 0 {
		t.Fatalf("fuzz env(s) never released: %s. Each unreleased env is one fully seeded company "+
			"(28 account_group rows, its ledgers and voucher types) abandoned in the shared dev "+
			"database, multiplied by %d seeds — this is exactly how the backend suite came to leak "+
			"685 companies a run. Add `defer e.close()` at the call site, or, if the release must "+
			"be explicit, name the function in explicitClosers with the reason.",
			strings.Join(offences, "; "), numSeeds)
	}
}

// auditFuzzEnvCallSites returns one message per offending newFuzzEnv call site in
// src, plus the total number of call sites it saw (the caller's anti-vacuity
// signal).
//
// The unit of judgement is the ENCLOSING FUNCTION, not the file: `defer` is
// function-scoped, so "the file contains a close somewhere" would be satisfied by
// a defer in a neighbouring function that never runs for this env. Matching is on
// the identifier the env was bound to, so `defer other.close()` does not excuse a
// leaked `e`.
func auditFuzzEnvCallSites(t *testing.T, filename string, src []byte) (offences []string, sites int) {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", filename, err)
	}

	// closesIn reports, for one function body, which identifiers it defers a
	// close() on and which it closes without a defer.
	closesIn := func(fn ast.Node) (deferred, plain map[string]bool) {
		deferred, plain = map[string]bool{}, map[string]bool{}
		recv := func(call *ast.CallExpr) string {
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "close" {
				return ""
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok {
				return ""
			}
			return id.Name
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			switch s := n.(type) {
			case *ast.DeferStmt:
				if name := recv(s.Call); name != "" {
					deferred[name] = true
				}
			case *ast.ExprStmt:
				if call, ok := s.X.(*ast.CallExpr); ok {
					if name := recv(call); name != "" {
						plain[name] = true
					}
				}
			}
			return true
		})
		return deferred, plain
	}

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		deferred, plain := closesIn(fn)
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			// A bare `newFuzzEnv(t)` statement binds nothing at all. It is counted
			// as a site AND reported, rather than skipped, so that the one way to
			// build a company this walker cannot see is not also the one way to
			// evade it.
			if stmt, ok := n.(*ast.ExprStmt); ok {
				if call, ok := stmt.X.(*ast.CallExpr); ok {
					if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "newFuzzEnv" {
						sites++
						offences = append(offences, fmt.Sprintf(
							"%s (%s): newFuzzEnv is called as a bare statement, so its company can never be released",
							fset.Position(stmt.Pos()).String(), fn.Name.Name))
					}
				}
				return true
			}
			assign, ok := n.(*ast.AssignStmt)
			if !ok || len(assign.Rhs) != 1 {
				return true
			}
			call, ok := assign.Rhs[0].(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			if !ok || id.Name != "newFuzzEnv" {
				return true
			}
			sites++
			where := fset.Position(assign.Pos()).String()
			lhs, ok := assign.Lhs[0].(*ast.Ident)
			if !ok || lhs.Name == "_" {
				offences = append(offences, fmt.Sprintf(
					"%s (%s): the env returned by newFuzzEnv is discarded, so its company can never be released",
					where, fn.Name.Name))
				return true
			}
			switch {
			case deferred[lhs.Name]:
				// correct
			case plain[lhs.Name]:
				if _, allowed := explicitClosers[fn.Name.Name]; !allowed {
					offences = append(offences, fmt.Sprintf(
						"%s (%s): %s.close() is called but not DEFERRED, so any t.Fatalf between the two skips it",
						where, fn.Name.Name, lhs.Name))
				}
			default:
				offences = append(offences, fmt.Sprintf(
					"%s (%s): no `defer %s.close()`",
					where, fn.Name.Name, lhs.Name))
			}
			return true
		})
	}
	return offences, sites
}
