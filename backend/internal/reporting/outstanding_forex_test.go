package reporting_test

// outstanding_forex_test.go — ⛔ T8 STEP 6. OUTSTANDINGS HAD ONE MONEY COLUMN AND
// EVERY BILL LANDED IN IT, SO A DOLLAR BILL WAS INDISTINGUISHABLE FROM A RUPEE ONE.
//
// ===========================================================================
// WHAT WAS WRONG
// ===========================================================================
// Steps 1–5 gave a bill three stored values (base, foreign, rate) and enforced the
// realised difference on settlement. `Bill` carried ONE `Pending` decimal, so the
// whole of the foreign half was invisible to the one report that exists to answer
// "who owes us what, and how old is it". An exporter with $12,000 outstanding read
// a rupee figure that was correct, ageing that was correct, and could not tell from
// any screen in the product that the debt was in dollars at all — which is the
// figure their exposure is actually measured in.
//
// ===========================================================================
// WHAT THE FIX IS — Q5 OF THE DESIGN MEMO, IN THREE PARTS
// ===========================================================================
//  1. TWO NEW FIELDS, NEVER ONE COLUMN. The base figure stays in `Pending` (it ties
//     to the Balance Sheet); the foreign figure gets `CurrencyCode` +
//     `ForeignPending`, blank/zero on a base bill.
//  2. THE NETTING KEY GAINS THE CURRENCY. `(ledger, ref_name, currency)`, so USD can
//     never be summed with INR under one bill number.
//  3. A ref_name open under TWO currencies is A DATA ERROR, NOT A SUM, and is
//     DISCLOSED — `CurrencyConflict` on every row of the offending reference.
//
// ⚠️ THE GRAND TOTAL, THE PARTY TOTALS AND THE AGEING SLABS STAY BASE-ONLY. Summing
// mixed currencies is meaningless. A design decision, not parity — Q5 says so.
//
// ⚠️ EVERY TEST HERE CARRIES A BASE-CURRENCY CONTROL, because a report that reported
// nothing at all would satisfy an assertion about a foreign column being blank.

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

// ---------------------------------------------------------------------------
// THE FIGURES. ⭐ THEY ARE STEP 5's, DELIBERATELY, AND THERE ARE TWO SETS AT TWO
// DIFFERENT RATES — a report formatter that hard-coded one rate would pass with one.
// ---------------------------------------------------------------------------
const (
	// Bill 1: a $1,000 export invoice. Its stored base is NOT 1000 × the rate
	// (Q2 makes the base authoritative and fixed-4 money cannot hold the product),
	// which is exactly why step 5 differences the VALUES and not the rates.
	fxBill1Base    = "83651.23"
	fxBill1Foreign = "1000.00"
	fxBill1Rate    = "83.6512345678"
	// Settled $200 at 85.2512345678: base cleared 16730.2460 (pro-rata),
	// realised 320.0009 (= round(200 × 85.2512345678, 4) − 16730.2460).
	fxBill1SettleForeign = "200.00"
	fxBill1SettleRate    = "85.2512345678"
	fxBill1SettleBase    = "16730.2460"
	fxBill1Realised      = "320.0009"
	fxBill1PendingBase   = "66920.9840" // 83651.23 − 16730.2460
	fxBill1PendingFgn    = "800.0000"   // 1000 − 200

	// Bill 2: a SECOND currency-bearing bill at a DIFFERENT rate (§6.2 of the
	// handoff: at least two probes must use two different rates).
	fxBill2Base    = "45074.07"
	fxBill2Foreign = "600.00"
	fxBill2Rate    = "75.1234567890"
	// Settled $200 at 79.1234567890: base cleared 15024.6900, realised 800.0014.
	fxBill2SettleForeign = "200.00"
	fxBill2SettleRate    = "79.1234567890"
	fxBill2SettleBase    = "15024.6900"
	fxBill2Realised      = "800.0014"
	fxBill2PendingBase   = "30049.3800" // 45074.07 − 15024.69
	fxBill2PendingFgn    = "400.0000"   // 600 − 200
)

// forexReportEnv is the fixture: one company, one USD currency master, a foreign
// debtor, a BASE debtor as the control, and the ledgers a settlement needs.
type forexReportEnv struct {
	t         *testing.T
	pool      *db.Pool
	ctx       context.Context
	companyID uuid.UUID
	svc       *reporting.Service
	led       *ledger.Service
	ms        *masters.Service

	usd      uuid.UUID
	debtor   uuid.UUID // the FOREIGN party
	rupee    uuid.UUID // the BASE control party
	creditor uuid.UUID
	sales    uuid.UUID
	bank     uuid.UUID
	forexPL  uuid.UUID
}

