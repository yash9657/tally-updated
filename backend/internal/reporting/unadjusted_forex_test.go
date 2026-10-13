package reporting_test

// unadjusted_forex_test.go — ⛔ T8 STEP 7. WHAT AN OPEN FOREIGN BILL IS WORTH TODAY,
// AS AGAINST WHAT THE BOOKS CARRY IT AT. A REPORT THAT POSTS NOTHING.
//
// ===========================================================================
// WHAT IT IS, AND WHAT Q4 FIXED ABOUT IT
// ===========================================================================
// Step 5 realises the difference when a foreign bill is SETTLED. Between the
// invoice and the receipt the rate moves and the books do not: a $1,000 export
// invoice raised at 83.65 and still open at 85.25 is ₹1,600 the business has
// gained and no report in this product would say so.
//
// ⛔ IT POSTS NOTHING. Q4 of the design memo: "report only" in v1, per the repo
// owner's own answer. The revaluation VOUCHER is deferred and costs no design
// debt, because postings are immutable so it could only ever BE a voucher.
//
// ===========================================================================
// ⭐⭐ THE ARITHMETIC IS STEP 5's, WHICH IS THE WHOLE POINT
// ===========================================================================
//
//	unrealised = round(foreign_pending × rate_as_on, 4) − book_value_carried
//
// which is `realisedFor`'s formula with the FINAL-SLICE branch taken: settle the
// whole remaining balance today at today's rate and the realised figure IS this
// number. ⛔ SO THE TWO REPORTS CANNOT DISAGREE, and `TestTodaysUnrealisedIsWhatASettlementTodayWouldRealise`
// is the assertion that keeps them honest — it is the composition invariant
// `forex_settlement_test.go` states from the other side.
//
// ⚠️ AND IT DIFFERENCES THE VALUES, NOT THE RATES, for Decision 2's reason: a
// bill's stored base is authoritative and is deliberately NOT `foreign × rate`.
//
// ===========================================================================
// ⛔ EVERY TEST HERE CARRIES A CONTROL, and two of them are the whole file's point:
// a BASE bill must not appear at all, and a bill whose currency has NO RATE on or
// before the report date must appear WITHOUT a difference rather than with a zero.
// ===========================================================================

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/reporting"
)

// The rate "as on" the report date, a fourth rate distinct from every bill rate
// and every settlement rate in this package's fixtures.
const fxAsOnRate = "88.4512345678"
const fxAsOnDate = "2025-08-31"

func (e *forexReportEnv) setRate(code, on, rate string) {
	e.t.Helper()
	if err := e.ms.SetExchangeRate(e.ctx, code, on, rate, "tester"); err != nil {
		e.t.Fatalf("set the %s rate as on %s: %v", code, on, err)
	}
}

func (e *forexReportEnv) unadjusted(asOn string) reporting.UnadjustedForex {
	e.t.Helper()
	out, err := e.svc.UnadjustedForex(e.ctx, e.companyID, asOn)
	if err != nil {
		e.t.Fatalf("unadjusted forex: %v", err)
	}
	return out
}

func fxRow(out reporting.UnadjustedForex, ledgerName, ref string) (reporting.UnadjustedForexBill, bool) {
	for _, b := range out.Bills {
		if b.LedgerName == ledgerName && b.RefName == ref {
			return b, true
		}
	}
	return reporting.UnadjustedForexBill{}, false
}

func describeFx(out reporting.UnadjustedForex) string {
	s := ""
	for _, b := range out.Bills {
		s += "\n\t  " + b.LedgerName + " / " + b.RefName + " " + b.CurrencyCode +
			" fgn=" + b.ForeignPending.StringFixed(4) +
			" book=" + b.BookValue.StringFixed(4) +
			" billRate=" + b.BillRate.String() +
			" asOn=" + b.RateAsOn.String() +
			" diff=" + b.Difference.StringFixed(4)
		if !b.HasRateAsOn {
			s += " NO-RATE"
		}
	}
	return s
}

// ---------------------------------------------------------------------------
// 1. THE CORE — FIVE FIGURES PER BILL, AND A BASE BILL IS NOT ON THE REPORT
// ---------------------------------------------------------------------------

