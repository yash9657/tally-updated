package reporting

// THE IMPORTED BANK STATEMENT — what the bank said, as a report.
//
// Design memo: docs/design/t4-slice2-model.md §8.3 (slice 2a).
// RESEARCH.md:876 names Tally's equivalent: the "Imported Bank Data" report.
//
// WHY THE READ IS HERE AND THE WRITE IS IN internal/bankstatement
// ---------------------------------------------------------------
// The same split T4 slice 1 made, for the same reason: this package's doc
// commits it to being read-only, and that sentence is load-bearing — it is why
// a report can never have a side effect on the books. Importing is a write, so
// it lives in its own package; reading back what was imported is a report, so
// it lives here beside the Bank Reconciliation Statement it will eventually sit
// next to.
//
// ⚠️ THIS REPORT SAYS NOTHING ABOUT WHAT HAS CLEARED, AND THAT IS STILL NOT AN
// OVERSIGHT — but the REASON changed when slice 2b shipped on 2026-08-28, and
// the disclosure changed with it.
//
// It used to say "matching is not built". That is now FALSE: Alt+S proposes
// matches and an accepted one writes a bank_reconciliation row. What remains
// true is that THIS report does not show match status, and a "Status" column
// here would need the match data this query does not load — so a row shown here
// may already be matched, set aside, or untouched, and the report says exactly
// that rather than leaving silence to read as "none of these matched".
//
// ⛔ THE OLD SENTENCE WAS LEFT STANDING BY THE FEATURE WORK AND WAS CAUGHT BY
// verify:demo, which asserts the disclosure exists. A gate that pins the
// PRESENCE of a disclosure does not pin its TRUTH — the check now looks for the
// current sentence, and that is the only reason the lie was short-lived.

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// BankStatementLine is one imported transaction as the screen sees it.
type BankStatementLine struct {
	ID        uuid.UUID
	ImportID  uuid.UUID
	LineNo    int
	ValueDate string
	// TxnDate is "" when the file carried only one date. It is NOT defaulted to
	// the value date: the whole point of storing both is that they can differ.
	TxnDate     string
	Description string
	// Amount is a magnitude; DrCr carries the direction, in OUR orientation —
	// "D" is money IN to the bank account. See migration 0062's column comment.
	Amount decimal.Decimal
	DrCr   string
	// RunningBalance is the bank's own figure. HasBalance distinguishes "the
	// file said zero" from "the file said nothing".
	RunningBalance decimal.Decimal
	HasBalance     bool
	BankReference  string
	ChequeNumber   string
}

// BankStatementImport is one imported file, as a header over its lines.
type BankStatementImport struct {
	ID         uuid.UUID
	FileName   string
	ImportedBy string
	ImportedAt string
	PeriodFrom string
	PeriodTo   string
	LineCount  int
	// BalanceWalked is false when the file carried no running balance, in which
	// case the dropped-row check never ran. Carried per import, not per report,
	// because a screen may show one file that walked and one that could not.
	BalanceWalked bool
}

// ImportedStatement is every line imported for one bank account over a period,
// with the files they came from.
type ImportedStatement struct {
	LedgerID   uuid.UUID
	LedgerName string
	// BankAccountNumber and BankIFSC are "" when NOT RECORDED, and a client
	// must render those words rather than an empty field — invariant I-11: a
	// blank labelled field reads as "this bank has no IFSC".
	BankAccountNumber string
	BankIFSC          string
	From              string
	To                string
	Imports           []BankStatementImport
	Lines             []BankStatementLine
	// TotalIn/TotalOut are the sums of the period's lines, in our orientation.
	// They are here so the screen can show the operator that what it displays
	// adds up to what the bank moved — NOT as an input to any derivation.
	TotalIn  decimal.Decimal
	TotalOut decimal.Decimal
	// Disclosures are what this report cannot tell you, in an operator's words.
	Disclosures []string
}

const importedLinesSQL = `
	SELECT l.id, l.import_id, l.line_no,
	       to_char(l.value_date, 'YYYY-MM-DD'),
	       COALESCE(to_char(l.txn_date, 'YYYY-MM-DD'), ''),
	       l.description, l.amount, l.dr_cr::text,
	       l.running_balance,
	       COALESCE(l.bank_reference, ''), COALESCE(l.cheque_number, '')
	  FROM bank_statement_line l
	 WHERE l.company_id = $1
	   AND l.ledger_id  = $2
	   AND ($3::date IS NULL OR l.value_date >= $3::date)
	   AND ($4::date IS NULL OR l.value_date <= $4::date)
	 ORDER BY l.value_date, l.line_no, l.id`

const importsSQL = `
	SELECT i.id, i.file_name, i.imported_by,
	       to_char(i.imported_at, 'YYYY-MM-DD"T"HH24:MI:SSOF'),
	       COALESCE(to_char(i.period_from, 'YYYY-MM-DD'), ''),
	       COALESCE(to_char(i.period_to, 'YYYY-MM-DD'), ''),
	       i.line_count,
	       -- The walk RAN if and only if the file carried balances, which is
	       -- exactly when we stored one. NULL here is "could not check", never
	       -- "checked and found nothing".
	       (i.opening_balance IS NOT NULL)
	  FROM bank_statement_import i
	 WHERE i.company_id = $1
	   AND i.ledger_id  = $2
	   AND ($3::date IS NULL OR i.period_to   >= $3::date)
	   AND ($4::date IS NULL OR i.period_from <= $4::date)
	 ORDER BY i.imported_at DESC, i.id DESC`

