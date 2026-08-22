// Package identity owns company (tenant) lifecycle. Phase 0 implements company
// creation, which inserts the company row and seeds the 28 predefined account
// groups, the two system ledgers, and the F8 Sales voucher type via the
// seed_company_defaults SQL function — all inside one company-scoped tx.
package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/platform/db"
)

// TxRunner is the minimal database capability identity needs.
type TxRunner interface {
	RunInTx(ctx context.Context, fn db.TxFunc) error
}

// Company is a tenant.
type Company struct {
	ID           uuid.UUID
	Name         string
	FYStartDate  string // ISO date
	BaseCurrency string
	// StateCode is the two-digit GST state code of the company's home/registered
	// place of business. It is the default SupplierStateCode for Sales/Purchase
	// posts that leave it empty (e.g. imported vouchers). Empty is the safe
	// default, matching the column default.
	StateCode string
	// GSTIN is the company's own (filer's) GSTIN, surfaced as the GSTR-1
	// top-level gstin. Empty is the safe default.
	GSTIN string

	// --- Mailing block (migration 0015) ------------------------------------
	// MailingName is the name printed on invoices; empty means "use Name".
	MailingName string
	// Address is the principal place of business as a newline-separated block.
	Address string
	City    string
	Pincode string
	// Country defaults to India when empty (matching the column default).
	Country string

	// BooksBeginningFrom is the date this company's books begin here, and
	// therefore the date every opening balance is stated as at. It equals
	// FYStartDate for a company starting at the top of its financial year and is
	// LATER for a mid-year migration; it may never be earlier (enforced by the
	// company_books_begin_within_fy CHECK). Empty defaults to FYStartDate.
	BooksBeginningFrom string // ISO date

	// --- Base-currency presentation (migration 0015) -----------------------
	// CurrencySymbol/CurrencyFormalName/DecimalPlaces describe how money is
	// RENDERED. Storage and arithmetic are NUMERIC(19,4) regardless.
	CurrencySymbol     string
	CurrencyFormalName string
	// DecimalPlaces is a pointer so "unset" (nil) is distinguishable from an
	// explicit 0, which is a legitimate choice for whole-rupee books.
	DecimalPlaces *int

	// --- Inventory policy (migration 0025) ---------------------------------
	// AllowNegativeStock is the per-company opt-in escape hatch read by BOTH
	// stock enforcers -- ledger.checkNegativeStock (the in-transaction
	// pre-check) and the deferred check_stock_not_negative() constraint
	// trigger. It had been readable by both and writable by NOTHING since 0025
	// shipped: no field on this struct, no column in the INSERT below, no
	// route, no setter, no control. So it was permanently false for every
	// tenant and the refusal was absolute, while the schema and both enforcers
	// implied a choice the operator did not have (register row D5).
	//
	// A POINTER, not a bool, for the same reason DecimalPlaces is one: the
	// zero value here is a MEANINGFUL setting (refuse), so "the caller omitted
	// the key" and "the caller asked for false" must stay distinguishable. nil
	// takes the column default, which is false -- refuse. That default is
	// deliberate and stays: an escape hatch nobody asked for must be shut.
	AllowNegativeStock *bool
}

// Service is the identity application service.
type Service struct {
	runner TxRunner
}

// NewService constructs an identity Service.
func NewService(runner TxRunner) *Service {
	return &Service{runner: runner}
}