// TestAnOpenForeignBillIsRevaluedAtTheRateAsOnTheReportDate is Q4's whole first
// half: per bill, the foreign pending, the rate the bill was RAISED at, the rate as
// on the report date, the base figure the books carry, and the difference.
func TestAnOpenForeignBillIsRevaluedAtTheRateAsOnTheReportDate(t *testing.T) {
	e := forexReportSetup(t)
	e.setRate("USD", fxAsOnDate, fxAsOnRate)

	// $1,000 at 83.6512345678, carried at 83,651.23; $200 settled at 85.2512345678
	// clearing 16,730.2460 pro-rata. So $800 remains, carried at 66,920.9840.
	e.sale("EXP-1", "EXP-1", e.debtor, fxBill1Base, e.usd, fxBill1Foreign, fxBill1Rate, "2025-06-10")
	e.receipt("RCT-1", "EXP-1", e.debtor, e.usd,
		fxBill1SettleForeign, fxBill1SettleRate, fxBill1SettleBase, fxBill1Realised)

	// ⭐ THE CONTROL — a rupee bill. It has no rate, nothing to revalue, and must
	// not be on this report AT ALL. A report that listed it with a zero difference
	// would put every bill in the field on a page about foreign exposure.
	e.sale("DOM-1", "DOM-1", e.rupee, "50000.00", uuid.Nil, "", "", "2025-06-30")

	out := e.unadjusted(fxAsOnDate)

	got, ok := fxRow(out, "Foreign Exports Ltd", "EXP-1")
	if !ok {
		t.Fatalf("EXP-1 is not on the report; rows are %s", describeFx(out))
	}
	if got.CurrencyCode != "USD" {
		t.Errorf("EXP-1 currency is %q, wanted USD", got.CurrencyCode)
	}
	if !got.ForeignPending.Equal(dec(fxBill1PendingFgn)) {
		t.Errorf("EXP-1 foreign pending is %s, wanted %s — this report revalues what is "+
			"STILL OPEN, not what the bill was raised for",
			got.ForeignPending.StringFixed(4), fxBill1PendingFgn)
	}
	if !got.BookValue.Equal(dec(fxBill1PendingBase)) {
		t.Errorf("EXP-1 book value is %s, wanted %s — the figure the books actually "+
			"carry, which is what the difference is measured against",
			got.BookValue.StringFixed(4), fxBill1PendingBase)
	}
	// ⛔ THE BILL'S OWN RATE, AT TEN DECIMALS. §6.3: a rate is NUMERIC(19,10) and
	// money is (19,4), and `StringFixed(4)` on a rate reads as consistency while
	// silently changing it.
	// ⛔ COMPARED AS A **VALUE**, NOT AS A STRING, and the distinction is measured:
	// `decimal.Decimal.String()` drops a trailing zero, so 75.1234567890 comes back
	// "75.123456789" and a string assertion fails on a rate that is exactly right.
	// ⭐ THE TEN-DECIMAL RENDERING IS THE WIRE'S JOB and is asserted there — the
	// server formats with StringFixed(10) and the drive compares the emitted text,
	// which is where §6.3's failure actually lives (a rate through a money formatter
	// gives 0.0033 for 0.0033333333 and round-trips through its own type perfectly).
	if !got.BillRate.Equal(dec(fxBill1Rate)) {
		t.Errorf("EXP-1's bill rate is %s, wanted %s — the rate the bill was RAISED at",
			got.BillRate.String(), fxBill1Rate)
	}
	if got.RateAsOn.String() != fxAsOnRate {
		t.Errorf("EXP-1's rate as on %s is %q, wanted %q", fxAsOnDate,
			got.RateAsOn.String(), fxAsOnRate)
	}
	if !got.HasRateAsOn {
		t.Error("EXP-1 reports no rate as on the date, and one was entered for it")
	}
	// round(800 × 88.4512345678, 4) − 66,920.9840
	//   = round(70,760.98765424, 4) − 66,920.9840
	//   = 70,760.9877 − 66,920.9840 = 3,840.0037
	want := dec("3840.0037")
	if !got.Difference.Equal(want) {
		t.Errorf("EXP-1's unrealised difference is %s, wanted %s — round(%s × %s, 4) "+
			"less the book value %s. ⛔ ONE ROUNDING, ON THE PRODUCT: quantising the "+
			"rate to money first gives a plausible figure that is wrong for ever",
			got.Difference.StringFixed(4), want.StringFixed(4),
			fxBill1PendingFgn, fxAsOnRate, fxBill1PendingBase)
	}

	// ⭐ THE CONTROL, ASSERTED AS HARD AS THE SUBJECT.
	if _, on := fxRow(out, "Domestic Traders", "DOM-1"); on {
		t.Errorf("the RUPEE bill DOM-1 is on the unadjusted-forex report. It has no "+
			"currency and no rate, so there is nothing to revalue — listing it with a "+
			"zero difference would put every bill in the field on a page about foreign "+
			"exposure. Rows: %s", describeFx(out))
	}
	if !out.TotalDifference.Equal(want) {
		t.Errorf("the total difference is %s, wanted %s — ⭐ UNLIKE Outstandings' "+
			"foreign column this total IS meaningful, because a DIFFERENCE is in the "+
			"books' own currency whatever currency produced it",
			out.TotalDifference.StringFixed(4), want.StringFixed(4))
	}
}

