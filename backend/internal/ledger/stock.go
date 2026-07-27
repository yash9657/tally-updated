package ledger

// stock.go — THE INVENTORY SIDE OF A POST.
//
// A stock movement is a SIBLING immutable child of voucher_version, exactly
// like voucher_gst_line and bill_allocation. It is written ONLY inside
// post()'s existing transaction, at the same seam the GST-line loop uses, and
// guarded the same way for a DELETE tombstone. Nothing else in this codebase
// may insert into stock_movement.
//
// WHY MOVEMENTS ARE NOT DERIVED FROM POSTINGS (AND VICE VERSA)
// -----------------------------------------------------------
// The two grains genuinely differ. A three-item invoice billed to a single
// Sales ledger is ONE posting and THREE movements; a Delivery Note is THREE
// movements and ZERO postings. Deriving either from the other would force one
// of those two perfectly ordinary documents to be recorded wrongly, so the post
// path writes both from the same input, in the same transaction, and lets them
// disagree in shape while agreeing in fate: they commit together or not at all.
//
// WHY THERE IS NO VALUE HERE
// --------------------------
// `amount` on a movement is the LINE AS ENTERED — an input to valuation, not a
// valuation. Stock VALUE is never stored: it is derived at report time by
// replaying this ledger through a pure valuer, because valuation is
// order-dependent (a back-dated inward re-values every later outward, and an
// ALTER of an April invoice changes the July closing value). See 0025's header.

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// Direction is the side of a stock movement. It mirrors DrCr: there are two
// values, there will only ever be two values, and the sign of a quantity
// belongs in a named column rather than in a negative number that a careless
// SUM can silently flip. Qty is therefore ALWAYS POSITIVE and Direction carries
// the sign. It binds to the stock_direction enum as a plain Go string, exactly
// as DrCr binds to dr_cr.
type Direction string

const (
	// Inward increases the position (purchase, production, transfer in).
	Inward Direction = "IN"
	// Outward decreases the position (sale, consumption, transfer out).
	Outward Direction = "OUT"
)

var validDirections = map[Direction]bool{Inward: true, Outward: true}

// InventoryLine is ONE inventory line of a voucher, in input form. It is the
// wire shape the F8/F9 item grid, the importer and the offline sequencer all
// build; the post path turns each one into exactly one stock_movement row on
// the version it is writing.
//
// GodownID and BatchID are OPTIONAL dimensions: uuid.Nil means "not tracked on
// this line" and is written as SQL NULL, which (per the composite FK's MATCH
// SIMPLE semantics) also suppresses that dimension's FK check. A company that
// never enables multi-location or batches is therefore not forced to invent
// master rows to record a movement.
//
// Every numeric field is a decimal.Decimal. Quantity is money-like here for the
// same reason money is: 0.1 + 0.2 must be 0.3, and a float quantity would make
// a closing stock balance depend on the order the lines were added.
type InventoryLine struct {
	StockItemID uuid.UUID
	// GodownID is the location; uuid.Nil writes NULL.
	GodownID uuid.UUID
	// BatchID is the batch/lot; uuid.Nil writes NULL.
	BatchID uuid.UUID
	// Direction carries the sign. Qty must be strictly positive.
	Direction   Direction
	Qty         decimal.Decimal
	Rate        decimal.Decimal
	DiscountPct decimal.Decimal
	// Amount is the line total AS ENTERED. It is deliberately not recomputed
	// from qty * rate here: Tally lets a user override a line amount, and
	// silently "correcting" it would make the printed invoice disagree with the
	// stored one.
	Amount decimal.Decimal
}

// InventoryLineView is one inventory line of a FETCHED voucher, carrying the
// REAL item/godown/batch uuids alongside their resolved names.
//
// The uuids are the load-bearing part, for exactly the reason PostingView
// carries LedgerID: the alter path re-seeds a form from a fetched voucher and
// then posts it back. A name-only projection would force the caller to
// re-resolve names to ids — and a name that no longer resolves (renamed item,
// two batches called 'B-01' under different items) would silently drop the
// line, turning an edit of the narration into an inventory write-off.
type InventoryLineView struct {
	LineNo      int
	StockItemID uuid.UUID
	ItemName    string
	GodownID    uuid.UUID
	GodownName  string
	BatchID     uuid.UUID
	BatchName   string
	Direction   string
	Qty         decimal.Decimal
	Rate        decimal.Decimal
	DiscountPct decimal.Decimal
	Amount      decimal.Decimal
}

