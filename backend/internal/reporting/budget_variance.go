package reporting

// BUDGET VS ACTUAL (T11, migrations 0085/0086) — the only report in this package
// whose left-hand column is not derived from a voucher.
//
// Every other statement here answers "what happened". This one sets that beside
// "what was meant to happen", and the second column comes from `budget_line`,
// which posts nothing and is in no hash chain.
//
// ⛔ THE ACTUAL SIDE IS NOT REIMPLEMENTED HERE. loadClosingBalances and
// loadPeriodMovement in reporting.go already are the two bases (see their PERIOD
// SEMANTICS note), and costCentreFactSQL in cost_summary.go already is the
// cost-centre one. A second derivation of a ledger balance in this file would be
// a number that could disagree with the Trial Balance, and the first person to
// notice would be an accountant.
//
// ===========================================================================
// ⛔⛔ THE FOUR WAYS A VARIANCE REPORT LIES, AND WHAT THIS FILE DOES ABOUT EACH
// ===========================================================================
// This repository's defect register is full of figures that looked complete and
// were not (D100, D105 are the pattern). A variance column is unusually good at
// producing them, because every one of these failures still FOOTS:
//
//  1. A BUDGET WITH NO ACTUAL IS NOT "ZERO VARIANCE". It is UNSPENT, which is a
//     different fact and often an alarming one — a department that has not
//     touched its budget three quarters in is a department whose plan was wrong
//     or whose invoices are not being keyed. Reported as its own status, never
//     folded into UNDER.
//     ⚠️ AND "NOTHING WAS POSTED" IS NOT "IT NETTED TO ZERO" either. A ledger
//     with 3,00,000 debit and 3,00,000 credit has a nett of zero and is NOT
//     unspent — something happened and cancelled. NETTED_TO_ZERO is its own
//     status for exactly that, which is why every row carries ActualDebit and
//     ActualCredit and not just the nett.
//
//  2. AN ACTUAL WITH NO BUDGET IS NOT "100% OVER". It is UNBUDGETED, and it is
//     the row a finance person most wants to see: money leaving the business
//     that no plan accounted for. It is carried in its OWN array (Unbudgeted)
//     rather than mixed into Rows with a budget of zero, so the budget column
//     never contains a figure nobody set and the plan's totals stay clean.
//
//  3. ⛔ NOTHING IS EVER DIVIDED BY A ZERO BUDGET. VariancePercent is a POINTER
//     and is nil when there is no budget to be a percentage of. A JSON `null`
//     forces a client to render "no budget set"; a 0, or a 100, or an Inf is a
//     number a reader will believe.
//
//  4. ⛔⛔ TWO BASES MUST NEVER BE ADDED TOGETHER. A CLOSING_BALANCE budget is a
//     POSITION and a NETT_TRANSACTIONS budget is a FLOW, and their sum is not a
//     quantity — see the migration 0085 header. A plan legitimately holds both
//     (a closing-debtors ceiling and a travel-spend cap), so this report emits a
//     SUBTOTAL PER BASIS always, and SUPPRESSES the grand total whenever more
//     than one basis is present, saying so on the artefact. That is the same call
//     CostCategorySummary makes about cross-axis totals, for the same reason.
//
// ===========================================================================
// ⛔ WHY THE COST-CENTRE ARM DEMANDS AN AXIS, AND WHY THAT IS NOT AN ANNOYANCE
// ===========================================================================
// A cost category is an AXIS and the same money is allocated IN FULL on every
// axis: Rs 1,00,000 of salary split across Departments and again across Branches
// is Rs 1,00,000 described twice. So a cost-centre variance report spanning two
// axes would total a multiple of the cost actually borne.
//
// Rather than emit a total and warn about it, this report REFUSES without an
// axis and names the ones this company has — reusing CostAxisError, which
// CostCentreSummaryFor already raises for the identical reason. One axis in, one
// coherent set of totals out, and the cross-axis sum is unrepresentable rather
// than merely discouraged.
//
// ===========================================================================
// ⛔ WHAT "UNBUDGETED" MEANS AT EACH GRAIN — ONE RULE, NOT THREE
// ===========================================================================
// Groups nest and cost centres nest, so a naive "every target with an actual and
// no line" list would contain, for one budgeted group, every ANCESTOR of it (each
// containing the budgeted figure) and every DESCENDANT (each contained by it) —
// and a reader adding that column up would multiply the same money several times
// over. That is failure mode 2 reintroduced as its own cure.
//
// The rule is therefore ONE rule at every grain:
//
//	AN UNBUDGETED ROW IS A LEAF-LEVEL ACTUAL THAT NO BUDGET LINE COVERS.
//
//	LEDGER      — a ledger with a non-zero actual that has no line. Ledgers do
//	              not nest, so coverage is simply "is this ledger budgeted".
//	GROUP       — a LEDGER with a non-zero actual none of whose ANCESTOR GROUPS is
//	              budgeted. Reported at ledger grain deliberately: it answers
//	              "what spend does no budgeted group cover", which is the coverage
//	              question, and it cannot overlap because each ledger appears once.
//	COST_CENTRE — a centre with non-zero OWN allocations, on the named axis,
//	              neither budgeted itself nor under a budgeted ancestor.
//
// Every unbudgeted list is COMPLETE — no top-N, no sampling, no cap. A report
// that quietly truncated the list of spend nobody planned would be asserting
// coverage it had not checked, and §9's no-silent-caps rule exists for exactly
// this shape.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// The dimensions and bases, spelled here as the report's own vocabulary. They
// are the strings migration 0085's CHECK constraints store, and internal/masters
// exports the same constants for the write side — reporting does not import
// masters (it depends only on the db interface, never on a sibling write module),
// so the two are pinned together by TestBudgetVocabularyMatchesMasters instead.
const (
	// BudgetDimensionGroup rolls every ledger under a budgeted group.
	BudgetDimensionGroup = "GROUP"
	// BudgetDimensionLedger measures one ledger.
	BudgetDimensionLedger = "LEDGER"
	// BudgetDimensionCostCentre measures one cost centre, rolled up through its
	// children, on ONE axis.
	BudgetDimensionCostCentre = "COST_CENTRE"

	// BudgetBasisClosing measures the actual as a POSITION as at the period end.
	BudgetBasisClosing = "CLOSING_BALANCE"
	// BudgetBasisNett measures the actual as MOVEMENT inside the period.
	BudgetBasisNett = "NETT_TRANSACTIONS"
)

