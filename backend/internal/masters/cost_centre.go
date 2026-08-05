package masters

// THE COST MASTERS — the AXES of cost analysis, and the POSITIONS on them.
//
// Migration 0040 created cost_category, cost_centre and posting_cost_allocation
// and nothing writes the first two. Until this file existed an axis could only be
// created by raw SQL, so on a real deployment cost allocation did not work at
// all: the voucher path can allocate to a centre, but there was no way to bring a
// centre into existence, and a screen offering an empty picker is a feature that
// is present and unusable. This is the masters half of T2; the allocation itself
// (posting_cost_allocation, inside the hashed payload) is the ledger's.
//
// Tally puts exactly this behind Gateway > Accounts Info > Cost Categories and
// Cost Centres, and ships one category — "Primary Cost Category" — so that a
// company which wants a single axis already has one.
//
// ===========================================================================
// ⛔⛔ A CATEGORY IS AN AXIS. A CENTRE IS A POSITION ON THAT AXIS.
// ===========================================================================
// Read this before changing any rule below, because every naive reading of this
// schema gets it wrong in the same way.
//
// More than one category exists in order to allocate the SAME MONEY IN PARALLEL.
// One ₹1,00,000 salary posting is allocated in full across Departments
// (Sales 60,000 / Admin 40,000) AND AGAIN IN FULL across Branches
// (Mumbai 70,000 / Delhi 30,000). Both allocations are complete, both are
// correct, and they describe the same money from two directions.
//
// The consequence that lands in THIS file is the uniqueness scope:
//
//	A CENTRE NAME IS UNIQUE WITHIN ITS AXIS, NOT WITHIN THE COMPANY.
//
// "Mumbai" may legitimately be both a Branch and a Project, and refusing the
// second one would make a whole axis unbuildable for any company whose branches
// and projects share place names — which in India is most of them. Migration
// 0040's index says so (cost_centre_name_unique is on
// (company_id, cost_category_id, lower(btrim(name)))) and the check below says so
// in the same words, because an operator told "Mumbai already exists" while
// looking at a screen that shows no Mumbai concludes the product is broken.
//
// The mirror rule is that a CATEGORY name is unique across the WHOLE company:
// two axes both called "Branch" would split one allocation set in half and leave
// both halves looking complete.
//
// ===========================================================================
// WHY THIS VALIDATES IN GO WHEN THE TABLE ALREADY HAS THE CONSTRAINTS
// ===========================================================================
// Same division of labour as deductor_status.go and tds_challan.go next door:
// migration 0040's CHECKs, unique indexes and composite foreign keys are THE
// AUTHORITY and they stay. They exist to be right even if this code is wrong.
//
// These checks exist to produce A SENTENCE A SHOPKEEPER CAN ACT ON. Without them
// an operator parenting a Branch under a Department is told
//
//	insert or update on table "cost_centre" violates foreign key constraint
//	"cost_centre_parent_shares_axis_fk"
//
// which names a constraint, not a consequence. Every refusal below names the
// consequence instead — which report goes wrong, and how.
//
// AND ONE RULE HERE HAS NO CONSTRAINT BEHIND IT AT ALL, which is the reason this
// file is not merely a nicer error message: A PARENT CYCLE LONGER THAN ONE HOP.
// Migration 0040 carries cost_centre_not_its_own_parent, so A-under-A is refused
// by the database. A-under-B-under-A satisfies every constraint in the schema —
// the composite FK is satisfied at every edge, the axis matches at every edge,
// no name repeats — and it is stored happily. It surfaces later, in a report that
// walks the hierarchy to roll a child's cost into its parent: the walk never
// terminates. So the cycle check below is the ONLY thing standing between an
// operator and a Cost Category Summary that hangs or exhausts the stack, and
// TestAMultiHopParentCycleIsRefused is the test that must never be deleted.
//
// ===========================================================================
// NORMALISATION MUST MATCH THE INDEX, OR GO ACCEPTS WHAT THE DATABASE REFUSES
// ===========================================================================
// Both unique indexes are on lower(btrim(name)). costNameKey() is that same
// expression in Go, and the name STORED is the trimmed form — so btrim in the
// index is a no-op on anything this file writes and the two cannot drift on
// whitespace.
//
// ⚠️ They can still drift on CASE for a non-ASCII name: Postgres lower() is
// locale-aware and strings.ToLower is Unicode-simple. That residue is handled the
// only honest way — the unique index is caught by NAME on the way back
// (isUniqueViolationOn) and turned into the same sentence the Go check would have
// produced. So the worst case is the right sentence arriving from the database
// instead of from Go, never a raw constraint name and never a duplicate axis.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/khata/cored/internal/platform/db"
)

// ErrInvalidCostMaster is returned for a cost category or centre this layer
// refuses to store on its own facts — a blank name, a centre with no axis, a
// parent on another axis, a parent cycle. A sentinel so the HTTP layer answers
// 400 rather than 500.
var ErrInvalidCostMaster = errors.New("masters: invalid cost master")

// ErrCostNameTaken is a name that is already in use in the scope that matters —
// the COMPANY for a category, the AXIS for a centre.
//
// It is separate from ErrInvalidCostMaster because it maps to a different HTTP
// family and means a different thing to the operator. Nothing about the request
// is malformed: the books already say otherwise, and the fix is to pick another
// name or to edit the row that already exists, not to correct a field. That is
// the same call ErrDuplicateTDSChallanCIN makes next door.
var ErrCostNameTaken = errors.New("masters: cost master name already in use")

