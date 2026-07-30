// Package banking is the WRITE side of bank reconciliation: recording that the
// bank has cleared a voucher's bank leg, and un-recording it.
//
// Design memo: docs/design/t4-model.md.
//
// WHY THIS IS ITS OWN PACKAGE AND NOT PART OF reporting
// ----------------------------------------------------
// internal/reporting's package doc commits it to being read-only ("derives
// read-only financial statements ... depends only on the db query interface,
// never on sibling write modules"). Reconciling is a WRITE. Putting it there
// would make that sentence false, and the sentence is load-bearing: it is why a
// report can never have a side effect on the books.
//
// It is not internal/masters either — a bank date is not a master. It is an
// assertion about an EVENT, made by a human reading a bank statement.
//
// WHAT THIS PACKAGE DELIBERATELY CANNOT DO
// ----------------------------------------
// It never touches voucher_version, posting, or ledger_balance. Reconciling
// changes NO balance and NO voucher: it records a fact about the bank alongside
// the books. `posting` is hard-immutable anyway (posting_no_mutate), so an
// attempt would raise — but the point is that nothing here tries.
package banking

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/platform/db"
)

// ErrLegNotFound is returned when the (voucher, ledger, leg) triple does not
// name a bank leg of the voucher's CURRENT version. It is a sentinel so the
// HTTP layer can answer 404 rather than 500, and so a test can assert the
// specific failure rather than "some error happened".
var ErrLegNotFound = errors.New("banking: no such bank leg on the current version of this voucher")

// TxRunner is the minimal database capability this package needs.
type TxRunner interface {
	RunInTx(ctx context.Context, fn db.TxFunc) error
}

// Service is the bank-reconciliation write service.
type Service struct{ runner TxRunner }

// NewService constructs a banking Service over the given transaction runner.
func NewService(runner TxRunner) *Service { return &Service{runner: runner} }

// legSQL resolves one bank leg of a voucher's CURRENT version and returns its
// amount and side. The leg ordinal is computed exactly as bankReconSQL computes
// it — row_number() over the same partition, same ORDER BY, 0-based — because
// the two MUST agree: if the reader and the writer disagreed about which leg is
// leg 1, an operator would clear one item and the statement would show another.
const legSQL = `
	WITH legs AS (
	    SELECT p.amount, p.dr_cr::text AS dr_cr,
	           row_number() OVER (
	               PARTITION BY vv.voucher_id, p.ledger_id
	               ORDER BY p.id
	           ) - 1 AS leg_no
	      FROM posting p
	      JOIN voucher_version vv ON vv.id = p.voucher_version_id
	      JOIN voucher_head vh
	        ON vh.company_id = vv.company_id
	       AND vh.voucher_id = vv.voucher_id
	       AND vh.current_version_id = vv.id
	     WHERE vh.is_deleted = false
	       AND vv.voucher_id = $1
	       AND p.ledger_id = $2
	)
	SELECT amount, dr_cr FROM legs WHERE leg_no = $3`

// SetBankDate records that the bank cleared one bank leg of a voucher on
// bankDate. It is idempotent on (voucher, ledger, leg): re-stating a bank date
// updates it rather than failing, because correcting a mistyped date is the
// normal case and forcing an un-reconcile first would be friction with no
// safety benefit.
//
// THE AMOUNT AND SIDE ARE READ FROM THE CURRENT VERSION AND STORED. That is not
// a cache — it is the mechanism by which a later alter becomes DETECTABLE. See
// the memo §0.1: the reconciliation is keyed on the stable voucher_id, so
// without a record of what was cleared an alter would silently re-point it.
func (s *Service) SetBankDate(
	ctx context.Context,
	companyID, voucherID, ledgerID uuid.UUID,
	legNo int,
	bankDate string,
	by string,
) error {
	if err := scope(ctx, companyID); err != nil {
		return err
	}
	if legNo < 0 {
		return fmt.Errorf("banking: leg_no must be non-negative, got %d", legNo)
	}
	return s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var (
			amount decimal.Decimal
			drCr   string
		)
		err := tx.QueryRow(ctx, legSQL, voucherID, ledgerID, legNo).Scan(&amount, &drCr)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: voucher %s ledger %s leg %d",
				ErrLegNotFound, voucherID, ledgerID, legNo)
		}
		if err != nil {
			return fmt.Errorf("banking: resolve bank leg: %w", err)
		}

		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("banking: new id: %w", err)
		}
		// The bank-group check and the bank_date >= voucher_date check both live
		// in the 0045 trigger, so they hold for ANY writer — including a future
		// import path and a by-hand psql fix — not only for this function.
		_, err = tx.Exec(ctx, `
			INSERT INTO bank_reconciliation
			    (id, company_id, voucher_id, ledger_id, leg_no,
			     bank_date, cleared_amount, cleared_dr_cr, reconciled_by)
			VALUES ($1, $2, $3, $4, $5, $6::date, $7, $8::dr_cr, $9)
			ON CONFLICT (company_id, voucher_id, ledger_id, leg_no) DO UPDATE
			   SET bank_date      = EXCLUDED.bank_date,
			       cleared_amount = EXCLUDED.cleared_amount,
			       cleared_dr_cr  = EXCLUDED.cleared_dr_cr,
			       reconciled_by  = EXCLUDED.reconciled_by,
			       reconciled_at  = now()`,
			id, companyID, voucherID, ledgerID, legNo,
			bankDate, amount, drCr, by)
		if err != nil {
			return fmt.Errorf("banking: set bank date: %w", err)
		}
		return nil
	})
}

// ClearBankDate un-reconciles one bank leg. Deleting the row (rather than
// nulling a column) is deliberate: an absent row means exactly one thing — "the
// bank has not cleared this" — and there is then no second representation of
// un-cleared for a reader to get wrong.
//
// Removing a reconciliation that does not exist is NOT an error. The caller's
// intent ("this should not be reconciled") is satisfied either way, and making
// it fail would turn a double-click into a support call.
func (s *Service) ClearBankDate(
	ctx context.Context, companyID, voucherID, ledgerID uuid.UUID, legNo int,
) error {
	if err := scope(ctx, companyID); err != nil {
		return err
	}
	return s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			DELETE FROM bank_reconciliation
			 WHERE company_id = $1 AND voucher_id = $2
			   AND ledger_id = $3 AND leg_no = $4`,
			companyID, voucherID, ledgerID, legNo)
		if err != nil {
			return fmt.Errorf("banking: clear bank date: %w", err)
		}
		return nil
	})
}

// scope mirrors reporting.scope: a fast, friendly check that the caller is
// operating on the company its context is bound to. db.RunInTx's GUC guard is
// the authoritative backstop.
func scope(ctx context.Context, companyID uuid.UUID) error {
	scoped, ok := db.CompanyFromContext(ctx)
	if !ok {
		return db.ErrNoCompany
	}
	if scoped != companyID {
		return fmt.Errorf("banking: company %s not in scope %s", companyID, scoped)
	}
	return nil
}
