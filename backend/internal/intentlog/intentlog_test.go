package intentlog

import (
	"context"
	"errors"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
)

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// harness follows handler_itc_test.go: CORED_SKIP_DB is the only skip, and an
// unreachable database with it unset FAILS.
func harness(t *testing.T) (*db.Pool, *pgx.Conn, uuid.UUID) {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped (CORED_SKIP_DB set)")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, envOr("DATABASE_URL", testdb.AppDSN))
	if err != nil {
		t.Fatalf("app postgres unavailable and CORED_SKIP_DB is unset: %v", err)
	}
	t.Cleanup(pool.Close)
	admin, err := pgx.Connect(ctx, testdb.AdminDSNValue())
	if err != nil {
		t.Fatalf("admin postgres unavailable: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	var present bool
	_ = admin.QueryRow(ctx, `SELECT to_regclass('public.agent_intent_log') IS NOT NULL`).Scan(&present)
	if !present {
		t.Fatal("migration 0092 (agent_intent_log) is not applied; run `cored migrate`")
	}
	cid := uuid.New()
	testdb.Cleanup(t, cid)
	return pool, admin, cid
}

func appendN(t *testing.T, pool *db.Pool, cid, run uuid.UUID, n int, t0 time.Time) {
	t.Helper()
	agent := uuid.New()
	var appendErr error
	err := pool.RunInTx(db.WithCompany(context.Background(), cid), func(ctx context.Context, tx pgx.Tx) error {
		for i := 0; i < n; i++ {
			_, appendErr = Append(ctx, tx, cid, Event{
				RunID: run, Kind: KindToolCall, ActorKind: ActorAgent, ActorID: "khata-agent",
				OnBehalfOf: agent, ActionID: "post.vouchers.payment", ACLKey: "create payment",
				// Keys deliberately out of order and nested: JSONB will reorder
				// them, and Verify must still recompute.
				Payload: map[string]any{"z": i, "a": map[string]any{"y": "₹1,00,000.00", "b": []any{3, 1}}},
			}, t0.Add(time.Duration(i)*time.Second))
			if appendErr != nil {
				return appendErr
			}
		}
		return nil
	})
	// ⛔ asserted AFTER the closure returns (HANDOFF §7.1).
	if err != nil {
		t.Fatalf("append: %v", err)
	}
}

func verify(t *testing.T, pool *db.Pool, cid, run uuid.UUID) (int, error) {
	t.Helper()
	var n int
	var verr error
	err := pool.RunInReadOnlyTx(db.WithCompany(context.Background(), cid), func(ctx context.Context, tx pgx.Tx) error {
		n, verr = Verify(ctx, tx, cid, run)
		return nil
	})
	if err != nil {
		t.Fatalf("verify tx: %v", err)
	}
	return n, verr
}

// TestChainRecomputesAfterJSONBNormalisation is the round trip that matters:
// the payload goes in as Go marshalled it and comes back as Postgres renders
// JSONB, and the chain must still verify.
func TestChainRecomputesAfterJSONBNormalisation(t *testing.T) {
	pool, _, cid := harness(t)
	run := uuid.New()
	appendN(t, pool, cid, run, 4, time.Date(2026, 9, 28, 9, 0, 0, 123456789, time.UTC))
	n, err := verify(t, pool, cid, run)
	if err != nil || n != 4 {
		t.Fatalf("Verify = (%d, %v), want (4, nil)", n, err)
	}
}

// TestTamperingIsDetected edits one stored payload with the trigger disarmed
// (the superuser's replica mode — the only way past 0092's trigger) and
// requires Verify to name the altered link.
//
// Mutation proof (run for D121): make computeHash ignore the payload and this
// test fails — the chain "verifies" over the altered row.
func TestTamperingIsDetected(t *testing.T) {
	pool, admin, cid := harness(t)
	run := uuid.New()
	appendN(t, pool, cid, run, 3, time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC))
	ctx := context.Background()
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = tx.Exec(ctx, `SET LOCAL session_replication_role = replica`)
	if _, err := tx.Exec(ctx, `UPDATE agent_intent_log SET payload = '{"z":99}'::jsonb
	                            WHERE company_id = $1 AND run_id = $2 AND seq = 2`, cid, run); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("tamper: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	n, verr := verify(t, pool, cid, run)
	if !errors.Is(verr, ErrChainBroken) {
		t.Fatalf("Verify over a tampered row = (%d, %v), want ErrChainBroken", n, verr)
	}
	if n != 1 || !strings.Contains(verr.Error(), "seq 2") {
		t.Fatalf("Verify should stop at seq 2 having checked 1 link, got n=%d err=%v", n, verr)
	}
}

// TestAppendOnlyByGrantAndByTrigger proves BOTH halves of 0092's append-only
// rule independently: the app role is refused by privilege, and the owner (a
// superuser, who bypasses privileges) is refused by the trigger.
func TestAppendOnlyByGrantAndByTrigger(t *testing.T) {
	pool, admin, cid := harness(t)
	run := uuid.New()
	appendN(t, pool, cid, run, 1, time.Now())

	for _, stmt := range []string{
		`UPDATE agent_intent_log SET actor_id = 'x' WHERE company_id = $1`,
		`DELETE FROM agent_intent_log WHERE company_id = $1`,
	} {
		err := pool.RunInTx(db.WithCompany(context.Background(), cid), func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, stmt, cid)
			return err
		})
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("app role %q: got %v, want permission denied (the GRANT half)", stmt, err)
		}
		_, err = admin.Exec(context.Background(), stmt, cid)
		if err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("superuser %q: got %v, want the trigger's append-only refusal (the TRIGGER half)", stmt, err)
		}
	}
}

