package masters

// THE BUDGET MASTER — the only figure in this product that no voucher contains.
//
// Migration 0085 created `budget` and `budget_line` and nothing writes them.
// Until this file existed a budget could only be created by raw SQL, so the
// variance report had nothing to compare against and a report with an empty
// second column is a feature that is present and useless — exactly what
// cost_centre.go's header says about an axis nobody can create.
//
// Tally puts this behind Gateway > Accounts Info > Budgets.
//
// ===========================================================================
// ⛔⛔ A BUDGET POSTS NOTHING, AND THIS FILE MUST NEVER MAKE IT POST.
// ===========================================================================
// No voucher, no `posting` row, no ledger_balance effect, no entry in the audit
// hash chain. If a change here finds itself writing into anything the ledger
// owns, it is wrong: a budget that posts appears in the Trial Balance as money
// that does not exist, and the sheet would foot against a fiction.
//
// What IS written is a master_audit row per act, exactly as every other master
// here does — that is the edit log, not the hash chain, and it is the only
// record of a revision (see the revision note below).
//
// ===========================================================================
// ⛔⛔ THE TWO RULES IN THIS FILE THAT NO CONSTRAINT CAN STATE
// ===========================================================================
// Migration 0085's CHECKs, unique indexes and composite FKs are THE AUTHORITY
// and they stay — they exist to be right even if this code is wrong. The checks
// here exist to produce A SENTENCE A SHOPKEEPER CAN ACT ON, and in two places
// they are the ONLY thing standing between an operator and a wrong number:
//
//  1. THE SIDE (`dr_cr`) IS DERIVED FROM THE TARGET'S NATURE when the caller
//     omits it. The database cannot do this: budget_line has no join to
//     account_group at write time, and a NOT NULL column with no default has to
//     be filled by somebody. Filled WRONG — a credit budget on an expense
//     ledger — the report compares a plan against `credit - debit` on a ledger
//     whose spend is all debit, and reports every rupee of spend as a NEGATIVE
//     actual: a business that overspent by 40% reads as one that spent nothing.
//     That is a real, correctly-footed, confidently wrong report, which is the
//     failure mode this codebase's register is full of.
//
//  2. A COST CENTRE MAY NOT BE BUDGETED ON A CLOSING BALANCE. 0085 carries the
//     CHECK, so the database refuses it — but it refuses it by constraint name,
//     and the operator's actual question is "why not?". The answer is that a cost
//     centre holds no position: there are only allocations against dated
//     postings, so a "closing balance" is every rupee ever booked to that
//     department since the books began. See budgetBasisError.
//
// ===========================================================================
// ⚠️ REVISION OVERWRITES. THAT IS A DECISION, AND IT IS THE ONE THIS FILE IS
// MOST LIKELY TO BE ASKED TO CHANGE.
// ===========================================================================
// AlterBudgetLine restates a figure IN PLACE and the previous figure is gone
// from `budget_line`. It survives only in master_audit's before_doc, which is an
// edit log rather than a reportable column.
//
// So "the original budget, the revised budget, and the actual" — three columns a
// real business asks for — is NOT AVAILABLE, and the FeaturesScreen row says so.
// ⛔ The fix is a budget_revision table, NOT a nullable original_amount column on
// budget_line: a second revision would overwrite the first "original" and the
// column would then be lying about which figure it holds.
//
// ===========================================================================
// NORMALISATION MUST MATCH THE INDEX, OR GO ACCEPTS WHAT THE DATABASE REFUSES
// ===========================================================================
// budget_name_unique is on lower(btrim(name)) and costNameKey() next door is that
// same expression in Go, reused here rather than re-spelled. The name STORED is
// the trimmed form, so btrim in the index is a no-op on anything this file
// writes. The same non-ASCII case-folding residue cost_centre.go's header
// documents applies, and is handled the same honest way: the unique index is
// caught BY NAME on the way back and turned into the sentence the Go check would
// have produced, so the worst case is the right sentence arriving from the
// database rather than a raw constraint name.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/platform/db"
)

// ---------------------------------------------------------------------------
// The vocabulary, spelled once. These strings are stored, so they are the same
// strings migration 0085's CHECK constraints name — a mismatch here is a write
// that fails on a constraint the operator cannot read.
// ---------------------------------------------------------------------------

const (
	// DimensionGroup budgets an account group: every ledger under it, rolled up.
	DimensionGroup = "GROUP"
	// DimensionLedger budgets one ledger.
	DimensionLedger = "LEDGER"
	// DimensionCostCentre budgets one cost centre (T2's allocation dimension).
	DimensionCostCentre = "COST_CENTRE"

	// BasisClosingBalance measures the actual as a POSITION as at the budget
	// period's to_date: opening balance plus every movement up to that date.
	BasisClosingBalance = "CLOSING_BALANCE"
	// BasisNettTransactions measures the actual as MOVEMENT INSIDE the budget
	// period, with the opening balance deliberately excluded.
	BasisNettTransactions = "NETT_TRANSACTIONS"
)

