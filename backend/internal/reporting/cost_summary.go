package reporting

// THE TWO COST SUMMARIES — "which department bore this cost", and "what am I
// missing". Schema: migration 0040_cost_centres.sql.
//
// ===========================================================================
// ⛔⛔ A CATEGORY IS AN AXIS. A CENTRE IS A POSITION ON THAT AXIS.
// ===========================================================================
// This is the one thing a report over these tables can get wrong, and it is
// wrong in a direction that still foots. Multiple cost categories exist in order
// to allocate the SAME money IN PARALLEL: one Rs 1,00,000 salary posting is
// allocated in full across Departments (Sales 60,000 / Admin 40,000) AND AGAIN
// IN FULL across Branches (Mumbai 70,000 / Delhi 30,000). Both allocations are
// complete, both are correct, and they describe the same money from two
// directions.
//
// Three consequences, and every line of SQL below obeys them:
//
//  1. THE SUM-TO-POSTING INVARIANT IS PER (POSTING, CATEGORY), NEVER PER
//     POSTING. The deferred constraint trigger cost_allocation_sums_to_posting
//     groups on exactly that pair; so does every GROUP BY in this file.
//  2. AN ALLOCATION BELONGS TO BOTH a centre and a category. cost_centre_id
//     alone is not enough to group by, which is why the centre query's join key
//     carries cost_category_id even though the composite FK already guarantees
//     agreement.
//  3. ⛔ A REPORT THAT SUMS ACROSS CATEGORIES DOUBLE-COUNTS. There is no
//     legitimate "total allocated" figure spanning axes: summing the example
//     above gives 2,00,000 against 1,00,000 of cost. So the Cost Centre Summary
//     takes ONE axis and refuses to guess, and the Cost Category Summary reports
//     one ROW per axis and deliberately carries NO grand total. The guard lives
//     in summariseCostCategories (a pure function) rather than in the SQL, so
//     that it can be mutation-proved without a database —
//     TestOnePostingOnTwoAxesReportsOneHundredPercentTwiceNotTwoHundred.
//
// This shape is inherited by T11 (budgets) and T24, so an error here costs three
// tracks rather than one.
//
// ===========================================================================
// WHAT THE CATEGORY SUMMARY IS FOR, WHICH IS NOT WHAT IT LOOKS LIKE
// ===========================================================================
// It is not a second, coarser Cost Centre Summary. It exists to make an
// INCOMPLETE ALLOCATION VISIBLE, and that is its whole purpose.
//
// The trigger guarantees that a posting allocated on an axis is allocated in
// FULL. NOTHING forces a posting to be allocated on an axis AT ALL — allocation
// is optional per ledger, and the trigger fires on the allocation rather than on
// the posting, so a posting with no allocation rows is perfectly legal and
// perfectly silent. That silence is the gap an operator is actually asking
// about: "I allocated the April salaries by department — did I miss any?"
//
// So each row carries, for its axis, the period's ALLOCABLE base, what was
// ALLOCATED, and the UNALLOCATED REMAINDER — and the remainder is computed
// INDEPENDENTLY (postings carrying no allocation on that axis) rather than by
// subtraction, so the two can be compared. They must agree TO THE PAISA. No
// tolerance: a tolerance is how a real misallocation hides for a year.
//
// ALLOCABLE means what cost_category.allocate_non_revenue says it means. That
// column is Tally's "Allocate Revenue Items / Allocate Non-Revenue Items" pair
// collapsed to one question — may this axis be used on a balance-sheet ledger,
// or only on a P&L one — so an axis with allocate_non_revenue = FALSE has a
// denominator of the period's P&L postings only (account_group.is_pl), and one
// with TRUE has every posting. Getting this wrong would report a Department axis
// as 8% complete because it counted every bank receipt as something the axis
// ought to have carried.
//
// ===========================================================================
// I-9: CURRENT VOUCHER VERSIONS ONLY, AND STRUCTURALLY
// ===========================================================================
// Every query here reaches `posting` through the SAME
// voucher_head.current_version_id join movementSQL uses, so an ALTERed voucher
// contributes once at its new amount and a DELETED one contributes nothing.
// posting_cost_allocation keys on posting_id and `posting` rows are re-created
// wholesale by every ALTER, so a superseded version's allocations still exist —
// they are excluded because their POSTING is not on the current version, not
// because anything deleted them. The printed Day Book has the opposite defect
// (it lists versions) and it is a known open product call; do not copy it.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// MaxCostCentreDepth bounds the hierarchy walk.
//
// ⚠️ IT IS A BOUND ON A WALK, NOT A BUSINESS RULE. cost_centre.parent_id is a
// self-reference with only cost_centre_not_its_own_parent to stop the trivial
// loop; a two-row cycle (A parents B, B parents A) is accepted by the database.
// The masters lane rejects cycles at entry, but a report that ASSUMED acyclicity
// would be one bad row — or one future re-parent path that forgets the check —
// away from a request that never returns. A hung report is worse than a report
// that says "these centres could not be placed", so the walk is bounded and the
// unplaced centres are listed with their figures instead of being lost.
//
// 64 is far past any real chart of cost centres (Tally's own group tree is three
// or four deep in practice) and far short of anything that costs time to walk.
const MaxCostCentreDepth = 64

// CostAxis is one cost category, named: an AXIS of analysis.
type CostAxis struct {
	ID   uuid.UUID
	Name string
	// AllocateNonRevenue is migration 0040's collapsed form of Tally's
	// "Allocate Non-Revenue Items": FALSE means this axis belongs on P&L
	// ledgers only, and it is what decides the ALLOCABLE denominator below.
	AllocateNonRevenue bool
}

// CostAxisError is the refusal when the Cost Centre Summary has not been told
// WHICH axis to report, or has been told one that does not exist.
//
// It carries the axes that DO exist because the alternative refusals are both
// worse: silently picking one puts a real, correctly-rendered report for the
// wrong axis on screen with nothing saying so, and silently summing all of them
// reports twice the cost (see the header). Naming the choices is the only refusal
// that leaves the operator able to act.
type CostAxisError struct {
	// Missing distinguishes "you did not say which axis" (a malformed request)
	// from "that axis is not in these books" (a request about something absent).
	// The HTTP seam maps the first to 400 and the second to 422.
	Missing bool
	// Named is the id that was asked for, when one was.
	Named uuid.UUID
	// Available is every axis this company has, in the order the report lists
	// them.
	Available []CostAxis
}

