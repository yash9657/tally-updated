package server

// handler_budget.go — the budget master over HTTP (T11, migration 0085).
//
//	GET    .../budgets                            -> Display / budget
//	POST   .../budgets                            -> Create  / budget
//	PUT    .../budgets/{budgetID}                 -> Alter   / budget
//	DELETE .../budgets/{budgetID}                 -> Delete  / budget
//	GET    .../budgets/{budgetID}/lines           -> Display / budget
//	POST   .../budgets/{budgetID}/lines           -> Create  / budget
//	PUT    .../budgets/{budgetID}/lines/{lineID}  -> Alter   / budget
//	DELETE .../budgets/{budgetID}/lines/{lineID}  -> Delete  / budget
//
// ===========================================================================
// ONE BARE TOKEN FOR ALL EIGHT, AND IT IS NOT A REPORT NAME
// ===========================================================================
// routeAction maps every route here to the BARE `budget` resource. That is what
// makes the seeded Data Entry level refused all eight with NO ACL MIGRATION AT
// ALL: its default_access is 'none' and every rule it carries names a specific
// token, so a token never mentioned stays denied. Had this been
// `report:budget`, migration 0011's coarse `allow display report` would have
// GRANTED it to every seeded clerk in every tenant on the day it shipped, and
// taking it back would have needed a counterweight migration.
//
// ⛔ THE VARIANCE REPORT IS THE OTHER CASE. `report:budget-variance` IS a report
// name, it DID arrive pre-granted, and migration 0086 is the refusal. It lives in
// handler_budget_variance.go and belongs to the reporting lane. Two files because
// they are two authorities.
//
// ⚠️ THE ROUTE IS `/budgets` PLURAL AND THE REPORT IS `/budget-variance`, and the
// two are one character from colliding. routeAction's arm is
// `strings.HasPrefix(rest, "/budgets")`; "/budget-variance" does not carry that
// prefix, so the master arm never claims the report path. A master route at
// "/budget" WOULD have swallowed it and served the report under the bare token,
// which no `report:` rule could ever match — a silent permit.
// TestReportVocabularyMatchesRouter is what holds this.
//
// ===========================================================================
// MONEY GOES OUT AS FIXED-4 STRINGS
// ===========================================================================
// A JSON number is a float64 and a budgeted figure is not a thing to send through
// one. Every amount on the wire here is decimal.StringFixed(4), which is what
// every other money-bearing handler in this package does.

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/masters"
)

// ---------------------------------------------------------------------------
// Requests
// ---------------------------------------------------------------------------

// budgetReq is the body of POST .../budgets and PUT .../budgets/{budgetID}.
type budgetReq struct {
	Name     string `json:"name"`
	FromDate string `json:"from_date"`
	ToDate   string `json:"to_date"`
	// CreatedBy is the actor. Named CreatedBy on both the create and the alter for
	// consistency with every other master handler here; masters records it as
	// altered_by on an ALTER.
	CreatedBy string `json:"created_by"`
}

// budgetLineReq is the body of POST .../budgets/{budgetID}/lines and of
// PUT .../budgets/{budgetID}/lines/{lineID}.
type budgetLineReq struct {
	Dimension string `json:"dimension"`
	TargetID  string `json:"target_id"`
	Basis     string `json:"basis"`
	// DrCr may be omitted for a GROUP or LEDGER line, where masters takes the
	// target's natural side from its nature. ⛔ IT IS REQUIRED FOR A COST CENTRE,
	// which has no nature to derive from.
	DrCr string `json:"dr_cr"`
	// Amount is a STRING on the wire, never a JSON number. A budgeted figure that
	// arrived as a float64 would be a figure that had already lost precision
	// before any validation could see it.
	Amount    string `json:"amount"`
	Note      string `json:"note"`
	CreatedBy string `json:"created_by"`
}

// ---------------------------------------------------------------------------
// Views
// ---------------------------------------------------------------------------

type budgetView struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	FromDate string `json:"from_date"`
	ToDate   string `json:"to_date"`
	// LineCount lets a screen show that a plan is EMPTY without a second request.
	// An empty budget is a live trap: the variance report over it is a page of
	// unbudgeted actuals, which reads as "nothing was planned" and is
	// indistinguishable from "the plan was never keyed".
	LineCount int    `json:"line_count"`
	CreatedBy string `json:"created_by"`
	CreatedAt string `json:"created_at"`
}

