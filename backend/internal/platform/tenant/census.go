// Package tenant derives, FROM THE LIVE SCHEMA, the complete set of tables that
// hold one company's data — and how each one's tenancy is expressed.
//
// # WHY THIS IS A PRODUCTION PACKAGE, AND WHY THERE IS EXACTLY ONE OF THEM
//
// Two features need this enumeration and they need the SAME one: the test-data
// teardown harness (internal/platform/testdb), which deletes a company, and the
// backup service (internal/backup), which serialises one. The census used to
// live inside testdb, where the backup could not reach it —
// TestNoProductionCodeImportsTestdb in internal/archtest forbids production code
// importing testdb at all, because testdb carries a hardcoded SUPERUSER DSN and
// a routine that disarms every immutability trigger and mass-deletes a tenant.
// That rule is correct and is not being relaxed; the census moved out from under
// it instead.
//
// ⛔ THE POINT OF THIS PACKAGE IS THAT THERE IS EXACTLY ONE ENUMERATION. If the
// backup ever keeps its own list of tables — even a "temporary" one, even a
// generated one that is refreshed by hand — then on the day a migration adds a
// table, teardown cleans it and backup silently omits it, and the omission is
// discovered by somebody restoring a company that is missing a plane of its
// books. A SECOND LIST IS THE DEFECT, not a shortcut to the feature. Anything
// that needs to know which tables are a company's data calls Census.
//
// # WHY IT IS DERIVED AND NOT WRITTEN DOWN
//
// A hardcoded list of tables is correct exactly until the next migration, and
// both failures it causes are silent: a teardown that misses a table succeeds
// while the database grows (that is literally how this repository accumulated
// 43,419 test companies and 603 MB of orphaned account_group rows), and a backup
// that misses a table produces an archive that restores without error and
// without a plane of the company.
//
// Enumeration alone is NOT sufficient, though, because tenancy is not always
// spelled `company_id`. The registries below carry the exceptions, and
// TestEveryTableIsClassified makes their COMPLETENESS a test: every base table
// in `public` must be classified as exactly one of
//
//	(a) scoped         — it has a company_id column, enumerated dynamically
//	(b) SpecialTenancy — tenancy is encoded some other way; we supply the predicate
//	(c) NotTenantScoped — it genuinely does not belong to a company
//
// Add a table with a company_id and it is covered automatically. Add a table
// whose tenancy is expressed some other way and that test fails, by name, on the
// day the migration lands — which is the only moment anybody still remembers how
// its tenancy works.
//
// # WHAT IS NOT HERE
//
// No DSN, no pool, no DELETE, no privileged operation of any kind. Census takes
// a ConnLike — the narrow Query slice of pgx that both a pooled connection and a
// pgx.Tx satisfy — so it runs on whatever connection the caller already has, at
// whatever privilege the caller already holds. This package imports nothing from
// internal/ and its archtest entry is the empty set.
package tenant

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// ConnLike is the narrow slice of pgx the census needs, satisfied by both a
// pooled connection and a transaction, so the catalog queries can run on either.
type ConnLike interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Scoped is a table that carries a literal company_id column.
type Scoped struct {
	Table string
	// IsText is true when company_id is TEXT rather than UUID. Exactly one table
	// is like this today: insightd_draft, written by the Python insightd service,
	// which stores the company id as a string. It is the reason the enumeration
	// query below must NOT filter on data_type='uuid': such a filter compiles,
	// passes every type check, and silently stops covering insightd drafts.
	IsText bool
}

// SpecialStmt is one table whose tenancy is not a company_id column, carrying
// BOTH statements generated from its single registered predicate. SQL is the
// DELETE ($1); Predicate is the raw registered form, still holding IDParam, from
// which the residue query and the backup's SELECT render their own copies. The
// company id is always TEXT-bound for these tables, because their encodings are
// textual by nature.
type SpecialStmt struct {
	Table     string
	SQL       string
	Predicate string
}

