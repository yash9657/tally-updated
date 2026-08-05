package reporting

// orderbook.go — WHAT IS STILL OUTSTANDING, per direction (T3, migration 0042).
//
//	OrderBookFor(ctx, companyID, SalesOrders, asOn)    -> report:sales-order-book
//	OrderBookFor(ctx, companyID, PurchaseOrders, asOn) -> report:purchase-order-book
//
// ===========================================================================
// ⛔⛔ AN ORDER IS A COMMITTED QUANTITY, NOT A MOVEMENT
// ===========================================================================
// Nothing in this file reads stock_movement, ledger_balance or posting, and
// nothing that computes closing stock, the trial balance or the balance sheet
// reads order_head, order_line or order_fulfilment. That separation is the whole
// T3 model: an order is a promise, and a promise is not an entry. See migration
// 0042's header for what pushing orders through the movement ledger would do to
// the balance sheet.
//
// ===========================================================================
// ⛔⛔ THE OUTSTANDING QUANTITY IS DERIVED HERE AND STORED NOWHERE
// ===========================================================================
// There is no fulfilled_qty column and there must never be one. It is summed on
// every read from order_fulfilment, and the SUM MUST JOIN
// voucher_head.current_version_id.
//
// That join is the single load-bearing line in this file. Fulfilment rows are
// VERSIONED with the voucher exactly as stock_movement rows are: an ALTER writes
// a NEW voucher version and re-states its links under it, leaving the superseded
// version's rows in the table forever. A sum without the join counts a corrected
// delivery TWICE — a 20-unit order delivered 5 and then corrected to 8 would read
// as 13 fulfilled and 7 outstanding, and every figure would look plausible. The
// negative-stock trigger in migration 0025 is the reference implementation of the
// same join and its comment says the same thing about the same hazard.
//
// `vh.is_deleted = false` is the second half of it: a DELETED voucher fulfils
// nothing. Its tombstone carries no fulfilments at all (ledger.post() nils the
// slice), so this is a second line of defence rather than the only one — but a
// tombstone is also a NEW current version, so without the flag a deleted delivery
// note would contribute zero rows and the clause would be redundant. It is kept
// because relying on "the tombstone happens to carry none" makes this query
// depend on a decision made in another package.
//
// ===========================================================================
// WHY as_on AND NOT from/to
// ===========================================================================
// Every other statement in this package takes a PERIOD, and this one takes a
// single date, deliberately. An outstanding balance is a POSITION — "what do we
// still owe, as at today" — in the way closing stock is, not a flow. A period
// would have to answer "outstanding as measured over July", which is not a
// quantity: an order placed in June and delivered in August is outstanding
// throughout July and belongs on the report, while its order date and every one
// of its fulfilments fall outside the window.
//
// So as_on bounds BOTH ends of the walk: orders placed on or before it, and
// fulfilments recorded on vouchers dated on or before it. Getting the second half
// wrong is the subtle version of this defect — bounding only the order date would
// deduct a September delivery from a July position and report an order as met
// weeks before it was.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// OrderDirection selects which half of order_head a report covers.
//
// ⛔ IT IS NOT A QUERY PARAMETER ON ONE REPORT, and that is an AUTHORISATION
// decision rather than a reporting one. internal/authz decides on the report
// NAME, so a single `report:order-book` serving both directions would make "the
// salesperson may see our sales pipeline but not what we have committed to pay
// suppliers" an unwritable rule — and that is an ordinary rule an ordinary
// business wants, because the two lists belong to two different jobs. Migration
// 0073's header records the same argument, and the cost reports reached it from
// the same place.
type OrderDirection string

const (
	// SalesOrders are commitments we have made to customers: what we have
	// promised to deliver.
	SalesOrders OrderDirection = "SALES"
	// PurchaseOrders are commitments made to us by suppliers, and by us to pay
	// for them.
	PurchaseOrders OrderDirection = "PURCHASE"
)

// Valid reports whether d is one of the two directions. Exported because the
// HTTP seam must refuse an unknown direction with a 400 rather than running a
// query that matches nothing and looks like an empty order book.
func (d OrderDirection) Valid() bool {
	return d == SalesOrders || d == PurchaseOrders
}

// OrderBookRow is ONE outstanding order line.
//
// The grain is the LINE and not the order, because fulfilment is per item: a
// three-line order routinely has one line complete and two outstanding, and an
// order-level row could only report that as a fraction nobody can act on.
type OrderBookRow struct {
	OrderID     uuid.UUID
	OrderNo     string
	OrderDate   string
	PartyLedger uuid.UUID
	Party       string

	OrderLineID uuid.UUID
	LineNo      int
	StockItemID uuid.UUID
	StockItem   string

	// Qty is what was COMMITTED; Fulfilled is what has been delivered against it
	// on current, non-deleted vouchers dated on or before as_on.
	Qty       decimal.Decimal
	Fulfilled decimal.Decimal

	// Rate is the ORDER's agreed rate, not the invoice's. The two may lawfully
	// differ (a price revision agreed after the order) and that difference is a
	// finding to show rather than a disagreement to resolve by overwriting either
	// figure — so this report values what is outstanding at what was AGREED,
	// which is the number a business is owed on.
	Rate decimal.Decimal

	// DueDate is "" when NO DATE WAS AGREED, which is common and is NOT the same
	// as overdue. See Overdue.
	DueDate string
}