func (e *CostAxisError) Error() string {
	var b strings.Builder
	if e.Missing {
		b.WriteString("a Cost Centre Summary is always for ONE cost category, and this request did not say which: ")
	} else {
		b.WriteString("cost category " + e.Named.String() + " is not in these books: ")
	}
	if len(e.Available) == 0 {
		b.WriteString("this company has no cost categories at all, so there is nothing to " +
			"summarise. Create a category (Department, Branch, Project) and the centres on " +
			"it before asking for this report")
		return b.String()
	}
	b.WriteString("pass ?cost_category_id= one of ")
	for i, a := range e.Available {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(a.Name)
		b.WriteString(" (")
		b.WriteString(a.ID.String())
		b.WriteString(")")
	}
	b.WriteString(". It cannot be defaulted and the axes cannot be added together: " +
		"the same money is allocated IN FULL on every axis, so summing Departments and " +
		"Branches would report twice the cost you actually bore")
	return b.String()
}

// ---------------------------------------------------------------------------
// 1. THE COST CENTRE SUMMARY — one axis, rolled up through the hierarchy
// ---------------------------------------------------------------------------

// CostCentreNode is one centre on the axis, with its own figures and the figures
// of everything under it.
//
// OWN and TOTAL are both carried because a reader needs both and cannot derive
// either from the other: a parent centre may hold allocations of its own AND
// have children, and a screen that showed only the total could not tell an
// operator whether "Sales 60,000" was keyed against Sales or against its
// sub-teams.
type CostCentreNode struct {
	CentreID uuid.UUID
	Name     string
	// ParentID is uuid.Nil for a centre at the top of the axis. A nil uuid is
	// never a real id, so no sentinel is needed beside it.
	ParentID uuid.UUID
	// Depth is 0 for a top-level centre. Carried so a flat renderer can indent
	// without walking, and so the bounded walk has something to report about.
	Depth int
	// OwnDebit / OwnCredit are the allocations keyed against THIS centre in the
	// period. They are MAGNITUDES split by the POSTING's dr_cr:
	// posting_cost_allocation.amount is always positive and the posting carries
	// the direction, exactly as stock_movement leaves the sign to `direction`.
	OwnDebit  decimal.Decimal
	OwnCredit decimal.Decimal
	// TotalDebit / TotalCredit are Own plus every descendant's Total.
	TotalDebit  decimal.Decimal
	TotalCredit decimal.Decimal
	Children    []CostCentreNode
}

// OwnNet is what this centre alone bore: debits less credits.
func (n CostCentreNode) OwnNet() decimal.Decimal { return n.OwnDebit.Sub(n.OwnCredit) }

// TotalNet is what this centre and everything under it bore.
func (n CostCentreNode) TotalNet() decimal.Decimal { return n.TotalDebit.Sub(n.TotalCredit) }

// CostCentreSummary is the report: ONE axis, over a closed period.
type CostCentreSummary struct {
	CategoryID         uuid.UUID
	CategoryName       string
	AllocateNonRevenue bool
	// FromDate / ToDate are echoed so a client cannot render a period it did not
	// get. Either may be "" for unbounded; both bounds are INCLUSIVE, which is
	// the convention period_test.go pins for every statement in this package.
	FromDate string
	ToDate   string
	// Roots are the top-level centres, each carrying its own subtree.
	Roots []CostCentreNode
	// Unattached are centres the bounded walk could not place — a parent chain
	// that loops, or one deeper than MaxCostCentreDepth. They are LISTED WITH
	// THEIR FIGURES rather than dropped, because a centre that silently vanishes
	// from a cost report is money that silently vanishes with it.
	Unattached []CostCentreNode
	// TotalDebit / TotalCredit are the sum of the ROOTS' rolled-up totals.
	//
	// ⛔ NOT the sum of every node. A parent's figure is already inside its own
	// Total and inside nothing else; adding every node together would count each
	// child once on its own line and again inside its parent, which is the
	// hierarchy version of the cross-axis double count.
	TotalDebit  decimal.Decimal
	TotalCredit decimal.Decimal
	// UnattachedDebit / UnattachedCredit are the Unattached centres' OWN figures.
	UnattachedDebit  decimal.Decimal
	UnattachedCredit decimal.Decimal
	// AllocatedDebit / AllocatedCredit are the FLAT sum of every centre's OWN
	// figures on this axis — computed without the tree at all.
	//
	// This is the report's INDEPENDENT ORACLE, not decoration. The tree total is
	// built by a recursive walk; the flat total is built by addition. If a
	// re-parent, a cycle or an off-by-one in the walk ever lost a centre, these
	// two disagree and Difference says by how much.
	AllocatedDebit  decimal.Decimal
	AllocatedCredit decimal.Decimal
	// DifferenceDebit / DifferenceCredit are Allocated less (Total + Unattached),
	// per side. Zero by construction, asserted rather than assumed, and reported
	// in Warnings with the figure when it is not — never absorbed.
	DifferenceDebit  decimal.Decimal
	DifferenceCredit decimal.Decimal
	// Warnings are the things this report has to SAY, in rupees, rather than
	// leave to a reader to notice. They travel with the data for the same reason
	// the TDS register's disclosures do: the reader who needs the caveat is the
	// one holding the artefact.
	Warnings []string
}

// Net is the axis's net cost: rolled-up debits less credits.
func (s CostCentreSummary) Net() decimal.Decimal { return s.TotalDebit.Sub(s.TotalCredit) }

// Reconciles reports whether the tree roll-up agrees with the flat sum of the
// same allocations, to the paisa. Callers gate on it the way they gate on
// TrialBalance.IsBalanced.
func (s CostCentreSummary) Reconciles() bool {
	return s.DifferenceDebit.IsZero() && s.DifferenceCredit.IsZero()
}

// costCentreFact is one centre and its OWN allocations in the period, as the SQL
// returns it. The tree is built from these in Go so that the roll-up and the
// bounded walk can be tested without a database.
type costCentreFact struct {
	CentreID uuid.UUID
	Name     string
	ParentID uuid.UUID // uuid.Nil for a top-level centre
	Debit    decimal.Decimal
	Credit   decimal.Decimal
}

