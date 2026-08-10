package reporting_test

// THE BILL REFERENCE A BROWSER CAN ACTUALLY MAKE.
//
// For the whole life of this feature no screen on any voucher path created a
// bill_allocation, so a receivable keyed in a browser sat in the Balance Sheet's
// Sundry Debtors and was ABSENT from Outstandings: money that never aged and
// never reached a statement. The demo only looked correct because seed-demo.mts
// hand-writes its allocations.
//
// The blocker was not the client's laziness. It was that a client CANNOT NAME
// THE BILL: allocateVoucherNo mints the voucher number inside the post
// transaction, so at the moment a browser assembles its request the name Tally
// would use ("New Ref" defaults to the voucher number) does not exist yet.
//
// So post() names it. An opening reference may arrive unnamed and is named
// after the voucher that opened it. AGAINST may NOT, because
// reporting.Outstandings groups by (ledger_id, ref_name) and ignores ref_type
// entirely — a defaulted AGAINST would open a second bill under its own number
// instead of settling the one it meant to, leaving both figures wrong and the
// report still footing.
//
// ⛔⛔ THESE TWO TESTS USED TO ASSERT `ref_name == the bare voucher number`, AND
// THAT ASSERTION WAS DEFECTS D10 AND D11 WRITTEN DOWN AS AN EXPECTATION. A bare
// number is allocated per (voucher TYPE, financial year), so "7" names one voucher
// per type per year and not one voucher: a Receipt 7's on-account amount netted
// against Sales invoice 7 on the same party, and an invoice split into two
// instalments became one bill because both instalments were stamped "7". The tests
// were right about the ARC — unnamed goes in, the report shows it, a receipt
// settles it — and wrong about the NAME, so the arc is kept and the name is
// asserted as a PROPERTY: it identifies the voucher (it carries its number) and it
// is read back rather than reconstructed. See ledger.go's stampBillRefNames, and
// outstanding_ref_collision_test.go for the collisions themselves.
//
// These tests drive the WHOLE arc a real business does: raise, part-settle,
// age. Each asserts against the INDEPENDENT ORACLE (the Balance Sheet's Sundry
// Debtors, computed from ledger_balance rather than from bill_allocation), so
// agreement between them is a genuine cross-check rather than one query
// reformatted.

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/reporting"
)

// unnamedSale posts a credit sale whose bill reference is left UNNAMED — exactly
// what a browser can construct — and lets the server allocate the voucher
// number. Returns the number the server assigned.
func unnamedSale(t *testing.T, led *ledger.Service, ctx context.Context,
	party, sales uuid.UUID, date, amount string,
) string {
	t.Helper()
	amt := dec(amount)
	res, err := led.PostVoucher(ctx, ledger.VoucherInput{
		Class:         ledger.Sales,
		PartyLedgerID: party,
		// NO VoucherNo: the server allocates it, which is the normal browser case
		// and the whole reason the name cannot be supplied by the caller.
		VoucherDate: date,
		Party:       "party",
		CreatedBy:   "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: party, DrCr: ledger.Debit, Amount: amt},
			{LedgerID: sales, DrCr: ledger.Credit, Amount: amt},
		},
		BillAllocations: []ledger.BillAllocationInput{
			{
				LedgerID: party, RefType: ledger.RefNew,
				DrCr: ledger.Debit, Amount: amt, DueDate: date,
			},
		},
	})
	if err != nil {
		t.Fatalf("post unnamed sale: %v", err)
	}
	if res.VoucherNo == "" {
		t.Fatal("server allocated no voucher number, so there is nothing to stamp")
	}
	return res.VoucherNo
}

