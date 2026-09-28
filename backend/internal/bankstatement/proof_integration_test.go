package bankstatement_test

// W5 — the balance proof and the narration columns, through the REAL import
// against Postgres. Design memo: docs/design/d123-w5-documents-bank.md §2.
//
// psql is the oracle: every assertion about what was stored is a SELECT, not
// the ImportResult, because the result is what the service SAYS it wrote.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/bankstatement"
	"github.com/khata/cored/internal/reporting"
)

const unprovableCSV = "Date,Narration,Withdrawal,Deposit\n" +
	"2025-04-05,UPI-ACME TRADERS-acme.traders@okhdfcbank-HDFC0001234-412345678901-INV 42,,100000.00\n" +
	"2025-04-08,NEFT DR-ICIC0000104-PUNE FABRICATORS-HDFCN52025040812345678,25000.00,\n"

func unprovableMapping() bankstatement.ColumnMapping {
	return bankstatement.ColumnMapping{
		ValueDate: "Date", DateLayout: bankstatement.YMDDash, Withdrawal: "Withdrawal",
		Deposit: "Deposit", Encoding: bankstatement.TwoColumns, Description: "Narration",
	}
}

// ⛔ THE GATE: no proof, no import — and NOTHING is written, not even the
// batch row. A refusal that left a batch behind would make the next,
// correctly-declared attempt a "duplicate file" no-op that imports nothing.
func TestAnUnprovableStatementIsRefusedAndWritesNothing(t *testing.T) {
	f := matchSetup(t)
	svc := bankstatement.NewService(f.pool)
	_, err := svc.Import(f.ctx, f.companyID, bankstatement.ImportRequest{
		LedgerID: f.bank, FileName: "april.csv", Content: unprovableCSV,
		Mapping: unprovableMapping(), By: "tester",
	})
	if !errors.Is(err, bankstatement.ErrNoBalanceProof) {
		t.Fatalf("want ErrNoBalanceProof, got %v", err)
	}
	if n := f.count(t, "bank_statement_import"); n != 0 {
		t.Fatalf("a refused import left %d batch row(s) behind", n)
	}
	if n := f.count(t, "bank_statement_line"); n != 0 {
		t.Fatalf("a refused import left %d line(s) behind", n)
	}

	// The same file, with the balances printed on the statement keyed in,
	// imports — and a closing one rupee out is refused, also writing nothing.
	opening, wrong := decimal.RequireFromString("5000"), decimal.RequireFromString("80001")
	_, err = svc.Import(f.ctx, f.companyID, bankstatement.ImportRequest{
		LedgerID: f.bank, FileName: "april.csv", Content: unprovableCSV,
		Mapping: unprovableMapping(), By: "tester",
		Declared: bankstatement.Declared{Opening: &opening, Closing: &wrong},
	})
	if !errors.Is(err, bankstatement.ErrBalanceProof) {
		t.Fatalf("a closing that does not foot must be refused, got %v", err)
	}
	if n := f.count(t, "bank_statement_import"); n != 0 {
		t.Fatalf("a failed proof left %d batch row(s) behind", n)
	}

	closing := decimal.RequireFromString("80000") // 5,000 + 1,00,000 − 25,000
	res, err := svc.Import(f.ctx, f.companyID, bankstatement.ImportRequest{
		LedgerID: f.bank, FileName: "april.csv", Content: unprovableCSV,
		Mapping: unprovableMapping(), By: "tester",
		Declared: bankstatement.Declared{Opening: &opening, Closing: &closing},
	})
	if err != nil {
		t.Fatalf("a declared-and-footing statement must import: %v", err)
	}
	if res.BalanceProof != bankstatement.ProofDeclared || res.BalanceWalked {
		t.Errorf("result says proof %q walked %v; want declared, not walked", res.BalanceProof, res.BalanceWalked)
	}

	// STORED, read back by SQL.
	var (
		proof, by     string
		order         *string
		open, close   decimal.Decimal
		rail, cp, vpa *string
		ref           *string
		rail2, cp2    *string
		ref2          *string
	)
	var dbErr error
	_ = f.pool.RunInTx(f.ctx, func(ctx context.Context, tx pgx.Tx) error {
		dbErr = tx.QueryRow(ctx, `
			SELECT balance_proof, balance_declared_by, row_order, opening_balance, closing_balance
			  FROM bank_statement_import WHERE company_id = $1`, f.companyID).
			Scan(&proof, &by, &order, &open, &close)
		if dbErr != nil {
			return dbErr
		}
		dbErr = tx.QueryRow(ctx, `
			SELECT txn_rail, counterparty, counterparty_vpa, rail_reference
			  FROM bank_statement_line WHERE company_id = $1 AND line_no = 2`, f.companyID).
			Scan(&rail, &cp, &vpa, &ref)
		if dbErr != nil {
			return dbErr
		}
		dbErr = tx.QueryRow(ctx, `
			SELECT txn_rail, counterparty, rail_reference
			  FROM bank_statement_line WHERE company_id = $1 AND line_no = 3`, f.companyID).
			Scan(&rail2, &cp2, &ref2)
		return dbErr
	})
	// Asserted OUTSIDE the closure (HANDOFF-AGENTIC-AFTER-W0.md §7.1).
	if dbErr != nil {
		t.Fatalf("read back: %v", dbErr)
	}
	if proof != "declared" || by != "operator" || order != nil {
		t.Errorf("stored proof %q by %q order %v; want declared / operator / NULL", proof, by, order)
	}
	if open.StringFixed(2) != "5000.00" || close.StringFixed(2) != "80000.00" {
		t.Errorf("stored balances %s / %s", open.StringFixed(2), close.StringFixed(2))
	}
	if rail == nil || *rail != "UPI" || cp == nil || *cp != "ACME TRADERS" ||
		vpa == nil || *vpa != "acme.traders@okhdfcbank" || ref == nil || *ref != "412345678901" {
		t.Errorf("UPI row narration stored as %v %v %v %v", deref(rail), deref(cp), deref(vpa), deref(ref))
	}
	if rail2 == nil || *rail2 != "NEFT" || cp2 == nil || *cp2 != "PUNE FABRICATORS" ||
		ref2 == nil || *ref2 != "HDFCN52025040812345678" {
		t.Errorf("NEFT row narration stored as %v %v %v", deref(rail2), deref(cp2), deref(ref2))
	}

	// ⛔ THE READ MODEL MUST NOT CALL A DECLARED PROOF A WALK. Before 0101 it
	// read "walked" off opening_balance IS NOT NULL, which a declared-only
	// import now sets.
	st, err := reporting.NewService(f.pool).ImportedStatementReport(f.ctx, f.companyID, f.bank, "", "")
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if len(st.Imports) != 1 {
		t.Fatalf("want 1 import in the report, got %d", len(st.Imports))
	}
	if st.Imports[0].BalanceWalked || st.Imports[0].BalanceProof != "declared" {
		t.Errorf("report says walked=%v proof=%q; a declared proof is not a walk",
			st.Imports[0].BalanceWalked, st.Imports[0].BalanceProof)
	}
}

// The database refuses a proof it cannot show, whichever writer tries.
func TestTheDatabaseRefusesAProofWithNoBalances(t *testing.T) {
	f := matchSetup(t)
	var execErr error
	_ = f.pool.RunInTx(f.ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, execErr = tx.Exec(ctx, `
			INSERT INTO bank_statement_import
			    (id, company_id, ledger_id, file_name, file_sha256, format, column_mapping,
			     line_count, imported_by, balance_proof)
			VALUES (gen_random_uuid(), $1, $2, 'x.csv', 'deadbeef', 'csv', '{}', 1, 't', 'running')`,
			f.companyID, f.bank)
		return execErr
	})
	// By NAME: a refusal for any other reason (a trigger, a format check) would
	// leave this CHECK untested while the test stayed green.
	if execErr == nil || !strings.Contains(execErr.Error(), "bank_statement_import_proof_has_balances") {
		t.Fatalf("a row naming a proof with no opening/closing balance must be refused by "+
			"bank_statement_import_proof_has_balances, got %v", execErr)
	}
}

func deref(s *string) string {
	if s == nil {
		return "<NULL>"
	}
	return *s
}