// ---------------------------------------------------------------------------
// The wire-facing records.
// ---------------------------------------------------------------------------

// CostCategoryInput creates an AXIS.
type CostCategoryInput struct {
	Name string
	// AllocateNonRevenue is Tally's "Allocate Non-Revenue Items": may this axis be
	// used on a BALANCE-SHEET ledger, or only on a P&L one? DEFAULT FALSE and
	// never required of a caller — departmental analysis of income and expense is
	// the ordinary case, and a default that permits MORE than the operator
	// intended is the wrong way for a default to be wrong (migration 0040 states
	// the same reasoning above the column).
	AllocateNonRevenue bool
}

// CostCategoryRecord is one axis as a screen reads it.
type CostCategoryRecord struct {
	ID                 uuid.UUID
	Name               string
	AllocateNonRevenue bool
	// CentreCount is DERIVED, never stored. It is here because an axis with no
	// centres allocates nothing, and that is the single most common reason a
	// voucher screen shows an axis an operator cannot use.
	CentreCount int
	CreatedBy   string
	CreatedAt   string
}

// CostCentreInput creates a POSITION on an axis, or restates one.
//
// It is a WHOLE-ROW value and AlterCostCentre treats it as one, the same call
// tds_profile.go makes and for a related reason: the name and the parent are
// checked AGAINST EACH OTHER and against the axis (the within-axis uniqueness
// scope, the same-axis parent rule, the cycle), so a partial body is not
// checkable. ⚠️ The practical consequence is stated at the HTTP layer too: an
// alteration that omits ParentID moves the centre to the TOP of its axis. It does
// not leave the parent alone.
type CostCentreInput struct {
	// CostCategoryID is REQUIRED. There is no uncategorised centre and no implicit
	// default axis: a centre with no axis cannot be grouped, and an allocation
	// naming it could not be summed against anything (the sum-to-posting invariant
	// is per (posting, category), so it would have nothing to group by). A company
	// that wants one axis creates one category, exactly as Tally ships "Primary
	// Cost Category".
	CostCategoryID uuid.UUID
	Name           string
	// ParentID is uuid.Nil for a centre at the top of its axis. A parent must sit
	// on the SAME axis — see validateCostCentre.
	ParentID uuid.UUID
}

// CostCentreRecord is one centre as a screen reads it, with the derived
// hierarchy fields a picker needs.
type CostCentreRecord struct {
	ID               uuid.UUID
	Name             string
	CostCategoryID   uuid.UUID
	CostCategoryName string
	// ParentID is uuid.Nil at the top of the axis; ParentName is then empty.
	ParentID   uuid.UUID
	ParentName string
	// Depth and Path are DERIVED from the list itself (decorateCostCentres), never
	// stored: migration 0040 keeps the hierarchy as a self-reference rather than a
	// materialised path precisely so that a re-parent is one UPDATE, and a stored
	// path is a column that disagrees with its parents after the first one.
	//
	// Depth is 0 at the top of the axis. Path is the ancestor names then this one,
	// "Mumbai / Andheri", so a flat list can be rendered as a tree without the
	// client walking parent pointers.
	Depth int
	Path  string
	// AncestryTruncated is a defensive flag, and it should never be true. It says
	// this row's ancestor walk hit its hop bound, i.e. the STORED data contains a
	// parent cycle. Go refuses to create one and the report layer must not hang on
	// one, so the walk is bounded and says when it stopped rather than looping.
	AncestryTruncated bool
	CreatedBy         string
	CreatedAt         string
}

// ---------------------------------------------------------------------------
// The pure layer. No database, no context — every rule below is a function of
// its input, which is what makes cost_centre_test.go able to run on a machine
// with no Postgres. See that file's header for why that matters.
// ---------------------------------------------------------------------------

// costNameKey is lower(btrim(name)) — the expression BOTH unique indexes in
// migration 0040 are built on. It must stay that expression: a Go key that
// normalised differently would accept a name the database then refuses (or refuse
// one it would have accepted), and the operator would meet a raw constraint name
// either way.
func costNameKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// existingCostCategory is one axis already on record, as the pure checks see it.
type existingCostCategory struct {
	ID      uuid.UUID
	Name    string // as stored, for the sentence
	NameKey string // costNameKey(Name)
}

// existingCostCentre is one centre already on record, as the pure checks see it.
// CategoryID/CategoryName are carried because the PARENT may be on another axis —
// which is the case the cross-axis refusal has to be able to describe.
type existingCostCentre struct {
	ID           uuid.UUID
	Name         string
	NameKey      string
	ParentID     uuid.UUID
	CategoryID   uuid.UUID
	CategoryName string
}

// costCentreDraft is a centre about to be written, plus everything about its
// surroundings the pure checks need.
//
// ID is uuid.Nil on a create and the row's own id on an alteration; it is what
// makes "a rename to the same spelling" not a conflict with itself, and what the
// self-parent and cycle checks compare against.
type costCentreDraft struct {
	ID           uuid.UUID
	CategoryID   uuid.UUID
	CategoryName string
	Name         string
	// ParentID is what the caller ASKED for. Parent is the row that was found for
	// it: nil when ParentID names nothing this company can see, which is its own
	// refusal (a centre parented under an id that does not resolve would otherwise
	// be created at the top of the axis, silently ignoring the operator).
	ParentID uuid.UUID
	Parent   *existingCostCentre
}

