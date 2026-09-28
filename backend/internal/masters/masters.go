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
	"regexp"
	"strings"

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
			return nameTaken(err, "account_group_company_id_name_key", "group", g.Name, "insert group")
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

// GroupIDByName resolves an account group case- and whitespace-insensitively,
// returning its id AND the name as actually stored. The stored name is the half
// callers need: every other lookup on this table matches EXACTLY, so a caller
// holding "Cash-in-hand" must learn that the company's group is spelled
// "Cash-in-Hand" before it can create a ledger under it.
//
// ⛔ IT EXISTS BECAUSE A NEAR-MISS HERE CREATES A SECOND GROUP RATHER THAN
// FAILING. account_group is unique on (company_id, name) and — unlike ledger,
// stock_item and unit since migration 0075 — has NO normalised unique index. So
// "Cash-in-hand" INSERTs happily alongside the seeded "Cash-in-Hand", and the
// company silently acquires two groups that a human reads as one, with its
// ledgers split across them and the Balance Sheet listing both. Tally's own
// export spells two of its built-in groups with a lowercase h, so an importer
// that resolves exactly would do this on the very first real file.
func (s *Service) GroupIDByName(ctx context.Context, name string) (uuid.UUID, string, error) {
	var (
		id     uuid.UUID
		stored string
	)
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`SELECT id, name FROM account_group WHERE lower(btrim(name)) = lower(btrim($1))`,
			name).Scan(&id, &stored)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("masters: %w: account_group %q", ErrNotFound, name)
		}
		return err
	})
	if err != nil {
		return uuid.Nil, "", err
	}
	return id, stored, nil
}

