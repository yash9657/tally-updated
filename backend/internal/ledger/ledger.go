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
)

// validClasses is the set of accepted voucher classes.
var validClasses = map[Class]bool{
	Contra: true, Payment: true, Receipt: true,
	Journal: true, Sales: true, Purchase: true,
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
	// reporting and bill-wise attribution; optional.
	PartyLedgerID uuid.UUID
	VoucherNo     string
	VoucherDate   string // ISO date (YYYY-MM-DD)
	Narration     string
	Party         string // party name (denormalised label)
	CreatedBy     string
	// Postings must balance: SUM(debit) == SUM(credit). The deferred DB trigger
	// enforces this at commit as a final backstop.
	Postings []PostingInput
	// BillAllocations are optional bill-wise references on party ledgers.
	BillAllocations []BillAllocationInput
	// GST, when non-nil and Class is Sales or Purchase, drives auto-derivation
	// and verification of the tax lines.
	GST *GSTParams
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

// AlterVoucher appends a NEW version (op ALTER) to an existing logical voucher,
// chaining onto the latest version's hash. The prior version is never mutated —
// immutability is preserved and the edit is auditable. It errors with
// ErrVoucherNotFound when the voucher_id has no existing head row.
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
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var (
			class string
			vtID  *uuid.UUID
			vNo   *string
			vDate *string
		)
		err := tx.QueryRow(ctx,
			`SELECT COALESCE(vv.payload->>'class', ''),
			        vv.voucher_type_id, vv.voucher_no,
			        to_char(vv.voucher_date, 'YYYY-MM-DD')
			   FROM voucher_head vh
			   JOIN voucher_version vv ON vv.id = vh.current_version_id
			  WHERE vh.company_id = $1 AND vh.voucher_id = $2`,
			companyID, voucherID).Scan(&class, &vtID, &vNo, &vDate)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return ErrVoucherNotFound
		case err != nil:
			return fmt.Errorf("ledger: read voucher for delete: %w", err)
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
		prows, err := tx.Query(ctx,
			`SELECT p.ledger_id, COALESCE(l.name, ''), p.dr_cr::text, p.amount
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
			if err := prows.Scan(&p.LedgerID, &p.LedgerName, &p.DrCr, &p.Amount); err != nil {
				return fmt.Errorf("ledger: scan posting: %w", err)
			}
			v.Postings = append(v.Postings, p)
		}
		if err := prows.Err(); err != nil {
			return fmt.Errorf("ledger: iterate postings: %w", err)
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
	if !validClasses[in.Class] {
		return PostResult{}, fmt.Errorf("ledger: invalid voucher class %q", in.Class)
	}
	// A DELETE tombstone carries no postings — it only reverses the prior version
	// out of ledger_balance. CREATE/ALTER must still be balanced double-entry.
	if op != OpDelete {
		if err := validatePostings(in.Postings); err != nil {
			return PostResult{}, err
		}
	}
	if err := validateBillAllocations(in.BillAllocations); err != nil {
		return PostResult{}, err
	}

	// Whether this post carries GST derivation (Sales/Purchase with GST params).
	gstActive := in.GST != nil && (in.Class == Sales || in.Class == Purchase)

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
			// Derive + verify GST inside the tx, after the state default has been
			// applied, so a derivation/verification error rolls the whole post back.
			t, err := deriveAndVerifyGST(in)
			if err != nil {
				return err
			}
			tax = t
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
		switch err := tx.QueryRow(ctx,
			`SELECT current_version_id FROM voucher_head
			  WHERE company_id = $1 AND voucher_id = $2`,
			companyID, voucherID).Scan(&priorCurrentID); {
		case err == nil:
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
		// the version row records a proper type. ALTER/DELETE keep whatever the
		// prior version carried (set above), so only resolve on CREATE.
		if op == OpCreate && in.VoucherTypeID == uuid.Nil {
			vtID, err := defaultVoucherTypeID(ctx, tx, companyID, in.Class)
			if err != nil {
				return err
			}
			in.VoucherTypeID = vtID
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
		for _, b := range in.BillAllocations {
			payload.BillAllocations = append(payload.BillAllocations, CanonicalBillAllocation{
				LedgerID: b.LedgerID.String(),
				RefName:  b.RefName,
				RefType:  string(b.RefType),
				DrCr:     string(b.DrCr),
				Amount:   b.Amount.StringFixed(4),
				DueDate:  b.DueDate,
			})
		}

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
			    place_of_supply, is_reverse_charge, taxable_value, cgst, sgst, igst)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
			         $14, $15, $16, $17, $18, $19)`,
			versionID, companyID, voucherID, versionNo, string(op), prevHash, hash, payloadJSON,
			in.CreatedBy, vtID, partyID, vNo, vDate,
			pos, rev, taxable, cgst, sgst, igst,
		); err != nil {
			return fmt.Errorf("ledger: insert voucher_version: %w", err)
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
		for _, p := range postings {
			postingID, err := uuid.NewV7()
			if err != nil {
				return fmt.Errorf("ledger: new posting id: %w", err)
			}
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

		// Insert bill-wise allocations (immutable; versioned with the voucher).
		for _, b := range in.BillAllocations {
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

		result = PostResult{
			VoucherID:        voucherID,
			VoucherVersionID: versionID,
			VersionNo:        versionNo,
			Hash:             hash,
			VoucherNo:        in.VoucherNo,
			Tax:              tax,
		}
		return nil
	})
	if err != nil {
		return PostResult{}, err
	}
	return result, nil
}

// deriveAndVerifyGST runs the pure compliance engine and, when tax-ledger ids
// are supplied, verifies the posted tax amounts equal the derived split. It
// returns the derived result for stamping on the version.
func deriveAndVerifyGST(in VoucherInput) (compliance.TaxResult, error) {
	g := in.GST
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

// validateBillAllocations checks each allocation is structurally sound.
func validateBillAllocations(bs []BillAllocationInput) error {
	for _, b := range bs {
		if b.LedgerID == uuid.Nil {
			return errors.New("ledger: bill allocation ledger id is required")
		}
		if b.RefName == "" {
			return errors.New("ledger: bill allocation ref name is required")
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
