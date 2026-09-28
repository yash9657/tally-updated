package tierb_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/internal/tierb"
)

// tierb_tamper_db_test.go is the proof that migration 0099 closed the Tier B
// residual: raw, UNVALIDATED SQL — the validator assumed broken — sent
// through the production path (tierb.RunRawAsAgent is RunQuery minus Parse)
// cannot leave its tenant, by any of the doors 0097 left open.
//
// ⛔ EVERY ATTACK HAS A CONTROL. An attack that "found nothing" proves
// nothing unless the same text, aimed at the tenant it runs as, DOES find
// the data. So each succeeding attack runs twice: as B aimed at A (must
// equal B's untampered answer and carry no marker), and as A aimed at A
// (must carry A's marker). Each refused attack must be refused for the
// reason named — a typo'd attack that fails to parse would otherwise pass.
//
// Outcomes are carried out of every transaction and asserted after it
// (HANDOFF §7.1: never Fatal inside a tx closure).

type attack struct {
	name string
	sql  string // {A} is replaced by the target company id
	// refused: the error must contain this. Empty: the attack must RUN, and
	// then the differential + marker oracle applies.
	refused string
	// baseline: for a running attack, the untampered query whose answer
	// under B the tampered one must equal. Empty: no-marker only.
	baseline string
}

var tamperAttacks = []attack{
	{name: "set_config GUC, join a view",
		sql:      `SELECT count(*) FROM (SELECT set_config('app.current_company_id', '{A}', true)) s, agent.ledgers l WHERE l.ledger_name LIKE '%ZZSECRET%'`,
		baseline: `SELECT count(*) FROM agent.ledgers l WHERE l.ledger_name LIKE '%ZZSECRET%'`},
	{name: "set_config GUC session-wide (is_local=false)",
		sql:      `SELECT count(*) FROM (SELECT set_config('app.current_company_id', '{A}', false)) s, agent.items i WHERE i.item_name LIKE '%ZZSECRET%'`,
		baseline: `SELECT count(*) FROM agent.items i WHERE i.item_name LIKE '%ZZSECRET%'`},
	{name: "set_config GUC in the select list, read in a later column",
		sql: `SELECT set_config('app.current_company_id', '{A}', true), query_to_xml('select ledger_name from agent.ledgers', true, false, '')::text`},
	{name: "query_to_xml after the tamper, as a scalar subquery",
		sql: `SELECT (SELECT query_to_xml('select party_name, voucher_no from agent.vouchers', true, false, '')::text) FROM (SELECT set_config('app.current_company_id', '{A}', true)) s`},
	{name: "a nested tier_b_run under a tampered GUC",
		sql: `SELECT r::text FROM (SELECT set_config('app.current_company_id', '{A}', true)) s, agent.tier_b_run('SELECT ledger_name FROM agent.ledgers', 100) r`},
	{name: "empty GUC, then read (default-deny, never all rows)",
		sql: `SELECT query_to_xml('select ledger_name from agent.ledgers', true, false, '')::text FROM (SELECT set_config('app.current_company_id', '', true)) s`},

	{name: "SET the GUC", sql: `SET app.current_company_id = '{A}'`, refused: "cannot open SET query as cursor"},
	{name: "SET LOCAL the GUC", sql: `SET LOCAL app.current_company_id = '{A}'`, refused: "cannot open SET query as cursor"},
	{name: "RESET the GUC", sql: `RESET app.current_company_id`, refused: "cannot open RESET query as cursor"},
	{name: "RESET ALL", sql: `RESET ALL`, refused: "cannot open RESET query as cursor"},
	{name: "RESET ROLE", sql: `RESET ROLE`, refused: "cannot open RESET query as cursor"},
	{name: "SET ROLE tally_app", sql: `SET ROLE tally_app`, refused: "cannot open SET query as cursor"},
	{name: "set_config role (the measured escape)", sql: `SELECT set_config('role', 'tally_app', true), current_user`,
		refused: `cannot set parameter "role" within security-definer function`},
	{name: "set_config role + GUC + base table via query_to_xml",
		sql:     `SELECT set_config('role', 'tally_app', true), set_config('app.current_company_id', '{A}', true), query_to_xml('select name from public.ledger', true, false, '')::text`,
		refused: `cannot set parameter "role" within security-definer function`},
	{name: "set_config role to NONE", sql: `SELECT set_config('role', 'none', true)`,
		refused: `cannot set parameter "role" within security-definer function`},
	{name: "set_config session_authorization", sql: `SELECT set_config('session_authorization', 'tally_app', true)`,
		refused: `cannot set parameter "session_authorization" within security-definer function`},
	{name: "DO block", sql: `DO $$ BEGIN PERFORM set_config('app.current_company_id', '{A}', true); END $$`,
		refused: "cannot open DO query as cursor"},
	{name: "two statements", sql: `SELECT 1; SELECT set_config('app.current_company_id', '{A}', true)`,
		refused: "multiple commands"},
	{name: "turn READ ONLY off", sql: `SELECT set_config('transaction_read_only', 'off', true)`,
		refused: "read-write mode must be set before any query"},
	{name: "re-bind as agent_ro", sql: `SELECT agent.tier_b_bind()`, refused: "permission denied"},
	{name: "read the binding table", sql: `SELECT * FROM agent.tier_b_binding`, refused: "permission denied"},
	{name: "write the binding table", sql: `INSERT INTO agent.tier_b_binding VALUES (pg_backend_pid(), pg_current_xact_id(), '{A}') RETURNING 1`,
		refused: "read-only transaction"},
	{name: "base table through query_to_xml", sql: `SELECT query_to_xml('select name from public.ledger', true, false, '')::text`,
		refused: "permission denied"},
	{name: "0074 security definer: lock state by subject", sql: `SELECT * FROM public.auth_session_state('x')`, refused: "permission denied"},
	{name: "0074 security definer: membership oracle", sql: `SELECT public.auth_admin_clear_lock('{A}', '{A}', '{A}')`, refused: "permission denied"},
	{name: "server file", sql: `SELECT pg_read_file('/etc/passwd')`, refused: "permission denied"},
	{name: "PREPARE", sql: `PREPARE p AS SELECT 1`, refused: "cannot open PREPARE query as cursor"},
	{name: "COPY out", sql: `COPY (SELECT ledger_name FROM agent.ledgers) TO STDOUT`, refused: "cannot open COPY query as cursor"},
}

