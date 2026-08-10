package reporting

// stock_summary.go — THE STOCK SUMMARY: quantity from SQL, VALUE from a replay.
//
// WHY THERE IS NO SUM(value) ANYWHERE IN THIS FILE
// -----------------------------------------------
// Stock value is never stored and is never aggregated in SQL. It is derived
// here by replaying the movement ledger through internal/inventory's pure
// Valuer, because valuation is ORDER-DEPENDENT in a way a SQL aggregate cannot
// express: a back-dated purchase re-prices every issue that already happened
// after it, and an ALTER of an April invoice changes the JULY closing value.
// `SUM(qty * rate)` would produce a confident, plausible, wrong number — it
// values an issue at the price it SOLD for rather than at what it COST, which
// silently zeroes gross profit and misstates closing stock on the Balance Sheet.
//
// Quantity is a different animal. It is order-INdependent — a plain signed sum —
// so it comes straight out of the movement rows, exactly as 0025's
// negative-stock trigger computes a closing quantity in SQL and could not have
// computed a closing value.
//
// This file is the ONLY consumer of internal/inventory (the archtest edge
// `internal/reporting -> internal/inventory` exists for precisely this call),
// and it holds no valuation arithmetic of its own. Every multiplication,
// division and rounding lives in the leaf, so a report and a corpus fixture
// cannot drift apart: TestClosingValueEqualsTheCorpusOracle seeds the database
// with the scenario a pure fixture asserts and demands the same closing value.
//
// POSITION vs PERIOD — the same split reporting.go makes for the accounts
// -----------------------------------------------------------------------
//   - StockSummary(asOn) is a POSITION AS AT one date. It takes ONE date, it has
//     NO lower bound, and it folds the opening position in. Applying a `from` to
//     it would drop the stock the company already owned and still produce a
//     tidy-looking table — the worst kind of wrong.
//   - StockMovementReport(from, to) is a REGISTER FOR A PERIOD. It takes TWO
//     dates, both bounds INCLUSIVE, and the opening position is excluded from
//     every figure it reports.
//
// Their ARITIES differ so neither can be called with the other's meaning by
// accident, and TestStockSummaryPositionVsPeriodAreDifferentFunctions pins that
// they answer different questions rather than the same one twice.
//
// The opening position IS still handed to the valuer inside the period report,
// and that is not a contradiction: it is not REPORTED, it only PRICES. An issue
// in July of goods bought in April costs what those goods cost; a register that
// replayed only July would value that issue at zero (there is nothing on hand to
// average) and call it cost of goods sold. So the opening and the whole prior
// history set the running cost state, and the reported figures are the DIFFERENCE
// between the replay up to `to` and the replay up to just before `from`. That
// difference is exactly the window's own inwards and outwards, which is what a
// period statement means.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/inventory"
)

// --------------------------------------------------------------------------
// Report shapes
// --------------------------------------------------------------------------

// StockPositionRow is one (item, godown, batch) line of the Stock Summary: the
// replayed inventory.Position plus the names a UI needs to render it.
//
// The dimensions are uuid.Nil / "" when the movement carried no godown or batch.
// That is a POSITION like any other, not a missing row: a company that never
// enables multi-location keeps all of its stock there, and a report that hid it
// would show an empty warehouse.
type StockPositionRow struct {
	StockItemID uuid.UUID
	ItemName    string
	UnitSymbol  string
	// GodownID/GodownName are uuid.Nil/"" for an untracked location.
	GodownID   uuid.UUID
	GodownName string
	// BatchID/BatchName are uuid.Nil/"" for an untracked batch.
	BatchID   uuid.UUID
	BatchName string
	// ValuationMethod names the basis the figures were derived on, so a printed
	// report can say which valuer produced it (RESEARCH sec 8: a statement must
	// be able to state its own basis).
	ValuationMethod string

	// Position carries the whole Opening/Inward/Outward/Closing quartet. Its
	// fields are promoted, so row.ClosingValue reads naturally while row.Position
	// can still be handed back to the leaf.
	inventory.Position
}

