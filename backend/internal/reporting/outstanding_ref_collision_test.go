package reporting_test

// TWO BILLS THAT WERE SILENTLY ONE — the driven half of the ref-naming fix.
//
// ⛔⛔ WHAT WAS WRONG, AND IT WAS ONE LINE. post() stamped the allocated voucher
// number onto EVERY reference that arrived unnamed, with no uniqueness check of
// any kind, and reporting.Outstandings identifies a bill by (ledger_id, ref_name)
// while ignoring ref_type entirely. So the name IS the bill, and a name that is
// not unique per party is a merge waiting to happen. Two of them were DRIVEN in a
// browser:
//
//   - an 11,800 invoice split 5,900 at 30 days and 5,900 at 60 days became ONE
//     bill of 11,800, aged entirely at the 30-day date. The 60-day instalment did
//     not exist on any report anybody could chase.
//
//   - a Receipt's ON-ACCOUNT amount was stamped with the RECEIPT's number, and
//     every voucher type has its OWN bare-integer counter per financial year, so
//     Receipt "1" NETTED against Sales invoice "1" on the same party: the invoice
//     showed as part paid and the unapplied money showed as applied. Both figures
//     wrong, and the report still footed.
//
// ⚠️ EVERY TEST HERE ASSERTS NAMES, SIDES AND VALUES — never a total on its own.
// A merged bill and two separate bills FOOT TO THE SAME PAISA, which is exactly
// why this survived every gate: the grand total was right the whole time. Each
// test also ties Outstandings to the Balance Sheet's Sundry Debtors, which is
// computed from ledger_balance rather than from bill_allocation, so agreement
// between them is a genuine cross-check rather than one query reformatted.

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/reporting"
)

// billsByName reads the party's bill-wise breakup out of the Receivables report,
// keyed by the name the report identifies each bill by.
func billsByName(t *testing.T, svc *reporting.Service, ctx context.Context, companyID uuid.UUID) map[string]reporting.Bill {
	t.Helper()
	os, err := svc.Outstandings(ctx, companyID, reporting.Receivables, "", nil)
	if err != nil {
		t.Fatalf("outstandings: %v", err)
	}
	out := make(map[string]reporting.Bill)
	for _, p := range os.Parties {
		for _, b := range p.Bills {
			if _, dup := out[b.RefName]; dup {
				t.Fatalf("the report returned two rows named %q", b.RefName)
			}
			out[b.RefName] = b
		}
	}
	return out
}

// splitSale posts ONE credit sale of 11,800 carrying TWO unnamed NEW references —
// the 30/60-day instalment split, exactly the body the F8 form sends when an
// operator adds a second reference and gives each one a due date.
func splitSale(t *testing.T, led *ledger.Service, ctx context.Context, party, sales uuid.UUID) string {
	t.Helper()
	res, err := led.PostVoucher(ctx, ledger.VoucherInput{
		Class:         ledger.Sales,
		PartyLedgerID: party,
		VoucherDate:   "2026-05-20",
		Party:         "party",
		CreatedBy:     "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: party, DrCr: ledger.Debit, Amount: dec("11800")},
			{LedgerID: sales, DrCr: ledger.Credit, Amount: dec("11800")},
		},
		BillAllocations: []ledger.BillAllocationInput{
			{LedgerID: party, RefType: ledger.RefNew, DrCr: ledger.Debit,
				Amount: dec("5900"), DueDate: "2026-06-19"},
			{LedgerID: party, RefType: ledger.RefNew, DrCr: ledger.Debit,
				Amount: dec("5900"), DueDate: "2026-07-19"},
		},
	})
	if err != nil {
		t.Fatalf("post split sale: %v", err)
	}
	return res.VoucherNo
}