// ImportedStatementReport builds the imported-statement view for one bank
// ledger over an optional period. Empty from/to mean "everything imported".
func (s *Service) ImportedStatementReport(
	ctx context.Context, companyID, ledgerID uuid.UUID, from, to string,
) (ImportedStatement, error) {
	if err := scope(ctx, companyID); err != nil {
		return ImportedStatement{}, err
	}
	out := ImportedStatement{LedgerID: ledgerID, From: from, To: to}

	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// The ledger must exist, belong to this company, and be a BANK ledger.
		// Refusing by NAME is T1's diagnostic-refusal habit: an empty statement
		// would read as "nothing imported" and send the operator hunting.
		var (
			isBank bool
			acct   *string
			ifsc   *string
		)
		err := tx.QueryRow(ctx, `
			SELECT l.name, `+BankLedgerPredicate+`, l.bank_account_number, l.bank_ifsc
			  FROM ledger l
			  JOIN account_group g ON g.id = l.group_id
			 WHERE l.id = $1`, ledgerID).Scan(&out.LedgerName, &isBank, &acct, &ifsc)
		if err == pgx.ErrNoRows {
			return fmt.Errorf("reporting: ledger %s not found", ledgerID)
		}
		if err != nil {
			return fmt.Errorf("reporting: load bank ledger: %w", err)
		}
		if !isBank {
			return fmt.Errorf(
				"reporting: ledger %q is not under a bank group; only a bank account has a statement to import",
				out.LedgerName)
		}
		if acct != nil {
			out.BankAccountNumber = *acct
		}
		if ifsc != nil {
			out.BankIFSC = *ifsc
		}

		rows, err := tx.Query(ctx, importsSQL, companyID, ledgerID, nullDate(from), nullDate(to))
		if err != nil {
			return fmt.Errorf("reporting: imports: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var im BankStatementImport
			if err := rows.Scan(&im.ID, &im.FileName, &im.ImportedBy, &im.ImportedAt,
				&im.PeriodFrom, &im.PeriodTo, &im.LineCount, &im.BalanceWalked); err != nil {
				return fmt.Errorf("reporting: scan import: %w", err)
			}
			out.Imports = append(out.Imports, im)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("reporting: imports: %w", err)
		}

		lineRows, err := tx.Query(ctx, importedLinesSQL, companyID, ledgerID, nullDate(from), nullDate(to))
		if err != nil {
			return fmt.Errorf("reporting: statement lines: %w", err)
		}
		defer lineRows.Close()
		for lineRows.Next() {
			var (
				ln  BankStatementLine
				bal *decimal.Decimal
			)
			if err := lineRows.Scan(&ln.ID, &ln.ImportID, &ln.LineNo, &ln.ValueDate,
				&ln.TxnDate, &ln.Description, &ln.Amount, &ln.DrCr,
				&bal, &ln.BankReference, &ln.ChequeNumber); err != nil {
				return fmt.Errorf("reporting: scan statement line: %w", err)
			}
			if bal != nil {
				ln.RunningBalance, ln.HasBalance = *bal, true
			}
			out.Lines = append(out.Lines, ln)
		}
		return lineRows.Err()
	})
	if err != nil {
		return ImportedStatement{}, err
	}

	for _, l := range out.Lines {
		if l.DrCr == "D" {
			out.TotalIn = out.TotalIn.Add(l.Amount)
		} else {
			out.TotalOut = out.TotalOut.Add(l.Amount)
		}
	}

	// ⚠️ THE DISCLOSURES ARE PART OF THE REPORT, not decoration. Each one is a
	// question the operator would otherwise answer wrongly from silence.
	out.Disclosures = append(out.Disclosures,
		"THIS REPORT SHOWS NO MATCH STATUS (T4 slice 2b). These are the bank's own rows, and a row "+
			"here may already be matched to a voucher, set aside as matching nothing, or untouched — "+
			"nothing on this report says which. Matching now works: press Alt+S on the Bank "+
			"Reconciliation screen. Reconciling by hand, a Bank Date at a time, still works too.")
	for _, im := range out.Imports {
		if !im.BalanceWalked {
			out.Disclosures = append(out.Disclosures, fmt.Sprintf(
				"%q carried no running balance, so its rows were NOT balance-walked: a row dropped or "+
					"duplicated by the export would not have been detected.", im.FileName))
		}
	}
	if out.BankAccountNumber == "" || out.BankIFSC == "" {
		out.Disclosures = append(out.Disclosures,
			"This ledger has no account number or IFSC recorded, so imports against it cannot be checked "+
				"against the file's own account. Record them on the ledger.")
	}
	return out, nil
}
