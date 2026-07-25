package nlq

import (
	"sort"
	"strings"

	"github.com/khata/cored/internal/query"
)

// emitQuerySchema is the HAND-WRITTEN JSON Schema of query.Query passed to the
// Messages API as the `emit_query` tool's input_schema. It is CLOSED
// (additionalProperties:false everywhere, every property required) with enums for
// entity/func/op pinned as literals. Writing it by hand (rather than generating
// it from the Go constants) makes schema_test.go's consistency check a genuine
// INDEPENDENT oracle: the test asserts these literals equal dsl.go's constants,
// so a drift on either side is caught.
//
// The entity enum includes "" deliberately: the system prompt instructs the model
// to call emit_query with entity "" when a question cannot be answered by this
// schema, and a closed enum would otherwise make that tool call schema-invalid.
//
// The JSON tags MUST match query.Query exactly (entity, metrics, dimensions,
// filters, date_range, order_by, limit; metric func/column/as; filter
// column/op/values; date_range column/from/to; order_by column/desc) — the
// response decoder uses DisallowUnknownFields, so any mismatch would reject an
// otherwise-valid answer.
const emitQuerySchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["entity", "metrics", "dimensions", "filters", "date_range", "order_by", "limit"],
  "properties": {
    "entity": {"type": "string", "enum": ["ledger_balances", "vouchers", "postings", "outstandings", ""]},
    "metrics": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["func", "column", "as"],
        "properties": {
          "func": {"type": "string", "enum": ["sum", "count", "min", "max", "avg"]},
          "column": {"type": "string"},
          "as": {"type": "string"}
        }
      }
    },
    "dimensions": {"type": "array", "items": {"type": "string"}},
    "filters": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["column", "op", "values"],
        "properties": {
          "column": {"type": "string"},
          "op": {"type": "string", "enum": ["eq", "ne", "gt", "ge", "lt", "le", "like", "in"]},
          "values": {"type": "array", "items": {"type": "string"}}
        }
      }
    },
    "date_range": {
      "type": ["object", "null"],
      "additionalProperties": false,
      "required": ["column", "from", "to"],
      "properties": {
        "column": {"type": "string"},
        "from": {"type": "string"},
        "to": {"type": "string"}
      }
    },
    "order_by": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["column", "desc"],
        "properties": {
          "column": {"type": "string"},
          "desc": {"type": "boolean"}
        }
      }
    },
    "limit": {"type": "integer"}
  }
}`

// systemPrompt builds the NL->DSL system prompt from query.Catalog() at runtime,
// so a whitelist widening automatically reaches the model. It carries NAMES ONLY
// (never SQL) plus the four compiler traps and three few-shot NL->DSL pairs. NO
// tenant data (company id, ledger/party names) ever appears here — the catalog is
// the fixed server-authored surface, and the caller's question is the only
// per-request input, carried in the user message, never interpolated here.
func systemPrompt() string {
	var b strings.Builder
	b.WriteString("You translate an accountant's natural-language question about their books ")
	b.WriteString("into a single typed query by calling the emit_query tool. You NEVER write SQL. ")
	b.WriteString("You may ONLY use the entities, dimensions, metrics, filters and date columns listed below. ")
	b.WriteString("If the question cannot be answered from this catalog, call emit_query with entity \"\" (empty string).\n\n")
	b.WriteString("CATALOG (names only):\n")
	for _, e := range query.Catalog() {
		b.WriteString("- entity ")
		b.WriteString(e.Entity)
		b.WriteString(":\n")
		b.WriteString("    dimensions: ")
		b.WriteString(strings.Join(e.Dimensions, ", "))
		b.WriteString("\n    metrics: ")
		b.WriteString(strings.Join(metricLines(e), ", "))
		b.WriteString("\n    filters: ")
		b.WriteString(strings.Join(filterLines(e), ", "))
		if len(e.DateCols) > 0 {
			b.WriteString("\n    date_range columns: ")
			b.WriteString(strings.Join(e.DateCols, ", "))
		}
		b.WriteString("\n")
	}
	b.WriteString(`
RULES (the compiler rejects violations; obey them so the query is answerable):
1. Aliases (a metric's "as", and any order_by column) must be lowercase snake_case, and an order_by column must spell an emitted dimension or metric alias IDENTICALLY.
2. ledger_balances accepts NO date_range. Only vouchers/postings (voucher_date) and outstandings (due_date) do.
3. The metrics "net" and "pending" accept only func "sum". count uses column "*".
4. On vouchers, filter by party name using the filter column "party", but GROUP BY the dimension "party_name" — they are different keys.
5. outstandings "nature" accepts only op "eq" (values ASSET for receivables, LIABILITY for payables).

EXAMPLES:
Q: "trial balance"
-> emit_query {"entity":"ledger_balances","metrics":[{"func":"sum","column":"debit","as":"debit"},{"func":"sum","column":"credit","as":"credit"}],"dimensions":["group_name"],"filters":[],"date_range":null,"order_by":[{"column":"group_name","desc":false}],"limit":100}
Q: "total purchases this quarter"
-> emit_query {"entity":"vouchers","metrics":[{"func":"sum","column":"taxable_value","as":"total_purchases"}],"dimensions":["month"],"filters":[{"column":"voucher_type_name","op":"eq","values":["Purchase"]}],"date_range":{"column":"voucher_date","from":"2026-01-01","to":"2026-03-31"},"order_by":[{"column":"month","desc":false}],"limit":100}
Q: "who owes us the most"
-> emit_query {"entity":"outstandings","metrics":[{"func":"sum","column":"pending","as":"pending"}],"dimensions":["ledger_name"],"filters":[{"column":"nature","op":"eq","values":["ASSET"]}],"date_range":null,"order_by":[{"column":"pending","desc":true}],"limit":100}
`)
	return b.String()
}

func metricLines(e query.EntityDoc) []string {
	out := make([]string, 0, len(e.Metrics))
	for _, m := range e.Metrics {
		out = append(out, m.Name+"("+strings.Join(m.Aggs, "/")+")")
	}
	sort.Strings(out)
	return out
}

func filterLines(e query.EntityDoc) []string {
	out := make([]string, 0, len(e.Filters))
	for _, f := range e.Filters {
		out = append(out, f.Name+"["+strings.Join(f.Ops, "/")+"]")
	}
	sort.Strings(out)
	return out
}
