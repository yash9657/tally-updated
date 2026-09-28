package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/agent"
)

// agent_effect.go reads WHAT A HANDLER'S TRANSACTION WROTE, from inside that
// transaction, before it commits or (for a preview) rolls back. It is the one
// source of every figure the approver's diff shows and of what a commit
// records as written (W2 breadth, D121 "Wave 2 breadth").
//
// ⛔ READ BACK, NEVER DERIVED FROM THE REQUEST. The body the model assembled is
// a claim; the rows the real handler wrote are the fact. An item invoice's
// postings, a physical-stock voucher's correction, an alter's before-image and
// a master's audit row all exist only after the handler ran, and a diff built
// from the body would show the approver the model's arithmetic instead of the
// books'.
//
// ⚠️ "WRITTEN BY THIS TRANSACTION" IS ASKED THROUGH xmin, and the comparison is
// on the low 32 bits: xmin is a 32-bit xid while txid_current_if_assigned() is
// epoch-extended. txid_current_if_assigned() is NULL for a transaction that
// has written nothing — the middleware's read transactions — so the read is a
// no-op there without a second query. It is asked on the tables whose rows a write CREATES or UPDATES (voucher_version,
// master_audit, order_head, order_line, bank_reconciliation). Rows hanging off
// a version (posting, stock_movement) are then read by that version's id, which
// is indexed. A DELETE leaves no row to see; the one write here that deletes (a
// bank-reconciliation un-tick) is read BEFORE the handler runs instead — see
// bankTickBefore.
//
// ⚠️ THE xmin SCANS ARE PER COMPANY AND UNINDEXED. They run once per agent
// preview or commit, never on a person's request; D121 records the cost.

const writtenBy = `txid_current_if_assigned() IS NOT NULL
	   AND xmin::text = (txid_current_if_assigned() % 4294967296)::text`

// txEffect is what one transaction wrote.
type txEffect struct {
	effect  agent.Effect
	kind    agent.ObjectKind
	objects []uuid.UUID
	// versions carries, for the commit hook, each ALTER's new version number.
	versions []agent.VersionWritten
}

