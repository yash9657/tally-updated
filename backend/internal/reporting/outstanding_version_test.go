package reporting_test

// REGRESSION: Outstandings must age only the CURRENT, non-deleted version of a
// voucher's bill-wise references.
//
// The bug: outstanding.go summed `bill_allocation` with no join to
// voucher_head, so EVERY version's allocations counted. Two consequences, both
// silent:
//
//   1. ALTER — restating a bill reference double-counts. The superseded
//      version's allocation stays in the sum alongside the new one, so a
//      corrected invoice is aged at (old + new).
//   2. DELETE — a tombstoned voucher's allocations keep ageing forever. The
//      tombstone carries no postings (so ledger_balance drops it correctly) but
//      nothing removed its bill_allocation rows.
//
// Both make Outstandings disagree with the Balance Sheet's Sundry Debtors /
// Creditors totals, which are computed from ledger_balance and DO respect
// voucher_head. Two reports over the same books, silently disagreeing.
//
// The fix mirrors what gstr1.go/gstr3b.go already do: join voucher_head on
// current_version_id and filter is_deleted = false.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/reporting"
)

// billSale posts a credit sale of `amount` to `party`, opening bill reference
// `ref` for the full amount. Returns the logical voucher id.
func billSale(t *testing.T, led *ledger.Service, ctx context.Context,
	party, sales uuid.UUID, no, date, ref, amount string,
) uuid.UUID {
	t.Helper()
	res, err := led.PostVoucher(ctx, buildBillSale(party, sales, no, date, ref, amount))
	if err != nil {
		t.Fatalf("post %s: %v", no, err)
	}
	return res.VoucherID
}

func buildBillSale(party, sales uuid.UUID, no, date, ref, amount string) ledger.VoucherInput {
	amt := dec(amount)
	return ledger.VoucherInput{
		Class:         ledger.Sales,
		PartyLedgerID: party,
		VoucherNo:     no,
		VoucherDate:   date,
		Party:         "party",
		CreatedBy:     "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: party, DrCr: ledger.Debit, Amount: amt},
			{LedgerID: sales, DrCr: ledger.Credit, Amount: amt},
		},
		BillAllocations: []ledger.BillAllocationInput{
			{
				LedgerID: party, RefName: ref, RefType: ledger.RefNew,
				DrCr: ledger.Debit, Amount: amt, DueDate: date,
			},
		},
	}
}

// debtorsFromBalanceSheet returns the Sundry Debtors total the Balance Sheet
// reports. It is the INDEPENDENT oracle for the Outstandings total: it comes
// from ledger_balance (maintained by the post path, version-aware) rather than
// from bill_allocation, so the two agreeing is a real cross-check.
func debtorsFromBalanceSheet(t *testing.T, svc *reporting.Service, ctx context.Context, companyID uuid.UUID) decimal.Decimal {
	t.Helper()
	bs, err := svc.BalanceSheet(ctx, companyID)
	if err != nil {
		t.Fatalf("balance sheet: %v", err)
	}
	var walk func(nodes []reporting.StatementNode) decimal.Decimal
	walk = func(nodes []reporting.StatementNode) decimal.Decimal {
		for _, n := range nodes {
			if n.Name == "Sundry Debtors" {
				return n.Amount
			}
			if v := walk(n.Children); !v.IsZero() {
				return v
			}
		}
		return decimal.Zero
	}
	return walk(bs.Assets.Nodes)
}

func outstandingTotal(t *testing.T, svc *reporting.Service, ctx context.Context, companyID uuid.UUID) decimal.Decimal {
	t.Helper()
	os, err := svc.Outstandings(ctx, companyID, reporting.Receivables, "", nil)
	if err != nil {
		t.Fatalf("outstandings: %v", err)
	}
	return os.Total
}

// TestOutstandingsUsesCurrentVersionOnly proves an ALTER that restates a bill
// reference is aged at the RESTATED amount, not at old+new, and that the total
// still agrees with the Balance Sheet.
func TestOutstandingsUsesCurrentVersionOnly(t *testing.T) {
	pool, ctx, companyID, _ := setup(t)
	led := ledger.NewService(pool)
	svc := reporting.NewService(pool)
	party, sales := billParties(t, pool, ctx)

	// Raise 24,000 on INV-A, then correct it to 26,500 — restating the bill ref.
	vID := billSale(t, led, ctx, party, sales, "INV-A", "2026-06-19", "INV-A", "24000.0000")
	if _, err := led.AlterVoucher(ctx, vID,
		buildBillSale(party, sales, "INV-A", "2026-06-19", "INV-A", "26500.0000")); err != nil {
		t.Fatalf("alter: %v", err)
	}

	got := outstandingTotal(t, svc, ctx, companyID)
	want := dec("26500")
	if !got.Equal(want) {
		t.Fatalf("Outstandings total = %s, want %s "+
			"(50500 means the superseded version's allocation is still being counted)", got, want)
	}
	// Cross-check against the independent oracle.
	if bs := debtorsFromBalanceSheet(t, svc, ctx, companyID); !bs.Equal(got) {
		t.Fatalf("Balance Sheet Sundry Debtors %s != Outstandings %s", bs, got)
	}
}

