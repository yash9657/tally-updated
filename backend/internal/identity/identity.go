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
			   (id, company_id, name, fy_start_date, base_currency, state_code, gstin)
			 VALUES ($1, $1, $2, $3, $4, $5, $6)`,
			c.ID, c.Name, c.FYStartDate, c.BaseCurrency, c.StateCode, c.GSTIN,
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