// TestRawSQLCannotLeaveItsTenant is the inverted residual pin: before 0099
// the first attack returned tenant A's rows to tenant B, and "set_config role"
// returned current_user = tally_app (d122 "Wave 2" has both measurements).
func TestRawSQLCannotLeaveItsTenant(t *testing.T) {
	pool := setup(t)
	a := newTenant(t, pool, "TierB TamperA")
	seedA(t, a)
	b := newTenant(t, pool, "TierB TamperB")
	aim := func(sql string) string { return strings.ReplaceAll(sql, "{A}", a.cid.String()) }

	ran, refused := 0, 0
	for _, at := range tamperAttacks {
		res, err := tierb.RunRawAsAgent(b.ctx, pool, aim(at.sql))
		if at.refused != "" {
			if err == nil || !strings.Contains(err.Error(), at.refused) {
				t.Errorf("%s: want refused with %q, got err=%v leaked A's marker=%v rows=%.200v", at.name, at.refused, err, leaks(res), res.Rows)
				continue
			}
			refused++
			continue
		}
		if err != nil {
			t.Errorf("%s: the attack must RUN (so its answer can be judged), got %v", at.name, err)
			continue
		}
		ran++
		if leaks(res) {
			t.Errorf("CROSS-TENANT LEAK (%s): tenant B read tenant A's marker: %.300v", at.name, res.Rows)
		}
		if at.baseline != "" {
			base, err := tierb.RunRawAsAgent(b.ctx, pool, at.baseline)
			if err != nil || canonicalRows(base) != canonicalRows(res) {
				t.Errorf("CROSS-TENANT LEAK (%s): B's answer moved under the tamper: %v vs untampered %v (%v)", at.name, res.Rows, base.Rows, err)
			}
		}
		// The control: the same text, run as A and aimed at A, finds A.
		ctl, err := tierb.RunRawAsAgent(a.ctx, pool, aim(at.sql))
		if at.name == "empty GUC, then read (default-deny, never all rows)" {
			// Aimed at nobody: A must see NOTHING either (the GUC policy
			// still holds; the binding does not widen it).
			if err != nil || leaks(ctl) {
				t.Errorf("%s as A: an empty GUC must read nothing, got err=%v leak=%v", at.name, err, leaks(ctl))
			}
			continue
		}
		if err != nil || !(leaks(ctl) || (len(ctl.Rows) == 1 && len(ctl.Rows[0]) == 1 && ctl.Rows[0][0] != "0")) {
			t.Errorf("%s: VACUOUS — the same attack as A aimed at A finds nothing (err=%v rows=%.200v)", at.name, err, ctl.Rows)
		}
	}
	// Every view: B re-pointed at A reads NOTHING — the GUC policy now admits
	// A's rows and the binding admits B's, and the two never meet. (Not "B's
	// own count": B's own rows fall out too, and that is the point.) The
	// control is that A, unbound from nothing, counts rows in every view.
	for _, v := range tierb.Views {
		tampered, err1 := tierb.RunRawAsAgent(b.ctx, pool, aim(`SELECT count(*) FROM (SELECT set_config('app.current_company_id', '{A}', true)) s, agent.`+v.Name))
		own, err2 := tierb.RunRawAsAgent(b.ctx, pool, `SELECT count(*) FROM agent.`+v.Name)
		asA, err3 := tierb.RunRawAsAgent(a.ctx, pool, `SELECT count(*) FROM agent.`+v.Name)
		if err1 != nil || err2 != nil || err3 != nil {
			t.Errorf("agent.%s: %v / %v / %v", v.Name, err1, err2, err3)
			continue
		}
		if tampered.Rows[0][0] != "0" {
			t.Errorf("CROSS-TENANT LEAK on agent.%s: B re-pointed at A counts %s rows, want 0 (B's own: %s, A's: %s)",
				v.Name, tampered.Rows[0][0], own.Rows[0][0], asA.Rows[0][0])
		}
		if asA.Rows[0][0] == "0" || asA.Rows[0][0] == own.Rows[0][0] {
			t.Errorf("agent.%s: A and B count the same %s rows — the per-view probe cannot tell them apart", v.Name, own.Rows[0][0])
		}
	}
	t.Logf("raw tamper suite: %d attacks refused for the named reason, %d ran and returned only tenant B's own answer (each with a live control), %d views differential",
		refused, ran, len(tierb.Views))
}

