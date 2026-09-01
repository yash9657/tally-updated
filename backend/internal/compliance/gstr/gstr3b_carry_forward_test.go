package gstr

// gstr3b_carry_forward_test.go covers the GSTN "no negative entries" rule for
// tables 3.1 and 3.2, and the carry-forward that rule forces.
//
// ⛔ THE SHAPE UNDER TEST IS THE ONE THE SEEDER SHIPS, NOT A HAND-BUILT ONE.
// The rows in july2026Rows() are the exact folded form of the two GST vouchers
// the demo book carries in July 2026 — a sales credit note against an earlier
// inter-state invoice, and a purchase-side debit note — measured with psql:
//
//	parent_class | voucher_no | voucher_date | taxable_value | cgst | sgst | igst | nature
//	CREDIT_NOTE  | 1          | 2026-07-08   | 12000.0000    | 0    | 0    | 2160 | ASSET
//	DEBIT_NOTE   | 1          | 2026-07-12   | 2500.0000     | 225  | 225  | 0    | LIABILITY
//
// July has a return and NO FRESH SALES, which is an ordinary business month.
// Before the carry-forward, 3.1(a) netted to -12000 and Validate rejected the
// month through the 3.2 comparison — reporting "table 3.2 taxable 0.0000
// exceeds 3.1(a) taxable -12000.0000", about a table that was EMPTY. One
// month's failure aborted the whole annual return.
//
// These tests are PURE (build3B + Validate, no database) for the reason the
// table-4 file gives: the properties are properties of the pure core.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func july2026() time.Time {
	t, err := time.Parse("2006-01-02", "2026-07-01")
	if err != nil {
		panic(err)
	}
	return t
}

// july2026Rows is the demo book's July, already folded by the SQL in
// Generate3B: a CREDIT_NOTE against an ASSET-nature party becomes SALES with
// sgn -1, and a DEBIT_NOTE against a LIABILITY-nature party becomes PURCHASE
// with sgn -1. Both parties are REGULAR (registered), so neither touches 3.2.
func july2026Rows() []row3b {
	return []row3b{
		{voucherID: uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001"), voucherNo: "1",
			class: "SALES", rawClass: "CREDIT_NOTE", pos: "27",
			taxable: d4("-12000"), cgst: d4("0"), sgst: d4("0"), igst: d4("-2160"),
			registered: true},
		{voucherID: uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000001"), voucherNo: "1",
			class: "PURCHASE", rawClass: "DEBIT_NOTE", pos: "29",
			taxable: d4("-2500"), cgst: d4("-225"), sgst: d4("-225"), igst: d4("0"),
			registered: true},
	}
}

func opts3B() Options3B { return Options3B{Options: Options{GSTIN: "29ZZZZZ0000Z1Z5"}} }

