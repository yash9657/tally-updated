package orders

// order_party_group_db_test.go — WHO AN ORDER MAY BE COMMITTED TO, against the
// live database.
//
// ⛔⛔ WHY THIS FILE NEEDS A DATABASE AT ALL, WHEN bom_test.go PROUDLY DOES NOT.
//
// The rule under test is "a sales order's party must be a Sundry Debtor and a
// purchase order's a Sundry Creditor", and that is not a fact about an Input — it
// is a fact about a ROW that lives two joins away (ledger -> account_group ->
// its ltree ancestry). A pure test could only assert a re-implementation of the
// query, which is the shape of coverage that proves nothing.
//
// The second thing only a database can show is the SUB-GROUP case. A company that
// files its customers under "Retail Customers", itself under Sundry Debtors, has
// perfectly ordinary customers — and the tempting implementation (compare
// account_group.name to "Sundry Debtors") refuses every one of them while looking
// correct on the seeded chart. That is not hypothetical in this codebase:
// LedgerListItem.is_bank is server-derived from this same ltree ancestry BECAUSE
// a client comparing group names silently dropped real bank accounts. So the
// sub-group leg below is not a nicety, it is the leg that tells the right
// implementation from the plausible one.
//
// ⚠️ EVERY REFUSAL HERE HAS A CONTROL. A test that only asserts refusals is
// consistent with a Create that refuses everything, and this package has already
// paid for that lesson twice (bom_test.go's validators both open with a control).
//
// ⛔ AND THE INVARIANT THIS FILE MUST NOT BREAK: AN ORDER MOVES NO STOCK. Nothing
// below posts a voucher, and the assertions read order_head / order_line only.
// TestARefusedOrderWritesNothingAtAll checks the other half — that a refusal
// leaves no half-written head behind, because Create's checks run INSIDE the same
// transaction as the insert.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/migrations"
)

// partyEnv is one isolated company with one ledger in each interesting place on
// the chart, and one stock item to commit a quantity of.
type partyEnv struct {
	pool    *db.Pool
	ctx     context.Context
	company uuid.UUID
	svc     *Service

	item uuid.UUID

	// The four parties the cases below choose between.
	debtor      uuid.UUID // directly under Sundry Debtors
	creditor    uuid.UUID // directly under Sundry Creditors
	subDebtor   uuid.UUID // under "Retail Customers", itself under Sundry Debtors
	bank        uuid.UUID // a BANK ledger — the headline defect
	expenseHead uuid.UUID // Rent Paid, which is not a counterparty of anything
}

