// Package reporting derives read-only financial statements from the
// incrementally-maintained ledger_balance table, the immutable voucher_version
// log, and bill_allocation. It depends only on the db query interface, never on
// sibling write modules.
//
// Reporting mirrors Tally's universal report grammar (RESEARCH sec 8): every
// report is a drill-down tree where a node expands group -> ledger -> voucher.
// The five Phase 1 statements — Balance Sheet, Profit & Loss, Trial Balance,
// Day Book and Outstandings — all share this tree shape so a UI can render and
// drill them uniformly.
//
// Sign convention. A ledger's closing balance is its opening balance plus the
// net of its posted transactions, oriented by the nature of its group:
//
//   - ASSET / EXPENSE ledgers are debit-natured: balance = debit - credit.
//   - LIABILITY / INCOME ledgers are credit-natured: balance = credit - debit.
//
// A positive balance therefore means "on the natural side". The Balance Sheet
// places assets against liabilities + capital, with the P&L net result folded
// in as the balancing figure (Tally shows it under Liabilities when a profit).
package reporting

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/platform/db"
)

// TxRunner is the minimal database capability reporting needs.
type TxRunner interface {
	RunInTx(ctx context.Context, fn db.TxFunc) error
}

// Service is the reporting application service.
type Service struct {
	runner TxRunner
}

// NewService constructs a reporting Service over the given transaction runner.
func NewService(runner TxRunner) *Service {
	return &Service{runner: runner}
}

// scope validates that the requested companyID matches the company bound to the
// context, so a caller cannot read a tenant it is not scoped to. The GUC guard
// in db.RunInTx is the authoritative backstop; this is a fast, friendly check.
func scope(ctx context.Context, companyID uuid.UUID) error {
	scoped, ok := db.CompanyFromContext(ctx)
	if !ok {
		return db.ErrNoCompany
	}
	if scoped != companyID {
		return fmt.Errorf("reporting: company %s not in scope %s", companyID, scoped)
	}
	return nil
}

// --------------------------------------------------------------------------
// Shared ledger-balance model
// --------------------------------------------------------------------------

// ledgerBalance is the closing position of one ledger, used to build every
// statement. It carries the raw debit/credit totals (opening folded in) plus
// the group it rolls up under.
type ledgerBalance struct {
	LedgerID   uuid.UUID
	LedgerName string
	GroupID    uuid.UUID
	GroupName  string
	Nature     string // ASSET / LIABILITY / INCOME / EXPENSE
	IsPL       bool
	Debit      decimal.Decimal
	Credit     decimal.Decimal
}

// signed returns the closing balance oriented by the ledger's nature: positive
// when on the natural side (debit for asset/expense, credit for liability/
// income).
func (lb ledgerBalance) signed() decimal.Decimal {
	switch lb.Nature {
	case "ASSET", "EXPENSE":
		return lb.Debit.Sub(lb.Credit)
	default: // LIABILITY, INCOME
		return lb.Credit.Sub(lb.Debit)
	}
}

// PERIOD SEMANTICS — the reason there are two loaders and not one flag.
//
// A period means something DIFFERENT to each statement, and collapsing them
// onto one "from/to" would produce confidently wrong numbers:
//
//   - The Balance Sheet and the Trial Balance are POSITIONS AS AT a date. They
//     need the opening balance plus EVERY movement up to `asOn` — including
//     movement before a `from` the user typed. Applying a lower bound to them
//     would silently drop the entity's own history and still foot, which is the
//     worst kind of wrong.
//   - The Profit & Loss is a statement FOR A PERIOD. It needs movement inside
//     [from, to] and must NOT fold in the opening balance, which is a position
//     carried from before the books began, not income earned this quarter.
//
// So `loadClosingBalances` takes one date and `loadPeriodMovement` takes two,
// and neither can be called with the other's meaning by accident.
//
// BOTH derive from the POSTINGS of current, non-deleted voucher versions rather
// than reading the materialised `ledger_balance` table, which has no date
// dimension. For the unbounded case the two must agree exactly — that identity
// is asserted as a gate (TestDerivedBalancesMatchTheMaterialisedTable), because
// a derivation that quietly disagreed with the running totals would make every
// report depend on which code path produced it.

// movementSQL is the shared derivation of posted debit/credit per ledger,
// bounded by an optional date range. $1 = from, $2 = to; either may be NULL.
const movementSQL = `
	SELECT p.ledger_id,
	       SUM(CASE WHEN p.dr_cr = 'D' THEN p.amount ELSE 0 END) AS debit,
	       SUM(CASE WHEN p.dr_cr = 'C' THEN p.amount ELSE 0 END) AS credit
	  FROM posting p
	  JOIN voucher_version vv ON vv.id = p.voucher_version_id
	  JOIN voucher_head vh
	    ON vh.company_id = vv.company_id
	   AND vh.voucher_id = vv.voucher_id
	   AND vh.current_version_id = vv.id
	 WHERE vh.is_deleted = false
	   -- The vh join above is what actually does the work: it keeps only the
	   -- CURRENT version, so an ALTERed voucher counts once at its new amount
	   -- and a DELETED one contributes nothing (its tombstone version carries
	   -- zero posting rows). is_deleted is belt-and-braces, kept because the
	   -- query DSL's postings entity states the same guard and the two must
	   -- not drift. TestBalancesFollowAltersAndDeletes pins the real invariant.
	   AND ($1::date IS NULL OR vv.voucher_date >= $1::date)
	   AND ($2::date IS NULL OR vv.voucher_date <= $2::date)
	 GROUP BY p.ledger_id`

