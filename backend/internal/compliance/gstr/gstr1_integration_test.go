package gstr_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/compliance"
	"github.com/khata/cored/internal/compliance/gstr"
	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/migrations"
)

const (
	adminDSN = "postgres://postgres:postgres@localhost:5433/tally?sslmode=disable"
	appDSN   = "postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable"
)

const supplierState = "29" // Karnataka — the filing company's state.

// setup migrates the live DB (idempotent) and returns an app-scoped pool plus a
// freshly created, seeded company scoped into the returned context. Each test
// gets its own company id so tenant-isolated rows never collide across runs.
func setup(t *testing.T) (*db.Pool, context.Context, uuid.UUID) {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	ctx := context.Background()

	adminConn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable, skipping integration test: %v", err)
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

	companyID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new company id: %v", err)
	}
	cctx := db.WithCompany(ctx, companyID)

	ids := identity.NewService(pool)
	if _, err := ids.CreateCompany(cctx, identity.Company{
		ID:          companyID,
		Name:        "GSTR Co " + companyID.String()[:8],
		FYStartDate: "2025-04-01",
	}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	return pool, cctx, companyID
}

// dec is a brief decimal constructor for the test.
func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// gstSale posts one intra/inter-state Sales voucher through the full GST path
// (compliance.TaxDerive runs inside the post and cross-checks the tax lines).
// It returns nothing; a failed post fails the test.
func gstSale(t *testing.T, led *ledger.Service, ctx context.Context, vtID, party, sales, cgst, sgst, igst uuid.UUID,
	no, date, pos string, taxable string, intra bool) {
	t.Helper()

	tx := dec(taxable)
	postings := []ledger.PostingInput{
		{LedgerID: sales, DrCr: ledger.Credit, Amount: tx},
	}
	gp := &ledger.GSTParams{
		SupplierStateCode:      supplierState,
		PlaceOfSupplyStateCode: pos,
		Lines:                  []compliance.Line{{TaxableValue: tx, GSTRatePct: dec("18"), HSN: "8471"}},
		CGSTLedgerID:           cgst,
		SGSTLedgerID:           sgst,
		IGSTLedgerID:           igst,
	}
	var total decimal.Decimal
	if intra {
		half := tx.Mul(dec("9")).Div(dec("100")).Round(4)
		postings = append(postings,
			ledger.PostingInput{LedgerID: cgst, DrCr: ledger.Credit, Amount: half},
			ledger.PostingInput{LedgerID: sgst, DrCr: ledger.Credit, Amount: half},
		)
		total = tx.Add(half).Add(half)
	} else {
		full := tx.Mul(dec("18")).Div(dec("100")).Round(4)
		postings = append(postings,
			ledger.PostingInput{LedgerID: igst, DrCr: ledger.Credit, Amount: full},
		)
		total = tx.Add(full)
	}
	// Debit the debtor for the invoice total to balance the entry.
	postings = append([]ledger.PostingInput{
		{LedgerID: party, DrCr: ledger.Debit, Amount: total},
	}, postings...)

	if _, err := led.PostVoucher(ctx, ledger.VoucherInput{
		Class:         ledger.Sales,
		VoucherTypeID: vtID,
		PartyLedgerID: party,
		VoucherNo:     no,
		VoucherDate:   date,
		Party:         "party",
		CreatedBy:     "tester",
		Postings:      postings,
		GST:           gp,
	}); err != nil {
		t.Fatalf("post sale %s: %v", no, err)
	}
}