// TestUnnamedNewRefReachesTheReportNamedAfterItsVoucher is the reachability
// proof: a reference a browser CAN build reaches the Outstandings report under a
// name that ties it back to the voucher the operator keyed.
//
// ⚠️ IT ASSERTS THE PROPERTY, NOT THE STRING. This test previously demanded the
// BARE allocated number and that demand was the defect — see the header. What the
// operator needs is a name that (a) exists, (b) contains the number printed on the
// document, and (c) says which document, because the number alone does not.
func TestUnnamedNewRefReachesTheReportNamedAfterItsVoucher(t *testing.T) {
	pool, ctx, companyID, _ := setup(t)
	led := ledger.NewService(pool)
	svc := reporting.NewService(pool)
	party, sales := billParties(t, pool, ctx)

	no := unnamedSale(t, led, ctx, party, sales, "2026-06-19", "94400.0000")

	os, err := svc.Outstandings(ctx, companyID, reporting.Receivables, "", nil)
	if err != nil {
		t.Fatalf("outstandings: %v", err)
	}
	if len(os.Parties) != 1 || len(os.Parties[0].Bills) != 1 {
		t.Fatalf("want exactly one party with one bill, got %+v", os.Parties)
	}
	bill := os.Parties[0].Bills[0]

	// (a) NOT BLANK. The old stamp wrote "" whenever no number was allocated, and
	//     every such reference on a party then merged into one nameless bill.
	if bill.RefName == "" {
		t.Fatal("the bill reached the report nameless, so nothing can ever settle it")
	}
	// (b) IT CARRIES THE NUMBER THE OPERATOR SEES. Without this the name could be
	//     a uuid and this test would still pass.
	if !strings.Contains(bill.RefName, no) {
		t.Fatalf("bill ref name %q does not contain the voucher number %q — "+
			"nobody can tie the bill on the ageing report to the invoice in their hand",
			bill.RefName, no)
	}
	// (c) AND IT IS NOT THE BARE NUMBER. That is the whole of D11: the number is
	//     allocated per (voucher type, financial year), so "7" is not an identity.
	//     The name must also say WHICH document and WHICH year.
	if bill.RefName == no {
		t.Fatalf("the bill is named %q, the bare voucher number — every voucher "+
			"TYPE has its own counter per financial year, so a Receipt %q or next "+
			"year's Sales %q would be the same bill on this party", bill.RefName, no, no)
	}
	if want := "Sales/"; !strings.HasPrefix(bill.RefName, want) {
		t.Fatalf("bill ref name %q does not name the class that numbered it", bill.RefName)
	}
	if want := dec("94400"); !bill.Pending.Equal(want) {
		t.Fatalf("pending = %s, want %s", bill.Pending, want)
	}
	// The independent oracle: ledger_balance, not bill_allocation.
	if bs := debtorsFromBalanceSheet(t, svc, ctx, companyID); !bs.Equal(os.Total) {
		t.Fatalf("Balance Sheet Sundry Debtors %s != Outstandings %s "+
			"(the two reports disagree about the same receivable)", bs, os.Total)
	}
}

