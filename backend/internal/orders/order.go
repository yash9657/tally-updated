// Package orders is the WRITE side of sales and purchase orders and of the bill
// of materials (T3, migrations 0042 and 0043).
//
// ===========================================================================
// ⛔⛔ AN ORDER IS A COMMITTED QUANTITY, NOT A MOVEMENT
// ===========================================================================
// The stock ledger has exactly ONE primitive: a positive quantity plus a
// direction (stock_movement, migration 0025). An order is neither. Nothing has
// moved when a customer orders 20 units — the goods are still on our shelf, still
// ours, and still in closing stock at their full value.
//
// So this package never touches stock_movement, posting, voucher_version or
// ledger_balance, and internal/archtest pins that MECHANICALLY: its allowed
// import set is {internal/platform/db} and nothing else. An edge to
// internal/ledger would be the beginning of "while we are recording this order,
// also move the stock", which corrupts closing stock and therefore the balance
// sheet, compounding with every open order and reconciling against nothing.
//
// ⚠️ THE LINK IN THE OTHER DIRECTION EXISTS AND IS NOT THIS PACKAGE'S. The
// ledger owns order_fulfilment (internal/ledger/fulfilment.go), because a
// fulfilment is written INSIDE the voucher's transaction from the same in-memory
// slice as the hashed payload. The ledger reaches the order tables; this package
// must not reach the ledger. The table is the shared vocabulary, not a call.
//
// ===========================================================================
// ⛔⛔ NOTHING HERE STORES A FULFILLED QUANTITY OR A STATUS
// ===========================================================================
// Both are DERIVED, by summing order_fulfilment across CURRENT, non-deleted
// voucher versions — exactly as stock VALUE is derived by replaying movements and
// is never stored. Read migration 0042's header before adding a column: a stored
// fulfilled_qty goes stale undetectably the first time a fulfilling voucher is
// ALTERED, because an ALTER writes a NEW voucher version and re-states its links
// without ever visiting this table. The order would sit at "15 outstanding"
// forever while the corrected delivery note said 8, and no reconciliation
// recovers from that because both numbers look equally authoritative.
//
// `is_closed` IS stored, and it is the exception that proves the rule: it records
// a HUMAN DECISION to abandon an order ("the customer cancelled"). Nothing can
// compute that — an order nobody ever delivered and one that was cancelled have
// identical fulfilment histories.
//
// The read side is the order book in internal/reporting, which stays read-only.
package orders

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/platform/db"
)

// ErrNotFound is returned when an order (or BOM) id does not name a row in this
// company. A sentinel so the HTTP layer answers 404 rather than 500, and so a
// test can assert the specific failure rather than "some error happened".
var ErrNotFound = errors.New("orders: no such order in this company")

// ErrConflict is returned when a write would violate a uniqueness rule an
// operator can see and fix — a duplicate order number, a duplicate recipe name,
// a second default BOM for one item. A sentinel so the HTTP layer answers 409:
// a 400 would say "your request is malformed" when it is not, and a 500 would
// say "we broke" when the operator can resolve it by choosing another name.
var ErrConflict = errors.New("orders: conflicts with an existing row")

// ErrStillFulfilled is returned when a revision would leave delivered goods
// standing against a commitment that no longer covers them.
var ErrStillFulfilled = errors.New("orders: more is already fulfilled than the revision would commit")

// TxRunner is the minimal database capability this package needs.
type TxRunner interface {
	RunInTx(ctx context.Context, fn db.TxFunc) error
}

// Service is the orders/BOM write side over a transaction runner.
type Service struct{ runner TxRunner }

// NewService constructs an orders Service.
func NewService(runner TxRunner) *Service { return &Service{runner: runner} }

// Direction is SALES or PURCHASE.
//
// ⛔ ONE TYPE AND ONE PAIR OF TABLES, not sales_order + purchase_order. Migration
// 0042's header records why: two head tables mean two copies of every constraint,
// index and trigger, and the copy that drifts is the one somebody reaches first.
//
// ⚠️ THE REPORT SURFACE GOES THE OTHER WAY — two report NAMES, because
// authorisation decides on the name and a query parameter is invisible to the PDP.
// Neither should be "made consistent" with the other.
type Direction string

const (
	Sales    Direction = "SALES"
	Purchase Direction = "PURCHASE"
)

// Valid reports whether d is one of the two directions.
func (d Direction) Valid() bool { return d == Sales || d == Purchase }