// StockItemRollup is one stock item's total across every position it holds, with
// those positions as drill-down children — the group -> leaf tree shape every
// report in this package shares.
//
// The totals are FOLDED FROM THE POSITIONS and never re-queried. Two godowns of
// the same item carry two different average costs and the average of the
// averages is not the average, so an item total derived from its own ungrouped
// aggregate would disagree with the rows printed underneath it. Which is why
// there is no ClosingRate here: a blended rate across locations is a number
// nobody can act on. Ask a row for its rate.
type StockItemRollup struct {
	StockItemID uuid.UUID
	ItemName    string
	UnitSymbol  string

	OpeningQty   decimal.Decimal
	OpeningValue decimal.Decimal
	InwardQty    decimal.Decimal
	InwardValue  decimal.Decimal
	OutwardQty   decimal.Decimal
	OutwardValue decimal.Decimal
	ClosingQty   decimal.Decimal
	ClosingValue decimal.Decimal

	Positions []StockPositionRow
}

// StockSummary is the position of every stock item as at one date: item-wise
// rollups (each expandable to its positions), the flat position rows, and the
// closing value of the whole inventory.
//
// TotalValue is what a Stock-in-Hand line on the Balance Sheet is worth on that
// date. It is a sum of already-replayed closing values, not a re-derivation.
type StockSummary struct {
	// AsOn is the boundary the replay was ACTUALLY bounded at — always
	// RESOLVED, never the caller's parameter (see resolvedStockAsOn). Empty
	// means UNBOUNDED: every voucher ever posted, INCLUDING ones dated after
	// today. It does NOT mean "today". The Stock Summary screen renders this
	// value, and it used to print "As at today (everything posted)" over an
	// unbounded report — 72,837.15 under that caption where the position as at
	// that day was 96,168.18.
	AsOn       string
	Items      []StockItemRollup
	Rows       []StockPositionRow
	TotalValue decimal.Decimal
}

// StockMovementRow is one position's MOVEMENT inside a period: what came in,
// what went out, and the net change. There is deliberately no opening and no
// closing column — those are a position, and a position is what StockSummary
// answers.
//
// OutwardValue is COST CONSUMED (cost of goods sold for the window), priced off
// the running weighted-average cost including everything that happened before
// the window began. It is NOT what the goods sold for; booking the sale price as
// the cost is the single most common inventory-valuation bug and it makes gross
// profit come out as zero.
//
// NetValue = InwardValue - OutwardValue is therefore the change in the position's
// carried value across the window, which is what reconciles two Stock Summaries
// taken at the two ends of it.
type StockMovementRow struct {
	StockItemID uuid.UUID
	ItemName    string
	UnitSymbol  string
	GodownID    uuid.UUID
	GodownName  string
	BatchID     uuid.UUID
	BatchName   string

	InwardQty    decimal.Decimal
	InwardValue  decimal.Decimal
	OutwardQty   decimal.Decimal
	OutwardValue decimal.Decimal
	NetQty       decimal.Decimal
	NetValue     decimal.Decimal
}

// StockMovementItemRollup is a stock item's movement across all its positions,
// folded from those positions for the same reason StockItemRollup is.
type StockMovementItemRollup struct {
	StockItemID uuid.UUID
	ItemName    string
	UnitSymbol  string

	InwardQty    decimal.Decimal
	InwardValue  decimal.Decimal
	OutwardQty   decimal.Decimal
	OutwardValue decimal.Decimal
	NetQty       decimal.Decimal
	NetValue     decimal.Decimal

	Positions []StockMovementRow
}

// StockMovementReport is the movement register for [From, To], both bounds
// INCLUSIVE.
type StockMovementReport struct {
	From  string
	To    string
	Items []StockMovementItemRollup
	Rows  []StockMovementRow
}

// --------------------------------------------------------------------------
// The movement query
// --------------------------------------------------------------------------

