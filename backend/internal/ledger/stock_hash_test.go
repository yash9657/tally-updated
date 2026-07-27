package ledger

// stock_hash_test.go — THE ONE-WAY DOOR, held open with a wedge.
//
// voucher_version.hash is a per-voucher BLAKE3 chain over the canonical payload
// bytes. Every hash ever written is a function of the EXACT JSON encoding_of
// CanonicalPayload at the moment it was written. Adding a field to that struct
// is therefore not a refactor, it is a migration of every historical record —
// and if it is done wrong, nothing fails loudly: VerifyChain simply begins
// reporting the entire book as tampered with, which is indistinguishable from
// an actual attack.
//
// The vectors below were captured from the code as it stood BEFORE
// CanonicalPayload.Inventory existed. They are hard-coded on purpose: a vector
// derived from the current code cannot detect that the current code changed.

import (
	"encoding/hex"
	"strings"
	"testing"
)

// The pre-Inventory canonical bytes and chain hashes, captured verbatim from
// the code at commit c3aca5d before this slice touched hash.go. Do not
// regenerate these from the current implementation — that would defeat them.
const (
	preInventoryJSON = `{"voucher_id":"018f0000-0000-7000-8000-000000000001","voucher_type_id":"018f0000-0000-7000-8000-000000000002","version_no":1,"op":"CREATE","class":"Sales","voucher_no":"INV-1","voucher_date":"2025-05-10","narration":"Sale to Acme","party":"Acme Corp","postings":[{"ledger_id":"018f0000-0000-7000-8000-000000000003","dr_cr":"D","amount":"1180.0000"},{"ledger_id":"018f0000-0000-7000-8000-000000000004","dr_cr":"C","amount":"1180.0000"}]}`
	preInventoryHash = "ec486146e659e1988b182b327f4b93cf96b0356fcf2716198ef409a46b7b73be"

	preInventoryBillJSON = `{"voucher_id":"018f0000-0000-7000-8000-000000000001","voucher_type_id":"018f0000-0000-7000-8000-000000000002","version_no":1,"op":"CREATE","class":"Sales","voucher_no":"INV-1","voucher_date":"2025-05-10","narration":"Sale to Acme","party":"Acme Corp","postings":[{"ledger_id":"018f0000-0000-7000-8000-000000000003","dr_cr":"D","amount":"1180.0000"},{"ledger_id":"018f0000-0000-7000-8000-000000000004","dr_cr":"C","amount":"1180.0000"}],"bill_allocations":[{"ledger_id":"018f0000-0000-7000-8000-000000000003","ref_name":"INV-1","ref_type":"NEW","dr_cr":"D","amount":"1180.0000","due_date":"2025-06-10"}]}`
	// The chain hash of the bill-allocation payload linked onto preInventoryHash.
	preInventoryBillChainHash = "ee46c8076da035d91cdc08c0a8bda4c16c9b487088be90c1b2ba78395a1edfe9"
)

// vectorPayload rebuilds the exact input the pinned vectors were captured from.
func vectorPayload() CanonicalPayload {
	return CanonicalPayload{
		VoucherID:     "018f0000-0000-7000-8000-000000000001",
		VoucherTypeID: "018f0000-0000-7000-8000-000000000002",
		VersionNo:     1,
		Op:            "CREATE",
		Class:         "Sales",
		VoucherNo:     "INV-1",
		VoucherDate:   "2025-05-10",
		Narration:     "Sale to Acme",
		Party:         "Acme Corp",
		Postings: []CanonicalPosting{
			{LedgerID: "018f0000-0000-7000-8000-000000000003", DrCr: "D", Amount: "1180.0000"},
			{LedgerID: "018f0000-0000-7000-8000-000000000004", DrCr: "C", Amount: "1180.0000"},
		},
	}
}

