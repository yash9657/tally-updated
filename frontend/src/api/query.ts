/**
 * The "Ask your books" conversational-query contract — the TS mirror of the
 * backend's CLOSED typed DSL (WS1 `internal/query/dsl.go`) and the POST /query
 * request/response envelope (WS2 `internal/server` handler).
 *
 * SAFETY MODEL (mirrored, not enforced here): user English never becomes SQL.
 * The pipeline is NL -> this typed {@link Query} DSL (whitelist-validated by the
 * server) -> a deterministic compiler emitting a FIXED skeleton with ONLY
 * whitelisted identifiers and ALL values as $N params -> a READ-ONLY,
 * company-GUC-scoped transaction (RLS scopes the tenant). The frontend only
 * SENDS an NL string (or a pre-built DSL for power users) and RENDERS the typed
 * answer verbatim; it never builds SQL and never parses money.
 *
 * Field names mirror the Go struct JSON tags FIELD-FOR-FIELD (snake_case where
 * the wire is snake_case): entity, metrics ([{func,column,as}]), dimensions
 * ([]string), filters ([{column,op,values}]), date_range ({column,from,to} or
 * null), order_by ([{column,desc}]), limit (int; server-clamped to [1,1000],
 * default 100 when 0). Money/dates stay STRINGS end to end.
 */

/** The fixed set of queryable entities. The server rejects any other value. */
export type QueryEntity =
  | "ledger_balances"
  | "vouchers"
  | "postings"
  | "outstandings"
  | "bill_allocations"
  // W3 (backend internal/query/whitelist.go). stock_movements' `amount` is the
  // line value AS ENTERED, never the valued cost of goods issued; the
  // bank_statement_lines narration is a filter only and never a column.
  | "stock_movements"
  | "bank_statement_lines"
  | "profile_facts";

/** Supported aggregate functions for a {@link QueryMetric}. */
export type AggFunc = "sum" | "count" | "min" | "max" | "avg";

/** Comparison operators for a {@link QueryFilter}. */
export type FilterOp =
  | "eq"
  | "ne"
  | "gt"
  | "ge"
  | "lt"
  | "le"
  | "like"
  | "in";

/**
 * An aggregate over a whitelisted numeric column. `column` must be in the
 * entity's numeric whitelist (or "*" for count). `as` is the result column
 * alias surfaced in {@link QueryResult.columns}.
 */
export interface QueryMetric {
  func: AggFunc;
  column: string;
  as: string;
}

/**
 * A filter on a whitelisted column. `values` are ALWAYS bound as $N parameters
 * server-side (never concatenated into SQL); an `in` op requires >= 1 value.
 */
export interface QueryFilter {
  column: string;
  op: FilterOp;
  values: string[];
}

/**
 * An inclusive date window on a whitelisted date column. `from`/`to` are ISO
 * dates (YYYY-MM-DD) or "" to leave that bound open.
 */
export interface DateRange {
  column: string;
  from: string;
  to: string;
}

/**
 * One ORDER BY term. `column` must be a SELECTED dimension or a metric `as`
 * alias (the server rejects ordering by an unselected column).
 */
export interface OrderTerm {
  column: string;
  desc: boolean;
}

/**
 * The CLOSED typed query DSL. This is the exact JSON shape the server returns
 * (echoed back as `dsl`) and the shape a power user may POST directly.
 */
export interface Query {
  entity: QueryEntity;
  metrics: QueryMetric[];
  dimensions: string[];
  filters: QueryFilter[];
  date_range: DateRange | null;
  order_by: OrderTerm[];
  limit: number;
}

/**
 * The POST /query request envelope. Provide `nl` (English question, the default
 * path) OR a pre-built `dsl` (power users). When both are present the server
 * prefers `dsl`.
 */
export interface QueryRequest {
  nl?: string;
  dsl?: Query;
}

/** Metadata for one result column: its name and SQL-ish type tag. */
export interface ColumnMeta {
  name: string;
  type: string;
}

/**
 * The POST /query 200 response. `rows` are arrays of STRINGS positionally
 * aligned with `columns` (NUMERIC via StringFixed(4), dates as YYYY-MM-DD, NULL
 * as ""). `sql_preview` is the compiled skeleton with $N PLACEHOLDERS ONLY
 * (never interpolated user values) — surfaced for transparency, safe because it
 * is the deterministic identifier-whitelisted skeleton, not raw input.
 */
export interface QueryResult {
	limit?: number;
	has_more?: boolean;
  dsl: Query;
  sql_preview: string;
  columns: ColumnMeta[];
  rows: string[][];
}
