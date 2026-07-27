// Package masters owns the accounting and inventory master data — Groups,
// Ledgers, Voucher Types, Stock Items, Units, Godowns and Batches — for a
// company. It is the authoritative writer of the master tables (account_group,
// ledger, voucher_type, stock_item, unit, godown, stock_batch) and resolves
// masters by name for the posting path.
//
// It also writes stock_opening, the opening POSITION of a stock item. That is
// master-setup data (stated as at company.books_beginning_from), not a
// movement: the immutable stock_movement ledger is written only inside
// ledger.post().
//
// Every write goes through the tenant-scoped GUC guard (db.RunInTx), so the
// default-deny RLS policies confine it to the caller's company. Masters are
// mutable (a ledger may be renamed or re-grouped), but each create/alter/delete
// appends an immutable row to master_audit capturing the before/after JSON
// snapshot — the MCA Rule 11(g) edit log for master data, mirroring the
// voucher_version hash chain for transactions.
package masters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/platform/db"
)

// TxRunner is the minimal database capability masters needs: run a unit of work
// inside a tenant-scoped transaction. *db.Pool satisfies it.
type TxRunner interface {
	RunInTx(ctx context.Context, fn db.TxFunc) error
}

// Op mirrors the voucher_op enum for the masters edit log.
type Op string

const (
	// OpCreate records a master creation.
	OpCreate Op = "CREATE"
	// OpAlter records a master modification.
	OpAlter Op = "ALTER"
	// OpDelete records a master deletion.
	OpDelete Op = "DELETE"
)

// ErrNotFound is returned when a named master cannot be resolved in scope.
var ErrNotFound = errors.New("masters: not found")

// Service is the masters application service.
type Service struct {
	runner TxRunner
}

// NewService constructs a masters Service over the given transaction runner.
func NewService(runner TxRunner) *Service {
	return &Service{runner: runner}
}

// --------------------------------------------------------------------------
// Group
// --------------------------------------------------------------------------

// Nature is the Balance-Sheet vs P&L nature inherited by a group's ledgers.
type Nature string

// The four account natures recognised by the engine (matches the
// account_nature enum).
const (
	Asset     Nature = "ASSET"
	Liability Nature = "LIABILITY"
	Income    Nature = "INCOME"
	Expense   Nature = "EXPENSE"
)

// Group is an account group (a node in the chart-of-accounts tree).
type Group struct {
	ID                 uuid.UUID
	Name               string
	ParentName         string // empty for a primary group
	Nature             Nature // required when ParentName is empty; else inherited
	IsPL               bool   // required when ParentName is empty; else inherited
	BehavesAsSubledger bool
	NettBalances       bool
	UsedForCalc        bool
}

