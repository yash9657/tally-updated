package migrations

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func privateMigrationDB(t *testing.T) *pgx.Conn {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("CORED_SKIP_DB")
	}
	dsn := os.Getenv("TESTDB_ADMIN_DSN")
	if dsn == "" {
		dsn = os.Getenv("ADMIN_DATABASE_URL")
	}
	if dsn == "" {
		dsn = "postgres://postgres:postgres@localhost:5433/tally?sslmode=disable"
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("tally_upgrade_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer admin.Close(context.Background())
		if _, err := admin.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	})
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Database = name
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(context.Background()) })
	return conn
}

func TestSecurityFixesReachAlreadyMigratedDatabases(t *testing.T) {
	ctx := context.Background()
	for _, old := range []bool{false, true} {
		t.Run(fmt.Sprintf("old0099=%t", old), func(t *testing.T) {
			conn := privateMigrationDB(t)
			if old {
				if err := ensureMigrationsTable(ctx, conn); err != nil {
					t.Fatal(err)
				}
				names, _ := Names()
				for _, name := range names {
					if name >= "0110" {
						break
					}
					body, _ := files.ReadFile(name)
					if err := applyOne(ctx, conn, strings.TrimSuffix(name, ".sql"), string(body)); err != nil {
						t.Fatal(err)
					}
				}
				// Restore the old executable body without changing its recorded version.
				if _, err := conn.Exec(ctx, `CREATE OR REPLACE FUNCTION agent.tier_b_plan(p_sql TEXT) RETURNS JSON
				LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = pg_catalog AS $$
				DECLARE r RECORD; BEGIN EXECUTE 'EXPLAIN (FORMAT JSON) ' || p_sql INTO r; RETURN r."QUERY PLAN"; END $$;`); err != nil {
					t.Fatal(err)
				}
				var oldDefinition string
				if err := conn.QueryRow(ctx, `SELECT pg_get_functiondef('agent.tier_b_plan(text)'::regprocedure)`).Scan(&oldDefinition); err != nil || !strings.Contains(oldDefinition, "INTO r") {
					t.Fatalf("old function was not installed: %v", err)
				}
			}
			if err := Apply(ctx, conn); err != nil {
				t.Fatal(err)
			}
			if err := Apply(ctx, conn); err != nil {
				t.Fatalf("second migration apply: %v", err)
			}
			var owner string
			var publicAllowed bool
			if err := conn.QueryRow(ctx, `SELECT pg_get_userbyid(proowner), EXISTS (SELECT 1 FROM aclexplode(proacl) WHERE grantee = 0 AND privilege_type = 'EXECUTE') FROM pg_proc WHERE oid = 'agent.tier_b_plan(text)'::regprocedure`).Scan(&owner, &publicAllowed); err != nil {
				t.Fatal(err)
			}
			if owner != "agent_ro" || publicAllowed {
				t.Fatalf("plan privileges: owner=%s public=%t", owner, publicAllowed)
			}
			for _, sql := range []string{"SELECT 1", "SELECT 1; PREPARE upgrade_escape AS SELECT 42; SELECT 2"} {
				tx, err := conn.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				for _, statement := range []string{"SET LOCAL app.current_company_id = '00000000-0000-4000-8000-000000000001'", "SELECT agent.tier_b_bind()", "SET LOCAL transaction_read_only = on", "SET LOCAL ROLE agent_ro"} {
					if _, err := tx.Exec(ctx, statement); err != nil {
						t.Fatal(err)
					}
				}
				var result any
				err = tx.QueryRow(ctx, "SELECT agent.tier_b_plan($1)", sql).Scan(&result)
				tx.Rollback(ctx)
				if sql == "SELECT 1" && err != nil {
					t.Fatalf("valid query refused: %v", err)
				}
				pgErr, rejected := err.(*pgconn.PgError)
				if sql != "SELECT 1" && (!rejected || pgErr.Code != "42P11") {
					t.Fatalf("multi-statement plan: %v", err)
				}
			}
			var planted int
			if err := conn.QueryRow(ctx, "SELECT count(*) FROM pg_prepared_statements WHERE name = 'upgrade_escape'").Scan(&planted); err != nil || planted != 0 {
				t.Fatalf("session modified: count=%d err=%v", planted, err)
			}
		})
	}
}