// stockMovementSQL is the shared derivation of the movement ledger.
//
// $1 = from (may be NULL), $2 = to (may be NULL). The UPPER bound is a WHERE
// clause because a movement after `to` is not part of any answer this file
// gives. The LOWER bound is a PROJECTED FLAG rather than a filter, because the
// period report needs the movements BEFORE its window to price the issues
// inside it (see the file header) — it must see them and not count them, which
// a WHERE clause cannot express.
//
// THE vh JOIN IS THE WHOLE OF THE ALTER/DELETE STORY, exactly as it is for
// postings in reporting.go. Movements are versioned: an ALTER re-states the
// lines under a NEW voucher_version and the superseded rows stay in the table
// forever. Joining voucher_head on current_version_id keeps only the live
// version, so an item altered from 10 to 4 counts 4 and not 14, and a DELETE
// tombstone (which carries zero movement rows) contributes nothing. There is no
// reversal code anywhere in this file and there must never be any.
//
// Both bounds are INCLUSIVE. Flipping `>=` to `>` or `<=` to `<` here is a full
// off-by-one on every stock report and it survived undetected on the accounting
// side until a test placed a voucher exactly on a bound — which is what
// TestStockPeriodBoundsAreInclusiveOnBothEnds now does.
const stockMovementSQL = `
	SELECT sm.stock_item_id, sm.godown_id, sm.batch_id,
	       sm.direction::text, sm.qty, sm.rate, sm.discount_pct,
	       vv.voucher_date, COALESCE(vv.voucher_no, ''),
	       vv.created_at, sm.line_no, vv.id,
	       COALESCE($1::date IS NULL OR vv.voucher_date >= $1::date, true) AS in_window
	  FROM stock_movement sm
	  JOIN voucher_version vv ON vv.id = sm.voucher_version_id
	  JOIN voucher_head vh
	    ON vh.company_id = vv.company_id
	   AND vh.voucher_id = vv.voucher_id
	   AND vh.current_version_id = vv.id
	 WHERE vh.is_deleted = false
	   AND ($2::date IS NULL OR vv.voucher_date <= $2::date)`

// stockMovementRow is a movement in valuer form plus whether it falls inside the
// reported window. The flag is carried alongside the leaf's Movement rather than
// inside it: internal/inventory knows nothing about report periods and must not
// start to.
type stockMovementRow struct {
	movement inventory.Movement
	inWindow bool
}

// queryStockMovements reads the movement ledger bounded by an optional upper
// date, flagging the rows on or after the optional lower date.
func queryStockMovements(ctx context.Context, tx pgx.Tx, from, to any) ([]stockMovementRow, error) {
	rows, err := tx.Query(ctx, stockMovementSQL, from, to)
	if err != nil {
		return nil, fmt.Errorf("reporting: query stock movements: %w", err)
	}
	defer rows.Close()

	var out []stockMovementRow
	for rows.Next() {
		var (
			r         stockMovementRow
			itemID    uuid.UUID
			godownID  *uuid.UUID
			batchID   *uuid.UUID
			direction string
			date      *time.Time
			versionID uuid.UUID
		)
		if err := rows.Scan(&itemID, &godownID, &batchID,
			&direction, &r.movement.Qty, &r.movement.Rate, &r.movement.DiscountPct,
			&date, &r.movement.VoucherNo,
			&r.movement.VersionCreatedAt, &r.movement.LineNo, &versionID,
			&r.inWindow); err != nil {
			return nil, fmt.Errorf("reporting: scan stock movement: %w", err)
		}
		if date != nil {
			r.movement.VoucherDate = *date
		}
		r.movement.Position = positionKey(itemID, godownID, batchID)
		r.movement.Direction = inventory.Direction(direction)
		r.movement.VoucherVersionID = versionID.String()
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reporting: iterate stock movements: %w", err)
	}
	return out, nil
}

// loadStockPositionMovements reads the movement ledger AS AT one date. It takes
// ONE date and passes NULL for the lower bound — there is no `from` to pass,
// which is the point: a position needs the entity's whole history up to `asOn`
// and dropping the earlier part of it would still add up and still be wrong.
func loadStockPositionMovements(ctx context.Context, tx pgx.Tx, asOn string) ([]stockMovementRow, error) {
	return queryStockMovements(ctx, tx, nil, nullDate(asOn))
}

