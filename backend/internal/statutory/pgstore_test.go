package statutory

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// Live-Postgres harness (connect-or-skip, mirroring internal/authn's idiom).
//
// NOTE these tests are NOT hermetic: they need the tally_pg container on :5433
// with migration 0013 applied. Every entry point skips (never fails) when the DB
// or the table is absent, and CORED_SKIP_DB=1 forces the skip, so the package
// stays green offline.
// ---------------------------------------------------------------------------

const (
	statAdminDSN = "postgres://postgres:postgres@localhost:5433/tally?sslmode=disable"
	statAppDSN   = "postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable"
)

func statTablePresent(ctx context.Context, conn *pgx.Conn, name string) bool {
	var ok bool
	_ = conn.QueryRow(ctx, `SELECT to_regclass('public.'||$1) IS NOT NULL`, name).Scan(&ok)
	return ok
}

// pgHarness returns an APP-role pool (what statutoryd itself uses, NOBYPASSRLS)
// plus an ADMIN connection used ONLY as an INDEPENDENT oracle: the assertions
// below read the table directly rather than asking PGStore to confirm its own
// writes.
func pgHarness(t *testing.T) (*pgxpool.Pool, *pgx.Conn) {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped (CORED_SKIP_DB set)")
	}
	ctx := context.Background()

	admin, err := pgx.Connect(ctx, statAdminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })

	if !statTablePresent(ctx, admin, "statutory_step") {
		t.Skip("migration 0013 (statutory_step) not applied — skipping PG StepStore tests")
	}

	pool, err := pgxpool.New(ctx, statAppDSN)
	if err != nil {
		t.Skipf("app pool unavailable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, admin
}

// freshIDs mints a company/voucher pair unique to this test run and registers
// the row cleanup. Unique ids are MANDATORY against a durable store: the
// existing in-memory suites reuse the constants "co1"/"v1", which would make a
// second `go test -count=1` find a fully-completed workflow and execute zero
// steps.
func freshIDs(t *testing.T, admin *pgx.Conn) (companyID, voucherID, workflowID string) {
	t.Helper()
	companyID = "co-" + uuid.NewString()
	voucherID = "v-" + uuid.NewString()
	// Mirrors GenerateIRNWorkflow's derivation (irn_workflow.go). If that
	// derivation ever changes, the row-count oracles below go to 0 and fail —
	// this coupling is self-checking, not silent.
	workflowID = "irn:" + companyID + ":" + voucherID
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(),
			`DELETE FROM statutory_step WHERE workflow_id = $1`, workflowID)
	})
	return companyID, voucherID, workflowID
}

// ---- independent oracles (read the table directly, never via PGStore) ------

