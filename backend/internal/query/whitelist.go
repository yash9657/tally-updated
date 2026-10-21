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
//     bill_allocation, voucher_version, voucher_head, ledger_opening_bill, and
//     company itself) carries company_id (or IS the company row) and is
//     FORCE-RLS, so cross-entity JOINs stay tenant-scoped automatically. The
//     outstandings skeleton relies on this for its UNION and for the
//     books-beginning sub-select; do not "fix" either by hand-writing a company
//     predicate, which would be a second, drifting copy of the scoping rule.
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
	valueAny         valueType = iota // any string (names, like-patterns)
	valueDate                         // ISO date YYYY-MM-DD
	valueNature                       // ASSET | LIABILITY | INCOME | EXPENSE
	valueDrCr                         // D | C
	valueDirection                    // IN | OUT (stock_direction)
	valueMatchStatus                  // matched | set-aside | unresolved
	valueEntityKind                   // party | item | tenant | stream
)

// metricCol describes a whitelisted numeric/metric source.
//
//	rawExpr — the constant SQL fragment the compiler aggregates over. For a
//	          plain metric this is a ROW-LEVEL expression (a bare column such as
//	          "lb.debit", or a fixed CASE over row columns), and the compiler
//	          wraps it: COALESCE(SUM(rawExpr),0) / MIN(rawExpr) / MAX(rawExpr) /
//	          AVG(rawExpr). When preAgg is true, rawExpr is ALREADY a complete
//	          aggregate expression and is emitted verbatim (sum only).
//	aggs    — the CLOSED set of aggregate functions legal on this column. A nil
//	          or missing entry is a hard reject, so every literal below states
//	          its set EXPLICITLY (never relying on the zero value).
//	isMoney — marks NUMERIC(19,4) money columns whose result is stringified via
//	          StringFixed(4) so totals match report output to the paisa.
//
// NULL SEMANTICS (pinned, WS2). Only SUM is COALESCE'd to 0. MIN/MAX/AVG are
// emitted bare, so a group in which every underlying value is SQL NULL yields
// NULL, which execute.go renders as the EMPTY STRING in a column whose declared
// type is "numeric". This is deliberate: 0 and "no data" are different answers
// for a min/max/avg, and silently reporting 0.0000 would be a wrong number.
// (Under the current schema vv.taxable_value/cgst/sgst/igst are NULLable and are
// only populated when GST derivation ran, so this case is reachable.)
//
// AVG DENOMINATOR (pinned, WS2). Because rawExpr for ledger_balances "debit" and
// "credit" is the BARE column (which keeps the SUM SQL byte-identical to the
// pre-WS2 compiler), AVG over them uses the SQL denominator: ledgers with NO
// ledger_balance row (no posted movement) are EXCLUDED from the average. The
// "opening"/"closing_net" metrics COALESCE inside rawExpr, so their AVG counts
// every ledger.
type metricCol struct {
	rawExpr string
	aggs    map[AggFunc]bool
	preAgg  bool
	isMoney bool
}

// sumOnly is the agg set for composite, PRE-AGGREGATED expressions (e.g. the
// ledger_balances "net"), where a MIN/MAX/AVG has no meaningful row-level
// operand. The closed-set rejection message is produced by the compiler.
func sumOnly() map[AggFunc]bool { return map[AggFunc]bool{AggSum: true} }

// numericAggs is the agg set for ROW-LEVEL numeric columns: all four numeric
// aggregates are legal because the compiler wraps the raw expression itself.
func numericAggs() map[AggFunc]bool {
	return map[AggFunc]bool{AggSum: true, AggMin: true, AggMax: true, AggAvg: true}
}

// openingSignedExpr is the CONSTANT row-level signed opening balance, oriented
// exactly like reporting.loadLedgerBalances (which adds the opening to Credit
// when opening_dc='C' and to Debit otherwise; ledger.opening_dc is CHECK-
// constrained to 'D'|'C', so the signed CASE is equivalent). Debit-positive.
const openingSignedExpr = `CASE WHEN l.opening_dc='D' THEN l.opening_balance ELSE -l.opening_balance END`

