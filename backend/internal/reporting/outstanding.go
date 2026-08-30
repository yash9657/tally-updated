package reporting

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// OnAccountRefName is the single bucket a party's UNREFERENCED balance is
// reported under (D106), and it must be spelled exactly as `ledger` spells it
// when it writes an explicit ON_ACCOUNT reference.
//
// ⛔ THE TWO CANNOT SHARE ONE CONSTANT, AND THAT IS AN ARCHITECTURE DECISION, NOT
// AN OVERSIGHT. `internal/archtest` allows `reporting` to import `platform/db`,
// `inventory` and `compliance/form140` and nothing else; importing `ledger` for
// a string would add a forbidden edge, and `ledger`'s own tests already import
// `reporting`, so the edge is a compile-time cycle as well as a policy breach.
//
// ⚠️ SO THE AGREEMENT IS ENFORCED WHERE BOTH ARE VISIBLE: `internal/server`
// (the composition root, which may import either) carries a test asserting these
// two constants are equal. If they ever drift, an explicit ON_ACCOUNT reference
// and the derived residue land in DIFFERENT buckets and a party shows two "On
// Account" rows that each look right — so the test is the thing that keeps this
// honest, and it is named for that failure.
const OnAccountRefName = "On Account"

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
	// OnAccount marks the one row per party that is NOT a bill: the part of the
	// party's balance that names no reference (D106).
	//
	// ⛔ IT IS DELIBERATELY UN-AGED AND OUTSIDE EVERY SLAB. An amount with no bill
	// has no due date to be overdue against, so ageing it would be inventing the
	// fact the row exists to report the absence of. Tally does the same — "On
	// Account amounts are not reflected in Interest Statement, Outstanding (Open
	// Bills) Statements or Age Analysis Statement".
	//
	// ⚠️ SO THE SLAB TOTALS DO NOT SUM TO `Total`, AND THAT IS WHY `OnAccountTotal`
	// EXISTS ON THE REPORT. Tally leaves that gap silent; this report states it,
	// because a column of buckets beside a larger total is exactly the shape a
	// reader checks by adding up.
	OnAccount bool
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
	Kind OutstandingKind
	// AsOn is the ISO date the AGEING is computed against. ⚠️ IT IS NOT A CLAIM
	// ABOUT WHICH BILLS ARE INCLUDED — see Bounded. It is always populated,
	// because every bill on this report is aged against something.
	AsOn string
	// Bounded is whether the CALLER named a date, and therefore whether the rows
	// are limited to vouchers dated on or before AsOn.
	//
	// ⛔⛔ IT EXISTS BECAUSE ONE FIELD CANNOT SAY BOTH THINGS, AND TRYING TO WAS
	// DEFECT D86's SECOND HALF. With `AsOn` alone, an unbounded report echoed
	// today's date and every caption downstream read it as "as at today" — a
	// sentence that is fully present, well-formed and false. A reader that wants
	// to know what the rows COVER must ask this; a reader that wants to know what
	// the ageing is measured FROM asks AsOn.
	Bounded    bool
	Parties    []PartyOutstanding
	SlabTotals []SlabTotal
	Total      decimal.Decimal
	// OnAccountTotal is the part of Total that names no bill and is therefore in
	// NO slab. `sum(SlabTotals) + OnAccountTotal == Total`, always, and that
	// identity is the report's own reconciliation — see Bill.OnAccount.
	OnAccountTotal decimal.Decimal
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
	// ⛔⛔ "THE CALLER SUPPLIED NOTHING" AND "THE CALLER SUPPLIED TODAY" ARE DIFFERENT
	// QUESTIONS, AND CONFLATING THEM WAS DEFECT D86. `resolveAsOn` answers the second
	// by defaulting the first, which is right for AGEING — an ageing report with no
	// reference date answers nothing — and wrong for INCLUSION. Bounded at today, this
	// report disagreed with the Balance Sheet's Sundry Debtors/Creditors the moment a
	// bill was dated ahead: driven, BS 109950.0000 against Outstandings 108770.0000
	// over one set of books, with nothing on either report to reconcile them.
	//
	// ⛔ THE DECISION IS THAT **OUTSTANDINGS** MOVES, NOT THE BALANCE SHEET. A ledger
	// balance includes every posted voucher whatever its date; a bill-wise breakup of
	// that same balance must cover the same vouchers or it is a breakup of something
	// else. So with no `as_on` the ROWS are unbounded and the whole balance is
	// explained; with an explicit `as_on` the bound is honoured exactly as before.
	//
	// ⚠️ `resolveAsOn` IS PACKAGE-SHARED — `bankrecon.go` is a second caller with its
	// own meaning for empty ("everything ever posted") — so its signature and its
	// default are UNTOUCHED and the distinction is carried here, next to the query
	// that needs it. `asOnTime` still reaches `ageBill` and still self-captions, so an
	// unbounded report ages against today rather than against nothing.
	//
	// ✅ THE PRECEDENT IS IN THIS PACKAGE: `orderbook.go`'s `resolveOverdueAsOn` splits
	// exactly these two halves — the ROWS stay genuinely unbounded while only the
	// LATENESS gets a default — and says so in its own comment.
	bounded := strings.TrimSpace(asOn) != ""

	// Receivables track debit-natured (asset) parties; payables credit-natured
	// (liability) parties. The pending sign is oriented so a still-open bill is
	// positive on its side.
	natureFilter := "ASSET"
	if kind == Payables {
		natureFilter = "LIABILITY"
	}

	out := Outstanding{
		Kind:           kind,
		AsOn:           asOnTime.Format("2006-01-02"),
		Bounded:        bounded,
		Total:          decimal.Zero,
		OnAccountTotal: decimal.Zero,
	}

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
		// ⛔ THE OPENING BREAKUP IS DECIDED EXPLICITLY FOR THE UNBOUNDED CASE RATHER
		// THAN INHERITING TODAY'S ANSWER. Unbounded means "every bill these books
		// carry", and the opening references ARE part of the books, so they are IN —
		// and the guard below is asked only when the caller named a date. It happens
		// to give the same answer for a live company (today >= books_beginning_from),
		// which is exactly why it must be written down: a silent agreement is not a
		// decision, and the next person to read `openingIsInScope` needs to know
		// which question it is answering.
		if bounded && !s.openingIsInScope(ctx, tx, asOnTime) {
			openingBranch = ""
		}
		// ⛔ THE INCLUSION BOUND, AND IT IS NOW THE ONLY THING `bounded` GATES. The
		// predicate is spelled exactly as before when a date was asked for; with no
		// date there is no predicate at all, so a voucher with a NULL date is included
		// too — which is the honest answer for "every bill on these books" and is the
		// same set the ledger balance covers.
		inclusionBound := ""
		// ⛔ THE ARGUMENT LIST MOVES WITH THE PREDICATE. pgx binds exactly what the
		// statement references, so leaving `$2` in the args while dropping it from the
		// text answers `bind message supplies 2 parameters, but prepared statement
		// requires 1` — a 500 on the report, not a wrong number. They are built
		// together, here, for that reason.
		args := []any{natureFilter}
		if bounded {
			inclusionBound = "\n			             AND vv.voucher_date <= $2::date"
			args = append(args, asOnTime.Format("2006-01-02"))
		}

		// =====================================================================
		// ⛔⛔ SOURCE 3 — THE RESIDUE, AND IT IS WHAT MAKES THE CAPTION TRUE.
		// =====================================================================
		// D106. Sources 1 and 2 are REFERENCES, and a party's balance is not
		// obliged to be fully referenced: a receipt entered without bill-wise
		// detail moves the ledger and names no bill. Driven in a browser
		// 2026-08-30 — Outstandings 318584.0000 against a Balance Sheet Sundry
		// Debtors of 311584.0000, differing by a 7,000 receipt with zero
		// allocations — while this report's own caption promised the two agree.
		//
		// ⭐ THE FIX IS NOT A SPECIAL CASE FOR RECEIPTS. It is an identity:
		//
		//     On Account  =  the ledger's whole balance  −  every named reference
		//
		// expressed as two more branches that file the WHOLE movement under
		// `On Account` (+) and take every named reference back out again (−).
		// A fully-referenced invoice contributes +X and −X and nets to zero, so
		// it never appears; an unreferenced receipt keeps its −X and does. The
		// report therefore reconciles to the ledger BY CONSTRUCTION rather than
		// by having had this one case patched, which is the only form of the fix
		// that can keep the caption honest for cases nobody has driven yet.
		//
		// ⛔ WHY THE RESIDUE IS COMPUTED AND NOT STORED. The obvious repair is to
		// write the missing ON_ACCOUNT allocation into the existing voucher. It
		// cannot be done: `bill_allocation` rows are inside the voucher's
		// canonical JSON and therefore inside its HASH (`ledger/hash.go` —
		// `BillAllocations` is `omitempty` precisely so that a voucher carrying
		// none hashes without the key). Adding one to a version already written
		// would change its bytes and break the chain, which is a one-way door.
		// So existing books are reconciled by DERIVING the residue here, and new
		// ones are kept clean at the point of entry instead — see
		// `ledger.validateBillWiseCoverage`, which is the other half of D106.
		//
		// ⚠️ IT COVERS EVERY LEDGER OF THE MATCHING NATURE, NOT ONLY THE
		// `maintain_bill_by_bill` ONES. The caption promises agreement with
		// Sundry Debtors/Creditors, and that group contains both kinds; a party
		// tracked without bill-wise detail has a balance and no bills at all, and
		// Tally reports exactly that as On Account ("when the sales and receipt
		// entries are not recorded bill-wise then such entries are tracked as On
		// Account and are displayed in the Ledger Outstandings report").
		// ⛔⛔ THE RESIDUE IS SCOPED TO PARTY GROUPS, AND GETTING THIS WRONG IS LOUD.
		// The outer query filters on `g.nature`, which for receivables is ASSET —
		// and ASSET is every bank, cash and stock group too. The first cut of this
		// branch filed EVERY asset ledger's balance under On Account and reported
		// 725784.0000 against a Sundry Debtors of 311584.0000: the excess was
		// exactly HDFC Bank 379000 + Cash 35200. Nature alone does not mean "party".
		//
		// ⭐ THE MARKER IS TALLY'S OWN: `behaves_as_subledger` — "Group behaves like
		// a sub-ledger" — which is set on Sundry Debtors and Sundry Creditors and
		// nothing else. It is matched over the ltree SUBTREE rather than on the
		// group itself, so a user-made sub-group under Sundry Debtors ("Debtors —
		// North") is a party group too. Matching the flag on the row alone would
		// have silently dropped every party in a sub-group, which is the same class
		// of miss as the one this whole defect is about.
		partyScope := `
			             AND EXISTS (SELECT 1 FROM account_group anc
			                          WHERE anc.company_id = %[1]s.company_id
			                            AND anc.behaves_as_subledger
			                            AND %[1]s.path <@ anc.path)`
		residueBranch := fmt.Sprintf(`
			          UNION ALL
			          SELECT p.ledger_id, '%[1]s', p.dr_cr, p.amount, NULL::date
			            FROM posting p
			            JOIN voucher_version vv2 ON vv2.id = p.voucher_version_id
			            JOIN voucher_head    vh2
			              ON vh2.company_id = vv2.company_id
			             AND vh2.voucher_id = vv2.voucher_id
			             AND vh2.current_version_id = vv2.id
			            JOIN ledger        l2 ON l2.id = p.ledger_id
			            JOIN account_group g2 ON g2.id = l2.group_id
			           WHERE vh2.is_deleted = false%[2]s`+fmt.Sprintf(partyScope, "g2")+`
			          UNION ALL
			          SELECT b2.ledger_id, '%[1]s',
			                 CASE b2.dr_cr WHEN 'D' THEN 'C'::dr_cr ELSE 'D'::dr_cr END,
			                 b2.amount, NULL::date
			            FROM bill_allocation b2
			            JOIN voucher_version vv3 ON vv3.id = b2.voucher_version_id
			            JOIN voucher_head    vh3
			              ON vh3.company_id = vv3.company_id
			             AND vh3.voucher_id = vv3.voucher_id
			             AND vh3.current_version_id = vv3.id
			            JOIN ledger        l3 ON l3.id = b2.ledger_id
			            JOIN account_group g3 ON g3.id = l3.group_id
			           WHERE vh3.is_deleted = false%[3]s`+fmt.Sprintf(partyScope, "g3")+``,
			OnAccountRefName,
			strings.ReplaceAll(inclusionBound, "vv.voucher_date", "vv2.voucher_date"),
			strings.ReplaceAll(inclusionBound, "vv.voucher_date", "vv3.voucher_date"))
		// ⛔ THE OPENING BALANCE JOINS THE RESIDUE ONLY WHEN ITS BREAKUP DOES.
		// `ledger.opening_balance` and `ledger_opening_bill` are the two halves of
		// one fact, and the guard above may drop the breakup as out of scope. If
		// the balance were folded in while the breakup was not, every opening
		// reference would reappear as On Account — the whole opening ageing
		// restated as unallocated money. They move together or not at all.
		if openingBranch != "" {
			residueBranch += fmt.Sprintf(`
			          UNION ALL
			          SELECT lo.id, '%s',
			                 CASE lo.opening_dc WHEN 'D' THEN 'D'::dr_cr ELSE 'C'::dr_cr END,
			                 lo.opening_balance, NULL::date
			            FROM ledger lo
			            JOIN account_group go2 ON go2.id = lo.group_id
			           WHERE lo.opening_balance <> 0
			             AND EXISTS (SELECT 1 FROM account_group anc
			                          WHERE anc.company_id = go2.company_id
			                            AND anc.behaves_as_subledger
			                            AND go2.path <@ anc.path)
			          UNION ALL
			          SELECT ob2.ledger_id, '%s',
			                 CASE ob2.dr_cr WHEN 'D' THEN 'C'::dr_cr ELSE 'D'::dr_cr END,
			                 ob2.amount, NULL::date
			            FROM ledger_opening_bill ob2`, OnAccountRefName, OnAccountRefName)
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
			           WHERE vh.is_deleted = false%s%s%s
			        ) ba
			   JOIN ledger l        ON l.id = ba.ledger_id
			   JOIN account_group g ON g.id = l.group_id
			  WHERE g.nature = $1
			  GROUP BY ba.ledger_id, l.name, g.name, ba.ref_name
			  ORDER BY l.name,
			           (ba.ref_name = '%[5]s') ASC,
			           MIN(ba.due_date) ASC NULLS LAST,
			           ba.ref_name`, signExpr, inclusionBound, openingBranch, residueBranch, OnAccountRefName),
			args...)
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
			// ⛔ THE ON-ACCOUNT ROW IS NOT AGED. ageBill would read its empty due
			// date as "not overdue" and drop it into the FIRST slab, which is the
			// worst of the three options: money that is not a bill at all would be
			// reported as the freshest debt on the party.
			bill.OnAccount = refName == OnAccountRefName
			if !bill.OnAccount {
				bill.OverdueDays, bill.SlabLabel = ageBill(bill.DueDate, asOnTime, slabs)
			}

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
			if bill.OnAccount {
				out.OnAccountTotal = out.OnAccountTotal.Add(pending)
			} else {
				slabTotal[bill.SlabLabel] = slabTotal[bill.SlabLabel].Add(pending)
			}
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
