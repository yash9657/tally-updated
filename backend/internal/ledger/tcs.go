package ledger

// TAX COLLECTED AT SOURCE ON THE POST PATH — Item Zero step 3.
//
// compliance.TCSDerive has existed as a pure, corpus-gated engine since T6's
// first slice and NOTHING CALLED IT. A sale of scrap collected no tax, and the
// s.394(2) declaration table written by migration 0049 was read by nobody. This
// file is the seam that ends both.
//
// ---------------------------------------------------------------------------
// IT IS THE MIRROR OF tds.go, AND THREE THINGS ARE DELIBERATELY NOT MIRRORED
// ---------------------------------------------------------------------------
//  1. THERE IS NO AGGREGATE AND NO CATCH-UP. Both shipped sections are
//     BasisPerTxn: s.394(1) Table Sl. No. 4 (scrap) states no threshold at all,
//     and Sl. No. 6 (motor vehicles) tests the SINGLE sale consideration
//     ("where the sale consideration exceeds ten lakh rupees"). Feeding a
//     running total to either would collect tax the section does not charge.
//     There is no TCSDeriveCumulative and there must not be one.
//
//  2. THERE IS NO LIABILITY GATE, AND THAT IS A DISCLOSED GAP RATHER THAN A
//     DECISION. On the deduction side s.402(11)/(37) narrows WHO must deduct,
//     and ledger.post refuses a company whose status is unrecorded. s.394(1)
//     charges "every person, being a seller", and the seller definition has NOT
//     been read against primary text from this environment — so no test is
//     modelled, and every company that posts a `tcs` block collects. That
//     direction OVER-collects rather than under-collects if it is wrong, which
//     is the safer error for the buyer and the recoverable one for the seller;
//     but it is a gap and it is listed in HANDOFF-FEATURE-LOCK.md.
//
//  3. THE DECLARATION IS READ FROM THE DATABASE, NEVER FROM THE REQUEST. A
//     s.394(2) declaration is a signed document the collector HOLDS, with a
//     s.394(3) obligation to forward a copy to the Commissioner. Letting a
//     voucher body assert "this buyer is exempt" would make the exemption an
//     entry-time opinion rather than a filed document, and would let an
//     operator zero the tax by typing a flag. So the post path looks it up by
//     (buyer, section, tax year) and the ONLY way to create one is the
//     declaration route.

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/compliance"
)

// DefaultTCSRulesetVersion is used when TCSParams.RulesetVersion is empty.
const DefaultTCSRulesetVersion = "tcs-2026-04"

// ErrTCSMismatch is returned when a posted TCS leg does not equal the amount the
// compliance engine derived, or sits on the wrong side of the entry.
var ErrTCSMismatch = errors.New("ledger: posted TCS leg does not match the derived collection")

// ErrBuyerUnknown is returned when a TCS block names no buyer.
//
// Required for the same reason the deductee is on the TDS side, plus one more
// that is specific to collection: the s.394(2) declaration is keyed on the
// BUYER, so without it the exemption cannot even be looked up and a buyer who
// filed a declaration would be charged anyway.
var ErrBuyerUnknown = errors.New(
	"ledger: a TCS collection needs the buyer ledger it was collected from")

// ErrTCSDateRequired is returned for a TCS voucher with no voucher date. Same
// reasoning as ErrTDSDateRequired: the date picks the tax year, and the tax year
// is what the quarter is filed for.
var ErrTCSDateRequired = errors.New(
	"ledger: a voucher carrying TCS must be dated; the collection date sets the tax year")

// TCSParams carries the inputs needed to derive and verify Tax Collected at
// Source on a Sales voucher.
//
// NOTE WHAT IS ABSENT: there is no Declaration field. The s.394(2) exemption is
// read from tcs_collection_declaration inside the post transaction — see the
// file header, point 3.
type TCSParams struct {
	// RulesetVersion selects the effective-dated rate table; empty means
	// DefaultTCSRulesetVersion.
	RulesetVersion string
	// Section is the collection key: "206C(1)-SCRAP" or "206C(1F)-MV".
	Section string
	// BuyerLedgerID is WHO bought. Required — it is the declaration's key and
	// the return's collectee.
	BuyerLedgerID uuid.UUID
	// PANAvailable reports whether the buyer's PAN is on record; false triggers
	// the s.397(2)(b)(ii) penal rate.
	PANAvailable bool
	// TaxableAmount is the sale consideration the collection applies to.
	TaxableAmount decimal.Decimal
	// TCSLedgerID, when set, identifies the TCS-payable ledger whose posted leg
	// is cross-checked against the derived collection. uuid.Nil derives (and
	// persists) without verifying.
	TCSLedgerID uuid.UUID
}