// TestAnInvoiceSplitIntoTwoInstalmentsIsTwoBills is D10, driven through the post
// path and read back off the report.
func TestAnInvoiceSplitIntoTwoInstalmentsIsTwoBills(t *testing.T) {
	pool, ctx, companyID, _ := setup(t)
	led := ledger.NewService(pool)
	svc := reporting.NewService(pool)
	party, sales := billParties(t, pool, ctx)

	splitSale(t, led, ctx, party, sales)

	bills := billsByName(t, svc, ctx, companyID)
	if len(bills) != 2 {
		t.Fatalf("want TWO bills, got %d: %+v — one row means both instalments were "+
			"stamped with the same voucher number and the report merged them", len(bills), bills)
	}

	// ⛔ THE DUE DATES ARE THE POINT, NOT THE COUNT. The merge was invisible in the
	// total and visible only in the ageing: MIN(due_date) took the whole 11,800 to
	// the 30-day date. Assert BOTH dates survive, each carrying its own 5,900.
	dates := map[string]decimal.Decimal{}
	for _, b := range bills {
		if b.DueDate == "" {
			t.Fatalf("bill %q lost its due date, so it can never become overdue", b.RefName)
		}
		if prev, dup := dates[b.DueDate]; dup {
			t.Fatalf("two bills both due %s (%s, %s)", b.DueDate, prev, b.Pending)
		}
		dates[b.DueDate] = b.Pending
		if want := dec("5900"); !b.Pending.Equal(want) {
			t.Fatalf("bill %q pending = %s, want %s", b.RefName, b.Pending, want)
		}
		// SIDE, not just size: a receivable still open is POSITIVE on the
		// receivable side. A negative here would be money we owe, rendered under a
		// caption that says the opposite.
		if b.Pending.IsNegative() {
			t.Fatalf("bill %q is negative (%s) on the RECEIVABLE report", b.RefName, b.Pending)
		}
	}
	for _, due := range []string{"2026-06-19", "2026-07-19"} {
		if _, ok := dates[due]; !ok {
			t.Fatalf("no bill is due %s; the report holds %+v", due, dates)
		}
	}
	// And the instalments age DIFFERENTLY, which is the operator-visible fact the
	// merge destroyed.
	if bills0, bills1 := overdueSpread(bills); bills0 == bills1 {
		t.Fatalf("both instalments age identically (%d days), so the split changed nothing", bills0)
	}

	// The independent oracle: ledger_balance, not bill_allocation.
	os, err := svc.Outstandings(ctx, companyID, reporting.Receivables, "", nil)
	if err != nil {
		t.Fatalf("outstandings: %v", err)
	}
	if bs := debtorsFromBalanceSheet(t, svc, ctx, companyID); !bs.Equal(os.Total) {
		t.Fatalf("Balance Sheet Sundry Debtors %s != Outstandings %s", bs, os.Total)
	}
}

// overdueSpread returns the two bills' OverdueDays in map-iteration order; it
// exists so the assertion above reads as one line.
func overdueSpread(bills map[string]reporting.Bill) (int, int) {
	days := make([]int, 0, 2)
	for _, b := range bills {
		days = append(days, b.OverdueDays)
	}
	if len(days) != 2 {
		return 0, 0
	}
	return days[0], days[1]
}

