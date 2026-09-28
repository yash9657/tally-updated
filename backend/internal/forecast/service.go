package forecast

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/reporting"
)

// Runner is the DB capability the loader needs: a read-only, RLS-scoped tx for
// its own supplementary reads. *db.Pool satisfies it (and reporting.TxRunner).
type Runner interface {
	RunInReadOnlyTx(ctx context.Context, fn db.TxFunc) error
}

// Service loads the rows the pure core needs and calls it. It composes
// reporting.Service for the AUTHORITATIVE bill-pending amounts (the same netting
// the Outstandings report uses — never re-implemented here) and issues its own
// read-only supplementary queries for the date attributes and opening cash.
//
// SEMANTICS. The projection's amounts come from reporting.Outstandings, and the
// invariance gate is a conservation check against it: whatever that report
// considers pending is what the projection distributes over buckets.
//
// This block used to disclose an OVER-COUNT — reporting.Outstandings summed
// bill_allocation across ALL versions with no voucher_head current-version join,
// so an ALTERed bill was projected at (old + new) and a DELETED one aged
// forever. That was true when this file was written. The demo-remediation
// session then fixed the report itself (it now joins voucher_head on
// current_version_id with is_deleted = false), which fixed the projection with
// it — and this comment went stale, still warning about a defect that no longer
// existed.
//
// TestForecastDoesNotOverCountAlteredOrDeletedBills now asserts the property
// directly instead of describing it, and reintroducing the all-versions universe
// reproduces the old 1400-instead-of-400 exactly.
type Service struct {
	runner Runner
	report *reporting.Service
}

// NewService builds a forecast Service over a *db.Pool (which satisfies both the
// read-only runner and reporting's tx runner).
func NewService(pool *db.Pool) *Service {
	return &Service{runner: pool, report: reporting.NewService(pool)}
}

// TxRunner is both halves a Service reads through: the report's tx and its own
// read-only supplement.
type TxRunner interface {
	reporting.TxRunner
	Runner
}

// NewServiceOver builds a Service over any runner — in particular one that
// REUSES a caller's open transaction (W4's nightly insight run reads the
// projection inside the same transaction as the facts it has just refreshed,
// so every figure of the night describes one moment of the books; the
// profile.Refresh rule, D122-9). A runner whose read-only half reuses a
// read-write transaction is the caller's promise that fn only reads.
func NewServiceOver(r TxRunner) *Service {
	return &Service{runner: r, report: reporting.NewService(r)}
}

// dateAttr carries a bill's date attributes, keyed by (ledger_id, ref_name).
type dateAttr struct {
	voucherDate *time.Time
	creditDays  *int
}

type billKey struct {
	ledgerID uuid.UUID
	refName  string
}

// CashFlow projects cash movement from open bills. asOn is an ISO date (empty =
// today). horizonDays/bucketDays are validated by Project. bucket is "week"
// (7-day) or "month" (30-day, rolling — NOT calendar).
func (s *Service) CashFlow(ctx context.Context, companyID uuid.UUID, asOn string, horizonDays int, bucket string) (Projection, error) {
	bucketDays, err := bucketDaysFor(bucket)
	if err != nil {
		return Projection{}, err
	}
	asOnTime, err := parseAsOn(asOn)
	if err != nil {
		return Projection{}, err
	}

	// AUTHORITATIVE amounts (reuse the report's netting; do NOT re-implement).
	rec, err := s.report.Outstandings(ctx, companyID, reporting.Receivables, asOn, nil)
	if err != nil {
		return Projection{}, fmt.Errorf("forecast: receivables: %w", err)
	}
	pay, err := s.report.Outstandings(ctx, companyID, reporting.Payables, asOn, nil)
	if err != nil {
		return Projection{}, fmt.Errorf("forecast: payables: %w", err)
	}

	// Supplementary date attributes + opening cash (our own read-only tx).
	var attrs map[billKey]dateAttr
	var opening decimal.Decimal
	if err := s.runner.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		attrs, err = loadDateAttrs(ctx, tx)
		if err != nil {
			return err
		}
		opening, err = loadOpeningCash(ctx, tx, asOnTime)
		return err
	}); err != nil {
		return Projection{}, err
	}

	bills := make([]ProjBill, 0)
	bills = append(bills, buildBills(rec, Inflow, attrs)...)
	bills = append(bills, buildBills(pay, Outflow, attrs)...)

	return Project(bills, opening, asOnTime, horizonDays, bucketDays)
}

