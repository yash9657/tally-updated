package rules

// einvoice_wire.go — THE ONE DEFINITION OF THE cored -> statutoryd e-INVOICE
// WIRE CONTRACT.
//
// ===========================================================================
// ⛔⛔ WHY THESE TWO STRUCTS LIVE IN THE PURE LEAF AND NOT IN EITHER CALLER
// ===========================================================================
// The e-invoice call graph is cored -> statutoryd -> cored. cored builds the
// request; statutoryd decodes it. Those are two SEPARATE DEPLOYABLES and
// archtest forbids `internal/server` (and `internal/einvoice`) from importing
// `internal/statutory` at all, so the obvious arrangement — one struct in
// statutoryd, another in cored — puts TWO definitions of the same JSON document
// in the tree with nothing holding them together.
//
// That is the exact defect shape this repository has shipped repeatedly: both
// halves compile, both look authoritative, and the first renamed field produces
// an e-invoice built from a request whose `lines` the server silently ignored —
// which surfaces, three hops later, as
// `[ItemList: 0 items, minimum 1]` blaming the item list for a wire mismatch.
//
// `internal/statutory/rules` is the one package BOTH sides may name: cored
// imports it for the pure Rule 138 core and the voucher-fact vocabulary, and
// internal/statutory's allowed set is exactly {rules}. So the contract lives
// here, is imported by both, and CANNOT drift. internal/statutory re-exports it
// by ALIAS (einvoice.go), exactly as it already does for VoucherSnapshot.
//
// ⚠️ This file adds NO I/O to the pure leaf and must never acquire any. It is a
// data shape with json tags — nothing here reads a clock, a socket or a store,
// so rules stays the network-free leaf archtest pins it to.
//
// ⚠️ NOTE WHAT `lines` SERIALISES AS. SnapshotLine carries NO json tags, so its
// keys are the Go field names ("Desc", "HSN", "Qty", …). That is the contract
// statutoryd already accepted before this file existed and it is left alone
// deliberately: nothing outside Go ever authors this document. The BROWSER
// sends cored `{voucher_id, created_by}` and cored builds the lines from the
// books, which is the whole architectural point (HANDOFF §1.4 B.1 item 1).

// EInvoiceRequest is the body of
// POST /companies/{companyID}/vouchers/{voucherID}/e-invoice on statutoryd.
//
// The seller/buyer identity is REQUIRED of the caller because statutoryd cannot
// read cored's masters: its CoredClient.GetVoucher maps five amount fields and
// nothing else. The lines are required for the same reason and one level deeper
// — the NIC schema demands at least one item, and only cored holds the
// inventory rows, the stock masters, the HSN and the rate.
type EInvoiceRequest struct {
	SellerGstin     string `json:"seller_gstin"`
	SellerLegalName string `json:"seller_legal_name"`
	SellerStateCode string `json:"seller_state_code"`
	BuyerGstin      string `json:"buyer_gstin"`
	BuyerLegalName  string `json:"buyer_legal_name"`
	BuyerStateCode  string `json:"buyer_state_code"`

	// Lines is the invoice's item list. Empty means "statutoryd has none
	// either", which fails NIC conformance with `ItemList: 0 items, minimum 1`.
	Lines []SnapshotLine `json:"lines,omitempty"`

	// Optional amount overrides. Blank means "use the value statutoryd fetched
	// from cored's voucher GET".
	Taxable      string `json:"taxable,omitempty"`
	CGST         string `json:"cgst,omitempty"`
	SGST         string `json:"sgst,omitempty"`
	IGST         string `json:"igst,omitempty"`
	InvoiceTotal string `json:"invoice_total,omitempty"`
}

// EInvoiceResponse is statutoryd's 200 body.
//
// ⚠️ AckNo IS THE PROVIDER'S ACKNOWLEDGEMENT AND IS NOT PERSISTED ANYWHERE.
// cored's voucher_external_reference holds the IRN and the pinned number only
// (migration 0009 has no column for an ack number or its timestamp), so this
// value is available on the raise and never again. A screen that wants it must
// show it at raise time.
type EInvoiceResponse struct {
	IRN             string `json:"irn"`
	AckNo           string `json:"ack_no"`
	Status          string `json:"status"`
	PinnedVoucherNo string `json:"pinned_voucher_no"`
}

// Overlay applies the request's identity block and its optional amount/line
// overrides onto a snapshot, returning the result.
//
// ⛔ IT IS A PURE FUNCTION AND IT LIVES BESIDE THE REQUEST TYPE ON PURPOSE. The
// overlay used to be twenty lines inlined in statutoryd's HTTP handler, where
// it was untestable without a socket and invisible to the caller building the
// request. The identity fields overwrite UNCONDITIONALLY (cored's voucher JSON
// carries none, so there is nothing to preserve); the amount fields overwrite
// only when non-blank, which is what makes them OVERRIDES rather than a second
// mandatory copy of figures statutoryd already fetched.
func (req EInvoiceRequest) Overlay(snap VoucherSnapshot) VoucherSnapshot {
	snap.SellerGstin = req.SellerGstin
	snap.SellerLegalName = req.SellerLegalName
	snap.SellerStateCode = req.SellerStateCode
	snap.BuyerGstin = req.BuyerGstin
	snap.BuyerLegalName = req.BuyerLegalName
	snap.BuyerStateCode = req.BuyerStateCode

	if len(req.Lines) > 0 {
		snap.Lines = req.Lines
	}
	if req.Taxable != "" {
		snap.Taxable = req.Taxable
	}
	if req.CGST != "" {
		snap.CGST = req.CGST
	}
	if req.SGST != "" {
		snap.SGST = req.SGST
	}
	if req.IGST != "" {
		snap.IGST = req.IGST
	}
	if req.InvoiceTotal != "" {
		snap.InvoiceTotal = req.InvoiceTotal
	}
	return snap
}
