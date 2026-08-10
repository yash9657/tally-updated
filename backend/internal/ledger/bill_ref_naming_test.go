package ledger

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// THE NAME A BILL GETS WHEN NOBODY NAMES IT — the pure half.
//
// ⛔ WHY THIS DESERVES A UNIT TEST BESIDE THE DRIVEN ONES. reporting.Outstandings
// identifies a bill by (ledger_id, ref_name) and ignores ref_type entirely, so
// the generated name is not cosmetic: it IS the bill's identity. The DB-backed
// tests in internal/reporting drive the two collisions this naming closes end to
// end; these check the PROPERTY those tests can only sample — that no two bills
// a voucher opens can ever share a name, and that an on-account amount is never
// named after a voucher at all.
//
// The properties, and each is a door that was open:
//
//  1. two unnamed openers on ONE ledger get DIFFERENT names   (D10)
//  2. ON_ACCOUNT is never named from the numbering counter     (D11)
//  3. the name carries the class AND the financial year, because a bare voucher
//     number names one voucher PER TYPE PER YEAR                (D11)
//  4. it is DETERMINISTIC — an alter that re-sends unnamed references must land
//     on the same bill rather than opening a fresh one
//  5. a supplied name is never touched

func stampFixture(refs ...BillAllocationInput) []BillAllocationInput { return refs }

// vid is a stable voucher id for the fallback case.
var vid = uuid.MustParse("018f1234-5678-7abc-8def-0123456789ab")