func forexReportSetup(t *testing.T) *forexReportEnv {
	t.Helper()
	pool, ctx, companyID, _ := setup(t)
	e := &forexReportEnv{
		t: t, pool: pool, ctx: ctx, companyID: companyID,
		svc: reporting.NewService(pool),
		led: ledger.NewService(pool),
		ms:  masters.NewService(pool),
	}
	var err error
	if e.usd, err = e.ms.CreateCurrency(ctx, masters.Currency{
		Code: "USD", Symbol: "$", FormalName: "US Dollar", DecimalPlaces: 2,
	}, "tester"); err != nil {
		t.Fatalf("create the USD master: %v", err)
	}
	// ⚠️ `billwise` IS ON THE PARTIES ONLY. A revenue or bank ledger marked
	// bill-by-bill demands a reference for its OWN leg, and `validateBillWiseCoverage`
	// refuses the voucher — which is correct behaviour and a fixture error here.
	mk := func(name, group string, billwise bool) uuid.UUID {
		id, err := e.ms.CreateLedger(ctx, masters.Ledger{
			Name: name, GroupName: group, MaintainBillByBill: billwise,
		}, "tester")
		if err != nil {
			t.Fatalf("create ledger %s: %v", name, err)
		}
		return id
	}
	e.debtor = mk("Foreign Exports Ltd", "Sundry Debtors", true)
	e.rupee = mk("Domestic Traders", "Sundry Debtors", true)
	e.creditor = mk("Overseas Supplies Inc", "Sundry Creditors", true)
	e.sales = mk("Export Sales", "Sales Accounts", false)
	e.bank = mk("HDFC Bank", "Bank Accounts", false)
	e.forexPL = mk("Forex Gain/Loss", "Indirect Expenses", false)
	if err := e.ms.SetForexSettings(ctx, e.forexPL); err != nil {
		t.Fatalf("choose the forex gain/loss ledger: %v", err)
	}
	return e
}

// sale posts a credit sale opening `ref`. A zero currency id makes it a BASE bill,
// which is how every control in this file is written: one helper, one difference.
func (e *forexReportEnv) sale(no, ref string, party uuid.UUID, base string, cur uuid.UUID, foreign, rate, due string) {
	e.t.Helper()
	alloc := ledger.BillAllocationInput{
		LedgerID: party, RefName: ref, RefType: ledger.RefNew,
		DrCr: ledger.Debit, Amount: dec(base), DueDate: due,
	}
	if cur != uuid.Nil {
		alloc.CurrencyID, alloc.ForeignAmount, alloc.Rate = cur, dec(foreign), dec(rate)
	}
	post := ledger.PostingInput{LedgerID: party, DrCr: ledger.Debit, Amount: dec(base)}
	if cur != uuid.Nil {
		post.CurrencyID, post.ForeignAmount, post.Rate = cur, dec(foreign), dec(rate)
	}
	_, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class: ledger.Sales, PartyLedgerID: party, VoucherNo: no,
		VoucherDate: "2025-05-10", Party: "export", CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			post,
			{LedgerID: e.sales, DrCr: ledger.Credit, Amount: dec(base)},
		},
		BillAllocations: []ledger.BillAllocationInput{alloc},
	})
	if err != nil {
		e.t.Fatalf("post the sale %s: %v", no, err)
	}
}

// receipt settles `foreign` of `ref` at `rate`, clearing `baseCleared` and taking
// `realised` to the chosen forex ledger. A zero currency id settles in BASE.
func (e *forexReportEnv) receipt(no, ref string, party uuid.UUID, cur uuid.UUID, foreign, rate, baseCleared, realised string) {
	e.t.Helper()
	alloc := ledger.BillAllocationInput{
		LedgerID: party, RefName: ref, RefType: ledger.RefAgainst,
		DrCr: ledger.Credit, Amount: dec(baseCleared),
	}
	party0 := ledger.PostingInput{LedgerID: party, DrCr: ledger.Credit, Amount: dec(baseCleared)}
	if cur != uuid.Nil {
		alloc.CurrencyID, alloc.ForeignAmount, alloc.Rate = cur, dec(foreign), dec(rate)
		party0.CurrencyID, party0.ForeignAmount, party0.Rate = cur, dec(foreign), dec(rate)
	}
	bankAmt := dec(baseCleared).Add(dec(realised))
	in := ledger.VoucherInput{
		Class: ledger.Receipt, PartyLedgerID: party, VoucherNo: no,
		VoucherDate: "2025-06-15", Party: "export", CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			party0,
			{LedgerID: e.bank, DrCr: ledger.Debit, Amount: bankAmt},
		},
		BillAllocations: []ledger.BillAllocationInput{alloc},
	}
	if !dec(realised).IsZero() {
		in.Postings = append(in.Postings, ledger.PostingInput{
			LedgerID: e.forexPL, DrCr: ledger.Credit, Amount: dec(realised),
		})
	}
	if _, err := e.led.PostVoucher(e.ctx, in); err != nil {
		e.t.Fatalf("post the receipt %s: %v", no, err)
	}
}

// bill finds one party's one bill by ref name, failing with the whole party's rows
// listed — a report assertion that says only "not found" costs a re-run to diagnose.
func (e *forexReportEnv) bill(out reporting.Outstanding, party, ref string) reporting.Bill {
	e.t.Helper()
	for _, p := range out.Parties {
		if p.LedgerName != party {
			continue
		}
		var found []reporting.Bill
		for _, b := range p.Bills {
			if b.RefName == ref {
				found = append(found, b)
			}
		}
		if len(found) == 1 {
			return found[0]
		}
		e.t.Fatalf("party %q carries %d rows for ref %q, wanted exactly 1; its rows are %s",
			party, len(found), ref, describeBills(p.Bills))
	}
	e.t.Fatalf("party %q is not on the report at all; parties are %v", party, partyNames(out))
	return reporting.Bill{}
}

func (e *forexReportEnv) rows(out reporting.Outstanding, party, ref string) []reporting.Bill {
	e.t.Helper()
	var found []reporting.Bill
	for _, p := range out.Parties {
		if p.LedgerName != party {
			continue
		}
		for _, b := range p.Bills {
			if b.RefName == ref {
				found = append(found, b)
			}
		}
	}
	return found
}

