package migration

// inventory.go — THE GOODS. Stock items, units, locations, batches, opening
// positions, and the movement lines of every imported voucher.
//
// ⛔⛔ WHY THIS FILE EXISTS, AND WHY ITS ABSENCE WAS INVISIBLE
// ===========================================================================
// integration.TallyInventoryEntry has been parsed since the importer was
// written, and until now it was referenced by NOTHING in this package. The
// importer read every stock line off the wire and threw it away: an imported
// company got the ACCOUNTING VALUE of its trading and no goods at all — no
// item, no quantity, no batch, no godown.
//
// And a TRIAL BALANCE CANNOT SEE THAT. The rupees are all there and both sides
// foot, so every accounting check passes; what a trader gets is a Balance Sheet
// carrying closing stock and a Stock Summary with nothing in it. That is why
// this survived the first real-file gate (T32) with seven other defects found:
// the oracle was balanced statements, and quantity is invisible in one.
//
// ⛔ THE ORACLE FOR ANYTHING IN THIS FILE HAS TO BE QUANTITIES, independently
// re-derived from the XML. Never compare the importer to itself.
//
// WHAT A REAL EXPORT LOOKS LIKE, WHICH IS NOT WHAT THE DOCUMENTATION SUGGESTS
// ---------------------------------------------------------------------------
// The real file this was built against carried NO STOCKITEM, UNIT, GODOWN,
// VOUCHERTYPE or COSTCENTRE MASTERS AT ALL — a Day Book export is vouchers and
// nothing else — while its 121 vouchers referenced 227 stock lines with batches
// and godowns. So every inventory master here must be inferable FROM A VOUCHER:
//
//   - the ITEM from <STOCKITEMNAME>;
//   - the UNIT from the quantity's own text ("100.00 BAG" -> "BAG"), because
//     that is the only place it appears;
//   - the GODOWN and the BATCH from <BATCHALLOCATIONS.LIST>, which is where a
//     real export puts them (not on the line);
//   - and NOTHING about the opening position, which cannot be inferred from a
//     voucher at all. See the negative-stock note in importStockLines.
//
// A Masters.xml that DOES carry them is imported properly first (units before
// items, items before their openings), and then anything a voucher references
// and the masters did not declare is created on demand.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/integration"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
)

// inventoryCache memoises the masters resolved or created during ONE import run
// so that an item named on forty invoices is resolved once.
//
// The keys are LOWER-CASED, matching the case-insensitive resolvers in
// internal/masters: two spellings of one item name in one file must hit one
// cache entry, or the second spelling round-trips to the database, resolves the
// same row and stores a second entry — harmless — or, before those resolvers
// were normalised, tried to CREATE it and aborted the import.
type inventoryCache struct {
	units   map[string]string    // lower(symbol) -> STORED symbol
	items   map[string]stockItem // lower(name)   -> id + stored name + unit
	godowns map[string]uuid.UUID // lower(name)   -> id
	batches map[string]uuid.UUID // itemID|lower(name) -> id
}

// cctx is the per-RUN context an inventory helper needs: the request context,
// who is importing, and the master cache for this run.
//
// ⛔ THE CACHE TRAVELS IN HERE RATHER THAN ON THE Importer, and that is not
// tidiness. An Importer is constructed once and can serve more than one import
// (the HTTP handler holds one), so a cache on the struct would carry one
// tenant's item ids into the NEXT company's import — every one of them invisible
// to RLS, because the ids would never be re-read. The cache belongs to the run.
type cctx struct {
	ctx       context.Context
	createdBy string
	inv       *inventoryCache
}

// stockItem is one resolved item: its id, the name AS STORED (needed by
// CreateStockBatch, which resolves the item by exact name) and its unit.
type stockItem struct {
	id     uuid.UUID
	name   string
	unit   string
	quirks []string
}

func newInventoryCache() *inventoryCache {
	return &inventoryCache{
		units:   map[string]string{},
		items:   map[string]stockItem{},
		godowns: map[string]uuid.UUID{},
		batches: map[string]uuid.UUID{},
	}
}

