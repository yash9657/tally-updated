package reporting

// BANK RECONCILIATION — the Bank Reconciliation Statement, and the read model
// behind the bank-date entry screen.
//
// Design memo: docs/design/t4-model.md.
//
// THE ONE ARITHMETIC IDENTITY THIS FILE EXISTS TO PRESERVE
// -------------------------------------------------------
//
//	balance as per books  -  net of un-cleared items  =  balance as per bank
//
// and the difference must be EXACTLY zero — decimal.Zero, never a tolerance.
// migration.ReconcileFull sets that precedent: it reconciles to the paisa. A
// statement that "balances within a rupee" does not balance, and a tolerance is
// how a real error hides for a year.
//
// THREE TRAPS, EACH OF WHICH IS A TEST
// ------------------------------------
//  1. AS-AT, NOT AS-OF-NOW. An item whose bank_date is AFTER the statement date
//     is UN-CLEARED in that statement, even though it is reconciled today. The
//     filter is `bank_date <= as_on`, never `bank_date IS NOT NULL`. This is the
//     easiest bug in the whole track to write and the hardest to notice, because
//     today's statement looks right and last month's silently changes.
//  2. CURRENT VERSIONS ONLY. The postings join goes through
//     voucher_head.current_version_id exactly as movementSQL does, so an ALTERed
//     voucher contributes once at its new amount and a DELETED one contributes
//     nothing. The printed Day Book got this wrong by listing versions.
//  3. AN ALTER MUST NOT SILENTLY RE-POINT A RECONCILIATION. bank_reconciliation
//     stores what was cleared; if the current version's leg disagrees, the item
//     is BROKEN and reported as such rather than quietly counted. See
//     BrokenByAlter below and §0.1 of the memo.

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// BankReconLine is one bank-ledger posting as the reconciliation screen and the
// statement see it.
type BankReconLine struct {
	VoucherID   uuid.UUID
	LegNo       int
	VoucherNo   string
	VoucherDate string
	Narration   string
	Party       string
	// Amount is the magnitude of the bank leg; DrCr carries the direction, as
	// everywhere else in this codebase. Never a signed amount.
	Amount decimal.Decimal
	DrCr   string
	// BankDate is "" when the item has not cleared AS AT the statement date —
	// which includes an item cleared later. See trap (1) above.
	BankDate string
	// AgeDays is how long an un-cleared item has been outstanding as at the
	// statement date. An item un-cleared for 90+ days is a stale cheque, and
	// surfacing it is the single most useful thing this report does.
	AgeDays int
	// BrokenByAlter is true when a reconciliation exists for this leg but the
	// current version's amount or side no longer matches what was cleared.
	// The item is then treated as UN-CLEARED for the arithmetic and flagged, so
	// the difference cannot silently absorb an edit made after clearing.
	BrokenByAlter bool
	// ClearedAmount/ClearedDrCr are what the reconciliation recorded. Set only
	// when BrokenByAlter, where they are the evidence of what changed.
	ClearedAmount decimal.Decimal
	ClearedDrCr   string
}

// BankLedgerPredicate is THE definition of "this ledger is a bank account", as a
// SQL boolean expression over an aliased `ledger l` JOIN `account_group g`.
//
// IT IS ANCESTRY, NOT A GROUP NAME, AND THAT DISTINCTION IS THE WHOLE REASON
// THIS CONSTANT EXISTS. A company may keep its accounts in a user-created
// "Current Accounts" sub-group UNDER the seeded "Bank Accounts" primary —
// masters.CreateGroup takes a ParentName and account_group.path is an ltree — so
// such a ledger's IMMEDIATE group name is "Current Accounts" and only `path <@
// bg.path` recognises it.
//
// Two places need the answer: this file (which refuses a non-bank ledger by name)
// and the GET /ledgers projection (so a reconciliation screen can offer the right
// accounts). They were a copy-paste apart, and the copy that drifted would have
// failed in the worse direction — a picker that silently omits a real bank
// account shows a shorter list, not an error, and the operator concludes the
// product does not know about their account. So the expression is declared ONCE
// here and referenced by both; the agreement is pinned from both sides by
// TestIsBankAgreesWithTheStatementEndpoint (invariant I-9 of
// docs/design/t1t4-ui-model.md).
//
// The bg.company_id correlation is retained even though account_group is FORCE
// RLS: it costs nothing and keeps the expression correct if it is ever used
// outside a tenant-scoped transaction.
const BankLedgerPredicate = `EXISTS (
	    SELECT 1 FROM account_group bg
	     WHERE bg.company_id = l.company_id
	       AND bg.name IN ('Bank Accounts', 'Bank OD A/c')
	       AND g.path <@ bg.path
	)`