// costCentreFactSQL reads every centre ON ONE AXIS with the allocations keyed
// against it inside the period.
//
// $1 = from (may be NULL), $2 = to (may be NULL), $3 = cost_category_id.
//
// FOUR THINGS IN HERE ARE LOAD-BEARING:
//
//   - `cc.cost_category_id = $3` — ONE AXIS. Dropping it would list every
//     centre in the company and invite a caller to total them, which is the
//     cross-axis double count.
//   - `a.cost_category_id = cc.cost_category_id` is IN THE JOIN KEY, beside the
//     centre, even though posting_cost_allocation_centre_matches_category_fk
//     already guarantees the pair agrees. An allocation belongs to BOTH a centre
//     and an axis; a join keyed on the centre alone would be a query whose
//     correctness depended entirely on a constraint declared in another file.
//   - LEFT JOIN, twice. A centre with no allocations in the period is a real row
//     showing zero — it may be the PARENT the roll-up needs — and dropping it
//     would silently re-root its children.
//   - `cur` is the current-version posting set. An allocation whose posting
//     belongs to a superseded version, or to a voucher outside the period,
//     joins to nothing and contributes nothing.
const costCentreFactSQL = `
	WITH cur AS (
	    SELECT p.id AS posting_id, p.dr_cr::text AS dr_cr
	      FROM posting p
	      JOIN voucher_version vv ON vv.id = p.voucher_version_id
	      JOIN voucher_head vh
	        ON vh.company_id = vv.company_id
	       AND vh.voucher_id = vv.voucher_id
	       AND vh.current_version_id = vv.id
	     WHERE vh.is_deleted = false
	       AND ($1::date IS NULL OR vv.voucher_date >= $1::date)
	       AND ($2::date IS NULL OR vv.voucher_date <= $2::date)
	)
	SELECT cc.id, cc.name, cc.parent_id,
	       COALESCE(SUM(CASE WHEN cur.dr_cr = 'D' THEN a.amount END), 0) AS debit,
	       COALESCE(SUM(CASE WHEN cur.dr_cr = 'C' THEN a.amount END), 0) AS credit
	  FROM cost_centre cc
	  LEFT JOIN posting_cost_allocation a
	         ON a.company_id       = cc.company_id
	        AND a.cost_centre_id   = cc.id
	        AND a.cost_category_id = cc.cost_category_id
	  LEFT JOIN cur ON cur.posting_id = a.posting_id
	 WHERE cc.cost_category_id = $3
	 GROUP BY cc.id, cc.name, cc.parent_id
	 ORDER BY lower(btrim(cc.name)), cc.id`

// CostCentreSummaryFor builds the Cost Centre Summary for ONE axis over a
// period. Both bounds are inclusive; either may be "" for unbounded.
//
// categoryID = uuid.Nil is a REFUSAL and not a default — see CostAxisError.
func (s *Service) CostCentreSummaryFor(
	ctx context.Context, companyID, categoryID uuid.UUID, from, to string,
) (CostCentreSummary, error) {
	if err := scope(ctx, companyID); err != nil {
		return CostCentreSummary{}, err
	}
	f, t, err := costPeriod(from, to)
	if err != nil {
		return CostCentreSummary{}, err
	}

	out := CostCentreSummary{
		CategoryID:       categoryID,
		FromDate:         f,
		ToDate:           t,
		TotalDebit:       decimal.Zero,
		TotalCredit:      decimal.Zero,
		UnattachedDebit:  decimal.Zero,
		UnattachedCredit: decimal.Zero,
		AllocatedDebit:   decimal.Zero,
		AllocatedCredit:  decimal.Zero,
	}
	var facts []costCentreFact

	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		axes, err := loadCostAxes(ctx, tx)
		if err != nil {
			return err
		}
		// THE AXIS IS RESOLVED BEFORE ANYTHING IS SUMMED, and a bad one is a
		// refusal that names the alternatives. An empty report for an axis that
		// does not exist reads as "nothing was allocated", which is a different
		// and reassuring claim.
		var found bool
		for _, a := range axes {
			if a.ID == categoryID {
				out.CategoryName, out.AllocateNonRevenue, found = a.Name, a.AllocateNonRevenue, true
				break
			}
		}
		if categoryID == uuid.Nil {
			return &CostAxisError{Missing: true, Available: axes}
		}
		if !found {
			return &CostAxisError{Named: categoryID, Available: axes}
		}

		rows, err := tx.Query(ctx, costCentreFactSQL, nullDate(f), nullDate(t), categoryID)
		if err != nil {
			return fmt.Errorf("reporting: cost centre summary: %w", err)
		}
		defer rows.Close()
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
		return rows.Err()
	})
	if err != nil {
		return CostCentreSummary{}, err
	}

	// THE FLAT ORACLE, computed before the tree exists and never from it.
	for _, fact := range facts {
		out.AllocatedDebit = out.AllocatedDebit.Add(fact.Debit)
		out.AllocatedCredit = out.AllocatedCredit.Add(fact.Credit)
	}

	roots, unattached, warnings := buildCostCentreTree(facts)
	out.Roots, out.Unattached, out.Warnings = roots, unattached, warnings
	for _, n := range roots {
		out.TotalDebit = out.TotalDebit.Add(n.TotalDebit)
		out.TotalCredit = out.TotalCredit.Add(n.TotalCredit)
	}
	for _, n := range unattached {
		out.UnattachedDebit = out.UnattachedDebit.Add(n.OwnDebit)
		out.UnattachedCredit = out.UnattachedCredit.Add(n.OwnCredit)
	}
	out.DifferenceDebit = out.AllocatedDebit.Sub(out.TotalDebit).Sub(out.UnattachedDebit)
	out.DifferenceCredit = out.AllocatedCredit.Sub(out.TotalCredit).Sub(out.UnattachedCredit)
	if !out.Reconciles() {
		// SAY SO, WITH THE FIGURE. Absorbing it is how a lost centre becomes a
		// cost report that is quietly short for a year.
		out.Warnings = append(out.Warnings, fmt.Sprintf(
			"THIS SUMMARY DOES NOT RECONCILE. The centres on this axis hold %s of debit and "+
				"%s of credit, but the roll-up accounts for %s and %s — %s of debit and %s of "+
				"credit belong to a centre this report failed to place. Do not use these "+
				"totals until that is explained.",
			out.AllocatedDebit.StringFixed(4), out.AllocatedCredit.StringFixed(4),
			out.TotalDebit.Add(out.UnattachedDebit).StringFixed(4),
			out.TotalCredit.Add(out.UnattachedCredit).StringFixed(4),
			out.DifferenceDebit.StringFixed(4), out.DifferenceCredit.StringFixed(4)))
	}
	// THE CROSS-AXIS CAVEAT TRAVELS WITH THE DATA. A screen may style it; it may
	// not decide whether to show it, because the reader most likely to add two of
	// these reports together is the one holding two printouts.
	out.Warnings = append(out.Warnings, "These figures are the "+out.CategoryName+
		" axis ONLY. The same money is allocated in full on every other axis as well, "+
		"so this total must never be added to another cost category's: the sum would be "+
		"a multiple of the cost actually borne.")
	return out, nil
}

