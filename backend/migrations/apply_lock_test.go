package migrations

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestConcurrentAppliesOnAnEmptyDatabase is HANDOFF-AGENTIC-WAVE-1 §4.1 as a
// test: several appliers race on a database that has NOTHING in it, as N
// package test binaries do under `go test -p N` on a fresh database. Before
// the lock, this failed with `duplicate key … pg_type_typname_nsp_index` or
// `relation … already exists`.
//
// It creates its OWN throwaway database (never the one the env names — that
// one is shared with the rest of the suite and is already migrated, which is
// exactly why the bug hid for so long) and drops it after.
func TestConcurrentAppliesOnAnEmptyDatabase(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	ctx := context.Background()
	dsn := "postgres://postgres:postgres@localhost:5433/tally?sslmode=disable"
	for _, k := range []string{"TESTDB_ADMIN_DSN", "ADMIN_DATABASE_URL"} {
		if v := os.Getenv(k); v != "" {
			dsn = v
			break
		}
	}
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("admin postgres unreachable and CORED_SKIP_DB is not set: %v", err)
	}
	name := fmt.Sprintf("tally_applylock_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		admin.Close(ctx)
		t.Fatalf("create %s: %v", name, err)
	}
	// ⛔ THE DROP OWNS THE ADMIN CONNECTION'S LIFETIME. A `defer admin.Close`
	// runs when this function returns — BEFORE t.Cleanup — so the drop ran on
	// a closed connection, its error was discarded, and every run leaked a
	// tally_applylock_* database into the shared cluster (five found at the
	// wave-2 merge). Report a failed drop instead of swallowing it.
	t.Cleanup(func() {
		bg := context.Background()
		defer admin.Close(bg)
		if _, err := admin.Exec(bg, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("cleanup: drop %s: %v", name, err)
		}
	})
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Database = name

	const appliers = 4
	errs := make([]error, appliers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < appliers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			conn, err := pgx.ConnectConfig(ctx, cfg.Copy())
			if err != nil {
				errs[i] = err
				return
			}
			defer conn.Close(ctx)
			<-start
			errs[i] = Apply(ctx, conn)
		}(i)
	}
	close(start)
	wg.Wait()
	failed := 0
	for i, err := range errs {
		if err != nil {
			failed++
			t.Errorf("applier %d of %d: %v", i+1, appliers, err)
		}
	}

	conn, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var applied int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&applied); err != nil {
		t.Fatal(err)
	}
	names, _ := Names()
	if applied != len(names) {
		t.Errorf("schema_migrations has %d rows, %d files on disk", applied, len(names))
	}
	var held int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND classid = $1 AND objid = 1 AND database = (SELECT oid FROM pg_database WHERE datname = current_database())", migrateLockClass).Scan(&held); err != nil {
		t.Fatal(err)
	}
	if held != 0 {
		t.Errorf("%d apply locks still held after every applier returned", held)
	}
	t.Logf("%d concurrent appliers on an empty database: %d failed, %d migrations recorded once each", appliers, failed, applied)
}