// TestAnOnAccountReceiptDoesNotNetAgainstALikeNumberedInvoice is D11, and the
// numbering collision it depends on is ASSERTED rather than assumed: if the
// Sales and the Receipt series ever stop agreeing on "1", this test would go on
// passing while proving nothing at all.
func TestAnOnAccountReceiptDoesNotNetAgainstALikeNumberedInvoice(t *testing.T) {
	pool, ctx, companyID, _ := setup(t)
	led := ledger.NewService(pool)
	svc := reporting.NewService(pool)
	party, sales := billParties(t, pool, ctx)
	bank := ensureBank(t, pool, ctx)

	invoiceNo := unnamedSale(t, led, ctx, party, sales, "2026-06-19", "94400.0000")

	// The receipt: 50,000 from the customer, applied to NO bill. Its reference is
	// unnamed, exactly as the settle picker sends it when the operator chooses
	// "On Account" instead of naming a bill.
	res, err := led.PostVoucher(ctx, ledger.VoucherInput{
		Class:         ledger.Receipt,
		PartyLedgerID: party,
		VoucherDate:   "2026-07-02",
		Party:         "party",
		CreatedBy:     "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: bank, DrCr: ledger.Debit, Amount: dec("50000")},
			{LedgerID: party, DrCr: ledger.Credit, Amount: dec("50000")},
		},
		BillAllocations: []ledger.BillAllocationInput{
			{LedgerID: party, RefType: ledger.RefOnAccount, DrCr: ledger.Credit, Amount: dec("50000")},
		},
	})
	if err != nil {
		t.Fatalf("post on-account receipt: %v", err)
	}
	// ⛔ THE PRECONDITION, ASSERTED. Each voucher TYPE has its own counter per
	// financial year, so the first Sales and the first Receipt are BOTH "1". That
	// coincidence is the entire defect; without it this test is theatre.
	if res.VoucherNo != invoiceNo {
		t.Fatalf("the receipt numbered %q and the invoice %q — the per-type counters "+
			"no longer collide, so this test no longer reproduces the defect",
			res.VoucherNo, invoiceNo)
	}

	bills := billsByName(t, svc, ctx, companyID)
	if len(bills) != 2 {
		t.Fatalf("want the invoice AND a separate on-account bucket, got %+v", bills)
	}
	// The invoice is UNTOUCHED. This is the assertion the old code failed: it read
	// 44,400 there, and the total was right either way.
	var invoice reporting.Bill
	for name, b := range bills {
		if name != "On Account" {
			invoice = b
		}
	}
	if want := dec("94400"); !invoice.Pending.Equal(want) {
		t.Fatalf("the invoice %q is at %s, want %s — an on-account receipt was "+
			"applied to a bill it never named", invoice.RefName, invoice.Pending, want)
	}
	onAcc, ok := bills["On Account"]
	if !ok {
		t.Fatalf("no On Account bucket; the report holds %+v", bills)
	}
	// ⛔ SIDE. Unapplied money from a customer REDUCES what they owe, so it is
	// negative on the receivable side. A positive here would report money we
	// received as money still owed to us.
	if want := dec("-50000"); !onAcc.Pending.Equal(want) {
		t.Fatalf("On Account = %s, want %s", onAcc.Pending, want)
	}

	os, err := svc.Outstandings(ctx, companyID, reporting.Receivables, "", nil)
	if err != nil {
		t.Fatalf("outstandings: %v", err)
	}
	if want := dec("44400"); !os.Total.Equal(want) {
		t.Fatalf("party total = %s, want %s", os.Total, want)
	}
	if bs := debtorsFromBalanceSheet(t, svc, ctx, companyID); !bs.Equal(os.Total) {
		t.Fatalf("Balance Sheet Sundry Debtors %s != Outstandings %s", bs, os.Total)
	}
}

// TestALikeNumberedCreditNoteOpensItsOwnBill is the SAME class as the on-account
// collision and the reason the fix could not stop at ON_ACCOUNT: the raise-mode
// picker defaults a Credit Note's reference to NEW, and the Credit Note series
// starts at "1" beside the Sales series.
func TestALikeNumberedCreditNoteOpensItsOwnBill(t *testing.T) {
	pool, ctx, companyID, _ := setup(t)
	led := ledger.NewService(pool)
	svc := reporting.NewService(pool)
	party, sales := billParties(t, pool, ctx)

	invoiceNo := unnamedSale(t, led, ctx, party, sales, "2026-06-19", "11800.0000")

	res, err := led.PostVoucher(ctx, ledger.VoucherInput{
		Class:         ledger.CreditNote,
		PartyLedgerID: party,
		VoucherDate:   "2026-07-05",
		Party:         "party",
		CreatedBy:     "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: party, DrCr: ledger.Credit, Amount: dec("1180")},
			{LedgerID: sales, DrCr: ledger.Debit, Amount: dec("1180")},
		},
		BillAllocations: []ledger.BillAllocationInput{
			{LedgerID: party, RefType: ledger.RefNew, DrCr: ledger.Credit, Amount: dec("1180")},
		},
	})
	if err != nil {
		t.Fatalf("post credit note: %v", err)
	}
	if res.VoucherNo != invoiceNo {
		t.Fatalf("the note numbered %q and the invoice %q — the per-type counters no "+
			"longer collide, so this test no longer reproduces the defect",
			res.VoucherNo, invoiceNo)
	}

	bills := billsByName(t, svc, ctx, companyID)
	if len(bills) != 2 {
		t.Fatalf("want the invoice AND the note's own credit, got %+v", bills)
	}
	for name, b := range bills {
		switch {
		case b.Pending.Equal(dec("11800")):
			continue
		case b.Pending.Equal(dec("-1180")):
			continue
		default:
			t.Fatalf("bill %q is at %s; want the invoice at 11800 and the note at -1180 "+
				"(10620 in one row means the note was merged into the invoice)", name, b.Pending)
		}
	}

	os, err := svc.Outstandings(ctx, companyID, reporting.Receivables, "", nil)
	if err != nil {
		t.Fatalf("outstandings: %v", err)
	}
	if bs := debtorsFromBalanceSheet(t, svc, ctx, companyID); !bs.Equal(os.Total) {
		t.Fatalf("Balance Sheet Sundry Debtors %s != Outstandings %s", bs, os.Total)
	}
}

