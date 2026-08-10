// Package ledger owns the atomic, double-entry voucher-post path — the
// load-bearing transactional spine of cored. It is the SOLE writer of
// voucher_version, posting, bill_allocation, and ledger_balance, so other
// modules participate in a post only through this package's interface.
//
// A post is atomic: in ONE database transaction (scoped to the company GUC by
// the db layer) it appends an immutable, hash-chained voucher_version, inserts
// the balanced postings (and any bill-wise allocations), and incrementally
// updates ledger_balance. The per-voucher BLAKE3 hash chain makes the log
// tamper-evident and doubles as the MCA Rule 11(g) edit log: an alter or delete
// appends a NEW version that chains onto the previous one's hash rather than
// mutating it.
//
// Sales and Purchase posts may carry GST derivation parameters; in that case the
// ledger calls the pure compliance.TaxDerive engine inside the post transaction
// to compute the CGST/SGST/IGST split, asserts it against the supplied tax
// postings, and records the derived breakup on the voucher version for GSTR.
package ledger

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/compliance"
	"github.com/khata/cored/internal/platform/db"
)

// TxRunner is the minimal database capability the ledger needs: run a unit of
// work inside a tenant-scoped transaction. *db.Pool satisfies it. Depending on
// the interface (not the concrete pool) keeps the module boundary clean.
type TxRunner interface {
	RunInTx(ctx context.Context, fn db.TxFunc) error
}

// DrCr is the debit/credit side of a posting.
type DrCr string

const (
	// Debit marks a debit posting.
	Debit DrCr = "D"
	// Credit marks a credit posting.
	Credit DrCr = "C"
)

// Class is the accounting class of a voucher, mapping to Tally's F-key voucher
// types (RESEARCH sec 5). It drives validation and report attribution.
type Class string

const (
	// Contra (F4): transfers between cash/bank accounts only.
	Contra Class = "Contra"
	// Payment (F5): money out.
	Payment Class = "Payment"
	// Receipt (F6): money in.
	Receipt Class = "Receipt"
	// Journal (F7): non-cash adjustments.
	Journal Class = "Journal"
	// Sales (F8): sales invoices.
	Sales Class = "Sales"
	// Purchase (F9): purchase invoices.
	Purchase Class = "Purchase"
	// CreditNote (Alt+F6): a SALES return / downward price correction. It is the
	// reverse of a Sales invoice — Dr Sales, Dr the output-tax heads, Cr the
	// customer — and reduces outward liability. It is the CORRECT way to undo
	// part of an invoice: altering the original rewrites history and (before this
	// pass) silently dropped the invoice from GSTR-1 entirely.
	CreditNote Class = "Credit Note"
	// DebitNote (Alt+F5): a PURCHASE return / upward price correction raised on a
	// supplier — Dr the supplier, Cr Purchases, Cr the input-tax heads.
	DebitNote Class = "Debit Note"
	// Payroll: the monthly pay run — Dr Salaries & Wages and the employer's PF
	// and ESI cost, Cr net pay, TDS on salary, PF and ESI payable.
	//
	// It is a CLASS OF ITS OWN and not a Journal, and that is not a naming
	// preference: migration 0011 seeds `allow create|alter|display journal` on
	// the Data Entry level of EVERY company in the fleet, so posting a pay run
	// as a Journal would grant every existing operator the right to create and
	// alter salary vouchers the day the route mounts — retroactively, with no
	// migration, and with nobody deciding it. A voucher_type named 'Payroll'
	// parented to JOURNAL does not fix it, because routeAction derives the ACL
	// token from the CLASS. See payroll_class.go for the full argument, and for
	// why the class is an ACCOUNTING one (a non-accounting pay run would post
	// zero rows, and posting_balanced is a trigger on the posting TABLE — a
	// voucher with no postings never fires it).
	//
	// Its system voucher_type is seeded by migration 0034 with
	// parent_class = 'PAYROLL', the form defaultVoucherTypeID matches.
	Payroll Class = "Payroll"
)

// validClasses is the set of accepted voucher classes: the accounting classes
// plus the stock-only ones that post no accounting lines at all (Delivery Note,
// Receipt Note, Stock Journal, Physical Stock — see nonaccounting.go).
//
// The two halves are kept as separate sets and UNIONED here rather than listed
// again, so a class can never be postable while belonging to neither half —
// which is what would make post()'s double-entry branch fall through to the
// no-postings-required arm by accident.
var validClasses = func() map[Class]bool {
	m := make(map[Class]bool, len(accountingClasses)+len(stockOnlyClasses))
	for c := range accountingClasses {
		m[c] = true
	}
	for c := range stockOnlyClasses {
		m[c] = true
	}
	return m
}()

// gstBearing reports whether a class participates in GST derivation. Credit and
// Debit Notes carry tax exactly as the invoices they adjust do — a credit note
// that did not derive GST would reduce the sale but not the tax on it, leaving
// the return overstating liability with nothing to show why.
func gstBearing(c Class) bool {
	switch c {
	case Sales, Purchase, CreditNote, DebitNote:
		return true
	}
	return false
}

// ErrVoucherNotFound is returned by AlterVoucher and DeleteVoucher when no
// voucher_head row exists for the (company, voucher_id) pair. Without this
// guard an alter/delete on an unknown id would silently mint version 1 and
// create a phantom voucher; the HTTP layer maps it to a 404.
var ErrVoucherNotFound = errors.New("ledger: voucher not found")

// RefType is a bill-wise allocation reference type (RESEARCH sec 5).
type RefType string

const (
	// RefNew opens a new outstanding bill.
	RefNew RefType = "NEW"
	// RefAgainst settles against an existing open bill.
	RefAgainst RefType = "AGAINST"
	// RefAdvance records money paid/received before invoice.
	RefAdvance RefType = "ADVANCE"
	// RefOnAccount is unallocated (no specific bill).
	RefOnAccount RefType = "ON_ACCOUNT"
)

var validRefTypes = map[RefType]bool{
	RefNew: true, RefAgainst: true, RefAdvance: true, RefOnAccount: true,
}

// PostingInput is a single double-entry line to post.
type PostingInput struct {
	LedgerID uuid.UUID
	DrCr     DrCr
	Amount   decimal.Decimal
}

// BillAllocationInput is a single bill-wise allocation against a party ledger
// line. The sum of a ledger's allocations should equal that ledger's net
// posting amount on the same side; this is the caller's responsibility (the
// engine records what it is given).
type BillAllocationInput struct {
	LedgerID uuid.UUID
	RefName  string
	RefType  RefType
	DrCr     DrCr
	Amount   decimal.Decimal
	DueDate  string // ISO date (YYYY-MM-DD) or empty
}

// GSTParams carries the inputs needed to auto-derive GST on a Sales/Purchase
// voucher. When present (non-nil) on a Sales or Purchase post, the ledger calls
// compliance.TaxDerive and (a) records the derived CGST/SGST/IGST/taxable on the
// voucher version and (b) verifies the supplied tax postings match the derived
// amounts, failing the post on mismatch. This wires the compliance engine into
// the posting path so tax is never hand-computed and silently wrong.
type GSTParams struct {
	RulesetVersion         string
	SupplierStateCode      string
	PlaceOfSupplyStateCode string
	ReverseCharge          bool
	Exempt                 bool
	// Lines are the taxable lines for derivation (taxable value + rate).
	Lines []compliance.Line
	// CGSTLedgerID / SGSTLedgerID / IGSTLedgerID, when set, identify the tax
	// ledgers whose posted amounts are cross-checked against the derived split.
	CGSTLedgerID uuid.UUID
	SGSTLedgerID uuid.UUID
	IGSTLedgerID uuid.UUID
}

// VoucherInput describes any of the six Phase 1 accounting voucher types.
type VoucherInput struct {
	Class         Class
	VoucherTypeID uuid.UUID
	// PartyLedgerID is the principal party (debtor/creditor/cash/bank) for
	// reporting and bill-wise attribution; optional — but REQUIRED on an item
	// invoice, where it is the ledger the generated party leg is posted to.
	PartyLedgerID uuid.UUID
	// ItemLedgerID is the revenue/expense ledger an ITEM INVOICE books the value
	// of the goods to: the Sales Accounts ledger for F8, the Purchase Accounts
	// ledger for F9. It is meaningful ONLY on an item invoice (inventory lines +
	// GST params), where the server generates the postings; every other voucher
	// states its own postings and leaves this zero. See item_invoice.go.
	ItemLedgerID uuid.UUID
	VoucherNo    string
	VoucherDate  string // ISO date (YYYY-MM-DD)
	Narration    string
	Party        string // party name (denormalised label)
	CreatedBy    string
	// Postings must balance: SUM(debit) == SUM(credit). The deferred DB trigger
	// enforces this at commit as a final backstop.
	Postings []PostingInput
	// BillAllocations are optional bill-wise references on party ledgers.
	BillAllocations []BillAllocationInput
	// PhysicalCount are the COUNTED positions of a Physical Stock voucher —
	// "there are 13 on this shelf" — and are meaningful ONLY on that class,
	// where they replace Inventory entirely. The server differences each count
	// against the book position INSIDE the post transaction and writes the
	// correction as an ordinary movement; see nonaccounting.go for why the
	// delta, and not the absolute figure, is what the movement ledger records.
	PhysicalCount []PhysicalCountLine
	// Inventory are the stock lines this voucher moves, in KEYED ORDER (the
	// order is preserved as line_no and is part of the replay ordering key).
	//
	// It is INDEPENDENT of Postings, not derived from them: a three-item invoice
	// billed to one Sales ledger is one posting and three inventory lines, and a
	// Delivery Note is three inventory lines and no postings at all. A DELETE
	// tombstone carries none, exactly as it carries no postings.
	Inventory []InventoryLine
	// Inward is the SUPPLIER'S own document identity (their invoice number and
	// date) plus this voucher's ITC classification. See inward_doc.go for why
	// none of the three columns that look like they already hold this actually
	// do.
	//
	// It is written to voucher_version's four inward columns AND into the hashed
	// canonical payload, from this ONE source inside ONE transaction. An ALTER
	// RE-STATES it exactly as it re-states the taxable value: a version says
	// what the voucher is NOW, so an alter that omits it clears it, and a DELETE
	// tombstone carries none — the same rule postings, GST lines and stock
	// movements already follow.
	//
	// nil (or a zero InwardDocument) writes nothing at all: no columns, and no
	// "inward" key in the payload. That is what keeps every voucher posted
	// before migration 0032, and every sales voucher forever, hashing to exactly
	// the bytes it always did.
	Inward *InwardDocument
	// CostAllocations are how this voucher's postings were split across cost
	// centres — "of the 1,00,000 of salary, Sales bore 60,000 and Admin 40,000".
	// See cost_allocation.go.
	//
	// ⛔⛔ A CATEGORY IS AN AXIS AND A CENTRE IS A POSITION ON IT, so the SAME
	// posting is allocated IN FULL ONCE PER CATEGORY: the same 1,00,000 is split
	// across Departments AND, in parallel and also in full, across Branches. Both
	// sets are complete and both are correct. Every check on this slice therefore
	// groups by (posting, CATEGORY) and never by posting alone — a posting-wide
	// total comes to twice the cost for two axes and would refuse correct data.
	//
	// Each entry names its posting by its 0-BASED INDEX INTO Postings, which is
	// also what rides in the hashed payload. It is written to
	// posting_cost_allocation AND into CanonicalPayload.CostAllocations from this
	// ONE slice inside ONE transaction, so the rows and the signature cannot
	// disagree.
	//
	// An ALTER RE-STATES the allocations, exactly as it re-states the postings
	// they split: a version says what the voucher is NOW, so an alter that omits
	// them clears the split, and — because an ALTER writes NEW posting rows —
	// they are re-derived against those new postings and never attached to the
	// superseded ones. A DELETE tombstone carries NONE, the same rule postings,
	// GST lines, stock movements and the supplier's inward document already
	// follow: a tombstone that restated the split would leave a deleted cost
	// sitting on a department's summary forever while the voucher had vanished
	// from the P&L.
	//
	// An empty slice writes nothing at all: no rows, and no "cost_allocations"
	// key in the payload. That is what keeps every voucher posted before
	// migration 0040 hashing to exactly the bytes it always did. A posting with
	// NO allocation is legal and stays legal — allocation is optional per
	// ledger, and most postings in most books will never be allocated.
	CostAllocations []CostAllocationInput
	// Fulfilments are which ORDER LINES this voucher's inventory consumed — "of
	// the 20 Tables sales order SO-14 committed, this delivery note ships 5".
	// See fulfilment.go.
	//
	// ⛔⛔ AN ORDER IS A COMMITTED QUANTITY, NOT A MOVEMENT. A fulfilment records
	// a LINK and never a stock position: its Qty is how much of an ORDER was
	// consumed, while InventoryLine.Qty is how much STOCK moved. Closing stock
	// reads the movement, the order book reads this, and neither reads the other.
	// The two are usually equal, and the case where they legitimately differ —
	// shipping 105 against a 100-unit order, linking 100, leaving 5 that consumed
	// no order — is why over-fulfilment can be refused without preventing anyone
	// from recording what actually happened.
	//
	// Each entry names its inventory line by its 1-BASED LINE NUMBER, which is
	// also what rides in the hashed payload and what stock_movement.line_no has
	// carried since migration 0025. ⚠️ NOTE THE ASYMMETRY WITH CostAllocations
	// ABOVE, whose PostingSeq is 0-BASED: that one indexes a JSON array that has
	// never been a column, this one names a column that has always been 1-based,
	// and spelling the same voucher line two ways inside one payload would be
	// worse than the inconsistency.
	//
	// ⚠️ AND UNLIKE CostAllocations, AN ITEM INVOICE MAY CARRY THESE. The reason
	// allocations are refused there is that an item invoice's POSTINGS are
	// generated inside the transaction, so the index an allocation names is not
	// one the client chose. Its INVENTORY is the operator's own grid on every
	// class, so a line number is a fact the client stated and can be held to.
	//
	// It is written to order_fulfilment AND into CanonicalPayload.Fulfilments
	// from this ONE slice inside ONE transaction, so the rows and the signature
	// cannot disagree.
	//
	// An ALTER RE-STATES them against the NEW stock_movement rows this version
	// writes, exactly as it re-states the movements themselves: a version says
	// what the voucher is NOW, so an alter that omits them clears the link and
	// the order goes back to outstanding — the only reading under which "I keyed
	// this against the wrong order" is correctable. A DELETE tombstone carries
	// NONE, the same rule postings, stock movements, GST lines, the inward
	// document and the cost split already follow: a tombstone that restated the
	// link would leave an order reporting itself fulfilled by a voucher that had
	// vanished from the books.
	//
	// An empty slice writes nothing at all: no rows, and no "fulfilments" key in
	// the payload. That is what keeps every voucher posted before migration 0042
	// hashing to exactly the bytes it always did. A line with NO fulfilment is
	// legal and stays legal — most stock that moves in most businesses was never
	// ordered in advance.
	Fulfilments []FulfilmentInput
	// GST, when non-nil and Class is Sales or Purchase, drives auto-derivation
	// and verification of the tax lines.
	GST *GSTParams
	// TDS, when non-nil and Class is Purchase, Payment or Journal, drives
	// derivation and verification of the posted TDS leg. See tds.go.
	TDS *TDSParams
	// TCS, when non-nil and Class is Sales, drives derivation and verification
	// of the posted TCS leg — the collection-side mirror. See tcs.go.
	//
	// It carries NO declaration field: the s.394(2) exemption is read from
	// tcs_collection_declaration inside the post transaction, so it cannot be
	// asserted by a request body.
	TCS *TCSParams
	// SourceGUID is the stable external (Tally) GUID this voucher was imported
	// from, recorded on voucher_head for idempotent re-import. Empty for natively
	// created vouchers (those never participate in the (company, guid) dedupe).
	SourceGUID string
}

