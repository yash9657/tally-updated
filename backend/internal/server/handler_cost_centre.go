package server

// HTTP surface for the COST MASTERS — T2, migration 0040.
//
//	GET  .../cost-categories                     -> Display / cost-centre
//	POST .../cost-categories                     -> Create  / cost-centre
//	PUT  .../cost-categories/{categoryID}        -> Alter   / cost-centre
//	GET  .../cost-centres[?cost_category_id=]    -> Display / cost-centre
//	POST .../cost-centres                        -> Create  / cost-centre
//	PUT  .../cost-centres/{centreID}             -> Alter   / cost-centre
//
// A CATEGORY IS AN AXIS OF ANALYSIS (Department, Branch, Project); A CENTRE IS A
// POSITION ON THAT AXIS. Several axes exist in order to allocate the SAME money in
// parallel — one salary posting split across Departments AND again, in full,
// across Branches — which is why nothing on this surface ever totals across them.
// masters/cost_centre.go's header has the model; migration 0040's has the schema
// reasoning. Both are worth reading before changing a field here.
//
// Until these routes existed an axis could only be created by raw SQL, so cost
// allocation was present in the schema and unusable in the product: the voucher
// path can allocate to a centre and there was no way to bring a centre into
// existence.
//
// ---------------------------------------------------------------------------
// ONE RESOURCE TOKEN FOR ALL SIX ROUTES, AND IT IS A BARE TOKEN
// ---------------------------------------------------------------------------
// `cost-centre`, for the read and the write, of both the axes and the centres.
//
// ONE TOKEN because these are authorities over the SAME small object — the shape
// of this company's cost analysis. The axes and the centres are not separable in
// practice: a centre's cost_category_id is NOT NULL, so whoever may create a
// centre must be able to see the axes, and an axis with no centres allocates
// nothing. That is the masters arms' reasoning, not bank-reconciliation's — there
// the read (the whole bank position) and the write (ticking one cheque) are
// authorities over DIFFERENT objects, so one token would force them to be granted
// together.
//
// ⛔ A BARE TOKEN AND DELIBERATELY NOT `report:cost-centre`. A report name is
// matched by migration 0011's coarse `allow display report` at specificity 1, so
// the moment this became a report name every seeded Data Entry level would be
// GRANTED it by a rule written years ago for another purpose, and taking it back
// would need a counterweight migration — which is exactly why 0068 exists for
// report:tds-register. As a bare token the seeded Data Entry level
// (default_access 'none', every rule it carries naming a specific token) is
// refused all six routes with NO ACL MIGRATION AT ALL, which is how T6 shipped
// tds-challan and tds-profile.
//
// STATE THE CONSEQUENCE PLAINLY: DATA ENTRY CANNOT SEE OR CREATE COST MASTERS
// until an administrator writes an explicit rule. That is the right default —
// creating an axis decides how every future allocation is grouped, and a wrong
// axis is not visible on any voucher — and an owner who wants a clerk maintaining
// centres writes `allow create cost-centre` through the admin API. A tenant's
// decision, not one this software makes for them.
//
// ⛔ AND `cost-centre` MUST NOT BE ADDED TO authz.KnownReportNames. server.go
// loops over that list and AUTO-MOUNTS GET /print/reports/<name>; a name with no
// matching reportName() case is a route routeAction cannot map, i.e. default-DENY
// for every level including the Owner. That bug already shipped once for
// reconciliation-queue. The two COST REPORTS (report:cost-centre-summary and
// report:cost-category-summary) are report names and belong to the reporting lane;
// they are not this file's tokens.
//
// ---------------------------------------------------------------------------
// THE WRITES CARRY NO MONEY, AND THEY ARE STILL WRITES
// ---------------------------------------------------------------------------
// Every POST/PUT here is a Create or an Alter, so each one inherits the back-dated
// cutoff and whatever step-up the level requires of a write. None of them carries a
// date, so the cutoff is inert on them by construction — voucherDateFromRequest
// finds no date and cutoffDeny skips. That is stated rather than left implied,
// because a comment asserting a control that never runs is worse than a missing
// control: renaming an axis is not a back-dated act and there is nothing here for
// the cutoff to judge.

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/khata/cored/internal/masters"
)

