package reporting

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// OutstandingKind selects receivables (debtors) or payables (creditors).
type OutstandingKind string

const (
	// Receivables are amounts owed to us (Sundry Debtors — net debit).
	Receivables OutstandingKind = "RECEIVABLE"
	// Payables are amounts we owe (Sundry Creditors — net credit).
	Payables OutstandingKind = "PAYABLE"
)

// AgeSlab is a configurable ageing bucket. UptoDays is the inclusive upper bound
// in days overdue; a slab with UptoDays == 0 is the open-ended "and above"
// bucket and must be last.
type AgeSlab struct {
	Label    string
	UptoDays int
}

// DefaultAgeSlabs is Tally's default ageing ladder (RESEARCH sec 8:
// <45 / 45-90 / 90-180 / >180).
var DefaultAgeSlabs = []AgeSlab{
	{Label: "0-45 days", UptoDays: 45},
	{Label: "45-90 days", UptoDays: 90},
	{Label: "90-180 days", UptoDays: 180},
	{Label: "180+ days", UptoDays: 0},
}

// Bill is a single outstanding reference (a New Ref netted by subsequent Agst
// Ref allocations) for a party ledger.
type Bill struct {
	RefName string
	// Pending is the unsettled amount, signed to the party's natural side
	// (positive = still outstanding on the receivable/payable side).
	Pending decimal.Decimal
	DueDate string // ISO date or empty
	// OverdueDays is days past DueDate as of the report's "as on" date; 0 if not
	// yet due or no due date.
	OverdueDays int
	SlabLabel   string
}

// PartyOutstanding is one party's outstanding position: its net pending amount
// and the per-bill breakdown (drill-down).
type PartyOutstanding struct {
	LedgerID   uuid.UUID
	LedgerName string
	GroupName  string
	Pending    decimal.Decimal
	Bills      []Bill
}

// Outstanding is the Receivables or Payables ageing report: per-party rows
// (each expandable to its bills), ageing-slab totals, and the grand total.
type Outstanding struct {
	Kind       OutstandingKind
	AsOn       string // ISO date the ageing is computed against
	Parties    []PartyOutstanding
	SlabTotals []SlabTotal
	Total      decimal.Decimal
}

// SlabTotal is the aggregate pending in one ageing slab.
type SlabTotal struct {
	Label  string
	Amount decimal.Decimal
}

// openingIsInScope reports whether the ledgers' OPENING bill-wise breakup belongs
// in a report dated asOn. It is queryBalances' guard, applied to the other place
// an opening position hides.
//
// ⚠️ IT MIRRORS THAT QUERY DELIBERATELY, INCLUDING THE FAIL-SAFE. An opening
// carries no date of its own; it is stated as at company.books_beginning_from
// (falling back to fy_start_date). Asked for a date BEFORE that, the position did
// not exist, and reporting it made this report and the Balance Sheet disagree in
// the one direction neither of them can explain.
//
// The company is NAMED rather than picked with LIMIT 1: `company` is FORCE ROW
// LEVEL SECURITY today, but a caller running without the tenant GUC would
// otherwise silently take an arbitrary tenant's books-beginning date and mis-date
// this cutoff on somebody else's ageing. current_setting(…, true) yields NULL when
// unset, matches no row, and falls through to "leave the opening in" — which is
// the pre-existing behaviour and therefore the safe direction for a guard.
func (s *Service) openingIsInScope(ctx context.Context, tx pgx.Tx, asOn time.Time) bool {
	var begins string
	err := tx.QueryRow(ctx,
		`SELECT to_char(COALESCE(books_beginning_from, fy_start_date), 'YYYY-MM-DD')
		   FROM company
		  WHERE id = current_setting('app.current_company_id', true)::uuid`).Scan(&begins)
	if err != nil {
		return true
	}
	return asOn.Format("2006-01-02") >= begins
}

