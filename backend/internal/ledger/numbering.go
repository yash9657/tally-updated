package ledger

// Tally server-side auto-numbering (WORKSTREAM A).
//
// INVARIANTS enforced here and at the post() call site:
//
//   - ALLOCATE ONLY ON CREATE WITH EMPTY voucher_no. The allocator is invoked
//     exclusively on the OpCreate branch of post(), and only when the client
//     sent an empty voucher_no on an AUTOMATIC-style type. ALTER and DELETE
//     NEVER allocate.
//
//   - MONOTONIC, GAP-ON-DELETE. The per-(company, voucher_type, fin_year)
//     counter only ever advances when WE allocate. It is never decremented or
//     backfilled, so a deleted voucher's number is not reused — the series is
//     allocation-ordered, not necessarily contiguous. This mirrors Tally.
//
//   - MANUAL OVERRIDE DOES NOT BUMP THE COUNTER. On a CREATE with a non-empty
//     voucher_no on an AUTOMATIC type, we honor the supplied value verbatim and
//     do NOT call the allocator. The auto series therefore continues at "prior
//     auto max + 1"; it does not jump to supplied+1. (Tally-faithful; if product
//     later wants "continue from highest used number" the allocator would take
//     MAX(next_no, supplied+1) on override — a deliberate, documented change.)
//
//   - ALTER CARRIES THE NUMBER FORWARD. An OpAlter with an empty voucher_no
//     reads the prior current version's voucher_no in the same tx and reuses it;
//     it never allocates.
//
//   - METHODS. AUTOMATIC (default), AUTOMATIC_MANUAL (Tally "Automatic (Manual
//     Override)") and MULTIUSER_AUTO ("Multi-user Auto", a synonym of AUTOMATIC
//     because the Postgres allocator is already concurrency-safe) all route
//     through the allocator when voucher_no is empty. MANUAL never allocates;
//     an empty voucher_no on a MANUAL type is a validation error.
//
//   - FY SCOPE. The counter is scoped to the Indian financial year (Apr–Mar)
//     derived from voucher_date, stored as the integer start-year (e.g. both
//     2026-07-xx and 2027-02-xx => 2026, label "2026-27"). A new FY restarts the
//     series at 1. The human prefix/suffix are taken verbatim from voucher_type
//     (Tally stores e.g. "INV/2026-27/" as the literal prefix); we do not
//     interpolate FY tokens here.
//
//   - DUPLICATES are allowed this increment (no UNIQUE constraint): the immutable
//     multi-version log shares one number across versions, gaps/overrides are
//     legal, and "Prevent Duplicates" is a future per-voucher_type opt-in flag.
//     The allocator's atomicity guarantees auto-allocated numbers are distinct;
//     only manual overrides could collide, matching Tally-without-Prevent-Dupes.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Numbering method values stored in voucher_type.numbering_method.
const (
	// methodAutomatic: server allocates when voucher_no is empty.
	methodAutomatic = "AUTOMATIC"
	// methodAutomaticManual: Tally "Automatic (Manual Override)" — server
	// allocates when voucher_no empty but accepts a client value without complaint.
	methodAutomaticManual = "AUTOMATIC_MANUAL"
	// methodMultiUserAuto: Tally "Multi-user Auto"; treated as AUTOMATIC because
	// our Postgres allocator is already concurrency-safe.
	methodMultiUserAuto = "MULTIUSER_AUTO"
	// methodManual: server never allocates; an empty voucher_no is an error.
	methodManual = "MANUAL"
)

// allocatesAutomatically reports whether the given numbering_method routes an
// empty voucher_no through the server allocator.
func allocatesAutomatically(method string) bool {
	switch method {
	case methodAutomatic, methodAutomaticManual, methodMultiUserAuto:
		return true
	default:
		return false
	}
}

// numberingConfig is the per-voucher_type numbering configuration read in-tx.
type numberingConfig struct {
	method string
	prefix string
	suffix string
	width  *int
}

