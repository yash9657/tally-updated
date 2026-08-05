package masters

// THE PURE HALF OF THE COST MASTERS. NO DATABASE, ON PURPOSE.
//
// Every rule that decides whether an axis or a centre can be stored is a function
// of its input, and this file exercises those functions directly. That matters
// beyond convenience: the DB-backed suite next door (cost_centre_db_test.go) SKIPS
// when Postgres is unreachable, and a rule whose only test is skippable is a rule
// that is unverified on any machine where the database is down.
//
// It is an IN-PACKAGE test (package masters, not masters_test) because
// validateCostCentre, validateCostCategoryName, ancestorLoop and
// decorateCostCentres are unexported and must stay so: they are enforcement
// wiring behind the four Service methods, not API. The precedent is
// tds_challan_test.go in this package, which makes the same call for
// validateTDSChallan.
//
// ===========================================================================
// ⛔ THE CONTROL THIS FILE EXISTS FOR
// ===========================================================================
// TestTheSameCentreNameOnADifferentAxisIsAccepted is not a nice-to-have. Every
// refusal below could be passing because the validator refuses everything, and the
// specific wrong "fix" this file guards against is widening the centre-name
// uniqueness check from the AXIS to the COMPANY — which reads more careful, passes
// every refusal test, and breaks a real book on the day a company has a Mumbai
// branch and a Mumbai project. Widening the scope makes THAT test fail and nothing
// else. It is the mutation proof recorded in the report.

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ---------------------------------------------------------------------------
// Fixtures. Two axes, because one axis cannot express the model: the whole
// point of a category is that several of them allocate the same money in
// parallel, so a single-axis fixture cannot test the uniqueness SCOPE at all.
// ---------------------------------------------------------------------------

var (
	branchAxis     = uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001")
	departmentAxis = uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000002")
)

func id(n byte) uuid.UUID {
	var u uuid.UUID
	u[0] = 0xbb
	u[15] = n
	return u
}

// branchCentres is a two-level Branch axis: Mumbai, with Andheri under it, and
// Delhi alongside.
func branchCentres() []existingCostCentre {
	mumbai := existingCostCentre{
		ID: id(1), Name: "Mumbai", NameKey: "mumbai",
		CategoryID: branchAxis, CategoryName: "Branch",
	}
	andheri := existingCostCentre{
		ID: id(2), Name: "Andheri", NameKey: "andheri", ParentID: mumbai.ID,
		CategoryID: branchAxis, CategoryName: "Branch",
	}
	delhi := existingCostCentre{
		ID: id(3), Name: "Delhi", NameKey: "delhi",
		CategoryID: branchAxis, CategoryName: "Branch",
	}
	return []existingCostCentre{mumbai, andheri, delhi}
}

// newBranchCentre is a create on the Branch axis: no id yet, no parent.
func newBranchCentre(name string) costCentreDraft {
	return costCentreDraft{
		ID:           uuid.Nil,
		CategoryID:   branchAxis,
		CategoryName: "Branch",
		Name:         name,
	}
}

// under parents the draft under one of the existing rows.
func under(d costCentreDraft, parent existingCostCentre) costCentreDraft {
	p := parent
	d.ParentID = p.ID
	d.Parent = &p
	return d
}

// ---------------------------------------------------------------------------
// THE CONTROLS. Without these every refusal below proves nothing.
// ---------------------------------------------------------------------------

// TestAValidAxisAndCentreAreAccepted is the base control, and it also pins the
// normalisation: the name STORED is trimmed but keeps the case an operator typed.
// Folding the stored value to lower case would put "r&d" on every report.
func TestAValidAxisAndCentreAreAccepted(t *testing.T) {
	name, err := validateCostCategoryName("  Branch ", nil, uuid.Nil)
	if err != nil {
		t.Fatalf("a valid cost category was refused: %v", err)
	}
	if name != "Branch" {
		t.Errorf("stored category name is %q, want %q — surrounding space must be stripped "+
			"BEFORE the uniqueness check, or \" Branch\" and \"Branch\" are two axes", name, "Branch")
	}

	centre, err := validateCostCentre(newBranchCentre("\tPune  "), branchCentres())
	if err != nil {
		t.Fatalf("a valid cost centre was refused: %v", err)
	}
	if centre != "Pune" {
		t.Errorf("stored centre name is %q, want %q", centre, "Pune")
	}
}

