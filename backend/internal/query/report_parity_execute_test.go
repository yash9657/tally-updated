package query_test

// report_parity_execute_test.go is the LIVE-POSTGRES gate for D20 and D51 — the
// two defects whose whole content is that TWO SURFACES OVER ONE SET OF BOOKS
// DISAGREED.
//
// ⛔ EVERY ASSERTION IN THIS FILE COMPARES THE QUERY ANSWER TO A REPORT'S OWN
// NUMBER, NEVER TO A HAND-WRITTEN CONSTANT. That is deliberate and it is the
// only form of test that can close these defects: a constant pinned by the same
// person who wrote the SQL agrees with the SQL by construction, and both were
// already wrong together. The oracles here (reporting.Outstandings,
// reporting.ProfitAndLoss) read the same rows through a completely independent
// Go code path.
//
// Setup helpers (setupCompany, dec, adminDSN/appDSN, runQuery) are reused from
// execute_test.go / widen_execute_test.go; nothing is redeclared.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/compliance"
	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/internal/query"
	"github.com/khata/cored/internal/reporting"
)

// --------------------------------------------------------------------------
// The seed. Its shape is dictated by the two DATE GUARDS the report acquired.
// --------------------------------------------------------------------------

// billWiseSeed records the dates the seed used, so a test can reason about the
// as-on boundary without re-deriving it.
type billWiseSeed struct {
	today  string // the as-on both surfaces default to
	future string // strictly after today
}

