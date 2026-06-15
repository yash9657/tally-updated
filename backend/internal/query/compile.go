package query

import (
	"errors"
	"fmt"
	"strings"
)

// CompileError is the typed error returned for any DSL that fails validation.
// The compiler NEVER falls through to building SQL on a bad input; it always
// returns a CompileError. The endpoint surfaces Message (a clean, non-leaking
// string) as the 422 body and never the raw SQL.
type CompileError struct {
	Message string
}

func (e *CompileError) Error() string { return e.Message }

func compileErrf(format string, a ...any) error {
	return &CompileError{Message: fmt.Sprintf(format, a...)}
}

// IsCompileError reports whether err is a CompileError (an invalid DSL), as
// opposed to an execution/database error.
func IsCompileError(err error) bool {
	var ce *CompileError
	return errors.As(err, &ce)
}

// compiled is the internal result of compilation: the SQL text (with $N
// placeholders only), the ordered args, and the column metadata describing the
// SELECT list in order.
type compiled struct {
	sql     string
	args    []any
	columns []ColumnMeta
	limit   int
}

// ColumnMeta describes one output column: its name and a coarse type the UI uses
// for alignment ("text" | "numeric").
type ColumnMeta struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// sqlOp maps each DSL operator to its CONSTANT SQL fragment. This is the ONLY
// source of comparison operators; nothing is ever taken from the request string.
var sqlOp = map[Op]string{
	OpEq:   "=",
	OpNe:   "<>",
	OpGt:   ">",
	OpGe:   ">=",
	OpLt:   "<",
	OpLe:   "<=",
	OpLike: "LIKE",
	OpIn:   "IN",
}

// Compile turns a validated DSL Query into parameterized SQL. RULES enforced
// here (the safety contract):
//
//   - Unknown Entity/dimension/metric/filter column/op/aggfunc => CompileError
//     (never a fall-through).
//   - Every identifier in the emitted SQL comes from a whitelist map; NO
//     user-supplied string is ever concatenated into the SQL text.
//   - Every user value is appended to args and referenced as $N (IN lists expand
//     to ($1,$2,...)); dates are bound and cast ::date.
//   - ORDER BY targets are validated against the SELECTED dimensions + metric
//     aliases only.
//   - Limit is clamped to [1, MaxLimit] (DefaultLimit when 0).
func Compile(q Query) (sql string, args []any, err error) {
	c, err := compile(q)
	if err != nil {
		return "", nil, err
	}
	return c.sql, c.args, nil
}

// SQLPreview returns the compiled SQL with $N placeholders (NEVER interpolated
// values), for the "how this was answered" panel. It is safe to expose because it
// is the deterministic, identifier-whitelisted skeleton, not raw input. On a bad
// DSL it returns the CompileError message instead of SQL.
func SQLPreview(q Query) string {
	c, err := compile(q)
	if err != nil {
		return err.Error()
	}
	return c.sql
}