func TestGenerateGSTR1_SectionsAndReconciliation(t *testing.T) {
	pool, ctx, _ := setup(t)

	ms := masters.NewService(pool)
	led := ledger.NewService(pool)

	vtID, err := ms.VoucherTypeID(ctx, "Sales")
	if err != nil {
		t.Fatalf("voucher type: %v", err)
	}

	// Output-tax ledgers.
	cgst, err := ms.EnsureLedger(ctx, "Output CGST", "Duties & Taxes", "tester")
	if err != nil {
		t.Fatalf("cgst: %v", err)
	}
	sgst, err := ms.EnsureLedger(ctx, "Output SGST", "Duties & Taxes", "tester")
	if err != nil {
		t.Fatalf("sgst: %v", err)
	}
	igst, err := ms.EnsureLedger(ctx, "Output IGST", "Duties & Taxes", "tester")
	if err != nil {
		t.Fatalf("igst: %v", err)
	}
	salesLed, err := ms.EnsureLedger(ctx, "Domestic Sales", "Sales Accounts", "tester")
	if err != nil {
		t.Fatalf("sales: %v", err)
	}

	// Registered B2B debtor (REGULAR, with GSTIN), in-state (29).
	b2bParty, err := ms.CreateLedger(ctx, masters.Ledger{
		Name: "Acme Pvt Ltd", GroupName: "Sundry Debtors",
		GSTIN: "29ABCDE1234F1Z5", StateCode: "29",
		RegistrationType: masters.GSTRegular,
	}, "tester")
	if err != nil {
		t.Fatalf("b2b party: %v", err)
	}
	// Unregistered B2C debtor for the large inter-state invoice (POS 27).
	b2clParty, err := ms.CreateLedger(ctx, masters.Ledger{
		Name: "Walk-in Mumbai", GroupName: "Sundry Debtors",
		StateCode: "27", RegistrationType: masters.GSTConsumer,
	}, "tester")
	if err != nil {
		t.Fatalf("b2cl party: %v", err)
	}
	// Unregistered B2C debtor for the small intra-state invoice (POS 29).
	b2csParty, err := ms.CreateLedger(ctx, masters.Ledger{
		Name: "Walk-in Local", GroupName: "Sundry Debtors",
		StateCode: "29", RegistrationType: masters.GSTConsumer,
	}, "tester")
	if err != nil {
		t.Fatalf("b2cs party: %v", err)
	}

	// Post three sales in May 2025.
	// B2B intra @18% on 10000 -> CGST 900, SGST 900.
	gstSale(t, led, ctx, vtID, b2bParty, salesLed, cgst, sgst, igst,
		"S-1", "2025-05-05", "29", "10000.0000", true)
	// B2CL inter @18% on 200000 -> IGST 36000; invoice 236000 > 1L.
	gstSale(t, led, ctx, vtID, b2clParty, salesLed, cgst, sgst, igst,
		"S-2", "2025-05-10", "27", "200000.0000", false)
	// B2CS intra @18% on 5000 -> CGST 450, SGST 450; invoice 5900.
	gstSale(t, led, ctx, vtID, b2csParty, salesLed, cgst, sgst, igst,
		"S-3", "2025-05-20", "29", "5000.0000", true)

	gen := gstr.NewService(pool)
	opts := gstr.Options{
		GSTIN:        "29ZZZZZ0000Z1Z5",
		CGSTLedgerID: cgst, SGSTLedgerID: sgst, IGSTLedgerID: igst,
	}

	g, recon, err := gen.Reconcile(ctx, "2025-05-01", "2025-05-31", opts)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// --- FP and GSTIN ---
	if g.FP != "052025" {
		t.Fatalf("FP: want 052025, got %s", g.FP)
	}
	if g.GSTIN != "29ZZZZZ0000Z1Z5" {
		t.Fatalf("GSTIN: got %s", g.GSTIN)
	}

	// --- B2B section ---
	if len(g.B2B) != 1 {
		t.Fatalf("B2B: want 1 party, got %d", len(g.B2B))
	}
	if g.B2B[0].CTIN != "29ABCDE1234F1Z5" {
		t.Fatalf("B2B ctin: got %s", g.B2B[0].CTIN)
	}
	if len(g.B2B[0].Inv) != 1 {
		t.Fatalf("B2B: want 1 invoice, got %d", len(g.B2B[0].Inv))
	}
	inv := g.B2B[0].Inv[0]
	if inv.Inum != "S-1" {
		t.Fatalf("B2B inum: got %s", inv.Inum)
	}
	if inv.Idt != "05-05-2025" {
		t.Fatalf("B2B idt: got %s", inv.Idt)
	}
	if !inv.Val.Equal(dec("11800")) {
		t.Fatalf("B2B val: want 11800, got %s", inv.Val)
	}
	if len(inv.Itms) != 1 {
		t.Fatalf("B2B itms: want 1, got %d", len(inv.Itms))
	}
	d := inv.Itms[0].ItmDet
	if !d.Rt.Equal(dec("18")) || !d.Txval.Equal(dec("10000")) ||
		!d.Camt.Equal(dec("900")) || !d.Samt.Equal(dec("900")) || !d.Iamt.Equal(dec("0")) {
		t.Fatalf("B2B item detail mismatch: %+v", d)
	}

	// --- B2CL section ---
	if len(g.B2CL) != 1 {
		t.Fatalf("B2CL: want 1 pos group, got %d", len(g.B2CL))
	}
	if g.B2CL[0].Pos != "27" {
		t.Fatalf("B2CL pos: got %s", g.B2CL[0].Pos)
	}
	if len(g.B2CL[0].Inv) != 1 || g.B2CL[0].Inv[0].Inum != "S-2" {
		t.Fatalf("B2CL inv: %+v", g.B2CL[0].Inv)
	}
	if !g.B2CL[0].Inv[0].Val.Equal(dec("236000")) {
		t.Fatalf("B2CL val: want 236000, got %s", g.B2CL[0].Inv[0].Val)
	}
	bd := g.B2CL[0].Inv[0].Itms[0].ItmDet
	if !bd.Iamt.Equal(dec("36000")) || !bd.Txval.Equal(dec("200000")) {
		t.Fatalf("B2CL item detail mismatch: %+v", bd)
	}

	// --- B2CS section ---
	if len(g.B2CS) != 1 {
		t.Fatalf("B2CS: want 1 bucket, got %d", len(g.B2CS))
	}
	bs := g.B2CS[0]
	if bs.SplyTy != "INTRA" || bs.Pos != "29" || !bs.Rt.Equal(dec("18")) {
		t.Fatalf("B2CS bucket: %+v", bs)
	}
	if !bs.Txval.Equal(dec("5000")) || !bs.Camt.Equal(dec("450")) || !bs.Samt.Equal(dec("450")) {
		t.Fatalf("B2CS amounts: %+v", bs)
	}

	// --- HSN summary: two rate buckets? all at 18% -> single rate row ---
	if len(g.HSN.Data) != 1 {
		t.Fatalf("HSN: want 1 rate row, got %d", len(g.HSN.Data))
	}
	h := g.HSN.Data[0]
	if !h.Rt.Equal(dec("18")) {
		t.Fatalf("HSN rate: got %s", h.Rt)
	}
	if !h.Txval.Equal(dec("215000")) { // 10000 + 200000 + 5000
		t.Fatalf("HSN txval: want 215000, got %s", h.Txval)
	}
	if !h.Camt.Equal(dec("1350")) || !h.Samt.Equal(dec("1350")) || !h.Iamt.Equal(dec("36000")) {
		t.Fatalf("HSN tax: c=%s s=%s i=%s", h.Camt, h.Samt, h.Iamt)
	}

	// --- Reconciliation: GSTR-1 tax == posted output tax, to the paisa ---
	if !recon.Balanced() {
		t.Fatalf("reconciliation not balanced:\n return  c=%s s=%s i=%s\n posted  c=%s s=%s i=%s",
			recon.Return.CGST, recon.Return.SGST, recon.Return.IGST,
			recon.Postings.CGST, recon.Postings.SGST, recon.Postings.IGST)
	}
	// Cross-check the absolute numbers.
	if !recon.Postings.CGST.Equal(dec("1350")) ||
		!recon.Postings.SGST.Equal(dec("1350")) ||
		!recon.Postings.IGST.Equal(dec("36000")) {
		t.Fatalf("posted totals wrong: %+v", recon.Postings)
	}
	if !recon.Return.Total().Equal(dec("38700")) { // 1350+1350+36000
		t.Fatalf("return total tax: want 38700, got %s", recon.Return.Total())
	}

	// The output must marshal to GSTN-shaped JSON.
	raw, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("marshal gstr1: %v", err)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("unmarshal probe: %v", err)
	}
	for _, k := range []string{"fp", "b2b", "b2cl", "b2cs", "hsn"} {
		if _, ok := probe[k]; !ok {
			t.Fatalf("gstr1 json missing key %q; got %s", k, raw)
		}
	}
}