// SalesVoucherInput describes an F8 Sales voucher to post. Retained for
// backward compatibility; new code should prefer VoucherInput + PostVoucher.
type SalesVoucherInput struct {
	VoucherTypeID uuid.UUID
	VoucherNo     string
	VoucherDate   string
	Narration     string
	Party         string
	CreatedBy     string
	Postings      []PostingInput
}

// PostResult identifies a committed voucher version.
type PostResult struct {
	VoucherID        uuid.UUID
	VoucherVersionID uuid.UUID
	VersionNo        int
	Hash             []byte
	// VoucherNo is the resolved display number: server-allocated (AUTOMATIC create
	// with empty voucher_no), echoed (manual/override), or carried forward (alter/
	// delete). It is the single source the HTTP layer reads for the response.
	VoucherNo string
	// Tax is the derived GST breakup, populated when GST derivation ran.
	Tax compliance.TaxResult
	// TDS is the derived deduction, populated when TDS derivation ran AND this
	// company was liable to deduct. It is also PERSISTED as a tds_deduction row
	// in the same transaction (migration 0050), so the register and the return
	// read the same numbers this echoes. A company with no duty under the
	// section leaves this zero and writes no row — see ledger/tds.go.
	TDS compliance.TDSResult
	// TCS is the derived collection, populated when TCS derivation ran, and
	// PERSISTED as a tcs_collection row (migration 0067) in the same
	// transaction. Its Declaration field records what a s.394(2) declaration
	// did — NONE, EXEMPT or INVALID_NO_PAN — which is the fact a zero amount
	// alone cannot express.
	TCS compliance.TCSResult
}

// Service is the ledger application service.
type Service struct {
	runner TxRunner
}

// NewService constructs a ledger Service over the given transaction runner.
func NewService(runner TxRunner) *Service {
	return &Service{runner: runner}
}

// PostVoucher atomically posts a NEW voucher (version 1, op CREATE) of any of
// the six Phase 1 classes. It validates the class-specific double-entry shape,
// runs GST derivation for Sales/Purchase when requested, appends the
// hash-chained voucher_version, inserts postings and bill allocations, and
// updates ledger_balance — all in one company-scoped transaction.
func (s *Service) PostVoucher(ctx context.Context, in VoucherInput) (PostResult, error) {
	voucherID, err := uuid.NewV7()
	if err != nil {
		return PostResult{}, fmt.Errorf("ledger: new voucher id: %w", err)
	}
	return s.post(ctx, voucherID, OpCreate, in)
}

// PostVoucherWithID posts a NEW voucher (op CREATE) under a CALLER-SUPPLIED
// voucher id. This is the offline-sync entry point: a node creates a voucher
// while partitioned, minting a node-partitioned UUIDv7 locally, and that id IS
// the committed identity once the outbox commits through the cloud sequencer.
// It is otherwise identical to PostVoucher — numbering still runs (the input's
// VoucherNo is left empty so the server allocates the FINAL display number) and
// a nil VoucherTypeID is resolved from the class — only the id is supplied
// rather than generated. The caller (the sequencer) guarantees no live head
// already exists for the id before invoking this.
func (s *Service) PostVoucherWithID(ctx context.Context, id uuid.UUID, in VoucherInput) (PostResult, error) {
	return s.post(ctx, id, OpCreate, in)
}

