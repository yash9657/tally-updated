package migrations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestCompanyReferencesRejectForeignParents(t *testing.T) {
	ctx := context.Background()
	conn := privateMigrationDB(t)
	if err := Apply(ctx, conn); err != nil {
		t.Fatal(err)
	}
	companies := []uuid.UUID{uuid.New(), uuid.New()}
	groups, ledgers, units := []uuid.UUID{}, []uuid.UUID{}, []uuid.UUID{}
	for _, cid := range companies {
		gid, lid, uid := uuid.New(), uuid.New(), uuid.New()
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO company(id,company_id,name,fy_start_date) VALUES ($1,$1,'reference fixture','2025-04-01')`, []any{cid}},
			{`INSERT INTO account_group(id,company_id,name,path,nature,is_pl) VALUES ($1,$2,'group','fixture','ASSET',false)`, []any{gid, cid}},
			{`INSERT INTO ledger(id,company_id,name,group_id) VALUES ($1,$2,'parent',$3)`, []any{lid, cid, gid}},
			{`INSERT INTO unit(id,company_id,symbol) VALUES ($1,$2,'Nos')`, []any{uid, cid}},
		} {
			if _, err := conn.Exec(ctx, q.sql, q.args...); err != nil {
				t.Fatal(err)
			}
		}
		groups = append(groups, gid)
		ledgers = append(ledgers, lid)
		units = append(units, uid)
	}
	vid := uuid.New()
	if _, err := conn.Exec(ctx, `INSERT INTO voucher_version(id,company_id,voucher_id,version_no,op,hash,payload,created_by) VALUES ($1,$2,$1,1,'CREATE','x','{}','fixture')`, vid, companies[0]); err != nil {
		t.Fatal(err)
	}
	edges := []struct {
		table, field, sql string
		parents           []uuid.UUID
	}{
		{"ledger", "group_id", `INSERT INTO ledger(id,company_id,name,group_id) VALUES ($1,$2,'child',$3)`, groups},
		{"stock_item", "unit_id", `INSERT INTO stock_item(id,company_id,name,unit_id) VALUES ($1,$2,'child',$3)`, units},
		{"voucher_version", "party_ledger_id", `INSERT INTO voucher_version(id,company_id,voucher_id,version_no,op,hash,payload,created_by,party_ledger_id) VALUES ($1,$2,$1,1,'CREATE','x','{}','fixture',$3)`, ledgers},
		{"posting", "ledger_id", fmt.Sprintf(`INSERT INTO posting(id,company_id,voucher_version_id,ledger_id,dr_cr,amount) VALUES ($1,$2,'%s',$3,'D',0)`, vid), ledgers},
		{"bill_allocation", "ledger_id", fmt.Sprintf(`INSERT INTO bill_allocation(id,company_id,voucher_version_id,ledger_id,ref_name,ref_type,dr_cr,amount) VALUES ($1,$2,'%s',$3,'fixture','NEW','D',0)`, vid), ledgers},
	}
	for _, edge := range edges {
		t.Run(edge.table+"."+edge.field, func(t *testing.T) {
			for i, parent := range edge.parents {
				tx, err := conn.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				// Exercise the real app role as well as the structural FK: the parent in
				// company B is hidden by RLS but still cannot become A's reference.
				if _, err = tx.Exec(ctx, `SET LOCAL ROLE tally_app`); err != nil {
					t.Fatal(err)
				}
				if _, err = tx.Exec(ctx, `SELECT set_config('app.current_company_id',$1,true)`, companies[0].String()); err != nil {
					t.Fatal(err)
				}
				_, err = tx.Exec(ctx, edge.sql, uuid.New(), companies[0], parent)
				if err == nil {
					_, err = tx.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`)
				}
				tx.Rollback(ctx)
				var pgerr *pgconn.PgError
				if i == 0 && err != nil {
					t.Fatalf("same company refused: %v", err)
				}
				if i == 1 && (!errors.As(err, &pgerr) || pgerr.Code != "23503") {
					t.Fatalf("foreign parent not rejected by FK: %v", err)
				}
			}
		})
	}
	// Simulate an older installation with an inconsistent ledger reference.
	// The forward migration must stop with a useful audit, preserving the row.
	if _, err := conn.Exec(ctx, `ALTER TABLE ledger DROP CONSTRAINT ledger_group_company_fk`); err != nil {
		t.Fatal(err)
	}
	bad := uuid.New()
	if _, err := conn.Exec(ctx, `INSERT INTO ledger(id,company_id,name,group_id) VALUES ($1,$2,'old inconsistent row',$3)`, bad, companies[0], groups[1]); err != nil {
		t.Fatal(err)
	}
	body, _ := files.ReadFile("0111_company_scoped_references.sql")
	if _, err := conn.Exec(ctx, string(body)); err == nil || !strings.Contains(err.Error(), "ledger.group_id has 1 inconsistent rows") {
		t.Fatalf("missing audit: %v", err)
	}
	var parent uuid.UUID
	if err := conn.QueryRow(ctx, `SELECT group_id FROM ledger WHERE id=$1`, bad).Scan(&parent); err != nil || parent != groups[1] {
		t.Fatalf("audit changed existing books: %s %v", parent, err)
	}
}
