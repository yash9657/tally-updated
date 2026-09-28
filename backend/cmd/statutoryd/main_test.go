package main

import (
	"bytes"
	"context"
	"log"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/khata/cored/internal/platform/testdb"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/statutory"
)

// These tests cover the statutoryd BOOT MATRIX for the durable StepStore
// (IMPLEMENTATION_PLAN.md §8.2 WS7a): no DSN keeps the in-memory store plus a
// loud warning; a DSN whose database lacks statutory_step refuses to boot with
// "cored migrate" guidance; a DSN whose ROLE cannot read the table also refuses
// to boot (instead of failing mid-workflow); a good DSN yields the PGStore.
//
// Every DB-touching case is connect-or-skip, so `go test ./cmd/...` stays green
// offline and with CORED_SKIP_DB set.

var (
	testAdminDSN = testdb.AdminDSNValue()
	testAppDSN   = testdb.AppDSNValue()
	// Same server, a database that will never hold statutory_step.
	testNoTableDSN = withDatabase(testAppDSN, "", "postgres")
	// The database the app DSN names: the probe role is granted CONNECT on
	// THIS one, never on a hardcoded "tally" (every lane and the integrator
	// run on a private database; wave-2 merge, HANDOFF-AGENTIC-WAVE-2 §4).
	testDBName = strings.TrimPrefix(mustParse(testAppDSN).Path, "/")
)

func mustParse(dsn string) *url.URL {
	u, err := url.Parse(dsn)
	if err != nil {
		panic("statutoryd test: unparseable DSN: " + err.Error())
	}
	return u
}

// withDatabase is dsn on the same server, with its user replaced (when
// userinfo is non-empty, "user:password") and its database set to db.
func withDatabase(dsn, userinfo, db string) string {
	u := mustParse(dsn)
	if userinfo != "" {
		name, pass, _ := strings.Cut(userinfo, ":")
		u.User = url.UserPassword(name, pass)
	}
	u.Path = "/" + db
	return u.String()
}

// captureLog redirects the standard logger for the duration of fn.
func captureLog(fn func()) string {
	var buf bytes.Buffer
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	}()
	fn()
	return buf.String()
}

func adminConn(t *testing.T) *pgx.Conn {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped (CORED_SKIP_DB set)")
	}
	conn, err := pgx.Connect(context.Background(), testAdminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	var present bool
	_ = conn.QueryRow(context.Background(),
		`SELECT to_regclass('public.statutory_step') IS NOT NULL`).Scan(&present)
	if !present {
		t.Skip("migration 0013 (statutory_step) not applied — skipping")
	}
	return conn
}

// TestNewStepStore_NoDSNKeepsMemStoreWithLoudLog: the default (unconfigured)
// deployment must still work, but must SAY that progress is lost on restart.
func TestNewStepStore_NoDSNKeepsMemStoreWithLoudLog(t *testing.T) {
	var store statutory.StepStore
	var closeFn func()
	var err error
	out := captureLog(func() { store, closeFn, err = newStepStore(context.Background(), "") })
	if err != nil {
		t.Fatalf("no DSN must not be an error: %v", err)
	}
	defer closeFn()

	if _, ok := store.(*statutory.MemStore); !ok {
		t.Fatalf("no DSN must yield *statutory.MemStore, got %T", store)
	}
	for _, want := range []string{"WARNING", "STATUTORYD_DSN", "IN-MEMORY", "restarts lose progress"} {
		if !strings.Contains(out, want) {
			t.Fatalf("startup log must contain %q; got:\n%s", want, out)
		}
	}
}

// TestNewStepStore_MissingTableFailsWithMigrateHint: statutoryd owns no schema,
// so a DSN pointing at an unmigrated database must fail at BOOT with actionable
// guidance — never start and fail on the first Append.
func TestNewStepStore_MissingTableFailsWithMigrateHint(t *testing.T) {
	adminConn(t) // skip-guard: needs a live server (and proves 0013 IS applied to the app DSN's database)

	store, closeFn, err := newStepStore(context.Background(), testNoTableDSN)
	if err == nil {
		closeFn()
		t.Fatalf("a database without statutory_step must fail boot, got store %T", store)
	}
	if !strings.Contains(err.Error(), "statutory_step") {
		t.Fatalf("error must name the missing table: %v", err)
	}
	if !strings.Contains(err.Error(), "cored migrate") {
		t.Fatalf("error must tell the operator to run `cored migrate`: %v", err)
	}
}