// TestGenerate_RejectsBadDates verifies structural input validation.
func TestGenerate_RejectsBadDates(t *testing.T) {
	pool, ctx, _ := setup(t)
	gen := gstr.NewService(pool)
	if _, err := gen.Generate(ctx, "not-a-date", "2025-05-31", gstr.Options{}); err == nil {
		t.Fatal("expected error for bad from date")
	}
	if _, err := gen.Generate(ctx, "2025-05-31", "2025-05-01", gstr.Options{}); err == nil {
		t.Fatal("expected error for end before start")
	}
}

// TestGenerate_NoCompanyGuard verifies the tenant-scope guard rejects calls
// with no company in context, before any DB round-trip.
func TestGenerate_NoCompanyGuard(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	pool, err := db.New(context.Background(), appDSN)
	if err != nil {
		t.Skipf("app pool unavailable: %v", err)
	}
	defer pool.Close()
	gen := gstr.NewService(pool)
	if _, err := gen.Generate(context.Background(), "2025-05-01", "2025-05-31", gstr.Options{}); err != db.ErrNoCompany {
		t.Fatalf("want ErrNoCompany, got %v", err)
	}
}

// buildIntraSale constructs a balanced intra-state B2B Sales VoucherInput at
// 18% (CGST 9% + SGST 9%) for the given taxable value. Used to post then alter
// the SAME logical voucher so the GSTR-1 current-version filter can be proven.
func buildIntraSale(vtID, party, sales, cgst, sgst, igst uuid.UUID, no, date, taxable string) ledger.VoucherInput {
	tx := dec(taxable)
	half := tx.Mul(dec("9")).Div(dec("100")).Round(4)
	total := tx.Add(half).Add(half)
	return ledger.VoucherInput{
		Class:         ledger.Sales,
		VoucherTypeID: vtID,
		PartyLedgerID: party,
		VoucherNo:     no,
		VoucherDate:   date,
		Party:         "party",
		CreatedBy:     "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: party, DrCr: ledger.Debit, Amount: total},
			{LedgerID: sales, DrCr: ledger.Credit, Amount: tx},
			{LedgerID: cgst, DrCr: ledger.Credit, Amount: half},
			{LedgerID: sgst, DrCr: ledger.Credit, Amount: half},
		},
		GST: &ledger.GSTParams{
			SupplierStateCode:      supplierState,
			PlaceOfSupplyStateCode: "29",
			Lines:                  []compliance.Line{{TaxableValue: tx, GSTRatePct: dec("18"), HSN: "8471"}},
			CGSTLedgerID:           cgst,
			SGSTLedgerID:           sgst,
			IGSTLedgerID:           igst,
		},
	}
}