func describeBills(bills []reporting.Bill) string {
	s := ""
	for _, b := range bills {
		code := b.CurrencyCode
		if code == "" {
			code = "(base)"
		}
		s += "\n\t  " + b.RefName + " " + code + " base=" + b.Pending.StringFixed(4) +
			" foreign=" + b.ForeignPending.StringFixed(4)
		if b.CurrencyConflict {
			s += " CONFLICT"
		}
	}
	return s
}

func partyNames(out reporting.Outstanding) []string {
	var n []string
	for _, p := range out.Parties {
		n = append(n, p.LedgerName)
	}
	return n
}

func (e *forexReportEnv) receivables() reporting.Outstanding {
	e.t.Helper()
	out, err := e.svc.Outstandings(e.ctx, e.companyID, reporting.Receivables, "", nil)
	if err != nil {
		e.t.Fatalf("outstandings: %v", err)
	}
	return out
}

// ---------------------------------------------------------------------------
// 1. THE CORE: A FOREIGN BILL REPORTS BOTH FIGURES, AND A BASE BILL REPORTS ONE.
// ---------------------------------------------------------------------------

// TestAForeignBillReportsItsCurrencyAndForeignPendingBesideItsBaseFigure is the
// whole of Q5's first half. ⭐ THE CONTROL IS IN THE SAME ASSERTION SET: a rupee bill
// on a different party must come back with an EMPTY code and a ZERO foreign figure,
// which is what distinguishes "the report reads the currency" from "the report
// stamps USD on everything".
func TestAForeignBillReportsItsCurrencyAndForeignPendingBesideItsBaseFigure(t *testing.T) {
	e := forexReportSetup(t)

	e.sale("EXP-1", "EXP-1", e.debtor, fxBill1Base, e.usd, fxBill1Foreign, fxBill1Rate, "2025-06-10")
	e.receipt("RCT-1", "EXP-1", e.debtor, e.usd,
		fxBill1SettleForeign, fxBill1SettleRate, fxBill1SettleBase, fxBill1Realised)

	// A SECOND foreign bill at a DIFFERENT rate, so a hard-coded rate cannot pass.
	e.sale("EXP-2", "EXP-2", e.debtor, fxBill2Base, e.usd, fxBill2Foreign, fxBill2Rate, "2025-06-20")
	e.receipt("RCT-2", "EXP-2", e.debtor, e.usd,
		fxBill2SettleForeign, fxBill2SettleRate, fxBill2SettleBase, fxBill2Realised)

	// ⭐ THE CONTROL — a rupee bill, partially settled, on a party of the same group.
	e.sale("DOM-1", "DOM-1", e.rupee, "50000.00", uuid.Nil, "", "", "2025-06-30")
	e.receipt("RCT-3", "DOM-1", e.rupee, uuid.Nil, "", "", "20000.00", "0")

	out := e.receivables()

	for _, want := range []struct {
		ref, base, foreign string
	}{
		{"EXP-1", fxBill1PendingBase, fxBill1PendingFgn},
		{"EXP-2", fxBill2PendingBase, fxBill2PendingFgn},
	} {
		got := e.bill(out, "Foreign Exports Ltd", want.ref)
		if got.CurrencyCode != "USD" {
			t.Errorf("bill %s came back with currency code %q, wanted \"USD\" — a foreign "+
				"bill that reports no currency is a rupee bill on the page, and the "+
				"exposure it carries is unreadable", want.ref, got.CurrencyCode)
		}
		if !got.ForeignPending.Equal(dec(want.foreign)) {
			t.Errorf("bill %s foreign pending is %s, wanted %s — this is the figure the "+
				"exposure is measured in and nothing else on the report carries it",
				want.ref, got.ForeignPending.StringFixed(4), want.foreign)
		}
		// ⛔ AND THE BASE FIGURE MUST NOT HAVE MOVED. It ties to the Balance Sheet.
		if !got.Pending.Equal(dec(want.base)) {
			t.Errorf("bill %s BASE pending is %s, wanted %s — the base figure is the one "+
				"that ties to Sundry Debtors and adding a foreign column must not touch it",
				want.ref, got.Pending.StringFixed(4), want.base)
		}
		if got.CurrencyConflict {
			t.Errorf("bill %s is flagged as a currency conflict; it is open in ONE "+
				"currency and settled in the same one, so the flag is a false alarm — "+
				"and a disclosure that cries wolf stops being read", want.ref)
		}
	}

	// ⭐ THE CONTROL, ASSERTED AS HARD AS THE SUBJECT.
	ctl := e.bill(out, "Domestic Traders", "DOM-1")
	if ctl.CurrencyCode != "" {
		t.Errorf("the RUPEE control bill came back with currency code %q, wanted empty — "+
			"a base bill has no currency at all (currency_id IS NULL is the one "+
			"encoding), and a code here means every existing bill in the field just "+
			"acquired a currency it was never keyed in", ctl.CurrencyCode)
	}
	if !ctl.ForeignPending.IsZero() {
		t.Errorf("the RUPEE control bill came back with a foreign pending of %s, wanted "+
			"zero", ctl.ForeignPending.StringFixed(4))
	}
	if !ctl.Pending.Equal(dec("30000.00")) {
		t.Errorf("the RUPEE control bill's pending is %s, wanted 30000.0000 — T8 moved a "+
			"base bill, which is a regression for every book in the field",
			ctl.Pending.StringFixed(4))
	}
}

