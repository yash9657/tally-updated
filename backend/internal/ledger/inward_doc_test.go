package ledger

// inward_doc_test.go — THE SECOND WEDGE IN THE ONE-WAY DOOR.
//
// stock_hash_test.go holds the first: vectors captured BEFORE
// CanonicalPayload.Inventory existed, which fail if an inventory-free voucher's
// bytes ever move. This file holds the equivalent for CanonicalPayload.Inward,
// and it has to do MORE work than the first one did, for a reason worth stating
// because it was discovered by trying the mutation rather than by reasoning:
//
//	MOVING `Inward` TO SIT BEFORE `Inventory` DOES NOT BREAK
//	TestExistingVoucherHashesAreUnchanged.
//
// That test's vector carries NEITHER stock lines NOR an inward document, and
// both fields are `omitempty`, so both are absent from the JSON and their
// relative order is unobservable. The pinned pre-Inventory hash cannot see the
// mutation at all. Neither can
// TestStockLinesAreInTheHashAndSurviveTheJSONBRoundTrip, whose vector sets
// Inventory but leaves Inward nil.
//
// The only vector that CAN see it is one where BOTH fields are present, and
// nothing in the repository had one. So this file pins that vector explicitly,
// with a byte literal composed BY HAND from the declared rule ("inward is last,
// after inventory; its four inner fields are plain strings in declaration
// order") rather than captured from a run of the code it is checking. A vector
// printed by the implementation cannot detect that the implementation changed.

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

// The inward object as it must be encoded, composed from the declared field
// order of CanonicalInward. Four keys, always all four, always in this order.
const inwardObjectJSON = `{"supplier_invoice_no":"SUP/2026/0007",` +
	`"supplier_invoice_date":"2026-03-31",` +
	`"itc_eligibility":"BLOCKED_17_5",` +
	`"itc_blocked_clause":"17(5)(a)"}`

// One inventory line, encoded exactly as stock_hash_test.go's vector encodes it.
// Restated here rather than imported so that this file's expectations do not
// move when somebody edits that one.
const inwardVectorInventoryJSON = `[{"stock_item_id":"018f0000-0000-7000-8000-00000000000a",` +
	`"godown_id":"018f0000-0000-7000-8000-00000000000b",` +
	`"batch_id":"018f0000-0000-7000-8000-00000000000c",` +
	`"direction":"OUT","qty":"12.3400","rate":"250.0000",` +
	`"discount_pct":"5.0000","amount":"2930.7500"}]`

// inwardVectorLine is the Go form of the line above.
var inwardVectorLine = CanonicalStockLine{
	StockItemID: "018f0000-0000-7000-8000-00000000000a",
	GodownID:    "018f0000-0000-7000-8000-00000000000b",
	BatchID:     "018f0000-0000-7000-8000-00000000000c",
	Direction:   "OUT",
	Qty:         "12.3400",
	Rate:        "250.0000",
	DiscountPct: "5.0000",
	Amount:      "2930.7500",
}

// inwardVectorDoc is the Go form of inwardObjectJSON.
//
// The values are chosen to be awkward on purpose. The document number carries
// slashes (which normalisation strips, and which must therefore be preserved
// VERBATIM in the hash — the hash signs what the supplier printed, not what the
// matcher makes of it). The date is 31 March, the section 16(4) financial-year
// boundary, so a vector that silently re-formats a date has moved a statutory
// expiry by a year.
var inwardVectorDoc = CanonicalInward{
	SupplierInvoiceNo:   "SUP/2026/0007",
	SupplierInvoiceDate: "2026-03-31",
	ITCEligibility:      "BLOCKED_17_5",
	ITCBlockedClause:    "17(5)(a)",
}

// trimClose drops the final '}' of a canonical payload encoding so a tail field
// can be appended to it. Composing the expected bytes this way states the rule
// under test — "appended at the END" — in the construction itself.
func trimClose(s string) string { return strings.TrimSuffix(s, "}") }