// TestAnAlterThatRestatesUnnamedRefsKeepsTheSAMEBill pins the property that made
// a deterministic name mandatory rather than tidy: bill_allocation is keyed on
// voucher_version_id, so an ALTER re-states. A name minted from a uuid or a clock
// would open a fresh bill on every edit and leave the original outstanding for
// ever, with the party's total silently doubling.
func TestAnAlterThatRestatesUnnamedRefsKeepsTheSAMEBill(t *testing.T) {
	pool, ctx, companyID, _ := setup(t)
	led := ledger.NewService(pool)
	svc := reporting.NewService(pool)
	party, sales := billParties(t, pool, ctx)

	res, err := led.PostVoucher(ctx, ledger.VoucherInput{
		Class:         ledger.Sales,
		PartyLedgerID: party,
		VoucherDate:   "2026-06-19",
		Party:         "party",
		CreatedBy:     "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: party, DrCr: ledger.Debit, Amount: dec("11800")},
			{LedgerID: sales, DrCr: ledger.Credit, Amount: dec("11800")},
		},
		BillAllocations: []ledger.BillAllocationInput{
			{LedgerID: party, RefType: ledger.RefNew, DrCr: ledger.Debit,
				Amount: dec("11800"), DueDate: "2026-07-19"},
		},
	})
	if err != nil {
		t.Fatalf("post sale: %v", err)
	}
	before := billsByName(t, svc, ctx, companyID)

	// The same voucher, edited, with the reference sent UNNAMED again.
	if _, err := led.AlterVoucher(ctx, res.VoucherID, ledger.VoucherInput{
		Class:         ledger.Sales,
		PartyLedgerID: party,
		VoucherDate:   "2026-06-19",
		Narration:     "typo fixed",
		Party:         "party",
		CreatedBy:     "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: party, DrCr: ledger.Debit, Amount: dec("11800")},
			{LedgerID: sales, DrCr: ledger.Credit, Amount: dec("11800")},
		},
		BillAllocations: []ledger.BillAllocationInput{
			{LedgerID: party, RefType: ledger.RefNew, DrCr: ledger.Debit,
				Amount: dec("11800"), DueDate: "2026-07-19"},
		},
	}); err != nil {
		t.Fatalf("alter sale: %v", err)
	}

	after := billsByName(t, svc, ctx, companyID)
	if len(after) != 1 {
		t.Fatalf("the alter left %d bills: %+v — a non-deterministic name opened a "+
			"second one and left the first outstanding", len(after), after)
	}
	for name := range before {
		if _, ok := after[name]; !ok {
			t.Fatalf("the bill was renamed by an alter (%q -> %+v)", name, after)
		}
	}
}

