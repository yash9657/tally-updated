package tierb

import "sort"

// views.go is the PUBLISHED-VIEW catalogue: the only relations a Tier-B query
// may name, and for each the only columns. It must equal what migration 0097
// creates in the `agent` schema — views_db_test.go reads
// information_schema.columns as agent_ro and fails on any drift in either
// direction (a column here the view lacks is a runtime error; a column the
// view has that is not here is merely unreachable, but drift is how the next
// person adds `description` to bank_lines without reading why it is absent).
//
// ⛔ WHAT IS DELIBERATELY NOT PUBLISHED, and the reason travels with it:
//   - company_id: RLS scopes every view; a column would invite a model to
//     "filter by company" and read an empty result as a refusal to widen.
//   - voucher_version.payload (raw JSONB), narration, bank descriptions:
//     free text a third party or an operator wrote is the injection surface
//     (PLAN-AGENTIC §2.4) — the same reason the DSL's bank_statement_lines
//     keeps the narration filter-only.
//   - PAN, bank account numbers, IFSC, employee and payroll anything: PII
//     that migration 0035 keeps off every seeded level by default.
//   - superseded voucher versions and deleted vouchers: the views carry the
//     current-version join, so the model cannot double-count an alteration.

// View is one published view.
type View struct {
	Name    string
	Columns []string
	Doc     string
}

// Views is the closed catalogue.
var Views = []View{
	{Name: "ledgers", Doc: "one row per ledger with its group, nature and posted movement (opening folded into closing_net)",
		Columns: []string{"ledger_id", "ledger_name", "group_name", "nature", "opening_signed", "debit", "credit", "closing_net"}},
	{Name: "vouchers", Doc: "current, non-deleted vouchers",
		Columns: []string{"voucher_id", "voucher_date", "voucher_no", "voucher_type", "voucher_class", "party_name", "taxable_value", "cgst", "sgst", "igst"}},
	{Name: "postings", Doc: "posting lines of current vouchers; natural_net is signed to the ledger's own side",
		Columns: []string{"voucher_id", "voucher_date", "voucher_type", "voucher_class", "ledger_name", "group_name", "nature", "dr_cr", "amount", "natural_net"}},
	{Name: "stock_movements", Doc: "inventory lines of current vouchers; amount is AS ENTERED, never valued cost",
		Columns: []string{"voucher_id", "voucher_date", "voucher_type", "party_name", "item_name", "godown_name", "batch_name", "direction", "qty", "rate", "amount"}},
	{Name: "bank_lines", Doc: "imported bank statement lines; no narration",
		Columns: []string{"line_id", "bank_ledger_name", "value_date", "dr_cr", "amount", "match_status"}},
	{Name: "bills", Doc: "bill-wise references (voucher and opening) with signed pending contribution",
		Columns: []string{"ledger_name", "group_name", "nature", "ref_name", "ref_type", "dr_cr", "amount", "due_date", "bill_date", "source"}},
	{Name: "items", Doc: "stock items",
		Columns: []string{"item_id", "item_name", "hsn", "gst_rate_pct"}},
	{Name: "facts", Doc: "current deterministic profile facts",
		Columns: []string{"entity_kind", "entity_name", "fact_key", "qualifier_name", "value_num", "value_text", "unit", "as_of"}},
}

// ViewSchema is the schema the views live in; the printer always qualifies.
const ViewSchema = "agent"

var viewCols = func() map[string]map[string]bool {
	m := map[string]map[string]bool{}
	for _, v := range Views {
		cs := map[string]bool{}
		for _, c := range v.Columns {
			cs[c] = true
		}
		m[v.Name] = cs
	}
	return m
}()

// Functions is the CLOSED function allow-list, with arity bounds. Every one
// is a pure scalar or aggregate in pg_catalog that cannot read another
// relation, sleep, touch a file, a setting, a sequence, a lock or the
// network, and none returns a set.
//
// Notably refused: every set-returning function (generate_series, unnest,
// json_each, regexp_split_to_table …), current_setting / set_config (the
// tenant GUC!), pg_sleep, pg_read_file, lo_*, dblink, query_to_xml, nextval,
// pg_advisory_*, version(), txid_*, to_regclass and every reg* cast, and
// every function this list does not name.
var Functions = map[string][2]int{ // name -> {min args, max args}; -1 = unbounded
	"count": {0, 1}, "sum": {1, 1}, "avg": {1, 1}, "min": {1, 1}, "max": {1, 1},
	"coalesce": {1, 8}, "nullif": {2, 2}, "greatest": {1, 8}, "least": {1, 8},
	"round": {1, 2}, "abs": {1, 1}, "floor": {1, 1}, "ceil": {1, 1},
	"lower": {1, 1}, "upper": {1, 1}, "length": {1, 1},
	// date_trunc / date_part take a FIELD as a string literal from a closed
	// set, checked by the parser — an arbitrary text first argument is
	// harmless to Postgres but there is no reason to accept it.
	"date_trunc": {2, 2}, "date_part": {2, 2},
}

// dateFields is date_trunc/date_part's first argument allow-list.
var dateFields = map[string]bool{"day": true, "week": true, "month": true, "quarter": true, "year": true, "dow": true}

// FunctionNames returns the allow-list, sorted, for the model-facing error.
func FunctionNames() []string {
	var out []string
	for f := range Functions {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// reserved are the keywords the grammar uses; none may be an alias or a
// bare column name, so `SELECT from FROM vouchers` fails at parse and never
// reaches Postgres as something surprising.
var reserved = map[string]bool{
	"select": true, "distinct": true, "from": true, "where": true, "group": true, "by": true, "having": true,
	"order": true, "limit": true, "offset": true, "as": true, "and": true, "or": true, "not": true,
	"in": true, "is": true, "null": true, "like": true, "ilike": true, "between": true, "case": true,
	"when": true, "then": true, "else": true, "end": true, "join": true, "inner": true, "left": true,
	"outer": true, "on": true, "asc": true, "desc": true, "nulls": true, "first": true, "last": true,
	"true": true, "false": true, "date": true,
	// Refused words that must never be read as an identifier either.
	"union": true, "intersect": true, "except": true, "with": true, "into": true, "for": true,
	"lateral": true, "only": true, "over": true, "window": true, "fetch": true, "returning": true,
	"values": true, "table": true, "insert": true, "update": true, "delete": true, "truncate": true,
	"drop": true, "alter": true, "create": true, "grant": true, "revoke": true, "copy": true,
	"do": true, "call": true, "set": true, "reset": true, "show": true, "explain": true, "analyze": true,
	"current_user": true, "session_user": true, "user": true, "current_role": true, "current_catalog": true,
	"current_schema": true, "current_date": true, "current_time": true, "current_timestamp": true,
	"localtime": true, "localtimestamp": true, "cast": true, "array": true, "row": true, "collate": true,
	"similar": true, "escape": true, "any": true, "all": true, "some": true, "exists": true,
	"tablesample": true, "natural": true, "using": true, "cross": true, "full": true, "right": true,
	"interval": true, "timestamp": true, "time": true, "operator": true, "rows": true, "range": true,
	"filter": true, "within": true, "ordinality": true, "listen": true, "notify": true, "lock": true,
	"prepare": true, "execute": true, "deallocate": true, "declare": true, "vacuum": true,
}