// BankReconciliation is the Bank Reconciliation Statement as at a date.
type BankReconciliation struct {
	LedgerID   uuid.UUID
	LedgerName string
	// Nature is the bank group's nature. A Bank OD A/c is a LIABILITY, so its
	// book balance is a credit — see signed().
	Nature string
	AsOn   string
	// BalanceAsPerBooks is the ledger's closing position as at AsOn, oriented by
	// nature exactly as every other statement orients it.
	BalanceAsPerBooks decimal.Decimal
	// UnclearedNet is the net effect of items the bank has not yet reflected,
	// in the same orientation as BalanceAsPerBooks.
	UnclearedNet decimal.Decimal
	// BalanceAsPerBank is BalanceAsPerBooks - UnclearedNet.
	BalanceAsPerBank decimal.Decimal
	// Difference is the residual. It is ALWAYS zero by construction; it exists
	// so the report can assert its own arithmetic rather than assume it, and so
	// a future statement-import slice has somewhere to put a genuine mismatch.
	Difference decimal.Decimal
	// Uncleared lists the items explaining the gap, oldest first.
	Uncleared []BankReconLine
	// Cleared lists items the bank has reflected as at AsOn.
	Cleared []BankReconLine
	// BrokenCount is how many reconciliations no longer match their voucher.
	// Non-zero means somebody altered a voucher after it was reconciled, and the
	// statement says so rather than absorbing it.
	BrokenCount int
}

// IsBalanced reports whether the statement's own arithmetic holds, to the paisa.
// Callers gate on this the way they gate on TrialBalance.IsBalanced.
func (br BankReconciliation) IsBalanced() bool {
	return br.Difference.Equal(decimal.Zero)
}

// bankReconSQL selects every bank-leg posting of a ledger up to `as_on`,
// left-joined to its reconciliation.
//
// $1 = ledger id, $2 = as_on date (may be NULL for "everything ever posted").
//
// THE JOIN TO voucher_head IS WHAT DOES THE WORK, exactly as in movementSQL:
// keeping only current_version_id means an altered voucher appears once at its
// new amount and a deleted one not at all.
//
// leg_no is computed with row_number() over the postings of the same
// (voucher, ledger), ordered by posting.id. v7 UUIDs are monotonic by creation,
// so this reproduces the order the legs were submitted in and is stable within a
// version. It is 0-based to match the column's CHECK and the Go field.
const bankReconSQL = `
	WITH legs AS (
	    SELECT p.id                AS posting_id,
	           vv.voucher_id       AS voucher_id,
	           vv.voucher_no       AS voucher_no,
	           to_char(vv.voucher_date, 'YYYY-MM-DD') AS voucher_date,
	           COALESCE(vv.payload->>'narration', '') AS narration,
	           COALESCE(vv.payload->>'party', '')     AS party,
	           p.amount            AS amount,
	           p.dr_cr::text       AS dr_cr,
	           row_number() OVER (
	               PARTITION BY vv.voucher_id, p.ledger_id
	               ORDER BY p.id
	           ) - 1               AS leg_no
	      FROM posting p
	      JOIN voucher_version vv ON vv.id = p.voucher_version_id
	      JOIN voucher_head vh
	        ON vh.company_id = vv.company_id
	       AND vh.voucher_id = vv.voucher_id
	       AND vh.current_version_id = vv.id
	     WHERE vh.is_deleted = false
	       AND p.ledger_id = $1
	       AND ($2::date IS NULL OR vv.voucher_date <= $2::date)
	)
	SELECT l.voucher_id, l.leg_no, l.voucher_no, l.voucher_date,
	       l.narration, l.party, l.amount, l.dr_cr,
	       to_char(br.bank_date, 'YYYY-MM-DD'), br.cleared_amount, br.cleared_dr_cr::text
	  FROM legs l
	  LEFT JOIN bank_reconciliation br
	         ON br.voucher_id = l.voucher_id
	        AND br.ledger_id  = $1
	        AND br.leg_no     = l.leg_no
	        -- AS-AT, NOT AS-OF-NOW. An item cleared AFTER the statement date is
	        -- un-cleared IN THAT STATEMENT. Putting this in the JOIN rather than
	        -- a WHERE is deliberate: a WHERE would drop the posting entirely
	        -- instead of showing it as un-cleared.
	        AND ($2::date IS NULL OR br.bank_date <= $2::date)
	 ORDER BY l.voucher_date, l.voucher_no, l.leg_no`