// EnsureGroup resolves an account group by name (case-insensitively, per
// GroupIDByName) and creates it if absent, returning the id and the name as
// stored. An already-present group is returned UNCHANGED — its parent and nature
// are never restated, so re-importing a chart of accounts cannot reshape one.
//
// g.ParentName must already name an existing group; pass the STORED spelling
// (this method's own second return value for the parent) because CreateGroup
// resolves the parent exactly.
func (s *Service) EnsureGroup(ctx context.Context, g Group, createdBy string) (uuid.UUID, string, error) {
	id, stored, err := s.GroupIDByName(ctx, g.Name)
	if err == nil {
		return id, stored, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return uuid.Nil, "", err
	}
	id, err = s.CreateGroup(ctx, g, createdBy)
	if err != nil {
		return uuid.Nil, "", err
	}
	return id, g.Name, nil
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

// ifscRE mirrors the ledger_bank_ifsc_shape CHECK constraint exactly. Kept
// beside the validation that uses it rather than in the schema alone, because a
// constraint an operator can trip must have a SENTENCE, and a constraint has
// none (register row D93).
var ifscRE = regexp.MustCompile(`^[A-Z]{4}0[A-Z0-9]{6}$`)

// valid reports whether this is a registration type the enum will accept.
//
// ⛔ GSTUnknown IS VALID HERE and is deliberately still not offerable from the
// ledger FORM. They are different questions: the column's default is UNKNOWN, so
// a body that omits the field must keep working, while an operator choosing
// "I did not say" is not a statement about a party (see D87 and
// frontend/src/screens/masters/gst.ts).
func (t GSTRegistrationType) valid() bool {
	switch t {
	case GSTUnregistered, GSTRegular, GSTComposition, GSTConsumer, GSTSEZ, GSTUnknown:
		return true
	}
	return false
}

// knownRegistrationTypes lists the enum for a refusal message. A caller told
// only "that is invalid" has to go and read the schema.
func knownRegistrationTypes() []string {
	return []string{
		string(GSTUnregistered), string(GSTRegular), string(GSTComposition),
		string(GSTConsumer), string(GSTSEZ), string(GSTUnknown),
	}
}

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
	// CreditLimit is the party's credit limit (migration 0103). nil = no
	// limit agreed, written NULL; a zero is the real instruction "no credit"
	// and is kept as 0. See 0103's header for why the two must not collapse.
	CreditLimit *decimal.Decimal

	// Address is the party's POSTAL ADDRESS as a NEWLINE-SEPARATED BLOCK, and
	// City / PIN complete it. Migration 0039 stores all three exactly as
	// migration 0015 stores the company's own, so internal/printing/printingdb
	// splits the recipient block with the same splitAddress helper it uses on
	// the supplier block.
	//
	// THIS IS WHAT MAKES A TAX INVOICE PRINTABLE. Rule 46(d) of the CGST Rules
	// 2017 makes the recipient's address a mandatory particular, so
	// printing.ValidateRule46 refuses a document whose recipient address is
	// blank. Before these fields existed there was nowhere to keep a party's
	// address, so that refusal fired on every sale in every company.
	//
	// Empty is still LEGAL and is written as NULL: a cash sale to an
	// unregistered walk-in has no address on file, and Rule 46(e) demands one
	// only for an unregistered recipient on a supply of fifty thousand rupees or
	// more. That threshold belongs to the document, not to the master, so
	// CreateLedger accepts a party with no address and only printing refuses.
	Address string
	City    string
	PIN     string // column `pincode`, matching printing.Party.PIN

	// BankAccountNumber and BankIFSC are the company's OWN account details for
	// a BANK ledger (migration 0064). Empty means NOT RECORDED and is written
	// as NULL — never as '', which the column's CHECK refuses, because a blank
	// string would be a second spelling of the same state and readers would
	// split on which one means what.
	//
	// THEY ARE HOW A STATEMENT IMPORT KNOWS WHICH LEDGER A FILE BELONGS TO
	// (docs/design/t4-slice2-model.md §1.3, invariant I-11). An operator
	// importing three banks' statements in a morning must not have to remember
	// which is which, and a file imported against the wrong ledger reconciles
	// nothing and corrupts two accounts at once — silently, because every line
	// in it is real and both accounts still balance.
	//
	// Nothing stops them being set on a non-bank ledger and nothing needs to:
	// the import path refuses a non-bank ledger outright, so the worst case is
	// an unused field rather than a wrong answer.
	BankAccountNumber string
	BankIFSC          string

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

	// ⭐⭐ T8 STEP 6 ADDED THE TRIPLE HERE, AND IT IS A GAP RATHER THAN A FEATURE
	// REQUEST. Migration 0080 put `currency_id`, `foreign_amount` and `rate` on
	// `ledger_opening_bill` — with a COMMENT ON COLUMN explaining that
	// Outstandings nets this table in a UNION with `bill_allocation`, so a
	// base-only opening bill would net into the same ref_name group as the
	// foreign allocations that settle it — and then NO WRITE PATH EVER SET THEM.
	// The columns existed, the CHECK constraint guarded them, and a foreign
	// opening bill could not be keyed through the product at all.
	//
	// ⛔ WHICH MADE THE REPORT BRANCH THAT READS THEM UNREACHABLE. §0.2 of the
	// design memo is a whole section on why the opening breakup must carry a
	// currency; step 6 could read it and could not create one to read, so the
	// branch could be verified only against hand-written SQL — and a report
	// branch whose only fixture is an INSERT nobody in the field can produce is
	// a branch that is asserted, not tested.
	//
	// ⚠️ ALL-OR-NONE, AND THE DATABASE IS STILL THE AUTHORITY.
	// `ledger_opening_bill_forex_all_or_none` fires whatever this struct carries;
	// `validate` below turns it into a sentence that names WHICH of the three is
	// missing, exactly as `ledger.forexTriple.validate` does for a posting. The
	// two-of-three row is the dangerous one: it lets a report compute a plausible
	// wrong number.
	//
	// ⛔ AND THE RATE IS THE RATE THE OPENING BILL WAS RAISED AT — the historical
	// truth of that bill, never re-read from `currency_rate`. Step 5's engine
	// differences a settlement against exactly this value (`openBillForex` reads
	// this table), so a wrong rate here is a wrong realised gain later.
	CurrencyID    uuid.UUID
	ForeignAmount decimal.Decimal
	Rate          decimal.Decimal
}

// isBase reports whether this opening bill is carried in the books' own currency.
// ⛔ THE ENCODING IS 0080's AND THERE IS ONLY ONE: no currency at all. A zero uuid
// and a zero rate are what "base" looks like on the way in; NULL is what it looks
// like in the table, and `nullForexOpening` is the single place that maps between
// them.
func (b OpeningBill) isBase() bool {
	return b.CurrencyID == uuid.Nil && b.ForeignAmount.IsZero() && b.Rate.IsZero()
}

