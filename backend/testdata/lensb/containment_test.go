package lensb

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/platform/testdb"
)

// LENS B / 2. While ONE session holds a teardown transaction open with
// `SET LOCAL session_replication_role = replica` and has already deleted
// immutable rows under it, a SECOND, concurrent session must still be REFUSED
// by voucher_version_no_mutate. That is the containment claim; the banned
// `ALTER TABLE ... DISABLE TRIGGER` would have let the second session through.
func TestContainment_ConcurrentSessionStillHitsTheAppendOnlyTrigger(t *testing.T) {
	ctx := context.Background()
	s1 := adminConn(t) // the "teardown" session
	s2 := adminConn(t) // an innocent bystander session

	victim, other := newID(t), newID(t)
	if err := Seed(ctx, s1, victim); err != nil {
		t.Fatalf("seed victim: %v", err)
	}
	if err := Seed(ctx, s1, other); err != nil {
		t.Fatalf("seed other: %v", err)
	}
	t.Cleanup(func() {
		_ = testdb.Drop(ctx, other)
		_ = testdb.Drop(ctx, victim)
	})

	// Baseline: with no teardown in flight, session 2's UPDATE is refused.
	_, base := s2.Exec(ctx, `UPDATE voucher_version SET created_by='tamper' WHERE company_id=$1`, other)
	if base == nil {
		t.Fatal("BASELINE BROKEN: voucher_version is supposed to be append-only but an ordinary UPDATE succeeded")
	}
	t.Logf("baseline (no teardown running): session2 UPDATE -> %v", base)

	// Now open the teardown transaction and hold it.
	tx, err := s1.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
		t.Fatalf("set local: %v", err)
	}
	// Prove replica mode IS in force on session 1: this delete would otherwise
	// be refused by voucher_version_no_mutate.
	tag, err := tx.Exec(ctx, `DELETE FROM voucher_version WHERE company_id=$1`, victim)
	if err != nil {
		t.Fatalf("session1 delete under replica mode should succeed: %v", err)
	}
	t.Logf("session1 (inside tx, replica mode): DELETE voucher_version removed %d immutable row(s) — replica mode confirmed active", tag.RowsAffected())

	var role1, role2 string
	if err := tx.QueryRow(ctx, `SHOW session_replication_role`).Scan(&role1); err != nil {
		t.Fatal(err)
	}
	if err := s2.QueryRow(ctx, `SHOW session_replication_role`).Scan(&role2); err != nil {
		t.Fatal(err)
	}
	t.Logf("WHILE the teardown tx is open: session1 session_replication_role=%q  session2=%q", role1, role2)
	if role1 != "replica" {
		t.Errorf("session1 should be in replica mode, got %q", role1)
	}
	if role2 != "origin" {
		t.Errorf("CONTAINMENT BROKEN: session2 sees session_replication_role=%q while another session is tearing down", role2)
	}

	// The actual containment assertion: concurrent UPDATE on a DIFFERENT company's
	// voucher_version, from a different connection, while the teardown is mid-flight.
	done := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, e := s2.Exec(ctx, `UPDATE voucher_version SET created_by='tamper' WHERE company_id=$1`, other)
		done <- e
	}()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("CONTAINMENT BROKEN: a concurrent UPDATE on voucher_version SUCCEEDED while a " +
				"teardown transaction had session_replication_role=replica set. The immutability " +
				"guarantee is observable-off to other connections.")
		}
		if !strings.Contains(strings.ToLower(e.Error()), "append-only") {
			t.Errorf("expected an immutability error, got %v", e)
		}
		t.Logf("session2 UPDATE DURING the teardown tx -> %v  (still refused: containment holds)", e)
	case <-time.After(10 * time.Second):
		t.Fatal("session2 UPDATE blocked for 10s — unexpected lock interaction")
	}
	wg.Wait()

	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	// After the tx ends, session1's own connection is back to origin.
	if err := s1.QueryRow(ctx, `SHOW session_replication_role`).Scan(&role1); err != nil {
		t.Fatal(err)
	}
	t.Logf("after ROLLBACK: session1 session_replication_role=%q", role1)
	if role1 != "origin" {
		t.Errorf("SET LOCAL escaped its transaction: session1 role is %q", role1)
	}

	// And no trigger anywhere is left disabled.
	var disarmed int
	if err := s1.QueryRow(ctx, `SELECT count(*) FROM pg_trigger WHERE NOT tgisinternal AND tgenabled <> 'O'`).Scan(&disarmed); err != nil {
		t.Fatal(err)
	}
	t.Logf("user triggers left disabled in the catalogue: %d", disarmed)
	if disarmed != 0 {
		t.Errorf("%d user triggers are disabled database-wide", disarmed)
	}
}