// TestInwardDocIsLastInTheHashAndOmittedWhenEmpty pins three things that are
// each a way for this change to silently invalidate every hash ever written.
//
//  1. A payload with Inward == nil marshals to preInventoryJSON BYTE FOR BYTE,
//     and hashes to the pinned pre-Inventory chain value. Every sales voucher
//     and every purchase posted before migration 0032 is that payload.
//  2. With BOTH inventory and inward present, "inward" comes LAST. This is the
//     assertion that catches a reordered struct, and it is the ONLY one in the
//     repository that can: with both fields omitempty, no vector carrying
//     fewer than both can observe their relative order.
//  3. The four inner fields have NO omitempty, so the object's SHAPE is fixed
//     once it is present and cannot vary with its contents.
func TestInwardDocIsLastInTheHashAndOmittedWhenEmpty(t *testing.T) {
	// ---- 1. nil Inward is byte-identical to the pre-Inward encoding ---------
	base := vectorPayload()
	if base.Inward != nil {
		t.Fatal("the zero CanonicalPayload must carry no inward document")
	}
	gotJSON, err := base.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(gotJSON) != preInventoryJSON {
		t.Fatalf("a voucher with no supplier document no longer marshals to the bytes it did before CanonicalPayload.Inward existed, so every historical hash is now invalid.\n  now:    %s\n  before: %s",
			gotJSON, preInventoryJSON)
	}
	if h := hex.EncodeToString(ChainHash(gotJSON, nil)); h != preInventoryHash {
		t.Fatalf("an inward-free voucher now hashes to %s; the log records %s for those exact contents", h, preInventoryHash)
	}

	// ---- 2. inward is LAST, after inventory --------------------------------
	//
	// The expected bytes are composed here from the rule, not captured from a
	// run: take the pre-Inventory encoding, append "inventory", then append
	// "inward". If the struct is reordered, the implementation emits them the
	// other way round and this literal no longer matches.
	wantBoth := trimClose(preInventoryJSON) +
		`,"inventory":` + inwardVectorInventoryJSON +
		`,"inward":` + inwardObjectJSON + `}`

	both := vectorPayload()
	both.Inventory = []CanonicalStockLine{inwardVectorLine}
	inw := inwardVectorDoc
	both.Inward = &inw
	bothJSON, err := both.Marshal()
	if err != nil {
		t.Fatalf("marshal inventory+inward payload: %v", err)
	}
	if string(bothJSON) != wantBoth {
		t.Fatalf("the canonical encoding of a voucher carrying BOTH stock lines and a supplier document is not what the field order requires.\nThis is what a reordered CanonicalPayload looks like, and it retroactively invalidates the hash of every voucher that carries both.\n  got:  %s\n  want: %s",
			bothJSON, wantBoth)
	}
	// Stated a second way, independent of the literal, so the failure message
	// names the actual defect rather than a diff.
	iInv := strings.Index(string(bothJSON), `"inventory":`)
	iInw := strings.Index(string(bothJSON), `"inward":`)
	if iInv < 0 || iInw < 0 {
		t.Fatalf("both keys must be present in a payload that carries both: %s", bothJSON)
	}
	if iInw < iInv {
		t.Fatalf(`"inward" is emitted BEFORE "inventory" (offsets %d and %d). encoding/json emits keys in struct-declaration order, so Inward has been moved ahead of Inventory in CanonicalPayload — which changes the bytes, and therefore the BLAKE3 hash, of every voucher already written that carries stock lines. VerifyChain will report those vouchers as tampered with.`, iInw, iInv)
	}

	// ---- 3. the object's shape does not vary with its contents -------------
	sparse := vectorPayload()
	only := CanonicalInward{SupplierInvoiceNo: "SUP/2026/0007"}
	sparse.Inward = &only
	sparseJSON, err := sparse.Marshal()
	if err != nil {
		t.Fatalf("marshal sparse inward: %v", err)
	}
	wantSparse := trimClose(preInventoryJSON) +
		`,"inward":{"supplier_invoice_no":"SUP/2026/0007","supplier_invoice_date":"","itc_eligibility":"","itc_blocked_clause":""}}`
	if string(sparseJSON) != wantSparse {
		t.Fatalf("an inward document with only a number set must still emit ALL FOUR keys — with omitempty on the inner fields, the same recorded facts would hash differently depending on which of them happened to be blank.\n  got:  %s\n  want: %s",
			sparseJSON, wantSparse)
	}

	// ---- the JSONB re-derivation VerifyChain depends on --------------------
	//
	// Decode the stored bytes back into the struct and re-marshal. Postgres does
	// not preserve a jsonb column's input text, so this round trip is how the
	// hashed bytes are recovered at verification time. It is exact only because
	// every value is a string; a date decoded as time.Time would come back in a
	// different layout and declare the book tampered with.
	var decoded CanonicalPayload
	if err := json.Unmarshal(bothJSON, &decoded); err != nil {
		t.Fatalf("decode stored payload: %v", err)
	}
	reJSON, err := decoded.Marshal()
	if err != nil {
		t.Fatalf("re-marshal decoded payload: %v", err)
	}
	if string(reJSON) != string(bothJSON) {
		t.Fatalf("the supplier document does not survive the JSONB round trip, so VerifyChain would report a correctly stored voucher as tampered with.\n  re-derived: %s\n  stored:     %s",
			reJSON, bothJSON)
	}
	rows := []RawVersion{{VersionNo: 1, Op: "CREATE", PayloadJSON: bothJSON, Hash: ChainHash(bothJSON, nil)}}
	if v := VerifyChain(rows); !v.OK {
		t.Fatalf("a voucher carrying a supplier document does not verify: %s", v.Reason)
	}
}