// HeadVersion returns the current head version_no for a LIVE voucher in the
// company in context. A voucher is live when a voucher_head row exists and is
// not tombstoned (is_deleted = false); a deleted voucher still has a head row,
// so the is_deleted filter distinguishes "live head" from "tombstoned head".
// It returns (versionNo, true, nil) for a live head and (0, false, nil) when no
// live head exists (unknown OR tombstoned). The cloud sequencer uses this to
// detect a stale-base conflict before applying an alter/delete: exists=false ->
// CONFLICT "no live head"; exists=true && BaseVersion != versionNo -> stale
// CONFLICT; equal -> apply. The read runs in one company-scoped transaction.
func (s *Service) HeadVersion(ctx context.Context, voucherID uuid.UUID) (int, bool, error) {
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return 0, false, db.ErrNoCompany
	}
	var versionNo int
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT version_no FROM voucher_head
			  WHERE company_id = $1 AND voucher_id = $2 AND is_deleted = false`,
			companyID, voucherID).Scan(&versionNo)
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return 0, false, nil
	case err != nil:
		return 0, false, fmt.Errorf("ledger: read head version: %w", err)
	}
	return versionNo, true, nil
}

// AlterVoucher appends a NEW version (op ALTER) to an existing logical voucher,
// chaining onto the latest version's hash. The prior version is never mutated —
// immutability is preserved and the edit is auditable. It errors with
// ErrVoucherNotFound when the voucher_id has no existing head row, AND when the
// head row is tombstoned (is_deleted): altering a deleted voucher is a 404, not
// a silent resurrection — see the policy note in post().
func (s *Service) AlterVoucher(ctx context.Context, voucherID uuid.UUID, in VoucherInput) (PostResult, error) {
	res, err := s.post(ctx, voucherID, OpAlter, in)
	if err != nil {
		s.raiseFreezeRecon(ctx, voucherID, "ALTER", err)
		return res, err
	}
	return res, nil
}

// raiseFreezeRecon routes a frozen-voucher refusal to the reconciliation queue.
// When post() returns a *FrozenError, the alter/delete was hard-blocked; log the
// attempt so it surfaces in the recon queue. A raise failure is swallowed (the
// FrozenError is the authoritative result returned to the caller regardless) but
// noted, so a transient recon-write hiccup never masks the 409.
func (s *Service) raiseFreezeRecon(ctx context.Context, voucherID uuid.UUID, op string, err error) {
	var fe *FrozenError
	if !errors.As(err, &fe) {
		return
	}
	if _, rerr := s.RaiseReconciliation(ctx, voucherID, op, fe); rerr != nil {
		fmt.Printf("ledger: raise reconciliation for %s voucher %s: %v\n", op, voucherID, rerr)
	}
}

// DeleteVoucher appends an append-only tombstone (op DELETE) to an existing
// logical voucher. In cored's immutable-log model a delete is NOT a row removal:
// it records a new version that reverses the prior current version out of
// ledger_balance and adds NO new postings, so the voucher's net accounting
// contribution becomes exactly zero while every prior version remains in the
// audit log. The tombstone copies the prior version's class / voucher_type /
// voucher_no / date so the edit log identifies what was deleted; it takes no
// VoucherInput because the body is, by definition, empty.
//
// It errors with ErrVoucherNotFound when no voucher_head row exists for the id.
//
// DELETE is IDEMPOTENT: deleting an already-tombstoned voucher writes nothing
// and returns the EXISTING tombstone's PostResult (same voucher_version_id /
// version_no / voucher_no / hash), so a retried delete is indistinguishable from
// the first for the caller. NOTE (pre-existing shape): the head probe and the
// tombstone write run in two separate transactions, so two DELETEs racing
// between them can still stack one extra tombstone — the guarantee is
// retry-safety, not serialisable exactly-once.
func (s *Service) DeleteVoucher(ctx context.Context, voucherID uuid.UUID, createdBy string) (PostResult, error) {
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return PostResult{}, db.ErrNoCompany
	}

	// Read the prior current version's identifying fields so the tombstone can
	// restate them for the edit log. A missing head row means the voucher does
	// not exist (or was never created) — surface ErrVoucherNotFound rather than
	// letting post() mint a phantom version 1.
	var in VoucherInput
	// tombstone is the already-committed tombstone's identity, populated only when
	// the head is ALREADY deleted (the idempotent-re-delete case below).
	var tombstone PostResult
	var alreadyDeleted bool
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var (
			class     string
			vtID      *uuid.UUID
			vNo       *string
			vDate     *string
			isDeleted bool
			versionNo int
			curVerID  uuid.UUID
			curHash   []byte
		)
		err := tx.QueryRow(ctx,
			`SELECT COALESCE(vv.payload->>'class', ''),
			        vv.voucher_type_id, vv.voucher_no,
			        to_char(vv.voucher_date, 'YYYY-MM-DD'),
			        vh.is_deleted, vh.version_no, vv.id, vv.hash
			   FROM voucher_head vh
			   JOIN voucher_version vv ON vv.id = vh.current_version_id
			  WHERE vh.company_id = $1 AND vh.voucher_id = $2`,
			companyID, voucherID).Scan(&class, &vtID, &vNo, &vDate,
			&isDeleted, &versionNo, &curVerID, &curHash)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return ErrVoucherNotFound
		case err != nil:
			return fmt.Errorf("ledger: read voucher for delete: %w", err)
		}
		if isDeleted {
			// DELETE POLICY (WS9): re-deleting an already-tombstoned voucher is an
			// IDEMPOTENT NO-OP. Stacking a second tombstone would append a version
			// that reverses an empty posting set — pure audit-log noise that also
			// makes retry-safety (flaky clients, offline replay) impossible to
			// reason about. Echo the EXISTING tombstone's identity instead so the
			// caller sees the same body it saw for the first DELETE.
			alreadyDeleted = true
			tombstone = PostResult{
				VoucherID:        voucherID,
				VoucherVersionID: curVerID,
				VersionNo:        versionNo,
				Hash:             curHash,
			}
			if vNo != nil {
				tombstone.VoucherNo = *vNo
			}
			return nil
		}
		in.Class = Class(class)
		if vtID != nil {
			in.VoucherTypeID = *vtID
		}
		if vNo != nil {
			in.VoucherNo = *vNo
		}
		if vDate != nil {
			in.VoucherDate = *vDate
		}
		return nil
	})
	if err != nil {
		return PostResult{}, err
	}
	if alreadyDeleted {
		return tombstone, nil
	}

	in.CreatedBy = createdBy
	// The tombstone carries no postings; post()'s OpDelete branch skips the
	// posting-insert + balance-add loop while still reversing the prior version.
	res, err := s.post(ctx, voucherID, OpDelete, in)
	if err != nil {
		s.raiseFreezeRecon(ctx, voucherID, "DELETE", err)
		return res, err
	}
	return res, nil
}

// PostingView is one posting line of a fetched voucher, carrying the REAL
// posting ledger_id (a uuid) alongside the resolved ledger name. This is the
// load-bearing difference from the day book's name-only projection: an alter
// path that re-seeds a form from a fetched voucher needs the actual ledger ids
// so buildVoucherInput's uuid.Parse succeeds (the name-as-uuid bug).
type PostingView struct {
	LedgerID   uuid.UUID
	LedgerName string
	DrCr       string
	Amount     decimal.Decimal
}

// BillAllocationView is one bill-wise allocation of a fetched voucher.
type BillAllocationView struct {
	LedgerID uuid.UUID
	RefName  string
	RefType  string
	DrCr     string
	Amount   decimal.Decimal
	DueDate  string // YYYY-MM-DD or empty
}

// VoucherView is the CURRENT (head) version of one logical voucher, projected
// for a read. Money fields are decimals (the HTTP layer formats them fixed-4);
// nullable GST attributes default to zero. Postings carry real ledger ids.
type VoucherView struct {
	VoucherID       uuid.UUID
	VoucherNo       string
	VoucherDate     string // YYYY-MM-DD or empty
	Class           string
	VoucherTypeID   uuid.UUID
	Party           string
	PartyLedgerID   uuid.UUID
	Narration       string
	VersionNo       int
	Op              string
	PlaceOfSupply   string
	ReverseCharge   bool
	Taxable         decimal.Decimal
	CGST            decimal.Decimal
	SGST            decimal.Decimal
	IGST            decimal.Decimal
	Postings        []PostingView
	BillAllocations []BillAllocationView
	// InventoryLines are the stock lines of the head version, carrying REAL
	// item/godown/batch uuids (not just names) so an alter round-trip —
	// GetVoucher -> rebuild VoucherInput -> AlterVoucher — restates the same
	// movements instead of silently zeroing the stock.
	InventoryLines []InventoryLineView
	// CostAllocations are the head version's cost-centre splits, and they are
	// here for EXACTLY the reason InventoryLines above is.
	//
	// ⛔ AN ALTER RE-STATES: a version says what the voucher is NOW, so an alter
	// that omits the allocations CLEARS them. Without this field the ordinary
	// round trip — open a voucher, change the narration, save — reads a voucher
	// that has no allocations, rebuilds a VoucherInput with none, and silently
	// destroys the departmental split of a cost that is still in the Profit &
	// Loss. Nothing refuses it: zero allocations is a legal state, so the sum
	// trigger has nothing to fire on, and the only trace is a Cost Centre Summary
	// that quietly stops adding up to the P&L.
	//
	// This is the third time this shape has bitten: postings had it, InventoryLines
	// had it and says so, and this is the same bug one field along.
	CostAllocations []CostAllocationView
	// Fulfilments are the head version's ORDER LINKS (T3, migration 0042), and they
	// are here for the FOURTH occurrence of exactly the reason above.
	//
	// ⛔ AN ALTER RE-STATES, AND order_fulfilment KEYS ON (voucher_version_id,
	// line_no). So an alter that omits the links DELETES them — the rows are written
	// under the new version and the old version's rows stop being current. The
	// ordinary round trip (open a delivery note, fix a typo in the narration, save)
	// would read a voucher with no links, rebuild a VoucherInput with none, and
	// silently RE-OPEN an order that has been delivered. Nothing refuses it: zero
	// fulfilments is a legal state, so no trigger fires, and the only trace is an
	// order book that quietly starts reporting a commitment the company already met.
	//
	// This field was ABSENT while no client could create a fulfilment, which made the
	// defect unreachable rather than fixed. The picker on voucher entry is what made
	// it reachable, so it is projected here in the same change.
	Fulfilments []FulfilmentView
	// GSTLines are the head version's PER-LINE GST detail (migration 0016), and
	// they are here for the SIXTH occurrence of the reason CostAllocations and
	// Fulfilments above are — a table `voucher_gst_line` that §4.3's read-back
	// audit never even listed.
	//
	// ⛔ AN ALTER RE-STATES. `voucher_gst_line` keys on voucher_version_id and is
	// written from `in.GST.Lines` inside post()'s transaction, so an alter whose
	// body omits the `gst` block writes NO lines — and, worse than the other five,
	// it also nulls the version's own aggregate taxable_value/cgst/sgst/igst,
	// because `gstActive` is false and those columns come from the same derivation.
	//
	// ⛔⛔ AND THE RATE IS THE PART THAT COULD NOT BE RECOVERED. The aggregate
	// columns record what the derivation PRODUCED; nothing on the read shape
	// recorded what it was DERIVED FROM. A client that rebuilt a GST voucher from
	// GET /vouchers/{id} therefore had no rate and no HSN to send back, so the
	// ordinary round trip — open an invoice, fix the narration, save — re-derived
	// the tax at 0%: the invoice stayed on the books at its net value, left the
	// GSTR-1 b2b section (which reads the aggregate columns) and left the table-12
	// HSN summary (which reads these rows), with nothing refusing it because a
	// zero-rated supply is a legal state.
	//
	// ⚠️ AN ITEM INVOICE IS NOT EXPOSED TO THAT, and the difference is worth
	// knowing: its lines are GENERATED server-side from the stock item master
	// (item_invoice.go), so the rate is re-derived from a master the round trip
	// cannot lose. It is the ACCOUNTING-mode GST voucher — taxable value + a typed
	// rate, no stock — where the rate exists nowhere but the request that carried
	// it. These rows are projected for both, because a read shape that told the
	// truth only for some vouchers is how the next reader concludes it is safe.
	GSTLines []GSTLineView
}

// GSTLineView is one persisted `voucher_gst_line` row of the head version: the
// INPUT to the tax derivation (hsn, taxable value, rate) beside the OUTPUT it
// produced (cgst/sgst/igst).
//
// ⚠️ LineNo IS 1-BASED and is the STORED column, like FulfilmentView.LineNo and
// unlike CostAllocationView.PostingSeq. It indexes nothing else on this view: a
// GST line is not a stock line (an accounting-mode voucher has GST lines and no
// inventory at all), and pairing the two by position would be a guess.
type GSTLineView struct {
	LineNo int
	// HSN is the harmonised code as stated; "" when the client sent none, which
	// is legal and is what takes a sale out of the GSTR-1 table-12 HSN summary.
	HSN string
	// TaxableValue is the base this line was taxed on.
	TaxableValue decimal.Decimal
	// GSTRatePct is the whole-percentage rate, e.g. 18 => "18.0000". ⛔ THIS IS
	// THE FIELD THE ROUND TRIP EXISTED WITHOUT: it is not recoverable from the
	// aggregate columns, because an exempt line and a zero-value line produce the
	// same zero tax.
	GSTRatePct decimal.Decimal
	CGST       decimal.Decimal
	SGST       decimal.Decimal
	IGST       decimal.Decimal
}

// FulfilmentView is one order link of the head version, carrying the real order and
// order-line ids AND the display fields a form needs to re-render the link without
// re-reading the order.
//
// ⚠️ LineNo IS 1-BASED and indexes this same view's InventoryLines slice at
// LineNo-1. It is the STORED stock_movement.line_no, not a re-derivation: unlike
// CostAllocationView.PostingSeq, which has to be re-derived because `posting` has no
// ordinal column, order_fulfilment stores the line number it was keyed against. So
// the round trip GetVoucher -> rebuild -> AlterVoucher restates the same number
// against the same line, and a client sends back what it was given.
//
// ⚠️ OutstandingQty IS NOT HERE, deliberately. It is a property of the ORDER and is
// derived across every current voucher version — including ones this voucher knows
// nothing about — so putting it on a voucher's own view would invite a client to
// treat a figure that was true at read time as a limit. A form that needs it reads
// the order.
type FulfilmentView struct {
	LineNo      int
	OrderID     uuid.UUID
	OrderLineID uuid.UUID
	// The order's human number, e.g. "SO-14" — what an operator recognises.
	OrderNo string
	// The item the ORDER line names. Carried so a form can show it beside the
	// movement's own item, which is how a mis-linked row becomes visible.
	StockItem string
	// How much of the ORDER this line consumed. NOT how much stock moved.
	Qty decimal.Decimal
}

// CostAllocationView is one cost-centre allocation of the head version, carrying
// real uuids AND names.
//
// ⚠️ PostingSeq IS AN INDEX INTO THIS VIEW'S OWN Postings SLICE, and it is NOT
// necessarily the sequence that was signed into the hashed payload. GetVoucher
// orders postings by (dr_cr DESC, amount DESC) to match the day book's
// projection, while the payload's order is the order the client stated. The index
// is therefore RE-DERIVED here against the slice this same read returns, which is
// what makes GetVoucher -> AlterVoucher self-consistent: the caller sends back the
// seq that indexes the postings it was just given.
//
// Do not "fix" this by trying to recover the original payload sequence. `posting`
// has no ordinal column, so it is not recoverable from the rows — and it does not
// need to be, because an ALTER re-states the whole voucher against its own new
// postings.
type CostAllocationView struct {
	PostingSeq       int
	CostCategoryID   uuid.UUID
	CostCategoryName string
	CostCentreID     uuid.UUID
	CostCentreName   string
	Amount           decimal.Decimal
}

// GetVoucher returns the CURRENT (head) version of one logical voucher, with its
// postings (carrying real ledger ids + names), bill allocations, and persisted
// GST attributes. It mirrors the day-book current-version join: it reads through
// voucher_head.current_version_id and excludes deleted heads, so a tombstoned or
// unknown voucher both surface as ErrVoucherNotFound (the HTTP layer maps that to
// 404). The whole read runs in ONE company-scoped transaction so every query is
// pinned to the same tenant GUC.
func (s *Service) GetVoucher(ctx context.Context, voucherID uuid.UUID) (VoucherView, error) {
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return VoucherView{}, db.ErrNoCompany
	}

	var v VoucherView
	v.VoucherID = voucherID
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var (
			versionID uuid.UUID
			vtID      *uuid.UUID
			partyID   *uuid.UUID
		)
		err := tx.QueryRow(ctx,
			`SELECT vv.id, vv.version_no, vv.op::text,
			        COALESCE(vv.payload->>'class', ''),
			        vv.voucher_type_id, vv.party_ledger_id,
			        COALESCE(vv.voucher_no, ''),
			        to_char(vv.voucher_date, 'YYYY-MM-DD'),
			        COALESCE(vv.payload->>'narration', ''),
			        COALESCE(vv.payload->>'party', ''),
			        COALESCE(vv.place_of_supply, ''),
			        vv.is_reverse_charge,
			        COALESCE(vv.taxable_value, 0), COALESCE(vv.cgst, 0),
			        COALESCE(vv.sgst, 0), COALESCE(vv.igst, 0)
			   FROM voucher_head vh
			   JOIN voucher_version vv ON vv.id = vh.current_version_id
			  WHERE vh.company_id = $1 AND vh.voucher_id = $2 AND vh.is_deleted = false`,
			companyID, voucherID).Scan(
			&versionID, &v.VersionNo, &v.Op, &v.Class,
			&vtID, &partyID, &v.VoucherNo, &v.VoucherDate,
			&v.Narration, &v.Party, &v.PlaceOfSupply, &v.ReverseCharge,
			&v.Taxable, &v.CGST, &v.SGST, &v.IGST)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return ErrVoucherNotFound
		case err != nil:
			return fmt.Errorf("ledger: read voucher head: %w", err)
		}
		if vtID != nil {
			v.VoucherTypeID = *vtID
		}
		if partyID != nil {
			v.PartyLedgerID = *partyID
		}

		// Postings of the current version, with real ledger ids + names. Same
		// projection and ordering as the day book's loadPostings.
		// p.id is selected but NOT projected: it is needed only to index the cost
		// allocations below against the position each posting occupies in THIS
		// slice. See CostAllocationView on why that index, and not the signed
		// payload's, is the correct one to hand back.
		postingSeqByID := map[uuid.UUID]int{}
		prows, err := tx.Query(ctx,
			`SELECT p.id, p.ledger_id, COALESCE(l.name, ''), p.dr_cr::text, p.amount
			   FROM posting p
			   LEFT JOIN ledger l ON l.id = p.ledger_id
			  WHERE p.company_id = $1 AND p.voucher_version_id = $2
			  ORDER BY p.dr_cr DESC, p.amount DESC`,
			companyID, versionID)
		if err != nil {
			return fmt.Errorf("ledger: query postings: %w", err)
		}
		defer prows.Close()
		for prows.Next() {
			var p PostingView
			var pid uuid.UUID
			if err := prows.Scan(&pid, &p.LedgerID, &p.LedgerName, &p.DrCr, &p.Amount); err != nil {
				return fmt.Errorf("ledger: scan posting: %w", err)
			}
			postingSeqByID[pid] = len(v.Postings)
			v.Postings = append(v.Postings, p)
		}
		if err := prows.Err(); err != nil {
			return fmt.Errorf("ledger: iterate postings: %w", err)
		}

		// Cost allocations of the current version, with real ids AND names.
		//
		// Joined through posting so the current-version keying is the postings'
		// own — a superseded version's allocations stay in the log and must not be
		// projected, exactly as its postings must not be.
		//
		// Ordered by (posting position, category, centre) via the seq map below
		// rather than in SQL, because the position is a property of the slice this
		// read built and not of any column.
		arows, err := tx.Query(ctx,
			`SELECT a.posting_id, a.cost_category_id, COALESCE(cat.name, ''),
			        a.cost_centre_id, COALESCE(ctr.name, ''), a.amount
			   FROM posting_cost_allocation a
			   JOIN posting p ON p.company_id = a.company_id AND p.id = a.posting_id
			   LEFT JOIN cost_category cat
			     ON cat.company_id = a.company_id AND cat.id = a.cost_category_id
			   LEFT JOIN cost_centre ctr
			     ON ctr.company_id = a.company_id AND ctr.id = a.cost_centre_id
			  WHERE a.company_id = $1 AND p.voucher_version_id = $2
			  ORDER BY cat.name, ctr.name`,
			companyID, versionID)
		if err != nil {
			return fmt.Errorf("ledger: query cost allocations: %w", err)
		}
		defer arows.Close()
		for arows.Next() {
			var (
				a   CostAllocationView
				pid uuid.UUID
			)
			if err := arows.Scan(&pid, &a.CostCategoryID, &a.CostCategoryName,
				&a.CostCentreID, &a.CostCentreName, &a.Amount); err != nil {
				return fmt.Errorf("ledger: scan cost allocation: %w", err)
			}
			seq, ok := postingSeqByID[pid]
			if !ok {
				// The join guarantees the posting belongs to this version, so a
				// miss here means the postings query and this one disagree about
				// what the version contains. Refuse rather than default the
				// sequence: a defaulted 0 would silently re-attach the allocation
				// to whichever posting happened to sort first, and an alter would
				// then write that fiction back as fact.
				return fmt.Errorf(
					"ledger: cost allocation names posting %s, which is not among the %d postings read for version %s — refusing to project an allocation whose posting cannot be identified",
					pid, len(v.Postings), versionID)
			}
			a.PostingSeq = seq
			v.CostAllocations = append(v.CostAllocations, a)
		}
		if err := arows.Err(); err != nil {
			return fmt.Errorf("ledger: iterate cost allocations: %w", err)
		}

		// ORDER LINKS of the current version (T3, migration 0042).
		//
		// Keyed on voucher_version_id, exactly as stock_movement and bill_allocation
		// are, so a superseded version's links stay in the table and are not
		// projected — the same current-version discipline every reader of this table
		// owes.
		//
		// ⛔ THIS READ IS WHAT STOPS AN ALTER SILENTLY RE-OPENING A DELIVERED ORDER.
		// An alter re-states the links from its input, so a form that rebuilt a
		// voucher from a GET which omitted them would send none, and the rows would
		// simply not be written under the new version. Zero fulfilments is a legal
		// state, so nothing refuses it and nothing detects it.
		//
		// The joins are LEFT and the display fields COALESCE'd for the reason every
		// other projection here does it: the LINK is the fact, and a missing master
		// must not make the fact disappear from the round trip.
		frows, err := tx.Query(ctx,
			`SELECT f.line_no, COALESCE(ol.order_id, '00000000-0000-0000-0000-000000000000'::uuid),
			        f.order_line_id, COALESCE(oh.order_no, ''), COALESCE(si.name, ''), f.qty
			   FROM order_fulfilment f
			   LEFT JOIN order_line ol
			     ON ol.company_id = f.company_id AND ol.id = f.order_line_id
			   LEFT JOIN order_head oh
			     ON oh.company_id = ol.company_id AND oh.id = ol.order_id
			   LEFT JOIN stock_item si
			     ON si.company_id = ol.company_id AND si.id = ol.stock_item_id
			  WHERE f.company_id = $1 AND f.voucher_version_id = $2
			  ORDER BY f.line_no, oh.order_no`,
			companyID, versionID)
		if err != nil {
			return fmt.Errorf("ledger: query fulfilments: %w", err)
		}
		defer frows.Close()
		for frows.Next() {
			var f FulfilmentView
			if err := frows.Scan(&f.LineNo, &f.OrderID, &f.OrderLineID,
				&f.OrderNo, &f.StockItem, &f.Qty); err != nil {
				return fmt.Errorf("ledger: scan fulfilment: %w", err)
			}
			v.Fulfilments = append(v.Fulfilments, f)
		}
		if err := frows.Err(); err != nil {
			return fmt.Errorf("ledger: iterate fulfilments: %w", err)
		}

		// Bill allocations of the current version (keyed by voucher_version_id).
		brows, err := tx.Query(ctx,
			`SELECT ledger_id, ref_name, ref_type::text, dr_cr::text, amount,
			        COALESCE(to_char(due_date, 'YYYY-MM-DD'), '')
			   FROM bill_allocation
			  WHERE company_id = $1 AND voucher_version_id = $2
			  ORDER BY created_at`,
			companyID, versionID)
		if err != nil {
			return fmt.Errorf("ledger: query bill allocations: %w", err)
		}
		defer brows.Close()
		for brows.Next() {
			var b BillAllocationView
			if err := brows.Scan(&b.LedgerID, &b.RefName, &b.RefType, &b.DrCr, &b.Amount, &b.DueDate); err != nil {
				return fmt.Errorf("ledger: scan bill allocation: %w", err)
			}
			v.BillAllocations = append(v.BillAllocations, b)
		}
		if err := brows.Err(); err != nil {
			return fmt.Errorf("ledger: iterate bill allocations: %w", err)
		}

		// PER-LINE GST detail of the current version (migration 0016).
		//
		// ⛔ THIS READ IS WHAT MAKES A GST VOUCHER SURVIVE AN ALTER AT ALL. The
		// rows are keyed on voucher_version_id and re-stated from `in.GST.Lines`,
		// and the RATE they were derived at is stored nowhere else — not on
		// voucher_version, which keeps only the amounts the derivation produced. So
		// a form rebuilt from a GET that omitted them had no rate to send back and
		// re-derived the tax at zero. See the GSTLines field on VoucherView.
		//
		// Ordered by the STORED line_no, which is the order post() wrote them in
		// from the request's own `lines` array — so a client that echoes this slice
		// sends the lines back in the order it stated them.
		grows, err := tx.Query(ctx,
			`SELECT line_no, COALESCE(hsn, ''), taxable_value, gst_rate_pct, cgst, sgst, igst
			   FROM voucher_gst_line
			  WHERE company_id = $1 AND voucher_version_id = $2
			  ORDER BY line_no`,
			companyID, versionID)
		if err != nil {
			return fmt.Errorf("ledger: query gst lines: %w", err)
		}
		defer grows.Close()
		for grows.Next() {
			var g GSTLineView
			if err := grows.Scan(&g.LineNo, &g.HSN, &g.TaxableValue,
				&g.GSTRatePct, &g.CGST, &g.SGST, &g.IGST); err != nil {
				return fmt.Errorf("ledger: scan gst line: %w", err)
			}
			v.GSTLines = append(v.GSTLines, g)
		}
		if err := grows.Err(); err != nil {
			return fmt.Errorf("ledger: iterate gst lines: %w", err)
		}

		// Inventory lines of the current version, with real ids. Same
		// current-version keying as the postings above: an altered voucher's
		// superseded movements stay in the log and must not be projected.
		lines, err := loadInventoryLines(ctx, tx, companyID, versionID)
		if err != nil {
			return err
		}
		v.InventoryLines = lines
		return nil
	})
	if err != nil {
		return VoucherView{}, err
	}
	return v, nil
}

// VoucherIDBySourceGUID looks up the logical voucher_id previously imported under
// the given external (Tally) GUID within the company in context. It returns
// (id, true, nil) when a head row carries that source_guid, (uuid.Nil, false,
// nil) when none does, and a non-nil error only on a real query failure. The
// importer calls this BEFORE posting so a re-import of the same export is a
// no-op at the (company, guid) grain. An empty guid never matches (guid-less
// vouchers always post).
func (s *Service) VoucherIDBySourceGUID(ctx context.Context, guid string) (uuid.UUID, bool, error) {
	if guid == "" {
		return uuid.Nil, false, nil
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return uuid.Nil, false, db.ErrNoCompany
	}
	var id uuid.UUID
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`SELECT voucher_id FROM voucher_head
			  WHERE company_id = $1 AND source_guid = $2`,
			companyID, guid).Scan(&id)
		return err
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return uuid.Nil, false, nil
	case err != nil:
		return uuid.Nil, false, fmt.Errorf("ledger: lookup source_guid: %w", err)
	}
	return id, true, nil
}

// PostSalesVoucher posts a NEW F8 Sales voucher. Backward-compatible shim over
// PostVoucher.
func (s *Service) PostSalesVoucher(ctx context.Context, in SalesVoucherInput) (PostResult, error) {
	return s.PostVoucher(ctx, salesToVoucher(in))
}

// AlterSalesVoucher appends a new ALTER version to an existing Sales voucher.
// Backward-compatible shim over AlterVoucher.
func (s *Service) AlterSalesVoucher(ctx context.Context, voucherID uuid.UUID, in SalesVoucherInput) (PostResult, error) {
	return s.AlterVoucher(ctx, voucherID, salesToVoucher(in))
}

func salesToVoucher(in SalesVoucherInput) VoucherInput {
	return VoucherInput{
		Class:         Sales,
		VoucherTypeID: in.VoucherTypeID,
		VoucherNo:     in.VoucherNo,
		VoucherDate:   in.VoucherDate,
		Narration:     in.Narration,
		Party:         in.Party,
		CreatedBy:     in.CreatedBy,
		Postings:      in.Postings,
	}
}

func (s *Service) post(ctx context.Context, voucherID uuid.UUID, op Op, in VoucherInput) (PostResult, error) {
	// Orders are refused BEFORE the generic "invalid class" message, because
	// "invalid voucher class \"Sales Order\"" tells a caller that the name was
	// wrong when in fact the concept is missing. See ErrOrderVouchersNotModelled.
	if in.Class == SalesOrder || in.Class == PurchaseOrder {
		return PostResult{}, ErrOrderVouchersNotModelled
	}
	if !validClasses[in.Class] {
		return PostResult{}, fmt.Errorf("ledger: invalid voucher class %q", in.Class)
	}
	// THE ACCOUNTING DECISION, MADE IN EXACTLY ONE PLACE. It reads a
	// compile-time closed set and NEVER voucher_type.is_accounting: flipping one
	// row in the database must not be able to turn a Sales invoice into a
	// voucher that ships goods and posts nothing. nonaccounting.go's header
	// spells out why the database cannot backstop that one.
	accounting := postsDoubleEntry(in.Class)
	// An ITEM INVOICE (inventory lines + GST params on a GST-bearing class) has
	// its postings and its GST lines GENERATED from the stock lines inside the
	// transaction below — see item_invoice.go. There are no postings to validate
	// yet at this point, so the pre-flight check for one is a different check:
	// that the client did not ALSO send its own postings/gst lines, which would
	// mean two independent statements of what the invoice is worth.
	itemInvoice := isItemInvoice(op, in)

	// A DELETE tombstone carries no postings — it only reverses the prior version
	// out of ledger_balance. CREATE/ALTER must still be balanced double-entry.
	if op != OpDelete {
		switch {
		case itemInvoice:
			if err := validateItemInvoice(in); err != nil {
				return PostResult{}, err
			}
		case !accounting:
			// A Delivery Note / Receipt Note / Stock Journal / Physical Stock.
			// validateStockVoucher is validatePostings' counterpart, not its
			// absence: it refuses postings, bill allocations, GST and TDS
			// outright, which is what keeps "skip the double-entry check" from
			// meaning "these are the one document allowed to be unbalanced".
			if err := validateStockVoucher(in); err != nil {
				return PostResult{}, err
			}
		default:
			if err := validatePostings(in.Postings); err != nil {
				return PostResult{}, err
			}
			if err := validatePartyOrientation(in); err != nil {
				return PostResult{}, err
			}
		}
		// Same guard, same reason: a tombstone's body is empty by definition, so
		// its inventory lines are neither validated nor written.
		if err := validateInventory(in.Inventory); err != nil {
			return PostResult{}, err
		}
	}
	if err := validateBillAllocations(in.BillAllocations); err != nil {
		return PostResult{}, err
	}
	// The supplier's document, checked BEFORE the transaction opens. The schema
	// would refuse a bad eligibility value anyway, but only at INSERT time —
	// after the numbering counter has advanced and the postings are written —
	// and the caller would get a rolled-back transaction naming a constraint
	// instead of the field they got wrong.
	if in.Inward != nil {
		if err := in.Inward.Validate(); err != nil {
			return PostResult{}, err
		}
	}
	// THE COST-CENTRE SPLIT, checked as early as it can honestly be checked.
	//
	// The authoritative check is the DEFERRED constraint trigger
	// cost_allocation_sums_to_posting, which fires at COMMIT. So the operator's
	// version of the same refusal has to come from here (and from the second call
	// inside the transaction below), or a mistyped share costs them a rolled-back
	// transaction carrying a plpgsql exception instead of a sentence naming the
	// axis that is short.
	//
	// ⚠️ IT DOES NOT COST THEM A VOUCHER NUMBER. An earlier version of this comment
	// said a rollback here leaves "a gap in a statutory voucher series"; it does
	// not. There is no sequence in this schema — voucher_number_counter is a table
	// row advanced by INSERT ... ON CONFLICT DO UPDATE on this same transaction
	// (numbering.go:126), so a rollback gives the number back. Corrected rather
	// than deleted because the false version is the plausible one and will be
	// re-invented otherwise.
	//
	// SKIPPED FOR A DELETE because a tombstone carries no postings and no
	// allocations — see the guard below where `allocations` is derived.
	if op != OpDelete && !itemInvoice {
		if err := validateCostAllocations(in.Postings, in.CostAllocations); err != nil {
			return PostResult{}, err
		}
	}

	// THE ORDER LINK, checked as early as it can honestly be checked, for exactly
	// the reasons above: migration 0042's deferred trigger
	// order_fulfilment_within_commitment is the authority and fires at COMMIT, so
	// the operator's version of the same refusal has to come from here (and from
	// the second call inside the transaction below) or a mistyped line number
	// costs them a rolled-back transaction carrying a plpgsql exception.
	//
	// ⚠️ NOT GATED ON !itemInvoice, unlike the cost split immediately above. That
	// guard exists because an item invoice's POSTINGS are generated inside the
	// transaction and a client-supplied posting index would name the wrong leg. An
	// item invoice's INVENTORY is the operator's own grid, so its line numbers are
	// stated by the client on every class and a fulfilment can be held to them —
	// which matters because an invoice fulfilling an order directly, with no
	// separate delivery note, is one of the two ordinary ways an order is closed.
	//
	// SKIPPED FOR A DELETE because a tombstone carries no inventory and no
	// fulfilments — see the guard below where `fulfilments` is derived.
	if op != OpDelete {
		if err := validateFulfilments(in.Inventory, in.Fulfilments); err != nil {
			return PostResult{}, err
		}
	}

	// ⛔⛔ AN ITEM INVOICE MAY NOT CARRY COST ALLOCATIONS IN THIS SLICE, AND THE
	// REASON IS THAT posting_seq WOULD BE A LIE.
	//
	// PostingSeq indexes the voucher's postings. On every other class those are
	// the postings the CLIENT stated, so the index is a thing the client chose and
	// can be held to. On an item invoice they are GENERATED inside the transaction
	// from the stock lines (item_invoice.go), and the order is an internal
	// implementation detail: index 0 is the PARTY leg, 1 is the goods/expense leg,
	// then a tax leg per non-zero component — with zero components emitting no
	// posting at all, so the indices SHIFT with the tax profile of the invoice.
	//
	// A client sending PostingSeq 0 means "the cost". It would get the party leg —
	// a receivable. Allocating the invoice's full party amount to it satisfies the
	// per-category sum perfectly, so nothing refuses it, and the Cost Centre
	// Summary then reports a department as having borne a DEBTOR rather than a
	// cost. Every figure is internally consistent and the report answers a
	// question nobody asked.
	//
	// Refusing is therefore the honest answer for this slice rather than a gap
	// worth papering over: an item invoice CAN be allocated, but only through a
	// contract that names the target by something the client actually chose (the
	// item ledger id), and inventing that contract here — untested, on the class
	// where the money is largest — is how the silent version ships. The message
	// says which classes do work, so an operator is not left guessing.
	if len(in.CostAllocations) > 0 && itemInvoice {
		return PostResult{}, fmt.Errorf(
			"ledger: an item invoice cannot carry cost centre allocations in this build — its postings are generated from the stock lines inside the transaction, so the posting number an allocation names would not be the one it meant and the cost would be attributed to the party rather than to the expense; allocate on a Journal, Payment, Receipt or Contra voucher, or record the invoice with stated postings")
	}

	// Whether this post carries GST derivation (Sales/Purchase with GST params).
	gstActive := in.GST != nil && gstBearing(in.Class)
	// Whether this post carries TDS derivation. A deduction is made by the payer,
	// so it rides Purchase (bill booking), Payment and Journal — never Sales or
	// Receipt. A DELETE tombstone carries no postings and derives nothing.
	tdsActive := in.TDS != nil && op != OpDelete &&
		(in.Class == Purchase || in.Class == Payment || in.Class == Journal)
	// Whether this post carries TCS derivation. Collection is made by the SELLER
	// out of what the buyer pays, so it is the exact mirror: Sales only, never
	// Purchase or Payment.
	//
	// A Credit Note reversing a sale on which tax was collected is NOT handled —
	// it would need a negative collection, migration 0067 forbids one by CHECK,
	// and a return that files a negative line is a question for the Form 143
	// slice rather than something to improvise here. Disclosed in the handoff.
	tcsActive := in.TCS != nil && op != OpDelete && in.Class == Sales

	versionID, err := uuid.NewV7()
	if err != nil {
		return PostResult{}, fmt.Errorf("ledger: new version id: %w", err)
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return PostResult{}, db.ErrNoCompany
	}

	// tax is the derived GST breakup, computed INSIDE the transaction below so the
	// company-state default (read from the same tx) is causally ordered before the
	// derivation that consumes it.
	var tax compliance.TaxResult
	// tdsRes is the derived TDS deduction, computed inside the same transaction
	// so a verification failure rolls the whole post back.
	var tdsRes compliance.TDSResult
	// tdsLiable records that the company WAS required to deduct, which is what
	// decides whether a tds_deduction row is written. It is a separate flag from
	// tdsRes.Applied on purpose: Applied false means "liable, but this payment
	// was below the threshold" and MUST still be recorded, because the aggregate
	// sums amounts paid and Annexure 6 reports sub-threshold payments.
	var tdsLiable bool
	// tdsTaxYear is the tax year of the PAYMENT — the aggregate's key, and NOT
	// the preceding year the deductor-status test reads.
	var tdsTaxYear int
	// tcsRes is the derived collection, and tcsTaxYear the tax year of the SALE.
	// There is no tcsLiable flag to match tdsLiable: no seller-liability test is
	// modelled (see ledger/tcs.go header, point 2), so a `tcs` block on a Sales
	// voucher always derives and always records.
	var tcsRes compliance.TCSResult
	var tcsTaxYear int

	var result PostResult
	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// Server-side, atomic SupplierStateCode default (WS3): when a Sales/Purchase
		// post carries GST but leaves SupplierStateCode empty (e.g. an imported
		// voucher, or a client that does not know the home state), fill it from the
		// company's registered state_code. This read MUST precede deriveAndVerifyGST
		// so the inter/intra (CGST/SGST vs IGST) classification is correct regardless
		// of the caller. A hand-keyed voucher that already set SupplierStateCode is
		// left untouched.
		if gstActive {
			if in.GST.SupplierStateCode == "" {
				var homeState string
				if err := tx.QueryRow(ctx,
					`SELECT state_code FROM company WHERE id = $1`, companyID).Scan(&homeState); err != nil {
					return fmt.Errorf("ledger: read company state_code: %w", err)
				}
				in.GST.SupplierStateCode = homeState
			}

			// THE ITEM INVOICE. Generate the GST lines and the postings from the
			// stock lines, and pin each movement's amount to the same derived
			// taxable value the GST line carries. Everything below this point —
			// the GST verification, the hashed payload, the movement rows, the
			// posting rows — then reads ONE set of numbers, which is what makes
			// migration 0027's tie hold structurally instead of hopefully.
			//
			// The GSTParams are COPIED, not mutated: the caller still holds the
			// original, and stamping server-generated Lines onto it would make a
			// second post of the same input trip the "sent both" refusal.
			if itemInvoice {
				built, err := buildItemInvoice(ctx, tx, companyID, in)
				if err != nil {
					return err
				}
				g := *in.GST
				g.Lines = built.gstLines
				in.GST = &g
				in.Inventory = built.inventory
				in.Postings = built.postings

				// Validate what WE built. The client had no postings to get wrong,
				// so this is a check of the generator: an orientation table that
				// put both legs on the same side, or a tax leg that went missing,
				// shows up here as a domain error instead of as a deferred trigger
				// violation at commit with no line number in it.
				if err := validatePostings(in.Postings); err != nil {
					return fmt.Errorf("ledger: item invoice generated an invalid voucher: %w", err)
				}
			}

			// Derive + verify GST inside the tx, after the state default has been
			// applied, so a derivation/verification error rolls the whole post back.
			t, err := deriveAndVerifyGST(in)
			if err != nil {
				return err
			}
			tax = t
		}
		// LIABILITY, CUMULATION, then verification — in that order, inside the
		// same tx, so any refusal rolls the whole post back.
		//
		// The order is the statute's. s.402(11)/(37) asks whether this company
		// must deduct AT ALL before s.393(1) asks how much; deriving an amount
		// for a company with no obligation and only then discovering it was not
		// owed is how software takes money out of a supplier's payment. And the
		// aggregate must be read before the derivation that consumes it, from
		// this same transaction, so a concurrent post cannot slip a payment in
		// between the sum and the decision it feeds.
		//
		// Like GST this NEVER injects a posting: it refuses a voucher whose TDS
		// leg contradicts the engine.
		if tdsActive {
			// A TDS voucher must be DATED, and is not given today by default the
			// way an undated voucher is for numbering below. The date is the
			// statutory fact of s.393(1)(c) ("credited or paid, whichever is
			// earlier"); it picks the tax year, and the tax year is what the
			// aggregate resets on and what the return is filed for. Stamping
			// "today" onto that would silently file a March payment in the wrong
			// year.
			if in.VoucherDate == "" {
				return ErrTDSDateRequired
			}
			paymentTaxYear, err := finYearOf(in.VoucherDate)
			if err != nil {
				return err
			}
			tdsTaxYear = paymentTaxYear

			status, err := readDeductorStatus(ctx, tx, companyID,
				deductorStatusTaxYear(paymentTaxYear))
			if err != nil {
				return err
			}
			must, err := compliance.MustDeduct(in.TDS.Section, status)
			if err != nil {
				return err
			}
			if !must {
				// No obligation. Refuse any leg that withheld anyway, record
				// nothing, and leave tdsRes zero — there is no deduction to
				// report, and a zero-amount row here would read in the register
				// as "below threshold", which is a different and false claim.
				if err := verifyNoTDSLegPosted(in, in.TDS.Section); err != nil {
					return err
				}
				tdsLiable = false
			} else {
				if in.TDS.DeducteeLedgerID == uuid.Nil {
					return ErrDeducteeUnknown
				}
				agg, err := readTDSAggregate(ctx, tx, companyID,
					in.TDS.DeducteeLedgerID, in.TDS.Section, paymentTaxYear, voucherID)
				if err != nil {
					return err
				}
				r, err := deriveAndVerifyTDS(in, agg)
				if err != nil {
					return err
				}
				tdsRes = r
				tdsLiable = true
			}
		}

		// TCS — the collection-side mirror, on Sales.
		//
		// The s.394(2) declaration is READ HERE, from the same transaction, and
		// never taken from the request body: it is a signed document the
		// collector holds and must forward to the Commissioner, not an
		// entry-time flag an operator can use to zero the tax.
		if tcsActive {
			if in.VoucherDate == "" {
				return ErrTCSDateRequired
			}
			saleTaxYear, err := finYearOf(in.VoucherDate)
			if err != nil {
				return err
			}
			tcsTaxYear = saleTaxYear
			if in.TCS.BuyerLedgerID == uuid.Nil {
				return ErrBuyerUnknown
			}
			// ⚠️ The SALE's own tax year, not the preceding one. The
			// deductor-status read above deliberately uses the preceding year
			// (s.402(37)(b)); a declaration governs the year it was furnished
			// for. Reusing deductorStatusTaxYear here would ignore every
			// declaration ever filed.
			decl, err := readTCSDeclaration(ctx, tx, companyID,
				in.TCS.BuyerLedgerID, in.TCS.Section, saleTaxYear)
			if err != nil {
				return err
			}
			r, err := deriveAndVerifyTCS(in, decl)
			if err != nil {
				return err
			}
			tcsRes = r
		}

		// An ALTER or DELETE re-states the voucher, so its prior deduction and
		// collection rows go first — before the aggregate any LATER post reads,
		// and before this voucher's own re-insert below. A voucher altered to
		// carry no TDS or TCS ends here with no row at all, which is the point.
		if op != OpCreate {
			if err := deleteTDSDeductions(ctx, tx, companyID, voucherID); err != nil {
				return err
			}
			if err := deleteTCSCollections(ctx, tx, companyID, voucherID); err != nil {
				return err
			}
		}

		// PHYSICAL STOCK: turn the counted figures into the correction.
		//
		// This runs INSIDE the transaction for the same reason the item-invoice
		// generator does: it reads the book position, and the book it reads must
		// be the one the movements are about to be written into. After this
		// assignment a Physical Stock voucher is an ENTIRELY ORDINARY stock
		// voucher — the hashed payload records the delta lines, the negative-stock
		// pre-check judges them, an ALTER restates them and a DELETE erases them,
		// with no further special-casing anywhere.
		if in.Class == PhysicalStock && op != OpDelete {
			deltas, err := buildPhysicalStockDeltas(ctx, tx, companyID, voucherID, in.PhysicalCount)
			if err != nil {
				return err
			}
			// Validate what WE built, exactly as the item-invoice branch does. The
			// client had no movements to get wrong, so a zero or negative quantity
			// here is a bug in the differencing, and it should say so rather than
			// arrive as a CHECK (qty > 0) violation with no line number in it.
			if err := validateInventory(deltas); err != nil {
				return fmt.Errorf("ledger: the physical count produced an invalid correction: %w", err)
			}
			in.Inventory = deltas
		}

		// THE DELETE GUARD FOR THE COST-CENTRE SPLIT, single-sourced exactly as
		// `movements` and `inward` are further down, so the hashed payload and the
		// posting_cost_allocation rows are both derived from THIS ONE variable and
		// cannot disagree about what the tombstone carries.
		//
		// A tombstone carries NONE. It reverses the voucher's entire contribution,
		// so it records zero allocations exactly as it records zero postings —
		// otherwise the deleted cost would sit on a department's Cost Centre
		// Summary forever while the voucher itself had vanished from the Profit &
		// Loss, and the two reports would disagree with nothing to show why.
		//
		// An ALTER, by contrast, RE-STATES from the new input, and re-states it
		// against the NEW posting rows this version is about to insert (`posting`
		// is hard immutable, so an ALTER always writes fresh ones). An alter that
		// omits the split clears it — the only reading under which "I put the
		// 60/40 the wrong way round" is correctable at all.
		//
		// ⛔ IT LIVES HERE, ABOVE THE NUMBERING BLOCK, AND NOT BESIDE `movements`.
		// The validation below must run BEFORE allocateVoucherNo advances the
		// (company, type, fin-year) counter, so that a refusal costs the operator
		// nothing but a re-key. This is the same reason InwardDocument.Validate is
		// called before the transaction opens at all.
		//
		// ⚠️ CORRECTED, NOT DELETED: this comment used to justify itself by saying a
		// refusal after that point "leaves a hole in a statutory series that
		// somebody has to explain to an auditor". THAT IS FALSE, and the correct
		// version is already 200 lines above at the first validateCostAllocations
		// call — there is no sequence in this schema. voucher_number_counter is an
		// ordinary table row advanced by INSERT ... ON CONFLICT DO UPDATE on THIS
		// transaction (numbering.go), so a rollback gives the number back. The claim
		// is left visible because it is the plausible one — it is what a Postgres
		// SEQUENCE would do — and it survived here for two releases after being
		// corrected elsewhere, which is exactly how a dead claim gets re-invented.
		// The ordering above is still right; only the reason was wrong.
		allocations := in.CostAllocations
		if op == OpDelete {
			allocations = nil
		}
		// Re-checked here against the FINAL posting list, which for an item invoice
		// is the one the generator above just built rather than anything the client
		// sent. The function is pure and cheap, so checking twice is a self-check
		// rather than waste — the same argument deriveGST records.
		if err := validateCostAllocations(in.Postings, allocations); err != nil {
			return err
		}

		var versionNo int
		var prevHash []byte
		err := tx.QueryRow(ctx,
			`SELECT COALESCE(MAX(version_no), 0) + 1,
			        (SELECT hash FROM voucher_version
			          WHERE company_id = $1 AND voucher_id = $2
			          ORDER BY version_no DESC LIMIT 1)
			   FROM voucher_version
			  WHERE company_id = $1 AND voucher_id = $2`,
			companyID, voucherID).Scan(&versionNo, &prevHash)
		if err != nil {
			return fmt.Errorf("ledger: read version chain: %w", err)
		}

		// For an ALTER/DELETE, reverse the prior CURRENT version's postings out
		// of ledger_balance before the new version's postings are added below.
		// Without this, ledger_balance would double-count an edited voucher. Only
		// the current version of a voucher contributes to the running balance;
		// voucher_head tracks which version that is (the log stays append-only).
		var priorCurrentID uuid.UUID
		var priorDeleted bool
		switch err := tx.QueryRow(ctx,
			`SELECT current_version_id, is_deleted FROM voucher_head
			  WHERE company_id = $1 AND voucher_id = $2`,
			companyID, voucherID).Scan(&priorCurrentID, &priorDeleted); {
		case err == nil:
			// DELETE/ALTER POLICY (WS9). A tombstoned head is NOT a live voucher:
			// GetVoucher already 404s it, so an ALTER must agree rather than
			// silently resurrecting it (the head upsert below sets
			// is_deleted = (op == OpDelete), which would flip a tombstone back to
			// live). Resurrection must be an explicit future "restore" feature,
			// never an accidental side effect of an edit.
			//
			// Scoped to OpAlter ON PURPOSE:
			//   * OpDelete of a tombstoned voucher is an idempotent no-op, and is
			//     short-circuited earlier in DeleteVoucher — it must not 404 here.
			//   * OpCreate re-posting a tombstoned id keeps its existing behaviour
			//     (the sequencer's applyCreate already reports StatusDuplicate for
			//     any existing head, live or tombstoned).
			if op == OpAlter && priorDeleted {
				return ErrVoucherNotFound
			}
			// FREEZE-ON-EXTERNAL-REFERENCE gate. The head exists, so this is an
			// ALTER/DELETE of a real voucher (OpCreate also reaches this arm only
			// when re-creating an existing id, which the guard below excludes).
			// If the voucher is pinned by an IRN / filed GSTR-1, refuse: return
			// the *FrozenError so RunInTx rolls back BEFORE any balance is
			// reversed — nothing is written. The entry points map this to a 409
			// and raise a reconciliation_queue row.
			if op != OpCreate {
				if fe, ferr := frozenRef(ctx, tx, companyID, voucherID); ferr != nil {
					return ferr
				} else if fe != nil {
					return fe
				}
			}
			if _, err := tx.Exec(ctx,
				`UPDATE ledger_balance lb
				    SET debit  = lb.debit  - s.d,
				        credit = lb.credit - s.c
				   FROM (SELECT ledger_id,
				                COALESCE(SUM(amount) FILTER (WHERE dr_cr = 'D'), 0) AS d,
				                COALESCE(SUM(amount) FILTER (WHERE dr_cr = 'C'), 0) AS c
				           FROM posting
				          WHERE company_id = $1 AND voucher_version_id = $2
				          GROUP BY ledger_id) s
				  WHERE lb.company_id = $1 AND lb.ledger_id = s.ledger_id`,
				companyID, priorCurrentID); err != nil {
				return fmt.Errorf("ledger: reverse prior version balance: %w", err)
			}
		case errors.Is(err, pgx.ErrNoRows):
			// No head row exists. For a CREATE this is the first version of a new
			// voucher (nothing to reverse). For an ALTER/DELETE it means the
			// voucher_id is unknown — fail rather than silently mint version 1 and
			// pollute the books with a phantom voucher.
			if op != OpCreate {
				return ErrVoucherNotFound
			}
		default:
			return fmt.Errorf("ledger: read voucher_head: %w", err)
		}

		// Tally auto-numbering. Resolve in.VoucherNo BEFORE building the canonical
		// payload so the number is part of the hashed payload and flows into the
		// denormalised voucher_no column unchanged.
		//
		//   * OpCreate + empty voucher_no + AUTOMATIC-style method => allocate the
		//     next number in the (company, voucher_type, fin_year) series.
		//   * OpCreate + empty voucher_no + MANUAL method          => validation error.
		//   * OpCreate + non-empty voucher_no (override)           => honor verbatim,
		//     do NOT touch the counter.
		//   * OpAlter  + empty voucher_no                          => carry forward the
		//     prior current version's number; NEVER allocate.
		//   * OpDelete                                             => unchanged (the
		//     caller already copied the prior voucher_no).
		// See numbering.go for the full invariant list.
		// A keystroke post (e.g. F8 Sales) carries the class but not a
		// voucher_type_id — the class implies the company's default (system)
		// voucher type, exactly as in Tally. Resolve it so numbering can run and
		// the version row records a proper type.
		//
		// An ALTER restates the BODY, not the identity: every caller (the F8/F9
		// screens, alterSalesVoucher, the seed script, the sync sequencer) sends
		// postings and amounts and leaves VoucherTypeID zero. The type must
		// therefore be CARRIED FORWARD from the version being superseded. Leaving
		// it NULL is not cosmetic: gstr1.go and gstr3b.go reach the voucher class
		// through an INNER JOIN on voucher_type_id, so a NULL silently ELIMINATES
		// the current version of an altered invoice from GSTR-1 and GSTR-3B while
		// it stays fully present in the Trial Balance and P&L — a wrong return
		// with no error anywhere. See the regression tests in
		// internal/compliance/gstr/alter_voucher_type_regression_test.go.
		//
		// OpDelete needs nothing here: DeleteVoucher reads the prior current
		// version's voucher_type_id and passes it in before calling post().
		if in.VoucherTypeID == uuid.Nil {
			switch op {
			case OpCreate:
				vtID, err := defaultVoucherTypeID(ctx, tx, companyID, in.Class)
				if err != nil {
					return err
				}
				in.VoucherTypeID = vtID
			case OpAlter:
				// priorCurrentID is always set for an ALTER: the head-lookup above
				// returns ErrVoucherNotFound when no head row exists.
				var priorType *uuid.UUID
				if err := tx.QueryRow(ctx,
					`SELECT voucher_type_id FROM voucher_version
					  WHERE company_id = $1 AND id = $2`,
					companyID, priorCurrentID).Scan(&priorType); err != nil {
					return fmt.Errorf("ledger: read prior version voucher type: %w", err)
				}
				if priorType != nil {
					in.VoucherTypeID = *priorType
					break
				}
				// The prior version predates this fix (or was imported without a
				// type). Fall back to the class default so an alter REPAIRS such a
				// voucher rather than perpetuating the gap.
				vtID, err := defaultVoucherTypeID(ctx, tx, companyID, in.Class)
				if err != nil {
					return err
				}
				in.VoucherTypeID = vtID
			}
		}

		switch {
		case op == OpCreate && in.VoucherNo == "" && in.VoucherTypeID != uuid.Nil:
			cfg, err := loadNumberingConfig(ctx, tx, in.VoucherTypeID)
			if err != nil {
				return err
			}
			if !allocatesAutomatically(cfg.method) {
				return fmt.Errorf("ledger: manual numbering requires a voucher number")
			}
			vDateForFY := in.VoucherDate
			if vDateForFY == "" {
				// Default an empty date to today server-side so a number can still
				// be allocated; the FY is derived from the voucher date, not today,
				// but a missing date must not block allocation.
				vDateForFY = time.Now().Format("2006-01-02")
				in.VoucherDate = vDateForFY
			}
			finYear, err := finYearOf(vDateForFY)
			if err != nil {
				return err
			}
			n, err := allocateVoucherNo(ctx, tx, companyID, in.VoucherTypeID, finYear)
			if err != nil {
				return err
			}
			in.VoucherNo = formatVoucherNo(cfg.prefix, cfg.suffix, cfg.width, n)
		case op == OpAlter && in.VoucherNo == "":
			// Carry forward the prior current version's number (same SELECT pattern
			// as DeleteVoucher). Never allocate on ALTER. If no head row exists the
			// reverse-prior block above already returned ErrVoucherNotFound.
			var prevNo *string
			err := tx.QueryRow(ctx,
				`SELECT vv.voucher_no
				   FROM voucher_head vh
				   JOIN voucher_version vv ON vv.id = vh.current_version_id
				  WHERE vh.company_id = $1 AND vh.voucher_id = $2`,
				companyID, voucherID).Scan(&prevNo)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				return ErrVoucherNotFound
			case err != nil:
				return fmt.Errorf("ledger: read prior voucher_no for alter: %w", err)
			}
			if prevNo != nil {
				in.VoucherNo = *prevNo
			}
		}

		// THE BILL REFERENCES, single-sourced exactly as `movements`,
		// `fulfilments`, `allocations` and `inward` are, so the hashed payload and
		// the bill_allocation rows are both derived from THIS ONE variable and
		// cannot disagree about what the voucher references.
		//
		// ⛔ IT LIVES HERE, BELOW THE NUMBERING BLOCK, AND THAT IS THE POINT.
		// A reference that OPENS a bill is named after the voucher that opened it
		// (Tally's `New Ref` default), and `in.VoucherNo` does not hold that name
		// until allocateVoucherNo has run four lines above. The client cannot
		// supply it — the number is minted inside this transaction — so a browser
		// sends the reference unnamed and the stamp happens here or nowhere.
		//
		// ⚠️ IT CAN NOW REFUSE, which the original could not, so its position is no
		// longer free: `allocations` must sit ABOVE the numbering for that reason
		// and this must sit BELOW it, because the name it checks does not exist
		// until the number does. Both refusals abort the same transaction, so
		// nothing is written either way. validateBillAllocations already ran before
		// the transaction opened, and AGAINST — the one type that may not be
		// defaulted — was rejected there.
		//
		// A DELETE carries none, for the same reason it carries no postings: a
		// tombstone that restated its references would leave the bill outstanding
		// forever against a voucher that had vanished from the books, with nothing
		// to reconcile the two. No caller can currently reach post() with OpDelete
		// AND references (DeleteVoucher builds a fresh VoucherInput), so this guard
		// is a latch rather than a fix — its four siblings all have one, and the
		// next delete path to be written will not have to rediscover why.
		//
		// ⛔⛔ AND THE STAMP IS A NAMING FUNCTION, NOT AN ASSIGNMENT. It used to be
		// `if RefName == "" { RefName = in.VoucherNo }` on every reference, with no
		// uniqueness of any kind, and that ONE line was two driven defects — see
		// stampBillRefNames for the whole argument and the two collisions.
		billAllocations := in.BillAllocations
		if op == OpDelete {
			billAllocations = nil
		} else {
			billAllocations = stampBillRefNames(
				billAllocations, in.Class, in.VoucherNo, in.VoucherDate, voucherID)
			// Checked AFTER the stamp, because the stamp is what makes the
			// generated names distinct — the only duplicates that can survive to
			// here are ones a CALLER wrote down twice.
			if err := validateDistinctBillOpeners(billAllocations); err != nil {
				return err
			}
		}
		// Checked HERE, not beside validateBillAllocations, because this is the
		// first point at which in.Postings is FINAL — an item invoice's postings
		// were generated above from the stock masters. See the function's comment.
		if err := validateBillAllocationsSumToPosting(in.Postings, billAllocations); err != nil {
			return err
		}

		// THE DELETE GUARD FOR STOCK, single-sourced so the hashed payload and
		// the movement rows can never disagree about what the tombstone carries.
		// A DELETE reverses the voucher's entire contribution — accounting AND
		// inventory — so it records ZERO movements, exactly as it records zero
		// postings. Were the tombstone to restate the lines, the deleted goods
		// would stay on the shelf forever in every stock report while the
		// invoice had vanished from the books.
		movements := in.Inventory
		if op == OpDelete {
			movements = nil
		}

		// THE DELETE GUARD FOR THE ORDER LINK, single-sourced exactly as
		// `movements`, `allocations` and `inward` are, so the hashed payload and
		// the order_fulfilment rows are both derived from THIS ONE variable and
		// cannot disagree about what the tombstone carries.
		//
		// A tombstone carries NONE. Were it to restate the link, the order would
		// report itself fulfilled forever by a voucher that had vanished from the
		// books — a salesperson would see nothing outstanding and the customer
		// would still be waiting, with the two facts held in different tables and
		// nothing to reconcile them.
		//
		// An ALTER RE-STATES from the new input, against the NEW stock_movement
		// rows this version is about to write (movements are immutable, so an
		// ALTER always writes fresh ones). An alter that omits the link clears it
		// and the order returns to outstanding.
		fulfilments := in.Fulfilments
		if op == OpDelete {
			fulfilments = nil
		}
		// Re-checked against the FINAL inventory list, which for a Physical Stock
		// voucher is the one buildPhysicalStockDeltas generated inside this
		// transaction rather than anything the client sent. The function is pure
		// and cheap, so checking twice is a self-check rather than waste — the
		// same argument deriveGST and validateCostAllocations record.
		if err := validateFulfilments(movements, fulfilments); err != nil {
			return err
		}

		// THE SAME DELETE GUARD FOR THE SUPPLIER'S DOCUMENT, single-sourced for
		// the same reason: the hashed payload and the four version columns are
		// both derived from this one variable, so they cannot disagree about what
		// the tombstone carries.
		//
		// A tombstone carries NONE. If it restated the supplier's invoice number,
		// the deleted purchase would keep matching against the 2B forever — the
		// matcher reads the CURRENT version — and the filer would go on claiming
		// input tax credit on a bill they had already removed from the books.
		// That is a claim with no voucher behind it, which is the worst shape an
		// ITC error can take.
		//
		// An ALTER, by contrast, RE-STATES from the new input. A version says
		// what the voucher is NOW, exactly as it does for the taxable value: an
		// alter that omits the supplier document clears it, which is the only
		// reading under which "correct the invoice number I mistyped" works at
		// all.
		var inward *InwardDocument
		if op != OpDelete && in.Inward != nil && !in.Inward.IsZero() {
			inward = in.Inward
		}

		payload := CanonicalPayload{
			VoucherID:     voucherID.String(),
			VoucherTypeID: in.VoucherTypeID.String(),
			VersionNo:     versionNo,
			Op:            string(op),
			Class:         string(in.Class),
			VoucherNo:     in.VoucherNo,
			VoucherDate:   in.VoucherDate,
			Narration:     in.Narration,
			Party:         in.Party,
		}
		for _, p := range in.Postings {
			payload.Postings = append(payload.Postings, CanonicalPosting{
				LedgerID: p.LedgerID.String(),
				DrCr:     string(p.DrCr),
				Amount:   p.Amount.StringFixed(4),
			})
		}
		for _, b := range billAllocations {
			payload.BillAllocations = append(payload.BillAllocations, CanonicalBillAllocation{
				LedgerID: b.LedgerID.String(),
				RefName:  b.RefName,
				RefType:  string(b.RefType),
				DrCr:     string(b.DrCr),
				Amount:   b.Amount.StringFixed(4),
				DueDate:  b.DueDate,
			})
		}

		// Inventory rides in the hashed payload for the same reason postings do:
		// what the edit log SHOWS must be what the edit log SIGNS. A quantity
		// changed in stock_movement without a matching payload change would leave
		// the chain verifying happily over a voucher that no longer says what it
		// did. Ids are stringified (empty for an untracked dimension) and every
		// number is fixed-4 — see CanonicalStockLine.
		for _, l := range movements {
			cl := CanonicalStockLine{
				StockItemID: l.StockItemID.String(),
				Direction:   string(l.Direction),
				Qty:         l.Qty.StringFixed(4),
				Rate:        l.Rate.StringFixed(4),
				DiscountPct: l.DiscountPct.StringFixed(4),
				Amount:      l.Amount.StringFixed(4),
			}
			if l.GodownID != uuid.Nil {
				cl.GodownID = l.GodownID.String()
			}
			if l.BatchID != uuid.Nil {
				cl.BatchID = l.BatchID.String()
			}
			payload.Inventory = append(payload.Inventory, cl)
		}

		// The supplier's document rides in the hashed payload for the same reason
		// postings and stock lines do: what the edit log SHOWS must be what the
		// edit log SIGNS. "Which supplier document is this credit claimed
		// against" is the first question an officer asks, so an unsigned answer
		// is one nobody can defend.
		//
		// The POINTER STAYS NIL when there is no inward detail, and that is
		// load-bearing rather than tidy: `inward,omitempty` on a nil pointer emits
		// no key at all, so every voucher without a supplier document — every
		// sale, and every purchase posted before migration 0032 — marshals to
		// EXACTLY the bytes it marshalled to before this field existed. Anything
		// else retroactively invalidates every hash in every customer's database.
		if inward != nil {
			c := inward.Canonical()
			payload.Inward = &c
		}

		// The cost-centre split rides in the hashed payload because "which
		// department bore this cost" is the figure a manager's budget and a segment
		// disclosure are computed from: left unsigned it would be a number anything
		// with UPDATE could move after the fact, with the chain still certifying
		// the voucher as untouched. It is SIGNED, and therefore tamper-evident.
		//
		// ⚠️ IT IS NOT YET SHOWN. The formula used elsewhere in this function —
		// "what the edit log SHOWS must be what the edit log SIGNS" — is true of
		// postings and stock lines and is NOT yet true here: VersionView
		// (history.go) projects only Postings and BillAllocations, so an auditor
		// opening the history of an altered pay run to see who moved Rs 20,000 from
		// Admin to Sales sees two versions that render identically. The signature
		// catches a change made BEHIND the write path; the history does not yet
		// display a change made THROUGH it.
		//
		// Stated rather than borrowed, because the borrowed sentence was a claim
		// about software that was false the moment it was written — the failure
		// mode HANDOFF §7.1 records. Projecting allocations into VersionView is the
		// fix and is a small, self-contained piece of work; until it lands this is
		// a known gap and not a property.
		//
		// ⛔ THE SLICE STAYS NIL WHEN THERE IS NOTHING TO ALLOCATE, and that is
		// load-bearing rather than tidy: an empty NON-NIL slice built
		// unconditionally would emit `"cost_allocations":[]` and change the hash of
		// every voucher it touched while looking perfectly correct at this call
		// site. Every voucher posted before migration 0040 carries none and must
		// marshal to EXACTLY the bytes it always did, or VerifyChain retroactively
		// reports every book in the field as tampered with — which reads to an
		// operator like an attack. canonicalCostAllocations returns nil for an
		// empty input for precisely this reason;
		// TestAVoucherWithNeitherAllocationsNorFulfilmentsHashesExactlyAsBefore is
		// the test that can see a violation.
		payload.CostAllocations = canonicalCostAllocations(allocations)

		// The order link rides in the hashed payload because "which promise did
		// this shipment keep" is an answer somebody CHOSE at entry, not metadata
		// derived afterwards. Left unsigned it would be a link anything with
		// UPDATE could move after the fact — re-pointing a delivery at a different
		// customer's order — with the chain still certifying the voucher as
		// untouched. It is SIGNED, and therefore tamper-evident.
		//
		// ⛔ THE SLICE STAYS NIL WHEN THERE IS NOTHING LINKED, and that is
		// load-bearing rather than tidy: an empty NON-NIL slice built
		// unconditionally would emit `"fulfilments":[]` and change the hash of
		// every voucher it touched while looking perfectly correct at this call
		// site. Every voucher posted before migration 0042 carries none and must
		// marshal to EXACTLY the bytes it always did, or VerifyChain retroactively
		// reports every book in the field as tampered with — which reads to an
		// operator like an attack. canonicalFulfilments returns nil for an empty
		// input for precisely this reason.
		//
		// ⛔ IT IS ASSIGNED AFTER CostAllocations AND ITS FIELD IS DECLARED AFTER
		// IT. The order of those two payload keys was fixed permanently before
		// either feature was built (see CanonicalPayload) because they arrived in
		// the same session and each independently "had to be last". Nothing may be
		// inserted above either of them, and a third optional field goes below
		// this one.
		payload.Fulfilments = canonicalFulfilments(fulfilments)

		payloadJSON, err := payload.Marshal()
		if err != nil {
			return fmt.Errorf("ledger: marshal payload: %w", err)
		}
		hash := ChainHash(payloadJSON, prevHash)

		// Optional nullable columns for the version row.
		var (
			vtID    any
			partyID any
			vNo     any = nullIfEmpty(in.VoucherNo)
			vDate   any = nullIfEmpty(in.VoucherDate)
			pos     any
			taxable any
			cgst    any
			sgst    any
			igst    any
			rev     = false
		)
		if in.VoucherTypeID != uuid.Nil {
			vtID = in.VoucherTypeID
		}
		if in.PartyLedgerID != uuid.Nil {
			partyID = in.PartyLedgerID
		}
		// The supplier's document, as four nullable columns. Derived from the
		// SAME `inward` variable the hashed payload above was built from — the
		// column and the signature are two renderings of one value, written in
		// one transaction, so no code path exists that can set one without the
		// other. They are columns as WELL as payload because the matcher reads
		// every purchase of a period by (company, party, supplier_invoice_no) and
		// cannot do that through a JSONB scan of the whole book.
		var (
			supplierNo   any
			supplierDate any
			itcElig      any
			itcClause    any
		)
		if inward != nil {
			supplierNo = nullIfEmpty(inward.SupplierInvoiceNo)
			supplierDate = nullIfEmpty(inward.SupplierInvoiceDate)
			itcElig = nullIfEmpty(inward.ITCEligibility)
			itcClause = nullIfEmpty(inward.ITCBlockedClause)
		}
		if gstActive {
			pos = nullIfEmpty(in.GST.PlaceOfSupplyStateCode)
			rev = in.GST.ReverseCharge
			taxable = subTaxable(tax, in)
			cgst = tax.CGST
			sgst = tax.SGST
			igst = tax.IGST
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO voucher_version
			   (id, company_id, voucher_id, version_no, op, prev_hash, hash, payload,
			    created_by, voucher_type_id, party_ledger_id, voucher_no, voucher_date,
			    place_of_supply, is_reverse_charge, taxable_value, cgst, sgst, igst,
			    supplier_invoice_no, supplier_invoice_date,
			    itc_eligibility, itc_blocked_clause)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
			         $14, $15, $16, $17, $18, $19, $20, $21, $22, $23)`,
			versionID, companyID, voucherID, versionNo, string(op), prevHash, hash, payloadJSON,
			in.CreatedBy, vtID, partyID, vNo, vDate,
			pos, rev, taxable, cgst, sgst, igst,
			supplierNo, supplierDate, itcElig, itcClause,
		); err != nil {
			return fmt.Errorf("ledger: insert voucher_version: %w", err)
		}

		// PER-LINE GST detail (migration 0016). The aggregate columns above feed
		// the GSTR-1 b2b/b2cl/b2cs sections; these rows feed the table-12 HSN
		// summary, which rolls up by (hsn, rate) — a LINE-grain key the aggregate
		// cannot express for a mixed-rate invoice. Written from the SAME
		// TaxResult, inside the same transaction, so the two can never disagree.
		//
		// A DELETE writes none: its tombstone carries no lines, exactly as it
		// carries no postings, so the voucher leaves every GST report.
		if gstActive && op != OpDelete {
			for n, lt := range tax.Lines {
				lineID, err := uuid.NewV7()
				if err != nil {
					return fmt.Errorf("ledger: new gst line id: %w", err)
				}
				if _, err := tx.Exec(ctx,
					`INSERT INTO voucher_gst_line
					   (id, company_id, voucher_version_id, line_no,
					    hsn, taxable_value, gst_rate_pct, cgst, sgst, igst)
					 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
					lineID, companyID, versionID, n+1,
					lt.HSN, lt.TaxableValue, lt.GSTRatePct, lt.CGST, lt.SGST, lt.IGST,
				); err != nil {
					return fmt.Errorf("ledger: insert voucher_gst_line: %w", err)
				}
			}
		}

		// STOCK MOVEMENTS — the inventory sibling of the GST-line loop above.
		//
		// Same seam, same transaction, same OpDelete guard, and for the same
		// reason: a movement is an immutable child of THIS voucher version, so it
		// must be impossible for one to exist without the version, or for the
		// version to commit without them. Doing this in a second transaction
		// after post() returns would leave orphan stock behind every time a later
		// posting failed — the books would roll back and the warehouse would not.
		//
		// The negative-stock pre-check runs first so the refusal names the item;
		// the deferred trigger stock_movement_not_negative is the authority and
		// fires at COMMIT regardless of what this layer concluded.
		if err := checkNegativeStock(ctx, tx, companyID, voucherID, movements); err != nil {
			return err
		}
		if err := writeStockMovements(ctx, tx, companyID, versionID, movements); err != nil {
			return err
		}

		// THE ORDER LINK ROWS — the second rendering of the very slice the hashed
		// payload was built from above.
		//
		// ⛔ IMMEDIATELY AFTER THE MOVEMENTS, AND THAT ORDER IS REQUIRED.
		// order_fulfilment_movement_fk references stock_movement
		// (company_id, voucher_version_id, line_no), so the key does not exist
		// until writeStockMovements has run. That FK is what makes line_no a
		// CHECKED fact rather than a number the client asserted — without it a
		// fulfilment could name line 7 of a three-line delivery note and the order
		// book would report a delivery that never happened — and it is also what
		// lets 0042's trigger read the movement's own direction, item and quantity
		// to enforce the three invariants this layer cannot see.
		//
		// Same transaction, so a link can never exist without its movement, nor the
		// voucher commit without its links — the property that makes the signature
		// and the rows two renderings of one value rather than two values that
		// agree today.
		//
		// A DELETE writes none: `fulfilments` was set to nil by the guard above, so
		// the tombstone's payload and its rows are both empty from ONE decision.
		if err := writeFulfilments(ctx, tx, companyID, versionID, fulfilments); err != nil {
			return err
		}

		// Insert postings and incrementally update ledger_balance. A DELETE
		// tombstone inserts ZERO postings: the reverse-prior block above already
		// backed the voucher out of ledger_balance, so adding nothing here leaves
		// its net contribution at exactly zero (the voucher fully disappears from
		// balances and reports while its versions remain in the audit log).
		postings := in.Postings
		if op == OpDelete {
			postings = nil
		}
		// postingIDs is indexed BY THE PAYLOAD'S POSTING SEQUENCE — postings are
		// inserted in payload order, so postingIDs[n] is the row the payload's
		// posting_seq "n" names. This is the entire mapping between the two keys
		// migration 0040 deliberately made different (the payload names a sequence
		// because posting ids are re-minted by every ALTER; the table names a
		// posting_id because its trigger compares against posting.amount and
		// `posting` has no ordinal column).
		//
		// ⛔ CAPTURED HERE, NEVER RE-QUERIED. There is no ORDER BY that recovers
		// the input order: `posting` has no ordinal column, and a voucher may
		// legitimately post twice to the SAME ledger for the SAME amount, so two
		// rows can be indistinguishable in SQL and distinguishable in the request.
		// A re-query that guessed would attribute one department's cost to another
		// with nothing anywhere to detect it.
		postingIDs := make([]uuid.UUID, 0, len(postings))
		for _, p := range postings {
			postingID, err := uuid.NewV7()
			if err != nil {
				return fmt.Errorf("ledger: new posting id: %w", err)
			}
			postingIDs = append(postingIDs, postingID)
			if _, err := tx.Exec(ctx,
				`INSERT INTO posting
				   (id, company_id, voucher_version_id, ledger_id, dr_cr, amount)
				 VALUES ($1, $2, $3, $4, $5, $6)`,
				postingID, companyID, versionID, p.LedgerID, string(p.DrCr), p.Amount,
			); err != nil {
				return fmt.Errorf("ledger: insert posting: %w", err)
			}

			debitDelta, creditDelta := decimal.Zero, decimal.Zero
			if p.DrCr == Debit {
				debitDelta = p.Amount
			} else {
				creditDelta = p.Amount
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO ledger_balance (company_id, ledger_id, debit, credit)
				 VALUES ($1, $2, $3, $4)
				 ON CONFLICT (company_id, ledger_id) DO UPDATE
				   SET debit  = ledger_balance.debit  + EXCLUDED.debit,
				       credit = ledger_balance.credit + EXCLUDED.credit`,
				companyID, p.LedgerID, debitDelta, creditDelta,
			); err != nil {
				return fmt.Errorf("ledger: update ledger_balance: %w", err)
			}
		}

		// THE COST-CENTRE SPLIT ROWS — the second rendering of the very slice the
		// hashed payload was built from a few lines above.
		//
		// Immediately after the posting loop because the composite FK
		// posting_cost_allocation_posting_fk references posting (company_id, id) and
		// the ids only exist once those INSERTs have run. Same transaction, so an
		// allocation can never exist without its posting, nor the voucher commit
		// without its allocations — the property that makes the signature and the
		// rows two renderings of one value rather than two values that agree today.
		//
		// A DELETE writes none: `allocations` was set to nil by the guard above, so
		// the tombstone's payload and its rows are both empty from ONE decision.
		//
		// The deferred trigger cost_allocation_sums_to_posting is the authority and
		// fires at COMMIT regardless of what validateCostAllocations concluded up
		// there, exactly as stock_movement_not_negative does for inventory.
		if err := writeCostAllocations(ctx, tx, companyID, postingIDs, allocations); err != nil {
			return err
		}

		// Insert bill-wise allocations (immutable; versioned with the voucher).
		// From the single source derived above — NOT in.BillAllocations, whose
		// opening references are still unnamed at this point.
		for _, b := range billAllocations {
			allocID, err := uuid.NewV7()
			if err != nil {
				return fmt.Errorf("ledger: new bill allocation id: %w", err)
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO bill_allocation
				   (id, company_id, voucher_version_id, ledger_id, ref_name, ref_type,
				    dr_cr, amount, due_date)
				 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
				allocID, companyID, versionID, b.LedgerID, b.RefName, string(b.RefType),
				string(b.DrCr), b.Amount, nullIfEmpty(b.DueDate),
			); err != nil {
				return fmt.Errorf("ledger: insert bill_allocation: %w", err)
			}
		}

		// Move the mutable head pointer to this new version. ledger_balance now
		// reflects exactly the current version of every voucher.
		if _, err := tx.Exec(ctx,
			`INSERT INTO voucher_head
			   (company_id, voucher_id, current_version_id, version_no, op, is_deleted, voucher_date, source_guid)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			 ON CONFLICT (company_id, voucher_id) DO UPDATE
			   SET current_version_id = EXCLUDED.current_version_id,
			       version_no         = EXCLUDED.version_no,
			       op                 = EXCLUDED.op,
			       is_deleted         = EXCLUDED.is_deleted,
			       voucher_date       = EXCLUDED.voucher_date,
			       source_guid        = COALESCE(EXCLUDED.source_guid, voucher_head.source_guid),
			       updated_at         = now()`,
			companyID, voucherID, versionID, versionNo, string(op), op == OpDelete, vDate,
			nullIfEmpty(in.SourceGUID),
		); err != nil {
			return fmt.Errorf("ledger: upsert voucher_head: %w", err)
		}

		// THE DEDUCTION ROW — the thing whose absence made the register and the
		// return impossible to build.
		//
		// It lands AFTER the voucher_head upsert because migration 0050's
		// composite FK references voucher_head (company_id, voucher_id), and a
		// CREATE has no head row until the statement above runs. Same
		// transaction, so a deduction can never exist without its voucher, nor
		// the voucher commit without its deduction.
		if tdsLiable {
			if err := writeTDSDeduction(ctx, tx, companyID, voucherID, in, tdsRes,
				tdsTaxYear, in.VoucherDate, in.CreatedBy); err != nil {
				return err
			}
		}
		// The collection row, for the same reason and at the same seam. Written
		// even when the tax is ZERO: a sale exempted by a s.394(2) declaration is
		// the one that most needs a record, because the exemption rests on a
		// document the department can ask to see.
		if tcsActive {
			if err := writeTCSCollection(ctx, tx, companyID, voucherID, in, tcsRes,
				tcsTaxYear, in.VoucherDate, in.CreatedBy); err != nil {
				return err
			}
		}

		result = PostResult{
			VoucherID:        voucherID,
			VoucherVersionID: versionID,
			VersionNo:        versionNo,
			Hash:             hash,
			VoucherNo:        in.VoucherNo,
			Tax:              tax,
			TDS:              tdsRes,
			TCS:              tcsRes,
		}
		return nil
	})
	if err != nil {
		return PostResult{}, err
	}
	return result, nil
}

