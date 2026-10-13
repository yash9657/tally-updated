package server

// GET /v1/companies/{companyID}/ledgers — read the chart of accounts.
//
// WHY THIS EXISTS: POST /ledgers could CREATE a ledger and returned its id, but
// nothing could ever enumerate them. A client that did not create a ledger
// itself had no way to resolve its id — including the two ledgers EVERY company
// is seeded with by seed_company_defaults (Cash, Profit & Loss A/c). The
// practical consequence was that callers invented duplicates: the demo seed
// created a second cash ledger ("Cash A/c") purely because it could not address
// the predefined "Cash", leaving a permanently-zero dead row on the Trial
// Balance.
//
// It is a Display over "master" (see routeAction), not a report: this is the
// master list, and modelling it as report:NAME would let a security level that
// disallows one report accidentally hide the chart of accounts.
//
// SAFETY: runs inside RunInReadOnlyTx (BEGIN ... READ ONLY with
// app.current_company_id bound as the first statement). `ledger` is FORCE RLS,
// so rows are tenant-scoped by the database, not by the query text.

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/reporting"
)

// ledgerListItem is one row of the chart of accounts. Field names mirror
// ledgerReq (the POST body) so a client can read a ledger and re-send it.
type ledgerListItem struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	GroupName string `json:"group_name"`
	// OpeningBalance is a fixed-4 decimal STRING, like every other money value
	// on the wire. Never a float.
	OpeningBalance     string `json:"opening_balance"`
	OpeningDC          string `json:"opening_dc"`
	MaintainBillByBill bool   `json:"maintain_bill_by_bill"`
	GSTIN              string `json:"gstin"`
	StateCode          string `json:"state_code"`
	RegistrationType   string `json:"registration_type"`
	// PAN and CreditDays complete the read side of this struct's own contract —
	// "a client can read a ledger and re-send it". `ledgerReq` has accepted
	// `pan` since it was written and now accepts `credit_days`, and NEITHER was
	// readable back, so the contract was false in the direction that matters:
	// a party's PAN could be written and never seen again through HTTP, which
	// is how D87 could report `(null)` for fields that were in fact stored.
	//
	// ⛔ CreditDays IS A POINTER and stays one. NULL means "no term agreed",
	// which is genuinely different from 0 ("due immediately") — collapsing them
	// would make every party that never stated a term look like a cash party in
	// an ageing report.
	PAN        string `json:"pan"`
	CreditDays *int   `json:"credit_days"`
	// Address / City / Pincode are the party's POSTAL BLOCK (migration 0039),
	// echoed here because ledgerReq accepts them and this struct's contract is
	// that a client can read a ledger and re-send it. Without them that
	// contract was false in the one direction that matters: the seeder wrote an
	// address, Rule 46(d) printed it, and no HTTP caller could ever see it.
	//
	// `address` is a NEWLINE-SEPARATED BLOCK, not a single line — same shape as
	// the company profile's. Absent reads back as "" (the column is nullable),
	// which is what a walk-in cash party legitimately has; blank here is NOT a
	// licence to print a blank on a tax invoice — printing.ValidateRule46 is
	// still the gate that refuses that.
	Address string `json:"address"`
	City    string `json:"city"`
	Pincode string `json:"pincode"`
	// BankAccountNumber and BankIFSC are the company's OWN details for a bank
	// ledger (migration 0064), echoed for the same round-trip reason the address
	// block above is: ledgerReq accepts them, so a client must be able to read
	// them back.
	//
	// "" MEANS NOT RECORDED, and a client must render those WORDS. A blank
	// labelled field reads as "this bank has no IFSC" — invariant I-11, which is
	// why these columns were required to ship with data rather than alone.
	//
	// This is also how the reconciliation screen's import panel knows whether a
	// file can be checked against the account at all: with no number recorded
	// there is nothing to compare, and the import says so rather than implying
	// it verified something.
	BankAccountNumber string `json:"bank_account_number"`
	BankIFSC          string `json:"bank_ifsc"`
	// IsSystem marks the ledgers seeded with the company, which a client should
	// prefer to reuse rather than duplicate.
	IsSystem bool `json:"is_system"`
	// IsBank is true when this ledger is a BANK ACCOUNT, i.e. one a Bank
	// Reconciliation Statement can be drawn for. DERIVED, never stored.
	//
	// It is here because a client CANNOT WORK IT OUT FROM GroupName. Bank-ness is
	// ltree ancestry under 'Bank Accounts' / 'Bank OD A/c'
	// (reporting.BankLedgerPredicate), while GroupName is the IMMEDIATE group —
	// so a ledger in a user-created "Current Accounts" sub-group reads
	// GroupName = "Current Accounts", and a client filtering on the two seeded
	// names silently drops a real bank account from its reconciliation picker.
	// Silently is the operative word: a shorter list is not an error, and the
	// operator concludes the product does not know about their account.
	//
	// Note that an OD account is a bank account too, and a LIABILITY — see
	// docs/design/t4-model.md §3.1 on the sign trap. This flag says "a statement
	// can be drawn", not "this is an asset".
	IsBank bool `json:"is_bank"`
	// Blocked is migration 0095's born-blocked state: a ledger the AGENT
	// created, which no voucher may post to until a person unblocks it
	// (POST /ledgers/{id}/unblock). BlockedReason says why, in words a picker
	// can show beside the name. A client that offers a blocked ledger in a
	// voucher's picker will have the voucher refused; it should say so first.
	Blocked       bool   `json:"blocked"`
	BlockedReason string `json:"blocked_reason,omitempty"`
}