// nullDate turns "" into a NULL bound so one query serves bounded and unbounded
// callers without string-building SQL.
func nullDate(d string) any {
	if strings.TrimSpace(d) == "" {
		return nil
	}
	return strings.TrimSpace(d)
}

// loadClosingBalances reads every ledger's POSITION AS AT `asOn` — its opening
// balance plus all posted movement up to and including that date. `asOn` empty
// means "everything ever posted", which is what callers got before periods
// existed. Substrate for the Trial Balance and the Balance Sheet.
func loadClosingBalances(ctx context.Context, tx pgx.Tx, asOn string) ([]ledgerBalance, error) {
	return queryBalances(ctx, tx, nil, nullDate(asOn), true)
}

// loadPeriodMovement reads every ledger's MOVEMENT WITHIN [from, to], with the
// opening balance deliberately excluded. Substrate for the Profit & Loss.
func loadPeriodMovement(ctx context.Context, tx pgx.Tx, from, to string) ([]ledgerBalance, error) {
	return queryBalances(ctx, tx, nullDate(from), nullDate(to), false)
}

// loadLedgerBalances is the unbounded, opening-inclusive form — the behaviour
// every caller had before periods existed.
func loadLedgerBalances(ctx context.Context, tx pgx.Tx) ([]ledgerBalance, error) {
	return loadClosingBalances(ctx, tx, "")
}

func queryBalances(
	ctx context.Context, tx pgx.Tx, from, to any, foldOpening bool,
) ([]ledgerBalance, error) {
	// AN OPENING BALANCE IS DATED TOO — as at company.books_beginning_from.
	// It carries no date column of its own, so an as-at query that folded it in
	// unconditionally would report a position for a date BEFORE the books
	// existed: the live verifier caught exactly that, a Trial Balance dated
	// 2020 showing 520000 of opening capital on a company whose books begin in
	// 2026. Excluding openings is ALL-OR-NOTHING (they balance among
	// themselves), so the statement still foots either way.
	if foldOpening && to != nil {
		var begins string
		// Name the company explicitly rather than trusting `LIMIT 1` over
		// whatever RLS happens to leave visible. It is correct today — company
		// is FORCE ROW LEVEL SECURITY — but a caller that ran without the
		// tenant GUC, or any policy that widened `company`, would silently pick
		// an arbitrary tenant's books-beginning date and mis-date the opening
		// cutoff on someone else's Balance Sheet. current_setting(...,true)
		// yields NULL when unset, which matches no row and falls through to the
		// ErrNoRows branch below — the existing fail-safe.
		err := tx.QueryRow(ctx,
			`SELECT to_char(COALESCE(books_beginning_from, fy_start_date), 'YYYY-MM-DD')
			   FROM company
			  WHERE id = current_setting('app.current_company_id', true)::uuid`).Scan(&begins)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// No company row in scope; leave the opening in rather than guess.
		case err != nil:
			return nil, fmt.Errorf("reporting: read books beginning date: %w", err)
		default:
			if asOn, ok := to.(string); ok && asOn < begins {
				foldOpening = false
			}
		}
	}

	rows, err := tx.Query(ctx,
		`SELECT l.id, l.name, g.id, g.name, g.nature::text, g.is_pl,
		        l.opening_balance, l.opening_dc,
		        COALESCE(m.debit, 0), COALESCE(m.credit, 0)
		   FROM ledger l
		   JOIN account_group g ON g.id = l.group_id
		   LEFT JOIN (`+movementSQL+`) m ON m.ledger_id = l.id
		  ORDER BY g.name, l.name`, from, to)
	if err != nil {
		return nil, fmt.Errorf("reporting: query ledger balances: %w", err)
	}
	defer rows.Close()

	var out []ledgerBalance
	for rows.Next() {
		var (
			lb      ledgerBalance
			openBal decimal.Decimal
			openDC  string
		)
		if err := rows.Scan(&lb.LedgerID, &lb.LedgerName, &lb.GroupID, &lb.GroupName,
			&lb.Nature, &lb.IsPL, &openBal, &openDC, &lb.Debit, &lb.Credit); err != nil {
			return nil, fmt.Errorf("reporting: scan ledger balance: %w", err)
		}
		// Fold the opening balance into the debit/credit totals so a CLOSING
		// figure is self-contained. A period statement (P&L) must not: an
		// opening balance is a position carried from before the books began,
		// not movement inside the period being reported.
		if foldOpening {
			if openDC == "C" {
				lb.Credit = lb.Credit.Add(openBal)
			} else {
				lb.Debit = lb.Debit.Add(openBal)
			}
		}
		out = append(out, lb)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reporting: iterate ledger balances: %w", err)
	}
	return out, nil
}

// --------------------------------------------------------------------------
// Trial Balance
// --------------------------------------------------------------------------

// TrialBalanceRow is a single ledger's line on the Trial Balance.
type TrialBalanceRow struct {
	LedgerID   uuid.UUID
	LedgerName string
	GroupName  string
	Debit      decimal.Decimal
	Credit     decimal.Decimal
}

