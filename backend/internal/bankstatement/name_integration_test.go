package bankstatement_test

// D111 — WHAT THE DATABASE ACTUALLY HOLDS FOR A STATEMENT THAT ARRIVED WITH NO
// FILE NAME.
//
// ⛔ THE PURE TEST (name_test.go) PROVES THE SENTENCE; THIS PROVES THE COLUMN.
// The defect was measured in psql — `SELECT file_name FROM
// bank_statement_import` returning two empty strings after two pasted imports —
// so the fix is measured there too. A test that only exercised the naming
// helper would stay green with the writer still storing `req.FileName`, which
// is exactly the line that was wrong.

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/bankstatement"
)

const namelessCSV = "Date,Narration,Withdrawal,Deposit\n" +
	"2025-04-05,NEFT ACME,,100000.00\n" +
	"2025-04-08,RENT APRIL,25000.00,\n"

// A DIFFERENT file over the SAME period with the SAME row count: the pair the
// register says must stay distinguishable. Only the narrations differ.
const namelessCSVTwin = "Date,Narration,Withdrawal,Deposit\n" +
	"2025-04-05,NEFT BORAX,,100000.00\n" +
	"2025-04-08,RENT APRIL (REVISED),25000.00,\n"

func (f *matchFixture) importNameless(t *testing.T, csv string) bankstatement.ImportResult {
	t.Helper()
	res, err := bankstatement.NewService(f.pool).Import(f.ctx, f.companyID,
		bankstatement.ImportRequest{
			LedgerID: f.bank,
			// EMPTY, exactly as the panel sends it when the operator pasted the
			// statement instead of choosing a file.
			FileName: "",
			Content:  csv,
			Mapping: bankstatement.ColumnMapping{
				ValueDate:   "Date",
				DateLayout:  bankstatement.YMDDash,
				Withdrawal:  "Withdrawal",
				Deposit:     "Deposit",
				Encoding:    bankstatement.TwoColumns,
				Description: "Narration",
			},
			By: "tester",
		})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	return res
}

// storedNames reads the column the disclosure quotes. psql is the oracle.
func (f *matchFixture) storedNames(t *testing.T) []string {
	t.Helper()
	var out []string
	if err := f.pool.RunInTx(f.ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT file_name FROM bank_statement_import
			  WHERE company_id = $1 ORDER BY file_name`, f.companyID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				return err
			}
			out = append(out, n)
		}
		return rows.Err()
	}); err != nil {
		t.Fatalf("read file names: %v", err)
	}
	return out
}

func TestPastedStatementIsStoredWithADerivedName(t *testing.T) {
	f := matchSetup(t)
	res := f.importNameless(t, namelessCSV)

	// The period and the row count come from the FILE, so the name can be
	// predicted exactly — everything but the fingerprint.
	const want = "pasted statement 2025-04-05 to 2025-04-08, 2 rows ("
	if !strings.HasPrefix(res.FileName, want) || !strings.HasSuffix(res.FileName, ")") {
		t.Fatalf("ImportResult.FileName = %q, want a name beginning %q", res.FileName, want)
	}

	// ⛔ AND THE SAME STRING IS IN THE TABLE. This is the assertion that fails
	// if the INSERT goes on writing req.FileName: it stored "" and every
	// disclosure quoting it read `""`.
	names := f.storedNames(t)
	if len(names) != 1 {
		t.Fatalf("bank_statement_import holds %d rows, want 1: %q", len(names), names)
	}
	if names[0] != res.FileName {
		t.Fatalf("stored %q, returned %q — the two must be the same string, "+
			"because the operator is shown one and the disclosures quote the other",
			names[0], res.FileName)
	}
	if names[0] == "" {
		t.Fatal(`file_name is "" — this IS D111: the disclosure that names the ` +
			`unchecked file renders an empty string`)
	}
}

func TestTwoPastedStatementsAreDistinguishableInTheDatabase(t *testing.T) {
	f := matchSetup(t)
	a := f.importNameless(t, namelessCSV)
	b := f.importNameless(t, namelessCSVTwin)

	if a.FileName == b.FileName {
		t.Fatalf("two pasted statements share the name %q — with more than one "+
			"import on an account the disclosure names none of them, which is "+
			"the defect a constant fallback like \"pasted.csv\" would preserve",
			a.FileName)
	}
	names := f.storedNames(t)
	if len(names) != 2 {
		t.Fatalf("bank_statement_import holds %d rows, want 2: %q", len(names), names)
	}
	if names[0] == names[1] {
		t.Fatalf("both rows are named %q", names[0])
	}
}

// ⭐ THE CONTROL: a CHOSEN file keeps the operator's own name, byte for byte.
// A server that renamed it would be editing evidence.
func TestAChosenFileNameIsStoredUnchanged(t *testing.T) {
	f := matchSetup(t)
	f.importCSV(t, namelessCSV) // sends FileName: "statement.csv"
	names := f.storedNames(t)
	if len(names) != 1 || names[0] != "statement.csv" {
		t.Fatalf("stored %q, want [statement.csv]", names)
	}
}

var _ = context.Background
