package ledger

import (
	"encoding/json"

	"github.com/zeebo/blake3"
)

// Op is the operation recorded by a voucher version.
type Op string

const (
	// OpCreate is the first version of a logical voucher.
	OpCreate Op = "CREATE"
	// OpAlter appends a modified version, preserving prior versions.
	OpAlter Op = "ALTER"
	// OpDelete appends a tombstone version.
	OpDelete Op = "DELETE"
)

// CanonicalPosting is the stable, serialization-friendly view of a posting used
// for hashing. Amounts are fixed-point strings so the hash is independent of
// decimal internal representation.
type CanonicalPosting struct {
	LedgerID string `json:"ledger_id"`
	DrCr     string `json:"dr_cr"`
	Amount   string `json:"amount"`
}

// CanonicalBillAllocation is the stable view of a bill-wise allocation used for
// hashing. Amounts are fixed-point strings.
type CanonicalBillAllocation struct {
	LedgerID string `json:"ledger_id"`
	RefName  string `json:"ref_name"`
	RefType  string `json:"ref_type"`
	DrCr     string `json:"dr_cr"`
	Amount   string `json:"amount"`
	DueDate  string `json:"due_date"`
}

// CanonicalStockLine is the stable view of ONE inventory line used for hashing.
//
// EVERY FIELD IS A STRING, DELIBERATELY. The chain is verified by decoding the
// stored JSONB back into this struct and re-marshalling it (see history.go):
// Postgres does not preserve the input text of a jsonb column, so the bytes
// that were hashed have to be RE-DERIVED. That re-derivation is only exact
// while every value is a string or an int. A float quantity would break it in
// two ways at once — 12.3400 and 12.34 would hash identically (so a corrected
// scale would be undetectable), and two quantities that differ below float64's
// ~16 significant digits would collide outright. Quantity is fixed-4 for the
// same reason money is.
//
// An absent dimension is the empty string, not the zero uuid: "" reads as "not
// tracked" and cannot be confused with a real id.
type CanonicalStockLine struct {
	StockItemID string `json:"stock_item_id"`
	GodownID    string `json:"godown_id"`
	BatchID     string `json:"batch_id"`
	Direction   string `json:"direction"`
	Qty         string `json:"qty"`
	Rate        string `json:"rate"`
	DiscountPct string `json:"discount_pct"`
	Amount      string `json:"amount"`
}

// CanonicalInward is the SUPPLIER's own document identity and the ITC
// classification for this voucher.
//
// It is in the hash because "which supplier document is this input tax credit
// claimed against" is the question a GST officer asks first. That makes it part
// of the voucher's AUDIT IDENTITY, not metadata beside it — an unhashed answer
// is one that anything with UPDATE can change and nobody can defend.
//
// EVERY FIELD IS A STRING, for the same reason CanonicalStockLine's are:
// VerifyChain re-derives the hashed bytes by decoding the stored JSONB back
// into this struct and re-marshalling it, and that re-derivation is exact only
// while every value is a string. A date decoded as time.Time would re-marshal
// with a different layout and declare the whole book tampered with.
type CanonicalInward struct {
	// SupplierInvoiceNo is the number the supplier printed, VERBATIM as keyed.
	// Never normalised here: normalisation lives in exactly one place
	// (itc.NormalizeDocNo, applied at match time), because a normalisation
	// stated twice is a normalisation that drifts — and a normalisation baked
	// into the hash could never be corrected afterwards.
	SupplierInvoiceNo string `json:"supplier_invoice_no"`
	// SupplierInvoiceDate is an ISO date (YYYY-MM-DD) or "". Statutory: it fixes
	// the financial year for section 16(4), and therefore the date this credit
	// expires.
	SupplierInvoiceDate string `json:"supplier_invoice_date"`
	// ITCEligibility is the per-voucher OVERRIDE of the party/expense ledger's
	// default. "" means "the ledger's default applies" — it is NOT a synonym for
	// ELIGIBLE written out, because the ledger may say BLOCKED_17_5 and the
	// empty string must not silently overrule it.
	ITCEligibility string `json:"itc_eligibility"`
	// ITCBlockedClause is free text, e.g. "17(5)(a)". Deliberately unconstrained
	// here and in the schema: the clause list in circulation is secondary
	// material pending re-verification against the bare Act.
	ITCBlockedClause string `json:"itc_blocked_clause"`
}