// readTxEffect reads everything the current transaction wrote. before is the
// bank-reconciliation state read ahead of the handler (nil if the request is
// not a bank-reconciliation write).
func readTxEffect(ctx context.Context, tx pgx.Tx, companyID uuid.UUID, before *bankTick) (txEffect, error) {
	var out txEffect
	var assigned *int64
	if err := tx.QueryRow(ctx, `SELECT txid_current_if_assigned()`).Scan(&assigned); err != nil {
		return out, err
	}
	if assigned == nil {
		return out, nil // a read transaction (the middleware's): it wrote nothing
	}

	// 1. voucher versions.
	type ver struct {
		id uuid.UUID
		agent.VersionWritten
	}
	var vers []ver
	rows, err := tx.Query(ctx, `
		SELECT id, voucher_id, version_no, op::text, COALESCE(payload->>'class', ''),
		       COALESCE(voucher_no, ''), COALESCE(to_char(voucher_date, 'YYYY-MM-DD'), '')
		  FROM voucher_version
		 WHERE company_id = $1 AND `+writtenBy+`
		 ORDER BY voucher_id, version_no`, companyID)
	if err != nil {
		return out, fmt.Errorf("read versions written: %w", err)
	}
	for rows.Next() {
		var v ver
		if err := rows.Scan(&v.id, &v.VoucherID, &v.VersionNo, &v.Op, &v.Class, &v.VoucherNo, &v.Date); err != nil {
			rows.Close()
			return out, err
		}
		vers = append(vers, v)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	seenVoucher := map[uuid.UUID]bool{}
	var newIDs []uuid.UUID
	for _, v := range vers {
		out.effect.Versions = append(out.effect.Versions, v.VersionWritten)
		out.versions = append(out.versions, v.VersionWritten)
		newIDs = append(newIDs, v.id)
		if !seenVoucher[v.VoucherID] {
			seenVoucher[v.VoucherID] = true
			out.objects = append(out.objects, v.VoucherID)
		}
	}
	out.effect.Vouchers = len(seenVoucher)
	if len(vers) > 0 {
		out.kind = agent.ObjectVoucher
		after, err := postingsOf(ctx, tx, companyID, newIDs)
		if err != nil {
			return out, err
		}
		for _, id := range newIDs {
			for _, p := range after[id] {
				out.effect.Postings = append(out.effect.Postings, p)
				if p.DrCr == "D" {
					out.effect.DebitPaise += p.AmountPaise
				} else {
					out.effect.CreditPaise += p.AmountPaise
				}
			}
		}
		// The before-image of every ALTER: the version it supersedes, which is
		// version_no − 1 of the same logical voucher, untouched.
		priorIDs := map[uuid.UUID]agent.VersionRef{}
		var priorKeys []uuid.UUID
		for _, v := range vers {
			if v.Op != "ALTER" {
				continue
			}
			var pid uuid.UUID
			if err := tx.QueryRow(ctx,
				`SELECT id FROM voucher_version WHERE company_id = $1 AND voucher_id = $2 AND version_no = $3`,
				companyID, v.VoucherID, v.VersionNo-1).Scan(&pid); err != nil {
				return out, fmt.Errorf("read superseded version of %s: %w", v.VoucherID, err)
			}
			priorIDs[pid] = agent.VersionRef{VoucherID: v.VoucherID, VersionNo: v.VersionNo - 1}
			priorKeys = append(priorKeys, pid)
		}
		if len(priorKeys) > 0 {
			before, err := postingsOf(ctx, tx, companyID, priorKeys)
			if err != nil {
				return out, err
			}
			net := map[uuid.UUID]*agent.LedgerDelta{}
			var order []uuid.UUID
			touch := func(p agent.Posting) *agent.LedgerDelta {
				d, ok := net[p.LedgerID]
				if !ok {
					d = &agent.LedgerDelta{LedgerID: p.LedgerID, Ledger: p.Ledger}
					net[p.LedgerID] = d
					order = append(order, p.LedgerID)
				}
				return d
			}
			signed := func(p agent.Posting) int64 {
				if p.DrCr == "D" {
					return p.AmountPaise
				}
				return -p.AmountPaise
			}
			for _, pid := range priorKeys {
				ref := priorIDs[pid]
				ref.Postings = before[pid]
				for _, p := range ref.Postings {
					if p.DrCr == "D" {
						ref.DebitPaise += p.AmountPaise
					} else {
						ref.CreditPaise += p.AmountPaise
					}
					touch(p).BeforePaise += signed(p)
				}
				if ref.Postings == nil {
					ref.Postings = []agent.Posting{}
				}
				out.effect.Replaces = append(out.effect.Replaces, ref)
			}
			altered := map[uuid.UUID]bool{}
			for _, v := range vers {
				if v.Op == "ALTER" {
					altered[v.id] = true
				}
			}
			for _, id := range newIDs {
				if !altered[id] {
					continue
				}
				for _, p := range after[id] {
					touch(p).AfterPaise += signed(p)
				}
			}
			for _, lid := range order {
				d := net[lid]
				d.ChangePaise = d.AfterPaise - d.BeforePaise
				if d.ChangePaise != 0 {
					out.effect.Delta = append(out.effect.Delta, *d)
				}
			}
			sort.SliceStable(out.effect.Delta, func(i, j int) bool { return out.effect.Delta[i].Ledger < out.effect.Delta[j].Ledger })
		}
		voucherOf := map[uuid.UUID]uuid.UUID{}
		for _, v := range vers {
			voucherOf[v.id] = v.VoucherID
		}
		stock, err := stockOf(ctx, tx, companyID, newIDs, voucherOf)
		if err != nil {
			return out, err
		}
		out.effect.Stock = stock
	}

	// 2. master audit rows.
	mrows, err := tx.Query(ctx, `
		SELECT entity_type, entity_id, op::text, before_doc, after_doc
		  FROM master_audit
		 WHERE company_id = $1 AND `+writtenBy+`
		 ORDER BY created_at, id`, companyID)
	if err != nil {
		return out, fmt.Errorf("read master audit written: %w", err)
	}
	for mrows.Next() {
		var m agent.MasterChange
		var b, a []byte
		if err := mrows.Scan(&m.Entity, &m.EntityID, &m.Op, &b, &a); err != nil {
			mrows.Close()
			return out, err
		}
		if len(b) > 0 {
			m.Before = json.RawMessage(b)
		}
		if len(a) > 0 {
			m.After = json.RawMessage(a)
		}
		out.effect.Masters = append(out.effect.Masters, m)
	}
	mrows.Close()
	if err := mrows.Err(); err != nil {
		return out, err
	}
	if out.kind == "" && len(out.effect.Masters) > 0 {
		out.kind = agent.ObjectMaster
		seen := map[uuid.UUID]bool{}
		for _, m := range out.effect.Masters {
			if !seen[m.EntityID] {
				seen[m.EntityID] = true
				out.objects = append(out.objects, m.EntityID)
			}
		}
	}

	// 3. orders.
	orows, err := tx.Query(ctx, `
		SELECT id, direction::text, order_no, to_char(order_date, 'YYYY-MM-DD'), is_closed
		  FROM order_head
		 WHERE company_id = $1 AND `+writtenBy+`
		 ORDER BY order_no, id`, companyID)
	if err != nil {
		return out, fmt.Errorf("read orders written: %w", err)
	}
	for orows.Next() {
		var o agent.OrderChange
		if err := orows.Scan(&o.OrderID, &o.Direction, &o.OrderNo, &o.Date, &o.Closed); err != nil {
			orows.Close()
			return out, err
		}
		out.effect.Orders = append(out.effect.Orders, o)
	}
	orows.Close()
	if err := orows.Err(); err != nil {
		return out, err
	}
	for i := range out.effect.Orders {
		lrows, err := tx.Query(ctx, `
			SELECT ol.line_no, si.name, ol.qty::text, ol.rate::text
			  FROM order_line ol JOIN stock_item si ON si.id = ol.stock_item_id
			 WHERE ol.company_id = $1 AND ol.order_id = $2 AND `+qualified("ol")+`
			 ORDER BY ol.line_no`, companyID, out.effect.Orders[i].OrderID)
		if err != nil {
			return out, fmt.Errorf("read order lines written: %w", err)
		}
		for lrows.Next() {
			var l agent.OrderLine
			if err := lrows.Scan(&l.LineNo, &l.Item, &l.Qty, &l.Rate); err != nil {
				lrows.Close()
				return out, err
			}
			out.effect.Orders[i].Lines = append(out.effect.Orders[i].Lines, l)
		}
		lrows.Close()
		if err := lrows.Err(); err != nil {
			return out, err
		}
	}
	if out.kind == "" && len(out.effect.Orders) > 0 {
		out.kind = agent.ObjectOrder
		for _, o := range out.effect.Orders {
			out.objects = append(out.objects, o.OrderID)
		}
	}

	// 4. bank-reconciliation ticks.
	if before != nil {
		after, err := readBankTick(ctx, tx, before.voucherID, before.ledgerID, before.legNo)
		if err != nil {
			return out, err
		}
		if after != before.date {
			out.effect.BankDates = append(out.effect.BankDates, agent.BankDateChange{VoucherID: before.voucherID,
				LedgerID: before.ledgerID, LegNo: before.legNo, Before: before.date, After: after})
		}
	}
	if out.kind == "" && len(out.effect.BankDates) > 0 {
		out.kind = agent.ObjectBankReconciliation
		for _, b := range out.effect.BankDates {
			out.objects = append(out.objects, b.VoucherID)
		}
	}
	return out, nil
}

func qualified(alias string) string {
	return `txid_current_if_assigned() IS NOT NULL
	   AND ` + alias + `.xmin::text = (txid_current_if_assigned() % 4294967296)::text`
}

// postingsOf reads the legs of the given voucher versions, in the order the
// wave-1 preview used (side, ledger name, amount), keyed by version id.
//
// ⛔ LEFT JOIN, so a leg whose ledger this company cannot see still counts in
// the effect (and in the delegated ceiling's debit) with an empty name, rather
// than vanishing and making a written voucher read as "postings: [], debit 0".
// The ledger service refuses such a leg first (ErrLedgerNotInCompany); this is
// the readback not hiding one if a future writer does not.
func postingsOf(ctx context.Context, tx pgx.Tx, companyID uuid.UUID, versionIDs []uuid.UUID) (map[uuid.UUID][]agent.Posting, error) {
	out := map[uuid.UUID][]agent.Posting{}
	rows, err := tx.Query(ctx, `
		SELECT p.voucher_version_id, p.ledger_id, COALESCE(l.name, ''), p.dr_cr::text, p.amount::text
		  FROM posting p LEFT JOIN ledger l ON l.id = p.ledger_id AND l.company_id = p.company_id
		 WHERE p.company_id = $1 AND p.voucher_version_id = ANY($2)
		 ORDER BY p.voucher_version_id, p.dr_cr, COALESCE(l.name, ''), p.amount`, companyID, versionIDs)
	if err != nil {
		return nil, fmt.Errorf("read postings: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var vid uuid.UUID
		var p agent.Posting
		if err := rows.Scan(&vid, &p.LedgerID, &p.Ledger, &p.DrCr, &p.Amount); err != nil {
			return nil, err
		}
		paise, err := toPaise(p.Amount)
		if err != nil {
			return nil, err
		}
		p.AmountPaise = paise
		out[vid] = append(out[vid], p)
	}
	return out, rows.Err()
}

// stockOf reads the stock movements of the given versions.
func stockOf(ctx context.Context, tx pgx.Tx, companyID uuid.UUID, versionIDs []uuid.UUID, voucherOf map[uuid.UUID]uuid.UUID) ([]agent.StockMove, error) {
	rows, err := tx.Query(ctx, `
		SELECT sm.voucher_version_id, sm.line_no, si.name, COALESCE(g.name, ''), sm.direction::text,
		       sm.qty::text, sm.rate::text, sm.amount::text
		  FROM stock_movement sm
		  JOIN stock_item si ON si.id = sm.stock_item_id
		  LEFT JOIN godown g ON g.id = sm.godown_id
		 WHERE sm.company_id = $1 AND sm.voucher_version_id = ANY($2)
		 ORDER BY sm.voucher_version_id, sm.line_no`, companyID, versionIDs)
	if err != nil {
		return nil, fmt.Errorf("read stock movements: %w", err)
	}
	defer rows.Close()
	var out []agent.StockMove
	for rows.Next() {
		var vid uuid.UUID
		var m agent.StockMove
		if err := rows.Scan(&vid, &m.LineNo, &m.Item, &m.Godown, &m.Direction, &m.Qty, &m.Rate, &m.Amount); err != nil {
			return nil, err
		}
		m.VoucherID = voucherOf[vid]
		out = append(out, m)
	}
	return out, rows.Err()
}

// bankTick is the bank-reconciliation state of one leg, read BEFORE a
// bank-reconciliation write runs: an un-tick DELETEs its row, and a deleted
// row is invisible to the in-transaction read.
type bankTick struct {
	voucherID, ledgerID uuid.UUID
	legNo               int
	date                string // "" = not ticked
}

func readBankTick(ctx context.Context, tx pgx.Tx, voucherID, ledgerID uuid.UUID, legNo int) (string, error) {
	var d string
	err := tx.QueryRow(ctx, `
		SELECT to_char(bank_date, 'YYYY-MM-DD') FROM bank_reconciliation
		 WHERE voucher_id = $1 AND ledger_id = $2 AND leg_no = $3`, voucherID, ledgerID, legNo).Scan(&d)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return d, err
}