// KnownBudgetDimensions and KnownBudgetBases exist so a refusal can NAME the
// alternatives rather than telling an operator only that what they sent was
// wrong. Both are the closed sets 0085's CHECKs enforce.
var (
	// KnownBudgetDimensions is every grain a budget line may target.
	KnownBudgetDimensions = []string{DimensionGroup, DimensionLedger, DimensionCostCentre}
	// KnownBudgetBases is every basis a budget line may be measured on.
	KnownBudgetBases = []string{BasisClosingBalance, BasisNettTransactions}
)

// ErrInvalidBudget is returned for a budget or line this layer refuses to store
// on its own facts — a blank name, a reversed period, an unknown dimension, a
// cost centre on a closing-balance basis, a target that does not exist. A
// sentinel so the HTTP layer answers 400 rather than 500.
var ErrInvalidBudget = errors.New("masters: invalid budget")

// ErrBudgetNameTaken is a budget name already in use in this company.
//
// Separate from ErrInvalidBudget because it maps to a different HTTP family and
// means a different thing to the operator: nothing about the request is
// malformed, the books already say otherwise, and the fix is to pick another name
// or edit the plan that exists — not to correct a field. Same call
// ErrCostNameTaken makes next door.
var ErrBudgetNameTaken = errors.New("masters: budget name already in use")

// ---------------------------------------------------------------------------
// The wire-facing records.
// ---------------------------------------------------------------------------

// BudgetInput creates or restates a budget head: a name and a period.
type BudgetInput struct {
	Name string
	// FromDate and ToDate are inclusive ISO dates (YYYY-MM-DD). ⛔ BOTH ARE
	// REQUIRED, unlike every report period in this codebase, which takes optional
	// bounds. A report with no bounds means "everything ever posted"; a budget
	// with no bounds is a plan for no stated period, and the CLOSING_BALANCE basis
	// would have no date to be "as at".
	FromDate string
	ToDate   string
}

// BudgetRecord is one budget as listed.
type BudgetRecord struct {
	ID       uuid.UUID
	Name     string
	FromDate string
	ToDate   string
	// LineCount is carried so a screen can show that a budget is EMPTY without a
	// second request. An empty budget is a live trap: the variance report over it
	// is a page of unbudgeted actuals, which reads as "nothing was planned" and is
	// indistinguishable from "the plan was never keyed".
	LineCount int
	CreatedBy string
	CreatedAt string
}

// BudgetLineInput is one budgeted figure.
type BudgetLineInput struct {
	// Dimension is one of KnownBudgetDimensions.
	Dimension string
	// TargetID is the group, ledger or cost centre id — whichever Dimension
	// names. ONE FIELD AND NOT THREE: the caller has already said which grain
	// this is, and three nullable id fields would make "dimension says LEDGER,
	// only account_group_id is set" a request this layer had to have an opinion
	// about. 0085 stores it in the matching column.
	TargetID uuid.UUID
	// Basis is one of KnownBudgetBases.
	Basis string
	// DrCr is "D" or "C", or "" to take the target's NATURAL side. Empty is the
	// ordinary case for a group or a ledger and an operator never types it.
	//
	// ⛔ EMPTY IS A REFUSAL FOR A COST CENTRE, which has no nature to derive from.
	DrCr string
	// Amount is a positive magnitude. Zero is refused: a budget of zero is the
	// ABSENCE of a budget, and the report has a distinct answer for that
	// (UNBUDGETED) which must not be spellable two ways.
	Amount decimal.Decimal
	Note   string
}

// BudgetLineRecord is one budget line as listed, with everything a reader needs
// to interpret it WITHOUT a second lookup.
type BudgetLineRecord struct {
	ID        uuid.UUID
	BudgetID  uuid.UUID
	Dimension string
	TargetID  uuid.UUID
	// TargetName is the group / ledger / centre name. It travels with every row
	// on purpose: a list of bare ids is unreadable, and for a COST CENTRE the name
	// alone is AMBIGUOUS — "Mumbai" may legitimately be both a Branch and a
	// Project — which is why CostCategoryName comes too.
	TargetName string
	// CostCategoryID / CostCategoryName are set only for a COST_CENTRE line: the
	// AXIS the centre sits on. uuid.Nil / "" otherwise.
	CostCategoryID   uuid.UUID
	CostCategoryName string
	// Nature is the target's ASSET / LIABILITY / INCOME / EXPENSE, and "" for a
	// cost centre, which has none. On the wire so a client can tell whether
	// spending more than budget is the good direction or the bad one without
	// re-deriving it — see the reporting package on why this layer refuses to
	// label a variance "favourable".
	Nature    string
	Basis     string
	DrCr      string
	Amount    decimal.Decimal
	Note      string
	CreatedAt string
}

// ---------------------------------------------------------------------------
// PURE VALIDATION. Kept free of the database so the two rules that matter — the
// side derivation and the cost-centre basis refusal — are testable without one.
// ---------------------------------------------------------------------------

// budgetTarget is a resolved target as the pure checks see it: what the database
// says exists, reduced to the four facts a decision needs.
type budgetTarget struct {
	ID   uuid.UUID
	Name string
	// Nature is "" for a cost centre.
	Nature string
	// CategoryID / CategoryName are set for a cost centre only.
	CategoryID   uuid.UUID
	CategoryName string
}