// CreateGroup creates a user account group. A primary group (no parent) must
// carry an explicit Nature; a sub-group inherits Nature/IsPL from its parent.
// It appends a master_audit CREATE row.
func (s *Service) CreateGroup(ctx context.Context, g Group, createdBy string) (uuid.UUID, error) {
	if g.Name == "" {
		return uuid.Nil, errors.New("masters: group name is required")
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return uuid.Nil, db.ErrNoCompany
	}
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("masters: new group id: %w", err)
	}

	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var (
			parentID   *uuid.UUID
			parentPath string
			nature     = g.Nature
			isPL       = g.IsPL
		)
		if g.ParentName != "" {
			var (
				pid  uuid.UUID
				pnat Nature
				ppl  bool
			)
			err := tx.QueryRow(ctx,
				`SELECT id, path::text, nature, is_pl FROM account_group WHERE name = $1`,
				g.ParentName).Scan(&pid, &parentPath, &pnat, &ppl)
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("masters: %w: parent group %q", ErrNotFound, g.ParentName)
			}
			if err != nil {
				return fmt.Errorf("masters: resolve parent group: %w", err)
			}
			parentID = &pid
			// A sub-group inherits its parent's nature and P&L flag.
			nature, isPL = pnat, ppl
		} else if nature == "" {
			return errors.New("masters: a primary group requires an explicit nature")
		}

		// Build the ltree path: primary groups get a single "g<id>" label;
		// sub-groups append "s<id>" onto the parent's path. The path is passed
		// as a text parameter and cast to ltree in SQL, so it is fully bound.
		var path string
		if parentID == nil {
			path = "g" + dashless(id)
		} else {
			path = parentPath + ".s" + dashless(id)
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO account_group
			   (id, company_id, name, parent_id, nature, is_pl,
			    behaves_as_subledger, nett_balances, used_for_calc, path)
			 VALUES ($1, $2, $3, $4, $5::account_nature, $6, $7, $8, $9, $10::ltree)`,
			id, companyID, g.Name, parentID, string(nature), isPL,
			g.BehavesAsSubledger, g.NettBalances, g.UsedForCalc, path,
		); err != nil {
			return fmt.Errorf("masters: insert group: %w", err)
		}
		after := map[string]any{
			"name": g.Name, "parent": g.ParentName, "nature": string(nature),
			"is_pl": isPL, "behaves_as_subledger": g.BehavesAsSubledger,
		}
		return appendAudit(ctx, tx, companyID, "group", id, OpCreate, nil, after, createdBy)
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// GroupID resolves an account group's id by name within the company in context.
func (s *Service) GroupID(ctx context.Context, name string) (uuid.UUID, error) {
	return s.resolveID(ctx, "account_group", name)
}

// --------------------------------------------------------------------------
// Ledger
// --------------------------------------------------------------------------

// GSTRegistrationType mirrors the gst_registration_type enum.
type GSTRegistrationType string

// Recognised GST registration types.
const (
	GSTUnregistered GSTRegistrationType = "UNREGISTERED"
	GSTRegular      GSTRegistrationType = "REGULAR"
	GSTComposition  GSTRegistrationType = "COMPOSITION"
	GSTConsumer     GSTRegistrationType = "CONSUMER"
	GSTSEZ          GSTRegistrationType = "SEZ"
	GSTUnknown      GSTRegistrationType = "UNKNOWN"
)

// Ledger is a posting account under a group, with optional GST master fields.
type Ledger struct {
	ID                 uuid.UUID
	Name               string
	GroupName          string
	OpeningBalance     decimal.Decimal
	OpeningDC          string // "D" or "C"
	MaintainBillByBill bool
	GSTIN              string
	StateCode          string
	RegistrationType   GSTRegistrationType
	PAN                string
	CreditDays         *int
	// OpeningBills is the BILL-WISE BREAKUP of OpeningBalance: the individual
	// unpaid bills that make it up, as at company.books_beginning_from. Tally
	// requires this for any bill-wise ledger carrying an opening balance, and
	// requires it to ADD UP — CreateLedger enforces both. Empty is fine for a
	// ledger with no opening balance or with bill-wise tracking off.
	OpeningBills []OpeningBill
}

// OpeningBill is one bill of a ledger's opening bill-wise breakup.
type OpeningBill struct {
	RefName string
	RefType string // NEW (default) / ADVANCE / ON_ACCOUNT
	DrCr    string // "D" or "C"
	Amount  decimal.Decimal
	DueDate string // ISO date, or "" for none
}

// EnsureLedger resolves a ledger by name, creating it under groupName if it does
// not exist. It is the convenience entry point for callers (and other agents)
// that just need a ledger to post to. Returns the ledger id.
func (s *Service) EnsureLedger(ctx context.Context, name, groupName string, createdBy string) (uuid.UUID, error) {
	id, err := s.resolveID(ctx, "ledger", name)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return uuid.Nil, err
	}
	return s.CreateLedger(ctx, Ledger{Name: name, GroupName: groupName}, createdBy)
}

// EnsureLedgerWithOpening resolves a ledger by name; if it already exists it is
// returned UNCHANGED (an opening balance or GST master already set is never
// clobbered — resolve-first). If absent it is created with the supplied opening
// balance and GST master attributes. This is the importer's first-pass entry
// point for LEDGER masters carrying an opening balance and/or GST identity.
func (s *Service) EnsureLedgerWithOpening(ctx context.Context, l Ledger, createdBy string) (uuid.UUID, error) {
	id, err := s.resolveID(ctx, "ledger", l.Name)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return uuid.Nil, err
	}
	return s.CreateLedger(ctx, l, createdBy)
}

// CreateLedger creates a ledger under the named group and appends a
// master_audit CREATE row. GST fields are optional.
func (s *Service) CreateLedger(ctx context.Context, l Ledger, createdBy string) (uuid.UUID, error) {
	if l.Name == "" {
		return uuid.Nil, errors.New("masters: ledger name is required")
	}
	if l.GroupName == "" {
		return uuid.Nil, errors.New("masters: ledger group is required")
	}
	if l.OpeningDC == "" {
		l.OpeningDC = "D"
	}
	if l.OpeningDC != "D" && l.OpeningDC != "C" {
		return uuid.Nil, fmt.Errorf("masters: invalid opening dr/cr %q", l.OpeningDC)
	}
	if l.RegistrationType == "" {
		l.RegistrationType = GSTUnknown
	}
	// A bill-wise opening breakup must ACCOUNT FOR the whole opening balance.
	// Tally refuses a breakup with a difference, and so do we: a breakup that
	// does not add up makes the Outstandings total disagree with the Balance
	// Sheet by exactly the shortfall, silently.
	if len(l.OpeningBills) > 0 {
		sum := decimal.Zero
		for i, b := range l.OpeningBills {
			if b.RefName == "" {
				return uuid.Nil, fmt.Errorf("masters: opening bill %d has no ref_name", i+1)
			}
			if b.Amount.IsNegative() {
				return uuid.Nil, fmt.Errorf("masters: opening bill %q has a negative amount", b.RefName)
			}
			if b.DrCr != "" && b.DrCr != "D" && b.DrCr != "C" {
				return uuid.Nil, fmt.Errorf("masters: opening bill %q has invalid dr/cr %q", b.RefName, b.DrCr)
			}
			sum = sum.Add(b.Amount)
		}
		if !sum.Equal(l.OpeningBalance) {
			return uuid.Nil, fmt.Errorf(
				"masters: opening bill-wise breakup for %q sums to %s but the opening balance is %s",
				l.Name, sum.StringFixed(4), l.OpeningBalance.StringFixed(4))
		}
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return uuid.Nil, db.ErrNoCompany
	}
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("masters: new ledger id: %w", err)
	}

	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var groupID uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id FROM account_group WHERE name = $1`, l.GroupName).Scan(&groupID)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("masters: %w: group %q", ErrNotFound, l.GroupName)
		}
		if err != nil {
			return fmt.Errorf("masters: resolve group: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO ledger
			   (id, company_id, name, group_id, opening_balance, opening_dc,
			    maintain_bill_by_bill, gstin, state_code, registration_type,
			    pan, credit_days)
			 VALUES ($1, $2, $3, $4, $5, $6, $7,
			         NULLIF($8,''), NULLIF($9,''), $10::gst_registration_type,
			         NULLIF($11,''), $12)`,
			id, companyID, l.Name, groupID, l.OpeningBalance, l.OpeningDC,
			l.MaintainBillByBill, l.GSTIN, l.StateCode, string(l.RegistrationType),
			l.PAN, l.CreditDays,
		); err != nil {
			return fmt.Errorf("masters: insert ledger: %w", err)
		}
		for _, b := range l.OpeningBills {
			billID, err := uuid.NewV7()
			if err != nil {
				return fmt.Errorf("masters: new opening bill id: %w", err)
			}
			refType := b.RefType
			if refType == "" {
				refType = "NEW"
			}
			drcr := b.DrCr
			if drcr == "" {
				// Default to the side the opening balance itself sits on.
				drcr = l.OpeningDC
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO ledger_opening_bill
				   (id, company_id, ledger_id, ref_name, ref_type, dr_cr, amount, due_date)
				 VALUES ($1, $2, $3, $4, $5::bill_ref_type, $6, $7, NULLIF($8,'')::date)`,
				billID, companyID, id, b.RefName, refType, drcr, b.Amount, b.DueDate,
			); err != nil {
				return fmt.Errorf("masters: insert opening bill %q: %w", b.RefName, err)
			}
		}
		after := map[string]any{
			"name": l.Name, "group": l.GroupName, "gstin": l.GSTIN,
			"state_code": l.StateCode, "registration_type": string(l.RegistrationType),
			"maintain_bill_by_bill": l.MaintainBillByBill,
		}
		return appendAudit(ctx, tx, companyID, "ledger", id, OpCreate, nil, after, createdBy)
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// SetLedgerOpening sets a ledger's OPENING BALANCE and its bill-wise opening
// breakup, replacing any breakup already recorded. It appends a master_audit
// ALTER row, so the change is traceable exactly like a voucher edit.
//
// Deliberately NARROW. This is not a general master editor: opening balances are
// a books-SETUP operation (they are stated as at company.books_beginning_from),
// and they are the one master field a caller cannot set at creation time for the
// ledgers seed_company_defaults creates for every company — Cash and Profit &
// Loss A/c. Without this, the predefined Cash ledger could never carry an
// opening balance and callers were pushed into inventing a duplicate cash
// ledger just to have one they could set.
//
// The breakup must SUM to the opening balance, same invariant CreateLedger
// enforces; see the comment there.
func (s *Service) SetLedgerOpening(ctx context.Context, ledgerID uuid.UUID, opening decimal.Decimal, openingDC string, bills []OpeningBill, createdBy string) error {
	if openingDC == "" {
		openingDC = "D"
	}
	if openingDC != "D" && openingDC != "C" {
		return fmt.Errorf("masters: invalid opening dr/cr %q", openingDC)
	}
	if len(bills) > 0 {
		sum := decimal.Zero
		for i, b := range bills {
			if b.RefName == "" {
				return fmt.Errorf("masters: opening bill %d has no ref_name", i+1)
			}
			if b.Amount.IsNegative() {
				return fmt.Errorf("masters: opening bill %q has a negative amount", b.RefName)
			}
			sum = sum.Add(b.Amount)
		}
		if !sum.Equal(opening) {
			return fmt.Errorf(
				"masters: opening bill-wise breakup sums to %s but the opening balance is %s",
				sum.StringFixed(4), opening.StringFixed(4))
		}
	}

	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return db.ErrNoCompany
	}

	return s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var (
			name       string
			prevOpen   decimal.Decimal
			prevOpenDC string
		)
		err := tx.QueryRow(ctx,
			`SELECT name, opening_balance, opening_dc FROM ledger WHERE id = $1`,
			ledgerID).Scan(&name, &prevOpen, &prevOpenDC)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("masters: %w: ledger %s", ErrNotFound, ledgerID)
		}
		if err != nil {
			return fmt.Errorf("masters: read ledger: %w", err)
		}

		if _, err := tx.Exec(ctx,
			`UPDATE ledger SET opening_balance = $2, opening_dc = $3 WHERE id = $1`,
			ledgerID, opening, openingDC); err != nil {
			return fmt.Errorf("masters: update opening balance: %w", err)
		}

		// Replace the breakup wholesale: a restated opening is a restatement of
		// its constituent bills, not an addition to them.
		if _, err := tx.Exec(ctx,
			`DELETE FROM ledger_opening_bill WHERE ledger_id = $1`, ledgerID); err != nil {
			return fmt.Errorf("masters: clear opening bills: %w", err)
		}
		for _, b := range bills {
			billID, err := uuid.NewV7()
			if err != nil {
				return fmt.Errorf("masters: new opening bill id: %w", err)
			}
			refType := b.RefType
			if refType == "" {
				refType = "NEW"
			}
			drcr := b.DrCr
			if drcr == "" {
				drcr = openingDC
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO ledger_opening_bill
				   (id, company_id, ledger_id, ref_name, ref_type, dr_cr, amount, due_date)
				 VALUES ($1, $2, $3, $4, $5::bill_ref_type, $6, $7, NULLIF($8,'')::date)`,
				billID, companyID, ledgerID, b.RefName, refType, drcr, b.Amount, b.DueDate,
			); err != nil {
				return fmt.Errorf("masters: insert opening bill %q: %w", b.RefName, err)
			}
		}

		before := map[string]any{
			"opening_balance": prevOpen.StringFixed(4), "opening_dc": prevOpenDC,
		}
		after := map[string]any{
			"name":            name,
			"opening_balance": opening.StringFixed(4), "opening_dc": openingDC,
			"opening_bills": len(bills),
		}
		return appendAudit(ctx, tx, companyID, "ledger", ledgerID, OpAlter, before, after, createdBy)
	})
}

// LedgerID resolves a ledger's id by name within the company in context.
func (s *Service) LedgerID(ctx context.Context, name string) (uuid.UUID, error) {
	return s.resolveID(ctx, "ledger", name)
}

// LedgerGST holds the GST master attributes of a ledger needed by the tax
// engine and GSTR reports.
type LedgerGST struct {
	GSTIN            string
	StateCode        string
	RegistrationType GSTRegistrationType
}

// LedgerGSTInfo returns the GST attributes for a ledger by id. Missing fields
// come back as empty strings / UNKNOWN.
func (s *Service) LedgerGSTInfo(ctx context.Context, ledgerID uuid.UUID) (LedgerGST, error) {
	var out LedgerGST
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var gstin, state, regType *string
		err := tx.QueryRow(ctx,
			`SELECT gstin, state_code, registration_type::text FROM ledger WHERE id = $1`,
			ledgerID).Scan(&gstin, &state, &regType)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("masters: %w: ledger %s", ErrNotFound, ledgerID)
		}
		if err != nil {
			return err
		}
		if gstin != nil {
			out.GSTIN = *gstin
		}
		if state != nil {
			out.StateCode = *state
		}
		out.RegistrationType = GSTUnknown
		if regType != nil {
			out.RegistrationType = GSTRegistrationType(*regType)
		}
		return nil
	})
	if err != nil {
		return LedgerGST{}, err
	}
	return out, nil
}

// --------------------------------------------------------------------------
// VoucherType
// --------------------------------------------------------------------------

// VoucherType is an accounting/inventory voucher type (e.g. Sales, Payment).
type VoucherType struct {
	ID              uuid.UUID
	Name            string
	ParentClass     string // SALES, PURCHASE, CONTRA, PAYMENT, RECEIPT, JOURNAL...
	NumberingMethod string
	AffectsStock    bool
	IsAccounting    bool
}

// CreateVoucherType creates a voucher type and appends a master_audit row.
func (s *Service) CreateVoucherType(ctx context.Context, vt VoucherType, createdBy string) (uuid.UUID, error) {
	if vt.Name == "" || vt.ParentClass == "" {
		return uuid.Nil, errors.New("masters: voucher type name and parent class are required")
	}
	if vt.NumberingMethod == "" {
		vt.NumberingMethod = "AUTOMATIC"
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return uuid.Nil, db.ErrNoCompany
	}
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("masters: new voucher type id: %w", err)
	}
	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO voucher_type
			   (id, company_id, name, parent_class, numbering_method,
			    affects_stock, is_accounting)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			id, companyID, vt.Name, vt.ParentClass, vt.NumberingMethod,
			vt.AffectsStock, vt.IsAccounting,
		); err != nil {
			return fmt.Errorf("masters: insert voucher type: %w", err)
		}
		after := map[string]any{"name": vt.Name, "parent_class": vt.ParentClass}
		return appendAudit(ctx, tx, companyID, "voucher_type", id, OpCreate, nil, after, createdBy)
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// VoucherTypeID resolves a voucher type's id by name within scope.
func (s *Service) VoucherTypeID(ctx context.Context, name string) (uuid.UUID, error) {
	return s.resolveID(ctx, "voucher_type", name)
}

// --------------------------------------------------------------------------
// Unit
// --------------------------------------------------------------------------

// Unit is a unit of measure for stock items.
type Unit struct {
	ID            uuid.UUID
	Symbol        string
	FormalName    string
	DecimalPlaces int
}

// CreateUnit creates a unit of measure and appends a master_audit row.
func (s *Service) CreateUnit(ctx context.Context, u Unit, createdBy string) (uuid.UUID, error) {
	if u.Symbol == "" {
		return uuid.Nil, errors.New("masters: unit symbol is required")
	}
	if u.DecimalPlaces < 0 {
		return uuid.Nil, errors.New("masters: unit decimal places must be non-negative")
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return uuid.Nil, db.ErrNoCompany
	}
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("masters: new unit id: %w", err)
	}
	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO unit (id, company_id, symbol, formal_name, decimal_places)
			 VALUES ($1, $2, $3, $4, $5)`,
			id, companyID, u.Symbol, u.FormalName, u.DecimalPlaces,
		); err != nil {
			return fmt.Errorf("masters: insert unit: %w", err)
		}
		after := map[string]any{"symbol": u.Symbol, "formal_name": u.FormalName}
		return appendAudit(ctx, tx, companyID, "unit", id, OpCreate, nil, after, createdBy)
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// UnitID resolves a unit's id by symbol within scope.
func (s *Service) UnitID(ctx context.Context, symbol string) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT id FROM unit WHERE symbol = $1`, symbol).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("masters: %w: unit %q", ErrNotFound, symbol)
		}
		return err
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// --------------------------------------------------------------------------
// StockItem
// --------------------------------------------------------------------------

// StockItem is an inventory master item.
type StockItem struct {
	ID         uuid.UUID
	Name       string
	UnitSymbol string // resolved to unit_id; optional
	HSN        string
	GSTRatePct decimal.Decimal
	// OpeningQty/OpeningRate are the item's OPENING POSITION — the stock
	// already on hand as at company.books_beginning_from. They are NOT columns
	// on the item: since migration 0025 they are written as a stock_opening
	// row at the company's system 'Main Location' with no batch, which is the
	// only position the retired dimensionless stock_item.opening_qty /
	// opening_rate pair could ever have meant.
	//
	// A position is (item, godown, batch, qty, rate); "40 units" on its own is
	// not a fact. Keeping the pair on the master would leave two sources of
	// truth for the same position, and the easier one to write to — the column
	// — is the one that cannot express where the stock is.
	OpeningQty  decimal.Decimal
	OpeningRate decimal.Decimal
}

// CreateStockItem creates a stock item and appends a master_audit row. If
// UnitSymbol is set it is resolved to a unit id (must already exist). A
// non-zero opening position is written as a stock_opening row in the SAME
// transaction — an item whose insert succeeded but whose opening was lost
// would misstate the closing stock of every later report, silently.
func (s *Service) CreateStockItem(ctx context.Context, si StockItem, createdBy string) (uuid.UUID, error) {
	if si.Name == "" {
		return uuid.Nil, errors.New("masters: stock item name is required")
	}
	// stock_opening.rate carries CHECK (rate >= 0); catch it here so the user
	// is told which field is wrong rather than reading a constraint name.
	if si.OpeningRate.IsNegative() {
		return uuid.Nil, fmt.Errorf("masters: stock item %q has a negative opening rate %s",
			si.Name, si.OpeningRate.StringFixed(4))
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return uuid.Nil, db.ErrNoCompany
	}
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("masters: new stock item id: %w", err)
	}
	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var unitID *uuid.UUID
		if si.UnitSymbol != "" {
			var uid uuid.UUID
			err := tx.QueryRow(ctx, `SELECT id FROM unit WHERE symbol = $1`, si.UnitSymbol).Scan(&uid)
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("masters: %w: unit %q", ErrNotFound, si.UnitSymbol)
			}
			if err != nil {
				return fmt.Errorf("masters: resolve unit: %w", err)
			}
			unitID = &uid
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO stock_item
			   (id, company_id, name, unit_id, hsn, gst_rate_pct)
			 VALUES ($1, $2, $3, $4, NULLIF($5,''), $6)`,
			id, companyID, si.Name, unitID, si.HSN, si.GSTRatePct,
		); err != nil {
			return fmt.Errorf("masters: insert stock item: %w", err)
		}
		after := map[string]any{
			"name": si.Name, "unit": si.UnitSymbol, "hsn": si.HSN,
			"gst_rate_pct": si.GSTRatePct.String(),
		}
		// An opening position is only recorded when there IS one. A zero row
		// would claim a position exists at Main Location for every item ever
		// created, which is both untrue and a needless dependency on the
		// location for tenants that only keep accounts.
		if !si.OpeningQty.IsZero() || !si.OpeningRate.IsZero() {
			godownID, err := mainLocationID(ctx, tx, companyID)
			if err != nil {
				return err
			}
			openingID, err := uuid.NewV7()
			if err != nil {
				return fmt.Errorf("masters: new stock opening id: %w", err)
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO stock_opening
				   (id, company_id, stock_item_id, godown_id, batch_id, qty, rate)
				 VALUES ($1, $2, $3, $4, NULL, $5, $6)`,
				openingID, companyID, id, godownID, si.OpeningQty, si.OpeningRate,
			); err != nil {
				return fmt.Errorf("masters: insert stock opening: %w", err)
			}
			after["opening_qty"] = si.OpeningQty.StringFixed(4)
			after["opening_rate"] = si.OpeningRate.StringFixed(4)
			after["opening_godown"] = MainLocationName
		}
		return appendAudit(ctx, tx, companyID, "stock_item", id, OpCreate, nil, after, createdBy)
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// StockItemID resolves a stock item's id by name within scope.
func (s *Service) StockItemID(ctx context.Context, name string) (uuid.UUID, error) {
	return s.resolveID(ctx, "stock_item", name)
}

// --------------------------------------------------------------------------
// internal helpers
// --------------------------------------------------------------------------

// resolveID is a generic name->id lookup for masters with a (company_id, name)
// uniqueness. It returns ErrNotFound when absent.
func (s *Service) resolveID(ctx context.Context, table, name string) (uuid.UUID, error) {
	var id uuid.UUID
	// table is a fixed internal literal (never user input), so the format is safe.
	sql := fmt.Sprintf(`SELECT id FROM %s WHERE name = $1`, table)
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, sql, name).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("masters: %w: %s %q", ErrNotFound, table, name)
		}
		return err
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// appendAudit writes one immutable master_audit row inside the caller's tx.
func appendAudit(ctx context.Context, tx pgx.Tx, companyID uuid.UUID, entityType string,
	entityID uuid.UUID, op Op, before, after map[string]any, createdBy string) error {
	if createdBy == "" {
		createdBy = "system"
	}
	auditID, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("masters: new audit id: %w", err)
	}
	var beforeJSON, afterJSON []byte
	if before != nil {
		if beforeJSON, err = json.Marshal(before); err != nil {
			return fmt.Errorf("masters: marshal before: %w", err)
		}
	}
	if after != nil {
		if afterJSON, err = json.Marshal(after); err != nil {
			return fmt.Errorf("masters: marshal after: %w", err)
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO master_audit
		   (id, company_id, entity_type, entity_id, op, before_doc, after_doc, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		auditID, companyID, entityType, entityID, string(op), beforeJSON, afterJSON, createdBy,
	); err != nil {
		return fmt.Errorf("masters: insert master_audit: %w", err)
	}
	return nil
}

// dashless returns a UUID's hex without dashes, for use as an ltree label
// (ltree labels may not contain '-').
func dashless(id uuid.UUID) string {
	s := id.String()
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '-' {
			out = append(out, s[i])
		}
	}
	return string(out)
}
