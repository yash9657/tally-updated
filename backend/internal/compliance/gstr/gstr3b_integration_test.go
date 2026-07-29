package gstr_test

// gstr3b_integration_test.go carries the GSTR-3B coverage the migration corpus
// structurally cannot: the ITC and reverse-charge halves. The Tally importer
// attaches GSTParams only to SALES vouchers (internal/migration/migration.go:514
// and :566), so NO imported corpus voucher can ever stamp the PURCHASE-side GST
// columns. Everything below therefore posts purchases DIRECTLY through
// ledger.Service, bypassing the importer.
//
// Package-scope helpers declared in gstr1_integration_test.go are reused, NOT
// redeclared: adminDSN/appDSN, supplierState, setup, dec, gstSale.

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/compliance"
	"github.com/khata/cored/internal/compliance/gstr"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/platform/db"
)

// purchaseLedgers is the master set a 3B test needs on the inward side.
type purchaseLedgers struct {
	vtID                           uuid.UUID
	supplier                       uuid.UUID
	purchases                      uuid.UUID
	inCGST, inSGST, inIGST         uuid.UUID
	rcmOutCGST, rcmOutSGST         uuid.UUID
	outCGST, outSGST, outIGST      uuid.UUID
	salesVTID, salesLedger         uuid.UUID
	b2bParty, b2clParty, b2csParty uuid.UUID
	// b2bInterParty is a REGISTERED out-of-state recipient. It exists so table
	// 3.2's "unregistered only" filter is actually exercised: without an
	// inter-state supply to a REGISTERED party, deleting the `!r.registered`
	// condition from build3B would not change any assertion (verified by
	// mutation test).
	b2bInterParty uuid.UUID
}

// setupPurchaseMasters creates the Sales AND Purchase master sets used by the 3B
// tests. Names are per-company (each test gets a fresh company id from setup),
// so EnsureLedger cannot collide across tests.
func setupPurchaseMasters(t *testing.T, ms *masters.Service, ctx context.Context) purchaseLedgers {
	t.Helper()
	var p purchaseLedgers
	var err error

	if p.salesVTID, err = ms.VoucherTypeID(ctx, "Sales"); err != nil {
		t.Fatalf("sales voucher type: %v", err)
	}
	if p.vtID, err = ms.VoucherTypeID(ctx, "Purchase"); err != nil {
		t.Fatalf("purchase voucher type: %v", err)
	}

	ensure := func(name, group string) uuid.UUID {
		t.Helper()
		id, err := ms.EnsureLedger(ctx, name, group, "tester")
		if err != nil {
			t.Fatalf("ensure ledger %s: %v", name, err)
		}
		return id
	}
	p.purchases = ensure("Local Purchases", "Purchase Accounts")
	p.inCGST = ensure("Input CGST", "Duties & Taxes")
	p.inSGST = ensure("Input SGST", "Duties & Taxes")
	p.inIGST = ensure("Input IGST", "Duties & Taxes")
	p.rcmOutCGST = ensure("RCM Payable CGST", "Duties & Taxes")
	p.rcmOutSGST = ensure("RCM Payable SGST", "Duties & Taxes")
	p.outCGST = ensure("Output CGST", "Duties & Taxes")
	p.outSGST = ensure("Output SGST", "Duties & Taxes")
	p.outIGST = ensure("Output IGST", "Duties & Taxes")
	p.salesLedger = ensure("Domestic Sales", "Sales Accounts")
	p.supplier = ensure("Local Supplier", "Sundry Creditors")
	return p
}

// gstPurchase posts one INTRA-state Purchase voucher at 18% through the full GST
// path. When rcm is false it is a plain ITC purchase (input tax debited, supplier
// credited with the tax-inclusive total). When rcm is true the supplier is
// credited with the taxable value only (compliance.TaxDerive drops the tax from
// the invoice total under reverse charge) and the self-assessed tax is booked as
// Dr input-tax / Cr RCM-payable, which is how a real RCM purchase is recorded.
//
// The RCM-payable legs deliberately use DIFFERENT ledgers from the input-tax
// legs: ledger.deriveAndVerifyGST cross-checks the SUM of postings on the ledger
// id passed as CGSTLedgerID against the derived split, so booking both sides on
// one ledger would double it and the post would be rejected.
func gstPurchase(t *testing.T, led *ledger.Service, ctx context.Context, p purchaseLedgers,
	no, date, taxable string, rcm bool) uuid.UUID {
	t.Helper()

	tx := dec(taxable)
	half := tx.Mul(dec("9")).Div(dec("100")).Round(4)

	postings := []ledger.PostingInput{
		{LedgerID: p.purchases, DrCr: ledger.Debit, Amount: tx},
		{LedgerID: p.inCGST, DrCr: ledger.Debit, Amount: half},
		{LedgerID: p.inSGST, DrCr: ledger.Debit, Amount: half},
	}
	if rcm {
		postings = append(postings,
			ledger.PostingInput{LedgerID: p.supplier, DrCr: ledger.Credit, Amount: tx},
			ledger.PostingInput{LedgerID: p.rcmOutCGST, DrCr: ledger.Credit, Amount: half},
			ledger.PostingInput{LedgerID: p.rcmOutSGST, DrCr: ledger.Credit, Amount: half},
		)
	} else {
		postings = append(postings,
			ledger.PostingInput{LedgerID: p.supplier, DrCr: ledger.Credit, Amount: tx.Add(half).Add(half)},
		)
	}

	res, err := led.PostVoucher(ctx, ledger.VoucherInput{
		Class:         ledger.Purchase,
		VoucherTypeID: p.vtID,
		PartyLedgerID: p.supplier,
		VoucherNo:     no,
		VoucherDate:   date,
		Party:         "supplier",
		CreatedBy:     "tester",
		Postings:      postings,
		GST: &ledger.GSTParams{
			SupplierStateCode:      supplierState,
			PlaceOfSupplyStateCode: supplierState, // intra-state purchase
			ReverseCharge:          rcm,
			Lines:                  []compliance.Line{{TaxableValue: tx, GSTRatePct: dec("18"), HSN: "8471"}},
			CGSTLedgerID:           p.inCGST,
			SGSTLedgerID:           p.inSGST,
			IGSTLedgerID:           p.inIGST,
		},
	})
	if err != nil {
		t.Fatalf("post purchase %s (rcm=%v): %v", no, rcm, err)
	}
	return res.VoucherID
}