// CanonicalCostAllocation is the stable view of ONE cost-centre allocation of
// ONE posting used for hashing.
//
// WHY THIS IS IN THE HASH AT ALL. An allocation is not a report setting; it is
// the answer to "which department bore this cost", which is the number a
// manager's budget and a segment disclosure are both computed from. Left
// unhashed it would be a figure anything with UPDATE could move after the fact,
// with the chain still certifying the voucher as untouched. Postings are hashed
// for that reason and this is the same reason.
//
// ⛔ PostingSeq, NOT a posting id. `posting` rows are re-created wholesale by
// every ALTER (see the posting_immutable trigger and HANDOFF §7.5), so a
// posting id in the hash would be a value that changes on correction — the
// signed bytes of version 2 would disagree with version 1 about which row a
// perfectly unchanged allocation belonged to. The 0-based INDEX into this same
// payload's `postings` array is self-contained: it names a posting inside the
// very object being signed, so it cannot go stale and needs no lookup to check.
// It is also why ledger_id is NOT the key — one voucher may legally post twice
// to the same ledger, and then a ledger_id key is ambiguous.
//
// EVERY FIELD IS A STRING, including the sequence number, for the reason
// CanonicalStockLine records: VerifyChain re-derives the hashed bytes by
// decoding the stored JSONB back into this struct and re-marshalling it, and
// that re-derivation is exact only while every value is a string. The sequence
// could safely be an int, but a mixed struct invites the next author to add a
// float beside it.
type CanonicalCostAllocation struct {
	// PostingSeq is the 0-based index into this payload's Postings slice. See
	// the type comment for why this and not a posting id.
	PostingSeq string `json:"posting_seq"`
	// CostCategoryID is the AXIS this allocation is drawn on. It is NOT optional
	// and NOT the empty string: a cost centre without its category is the defect
	// the plan warns about, because two categories may each allocate the SAME
	// posting in full (parallel allocation) and only the category tells the two
	// sets apart. An allocation with no category would be summed together with
	// every other and would break the sum-to-posting invariant for reasons that
	// look like an arithmetic bug.
	CostCategoryID string `json:"cost_category_id"`
	// CostCentreID is the centre within that category that bore the cost.
	CostCentreID string `json:"cost_centre_id"`
	// Amount is a fixed-4 string, exactly as CanonicalPosting.Amount is. A
	// MAGNITUDE, never a sign: the posting's own dr_cr carries the direction, so
	// an allocation that forgot it reads obviously wrong rather than subtly so.
	Amount string `json:"amount"`
}

