package ledger

// payload_order_test.go — THE THIRD AND FOURTH WEDGES IN THE ONE-WAY DOOR.
//
// stock_hash_test.go wedged CanonicalPayload.Inventory open and
// inward_doc_test.go wedged Inward. This file does the same job for the two
// fields that arrived together — CostAllocations (cost centres) and Fulfilments
// (order lines) — and it exists because of a specific, recorded failure mode
// rather than for symmetry.
//
// THE FAILURE MODE, stated plainly. Both fields are `omitempty` and EVERY
// pinned vector in this package sets neither. So swapping their declaration
// order changes the bytes of NOTHING those vectors cover, and
// TestExistingVoucherHashesAreUnchanged, TestInwardDocIsLastInTheHashAndOmitted
// WhenEmpty and every hash test in the package all still PASS. The order is
// simultaneously load-bearing and invisible. Only a vector that sets BOTH
// fields can observe it, which is exactly the lesson CanonicalPayload.Inward's
// own comment records — this file is that lesson applied one field further
// along, to the case where two fields raced for the same last position.
//
// WHY THE ASSERTIONS ARE STRUCTURAL AND NOT A PINNED BYTE STRING. A golden
// captured from the current implementation cannot detect that the current
// implementation changed — stock_hash_test.go says so in its own header, which
// is why its vectors are hard-coded from a named prior commit. There is no
// prior commit to capture from here: these fields never existed before, so
// there are no historical bytes to preserve and a "pinned" string would only be
// this code's current output handed back to itself. What CAN be pinned is the
// RULE the bytes must obey — allocations before fulfilments, both after inward,
// all three after inventory — and that is what this file asserts. It fails on a
// declaration-order swap; a self-generated golden would too, but it would also
// fail on every harmless change and teach the next author to regenerate it
// without reading it.

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

// fullyLoadedPayload sets ALL FOUR optional fields. Nothing else in this
// package does, and until something did, the declaration order of the last two
// was unobservable.
func fullyLoadedPayload() CanonicalPayload {
	p := vectorPayload()
	p.BillAllocations = []CanonicalBillAllocation{{
		LedgerID: "018f0000-0000-7000-8000-000000000003",
		RefName:  "INV-1", RefType: "NEW", DrCr: "D",
		Amount: "1180.0000", DueDate: "2025-06-10",
	}}
	p.Inventory = []CanonicalStockLine{{
		StockItemID: "018f0000-0000-7000-8000-00000000000a",
		Direction:   "OUT",
		Qty:         "5.0000",
		Rate:        "100.0000",
		DiscountPct: "0.0000",
		Amount:      "500.0000",
	}}
	p.Inward = &CanonicalInward{
		SupplierInvoiceNo:   "S/99",
		SupplierInvoiceDate: "2025-05-01",
		ITCEligibility:      "ELIGIBLE",
		ITCBlockedClause:    "",
	}
	p.CostAllocations = []CanonicalCostAllocation{{
		PostingSeq:     "0",
		CostCategoryID: "018f0000-0000-7000-8000-00000000000d",
		CostCentreID:   "018f0000-0000-7000-8000-00000000000e",
		Amount:         "1180.0000",
	}}
	p.Fulfilments = []CanonicalFulfilment{{
		LineNo:      "1",
		OrderLineID: "018f0000-0000-7000-8000-00000000000f",
		Qty:         "5.0000",
	}}
	return p
}

// TestTheTwoNewOptionalFieldsAreLastAndInTheirPinnedOrder is the only test in
// the package that can see the order of the last two fields.
//
// If it fails, the fix is NOT to update the expectation. Two fields raced for
// the last position in the hashed payload, the tie was broken once and written
// into CanonicalPayload's comment, and re-breaking it later moves a key in
// every voucher signed since — retroactively declaring books tampered with,
// which reads to an operator exactly like an attack.
func TestTheTwoNewOptionalFieldsAreLastAndInTheirPinnedOrder(t *testing.T) {
	j, err := fullyLoadedPayload().Marshal()
	if err != nil {
		t.Fatalf("marshal fully loaded payload: %v", err)
	}
	got := string(j)

	// The five trailing keys, in the order the struct fixes them. `postings` is
	// included as an anchor: it is the last NON-optional key, so it pins the
	// whole optional tail to the end of the object rather than merely ordering
	// the tail within itself.
	order := []string{
		`"postings":`,
		`"bill_allocations":`,
		`"inventory":`,
		`"inward":`,
		`"cost_allocations":`,
		`"fulfilments":`,
	}
	prev := -1
	prevKey := ""
	for _, key := range order {
		at := strings.Index(got, key)
		if at < 0 {
			t.Fatalf("the fully loaded payload is missing %s, so this test can no longer observe the field order it exists to pin.\npayload: %s", key, got)
		}
		if at < prev {
			t.Fatalf("%s now serialises BEFORE %s. The hashed payload's key order changed, so every voucher signed since the previous order is now unverifiable and VerifyChain will report those books as tampered with.\nSee CanonicalPayload's comment: the order is fixed, and the fix is to restore the declaration order, NOT to update this test.\npayload: %s",
				key, prevKey, got)
		}
		prev = at
		prevKey = key
	}

	// And the tail really is the TAIL: nothing follows fulfilments but the
	// closing brace.
	if !strings.HasSuffix(got, `"qty":"5.0000"}]}`) {
		t.Fatalf("fulfilments is no longer the LAST key in the hashed payload. A field appended after it moves no bytes today, but the next optional field added above it will move every key below — which is how a whole book gets retroactively invalidated.\npayload: %s", got)
	}
}