const isoDateBudget = "2006-01-02"

// validateBudgetPeriod parses and orders a budget's bounds.
//
// A SINGLE-DAY BUDGET IS LEGAL (to == from) and a reversed one is not. Reversed
// bounds would make every NETT_TRANSACTIONS line match no movement at all and
// report the whole budget as unspent — which reads as a business fact ("we
// planned and spent nothing") rather than as the typo it is.
func validateBudgetPeriod(from, to string) (string, string, error) {
	f, t := strings.TrimSpace(from), strings.TrimSpace(to)
	if f == "" || t == "" {
		return "", "", fmt.Errorf("%w: a budget needs both a from_date and a to_date "+
			"(YYYY-MM-DD). A plan for no stated period cannot be compared against "+
			"anything: the closing-balance basis would have no date to be measured as at",
			ErrInvalidBudget)
	}
	fd, err := time.Parse(isoDateBudget, f)
	if err != nil {
		return "", "", fmt.Errorf("%w: from_date %q is not a date (want YYYY-MM-DD)",
			ErrInvalidBudget, f)
	}
	td, err := time.Parse(isoDateBudget, t)
	if err != nil {
		return "", "", fmt.Errorf("%w: to_date %q is not a date (want YYYY-MM-DD)",
			ErrInvalidBudget, t)
	}
	if td.Before(fd) {
		return "", "", fmt.Errorf("%w: the budget period ends (%s) before it begins (%s). "+
			"Reversed bounds would match no movement at all and report the entire budget "+
			"as unspent, which reads as a business fact rather than as a typo",
			ErrInvalidBudget, t, f)
	}
	return fd.Format(isoDateBudget), td.Format(isoDateBudget), nil
}

// validateBudgetName trims and checks a budget name against the names already in
// use, case-insensitively — the scope budget_name_unique enforces.
func validateBudgetName(raw string, existing map[string]uuid.UUID, selfID uuid.UUID) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", fmt.Errorf("%w: a budget needs a name — a plan nobody can name is a "+
			"plan nobody can be asked about", ErrInvalidBudget)
	}
	if id, clash := existing[costNameKey(name)]; clash && id != selfID {
		return "", fmt.Errorf("%w: a budget called %q already exists for this company "+
			"(names are compared ignoring case and surrounding spaces). Two plans with one "+
			"name is how half the lines end up on each and both look complete",
			ErrBudgetNameTaken, name)
	}
	return name, nil
}

// naturalSide returns the side a nature's balance sits on when it is positive.
// Debit for what a business OWNS or SPENDS, credit for what it OWES or EARNS —
// the same convention reporting.ledgerBalance.signed() applies.
func naturalSide(nature string) string {
	switch nature {
	case "ASSET", "EXPENSE":
		return "D"
	default: // LIABILITY, INCOME
		return "C"
	}
}

// validateBudgetLine is the whole of the line's own correctness, with the target
// already resolved. Returns the line ready to store.
//
// ⛔ THE ORDER OF THE CHECKS IS NOT ARBITRARY: dimension first, then basis, then
// the side. Each later check reads a field the earlier one has already
// established the meaning of, and reporting a side error on a line whose
// dimension is nonsense tells an operator to fix the wrong field.
func validateBudgetLine(in BudgetLineInput, target budgetTarget) (BudgetLineInput, error) {
	out := in
	out.Dimension = strings.ToUpper(strings.TrimSpace(in.Dimension))
	out.Basis = strings.ToUpper(strings.TrimSpace(in.Basis))
	out.DrCr = strings.ToUpper(strings.TrimSpace(in.DrCr))
	out.Note = strings.TrimSpace(in.Note)

	if !contains(KnownBudgetDimensions, out.Dimension) {
		return out, fmt.Errorf("%w: %q is not a budget dimension — want one of %s",
			ErrInvalidBudget, in.Dimension, strings.Join(KnownBudgetDimensions, ", "))
	}
	if !contains(KnownBudgetBases, out.Basis) {
		return out, fmt.Errorf("%w: %q is not a budget basis — want %s (a POSITION as at the "+
			"period end, opening balance folded in) or %s (MOVEMENT inside the period, "+
			"opening balance excluded). These answer different questions and the same "+
			"figure means different things under each, so there is no default",
			ErrInvalidBudget, in.Basis, BasisClosingBalance, BasisNettTransactions)
	}

	// ⛔⛔ A COST CENTRE HOLDS NO POSITION. See the file header and 0085's
	// budget_line_cost_centre_is_transactional. The database refuses this too; the
	// refusal is here as well because the operator's question is "why not".
	if out.Dimension == DimensionCostCentre && out.Basis != BasisNettTransactions {
		return out, fmt.Errorf("%w: a cost centre cannot be budgeted on a %s. A cost centre is "+
			"not an account: it holds no opening balance and carries no position, only "+
			"allocations against dated postings. Summing every allocation it has ever "+
			"received would be the total cost booked to %q since the books began, which is "+
			"a number no business plans against — it would make a year's budget look almost "+
			"entirely unspent in year one and several times overspent in year four. Use %s",
			ErrInvalidBudget, BasisClosingBalance, target.Name, BasisNettTransactions)
	}

	// THE SIDE. Derived from nature when omitted, which is the ordinary path for a
	// group or a ledger; required outright for a cost centre, which has none.
	switch {
	case out.DrCr == "":
		if target.Nature == "" {
			return out, fmt.Errorf("%w: a cost centre budget must state its side (dr_cr "+
				"\"D\" or \"C\"). A group or a ledger has a NATURE to take the side from; a "+
				"cost centre has none — it is an axis of analysis, not an account — so "+
				"there is nothing here to derive %q's expected side from. A cost budget is "+
				"\"D\"", ErrInvalidBudget, target.Name)
		}
		out.DrCr = naturalSide(target.Nature)
	case out.DrCr != "D" && out.DrCr != "C":
		return out, fmt.Errorf("%w: dr_cr must be \"D\" or \"C\" (or omitted, to take the "+
			"target's natural side), not %q", ErrInvalidBudget, in.DrCr)
	}

	if out.Amount.IsZero() || out.Amount.IsNegative() {
		return out, fmt.Errorf("%w: a budget amount must be greater than zero, not %s. A "+
			"budget of zero is the ABSENCE of a budget, and the variance report already has "+
			"a distinct answer for a target nobody budgeted (it reports the actual as "+
			"UNBUDGETED). Two ways to spell the same thing is how one of them stops being "+
			"read. To plan to spend nothing here, set no line at all",
			ErrInvalidBudget, out.Amount.StringFixed(4))
	}
	return out, nil
}