// TestNewStepStore_UnreachableDSNFailsClosed: no silent fallback to MemStore.
func TestNewStepStore_UnreachableDSNFailsClosed(t *testing.T) {
	// Port 1 is reserved and never listening.
	store, closeFn, err := newStepStore(context.Background(),
		"postgres://tally_app:tally_app@127.0.0.1:1/tally?sslmode=disable&connect_timeout=2")
	if err == nil {
		closeFn()
		t.Fatalf("an unreachable DSN must fail boot, got store %T (silent MemStore fallback would lose IRN state)", store)
	}
	if !strings.Contains(err.Error(), "cored migrate") {
		t.Fatalf("error should carry the operator hint: %v", err)
	}
}

// TestNewStepStore_UnprivilegedRoleFailsAtBoot proves the privilege probe is not
// dead code: to_regclass resolves a NAME and ignores privileges, so a forgotten
// GRANT would otherwise sail past boot and explode mid-workflow.
func TestNewStepStore_UnprivilegedRoleFailsAtBoot(t *testing.T) {
	admin := adminConn(t)
	ctx := context.Background()

	role := "statd_noperm_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := admin.Exec(ctx,
		`CREATE ROLE `+role+` LOGIN PASSWORD 'probe' NOSUPERUSER NOCREATEDB NOCREATEROLE`); err != nil {
		t.Skipf("cannot create a probe role (needs CREATEROLE): %v", err)
	}
	// Teardown must be THOROUGH: a role holding any privilege (here CONNECT on
	// the database) cannot be dropped, and this database is shared with other
	// work — a leaked role is real pollution. Failures are reported, not
	// swallowed.
	t.Cleanup(func() {
		bg := context.Background()
		if _, err := admin.Exec(bg, `REVOKE ALL ON DATABASE `+pgx.Identifier{testDBName}.Sanitize()+` FROM `+role); err != nil {
			t.Errorf("cleanup: revoke database privileges from %s: %v", role, err)
		}
		if _, err := admin.Exec(bg, `DROP OWNED BY `+role); err != nil {
			t.Errorf("cleanup: drop objects owned by %s: %v", role, err)
		}
		if _, err := admin.Exec(bg, `DROP ROLE IF EXISTS `+role); err != nil {
			t.Errorf("cleanup: drop probe role %s: %v", role, err)
		}
	})
	if _, err := admin.Exec(ctx, `GRANT CONNECT ON DATABASE `+pgx.Identifier{testDBName}.Sanitize()+` TO `+role); err != nil {
		t.Skipf("cannot grant connect to the probe role: %v", err)
	}
	// Deliberately NO grant on statutory_step.

	store, closeFn, err := newStepStore(ctx,
		withDatabase(testAppDSN, role+":probe", testDBName))
	if err == nil {
		closeFn()
		t.Fatalf("a role without SELECT on statutory_step must fail boot, got store %T", store)
	}
	if !strings.Contains(err.Error(), "GRANT") {
		t.Fatalf("error should point at the missing GRANT: %v", err)
	}
}

// TestNewStepStore_GoodDSNYieldsDurableStore: the happy path actually returns
// the Postgres store (not a fallback) and that store really writes to the table.
func TestNewStepStore_GoodDSNYieldsDurableStore(t *testing.T) {
	admin := adminConn(t)
	ctx := context.Background()

	var store statutory.StepStore
	var closeFn func()
	var err error
	out := captureLog(func() { store, closeFn, err = newStepStore(ctx, testAppDSN) })
	if err != nil {
		t.Fatalf("boot with a migrated DSN: %v", err)
	}
	defer closeFn()

	pg, ok := store.(*statutory.PGStore)
	if !ok {
		t.Fatalf("a good DSN must yield *statutory.PGStore, got %T", store)
	}
	if !strings.Contains(out, "DURABLE") {
		t.Fatalf("startup log should announce the durable store; got:\n%s", out)
	}

	// Independent oracle: write through the store, read back with the ADMIN conn.
	wf := "irn:bootmatrix:" + uuid.NewString()
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM statutory_step WHERE workflow_id = $1`, wf)
	})
	if err := pg.Append(context.Background(), wf, statutory.StepResult{Name: "build", Output: []byte(`{"ok":true}`)}); err != nil {
		t.Fatalf("Append through the booted store: %v", err)
	}
	var n int
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM statutory_step WHERE workflow_id = $1`, wf).Scan(&n); err != nil {
		t.Fatalf("oracle count: %v", err)
	}
	if n != 1 {
		t.Fatalf("the booted store did not persist to statutory_step (found %d rows)", n)
	}
}