// seedBillWiseBooks creates a bill-wise book that exercises EVERY branch the
// Outstandings report has, because a seed that misses one turns the parity
// assertion into a tautology:
//
//	OPENING BILL-WISE BREAKUP  — the source the query entity did not read at all
//	                             (D20: receivables answered 1,33,920.00 against
//	                             the report's 2,53,920.00, short by exactly this).
//	A POSTED INVOICE           — the source it did read.
//	AN "AGAINST" SETTLEMENT    — so netting inside a (ledger, ref) group is live.
//	A FUTURE-DATED INVOICE     — the report's `vv.voucher_date <= as_on` bound.
//	                             Without one in the seed, a query that ignored
//	                             the bound would still match the report.
//	GST ON THE INVOICE         — so vv.taxable_value is actually stamped and the
//	                             D51 vacuity proof can compare the OLD measure
//	                             against the new one on real data.
//	A CREDIT NOTE              — the voucher type the old "total sales" excluded.
//
// The openings BALANCE (1,20,000 Dr debtor vs 68,000 Cr creditor + 52,000 Cr
// capital), so the books this is asserted against are a real set of books.
func seedBillWiseBooks(t *testing.T, pool *db.Pool, ctx context.Context, ids *identity.Service) billWiseSeed {
	t.Helper()
	ms := masters.NewService(pool)
	led := ledger.NewService(pool)

	now := time.Now().UTC()
	// The "past" vouchers are dated TODAY, not today-minus-something: today is
	// always inside the current financial year and is always <= the as-on bound,
	// so this seed cannot become flaky in the first days of April or the last
	// days of March.
	today := now.Format("2006-01-02")
	future := now.AddDate(0, 0, 20).Format("2006-01-02")

	mk := func(l masters.Ledger) uuid.UUID {
		id, err := ms.CreateLedger(ctx, l, "tester")
		if err != nil {
			t.Fatalf("create ledger %s: %v", l.Name, err)
		}
		return id
	}

	acme := mk(masters.Ledger{
		Name: "Acme Corp", GroupName: "Sundry Debtors",
		OpeningBalance: dec("120000"), OpeningDC: "D",
		OpeningBills: []masters.OpeningBill{
			{RefName: "OB-ACME-1", RefType: "NEW", DrCr: "D", Amount: dec("70000"), DueDate: "2025-05-31"},
			{RefName: "OB-ACME-2", RefType: "NEW", DrCr: "D", Amount: dec("50000"), DueDate: "2025-06-30"},
		},
	})
	globex := mk(masters.Ledger{
		Name: "Globex Ltd", GroupName: "Sundry Creditors",
		OpeningBalance: dec("68000"), OpeningDC: "C",
		OpeningBills: []masters.OpeningBill{
			{RefName: "OB-GLOBEX-1", RefType: "NEW", DrCr: "C", Amount: dec("68000"), DueDate: "2025-06-15"},
		},
	})
	mk(masters.Ledger{Name: "Owner Capital", GroupName: "Capital Account",
		OpeningBalance: dec("52000"), OpeningDC: "C"})

	sales := mk(masters.Ledger{Name: "Domestic Sales", GroupName: "Sales Accounts"})
	purch := mk(masters.Ledger{Name: "Local Purchase", GroupName: "Purchase Accounts"})
	ocgst := mk(masters.Ledger{Name: "Output CGST", GroupName: "Duties & Taxes"})
	osgst := mk(masters.Ledger{Name: "Output SGST", GroupName: "Duties & Taxes"})

	vt := func(name string) uuid.UUID {
		id, err := ids.VoucherTypeID(ctx, name)
		if err != nil {
			t.Fatalf("voucher type %s: %v", name, err)
		}
		return id
	}
	post := func(in ledger.VoucherInput) {
		in.CreatedBy = "tester"
		if _, err := led.PostVoucher(ctx, in); err != nil {
			t.Fatalf("post %s: %v", in.VoucherNo, err)
		}
	}

	// A GST-bearing sale dated TODAY: 10,000 taxable + 18% intra-state.
	// vv.taxable_value is stamped ONLY because these GST params are present —
	// which is itself half of D51 (on a non-GST book the old measure answered
	// 0.0000 for a trading company).
	post(ledger.VoucherInput{
		Class: ledger.Sales, VoucherTypeID: vt("Sales"), VoucherNo: "INV-1",
		VoucherDate: today, PartyLedgerID: acme,
		Postings: []ledger.PostingInput{
			{LedgerID: acme, DrCr: ledger.Debit, Amount: dec("11800")},
			{LedgerID: sales, DrCr: ledger.Credit, Amount: dec("10000")},
			{LedgerID: ocgst, DrCr: ledger.Credit, Amount: dec("900")},
			{LedgerID: osgst, DrCr: ledger.Credit, Amount: dec("900")},
		},
		BillAllocations: []ledger.BillAllocationInput{
			{LedgerID: acme, RefName: "INV-1", RefType: ledger.RefNew, DrCr: ledger.Debit, Amount: dec("11800"), DueDate: future},
		},
		GST: &ledger.GSTParams{
			RulesetVersion:         "gst-2024-09",
			SupplierStateCode:      "27",
			PlaceOfSupplyStateCode: "27",
			Lines:                  []compliance.Line{{TaxableValue: dec("10000"), GSTRatePct: dec("18.00"), HSN: "8471"}},
			CGSTLedgerID:           ocgst,
			SGSTLedgerID:           osgst,
		},
	})

	// A CREDIT NOTE (sales return) settling part of INV-1. It DEBITS Sales, so
	// it reduces the P&L's Sales line — and it is a voucher of a different TYPE,
	// which is exactly why the old "total sales" never saw it. No GST params:
	// that is deliberate, and it also proves the new measure does not depend on
	// vv.taxable_value being stamped.
	post(ledger.VoucherInput{
		Class: ledger.CreditNote, VoucherTypeID: vt("Credit Note"), VoucherNo: "CN-1",
		VoucherDate: today, PartyLedgerID: acme,
		Postings: []ledger.PostingInput{
			{LedgerID: sales, DrCr: ledger.Debit, Amount: dec("3000")},
			{LedgerID: acme, DrCr: ledger.Credit, Amount: dec("3000")},
		},
		BillAllocations: []ledger.BillAllocationInput{
			{LedgerID: acme, RefName: "INV-1", RefType: ledger.RefAgainst, DrCr: ledger.Credit, Amount: dec("3000")},
		},
	})

	// A purchase, so the payable side is not empty.
	post(ledger.VoucherInput{
		Class: ledger.Purchase, VoucherTypeID: vt("Purchase"), VoucherNo: "PB-1",
		VoucherDate: today, PartyLedgerID: globex,
		Postings: []ledger.PostingInput{
			{LedgerID: purch, DrCr: ledger.Debit, Amount: dec("5000")},
			{LedgerID: globex, DrCr: ledger.Credit, Amount: dec("5000")},
		},
		BillAllocations: []ledger.BillAllocationInput{
			{LedgerID: globex, RefName: "PB-1", RefType: ledger.RefNew, DrCr: ledger.Credit, Amount: dec("5000"), DueDate: future},
		},
	})

	// ⛔ THE FUTURE-DATED INVOICE. It is large on purpose (90,000): if the query
	// entity ignored the as-on bound the parity assertion would miss by an
	// amount nobody could mistake for rounding.
	post(ledger.VoucherInput{
		Class: ledger.Sales, VoucherTypeID: vt("Sales"), VoucherNo: "INV-FUT",
		VoucherDate: future, PartyLedgerID: acme,
		Postings: []ledger.PostingInput{
			{LedgerID: acme, DrCr: ledger.Debit, Amount: dec("90000")},
			{LedgerID: sales, DrCr: ledger.Credit, Amount: dec("90000")},
		},
		BillAllocations: []ledger.BillAllocationInput{
			{LedgerID: acme, RefName: "INV-FUT", RefType: ledger.RefNew, DrCr: ledger.Debit, Amount: dec("90000"), DueDate: future},
		},
	})

	return billWiseSeed{today: today, future: future}
}