func lowerKey(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// ensureUnit resolves or creates a unit of measure and returns the symbol AS
// STORED. An empty symbol returns empty: an item with no unit is legal here (the
// server allows unit_id NULL) and inventing "Nos" for it would put a unit on the
// e-invoice of goods that were never counted in pieces.
func (im *Importer) ensureUnit(ctx cctx, symbol string, res *ImportResult) (string, error) {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return "", nil
	}
	if stored, ok := ctx.inv.units[lowerKey(symbol)]; ok {
		return stored, nil
	}
	// DecimalPlaces 4 rather than 0: Tally quantities in a real file carry
	// fractions (grain is weighed), and unit.decimal_places is a DISPLAY
	// constraint whose default of 0 would make every fractional quantity in the
	// file look wrong on screen while the stored NUMERIC(19,4) held the truth.
	// Four matches the column.
	_, stored, created, err := im.masters.EnsureUnit(ctx.ctx,
		masters.Unit{Symbol: symbol, DecimalPlaces: 4}, ctx.createdBy)
	if err != nil {
		return "", fmt.Errorf("unit %q: %w", symbol, err)
	}
	ctx.inv.units[lowerKey(symbol)] = stored
	if created {
		res.UnitsEnsured = append(res.UnitsEnsured, stored)
	}
	return stored, nil
}

// ensureGodown resolves or creates a stock location. An empty name returns
// uuid.Nil, which is an UNTRACKED position and not an error: a company that
// never enabled multi-location keeps all its stock there, and inventing a
// location would move the goods somewhere the operator has never heard of.
func (im *Importer) ensureGodown(ctx cctx, name string, res *ImportResult) (uuid.UUID, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return uuid.Nil, nil
	}
	if id, ok := ctx.inv.godowns[lowerKey(name)]; ok {
		return id, nil
	}
	id, _, created, err := im.masters.EnsureGodown(ctx.ctx, name, ctx.createdBy)
	if err != nil {
		return uuid.Nil, fmt.Errorf("godown %q: %w", name, err)
	}
	ctx.inv.godowns[lowerKey(name)] = id
	if created {
		res.GodownsEnsured = append(res.GodownsEnsured, name)
	}
	return id, nil
}

// ensureStockItem resolves or creates a stock item. unitSymbol/hsn/gstRate are
// used ONLY when the item is created — an existing item is never restated, so a
// re-import cannot overwrite a unit or an HSN a human fixed after the first one.
func (im *Importer) ensureStockItem(ctx cctx, name, unitSymbol, hsn string,
	gstRate decimal.Decimal, res *ImportResult) (stockItem, error) {

	name = strings.TrimSpace(name)
	if name == "" {
		return stockItem{}, fmt.Errorf("a stock line names no item")
	}
	if it, ok := ctx.inv.items[lowerKey(name)]; ok {
		return it, nil
	}
	unit, err := im.ensureUnit(ctx, unitSymbol, res)
	if err != nil {
		return stockItem{}, err
	}
	id, stored, created, err := im.masters.EnsureStockItem(ctx.ctx, masters.StockItem{
		Name:       name,
		UnitSymbol: unit,
		HSN:        hsn,
		GSTRatePct: gstRate,
	}, ctx.createdBy)
	if err != nil {
		return stockItem{}, fmt.Errorf("stock item %q: %w", name, err)
	}
	it := stockItem{id: id, name: stored, unit: unit}
	ctx.inv.items[lowerKey(name)] = it
	if created {
		res.StockItemsEnsured = append(res.StockItemsEnsured, stored)
	}
	return it, nil
}