// Plan is the derived census: every table holding one company's data, in an
// order that is safe to delete in (children first) and therefore safe to INSERT
// in when reversed (parents first).
type Plan struct {
	// Order is the scoped tables in reverse-topological FK order: a table appears
	// only after every table that REFERENCES it, ties broken alphabetically so the
	// order is byte-stable across runs. TenantRoot is forced last.
	Order []Scoped
	// Special holds the SpecialTenancy tables. For teardown they are issued BEFORE
	// Order, which is always safe: these tables participate in no FK edge at all
	// (checked by the builder), so moving them can never violate the topological
	// constraint.
	Special []SpecialStmt
	// VerifySQL is one UNION ALL over every table the plan touches, returning
	// (table, row_count) for one company. $1 is the company id as UUID, $2 the
	// same id as TEXT.
	VerifySQL string
}

// Tables returns every table name the census covers, scoped and special alike,
// in Order's order with the special tables last. It is the enumeration callers
// that do not care HOW tenancy is expressed should use — and the reason none of
// them needs to keep a list.
func (p *Plan) Tables() []string {
	out := make([]string, 0, len(p.Order)+len(p.Special))
	for _, s := range p.Order {
		out = append(out, s.Table)
	}
	for _, s := range p.Special {
		out = append(out, s.Table)
	}
	return out
}

// IDParam is the placeholder a SpecialTenancy predicate writes where the company
// id goes. It is substituted with the right positional parameter for whichever
// statement is being generated.
//
// A NAMED TOKEN rather than a literal "$1" that gets rewritten: rewriting would
// mean a textual search for "$1" inside a predicate that may legitimately contain
// other parameters or the character sequence in a string literal, and the failure
// would be a silently mis-bound query — the exact class of bug this file already
// carries a warning about.
const IDParam = "{{id}}"

// TenantRoot is the company table itself: deleted last, and INSERTed first.
const TenantRoot = "company"

// SpecialTenancy maps a table with NO company_id column to the WHERE PREDICATE
// that selects one company's rows in it, written with IDParam where the company
// id belongs.
//
// A PREDICATE, NOT A STATEMENT, AND THIS IS THE WHOLE POINT. Every statement any
// caller needs — the teardown DELETE, the residue count, the backup SELECT — is
// generated from this one string, so they cannot disagree. They used to be able
// to: the registry held a full DELETE while BuildVerifySQL hardcoded
// statutory_step's `workflow_id LIKE 'irn:' || $2 || ':%'` for EVERY special
// entry. With only one entry registered that was invisible, but the second entry
// would have been deleted by its own predicate and then verified by
// statutory_step's — so a wrong predicate would delete nothing, count nothing,
// and the teardown would report success while orphaning tenant rows.
//
// statutory_step has no company_id, no foreign key and no RLS policy: it is the
// statutoryd plane's durable workflow log, and tenancy is encoded in its TEXT
// primary key as "irn:<companyID>:<voucherID>" — see
// backend/internal/statutory/irn_workflow.go. Nothing about the column list of
// that table reveals that it is tenant data, which is exactly why dynamic
// enumeration cannot find it and why this registry exists.
//
// ⚠️ AND IT IS WHY A RESTORE HAS TO REWRITE A VALUE. Every other table carries
// its tenancy in a column a restore simply carries across; this one carries it
// INSIDE a primary key, so restoring a company under a new id must re-spell the
// key. See RewriteSpecialKey.
var SpecialTenancy = map[string]string{
	"statutory_step": `workflow_id LIKE 'irn:' || ` + IDParam + ` || ':%'`,
}

// specialKeyColumn names, per SpecialTenancy table, the column whose VALUE
// embeds the company id, together with the prefix and suffix around it. A
// restore under a different company id must rewrite exactly this column and
// nothing else.
//
// It is a separate registry from SpecialTenancy on purpose: the predicate says
// how to FIND the rows, this says how to TRANSFORM them, and a table could
// plausibly need the first without the second. An entry here without one there
// is a bug, and TestEverySpecialKeyRewriteHasAPredicate says so.
var specialKeyColumn = map[string]struct{ Column, Prefix string }{
	"statutory_step": {Column: "workflow_id", Prefix: "irn:"},
}

