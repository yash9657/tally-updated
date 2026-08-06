package ledger

// class_label_test.go — the voucher CLASS LABEL is a wire contract, and this
// file is where it is pinned.
//
// ⛔⛔ WHY A TEST AND NOT A COMMENT. `Class` is a bare `string` type, and its
// value is what `post()` writes into the version payload's `class` field. That
// string leaves the server in two places an operator can reach —
// `DayBookEntry.class` and `VoucherView.class` — and since the Day Book DRILL
// began routing on it (frontend/src/keystroke/voucherClass.ts), the exact bytes
// decide whether an operator can open a voucher at all.
//
// The browser holds its own copy of this table, because it must: it has to map a
// label back to the voucher screen that can render it. That copy is
// `VOUCHER_CLASS_LABELS` in frontend/src/api/types.ts. Renaming a constant here
// is a two-line change that compiles cleanly, passes every Go test, and then
// makes the drill a silent no-op for that one class in the browser — a defect
// with no error message anywhere. This test is the tripwire: it fails BY NAME and
// names the file to change.
//
// ⚠️ It deliberately writes the labels out as literals rather than deriving them
// from the constants. A test that asserted `string(Sales) == string(Sales)`
// cannot fail; the literal is the whole point (§9.2).

import "testing"

func TestClassLabelsAreTheWireContract(t *testing.T) {
	// ⛔ IF YOU CHANGE A STRING HERE, CHANGE `VOUCHER_CLASS_LABELS` IN
	// frontend/src/api/types.ts IN THE SAME COMMIT. The browser maps the label
	// back to a voucher screen; a label only the server knows opens nothing.
	want := map[Class]string{
		Contra:        "Contra",
		Payment:       "Payment",
		Receipt:       "Receipt",
		Journal:       "Journal",
		Sales:         "Sales",
		Purchase:      "Purchase",
		CreditNote:    "Credit Note",
		DebitNote:     "Debit Note",
		DeliveryNote:  "Delivery Note",
		ReceiptNote:   "Receipt Note",
		StockJournal:  "Stock Journal",
		PhysicalStock: "Physical Stock",
	}
	for c, label := range want {
		if string(c) != label {
			t.Errorf("class label drifted: got %q, want %q — update VOUCHER_CLASS_LABELS in frontend/src/api/types.ts too", string(c), label)
		}
	}

	// THE COMPLETENESS HALF, and without it the map above is worth little: a
	// class added to validClasses but not to `want` would be unpinned, which is
	// the exact case the browser cannot render. Every POSTABLE class must be
	// listed. (Payroll is postable but has no browser entry screen at all — see
	// below — so it is excluded by name rather than by omission.)
	for c := range validClasses {
		if c == Payroll {
			continue
		}
		if _, ok := want[c]; !ok {
			t.Errorf("class %q is postable but unpinned here — add it, and add it to VOUCHER_CLASS_LABELS in frontend/src/api/types.ts", string(c))
		}
	}

	// ⚠️ THE CLASSES WITH NO BROWSER ENTRY SCREEN, stated so the asymmetry is a
	// decision and not an oversight. A Day Book row of one of these publishes an
	// UNPREFIXED row key, which the navigation machine refuses to drill — which
	// is right: opening a Payroll voucher in a Sales form and pressing Ctrl+A is
	// how a pay run becomes a sales invoice.
	//
	// Payroll IS postable (payroll_class.go) and has no voucher screen; Sales
	// Order and Purchase Order are named-but-not-postable and never appear in the
	// Day Book at all.
	for _, c := range []Class{Payroll, SalesOrder, PurchaseOrder} {
		if _, ok := want[c]; ok {
			t.Errorf("class %q has no browser entry screen, so pinning it here claims a mapping that does not exist", string(c))
		}
	}
	if !validClasses[Payroll] {
		t.Error("Payroll is no longer postable — the exclusion above is now dead and should be removed")
	}
	if validClasses[SalesOrder] || validClasses[PurchaseOrder] {
		t.Error("an ORDER class became postable: it can now reach the Day Book, so it needs a browser entry screen or an explicit refusal")
	}
}