// validateCostCategoryName is the AXIS name rule: not blank, and unique across
// the WHOLE COMPANY, case- and whitespace-insensitively.
//
// selfID is uuid.Nil on a create and the row's own id on a rename, so restating a
// name with different capitalisation is not a clash with itself.
//
// Returns the name as it should be STORED (trimmed, original case). The case an
// operator typed is kept: "R&D" is not "r&d" on a report, and the index does the
// case folding for uniqueness without the stored value having to lose it.
func validateCostCategoryName(raw string, existing []existingCostCategory, selfID uuid.UUID) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		// Shadows cost_category_name_not_blank.
		return "", fmt.Errorf("%w: a cost category needs a name. It is the AXIS every "+
			"allocation is grouped by — \"Department\", \"Branch\", \"Project\" — and an "+
			"unnamed axis cannot be chosen on a voucher or headed on a report",
			ErrInvalidCostMaster)
	}
	key := costNameKey(name)
	for _, e := range existing {
		if e.ID == selfID {
			continue
		}
		if e.NameKey == key {
			// Shadows cost_category_name_unique.
			return "", fmt.Errorf("%w: this company already has a cost category called %q, and "+
				"%q is the same axis to anyone reading it (the name is compared ignoring case and "+
				"surrounding spaces). Two axes with one name would split one set of allocations in "+
				"half and BOTH HALVES WOULD LOOK COMPLETE — each would sum to the posting on its "+
				"own, so nothing would ever appear to be wrong. Rename the existing axis or add "+
				"centres to it instead",
				ErrCostNameTaken, e.Name, name)
		}
	}
	return name, nil
}

// validateCostCentre is the POSITION rule. `axis` is every centre already on the
// draft's OWN axis — which is the correct scope for the uniqueness check and, per
// migration 0040's composite parent FK, also contains every ancestor a legal
// parent can have. A row on any OTHER axis that reaches this slice is IGNORED by
// the name check rather than trusted, so the scope is a property of this function
// and not of the query that fed it.
//
// Returns the name as it should be stored.
func validateCostCentre(in costCentreDraft, axis []existingCostCentre) (string, error) {
	if in.CategoryID == uuid.Nil {
		return "", fmt.Errorf("%w: a cost centre must be created ON an axis — pick the cost "+
			"category it belongs to. There is no uncategorised cost centre: a centre with no "+
			"axis cannot be grouped, so nothing could ever be reported about it. A company "+
			"that wants a single axis creates one category and puts every centre on it",
			ErrInvalidCostMaster)
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		// Shadows cost_centre_name_not_blank.
		return "", fmt.Errorf("%w: a cost centre needs a name — it is what an operator picks on "+
			"a voucher and what a report prints as the thing that bore the cost",
			ErrInvalidCostMaster)
	}

	// ---- Uniqueness, WITHIN THE AXIS AND NOT WITHIN THE COMPANY. ----------
	// ⛔ Widening this to the company is the single most likely wrong "fix" to
	// this file. TestTheSameCentreNameOnADifferentAxisIsAccepted is the control
	// that fails when someone does.
	key := costNameKey(name)
	for _, e := range axis {
		if e.ID == in.ID {
			continue
		}
		// ⛔ THE SCOPE IS STATED HERE AND NOT LEFT TO THE CALLER'S QUERY. The store
		// already loads only this axis's centres, so this filter is redundant
		// today — and it is here anyway, because the failure it guards against is a
		// future caller widening the load "to be safe". That change looks harmless,
		// compiles, and would start refusing a Project called Mumbai because a
		// Branch called Mumbai exists. One rule, in the one place that decides it.
		if e.CategoryID != in.CategoryID {
			continue
		}
		if e.NameKey == key {
			// Shadows cost_centre_name_unique. The axis NAMED is the one the row we
			// found sits on, which under the rule above is always the draft's own —
			// but naming the found row's axis means the sentence stays true if this
			// scope is ever widened, instead of confidently naming the wrong axis.
			axisName := axisLabel(e.CategoryName)
			if strings.TrimSpace(e.CategoryName) == "" {
				axisName = axisLabel(in.CategoryName)
			}
			return "", fmt.Errorf("%w: the %s axis already has a cost centre called %q. A centre "+
				"name has to be unique only WITHIN ITS OWN AXIS — %q may also exist on another "+
				"axis, so a Branch called Mumbai and a Project called Mumbai are both fine — but "+
				"two of them on %s would split that one place's cost in two, and each half would "+
				"look like the whole",
				ErrCostNameTaken, axisName, e.Name, name, axisName)
		}
	}

	if in.ParentID == uuid.Nil {
		return name, nil
	}

	// ---- The parent has to exist and be visible to this company. ----------
	if in.Parent == nil {
		return "", fmt.Errorf("%w: the cost centre this one was to sit under (%s) is not on record "+
			"for this company, so there is nothing to put it under. It was NOT created at the top "+
			"of the axis instead: a centre in the wrong place on the tree misreports every parent "+
			"above it, and silently",
			ErrNotFound, in.ParentID)
	}

	// ---- A centre may not be its own parent. ------------------------------
	// Shadows cost_centre_not_its_own_parent, which is the ONE cycle the database
	// catches by itself.
	if in.Parent.ID == in.ID {
		return "", fmt.Errorf("%w: %q cannot sit under itself. A centre's cost rolls up into its "+
			"parent, so a centre that is its own parent rolls up into itself and the total grows "+
			"every time the report is run",
			ErrInvalidCostMaster, name)
	}

	// ---- A parent must sit on the SAME axis. ------------------------------
	// Shadows cost_centre_parent_shares_axis_fk, which is a COMPOSITE foreign key
	// on (company_id, parent_id, cost_category_id) precisely so this cannot be
	// left to the application. The sentence is here because the constraint's own
	// message names none of the four things the operator needs to know.
	if in.Parent.CategoryID != in.CategoryID {
		return "", fmt.Errorf("%w: %q is on the %s axis and %q is on the %s axis, so one cannot "+
			"sit under the other. A %s centre cannot sit under a %s centre — one axis would roll "+
			"up into the other, and the Cost Category Summary would then count the same money "+
			"twice: once on %s and again inside %s. Every axis allocates the SAME cost in full and "+
			"in parallel, which is why they must stay separate trees",
			ErrInvalidCostMaster, name, axisLabel(in.CategoryName),
			in.Parent.Name, axisLabel(in.Parent.CategoryName),
			axisLabel(in.CategoryName), axisLabel(in.Parent.CategoryName),
			axisLabel(in.CategoryName), axisLabel(in.Parent.CategoryName))
	}

	// ---- A LONGER CYCLE. NOTHING IN THE DATABASE CATCHES THIS. ------------
	if loop := ancestorLoop(in.ID, *in.Parent, axis); loop != nil {
		return "", fmt.Errorf("%w: putting %q under %q would make it a descendant of itself — %s. "+
			"Every constraint in the database is satisfied by that, which is exactly why it is "+
			"refused here: a report that rolls a child's cost up into its parent walks this chain, "+
			"and a chain that comes back to where it started never ends. The Cost Category Summary "+
			"would stop responding rather than print a wrong number",
			ErrInvalidCostMaster, name, in.Parent.Name, renderLoop(name, loop))
	}

	return name, nil
}