// TestACentreUnderASameAxisParentIsAccepted is the CONTROL for the cross-axis and
// cycle refusals below. Without it, "the parent was refused" could mean "every
// parent is refused" and the hierarchy would be dead while the tests were green.
func TestACentreUnderASameAxisParentIsAccepted(t *testing.T) {
	axis := branchCentres()
	mumbai := axis[0]
	if _, err := validateCostCentre(under(newBranchCentre("Bandra"), mumbai), axis); err != nil {
		t.Fatalf("a centre under a parent ON ITS OWN AXIS was refused: %v — the hierarchy is "+
			"unusable and every parent refusal below is proving nothing", err)
	}
	// And two levels down, so the ancestor walk is exercised on a legal chain.
	andheri := axis[1]
	if _, err := validateCostCentre(under(newBranchCentre("Versova"), andheri), axis); err != nil {
		t.Fatalf("a centre two levels down was refused: %v", err)
	}
}

// ---------------------------------------------------------------------------
// ⛔⛔ THE UNIQUENESS SCOPE. THE MUTATION TARGET.
// ---------------------------------------------------------------------------

// TestTheSameCentreNameOnADifferentAxisIsAccepted is the control that proves the
// uniqueness scope is the AXIS and not the COMPANY.
//
// "Mumbai" is already a Branch. Creating a Project called "Mumbai" MUST be
// accepted: migration 0040's index is on
// (company_id, cost_category_id, lower(btrim(name))), and in India a business's
// branches and its projects share place names as a matter of course. A
// company-wide check here would refuse this and the operator would be told a name
// exists that their screen does not show.
//
// ⚠️ THE `axis` ARGUMENT IS THE PROJECT AXIS'S CENTRES — which is empty. That is
// the whole point and it is why this test cannot be written by passing
// branchCentres(): the store loads the centres of the axis being written to, so a
// correct implementation never even sees the Branch rows. A test that passed them
// in would be testing a store this package does not have.
func TestTheSameCentreNameOnADifferentAxisIsAccepted(t *testing.T) {
	projectAxis := uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000003")
	draft := costCentreDraft{
		CategoryID:   projectAxis,
		CategoryName: "Project",
		Name:         "Mumbai",
	}
	// Every centre on the PROJECT axis — there are none yet.
	if _, err := validateCostCentre(draft, nil); err != nil {
		t.Fatalf("a Project called Mumbai was refused while a Branch called Mumbai exists: %v\n"+
			"A centre name is unique WITHIN ITS AXIS only (migration 0040's "+
			"cost_centre_name_unique is on (company_id, cost_category_id, lower(btrim(name)))). "+
			"Refusing this makes the Project axis unbuildable for any company whose branches and "+
			"projects share place names, and the operator is told a name is taken that their "+
			"screen does not show", err)
	}

	// The other half of the same statement, from the store's point of view: the
	// axis load is scoped, so an implementation that loaded the whole company
	// would ALSO have to widen the check to break. Assert the check is looking at
	// the axis it was handed, by handing it a same-name row on ANOTHER axis and
	// requiring it to be ignored.
	//
	// This is the shape a company-wide "fix" takes, so it is refused explicitly.
	foreign := []existingCostCentre{{
		ID: id(9), Name: "Mumbai", NameKey: "mumbai",
		CategoryID: departmentAxis, CategoryName: "Department",
	}}
	if _, err := validateCostCentre(draft, foreign); err != nil {
		t.Errorf("a centre on ANOTHER axis blocked this name: %v — the check is comparing names "+
			"without regard to which axis the row it found sits on", err)
	}
}

// TestADuplicateCentreNameOnTheSameAxisIsRefused is the other side. It is
// case- and whitespace-insensitive because the index is: "mumbai" and " Mumbai "
// are the same branch to anyone reading a report, and two of them split one
// place's cost in two with each half looking whole.
func TestADuplicateCentreNameOnTheSameAxisIsRefused(t *testing.T) {
	for _, spelling := range []string{"Mumbai", "mumbai", "MUMBAI", "  Mumbai  ", "\tmumbai\n"} {
		_, err := validateCostCentre(newBranchCentre(spelling), branchCentres())
		if !errors.Is(err, ErrCostNameTaken) {
			t.Errorf("a second Branch called %q was accepted (err=%v); migration 0040's index "+
				"folds case and surrounding space, so the database would have refused it with a "+
				"constraint name", spelling, err)
			continue
		}
		// The message has to say the scope, or the operator concludes the product
		// is broken when they cannot create a Project of the same name either.
		if !strings.Contains(err.Error(), "DIFFERENT axis") && !strings.Contains(err.Error(), "another axis") {
			t.Errorf("the refusal for %q does not tell the operator the name is free on another "+
				"axis, which is the one thing they need to know: %v", spelling, err)
		}
	}
}

