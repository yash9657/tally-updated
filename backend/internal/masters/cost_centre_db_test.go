package masters_test

// DB-BACKED tests for the COST MASTERS (migration 0040).
//
// ⚠️ WRITTEN BUT NOT EXECUTED IN THE SESSION THAT ADDED THEM. Every worktree on
// this machine shares one Postgres on :5433 and concurrent runs race, so these
// were written and type-checked (go vet compiles test files) and NOT RUN. They are
// part of the deliverable; the first serial run of
//
//	go test ./internal/masters/ -count=1
//
// against a free database is the evidence. NOTHING BELOW HAS BEEN OBSERVED TO
// PASS. Treat a failure here as a real defect in cost_centre.go, not as flake:
// none of them tolerate a missing database silently — setup() skips only when
// Postgres is unreachable, and the handoff's §1.3 census (`grep -c -- '--- SKIP'`
// must be 0) is what catches that.
//
// Each test gets its company from setup() in masters_test.go, which registers
// testdb.Cleanup the moment the company exists. Any company created here does the
// same. Nothing calls testdb.Keep.
//
// ===========================================================================
// WHAT THESE TESTS ARE FOR THAT THE PURE ONES CANNOT BE
// ===========================================================================
// cost_centre_test.go proves the RULES. Only the database can prove the four
// things that are properties of the schema rather than of the Go:
//
//   - the unique index really is scoped to (company, category, name), so
//     "Mumbai" really can be both a Branch and a Project IN A REAL BOOK;
//   - the composite parent FK really refuses a cross-axis parent, so the Go
//     refusal is a nicer message for a rule that holds and not the only rule;
//   - RLS really confines an axis to its company, with NO WHERE company_id
//     anywhere in the store;
//   - and A MULTI-HOP CYCLE REALLY IS ACCEPTED BY THE DATABASE, which is the
//     claim cost_centre.go's cycle check rests on. If that ever stops being true
//     the Go check has become redundant and should be reconsidered — but until
//     then it is the only guard, and TestTheDatabaseDoesNotCatchAMultiHopCycle
//     is the proof of the premise.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
)

// costAxis creates one axis and fails the test if it cannot, so every test below
// starts from a fixture whose creation is not the thing under test.
func costAxis(t *testing.T, m *masters.Service, ctx context.Context, name string) uuid.UUID {
	t.Helper()
	id, err := m.CreateCostCategory(ctx, masters.CostCategoryInput{Name: name}, "tester")
	if err != nil {
		t.Fatalf("create cost category %q: %v", name, err)
	}
	return id
}

// costCentre creates one centre on an axis, with an optional parent.
func costCentre(t *testing.T, m *masters.Service, ctx context.Context,
	axis uuid.UUID, name string, parent uuid.UUID) uuid.UUID {
	t.Helper()
	id, err := m.CreateCostCentre(ctx, masters.CostCentreInput{
		CostCategoryID: axis, Name: name, ParentID: parent,
	}, "tester")
	if err != nil {
		t.Fatalf("create cost centre %q: %v", name, err)
	}
	return id
}