func amt(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestStampNamesTwoUnnamedOpenersDIFFERENTLY(t *testing.T) {
	party := uuid.New()
	in := stampFixture(
		BillAllocationInput{LedgerID: party, RefType: RefNew, DrCr: Debit, Amount: amt("5900"), DueDate: "2026-07-19"},
		BillAllocationInput{LedgerID: party, RefType: RefNew, DrCr: Debit, Amount: amt("5900"), DueDate: "2026-08-18"},
	)
	got := stampBillRefNames(in, Sales, "7", "2026-06-19", vid)

	if got[0].RefName == got[1].RefName {
		t.Fatalf("both instalments were named %q — the Outstandings report groups by "+
			"(ledger, ref_name), so they would become ONE bill aged at the earlier due date",
			got[0].RefName)
	}
	for i, b := range got {
		if b.RefName == "" {
			t.Fatalf("reference %d was left unnamed", i)
		}
	}
	// ⛔ THE INPUT MUST BE UNTOUCHED. post() hashes the payload from the RETURNED
	// slice; if the stamp wrote through to the caller's array the hashed payload
	// and the caller's own copy could diverge in a future refactor.
	if in[0].RefName != "" || in[1].RefName != "" {
		t.Fatalf("the stamp mutated its input: %+v", in)
	}
}

// ⛔ THE CONTROL FOR THE TEST ABOVE. Two openers on DIFFERENT ledgers are two
// different parties' bills and never collide, so they must NOT be suffixed —
// otherwise "the names differ" would be satisfied by suffixing everything, and
// every ordinary invoice would carry a "-1" nobody asked for.
func TestStampDoesNotSuffixOpenersOnDifferentLedgers(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	got := stampBillRefNames(stampFixture(
		BillAllocationInput{LedgerID: a, RefType: RefNew, DrCr: Debit, Amount: amt("100")},
		BillAllocationInput{LedgerID: b, RefType: RefNew, DrCr: Debit, Amount: amt("100")},
	), Sales, "7", "2026-06-19", vid)

	if got[0].RefName != got[1].RefName {
		t.Fatalf("two parties' bills were given different names (%q, %q); they are "+
			"grouped per ledger and never collide", got[0].RefName, got[1].RefName)
	}
	if want := "Sales/2026-27/7"; got[0].RefName != want {
		t.Fatalf("ordinary single-bill name = %q, want %q", got[0].RefName, want)
	}
}

// ⛔ THE COLLISION THAT WAS DRIVEN: a Receipt #7's on-account amount netting
// against Sales invoice #7 on the same party. An on-account amount is BY
// DEFINITION applied to no bill, so it may not be named after a voucher.
func TestStampNeverNamesOnAccountAfterTheVoucher(t *testing.T) {
	party := uuid.New()
	got := stampBillRefNames(stampFixture(
		BillAllocationInput{LedgerID: party, RefType: RefOnAccount, DrCr: Credit, Amount: amt("50000")},
	), Receipt, "7", "2026-07-02", vid)

	if got[0].RefName != onAccountRefName {
		t.Fatalf("on-account reference named %q, want the %q bucket", got[0].RefName, onAccountRefName)
	}
	// And the sale it must not touch: the same number, a different class.
	sale := stampBillRefNames(stampFixture(
		BillAllocationInput{LedgerID: party, RefType: RefNew, DrCr: Debit, Amount: amt("94400")},
	), Sales, "7", "2026-06-19", vid)
	if sale[0].RefName == got[0].RefName {
		t.Fatalf("the on-account amount and the invoice share the name %q — "+
			"the report would show the invoice as part paid", sale[0].RefName)
	}
}

// ⛔ THE OTHER HALF OF THE SAME CLASS, and it is NOT on-account: every counter is
// keyed on (voucher type, financial year), so a bare number names one voucher per
// type per year. Three vouchers that all number "7" must produce three names.
func TestStampSeparatesLikeNumberedVouchersOfOtherTypesAndYears(t *testing.T) {
	party := uuid.New()
	one := func(class Class, date string) string {
		return stampBillRefNames(stampFixture(
			BillAllocationInput{LedgerID: party, RefType: RefNew, DrCr: Debit, Amount: amt("100")},
		), class, "7", date, vid)[0].RefName
	}
	sale := one(Sales, "2026-06-19")
	note := one(CreditNote, "2026-06-19")      // its own counter — also "7"
	nextYr := one(Sales, "2027-06-19")         // the series restarts every 1 April
	advance := stampBillRefNames(stampFixture( // an advance on a receipt, also "7"
		BillAllocationInput{LedgerID: party, RefType: RefAdvance, DrCr: Credit, Amount: amt("100")},
	), Receipt, "7", "2026-07-02", vid)[0].RefName

	seen := map[string]string{}
	for name, ref := range map[string]string{
		"sales": sale, "credit note": note, "next year's sales": nextYr, "advance": advance,
	} {
		if prev, dup := seen[ref]; dup {
			t.Fatalf("%s and %s are both named %q, so they are ONE bill", prev, name, ref)
		}
		seen[ref] = name
	}
	if sale != "Sales/2026-27/7" {
		t.Fatalf("sale bill = %q, want Sales/2026-27/7", sale)
	}
	if nextYr != "Sales/2027-28/7" {
		t.Fatalf("next year's sale bill = %q, want Sales/2027-28/7", nextYr)
	}
}

// DETERMINISM. An ALTER re-states the references; a caller that re-sends them
// unnamed must land on the SAME bill. A uuid or a timestamp in the name would
// open a fresh bill on every edit and leave the original outstanding forever.
func TestStampIsDeterministic(t *testing.T) {
	party := uuid.New()
	body := func() []BillAllocationInput {
		return stampFixture(
			BillAllocationInput{LedgerID: party, RefType: RefNew, DrCr: Debit, Amount: amt("5900")},
			BillAllocationInput{LedgerID: party, RefType: RefNew, DrCr: Debit, Amount: amt("5900")},
		)
	}
	first := stampBillRefNames(body(), Sales, "7", "2026-06-19", vid)
	again := stampBillRefNames(body(), Sales, "7", "2026-06-19", vid)
	for i := range first {
		if first[i].RefName != again[i].RefName {
			t.Fatalf("reference %d was named %q and then %q — an alter would open a "+
				"second bill and leave the first outstanding forever",
				i, first[i].RefName, again[i].RefName)
		}
	}
}

// A SUPPLIED NAME IS NEVER TOUCHED — the seed script, the sync sequencer and any
// settlement depend on it.
func TestStampLeavesSuppliedNamesAlone(t *testing.T) {
	party := uuid.New()
	got := stampBillRefNames(stampFixture(
		BillAllocationInput{LedgerID: party, RefName: "OB/ACME/24-25/07", RefType: RefAgainst,
			DrCr: Credit, Amount: amt("100")},
		BillAllocationInput{LedgerID: party, RefName: "PO-4471/partial", RefType: RefNew,
			DrCr: Debit, Amount: amt("100")},
	), Receipt, "7", "2026-07-02", vid)

	if got[0].RefName != "OB/ACME/24-25/07" || got[1].RefName != "PO-4471/partial" {
		t.Fatalf("a supplied name was overwritten: %+v", got)
	}
}

// ⚠️ THE FALLBACK, and it closes an EMPTY name rather than a duplicate one. A
// company with no system voucher type for the class gets no number at all
// (defaultVoucherTypeID returns nil without an error and numbering is skipped),
// and the old stamp then wrote ref_name = "" — which groups every such reference
// on a party into one nameless bill.
func TestStampFallsBackToTheVoucherIdWhenThereIsNoNumber(t *testing.T) {
	party := uuid.New()
	got := stampBillRefNames(stampFixture(
		BillAllocationInput{LedgerID: party, RefType: RefNew, DrCr: Debit, Amount: amt("100")},
	), Sales, "", "2026-06-19", vid)

	if got[0].RefName == "" {
		t.Fatal("an unnumbered voucher's bill was left nameless")
	}
	if want := "Sales/2026-27/#018f1234"; got[0].RefName != want {
		t.Fatalf("unnumbered bill = %q, want %q", got[0].RefName, want)
	}
	// A voucher with no date either: the FY segment drops out rather than
	// blocking the post.
	undated := stampBillRefNames(stampFixture(
		BillAllocationInput{LedgerID: party, RefType: RefNew, DrCr: Debit, Amount: amt("100")},
	), Sales, "7", "", vid)
	if want := "Sales/7"; undated[0].RefName != want {
		t.Fatalf("undated bill = %q, want %q", undated[0].RefName, want)
	}
}

// THE LATCH: a CALLER that names two openers the same is refused, because the
// report would silently make them one bill. Generated names cannot reach this —
// which is why it is a latch and not the fix.
func TestDistinctBillOpenersRefusesADuplicateSuppliedName(t *testing.T) {
	party, other := uuid.New(), uuid.New()

	err := validateDistinctBillOpeners([]BillAllocationInput{
		{LedgerID: party, RefName: "SPLIT-1", RefType: RefNew, DrCr: Debit, Amount: amt("5900")},
		{LedgerID: party, RefName: "SPLIT-1", RefType: RefNew, DrCr: Debit, Amount: amt("5900")},
	})
	if err == nil {
		t.Fatal("two bills named SPLIT-1 on one ledger were accepted; they are one bill")
	}

	// POSITIVE CONTROLS — without these, "it refused" is equally consistent with a
	// function that refuses every voucher carrying references.
	for name, refs := range map[string][]BillAllocationInput{
		"distinct names on one ledger": {
			{LedgerID: party, RefName: "SPLIT-1", RefType: RefNew, DrCr: Debit, Amount: amt("5900")},
			{LedgerID: party, RefName: "SPLIT-2", RefType: RefNew, DrCr: Debit, Amount: amt("5900")},
		},
		"the same name on two ledgers": {
			{LedgerID: party, RefName: "SPLIT-1", RefType: RefNew, DrCr: Debit, Amount: amt("5900")},
			{LedgerID: other, RefName: "SPLIT-1", RefType: RefNew, DrCr: Debit, Amount: amt("5900")},
		},
		// Two part-settlements of one bill in one voucher, and two on-account
		// amounts landing in one bucket, are both legal and must stay legal.
		"two settlements of one bill": {
			{LedgerID: party, RefName: "INV-1", RefType: RefAgainst, DrCr: Credit, Amount: amt("100")},
			{LedgerID: party, RefName: "INV-1", RefType: RefAgainst, DrCr: Credit, Amount: amt("200")},
		},
		"two on-account amounts": {
			{LedgerID: party, RefName: onAccountRefName, RefType: RefOnAccount, DrCr: Credit, Amount: amt("100")},
			{LedgerID: party, RefName: onAccountRefName, RefType: RefOnAccount, DrCr: Credit, Amount: amt("200")},
		},
	} {
		if err := validateDistinctBillOpeners(refs); err != nil {
			t.Fatalf("%s must be accepted, got: %v", name, err)
		}
	}
}