// TestStatementTimeoutCannotBeTalkedAway: raw text may set_config the
// server-side timeout to 0. Measured: that does not reach the statement it is
// in (the timer is armed when the statement starts), so the 5 s server
// timeout still fires; the Go context deadline (timeout + 2 s) is the
// backstop if a future path ever let text run a statement of its own.
func TestStatementTimeoutCannotBeTalkedAway(t *testing.T) {
	pool := setup(t)
	b := newTenant(t, pool, "TierB Timeout")
	start := time.Now()
	_, err := tierb.RunRawAsAgent(b.ctx, pool, `SELECT set_config('statement_timeout', '0', true), pg_sleep(30)`)
	took := time.Since(start)
	if err == nil || took > 12*time.Second {
		t.Errorf("a 30 s sleep with the timeout switched off ran for %v (err=%v); the context deadline must stop it near 7 s", took.Round(time.Millisecond), err)
	}
	t.Logf("timeout switched off in SQL: stopped after %v by %v", took.Round(100*time.Millisecond), err)
}

// TestTheBindingCannotBeMovedOrSkipped exercises the binding functions
// directly, as tally_app (the only role that may call bind).
func TestTheBindingCannotBeMovedOrSkipped(t *testing.T) {
	pool := setup(t)
	a := newTenant(t, pool, "TierB BindA")
	b := newTenant(t, pool, "TierB BindB")

	// 1. A second bind in one transaction — even by tally_app, even after
	//    the GUC is re-pointed — is a duplicate key, not a re-bind.
	var second error
	_ = pool.RunInTx(b.ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT agent.tier_b_bind()`); err != nil {
			second = err
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.current_company_id', $1, true)`, a.cid.String()); err != nil {
			second = err
			return err
		}
		_, second = tx.Exec(ctx, `SELECT agent.tier_b_bind()`)
		return errors.New("rollback")
	})
	if second == nil || !strings.Contains(second.Error(), "tier_b_binding_pkey") {
		t.Errorf("a second bind in one transaction must be refused by the key, got %v", second)
	}

	// 2. Bind refuses a READ ONLY transaction (it could not have been first).
	var ro error
	_ = pool.RunInReadOnlyTx(b.ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, ro = tx.Exec(ctx, `SELECT agent.tier_b_bind()`)
		return ro
	})
	if ro == nil || !strings.Contains(ro.Error(), "READ ONLY") {
		t.Errorf("bind inside a read-only transaction: %v", ro)
	}

	// 3. The doors refuse to open unbound, or writable — loudly, never empty.
	var unbound, writable error
	_ = pool.RunInReadOnlyTx(b.ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL ROLE agent_ro`); err != nil {
			unbound = err
			return err
		}
		_, unbound = tx.Exec(ctx, `SELECT * FROM agent.tier_b_run('SELECT 1', 1)`)
		return unbound
	})
	if unbound == nil || !strings.Contains(unbound.Error(), "no tenant binding") {
		t.Errorf("tier_b_run in an unbound transaction: %v", unbound)
	}
	_ = pool.RunInTx(b.ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT agent.tier_b_bind()`); err != nil {
			writable = err
			return err
		}
		if _, err := tx.Exec(ctx, `SET LOCAL ROLE agent_ro`); err != nil {
			writable = err
			return err
		}
		_, writable = tx.Exec(ctx, `SELECT * FROM agent.tier_b_run('SELECT 1', 1)`)
		return errors.New("rollback")
	})
	if writable == nil || !strings.Contains(writable.Error(), "READ ONLY") {
		t.Errorf("tier_b_run in a writable transaction: %v", writable)
	}

	// 4. The views read NOTHING in an unbound transaction even with a correct
	//    GUC — the restrictive policy is not satisfied by the GUC alone. (Read
	//    as agent_ro directly: this is the property, not the production path.)
	var n int64 = -1
	var viewErr error
	_ = pool.RunInReadOnlyTx(a.ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL ROLE agent_ro`); err != nil {
			viewErr = err
			return err
		}
		viewErr = tx.QueryRow(ctx, `SELECT count(*) FROM agent.ledgers`).Scan(&n)
		return viewErr
	})
	if viewErr != nil || n != 0 {
		t.Errorf("unbound read of agent.ledgers as A: n=%d err=%v, want 0 rows", n, viewErr)
	}

	// 5. Nothing a Tier-B call does commits: the table is empty afterwards.
	if _, err := tierb.Run(a.ctx, pool, `SELECT count(*) FROM ledgers`, tierb.Options{}); err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.Connect(context.Background(), testdb.AdminDSNValue())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	var left int64
	if err := admin.QueryRow(context.Background(), `SELECT count(*) FROM agent.tier_b_binding WHERE company_id = ANY($1)`,
		[]string{a.cid.String(), b.cid.String()}).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Errorf("%d binding rows committed; a Tier-B transaction must always roll back", left)
	}
}

// TestNoSecurityDefinerDoorForAgentRo: a security-definer function runs as
// its owner — for a superuser owner, without RLS — so every one agent_ro may
// execute is a door around both walls. The census is exact.
func TestNoSecurityDefinerDoorForAgentRo(t *testing.T) {
	setup(t)
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, testdb.AdminDSNValue())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	rows, err := admin.Query(ctx, `
		SELECT n.nspname || '.' || p.proname
		  FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		 WHERE p.prosecdef AND n.nspname NOT IN ('pg_catalog', 'information_schema')
		   AND has_function_privilege('agent_ro', p.oid, 'EXECUTE')
		 ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		got = append(got, s)
	}
	rows.Close()
	want := []string{"agent.tier_b_company", "agent.tier_b_plan", "agent.tier_b_run"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("security-definer functions agent_ro may execute: %v, want exactly %v — a new one needs REVOKE ALL … FROM PUBLIC", got, want)
	}

	// Every base table agent_view_owner may read carries the restrictive
	// binding policy — a view added over a new table without one would read
	// by the GUC alone, which raw SQL can re-point.
	rows, err = admin.Query(ctx, `
		SELECT c.relname,
		       EXISTS (SELECT 1 FROM pg_policy pol
		                WHERE pol.polrelid = c.oid AND pol.polname = 'tier_b_bound' AND NOT pol.polpermissive
		                  AND pol.polroles = ARRAY[(SELECT oid FROM pg_roles WHERE rolname = 'agent_view_owner')]::oid[])
		  FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = 'public' AND c.relkind = 'r'
		   AND has_table_privilege('agent_view_owner', c.oid, 'SELECT')
		 ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	var tables, missing []string
	for rows.Next() {
		var name string
		var ok bool
		if err := rows.Scan(&name, &ok); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
		if !ok {
			missing = append(missing, name)
		}
	}
	rows.Close()
	if len(tables) < 16 {
		t.Errorf("agent_view_owner reads %d base tables; 0097 grants 16 — the census query is wrong", len(tables))
	}
	if len(missing) > 0 {
		t.Errorf("base tables agent_view_owner reads WITHOUT the restrictive tier_b_bound policy: %v", missing)
	}
	t.Logf("security-definer functions agent_ro may execute: %v; %d base tables behind the views, all with tier_b_bound", got, len(tables))
}

// TestOtherSessionsQueryTextIsHidden: agent_ro must not read another
// backend's SQL from pg_stat_activity (a concurrent Tier-B query from
// another tenant carries that tenant's literals). The text also never
// appears there itself: it is a bind parameter.
func TestOtherSessionsQueryTextIsHidden(t *testing.T) {
	pool := setup(t)
	b := newTenant(t, pool, "TierB Activity")
	ctx := context.Background()
	other, err := pgx.Connect(ctx, appDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close(ctx)
	done := make(chan error, 1)
	go func() {
		_, err := other.Exec(ctx, `SELECT pg_sleep(3), 'ZZSECRET-activity'`)
		done <- err
	}()
	admin, err := pgx.Connect(ctx, testdb.AdminDSNValue())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	seen := false
	for i := 0; i < 40 && !seen; i++ {
		var n int64
		_ = admin.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE query LIKE '%ZZSECRET-activity''%'`).Scan(&n)
		seen = n > 0
		if !seen {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if !seen {
		t.Fatal("the control session's query never appeared to the admin — the probe below would be vacuous")
	}
	res, err := tierb.RunRawAsAgent(b.ctx, pool, `SELECT query FROM pg_stat_activity WHERE query LIKE '%ZZSECRET-activity''%'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 0 {
		t.Errorf("agent_ro read another session's SQL from pg_stat_activity: %v", res.Rows)
	}
	all, _ := tierb.RunRawAsAgent(b.ctx, pool, `SELECT query FROM pg_stat_activity WHERE pid <> pg_backend_pid()`)
	var texts []string
	for _, r := range all.Rows {
		if r[0] != "" && r[0] != "<insufficient privilege>" {
			texts = append(texts, r[0])
		}
	}
	sort.Strings(texts)
	if len(texts) != 0 {
		t.Errorf("agent_ro can read %d other backends' query text: %.200v", len(texts), texts)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// TestJSONDecodeRendersLikeTheNativeDriver: moving the text inside
// agent.tier_b_run changed WHERE it runs, and must not change WHAT it
// returns. Every accepted query, as A with A's books: same columns, same
// cells, same refusals, via the JSON path and via a direct statement.
func TestJSONDecodeRendersLikeTheNativeDriver(t *testing.T) {
	pool := setup(t)
	a := newTenant(t, pool, "TierB Render")
	seedA(t, a)
	ran, refused, differ := 0, 0, 0
	for _, q := range tierb.AcceptedCorpus() {
		got, e1 := tierb.RunQuery(a.ctx, pool, q, tierb.Options{MaxCost: 1e12})
		want, e2 := tierb.NativeRunQuery(a.ctx, pool, q, 200)
		switch {
		case (e1 == nil) != (e2 == nil):
			differ++
			if differ <= 5 {
				t.Errorf("one path refused and the other did not: json=%v native=%v\n  %s", e1, e2, q.SQL(200))
			}
		case e1 != nil:
			refused++
		case strings.Join(got.Columns, "\x1f") != strings.Join(want.Columns, "\x1f") || canonicalRows(got) != canonicalRows(want):
			differ++
			if differ <= 5 {
				t.Errorf("render differs:\n  json   %v %v\n  native %v %v\n  %s", got.Columns, got.Rows, want.Columns, want.Rows, q.SQL(200))
			}
		default:
			ran++
		}
	}
	t.Logf("render parity over the accepted corpus as tenant A: %d identical, %d refused by both, %d differ", ran, refused, differ)
	if ran < 500 {
		t.Errorf("only %d queries compared — too thin", ran)
	}
}