// seedSales posts the same three sales as the GSTR-1 integration test (B2B intra
// 10000, B2CL inter 200000 to an UNREGISTERED Mumbai party, B2CS intra 5000)
// PLUS a fourth: an inter-state sale of 50000 to a REGISTERED Maharashtra party
// (IGST 9000). The fourth invoice is what makes table 3.2 discriminating — it is
// inter-state but must NOT appear in 3.2, which reports supplies to
// UNREGISTERED persons only.
func seedSales(t *testing.T, ms *masters.Service, led *ledger.Service, ctx context.Context, p *purchaseLedgers) {
	t.Helper()
	var err error
	if p.b2bParty, err = ms.CreateLedger(ctx, masters.Ledger{
		Name: "Acme Pvt Ltd", GroupName: "Sundry Debtors",
		GSTIN: "29ABCDE1234F1Z5", StateCode: "29", RegistrationType: masters.GSTRegular,
	}, "tester"); err != nil {
		t.Fatalf("b2b party: %v", err)
	}
	if p.b2clParty, err = ms.CreateLedger(ctx, masters.Ledger{
		Name: "Walk-in Mumbai", GroupName: "Sundry Debtors",
		StateCode: "27", RegistrationType: masters.GSTConsumer,
	}, "tester"); err != nil {
		t.Fatalf("b2cl party: %v", err)
	}
	if p.b2csParty, err = ms.CreateLedger(ctx, masters.Ledger{
		Name: "Walk-in Local", GroupName: "Sundry Debtors",
		StateCode: "29", RegistrationType: masters.GSTConsumer,
	}, "tester"); err != nil {
		t.Fatalf("b2cs party: %v", err)
	}
	if p.b2bInterParty, err = ms.CreateLedger(ctx, masters.Ledger{
		Name: "Mumbai Traders Pvt Ltd", GroupName: "Sundry Debtors",
		GSTIN: "27PQRST5678G1Z9", StateCode: "27", RegistrationType: masters.GSTRegular,
	}, "tester"); err != nil {
		t.Fatalf("b2b inter party: %v", err)
	}
	gstSale(t, led, ctx, p.salesVTID, p.b2bParty, p.salesLedger, p.outCGST, p.outSGST, p.outIGST,
		"S-1", "2025-05-05", "29", "10000.0000", true)
	gstSale(t, led, ctx, p.salesVTID, p.b2clParty, p.salesLedger, p.outCGST, p.outSGST, p.outIGST,
		"S-2", "2025-05-10", "27", "200000.0000", false)
	gstSale(t, led, ctx, p.salesVTID, p.b2csParty, p.salesLedger, p.outCGST, p.outSGST, p.outIGST,
		"S-3", "2025-05-20", "29", "5000.0000", true)
	// Inter-state to a REGISTERED party: enters 3.1(a), must stay OUT of 3.2.
	gstSale(t, led, ctx, p.salesVTID, p.b2bInterParty, p.salesLedger, p.outCGST, p.outSGST, p.outIGST,
		"S-4", "2025-05-22", "27", "50000.0000", false)
}

func opts3B(p purchaseLedgers) gstr.Options3B {
	return gstr.Options3B{
		Options: gstr.Options{
			GSTIN:        "29ZZZZZ0000Z1Z5",
			CGSTLedgerID: p.outCGST, SGSTLedgerID: p.outSGST, IGSTLedgerID: p.outIGST,
		},
		InputCGSTLedgerID: p.inCGST,
		InputSGSTLedgerID: p.inSGST,
		InputIGSTLedgerID: p.inIGST,
	}
}

