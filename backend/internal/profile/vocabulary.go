// Package profile is W3's "the agent knows the business" layer: deterministic
// feature jobs over the books that write bi-temporal entity_facts /
// entity_edges (migration 0096), the ≤1.5k-token tenant card, and the
// profile TOOLS the agent calls (get_customer_profile & co, each ≤1k tokens,
// each with an as_of, each readable through the query DSL's profile_facts
// entity so the screen and the agent show one number).
//
// ⛔ TWO RULES DECIDE EVERYTHING IN THIS PACKAGE.
//
//  1. A MONEY FACT THAT A REPORT PRINTS IS TAKEN FROM THAT REPORT, NEVER
//     RE-DERIVED. A party's receivable is whatever reporting.Outstandings
//     says it is; a month's sales is whatever the Profit & Loss says. This
//     repo has re-learned D20 and D51 twice — a second SQL derivation of a
//     report figure agrees with the report until the day it does not — so a
//     job that needs a report figure CALLS THE REPORT. What the jobs compute
//     themselves is only what no report prints: lags, percentiles, classes,
//     velocities, streams. The reconciliation test re-runs every report and
//     compares, and — where one exists — also checks against an INDEPENDENT
//     SQL path (the query DSL), so "taken from the report" cannot hide a
//     report that is itself wrong in the same way.
//
//  2. NOTHING READS A FACT TO MOVE MONEY (PLAN-AGENTIC §2.2). A fact is a
//     cache, stale the moment a voucher posts. A narrative may quote it; an
//     action must re-read the books.
//
// The vocabulary below is CLOSED: a job may only write a fact_key declared
// here, and each declaration says its unit and either the report it
// reconciles to or that it is a statistic no report prints. vocabulary_test
// fails on a money fact with neither — which is how a new job is stopped from
// shipping a rupee figure nobody can check.
package profile

// Kind is entity_facts.entity_kind.
type Kind string

const (
	KindParty  Kind = "party"
	KindItem   Kind = "item"
	KindTenant Kind = "tenant"
	KindStream Kind = "stream"
)

// Unit is entity_facts.unit (the migration's CHECK set, exactly).
type Unit string

const (
	UnitINR       Unit = "inr"
	UnitDays      Unit = "days"
	UnitQty       Unit = "qty"
	UnitQtyPerDay Unit = "qty_per_day"
	UnitPct       Unit = "pct"
	UnitCount     Unit = "count"
	UnitRatio     Unit = "ratio"
	UnitClass     Unit = "class"
)

// FactDef declares one fact key.
type FactDef struct {
	Key  string
	Kind Kind
	Unit Unit
	// Label is the human phrase the tools and the tenant card render.
	Label string
	// ReconcilesTo is the authz report name the figure must equal, or "".
	ReconcilesTo string
	// Statistic is true for a figure no report prints (a percentile, a
	// class, a velocity). Exactly one of ReconcilesTo / Statistic is set.
	Statistic bool
	// Job is the job that writes it; tools filter by job, so a party that is
	// both a customer and a supplier shows the right half to each tool.
	Job string
}

// Job names. Versioned: bump the version when a job's arithmetic changes, so
// a fact computed under the old rule is distinguishable after the fact.
const (
	JobCustomer = "party.customer"
	JobSupplier = "party.supplier"
	JobItem     = "item.velocity"
	JobStream   = "stream.recurring"
	JobTenant   = "tenant.runrate"

	jobVersion = "w3-v1"
)