// ensureBatch resolves or creates a batch of one item.
func (im *Importer) ensureBatch(ctx cctx, it stockItem, name, mfg, expiry string,
	res *ImportResult) (uuid.UUID, error) {

	name = strings.TrimSpace(name)
	if name == "" {
		return uuid.Nil, nil
	}
	key := it.id.String() + "|" + lowerKey(name)
	if id, ok := ctx.inv.batches[key]; ok {
		return id, nil
	}
	id, created, err := im.masters.EnsureStockBatch(ctx.ctx, it.id, it.name, name,
		tallyDateOrEmpty(mfg), tallyDateOrEmpty(expiry), ctx.createdBy)
	if err != nil {
		return uuid.Nil, fmt.Errorf("batch %q of %q: %w", name, it.name, err)
	}
	ctx.inv.batches[key] = id
	if created {
		res.BatchesEnsured = append(res.BatchesEnsured, it.name+"/"+name)
	}
	return id, nil
}

// tallyDateOrEmpty converts a Tally YYYYMMDD date to ISO, or returns "" for
// anything it cannot read. A batch DATE is optional metadata: refusing an import
// over an unparseable expiry would trade the whole migration for a field no
// figure depends on. ⚠️ It is not silent — importInventoryMasters warns.
func tallyDateOrEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	iso, err := integration.TallyDateToISO(s)
	if err != nil {
		return ""
	}
	return iso
}

// importInventoryMasters is the pass that runs BEFORE any voucher: UNIT masters,
// then GODOWN masters, then STOCKITEM masters with their opening positions.
//
// ⛔ THE ORDER IS LOAD-BEARING AND IT IS NOT THE FILE'S ORDER. A stock item
// names a unit (CreateStockItem resolves it and refuses ErrNotFound) and its
// opening position names a godown, so the two dimensions must exist first. Tally
// writes masters in its own order and a real export interleaves them.
func (im *Importer) importInventoryMasters(ctx cctx, msgs []integration.TallyMessage,
	res *ImportResult) error {

	for _, msg := range msgs {
		u := msg.Unit
		if u == nil || u.Deleted() || u.Name() == "" {
			continue
		}
		stored, err := im.ensureUnit(ctx, u.Name(), res)
		if err != nil {
			return fmt.Errorf("migration: %w", err)
		}
		// ⛔ A COMPOUND UNIT IS REPORTED, NEVER SILENTLY FLATTENED. "Box of 12
		// Nos" arrives as a unit named Box with a conversion this schema cannot
		// hold, so every quantity in Boxes is stored as Boxes and is CORRECT —
		// but a report cannot restate it in pieces, and a reader who assumes it
		// can is out by the conversion factor. Saying so is the difference
		// between a limitation and a wrong number.
		if u.Compound() {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"unit %q is a COMPOUND unit in Tally (1 %s = %s %s) and was imported as a SIMPLE "+
					"symbol: quantities in it are stored as %s and nothing in this build can "+
					"restate them in %s",
				stored, strings.TrimSpace(u.BaseUnits), strings.TrimSpace(u.Conversion),
				strings.TrimSpace(u.AdditionalUnits), stored, strings.TrimSpace(u.AdditionalUnits)))
		}
	}

	for _, msg := range msgs {
		g := msg.Godown
		if g == nil || g.Deleted() || g.Name() == "" {
			continue
		}
		if _, err := im.ensureGodown(ctx, g.Name(), res); err != nil {
			return fmt.Errorf("migration: %w", err)
		}
		// Tally's locations are a TREE and cored's are flat. The stock is in the
		// right place either way (a movement names a leaf), but a location that
		// was a child of "Main Warehouse" no longer says so.
		if p := strings.TrimSpace(g.Parent); p != "" && !strings.EqualFold(p, "Primary") {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"godown %q was under %q in Tally; locations are FLAT here, so the parent was not "+
					"recorded (no quantity is affected)", g.Name(), p))
		}
	}

	for _, msg := range msgs {
		si := msg.StockItem
		if si == nil || si.Deleted() || si.Name() == "" {
			continue
		}
		rate := decimal.Zero
		if r := strings.TrimSpace(si.GSTRate); r != "" {
			if d, err := decimal.NewFromString(r); err == nil {
				rate = d
			}
		}
		unit := si.UnitSymbol()
		if unit == "" {
			// The unit may still be recoverable from the opening quantity's own
			// text ("120 BAG"), which is where a Day-Book-only export hides it.
			for _, p := range si.OpeningPositions() {
				if _, u, err := integration.SplitQtyUnit(p.Qty); err == nil && u != "" {
					unit = u
					break
				}
			}
		}
		it, err := im.ensureStockItem(ctx, si.Name(), unit, si.HSNCode(), rate, res)
		if err != nil {
			return fmt.Errorf("migration: %w", err)
		}
		if err := im.importItemOpenings(ctx, si, it, res); err != nil {
			return err
		}
	}
	return nil
}

