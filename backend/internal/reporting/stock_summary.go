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
	"strings"
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
	// StockGroupName is the item's group (T10), or "" for an ungrouped item.
	// It is carried on the POSITION as well as on the rollup so that a client
	// reading the flat `Rows` list (the export does) can group it without a
	// second lookup.
	StockGroupName string

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
	// ValuationMethod is the basis every figure BELOW this line was derived on,
	// and it belongs on the item rather than on a position because that is where
	// stock_item.valuation_method lives: one item is valued one way in every
	// godown it sits in.
	//
	// ⛔ IT IS HERE BECAUSE T9 MADE IT MEAN SOMETHING. While WEIGHTED_AVG was the
	// only method, a rollup that omitted it was merely incomplete; with three
	// methods in one company (they can differ item by item) a Stock Summary that
	// prints values and NOT the basis is a report whose numbers cannot be
	// reproduced or compared — the same item total means different things on two
	// lines and nothing on the page says which.
	ValuationMethod string
	// StockGroupName is the item's group (T10), "" for an ungrouped item.
	StockGroupName string
	// BaseUnitSymbol and ClosingBaseQty RESTATE the closing quantity in the
	// unit's base unit when the item's unit is COMPOUND: 5 BOX of a "BOX of 12
	// NOS" unit closes at 5.0000 BOX and 60.0000 NOS.
	//
	// ⛔ IT IS A DERIVED FIGURE AND THE STORED QUANTITY IS THE OTHER ONE. Nothing
	// converts a quantity on the way in (migration 0079's header says why: an
	// inbound conversion loses the number that was keyed and cannot be restated
	// when a factor changes), so this is multiplication at READ time — exactly
	// like value. Both are empty/zero for a simple unit, and a client must render
	// nothing rather than "0.0000 " in that case.
	BaseUnitSymbol   string
	ClosingBaseQty   decimal.Decimal
	ConversionFactor decimal.Decimal

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

