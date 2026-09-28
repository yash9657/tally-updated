package tierb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/platform/db"
)

// exec.go runs a validated query under every wall PLAN-AGENTIC §2.3 names,
// and since migration 0099 each wall is one the DATABASE keeps on its own:
//
//	tenant GUC                 set by db.RunInTx as its first statement
//	tenant BINDING             agent.tier_b_bind(): (backend, xact) -> company,
//	                           written by tally_app BEFORE any untrusted text;
//	                           agent_ro cannot write it; a RESTRICTIVE policy
//	                           per base table holds the views' owner to it
//	READ ONLY                  SET LOCAL transaction_read_only = on, AFTER the
//	                           bind (a write errors; it cannot be undone once
//	                           a query has run)
//	statement_timeout 5 s      SET LOCAL, plus a Go context deadline as backstop
//	search_path pg_catalog     nothing a later migration puts in public can shadow
//	SET LOCAL ROLE agent_ro    SELECT on agent.* only; no base table
//	security-definer frame     the text runs ONLY as the $1 of
//	                           agent.tier_b_run / tier_b_plan (owned by
//	                           agent_ro), where Postgres refuses any change of
//	                           role — so SET ROLE / RESET ROLE /
//	                           set_config('role') cannot reach tally_app
//	EXPLAIN cost gate          refuse before running a query the planner prices high
//	forced LIMIT               printed by Query.SQL, and the function stops at it too
//
// ⛔ THE ORDER IS LOAD-BEARING. Bind first (it must write, and it trusts the
// GUC only because nothing untrusted has run yet); READ ONLY second (a bind
// after it would fail, and nothing after it can write); the role switch LAST.
//
// ⛔ THE TEXT IS NEVER A STATEMENT OF ITS OWN. Before 0099 the query was
// tx.Query(sql) after SET LOCAL ROLE, and a raw `SELECT
// set_config('role','tally_app',true), …` walked straight back out to
// tally_app (measured; d122 "Wave 2"). Now the only things Postgres receives
// with the text in them are (1) a Parse/Describe of it, to learn the result
// columns and their types — which executes nothing — and (2) the $1 of the
// two security-definer functions. A bind parameter also keeps the text out
// of pg_stat_activity.

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
	// ctxGrace is how much longer than statement_timeout the Go deadline
	// allows. The server-side timeout is the stop (text that set_config's it
	// to 0 does not reach its own, already-armed statement — measured by
	// TestStatementTimeoutCannotBeTalkedAway); the context is the backstop.
	ctxGrace = 2 * time.Second
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

// ErrBinding means the database did not bind this transaction to the
// caller's company. It is never expected; it is checked because a binding to
// the wrong company would be exactly the leak this package exists to stop.
var ErrBinding = errors.New("tierb: the tenant binding does not match the caller's company")

// TxRunner is what Run needs (*db.Pool satisfies it). A READ-WRITE start is
// required: the binding row is written before the transaction turns READ
// ONLY. The transaction is ALWAYS rolled back (errRollback), so the binding
// never commits and nothing else could.
type TxRunner interface {
	RunInTx(ctx context.Context, fn db.TxFunc) error
}

// errRollback makes db.RunInTx roll back a Tier-B transaction that succeeded.
var errRollback = errors.New("tierb: rollback")

// Run validates src and executes it. Validation failures wrap ErrRejected.
func Run(ctx context.Context, r TxRunner, src string, opt Options) (Result, error) {
	q, err := Parse(src)
	if err != nil {
		return Result{}, err
	}
	return RunQuery(ctx, r, q, opt)
}

// RunQuery executes an already-validated query.
func RunQuery(ctx context.Context, r TxRunner, q *Query, opt Options) (Result, error) {
	opt = opt.normalised()
	return execute(ctx, r, q.SQL(opt.MaxRows), opt, true)
}

// RunRawAsAgent executes sql under the SAME walls as RunQuery but WITHOUT the
// validator (and with the cost gate off). It exists for exactly one caller —
// the test that proves the database half holds on its own when the validator
// is assumed broken — and is named so no production caller reaches for it.
// It is the same path, not a lookalike: if it and RunQuery ever differed,
// the proof would be about the wrong code.
func RunRawAsAgent(ctx context.Context, r TxRunner, sql string) (Result, error) {
	opt := Options{MaxRows: defMaxRows, MaxCost: 1e12, Timeout: defTimeout}
	return execute(ctx, r, sql, opt, false)
}

func (opt Options) normalised() Options {
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
	return opt
}