// TestACategoryNameIsUniqueAcrossTheWholeCompany is the MIRROR of the rule above,
// and the pair is the point: the two scopes are deliberately different. Two axes
// called "Branch" would split one allocation set in half and both halves would sum
// to the posting on their own, so nothing would ever look wrong.
func TestACategoryNameIsUniqueAcrossTheWholeCompany(t *testing.T) {
	existing := []existingCostCategory{
		{ID: branchAxis, Name: "Branch", NameKey: "branch"},
		{ID: departmentAxis, Name: "Department", NameKey: "department"},
	}
	for _, spelling := range []string{"Branch", "branch", " BRANCH ", "\tBranch"} {
		_, err := validateCostCategoryName(spelling, existing, uuid.Nil)
		if !errors.Is(err, ErrCostNameTaken) {
			t.Errorf("a second cost category called %q was accepted (err=%v)", spelling, err)
		}
	}
	// CONTROL: a different axis name is accepted, so the refusal above is not
	// simply "every category is refused".
	if _, err := validateCostCategoryName("Project", existing, uuid.Nil); err != nil {
		t.Fatalf("a new axis name was refused: %v", err)
	}
}

// TestARenameToItsOwnNameIsNotAClashWithItself. Restating a row's own name —
// which is what a whole-row PUT does when the operator changed only the case, or
// changed nothing — must not collide with the row being renamed. Without selfID
// every rename of the only axis would be refused.
func TestARenameToItsOwnNameIsNotAClashWithItself(t *testing.T) {
	existing := []existingCostCategory{{ID: branchAxis, Name: "Branch", NameKey: "branch"}}
	if _, err := validateCostCategoryName("BRANCH", existing, branchAxis); err != nil {
		t.Errorf("renaming Branch to BRANCH was refused as a duplicate of itself: %v", err)
	}
	// And a centre renaming itself.
	axis := branchCentres()
	d := newBranchCentre("MUMBAI")
	d.ID = axis[0].ID
	if _, err := validateCostCentre(d, axis); err != nil {
		t.Errorf("renaming Mumbai to MUMBAI was refused as a duplicate of itself: %v", err)
	}
	// CONTROL: renaming Mumbai to Delhi, which is a DIFFERENT existing row on the
	// same axis, is still refused. Without this leg the selfID skip could be
	// skipping every row.
	d.Name = "Delhi"
	if _, err := validateCostCentre(d, axis); !errors.Is(err, ErrCostNameTaken) {
		t.Errorf("renaming Mumbai to Delhi (an existing sibling) was accepted: %v", err)
	}
}

// ---------------------------------------------------------------------------
// BLANK NAMES
// ---------------------------------------------------------------------------

// TestABlankNameIsRefused, in every spelling that reaches a text column as
// something migration 0040's not-blank CHECKs (btrim of the name, compared with
// the empty string) would reject.
func TestABlankNameIsRefused(t *testing.T) {
	for _, blank := range []string{"", " ", "   ", "\t", "\n", " \t\n "} {
		if _, err := validateCostCategoryName(blank, nil, uuid.Nil); !errors.Is(err, ErrInvalidCostMaster) {
			t.Errorf("cost category name %q was accepted (err=%v); "+
				"cost_category_name_not_blank would have refused it as a constraint name", blank, err)
		}
		if _, err := validateCostCentre(newBranchCentre(blank), nil); !errors.Is(err, ErrInvalidCostMaster) {
			t.Errorf("cost centre name %q was accepted (err=%v); "+
				"cost_centre_name_not_blank would have refused it as a constraint name", blank, err)
		}
	}
}

