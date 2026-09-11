package llm

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/migrations"
)

// DB test discipline (§1.4): CORED_SKIP_DB guard FIRST, then connect-or-FAIL.
// The two DSNs are copied verbatim from internal/forecast/forecast_db_test.go —
// _test.go identifiers do not cross packages.
const (
	adminDSN = "postgres://postgres:postgres@localhost:5433/tally?sslmode=disable"
	appDSN   = "postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable"
)

// budgetFixture migrates, opens the app pool, creates a company and registers
// its teardown. It returns the pool and the company id.
//
// ⛔ AN UNREACHABLE DATABASE IS A FAILURE HERE, NOT A SKIP, AND THE ASYMMETRY
// WITH CORED_SKIP_DB IS THE WHOLE POINT. CORED_SKIP_DB is an operator SAYING
// "there is no Postgres in this environment" — a stated choice, so it skips.
// A connect error with the guard unset is the opposite: nobody said anything,
// the database was supposed to be there, and it was not. Skipping that arm
// printed `ok  github.com/khata/cored/internal/llm` on a machine where the
// exhaustion path — the code that decides whether a tenant can spend without
// limit — never executed, and a green run containing an invisible skip is
// indistinguishable from a green run that proved something.
//
// newITCLiveServer in internal/server/handler_itc_test.go:657 is the precedent
// and its reason is the same one in one line: "a skipped pin is
// indistinguishable from a passing one".
func budgetFixture(t *testing.T) (*db.Pool, uuid.UUID) {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("CORED_SKIP_DB set")
	}
	ctx := context.Background()
	adminConn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("admin postgres unavailable: %v — the token-cap tests cannot run, and they "+
			"must not be reported as passing. Start the dev database (or set CORED_SKIP_DB to "+
			"say out loud that this environment has none)", err)
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
	t.Cleanup(pool.Close)

	companyID := uuid.Must(uuid.NewV7())
	cctx := db.WithCompany(ctx, companyID)
	ids := identity.NewService(pool)
	if _, err := ids.CreateCompany(cctx, identity.Company{
		ID: companyID, Name: "LLM Budget Co " + companyID.String()[:8], FYStartDate: "2026-04-01",
	}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	// Teardown registered the moment the company exists, per the repo's
	// standing rule (internal/testhygiene): every test that brings a company
	// into existence hands it to testdb.
	testdb.Cleanup(t, companyID)
	return pool, companyID
}

