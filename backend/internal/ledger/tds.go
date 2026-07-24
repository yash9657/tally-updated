package ledger

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/compliance"
)

// DefaultTDSRulesetVersion is used when TDSParams.RulesetVersion is empty.
const DefaultTDSRulesetVersion = "tds-2026-04"

// ErrTDSMismatch is returned when a posted TDS leg does not equal the amount
// the compliance engine derived (or sits on the wrong side of the entry). It is
// a sentinel so callers and tests can assert the specific failure rather than
// "some error happened".
var ErrTDSMismatch = errors.New("ledger: posted TDS leg does not match the derived deduction")

// TDSParams carries the inputs needed to derive and verify Tax Deducted at
// Source on a Purchase/Payment/Journal voucher. It mirrors GSTParams: when
// present the ledger calls compliance.TDSDerive and verifies the supplied TDS
// posting matches the derived amount, failing the post on mismatch. It NEVER
// injects postings of its own — the books stay exactly what the client sent,
// they are merely refused when they contradict the engine.
//
// DISCLOSED v1 LIMITATIONS (IMPLEMENTATION_PLAN.md §7.5):
//
//   - NOTHING IS PERSISTED. There is no nature-of-payment / section / deductee
//     schema, and voucher_version has no TDS columns, so a posted voucher
//     carries no TDS provenance: no 26Q, challan or TDS report can see it. The
//     derived value is surfaced in-process on PostResult.TDS only. A future
//     returns feature must add schema and backfill.
//   - Thresholds are applied PER TRANSACTION. For 194J/194H the limit is
//     statutorily an ANNUAL aggregate and for 194I a MONTHLY one, so v1
//     under-deducts on a stream of small payments. compliance.TDSResult.Basis
//     records the truth so the approximation is machine-visible.
//   - TCS (compliance.TCSDerive) is NOT wired into the post path in v1; it
//     exists as a pure engine only.
type TDSParams struct {
	// RulesetVersion selects the effective-dated rate table; empty means
	// DefaultTDSRulesetVersion.
	RulesetVersion string
	// Section is the nature-of-payment key, e.g. "194C", "194J-P", "194I-B".
	Section string
	// DeducteeType is compliance.DeducteeIndHUF or compliance.DeducteeOther.
	DeducteeType string
	// PANAvailable reports whether the deductee's PAN is on record; false
	// triggers the s.206AA floor.
	PANAvailable bool
	// TaxableAmount is the amount the deduction applies to.
	TaxableAmount decimal.Decimal
	// TDSLedgerID, when set, identifies the TDS-payable ledger whose posted leg
	// is cross-checked against the derived deduction. When it is uuid.Nil the
	// derivation still runs (and is returned) but nothing is verified.
	TDSLedgerID uuid.UUID
}

// deriveAndVerifyTDS runs the pure compliance engine and, when a TDS ledger id
// is supplied, verifies the posted leg equals the derived deduction.
//
// It is DELIBERATELY STRICTER than deriveAndVerifyGST: that function sums
// postings on the tax ledger ignoring Dr/Cr, so a tax leg posted on the wrong
// side still passes. A TDS deduction is always a CREDIT (a liability to the
// government withheld out of what the deductee is paid), so this function
// asserts the side as well as the amount — a Debit on the TDS ledger is an
// error even when its magnitude matches.
func deriveAndVerifyTDS(in VoucherInput) (compliance.TDSResult, error) {
	p := in.TDS
	version := p.RulesetVersion
	if version == "" {
		version = DefaultTDSRulesetVersion
	}
	rs, err := compliance.LoadTDSRuleSet(version)
	if err != nil {
		return compliance.TDSResult{}, fmt.Errorf("ledger: tds ruleset: %w", err)
	}
	deducteeType := p.DeducteeType
	if deducteeType == "" {
		deducteeType = compliance.DeducteeOther
	}
	res, err := compliance.TDSDerive(compliance.TDSInput{
		Section:       p.Section,
		DeducteeType:  deducteeType,
		PANAvailable:  p.PANAvailable,
		TaxableAmount: p.TaxableAmount,
	}, rs)
	if err != nil {
		return compliance.TDSResult{}, fmt.Errorf("ledger: derive tds: %w", err)
	}

	if p.TDSLedgerID == uuid.Nil {
		return res, nil
	}

	posted := decimal.Zero
	for _, pl := range in.Postings {
		if pl.LedgerID != p.TDSLedgerID {
			continue
		}
		if pl.DrCr != Credit {
			return compliance.TDSResult{}, fmt.Errorf(
				"%w: leg of %s is on the DEBIT side; a TDS deduction is always a credit",
				ErrTDSMismatch, pl.Amount.StringFixed(4))
		}
		posted = posted.Add(pl.Amount)
	}
	if !posted.Equal(res.TDS) {
		return compliance.TDSResult{}, fmt.Errorf(
			"%w: posted %s != derived %s (section %s, rate %s%%, threshold %s, applied=%v)",
			ErrTDSMismatch, posted.StringFixed(4), res.TDS.StringFixed(4),
			p.Section, res.RatePct, res.Threshold.StringFixed(4), res.Applied)
	}
	return res, nil
}
