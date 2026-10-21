package query_test

import (
	"testing"

	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/query"
)

func TestAllocationEvidencePartialReceiptAlterAndTenantIsolation(t *testing.T) {
	pool, ctx, _, ids := setupCompany(t, "Allocation evidence")
	seedSales(t, pool, ctx, ids)
	party, err := ids.LedgerID(ctx, "Acme Corp")
	if err != nil {
		t.Fatal(err)
	}
	bank, err := ids.CreateLedger(ctx, "Settlement Bank", "Bank Accounts")
	if err != nil {
		t.Fatal(err)
	}
	vt, err := ids.VoucherTypeID(ctx, "Receipt")
	if err != nil {
		t.Fatal(err)
	}
	in := func(amount string) ledger.VoucherInput {
		return ledger.VoucherInput{Class: ledger.Receipt, VoucherTypeID: vt, VoucherNo: "PARTIAL-1", VoucherDate: "2025-04-20", CreatedBy: "test", Postings: []ledger.PostingInput{{LedgerID: bank, DrCr: ledger.Debit, Amount: dec(amount)}, {LedgerID: party, DrCr: ledger.Credit, Amount: dec(amount)}}, BillAllocations: []ledger.BillAllocationInput{{LedgerID: party, RefName: "INV-1", RefType: ledger.RefAgainst, DrCr: ledger.Credit, Amount: dec(amount)}}}
	}
	led := ledger.NewService(pool)
	posted, err := led.PostVoucher(ctx, in("500"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = led.AlterVoucher(ctx, posted.VoucherID, in("600")); err != nil {
		t.Fatal(err)
	}
	q := query.Query{Entity: query.EntityBillAllocations, Dimensions: []string{"ledger_name", "ref_name"}, Metrics: []query.Metric{{Func: query.AggSum, Column: "debit_minus_credit", As: "remaining"}}, Filters: []query.Filter{{Column: "ref_name", Op: query.OpEq, Values: []string{"INV-1"}}}}
	got, err := query.Execute(ctx, pool, q)
	// Independent arithmetic: invoice 1180 less current receipt 600 = 580;
	// the superseded receipt 500 must not be counted again.
	if err != nil || len(got.Rows) != 1 || got.Rows[0][2] != "580.0000" {
		t.Fatalf("wrong current allocation balance: %+v %v", got, err)
	}
	q.Dimensions = []string{"voucher_no", "voucher_date", "ref_type"}
	q.Metrics = []query.Metric{{Func: query.AggSum, Column: "amount", As: "amount"}}
	q.OrderBy = []query.OrderTerm{{Column: "voucher_no"}}
	got, err = query.Execute(ctx, pool, q)
	if err != nil || len(got.Rows) != 2 || got.Rows[1][0] != "PARTIAL-1" || got.Rows[1][3] != "600.0000" {
		t.Fatalf("missing drilldown: %+v %v", got, err)
	}
	other, otherCtx, _, _ := setupCompany(t, "Unrelated allocation tenant")
	got, err = query.Execute(otherCtx, other, q)
	if err != nil || len(got.Rows) != 0 {
		t.Fatalf("cross-tenant allocation leakage: %+v %v", got, err)
	}
	q.Filters[0].Values = []string{"INV-1' OR '1'='1"}
	got, err = query.Execute(ctx, pool, q)
	if err != nil || len(got.Rows) != 0 {
		t.Fatalf("untrusted reference became SQL: %+v %v", got, err)
	}
}