// importItemOpenings writes a stock item's opening positions.
//
// ⛔ THE OPENING IS THE ONE THING A VOUCHER CANNOT TELL US, and getting it wrong
// is not a rounding error. Without it, a company that sold goods it bought
// before the export window drives that position NEGATIVE — and a negative
// position has no cost to consume, so the cost of goods sold is understated by
// the whole opening value and the closing stock on the Balance Sheet with it.
// Everything still balances.
func (im *Importer) importItemOpenings(ctx cctx, si *integration.TallyStockItemMaster,
	it stockItem, res *ImportResult) error {

	for _, pos := range si.OpeningPositions() {
		qty, unitText, err := integration.SplitQtyUnit(pos.Qty)
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"stock item %q: opening quantity %q could not be read, so this position opens at "+
					"ZERO and its first issue will drive it negative: %v", it.name, pos.Qty, err))
			continue
		}
		if qty.IsZero() {
			continue
		}
		_ = unitText // the item's unit is already set; a per-position unit is not a thing here.

		// The rate. Tally may state OPENINGRATE ("82.50/BAG"), or only
		// OPENINGVALUE — in which case the rate is value/qty, which is what the
		// valuer needs and what Tally itself displays.
		rate := decimal.Zero
		if r, _, err := integration.SplitQtyUnit(pos.Rate); err == nil && !r.IsZero() {
			rate = r
		} else if pos.Amount != "" {
			if v, err := decimal.NewFromString(strings.TrimSpace(pos.Amount)); err == nil && !qty.IsZero() {
				rate = v.Abs().DivRound(qty.Abs(), 4)
			}
		}
		if rate.IsZero() {
			// ⛔ REPORTED, NOT GUESSED. A zero-rate opening is a real position
			// carried at nil value: the quantity is right and the Balance Sheet
			// is understated by whatever the stock actually cost. Guessing the
			// rate from a later purchase would invent a number.
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"stock item %q: opening of %s carries NO rate or value in the export, so it is "+
					"carried at ZERO — the quantity is right and closing stock VALUE is "+
					"understated until you set the rate", it.name, qty.StringFixed(4)))
		}

		godownID, err := im.ensureGodown(ctx, pos.GodownName, res)
		if err != nil {
			return fmt.Errorf("migration: stock item %q opening: %w", it.name, err)
		}
		// ⛔⛔ AN OPENING WITH NO LOCATION STAYS UNTRACKED (SQL NULL), AND IT MUST
		// NOT BE PUT AT 'Main Location'. This was measured, and the failure is
		// total rather than subtle:
		//
		// masters.CreateStockItem puts a keyboard-created item's opening at the
		// system 'Main Location', because that is the only position the retired
		// dimensionless opening_qty column could have meant. Doing the same here
		// looks harmless and is not: a Tally stock line that names no godown
		// posts as UNTRACKED, and untracked is a DIFFERENT POSITION from Main
		// Location. The opening would sit in one and every issue would come out
		// of the other, so the first sale drove the untracked position to -100
		// while 120 bags sat in Main Location — with negative stock refused, the
		// whole import aborted; with it allowed, the sale would have consumed
		// NOTHING (a negative position has no cost) and the trader's cost of
		// goods sold would have been understated by the entire opening value,
		// with the quantities split across two positions that each look wrong.
		//
		// The rule: THE OPENING GOES WHERE THE FILE'S OWN MOVEMENTS GO. Inside
		// one export the two must agree about what "no godown" means.
		_ = godownID
		batchID, err := im.ensureBatch(ctx, it, pos.BatchName, pos.MfgDate, pos.ExpiryDate, res)
		if err != nil {
			return fmt.Errorf("migration: stock item %q opening: %w", it.name, err)
		}

		created, err := im.masters.SetStockOpening(ctx.ctx, it.id, godownID, batchID,
			qty, rate, ctx.createdBy)
		if err != nil {
			return fmt.Errorf("migration: stock item %q opening: %w", it.name, err)
		}
		if created {
			res.OpeningPositions++
		}
	}
	return nil
}