// ⛔⛔ THE THIRD DOOR, AND IT IS A KEYSTROKE: TWO INVOICES MANUALLY GIVEN ONE
// NUMBER. "Sales/2026-27/1" is unique only if class + financial year + number name
// one voucher, and numbering.go says in its own words that a client-supplied
// voucher_no is honoured verbatim and does NOT bump the counter. VoucherScreen
// sends that field (`if (f.voucherNo) req.voucher_no = f.voucherNo`), so an
// operator matching a paper book types 1, the auto series later reaches 1, and two
// invoices to one customer become ONE bill of the summed amount aged at the
// earlier due date — exactly D10's shape, arriving through the numbering rather
// than through a split.
//
// The same collision arrives a second way: voucher_type_id is on the wire and each
// TYPE has its own counter, so two types of class Sales both mint a 1.
//
// resolveGeneratedBillRefNames closes it by resolving the names this server
// INVENTS against what the party's bills are already called. A name a CALLER
// supplied is deliberately left alone — see TestAVoucherCannotOpenTwoBillsOfTheSameName
// for the within-one-voucher latch, and the function's comment for why repeating a
// name ACROSS vouchers stays legal (the Tally XML importer depends on it).
func TestASecondInvoiceManuallyGivenTheFirstsNumberIsStillItsOwnBill(t *testing.T) {
	pool, ctx, companyID, _ := setup(t)
	led := ledger.NewService(pool)
	svc := reporting.NewService(pool)
	party, sales := billParties(t, pool, ctx)

	// The first invoice takes the auto series.
	autoNo := unnamedSale(t, led, ctx, party, sales, "2026-06-19", "11800.0000")

	// The second is keyed with the SAME number by hand — the paper-book habit.
	manual, err := led.PostVoucher(ctx, ledger.VoucherInput{
		Class:         ledger.Sales,
		PartyLedgerID: party,
		VoucherNo:     autoNo,
		VoucherDate:   "2026-06-25",
		Party:         "party",
		CreatedBy:     "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: party, DrCr: ledger.Debit, Amount: dec("5000")},
			{LedgerID: sales, DrCr: ledger.Credit, Amount: dec("5000")},
		},
		BillAllocations: []ledger.BillAllocationInput{
			{LedgerID: party, RefType: ledger.RefNew, DrCr: ledger.Debit,
				Amount: dec("5000"), DueDate: "2026-09-30"},
		},
	})
	if err != nil {
		t.Fatalf("post the manually-numbered invoice: %v", err)
	}
	// ⛔ THE PRECONDITION, ASSERTED. If the manual override ever starts bumping the
	// counter this test would go on passing while reproducing nothing.
	if manual.VoucherNo != autoNo {
		t.Fatalf("the second invoice numbered %q and the first %q — a manual number "+
			"no longer collides with the auto series, so this test proves nothing",
			manual.VoucherNo, autoNo)
	}

	bills := billsByName(t, svc, ctx, companyID)
	if len(bills) != 2 {
		t.Fatalf("want TWO bills, got %d: %+v — one row means the server minted the "+
			"same name twice and the report merged two unrelated invoices", len(bills), bills)
	}
	// PER-BILL AMOUNTS AND SIDES, never the total: 16,800 in one row and 16,800 in
	// two rows foot identically, which is why this class survives every gate.
	byAmount := map[string]reporting.Bill{}
	for _, b := range bills {
		if b.Pending.IsNegative() {
			t.Fatalf("bill %q is negative (%s) on the RECEIVABLE report", b.RefName, b.Pending)
		}
		byAmount[b.Pending.String()] = b
	}
	first, ok := byAmount["11800"]
	if !ok {
		t.Fatalf("the 11,800 invoice is not a bill of its own: %+v", bills)
	}
	second, ok := byAmount["5000"]
	if !ok {
		t.Fatalf("the 5,000 invoice is not a bill of its own: %+v", bills)
	}
	// unnamedSale dates the bill on the voucher date; the manual one is dated out
	// to September, so a merge would have aged all 16,800 at the June date.
	if first.DueDate != "2026-06-19" || second.DueDate != "2026-09-30" {
		t.Fatalf("the due dates did not survive: 11800 due %q, 5000 due %q",
			first.DueDate, second.DueDate)
	}
	// Both names must still say which voucher they came from, or the disambiguation
	// has traded a merge for an unreadable report.
	for _, b := range []reporting.Bill{first, second} {
		if !strings.Contains(b.RefName, autoNo) {
			t.Fatalf("bill %q no longer carries the voucher number %q", b.RefName, autoNo)
		}
	}

	// The independent oracle: ledger_balance, not bill_allocation.
	os, err := svc.Outstandings(ctx, companyID, reporting.Receivables, "", nil)
	if err != nil {
		t.Fatalf("outstandings: %v", err)
	}
	if bs := debtorsFromBalanceSheet(t, svc, ctx, companyID); !bs.Equal(os.Total) {
		t.Fatalf("Balance Sheet Sundry Debtors %s != Outstandings %s", bs, os.Total)
	}

	// ⛔ AND THE DISAMBIGUATED NAME MUST SURVIVE AN ALTER. bill_allocation is keyed
	// on voucher_version_id, so an alter RE-STATES: if the resolution were not
	// stable the edited invoice would open a THIRD bill and leave its own previous
	// one outstanding beside it, doubling what the customer appears to owe.
	if _, err := led.AlterVoucher(ctx, manual.VoucherID, ledger.VoucherInput{
		Class:         ledger.Sales,
		PartyLedgerID: party,
		VoucherNo:     autoNo,
		VoucherDate:   "2026-06-25",
		Narration:     "typo fixed",
		Party:         "party",
		CreatedBy:     "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: party, DrCr: ledger.Debit, Amount: dec("5000")},
			{LedgerID: sales, DrCr: ledger.Credit, Amount: dec("5000")},
		},
		BillAllocations: []ledger.BillAllocationInput{
			{LedgerID: party, RefType: ledger.RefNew, DrCr: ledger.Debit,
				Amount: dec("5000"), DueDate: "2026-09-30"},
		},
	}); err != nil {
		t.Fatalf("alter the manually-numbered invoice: %v", err)
	}
	after := billsByName(t, svc, ctx, companyID)
	if len(after) != 2 {
		t.Fatalf("the alter left %d bills: %+v — the disambiguated name is not stable, "+
			"so every edit opens a fresh bill", len(after), after)
	}
	for name := range bills {
		if _, ok := after[name]; !ok {
			t.Fatalf("the alter renamed %q; the report now holds %+v", name, after)
		}
	}
}