// TestReceiptAgainstTheStampedRefSettlesIt drives the arc that makes the feature
// worth having: raise 94,400, receive 50,000 against it, and see 44,400 age.
//
// ⛔ AND IT CARRIES THE CONTROL THE REFUSAL EXISTS FOR. Settling requires naming
// the bill; the second half of this test proves that a reference which does NOT
// name it produces a SECOND bill rather than a settlement — which is the exact
// silent-wrong-number failure validateBillAllocations refuses, and without this
// half the first half is consistent with "any receipt reduces any bill".
// ⛔ AND THE NAME IS READ BACK, NEVER RECONSTRUCTED. This test used to settle by
// the bare voucher number, which was only ever going to work while the name WAS
// the bare number. A real client does what this now does: it reads the party's
// open bills (the same reporting.Outstandings call the settle picker's list is
// built from) and settles the one it was shown. Reconstructing a server-minted
// name on the client is the mistake billAllocation.ts's header now warns about.
func TestReceiptAgainstTheStampedRefSettlesIt(t *testing.T) {
	pool, ctx, companyID, _ := setup(t)
	led := ledger.NewService(pool)
	svc := reporting.NewService(pool)
	party, sales := billParties(t, pool, ctx)
	bank := ensureBank(t, pool, ctx)

	no := unnamedSale(t, led, ctx, party, sales, "2026-06-19", "94400.0000")

	// What the picker would offer: the party's open bills, by the name the report
	// identifies each one by.
	open, err := svc.Outstandings(ctx, companyID, reporting.Receivables, "", nil)
	if err != nil {
		t.Fatalf("outstandings: %v", err)
	}
	if len(open.Parties) != 1 || len(open.Parties[0].Bills) != 1 {
		t.Fatalf("want one open bill to settle, got %+v", open.Parties)
	}
	ref := open.Parties[0].Bills[0].RefName

	// The receipt NAMES the bill it settles — the one thing a client must supply.
	if _, err := led.PostVoucher(ctx, receiptAgainst(party, bank, ref, "2026-07-02", "50000.0000")); err != nil {
		t.Fatalf("post receipt: %v", err)
	}

	os, err := svc.Outstandings(ctx, companyID, reporting.Receivables, "", nil)
	if err != nil {
		t.Fatalf("outstandings: %v", err)
	}
	if len(os.Parties) != 1 || len(os.Parties[0].Bills) != 1 {
		t.Fatalf("want ONE netted bill, got %+v — two rows means the receipt "+
			"opened its own reference instead of settling the invoice", os.Parties)
	}
	bill := os.Parties[0].Bills[0]
	if bill.RefName != ref {
		t.Fatalf("netted bill ref = %q, want %q", bill.RefName, ref)
	}
	if want := dec("44400"); !bill.Pending.Equal(want) {
		t.Fatalf("pending after part settlement = %s, want %s", bill.Pending, want)
	}
	if bs := debtorsFromBalanceSheet(t, svc, ctx, companyID); !bs.Equal(os.Total) {
		t.Fatalf("Balance Sheet Sundry Debtors %s != Outstandings %s", bs, os.Total)
	}

	// ── THE CONTROL ────────────────────────────────────────────────────────────
	// A receipt naming a DIFFERENT reference must NOT touch the invoice. If it
	// did, the assertions above would pass for a system that nets any credit
	// against any bill, and the AGAINST-must-be-named refusal would be pointless.
	if _, err := led.PostVoucher(ctx, receiptAgainst(party, bank, "SOME-OTHER-BILL", "2026-07-03", "1000.0000")); err != nil {
		t.Fatalf("post control receipt: %v", err)
	}
	os2, err := svc.Outstandings(ctx, companyID, reporting.Receivables, "", nil)
	if err != nil {
		t.Fatalf("outstandings after control: %v", err)
	}
	var settled, other bool
	for _, b := range os2.Parties[0].Bills {
		switch b.RefName {
		case ref:
			settled = true
			if want := dec("44400"); !b.Pending.Equal(want) {
				t.Fatalf("the invoice moved to %s when a DIFFERENT bill was settled; "+
					"want it untouched at %s", b.Pending, want)
			}
		case "SOME-OTHER-BILL":
			other = true
		}
	}
	if !settled || !other {
		t.Fatalf("want the invoice AND a separate unapplied credit, got %+v", os2.Parties[0].Bills)
	}

	// ── THE SECOND CONTROL, AND IT IS THE PRICE OF THE FIX ─────────────────────
	// ⛔ THE BARE VOUCHER NUMBER IS NOT THE BILL'S NAME ANY MORE, so a client that
	// GUESSES it does not settle anything — it opens a third reference. That is
	// the operator-visible cost of D11 being closed, and it is stated here rather
	// than discovered later: it is why the settle picker offers the names off this
	// very report, and why BillAllocationPicker warns when a hand-typed AGAINST
	// matches nothing in the party's open bills.
	if _, err := led.PostVoucher(ctx, receiptAgainst(party, bank, no, "2026-07-04", "500.0000")); err != nil {
		t.Fatalf("post bare-number receipt: %v", err)
	}
	os3, err := svc.Outstandings(ctx, companyID, reporting.Receivables, "", nil)
	if err != nil {
		t.Fatalf("outstandings after the bare-number receipt: %v", err)
	}
	for _, b := range os3.Parties[0].Bills {
		if b.RefName != ref {
			continue
		}
		if want := dec("44400"); !b.Pending.Equal(want) {
			t.Fatalf("a receipt naming the BARE number %q moved the invoice to %s; "+
				"the bill is called %q and nothing else may net against it", no, b.Pending, ref)
		}
	}
}

// TestAgainstRefMustNameTheBillItSettles is the negative half, and it is
// mutation-proof by construction: it asserts a REFUSAL, so it carries the
// positive case beside it (an unnamed NEW is accepted on the same body shape).
// Without that pairing the test would pass just as happily if post() refused
// every bill allocation ever written.
func TestAgainstRefMustNameTheBillItSettles(t *testing.T) {
	pool, ctx, _, _ := setup(t)
	led := ledger.NewService(pool)
	party, sales := billParties(t, pool, ctx)
	bank := ensureBank(t, pool, ctx)

	// NEGATIVE: an AGAINST with no name is refused, BY NAME.
	in := receiptAgainst(party, bank, "", "2026-07-02", "5000.0000")
	_, err := led.PostVoucher(ctx, in)
	if err == nil {
		t.Fatal("an unnamed AGAINST was accepted; it would have opened a second " +
			"bill under this voucher's own number instead of settling anything")
	}
	if !strings.Contains(err.Error(), "must name the bill it settles") {
		t.Fatalf("refusal does not explain itself: %v", err)
	}

	// POSITIVE CONTROL, on the same shape: an unnamed NEW is fine. This is what
	// stops the assertion above from passing on a blanket refusal.
	if _, err := led.PostVoucher(ctx, ledger.VoucherInput{
		Class: ledger.Sales, PartyLedgerID: party, VoucherDate: "2026-06-19",
		Party: "party", CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: party, DrCr: ledger.Debit, Amount: dec("100")},
			{LedgerID: sales, DrCr: ledger.Credit, Amount: dec("100")},
		},
		BillAllocations: []ledger.BillAllocationInput{
			{LedgerID: party, RefType: ledger.RefNew, DrCr: ledger.Debit, Amount: dec("100")},
		},
	}); err != nil {
		t.Fatalf("an unnamed NEW ref must be accepted, got: %v", err)
	}
}

