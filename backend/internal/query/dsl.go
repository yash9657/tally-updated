// Package query is the SAFE, conversational read-only query core — the moat of
// the AI-native Tally competitor.
//
// SAFETY MODEL (non-negotiable). User input NEVER becomes SQL text. The pipeline
// is:
//
//	NL --(LLM / FakeTranslator)--> a CLOSED typed DSL (this file)
//	   --(whitelist validation)--> only known entities/columns/ops survive
//	   --(deterministic compiler)--> a FIXED schema-driven FROM/JOIN skeleton
//	      with ONLY whitelist-mapped identifiers and ALL user values as $N args
//	   --(db.RunInReadOnlyTx, company GUC)--> RLS scopes the tenant; the
//	      read-only transaction makes any write impossible.
//
// Two independent guarantees protect the books: (a) tenant isolation from the
// default-deny RLS policies (app_current_company_id()), and (b) write-
// impossibility from the read-only transaction. Either alone is insufficient for
// the moat; both are always present.
//
// The Query struct below is BOTH the in-process DSL and the JSON wire shape: the
// HTTP endpoint returns it (so the UI can show "how this was answered") and the
// frontend mirrors it field-for-field. The JSON tags are the pinned wire
// contract; do not rename them.
package query

// Entity names one of the FIXED set of queryable business projections. Anything
// outside this set is rejected by the compiler before a single byte of SQL is
// built. Each entity maps (in whitelist.go) to a constant FROM/JOIN skeleton.
type Entity string

const (
	// EntityLedgerBalances projects current ledger balances (movement-only,
	// posted debit/credit) by ledger/group/nature.
	EntityLedgerBalances Entity = "ledger_balances"
	// EntityVouchers projects current (non-deleted) voucher versions with their
	// type, class and party.
	EntityVouchers Entity = "vouchers"
	// EntityPostings projects individual posting lines of current voucher
	// versions.
	EntityPostings Entity = "postings"
	// EntityOutstandings projects bill-wise pending amounts of current versions.
	EntityOutstandings Entity = "outstandings"
	// EntityStockMovements projects inventory movement lines of current,
	// non-deleted voucher versions (W3). Quantities, and the line value AS
	// ENTERED — never the valued cost of an issue, which only the valuer knows.
	EntityStockMovements Entity = "stock_movements"
	// EntityBankStatementLines projects imported bank statement lines, as the
	// BANK reported them (W3), with the operator's match decision joined in.
	EntityBankStatementLines Entity = "bank_statement_lines"
	// EntityProfileFacts projects the CURRENT deterministic entity facts
	// (internal/profile, migration 0096) — the same rows the agent's profile
	// tools read, so the query screen and the agent can never show two numbers
	// for one question (W3 deliverable: "each also a DSL report").
	EntityProfileFacts Entity = "profile_facts"
)

// AggFunc is one of the FIXED aggregate functions a Metric may apply. The set is
// closed; the compiler rejects any other value.
type AggFunc string

const (
	AggSum   AggFunc = "sum"
	AggCount AggFunc = "count"
	AggMin   AggFunc = "min"
	AggMax   AggFunc = "max"
	AggAvg   AggFunc = "avg"
)

// Metric is an aggregate over a whitelisted numeric column. Column must be in the
// entity's metric whitelist, or "*" for count. As is the output alias (also a
// legal ORDER BY target); it is sanitized to a safe identifier by the compiler.
type Metric struct {
	Func   AggFunc `json:"func"`
	Column string  `json:"column"`
	As     string  `json:"as"`
}

// Op is one of the FIXED comparison operators a Filter may use. The set is
// closed; the compiler maps each to a constant SQL fragment (never built from the
// request) and always binds the compared value(s) as $N parameters.
type Op string

const (
	OpEq   Op = "eq"
	OpNe   Op = "ne"
	OpGt   Op = "gt"
	OpGe   Op = "ge"
	OpLt   Op = "lt"
	OpLe   Op = "le"
	OpLike Op = "like"
	OpIn   Op = "in"
)

// Filter is a WHERE predicate on a whitelisted filter column. Column resolves
// only through the entity's filter whitelist; Values are ALWAYS bound as $N args
// (an IN list expands to ($1,$2,...)). A filter never contributes any
// user-controlled text to the SQL string.
type Filter struct {
	Column string   `json:"column"`
	Op     Op       `json:"op"`
	Values []string `json:"values"`
}

// DateRange is an inclusive [From,To] bound on the entity's date column. Column
// must be the entity's whitelisted date column; From/To are ISO dates ("" means
// unbounded on that side) and are bound as $N params (cast ::date in SQL).
type DateRange struct {
	Column string `json:"column"`
	From   string `json:"from"`
	To     string `json:"to"`
}

// OrderTerm orders by a SELECTED dimension identifier or a metric alias. The
// compiler validates Column against the emitted dimensions + metric aliases ONLY
// (never the global whitelist), so a query cannot order by a column it did not
// select (which would leak an ordering oracle).
type OrderTerm struct {
	Column string `json:"column"`
	Desc   bool   `json:"desc"`
}

// Query is the closed typed DSL: a single aggregate-projection over one entity.
// It is the only thing the LLM produces (never SQL), and the only input to the
// deterministic compiler.
type Query struct {
	Entity     Entity      `json:"entity"`
	Metrics    []Metric    `json:"metrics"`
	Dimensions []string    `json:"dimensions"`
	Filters    []Filter    `json:"filters"`
	DateRange  *DateRange  `json:"date_range"`
	OrderBy    []OrderTerm `json:"order_by"`
	Limit      int         `json:"limit"`
}

// Limit clamping bounds. A 0 limit defaults to DefaultLimit; anything above
// MaxLimit is capped. The clamp happens in the compiler so it cannot be bypassed
// by hand-crafting a DSL.
const (
	DefaultLimit = 100
	MaxLimit     = 1000
)