// KnownBudgetDimensions is every grain this report will serve, in the order a
// refusal lists them.
var KnownBudgetDimensions = []string{
	BudgetDimensionGroup, BudgetDimensionLedger, BudgetDimensionCostCentre,
}

// The seven statuses. ⛔ EACH IS A DISTINCT FACT AND NONE IS A ROUNDING OF
// ANOTHER — see failure modes 1 and 2 in the file header. Collapsing UNSPENT into
// UNDER, or NETTED_TO_ZERO into UNSPENT, is the exact defect this set exists to
// prevent.
const (
	// BudgetOnTarget — the actual equals the budget, to the paisa.
	BudgetOnTarget = "ON_TARGET"
	// BudgetOver — the actual exceeds the budget on the budgeted side.
	BudgetOver = "OVER"
	// BudgetUnder — some actual, less than the budget.
	BudgetUnder = "UNDER"
	// BudgetUnspent — a budget, and NOTHING posted against it at all. Not zero
	// variance: an untouched budget is a fact about the plan or about the keying.
	BudgetUnspent = "UNSPENT"
	// BudgetNettedToZero — movement happened on both sides and cancelled exactly.
	// Distinct from UNSPENT, where nothing happened.
	BudgetNettedToZero = "NETTED_TO_ZERO"
	// BudgetContra — the actual is on the OPPOSITE side from the budget. A
	// negative actual is not "well under budget"; it means the position or the
	// movement went the other way, which usually means a reversal or a
	// misclassified posting.
	BudgetContra = "CONTRA"
	// BudgetUnbudgeted — an actual with no budget line. Never "100% over".
	BudgetUnbudgeted = "UNBUDGETED"
)

// ---------------------------------------------------------------------------
// BudgetSelectionError — a missing or unknown budget, which NAMES the choices.
// ---------------------------------------------------------------------------

// BudgetChoice is one budget a caller could have meant.
type BudgetChoice struct {
	ID       uuid.UUID
	Name     string
	FromDate string
	ToDate   string
}

// BudgetSelectionError is raised when no budget was named, or one was named that
// these books do not contain.
//
// IT NAMES THE ALTERNATIVES, for the reason CostAxisError does next door: an
// EMPTY variance report for a budget that does not exist reads as "nothing was
// planned and nothing was spent", which is a real and reassuring claim about a
// company, and it would be a lie. The distinction between Missing and Named is
// what lets the HTTP seam answer 400 for a malformed request and 422 for a
// request about something absent.
type BudgetSelectionError struct {
	Missing   bool
	Named     uuid.UUID
	Available []BudgetChoice
}

func (e *BudgetSelectionError) Error() string {
	var b strings.Builder
	if e.Missing {
		b.WriteString("a budget must be named (budget_id): a variance report is a comparison, " +
			"and there is no default plan to compare against")
	} else {
		b.WriteString(fmt.Sprintf("no budget %s in these books", e.Named))
	}
	if len(e.Available) == 0 {
		b.WriteString(". This company has no budgets at all yet — create one before asking " +
			"for a variance report")
		return b.String()
	}
	b.WriteString(". This company has: ")
	names := make([]string, 0, len(e.Available))
	for _, c := range e.Available {
		names = append(names, fmt.Sprintf("%q (%s to %s, %s)", c.Name, c.FromDate, c.ToDate, c.ID))
	}
	b.WriteString(strings.Join(names, "; "))
	return b.String()
}

// AsBudgetSelectionError reports whether err is a BudgetSelectionError, so the
// HTTP seam can choose a status code without matching on message text.
func AsBudgetSelectionError(err error) (*BudgetSelectionError, bool) {
	var be *BudgetSelectionError
	if errors.As(err, &be) {
		return be, true
	}
	return nil, false
}

// BudgetDimensionError is an absent or unknown dimension, naming the three.
type BudgetDimensionError struct {
	Named string
}

func (e *BudgetDimensionError) Error() string {
	if strings.TrimSpace(e.Named) == "" {
		return "a dimension must be named (dimension): a budget is compared at ONE grain, and " +
			"the three answer different questions — " + strings.Join(KnownBudgetDimensions, ", ")
	}
	return fmt.Sprintf("%q is not a budget dimension — want one of %s",
		e.Named, strings.Join(KnownBudgetDimensions, ", "))
}

// AsBudgetDimensionError reports whether err is a BudgetDimensionError.
func AsBudgetDimensionError(err error) (*BudgetDimensionError, bool) {
	var de *BudgetDimensionError
	if errors.As(err, &de) {
		return de, true
	}
	return nil, false
}

// ---------------------------------------------------------------------------
// The report.
// ---------------------------------------------------------------------------

