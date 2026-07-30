package server

import (
	"net/http"

	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/ledger"
)

// tdsReq is the optional `tds` block on a voucher request body. It mirrors the
// `gst` block: the client declares the nature of payment and the amount the
// deduction applies to, plus the TDS-payable ledger whose posted leg the server
// cross-checks against compliance.TDSDerive. The server NEVER injects a
// posting — a voucher whose TDS leg contradicts the engine is refused.
//
// THIS IS A LIVE PATH. It was dead when written — the comment here used to say
// so, and said it was unreachable until an integrator applied a two-hunk wiring
// diff. That diff LANDED: `voucherReq` carries `TDS *tdsReq` (server.go) and
// buildVoucherInput calls buildTDS. Verified end-to-end over HTTP: a Journal
// carrying a `tds` block returns a derived deduction in the response.
//
// The stale comment mattered, because "not a live path at all" is exactly the
// sentence that stops a reviewer looking. No new route is created, so the
// two-touch rule still does not apply and auth_middleware.go still needs no
// routeAction entry of its own.
//
// WHAT IS STILL TRUE, AND IS T6's WHOLE PROBLEM: nothing here is PERSISTED. The
// section, deductee type, PAN-availability, rate and derived amount are used to
// verify the posted leg and are then discarded — they are in neither a column
// nor the hashed canonical payload, so a posted TDS voucher leaves behind only
// an unexplained credit to a TDS ledger and no return can be filed from it.
// See docs/design/t6-model.md §0.1, where that is measured rather than assumed.
type tdsReq struct {
	// Section is the nature-of-payment key, e.g. "194C", "194J-P", "194I-B".
	Section string `json:"section"`
	// DeducteeType is "IND_HUF" or "OTHER"; empty defaults to "OTHER" in the
	// ledger layer.
	DeducteeType string `json:"deductee_type"`
	// PANAvailable false triggers the s.206AA floor.
	PANAvailable bool `json:"pan_available"`
	// TaxableAmount is a fixed-point decimal STRING (money is never a float).
	TaxableAmount string `json:"taxable_amount"`
	// TDSLedgerID is optional; when empty the deduction is derived but no
	// posted leg is verified.
	TDSLedgerID string `json:"tds_ledger_id"`
	// RulesetVersion selects the effective-dated rate table; empty means the
	// ledger's default (currently "tds-2026-04").
	RulesetVersion string `json:"ruleset_version"`
}

// buildTDS converts the TDS request block into ledger.TDSParams, writing a 400
// and returning ok=false on any malformed field. It mirrors buildGST exactly.
func buildTDS(w http.ResponseWriter, t tdsReq) (*ledger.TDSParams, bool) {
	out := &ledger.TDSParams{
		RulesetVersion: t.RulesetVersion,
		Section:        t.Section,
		DeducteeType:   t.DeducteeType,
		PANAvailable:   t.PANAvailable,
	}
	amount, err := decimal.NewFromString(t.TaxableAmount)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid tds taxable_amount")
		return nil, false
	}
	out.TaxableAmount = amount
	if out.TDSLedgerID, err = optionalUUID(t.TDSLedgerID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid tds_ledger_id")
		return nil, false
	}
	return out, true
}