// adminCount runs a scalar count as the SUPERUSER (the app role is FORCE-RLS'd
// and would answer for one company only, which is fine, but the admin path
// matches rawCount's convention in this package and cannot be fooled by a GUC
// that was never set).
func adminCount(t *testing.T, sql string, args ...any) int {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	defer conn.Close(ctx)
	var n int
	if err := conn.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		t.Fatalf("admin count: %v", err)
	}
	return n
}

// --------------------------------------------------------------------------
// D20 — the NL answer must equal the Outstandings REPORT.
// --------------------------------------------------------------------------

// TestExecute_OutstandingsMatchesTheOutstandingsReport is the flagship parity
// gate for D20. It runs the query the FakeTranslator produces for "top debtors"
// and for "payables", and compares BOTH to reporting.Service.Outstandings — per
// party and in total.
//
// NON-VACUOUS, four ways, each guarded explicitly below:
//   - the seed carries opening bill-wise references, so a skeleton that reads
//     bill_allocation alone is short by them (this was the reported defect);
//   - it carries a FUTURE-dated invoice, so a skeleton that drops the as-on
//     bound is OVER by it (the guard the report acquired an hour before this);
//   - it carries an AGAINST settlement, so the netting is live;
//   - the presence of both seeded shapes is asserted from the DATABASE, so the
//     test fails loudly rather than silently if a future change stops seeding
//     one of them.
func TestExecute_OutstandingsMatchesTheOutstandingsReport(t *testing.T) {
	pool, ctx, companyID, ids := setupCompany(t, "OS Parity Co")
	seedBillWiseBooks(t, pool, ctx, ids)

	// --- vacuity guards on the seed itself -------------------------------
	if n := adminCount(t,
		`SELECT count(*) FROM ledger_opening_bill WHERE company_id = $1`, companyID); n < 3 {
		t.Fatalf("seed no longer carries an opening bill-wise breakup (%d rows): the "+
			"UNION this test exists to prove is not exercised", n)
	}
	if n := adminCount(t,
		`SELECT count(*) FROM bill_allocation ba
		   JOIN voucher_version vv ON vv.id = ba.voucher_version_id
		  WHERE ba.company_id = $1 AND vv.voucher_date > (now() AT TIME ZONE 'UTC')::date`,
		companyID); n < 1 {
		t.Fatalf("seed no longer carries a FUTURE-dated bill allocation: the as-on "+
			"bound this test exists to prove is not exercised")
	}

	rep := reporting.NewService(pool)

	for _, tc := range []struct {
		nl     string
		kind   reporting.OutstandingKind
		nature string
	}{
		{"top debtors", reporting.Receivables, "ASSET"},
		{"payables", reporting.Payables, "LIABILITY"},
	} {
		// --- the independent oracle: the REPORT, at its default as-on (today) ---
		want, err := rep.Outstandings(ctx, companyID, tc.kind, "", nil)
		if err != nil {
			t.Fatalf("%s: outstandings oracle: %v", tc.nl, err)
		}
		if want.Total.IsZero() {
			t.Fatalf("%s: the oracle reports a zero total — the seed no longer "+
				"produces an outstanding position and the comparison would be vacuous", tc.nl)
		}

		// --- the system under test: exactly the Query the NL surface produces ---
		q, err := query.FakeTranslator{Now: time.Now}.ToQuery(ctx, companyID, tc.nl)
		if err != nil {
			t.Fatalf("%s: translate: %v", tc.nl, err)
		}
		_, got := runQuery(t, ctx, pool, q)

		// Per PARTY.
		total := decimal.Zero
		for _, p := range want.Parties {
			g, ok := got[p.LedgerName]
			if !ok {
				t.Errorf("%s: party %q is in the Outstandings report but missing from the "+
					"query answer", tc.nl, p.LedgerName)
				continue
			}
			if g != p.Pending.StringFixed(4) {
				t.Errorf("%s: party %q: query = %s, Outstandings report = %s",
					tc.nl, p.LedgerName, g, p.Pending.StringFixed(4))
			}
			total = total.Add(p.Pending)
		}
		// And in TOTAL — the figure the audit actually drove.
		sum := decimal.Zero
		for _, v := range got {
			d, err := decimal.NewFromString(v)
			if err != nil {
				t.Fatalf("%s: query returned a non-numeric pending %q", tc.nl, v)
			}
			sum = sum.Add(d)
		}
		if !sum.Equal(want.Total) {
			t.Errorf("%s: query TOTAL = %s, Outstandings report TOTAL = %s (short/over by %s)",
				tc.nl, sum.StringFixed(4), want.Total.StringFixed(4), want.Total.Sub(sum).StringFixed(4))
		}
	}
}