// buildBills turns a reporting.Outstanding into ProjBills of the given kind,
// joining the date attributes on (ledger_id, ref_name). The bill's DueDate comes
// from the report (its MIN due date); VoucherDate/CreditDays from the supplement.
func buildBills(o reporting.Outstanding, kind FlowKind, attrs map[billKey]dateAttr) []ProjBill {
	var bills []ProjBill
	for _, party := range o.Parties {
		for _, b := range party.Bills {
			pb := ProjBill{Kind: kind, Pending: b.Pending}
			if b.DueDate != "" {
				if d, err := time.Parse("2006-01-02", b.DueDate); err == nil {
					pb.DueDate = &d
				}
			}
			if a, ok := attrs[billKey{party.LedgerID, b.RefName}]; ok {
				pb.CreditDays = a.creditDays
				if a.voucherDate != nil {
					pb.VoucherDate = *a.voucherDate
				}
			}
			bills = append(bills, pb)
		}
	}
	return bills
}

// loadDateAttrs reads, per (ledger_id, ref_name), the earliest NEW-allocation
// voucher date and the party ledger's credit_days.
//
// It deliberately scans ALL versions rather than only current ones. That is not
// the over-count the header used to describe — it carries no AMOUNTS. It is a
// DATE lookup, and a superset is the safe direction: every bill the (now
// current-version-only) amount query returns is guaranteed a matching date row,
// where a narrower scan could leave one dateless and silently fall through to
// the +30 default.
func loadDateAttrs(ctx context.Context, tx pgx.Tx) (map[billKey]dateAttr, error) {
	rows, err := tx.Query(ctx,
		`SELECT ba.ledger_id, ba.ref_name,
		        MIN(vv.voucher_date) FILTER (WHERE ba.ref_type = 'NEW') AS voucher_date,
		        MAX(l.credit_days) AS credit_days
		   FROM bill_allocation ba
		   JOIN voucher_version vv ON vv.id = ba.voucher_version_id
		   JOIN ledger l ON l.id = ba.ledger_id
		  GROUP BY ba.ledger_id, ba.ref_name`)
	if err != nil {
		return nil, fmt.Errorf("forecast: date attrs: %w", err)
	}
	defer rows.Close()

	out := map[billKey]dateAttr{}
	for rows.Next() {
		var (
			k  billKey
			vd *time.Time
			cd *int
		)
		if err := rows.Scan(&k.ledgerID, &k.refName, &vd, &cd); err != nil {
			return nil, fmt.Errorf("forecast: scan date attr: %w", err)
		}
		out[k] = dateAttr{voucherDate: vd, creditDays: cd}
	}
	return out, rows.Err()
}