// loadStockPeriodMovements reads the movement ledger up to `to`, flagging the
// rows inside [from, to]. It takes TWO dates. The rows before `from` come back
// UNFLAGGED and are used only to set the running cost state (file header).
func loadStockPeriodMovements(ctx context.Context, tx pgx.Tx, from, to string) ([]stockMovementRow, error) {
	return queryStockMovements(ctx, tx, nullDate(from), nullDate(to))
}

// --------------------------------------------------------------------------
// Openings, and the date they are stated as at
// --------------------------------------------------------------------------

// loadStockOpenings reads the opening positions, subject to the same
// books-beginning rule an opening BALANCE obeys in queryBalances.
//
// A stock_opening row has no date column of its own — it is stated as at
// company.books_beginning_from, exactly as a ledger's opening_balance is. An
// as-at query that folded it in unconditionally would report stock on hand for a
// date BEFORE the entity's books existed. The live verifier caught precisely
// that class of bug on the accounting side (a Trial Balance dated 2020 showing
// 520000 of opening capital on a company whose books begin in 2026) and the
// inventory plane must not reintroduce it: 40 units of stock reported as at 2020
// on books that begin in 2026 is the same lie in a different unit.
//
// The company row is named EXPLICITLY rather than trusted to `LIMIT 1` over
// whatever RLS leaves visible, for the reason spelled out in queryBalances: a
// session without the tenant GUC would otherwise pick an arbitrary tenant's
// books-beginning date and mis-date this company's cutoff. current_setting with
// `true` yields NULL when unset, which matches no row and falls through to the
// leave-the-opening-in fail-safe.
func loadStockOpenings(ctx context.Context, tx pgx.Tx, asOn string) ([]inventory.Opening, error) {
	if asOn != "" {
		var begins string
		err := tx.QueryRow(ctx,
			`SELECT to_char(COALESCE(books_beginning_from, fy_start_date), 'YYYY-MM-DD')
			   FROM company
			  WHERE id = current_setting('app.current_company_id', true)::uuid`).Scan(&begins)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// No company row in scope; leave the opening in rather than guess.
		case err != nil:
			return nil, fmt.Errorf("reporting: read books beginning date: %w", err)
		default:
			// ISO dates compare correctly as strings, which is why every date on
			// this seam is carried as YYYY-MM-DD.
			if asOn < begins {
				return nil, nil
			}
		}
	}

	rows, err := tx.Query(ctx,
		`SELECT stock_item_id, godown_id, batch_id, qty, rate FROM stock_opening`)
	if err != nil {
		return nil, fmt.Errorf("reporting: query stock openings: %w", err)
	}
	defer rows.Close()

	var out []inventory.Opening
	for rows.Next() {
		var (
			o        inventory.Opening
			itemID   uuid.UUID
			godownID *uuid.UUID
			batchID  *uuid.UUID
		)
		if err := rows.Scan(&itemID, &godownID, &batchID, &o.Qty, &o.Rate); err != nil {
			return nil, fmt.Errorf("reporting: scan stock opening: %w", err)
		}
		o.Position = positionKey(itemID, godownID, batchID)
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reporting: iterate stock openings: %w", err)
	}
	return out, nil
}

// positionKey renders an (item, godown, batch) triple as the leaf's map key.
// A NULL dimension becomes the empty string, which is how internal/inventory
// spells SQL NULL — that equivalence is what reproduces the database's
// IS NOT DISTINCT FROM grouping in Go without a pointer-keyed map (two pointers
// to equal uuids hash differently and would split one position into two).
func positionKey(item uuid.UUID, godown, batch *uuid.UUID) inventory.PositionKey {
	k := inventory.PositionKey{StockItemID: item.String()}
	if godown != nil {
		k.GodownID = godown.String()
	}
	if batch != nil {
		k.BatchID = batch.String()
	}
	return k
}

// uuidOrNil turns a PositionKey dimension back into a uuid, with "" (SQL NULL)
// becoming uuid.Nil. A malformed string cannot occur — the keys are built from
// uuids a few lines above — and yields uuid.Nil rather than an error, because a
// report must not fail to render over a dimension label.
func uuidOrNil(s string) uuid.UUID {
	if s == "" {
		return uuid.Nil
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil
	}
	return id
}

// --------------------------------------------------------------------------
// Names and valuation methods
// --------------------------------------------------------------------------

