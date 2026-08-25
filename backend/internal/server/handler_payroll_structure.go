package server

// handler_payroll_structure.go — the two effective-dated children of the
// employee master, and the closed vocabularies a form needs to key them.
//
// ===========================================================================
// ⛔ WHY THIS FILE EXISTS: THE PAYROLL MODULE COULD NOT PAY ANYBODY OVER HTTP
// ===========================================================================
// Track 3 mounted eight payroll routes, mapped every one of them in the PDP,
// and tested them — and an employee created through POST /employees could never
// be paid, because a pay run reads two things that had NO ROUTE AT ALL:
//
//	payrolldb.SetSalaryComponent   -> employee_salary_structure
//	payrolldb.SetStatutoryElection -> employee_statutory_election
//
// Both were reachable only from Go. computeOne refuses without them, by name:
//
//	"no statutory election is in force for wage month %s; the PF contribution
//	 base and the tax regime are elections and there is no safe default"
//	"no salary structure is in force on %s; there is nothing to pay and
//	 nothing to contribute on"
//
// so the whole module answered 422 to every caller that had not reached into
// the service directly. EVERY payroll test in this package sets the structure
// and the election up by calling s.payroll.SetSalaryComponent — which is why a
// green suite never noticed: the tests bypassed the exact seam that was missing.
// That is the same shape as the defects HANDOFF §7.2 catalogues, and it is worth
// naming as a class: A ROUTE TABLE IS NOT A SURFACE. Eight mounted routes that
// cannot be composed into one completed task are eight routes nobody can use.
//
// ===========================================================================
// THE AUTHORITY: `employee`, AND DELIBERATELY NOT A NEW TOKEN
// ===========================================================================
// All five routes below resolve to the resource `employee` — Create for the
// writes, Display for the reads — which is the same authority POST /employees
// and GET /employees already carry. Two consequences, both wanted:
//
//   - Migration 0035 already seeds `disallow create|alter|display employee` for
//     the seeded Data Entry level in every tenant, so these routes are refused
//     there from the moment they exist. A NEW token would have been granted by
//     nothing and denied by nothing, and would have needed its own migration to
//     be safe — a token an administrator has never heard of, protecting salaries.
//   - Whoever may create an employee may set that employee's pay. That is one
//     decision rather than two, and it is the honest one: an operator who can
//     create the master can already create a second employee at any salary they
//     like, so splitting the authority would protect nothing while reading as if
//     it did.
//
// ⚠️ THE WRITES ARE Creates, SO THEY INHERIT THE BACK-DATED CUTOFF AND THE
// WebAuthn STEP-UP, exactly as POST /employees does. That is right and it is not
// incidental: a salary component is effective-DATED, and back-dating one into a
// closed period silently re-prices every pay run that period contains.

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/payroll"
	"github.com/khata/cored/internal/payroll/payrolldb"
)

// ---------------------------------------------------------------------------
// The salary structure.
// ---------------------------------------------------------------------------

// salaryComponentReq is the POST body for one effective-dated salary component.
//
// Amount is a STRING and is parsed with decimal.NewFromString, for the reason
// every money field on the payroll surface is: a JSON number arrives as a
// float64 and re-prices somebody's salary in the last paisa — and this one is
// the figure every other payroll number is derived from.
type salaryComponentReq struct {
	ID            string `json:"id"`
	EffectiveFrom string `json:"effective_from"`
	EffectiveTo   string `json:"effective_to"`
	Code          string `json:"code"`
	Type          string `json:"type"`
	Amount        string `json:"amount"`
}