// ---------------------------------------------------------------------------
// The wire shapes
// ---------------------------------------------------------------------------

// costCategoryReq is the body of POST .../cost-categories and of
// PUT .../cost-categories/{categoryID}.
type costCategoryReq struct {
	// Name is the AXIS name — "Department", "Branch", "Project". Unique across the
	// company, compared ignoring case and surrounding spaces (the same expression
	// migration 0040's index uses).
	Name string `json:"name"`
	// AllocateNonRevenue is Tally's "Allocate Non-Revenue Items": may this axis be
	// used on a balance-sheet ledger, or only on a P&L one? OPTIONAL and defaults
	// FALSE. It is not required of a client because departmental analysis of income
	// and expense is the ordinary case, and a default that permits MORE than the
	// operator intended is the wrong way for a default to be wrong.
	//
	// ⚠️ IT IS SET ON CREATE AND IS NOT ALTERABLE by the PUT below, which is a
	// STATED GAP rather than an oversight: correcting it changes which ledgers an
	// existing axis may be used on, and this slice has not decided what that means
	// for allocations already made under the old answer. The PUT is a RENAME.
	AllocateNonRevenue bool `json:"allocate_non_revenue"`
	// CreatedBy / AlteredBy is the audit trail. One field, because the same body
	// serves both verbs and a master nobody can be asked about is a master nobody
	// can correct.
	CreatedBy string `json:"created_by"`
}

// costCentreReq is the body of POST .../cost-centres and of
// PUT .../cost-centres/{centreID}.
//
// ⛔⛔ THE PUT IS A WHOLE-ROW RESTATEMENT AND NOT A PATCH, exactly as
// PUT .../tds-profile is, and for a related reason: the name and the parent are
// checked AGAINST EACH OTHER and against the axis (the within-axis uniqueness
// scope, the same-axis parent rule, the cycle), so a partial body is not
// checkable.
//
// THE CONSEQUENCE, SPELT OUT BECAUSE IT WILL SURPRISE SOMEBODY: A PUT THAT OMITS
// parent_id MOVES THE CENTRE TO THE TOP OF ITS AXIS. It does not leave the parent
// alone. A client that means "just rename it" must send the parent it already has.
// The response echoes the row as it now stands so the move is visible immediately
// rather than discovered on a report.
type costCentreReq struct {
	// CostCategoryID is REQUIRED on the POST. There is no uncategorised centre and
	// no implicit default axis: a centre with no axis cannot be grouped, and the
	// sum-to-posting invariant (which is per (posting, category), never per
	// posting) would have nothing to group by. A company that wants one axis
	// creates one category, exactly as Tally ships "Primary Cost Category".
	//
	// On the PUT it is OPTIONAL and, if sent, must MATCH the axis the centre is
	// already on. The axis is immutable and an attempt to change it is refused with
	// a sentence: moving a centre between axes would re-file every allocation ever
	// made to it from one report to another without a voucher being touched.
	CostCategoryID string `json:"cost_category_id"`
	Name           string `json:"name"`
	// ParentID is empty for a centre at the top of its axis. A parent must sit on
	// the SAME axis — a Branch centre cannot sit under a Department centre.
	ParentID  string `json:"parent_id"`
	CreatedBy string `json:"created_by"`
}

// costCategoryView is one axis on the wire.
type costCategoryView struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	AllocateNonRevenue bool   `json:"allocate_non_revenue"`
	// CentreCount is DERIVED per axis. It is served because an axis with no centres
	// allocates nothing, and that is the commonest reason a voucher screen offers an
	// axis an operator cannot use.
	CentreCount int    `json:"centre_count"`
	CreatedBy   string `json:"created_by"`
	CreatedAt   string `json:"created_at"`
}

