package durable

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AdvisoryLocker is the TryLock half of a Postgres-backed StepStore: a
// session-level pg_try_advisory_lock on ONE pooled connection, held for as long
// as the caller holds the release func.
//
// WHY AN ADVISORY LOCK AND NOT A LEASE ROW. D120 §4 measured both halves: with
// no lock, two pollers executed the money step twice; with this lock, under the
// identical widened window, exactly once — with no table, no migration and no
// dependency. A lease row would need a column, a clock and a reaper for leases
// whose holder died; an advisory lock is released by Postgres the instant the
// holding session ends, so a crashed cored cannot strand a run.
//
// ⛔ THE CONNECTION IS TAKEN OUT OF THE POOL FOR THE WHOLE RUN, and that is the
// point rather than a cost to optimise away. A session-level lock belongs to a
// SESSION: acquiring it on a pooled connection and releasing that connection
// would hand the lock to whatever request borrowed the connection next, and
// "unlock" would then run on a different session and silently do nothing.
//
// ⚠️ hashtextextended IS 64-BIT, hashtext (the spike's spelling) IS 32-BIT. A
// collision between two workflow ids makes one of them report ErrLocked while
// the other runs — which is SAFE (nothing double-executes; the loser comes back
// next tick) but is a stall, and at 32 bits the birthday bound is ~77k live
// runs. 64 bits moves it past anything this product will hold.
type AdvisoryLocker struct {
	Pool *pgxpool.Pool
	// Namespace keeps two stores' workflow ids from sharing a lock space:
	// "statutory" for the IRN plane, "agent" for agent runs.
	Namespace string
}

// TryLock implements StepStore.TryLock.
func (l AdvisoryLocker) TryLock(ctx context.Context, workflowID string) (func(), bool, error) {
	if l.Pool == nil {
		return nil, false, fmt.Errorf("durable: AdvisoryLocker has no pool")
	}
	key := l.Namespace + ":" + workflowID
	conn, err := l.Pool.Acquire(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("durable: acquire lock connection: %w", err)
	}
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, key).Scan(&got); err != nil {
		conn.Release()
		return nil, false, fmt.Errorf("durable: pg_try_advisory_lock: %w", err)
	}
	if !got {
		conn.Release()
		return nil, false, nil
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			// A fresh deadline, not the caller's ctx: the run may have ended
			// BECAUSE its ctx was cancelled, and an unlock on a cancelled ctx
			// would fail and leave the lock held on a pooled session.
			uctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var unlocked bool
			err := conn.QueryRow(uctx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, key).Scan(&unlocked)
			if err != nil || !unlocked {
				// ⛔ IF THE UNLOCK DID NOT DEMONSTRABLY HAPPEN, THE SESSION DIES.
				// Returning a connection that still holds the lock to the pool
				// would wedge this workflow for as long as that connection
				// lives — every later TryLock from THIS process would even
				// succeed (advisory locks are re-entrant per session) and
				// every other process's would fail. Closing the session is
				// the one release Postgres guarantees.
				c := conn.Hijack()
				_ = c.Close(uctx)
				return
			}
			conn.Release()
		})
	}
	return release, true, nil
}