// RewriteSpecialKey re-spells one archived value of a SpecialTenancy table's
// tenancy-bearing column so it names newID instead of oldID, and reports whether
// the table has such a column at all.
//
// ⛔ IT IS DELIBERATELY NOT A BLIND strings.Replace. The old company id is a
// uuid, so a blind replace would also rewrite any OTHER occurrence of it in the
// value — and for a workflow_id of the form "irn:<company>:<voucher>" a voucher
// id can never equal the company id, but that is a property of today's data, not
// of the format. The rewrite is anchored on the registered prefix and on the
// exact segment that follows it, so a value that does not have the expected
// shape is REPORTED rather than silently half-rewritten.
func RewriteSpecialKey(table, value, oldID, newID string) (string, bool, error) {
	spec, ok := specialKeyColumn[table]
	if !ok {
		return value, false, nil
	}
	if !strings.HasPrefix(value, spec.Prefix) {
		return "", true, fmt.Errorf(
			"tenant: %s value %q does not start with the registered prefix %q; "+
				"its tenancy encoding changed and the restore rewrite must be taught the new shape "+
				"before it silently restores rows that still name the SOURCE company",
			table, value, spec.Prefix)
	}
	rest := strings.TrimPrefix(value, spec.Prefix)
	seg, tail, found := strings.Cut(rest, ":")
	if !found {
		return "", true, fmt.Errorf(
			"tenant: %s value %q has no segment separator after %q; expected %s<companyID>:<rest>",
			table, value, spec.Prefix, spec.Prefix)
	}
	if seg != oldID {
		return "", true, fmt.Errorf(
			"tenant: %s value %q names company %q, not the archive's source company %q; "+
				"the archive mixes tenants and must not be restored",
			table, value, seg, oldID)
	}
	return spec.Prefix + newID + ":" + tail, true, nil
}

// SpecialKeyColumn returns the tenancy-bearing column name for a SpecialTenancy
// table, or "" if the table has none.
func SpecialKeyColumn(table string) string {
	return specialKeyColumn[table].Column
}

// NotTenantScoped maps a table that must NOT be treated as a company's data to
// the reason why. Every entry is either user-scoped (it belongs to a person, who
// outlives any one company) or global (it belongs to the database).
//
// This is a registry of DELIBERATE OMISSIONS, and each one has to be written
// down, because "no rule matched this table" and "we decided not to touch this
// table" look identical from the outside and only one of them is a bug.
var NotTenantScoped = map[string]string{
	"app_user":           "a person, shared across every company they are a member of",
	"auth_login_attempt": "subject-keyed lockout audit; survives the company it was attempted against",
	"schema_migrations":  "global: the migration ledger of the database itself",
	"user_credential":    "user-scoped password/credential rows; cascade off app_user, not company",
	"webauthn_challenge": "user-scoped, short-lived ceremony state",
	"webauthn_credential": "user-scoped authenticator registration; a person keeps their key " +
		"when a company is deleted",
}

// Census derives the plan by issuing the two catalog queries on conn and
// assembling the result. It is pure catalog reading: it holds no state, caches
// nothing, and may be called on any connection the caller can already use.
func Census(ctx context.Context, conn ConnLike) (*Plan, error) {
	scopedTables, err := ScopedTables(ctx, conn)
	if err != nil {
		return nil, err
	}
	edges, err := FKEdges(ctx, conn)
	if err != nil {
		return nil, err
	}
	return BuildPlan(scopedTables, edges)
}

// BuildPlan assembles a Plan from an already-queried census, so the ordering
// logic can be exercised without a database.
func BuildPlan(scopedTables map[string]bool, edges []FKEdge) (*Plan, error) {
	// The target set is every scoped table plus every SpecialTenancy table.
	target := make(map[string]bool, len(scopedTables)+len(SpecialTenancy))
	for t := range scopedTables {
		target[t] = true
	}
	for t := range SpecialTenancy {
		target[t] = true
	}

	ordered, err := ReverseTopo(target, edges)
	if err != nil {
		return nil, err
	}

	pl := &Plan{}
	for _, t := range ordered {
		if predicate, ok := SpecialTenancy[t]; ok {
			// A SpecialTenancy table must not participate in FK edges, or moving
			// it out of the topological order (see Plan.Special) could break the
			// ordering guarantee. Fail loudly rather than reorder silently.
			for _, e := range edges {
				if e.Child == t || e.Parent == t {
					return nil, fmt.Errorf(
						"tenant: SpecialTenancy table %q now participates in a foreign key (%s -> %s); "+
							"it can no longer be handled outside the topological order — give it a position in it",
						t, e.Child, e.Parent)
				}
			}
			pl.Special = append(pl.Special, SpecialStmt{
				Table:     t,
				SQL:       DeleteFor(t, predicate),
				Predicate: predicate,
			})
			continue
		}
		pl.Order = append(pl.Order, Scoped{Table: t, IsText: scopedTables[t]})
	}

	pl.VerifySQL = BuildVerifySQL(pl)
	return pl, nil
}