func contains(set []string, want string) bool {
	for _, s := range set {
		if s == want {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// The store. Every statement runs inside RunInTx, so RLS supplies the tenancy
// from app.current_company_id and NO query below carries a WHERE company_id — a
// hand-written clause would be a second copy of the tenancy rule that can
// disagree with the policy.
// ---------------------------------------------------------------------------

// CreateBudget creates one plan and appends a master_audit CREATE row.
func (s *Service) CreateBudget(ctx context.Context, in BudgetInput, createdBy string) (uuid.UUID, error) {
	if strings.TrimSpace(createdBy) == "" {
		return uuid.Nil, fmt.Errorf("%w: created_by is required — a plan nobody can be asked "+
			"about is a plan nobody can correct", ErrInvalidBudget)
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return uuid.Nil, db.ErrNoCompany
	}
	from, to, err := validateBudgetPeriod(in.FromDate, in.ToDate)
	if err != nil {
		return uuid.Nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("masters: new budget id: %w", err)
	}

	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		existing, err := loadBudgetNames(ctx, tx)
		if err != nil {
			return err
		}
		name, err := validateBudgetName(in.Name, existing, uuid.Nil)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO budget (id, company_id, name, from_date, to_date, created_by)
			 VALUES ($1, $2, $3, $4::date, $5::date, $6)`,
			id, companyID, name, from, to, createdBy); err != nil {
			// The read above can lose a race, and the case-folding residue noted in
			// the file header lands here too. Either way the index is the authority
			// and the operator gets the same sentence.
			if isUniqueViolationOn(err, "budget_name_unique") {
				return fmt.Errorf("%w: a budget called %q already exists for this company",
					ErrBudgetNameTaken, name)
			}
			return fmt.Errorf("masters: insert budget: %w", err)
		}
		after := map[string]any{"name": name, "from_date": from, "to_date": to}
		return appendAudit(ctx, tx, companyID, "budget", id, OpCreate, nil, after, createdBy)
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// AlterBudget restates a plan's NAME and its PERIOD together, and appends a
// master_audit ALTER row.
//
// THE TWO MOVE TOGETHER because a period change is not cosmetic: it changes which
// movement every NETT_TRANSACTIONS line is measured over and which date every
// CLOSING_BALANCE line is measured as at — i.e. it changes every actual on the
// report without touching a single budgeted figure. Restating both in one call is
// what makes that one audited act rather than two, so the before_doc shows the
// period the old figures were set against.
func (s *Service) AlterBudget(ctx context.Context, budgetID uuid.UUID, in BudgetInput, alteredBy string) error {
	if strings.TrimSpace(alteredBy) == "" {
		return fmt.Errorf("%w: altered_by is required", ErrInvalidBudget)
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return db.ErrNoCompany
	}
	from, to, err := validateBudgetPeriod(in.FromDate, in.ToDate)
	if err != nil {
		return err
	}
	return s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var wasName, wasFrom, wasTo string
		err := tx.QueryRow(ctx,
			`SELECT name, to_char(from_date, 'YYYY-MM-DD'), to_char(to_date, 'YYYY-MM-DD')
			   FROM budget WHERE id = $1`, budgetID).Scan(&wasName, &wasFrom, &wasTo)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: no budget %s in this company", ErrNotFound, budgetID)
		}
		if err != nil {
			return fmt.Errorf("masters: read budget: %w", err)
		}

		existing, err := loadBudgetNames(ctx, tx)
		if err != nil {
			return err
		}
		name, err := validateBudgetName(in.Name, existing, budgetID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE budget SET name = $2, from_date = $3::date, to_date = $4::date WHERE id = $1`,
			budgetID, name, from, to); err != nil {
			if isUniqueViolationOn(err, "budget_name_unique") {
				return fmt.Errorf("%w: a budget called %q already exists for this company",
					ErrBudgetNameTaken, name)
			}
			return fmt.Errorf("masters: update budget: %w", err)
		}
		before := map[string]any{"name": wasName, "from_date": wasFrom, "to_date": wasTo}
		after := map[string]any{"name": name, "from_date": from, "to_date": to}
		return appendAudit(ctx, tx, companyID, "budget", budgetID, OpAlter, before, after, alteredBy)
	})
}

