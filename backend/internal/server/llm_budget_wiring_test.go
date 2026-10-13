package server

// llm_budget_wiring_test.go pins the ONE DELIBERATE EXCEPTION to
// actions.ActionSpec.IsWrite: a route the generated registry records as
// `display query`, is_write=false, causes rows to be INSERTed and UPDATEd.
//
// ⛔ THE EXCEPTION IS CORRECT AND IT IS THE INVISIBILITY THAT IS DANGEROUS.
// Metering has to write on every action, reads included, or the cap is not a
// cap. buildBudget's comment says that in terms — IsWrite describes THE BOOKS,
// not "touches no table" — and W2 is going to build an autonomy table on
// IsWrite, so the exception needs a test and not only a paragraph.
//
// WHAT IS ACTUALLY ASSERTED, precisely, because it is easy to state this one
// too strongly: llm.PGBudget's write opens ITS OWN read-write transaction from
// the pool, so it commits even when it is called from inside the read-only,
// RLS-scoped transaction that executes a tenant's query — and it stays
// committed when that transaction rolls back. That is the property that makes
// the exception safe today (handleQuery translates BEFORE it executes, so the
// two are not nested yet) and the property a future refactor would break: hand
// PGBudget the ambient pgx.Tx instead of the Pool — the obvious "reuse the
// caller's transaction" cleanup — and Postgres answers "cannot execute UPDATE
// in a read-only transaction" on a customer's query. This test goes red there,
// in CI, instead of at runtime.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/llm"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/migrations"
)

// errRollbackTheRequest is returned from the read-only transaction to force a
// ROLLBACK. It stands for the request failing after metering has already
// happened, which is the case that decides whether a tenant's spend survives a
// failed query — see step 4.
var errRollbackTheRequest = errors.New("deliberate rollback: the request failed after metering")

// budgetWiringPool brings up the app pool and a company.
//
// ⛔ AN UNREACHABLE DATABASE FAILS. Same rule, same reason, as
// newITCLiveServer above (handler_itc_test.go:657): CORED_SKIP_DB is an
// operator saying this environment has no Postgres, and it skips; a connect
// error with the guard unset is a machine that was supposed to have one, and a
// green run that silently skipped the only test of a spend control is worse
// than a red one.
func budgetWiringPool(t *testing.T) (*db.Pool, uuid.UUID) {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("CORED_SKIP_DB set")
	}
	ctx := context.Background()
	adminConn, err := pgx.Connect(ctx, testdb.AdminDSNValue())
	if err != nil {
		t.Fatalf("admin postgres unavailable: %v — this is the only test of the metering "+
			"write's transaction boundary and it must not be reported as passing", err)
	}
	if err := migrations.Apply(ctx, adminConn); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("apply migrations: %v", err)
	}
	_ = adminConn.Close(ctx)

	pool, err := db.New(ctx, itcTestAppDSN)
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}
	t.Cleanup(pool.Close)

	companyID := uuid.Must(uuid.NewV7())
	cctx := db.WithCompany(ctx, companyID)
	if _, err := identity.NewService(pool).CreateCompany(cctx, identity.Company{
		ID: companyID, Name: "LLM Budget Wiring Co " + companyID.String()[:8], FYStartDate: "2026-04-01",
	}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	testdb.Cleanup(t, companyID)
	return pool, companyID
}