// TestGenerate3B_AllTables is the main hermetic gate: three sales, one plain ITC
// purchase and one REVERSE-CHARGE purchase, then every 3B table is asserted
// against hand-computed figures, and both reconciliation halves must tie to the
// posted tax ledgers.
//
// Hand-derived expectations (18% throughout, intra = 9+9):
//
//	3.1(a): txval 10000+200000+5000+50000 = 265000; CGST 900+450 = 1350;
//	        SGST 1350; IGST 36000+9000 = 45000 (the two inter-state sales).
//	3.1(d): the RCM purchase only — txval 4000, CGST 360, SGST 360, IGST 0.
//	3.2   : the UNREGISTERED inter-state sale only — POS 27, txval 200000,
//	        IGST 36000. S-4 is also inter-state to POS 27 but the recipient is
//	        REGISTERED, so it must NOT be added to that row.
//	4 ITC : OTH = the plain purchase 2000 @18% -> CGST 180 / SGST 180;
//	        ISRC = the RCM purchase 4000 @18% -> CGST 360 / SGST 360;
//	        NET = 540 / 540 / 0.
func TestGenerate3B_AllTables(t *testing.T) {
	pool, ctx, _ := setup(t)
	ms := masters.NewService(pool)
	led := ledger.NewService(pool)

	p := setupPurchaseMasters(t, ms, ctx)
	seedSales(t, ms, led, ctx, &p)
	gstPurchase(t, led, ctx, p, "PU-1", "2025-05-12", "2000.0000", false) // plain ITC
	gstPurchase(t, led, ctx, p, "PU-2", "2025-05-18", "4000.0000", true)  // reverse charge

	gen := gstr.NewService(pool)
	g, recon, err := gen.Reconcile3B(ctx, "2025-05-01", "2025-05-31", opts3B(p))
	if err != nil {
		t.Fatalf("reconcile3b: %v", err)
	}

	if g.FP != "052025" {
		t.Fatalf("FP: want 052025, got %s", g.FP)
	}
	if g.GSTIN != "29ZZZZZ0000Z1Z5" {
		t.Fatalf("GSTIN: got %s", g.GSTIN)
	}

	// --- 3.1(a) outward ---
	a := g.T31.OsupDet
	if !a.Txval.Equal(dec("265000")) || !a.Camt.Equal(dec("1350")) ||
		!a.Samt.Equal(dec("1350")) || !a.Iamt.Equal(dec("45000")) {
		t.Fatalf("3.1(a) mismatch: %+v", a)
	}
	// The purchases must NOT leak into the outward row (that would be 271000).
	if a.Txval.Equal(dec("271000")) {
		t.Fatal("3.1(a) includes purchases — class filter broken")
	}

	// --- 3.1(b)/(c)/(e) are all zero in v1 ---
	for name, row := range map[string]gstr.Table31Row{
		"3.1(b)": g.T31.OsupZero, "3.1(c)": g.T31.OsupNilExmp, "3.1(e)": g.T31.OsupNongst,
	} {
		if !row.Txval.IsZero() || !row.Camt.IsZero() || !row.Samt.IsZero() || !row.Iamt.IsZero() {
			t.Fatalf("%s must be zero in v1, got %+v", name, row)
		}
	}

	// --- 3.1(d) inward reverse charge (NON-ZERO: the corpus cannot cover this) ---
	d := g.T31.IsupRev
	if !d.Txval.Equal(dec("4000")) || !d.Camt.Equal(dec("360")) ||
		!d.Samt.Equal(dec("360")) || !d.Iamt.Equal(dec("0")) {
		t.Fatalf("3.1(d) mismatch: %+v (want txval 4000, c/s 360, i 0)", d)
	}

	// --- 3.2 inter-state to unregistered ---
	if len(g.T32) != 1 {
		t.Fatalf("3.2: want exactly 1 POS row, got %d (%+v)", len(g.T32), g.T32)
	}
	// 250000/45000 here would mean the REGISTERED inter-state sale S-4 leaked in.
	if g.T32[0].Pos != "27" || !g.T32[0].Txval.Equal(dec("200000")) || !g.T32[0].Iamt.Equal(dec("36000")) {
		t.Fatalf("3.2 row: %+v (registered recipients must be excluded; leak would show 250000/45000)", g.T32[0])
	}

	// --- table 4 ITC (NON-ZERO) ---
	byTy := map[string]gstr.Table4Row{}
	for _, r := range g.T4.ItcAvl {
		byTy[r.Ty] = r
	}
	// The 4(A) row set is checked EXACTLY, by name. A `len == 2` assertion went
	// brittle the moment IMPG/IMPS/ISD became emittable — it would fail on a
	// correct return that gained a row, which is the kind of assertion that
	// gets "fixed" by loosening it. Naming the rows says what is true: these
	// books have reverse-charge and ordinary ITC and nothing else, so exactly
	// ISRC and OTH may appear, in that order.
	var gotTys []string
	for _, r := range g.T4.ItcAvl {
		gotTys = append(gotTys, r.Ty)
	}
	if len(gotTys) != 2 || gotTys[0] != "ISRC" || gotTys[1] != "OTH" {
		t.Fatalf("itc_avl: want exactly [ISRC OTH] in GSTN row order, got %v (%+v)", gotTys, g.T4.ItcAvl)
	}
	// With no ITCSource nothing may be reversed and nothing may be ineligible.
	if len(g.T4.ItcRev) != 0 || len(g.T4.ItcInelg) != 0 {
		t.Fatalf("no ITCSource: itc_rev/itc_inelg must be absent, got rev=%+v inelg=%+v",
			g.T4.ItcRev, g.T4.ItcInelg)
	}
	if oth := byTy["OTH"]; !oth.Camt.Equal(dec("180")) || !oth.Samt.Equal(dec("180")) || !oth.Iamt.Equal(dec("0")) {
		t.Fatalf("itc OTH: %+v (want c/s 180)", oth)
	}
	if isrc := byTy["ISRC"]; !isrc.Camt.Equal(dec("360")) || !isrc.Samt.Equal(dec("360")) || !isrc.Iamt.Equal(dec("0")) {
		t.Fatalf("itc ISRC: %+v (want c/s 360)", isrc)
	}
	if !g.T4.ItcNet.Camt.Equal(dec("540")) || !g.T4.ItcNet.Samt.Equal(dec("540")) || !g.T4.ItcNet.Iamt.Equal(dec("0")) {
		t.Fatalf("itc_net: %+v (want c/s 540)", g.T4.ItcNet)
	}

	// --- reconciliation: BOTH halves tie to the posted tax ledgers ---
	if !recon.Balanced() {
		t.Fatalf("3B reconciliation not balanced:\n outward %+v vs postings %+v\n itc %+v vs postings %+v",
			recon.Outward, recon.OutwardPostings, recon.ITC, recon.ITCPostings)
	}
	// Absolute cross-check of the books side (independent of the return).
	if !recon.OutwardPostings.CGST.Equal(dec("1350")) || !recon.OutwardPostings.IGST.Equal(dec("45000")) {
		t.Fatalf("outward postings: %+v", recon.OutwardPostings)
	}
	if !recon.ITCPostings.CGST.Equal(dec("540")) || !recon.ITCPostings.SGST.Equal(dec("540")) {
		t.Fatalf("itc postings: %+v (want c/s 540)", recon.ITCPostings)
	}

	// --- JSON shape ---
	raw, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("marshal gstr3b: %v", err)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("unmarshal probe: %v", err)
	}
	for _, k := range []string{"fp", "sup_details", "inter_sup", "itc_elg"} {
		if _, ok := probe[k]; !ok {
			t.Fatalf("gstr3b json missing key %q; got %s", k, raw)
		}
	}
}