// BudgetVarianceRow is one budgeted target with its actual.
type BudgetVarianceRow struct {
	Dimension  string
	TargetID   uuid.UUID
	TargetName string
	// CostCategoryID / CostCategoryName are set for a COST_CENTRE row only.
	CostCategoryID   uuid.UUID
	CostCategoryName string
	// Nature is the target's ASSET / LIABILITY / INCOME / EXPENSE, "" for a cost
	// centre. ⛔ IT IS ON THE WIRE BECAUSE THIS REPORT REFUSES TO SAY
	// "FAVOURABLE". Spending more than budget is bad; EARNING more than budget is
	// good; and holding more of an asset than planned is neither without knowing
	// which asset. OVER and UNDER are FACTS about arithmetic. Whether a fact is
	// good news is an interpretation, and one this layer would get wrong for a
	// whole class of ledgers — so the nature travels and the reader decides.
	Nature string
	// Basis is this row's own — a plan legitimately mixes the two, which is why
	// there is no single basis on the report and why the totals split by it.
	Basis string
	DrCr  string

	Budget decimal.Decimal
	// ActualDebit / ActualCredit are the raw sides, carried so a reader (or a live
	// verifier) can tell NETTED_TO_ZERO from UNSPENT and can re-derive Actual
	// rather than trust it.
	ActualDebit  decimal.Decimal
	ActualCredit decimal.Decimal
	// Actual is oriented to DrCr: the figure the budget is actually compared with.
	Actual decimal.Decimal
	// Variance is Actual - Budget. Positive means MORE than planned on the
	// budgeted side. See Nature on why that is not called favourable.
	Variance decimal.Decimal
	// VariancePercent is nil when Budget is zero. ⛔ A POINTER ON PURPOSE — see
	// failure mode 3 in the file header. Never populate it with a sentinel.
	VariancePercent *decimal.Decimal

	Status string
	// Note is the operator's own note from the budget line.
	Note string
	// Notes are the REPORT's caveats about this row, which travel with the number.
	Notes []string
}

// BudgetBasisTotal is the subtotal for one basis. ⛔ THE ONLY TOTALS THIS REPORT
// IS ALWAYS WILLING TO STATE — see failure mode 4.
type BudgetBasisTotal struct {
	Basis    string
	Rows     int
	Budget   decimal.Decimal
	Actual   decimal.Decimal
	Variance decimal.Decimal
}

// BudgetVariance is the whole report for one budget at one grain.
type BudgetVariance struct {
	BudgetID   uuid.UUID
	BudgetName string
	// The budget's OWN period. ⛔ THE REPORT DOES NOT NARROW IT — see the note in
	// migration 0085's header: comparing a full-period budget against a shorter
	// window needs a spread rule (straight-line, monthly, seasonal) that Tally
	// makes the operator choose and that these books give no basis to invent.
	FromDate string
	ToDate   string
	Dimension string
	// CategoryID / CategoryName are set for the COST_CENTRE dimension only.
	CategoryID   uuid.UUID
	CategoryName string

	Rows []BudgetVarianceRow
	// Unbudgeted are leaf-level actuals no budget line covers. Their Budget is
	// zero and their VariancePercent is nil, by construction. ⛔ A SEPARATE ARRAY
	// AND NOT ROWS WITH A ZERO BUDGET — see failure mode 2.
	Unbudgeted []BudgetVarianceRow

	// Totals per basis, always. See failure mode 4.
	Totals []BudgetBasisTotal
	// GrandTotal* are set ONLY when every row shares one basis. When the plan
	// mixes bases they stay zero and TotalsAreAddable is false, because a position
	// plus a flow is not a quantity.
	TotalsAreAddable bool
	TotalBudget      decimal.Decimal
	TotalActual      decimal.Decimal
	TotalVariance    decimal.Decimal

	// UnbudgetedActual is the total of the unbudgeted column. It is ALWAYS
	// addable — every unbudgeted figure is measured the same way (see
	// unbudgetedBasis) and none of them overlaps another.
	UnbudgetedActual decimal.Decimal
	UnbudgetedCount  int

	// Counts by status, so a screen can lead with "4 lines overspent, 11
	// untouched" without walking the rows.
	StatusCounts map[string]int

	// Notes are the report-level caveats. A screen may style them; it may not
	// decide whether to show them.
	Notes []string
}

// BudgetVarianceFor builds the budget-vs-actual report for ONE budget at ONE
// grain.
//
// categoryID is REQUIRED for BudgetDimensionCostCentre and ignored otherwise;
// uuid.Nil there is a CostAxisError, not a default. See the file header.
func (s *Service) BudgetVarianceFor(
	ctx context.Context, companyID, budgetID, categoryID uuid.UUID, dimension string,
) (BudgetVariance, error) {
	if err := scope(ctx, companyID); err != nil {
		return BudgetVariance{}, err
	}
	dim := strings.ToUpper(strings.TrimSpace(dimension))
	found := false
	for _, d := range KnownBudgetDimensions {
		if d == dim {
			found = true
			break
		}
	}
	if !found {
		return BudgetVariance{}, &BudgetDimensionError{Named: dimension}
	}

	out := BudgetVariance{
		BudgetID:         budgetID,
		Dimension:        dim,
		TotalBudget:      decimal.Zero,
		TotalActual:      decimal.Zero,
		TotalVariance:    decimal.Zero,
		UnbudgetedActual: decimal.Zero,
		StatusCounts:     map[string]int{},
	}

	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// THE BUDGET IS RESOLVED BEFORE ANYTHING IS MEASURED, and a bad one is a
		// refusal that names the alternatives. See BudgetSelectionError.
		choices, err := loadBudgetChoices(ctx, tx)
		if err != nil {
			return err
		}
		if budgetID == uuid.Nil {
			return &BudgetSelectionError{Missing: true, Available: choices}
		}
		var picked *BudgetChoice
		for i := range choices {
			if choices[i].ID == budgetID {
				picked = &choices[i]
				break
			}
		}
		if picked == nil {
			return &BudgetSelectionError{Named: budgetID, Available: choices}
		}
		out.BudgetName, out.FromDate, out.ToDate = picked.Name, picked.FromDate, picked.ToDate

		lines, err := loadBudgetLines(ctx, tx, budgetID, dim)
		if err != nil {
			return err
		}

		switch dim {
		case BudgetDimensionLedger:
			return s.budgetByLedger(ctx, tx, &out, lines)
		case BudgetDimensionGroup:
			return s.budgetByGroup(ctx, tx, &out, lines)
		default:
			return s.budgetByCostCentre(ctx, tx, &out, lines, categoryID)
		}
	})
	if err != nil {
		return BudgetVariance{}, err
	}

	finaliseBudgetVariance(&out)
	return out, nil
}