func TestTokenBudgetWriteIsOutsideTheReadOnlyRequestTx(t *testing.T) {
	pool, companyID := budgetWiringPool(t)
	ctx := db.WithCompany(context.Background(), companyID)

	// 1. Go through buildBudget rather than constructing a PGBudget, because the
	//    composition root is the thing under test: an operator who sets this
	//    variable must get the WRITING budget, and a test that built the struct
	//    itself would still pass if buildBudget stopped honouring the knob.
	t.Setenv("LLM_BUDGET_TOKENS_PER_MONTH", "100000")
	budget, err := buildBudget(pool)
	if err != nil {
		t.Fatalf("buildBudget: %v", err)
	}
	if _, ok := budget.(*llm.PGBudget); !ok {
		t.Fatalf("buildBudget returned %T with a cap configured, want *llm.PGBudget — "+
			"every assertion below would pass vacuously on llm.NoBudget, which writes nothing", budget)
	}

	const feature = "nlq.translate"

	// ⛔ NOTHING INSIDE THE CLOSURE CALLS t.Fatal, AND THAT IS NOT STYLE.
	// t.Fatalf runs runtime.Goexit, so a Fatal inside a TxFunc unwinds PAST
	// RunInReadOnlyTx's rollback: the transaction is never closed, its
	// connection is never returned to the pool, and the NEXT acquire — step 5,
	// two lines down — blocks until the test binary's timeout. Measured, on the
	// mutation run that was supposed to make this test go red: instead of a
	// failure it produced a ten-minute hang and a killed CI slot. Every outcome
	// is therefore carried OUT of the closure and asserted after it returns.
	var (
		reserved        llm.Reservation
		ambientWriteErr error
		reserveErr      error
		settleErr       error
	)
	txErr := pool.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// 2. THE ORACLE. Everything in step 3 would also pass if this
		//    transaction were not really read-only, so first establish that it
		//    is: a write on the AMBIENT tx must be refused by Postgres itself,
		//    independent of role grants.
		_, ambientWriteErr = tx.Exec(ctx,
			`UPDATE llm_token_budget SET calls = calls + 1 WHERE company_id = $1`, companyID)

		// 3. THE ASSERTION. Metering, called from inside that same read-only
		//    request transaction, still succeeds — because PGBudget takes its
		//    own connection and its own READ-WRITE transaction from the pool.
		//    This is the arm a "reuse the caller's transaction" refactor breaks.
		r, rerr := budget.Reserve(ctx, companyID, feature, 500)
		reserveErr = rerr
		if rerr == nil {
			reserved = r
			settleErr = r.Settle(ctx, llm.Usage{PromptTokens: 400, CompletionTokens: 50})
		}

		// 4. Fail the request AFTER metering. A rollback here must not unwind
		//    the spend: tokens that were bought are billed whether or not the
		//    answer reached the tenant, and a metering write that rolled back
		//    with the request would let a tenant loop on failing queries for
		//    free.
		return errRollbackTheRequest
	})

	if ambientWriteErr == nil {
		t.Error("the request's execution transaction accepted an UPDATE, so it is NOT read-only — " +
			"step 3 proves nothing and the query path has lost its defence-in-depth")
	} else if !strings.Contains(ambientWriteErr.Error(), "read-only transaction") {
		t.Errorf("the ambient write failed with %v, want Postgres's read-only refusal — "+
			"if it failed for some other reason this oracle is measuring the wrong thing", ambientWriteErr)
	}
	if reserveErr != nil {
		t.Fatalf("Reserve inside the read-only request tx: %v — the metering plane MUST be able to "+
			"write on a read action, or POST /query is uncapped for a principal holding only "+
			"display rights", reserveErr)
	}
	if settleErr != nil {
		t.Fatalf("Settle inside the read-only request tx: %v", settleErr)
	}
	if !errors.Is(txErr, errRollbackTheRequest) {
		t.Fatalf("read-only tx returned %v, want the deliberate rollback — the assertions above "+
			"only mean something if the transaction actually ran and then rolled back", txErr)
	}
	if reserved == nil {
		t.Fatal("no reservation was taken")
	}

	// 5. The row is there, with the SERVED figures, after the request rolled
	//    back. Read on a fresh transaction so nothing is being read out of the
	//    aborted one.
	var prompt, completion, calls int64
	if err := pool.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT prompt_tokens, completion_tokens, calls
			  FROM llm_token_budget
			 WHERE company_id = $1 AND feature = $2
			 ORDER BY period_start DESC
			 LIMIT 1`, companyID, feature).Scan(&prompt, &completion, &calls)
	}); err != nil {
		t.Fatalf("reading the metering row back: %v — the write did not survive the request's "+
			"rollback, which means the spend was never recorded", err)
	}
	if prompt != 400 || completion != 50 || calls != 1 {
		t.Errorf("metering row = prompt %d completion %d calls %d, want 400/50/1", prompt, completion, calls)
	}
}