// TestTheGrandTotalAndTheAgeingSlabsStayBaseOnly pins Q5's explicit design decision.
// ⭐ AND IT PINS THE REPORT'S OWN RECONCILIATION THROUGH THE CHANGE:
// sum(SlabTotals) + OnAccountTotal == Total, which is the identity a reader checks by
// adding the column up.
func TestTheGrandTotalAndTheAgeingSlabsStayBaseOnly(t *testing.T) {
	e := forexReportSetup(t)

	e.sale("EXP-1", "EXP-1", e.debtor, fxBill1Base, e.usd, fxBill1Foreign, fxBill1Rate, "2025-06-10")
	e.sale("DOM-1", "DOM-1", e.rupee, "50000.00", uuid.Nil, "", "", "2025-06-30")

	out := e.receivables()

	// The total is the sum of the two BASE figures. ⛔ NOT 83651.23 + 1000 + 50000,
	// which is what a total that summed a mixed column would produce — a number in no
	// currency at all, that ties to nothing, and that still foots.
	wantTotal := dec(fxBill1Base).Add(dec("50000.00"))
	if !out.Total.Equal(wantTotal) {
		t.Errorf("the grand total is %s, wanted %s (base only) — a total that added a "+
			"foreign column into a rupee total would be a figure in no currency that "+
			"still foots, which is the shape nobody catches",
			out.Total.StringFixed(4), wantTotal.StringFixed(4))
	}

	slabs := decimal.Zero
	for _, st := range out.SlabTotals {
		slabs = slabs.Add(st.Amount)
	}
	if !slabs.Add(out.OnAccountTotal).Equal(out.Total) {
		t.Errorf("sum(slabs) %s + on-account %s != total %s — this identity is the "+
			"report's own reconciliation and step 6 must not break it",
			slabs.StringFixed(4), out.OnAccountTotal.StringFixed(4), out.Total.StringFixed(4))
	}

	// And the party total is base-only for the same reason.
	for _, p := range out.Parties {
		if p.LedgerName == "Foreign Exports Ltd" && !p.Pending.Equal(dec(fxBill1Base)) {
			t.Errorf("the foreign party's total is %s, wanted %s (base only)",
				p.Pending.StringFixed(4), fxBill1Base)
		}
	}
}

// ---------------------------------------------------------------------------
// 2. ⛔⛔ §0.2 OF THE MEMO — THE OPENING BILL, WHICH IS THE HALF THAT GETS FORGOTTEN.
// ---------------------------------------------------------------------------

// TestAForeignOPENINGBillIsReportedForeign is the trap the design memo wrote a whole
// section about. Outstandings nets a UNION of `bill_allocation` AND
// `ledger_opening_bill`; handle only the first and a foreign party's OPENING bill —
// which for a migrated customer IS their entire history — silently becomes a base
// bill. Its rupee figure is real, its foreign column is blank, and NOTHING on the
// report disagrees.
//
// ⚠️ IT ALSO PINS THE WRITE PATH, because there was none: 0080 put the three columns
// on `ledger_opening_bill` and `masters.OpeningBill` never carried them, so a foreign
// opening bill could not be keyed at all. A report branch nothing can populate is
// untestable against the real system, which is why this test needs both halves.
func TestAForeignOPENINGBillIsReportedForeign(t *testing.T) {
	e := forexReportSetup(t)

	// A migrated foreign debtor: $600 outstanding, carried at 45,074.07.
	migrated, err := e.ms.CreateLedger(e.ctx, masters.Ledger{
		Name: "Migrated Exporter", GroupName: "Sundry Debtors",
		MaintainBillByBill: true,
		OpeningBalance:     dec(fxBill2Base), OpeningDC: "D",
		OpeningBills: []masters.OpeningBill{{
			RefName: "OPEN-USD", DrCr: "D", Amount: dec(fxBill2Base),
			DueDate:    "2025-05-01",
			CurrencyID: e.usd, ForeignAmount: dec(fxBill2Foreign), Rate: dec(fxBill2Rate),
		}},
	}, "tester")
	if err != nil {
		t.Fatalf("create the migrated foreign debtor: %v", err)
	}
	_ = migrated

	// ⭐ THE CONTROL — a migrated RUPEE debtor's opening bill, same table, same branch.
	if _, err := e.ms.CreateLedger(e.ctx, masters.Ledger{
		Name: "Migrated Domestic", GroupName: "Sundry Debtors",
		MaintainBillByBill: true,
		OpeningBalance:     dec("12000.00"), OpeningDC: "D",
		OpeningBills: []masters.OpeningBill{{
			RefName: "OPEN-INR", DrCr: "D", Amount: dec("12000.00"), DueDate: "2025-05-01",
		}},
	}, "tester"); err != nil {
		t.Fatalf("create the migrated rupee debtor: %v", err)
	}

	out := e.receivables()

	got := e.bill(out, "Migrated Exporter", "OPEN-USD")
	if got.CurrencyCode != "USD" || !got.ForeignPending.Equal(dec(fxBill2Foreign)) {
		t.Errorf("the foreign OPENING bill came back as %q %s / base %s, wanted USD %s — "+
			"the opening breakup is the second table in the UNION, and a report that "+
			"reads the currency off `bill_allocation` alone turns every migrated "+
			"foreign debtor into a rupee debtor",
			got.CurrencyCode, got.ForeignPending.StringFixed(4),
			got.Pending.StringFixed(4), fxBill2Foreign)
	}
	if !got.Pending.Equal(dec(fxBill2Base)) {
		t.Errorf("the foreign opening bill's base pending is %s, wanted %s",
			got.Pending.StringFixed(4), fxBill2Base)
	}

	ctl := e.bill(out, "Migrated Domestic", "OPEN-INR")
	if ctl.CurrencyCode != "" || !ctl.ForeignPending.IsZero() {
		t.Errorf("the RUPEE opening control came back as %q %s, wanted base/zero",
			ctl.CurrencyCode, ctl.ForeignPending.StringFixed(4))
	}
}