// closingNetExpr is the CONSTANT row-level closing position: signed opening plus
// posted movement, debit-positive — i.e. reporting's (Debit - Credit) per ledger.
//
// The INNER COALESCEs are LOAD-BEARING, not decoration: ledger_balance's primary
// key is (company_id, ledger_id) and a row only exists once something has been
// posted, so the LEFT JOIN yields NULL debit/credit for every ledger with no
// movement. Without COALESCE, NULL would propagate through the addition and SUM
// would SKIP those rows entirely — silently vanishing the opening balance of
// every unposted ledger (e.g. Capital, Opening Stock) and breaking the Trial
// Balance reconciliation. Do not remove them.
const closingNetExpr = `(` + openingSignedExpr + `) + COALESCE(lb.debit,0) - COALESCE(lb.credit,0)`

// naturalNetExpr is the CONSTANT row-level posting movement signed to the
// LEDGER'S OWN NATURAL SIDE, and it is a transcription of
// reporting.ledgerBalance.signed() — assets and expenses are debit-positive,
// liabilities and income are credit-positive. It is not a convenience: it is the
// only definition under which a query answer and the Profit & Loss can be the
// same number.
//
// ⛔ WHY THIS EXISTS AT ALL (D51). "total sales" used to be
// sum(vv.taxable_value) filtered to voucher_type_name='Sales'. That figure
// matched NO report in the product, because it silently dropped two things: the
// GST on every invoice (which posts to Duties & Taxes, a different group) and
// every CREDIT NOTE (a sales return is its own voucher type, so a 12,000.00
// return never reduced it) — and because vv.taxable_value is NULL unless GST
// derivation ran, so on a non-GST book it answered 0.0000 for a trading company.
// Summing signed POSTINGS has none of those failure modes: a credit note debits
// Sales and therefore subtracts itself, output tax is on another ledger and
// therefore never enters, and posting.amount is NOT NULL always.
//
// Nature is compared as ::text, consistently with every other use of
// account_group.nature in this file, so the enum's storage never has to be
// reasoned about at the call site.
const naturalNetExpr = `CASE WHEN g.nature::text IN ('ASSET','EXPENSE')
	                            THEN (CASE WHEN p.dr_cr='D' THEN p.amount ELSE -p.amount END)
	                            ELSE (CASE WHEN p.dr_cr='C' THEN p.amount ELSE -p.amount END) END`

// plRootGroupExpr is the CONSTANT scalar sub-select naming the P&L ROOT group a
// posting's ledger rolls up into ("Sales Accounts", "Purchase Accounts",
// "Indirect Expenses", …), or NULL for a balance-sheet ledger.
//
// ⛔ IT IS A SUBTREE TEST, AND FILTERING ON `group_name` INSTEAD IS A TRAP.
// account_group is an ltree hierarchy and a user may perfectly well file export
// invoices under an "Export Sales" sub-group of Sales Accounts. The P&L rolls
// that up (buildGroupNode recurses into children); a query filtering
// g.name = 'Sales Accounts' would not, and the two surfaces would disagree by
// exactly the sub-group — the same "two reports over one set of books" failure
// D51 records, re-created by the fix for it.
//
// The root is chosen by the P&L's OWN rule, not by parent_id IS NULL: a P&L root
// is an is_pl group whose parent is not itself is_pl (reporting.ProfitAndLoss).
// `<@` is ltree "is a descendant of, or equal to", so a ledger directly under the
// root matches too. account_group is FORCE-RLS, so the sub-select is
// tenant-scoped without a company predicate, exactly like every other join here.
const plRootGroupExpr = `(SELECT root.name FROM account_group root
	                          WHERE g.path <@ root.path
	                            AND root.is_pl
	                            AND NOT EXISTS (SELECT 1 FROM account_group par
	                                             WHERE par.id = root.parent_id AND par.is_pl)
	                          LIMIT 1)`