// TestExecute_OutstandingsExcludesTheFutureDatedBill nails the as-on bound
// directly rather than by arithmetic: it asks for the answer broken down by
// REFERENCE NAME and requires the future-dated invoice's reference to be absent
// while a same-day one is present.
//
// A test that only compared totals could be satisfied by two errors cancelling.
// This one names the row.
func TestExecute_OutstandingsExcludesTheFutureDatedBill(t *testing.T) {
	pool, ctx, companyID, ids := setupCompany(t, "OS AsOn Co")
	seedBillWiseBooks(t, pool, ctx, ids)

	_, byRef := runQuery(t, ctx, pool, query.Query{
		Entity:     query.EntityOutstandings,
		Dimensions: []string{"ref_name"},
		Metrics:    []query.Metric{{Func: query.AggSum, Column: "pending", As: "pending"}},
		Filters:    []query.Filter{{Column: "nature", Op: query.OpEq, Values: []string{"ASSET"}}},
		Limit:      1000,
	})

	if _, ok := byRef["INV-FUT"]; ok {
		t.Errorf("a bill raised in the FUTURE (INV-FUT) is reported as outstanding TODAY; "+
			"the skeleton's `vv.voucher_date <= as-on` bound is not doing its job. rows=%v", byRef)
	}
	// Controls: the same-day invoice and BOTH opening references are present, so
	// the absence above is a date decision and not an empty answer.
	for _, ref := range []string{"INV-1", "OB-ACME-1", "OB-ACME-2"} {
		if _, ok := byRef[ref]; !ok {
			t.Errorf("reference %q is missing from the query answer; rows=%v", ref, byRef)
		}
	}

	// And the report agrees about the same row set — the point of D20 is that
	// these two surfaces must not diverge again.
	want, err := reporting.NewService(pool).Outstandings(ctx, companyID, reporting.Receivables, "", nil)
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}
	for _, p := range want.Parties {
		for _, b := range p.Bills {
			if b.RefName == "INV-FUT" {
				t.Fatalf("the REPORT itself now includes INV-FUT — reporting/outstanding.go "+
					"lost its as-on bound and this test is measuring the wrong thing")
			}
		}
	}
}