type ledgerListResp struct {
	Ledgers []ledgerListItem `json:"ledgers"`
}

// companyGSTIN reads the filing company's own GSTIN. It is the default filer
// identity for GSTR-1/GSTR-3B: the returns are filed BY this company, so the
// gstin belongs to the company master rather than to a request parameter.
// Returns "" when the company has none recorded (a draft/preview return).
func (s *Server) companyGSTIN(ctx context.Context) (string, error) {
	var gstin string
	err := s.pool.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT gstin FROM company`).Scan(&gstin)
	})
	if err != nil {
		return "", err
	}
	return gstin, nil
}

func (s *Server) handleListLedgers(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}

	out := []ledgerListItem{}
	err := s.pool.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT l.id, l.name, g.name,
			        l.opening_balance, l.opening_dc, l.maintain_bill_by_bill,
			        COALESCE(l.gstin, ''), COALESCE(l.state_code, ''),
			        l.registration_type::text,
			        COALESCE(l.pan, ''), l.credit_days,
			        COALESCE(l.address, ''), COALESCE(l.city, ''), COALESCE(l.pincode, ''),
			        COALESCE(l.bank_account_number, ''), COALESCE(l.bank_ifsc, ''),
			        l.is_system,
			        `+reporting.BankLedgerPredicate+`,
			        l.blocked_at IS NOT NULL, COALESCE(l.blocked_reason, '')
			   FROM ledger l
			   JOIN account_group g ON g.id = l.group_id
			  ORDER BY g.name, l.name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				it      ledgerListItem
				opening decimal.Decimal
			)
			if err := rows.Scan(&it.ID, &it.Name, &it.GroupName,
				&opening, &it.OpeningDC, &it.MaintainBillByBill,
				&it.GSTIN, &it.StateCode, &it.RegistrationType,
				&it.PAN, &it.CreditDays,
				&it.Address, &it.City, &it.Pincode,
				&it.BankAccountNumber, &it.BankIFSC, &it.IsSystem,
				&it.IsBank, &it.Blocked, &it.BlockedReason); err != nil {
				return err
			}
			it.OpeningBalance = opening.StringFixed(4)
			out = append(out, it)
		}
		return rows.Err()
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list ledgers: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, ledgerListResp{Ledgers: out})
}

// --------------------------------------------------------------------------
// PUT /v1/companies/{companyID}/ledgers/{ledgerID}/opening
// --------------------------------------------------------------------------

// setOpeningReq is the body of the opening-balance setter.
type setOpeningReq struct {
	OpeningBalance string           `json:"opening_balance"`
	OpeningDC      string           `json:"opening_dc"`
	OpeningBills   []openingBillReq `json:"opening_bills"`
	CreatedBy      string           `json:"created_by"`
}

// handleSetLedgerOpening sets a ledger's opening balance and its bill-wise
// opening breakup. See masters.SetLedgerOpening for why this is deliberately
// narrow rather than a general master editor.
func (s *Server) handleSetLedgerOpening(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	ledgerID, err := uuid.Parse(chi.URLParam(r, "ledgerID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid ledger id")
		return
	}
	var req setOpeningReq
	if !decodeJSON(w, r, &req) {
		return
	}
	opening, err := optionalDecimal(req.OpeningBalance)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid opening_balance")
		return
	}
	bills, ok := openingBillsFromReq(w, req.OpeningBills)
	if !ok {
		return
	}

	if err := s.masters.SetLedgerOpening(ctx, ledgerID, opening, req.OpeningDC, bills, req.CreatedBy); err != nil {
		writeMastersError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ledger_id": ledgerID.String()})
}