// deriveGST runs the PURE compliance engine over the params, defaulting the
// ruleset version. It is split out of deriveAndVerifyGST so the item-invoice
// generator can obtain the tax it must post BEFORE any postings exist to verify
// against — the verification half then runs, unchanged, over what the generator
// built. Pure and cheap, so deriving twice is a self-check, not waste.
func deriveGST(g *GSTParams) (compliance.TaxResult, error) {
	version := g.RulesetVersion
	if version == "" {
		version = "gst-2024-09"
	}
	rs, err := compliance.LoadRuleSet(version)
	if err != nil {
		return compliance.TaxResult{}, fmt.Errorf("ledger: gst ruleset: %w", err)
	}
	tax, err := compliance.TaxDerive(compliance.TaxInput{
		SupplierStateCode:      g.SupplierStateCode,
		PlaceOfSupplyStateCode: g.PlaceOfSupplyStateCode,
		ReverseCharge:          g.ReverseCharge,
		Exempt:                 g.Exempt,
		Lines:                  g.Lines,
	}, rs)
	if err != nil {
		return compliance.TaxResult{}, fmt.Errorf("ledger: derive gst: %w", err)
	}
	return tax, nil
}

// deriveAndVerifyGST runs the pure compliance engine and, when tax-ledger ids
// are supplied, verifies the posted tax amounts equal the derived split. It
// returns the derived result for stamping on the version.
//
// NOTE THE HOLE THIS DOES NOT COVER, because it is the reason item_invoice.go
// exists: `posted` sums posting amounts WITHOUT looking at dr_cr, so a fully
// REVERSED voucher verifies here exactly as a correct one does. And nothing
// here looks at the taxable BASE at all — g.Lines is taken as given. On an item
// invoice both gaps are closed upstream, by generating the base from the stock
// lines and the sides from invoiceOrientation.
func deriveAndVerifyGST(in VoucherInput) (compliance.TaxResult, error) {
	g := in.GST
	tax, err := deriveGST(g)
	if err != nil {
		return compliance.TaxResult{}, err
	}

	// Cross-check the posted tax lines against the derived split when the caller
	// identified the tax ledgers. This guarantees the books match the engine.
	posted := func(id uuid.UUID) decimal.Decimal {
		sum := decimal.Zero
		if id == uuid.Nil {
			return sum
		}
		for _, p := range in.Postings {
			if p.LedgerID == id {
				sum = sum.Add(p.Amount)
			}
		}
		return sum
	}
	check := func(name string, want decimal.Decimal, id uuid.UUID) error {
		if id == uuid.Nil {
			return nil
		}
		if got := posted(id); !got.Equal(want) {
			return fmt.Errorf("ledger: %s posting %s != derived %s",
				name, got.StringFixed(4), want.StringFixed(4))
		}
		return nil
	}
	if err := check("CGST", tax.CGST, g.CGSTLedgerID); err != nil {
		return compliance.TaxResult{}, err
	}
	if err := check("SGST", tax.SGST, g.SGSTLedgerID); err != nil {
		return compliance.TaxResult{}, err
	}
	if err := check("IGST", tax.IGST, g.IGSTLedgerID); err != nil {
		return compliance.TaxResult{}, err
	}
	return tax, nil
}

