package reporting_test

// orderbook_db_test.go — THE ORDER BOOK IN THE STATE AN OPERATOR OPENS IT.
//
// ⛔⛔ WHY A DATABASE TEST WHEN orderbook_overdue_test.go ALREADY PINS THE
// DECISION. Because the decision has to be REACHED. The pure tests assert that a
// row with a resolved reference answers "late"; this one asserts that
// OrderBookFor puts the reference on the rows at all — the wiring step, which is
// exactly where a fix like this dies silently. Delete the `r.OverdueAsOn =
// out.OverdueAsOn` line in OrderBookFor and every pure test above stays green
// while the product goes back to reporting zero late deliveries.
//
// ⛔ AND IT DOUBLES AS THE ORDER BOOK'S CONTROL FOR THE PARTY-GROUP RULE: the
// order below is created through orders.Service against a Sundry Debtor, so if
// that rule ever starts refusing an ordinary customer, this test fails in setup
// rather than the failure surfacing first in a browser.
//
// ⛔ NOTHING HERE MOVES STOCK. An order is a committed quantity; no voucher is
// posted, and the assertions read quantities and the PARTY, never a total on its
// own.

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/orders"
	"github.com/khata/cored/internal/reporting"
)

// TestTheDefaultOrderBookReportsItsLateLines is the end-to-end form of D44.
func TestTheDefaultOrderBookReportsItsLateLines(t *testing.T) {
	pool, ctx, companyID, ids := setup(t)

	debtor, err := ids.CreateLedger(ctx, "Acme Retail", "Sundry Debtors")
	if err != nil {
		t.Fatalf("create the customer: %v", err)
	}
	var item uuid.UUID
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		item = id
		_, err = tx.Exec(ctx,
			`INSERT INTO stock_item (id, company_id, name) VALUES ($1, $2, 'MS Angle')`,
			id, companyID)
		return err
	}); err != nil {
		t.Fatalf("create the stock item: %v", err)
	}

	// One order, three lines, chosen so the report has all three shapes to tell
	// apart: a promise that has passed, a promise that has not, and no promise at
	// all. 2020 and 2099 are used rather than dates near today so this test does
	// not start failing on a particular morning.
	if _, err := orders.NewService(pool).Create(ctx, orders.Input{
		Direction:     orders.Sales,
		OrderNo:       "SO-LATE-1",
		OrderDate:     "2020-01-01",
		PartyLedgerID: debtor,
		CreatedBy:     "tester",
		Lines: []orders.LineInput{
			{StockItemID: item, Qty: dec("50"), Rate: dec("300"), DueDate: "2020-02-15"},
			{StockItemID: item, Qty: dec("10"), Rate: dec("1050"), DueDate: "2099-12-31"},
			{StockItemID: item, Qty: dec("7"), Rate: dec("25")},
		},
	}); err != nil {
		t.Fatalf("record the sales order: %v", err)
	}

	// ⛔ NO as_on — THE DEFAULT. This is the exact call the screen makes before an
	// operator touches F2, and the state in which the book reported nothing late.
	book, err := reporting.NewService(pool).OrderBookFor(ctx, companyID, reporting.SalesOrders, "")
	if err != nil {
		t.Fatalf("order book: %v", err)
	}

	if book.AsOn != "" {
		t.Errorf("as_on must be ECHOED as given (\"\"), not filled in — the client renders the "+
			"unbounded case as its own sentence and a substituted date would relabel the report; "+
			"got %q", book.AsOn)
	}
	if book.OverdueAsOn == "" {
		t.Fatal("the book carries no resolved overdue reference, so nothing can ever be late on it")
	}
	if len(book.Rows) != 3 {
		t.Fatalf("want the 3 outstanding lines, got %d: %+v", len(book.Rows), book.Rows)
	}

	// ⛔ ASSERT THE QUANTITIES AND THE PARTY, never just the flag. A report that
	// flagged the right row while losing the commitment behind it would be a worse
	// defect than the one being fixed.
	var late, notYet, undated int
	for _, r := range book.Rows {
		if r.Party != "Acme Retail" {
			t.Errorf("line %d is filed against %q, want the customer it was committed to",
				r.LineNo, r.Party)
		}
		if r.OverdueAsOn != book.OverdueAsOn {
			t.Errorf("line %d carries overdue reference %q, want the book's %q — a row without one "+
				"answers \"not late\" to internal/server, which passes book.AsOn (empty here)",
				r.LineNo, r.OverdueAsOn, book.OverdueAsOn)
		}
		// The call internal/server makes, verbatim.
		switch overdue := r.Overdue(book.AsOn); {
		case r.DueDate == "":
			undated++
			if overdue {
				t.Errorf("line %d agreed NO delivery date and is flagged overdue", r.LineNo)
			}
		case overdue:
			late++
			if r.DueDate != "2020-02-15" {
				t.Errorf("line %d (due %s) is flagged overdue and should not be", r.LineNo, r.DueDate)
			}
			if got := r.Outstanding().StringFixed(4); got != "50.0000" {
				t.Errorf("the late line's outstanding quantity reads %s, want 50.0000", got)
			}
		default:
			notYet++
		}
	}
	if late != 1 || notYet != 1 || undated != 1 {
		t.Fatalf("want exactly one late, one not-yet-due and one undated line; got %d/%d/%d — "+
			"if late is 0 the default view is reporting no late deliveries again, which is the "+
			"whole defect", late, notYet, undated)
	}

	// AND THE ARTEFACT SAYS SO, because a red row an operator has to hunt for is
	// not the same as a report that states its own count.
	if !strings.Contains(strings.Join(book.Notes, "\n"),
		"1 outstanding line(s) are past their agreed due date") {
		t.Errorf("the notes do not report the one late line:\n%v", book.Notes)
	}
	// And they name the day it was measured against, so the count is reproducible
	// from the artefact rather than from the reader's own clock.
	if !strings.Contains(strings.Join(book.Notes, "\n"), book.OverdueAsOn) {
		t.Errorf("the notes do not name the date lateness was measured against (%s):\n%v",
			book.OverdueAsOn, book.Notes)
	}
}
