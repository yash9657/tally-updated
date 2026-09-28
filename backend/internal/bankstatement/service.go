package bankstatement

// THE WRITE PATH — persisting one imported file, in ONE transaction.
//
// Design memo: docs/design/t4-slice2-model.md §2.1 and §8.
//
// WHAT THIS SERVICE DELIBERATELY CANNOT DO
// ----------------------------------------
// It never writes bank_reconciliation, never touches a voucher, and never
// records that anything CLEARED. Importing a statement asserts what the BANK
// said; it asserts nothing about our books. The two meet only in slice 2b's
// matcher, and even then the clearance lands in 0045's table — the single
// source of "cleared" (memo §2.1).
//
// That is not a layering nicety. bank_reconciliation carries the alter-detection
// mechanism: it stores what was cleared and the reporting layer compares it
// against the current version, marking an item BROKEN BY ALTER rather than
// silently valid. A second table recording clearance would lose that silently.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/platform/db"
)

var (
	// ErrNotABankLedger is the ledger refusal, surfaced as a sentinel so the
	// HTTP layer can answer 400 and a test can assert THIS refusal rather than
	// "some error happened".
	ErrNotABankLedger = errors.New("bankstatement: that ledger is not a bank account")
	// ErrWrongAccount is the file's account number disagreeing with the
	// ledger's. It is the refusal that stops a morning's work going into the
	// wrong account — see the ImportRequest field comment.
	ErrWrongAccount = errors.New("bankstatement: this file is for a different account")
)

// TxRunner is the minimal database capability this package needs, mirroring
// banking.TxRunner.
type TxRunner interface {
	RunInTx(ctx context.Context, fn db.TxFunc) error
}

// Service is the statement-import write service.
type Service struct{ runner TxRunner }

// NewService constructs a Service over the given transaction runner.
func NewService(runner TxRunner) *Service { return &Service{runner: runner} }

// ImportRequest is one import.
type ImportRequest struct {
	LedgerID uuid.UUID
	FileName string
	Content  string
	Mapping  ColumnMapping
	By       string
	// StatementAccountNumber is what THE FILE says the account is, when the
	// operator or the client could read it off the statement. Optional.
	//
	// IT IS SUPPLIED RATHER THAN EXTRACTED, and that is the §8.2 rule again: an
	// account number sits in a free-form preamble whose shape differs per bank,
	// and a regex that scrapes "the number that looks like an account" would
	// eventually scrape a customer reference and refuse a correct import — or,
	// worse, match nothing and quietly stop checking.
	//
	// When it and the ledger's recorded number are both present they MUST
	// agree, because a statement imported against the wrong ledger reconciles
	// nothing and corrupts two accounts at once, silently: every line is real
	// and both accounts still balance.
	StatementAccountNumber string
	// Declared is the opening and closing balance the OPERATOR read off the
	// printed statement (W5). Optional when the file carries a running balance
	// or its own summary; REQUIRED in effect when it carries neither, because
	// ParseStatement refuses a statement it cannot prove. When the file states
	// the same figure the two must agree — see proof.go.
	Declared Declared
}

// ImportResult is what happened, in the terms an operator needs.
type ImportResult struct {
	ImportID uuid.UUID
	// FileName is the name the import was STORED under — the operator's own, or
	// the one derived from the statement when they pasted rather than chose a
	// file (D111). It is returned rather than assumed, so a client can show the
	// operator the string the disclosures will quote back at them.
	FileName string
	// AlreadyImported is true when this exact file has been imported into this
	// account before. It is a NO-OP AND A SUCCESS, not an error: the operator's
	// intent ("this file should be in the system") is satisfied, and making a
	// double-click an error is friction with no safety gain.
	AlreadyImported bool
	// LinesInFile is what the file contained; NewLines is what this import
	// added. They differ when statements overlap, which is the ordinary case —
	// an operator downloading 1–31 January and then 15 January–15 February.
	LinesInFile    int
	NewLines       int
	DuplicateLines int

	OpeningBalance string
	ClosingBalance string
	// BalanceWalked is false when the file carried no running balance. The
	// caller MUST surface it: a check that did not run must never be presented
	// the same way as a check that passed.
	BalanceWalked bool
	// BalanceProof is WHICH proof held (proof.go). Never empty on a successful
	// import since W5: a statement with no proof is refused, not imported.
	BalanceProof ProofMethod
	// BalanceDeclaredBy is "file", "operator", "file+operator" or "".
	BalanceDeclaredBy string
	// RowOrder is what the running balances revealed, or "" without them.
	RowOrder Order
	// ProofChecks lists each comparison that ran and held, in words.
	ProofChecks []string
	PeriodFrom  string
	PeriodTo    string
	// Disclosures are the things this import could NOT verify, in the
	// operator's words. An empty list means everything checkable was checked.
	Disclosures []string
}