// TestAForeignOPENINGBillSettledByAVoucherNetsInBothColumns proves the two tables
// meet in ONE group: an opening bill and the receipt that settles it must net, in
// the base column AND the foreign column, or the union is two reports.
func TestAForeignOPENINGBillSettledByAVoucherNetsInBothColumns(t *testing.T) {
	e := forexReportSetup(t)

	migrated, err := e.ms.CreateLedger(e.ctx, masters.Ledger{
		Name: "Migrated Exporter", GroupName: "Sundry Debtors",
		MaintainBillByBill: true,
		OpeningBalance:     dec(fxBill2Base), OpeningDC: "D",
		OpeningBills: []masters.OpeningBill{{
			RefName: "OPEN-USD", DrCr: "D", Amount: dec(fxBill2Base), DueDate: "2025-05-01",
			CurrencyID: e.usd, ForeignAmount: dec(fxBill2Foreign), Rate: dec(fxBill2Rate),
		}},
	}, "tester")
	if err != nil {
		t.Fatalf("create the migrated foreign debtor: %v", err)
	}

	e.receipt("RCT-OPEN", "OPEN-USD", migrated, e.usd,
		fxBill2SettleForeign, fxBill2SettleRate, fxBill2SettleBase, fxBill2Realised)

	got := e.bill(e.receivables(), "Migrated Exporter", "OPEN-USD")
	if !got.Pending.Equal(dec(fxBill2PendingBase)) || !got.ForeignPending.Equal(dec(fxBill2PendingFgn)) {
		t.Errorf("the settled foreign opening bill is base %s / %s %s, wanted base %s / "+
			"USD %s — the opening row and the AGAINST row must land in ONE netting "+
			"group or the two halves of the UNION report the bill twice",
			got.Pending.StringFixed(4), got.CurrencyCode,
			got.ForeignPending.StringFixed(4), fxBill2PendingBase, fxBill2PendingFgn)
	}
}

// ---------------------------------------------------------------------------
// 3. ⛔⛔ Q5's SECOND HALF — A REF UNDER TWO CURRENCIES IS DISCLOSED, NEVER SUMMED.
// ---------------------------------------------------------------------------

// TestABaseSettlementOfAForeignBillIsDisclosedRatherThanHidden is the case step 5
// DELIBERATELY did not refuse (L36 item 2: catching it would put the bill lookup on
// every base settlement in the product), on the stated grounds that step 6's report
// would disclose it instead. This is that disclosure.
//
// ⛔⛔ AND WITHOUT THE CURRENCY IN THE NETTING KEY THE BILL DISAPPEARS ALTOGETHER.
// The base rows net to exactly zero, `Pending.IsZero()` skips the row, and a party
// with $1,000 uncollected shows NO open bill at all — a silent clean bill of health,
// which is worse than a wrong number.
func TestABaseSettlementOfAForeignBillIsDisclosedRatherThanHidden(t *testing.T) {
	e := forexReportSetup(t)

	e.sale("EXP-1", "EXP-1", e.debtor, fxBill1Base, e.usd, fxBill1Foreign, fxBill1Rate, "2025-06-10")
	// ⛔ A RUPEE RECEIPT AGAINST A DOLLAR BILL. Step 5 does not refuse it: its
	// aggregator skips base AGAINST rows, so no lookup runs and no rate is
	// differenced. The rupee receivable clears; the dollar exposure does not.
	e.receipt("RCT-BASE", "EXP-1", e.debtor, uuid.Nil, "", "", fxBill1Base, "0")

	out := e.receivables()
	rows := e.rows(out, "Foreign Exports Ltd", "EXP-1")
	if len(rows) != 2 {
		t.Fatalf("ref EXP-1 came back as %d row(s), wanted 2 — one per currency it is "+
			"open under. ⛔ MEASURED WITH THE CURRENCY TAKEN OUT OF THE NETTING KEY: "+
			"ONE row, `base=0.0000 foreign=1000.0000`, currency code EMPTY and no "+
			"conflict flag. The exposure is on the page but nothing says WHICH "+
			"currency the $%s is in or that a rupee receipt was allocated against a "+
			"dollar bill — an operator reads a bill that is settled in rupees and "+
			"un-settled in some unnamed currency. Rows: %s",
			len(rows), fxBill1Foreign, describeBills(rows))
	}

	var usd, base *reporting.Bill
	for i := range rows {
		if rows[i].CurrencyCode == "USD" {
			usd = &rows[i]
		} else {
			base = &rows[i]
		}
	}
	if usd == nil || base == nil {
		t.Fatalf("wanted one USD row and one base row for EXP-1, got %s", describeBills(rows))
	}
	if !usd.ForeignPending.Equal(dec(fxBill1Foreign)) {
		t.Errorf("the USD row of EXP-1 shows %s foreign pending, wanted %s — the dollar "+
			"debt was never settled and this is the figure that says so",
			usd.ForeignPending.StringFixed(4), fxBill1Foreign)
	}
	if !usd.Pending.Add(base.Pending).IsZero() {
		t.Errorf("the two rows of EXP-1 sum to a base pending of %s, wanted zero — the "+
			"rupee receivable WAS cleared and the base column must still tie to the "+
			"Balance Sheet", usd.Pending.Add(base.Pending).StringFixed(4))
	}
	// ⛔ AND BOTH ROWS SAY WHY THEY ARE TWO. Two rows under one bill number with
	// nothing marking them look like two bills a customer happened to name alike.
	for _, r := range rows {
		if !r.CurrencyConflict {
			t.Errorf("the %q row of EXP-1 is not flagged as a currency conflict; a "+
				"ref_name open under two currencies is a data error and not a sum, and "+
				"two unmarked rows under one bill number read as two bills",
				r.CurrencyCode)
		}
	}
}