// ⛔⛔ THERE IS NO "AS ON" IN THIS SKELETON ANY MORE, AND ITS ABSENCE IS THE POINT.
// (D86, and it is the D20 rule applied a second time.)
//
// This entity has no `as_on` parameter — the DSL surface cannot express one — so the
// question it answers is the same question `reporting.Outstandings(…, asOn: "")`
// answers: EVERY bill these books carry. Until D86 both surfaces bounded that at
// today, which made the bill-wise breakup disagree with the ledger balance it is a
// breakup OF the moment a bill was dated ahead. Both surfaces now drop the bound
// together, and `report_parity_execute_test.go` is what refuses to let them diverge.
//
// ⛔ THE OPENING-BILL BRANCH LOST ITS books_beginning_from GUARD FOR THE SAME REASON,
// AND THAT WAS DECIDED RATHER THAN DROPPED. The guard asks "had the books begun by
// the as-on date?" — a question with no meaning when there is no as-on date. The
// opening bill-wise breakup IS part of these books and the Balance Sheet's ledger
// balance always includes it, so an unbounded bill-wise breakup includes it too.
// `reporting.Outstandings` makes the same decision in the same words
// (`bounded && !openingIsInScope`).
//
// ⚠️ WHAT WAS HERE, KEPT BECAUSE THE REASONING IS STILL RIGHT AND WILL BE NEEDED IF
// AN `as_on` PARAMETER IS EVER ADDED TO THE DSL: the constant was spelled
// `(now() AT TIME ZONE 'UTC')::date` rather than CURRENT_DATE on purpose, because
// `reporting.resolveAsOn` defaults to `time.Now().UTC()` while CURRENT_DATE is
// evaluated in the SESSION time zone — so on a server running in IST the two surfaces
// would name different days for five and a half hours out of every twenty-four, and
// the mismatch would appear only at night.
//
// outstandingAsOnExpr WAS the CONSTANT "as on" date the outstandings entity is
// answered at: TODAY in UTC.
//
// It is spelled `(now() AT TIME ZONE 'UTC')::date` rather than CURRENT_DATE on
// purpose. reporting.resolveAsOn defaults an empty as-on to time.Now().UTC()
// truncated to a day; CURRENT_DATE is evaluated in the SESSION time zone, so on
// a server running in IST the two surfaces would name different days for five
// and a half hours out of every twenty-four and the mismatch would appear only
// at night. Matching the report's clock exactly is the whole point of the
// entity.
// (retained, unreferenced, as the spelling to reuse — see the note above)
const outstandingAsOnExprIfEverNeeded = `(now() AT TIME ZONE 'UTC')::date`

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
	EntityBillAllocations: {
		skeleton: `bill_allocation ba
		 JOIN voucher_version vv ON vv.company_id = ba.company_id AND vv.id = ba.voucher_version_id
		 JOIN voucher_head vh ON vh.company_id = vv.company_id AND vh.current_version_id = vv.id
		 JOIN ledger l ON l.company_id = ba.company_id AND l.id = ba.ledger_id`,
		whereGuard: "vh.is_deleted = false",
		dimensions: map[string]string{
			"ledger_name": "l.name", "ref_name": "ba.ref_name", "ref_type": "ba.ref_type::text",
			"dr_cr": "ba.dr_cr::text", "voucher_no": "COALESCE(vv.voucher_no,'')",
			"voucher_date":  "to_char(vv.voucher_date,'YYYY-MM-DD')",
			"voucher_class": "COALESCE(vv.payload->>'class','')",
			"due_date":      "COALESCE(to_char(ba.due_date,'YYYY-MM-DD'),'')",
		},
		metrics: map[string]metricCol{
			"amount":             {rawExpr: "ba.amount", aggs: numericAggs(), isMoney: true},
			"debit_minus_credit": {rawExpr: "CASE WHEN ba.dr_cr = 'D' THEN ba.amount ELSE -ba.amount END", aggs: sumOnly(), isMoney: true},
		},
		filters: map[string]filterCol{
			"ledger_name":  {expr: "l.name", ops: nameOps(), valType: valueAny},
			"ref_name":     {expr: "ba.ref_name", ops: nameOps(), valType: valueAny},
			"ref_type":     {expr: "ba.ref_type::text", ops: enumOps(), valType: valueAny},
			"dr_cr":        {expr: "ba.dr_cr::text", ops: enumOps(), valType: valueDrCr},
			"voucher_no":   {expr: "vv.voucher_no", ops: nameOps(), valType: valueAny},
			"voucher_date": {expr: "vv.voucher_date", ops: dateOps(), valType: valueDate},
		},
		dateCols: map[string]string{"voucher_date": "vv.voucher_date", "due_date": "ba.due_date"},
	},
	// ---------------------------------------------------------------------
	// ledger_balances — ledger positions. All three tables are FORCE-RLS, so
	// tenant-scoped. No version/delete guard needed (no vouchers involved).
	//
	// DIVERGENCE NOTE (updated by WS2 — the divergence is now CLOSABLE by the
	// caller). Two families of metrics live here and they answer different
	// questions:
	//
	//   - MOVEMENT-ONLY: "debit", "credit", "net" read ledger_balance alone, so
	//     they report POSTED movement and EXCLUDE opening balances. A "balances
	//     by group" query built on these still differs from the Trial Balance
	//     report by exactly the opening balances. This is retained deliberately
	//     ("what moved this period" is a real question) and remains the default.
	//
	//   - OPENING-FOLDED: "opening" (the signed opening balance) and
	//     "closing_net" (signed opening + posted movement) mirror
	//     reporting.loadLedgerBalances exactly. Selecting sum(closing_net)
	//     CLOSES the divergence: per ledger it equals the Trial Balance row's
	//     (Debit - Credit), and summed over all ledgers it is 0 for a balanced
	//     book. This equivalence is pinned by an execute test whose oracle is
	//     the independent reporting package.
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
			"debit":  {rawExpr: "lb.debit", aggs: numericAggs(), isMoney: true},
			"credit": {rawExpr: "lb.credit", aggs: numericAggs(), isMoney: true},
			// net is a fixed PRE-AGGREGATED money expression; the user cannot pick
			// the sign, and there is no row-level operand a min/max/avg could
			// apply to, so it is sum-only.
			"net": {rawExpr: "COALESCE(SUM(lb.debit),0) - COALESCE(SUM(lb.credit),0)", aggs: sumOnly(), preAgg: true, isMoney: true},
			// opening / closing_net fold the opening balance in (see the
			// DIVERGENCE NOTE above). Both are row-level, so all four aggregates
			// are legal: sum(closing_net) is the Trial Balance closing,
			// min/max(opening) answer "largest opening balance" style questions.
			"opening":     {rawExpr: openingSignedExpr, aggs: numericAggs(), isMoney: true},
			"closing_net": {rawExpr: closingNetExpr, aggs: numericAggs(), isMoney: true},
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
			"voucher_no":        "COALESCE(vv.voucher_no,'')",
			"voucher_date":      "to_char(vv.voucher_date,'YYYY-MM-DD')",
			"voucher_type_name": "COALESCE(vt.name,'')",
			// sanctioned payload carve-out: a FIXED scalar extraction, never a
			// user-controlled jsonb path.
			"voucher_class": "COALESCE(vv.payload->>'class','')",
			"month":         "to_char(vv.voucher_date,'YYYY-MM')",
			// CALENDAR quarter (Q1 = Jan-Mar), NOT the Indian financial-year
			// quarter a CA reads on a GST return (where Q1 = Apr-Jun). Pinned
			// deliberately and consistent with FakeTranslator.quarterRange, which
			// is also calendar-based. Do not confuse with "this year", which IS
			// the financial year (FakeTranslator.fyRange).
			"quarter":    `to_char(vv.voucher_date,'YYYY-"Q"Q')`,
			"year":       "to_char(vv.voucher_date,'YYYY')",
			"party_name": "COALESCE(party.name,'')",
		},
		metrics: map[string]metricCol{
			// NOTE: these four columns are NULLable and are only stamped when GST
			// derivation ran, so min/max/avg over a group of un-stamped vouchers
			// returns SQL NULL -> "" on the wire (see metricCol's NULL SEMANTICS).
			"taxable_value": {rawExpr: "vv.taxable_value", aggs: numericAggs(), isMoney: true},
			"cgst":          {rawExpr: "vv.cgst", aggs: numericAggs(), isMoney: true},
			"sgst":          {rawExpr: "vv.sgst", aggs: numericAggs(), isMoney: true},
			"igst":          {rawExpr: "vv.igst", aggs: numericAggs(), isMoney: true},
		},
		filters: map[string]filterCol{
			"voucher_no":        {expr: "vv.voucher_no", ops: nameOps(), valType: valueAny},
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
			// CALENDAR quarter — see the vouchers entity's note.
			"quarter": `to_char(vv.voucher_date,'YYYY-"Q"Q')`,
			"year":    "to_char(vv.voucher_date,'YYYY')",
		},
		metrics: map[string]metricCol{
			// posting.amount is NOT NULL with CHECK (amount >= 0), so min/max/avg
			// over it are always defined.
			"amount": {rawExpr: "p.amount", aggs: numericAggs(), isMoney: true},
			// natural_net is the movement signed to the LEDGER'S OWN natural side
			// (see naturalNetExpr). SUM-only, for the same reason outstandings'
			// pending is: a min/max/avg over one sign-flipped posting line is a
			// single journal leg, not a position, and reporting it as one would be
			// a number nobody could reconcile.
			"natural_net": {rawExpr: naturalNetExpr, aggs: sumOnly(), isMoney: true},
		},
		filters: map[string]filterCol{
			"ledger_name": {expr: "l.name", ops: nameOps(), valType: valueAny},
			"group_name":  {expr: "g.name", ops: nameOps(), valType: valueAny},
			// pl_group is the P&L ROOT this posting rolls up into, not the ledger's
			// immediate group — see plRootGroupExpr. `group_name` above is left
			// exactly as it was; the two answer different questions and both are
			// legitimate.
			"pl_group":     {expr: plRootGroupExpr, ops: nameOps(), valType: valueAny},
			"dr_cr":        {expr: "p.dr_cr::text", ops: enumOps(), valType: valueDrCr},
			"nature":       {expr: "g.nature::text", ops: enumOps(), valType: valueNature},
			"voucher_date": {expr: "vv.voucher_date", ops: dateOps(), valType: valueDate},
		},
		dateCols: map[string]string{
			"voucher_date": "vv.voucher_date",
		},
	},

	// ---------------------------------------------------------------------
	// outstandings — bill-wise pending, AS AT TODAY, from the SAME TWO SOURCES
	// the Outstandings report reads.
	//
	// The pending metric is nature-oriented (receivable vs payable sign flip).
	// The compiler picks the correct FIXED expression keyed off the nature
	// filter value (default ASSET/receivable), exactly like outstanding.go's
	// signExpr — the user never picks the sign.
	//
	// =====================================================================
	// ⛔ D20: THE NL ANSWER DID NOT MATCH THE REPORT, AND IT WAS ONE MISSING
	// UNION. THIS SKELETON NOW MIRRORS reporting.Outstandings, INCLUDING BOTH
	// OF ITS DATE GUARDS. Read that function before changing a line here.
	// =====================================================================
	// The old skeleton read `bill_allocation` and nothing else. Driven in a
	// browser: "receivables" answered 1,33,920.00 against the Outstandings
	// report's 2,53,920.00 and "payables" 36,050.00 against 1,04,050.00 —
	// short, in both cases, by exactly the OPENING bill-wise breakup carried on
	// the ledger master (`ledger_opening_bill`), which the report unions in and
	// this did not. Two surfaces over one set of books, each internally
	// consistent, disagreeing by 1,20,000.00 with nothing to reconcile them.
	//
	// Matching the report means matching THREE things, not one:
	//
	//  1. THE UNION. ledger_opening_bill is the bill-wise breakup of
	//     ledger.opening_balance. The Balance Sheet's Sundry Debtors includes
	//     the opening balance, so a bill-wise view that omits its references is
	//     short by exactly it. A settlement posted later against an opening ref
	//     nets naturally: both legs land in the same (ledger, ref_name) group.
	//
	//  2. THE AS-ON BOUND ON THE VOUCHER SIDE (`vv.voucher_date <= as-on`). The
	//     report acquired this in the same pass that produced D20's fix: without
	//     it a bill raised AFTER the as-on date was reported under a caption
	//     asserting the two dates agreed. Spelled the way movementSQL spells it
	//     — a NULL voucher date is dated into NO period and is excluded here
	//     exactly as it is excluded from every other as-at report.
	//
	//  3. THE BOOKS-BEGINNING GUARD ON THE OPENING SIDE. An opening carries no
	//     date of its own; it is stated as at company.books_beginning_from
	//     (falling back to fy_start_date). Folding it into a report dated BEFORE
	//     that reports a position that did not exist yet. This is
	//     reporting.openingIsInScope, transcribed — INCLUDING ITS FAIL-SAFE:
	//     when the company row cannot be read the opening stays IN (the
	//     pre-existing behaviour, and the safe direction for a guard). The Go
	//     version gets that by returning true on a query error; the SQL gets it
	//     from COALESCE(..., '-infinity'), because a bare `>= NULL` is NULL,
	//     which WHERE treats as false and would have silently dropped every
	//     opening reference the moment the GUC was unset. Do not remove the
	//     COALESCE.
	//
	// AS-ON IS TODAY, AND ONLY TODAY. The DSL has no as-on parameter (the
	// skeleton is a constant and takes no $N), so this entity answers the
	// question the NL surface actually asks — "what is outstanding now" — at the
	// report's own default as-on. The translator REFUSES a period-scoped
	// outstandings question rather than silently answering today's position for
	// it (see translate.go's refuseIfPeriodScoped), and QueryScreen prints the
	// basis above the table. A future as-on would need a bound parameter reaching
	// inside this sub-select, which is a compiler change, not a whitelist one.
	//
	// EVERY TABLE HERE IS FORCE-RLS (bill_allocation, voucher_version,
	// voucher_head, ledger_opening_bill, ledger, account_group, company), so the
	// union is tenant-scoped with no company predicate — identical to the report,
	// and NOT something to "harden" by hand-writing one.
	// ---------------------------------------------------------------------
	EntityOutstandings: {
		skeleton: `(
		            SELECT b.ledger_id, b.ref_name, b.dr_cr, b.amount, b.due_date
		              FROM bill_allocation b
		              JOIN voucher_version vv ON vv.id = b.voucher_version_id
		              JOIN voucher_head    vh
		                ON vh.company_id = vv.company_id
		               AND vh.voucher_id = vv.voucher_id
		               AND vh.current_version_id = vv.id
		             WHERE vh.is_deleted = false
		             UNION ALL
		            SELECT ob.ledger_id, ob.ref_name, ob.dr_cr, ob.amount, ob.due_date
		              FROM ledger_opening_bill ob
		           ) ba
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
		// filter) BEFORE this map is consulted, and is SUM-ONLY: min/max/avg over
		// a sign-flipped allocation amount is not a meaningful figure, so the
		// compiler rejects them there. count(*) is handled generically. This map
		// therefore stays empty by design — it is not an oversight.
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

	// ---------------------------------------------------------------------
	// stock_movements — inventory movement lines of current, non-deleted
	// voucher versions (W3). The FROM/JOIN is stockMovementSQL's
	// (reporting/stock_summary.go) with the vh current-version join and the
	// is_deleted guard carried over verbatim, because movements are versioned
	// exactly like postings: an ALTER re-states the lines under a new
	// voucher_version and the superseded rows stay in the table forever, so an
	// item altered from 10 to 4 must count 4 and not 14.
	//
	// ⛔ THERE IS NO "VALUE OF AN ISSUE" METRIC HERE, AND THAT IS THE DECISION.
	// sm.amount is the line AS ENTERED — for a sale, the SELLING value. What
	// the goods COST is not a column anywhere: it is derived at report time by
	// replaying every movement through the item's valuer (weighted average,
	// FIFO, LIFO — internal/inventory), and a SQL SUM cannot replay a valuer.
	// A metric named "outward_value" that summed sm.amount would agree with the
	// Stock Movement report on a purchase and disagree with it on every sale,
	// silently, by exactly the gross margin. So the metric is named `amount`
	// and documented as "as entered", and questions about stock VALUE belong to
	// the stock-summary / stock-movement reports (the semantic catalogue routes
	// them there). Quantities have no valuer and reconcile exactly;
	// report_parity_execute_test.go's stock case pins that.
	//
	// ⚠️ OPENINGS ARE NOT HERE. stock_opening rows are positions the books
	// began with, not movements, and the Stock Movement report excludes them
	// for the same reason. "How much do we have" is the Stock Summary.
	//
	// Every joined table (stock_movement, voucher_version, voucher_head,
	// stock_item, godown, stock_batch, voucher_type, ledger) is FORCE-RLS, so
	// the skeleton is tenant-scoped with no company predicate — the rule stated
	// at the top of this file.
	// ---------------------------------------------------------------------
	EntityStockMovements: {
		skeleton: `stock_movement sm
		   JOIN voucher_version vv ON vv.id = sm.voucher_version_id
		   JOIN voucher_head vh
		     ON vh.company_id = vv.company_id
		    AND vh.voucher_id = vv.voucher_id
		    AND vh.current_version_id = vv.id
		   JOIN stock_item si ON si.id = sm.stock_item_id
		   LEFT JOIN godown gd ON gd.id = sm.godown_id
		   LEFT JOIN stock_batch sb ON sb.id = sm.batch_id
		   LEFT JOIN voucher_type vt ON vt.id = vv.voucher_type_id
		   LEFT JOIN ledger party
		     ON party.company_id = vv.company_id
		    AND party.id = vv.party_ledger_id`,
		whereGuard: "vh.is_deleted = false",
		dimensions: map[string]string{
			"item_name": "si.name",
			// An untracked location/batch is a POSITION like any other (see
			// reporting.StockPositionRow); COALESCE to '' so it groups as one
			// row rather than vanishing into a NULL group the UI renders blank.
			"godown_name":       "COALESCE(gd.name,'')",
			"batch_name":        "COALESCE(sb.name,'')",
			"direction":         "sm.direction::text",
			"voucher_type_name": "COALESCE(vt.name,'')",
			"party_name":        "COALESCE(party.name,'')",
			"month":             "to_char(vv.voucher_date,'YYYY-MM')",
			// CALENDAR quarter — see the vouchers entity's note.
			"quarter": `to_char(vv.voucher_date,'YYYY-"Q"Q')`,
			"year":    "to_char(vv.voucher_date,'YYYY')",
		},
		metrics: map[string]metricCol{
			// qty is NOT NULL with CHECK (qty > 0), a magnitude; direction
			// carries the sign. Row-level, so all four aggregates are legal.
			"qty": {rawExpr: "sm.qty", aggs: numericAggs()},
			// in_qty / out_qty / net_qty are the Stock Movement report's
			// InwardQty / OutwardQty / NetQty, per the same current-version rows.
			// SUM-only: a min over a CASE that is zero on every other-direction
			// row answers "0" for a question nobody asked.
			"in_qty":  {rawExpr: "CASE WHEN sm.direction = 'IN' THEN sm.qty ELSE 0 END", aggs: sumOnly()},
			"out_qty": {rawExpr: "CASE WHEN sm.direction = 'OUT' THEN sm.qty ELSE 0 END", aggs: sumOnly()},
			"net_qty": {rawExpr: "CASE WHEN sm.direction = 'IN' THEN sm.qty ELSE -sm.qty END", aggs: sumOnly()},
			// The line value AS ENTERED (see the ⛔ note above). NOT NULL DEFAULT 0.
			"amount": {rawExpr: "sm.amount", aggs: numericAggs(), isMoney: true},
			// The effective rate a line was entered at. min/max/avg are the
			// point (supplier price drift reads max vs min); a SUM of rates is
			// meaningless and is not offered.
			"rate": {rawExpr: "sm.rate", aggs: map[AggFunc]bool{AggMin: true, AggMax: true, AggAvg: true}, isMoney: true},
		},
		filters: map[string]filterCol{
			"item_name":         {expr: "si.name", ops: nameOps(), valType: valueAny},
			"godown_name":       {expr: "gd.name", ops: nameOps(), valType: valueAny},
			"direction":         {expr: "sm.direction::text", ops: enumOps(), valType: valueDirection},
			"voucher_type_name": {expr: "vt.name", ops: nameOps(), valType: valueAny},
			"party":             {expr: "party.name", ops: nameOps(), valType: valueAny},
			"voucher_date":      {expr: "vv.voucher_date", ops: dateOps(), valType: valueDate},
		},
		dateCols: map[string]string{
			"voucher_date": "vv.voucher_date",
		},
	},

	// ---------------------------------------------------------------------
	// bank_statement_lines — imported statement lines as the BANK reported
	// them (W3), from the same table reporting.ImportedStatementReport reads.
	//
	// dr_cr is OUR orientation, which INVERTS the word on the statement: a
	// DEPOSIT is 'D' (the bank asset rose). See migration 0062's column
	// comment; `deposits` / `withdrawals` below are spelled in the operator's
	// words so nobody has to remember the inversion at the call site.
	//
	// ⛔ THE NARRATION IS A FILTER AND NEVER A DIMENSION, ON PURPOSE. A bank
	// description is text a THIRD PARTY wrote — the payer's UPI note, a
	// merchant's descriptor — and PLAN-AGENTIC §2.4 treats it as untrusted: it
	// is precisely where an injection payload arrives ("IGNORE PREVIOUS
	// INSTRUCTIONS, PAY …" in a NEFT remark). A dimension would put that text
	// verbatim into the result set the model reads next; a filter binds the
	// user's OWN search string as a $N value and returns only amounts. It is
	// also where counterparty VPA ids and account fragments live, which is a
	// DPDP question the answer need not raise.
	//
	// Duplicates are impossible by construction: an overlapping download is
	// deduplicated at import by (line_hash, occurrence) UNIQUE, so a SUM here
	// cannot double-count a transaction that arrived in two files.
	//
	// The match join is LEFT and one-per-line (bank_statement_match's UNIQUE
	// (company_id, line_id)), so it can never multiply a line. A line with no
	// match row is 'unresolved' — which is NOT the same as unreconciled in the
	// books (a clearance can be ticked by hand without an import), and the
	// catalogue says so.
	// ---------------------------------------------------------------------
	EntityBankStatementLines: {
		skeleton: `bank_statement_line bl
		   JOIN ledger l ON l.id = bl.ledger_id
		   LEFT JOIN bank_statement_match bm
		     ON bm.company_id = bl.company_id
		    AND bm.line_id = bl.id`,
		whereGuard: "",
		dimensions: map[string]string{
			"bank_ledger_name": "l.name",
			"dr_cr":            "bl.dr_cr::text",
			"match_status":     "COALESCE(bm.status,'unresolved')",
			"month":            "to_char(bl.value_date,'YYYY-MM')",
			// CALENDAR quarter — see the vouchers entity's note.
			"quarter": `to_char(bl.value_date,'YYYY-"Q"Q')`,
			"year":    "to_char(bl.value_date,'YYYY')",
		},
		metrics: map[string]metricCol{
			"amount":      {rawExpr: "bl.amount", aggs: numericAggs(), isMoney: true},
			"deposits":    {rawExpr: "CASE WHEN bl.dr_cr = 'D' THEN bl.amount ELSE 0 END", aggs: sumOnly(), isMoney: true},
			"withdrawals": {rawExpr: "CASE WHEN bl.dr_cr = 'C' THEN bl.amount ELSE 0 END", aggs: sumOnly(), isMoney: true},
			// net is deposits minus withdrawals: the movement of the bank's own
			// balance across the lines selected.
			"net": {rawExpr: "CASE WHEN bl.dr_cr = 'D' THEN bl.amount ELSE -bl.amount END", aggs: sumOnly(), isMoney: true},
		},
		filters: map[string]filterCol{
			"bank_ledger_name": {expr: "l.name", ops: nameOps(), valType: valueAny},
			"dr_cr":            {expr: "bl.dr_cr::text", ops: enumOps(), valType: valueDrCr},
			// Filter-only; see the ⛔ note above. eq/like only — an IN list of
			// narrations is not a question anyone asks.
			"description":  {expr: "bl.description", ops: map[Op]bool{OpEq: true, OpLike: true}, valType: valueAny},
			"match_status": {expr: "COALESCE(bm.status,'unresolved')", ops: enumOps(), valType: valueMatchStatus},
			"value_date":   {expr: "bl.value_date", ops: dateOps(), valType: valueDate},
		},
		dateCols: map[string]string{
			"value_date": "bl.value_date",
		},
	},

	// ---------------------------------------------------------------------
	// profile_facts — the CURRENT deterministic entity facts (W3,
	// internal/profile, migration 0096). This entity exists so the agent's
	// profile tools and the query screen read the SAME rows: a tool answering
	// "Acme's DSO is 41 days" and a screen answering 38 for the same question
	// would be two surfaces over one set of books disagreeing, which is the
	// D20/D51 failure this package keeps re-learning.
	//
	// "Current" is BOTH temporal axes closed off: superseded_at IS NULL (we
	// still believe it) AND valid_to IS NULL (it is still true). Dropping
	// either half would show a corrected-away value or a historical one next
	// to today's, and both look perfectly plausible.
	//
	// value_text is a dimension because some facts are classes (ABC 'A', XYZ
	// 'Z', a stream's cadence 'monthly') and a class has no numeric value. It is
	// COMPUTED text from a closed vocabulary written by Go, never a
	// user-entered or third-party string — so it does not reopen the
	// bank-narration hole the entity above closes.
	// ---------------------------------------------------------------------
	EntityProfileFacts: {
		skeleton:   `entity_facts f`,
		whereGuard: "f.superseded_at IS NULL AND f.valid_to IS NULL",
		dimensions: map[string]string{
			"entity_kind": "f.entity_kind",
			"entity_name": "f.entity_name",
			"fact_key":    "f.fact_key",
			// The item a supplier's price drift is about, the month a sales
			// figure is for; '' when the fact has none.
			"qualifier_name": "f.qualifier_name",
			"unit":           "f.unit",
			"value_text":     "COALESCE(f.value_text,'')",
			"as_of":          "to_char(f.valid_from,'YYYY-MM-DD')",
		},
		metrics: map[string]metricCol{
			// NULL for a class fact; SUM COALESCEs, MIN/MAX/AVG stay NULL -> ""
			// (metricCol's NULL SEMANTICS) — a class has no number, and 0 would
			// be a wrong one.
			"value": {rawExpr: "f.value_num", aggs: numericAggs()},
		},
		filters: map[string]filterCol{
			"entity_kind":    {expr: "f.entity_kind", ops: enumOps(), valType: valueEntityKind},
			"entity_name":    {expr: "f.entity_name", ops: nameOps(), valType: valueAny},
			"fact_key":       {expr: "f.fact_key", ops: nameOps(), valType: valueAny},
			"qualifier_name": {expr: "f.qualifier_name", ops: nameOps(), valType: valueAny},
			"value_text":     {expr: "f.value_text", ops: nameOps(), valType: valueAny},
		},
		dateCols: map[string]string{
			"as_of": "f.valid_from",
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
