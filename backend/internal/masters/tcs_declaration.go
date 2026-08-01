package masters

// THE s.394(2) MANUFACTURING DECLARATION — Item Zero step 3.
//
// Migration 0049 created tcs_collection_declaration and nothing wrote to it, so
// compliance's whole declaration path (the exemption AND its penal-rate
// coupling) was unreachable. This is the write side; ledger/tcs.go is the read
// side, which looks a declaration up by (buyer, section, tax year) inside the
// post transaction.
//
// s.394(2), verbatim: "Irrespective of anything contained in sub-section (1)
// (Table: Sl. Nos. 1 to 5), the collection of tax shall not to be made ... in
// respect of the buyer, who is a resident in India, if he furnishes a written
// declaration in duplicate ... mentioning that such goods are to be utilised--
// (a) for the purposes of manufacturing, processing or producing articles or
// things or for generating power; and (b) not for trading purposes."
//
// ---------------------------------------------------------------------------
// FOUR THINGS THIS LAYER REFUSES, AND WHY EACH IS A REFUSAL AND NOT A WARNING
// ---------------------------------------------------------------------------
//  1. A DECLARATION AGAINST AN INELIGIBLE SECTION. s.394(2) reaches Sl. Nos. 1
//     to 5. Scrap is Sl. No. 4 and IS reachable; motor vehicles are Sl. No. 6
//     and are NOT. An operator who files one against a car sale believes their
//     buyer is exempt and will not be — silently storing it would let them
//     discover that from a demand notice.
//  2. (a) AND (b) ARE CONJUNCTIVE. Goods for manufacturing that are ALSO for
//     trading do not qualify.
//  3. THE BUYER MUST BE RESIDENT.
//  4. RECEIVED BEFORE DECLARED is impossible, and s.394(3)'s due date runs off
//     the received date, so an inverted pair would produce a wrong deadline.
//
// ⚠️ WHAT IT DOES *NOT* REFUSE IS THE MISSING PAN, and that is the subtle one.
// s.397(2)(f)(i) makes a PAN-less declaration INVALID and (g) then requires the
// PENAL rate — so it is a real declaration in an invalid state, and the engine
// has to be able to SEE it to apply (g). Refusing it here would erase the very
// fact that makes the penal rate correct, and the buyer would be charged the
// base rate instead of 5%.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/platform/db"
)

// ErrInvalidTCSDeclaration is returned for a declaration this layer refuses to
// store. A sentinel so the HTTP layer answers 400 rather than 500.
var ErrInvalidTCSDeclaration = errors.New("masters: invalid s.394(2) declaration")

// declarationEligibleSections mirrors migration 0049's
// tcs_collection_declaration_section_eligible CHECK and compliance's own set.
// Pinned against the migration by TestDeclarationEligibleSectionsMatchTheMigration.
var declarationEligibleSections = map[string]bool{
	"206C(1)-SCRAP": true,
}

// TCSDeclarationRecord is one tcs_collection_declaration row plus the s.394(3)
// due date derived from it.
type TCSDeclarationRecord struct {
	BuyerLedgerID uuid.UUID
	BuyerName     string
	Section       string
	TaxYearStart  int
	// DeclarationDate is the date ON the document; ReceivedDate is when the
	// collector got it. Two different facts, and the deadline runs off the
	// second.
	DeclarationDate string
	ReceivedDate    string
	// ForwardDueDate is s.394(3): a copy must reach the Commissioner "on or
	// before the seventh day of the month following the month of receipt".
	// DERIVED, never stored — a stored deadline drifts from the date it is
	// computed from the first time somebody corrects the receipt date.
	ForwardDueDate string
	// BuyerPAN is empty when none was furnished, which under s.397(2)(f)(i)
	// makes the declaration INVALID and triggers the penal rate.
	BuyerPAN string
	// PANValid is the readable form of that. False does NOT mean "no
	// declaration" — it means a declaration that costs the buyer MORE than
	// furnishing none.
	PANValid   bool
	RecordedBy string
}