// NegativeStockError is returned when a post would drive an (item, godown,
// batch) position below zero and the company has not opted in to negative
// stock.
//
// THE DEFERRED CONSTRAINT TRIGGER stock_movement_not_negative IS THE AUTHORITY.
// This error exists only so the refusal NAMES the item, the location and the
// resulting balance in the user's own vocabulary. Without it the user sees the
// database's message, which is correct but arrives as a raw SQLSTATE P0001 the
// HTTP layer cannot classify — and a data-entry operator who is told "closing
// balance would be -10" without being told OF WHAT cannot fix the entry.
// If the two ever disagree, the database wins; that is the point of putting the
// authority where a bug in (or a bypass of) this layer cannot reach it.
type NegativeStockError struct {
	StockItemID uuid.UUID
	ItemName    string
	GodownName  string
	BatchName   string
	Closing     decimal.Decimal
}

func (e *NegativeStockError) Error() string {
	godown := e.GodownName
	if godown == "" {
		godown = "-"
	}
	batch := e.BatchName
	if batch == "" {
		batch = "-"
	}
	return fmt.Sprintf(
		"ledger: negative stock refused for item %s (godown %s, batch %s): closing balance would be %s",
		e.ItemName, godown, batch, e.Closing.StringFixed(4))
}

// stockPosition is the grain at which a stock balance exists: an item, in a
// place, from a lot. uuid.Nil stands for an untracked dimension, which is how
// the SQL side's NULLS-NOT-DISTINCT / IS NOT DISTINCT FROM treatment reads in
// Go — "no godown" is a position like any other, not an unknown.
type stockPosition struct {
	item   uuid.UUID
	godown uuid.UUID
	batch  uuid.UUID
}

func positionOf(l InventoryLine) stockPosition {
	return stockPosition{item: l.StockItemID, godown: l.GodownID, batch: l.BatchID}
}

// validateInventory checks each line is structurally sound BEFORE any database
// work, so an obviously malformed grid fails with a domain message rather than
// a CHECK-constraint violation. It mirrors validatePostings' contract: this is
// the friendly early check, the table's CHECKs are the backstop.
func validateInventory(ls []InventoryLine) error {
	for i, l := range ls {
		if l.StockItemID == uuid.Nil {
			return fmt.Errorf("ledger: inventory line %d: stock item id is required", i+1)
		}
		if !validDirections[l.Direction] {
			return fmt.Errorf("ledger: inventory line %d: invalid direction %q", i+1, l.Direction)
		}
		// Strictly positive: the direction carries the sign, so a signed
		// quantity would double-count it (an OUT of -5 is an inward).
		if !l.Qty.IsPositive() {
			return fmt.Errorf("ledger: inventory line %d: quantity must be positive (direction carries the sign), got %s",
				i+1, l.Qty.StringFixed(4))
		}
		if l.Rate.IsNegative() {
			return fmt.Errorf("ledger: inventory line %d: rate must be non-negative, got %s", i+1, l.Rate.StringFixed(4))
		}
		if l.Amount.IsNegative() {
			return fmt.Errorf("ledger: inventory line %d: amount must be non-negative, got %s", i+1, l.Amount.StringFixed(4))
		}
		if l.DiscountPct.IsNegative() || l.DiscountPct.GreaterThan(decimal.NewFromInt(100)) {
			return fmt.Errorf("ledger: inventory line %d: discount must be between 0 and 100, got %s",
				i+1, l.DiscountPct.StringFixed(4))
		}
	}
	return nil
}

// nullUUID renders uuid.Nil as SQL NULL. An untracked dimension must be NULL
// rather than the all-zeroes uuid: the zero uuid would fail the composite FK
// (no godown has that id), and even if it did not, it would create a phantom
// "position" that no report could name.
func nullUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

