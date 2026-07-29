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