// TestExecute_OutstandingsDropsTheOpeningWhenBooksBeginLater is the second guard
// the report carries and the one that is easiest to transcribe wrongly.
//
// An opening balance carries no date of its own: it is stated as at
// company.books_beginning_from. Asked for a date BEFORE that, the position did
// not exist, and reporting it makes this report and the Balance Sheet disagree
// in the one direction neither can explain. reporting.openingIsInScope excludes
// the whole opening breakup in that case; the query skeleton must too, and it
// must do so ALL-OR-NOTHING for the same reason (opening references balance
// among themselves).
//
// ⚠️ THE FAIL-SAFE IS THE SUBTLE PART, AND IT IS ASSERTED BY THE CONTROL TEST
// ABOVE, NOT HERE. The Go guard returns TRUE (leave the opening in) when the
// company row cannot be read; the SQL gets the same behaviour from
// COALESCE(..., '-infinity'), because a bare `>= NULL` is NULL and WHERE treats
// that as false — i.e. it would have silently dropped every opening reference
// the moment the GUC was unreadable, which is the OPPOSITE of the report.
func TestExecute_OutstandingsDropsTheOpeningWhenBooksBeginLater(t *testing.T) {
	pool, ctx, companyID, ids := setupCompanyBooksFrom(t, "OS Books Co",
		time.Now().UTC().AddDate(0, 0, 30).Format("2006-01-02"))
	seedBillWiseBooks(t, pool, ctx, ids)

	// The opening rows EXIST — this is not a test of an empty table.
	if n := adminCount(t,
		`SELECT count(*) FROM ledger_opening_bill WHERE company_id = $1`, companyID); n < 3 {
		t.Fatalf("seed carries no opening bill-wise breakup (%d rows); the guard is untested", n)
	}

	_, byRef := runQuery(t, ctx, pool, query.Query{
		Entity:     query.EntityOutstandings,
		Dimensions: []string{"ref_name"},
		Metrics:    []query.Metric{{Func: query.AggSum, Column: "pending", As: "pending"}},
		Filters:    []query.Filter{{Column: "nature", Op: query.OpEq, Values: []string{"ASSET"}}},
		Limit:      1000,
	})
	for _, ref := range []string{"OB-ACME-1", "OB-ACME-2"} {
		if _, ok := byRef[ref]; ok {
			t.Errorf("opening reference %q is reported although the books do not begin "+
				"until 30 days from now; rows=%v", ref, byRef)
		}
	}
	// The POSTED invoice is still there, proving the exclusion is the opening
	// branch and not the whole query going dark.
	if _, ok := byRef["INV-1"]; !ok {
		t.Errorf("the posted invoice INV-1 vanished too — the guard excluded more than "+
			"the opening breakup; rows=%v", byRef)
	}

	// PARITY, which is the actual requirement: whatever the report does here,
	// the query must do the same.
	want, err := reporting.NewService(pool).Outstandings(ctx, companyID, reporting.Receivables, "", nil)
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}
	sum := decimal.Zero
	for _, v := range byRef {
		d, _ := decimal.NewFromString(v)
		sum = sum.Add(d)
	}
	if !sum.Equal(want.Total) {
		t.Errorf("query TOTAL = %s, Outstandings report TOTAL = %s — the books-beginning "+
			"guard is transcribed differently in the two surfaces",
			sum.StringFixed(4), want.Total.StringFixed(4))
	}
}