// TestBuild3B_AMonthOfReturnsWithNoSalesIsFilable is the D113 regression.
//
// ⛔ MUTATION PROOF: revert the clamp in build3B and this test fails with
//
//	table 3.2 taxable 0.0000 exceeds 3.1(a) taxable -12000.0000
//
// which is verbatim what GET /v1/companies/{id}/gstr3b?from=2026-07-01 returned
// against the shipped demo book at 74b8619.
func TestBuild3B_AMonthOfReturnsWithNoSalesIsFilable(t *testing.T) {
	got, err := build3B(july2026(), july2026Rows(), nil, opts3B())
	if err != nil {
		t.Fatalf("build3B on a month of pure returns must not error: %v", err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("a month whose credit notes exceed its sales is an ordinary month and must "+
			"produce a filable return, got: %v", err)
	}

	// THE PORTAL'S OWN RULE: "Negative values, if any, shall not be considered
	// in the table and system will provide the value as zero."
	if !got.T31.OsupDet.Txval.IsZero() || !got.T31.OsupDet.Iamt.IsZero() {
		t.Fatalf("3.1(a) must be clamped to zero, got txval %s iamt %s",
			got.T31.OsupDet.Txval, got.T31.OsupDet.Iamt)
	}

	// AND THE CLAMPED VALUE MUST NOT VANISH. A clamp with no carry-forward is
	// the silent version of this defect: the filer loses 12,000 of taxable
	// value and 2,160 of IGST with nothing on the return to say so.
	c := carriedFor(t, got, "3.1(a)")
	if c.Txval.String() != "12000" || c.Iamt.String() != "2160" {
		t.Fatalf("3.1(a) must carry forward 12000/2160, got txval %s iamt %s", c.Txval, c.Iamt)
	}

	// 3.1(a) IS THE ONLY THING CARRIED. The purchase-side debit note is NOT
	// reverse-charge, so it never reaches 3.1(d) — it lands in table 4 as
	// negative input tax, where a negative is LEGAL and must survive untouched.
	// Clamping there would manufacture credit out of nothing.
	if len(got.Carried) != 1 {
		t.Fatalf("only 3.1(a) may be carried for this month, got %+v", got.Carried)
	}
	if !got.T31.IsupRev.Txval.IsZero() {
		t.Fatalf("3.1(d) is for reverse-charge inward supply only, got %s", got.T31.IsupRev.Txval)
	}
	if n := got.T4.ItcNet; n.Camt.String() != "-225" || n.Samt.String() != "-225" {
		t.Fatalf("a purchase return must leave table 4 negative and unclamped, got %s/%s",
			n.Camt, n.Samt)
	}

	if !hasDisclosure(got, DiscNegativeValuesCarriedForward) {
		t.Fatalf("a return that clamped a negative must disclose it; got %v", got.Disclosures)
	}
	if !hasDisclosure(got, DiscNotFilingReady) {
		t.Fatalf("a carried-forward amount needs the filer to act, so the return is not "+
			"filing-ready; got %v", got.Disclosures)
	}
}

// TestValidate3B_RejectsANegative31RowWithASentenceAboutTheRightTable is the
// guard that survives the clamp being deleted or bypassed. Its VALUE is its
// sentence: the pre-fix code detected the same broken state and blamed table
// 3.2, which was empty.
func TestValidate3B_RejectsANegative31RowWithASentenceAboutTheRightTable(t *testing.T) {
	g, err := build3B(july2026(), july2026Rows(), nil, opts3B())
	if err != nil {
		t.Fatalf("build3B: %v", err)
	}
	g.T31.OsupDet.Txval = d4("-12000") // undo the clamp, as a mutation would
	g.T31.OsupDet.Iamt = d4("-2160")

	err = g.Validate()
	if err == nil {
		t.Fatal("a negative 3.1(a) is not filable and must be refused")
	}
	msg := err.Error()
	if !strings.Contains(msg, "3.1(a)") {
		t.Fatalf("the refusal must name the table that is actually negative, got: %s", msg)
	}
	if strings.Contains(msg, "3.2") {
		t.Fatalf("the refusal must NOT blame table 3.2, which is empty here — that was the "+
			"defect. Got: %s", msg)
	}
	if !strings.Contains(msg, "-12000") {
		t.Fatalf("the refusal must print the offending value, got: %s", msg)
	}
}

// TestBuild3B_AnInterStateCreditNoteReducesTable32 covers the sign-blindness
// that would have re-created this defect one table over.
//
// interState() read `igst > 0`, so a folded CREDIT NOTE — whose igst is
// NEGATIVE by construction — was classified INTRA-state and never reduced the
// 3.2 bucket it belonged to, while it DID reduce 3.1(a). Two ordinary vouchers
// are enough to drive 3.2 above 3.1(a) and earn a refusal for books that are
// perfectly correct.
func TestBuild3B_AnInterStateCreditNoteReducesTable32(t *testing.T) {
	rows := []row3b{
		{voucherID: uuid.MustParse("cccccccc-0000-0000-0000-000000000001"), voucherNo: "S-1",
			class: "SALES", rawClass: "SALES", pos: "27",
			taxable: d4("200000"), cgst: d4("0"), sgst: d4("0"), igst: d4("36000"),
			registered: false},
		{voucherID: uuid.MustParse("cccccccc-0000-0000-0000-000000000002"), voucherNo: "CN-1",
			class: "SALES", rawClass: "CREDIT_NOTE", pos: "27",
			taxable: d4("-50000"), cgst: d4("0"), sgst: d4("0"), igst: d4("-9000"),
			registered: false},
	}
	got, err := build3B(july2026(), rows, nil, opts3B())
	if err != nil {
		t.Fatalf("build3B: %v", err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("an inter-state sale plus a partial return of it is an ordinary month: %v", err)
	}
	if len(got.T32) != 1 {
		t.Fatalf("expected exactly one 3.2 bucket for POS 27, got %d", len(got.T32))
	}
	// 200000 - 50000, and 36000 - 9000. Before the fix the note was treated as
	// intra-state, so 3.2 stayed at the gross 200000/36000 while 3.1(a) netted
	// to 150000/27000 — 3.2 exceeding 3.1(a) on books that are correct.
	if got.T32[0].Txval.String() != "150000" || got.T32[0].Iamt.String() != "27000" {
		t.Fatalf("3.2 must net the credit note, got txval %s iamt %s",
			got.T32[0].Txval, got.T32[0].Iamt)
	}
}

// TestBuild3B_AnOrdinaryMonthCarriesNothing is the CONTROL. The clamp must be
// invisible to every book that never goes negative — no new key in the
// document, no new disclosure token.
func TestBuild3B_AnOrdinaryMonthCarriesNothing(t *testing.T) {
	got, err := build3B(may2025(), goldenRows(), nil, opts3B())
	if err != nil {
		t.Fatalf("build3B: %v", err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(got.Carried) != 0 {
		t.Fatalf("nothing went negative, so nothing may be carried; got %+v", got.Carried)
	}
	if hasDisclosure(got, DiscNegativeValuesCarriedForward) {
		t.Fatalf("the carry-forward token must not appear when nothing was clamped; got %v",
			got.Disclosures)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "carried_forward") {
		t.Fatalf("the carried_forward key must be ABSENT, not present-and-empty: a consumer "+
			"that switches on presence reads them differently.\n%s", raw)
	}
}

func carriedFor(t *testing.T, g GSTR3B, table string) CarriedForward {
	t.Helper()
	for _, c := range g.Carried {
		if c.Table == table {
			return c
		}
	}
	t.Fatalf("no carried-forward entry for table %s; got %+v", table, g.Carried)
	return CarriedForward{}
}

func hasDisclosure(g GSTR3B, want string) bool {
	for _, d := range g.Disclosures {
		if d == want {
			return true
		}
	}
	return false
}

var _ = decimal.Zero