// buildCostCentreTree turns the flat centre facts into a forest with rolled-up
// totals, and returns the centres it could not place.
//
// PURE, AND DELIBERATELY SO. The roll-up arithmetic and the walk bound are the
// two things in this file most likely to be wrong and least likely to be noticed
// (a wrong roll-up still foots at the leaves), so they are testable without a
// database — cost_summary_test.go.
//
// THE WALK IS BOUNDED TWICE OVER, because the two failure modes are different:
//
//   - A DEPTH BOUND stops a legitimately-shaped but absurdly deep chain, and a
//     chain that has been made circular BELOW a real root.
//   - Everything not REACHED from a root is unattached. A pure cycle (A parents
//     B, B parents A) has no root at all, so no depth bound could ever see it —
//     it is caught by the reachability sweep instead. Either failure yields a
//     LISTED centre with its figures, never a hung request and never a
//     silently-dropped amount.
func buildCostCentreTree(facts []costCentreFact) (roots, unattached []CostCentreNode, warnings []string) {
	byID := make(map[uuid.UUID]costCentreFact, len(facts))
	for _, f := range facts {
		byID[f.CentreID] = f
	}

	children := make(map[uuid.UUID][]uuid.UUID, len(facts))
	var rootIDs []uuid.UUID
	var orphaned []uuid.UUID
	for _, f := range facts {
		switch {
		case f.ParentID == uuid.Nil:
			rootIDs = append(rootIDs, f.CentreID)
		case f.ParentID == f.CentreID:
			// cost_centre_not_its_own_parent forbids this, so reaching it means
			// the constraint is gone. Treat it as an orphan rather than looping.
			orphaned = append(orphaned, f.CentreID)
		default:
			if _, ok := byID[f.ParentID]; !ok {
				// cost_centre_parent_shares_axis_fk makes this unreachable — a
				// parent is always on the same axis and the query selected the
				// whole axis. Handled anyway: a re-rooted centre would silently
				// leave the report otherwise.
				orphaned = append(orphaned, f.CentreID)
				continue
			}
			children[f.ParentID] = append(children[f.ParentID], f.CentreID)
		}
	}

	// Stable order everywhere: by folded name, then by id. A report that reorders
	// itself between two identical requests cannot be diffed by a human or
	// golden-tested by a machine.
	byName := func(ids []uuid.UUID) {
		sort.Slice(ids, func(i, j int) bool {
			a, b := byID[ids[i]], byID[ids[j]]
			ai, bi := strings.ToLower(strings.TrimSpace(a.Name)), strings.ToLower(strings.TrimSpace(b.Name))
			if ai != bi {
				return ai < bi
			}
			return a.CentreID.String() < b.CentreID.String()
		})
	}
	byName(rootIDs)
	byName(orphaned)
	for k := range children {
		byName(children[k])
	}

	placed := make(map[uuid.UUID]bool, len(facts))
	var tooDeep []uuid.UUID

	var build func(id uuid.UUID, depth int) CostCentreNode
	build = func(id uuid.UUID, depth int) CostCentreNode {
		f := byID[id]
		placed[id] = true
		n := CostCentreNode{
			CentreID:    id,
			Name:        f.Name,
			ParentID:    f.ParentID,
			Depth:       depth,
			OwnDebit:    f.Debit,
			OwnCredit:   f.Credit,
			TotalDebit:  f.Debit,
			TotalCredit: f.Credit,
		}
		if depth >= MaxCostCentreDepth {
			// STOP DESCENDING, and do not pretend the subtree is empty: the
			// children stay unplaced and are swept into Unattached below with
			// their own figures, so the amounts are still on the report.
			if len(children[id]) > 0 {
				tooDeep = append(tooDeep, id)
			}
			return n
		}
		for _, childID := range children[id] {
			if placed[childID] {
				// A child already placed means the parent map is not a forest.
				// Refuse to walk it twice — that is how the same rupees appear in
				// two subtrees and the total exceeds what was allocated.
				continue
			}
			c := build(childID, depth+1)
			n.TotalDebit = n.TotalDebit.Add(c.TotalDebit)
			n.TotalCredit = n.TotalCredit.Add(c.TotalCredit)
			n.Children = append(n.Children, c)
		}
		return n
	}
	for _, id := range rootIDs {
		roots = append(roots, build(id, 0))
	}

	// THE REACHABILITY SWEEP. Anything still unplaced is in a cycle, under a
	// cycle, below the depth bound, or orphaned. All four are the same thing to a
	// reader — "this centre is not under any parent" — so they are reported
	// together, in the same stable order.
	unreachedIDs := make([]uuid.UUID, 0, len(orphaned))
	for _, f := range facts {
		if !placed[f.CentreID] {
			unreachedIDs = append(unreachedIDs, f.CentreID)
		}
	}
	byName(unreachedIDs)
	unattachedDebit, unattachedCredit := decimal.Zero, decimal.Zero
	for _, id := range unreachedIDs {
		f := byID[id]
		unattached = append(unattached, CostCentreNode{
			CentreID: id, Name: f.Name, ParentID: f.ParentID, Depth: 0,
			OwnDebit: f.Debit, OwnCredit: f.Credit,
			TotalDebit: f.Debit, TotalCredit: f.Credit,
		})
		unattachedDebit = unattachedDebit.Add(f.Debit)
		unattachedCredit = unattachedCredit.Add(f.Credit)
	}
	if len(unattached) > 0 {
		names := make([]string, 0, len(unattached))
		for _, n := range unattached {
			names = append(names, n.Name)
		}
		warnings = append(warnings, fmt.Sprintf(
			"%d cost centre(s) holding %s of debit and %s of credit are NOT under any parent "+
				"and are listed separately: %s. Their parent chain either loops back on itself or "+
				"is deeper than %d levels, so the roll-up cannot place them. The rolled-up totals "+
				"EXCLUDE them — read the two together, and fix the parent of each before treating "+
				"this axis as complete.",
			len(unattached), unattachedDebit.StringFixed(4), unattachedCredit.StringFixed(4),
			strings.Join(names, ", "), MaxCostCentreDepth))
	}
	if len(tooDeep) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"%d cost centre(s) sit exactly %d levels deep and their children were not walked. "+
				"A chart of cost centres that deep is a data fault (usually a parent pointed back "+
				"into its own ancestors), not a business structure.",
			len(tooDeep), MaxCostCentreDepth))
	}
	return roots, unattached, warnings
}

