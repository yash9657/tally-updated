package gstr_test

// gstr9_integration_test.go is the DATABASE half of the GSTR-9 proof. The pure
// half (gstr9_test.go, in package gstr) exercises the arithmetic and the honesty
// rules on hand-built structs; nothing there touches a voucher. This file posts
// real vouchers through the real ledger and asks the real service, because the
// property that matters most — that three independently-computed views of one
// year land on one number — cannot be demonstrated on fixtures the test wrote
// itself.
//
// Package-scope helpers declared in gstr1_integration_test.go and
// gstr3b_integration_test.go are reused, NOT redeclared: adminDSN/appDSN,
// supplierState, setup, dec, gstSale, setupPurchaseMasters, gstPurchase,
// seedSales and opts3B.
//
// ---------------------------------------------------------------------------
// ⭐ HOW A DIVERGENCE IS MANUFACTURED, AND WHY THIS ONE IS REAL
// ---------------------------------------------------------------------------
// All three sources read the same immutable voucher log, so a well-kept year
// reconciles by construction and a test that only ever posts well-formed
// invoices would prove nothing about the detector.
//
// The break used below is not synthetic. GSTR-1's invoice query carries
// `AND vv.taxable_value IS NOT NULL` (gstr1.go), and Generate3B's outward query
// reads the same denormalised GST columns — so a SALES voucher keyed WITHOUT GST
// detail is invisible to both returns. PostingTaxTotals has no such filter: it
// sums postings on the named output-tax ledgers for any SALES-class voucher. So
// a tax adjustment keyed on a sales voucher with no GST block lands in the BOOKS
// and in neither RETURN.
//
// That is a real bookkeeping mistake with a real consequence — the trial balance
// and the filed return disagree about what was charged — and it is exactly the
// class of error an annual reconciliation exists to find. It is also the only
// break available that needs no fixture the product itself would refuse to post.

import (
	"context"
	"strings"
	"testing"

	"github.com/khata/cored/internal/compliance/gstr"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/platform/db"
)

// fy2025 is the financial year every test below asks for. Its months are the
// ones seedSales already posts into (May 2025), so the grid is never empty.
const (
	fy2025From = "2025-04-01"
	fy2025To   = "2026-03-31"
)

// untaxedSalesAdjustment posts a SALES-class voucher with NO GST block that moves
// the OUTPUT CGST ledger. See the file header: it is invisible to both returns
// and visible to the books, which is the whole point.
//
// `credit` chooses the direction: true adds to the posted output tax (the books
// say MORE was charged than the returns declare), false takes it away.
//
// ⛔ THE PARTY LEG IS ALWAYS A DEBIT, IN BOTH DIRECTIONS, AND THAT IS NOT A
// STYLISTIC CHOICE. ledger.PostVoucher REFUSES a Sales voucher whose party
// posting is a credit, with a message that says why: it would turn a receivable
// into a payable and it would BALANCE, so no total would ever disagree with it.
// The direction is therefore carried by the SALES leg instead — the party is
// billed 10,000 either way, and September's voucher credits Sales by 15,000
// against a 5,000 DEBIT to output tax.
//
// That shape is exactly the real keying error this fixture stands for: an
// operator entering the tax by hand on the postings instead of through the GST
// block, and getting the side wrong on one of them.
func untaxedSalesAdjustment(t *testing.T, led *ledger.Service, ctx context.Context,
	p purchaseLedgers, no, date, amount string, credit bool) {
	t.Helper()

	amt := dec(amount)
	party := dec("10000.0000")

	postings := []ledger.PostingInput{
		{LedgerID: p.b2bParty, DrCr: ledger.Debit, Amount: party},
	}
	if credit {
		// A sale of `party`, of which `amt` was hand-keyed as output tax.
		postings = append(postings,
			ledger.PostingInput{LedgerID: p.salesLedger, DrCr: ledger.Credit, Amount: party.Sub(amt)},
			ledger.PostingInput{LedgerID: p.outCGST, DrCr: ledger.Credit, Amount: amt},
		)
	} else {
		// The same sale with the tax leg keyed on the WRONG SIDE: output tax
		// debited, so the books now carry LESS output tax than the returns.
		postings = append(postings,
			ledger.PostingInput{LedgerID: p.outCGST, DrCr: ledger.Debit, Amount: amt},
			ledger.PostingInput{LedgerID: p.salesLedger, DrCr: ledger.Credit, Amount: party.Add(amt)},
		)
	}

	if _, err := led.PostVoucher(ctx, ledger.VoucherInput{
		Class:         ledger.Sales,
		VoucherTypeID: p.salesVTID,
		PartyLedgerID: p.b2bParty,
		VoucherNo:     no,
		VoucherDate:   date,
		Party:         "adjustment",
		CreatedBy:     "tester",
		Postings:      postings,
		// ⛔ NO GST BLOCK. That is the fixture, not an omission — with one, the
		// voucher would stamp taxable_value and both returns would see it, and
		// this test would have nothing to detect.
	}); err != nil {
		t.Fatalf("post untaxed sales adjustment %s: %v", no, err)
	}
}