// TestTodaysUnrealisedIsWhatASettlementTodayWouldRealise is the invariant that
// binds step 7 to step 5. ⛔ IF THESE TWO EVER DISAGREE, ONE OF THE TWO REPORTS IS
// WRONG AND NOTHING ELSE IN THE PRODUCT WOULD SAY WHICH.
//
// The bill is settled IN FULL at exactly the rate the report used, so step 5's
// engine derives the realised figure from its own arithmetic — and the report's
// prediction, taken a moment earlier, must be the same number to the paisa.
func TestTodaysUnrealisedIsWhatASettlementTodayWouldRealise(t *testing.T) {
	e := forexReportSetup(t)
	e.setRate("USD", fxAsOnDate, fxAsOnRate)

	e.sale("EXP-1", "EXP-1", e.debtor, fxBill1Base, e.usd, fxBill1Foreign, fxBill1Rate, "2025-06-10")

	// What the report SAYS the exposure is, before anything is settled.
	predicted, ok := fxRow(e.unadjusted(fxAsOnDate), "Foreign Exports Ltd", "EXP-1")
	if !ok {
		t.Fatal("EXP-1 is not on the report")
	}

	// ⛔ NOW SETTLE THE WHOLE BILL AT THAT VERY RATE. The engine derives the realised
	// figure itself and REFUSES any disagreement (step 5's verify design), so the
	// figure below is only postable if the engine agrees with it — which makes this
	// helper's `realised` argument an assertion, not a fixture.
	realised := predicted.Difference.StringFixed(4)
	e.receipt("RCT-FULL", "EXP-1", e.debtor, e.usd,
		fxBill1Foreign, fxAsOnRate, fxBill1Base, realised)

	// And afterwards there is nothing left to revalue.
	out := e.unadjusted(fxAsOnDate)
	if _, still := fxRow(out, "Foreign Exports Ltd", "EXP-1"); still {
		t.Errorf("EXP-1 is STILL on the unadjusted-forex report after being settled in "+
			"full — the exposure is closed and the difference is now REALISED, so "+
			"reporting it again would double-count it. Rows: %s", describeFx(out))
	}
	if !out.TotalDifference.IsZero() {
		t.Errorf("the total unrealised difference is %s after the only bill closed, "+
			"wanted zero", out.TotalDifference.StringFixed(4))
	}
}

// ---------------------------------------------------------------------------
// 2. ⛔⛔ A MISSING RATE IS NOT A RATE OF ZERO
// ---------------------------------------------------------------------------

// TestABillWhoseCurrencyHasNoRateAsOnTheDateIsListedWITHOUTADifference is the
// honesty assertion this report cannot ship without.
//
// ⛔ A ZERO RATE REVALUES THE WHOLE EXPOSURE TO NOTHING. A $1,000 receivable
// carried at 83,651.23, revalued at a rate of 0, is a LOSS of 83,651.23 — a large,
// plausible, correctly-signed figure in the right column, produced by a rate nobody
// entered. `masters.RateAsOn` returns `(zero, false, nil)` for exactly this case and
// the `false` is the whole answer; a report that read only the decimal would
// silently publish the zero.
func TestABillWhoseCurrencyHasNoRateAsOnTheDateIsListedWITHOUTADifference(t *testing.T) {
	e := forexReportSetup(t)
	// ⛔ NO RATE ENTERED AT ALL for USD.
	e.sale("EXP-1", "EXP-1", e.debtor, fxBill1Base, e.usd, fxBill1Foreign, fxBill1Rate, "2025-06-10")

	out := e.unadjusted(fxAsOnDate)
	got, ok := fxRow(out, "Foreign Exports Ltd", "EXP-1")
	if !ok {
		t.Fatalf("EXP-1 is not on the report at all. A bill whose rate is unknown is "+
			"still an EXPOSURE and must be listed — dropping it is how an operator "+
			"reads a short report as a small exposure. Rows: %s", describeFx(out))
	}
	if got.HasRateAsOn {
		t.Errorf("EXP-1 claims a rate as on %s (%s) and none was ever entered",
			fxAsOnDate, got.RateAsOn.String())
	}
	if !got.Difference.IsZero() {
		t.Errorf("EXP-1's difference is %s with no rate to compute it from, wanted zero "+
			"AND flagged. ⛔ Revaluing at a rate of 0 turns a %s receivable into a loss "+
			"of that whole amount: a large, plausible, correctly-signed figure produced "+
			"by a rate nobody entered", got.Difference.StringFixed(4), fxBill1Base)
	}
	if out.UnratedCount != 1 {
		t.Errorf("the report says %d bill(s) have no rate, wanted 1 — the count is the "+
			"DISCLOSURE, and a report whose total silently omits a row is worse than "+
			"one that says what it omitted", out.UnratedCount)
	}
	if !out.TotalDifference.IsZero() {
		t.Errorf("the total is %s and no difference was computable, wanted zero",
			out.TotalDifference.StringFixed(4))
	}
}