// Outstanding is what is still to come: committed minus fulfilled.
//
// Clamped at zero rather than allowed negative. Over-fulfilment is refused by
// migration 0042's deferred trigger and by ledger.validateFulfilments, so a
// negative here would mean the invariant had been bypassed — and in that case a
// clamped zero on the report is the honest reading ("nothing further is owed")
// while a negative would be silently netted against another line's positive by
// TotalOutstandingValue and quietly understate the book.
func (r OrderBookRow) Outstanding() decimal.Decimal {
	out := r.Qty.Sub(r.Fulfilled)
	if out.IsNegative() {
		return decimal.Zero
	}
	return out
}

// OutstandingValue values what is left at the AGREED rate.
func (r OrderBookRow) OutstandingValue() decimal.Decimal {
	return r.Outstanding().Mul(r.Rate)
}

// Overdue reports whether an AGREED date has passed.
//
// ⛔ A LINE WITH NO DUE DATE IS NEVER OVERDUE, however old. Treating an absent
// date as "today" (or as the zero time, which is worse — every such line would be
// overdue by two thousand years) would invent a commitment the business never
// made, and the salesperson chasing it would be chasing nothing.
func (r OrderBookRow) Overdue(asOn string) bool {
	if r.DueDate == "" || asOn == "" {
		return false
	}
	due, err := time.Parse("2006-01-02", r.DueDate)
	if err != nil {
		return false
	}
	on, err := time.Parse("2006-01-02", asOn)
	if err != nil {
		return false
	}
	return due.Before(on)
}

// OrderBook is one direction's outstanding position as at a date.
type OrderBook struct {
	Direction OrderDirection
	AsOn      string
	Rows      []OrderBookRow
	// TotalOutstandingValue sums OutstandingValue across the rows. Meaningful
	// BECAUSE the report is one direction: sales commitments and purchase
	// commitments are opposite obligations and a total across them would net a
	// receivable against a payable into a number that describes nothing. That is
	// the same reason the report is split at all — see OrderDirection.
	TotalOutstandingValue decimal.Decimal
	Notes                 []string
}

// OrderBookFor computes one direction's outstanding order position.
//
// A line with nothing outstanding is EXCLUDED: an order book is a list of what is
// still owed, and a fully delivered line on it is a row an operator has to read
// and dismiss. A CLOSED order is excluded too — is_closed records a human
// decision to abandon it (see migration 0042) and an abandoned commitment is not
// outstanding, however little was delivered.
func (s *Service) OrderBookFor(
	ctx context.Context, companyID uuid.UUID, direction OrderDirection, asOn string,
) (OrderBook, error) {
	if err := scope(ctx, companyID); err != nil {
		return OrderBook{}, err
	}
	if !direction.Valid() {
		return OrderBook{}, fmt.Errorf(
			"reporting: order direction %q is neither %s nor %s — the two are separate reports because "+
				"they are separate authorities, not one report with a filter",
			string(direction), SalesOrders, PurchaseOrders)
	}
	on := strings.TrimSpace(asOn)
	if on != "" {
		if _, err := time.Parse("2006-01-02", on); err != nil {
			return OrderBook{}, fmt.Errorf("reporting: as_on date %q is not yyyy-mm-dd", asOn)
		}
	}

	out := OrderBook{
		Direction:             direction,
		AsOn:                  on,
		TotalOutstandingValue: decimal.Zero,
	}

	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := loadOrderBookRows(ctx, tx, direction, on)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if !r.Outstanding().IsPositive() {
				continue
			}
			out.Rows = append(out.Rows, r)
			out.TotalOutstandingValue = out.TotalOutstandingValue.Add(r.OutstandingValue())
		}
		return nil
	})
	if err != nil {
		return OrderBook{}, err
	}
	out.Notes = orderBookNotes(out)
	return out, nil
}