// DeleteFor renders a SpecialTenancy predicate as a DELETE binding the company
// id as $1.
func DeleteFor(table, predicate string) string {
	return `DELETE FROM ` + table + ` WHERE ` + strings.ReplaceAll(predicate, IDParam, "$1")
}

// RenderPredicate renders a registered SpecialTenancy predicate with the company
// id bound to a caller-chosen positional parameter, so a backup's SELECT, a
// teardown's DELETE and a residue count are all generated from the SAME string.
//
// It exists so that no caller ever re-spells a predicate. That is not a style
// preference: statutory_step's tenancy used to be re-spelled by the residue
// query, and with one entry registered the duplication was invisible — a second
// entry would have been deleted by its own predicate and verified by
// statutory_step's, so a wrong predicate would delete nothing, count nothing,
// and report success while orphaning tenant rows.
func RenderPredicate(predicate, param string) string {
	return strings.ReplaceAll(predicate, IDParam, param)
}

// ScopedTables returns table -> isText for every public BASE TABLE with a
// company_id column.
//
// ⛔ DO NOT add `AND c.data_type = 'uuid'` to this query. It is the single most
// tempting change in this file and it is a silent data-loss bug:
// insightd_draft.company_id is TEXT, so the filter drops it from the plan, every
// test keeps passing, and insightd drafts are neither cleaned up nor backed up.
// The type is RECORDED instead, and callers bind uuid.UUID or string from it.
func ScopedTables(ctx context.Context, conn ConnLike) (map[string]bool, error) {
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
		return nil, fmt.Errorf("tenant: enumerate company_id tables: %w", err)
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var name, dataType string
		if err := rows.Scan(&name, &dataType); err != nil {
			return nil, fmt.Errorf("tenant: scan company_id table: %w", err)
		}
		switch dataType {
		case "uuid":
			out[name] = false
		case "text", "character varying", "character":
			out[name] = true
		default:
			return nil, fmt.Errorf(
				"tenant: table %q has a company_id of unexpected type %q; teach the census how to bind it "+
					"before it silently stops being cleaned up and backed up", name, dataType)
		}
	}
	return out, rows.Err()
}

// FKEdge is a foreign key: Child references Parent.
type FKEdge struct{ Child, Parent string }

// FKEdges returns every foreign key among public base tables. Self-edges
// (account_group.parent_id -> account_group) are dropped here: they are not a
// cycle, they are a hierarchy inside one table.
//
// ⚠️ A self-edge IS load-bearing for a restore, though, in a way it is not for a
// delete: a single DELETE removes a whole hierarchy at once, but INSERTing one
// requires parents before children WITHIN the table. The restore handles that by
// running in one transaction where the FK is checked at COMMIT, which is why it
// does not need to be modelled here.
func FKEdges(ctx context.Context, conn ConnLike) ([]FKEdge, error) {
	const q = `
SELECT src.relname AS child, tgt.relname AS parent
  FROM pg_constraint k
  JOIN pg_class     src ON src.oid = k.conrelid
  JOIN pg_class     tgt ON tgt.oid = k.confrelid
  JOIN pg_namespace n   ON n.oid   = src.relnamespace
 WHERE k.contype = 'f' AND n.nspname = 'public'`
	rows, err := conn.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("tenant: enumerate foreign keys: %w", err)
	}
	defer rows.Close()

	var out []FKEdge
	seen := map[FKEdge]bool{}
	for rows.Next() {
		var e FKEdge
		if err := rows.Scan(&e.Child, &e.Parent); err != nil {
			return nil, fmt.Errorf("tenant: scan foreign key: %w", err)
		}
		if e.Child == e.Parent || seen[e] {
			continue // self-edge, or the same pair via a second constraint
		}
		seen[e] = true
		out = append(out, e)
	}
	return out, rows.Err()
}