// TestGSTR3B_ConsistentWithGSTR1 is the cheapest INDEPENDENT oracle: 3.1(a) is
// produced by Generate3B's own aggregating SQL while GSTR1.TaxTotals() /
// sum(HSN.Txval) are produced by Generate's invoice-wise SQL + classification.
// Break either query alone and this test goes red.
//
// It also asserts the GSTN cross-table rule 3.2 <= 3.1(a) on real data.
func TestGSTR3B_ConsistentWithGSTR1(t *testing.T) {
	pool, ctx, _ := setup(t)
	ms := masters.NewService(pool)
	led := ledger.NewService(pool)

	p := setupPurchaseMasters(t, ms, ctx)
	seedSales(t, ms, led, ctx, &p)
	// Purchases exist too: they must NOT disturb the outward-side equality.
	gstPurchase(t, led, ctx, p, "PU-1", "2025-05-12", "2000.0000", false)
	gstPurchase(t, led, ctx, p, "PU-2", "2025-05-18", "4000.0000", true)

	gen := gstr.NewService(pool)
	o := opts3B(p)

	g1, err := gen.Generate(ctx, "2025-05-01", "2025-05-31", o.Options)
	if err != nil {
		t.Fatalf("generate gstr1: %v", err)
	}
	g3, err := gen.Generate3B(ctx, "2025-05-01", "2025-05-31", o)
	if err != nil {
		t.Fatalf("generate gstr3b: %v", err)
	}

	// Head-for-head tax equality.
	if !g3.OutwardTax().Equal(g1.TaxTotals()) {
		t.Fatalf("3.1(a) tax %+v != GSTR-1 TaxTotals %+v", g3.OutwardTax(), g1.TaxTotals())
	}
	// Taxable equality against the GSTR-1 HSN summary (which covers every invoice).
	taxable := decimal.Zero
	for _, h := range g1.HSN.Data {
		taxable = taxable.Add(h.Txval)
	}
	if !g3.T31.OsupDet.Txval.Equal(taxable) {
		t.Fatalf("3.1(a) txval %s != GSTR-1 HSN taxable %s", g3.T31.OsupDet.Txval, taxable)
	}
	// Non-vacuity: the shared figure must not be zero.
	if taxable.IsZero() || g1.TaxTotals().Total().IsZero() {
		t.Fatalf("consistency test is vacuous: taxable=%s tax=%s", taxable, g1.TaxTotals().Total())
	}
	// FP agreement.
	if g3.FP != g1.FP {
		t.Fatalf("FP diverges: 3B %s vs GSTR-1 %s", g3.FP, g1.FP)
	}
	// GSTN rule: 3.2 <= 3.1(a).
	if err := g3.Validate(); err != nil {
		t.Fatalf("generated 3B fails its own validation: %v", err)
	}
}

// TestValidate3B_RejectsInterSupExceeding31a proves Validate is a real gate: a
// hand-built 3B whose table 3.2 exceeds 3.1(a) must be rejected. Pure, no DB.
func TestValidate3B_RejectsInterSupExceeding31a(t *testing.T) {
	good := gstr.GSTR3B{
		T31: gstr.Table31{OsupDet: gstr.Table31Row{
			Txval: dec("200000"), Iamt: dec("36000"), Camt: decimal.Zero, Samt: decimal.Zero}},
		T32: []gstr.Table32Row{{Pos: "27", Txval: dec("200000"), Iamt: dec("36000")}},
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("3.2 == 3.1(a) must be valid, got %v", err)
	}
	overTxval := good
	overTxval.T32 = []gstr.Table32Row{{Pos: "27", Txval: dec("200001"), Iamt: dec("36000")}}
	if err := overTxval.Validate(); err == nil {
		t.Fatal("expected Validate to reject 3.2 taxable > 3.1(a) taxable")
	}
	overIGST := good
	overIGST.T32 = []gstr.Table32Row{{Pos: "27", Txval: dec("200000"), Iamt: dec("36001")}}
	if err := overIGST.Validate(); err == nil {
		t.Fatal("expected Validate to reject 3.2 IGST > 3.1(a) IGST")
	}
}

