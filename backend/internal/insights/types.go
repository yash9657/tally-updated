// Package insights is W4's insight engine (PLAN-AGENTIC §3 W4, RESEARCH-AGENTIC
// D8): deterministic detectors over the books and W3's facts, an insights table
// whose every row can prove its own numbers, a ranker, and the selection half of
// the morning digest. The narration half lives in internal/insights/narrate,
// because it is the only half that may reach a model.
//
// ⛔ THREE RULES DECIDE EVERYTHING IN THIS PACKAGE.
//
//  1. A DETECTOR IS GO, AND ITS NUMBERS ARE PAISA-EXACT. No model is consulted
//     to decide whether something is an insight or how much it is worth. The
//     model-backed detectors the plan names (LightGBM + SHAP for P(late),
//     survival for pay dates, BG/NBD, ALS, Chronos) are OWED (Owed below) and
//     are not imitated by a heuristic wearing their name.
//
//  2. AN INSIGHT THAT CANNOT REPRODUCE ITS NUMBERS IS NEVER STORED. Every
//     candidate carries evidence_sql — a constant in this package, one per
//     type, never model text — which is RE-RUN before the row is written and
//     must return every evidence key of the payload equal to the paisa, and
//     exactly the voucher_ids the row names. Where the detector's own figure
//     came from a report (forecast.CashFlow, reporting.Outstandings) the
//     evidence SQL is a SECOND, independent derivation from the base tables,
//     so a disagreement between the two is caught at write time and counted in
//     the run's stats (Stats.EvidenceRefused) instead of shown to an owner.
//
//  3. NOTHING HERE MOVES MONEY. An insight names an action a human can take in
//     the product; it never takes one, and no write path may read a figure
//     from the insight table (the entity_facts rule, one table along).
package insights

import (
	"sort"

	"github.com/shopspring/decimal"
)

// Unit says how a payload value is written, compared and narrated.
type Unit string

const (
	UnitINR   Unit = "inr"   // money, 2 decimals ("12500.00"), 4 when it carries them (a rate)
	UnitPct   Unit = "pct"   // a percentage, fixed 2 decimals
	UnitQty   Unit = "qty"   // a stock quantity, up to 4 decimals, trailing zeros trimmed
	UnitDays  Unit = "days"  // whole days
	UnitCount Unit = "count" // a whole count
	UnitDate  Unit = "date"  // ISO YYYY-MM-DD
	UnitText  Unit = "text"  // a name or a number-as-label (a voucher number)
)

// Evidenced reports whether values of this unit are re-derived by the evidence
// SQL. Text is not: a party name is an identity, not a figure, and the
// evidence SQL keys on the entity id instead.
func (u Unit) Evidenced() bool { return u != UnitText }

// Field declares one payload key.
type Field struct {
	Key   string
	Unit  Unit
	Label string
}

// Cadence is when a detector runs.
type Cadence string

const (
	RealTime Cadence = "realtime"
	Nightly  Cadence = "nightly"
)

// TypeDef is one insight type. The registry below is CLOSED: a detector may
// emit only a declared type, with exactly the declared fields
// (TestEveryTypeIsWellFormed), and the digest may narrate only declared keys.
type TypeDef struct {
	Code    string
	Title   string
	Cadence Cadence
	// EntityKind is insight.entity_kind (the migration's CHECK set).
	EntityKind string
	// Actionability ∈ (0,1]: 1.0 when the product has the screen that acts on
	// it today (post a payment, raise an e-way bill, open the voucher); lower
	// when the action is a phone call or a negotiation outside the product.
	// It is a DECLARED constant, and TypeDef says so rather than dressing it as
	// a learned weight.
	Actionability decimal.Decimal
	// Action is the one line the inbox shows under the item.
	Action string
	// PriorReal / PriorFalse are the Beta prior on "this type's insights are
	// real" (p_real) before any feedback. ⛔ THEY ARE THIS BUILD'S JUDGEMENT,
	// NOT A MEASUREMENT: a statutory rule (TDS unpaid past the 7th) is as real
	// as the arithmetic, a heuristic (a customer "overdue" for a reorder) is
	// not. Feedback moves them (rank.go); the ADR lists them as priors.
	PriorReal, PriorFalse float64
	// WeeklyCap is the most digest appearances the type may have in 7 days.
	WeeklyCap int
	// Fields are the payload keys, in narration order.
	Fields []Field
	// Template is the deterministic narration: {{key}} placeholders only.
	// It is both the fallback when no model is configured and the reference
	// the provenance check is run against in tests.
	Template string
}