// ---------------------------------------------------------------------------
// 2. THE COST CATEGORY SUMMARY — one row per axis, and the gap
// ---------------------------------------------------------------------------

// CostCategoryRow is ONE AXIS: how much of the period's allocable cost it
// accounts for, and how much it says nothing about.
type CostCategoryRow struct {
	CategoryID         uuid.UUID
	CategoryName       string
	AllocateNonRevenue bool
	// ALLOCABLE is the DENOMINATOR: the period's postings this axis may be
	// allocated on — P&L postings only when AllocateNonRevenue is false, every
	// posting when it is true. See the file header for why the flag decides it.
	AllocableDebit    decimal.Decimal
	AllocableCredit   decimal.Decimal
	AllocablePostings int
	// ALLOCATED is what this axis actually carries, summed from the allocations
	// themselves, grouped by (posting, category) exactly as the trigger groups.
	AllocatedDebit    decimal.Decimal
	AllocatedCredit   decimal.Decimal
	AllocatedPostings int
	// UNALLOCATED is the REMAINDER, and it is the whole point of this report.
	// Computed INDEPENDENTLY — postings carrying no allocation at all on this
	// axis — rather than as Allocable minus Allocated, so the two can be
	// compared and the report can assert its own arithmetic.
	UnallocatedDebit    decimal.Decimal
	UnallocatedCredit   decimal.Decimal
	UnallocatedPostings int
	// PARTLY ALLOCATED postings should be IMPOSSIBLE: the deferred trigger
	// cost_allocation_sums_to_posting refuses an allocation set that does not
	// total the posting exactly. Counted anyway, because the only way this is
	// non-zero is that the guarantee is gone (a dropped trigger, a restore that
	// skipped it), and that is precisely when a report must be loud.
	PartlyAllocatedPostings  int
	PartlyAllocatedShortfall decimal.Decimal
	// OFF-AXIS allocations name a posting this axis may NOT be allocated on: a
	// balance-sheet ledger allocated on a revenue-only axis. The database does
	// not enforce allocate_non_revenue, so this is where the contradiction
	// surfaces instead of quietly inflating the coverage.
	OffAxisDebit    decimal.Decimal
	OffAxisCredit   decimal.Decimal
	OffAxisPostings int
	// DIFFERENCE is Allocable less Allocated less Unallocated, per side. Zero to
	// the paisa or the row says so — no tolerance.
	DifferenceDebit  decimal.Decimal
	DifferenceCredit decimal.Decimal
	// Notes are this row's gap and contradictions, in rupees.
	Notes []string
}

// Reconciles reports whether the independently-computed remainder agrees with
// the allocable base less what was allocated, to the paisa.
func (r CostCategoryRow) Reconciles() bool {
	return r.DifferenceDebit.IsZero() && r.DifferenceCredit.IsZero()
}

// IsFullyAllocated reports whether every allocable posting in the period carries
// an allocation on this axis.
func (r CostCategoryRow) IsFullyAllocated() bool {
	return r.UnallocatedPostings == 0 &&
		r.UnallocatedDebit.IsZero() && r.UnallocatedCredit.IsZero()
}

// CostCategorySummary is one row per axis, over a period.
//
// ⛔ THERE IS NO GRAND TOTAL FIELD AND THERE MUST NEVER BE ONE. Rows describe
// the SAME money from different directions; a total across them is a multiple of
// the cost borne. The Notes say so on the artefact, because the field's absence
// only tells a programmer.
type CostCategorySummary struct {
	FromDate string
	ToDate   string
	Rows     []CostCategoryRow
	Notes    []string
}

// Reconciles reports whether every row's arithmetic holds.
func (s CostCategorySummary) Reconciles() bool {
	for _, r := range s.Rows {
		if !r.Reconciles() {
			return false
		}
	}
	return true
}

// costAxisBase is one axis's DENOMINATOR and its independently-computed
// remainder, as the SQL returns them.
type costAxisBase struct {
	Axis                CostAxis
	AllocableDebit      decimal.Decimal
	AllocableCredit     decimal.Decimal
	AllocablePostings   int
	UnallocatedDebit    decimal.Decimal
	UnallocatedCredit   decimal.Decimal
	UnallocatedPostings int
}

// costAllocationFact is ONE (POSTING, CATEGORY) PAIR — the grain the invariant
// is defined on, and the grain this report groups on.
//
// ⚠️ IT IS NOT ONE POSTING. The same posting appears here once per axis it is
// allocated on, each time with that axis's own complete total. That is not
// duplication to be de-duplicated; it is parallel allocation, and collapsing
// these to one row per posting is exactly the mistake the file header describes.
type costAllocationFact struct {
	CategoryID    uuid.UUID
	PostingID     uuid.UUID
	DrCr          string
	PostingAmount decimal.Decimal
	// Allocated is the total allocated on THIS axis for THIS posting — the sum
	// the trigger compares against PostingAmount.
	Allocated decimal.Decimal
	// Allocable is whether this axis may be allocated on this posting's ledger
	// at all (allocate_non_revenue OR the group is a P&L group).
	Allocable bool
}