// TestARateEnteredAFTERTheReportDateIsNotUsed pins the "as on" semantics against
// the one mistake that produces a number rather than an error.
func TestARateEnteredAFTERTheReportDateIsNotUsed(t *testing.T) {
	e := forexReportSetup(t)
	// An EARLIER rate, and a LATER one. Asked as at the earlier day, the later rate
	// must not be reached — a rate is "as on" a date, and reading a future rate
	// restates a position with information that did not exist.
	e.setRate("USD", "2025-07-15", "80.0000000000")
	e.setRate("USD", "2025-09-30", "99.0000000000")
	e.sale("EXP-1", "EXP-1", e.debtor, fxBill1Base, e.usd, fxBill1Foreign, fxBill1Rate, "2025-06-10")

	got, ok := fxRow(e.unadjusted(fxAsOnDate), "Foreign Exports Ltd", "EXP-1")
	if !ok {
		t.Fatal("EXP-1 is not on the report")
	}
	if got.RateAsOn.String() != "80" {
		t.Errorf("the rate as on %s is %q, wanted 80 — the LATEST rate on or before "+
			"the date, never a later one", fxAsOnDate, got.RateAsOn.String())
	}
	// round(1000 × 80, 4) − 83,651.23 = 80,000 − 83,651.23 = −3,651.23, a LOSS.
	want := dec("-3651.2300")
	if !got.Difference.Equal(want) {
		t.Errorf("the difference is %s, wanted %s (a LOSS — the rupee has strengthened "+
			"against the books)", got.Difference.StringFixed(4), want.StringFixed(4))
	}
}

// ---------------------------------------------------------------------------
// 3. ⛔ THE PAYABLE SIDE — THE SIGN IS THE WHOLE ASSERTION
// ---------------------------------------------------------------------------

// TestARisingRateLOSESOnAPayableAndGAINSOnAReceivable is `realisedFor`'s own
// reasoning applied to the unrealised figure: "an engine that read the difference
// off the rates alone, with no regard for which side of the books the bill sits on,
// is wrong by TWICE the figure".
//
// ⭐ AND IT IS ONE REPORT COVERING BOTH SIDES, which is the point of a NET exposure
// figure: the two rows must offset, not accumulate.
func TestARisingRateLOSESOnAPayableAndGAINSOnAReceivable(t *testing.T) {
	e := forexReportSetup(t)
	e.setRate("USD", fxAsOnDate, fxAsOnRate)

	e.sale("EXP-1", "EXP-1", e.debtor, fxBill1Base, e.usd, fxBill1Foreign, fxBill1Rate, "2025-06-10")
	e.purchase("IMP-1", "IMP-1", fxBill1Base, fxBill1Foreign, fxBill1Rate, "2025-06-11")

	out := e.unadjusted(fxAsOnDate)
	rec, ok1 := fxRow(out, "Foreign Exports Ltd", "EXP-1")
	pay, ok2 := fxRow(out, "Overseas Supplies Inc", "IMP-1")
	if !ok1 || !ok2 {
		t.Fatalf("wanted both the receivable and the payable on ONE report; rows are %s",
			describeFx(out))
	}
	// round(1000 × 88.4512345678, 4) − 83,651.23 = 88,451.2346 − 83,651.23 = 4,800.0046
	wantGain := dec("4800.0046")
	if !rec.Difference.Equal(wantGain) {
		t.Errorf("the RECEIVABLE's difference is %s, wanted %s (a GAIN)",
			rec.Difference.StringFixed(4), wantGain.StringFixed(4))
	}
	if !pay.Difference.Equal(wantGain.Neg()) {
		t.Errorf("the PAYABLE's difference is %s, wanted %s (a LOSS of the same "+
			"amount) — the same rise in the rate that gains on a receivable loses on a "+
			"payable, and a report that read the rates alone is wrong by TWICE the "+
			"figure while every number on it still looks like money",
			pay.Difference.StringFixed(4), wantGain.Neg().StringFixed(4))
	}
	// ⭐ AND THEY NET. That is what makes one report the right shape for this.
	if !out.TotalDifference.IsZero() {
		t.Errorf("the total is %s, wanted zero — a $1,000 receivable and a $1,000 "+
			"payable at the same rate are a net exposure of nothing, and two separate "+
			"reports could not say so", out.TotalDifference.StringFixed(4))
	}
}

