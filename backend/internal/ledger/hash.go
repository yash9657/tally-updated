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