func toBudgetView(b masters.BudgetRecord) budgetView {
	return budgetView{
		ID: b.ID.String(), Name: b.Name,
		FromDate: b.FromDate, ToDate: b.ToDate,
		LineCount: b.LineCount, CreatedBy: b.CreatedBy, CreatedAt: b.CreatedAt,
	}
}

type budgetLineView struct {
	ID        string `json:"id"`
	BudgetID  string `json:"budget_id"`
	Dimension string `json:"dimension"`
	TargetID  string `json:"target_id"`
	// TargetName travels with every row: a list of bare ids is unreadable, and for
	// a COST_CENTRE the name alone is AMBIGUOUS — "Mumbai" may legitimately be both
	// a Branch and a Project — which is why the axis comes too.
	TargetName       string `json:"target_name"`
	CostCategoryID   string `json:"cost_category_id"`
	CostCategoryName string `json:"cost_category_name"`
	// Nature is the target's ASSET / LIABILITY / INCOME / EXPENSE, "" for a cost
	// centre. On the wire so a client can tell whether exceeding this budget is the
	// good direction without re-deriving it — see the variance report on why this
	// layer refuses to say "favourable".
	Nature string `json:"nature"`
	Basis  string `json:"basis"`
	DrCr   string `json:"dr_cr"`
	Amount string `json:"amount"`
	Note   string `json:"note"`
	// BasisMeans is the basis spelled out in words, ON EVERY LINE. ⛔ NOT
	// DECORATION: CLOSING_BALANCE and NETT_TRANSACTIONS answer different questions
	// and the same figure means different things under each, so a screen that
	// rendered only the token would let an operator set a position ceiling thinking
	// they had set a spend cap.
	BasisMeans string `json:"basis_means"`
	CreatedAt  string `json:"created_at"`
}

func toBudgetLineView(l masters.BudgetLineRecord) budgetLineView {
	v := budgetLineView{
		ID: l.ID.String(), BudgetID: l.BudgetID.String(),
		Dimension: l.Dimension, TargetID: l.TargetID.String(),
		TargetName: l.TargetName, CostCategoryName: l.CostCategoryName,
		Nature: l.Nature, Basis: l.Basis, DrCr: l.DrCr,
		Amount: l.Amount.StringFixed(4), Note: l.Note,
		BasisMeans: budgetBasisMeans(l.Basis), CreatedAt: l.CreatedAt,
	}
	// uuid.Nil must not reach a client as "00000000-…", which a naive caller would
	// send back as a real id.
	if l.CostCategoryID != uuid.Nil {
		v.CostCategoryID = l.CostCategoryID.String()
	}
	return v
}

func budgetBasisMeans(basis string) string {
	switch basis {
	case masters.BasisClosingBalance:
		return "a POSITION as at the budget period's end date, with the opening balance " +
			"folded in"
	case masters.BasisNettTransactions:
		return "MOVEMENT inside the budget period, with the opening balance excluded"
	}
	return ""
}

// ---------------------------------------------------------------------------
// THE HEAD
// ---------------------------------------------------------------------------

// handleListBudgets serves GET .../budgets.
func (s *Server) handleListBudgets(w http.ResponseWriter, r *http.Request) {
	rows, err := s.masters.ListBudgets(r.Context())
	if err != nil {
		writeBudgetError(w, err)
		return
	}
	views := make([]budgetView, 0, len(rows))
	for _, row := range rows {
		views = append(views, toBudgetView(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"budgets": views})
}

// handleCreateBudget serves POST .../budgets.
func (s *Server) handleCreateBudget(w http.ResponseWriter, r *http.Request) {
	var req budgetReq
	if !decodeJSON(w, r, &req) {
		return
	}
	id, err := s.masters.CreateBudget(r.Context(), masters.BudgetInput{
		Name: req.Name, FromDate: req.FromDate, ToDate: req.ToDate,
	}, req.CreatedBy)
	if err != nil {
		writeBudgetError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": id.String(), "name": req.Name,
		"from_date": req.FromDate, "to_date": req.ToDate,
	})
}

