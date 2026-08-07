// Package statutory implements the IRN e-invoice generation plane (statutoryd).
//
// It is a SEPARATE deployable from cored: it must not import internal/ledger or
// internal/server. The only coupling to cored is over HTTP via the freeze-hook
// contract (see cored_client.go). The package builds an NIC e-invoice payload
// from a voucher snapshot, validates it against a recorded NIC golden schema,
// generates an IRN through a pluggable ProviderAdapter, and registers the IRN
// back into cored (which pins/freezes the voucher's display number).
//
// IRN generation runs through a self-contained, durable, idempotent, resumable
// in-process workflow runner (runner.go) — there is no River/Temporal. The
// runner memoizes each step's result so the external GenerateIRN call happens
// EXACTLY ONCE even across retries and resumes (no double-IRN).
package statutory

import (
	"context"
	"errors"
)

// EInvoicePayload is the subset of the NIC e-invoice schema this slice emits.
// All money values are STRINGS in fixed-2 precision to match NIC, which rejects
// trailing-zero ambiguity and requires exactly two decimal places.
type EInvoicePayload struct {
	Version    string         `json:"Version"`
	TranDtls   TranDtls       `json:"TranDtls"`
	DocDtls    DocDtls        `json:"DocDtls"`
	SellerDtls PartyDtls      `json:"SellerDtls"`
	BuyerDtls  PartyDtls      `json:"BuyerDtls"`
	ItemList   []EInvoiceItem `json:"ItemList"`
	ValDtls    ValDtls        `json:"ValDtls"`
}

// TranDtls carries transaction-level flags. TaxSch is always "GST", SupTyp is
// "B2B" for this slice, RegRev "N" (reverse charge out of scope here).
type TranDtls struct {
	TaxSch string `json:"TaxSch"`
	SupTyp string `json:"SupTyp"`
	RegRev string `json:"RegRev"`
}

// DocDtls is the document identity. Typ is "INV" for a tax invoice.
type DocDtls struct {
	Typ string `json:"Typ"`
	No  string `json:"No"`
	Dt  string `json:"Dt"`
}

// PartyDtls is the shared shape for seller and buyer identity blocks.
type PartyDtls struct {
	Gstin string `json:"Gstin"`
	LglNm string `json:"LglNm"`
	Pos   string `json:"Pos"`
	Addr1 string `json:"Addr1"`
	Loc   string `json:"Loc"`
	Pin   string `json:"Pin"`
	Stcd  string `json:"Stcd"`
}

// EInvoiceItem is one line of the ItemList. Money fields are fixed-2 strings,
// with the single exception of UnitPrice — a RATE, which NIC models as
// decimal(12,3) and which must reproduce TotAmt when multiplied by Qty.
//
// ⛔⛔ THE FOUR AMOUNT FIELDS ARE AN ARITHMETIC STATEMENT, NOT FOUR INDEPENDENT
// NUMBERS, and ValidateEInvoice now checks it:
//
//	TotAmt     = Qty x UnitPrice          (the gross value of the goods)
//	AssAmt     = TotAmt - Discount        (the value GST is charged on)
//	TotItemVal = AssAmt + Igst + Cgst + Sgst
//
// This shipped with Discount ABSENT and TotAmt set equal to AssAmt, so a
// discounted line declared a gross of 4500.00 for 10 units priced at 500.00 —
// self-contradictory by ₹500, un-noticed because nothing checked the item
// arithmetic and no test posted a discount.
type EInvoiceItem struct {
	SlNo      string `json:"SlNo"`
	PrdDesc   string `json:"PrdDesc"`
	HsnCd     string `json:"HsnCd"`
	Qty       string `json:"Qty"`
	Unit      string `json:"Unit"`
	UnitPrice string `json:"UnitPrice"`
	TotAmt    string `json:"TotAmt"`
	// Discount is the line discount in currency. "0.00" when none was given.
	Discount   string `json:"Discount"`
	AssAmt     string `json:"AssAmt"`
	GstRt      string `json:"GstRt"`
	IgstAmt    string `json:"IgstAmt"`
	CgstAmt    string `json:"CgstAmt"`
	SgstAmt    string `json:"SgstAmt"`
	TotItemVal string `json:"TotItemVal"`
}

// ValDtls is the invoice-level value summary. Footing: TotInvVal must equal
// AssVal + CgstVal + SgstVal + IgstVal.
type ValDtls struct {
	AssVal    string `json:"AssVal"`
	CgstVal   string `json:"CgstVal"`
	SgstVal   string `json:"SgstVal"`
	IgstVal   string `json:"IgstVal"`
	TotInvVal string `json:"TotInvVal"`
}

// IRNResult is what a ProviderAdapter returns on a successful generation.
type IRNResult struct {
	IRN          string `json:"irn"`
	AckNo        string `json:"ack_no"`
	AckDt        string `json:"ack_dt"`
	SignedQRCode string `json:"signed_qr_code"`
	Status       string `json:"status"`
}

// ProviderAdapter abstracts the IRP/NIC provider. The deterministic fake and a
// future real NIC client both satisfy it.
type ProviderAdapter interface {
	GenerateIRN(ctx context.Context, p EInvoicePayload) (IRNResult, error)
	Name() string
}

// Provider error taxonomy. Each carries a Retryable() classification so the
// workflow can distinguish a transient fault (resume) from a permanent reject
// (abort). Transient errors leave the workflow resumable; the runner's step
// memoization guarantees the provider is still hit exactly once overall.
type ProviderError struct {
	Code      string
	Message   string
	retryable bool
}

func (e *ProviderError) Error() string { return "statutory: provider " + e.Code + ": " + e.Message }

// Retryable reports whether re-running the workflow may eventually succeed.
func (e *ProviderError) Retryable() bool { return e.retryable }

var (
	// ErrProviderOutage is a transient outage (HTTP 5xx / connection reset).
	ErrProviderOutage = &ProviderError{Code: "OUTAGE", Message: "provider unavailable", retryable: true}
	// ErrTokenExpired means the auth token must be refreshed; retryable.
	ErrTokenExpired = &ProviderError{Code: "TOKEN_EXPIRED", Message: "auth token expired", retryable: true}
	// ErrRateLimited means back off and retry.
	ErrRateLimited = &ProviderError{Code: "RATE_LIMITED", Message: "rate limited", retryable: true}
	// ErrPermanentReject is a non-retryable rejection (duplicate IRN, bad GSTIN
	// at the IRP, schema reject). The workflow aborts and no freeze happens.
	ErrPermanentReject = &ProviderError{Code: "PERMANENT_REJECT", Message: "permanently rejected by IRP", retryable: false}
)

// IsRetryable reports whether err is a retryable provider error. A nil error or
// a non-provider error is treated as non-retryable.
func IsRetryable(err error) bool {
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.Retryable()
	}
	return false
}