// DeleteBudget removes a plan AND its lines (0085's budget_line_budget_fk
// cascades) and appends a master_audit DELETE row.
//
// ⛔ A DELETE AND NOT A SOFT FLAG, which is the opposite of how this codebase
// treats a voucher. A voucher is a record of something that HAPPENED and may
// never be erased; a budget is a plan for something that has not, and a plan
// abandoned before the period began is not history — keeping it would put a
// column on the variance report that no longer means anything. The master_audit
// DELETE row records that it existed and who dropped it.
//
// ⚠️ THE CASCADE IS WHY THE AUDIT ROW NAMES THE LINE COUNT. Deleting a plan takes
// figures with it silently otherwise, and "I deleted a budget" and "I deleted 40
// budgeted figures" are different acts to whoever reads the log later.
func (s *Service) DeleteBudget(ctx context.Context, budgetID uuid.UUID, deletedBy string) error {
	if strings.TrimSpace(deletedBy) == "" {
		return fmt.Errorf("%w: deleted_by is required", ErrInvalidBudget)
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return db.ErrNoCompany
	}
	return s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var name, from, to string
		var lines int
		err := tx.QueryRow(ctx,
			`SELECT b.name, to_char(b.from_date, 'YYYY-MM-DD'), to_char(b.to_date, 'YYYY-MM-DD'),
			        (SELECT count(*) FROM budget_line l WHERE l.budget_id = b.id)
			   FROM budget b WHERE b.id = $1`, budgetID).Scan(&name, &from, &to, &lines)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: no budget %s in this company", ErrNotFound, budgetID)
		}
		if err != nil {
			return fmt.Errorf("masters: read budget: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM budget WHERE id = $1`, budgetID); err != nil {
			return fmt.Errorf("masters: delete budget: %w", err)
		}
		before := map[string]any{
			"name": name, "from_date": from, "to_date": to,
			// See the doc comment: the count is what makes the log honest about
			// what the cascade took.
			"lines_deleted_by_cascade": lines,
		}
		return appendAudit(ctx, tx, companyID, "budget", budgetID, OpDelete, before, nil, deletedBy)
	})
}