// LENS B / 3. FORCE A FAILURE. Point the teardown at the RLS-restricted app role
// instead of a superuser: `SET LOCAL session_replication_role` is a superuser-only
// GUC, so the very first statement of the teardown must fail. What matters is
// (a) it returns an error rather than swallowing it, (b) the transaction rolls
// back, and (c) the company is left WHOLE, not half-deleted.
//
// Runs as a subprocess-free test by exporting TESTDB_ADMIN_DSN before the pool
// is ever built — see TestMain-free ordering note in the harness: this test must
// be the ONLY test in its binary, so it is run with -run.
func TestFailure_NonSuperuserTeardownErrorsAndRollsBack(t *testing.T) {
	ctx := context.Background()
	admin := adminConn(t)

	cid := newID(t)
	if err := Seed(ctx, admin, cid); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() {
		// Torn down with a REAL admin connection at the end, whatever happened.
		c, err := pgx.Connect(ctx, AdminDSN)
		if err != nil {
			t.Errorf("cleanup connect: %v", err)
			return
		}
		defer c.Close(ctx)
		if err := dropDirect(ctx, c, cid); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})

	t.Setenv("TESTDB_ADMIN_DSN", AppDSN)
	err := testdb.Drop(ctx, cid)
	if err == nil {
		t.Fatal("Drop as the NON-SUPERUSER app role returned nil — a teardown that cannot " +
			"disarm the immutability triggers must not report success")
	}
	t.Logf("Drop as tally_app -> %v", err)

	// The company must be COMPLETELY intact: not one row removed.
	var counts []string
	for _, tbl := range []string{"company", "account_group", "ledger", "voucher_version", "posting", "acl_rule"} {
		var n int64
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM `+tbl+` WHERE company_id=$1`, cid).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", tbl, err)
		}
		counts = append(counts, tbl+"="+itoa(n))
		if n != 1 {
			t.Errorf("after a FAILED teardown, %s has %d rows for the company, want 1 — the teardown was not atomic", tbl, n)
		}
	}
	t.Logf("after the failed teardown the company is whole: %s", strings.Join(counts, " "))
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// dropDirect removes a lensb fixture company without going through testdb (used
// when TESTDB_ADMIN_DSN has been repointed).
func dropDirect(ctx context.Context, c *pgx.Conn, cid uuid.UUID) error {
	tx, err := c.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM statutory_step WHERE workflow_id LIKE 'irn:'||$1||':%'`, cid.String()); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT c.table_name FROM information_schema.columns c JOIN information_schema.tables t
		ON t.table_schema=c.table_schema AND t.table_name=c.table_name
		WHERE c.table_schema='public' AND c.column_name='company_id' AND t.table_type='BASE TABLE' AND c.table_name <> 'company'`)
	if err != nil {
		return err
	}
	var tables []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return err
		}
		tables = append(tables, s)
	}
	rows.Close()
	for _, tbl := range tables {
		if tbl == "insightd_draft" {
			if _, err := tx.Exec(ctx, `DELETE FROM insightd_draft WHERE company_id=$1`, cid.String()); err != nil {
				return err
			}
			continue
		}
		if _, err := tx.Exec(ctx, `DELETE FROM `+tbl+` WHERE company_id=$1`, cid); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM company WHERE company_id=$1`, cid); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