// ---------------------------------------------------------------------------
// The budget side.
// ---------------------------------------------------------------------------

// budgetLine is one stored line as this report reads it. Reporting does NOT
// import internal/masters (see the package header: it depends only on the db
// interface, never on a sibling write module), so the row shape is spelled here.
type budgetLine struct {
	TargetID   uuid.UUID
	TargetName string
	CategoryID uuid.UUID
	Nature     string
	Basis      string
	DrCr       string
	Amount     decimal.Decimal
	Note       string
}

func loadBudgetChoices(ctx context.Context, tx pgx.Tx) ([]BudgetChoice, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, name, to_char(from_date, 'YYYY-MM-DD'), to_char(to_date, 'YYYY-MM-DD')
		   FROM budget ORDER BY from_date DESC, lower(btrim(name))`)
	if err != nil {
		return nil, fmt.Errorf("reporting: query budgets: %w", err)
	}
	defer rows.Close()
	var out []BudgetChoice
	for rows.Next() {
		var c BudgetChoice
		if err := rows.Scan(&c.ID, &c.Name, &c.FromDate, &c.ToDate); err != nil {
			return nil, fmt.Errorf("reporting: scan budget: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// loadBudgetLines reads one budget's lines at one grain, with the target's name
// and (for a group or ledger) its nature resolved.
//
// ⚠️ TWO ALIASES ON account_group, deliberately: `g` is a GROUP line's own
// target, `lg` is the group behind a LEDGER line's target. One alias would hand a
// group line the nature of a ledger's parent on any row where both were set.
func loadBudgetLines(ctx context.Context, tx pgx.Tx, budgetID uuid.UUID, dim string) ([]budgetLine, error) {
	rows, err := tx.Query(ctx,
		`SELECT COALESCE(l.account_group_id, l.ledger_id, l.cost_centre_id),
		        COALESCE(g.name, led.name, cc.name, ''),
		        cc.cost_category_id,
		        COALESCE(g.nature::text, lg.nature::text, ''),
		        l.basis, l.dr_cr, l.amount, l.note
		   FROM budget_line l
		   LEFT JOIN account_group g   ON g.id   = l.account_group_id
		   LEFT JOIN ledger        led ON led.id = l.ledger_id
		   LEFT JOIN account_group lg  ON lg.id  = led.group_id
		   LEFT JOIN cost_centre   cc  ON cc.id  = l.cost_centre_id
		  WHERE l.budget_id = $1 AND l.dimension = $2
		  ORDER BY lower(btrim(COALESCE(g.name, led.name, cc.name, ''))), l.id`,
		budgetID, dim)
	if err != nil {
		return nil, fmt.Errorf("reporting: query budget lines: %w", err)
	}
	defer rows.Close()
	var out []budgetLine
	for rows.Next() {
		var (
			bl       budgetLine
			category *uuid.UUID
		)
		if err := rows.Scan(&bl.TargetID, &bl.TargetName, &category, &bl.Nature,
			&bl.Basis, &bl.DrCr, &bl.Amount, &bl.Note); err != nil {
			return nil, fmt.Errorf("reporting: scan budget line: %w", err)
		}
		if category != nil {
			bl.CategoryID = *category
		}
		out = append(out, bl)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// LEDGER
// ---------------------------------------------------------------------------

func (s *Service) budgetByLedger(
	ctx context.Context, tx pgx.Tx, out *BudgetVariance, lines []budgetLine,
) error {
	actuals, err := loadBudgetActualsByBasis(ctx, tx, lines, out.FromDate, out.ToDate)
	if err != nil {
		return err
	}

	budgeted := map[uuid.UUID]bool{}
	for _, bl := range lines {
		budgeted[bl.TargetID] = true
		lb, ok := actuals[bl.Basis][bl.TargetID]
		row := newBudgetRow(BudgetDimensionLedger, bl)
		if ok {
			row.ActualDebit, row.ActualCredit = lb.Debit, lb.Credit
		} else {
			// A ledger with no row in the balance set has no postings at all. Not an
			// error, and not a zero to be silent about — it becomes UNSPENT below.
			row.ActualDebit, row.ActualCredit = decimal.Zero, decimal.Zero
		}
		scoreBudgetRow(&row)
		out.Rows = append(out.Rows, row)
	}

	// UNBUDGETED: every ledger with a non-zero actual and no line. Ledgers do not
	// nest, so coverage is simply membership — see the file header.
	for _, lb := range actuals[unbudgetedBasis(lines)] {
		if budgeted[lb.LedgerID] {
			continue
		}
		out.Unbudgeted = append(out.Unbudgeted, unbudgetedRow(
			BudgetDimensionLedger, lb.LedgerID, lb.LedgerName, lb.Nature,
			uuid.Nil, "", lb.Debit, lb.Credit, unbudgetedBasis(lines)))
	}
	return nil
}

// ---------------------------------------------------------------------------
// GROUP
// ---------------------------------------------------------------------------

func (s *Service) budgetByGroup(
	ctx context.Context, tx pgx.Tx, out *BudgetVariance, lines []budgetLine,
) error {
	actuals, err := loadBudgetActualsByBasis(ctx, tx, lines, out.FromDate, out.ToDate)
	if err != nil {
		return err
	}
	nodes, _, err := loadGroupTree(ctx, tx)
	if err != nil {
		return err
	}

	for _, bl := range lines {
		row := newBudgetRow(BudgetDimensionGroup, bl)
		dr, cr := subtreeTotals(nodes, bl.TargetID, actuals[bl.Basis])
		row.ActualDebit, row.ActualCredit = dr, cr
		scoreBudgetRow(&row)
		out.Rows = append(out.Rows, row)
	}

	// UNBUDGETED at LEDGER grain: a ledger whose ancestor chain contains no
	// budgeted group. Reported at ledger grain deliberately — groups nest, and a
	// list of unbudgeted GROUPS would contain both ancestors and descendants of
	// every budgeted one, each containing the others. See the file header.
	budgetedGroups := map[uuid.UUID]bool{}
	for _, bl := range lines {
		budgetedGroups[bl.TargetID] = true
	}
	basis := unbudgetedBasis(lines)
	for _, lb := range actuals[basis] {
		if groupIsCovered(nodes, lb.GroupID, budgetedGroups) {
			continue
		}
		out.Unbudgeted = append(out.Unbudgeted, unbudgetedRow(
			BudgetDimensionLedger, lb.LedgerID, lb.LedgerName, lb.Nature,
			uuid.Nil, "", lb.Debit, lb.Credit, basis))
	}
	if len(out.Unbudgeted) > 0 {
		out.Notes = append(out.Notes, "The unbudgeted list is at LEDGER grain even though the "+
			"budget is on groups: it is every ledger with movement that no budgeted group "+
			"covers. Groups nest, so a list of unbudgeted GROUPS would contain both the "+
			"ancestors and the descendants of every budgeted one and adding it up would "+
			"count the same money several times.")
	}
	return nil
}

// subtreeTotals sums a group's own ledgers plus every descendant group's,
// bounded by a visited set.
//
// ⛔ THE VISITED SET IS NOT DEFENSIVE PROGRAMMING. account_group.parent_id is a
// self-reference with no cycle constraint beyond the row's own id, so A-under-
// B-under-A is storable. Unbounded recursion on it does not produce a wrong
// number — it hangs the request or exhausts the stack, which is how a report
// stops being available at all. cost_summary.go's buildCostCentreTree bounds its
// walk twice over for the identical reason.
func subtreeTotals(
	nodes map[uuid.UUID]*groupNode, root uuid.UUID, byLedger map[uuid.UUID]ledgerBalance,
) (debit, credit decimal.Decimal) {
	debit, credit = decimal.Zero, decimal.Zero
	if _, ok := nodes[root]; !ok {
		return debit, credit
	}
	seen := map[uuid.UUID]bool{}
	stack := []uuid.UUID{root}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[id] {
			continue
		}
		seen[id] = true
		n, ok := nodes[id]
		if !ok {
			continue
		}
		stack = append(stack, n.children...)
	}
	// One pass over the balances rather than a per-group index: a ledger belongs
	// to exactly one group, so membership of the visited set IS the subtree test.
	for _, lb := range byLedger {
		if seen[lb.GroupID] {
			debit = debit.Add(lb.Debit)
			credit = credit.Add(lb.Credit)
		}
	}
	return debit, credit
}

// groupIsCovered walks a ledger's group up to the root, asking whether any
// ancestor is budgeted. Bounded by a visited set for the reason subtreeTotals is.
func groupIsCovered(nodes map[uuid.UUID]*groupNode, groupID uuid.UUID, budgeted map[uuid.UUID]bool) bool {
	seen := map[uuid.UUID]bool{}
	for id := groupID; ; {
		if budgeted[id] {
			return true
		}
		if seen[id] {
			// A cycle. Report it as UNCOVERED rather than covered: the failure
			// direction that shows a figure is better than the one that hides it.
			return false
		}
		seen[id] = true
		n, ok := nodes[id]
		if !ok || n.parent == nil {
			return false
		}
		id = *n.parent
	}
}

// ---------------------------------------------------------------------------
// COST CENTRE
// ---------------------------------------------------------------------------

func (s *Service) budgetByCostCentre(
	ctx context.Context, tx pgx.Tx, out *BudgetVariance, lines []budgetLine, categoryID uuid.UUID,
) error {
	// ⛔ THE AXIS IS RESOLVED BEFORE ANYTHING IS SUMMED, and a bad one is a
	// refusal that names the alternatives — CostAxisError, reused verbatim from
	// the Cost Centre Summary because the reason is identical. See the file
	// header on why a cross-axis variance total is unrepresentable here.
	axes, err := loadCostAxes(ctx, tx)
	if err != nil {
		return err
	}
	var found bool
	for _, a := range axes {
		if a.ID == categoryID {
			out.CategoryID, out.CategoryName, found = a.ID, a.Name, true
			break
		}
	}
	if categoryID == uuid.Nil {
		return &CostAxisError{Missing: true, Available: axes}
	}
	if !found {
		return &CostAxisError{Named: categoryID, Available: axes}
	}

	// 0085's budget_line_cost_centre_is_transactional guarantees every cost-centre
	// line is NETT_TRANSACTIONS, so there is exactly one basis and one query.
	rows, err := tx.Query(ctx, costCentreFactSQL, nullDate(out.FromDate), nullDate(out.ToDate), categoryID)
	if err != nil {
		return fmt.Errorf("reporting: budget variance cost centres: %w", err)
	}
	defer rows.Close()
	var facts []costCentreFact
	for rows.Next() {
		var (
			fact   costCentreFact
			parent *uuid.UUID
		)
		if err := rows.Scan(&fact.CentreID, &fact.Name, &parent, &fact.Debit, &fact.Credit); err != nil {
			return fmt.Errorf("reporting: scan cost centre: %w", err)
		}
		if parent != nil {
			fact.ParentID = *parent
		}
		facts = append(facts, fact)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// ROLLED UP, not own-only: budgeting "Sales" and letting its sub-teams' spend
	// escape the comparison would make the parent's variance meaningless. This
	// reuses the Cost Centre Summary's own tree so the two reports cannot disagree
	// about what "Sales cost" means.
	roots, unattached, warnings := buildCostCentreTree(facts)
	rolled := map[uuid.UUID]CostCentreNode{}
	flattenCostCentreNodes(roots, rolled)
	flattenCostCentreNodes(unattached, rolled)
	out.Notes = append(out.Notes, warnings...)

	budgetedCentres := map[uuid.UUID]bool{}
	for _, bl := range lines {
		budgetedCentres[bl.TargetID] = true
	}

	for _, bl := range lines {
		row := newBudgetRow(BudgetDimensionCostCentre, bl)
		row.CostCategoryID, row.CostCategoryName = out.CategoryID, out.CategoryName
		if n, ok := rolled[bl.TargetID]; ok {
			row.ActualDebit, row.ActualCredit = n.TotalDebit, n.TotalCredit
			// ⚠️ A BUDGETED CENTRE UNDER ANOTHER BUDGETED CENTRE IS AN OVERLAP AND
			// THE READER MUST BE TOLD. The parent's rolled-up actual CONTAINS the
			// child's, so the two rows are not independent and their budget column
			// cannot be added. Reported per row rather than suppressed, because both
			// rows are individually correct — it is only the sum that is not.
			if centreHasBudgetedAncestor(facts, bl.TargetID, budgetedCentres) {
				row.Notes = append(row.Notes, "This centre sits UNDER another budgeted centre "+
					"in this plan. Its actual is included in that ancestor's rolled-up figure "+
					"as well, so these two rows overlap and their budgets must not be added.")
			}
		} else {
			// The centre is not on this axis (or does not exist). 0085's FK proves it
			// exists, so this is the axis mismatch — and it is a fact, not a zero.
			row.ActualDebit, row.ActualCredit = decimal.Zero, decimal.Zero
			row.Notes = append(row.Notes, fmt.Sprintf(
				"This centre is not on the %s axis, so nothing on this report measures it. "+
					"Its budget is shown but its actual is NOT zero spend — it is unmeasured "+
					"here. Ask for the axis this centre sits on.", out.CategoryName))
		}
		scoreBudgetRow(&row)
		out.Rows = append(out.Rows, row)
	}

	// UNBUDGETED: a centre with non-zero OWN allocations, neither budgeted nor
	// under a budgeted ancestor. OWN and not rolled up, so a parent and its child
	// cannot both appear for the same money.
	for _, f := range facts {
		if f.Debit.IsZero() && f.Credit.IsZero() {
			continue
		}
		if budgetedCentres[f.CentreID] || centreHasBudgetedAncestor(facts, f.CentreID, budgetedCentres) {
			continue
		}
		row := unbudgetedRow(BudgetDimensionCostCentre, f.CentreID, f.Name, "",
			out.CategoryID, out.CategoryName, f.Debit, f.Credit, BudgetBasisNett)
		out.Unbudgeted = append(out.Unbudgeted, row)
	}

	// THE CROSS-AXIS CAVEAT TRAVELS WITH THE DATA, exactly as CostCentreSummary's
	// does. The reader most likely to add two of these together is the one holding
	// two printouts.
	out.Notes = append(out.Notes, "These figures are the "+out.CategoryName+" axis ONLY. The "+
		"same money is allocated in full on every other axis as well, so nothing here may be "+
		"added to another cost category's variance: the sum would be a multiple of the cost "+
		"actually borne.")
	return nil
}

func flattenCostCentreNodes(nodes []CostCentreNode, into map[uuid.UUID]CostCentreNode) {
	for _, n := range nodes {
		into[n.CentreID] = n
		flattenCostCentreNodes(n.Children, into)
	}
}

// centreHasBudgetedAncestor walks a centre's parent chain, bounded by a visited
// set — cost_centre.go refuses to CREATE a cycle but 0040 carries no constraint
// against a multi-hop one, which is why buildCostCentreTree bounds its walk too.
func centreHasBudgetedAncestor(facts []costCentreFact, id uuid.UUID, budgeted map[uuid.UUID]bool) bool {
	parent := map[uuid.UUID]uuid.UUID{}
	for _, f := range facts {
		parent[f.CentreID] = f.ParentID
	}
	seen := map[uuid.UUID]bool{}
	for cur := parent[id]; cur != uuid.Nil; cur = parent[cur] {
		if seen[cur] {
			return false
		}
		seen[cur] = true
		if budgeted[cur] {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Shared scoring.
// ---------------------------------------------------------------------------

// loadBudgetActualsByBasis loads the ledger balance set ONCE PER BASIS THE PLAN
// ACTUALLY USES, keyed by ledger id.
//
// ⛔ THE TWO BASES ARE TWO DIFFERENT QUERIES AND NEITHER CAN STAND IN FOR THE
// OTHER — loadClosingBalances folds in the opening balance and takes a single
// as-at date; loadPeriodMovement excludes it and takes both bounds. Using one
// where the other was meant produces a real, correctly-footed, confidently wrong
// number, which is why 0085 refuses to give `basis` a default.
func loadBudgetActualsByBasis(
	ctx context.Context, tx pgx.Tx, lines []budgetLine, from, to string,
) (map[string]map[uuid.UUID]ledgerBalance, error) {
	need := map[string]bool{}
	for _, bl := range lines {
		need[bl.Basis] = true
	}
	// The unbudgeted sweep needs a basis too, even when the plan has no lines at
	// all — otherwise a budget with nothing in it would report no unbudgeted
	// spend, which is the most misleading answer this report could give.
	need[unbudgetedBasis(lines)] = true

	out := map[string]map[uuid.UUID]ledgerBalance{}
	for basis := range need {
		var (
			balances []ledgerBalance
			err      error
		)
		switch basis {
		case BudgetBasisClosing:
			balances, err = loadClosingBalances(ctx, tx, to)
		default:
			balances, err = loadPeriodMovement(ctx, tx, from, to)
		}
		if err != nil {
			return nil, err
		}
		byID := make(map[uuid.UUID]ledgerBalance, len(balances))
		for _, lb := range balances {
			byID[lb.LedgerID] = lb
		}
		out[basis] = byID
	}
	return out, nil
}

// unbudgetedBasis picks the basis the unbudgeted sweep is measured on.
//
// ⛔ NETT_TRANSACTIONS UNLESS THE PLAN IS ENTIRELY CLOSING_BALANCE, and the
// choice is stated on the report rather than left implicit. "Spend nobody
// planned" is a question about MOVEMENT IN THE PERIOD: measuring it as a closing
// position would list every ledger carrying an opening balance from before the
// books began as unbudgeted, which is true of almost every ledger and would bury
// the handful of rows that matter. When the whole plan is on closing balances the
// nett sweep would be the odd one out, so it follows the plan instead.
func unbudgetedBasis(lines []budgetLine) string {
	if len(lines) == 0 {
		return BudgetBasisNett
	}
	for _, bl := range lines {
		if bl.Basis != BudgetBasisClosing {
			return BudgetBasisNett
		}
	}
	return BudgetBasisClosing
}

func newBudgetRow(dimension string, bl budgetLine) BudgetVarianceRow {
	return BudgetVarianceRow{
		Dimension:      dimension,
		TargetID:       bl.TargetID,
		TargetName:     bl.TargetName,
		CostCategoryID: bl.CategoryID,
		Nature:         bl.Nature,
		Basis:          bl.Basis,
		DrCr:           bl.DrCr,
		Budget:         bl.Amount,
		ActualDebit:    decimal.Zero,
		ActualCredit:   decimal.Zero,
		Note:           bl.Note,
	}
}

func unbudgetedRow(
	dimension string, id uuid.UUID, name, nature string,
	categoryID uuid.UUID, categoryName string,
	debit, credit decimal.Decimal, basis string,
) BudgetVarianceRow {
	row := BudgetVarianceRow{
		Dimension:        dimension,
		TargetID:         id,
		TargetName:       name,
		CostCategoryID:   categoryID,
		CostCategoryName: categoryName,
		Nature:           nature,
		Basis:            basis,
		// The side an unbudgeted actual is REPORTED on is its natural one — there
		// is no budget to take a side from. For a cost centre (no nature) that is
		// debit, which is the side a cost lands on.
		DrCr:         naturalSideForNature(nature),
		Budget:       decimal.Zero,
		ActualDebit:  debit,
		ActualCredit: credit,
	}
	row.Actual = orientedActual(row.DrCr, debit, credit)
	row.Variance = row.Actual
	// ⛔ VariancePercent STAYS NIL. There is no budget to be a percentage of, and
	// this row is precisely the case failure mode 3 names.
	row.Status = BudgetUnbudgeted
	row.Notes = append(row.Notes, "No budget line covers this. It is UNBUDGETED, not 100% over "+
		"budget: the plan is silent about it rather than exceeded, and no percentage of a "+
		"budget that was never set can be stated.")
	return row
}

// naturalSideForNature mirrors reporting.ledgerBalance.signed()'s orientation.
// A cost centre has no nature and takes debit, the side a cost lands on.
func naturalSideForNature(nature string) string {
	switch nature {
	case "LIABILITY", "INCOME":
		return "C"
	default: // ASSET, EXPENSE, and "" (a cost centre)
		return "D"
	}
}

func orientedActual(drCr string, debit, credit decimal.Decimal) decimal.Decimal {
	if drCr == "C" {
		return credit.Sub(debit)
	}
	return debit.Sub(credit)
}

// scoreBudgetRow computes the actual, the variance, the percentage and the
// status. ⛔ THIS FUNCTION IS THE WHOLE OF FAILURE MODES 1 AND 3 — the seven
// statuses are seven distinct facts and none may be collapsed into another. It is
// PURE so it can be tested exhaustively without a database.
func scoreBudgetRow(row *BudgetVarianceRow) {
	row.Actual = orientedActual(row.DrCr, row.ActualDebit, row.ActualCredit)
	row.Variance = row.Actual.Sub(row.Budget)

	// ⛔ THE PERCENTAGE IS COMPUTED ONLY WHERE IT MEANS SOMETHING. A zero budget
	// leaves it nil, which reaches the wire as JSON null and forces a client to
	// render "no budget set" rather than a number it will believe.
	if !row.Budget.IsZero() {
		pct := row.Variance.Div(row.Budget).Mul(decimal.NewFromInt(100)).Round(2)
		row.VariancePercent = &pct
	}

	hadMovement := !row.ActualDebit.IsZero() || !row.ActualCredit.IsZero()
	switch {
	case row.Budget.IsZero():
		// Reached only via unbudgetedRow, which sets the status itself; kept so a
		// future caller cannot land here and get a misleading UNDER.
		row.Status = BudgetUnbudgeted

	case row.Actual.IsNegative():
		row.Status = BudgetContra
		row.Notes = append(row.Notes, fmt.Sprintf(
			"The actual is on the OPPOSITE side from the budget: this was budgeted %s and the "+
				"books show a net %s of %s. That is not \"comfortably under budget\" — it usually "+
				"means a reversal, a refund, or a posting on the wrong side.",
			sideWord(row.DrCr), sideWord(oppositeSide(row.DrCr)), row.Actual.Neg().StringFixed(4)))

	case !hadMovement:
		// ⛔ UNSPENT, NOT "ZERO VARIANCE". Nothing at all was posted.
		row.Status = BudgetUnspent
		row.Notes = append(row.Notes, "Nothing was posted against this at all in the period. "+
			"That is UNSPENT, not a variance of zero — the whole budget is untouched, which is "+
			"a fact about the plan or about what has been keyed, not a sign of accuracy.")

	case row.Actual.IsZero():
		// ⛔ MOVEMENT HAPPENED AND CANCELLED. Distinct from UNSPENT above, and the
		// pair is why every row carries both raw sides.
		row.Status = BudgetNettedToZero
		row.Notes = append(row.Notes, fmt.Sprintf(
			"The nett is zero but this was NOT untouched: %s of debit and %s of credit were "+
				"posted and cancelled exactly. Look for a reversal or a misposting before "+
				"reading this as an unused budget.",
			row.ActualDebit.StringFixed(4), row.ActualCredit.StringFixed(4)))

	case row.Variance.IsZero():
		row.Status = BudgetOnTarget

	case row.Variance.IsPositive():
		row.Status = BudgetOver

	default:
		row.Status = BudgetUnder
	}
}

func sideWord(drCr string) string {
	if drCr == "C" {
		return "credit"
	}
	return "debit"
}

func oppositeSide(drCr string) string {
	if drCr == "C" {
		return "D"
	}
	return "C"
}

// finaliseBudgetVariance computes the subtotals, decides whether a grand total
// may be stated at all, and attaches the report-level caveats.
//
// PURE, and deliberately so: the addability rule in failure mode 4 is the single
// most consequential decision this report makes and it is testable without a
// database.
func finaliseBudgetVariance(out *BudgetVariance) {
	byBasis := map[string]*BudgetBasisTotal{}
	var order []string
	for i := range out.Rows {
		row := &out.Rows[i]
		out.StatusCounts[row.Status]++
		t, ok := byBasis[row.Basis]
		if !ok {
			t = &BudgetBasisTotal{
				Basis: row.Basis, Budget: decimal.Zero,
				Actual: decimal.Zero, Variance: decimal.Zero,
			}
			byBasis[row.Basis] = t
			order = append(order, row.Basis)
		}
		t.Rows++
		t.Budget = t.Budget.Add(row.Budget)
		t.Actual = t.Actual.Add(row.Actual)
		t.Variance = t.Variance.Add(row.Variance)
	}
	sort.Strings(order)
	for _, b := range order {
		out.Totals = append(out.Totals, *byBasis[b])
	}

	for i := range out.Unbudgeted {
		out.StatusCounts[out.Unbudgeted[i].Status]++
		out.UnbudgetedActual = out.UnbudgetedActual.Add(out.Unbudgeted[i].Actual)
	}
	out.UnbudgetedCount = len(out.Unbudgeted)

	// ⛔⛔ THE GRAND TOTAL EXISTS ONLY WHEN EVERY ROW SHARES ONE BASIS. A
	// CLOSING_BALANCE figure is a POSITION and a NETT_TRANSACTIONS figure is a
	// FLOW; their sum is not a quantity, and a footer showing one would be the
	// single most believable wrong number on the page.
	switch len(out.Totals) {
	case 0:
		out.TotalsAreAddable = true
	case 1:
		out.TotalsAreAddable = true
		out.TotalBudget = out.Totals[0].Budget
		out.TotalActual = out.Totals[0].Actual
		out.TotalVariance = out.Totals[0].Variance
		out.Notes = append(out.Notes, "Every line in this plan is measured on "+
			basisSentence(out.Totals[0].Basis)+" so the totals below are addable.")
	default:
		out.TotalsAreAddable = false
		out.Notes = append(out.Notes, "⛔ THIS PLAN MIXES BOTH BASES, SO THERE IS NO GRAND "+
			"TOTAL. A closing-balance budget is a POSITION as at "+out.ToDate+" and a "+
			"nett-transactions budget is MOVEMENT within the period; adding them would not "+
			"produce a quantity. Use the per-basis subtotals.")
	}

	if len(out.Rows) == 0 {
		out.Notes = append(out.Notes, "This budget has no lines at this grain, so there is "+
			"nothing to compare. Every actual below is listed as unbudgeted — that is the "+
			"absence of a plan, not a plan that was met.")
	}
	if out.UnbudgetedCount > 0 {
		out.Notes = append(out.Notes, fmt.Sprintf(
			"%d actual(s) totalling %s are covered by no budget line, measured on %s. They are "+
				"listed in full and are NOT included in the totals above: they are the plan's "+
				"gaps, not variances against it.",
			out.UnbudgetedCount, out.UnbudgetedActual.StringFixed(4),
			basisSentence(unbudgetedBasisOf(out))))
	}
	// ⛔ THE PERIOD CAVEAT ALWAYS TRAVELS. It is the limitation most likely to be
	// forgotten by a reader looking at a year's plan in month four.
	out.Notes = append(out.Notes, "The budget column is this plan's WHOLE period ("+
		out.FromDate+" to "+out.ToDate+") and is not apportioned to any shorter window. "+
		"Comparing a full-period budget against part-period actuals needs a spread rule "+
		"(straight-line, monthly or seasonal) that nobody has chosen, so this report does "+
		"not narrow the period rather than inventing one.")
	// ⛔ AND THE ONE THAT KEEPS A SCREEN FROM COLOURING THE PAGE RED AND GREEN.
	out.Notes = append(out.Notes, "OVER and UNDER are statements about arithmetic, not about "+
		"good and bad. Spending more than budget and EARNING more than budget are both OVER. "+
		"Each row carries the target's nature so a reader can tell which it is.")
}

// unbudgetedBasisOf reads the basis the unbudgeted rows were measured on back off
// the rows themselves, so the sentence cannot disagree with the data.
func unbudgetedBasisOf(out *BudgetVariance) string {
	if len(out.Unbudgeted) == 0 {
		return BudgetBasisNett
	}
	return out.Unbudgeted[0].Basis
}

func basisSentence(basis string) string {
	if basis == BudgetBasisClosing {
		return "CLOSING BALANCE (a position as at the period end, opening balance folded in),"
	}
	return "NETT TRANSACTIONS (movement inside the period, opening balance excluded),"
}