// validateForex turns `ledger_opening_bill_forex_all_or_none` into a sentence.
// ⚠️ IT MIRRORS `ledger.forexTriple.validate` DELIBERATELY, including its reasoning:
// `violates check constraint "ledger_opening_bill_forex_all_or_none"` is true and
// tells an operator nothing they can act on.
func (b OpeningBill) validateForex() error {
	if b.isBase() {
		return nil
	}
	switch {
	case b.CurrencyID == uuid.Nil:
		return fmt.Errorf("masters: opening bill %q names a foreign amount or a rate but "+
			"no currency; a foreign figure needs all three of currency, foreign amount "+
			"and rate, or none of them (an opening bill with no currency IS the base "+
			"currency)", b.RefName)
	case b.ForeignAmount.IsZero():
		return fmt.Errorf("masters: opening bill %q names a currency but no foreign "+
			"amount; a foreign figure needs all three of currency, foreign amount and "+
			"rate", b.RefName)
	case b.ForeignAmount.IsNegative():
		return fmt.Errorf("masters: opening bill %q has a negative foreign amount (%s); "+
			"direction is carried by dr_cr, so every amount on an opening bill is "+
			"unsigned", b.RefName, b.ForeignAmount.StringFixed(4))
	case b.Rate.IsZero():
		return fmt.Errorf("masters: opening bill %q names a currency but no exchange "+
			"rate; a foreign figure needs all three of currency, foreign amount and "+
			"rate", b.RefName)
	case b.Rate.IsNegative():
		return fmt.Errorf("masters: opening bill %q has a negative exchange rate (%s); a "+
			"rate is base units per one foreign unit and is always above zero",
			b.RefName, b.Rate.String())
	}
	return nil
}

// nullForexOpening maps the triple to the three query arguments.
//
// ⛔ nil, NEVER THE ZERO VALUE — `ledger.nullForex`'s reasoning, restated here
// because this package cannot import it: uuid.Nil is a legal-looking uuid and 0 is
// a legal-looking numeric, and either would satisfy the all-or-none CHECK as a
// PRESENT value, quietly marking a rupee opening bill as foreign against a currency
// that does not exist.
func nullForexOpening(b OpeningBill) (any, any, any) {
	if b.isBase() {
		return nil, nil, nil
	}
	return b.CurrencyID, b.ForeignAmount, b.Rate
}

// EnsureLedger resolves a ledger by name, creating it under groupName if it does
// not exist. It is the convenience entry point for callers (and other agents)
// that just need a ledger to post to. Returns the ledger id.
func (s *Service) EnsureLedger(ctx context.Context, name, groupName string, createdBy string) (uuid.UUID, error) {
	id, _, err := s.LedgerIDByName(ctx, name)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return uuid.Nil, err
	}
	return s.CreateLedger(ctx, Ledger{Name: name, GroupName: groupName}, createdBy)
}

// LedgerIDByName resolves a ledger case- and whitespace-insensitively, returning
// its id and the name as stored.
//
// ⛔ THE ENSURE VERBS MUST MATCH THE WAY UNIQUENESS IS ENFORCED, and since
// migration 0075 that is lower(btrim(name)), not the raw string. Resolving
// exactly while refusing normalised leaves a gap that nothing can get through:
// the resolve misses, the create is refused as a duplicate, and the caller — who
// asked only for "make sure this exists" — gets an error for a ledger that DOES
// exist. A real Tally import walked straight into it on its 2,522nd master, an
// export naming "SALARY PAYABLE" against the seeded "Salary Payable", and the
// whole migration stopped there.
//
// LedgerID() stays EXACT on purpose: it answers "the ledger by this name", and a
// caller resolving a name a human typed should not be silently given a different
// spelling. This one answers "the ledger this name would collide with", which is
// the question ensure is really asking.
func (s *Service) LedgerIDByName(ctx context.Context, name string) (uuid.UUID, string, error) {
	var (
		id     uuid.UUID
		stored string
	)
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`SELECT id, name FROM ledger WHERE lower(btrim(name)) = lower(btrim($1))`,
			name).Scan(&id, &stored)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("masters: %w: ledger %q", ErrNotFound, name)
		}
		return err
	})
	if err != nil {
		return uuid.Nil, "", err
	}
	return id, stored, nil
}

// EnsureLedgerWithOpening resolves a ledger by name; if it already exists it is
// returned UNCHANGED (an opening balance or GST master already set is never
// clobbered — resolve-first). If absent it is created with the supplied opening
// balance and GST master attributes. This is the importer's first-pass entry
// point for LEDGER masters carrying an opening balance and/or GST identity.
func (s *Service) EnsureLedgerWithOpening(ctx context.Context, l Ledger, createdBy string) (uuid.UUID, error) {
	id, _, err := s.LedgerIDByName(ctx, l.Name)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return uuid.Nil, err
	}
	return s.CreateLedger(ctx, l, createdBy)
}

