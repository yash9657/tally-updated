// Package migrations holds the ordered SQL schema migrations (embedded) and a
// tiny runner. Migrations are applied as the ADMIN DSN because they create the
// application role, extensions, and tables. The application itself never runs
// with admin privileges.
package migrations

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

//go:embed *.sql
var files embed.FS

// Apply runs every embedded *.sql migration, in lexical (== numeric prefix)
// order, exactly once. Applied versions are tracked in schema_migrations. The
// supplied conn must authenticate as an admin/owner role. Each migration runs
// in its own transaction so a failure leaves earlier migrations committed and
// the failing one rolled back.
//
// ⛔ ONE APPLIER AT A TIME, PER DATABASE. `go test ./... -p N` starts N
// package binaries that each call Apply on the same database; on an empty
// one they all read "nothing applied" and ran 0001 at once, failing with
// `duplicate key … pg_type_typname_nsp_index` / `relation … already exists`
// (HANDOFF-AGENTIC-WAVE-1 §4.1). So Apply holds a SESSION advisory lock from
// before it creates schema_migrations until it returns, and reads the
// applied set only once it holds it: a waiter wakes to find the work done.
// Advisory locks are scoped to the current database, so appliers on other
// databases of the cluster never wait on this one. The two-int key space
// (migrateLockClass, 1) is disjoint from durable's bigint keys and from
// intentlog's (0x1A7E, …). A connection that dies releases the lock.
func Apply(ctx context.Context, conn *pgx.Conn) error {
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1, 1)", migrateLockClass); err != nil {
		return fmt.Errorf("migrations: take the apply lock: %w", err)
	}
	defer func() {
		// Background: the unlock must be attempted even when ctx is done.
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1, 1)", migrateLockClass)
	}()
	if err := ensureMigrationsTable(ctx, conn); err != nil {
		return err
	}

	names, err := migrationNames()
	if err != nil {
		return err
	}

	applied, err := appliedVersions(ctx, conn)
	if err != nil {
		return err
	}

	for _, name := range names {
		version := strings.TrimSuffix(name, ".sql")
		if applied[version] {
			continue
		}
		body, err := files.ReadFile(name)
		if err != nil {
			return fmt.Errorf("migrations: read %s: %w", name, err)
		}
		if err := applyOne(ctx, conn, version, string(body)); err != nil {
			return fmt.Errorf("migrations: apply %s: %w", name, err)
		}
	}
	return nil
}

// migrateLockClass is the advisory-lock class for Apply ("MG").
const migrateLockClass int32 = 0x4D47

// Names returns the ordered migration file names. Exported for tooling/tests.
func Names() ([]string, error) { return migrationNames() }

func migrationNames() ([]string, error) {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return nil, fmt.Errorf("migrations: read dir: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func ensureMigrationsTable(ctx context.Context, conn *pgx.Conn) error {
	const ddl = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`
	if _, err := conn.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("migrations: ensure schema_migrations: %w", err)
	}
	return nil
}

func appliedVersions(ctx context.Context, conn *pgx.Conn) (map[string]bool, error) {
	rows, err := conn.Query(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("migrations: load applied: %w", err)
	}
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out[v] = true
	}
	return out, rows.Err()
}

func applyOne(ctx context.Context, conn *pgx.Conn, version, body string) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, body); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", version); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