// gstr9Fixture builds the masters and the ordinary, well-formed trading for
// FY 2025-26: the four sales seedSales posts in May 2025 and two purchases.
// It returns the ledger service as well as the gstr service: only the offsetting
// test posts anything further, but handing it back is cheaper than a second way
// of recovering the pool from a context.
func gstr9Fixture(t *testing.T) (*gstr.Service, *ledger.Service, context.Context, purchaseLedgers) {
	t.Helper()
	pool, ctx, _ := setup(t)
	ms := masters.NewService(pool)
	led := ledger.NewService(pool)

	p := setupPurchaseMasters(t, ms, ctx)
	seedSales(t, ms, led, ctx, &p)
	gstPurchase(t, led, ctx, p, "P-1", "2025-05-08", "40000.0000", false)
	gstPurchase(t, led, ctx, p, "P-2", "2025-09-12", "25000.0000", false)

	return gstr.NewService(pool), led, ctx, p
}

// ---------------------------------------------------------------------------
// THE CLEAN YEAR — and the control that it is not clean because it is empty
// ---------------------------------------------------------------------------

// TestGSTR9_AWellKeptYearReconciles is the positive case. Three independently
// computed views of the same twelve months must land on one number.
//
// ⚠️ THE CONTROL IS HALF THIS TEST. A year with no vouchers in it reconciles
// perfectly and proves nothing whatever, so the annual turnover and the annual
// input credit are both asserted NON-ZERO before any agreement is believed. A
// reconciliation suite that omits this check is green on an empty database.
func TestGSTR9_AWellKeptYearReconciles(t *testing.T) {
	svc, _, ctx, p := gstr9Fixture(t)

	g, err := svc.Generate9(ctx, fy2025From, fy2025To, opts3B(p))
	if err != nil {
		t.Fatalf("Generate9: %v", err)
	}

	// CONTROL 1: the year is not empty on the outward side.
	outward := gstr.TaxTotals{}
	itc := gstr.TaxTotals{}
	for _, m := range g.Months {
		outward.CGST = outward.CGST.Add(m.R1Tax.CGST)
		outward.SGST = outward.SGST.Add(m.R1Tax.SGST)
		outward.IGST = outward.IGST.Add(m.R1Tax.IGST)
		itc.CGST = itc.CGST.Add(m.BooksITCTax.CGST)
		itc.SGST = itc.SGST.Add(m.BooksITCTax.SGST)
	}
	if outward.Total().IsZero() {
		t.Fatalf("CONTROL: the year declares NO outward tax at all. An empty year reconciles " +
			"perfectly and this whole test would be vacuous")
	}
	// CONTROL 2: and not empty on the ITC side either. The two halves are
	// separate queries against separate ledgers; one can be live while the other
	// is silently dead.
	if itc.Total().IsZero() {
		t.Fatalf("CONTROL: the year posted NO input tax at all, so the ITC leg of this " +
			"reconciliation is asserting agreement between two zeros")
	}

	if !g.Reconciled() {
		t.Errorf("a year of well-formed invoices did not reconcile. r1-vs-3b months=%v, "+
			"3b-vs-books months=%v, r1-vs-books months=%v, itc months=%v, hsn months=%v, "+
			"taxable months=%v",
			g.OutwardR1VsR3B.Months, g.OutwardR3BVsBooks.Months, g.OutwardR1VsBooks.Months,
			g.ITCVsBooks.Months, g.HSNVsR1.Months, g.TaxableR1VsR3B.Months)
	}
	for _, d := range []gstr.Divergence{
		g.OutwardR1VsR3B, g.OutwardR3BVsBooks, g.OutwardR1VsBooks, g.ITCVsBooks, g.HSNVsR1,
	} {
		if !d.Gross.Total().IsZero() {
			t.Errorf("%s vs %s: gross divergence %s over a clean year, months %v",
				d.A, d.B, d.Gross.Total().StringFixed(4), d.Months)
		}
		if d.Offsetting {
			t.Errorf("%s vs %s reports offsetting on a year with no differences at all", d.A, d.B)
		}
	}

	// ⛔ AND IT IS STILL NOT FILING-READY. Reconciling is not the same as being a
	// return anybody may file, and the day those two get conflated is the day
	// this report becomes dangerous.
	if !containsStr(g.Disclosures, "not-filing-ready") {
		t.Errorf("a RECONCILED year dropped `not-filing-ready`: %v. Reconciling means the "+
			"three sources agree; it says nothing about whether eleven of fourteen tables "+
			"could be derived or whether the portal schema was ever checked", g.Disclosures)
	}
	if !containsStr(g.Disclosures, "annual-table-structure-unverified") {
		t.Errorf("`annual-table-structure-unverified` is missing: %v. RESEARCH.md contains no "+
			"GSTR-9 material at all and the portal is geo-fenced from this build, so the "+
			"table numbering has never been checked against anything", g.Disclosures)
	}
	if containsStr(g.Disclosures, "returns-and-books-disagree") {
		t.Errorf("a clean year disclosed `returns-and-books-disagree`: %v", g.Disclosures)
	}
}