// loadOrderBookRows reads every line of every open order in one direction, with
// its fulfilled quantity as at `asOn`.
//
// ⛔ THE FULFILMENT SUM IS A CORRELATED SUBQUERY WITH THREE BOUNDS, AND EVERY ONE
// OF THEM IS LOAD-BEARING:
//
//	vh.current_version_id = f.voucher_version_id — fulfilments are VERSIONED, and
//	  the superseded version's rows stay in the table. Without it an altered
//	  delivery is counted twice and the outstanding balance silently understates.
//	  This is the same join stock_movement_not_negative uses, for the same reason.
//	vh.is_deleted = false — a deleted voucher fulfils nothing.
//	vh.voucher_date <= asOn — the AS-AT bound on the DELIVERY, not just on the
//	  order. Bounding only the order date would deduct a September delivery from a
//	  July position and report the order as met weeks before it was.
//
// A LEFT JOIN, not an inner one: a line with no fulfilment at all is the most
// outstanding line there is and must not be dropped. COALESCE turns its NULL into
// zero.
func loadOrderBookRows(
	ctx context.Context, tx pgx.Tx, direction OrderDirection, asOn string,
) ([]OrderBookRow, error) {
	// A single nullable bind for the date bound: NULL means "no bound", so one
	// query shape serves both cases rather than two concatenated strings that can
	// drift apart.
	var bound any
	if asOn != "" {
		bound = asOn
	}
	const q = `
		SELECT oh.id, oh.order_no, oh.order_date, oh.party_ledger_id,
		       COALESCE(l.name, ''),
		       ol.id, ol.line_no, ol.stock_item_id, COALESCE(si.name, ''),
		       ol.qty, ol.rate, ol.due_date,
		       COALESCE((
		           SELECT SUM(f.qty)
		             FROM order_fulfilment f
		             JOIN voucher_head vh
		               ON vh.company_id         = f.company_id
		              AND vh.current_version_id = f.voucher_version_id
		            WHERE f.company_id    = ol.company_id
		              AND f.order_line_id = ol.id
		              AND vh.is_deleted   = false
		              AND ($2::date IS NULL OR vh.voucher_date <= $2::date)
		       ), 0)
		  FROM order_line ol
		  JOIN order_head oh
		    ON oh.company_id = ol.company_id AND oh.id = ol.order_id
		  LEFT JOIN ledger l
		    ON l.company_id = oh.company_id AND l.id = oh.party_ledger_id
		  LEFT JOIN stock_item si
		    ON si.company_id = ol.company_id AND si.id = ol.stock_item_id
		 WHERE oh.direction = $1::order_direction
		   AND oh.is_closed = false
		   AND ($2::date IS NULL OR oh.order_date <= $2::date)
		 ORDER BY oh.order_date, lower(btrim(oh.order_no)), oh.id, ol.line_no`
	rs, err := tx.Query(ctx, q, string(direction), bound)
	if err != nil {
		return nil, fmt.Errorf("reporting: load order book: %w", err)
	}
	defer rs.Close()

	var out []OrderBookRow
	for rs.Next() {
		var r OrderBookRow
		var orderDate time.Time
		var due *time.Time
		if err := rs.Scan(
			&r.OrderID, &r.OrderNo, &orderDate, &r.PartyLedger, &r.Party,
			&r.OrderLineID, &r.LineNo, &r.StockItemID, &r.StockItem,
			&r.Qty, &r.Rate, &due, &r.Fulfilled,
		); err != nil {
			return nil, fmt.Errorf("reporting: scan order book row: %w", err)
		}
		r.OrderDate = orderDate.Format("2006-01-02")
		if due != nil {
			r.DueDate = due.Format("2006-01-02")
		}
		out = append(out, r)
	}
	if err := rs.Err(); err != nil {
		return nil, fmt.Errorf("reporting: load order book: %w", err)
	}
	return out, nil
}

// orderBookNotes states on the artefact what a reader would otherwise have to
// know from the source.
//
// Every note here is a claim about THESE numbers rather than about the software,
// which is the distinction HANDOFF §7.1 draws: a note that can only be true or
// false about the product goes stale silently, while one computed from the rows
// in front of the reader cannot.
func orderBookNotes(b OrderBook) []string {
	var notes []string

	what := "we have promised to deliver"
	if b.Direction == PurchaseOrders {
		what = "suppliers have promised to deliver to us"
	}
	notes = append(notes, fmt.Sprintf(
		"Outstanding quantities and value at the ORDER's agreed rate: what %s and has not yet moved. "+
			"An order is a committed quantity and not a movement, so nothing on this report affects "+
			"closing stock, the Profit & Loss or the Balance Sheet.", what))

	if b.AsOn != "" {
		notes = append(notes, fmt.Sprintf(
			"As at %s: orders placed on or before that date, less deliveries recorded on vouchers "+
				"dated on or before it.", b.AsOn))
	} else {
		notes = append(notes,
			"No as-at date given, so this is the position from every order ever placed, less every "+
				"delivery ever recorded.")
	}

	var undated, overdue int
	for _, r := range b.Rows {
		if r.DueDate == "" {
			undated++
		} else if r.Overdue(b.AsOn) {
			overdue++
		}
	}
	if undated > 0 {
		notes = append(notes, fmt.Sprintf(
			"%d of %d outstanding line(s) carry NO agreed due date. They are not overdue and are not "+
				"counted as such — no date was ever promised for them.", undated, len(b.Rows)))
	}
	if overdue > 0 {
		notes = append(notes, fmt.Sprintf(
			"%d outstanding line(s) are past their agreed due date.", overdue))
	}
	if len(b.Rows) == 0 {
		notes = append(notes,
			"Nothing is outstanding. Fully delivered lines and orders closed by hand are excluded, "+
				"so an empty report means every commitment is either met or abandoned — not that no "+
				"orders exist.")
	}
	return notes
}