func compile(q Query) (*compiled, error) {
	def, ok := entities[q.Entity]
	if !ok {
		return nil, compileErrf("unknown entity %q", string(q.Entity))
	}

	// --- SELECT list: dimensions first (in order), then metrics. ---
	var selectParts []string
	var groupByParts []string
	var columns []ColumnMeta

	// emittedNames tracks legal ORDER BY targets: the dimension identifiers'
	// display names and the metric aliases. ORDER BY validates against this set
	// ONLY, so a query cannot order by a column it did not select.
	emittedNames := map[string]bool{}

	for _, dim := range q.Dimensions {
		ident, ok := def.dimensions[dim]
		if !ok {
			return nil, compileErrf("unknown dimension %q for entity %q", dim, string(q.Entity))
		}
		selectParts = append(selectParts, ident+" AS "+quoteIdent(dim))
		groupByParts = append(groupByParts, ident)
		columns = append(columns, ColumnMeta{Name: dim, Type: "text"})
		emittedNames[dim] = true
	}

	if len(q.Metrics) == 0 && len(q.Dimensions) == 0 {
		return nil, compileErrf("query must select at least one dimension or metric")
	}

	// Determine outstanding orientation up front (the pending metric needs it).
	payable := outstandingIsPayable(q)

	aliasUsed := map[string]bool{}
	for i, m := range q.Metrics {
		expr, colType, err := metricExpr(q.Entity, def, m, payable)
		if err != nil {
			return nil, err
		}
		alias := m.As
		if alias == "" {
			alias = defaultAlias(m)
		}
		alias = sanitizeAlias(alias)
		if alias == "" {
			return nil, compileErrf("invalid metric alias at position %d", i)
		}
		if aliasUsed[alias] {
			return nil, compileErrf("duplicate metric alias %q", alias)
		}
		aliasUsed[alias] = true
		selectParts = append(selectParts, expr+" AS "+quoteIdent(alias))
		columns = append(columns, ColumnMeta{Name: alias, Type: colType})
		emittedNames[alias] = true
	}

	// --- WHERE: baked guard + filters (all values as $N). ---
	var whereParts []string
	if def.whereGuard != "" {
		whereParts = append(whereParts, def.whereGuard)
	}
	args := []any{}
	argN := 0
	nextArg := func(v any) string {
		args = append(args, v)
		argN++
		return fmt.Sprintf("$%d", argN)
	}

	for _, f := range q.Filters {
		fc, ok := def.filters[f.Column]
		if !ok {
			return nil, compileErrf("unknown filter column %q for entity %q", f.Column, string(q.Entity))
		}
		opSQL, ok := sqlOp[f.Op]
		if !ok {
			return nil, compileErrf("unknown operator %q", string(f.Op))
		}
		if !fc.ops[f.Op] {
			return nil, compileErrf("operator %q not allowed on column %q", string(f.Op), f.Column)
		}
		if err := validateValues(f, fc.valType); err != nil {
			return nil, err
		}
		switch f.Op {
		case OpIn:
			placeholders := make([]string, 0, len(f.Values))
			for _, v := range f.Values {
				placeholders = append(placeholders, nextArg(v))
			}
			whereParts = append(whereParts, fmt.Sprintf("%s IN (%s)", fc.expr, strings.Join(placeholders, ",")))
		default:
			whereParts = append(whereParts, fmt.Sprintf("%s %s %s", fc.expr, opSQL, nextArg(f.Values[0])))
		}
	}

	// --- date range (bound + cast ::date). ---
	if q.DateRange != nil {
		dr := q.DateRange
		dcol, ok := def.dateCols[dr.Column]
		if !ok {
			return nil, compileErrf("unknown date column %q for entity %q", dr.Column, string(q.Entity))
		}
		if dr.From != "" {
			if !isISODate(dr.From) {
				return nil, compileErrf("invalid from date %q", dr.From)
			}
			whereParts = append(whereParts, fmt.Sprintf("%s >= %s::date", dcol, nextArg(dr.From)))
		}
		if dr.To != "" {
			if !isISODate(dr.To) {
				return nil, compileErrf("invalid to date %q", dr.To)
			}
			whereParts = append(whereParts, fmt.Sprintf("%s <= %s::date", dcol, nextArg(dr.To)))
		}
	}

	// --- ORDER BY (validated against selected dims/aliases only). ---
	var orderParts []string
	for _, ot := range q.OrderBy {
		if !emittedNames[ot.Column] {
			return nil, compileErrf("order_by column %q is not a selected dimension or metric alias", ot.Column)
		}
		dir := "ASC"
		if ot.Desc {
			dir = "DESC"
		}
		orderParts = append(orderParts, quoteIdent(ot.Column)+" "+dir)
	}

	// --- assemble ---
	var b strings.Builder
	b.WriteString("SELECT ")
	b.WriteString(strings.Join(selectParts, ", "))
	b.WriteString(" FROM ")
	b.WriteString(def.skeleton)
	if len(whereParts) > 0 {
		b.WriteString(" WHERE ")
		b.WriteString(strings.Join(whereParts, " AND "))
	}
	if len(groupByParts) > 0 {
		b.WriteString(" GROUP BY ")
		b.WriteString(strings.Join(groupByParts, ", "))
	}
	if len(orderParts) > 0 {
		b.WriteString(" ORDER BY ")
		b.WriteString(strings.Join(orderParts, ", "))
	}
	limit := clampLimit(q.Limit)
	b.WriteString(fmt.Sprintf(" LIMIT %d", limit))

	return &compiled{sql: b.String(), args: args, columns: columns, limit: limit}, nil
}

// metricExpr returns the constant SQL expression and coarse type for a metric.
func metricExpr(entity Entity, def entityDef, m Metric, payable bool) (string, string, error) {
	switch m.Func {
	case AggCount:
		// count(*) is the only count form; a column for count is ignored but
		// must be "*" or empty to avoid surprising the user.
		if m.Column != "" && m.Column != "*" {
			return "", "", compileErrf("count only supports column \"*\", got %q", m.Column)
		}
		return "COUNT(*)", "numeric", nil
	case AggSum, AggMin, AggMax, AggAvg:
		// Special-case the outstanding pending metric (sign-oriented).
		if entity == EntityOutstandings && m.Column == "pending" {
			if m.Func != AggSum {
				return "", "", compileErrf("outstandings 'pending' only supports sum")
			}
			return outstandingPendingExpr(payable), "numeric", nil
		}
		mc, ok := def.metrics[m.Column]
		if !ok {
			return "", "", compileErrf("unknown metric column %q for entity %q", m.Column, string(entity))
		}
		// The whitelist metric expr for sum already wraps SUM(...). For min/max/
		// avg we wrap the underlying column reference. To keep this simple and
		// fully whitelisted, we only support SUM over the pre-aggregated money
		// expressions; min/max/avg apply to the same money columns via a derived
		// inner reference. We reject min/max/avg to avoid emitting an
		// un-whitelisted nested expression.
		if m.Func != AggSum {
			return "", "", compileErrf("%s is not supported on column %q", string(m.Func), m.Column)
		}
		colType := "numeric"
		if !mc.isMoney {
			colType = "numeric"
		}
		return mc.expr, colType, nil
	default:
		return "", "", compileErrf("unknown aggregate function %q", string(m.Func))
	}
}

