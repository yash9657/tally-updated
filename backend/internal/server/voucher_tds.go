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
// DELIBERATELY DEAD CODE AT COMMIT TIME. Go cannot add a field to a struct from
// another file, so `tds` is unreachable until the integrator applies the
// two-hunk wiring diff reported by this workstream (a `TDS *tdsReq` field on
// `voucherReq` and a `buildTDS` call in `buildVoucherInput`). Unused types and
// funcs compile and vet cleanly, so `go build`/`go vet` stay green meanwhile.
// Reviewers: this is NOT an untested live path — it is not a live path at all
// until that diff lands. No new route is created, so §1.5's two-touch rule does
// not apply and auth_middleware.go needs no routeAction entry.
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
