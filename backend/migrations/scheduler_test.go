package migrations

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/khata/cored/internal/insights"
	"github.com/khata/cored/internal/platform/db"
)

func TestInsightFailuresDoNotStarveLaterCompanies(t *testing.T) {
	conn := privateMigrationDB(t)
	ctx := context.Background()
	if err := Apply(ctx, conn); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 101; i++ {
		cid := fmt.Sprintf("00000000-0000-4000-8000-%012d", i)
		name := "failing"
		if i == 101 {
			name = "healthy"
		}
		if _, err := conn.Exec(ctx, `INSERT INTO company (id,company_id,name,fy_start_date) VALUES ($1,$1,$2,'2025-04-01')`, cid, name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := conn.Exec(ctx, `CREATE FUNCTION test_nightly_failure() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN IF EXISTS (SELECT 1 FROM company WHERE id=NEW.company_id AND name='failing') THEN
	RAISE EXCEPTION 'fixture: persistent failure'; END IF; RETURN NEW; END $$;
	CREATE TRIGGER test_nightly_failure BEFORE INSERT ON insight_run FOR EACH ROW EXECUTE FUNCTION test_nightly_failure();`); err != nil {
		t.Fatal(err)
	}
	cfg := conn.Config().Copy()
	pool, err := db.New(ctx, fmt.Sprintf("host=%s port=%d dbname=%s user=tally_app password=tally_app sslmode=disable", cfg.Host, cfg.Port, cfg.Database))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	date := insights.BusinessDate(now)
	p := &insights.Poller{Pool: pool, Now: func() time.Time { return now }, Batch: 100}
	first, err := p.NightlyForStats(ctx, date)
	if err != nil {
		t.Fatal(err)
	}
	if first.Nightly.Failed != 100 {
		t.Fatalf("failure control: %+v", first)
	}
	second, err := p.NightlyForStats(ctx, date)
	if err != nil {
		t.Fatal(err)
	}
	if second.Nightly.Succeeded != 1 || second.Digest.Succeeded != 1 {
		t.Fatalf("healthy company starved behind failures: %+v", second)
	}
	var waiting int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM insight_job WHERE kind='nightly' AND attempts=1 AND next_attempt_at>$1`, now).Scan(&waiting); err != nil || waiting != 100 {
		t.Fatalf("persistent backoff: %d %v", waiting, err)
	}

	// A failed digest remains due under its ORIGINAL business date after midnight.
	if _, err := conn.Exec(ctx, `DROP TRIGGER test_nightly_failure ON insight_run;
	CREATE FUNCTION test_digest_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture: digest failure'; END $$;
	CREATE TRIGGER test_digest_failure BEFORE INSERT ON insight_digest FOR EACH ROW EXECUTE FUNCTION test_digest_failure();`); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	failed, err := p.NightlyForStats(ctx, date)
	if err != nil || failed.Digest.Failed != 100 {
		t.Fatalf("digest failure control: %+v %v", failed, err)
	}
	if _, err := conn.Exec(ctx, `DROP TRIGGER test_digest_failure ON insight_digest`); err != nil {
		t.Fatal(err)
	}
	now = now.Add(24 * time.Hour)
	if _, err := p.NightlyForStats(ctx, insights.BusinessDate(now)); err != nil {
		t.Fatal(err)
	}
	var delivered int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM insight_digest WHERE digest_date=$1::date`, date.Format("2006-01-02")).Scan(&delivered); err != nil || delivered != 101 {
		t.Fatalf("old digests lost at midnight: %d %v", delivered, err)
	}
}

func TestInsightClaimsAreExclusiveAndRecoverAfterLeaseExpiry(t *testing.T) {
	conn := privateMigrationDB(t)
	ctx := context.Background()
	if err := Apply(ctx, conn); err != nil {
		t.Fatal(err)
	}
	cid := uuid.NewString()
	if _, err := conn.Exec(ctx, `INSERT INTO company(id,company_id,name,fy_start_date) VALUES ($1,$1,'claim fixture','2025-04-01')`, cid); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	claim := func(at time.Time) []uuid.UUID {
		t.Helper()
		rows, err := conn.Query(ctx, `SELECT claim_token FROM insight_claim_jobs('nightly','2026-10-21',$1,100)`, at)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var tokens []uuid.UUID
		for rows.Next() {
			var token uuid.UUID
			if err := rows.Scan(&token); err != nil {
				t.Fatal(err)
			}
			tokens = append(tokens, token)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return tokens
	}
	first := claim(now)
	if len(first) != 1 {
		t.Fatalf("first claim: %v", first)
	}
	if second := claim(now); len(second) != 0 {
		t.Fatalf("another poller claimed a leased job: %v", second)
	}
	last := claim(now.Add(6 * time.Minute))
	if len(last) != 1 || last[0] == first[0] {
		t.Fatalf("interrupted lease was not replaced: %v", last)
	}
}

// Concurrent schedulers must not both own the same job, even while the first
// invocation is creating the pending row. A scoped tick never claims a neighbor.
func TestInsightConcurrentAndScopedClaims(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn := privateMigrationDB(t)
	if err := Apply(ctx, conn); err != nil {
		t.Fatal(err)
	}
	cid, other := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{cid, other} {
		if _, err := conn.Exec(ctx, `INSERT INTO company(id,company_id,name,fy_start_date) VALUES ($1,$1,'claim race','2025-04-01')`, id); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	results := make(chan struct {
		count int
		err   error
	}, 2)
	now := time.Now()
	for i := 0; i < 2; i++ {
		go func() {
			c, err := pgx.ConnectConfig(ctx, conn.Config().Copy())
			if err != nil {
				results <- struct {
					count int
					err   error
				}{0, err}
				return
			}
			defer c.Close(context.Background())
			<-start
			var n int
			err = c.QueryRow(ctx, `SELECT count(*) FROM insight_claim_jobs('nightly','2026-10-21',$1,100,$2)`, now, cid).Scan(&n)
			results <- struct {
				count int
				err   error
			}{n, err}
		}()
	}
	close(start)
	n := 0
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		n += r.count
	}
	if n != 1 {
		t.Fatalf("concurrent owners: %d", n)
	}
	var unscoped int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM insight_job WHERE company_id=$1`, other).Scan(&unscoped); err != nil || unscoped != 0 {
		t.Fatalf("scoped claim enqueued neighbor: %d %v", unscoped, err)
	}
}

func TestInsightClaimSkipsACompanyBeingDeleted(t *testing.T) {
	conn := privateMigrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := Apply(ctx, conn); err != nil {
		t.Fatal(err)
	}
	cid := uuid.New()
	if _, err := conn.Exec(ctx, `INSERT INTO company(id,company_id,name,fy_start_date) VALUES ($1,$1,'deleting','2025-04-01')`, cid); err != nil {
		t.Fatal(err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `DELETE FROM company WHERE id=$1`, cid); err != nil {
		t.Fatal(err)
	}
	other, err := pgx.ConnectConfig(ctx, conn.Config().Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close(context.Background())
	var count int
	if err = other.QueryRow(ctx, `SELECT count(*) FROM insight_claim_jobs('nightly','2026-10-21',now(),100)`).Scan(&count); err != nil {
		t.Fatalf("claim blocked or raced company deletion: %v", err)
	}
	if count != 0 {
		t.Fatalf("deleting company was claimed: %d", count)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