func stepCount(t *testing.T, admin *pgx.Conn, workflowID string) int {
	t.Helper()
	var n int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM statutory_step WHERE workflow_id = $1`, workflowID).Scan(&n); err != nil {
		t.Fatalf("count steps: %v", err)
	}
	return n
}

func stepNames(t *testing.T, admin *pgx.Conn, workflowID string) []string {
	t.Helper()
	rows, err := admin.Query(context.Background(),
		`SELECT step_name FROM statutory_step WHERE workflow_id = $1 ORDER BY seq`, workflowID)
	if err != nil {
		t.Fatalf("select step names: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan step name: %v", err)
		}
		out = append(out, s)
	}
	return out
}

func stepCreatedAt(t *testing.T, admin *pgx.Conn, workflowID, step string) time.Time {
	t.Helper()
	var ts time.Time
	if err := admin.QueryRow(context.Background(),
		`SELECT created_at FROM statutory_step WHERE workflow_id = $1 AND step_name = $2`,
		workflowID, step).Scan(&ts); err != nil {
		t.Fatalf("created_at of step %q: %v", step, err)
	}
	return ts
}

// jsonEqual compares two JSON documents SEMANTICALLY. Byte equality is the wrong
// oracle here: `output` is JSONB, so Postgres normalizes object key order and
// whitespace, while MemStore hands back the raw bytes. See pgstore.go for why
// that is safe for IRN determinism.
func jsonEqual(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		t.Fatalf("unmarshal %s: %v", a, err)
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		t.Fatalf("unmarshal %s: %v", b, err)
	}
	return reflect.DeepEqual(av, bv)
}

// ---------------------------------------------------------------------------
// 1. StepStore conformance — the SAME suite run against MemStore and PGStore.
// ---------------------------------------------------------------------------

func stepStoreConformance(t *testing.T, store StepStore, wfA, wfB string) {
	t.Helper()

	// Unknown workflow: empty, no error (Run relies on this for a first attempt).
	got, err := store.Load(wfA)
	if err != nil {
		t.Fatalf("Load(unknown): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Load(unknown) must be empty, got %d", len(got))
	}

	// Append order is preserved by Load.
	want := []StepResult{
		{Name: "build", Output: json.RawMessage(`{"b":2,"a":1,"s":"x"}`)},
		{Name: "validate", Output: json.RawMessage(`{"ok":true}`)},
		{Name: "generate", Output: json.RawMessage(`{"irn":"deadbeef","status":"ACT"}`)},
	}
	for _, r := range want {
		if err := store.Append(wfA, r); err != nil {
			t.Fatalf("Append(%s): %v", r.Name, err)
		}
	}
	got, err = store.Load(wfA)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("Load returned %d results, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Name != want[i].Name {
			t.Fatalf("result %d: name %q, want %q (append order not preserved)", i, got[i].Name, want[i].Name)
		}
		if !jsonEqual(t, got[i].Output, want[i].Output) {
			t.Fatalf("result %d (%s): output %s, want semantically %s", i, got[i].Name, got[i].Output, want[i].Output)
		}
	}

	// Duplicate step name is a SILENT no-op that does not clobber the stored
	// output. This is what makes a completed workflow re-Run add no rows.
	if err := store.Append(wfA, StepResult{Name: "generate", Output: json.RawMessage(`{"irn":"SECOND-IRN","status":"ACT"}`)}); err != nil {
		t.Fatalf("duplicate Append must be a silent no-op, got error: %v", err)
	}
	got, err = store.Load(wfA)
	if err != nil {
		t.Fatalf("Load after duplicate: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("duplicate Append added a row: %d results, want %d", len(got), len(want))
	}
	if !jsonEqual(t, got[2].Output, want[2].Output) {
		t.Fatalf("duplicate Append overwrote the stored output: %s", got[2].Output)
	}

	// Workflows are isolated from each other.
	if err := store.Append(wfB, StepResult{Name: "build", Output: json.RawMessage(`{"other":true}`)}); err != nil {
		t.Fatalf("Append(wfB): %v", err)
	}
	got, err = store.Load(wfA)
	if err != nil {
		t.Fatalf("Load wfA: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("wfB leaked into wfA: %d results", len(got))
	}
	gotB, err := store.Load(wfB)
	if err != nil {
		t.Fatalf("Load wfB: %v", err)
	}
	if len(gotB) != 1 || gotB[0].Name != "build" {
		t.Fatalf("wfB should hold exactly its own step, got %+v", gotB)
	}
}

func TestStepStoreConformance_Mem(t *testing.T) {
	stepStoreConformance(t, NewMemStore(), "wf-a", "wf-b")
}

func TestStepStoreConformance_PG(t *testing.T) {
	pool, admin := pgHarness(t)
	_, _, wfA := freshIDs(t, admin)
	_, _, wfB := freshIDs(t, admin)

	stepStoreConformance(t, NewPGStore(pool), wfA, wfB)

	// Independent oracle: the rows really are in the table, exactly once each.
	if got := stepCount(t, admin, wfA); got != 3 {
		t.Fatalf("statutory_step holds %d rows for wfA, want 3", got)
	}
	if got := stepNames(t, admin, wfA); !reflect.DeepEqual(got, []string{"build", "validate", "generate"}) {
		t.Fatalf("row order in the table is %v, want [build validate generate]", got)
	}
}

// TestPGStoreRejectsEmptyOutput pins the one place PGStore is STRICTER than
// MemStore: `output` is JSONB NOT NULL, so a step returning no bytes is refused
// with a clear error instead of an opaque driver failure.
func TestPGStoreRejectsEmptyOutput(t *testing.T) {
	pool, admin := pgHarness(t)
	_, _, wf := freshIDs(t, admin)

	err := NewPGStore(pool).Append(wf, StepResult{Name: "build", Output: nil})
	if err == nil {
		t.Fatal("Append with empty output must fail (output is JSONB NOT NULL)")
	}
	if got := stepCount(t, admin, wf); got != 0 {
		t.Fatalf("failed Append must write nothing, found %d rows", got)
	}
}

// ---------------------------------------------------------------------------
// 2. Runner scenarios (runner_test.go) re-expressed against PGStore.
//
// The existing suites cannot simply be "pointed at" PGStore: they construct
// NewMemStore() inline AND reuse constant workflow ids ("wf", "wf1"), which a
// durable table would carry across runs. The scenarios are therefore re-run here
// with a fresh id per test.
// ---------------------------------------------------------------------------

func TestPGStoreRunnerExecutesStepsOnceAndResumes(t *testing.T) {
	pool, admin := pgHarness(t)
	_, _, wf := freshIDs(t, admin)
	store := NewPGStore(pool)

	var aCalls, bCalls, cCalls int
	failB := true
	steps := func() []Step {
		return []Step{
			{Name: "a", Run: func(context.Context, map[string]json.RawMessage) (json.RawMessage, error) {
				aCalls++
				return json.RawMessage(`"a"`), nil
			}},
			{Name: "b", Run: func(context.Context, map[string]json.RawMessage) (json.RawMessage, error) {
				bCalls++
				if failB {
					return nil, errors.New("boom")
				}
				return json.RawMessage(`"b"`), nil
			}},
			{Name: "c", Run: func(_ context.Context, prev map[string]json.RawMessage) (json.RawMessage, error) {
				cCalls++
				if string(prev["a"]) != `"a"` {
					t.Errorf("step c did not see step a's stored output: %s", prev["a"])
				}
				return json.RawMessage(`"c"`), nil
			}},
		}
	}

	// First run: b fails, c never runs, b is NOT recorded.
	if _, err := Run(context.Background(), store, wf, steps()); err == nil {
		t.Fatal("expected error from failing step b")
	}
	if aCalls != 1 || bCalls != 1 || cCalls != 0 {
		t.Fatalf("after first run want a=1 b=1 c=0, got a=%d b=%d c=%d", aCalls, bCalls, cCalls)
	}
	if got := stepNames(t, admin, wf); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("only the completed step may be persisted, table holds %v", got)
	}

	// Resume: a is memoized (not re-run), b retried, c runs.
	failB = false
	state, err := Run(context.Background(), store, wf, steps())
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if aCalls != 1 {
		t.Fatalf("step a must NOT re-run on resume, got %d", aCalls)
	}
	if bCalls != 2 || cCalls != 1 {
		t.Fatalf("want b=2 c=1 after resume, got b=%d c=%d", bCalls, cCalls)
	}
	if string(state["c"]) != `"c"` {
		t.Fatalf("unexpected final state: %v", state)
	}
	if got := stepNames(t, admin, wf); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("table holds %v, want [a b c]", got)
	}

	// Re-run a fully-completed workflow: ZERO executions, ZERO new rows.
	if _, err := Run(context.Background(), store, wf, steps()); err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if aCalls != 1 || bCalls != 2 || cCalls != 1 {
		t.Fatalf("completed workflow re-run must execute nothing, got a=%d b=%d c=%d", aCalls, bCalls, cCalls)
	}
	if got := stepCount(t, admin, wf); got != 3 {
		t.Fatalf("re-run duplicated rows: %d, want 3", got)
	}
}

// ---------------------------------------------------------------------------
// 3. Chaos scenarios (chaos_test.go) re-expressed against PGStore.
//
// runUntilDone (chaos_test.go) hard-codes the company/voucher ids "co1"/"v1",
// so it is unusable against a durable table; this is the same loop with the ids
// as parameters.
// ---------------------------------------------------------------------------

func runUntilDoneIDs(t *testing.T, store StepStore, prov ProviderAdapter, cored CoredClient,
	companyID, voucherID string, snap VoucherSnapshot) (IRNResult, int) {
	t.Helper()
	var last IRNResult
	for attempt := 1; attempt <= 20; attempt++ {
		res, err := GenerateIRNWorkflow(context.Background(), store, prov, cored, companyID, voucherID, snap)
		if err == nil {
			return res, attempt
		}
		if IsPermanent(err) {
			t.Fatalf("unexpected permanent error on attempt %d: %v", attempt, err)
		}
		last = res
	}
	t.Fatalf("workflow did not converge within 20 attempts (last=%+v)", last)
	return last, 0
}

func TestPGStoreChaosTransientProviderFailuresExactlyOnce(t *testing.T) {
	pool, admin := pgHarness(t)

	cases := []struct {
		name string
		plan []error
	}{
		{"outage", []error{ErrProviderOutage, nil}},
		{"token-expired", []error{ErrTokenExpired, nil}},
		{"rate-limited", []error{ErrRateLimited, nil}},
		{"all-three-then-ok", []error{ErrProviderOutage, ErrTokenExpired, ErrRateLimited, nil}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cid, vid, wf := freshIDs(t, admin)
			store := NewPGStore(pool)
			chaos := NewChaosProvider(FakeProvider{}, tc.plan...)
			cored := &FakeCoredClient{Snapshot: conformantSnapshot()}

			res, _ := runUntilDoneIDs(t, store, chaos, cored, cid, vid, conformantSnapshot())

			if res.IRN == "" {
				t.Fatal("no IRN minted")
			}
			if got := chaos.InnerCalls(); got != 1 {
				t.Fatalf("inner provider must be called exactly once, got %d", got)
			}
			if len(cored.Registered) != 1 {
				t.Fatalf("freeze hook must be called once, got %d", len(cored.Registered))
			}
			if cored.Registered[0].Kind != "IRN" || cored.Registered[0].Ref != res.IRN {
				t.Fatalf("freeze recorded wrong ref: %+v", cored.Registered[0])
			}
			// Independent oracle: the two EFFECTFUL steps durably recorded, in
			// order — and only those two.
			//
			// ⛔ `build` and `validate` are deliberately ABSENT. They are pure
			// functions of the snapshot and are re-derived on every unsettled
			// attempt; memoizing them is what made a mistyped first attempt
			// poison the voucher permanently (see GenerateIRNWorkflow's header).
			if got := stepNames(t, admin, wf); !reflect.DeepEqual(got, []string{"generate", "freeze"}) {
				t.Fatalf("statutory_step holds %v, want [generate freeze]", got)
			}

			// Re-running a now-complete workflow: same IRN, no new external calls.
			res2, err := GenerateIRNWorkflow(context.Background(), store, chaos, cored, cid, vid, conformantSnapshot())
			if err != nil {
				t.Fatalf("re-run: %v", err)
			}
			if res2.IRN != res.IRN {
				t.Fatalf("IRN changed across resume: %q vs %q", res.IRN, res2.IRN)
			}
			if chaos.InnerCalls() != 1 {
				t.Fatalf("inner provider called again on resume: %d", chaos.InnerCalls())
			}
			if len(cored.Registered) != 1 {
				t.Fatalf("freeze called again on resume: %d", len(cored.Registered))
			}
			if got := stepCount(t, admin, wf); got != 2 {
				t.Fatalf("re-run duplicated rows: %d, want 2", got)
			}
		})
	}
}

func TestPGStoreChaosFreezeFailureResumesNoDoubleIRN(t *testing.T) {
	pool, admin := pgHarness(t)
	cid, vid, wf := freshIDs(t, admin)

	store := NewPGStore(pool)
	chaos := NewChaosProvider(FakeProvider{}) // provider always succeeds
	cored := &FakeCoredClient{Snapshot: conformantSnapshot(), FailRegisterTimes: 2}

	res, _ := runUntilDoneIDs(t, store, chaos, cored, cid, vid, conformantSnapshot())

	if res.IRN == "" {
		t.Fatal("no IRN minted")
	}
	if chaos.InnerCalls() != 1 {
		t.Fatalf("provider must be called exactly once across freeze resumes, got %d", chaos.InnerCalls())
	}
	if len(cored.Registered) != 1 {
		t.Fatalf("freeze must ultimately be recorded exactly once, got %d", len(cored.Registered))
	}
	if cored.Registered[0].Ref != res.IRN {
		t.Fatalf("frozen ref mismatch")
	}
	if got := stepCount(t, admin, wf); got != 2 {
		t.Fatalf("statutory_step holds %d rows, want 2 (generate, freeze)", got)
	}
}

func TestPGStoreChaosPermanentRejectAbortsNoFreeze(t *testing.T) {
	pool, admin := pgHarness(t)
	cid, vid, wf := freshIDs(t, admin)

	store := NewPGStore(pool)
	chaos := NewChaosProvider(FakeProvider{}, ErrPermanentReject)
	cored := &FakeCoredClient{Snapshot: conformantSnapshot()}

	_, err := GenerateIRNWorkflow(context.Background(), store, chaos, cored, cid, vid, conformantSnapshot())
	if err == nil {
		t.Fatal("expected permanent reject error")
	}
	if !IsPermanent(err) {
		t.Fatalf("expected permanent error, got: %v", err)
	}
	var pe *ProviderError
	if !errors.As(err, &pe) || pe.Retryable() {
		t.Fatalf("expected non-retryable provider error, got: %v", err)
	}
	if len(cored.Registered) != 0 {
		t.Fatalf("freeze must NOT be called on permanent reject, got %d", len(cored.Registered))
	}
	if chaos.InnerCalls() != 0 {
		t.Fatalf("inner provider should not be reached, got %d", chaos.InnerCalls())
	}
	// ⛔ THE TABLE IS COMPLETELY EMPTY, and that is the fix. The aborted step is
	// not persisted, and the pure prefix is no longer persisted either — so a
	// permanent reject leaves NOTHING behind that a corrected attempt has to get
	// past. Previously this left [build validate], and the `build` row was the
	// poison: a retry resumed from it and could never see new data.
	if got := stepNames(t, admin, wf); len(got) != 0 {
		t.Fatalf("statutory_step holds %v, want nothing at all", got)
	}
}

// ---------------------------------------------------------------------------
// 4. THE restart gate (§8.2): GenerateIRN is called EXACTLY ONCE across a
//    statutoryd restart, modelled at the Go layer as a brand-new store instance
//    over the same DSN.
// ---------------------------------------------------------------------------

func TestPGStoreRestartMidWorkflowGeneratesIRNExactlyOnce(t *testing.T) {
	pool, admin := pgHarness(t)
	cid, vid, wf := freshIDs(t, admin)

	// The ChaosProvider instance is the call-count oracle and therefore SURVIVES
	// the simulated restart (it stands in for the external IRP, not for
	// statutoryd). The STORE is what gets rebuilt.
	chaos := NewChaosProvider(FakeProvider{})
	cored := &FakeCoredClient{Snapshot: conformantSnapshot(), FailRegisterTimes: 1}

	// --- "process 1": aborts inside the freeze step, after `generate` ---------
	before := NewPGStore(pool)
	_, err := GenerateIRNWorkflow(context.Background(), before, chaos, cored, cid, vid, conformantSnapshot())
	if err == nil {
		t.Fatal("first attempt must fail in the freeze step (FailRegisterTimes=1)")
	}
	if IsPermanent(err) {
		t.Fatalf("freeze outage must be transient/resumable, got permanent: %v", err)
	}
	if chaos.InnerCalls() != 1 {
		t.Fatalf("provider must have been called once before the crash, got %d", chaos.InnerCalls())
	}
	// Independent oracle: the durable table holds the completed `generate` and NOT
	// the failed freeze. (The pure prefix is never recorded — see
	// GenerateIRNWorkflow's header.)
	if got := stepNames(t, admin, wf); !reflect.DeepEqual(got, []string{"generate"}) {
		t.Fatalf("mid-workflow table state is %v, want [generate]", got)
	}
	generateAt := stepCreatedAt(t, admin, wf, "generate")

	// --- "process 2": brand-new store over the SAME DSN ----------------------
	// A fresh pool as well, so nothing in-process is carried over.
	pool2, err := pgxpool.New(context.Background(), statAppDSN)
	if err != nil {
		t.Fatalf("second pool: %v", err)
	}
	defer pool2.Close()
	after := NewPGStore(pool2)

	res, _ := runUntilDoneIDs(t, after, chaos, cored, cid, vid, conformantSnapshot())
	if res.IRN == "" {
		t.Fatal("no IRN after restart")
	}

	// THE assertion: the external IRP call happened exactly once IN TOTAL.
	if got := chaos.InnerCalls(); got != 1 {
		t.Fatalf("GenerateIRN must be called exactly once across the restart, got %d (double-IRN!)", got)
	}
	if len(cored.Registered) != 1 {
		t.Fatalf("freeze must be recorded exactly once, got %d", len(cored.Registered))
	}
	if cored.Registered[0].Ref != res.IRN {
		t.Fatalf("frozen ref %q != minted IRN %q", cored.Registered[0].Ref, res.IRN)
	}
	if got := stepNames(t, admin, wf); !reflect.DeepEqual(got, []string{"generate", "freeze"}) {
		t.Fatalf("post-restart table state is %v, want [generate freeze]", got)
	}
	// The `generate` row was REUSED, not rewritten: its created_at is untouched.
	// (An independent way of saying "the external call was not re-executed".)
	if got := stepCreatedAt(t, admin, wf, "generate"); !got.Equal(generateAt) {
		t.Fatalf("generate row was rewritten across the restart: %s -> %s", generateAt, got)
	}

	// --- NON-VACUITY CONTROL --------------------------------------------------
	// The exact same restart scenario with the IN-MEMORY store (what statutoryd
	// used before this workstream) calls the provider TWICE: the restart loses
	// the stored `generate` result. If this control ever reports 1, the test
	// above proves nothing.
	memChaos := NewChaosProvider(FakeProvider{})
	memCored := &FakeCoredClient{Snapshot: conformantSnapshot(), FailRegisterTimes: 1}
	cid2, vid2 := "co-"+uuid.NewString(), "v-"+uuid.NewString()
	if _, err := GenerateIRNWorkflow(context.Background(), NewMemStore(), memChaos, memCored, cid2, vid2, conformantSnapshot()); err == nil {
		t.Fatal("control: first attempt should fail in freeze")
	}
	if memChaos.InnerCalls() != 1 {
		t.Fatalf("control: want 1 provider call before the crash, got %d", memChaos.InnerCalls())
	}
	runUntilDoneIDs(t, NewMemStore(), memChaos, memCored, cid2, vid2, conformantSnapshot()) // "restart"
	if memChaos.InnerCalls() != 2 {
		t.Fatalf("control is broken: MemStore must LOSE state across a restart and re-call the provider "+
			"(want 2 calls, got %d) — otherwise the PGStore assertion above is vacuous", memChaos.InnerCalls())
	}
}

// ---------------------------------------------------------------------------
// 5. ⛔⛔ THE POISONED WORKFLOW: a failed first attempt must NOT make a voucher
//    permanently un-invoiceable.
//
// THE DEFECT THIS PINS. `build` used to be a durable Step, and it runs BEFORE
// `validate`. So a first attempt with a bad snapshot recorded a bad payload in
// statutory_step and THEN failed validation — and every later attempt, including
// one carrying correct data, resumed from that memo and failed identically.
// Forever. There was no reset path anywhere; the only cure found in the wild was
// a hand-written DELETE.
//
// The realistic shape is exactly the one that happened: an e-invoice attempted
// with NO LINE ITEMS (cored's GetVoucher does not carry them), which fails the
// NIC schema with "[ItemList: 0 items, minimum 1]" — a message that blames the
// item list while the real obstacle is a memo.
// ---------------------------------------------------------------------------

func TestPGStoreAFailedFirstAttemptDoesNotPoisonTheVoucher(t *testing.T) {
	pool, admin := pgHarness(t)
	cid, vid, wf := freshIDs(t, admin)

	store := NewPGStore(pool)
	chaos := NewChaosProvider(FakeProvider{})
	cored := &FakeCoredClient{Snapshot: conformantSnapshot()}

	// --- attempt 1: NO LINE ITEMS. Permanently invalid, by NIC's schema. ------
	bad := conformantSnapshot()
	bad.Lines = nil
	_, err := GenerateIRNWorkflow(context.Background(), store, chaos, cored, cid, vid, bad)
	if err == nil {
		t.Fatal("an e-invoice with no line items must fail validation")
	}
	if !IsPermanent(err) {
		t.Fatalf("a schema violation is permanent, not transient: %v", err)
	}
	// It fails for the RIGHT reason — otherwise attempt 2 below could be passing
	// because attempt 1 never reached validation at all (§9.3).
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want a ValidationError, got %T: %v", err, err)
	}
	if !strings.Contains(strings.Join(ve.Violations, "; "), "ItemList") {
		t.Fatalf("want an ItemList violation, got %v", ve.Violations)
	}
	if chaos.InnerCalls() != 0 {
		t.Fatalf("an invalid payload must never reach the IRP, got %d calls", chaos.InnerCalls())
	}
	// ⛔ AND IT LEFT NOTHING BEHIND. This is the fix: the row that used to sit
	// here (`build`, holding "ItemList": []) was the poison.
	if got := stepNames(t, admin, wf); len(got) != 0 {
		t.Fatalf("a failed attempt persisted %v — a corrected retry would resume from it", got)
	}

	// --- attempt 2: the SAME voucher, corrected. It must succeed. ------------
	res, err := GenerateIRNWorkflow(context.Background(), store, chaos, cored, cid, vid, conformantSnapshot())
	if err != nil {
		t.Fatalf("the corrected attempt must succeed, got: %v", err)
	}
	if res.IRN == "" {
		t.Fatal("no IRN from the corrected attempt")
	}
	if chaos.InnerCalls() != 1 {
		t.Fatalf("want exactly one IRP call, got %d", chaos.InnerCalls())
	}
	if len(cored.Registered) != 1 || cored.Registered[0].Ref != res.IRN {
		t.Fatalf("freeze did not pin the minted IRN: %+v", cored.Registered)
	}
	if got := stepNames(t, admin, wf); !reflect.DeepEqual(got, []string{"generate", "freeze"}) {
		t.Fatalf("statutory_step holds %v, want [generate freeze]", got)
	}

	// --- ⛔ NON-VACUITY CONTROL: the OLD step list, run against the same store.
	// Without this, "attempt 2 succeeded" is equally consistent with the workflow
	// never having been poisonable — in which case the whole test proves nothing.
	// This models the pre-fix shape directly: memoize a pure `build` step BEFORE
	// validating it, on a fresh workflow id.
	cid2, vid2, wf2 := freshIDs(t, admin)
	poison := func(snap VoucherSnapshot) error {
		steps := []Step{
			{
				Name: "build",
				Run: func(_ context.Context, _ map[string]json.RawMessage) (json.RawMessage, error) {
					return json.Marshal(BuildEInvoice(snap))
				},
			},
			{
				Name: "validate",
				Run: func(_ context.Context, prev map[string]json.RawMessage) (json.RawMessage, error) {
					var p EInvoicePayload
					if err := json.Unmarshal(prev["build"], &p); err != nil {
						return nil, err
					}
					if v := ValidateEInvoice(p); len(v) > 0 {
						return nil, &ValidationError{Violations: v}
					}
					return json.Marshal(map[string]bool{"ok": true})
				},
			},
		}
		_, err := Run(context.Background(), store, "irn:"+cid2+":"+vid2, steps)
		return err
	}
	if err := poison(bad); err == nil {
		t.Fatal("control: the no-lines snapshot must fail validation")
	}
	if got := stepNames(t, admin, wf2); !reflect.DeepEqual(got, []string{"build"}) {
		t.Fatalf("control: the old shape must persist a `build` row, got %v", got)
	}
	// THE CONTROL'S POINT: corrected data, and it STILL fails — because `build`
	// is memoized. If this ever starts passing, the poisoning is not real and the
	// assertions above are vacuous.
	if err := poison(conformantSnapshot()); err == nil {
		t.Fatal("control is broken: a memoized `build` must make the corrected retry fail too — " +
			"otherwise the voucher was never poisonable and this whole test is vacuous")
	}
}

// ---------------------------------------------------------------------------
// 6. ⛔ A SETTLED WORKFLOW RETURNS ITS IRN AND IS NEVER RE-VALIDATED.
//
// §12/B.5 item 2: "an idempotent re-post returns the SAME IRN — that is the
// entire reason this plane is durable, and it is the one property whose failure
// mints a duplicate against a real government API."
//
// The sharp case is a re-post whose snapshot has since become INVALID (the
// voucher was altered, a line was removed). An IRN is a fact about what was
// SUBMITTED: it cannot be un-issued, so answering 422 to a voucher that
// demonstrably holds one is both wrong and unactionable. This is why
// GenerateIRNWorkflow asks the store whether `generate` is recorded BEFORE it
// derives or validates anything.
// ---------------------------------------------------------------------------

func TestPGStoreASettledWorkflowIsNeverRevalidated(t *testing.T) {
	pool, admin := pgHarness(t)
	cid, vid, wf := freshIDs(t, admin)

	store := NewPGStore(pool)
	chaos := NewChaosProvider(FakeProvider{})
	cored := &FakeCoredClient{Snapshot: conformantSnapshot()}

	first, err := GenerateIRNWorkflow(context.Background(), store, chaos, cored, cid, vid, conformantSnapshot())
	if err != nil {
		t.Fatalf("first attempt: %v", err)
	}
	if first.IRN == "" {
		t.Fatal("no IRN")
	}

	// The voucher has since lost its lines — a snapshot that would be REFUSED as a
	// first attempt (proved by the poisoning test above, which asserts exactly
	// this shape fails validation).
	stale := conformantSnapshot()
	stale.Lines = nil

	again, err := GenerateIRNWorkflow(context.Background(), store, chaos, cored, cid, vid, stale)
	if err != nil {
		t.Fatalf("a re-post of a settled workflow must succeed, got: %v", err)
	}
	if again.IRN != first.IRN {
		t.Fatalf("re-post minted a DIFFERENT IRN: %q then %q", first.IRN, again.IRN)
	}
	// And it did so without touching the IRP or the freeze hook again.
	if got := chaos.InnerCalls(); got != 1 {
		t.Fatalf("the IRP was called %d times, want exactly 1 — a second call is a duplicate IRN against a real government API", got)
	}
	if len(cored.Registered) != 1 {
		t.Fatalf("freeze called %d times, want 1", len(cored.Registered))
	}
	if got := stepNames(t, admin, wf); !reflect.DeepEqual(got, []string{"generate", "freeze"}) {
		t.Fatalf("statutory_step holds %v, want [generate freeze]", got)
	}
}
