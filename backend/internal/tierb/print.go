package tierb

import (
	"fmt"
	"strconv"
	"strings"
)

// print.go RE-EMITS a validated Query as SQL. This is the only SQL text the
// executor ever sends, and it is generated, not forwarded:
//
//   - every relation is schema-qualified to agent.<view> — search_path cannot
//     redirect it, and a base table name cannot appear because the parser
//     only produced views;
//   - every identifier is double-quoted from a validated [a-z_][a-z0-9_]*
//     name, so no keyword or case trick survives;
//   - every string literal is re-escaped ('' only; the executor pins
//     standard_conforming_strings = on so a backslash is just a byte);
//   - every binary expression is fully parenthesised, so the printed text
//     cannot parse differently from the tree (the printer does not rely on
//     precedence agreeing between this parser and Postgres);
//   - LIMIT is ALWAYS emitted and is at most maxRows, whatever was asked.

// SQL renders q with a forced LIMIT of at most maxRows.
func (q *Query) SQL(maxRows int) string {
	var b strings.Builder
	b.WriteString("SELECT ")
	if q.distinct {
		b.WriteString("DISTINCT ")
	}
	if q.all {
		b.WriteString("*")
	}
	for i, it := range q.items {
		if i > 0 {
			b.WriteString(", ")
		}
		pe(&b, it.x)
		if it.alias != "" {
			b.WriteString(" AS " + qi(it.alias))
		}
	}
	b.WriteString(" FROM ")
	for i, f := range q.from {
		if i > 0 {
			if f.join == "left" {
				b.WriteString(" LEFT JOIN ")
			} else {
				b.WriteString(" JOIN ")
			}
		}
		b.WriteString(ViewSchema + "." + qi(f.view) + " AS " + qi(f.alias))
		if f.on != nil {
			b.WriteString(" ON ")
			pe(&b, f.on)
		}
	}
	if q.where != nil {
		b.WriteString(" WHERE ")
		pe(&b, q.where)
	}
	if len(q.groupBy) > 0 {
		b.WriteString(" GROUP BY ")
		for i, x := range q.groupBy {
			if i > 0 {
				b.WriteString(", ")
			}
			pe(&b, x)
		}
	}
	if q.having != nil {
		b.WriteString(" HAVING ")
		pe(&b, q.having)
	}
	if len(q.orderBy) > 0 {
		b.WriteString(" ORDER BY ")
		for i, o := range q.orderBy {
			if i > 0 {
				b.WriteString(", ")
			}
			pe(&b, o.x)
			if o.desc {
				b.WriteString(" DESC")
			} else {
				b.WriteString(" ASC")
			}
			if o.nulls != "" {
				b.WriteString(" NULLS " + strings.ToUpper(o.nulls))
			}
		}
	}
	lim := maxRows
	if q.limit >= 0 && q.limit < lim {
		lim = q.limit
	}
	b.WriteString(" LIMIT " + strconv.Itoa(lim))
	if q.offset > 0 {
		b.WriteString(" OFFSET " + strconv.Itoa(q.offset))
	}
	return b.String()
}

// qi quotes a validated identifier. The name can only contain [a-z0-9_]
// (the lexer produced it), so there is no quote to double — but it is
// doubled anyway so this function is safe on its own.
func qi(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func ql(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func pe(b *strings.Builder, x Expr) {
	switch e := x.(type) {
	case litNum:
		b.WriteString(e.v)
	case litStr:
		b.WriteString(ql(e.v))
	case litNull:
		b.WriteString("NULL")
	case litBool:
		if e.v {
			b.WriteString("TRUE")
		} else {
			b.WriteString("FALSE")
		}
	case litDate:
		b.WriteString("DATE " + ql(e.v))
	case colRef:
		b.WriteString(qi(e.alias) + "." + qi(e.col))
	case outRef:
		b.WriteString(qi(e.name))
	case star:
		b.WriteString("*")
	case unary:
		b.WriteString("(- ")
		pe(b, e.x)
		b.WriteString(")")
	case binary:
		b.WriteString("(")
		pe(b, e.l)
		b.WriteString(" " + e.op + " ")
		pe(b, e.r)
		b.WriteString(")")
	case notE:
		b.WriteString("(NOT ")
		pe(b, e.x)
		b.WriteString(")")
	case between:
		b.WriteString("(")
		pe(b, e.x)
		if e.not {
			b.WriteString(" NOT")
		}
		b.WriteString(" BETWEEN ")
		pe(b, e.lo)
		b.WriteString(" AND ")
		pe(b, e.hi)
		b.WriteString(")")
	case inList:
		b.WriteString("(")
		pe(b, e.x)
		if e.not {
			b.WriteString(" NOT")
		}
		b.WriteString(" IN (")
		for i, l := range e.list {
			if i > 0 {
				b.WriteString(", ")
			}
			pe(b, l)
		}
		b.WriteString("))")
	case isNull:
		b.WriteString("(")
		pe(b, e.x)
		if e.not {
			b.WriteString(" IS NOT NULL)")
		} else {
			b.WriteString(" IS NULL)")
		}
	case like:
		b.WriteString("(")
		pe(b, e.x)
		if e.not {
			b.WriteString(" NOT")
		}
		if e.ilike {
			b.WriteString(" ILIKE ")
		} else {
			b.WriteString(" LIKE ")
		}
		pe(b, e.pat)
		b.WriteString(")")
	case funcCall:
		// pg_catalog-qualified: the executor also pins search_path, but a
		// qualified name needs no second defence to mean what it says.
		// COALESCE / NULLIF / GREATEST / LEAST are SYNTAX in Postgres, not
		// functions — `pg_catalog.coalesce(...)` is an error — and as
		// keywords they cannot be shadowed, so they are emitted bare.
		switch e.name {
		case "coalesce", "nullif", "greatest", "least":
			b.WriteString(strings.ToUpper(e.name) + "(")
		default:
			b.WriteString("pg_catalog." + e.name + "(")
		}
		if e.star {
			b.WriteString("*")
		}
		if e.distinct {
			b.WriteString("DISTINCT ")
		}
		for i, a := range e.args {
			if i > 0 {
				b.WriteString(", ")
			}
			pe(b, a)
		}
		b.WriteString(")")
	case caseE:
		b.WriteString("(CASE")
		for _, w := range e.whens {
			b.WriteString(" WHEN ")
			pe(b, w[0])
			b.WriteString(" THEN ")
			pe(b, w[1])
		}
		if e.els != nil {
			b.WriteString(" ELSE ")
			pe(b, e.els)
		}
		b.WriteString(" END)")
	default:
		// Unreachable: every node type the parser builds is handled above.
		// A panic, not a silent empty string, so a new node type added to the
		// parser without a printer case fails every test that touches it.
		panic(fmt.Sprintf("tierb: no printer for %T", x))
	}
}
