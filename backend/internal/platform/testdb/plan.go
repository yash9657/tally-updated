package testdb

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
)

// ---------------------------------------------------------------------------
// The delete plan
// ---------------------------------------------------------------------------
//
// The plan is DERIVED FROM THE LIVE SCHEMA at runtime, once per process, rather
// than hardcoded. That choice is the whole point of this file, and it is worth
// stating why: a hardcoded list of tables is correct exactly until the next
// migration, and a teardown that silently misses a table is indistinguishable
// from a teardown that works — the suite stays green while the database grows.
// That is literally how this repository accumulated 43,419 test companies and
// 603 MB of orphaned account_group rows.
//
// Enumeration alone is NOT sufficient, though, because tenancy is not always
// spelled `company_id`. The registries below carry the exceptions, and
// TestEveryTableIsClassifiedForTeardown makes their COMPLETENESS a test: every
// base table in `public` must be classified as exactly one of
//
//	(a) scoped        — it has a company_id column, enumerated dynamically
//	(b) specialTenancy — tenancy is encoded some other way; we supply the DELETE
//	(c) notTenantScoped — it genuinely does not belong to a company
//
// Add a table with a company_id and it is cleaned up automatically. Add a table
// whose tenancy is expressed some other way and that test fails, by name, on the
// day the migration lands — which is the only moment anybody still remembers how
// its tenancy works.

// scoped is a table that carries a literal company_id column.
type scoped struct {
	Table string
	// IsText is true when company_id is TEXT rather than UUID. Exactly one table
	// is like this today: insightd_draft, written by the Python insightd service,
	// which stores the company id as a string. It is the reason the enumeration
	// query below must NOT filter on data_type='uuid' — such a filter compiles,
	// passes every type check, and silently stops deleting insightd drafts.
	IsText bool
}

// specialStmt is a hand-written DELETE for a table whose tenancy is not a
// company_id column. The SQL takes the company id as $1 (always TEXT-bound,
// because these encodings are textual by nature).
type specialStmt struct {
	Table string
	SQL   string
}

// plan is the immutable, process-wide teardown plan.
type plan struct {
	// order is the scoped tables in reverse-topological FK order: a table appears
	// only after every table that REFERENCES it, so a partial failure stops at a
	// comprehensible place and the order stays correct even if replica mode is
	// ever narrowed. company is forced last, as the tenant root.
	order []scoped
	// special holds the specialTenancy deletes. They are issued BEFORE `order`.
	// That is always safe: these tables participate in no FK edge at all (checked
	// by the plan builder), so moving them earlier can never violate the
	// topological constraint.
	special []specialStmt
	// verifySQL is one UNION ALL over every table the plan touches, returning
	// (table, surviving_row_count) for the company being dropped. $1 is the
	// company id as UUID, $2 the same id as TEXT.
	verifySQL string
}

// specialTenancy maps a table with NO company_id column to the parameterised
// DELETE that removes one company's rows from it.
//
// statutory_step has no company_id, no foreign key and no RLS policy: it is the
// statutoryd plane's durable workflow log, and tenancy is encoded in its TEXT
// primary key as "irn:<companyID>:<voucherID>" — see
// backend/internal/statutory/irn_workflow.go:47. Nothing about the column list
// of that table reveals that it is tenant data, which is exactly why dynamic
// enumeration cannot find it and why this registry exists.
var specialTenancy = map[string]string{
	"statutory_step": `DELETE FROM statutory_step WHERE workflow_id LIKE 'irn:' || $1 || ':%'`,
}

// notTenantScoped maps a table that must NOT be deleted along with a company to
// the reason why. Every entry is either user-scoped (it belongs to a person, who
// outlives any one company) or global (it belongs to the database).
//
// This is a registry of DELIBERATE OMISSIONS, and each one has to be written
// down, because "no rule matched this table" and "we decided not to touch this
// table" look identical from the outside and only one of them is a bug.
var notTenantScoped = map[string]string{
	"app_user":           "a person, shared across every company they are a member of",
	"auth_login_attempt": "subject-keyed lockout audit; survives the company it was attempted against",
	"schema_migrations":  "global: the migration ledger of the database itself",
	"user_credential":    "user-scoped password/credential rows; cascade off app_user, not company",
	"webauthn_challenge": "user-scoped, short-lived ceremony state",
	"webauthn_credential": "user-scoped authenticator registration; a person keeps their key " +
		"when a company is deleted",
}

var (
	planOnce sync.Once
	planVal  *plan
	planErr  error
)