// ---------------------------------------------------------------------------
// 4. ⛔⛔ §0.2 AGAIN — THE OPENING BILL, WHICH IS THE HALF THAT GETS FORGOTTEN
// ---------------------------------------------------------------------------

// TestAForeignOPENINGBillIsRevaluedToo. This report reads the SAME two-table UNION
// Outstandings does, and it is a NEW spelling of it (the sixth in this package), so
// the trap is live again: handle only `bill_allocation` and every migrated foreign
// debtor's exposure is invisible.
func TestAForeignOPENINGBillIsRevaluedToo(t *testing.T) {
	e := forexReportSetup(t)
	e.setRate("USD", fxAsOnDate, fxAsOnRate)

	if _, err := e.ms.CreateLedger(e.ctx, masters.Ledger{
		Name: "Migrated Exporter", GroupName: "Sundry Debtors",
		MaintainBillByBill: true,
		OpeningBalance:     dec(fxBill2Base), OpeningDC: "D",
		OpeningBills: []masters.OpeningBill{{
			RefName: "OPEN-FX", DrCr: "D", Amount: dec(fxBill2Base), DueDate: "2025-05-01",
			CurrencyID: e.usd, ForeignAmount: dec(fxBill2Foreign), Rate: dec(fxBill2Rate),
		}},
	}, "tester"); err != nil {
		t.Fatalf("create the migrated foreign debtor: %v", err)
	}
	// ⭐ THE CONTROL — a migrated RUPEE debtor, same table, same branch.
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

	out := e.unadjusted(fxAsOnDate)
	got, ok := fxRow(out, "Migrated Exporter", "OPEN-FX")
	if !ok {
		t.Fatalf("the foreign OPENING bill is not on the report. This report reads a "+
			"UNION of bill_allocation AND ledger_opening_bill, and handling only the "+
			"first makes every migrated foreign debtor's exposure invisible — which "+
			"for a customer acquired before this build IS their whole history. "+
			"Rows: %s", describeFx(out))
	}
	if !got.BillRate.Equal(dec(fxBill2Rate)) {
		t.Errorf("the opening bill's rate is %s, wanted %s — read off the opening "+
			"breakup, not defaulted", got.BillRate.String(), fxBill2Rate)
	}
	// round(600 × 88.4512345678, 4) − 45,074.07 = 53,070.7407 − 45,074.07 = 7,996.6707
	want := dec("7996.6707")
	if !got.Difference.Equal(want) {
		t.Errorf("the opening bill's difference is %s, wanted %s",
			got.Difference.StringFixed(4), want.StringFixed(4))
	}
	if _, on := fxRow(out, "Migrated Domestic", "OPEN-INR"); on {
		t.Errorf("the RUPEE opening control is on the report; it has no currency")
	}
}

// ---------------------------------------------------------------------------
// 5. ⚠️ WHAT IS DELIBERATELY NOT REVALUED, AND WHAT IS
// ---------------------------------------------------------------------------