// TestOutstandingsExcludesDeletedVouchers proves a tombstoned voucher stops
// ageing, and that the two reports still agree afterwards.
func TestOutstandingsExcludesDeletedVouchers(t *testing.T) {
	pool, ctx, companyID, _ := setup(t)
	led := ledger.NewService(pool)
	svc := reporting.NewService(pool)
	party, sales := billParties(t, pool, ctx)

	billSale(t, led, ctx, party, sales, "KEEP-1", "2026-04-10", "KEEP-1", "10000.0000")
	doomed := billSale(t, led, ctx, party, sales, "GONE-1", "2026-04-11", "GONE-1", "7000.0000")
	if _, err := led.DeleteVoucher(ctx, doomed, "tester"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	got := outstandingTotal(t, svc, ctx, companyID)
	want := dec("10000")
	if !got.Equal(want) {
		t.Fatalf("Outstandings total = %s, want %s "+
			"(17000 means the DELETED voucher's bill reference is still ageing)", got, want)
	}
	if bs := debtorsFromBalanceSheet(t, svc, ctx, companyID); !bs.Equal(got) {
		t.Fatalf("Balance Sheet Sundry Debtors %s != Outstandings %s", bs, got)
	}
}

// billParties creates the debtor + sales ledgers these tests post against.
func billParties(t *testing.T, pool *db.Pool, ctx context.Context) (party, sales uuid.UUID) {
	t.Helper()
	ms := masters.NewService(pool)
	var err error
	party, err = ms.EnsureLedger(ctx, "Bill Party", "Sundry Debtors", "tester")
	if err != nil {
		t.Fatalf("party ledger: %v", err)
	}
	sales, err = ms.EnsureLedger(ctx, "Bill Sales", "Sales Accounts", "tester")
	if err != nil {
		t.Fatalf("sales ledger: %v", err)
	}
	return party, sales
}

// TestOutstandingsIncludesOpeningBills proves the BILL-WISE OPENING BREAKUP is
// part of the ageing report and that the total still ties to the Balance Sheet.
//
// This is not decorative: the Balance Sheet's Sundry Debtors total includes
// ledger.opening_balance (reporting.go folds it into the closing figure). If
// Outstandings read only bill_allocation, the two reports would disagree by
// exactly the opening balance — while each looked internally consistent.
func TestOutstandingsIncludesOpeningBills(t *testing.T) {
	pool, ctx, companyID, _ := setup(t)
	led := ledger.NewService(pool)
	svc := reporting.NewService(pool)
	ms := masters.NewService(pool)

	// A debtor carried in owing two specific invoices, with different due dates.
	party, err := ms.CreateLedger(ctx, masters.Ledger{
		Name: "Carried In Co", GroupName: "Sundry Debtors",
		OpeningBalance: dec("120000"), OpeningDC: "D",
		MaintainBillByBill: true,
		OpeningBills: []masters.OpeningBill{
			{RefName: "OB-1", Amount: dec("70000"), DueDate: "2026-04-20"},
			{RefName: "OB-2", Amount: dec("50000"), DueDate: "2026-05-05"},
		},
	}, "tester")
	if err != nil {
		t.Fatalf("create party with opening: %v", err)
	}
	sales, err := ms.EnsureLedger(ctx, "Opening Sales", "Sales Accounts", "tester")
	if err != nil {
		t.Fatalf("sales ledger: %v", err)
	}

	// Plus one ordinary transactional invoice on top of the opening.
	billSale(t, led, ctx, party, sales, "NEW-1", "2026-06-01", "NEW-1", "30000.0000")

	os, err := svc.Outstandings(ctx, companyID, reporting.Receivables, "2026-07-26", nil)
	if err != nil {
		t.Fatalf("outstandings: %v", err)
	}
	want := dec("150000") // 70,000 + 50,000 opening + 30,000 transactional
	if !os.Total.Equal(want) {
		t.Fatalf("Outstandings total = %s, want %s "+
			"(30000 means the opening bill-wise breakup was ignored)", os.Total, want)
	}
	// The opening references must be aged by THEIR OWN due dates, not lumped.
	seen := map[string]string{}
	for _, p := range os.Parties {
		for _, b := range p.Bills {
			seen[b.RefName] = b.SlabLabel
		}
	}
	for _, ref := range []string{"OB-1", "OB-2", "NEW-1"} {
		if seen[ref] == "" {
			t.Errorf("reference %q missing from the ageing report", ref)
		}
	}
	if seen["OB-1"] == seen["OB-2"] {
		t.Errorf("OB-1 and OB-2 have different due dates but landed in the same slab (%q); "+
			"the opening breakup is not being aged individually", seen["OB-1"])
	}

	// Independent oracle: the Balance Sheet, computed from ledger_balance.
	if bs := debtorsFromBalanceSheet(t, svc, ctx, companyID); !bs.Equal(os.Total) {
		t.Fatalf("Balance Sheet Sundry Debtors %s != Outstandings %s", bs, os.Total)
	}
}

// TestOpeningBillsMustSumToOpeningBalance proves a breakup that does not add up
// is REFUSED. Accepting one would make Outstandings disagree with the Balance
// Sheet by exactly the shortfall, with nothing to indicate why.
func TestOpeningBillsMustSumToOpeningBalance(t *testing.T) {
	pool, ctx, _, _ := setup(t)
	ms := masters.NewService(pool)

	_, err := ms.CreateLedger(ctx, masters.Ledger{
		Name: "Mismatched Co", GroupName: "Sundry Debtors",
		OpeningBalance: dec("100000"), OpeningDC: "D",
		OpeningBills: []masters.OpeningBill{
			{RefName: "OB-1", Amount: dec("70000")},
			{RefName: "OB-2", Amount: dec("20000")}, // 90,000 != 100,000
		},
	}, "tester")
	if err == nil {
		t.Fatal("a breakup summing to 90,000 against a 100,000 opening balance was accepted")
	}
}