// TestBillRefsMustAccountForWhatTheLedgerPosts proves the sum-to-posting
// invariant is ENFORCED and not merely declared.
//
// The failure it prevents is the quietest one in this feature: Outstandings is
// built from bill_allocation and Sundry Debtors from ledger_balance, so a
// reference short by any amount leaves two reports over the same books
// disagreeing, with every voucher balancing and every total footing.
//
// ⛔ THREE CASES, and the third is the one a client bug actually produces.
func TestBillRefsMustAccountForWhatTheLedgerPosts(t *testing.T) {
	pool, ctx, _, _ := setup(t)
	led := ledger.NewService(pool)
	party, sales := billParties(t, pool, ctx)

	// A sale of 10,000 whose bill reference claims only 9,000.
	short := func(refAmount string, refLedger uuid.UUID) error {
		_, err := led.PostVoucher(ctx, ledger.VoucherInput{
			Class: ledger.Sales, PartyLedgerID: party, VoucherDate: "2026-06-19",
			Party: "party", CreatedBy: "tester",
			Postings: []ledger.PostingInput{
				{LedgerID: party, DrCr: ledger.Debit, Amount: dec("10000")},
				{LedgerID: sales, DrCr: ledger.Credit, Amount: dec("10000")},
			},
			BillAllocations: []ledger.BillAllocationInput{
				{LedgerID: refLedger, RefType: ledger.RefNew, DrCr: ledger.Debit, Amount: dec(refAmount)},
			},
		})
		return err
	}

	// 1. SHORT by 1,000 — the classic client arithmetic bug.
	err := short("9000", party)
	if err == nil {
		t.Fatal("a bill reference 1,000 short of its posting was accepted")
	}
	if !strings.Contains(err.Error(), "the difference is 1000.0000") {
		t.Fatalf("refusal must state the difference, got: %v", err)
	}

	// 2. A PAISA over — proves there is no tolerance. Tolerances are how a real
	//    error hides for a year.
	if err := short("10000.0001", party); err == nil {
		t.Fatal("a bill reference over by one paisa was accepted; the check has a tolerance")
	}

	// 3. Attached to the WRONG LEDGER — the sales account rather than the debtor.
	//    This is what an off-by-one over a postings array produces, and it is the
	//    shape that cost allocations already guard against.
	err = short("10000", sales)
	if err == nil {
		t.Fatal("a bill reference on the SALES ledger was accepted; the receivable " +
			"would be aged against an income account")
	}
	if !strings.Contains(err.Error(), "posts") {
		t.Fatalf("refusal should name the mismatch, got: %v", err)
	}

	// POSITIVE CONTROL: the exact same body, correctly totalled, is accepted.
	// Without it every assertion above is consistent with "all bills refused".
	if err := short("10000", party); err != nil {
		t.Fatalf("a correctly-totalled bill reference must be accepted, got: %v", err)
	}
}

// receiptAgainst builds a receipt settling `ref` for `amount`: bank Dr, party Cr.
func receiptAgainst(party, bank uuid.UUID, ref, date, amount string) ledger.VoucherInput {
	amt := dec(amount)
	return ledger.VoucherInput{
		Class:         ledger.Receipt,
		PartyLedgerID: party,
		VoucherDate:   date,
		Party:         "party",
		CreatedBy:     "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: bank, DrCr: ledger.Debit, Amount: amt},
			{LedgerID: party, DrCr: ledger.Credit, Amount: amt},
		},
		BillAllocations: []ledger.BillAllocationInput{
			{
				LedgerID: party, RefName: ref, RefType: ledger.RefAgainst,
				DrCr: ledger.Credit, Amount: amt,
			},
		},
	}
}

func ensureBank(t *testing.T, pool *db.Pool, ctx context.Context) uuid.UUID {
	t.Helper()
	id, err := masters.NewService(pool).EnsureLedger(ctx, "Bill Bank", "Bank Accounts", "tester")
	if err != nil {
		t.Fatalf("bank ledger: %v", err)
	}
	return id
}