// TestExistingVoucherHashesAreUnchanged is the wedge. A voucher that carries no
// inventory must marshal to BYTE-IDENTICAL JSON, and hash to the identical
// BLAKE3 chain value, as it did before the Inventory field existed — with and
// without bill allocations, so the new field's position relative to the last
// existing optional field is pinned too.
//
// If this fails, every voucher already in every customer's database has just
// been retroactively declared tampered with by VerifyChain.
func TestExistingVoucherHashesAreUnchanged(t *testing.T) {
	base := vectorPayload()

	gotJSON, err := base.Marshal()
	if err != nil {
		t.Fatalf("marshal canonical payload: %v", err)
	}
	if string(gotJSON) != preInventoryJSON {
		t.Fatalf("the canonical encoding of an inventory-free voucher CHANGED, so every historical hash is now invalid.\n  now:    %s\n  before: %s",
			gotJSON, preInventoryJSON)
	}
	gotHash := hex.EncodeToString(ChainHash(gotJSON, nil))
	if gotHash != preInventoryHash {
		t.Fatalf("version 1 of an inventory-free voucher now hashes to %s, but the log records %s for those exact contents",
			gotHash, preInventoryHash)
	}

	withBill := base
	withBill.BillAllocations = []CanonicalBillAllocation{{
		LedgerID: "018f0000-0000-7000-8000-000000000003",
		RefName:  "INV-1", RefType: "NEW", DrCr: "D",
		Amount: "1180.0000", DueDate: "2025-06-10",
	}}
	billJSON, err := withBill.Marshal()
	if err != nil {
		t.Fatalf("marshal bill-allocation payload: %v", err)
	}
	if string(billJSON) != preInventoryBillJSON {
		t.Fatalf("the canonical encoding of a bill-wise voucher CHANGED, so every historical hash on a bill-wise voucher is now invalid.\n  now:    %s\n  before: %s",
			billJSON, preInventoryBillJSON)
	}
	billHash := hex.EncodeToString(ChainHash(billJSON, ChainHash(gotJSON, nil)))
	if billHash != preInventoryBillChainHash {
		t.Fatalf("a bill-wise version chained onto version 1 now hashes to %s, but the log records %s",
			billHash, preInventoryBillChainHash)
	}

	// And the chain over those two versions must still verify end to end — the
	// property an auditor actually reads.
	v1Hash := ChainHash(gotJSON, nil)
	rows := []RawVersion{
		{VersionNo: 1, Op: "CREATE", PayloadJSON: gotJSON, Hash: v1Hash},
		{VersionNo: 2, Op: "ALTER", PayloadJSON: billJSON, Hash: ChainHash(billJSON, v1Hash), PrevHash: v1Hash},
	}
	if v := VerifyChain(rows); !v.OK {
		t.Fatalf("an untouched two-version chain no longer verifies: %s", v.Reason)
	}
}