// findCentre returns the listed record for one id.
func findCentre(t *testing.T, rows []masters.CostCentreRecord, id uuid.UUID) masters.CostCentreRecord {
	t.Helper()
	for _, r := range rows {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("cost centre %s is not in the list of %d", id, len(rows))
	return masters.CostCentreRecord{}
}

// ---------------------------------------------------------------------------
// THE ROUND TRIP
// ---------------------------------------------------------------------------

// TestCostMastersRoundTrip. Not "a write succeeds" — that every field an operator
// keyed comes back as they keyed it, and that the DERIVED fields (the centre
// count, the parent name, the depth, the path) are derived from the rows rather
// than from anything stored.
func TestCostMastersRoundTrip(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	branch := costAxis(t, m, ctx, "Branch")
	// allocate_non_revenue is NOT required and defaults FALSE; the second axis
	// sets it, so both states are proved to round trip.
	project, err := m.CreateCostCategory(ctx, masters.CostCategoryInput{
		Name: "Project", AllocateNonRevenue: true,
	}, "tester")
	if err != nil {
		t.Fatalf("create Project axis: %v", err)
	}

	mumbai := costCentre(t, m, ctx, branch, "Mumbai", uuid.Nil)
	andheri := costCentre(t, m, ctx, branch, "Andheri", mumbai)
	_ = costCentre(t, m, ctx, project, "Metro Phase 3", uuid.Nil)

	cats, err := m.ListCostCategories(ctx)
	if err != nil {
		t.Fatalf("list cost categories: %v", err)
	}
	if len(cats) != 2 {
		t.Fatalf("got %d cost categories, want 2", len(cats))
	}
	// Ordered by the folded name, so Branch precedes Project.
	if cats[0].Name != "Branch" || cats[1].Name != "Project" {
		t.Errorf("cost categories are ordered %q, %q", cats[0].Name, cats[1].Name)
	}
	if cats[0].AllocateNonRevenue {
		t.Error("Branch has allocate_non_revenue true; the column defaults FALSE and the input " +
			"did not set it — a default that permits MORE than the operator asked for is the " +
			"wrong way for a default to be wrong")
	}
	if !cats[1].AllocateNonRevenue {
		t.Error("Project has allocate_non_revenue false; the input set it true")
	}
	if cats[0].CentreCount != 2 || cats[1].CentreCount != 1 {
		t.Errorf("centre counts are %d and %d, want 2 and 1 — the count is derived per axis, and "+
			"a count that spanned axes would be the first double-count in the feature",
			cats[0].CentreCount, cats[1].CentreCount)
	}
	if cats[0].CreatedBy != "tester" || cats[0].CreatedAt == "" {
		t.Errorf("audit trail on the axis is %q / %q", cats[0].CreatedBy, cats[0].CreatedAt)
	}

	// ---- The centres, unfiltered and filtered. ----------------------------
	all, err := m.ListCostCentres(ctx, uuid.Nil)
	if err != nil {
		t.Fatalf("list cost centres: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("got %d cost centres, want 3", len(all))
	}
	child := findCentre(t, all, andheri)
	if child.ParentID != mumbai || child.ParentName != "Mumbai" {
		t.Errorf("Andheri's parent reads %s / %q, want %s / Mumbai", child.ParentID, child.ParentName, mumbai)
	}
	if child.Depth != 1 || child.Path != "Mumbai / Andheri" {
		t.Errorf("Andheri is depth %d path %q, want 1 and \"Mumbai / Andheri\"", child.Depth, child.Path)
	}
	if child.AncestryTruncated {
		t.Error("Andheri is flagged as sitting in a parent cycle and it does not")
	}
	if child.CostCategoryName != "Branch" {
		t.Errorf("Andheri's axis reads %q, want Branch", child.CostCategoryName)
	}

	onBranch, err := m.ListCostCentres(ctx, branch)
	if err != nil {
		t.Fatalf("list cost centres on one axis: %v", err)
	}
	if len(onBranch) != 2 {
		t.Fatalf("the Branch axis lists %d centres, want 2 — the filter is the ONLY way a caller "+
			"gets a set it may legitimately total, because a list spanning axes describes the same "+
			"money twice", len(onBranch))
	}

	// ---- The rename, and the re-parent. -----------------------------------
	if err := m.RenameCostCategory(ctx, branch, "  Branches ", "corrector"); err != nil {
		t.Fatalf("rename the Branch axis: %v", err)
	}
	cats, err = m.ListCostCategories(ctx)
	if err != nil {
		t.Fatalf("re-list cost categories: %v", err)
	}
	if cats[0].Name != "Branches" {
		t.Errorf("the renamed axis reads %q, want %q (surrounding space stripped)", cats[0].Name, "Branches")
	}
	if cats[0].ID != branch {
		t.Errorf("the rename changed the axis id from %s to %s; every centre and every stored "+
			"allocation points at that id", branch, cats[0].ID)
	}

	// A whole-row alteration: rename Andheri AND lift it to the top of the axis.
	if err := m.AlterCostCentre(ctx, andheri, masters.CostCentreInput{
		Name: "Andheri East", ParentID: uuid.Nil,
	}, "corrector"); err != nil {
		t.Fatalf("rename and re-parent Andheri: %v", err)
	}
	onBranch, err = m.ListCostCentres(ctx, branch)
	if err != nil {
		t.Fatalf("re-list the Branch axis: %v", err)
	}
	moved := findCentre(t, onBranch, andheri)
	if moved.Name != "Andheri East" {
		t.Errorf("the renamed centre reads %q", moved.Name)
	}
	if moved.ParentID != uuid.Nil || moved.Depth != 0 || moved.Path != "Andheri East" {
		t.Errorf("Andheri East is still under %s (depth %d, path %q); an alteration that omits "+
			"the parent moves the centre to the TOP of its axis — CostCentreInput is a whole-row "+
			"restatement, not a patch", moved.ParentID, moved.Depth, moved.Path)
	}

	// And the edit log carries both acts.
	var audits int
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM master_audit
			  WHERE entity_type IN ('cost_category', 'cost_centre')`).Scan(&audits)
	}); err != nil {
		t.Fatalf("count master_audit rows: %v", err)
	}
	// 2 categories + 3 centres created, 1 category renamed, 1 centre altered.
	if audits != 7 {
		t.Errorf("master_audit has %d cost-master rows, want 7 — a master change with no audit "+
			"row cannot be answered for when a report is questioned", audits)
	}
}

// ---------------------------------------------------------------------------
// ⛔⛔ THE UNIQUENESS SCOPE, IN THE REAL DATABASE
// ---------------------------------------------------------------------------

// TestTheSameCentreNameOnTwoAxesIsAcceptedByTheDatabase is the DB-level control
// for the whole model. Migration 0040's index is on
// (company_id, cost_category_id, lower(btrim(name))), so "Mumbai" may be both a
// Branch and a Project — and in India that is the ordinary case, not a corner.
//
// ⚠️ THE REFUSAL LEG IS SECOND AND USES THE SAME NAME ON PURPOSE. If the accept
// leg were second it could pass for the wrong reason (a rolled-back transaction
// leaving no row), and if the two legs used different names the refusal would not
// be about the scope at all.
func TestTheSameCentreNameOnTwoAxesIsAcceptedByTheDatabase(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	branch := costAxis(t, m, ctx, "Branch")
	project := costAxis(t, m, ctx, "Project")

	if _, err := m.CreateCostCentre(ctx, masters.CostCentreInput{
		CostCategoryID: branch, Name: "Mumbai",
	}, "tester"); err != nil {
		t.Fatalf("create the Branch called Mumbai: %v", err)
	}
	// THE CONTROL. Same name, different axis.
	if _, err := m.CreateCostCentre(ctx, masters.CostCentreInput{
		CostCategoryID: project, Name: "Mumbai",
	}, "tester"); err != nil {
		t.Fatalf("a PROJECT called Mumbai was refused while a BRANCH called Mumbai exists: %v\n"+
			"A centre name is unique WITHIN ITS AXIS only. Refusing this makes the second axis "+
			"unbuildable for any company whose branches and projects share place names, and the "+
			"operator is told a name is taken that the screen they are looking at does not show", err)
	}

	// And the refusal that IS real: a second Mumbai on the SAME axis.
	_, err := m.CreateCostCentre(ctx, masters.CostCentreInput{
		CostCategoryID: branch, Name: " mumbai ",
	}, "tester")
	if !errors.Is(err, masters.ErrCostNameTaken) {
		t.Fatalf("a second Branch called \" mumbai \" was accepted (err=%v); the index folds case "+
			"and surrounding space, so two of them would split one place's cost in two with each "+
			"half looking whole", err)
	}

	// Both survived, on their own axes.
	onBranch, err := m.ListCostCentres(ctx, branch)
	if err != nil {
		t.Fatalf("list the Branch axis: %v", err)
	}
	if len(onBranch) != 1 {
		t.Errorf("the Branch axis has %d centres, want 1", len(onBranch))
	}
	onProject, err := m.ListCostCentres(ctx, project)
	if err != nil {
		t.Fatalf("list the Project axis: %v", err)
	}
	if len(onProject) != 1 {
		t.Errorf("the Project axis has %d centres, want 1", len(onProject))
	}
}

// TestACategoryNameIsUniqueAcrossTheCompanyInTheDatabase — the mirror rule, in the
// database. Two axes with one name would split one allocation set in half and both
// halves would sum to the posting on their own.
func TestACategoryNameIsUniqueAcrossTheCompanyInTheDatabase(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	costAxis(t, m, ctx, "Department")
	_, err := m.CreateCostCategory(ctx, masters.CostCategoryInput{Name: "  DEPARTMENT "}, "tester")
	if !errors.Is(err, masters.ErrCostNameTaken) {
		t.Fatalf("a second cost category called \"  DEPARTMENT \" was accepted (err=%v)", err)
	}
	// CONTROL: a different name is accepted, so the refusal is not "every axis is
	// refused after the first".
	costAxis(t, m, ctx, "Branch")
}

// ---------------------------------------------------------------------------
// THE PARENT RULES, WITH THE COMPOSITE FK BEHIND THEM
// ---------------------------------------------------------------------------

// TestACrossAxisParentIsRefusedWithASameAxisControl.
//
// ⚠️ THE NAMES ARE CHOSEN SO THAT NEITHER LEG CAN TRIP THE UNIQUENESS INDEX
// BEFORE REACHING THE RULE UNDER TEST. "Andheri" does not exist on the Branch axis
// when the refusal is attempted, so the refusal cannot be a duplicate-name
// refusal wearing the wrong sentinel — and the control then reuses the SAME name,
// which additionally proves the refusal stored nothing.
func TestACrossAxisParentIsRefusedWithASameAxisControl(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	branch := costAxis(t, m, ctx, "Branch")
	department := costAxis(t, m, ctx, "Department")
	mumbai := costCentre(t, m, ctx, branch, "Mumbai", uuid.Nil)
	sales := costCentre(t, m, ctx, department, "Sales", uuid.Nil)

	// THE REFUSAL: a Branch centre under a Department centre.
	_, err := m.CreateCostCentre(ctx, masters.CostCentreInput{
		CostCategoryID: branch, Name: "Andheri", ParentID: sales,
	}, "tester")
	if !errors.Is(err, masters.ErrInvalidCostMaster) {
		t.Fatalf("a Branch centre was parented under a Department centre (err=%v). One axis would "+
			"roll up into the other and the Cost Category Summary would count the same money "+
			"twice", err)
	}
	for _, want := range []string{"Branch", "Department", "twice"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q, so it does not explain to the operator why "+
				"the org chart they drew is not allowed: %v", want, err)
		}
	}

	// THE CONTROL, and the atomicity proof in one: the same name, under a parent
	// on its OWN axis, must be accepted.
	andheri := costCentre(t, m, ctx, branch, "Andheri", mumbai)
	rows, err := m.ListCostCentres(ctx, branch)
	if err != nil {
		t.Fatalf("list the Branch axis: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("the Branch axis has %d centres, want 2 — the refused cross-axis attempt left a "+
			"row behind", len(rows))
	}
	if got := findCentre(t, rows, andheri); got.ParentID != mumbai {
		t.Errorf("Andheri's parent is %s, want Mumbai (%s)", got.ParentID, mumbai)
	}

	// The SAME refusal on the alteration path, which is the one an operator
	// actually reaches (a re-parent, not a create).
	if err := m.AlterCostCentre(ctx, andheri, masters.CostCentreInput{
		Name: "Andheri", ParentID: sales,
	}, "corrector"); !errors.Is(err, masters.ErrInvalidCostMaster) {
		t.Errorf("re-parenting a Branch centre under a Department centre was accepted: %v", err)
	}
}

// TestACentresAxisCannotBeChanged. The refusal is in Go and it has to be: before
// the centre's first allocation the DATABASE WOULD ALLOW the move, and it would
// silently re-file the centre from one axis's report to another's. After the first
// allocation posting_cost_allocation's composite FK refuses it — by constraint
// name, and by then the damage the message describes is the one an operator cannot
// undo without help.
func TestACentresAxisCannotBeChanged(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	branch := costAxis(t, m, ctx, "Branch")
	project := costAxis(t, m, ctx, "Project")
	mumbai := costCentre(t, m, ctx, branch, "Mumbai", uuid.Nil)

	err := m.AlterCostCentre(ctx, mumbai, masters.CostCentreInput{
		CostCategoryID: project, Name: "Mumbai",
	}, "corrector")
	if !errors.Is(err, masters.ErrInvalidCostMaster) {
		t.Fatalf("a centre was moved from the Branch axis to the Project axis (err=%v); every "+
			"allocation ever made to it would have been re-filed from one report to another "+
			"without a voucher being touched", err)
	}

	// CONTROL: restating the SAME axis is fine — a whole-row PUT repeats it.
	if err := m.AlterCostCentre(ctx, mumbai, masters.CostCentreInput{
		CostCategoryID: branch, Name: "Mumbai City",
	}, "corrector"); err != nil {
		t.Fatalf("restating a centre's own axis was refused: %v — a whole-row alteration has to "+
			"be able to repeat the axis it is already on", err)
	}
}

// TestAMultiHopCycleIsRefusedByGo. The pure test proves the rule; this proves it
// survives a real re-parent through the store, with the ancestors read back out of
// the database rather than handed in by a fixture.
func TestAMultiHopCycleIsRefusedByGo(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	branch := costAxis(t, m, ctx, "Branch")
	mumbai := costCentre(t, m, ctx, branch, "Mumbai", uuid.Nil)
	andheri := costCentre(t, m, ctx, branch, "Andheri", mumbai)
	versova := costCentre(t, m, ctx, branch, "Versova", andheri)

	err := m.AlterCostCentre(ctx, mumbai, masters.CostCentreInput{
		Name: "Mumbai", ParentID: versova,
	}, "corrector")
	if !errors.Is(err, masters.ErrInvalidCostMaster) {
		t.Fatalf("Mumbai was re-parented under Versova, which is three hops below it (err=%v). "+
			"The row is now stored, satisfies every constraint in migration 0040, and any report "+
			"that rolls a child's cost up into its parent walks this chain and never returns", err)
	}
	if !strings.Contains(err.Error(), "Mumbai under Versova under Andheri under Mumbai") {
		t.Errorf("the refusal does not show the loop it found, which is the one thing that lets "+
			"an operator see which link to break: %v", err)
	}

	// CONTROL: a legal re-parent through the same code path.
	if err := m.AlterCostCentre(ctx, versova, masters.CostCentreInput{
		Name: "Versova", ParentID: mumbai,
	}, "corrector"); err != nil {
		t.Fatalf("a legal re-parent (Versova under Mumbai) was refused: %v — the cycle check is "+
			"refusing every re-parent and the assertion above is proving nothing", err)
	}
}

// TestTheDatabaseDoesNotCatchAMultiHopCycle IS THE PROOF OF THE PREMISE that the
// Go cycle check rests on. cost_centre.go claims that A-under-B-under-A satisfies
// every constraint in migration 0040 and is stored happily; if that claim is
// false, the Go check is redundant and this file should say so.
//
// So the cycle is created by RAW SQL, deliberately bypassing the Go layer, and the
// assertion is that THE DATABASE ACCEPTED IT. It is then undone in the same test,
// so no cyclic row survives into another test's view or into teardown.
//
// ⛔ IF THIS TEST EVER FAILS, DO NOT "FIX" IT BY DELETING IT. It failing means
// migration 0040 grew a guard, and the right response is to check whether the Go
// check and the new guard now disagree about what a cycle is.
func TestTheDatabaseDoesNotCatchAMultiHopCycle(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	branch := costAxis(t, m, ctx, "Branch")
	mumbai := costCentre(t, m, ctx, branch, "Mumbai", uuid.Nil)
	andheri := costCentre(t, m, ctx, branch, "Andheri", mumbai)

	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE cost_centre SET parent_id = $2 WHERE id = $1`, mumbai, andheri)
		return err
	}); err != nil {
		t.Fatalf("the database REFUSED Mumbai -> Andheri -> Mumbai: %v\n"+
			"cost_centre.go's cycle check is documented as the only guard against this, on the "+
			"strength of migration 0040 carrying nothing that catches a cycle longer than one "+
			"hop. That is now untrue, and the two guards must be reconciled deliberately", err)
	}
	// The undo is registered THE MOMENT the cycle exists, not written at the bottom
	// of the test: a t.Fatalf below would skip a trailing undo and leave a cyclic
	// row for the teardown to trip over. Cleanups run LIFO, so this runs before
	// testdb.Cleanup's drop.
	t.Cleanup(func() {
		if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE cost_centre SET parent_id = NULL WHERE id = $1`, mumbai)
			return err
		}); err != nil {
			t.Errorf("undo the deliberately-created cycle: %v", err)
		}
	})

	// The list must survive the cycle rather than hang on it — this is the screen
	// an operator would go to in order to find and fix exactly this.
	rows, err := m.ListCostCentres(ctx, branch)
	if err != nil {
		t.Fatalf("listing an axis that contains a stored cycle failed: %v", err)
	}
	for _, r := range rows {
		if !r.AncestryTruncated {
			t.Errorf("%s sits in a stored parent cycle and the list does not flag it; a caller "+
				"walking Path has no way to know the tree is not a tree", r.Name)
		}
	}
}

// TestAParentThatDoesNotResolveIsRefused. A typo'd parent id, or another tenant's
// centre (which RLS makes invisible), must be a refusal — not a centre quietly
// created at the top of the axis, which would misreport every parent above it.
func TestAParentThatDoesNotResolveIsRefused(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	branch := costAxis(t, m, ctx, "Branch")
	ghost, _ := uuid.NewV7()
	_, err := m.CreateCostCentre(ctx, masters.CostCentreInput{
		CostCategoryID: branch, Name: "Pune", ParentID: ghost,
	}, "tester")
	if !errors.Is(err, masters.ErrNotFound) {
		t.Fatalf("a centre naming a parent that does not exist was accepted (err=%v)", err)
	}
	rows, err := m.ListCostCentres(ctx, branch)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("the Branch axis has %d centres; the refused create left a row behind", len(rows))
	}
}

// TestACentreOnAnAxisThatDoesNotExistIsRefused, which is also the RLS answer for
// another company's axis — see the RLS test below.
func TestACentreOnAnAxisThatDoesNotExistIsRefused(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	ghost, _ := uuid.NewV7()
	if _, err := m.CreateCostCentre(ctx, masters.CostCentreInput{
		CostCategoryID: ghost, Name: "Mumbai",
	}, "tester"); !errors.Is(err, masters.ErrNotFound) {
		t.Fatalf("a centre on a non-existent axis was accepted (err=%v)", err)
	}
	// And a centre with NO axis at all is refused as invalid input rather than as
	// not-found: there is nothing to look up, and the operator has to pick one.
	if _, err := m.CreateCostCentre(ctx, masters.CostCentreInput{
		Name: "Mumbai",
	}, "tester"); !errors.Is(err, masters.ErrInvalidCostMaster) {
		t.Fatalf("a centre with no cost category was accepted (err=%v)", err)
	}
}

// ---------------------------------------------------------------------------
// TENANCY
// ---------------------------------------------------------------------------

// TestACostCategoryIsInvisibleToAnotherCompany. There is NO `WHERE company_id`
// anywhere in cost_centre.go; RLS supplies it from app.current_company_id, which
// RunInTx sets as the transaction's first statement. This is the test that proves
// the absence is correct rather than an omission.
//
// It also proves the uniqueness scopes are PER COMPANY: company B creating its own
// "Branch" while A has one must be accepted, or every tenant after the first would
// find the obvious axis names taken.
func TestACostCategoryIsInvisibleToAnotherCompany(t *testing.T) {
	pool, ctxA, _ := setup(t)
	m := masters.NewService(pool)

	// A second company in the same database, torn down like the first.
	otherID, _ := uuid.NewV7()
	ctxB := db.WithCompany(context.Background(), otherID)
	if _, err := identity.NewService(pool).CreateCompany(ctxB, identity.Company{
		ID: otherID, Name: "Other Co " + otherID.String()[:8], FYStartDate: "2025-04-01",
	}); err != nil {
		t.Fatalf("create second company: %v", err)
	}
	testdb.Cleanup(t, otherID)

	branchA := costAxis(t, m, ctxA, "Branch")
	costCentre(t, m, ctxA, branchA, "Mumbai", uuid.Nil)

	// B sees nothing of A's.
	catsB, err := m.ListCostCategories(ctxB)
	if err != nil {
		t.Fatalf("company B lists cost categories: %v", err)
	}
	if len(catsB) != 0 {
		t.Fatalf("company B can see %d of company A's cost categories — departmental "+
			"profitability is one tenant's most commercially sensitive figure", len(catsB))
	}
	centresB, err := m.ListCostCentres(ctxB, uuid.Nil)
	if err != nil {
		t.Fatalf("company B lists cost centres: %v", err)
	}
	if len(centresB) != 0 {
		t.Fatalf("company B can see %d of company A's cost centres", len(centresB))
	}

	// B may use the same axis name, and the same centre name on it.
	branchB := costAxis(t, m, ctxB, "Branch")
	costCentre(t, m, ctxB, branchB, "Mumbai", uuid.Nil)

	// ⛔ AND B MAY NOT CREATE A CENTRE ON A'S AXIS BY NAMING ITS ID. The FK is
	// (company_id, cost_category_id), so the database would refuse it too — but the
	// refusal has to be "not on record", because telling B that the id exists
	// somewhere is itself a leak.
	if _, err := m.CreateCostCentre(ctxB, masters.CostCentreInput{
		CostCategoryID: branchA, Name: "Chennai",
	}, "tester-b"); !errors.Is(err, masters.ErrNotFound) {
		t.Errorf("company B created a cost centre on company A's axis (err=%v)", err)
	}

	// A is untouched: one axis, one centre.
	catsA, err := m.ListCostCategories(ctxA)
	if err != nil {
		t.Fatalf("company A re-lists: %v", err)
	}
	// ⛔ THE LENGTH IS CHECKED FIRST AND SEPARATELY, and that is not style. The
	// previous form was `if len(catsA) != 1 || catsA[0].CentreCount != 1` with
	// catsA[0] ALSO in the t.Errorf argument list — and Go evaluates call arguments
	// before invoking the function. So on the exact leak this test exists to catch
	// (RLS stops confining cost_category, A's list comes back empty), it would
	// index [0] of a nil slice and PANIC. A panic in the test goroutine aborts the
	// binary, which means neither company's testdb.Cleanup runs and the residue
	// census then fails on unrelated tables — turning the one clear failure this
	// test was written to produce into a confusing cascade.
	if len(catsA) != 1 {
		t.Fatalf("company A sees %d axes, want 1: B's writes reached A, or A's own rows became "+
			"invisible to it — either way cost_category is no longer confined by RLS", len(catsA))
	}
	if catsA[0].CentreCount != 1 {
		t.Errorf("company A's axis now reports %d centres, want 1: B's centre reached A's axis",
			catsA[0].CentreCount)
	}
}

// TestTheCostMastersRefuseWithoutACompanyInContext. The company comes from the
// context, never from a URL path, because RLS is what supplies tenancy and RunInTx
// sets the GUC. A missing company must be a refusal, never a query across tenants.
func TestTheCostMastersRefuseWithoutACompanyInContext(t *testing.T) {
	pool, _, _ := setup(t)
	m := masters.NewService(pool)
	bare := context.Background()

	if _, err := m.CreateCostCategory(bare, masters.CostCategoryInput{Name: "Branch"}, "t"); !errors.Is(err, db.ErrNoCompany) {
		t.Errorf("CreateCostCategory without a company: want db.ErrNoCompany, got %v", err)
	}
	if _, err := m.ListCostCategories(bare); !errors.Is(err, db.ErrNoCompany) {
		t.Errorf("ListCostCategories without a company: want db.ErrNoCompany, got %v", err)
	}
	if _, err := m.CreateCostCentre(bare, masters.CostCentreInput{
		CostCategoryID: uuid.New(), Name: "Mumbai",
	}, "t"); !errors.Is(err, db.ErrNoCompany) {
		t.Errorf("CreateCostCentre without a company: want db.ErrNoCompany, got %v", err)
	}
	if _, err := m.ListCostCentres(bare, uuid.Nil); !errors.Is(err, db.ErrNoCompany) {
		t.Errorf("ListCostCentres without a company: want db.ErrNoCompany, got %v", err)
	}
	if err := m.RenameCostCategory(bare, uuid.New(), "X", "t"); !errors.Is(err, db.ErrNoCompany) {
		t.Errorf("RenameCostCategory without a company: want db.ErrNoCompany, got %v", err)
	}
	if err := m.AlterCostCentre(bare, uuid.New(), masters.CostCentreInput{Name: "X"}, "t"); !errors.Is(err, db.ErrNoCompany) {
		t.Errorf("AlterCostCentre without a company: want db.ErrNoCompany, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// THE CONSTRAINTS ARE STILL THE AUTHORITY
// ---------------------------------------------------------------------------

// TestTheCostMasterConstraintsAreStillTheAuthority. The Go checks exist to produce
// a SENTENCE, not to replace migration 0040's constraints. This drives each value
// past the Go layer by writing it directly and asserts the database still refuses.
//
// If any leg passes, the corresponding constraint has been dropped and the Go
// message is the only thing left — which is exactly the arrangement every header in
// this package says it is not.
func TestTheCostMasterConstraintsAreStillTheAuthority(t *testing.T) {
	pool, ctx, companyID := setup(t)
	m := masters.NewService(pool)

	branch := costAxis(t, m, ctx, "Branch")
	department := costAxis(t, m, ctx, "Department")
	// Mumbai exists so the duplicate-name leg below has something to collide with.
	costCentre(t, m, ctx, branch, "Mumbai", uuid.Nil)
	sales := costCentre(t, m, ctx, department, "Sales", uuid.Nil)

	raw := func(sql string, args ...any) error {
		return pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, sql, args...)
			return err
		})
	}

	for _, leg := range []struct {
		what       string
		constraint string
		run        func() error
	}{
		{
			what:       "a blank cost category name",
			constraint: "cost_category_name_not_blank",
			run: func() error {
				id, _ := uuid.NewV7()
				return raw(`INSERT INTO cost_category (id, company_id, name, created_by)
				            VALUES ($1, $2, '   ', 'raw')`, id, companyID)
			},
		},
		{
			what:       "a blank cost centre name",
			constraint: "cost_centre_name_not_blank",
			run: func() error {
				id, _ := uuid.NewV7()
				return raw(`INSERT INTO cost_centre
				              (id, company_id, cost_category_id, name, created_by)
				            VALUES ($1, $2, $3, '  ', 'raw')`, id, companyID, branch)
			},
		},
		{
			what:       "a second Mumbai on the Branch axis",
			constraint: "cost_centre_name_unique",
			run: func() error {
				id, _ := uuid.NewV7()
				return raw(`INSERT INTO cost_centre
				              (id, company_id, cost_category_id, name, created_by)
				            VALUES ($1, $2, $3, ' MUMBAI ', 'raw')`, id, companyID, branch)
			},
		},
		{
			what:       "a Branch centre parented under a Department centre",
			constraint: "cost_centre_parent_shares_axis_fk",
			run: func() error {
				id, _ := uuid.NewV7()
				return raw(`INSERT INTO cost_centre
				              (id, company_id, cost_category_id, name, parent_id, created_by)
				            VALUES ($1, $2, $3, 'Andheri', $4, 'raw')`, id, companyID, branch, sales)
			},
		},
		{
			what:       "a centre that is its own parent",
			constraint: "cost_centre_not_its_own_parent",
			run: func() error {
				id, _ := uuid.NewV7()
				return raw(`INSERT INTO cost_centre
				              (id, company_id, cost_category_id, name, parent_id, created_by)
				            VALUES ($1, $2, $3, 'Thane', $1, 'raw')`, id, companyID, branch)
			},
		},
		{
			what:       "a centre with no axis",
			constraint: "null value in column \"cost_category_id\"",
			run: func() error {
				id, _ := uuid.NewV7()
				return raw(`INSERT INTO cost_centre
				              (id, company_id, cost_category_id, name, created_by)
				            VALUES ($1, $2, NULL, 'Nowhere', 'raw')`, id, companyID)
			},
		},
	} {
		err := leg.run()
		if err == nil {
			t.Errorf("the database ACCEPTED %s: %s is gone from migration 0040, and "+
				"cost_centre.go still writes its refusal as though the constraint were the "+
				"authority", leg.what, leg.constraint)
			continue
		}
		if !strings.Contains(err.Error(), leg.constraint) {
			t.Errorf("%s was refused, but not by %s — so this leg is proving something else: %v",
				leg.what, leg.constraint, err)
		}
	}
}