// TestGSTR3B_ITCReflectsAlterAndDelete proves the PURCHASE arm of the 3B query
// carries the SAME head-version + is_deleted guard the SALES arm does. Without
// the voucher_head join an ALTERed purchase would be counted twice (every
// version stamps the GST columns) and a DELETEd one would survive — over-
// claiming ITC, the most audit-sensitive number on the return.
func TestGSTR3B_ITCReflectsAlterAndDelete(t *testing.T) {
	pool, ctx, _ := setup(t)
	ms := masters.NewService(pool)
	led := ledger.NewService(pool)

	p := setupPurchaseMasters(t, ms, ctx)
	vID := gstPurchase(t, led, ctx, p, "PU-1", "2025-05-12", "2000.0000", false)

	gen := gstr.NewService(pool)
	o := opts3B(p)

	g, err := gen.Generate3B(ctx, "2025-05-01", "2025-05-31", o)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !g.T4.ItcNet.Camt.Equal(dec("180")) {
		t.Fatalf("initial ITC CGST: want 180, got %s", g.T4.ItcNet.Camt)
	}

	// ALTER the same purchase to 5000 (CGST/SGST 450 each).
	alt := ledger.VoucherInput{
		Class:         ledger.Purchase,
		VoucherTypeID: p.vtID,
		PartyLedgerID: p.supplier,
		VoucherNo:     "PU-1",
		VoucherDate:   "2025-05-12",
		Party:         "supplier",
		CreatedBy:     "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: p.purchases, DrCr: ledger.Debit, Amount: dec("5000")},
			{LedgerID: p.inCGST, DrCr: ledger.Debit, Amount: dec("450")},
			{LedgerID: p.inSGST, DrCr: ledger.Debit, Amount: dec("450")},
			{LedgerID: p.supplier, DrCr: ledger.Credit, Amount: dec("5900")},
		},
		GST: &ledger.GSTParams{
			SupplierStateCode:      supplierState,
			PlaceOfSupplyStateCode: supplierState,
			Lines:                  []compliance.Line{{TaxableValue: dec("5000"), GSTRatePct: dec("18"), HSN: "8471"}},
			CGSTLedgerID:           p.inCGST,
			SGSTLedgerID:           p.inSGST,
			IGSTLedgerID:           p.inIGST,
		},
	}
	if _, err := led.AlterVoucher(ctx, vID, alt); err != nil {
		t.Fatalf("alter: %v", err)
	}

	// THE DECISION LOOKUP MUST BE VERSION-AWARE, and this is where that is
	// proved. Before the alter a reconciliation could legitimately have
	// concluded "this 2000-rupee purchase matched, 180+180 available". The
	// alter moves the taxable value to 5000 — the very field a GSTR-2B match
	// keys on — so that conclusion is not merely stale, it is about a document
	// that no longer exists. Applied blindly it would reverse or avail 180
	// against a voucher that now carries 450, and (I1) would quietly stop
	// holding while every other assertion in this file stayed green.
	//
	// The guard is the Available+Ineligible partition: a decision is only
	// applied when it accounts for EXACTLY the current version's posted tax.
	staleSrc := &stubITCSource{m: map[uuid.UUID]gstr.ITCDecision{
		vID: {
			Bucket:        gstr.BucketOTH,
			Available:     gstr.TaxTotals{CGST: dec("180"), SGST: dec("180"), IGST: dec("0")},
			ReversedOther: gstr.TaxTotals{CGST: dec("180"), SGST: dec("180"), IGST: dec("0")},
			Reason:        "resolved against version 1 (taxable 2000)",
		},
	}}
	stale := o
	stale.ITC = staleSrc
	if _, err := gen.Generate3B(ctx, "2025-05-01", "2025-05-31", stale); err == nil {
		t.Fatal("a decision resolved against the SUPERSEDED version must not be applied: " +
			"Generate3B accepted 180/180 of decisions for a voucher that now posts 450/450")
	} else if !strings.Contains(err.Error(), "PU-1") || !strings.Contains(err.Error(), "superseded") {
		t.Fatalf("the staleness error must name the voucher and say why, got: %v", err)
	}
	// The SAME source, restated against the current version, is accepted — so
	// the check above is a version guard and not a blanket refusal.
	staleSrc.m[vID] = gstr.ITCDecision{
		Bucket:    gstr.BucketOTH,
		Available: gstr.TaxTotals{CGST: dec("450"), SGST: dec("450"), IGST: dec("0")},
	}
	if fresh, err := gen.Generate3B(ctx, "2025-05-01", "2025-05-31", stale); err != nil {
		t.Fatalf("a decision restated against the current version must be accepted: %v", err)
	} else if !fresh.T4.ItcNet.Camt.Equal(dec("450")) {
		t.Fatalf("restated decision: itc_net CGST want 450, got %s", fresh.T4.ItcNet.Camt)
	}

	g, err = gen.Generate3B(ctx, "2025-05-01", "2025-05-31", o)
	if err != nil {
		t.Fatalf("generate after alter: %v", err)
	}
	if !g.T4.ItcNet.Camt.Equal(dec("450")) {
		t.Fatalf("after alter: ITC CGST want 450 (edited), got %s (double-count would be 630)", g.T4.ItcNet.Camt)
	}

	// The POSTING side must carry the same guard. ITCPostingTotals joins
	// voucher_head on current_version_id precisely so the superseded version's
	// input-tax postings are not summed again; without it this reads 630 and
	// Reconcile3B would report a perfectly good set of books as UNBALANCED.
	// (Verified by mutation: dropping current_version_id from ITCPostingTotals
	// leaves every other assertion in this package green.)
	_, recon, err := gen.Reconcile3B(ctx, "2025-05-01", "2025-05-31", o)
	if err != nil {
		t.Fatalf("reconcile3b after alter: %v", err)
	}
	if !recon.ITCPostings.CGST.Equal(dec("450")) {
		t.Fatalf("after alter: ITC POSTINGS CGST want 450, got %s (double-count would be 630)", recon.ITCPostings.CGST)
	}
	if !recon.Balanced() {
		t.Fatalf("after alter: 3B must still reconcile: itc %+v vs postings %+v", recon.ITC, recon.ITCPostings)
	}

	// DELETE: the purchase must vanish from table 4 entirely.
	if _, err := led.DeleteVoucher(ctx, vID, "tester"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	g, err = gen.Generate3B(ctx, "2025-05-01", "2025-05-31", o)
	if err != nil {
		t.Fatalf("generate after delete: %v", err)
	}
	if !g.T4.ItcNet.Camt.IsZero() || len(g.T4.ItcAvl) != 0 {
		t.Fatalf("after delete: want zero ITC, got net %+v avl %+v", g.T4.ItcNet, g.T4.ItcAvl)
	}
	// Posting side must vanish too (the is_deleted guard on ITCPostingTotals).
	if _, recon, err = gen.Reconcile3B(ctx, "2025-05-01", "2025-05-31", o); err != nil {
		t.Fatalf("reconcile3b after delete: %v", err)
	}
	if !recon.ITCPostings.CGST.IsZero() || !recon.ITCPostings.SGST.IsZero() {
		t.Fatalf("after delete: ITC postings must be zero, got %+v", recon.ITCPostings)
	}
}