// CanonicalFulfilment is the stable view of ONE order-line fulfilment used for
// hashing: "this delivery note line delivers 5 of the 20 that sales order line
// committed".
//
// ⛔⛔ AN ORDER IS A COMMITTED QUANTITY, NOT A MOVEMENT, and this struct is the
// only place the two shapes touch. It records a LINK, never a stock position:
// the quantity here says how much of an ORDER this voucher consumed, and the
// quantity in CanonicalStockLine says how much STOCK actually moved. They are
// usually equal and they are not the same fact — closing stock is computed from
// the movement and the order book from this, and nothing in either direction
// reads the other.
//
// WHY IT IS HASHED RATHER THAN A TABLE WRITTEN BESIDE THE VOUCHER. This is
// OPERATOR-KEYED INPUT — someone chose, at entry, which order this shipment
// fulfils — which puts it with inward_doc and NOT with bank_reconciliation.
// The distinction is worth stating because it decides the trap: 0045 and 0069
// key on voucher_id and store what they matched precisely BECAUSE they are
// metadata added later, by a different act, and can therefore be left stale by
// an ALTER they never see. A fulfilment cannot be left stale, because an ALTER
// RE-STATES it from the new input in the same transaction — so an alter that
// moves 5 units to a different order line says so in the signed bytes, and the
// outstanding balance recomputes from the CURRENT version with nothing to
// detect and nothing to repair.
//
// EVERY FIELD IS A STRING, for the reason given on CanonicalCostAllocation.
type CanonicalFulfilment struct {
	// LineNo is this voucher's 1-based inventory line, matching
	// stock_movement.line_no. It ties the fulfilment to the line it came from so
	// a three-item delivery note against a three-line order is unambiguous.
	LineNo string `json:"line_no"`
	// OrderLineID is the order line being consumed. An order line, not an order:
	// fulfilment is per item, and a delivery note routinely completes one line of
	// an order and leaves the rest outstanding.
	OrderLineID string `json:"order_line_id"`
	// Qty is a fixed-4 string, the quantity of the ORDER consumed by this line.
	// Partial fulfilment is the normal case, so this is very often less than the
	// order line's quantity — and it is the whole point of the feature that the
	// difference is visible.
	Qty string `json:"qty"`
}

