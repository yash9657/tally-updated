package masters_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/migrations"
)

const (
	adminDSN = "postgres://postgres:postgres@localhost:5433/tally?sslmode=disable"
	appDSN   = "postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable"
)

func setup(t *testing.T) (*db.Pool, context.Context, uuid.UUID) {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	ctx := context.Background()

	adminConn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	if err := migrations.Apply(ctx, adminConn); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("apply migrations: %v", err)
	}
	_ = adminConn.Close(ctx)

	pool, err := db.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}
	t.Cleanup(pool.Close)

	companyID, _ := uuid.NewV7()
	cctx := db.WithCompany(ctx, companyID)
	ids := identity.NewService(pool)
	if _, err := ids.CreateCompany(cctx, identity.Company{
		ID: companyID, Name: "Masters Co " + companyID.String()[:8], FYStartDate: "2025-04-01",
	}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	// Teardown, registered the moment the company exists. This package writes
	// master_audit rows, which carry a BEFORE UPDATE OR DELETE immutability
	// trigger — an ordinary DELETE cannot remove them at all, which is why the
	// teardown runs its deletes under SET LOCAL session_replication_role=replica
	// inside one transaction rather than disabling the trigger globally.
	testdb.Cleanup(t, companyID)
	return pool, cctx, companyID
}