// ⛔ THE SAME DOOR, FROM THE OTHER SOURCE THE REPORT UNIONS IN. Outstandings reads
// bill_allocation AND ledger_opening_bill into one (ledger, ref_name) space, so a
// generated name that lands on an OPENING reference nets a fresh invoice against
// the party's brought-forward balance. The opening breakup is hand-keyed, so a
// name of exactly this shape is unlikely — and "unlikely" is not a guard, which is
// why the resolver's query covers both tables.
func TestAGeneratedNameNeverLandsOnTheOPENINGBreakup(t *testing.T) {
	pool, ctx, companyID, _ := setup(t)
	led := ledger.NewService(pool)
	svc := reporting.NewService(pool)
	ms := masters.NewService(pool)

	// A debtor carried in owing one bill that is ALREADY called what the first
	// Sales invoice of 2026-27 will be called.
	party, err := ms.CreateLedger(ctx, masters.Ledger{
		Name: "Collision Carried In Co", GroupName: "Sundry Debtors",
		OpeningBalance: dec("7000"), OpeningDC: "D",
		MaintainBillByBill: true,
		OpeningBills: []masters.OpeningBill{
			{RefName: "Sales/2026-27/1", Amount: dec("7000"), DueDate: "2026-05-05"},
		},
	}, "tester")
	if err != nil {
		t.Fatalf("create party with opening: %v", err)
	}
	sales, err := ms.EnsureLedger(ctx, "Collision Sales", "Sales Accounts", "tester")
	if err != nil {
		t.Fatalf("sales ledger: %v", err)
	}

	no := unnamedSale(t, led, ctx, party, sales, "2026-06-19", "11800.0000")
	if no != "1" {
		t.Fatalf("the first Sales voucher of 2026-27 numbered %q, not 1 — the fixture "+
			"no longer collides with the opening reference it was built to collide with", no)
	}

	os, err := svc.Outstandings(ctx, companyID, reporting.Receivables, "", nil)
	if err != nil {
		t.Fatalf("outstandings: %v", err)
	}
	if len(os.Parties) != 1 {
		t.Fatalf("want one party, got %+v", os.Parties)
	}
	bills := os.Parties[0].Bills
	if len(bills) != 2 {
		t.Fatalf("want the opening bill AND the invoice, got %+v — one row means the "+
			"invoice was netted into the balance the customer was carried in owing", bills)
	}
	var opening, invoice reporting.Bill
	for _, b := range bills {
		switch b.Pending.String() {
		case "7000":
			opening = b
		case "11800":
			invoice = b
		default:
			t.Fatalf("unexpected bill %q at %s; want 7000 opening and 11800 invoice",
				b.RefName, b.Pending)
		}
	}
	if opening.RefName != "Sales/2026-27/1" {
		t.Fatalf("the OPENING reference was renamed to %q; only generated names may "+
			"move, and an opening reference is one a human wrote down", opening.RefName)
	}
	if invoice.RefName == opening.RefName {
		t.Fatalf("the invoice took the opening reference's name %q", invoice.RefName)
	}
	if opening.DueDate == invoice.DueDate {
		t.Fatalf("both bills are due %s, so the ageing cannot tell them apart", opening.DueDate)
	}
	if bs := debtorsFromBalanceSheet(t, svc, ctx, companyID); !bs.Equal(os.Total) {
		t.Fatalf("Balance Sheet Sundry Debtors %s != Outstandings %s", bs, os.Total)
	}
}

