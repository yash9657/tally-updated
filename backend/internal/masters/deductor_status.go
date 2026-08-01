package masters

// THE DEDUCTOR STATUS MASTER — recording whether this company must deduct.
//
// Migration 0049 created company_deductor_status and nothing wrote to it. The
// TDS post path (internal/ledger/tds.go) then began REFUSING every voucher for a
// company with no row, which is correct and which made this the blocking gap:
// liability could only be recorded by raw SQL, so on a real deployment TDS did
// not work at all.
//
// Tally puts exactly this behind F11 (Company Features) > Enable TDS >
// "TDS Deductor Details", which asks for TAN, DEDUCTOR TYPE and the responsible
// person — RESEARCH.md:692. `entity_category` here is that deductor type.
//
// ---------------------------------------------------------------------------
// ⚠️ THE TAX YEAR STORED IS NOT THE YEAR THE PAYMENTS FALL IN
// ---------------------------------------------------------------------------
// s.402(11)(l)(ii) and s.402(37)(b) test turnover "during the tax year
// IMMEDIATELY PRECEDING the tax year in which such income or sum is credited or
// paid". So the row for 2025-26 governs payments made in 2026-27, and the post
// path subtracts one when it looks the row up.
//
// This is the single most confusable thing about the feature, so this layer does
// NOT hide it: GovernsPaymentsIn is returned alongside every row so a screen can
// say "2025-26 — governs payments made in 2026-27" rather than leaving an
// operator to guess which year to key. A silent convention here puts a company's
// whole year of deductions under the wrong test.
//
// ---------------------------------------------------------------------------
// WHY THIS VALIDATES IN GO WHEN THE TABLE ALREADY HAS CHECKS
// ---------------------------------------------------------------------------
// Migration 0049's CHECK constraints are the authority and they stay. But a
// constraint violation reaches an operator as "new row for relation
// company_deductor_status violates check constraint
// company_deductor_status_turnover_required_for_natural_persons", which tells a
// shopkeeper nothing. These checks exist to produce a sentence a person can act
// on; the constraints exist to be right even if this code is wrong.

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/compliance"
	"github.com/khata/cored/internal/platform/db"
)

// ErrInvalidDeductorStatus is returned for a status this layer refuses to store.
// It is a sentinel so the HTTP layer can map it to a 400 rather than a 500.
var ErrInvalidDeductorStatus = errors.New("masters: invalid deductor status")

// DeductorStatusRecord is one company_deductor_status row, plus the derived year
// it governs.
type DeductorStatusRecord struct {
	// TaxYearStart is the calendar year the tax year BEGINS in: 2025 = 2025-26.
	TaxYearStart int
	// GovernsPaymentsIn is TaxYearStart + 1 — the tax year whose payments this
	// row decides. Derived, never stored: two columns that must differ by one
	// are a column and a bug waiting to disagree.
	GovernsPaymentsIn int
	// EntityCategory is one of compliance's Entity* constants — Tally's
	// "deductor type".
	EntityCategory string
	// PriorYearTurnoverRupees is meaningful only when TurnoverKnown.
	PriorYearTurnoverRupees decimal.Decimal
	// PriorYearActivity is BUSINESS or PROFESSION; empty when not applicable.
	PriorYearActivity string
	// TurnoverKnown distinguishes "the test does not apply to this category"
	// from a turnover of zero.
	TurnoverKnown bool
	// RecordedBy and RecordedAt are the audit trail.
	RecordedBy string
	RecordedAt string
}

// naturalPersonCategories are the four the turnover test can reach. A COMPANY is
// a specified and designated person whatever it turned over, so it must record
// no turnover at all — and a zero would be a different and false claim.
//
// Kept in step with migration 0049's turnover_required_for_natural_persons CHECK
// by TestNaturalPersonCategoriesMatchTheMigration.
func isNaturalPerson(category string) bool {
	switch category {
	case compliance.EntityIndividual, compliance.EntityHUF,
		compliance.EntityAOP, compliance.EntityBOI:
		return true
	default:
		return false
	}
}

