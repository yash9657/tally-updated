package tierb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/platform/db"
)

// exec.go runs a validated query under every wall PLAN-AGENTIC §2.3 names:
//
//	READ ONLY transaction      db.RunInReadOnlyTx (a write errors in Postgres)
//	tenant GUC                 set by RunInReadOnlyTx as its first statement
//	SET LOCAL ROLE agent_ro    SELECT on agent.* only; RLS-bound; no base table
//	statement_timeout 5 s      SET LOCAL, so it dies with the transaction
//	search_path pg_catalog     nothing a later migration puts in public can shadow
//	EXPLAIN cost gate          refuse before running a query the planner prices high
//	forced LIMIT               printed by Query.SQL, and the scan stops at it too
//
// ⛔ THE ORDER OF THE SET LOCALs IS LOAD-BEARING. The role switch comes LAST,
// because once the session is agent_ro it cannot be trusted to have been
// able to set the others; and none of it can be undone from inside the query,
// because the query is a single SELECT the printer built (no RESET ROLE, no
// set_config — the function allow-list refuses it).

// Options bound one execution.
type Options struct {
	MaxRows int // forced LIMIT; default 200, hard ceiling 1000
	// MaxCost is the EXPLAIN total-cost ceiling. Default 50,000 planner units
	// — generous for a tenant's own views, and a query over it is a cross
	// join or an unindexed scan the agent should not be running
	// interactively. Tests lower it to prove the gate fires.
	MaxCost float64
	Timeout time.Duration // statement_timeout; default 5 s
}

// Hard ceilings the options cannot raise.
const (
	hardMaxRows = 1000
	defMaxRows  = 200
	defMaxCost  = 50000
	defTimeout  = 5 * time.Second
	hardTimeout = 5 * time.Second
)

// Result is the tabular answer. Values are strings, money in StringFixed(4),
// as the Tier-A DSL renders them.
type Result struct {
	SQL       string     `json:"sql"` // the RE-EMITTED text that ran
	Columns   []string   `json:"columns"`
	Rows      [][]string `json:"rows"`
	Cost      float64    `json:"plan_cost"`
	Truncated bool       `json:"truncated"`
}

// ErrTooExpensive is the cost-gate refusal.
var ErrTooExpensive = errors.New("tierb: the planner prices this query above the cost gate")

// ReadOnlyRunner is what Run needs (*db.Pool satisfies it).
type ReadOnlyRunner interface {
	RunInReadOnlyTx(ctx context.Context, fn db.TxFunc) error
}

// Run validates src and executes it. Validation failures wrap ErrRejected.
func Run(ctx context.Context, r ReadOnlyRunner, src string, opt Options) (Result, error) {
	q, err := Parse(src)
	if err != nil {
		return Result{}, err
	}
	return RunQuery(ctx, r, q, opt)
}

// RunQuery executes an already-validated query.
func RunQuery(ctx context.Context, r ReadOnlyRunner, q *Query, opt Options) (Result, error) {
	if opt.MaxRows <= 0 {
		opt.MaxRows = defMaxRows
	}
	if opt.MaxRows > hardMaxRows {
		opt.MaxRows = hardMaxRows
	}
	if opt.MaxCost <= 0 {
		opt.MaxCost = defMaxCost
	}
	if opt.Timeout <= 0 || opt.Timeout > hardTimeout {
		opt.Timeout = defTimeout
	}
	sql := q.SQL(opt.MaxRows)
	out := Result{SQL: sql, Rows: [][]string{}}
	err := r.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return runIn(ctx, tx, sql, opt, &out)
	})
	if err != nil {
		return Result{}, err
	}
	return out, nil
}

// sessionSetup is the constant preamble, in its load-bearing order.
func sessionSetup(timeout time.Duration) []string {
	return []string{
		fmt.Sprintf("SET LOCAL statement_timeout = %d", timeout.Milliseconds()),
		"SET LOCAL search_path = pg_catalog",
		"SET LOCAL standard_conforming_strings = on",
		"SET LOCAL ROLE agent_ro",
	}
}

