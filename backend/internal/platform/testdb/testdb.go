// Package testdb is the shared teardown harness for backend tests that create a
// company in the development database.
//
// # WHY THIS PACKAGE EXISTS
//
// Before it, every DB-touching package carried its own copy of a 25-line
// setup(t) helper, and each copy cleaned up a slightly different subset of the
// schema — or nothing at all. One full `go test ./...` left 685 net-new
// companies behind; over time the dev database reached 43,419 companies, 262,658
// vouchers and 2 GB, of which 603 MB was account_group rows orphaned by a
// cleanup that deleted only voucher_version and company. Nothing was ever red,
// because a test that leaks data still passes. This package makes the teardown
// ONE implementation, derived from the live schema, that fails the test when it
// does not fully succeed.
//
// WHY IT IMPORTS NOTHING FROM internal/
//
// Its archtest entry is the empty set. testdb talks to Postgres directly over
// pgxpool with the ADMIN DSN and does not import platform/db, identity, or any
// other sibling. That is deliberate: every package's tests need to import it, so
// any sibling edge it acquired would have to be added to every one of those
// packages' allowed sets forever, and a harness would end up dictating the
// production import graph. It does not import "testing" either — it accepts the
// minimal TB interface below, which *testing.T satisfies — so that the package
// can be checked by the ordinary boundary walker without special-casing.
//
// It is nonetheless a NON-test package, which means it COULD be linked into
// cored. TestNoProductionCodeImportsTestdb in internal/archtest is what stops
// that: a binary that ships a superuser-DSN mass-delete helper is not something
// to rely on code review to prevent.
package testdb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/khata/cored/internal/platform/tenant"
)

// TB is the slice of *testing.T this package needs. Taking an interface rather
// than *testing.T is what lets testdb avoid importing "testing" (see the package
// comment) and also lets the harness be exercised with a recording fake.
type TB interface {
	Helper()
	Errorf(format string, args ...any)
	Cleanup(func())
}

// AdminDSN is the SUPERUSER connection. Teardown needs it and cannot work
// without it: tally_app cannot SET session_replication_role, does not own the
// tables, has no DELETE grant on notification_outbox, and is FORCE-RLS'd to a
// single company — so as the app role a teardown would silently delete nothing.
const AdminDSN = "postgres://postgres:postgres@localhost:5433/tally?sslmode=disable"

// AppDSN is the ordinary, RLS-restricted application role, offered here so tests
// have one place to get both DSNs instead of thirteen copies of each string.
const AppDSN = "postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable"

// AdminDSNEnv lets CI or a differently-provisioned developer point the harness
// at another cluster without editing code.
const AdminDSNEnv = "TESTDB_ADMIN_DSN"

// dropTimeout bounds a single teardown. A cleanup that hangs holds the whole
// package's test binary open and looks like a flake, so it is given a deadline
// and allowed to report a timeout as the failure it is.
const dropTimeout = 60 * time.Second

var (
	poolOnce sync.Once
	poolVal  *pgxpool.Pool
	poolErr  error
)

// AppDSNEnv is AdminDSNEnv's twin for the application role.
//
// ⛔ THE TWO MUST MOVE TOGETHER, AND UNTIL W5 ONLY ONE COULD. Most DB tests
// migrate through the ADMIN DSN and then connect as the APP role; with only the
// admin half overridable, a run pointed at a private database migrated that
// database and then queried the shared one — which, with concurrent lanes each
// adding migrations, reads as a missing column in a file nobody touched. Every
// test DSN in the tree now goes through these two functions.
const AppDSNEnv = "TESTDB_APP_DSN"

// AdminDSNValue returns the admin DSN in force, honouring TESTDB_ADMIN_DSN.
func AdminDSNValue() string {
	if v := os.Getenv(AdminDSNEnv); v != "" {
		return v
	}
	return AdminDSN
}

// AppDSNValue returns the application-role DSN in force, honouring
// TESTDB_APP_DSN.
func AppDSNValue() string {
	if v := os.Getenv(AppDSNEnv); v != "" {
		return v
	}
	return AppDSN
}

// adminPool returns the ONE process-wide admin pool, created lazily.
//
// One pool, four connections, for the entire test binary — not one connection
// per teardown. A suite that mints hundreds of companies would otherwise open
// hundreds of superuser connections and start failing on max_connections, which
// would present as an unrelated flake in whichever test happened to be running.
// The pool is intentionally never closed: it outlives every test in the process
// and the process exit releases it.
func adminPool(ctx context.Context) (*pgxpool.Pool, error) {
	poolOnce.Do(func() {
		cfg, err := pgxpool.ParseConfig(AdminDSNValue())
		if err != nil {
			poolErr = fmt.Errorf("testdb: parse admin DSN: %w", err)
			return
		}
		cfg.MaxConns = 4
		p, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			poolErr = fmt.Errorf("testdb: connect admin pool: %w", err)
			return
		}
		if err := p.Ping(ctx); err != nil {
			p.Close()
			poolErr = fmt.Errorf("testdb: ping admin pool: %w", err)
			return
		}
		poolVal = p
	})
	return poolVal, poolErr
}

