package insights

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

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
//   - NIGHTLY: once the IST clock passes NightlyHour, insight_nightly_due_companies
//     (today) → RunNightly; then insight_digest_due_companies (today: the night
//     ran, no digest yet) → ComposeDigest. Idempotent per (company, day) by
//     insight_run's UNIQUE and the digest's, so every replica may run the same
//     loop: the loser of a race finds the row and does nothing.

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

// NightlyFor runs the night for date regardless of the hour (the operator's
// "run now", and the tests).
func (p *Poller) NightlyFor(ctx context.Context, date time.Time) (int, error) {
	companies, err := p.ids(ctx, `SELECT company_id FROM insight_nightly_due_companies($1::date, $2)`,
		date.Format(isoDate), p.batch())
	if err != nil {
		return 0, err
	}
	n := 0
	for _, c := range companies {
		cctx := db.WithCompany(ctx, c)
		if _, err := RunNightly(cctx, p.Pool, c, date); err != nil {
			// One company's failure must not stop the others (notify's rule).
			log.Printf("insights: nightly for %s: %v", c, err)
			continue
		}
		n++
	}
	// ⛔ THE DIGEST IS DUE ON ITS OWN CONDITION (0104, the wave-2 review):
	// the night for the date ran and the digest does not exist — whichever
	// road ran the night. Composed in the nightly pass alone, a night run by
	// POST /insights/run was never followed by a digest, and a compose that
	// failed after its night was never retried.
	due, err := p.ids(ctx, `SELECT company_id FROM insight_digest_due_companies($1::date, $2)`,
		date.Format(isoDate), p.batch())
	if err != nil {
		return n, err
	}
	for _, c := range due {
		if _, err := ComposeDigest(db.WithCompany(ctx, c), p.Pool, c, date, "", p.Narrator); err != nil {
			log.Printf("insights: digest for %s: %v", c, err)
		}
	}
	return n, nil
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