// BaseTables returns the full census of public base tables. Only the
// classification test uses it; the plan itself does not need it.
func BaseTables(ctx context.Context, conn ConnLike) ([]string, error) {
	rows, err := conn.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname = 'public'`)
	if err != nil {
		return nil, fmt.Errorf("tenant: census public tables: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, fmt.Errorf("tenant: scan census row: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// ReverseTopo returns the tables of `target` in DELETE order: a table is emitted
// only after every table that references it, ties broken alphabetically so the
// order is byte-stable across runs, and TenantRoot forced to the very end.
// Reversed, it is INSERT order: parents before children, tenant root first.
func ReverseTopo(target map[string]bool, edges []FKEdge) ([]string, error) {
	// referencers[parent] = set of children still to be emitted.
	referencers := map[string]map[string]bool{}
	for t := range target {
		referencers[t] = map[string]bool{}
	}
	for _, e := range edges {
		if !target[e.Child] || !target[e.Parent] {
			continue // an edge leaving the target set constrains nothing here
		}
		referencers[e.Parent][e.Child] = true
	}

	var out []string
	remaining := len(target)
	for remaining > 0 {
		// Collect this wave: every remaining table nothing still references.
		var wave []string
		for t := range referencers {
			if len(referencers[t]) == 0 && t != TenantRoot {
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

	// TenantRoot is forced last. Nothing in the plan is its parent (only
	// notification_outbox references it), so moving it later never violates the
	// ordering — and putting the tenant root last makes a half-finished teardown
	// obvious: the company row is still there. Reversed for a restore it becomes
	// FIRST, which is equally what a restore needs.
	if target[TenantRoot] {
		delete(referencers, TenantRoot)
		remaining--
	}

	if remaining != 0 {
		var stuck []string
		for t := range referencers {
			stuck = append(stuck, t)
		}
		sort.Strings(stuck)
		return nil, fmt.Errorf(
			"tenant: foreign-key cycle among {%s}: no order exists that removes children first. "+
				"Break the cycle, or teach the census how to defer one of its constraints — "+
				"picking an arbitrary order here would fail intermittently instead of always",
			strings.Join(stuck, ", "))
	}
	if target[TenantRoot] {
		out = append(out, TenantRoot)
	}
	return out, nil
}

// BuildVerifySQL builds the single residue query. It is assembled from the plan
// (never from a literal list) so that it can never disagree with what was
// deleted — a residue check that inspects a different set of tables from the one
// the deletes covered would be worse than no check at all.
func BuildVerifySQL(pl *Plan) string {
	var parts []string
	for _, s := range pl.Order {
		param := "$1"
		if s.IsText {
			param = "$2"
		}
		parts = append(parts, fmt.Sprintf(
			"SELECT %s AS t, count(*) AS n FROM %s WHERE company_id = %s",
			QuoteLiteral(s.Table), s.Table, param))
	}
	for _, s := range pl.Special {
		// The residue predicate is THE REGISTERED PREDICATE, with the id bound as
		// $2 (TEXT). It is never re-spelled here: a second entry whose tenancy is
		// encoded differently must be verified by its OWN predicate, not by
		// whichever one happened to be written first.
		parts = append(parts, fmt.Sprintf(
			"SELECT %s AS t, count(*) AS n FROM %s WHERE %s",
			QuoteLiteral(s.Table), s.Table,
			strings.ReplaceAll(s.Predicate, IDParam, "$2")))
	}
	return strings.Join(parts, "\nUNION ALL ")
}

// QuoteLiteral renders a Go string as a SQL string literal. Only ever called
// with catalog-derived table names, but it doubles the quote anyway: a helper
// that is safe only because of its callers stops being safe the first time
// somebody adds a caller.
func QuoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