// stubITCSource is a fixed decision map standing in for the 2A/2B matcher. It
// is deliberately DUMB: every test below states the decisions it wants by hand,
// so what is under test is how build3B APPLIES a decision, never how the
// matcher reached one. (The matcher's own coverage is the corpora/gstr2b
// family and the compliance/itc package.)
type stubITCSource struct {
	m     map[uuid.UUID]gstr.ITCDecision
	calls int
}

func (s *stubITCSource) Decisions(ctx context.Context, from, to string) (map[uuid.UUID]gstr.ITCDecision, error) {
	s.calls++
	return s.m, nil
}

// TestGenerate3B_Table4Reversals is the positive case for the whole slice: a
// reconciliation that reverses part of the ordinary ITC.
//
// It asserts the shape the restated invariants require, and each assertion is
// there because a different plausible implementation gets it wrong:
//
//   - 4(A) is UNCHANGED by the reversal — it is what the books posted. An
//     implementation that netted the reversal into 4(A) instead would give the
//     same 4(C) and break the tie to the trial balance permanently.
//   - 4(C) < sum(4A), by exactly the reversal.
//   - Reconcile3B still reports BALANCED. This is the point of restating
//     Balanced(): a CORRECT reversal must not read as a broken reconciliation,
//     because the obvious way to make that failure go away is to stop
//     reversing.
//   - ITCPostings is UNMOVED at 540 — reconciliation never writes to the books.
func TestGenerate3B_Table4Reversals(t *testing.T) {
	pool, ctx, _ := setup(t)
	ms := masters.NewService(pool)
	led := ledger.NewService(pool)

	p := setupPurchaseMasters(t, ms, ctx)
	plain := gstPurchase(t, led, ctx, p, "PU-1", "2025-05-12", "2000.0000", false) // 180/180
	gstPurchase(t, led, ctx, p, "PU-2", "2025-05-18", "4000.0000", true)           // RCM 360/360

	// The supplier under-reported PU-1 in his GSTR-1: 100 per head of the 180
	// claimed has no counterpart in the 2B, so it is reversed under 4(B)(2).
	src := &stubITCSource{m: map[uuid.UUID]gstr.ITCDecision{
		plain: {
			Bucket:        gstr.BucketOTH,
			Available:     gstr.TaxTotals{CGST: dec("180"), SGST: dec("180"), IGST: dec("0")},
			ReversedOther: gstr.TaxTotals{CGST: dec("100"), SGST: dec("100"), IGST: dec("0")},
			Reason:        "MISMATCHED: books exceed the statement by 100 per head",
		},
	}}
	o := opts3B(p)
	o.ITC = src

	gen := gstr.NewService(pool)
	g, recon, err := gen.Reconcile3B(ctx, "2025-05-01", "2025-05-31", o)
	if err != nil {
		t.Fatalf("reconcile3b: %v", err)
	}
	if src.calls == 0 {
		t.Fatal("the ITCSource was never consulted — the seam is not wired")
	}

	avl := map[string]gstr.Table4Row{}
	for _, r := range g.T4.ItcAvl {
		avl[r.Ty] = r
	}
	if oth := avl["OTH"]; !oth.Camt.Equal(dec("180")) || !oth.Samt.Equal(dec("180")) {
		t.Fatalf("4(A) OTH must stay at what the BOOKS posted (180/180), got %+v", oth)
	}
	if isrc := avl["ISRC"]; !isrc.Camt.Equal(dec("360")) || !isrc.Samt.Equal(dec("360")) {
		t.Fatalf("4(A) ISRC: want 360/360, got %+v", isrc)
	}
	if len(g.T4.ItcRev) != 1 || g.T4.ItcRev[0].Ty != "OTH" {
		t.Fatalf("4(B): want exactly one OTH row, got %+v", g.T4.ItcRev)
	}
	if r := g.T4.ItcRev[0]; !r.Camt.Equal(dec("100")) || !r.Samt.Equal(dec("100")) {
		t.Fatalf("4(B)(2): want 100/100, got %+v", r)
	}
	if !g.T4.ItcNet.Camt.Equal(dec("440")) || !g.T4.ItcNet.Samt.Equal(dec("440")) {
		t.Fatalf("4(C): want 540 - 100 = 440 per head, got %+v", g.T4.ItcNet)
	}
	// The whole reason 4(C) exists as a separate number: it is STRICTLY LESS
	// than what was availed. If these were equal the reversal did nothing.
	if !g.T4.ItcNet.Camt.LessThan(recon.ITCAvailable.CGST) {
		t.Fatalf("itc_net CGST %s must be less than sum(4A) %s once a reversal exists",
			g.T4.ItcNet.Camt, recon.ITCAvailable.CGST)
	}

	// The books are UNTOUCHED: reconciliation never posts.
	if !recon.ITCPostings.CGST.Equal(dec("540")) || !recon.ITCPostings.SGST.Equal(dec("540")) {
		t.Fatalf("input-tax postings must be unmoved at 540/540, got %+v", recon.ITCPostings)
	}
	if !recon.Balanced() {
		t.Fatalf("a CORRECT reversal must still reconcile:\n itc(4C) %+v postings %+v\n 4A %+v 4B %+v 4D %+v\n booksTie=%v internalTie=%v",
			recon.ITC, recon.ITCPostings, recon.ITCAvailable, recon.ITCReversed, recon.ITCIneligible,
			recon.BalancedBooks(), recon.BalancedInternal())
	}
	if !recon.BalancedBooks() {
		t.Fatal("(I1) sum(4A) + sum(4D) == input-tax postings must hold")
	}

	// The return must SAY the reversal has not been posted to the books.
	if !containsStr(g.Disclosures, "reversals-not-posted-to-books") {
		t.Fatalf("a return carrying a 4(B) reversal must disclose that the book entry is the filer's to pass: %v", g.Disclosures)
	}
	if containsStr(g.Disclosures, "no-inward-statement") {
		t.Fatalf("no-inward-statement must not be emitted when an ITCSource was supplied: %v", g.Disclosures)
	}
	raw, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, k := range []string{"itc_rev", "disclosures"} {
		if !strings.Contains(string(raw), `"`+k+`"`) {
			t.Fatalf("json must carry %q once the seam is engaged: %s", k, raw)
		}
	}
}