// validateDeductorStatus produces the sentence an operator can act on.
func validateDeductorStatus(in DeductorStatusRecord) error {
	if in.TaxYearStart < 2025 || in.TaxYearStart > 2200 {
		return fmt.Errorf("%w: tax year %d is outside 2025–2200; the 2025 Act's "+
			"entity categories cannot describe a year before it commenced",
			ErrInvalidDeductorStatus, in.TaxYearStart)
	}
	if !compliance.IsKnownEntityCategory(in.EntityCategory) {
		return fmt.Errorf("%w: %q is not one of the s.402(11)(a)–(l) deductor "+
			"categories", ErrInvalidDeductorStatus, in.EntityCategory)
	}
	natural := isNaturalPerson(in.EntityCategory)
	if natural && !in.TurnoverKnown {
		return fmt.Errorf("%w: %s is subject to the turnover test, so the prior "+
			"year's turnover and whether it was business or profession are both "+
			"required — without them it cannot be decided whether this company "+
			"must deduct at all", ErrInvalidDeductorStatus, in.EntityCategory)
	}
	if !natural && in.TurnoverKnown {
		return fmt.Errorf("%w: %s is a specified and designated person WHATEVER "+
			"its turnover, so no turnover may be recorded against it — a figure "+
			"here would imply a test that is never applied",
			ErrInvalidDeductorStatus, in.EntityCategory)
	}
	if in.TurnoverKnown {
		if in.PriorYearTurnoverRupees.IsNegative() {
			return fmt.Errorf("%w: turnover cannot be negative", ErrInvalidDeductorStatus)
		}
		switch in.PriorYearActivity {
		case compliance.ActivityBusiness, compliance.ActivityProfession:
		default:
			return fmt.Errorf("%w: prior-year activity must be %s or %s — they "+
				"carry DIFFERENT limits (one crore against fifty lakh), so it "+
				"cannot be inferred from the turnover", ErrInvalidDeductorStatus,
				compliance.ActivityBusiness, compliance.ActivityProfession)
		}
	}
	return nil
}

// SetDeductorStatus records (or corrects) this company's status for one tax
// year. It is an UPSERT on (company_id, tax_year_start): there is one answer per
// year, and a second row would make "was this company required to deduct in
// 2026-27?" unanswerable.
//
// CORRECTING A YEAR DOES NOT RESTATE DEDUCTIONS ALREADY POSTED. Each
// tds_deduction row records what was derived under the status as it then stood,
// and this call does not walk back over them. That is a DISCLOSED limitation
// rather than an oversight: re-deriving a year of deductions would move money on
// vouchers an operator has not looked at, and the right shape for it is a
// reviewable restatement report in T6, not a silent side effect of saving a
// form.
func (s *Service) SetDeductorStatus(ctx context.Context, in DeductorStatusRecord, recordedBy string) error {
	if err := validateDeductorStatus(in); err != nil {
		return err
	}
	if recordedBy == "" {
		return fmt.Errorf("%w: recorded_by is required", ErrInvalidDeductorStatus)
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return db.ErrNoCompany
	}
	// NULL, not zero, when the test does not apply — the column pair is what
	// migration 0049's turnover_pair CHECK keeps in step, and what
	// compliance.DeductorStatus.TurnoverKnown reads back.
	var turnover *decimal.Decimal
	var activity *string
	if in.TurnoverKnown {
		t := in.PriorYearTurnoverRupees
		a := in.PriorYearActivity
		turnover, activity = &t, &a
	}
	return s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO company_deductor_status
			   (id, company_id, tax_year_start, entity_category,
			    prior_year_turnover_rupees, prior_year_activity, recorded_by)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)
			 ON CONFLICT (company_id, tax_year_start) DO UPDATE
			   SET entity_category            = EXCLUDED.entity_category,
			       prior_year_turnover_rupees = EXCLUDED.prior_year_turnover_rupees,
			       prior_year_activity        = EXCLUDED.prior_year_activity,
			       recorded_by                = EXCLUDED.recorded_by,
			       recorded_at                = now()`,
			uuid.New(), companyID, in.TaxYearStart, in.EntityCategory,
			turnover, activity, recordedBy)
		if err != nil {
			return fmt.Errorf("masters: upsert deductor status: %w", err)
		}
		return nil
	})
}

// ListDeductorStatuses returns every recorded year for this company, most recent
// first. The list is short by construction — one row per tax year.
func (s *Service) ListDeductorStatuses(ctx context.Context) ([]DeductorStatusRecord, error) {
	if _, ok := db.CompanyFromContext(ctx); !ok {
		return nil, db.ErrNoCompany
	}
	var out []DeductorStatusRecord
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT tax_year_start, entity_category, prior_year_turnover_rupees,
			        prior_year_activity, recorded_by,
			        to_char(recorded_at, 'YYYY-MM-DD"T"HH24:MI:SSOF:00')
			   FROM company_deductor_status
			  ORDER BY tax_year_start DESC`)
		if err != nil {
			return fmt.Errorf("masters: list deductor statuses: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var (
				r        DeductorStatusRecord
				turnover *decimal.Decimal
				activity *string
			)
			if err := rows.Scan(&r.TaxYearStart, &r.EntityCategory, &turnover,
				&activity, &r.RecordedBy, &r.RecordedAt); err != nil {
				return fmt.Errorf("masters: scan deductor status: %w", err)
			}
			if turnover != nil {
				r.TurnoverKnown = true
				r.PriorYearTurnoverRupees = *turnover
			}
			if activity != nil {
				r.PriorYearActivity = *activity
			}
			r.GovernsPaymentsIn = r.TaxYearStart + 1
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