// Import parses a file and persists it. Everything happens in one transaction:
// a half-imported statement whose balances no longer walk is indistinguishable
// from a bank error, and it would be discovered by somebody reconciling.
func (s *Service) Import(ctx context.Context, companyID uuid.UUID, req ImportRequest) (ImportResult, error) {
	if err := scope(ctx, companyID); err != nil {
		return ImportResult{}, err
	}

	// PARSE BEFORE OPENING THE TRANSACTION. Every refusal in parse.go is about
	// the FILE and needs no database at all, so a bad file never holds a
	// connection or leaves a rolled-back batch id behind.
	//
	// ⛔ ParseStatement, NOT Parse (W5). The balance proof is mandatory: a file
	// with neither a running balance nor a declared opening and closing is
	// REFUSED here. Slice 2a imported it with a disclosure; the disclosure was
	// read once and the unverifiable rows were matched against for a year.
	st, err := ParseStatement(req.Content, req.Mapping, req.Declared)
	if err != nil {
		return ImportResult{}, err
	}

	sum := sha256.Sum256([]byte(req.Content))
	fileHash := hex.EncodeToString(sum[:])
	mappingJSON, err := json.Marshal(req.Mapping)
	if err != nil {
		return ImportResult{}, fmt.Errorf("bankstatement: encode mapping: %w", err)
	}

	out := ImportResult{
		LinesInFile:   len(st.Lines),
		BalanceWalked: st.BalanceWalked,
		PeriodFrom:    st.PeriodFrom.Format("2006-01-02"),
		PeriodTo:      st.PeriodTo.Format("2006-01-02"),
		// The PROOF's figures: the walk's when it ran, else the declared ones.
		OpeningBalance:    st.Proof.Opening.StringFixed(4),
		ClosingBalance:    st.Proof.Closing.StringFixed(4),
		BalanceProof:      st.Proof.Method,
		BalanceDeclaredBy: st.Proof.DeclaredBy,
		RowOrder:          st.Order,
		ProofChecks:       st.Proof.Checks,
	}
	// D111 — A STATEMENT WITH NO NAME IS STILL NAMED, from its own contents.
	// The disclosure on the combined import report quotes this string to say
	// WHICH file could not be balance-walked; stored empty (which is what every
	// PASTED statement was) it renders as `""` and names nothing. See name.go
	// for why the fallback is derived rather than constant, and note it is
	// computed AFTER the parse because the period and the row count come from
	// the file itself.
	out.FileName = resolveFileName(req.FileName, out.PeriodFrom, out.PeriodTo,
		len(st.Lines), fileHash)
	if !st.BalanceWalked {
		// PROVED, BUT BY THE WEAKER OF THE TWO CHECKS, and the difference is
		// stated rather than implied. A declared-totals proof catches any single
		// dropped or duplicated row; it cannot catch two errors that cancel.
		out.Disclosures = append(out.Disclosures,
			"This file carries no running balance, so it was proved by its opening and closing balance "+
				"(opening + deposits − withdrawals = closing) rather than row by row. A single missing or "+
				"duplicated row would have been refused; two errors of exactly equal and opposite amount "+
				"would not have been detected.")
	}

	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.checkAccount(ctx, tx, companyID, req, &out); err != nil {
			return err
		}

		// Has this exact file been imported into this account already? Asking
		// first (rather than catching the unique violation) is what lets the
		// answer be a no-op with the ORIGINAL import's id, which is the thing
		// the operator actually wants to see.
		var existing uuid.UUID
		err := tx.QueryRow(ctx, `
			SELECT id FROM bank_statement_import
			 WHERE company_id = $1 AND ledger_id = $2 AND file_sha256 = $3`,
			companyID, req.LedgerID, fileHash).Scan(&existing)
		switch {
		case err == nil:
			out.ImportID = existing
			out.AlreadyImported = true
			out.NewLines = 0
			out.DuplicateLines = len(st.Lines)
			return nil
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("bankstatement: look up prior import: %w", err)
		}

		importID, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("bankstatement: new id: %w", err)
		}
		out.ImportID = importID

		// The bank-group check lives in 0062's trigger, so it holds for ANY
		// writer — including a by-hand psql fix — not only for this function.
		_, err = tx.Exec(ctx, `
			INSERT INTO bank_statement_import
			    (id, company_id, ledger_id, file_name, file_sha256, format,
			     column_mapping, opening_balance, closing_balance,
			     period_from, period_to, line_count, imported_by,
			     balance_proof, balance_declared_by, row_order)
			VALUES ($1, $2, $3, $4, $5, 'csv', $6,
			        $7, $8, $9::date, $10::date, $11, $12, $13, $14, $15)`,
			importID, companyID, req.LedgerID, out.FileName, fileHash,
			mappingJSON, st.Proof.Opening, st.Proof.Closing,
			out.PeriodFrom, out.PeriodTo, len(st.Lines), req.By,
			string(st.Proof.Method), nullText(st.Proof.DeclaredBy), nullText(string(st.Order)))
		if err != nil {
			if strings.Contains(err.Error(), "not under a bank group") {
				return fmt.Errorf("%w: %s", ErrNotABankLedger, err.Error())
			}
			return fmt.Errorf("bankstatement: record import: %w", err)
		}

		for _, ln := range st.Lines {
			lineID, err := uuid.NewV7()
			if err != nil {
				return fmt.Errorf("bankstatement: new line id: %w", err)
			}
			raw, err := json.Marshal(ln.Raw)
			if err != nil {
				return fmt.Errorf("bankstatement: encode raw row: %w", err)
			}
			// ON CONFLICT DO NOTHING is invariant B-3. A line already present
			// from an overlapping statement is not an error and not a second
			// row: it is the SAME transaction, and the bank only did it once.
			tag, err := tx.Exec(ctx, `
				INSERT INTO bank_statement_line
				    (id, company_id, import_id, ledger_id, line_no,
				     value_date, txn_date, description, amount, dr_cr,
				     running_balance, bank_reference, cheque_number, raw,
				     line_hash, occurrence,
				     txn_rail, counterparty, counterparty_vpa, rail_reference)
				VALUES ($1, $2, $3, $4, $5, $6::date, $7::date, $8, $9, $10::dr_cr,
				        $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
				ON CONFLICT (company_id, ledger_id, line_hash, occurrence) DO NOTHING`,
				lineID, companyID, importID, req.LedgerID, ln.LineNo,
				ln.ValueDate.Format("2006-01-02"), nullDate(ln.TxnDate),
				ln.Description, ln.Amount, ln.DrCr,
				nullBalance(ln), nullText(ln.BankReference), nullText(ln.ChequeNumber),
				raw, ln.Hash, ln.Occurrence,
				nullText(string(ln.Narration.Rail)), nullText(ln.Narration.Counterparty),
				nullText(ln.Narration.VPA), nullText(ln.Narration.Reference))
			if err != nil {
				return fmt.Errorf("bankstatement: insert line %d: %w", ln.LineNo, err)
			}
			if tag.RowsAffected() == 1 {
				out.NewLines++
			} else {
				out.DuplicateLines++
			}
		}
		return nil
	})
	if err != nil {
		return ImportResult{}, err
	}
	if out.DuplicateLines > 0 && !out.AlreadyImported {
		out.Disclosures = append(out.Disclosures, fmt.Sprintf(
			"%d of the %d rows in this file were already on this account from an earlier import "+
				"and were not added again — overlapping statements are ordinary.",
			out.DuplicateLines, out.LinesInFile))
	}
	return out, nil
}