// Minimum p_real for a type to appear in a DIGEST (the inbox shows every open
// insight). PLAN-AGENTIC W4: "false-positive rate per type below 30% before it
// may appear in a digest".
var DigestMinPReal = 0.70

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// The type codes.
const (
	TypeCreditLimitBreach  = "credit_limit_breach"
	TypeDuplicateBill      = "duplicate_bill"
	TypePlaceOfSupply      = "gst_place_of_supply_mismatch"
	TypeHSNRate            = "hsn_rate_mismatch"
	TypeEWayBillNeeded     = "eway_bill_needed"
	TypeCustomerSlower     = "customer_slower_than_norm"
	TypeSupplierNotIn2B    = "supplier_not_in_gstr2b"
	TypeSupplierPriceCreep = "supplier_price_creep"
	TypeCustomerReorderDue = "customer_reorder_overdue"
	TypeReorderNow         = "stock_reorder_now"
	TypeDeadStock          = "dead_stock"
	TypeCashShortfall      = "cash_shortfall_13w"
	TypeGSTLiability       = "gst_liability_estimate"
	TypeTDSNotDeposited    = "tds_not_deposited"
	TypeBackdatedEdits     = "backdated_edits"
)

// Types is the registry, in no particular order (ByCode indexes it).
var Types = []TypeDef{
	// ---- real-time, on voucher commit --------------------------------------
	{Code: TypeCreditLimitBreach, Title: "Credit limit crossed", Cadence: RealTime, EntityKind: "party",
		Actionability: d("1"), Action: "Open the party's ledger; hold further credit sales or raise the limit.",
		PriorReal: 19, PriorFalse: 1, WeeklyCap: 3,
		Fields: []Field{
			{"party", UnitText, "party"}, {"voucher_no", UnitText, "voucher that crossed it"},
			{"credit_limit", UnitINR, "credit limit"}, {"balance", UnitINR, "balance now owed"},
			{"excess", UnitINR, "amount over the limit"},
		},
		Template: "{{party}} now owes Rs {{balance}} against a credit limit of Rs {{credit_limit}} — Rs {{excess}} over, after voucher {{voucher_no}}."},
	{Code: TypeDuplicateBill, Title: "Possible duplicate purchase bill", Cadence: RealTime, EntityKind: "voucher",
		Actionability: d("1"), Action: "Open both vouchers; delete one before it is paid twice.",
		PriorReal: 8, PriorFalse: 2, WeeklyCap: 5,
		Fields: []Field{
			{"party", UnitText, "supplier"}, {"supplier_invoice_no", UnitText, "supplier's bill number"},
			{"voucher_no", UnitText, "voucher just entered"}, {"amount", UnitINR, "bill amount"},
			{"bills_with_same_number", UnitCount, "bills carrying that number"},
		},
		Template: "Bill {{supplier_invoice_no}} from {{party}} (Rs {{amount}}) is entered {{bills_with_same_number}} times — voucher {{voucher_no}} may be a duplicate."},
	{Code: TypePlaceOfSupply, Title: "Place of supply differs from the customer's GSTIN state", Cadence: RealTime, EntityKind: "voucher",
		Actionability: d("1"), Action: "Open the invoice and confirm the place of supply before it reaches GSTR-1.",
		PriorReal: 7, PriorFalse: 3, WeeklyCap: 5,
		Fields: []Field{
			{"party", UnitText, "customer"}, {"voucher_no", UnitText, "invoice"},
			{"place_of_supply", UnitText, "place of supply on the invoice"}, {"gstin_state", UnitText, "state in the customer's GSTIN"},
			{"tax", UnitINR, "GST charged"},
		},
		Template: "Invoice {{voucher_no}} to {{party}} names place of supply {{place_of_supply}}, but their GSTIN is registered in state {{gstin_state}}; Rs {{tax}} of GST rides on that choice."},
	{Code: TypeHSNRate, Title: "GST rate differs from the item master for this HSN", Cadence: RealTime, EntityKind: "voucher",
		Actionability: d("1"), Action: "Open the voucher and check the rate on that line.",
		PriorReal: 7, PriorFalse: 3, WeeklyCap: 5,
		Fields: []Field{
			{"voucher_no", UnitText, "voucher"}, {"hsn", UnitText, "HSN"},
			{"line_rate", UnitPct, "rate on the line"}, {"master_rate", UnitPct, "rate on the item master"},
			{"taxable", UnitINR, "taxable value"}, {"tax_difference", UnitINR, "tax difference"},
		},
		Template: "Voucher {{voucher_no}} charges {{line_rate}}% on HSN {{hsn}}, where the item master says {{master_rate}}%: Rs {{tax_difference}} of tax on Rs {{taxable}}."},
	{Code: TypeEWayBillNeeded, Title: "E-way bill needed", Cadence: RealTime, EntityKind: "voucher",
		Actionability: d("1"), Action: "Raise the e-way bill from the invoice (Alt+G → E-Way Bill) before the goods move.",
		PriorReal: 9, PriorFalse: 1, WeeklyCap: 5,
		Fields: []Field{
			{"party", UnitText, "customer"}, {"voucher_no", UnitText, "invoice"},
			{"invoice_value", UnitINR, "invoice value"}, {"threshold", UnitINR, "Rule 138 threshold"},
			{"penalty_floor", UnitINR, "minimum penalty if the goods move without one"},
		},
		Template: "Invoice {{voucher_no}} to {{party}} is Rs {{invoice_value}}, at or above the Rs {{threshold}} threshold, and has no e-way bill yet; moving the goods without it risks a penalty of at least Rs {{penalty_floor}}."},

	// ---- nightly -----------------------------------------------------------
	{Code: TypeCustomerSlower, Title: "Customer paying slower than their own norm", Cadence: Nightly, EntityKind: "party",
		Actionability: d("0.8"), Action: "Call the customer; the bills are listed under the insight.",
		PriorReal: 8, PriorFalse: 2, WeeklyCap: 3,
		Fields: []Field{
			{"party", UnitText, "customer"}, {"p90_days", UnitDays, "their 90th-percentile days to pay"},
			{"overdue_bills", UnitCount, "bills older than that"}, {"oldest_days", UnitDays, "age of the oldest"},
			{"overdue_amount", UnitINR, "pending on those bills"},
		},
		Template: "{{party}} usually pays within {{p90_days}} days, but {{overdue_bills}} bills worth Rs {{overdue_amount}} are older than that; the oldest is {{oldest_days}} days old."},
	{Code: TypeSupplierNotIn2B, Title: "Supplier's invoices missing from GSTR-2B (ITC at risk)", Cadence: Nightly, EntityKind: "party",
		Actionability: d("0.8"), Action: "Ask the supplier to file GSTR-1; hold their payment until they do.",
		PriorReal: 9, PriorFalse: 1, WeeklyCap: 3,
		Fields: []Field{
			{"party", UnitText, "supplier"}, {"gstin", UnitText, "their GSTIN"},
			{"return_period", UnitText, "2B period (MMYYYY)"}, {"invoices", UnitCount, "invoices in your books"},
			{"itc_at_risk", UnitINR, "ITC on them"},
		},
		Template: "{{party}} ({{gstin}}) appears nowhere in your GSTR-2B for {{return_period}}, yet you booked {{invoices}} invoices from them: Rs {{itc_at_risk}} of ITC is at risk."},
	{Code: TypeSupplierPriceCreep, Title: "Supplier price creep", Cadence: Nightly, EntityKind: "party",
		Actionability: d("0.6"), Action: "Renegotiate, or compare with another supplier of the item.",
		PriorReal: 8, PriorFalse: 2, WeeklyCap: 2,
		Fields: []Field{
			{"party", UnitText, "supplier"}, {"item", UnitText, "item"},
			{"first_rate", UnitINR, "first rate in the year"}, {"last_rate", UnitINR, "latest rate"},
			{"drift_pct", UnitPct, "change"}, {"qty_window", UnitQty, "quantity bought in the year"},
			{"extra_cost", UnitINR, "extra cost of the year's volume at the latest rate"},
		},
		Template: "{{party}} has raised {{item}} from Rs {{first_rate}} to Rs {{last_rate}} ({{drift_pct}}%); at the year's volume of {{qty_window}} that is Rs {{extra_cost}} more."},
	{Code: TypeCustomerReorderDue, Title: "Customer overdue for their usual reorder", Cadence: Nightly, EntityKind: "party",
		Actionability: d("0.6"), Action: "Call or message the customer with their usual order.",
		PriorReal: 7, PriorFalse: 3, WeeklyCap: 3,
		Fields: []Field{
			{"party", UnitText, "customer"}, {"median_gap_days", UnitDays, "usual days between orders"},
			{"days_since_last", UnitDays, "days since the last order"}, {"last_invoice_date", UnitDate, "last order"},
			{"invoices", UnitCount, "orders in the year"}, {"typical_invoice", UnitINR, "typical order value"},
		},
		Template: "{{party}} orders every {{median_gap_days}} days or so ({{invoices}} orders this year), but has not ordered for {{days_since_last}} days, since {{last_invoice_date}}; a typical order is Rs {{typical_invoice}}."},
	{Code: TypeReorderNow, Title: "Reorder now — stock runs out within the horizon", Cadence: Nightly, EntityKind: "item",
		Actionability: d("1"), Action: "Raise a purchase order for the item.",
		PriorReal: 8, PriorFalse: 2, WeeklyCap: 5,
		Fields: []Field{
			{"item", UnitText, "item"}, {"closing_qty", UnitQty, "on hand"},
			{"velocity_qty_per_day", UnitQty, "sold per day"}, {"days_of_cover", UnitQty, "days of cover"},
			{"horizon_days", UnitDays, "cover wanted"}, {"shortfall_qty", UnitQty, "quantity short of that"},
			{"last_purchase_rate", UnitINR, "last purchase rate"}, {"reorder_value", UnitINR, "cost of the shortfall at that rate"},
		},
		Template: "{{item}}: {{closing_qty}} on hand at {{velocity_qty_per_day}} a day is {{days_of_cover}} days of cover, under the {{horizon_days}} wanted; {{shortfall_qty}} more at Rs {{last_purchase_rate}} is Rs {{reorder_value}}."},
	{Code: TypeDeadStock, Title: "Dead stock", Cadence: Nightly, EntityKind: "item",
		Actionability: d("0.6"), Action: "Discount, return to the supplier, or write down.",
		PriorReal: 8, PriorFalse: 2, WeeklyCap: 2,
		Fields: []Field{
			{"item", UnitText, "item"}, {"closing_qty", UnitQty, "on hand"},
			{"idle_days", UnitDays, "days since it last moved out"}, {"last_purchase_rate", UnitINR, "last purchase rate"},
			{"stock_value_at_last_rate", UnitINR, "value at that rate"},
		},
		Template: "{{item}} has not sold for {{idle_days}} days; {{closing_qty}} on hand is worth about Rs {{stock_value_at_last_rate}} at the last purchase rate of Rs {{last_purchase_rate}}."},
	{Code: TypeCashShortfall, Title: "Cash goes negative within 13 weeks", Cadence: Nightly, EntityKind: "tenant",
		Actionability: d("0.8"), Action: "Chase the receivables in the cash-flow projection, or defer payables.",
		PriorReal: 8, PriorFalse: 2, WeeklyCap: 2,
		Fields: []Field{
			{"week_from", UnitDate, "week starting"}, {"week_to", UnitDate, "week ending (exclusive)"},
			{"opening_cash", UnitINR, "cash and bank today"}, {"inflow_through", UnitINR, "expected receipts by then"},
			{"outflow_through", UnitINR, "expected payments by then"}, {"closing_cash", UnitINR, "projected cash that week"},
			{"shortfall", UnitINR, "shortfall"},
		},
		Template: "Cash of Rs {{opening_cash}} today, plus Rs {{inflow_through}} expected in and less Rs {{outflow_through}} due out, projects to Rs {{closing_cash}} in the week from {{week_from}}: a shortfall of Rs {{shortfall}}."},
	{Code: TypeGSTLiability, Title: "GST payable this month", Cadence: Nightly, EntityKind: "period",
		Actionability: d("1"), Action: "Reserve the cash; file GSTR-3B by the due date.",
		PriorReal: 9, PriorFalse: 1, WeeklyCap: 2,
		Fields: []Field{
			{"period", UnitText, "tax period (YYYY-MM)"}, {"due_date", UnitDate, "GSTR-3B due"},
			{"output_tax", UnitINR, "output tax on sales, net of credit notes"},
			{"itc_available", UnitINR, "eligible ITC on purchases, net of debit notes"},
			{"net_payable", UnitINR, "estimated cash payable"}, {"itc_carried_forward", UnitINR, "ITC left over"},
		},
		Template: "GST for {{period}}: output Rs {{output_tax}} less ITC Rs {{itc_available}} leaves Rs {{net_payable}} to pay by {{due_date}} (ITC carried forward Rs {{itc_carried_forward}})."},
	{Code: TypeTDSNotDeposited, Title: "TDS deducted but not deposited", Cadence: Nightly, EntityKind: "period",
		Actionability: d("1"), Action: "Deposit the TDS and record the challan (Alt+G → TDS Challans).",
		PriorReal: 19, PriorFalse: 1, WeeklyCap: 2,
		Fields: []Field{
			{"period", UnitText, "month deducted (YYYY-MM)"}, {"due_date", UnitDate, "deposit due"},
			{"days_late", UnitDays, "days past due"}, {"deductions", UnitCount, "deductions unpaid"},
			{"unpaid_tds", UnitINR, "TDS not yet deposited"},
		},
		Template: "Rs {{unpaid_tds}} of TDS deducted in {{period}} across {{deductions}} deductions was due by {{due_date}} and is {{days_late}} days late."},
	{Code: TypeBackdatedEdits, Title: "Back-dated entries by a user", Cadence: Nightly, EntityKind: "user",
		Actionability: d("0.6"), Action: "Review the vouchers with the user.",
		PriorReal: 7, PriorFalse: 3, WeeklyCap: 2,
		Fields: []Field{
			{"user", UnitText, "entered by"}, {"week_from", UnitDate, "from"}, {"week_to", UnitDate, "to"},
			{"entries", UnitCount, "back-dated entries or edits"}, {"vouchers", UnitCount, "vouchers touched"},
			{"oldest_voucher_date", UnitDate, "earliest date written"}, {"amount", UnitINR, "value of those entries"},
		},
		Template: "Between {{week_from}} and {{week_to}}, {{user}} made {{entries}} entries dated over a month before they were made, on {{vouchers}} vouchers worth Rs {{amount}}; the earliest is dated {{oldest_voucher_date}}."},
}