// loadPlan builds the teardown plan once per process and memoises it (including
// the error). Every teardown in the process shares it: the three catalog queries
// run exactly once no matter how many companies the suite creates.
func loadPlan(ctx context.Context) (*plan, error) {
	planOnce.Do(func() {
		planVal, planErr = buildPlan(ctx)
	})
	return planVal, planErr
}

// buildPlan issues the three catalog queries and assembles the plan.
func buildPlan(ctx context.Context) (*plan, error) {
	p, err := adminPool(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := p.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("testdb: acquire admin conn for plan: %w", err)
	}
	defer conn.Release()

	scopedTables, err := queryScopedTables(ctx, conn)
	if err != nil {
		return nil, err
	}
	edges, err := queryFKEdges(ctx, conn)
	if err != nil {
		return nil, err
	}

	// The target set is every scoped table plus every specialTenancy table.
	target := make(map[string]bool, len(scopedTables)+len(specialTenancy))
	for t := range scopedTables {
		target[t] = true
	}
	for t := range specialTenancy {
		target[t] = true
	}

	ordered, err := reverseTopo(target, edges)
	if err != nil {
		return nil, err
	}

	pl := &plan{}
	for _, t := range ordered {
		if sql, ok := specialTenancy[t]; ok {
			// A specialTenancy table must not participate in FK edges, or moving
			// it to the front of the batch (see plan.special) could break the
			// ordering guarantee. Fail loudly rather than reorder silently.
			for _, e := range edges {
				if e.child == t || e.parent == t {
					return nil, fmt.Errorf(
						"testdb: specialTenancy table %q now participates in a foreign key (%s -> %s); "+
							"it can no longer be deleted in the first wave — give it a position in the topological order",
						t, e.child, e.parent)
				}
			}
			pl.special = append(pl.special, specialStmt{Table: t, SQL: sql})
			continue
		}
		pl.order = append(pl.order, scoped{Table: t, IsText: scopedTables[t]})
	}

	pl.verifySQL = buildVerifySQL(pl)
	return pl, nil
}

// queryScopedTables returns table -> isText for every public BASE TABLE with a
// company_id column.
//
// DO NOT add `AND c.data_type = 'uuid'` to this query. It is the single most
// tempting change in this file and it is a silent data-retention bug:
// insightd_draft.company_id is TEXT, so the filter drops it from the plan, every
// test keeps passing, and insightd drafts accumulate forever. The type is
// RECORDED instead, and the binder below chooses uuid.UUID or string from it.
func queryScopedTables(ctx context.Context, conn connLike) (map[string]bool, error) {
	const q = `
SELECT c.table_name, c.data_type
  FROM information_schema.columns c
  JOIN information_schema.tables t
    ON t.table_schema = c.table_schema AND t.table_name = c.table_name
 WHERE c.table_schema = 'public'
   AND c.column_name  = 'company_id'
   AND t.table_type   = 'BASE TABLE'`
	rows, err := conn.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("testdb: enumerate company_id tables: %w", err)
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var name, dataType string
		if err := rows.Scan(&name, &dataType); err != nil {
			return nil, fmt.Errorf("testdb: scan company_id table: %w", err)
		}
		switch dataType {
		case "uuid":
			out[name] = false
		case "text", "character varying", "character":
			out[name] = true
		default:
			return nil, fmt.Errorf(
				"testdb: table %q has a company_id of unexpected type %q; teach the plan how to bind it "+
					"before it silently stops being cleaned up", name, dataType)
		}
	}
	return out, rows.Err()
}

// fkEdge is a foreign key: child references parent.
type fkEdge struct{ child, parent string }

// queryFKEdges returns every foreign key among public base tables. Self-edges
// (account_group.parent_id -> account_group) are dropped here: they are not a
// cycle, they are a hierarchy inside one table, and a single DELETE removes the
// whole hierarchy at once.
func queryFKEdges(ctx context.Context, conn connLike) ([]fkEdge, error) {
	const q = `
SELECT src.relname AS child, tgt.relname AS parent
  FROM pg_constraint k
  JOIN pg_class     src ON src.oid = k.conrelid
  JOIN pg_class     tgt ON tgt.oid = k.confrelid
  JOIN pg_namespace n   ON n.oid   = src.relnamespace
 WHERE k.contype = 'f' AND n.nspname = 'public'`
	rows, err := conn.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("testdb: enumerate foreign keys: %w", err)
	}
	defer rows.Close()

	var out []fkEdge
	seen := map[fkEdge]bool{}
	for rows.Next() {
		var e fkEdge
		if err := rows.Scan(&e.child, &e.parent); err != nil {
			return nil, fmt.Errorf("testdb: scan foreign key: %w", err)
		}
		if e.child == e.parent || seen[e] {
			continue // self-edge, or the same pair via a second constraint
		}
		seen[e] = true
		out = append(out, e)
	}
	return out, rows.Err()
}

