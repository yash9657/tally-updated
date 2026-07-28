package lensb

import (
	"context"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const AdminDSN = "postgres://postgres:postgres@localhost:5433/tally?sslmode=disable"
const AppDSN = "postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable"

type row struct {
	Table string
	SQL   string
	Args  []any
}

// Fixture is a verbatim transcription of buildFixture() from
// internal/platform/testdb/testdb_test.go so this reviewer seeds the same
// one-row-per-table shape without importing an internal test file.
func Fixture(cid uuid.UUID) []row {
	id := func() uuid.UUID { return uuid.New() }
	var (
		groupID, levelID, userID, unitID = id(), id(), id(), id()
		itemID, godownID, batchID        = id(), id(), id()
		ledgerID, vtypeID                = id(), id()
		voucherID, versionID, plistID    = id(), id(), id()
	)
	cidText := cid.String()
	return []row{
		{"company", `INSERT INTO company (id, company_id, name, fy_start_date, books_beginning_from)
			VALUES ($1,$1,'lensb ' || $2, DATE '2024-04-01', DATE '2024-04-01')`, []any{cid, cidText[:8]}},
		{"account_group", `INSERT INTO account_group (id, company_id, name, path, nature, is_pl)
			VALUES ($1,$2,'Fixture Group','fixture'::ltree,'ASSET',false)`, []any{groupID, cid}},
		{"security_level", `INSERT INTO security_level (id, company_id, name) VALUES ($1,$2,'Fixture Level')`, []any{levelID, cid}},
		{"acl_rule", `INSERT INTO acl_rule (id, company_id, security_level_id, effect, action, resource)
			VALUES ($1,$2,$3,'allow','display','report:Fixture')`, []any{id(), cid, levelID}},
		{"company_invite", `INSERT INTO company_invite (id, company_id, subject, security_level_id)
			VALUES ($1,$2,'lensb-subject-' || $3,$4)`, []any{id(), cid, cidText, levelID}},
		{"company_membership", `INSERT INTO company_membership (user_id, company_id, security_level_id) VALUES ($1,$2,$3)`, []any{userID, cid, levelID}},
		{"unit", `INSERT INTO unit (id, company_id, symbol) VALUES ($1,$2,'Nos')`, []any{unitID, cid}},
		{"stock_item", `INSERT INTO stock_item (id, company_id, name, unit_id) VALUES ($1,$2,'Fixture Item',$3)`, []any{itemID, cid, unitID}},
		{"godown", `INSERT INTO godown (id, company_id, name) VALUES ($1,$2,'Fixture Godown')`, []any{godownID, cid}},
		{"stock_batch", `INSERT INTO stock_batch (id, company_id, stock_item_id, name) VALUES ($1,$2,$3,'Fixture Batch')`, []any{batchID, cid, itemID}},
		{"stock_opening", `INSERT INTO stock_opening (id, company_id, stock_item_id, godown_id, batch_id, qty, rate) VALUES ($1,$2,$3,$4,$5,5,10)`, []any{id(), cid, itemID, godownID, batchID}},
		{"ledger", `INSERT INTO ledger (id, company_id, name, group_id) VALUES ($1,$2,'Fixture Ledger',$3)`, []any{ledgerID, cid, groupID}},
		{"ledger_balance", `INSERT INTO ledger_balance (company_id, ledger_id, debit, credit) VALUES ($1,$2,100,0)`, []any{cid, ledgerID}},
		{"ledger_opening_bill", `INSERT INTO ledger_opening_bill (id, company_id, ledger_id, ref_name, dr_cr, amount) VALUES ($1,$2,$3,'OB-1','D',100)`, []any{id(), cid, ledgerID}},
		{"voucher_type", `INSERT INTO voucher_type (id, company_id, name, parent_class) VALUES ($1,$2,'Fixture Sales','Sales')`, []any{vtypeID, cid}},
		{"voucher_number_counter", `INSERT INTO voucher_number_counter (company_id, voucher_type_id, fin_year, next_no) VALUES ($1,$2,2024,2)`, []any{cid, vtypeID}},
		{"voucher_version", `INSERT INTO voucher_version (id, company_id, voucher_id, version_no, op, hash, payload, created_by, voucher_type_id, party_ledger_id, voucher_date)
			VALUES ($1,$2,$3,1,'CREATE','\x00'::bytea,'{}'::jsonb,'lensb',$4,$5, DATE '2024-05-01')`, []any{versionID, cid, voucherID, vtypeID, ledgerID}},
		{"voucher_head", `INSERT INTO voucher_head (company_id, voucher_id, current_version_id, version_no, op, voucher_date) VALUES ($1,$2,$3,1,'CREATE', DATE '2024-05-01')`, []any{cid, voucherID, versionID}},
		{"posting", `INSERT INTO posting (id, company_id, voucher_version_id, ledger_id, dr_cr, amount) VALUES ($1,$2,$3,$4,'D',100)`, []any{id(), cid, versionID, ledgerID}},
		{"bill_allocation", `INSERT INTO bill_allocation (id, company_id, voucher_version_id, ledger_id, ref_name, ref_type, dr_cr, amount) VALUES ($1,$2,$3,$4,'BILL-1','NEW','D',100)`, []any{id(), cid, versionID, ledgerID}},
		{"voucher_gst_line", `INSERT INTO voucher_gst_line (id, company_id, voucher_version_id, line_no, hsn, taxable_value, gst_rate_pct, cgst, sgst, igst) VALUES ($1,$2,$3,1,'1234',100,18,9,9,0)`, []any{id(), cid, versionID}},
		{"stock_movement", `INSERT INTO stock_movement (id, company_id, voucher_version_id, line_no, stock_item_id, godown_id, batch_id, direction, qty, rate, amount) VALUES ($1,$2,$3,1,$4,$5,$6,'IN',3,10,30)`, []any{id(), cid, versionID, itemID, godownID, batchID}},
		{"voucher_external_reference", `INSERT INTO voucher_external_reference (id, company_id, voucher_id, kind, ref_value, pinned_voucher_no) VALUES ($1,$2,$3,'IRN','lensb-irn','F/1')`, []any{id(), cid, voucherID}},
		{"reconciliation_queue", `INSERT INTO reconciliation_queue (id, company_id, voucher_id, attempted_op, kind, ref_value, frozen_voucher_no) VALUES ($1,$2,$3,'ALTER','IRN','lensb-irn','F/1')`, []any{id(), cid, voucherID}},
		{"sync_applied", `INSERT INTO sync_applied (id, company_id, idempotency_key, client_voucher_id, outcome) VALUES ($1,$2,'lensb-key-'||$3,$4,'APPLIED')`, []any{id(), cid, cidText, voucherID}},
		{"master_audit", `INSERT INTO master_audit (id, company_id, entity_type, entity_id, op, after_doc, created_by) VALUES ($1,$2,'ledger',$3,'CREATE','{}'::jsonb,'lensb')`, []any{id(), cid, ledgerID}},
		{"notification_outbox", `INSERT INTO notification_outbox (id, company_id, kind, ref_id, recipient, subject_line, body) VALUES ($1,$2,'invite',$3,'a@example.com','subject','body')`, []any{id(), cid, userID}},
		{"price_list", `INSERT INTO price_list (id, company_id, name, party_group_id, effective) VALUES ($1,$2,'Fixture List',$3, daterange(DATE '2024-04-01', DATE '2025-04-01','[)'))`, []any{plistID, cid, groupID}},
		{"price_list_entry", `INSERT INTO price_list_entry (id, company_id, price_list_id, stock_item_id, qty_range, rate, discount_pct) VALUES ($1,$2,$3,$4, numrange(0,NULL,'[)'),10,0)`, []any{id(), cid, plistID, itemID}},
		{"insightd_draft", `INSERT INTO insightd_draft (company_id, draft_id, body) VALUES ($1,'lensb-draft','{}'::jsonb)`, []any{cidText}},
		{"statutory_step", `INSERT INTO statutory_step (workflow_id, step_name, output) VALUES ('irn:' || $1 || ':' || $2, 'build','{}'::jsonb)`, []any{cidText, voucherID.String()}},
	}
}

func Seed(ctx context.Context, conn *pgx.Conn, cid uuid.UUID) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
		return err
	}
	for _, r := range Fixture(cid) {
		if _, err := tx.Exec(ctx, r.SQL, r.Args...); err != nil {
			return fmt.Errorf("seed %s: %w", r.Table, err)
		}
	}
	return tx.Commit(ctx)
}

// Digest returns, for EVERY public base table, "count|md5-of-all-rows". This is
// a whole-database fingerprint: any row added or removed anywhere changes it.
func Digest(ctx context.Context, conn *pgx.Conn) (map[string]string, error) {
	rows, err := conn.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	var tables []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tables = append(tables, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(tables)
	out := map[string]string{}
	for _, t := range tables {
		var n int64
		var h string
		q := fmt.Sprintf(`SELECT count(*), coalesce(md5(string_agg(md5(x::text), '' ORDER BY md5(x::text))),'-') FROM %s x`, t)
		if err := conn.QueryRow(ctx, q).Scan(&n, &h); err != nil {
			return nil, fmt.Errorf("digest %s: %w", t, err)
		}
		out[t] = fmt.Sprintf("%d|%s", n, h)
	}
	return out, nil
}