// ---------------------------------------------------------------------------
// ⭐ THE ORACLE
// ---------------------------------------------------------------------------

// TestGSTR9_TwoMonthsThatCancelAreNotReportedAsAgreement is the reason this
// report exists, proved against real vouchers rather than hand-built structs.
//
// THE FIXTURE: an untaxed sales adjustment CREDITS Output CGST by 5,000 in May
// 2025 and another DEBITS it by 5,000 in September 2025. Neither is visible to
// GSTR-1 or GSTR-3B (no taxable_value); both are visible to the books. So the
// books are 5,000 HIGH in May and 5,000 LOW in September.
//
//	ANNUAL NET   = 0        — and every total on the return foots
//	ANNUAL GROSS = 10,000   — two months are wrong by 5,000 each
//
// ⛔ IF THIS TEST EVER PASSES BY REPORTING AGREEMENT, the product is certifying a
// year in which two months disagree with the books, in a document whose entire
// purpose is to say so. That is worse than not having the report: a filer who ran
// it and saw "reconciled" has been actively told there is nothing to look at.
func TestGSTR9_TwoMonthsThatCancelAreNotReportedAsAgreement(t *testing.T) {
	svc, led, ctx, p := gstr9Fixture(t)

	untaxedSalesAdjustment(t, led, ctx, p, "ADJ-MAY", "2025-05-28", "5000.0000", true)
	untaxedSalesAdjustment(t, led, ctx, p, "ADJ-SEP", "2025-09-18", "5000.0000", false)

	g, err := svc.Generate9(ctx, fy2025From, fy2025To, opts3B(p))
	if err != nil {
		t.Fatalf("Generate9: %v", err)
	}

	d := g.OutwardR3BVsBooks

	// The fixture's own precondition. If the two adjustments did not cancel, the
	// rest of this test is about something else.
	if !d.Net.CGST.IsZero() {
		t.Fatalf("the fixture did not cancel: net CGST = %s, want 0. May adds 5,000 and "+
			"September takes 5,000 away, so the annual net must be exactly zero for this "+
			"test to be about offsetting at all", d.Net.CGST.StringFixed(4))
	}

	// ⛔ THE ASSERTION THE WHOLE FEATURE RESTS ON.
	if !d.Gross.CGST.Equal(dec("10000.0000")) {
		t.Fatalf("gross CGST = %s, want 10000. The books disagree with GSTR-3B by 5,000 in "+
			"May and by 5,000 in September, in opposite directions. A gross that reads zero "+
			"here means this report tells a filer their year is clean while two months are "+
			"out by 5,000 each — which is the exact defect it was built to find",
			d.Gross.CGST.StringFixed(4))
	}
	if !d.Offsetting {
		t.Errorf("offsetting = false while gross %s and |net| %s differ. The flag is what "+
			"tells the screen to print the months instead of the reassuring zero",
			d.Gross.CGST.StringFixed(4), d.Net.CGST.Abs().StringFixed(4))
	}
	if d.Agrees {
		t.Errorf("agrees = true over a year with two months that disagree with the books")
	}
	if strings.Join(d.Months, ",") != "052025,092025" {
		t.Errorf("disagreeing months = %v, want [052025 092025] — a reader cannot go and "+
			"look at a month the report does not name", d.Months)
	}
	if g.Reconciled() {
		t.Errorf("Reconciled() = true. It is built from the per-month verdicts precisely so " +
			"that an offsetting pair cannot make it true")
	}
	for _, want := range []string{"monthly-differences-offset", "returns-and-books-disagree"} {
		if !containsStr(g.Disclosures, want) {
			t.Errorf("missing disclosure %q: %v", want, g.Disclosures)
		}
	}

	// ⚠️ AND THE TWO RETURNS STILL AGREE WITH EACH OTHER. This is what makes the
	// finding ACTIONABLE rather than merely alarming: GSTR-1 and GSTR-3B are
	// consistent, so the problem is in the books, not in the filing. A report
	// that flagged all three comparisons on a books-only error would send an
	// accountant to re-check two returns that are fine.
	if !g.OutwardR1VsR3B.Agrees {
		t.Errorf("GSTR-1 and GSTR-3B disagree (months %v) on a fixture that only ever "+
			"touched the BOOKS. The two returns read the same GST columns and neither can "+
			"see an adjustment with no taxable_value, so this points at the fixture or at "+
			"one of the two return queries", g.OutwardR1VsR3B.Months)
	}
	// The per-month row must carry the same verdict as the annual divergence, or
	// the grid and the summary would tell a reader two different stories.
	for _, m := range g.Months {
		wantMatch := m.FP != "052025" && m.FP != "092025"
		if m.R3BMatchesBooks != wantMatch {
			t.Errorf("month %s: R3BMatchesBooks = %v, want %v — the grid is the evidence "+
				"behind the divergence and must agree with it", m.FP, m.R3BMatchesBooks, wantMatch)
		}
	}
}