// TestGenerate3B_Table4D_IsInformationalNotSubtracted pins the
// Notification-14/2022 shape of table 4: 4(D) is "Other Details" and is NOT
// subtracted from 4(C).
//
// The setup is the case that makes both halves of that visible: PU-1's credit
// is time-barred under section 16(4), so it is EXCLUDED from 4(A) entirely
// (never available — GSTN marks such a document itcavl="N" and keeps it out of
// the ITC-available section) and reported in 4(D)(2). PU-2 is ordinary.
//
//	4(A) = 450 (PU-2 only)      4(D) = 180 (PU-1)      postings = 630
//	4(C) = 4(A) - 4(B) = 450.   NOT 450 - 180 = 270.
//
// Subtracting 4(D) as well double-counts the haircut: the rupee is already
// missing from 4(A), so taking it out again denies the filer credit twice.
// (I1) still holds exactly, which is what excluding rather than reversing buys.
func TestGenerate3B_Table4D_IsInformationalNotSubtracted(t *testing.T) {
	pool, ctx, _ := setup(t)
	ms := masters.NewService(pool)
	led := ledger.NewService(pool)

	p := setupPurchaseMasters(t, ms, ctx)
	barred := gstPurchase(t, led, ctx, p, "PU-1", "2025-05-12", "2000.0000", false) // 180/180
	good := gstPurchase(t, led, ctx, p, "PU-2", "2025-05-18", "5000.0000", false)   // 450/450

	src := &stubITCSource{m: map[uuid.UUID]gstr.ITCDecision{
		barred: {
			Bucket:     gstr.BucketOTH,
			Available:  gstr.TaxTotals{CGST: dec("0"), SGST: dec("0"), IGST: dec("0")},
			Ineligible: gstr.TaxTotals{CGST: dec("180"), SGST: dec("180"), IGST: dec("0")},
			Reason:     "section 16(4): the claim window closed",
		},
		good: {
			Bucket:    gstr.BucketOTH,
			Available: gstr.TaxTotals{CGST: dec("450"), SGST: dec("450"), IGST: dec("0")},
		},
	}}
	o := opts3B(p)
	o.ITC = src

	gen := gstr.NewService(pool)
	g, recon, err := gen.Reconcile3B(ctx, "2025-05-01", "2025-05-31", o)
	if err != nil {
		t.Fatalf("reconcile3b failed — 4(D) must NOT be subtracted from 4(C); subtracting it "+
			"as well as excluding the credit from 4(A) DOUBLE-COUNTS the haircut and breaks "+
			"the 4C == 4A - 4B identity: %v", err)
	}

	if !recon.ITCAvailable.CGST.Equal(dec("450")) {
		t.Fatalf("4(A) must exclude the time-barred credit entirely: want 450, got %s", recon.ITCAvailable.CGST)
	}
	if len(g.T4.ItcInelg) != 1 || g.T4.ItcInelg[0].Ty != "OTH" {
		t.Fatalf("4(D) must carry exactly one OTH row (4(D)(1) is never emitted), got %+v", g.T4.ItcInelg)
	}
	if !g.T4.ItcInelg[0].Camt.Equal(dec("180")) {
		t.Fatalf("4(D)(2): want 180, got %s", g.T4.ItcInelg[0].Camt)
	}
	if !g.T4.ItcNet.Camt.Equal(dec("450")) {
		t.Fatalf("4(C) must be 4(A) - 4(B) = 450, got %s. 270 here means 4(D) was subtracted "+
			"as well, which DOUBLE-COUNTS the haircut: the 180 is already absent from 4(A).",
			g.T4.ItcNet.Camt)
	}
	// (I1): every posted rupee landed in exactly one of 4(A) and 4(D).
	if !recon.ITCPostings.CGST.Equal(dec("630")) {
		t.Fatalf("input-tax postings: want 630, got %s", recon.ITCPostings.CGST)
	}
	if !recon.BalancedBooks() {
		t.Fatalf("(I1) broken: postings %+v != 4A %+v + 4D %+v",
			recon.ITCPostings, recon.ITCAvailable, recon.ITCIneligible)
	}
	if !recon.Balanced() {
		t.Fatalf("an excluded-and-reported credit must still reconcile: %+v", recon)
	}
}

