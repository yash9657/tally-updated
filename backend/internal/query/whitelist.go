package query

// whitelist.go defines, per entity, the FIXED schema-driven query skeleton and
// the closed maps of allowed identifiers. Everything here is server-authored and
// constant: nothing in this file is ever built from a request. The compiler may
// emit an identifier ONLY by looking it up in one of these maps; a value it
// cannot find is rejected (typed error), never concatenated.
//
// The skeletons bake in the security-critical invariants so the compiler cannot
// forget them:
//
//   - voucher/posting/outstanding entities JOIN voucher_head on
//     current_version_id and require is_deleted=false, so only the current,
//     non-deleted version of each voucher contributes (no double-counting of
//     superseded ALTER versions). This is verbatim the daybook.go guard.
//   - every joined table (ledger, account_group, voucher_type, ledger_balance,
//     bill_allocation, voucher_version, voucher_head) carries company_id and is
//     FORCE-RLS, so cross-entity JOINs stay tenant-scoped automatically.
//
// PAYLOAD CARVE-OUT (sanctioned). The "never expose voucher_version.payload"
// rule means: never SELECT the raw JSONB blob, and never accept a user-controlled
// jsonb path. A FIXED scalar extraction baked into the whitelist —
// COALESCE(vv.payload->>'class','') — is permitted: it is a constant fragment in
// this file, never assembled from the request, exactly as daybook.go does. No
// user-controllable jsonb path operator exists anywhere in this package.

// valueType constrains the LITERAL values a filter accepts, in addition to the
// $N binding. It is a defense-in-depth correctness check (e.g. dr_cr only D/C,
// nature only the four enum values) so a nonsense literal is rejected up front
// rather than silently returning zero rows. It does NOT relax the $N binding:
// values are still always bound as parameters.
type valueType int

const (
	valueAny     valueType = iota // any string (names, like-patterns)
	valueDate                     // ISO date YYYY-MM-DD
	valueNature                   // ASSET | LIABILITY | INCOME | EXPENSE
	valueDrCr                     // D | C
)

// metricCol describes a whitelisted numeric/metric source. expr is the constant
// SQL fragment (a column reference or a fixed aggregate-friendly expression).
// isMoney marks NUMERIC(19,4) money columns whose result is stringified via
// StringFixed(4) so totals match report output to the paisa.
type metricCol struct {
	expr    string
	isMoney bool
}

// filterCol describes a whitelisted filter target: the constant SQL identifier,
// the set of operators it accepts, and the value-type that constrains literals.
type filterCol struct {
	expr    string
	ops     map[Op]bool
	valType valueType
}

// entityDef is the constant definition of one queryable entity.
type entityDef struct {
	// skeleton is the FIXED FROM/JOIN (+ baked guards) fragment. It begins after
	// "FROM " and includes all JOINs; baked WHERE guards live in whereGuard so
	// filters can be appended with AND.
	skeleton string
	// whereGuard is the constant WHERE predicate (current-version/not-deleted
	// guards). Empty for entities that need none. Always emitted; user filters
	// are ANDed after it.
	whereGuard string
	// dimensions maps a display name to its constant SQL identifier/expression.
	dimensions map[string]string
	// metrics maps a metric column name to its source. "*" (for count) is
	// handled specially by the compiler and need not appear here.
	metrics map[string]metricCol
	// filters maps a filter column name to its definition.
	filters map[string]filterCol
	// dateCols maps an allowed DateRange.Column to its constant SQL identifier.
	dateCols map[string]string
}

// allOps is the full operator set used by free-text columns (names).
func allOps() map[Op]bool {
	return map[Op]bool{OpEq: true, OpNe: true, OpGt: true, OpGe: true, OpLt: true, OpLe: true, OpLike: true, OpIn: true}
}

// nameOps is the operator set for name/string columns (no ordered comparisons).
func nameOps() map[Op]bool {
	return map[Op]bool{OpEq: true, OpNe: true, OpLike: true, OpIn: true}
}

// enumOps is the operator set for closed-enum columns (dr_cr, nature).
func enumOps() map[Op]bool {
	return map[Op]bool{OpEq: true, OpNe: true, OpIn: true}
}