// axisLabel renders an axis name for a sentence, tolerating the case where the
// caller did not have one to hand. A refusal that says `the "" axis` is a refusal
// that reads like a bug, so it says "this" instead.
func axisLabel(name string) string {
	if strings.TrimSpace(name) == "" {
		return "this"
	}
	return name
}

// ancestorLoop walks UP from `parent` and returns the chain of names from the
// parent to the node whose own parent is `id` — i.e. the loop the proposed edge
// would close. It returns nil when there is no loop.
//
// IT IS BOUNDED, and the bound is not defensive padding. A cycle already present
// in the stored rows (put there by raw SQL, or by a future writer that skips this
// check) would make an unbounded walk hang inside a validation — turning the
// guard against a hanging report into a hanging write. len(axis)+1 hops is more
// than any acyclic chain on the axis can need, so exceeding it IS the cycle.
func ancestorLoop(id uuid.UUID, parent existingCostCentre, axis []existingCostCentre) []string {
	if id == uuid.Nil {
		// A create: the row does not exist yet, so nothing can already point at
		// it and no chain can come back to it.
		return nil
	}
	byID := make(map[uuid.UUID]existingCostCentre, len(axis))
	for _, e := range axis {
		byID[e.ID] = e
	}
	chain := []string{parent.Name}
	cur := parent
	for hop := 0; hop <= len(axis)+1; hop++ {
		if cur.ParentID == uuid.Nil {
			return nil
		}
		if cur.ParentID == id {
			return chain
		}
		next, ok := byID[cur.ParentID]
		if !ok {
			// An ancestor that is not on this axis cannot exist —
			// cost_centre_parent_shares_axis_fk forbids it — so this is the end of
			// a chain we cannot see rather than a loop. Refusing here would refuse
			// a legal parent on the strength of a row we failed to load.
			return nil
		}
		chain = append(chain, next.Name)
		cur = next
	}
	// Out of hops: the STORED data already contains a loop. Report it as one.
	return chain
}

// renderLoop turns a chain into the sentence an operator can check against their
// own screen: `Mumbai under Andheri under West under Mumbai`.
func renderLoop(name string, chain []string) string {
	parts := make([]string, 0, len(chain)+2)
	parts = append(parts, name)
	parts = append(parts, chain...)
	parts = append(parts, name)
	return strings.Join(parts, " under ")
}