// LineInput is one committed item quantity.
type LineInput struct {
	StockItemID uuid.UUID
	// Qty is the COMMITTED quantity, always positive: Direction carries the
	// sense, exactly as stock_movement.qty leaves the sign to `direction`.
	Qty decimal.Decimal
	// Rate is the AGREED rate. An input to the order book's value column and
	// never a valuation: the invoice that fulfils this line may lawfully bill a
	// different rate, and that difference is a finding to show rather than a
	// disagreement to resolve by overwriting either figure.
	Rate decimal.Decimal
	// DueDate is "" when no date was agreed, which is common and is NOT overdue.
	// A report that treated an absent date as today's would invent a commitment.
	DueDate string
}

// Input is a whole order as an operator states it.
type Input struct {
	Direction     Direction
	OrderNo       string
	OrderDate     string
	PartyLedgerID uuid.UUID
	Narration     string
	CreatedBy     string
	Lines         []LineInput
}

// Validate refuses what the database would refuse anyway, with a sentence
// instead of a constraint name.
//
// ⚠️ IT DOES NOT CHECK WHAT NEEDS A ROW. That the party ledger and every stock
// item exist, and that the order number is unique, are decided by the foreign
// keys and the unique index inside the transaction — a pre-check here would be a
// second copy that can disagree, and the copy that drifts always wins for
// whichever caller reaches it first.
func (in Input) Validate() error {
	if !in.Direction.Valid() {
		return fmt.Errorf(
			"orders: direction must be %s or %s, not %q — the two are separate objects with separate "+
				"authorities, and the direction decides which way goods must move to fulfil the order",
			Sales, Purchase, in.Direction)
	}
	if strings.TrimSpace(in.OrderNo) == "" {
		return errors.New(
			"orders: an order needs a number — it is what a customer or supplier quotes back, and " +
				"what a delivery note is matched against by hand when something does not add up")
	}
	if strings.TrimSpace(in.OrderDate) == "" {
		return errors.New("orders: an order needs a date")
	}
	if in.PartyLedgerID == uuid.Nil {
		// ⛔ THE ZERO UUID PARSES (HANDOFF §7.3), so it is refused explicitly here
		// rather than left to the FK: an order with the all-zeros party would fail
		// on a constraint naming a table the operator has never heard of, for a
		// mistake entirely about the form they filled in.
		return fmt.Errorf(
			"orders: a %s order needs a party ledger — the %s it commits with; without one the order "+
				"book cannot group by counterparty and nobody knows who is owed what",
			strings.ToLower(string(in.Direction)), in.counterparty())
	}
	if len(in.Lines) == 0 {
		return errors.New(
			"orders: an order with no lines commits nothing — there is no quantity to be outstanding, " +
				"so it would sit on the order book forever with nothing to deliver")
	}
	for i, l := range in.Lines {
		where := fmt.Sprintf(" on line %d", i+1)
		if l.StockItemID == uuid.Nil {
			return errors.New("orders: every line must name a stock item" + where)
		}
		if !l.Qty.IsPositive() {
			return fmt.Errorf(
				"orders: the committed quantity must be more than zero%s — a commitment is a MAGNITUDE "+
					"and the order's direction carries the sense, so a zero or negative line is not a "+
					"smaller order but an outstanding balance that means nothing", where)
		}
		if l.Rate.IsNegative() {
			return fmt.Errorf("orders: the agreed rate cannot be negative%s", where)
		}
	}
	// The same item twice is LEGAL here, unlike a BOM component: two lines for the
	// same item at two agreed rates (a hundred at the old price, fifty at the new)
	// is an ordinary commercial order, and fulfilment is per LINE so the two stay
	// separately outstanding.
	return nil
}

func (in Input) counterparty() string {
	if in.Direction == Purchase {
		return "supplier"
	}
	return "customer"
}

// Line is one order line read back, with its DERIVED position.
type Line struct {
	ID          uuid.UUID
	LineNo      int
	StockItemID uuid.UUID
	StockItem   string
	Qty         decimal.Decimal
	Rate        decimal.Decimal
	DueDate     string
	// Fulfilled is summed from order_fulfilment across CURRENT, non-deleted
	// voucher versions. NOT a column — see the package header.
	Fulfilled decimal.Decimal
}