// StockGroupRollup is one STOCK GROUP's total across every item in it AND in
// every group beneath it (T10) — the third level of the Stock Summary's tree,
// above the items.
//
// ⛔⛔ THE TOTAL IS THE WHOLE BRANCH, AND THAT IS WHAT A STOCK GROUP IS FOR. A
// group total that counted only its DIRECT items would be a number no operator
// asked for: "Raw Materials" exists so that a company with four hundred items
// can read one line, and if that line excluded "Raw Materials > Packing" it
// would be smaller than the sum of what is printed underneath it and nothing on
// the page would explain the difference. The branch is resolved through the
// ltree path, which is why loadStockNames reads it.
//
// ⚠️ AN ITEM APPEARS IN EVERY ANCESTOR'S TOTAL, so these totals DO NOT SUM to
// the company's closing stock — TotalValue is the sum of the POSITIONS, never of
// the groups. A client that adds the group rows up will double-count every
// nested branch. (The same rule the Cost Category Summary states for its axes,
// for the same reason.)
type StockGroupRollup struct {
	// GroupName is "" for the UNGROUPED bucket, which is a real bucket: every
	// item that predates T10 has no group, and a report that hid them would show
	// a company holding less stock than it does.
	GroupName string
	// Path is the ltree ancestry, "" for the ungrouped bucket. Depth is the
	// number of labels in it, so a client can indent the tree without parsing.
	Path  string
	Depth int

	OpeningValue decimal.Decimal
	InwardValue  decimal.Decimal
	OutwardValue decimal.Decimal
	ClosingValue decimal.Decimal
	// ItemCount is how many items are in this branch — the figure that says
	// whether reading the group line saved you anything.
	ItemCount int
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
	AsOn  string
	Items []StockItemRollup
	// Groups are the STOCK GROUP subtotals (T10), in tree order — a parent
	// always precedes its children — with the UNGROUPED bucket last.
	//
	// ⚠️ THEY DO NOT SUM TO TotalValue. Every item counts in every ancestor's
	// total, which is what makes a branch total useful and what makes adding the
	// rows up wrong. See StockGroupRollup.
	Groups     []StockGroupRollup
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
//
// ⛔⛔ THE voucher_type JOIN IS D103, AND IT IS THE ONLY THING THAT TELLS THE
// VALUER A DOCUMENT MUST CONSERVE VALUE. A Stock Journal moves goods between
// the company's OWN shelves and posts NO accounting lines, so whatever its
// inward legs are carried at must be exactly what its outward legs consumed —
// see inventory/conserving.go for what a FIFO transfer did to the Balance Sheet
// without this. The class is NOT a column on the voucher: it lives on the TYPE
// (voucher_type.parent_class, UPPER_SNAKE, seeded by migration 0029 as the
// form ledger.defaultVoucherTypeID resolves), so the flag has to be joined in
// here or the leaf cannot know.
//
// The join is LEFT and the comparison COALESCEd because voucher_type_id is
// NULLABLE (see migration 0034's header on what a missing seeded type does):
// a movement whose type cannot be resolved is treated as NOT conserving, which
// is the same behaviour every class other than the Stock Journal already has.
//
// ⚠️ IF THE STRING DRIFTS, VALUE CREATION COMES BACK SILENTLY. The literal is
// pinned end-to-end by TestAFifoTransferBetweenOurOwnGodownsConservesValue,
// which posts a real Stock Journal through ledger.PostVoucher and reads the
// Stock Summary back: a renamed parent_class fails it, rather than quietly
// turning the rule off.
const stockMovementSQL = `
	SELECT sm.stock_item_id, sm.godown_id, sm.batch_id,
	       sm.direction::text, sm.qty, sm.rate, sm.discount_pct,
	       vv.voucher_date, COALESCE(vv.voucher_no, ''), vv.voucher_id,
	       vv.created_at, sm.line_no, vv.id,
	       COALESCE(vt.parent_class, '') = 'STOCK_JOURNAL' AS voucher_conserves,
	       COALESCE($1::date IS NULL OR vv.voucher_date >= $1::date, true) AS in_window
	  FROM stock_movement sm
	  JOIN voucher_version vv ON vv.id = sm.voucher_version_id
	  JOIN voucher_head vh
	    ON vh.company_id = vv.company_id
	   AND vh.voucher_id = vv.voucher_id
	   AND vh.current_version_id = vv.id
	  LEFT JOIN voucher_type vt ON vt.id = vv.voucher_type_id
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
			// ⛔ voucherID is the REPLAY ORDERING key (D8), voucherVersionID is the
			// final tiebreak. They are different columns and an ALTER moves only the
			// second one; scanning one into the other would make an alteration look
			// like a new voucher entered now.
			voucherID uuid.UUID
			versionID uuid.UUID
		)
		if err := rows.Scan(&itemID, &godownID, &batchID,
			&direction, &r.movement.Qty, &r.movement.Rate, &r.movement.DiscountPct,
			&date, &r.movement.VoucherNo, &voucherID,
			&r.movement.VersionCreatedAt, &r.movement.LineNo, &versionID,
			&r.movement.VoucherConserves,
			&r.inWindow); err != nil {
			return nil, fmt.Errorf("reporting: scan stock movement: %w", err)
		}
		if date != nil {
			r.movement.VoucherDate = *date
		}
		r.movement.Position = positionKey(itemID, godownID, batchID)
		r.movement.Direction = inventory.Direction(direction)
		r.movement.VoucherID = voucherID.String()
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
	// StockGroupName is the item's group (T10), or "" for an UNGROUPED item —
	// which is a real state and not a missing value, so the rollup renders it as
	// a stated bucket rather than dropping the item or filing it under a group
	// nobody created.
	StockGroupName string
	// StockGroupPath is the group's ltree ancestry, which is what makes the
	// group tree sort into tree order (a parent always precedes its children)
	// without the report sorting anything itself.
	StockGroupPath string
	// BaseUnitSymbol / ConversionFactor describe a COMPOUND unit (1 BOX = 12
	// NOS). They are how a quantity stored in BOX is RESTATED in NOS at report
	// time; nothing converts a quantity on the way in. Empty/zero for a simple
	// unit.
	BaseUnitSymbol   string
	ConversionFactor decimal.Decimal
}

// stockNames is the master-data a stock report renders with, read once per
// report rather than joined onto every movement row: the movement query already
// returns one row per line of every voucher ever posted and does not need three
// more joins on top of that.
type stockNames struct {
	items   map[uuid.UUID]stockItemMeta
	godowns map[uuid.UUID]string
	batches map[uuid.UUID]string
	// groupNames maps a stock group's LTREE PATH to its name (T10). It is keyed
	// by path rather than by id because the group rollup walks an item's ancestry
	// as path prefixes — every prefix is an ancestor — and would otherwise need a
	// second map from path to id to get there.
	groupNames map[string]string
}

func loadStockNames(ctx context.Context, tx pgx.Tx) (stockNames, error) {
	n := stockNames{
		items:      map[uuid.UUID]stockItemMeta{},
		godowns:    map[uuid.UUID]string{},
		batches:    map[uuid.UUID]string{},
		groupNames: map[string]string{},
	}

	// ⚠️ THREE LEFT JOINS, AND EVERY ONE OF THEM IS A LEFT JOIN FOR A REASON. An
	// item may carry no unit (the server allows unit_id NULL), no stock group
	// (ungrouped is a real state), and its unit may be simple (no base). An inner
	// join on any of the three would make the ITEM VANISH from the report — its
	// stock would exist in the movement ledger and appear nowhere, which is worse
	// than any wrong figure because nothing on the page would be missing.
	rows, err := tx.Query(ctx,
		`SELECT si.id, si.name, COALESCE(u.symbol, ''), si.valuation_method,
		        COALESCE(sg.name, ''), COALESCE(sg.path::text, ''),
		        COALESCE(bu.symbol, ''), COALESCE(u.conversion_factor, 0)
		   FROM stock_item si
		   LEFT JOIN unit u         ON u.id  = si.unit_id
		   LEFT JOIN stock_group sg ON sg.id = si.stock_group_id
		   LEFT JOIN unit bu        ON bu.id = u.base_unit_id`)
	if err != nil {
		return n, fmt.Errorf("reporting: query stock items: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id uuid.UUID
			m  stockItemMeta
		)
		if err := rows.Scan(&id, &m.Name, &m.UnitSymbol, &m.ValuationMethod,
			&m.StockGroupName, &m.StockGroupPath, &m.BaseUnitSymbol, &m.ConversionFactor); err != nil {
			return n, fmt.Errorf("reporting: scan stock item: %w", err)
		}
		n.items[id] = m
	}
	if err := rows.Err(); err != nil {
		return n, fmt.Errorf("reporting: iterate stock items: %w", err)
	}
	rows.Close()

	grows, err := tx.Query(ctx, `SELECT path::text, name FROM stock_group`)
	if err != nil {
		return n, fmt.Errorf("reporting: query stock groups: %w", err)
	}
	for grows.Next() {
		var path, name string
		if err := grows.Scan(&path, &name); err != nil {
			grows.Close()
			return n, fmt.Errorf("reporting: scan stock group: %w", err)
		}
		n.groupNames[path] = name
	}
	if err := grows.Err(); err != nil {
		grows.Close()
		return n, fmt.Errorf("reporting: iterate stock groups: %w", err)
	}
	grows.Close()

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

// replayPositions replays EVERY position of EVERY item in ONE coupled pass,
// handing the leaf a selector that resolves each item's own valuer.
//
// PER-ITEM VALUERS ARE NOT AN OPTIMISATION: stock_item.valuation_method is per
// item, so two items in the same report are valued on different bases.
// ValuerFor returns an ERROR for a method it does not implement and this
// propagates it — a report that quietly fell back to weighted average for a
// FIFO item would print a plausible number under the wrong heading, which is
// the failure mode the leaf's default arm exists to prevent.
//
// ⛔⛔ IT USED TO REPLAY EACH ITEM SEPARATELY, AND THAT WAS HALF OF D103. The
// legs of a Stock Journal land in different POSITIONS and, on a manufacture, in
// different ITEMS; an inward leg cannot be carried at what its paired outward
// leg consumed if the outward was valued in a replay this one never sees. One
// pass is what makes the coupling expressible at all — inventory.ReplayCompany
// owns the rule itself.
//
// ⚠️ EVERY ITEM IS RESOLVED UP FRONT, IN A DETERMINISTIC ORDER, rather than
// lazily inside the replay. Two items can BOTH be unvaluable, and a report that
// named whichever one the movement stream happened to reach first would produce
// a different error message on the same data from one call to the next — an
// irreproducible failure, which is the worst property a refusal can have.
func replayPositions(
	openings []inventory.Opening, movements []inventory.Movement, names stockNames,
) (map[inventory.PositionKey]inventory.Position, error) {
	var itemOrder []string
	seen := map[string]bool{}
	note := func(item string) {
		if !seen[item] {
			seen[item] = true
			itemOrder = append(itemOrder, item)
		}
	}
	for _, o := range openings {
		note(o.Position.StockItemID)
	}
	for _, m := range movements {
		note(m.Position.StockItemID)
	}

	valuers := make(map[string]inventory.Valuer, len(itemOrder))
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
		valuers[item] = valuer
	}

	return inventory.ReplayCompany(openings, movements, func(item string) (inventory.Valuer, error) {
		v, ok := valuers[item]
		if !ok {
			// Unreachable: itemOrder above is built from these exact inputs.
			return nil, fmt.Errorf("reporting: stock item %s reached the replay unresolved", item)
		}
		return v, nil
	})
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
		names, err := loadStockNames(ctx, tx)
		if err != nil {
			return err
		}
		out.Rows = rows
		out.TotalValue = totalClosingValue(out.Rows)
		out.Items = rollupStockItems(out.Rows, names)
		out.Groups = rollupStockGroups(out.Items, names)
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
		StockGroupName:  meta.StockGroupName,
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
// ⚠️ IT TAKES `names` FOR T10 AND FOR NOTHING ELSE. The group, the base unit and
// the conversion factor are properties of the ITEM MASTER, not of a movement, so
// they cannot come off the rows the way the valuation method does — and the
// alternative (a second query here) is the one this function's own doc forbids.
// The names map has already been read once for this report.
func rollupStockItems(rows []StockPositionRow, names stockNames) []StockItemRollup {
	var out []StockItemRollup
	idx := map[uuid.UUID]int{}
	for _, r := range rows {
		i, ok := idx[r.StockItemID]
		if !ok {
			i = len(out)
			idx[r.StockItemID] = i
			out = append(out, StockItemRollup{
				StockItemID: r.StockItemID,
				ItemName:    r.ItemName,
				UnitSymbol:  r.UnitSymbol,
				// Taken from the position rows, which is where the replay put
				// it: every position of one item shares the item's method, so
				// the first row to arrive states it for the rollup. Re-querying
				// stock_item here would be a second read of the same fact, able
				// to disagree with the basis the figures were actually derived on.
				ValuationMethod: r.ValuationMethod,
				StockGroupName:  r.StockGroupName,
				OpeningQty:      decimal.Zero,
				OpeningValue:    decimal.Zero,
				InwardQty:       decimal.Zero,
				InwardValue:     decimal.Zero,
				OutwardQty:      decimal.Zero,
				OutwardValue:    decimal.Zero,
				ClosingQty:      decimal.Zero,
				ClosingValue:    decimal.Zero,
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
	// THE COMPOUND-UNIT RESTATEMENT, once per item, after the fold — because it
	// is a function of the item's FINAL closing quantity and doing it per row
	// would restate each position separately and then add up the roundings.
	for i := range out {
		meta := names.items[out[i].StockItemID]
		if meta.BaseUnitSymbol == "" || !meta.ConversionFactor.IsPositive() {
			continue
		}
		out[i].BaseUnitSymbol = meta.BaseUnitSymbol
		out[i].ConversionFactor = meta.ConversionFactor
		// Rounded to the money/quantity scale the columns carry, exactly as every
		// other derived quantity in this package is: 4 dp.
		out[i].ClosingBaseQty = out[i].ClosingQty.Mul(meta.ConversionFactor).Round(4)
	}
	return out
}

// rollupStockGroups folds the item rollups into STOCK GROUP subtotals (T10),
// with every item counted in EVERY ANCESTOR of its group.
//
// ⛔ THE BRANCH, NOT THE LEVEL. A group total that counted only its direct items
// would print a figure smaller than the rows beneath it whenever a group has
// children, with nothing on the page to explain the gap. The ltree path is what
// makes "every ancestor" cheap: "g1.s2.s3" contains its own ancestors as
// prefixes, so one pass over the labels adds an item to each.
//
// ⚠️ THE RESULT DOES NOT SUM TO THE COMPANY'S CLOSING STOCK, deliberately, and
// StockGroupRollup's doc says so out loud: an item in a nested group is in two
// totals. The company total is the sum of the POSITIONS.
//
// The UNGROUPED bucket comes LAST and is named "" — every item that predates T10
// is in it, and a report that dropped it would show a company holding less stock
// than it does.
func rollupStockGroups(items []StockItemRollup, names stockNames) []StockGroupRollup {
	byPath := map[string]*StockGroupRollup{}
	ungrouped := &StockGroupRollup{}

	for _, it := range items {
		meta := names.items[it.StockItemID]
		if meta.StockGroupPath == "" {
			ungrouped.ItemCount++
			ungrouped.OpeningValue = ungrouped.OpeningValue.Add(it.OpeningValue)
			ungrouped.InwardValue = ungrouped.InwardValue.Add(it.InwardValue)
			ungrouped.OutwardValue = ungrouped.OutwardValue.Add(it.OutwardValue)
			ungrouped.ClosingValue = ungrouped.ClosingValue.Add(it.ClosingValue)
			continue
		}
		// Every prefix of the path is an ancestor group, including the path
		// itself. names.groupsByPath gives each one its NAME; a path with no
		// group behind it cannot happen (the FK guarantees it) and is skipped
		// rather than invented.
		labels := strings.Split(meta.StockGroupPath, ".")
		for i := range labels {
			prefix := strings.Join(labels[:i+1], ".")
			name, ok := names.groupNames[prefix]
			if !ok {
				continue
			}
			g, seen := byPath[prefix]
			if !seen {
				g = &StockGroupRollup{
					GroupName:    name,
					Path:         prefix,
					Depth:        i,
					OpeningValue: decimal.Zero,
					InwardValue:  decimal.Zero,
					OutwardValue: decimal.Zero,
					ClosingValue: decimal.Zero,
				}
				byPath[prefix] = g
			}
			g.ItemCount++
			g.OpeningValue = g.OpeningValue.Add(it.OpeningValue)
			g.InwardValue = g.InwardValue.Add(it.InwardValue)
			g.OutwardValue = g.OutwardValue.Add(it.OutwardValue)
			g.ClosingValue = g.ClosingValue.Add(it.ClosingValue)
		}
	}

	out := make([]StockGroupRollup, 0, len(byPath)+1)
	for _, g := range byPath {
		out = append(out, *g)
	}
	// PATH ORDER IS TREE ORDER: a parent's path is a prefix of its children's, so
	// sorting the paths as strings puts every parent immediately above its own
	// subtree. Nothing else needs to know the shape of the tree.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	if ungrouped.ItemCount > 0 {
		out = append(out, *ungrouped)
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