// ---------------------------------------------------------------------------
// THE GRID
// ---------------------------------------------------------------------------

// TestGSTR9_TheGridIsTwelveMonthsIncludingTheEmptyOnes. A month missing from a
// list of twelve is a month nobody checked, and a reader who cannot tell
// "absent" from "nothing happened" has no way to know which they are looking at.
func TestGSTR9_TheGridIsTwelveMonthsIncludingTheEmptyOnes(t *testing.T) {
	svc, _, ctx, p := gstr9Fixture(t)

	g, err := svc.Generate9(ctx, fy2025From, fy2025To, opts3B(p))
	if err != nil {
		t.Fatalf("Generate9: %v", err)
	}
	if len(g.Months) != 12 {
		t.Fatalf("the grid has %d months, want 12", len(g.Months))
	}
	wantFPs := []string{"042025", "052025", "062025", "072025", "082025", "092025",
		"102025", "112025", "122025", "012026", "022026", "032026"}
	var gotFPs []string
	for _, m := range g.Months {
		gotFPs = append(gotFPs, m.FP)
	}
	if strings.Join(gotFPs, ",") != strings.Join(wantFPs, ",") {
		t.Errorf("grid periods = %v, want %v — the FP is how a reader matches a row here "+
			"against the monthly return they actually filed, and April to March is the "+
			"order they filed them in", gotFPs, wantFPs)
	}
	if g.FY != "2025-26" {
		t.Errorf("FY label = %q, want \"2025-26\"", g.FY)
	}
	// An empty month must be present AND zero, not present and stale.
	for _, m := range g.Months {
		if m.FP != "072025" {
			continue
		}
		if !m.R1Tax.Total().IsZero() || !m.BooksOutwardTax.Total().IsZero() {
			t.Errorf("July 2025 has no vouchers but reports r1=%s books=%s — a month "+
				"carrying another month's figures is worse than a missing one",
				m.R1Tax.Total().StringFixed(4), m.BooksOutwardTax.Total().StringFixed(4))
		}
	}
}

// ---------------------------------------------------------------------------
// THE FOURTH LEG
// ---------------------------------------------------------------------------

// TestGSTR9_PerLineHSNDetailFootsToTheInvoiceAggregates drives HSNVsR1, which is
// the only comparison in this report that reads `voucher_gst_line`. Everything
// else comes from the aggregate GST columns on `voucher_version`.
//
// ⚠️ THE CONTROL MATTERS MORE THAN THE ASSERTION. Two empty rollups agree
// trivially, so table 17 must have real buckets in it before the agreement means
// anything — which is also what proves table 17 is DERIVED rather than a stub.
func TestGSTR9_PerLineHSNDetailFootsToTheInvoiceAggregates(t *testing.T) {
	svc, _, ctx, p := gstr9Fixture(t)

	g, err := svc.Generate9(ctx, fy2025From, fy2025To, opts3B(p))
	if err != nil {
		t.Fatalf("Generate9: %v", err)
	}

	var t17 gstr.Table9
	for _, tb := range g.Tables {
		if tb.No == "17" {
			t17 = tb
		}
	}
	// CONTROL: real buckets, or the agreement below is between two empty sets.
	if t17.Status != gstr.TableDerived || len(t17.Rows) == 0 {
		t.Fatalf("CONTROL: table 17 is %q with %d rows. The fixture posts four GST sales at "+
			"18%% with HSN 8471, so the per-line detail is there — an empty table 17 makes "+
			"the HSN leg an agreement between two zeros", t17.Status, len(t17.Rows))
	}
	if !g.HSNVsR1.Agrees {
		t.Errorf("the per-line HSN detail does not foot to the invoice aggregates: gross %s, "+
			"months %v. Migration 0016 says voucher_gst_line sums exactly to the aggregate "+
			"columns on voucher_version; this is the check that it does on real data",
			g.HSNVsR1.Gross.Total().StringFixed(4), g.HSNVsR1.Months)
	}
}

