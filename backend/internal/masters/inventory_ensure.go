package masters

// inventory_ensure.go — RESOLVE-OR-CREATE for the inventory masters, and the
// writer for a stock item's OPENING POSITION.
//
// WHY THESE EXIST SEPARATELY FROM THE Create* METHODS
// ---------------------------------------------------
// The Tally importer meets the same master many times — an export names one
// item on forty invoices, one godown on all of them — and it meets masters that
// are NOT DECLARED ANYWHERE IN THE FILE. The real export the importer was fixed
// against carried no STOCKITEM, UNIT, GODOWN, VOUCHERTYPE or COSTCENTRE masters
// at all, while its vouchers referenced 227 stock lines with batches and
// godowns. So every inventory master has to be inferable FROM A VOUCHER, and
// the fortieth invoice must resolve what the first one created.
//
// ⛔ EVERY RESOLVE HERE IS CASE-INSENSITIVE, AND THAT IS NOT A CONVENIENCE. It
// is the same defect EnsureGroup's doc describes, one plane along: migration
// 0075 put a NORMALISED unique index on these names, so an exact-match resolve
// that missed "rice" against a stored "RICE" would go on to CREATE it and be
// refused by the database — aborting the import on a duplicate that is not a
// duplicate. Tally's own export spells its built-in groups with a different case
// from the seed; there is no reason to expect a trader's item names to be
// spelled consistently across four years of invoices, and they are not.
//
// ⚠️ AN ALREADY-PRESENT MASTER IS RETURNED UNCHANGED. Re-importing a file must
// not restate an item's unit, its HSN or its opening position: the second import
// would then overwrite corrections a human made after the first. `Ensure` means
// "make sure it exists", never "make it match the file".

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/platform/db"
)

// UnitIDBySymbol resolves a unit by symbol, case- and whitespace-insensitively,
// returning the id and the symbol AS STORED — which is what a caller must use
// for anything that resolves exactly (CreateStockItem does).
func (s *Service) UnitIDBySymbol(ctx context.Context, symbol string) (uuid.UUID, string, error) {
	return s.resolveNormalised(ctx, "unit", "symbol", symbol)
}

// GodownIDByName resolves a stock location by name, case-insensitively,
// returning the id and the name as stored.
func (s *Service) GodownIDByName(ctx context.Context, name string) (uuid.UUID, string, error) {
	return s.resolveNormalised(ctx, "godown", "name", name)
}

// StockItemIDByName resolves a stock item by name, case-insensitively,
// returning the id and the name as stored.
func (s *Service) StockItemIDByName(ctx context.Context, name string) (uuid.UUID, string, error) {
	return s.resolveNormalised(ctx, "stock_item", "name", name)
}

// resolveNormalised is the shared case-insensitive lookup. table and column are
// fixed internal literals (never user input), so the format string is safe.
func (s *Service) resolveNormalised(ctx context.Context, table, column, value string) (uuid.UUID, string, error) {
	var (
		id     uuid.UUID
		stored string
	)
	sql := fmt.Sprintf(
		`SELECT id, %s FROM %s WHERE lower(btrim(%s)) = lower(btrim($1))`, column, table, column)
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, sql, value).Scan(&id, &stored)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("masters: %w: %s %q", ErrNotFound, table, value)
		}
		return err
	})
	if err != nil {
		return uuid.Nil, "", err
	}
	return id, stored, nil
}

// EnsureUnit resolves a unit of measure by symbol and creates it if absent.
// created reports which happened, so an importer can list what it BROUGHT IN
// rather than what it merely referenced.
func (s *Service) EnsureUnit(ctx context.Context, u Unit, createdBy string) (id uuid.UUID, stored string, created bool, err error) {
	id, stored, err = s.UnitIDBySymbol(ctx, u.Symbol)
	if err == nil {
		return id, stored, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return uuid.Nil, "", false, err
	}
	id, err = s.CreateUnit(ctx, u, createdBy)
	if err != nil {
		return uuid.Nil, "", false, err
	}
	return id, u.Symbol, true, nil
}

// EnsureGodown resolves a stock location by name and creates it if absent.
func (s *Service) EnsureGodown(ctx context.Context, name, createdBy string) (id uuid.UUID, stored string, created bool, err error) {
	id, stored, err = s.GodownIDByName(ctx, name)
	if err == nil {
		return id, stored, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return uuid.Nil, "", false, err
	}
	id, err = s.CreateGodown(ctx, Godown{Name: name}, createdBy)
	if err != nil {
		return uuid.Nil, "", false, err
	}
	return id, name, true, nil
}

// EnsureStockItem resolves a stock item by name and creates it if absent.
//
// ⚠️ si.UnitSymbol MUST BE THE STORED SPELLING of an existing unit (use
// EnsureUnit's second return value): CreateStockItem resolves the unit EXACTLY,
// so a differently-cased symbol answers ErrNotFound and the item is refused —
// with a message about a unit, on an import of an item.
func (s *Service) EnsureStockItem(ctx context.Context, si StockItem, createdBy string) (id uuid.UUID, stored string, created bool, err error) {
	id, stored, err = s.StockItemIDByName(ctx, si.Name)
	if err == nil {
		return id, stored, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return uuid.Nil, "", false, err
	}
	id, err = s.CreateStockItem(ctx, si, createdBy)
	if err != nil {
		return uuid.Nil, "", false, err
	}
	return id, si.Name, true, nil
}

