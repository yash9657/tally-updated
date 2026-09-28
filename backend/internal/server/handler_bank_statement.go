package server

// HTTP surface for T4 slice 2a — bank statement import.
//
// Design memo: docs/design/t4-slice2-model.md §8.
//
// THE SHAPE IS THE PRODUCT DECISION. Preview-then-import exists because this
// build ships NO per-bank column layouts: the mapping is proposed from the
// file's own header and CONFIRMED by a human. A single-call import would have
// to guess, and a guessed mapping does not fail — it produces real money on
// real dates that was never a transaction.

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/bankstatement"
)

// columnMappingReq is the wire shape of a confirmed mapping. It mirrors
// bankstatement.ColumnMapping exactly rather than reusing it, so the wire
// contract cannot silently change when the engine's struct does — the same
// reason every other handler here declares its own request type.
type columnMappingReq struct {
	Encoding   string `json:"encoding"`
	DateLayout string `json:"date_layout"`

	ValueDate   string `json:"value_date"`
	TxnDate     string `json:"txn_date"`
	Description string `json:"description"`

	Deposit    string `json:"deposit"`
	Withdrawal string `json:"withdrawal"`
	Amount     string `json:"amount"`
	Direction  string `json:"direction"`

	RunningBalance string `json:"running_balance"`
	BankReference  string `json:"bank_reference"`
	ChequeNumber   string `json:"cheque_number"`
	// BalanceSign is the Cr/Dr column carrying the running balance's SIGN
	// (W5; Kotak's second "Dr / Cr"). omitempty so every client written before
	// it sends and receives exactly the bytes it always did.
	BalanceSign string `json:"balance_sign,omitempty"`
}

func (m columnMappingReq) toDomain() bankstatement.ColumnMapping {
	return bankstatement.ColumnMapping{
		Encoding:       bankstatement.DirectionEncoding(m.Encoding),
		DateLayout:     bankstatement.DateLayout(m.DateLayout),
		ValueDate:      m.ValueDate,
		TxnDate:        m.TxnDate,
		Description:    m.Description,
		Deposit:        m.Deposit,
		Withdrawal:     m.Withdrawal,
		Amount:         m.Amount,
		Direction:      m.Direction,
		RunningBalance: m.RunningBalance,
		BankReference:  m.BankReference,
		ChequeNumber:   m.ChequeNumber,
		BalanceSign:    m.BalanceSign,
	}
}

func viewColumnMapping(m bankstatement.ColumnMapping) columnMappingReq {
	return columnMappingReq{
		Encoding:       string(m.Encoding),
		DateLayout:     string(m.DateLayout),
		ValueDate:      m.ValueDate,
		TxnDate:        m.TxnDate,
		Description:    m.Description,
		Deposit:        m.Deposit,
		Withdrawal:     m.Withdrawal,
		Amount:         m.Amount,
		Direction:      m.Direction,
		RunningBalance: m.RunningBalance,
		BankReference:  m.BankReference,
		ChequeNumber:   m.ChequeNumber,
		BalanceSign:    m.BalanceSign,
	}
}

// previewBankStatementReq is the body of POST .../preview.
type previewBankStatementReq struct {
	FileName string `json:"file_name"`
	// Content is the file, verbatim. It is a JSON string rather than a
	// multipart upload because every other route on this API is JSON and a
	// bank CSV is small; if XLSX ever ships that decision is revisited with it.
	Content string `json:"content"`
}

// importBankStatementReq is the body of POST .../import.
type importBankStatementReq struct {
	FileName string           `json:"file_name"`
	Content  string           `json:"content"`
	Mapping  columnMappingReq `json:"mapping"`
	// StatementAccountNumber is what the FILE says the account is, if the
	// operator read it off the statement. When the ledger has one recorded and
	// the two disagree, the import is refused — see the service.
	StatementAccountNumber string `json:"statement_account_number"`
	CreatedBy              string `json:"created_by"`
	// The opening and closing balance PRINTED ON THE STATEMENT, keyed by the
	// operator (W5). Decimal STRINGS, never JSON numbers: a float64 on the wire
	// is how a paisa is lost before the proof ever sees it. Empty = not keyed.
	DeclaredOpeningBalance string `json:"declared_opening_balance"`
	DeclaredClosingBalance string `json:"declared_closing_balance"`
}