// TestOnAccountIsNotOnThisReportAndAForeignADVANCEIs records step 7's answer to the
// memo's note that "ADVANCE and ON_ACCOUNT have no bill rate to difference against".
//
// ⭐ THAT NOTE IS ABOUT Q3 — REALISED — AND IT SAYS SO IN ITS OWN WORDS: "they
// realise nothing until they are allocated". An UNREALISED difference is measured
// against the BOOK VALUE CARRIED and not against a bill's historical rate, so:
//
//   - `On Account` is NOT on this report. Step 6 made that bucket base-only and
//     currency-collapsed, deliberately: it is `the whole balance − every named
//     reference`, a difference of sums over many rates, and it carries no currency
//     at all to revalue.
//   - A foreign `ADVANCE` **IS** revalued. It is a NAMED reference with one rate on
//     one document and a real book value; $10,000 received at 83 and still held at
//     88 is a genuine ₹50,000 exposure, and omitting it understates the position.
//
// ⚠️ THE SECOND HALF IS A DESIGN DECISION TAKEN HERE, not parity, and it is
// flagged rather than folded in.
func TestOnAccountIsNotOnThisReportAndAForeignADVANCEIs(t *testing.T) {
	e := forexReportSetup(t)
	e.setRate("USD", fxAsOnDate, fxAsOnRate)

	// A foreign ADVANCE: $100 received at the bill-1 rate, carried at 8,365.12.
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

	// ⛔⛔ AN **EXPLICIT** FOREIGN ON_ACCOUNT ALLOCATION, AND IT IS THE CASE THAT
	// MAKES THE EXCLUSION LOAD-BEARING. Measured: the DERIVED residue cannot reach
	// this report at all — it is computed from `posting`, and this query reads
	// `bill_allocation` and `ledger_opening_bill` and never `posting`. So removing
	// the name filter changes nothing for an unreferenced receipt and everything for
	// this row, which arrives with a currency and a rate on it.
	//
	// ⭐ AND REVALUING IT WOULD BE WORSE THAN OMITTING IT. Outstandings shows the
	// whole On Account bucket as ONE base figure (step 6 collapses it, because it is
	// `the whole balance − every named reference` over many rates). Revaluing only
	// the fragment that happens to have been keyed with an allocation is a partial
	// revaluation of a figure no report presents in parts — a number that ties to
	// nothing on either page.
	walkin, err := e.ms.CreateLedger(e.ctx, masters.Ledger{
		Name: "Walk-In Exports", GroupName: "Sundry Debtors",
	}, "tester")
	if err != nil {
		t.Fatalf("create the un-tracked party: %v", err)
	}
	if _, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class: ledger.Receipt, PartyLedgerID: walkin, VoucherNo: "RCT-OA",
		VoucherDate: "2025-06-02", Party: "export", CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{
				LedgerID: walkin, DrCr: ledger.Credit, Amount: dec("5000.00"),
				CurrencyID: e.usd, ForeignAmount: dec("60.00"), Rate: dec(fxBill1Rate),
			},
			{LedgerID: e.bank, DrCr: ledger.Debit, Amount: dec("5000.00")},
		},
		BillAllocations: []ledger.BillAllocationInput{{
			LedgerID: walkin, RefName: reporting.OnAccountRefName,
			RefType: ledger.RefOnAccount, DrCr: ledger.Credit, Amount: dec("5000.00"),
			CurrencyID: e.usd, ForeignAmount: dec("60.00"), Rate: dec(fxBill1Rate),
		}},
	}); err != nil {
		t.Fatalf("post the explicit foreign ON_ACCOUNT receipt: %v", err)
	}

	out := e.unadjusted(fxAsOnDate)

	if _, on := fxRow(out, "Walk-In Exports", reporting.OnAccountRefName); on {
		t.Errorf("`On Account` is on the unadjusted-forex report. Step 6 made that "+
			"bucket base-only and currency-collapsed on purpose — it is a difference "+
			"of sums over many rates and carries no currency to revalue, so a row here "+
			"would be a revaluation of a figure that has no rate. Rows: %s",
			describeFx(out))
	}

	adv, ok := fxRow(out, "Foreign Exports Ltd", "ADV-1")
	if !ok {
		t.Fatalf("the foreign ADVANCE is not on the report. It is a NAMED reference "+
			"with one rate on one document and a real book value, and an unrealised "+
			"difference is measured against the book value carried rather than against "+
			"a bill's historical rate. Rows: %s", describeFx(out))
	}
	// A CREDIT on a debtor, so the foreign pending and the book value are negative,
	// and a rising rate therefore LOSES: round(-100 × 88.4512345678, 4) − (−8,365.12)
	//   = −8,845.1235 + 8,365.12 = −480.0035
	want := dec("-480.0035")
	if !adv.Difference.Equal(want) {
		t.Errorf("the advance's difference is %s, wanted %s — an advance HELD is a "+
			"liability in dollars, so the same rise that gains on a receivable loses "+
			"here, and the sign follows the position rather than the document class",
			adv.Difference.StringFixed(4), want.StringFixed(4))
	}
}