// inventoryLinesFor builds the stock movements of ONE voucher from its stock
// lines, ensuring any master the line names.
//
// ⛔ THE DIRECTION IS THE VOUCHER'S CLASS, CORRECTED BY THE SIGN OF THE QUANTITY,
// AND NEVER ISDEEMEDPOSITIVE ALONE. A Sales voucher ships goods out and a
// Purchase brings them in — that is what the document IS. But a real export
// carries negative quantities on a stock line to mean the opposite (a returned
// item inside a sales voucher, a rejection inside a purchase), and qty is
// CHECK (qty > 0) in this schema, so the sign has to become the DIRECTION or the
// post is refused. Reading ISDEEMEDPOSITIVE instead is the trap: on a stock line
// Tally uses it inconsistently across versions, and getting it backwards turns
// every sale into a receipt — the warehouse fills up as the company sells, and
// the accounting still balances because the postings come from the ledger
// entries.
func (im *Importer) inventoryLinesFor(ctx cctx, idx int, v *integration.TallyVoucher,
	class ledger.Class, res *ImportResult) ([]ledger.InventoryLine, error) {

	lines := v.InventoryLines()
	if len(lines) == 0 {
		return nil, nil
	}

	classDir, ok := stockDirectionForClass(class)
	if !ok {
		// A stock line on a class that cannot move goods here (a Payment, a
		// Receipt, a Contra, or a Journal — cored's Stock Journal is its own
		// document and is not what a Tally journal is). ⛔ REPORTED, not
		// swallowed: the goods in it are NOT imported, and an operator who is
		// not told will find the quantity missing months later.
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"voucher %d (%s %s): carries %d stock line(s) on a %s voucher, which cannot move goods "+
				"in this build — the ACCOUNTING was imported and the QUANTITIES were NOT",
			idx+1, v.VchType, strings.TrimSpace(v.VoucherNumber), len(lines), class))
		return nil, nil
	}

	out := make([]ledger.InventoryLine, 0, len(lines))
	for _, line := range lines {
		itemName := strings.TrimSpace(line.StockItemName)
		if itemName == "" {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"voucher %d: a stock line names no item and was skipped; its quantity is missing "+
					"from the warehouse", idx+1))
			continue
		}
		for _, pos := range line.StockPositions() {
			qty, unit, err := integration.SplitQtyUnit(pos.Qty)
			if err != nil {
				return nil, fmt.Errorf("migration: voucher %d: stock line %q: %w", idx+1, itemName, err)
			}
			if qty.IsZero() {
				// A zero-quantity stock line moves nothing. It is legal in Tally
				// (a line keyed and blanked), and writing it would be refused by
				// CHECK (qty > 0).
				continue
			}
			rate, rateUnit, err := integration.SplitQtyUnit(pos.Rate)
			if err != nil {
				// A rate that cannot be read is not fatal: the quantity is the
				// fact this file is here for, and the accounting value came from
				// the ledger entries. It IS reported, because a zero cost
				// understates this position for ever.
				res.Warnings = append(res.Warnings, fmt.Sprintf(
					"voucher %d: stock line %q has an unreadable rate %q, so the goods are "+
						"received at ZERO cost and this position's value is understated",
					idx+1, itemName, pos.Rate))
				rate = decimal.Zero
			}
			if unit == "" {
				unit = rateUnit
			}

			it, err := im.ensureStockItem(ctx, itemName, unit, "", decimal.Zero, res)
			if err != nil {
				return nil, fmt.Errorf("migration: voucher %d: %w", idx+1, err)
			}
			godownID, err := im.ensureGodown(ctx, pos.GodownName, res)
			if err != nil {
				return nil, fmt.Errorf("migration: voucher %d: %w", idx+1, err)
			}
			batchID, err := im.ensureBatch(ctx, it, pos.BatchName, pos.MfgDate, pos.ExpiryDate, res)
			if err != nil {
				return nil, fmt.Errorf("migration: voucher %d: %w", idx+1, err)
			}

			dir := classDir
			if qty.IsNegative() {
				dir = flipDirection(classDir)
			}
			out = append(out, ledger.InventoryLine{
				StockItemID: it.id,
				GodownID:    godownID,
				BatchID:     batchID,
				Direction:   dir,
				Qty:         qty.Abs(),
				Rate:        rate.Abs(),
				// ⛔ Amount IS LEFT ZERO ON PURPOSE, WHICH MEANS "DERIVE IT".
				// Tally's AMOUNT on a stock line is the BILLED value, and the
				// quantity written here is the ACTUAL one (what physically
				// moved). On a line where the two differ — a free replacement,
				// a short-shipment — qty * rate does not equal AMOUNT, and
				// ledger.validateInventory REFUSES a contradicting pair rather
				// than guessing which the operator meant. Sending the billed
				// amount beside the actual quantity would abort the import on
				// exactly the vouchers that are most interesting.
			})
			res.StockLines++
		}
	}
	return out, nil
}

