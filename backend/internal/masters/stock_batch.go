package masters

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/platform/db"
)

// --------------------------------------------------------------------------
// StockBatch (batch / lot)
// --------------------------------------------------------------------------

// isoDate is the wire format for the optional batch dates. It matches the
// convention already used for OpeningBill.DueDate: an ISO date, or "" for
// "there isn't one".
const isoDate = "2006-01-02"

// StockBatch is a batch (Tally: "Batch / Lot Number") of ONE stock item.
//
// A batch is a MASTER row, never a free-text column on a movement, and its name
// is scoped to its ITEM rather than to the company. Two different items may
// each carry a batch called 'B-01' and they are unrelated; collapsing them into
// one company-wide name space would silently merge two physical batches with
// different expiry dates the first time a second supplier reuses a numbering
// scheme — which, for a pharma or food tenant, destroys the only thing batches
// exist to record.
//
// MfgDate and ExpiryDate are OPTIONAL and are carried as ISO date strings so
// that "absent" is expressible as "" rather than as a zero time.Time. A zero
// time is a real instant (0001-01-01) and would make every undated batch look
// like the most expired stock in the warehouse to any report that sorts by
// expiry.
type StockBatch struct {
	ID            uuid.UUID
	StockItemName string // the item this batch belongs to; must already exist
	Name          string
	MfgDate       string // ISO date, or "" for none
	ExpiryDate    string // ISO date, or "" for none
}

// CreateStockBatch creates a batch under the named stock item and appends a
// master_audit CREATE row.
func (s *Service) CreateStockBatch(ctx context.Context, b StockBatch, createdBy string) (uuid.UUID, error) {
	if b.Name == "" {
		return uuid.Nil, errors.New("masters: batch name is required")
	}
	if b.StockItemName == "" {
		return uuid.Nil, errors.New("masters: batch stock item is required")
	}
	mfg, err := parseOptionalDate(b.MfgDate, "manufacturing date")
	if err != nil {
		return uuid.Nil, err
	}
	expiry, err := parseOptionalDate(b.ExpiryDate, "expiry date")
	if err != nil {
		return uuid.Nil, err
	}
	// A batch that expires before it was manufactured is a transposed pair of
	// dates, and the damage is silent: the batch reads as already-expired
	// stock forever, so an expiry report either hides live stock or screams
	// about goods that are fine. Refuse it at the door, where the user can
	// still see what they typed.
	if mfg != nil && expiry != nil && expiry.Before(*mfg) {
		return uuid.Nil, fmt.Errorf(
			"masters: batch %q expires %s, before it was manufactured on %s",
			b.Name, b.ExpiryDate, b.MfgDate)
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return uuid.Nil, db.ErrNoCompany
	}
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("masters: new batch id: %w", err)
	}

	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var itemID uuid.UUID
		err := tx.QueryRow(ctx,
			`SELECT id FROM stock_item WHERE name = $1`, b.StockItemName).Scan(&itemID)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("masters: %w: stock item %q", ErrNotFound, b.StockItemName)
		}
		if err != nil {
			return fmt.Errorf("masters: resolve stock item: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO stock_batch
			   (id, company_id, stock_item_id, name, mfg_date, expiry_date)
			 VALUES ($1, $2, $3, $4, NULLIF($5,'')::date, NULLIF($6,'')::date)`,
			id, companyID, itemID, b.Name, b.MfgDate, b.ExpiryDate,
		); err != nil {
			return fmt.Errorf("masters: insert batch: %w", err)
		}
		after := map[string]any{
			"name": b.Name, "stock_item": b.StockItemName,
			"mfg_date": b.MfgDate, "expiry_date": b.ExpiryDate,
		}
		return appendAudit(ctx, tx, companyID, "stock_batch", id, OpCreate, nil, after, createdBy)
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// StockBatchID resolves a batch's id by (stock item name, batch name) within
// the company in context. Both names are required: a batch name alone is not
// unique, by design — see the StockBatch type comment.
func (s *Service) StockBatchID(ctx context.Context, stockItemName, batchName string) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`SELECT b.id
			   FROM stock_batch b
			   JOIN stock_item i ON i.id = b.stock_item_id
			  WHERE i.name = $1 AND b.name = $2`,
			stockItemName, batchName).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("masters: %w: batch %q of stock item %q",
				ErrNotFound, batchName, stockItemName)
		}
		return err
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// StockBatchIDForItem resolves a batch's id when the caller already holds the
// stock item's id — the posting path's shape, which resolves the item once per
// line and would otherwise pay for the join on every batch lookup.
func (s *Service) StockBatchIDForItem(ctx context.Context, stockItemID uuid.UUID, batchName string) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`SELECT id FROM stock_batch WHERE stock_item_id = $1 AND name = $2`,
			stockItemID, batchName).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("masters: %w: batch %q of stock item %s",
				ErrNotFound, batchName, stockItemID)
		}
		return err
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// StockBatchInfo reads a batch back by id, with its dates in the same optional
// ISO-string form CreateStockBatch accepts: a batch created without an expiry
// reads back with ExpiryDate == "", never "0001-01-01".
//
// The dates are scanned into *time.Time for exactly that reason. Scanning a
// NULL DATE into a plain time.Time cannot express "absent" — it either errors
// or yields the zero instant, and the zero instant is a date a report will
// happily believe.
func (s *Service) StockBatchInfo(ctx context.Context, batchID uuid.UUID) (StockBatch, error) {
	out := StockBatch{ID: batchID}
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var mfg, expiry *time.Time
		err := tx.QueryRow(ctx,
			`SELECT b.name, i.name, b.mfg_date, b.expiry_date
			   FROM stock_batch b
			   JOIN stock_item i ON i.id = b.stock_item_id
			  WHERE b.id = $1`,
			batchID).Scan(&out.Name, &out.StockItemName, &mfg, &expiry)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("masters: %w: batch %s", ErrNotFound, batchID)
		}
		if err != nil {
			return fmt.Errorf("masters: read batch: %w", err)
		}
		out.MfgDate = formatOptionalDate(mfg)
		out.ExpiryDate = formatOptionalDate(expiry)
		return nil
	})
	if err != nil {
		return StockBatch{}, err
	}
	return out, nil
}

// parseOptionalDate validates an optional ISO date. "" means absent and is
// valid. The parse happens in Go rather than being left to Postgres' ::date
// cast so the caller gets a message naming the FIELD they got wrong instead of
// a bare `invalid input syntax for type date`.
func parseOptionalDate(s, field string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(isoDate, s)
	if err != nil {
		return nil, fmt.Errorf("masters: invalid %s %q: want YYYY-MM-DD", field, s)
	}
	return &t, nil
}

// formatOptionalDate renders a nullable DATE back to the wire form: "" for
// SQL NULL, ISO otherwise.
func formatOptionalDate(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(isoDate)
}