// TestPGBudget_ReserveSettleExhaust drives the real table through the real RLS
// session. It is the only test in this package that needs a database.
func TestPGBudget_ReserveSettleExhaust(t *testing.T) {
	pool, companyID := budgetFixture(t)
	ctx := db.WithCompany(context.Background(), companyID)

	// A frozen clock, so the window is deterministic and a month boundary
	// during a CI run cannot split the assertions across two rows.
	frozen := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	b := &PGBudget{Pool: pool, DefaultLimitTokens: 1000, Now: fixedClock(frozen)}

	// 1. A reservation inside the cap succeeds and creates the window row.
	r1, err := b.Reserve(ctx, companyID, "nlq.translate", 400)
	if err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	row := readBudgetRow(t, pool, ctx, companyID, "nlq.translate")
	if row.reserved != 400 || row.limit != 1000 || row.calls != 0 {
		t.Errorf("after reserve: %+v, want reserved=400 limit=1000 calls=0", row)
	}

	// 2. Settling replaces the ESTIMATE with the SERVED figures.
	if err := r1.Settle(ctx, Usage{PromptTokens: 300, CachedPromptTokens: 250, CompletionTokens: 20}); err != nil {
		t.Fatalf("settle: %v", err)
	}
	row = readBudgetRow(t, pool, ctx, companyID, "nlq.translate")
	if row.reserved != 0 || row.prompt != 300 || row.cached != 250 || row.completion != 20 || row.calls != 1 {
		t.Errorf("after settle: %+v", row)
	}

	// 3. A second Settle must not double-count. (The reservation is done; the
	//    database CHECK would refuse a negative reserved_tokens, which is the
	//    belt to this brace.)
	if err := r1.Settle(ctx, Usage{PromptTokens: 999}); err != nil {
		t.Fatalf("second settle: %v", err)
	}
	row = readBudgetRow(t, pool, ctx, companyID, "nlq.translate")
	if row.prompt != 300 || row.calls != 1 {
		t.Errorf("a second Settle double-counted: %+v", row)
	}

	// 4. Release hands the estimate back and does NOT count a call.
	r2, err := b.Reserve(ctx, companyID, "nlq.translate", 100)
	if err != nil {
		t.Fatalf("second reserve: %v", err)
	}
	if err := r2.Release(ctx); err != nil {
		t.Fatalf("release: %v", err)
	}
	row = readBudgetRow(t, pool, ctx, companyID, "nlq.translate")
	if row.reserved != 0 || row.calls != 1 || row.released != 1 {
		t.Errorf("after release: %+v, want reserved=0 calls=1 released=1", row)
	}

	// 5. EXHAUSTION. 320 settled + a 700 reservation exceeds the 1000 cap.
	if _, err := b.Reserve(ctx, companyID, "nlq.translate", 700); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("over-cap reserve: err = %v, want ErrBudgetExhausted", err)
	}
	// CONTROL: a reservation that FITS still succeeds, or step 5 would also
	// pass on an implementation that refused everything.
	r3, err := b.Reserve(ctx, companyID, "nlq.translate", 600)
	if err != nil {
		t.Fatalf("control: an in-cap reserve was refused: %v", err)
	}
	if err := r3.Release(ctx); err != nil {
		t.Fatalf("control release: %v", err)
	}

	// 6. A DIFFERENT FEATURE has its own row and its own cap. This is what the
	//    (company, period, feature) key buys: "the bill tripled" is answered by
	//    which feature moved.
	r4, err := b.Reserve(ctx, companyID, "bill.extract", 900)
	if err != nil {
		t.Fatalf("other feature reserve: %v", err)
	}
	if err := r4.Settle(ctx, Usage{PromptTokens: 800, CompletionTokens: 50}); err != nil {
		t.Fatalf("other feature settle: %v", err)
	}
	other := readBudgetRow(t, pool, ctx, companyID, "bill.extract")
	if other.prompt != 800 {
		t.Errorf("bill.extract row = %+v", other)
	}
	same := readBudgetRow(t, pool, ctx, companyID, "nlq.translate")
	if same.prompt != 300 {
		t.Errorf("the other feature's spend leaked into nlq.translate: %+v", same)
	}

	// 7. A NEW MONTH is a new window with a fresh cap. Without the period key a
	//    tenant that tripped the cap once would be refused forever.
	nextMonth := &PGBudget{Pool: pool, DefaultLimitTokens: 1000,
		Now: fixedClock(frozen.AddDate(0, 1, 0))}
	r5, err := nextMonth.Reserve(ctx, companyID, "nlq.translate", 900)
	if err != nil {
		t.Fatalf("next month reserve: %v", err)
	}
	if err := r5.Release(ctx); err != nil {
		t.Fatalf("next month release: %v", err)
	}
}

// TestPGBudget_RefusesTenantScopeMismatch is the tenant-isolation control: a
// session scoped to company A may not bill company B, and the refusal happens in
// Go before the statement is issued rather than as a confusing RLS error later.
func TestPGBudget_RefusesTenantScopeMismatch(t *testing.T) {
	pool, companyID := budgetFixture(t)
	ctx := db.WithCompany(context.Background(), companyID)
	b := &PGBudget{Pool: pool, DefaultLimitTokens: 1000}

	other := uuid.Must(uuid.NewV7())
	if _, err := b.Reserve(ctx, other, "f", 10); !errors.Is(err, ErrBudgetScopeMismatch) {
		t.Errorf("err = %v, want ErrBudgetScopeMismatch", err)
	}
	// And with no company in context at all, the db layer's own refusal wins,
	// before any SQL is issued.
	if _, err := b.Reserve(context.Background(), companyID, "f", 10); !errors.Is(err, db.ErrNoCompany) {
		t.Errorf("no-company err = %v, want db.ErrNoCompany", err)
	}
	// CONTROL: the matching id works.
	if _, err := b.Reserve(ctx, companyID, "f", 10); err != nil {
		t.Errorf("control: matching tenant id was refused: %v", err)
	}
}

type budgetRow struct {
	limit      int64
	reserved   int64
	prompt     int64
	cached     int64
	completion int64
	calls      int64
	released   int64
}

func readBudgetRow(t *testing.T, pool *db.Pool, ctx context.Context, companyID uuid.UUID, feature string) budgetRow {
	t.Helper()
	var r budgetRow
	err := pool.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT limit_tokens, reserved_tokens, prompt_tokens, cached_prompt_tokens,
			       completion_tokens, calls, released
			  FROM llm_token_budget
			 WHERE company_id = $1 AND feature = $2
			 ORDER BY period_start DESC
			 LIMIT 1`, companyID, feature).
			Scan(&r.limit, &r.reserved, &r.prompt, &r.cached, &r.completion, &r.calls, &r.released)
	})
	if err != nil {
		t.Fatalf("read budget row (%s): %v", feature, err)
	}
	return r
}