func toCostCategoryView(c masters.CostCategoryRecord) costCategoryView {
	return costCategoryView{
		ID:                 c.ID.String(),
		Name:               c.Name,
		AllocateNonRevenue: c.AllocateNonRevenue,
		CentreCount:        c.CentreCount,
		CreatedBy:          c.CreatedBy,
		CreatedAt:          c.CreatedAt,
	}
}

// costCentreView is one centre on the wire, with the axis it sits on NAMED.
//
// The axis name travels with every row on purpose: a list of centre names spanning
// axes is ambiguous — "Mumbai" may legitimately be both a Branch and a Project —
// and a screen that renders the bare name invites an operator to pick the wrong
// one on a voucher.
type costCentreView struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	CostCategoryID   string `json:"cost_category_id"`
	CostCategoryName string `json:"cost_category_name"`
	// ParentID is "" at the top of the axis, and ParentName is then "" too.
	ParentID   string `json:"parent_id"`
	ParentName string `json:"parent_name"`
	// Depth and Path are DERIVED from the list, never stored — migration 0040 keeps
	// the hierarchy as a self-reference so a re-parent is one UPDATE, and a stored
	// path is a column that disagrees with its parents after the first one. Path is
	// "Mumbai / Andheri", so a flat list renders as a tree without the client
	// walking parent pointers.
	Depth int    `json:"depth"`
	Path  string `json:"path"`
	// AncestryTruncated should never be true. It says the STORED rows contain a
	// parent cycle, which Go refuses to create and which no constraint in migration
	// 0040 catches beyond a one-hop self-reference. A client must not walk Path as a
	// tree when this is set.
	AncestryTruncated bool   `json:"ancestry_truncated"`
	CreatedBy         string `json:"created_by"`
	CreatedAt         string `json:"created_at"`
}

func toCostCentreView(c masters.CostCentreRecord) costCentreView {
	v := costCentreView{
		ID:                c.ID.String(),
		Name:              c.Name,
		CostCategoryID:    c.CostCategoryID.String(),
		CostCategoryName:  c.CostCategoryName,
		ParentName:        c.ParentName,
		Depth:             c.Depth,
		Path:              c.Path,
		AncestryTruncated: c.AncestryTruncated,
		CreatedBy:         c.CreatedBy,
		CreatedAt:         c.CreatedAt,
	}
	// uuid.Nil is "no parent" in masters; it must not reach a client as
	// "00000000-0000-0000-0000-000000000000", which a naive caller would send back
	// as a parent id.
	if c.ParentID != uuid.Nil {
		v.ParentID = c.ParentID.String()
	}
	return v
}

// ---------------------------------------------------------------------------
// COST CATEGORIES — the axes
// ---------------------------------------------------------------------------

// handleListCostCategories serves GET .../cost-categories.
func (s *Server) handleListCostCategories(w http.ResponseWriter, r *http.Request) {
	rows, err := s.masters.ListCostCategories(r.Context())
	if err != nil {
		writeCostMasterError(w, err)
		return
	}
	views := make([]costCategoryView, 0, len(rows))
	for _, row := range rows {
		views = append(views, toCostCategoryView(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"cost_categories": views})
}

// handleCreateCostCategory serves POST .../cost-categories.
func (s *Server) handleCreateCostCategory(w http.ResponseWriter, r *http.Request) {
	var req costCategoryReq
	if !decodeJSON(w, r, &req) {
		return
	}
	id, err := s.masters.CreateCostCategory(r.Context(), masters.CostCategoryInput{
		Name:               req.Name,
		AllocateNonRevenue: req.AllocateNonRevenue,
	}, attributedTo(r, req.CreatedBy))
	if err != nil {
		writeCostMasterError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":                   id.String(),
		"name":                 req.Name,
		"allocate_non_revenue": req.AllocateNonRevenue,
	})
}