// queryBaseTables returns the full census of public base tables. Only the
// classification test uses it; the plan itself does not need it.
func queryBaseTables(ctx context.Context, conn connLike) ([]string, error) {
	rows, err := conn.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname = 'public'`)
	if err != nil {
		return nil, fmt.Errorf("testdb: census public tables: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, fmt.Errorf("testdb: scan census row: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// tenantRoot is deleted last: every other table in the plan is conceptually a
// child of it, and it is the row whose presence a leak check counts.
const tenantRoot = "company"

// reverseTopo returns the tables of `target` in DELETE order: a table is emitted
// only after every table that references it, ties broken alphabetically so the
// order is byte-stable across runs, and `company` forced to the very end.
//
// Under `SET LOCAL session_replication_role = replica` foreign-key enforcement
// is suppressed, so with a COMPLETE target set the order is not, today, strictly
// load-bearing. It is derived anyway for three reasons: a partial failure is
// debuggable, the teardown stays correct if replica mode is ever narrowed, and
// forty lines of Kahn cannot rot the way a hand-maintained list can.
func reverseTopo(target map[string]bool, edges []fkEdge) ([]string, error) {
	// referencers[parent] = set of children still to be deleted.
	referencers := map[string]map[string]bool{}
	for t := range target {
		referencers[t] = map[string]bool{}
	}
	for _, e := range edges {
		if !target[e.child] || !target[e.parent] {
			continue // an edge leaving the target set constrains nothing here
		}
		referencers[e.parent][e.child] = true
	}

	var out []string
	remaining := len(target)
	for remaining > 0 {
		// Collect this wave: every remaining table nothing still references.
		var wave []string
		for t := range referencers {
			if len(referencers[t]) == 0 && t != tenantRoot {
				wave = append(wave, t)
			}
		}
		if len(wave) == 0 {
			break
		}
		sort.Strings(wave) // deterministic tie-break
		for _, t := range wave {
			out = append(out, t)
			delete(referencers, t)
			remaining--
			for _, cs := range referencers {
				delete(cs, t)
			}
		}
	}

	// company is forced last. Nothing in the plan is its parent (only
	// notification_outbox references it), so moving it later never violates the
	// ordering — and putting the tenant root last makes a half-finished teardown
	// obvious: the company row is still there.
	if target[tenantRoot] {
		delete(referencers, tenantRoot)
		remaining--
	}

	if remaining != 0 {
		var stuck []string
		for t := range referencers {
			stuck = append(stuck, t)
		}
		sort.Strings(stuck)
		return nil, fmt.Errorf(
			"testdb: foreign-key cycle among {%s}: no delete order exists that removes children first. "+
				"Break the cycle, or teach the plan how to defer one of its constraints — "+
				"picking an arbitrary order here would fail intermittently instead of always",
			strings.Join(stuck, ", "))
	}
	if target[tenantRoot] {
		out = append(out, tenantRoot)
	}
	return out, nil
}

// buildVerifySQL builds the single residue query. It is assembled from the plan
// (never from a literal list) so that it can never disagree with what was
// deleted — a residue check that inspects a different set of tables from the one
// the deletes covered would be worse than no check at all.
func buildVerifySQL(pl *plan) string {
	var parts []string
	for _, s := range pl.order {
		param := "$1"
		if s.IsText {
			param = "$2"
		}
		parts = append(parts, fmt.Sprintf(
			"SELECT %s AS t, count(*) AS n FROM %s WHERE company_id = %s",
			quoteLiteral(s.Table), s.Table, param))
	}
	for _, s := range pl.special {
		// The residue predicate mirrors the special DELETE, with $2 (TEXT).
		parts = append(parts, fmt.Sprintf(
			"SELECT %s AS t, count(*) AS n FROM %s WHERE workflow_id LIKE 'irn:' || $2 || ':%%'",
			quoteLiteral(s.Table), s.Table))
	}
	return strings.Join(parts, "\nUNION ALL ")
}

// quoteLiteral renders a Go string as a SQL string literal. Only ever called
// with catalog-derived table names, but it doubles the quote anyway: a helper
// that is safe only because of its callers stops being safe the first time
// somebody adds a caller.
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// connLike is the narrow slice of pgx we need from either a pooled connection or
// a transaction, so the catalog queries and the residue query can run in both.
type connLike interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}