// TestAConflictFlagIsNotSetOnAnOrdinaryForeignBill is the flag's inverse control. A
// disclosure that fires on the normal case is noise, and noise is how a real
// disclosure stops being read.
func TestAConflictFlagIsNotSetOnAnOrdinaryForeignBill(t *testing.T) {
	e := forexReportSetup(t)
	e.sale("EXP-1", "EXP-1", e.debtor, fxBill1Base, e.usd, fxBill1Foreign, fxBill1Rate, "2025-06-10")
	e.receipt("RCT-1", "EXP-1", e.debtor, e.usd,
		fxBill1SettleForeign, fxBill1SettleRate, fxBill1SettleBase, fxBill1Realised)
	e.sale("DOM-1", "DOM-1", e.rupee, "50000.00", uuid.Nil, "", "", "2025-06-30")

	out := e.receivables()
	for _, p := range out.Parties {
		for _, b := range p.Bills {
			if b.CurrencyConflict {
				t.Errorf("%s / %s is flagged as a currency conflict and nothing on these "+
					"books is open under two currencies: %s", p.LedgerName, b.RefName,
					describeBills([]reporting.Bill{b}))
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 4. ⛔ ON ACCOUNT — THE THIRD ROW TYPE, AND THE DECISION §4.1 ASKED FOR.
// ---------------------------------------------------------------------------

// TestOnAccountIsBaseOnlyAndStaysONEROWPerParty records step 6's answer to "what
// currency does the On Account row show".
//
// ⭐ THE ANSWER IS NONE, AND IT IS A DECISION WITH THREE MEASURED REASONS:
//
//  1. The residue is `the ledger's whole balance − every named reference` — a
//     DIFFERENCE OF SUMS over many vouchers at many rates. There is no single rate it
//     can be labelled with, and computing one would invent the fact the row exists to
//     report the absence of. The memo says the same of ADVANCE and ON_ACCOUNT: "State
//     this rather than computing zero."
//  2. ⛔ SPLITTING THE BUCKET BY CURRENCY GIVES A PARTY TWO "On Account" ROWS THAT
//     EACH LOOK RIGHT — which is, word for word, the failure
//     TestOnAccountRefNameAgreesAcrossTheWriteAndReadSides exists to prevent, reached
//     from the other direction: an explicit ON_ACCOUNT allocation carrying a currency
//     would land in a different group from the derived residue that cancels it.
//  3. The identity sum(SlabTotals) + OnAccountTotal == Total is a BASE identity. The
//     base amounts are untouched, so it holds by construction rather than by care.
//
// ⚠️ ADVANCE IS NOT COLLAPSED, and the distinction is the point: an ADVANCE is a
// NAMED reference and keeps its currency; On Account is the ABSENCE of one.
func TestOnAccountIsBaseOnlyAndStaysONEROWPerParty(t *testing.T) {
	e := forexReportSetup(t)

	// ⛔ THE PARTY IS **NOT** MAINTAINED BILL-BY-BILL, WHICH IS WHAT MAKES THE TWO
	// SOURCES OF On Account BOTH REACHABLE AT ONCE. On a bill-by-bill party the
	// residue always cancels to exactly zero (D106's write-side half refuses an
	// unreferenced leg), so only an explicit allocation could arrive and the split
	// could not be observed. Tally reports exactly this kind of party as On Account.
	walkin, err := e.ms.CreateLedger(e.ctx, masters.Ledger{
		Name: "Walk-In Exports", GroupName: "Sundry Debtors",
	}, "tester")
	if err != nil {
		t.Fatalf("create the un-tracked party: %v", err)
	}

	// (a) An EXPLICIT ON_ACCOUNT allocation carrying a currency — source 1, USD.
	if _, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class: ledger.Receipt, PartyLedgerID: walkin, VoucherNo: "RCT-OA-USD",
		VoucherDate: "2025-06-01", Party: "export", CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{
				LedgerID: walkin, DrCr: ledger.Credit, Amount: dec("8365.12"),
				CurrencyID: e.usd, ForeignAmount: dec("100.00"), Rate: dec(fxBill1Rate),
			},
			{LedgerID: e.bank, DrCr: ledger.Debit, Amount: dec("8365.12")},
		},
		BillAllocations: []ledger.BillAllocationInput{{
			LedgerID: walkin, RefName: reporting.OnAccountRefName,
			RefType: ledger.RefOnAccount, DrCr: ledger.Credit, Amount: dec("8365.12"),
			CurrencyID: e.usd, ForeignAmount: dec("100.00"), Rate: dec(fxBill1Rate),
		}},
	}); err != nil {
		t.Fatalf("post the explicit foreign ON_ACCOUNT receipt: %v", err)
	}

	// (b) An UNREFERENCED rupee receipt — source 3, the DERIVED residue, no currency.
	if _, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class: ledger.Receipt, PartyLedgerID: walkin, VoucherNo: "RCT-OA-INR",
		VoucherDate: "2025-06-02", Party: "export", CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: walkin, DrCr: ledger.Credit, Amount: dec("5000.00")},
			{LedgerID: e.bank, DrCr: ledger.Debit, Amount: dec("5000.00")},
		},
	}); err != nil {
		t.Fatalf("post the unreferenced rupee receipt: %v", err)
	}

	out := e.receivables()
	oa := e.rows(out, "Walk-In Exports", reporting.OnAccountRefName)
	if len(oa) != 1 {
		t.Fatalf("the party carries %d On Account rows, wanted exactly 1.\n⛔ THE TWO "+
			"SOURCES OF On Account ARE KEYED ON DIFFERENT TABLES: an explicit "+
			"ON_ACCOUNT allocation arrives with a currency and the derived residue "+
			"arrives with none, so putting the currency in this bucket's netting key "+
			"gives a party two rows that EACH LOOK RIGHT and that STILL FOOT to the "+
			"same total — nothing on the page reveals it. Rows: %s",
			len(oa), describeBills(oa))
	}
	if oa[0].CurrencyCode != "" || !oa[0].ForeignPending.IsZero() {
		t.Errorf("the On Account row reports %q %s; it must be BASE-ONLY — the residue "+
			"is a difference of sums over many rates and has no single currency, and "+
			"stamping one on it invents the fact the row exists to report the absence of",
			oa[0].CurrencyCode, oa[0].ForeignPending.StringFixed(4))
	}
	// ⭐ THE FREE ORACLE: the one row must be the party's WHOLE balance, because
	// neither receipt named a bill. 8365.12 + 5000.00, credit, so negative on a
	// receivable.
	if !oa[0].Pending.Equal(dec("-13365.12")) {
		t.Errorf("the On Account residue is %s, wanted -13365.1200 — the two receipts "+
			"are the whole of it, and a bucket that reported only one of them would "+
			"still look like a plausible figure", oa[0].Pending.StringFixed(4))
	}
	if oa[0].CurrencyConflict {
		t.Error("the On Account row is flagged as a currency conflict; it is deliberately " +
			"currency-collapsed, so it cannot be in conflict with itself")
	}

	slabs := decimal.Zero
	for _, st := range out.SlabTotals {
		slabs = slabs.Add(st.Amount)
	}
	if !slabs.Add(out.OnAccountTotal).Equal(out.Total) {
		t.Errorf("sum(slabs) %s + on-account %s != total %s", slabs.StringFixed(4),
			out.OnAccountTotal.StringFixed(4), out.Total.StringFixed(4))
	}
}