// costAllocationFactSQL reads the allocations of the period, one row per
// (posting, category).
//
// ⛔ THE GROUP BY CARRIES cost_category_id. That single column is the difference
// between "each axis accounts for the whole posting" and "this posting was
// allocated twice over". It is the same grouping migration 0040's trigger uses,
// and for the same reason.
//
// $1 = from, $2 = to (either may be NULL).
const costAllocationFactSQL = `
	SELECT a.cost_category_id, a.posting_id, p.dr_cr::text, p.amount,
	       SUM(a.amount) AS allocated,
	       (c.allocate_non_revenue OR g.is_pl) AS allocable
	  FROM posting_cost_allocation a
	  JOIN cost_category c ON c.company_id = a.company_id AND c.id = a.cost_category_id
	  JOIN posting p       ON p.company_id = a.company_id AND p.id = a.posting_id
	  JOIN voucher_version vv ON vv.id = p.voucher_version_id
	  JOIN voucher_head vh
	    ON vh.company_id = vv.company_id
	   AND vh.voucher_id = vv.voucher_id
	   AND vh.current_version_id = vv.id
	  JOIN ledger l        ON l.company_id = p.company_id AND l.id = p.ledger_id
	  JOIN account_group g ON g.company_id = l.company_id AND g.id = l.group_id
	 WHERE vh.is_deleted = false
	   AND ($1::date IS NULL OR vv.voucher_date >= $1::date)
	   AND ($2::date IS NULL OR vv.voucher_date <= $2::date)
	 GROUP BY a.cost_category_id, a.posting_id, p.dr_cr, p.amount,
	          c.allocate_non_revenue, g.is_pl`

// costAxisBaseSQL reads, PER AXIS, the period's allocable base and the part of
// it carrying no allocation on that axis.
//
// THE CONDITIONAL LEFT JOIN IS THE ALLOCABLE RULE, in SQL:
// `ON (c.allocate_non_revenue OR cur.is_pl)` pairs each axis with exactly the
// postings it is entitled to carry. An axis with no allocable postings still
// returns a row with zeros, because "this axis accounts for nothing this period"
// is an answer and a missing row is not.
//
// `ab` pre-aggregates allocations per (posting, category) so "unallocated" is a
// join miss rather than a correlated subquery per pair — and, again, that
// aggregate is grouped BY CATEGORY.
//
// $1 = from, $2 = to (either may be NULL).
const costAxisBaseSQL = `
	WITH cur AS (
	    SELECT p.id AS posting_id, p.amount AS amount, p.dr_cr::text AS dr_cr, g.is_pl AS is_pl
	      FROM posting p
	      JOIN voucher_version vv ON vv.id = p.voucher_version_id
	      JOIN voucher_head vh
	        ON vh.company_id = vv.company_id
	       AND vh.voucher_id = vv.voucher_id
	       AND vh.current_version_id = vv.id
	      JOIN ledger l        ON l.company_id = p.company_id AND l.id = p.ledger_id
	      JOIN account_group g ON g.company_id = l.company_id AND g.id = l.group_id
	     WHERE vh.is_deleted = false
	       AND ($1::date IS NULL OR vv.voucher_date >= $1::date)
	       AND ($2::date IS NULL OR vv.voucher_date <= $2::date)
	),
	ab AS (
	    SELECT a.cost_category_id, a.posting_id
	      FROM posting_cost_allocation a
	     GROUP BY a.cost_category_id, a.posting_id
	)
	SELECT c.id, c.name, c.allocate_non_revenue,
	       COALESCE(SUM(CASE WHEN cur.dr_cr = 'D' THEN cur.amount END), 0) AS allocable_debit,
	       COALESCE(SUM(CASE WHEN cur.dr_cr = 'C' THEN cur.amount END), 0) AS allocable_credit,
	       COUNT(cur.posting_id) AS allocable_postings,
	       COALESCE(SUM(CASE WHEN cur.dr_cr = 'D' AND ab.posting_id IS NULL
	                         THEN cur.amount END), 0) AS unallocated_debit,
	       COALESCE(SUM(CASE WHEN cur.dr_cr = 'C' AND ab.posting_id IS NULL
	                         THEN cur.amount END), 0) AS unallocated_credit,
	       COUNT(cur.posting_id) FILTER (WHERE ab.posting_id IS NULL) AS unallocated_postings
	  FROM cost_category c
	  LEFT JOIN cur ON (c.allocate_non_revenue OR cur.is_pl)
	  LEFT JOIN ab  ON ab.cost_category_id = c.id AND ab.posting_id = cur.posting_id
	 GROUP BY c.id, c.name, c.allocate_non_revenue
	 ORDER BY lower(btrim(c.name)), c.id`

// CostCategorySummaryFor builds the per-axis summary over a period. Both bounds
// are inclusive; either may be "" for unbounded.
func (s *Service) CostCategorySummaryFor(
	ctx context.Context, companyID uuid.UUID, from, to string,
) (CostCategorySummary, error) {
	if err := scope(ctx, companyID); err != nil {
		return CostCategorySummary{}, err
	}
	f, t, err := costPeriod(from, to)
	if err != nil {
		return CostCategorySummary{}, err
	}

	var (
		bases []costAxisBase
		facts []costAllocationFact
	)
	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, costAxisBaseSQL, nullDate(f), nullDate(t))
		if err != nil {
			return fmt.Errorf("reporting: cost category base: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var b costAxisBase
			if err := rows.Scan(&b.Axis.ID, &b.Axis.Name, &b.Axis.AllocateNonRevenue,
				&b.AllocableDebit, &b.AllocableCredit, &b.AllocablePostings,
				&b.UnallocatedDebit, &b.UnallocatedCredit, &b.UnallocatedPostings); err != nil {
				return fmt.Errorf("reporting: scan cost category base: %w", err)
			}
			bases = append(bases, b)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("reporting: cost category base: %w", err)
		}

		frows, err := tx.Query(ctx, costAllocationFactSQL, nullDate(f), nullDate(t))
		if err != nil {
			return fmt.Errorf("reporting: cost allocations: %w", err)
		}
		defer frows.Close()
		for frows.Next() {
			var fact costAllocationFact
			if err := frows.Scan(&fact.CategoryID, &fact.PostingID, &fact.DrCr,
				&fact.PostingAmount, &fact.Allocated, &fact.Allocable); err != nil {
				return fmt.Errorf("reporting: scan cost allocation: %w", err)
			}
			facts = append(facts, fact)
		}
		return frows.Err()
	})
	if err != nil {
		return CostCategorySummary{}, err
	}

	out := CostCategorySummary{
		FromDate: f,
		ToDate:   t,
		Rows:     summariseCostCategories(bases, facts),
	}
	out.Notes = costCategoryNotes(out.Rows)
	return out, nil
}