// dateOps is the operator set for date columns.
func dateOps() map[Op]bool {
	return map[Op]bool{OpEq: true, OpNe: true, OpGt: true, OpGe: true, OpLt: true, OpLe: true}
}

// entities is the closed registry. A nil lookup result is a hard reject.
var entities = map[Entity]entityDef{
	// ---------------------------------------------------------------------
	// ledger_balances — current ledger balances (movement-only: posted
	// debit/credit). All three tables are FORCE-RLS, so tenant-scoped. No
	// version/delete guard needed (no vouchers involved).
	//
	// DIVERGENCE NOTE: unlike reporting.loadLedgerBalances, opening_balance is
	// NOT folded in here — ledger_balances reflects POSTED movement only. A
	// "balances by group" query may therefore differ from the Trial Balance
	// report by the opening balances; this is pinned and documented.
	// ---------------------------------------------------------------------
	EntityLedgerBalances: {
		skeleton: `ledger l
		   JOIN account_group g ON g.id = l.group_id
		   LEFT JOIN ledger_balance lb ON lb.ledger_id = l.id`,
		whereGuard: "",
		dimensions: map[string]string{
			"ledger_name": "l.name",
			"group_name":  "g.name",
			"nature":      "g.nature::text",
		},
		metrics: map[string]metricCol{
			"debit":  {expr: "COALESCE(SUM(lb.debit),0)", isMoney: true},
			"credit": {expr: "COALESCE(SUM(lb.credit),0)", isMoney: true},
			// net is a fixed money expression; the user cannot pick the sign.
			"net": {expr: "COALESCE(SUM(lb.debit),0) - COALESCE(SUM(lb.credit),0)", isMoney: true},
		},
		filters: map[string]filterCol{
			"ledger_name": {expr: "l.name", ops: nameOps(), valType: valueAny},
			"group_name":  {expr: "g.name", ops: nameOps(), valType: valueAny},
			"nature":      {expr: "g.nature::text", ops: enumOps(), valType: valueNature},
		},
		dateCols: map[string]string{},
	},

	// ---------------------------------------------------------------------
	// vouchers — current (non-deleted) voucher versions. The vh join +
	// is_deleted=false guard is the daybook.go invariant. A LEFT JOIN to ledger
	// (aliased party) resolves the party name via party_ledger_id; the join is
	// constant, only the compared value is a $N param.
	// ---------------------------------------------------------------------
	EntityVouchers: {
		skeleton: `voucher_version vv
		   JOIN voucher_head vh
		     ON vh.company_id = vv.company_id
		    AND vh.voucher_id = vv.voucher_id
		    AND vh.current_version_id = vv.id
		   LEFT JOIN voucher_type vt ON vt.id = vv.voucher_type_id
		   LEFT JOIN ledger party
		     ON party.company_id = vv.company_id
		    AND party.id = vv.party_ledger_id`,
		whereGuard: "vh.is_deleted = false",
		dimensions: map[string]string{
			"voucher_type_name": "COALESCE(vt.name,'')",
			// sanctioned payload carve-out: a FIXED scalar extraction, never a
			// user-controlled jsonb path.
			"voucher_class": "COALESCE(vv.payload->>'class','')",
			"month":         "to_char(vv.voucher_date,'YYYY-MM')",
			"party_name":    "COALESCE(party.name,'')",
		},
		metrics: map[string]metricCol{
			"taxable_value": {expr: "COALESCE(SUM(vv.taxable_value),0)", isMoney: true},
			"cgst":          {expr: "COALESCE(SUM(vv.cgst),0)", isMoney: true},
			"sgst":          {expr: "COALESCE(SUM(vv.sgst),0)", isMoney: true},
			"igst":          {expr: "COALESCE(SUM(vv.igst),0)", isMoney: true},
		},
		filters: map[string]filterCol{
			"voucher_type_name": {expr: "vt.name", ops: nameOps(), valType: valueAny},
			"party":             {expr: "party.name", ops: nameOps(), valType: valueAny},
			"voucher_date":      {expr: "vv.voucher_date", ops: dateOps(), valType: valueDate},
		},
		dateCols: map[string]string{
			"voucher_date": "vv.voucher_date",
		},
	},

	// ---------------------------------------------------------------------
	// postings — posting lines of current, non-deleted voucher versions. The vh
	// join keeps only current versions (mirrors the ledger_balance rebuild).
	// ---------------------------------------------------------------------
	EntityPostings: {
		skeleton: `posting p
		   JOIN voucher_version vv ON vv.id = p.voucher_version_id
		   JOIN voucher_head vh
		     ON vh.company_id = vv.company_id
		    AND vh.voucher_id = vv.voucher_id
		    AND vh.current_version_id = vv.id
		   JOIN ledger l ON l.id = p.ledger_id
		   JOIN account_group g ON g.id = l.group_id`,
		whereGuard: "vh.is_deleted = false",
		dimensions: map[string]string{
			"ledger_name": "l.name",
			"group_name":  "g.name",
			"dr_cr":       "p.dr_cr::text",
			"nature":      "g.nature::text",
			"month":       "to_char(vv.voucher_date,'YYYY-MM')",
		},
		metrics: map[string]metricCol{
			"amount": {expr: "COALESCE(SUM(p.amount),0)", isMoney: true},
		},
		filters: map[string]filterCol{
			"ledger_name":  {expr: "l.name", ops: nameOps(), valType: valueAny},
			"group_name":   {expr: "g.name", ops: nameOps(), valType: valueAny},
			"dr_cr":        {expr: "p.dr_cr::text", ops: enumOps(), valType: valueDrCr},
			"nature":       {expr: "g.nature::text", ops: enumOps(), valType: valueNature},
			"voucher_date": {expr: "vv.voucher_date", ops: dateOps(), valType: valueDate},
		},
		dateCols: map[string]string{
			"voucher_date": "vv.voucher_date",
		},
	},

	// ---------------------------------------------------------------------
	// outstandings — bill-wise pending of current, non-deleted versions. The vh
	// join (the correction vs the bare report) keeps only current versions, so
	// superseded ALTER allocations do not double-count.
	//
	// The pending metric is nature-oriented (receivable vs payable sign flip).
	// The compiler picks the correct FIXED expression keyed off the nature
	// filter value (default ASSET/receivable), exactly like outstanding.go's
	// signExpr — the user never picks the sign.
	// ---------------------------------------------------------------------
	EntityOutstandings: {
		skeleton: `bill_allocation ba
		   JOIN voucher_head vh
		     ON vh.company_id = ba.company_id
		    AND vh.current_version_id = ba.voucher_version_id
		    AND vh.is_deleted = false
		   JOIN ledger l ON l.id = ba.ledger_id
		   JOIN account_group g ON g.id = l.group_id`,
		whereGuard: "",
		dimensions: map[string]string{
			"ledger_name": "l.name",
			"ref_name":    "ba.ref_name",
			"group_name":  "g.name",
			"nature":      "g.nature::text",
		},
		// "pending" is special-cased by the compiler (sign depends on the nature
		// filter); count(*) is handled generically. Expose only count here.
		metrics: map[string]metricCol{},
		filters: map[string]filterCol{
			"nature":      {expr: "g.nature::text", ops: map[Op]bool{OpEq: true}, valType: valueNature},
			"ledger_name": {expr: "l.name", ops: nameOps(), valType: valueAny},
			"due_date":    {expr: "ba.due_date", ops: dateOps(), valType: valueDate},
		},
		dateCols: map[string]string{
			"due_date": "ba.due_date",
		},
	},
}

// outstandingPendingExpr returns the FIXED signed-sum expression for the pending
// metric, oriented by the receivable (ASSET) vs payable (LIABILITY) nature. The
// orientation is chosen by the server from the nature filter, never by the user.
// Default (no nature filter, or ASSET) is receivable orientation: debit - credit.
func outstandingPendingExpr(payable bool) string {
	if payable {
		return "SUM(CASE WHEN ba.dr_cr='C' THEN ba.amount ELSE -ba.amount END)"
	}
	return "SUM(CASE WHEN ba.dr_cr='D' THEN ba.amount ELSE -ba.amount END)"
}