// forwardDueDate implements s.394(3): the 7th of the month FOLLOWING the month
// of receipt.
func forwardDueDate(received time.Time) string {
	y, m := received.Year(), received.Month()
	next := time.Date(y, m, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0)
	return time.Date(next.Year(), next.Month(), 7, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
}

func validateTCSDeclaration(in TCSDeclarationRecord) error {
	if !declarationEligibleSections[in.Section] {
		return fmt.Errorf("%w: s.394(2) reaches only Table Sl. Nos. 1 to 5, and %q is "+
			"not one of them — a declaration filed against it would not exempt the "+
			"buyer, so it is refused rather than stored",
			ErrInvalidTCSDeclaration, in.Section)
	}
	if in.TaxYearStart < 2025 || in.TaxYearStart > 2200 {
		return fmt.Errorf("%w: tax year %d is outside 2025–2200",
			ErrInvalidTCSDeclaration, in.TaxYearStart)
	}
	if in.BuyerLedgerID == uuid.Nil {
		return fmt.Errorf("%w: a declaration is furnished BY a buyer; without one it "+
			"cannot be matched to a sale", ErrInvalidTCSDeclaration)
	}
	declared, err := time.Parse("2006-01-02", in.DeclarationDate)
	if err != nil {
		return fmt.Errorf("%w: declaration date %q is not yyyy-mm-dd",
			ErrInvalidTCSDeclaration, in.DeclarationDate)
	}
	received, err := time.Parse("2006-01-02", in.ReceivedDate)
	if err != nil {
		return fmt.Errorf("%w: received date %q is not yyyy-mm-dd",
			ErrInvalidTCSDeclaration, in.ReceivedDate)
	}
	if received.Before(declared) {
		return fmt.Errorf("%w: received %s is before the declaration date %s, and "+
			"s.394(3)'s forwarding deadline runs off the receipt date",
			ErrInvalidTCSDeclaration, in.ReceivedDate, in.DeclarationDate)
	}
	// A PAN is OPTIONAL (see the header) but a malformed one is not a PAN. An
	// empty string must reach the column as NULL, never as a value that any
	// IS NOT NULL test would read as "a PAN was furnished".
	if pan := strings.TrimSpace(in.BuyerPAN); pan != "" && len(pan) != 10 {
		return fmt.Errorf("%w: a PAN is 10 characters; %q is %d. Leave it EMPTY if the "+
			"buyer furnished none — that is a valid state, and it makes the "+
			"declaration invalid under s.397(2)(f)(i) so the penal rate applies",
			ErrInvalidTCSDeclaration, pan, len(pan))
	}
	return nil
}

// SetTCSDeclaration records (or corrects) a buyer's declaration for one section
// and tax year. Idempotent on that triple: one declaration covers the buyer's
// purchases for the year, and a second row would make "is this buyer exempt?"
// unanswerable.
//
// The three conditions of s.394(2) are not parameters. They are stored as TRUE
// because migration 0049's purposes_conjunctive and buyer_resident CHECKs
// require it: a declaration that fails any of them is not a declaration this
// table can hold, and the caller's remedy is not to file one. Modelling them as
// togglable booleans would invite a row that exists and exempts nothing.
func (s *Service) SetTCSDeclaration(ctx context.Context, in TCSDeclarationRecord, recordedBy string) error {
	if err := validateTCSDeclaration(in); err != nil {
		return err
	}
	if recordedBy == "" {
		return fmt.Errorf("%w: recorded_by is required", ErrInvalidTCSDeclaration)
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return db.ErrNoCompany
	}
	var pan *string
	if p := strings.TrimSpace(in.BuyerPAN); p != "" {
		pan = &p
	}
	return s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO tcs_collection_declaration
			   (id, company_id, buyer_ledger_id, section, tax_year_start,
			    declaration_date, received_date, for_manufacturing,
			    not_for_trading, buyer_is_resident, buyer_pan, recorded_by)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE, TRUE, TRUE, $8, $9)
			 ON CONFLICT (company_id, buyer_ledger_id, section, tax_year_start)
			 DO UPDATE SET declaration_date = EXCLUDED.declaration_date,
			               received_date    = EXCLUDED.received_date,
			               buyer_pan        = EXCLUDED.buyer_pan,
			               recorded_by      = EXCLUDED.recorded_by,
			               recorded_at      = now()`,
			uuid.New(), companyID, in.BuyerLedgerID, in.Section, in.TaxYearStart,
			in.DeclarationDate, in.ReceivedDate, pan, recordedBy)
		if err != nil {
			return fmt.Errorf("masters: upsert tcs declaration: %w", err)
		}
		return nil
	})
}

// ListTCSDeclarations returns every declaration held by this company, newest
// receipt first, with the buyer's name resolved and the s.394(3) deadline
// derived.
func (s *Service) ListTCSDeclarations(ctx context.Context) ([]TCSDeclarationRecord, error) {
	if _, ok := db.CompanyFromContext(ctx); !ok {
		return nil, db.ErrNoCompany
	}
	var out []TCSDeclarationRecord
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT d.buyer_ledger_id, l.name, d.section, d.tax_year_start,
			        to_char(d.declaration_date, 'YYYY-MM-DD'),
			        to_char(d.received_date, 'YYYY-MM-DD'),
			        d.buyer_pan, d.recorded_by
			   FROM tcs_collection_declaration d
			   JOIN ledger l ON l.company_id = d.company_id AND l.id = d.buyer_ledger_id
			  ORDER BY d.received_date DESC, l.name`)
		if err != nil {
			return fmt.Errorf("masters: list tcs declarations: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var (
				r   TCSDeclarationRecord
				pan *string
			)
			if err := rows.Scan(&r.BuyerLedgerID, &r.BuyerName, &r.Section,
				&r.TaxYearStart, &r.DeclarationDate, &r.ReceivedDate,
				&pan, &r.RecordedBy); err != nil {
				return fmt.Errorf("masters: scan tcs declaration: %w", err)
			}
			if pan != nil {
				r.BuyerPAN = *pan
				r.PANValid = true
			}
			if received, err := time.Parse("2006-01-02", r.ReceivedDate); err == nil {
				r.ForwardDueDate = forwardDueDate(received)
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// TCSDeclarationSections returns the sections a declaration may be filed
// against, for a picker. Short by construction and served rather than
// hard-coded in the client, for the reason the deductor-status categories are.
func TCSDeclarationSections() []string {
	out := make([]string, 0, len(declarationEligibleSections))
	for s := range declarationEligibleSections {
		out = append(out, s)
	}
	return out
}