// summariseCostCategories folds the allocation facts onto their OWN axis and
// nothing else.
//
// ⛔⛔ THIS IS THE DOUBLE-COUNT GUARD, AND IT IS THE ONE LINE THAT MATTERS:
// `row, ok := byAxis[f.CategoryID]`. A fact is added to the axis it names and to
// no other. Fold every fact onto every axis — or drop the CategoryID from the
// key — and a Rs 1,00,000 salary allocated across Departments AND across
// Branches reports Rs 2,00,000 on both rows, telling a shopkeeper their April
// salary bill was twice what they paid.
// TestOnePostingOnTwoAxesReportsOneHundredPercentTwiceNotTwoHundred is the
// mutation proof, and it is the most important test in this file.
//
// PURE. bases carries the denominators the database computed per axis; facts
// carries the numerators. Neither is derived from the other, which is what makes
// DifferenceDebit/Credit worth asserting.
func summariseCostCategories(bases []costAxisBase, facts []costAllocationFact) []CostCategoryRow {
	out := make([]CostCategoryRow, 0, len(bases))
	byAxis := make(map[uuid.UUID]int, len(bases))
	for _, b := range bases {
		byAxis[b.Axis.ID] = len(out)
		out = append(out, CostCategoryRow{
			CategoryID:               b.Axis.ID,
			CategoryName:             b.Axis.Name,
			AllocateNonRevenue:       b.Axis.AllocateNonRevenue,
			AllocableDebit:           b.AllocableDebit,
			AllocableCredit:          b.AllocableCredit,
			AllocablePostings:        b.AllocablePostings,
			AllocatedDebit:           decimal.Zero,
			AllocatedCredit:          decimal.Zero,
			UnallocatedDebit:         b.UnallocatedDebit,
			UnallocatedCredit:        b.UnallocatedCredit,
			UnallocatedPostings:      b.UnallocatedPostings,
			PartlyAllocatedShortfall: decimal.Zero,
			OffAxisDebit:             decimal.Zero,
			OffAxisCredit:            decimal.Zero,
			DifferenceDebit:          decimal.Zero,
			DifferenceCredit:         decimal.Zero,
		})
	}

	for _, f := range facts {
		i, ok := byAxis[f.CategoryID]
		if !ok {
			// An allocation naming an axis the base query did not return. Both
			// queries read cost_category unfiltered inside one transaction, so
			// this is unreachable; skipping is the safe direction because adding
			// it anywhere else would be the double count by another route.
			continue
		}
		r := &out[i]
		debit := f.DrCr == "D"
		if !f.Allocable {
			// A contradiction, not a total: this axis is not entitled to this
			// posting's ledger. Kept out of AllocatedDebit/Credit so it cannot
			// inflate the coverage of the allocable base it is not part of.
			if debit {
				r.OffAxisDebit = r.OffAxisDebit.Add(f.Allocated)
			} else {
				r.OffAxisCredit = r.OffAxisCredit.Add(f.Allocated)
			}
			r.OffAxisPostings++
			continue
		}
		if debit {
			r.AllocatedDebit = r.AllocatedDebit.Add(f.Allocated)
		} else {
			r.AllocatedCredit = r.AllocatedCredit.Add(f.Allocated)
		}
		r.AllocatedPostings++
		if !f.Allocated.Equal(f.PostingAmount) {
			r.PartlyAllocatedPostings++
			r.PartlyAllocatedShortfall = r.PartlyAllocatedShortfall.
				Add(f.PostingAmount.Sub(f.Allocated))
		}
	}

	for i := range out {
		r := &out[i]
		r.DifferenceDebit = r.AllocableDebit.Sub(r.AllocatedDebit).Sub(r.UnallocatedDebit)
		r.DifferenceCredit = r.AllocableCredit.Sub(r.AllocatedCredit).Sub(r.UnallocatedCredit)
		r.Notes = costCategoryRowNotes(*r)
	}
	return out
}

// costCategoryRowNotes words one axis's gap. In RUPEES and in the operator's
// question — "what am I missing" — never as a constraint name.
func costCategoryRowNotes(r CostCategoryRow) []string {
	var notes []string
	switch {
	case r.AllocablePostings == 0:
		notes = append(notes, "Nothing in this period could be allocated on this axis. "+
			r.allocableRuleSentence())
	case r.IsFullyAllocated():
		notes = append(notes, fmt.Sprintf(
			"Every one of the %d posting(s) this axis covers in the period carries an "+
				"allocation, and each is allocated in full. Nothing is missing on this axis.",
			r.AllocablePostings))
	default:
		// ⚠️ WHERE THE GAP SITS DEPENDS ON THE AXIS'S OWN RULE, and this sentence
		// used to assert "it is in the Profit & Loss" unconditionally. For an axis
		// with allocate_non_revenue the denominator is EVERY posting in the period,
		// so the gap may sit on a balance-sheet posting instead — and the very next
		// sentence, allocableRuleSentence(), said exactly that. The note
		// contradicted itself on the one class of axis where the operator most
		// needs to know which ledgers to go and look at.
		where := "it is in the Profit & Loss"
		if r.AllocateNonRevenue {
			where = "it is somewhere in this period's postings, which for this axis means the " +
				"balance sheet as well as the Profit & Loss"
		}
		notes = append(notes, fmt.Sprintf(
			"%s of debit and %s of credit — %d of the %d posting(s) this axis covers — carry "+
				"NO %s allocation at all. That money is real cost with no %s attached to it: "+
				"%s and it is missing from every %s report. %s",
			r.UnallocatedDebit.StringFixed(4), r.UnallocatedCredit.StringFixed(4),
			r.UnallocatedPostings, r.AllocablePostings,
			r.CategoryName, r.CategoryName, where, r.CategoryName,
			r.allocableRuleSentence()))
	}
	if r.PartlyAllocatedPostings > 0 {
		notes = append(notes, fmt.Sprintf(
			"⛔ %d posting(s) are allocated on this axis but not in full: %s is unattributed. "+
				"This should be impossible — the database refuses a part-allocated posting at "+
				"commit — so treat it as a broken installation and not as a data-entry mistake.",
			r.PartlyAllocatedPostings, r.PartlyAllocatedShortfall.StringFixed(4)))
	}
	if r.OffAxisPostings > 0 {
		notes = append(notes, fmt.Sprintf(
			"%d posting(s) carrying %s of debit and %s of credit are allocated on this axis "+
				"although it is marked for revenue items only, so they are counted in neither "+
				"the figures above nor the gap. Either allow non-revenue items on this axis or "+
				"move those allocations.",
			r.OffAxisPostings, r.OffAxisDebit.StringFixed(4), r.OffAxisCredit.StringFixed(4)))
	}
	if !r.Reconciles() {
		notes = append(notes, fmt.Sprintf(
			"⛔ THIS ROW DOES NOT RECONCILE. The allocable base is %s Dr / %s Cr, of which %s / "+
				"%s is allocated and %s / %s is not — leaving %s / %s unexplained. Do not act on "+
				"this axis's coverage figure.",
			r.AllocableDebit.StringFixed(4), r.AllocableCredit.StringFixed(4),
			r.AllocatedDebit.StringFixed(4), r.AllocatedCredit.StringFixed(4),
			r.UnallocatedDebit.StringFixed(4), r.UnallocatedCredit.StringFixed(4),
			r.DifferenceDebit.StringFixed(4), r.DifferenceCredit.StringFixed(4)))
	}
	return notes
}