// handleSetSalaryComponent inserts one effective-dated salary component.
//
// POST /v1/companies/{companyID}/employees/{employeeID}/salary-components
//
// The employee id is in the PATH and never in the body. It is not the
// operator's to type — it comes from the master they are looking at — and a body
// that could disagree with the URL is a question about which one wins.
func (s *Server) handleSetSalaryComponent(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	employeeID, ok := employeeIDParam(w, r)
	if !ok {
		return
	}
	var req salaryComponentReq
	if !decodeJSON(w, r, &req) {
		return
	}

	var c payrolldb.SalaryComponent
	var err error
	if c.ID, err = optionalUUID(req.ID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid salary component id")
		return
	}
	c.EmployeeID = employeeID
	c.EffectiveFrom = strings.TrimSpace(req.EffectiveFrom)
	c.EffectiveTo = strings.TrimSpace(req.EffectiveTo)
	if c.EffectiveFrom == "" {
		// Never defaulted to today. A salary component is effective-dated and the
		// pay run reads the window in force on the FIRST of the wage month, so a
		// date this handler invented would decide which months a figure applies
		// to — silently, and differently from what the operator meant.
		writeError(w, http.StatusBadRequest,
			"effective_from is required and must be YYYY-MM-DD: a salary component is effective-dated, and the pay run reads the window in force on the first day of the wage month")
		return
	}
	c.Code = strings.TrimSpace(req.Code)
	if c.Code == "" {
		writeError(w, http.StatusBadRequest, "code is required (the component's own name, e.g. \"Basic\")")
		return
	}
	// The TYPE is resolved against the closed vocabulary HERE as well as in
	// payrolldb, so the refusal names the vocabulary rather than arriving as a
	// CHECK-constraint violation. See payroll.ComponentType: a type that fell
	// through to "excluded" would under-contribute PF and ESI for the whole of
	// that employee's service, with no error anywhere.
	ct, err := payroll.ComponentTypeFor(strings.TrimSpace(req.Type))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"%s — the type decides whether PF and ESI are contributed on this component (Code on Social Security s.2(88)), so it is a closed set; GET ../payroll-vocabulary lists it",
			err.Error()))
		return
	}
	c.Type = ct
	amount := strings.TrimSpace(req.Amount)
	if amount == "" {
		writeError(w, http.StatusBadRequest, "amount is required and must be a decimal string (never a JSON number)")
		return
	}
	if c.Amount, err = decimal.NewFromString(amount); err != nil {
		writeError(w, http.StatusBadRequest, "amount is not a decimal")
		return
	}

	id, err := s.payroll.SetSalaryComponent(ctx, c)
	if err != nil {
		writeStructureError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"salary_component_id": id.String()})
}