// EnsureStockBatch resolves a batch of ONE item by name and creates it if
// absent. The item id is passed rather than its name because a batch's name is
// item-scoped: two items may each carry a 'B-01' and they are unrelated.
func (s *Service) EnsureStockBatch(ctx context.Context, stockItemID uuid.UUID, itemName, batchName,
	mfgDate, expiryDate, createdBy string) (id uuid.UUID, created bool, err error) {

	// ⚠️ RESOLVED CASE-INSENSITIVELY HERE, unlike StockBatchIDForItem, which
	// matches exactly. There is no normalised unique index on stock_batch
	// (migration 0075 added them for ledger, stock_item and unit only), so an
	// exact-match resolve would NOT be refused on create — it would quietly
	// produce a SECOND batch, 'b-01' beside 'B-01', splitting one physical lot's
	// stock across two positions with two expiry dates. That is worse than the
	// duplicate the other resolvers avoid, because nothing stops it.
	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		e := tx.QueryRow(ctx,
			`SELECT id FROM stock_batch
			  WHERE stock_item_id = $1 AND lower(btrim(name)) = lower(btrim($2))`,
			stockItemID, batchName).Scan(&id)
		if errors.Is(e, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return e
	})
	if err == nil {
		return id, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return uuid.Nil, false, err
	}
	id, err = s.CreateStockBatch(ctx, StockBatch{
		StockItemName: itemName,
		Name:          batchName,
		MfgDate:       mfgDate,
		ExpiryDate:    expiryDate,
	}, createdBy)
	if err != nil {
		return uuid.Nil, false, err
	}
	return id, true, nil
}

// SetStockOpening records the OPENING POSITION of one (item, godown, batch)
// triple: the quantity and cost the books opened with, as at
// company.books_beginning_from.
//
// ⛔ IT IS AN INSERT THAT DOES NOTHING IF THE POSITION ALREADY HAS AN OPENING,
// AND NOT AN UPSERT. Two reasons, and the second is the one that matters:
//
//  1. re-importing a file must not restate an opening a human has since
//     corrected (the rule every Ensure in this file follows);
//  2. an opening is not additive. If a second import ADDED to it the trader's
//     opening stock would double, and nothing would disagree — closing quantity
//     would simply be wrong by exactly the opening, on every report, for ever.
//
// created reports whether a row landed, so an importer can say what it brought
// in and a caller can tell "already had one" from "wrote one".
func (s *Service) SetStockOpening(ctx context.Context, stockItemID, godownID, batchID uuid.UUID,
	qty, rate decimal.Decimal, createdBy string) (created bool, err error) {

	if rate.IsNegative() {
		// stock_opening.rate carries CHECK (rate >= 0); say which field is wrong
		// rather than surfacing a constraint name.
		return false, fmt.Errorf("masters: opening rate %s is negative", rate.StringFixed(4))
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return false, db.ErrNoCompany
	}
	openingID, err := uuid.NewV7()
	if err != nil {
		return false, fmt.Errorf("masters: new stock opening id: %w", err)
	}
	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// ⛔ THE TWO NULLABLE DIMENSIONS ARE PASSED AS POINTERS, NOT AS uuid.Nil
		// WITH A NULLIF. Both express "untracked" — but NULLIF($4, $7) leaves
		// Postgres to infer the parameter types of a comparison between two
		// unknowns and it binds them as TEXT, so the INSERT is refused with
		// "column godown_id is of type uuid but expression is of type text".
		// A nil pointer is unambiguous, and stock_opening_position is UNIQUE
		// NULLS NOT DISTINCT so the conflict target still matches an untracked
		// position.
		row := tx.QueryRow(ctx,
			`INSERT INTO stock_opening
			   (id, company_id, stock_item_id, godown_id, batch_id, qty, rate)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)
			 ON CONFLICT ON CONSTRAINT stock_opening_position DO NOTHING
			 RETURNING id`,
			openingID, companyID, stockItemID, nilAsNull(godownID), nilAsNull(batchID), qty, rate)
		var landed uuid.UUID
		err := row.Scan(&landed)
		if errors.Is(err, pgx.ErrNoRows) {
			// The position already had an opening. Not an error, and NOT audited:
			// nothing changed.
			created = false
			return nil
		}
		if err != nil {
			return fmt.Errorf("masters: insert stock opening: %w", err)
		}
		created = true
		return appendAudit(ctx, tx, companyID, "stock_item", stockItemID, OpAlter, nil,
			map[string]any{
				"opening_qty":  qty.StringFixed(4),
				"opening_rate": rate.StringFixed(4),
				"godown_id":    uuidOrEmptyString(godownID),
				"batch_id":     uuidOrEmptyString(batchID),
			}, createdBy)
	})
	if err != nil {
		return false, err
	}
	return created, nil
}

// nilAsNull turns an untracked dimension into a SQL NULL: uuid.Nil means "this
// movement carries no godown / no batch", which the schema spells NULL.
func nilAsNull(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

// uuidOrEmptyString renders a nullable dimension for an audit snapshot: the
// empty string for uuid.Nil, so "untracked" reads as untracked rather than as
// the all-zeroes uuid, which looks like a real id.
func uuidOrEmptyString(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}