// TestGenerate3B_RCMIsNeverReversedForBeingUnmatched protects the carve-out
// that a naive matcher destroys.
//
// Reverse-charge ITC is SELF-ASSESSED: the recipient raises the liability and
// takes the credit, so there is no supplier document in a GSTR-2B to match
// against, and GSTN excludes RCM from IMS outright (IMS FAQ 8 and 18). A
// matcher that treats "no statement row" as "reverse it" therefore reverses
// ONE HUNDRED PER CENT of every taxpayer's RCM credit — a large, confident,
// entirely wrong number.
//
// The test states that as a trap: the stub hands build3B a decision for the RCM
// voucher that reverses the whole of it. The decision must be IGNORED, because
// the reverse-charge arm never reaches the lookup. Delete the `continue` from
// that arm and this test fails on three assertions at once.
func TestGenerate3B_RCMIsNeverReversedForBeingUnmatched(t *testing.T) {
	pool, ctx, _ := setup(t)
	ms := masters.NewService(pool)
	led := ledger.NewService(pool)

	p := setupPurchaseMasters(t, ms, ctx)
	plain := gstPurchase(t, led, ctx, p, "PU-1", "2025-05-12", "2000.0000", false)
	rcm := gstPurchase(t, led, ctx, p, "PU-2", "2025-05-18", "4000.0000", true)

	src := &stubITCSource{m: map[uuid.UUID]gstr.ITCDecision{
		plain: {
			Bucket:    gstr.BucketOTH,
			Available: gstr.TaxTotals{CGST: dec("180"), SGST: dec("180"), IGST: dec("0")},
		},
		// THE TRAP: "this RCM purchase is not in the 2B, reverse all of it."
		rcm: {
			Bucket:        gstr.BucketOTH,
			Available:     gstr.TaxTotals{CGST: dec("360"), SGST: dec("360"), IGST: dec("0")},
			ReversedOther: gstr.TaxTotals{CGST: dec("360"), SGST: dec("360"), IGST: dec("0")},
			Reason:        "MISSING_IN_2B (which an RCM document always is)",
		},
	}}
	o := opts3B(p)
	o.ITC = src

	gen := gstr.NewService(pool)
	g, recon, err := gen.Reconcile3B(ctx, "2025-05-01", "2025-05-31", o)
	if err != nil {
		t.Fatalf("reconcile3b: %v", err)
	}

	avl := map[string]gstr.Table4Row{}
	for _, r := range g.T4.ItcAvl {
		avl[r.Ty] = r
	}
	if isrc := avl["ISRC"]; !isrc.Camt.Equal(dec("360")) || !isrc.Samt.Equal(dec("360")) {
		t.Fatalf("RCM credit must stay whole in ISRC (360/360), got %+v — the RCM carve-out is gone", isrc)
	}
	if len(g.T4.ItcRev) != 0 {
		t.Fatalf("NOTHING may be reversed here: RCM is outside IMS and outside 2B matching, "+
			"so 'absent from the statement' is not evidence of anything. Got %+v", g.T4.ItcRev)
	}
	if !g.T4.ItcNet.Camt.Equal(dec("540")) || !g.T4.ItcNet.Samt.Equal(dec("540")) {
		t.Fatalf("4(C): want the full 540/540, got %+v (360 short means RCM was reversed)", g.T4.ItcNet)
	}
	if !recon.Balanced() {
		t.Fatalf("reconciliation broke: %+v", recon)
	}
}

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// TestGenerate3B_RejectsBadDates mirrors TestGenerate_RejectsBadDates.
func TestGenerate3B_RejectsBadDates(t *testing.T) {
	pool, ctx, _ := setup(t)
	gen := gstr.NewService(pool)
	if _, err := gen.Generate3B(ctx, "not-a-date", "2025-05-31", gstr.Options3B{}); err == nil {
		t.Fatal("expected error for bad from date")
	}
	if _, err := gen.Generate3B(ctx, "2025-05-01", "nope", gstr.Options3B{}); err == nil {
		t.Fatal("expected error for bad to date")
	}
	if _, err := gen.Generate3B(ctx, "2025-05-31", "2025-05-01", gstr.Options3B{}); err == nil {
		t.Fatal("expected error for end before start")
	}
}

// TestGenerate3B_NoCompanyGuard mirrors TestGenerate_NoCompanyGuard: the tenant
// guard rejects a call with no company in context, before any DB round-trip.
func TestGenerate3B_NoCompanyGuard(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	pool, err := db.New(context.Background(), appDSN)
	if err != nil {
		t.Skipf("app pool unavailable: %v", err)
	}
	defer pool.Close()
	gen := gstr.NewService(pool)
	if _, err := gen.Generate3B(context.Background(), "2025-05-01", "2025-05-31", gstr.Options3B{}); err != db.ErrNoCompany {
		t.Fatalf("want ErrNoCompany, got %v", err)
	}
	if _, err := gen.ITCPostingTotals(context.Background(), "2025-05-01", "2025-05-31", gstr.Options3B{}); err != db.ErrNoCompany {
		t.Fatalf("ITCPostingTotals: want ErrNoCompany, got %v", err)
	}
}