// TestACentreWithNoAxisIsRefused. cost_centre.cost_category_id is NOT NULL, and
// the reason is the whole model: the sum-to-posting invariant groups by
// (posting, category), so a centre with no category is a centre no allocation
// could ever be summed against.
//
// It is refused BEFORE the name is even looked at, so the operator is told the
// thing they can actually fix.
func TestACentreWithNoAxisIsRefused(t *testing.T) {
	d := newBranchCentre("Pune")
	d.CategoryID = uuid.Nil
	d.CategoryName = ""
	err := validateCostCentreErr(t, d, nil)
	if !errors.Is(err, ErrInvalidCostMaster) {
		t.Fatalf("a cost centre with no cost category was accepted: %v", err)
	}
	if !strings.Contains(err.Error(), "cost category") {
		t.Errorf("the refusal does not name the field the operator must supply: %v", err)
	}
}

// ---------------------------------------------------------------------------
// THE PARENT RULES
// ---------------------------------------------------------------------------

// TestACentreCannotBeItsOwnParent. The database catches exactly this one
// (cost_centre_not_its_own_parent) and nothing longer, so this leg proves the Go
// message rather than the rule.
func TestACentreCannotBeItsOwnParent(t *testing.T) {
	axis := branchCentres()
	mumbai := axis[0]
	d := newBranchCentre("Mumbai")
	d.ID = mumbai.ID
	d = under(d, mumbai) // parent == self
	err := validateCostCentreErr(t, d, axis)
	if !errors.Is(err, ErrInvalidCostMaster) {
		t.Fatalf("a centre parented under itself was accepted: %v", err)
	}
	if !strings.Contains(err.Error(), "under itself") {
		t.Errorf("the refusal does not say what is wrong in words: %v", err)
	}
}

// TestAMultiHopParentCycleIsRefused is the test that must never be deleted.
//
// ⛔ NOTHING IN THE DATABASE CATCHES THIS. Andheri is under Mumbai; putting Mumbai
// under Andheri satisfies every constraint in migration 0040 — the composite
// parent FK holds at both edges, both rows are on one axis, no name repeats — and
// the row is stored happily. It surfaces later in a report that rolls a child's
// cost up into its parent: the walk never terminates. So this Go check is the ONLY
// thing between an operator and a Cost Category Summary that hangs.
func TestAMultiHopParentCycleIsRefused(t *testing.T) {
	axis := branchCentres()
	mumbai, andheri := axis[0], axis[1]

	// TWO HOPS: Mumbai under Andheri, when Andheri is already under Mumbai.
	d := newBranchCentre("Mumbai")
	d.ID = mumbai.ID
	d = under(d, andheri)
	_, err := validateCostCentre(d, axis)
	if err == nil {
		t.Fatal("Mumbai was ACCEPTED under Andheri while Andheri sits under Mumbai. " +
			"Mumbai -> Andheri -> Mumbai satisfies EVERY constraint in migration 0040 " +
			"(cost_centre_not_its_own_parent only catches a one-hop self-reference), so this row " +
			"is now stored, and the Cost Category Summary rolls a child's cost into its parent by " +
			"walking exactly this chain. The report will not print a wrong number — it will stop " +
			"responding, and the only way out is to fix the row in the database")
	}
	if !errors.Is(err, ErrInvalidCostMaster) {
		t.Fatalf("a two-hop parent cycle was refused for the WRONG reason: %v", err)
	}
	if !strings.Contains(err.Error(), "Mumbai under Andheri under Mumbai") {
		t.Errorf("the refusal does not show the operator the loop it found: %v", err)
	}

	// THREE HOPS, so the walk is proved to be a walk and not a one-level look at
	// the parent's parent.
	versova := existingCostCentre{
		ID: id(4), Name: "Versova", NameKey: "versova", ParentID: andheri.ID,
		CategoryID: branchAxis, CategoryName: "Branch",
	}
	deep := append(branchCentres(), versova)
	d3 := newBranchCentre("Mumbai")
	d3.ID = mumbai.ID
	d3 = under(d3, versova)
	_, err = validateCostCentre(d3, deep)
	if err == nil {
		t.Fatal("Mumbai was ACCEPTED under Versova, three hops below itself: the check is " +
			"looking only at the parent's own parent instead of walking the chain, so any cycle " +
			"longer than two hops still reaches the database and still hangs the report")
	}
	if !errors.Is(err, ErrInvalidCostMaster) {
		t.Fatalf("a three-hop parent cycle was refused for the wrong reason: %v", err)
	}
	if !strings.Contains(err.Error(), "Mumbai under Versova under Andheri under Mumbai") {
		t.Errorf("the three-hop loop is not rendered in full: %v", err)
	}

	// CONTROL: Delhi has no relationship to Mumbai, so putting Mumbai under Delhi
	// is legal and must be ACCEPTED. Without this leg the cycle check could be
	// refusing every re-parent and both assertions above would still pass.
	ok := newBranchCentre("Mumbai")
	ok.ID = mumbai.ID
	ok = under(ok, axis[2]) // Delhi
	if _, err := validateCostCentre(ok, axis); err != nil {
		t.Fatalf("re-parenting Mumbai under Delhi (no cycle) was refused: %v — the cycle check "+
			"is refusing every re-parent", err)
	}
}

