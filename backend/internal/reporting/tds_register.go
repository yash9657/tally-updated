package reporting

// THE TDS / TCS REGISTER — Item Zero step 4, and docs/design/t6-model.md §1.4.
//
// Everything Item Zero built so far is invisible. Deductions and collections are
// derived correctly, refused correctly and stored correctly, and there has been
// NO WAY TO LOOK AT THEM. This is the report that ends that, and it is the last
// piece of the debt that can be built from what has actually been read.
//
// ---------------------------------------------------------------------------
// WHAT THIS DELIBERATELY DOES NOT SHOW, AND WHY IT IS NOT AN OVERSIGHT
// ---------------------------------------------------------------------------
// t6-model.md §1.4 lists "challan reference, deposit status" per deduction, and
// §1.5 wants a challan register beside this one. NEITHER IS HERE: no
// `tds_challan` table exists, so there is nothing to join to. Showing an empty
// "Challan" column would read as "not yet deposited" for every row, which is a
// FALSE STATEMENT about the one fact that attracts interest under s.396 — so the
// column is absent and the response says so in `disclosures` rather than
// rendering a blank the operator has to interpret.
//
// ---------------------------------------------------------------------------
// I-9: IT READS THE CURRENT VOUCHER VERSION ONLY, AND GETS THAT STRUCTURALLY
// ---------------------------------------------------------------------------
// `tds_deduction` and `tcs_collection` are keyed on voucher_id, re-derived on
// ALTER and deleted on DELETE (ledger/tds.go, ledger/tcs.go). So there is at
// most one live row per voucher per section and a tombstoned voucher has none —
// this report cannot double-count an altered voucher the way the printed Day
// Book does, because the rows it reads were replaced rather than appended.
// TestRegisterCountsAnAlteredVoucherOnce and
// TestRegisterExcludesADeletedVoucher pin both halves against the tables rather
// than against that reasoning.

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// TDSRegisterRow is one deduction, joined to the names a human needs.
type TDSRegisterRow struct {
	Date         string
	VoucherNo    string
	VoucherID    uuid.UUID
	Deductee     string
	Section      string
	DeducteeType string
	// ReturnCode is the Form 140 Annexure 2 payment code stored at derivation
	// time. Shown because it is what the return will file this row under, and an
	// operator checking a return against the register needs to see the same key.
	ReturnCode   string
	BaseRupees   decimal.Decimal
	RatePct      decimal.Decimal
	TDSRupees    decimal.Decimal
	PANAvailable bool
}

// TCSRegisterRow is one collection.
type TCSRegisterRow struct {
	Date       string
	VoucherNo  string
	VoucherID  uuid.UUID
	Buyer      string
	Section    string
	BaseRupees decimal.Decimal
	RatePct    decimal.Decimal
	TCSRupees  decimal.Decimal
	// Declaration is NONE / EXEMPT / INVALID_NO_PAN. Carried through to the
	// register because a zero in the tax column has three different meanings and
	// this is the only column that distinguishes them.
	Declaration  string
	PANAvailable bool
}

// TDSRegister is the whole report for a period.
type TDSRegister struct {
	FromDate string
	ToDate   string
	// TaxYearStart is the year the period falls in, or 0 when the range spans
	// two tax years — in which case the caller has asked for something the
	// return cannot be filed from, and Disclosures says so.
	TaxYearStart int
	Deductions   []TDSRegisterRow
	Collections  []TCSRegisterRow
	// Totals, summed from the rows above rather than by a second query. A
	// separate SUM() could disagree with the list it heads — the classic way a
	// register reconciles to nothing.
	TotalBaseDeducted  decimal.Decimal
	TotalTDS           decimal.Decimal
	TotalBaseCollected decimal.Decimal
	TotalTCS           decimal.Decimal
	// Counts that a bare amount cannot convey.
	SubThresholdDeductions int
	ExemptCollections      int
	// Disclosures are the things this register CANNOT tell you, in the words a
	// user needs. Carried in the payload rather than left to the screen, so the
	// print path and the JSON say exactly the same thing.
	Disclosures []string
}