// CreateCompany inserts a new company and seeds its defaults. The returned
// company id must be placed into context (via db.WithCompany) for any further
// tenant-scoped work. The caller's context MUST already be scoped to this same
// company id, since the insert is subject to RLS — CreateCompany validates that
// invariant and returns an error otherwise.
func (s *Service) CreateCompany(ctx context.Context, c Company) (uuid.UUID, error) {
	if c.Name == "" {
		return uuid.Nil, errors.New("identity: company name is required")
	}
	if c.ID == uuid.Nil {
		id, err := uuid.NewV7()
		if err != nil {
			return uuid.Nil, fmt.Errorf("identity: new company id: %w", err)
		}
		c.ID = id
	}
	if c.BaseCurrency == "" {
		c.BaseCurrency = "INR"
	}
	// Books begin when the year does unless the caller says otherwise (the
	// mid-year-migration case). Defaulting here rather than in SQL keeps the
	// column NOT NULL without a magic date.
	if c.BooksBeginningFrom == "" {
		c.BooksBeginningFrom = c.FYStartDate
	}
	if c.Country == "" {
		c.Country = "India"
	}
	if c.CurrencySymbol == "" {
		c.CurrencySymbol = "₹"
	}
	if c.CurrencyFormalName == "" {
		c.CurrencyFormalName = "Indian Rupees"
	}
	decimals := 2
	if c.DecimalPlaces != nil {
		decimals = *c.DecimalPlaces
	}
	// Same treatment as decimals, and the default is the RESTRICTIVE one. Until
	// this line existed the key was accepted on the wire and dropped on the
	// floor at 201 (register row D43): the caller was told a setting had been
	// applied that never was.
	allowNegativeStock := false
	if c.AllowNegativeStock != nil {
		allowNegativeStock = *c.AllowNegativeStock
	}

	scoped, ok := db.CompanyFromContext(ctx)
	if !ok {
		return uuid.Nil, db.ErrNoCompany
	}
	if scoped != c.ID {
		return uuid.Nil, fmt.Errorf("identity: context company %s != new company %s", scoped, c.ID)
	}

	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO company
			   (id, company_id, name, fy_start_date, base_currency, state_code, gstin,
			    mailing_name, address, city, pincode, country,
			    books_beginning_from,
			    currency_symbol, currency_formal_name, decimal_places,
			    allow_negative_stock)
			 VALUES ($1, $1, $2, $3, $4, $5, $6,
			         $7, $8, $9, $10, $11,
			         $12,
			         $13, $14, $15,
			         $16)`,
			c.ID, c.Name, c.FYStartDate, c.BaseCurrency, c.StateCode, c.GSTIN,
			c.MailingName, c.Address, c.City, c.Pincode, c.Country,
			c.BooksBeginningFrom,
			c.CurrencySymbol, c.CurrencyFormalName, decimals,
			allowNegativeStock,
		); err != nil {
			return fmt.Errorf("identity: insert company: %w", err)
		}
		if _, err := tx.Exec(ctx, `SELECT seed_company_defaults($1)`, c.ID); err != nil {
			return fmt.Errorf("identity: seed defaults: %w", err)
		}
		return nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	return c.ID, nil
}

// ErrCompanyNotFound is returned by SetCompanyFeatures when the UPDATE matches
// no row. Under RLS that is indistinguishable from "the company exists and this
// session cannot see it", and both are the same answer to the caller: 404.
var ErrCompanyNotFound = errors.New("identity: company not found")

// SetCompanyFeatures writes the per-company inventory policy.
//
// It exists because company.allow_negative_stock was readable by both stock
// enforcers and writable by nothing (register row D5). This is the whole write
// side, deliberately narrow: it is NOT a general company editor. The name, the
// financial year, the state code and the currency block are all identity a
// posted book depends on -- moving fy_start_date under existing vouchers would
// re-file them into a different year -- so nothing in this function touches
// them, and a future setting belongs beside allow_negative_stock rather than in
// a widened UPDATE over the whole row.
//
// The context MUST already be scoped to the company (RLS binds
// app.current_company_id inside RunInTx), and companyID is cross-checked
// against that scope for the same reason CreateCompany cross-checks it: the
// UPDATE is subject to RLS, so a mis-scoped session would silently match zero
// rows and look exactly like a missing company.
func (s *Service) SetCompanyFeatures(ctx context.Context, companyID uuid.UUID, allowNegativeStock bool) error {
	scoped, ok := db.CompanyFromContext(ctx)
	if !ok {
		return db.ErrNoCompany
	}
	if scoped != companyID {
		return fmt.Errorf("identity: context company %s != target company %s", scoped, companyID)
	}
	return s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE company SET allow_negative_stock = $2 WHERE id = $1`,
			companyID, allowNegativeStock)
		if err != nil {
			return fmt.Errorf("identity: set company features: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrCompanyNotFound
		}
		return nil
	})
}

// CompanyFeatures reads the per-company inventory policy back out of the row.
//
// The write path calls this AFTER the UPDATE and answers with what it returns,
// never with an echo of the request body. That is not ceremony: echoing the
// request is precisely the defect D43 was -- a caller told a setting was applied
// when the column never moved -- and an echo cannot tell the two apart.
func (s *Service) CompanyFeatures(ctx context.Context, companyID uuid.UUID) (allowNegativeStock bool, err error) {
	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		switch err := tx.QueryRow(ctx,
			`SELECT allow_negative_stock FROM company WHERE id = $1`, companyID).
			Scan(&allowNegativeStock); {
		case errors.Is(err, pgx.ErrNoRows):
			return ErrCompanyNotFound
		case err != nil:
			return fmt.Errorf("identity: read company features: %w", err)
		}
		return nil
	})
	return allowNegativeStock, err
}

// LedgerID resolves a ledger's id by name within the company in context.
func (s *Service) LedgerID(ctx context.Context, name string) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id FROM ledger WHERE name = $1`, name).Scan(&id)
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("identity: resolve ledger %q: %w", name, err)
	}
	return id, nil
}

// CreateLedger creates a user ledger under the named group and returns its id.
func (s *Service) CreateLedger(ctx context.Context, name, groupName string) (uuid.UUID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("identity: new ledger id: %w", err)
	}
	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var groupID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM account_group WHERE name = $1`, groupName).Scan(&groupID); err != nil {
			return fmt.Errorf("identity: resolve group %q: %w", groupName, err)
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO ledger (id, company_id,
			   name, group_id)
			 VALUES ($1, current_setting('app.current_company_id', true)::uuid, $2, $3)`,
			id, name, groupID)
		return err
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("identity: create ledger %q: %w", name, err)
	}
	return id, nil
}

// VoucherTypeID resolves a voucher type's id by name within the company in context.
func (s *Service) VoucherTypeID(ctx context.Context, name string) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id FROM voucher_type WHERE name = $1`, name).Scan(&id)
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("identity: resolve voucher type %q: %w", name, err)
	}
	return id, nil
}