// CanonicalPayload is the canonical, deterministic representation of a voucher
// version that is hashed and stored verbatim as the voucher_version.payload
// JSONB. Field order is fixed by the struct; encoding/json emits object keys in
// struct-declaration order, giving a stable byte sequence for a given input.
type CanonicalPayload struct {
	VoucherID     string             `json:"voucher_id"`
	VoucherTypeID string             `json:"voucher_type_id"`
	VersionNo     int                `json:"version_no"`
	Op            string             `json:"op"`
	Class         string             `json:"class"`
	VoucherNo     string             `json:"voucher_no"`
	VoucherDate   string             `json:"voucher_date"`
	Narration     string             `json:"narration"`
	Party         string             `json:"party"`
	Postings      []CanonicalPosting `json:"postings"`
	// BillAllocations is omitted from the JSON (and so from the hash) when empty,
	// preserving the byte sequence — and therefore the hash — of vouchers that
	// carry no bill-wise detail.
	BillAllocations []CanonicalBillAllocation `json:"bill_allocations,omitempty"`
	// Inventory is the stock lines this version records.
	//
	// THE HASH CHAIN IS A ONE-WAY DOOR, so this field obeys three rules that are
	// not stylistic:
	//   * it is LAST — encoding/json emits keys in struct-declaration order, so
	//     inserting it anywhere else would move every following key and change
	//     the bytes of every voucher ever written;
	//   * it is `omitempty` — a voucher with no stock lines must marshal to
	//     BYTE-IDENTICAL JSON to what it marshalled to before this field
	//     existed, or every historical hash is retroactively invalidated and
	//     VerifyChain reports the entire book as tampered with;
	//   * its element type is all-strings (see CanonicalStockLine) so the
	//     JSONB -> struct -> bytes re-derivation VerifyChain depends on is exact.
	// The pinned vector in stock_hash_test.go exists to make a violation of the
	// first two rules a failing test rather than a corrupted audit log.
	Inventory []CanonicalStockLine `json:"inventory,omitempty"`
	// Inward is the supplier's document identity and this voucher's ITC
	// classification. It obeys the SAME THREE RULES as Inventory above, and each
	// of them is a failing test rather than a style note:
	//
	//   * it is LAST, AFTER Inventory — encoding/json emits keys in
	//     struct-declaration order, so putting it anywhere else moves every
	//     following key and changes the bytes of every voucher ever written.
	//     TestInwardDocIsLastInTheHashAndOmittedWhenEmpty is the ONLY test that
	//     can see a violation, and that was established by trying it rather than
	//     by assuming it: stock_hash_test.go's pinned vectors carry neither
	//     stock lines nor an inward document, and both fields are omitempty, so
	//     swapping their declaration order leaves those vectors byte-identical
	//     and TestExistingVoucherHashesAreUnchanged PASSES. Only a vector that
	//     sets BOTH fields can observe the order at all.
	//
	//   * the OUTER POINTER is `omitempty` — a voucher with no inward detail,
	//     which is every voucher posted before migration 0032 and every sales
	//     voucher forever, must marshal BYTE-IDENTICALLY to what it marshalled
	//     to before this field existed. Otherwise VerifyChain retroactively
	//     reports every book in the field as tampered with, which is
	//     indistinguishable from an actual attack.
	//
	//   * the FOUR INNER FIELDS are plain strings with NO omitempty — once the
	//     object is present its shape is FIXED and cannot vary with its content.
	//     With omitempty on the inner fields, a voucher whose clause happened to
	//     be blank would hash under a different key set from one whose clause was
	//     "17(5)(a)" and then cleared, so the same recorded facts would produce
	//     two different signatures depending on their history.
	Inward *CanonicalInward `json:"inward,omitempty"`
	// CostAllocations is how this voucher's postings were split across cost
	// centres, and Fulfilments is which order lines its inventory consumed.
	//
	// ⛔⛔ THE ORDER OF THESE TWO FIELDS IS FIXED FOREVER AND WAS CHOSEN BEFORE
	// EITHER FEATURE WAS BUILT. Read this before adding a third.
	// -----------------------------------------------------------------------
	// Both of these arrived in the same session, and each independently "had to
	// be last" under the rule Inward records above. They cannot both be last, so
	// the tie was broken ONCE, here, and written down: CostAllocations then
	// Fulfilments, alphabetical, for no better reason than that a rule with an
	// arbitrary tie-break is still a rule and a rule with none is a coin flip
	// the next author loses.
	//
	// What actually matters is that the decision is made in ONE place at ONE
	// time. Had the two features been built in separate lanes each appending
	// "at the end", whichever merged second would have moved the other's key and
	// silently invalidated every hash written between the two merges — a window
	// in which real vouchers get signed. There is no test that could have caught
	// that after the fact, because both fields are omitempty and every existing
	// pinned vector sets neither.
	//
	// Each obeys the same three rules Inward does, and for the same reasons:
	//   * they are LAST, in the order declared here, and nothing may be inserted
	//     above them — that would move every following key and change the bytes
	//     of every voucher ever written;
	//   * each is `omitempty` — a voucher with no allocations and no fulfilments,
	//     which is every voucher posted before this session and most vouchers
	//     forever, must marshal BYTE-IDENTICALLY to what it always did, or
	//     VerifyChain retroactively reports every book in the field as tampered
	//     with, which is indistinguishable from an actual attack;
	//   * their element types are all-strings, so the JSONB -> struct -> bytes
	//     re-derivation VerifyChain depends on is exact.
	//
	// TestTheTwoNewOptionalFieldsAreLastAndInTheirPinnedOrder is the ONLY test
	// that can observe the third rule, and that is established by having tried
	// it rather than by assuming: every OTHER pinned vector leaves both fields
	// empty, so swapping their declaration order keeps those vectors
	// byte-identical and they all still PASS. Only a vector that sets BOTH can
	// see the order at all — the same lesson Inward's comment records, applied
	// one field further along.
	CostAllocations []CanonicalCostAllocation `json:"cost_allocations,omitempty"`
	Fulfilments     []CanonicalFulfilment     `json:"fulfilments,omitempty"`
}

// Marshal returns the canonical JSON encoding of the payload.
func (p CanonicalPayload) Marshal() ([]byte, error) {
	return json.Marshal(p)
}

// ChainHash computes the BLAKE3-256 hash over the canonical payload bytes
// concatenated with the previous version's hash, forming a per-voucher hash
// chain. For the first version prevHash is nil and only the payload is hashed.
func ChainHash(payload, prevHash []byte) []byte {
	h := blake3.New()
	_, _ = h.Write(payload)
	if len(prevHash) > 0 {
		_, _ = h.Write(prevHash)
	}
	sum := h.Sum(nil)
	return sum
}