// decorateCostCentres fills in ParentName, Depth and Path for a whole list, from
// the list itself. Pure, so it is tested without a database.
//
// The list must be CLOSED under "parent of": every parent that appears must be in
// it. Both list queries satisfy that — an unfiltered list is the whole company,
// and a list filtered to one axis contains every ancestor because a parent is on
// the same axis by construction.
//
// THE WALK IS BOUNDED FOR THE SAME REASON ancestorLoop IS. This function runs on
// the read path, so an unbounded walk over a cycle that raw SQL created would
// hang the LIST — the screen an operator would go to in order to find and fix it.
// A bounded walk marks the row and carries on.
func decorateCostCentres(rows []CostCentreRecord) []CostCentreRecord {
	byID := make(map[uuid.UUID]CostCentreRecord, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}
	out := make([]CostCentreRecord, 0, len(rows))
	for _, r := range rows {
		if r.ParentID != uuid.Nil {
			if p, ok := byID[r.ParentID]; ok {
				r.ParentName = p.Name
			}
		}
		// Ancestors, nearest first.
		var ancestors []string
		cur := r
		for hop := 0; hop < len(rows)+1; hop++ {
			if cur.ParentID == uuid.Nil {
				break
			}
			p, ok := byID[cur.ParentID]
			if !ok {
				break
			}
			ancestors = append(ancestors, p.Name)
			cur = p
			if len(ancestors) > len(rows) {
				break
			}
		}
		if cur.ParentID != uuid.Nil {
			if _, ok := byID[cur.ParentID]; ok {
				// Still pointing at a row we have after the bound ran out: a cycle.
				r.AncestryTruncated = true
			}
		}
		r.Depth = len(ancestors)
		parts := make([]string, 0, len(ancestors)+1)
		for i := len(ancestors) - 1; i >= 0; i-- {
			parts = append(parts, ancestors[i])
		}
		parts = append(parts, r.Name)
		r.Path = strings.Join(parts, " / ")
		out = append(out, r)
	}
	return out
}

// ---------------------------------------------------------------------------
// The store. Every statement runs inside RunInTx, so RLS supplies the tenancy
// from app.current_company_id and NO query below carries a WHERE company_id — a
// hand-written clause would be a second copy of the tenancy rule that can
// disagree with the policy.
// ---------------------------------------------------------------------------

// CreateCostCategory creates one AXIS and appends a master_audit CREATE row.
func (s *Service) CreateCostCategory(ctx context.Context, in CostCategoryInput, createdBy string) (uuid.UUID, error) {
	if strings.TrimSpace(createdBy) == "" {
		return uuid.Nil, fmt.Errorf("%w: created_by is required — a master nobody can be asked "+
			"about is a master nobody can correct", ErrInvalidCostMaster)
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return uuid.Nil, db.ErrNoCompany
	}
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("masters: new cost category id: %w", err)
	}

	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		existing, err := loadCostCategories(ctx, tx)
		if err != nil {
			return err
		}
		name, err := validateCostCategoryName(in.Name, existing, uuid.Nil)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO cost_category (id, company_id, name, allocate_non_revenue, created_by)
			 VALUES ($1, $2, $3, $4, $5)`,
			id, companyID, name, in.AllocateNonRevenue, createdBy); err != nil {
			// The read above can lose a race, and the case-folding residue noted in
			// the file header lands here too. Either way the index is the authority
			// and the operator gets the same sentence.
			if isUniqueViolationOn(err, "cost_category_name_unique") {
				return fmt.Errorf("%w: a cost category called %q already exists for this company "+
					"(names are compared ignoring case and surrounding spaces). Two axes with one "+
					"name would split one set of allocations in half with both halves looking "+
					"complete", ErrCostNameTaken, name)
			}
			return fmt.Errorf("masters: insert cost category: %w", err)
		}
		after := map[string]any{
			"name": name, "allocate_non_revenue": in.AllocateNonRevenue,
		}
		return appendAudit(ctx, tx, companyID, "cost_category", id, OpCreate, nil, after, createdBy)
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// RenameCostCategory corrects an axis's name, in place, and appends a
// master_audit ALTER row.
//
// A RENAME AND NOT A DELETE-AND-RECREATE, because the id is what every centre and
// every stored allocation points at: recreating the axis would orphan them, and
// posting_cost_allocation's composite FK would refuse the delete anyway once one
// voucher had used it. Renaming is therefore the only correction that exists for
// an axis, which is also why it must be reachable — see the file header on what
// happens to a master with no writer.
func (s *Service) RenameCostCategory(ctx context.Context, categoryID uuid.UUID, newName, alteredBy string) error {
	if strings.TrimSpace(alteredBy) == "" {
		return fmt.Errorf("%w: altered_by is required", ErrInvalidCostMaster)
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return db.ErrNoCompany
	}
	return s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var (
			was    string
			nonRev bool
		)
		err := tx.QueryRow(ctx,
			`SELECT name, allocate_non_revenue FROM cost_category WHERE id = $1`,
			categoryID).Scan(&was, &nonRev)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("masters: %w: cost category %s", ErrNotFound, categoryID)
		}
		if err != nil {
			return fmt.Errorf("masters: read cost category: %w", err)
		}

		existing, err := loadCostCategories(ctx, tx)
		if err != nil {
			return err
		}
		name, err := validateCostCategoryName(newName, existing, categoryID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE cost_category SET name = $2 WHERE id = $1`, categoryID, name); err != nil {
			if isUniqueViolationOn(err, "cost_category_name_unique") {
				return fmt.Errorf("%w: a cost category called %q already exists for this company",
					ErrCostNameTaken, name)
			}
			return fmt.Errorf("masters: rename cost category: %w", err)
		}
		before := map[string]any{"name": was, "allocate_non_revenue": nonRev}
		after := map[string]any{"name": name, "allocate_non_revenue": nonRev}
		return appendAudit(ctx, tx, companyID, "cost_category", categoryID, OpAlter, before, after, alteredBy)
	})
}

