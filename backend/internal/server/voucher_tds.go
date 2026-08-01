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
// THAT IS NOW PERSISTED, and the paragraph that used to sit here saying it was
// not is gone. The section, deductee, deductee type, PAN-availability, rate and
// derived amount are written to migration 0050's tds_deduction inside the post
// transaction, so the register and the Form 140 return have something to read.
// See internal/ledger/tds.go for the order the post path runs them in.
//
// ⚠️ TWO CONSEQUENCES FOR A CLIENT OF THIS ENDPOINT, both of them 422s:
//
//   - `deductee_ledger_id` is now REQUIRED on a TDS block. Who was paid is half
//     the key of the s.393(1) aggregate and the whole of the return's deductee
//     annexure.
//   - The company must have a `company_deductor_status` row for the tax year
//     PRECEDING the voucher's, or the post is refused outright — whether there
//     is a duty to deduct is not a question this software guesses at.
type tdsReq struct {
	// Section is the nature-of-payment key, e.g. "194C", "194J-P", "194I-B".
	Section string `json:"section"`
	// DeducteeLedgerID is the ledger of the party being paid. REQUIRED: the
	// aggregate is per deductee and the return is filed per deductee.
	DeducteeLedgerID string `json:"deductee_ledger_id"`
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
	// Parsed with optionalUUID like every other id on this body, so a malformed
	// value is a 400 here and an ABSENT one is left uuid.Nil. Whether Nil is an
	// error is the ledger's call, not this layer's: it refuses with
	// ErrDeducteeUnknown, which surfaces as a 422 naming the missing deductee.
	// Duplicating that decision here would give two places to keep in step.
	if out.DeducteeLedgerID, err = optionalUUID(t.DeducteeLedgerID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid tds deductee_ledger_id")
		return nil, false
	}
	return out, true
}