func TestCreateLedgerWithGSTAndAudit(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	id, err := m.CreateLedger(ctx, masters.Ledger{
		Name:             "GST Customer",
		GroupName:        "Sundry Debtors",
		GSTIN:            "27ABCDE1234F1Z5",
		StateCode:        "27",
		RegistrationType: masters.GSTRegular,
	}, "tester")
	if err != nil {
		t.Fatalf("create ledger: %v", err)
	}

	gst, err := m.LedgerGSTInfo(ctx, id)
	if err != nil {
		t.Fatalf("gst info: %v", err)
	}
	if gst.GSTIN != "27ABCDE1234F1Z5" || gst.StateCode != "27" || gst.RegistrationType != masters.GSTRegular {
		t.Fatalf("unexpected gst info: %+v", gst)
	}

	// Audit row must exist for the create.
	var auditCount int
	err = pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM master_audit WHERE entity_type='ledger' AND entity_id=$1 AND op='CREATE'`,
			id).Scan(&auditCount)
	})
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("expected 1 audit row, got %d", auditCount)
	}
}

// invoiceAddress is the address of a real registered customer, keyed the way an
// operator keys one: several lines, because that is what is printed on the
// document. The newlines are the point — printingdb's splitAddress turns this
// block into the recipient's address lines, so a store that flattened or
// truncated it would print a different address from the one on file.
const invoiceAddress = "Plot 14, 2nd Floor\nIndustrial Suburb, Yeshwanthpur\nOpp. Goods Shed"

// TestAPartyWithNoAddressOnFileIsATaxInvoiceThatCannotBeIssued is the whole
// reason migration 0039 exists, asserted from the master's side.
//
// Rule 46(d) of the CGST Rules 2017 makes the recipient's NAME, ADDRESS and
// GSTIN mandatory particulars of a tax invoice, and internal/printing refuses to
// render a document whose recipient address is blank. Until this column existed
// there was NOWHERE to keep a party's address, so that refusal fired on every
// sales voucher in every company and this product could not print a single valid
// tax invoice. If this test fails, the address an operator keyed against a
// customer is not the address the books hold, and the invoice raised on that
// customer either cannot be issued or states an address nobody entered.
func TestAPartyWithNoAddressOnFileIsATaxInvoiceThatCannotBeIssued(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	id, err := m.CreateLedger(ctx, masters.Ledger{
		Name:             "Addressed Customer",
		GroupName:        "Sundry Debtors",
		GSTIN:            "29AACCA5678L1Z3",
		StateCode:        "29",
		RegistrationType: masters.GSTRegular,
		Address:          invoiceAddress,
		City:             "Bengaluru",
		PIN:              "560022",
	}, "tester")
	if err != nil {
		t.Fatalf("create a customer with the address its invoices must carry: %v", err)
	}

	got, err := m.LedgerAddressInfo(ctx, id)
	if err != nil {
		t.Fatalf("read back the address of the customer just created: %v", err)
	}
	if got.Address != invoiceAddress {
		t.Errorf("the address on file for this customer is %q but %q was keyed against them: "+
			"a tax invoice raised on this party would be addressed to the wrong place, and "+
			"Rule 46(d) makes the recipient's address a mandatory particular",
			got.Address, invoiceAddress)
	}
	if got.City != "Bengaluru" || got.PIN != "560022" {
		t.Errorf("the city and PIN on file are %q / %q but %q / %q were keyed: "+
			"the invoice would state a different destination from the one the operator entered",
			got.City, got.PIN, "Bengaluru", "560022")
	}
	if !got.OnFile() {
		t.Errorf("this customer reports NO address on file although one was keyed against them, "+
			"so every tax invoice raised on them would be refused as missing a "+
			"mandatory Rule 46(d) particular (address %q)", got.Address)
	}

	// The GST identity must survive alongside the address. Rule 46(d) names all
	// three particulars in one breath, and a change that carried the address at
	// the cost of the GSTIN would have swapped one unprintable invoice for
	// another.
	gst, err := m.LedgerGSTInfo(ctx, id)
	if err != nil {
		t.Fatalf("read back the GST identity of the customer just created: %v", err)
	}
	if gst.GSTIN != "29AACCA5678L1Z3" || gst.StateCode != "29" {
		t.Errorf("this customer's GSTIN/state came back as %q/%q instead of %q/%q: "+
			"Rule 46(d) requires the recipient's GSTIN as well as their address, so the "+
			"invoice is still not issuable", gst.GSTIN, gst.StateCode, "29AACCA5678L1Z3", "29")
	}
}

// TestAWalkInCashCustomerWithNoAddressIsStillAValidPartyToSellTo guards the
// OTHER direction, which is the one a well-meant NOT NULL would break.
//
// A cash sale to an unregistered walk-in customer has no address on file, and
// that is not missing data: Rule 46(e) requires an unregistered recipient's
// address (with the State and its code) only where the taxable value of the
// supply is FIFTY THOUSAND RUPEES OR MORE. The threshold belongs to the
// individual document, which the master cannot see, so the master must accept a
// party with no address and let the print path refuse the specific invoices that
// need one. If this test fails, a shop cannot record a counter sale at all.
func TestAWalkInCashCustomerWithNoAddressIsStillAValidPartyToSellTo(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	id, err := m.CreateLedger(ctx, masters.Ledger{
		Name:             "Counter Sales (Walk-in)",
		GroupName:        "Sundry Debtors",
		RegistrationType: masters.GSTUnregistered,
	}, "tester")
	if err != nil {
		t.Fatalf("a walk-in cash customer with no address on file was refused, so a counter "+
			"sale below the Rule 46(e) fifty-thousand threshold cannot be recorded at all: %v", err)
	}

	got, err := m.LedgerAddressInfo(ctx, id)
	if err != nil {
		t.Fatalf("read back the (absent) address of the walk-in customer: %v", err)
	}
	if got.Address != "" || got.City != "" || got.PIN != "" {
		t.Errorf("a walk-in customer nobody gave an address for reports address %q, city %q, "+
			"PIN %q — an address that was never keyed would be printed on their invoice",
			got.Address, got.City, got.PIN)
	}
	if got.OnFile() {
		t.Errorf("a walk-in customer with no address keyed reports an address ON FILE, " +
			"which would let a fifty-thousand-rupee supply to an unregistered person print " +
			"without the Rule 46(e) particulars it needs")
	}

	// Absent must be NULL, not '': every other optional party attribute on
	// `ledger` (gstin, pan, state_code) is stored that way and masters writes
	// them through NULLIF. Two spellings of "not on file" in one row is how a
	// later reader starts treating a blank address as a recorded one.
	var addrIsNull, cityIsNull, pinIsNull bool
	err = pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT address IS NULL, city IS NULL, pincode IS NULL FROM ledger WHERE id = $1`,
			id).Scan(&addrIsNull, &cityIsNull, &pinIsNull)
	})
	if err != nil {
		t.Fatalf("inspect the stored address columns of the walk-in customer: %v", err)
	}
	if !addrIsNull || !cityIsNull || !pinIsNull {
		t.Errorf("the walk-in customer's unkeyed address stored as an empty string rather than "+
			"NULL (address null=%v, city null=%v, pincode null=%v): this row now spells "+
			"\"no address on file\" differently from how it spells \"no GSTIN on file\"",
			addrIsNull, cityIsNull, pinIsNull)
	}
}