// handleAlterBudget serves PUT .../budgets/{budgetID}.
//
// THE NAME AND THE PERIOD MOVE TOGETHER, because a period change is not
// cosmetic: it changes which movement every NETT_TRANSACTIONS line is measured
// over and which date every CLOSING_BALANCE line is measured as at — i.e. it
// changes every actual on the variance report without touching one budgeted
// figure. One call means one audited act, with the old period in before_doc.
func (s *Server) handleAlterBudget(w http.ResponseWriter, r *http.Request) {
	budgetID, err := uuid.Parse(chi.URLParam(r, "budgetID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "budget id must be a uuid")
		return
	}
	var req budgetReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.masters.AlterBudget(r.Context(), budgetID, masters.BudgetInput{
		Name: req.Name, FromDate: req.FromDate, ToDate: req.ToDate,
	}, req.CreatedBy); err != nil {
		writeBudgetError(w, err)
		return
	}
	// Read back, so the client sees the name as STORED (trimmed) and the line count
	// it is about to render.
	out := map[string]any{"id": budgetID.String(), "budget": nil}
	rows, rerr := s.masters.ListBudgets(r.Context())
	if rerr != nil {
		// ⚠️ A FAILED READ-BACK IS NOT A FAILED ALTER. The change is committed by
		// the time we are here, so a 4xx would tell the operator their correction
		// did not happen when it did — and the next thing they would do is make it
		// again.
		out["read_back_error"] = rerr.Error()
	}
	for _, row := range rows {
		if row.ID == budgetID {
			v := toBudgetView(row)
			out["budget"] = v
			break
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleDeleteBudget serves DELETE .../budgets/{budgetID}.
//
// ⚠️ IT REPORTS HOW MANY LINES THE CASCADE TOOK. 0085's budget_line_budget_fk is
// ON DELETE CASCADE, so deleting a plan silently takes every figure on it — and
// "I deleted a budget" and "I deleted 40 budgeted figures" are different acts to
// the operator as well as to whoever reads master_audit later.
func (s *Server) handleDeleteBudget(w http.ResponseWriter, r *http.Request) {
	budgetID, err := uuid.Parse(chi.URLParam(r, "budgetID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "budget id must be a uuid")
		return
	}
	// Counted BEFORE the delete: afterwards the rows are gone and the number is
	// unknowable.
	lines := 0
	if rows, lerr := s.masters.ListBudgets(r.Context()); lerr == nil {
		for _, row := range rows {
			if row.ID == budgetID {
				lines = row.LineCount
				break
			}
		}
	}
	actor := r.URL.Query().Get("deleted_by")
	if actor == "" {
		// A DELETE carries no body by convention, so the actor rides in the query
		// string. Refused rather than defaulted: masters will refuse it anyway, and
		// a plan deleted by "system" is a plan nobody can be asked about.
		writeError(w, http.StatusBadRequest,
			"deleted_by is required (?deleted_by=…) — a plan deleted by nobody in "+
				"particular is a plan nobody can be asked about")
		return
	}
	if err := s.masters.DeleteBudget(r.Context(), budgetID, actor); err != nil {
		writeBudgetError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": budgetID.String(), "deleted": true,
		"lines_deleted_by_cascade": lines,
	})
}

// ---------------------------------------------------------------------------
// THE LINES
// ---------------------------------------------------------------------------

// handleListBudgetLines serves GET .../budgets/{budgetID}/lines.
func (s *Server) handleListBudgetLines(w http.ResponseWriter, r *http.Request) {
	budgetID, err := uuid.Parse(chi.URLParam(r, "budgetID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "budget id must be a uuid")
		return
	}
	rows, err := s.masters.ListBudgetLines(r.Context(), budgetID)
	if err != nil {
		writeBudgetError(w, err)
		return
	}
	views := make([]budgetLineView, 0, len(rows))
	for _, row := range rows {
		views = append(views, toBudgetLineView(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"budget_id": budgetID.String(),
		"lines":     views,
	})
}

// handleAddBudgetLine serves POST .../budgets/{budgetID}/lines.
func (s *Server) handleAddBudgetLine(w http.ResponseWriter, r *http.Request) {
	budgetID, err := uuid.Parse(chi.URLParam(r, "budgetID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "budget id must be a uuid")
		return
	}
	var req budgetLineReq
	if !decodeJSON(w, r, &req) {
		return
	}
	in, ok := budgetLineInputFrom(w, req, true)
	if !ok {
		return
	}
	id, err := s.masters.AddBudgetLine(r.Context(), budgetID, in, req.CreatedBy)
	if err != nil {
		writeBudgetError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": id.String(), "budget_id": budgetID.String(),
	})
}

// handleAlterBudgetLine serves PUT .../budgets/{budgetID}/lines/{lineID}.
//
// ⛔ THE DIMENSION AND THE TARGET ARE IMMUTABLE and masters refuses a change to
// either BY NAME. The database would accept it and silently re-file a budgeted
// figure from one row of the variance report to another, leaving the original
// target reading as unbudgeted and the new one as having been planned all along.
func (s *Server) handleAlterBudgetLine(w http.ResponseWriter, r *http.Request) {
	lineID, err := uuid.Parse(chi.URLParam(r, "lineID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "budget line id must be a uuid")
		return
	}
	var req budgetLineReq
	if !decodeJSON(w, r, &req) {
		return
	}
	// target_id is OPTIONAL on an alter: masters uses the stored one and refuses a
	// different one. So this parse must not demand it.
	in, ok := budgetLineInputFrom(w, req, false)
	if !ok {
		return
	}
	if err := s.masters.AlterBudgetLine(r.Context(), lineID, in, req.CreatedBy); err != nil {
		writeBudgetError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": lineID.String(), "altered": true})
}

// handleDeleteBudgetLine serves DELETE .../budgets/{budgetID}/lines/{lineID}.
func (s *Server) handleDeleteBudgetLine(w http.ResponseWriter, r *http.Request) {
	lineID, err := uuid.Parse(chi.URLParam(r, "lineID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "budget line id must be a uuid")
		return
	}
	actor := r.URL.Query().Get("deleted_by")
	if actor == "" {
		writeError(w, http.StatusBadRequest,
			"deleted_by is required (?deleted_by=…)")
		return
	}
	if err := s.masters.DeleteBudgetLine(r.Context(), lineID, actor); err != nil {
		writeBudgetError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": lineID.String(), "deleted": true})
}

// budgetLineInputFrom parses the wire body into a masters input.
//
// requireTarget is false on an ALTER, where the stored target is authoritative
// and masters refuses a different one — so demanding it here would make a
// legitimate "just change the figure" call impossible.
func budgetLineInputFrom(w http.ResponseWriter, req budgetLineReq, requireTarget bool) (masters.BudgetLineInput, bool) {
	var in masters.BudgetLineInput
	in.Dimension = req.Dimension
	in.Basis = req.Basis
	in.DrCr = req.DrCr
	in.Note = req.Note

	if req.TargetID != "" {
		id, err := uuid.Parse(req.TargetID)
		if err != nil {
			writeError(w, http.StatusBadRequest,
				"target_id must be a uuid: it is the group, ledger or cost centre being "+
					"budgeted, and it cannot be guessed")
			return in, false
		}
		in.TargetID = id
	} else if requireTarget {
		writeError(w, http.StatusBadRequest,
			"target_id is required — the thing being budgeted. A line with no target "+
				"could not be compared against anything")
		return in, false
	}

	// ⛔ THE AMOUNT IS PARSED FROM A STRING, and a bad one is a 400 rather than a
	// silent zero. decimal.NewFromString on "" yields an error, which is what makes
	// an omitted amount a refusal instead of a budget of nothing.
	amt, err := decimal.NewFromString(req.Amount)
	if err != nil {
		writeError(w, http.StatusBadRequest,
			"amount must be a decimal string such as \"1000000.0000\" — a budgeted figure "+
				"sent as a JSON number is a figure that has already lost precision")
		return in, false
	}
	in.Amount = amt
	return in, true
}

// writeBudgetError maps the masters sentinels onto status codes.
//
// THE THREE FAMILIES ARE DIFFERENT THINGS TO AN OPERATOR and the sentence is
// surfaced verbatim, because every refusal masters raises names the CONSEQUENCE
// (which report goes wrong, and how) rather than a constraint.
func writeBudgetError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, masters.ErrInvalidBudget):
		// The request is malformed on its own facts: a blank name, a reversed
		// period, an unknown basis, a cost centre on a closing balance.
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, masters.ErrBudgetNameTaken):
		// Nothing is malformed; the books already say otherwise. 409, the same
		// family a duplicate master gets elsewhere in this package.
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, masters.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
