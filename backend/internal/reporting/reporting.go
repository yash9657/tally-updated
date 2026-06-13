// Package reporting derives read-only financial statements by rolling up the
// incrementally-maintained ledger_balance table. It depends only on the db
// query interface, never on sibling write modules.
package reporting

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/platform/db"
)

// TxRunner is the minimal database capability reporting needs.
type TxRunner interface {
	RunInTx(ctx context.Context, fn db.TxFunc) error
}

// TrialBalanceRow is a single ledger's line on the Trial Balance.
type TrialBalanceRow struct {
	LedgerID   uuid.UUID
	LedgerName string
	GroupName  string
	Debit      decimal.Decimal
	Credit     decimal.Decimal
}

// TrialBalance is the full Trial Balance: per-ledger rows plus the column
// totals. IsBalanced reports whether total debits equal total credits.
type TrialBalance struct {
	Rows        []TrialBalanceRow
	TotalDebit  decimal.Decimal
	TotalCredit decimal.Decimal
}

// IsBalanced reports whether total debits equal total credits.
func (tb TrialBalance) IsBalanced() bool {
	return tb.TotalDebit.Equal(tb.TotalCredit)
}

// Service is the reporting application service.
type Service struct {
	runner TxRunner
}

// NewService constructs a reporting Service over the given transaction runner.
func NewService(runner TxRunner) *Service {
	return &Service{runner: runner}
}

// TrialBalance rolls up ledger_balance for the company in context into a Trial
// Balance. The companyID argument is validated against the context scope so a
// caller cannot request a tenant they are not bound to.
func (s *Service) TrialBalance(ctx context.Context, companyID uuid.UUID) (TrialBalance, error) {
	scoped, ok := db.CompanyFromContext(ctx)
	if !ok {
		return TrialBalance{}, db.ErrNoCompany
	}
	if scoped != companyID {
		return TrialBalance{}, fmt.Errorf("reporting: company %s not in scope %s", companyID, scoped)
	}

	var tb TrialBalance
	tb.TotalDebit = decimal.Zero
	tb.TotalCredit = decimal.Zero

	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT lb.ledger_id, l.name, g.name, lb.debit, lb.credit
			   FROM ledger_balance lb
			   JOIN ledger        l ON l.id = lb.ledger_id
			   JOIN account_group g ON g.id = l.group_id
			  ORDER BY g.name, l.name`)
		if err != nil {
			return fmt.Errorf("reporting: query ledger_balance: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var r TrialBalanceRow
			if err := rows.Scan(&r.LedgerID, &r.LedgerName, &r.GroupName, &r.Debit, &r.Credit); err != nil {
				return fmt.Errorf("reporting: scan row: %w", err)
			}
			tb.Rows = append(tb.Rows, r)
			tb.TotalDebit = tb.TotalDebit.Add(r.Debit)
			tb.TotalCredit = tb.TotalCredit.Add(r.Credit)
		}
		return rows.Err()
	})
	if err != nil {
		return TrialBalance{}, err
	}
	return tb, nil
}