func TestEnsureLedgerIsIdempotent(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	id1, err := m.EnsureLedger(ctx, "Repeat Ledger", "Indirect Expenses", "tester")
	if err != nil {
		t.Fatalf("ensure 1: %v", err)
	}
	id2, err := m.EnsureLedger(ctx, "Repeat Ledger", "Indirect Expenses", "tester")
	if err != nil {
		t.Fatalf("ensure 2: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("EnsureLedger not idempotent: %s != %s", id1, id2)
	}
}

func TestCreateGroupSubgroupInheritsNature(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	// Primary group with explicit nature.
	if _, err := m.CreateGroup(ctx, masters.Group{
		Name: "My Primary", Nature: masters.Liability, IsPL: false,
	}, "tester"); err != nil {
		t.Fatalf("create primary: %v", err)
	}
	// Sub-group under it inherits nature/is_pl.
	subID, err := m.CreateGroup(ctx, masters.Group{
		Name: "My Sub", ParentName: "My Primary",
	}, "tester")
	if err != nil {
		t.Fatalf("create sub: %v", err)
	}

	var nature string
	err = pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT nature::text FROM account_group WHERE id=$1`, subID).Scan(&nature)
	})
	if err != nil {
		t.Fatalf("read sub nature: %v", err)
	}
	if nature != "LIABILITY" {
		t.Fatalf("sub-group should inherit LIABILITY, got %s", nature)
	}
}

func TestCreateStockItemAndUnit(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	// "Nos" is seeded; create a custom unit too.
	if _, err := m.CreateUnit(ctx, masters.Unit{Symbol: "Kg", FormalName: "Kilogram", DecimalPlaces: 3}, "tester"); err != nil {
		t.Fatalf("create unit: %v", err)
	}
	id, err := m.CreateStockItem(ctx, masters.StockItem{
		Name: "Widget", UnitSymbol: "Kg", HSN: "8471",
		GSTRatePct: decimal.RequireFromString("18.00"),
	}, "tester")
	if err != nil {
		t.Fatalf("create stock item: %v", err)
	}
	got, err := m.StockItemID(ctx, "Widget")
	if err != nil {
		t.Fatalf("resolve stock item: %v", err)
	}
	if got != id {
		t.Fatalf("resolved id mismatch")
	}
}

func TestResolveUnknownReturnsNotFound(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)
	_, err := m.LedgerID(ctx, "Does Not Exist")
	if !errors.Is(err, masters.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMastersRequiresCompanyScope(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, appDSN)
	if err != nil {
		t.Skipf("pool unavailable: %v", err)
	}
	defer pool.Close()
	m := masters.NewService(pool)
	// No company in context -> must refuse before touching the DB.
	if _, err := m.CreateLedger(context.Background(), masters.Ledger{Name: "X", GroupName: "Y"}, "t"); !errors.Is(err, db.ErrNoCompany) {
		t.Fatalf("expected ErrNoCompany, got %v", err)
	}
}

// TestMastersHarnessRemovesItsCompany is the anti-rot guard on setup()'s
// teardown: it proves the harness still registers it. The failure it guards
// against is silent — a harness that stops cleaning up breaks no assertion, it
// just buries another company and its whole chart of accounts on every run,
// which is how the dev database reached 43,419 of them.
//
// The child tables are counted, not just `company`, because only
// notification_outbox references company(id) and its foreign-key action is NO
// ACTION: deleting the parent alone orphans account_group, ledger, voucher_type
// and acl_rule without raising an error. A company-only assertion would have
// passed against exactly that bug.
func TestMastersHarnessRemovesItsCompany(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	// Cleanups registered on the inner *testing.T have all run by the time t.Run
	// returns, so the harness has had its chance before the next line.
	var cid uuid.UUID
	t.Run("harness", func(t *testing.T) { _, _, cid = setup(t) })
	if cid == uuid.Nil {
		t.Skip("harness skipped (no database), nothing to assert about teardown")
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, testdb.AdminDSNValue())
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	defer conn.Close(ctx)

	// ADMIN, deliberately: the app role is FORCE-RLS'd to a single company and
	// would answer 0 for every table whatever survived — a vacuous green.
	for _, tbl := range []string{"company", "account_group", "ledger", "voucher_type", "acl_rule"} {
		var n int
		if err := conn.QueryRow(ctx, "SELECT count(*) FROM "+tbl+" WHERE company_id = $1", cid).Scan(&n); err != nil {
			t.Fatalf("count %s for company %s: %v", tbl, cid, err)
		}
		if n != 0 {
			t.Errorf("%s still holds %d rows for company %s after the harness cleaned up: "+
				"the masters harness leaks them on EVERY run and nothing will ever delete them",
				tbl, n, cid)
		}
	}
}