// Available reports whether the admin database is reachable, so a test can skip
// rather than fail on a developer machine with no Postgres running. It also
// honours CORED_SKIP_DB, the flag the rest of the backend suite already uses.
func Available(ctx context.Context) error {
	if os.Getenv("CORED_SKIP_DB") != "" {
		return errors.New("CORED_SKIP_DB is set")
	}
	_, err := adminPool(ctx)
	return err
}

// Cleanup registers a teardown of companyID that runs when t (and every subtest
// of t) has finished. This is the call every company-creating test should make,
// immediately after the company exists.
func Cleanup(t TB, companyID uuid.UUID) {
	t.Helper()
	t.Cleanup(func() { DropNow(t, companyID) })
}

// DropNow tears the company down immediately, for loops that create many
// companies and must not hold them all until the end of the test.
func DropNow(t TB, companyID uuid.UUID) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), dropTimeout)
	defer cancel()
	if err := Drop(ctx, companyID); err != nil {
		// t.Errorf, and specifically NOT t.Fatalf and NOT a log line.
		//
		// Not Fatalf: FailNow may only be called from the goroutine running the
		// test, and inside a cleanup it aborts the REMAINING cleanup functions
		// registered on that *testing.T. In the two-tenant harnesses (ledger,
		// query, reporting, masters, security, server) two companies are
		// registered, so a Fatalf on the first would skip the teardown of the
		// second — turning one REPORTED leak into two SILENT ones, plus a skipped
		// pool.Close in whatever harness registered one.
		//
		// Not a log line: t.Log on a passing test is invisible without -v, and a
		// teardown that only logs is precisely how a residue bound stays green
		// while the database fills up. That is the failure this package exists to
		// end, so the failure mode has to be a failing test.
		t.Errorf("%v", err)
	}
}

// KeptCompany records a company deliberately left in the database.
type KeptCompany struct {
	ID     uuid.UUID
	Reason string
}

var (
	keptMu sync.Mutex
	kept   []KeptCompany
)

// Keep declares that companyID is deliberately NOT torn down, and why.
//
// It deletes nothing. Its whole value is that it is a POSITIVE, greppable
// statement of intent: internal/testhygiene's static check accepts a
// company-creating test whose enclosing function mentions testdb.Keep, and
// TestOptOutsAreDeclared requires every Keep call site to be listed with a
// reason in a hand-maintained map that a reviewer reads. Simply omitting the
// cleanup call would be indistinguishable from forgetting it — which is how 685
// companies per run happened — so the opt-out has to be louder than the default,
// not quieter.
//
// There are no callers today, and TestOptOutsAreDeclared asserts there are none.
func Keep(t TB, companyID uuid.UUID, reason string) {
	t.Helper()
	if strings.TrimSpace(reason) == "" {
		t.Errorf("testdb.Keep(%s): a reason is required — an undocumented opt-out is a leak with better manners", companyID)
		return
	}
	keptMu.Lock()
	defer keptMu.Unlock()
	kept = append(kept, KeptCompany{ID: companyID, Reason: reason})
}

// Kept returns the companies declared with Keep in this process.
func Kept() []KeptCompany {
	keptMu.Lock()
	defer keptMu.Unlock()
	out := make([]KeptCompany, len(kept))
	copy(out, kept)
	return out
}

// tableCount is one row of the residue query.
type tableCount struct {
	Table string
	N     int64
}

