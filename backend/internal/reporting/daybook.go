package reporting

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// VoucherEntry is one posted voucher version as it appears in the Day Book or a
// ledger drill-down. It is the terminal drill-down level of every report tree
// (RESEARCH sec 8: "no report is a dead end" — every line reaches its voucher).
type VoucherEntry struct {
	VoucherID        uuid.UUID
	VoucherVersionID uuid.UUID
	VersionNo        int
	Op               string // CREATE / ALTER / DELETE
	Class            string // accounting class label from the payload
	VoucherTypeName  string
	VoucherNo        string
	Date             string // ISO date (YYYY-MM-DD)
	Narration        string
	Party            string
	// Debit/Credit are the total debit and credit of the version's postings
	// (they are equal by double-entry; both are surfaced as the Day Book shows
	// the voucher amount).
	Debit  decimal.Decimal
	Credit decimal.Decimal
	// Postings are the version's individual ledger lines (the innermost detail).
	Postings []VoucherPosting
}

// VoucherPosting is a single ledger line within a voucher entry.
type VoucherPosting struct {
	LedgerID   uuid.UUID
	LedgerName string
	DrCr       string // "D" or "C"
	Amount     decimal.Decimal
}

// DayBook is the chronological list of all posted voucher versions in a period
// (Tally's Gateway > Day Book, RESEARCH sec 8). Each entry drills to its
// postings. Because balances are maintained incrementally per version, every
// version (including ALTER restatements) is listed as its own dated event.
type DayBook struct {
	From    string
	To      string
	Entries []VoucherEntry
}