// TestACrossAxisParentIsRefused. The composite FK
// (company_id, parent_id, cost_category_id) -> cost_centre enforces this, so the
// Go check exists for the SENTENCE — and the sentence has to explain the
// consequence, because "a Branch cannot go under a Department" is not obviously
// true to somebody who has just drawn an org chart that way.
func TestACrossAxisParentIsRefused(t *testing.T) {
	sales := existingCostCentre{
		ID: id(7), Name: "Sales", NameKey: "sales",
		CategoryID: departmentAxis, CategoryName: "Department",
	}
	d := under(newBranchCentre("Mumbai City"), sales)
	err := validateCostCentreErr(t, d, branchCentres())
	if !errors.Is(err, ErrInvalidCostMaster) {
		t.Fatalf("a Branch centre parented under a Department centre was accepted: %v", err)
	}
	for _, want := range []string{"Branch", "Department", "twice"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the cross-axis refusal does not mention %q, so it does not explain why one "+
				"axis may not roll up into another: %v", want, err)
		}
	}
}

// TestAParentThatDoesNotResolveIsRefusedRatherThanIgnored. A parent id that names
// nothing this company can see (a typo, or another tenant's centre made invisible
// by RLS) must be a refusal. Creating the centre at the TOP of the axis instead
// would put it in the wrong place on the tree and misreport every parent above it,
// silently — and the operator asked for a parent.
func TestAParentThatDoesNotResolveIsRefusedRatherThanIgnored(t *testing.T) {
	d := newBranchCentre("Pune")
	d.ParentID = id(200) // no row, so Parent stays nil
	err := validateCostCentreErr(t, d, branchCentres())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("a centre naming a parent that does not resolve was accepted: %v", err)
	}
}

// TestNoParentIsLegal — the top of an axis. A centre with no parent is the
// ordinary case and must not be dragged into any of the parent rules.
func TestNoParentIsLegal(t *testing.T) {
	if _, err := validateCostCentre(newBranchCentre("Chennai"), branchCentres()); err != nil {
		t.Fatalf("a centre at the top of its axis was refused: %v", err)
	}
}

// ---------------------------------------------------------------------------
// THE DERIVED HIERARCHY (the read path)
// ---------------------------------------------------------------------------

// TestTheListDerivesDepthAndPath. Depth and Path are computed from the list, never
// stored: migration 0040 keeps the hierarchy as a self-reference so a re-parent is
// one UPDATE, and a stored path is a column that disagrees with its parents after
// the first one.
func TestTheListDerivesDepthAndPath(t *testing.T) {
	rows := []CostCentreRecord{
		{ID: id(1), Name: "Mumbai", CostCategoryID: branchAxis, CostCategoryName: "Branch"},
		{ID: id(2), Name: "Andheri", ParentID: id(1), CostCategoryID: branchAxis, CostCategoryName: "Branch"},
		{ID: id(4), Name: "Versova", ParentID: id(2), CostCategoryID: branchAxis, CostCategoryName: "Branch"},
	}
	got := decorateCostCentres(rows)
	want := []struct {
		depth  int
		path   string
		parent string
	}{
		{0, "Mumbai", ""},
		{1, "Mumbai / Andheri", "Mumbai"},
		{2, "Mumbai / Andheri / Versova", "Andheri"},
	}
	for i, w := range want {
		if got[i].Depth != w.depth || got[i].Path != w.path || got[i].ParentName != w.parent {
			t.Errorf("row %d (%s): depth=%d path=%q parent=%q, want depth=%d path=%q parent=%q",
				i, got[i].Name, got[i].Depth, got[i].Path, got[i].ParentName, w.depth, w.path, w.parent)
		}
		if got[i].AncestryTruncated {
			t.Errorf("row %d (%s) is flagged as a cycle and is not one", i, got[i].Name)
		}
	}
}

