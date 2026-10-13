package tierb

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
)

// native_export_test.go keeps, FOR TESTS ONLY, the pre-0099 way of reading a
// result: the validated SQL sent as a statement of its own and each value
// rendered from pgx's native decode. It is NOT a wall (it runs trusted,
// re-emitted SQL only) — it is the ORACLE for the production path's JSON
// decode: TestJSONDecodeRendersLikeTheNativeDriver runs every accepted query
// both ways and requires the same cells and the same refusals, so moving the
// text inside agent.tier_b_run changed where it runs and nothing it returns.
func NativeRunQuery(ctx context.Context, r TxRunner, q *Query, maxRows int) (Result, error) {
	sql := q.SQL(maxRows)
	out := Result{SQL: sql, Rows: [][]string{}}
	err := r.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT agent.tier_b_bind()"); err != nil {
			return err
		}
		for _, s := range sessionSetup(defTimeout) {
			if _, err := tx.Exec(ctx, s); err != nil {
				return fmt.Errorf("tierb: session setup %q: %w", s, err)
			}
		}
		// pgx hands a timestamptz back in the GO PROCESS's zone; the session's
		// zone is what date_trunc truncated in, so the oracle renders in that.
		// (The pre-0099 path did not, and printed April's month as
		// "2025-03-31" on a host west of UTC — measured; d122 "Wave 2".)
		var tz string
		if err := tx.QueryRow(ctx, "SHOW TimeZone").Scan(&tz); err != nil {
			return err
		}
		loc, err := time.LoadLocation(tz)
		if err != nil {
			return err
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
			vals, err := rows.Values()
			if err != nil {
				return err
			}
			rec := make([]string, len(vals))
			for i, v := range vals {
				if tt, ok := v.(time.Time); ok {
					v = tt.In(loc)
				}
				rec[i] = nativeRender(v)
			}
			out.Rows = append(out.Rows, rec)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("tierb: query: %w", err)
		}
		return errRollback
	})
	if err != nil && err != errRollback {
		return Result{}, err
	}
	return out, nil
}

var _ = pgtype.Numeric{}
var _ = decimal.Zero

func nativeRender(v any) string {
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
