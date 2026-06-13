// Package ledger owns the atomic, double-entry voucher-post path — the
// load-bearing transactional spine of cored. It is the SOLE writer of
// voucher_version, posting, and ledger_balance, so other modules participate in
// a post only through this package's interface.
//
// A post is atomic: in ONE database transaction (scoped to the company GUC by
// the db layer) it appends an immutable, hash-chained voucher_version, inserts
// the balanced postings, and incrementally updates ledger_balance. The
// per-voucher BLAKE3 hash chain makes the log tamper-evident and doubles as the
// MCA Rule 11(g) edit log: an alter or delete appends a NEW version that chains
// onto the previous one's hash rather than mutating it.
package ledger

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

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

// PostingInput is a single double-entry line to post.
type PostingInput struct {
	LedgerID uuid.UUID
	DrCr     DrCr
	Amount   decimal.Decimal
}

// SalesVoucherInput describes an F8 Sales voucher to post.
type SalesVoucherInput struct {
	VoucherTypeID uuid.UUID
	VoucherNo     string
	VoucherDate   string // ISO date (YYYY-MM-DD)
	Narration     string
	Party         string
	CreatedBy     string
	// Postings must balance: SUM(debit) == SUM(credit). The deferred DB trigger
	// enforces this at commit as a final backstop.
	Postings []PostingInput
}

// PostResult identifies a committed voucher version.
type PostResult struct {
	VoucherID        uuid.UUID
	VoucherVersionID uuid.UUID
	VersionNo        int
	Hash             []byte
}

// Service is the ledger application service.
type Service struct {
	runner TxRunner
}

// NewService constructs a ledger Service over the given transaction runner.
func NewService(runner TxRunner) *Service {
	return &Service{runner: runner}
}

// PostSalesVoucher atomically posts a NEW F8 Sales voucher (version 1, op
// CREATE). It validates double-entry up front, appends the hash-chained
// voucher_version, inserts the postings, and updates ledger_balance — all in a
// single company-scoped transaction.
func (s *Service) PostSalesVoucher(ctx context.Context, in SalesVoucherInput) (PostResult, error) {
	voucherID, err := uuid.NewV7()
	if err != nil {
		return PostResult{}, fmt.Errorf("ledger: new voucher id: %w", err)
	}
	return s.post(ctx, voucherID, OpCreate, in)
}

// AlterSalesVoucher appends a NEW version (op ALTER) to an existing logical
// voucher, chaining onto the latest version's hash. The prior version is never
// mutated — immutability is preserved and the edit is auditable.
func (s *Service) AlterSalesVoucher(ctx context.Context, voucherID uuid.UUID, in SalesVoucherInput) (PostResult, error) {
	return s.post(ctx, voucherID, OpAlter, in)
}

func (s *Service) post(ctx context.Context, voucherID uuid.UUID, op Op, in SalesVoucherInput) (PostResult, error) {
	if err := validatePostings(in.Postings); err != nil {
		return PostResult{}, err
	}

	versionID, err := uuid.NewV7()
	if err != nil {
		return PostResult{}, fmt.Errorf("ledger: new version id: %w", err)
	}

	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return PostResult{}, db.ErrNoCompany
	}

	var result PostResult
	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// Determine the next version number and the previous hash for the chain.
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

		payload := CanonicalPayload{
			VoucherID:     voucherID.String(),
			VoucherTypeID: in.VoucherTypeID.String(),
			VersionNo:     versionNo,
			Op:            string(op),
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

		payloadJSON, err := payload.Marshal()
		if err != nil {
			return fmt.Errorf("ledger: marshal payload: %w", err)
		}
		hash := ChainHash(payloadJSON, prevHash)

		// Append the immutable voucher version.
		if _, err := tx.Exec(ctx,
			`INSERT INTO voucher_version
			   (id, company_id, voucher_id, version_no, op, prev_hash, hash, payload, created_by)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			versionID, companyID, voucherID, versionNo, string(op), prevHash, hash, payloadJSON, in.CreatedBy,
		); err != nil {
			return fmt.Errorf("ledger: insert voucher_version: %w", err)
		}

		// Insert postings and incrementally update ledger_balance.
		for _, p := range in.Postings {
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

		result = PostResult{
			VoucherID:        voucherID,
			VoucherVersionID: versionID,
			VersionNo:        versionNo,
			Hash:             hash,
		}
		return nil
	})
	if err != nil {
		return PostResult{}, err
	}
	return result, nil
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