// loadNumberingConfig fetches the numbering_method / prefix / suffix / width for
// a voucher_type inside the post() tx (one row, RLS-scoped). prefix/suffix are
// returned as empty strings when NULL; width is nil when NULL (no zero padding).
func loadNumberingConfig(ctx context.Context, tx pgx.Tx, voucherTypeID uuid.UUID) (numberingConfig, error) {
	var (
		cfg    numberingConfig
		prefix *string
		suffix *string
	)
	err := tx.QueryRow(ctx,
		`SELECT numbering_method, prefix, suffix, width
		   FROM voucher_type WHERE id = $1`,
		voucherTypeID).Scan(&cfg.method, &prefix, &suffix, &cfg.width)
	if err != nil {
		return numberingConfig{}, fmt.Errorf("ledger: load numbering config: %w", err)
	}
	if prefix != nil {
		cfg.prefix = *prefix
	}
	if suffix != nil {
		cfg.suffix = *suffix
	}
	return cfg, nil
}

// allocateVoucherNo atomically consumes the next number in the
// (company, voucher_type, fin_year) series and returns it. The single
// INSERT ... ON CONFLICT DO UPDATE ... RETURNING is atomic and serializes
// concurrent allocations of the same series via the PK row lock, so two
// concurrent CREATEs get distinct consecutive numbers with no application-level
// locking. It MUST be called inside post()'s tx so the increment commits (or
// rolls back) atomically with the voucher.
func allocateVoucherNo(ctx context.Context, tx pgx.Tx, companyID, voucherTypeID uuid.UUID, finYear int) (int64, error) {
	var n int64
	err := tx.QueryRow(ctx,
		`INSERT INTO voucher_number_counter
		   (company_id, voucher_type_id, fin_year, next_no)
		 VALUES ($1, $2, $3, 2)
		 ON CONFLICT (company_id, voucher_type_id, fin_year) DO UPDATE
		   SET next_no = voucher_number_counter.next_no + 1,
		       updated_at = now()
		 RETURNING next_no - 1`,
		companyID, voucherTypeID, finYear).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("ledger: allocate voucher no: %w", err)
	}
	return n, nil
}

// defaultVoucherTypeID resolves the company's system voucher type for an
// accounting class (e.g. Sales -> the seeded "Sales" type), so a keystroke post
// (F8 Sales) need not carry an explicit voucher_type_id — the class implies the
// default type exactly as it does in Tally. parent_class is stored upper-cased
// with underscores ('SALES', 'CREDIT_NOTE'); the ledger Class is title-case and
// may contain a space ("Sales", "Credit Note"), hence the replace() as well as
// the upper(). Returns uuid.Nil with no error when the company has no system
// type for the class (caller skips numbering rather than failing the post).
func defaultVoucherTypeID(ctx context.Context, tx pgx.Tx, companyID uuid.UUID, class Class) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx,
		`SELECT id FROM voucher_type
		  WHERE company_id = $1
		    AND parent_class = upper(replace($2, ' ', '_'))
		    AND is_system = true
		  ORDER BY created_at
		  LIMIT 1`,
		companyID, string(class)).Scan(&id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return uuid.Nil, nil
	case err != nil:
		return uuid.Nil, fmt.Errorf("ledger: resolve default voucher type for %q: %w", class, err)
	}
	return id, nil
}

// formatVoucherNo renders the display number: prefix || running-number || suffix.
// width!=nil zero-pads the number to that width; width==nil emits the bare
// integer (Tally's default of no padding).
func formatVoucherNo(prefix, suffix string, width *int, n int64) string {
	var num string
	if width != nil && *width > 0 {
		num = fmt.Sprintf("%0*d", *width, n)
	} else {
		num = strconv.FormatInt(n, 10)
	}
	return prefix + num + suffix
}

// finYearOf derives the Indian financial-year start-year (Apr–Mar) from an
// ISO date (YYYY-MM-DD): if month>=4 the FY start-year is the date's year, else
// year-1. E.g. 2026-03-31 => 2025, 2026-04-01 => 2026.
func finYearOf(voucherDate string) (int, error) {
	t, err := time.Parse("2006-01-02", voucherDate)
	if err != nil {
		return 0, fmt.Errorf("ledger: parse voucher date %q: %w", voucherDate, err)
	}
	if t.Month() >= time.April {
		return t.Year(), nil
	}
	return t.Year() - 1, nil
}