// stockItemMeta is everything about an item that a position row needs but a
// movement row does not carry.
type stockItemMeta struct {
	Name            string
	UnitSymbol      string
	ValuationMethod string
}

// stockNames is the master-data a stock report renders with, read once per
// report rather than joined onto every movement row: the movement query already
// returns one row per line of every voucher ever posted and does not need three
// more joins on top of that.
type stockNames struct {
	items   map[uuid.UUID]stockItemMeta
	godowns map[uuid.UUID]string
	batches map[uuid.UUID]string
}

func loadStockNames(ctx context.Context, tx pgx.Tx) (stockNames, error) {
	n := stockNames{
		items:   map[uuid.UUID]stockItemMeta{},
		godowns: map[uuid.UUID]string{},
		batches: map[uuid.UUID]string{},
	}

	rows, err := tx.Query(ctx,
		`SELECT si.id, si.name, COALESCE(u.symbol, ''), si.valuation_method
		   FROM stock_item si
		   LEFT JOIN unit u ON u.id = si.unit_id`)
	if err != nil {
		return n, fmt.Errorf("reporting: query stock items: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id uuid.UUID
			m  stockItemMeta
		)
		if err := rows.Scan(&id, &m.Name, &m.UnitSymbol, &m.ValuationMethod); err != nil {
			return n, fmt.Errorf("reporting: scan stock item: %w", err)
		}
		n.items[id] = m
	}
	if err := rows.Err(); err != nil {
		return n, fmt.Errorf("reporting: iterate stock items: %w", err)
	}
	rows.Close()

	if err := scanNames(ctx, tx, `SELECT id, name FROM godown`, n.godowns); err != nil {
		return n, err
	}
	if err := scanNames(ctx, tx, `SELECT id, name FROM stock_batch`, n.batches); err != nil {
		return n, err
	}
	return n, nil
}

func scanNames(ctx context.Context, tx pgx.Tx, sql string, into map[uuid.UUID]string) error {
	rows, err := tx.Query(ctx, sql)
	if err != nil {
		return fmt.Errorf("reporting: query stock dimension names: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id   uuid.UUID
			name string
		)
		if err := rows.Scan(&id, &name); err != nil {
			return fmt.Errorf("reporting: scan stock dimension name: %w", err)
		}
		into[id] = name
	}
	return rows.Err()
}

// --------------------------------------------------------------------------
// The replay
// --------------------------------------------------------------------------

// replayPositions groups the openings and movements BY ITEM, selects that item's
// valuer, and replays each of its positions.
//
// Grouping by item first is not an optimisation: stock_item.valuation_method is
// per item, so two items in the same report can be valued on different bases.
// ValuerFor returns an ERROR for a method it does not implement and this
// propagates it — a report that quietly fell back to weighted average for a FIFO
// item would print a plausible number under the wrong heading, which is the
// failure mode the leaf's default arm exists to prevent.
func replayPositions(
	openings []inventory.Opening, movements []inventory.Movement, names stockNames,
) (map[inventory.PositionKey]inventory.Position, error) {
	openingsByItem := map[string][]inventory.Opening{}
	movementsByItem := map[string][]inventory.Movement{}
	var itemOrder []string
	seen := map[string]bool{}
	note := func(item string) {
		if !seen[item] {
			seen[item] = true
			itemOrder = append(itemOrder, item)
		}
	}
	for _, o := range openings {
		openingsByItem[o.Position.StockItemID] = append(openingsByItem[o.Position.StockItemID], o)
		note(o.Position.StockItemID)
	}
	for _, m := range movements {
		movementsByItem[m.Position.StockItemID] = append(movementsByItem[m.Position.StockItemID], m)
		note(m.Position.StockItemID)
	}

	out := map[inventory.PositionKey]inventory.Position{}
	for _, item := range itemOrder {
		meta, ok := names.items[uuidOrNil(item)]
		if !ok {
			// Structurally impossible: every movement and opening carries a
			// composite FK to stock_item. Refuse rather than guess a method.
			return nil, fmt.Errorf("reporting: stock item %s has movements but no master row", item)
		}
		valuer, err := inventory.ValuerFor(meta.ValuationMethod)
		if err != nil {
			return nil, fmt.Errorf("reporting: cannot value stock item %q: %w", meta.Name, err)
		}
		for key, pos := range inventory.ReplayAll(openingsByItem[item], movementsByItem[item], valuer) {
			out[key] = pos
		}
	}
	return out, nil
}