// setupParty mints one company and everything the cases need. It creates the
// company and registers its teardown in the same breath — internal/testhygiene
// fails the BUILD for a company-creating test function that does not reach for
// testdb.
func setupParty(t *testing.T) *partyEnv {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	ctx := context.Background()

	adminConn, err := pgx.Connect(ctx, testdb.AdminDSNValue())
	if err != nil {
		t.Skipf("admin postgres unavailable, skipping integration test: %v", err)
	}
	if err := migrations.Apply(ctx, adminConn); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("apply migrations: %v", err)
	}
	_ = adminConn.Close(ctx)

	pool, err := db.New(ctx, testdb.AppDSN)
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}
	t.Cleanup(pool.Close)

	companyID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new company id: %v", err)
	}
	cctx := db.WithCompany(ctx, companyID)
	ids := identity.NewService(pool)
	if _, err := ids.CreateCompany(cctx, identity.Company{
		ID:          companyID,
		Name:        "Order Party Co " + companyID.String()[:8],
		FYStartDate: "2026-04-01",
	}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	testdb.Cleanup(t, companyID)

	e := &partyEnv{pool: pool, ctx: cctx, company: companyID, svc: NewService(pool)}

	mk := func(name, group string) uuid.UUID {
		id, err := ids.CreateLedger(cctx, name, group)
		if err != nil {
			t.Fatalf("create ledger %q under %q: %v", name, group, err)
		}
		return id
	}
	e.debtor = mk("Acme Retail", "Sundry Debtors")
	e.creditor = mk("Timber Supplies", "Sundry Creditors")
	e.bank = mk("HDFC Current", "Bank Accounts")
	e.expenseHead = mk("Rent Paid", "Indirect Expenses")

	// A USER SUB-GROUP under Sundry Debtors, planted directly rather than through
	// masters: what is under test is the ancestry walk, and routing the fixture
	// through another service's group-creation rules would let a change there move
	// a result in here without this file saying so.
	if err := pool.RunInTx(cctx, func(ctx context.Context, tx pgx.Tx) error {
		subID, err := uuid.NewV7()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO account_group (id, company_id, name, parent_id, path, nature, is_pl,
			                            behaves_as_subledger)
			 SELECT $1::uuid, $2::uuid, 'Retail Customers', p.id,
			        p.path || text2ltree('u' || replace($3::text, '-', '')),
			        p.nature, p.is_pl, true
			   FROM account_group p
			  WHERE p.company_id = $2::uuid AND p.name = 'Sundry Debtors'`,
			subID, companyID, subID.String()); err != nil {
			return err
		}
		ledID, err := uuid.NewV7()
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx,
			`INSERT INTO ledger (id, company_id, name, group_id) VALUES ($1, $2, $3, $4)`,
			ledID, companyID, "Basavanagudi Hardware", subID)
		e.subDebtor = ledID
		return err
	}); err != nil {
		t.Fatalf("plant the 'Retail Customers' sub-group and its ledger: %v", err)
	}

	if err := pool.RunInTx(cctx, func(ctx context.Context, tx pgx.Tx) error {
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx,
			`INSERT INTO stock_item (id, company_id, name) VALUES ($1, $2, 'MS Angle')`,
			id, companyID)
		e.item = id
		return err
	}); err != nil {
		t.Fatalf("create the stock item: %v", err)
	}
	return e
}

// order builds one single-line order for the given direction and party.
func (e *partyEnv) order(direction Direction, party uuid.UUID, no string) Input {
	return Input{
		Direction:     direction,
		OrderNo:       no,
		OrderDate:     "2026-06-01",
		PartyLedgerID: party,
		CreatedBy:     "tester",
		Lines:         []LineInput{{StockItemID: e.item, Qty: d("20"), Rate: d("500")}},
	}
}

// ⛔⛔ THE HEADLINE: A SALES ORDER MAY NOT BE COMMITTED TO A BANK LEDGER, AND A
// PURCHASE ORDER MAY NOT BE COMMITTED TO A CUSTOMER.
//
// Both order books GROUP BY COUNTERPARTY and exist to answer "who is owed what",
// so a commitment filed against HDFC Current names — in the party column of a
// report an operator acts on — an account that can never be invoiced and can
// never deliver. Before this, the picker offered the entire chart of accounts and
// nothing on the client, in Validate, or in the database said no.
func TestAnOrderMayOnlyBeCommittedToACounterpartyOfItsDirection(t *testing.T) {
	e := setupParty(t)

	// ⛔ THE CONTROLS FIRST, one per direction. Without them every refusal below is
	// equally consistent with a Create that has stopped working.
	if _, err := e.svc.Create(e.ctx, e.order(Sales, e.debtor, "SO-CONTROL")); err != nil {
		t.Fatalf("CONTROL: a sales order to a Sundry Debtor must be ACCEPTED: %v", err)
	}
	if _, err := e.svc.Create(e.ctx, e.order(Purchase, e.creditor, "PO-CONTROL")); err != nil {
		t.Fatalf("CONTROL: a purchase order to a Sundry Creditor must be ACCEPTED: %v", err)
	}

	cases := []struct {
		name      string
		direction Direction
		party     func() uuid.UUID
		// wantSaid is a phrase the refusal must contain, so a sentence that names
		// the wrong group (or names nothing) fails rather than merely being red.
		wantSaid string
	}{
		{
			name:      "a SALES order committed to a BANK ledger — the reported defect",
			direction: Sales, party: func() uuid.UUID { return e.bank },
			wantSaid: "Sundry Debtors",
		},
		{
			name:      "a PURCHASE order committed to a CUSTOMER — the mirror of it",
			direction: Purchase, party: func() uuid.UUID { return e.debtor },
			wantSaid: "Sundry Creditors",
		},
		{
			name:      "a SALES order committed to a SUPPLIER",
			direction: Sales, party: func() uuid.UUID { return e.creditor },
			wantSaid: "Sundry Debtors",
		},
		{
			name:      "a PURCHASE order committed to a BANK ledger",
			direction: Purchase, party: func() uuid.UUID { return e.bank },
			wantSaid: "Sundry Creditors",
		},
		{
			name:      "an order committed to an EXPENSE head, which is nobody's counterparty",
			direction: Sales, party: func() uuid.UUID { return e.expenseHead },
			wantSaid: "Sundry Debtors",
		},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := e.svc.Create(e.ctx, e.order(c.direction, c.party(), "X-"+strings.Repeat("x", i+1)))
			if err == nil {
				t.Fatalf("ACCEPTED %s. The order book groups by counterparty, so this commitment now "+
					"sits under a party that can never be invoiced and can never deliver", c.name)
			}
			if !errors.Is(err, ErrWrongPartyGroup) {
				t.Fatalf("refused %s, but NOT with ErrWrongPartyGroup — so the HTTP layer cannot tell "+
					"it apart from a party that does not exist, and a test cannot tell whether the "+
					"refusal was for the reason claimed: %v", c.name, err)
			}
			if !strings.Contains(err.Error(), c.wantSaid) {
				t.Errorf("the refusal for %s does not name the group the party must be under (%q): %v",
					c.name, c.wantSaid, err)
			}
		})
	}
}

// ⛔ A CUSTOMER FILED UNDER A USER SUB-GROUP OF SUNDRY DEBTORS IS STILL A
// CUSTOMER. This is the leg that tells an ANCESTRY check from a name comparison:
// "Basavanagudi Hardware" sits in "Retail Customers", whose parent is Sundry
// Debtors, and its account_group.name is not "Sundry Debtors" at all. An
// implementation that compared the immediate group name would refuse it — and
// would look completely correct against every other test in this file.
func TestAPartyInASubGroupOfSundryDebtorsIsAcceptedOnASalesOrder(t *testing.T) {
	e := setupParty(t)

	id, err := e.svc.Create(e.ctx, e.order(Sales, e.subDebtor, "SO-SUB"))
	if err != nil {
		t.Fatalf("a customer kept under a sub-group of Sundry Debtors was REFUSED, so the check is "+
			"comparing the IMMEDIATE group name instead of walking ltree ancestry. Every company "+
			"that organises its debtors into sub-groups can now record no sales order at all: %v", err)
	}

	// ⛔ READ THE ORDER BACK AND ASSERT THE PARTY AND THE QUANTITY, never a status
	// code and never a total: the point of the order is WHO it commits us to and
	// HOW MUCH, and both must survive the write.
	got, err := e.svc.Get(e.ctx, id)
	if err != nil {
		t.Fatalf("read the order back: %v", err)
	}
	if got.PartyLedgerID != e.subDebtor {
		t.Errorf("the order came back committed to %s, want the sub-group customer %s",
			got.PartyLedgerID, e.subDebtor)
	}
	if got.Party != "Basavanagudi Hardware" {
		t.Errorf("the order's party reads %q, want %q", got.Party, "Basavanagudi Hardware")
	}
	if len(got.Lines) != 1 || got.Lines[0].Qty.StringFixed(4) != "20.0000" {
		t.Fatalf("the committed QUANTITY did not survive: %+v", got.Lines)
	}
	if got.Lines[0].Fulfilled.StringFixed(4) != "0.0000" ||
		got.Lines[0].Outstanding().StringFixed(4) != "20.0000" {
		t.Errorf("a fresh order must be outstanding IN FULL: fulfilled %s, outstanding %s",
			got.Lines[0].Fulfilled, got.Lines[0].Outstanding())
	}
}

// ⛔ A REFUSED ORDER LEAVES NOTHING BEHIND, and this is why the check lives
// INSIDE Create's transaction rather than in Validate.
//
// A pre-check outside the transaction would be a second copy of a fact that can
// change between the read and the write; a check after the head insert would need
// the rollback to be right. This asserts the observable consequence: after a
// refusal there is no order_head row with that number, so the operator can key
// the same number again with the party they meant.
func TestARefusedOrderWritesNothingAtAll(t *testing.T) {
	e := setupParty(t)

	if _, err := e.svc.Create(e.ctx, e.order(Sales, e.bank, "SO-ROLLBACK")); err == nil {
		t.Fatal("a sales order to a bank ledger was ACCEPTED")
	}

	var heads, lines int
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM order_head WHERE company_id = $1 AND order_no = 'SO-ROLLBACK'`,
			e.company).Scan(&heads); err != nil {
			return err
		}
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM order_line ol JOIN order_head oh ON oh.id = ol.order_id
			  WHERE ol.company_id = $1 AND oh.order_no = 'SO-ROLLBACK'`,
			e.company).Scan(&lines)
	}); err != nil {
		t.Fatalf("count what the refusal left behind: %v", err)
	}
	if heads != 0 || lines != 0 {
		t.Errorf("a refused order left %d head row(s) and %d line row(s) behind — the number is now "+
			"taken and re-keying it with the right party would come back a 409", heads, lines)
	}

	// AND THE ORDER NUMBER IS STILL FREE, which is the thing the operator
	// actually experiences. A count of zero is consistent with a row under a
	// different number; this is not.
	if _, err := e.svc.Create(e.ctx, e.order(Sales, e.debtor, "SO-ROLLBACK")); err != nil {
		t.Errorf("after a refusal the same order number must still be usable: %v", err)
	}
}

// ⛔⛔ THE DUE-DATE RULE'S FAIL-CLOSED HALF, AND THE ONLY TEST THAT CAN SEE IT.
//
// Validate refuses a delivery date that precedes the order date, but it only
// compares dates that parse as CANONICAL yyyy-mm-dd — because Postgres, not Go,
// is the authority on what a date is, and it accepts spellings time.Parse
// rejects. "2026-6-1" is stored happily. So an order dated "2026-6-1" with a line
// due "2026-1-1" walks straight past the Go check.
//
// bom_test.go's TestADueDateIsRefusedONLYWhenItPrecedesTheOrderDate asserts the
// Go half DECLINES to decide on that spelling. This asserts the other half — that
// declining is safe, because the comparison is made again inside the transaction
// by the layer that owns the grammar. Together they are the property: the looser
// layer is the one that cannot decide, never the one that lets it through.
func TestALooselySpelledDueDateBeforeTheOrderDateIsStillRefused(t *testing.T) {
	e := setupParty(t)

	// THE CONTROL: the same loose spelling with a due date AFTER the order date is
	// perfectly legal and must be accepted, or the refusal below proves only that
	// Postgres dislikes the spelling.
	ok := e.order(Sales, e.debtor, "SO-LOOSE-OK")
	ok.OrderDate = "2026-6-1"
	ok.Lines[0].DueDate = "2026-7-15"
	if _, err := e.svc.Create(e.ctx, ok); err != nil {
		t.Fatalf("CONTROL: a loosely spelled date pair in the right ORDER must be accepted — "+
			"Postgres parses both: %v", err)
	}

	bad := e.order(Sales, e.debtor, "SO-LOOSE-BAD")
	bad.OrderDate = "2026-6-1"
	bad.Lines[0].DueDate = "2026-1-1"
	// The Go validator must be SILENT on this pair — that is the design, and if it
	// ever starts refusing it, it has grown a second copy of Postgres's date
	// grammar and this comment is the warning.
	if err := bad.Validate(); err != nil {
		t.Logf("note: Validate now refuses the loose spelling too (%v). That is not a failure, but "+
			"it means dueBeforeOrder has learned a date grammar it does not own — read its comment", err)
	}
	if _, err := e.svc.Create(e.ctx, bad); err == nil {
		t.Fatal("an order dated 2026-6-1 with a line due 2026-1-1 was ACCEPTED. It is overdue on the " +
			"order book the instant it exists, and the Go-side check cannot see it because " +
			"time.Parse rejects the spelling Postgres accepts")
	} else if !strings.Contains(err.Error(), "BEFORE the order date") {
		t.Errorf("the refusal does not say what is wrong with the dates: %v", err)
	}
}