// Outstanding is what is still to come. Clamped at zero: over-fulfilment is
// refused by 0042's trigger, so a negative would mean the invariant was bypassed,
// and in that case "nothing further is owed" is the honest reading while a
// negative would net against another line somewhere and understate the book.
func (l Line) Outstanding() decimal.Decimal {
	out := l.Qty.Sub(l.Fulfilled)
	if out.IsNegative() {
		return decimal.Zero
	}
	return out
}

// Order is a whole order read back.
type Order struct {
	ID            uuid.UUID
	Direction     Direction
	OrderNo       string
	OrderDate     string
	PartyLedgerID uuid.UUID
	Party         string
	Narration     string
	IsClosed      bool
	ClosedReason  string
	CreatedBy     string
	Lines         []Line
}

// Create writes one order and its lines in ONE transaction.
//
// One transaction and not two calls, because an order head with no lines commits
// nothing and would sit on the order book forever with nothing to deliver — the
// state Validate refuses above must also be impossible to reach by a partial
// write.
func (s *Service) Create(ctx context.Context, in Input) (uuid.UUID, error) {
	if err := in.Validate(); err != nil {
		return uuid.Nil, err
	}
	orderID, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("orders: new order id: %w", err)
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return uuid.Nil, errors.New("orders: no company in context")
	}
	createdBy := strings.TrimSpace(in.CreatedBy)
	if createdBy == "" {
		createdBy = "unknown"
	}

	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO order_head
			   (id, company_id, direction, order_no, order_date, party_ledger_id,
			    narration, created_by)
			 VALUES ($1, $2, $3::order_direction, $4, $5::date, $6, $7, $8)`,
			orderID, companyID, string(in.Direction), strings.TrimSpace(in.OrderNo),
			in.OrderDate, in.PartyLedgerID, in.Narration, createdBy,
		); err != nil {
			return wrapOrderWrite(err, in)
		}
		for i, l := range in.Lines {
			lineID, err := uuid.NewV7()
			if err != nil {
				return fmt.Errorf("orders: new order line id: %w", err)
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO order_line
				   (id, company_id, order_id, line_no, stock_item_id, qty, rate, due_date)
				 VALUES ($1, $2, $3, $4, $5, $6, $7, $8::date)`,
				lineID, companyID, orderID, i+1, l.StockItemID, l.Qty, l.Rate,
				nullIfBlank(l.DueDate),
			); err != nil {
				return fmt.Errorf("orders: insert order line %d: %w", i+1, err)
			}
		}
		return nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	return orderID, nil
}

// Get reads one order with its lines and their derived fulfilment.
func (s *Service) Get(ctx context.Context, orderID uuid.UUID) (Order, error) {
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return Order{}, errors.New("orders: no company in context")
	}
	var out Order
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		o, err := readOrderHead(ctx, tx, companyID, orderID)
		if err != nil {
			return err
		}
		lines, err := readOrderLines(ctx, tx, companyID, orderID)
		if err != nil {
			return err
		}
		o.Lines = lines
		out = o
		return nil
	})
	return out, err
}

