package query

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/platform/db"
)

// ReadOnlyRunner is the minimal database capability the query core needs: a
// read-only, company-scoped transaction. *db.Pool satisfies it via
// RunInReadOnlyTx.
type ReadOnlyRunner interface {
	RunInReadOnlyTx(ctx context.Context, fn db.TxFunc) error
}

// Result is the executed query's tabular answer. Rows are ALL strings: money
// columns are NUMERIC(19,4) rendered via decimal.StringFixed(4), dates are
// YYYY-MM-DD, and SQL NULL is the empty string. This matches every existing
// handler (voucherEntryJSON, sectionJSON) and keeps money string end-to-end.
type Result struct {
	Columns []ColumnMeta `json:"columns"`
	Rows    [][]string   `json:"rows"`
	Limit   int          `json:"limit"`
	HasMore bool         `json:"has_more"`
}

// Execute compiles q, runs it in a read-only company-scoped transaction (RLS
// scopes the tenant), and returns the stringified rows. A compile error is
// returned as a *CompileError (the endpoint maps it to 422); a database error is
// returned as-is.
func Execute(ctx context.Context, runner ReadOnlyRunner, q Query) (Result, error) {
	c, err := compileBounded(q, true)
	if err != nil {
		return Result{}, err
	}

	out := Result{Columns: c.columns, Rows: [][]string{}, Limit: c.limit}

	err = runner.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, c.sql, c.args...)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			if len(out.Rows) == c.limit {
				out.HasMore = true
				break
			}
			vals, err := rows.Values()
			if err != nil {
				return err
			}
			rec := make([]string, len(vals))
			for i, v := range vals {
				rec[i] = stringify(v)
			}
			out.Rows = append(out.Rows, rec)
		}
		return rows.Err()
	})
	if err != nil {
		return Result{}, err
	}
	return out, nil
}

// stringify renders one scanned value as a string per the wire contract:
//   - nil (SQL NULL)        -> ""
//   - pgtype Numeric / our  -> StringFixed(4) (paisa-exact, matches reports)
//   - already a string      -> verbatim (dates arrive as text via to_char)
//   - everything else       -> a plain Go string form
func stringify(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	case bool:
		if t {
			return "true"
		}
		return "false"
	case decimal.Decimal:
		return t.StringFixed(4)
	default:
		// pgx returns NUMERIC as pgtype.Numeric which renders its text form via
		// Value(); convert through decimal for paisa-exact StringFixed(4).
		if d, ok := toDecimal(v); ok {
			return d.StringFixed(4)
		}
		return fmt.Sprintf("%v", v)
	}
}

// toDecimal best-effort converts a scanned value to a shopspring decimal. It
// handles pgtype.Numeric (the default NUMERIC scan target from rows.Values()),
// integers, and decimal-text driver values. Returns (zero,false) for values that
// are not numbers (so they fall through to a plain string render).
func toDecimal(v any) (decimal.Decimal, bool) {
	switch t := v.(type) {
	case pgtype.Numeric:
		if !t.Valid {
			return decimal.Zero, false
		}
		dv, err := t.Value() // text form, e.g. "1180.0000" or nil if invalid
		if err != nil || dv == nil {
			return decimal.Zero, false
		}
		s, ok := dv.(string)
		if !ok {
			return decimal.Zero, false
		}
		d, err := decimal.NewFromString(s)
		if err != nil {
			return decimal.Zero, false
		}
		return d, true
	case int64:
		return decimal.NewFromInt(t), true
	case int32:
		return decimal.NewFromInt(int64(t)), true
	case int:
		return decimal.NewFromInt(int64(t)), true
	default:
		return decimal.Zero, false
	}
}