// TestTheListDoesNotHangOnAStoredCycle. Go refuses to CREATE a cycle, but raw SQL
// can put one in — and the list is the screen an operator would go to in order to
// find and fix it. An unbounded ancestor walk would hang exactly there. The walk
// is bounded and says it stopped.
//
// If this test ever times out, the bound has been removed.
func TestTheListDoesNotHangOnAStoredCycle(t *testing.T) {
	rows := []CostCentreRecord{
		{ID: id(1), Name: "Mumbai", ParentID: id(2), CostCategoryID: branchAxis},
		{ID: id(2), Name: "Andheri", ParentID: id(1), CostCategoryID: branchAxis},
	}
	got := decorateCostCentres(rows)
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2", len(got))
	}
	for _, r := range got {
		if !r.AncestryTruncated {
			t.Errorf("%s sits in a stored parent cycle and is not flagged; a caller walking "+
				"Path would have no way to know the tree is not a tree", r.Name)
		}
	}
}

// TestAncestorLoopToleratesAnAncestorItCannotSee. An ancestor that is not on the
// axis cannot exist (the composite FK forbids it), so a chain that leaves the
// loaded set is the end of what we can see and NOT a loop. Refusing there would
// refuse a legal parent on the strength of a row we failed to load.
func TestAncestorLoopToleratesAnAncestorItCannotSee(t *testing.T) {
	orphanParent := existingCostCentre{
		ID: id(5), Name: "Kalyan", NameKey: "kalyan", ParentID: id(99), // id(99) not in the set
		CategoryID: branchAxis, CategoryName: "Branch",
	}
	if loop := ancestorLoop(id(1), orphanParent, branchCentres()); loop != nil {
		t.Errorf("a chain that leaves the loaded set was reported as a cycle: %v", loop)
	}
}

// TestAncestorLoopIsBoundedOnAlreadyCyclicData. The write path calls this, so an
// unbounded walk over a cycle raw SQL created would hang the VALIDATION — turning
// the guard against a hanging report into a hanging write.
func TestAncestorLoopIsBoundedOnAlreadyCyclicData(t *testing.T) {
	a := existingCostCentre{ID: id(1), Name: "A", NameKey: "a", ParentID: id(2), CategoryID: branchAxis}
	b := existingCostCentre{ID: id(2), Name: "B", NameKey: "b", ParentID: id(1), CategoryID: branchAxis}
	// id(3) is not part of the loop, so an honest walk from A never reaches it —
	// it can only stop by running out of hops, which is what must happen.
	if loop := ancestorLoop(id(3), a, []existingCostCentre{a, b}); loop == nil {
		t.Error("a walk over already-cyclic stored data returned no loop and therefore " +
			"terminated by exhausting the ancestor map, not by the bound — remove the bound and " +
			"this becomes an infinite loop inside a write")
	}
}

// ---------------------------------------------------------------------------
// ANTI-DRIFT AGAINST THE MIGRATION
// ---------------------------------------------------------------------------