// subTaxable returns the taxable total (invoice total minus tax) for the version
// row. Under reverse charge the invoice total already equals the taxable total.
func subTaxable(tax compliance.TaxResult, in VoucherInput) decimal.Decimal {
	if in.GST != nil && in.GST.ReverseCharge {
		return tax.InvoiceTotal
	}
	return tax.InvoiceTotal.Sub(tax.TotalTax)
}

// nullIfEmpty returns nil for an empty string so it inserts SQL NULL.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// validatePostings ensures there is at least one posting on each side and that
// debits equal credits. This is an early, friendly check; the deferred DB
// trigger is the authoritative backstop at commit.
func validatePostings(ps []PostingInput) error {
	if len(ps) < 2 {
		return errors.New("ledger: a voucher needs at least two postings")
	}
	debit, credit := decimal.Zero, decimal.Zero
	for _, p := range ps {
		if p.LedgerID == uuid.Nil {
			return errors.New("ledger: posting ledger id is required")
		}
		if p.Amount.IsNegative() {
			return errors.New("ledger: posting amount must be non-negative")
		}
		switch p.DrCr {
		case Debit:
			debit = debit.Add(p.Amount)
		case Credit:
			credit = credit.Add(p.Amount)
		default:
			return fmt.Errorf("ledger: invalid dr_cr %q", p.DrCr)
		}
	}
	if !debit.Equal(credit) {
		return fmt.Errorf("ledger: double-entry violation: debit %s != credit %s",
			debit.StringFixed(4), credit.StringFixed(4))
	}
	return nil
}