// TrialBalanceGroup is a group-wise rollup of its ledgers (Tally's default
// group-wise Trial Balance, RESEARCH sec 8). Ledgers carries the per-ledger
// drill-down rows.
type TrialBalanceGroup struct {
	GroupID   uuid.UUID
	GroupName string
	Debit     decimal.Decimal
	Credit    decimal.Decimal
	Ledgers   []TrialBalanceRow
}

// TrialBalance is the full Trial Balance: group-wise rollups (each expandable to
// its ledgers), the flat per-ledger rows, and the column totals.
type TrialBalance struct {
	Groups      []TrialBalanceGroup
	Rows        []TrialBalanceRow
	TotalDebit  decimal.Decimal
	TotalCredit decimal.Decimal
}

// IsBalanced reports whether total debits equal total credits.
func (tb TrialBalance) IsBalanced() bool {
	return tb.TotalDebit.Equal(tb.TotalCredit)
}

// TrialBalance rolls up every ledger's closing position into a Trial Balance,
// presented both group-wise (default Tally view) and as flat rows. Opening
// balances are folded in, so the totals reconcile with the Balance Sheet + P&L.
func (s *Service) TrialBalance(ctx context.Context, companyID uuid.UUID, asOn string) (TrialBalance, error) {
	if err := scope(ctx, companyID); err != nil {
		return TrialBalance{}, err
	}

	var tb TrialBalance
	tb.TotalDebit = decimal.Zero
	tb.TotalCredit = decimal.Zero

	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		balances, err := loadClosingBalances(ctx, tx, asOn)
		if err != nil {
			return err
		}
		// Group the ledgers; preserve the query's (group, ledger) order.
		groupIdx := make(map[uuid.UUID]int)
		for _, lb := range balances {
			// Net the ledger to a single side for the Trial Balance.
			net := lb.Debit.Sub(lb.Credit)
			row := TrialBalanceRow{
				LedgerID:   lb.LedgerID,
				LedgerName: lb.LedgerName,
				GroupName:  lb.GroupName,
			}
			if net.IsNegative() {
				row.Credit = net.Neg()
			} else {
				row.Debit = net
			}
			tb.Rows = append(tb.Rows, row)
			tb.TotalDebit = tb.TotalDebit.Add(row.Debit)
			tb.TotalCredit = tb.TotalCredit.Add(row.Credit)

			i, ok := groupIdx[lb.GroupID]
			if !ok {
				i = len(tb.Groups)
				groupIdx[lb.GroupID] = i
				tb.Groups = append(tb.Groups, TrialBalanceGroup{
					GroupID:   lb.GroupID,
					GroupName: lb.GroupName,
					Debit:     decimal.Zero,
					Credit:    decimal.Zero,
				})
			}
			g := &tb.Groups[i]
			g.Ledgers = append(g.Ledgers, row)
			g.Debit = g.Debit.Add(row.Debit)
			g.Credit = g.Credit.Add(row.Credit)
		}
		return nil
	})
	if err != nil {
		return TrialBalance{}, err
	}
	return tb, nil
}

// --------------------------------------------------------------------------
// Drill-down tree (Balance Sheet / P&L)
// --------------------------------------------------------------------------

// NodeKind classifies a node in a statement tree so a UI knows how to render
// and drill it.
type NodeKind string

const (
	// NodeGroup is an account-group rollup (expands to sub-groups / ledgers).
	NodeGroup NodeKind = "GROUP"
	// NodeLedger is a posting ledger (expands to its vouchers).
	NodeLedger NodeKind = "LEDGER"
	// NodeComputed is a derived line such as "Net Profit" or "Difference in
	// Opening Balances" that has no single underlying ledger.
	NodeComputed NodeKind = "COMPUTED"
)

// StatementNode is one line in a financial-statement tree. Group and computed
// nodes carry Children; ledger nodes are leaves that drill to vouchers via
// LedgerVouchers. Amount is the node's closing balance, oriented to the section
// it appears under (positive = on that section's natural side).
type StatementNode struct {
	Kind     NodeKind
	ID       uuid.UUID // group id or ledger id; nil for computed
	Name     string
	Amount   decimal.Decimal
	Children []StatementNode
}

// Section is one side of a two-sided statement (e.g. Assets vs Liabilities, or
// Income vs Expenses).
type Section struct {
	Title string
	Nodes []StatementNode
	Total decimal.Decimal
}

// groupKey indexes the group tree while building a statement.
type groupNode struct {
	id       uuid.UUID
	name     string
	parent   *uuid.UUID
	nature   string
	isPL     bool
	children []uuid.UUID
	ledgers  []ledgerBalance
	// computed are derived children that are not ledgers: a figure this group is
	// worth that no voucher posted to it. Today that is Closing Stock, which this
	// schema records only in the inventory plane — quantity is posted to
	// stock_movement and the value is derived by replay at report time, so there
	// is no ledger under Stock-in-Hand carrying it and never will be (see the
	// STOCK VALUE IS NEVER STORED note in stock_summary.go).
	computed []StatementNode
}