// TestCostNameKeyMatchesTheIndexExpressions reads migration 0040 off disk.
//
// This is the one pair with no other guard. costNameKey is lower(btrim(name)) in
// Go; if the index stops being that expression, a Go check that ACCEPTS is
// followed by a database that refuses (the operator meets a constraint name), and
// a Go check that REFUSES turns away a name the database would have taken. Either
// way the division of labour this file's header describes has quietly stopped
// holding.
func TestCostNameKeyMatchesTheIndexExpressions(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/0040_cost_centres.sql")
	if err != nil {
		t.Fatalf("read migration 0040: %v", err)
	}
	sql := string(raw)

	// The CATEGORY index: company-wide.
	if !strings.Contains(sql, "ON cost_category (company_id, lower(btrim(name)))") {
		t.Error("migration 0040's cost_category_name_unique is no longer " +
			"(company_id, lower(btrim(name))) — validateCostCategoryName normalises to " +
			"lower(TrimSpace(name)) and company-wide scope on the strength of that index")
	}
	// The CENTRE index: WITHIN THE AXIS. The cost_category_id in the middle is the
	// whole model, and its removal would be the change that breaks
	// TestTheSameCentreNameOnADifferentAxisIsAccepted for real.
	if !strings.Contains(sql, "ON cost_centre (company_id, cost_category_id, lower(btrim(name)))") {
		t.Error("migration 0040's cost_centre_name_unique is no longer " +
			"(company_id, cost_category_id, lower(btrim(name))) — validateCostCentre scopes the " +
			"name check to ONE AXIS on the strength of that index, so a change here changes " +
			"which names an operator may use")
	}
	// The constraints whose sentences this file writes. If one is dropped, the Go
	// refusal is a rule the database no longer holds and the two sides have to be
	// reconciled deliberately rather than discovered.
	for _, want := range []string{
		"cost_category_name_not_blank",
		"cost_centre_name_not_blank",
		"cost_centre_not_its_own_parent",
		"cost_centre_parent_shares_axis_fk",
		"cost_centre_axis_scope",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("constraint %s is gone from migration 0040, and cost_centre.go still writes "+
				"its refusal as though the database were the authority", want)
		}
	}
	// cost_category_id NOT NULL is the model, not a detail: a centre with no axis
	// cannot be grouped by the per-category invariant.
	if !strings.Contains(sql, "cost_category_id  UUID NOT NULL") {
		t.Error("cost_centre.cost_category_id is no longer NOT NULL in migration 0040 — an " +
			"uncategorised centre would be storable and nothing could ever be reported about it")
	}
}

// TestTheAllocationInvariantIsPerCategoryAndThisPackageNeverTotalsAcrossAxes is a
// statement about what this package deliberately does NOT do.
//
// A list of centres spanning axes is not a total of anything: axes describe the
// SAME money in parallel, so summing across them double-counts (migration 0040's
// header, and the trigger it installs, group by (posting, category) for exactly
// this reason). ListCostCentres therefore returns masters and no amounts at all,
// which is what makes a wrong total unreachable from here rather than merely
// discouraged.
func TestTheAllocationInvariantIsPerCategoryAndThisPackageNeverTotalsAcrossAxes(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/0040_cost_centres.sql")
	if err != nil {
		t.Fatalf("read migration 0040: %v", err)
	}
	// The trigger groups on the category. If this ever becomes posting-wide, the
	// model has changed and every comment in cost_centre.go is wrong.
	if !strings.Contains(string(raw), "AND a.cost_category_id = NEW.cost_category_id") {
		t.Error("migration 0040's sum-to-posting trigger no longer groups by cost_category_id: " +
			"the invariant has become posting-wide, which would refuse the parallel allocation " +
			"the whole feature exists for")
	}
	// And this package must not grow a MONEY field on a record that spans axes.
	//
	// ⛔ THIS ASSERTION USED TO BE UNFALSIFIABLE, and the way it failed is worth
	// keeping: it asked whether CostCentreRecord satisfied
	// `interface{ Total() any }`. Nothing in Go would ever declare a method with
	// that signature, so the branch could not be entered — the test passed
	// unconditionally while reading as a guard. Adding `Total decimal.Decimal` to
	// CostCentreRecord left it green, which is exactly the change it claimed to
	// forbid.
	//
	// It now walks the FIELDS by reflection, which is what the claim needs: a
	// masters record spans every axis, so any money on it is a figure summed
	// across axes, and a figure summed across axes is a double count waiting to be
	// printed. Cost money belongs on the reporting types, which are scoped to one
	// axis by construction.
	rt := reflect.TypeOf(CostCentreRecord{})
	moneyType := reflect.TypeOf(decimal.Decimal{})
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if f.Type == moneyType {
			t.Errorf("CostCentreRecord has grown the money field %q (%s). A masters record spans "+
				"EVERY axis, so an amount on it is summed across axes — and cost figures from "+
				"different axes describe the same money, so that total is a multiple of the cost "+
				"actually borne. Put it on a reporting type scoped to one axis instead",
				f.Name, f.Type)
		}
	}
}

// validateCostCentreErr runs the validator and fails the test if it ACCEPTED,
// so each refusal test can assert on the message without a nil dereference.
func validateCostCentreErr(t *testing.T, d costCentreDraft, axis []existingCostCentre) error {
	t.Helper()
	name, err := validateCostCentre(d, axis)
	if err == nil {
		t.Fatalf("this cost centre was ACCEPTED and would have been stored as %q. Whatever rule "+
			"this test names is no longer enforced, and the row it lets through misreports every "+
			"parent above it on the axis", name)
	}
	return err
}