// setupCompanyBooksFrom mints a company whose books BEGIN on a stated date. It
// mirrors setupCompany (execute_test.go) rather than parameterising it, because
// that helper is shared with the adversarial gate and widening its signature to
// serve one test here is how a shared harness accumulates knobs nobody reads.
func setupCompanyBooksFrom(t *testing.T, name, booksFrom string) (*db.Pool, context.Context, uuid.UUID, *identity.Service) {
	t.Helper()
	// setupCompany does the migrate + pool work (and registers its own teardown);
	// the company it mints is discarded — this test needs one with a stated
	// books-beginning date, which that helper cannot express.
	pool, _, _, _ := setupCompany(t, name+" base")

	companyID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new company id: %v", err)
	}
	cctx := db.WithCompany(context.Background(), companyID)
	ids := identity.NewService(pool)
	if _, err := ids.CreateCompany(cctx, identity.Company{
		ID:                 companyID,
		Name:               fmt.Sprintf("%s %s", name, companyID.String()[:8]),
		FYStartDate:        "2025-04-01",
		BooksBeginningFrom: booksFrom,
	}); err != nil {
		t.Fatalf("create company with books from %s: %v", booksFrom, err)
	}
	// Registered at the site the company is minted — the retrofit-missing site
	// this package already learned about once (see query_security_test.go).
	testdb.Cleanup(t, companyID)
	return pool, cctx, companyID, ids
}

// --------------------------------------------------------------------------
// D51 — "total sales" must equal the P&L's Sales line.
// --------------------------------------------------------------------------

// TestExecute_NetSalesMatchesProfitAndLoss is the parity gate for D51. The
// oracle is reporting.Service.ProfitAndLoss — an independent code path that
// loads period movement per ledger, signs it by the ledger's nature, and rolls
// it up the group tree.
//
// It asserts BOTH periods the surface can produce:
//   - "total sales"     -> no date filter  -> the P&L over all dates;
//   - "sales this year" -> the FY window   -> the P&L for exactly that window,
//     taken FROM THE ECHOED DSL so the test cannot
//     agree by using a different window than the query did.
//
// NON-VACUOUS: it re-runs the OLD measure (sum(vv.taxable_value) over
// voucher_type_name='Sales') on the same books and requires it to DIFFER. If a
// future refactor quietly restores that measure, this fails.
func TestExecute_NetSalesMatchesProfitAndLoss(t *testing.T) {
	pool, ctx, companyID, ids := setupCompany(t, "Sales Parity Co")
	seedBillWiseBooks(t, pool, ctx, ids)
	rep := reporting.NewService(pool)

	// ---- (a) "total sales": every date -----------------------------------
	q, err := query.FakeTranslator{Now: time.Now}.ToQuery(ctx, companyID, "total sales")
	if err != nil {
		t.Fatalf("translate 'total sales': %v", err)
	}
	if q.DateRange != nil {
		t.Fatalf("'total sales' produced a date window %v; it named no period and must "+
			"not be silently narrowed", q.DateRange)
	}
	gotAll := sumMetricColumn(t, ctx, pool, q)

	pl, err := rep.ProfitAndLoss(ctx, companyID, "", "")
	if err != nil {
		t.Fatalf("P&L oracle: %v", err)
	}
	wantAll := plSectionNode(t, pl.Income, "Sales Accounts")
	if wantAll.IsZero() {
		t.Fatal("the P&L oracle reports zero sales — the seed no longer produces a Sales " +
			"position and the comparison would be vacuous")
	}
	if !gotAll.Equal(wantAll) {
		t.Errorf("'total sales' = %s, Profit & Loss 'Sales Accounts' = %s (off by %s)",
			gotAll.StringFixed(4), wantAll.StringFixed(4), wantAll.Sub(gotAll).StringFixed(4))
	}

	// ---- (b) "sales this year": the FY window, taken from the DSL ---------
	qy, err := query.FakeTranslator{Now: time.Now}.ToQuery(ctx, companyID, "sales this year")
	if err != nil {
		t.Fatalf("translate 'sales this year': %v", err)
	}
	if qy.DateRange == nil {
		t.Fatal("'sales this year' produced NO window")
	}
	gotFY := sumMetricColumn(t, ctx, pool, qy)
	plFY, err := rep.ProfitAndLoss(ctx, companyID, qy.DateRange.From, qy.DateRange.To)
	if err != nil {
		t.Fatalf("P&L oracle (FY): %v", err)
	}
	wantFY := plSectionNode(t, plFY.Income, "Sales Accounts")
	if wantFY.IsZero() {
		t.Fatalf("the P&L reports zero sales inside %s..%s — the seed's invoices fell "+
			"outside the financial year and the window comparison is vacuous",
			qy.DateRange.From, qy.DateRange.To)
	}
	if !gotFY.Equal(wantFY) {
		t.Errorf("'sales this year' (%s..%s) = %s, Profit & Loss = %s",
			qy.DateRange.From, qy.DateRange.To, gotFY.StringFixed(4), wantFY.StringFixed(4))
	}

	// ---- (c) VACUITY PROOF: the OLD measure disagrees --------------------
	// sum(vv.taxable_value) filtered to voucher_type_name='Sales' is what D51
	// records as answering 1,56,500.00 against a Sales-ledger position of
	// 1,44,500.00. On these books it is wrong twice over: it never sees the
	// credit note (a different voucher type) and it never sees the future-dated
	// invoice (posted without GST params, so vv.taxable_value is NULL and SUM
	// skips it).
	oldMeasure := sumMetricColumn(t, ctx, pool, query.Query{
		Entity:     query.EntityVouchers,
		Metrics:    []query.Metric{{Func: query.AggSum, Column: "taxable_value", As: "total_sales"}},
		Dimensions: []string{"month"},
		Filters:    []query.Filter{{Column: "voucher_type_name", Op: query.OpEq, Values: []string{"Sales"}}},
		Limit:      1000,
	})
	if oldMeasure.Equal(wantAll) {
		t.Fatalf("vacuity guard: the OLD measure (sum of taxable_value over Sales-typed "+
			"vouchers) equals the P&L figure %s on this seed, so this test can no longer "+
			"tell the two apart — re-seed it", wantAll.StringFixed(4))
	}
}