// handleListSalaryComponents enumerates one employee's salary structure.
//
// GET /v1/companies/{companyID}/employees/{employeeID}/salary-components
func (s *Server) handleListSalaryComponents(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	employeeID, ok := employeeIDParam(w, r)
	if !ok {
		return
	}
	list, err := s.payroll.ListSalaryComponents(ctx, employeeID)
	if err != nil {
		writeStructureError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, c := range list {
		out = append(out, map[string]any{
			"id":             c.ID.String(),
			"effective_from": c.EffectiveFrom,
			"effective_to":   c.EffectiveTo,
			"code":           c.Code,
			"type":           string(c.Type),
			// StringFixed(4) — the storage scale — and never a JSON number.
			"amount": c.Amount.StringFixed(4),
			// The classification travels WITH the row rather than being looked up
			// by a client against a list it fetched at some other time. Whether PF
			// and ESI are contributed on a component is the single most consequential
			// fact about it, and a client joining two responses to work it out is a
			// join that can be stale.
			"is_wages":        payroll.IsWagesComponent(c.Type),
			"in_deeming_test": payroll.InDeemingTest(c.Type),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"salary_components": out})
}

// ---------------------------------------------------------------------------
// The statutory election.
// ---------------------------------------------------------------------------

// statutoryElectionReq is the POST body for one effective-dated election.
//
// ⛔ EPSMember, ESIApplicable AND EmployerMatchesVoluntary ARE PLAIN BOOLS AND
// THE ZERO VALUE IS THE HONEST ONE HERE, unlike employeeReq.IsActive. Each of
// these is a POSITIVE assertion about one person — "this employee is a member of
// the pension scheme", "this employee is covered by ESI" — and there is no
// answer that is safe to assume: assuming membership contributes on somebody who
// is not a member, and assuming coverage withholds from somebody who is not
// covered. An omitted field is therefore FALSE, which is "not asserted", and the
// screen asks for all three rather than letting a default decide.
type statutoryElectionReq struct {
	ID                     string `json:"id"`
	EffectiveFrom          string `json:"effective_from"`
	EffectiveTo            string `json:"effective_to"`
	PFBase                 string `json:"pf_base"`
	PFVoluntaryBase        string `json:"pf_voluntary_base"`
	EmployerMatchVoluntary bool   `json:"employer_matches_voluntary"`
	EPSMember              bool   `json:"eps_member"`
	ESIApplicable          bool   `json:"esi_applicable"`
	Regime                 string `json:"regime"`
}

// handleSetStatutoryElection inserts one effective-dated statutory election.
//
// POST /v1/companies/{companyID}/employees/{employeeID}/statutory-elections
func (s *Server) handleSetStatutoryElection(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	employeeID, ok := employeeIDParam(w, r)
	if !ok {
		return
	}
	var req statutoryElectionReq
	if !decodeJSON(w, r, &req) {
		return
	}

	var e payrolldb.StatutoryElection
	var err error
	if e.ID, err = optionalUUID(req.ID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid statutory election id")
		return
	}
	e.EmployeeID = employeeID
	e.EffectiveFrom = strings.TrimSpace(req.EffectiveFrom)
	e.EffectiveTo = strings.TrimSpace(req.EffectiveTo)
	if e.EffectiveFrom == "" {
		writeError(w, http.StatusBadRequest,
			"effective_from is required and must be YYYY-MM-DD: an election is effective-dated, and the pay run reads the one in force on the first day of the wage month")
		return
	}
	// ⛔ THE PF BASE IS NOT DEFAULTED HERE AND MUST NOT BE. CEILING and ACTUAL
	// differ for anybody paid above the EPF wage ceiling, which is most of the
	// people an employer worries about, so a defaulted base is a contribution
	// figure nobody chose. payrolldb.PFBaseOrDefault would accept an empty
	// string; this refuses it, because on THIS seam the empty string means the
	// operator was never asked.
	if strings.TrimSpace(req.PFBase) == "" {
		writeError(w, http.StatusBadRequest,
			"pf_base is required and must be CEILING or ACTUAL: the two differ for anybody paid above the EPF wage ceiling, so there is no safe default")
		return
	}
	e.PFBase = payroll.PFBase(strings.TrimSpace(req.PFBase))
	// The REGIME may legitimately be empty — that is "no election", which
	// payrolldb resolves to the s.202 default (NEW) and stores as given, so a
	// later reader can still tell an election from a non-election.
	e.Regime = payroll.Regime(strings.TrimSpace(req.Regime))
	if v := strings.TrimSpace(req.PFVoluntaryBase); v != "" {
		if e.PFVoluntaryBase, err = decimal.NewFromString(v); err != nil {
			writeError(w, http.StatusBadRequest, "pf_voluntary_base is not a decimal")
			return
		}
	}
	e.EmployerMatchVoluntary = req.EmployerMatchVoluntary
	e.EPSMember = req.EPSMember
	e.ESIApplicable = req.ESIApplicable

	id, err := s.payroll.SetStatutoryElection(ctx, e)
	if err != nil {
		writeStructureError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"statutory_election_id": id.String()})
}

// handleListStatutoryElections enumerates one employee's elections.
//
// GET /v1/companies/{companyID}/employees/{employeeID}/statutory-elections
func (s *Server) handleListStatutoryElections(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	employeeID, ok := employeeIDParam(w, r)
	if !ok {
		return
	}
	list, err := s.payroll.ListStatutoryElections(ctx, employeeID)
	if err != nil {
		writeStructureError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, e := range list {
		out = append(out, map[string]any{
			"id":                         e.ID.String(),
			"effective_from":             e.EffectiveFrom,
			"effective_to":               e.EffectiveTo,
			"pf_base":                    string(e.PFBase),
			"pf_voluntary_base":          e.PFVoluntaryBase.StringFixed(4),
			"employer_matches_voluntary": e.EmployerMatchVoluntary,
			"eps_member":                 e.EPSMember,
			"esi_applicable":             e.ESIApplicable,
			// Served AS STORED. An empty regime is "no election" and is reported as
			// "", never as "NEW": the two are different facts, and only one of them
			// was chosen by anybody.
			"regime": string(e.Regime),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"statutory_elections": out})
}

// ---------------------------------------------------------------------------
// The closed vocabularies.
// ---------------------------------------------------------------------------

// handlePayrollVocabulary serves the closed sets a payroll form must choose
// from: the salary component types with their s.2(88) classification, the two PF
// contribution bases, the two income-tax regimes, and the three age bands.
//
// GET /v1/companies/{companyID}/payroll-vocabulary
//
// ⛔ SERVED RATHER THAN HARDCODED IN THE CLIENT, for the reason
// /security-levels/vocabulary is: a form built from a list the server does not
// own can compose a value the server answers 422, and the operator meets the
// refusal after keying the whole thing. Here it is worse than inconvenient —
// `is_wages` is a LEGAL classification (it carries the Vivekananda Vidyamandir
// holding on special allowance), and a second copy of it in another language,
// updated by somebody else, is exactly the drift that under-contributes PF for
// the whole of an employee's service with no error anywhere.
//
// NO PARAMETERS, and nothing about this company in the answer: the vocabulary is
// the statute's, not the tenant's, and two companies on one deployment get the
// same reply. The company id is in the path only because every route on this
// seam is company-scoped by the middleware that authorises it.
func (s *Server) handlePayrollVocabulary(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.scopeCtx(w, r); !ok {
		return
	}
	types := make([]map[string]any, 0, len(payroll.ComponentTypes()))
	for _, ct := range payroll.ComponentTypes() {
		types = append(types, map[string]any{
			"code":     string(ct),
			"is_wages": payroll.IsWagesComponent(ct),
			// Whether this component is one of clauses (a)-(i), the only sum
			// proviso 1 measures against the one-half floor. A picker that showed
			// only wages/not-wages would tell an operator that an excluded
			// component never affects the contribution, which is false for these.
			"in_deeming_test": payroll.InDeemingTest(ct),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"component_types": types,
		"pf_bases":        []string{string(payroll.PFBaseCeiling), string(payroll.PFBaseActual)},
		"regimes":         []string{string(payroll.RegimeNew), string(payroll.RegimeOld)},
		// The s.202 default, stated so a form can SAY what an unstated regime
		// resolves to rather than pre-selecting it — which would record an
		// election nobody made.
		"default_regime": string(payroll.DefaultRegime),
		"age_bands": []string{
			string(payroll.AgeBelow60), string(payroll.AgeSenior), string(payroll.AgeSuperSenior),
		},
	})
}

// ---------------------------------------------------------------------------
// Shared helpers.
// ---------------------------------------------------------------------------

// employeeIDParam reads the {employeeID} path segment.
func employeeIDParam(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "employeeID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "employeeID in the path must be a uuid")
		return uuid.Nil, false
	}
	return id, true
}

// writeStructureError maps a salary-structure or election error to a status.
//
// It passes the message through for the reason writePayrollError does: every
// refusal in payrolldb is written to tell an operator what to fix, and the two
// most common ones here — an OVERLAPPING effective-dated window (refused by
// migration 0034's EXCLUDE constraint) and an unknown component type — are
// unactionable without their own words.
func writeStructureError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, payroll.ErrUnknownComponentType),
		errors.Is(err, payroll.ErrUnknownPFBase),
		errors.Is(err, payroll.ErrUnknownRegime),
		errors.Is(err, payrolldb.ErrInvalidEmployeeField):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writePayrollError(w, err)
	}
}