// loadOpeningCash computes the cash/bank position as of asOn: the signed opening
// balances of every ledger under the seeded 'Cash-in-Hand' or 'Bank Accounts'
// subtree, plus the signed posted movement on those ledgers up to asOn (current,
// non-deleted versions only). 'Bank OD A/c' (a liability) is deliberately
// EXCLUDED in v1 (Tally would net it — a documented deviation).
func loadOpeningCash(ctx context.Context, tx pgx.Tx, asOn time.Time) (decimal.Decimal, error) {
	const cashSubtree = `(g.path <@ (SELECT path FROM account_group WHERE name = 'Cash-in-Hand')
	                   OR g.path <@ (SELECT path FROM account_group WHERE name = 'Bank Accounts'))`
	var cash decimal.Decimal
	err := tx.QueryRow(ctx,
		`SELECT
		   (SELECT COALESCE(SUM(CASE WHEN l.opening_dc = 'D' THEN l.opening_balance ELSE -l.opening_balance END), 0)
		      FROM ledger l
		      JOIN account_group g ON g.id = l.group_id
		     WHERE `+cashSubtree+`)
		 + (SELECT COALESCE(SUM(CASE WHEN p.dr_cr = 'D' THEN p.amount ELSE -p.amount END), 0)
		      FROM posting p
		      JOIN ledger l ON l.id = p.ledger_id
		      JOIN account_group g ON g.id = l.group_id
		      JOIN voucher_version vv ON vv.id = p.voucher_version_id
		      JOIN voucher_head vh
		        ON vh.company_id = vv.company_id
		       AND vh.voucher_id = vv.voucher_id
		       AND vh.current_version_id = vv.id
		       AND vh.is_deleted = false
		     WHERE `+cashSubtree+`
		       AND vv.voucher_date <= $1::date)`,
		asOn.Format("2006-01-02")).Scan(&cash)
	if err != nil {
		return decimal.Zero, fmt.Errorf("forecast: opening cash: %w", err)
	}
	return cash, nil
}

// Anomalies loads all voucher versions whose created_at falls in [from, to] (ISO
// dates; empty = unbounded) and runs the deterministic detector. The window keys
// on created_at (the audit "when was it entered" axis the report is about), so a
// back-dated voucher entered in the window is included even though its
// voucher_date is older.
func (s *Service) Anomalies(ctx context.Context, companyID uuid.UUID, from, to string) ([]Finding, error) {
	var recs []VersionRecord
	if err := s.runner.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		recs, err = loadVersionRecords(ctx, tx, from, to)
		return err
	}); err != nil {
		return nil, err
	}
	return DetectAnomalies(recs, DefaultThresholds()), nil
}

// loadVersionRecords reads ALL voucher versions (not just current — EDIT_BURST
// needs supersessions) in the created_at window.
func loadVersionRecords(ctx context.Context, tx pgx.Tx, from, to string) ([]VersionRecord, error) {
	rows, err := tx.Query(ctx,
		`SELECT vv.voucher_id, COALESCE(vv.voucher_no, ''), vv.voucher_date,
		        vv.created_at, vv.version_no, vv.taxable_value
		   FROM voucher_version vv
		  WHERE ($1 = '' OR vv.created_at >= $1::date)
		    AND ($2 = '' OR vv.created_at < ($2::date + 1))
		  ORDER BY vv.created_at, vv.version_no`,
		from, to)
	if err != nil {
		return nil, fmt.Errorf("forecast: version records: %w", err)
	}
	defer rows.Close()

	var out []VersionRecord
	for rows.Next() {
		var (
			r  VersionRecord
			vd *time.Time
			tv *decimal.Decimal
		)
		if err := rows.Scan(&r.VoucherID, &r.VoucherNo, &vd, &r.CreatedAt, &r.VersionNo, &tv); err != nil {
			return nil, fmt.Errorf("forecast: scan version: %w", err)
		}
		if vd != nil {
			r.VoucherDate = *vd
		}
		if tv != nil {
			r.TaxableValue = *tv
			r.HasTaxable = true
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// bucketDaysFor maps the bucket name to its day width.
func bucketDaysFor(bucket string) (int, error) {
	switch bucket {
	case "", "week":
		return 7, nil
	case "month":
		return 30, nil
	default:
		return 0, fmt.Errorf("forecast: unknown bucket %q (want week|month)", bucket)
	}
}

// parseAsOn parses an ISO date, defaulting empty to today (UTC).
func parseAsOn(asOn string) (time.Time, error) {
	if asOn == "" {
		return dateOnly(time.Now().UTC()), nil
	}
	t, err := time.Parse("2006-01-02", asOn)
	if err != nil {
		return time.Time{}, fmt.Errorf("forecast: invalid as_on %q", asOn)
	}
	return t, nil
}