// TestAnchorCatchesATruncatedTail is the reason the anchor exists: removing a
// run's LAST links leaves a chain that still verifies on its own, and only the
// anchor, which committed to the old head, can see it.
func TestAnchorCatchesATruncatedTail(t *testing.T) {
	pool, admin, cid := harness(t)
	runA, runB := uuid.New(), uuid.New()
	t0 := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	appendN(t, pool, cid, runA, 3, t0)
	appendN(t, pool, cid, runB, 2, t0)

	ctx := db.WithCompany(context.Background(), cid)
	var row AnchorRow
	var aerr error
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		row, aerr = Anchor(ctx, tx, cid, t0.Add(time.Hour))
		return aerr
	}); err != nil {
		t.Fatalf("anchor: %v", err)
	}
	if row.RunCount != 2 || row.EventCount != 5 {
		t.Fatalf("anchor counted runs=%d events=%d, want 2 and 5", row.RunCount, row.EventCount)
	}
	verifyAnchors := func() (int, error) {
		var n int
		var verr error
		_ = pool.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			n, verr = VerifyAnchors(ctx, tx, cid)
			return nil
		})
		return n, verr
	}
	if n, err := verifyAnchors(); err != nil || n != 1 {
		t.Fatalf("VerifyAnchors on untouched rows = (%d, %v)", n, err)
	}

	// Truncate run A's last link, past the trigger.
	tx, _ := admin.Begin(context.Background())
	_, _ = tx.Exec(context.Background(), `SET LOCAL session_replication_role = replica`)
	if _, err := tx.Exec(context.Background(), `DELETE FROM agent_intent_log WHERE company_id = $1 AND run_id = $2 AND seq = 3`, cid, runA); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit(context.Background())

	if n, err := verify(t, pool, cid, runA); err != nil || n != 2 {
		t.Fatalf("precondition: the truncated chain should still verify ALONE (that is the hole), got (%d, %v)", n, err)
	}
	if _, err := verifyAnchors(); !errors.Is(err, ErrChainBroken) {
		t.Fatalf("VerifyAnchors after truncating an anchored tail = %v, want ErrChainBroken", err)
	}
}