// TestAForeignADVANCEKeepsItsCurrency is the inverse control for the collapse above,
// and it is the line the memo draws: an ADVANCE is a NAMED reference with one rate
// on one document, so it keeps its currency and its foreign figure. `On Account` is
// the ABSENCE of a reference. A collapse written as "anything that is not a bill" —
// the obvious simplification — would take the advance's currency with it.
func TestAForeignADVANCEKeepsItsCurrency(t *testing.T) {
	e := forexReportSetup(t)

	if _, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class: ledger.Receipt, PartyLedgerID: e.debtor, VoucherNo: "RCT-ADV",
		VoucherDate: "2025-06-01", Party: "export", CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{
				LedgerID: e.debtor, DrCr: ledger.Credit, Amount: dec("8365.12"),
				CurrencyID: e.usd, ForeignAmount: dec("100.00"), Rate: dec(fxBill1Rate),
			},
			{LedgerID: e.bank, DrCr: ledger.Debit, Amount: dec("8365.12")},
		},
		BillAllocations: []ledger.BillAllocationInput{{
			LedgerID: e.debtor, RefName: "ADV-1", RefType: ledger.RefAdvance,
			DrCr: ledger.Credit, Amount: dec("8365.12"),
			CurrencyID: e.usd, ForeignAmount: dec("100.00"), Rate: dec(fxBill1Rate),
		}},
	}); err != nil {
		t.Fatalf("post the foreign advance: %v", err)
	}

	got := e.bill(e.receivables(), "Foreign Exports Ltd", "ADV-1")
	if got.CurrencyCode != "USD" || !got.ForeignPending.Equal(dec("-100.00")) {
		t.Errorf("the foreign ADVANCE reports %q %s, wanted USD -100.0000 — an advance "+
			"is a named reference with one rate on one document and keeps its "+
			"currency; only the On Account bucket is collapsed",
			got.CurrencyCode, got.ForeignPending.StringFixed(4))
	}
}

// ---------------------------------------------------------------------------
// 5. ⛔ A FOREIGN FIGURE IS NEVER HIDDEN BEHIND A ZERO RUPEE FIGURE.
// ---------------------------------------------------------------------------