// TestEveryInwardFieldMovesTheHash proves the field carries its weight. If a
// value could be changed without moving the hash, the edit log would go on
// certifying a voucher whose supplier document had been rewritten — and the
// supplier document is the single fact the whole ITC claim is keyed on.
func TestEveryInwardFieldMovesTheHash(t *testing.T) {
	baseline := func(d CanonicalInward) string {
		p := vectorPayload()
		c := d
		p.Inward = &c
		j, err := p.Marshal()
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return hex.EncodeToString(ChainHash(j, nil))
	}
	base := baseline(inwardVectorDoc)

	for _, tc := range []struct {
		what   string
		mutate func(*CanonicalInward)
	}{
		{"the supplier's invoice number", func(d *CanonicalInward) { d.SupplierInvoiceNo = "SUP/2026/0008" }},
		{"the punctuation in the invoice number", func(d *CanonicalInward) { d.SupplierInvoiceNo = "SUP20260007" }},
		{"the supplier's invoice date", func(d *CanonicalInward) { d.SupplierInvoiceDate = "2026-04-01" }},
		{"the ITC eligibility", func(d *CanonicalInward) { d.ITCEligibility = "ELIGIBLE" }},
		{"the blocked clause", func(d *CanonicalInward) { d.ITCBlockedClause = "17(5)(b)" }},
	} {
		m := inwardVectorDoc
		tc.mutate(&m)
		if h := baseline(m); h == base {
			t.Fatalf("changing %s left the hash at %s: the document this credit is claimed against could be rewritten and the chain would still certify the voucher as untouched",
				tc.what, h)
		}
	}

	// The two dates in this pair are ONE DAY apart and fall on opposite sides of
	// the financial-year boundary, so they carry section 16(4) expiries a full
	// year apart (30-Nov-2026 against 30-Nov-2027). If those two hashed the same,
	// the date that decides when the credit dies would be unsigned.
	fyEnd := inwardVectorDoc
	fyEnd.SupplierInvoiceDate = "2026-03-31"
	fyStart := inwardVectorDoc
	fyStart.SupplierInvoiceDate = "2026-04-01"
	if baseline(fyEnd) == baseline(fyStart) {
		t.Fatal("31-March and 1-April hash identically: the date that fixes the financial year, and therefore the section 16(4) expiry of this credit, is not signed")
	}
}

// TestInwardDocumentValidation covers the pre-flight check. Its whole job is to
// fail BEFORE the transaction opens: the schema's CHECK would catch a bad
// eligibility too, but only after the numbering counter has advanced and the
// postings are written, and it would report a constraint name rather than the
// field.
func TestInwardDocumentValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		doc     InwardDocument
		wantErr bool
	}{
		{"empty is legal — it means no inward detail", InwardDocument{}, false},
		{"number alone", InwardDocument{SupplierInvoiceNo: "A/1"}, false},
		{"every eligibility value", InwardDocument{ITCEligibility: "ELIGIBLE"}, false},
		{"blocked", InwardDocument{ITCEligibility: "BLOCKED_17_5", ITCBlockedClause: "17(5)(a)"}, false},
		{"capital goods", InwardDocument{ITCEligibility: "CAPITAL_GOODS"}, false},
		{"isd", InwardDocument{ITCEligibility: "ISD"}, false},
		{"a clause without a classification is allowed — the ledger may carry it", InwardDocument{ITCBlockedClause: "17(5)(a)"}, false},
		{"an unknown eligibility", InwardDocument{ITCEligibility: "MAYBE"}, true},
		{"eligibility is case sensitive, like the CHECK", InwardDocument{ITCEligibility: "eligible"}, true},
		{"iso date", InwardDocument{SupplierInvoiceDate: "2026-03-31"}, false},
		// dd-mm-yyyy is the shape an Indian invoice prints and the shape an
		// operator types. Accepted as text it would reach Postgres and be coerced
		// under whatever DateStyle the session carried — "01-02-2026" is a REAL
		// date in the WRONG MONTH, and nothing downstream could tell.
		{"a dd-mm-yyyy date is refused", InwardDocument{SupplierInvoiceDate: "31-03-2026"}, true},
		{"a nonsense date is refused", InwardDocument{SupplierInvoiceDate: "2026-13-01"}, true},
		{"a timestamp is refused", InwardDocument{SupplierInvoiceDate: "2026-03-31T00:00:00Z"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.doc.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("%#v must be refused before the transaction opens", tc.doc)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("%#v is legal but was refused: %v", tc.doc, err)
			}
		})
	}
}

// TestInwardDocumentIsZeroIsTotal guards the predicate post() uses to decide
// whether a voucher gets an "inward" object at all. If a field were added to
// InwardDocument and left out of IsZero, a voucher carrying ONLY that field
// would write a column and no signature — and the two would disagree forever,
// silently, on exactly the vouchers that used the new field.
func TestInwardDocumentIsZeroIsTotal(t *testing.T) {
	if !(InwardDocument{}).IsZero() {
		t.Fatal("the zero InwardDocument must be zero")
	}
	for _, d := range []InwardDocument{
		{SupplierInvoiceNo: "x"},
		{SupplierInvoiceDate: "2026-03-31"},
		{ITCEligibility: "ELIGIBLE"},
		{ITCBlockedClause: "17(5)(a)"},
	} {
		if d.IsZero() {
			t.Fatalf("%#v is not zero, but IsZero says it is: post() would drop it from the payload while still writing its column", d)
		}
		if d.Canonical() == (CanonicalInward{}) {
			t.Fatalf("%#v produced an empty CanonicalInward: the field is missing from Canonical()", d)
		}
	}
}