// Drop removes every row belonging to companyID, from every table the derived
// plan covers, in one transaction, and VERIFIES that it did so before it commits.
//
// The transaction is the mechanism:
//
//	BEGIN
//	SET LOCAL session_replication_role = replica
//	<one batched DELETE per table, in reverse-topological order>
//	<one UNION ALL residue count over the same tables>
//	COMMIT
//
// session_replication_role = replica is what makes the DELETEs possible at all.
// Six BEFORE ROW UPDATE-OR-DELETE triggers (posting_no_mutate,
// bill_allocation_no_mutate, voucher_gst_line_no_mutate,
// voucher_version_no_mutate, stock_movement_no_mutate, master_audit_no_mutate)
// RAISE on any delete, superuser included — that immutability is a real product
// guarantee and is not being weakened here. SET LOCAL is scoped to this one
// transaction on this one connection and cannot outlive it, so no other session
// ever observes immutability disarmed, not even for an instant.
//
// `ALTER TABLE ... DISABLE TRIGGER` is BANNED as the alternative. It is
// database-wide and PERSISTENT: a test that crashes between disable and re-enable
// leaves the immutability guarantee off for every other connection and every
// later test run. That is exactly why `go test ./internal/server` is currently
// unsafe to run unscoped, and this package exists partly to retire those two
// call sites.
//
// The residue check runs INSIDE the transaction, before COMMIT, because replica
// mode also suppresses foreign-key enforcement: an incomplete plan does not error,
// it succeeds and leaves orphans. "The DELETE returned no error" is therefore not
// evidence of anything. Counting what survived is.
//
// KNOW WHAT THE RESIDUE CHECK CANNOT DO. It is generated from the SAME plan the
// deletes came from, so it can only catch a delete that ran against a table in
// the plan and failed to remove that table's rows — a wrong predicate, a wrong
// parameter binding, a partially-applied batch. It CANNOT catch a table that is
// missing from the plan entirely, because such a table is missing from the count
// query too. That second failure mode is covered by a different layer:
// TestEveryTableIsClassifiedForTeardown (every base table must be classified)
// and the fixture/plan equality assertion in seedCompany. Two layers, because
// one of them structurally cannot see the other's failure.
func Drop(ctx context.Context, companyID uuid.UUID) error {
	if companyID == uuid.Nil {
		return errors.New("testdb: refusing to drop the nil company (a zero uuid usually means an id was never assigned)")
	}
	pl, err := loadPlan(ctx)
	if err != nil {
		return err
	}
	return dropWithPlan(ctx, pl, companyID)
}

// dropWithPlan is Drop's body, parameterised on the plan so that the residue
// check can be exercised against a deliberately broken plan (see
// TestResidueCheckCatchesADeleteThatMissedItsRows). Production callers always go
// through Drop and get the derived plan.
func dropWithPlan(ctx context.Context, pl *tenant.Plan, companyID uuid.UUID) error {
	pool, err := adminPool(ctx)
	if err != nil {
		return err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("testdb: begin teardown tx for company %s: %w", companyID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after a successful commit

	if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
		return fmt.Errorf("testdb: disarm immutability triggers for company %s "+
			"(needs a SUPERUSER connection; the app role cannot do this): %w", companyID, err)
	}

	batch := &pgx.Batch{}
	idText := companyID.String()
	for _, s := range pl.Special {
		batch.Queue(s.SQL, idText)
	}
	for _, s := range pl.Order {
		if s.IsText {
			batch.Queue(`DELETE FROM `+s.Table+` WHERE company_id = $1`, idText)
		} else {
			batch.Queue(`DELETE FROM `+s.Table+` WHERE company_id = $1`, companyID)
		}
	}
	br := tx.SendBatch(ctx, batch)
	for i := 0; i < batch.Len(); i++ {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			return fmt.Errorf("testdb: delete %s for company %s: %w", batchTableName(pl, i), companyID, err)
		}
	}
	if err := br.Close(); err != nil {
		return fmt.Errorf("testdb: close teardown batch for company %s: %w", companyID, err)
	}

	counts, err := residue(ctx, tx, pl, companyID)
	if err != nil {
		return err
	}
	var survivors []string
	for _, c := range counts {
		if c.N > 0 {
			survivors = append(survivors, fmt.Sprintf("%s=%d", c.Table, c.N))
		}
	}
	if len(survivors) > 0 {
		sort.Strings(survivors)
		return fmt.Errorf(
			"testdb: company %s NOT fully torn down — rows survived the delete: %s (plan covered %d tables). "+
				"Test data is leaking into the shared dev database; classify the table or fix its delete predicate",
			companyID, strings.Join(survivors, " "), len(pl.Order)+len(pl.Special))
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("testdb: commit teardown for company %s: %w", companyID, err)
	}
	return nil
}

// batchTableName maps a batch index back to the table it deleted from, so a
// failure names the table instead of an integer.
func batchTableName(pl *tenant.Plan, i int) string {
	if i < len(pl.Special) {
		return pl.Special[i].Table
	}
	i -= len(pl.Special)
	if i < len(pl.Order) {
		return pl.Order[i].Table
	}
	return "?"
}

// residue runs the one UNION ALL count query over every table in the plan and
// returns a count for each, INCLUDING zeros — callers filter. Returning zeros is
// what lets a test assert that the fixture actually populated every table, which
// is the difference between "the teardown works" and "the teardown was never
// given anything to delete".
func residue(ctx context.Context, q tenant.ConnLike, pl *tenant.Plan, companyID uuid.UUID) ([]tableCount, error) {
	rows, err := q.Query(ctx, pl.VerifySQL, companyID, companyID.String())
	if err != nil {
		return nil, fmt.Errorf("testdb: residue check for company %s: %w", companyID, err)
	}
	defer rows.Close()

	var out []tableCount
	for rows.Next() {
		var c tableCount
		if err := rows.Scan(&c.Table, &c.N); err != nil {
			return nil, fmt.Errorf("testdb: scan residue row: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("testdb: residue check for company %s: %w", companyID, err)
	}
	return out, nil
}