// TestStockLinesAreInTheHashAndSurviveTheJSONBRoundTrip proves the new field
// carries its weight (a changed line changes the hash) AND that it survives the
// JSONB re-derivation VerifyChain depends on.
//
// The DB half of the round trip lives in
// TestStockLinesSurviveTheStoredJSONBRoundTrip (stock_integration_test.go);
// this half pins the ENCODING that makes it possible: fixed-4 strings.
func TestStockLinesAreInTheHashAndSurviveTheJSONBRoundTrip(t *testing.T) {
	line := CanonicalStockLine{
		StockItemID: "018f0000-0000-7000-8000-00000000000a",
		GodownID:    "018f0000-0000-7000-8000-00000000000b",
		BatchID:     "018f0000-0000-7000-8000-00000000000c",
		Direction:   "OUT",
		Qty:         "12.3400",
		Rate:        "250.0000",
		DiscountPct: "5.0000",
		Amount:      "2930.7500",
	}
	base := vectorPayload()
	base.Inventory = []CanonicalStockLine{line}
	baseJSON, err := base.Marshal()
	if err != nil {
		t.Fatalf("marshal stock payload: %v", err)
	}

	// The stored SCALE must survive. If quantity were a number rather than a
	// fixed-4 string, "12.3400" and "12.34" would be the same bytes and a
	// silently re-scaled quantity would be undetectable in the log.
	if !strings.Contains(string(baseJSON), `"qty":"12.3400"`) {
		t.Fatalf("stock quantity must be hashed as a fixed-4 STRING so its scale survives the JSONB round trip; payload was: %s", baseJSON)
	}
	if !strings.Contains(string(baseJSON), `"inventory":[`) {
		t.Fatalf("inventory lines are missing from the hashed payload, so the edit log would show stock it did not sign: %s", baseJSON)
	}

	baseHash := hex.EncodeToString(ChainHash(baseJSON, nil))

	// Every field of a line must move the hash. The quantity cases are chosen to
	// differ in the 19th significant digit: NUMERIC(19,4) can hold them and a
	// fixed-4 string distinguishes them, but float64 cannot — under a float
	// encoding these two DIFFERENT vouchers would share one hash, and swapping
	// one for the other would be invisible to the auditor.
	for _, tc := range []struct {
		what   string
		mutate func(*CanonicalStockLine)
	}{
		{"the quantity", func(l *CanonicalStockLine) { l.Qty = "12.3500" }},
		{"the quantity's scale", func(l *CanonicalStockLine) { l.Qty = "12.34" }},
		{"the rate", func(l *CanonicalStockLine) { l.Rate = "250.0001" }},
		{"the stock item", func(l *CanonicalStockLine) { l.StockItemID = "018f0000-0000-7000-8000-0000000000aa" }},
		{"the godown", func(l *CanonicalStockLine) { l.GodownID = "018f0000-0000-7000-8000-0000000000bb" }},
		{"the batch", func(l *CanonicalStockLine) { l.BatchID = "" }},
		{"the direction", func(l *CanonicalStockLine) { l.Direction = "IN" }},
		{"the discount", func(l *CanonicalStockLine) { l.DiscountPct = "6.0000" }},
		{"the amount", func(l *CanonicalStockLine) { l.Amount = "2930.7600" }},
	} {
		mutated := line
		tc.mutate(&mutated)
		p := vectorPayload()
		p.Inventory = []CanonicalStockLine{mutated}
		j, err := p.Marshal()
		if err != nil {
			t.Fatalf("%s: marshal: %v", tc.what, err)
		}
		if h := hex.EncodeToString(ChainHash(j, nil)); h == baseHash {
			t.Fatalf("changing %s of a stock line left the hash at %s: the movement could be rewritten and the chain would still certify the voucher as untouched",
				tc.what, h)
		}
	}

	// Two quantities that differ only below float64's precision. The whole point
	// of the fixed-4 string encoding.
	fine := func(q string) string {
		p := vectorPayload()
		l := line
		l.Qty = q
		p.Inventory = []CanonicalStockLine{l}
		j, err := p.Marshal()
		if err != nil {
			t.Fatalf("marshal %s: %v", q, err)
		}
		return hex.EncodeToString(ChainHash(j, nil))
	}
	if a, b := fine("123456789012345.6789"), fine("123456789012345.6788"); a == b {
		t.Fatalf("two quantities that a NUMERIC(19,4) column stores distinctly hash identically (%s): a line's quantity can be altered without breaking the chain", a)
	}

	// The re-derivation VerifyChain performs: decode the stored payload back into
	// the struct, re-marshal, recompute. Every value being a string is what makes
	// this exact.
	rows := []RawVersion{{VersionNo: 1, Op: "CREATE", PayloadJSON: baseJSON, Hash: ChainHash(baseJSON, nil)}}
	if v := VerifyChain(rows); !v.OK {
		t.Fatalf("a voucher carrying stock lines does not survive the payload re-derivation: %s", v.Reason)
	}
}