// List returns a company's orders in one direction, newest first, WITHOUT their
// lines.
//
// Without the lines deliberately: a list is for choosing which order to open, and
// loading every line of every order to render a picker is how a list of a
// thousand orders becomes a query that times out. `openOnly` filters to orders
// that still have something outstanding, which is what a fulfilment picker wants.
func (s *Service) List(ctx context.Context, direction Direction, openOnly bool) ([]Order, error) {
	if !direction.Valid() {
		return nil, fmt.Errorf("orders: direction must be %s or %s, not %q", Sales, Purchase, direction)
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return nil, errors.New("orders: no company in context")
	}
	var out []Order
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT oh.id, oh.direction::text, oh.order_no, oh.order_date,
			        oh.party_ledger_id, COALESCE(l.name, ''), oh.narration,
			        oh.is_closed, oh.closed_reason, oh.created_by
			   FROM order_head oh
			   LEFT JOIN ledger l
			     ON l.company_id = oh.company_id AND l.id = oh.party_ledger_id
			  WHERE oh.company_id = $1 AND oh.direction = $2::order_direction
			    AND (NOT $3::boolean OR (
			         oh.is_closed = false AND EXISTS (
			           SELECT 1 FROM order_line ol
			            WHERE ol.company_id = oh.company_id AND ol.order_id = oh.id
			              AND ol.qty > COALESCE((
							SELECT SUM(f.qty) FROM order_fulfilment f
							  JOIN voucher_head vh
							    ON vh.company_id = f.company_id
							   AND vh.current_version_id = f.voucher_version_id
							 WHERE f.company_id = ol.company_id
							   AND f.order_line_id = ol.id
							   AND vh.is_deleted = false), 0)
			         )))
			  ORDER BY oh.order_date DESC, lower(btrim(oh.order_no)) DESC, oh.id DESC`,
			companyID, string(direction), openOnly)
		if err != nil {
			return fmt.Errorf("orders: list orders: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			o, err := scanOrderHead(rows)
			if err != nil {
				return err
			}
			out = append(out, o)
		}
		return rows.Err()
	})
	return out, err
}

// Close records the HUMAN DECISION to abandon an order, with its reason.
//
// ⛔ THIS IS NOT "MARK FULFILLED". Fulfilment is derived and can never be
// asserted; this says the commitment will not be met and should stop appearing on
// the order book. The reason is REQUIRED (0042 CHECKs it) because a manual
// closure with no stated reason is exactly the row an auditor asks about, and
// because the alternative — a silent flag — makes an abandoned order
// indistinguishable from a mistake.
//
// It is reversible: passing closed = false clears the flag and the reason
// together, which is the only reading under which "I closed the wrong order"
// is correctable.
func (s *Service) Close(ctx context.Context, orderID uuid.UUID, closed bool, reason, by string) error {
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return errors.New("orders: no company in context")
	}
	reason = strings.TrimSpace(reason)
	if closed && reason == "" {
		return errors.New(
			"orders: closing an order needs a reason — an abandoned commitment with no stated cause " +
				"is the row an auditor asks about, and without one it cannot be told apart from a mistake")
	}
	if !closed {
		// Cleared TOGETHER, because 0042's order_head_open_has_no_reason CHECK
		// refuses a reason on an open order: a reason attached to something that has
		// not happened is a claim about nothing.
		reason = ""
	}
	return s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE order_head SET is_closed = $3, closed_reason = $4
			  WHERE company_id = $1 AND id = $2`,
			companyID, orderID, closed, reason)
		if err != nil {
			return fmt.Errorf("orders: close order: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// ReviseLineQty changes one line's committed quantity.
//
// ⛔ IT CAN FAIL FOR A REASON NO OTHER WRITE HERE CAN: 0042's
// order_line_still_covers_fulfilments trigger refuses a revision DOWN below what
// is already delivered. Reducing a 20-unit line to 5 when 12 have shipped would
// strand 7 units of delivery against a commitment that no longer covers them —
// the over-fulfilment violation reached from the order side, and invisible to a
// check that only watched fulfilments. The trigger is deferred, so the refusal
// arrives at COMMIT and is translated here into a sentence.
func (s *Service) ReviseLineQty(ctx context.Context, lineID uuid.UUID, qty decimal.Decimal) error {
	if !qty.IsPositive() {
		return errors.New(
			"orders: a revised quantity must be more than zero — to withdraw a commitment entirely, " +
				"close the order with a reason so the order book records why")
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return errors.New("orders: no company in context")
	}
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE order_line SET qty = $3 WHERE company_id = $1 AND id = $2`,
			companyID, lineID, qty)
		if err != nil {
			return fmt.Errorf("orders: revise order line: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil && isRaise(err, "has now been fulfilled against it") {
		return fmt.Errorf("%w: %v", ErrStillFulfilled, err)
	}
	return err
}

// ---------------------------------------------------------------------------
// Reads shared with Get/List
// ---------------------------------------------------------------------------

func readOrderHead(ctx context.Context, tx pgx.Tx, companyID, orderID uuid.UUID) (Order, error) {
	row := tx.QueryRow(ctx,
		`SELECT oh.id, oh.direction::text, oh.order_no, oh.order_date,
		        oh.party_ledger_id, COALESCE(l.name, ''), oh.narration,
		        oh.is_closed, oh.closed_reason, oh.created_by
		   FROM order_head oh
		   LEFT JOIN ledger l
		     ON l.company_id = oh.company_id AND l.id = oh.party_ledger_id
		  WHERE oh.company_id = $1 AND oh.id = $2`,
		companyID, orderID)
	o, err := scanOrderHead(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, ErrNotFound
	}
	return o, err
}

// scanner is what both pgx.Row and pgx.Rows satisfy, so the head projection is
// written ONCE. Two copies of a ten-column scan is how a list and a detail view
// start disagreeing about which column is the narration.
type scanner interface {
	Scan(dest ...any) error
}

func scanOrderHead(sc scanner) (Order, error) {
	var (
		o         Order
		dir       string
		orderDate time.Time
	)
	if err := sc.Scan(&o.ID, &dir, &o.OrderNo, &orderDate, &o.PartyLedgerID,
		&o.Party, &o.Narration, &o.IsClosed, &o.ClosedReason, &o.CreatedBy); err != nil {
		return Order{}, err
	}
	o.Direction = Direction(dir)
	o.OrderDate = orderDate.Format("2006-01-02")
	return o, nil
}

// readOrderLines reads a line WITH its derived fulfilment.
//
// ⛔ THE voucher_head JOIN IS LOAD-BEARING AND IS THE SAME ONE THE ORDER BOOK
// USES. Fulfilment rows are VERSIONED: an ALTER writes a new voucher version and
// re-states its links, leaving the superseded version's rows in the table
// forever. Without the join a delivery corrected from 5 to 8 counts as 13, and
// the order reads 7 outstanding where 12 is correct. reporting/orderbook.go
// carries the same clause and the same comment; if you change one, change both.
func readOrderLines(ctx context.Context, tx pgx.Tx, companyID, orderID uuid.UUID) ([]Line, error) {
	rows, err := tx.Query(ctx,
		`SELECT ol.id, ol.line_no, ol.stock_item_id, COALESCE(si.name, ''),
		        ol.qty, ol.rate, ol.due_date,
		        COALESCE((
		            SELECT SUM(f.qty) FROM order_fulfilment f
		              JOIN voucher_head vh
		                ON vh.company_id = f.company_id
		               AND vh.current_version_id = f.voucher_version_id
		             WHERE f.company_id = ol.company_id
		               AND f.order_line_id = ol.id
		               AND vh.is_deleted = false), 0)
		   FROM order_line ol
		   LEFT JOIN stock_item si
		     ON si.company_id = ol.company_id AND si.id = ol.stock_item_id
		  WHERE ol.company_id = $1 AND ol.order_id = $2
		  ORDER BY ol.line_no`,
		companyID, orderID)
	if err != nil {
		return nil, fmt.Errorf("orders: read order lines: %w", err)
	}
	defer rows.Close()
	var out []Line
	for rows.Next() {
		var (
			l   Line
			due *time.Time
		)
		if err := rows.Scan(&l.ID, &l.LineNo, &l.StockItemID, &l.StockItem,
			&l.Qty, &l.Rate, &due, &l.Fulfilled); err != nil {
			return nil, fmt.Errorf("orders: scan order line: %w", err)
		}
		if due != nil {
			l.DueDate = due.Format("2006-01-02")
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Error translation
// ---------------------------------------------------------------------------

// wrapOrderWrite turns a Postgres constraint violation into the sentence an
// operator can act on.
//
// Keyed on the CONSTRAINT NAME rather than on the message text, because the
// message is Postgres's and could change with a version while the name is ours
// and is pinned by the migration.
func wrapOrderWrite(err error, in Input) error {
	var pg *pgconn.PgError
	if !errors.As(err, &pg) {
		return fmt.Errorf("orders: insert order: %w", err)
	}
	switch {
	case pg.ConstraintName == "order_head_no_unique":
		return fmt.Errorf(
			"%w: this company already has a %s order numbered %q — order numbers are unique per "+
				"direction and are matched case-insensitively, so \"SO-1\" and \"so-1\" are the same "+
				"order (a sales and a purchase order MAY share a number)",
			ErrConflict, strings.ToLower(string(in.Direction)), strings.TrimSpace(in.OrderNo))
	case pg.ConstraintName == "order_head_party_fk":
		return fmt.Errorf(
			"orders: the party ledger named does not exist in this company, so the order would commit "+
				"with a %s nobody can invoice: %w", in.counterparty(), err)
	case pg.ConstraintName == "order_line_item_fk":
		return fmt.Errorf(
			"orders: an order line names a stock item that does not exist in this company: %w", err)
	}
	return fmt.Errorf("orders: insert order: %w", err)
}

// isRaise reports whether err is a plpgsql RAISE whose message contains needle —
// how a deferred constraint trigger's refusal is recognised. Matched on the
// message because a RAISE has no constraint name to key on.
func isRaise(err error, needle string) bool {
	var pg *pgconn.PgError
	if !errors.As(err, &pg) {
		return false
	}
	return strings.Contains(pg.Message, needle)
}

func nullIfBlank(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}