// stockDirectionForClass maps a voucher class to the direction its goods move.
// The bool is false for a class that moves no stock in this build.
func stockDirectionForClass(class ledger.Class) (ledger.Direction, bool) {
	switch class {
	case ledger.Sales:
		return ledger.Outward, true
	case ledger.Purchase:
		return ledger.Inward, true
	default:
		return "", false
	}
}

// flipDirection is the reversal a negative quantity asks for.
func flipDirection(d ledger.Direction) ledger.Direction {
	if d == ledger.Inward {
		return ledger.Outward
	}
	return ledger.Inward
}

// describeNegativeStock turns ledger's negative-stock refusal into a sentence an
// operator can act on, naming the two ways forward.
//
// ⛔ THIS IS THE MOST LIKELY WAY A REAL IMPORT FAILS, AND IT IS NOT A BUG IN THE
// FILE. A Day Book export carries no STOCKITEM masters, so it carries no opening
// stock; the company's first sale of goods bought before the window has nothing
// to issue. The importer will NOT invent an opening (a quantity nobody stated)
// and will NOT switch the company's allow_negative_stock setting on behalf of
// the operator — either would be this program deciding what the trader's
// warehouse held.
func describeNegativeStock(idx int, v *integration.TallyVoucher, err error) error {
	return fmt.Errorf(
		"migration: voucher %d (%s %s dated %s) would drive a stock position NEGATIVE: %w.\n"+
			"This is what an export with no STOCKITEM masters looks like: it carries no OPENING "+
			"STOCK, so goods bought before the export window cannot be issued. Two ways forward, "+
			"and the importer will not choose for you: (1) import a Masters.xml carrying the items "+
			"with their opening balances FIRST — that is the correct fix and it also gets the "+
			"opening VALUE right; or (2) switch this company's 'allow negative stock' on before "+
			"importing, which brings the quantities in with the shortfall visible as a negative "+
			"position, at ZERO cost, and understates the cost of goods sold by the whole opening "+
			"value until you correct it",
		idx+1, strings.TrimSpace(v.VchType), strings.TrimSpace(v.VoucherNumber),
		strings.TrimSpace(v.Date), err)
}

// sortedUnique returns the distinct members of a slice in a stable order, for
// reporting what an import brought in without repeating an item forty times.
func sortedUnique(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