// BankReconciliationStatement builds the BRS for one bank ledger as at a date.
// An empty asOn means "everything ever posted".
func (s *Service) BankReconciliationStatement(
	ctx context.Context, companyID, ledgerID uuid.UUID, asOn string,
) (BankReconciliation, error) {
	if err := scope(ctx, companyID); err != nil {
		return BankReconciliation{}, err
	}
	out := BankReconciliation{LedgerID: ledgerID, AsOn: asOn}

	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// The ledger must exist, must belong to this company, and must be a
		// BANK ledger. Refusing a non-bank ledger by NAME is T1's diagnostic
		// refusal habit: the caller is told which field is wrong, not handed an
		// empty statement that looks like "nothing to reconcile".
		var isBank bool
		err := tx.QueryRow(ctx, `
			SELECT l.name, g.nature, `+BankLedgerPredicate+`
			  FROM ledger l
			  JOIN account_group g ON g.id = l.group_id
			 WHERE l.id = $1`, ledgerID).Scan(&out.LedgerName, &out.Nature, &isBank)
		if err == pgx.ErrNoRows {
			return fmt.Errorf("reporting: ledger %s not found", ledgerID)
		}
		if err != nil {
			return fmt.Errorf("reporting: load bank ledger: %w", err)
		}
		if !isBank {
			return fmt.Errorf(
				"reporting: ledger %q is not under a bank group; a bank reconciliation statement needs a bank ledger",
				out.LedgerName)
		}

		// Balance as per books, as at the date — from the SAME loader every
		// other statement uses, so the BRS agrees with the Trial Balance by
		// construction rather than by coincidence.
		balances, err := loadClosingBalances(ctx, tx, asOn)
		if err != nil {
			return fmt.Errorf("reporting: closing balances: %w", err)
		}
		for _, b := range balances {
			if b.LedgerID == ledgerID {
				out.BalanceAsPerBooks = b.signed()
				break
			}
		}

		rows, err := tx.Query(ctx, bankReconSQL, ledgerID, nullDate(asOn))
		if err != nil {
			return fmt.Errorf("reporting: bank legs: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var (
				ln            BankReconLine
				bankDate      *string
				clearedAmount *decimal.Decimal
				clearedDrCr   *string
			)
			if err := rows.Scan(&ln.VoucherID, &ln.LegNo, &ln.VoucherNo, &ln.VoucherDate,
				&ln.Narration, &ln.Party, &ln.Amount, &ln.DrCr,
				&bankDate, &clearedAmount, &clearedDrCr); err != nil {
				return fmt.Errorf("reporting: scan bank leg: %w", err)
			}

			if bankDate != nil {
				// TRAP (3): a reconciliation exists — but does it still describe
				// this leg? If the voucher was altered after clearing, the
				// amount or the side will have moved. Treat it as UN-CLEARED and
				// flag it, so the difference cannot silently absorb the edit.
				matches := clearedAmount != nil && clearedDrCr != nil &&
					clearedAmount.Equal(ln.Amount) && *clearedDrCr == ln.DrCr
				if matches {
					ln.BankDate = *bankDate
					out.Cleared = append(out.Cleared, ln)
					continue
				}
				ln.BrokenByAlter = true
				if clearedAmount != nil {
					ln.ClearedAmount = *clearedAmount
				}
				if clearedDrCr != nil {
					ln.ClearedDrCr = *clearedDrCr
				}
				out.BrokenCount++
			}
			out.Uncleared = append(out.Uncleared, ln)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("reporting: bank legs: %w", err)
		}
		return nil
	})
	if err != nil {
		return BankReconciliation{}, err
	}

	// The un-cleared net, oriented the SAME way as BalanceAsPerBooks. For a
	// debit-natured bank account a debit leg increases the book balance, so an
	// un-cleared debit is money the books show and the bank does not. For a
	// credit-natured overdraft the orientation flips — and it flips HERE, once,
	// rather than at every call site.
	debitNatured := out.Nature == "ASSET" || out.Nature == "EXPENSE"
	// asOn empty means "everything ever posted"; resolveAsOn then gives today,
	// which is the right reference for ageing an item on an unbounded statement.
	asOnTime, err := resolveAsOn(asOn)
	if err != nil {
		return BankReconciliation{}, err
	}
	for i := range out.Uncleared {
		ln := &out.Uncleared[i]
		signed := ln.Amount
		if (ln.DrCr == "D") != debitNatured {
			signed = signed.Neg()
		}
		out.UnclearedNet = out.UnclearedNet.Add(signed)
		if vd, perr := time.Parse("2006-01-02", ln.VoucherDate); perr == nil {
			ln.AgeDays = daysBetween(vd, asOnTime)
		}
	}

	out.BalanceAsPerBank = out.BalanceAsPerBooks.Sub(out.UnclearedNet)
	// Zero BY CONSTRUCTION. The value of asserting it is that the test computes
	// the bank balance INDEPENDENTLY (by summing cleared legs) and compares — a
	// report that re-derives its own figure and agrees with itself proves
	// nothing (§3.4).
	out.Difference = out.BalanceAsPerBooks.
		Sub(out.UnclearedNet).
		Sub(out.BalanceAsPerBank)
	return out, nil
}
