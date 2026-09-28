package agent

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/intentlog"
	"github.com/khata/cored/internal/platform/db"
)

// Poller is D120 F5: the poll-driven re-invoker. It finds due runs through
// migration 0094's SECURITY DEFINER agent_runs_due() (ids only — the
// notify-outbox shape, rather than an RLS bypass) and Resumes each under its
// own company scope.
//
// ⛔ IT HOLDS NO STATE ACROSS TICKS, AND THAT IS THE DESIGN. Everything a
// parked run needs is in agent_run / agent_approval / agent_decision; a
// poller that died mid-tick loses nothing, and a second cored instance polling
// the same rows is safe because Resume runs under the run's advisory lock —
// the loser returns at once (durable.ErrLocked) without executing a step.
//
// HOW OFTEN A PARKED RUN IS RETRIED, AND HOW THAT MEETS THE LOCK. A parked run
// is due again after Engine.ParkedPoll (1 minute); a decision written through
// the approval API sets next_poll_at = now, so an approval takes effect on the
// next tick rather than a minute later. A tick takes the lock only for as long
// as one Resume takes, and pg_try_advisory_lock never blocks, so a slow run
// cannot stall the tick for other runs on another instance.
type Poller struct {
	Engine *Engine
	// Batch bounds runs per tick, so one tick cannot hold the process.
	Batch int
}

// RunOnce resumes every due run once and returns how many it resumed.
// Exported so tests (and an operator) can drive it deterministically.
func (p *Poller) RunOnce(ctx context.Context) (int, error) {
	batch := p.Batch
	if batch <= 0 {
		batch = 50
	}
	now := p.Engine.now()
	rows, err := p.Engine.Pool.Raw().Query(ctx, `SELECT company_id, run_id FROM agent_runs_due($1, $2)`, now, batch)
	if err != nil {
		return 0, fmt.Errorf("agent: due runs: %w", err)
	}
	type due struct{ company, run uuid.UUID }
	var work []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.company, &d.run); err != nil {
			rows.Close()
			return 0, err
		}
		work = append(work, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, d := range work {
		if _, err := p.Engine.Resume(ctx, d.company, d.run); err != nil {
			// One run's failure must not stop the others (notify's rule).
			log.Printf("agent: resume run %s: %v", d.run, err)
			continue
		}
		n++
	}
	return n, nil
}

// Run polls on a ticker, and immediately whenever kick fires, until ctx ends.
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
		if _, err := p.RunOnce(ctx); err != nil {
			log.Printf("agent: poll: %v", err)
		}
	}
}

// AnchorLag is how far behind now an anchor is taken. An intent row's
// created_at is stamped app-side before its transaction commits, so a row can
// become visible AFTER an anchor whose instant is later than its timestamp; an
// anchor taken at now-AnchorLag commits only to rows old enough that every
// transaction that could have written them has long finished.
const AnchorLag = 5 * time.Minute

// AnchorOnce writes one intent-log anchor for every company with rows newer
// than its latest anchor (0092's agent_intent_unanchored_companies). The
// "nightly" of PLAN-AGENTIC W2 is how often cored calls it.
func (p *Poller) AnchorOnce(ctx context.Context) (int, error) {
	rows, err := p.Engine.Pool.Raw().Query(ctx, `SELECT company_id FROM agent_intent_unanchored_companies(100)`)
	if err != nil {
		return 0, fmt.Errorf("agent: unanchored companies: %w", err)
	}
	var companies []uuid.UUID
	for rows.Next() {
		var c uuid.UUID
		if err := rows.Scan(&c); err != nil {
			rows.Close()
			return 0, err
		}
		companies = append(companies, c)
	}
	rows.Close()
	asOf := p.Engine.now().Add(-AnchorLag)
	n := 0
	for _, c := range companies {
		err := p.Engine.Pool.RunInTx(db.WithCompany(ctx, c), func(ctx context.Context, tx pgx.Tx) error {
			_, err := intentlog.Anchor(ctx, tx, c, asOf)
			return err
		})
		if err != nil {
			log.Printf("agent: anchor company %s: %v", c, err)
			continue
		}
		n++
	}
	return n, nil
}
