package insights

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/platform/db"
)

// poller.go is the scheduler: the repo's background-job shape (notify's
// outbox dispatcher, agent.Poller) — a ticker, a SECURITY DEFINER function
// that returns only company ids, then per-company work inside that company's
// RLS-bound transactions. No new job framework (River is named by D8; it is a
// module dependency, which IMPLEMENTATION_PLAN §1.1 forbids).
//
// Two jobs share the loop:
//
//   - REAL TIME: insight_event_pending_companies() → ProcessEvents. Seconds of
//     latency, by the tick.
//   - NIGHTLY: after NightlyHour, atomically claim durable nightly/digest jobs.
//     Leases keep replicas apart; failures back off without losing their date.
//     RunNightly and ComposeDigest remain idempotent if a lease expires.

// NightlyHour is the IST hour after which the night's run may start.
const NightlyHour = 2

// Poller drives both jobs.
type Poller struct {
	Pool     *db.Pool
	Now      func() time.Time
	Narrator Narrator
	Batch    int
}

func (p *Poller) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// EventsOnce processes pending voucher events for every company that has any.
func (p *Poller) EventsOnce(ctx context.Context) (int, error) {
	companies, err := p.ids(ctx, `SELECT company_id FROM insight_event_pending_companies($1)`, p.batch())
	if err != nil {
		return 0, err
	}
	n := 0
	for _, c := range companies {
		st, err := ProcessEvents(db.WithCompany(ctx, c), p.Pool, c, p.now(), p.batch())
		if err != nil {
			log.Printf("insights: events for %s: %v", c, err)
			continue
		}
		n += st.Events
	}
	return n, nil
}

// NightlyOnce runs the night (and the morning digest) for every company that
// has not had it for today's business date, once the IST hour allows.
func (p *Poller) NightlyOnce(ctx context.Context) (int, error) {
	now := p.now()
	if now.In(istZone).Hour() < NightlyHour {
		return 0, nil
	}
	return p.NightlyFor(ctx, BusinessDate(now))
}

// JobPass records selection separately from execution, so a missing digest can
// be distinguished from a selection, lease, or composition failure.
type JobPass struct{ Selected, Attempted, Succeeded, Failed int }
type PassStats struct{ Nightly, Digest JobPass }
type claimedJob struct {
	Company uuid.UUID
	Date    time.Time
	Token   uuid.UUID
}

// NightlyFor runs due work through date regardless of the current hour.
func (p *Poller) NightlyFor(ctx context.Context, date time.Time) (int, error) {
	stats, err := p.NightlyForStats(ctx, date)
	return stats.Nightly.Succeeded, err
}

func (p *Poller) NightlyForStats(ctx context.Context, date time.Time) (PassStats, error) {
	var stats PassStats
	for _, kind := range []string{"nightly", "digest"} {
		pass := &stats.Nightly
		if kind == "digest" {
			pass = &stats.Digest
		}
		jobs, err := p.claim(ctx, kind, date)
		if err != nil {
			return stats, err
		}
		pass.Selected = len(jobs)
		for _, job := range jobs {
			pass.Attempted++
			cctx := db.WithCompany(ctx, job.Company)
			var workErr error
			if kind == "nightly" {
				_, workErr = RunNightly(cctx, p.Pool, job.Company, job.Date)
			} else {
				_, workErr = ComposeDigest(cctx, p.Pool, job.Company, job.Date, "", p.Narrator)
			}
			if workErr != nil {
				pass.Failed++
				log.Printf("insights: %s company=%s date=%s failed: %v", kind, job.Company, job.Date.Format(isoDate), workErr)
			} else {
				pass.Succeeded++
			}
			if err := p.finishJob(cctx, kind, job, workErr); err != nil {
				return stats, err
			}
		}
		log.Printf("insights: %s date=%s selected=%d attempted=%d succeeded=%d failed=%d", kind, date.Format(isoDate), pass.Selected, pass.Attempted, pass.Succeeded, pass.Failed)
	}
	return stats, nil
}

func (p *Poller) claim(ctx context.Context, kind string, date time.Time) ([]claimedJob, error) {
	// An explicit company scope (operator run-now / a company test) must not
	// drain unrelated companies. The background scheduler has no company scope.
	var company any
	if cid, ok := db.CompanyFromContext(ctx); ok {
		company = cid
	}
	rows, err := p.Pool.Raw().Query(ctx, `SELECT company_id, work_date, claim_token FROM insight_claim_jobs($1, $2::date, $3, $4, $5)`, kind, date.Format(isoDate), p.now(), p.batch(), company)
	if err != nil {
		return nil, fmt.Errorf("insights: claim %s: %w", kind, err)
	}
	defer rows.Close()
	var jobs []claimedJob
	for rows.Next() {
		var job claimedJob
		if err := rows.Scan(&job.Company, &job.Date, &job.Token); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (p *Poller) finishJob(ctx context.Context, kind string, job claimedJob, workErr error) error {
	return p.Pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if workErr == nil {
			_, err := tx.Exec(ctx, `DELETE FROM insight_job WHERE company_id=$1 AND kind=$2 AND work_date=$3::date AND claim_token=$4`, job.Company, kind, job.Date.Format(isoDate), job.Token)
			return err
		}
		// The token prevents a worker whose lease expired from overwriting a
		// newer claim. Backoff is persisted, including the original work date.
		_, err := tx.Exec(ctx, `UPDATE insight_job SET lease_until='-infinity',
            next_attempt_at=$5::timestamptz + interval '1 minute' * power(2, least(attempts-1,6)),
            last_error=left($6,2000)
            WHERE company_id=$1 AND kind=$2 AND work_date=$3::date AND claim_token=$4`,
			job.Company, kind, job.Date.Format(isoDate), job.Token, p.now(), workErr.Error())
		return err
	})
}

// Run ticks both jobs until ctx ends. kick wakes it early (a voucher post).
func (p *Poller) Run(ctx context.Context, every time.Duration, kick <-chan struct{}) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-kick:
		}
		if _, err := p.EventsOnce(ctx); err != nil {
			log.Printf("insights: events: %v", err)
		}
		if _, err := p.NightlyOnce(ctx); err != nil {
			log.Printf("insights: nightly: %v", err)
		}
	}
}

func (p *Poller) batch() int {
	if p.Batch <= 0 {
		return 100
	}
	return p.Batch
}

func (p *Poller) ids(ctx context.Context, sql string, args ...any) ([]uuid.UUID, error) {
	rows, err := p.Pool.Raw().Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("insights: due companies: %w", err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