// TestAVoucherWithNeitherAllocationsNorFulfilmentsHashesExactlyAsBefore is the
// omitempty half, and it is the half that protects data already in the field.
//
// Every voucher posted before this session carries neither field. Each must
// marshal to BYTE-IDENTICAL JSON, and chain to the identical hash, as it did
// when neither field existed — so the pinned pre-Inventory vectors are reused
// deliberately: they are hard-coded from a commit that predates all of this,
// which is what makes them evidence rather than a restatement of current
// behaviour.
func TestAVoucherWithNeitherAllocationsNorFulfilmentsHashesExactlyAsBefore(t *testing.T) {
	base := vectorPayload()
	j, err := base.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(j) != preInventoryJSON {
		t.Fatalf("adding cost allocations and fulfilments to the payload struct changed the encoding of a voucher that has NEITHER. Every historical hash is now invalid.\n  now:    %s\n  before: %s", j, preInventoryJSON)
	}
	if h := hex.EncodeToString(ChainHash(j, nil)); h != preInventoryHash {
		t.Fatalf("a voucher with neither field now hashes to %s; the log records %s for those exact contents", h, preInventoryHash)
	}

	// An EMPTY, NON-NIL slice must behave as absent too. This is not pedantry:
	// a handler that unconditionally builds `make([]CanonicalCostAllocation, 0)`
	// would, without omitempty's nil-and-empty equivalence, emit
	// `"cost_allocations":[]` and change the hash of every voucher it touched
	// while looking completely correct at the call site.
	empty := vectorPayload()
	empty.CostAllocations = []CanonicalCostAllocation{}
	empty.Fulfilments = []CanonicalFulfilment{}
	ej, err := empty.Marshal()
	if err != nil {
		t.Fatalf("marshal empty-slice payload: %v", err)
	}
	if !bytes.Equal(ej, j) {
		t.Fatalf("an EMPTY (non-nil) allocation/fulfilment slice serialises differently from an absent one, so a handler that builds empty slices silently re-hashes every voucher:\n  empty: %s\n  nil:   %s", ej, j)
	}
}

// TestCostAllocationsAndFulfilmentsSurviveTheJSONBRoundTrip proves the two new
// fields carry their weight — every field moves the hash — and that they
// survive the decode/re-marshal VerifyChain performs against the stored JSONB.
//
// The round trip is the reason every field is a string. Postgres does not
// preserve the input text of a jsonb column, so the bytes that were hashed have
// to be RE-DERIVED from the decoded struct; that is exact only while no value
// has a second legal spelling. An amount decoded as a float would re-marshal as
// 1180 where 1180.0000 was signed, and the voucher would read as tampered with.
func TestCostAllocationsAndFulfilmentsSurviveTheJSONBRoundTrip(t *testing.T) {
	base := fullyLoadedPayload()
	baseJSON, err := base.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	baseHash := hex.EncodeToString(ChainHash(baseJSON, nil))

	// The scale must survive, for the reason stock quantity's does.
	if !strings.Contains(string(baseJSON), `"amount":"1180.0000"`) {
		t.Fatalf("an allocation amount must be hashed as a fixed-4 STRING so its scale survives the JSONB round trip: %s", baseJSON)
	}

	for _, tc := range []struct {
		what   string
		mutate func(*CanonicalPayload)
	}{
		{"the allocated amount", func(p *CanonicalPayload) { p.CostAllocations[0].Amount = "1180.0001" }},
		{"the allocated amount's scale", func(p *CanonicalPayload) { p.CostAllocations[0].Amount = "1180" }},
		{"which posting was allocated", func(p *CanonicalPayload) { p.CostAllocations[0].PostingSeq = "1" }},
		{"the cost centre that bore the cost", func(p *CanonicalPayload) {
			p.CostAllocations[0].CostCentreID = "018f0000-0000-7000-8000-0000000000ee"
		}},
		{"the cost category axis", func(p *CanonicalPayload) {
			p.CostAllocations[0].CostCategoryID = "018f0000-0000-7000-8000-0000000000dd"
		}},
		{"dropping the allocation entirely", func(p *CanonicalPayload) { p.CostAllocations = nil }},
		{"the order line fulfilled", func(p *CanonicalPayload) {
			p.Fulfilments[0].OrderLineID = "018f0000-0000-7000-8000-0000000000ff"
		}},
		{"the quantity fulfilled", func(p *CanonicalPayload) { p.Fulfilments[0].Qty = "4.0000" }},
		{"which line fulfilled it", func(p *CanonicalPayload) { p.Fulfilments[0].LineNo = "2" }},
		{"dropping the fulfilment entirely", func(p *CanonicalPayload) { p.Fulfilments = nil }},
	} {
		p := fullyLoadedPayload()
		tc.mutate(&p)
		mj, err := p.Marshal()
		if err != nil {
			t.Fatalf("%s: marshal: %v", tc.what, err)
		}
		if h := hex.EncodeToString(ChainHash(mj, nil)); h == baseHash {
			t.Fatalf("changing %s left the hash at %s: the record of which department bore a cost, or which order a shipment satisfied, could be rewritten and the chain would still certify the voucher as untouched",
				tc.what, h)
		}
	}

	// The re-derivation itself: decode the stored bytes back into the struct and
	// re-marshal. This is what VerifyChain does on every read.
	var round CanonicalPayload
	if err := json.Unmarshal(baseJSON, &round); err != nil {
		t.Fatalf("decode stored payload: %v", err)
	}
	again, err := round.Marshal()
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	if !bytes.Equal(again, baseJSON) {
		t.Fatalf("the payload does not survive the JSONB round trip, so VerifyChain will report a correctly stored voucher as tampered with:\n  stored:      %s\n  re-derived:  %s", baseJSON, again)
	}
	if h := hex.EncodeToString(ChainHash(again, nil)); h != baseHash {
		t.Fatalf("the re-derived payload hashes to %s but %s was signed", h, baseHash)
	}
}