// TestFractionalNumbersAreRefused pins the canonicalisation rule: a payload
// number with a fraction would hash differently after JSONB re-renders it.
func TestFractionalNumbersAreRefused(t *testing.T) {
	if _, err := canonical([]byte(`{"amount": 1.50}`)); err == nil {
		t.Fatal("a fractional number was accepted; it would fail Verify after JSONB re-renders it")
	}
	if _, err := canonical([]byte(`{"amount": "1.50", "n": 3}`)); err != nil {
		t.Fatalf("strings and integers must be accepted: %v", err)
	}
	a, _ := canonical([]byte(`{"b":1,"a":{"d":2,"c":3}}`))
	b, _ := canonical([]byte(`{ "a": {"c": 3, "d": 2}, "b": 1 }`))
	if string(a) != string(b) {
		t.Fatalf("canonical form depends on key order / whitespace: %s vs %s", a, b)
	}
}

// TestKindsMatchTheMigration pins Kinds against 0092's CHECK, read from the
// live catalog rather than from the file text.
func TestKindsMatchTheMigration(t *testing.T) {
	_, admin, _ := harness(t)
	var def string
	if err := admin.QueryRow(context.Background(),
		`SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'agent_intent_log_kind'`).Scan(&def); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, part := range strings.Split(def, "'") {
		if part != "" && !strings.ContainsAny(part, "(): ,=") {
			got = append(got, part)
		}
	}
	var want []string
	for _, k := range Kinds {
		want = append(want, string(k))
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("0092 CHECK kinds %v != intentlog.Kinds %v", got, want)
	}
}

// TestRLSHidesAnotherTenantsLog: company B's session sees nothing of A's run.
func TestRLSHidesAnotherTenantsLog(t *testing.T) {
	pool, _, cid := harness(t)
	run := uuid.New()
	appendN(t, pool, cid, run, 2, time.Now())
	other := uuid.New()
	var n int
	var lerr error
	_ = pool.RunInReadOnlyTx(db.WithCompany(context.Background(), other), func(ctx context.Context, tx pgx.Tx) error {
		es, err := List(ctx, tx, cid, run, 0) // even naming A's id explicitly
		n, lerr = len(es), err
		return nil
	})
	if lerr != nil || n != 0 {
		t.Fatalf("company B listed %d of company A's intent rows (err %v), want 0", n, lerr)
	}
}

// TestConcurrentAppendsToOneRunSerialise: appends from several transactions at
// once — the poller's step, an approval decision, a cancel — must each get the
// next seq. Before the per-run transaction lock, two of them read the same head
// and one failed on agent_intent_log_one_seq_per_run.
func TestConcurrentAppendsToOneRunSerialise(t *testing.T) {
	pool, _, cid := harness(t)
	run := uuid.New()
	const writers = 8
	errs := make(chan error, writers)
	start := make(chan struct{})
	for w := 0; w < writers; w++ {
		go func(w int) {
			<-start
			errs <- pool.RunInTx(db.WithCompany(context.Background(), cid), func(ctx context.Context, tx pgx.Tx) error {
				if _, err := Append(ctx, tx, cid, Event{RunID: run, Kind: KindApprovalDecided, ActorKind: ActorUser,
					ActorID: uuid.NewString(), Payload: map[string]any{"w": w}}, time.Now()); err != nil {
					return err
				}
				// Hold the transaction open a moment so the writers overlap.
				_, err := tx.Exec(ctx, `SELECT pg_sleep(0.02)`)
				return err
			})
		}(w)
	}
	close(start)
	var failed []error
	for w := 0; w < writers; w++ {
		if err := <-errs; err != nil {
			failed = append(failed, err)
		}
	}
	if len(failed) > 0 {
		t.Fatalf("%d of %d concurrent appends failed: %v", len(failed), writers, failed[0])
	}
	if n, err := verify(t, pool, cid, run); err != nil || n != writers {
		t.Fatalf("Verify = (%d, %v), want (%d, nil)", n, err, writers)
	}
}