// TestAFullySettledForeignBillIsNotOnTheReport is the skip's control: this report
// is about what is STILL OPEN, and a closed bill's difference has been realised.
func TestAFullySettledForeignBillIsNotOnTheReport(t *testing.T) {
	e := forexReportSetup(t)
	e.setRate("USD", fxAsOnDate, fxAsOnRate)

	e.sale("EXP-1", "EXP-1", e.debtor, fxBill1Base, e.usd, fxBill1Foreign, fxBill1Rate, "2025-06-10")
	e.sale("EXP-2", "EXP-2", e.debtor, fxBill2Base, e.usd, fxBill2Foreign, fxBill2Rate, "2025-06-12")
	e.receipt("RCT-FULL", "EXP-1", e.debtor, e.usd,
		fxBill1Foreign, fxBill1SettleRate, fxBill1Base, "1600.0046")

	out := e.unadjusted(fxAsOnDate)
	if _, on := fxRow(out, "Foreign Exports Ltd", "EXP-1"); on {
		t.Errorf("the fully settled EXP-1 is still on the report — its difference is "+
			"REALISED now, and reporting it here double-counts it. Rows: %s",
			describeFx(out))
	}
	// ⭐ THE CONTROL — the OTHER bill is still there, so "nothing is reported" is
	// distinguishable from "the settled one was dropped".
	if _, on := fxRow(out, "Foreign Exports Ltd", "EXP-2"); !on {
		t.Errorf("EXP-2 is open and is NOT on the report; the skip is dropping "+
			"everything. Rows: %s", describeFx(out))
	}
}

// TestABillWhoseBaseNetsToZeroButWhoseForeignDoesNotIsStillRevalued is the second
// half of the two-question skip, and step 6's own control repeated here because THIS
// report is the one where dropping the row does the most damage: a $400 exposure
// that has no rupee figure behind it is precisely what an exposure statement exists
// to surface, and a skip that asks only `BookValue.IsZero()` drops it.
//
// ⛔ MEASURED AS UNCOVERED FIRST. Reverting the skip to base-only left every test in
// this file green, which is how the base-only skip would have shipped.
func TestABillWhoseBaseNetsToZeroButWhoseForeignDoesNotIsStillRevalued(t *testing.T) {
	e := forexReportSetup(t)
	e.setRate("USD", fxAsOnDate, fxAsOnRate)

	// A mis-keyed migration: the foreign figure was carried in and the rupee figure
	// was not. Legal at the schema level (amount >= 0 and the triple is complete) and
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

	out := e.unadjusted(fxAsOnDate)
	got, ok := fxRow(out, "Half-Keyed Exporter", "OPEN-ZERO")
	if !ok {
		t.Fatalf("the half-keyed bill is not on the report — its rupee figure is zero, "+
			"so a skip that asks only `BookValue.IsZero()` drops $400 of exposure off "+
			"the one report whose whole subject is exposure. Rows: %s", describeFx(out))
	}
	// round(400 × 88.4512345678, 4) − 0 = 35,380.4938
	want := dec("35380.4938")
	if !got.Difference.Equal(want) {
		t.Errorf("the half-keyed bill's difference is %s, wanted %s — the WHOLE rupee "+
			"value of the position, because the books carry none of it",
			got.Difference.StringFixed(4), want.StringFixed(4))
	}
}

// ---------------------------------------------------------------------------
// 6. ⚠️ THE "AS ON" DATE IS TWO QUESTIONS, EXACTLY AS IT IS ON OUTSTANDINGS (D86)
// ---------------------------------------------------------------------------