func execute(ctx context.Context, r TxRunner, sql string, opt Options, costGate bool) (Result, error) {
	want, ok := db.CompanyFromContext(ctx)
	if !ok {
		return Result{}, db.ErrNoCompany
	}
	ctx, cancel := context.WithTimeout(ctx, opt.Timeout+ctxGrace)
	defer cancel()
	out := Result{SQL: sql, Rows: [][]string{}}
	err := r.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := runIn(ctx, tx, want, sql, opt, costGate, &out); err != nil {
			return err
		}
		return errRollback
	})
	if err != nil && !errors.Is(err, errRollback) {
		return Result{}, err
	}
	return out, nil
}

// sessionSetup is the constant preamble after the bind, in its load-bearing
// order.
func sessionSetup(timeout time.Duration) []string {
	return []string{
		"SET LOCAL transaction_read_only = on",
		fmt.Sprintf("SET LOCAL statement_timeout = %d", timeout.Milliseconds()),
		"SET LOCAL search_path = pg_catalog",
		"SET LOCAL standard_conforming_strings = on",
		"SET LOCAL ROLE agent_ro",
	}
}

func runIn(ctx context.Context, tx pgx.Tx, want uuid.UUID, sql string, opt Options, costGate bool, out *Result) error {
	var bound uuid.UUID
	if err := tx.QueryRow(ctx, "SELECT agent.tier_b_bind()").Scan(&bound); err != nil {
		return fmt.Errorf("tierb: bind: %w", err)
	}
	if bound != want {
		return fmt.Errorf("%w: bound %s, caller %s", ErrBinding, bound, want)
	}
	for _, s := range sessionSetup(opt.Timeout) {
		if _, err := tx.Exec(ctx, s); err != nil {
			return fmt.Errorf("tierb: session setup %q: %w", s, err)
		}
	}
	// Parse + Describe only: the column names and types, nothing executed.
	// The unnamed statement is replaced by the next Parse on this connection.
	sd, err := tx.Conn().Prepare(ctx, "", sql)
	if err != nil {
		return fmt.Errorf("tierb: query: %w", err)
	}
	oids := make([]uint32, len(sd.Fields))
	for i, fd := range sd.Fields {
		out.Columns = append(out.Columns, fd.Name)
		oids[i] = fd.DataTypeOID
	}
	if costGate {
		var plan []byte
		if err := tx.QueryRow(ctx, "SELECT agent.tier_b_plan($1)", sql).Scan(&plan); err != nil {
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
	}
	// One row past the limit, so a result that was cut is known to be cut.
	rows, err := tx.Query(ctx, "SELECT r FROM agent.tier_b_run($1, $2) AS r", sql, opt.MaxRows+1)
	if err != nil {
		return fmt.Errorf("tierb: query: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		if len(out.Rows) >= opt.MaxRows {
			out.Truncated = true
			break
		}
		rec, err := decodeRow(raw, oids)
		if err != nil {
			return err
		}
		out.Rows = append(out.Rows, rec)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("tierb: query: %w", err)
	}
	return nil
}

// decodeRow reads one row_to_json object IN ORDER (duplicate column names
// are legal SQL and a map would fold them) and renders each value by the
// type the Describe reported, exactly as the pre-0099 path rendered the
// native value: numeric StringFixed(4), integers bare, dates yyyy-mm-dd.
func decodeRow(raw []byte, oids []uint32) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, fmt.Errorf("tierb: unreadable row %q", raw)
	}
	rec := make([]string, 0, len(oids))
	for dec.More() {
		if _, err := dec.Token(); err != nil { // the key
			return nil, fmt.Errorf("tierb: unreadable row %q: %w", raw, err)
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf("tierb: unreadable row %q: %w", raw, err)
		}
		var oid uint32
		if i := len(rec); i < len(oids) {
			oid = oids[i]
		}
		rec = append(rec, renderJSON(v, oid))
	}
	if len(rec) != len(oids) {
		return nil, fmt.Errorf("tierb: row has %d values, the statement describes %d columns", len(rec), len(oids))
	}
	return rec, nil
}

func renderJSON(v json.RawMessage, oid uint32) string {
	if len(v) == 0 || string(v) == "null" {
		return ""
	}
	switch v[0] {
	case '"':
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return string(v)
		}
		switch oid {
		case pgtype.DateOID, pgtype.TimestampOID, pgtype.TimestamptzOID:
			if len(s) >= 10 {
				return s[:10]
			}
		}
		return s
	case 't', 'f', '{', '[':
		return string(v)
	}
	// A number.
	switch oid {
	case pgtype.NumericOID:
		if d, err := decimal.NewFromString(string(v)); err == nil {
			return d.StringFixed(4)
		}
	case pgtype.Float4OID, pgtype.Float8OID:
		if f, err := strconv.ParseFloat(string(v), 64); err == nil {
			return fmt.Sprint(f)
		}
	}
	return string(v)
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