// checkAccount compares the file's account number against the ledger's.
//
// EVERY OUTCOME IS STATED, including the two where nothing could be checked.
// Silence here would be the worst answer available: an import that says nothing
// about the account reads as an import that verified it.
func (s *Service) checkAccount(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID, req ImportRequest, out *ImportResult,
) error {
	var (
		name   string
		acctNo *string
		isBank bool
	)
	err := tx.QueryRow(ctx, `
		SELECT l.name, l.bank_account_number, is_bank_ledger(l.company_id, l.id)
		  FROM ledger l
		 WHERE l.company_id = $1 AND l.id = $2`, companyID, req.LedgerID).Scan(&name, &acctNo, &isBank)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("bankstatement: ledger %s not found", req.LedgerID)
	}
	if err != nil {
		return fmt.Errorf("bankstatement: load ledger: %w", err)
	}
	if !isBank {
		// Refused by NAME rather than by id: T1's diagnostic-refusal habit.
		return fmt.Errorf("%w: %q is not under a bank group, so a bank statement cannot be imported against it",
			ErrNotABankLedger, name)
	}

	fileAcct := normaliseAccount(req.StatementAccountNumber)
	var ledgerAcct string
	if acctNo != nil {
		ledgerAcct = normaliseAccount(*acctNo)
	}

	switch {
	case fileAcct != "" && ledgerAcct != "" && fileAcct != ledgerAcct:
		return fmt.Errorf(
			"%w: the file says account %s but %q is recorded as %s. "+
				"Importing a statement against the wrong account reconciles nothing and leaves two accounts wrong, "+
				"so nothing has been imported",
			ErrWrongAccount, req.StatementAccountNumber, name, *acctNo)
	case fileAcct != "" && ledgerAcct == "":
		out.Disclosures = append(out.Disclosures, fmt.Sprintf(
			"%q has no account number recorded, so this file could not be checked against it. "+
				"Record the account number and IFSC on the ledger to have future imports verified.", name))
	case fileAcct == "" && ledgerAcct != "":
		out.Disclosures = append(out.Disclosures,
			"The file's own account number was not supplied, so it was not checked against this ledger's.")
	case fileAcct == "" && ledgerAcct == "":
		out.Disclosures = append(out.Disclosures, fmt.Sprintf(
			"Neither this file nor %q carries an account number, so there is nothing to check "+
				"that this statement belongs to this account.", name))
	}
	return nil
}

// normaliseAccount strips the punctuation banks print account numbers with, so
// "50200 0123 45678" and "50200012345678" are one number. It does NOT strip
// leading zeros: they are part of the number at several banks.
func normaliseAccount(s string) string {
	return strings.NewReplacer(" ", "", "-", "", "/", "").Replace(strings.TrimSpace(s))
}

func nullDate(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.Format("2006-01-02")
}

func nullText(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func nullBalance(l Line) any {
	if !l.HasBalance {
		return nil
	}
	return l.RunningBalance
}

// scope mirrors banking.scope and reporting.scope: a fast, friendly check that
// the caller is operating on the company its context is bound to. db.RunInTx's
// GUC guard is the authoritative backstop.
func scope(ctx context.Context, companyID uuid.UUID) error {
	scoped, ok := db.CompanyFromContext(ctx)
	if !ok {
		return db.ErrNoCompany
	}
	if scoped != companyID {
		return fmt.Errorf("bankstatement: company %s not in scope %s", companyID, scoped)
	}
	return nil
}