// ListCostCategories returns every axis this company has, with how many centres
// sit on each. Ordered by the folded name so the list matches the order the
// unique index thinks in.
func (s *Service) ListCostCategories(ctx context.Context) ([]CostCategoryRecord, error) {
	if _, ok := db.CompanyFromContext(ctx); !ok {
		return nil, db.ErrNoCompany
	}
	var out []CostCategoryRecord
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT c.id, c.name, c.allocate_non_revenue, c.created_by,
			        to_char(c.created_at, 'YYYY-MM-DD"T"HH24:MI:SSOF:00'),
			        (SELECT count(*) FROM cost_centre cc WHERE cc.cost_category_id = c.id)
			   FROM cost_category c
			  ORDER BY lower(btrim(c.name))`)
		if err != nil {
			return fmt.Errorf("masters: list cost categories: %w", err)
		}
		defer rows.Close()
		out = nil
		for rows.Next() {
			var r CostCategoryRecord
			if err := rows.Scan(&r.ID, &r.Name, &r.AllocateNonRevenue, &r.CreatedBy,
				&r.CreatedAt, &r.CentreCount); err != nil {
				return fmt.Errorf("masters: scan cost category: %w", err)
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

// CreateCostCentre creates one POSITION on an axis and appends a master_audit
// CREATE row.
func (s *Service) CreateCostCentre(ctx context.Context, in CostCentreInput, createdBy string) (uuid.UUID, error) {
	if strings.TrimSpace(createdBy) == "" {
		return uuid.Nil, fmt.Errorf("%w: created_by is required", ErrInvalidCostMaster)
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return uuid.Nil, db.ErrNoCompany
	}
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("masters: new cost centre id: %w", err)
	}

	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		draft, axis, err := prepareCostCentreDraft(ctx, tx, id, in)
		if err != nil {
			return err
		}
		name, err := validateCostCentre(draft, axis)
		if err != nil {
			return err
		}
		if err := insertCostCentre(ctx, tx, id, companyID, name, draft, createdBy); err != nil {
			return err
		}
		after := map[string]any{
			"name": name, "cost_category": draft.CategoryName,
			"parent": parentAuditName(draft),
		}
		return appendAudit(ctx, tx, companyID, "cost_centre", id, OpCreate, nil, after, createdBy)
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// AlterCostCentre restates one centre's NAME and its PARENT together, and
// appends a master_audit ALTER row. It is the rename AND the re-parent, in one
// call, because the two are checked against each other: the within-axis
// uniqueness scope and the same-axis parent rule are both statements about the
// pair, and a caller that could change one without the other could walk the row
// through a state neither check would have allowed.
//
// ⛔ THE AXIS IS IMMUTABLE and an attempt to move a centre to another one is
// refused by name. The database would refuse it too, but only once the centre had
// been allocated to: posting_cost_allocation's composite FK pins
// (centre, category) as a pair, so moving the centre would leave every stored
// allocation claiming an axis the centre is no longer on. Before the first
// allocation the database would ALLOW it — and it would silently re-file the
// centre from one report to another. So the refusal has to be here.
func (s *Service) AlterCostCentre(ctx context.Context, centreID uuid.UUID, in CostCentreInput, alteredBy string) error {
	if strings.TrimSpace(alteredBy) == "" {
		return fmt.Errorf("%w: altered_by is required", ErrInvalidCostMaster)
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return db.ErrNoCompany
	}
	return s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// The row as it stands, with its axis, so the alteration is checked against
		// the axis it is ON rather than the one the body claims.
		var (
			wasName     string
			wasParent   *uuid.UUID
			axisID      uuid.UUID
			axisName    string
			wasParentNm string
		)
		err := tx.QueryRow(ctx,
			`SELECT cc.name, cc.parent_id, cc.cost_category_id, cat.name,
			        COALESCE(p.name, '')
			   FROM cost_centre cc
			   JOIN cost_category cat
			     ON cat.company_id = cc.company_id AND cat.id = cc.cost_category_id
			   LEFT JOIN cost_centre p
			     ON p.company_id = cc.company_id AND p.id = cc.parent_id
			  WHERE cc.id = $1`,
			centreID).Scan(&wasName, &wasParent, &axisID, &axisName, &wasParentNm)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("masters: %w: cost centre %s", ErrNotFound, centreID)
		}
		if err != nil {
			return fmt.Errorf("masters: read cost centre: %w", err)
		}

		// A body may repeat the axis (it is a whole-row restatement) but may not
		// CHANGE it.
		if in.CostCategoryID != uuid.Nil && in.CostCategoryID != axisID {
			return fmt.Errorf("%w: %q sits on the %s axis and cannot be moved to another one. "+
				"Moving it would re-file every allocation ever made to it from one axis's report "+
				"to another's, without touching a single voucher — the Cost Category Summary for "+
				"both axes would change and nothing would say why. Create the centre you want on "+
				"the other axis instead; the same name on a different axis is allowed",
				ErrInvalidCostMaster, wasName, axisLabel(axisName))
		}

		// The alteration is checked on the STORED axis.
		effective := in
		effective.CostCategoryID = axisID
		draft, axis, err := prepareCostCentreDraft(ctx, tx, centreID, effective)
		if err != nil {
			return err
		}
		name, err := validateCostCentre(draft, axis)
		if err != nil {
			return err
		}

		var parentArg *uuid.UUID
		if draft.ParentID != uuid.Nil {
			p := draft.ParentID
			parentArg = &p
		}
		if _, err := tx.Exec(ctx,
			`UPDATE cost_centre SET name = $2, parent_id = $3 WHERE id = $1`,
			centreID, name, parentArg); err != nil {
			return costCentreWriteError(err, name, draft)
		}

		before := map[string]any{
			"name": wasName, "cost_category": axisName, "parent": wasParentNm,
		}
		if wasParent == nil {
			before["parent"] = ""
		}
		after := map[string]any{
			"name": name, "cost_category": axisName, "parent": parentAuditName(draft),
		}
		return appendAudit(ctx, tx, companyID, "cost_centre", centreID, OpAlter, before, after, alteredBy)
	})
}

// ListCostCentres returns this company's centres, optionally narrowed to ONE
// axis. Pass uuid.Nil for every axis.
//
// ⚠️ AN UNFILTERED LIST IS NOT A "TOTAL" OF ANYTHING and callers must not treat it
// as one. It spans axes, and axes describe the SAME money in parallel — anything
// summed across it double-counts. This returns masters, not amounts, precisely so
// that no total can be taken from it; a report has to filter to one axis or group
// by axis, and migration 0040's header says why.
func (s *Service) ListCostCentres(ctx context.Context, categoryID uuid.UUID) ([]CostCentreRecord, error) {
	if _, ok := db.CompanyFromContext(ctx); !ok {
		return nil, db.ErrNoCompany
	}
	var filter *uuid.UUID
	if categoryID != uuid.Nil {
		c := categoryID
		filter = &c
	}
	var out []CostCentreRecord
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT cc.id, cc.name, cc.cost_category_id, cat.name, cc.parent_id,
			        cc.created_by,
			        to_char(cc.created_at, 'YYYY-MM-DD"T"HH24:MI:SSOF:00')
			   FROM cost_centre cc
			   JOIN cost_category cat
			     ON cat.company_id = cc.company_id AND cat.id = cc.cost_category_id
			  WHERE $1::uuid IS NULL OR cc.cost_category_id = $1
			  ORDER BY lower(btrim(cat.name)), lower(btrim(cc.name))`, filter)
		if err != nil {
			return fmt.Errorf("masters: list cost centres: %w", err)
		}
		defer rows.Close()
		out = nil
		for rows.Next() {
			var (
				r      CostCentreRecord
				parent *uuid.UUID
			)
			if err := rows.Scan(&r.ID, &r.Name, &r.CostCategoryID, &r.CostCategoryName,
				&parent, &r.CreatedBy, &r.CreatedAt); err != nil {
				return fmt.Errorf("masters: scan cost centre: %w", err)
			}
			if parent != nil {
				r.ParentID = *parent
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return decorateCostCentres(out), nil
}

// ---------------------------------------------------------------------------
// internal helpers
// ---------------------------------------------------------------------------

// loadCostCategories reads this company's axes for the pure name check. Short by
// construction — an axis is a heading on a report, and a business has a handful.
func loadCostCategories(ctx context.Context, tx pgx.Tx) ([]existingCostCategory, error) {
	rows, err := tx.Query(ctx, `SELECT id, name FROM cost_category`)
	if err != nil {
		return nil, fmt.Errorf("masters: read cost categories: %w", err)
	}
	defer rows.Close()
	var out []existingCostCategory
	for rows.Next() {
		var e existingCostCategory
		if err := rows.Scan(&e.ID, &e.Name); err != nil {
			return nil, fmt.Errorf("masters: scan cost category: %w", err)
		}
		e.NameKey = costNameKey(e.Name)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("masters: read cost categories: %w", err)
	}
	return out, nil
}

// prepareCostCentreDraft assembles everything validateCostCentre needs: the axis
// (which must exist and be visible to this company), the named parent's own row,
// and every centre already on the axis.
//
// LOADING ONLY THE AXIS'S CENTRES IS DELIBERATE AND SUFFICIENT. The uniqueness
// scope is the axis, and every ancestor a legal parent can have is on the same
// axis by migration 0040's composite FK — so a whole-company load would read rows
// no rule looks at, and would grow with a book's size for nothing.
func prepareCostCentreDraft(ctx context.Context, tx pgx.Tx, selfID uuid.UUID, in CostCentreInput) (costCentreDraft, []existingCostCentre, error) {
	draft := costCentreDraft{
		ID:         selfID,
		CategoryID: in.CostCategoryID,
		Name:       in.Name,
		ParentID:   in.ParentID,
	}
	if in.CostCategoryID == uuid.Nil {
		// Let the pure check produce the sentence about a centre with no axis
		// rather than issuing a lookup for uuid.Nil.
		return draft, nil, nil
	}

	err := tx.QueryRow(ctx, `SELECT name FROM cost_category WHERE id = $1`,
		in.CostCategoryID).Scan(&draft.CategoryName)
	if errors.Is(err, pgx.ErrNoRows) {
		// Also the answer for another company's axis: RLS makes it invisible, and
		// "not on record" is the right thing to say about a row this company may
		// not know exists.
		return draft, nil, fmt.Errorf("masters: %w: cost category %s is not on record for this "+
			"company, so no cost centre can be created on it", ErrNotFound, in.CostCategoryID)
	}
	if err != nil {
		return draft, nil, fmt.Errorf("masters: read cost category: %w", err)
	}

	// The parent is looked up WITHOUT an axis filter, on purpose: a parent on
	// another axis has to be FOUND in order to be refused with a sentence naming
	// both axes. Filtering it out here would turn the cross-axis case into "no
	// such centre", which sends the operator looking for a row that is on their
	// screen.
	if in.ParentID != uuid.Nil {
		var (
			p           existingCostCentre
			grandparent *uuid.UUID
		)
		err := tx.QueryRow(ctx,
			`SELECT cc.id, cc.name, cc.parent_id, cc.cost_category_id, cat.name
			   FROM cost_centre cc
			   JOIN cost_category cat
			     ON cat.company_id = cc.company_id AND cat.id = cc.cost_category_id
			  WHERE cc.id = $1`, in.ParentID).
			Scan(&p.ID, &p.Name, &grandparent, &p.CategoryID, &p.CategoryName)
		switch {
		case err == nil:
			// NULL parent_id is uuid.Nil in this package, never a nil pointer: two
			// spellings of "no parent" is how a check ends up testing the wrong one.
			if grandparent != nil {
				p.ParentID = *grandparent
			}
			p.NameKey = costNameKey(p.Name)
			draft.Parent = &p
		case errors.Is(err, pgx.ErrNoRows):
			// Left nil; validateCostCentre turns that into the ErrNotFound sentence.
		default:
			return draft, nil, fmt.Errorf("masters: read parent cost centre: %w", err)
		}
	}

	axis, err := loadAxisCentres(ctx, tx, in.CostCategoryID)
	if err != nil {
		return draft, nil, err
	}
	return draft, axis, nil
}

// loadAxisCentres reads every centre on one axis for the pure checks.
func loadAxisCentres(ctx context.Context, tx pgx.Tx, categoryID uuid.UUID) ([]existingCostCentre, error) {
	rows, err := tx.Query(ctx,
		`SELECT cc.id, cc.name, cc.parent_id, cc.cost_category_id, cat.name
		   FROM cost_centre cc
		   JOIN cost_category cat
		     ON cat.company_id = cc.company_id AND cat.id = cc.cost_category_id
		  WHERE cc.cost_category_id = $1`, categoryID)
	if err != nil {
		return nil, fmt.Errorf("masters: read cost centres on axis: %w", err)
	}
	defer rows.Close()
	var out []existingCostCentre
	for rows.Next() {
		var (
			e      existingCostCentre
			parent *uuid.UUID
		)
		if err := rows.Scan(&e.ID, &e.Name, &parent, &e.CategoryID, &e.CategoryName); err != nil {
			return nil, fmt.Errorf("masters: scan cost centre: %w", err)
		}
		if parent != nil {
			e.ParentID = *parent
		}
		e.NameKey = costNameKey(e.Name)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("masters: read cost centres on axis: %w", err)
	}
	return out, nil
}

// insertCostCentre writes the row, translating the two constraints that can
// still fire after the Go checks pass.
func insertCostCentre(ctx context.Context, tx pgx.Tx, id, companyID uuid.UUID,
	name string, draft costCentreDraft, createdBy string) error {
	var parentArg *uuid.UUID
	if draft.ParentID != uuid.Nil {
		p := draft.ParentID
		parentArg = &p
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO cost_centre
		   (id, company_id, cost_category_id, name, parent_id, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		id, companyID, draft.CategoryID, name, parentArg, createdBy); err != nil {
		return costCentreWriteError(err, name, draft)
	}
	return nil
}

// costCentreWriteError turns the constraints that survive the Go checks into the
// same sentences the Go checks would have produced.
//
// TWO OF THEM ARE REACHABLE AND BOTH ARE RACES, not bugs in the checks above: two
// sessions naming the same centre on one axis at the same moment, and a session
// re-parenting under a centre another session moved to a different axis
// mid-transaction. The constraint is the authority in both cases; this only makes
// sure the operator does not meet its name.
func costCentreWriteError(err error, name string, draft costCentreDraft) error {
	if isUniqueViolationOn(err, "cost_centre_name_unique") {
		return fmt.Errorf("%w: the %s axis already has a cost centre called %q. The name only has "+
			"to be unique within one axis — the same name on a DIFFERENT axis is allowed — but two "+
			"on this one would split that thing's cost in two",
			ErrCostNameTaken, axisLabel(draft.CategoryName), name)
	}
	if isConstraintViolation(err, "23503", "cost_centre_parent_shares_axis_fk") {
		return fmt.Errorf("%w: %q cannot sit under a cost centre on another axis — one axis would "+
			"roll up into the other and the Cost Category Summary would count the same money "+
			"twice", ErrInvalidCostMaster, name)
	}
	if isConstraintViolation(err, "23514", "cost_centre_not_its_own_parent") {
		return fmt.Errorf("%w: %q cannot sit under itself", ErrInvalidCostMaster, name)
	}
	return fmt.Errorf("masters: write cost centre: %w", err)
}

// isConstraintViolation reports whether err is the named Postgres error on the
// named constraint. BY NAME, never by bare SQLSTATE: this table carries several
// foreign keys and several CHECKs, and a bare 23503 test would answer the
// cross-axis question with the company FK's failure.
func isConstraintViolation(err error, code, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code && pgErr.ConstraintName == constraint
}

// parentAuditName is what the audit log records as the parent: the NAME, because
// the log is read by a person asking "what did this master look like on the day
// the report was run", and a uuid answers nothing.
func parentAuditName(draft costCentreDraft) string {
	if draft.Parent == nil {
		return ""
	}
	return draft.Parent.Name
}