// allocableRuleSentence states, in one line, WHICH postings this axis was
// measured against — because a coverage figure whose denominator is unstated is
// a percentage of nothing in particular.
func (r CostCategoryRow) allocableRuleSentence() string {
	if r.AllocateNonRevenue {
		return "This axis allows non-revenue items, so it is measured against EVERY posting in the period."
	}
	return "This axis is for revenue items only, so it is measured against the period's " +
		"Profit & Loss postings and not against balance-sheet ones."
}

// costCategoryNotes states what the REPORT cannot tell you. The first line is
// the one the whole design rests on.
func costCategoryNotes(rows []CostCategoryRow) []string {
	notes := []string{
		// ⛔ THE ONE THAT MUST NEVER BE REMOVED. There is no total row above, and
		// the absence of a field only informs a programmer.
		"NO TOTAL IS SHOWN ACROSS THESE ROWS AND NONE CAN BE. Each row is a different " +
			"AXIS through the SAME money: Rs 1,00,000 of salary allocated across Departments " +
			"and again across Branches is Rs 1,00,000 of cost described twice, not Rs 2,00,000. " +
			"Read one row at a time.",
		"An axis being COMPLETE means every posting it covers carries an allocation. The " +
			"database guarantees the other half — that a posting allocated on an axis is " +
			"allocated in full — so the only gap it cannot see, and the only gap this report " +
			"exists to show, is a posting nobody allocated at all.",
	}
	var incomplete []string
	for _, r := range rows {
		if r.AllocablePostings > 0 && !r.IsFullyAllocated() {
			incomplete = append(incomplete, r.CategoryName)
		}
	}
	if len(rows) == 0 {
		notes = append(notes, "This company has no cost categories, so no cost analysis is "+
			"being kept. Every posting in the period is unallocated by definition.")
	}
	if len(incomplete) > 0 {
		notes = append(notes, "Incomplete on: "+strings.Join(incomplete, ", ")+
			". The per-row note says how much is missing on each.")
	}
	return notes
}

// ---------------------------------------------------------------------------
// Shared
// ---------------------------------------------------------------------------

// loadCostAxes reads every cost category in scope, in the order the reports list
// them. Used to resolve the requested axis and to NAME the alternatives in a
// refusal.
func loadCostAxes(ctx context.Context, tx pgx.Tx) ([]CostAxis, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, name, allocate_non_revenue
		   FROM cost_category
		  ORDER BY lower(btrim(name)), id`)
	if err != nil {
		return nil, fmt.Errorf("reporting: load cost categories: %w", err)
	}
	defer rows.Close()
	var out []CostAxis
	for rows.Next() {
		var a CostAxis
		if err := rows.Scan(&a.ID, &a.Name, &a.AllocateNonRevenue); err != nil {
			return nil, fmt.Errorf("reporting: scan cost category: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reporting: load cost categories: %w", err)
	}
	return out, nil
}

// CostAxesFor lists the axes a company keeps. Offered because "which axes are
// there" is the question a caller has to answer before it can ask for a Cost
// Centre Summary at all.
func (s *Service) CostAxesFor(ctx context.Context, companyID uuid.UUID) ([]CostAxis, error) {
	if err := scope(ctx, companyID); err != nil {
		return nil, err
	}
	var out []CostAxis
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = loadCostAxes(ctx, tx)
		return err
	})
	return out, err
}

// costPeriod validates a CLOSED interval of dates, INCLUSIVE at both ends,
// which is the convention period_test.go pins for every statement in this
// package ("1 April to 30 June" includes everything posted on 1 April and on
// 30 June — the SQL says `>= $1` and `<= $2`, never `<`).
//
// Either bound may be "" for unbounded, exactly as the Profit & Loss allows: a
// cost analysis of the whole book is a legitimate question. An unparseable date
// is a refusal, never a silent fall back to "no bound" — a report that quietly
// ignored the period asked for would answer a different question and still look
// right.
func costPeriod(from, to string) (string, string, error) {
	f, t := strings.TrimSpace(from), strings.TrimSpace(to)
	var fromT, toT time.Time
	var err error
	if f != "" {
		if fromT, err = time.Parse("2006-01-02", f); err != nil {
			return "", "", fmt.Errorf("reporting: from date %q is not yyyy-mm-dd", from)
		}
	}
	if t != "" {
		if toT, err = time.Parse("2006-01-02", t); err != nil {
			return "", "", fmt.Errorf("reporting: to date %q is not yyyy-mm-dd", to)
		}
	}
	if f != "" && t != "" && toT.Before(fromT) {
		return "", "", fmt.Errorf("reporting: the period ends (%s) before it begins (%s)", t, f)
	}
	return f, t, nil
}

// AsCostAxisError extracts a *CostAxisError from an error chain, so the HTTP
// seam can choose a status code without importing errors-handling knowledge of
// this package's internals into three places.
func AsCostAxisError(err error) (*CostAxisError, bool) {
	var ae *CostAxisError
	if errors.As(err, &ae) {
		return ae, true
	}
	return nil, false
}