// checkNegativeStock refuses a post that would drive any touched position
// below zero, unless the company has opted in via company.allow_negative_stock.
//
// It runs INSIDE post()'s transaction, before the movements are inserted, and
// it computes the position the voucher WILL leave behind:
//
//	opening
//	  + net movements of every OTHER live voucher's CURRENT version
//	  + net of THIS voucher's new lines
//
// The "every other voucher" exclusion is what makes an ALTER correct. Movements
// are versioned: the superseded version's rows are still in the table and the
// head pointer has not moved yet at this point in the transaction, so counting
// them would compare the new lines against a balance that already contains the
// old ones — and altering "OUT 10" to "OUT 4" would be refused as if it were an
// additional withdrawal of 4. The deferred trigger does not have this problem
// because it fires at COMMIT, after the head has moved; this pre-check has to
// model that future explicitly.
//
// Every TOUCHED position is checked, not only the outward ones, exactly as the
// trigger does (it fires per inserted row regardless of direction). A position
// that is already negative — possible if the company once allowed it and then
// turned the setting off — therefore reports on the next voucher that touches
// it rather than staying silent forever.
func checkNegativeStock(ctx context.Context, tx pgx.Tx, companyID, voucherID uuid.UUID, lines []InventoryLine) error {
	if len(lines) == 0 {
		return nil
	}

	// Fail CLOSED, exactly as the trigger does: an unreadable/absent company row
	// must not be able to hand out the permissive behaviour.
	var allow bool
	switch err := tx.QueryRow(ctx,
		`SELECT allow_negative_stock FROM company WHERE id = $1`, companyID).Scan(&allow); {
	case errors.Is(err, pgx.ErrNoRows):
		allow = false
	case err != nil:
		return fmt.Errorf("ledger: read allow_negative_stock: %w", err)
	}
	if allow {
		return nil
	}

	// Net this voucher's own lines per position, preserving first-seen order so
	// the error names the FIRST offending line rather than a map-random one.
	delta := map[stockPosition]decimal.Decimal{}
	order := []stockPosition{}
	for _, l := range lines {
		p := positionOf(l)
		if _, seen := delta[p]; !seen {
			order = append(order, p)
			delta[p] = decimal.Zero
		}
		if l.Direction == Inward {
			delta[p] = delta[p].Add(l.Qty)
		} else {
			delta[p] = delta[p].Sub(l.Qty)
		}
	}

	for _, p := range order {
		var existing decimal.Decimal
		// The canonical closing-position query (0025's trigger body), minus this
		// voucher: opening + the net of every OTHER live voucher's CURRENT
		// version. IS NOT DISTINCT FROM is required because the dimensions are
		// nullable and "= NULL" would match nothing.
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE((SELECT SUM(so.qty) FROM stock_opening so
			                   WHERE so.company_id    = $1
			                     AND so.stock_item_id = $2
			                     AND so.godown_id IS NOT DISTINCT FROM $3
			                     AND so.batch_id  IS NOT DISTINCT FROM $4), 0)
			      + COALESCE((SELECT SUM(CASE WHEN sm.direction = 'IN' THEN sm.qty ELSE -sm.qty END)
			                    FROM stock_movement sm
			                    JOIN voucher_head vh
			                      ON vh.company_id         = sm.company_id
			                     AND vh.current_version_id = sm.voucher_version_id
			                   WHERE sm.company_id    = $1
			                     AND sm.stock_item_id = $2
			                     AND sm.godown_id IS NOT DISTINCT FROM $3
			                     AND sm.batch_id  IS NOT DISTINCT FROM $4
			                     AND vh.is_deleted = false
			                     AND vh.voucher_id <> $5), 0)`,
			companyID, p.item, nullUUID(p.godown), nullUUID(p.batch), voucherID).Scan(&existing); err != nil {
			return fmt.Errorf("ledger: read closing stock position: %w", err)
		}

		closing := existing.Add(delta[p])
		if closing.IsNegative() {
			e := &NegativeStockError{StockItemID: p.item, Closing: closing}
			// Names, best effort: the refusal is authoritative whether or not the
			// masters can be read, so a lookup failure must not mask it.
			_ = tx.QueryRow(ctx,
				`SELECT name FROM stock_item WHERE company_id = $1 AND id = $2`,
				companyID, p.item).Scan(&e.ItemName)
			if e.ItemName == "" {
				e.ItemName = p.item.String()
			}
			if p.godown != uuid.Nil {
				_ = tx.QueryRow(ctx,
					`SELECT name FROM godown WHERE company_id = $1 AND id = $2`,
					companyID, p.godown).Scan(&e.GodownName)
			}
			if p.batch != uuid.Nil {
				_ = tx.QueryRow(ctx,
					`SELECT name FROM stock_batch WHERE company_id = $1 AND id = $2`,
					companyID, p.batch).Scan(&e.BatchName)
			}
			return e
		}
	}
	return nil
}

// writeStockMovements inserts one stock_movement per inventory line on the
// version being written. It is called from post() ONLY, inside post()'s
// transaction, immediately after the GST-line loop — so a movement can never
// outlive the voucher version it belongs to, and a failed post leaves no stock
// behind.
//
// line_no is the 1-based input position, not a sort of anything: it is part of
// the replay ordering key (voucher_date, voucher_no, version_created_at,
// line_no), so preserving the order the user keyed the grid in is what makes a
// weighted-average replay reproducible.
func writeStockMovements(ctx context.Context, tx pgx.Tx, companyID, versionID uuid.UUID, lines []InventoryLine) error {
	for n, l := range lines {
		moveID, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("ledger: new stock movement id: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO stock_movement
			   (id, company_id, voucher_version_id, line_no, stock_item_id,
			    godown_id, batch_id, direction, qty, rate, discount_pct, amount)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			moveID, companyID, versionID, n+1, l.StockItemID,
			nullUUID(l.GodownID), nullUUID(l.BatchID), string(l.Direction),
			l.Qty, l.Rate, l.DiscountPct, l.Amount,
		); err != nil {
			return fmt.Errorf("ledger: insert stock_movement: %w", err)
		}
	}
	return nil
}

// loadInventoryLines projects the inventory lines of ONE voucher version, with
// real ids plus resolved names, ordered by line_no so the grid comes back in
// the order it was keyed. LEFT JOINs on the dimension masters: a NULL godown or
// batch is a legitimate "not tracked here", not a missing row.
func loadInventoryLines(ctx context.Context, tx pgx.Tx, companyID, versionID uuid.UUID) ([]InventoryLineView, error) {
	rows, err := tx.Query(ctx,
		`SELECT sm.line_no, sm.stock_item_id, COALESCE(si.name, ''),
		        sm.godown_id, COALESCE(g.name, ''),
		        sm.batch_id, COALESCE(b.name, ''),
		        sm.direction::text, sm.qty, sm.rate, sm.discount_pct, sm.amount
		   FROM stock_movement sm
		   LEFT JOIN stock_item  si ON si.company_id = sm.company_id AND si.id = sm.stock_item_id
		   LEFT JOIN godown      g  ON g.company_id  = sm.company_id AND g.id  = sm.godown_id
		   LEFT JOIN stock_batch b  ON b.company_id  = sm.company_id AND b.id  = sm.batch_id
		  WHERE sm.company_id = $1 AND sm.voucher_version_id = $2
		  ORDER BY sm.line_no`,
		companyID, versionID)
	if err != nil {
		return nil, fmt.Errorf("ledger: query stock movements: %w", err)
	}
	defer rows.Close()

	var out []InventoryLineView
	for rows.Next() {
		var (
			l        InventoryLineView
			godownID *uuid.UUID
			batchID  *uuid.UUID
		)
		if err := rows.Scan(&l.LineNo, &l.StockItemID, &l.ItemName,
			&godownID, &l.GodownName, &batchID, &l.BatchName,
			&l.Direction, &l.Qty, &l.Rate, &l.DiscountPct, &l.Amount); err != nil {
			return nil, fmt.Errorf("ledger: scan stock movement: %w", err)
		}
		if godownID != nil {
			l.GodownID = *godownID
		}
		if batchID != nil {
			l.BatchID = *batchID
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ledger: iterate stock movements: %w", err)
	}
	return out, nil
}