// ListBudgets returns every plan in the company, newest period first.
func (s *Service) ListBudgets(ctx context.Context) ([]BudgetRecord, error) {
	var out []BudgetRecord
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT b.id, b.name,
			        to_char(b.from_date, 'YYYY-MM-DD'), to_char(b.to_date, 'YYYY-MM-DD'),
			        (SELECT count(*) FROM budget_line l WHERE l.budget_id = b.id),
			        b.created_by, to_char(b.created_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')
			   FROM budget b
			  ORDER BY b.from_date DESC, lower(btrim(b.name))`)
		if err != nil {
			return fmt.Errorf("masters: query budgets: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var r BudgetRecord
			if err := rows.Scan(&r.ID, &r.Name, &r.FromDate, &r.ToDate,
				&r.LineCount, &r.CreatedBy, &r.CreatedAt); err != nil {
				return fmt.Errorf("masters: scan budget: %w", err)
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// THE LINES
// ---------------------------------------------------------------------------

// AddBudgetLine adds one budgeted figure to a plan and appends a master_audit
// CREATE row against the LINE.
func (s *Service) AddBudgetLine(
	ctx context.Context, budgetID uuid.UUID, in BudgetLineInput, createdBy string,
) (uuid.UUID, error) {
	if strings.TrimSpace(createdBy) == "" {
		return uuid.Nil, fmt.Errorf("%w: created_by is required", ErrInvalidBudget)
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return uuid.Nil, db.ErrNoCompany
	}
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("masters: new budget line id: %w", err)
	}

	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := requireBudget(ctx, tx, budgetID); err != nil {
			return err
		}
		line, target, err := resolveAndValidateLine(ctx, tx, in)
		if err != nil {
			return err
		}
		if err := insertBudgetLine(ctx, tx, id, companyID, budgetID, line); err != nil {
			return err
		}
		return appendAudit(ctx, tx, companyID, "budget_line", id, OpCreate, nil,
			budgetLineAuditDoc(line, target), createdBy)
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// AlterBudgetLine restates one figure IN PLACE and appends a master_audit ALTER
// row carrying the previous figure.
//
// ⛔ THE TARGET AND THE DIMENSION ARE IMMUTABLE and an attempt to move a line to
// another ledger is refused BY NAME. The database would accept it — no
// constraint in 0085 pins a line to the target it was created against — and it
// would silently re-file a budgeted figure from one row of the variance report to
// another, leaving the original target reading as unbudgeted and the new one as
// having been planned all along. That is a change of MEANING wearing the clothes
// of a correction, so the refusal has to be here. Delete the line and add the
// right one: two audited acts, which is what actually happened.
func (s *Service) AlterBudgetLine(
	ctx context.Context, lineID uuid.UUID, in BudgetLineInput, alteredBy string,
) error {
	if strings.TrimSpace(alteredBy) == "" {
		return fmt.Errorf("%w: altered_by is required", ErrInvalidBudget)
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return db.ErrNoCompany
	}
	return s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var (
			wasDim, wasBasis, wasDrCr, wasNote string
			wasAmount                          decimal.Decimal
			wasTarget                          uuid.UUID
		)
		err := tx.QueryRow(ctx,
			`SELECT dimension, basis, dr_cr, note, amount,
			        COALESCE(account_group_id, ledger_id, cost_centre_id)
			   FROM budget_line WHERE id = $1`, lineID).
			Scan(&wasDim, &wasBasis, &wasDrCr, &wasNote, &wasAmount, &wasTarget)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: no budget line %s in this company", ErrNotFound, lineID)
		}
		if err != nil {
			return fmt.Errorf("masters: read budget line: %w", err)
		}

		wantDim := strings.ToUpper(strings.TrimSpace(in.Dimension))
		if wantDim != "" && wantDim != wasDim {
			return fmt.Errorf("%w: a budget line's dimension cannot be changed (it is %s). "+
				"Re-filing a budgeted figure from one grain to another would leave the "+
				"original target reading as unbudgeted and the new one as having been planned "+
				"all along — a change of meaning dressed as a correction. Delete this line "+
				"and add the right one", ErrInvalidBudget, wasDim)
		}
		if in.TargetID != uuid.Nil && in.TargetID != wasTarget {
			return fmt.Errorf("%w: a budget line's target cannot be changed. Delete this line "+
				"and add one against the target you mean — that is two audited acts, which is "+
				"what actually happened", ErrInvalidBudget)
		}

		// The dimension and target are the STORED ones; only the figure, the basis,
		// the side and the note are the caller's to restate.
		next := in
		next.Dimension, next.TargetID = wasDim, wasTarget
		line, target, err := resolveAndValidateLine(ctx, tx, next)
		if err != nil {
			return err
		}

		if _, err := tx.Exec(ctx,
			`UPDATE budget_line SET basis = $2, dr_cr = $3, amount = $4, note = $5
			  WHERE id = $1`,
			lineID, line.Basis, line.DrCr, line.Amount, line.Note); err != nil {
			return fmt.Errorf("masters: update budget line: %w", err)
		}
		before := map[string]any{
			"target": target.Name, "dimension": wasDim, "basis": wasBasis,
			"dr_cr": wasDrCr, "amount": wasAmount.StringFixed(4), "note": wasNote,
		}
		return appendAudit(ctx, tx, companyID, "budget_line", lineID, OpAlter,
			before, budgetLineAuditDoc(line, target), alteredBy)
	})
}

// DeleteBudgetLine removes one figure and appends a master_audit DELETE row.
func (s *Service) DeleteBudgetLine(ctx context.Context, lineID uuid.UUID, deletedBy string) error {
	if strings.TrimSpace(deletedBy) == "" {
		return fmt.Errorf("%w: deleted_by is required", ErrInvalidBudget)
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return db.ErrNoCompany
	}
	return s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var (
			dim, basis, drCr string
			amount           decimal.Decimal
		)
		err := tx.QueryRow(ctx,
			`SELECT dimension, basis, dr_cr, amount FROM budget_line WHERE id = $1`, lineID).
			Scan(&dim, &basis, &drCr, &amount)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: no budget line %s in this company", ErrNotFound, lineID)
		}
		if err != nil {
			return fmt.Errorf("masters: read budget line: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM budget_line WHERE id = $1`, lineID); err != nil {
			return fmt.Errorf("masters: delete budget line: %w", err)
		}
		before := map[string]any{
			"dimension": dim, "basis": basis, "dr_cr": drCr,
			"amount": amount.StringFixed(4),
		}
		return appendAudit(ctx, tx, companyID, "budget_line", lineID, OpDelete,
			before, nil, deletedBy)
	})
}

