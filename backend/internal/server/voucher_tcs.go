package server

import (
	"net/http"

	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/ledger"
)

// tcsReq is the optional `tcs` block on a Sales voucher request body — the
// collection-side mirror of `tds`, and Item Zero step 3.
//
// ⚠️ THERE IS NO EXEMPTION FIELD HERE, AND THAT IS THE DESIGN. s.394(2) exempts
// a buyer who has FURNISHED A WRITTEN DECLARATION, which the collector holds and
// must forward to the Commissioner by the 7th of the following month
// (s.394(3)). If this block carried a `declaration: true` flag, the exemption
// would become an entry-time opinion — an operator could zero the tax on any
// sale by typing a boolean, and no document would exist behind it.
//
// So the post path LOOKS THE DECLARATION UP by (buyer, section, tax year) from
// the table that POST /tcs-declarations writes. What the client sends is only
// the facts of the sale; whether it is exempt is decided from stored evidence.
//
// The same reasoning applies to `pan_available`, which IS here — that is a fact
// about the buyer master rather than a claim about a document, and getting it
// wrong raises the rate rather than removing it.
type tcsReq struct {
	// Section is the collection key: "206C(1)-SCRAP" or "206C(1F)-MV".
	Section string `json:"section"`
	// BuyerLedgerID is REQUIRED: it is the declaration's lookup key and the
	// return's collectee.
	BuyerLedgerID string `json:"buyer_ledger_id"`
	// PANAvailable false triggers the s.397(2)(b)(ii) penal rate — higher of
	// twice the rate or 5%, capped at 20%. NOT the TDS side's flat 20% floor.
	PANAvailable bool `json:"pan_available"`
	// TaxableAmount is a fixed-point decimal STRING (money is never a float).
	TaxableAmount string `json:"taxable_amount"`
	// TCSLedgerID is optional; when empty the collection is derived and
	// persisted but no posted leg is verified.
	TCSLedgerID string `json:"tcs_ledger_id"`
	// RulesetVersion selects the effective-dated rate table; empty means the
	// ledger's default (currently "tcs-2026-04").
	RulesetVersion string `json:"ruleset_version"`
}

// buildTCS converts the TCS request block into ledger.TCSParams, writing a 400
// and returning ok=false on any malformed field. It mirrors buildTDS exactly.
func buildTCS(w http.ResponseWriter, t tcsReq) (*ledger.TCSParams, bool) {
	out := &ledger.TCSParams{
		RulesetVersion: t.RulesetVersion,
		Section:        t.Section,
		PANAvailable:   t.PANAvailable,
	}
	amount, err := decimal.NewFromString(t.TaxableAmount)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid tcs taxable_amount")
		return nil, false
	}
	out.TaxableAmount = amount
	if out.TCSLedgerID, err = optionalUUID(t.TCSLedgerID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid tcs_ledger_id")
		return nil, false
	}
	// Parsed with optionalUUID like every other id on this body, so a malformed
	// value is a 400 here and an ABSENT one is left uuid.Nil. Whether Nil is an
	// error is the ledger's call: it refuses with ErrBuyerUnknown, which
	// surfaces as a 422 naming the missing buyer.
	if out.BuyerLedgerID, err = optionalUUID(t.BuyerLedgerID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid tcs buyer_ledger_id")
		return nil, false
	}
	return out, true
}
