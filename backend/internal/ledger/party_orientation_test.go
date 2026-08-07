package ledger

// party_orientation_test.go — THE ACCOUNTING-MODE COUNTERPART OF
// item_invoice_test.go's ORIENTATION ASSERTIONS.
//
// ===========================================================================
// ⛔⛔ WHY THIS FILE EXISTS, AND IT IS THE SAME DEFECT TWICE
// ===========================================================================
// item_invoice_test.go's header records that this project "shipped a fully
// REVERSED purchase once (WS8b)". That was fixed on the ITEM-INVOICE path, by
// generating the postings from `orientationFor`.
//
// It then shipped AGAIN, on the ACCOUNTING-MODE path, and was found by keying a
// purchase in a browser and reading `posting` in Postgres against the seeded
// controls (HANDOFF §5.5):
//
//	Purchase (seeded, CORRECT)   Cr supplier / Dr Purchases / Dr INPUT tax
//	Purchase (BROWSER-KEYED)     Dr supplier / Cr Purchases / Cr OUTPUT tax
//
// The two paths disagreed about whether the question mattered. The item path
// consulted the orientation table; the accounting path — the one a browser
// actually used for F9, Alt+F5 and Alt+F6 — consulted nothing, because a
// client-supplied posting set was taken as authoritative.
//
// ⛔ EVERY EXISTING CHECK IS BLIND TO IT BY CONSTRUCTION, and each of these was
// verified to still pass on the reversed voucher below:
//   - validatePostings only asks SUM(Dr) == SUM(Cr). Both orientations balance.
//   - deriveAndVerifyGST sums posting amounts WITHOUT reading dr_cr, and says so
//     in its own comment.
//   - the deferred DB trigger re-checks that same balance.
//
// So the assertions here are about SIDES, never sums. A test that counted "two
// debits and two credits", or that totalled by side, would pass on the defect.

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// accountingInvoice builds an ACCOUNTING-MODE (no inventory) GST voucher with
// client-supplied postings, which is what every browser-keyed F9 / Alt+F5 /
// Alt+F6 sends.
//
// `partySide` is a PARAMETER precisely so the reversed voucher can be built and
// shown to be refused — the defect is not expressible any other way.
func (e *itemEnv) accountingInvoice(
	class Class,
	party, base, cgst, sgst uuid.UUID,
	partySide, baseSide, taxSide DrCr,
) VoucherInput {
	return VoucherInput{
		Class:         class,
		PartyLedgerID: party,
		VoucherDate:   "2025-05-10",
		Party:         "a party",
		CreatedBy:     "test",
		Postings: []PostingInput{
			{LedgerID: party, DrCr: partySide, Amount: dec("1180")},
			{LedgerID: base, DrCr: baseSide, Amount: dec("1000")},
			{LedgerID: cgst, DrCr: taxSide, Amount: dec("90")},
			{LedgerID: sgst, DrCr: taxSide, Amount: dec("90")},
		},
	}
}

// TestAccountingModePurchaseIsRefusedWhenSalesShaped is the regression for the
// live defect. The body is EXACTLY what the browser used to send.
func TestAccountingModePurchaseIsRefusedWhenSalesShaped(t *testing.T) {
	e := setupItemInvoice(t)

	// ---- THE DEFECT: a purchase built with the sales shape.
	reversed := e.accountingInvoice(Purchase,
		e.supplier, e.purchases, e.ocgst, e.osgst,
		Debit, Credit, Credit)

	// ⛔ THE CONTROLS FIRST, so the refusal below cannot be credited to a
	// malformed body. This voucher is well-formed by every measure that existed
	// before it was refused.
	if err := validatePostings(reversed.Postings); err != nil {
		t.Fatalf("the reversed purchase must still BALANCE — otherwise this test "+
			"proves the balance check, not the orientation check: %v", err)
	}
	debit, credit := decimal.Zero, decimal.Zero
	for _, p := range reversed.Postings {
		if p.DrCr == Debit {
			debit = debit.Add(p.Amount)
		} else {
			credit = credit.Add(p.Amount)
		}
	}
	if !debit.Equal(credit) || !debit.Equal(dec("1180")) {
		t.Fatalf("control: reversed purchase should total 1180 per side, got Dr %s Cr %s",
			debit.StringFixed(4), credit.StringFixed(4))
	}

	_, err := e.led.PostVoucher(e.ctx, reversed)
	if err == nil {
		t.Fatal("a sales-shaped PURCHASE was accepted — this turns a payable into " +
			"a receivable and it balances, so nothing downstream will ever report it")
	}
	if !errors.Is(err, ErrPartyOrientation) {
		t.Fatalf("want ErrPartyOrientation, got %v", err)
	}
	// ⛔ ASSERT THE WORDING, not just the sentinel. A bare "invalid voucher"
	// sends an accountant looking at their own data entry for a defect that is
	// in the request shape.
	for _, want := range []string{"Purchase", "Cr", "payable", "receivable", "BALANCES"} {
		if !contains(err.Error(), want) {
			t.Errorf("refusal should mention %q, got: %v", want, err)
		}
	}
}