// handleRenameCostCategory serves PUT .../cost-categories/{categoryID}.
//
// A RENAME AND NOT A REPLACE: the id is what every centre and every stored
// allocation points at, so an axis is corrected in place. Deleting and recreating
// would orphan them, and posting_cost_allocation's composite FK would refuse the
// delete anyway once one voucher had used the axis.
func (s *Server) handleRenameCostCategory(w http.ResponseWriter, r *http.Request) {
	categoryID, err := uuid.Parse(chi.URLParam(r, "categoryID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "cost category id must be a uuid")
		return
	}
	var req costCategoryReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.masters.RenameCostCategory(r.Context(), categoryID, req.Name, req.CreatedBy); err != nil {
		writeCostMasterError(w, err)
		return
	}
	// Read back, so the client sees the name as STORED (trimmed) rather than the
	// string it sent, and sees the centre count it is about to render.
	rows, rerr := s.masters.ListCostCategories(r.Context())
	out := map[string]any{"id": categoryID.String(), "cost_category": nil}
	if rerr != nil {
		// ⚠️ A FAILED READ-BACK IS NOT A FAILED RENAME. The rename is committed by
		// the time we are here, so a 4xx would tell the operator their correction
		// did not happen when it did — and the next thing they would do is make it
		// again.
		out["read_back_error"] = rerr.Error()
	}
	for _, row := range rows {
		if row.ID == categoryID {
			out["cost_category"] = toCostCategoryView(row)
			break
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------------------------------------------------------------------------
// COST CENTRES — the positions on an axis
// ---------------------------------------------------------------------------

// handleListCostCentres serves GET .../cost-centres, optionally narrowed to one
// axis with ?cost_category_id=.
//
// ⚠️ AN UNFILTERED LIST SPANS AXES AND IS NOT A TOTAL OF ANYTHING. Axes describe
// the SAME money in parallel, so anything summed across this list double-counts.
// The response carries no amounts at all, which is what makes the wrong total
// unreachable rather than merely discouraged; a report has to filter to one axis
// or group by axis.
//
// AN UNKNOWN cost_category_id IS A 404 AND NOT AN EMPTY LIST. "This axis has no
// centres yet" and "there is no such axis" are different answers and only one of
// them is worth acting on — an empty list for a typo'd or another tenant's id
// reads exactly like a company whose masters have been wiped.
func (s *Server) handleListCostCentres(w http.ResponseWriter, r *http.Request) {
	var filter uuid.UUID
	if raw := r.URL.Query().Get("cost_category_id"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest,
				"cost_category_id must be a uuid — it is the AXIS to narrow to (get the ids from "+
					"GET .../cost-categories); omit it for every axis")
			return
		}
		// The all-zeros uuid parses, and uuid.Nil is this handler's sentinel for
		// "every axis" — so without this guard `?cost_category_id=00000000-...`
		// silently returns every centre on every axis with a 200, skipping the
		// known-axis check below entirely. A caller asking for one axis and being
		// handed all of them is the double-count this feature spends its whole
		// design budget avoiding. Same reasoning as the parent_id guard in
		// decodeCostCentreInput.
		if parsed == uuid.Nil {
			writeError(w, http.StatusBadRequest,
				"cost_category_id is all zeros, which is not an axis: OMIT the parameter for every "+
					"axis, or name one (get the ids from GET .../cost-categories) — the all-zeros uuid "+
					"would silently return every axis, and cost figures from different axes must never "+
					"be added together")
			return
		}
		filter = parsed
	}

	// The axes are served alongside the centres because a centre list is not
	// readable without them: the create form needs the axis picker, and a screen
	// grouping centres by axis needs the order and the names.
	//
	// ⚠️ THIS IS TWO TRANSACTIONS, NOT ONE SNAPSHOT. An earlier version of this
	// comment claimed it avoided "a second round trip that can observe a different
	// state"; it does not — ListCostCategories and ListCostCentres each open their
	// own RunInTx, so an axis created between them yields a response whose centres
	// reference a cost_category_id absent from its own categories array. It saves a
	// round TRIP, not a race. A client must tolerate that rather than assume the
	// two arrays are consistent; making them one transaction is the real fix and is
	// not done here.
	cats, err := s.masters.ListCostCategories(r.Context())
	if err != nil {
		writeCostMasterError(w, err)
		return
	}
	if filter != uuid.Nil {
		known := false
		for _, c := range cats {
			if c.ID == filter {
				known = true
				break
			}
		}
		if !known {
			writeError(w, http.StatusNotFound,
				"there is no cost category with that id for this company, so there are no cost "+
					"centres to list on it. This is not the same answer as \"the axis is empty\", "+
					"which is why it is a refusal rather than a list of none")
			return
		}
	}

	rows, err := s.masters.ListCostCentres(r.Context(), filter)
	if err != nil {
		writeCostMasterError(w, err)
		return
	}
	views := make([]costCentreView, 0, len(rows))
	for _, row := range rows {
		views = append(views, toCostCentreView(row))
	}
	catViews := make([]costCategoryView, 0, len(cats))
	for _, c := range cats {
		catViews = append(catViews, toCostCategoryView(c))
	}
	body := map[string]any{
		"cost_centres":    views,
		"cost_categories": catViews,
	}
	if filter != uuid.Nil {
		body["cost_category_id"] = filter.String()
	}
	writeJSON(w, http.StatusOK, body)
}

// handleCreateCostCentre serves POST .../cost-centres.
func (s *Server) handleCreateCostCentre(w http.ResponseWriter, r *http.Request) {
	var req costCentreReq
	if !decodeJSON(w, r, &req) {
		return
	}
	in, ok := decodeCostCentreInput(w, req, true)
	if !ok {
		return
	}
	id, err := s.masters.CreateCostCentre(r.Context(), in, req.CreatedBy)
	if err != nil {
		writeCostMasterError(w, err)
		return
	}
	writeCostCentreReadBack(w, s, r, http.StatusCreated, id, in.CostCategoryID)
}

// handleAlterCostCentre serves PUT .../cost-centres/{centreID} — the rename AND
// the re-parent, together.
//
// ONE ROUTE FOR BOTH because the two are checked against each other: the
// within-axis uniqueness scope and the same-axis parent rule are statements about
// the PAIR, and a caller able to change one without the other could walk the row
// through a state neither check would have allowed. See costCentreReq on what
// omitting parent_id means.
func (s *Server) handleAlterCostCentre(w http.ResponseWriter, r *http.Request) {
	centreID, err := uuid.Parse(chi.URLParam(r, "centreID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "cost centre id must be a uuid")
		return
	}
	var req costCentreReq
	if !decodeJSON(w, r, &req) {
		return
	}
	// The axis is OPTIONAL here: a whole-row restatement may repeat it, and the
	// store refuses one that DISAGREES with the axis the centre is on.
	in, ok := decodeCostCentreInput(w, req, false)
	if !ok {
		return
	}
	if err := s.masters.AlterCostCentre(r.Context(), centreID, in, req.CreatedBy); err != nil {
		writeCostMasterError(w, err)
		return
	}
	writeCostCentreReadBack(w, s, r, http.StatusOK, centreID, uuid.Nil)
}

// decodeCostCentreInput parses the two uuids off the wire. requireAxis is true on
// the create, where a centre with no axis is not a thing that can exist.
func decodeCostCentreInput(w http.ResponseWriter, req costCentreReq, requireAxis bool) (masters.CostCentreInput, bool) {
	var in masters.CostCentreInput
	in.Name = req.Name

	if req.CostCategoryID == "" {
		if requireAxis {
			writeError(w, http.StatusBadRequest,
				"cost_category_id is required: a cost centre is created ON an axis and there is no "+
					"uncategorised centre. A centre with no axis cannot be grouped, so nothing "+
					"could ever be reported about it — pick the cost category it belongs to, or "+
					"create one first")
			return in, false
		}
	} else {
		axis, err := uuid.Parse(req.CostCategoryID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "cost_category_id must be a uuid")
			return in, false
		}
		in.CostCategoryID = axis
	}

	// An EMPTY parent_id means "at the top of the axis". It is NOT an error and it
	// is NOT ignored on an alteration — see costCentreReq.
	if req.ParentID != "" {
		parent, err := uuid.Parse(req.ParentID)
		if err != nil {
			writeError(w, http.StatusBadRequest,
				"parent_id must be a uuid (the cost centre this one sits under), or empty for a "+
					"centre at the top of its axis")
			return in, false
		}
		// ⛔ THE ALL-ZEROS UUID PARSES SUCCESSFULLY, AND uuid.Nil IS THIS LAYER'S
		// SENTINEL FOR "no parent". Without this guard a request that explicitly
		// names "00000000-0000-0000-0000-000000000000" as the parent is read as
		// "at the top of the axis": the domain layer never issues a parent lookup,
		// so nothing refuses it, and a POST silently creates a top-level centre
		// while a PUT silently LIFTS an existing child out of its hierarchy —
		// changing which totals it rolls into, with a 200 and a read-back that
		// looks correct because it truthfully reports the state that was written.
		//
		// The zero uuid is not a plausible typo; it is what a client sends when it
		// has a "none" option wired to a zero value, or when it round-trips a
		// parent_id it read as "" through a uuid formatter. Both are ordinary bugs
		// and both must be refused rather than reinterpreted.
		if parent == uuid.Nil {
			writeError(w, http.StatusBadRequest,
				"parent_id is all zeros, which is not a cost centre: send the field EMPTY to put this "+
					"centre at the top of its axis, or name the centre it sits under — the all-zeros "+
					"uuid would silently move it to the top and change which totals it rolls into")
			return in, false
		}
		in.ParentID = parent
	}
	return in, true
}

// writeCostCentreReadBack answers with the row as it now stands.
//
// IT READS BACK BECAUSE THREE OF THE FIELDS CANNOT BE COMPUTED BY THE CLIENT: the
// stored (trimmed) name, and the derived Depth/Path — and on an alteration, the
// parent that omitting parent_id has just cleared. An operator who meant to rename
// and accidentally lifted a centre to the top of its axis should see it here, not
// on a report a month later.
//
// ⚠️ A FAILED READ-BACK IS NOT A FAILED WRITE. The row is committed by the time we
// are here, so answering 4xx/422 would tell the operator nothing happened when it
// did — and the next thing they would do is create it again, which the uniqueness
// index would refuse in a way that reads like a bug.
func writeCostCentreReadBack(w http.ResponseWriter, s *Server, r *http.Request,
	status int, centreID, axis uuid.UUID) {
	out := map[string]any{"id": centreID.String(), "cost_centre": nil}
	rows, err := s.masters.ListCostCentres(r.Context(), axis)
	if err != nil {
		out["read_back_error"] = err.Error()
		writeJSON(w, status, out)
		return
	}
	for _, row := range rows {
		if row.ID == centreID {
			out["cost_centre"] = toCostCentreView(row)
			break
		}
	}
	writeJSON(w, status, out)
}

// writeCostMasterError maps the store's two sentinels onto two HTTP families.
//
// 400 — THE REQUEST IS WRONG AND THE OPERATOR CAN FIX IT BY CHANGING IT:
// ErrInvalidCostMaster covers a blank name, a centre with no axis, a self-parent,
// a parent on another axis, a parent cycle, and an attempt to move a centre
// between axes. Every one of those is a field the operator can change.
//
// 409 — THE REQUEST IS WELL FORMED AND THE BOOKS ALREADY SAY OTHERWISE:
// ErrCostNameTaken. Nothing in the body is malformed, retrying is not the answer,
// and the store's sentence names the row that already holds the name AND the scope
// it is held in — because "Mumbai already exists" on a screen showing no Mumbai is
// how an operator concludes the product is broken. The name may well be free on
// another axis, and the message says so.
//
// Everything else falls through to writeMastersError, so this route agrees with
// every sibling masters route on what a non-domain failure means: masters.ErrNotFound
// is a 404 (a named axis or parent that is not on record — which is also the answer
// for another tenant's id, because RLS makes it invisible and saying more would be
// the leak) and db.ErrNoCompany a 400.
func writeCostMasterError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, masters.ErrInvalidCostMaster):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, masters.ErrCostNameTaken):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeMastersError(w, err)
	}
}