// TestTheAsOnDateBoundsTheROWSAndPicksTheRATE. On this report the date does BOTH
// jobs and they are genuinely different: it limits which vouchers are included AND
// it chooses which rate the revaluation uses. ⛔ A report that honoured it for one
// and not the other is the shape of D86.
func TestTheAsOnDateBoundsTheROWSAndPicksTheRATE(t *testing.T) {
	e := forexReportSetup(t)
	e.setRate("USD", "2025-06-30", "86.0000000000")
	e.setRate("USD", fxAsOnDate, fxAsOnRate)

	e.saleOn("EARLY", "EARLY", e.debtor, fxBill1Base, e.usd, fxBill1Foreign, fxBill1Rate,
		"2025-07-10", "2025-06-10")
	// ⛔ DATED AFTER THE EARLIER AS-ON DATE — `saleOn`, because `sale`'s last argument
	// is the DUE date and passing a voucher date there is silently ignored.
	e.saleOn("LATE", "LATE", e.debtor, fxBill2Base, e.usd, fxBill2Foreign, fxBill2Rate,
		"2025-08-20", "2025-07-20")

	early := e.unadjusted("2025-06-30")
	if _, on := fxRow(early, "Foreign Exports Ltd", "LATE"); on {
		t.Errorf("as at 2025-06-30 the report includes LATE, a bill dated 2025-07-20 — "+
			"an exposure statement that lists invoices which did not exist yet. "+
			"Rows: %s", describeFx(early))
	}
	got, ok := fxRow(early, "Foreign Exports Ltd", "EARLY")
	if !ok {
		t.Fatalf("EARLY is missing from the bounded report; rows are %s", describeFx(early))
	}
	if got.RateAsOn.String() != "86" {
		t.Errorf("the bounded report used a rate of %q, wanted 86 — the rate as on the "+
			"date it was asked for, not the latest rate on the books",
			got.RateAsOn.String())
	}
	// round(1000 × 86, 4) − 83,651.23 = 86,000 − 83,651.23 = 2,348.77
	if !got.Difference.Equal(dec("2348.7700")) {
		t.Errorf("EARLY's bounded difference is %s, wanted 2348.7700",
			got.Difference.StringFixed(4))
	}
	if !early.Bounded {
		t.Error("the report does not report itself as bounded, and the caller named a date")
	}

	// ⭐ AND THE UNBOUNDED READ IS A DIFFERENT ANSWER, so `Bounded` is load-bearing
	// rather than decorative.
	all := e.unadjusted("")
	if _, on := fxRow(all, "Foreign Exports Ltd", "LATE"); !on {
		t.Errorf("the unbounded report omits LATE; unbounded means every bill these "+
			"books carry. Rows: %s", describeFx(all))
	}
	if all.Bounded {
		t.Error("the unbounded report claims to be bounded (D86: empty is an answer)")
	}
}

// ---------------------------------------------------------------------------
// 7. ⛔ THE COMPOSITION INVARIANT, OVER A BOOK WITH TWO CURRENCIES AND TWO RATES
// ---------------------------------------------------------------------------

// TestTheTotalIsTheSumOfTheRowsItActuallyPriced is the report's own
// reconciliation, and it is the shape `sum(SlabTotals) + OnAccountTotal == Total`
// takes here: a reader adds the difference column up, so the total must be that sum
// and the rows it could NOT price must be counted separately rather than silently
// contributing zero.
func TestTheTotalIsTheSumOfTheRowsItActuallyPriced(t *testing.T) {
	e := forexReportSetup(t)
	e.setRate("USD", fxAsOnDate, fxAsOnRate)

	eur, err := e.ms.CreateCurrency(e.ctx, masters.Currency{
		Code: "EUR", Symbol: "€", FormalName: "Euro", DecimalPlaces: 2,
	}, "tester")
	if err != nil {
		t.Fatalf("create the EUR master: %v", err)
	}
	// ⛔ EUR gets NO rate, so its bill is an exposure the report cannot price.
	e.sale("EXP-1", "EXP-1", e.debtor, fxBill1Base, e.usd, fxBill1Foreign, fxBill1Rate, "2025-06-10")
	e.sale("EXP-2", "EXP-2", e.debtor, fxBill2Base, e.usd, fxBill2Foreign, fxBill2Rate, "2025-06-12")
	e.sale("EUR-1", "EUR-1", e.debtor, "90000.00", eur, "1000.00", "90.0000000000", "2025-06-13")

	out := e.unadjusted(fxAsOnDate)

	priced := decimal.Zero
	unpriced := 0
	for _, b := range out.Bills {
		if b.HasRateAsOn {
			priced = priced.Add(b.Difference)
			continue
		}
		unpriced++
		if !b.Difference.IsZero() {
			t.Errorf("%s / %s has no rate and a difference of %s", b.LedgerName,
				b.RefName, b.Difference.StringFixed(4))
		}
	}
	if !out.TotalDifference.Equal(priced) {
		t.Errorf("the total is %s and the priced rows sum to %s — a reader adds this "+
			"column up, so the two must agree",
			out.TotalDifference.StringFixed(4), priced.StringFixed(4))
	}
	if out.UnratedCount != unpriced || unpriced != 1 {
		t.Errorf("UnratedCount is %d, the rows say %d, and exactly 1 was expected (the "+
			"EUR bill) — the count is the disclosure that the total is short",
			out.UnratedCount, unpriced)
	}
	if len(out.Bills) != 3 {
		t.Errorf("the report carries %d rows, wanted 3 (two USD and one unpriced EUR): %s",
			len(out.Bills), describeFx(out))
	}
}