// TestAccountingModeCorrectOrientationsStillPost is the non-vacuity control for
// the whole file: the refusal must not be a blanket "accounting-mode invoices
// are refused".
//
// ⛔ ALL FOUR CLASSES, because the defect was a SHARED builder — fixing the one
// that was reported and leaving its three neighbours is the failure mode this
// repository has already had twice (see ensureOutputTaxLedgers' own header).
func TestAccountingModeCorrectOrientationsStillPost(t *testing.T) {
	e := setupItemInvoice(t)

	// ⛔ THE EXPECTED LEDGERS ARE NAMED, not resolved from the ids the case
	// itself supplied. Asserting `legs[c.party]` would be circular — it would
	// pass whatever was sent. item_invoice_test.go's header makes exactly this
	// point: only resolving ids back to NAMES can see a reversed voucher.
	cases := []struct {
		name                         string
		class                        Class
		party, base, cgst, sgst      uuid.UUID
		partyName, baseName          string
		cgstName, sgstName           string
		partySide, baseSide, taxSide DrCr
	}{
		{"sales", Sales, e.customer, e.sales, e.ocgst, e.osgst,
			nameCustomer, nameSales, nameOutCGST, nameOutSGST, Debit, Credit, Credit},
		// ⛔ INPUT tax, named. Output-for-Input balances and is the half of this
		// defect that survived the direction fix on the item path for two
		// releases: the tax was on the right SIDE and in the wrong ACCOUNT.
		{"purchase", Purchase, e.supplier, e.purchases, e.icgst, e.isgst,
			nameSupplier, namePurchases, nameInCGST, nameInSGST, Credit, Debit, Debit},
		// A sales return: the customer is credited, revenue and output tax given
		// back. The exact reverse of the sale above — and the tax stays OUTPUT,
		// because it is still tax on an outward supply.
		{"credit note", CreditNote, e.customer, e.sales, e.ocgst, e.osgst,
			nameCustomer, nameSales, nameOutCGST, nameOutSGST, Credit, Debit, Debit},
		// A purchase return: the supplier is debited and the INPUT credit
		// reversed. The exact reverse of the purchase above.
		{"debit note", DebitNote, e.supplier, e.purchases, e.icgst, e.isgst,
			nameSupplier, namePurchases, nameInCGST, nameInSGST, Debit, Credit, Credit},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := e.accountingInvoice(c.class, c.party, c.base, c.cgst, c.sgst,
				c.partySide, c.baseSide, c.taxSide)
			res, err := e.led.PostVoucher(e.ctx, in)
			if err != nil {
				t.Fatalf("a correctly-oriented %s must still post: %v", c.name, err)
			}
			// And it landed the way it was sent — the check refuses, it does not
			// silently rewrite.
			e.assertLegs(t, c.name, res.VoucherVersionID, map[string]postedLeg{
				c.partyName: {drcr: string(c.partySide), amount: dec("1180")},
				c.baseName:  {drcr: string(c.baseSide), amount: dec("1000")},
				c.cgstName:  {drcr: string(c.taxSide), amount: dec("90")},
				c.sgstName:  {drcr: string(c.taxSide), amount: dec("90")},
			})
		})
	}
}

// TestEveryGSTClassRefusesTheReversedParty walks all four, because the defect
// was one shared builder and a per-class fix is how three of them survive.
func TestEveryGSTClassRefusesTheReversedParty(t *testing.T) {
	e := setupItemInvoice(t)

	cases := []struct {
		name        string
		class       Class
		party, base uuid.UUID
		wrongParty  DrCr
	}{
		{"sales", Sales, e.customer, e.sales, Credit},
		{"purchase", Purchase, e.supplier, e.purchases, Debit},
		{"credit note", CreditNote, e.customer, e.sales, Debit},
		{"debit note", DebitNote, e.supplier, e.purchases, Credit},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// The other legs are flipped too, so the voucher still balances —
			// otherwise the balance check fires first and proves nothing.
			otherSide := Debit
			if c.wrongParty == Debit {
				otherSide = Credit
			}
			in := e.accountingInvoice(c.class, c.party, c.base, e.ocgst, e.osgst,
				c.wrongParty, otherSide, otherSide)
			if _, err := e.led.PostVoucher(e.ctx, in); !errors.Is(err, ErrPartyOrientation) {
				t.Fatalf("a reversed %s must be refused by ErrPartyOrientation, got %v", c.name, err)
			}
		})
	}
}

// TestNonInvoiceClassesKeepBothSides is the other half of the non-vacuity
// argument, and it guards a real feature rather than an edge case.
//
// ⛔ A RECEIPT, PAYMENT, CONTRA OR JOURNAL LEGITIMATELY PUTS THE PARTY ON EITHER
// SIDE — that is what those documents are FOR. A refusal that reached them would
// break the four most-used screens in the product, and it would do so only for
// the half of real entries that happen to go the "wrong" way, which is the kind
// of breakage that reaches production.
func TestNonInvoiceClassesKeepBothSides(t *testing.T) {
	e := setupItemInvoice(t)

	for _, class := range []Class{Receipt, Payment, Contra, Journal} {
		for _, side := range []DrCr{Debit, Credit} {
			other := Debit
			if side == Debit {
				other = Credit
			}
			in := VoucherInput{
				Class:         class,
				PartyLedgerID: e.customer,
				VoucherDate:   "2025-05-10",
				CreatedBy:     "test",
				Postings: []PostingInput{
					{LedgerID: e.customer, DrCr: side, Amount: dec("500")},
					{LedgerID: e.sales, DrCr: other, Amount: dec("500")},
				},
			}
			if _, err := e.led.PostVoucher(e.ctx, in); err != nil {
				t.Fatalf("%s with the party on %s must post: %v", class, side, err)
			}
		}
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		indexOf(haystack, needle) >= 0
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