// Facts is the closed vocabulary.
var Facts = []FactDef{
	// --- customers (Sundry Debtors subtree) --------------------------------
	{Key: "receivable_outstanding", Kind: KindParty, Unit: UnitINR, Label: "owes us (bill-wise)", ReconcilesTo: "outstandings", Job: JobCustomer},
	{Key: "credit_sales_window", Kind: KindParty, Unit: UnitINR, Label: "invoiced in the window", ReconcilesTo: "day-book", Job: JobCustomer},
	{Key: "dso_days", Kind: KindParty, Unit: UnitDays, Label: "days sales outstanding", Statistic: true, Job: JobCustomer},
	{Key: "receipt_lag_p50_days", Kind: KindParty, Unit: UnitDays, Label: "median days to pay a bill", Statistic: true, Job: JobCustomer},
	{Key: "receipt_lag_p90_days", Kind: KindParty, Unit: UnitDays, Label: "90th-percentile days to pay", Statistic: true, Job: JobCustomer},
	{Key: "receipt_lag_mean_days", Kind: KindParty, Unit: UnitDays, Label: "mean days to pay", Statistic: true, Job: JobCustomer},
	{Key: "bills_settled_count", Kind: KindParty, Unit: UnitCount, Label: "bills fully paid in the window", Statistic: true, Job: JobCustomer},

	// --- suppliers (Sundry Creditors subtree) ------------------------------
	{Key: "payable_outstanding", Kind: KindParty, Unit: UnitINR, Label: "we owe them (bill-wise)", ReconcilesTo: "bills-payable", Job: JobSupplier},
	{Key: "purchases_window", Kind: KindParty, Unit: UnitINR, Label: "billed to us in the window", ReconcilesTo: "day-book", Job: JobSupplier},
	{Key: "payment_lag_p50_days", Kind: KindParty, Unit: UnitDays, Label: "median days we take to pay", Statistic: true, Job: JobSupplier},
	{Key: "payment_lag_p90_days", Kind: KindParty, Unit: UnitDays, Label: "90th-percentile days we take to pay", Statistic: true, Job: JobSupplier},
	// Qualified by ITEM.
	{Key: "item_purchase_amount", Kind: KindParty, Unit: UnitINR, Label: "bought of this item (as entered)", ReconcilesTo: "stock-item-vouchers", Job: JobSupplier},
	{Key: "item_first_rate", Kind: KindParty, Unit: UnitINR, Label: "first rate in the window", Statistic: true, Job: JobSupplier},
	{Key: "item_last_rate", Kind: KindParty, Unit: UnitINR, Label: "latest rate", Statistic: true, Job: JobSupplier},
	{Key: "item_price_drift_pct", Kind: KindParty, Unit: UnitPct, Label: "rate change first to latest", Statistic: true, Job: JobSupplier},

	// --- items -------------------------------------------------------------
	{Key: "closing_qty", Kind: KindItem, Unit: UnitQty, Label: "on hand", ReconcilesTo: "stock-summary", Job: JobItem},
	{Key: "closing_value", Kind: KindItem, Unit: UnitINR, Label: "on-hand value (valuer)", ReconcilesTo: "stock-summary", Job: JobItem},
	{Key: "out_qty_window", Kind: KindItem, Unit: UnitQty, Label: "issued in the window", ReconcilesTo: "stock-movement", Job: JobItem},
	{Key: "in_qty_window", Kind: KindItem, Unit: UnitQty, Label: "received in the window", ReconcilesTo: "stock-movement", Job: JobItem},
	{Key: "out_amount_window", Kind: KindItem, Unit: UnitINR, Label: "issued in the window at entered value (a sale's selling price)", ReconcilesTo: "stock-item-vouchers", Job: JobItem},
	{Key: "velocity_qty_per_day", Kind: KindItem, Unit: UnitQtyPerDay, Label: "issued per day", Statistic: true, Job: JobItem},
	{Key: "days_of_cover", Kind: KindItem, Unit: UnitDays, Label: "days of cover at that rate", Statistic: true, Job: JobItem},
	{Key: "abc_class", Kind: KindItem, Unit: UnitClass, Label: "ABC class (sales value)", Statistic: true, Job: JobItem},
	{Key: "xyz_class", Kind: KindItem, Unit: UnitClass, Label: "XYZ class (demand variability)", Statistic: true, Job: JobItem},
	{Key: "monthly_out_cv", Kind: KindItem, Unit: UnitRatio, Label: "coefficient of variation of monthly issues", Statistic: true, Job: JobItem},

	// --- recurring streams -------------------------------------------------
	{Key: "stream_total_window", Kind: KindStream, Unit: UnitINR, Label: "paid on this stream in the window", ReconcilesTo: "day-book", Job: JobStream},
	{Key: "stream_median_amount", Kind: KindStream, Unit: UnitINR, Label: "typical amount", Statistic: true, Job: JobStream},
	{Key: "stream_interval_days", Kind: KindStream, Unit: UnitDays, Label: "typical interval", Statistic: true, Job: JobStream},
	{Key: "stream_occurrences", Kind: KindStream, Unit: UnitCount, Label: "occurrences", Statistic: true, Job: JobStream},
	{Key: "stream_cadence", Kind: KindStream, Unit: UnitClass, Label: "cadence", Statistic: true, Job: JobStream},
	{Key: "stream_next_due_in_days", Kind: KindStream, Unit: UnitDays, Label: "next expected in (days; negative = late)", Statistic: true, Job: JobStream},

	// --- tenant ------------------------------------------------------------
	// Qualified by MONTH (YYYY-MM).
	{Key: "sales_month", Kind: KindTenant, Unit: UnitINR, Label: "sales", ReconcilesTo: "profit-and-loss", Job: JobTenant},
	{Key: "purchases_month", Kind: KindTenant, Unit: UnitINR, Label: "purchases", ReconcilesTo: "profit-and-loss", Job: JobTenant},
	{Key: "sales_run_rate_3m", Kind: KindTenant, Unit: UnitINR, Label: "sales run-rate (mean of last 3 months)", Statistic: true, Job: JobTenant},
	{Key: "sales_run_rate_12m", Kind: KindTenant, Unit: UnitINR, Label: "sales run-rate (mean of last 12 months)", Statistic: true, Job: JobTenant},
	// Qualified by calendar month (MM); only with >= 12 months of history.
	{Key: "sales_seasonality_index", Kind: KindTenant, Unit: UnitRatio, Label: "seasonality index (month / mean month)", Statistic: true, Job: JobTenant},
	{Key: "receivables_total", Kind: KindTenant, Unit: UnitINR, Label: "total receivables", ReconcilesTo: "outstandings", Job: JobTenant},
	{Key: "payables_total", Kind: KindTenant, Unit: UnitINR, Label: "total payables", ReconcilesTo: "bills-payable", Job: JobTenant},
	{Key: "vouchers_window", Kind: KindTenant, Unit: UnitCount, Label: "vouchers entered in the window", ReconcilesTo: "day-book", Job: JobTenant},
}

var factByKey = func() map[string]FactDef {
	m := make(map[string]FactDef, len(Facts))
	for _, d := range Facts {
		m[d.Key] = d
	}
	return m
}()

// Def returns the declaration of a fact key.
func Def(key string) (FactDef, bool) {
	d, ok := factByKey[key]
	return d, ok
}