// TestAVoucherCannotOpenTwoBillsOfTheSameName is the latch on the door the stamp
// cannot reach: a CALLER (the sync sequencer, the seed script, an API client)
// naming two openers the same. It carries its positive control, without which the
// refusal is indistinguishable from refusing every bill reference ever written.
func TestAVoucherCannotOpenTwoBillsOfTheSameName(t *testing.T) {
	pool, ctx, _, _ := setup(t)
	led := ledger.NewService(pool)
	party, sales := billParties(t, pool, ctx)

	twoNamed := func(a, b string) error {
		_, err := led.PostVoucher(ctx, ledger.VoucherInput{
			Class:         ledger.Sales,
			PartyLedgerID: party,
			VoucherDate:   "2026-06-19",
			Party:         "party",
			CreatedBy:     "tester",
			Postings: []ledger.PostingInput{
				{LedgerID: party, DrCr: ledger.Debit, Amount: dec("11800")},
				{LedgerID: sales, DrCr: ledger.Credit, Amount: dec("11800")},
			},
			BillAllocations: []ledger.BillAllocationInput{
				{LedgerID: party, RefName: a, RefType: ledger.RefNew, DrCr: ledger.Debit,
					Amount: dec("5900"), DueDate: "2026-06-19"},
				{LedgerID: party, RefName: b, RefType: ledger.RefNew, DrCr: ledger.Debit,
					Amount: dec("5900"), DueDate: "2026-07-19"},
			},
		})
		return err
	}

	err := twoNamed("SPLIT-1", "SPLIT-1")
	if err == nil {
		t.Fatal("a voucher opening two bills named SPLIT-1 on one ledger was accepted; " +
			"the report would have shown one bill of 11,800 aged at the earlier date")
	}
	if !strings.Contains(err.Error(), "two bills named") {
		t.Fatalf("the refusal does not explain itself: %v", err)
	}

	// POSITIVE CONTROL: the same body with distinct names is accepted.
	if err := twoNamed("SPLIT-1", "SPLIT-2"); err != nil {
		t.Fatalf("two DIFFERENTLY named instalments must be accepted, got: %v", err)
	}
}
