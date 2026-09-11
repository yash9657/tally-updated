package nlq

import (
	"sort"
	"strings"
	"time"

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

// ---------------------------------------------------------------------------
// THE PROMPT, SPLIT INTO ITS CACHEABLE HALF AND ITS PER-CALL HALF.
// ---------------------------------------------------------------------------
//
// ⛔ THE SPLIT IS A COST CONTRACT, NOT A TIDY-UP.
//
// Every provider we send this to bills a REPEATED prompt prefix at a fraction of
// a fresh one, and every one of them matches that prefix BYTE FOR BYTE from the
// first byte of the request. Until W0 this prompt was one string with today's
// date and the resolved period windows baked into the middle of it — which means
// the prefix changed at midnight, every night, for every tenant, and the ~6 KB
// catalogue-and-rules preamble was re-billed at full price on every single
// question anybody had ever asked. Nothing failed; the only symptom was the
// invoice, which is exactly why it survived three lanes unnoticed.
//
// So: staticPrompt() is what is identical across every call of this prompt
// version — the catalogue, the compiler traps, the refusal rules. It goes in
// llm.Request.Static. dynamicPrompt() is what a clock or a company changes —
// today's date, the financial-year month, the resolved windows, and the worked
// examples that interpolate a live window. It goes in llm.Request.Dynamic,
// AFTER the static half.
//
// ⚠️ THE EXAMPLES ARE DYNAMIC AND THAT IS DELIBERATE. They look like teaching
// material and teaching material looks static, so the tempting move is to hoist
// them into staticPrompt() and freeze the quarter they name. That is D21 exactly
// — a frozen example window is what once taught the model to answer "this
// quarter" with a quarter three years gone — and it would cost a few hundred
// bytes of cache to avoid. The catalogue and the rules are the bulk; the
// examples are four lines. Freeze them and the cache saves nothing it did not
// already save, while the answers go wrong.
//
// TestStaticPromptIsClockIndependent (translate_test.go) is the detector: it
// builds the request under two clocks years apart and requires identical Static
// bytes, a DIFFERENT Dynamic, and — the control — a Static that actually
// carries the catalogue, so a "split" that put everything in Dynamic (leaving
// Static trivially stable because it is empty) fails instead of passing.

// systemPrompt is the whole prompt, static half followed by dynamic half, in the
// order they go over the wire.
//
// ⚠️ IT SURVIVES THE SPLIT AS A REAL FUNCTION, not as a test helper. Six tests in
// schema_test.go assert on the RENDERED prompt — that no SQL leaks into it, that
// every worked example compiles, that the window the model is told to copy is the
// window FakeTranslator answers with — and those are assertions about what the
// model actually reads, which is the concatenation. Splitting them into two
// half-prompt test suites would let a rule land in one half and the example that
// contradicts it in the other, with each half passing on its own.
func systemPrompt(now time.Time, fyStartMonth int) string {
	return staticPrompt() + dynamicPrompt(now, fyStartMonth)
}

// staticPrompt builds the cacheable half from query.Catalog() at runtime, so a
// whitelist widening automatically reaches the model. It carries NAMES ONLY
// (never SQL) plus the compiler traps and the refusal rules. NO tenant data
// (company id, ledger/party names) ever appears here — the catalog is the fixed
// server-authored surface, and the caller's question is the only per-request
// input, carried in the user message, never interpolated here.
//
// ⛔ THE PROMPT IS PART OF THE PRODUCT, NOT DOCUMENTATION FOR IT (L5). Whatever
// this file teaches IS what a real deployment answers: server.go swaps
// FakeTranslator for the real translator the moment a provider key is set, and
// the compiler downstream only rejects ILLEGAL queries — it cannot reject a
// legal query that measures the wrong thing. Three of L5's four defects are
// therefore restated here as rules the model must obey, because fixing them in
// FakeTranslator alone would leave the production path wrong:
//
//   - D51: the worked example used to be sum(taxable_value) over voucher type
//     "Sales" — the exact figure that matched no report. Rule 6 now forbids it
//     and the example (in dynamicPrompt) emits the postings/natural_net/pl_group
//     shape instead.
//   - D21: the example used to carry a HARD-CODED quarter and the model was
//     given no clock at all, so every relative phrase was a guess. The resolved
//     windows come from query.NamedWindows — the same parser FakeTranslator
//     answers with — and rule 7 says to copy them verbatim.
//   - D50: rule R1 refuses a question that names another company's books.
//
// ⛔ IT TAKES NO ARGUMENTS, AND THAT IS THE POINT. A parameter here is a byte of
// per-call data in the cache prefix; the compiler is what keeps the next person
// from adding one. Anything a caller could vary belongs in dynamicPrompt.
func staticPrompt() string {
	var b strings.Builder
	b.WriteString("You translate an accountant's natural-language question about their books ")
	b.WriteString("into a single typed query by calling the emit_query tool. You NEVER write SQL. ")
	b.WriteString("You may ONLY use the entities, dimensions, metrics, filters and date columns listed below. ")
	b.WriteString("If the question cannot be answered from this catalog, or if any REFUSE rule applies, ")
	b.WriteString("call emit_query with entity \"\" (empty string).\n\n")
	b.WriteString("CATALOG (names only):\n")
	// query.Catalog() sorts entities, dimensions, metrics, filters and date
	// columns before returning them, and metricLines/filterLines sort again —
	// so this loop is byte-stable across processes. That determinism is not
	// cosmetic here: a map-order catalogue would produce a different prefix in
	// every replica, and the cache would never hit anywhere.
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
3. The metrics "net", "natural_net" and "pending" accept only func "sum". count uses column "*".
4. On vouchers, filter by party name using the filter column "party", but GROUP BY the dimension "party_name" — they are different keys.
5. outstandings "nature" accepts only op "eq" (values ASSET for receivables, LIABILITY for payables).
6. A SALES OR PURCHASE TOTAL IS MEASURED ON postings, NEVER ON vouchers. Use entity "postings", metric sum(natural_net), and filter pl_group eq "Sales Accounts" (or "Purchase Accounts"); that is the figure the Profit & Loss prints, for the same books. The vouchers metric taxable_value is the GST TAXABLE BASE of the invoice register: it EXCLUDES the GST charged on every invoice, EXCLUDES credit notes and debit notes (a sales return is a voucher of a different type, so it would never reduce the total), and is EMPTY on a book where no GST was derived. Summing it for a "total sales" question answers a number that agrees with no report in the product. Use taxable_value only when the question explicitly asks for taxable value or the GST base.
7. PERIODS: copy a window from RESOLVED PERIOD WINDOWS below, VERBATIM. Never compute one yourself and never carry one over from an example. A period word with NO this/last qualifier ("sales by month", "monthly sales") names no window at all: emit NO date_range and group by the month dimension instead. A period the user states in full ("April 2026", "1 April 2026 to 30 June 2026") may be emitted directly.
8. outstandings is an AS-AT-TODAY position, always. Its due_date range filters WHEN BILLS FALL DUE; it does not move the as-on date, and there is no way to ask it for a past position.

REFUSE — call emit_query with entity "" (and empty metrics/dimensions/filters/order_by, null date_range, limit 0) when:
R1. The question names a scope other than the signed-in company: another company/tenant/organisation/firm by name, "all companies", "every tenant", "across all books", or a raw company UUID. Every answer is scoped to the signed-in company by the server and no other scope is reachable. Answering such a question with this company's figures would put the caller's own number under someone else's question, which is worse than a refusal.
R2. The question names a multi-period window ("last 6 months", "past 3 years") or two different windows at once ("last month and this year"). One date_range expresses neither, and answering for one of the periods is a wrong answer that looks like a right one.
R3. The question asks for an outstandings position at a past or future date ("receivables as at March", "what was overdue last quarter") — see rule 8.
R4. The catalog cannot express the question at all.
`)
	return b.String()
}

// dynamicPrompt builds the per-call half: today's date, the company's
// financial-year start month, the resolved period windows, and the worked
// examples that quote one of those windows.
//
// now/fyStartMonth are the request clock and the company financial-year start;
// they are calendar facts, not tenant data — which is why they may cross the
// wire at all. (The company's IDENTITY may not, and llm.Client checks the
// encoded body for it before opening a socket.)
func dynamicPrompt(now time.Time, fyStartMonth int) string {
	// Resolved ONCE, from query's own parser, and used by BOTH the window table
	// and the worked example — so the example can never teach a window the table
	// contradicts.
	windows := query.NamedWindows(func() time.Time { return now }, fyStartMonth)
	thisQuarter := windowFor(windows, "this quarter")

	var b strings.Builder
	b.WriteString(`
TODAY IS ` + iso(now) + `. The financial year starts in ` + query.FYStartMonth(fyStartMonth).String() + `; "year"/"annum"/"fy" mean that financial year, while "quarter" means the CALENDAR quarter.

RESOLVED PERIOD WINDOWS (inclusive, on voucher_date — copy these dates exactly):
`)
	for _, w := range windows {
		b.WriteString("- \"")
		b.WriteString(w.Phrase)
		b.WriteString("\": ")
		b.WriteString(w.From)
		b.WriteString(" .. ")
		b.WriteString(w.To)
		b.WriteString("\n")
	}
	b.WriteString(`
EXAMPLES:
Q: "trial balance"
-> emit_query {"entity":"ledger_balances","metrics":[{"func":"sum","column":"debit","as":"debit"},{"func":"sum","column":"credit","as":"credit"}],"dimensions":["group_name"],"filters":[],"date_range":null,"order_by":[{"column":"group_name","desc":false}],"limit":100}
Q: "total purchases this quarter"
-> emit_query {"entity":"postings","metrics":[{"func":"sum","column":"natural_net","as":"net_purchases"}],"dimensions":["month"],"filters":[{"column":"pl_group","op":"eq","values":["Purchase Accounts"]}],"date_range":{"column":"voucher_date","from":"` + thisQuarter.From + `","to":"` + thisQuarter.To + `"},"order_by":[{"column":"month","desc":false}],"limit":100}
Q: "who owes us the most"
-> emit_query {"entity":"outstandings","metrics":[{"func":"sum","column":"pending","as":"pending"}],"dimensions":["ledger_name"],"filters":[{"column":"nature","op":"eq","values":["ASSET"]}],"date_range":null,"order_by":[{"column":"pending","desc":true}],"limit":100}
Q: "total sales for every company"
-> emit_query {"entity":"","metrics":[],"dimensions":[],"filters":[],"date_range":null,"order_by":[],"limit":0}
`)
	return b.String()
}

// iso formats a date the way every date in the DSL is spelled (YYYY-MM-DD), so
// the "TODAY IS" line and the resolved windows cannot disagree in format.
func iso(t time.Time) string { return t.Format("2006-01-02") }

// windowFor returns the resolved window for one phrase, or a zero NamedWindow if
// the parser produced none. The examples interpolate a LIVE window rather than a
// hard-coded one: a frozen example window is what taught the model to answer
// "this quarter" with a quarter three years gone (D21).
func windowFor(windows []query.NamedWindow, phrase string) query.NamedWindow {
	for _, w := range windows {
		if w.Phrase == phrase {
			return w
		}
	}
	return query.NamedWindow{}
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