var typeByCode = func() map[string]TypeDef {
	m := make(map[string]TypeDef, len(Types))
	for _, t := range Types {
		m[t.Code] = t
	}
	return m
}()

// ByCode returns a type's declaration.
func ByCode(code string) (TypeDef, bool) { t, ok := typeByCode[code]; return t, ok }

// Field returns a field declaration of the type.
func (t TypeDef) Field(key string) (Field, bool) {
	for _, f := range t.Fields {
		if f.Key == key {
			return f, true
		}
	}
	return Field{}, false
}

// EvidenceKeys are the keys the evidence SQL must reproduce, sorted.
func (t TypeDef) EvidenceKeys() []string {
	var ks []string
	for _, f := range t.Fields {
		if f.Unit.Evidenced() {
			ks = append(ks, f.Key)
		}
	}
	sort.Strings(ks)
	return ks
}

// Owed is every insight type PLAN-AGENTIC W4 names that this build does NOT
// detect, with the reason. It is exported so the Features screen and the ADR
// can say so from one list, and TestOwedTypesAreNotBuilt keeps a type from
// being in both.
var Owed = []struct{ Type, Reason string }{
	{"predicted_late_invoice", "needs LightGBM + SHAP trained per tenant (insightd); no model plane is built"},
	{"expected_pay_date", "needs a survival model (scikit-survival) in insightd"},
	{"customer_reorder_bgnbd", "BG/NBD P(alive) needs PyMC-Marketing; customer_reorder_overdue is the simple interval rule instead, and says so"},
	{"cross_sell_item", "implicit ALS + FP-growth over line items is not built"},
	{"festival_uplift", "needs a forecast with holiday covariates (StatsForecast / Chronos-2)"},
	{"early_payment_discount", "no bill in this schema carries discount terms (2/10 net 30), so there is nothing to compute an NPV from"},
	{"vendor_bank_change", "no ledger ALTER path exists and a party ledger has no payee bank account; the design (compare a supplier's matched bank-line counterparty/VPA with its history) needs W5's matcher to use rail_reference/counterparty first"},
	{"cash_path_p10_p90", "the 13-week path is the deterministic open-bill projection (internal/forecast); p10/p90 needs AR/AP timing distributions and a Monte Carlo"},
}