// movementsOf projects the leaf-shaped movements out of the query rows, keeping
// only the ones the caller asks for.
func movementsOf(rows []stockMovementRow, onlyInWindow bool) []inventory.Movement {
	out := make([]inventory.Movement, 0, len(rows))
	for _, r := range rows {
		if onlyInWindow != r.inWindow {
			continue
		}
		out = append(out, r.movement)
	}
	return out
}

// --------------------------------------------------------------------------
// Stock Summary — a POSITION as at one date
// --------------------------------------------------------------------------

// StockSummary is the closing stock position of every item as at `asOn`, broken
// down by (item, godown, batch) and rolled up per item. `asOn` empty means
// "everything ever posted".
//
// It takes ONE date and has NO lower bound. See the file header.
//
// NOTHING IS SUPPRESSED. A position whose closing quantity is zero but which had
// inwards and outwards during its life is REPORTED, with zeros in the closing
// columns — unlike the group tree in reporting.go, which drops a zero node to
// match Tally's "exclude zero" default for accounts. For stock that default is
// wrong: an item bought and then entirely sold is one of the most interesting
// rows on the report (it is where the gross profit is), and hiding it would make
// the outward column of the item total unexplainable from the rows beneath it.
func (s *Service) StockSummary(ctx context.Context, companyID uuid.UUID, asOn string) (StockSummary, error) {
	if err := scope(ctx, companyID); err != nil {
		return StockSummary{}, err
	}

	// The echo is the RESOLVED boundary, produced by the same function
	// stockPositionsAsOn resolves with, so the caption a screen prints and the
	// figures under it cannot come from two different dates.
	out := StockSummary{AsOn: resolvedStockAsOn(asOn), TotalValue: decimal.Zero}

	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// The five steps this used to spell out inline (names, guarded openings,
		// movements, replay, sorted rows) ARE stockPositionsAsOn, in that order
		// with those guards. Going through it means this screen and the Balance
		// Sheet's Closing Stock line share one derivation BY CONSTRUCTION rather
		// than by two maintainers keeping two copies in step — and closing stock
		// is order-dependent, so a copy that drifts by one guard prints a
		// different asset total on two screens with no way to tell which is right.
		rows, _, err := stockPositionsAsOn(ctx, tx, asOn)
		if err != nil {
			return err
		}
		out.Rows = rows
		out.TotalValue = totalClosingValue(out.Rows)
		out.Items = rollupStockItems(out.Rows)
		return nil
	})
	if err != nil {
		return StockSummary{}, err
	}
	return out, nil
}

// dimensionsOf resolves a position key into the labelled (item, godown, batch)
// triple both report shapes carry. An untracked dimension comes back as
// uuid.Nil with an empty name, which is what the UI renders as "-".
func dimensionsOf(key inventory.PositionKey, names stockNames) StockPositionRow {
	itemID := uuidOrNil(key.StockItemID)
	godownID := uuidOrNil(key.GodownID)
	batchID := uuidOrNil(key.BatchID)
	meta := names.items[itemID]
	return StockPositionRow{
		StockItemID:     itemID,
		ItemName:        meta.Name,
		UnitSymbol:      meta.UnitSymbol,
		GodownID:        godownID,
		GodownName:      names.godowns[godownID],
		BatchID:         batchID,
		BatchName:       names.batches[batchID],
		ValuationMethod: meta.ValuationMethod,
	}
}

// stockRowOf decorates a replayed position with its names.
func stockRowOf(key inventory.PositionKey, pos inventory.Position, names stockNames) StockPositionRow {
	row := dimensionsOf(key, names)
	row.Position = pos
	return row
}