// TDSRegisterFor builds the register for a date range, inclusive of both ends.
func (s *Service) TDSRegisterFor(ctx context.Context, companyID uuid.UUID, from, to string) (TDSRegister, error) {
	fromT, err := time.Parse("2006-01-02", from)
	if err != nil {
		return TDSRegister{}, fmt.Errorf("reporting: from date %q is not yyyy-mm-dd", from)
	}
	toT, err := time.Parse("2006-01-02", to)
	if err != nil {
		return TDSRegister{}, fmt.Errorf("reporting: to date %q is not yyyy-mm-dd", to)
	}
	if toT.Before(fromT) {
		return TDSRegister{}, fmt.Errorf("reporting: the period ends (%s) before it begins (%s)", to, from)
	}

	out := TDSRegister{
		FromDate:           from,
		ToDate:             to,
		TotalBaseDeducted:  decimal.Zero,
		TotalTDS:           decimal.Zero,
		TotalBaseCollected: decimal.Zero,
		TotalTCS:           decimal.Zero,
	}
	fy := taxYearOf(fromT)
	if taxYearOf(toT) == fy {
		out.TaxYearStart = fy
	}

	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// DEDUCTIONS. The join to voucher_head is what ties the row to the
		// voucher an operator can open; it is an inner join, so a deduction whose
		// voucher vanished could not appear — and cannot, because the FK forbids
		// it. voucher_no comes off the CURRENT version (see I-9 in the header).
		rows, err := tx.Query(ctx,
			`SELECT to_char(d.deduction_date, 'YYYY-MM-DD'),
			        COALESCE(vv.voucher_no, ''), d.voucher_id, l.name, d.section,
			        d.deductee_type, d.return_code, d.taxable_base_rupees,
			        d.rate_pct, d.tds_amount_rupees, d.pan_available
			   FROM tds_deduction d
			   JOIN ledger l        ON l.company_id  = d.company_id AND l.id = d.deductee_ledger_id
			   JOIN voucher_head vh ON vh.company_id = d.company_id AND vh.voucher_id = d.voucher_id
			   JOIN voucher_version vv ON vv.id = vh.current_version_id
			  WHERE d.company_id = $1
			    AND d.deduction_date BETWEEN $2 AND $3
			  ORDER BY d.deduction_date, vv.voucher_no, l.name`,
			companyID, from, to)
		if err != nil {
			return fmt.Errorf("reporting: tds register deductions: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var r TDSRegisterRow
			if err := rows.Scan(&r.Date, &r.VoucherNo, &r.VoucherID, &r.Deductee,
				&r.Section, &r.DeducteeType, &r.ReturnCode, &r.BaseRupees,
				&r.RatePct, &r.TDSRupees, &r.PANAvailable); err != nil {
				return fmt.Errorf("reporting: scan deduction: %w", err)
			}
			out.Deductions = append(out.Deductions, r)
			out.TotalBaseDeducted = out.TotalBaseDeducted.Add(r.BaseRupees)
			out.TotalTDS = out.TotalTDS.Add(r.TDSRupees)
			// A zero-tax row is a PAYMENT BELOW THE THRESHOLD, not an absence —
			// it is what carries the s.393(1) aggregate, and Annexure 6 reports
			// it with flag `Y`. Counted so the register can say how many, rather
			// than leaving a reader to wonder why rows show no tax.
			if r.TDSRupees.IsZero() {
				out.SubThresholdDeductions++
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}

		// COLLECTIONS.
		crows, err := tx.Query(ctx,
			`SELECT to_char(c.collection_date, 'YYYY-MM-DD'),
			        COALESCE(vv.voucher_no, ''), c.voucher_id, l.name, c.section,
			        c.taxable_base_rupees, c.rate_pct, c.tcs_amount_rupees,
			        c.declaration_outcome, c.pan_available
			   FROM tcs_collection c
			   JOIN ledger l        ON l.company_id  = c.company_id AND l.id = c.buyer_ledger_id
			   JOIN voucher_head vh ON vh.company_id = c.company_id AND vh.voucher_id = c.voucher_id
			   JOIN voucher_version vv ON vv.id = vh.current_version_id
			  WHERE c.company_id = $1
			    AND c.collection_date BETWEEN $2 AND $3
			  ORDER BY c.collection_date, vv.voucher_no, l.name`,
			companyID, from, to)
		if err != nil {
			return fmt.Errorf("reporting: tds register collections: %w", err)
		}
		defer crows.Close()
		for crows.Next() {
			var r TCSRegisterRow
			if err := crows.Scan(&r.Date, &r.VoucherNo, &r.VoucherID, &r.Buyer,
				&r.Section, &r.BaseRupees, &r.RatePct, &r.TCSRupees,
				&r.Declaration, &r.PANAvailable); err != nil {
				return fmt.Errorf("reporting: scan collection: %w", err)
			}
			out.Collections = append(out.Collections, r)
			out.TotalBaseCollected = out.TotalBaseCollected.Add(r.BaseRupees)
			out.TotalTCS = out.TotalTCS.Add(r.TCSRupees)
			if r.Declaration == "EXEMPT" {
				out.ExemptCollections++
			}
		}
		return crows.Err()
	})
	if err != nil {
		return TDSRegister{}, err
	}

	out.Disclosures = registerDisclosures(out)
	return out, nil
}