// runIn is exported to the package's own DB tests through RunRawAsAgent,
// which bypasses the validator on purpose to prove the second wall.
func runIn(ctx context.Context, tx pgx.Tx, sql string, opt Options, out *Result) error {
	for _, s := range sessionSetup(opt.Timeout) {
		if _, err := tx.Exec(ctx, s); err != nil {
			return fmt.Errorf("tierb: session setup %q: %w", s, err)
		}
	}
	var plan []byte
	if err := tx.QueryRow(ctx, "EXPLAIN (FORMAT JSON) "+sql).Scan(&plan); err != nil {
		return fmt.Errorf("tierb: explain: %w", err)
	}
	cost, err := planCost(plan)
	if err != nil {
		return err
	}
	out.Cost = cost
	if cost > opt.MaxCost {
		return fmt.Errorf("%w (%.0f > %.0f)", ErrTooExpensive, cost, opt.MaxCost)
	}
	rows, err := tx.Query(ctx, sql)
	if err != nil {
		return fmt.Errorf("tierb: query: %w", err)
	}
	defer rows.Close()
	for _, fd := range rows.FieldDescriptions() {
		out.Columns = append(out.Columns, fd.Name)
	}
	for rows.Next() {
		if len(out.Rows) >= opt.MaxRows {
			out.Truncated = true
			break
		}
		vals, err := rows.Values()
		if err != nil {
			return err
		}
		rec := make([]string, len(vals))
		for i, v := range vals {
			rec[i] = render(v)
		}
		out.Rows = append(out.Rows, rec)
	}
	return rows.Err()
}

// RunRawAsAgent executes sql under the SAME session walls as RunQuery but
// WITHOUT the validator. It exists for exactly one caller — the test that
// proves agent_ro + RLS + READ ONLY hold on their own when the validator is
// assumed broken — and is named so no production caller reaches for it.
func RunRawAsAgent(ctx context.Context, r ReadOnlyRunner, sql string) (Result, error) {
	opt := Options{MaxRows: defMaxRows, MaxCost: 1e12, Timeout: defTimeout}
	out := Result{SQL: sql, Rows: [][]string{}}
	err := r.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		for _, s := range sessionSetup(opt.Timeout) {
			if _, err := tx.Exec(ctx, s); err != nil {
				return err
			}
		}
		rows, err := tx.Query(ctx, sql)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			vals, err := rows.Values()
			if err != nil {
				return err
			}
			rec := make([]string, len(vals))
			for i, v := range vals {
				rec[i] = render(v)
			}
			out.Rows = append(out.Rows, rec)
		}
		return rows.Err()
	})
	return out, err
}

func planCost(plan []byte) (float64, error) {
	var p []struct {
		Plan struct {
			TotalCost float64 `json:"Total Cost"`
		} `json:"Plan"`
	}
	if err := json.Unmarshal(plan, &p); err != nil || len(p) == 0 {
		return 0, fmt.Errorf("tierb: unreadable plan: %v", err)
	}
	return p[0].Plan.TotalCost, nil
}

func render(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case time.Time:
		return t.Format("2006-01-02")
	case int64:
		return decimal.NewFromInt(t).String()
	case int32:
		return decimal.NewFromInt(int64(t)).String()
	case pgtype.Numeric:
		dv, err := t.Value()
		if err != nil || dv == nil {
			return ""
		}
		if s, ok := dv.(string); ok {
			if d, err := decimal.NewFromString(s); err == nil {
				return d.StringFixed(4)
			}
			return s
		}
		return fmt.Sprint(dv)
	case [16]byte:
		return fmt.Sprintf("%x-%x-%x-%x-%x", t[0:4], t[4:6], t[6:8], t[8:10], t[10:16])
	default:
		return fmt.Sprint(t)
	}
}