// TestABillWhoseBaseNetsToZeroButWhoseForeignDoesNotIsStillReported pins the
// zero-skip. `Pending.IsZero()` was the whole test for "settled", and once a row
// carries two figures that is no longer the same question: a bill with nothing left
// in rupees and $400 left in dollars is exactly the row an exporter needs to see, and
// the base-only skip drops it.
func TestABillWhoseBaseNetsToZeroButWhoseForeignDoesNotIsStillReported(t *testing.T) {
	e := forexReportSetup(t)

	// A mis-keyed migration: the foreign figure was carried in and the rupee figure
	// was not. Legal at the schema level (amount >= 0, the triple is complete) and
	// exactly the shape an importer produces from a partial export file.
	if _, err := e.ms.CreateLedger(e.ctx, masters.Ledger{
		Name: "Half-Keyed Exporter", GroupName: "Sundry Debtors",
		MaintainBillByBill: true,
		OpeningBalance:     decimal.Zero, OpeningDC: "D",
		OpeningBills: []masters.OpeningBill{{
			RefName: "OPEN-ZERO", DrCr: "D", Amount: decimal.Zero,
			CurrencyID: e.usd, ForeignAmount: dec("400.00"), Rate: dec(fxBill2Rate),
		}},
	}, "tester"); err != nil {
		t.Fatalf("create the half-keyed debtor: %v", err)
	}

	rows := e.rows(e.receivables(), "Half-Keyed Exporter", "OPEN-ZERO")
	if len(rows) != 1 {
		t.Fatalf("the half-keyed bill came back as %d row(s), wanted 1 — its rupee "+
			"figure is zero, so a skip that asks only `Pending.IsZero()` drops $400 of "+
			"exposure off the report entirely", len(rows))
	}
	if !rows[0].ForeignPending.Equal(dec("400.00")) || rows[0].CurrencyCode != "USD" {
		t.Errorf("the half-keyed bill reports %q %s, wanted USD 400.0000",
			rows[0].CurrencyCode, rows[0].ForeignPending.StringFixed(4))
	}
}

// TestAFullySettledForeignBillIsStillSkipped is the inverse control for the test
// above: widening the skip must not turn every closed bill into an open one.
func TestAFullySettledForeignBillIsStillSkipped(t *testing.T) {
	e := forexReportSetup(t)

	e.sale("EXP-1", "EXP-1", e.debtor, fxBill1Base, e.usd, fxBill1Foreign, fxBill1Rate, "2025-06-10")
	// The FINAL slice clears the remaining book value in full (step 5's Decision 3).
	// realised = round(1000 × 85.2512345678, 4) − 83651.23 = 1600.0046.
	e.receipt("RCT-FULL", "EXP-1", e.debtor, e.usd,
		fxBill1Foreign, fxBill1SettleRate, fxBill1Base, "1600.0046")

	if rows := e.rows(e.receivables(), "Foreign Exports Ltd", "EXP-1"); len(rows) != 0 {
		t.Errorf("a fully settled foreign bill is still on the report as %d row(s): %s — "+
			"both columns net to zero, so it is closed, and a skip widened to `base OR "+
			"foreign is non-zero` would leave every closed bill open for ever",
			len(rows), describeBills(rows))
	}
}

// ---------------------------------------------------------------------------
// 6. ⚠️ THE PAYABLE SIDE, because the sign expression is written twice.
// ---------------------------------------------------------------------------

// TestAForeignPayableReportsItsForeignPendingOnTheCreditorSide covers the second
// spelling of the netting sign. A foreign column added to the receivable branch and
// forgotten on the payable one is a report that is right for exporters and blank for
// importers.
func TestAForeignPayableReportsItsForeignPendingOnTheCreditorSide(t *testing.T) {
	e := forexReportSetup(t)

	purchases, err := e.ms.EnsureLedger(e.ctx, "Import Purchases", "Purchase Accounts", "tester")
	if err != nil {
		t.Fatalf("purchases ledger: %v", err)
	}
	if _, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class: ledger.Purchase, PartyLedgerID: e.creditor, VoucherNo: "IMP-1",
		VoucherDate: "2025-05-12", Party: "import", CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: purchases, DrCr: ledger.Debit, Amount: dec(fxBill1Base)},
			{
				LedgerID: e.creditor, DrCr: ledger.Credit, Amount: dec(fxBill1Base),
				CurrencyID: e.usd, ForeignAmount: dec(fxBill1Foreign), Rate: dec(fxBill1Rate),
			},
		},
		BillAllocations: []ledger.BillAllocationInput{{
			LedgerID: e.creditor, RefName: "IMP-1", RefType: ledger.RefNew,
			DrCr: ledger.Credit, Amount: dec(fxBill1Base), DueDate: "2025-06-12",
			CurrencyID: e.usd, ForeignAmount: dec(fxBill1Foreign), Rate: dec(fxBill1Rate),
		}},
	}); err != nil {
		t.Fatalf("post the import invoice: %v", err)
	}

	out, err := e.svc.Outstandings(e.ctx, e.companyID, reporting.Payables, "", nil)
	if err != nil {
		t.Fatalf("payables: %v", err)
	}
	got := e.bill(out, "Overseas Supplies Inc", "IMP-1")
	if got.CurrencyCode != "USD" || !got.ForeignPending.Equal(dec(fxBill1Foreign)) {
		t.Errorf("the foreign PAYABLE reports %q %s, wanted USD %s — the netting sign is "+
			"spelled twice in this query and a foreign column added to one branch is a "+
			"report that works for exporters and is blank for importers",
			got.CurrencyCode, got.ForeignPending.StringFixed(4), fxBill1Foreign)
	}
	if !got.Pending.Equal(dec(fxBill1Base)) {
		t.Errorf("the foreign payable's base pending is %s, wanted %s (positive on the "+
			"creditor side)", got.Pending.StringFixed(4), fxBill1Base)
	}
}