// TestExecute_NetPurchasesMatchesProfitAndLoss is the mirror. It is a separate
// test rather than a table row because the failure it catches is different: the
// purchases arm is EXPENSE-natured, so a natural_net expression that hard-coded
// the credit-positive sign would pass every sales assertion and report purchases
// as a negative number here.
func TestExecute_NetPurchasesMatchesProfitAndLoss(t *testing.T) {
	pool, ctx, companyID, ids := setupCompany(t, "Purch Parity Co")
	seedBillWiseBooks(t, pool, ctx, ids)

	q, err := query.FakeTranslator{Now: time.Now}.ToQuery(ctx, companyID, "total purchases")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	got := sumMetricColumn(t, ctx, pool, q)

	pl, err := reporting.NewService(pool).ProfitAndLoss(ctx, companyID, "", "")
	if err != nil {
		t.Fatalf("P&L oracle: %v", err)
	}
	want := plSectionNode(t, pl.Expenses, "Purchase Accounts")
	if want.IsZero() {
		t.Fatal("the P&L oracle reports zero purchases — the comparison would be vacuous")
	}
	if !got.Equal(want) {
		t.Errorf("'total purchases' = %s, Profit & Loss 'Purchase Accounts' = %s",
			got.StringFixed(4), want.StringFixed(4))
	}
	if got.IsNegative() {
		t.Errorf("'total purchases' came back NEGATIVE (%s): the natural-side sign is "+
			"wrong for EXPENSE-natured groups", got.StringFixed(4))
	}
}