// outstandingIsPayable inspects the nature filter (if any) on an outstandings
// query to choose the pending-sum orientation. ASSET (or unset) => receivable;
// LIABILITY => payable. The user supplies only the nature VALUE; the server
// picks the fixed sign expression.
func outstandingIsPayable(q Query) bool {
	if q.Entity != EntityOutstandings {
		return false
	}
	for _, f := range q.Filters {
		if f.Column == "nature" && f.Op == OpEq && len(f.Values) == 1 {
			if strings.EqualFold(f.Values[0], "LIABILITY") {
				return true
			}
		}
	}
	return false
}

// validateValues enforces the per-op arity and the value-type constraint. Values
// remain bound as $N regardless; this only rejects malformed literals early.
func validateValues(f Filter, vt valueType) error {
	switch f.Op {
	case OpIn:
		if len(f.Values) < 1 {
			return compileErrf("operator 'in' requires at least one value for column %q", f.Column)
		}
	default:
		if len(f.Values) != 1 {
			return compileErrf("operator %q requires exactly one value for column %q", string(f.Op), f.Column)
		}
	}
	for _, v := range f.Values {
		if err := checkValueType(v, vt, f.Column); err != nil {
			return err
		}
	}
	return nil
}

func checkValueType(v string, vt valueType, col string) error {
	switch vt {
	case valueNature:
		switch v {
		case "ASSET", "LIABILITY", "INCOME", "EXPENSE":
			return nil
		}
		return compileErrf("invalid value %q for %q (expected ASSET|LIABILITY|INCOME|EXPENSE)", v, col)
	case valueDrCr:
		if v == "D" || v == "C" {
			return nil
		}
		return compileErrf("invalid value %q for %q (expected D|C)", v, col)
	case valueDate:
		if !isISODate(v) {
			return compileErrf("invalid date %q for %q (expected YYYY-MM-DD)", v, col)
		}
		return nil
	default:
		return nil
	}
}

// isISODate validates a YYYY-MM-DD string structurally (digits + dashes + plausible
// ranges). The value is still bound as a $N param and cast ::date by Postgres;
// this is an early reject for obviously-bad input.
func isISODate(s string) bool {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return false
	}
	for i, ch := range s {
		if i == 4 || i == 7 {
			continue
		}
		if ch < '0' || ch > '9' {
			return false
		}
	}
	mo := (int(s[5]-'0'))*10 + int(s[6]-'0')
	dy := (int(s[8]-'0'))*10 + int(s[9]-'0')
	if mo < 1 || mo > 12 || dy < 1 || dy > 31 {
		return false
	}
	return true
}

// clampLimit clamps to [1, MaxLimit], defaulting to DefaultLimit at 0.
func clampLimit(n int) int {
	if n <= 0 {
		return DefaultLimit
	}
	if n > MaxLimit {
		return MaxLimit
	}
	return n
}

// defaultAlias produces a stable alias for a metric with no As set.
func defaultAlias(m Metric) string {
	col := m.Column
	if col == "" || col == "*" {
		col = "all"
	}
	return string(m.Func) + "_" + col
}

// sanitizeAlias reduces an alias to a safe identifier: lowercase letters, digits
// and underscores, never starting with a digit. It is then double-quoted when
// emitted. This guarantees the alias text cannot inject SQL even though it is
// user-influenced — only [a-z0-9_] survive, and the result is quoted.
func sanitizeAlias(s string) string {
	var b strings.Builder
	for _, ch := range strings.ToLower(s) {
		switch {
		case ch >= 'a' && ch <= 'z', ch == '_':
			b.WriteRune(ch)
		case ch >= '0' && ch <= '9':
			if b.Len() == 0 {
				b.WriteByte('_')
			}
			b.WriteRune(ch)
		default:
			// drop anything else
		}
	}
	return b.String()
}

// quoteIdent double-quotes an already-sanitized identifier (dimension display
// name or metric alias) for the SELECT/ORDER BY list. Dimension names come from
// the whitelist keys (so are already safe); aliases are sanitizeAlias'd first.
// Embedded double quotes are doubled per SQL rules as a belt-and-braces measure.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
