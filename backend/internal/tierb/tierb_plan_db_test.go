package tierb_test

// tierb_plan_db_test.go — wave-2 review, finding 5: agent.tier_b_plan is a
// door for untrusted text too, and 0099's header said it could not run SET,
// DO or multi-statement text. It could: `EXECUTE 'EXPLAIN (FORMAT JSON) ' ||
// p_sql INTO p` runs every statement of a multi-statement string, so a DO
// block ran as agent_ro, the GUC was re-pointed, and a PREPARE survived the
// Tier-B rollback on the pooled connection. Go's Parse/Describe (the extended
// protocol refuses multi-statement text) was the only wall in front of it.
//
// Mutation proof (run for the review): tier_b_plan back to `EXECUTE ... INTO`
// turns this red — the call returns a plan, and 'planted' is prepared.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestTierBPlanRunsOneStatementOrNone(t *testing.T) {
	pool := setup(t)
	b := newTenant(t, pool, "TierB PlanB")
	const attack = `SELECT 1; PREPARE planted AS SELECT 42; ` +
		`DO $d$ BEGIN RAISE NOTICE 'DO ran as %', current_user; END $d$; ` +
		`SET app.current_company_id = '00000000-0000-0000-0000-000000000001'; SELECT '{}'::json`
	var planErr, probeErr, ctlErr error
	var prepared int
	var guc string
	_ = pool.RunInTx(b.ctx, func(ctx context.Context, tx pgx.Tx) error {
		for _, s := range []string{"SELECT agent.tier_b_bind()", "SET LOCAL transaction_read_only = on", "SET LOCAL ROLE agent_ro"} {
			if _, err := tx.Exec(ctx, s); err != nil {
				probeErr = err
				return err
			}
		}
		sp, err := tx.Begin(ctx)
		if err != nil {
			probeErr = err
			return err
		}
		var plan []byte
		planErr = sp.QueryRow(ctx, `SELECT agent.tier_b_plan($1)`, attack).Scan(&plan)
		_ = sp.Rollback(ctx)
		probeErr = tx.QueryRow(ctx, `SELECT count(*) FROM pg_prepared_statements WHERE name = 'planted'`).Scan(&prepared)
		if probeErr == nil {
			probeErr = tx.QueryRow(ctx, `SELECT current_setting('app.current_company_id')`).Scan(&guc)
		}
		// The control: a single SELECT still plans.
		ctlErr = tx.QueryRow(ctx, `SELECT agent.tier_b_plan('SELECT 1')`).Scan(&plan)
		return errors.New("rollback")
	})
	if probeErr != nil {
		t.Fatalf("probe: %v", probeErr)
	}
	if planErr == nil {
		t.Errorf("tier_b_plan ran a multi-statement string (DO, SET, PREPARE) and returned a plan")
	} else if !strings.Contains(planErr.Error(), "multi-query") && !strings.Contains(planErr.Error(), "cannot open") {
		t.Errorf("tier_b_plan refused the attack for an unexpected reason: %v", planErr)
	}
	if prepared != 0 {
		t.Errorf("a PREPARE inside tier_b_plan's text is on the connection (%d)", prepared)
	}
	if guc == "00000000-0000-0000-0000-000000000001" {
		t.Errorf("tier_b_plan's text re-pointed the tenant GUC")
	}
	if ctlErr != nil {
		t.Errorf("CONTROL: a single SELECT no longer plans: %v", ctlErr)
	}
}