// loadGroupTree reads all groups (with parent links) for tree assembly.
func loadGroupTree(ctx context.Context, tx pgx.Tx) (map[uuid.UUID]*groupNode, []uuid.UUID, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, name, parent_id, nature::text, is_pl
		   FROM account_group ORDER BY name`)
	if err != nil {
		return nil, nil, fmt.Errorf("reporting: query groups: %w", err)
	}
	defer rows.Close()

	nodes := make(map[uuid.UUID]*groupNode)
	var order []uuid.UUID
	for rows.Next() {
		var (
			gn     groupNode
			parent *uuid.UUID
		)
		if err := rows.Scan(&gn.id, &gn.name, &parent, &gn.nature, &gn.isPL); err != nil {
			return nil, nil, fmt.Errorf("reporting: scan group: %w", err)
		}
		gn.parent = parent
		n := gn
		nodes[gn.id] = &n
		order = append(order, gn.id)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("reporting: iterate groups: %w", err)
	}
	// Wire up child links.
	for _, id := range order {
		n := nodes[id]
		if n.parent != nil {
			if p, ok := nodes[*n.parent]; ok {
				p.children = append(p.children, id)
			}
		}
	}
	return nodes, order, nil
}

// buildGroupNode recursively materialises a StatementNode for a group: its
// direct ledgers (as leaves) plus its child groups. Returns the node and its
// signed closing total. A node is omitted (ok=false) when its total is zero and
// it has no contributing descendants, matching Tally's "exclude zero" default.
func buildGroupNode(id uuid.UUID, nodes map[uuid.UUID]*groupNode) (StatementNode, decimal.Decimal, bool) {
	gn := nodes[id]
	node := StatementNode{Kind: NodeGroup, ID: id, Name: gn.name}
	total := decimal.Zero

	for _, lb := range gn.ledgers {
		bal := lb.signed()
		if bal.IsZero() {
			continue
		}
		node.Children = append(node.Children, StatementNode{
			Kind:   NodeLedger,
			ID:     lb.LedgerID,
			Name:   lb.LedgerName,
			Amount: bal,
		})
		total = total.Add(bal)
	}
	for _, cid := range gn.children {
		child, ctotal, ok := buildGroupNode(cid, nodes)
		if !ok {
			continue
		}
		node.Children = append(node.Children, child)
		total = total.Add(ctotal)
	}

	// Derived children (today: Closing Stock under Stock-in-Hand). THE DROP TEST
	// BELOW MUST STAY BELOW THIS LOOP, and the ordering is the whole mechanism:
	//
	//   - a company with NO inventory injects nothing, so an empty Stock-in-Hand
	//     is still dropped exactly as it was before closing stock existed and the
	//     tree is byte-identical — "absent, not a zero row that changes the tree".
	//   - a company WITH inventory resurrects Stock-in-Hand and, because `total`
	//     flows up through the recursion, its `Current Assets` ancestor subtotals
	//     the stock too. A sheet whose Current Assets excluded the stock while
	//     Assets.Total included it would foot and still be internally
	//     inconsistent, which is precisely what an accountant notices first.
	//
	// Moving the drop test above this loop would re-introduce both bugs at once,
	// and neither one can be seen by IsBalanced().
	for _, c := range gn.computed {
		node.Children = append(node.Children, c)
		total = total.Add(c.Amount)
	}

	node.Amount = total
	if total.IsZero() && len(node.Children) == 0 {
		return StatementNode{}, decimal.Zero, false
	}
	return node, total, true
}

// stockInHandGroupName is the seeded Balance-Sheet group closing stock belongs
// under. It is planted by every company seed (migrations 0003/0005/0018/0025/
// 0029) as a system group under Current Assets, and it is unique per company.
//
// WHAT THE DOUBLE-BOOKING REFUSAL BUILT ON THIS NAME DOES NOT CATCH. The guard
// is a HEURISTIC keyed on this one English group name, so it catches the common
// case — a manual stock ledger filed where the seed puts stock — and misses the
// same double count filed anywhere else. An adversarial review demonstrated both
// escapes against the live API:
//
//	a ledger "Stock on hand" under a new group "Inventory (manual)" beneath
//	Current Assets, given 60000 Dr, alongside 1000 of derived closing stock:
//	    GET /balance-sheet -> 200, assets 61000, balanced TRUE
//	the same books with that ledger under "Stock-in-Hand" -> refused, as intended.
//
// A tenant that renames the group, or files stock one level up, gets the silent
// still-footing double count the refusal exists to prevent. Widening it means
// deciding what a "stock ledger" is on a chart the tenant controls, and every
// candidate rule is another name heuristic — which is why this is documented as
// a known limit rather than papered over with a longer word list. Treat the
// refusal as catching a common mistake, NOT as a proof that the books state
// stock once. HANDOFF.md §6 carries this as an open item.
const stockInHandGroupName = "Stock-in-Hand"

// findStockInHand returns the ASSET, non-P&L group named "Stock-in-Hand", or nil
// when the tenant runs a custom chart of accounts that has none.
//
// It walks `order` (the query's name-sorted id list) rather than ranging the map
// so the choice is deterministic even if a chart ever grew two same-named
// groups; RLS scopes the query to one company, where the name is unique.
func findStockInHand(nodes map[uuid.UUID]*groupNode, order []uuid.UUID) *groupNode {
	for _, id := range order {
		gn := nodes[id]
		if gn.nature == "ASSET" && !gn.isPL && gn.name == stockInHandGroupName {
			return gn
		}
	}
	return nil
}

// subtreeLedgerTotal sums the signed closing balance of every ledger under `gn`
// and under every group beneath it. It exists for the double-booking refusal in
// BalanceSheet: stock kept as an ordinary ledger may be filed under a sub-group
// of Stock-in-Hand rather than directly under it, and a check that looked only
// at the direct ledgers would miss it.
func subtreeLedgerTotal(gn *groupNode, nodes map[uuid.UUID]*groupNode) decimal.Decimal {
	total := decimal.Zero
	for _, lb := range gn.ledgers {
		total = total.Add(lb.signed())
	}
	for _, cid := range gn.children {
		if c, ok := nodes[cid]; ok {
			total = total.Add(subtreeLedgerTotal(c, nodes))
		}
	}
	return total
}

// --------------------------------------------------------------------------
// Profit & Loss
// --------------------------------------------------------------------------

// ProfitAndLoss is the two-sided P&L: Income vs Expenses, with the net result.
// NetProfit is positive for a profit (Income > Expense) and negative for a
// loss. It is the figure folded into the Balance Sheet.
type ProfitAndLoss struct {
	Income   Section
	Expenses Section
	// NetProfit = total income - total expense (positive = profit).
	NetProfit decimal.Decimal
}

// ProfitAndLoss builds the Profit & Loss statement as a drill-down tree. Only
// P&L-nature groups (Income / Expense) contribute; the net result balances the
// two sides and flows to the Balance Sheet.
func (s *Service) ProfitAndLoss(ctx context.Context, companyID uuid.UUID, from, to string) (ProfitAndLoss, error) {
	if err := scope(ctx, companyID); err != nil {
		return ProfitAndLoss{}, err
	}
	var pl ProfitAndLoss
	pl.Income = Section{Title: "Income", Total: decimal.Zero}
	pl.Expenses = Section{Title: "Expenses", Total: decimal.Zero}

	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		nodes, order, err := loadGroupTree(ctx, tx)
		if err != nil {
			return err
		}
		// A P&L is a statement FOR A PERIOD: movement inside [from, to], with
		// the opening balance deliberately excluded.
		balances, err := loadPeriodMovement(ctx, tx, from, to)
		if err != nil {
			return err
		}
		attachLedgers(nodes, balances)
		// Roots are P&L groups whose parent is not itself a P&L group.
		for _, id := range order {
			gn := nodes[id]
			if !gn.isPL {
				continue
			}
			if gn.parent != nil {
				if p, ok := nodes[*gn.parent]; ok && p.isPL {
					continue // not a P&L root
				}
			}
			node, total, ok := buildGroupNode(id, nodes)
			if !ok {
				continue
			}
			switch gn.nature {
			case "INCOME":
				pl.Income.Nodes = append(pl.Income.Nodes, node)
				pl.Income.Total = pl.Income.Total.Add(total)
			case "EXPENSE":
				pl.Expenses.Nodes = append(pl.Expenses.Nodes, node)
				pl.Expenses.Total = pl.Expenses.Total.Add(total)
			}
		}

		// ------------------------------------------------------------------
		// THE TRADING ACCOUNT: Opening Stock and Closing Stock
		// ------------------------------------------------------------------
		//
		// Gross Profit = Sales - (Opening Stock + Purchases - Closing Stock).
		// This schema keeps a PERIODIC inventory: a purchase is expensed to
		// Purchase Accounts when it is bought, and the stock still on hand at the
		// period end is brought back as the Closing Stock credit. So the two
		// terms go on Tally's Trading-account sides — Opening Stock on the
		// debit/expense side, Closing Stock on the credit/income side — and the
		// EXISTING net-profit line below (left byte-identical) then yields
		//
		//     NetProfit = (Sales + Closing) - (Purchases + Opening)
		//               = Sales - (Opening + Purchases - Closing)
		//
		// which is exactly gross profit less the indirect items.
		//
		// WHY THESE ARE SECTION-LEVEL APPENDS WHILE THE BALANCE SHEET NESTS.
		// The Balance Sheet's Closing Stock belongs to a real group that every
		// seeded chart has (Stock-in-Hand, under Current Assets), so it is nested
		// there and Current Assets subtotals it. Opening/Closing Stock belong to
		// a TRADING ACCOUNT, and this chart has no such group. Do not invent one,
		// and above all do NOT nest these under Direct Expenses / Direct Incomes:
		// those are real, user-postable groups, and a computed node inside one
		// would corrupt a subtotal a user can drill into and reconcile.
		//
		// THE PERIOD FORM, AND WHY THE OPENING IS TAKEN AT from-1.
		// S(d0,d1) = CS(d1) - CS(d0-1). Two CONTIGUOUS windows telescope:
		//     S(d0,d1) + S(d1+1,d2)
		//   = (CS(d1) - CS(d0-1)) + (CS(d2) - CS(d1))
		//   = CS(d2) - CS(d0-1) = S(d0,d2),
		// because CS(d1) appears once positively and once negatively. Taking the
		// opening at d0 instead of d0-1 breaks the shared boundary value and the
		// two halves then differ by whatever moved ON d1+1. This holds for
		// CONTIGUOUS windows only — two disjoint windows do not telescope, and no
		// test may assert that they do.
		//
		// openingStockValue maps an EMPTY `from` to zero rather than to CS(""),
		// which on this seam means "everything ever posted": passing "" through
		// would subtract the entire warehouse and understate gross profit by it,
		// and the P&L would still foot. An absent `from` query param is the
		// DEFAULT HTTP path, not an edge case.
		opening, err := openingStockValue(ctx, tx, from)
		if err != nil {
			return err
		}
		closing, err := closingStockValue(ctx, tx, to)
		if err != nil {
			return err
		}
		// Section.Total is accumulated by the caller and is NEVER derived from
		// Nodes anywhere in this file, so every append must add to Total
		// explicitly or the statement silently understates the side it is on.
		// Both terms are suppressed when zero: a company with no inventory must
		// see no row at all, not a zero row that changes the shape of the tree.
		if !opening.IsZero() {
			pl.Expenses.Nodes = append(pl.Expenses.Nodes, StatementNode{
				Kind:   NodeComputed,
				Name:   "Opening Stock",
				Amount: opening,
			})
			pl.Expenses.Total = pl.Expenses.Total.Add(opening)
		}
		if !closing.IsZero() {
			pl.Income.Nodes = append(pl.Income.Nodes, StatementNode{
				Kind:   NodeComputed,
				Name:   "Closing Stock",
				Amount: closing,
			})
			pl.Income.Total = pl.Income.Total.Add(closing)
		}
		pl.NetProfit = pl.Income.Total.Sub(pl.Expenses.Total)
		return nil
	})
	if err != nil {
		return ProfitAndLoss{}, err
	}
	return pl, nil
}

// --------------------------------------------------------------------------
// Balance Sheet
// --------------------------------------------------------------------------

// BalanceSheet is the two-sided Balance Sheet: Liabilities (incl. capital and
// the current-period net result) vs Assets. NetProfit is surfaced as a computed
// node under Liabilities (a profit increases owners' funds); a loss appears as a
// negative-amount node, matching Tally's "show loss under liabilities" default.
type BalanceSheet struct {
	Assets      Section
	Liabilities Section
	// NetProfit folded into Liabilities (positive = profit).
	NetProfit decimal.Decimal
}

// IsBalanced reports whether Assets equal Liabilities (the fundamental identity,
// with the P&L result already folded into Liabilities).
func (bs BalanceSheet) IsBalanced() bool {
	return bs.Assets.Total.Equal(bs.Liabilities.Total)
}

// BalanceSheet builds the Balance Sheet as a drill-down tree. Balance-sheet
// groups (Asset / Liability nature, non-P&L) form the two sides; the current
// period's net profit is folded into Liabilities as a computed node so the
// accounting identity Assets = Liabilities + Capital + Profit holds.
func (s *Service) BalanceSheet(ctx context.Context, companyID uuid.UUID, asOn string) (BalanceSheet, error) {
	if err := scope(ctx, companyID); err != nil {
		return BalanceSheet{}, err
	}
	var bs BalanceSheet
	bs.Assets = Section{Title: "Assets", Total: decimal.Zero}
	bs.Liabilities = Section{Title: "Liabilities", Total: decimal.Zero}

	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		nodes, order, err := loadGroupTree(ctx, tx)
		if err != nil {
			return err
		}
		// A Balance Sheet is a POSITION AS AT a date: opening plus every
		// movement up to `asOn`. It takes no lower bound — dropping an
		// entity's earlier history would still foot, and be wrong.
		balances, err := loadClosingBalances(ctx, tx, asOn)
		if err != nil {
			return err
		}
		attachLedgers(nodes, balances)

		// ------------------------------------------------------------------
		// CLOSING STOCK — the asset this schema records only in the inventory
		// plane. Derived here, inside the CALLER'S transaction.
		// ------------------------------------------------------------------
		//
		// TX DISCIPLINE. closingStockValue takes `tx`, and must: db.Pool.RunInTx
		// calls pool.Begin unconditionally and supports no savepoints, so calling
		// s.StockSummary(...) from in here would hold one pooled connection while
		// acquiring a second — a hard deadlock at pool-size concurrency — and
		// would read a SECOND SNAPSHOT, so the accounting side and the stock line
		// could straddle a concurrent commit and the sheet would fail
		// IsBalanced() irreproducibly. The outer RunInTx has already set the
		// company GUC, so RLS and the books-beginning probe resolve with no
		// extra work.
		//
		// AN UNVALUABLE ITEM ABORTS THE WHOLE STATEMENT, DELIBERATELY. A stock
		// item whose valuation_method has no implemented valuer makes
		// replayPositions fail (inventory.ValuerFor returns an error rather than
		// falling back), and that error propagates out of here and kills the
		// Balance Sheet, accounting side included. There is no `continue`, no
		// zero fallback and no "unvalued" bucket, for the reason stated in
		// inventory/valuation.go: a report that cannot render is better than one
		// that renders a wrong number. It is unreachable today (migration 0025's
		// CHECK admits only WEIGHTED_AVG) and reachable exactly in the
		// deployment-skew case where a silently weighted-average "FIFO" Balance
		// Sheet would be the worst possible output.
		//
		// EXPIRED STOCK IS CARRIED AT COST. This path never reads
		// stock_batch.expiry_date and must not start to: an expired batch is
		// still physically in the warehouse and still the company's property
		// until somebody writes it off with a voucher somebody signs. See the
		// header of stock_batch_summary.go, which anticipated this fold-in by
		// name.
		closing, err := closingStockValue(ctx, tx, asOn)
		if err != nil {
			return err
		}

		stockInHand := findStockInHand(nodes, order)
		if !closing.IsZero() {
			// THE DOUBLE-BOOKING REFUSAL.
			//
			// Some charts of accounts carry stock as an ORDINARY LEDGER under
			// Stock-in-Hand (corpora/migration/01-opening-trading.xml plants
			// exactly that: an "Opening Stock" ledger of 60000 Dr). If such a
			// company ALSO posts stock movements, the same goods are stated
			// twice — once by the ledger and once by this derivation.
			//
			// WHY THIS IS A REFUSAL AND NOT A SILENT NET-OFF OR A DOCUMENTED
			// LIMITATION. The fold-in adds the same figure S to Assets and to
			// net profit, so S CANCELS out of Assets - Liabilities: a double
			// count still FOOTS. bs.IsBalanced() cannot see it, verify-demo's
			// balance check cannot see it, and no gate that compares two sides
			// of one number ever will. The output is a statement somebody files.
			// Netting the ledger balance off the computed node was rejected too:
			// it silently rewrites the accounting side, and there is no
			// principled reason the two figures relate. Refusing follows the
			// precedent inventory.ValuerFor already set.
			//
			// It fires only when BOTH are non-zero, so the corpora case above
			// (a stock ledger with zero stock_movement) still renders — that is
			// TestAStockLedgerWithNoInventoryIsLeftAlone. There is deliberately
			// no equivalent check on the P&L: Stock-in-Hand is a Balance-Sheet
			// group and never appears there.
			if stockInHand != nil {
				if manual := subtreeLedgerTotal(stockInHand, nodes); !manual.IsZero() {
					return fmt.Errorf(
						"reporting: company books stock twice — ledgers under %q carry %s "+
							"while the inventory ledger derives a closing stock of %s as at %s; "+
							"a Balance Sheet cannot state both, and because the same figure is "+
							"added to assets and to net profit the double count would still "+
							"balance. Remove the manual stock ledger balance, or stop posting "+
							"stock movements",
						stockInHandGroupName, manual.StringFixed(4), closing.StringFixed(4),
						dateOrEver(asOn))
				}
			}

			// NEST IT under the real group rather than appending a top-level
			// node: Current Assets must subtotal the stock, or the sheet is
			// internally inconsistent (Assets.Total would include a figure its
			// own Current Assets line excludes). Injecting into `computed`
			// rather than appending a finished node also makes zero-suppression
			// structural — see buildGroupNode.
			if stockInHand != nil {
				stockInHand.computed = append(stockInHand.computed, StatementNode{
					Kind:   NodeComputed,
					Name:   "Closing Stock",
					Amount: closing,
				})
			}
		}

		income, expense := decimal.Zero, decimal.Zero
		for _, id := range order {
			gn := nodes[id]
			// Accumulate the P&L net result from P&L roots.
			if gn.isPL {
				if gn.parent != nil {
					if p, ok := nodes[*gn.parent]; ok && p.isPL {
						continue
					}
				}
				_, total, ok := buildGroupNode(id, nodes)
				if ok {
					switch gn.nature {
					case "INCOME":
						income = income.Add(total)
					case "EXPENSE":
						expense = expense.Add(total)
					}
				}
				continue
			}
			// Balance-sheet root: a non-P&L group whose parent is not a non-P&L
			// group (i.e. a top-level BS group).
			if gn.parent != nil {
				if p, ok := nodes[*gn.parent]; ok && !p.isPL {
					continue
				}
			}
			node, total, ok := buildGroupNode(id, nodes)
			if !ok {
				continue
			}
			switch gn.nature {
			case "ASSET":
				bs.Assets.Nodes = append(bs.Assets.Nodes, node)
				bs.Assets.Total = bs.Assets.Total.Add(total)
			case "LIABILITY":
				bs.Liabilities.Nodes = append(bs.Liabilities.Nodes, node)
				bs.Liabilities.Total = bs.Liabilities.Total.Add(total)
			}
		}

		// FALLBACK for a custom chart of accounts with no Stock-in-Hand group at
		// all: state the asset as a top-level computed node on Assets rather
		// than dropping it. Both paths add EXACTLY the same amount to
		// Assets.Total; only the nesting differs, and nesting is preferred
		// wherever the group exists (see above).
		if !closing.IsZero() && stockInHand == nil {
			bs.Assets.Nodes = append(bs.Assets.Nodes, StatementNode{
				Kind:   NodeComputed,
				Name:   "Closing Stock",
				Amount: closing,
			})
			bs.Assets.Total = bs.Assets.Total.Add(closing)
		}

		// ------------------------------------------------------------------
		// The net-profit line, in its GENERAL PERIOD FORM.
		// ------------------------------------------------------------------
		//
		// The Balance Sheet's implicit P&L period runs from the books' beginning
		// to `asOn`, so its stock adjustment is the same S = CS(end) - CS(start-1)
		// that ProfitAndLoss uses, with start-1 = books_beginning_from - 1.
		//
		// THAT SECOND TERM IS ZERO BECAUSE THE BALANCE SHEET'S PERIOD HAS NO LOWER
		// BOUND — not because nothing can be on hand before the books begin.
		//
		// The distinction matters, and an earlier version of this comment got it
		// wrong. It claimed stock_opening is the ONLY record of pre-books stock, so
		// "nothing can be on hand the day before the books begin". That is FALSE: a
		// stock MOVEMENT may be dated before books_beginning_from, and
		// loadStockPositionMovements deliberately carries NO lower bound, because a
		// position needs the entity's whole history. Goods CAN be on hand then.
		//
		// The zero is still correct, for the other reason. Like loadClosingBalances
		// beside it, this statement is a POSITION AS AT a date: its period runs from
		// inception — not from books_beginning_from — to `asOn`. CS(inception - 1)
		// is zero because there is no "before inception". So S = CS(asOn) - 0.
		//
		// WHAT THIS MEANS WHEN SOMETHING IS BACK-DATED BEFORE THE BOOKS BEGAN, which
		// an adversarial review measured and mistook for a defect. Books beginning
		// 2026-04-01, a NON-ACCOUNTING receipt note back-dated to 2026-03-15 for
		// 10 @ 700, a second receipt on 2026-05-10 for 5 @ 100:
		//
		//     GET /balance-sheet?as_on=2027-03-31                net_profit 7500
		//     GET /profit-and-loss?from=2026-04-01&to=2027-03-31  net_profit  500
		//
		// Those differ by 7000 and BOTH ARE RIGHT: they cover DIFFERENT PERIODS. The
		// sheet's period includes 2026-03-15 and the P&L's does not, so the goods
		// that arrived with no counter-entry are a windfall inside the sheet's
		// period and outside the P&L's. The accounting plane behaves identically —
		// loadClosingBalances has no lower bound either, so a pre-books JOURNAL
		// splits the two net profits in exactly the same way, and has always done
		// so. The stock plane is symmetric with it, which is the property to keep.
		//
		// DO NOT "FIX" THIS by deriving the term from books_beginning_from. It was
		// tried: it subtracts the pre-books stock from the profit side while the
		// ASSET side still carries the full closing stock, so the two sides stop
		// moving together and THE SHEET STOPS BALANCING — measured at assets 7500
		// against liabilities 500 on the scenario above.
		// TestBackDatedStockKeepsTheSheetBalanced pins that.
		//
		// The subtraction is written out in full rather than collapsed to a bare
		// `.Add(closing)` so that a later reader cannot "restore" a subtraction that
		// is already there — that mistake would put the sheet out by exactly the
		// opening-stock value (books begin 2026-04-01, opening 1000, purchases 500,
		// sales 900, closing 300: correct sheet 700 = 700; with the opening wrongly
		// forced in, assets 700 against liabilities -300).
		//
		// Note what this means and accept it out loud: in a PERIODIC inventory
		// system, opening stock recorded only in the inventory plane has no
		// counter-credit anywhere in the accounts, so its whole value flows into
		// the first period's profit. That is the honest consequence of this
		// schema, not a defect in this line.
		//
		// THE ALGEBRA, AND THE WARNING IT EARNS.
		// With A = assets, L = liabilities + capital, NP = income - expense taken
		// cumulatively from inception, double entry gives A = L + NP. The
		// fold-in makes Assets' = A + closing and Liabilities' = L + (NP +
		// closing), so Assets' - Liabilities' = A - L - NP = 0: THE TERM CANCELS.
		//
		//   *** BECAUSE IT CANCELS, IsBalanced() IS TRUE FOR ANY VALUE OF
		//   *** `closing`, INCLUDING A COMPLETELY WRONG ONE. Balance proves
		//   *** SYMMETRY — that the asset and the P&L term moved together at the
		//   *** same date — and never CORRECTNESS. The number itself is pinned
		//   *** only by the identity against StockSummary for the same date
		//   *** (TestClosingStockOnTheBalanceSheetEqualsTheStockSummaryTotal, and
		//   *** the demo gate). Do not add a check here and believe it proves
		//   *** more than it does.
		//
		// Finally: the Balance Sheet recomputes net profit INDEPENDENTLY (it does
		// not call ProfitAndLoss), so this and the Trading-account block in
		// ProfitAndLoss are two code paths that must agree.
		// internal/migration's oracle cross-checks BS.NetProfit == PL.NetProfit
		// on every corpus, but only UNBOUNDED — a bound-dependent divergence
		// would slip straight past it.
		openingBS := decimal.Zero
		stockAdj := closing.Sub(openingBS)
		bs.NetProfit = income.Sub(expense).Add(stockAdj)
		if !bs.NetProfit.IsZero() {
			bs.Liabilities.Nodes = append(bs.Liabilities.Nodes, StatementNode{
				Kind:   NodeComputed,
				Name:   "Profit & Loss A/c (Current Period)",
				Amount: bs.NetProfit,
			})
			bs.Liabilities.Total = bs.Liabilities.Total.Add(bs.NetProfit)
		}
		return nil
	})
	if err != nil {
		return BalanceSheet{}, err
	}
	return bs, nil
}

// dateOrEver renders an as-on date for an error message. Empty means "no upper
// bound at all" on this seam, and a message reading "as at " with nothing after
// it would send a reader looking for a date that was never supplied.
func dateOrEver(asOn string) string {
	if strings.TrimSpace(asOn) == "" {
		return "the latest date posted"
	}
	return strings.TrimSpace(asOn)
}

// attachLedgers attaches each ledger balance to its group node so the statement
// builders can roll them up. It takes the balances rather than loading them, so
// each statement chooses its own period semantics (see PERIOD SEMANTICS above).
func attachLedgers(nodes map[uuid.UUID]*groupNode, balances []ledgerBalance) {
	for _, lb := range balances {
		if gn, ok := nodes[lb.GroupID]; ok {
			gn.ledgers = append(gn.ledgers, lb)
		}
	}
}