// declaredBalance parses one keyed balance, "" meaning "not keyed".
func declaredBalance(raw, which string) (*decimal.Decimal, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	d, err := decimal.NewFromString(strings.ReplaceAll(raw, ",", ""))
	if err != nil {
		return nil, fmt.Errorf("declared_%s_balance must be a decimal number such as 150000.00, got %q", which, raw)
	}
	return &d, nil
}

// handlePreviewBankStatement proposes a mapping. It writes nothing.
func (s *Server) handlePreviewBankStatement(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.scopeCtxID(w, r); !ok {
		return
	}
	if _, err := uuid.Parse(chi.URLParam(r, "ledgerID")); err != nil {
		writeError(w, http.StatusBadRequest, "invalid ledgerID")
		return
	}
	var req previewBankStatementReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Content == "" {
		writeError(w, http.StatusBadRequest, "content is required — there is no file to read")
		return
	}

	p, err := bankstatement.ProposeMapping(req.Content)
	if err != nil {
		// Every refusal here is about the FILE and names what is wrong with it.
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	layouts := make([]string, 0, len(bankstatement.KnownDateLayouts()))
	for _, l := range bankstatement.KnownDateLayouts() {
		layouts = append(layouts, string(l))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"file_name": req.FileName,
		"header":    p.Header,
		"mapping":   viewColumnMapping(p.Mapping),
		// UNRESOLVED IS THE POINT OF THIS ROUTE. A client must show it and must
		// not pre-confirm past it: each entry is a question only the operator
		// can answer, and the commonest — an ambiguous dd/mm vs mm/dd date —
		// silently moves a whole statement by up to eleven months.
		"unresolved": nonNilStrings(p.Unresolved),
		"notes":      nonNilStrings(p.Notes),
		// W5: the bank whose header signature matched IN FULL ("" if none),
		// whether that signature has been checked against a real export (never,
		// yet — the notes say so), and the delimiter the file was read with.
		"bank":          p.Bank,
		"bank_verified": p.BankVerified,
		"delimiter":     p.Delimiter,
		// The pickers the client needs, from the engine rather than duplicated
		// in the client, so a layout added there appears here without a second
		// edit.
		"known_date_layouts": layouts,
		"known_encodings": []string{
			string(bankstatement.TwoColumns),
			string(bankstatement.SignedAmount),
			string(bankstatement.AmountAndFlag),
		},
	})
}

// handleImportBankStatement persists a file under a confirmed mapping.
func (s *Server) handleImportBankStatement(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	ledgerID, err := uuid.Parse(chi.URLParam(r, "ledgerID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid ledgerID")
		return
	}
	var req importBankStatementReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Content == "" {
		writeError(w, http.StatusBadRequest, "content is required — there is no file to import")
		return
	}

	opening, err := declaredBalance(req.DeclaredOpeningBalance, "opening")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	closing, err := declaredBalance(req.DeclaredClosingBalance, "closing")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	res, err := s.bankstmt.Import(ctx, companyID, bankstatement.ImportRequest{
		LedgerID:               ledgerID,
		FileName:               req.FileName,
		Content:                req.Content,
		Mapping:                req.Mapping.toDomain(),
		By:                     req.CreatedBy,
		StatementAccountNumber: req.StatementAccountNumber,
		Declared:               bankstatement.Declared{Opening: opening, Closing: closing},
	})
	if err != nil {
		// EVERY REFUSAL ON THIS ROUTE IS THE CALLER'S ERROR AND EVERY ONE NAMES
		// ITS REASON — the file, the column, the row, the account — so the
		// message is passed through rather than flattened to "import failed".
		//
		// ⚠️ THEY ALL ANSWER 400, WHICH IS WHY A TEST MUST ASSERT THE REASON
		// AND NOT THE STATUS. HANDOFF-SESSION-ITEM-ZERO.md §4.2 records two
		// tests that were green while sending an unrelated malformed body,
		// because every domain refusal on that route shared one code.
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"import_id": res.ImportID.String(),
		// A repeat of the same file is a SUCCESS with this flag set, not an
		// error: the operator's intent is satisfied either way and making a
		// double-click fail is friction with no safety gain.
		"already_imported": res.AlreadyImported,
		// THE NAME IT WAS STORED UNDER (D111). Echoed because it is not always
		// the name the caller sent: a statement PASTED rather than chosen
		// arrives with no name at all, and the server derives one from the
		// file's own period, row count and fingerprint. The disclosures on the
		// combined import report quote this string, so a client that cannot see
		// it cannot tell an operator which of two pastes a warning is about.
		"file_name":       res.FileName,
		"lines_in_file":   res.LinesInFile,
		"new_lines":       res.NewLines,
		"duplicate_lines": res.DuplicateLines,
		"opening_balance": res.OpeningBalance,
		"closing_balance": res.ClosingBalance,
		// FALSE MEANS THE DROPPED-ROW CHECK NEVER RAN. A client must not render
		// this the same way as a check that passed.
		"balance_walked": res.BalanceWalked,
		// W5: WHICH PROOF HELD, and every comparison it made. Never empty on a
		// 200 — a statement that cannot be proved is refused, not imported.
		"balance_proof":       string(res.BalanceProof),
		"balance_declared_by": res.BalanceDeclaredBy,
		"row_order":           string(res.RowOrder),
		"proof_checks":        nonNilStrings(res.ProofChecks),
		"period_from":         res.PeriodFrom,
		"period_to":           res.PeriodTo,
		"disclosures":         nonNilStrings(res.Disclosures),
	})
}