// taxYearOf returns the calendar year the Indian tax year begins in.
func taxYearOf(t time.Time) int {
	if t.Month() >= time.April {
		return t.Year()
	}
	return t.Year() - 1
}

// registerDisclosures states, in the payload, what this register cannot tell
// you. They travel WITH the data for the same reason the e-invoice QR refusal is
// printed on the document rather than kept in a design note: the reader who
// needs the caveat is the one holding the artefact.
func registerDisclosures(r TDSRegister) []string {
	d := []string{
		// The big one. Everything here is what was DEDUCTED and COLLECTED; none
		// of it is what was PAID to the government, and it is the deposit that
		// stops interest running under s.396.
		"DEPOSIT IS NOT TRACKED. Every figure here is tax deducted or collected, " +
			"not tax PAID to the government. There is no challan register yet, so " +
			"this report cannot tell you what has been deposited, when, or what is " +
			"still outstanding — and it is the deposit, not the deduction, that " +
			"stops interest running.",
		// I-7. The register exists; the return does not — but NOT for the reason
		// this line used to give. The Form 140 file format HAS been read and is
		// implemented (internal/compliance/form140); what is missing now is a
		// challan register and this company's TAN. Saying "the layout has not
		// been read" after it was read is how a blocker outlives its own truth.
		"NO RETURN CAN BE FILED FROM THIS YET. The Form 140 layout is implemented, " +
			"but nothing here records the challans you have DEPOSITED and this " +
			"company has no TAN on file, so there is nothing to write a return " +
			"from — see the Returns section of F11 Company Features.",
	}
	if r.TaxYearStart == 0 {
		d = append(d, "THIS PERIOD SPANS TWO TAX YEARS. A return is filed per "+
			"quarter within one tax year, so these totals do not correspond to any "+
			"single filing.")
	}
	if r.SubThresholdDeductions > 0 {
		d = append(d, fmt.Sprintf("%d payment(s) carry NO tax because they were "+
			"below the threshold. They are listed on purpose: the annual aggregate "+
			"is of amounts PAID, so these are what can push a later payment over "+
			"the limit.", r.SubThresholdDeductions))
	}
	if r.ExemptCollections > 0 {
		d = append(d, fmt.Sprintf("%d sale(s) collected nothing under a s.394(2) "+
			"declaration. Each rests on a document you must be able to produce, and "+
			"a copy was due to the Commissioner by the 7th of the month after you "+
			"received it.", r.ExemptCollections))
	}
	return d
}