// ErrMasterNameTaken is a master whose name (or, for a unit, symbol) the company
// already uses.
//
// ⛔ IT EXISTS BECAUSE THE REFUSAL REACHED THE OPERATOR AS A POSTGRES ERROR.
// Driving L13's D47 arm — the row that made a refused create reach the shell's
// status line at all — showed what actually arrived there:
//
//	Could not create ledger "Acme": masters: insert ledger: ERROR: duplicate key
//	value violates unique constraint "ledger_company_id_name_key" (SQLSTATE 23505)
//
// A refusal an accountant cannot read is only marginally better than the silence
// D47 records, and it hands out the schema's constraint names for free. Every
// name/symbol collision on the five kinds the master-create form can build is
// mapped here instead. It is deliberately ONE sentinel and not five: the caller
// (writeMastersError -> 422) treats them identically, and the sentence names the
// kind, so five sentinels would be five copies of one rule.
var ErrMasterNameTaken = errors.New("masters: that name is already in use")

// MasterNameTaken is the error actually returned, so the operator reads ONE
// sentence naming the kind and the name. It matches ErrMasterNameTaken under
// errors.Is (via Is below) without wrapping it, because a wrap would print both
// sentences and they say the same thing twice.
type MasterNameTaken struct {
	Kind string // "ledger", "group", "unit", "stock item", "voucher type"
	Name string
	// Existing is the name ALREADY STORED, when it differs from Name.
	//
	// ⛔ IT EXISTS BECAUSE THE COLLISION IS NO LONGER BYTE-EXACT (D90).
	// Uniqueness is now case-insensitive and whitespace-trimmed, so an operator
	// who types "acme traders" collides with a stored "Acme Traders" — and a
	// message quoting only what THEY typed reads as a lie: they can see there is
	// no "acme traders" in the picker. Naming the stored master is what turns
	// the refusal into an instruction.
	Existing string
}

func (e *MasterNameTaken) Error() string {
	if e.Existing != "" && e.Existing != e.Name {
		return fmt.Sprintf(
			"masters: this company already has a %s named %q, which is the same name as %q "+
				"apart from capitalisation or surrounding spaces — use the existing one",
			e.Kind, e.Existing, e.Name)
	}
	return fmt.Sprintf("masters: this company already has a %s named %q", e.Kind, e.Name)
}

// Is makes errors.Is(err, ErrMasterNameTaken) true for every kind, so the HTTP
// layer keeps one branch (writeMastersError -> 422) rather than five.
func (e *MasterNameTaken) Is(target error) bool { return target == ErrMasterNameTaken }

// nameTaken maps a Postgres unique violation on `constraint` to a
// MasterNameTaken; any other error passes through wrapped by `context`. A BARE
// 23505 test would be wrong — a master insert can violate other unique
// constraints (the company-scope keys) and those are bugs, not name collisions,
// so the constraint is named explicitly exactly as isUniqueViolationOn's own
// header requires.
func nameTaken(err error, constraint, kind, name, context string) error {
	if isUniqueViolationOn(err, constraint) {
		return &MasterNameTaken{Kind: kind, Name: name}
	}
	return fmt.Errorf("masters: %s: %w", context, err)
}

// refuseIfNameTaken looks the name up BEFORE the INSERT and refuses with the
// STORED name when one collides under the normalised rule (D90, migration
// 0075).
//
// ⛔⛔ IT RUNS BEFORE THE INSERT, AND THAT ORDER IS NOT A STYLE CHOICE.
// The obvious shape is to let the unique index fire and then read the stored
// name to build the message. It does not work: by then the transaction is in a
// FAILED state, and Postgres rejects every further query in it with
// "current transaction is aborted". The first version of this did exactly that
// and every refusal silently fell back to quoting the caller's own text —
// measured on the wire, where an operator who typed "d90x ..." was told they
// already had a stock item named "d90x ...", which they can see they do not.
//
// ⚠️ THE UNIQUE INDEX IS STILL THE AUTHORITY AND STILL RUNS. This check races:
// two concurrent creates of the same name both pass it and one is refused by
// the index, with the plainer message. That is the correct trade — the common
// case gets a sentence naming the master the operator actually has, and the
// rare case gets a correct refusal with a vaguer sentence. A pre-check that
// REPLACED the constraint would be the D83 defect by construction.
//
// #nosec G201 -- table and column are compile-time literals from this file's
// own call sites, never caller input.
func refuseIfNameTaken(
	ctx context.Context, tx pgx.Tx, kind, name, table, column string,
) error {
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return db.ErrNoCompany
	}
	var existing string
	err := tx.QueryRow(ctx,
		`SELECT `+column+` FROM `+table+
			` WHERE company_id = $1 AND lower(btrim(`+column+`)) = lower(btrim($2))`,
		companyID, name).Scan(&existing)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("masters: check %s name: %w", kind, err)
	}
	return &MasterNameTaken{Kind: kind, Name: name, Existing: existing}
}

