package masters

// stock_valuation.go — the COSTING METHOD of a stock item (feature T9): the
// validation every write of stock_item.valuation_method goes through, and the
// one route that can CHANGE it on an item that is already trading.
//
// WHY CHANGING IT IS ITS OWN NARROW OPERATION AND NOT A FIELD ON A GENERAL
// MASTER EDITOR
// ---------------------------------------------------------------------------
// Because it is a RESTATEMENT, not an edit. Nothing about the item's history
// changes and no row of stock_movement is touched — and the next Stock Summary,
// the next Balance Sheet and the next Profit & Loss all come back with
// different numbers, on figures somebody has already read, printed and
// possibly filed. Renaming an item cannot do that. So this write:
//
//   - refuses a method no valuer implements, BEFORE the column can hold it;
//   - refuses a no-op, so an audit trail of restatements contains only real ones;
//   - writes a master_audit row naming BOTH methods, which is the only record
//     anywhere of why a period's closing stock moved.
//
// ⚠️ WHAT IT DOES NOT DO, AND CANNOT: it does not restate the item's LAYERS.
// A position that traded at weighted average has no lot queue to recover — the
// layers were never stored, because no valuation state is ever stored — so a
// switch to FIFO replays the WHOLE history under FIFO from the opening onwards,
// as if the item had always been FIFO. That is the only self-consistent answer
// available (and it is what makes the switch reversible: switching back
// reproduces the earlier figures exactly), but it means the restatement reaches
// every period the item has ever appeared in, not just the ones after today.
// The caller is told so in the API's own words; see the handler.

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/inventory"
	"github.com/khata/cored/internal/platform/db"
)

// valuationMethodOrDefault resolves a caller's valuation-method string to one
// this binary can actually value stock with, defaulting an empty string to
// Tally's default (and this build's) rather than refusing it: every caller
// written before T9 — the Tally importer, the demo seed, the offline
// sequencer — constructs a StockItem without knowing the field exists.
//
// ⛔ THE CHECK IS "IS THERE A VALUER", NOT "IS IT IN A LIST". A list here would
// be a third copy of the vocabulary (the valuers and migration 0078's CHECK are
// the other two) and the one most likely to drift ahead of the others — which
// would put a method in the column that every report then fails on.
func valuationMethodOrDefault(method string) (string, error) {
	if method == "" {
		return inventory.MethodWeightedAverage, nil
	}
	if _, err := inventory.ValuerFor(method); err != nil {
		return "", fmt.Errorf(
			"masters: valuation method %q is not one this build can value stock with (%s): %w",
			method, joinMethods(inventory.KnownValuationMethods()), err)
	}
	return method, nil
}

// joinMethods renders the implemented methods for an error message. The message
// names them because the alternative — "invalid valuation method" — leaves a
// caller guessing at a closed set of three.
func joinMethods(methods []string) string {
	out := ""
	for i, m := range methods {
		if i > 0 {
			out += ", "
		}
		out += m
	}
	return out
}

// ErrValuationMethodUnchanged is returned when a switch names the method the
// item already carries. It is an ERROR and not a silent success so that the
// master_audit trail of restatements holds only real ones — an audit log with a
// no-op restatement in it costs somebody an afternoon reconciling a period that
// never moved.
var ErrValuationMethodUnchanged = errors.New("masters: the stock item already uses that valuation method")

// StockItemValuation is one item's costing method as stored.
type StockItemValuation struct {
	StockItemID     uuid.UUID
	Name            string
	ValuationMethod string
}

// SetStockItemValuationMethod switches a stock item's costing method and
// appends a master_audit row naming both the old and the new one.
//
// It reads the current method inside the SAME transaction as the write, so the
// audit row's before-image is the value that was actually replaced rather than
// one read a moment earlier by somebody else's request.
func (s *Service) SetStockItemValuationMethod(ctx context.Context, stockItemID uuid.UUID,
	method, createdBy string) (StockItemValuation, error) {

	resolved, err := valuationMethodOrDefault(method)
	if err != nil {
		return StockItemValuation{}, err
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return StockItemValuation{}, db.ErrNoCompany
	}

	var out StockItemValuation
	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var name, before string
		err := tx.QueryRow(ctx,
			`SELECT name, valuation_method FROM stock_item WHERE id = $1`,
			stockItemID).Scan(&name, &before)
		if errors.Is(err, pgx.ErrNoRows) {
			// RLS hides another tenant's item, so "not found" covers both
			// "no such item" and "not yours" — deliberately indistinguishable.
			return fmt.Errorf("masters: %w: stock item %s", ErrNotFound, stockItemID)
		}
		if err != nil {
			return fmt.Errorf("masters: read stock item valuation method: %w", err)
		}
		if before == resolved {
			return fmt.Errorf("%w: %s is already %s", ErrValuationMethodUnchanged, name, resolved)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE stock_item SET valuation_method = $2 WHERE id = $1`,
			stockItemID, resolved); err != nil {
			return fmt.Errorf("masters: set stock item valuation method: %w", err)
		}
		out = StockItemValuation{StockItemID: stockItemID, Name: name, ValuationMethod: resolved}
		return appendAudit(ctx, tx, companyID, "stock_item", stockItemID, OpAlter,
			map[string]any{"name": name, "valuation_method": before},
			map[string]any{"name": name, "valuation_method": resolved},
			createdBy)
	})
	if err != nil {
		return StockItemValuation{}, err
	}
	return out, nil
}