// TestGSTR1_ReflectsAlterAndDelete proves GSTR-1 reads only the CURRENT,
// non-deleted version of each voucher. Before the voucher_head join + is_deleted
// filter, altering a sale's taxable value made the invoice appear TWICE (both
// versions carry taxable_value) and a delete left the original visible —
// over-reporting turnover and output tax to the CA. This is the compliance
// analogue of the Day Book current-version fix.
func TestGSTR1_ReflectsAlterAndDelete(t *testing.T) {
	pool, ctx, _ := setup(t)
	ms := masters.NewService(pool)
	led := ledger.NewService(pool)

	vtID, err := ms.VoucherTypeID(ctx, "Sales")
	if err != nil {
		t.Fatalf("voucher type: %v", err)
	}
	cgst, _ := ms.EnsureLedger(ctx, "Output CGST", "Duties & Taxes", "tester")
	sgst, _ := ms.EnsureLedger(ctx, "Output SGST", "Duties & Taxes", "tester")
	igst, _ := ms.EnsureLedger(ctx, "Output IGST", "Duties & Taxes", "tester")
	salesLed, _ := ms.EnsureLedger(ctx, "Domestic Sales", "Sales Accounts", "tester")
	party, err := ms.CreateLedger(ctx, masters.Ledger{
		Name: "Acme Pvt Ltd", GroupName: "Sundry Debtors",
		GSTIN: "29ABCDE1234F1Z5", StateCode: "29", RegistrationType: masters.GSTRegular,
	}, "tester")
	if err != nil {
		t.Fatalf("party: %v", err)
	}

	gen := gstr.NewService(pool)
	opts := gstr.Options{GSTIN: "29ZZZZZ0000Z1Z5", CGSTLedgerID: cgst, SGSTLedgerID: sgst, IGSTLedgerID: igst}

	// 1. Post a B2B intra sale of 10000 (CGST 900 / SGST 900, invoice 11800).
	res, err := led.PostVoucher(ctx, buildIntraSale(vtID, party, salesLed, cgst, sgst, igst, "S-1", "2025-05-05", "10000.0000"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}

	// 2. ALTER the SAME voucher to 20000 (CGST 1800 / SGST 1800), resending S-1.
	if _, err := led.AlterVoucher(ctx, res.VoucherID, buildIntraSale(vtID, party, salesLed, cgst, sgst, igst, "S-1", "2025-05-05", "20000.0000")); err != nil {
		t.Fatalf("alter: %v", err)
	}

	g, err := gen.Generate(ctx, "2025-05-01", "2025-05-31", opts)
	if err != nil {
		t.Fatalf("generate after alter: %v", err)
	}
	// Exactly ONE B2B invoice, reflecting the EDITED 20000 — not two versions,
	// and not the 30000 sum of both.
	if len(g.B2B) != 1 || len(g.B2B[0].Inv) != 1 {
		t.Fatalf("after alter: want exactly 1 B2B invoice, got %d parties", len(g.B2B))
	}
	inv := g.B2B[0].Inv[0]
	if !inv.Val.Equal(dec("23600")) { // 20000 + 1800 + 1800
		t.Fatalf("after alter: invoice value want 23600 (edited), got %s (double-count would be 35400)", inv.Val)
	}

	// 3. DELETE the voucher; it must vanish from GSTR-1 entirely.
	if _, err := led.DeleteVoucher(ctx, res.VoucherID, "tester"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	g, err = gen.Generate(ctx, "2025-05-01", "2025-05-31", opts)
	if err != nil {
		t.Fatalf("generate after delete: %v", err)
	}
	if len(g.B2B) != 0 {
		t.Fatalf("after delete: want 0 B2B invoices, got %d parties", len(g.B2B))
	}
}