// nameTakenNormalised is nameTaken for the three masters whose uniqueness is
// NORMALISED. It is the BACKSTOP behind refuseIfNameTaken — reached only when
// two creates race — so its message cannot name the stored master (see above).
//
// ⛔ THE CONSTRAINT NAME IT IS GIVEN MUST BE THE INDEX 0075 CREATES, NOT THE
// CONSTRAINT IT DROPPED. `isUniqueViolationOn` compares the name EXACTLY, so a
// stale name here does not loosen the refusal — the database still refuses —
// it makes the refusal come out as a raw wrapped driver error instead of a
// sentence. That is register row D93's exact shape, one migration away at all
// times, and it is why the sentence is asserted in a test rather than the
// status code.
func nameTakenNormalised(err error, constraint, kind, name, context_ string) error {
	if isUniqueViolationOn(err, constraint) {
		return &MasterNameTaken{Kind: kind, Name: name}
	}
	return fmt.Errorf("masters: %s: %w", context_, err)
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
	// ⛔ VALIDATED HERE, NOT LEFT TO THE ENUM CAST — register row D93.
	// `registration_type` is a Postgres ENUM, so an unrecognised value did not
	// pass silently: it failed the cast inside the INSERT and the raw driver
	// error went to the caller verbatim, SQLSTATE and all —
	//
	//   masters: insert ledger: ERROR: invalid input value for enum
	//   gst_registration_type: "Regular" (SQLSTATE 22P02)
	//
	// which names an internal type, quotes a SQLSTATE, and tells an operator
	// nothing about what they may type instead. `drive-l13-masters.mjs` already
	// asserts `!/SQLSTATE/` on a refusal message from THIS ROUTE, so the
	// expectation was pinned on one path and unenforced on three others.
	//
	// Found by a MUTATION PROOF, and by the prediction being WRONG: sending the
	// display label "Regular" instead of "REGULAR" was expected to be silently
	// coerced to the UNKNOWN default. It is not — the refusal is correct and
	// only its SENTENCE was wrong. See §4.2.
	if !l.RegistrationType.valid() {
		return uuid.Nil, fmt.Errorf(
			"masters: %q is not a GST registration type (want one of %s)",
			l.RegistrationType, strings.Join(knownRegistrationTypes(), ", "))
	}
	// The same treatment for the two CHECK constraints an HTTP caller can
	// reach. Both leaked the constraint NAME as well as the SQLSTATE.
	if l.BankIFSC != "" && !ifscRE.MatchString(l.BankIFSC) {
		return uuid.Nil, fmt.Errorf(
			"masters: %q is not an IFSC (four letters, then 0, then six letters or digits)",
			l.BankIFSC)
	}
	if l.BankAccountNumber != "" && strings.TrimSpace(l.BankAccountNumber) == "" {
		return uuid.Nil, errors.New(
			"masters: a bank account number cannot be only spaces — leave it empty if it is not recorded")
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
			if err := b.validateForex(); err != nil {
				return uuid.Nil, err
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
		// address/city/pincode go in through NULLIF like every other optional
		// party attribute on this table, so "not supplied" lands as NULL rather
		// than as an empty string that a later reader would have to treat as a
		// fourth way of spelling absent. Dropping them from this column list is
		// what made every tax invoice unprintable — see the Ledger.Address
		// comment and TestAPartyWithNoAddressOnFileIsATaxInvoiceThatCannotBeIssued.
		// D90: refuse a NORMALISED collision here, so the message can name
		// the master the operator actually has. The unique index behind it
		// is still the authority and still catches a race.
		if err := refuseIfNameTaken(ctx, tx, "ledger", l.Name, "ledger", "name"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO ledger
			   (id, company_id, name, group_id, opening_balance, opening_dc,
			    maintain_bill_by_bill, gstin, state_code, registration_type,
			    pan, credit_days, address, city, pincode,
			    bank_account_number, bank_ifsc, credit_limit)
			 VALUES ($1, $2, $3, $4, $5, $6, $7,
			         NULLIF($8,''), NULLIF($9,''), $10::gst_registration_type,
			         NULLIF($11,''), $12,
			         NULLIF($13,''), NULLIF($14,''), NULLIF($15,''),
			         NULLIF($16,''), NULLIF($17,''), $18)`,
			id, companyID, l.Name, groupID, l.OpeningBalance, l.OpeningDC,
			l.MaintainBillByBill, l.GSTIN, l.StateCode, string(l.RegistrationType),
			l.PAN, l.CreditDays, l.Address, l.City, l.PIN,
			l.BankAccountNumber, strings.ToUpper(strings.TrimSpace(l.BankIFSC)),
			l.CreditLimit,
		); err != nil {
			return nameTakenNormalised(err,
				"ledger_company_name_normalised", "ledger", l.Name, "insert ledger")
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
			fxCur, fxAmt, fxRate := nullForexOpening(b)
			if _, err := tx.Exec(ctx,
				`INSERT INTO ledger_opening_bill
				   (id, company_id, ledger_id, ref_name, ref_type, dr_cr, amount, due_date,
				    currency_id, foreign_amount, rate)
				 VALUES ($1, $2, $3, $4, $5::bill_ref_type, $6, $7, NULLIF($8,'')::date,
				         $9, $10, $11)`,
				billID, companyID, id, b.RefName, refType, drcr, b.Amount, b.DueDate,
				fxCur, fxAmt, fxRate,
			); err != nil {
				return fmt.Errorf("masters: insert opening bill %q: %w", b.RefName, err)
			}
		}
		after := map[string]any{
			"name": l.Name, "group": l.GroupName, "gstin": l.GSTIN,
			"state_code": l.StateCode, "registration_type": string(l.RegistrationType),
			"maintain_bill_by_bill": l.MaintainBillByBill,
			// The address is audited because it is a MANDATORY INVOICE
			// PARTICULAR (Rule 46(d)): when a customer disputes the address on a
			// document, the question is what the master said on the day it was
			// printed, and an unaudited field cannot answer that.
			"address": l.Address, "city": l.City, "pincode": l.PIN,
			// The bank details are audited for the same reason the address is:
			// when an import is refused as "the wrong account", the question is
			// what the master said at the time, and an unaudited field cannot
			// answer it.
			"bank_account_number": l.BankAccountNumber, "bank_ifsc": l.BankIFSC,
		}
		// Audited only when set, so every ledger created before 0103 keeps
		// the audit-doc keys it always had.
		if l.CreditLimit != nil {
			after["credit_limit"] = l.CreditLimit.StringFixed(2)
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
			if err := b.validateForex(); err != nil {
				return err
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
			fxCur, fxAmt, fxRate := nullForexOpening(b)
			if _, err := tx.Exec(ctx,
				`INSERT INTO ledger_opening_bill
				   (id, company_id, ledger_id, ref_name, ref_type, dr_cr, amount, due_date,
				    currency_id, foreign_amount, rate)
				 VALUES ($1, $2, $3, $4, $5::bill_ref_type, $6, $7, NULLIF($8,'')::date,
				         $9, $10, $11)`,
				billID, companyID, ledgerID, b.RefName, refType, drcr, b.Amount, b.DueDate,
				fxCur, fxAmt, fxRate,
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

// LedgerOpening returns a ledger's opening balance and its Dr/Cr side.
//
// It exists for the importer, which must tell two situations apart that the
// ensure verbs deliberately blur: a ledger it just created (opening already
// written) and one that was ALREADY THERE — seeded, or from an earlier import —
// whose opening is still zero and whose real opening is sitting in the file.
// EnsureLedgerWithOpening returns an existing ledger UNCHANGED, which is the
// right rule and, on its own, silently drops that opening.
func (s *Service) LedgerOpening(ctx context.Context, id uuid.UUID) (decimal.Decimal, string, error) {
	var (
		bal decimal.Decimal
		dc  string
	)
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`SELECT opening_balance, opening_dc FROM ledger WHERE id = $1`, id).Scan(&bal, &dc)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("masters: %w: ledger %s", ErrNotFound, id)
		}
		return err
	})
	if err != nil {
		return decimal.Zero, "", err
	}
	return bal, dc, nil
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

// LedgerAddress is a party's POSTAL BLOCK: the address a document issued to them
// is addressed to. Address is NEWLINE-SEPARATED free text, exactly as
// company.address is, so the two sides of an invoice are split by one helper.
//
// It is a separate type from LedgerGST rather than three more fields on it
// because the two answer different questions: LedgerGST decides the TAX (which
// heads apply, whether the recipient is registered), and this decides what the
// PAPER says. The tax engine has no business reading a street name.
type LedgerAddress struct {
	Address string
	City    string
	PIN     string
}

// OnFile reports whether an address was ever recorded for this party.
//
// It exists so a caller does not have to decide for itself what "no address"
// looks like. The absence is LEGAL — Rule 46(e) requires an unregistered
// recipient's address only on a supply of fifty thousand rupees or more — so the
// answer a caller wants is almost never "is this string empty" but "may I put
// this party on a tax invoice at all". Rule 46(d) says no when this is false.
func (a LedgerAddress) OnFile() bool {
	return a.Address != ""
}

// LedgerAddressInfo returns the postal block for a ledger by id. Missing fields
// come back as empty strings, matching LedgerGSTInfo — the column is nullable
// because a walk-in cash party genuinely has no address on file.
func (s *Service) LedgerAddressInfo(ctx context.Context, ledgerID uuid.UUID) (LedgerAddress, error) {
	var out LedgerAddress
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var address, city, pin *string
		err := tx.QueryRow(ctx,
			`SELECT address, city, pincode FROM ledger WHERE id = $1`,
			ledgerID).Scan(&address, &city, &pin)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("masters: %w: ledger %s", ErrNotFound, ledgerID)
		}
		if err != nil {
			return err
		}
		if address != nil {
			out.Address = *address
		}
		if city != nil {
			out.City = *city
		}
		if pin != nil {
			out.PIN = *pin
		}
		return nil
	})
	if err != nil {
		return LedgerAddress{}, err
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
			return nameTaken(err, "voucher_type_company_id_name_key", "voucher type", vt.Name, "insert voucher type")
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
	// BaseUnitSymbol and ConversionFactor make this a COMPOUND unit (T10):
	// 1 <Symbol> = <ConversionFactor> <BaseUnitSymbol>, e.g. 1 BOX = 12 NOS.
	// Both empty/zero is a SIMPLE unit, which is what every unit created before
	// T10 is and what every caller that does not know about this field creates.
	//
	// ⛔ A QUANTITY IS NEVER CONVERTED ON THE WAY IN. A movement of 5 BOX stores
	// 5.0000 in BOX, and a report that wants pieces multiplies at READ time —
	// the same rule stock VALUE follows, for the same reason: an inbound
	// conversion destroys the number that was keyed, so the invoice and the stock
	// ledger would no longer agree, and correcting a factor later (the supplier
	// repacks 12 to a box as 10) could not restate the history. See migration
	// 0079's header.
	BaseUnitSymbol   string
	ConversionFactor decimal.Decimal
}

// UnitRecord is a unit as stored, for a list or a picker: the symbol, and — for
// a compound unit — what it is defined in terms of.
type UnitRecord struct {
	ID               uuid.UUID
	Symbol           string
	FormalName       string
	DecimalPlaces    int
	BaseUnitSymbol   string
	ConversionFactor decimal.Decimal
	IsSystem         bool
}

// Compound reports whether the unit is defined in terms of another.
func (u UnitRecord) Compound() bool { return u.BaseUnitSymbol != "" }

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
	// A compound unit needs BOTH halves or neither: the CHECK constraint
	// unit_compound_is_whole says so too, and catching it here names the field.
	compound := strings.TrimSpace(u.BaseUnitSymbol) != ""
	if compound && !u.ConversionFactor.IsPositive() {
		return uuid.Nil, fmt.Errorf(
			"masters: unit %q is defined in terms of %q, so it needs a conversion factor greater "+
				"than zero (1 %s = ? %s); a factor of zero would make every quantity in %s worth "+
				"nothing in %s",
			u.Symbol, u.BaseUnitSymbol, u.Symbol, u.BaseUnitSymbol, u.Symbol, u.BaseUnitSymbol)
	}
	if !compound && !u.ConversionFactor.IsZero() {
		return uuid.Nil, fmt.Errorf(
			"masters: unit %q carries a conversion factor of %s and names no base unit to convert "+
				"TO — a factor with no base converts to nothing, and a unit that looks compound to "+
				"a reader and cannot be restated is worse than a simple one",
			u.Symbol, u.ConversionFactor.String())
	}

	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// D90: refuse a NORMALISED collision here, so the message can name
		// the master the operator actually has. The unique index behind it
		// is still the authority and still catches a race.
		if err := refuseIfNameTaken(ctx, tx, "unit", u.Symbol, "unit", "symbol"); err != nil {
			return err
		}
		var baseID *uuid.UUID
		var factor *decimal.Decimal
		if compound {
			var (
				bid      uuid.UUID
				baseBase *uuid.UUID
			)
			err := tx.QueryRow(ctx,
				`SELECT id, base_unit_id FROM unit
				  WHERE lower(btrim(symbol)) = lower(btrim($1))`,
				u.BaseUnitSymbol).Scan(&bid, &baseBase)
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("masters: %w: base unit %q (create it before the compound unit "+
					"that is measured in it)", ErrNotFound, u.BaseUnitSymbol)
			}
			if err != nil {
				return fmt.Errorf("masters: resolve base unit: %w", err)
			}
			// ⛔ A CHAIN IS REFUSED, NOT FLATTENED. Tally allows a compound unit
			// whose base is itself compound (a PALLET of 20 BOX of 12 NOS) and
			// this build does not, because a conversion that resolves ONE level
			// of a two-level chain is wrong by the second factor — a factor of
			// twelve, reported as confidently as any other number. Refusing says
			// so; resolving one level would not.
			if baseBase != nil {
				return fmt.Errorf(
					"masters: unit %q would be measured in %q, which is itself a compound unit. "+
						"This build converts ONE level only, so a chain is refused rather than "+
						"half-resolved — define %q in terms of the SIMPLE unit at the bottom of "+
						"the chain instead",
					u.Symbol, u.BaseUnitSymbol, u.Symbol)
			}
			f := u.ConversionFactor
			baseID, factor = &bid, &f
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO unit (id, company_id, symbol, formal_name, decimal_places,
			                   base_unit_id, conversion_factor)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			id, companyID, u.Symbol, u.FormalName, u.DecimalPlaces, baseID, factor,
		); err != nil {
			return nameTakenNormalised(err,
				"unit_company_symbol_normalised", "unit", u.Symbol, "insert unit")
		}
		after := map[string]any{"symbol": u.Symbol, "formal_name": u.FormalName}
		if compound {
			after["base_unit"] = u.BaseUnitSymbol
			after["conversion_factor"] = u.ConversionFactor.String()
		}
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
	// StockGroupName is the item's STOCK GROUP (T10), or empty for an ungrouped
	// item — which is a real state (Tally's "under Primary") and not a missing
	// value: the Stock Summary renders ungrouped items as a stated bucket rather
	// than hiding them or filing them somewhere nobody chose.
	//
	// ⚠️ RESOLVED CASE-INSENSITIVELY and it must ALREADY EXIST. Creating a group
	// implicitly from a misspelling here is how a company ends up with "Raw
	// Materials" and "Raw materials" subtotalling half its stock each.
	StockGroupName string
	// ValuationMethod is the COSTING METHOD this item's movements are replayed
	// through — inventory.MethodWeightedAverage (the default, and Tally's),
	// MethodFIFO or MethodLIFO. Empty means "the default", so every caller that
	// predates T9 keeps creating weighted-average items without knowing the
	// field exists.
	//
	// ⛔ IT IS VALIDATED AGAINST inventory.ValuerFor AND NOT AGAINST A LIST HERE.
	// A method this binary has no valuer for must never reach the column: the
	// reports would then fail on the Stock Summary and the Balance Sheet — loudly,
	// by design, but for the whole company and not just this item, and long after
	// the master was created. There are exactly two copies of the vocabulary (the
	// valuers, and migration 0078's CHECK) and TestValuationMethodsMatchTheMigration
	// pins them to each other.
	ValuationMethod string
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
	method, err := valuationMethodOrDefault(si.ValuationMethod)
	if err != nil {
		return uuid.Nil, err
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
		var groupID *uuid.UUID
		if strings.TrimSpace(si.StockGroupName) != "" {
			var gid uuid.UUID
			err := tx.QueryRow(ctx,
				`SELECT id FROM stock_group WHERE lower(btrim(name)) = lower(btrim($1))`,
				si.StockGroupName).Scan(&gid)
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("masters: %w: stock group %q (create the group before the item "+
					"that belongs to it)", ErrNotFound, si.StockGroupName)
			}
			if err != nil {
				return fmt.Errorf("masters: resolve stock group: %w", err)
			}
			groupID = &gid
		}
		// D90: refuse a NORMALISED collision here, so the message can name
		// the master the operator actually has. The unique index behind it
		// is still the authority and still catches a race.
		if err := refuseIfNameTaken(ctx, tx, "stock item", si.Name, "stock_item", "name"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO stock_item
			   (id, company_id, name, unit_id, hsn, gst_rate_pct, valuation_method, stock_group_id)
			 VALUES ($1, $2, $3, $4, NULLIF($5,''), $6, $7, $8)`,
			id, companyID, si.Name, unitID, si.HSN, si.GSTRatePct, method, groupID,
		); err != nil {
			return nameTakenNormalised(err,
				"stock_item_company_name_normalised", "stock item", si.Name, "insert stock item")
		}
		after := map[string]any{
			"name": si.Name, "unit": si.UnitSymbol, "hsn": si.HSN,
			"gst_rate_pct": si.GSTRatePct.String(),
			"stock_group":  si.StockGroupName,
			// The costing method is in the audit row because it decides every
			// value this item will ever be reported at. A master_audit trail that
			// recorded the name and not the basis could not explain why the
			// Balance Sheet moved.
			"valuation_method": method,
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