// handleImportedStatement reads back what was imported for one bank account.
func (s *Server) handleImportedStatement(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	ledgerID, err := uuid.Parse(chi.URLParam(r, "ledgerID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid ledgerID")
		return
	}

	st, err := s.report.ImportedStatementReport(ctx, companyID, ledgerID,
		r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	imports := make([]map[string]any, 0, len(st.Imports))
	for _, im := range st.Imports {
		imports = append(imports, map[string]any{
			"import_id":      im.ID.String(),
			"file_name":      im.FileName,
			"imported_by":    im.ImportedBy,
			"imported_at":    im.ImportedAt,
			"period_from":    im.PeriodFrom,
			"period_to":      im.PeriodTo,
			"line_count":     im.LineCount,
			"balance_walked": im.BalanceWalked,
			// "" for an import written before W5 (migration 0101).
			"balance_proof": im.BalanceProof,
		})
	}

	lines := make([]map[string]any, 0, len(st.Lines))
	for _, l := range st.Lines {
		row := map[string]any{
			"id":         l.ID.String(),
			"import_id":  l.ImportID.String(),
			"line_no":    l.LineNo,
			"value_date": l.ValueDate,
			// "" when the file carried one date. NOT defaulted to the value
			// date: the point of storing both is that they can differ.
			"txn_date":    l.TxnDate,
			"description": l.Description,
			// Money is a fixed-4 decimal STRING, like every money value on this
			// API — never a float.
			"amount": l.Amount.StringFixed(4),
			// Direction in OUR books: "D" is money IN to the bank account,
			// which INVERTS the word the statement itself uses.
			"dr_cr":          l.DrCr,
			"bank_reference": l.BankReference,
			"cheque_number":  l.ChequeNumber,
		}
		// Present only when the file carried one, so "" is distinguishable from
		// a balance of zero.
		if l.HasBalance {
			row["running_balance"] = l.RunningBalance.StringFixed(4)
		}
		lines = append(lines, row)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ledger_id":   st.LedgerID.String(),
		"ledger_name": st.LedgerName,
		// "" means NOT RECORDED, and a client must render those words rather
		// than an empty field — invariant I-11.
		"bank_account_number": st.BankAccountNumber,
		"bank_ifsc":           st.BankIFSC,
		"from":                st.From,
		"to":                  st.To,
		"imports":             imports,
		"lines":               lines,
		"total_in":            st.TotalIn.StringFixed(4),
		"total_out":           st.TotalOut.StringFixed(4),
		// THE DISCLOSURES ARE PART OF THE REPORT. The first one always says
		// that matching is not built, because a screen of bank rows with no
		// status column otherwise reads as "none of these matched".
		"disclosures": nonNilStrings(st.Disclosures),
	})
}

// nonNilStrings renders a nil slice as [] rather than null. A client that
// treats null as "no disclosures" and [] as "no disclosures" is fine; one that
// crashes on null is not, and the difference is invisible until it happens.
func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
