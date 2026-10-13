package testdb

import (
	"context"
	"fmt"
	"sync"

	"github.com/khata/cored/internal/platform/tenant"
)

// ---------------------------------------------------------------------------
// The delete plan
// ---------------------------------------------------------------------------
//
// THE CENSUS ITSELF LIVES IN internal/platform/tenant AND NOT HERE. It used to
// live in this file, and it had to move: the backup service needs the exact same
// enumeration, and TestNoProductionCodeImportsTestdb in internal/archtest
// forbids production code importing this package — correctly, because of the
// superuser DSN and the tenant-wipe next door in testdb.go. Rather than relax
// that rule or let backup keep a second list (see the tenant package comment on
// why a second list is the defect), the schema-derived part moved into a
// production package that carries no privilege of its own.
//
// What remains here is the PRIVILEGED half: the admin pool, and the memoisation
// of one census per test process. tenant.Census is pure catalog reading and
// caches nothing, which is right for a request-scoped backup and wrong for a
// suite that mints hundreds of companies — so the caching is here, next to the
// pool it shares.

var (
	planOnce sync.Once
	planVal  *tenant.Plan
	planErr  error
)

// loadPlan builds the teardown plan once per process and memoises it (including
// the error). Every teardown in the process shares it: the catalog queries run
// exactly once no matter how many companies the suite creates.
func loadPlan(ctx context.Context) (*tenant.Plan, error) {
	planOnce.Do(func() {
		planVal, planErr = buildPlan(ctx)
	})
	return planVal, planErr
}

// buildPlan takes an admin connection and asks tenant for the census.
func buildPlan(ctx context.Context) (*tenant.Plan, error) {
	p, err := adminPool(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := p.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("testdb: acquire admin conn for plan: %w", err)
	}
	defer conn.Release()
	return tenant.Census(ctx, conn)
}