// ListBudgetLines returns one plan's lines, grouped by dimension then target
// name. It is also what reporting reads to build the budget column.
func (s *Service) ListBudgetLines(ctx context.Context, budgetID uuid.UUID) ([]BudgetLineRecord, error) {
	var out []BudgetLineRecord
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := requireBudget(ctx, tx, budgetID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, budgetLineSelectSQL, budgetID)
		if err != nil {
			return fmt.Errorf("masters: query budget lines: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			r, err := scanBudgetLine(rows)
			if err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// budgetLineSelectSQL reads one budget's lines with every target's name, nature
// and (for a centre) axis resolved in ONE query.
//
// THREE LEFT JOINS AND NOT A UNION, so a line whose target somehow does not
// resolve still comes back — with an empty name — rather than vanishing. A
// budgeted figure that disappeared from this list would disappear from the
// variance report's budget column too, and the report would understate the plan
// while footing perfectly. 0085's composite FKs make that unreachable; the LEFT
// JOIN means a future schema change cannot make it silent.
const budgetLineSelectSQL = `
	SELECT l.id, l.budget_id, l.dimension,
	       COALESCE(l.account_group_id, l.ledger_id, l.cost_centre_id) AS target_id,
	       COALESCE(g.name, led.name, cc.name, '')                     AS target_name,
	       COALESCE(g.nature::text, lg.nature::text, '')               AS nature,
	       cc.cost_category_id, COALESCE(cat.name, '')                 AS category_name,
	       l.basis, l.dr_cr, l.amount, l.note,
	       to_char(l.created_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')
	  FROM budget_line l
	  LEFT JOIN account_group g   ON g.id   = l.account_group_id
	  LEFT JOIN ledger        led ON led.id = l.ledger_id
	  -- The LEDGER's nature comes from ITS group, which is the only place a nature
	  -- is stored. Two joins to account_group under different aliases, deliberately:
	  -- one is the line's own GROUP target, the other is the group behind a LEDGER
	  -- target, and folding them into one alias would give a group line the nature
	  -- of a ledger's parent on any row where both were set.
	  LEFT JOIN account_group lg  ON lg.id  = led.group_id
	  LEFT JOIN cost_centre   cc  ON cc.id  = l.cost_centre_id
	  LEFT JOIN cost_category cat ON cat.id = cc.cost_category_id
	 WHERE l.budget_id = $1
	 ORDER BY l.dimension, lower(btrim(COALESCE(g.name, led.name, cc.name, ''))), l.id`

func scanBudgetLine(rows pgx.Rows) (BudgetLineRecord, error) {
	var (
		r        BudgetLineRecord
		category *uuid.UUID
	)
	if err := rows.Scan(&r.ID, &r.BudgetID, &r.Dimension, &r.TargetID, &r.TargetName,
		&r.Nature, &category, &r.CostCategoryName, &r.Basis, &r.DrCr, &r.Amount,
		&r.Note, &r.CreatedAt); err != nil {
		return r, fmt.Errorf("masters: scan budget line: %w", err)
	}
	if category != nil {
		r.CostCategoryID = *category
	}
	return r, nil
}

// ---------------------------------------------------------------------------
// Store helpers.
// ---------------------------------------------------------------------------

// requireBudget turns a missing plan into ErrNotFound rather than an empty list.
// An empty line list for a budget that does not exist reads as "this plan has no
// figures", which is a different and more reassuring claim than "there is no such
// plan".
func requireBudget(ctx context.Context, tx pgx.Tx, budgetID uuid.UUID) error {
	var one int
	err := tx.QueryRow(ctx, `SELECT 1 FROM budget WHERE id = $1`, budgetID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: no budget %s in this company", ErrNotFound, budgetID)
	}
	if err != nil {
		return fmt.Errorf("masters: read budget: %w", err)
	}
	return nil
}

func loadBudgetNames(ctx context.Context, tx pgx.Tx) (map[string]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT id, name FROM budget`)
	if err != nil {
		return nil, fmt.Errorf("masters: query budget names: %w", err)
	}
	defer rows.Close()
	out := map[string]uuid.UUID{}
	for rows.Next() {
		var (
			id   uuid.UUID
			name string
		)
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("masters: scan budget name: %w", err)
		}
		out[costNameKey(name)] = id
	}
	return out, rows.Err()
}

// resolveAndValidateLine looks the target up and then applies the pure checks.
//
// THE LOOKUP COMES FIRST because every good refusal below needs the target's
// NAME and its NATURE: "a cost centre cannot be budgeted on a closing balance"
// is a sentence about Mumbai, and the side derivation is impossible without the
// nature. A layer that validated before resolving could only name ids.
func resolveAndValidateLine(
	ctx context.Context, tx pgx.Tx, in BudgetLineInput,
) (BudgetLineInput, budgetTarget, error) {
	dim := strings.ToUpper(strings.TrimSpace(in.Dimension))
	if !contains(KnownBudgetDimensions, dim) {
		return in, budgetTarget{}, fmt.Errorf("%w: %q is not a budget dimension — want one of %s",
			ErrInvalidBudget, in.Dimension, strings.Join(KnownBudgetDimensions, ", "))
	}
	if in.TargetID == uuid.Nil {
		return in, budgetTarget{}, fmt.Errorf("%w: a %s budget line needs a target_id — the "+
			"thing being budgeted. A line with no target could not be compared against "+
			"anything", ErrInvalidBudget, dim)
	}
	target, err := loadBudgetTarget(ctx, tx, dim, in.TargetID)
	if err != nil {
		return in, budgetTarget{}, err
	}
	line, err := validateBudgetLine(in, target)
	return line, target, err
}

// loadBudgetTarget resolves one target and refuses, by name, a target that is not
// in these books.
//
// ⚠️ RLS DOES THE TENANCY. Each query names only the id, so a target in another
// company is invisible and comes back as ErrNotFound — which is the correct
// answer and the correct disclosure. 0085's composite FKs are the authoritative
// backstop; this is the friendly one.
func loadBudgetTarget(
	ctx context.Context, tx pgx.Tx, dimension string, id uuid.UUID,
) (budgetTarget, error) {
	t := budgetTarget{ID: id}
	var err error
	switch dimension {
	case DimensionGroup:
		err = tx.QueryRow(ctx,
			`SELECT name, nature::text FROM account_group WHERE id = $1`, id).
			Scan(&t.Name, &t.Nature)
	case DimensionLedger:
		// The nature lives on the GROUP, never on the ledger — the same place
		// reporting reads it from.
		err = tx.QueryRow(ctx,
			`SELECT l.name, g.nature::text
			   FROM ledger l JOIN account_group g ON g.id = l.group_id
			  WHERE l.id = $1`, id).Scan(&t.Name, &t.Nature)
	case DimensionCostCentre:
		// ⛔ NATURE STAYS EMPTY, and that is the fact the side check reads. A cost
		// centre is an axis of analysis and has no natural side; leaving this ""
		// is what makes validateBudgetLine demand an explicit dr_cr rather than
		// silently defaulting a cost budget to the credit side.
		err = tx.QueryRow(ctx,
			`SELECT cc.name, cc.cost_category_id, cat.name
			   FROM cost_centre cc JOIN cost_category cat ON cat.id = cc.cost_category_id
			  WHERE cc.id = $1`, id).Scan(&t.Name, &t.CategoryID, &t.CategoryName)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return t, fmt.Errorf("%w: no %s %s in this company — a budget line must name something "+
			"these books contain, or the variance report would compare a plan against nothing "+
			"and report it as entirely unspent",
			ErrInvalidBudget, strings.ToLower(strings.ReplaceAll(dimension, "_", " ")), id)
	}
	if err != nil {
		return t, fmt.Errorf("masters: resolve budget target: %w", err)
	}
	return t, nil
}

func insertBudgetLine(
	ctx context.Context, tx pgx.Tx, id, companyID, budgetID uuid.UUID, line BudgetLineInput,
) error {
	// The target lands in the column its dimension names, which is what 0085's
	// budget_line_target_matches_dimension enforces. Three variables rather than
	// a switch building SQL: one INSERT statement means one place for the column
	// list to be right.
	var group, ledger, centre *uuid.UUID
	switch line.Dimension {
	case DimensionGroup:
		group = &line.TargetID
	case DimensionLedger:
		ledger = &line.TargetID
	case DimensionCostCentre:
		centre = &line.TargetID
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO budget_line
		   (id, company_id, budget_id, dimension,
		    account_group_id, ledger_id, cost_centre_id,
		    basis, dr_cr, amount, note)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		id, companyID, budgetID, line.Dimension,
		group, ledger, centre,
		line.Basis, line.DrCr, line.Amount, line.Note)
	if err != nil {
		// ⛔ THE THREE PARTIAL UNIQUE INDEXES, caught BY NAME. A second line for the
		// same target in the same budget would make the variance report show that
		// target twice with the actual counted in full against each — a report whose
		// budget column totals more than anyone set.
		for _, ix := range []string{
			"budget_line_one_per_group",
			"budget_line_one_per_ledger",
			"budget_line_one_per_cost_centre",
		} {
			if isUniqueViolationOn(err, ix) {
				return fmt.Errorf("%w: this budget already has a line for that target. One "+
					"target gets one figure per budget: two would make the variance report "+
					"show it twice, with the actual counted in full against each. Alter the "+
					"existing line instead", ErrBudgetNameTaken)
			}
		}
		if isConstraintViolation(err, "23514", "budget_line_cost_centre_is_transactional") {
			// Unreachable while validateBudgetLine runs first; kept so the database's
			// refusal arrives as a sentence if that order ever changes.
			return fmt.Errorf("%w: a cost centre cannot be budgeted on a closing balance — it "+
				"holds no position, only allocations against dated postings",
				ErrInvalidBudget)
		}
		return fmt.Errorf("masters: insert budget line: %w", err)
	}
	return nil
}

// budgetLineAuditDoc is what master_audit records: NAMES and not ids, because the
// log is read by a person asking "what did this plan say on the day the report
// was run", and a uuid answers nothing.
func budgetLineAuditDoc(line BudgetLineInput, target budgetTarget) map[string]any {
	doc := map[string]any{
		"dimension": line.Dimension,
		"target":    target.Name,
		"basis":     line.Basis,
		"dr_cr":     line.DrCr,
		// FIXED-4, never a float. The log is evidence, and a figure that reads
		// 1e+06 in a JSON document is evidence nobody can use.
		"amount": line.Amount.StringFixed(4),
	}
	if line.Note != "" {
		doc["note"] = line.Note
	}
	if target.CategoryName != "" {
		// The axis, for a cost centre. Without it the log says "Mumbai", which may
		// be a Branch or a Project — see BudgetLineRecord.
		doc["cost_category"] = target.CategoryName
	}
	return doc
}