// readTCSDeclaration loads the buyer's s.394(2) declaration for a section and
// tax year, or nil when none was furnished.
//
// ⚠️ THE TAX YEAR IS THE SALE'S OWN, not the preceding one. This is the opposite
// of readDeductorStatus next door, and the difference is real rather than an
// inconsistency: s.402(37)(b) tests turnover "during the tax year immediately
// preceding", whereas a declaration simply governs the year it was furnished
// for. Confusing the two would silently ignore every declaration.
func readTCSDeclaration(
	ctx context.Context,
	tx pgx.Tx,
	companyID, buyerID uuid.UUID,
	section string,
	taxYearStart int,
) (*compliance.TCSDeclaration, error) {
	var (
		d   compliance.TCSDeclaration
		pan *string
	)
	d.Section = section
	err := tx.QueryRow(ctx,
		`SELECT buyer_is_resident, for_manufacturing, not_for_trading, buyer_pan
		   FROM tcs_collection_declaration
		  WHERE company_id = $1 AND buyer_ledger_id = $2
		    AND section = $3 AND tax_year_start = $4`,
		companyID, buyerID, section, taxYearStart,
	).Scan(&d.BuyerIsResident, &d.ForManufacturing, &d.NotForTrading, &pan)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// No declaration is the ORDINARY case and means "collect normally". It
		// is nil rather than a zero-valued declaration: a TCSDeclaration with
		// every flag false is a FURNISHED but failing document, which
		// compliance.TCSDerive refuses outright.
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("ledger: read tcs declaration: %w", err)
	}
	// s.397(2)(f)(i): a declaration without a valid PAN BECOMES INVALID, and (g)
	// then requires the penal rate. Migration 0049 keeps buyer_pan NULLABLE
	// precisely so that state is representable; collapsing NULL into "no
	// declaration" here would silently exempt nothing and charge the BASE rate,
	// which is the wrong answer in the one direction that looks plausible.
	d.BuyerPANValid = pan != nil
	return &d, nil
}

// deriveAndVerifyTCS runs the pure engine against the stored declaration and,
// when a TCS ledger id is supplied, verifies the posted leg.
//
// Like the TDS side it asserts the SIDE as well as the amount. A collection is
// money taken from the buyer and owed to the government, so it is always a
// CREDIT; a debit on the TCS ledger is an error even at the right magnitude.
func deriveAndVerifyTCS(in VoucherInput, decl *compliance.TCSDeclaration) (compliance.TCSResult, error) {
	p := in.TCS
	version := p.RulesetVersion
	if version == "" {
		version = DefaultTCSRulesetVersion
	}
	rs, err := compliance.LoadTCSRuleSet(version)
	if err != nil {
		return compliance.TCSResult{}, fmt.Errorf("ledger: tcs ruleset: %w", err)
	}
	res, err := compliance.TCSDerive(compliance.TCSInput{
		Section:       p.Section,
		PANAvailable:  p.PANAvailable,
		TaxableAmount: p.TaxableAmount,
		Declaration:   decl,
	}, rs)
	if err != nil {
		return compliance.TCSResult{}, fmt.Errorf("ledger: derive tcs: %w", err)
	}

	if p.TCSLedgerID == uuid.Nil {
		return res, nil
	}

	posted := decimal.Zero
	for _, pl := range in.Postings {
		if pl.LedgerID != p.TCSLedgerID {
			continue
		}
		if pl.DrCr != Credit {
			return compliance.TCSResult{}, fmt.Errorf(
				"%w: leg of %s is on the DEBIT side; a TCS collection is always a credit",
				ErrTCSMismatch, pl.Amount.StringFixed(4))
		}
		posted = posted.Add(pl.Amount)
	}
	if !posted.Equal(res.TCS) {
		return compliance.TCSResult{}, fmt.Errorf(
			"%w: posted %s != derived %s (section %s, rate %s%%, threshold %s, "+
				"applied=%v, s.394(2) declaration: %s)",
			ErrTCSMismatch, posted.StringFixed(4), res.TCS.StringFixed(4),
			p.Section, res.RatePct, res.Threshold.StringFixed(4), res.Applied,
			res.Declaration)
	}
	return res, nil
}

// deleteTCSCollections removes every collection row for a voucher, for the same
// reason deleteTDSDeductions does: migration 0067 keys on voucher_id, an ALTER
// re-states what the voucher IS NOW, and a sale altered to carry no TCS must
// lose its row rather than keep a stale one the return would file.
func deleteTCSCollections(ctx context.Context, tx pgx.Tx, companyID, voucherID uuid.UUID) error {
	if _, err := tx.Exec(ctx,
		`DELETE FROM tcs_collection WHERE company_id = $1 AND voucher_id = $2`,
		companyID, voucherID); err != nil {
		return fmt.Errorf("ledger: clear prior tcs collections: %w", err)
	}
	return nil
}

// writeTCSCollection persists the derived collection as migration 0067's row.
//
// ⚠️ IT WRITES A ROW EVEN WHEN NOTHING WAS COLLECTED. A sale exempted by a
// s.394(2) declaration is the one that MOST needs recording — the exemption
// rests on a document the department can demand, and s.394(3) puts a dated
// obligation on the collector to forward a copy. Do not add an "only if
// applied" guard.
func writeTCSCollection(
	ctx context.Context,
	tx pgx.Tx,
	companyID, voucherID uuid.UUID,
	in VoucherInput,
	res compliance.TCSResult,
	taxYearStart int,
	collectionDate string,
	recordedBy string,
) error {
	p := in.TCS
	version := p.RulesetVersion
	if version == "" {
		version = DefaultTCSRulesetVersion
	}
	outcome := res.Declaration
	if outcome == "" {
		outcome = compliance.DeclarationNone
	}
	rowID, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("ledger: new tcs collection id: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO tcs_collection
		   (id, company_id, voucher_id, leg_seq, buyer_ledger_id, section,
		    tax_year_start, collection_date, taxable_base_rupees, rate_pct,
		    tcs_amount_rupees, pan_available, declaration_outcome,
		    ruleset_version, recorded_by)
		 VALUES ($1, $2, $3, 0, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
		rowID, companyID, voucherID, p.BuyerLedgerID, p.Section,
		taxYearStart, collectionDate, p.TaxableAmount, res.RatePct,
		res.TCS, p.PANAvailable, string(outcome),
		version, recordedBy,
	); err != nil {
		return fmt.Errorf("ledger: insert tcs_collection: %w", err)
	}
	return nil
}