// DayBook returns every voucher version posted between from and to (inclusive,
// ISO dates). Passing empty from/to returns all dates. Entries are ordered by
// voucher date then version creation, and each carries its postings for
// drill-down.
func (s *Service) DayBook(ctx context.Context, companyID uuid.UUID, from, to string) (DayBook, error) {
	if err := scope(ctx, companyID); err != nil {
		return DayBook{}, err
	}
	out := DayBook{From: from, To: to}

	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT vv.id, vv.voucher_id, vv.version_no, vv.op::text,
			        COALESCE(vv.payload->>'class', ''),
			        COALESCE(vt.name, ''), COALESCE(vv.voucher_no, ''),
			        to_char(vv.voucher_date, 'YYYY-MM-DD'),
			        COALESCE(vv.payload->>'narration', ''),
			        COALESCE(vv.payload->>'party', '')
			   FROM voucher_version vv
			   JOIN voucher_head vh
			     ON vh.company_id = vv.company_id
			    AND vh.voucher_id = vv.voucher_id
			    AND vh.current_version_id = vv.id
			   LEFT JOIN voucher_type vt ON vt.id = vv.voucher_type_id
			  WHERE vh.is_deleted = false
			    AND ($1 = '' OR vv.voucher_date >= $1::date)
			    AND ($2 = '' OR vv.voucher_date <= $2::date)
			  ORDER BY vv.voucher_date NULLS LAST, vv.created_at, vv.version_no`,
			from, to)
		if err != nil {
			return fmt.Errorf("reporting: query day book: %w", err)
		}
		defer rows.Close()

		var entries []VoucherEntry
		for rows.Next() {
			var (
				e    VoucherEntry
				date *string
			)
			if err := rows.Scan(&e.VoucherVersionID, &e.VoucherID, &e.VersionNo, &e.Op,
				&e.Class, &e.VoucherTypeName, &e.VoucherNo, &date,
				&e.Narration, &e.Party); err != nil {
				return fmt.Errorf("reporting: scan day book entry: %w", err)
			}
			if date != nil {
				e.Date = *date
			}
			entries = append(entries, e)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("reporting: iterate day book: %w", err)
		}

		// Attach postings (drill-down detail) to each entry.
		for i := range entries {
			ps, dr, cr, err := loadPostings(ctx, tx, entries[i].VoucherVersionID)
			if err != nil {
				return err
			}
			entries[i].Postings = ps
			entries[i].Debit = dr
			entries[i].Credit = cr
		}
		out.Entries = entries
		return nil
	})
	if err != nil {
		return DayBook{}, err
	}
	return out, nil
}

// loadPostings reads the postings of one voucher version (with ledger names) and
// returns them plus the debit and credit totals.
func loadPostings(ctx context.Context, tx pgx.Tx, versionID uuid.UUID) ([]VoucherPosting, decimal.Decimal, decimal.Decimal, error) {
	rows, err := tx.Query(ctx,
		`SELECT p.ledger_id, COALESCE(l.name, ''), p.dr_cr::text, p.amount
		   FROM posting p
		   LEFT JOIN ledger l ON l.id = p.ledger_id
		  WHERE p.voucher_version_id = $1
		  ORDER BY p.dr_cr DESC, p.amount DESC`,
		versionID)
	if err != nil {
		return nil, decimal.Zero, decimal.Zero, fmt.Errorf("reporting: query postings: %w", err)
	}
	defer rows.Close()

	var (
		ps     []VoucherPosting
		debit  = decimal.Zero
		credit = decimal.Zero
	)
	for rows.Next() {
		var p VoucherPosting
		if err := rows.Scan(&p.LedgerID, &p.LedgerName, &p.DrCr, &p.Amount); err != nil {
			return nil, decimal.Zero, decimal.Zero, fmt.Errorf("reporting: scan posting: %w", err)
		}
		if p.DrCr == "D" {
			debit = debit.Add(p.Amount)
		} else {
			credit = credit.Add(p.Amount)
		}
		ps = append(ps, p)
	}
	if err := rows.Err(); err != nil {
		return nil, decimal.Zero, decimal.Zero, fmt.Errorf("reporting: iterate postings: %w", err)
	}
	return ps, debit, credit, nil
}

// LedgerVouchers returns every voucher version that posted to the given ledger,
// in chronological order, for the optional period. This is the group->ledger->
// voucher drill-down leaf used from the Balance Sheet, P&L and Trial Balance.
func (s *Service) LedgerVouchers(ctx context.Context, companyID, ledgerID uuid.UUID, from, to string) ([]VoucherEntry, error) {
	if err := scope(ctx, companyID); err != nil {
		return nil, err
	}
	var entries []VoucherEntry
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT vv.id, vv.voucher_id, vv.version_no, vv.op::text,
			        COALESCE(vv.payload->>'class', ''),
			        COALESCE(vt.name, ''), COALESCE(vv.voucher_no, ''),
			        to_char(vv.voucher_date, 'YYYY-MM-DD'),
			        COALESCE(vv.payload->>'narration', ''),
			        COALESCE(vv.payload->>'party', '')
			   FROM voucher_version vv
			   JOIN voucher_head vh
			     ON vh.company_id = vv.company_id
			    AND vh.voucher_id = vv.voucher_id
			    AND vh.current_version_id = vv.id
			   LEFT JOIN voucher_type vt ON vt.id = vv.voucher_type_id
			  WHERE vh.is_deleted = false
			    AND EXISTS (
			            SELECT 1 FROM posting p
			             WHERE p.voucher_version_id = vv.id AND p.ledger_id = $1)
			    AND ($2 = '' OR vv.voucher_date >= $2::date)
			    AND ($3 = '' OR vv.voucher_date <= $3::date)
			  ORDER BY vv.voucher_date NULLS LAST, vv.created_at, vv.version_no`,
			ledgerID, from, to)
		if err != nil {
			return fmt.Errorf("reporting: query ledger vouchers: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var (
				e    VoucherEntry
				date *string
			)
			if err := rows.Scan(&e.VoucherVersionID, &e.VoucherID, &e.VersionNo, &e.Op,
				&e.Class, &e.VoucherTypeName, &e.VoucherNo, &date,
				&e.Narration, &e.Party); err != nil {
				return fmt.Errorf("reporting: scan ledger voucher: %w", err)
			}
			if date != nil {
				e.Date = *date
			}
			entries = append(entries, e)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("reporting: iterate ledger vouchers: %w", err)
		}
		for i := range entries {
			ps, dr, cr, err := loadPostings(ctx, tx, entries[i].VoucherVersionID)
			if err != nil {
				return err
			}
			entries[i].Postings = ps
			entries[i].Debit = dr
			entries[i].Credit = cr
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}