// ---------------------------------------------------------------------------
// THE REFUSALS, AT THE SERVICE SURFACE
// ---------------------------------------------------------------------------

// TestGSTR9_RefusesANonFinancialYearBeforeReadingAnything. The pure test proves
// financialYear() refuses; this proves the SERVICE does, which is a different
// claim — a validation that exists but is never called is the shape of several
// real defects in this repo.
func TestGSTR9_RefusesANonFinancialYearBeforeReadingAnything(t *testing.T) {
	svc, _, ctx, p := gstr9Fixture(t)

	// ⚠️ THE SUGGESTED YEAR IS 2024-25, NOT 2025-26, AND THAT IS CORRECT. The
	// window starts 1 January 2025, and January falls in the financial year that
	// began the PREVIOUS April. Suggesting the year that CONTAINS the caller's own
	// start date is the only rule that cannot send somebody to twelve months they
	// did not ask about.
	if _, err := svc.Generate9(ctx, "2025-01-01", "2025-12-31", opts3B(p)); err == nil {
		t.Fatalf("Generate9 produced a GSTR-9 for a CALENDAR year. There is no such document, " +
			"and one that exists is a statutory-looking artifact for a period the law does " +
			"not recognise")
	} else if !strings.Contains(err.Error(), "2024-04-01") {
		t.Errorf("the refusal does not name the window that would work: %v", err)
	}
}

// TestGSTR9_RefusesWithoutTheTaxLedgerIds at the service surface, for the same
// reason as the test above.
//
// ⛔ WITHOUT THE REFUSAL THIS IS THE WORST OUTPUT THE REPORT COULD PRODUCE: the
// books column would be zero for all twelve months, every period would appear to
// disagree with the returns by exactly the tax charged, and the filer would take
// away twelve findings that are all artefacts of a missing query parameter.
func TestGSTR9_RefusesWithoutTheTaxLedgerIds(t *testing.T) {
	svc, _, ctx, p := gstr9Fixture(t)

	bare := gstr.Options3B{Options: gstr.Options{GSTIN: "29ZZZZZ0000Z1Z5"}}
	if _, err := svc.Generate9(ctx, fy2025From, fy2025To, bare); err == nil {
		t.Fatalf("Generate9 produced a GSTR-9 with no tax ledgers. Every month would show a " +
			"books column of zero and read as twelve findings")
	}

	// CONTROL: the SAME call with the ledgers succeeds, so the refusal above is
	// about the ledgers and not about the fixture being unreadable.
	if _, err := svc.Generate9(ctx, fy2025From, fy2025To, opts3B(p)); err != nil {
		t.Fatalf("CONTROL: the same year WITH the ledger ids failed: %v", err)
	}
}

// TestGSTR9_NoCompanyGuard mirrors TestGenerate_NoCompanyGuard: the tenant GUC
// guard must refuse a context with no company rather than reading across
// tenants.
func TestGSTR9_NoCompanyGuard(t *testing.T) {
	svc, _, _, p := gstr9Fixture(t)

	if _, err := svc.Generate9(context.Background(), fy2025From, fy2025To, opts3B(p)); err == nil {
		t.Fatal("Generate9 read the books with NO company in the context. RLS is scoped by a " +
			"company GUC, so an unscoped read is a cross-tenant read")
	} else if !errorsIsNoCompany(err) {
		t.Errorf("Generate9 with no company returned %v, want db.ErrNoCompany", err)
	}
}

// errorsIsNoCompany keeps the assertion above readable and is the one place the
// sentinel is named.
func errorsIsNoCompany(err error) bool {
	return err != nil && strings.Contains(err.Error(), db.ErrNoCompany.Error())
}