// Outstandings builds the bill-wise ageing report for receivables or payables as
// of asOn (ISO date; empty means today). It aggregates references per
// (ledger, ref_name) from TWO sources — bill_allocation rows belonging to the
// CURRENT, non-deleted version of each voucher, and the ledger's opening
// bill-wise breakup — nets them by dr_cr to the pending amount, and buckets each
// open bill by days overdue. Only parties under credit-natured
// (liability) groups are payables; debit-natured (asset) groups are receivables.
// Passing nil slabs uses DefaultAgeSlabs.
func (s *Service) Outstandings(ctx context.Context, companyID uuid.UUID, kind OutstandingKind, asOn string, slabs []AgeSlab) (Outstanding, error) {
	if err := scope(ctx, companyID); err != nil {
		return Outstanding{}, err
	}
	if kind != Receivables && kind != Payables {
		return Outstanding{}, fmt.Errorf("reporting: invalid outstanding kind %q", kind)
	}
	if len(slabs) == 0 {
		slabs = DefaultAgeSlabs
	}
	asOnTime, err := resolveAsOn(asOn)
	if err != nil {
		return Outstanding{}, err
	}

	// Receivables track debit-natured (asset) parties; payables credit-natured
	// (liability) parties. The pending sign is oriented so a still-open bill is
	// positive on its side.
	natureFilter := "ASSET"
	if kind == Payables {
		natureFilter = "LIABILITY"
	}

	out := Outstanding{Kind: kind, AsOn: asOnTime.Format("2006-01-02"), Total: decimal.Zero}

	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// Net each (ledger, ref_name) by dr_cr. For a receivable (debit-natured),
		// pending = debit - credit; for a payable, pending = credit - debit. The
		// earliest non-null due date for the ref is used for ageing.
		signExpr := "SUM(CASE WHEN ba.dr_cr='D' THEN ba.amount ELSE -ba.amount END)"
		if kind == Payables {
			signExpr = "SUM(CASE WHEN ba.dr_cr='C' THEN ba.amount ELSE -ba.amount END)"
		}
		// The reference universe is TWO sources UNIONed:
		//
		//   1. bill_allocation      — references made by vouchers (current
		//                             versions only, see the join below).
		//   2. ledger_opening_bill  — the BILL-WISE OPENING BREAKUP carried in on
		//                             the ledger master as at
		//                             company.books_beginning_from.
		//
		// Source 2 is not optional bookkeeping: the Balance Sheet's Sundry
		// Debtors/Creditors totals include ledger.opening_balance (reporting.go
		// folds it into the closing figure), so omitting the opening references
		// here would make this report short by exactly the opening balance while
		// both reports still looked internally consistent.
		//
		// A settlement posted LATER against an opening reference nets against it
		// naturally, because both sides land in the same (ledger, ref_name) group.
		//
		// =====================================================================
		// ⛔ BOTH SOURCES ARE NOW DATED, AND NEITHER WAS. DRIVEN.
		// =====================================================================
		// This report honoured `as_on` for AGEING and not for INCLUSION: the
		// sub-select carried no date predicate at all, so a bill raised AFTER the
		// as-on date was reported under a caption asserting the two dates agreed.
		// Driven in a browser: an os-total of 2,65,720.00 at as-on 2026-04-30
		// against a Balance Sheet Sundry Debtors of 1,79,000.00 — two reports over
		// one set of books, differing by 86,720.00, with nothing anywhere to
		// reconcile them. An operator chasing debtors as at the quarter end was
		// being shown invoices that did not exist yet.
		//
		// The predicate is spelled the way movementSQL spells it, deliberately: a
		// voucher with a NULL date is dated into NO period, so it is excluded here
		// exactly as it is excluded from every other as-at report. One convention.
		//
		// AND THE OPENING BREAKUP IS DATED TOO — as at books_beginning_from, the
		// same fact queryBalances states for ledger.opening_balance. It carries no
		// date column, so folding it in unconditionally reported the entire
		// opening ageing for a date BEFORE the books existed. Excluding it is
		// ALL-OR-NOTHING for the same reason it is there: opening references
		// balance among themselves.
		openingBranch := `
			          UNION ALL
			          SELECT ob.ledger_id, ob.ref_name, ob.dr_cr, ob.amount, ob.due_date
			            FROM ledger_opening_bill ob`
		if !s.openingIsInScope(ctx, tx, asOnTime) {
			openingBranch = ""
		}
		rows, err := tx.Query(ctx, fmt.Sprintf(
			`SELECT ba.ledger_id, l.name, g.name, ba.ref_name,
			        %s AS pending,
			        to_char(MIN(ba.due_date), 'YYYY-MM-DD') AS due_date
			   FROM (
			          SELECT b.ledger_id, b.ref_name, b.dr_cr, b.amount, b.due_date
			            FROM bill_allocation b
			            JOIN voucher_version vv ON vv.id = b.voucher_version_id
			            JOIN voucher_head    vh
			              ON vh.company_id = vv.company_id
			             AND vh.voucher_id = vv.voucher_id
			             AND vh.current_version_id = vv.id
			           WHERE vh.is_deleted = false
			             AND vv.voucher_date <= $2::date%s
			        ) ba
			   JOIN ledger l        ON l.id = ba.ledger_id
			   JOIN account_group g ON g.id = l.group_id
			  WHERE g.nature = $1
			  GROUP BY ba.ledger_id, l.name, g.name, ba.ref_name
			  ORDER BY l.name, ba.ref_name`, signExpr, openingBranch),
			natureFilter, asOnTime.Format("2006-01-02"))
		if err != nil {
			return fmt.Errorf("reporting: query outstandings: %w", err)
		}
		defer rows.Close()

		partyIdx := make(map[uuid.UUID]int)
		slabTotal := make(map[string]decimal.Decimal)
		for rows.Next() {
			var (
				ledgerID uuid.UUID
				ledgerNm string
				groupNm  string
				refName  string
				pending  decimal.Decimal
				dueDate  *string
			)
			if err := rows.Scan(&ledgerID, &ledgerNm, &groupNm, &refName, &pending, &dueDate); err != nil {
				return fmt.Errorf("reporting: scan outstanding: %w", err)
			}
			// A fully-settled bill nets to zero; skip it.
			if pending.IsZero() {
				continue
			}

			bill := Bill{RefName: refName, Pending: pending}
			if dueDate != nil {
				bill.DueDate = *dueDate
			}
			bill.OverdueDays, bill.SlabLabel = ageBill(bill.DueDate, asOnTime, slabs)

			i, ok := partyIdx[ledgerID]
			if !ok {
				i = len(out.Parties)
				partyIdx[ledgerID] = i
				out.Parties = append(out.Parties, PartyOutstanding{
					LedgerID:   ledgerID,
					LedgerName: ledgerNm,
					GroupName:  groupNm,
					Pending:    decimal.Zero,
				})
			}
			p := &out.Parties[i]
			p.Bills = append(p.Bills, bill)
			p.Pending = p.Pending.Add(pending)
			out.Total = out.Total.Add(pending)
			slabTotal[bill.SlabLabel] = slabTotal[bill.SlabLabel].Add(pending)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("reporting: iterate outstandings: %w", err)
		}

		// Emit slab totals in slab order.
		for _, sl := range slabs {
			out.SlabTotals = append(out.SlabTotals, SlabTotal{
				Label:  sl.Label,
				Amount: slabTotal[sl.Label],
			})
		}
		return nil
	})
	if err != nil {
		return Outstanding{}, err
	}
	// Stable order: largest pending first, then name.
	sort.SliceStable(out.Parties, func(i, j int) bool {
		if !out.Parties[i].Pending.Equal(out.Parties[j].Pending) {
			return out.Parties[i].Pending.GreaterThan(out.Parties[j].Pending)
		}
		return out.Parties[i].LedgerName < out.Parties[j].LedgerName
	})
	return out, nil
}

// resolveAsOn parses an ISO "as on" date, defaulting to today (UTC) when empty.
func resolveAsOn(asOn string) (time.Time, error) {
	if asOn == "" {
		now := time.Now().UTC()
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC), nil
	}
	t, err := time.Parse("2006-01-02", asOn)
	if err != nil {
		return time.Time{}, fmt.Errorf("reporting: invalid as-on date %q: %w", asOn, err)
	}
	return t, nil
}

// ageBill returns the days overdue (0 if not yet due / no due date) and the slab
// label the bill falls into. The strict ageing is by due date as of asOn.
func ageBill(dueDate string, asOn time.Time, slabs []AgeSlab) (int, string) {
	overdue := 0
	if dueDate != "" {
		if due, err := time.Parse("2006-01-02", dueDate); err == nil {
			if d := int(asOn.Sub(due).Hours() / 24); d > 0 {
				overdue = d
			}
		}
	}
	for _, sl := range slabs {
		if sl.UptoDays == 0 || overdue <= sl.UptoDays {
			return overdue, sl.Label
		}
	}
	if len(slabs) > 0 {
		return overdue, slabs[len(slabs)-1].Label
	}
	return overdue, ""
}