// ErrPartyOrientation refuses an ACCOUNTING-MODE invoice whose party leg is on
// the wrong side for its class.
//
// ===========================================================================
// ⛔⛔ THIS EXISTS BECAUSE THE SERVER ANSWERED 201 TO A FULLY REVERSED INVOICE,
// AND EVERY OTHER CHECK IN THIS FILE IS BLIND TO IT BY CONSTRUCTION
// ===========================================================================
// Found by keying a Purchase in a browser and reading `posting` in Postgres
// (HANDOFF §5.5). The client built every GST voucher with the SALES shape, so:
//
//	Purchase (BROWSER-KEYED)  Dr supplier / Cr Purchases / Cr OUTPUT tax
//	Purchase (CORRECT)        Cr supplier / Dr Purchases / Dr INPUT tax
//
// A payable became a receivable, and the company declared output tax it does
// not owe while destroying the input credit it could have claimed.
//
// ⛔ NOTHING REFUSED IT, AND NOT BY OVERSIGHT:
//   - validatePostings only checks SUM(Dr) == SUM(Cr), and BOTH orientations
//     balance exactly;
//   - deriveAndVerifyGST sums posting amounts WITHOUT reading dr_cr, and says so
//     in its own comment ten lines up: "a fully REVERSED voucher verifies here
//     exactly as a correct one does";
//   - the deferred DB trigger checks the same balance the first one does.
//
// So the orientation was checked NOWHERE for a client-supplied posting set. It
// was checked for ITEM invoices, because those are generated from
// orientationFor — which is the same table this now consults. The two paths
// disagreed about whether the question mattered, and the accounting path was
// the one a browser actually used.
//
// ⚠️ AND WHY THE FIX BELONGS HERE RATHER THAN ONLY IN THE CLIENT: the client is
// one caller. This is a public HTTP API with an importer, a draft-accept path
// and a mock behind it, and "the browser sends it correctly now" is not a
// property of the books. A wrong number that returns 201 is indistinguishable
// from a right one at every later layer.
var ErrPartyOrientation = errors.New("ledger: party posting is on the wrong side for this voucher class")