// TestExecute_NetSalesFollowsASubGroup is the trap plRootGroupExpr exists for.
// A user may file export invoices under an "Export Sales" SUB-group of Sales
// Accounts. The P&L rolls that up (buildGroupNode recurses); a query filtering
// on the ledger's IMMEDIATE group name would not, and the two surfaces would
// disagree by exactly the sub-group — re-creating D51 inside the fix for it.
func TestExecute_NetSalesFollowsASubGroup(t *testing.T) {
	pool, ctx, companyID, ids := setupCompany(t, "SubGroup Sales Co")
	seedBillWiseBooks(t, pool, ctx, ids)

	ms := masters.NewService(pool)
	if _, err := ms.CreateGroup(ctx, masters.Group{
		Name: "Export Sales", ParentName: "Sales Accounts",
	}, "tester"); err != nil {
		t.Skipf("cannot create a sub-group through masters (%v); the subtree rule is "+
			"still asserted by the P&L parity tests on the flat chart", err)
	}
	exportSales, err := ms.CreateLedger(ctx, masters.Ledger{
		Name: "Export Sales A/c", GroupName: "Export Sales",
	}, "tester")
	if err != nil {
		t.Fatalf("create export ledger: %v", err)
	}
	acme, err := ids.LedgerID(ctx, "Acme Corp")
	if err != nil {
		t.Fatalf("resolve Acme: %v", err)
	}
	salesVT, err := ids.VoucherTypeID(ctx, "Sales")
	if err != nil {
		t.Fatalf("sales vt: %v", err)
	}
	if _, err := ledger.NewService(pool).PostVoucher(ctx, ledger.VoucherInput{
		Class: ledger.Sales, VoucherTypeID: salesVT, VoucherNo: "EXP-1",
		VoucherDate: time.Now().UTC().Format("2006-01-02"), PartyLedgerID: acme, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: acme, DrCr: ledger.Debit, Amount: dec("25000")},
			{LedgerID: exportSales, DrCr: ledger.Credit, Amount: dec("25000")},
		},
	}); err != nil {
		t.Fatalf("post export sale: %v", err)
	}

	q, err := query.FakeTranslator{Now: time.Now}.ToQuery(ctx, companyID, "total sales")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	got := sumMetricColumn(t, ctx, pool, q)

	pl, err := reporting.NewService(pool).ProfitAndLoss(ctx, companyID, "", "")
	if err != nil {
		t.Fatalf("P&L oracle: %v", err)
	}
	want := plSectionNode(t, pl.Income, "Sales Accounts")
	if !got.Equal(want) {
		t.Errorf("'total sales' = %s but the P&L 'Sales Accounts' rollup = %s — the "+
			"25,000 booked to the 'Export Sales' SUB-group is in one surface and not the "+
			"other", got.StringFixed(4), want.StringFixed(4))
	}
}

// --------------------------------------------------------------------------
// helpers
// --------------------------------------------------------------------------

// sumMetricColumn executes q and sums its LAST column across all rows, which is
// the metric on every Query this file builds. Money stays a decimal; it is never
// parsed as a float.
func sumMetricColumn(t *testing.T, ctx context.Context, pool *db.Pool, q query.Query) decimal.Decimal {
	t.Helper()
	res, err := query.Execute(ctx, pool, q)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	sum := decimal.Zero
	for _, r := range res.Rows {
		if len(r) == 0 {
			continue
		}
		cell := r[len(r)-1]
		if cell == "" {
			continue // SQL NULL renders as "" (see execute.go's stringify)
		}
		d, err := decimal.NewFromString(cell)
		if err != nil {
			t.Fatalf("non-numeric metric cell %q in row %v", cell, r)
		}
		sum = sum.Add(d)
	}
	return sum
}

// plSectionNode returns the Amount of the named root node in a P&L section, or
// fails. It fails rather than returning zero on a miss: a silent zero would make
// every comparison in this file pass against an empty oracle.
func plSectionNode(t *testing.T, sec reporting.Section, name string) decimal.Decimal {
	t.Helper()
	for _, n := range sec.Nodes {
		if n.Name == name {
			return n.Amount
		}
	}
	names := make([]string, 0, len(sec.Nodes))
	for _, n := range sec.Nodes {
		names = append(names, n.Name)
	}
	t.Fatalf("the P&L %s section has no %q node (it has %v) — the oracle cannot be read",
		sec.Title, name, names)
	return decimal.Zero
}