// sortStockRows gives the report a stable, human order: item, then location,
// then batch. The ids are the final tiebreak so the order is TOTAL — two
// godowns cannot share a name within a company, but an untracked dimension has
// no name at all and several of those must still come back in one fixed order,
// or two runs of the same report would differ.
func sortStockRows(rows []StockPositionRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.ItemName != b.ItemName {
			return a.ItemName < b.ItemName
		}
		if a.GodownName != b.GodownName {
			return a.GodownName < b.GodownName
		}
		if a.BatchName != b.BatchName {
			return a.BatchName < b.BatchName
		}
		if a.GodownID != b.GodownID {
			return a.GodownID.String() < b.GodownID.String()
		}
		return a.BatchID.String() < b.BatchID.String()
	})
}

// rollupStockItems folds the position rows into per-item totals.
//
// IT FOLDS THE ROWS AND ISSUES NO QUERY OF ITS OWN. That is the whole design:
// an item total derived from a separate ungrouped aggregate would silently drop
// the positions whose godown is NULL the moment anyone joined the godown master
// for a name, and it could not produce a VALUE at all without blending two
// average costs into one that belongs to neither. The rows and the total they
// roll up into must be the same arithmetic, which is what
// TestPerGodownAndPerBatchPositionsSumToTheItemTotal asserts in quantity AND in
// value.
func rollupStockItems(rows []StockPositionRow) []StockItemRollup {
	var out []StockItemRollup
	idx := map[uuid.UUID]int{}
	for _, r := range rows {
		i, ok := idx[r.StockItemID]
		if !ok {
			i = len(out)
			idx[r.StockItemID] = i
			out = append(out, StockItemRollup{
				StockItemID:  r.StockItemID,
				ItemName:     r.ItemName,
				UnitSymbol:   r.UnitSymbol,
				OpeningQty:   decimal.Zero,
				OpeningValue: decimal.Zero,
				InwardQty:    decimal.Zero,
				InwardValue:  decimal.Zero,
				OutwardQty:   decimal.Zero,
				OutwardValue: decimal.Zero,
				ClosingQty:   decimal.Zero,
				ClosingValue: decimal.Zero,
			})
		}
		g := &out[i]
		g.OpeningQty = g.OpeningQty.Add(r.OpeningQty)
		g.OpeningValue = g.OpeningValue.Add(r.OpeningValue)
		g.InwardQty = g.InwardQty.Add(r.InwardQty)
		g.InwardValue = g.InwardValue.Add(r.InwardValue)
		g.OutwardQty = g.OutwardQty.Add(r.OutwardQty)
		g.OutwardValue = g.OutwardValue.Add(r.OutwardValue)
		g.ClosingQty = g.ClosingQty.Add(r.ClosingQty)
		g.ClosingValue = g.ClosingValue.Add(r.ClosingValue)
		g.Positions = append(g.Positions, r)
	}
	return out
}

// --------------------------------------------------------------------------
// Stock Movement Report — a PERIOD, both bounds inclusive
// --------------------------------------------------------------------------