// validatePartyOrientation checks the party leg of an accounting-mode invoice
// against the class's orientation.
//
// ⚠️ IT CHECKS THE PARTY LEG ONLY, AND THAT IS A DELIBERATE FLOOR RATHER THAN A
// COMPLETE MODEL. The party leg is the one posting an accounting-mode invoice
// always has, always names (PartyLedgerID), and cannot get right by accident —
// and its direction alone decides whether the document is a receivable or a
// payable. Checking the revenue/expense and tax legs too would mean this
// function deciding which supplied ledger is "the income one", which it cannot
// know: an accounting-mode voucher may legitimately split across several.
//
// ⛔ SO A VOUCHER THAT PASSES THIS IS NOT CERTIFIED CORRECT. It has only been
// refused the one error that turns a payable into a receivable.
func validatePartyOrientation(in VoucherInput) error {
	// Only the four GST-bearing invoice classes have an orientation. Receipts,
	// payments, contras and journals legitimately put the party on either side —
	// that is what they are FOR — and orientationFor errors for them.
	o, err := orientationFor(in.Class)
	if err != nil {
		return nil
	}
	// No party named: nothing to check against. An accounting-mode invoice
	// SHOULD name one, but requiring it here would reject documents this
	// function has no complaint about, and the gap is stated rather than
	// enforced by a check that is really about something else.
	if in.PartyLedgerID == uuid.Nil {
		return nil
	}
	for _, p := range in.Postings {
		if p.LedgerID != in.PartyLedgerID {
			continue
		}
		if p.DrCr != o.party {
			return fmt.Errorf(
				"%w: a %s posts the party %s, and this voucher posts it %s. "+
					"A %s is the document that %s — posting it the other way round "+
					"turns a %s into a %s, and it BALANCES, so no total will ever "+
					"disagree with it",
				ErrPartyOrientation, in.Class, sideWord(o.party), sideWord(p.DrCr),
				in.Class, classPurpose(in.Class),
				owedWord(o.party), owedWord(p.DrCr),
			)
		}
		return nil
	}
	// The party is named but takes no posting. Legal — a cash sale booked
	// against a bank ledger names no debtor leg — and not this check's business.
	return nil
}

func sideWord(d DrCr) string {
	if d == Debit {
		return "Dr"
	}
	return "Cr"
}

// owedWord names what a party leg on this side MEANS, so the refusal says what
// went wrong in the operator's language rather than in the schema's.
func owedWord(d DrCr) string {
	if d == Debit {
		return "receivable (money owed TO this company)"
	}
	return "payable (money this company OWES)"
}

func classPurpose(c Class) string {
	switch c {
	case Sales:
		return "bills a customer"
	case Purchase:
		return "records what a supplier billed us"
	case CreditNote:
		return "gives value BACK to a customer"
	case DebitNote:
		return "claims value back FROM a supplier"
	}
	return "records a trade"
}

// validateBillAllocations checks each allocation is structurally sound.
//
// ⛔ AN EMPTY RefName IS LEGAL FOR EVERY TYPE EXCEPT AGAINST, AND THE ASYMMETRY
// IS THE WHOLE POINT.
//
// The voucher number is allocated by allocateVoucherNo INSIDE the post
// transaction, so a client keying a sales invoice in a browser CANNOT know it:
// it does not exist until after this validation has run. A reference that opens
// a bill therefore leaves the name empty and post() names it from the allocated
// number — Tally's own `New Ref` default. That is the ONLY way a browser-keyed
// invoice can ever reach the Outstandings report, and for the whole life of this
// feature it could not, because no client could name the bill.
//
// ⚠️ THE GENERATED NAME IS NOT THE BARE NUMBER — see stampBillRefNames. A bare
// number names one voucher per TYPE per YEAR, so it collided; the name carries
// the class and the financial year with it.
//
// AGAINST is the exception because it is the one type whose name must MATCH
// something that already exists. reporting.Outstandings groups by
// (ledger_id, ref_name) and ignores ref_type entirely, so an AGAINST carrying a
// defaulted name would not settle the bill it meant to settle — it would open a
// SECOND bill under this voucher's own number, leaving the original fully
// outstanding and a negative row beside it. Both figures would be wrong and the
// report would still foot. A settlement must say what it settles.
func validateBillAllocations(bs []BillAllocationInput) error {
	for _, b := range bs {
		if b.LedgerID == uuid.Nil {
			return errors.New("ledger: bill allocation ledger id is required")
		}
		if b.RefName == "" && b.RefType == RefAgainst {
			return errors.New("ledger: an AGAINST bill allocation must name the bill it settles")
		}
		if !validRefTypes[b.RefType] {
			return fmt.Errorf("ledger: invalid bill ref type %q", b.RefType)
		}
		if b.DrCr != Debit && b.DrCr != Credit {
			return fmt.Errorf("ledger: invalid bill allocation dr_cr %q", b.DrCr)
		}
		if b.Amount.IsNegative() {
			return errors.New("ledger: bill allocation amount must be non-negative")
		}
	}
	return nil
}

// onAccountRefName is the ONE bucket every ON_ACCOUNT reference on a party is
// filed under, and it is deliberately NOT derived from any voucher number.
//
// ⛔ AN ON-ACCOUNT AMOUNT IS BY DEFINITION TIED TO NO BILL. Tally shows it as its
// own line in a party's bill-wise breakup, not as a bill; naming it after the
// voucher that carried it invents a bill that the operator never opened and that
// nothing will ever settle. Every on-account amount on a party therefore nets
// into this single bucket, which is exactly how the operator reads it: "money
// from this customer that is not applied to anything".
const onAccountRefName = "On Account"

// stampBillRefNames names every reference that arrived WITHOUT a name, and it is
// the whole of the fix for two defects that were driven in a browser.
//
// ⛔⛔ WHAT IT REPLACES, AND WHY THE OLD ONE LINE WAS TWO DEFECTS.
//
// This used to be `if RefName == "" { RefName = in.VoucherNo }`, applied to every
// unnamed reference with no uniqueness check of any kind. reporting.Outstandings
// groups by (ledger_id, ref_name) and IGNORES ref_type — the name IS the bill's
// identity — so a name that is not unique per party is not a bill reference at
// all, it is a collision waiting for a party unlucky enough to have two:
//
//  1. WITHIN ONE VOUCHER. An 11,800 invoice split 5,900 at 30 days and 5,900 at
//     60 days sent two unnamed NEW references; both were stamped "7"; the report
//     grouped them into ONE bill of 11,800 aged at MIN(due_date) — i.e. the whole
//     invoice aged at the 30-day date, and the 60-day instalment ceased to exist
//     as a separate thing anybody could chase.
//
//  2. ACROSS VOUCHERS. Voucher numbers are allocated by a counter keyed on
//     (company, voucher_type, financial year) — see numbering.go — so a bare "7"
//     names one voucher PER TYPE PER YEAR, not one voucher. A Receipt #7 carrying
//     an ON_ACCOUNT or ADVANCE reference was stamped "7" and silently NETTED
//     against Sales invoice "7" on the same party: the invoice showed as part
//     paid, the money showed as applied, and both figures were wrong while the
//     report still footed. The same collision exists between a Sales #7 and a
//     Credit Note #7 (the raise-mode picker defaults a note's reference to NEW),
//     and between Sales #7 of one financial year and Sales #7 of the next — which
//     printing/printingdb states in its own words: "a serial number repeats every
//     financial year, so 'INV/0001' names one invoice per year, not one invoice".
//
// ⛔ SO THE GENERATED NAME CARRIES EVERY PART OF THE COUNTER'S KEY: the class, the
// financial year, and the number — "Sales/2026-27/7" — plus an index when ONE
// voucher opens more than one bill on the SAME ledger. That is unique by
// construction, which is the only thing that works here: bill_allocation has no
// unique index to lean on (duplicate voucher numbers are legal by design,
// numbering.go's "Prevent Duplicates" is a future flag), so a lookup-then-name
// scheme would still race two concurrent posts into the same bill.
//
// ⚠️ IT IS DETERMINISTIC, AND THAT IS LOAD-BEARING RATHER THAN TIDY. An ALTER
// re-states the references; a client that re-sends them unnamed (the F8 form
// re-sends the STORED names, but the API accepts unnamed ones) must land on the
// SAME names, or every edit would open a fresh bill and leave the original
// outstanding forever. A uuid or a timestamp here would do exactly that.
//
// ⚠️ AND THE NAME IS NO LONGER THE VOUCHER NUMBER, which two callers cared about:
// a Credit Note prints "Against Invoice No.: <ref_name>" (printingdb/document.go),
// so the ORIGINAL INVOICE's number should be read from the voucher that opened the
// bill rather than from the bill's name — that lookup already exists there
// (againstInvoiceDate) and needs to return the number beside the date. It was
// already wrong for any hand-named bill ("PO-4471/partial"); this makes it common.
func stampBillRefNames(
	bs []BillAllocationInput, class Class, voucherNo, voucherDate string, voucherID uuid.UUID,
) []BillAllocationInput {
	out := make([]BillAllocationInput, len(bs))
	copy(out, bs)

	base := billRefBase(class, voucherNo, voucherDate, voucherID)

	// How many bills does this voucher OPEN on each ledger without naming them?
	// Counted first so the single-bill case — virtually every invoice — keeps the
	// clean, unsuffixed name and only a genuine split pays for the index.
	//
	// AGAINST is never in here: validateBillAllocations refused an unnamed one
	// before the transaction opened, because a settlement must say what it
	// settles. ON_ACCOUNT is never in here either: it opens nothing.
	openers := make(map[uuid.UUID]int)
	for _, b := range out {
		if b.RefName == "" && b.RefType != RefOnAccount {
			openers[b.LedgerID]++
		}
	}

	seen := make(map[uuid.UUID]int)
	for i := range out {
		if out[i].RefName != "" {
			continue
		}
		if out[i].RefType == RefOnAccount {
			out[i].RefName = onAccountRefName
			continue
		}
		seen[out[i].LedgerID]++
		if openers[out[i].LedgerID] > 1 {
			out[i].RefName = fmt.Sprintf("%s-%d", base, seen[out[i].LedgerID])
			continue
		}
		out[i].RefName = base
	}
	return out
}

// billRefBase builds the part of a generated bill name that identifies the
// VOUCHER: "<class>/<financial year>/<number>", e.g. "Sales/2026-27/7".
//
// Each segment is there because the one inside it is not unique without it — the
// number is unique only within (voucher type, financial year), which is precisely
// the key allocateVoucherNo counts on. The class stands in for the voucher type
// because it is always present on the input (post() validates it for every op,
// including ALTER, where the type id may only be resolvable from the prior
// version) and because it is what the operator sees on the screen.
//
// ⚠️ THE FALLBACKS ARE NOT DECORATION. A voucher with no date (the column is
// nullable) has no financial year, and a company with no system voucher type for
// the class gets NO number at all — defaultVoucherTypeID returns nil without an
// error and numbering is skipped. The old stamp turned that second case into an
// EMPTY ref_name, which groups every such reference on a party into one nameless
// bill. The voucher id closes it: it is unique, it is stable across versions
// (voucher_head keyed on voucher_id), and it is short enough to read out.
func billRefBase(class Class, voucherNo, voucherDate string, voucherID uuid.UUID) string {
	parts := make([]string, 0, 3)
	if class != "" {
		parts = append(parts, string(class))
	}
	if fy, err := finYearOf(voucherDate); err == nil {
		parts = append(parts, finYearLabel(fy))
	}
	switch {
	case voucherNo != "":
		parts = append(parts, voucherNo)
	default:
		// No number was ever allocated. Name the bill after the voucher itself so
		// it is still one identifiable bill rather than a shared blank.
		parts = append(parts, "#"+voucherID.String()[:8])
	}
	return strings.Join(parts, "/")
}

// finYearLabel renders an Indian financial year from its start year: 2026 ->
// "2026-27". Same convention as tds.go's messages and as the prefixes Tally
// stores ("INV/2026-27/").
func finYearLabel(startYear int) string {
	return fmt.Sprintf("%d-%02d", startYear, (startYear+1)%100)
}

// validateDistinctBillOpeners refuses a voucher that OPENS two bills of the same
// name on one ledger.
//
// ⛔ WHY A REFUSAL AND NOT A SILENT MERGE. reporting.Outstandings groups by
// (ledger_id, ref_name), so two openers sharing a name are not two bills: they
// are one bill of the summed amount, aged at MIN(due_date), and NOTHING anywhere
// says so — the voucher balances, the sum-to-posting check passes, and the ageing
// report quietly loses an instalment. That was defect D10, driven in a browser.
//
// ⚠️ IT GUARDS THE DOOR THE STAMP CANNOT REACH. Generated names are distinct by
// construction (stampBillRefNames indexes them), so this can only fire on names a
// CALLER supplied — the sync sequencer, the seed script, an API client, or a
// future form with a name box. The keyboard cannot reach it today, which is why
// it is a latch rather than the fix; the fix is the stamp.
//
// AGAINST and ON_ACCOUNT are deliberately EXCLUDED, and neither is an oversight:
// two AGAINST rows naming one bill are two part-settlements of it in one voucher,
// and every ON_ACCOUNT row is meant to land in the same per-party bucket.
func validateDistinctBillOpeners(bs []BillAllocationInput) error {
	type opener struct {
		ledger uuid.UUID
		name   string
	}
	seen := make(map[opener]bool, len(bs))
	for _, b := range bs {
		if b.RefType != RefNew && b.RefType != RefAdvance {
			continue
		}
		k := opener{b.LedgerID, b.RefName}
		if seen[k] {
			return fmt.Errorf(
				"ledger: this voucher opens two bills named %q on the same ledger — "+
					"the Outstandings report identifies a bill by its NAME, so the two would "+
					"become ONE bill of their summed amount, aged at the EARLIER due date, "+
					"with the later instalment gone from the report and nothing to say so",
				b.RefName)
		}
		seen[k] = true
	}
	return nil
}

// validateBillAllocationsSumToPosting enforces the invariant BillAllocationInput's
// own doc comment has always declared and NOTHING has ever checked: the
// references on a ledger must account for exactly what that ledger posts.
//
// ⛔ WHY IT MATTERS MORE THAN IT LOOKS. reporting.Outstandings is built from
// bill_allocation; the Balance Sheet's Sundry Debtors is built from
// ledger_balance, which is built from postings. If the two disagree by a paisa
// the company has two reports over the same books that quietly differ, and
// NOTHING anywhere reconciles them — the voucher still balances, every total
// still foots, and the difference surfaces months later as a customer disputing
// a statement. "The engine records what it is given" was true and was the bug.
//
// ⚠️ IT IS CHECKED INSIDE THE TRANSACTION, AGAINST THE FINAL POSTING LIST, for
// the same reason validateCostAllocations is re-checked there: on an ITEM
// INVOICE the postings do not exist when the request arrives — the server
// generates them from the stock masters, and the client's on-screen total is a
// PREVIEW derived from rates it does not own. A master whose rate changed
// between the preview and the post is exactly the case that would otherwise
// write a bill for the wrong money, and it is invisible to every other check.
//
// The comparison is SIGNED (Dr positive, Cr negative) on both sides, so a ledger
// carrying both a settlement and an on-account remainder nets naturally instead
// of needing a special case. It is exact to the paisa: a tolerance here is how a
// real error hides for a year.
func validateBillAllocationsSumToPosting(postings []PostingInput, bs []BillAllocationInput) error {
	if len(bs) == 0 {
		return nil
	}
	signed := func(dc DrCr, amt decimal.Decimal) decimal.Decimal {
		if dc == Credit {
			return amt.Neg()
		}
		return amt
	}
	allocByLedger := make(map[uuid.UUID]decimal.Decimal)
	order := make([]uuid.UUID, 0, len(bs))
	for _, b := range bs {
		if _, seen := allocByLedger[b.LedgerID]; !seen {
			order = append(order, b.LedgerID)
		}
		allocByLedger[b.LedgerID] = allocByLedger[b.LedgerID].Add(signed(b.DrCr, b.Amount))
	}
	postByLedger := make(map[uuid.UUID]decimal.Decimal)
	for _, p := range postings {
		postByLedger[p.LedgerID] = postByLedger[p.LedgerID].Add(signed(p.DrCr, p.Amount))
	}
	for _, lid := range order {
		got, want := allocByLedger[lid], postByLedger[lid]
		if got.Equal(want) {
			continue
		}
		if want.IsZero() {
			return fmt.Errorf(
				"ledger: bill references total %s against a ledger this voucher does not post to at all — "+
					"a bill cannot be raised or settled on a line that is not in the entry",
				got.StringFixed(4))
		}
		return fmt.Errorf(
			"ledger: bill references on this ledger total %s but it posts %s — "+
				"the difference is %s, and leaving it would make the Outstandings report "+
				"disagree with Sundry Debtors on the Balance Sheet",
			got.StringFixed(4), want.StringFixed(4), want.Sub(got).StringFixed(4))
	}
	return nil
}