// StockMovementReport is the movement register for [from, to], INCLUSIVE on both
// ends. Empty bounds mean unbounded on that side.
//
// It takes TWO dates and reports no opening and no closing: a period is what
// MOVED, a position is what IS. The opening position and every movement before
// `from` are still replayed — they set the running cost so an issue inside the
// window is consumed at what the goods actually cost — but they are not
// reported. The figures are the difference between the replay up to `to` and the
// replay up to just before `from`, which is exactly the window's own inwards and
// outwards because a replay accumulates them monotonically.
//
// Only positions that MOVED inside the window appear. That is not the zero
// suppression StockSummary refuses: a position that moved and came back to zero
// IS listed (it moved), while a position that sat untouched all quarter is not
// an event a movement register has anything to say about.
func (s *Service) StockMovementReport(ctx context.Context, companyID uuid.UUID, from, to string) (StockMovementReport, error) {
	if err := scope(ctx, companyID); err != nil {
		return StockMovementReport{}, err
	}

	out := StockMovementReport{From: from, To: to}

	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		names, err := loadStockNames(ctx, tx)
		if err != nil {
			return err
		}
		// The opening is priced in but never reported — hence `to`, not `from`:
		// the guard asks whether the books had begun by the end of the window.
		openings, err := loadStockOpenings(ctx, tx, to)
		if err != nil {
			return err
		}
		rows, err := loadStockPeriodMovements(ctx, tx, from, to)
		if err != nil {
			return err
		}

		// The replay up to the END of the window must see the movements BEFORE it
		// too, or the running cost state would start from nothing and an issue of
		// goods bought last quarter would be consumed at zero.
		upToEnd, err := replayPositions(openings, allMovements(rows), names)
		if err != nil {
			return err
		}
		// Every fetched movement is at or before `to`, so the UNFLAGGED ones are
		// exactly those before `from`.
		beforeStart, err := replayPositions(openings, movementsOf(rows, false), names)
		if err != nil {
			return err
		}

		moved := map[inventory.PositionKey]bool{}
		for _, r := range rows {
			if r.inWindow {
				moved[r.movement.Position] = true
			}
		}

		for key := range moved {
			end := upToEnd[key]
			start := beforeStart[key]
			row := StockMovementRow{
				InwardQty:    end.InwardQty.Sub(start.InwardQty),
				InwardValue:  end.InwardValue.Sub(start.InwardValue),
				OutwardQty:   end.OutwardQty.Sub(start.OutwardQty),
				OutwardValue: end.OutwardValue.Sub(start.OutwardValue),
			}
			row.NetQty = row.InwardQty.Sub(row.OutwardQty)
			row.NetValue = row.InwardValue.Sub(row.OutwardValue)

			d := dimensionsOf(key, names)
			row.StockItemID = d.StockItemID
			row.ItemName = d.ItemName
			row.UnitSymbol = d.UnitSymbol
			row.GodownID = d.GodownID
			row.GodownName = d.GodownName
			row.BatchID = d.BatchID
			row.BatchName = d.BatchName
			out.Rows = append(out.Rows, row)
		}
		sortStockMovementRows(out.Rows)
		out.Items = rollupStockMovementItems(out.Rows)
		return nil
	})
	if err != nil {
		return StockMovementReport{}, err
	}
	return out, nil
}

// allMovements projects every fetched movement, in or out of the window.
func allMovements(rows []stockMovementRow) []inventory.Movement {
	out := make([]inventory.Movement, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.movement)
	}
	return out
}

func sortStockMovementRows(rows []StockMovementRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.ItemName != b.ItemName {
			return a.ItemName < b.ItemName
		}
		if a.GodownName != b.GodownName {
			return a.GodownName < b.GodownName
		}
		if a.BatchName != b.BatchName {
			return a.BatchName < b.BatchName
		}
		if a.GodownID != b.GodownID {
			return a.GodownID.String() < b.GodownID.String()
		}
		return a.BatchID.String() < b.BatchID.String()
	})
}

// rollupStockMovementItems folds the movement rows into per-item totals, for the
// same reason rollupStockItems does: the total printed above the rows must be
// the rows.
func rollupStockMovementItems(rows []StockMovementRow) []StockMovementItemRollup {
	var out []StockMovementItemRollup
	idx := map[uuid.UUID]int{}
	for _, r := range rows {
		i, ok := idx[r.StockItemID]
		if !ok {
			i = len(out)
			idx[r.StockItemID] = i
			out = append(out, StockMovementItemRollup{
				StockItemID:  r.StockItemID,
				ItemName:     r.ItemName,
				UnitSymbol:   r.UnitSymbol,
				InwardQty:    decimal.Zero,
				InwardValue:  decimal.Zero,
				OutwardQty:   decimal.Zero,
				OutwardValue: decimal.Zero,
				NetQty:       decimal.Zero,
				NetValue:     decimal.Zero,
			})
		}
		g := &out[i]
		g.InwardQty = g.InwardQty.Add(r.InwardQty)
		g.InwardValue = g.InwardValue.Add(r.InwardValue)
		g.OutwardQty = g.OutwardQty.Add(r.OutwardQty)
		g.OutwardValue = g.OutwardValue.Add(r.OutwardValue)
		g.NetQty = g.NetQty.Add(r.NetQty)
		g.NetValue = g.NetValue.Add(r.NetValue)
		g.Positions = append(g.Positions, r)
	}
	return out
}
