package server

// handler_payroll.go — the Track 3 payroll HTTP surface: the employee master,
// the pay run, the two payroll REPORTS and — for the first time in this
// codebase — the three statutory payroll FILES.
//
// ===========================================================================
// WIRING (the two-touch rule). NOTHING HERE IS REGISTERED.
// ===========================================================================
// These are handler METHODS only. Until the serial wiring stage adds BOTH
//
//	(a) the routes in server.go routes():
//
//	      r.Post("/employees",           s.handleCreateEmployee)
//	      r.Get("/employees",            s.handleListEmployees)
//	      r.Post("/vouchers/payroll",    s.handlePayrollRun)
//	      r.Get("/payroll-register",     s.handlePayrollRegister)
//	      r.Get("/payroll-payslip",      s.handlePayrollPayslip)
//	      r.Get("/payroll-form-138",     s.handlePayrollForm138)
//	      r.Get("/payroll-pf-ecr",       s.handlePayrollPFECR)
//	      r.Get("/payroll-esi-return",   s.handlePayrollESIReturn)
//
//	(b) the PDP mappings in auth_middleware.go:
//
//	      "employees" added to isMasterPath()  ->  (Create|Display, "master")?
//	          NO — see below. It needs its OWN resource token "employee".
//	      "payroll" added to isVoucherClass()  ->  (Create, "payroll")
//	      five cases added to reportName():
//	          "/payroll-register" -> "payroll-register"
//	          "/payroll-payslip"  -> "payroll-payslip"
//	          "/payroll-form-138" -> "payroll-form-138"
//	          "/payroll-pf-ecr"   -> "payroll-pf-ecr"
//	          "/payroll-esi-return" -> "payroll-esi-return"
//
// the routes do not exist, and adding only (a) would make even an Owner get a
// 403 because routeAction default-denies unmapped {companyID} sub-paths.
//
// THE `employees` COLLECTION MUST NOT JOIN isMasterPath(). Migration 0022
// grants the seeded Data Entry level `allow display master`, so folding the
// employee master into the "master" token would hand every operator in every
// tenant the PAN, UAN, ESIC IP number and bank IFSC of every person in the
// company — by inheritance, without anybody deciding to. Migration 0035 seeds
// `disallow create|alter|display employee` precisely because the token is
// `employee` and not `master`.
//
// THE ROUTE PATH SEGMENT MUST EQUAL THE REPORT NAME. Migration 0035's header
// writes the five reports as ".../reports/payroll-register"; that nesting is
// illustrative and WRONG. TestReportVocabularyMatchesRouter asks routeAction
// for GET /v1/companies/{id}/<name> for every name in authz.KnownReportNames,
// so a report whose route is not exactly its name is a vocabulary entry no
// route serves — a `disallow` an administrator can write that can never match.
//
// ===========================================================================
// SENSITIVITY — WHAT THESE HANDLERS DELIBERATELY DO NOT OFFER
// ===========================================================================
// Every read below goes through s.scopeCtx, which uses the authz-VERIFIED
// company from the request context and never the raw path param. There is no
// "internal" flag, no company query param and no cross-company employee lookup:
// a payslip request naming another tenant's employee resolves through RLS to no
// rows and is answered 404 — never that employee's figures.
//
// ===========================================================================
// THE STATUTORY FILES ARE SERVED AS A ZIP, ON PURPOSE
// ===========================================================================
// payrollfile's package doc establishes that NONE of the three formats permits
// an in-file comment: the ECR is a bare member list the portal parses line by
// line, the Form 138 file must begin with its FH record, and the ESI CSV must
// begin with the ESIC column headers. The not-validator-verified disclosure
// therefore travels in a SIDECAR — and a sidecar that the download does not
// carry is a sidecar nobody reads.
//
// So each of the three file endpoints returns application/zip containing
// exactly two entries: the statutory file under its statutory filename, and
// "<name>.README.txt" holding payrollfile.Disclosure plus this mapping's own
// disclosures (payrolldb.WriteSidecar). The file and the statement of what is
// not verified about it cannot be separated by saving one and not the other.
//
// NOTHING EMITTED HERE HAS BEEN VALIDATED. The NSDL/Protean FVU, the EPFO
// Unified Portal and the ESIC employer portal cannot run in this environment,
// and no test in this repository claims otherwise.

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/payroll"
	"github.com/khata/cored/internal/payroll/payrolldb"
	"github.com/khata/cored/internal/platform/db"
)

// producerVersion identifies this software in a Form 138 file header. It is a
// fact about us, so it is a constant here rather than a query param a caller
// could misstate.
const producerVersion = "cored-payroll-1"

// ---------------------------------------------------------------------------
// The employee master.
// ---------------------------------------------------------------------------

// employeeReq is the POST/PUT body for an employee master row.
//
// IsActive is a *bool so an omitted field means TRUE. A plain bool would make
// every employee created by a client that did not know about the field
// INACTIVE, and an inactive employee is silently excluded from the pay run —
// which is a missing salary, discovered on pay day.
type employeeReq struct {
	ID               string `json:"id"`
	Code             string `json:"code"`
	Name             string `json:"name"`
	DOJ              string `json:"doj"`
	DOL              string `json:"dol"`
	Designation      string `json:"designation"`
	PAN              string `json:"pan"`
	UAN              string `json:"uan"`
	ESICIPNo         string `json:"esic_ip_number"`
	BankIFSC         string `json:"bank_ifsc"`
	BankAccountLast4 string `json:"bank_account_last4"`
	PayableLedgerID  string `json:"payable_ledger_id"`
	IsActive         *bool  `json:"is_active"`
}

// toEmployee converts the request body, reporting a 400 on any malformed id.
func (req employeeReq) toEmployee(w http.ResponseWriter) (payrolldb.Employee, bool) {
	var e payrolldb.Employee
	var err error
	if e.ID, err = optionalUUID(req.ID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid employee id")
		return e, false
	}
	if e.PayableLedgerID, err = optionalUUID(req.PayableLedgerID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid payable_ledger_id")
		return e, false
	}
	e.Code = req.Code
	e.Name = req.Name
	e.DOJ = req.DOJ
	e.DOL = req.DOL
	e.Designation = req.Designation
	e.PAN = req.PAN
	e.UAN = req.UAN
	e.ESICIPNo = req.ESICIPNo
	e.BankIFSC = req.BankIFSC
	e.BankAccountLast4 = req.BankAccountLast4
	e.IsActive = req.IsActive == nil || *req.IsActive
	return e, true
}

// handleCreateEmployee creates one employee master row.
//
// POST /v1/companies/{companyID}/employees
func (s *Server) handleCreateEmployee(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	var req employeeReq
	if !decodeJSON(w, r, &req) {
		return
	}
	e, ok := req.toEmployee(w)
	if !ok {
		return
	}
	id, err := s.payroll.CreateEmployee(ctx, e)
	if err != nil {
		writePayrollError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"employee_id": id.String()})
}

// handleListEmployees enumerates the company's employees.
//
// GET /v1/companies/{companyID}/employees?include_inactive=true
func (s *Server) handleListEmployees(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	includeInactive := r.URL.Query().Get("include_inactive") == "true"
	list, err := s.payroll.ListEmployees(ctx, includeInactive)
	if err != nil {
		writePayrollError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, e := range list {
		m := map[string]any{
			"id": e.ID.String(), "code": e.Code, "name": e.Name,
			"doj": e.DOJ, "dol": e.DOL, "designation": e.Designation,
			// The identifiers ARE returned: this is the master, and an operator
			// who may reach it must be able to see and correct a wrong PAN — a
			// malformed one is a Form 138 deductee record the department rejects.
			// Who may reach it is decided by the ACL, not by this projection:
			// migration 0035 disallows display of the `employee` resource for the
			// seeded Data Entry level in every tenant.
			"pan": e.PAN, "uan": e.UAN, "esic_ip_number": e.ESICIPNo,
			"bank_ifsc": e.BankIFSC, "bank_account_last4": e.BankAccountLast4,
			"is_active": e.IsActive,
		}
		if e.PayableLedgerID != uuid.Nil {
			m["payable_ledger_id"] = e.PayableLedgerID.String()
		}
		out = append(out, m)
	}
	writeJSON(w, http.StatusOK, map[string]any{"employees": out})
}

// ---------------------------------------------------------------------------
// The pay run.
// ---------------------------------------------------------------------------

// monthInputReq is one employee's non-derivable facts for the month. Every
// money field is a STRING and is parsed with decimal.NewFromString; a JSON
// number would arrive as a float64 and silently re-price somebody's salary.
type monthInputReq struct {
	EmployeeID           string `json:"employee_id"`
	PaidDays             string `json:"paid_days"`
	LOPDays              string `json:"lop_days"`
	FirstPFMemberOn      string `json:"first_pf_member_on"`
	PriorEmployerTaxable string `json:"prior_employer_taxable"`
	PriorEmployerTDS     string `json:"prior_employer_tds"`
	HousePropertyLoss    string `json:"house_property_loss"`
	ChapterVIA           string `json:"chapter_via"`
	OtherIncomeDeclared  string `json:"other_income_declared"`
	NonResident          bool   `json:"non_resident"`
	AgeBand              string `json:"age_band"`
}

// payRunReq is the POST /vouchers/payroll body.
type payRunReq struct {
	WageMonth string          `json:"wage_month"`
	CreatedBy string          `json:"created_by"`
	Narration string          `json:"narration"`
	Inputs    []monthInputReq `json:"inputs"`
}

// handlePayrollRun computes and posts a whole wage month.
//
// POST /v1/companies/{companyID}/vouchers/payroll
//
// It rides the ordinary /vouchers/{class} shape so the PDP mapping, the
// back-dated cutoff and the WebAuthn step-up all apply to it through the SAME
// code that applies them to an invoice — see isVoucherClass's comment.
func (s *Server) handlePayrollRun(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	var req payRunReq
	if !decodeJSON(w, r, &req) {
		return
	}
	month, err := parseWageMonth(req.WageMonth)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	in := payrolldb.ComputePayRunInput{Month: month, CreatedBy: req.CreatedBy, Narration: req.Narration}
	for i, mi := range req.Inputs {
		converted, cerr := mi.toMonthInput()
		if cerr != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("inputs[%d]: %s", i, cerr.Error()))
			return
		}
		in.Inputs = append(in.Inputs, converted)
	}
	res, err := s.payroll.ComputeAndRunPayRun(ctx, in)
	if err != nil {
		writePayrollError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"pay_run_id":     res.PayRunID.String(),
		"voucher_id":     res.VoucherID.String(),
		"voucher_no":     res.VoucherNo,
		"version_no":     res.VersionNo,
		"status":         res.Status,
		"already_posted": res.AlreadyPosted,
	})
}

// toMonthInput parses one employee's month input. Money and day counts go
// through decimal.NewFromString — NEVER strconv.ParseFloat.
func (m monthInputReq) toMonthInput() (payrolldb.MonthInput, error) {
	var out payrolldb.MonthInput
	id, err := uuid.Parse(m.EmployeeID)
	if err != nil {
		return out, errors.New("employee_id is not a uuid")
	}
	out.EmployeeID = id
	for _, f := range []struct {
		name string
		raw  string
		dst  *decimal.Decimal
	}{
		{"paid_days", m.PaidDays, &out.PaidDays},
		{"lop_days", m.LOPDays, &out.LOPDays},
		{"prior_employer_taxable", m.PriorEmployerTaxable, &out.PriorEmployerTaxable},
		{"prior_employer_tds", m.PriorEmployerTDS, &out.PriorEmployerTDS},
		{"house_property_loss", m.HousePropertyLoss, &out.HousePropertyLoss},
		{"chapter_via", m.ChapterVIA, &out.ChapterVIA},
		{"other_income_declared", m.OtherIncomeDeclared, &out.OtherIncomeDeclared},
	} {
		if strings.TrimSpace(f.raw) == "" {
			continue
		}
		d, derr := decimal.NewFromString(f.raw)
		if derr != nil {
			return out, fmt.Errorf("%s is not a decimal", f.name)
		}
		*f.dst = d
	}
	out.FirstPFMemberOn = m.FirstPFMemberOn
	out.NonResident = m.NonResident
	out.AgeBand = payroll.AgeBand(m.AgeBand)
	return out, nil
}

// ---------------------------------------------------------------------------
// The two payroll reports.
// ---------------------------------------------------------------------------

// handlePayrollRegister renders the wage month's register.
//
// GET /v1/companies/{companyID}/payroll-register?month=YYYY-MM
func (s *Server) handlePayrollRegister(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	month, ok := wageMonthParam(w, r)
	if !ok {
		return
	}
	reg, err := s.payroll.PayrollRegister(ctx, month)
	if err != nil {
		writePayrollError(w, err)
		return
	}
	rows := make([]map[string]any, 0, len(reg.Rows))
	for _, row := range reg.Rows {
		rows = append(rows, registerRowJSON(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"pay_run_id": reg.PayRunID.String(),
		"wage_month": reg.WageMonth,
		"status":     reg.Status,
		// The ruleset versions are ON THE REPORT. A payroll figure with no
		// statement of which law produced it cannot be checked by anybody who was
		// not present when it was run.
		"ss_ruleset_version": reg.SSRuleSetVersion,
		"it_ruleset_version": reg.ITRuleSetVersion,
		"rows":               rows,
		"totals":             registerRowJSON(reg.Totals),
	})
}

// registerRowJSON serialises one register line. Money is rendered with
// StringFixed(4) — the storage scale — and never as a JSON number.
func registerRowJSON(row payrolldb.RegisterRow) map[string]any {
	m := map[string]any{
		"days_paid":    row.DaysPaid.StringFixed(4),
		"gross":        row.Gross.StringFixed(4),
		"epf_employee": row.EPFEmployee.StringFixed(4),
		"esi_employee": row.ESIEmployee.StringFixed(4),
		"tds":          row.TDS.StringFixed(4),
		"deductions":   row.Deductions.StringFixed(4),
		"net":          row.Net.StringFixed(4),
		"epf_employer": row.EPFEmployer.StringFixed(4),
		"eps_employer": row.EPSEmployer.StringFixed(4),
		"esi_employer": row.ESIEmployer.StringFixed(4),
	}
	if row.EmployeeID != uuid.Nil {
		m["employee_id"] = row.EmployeeID.String()
		m["employee_code"] = row.EmployeeCode
		m["employee_name"] = row.EmployeeName
	}
	return m
}

// handlePayrollPayslip renders ONE employee's payslip.
//
// GET /v1/companies/{companyID}/payroll-payslip?month=YYYY-MM&employee_id=UUID
//
// An employee_id belonging to another company resolves, through RLS, to no
// rows and is answered 404. There is deliberately no path by which this handler
// can read outside the authorized company.
func (s *Server) handlePayrollPayslip(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	month, ok := wageMonthParam(w, r)
	if !ok {
		return
	}
	employeeID, err := uuid.Parse(r.URL.Query().Get("employee_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "employee_id query param is required and must be a uuid")
		return
	}
	v, err := s.payroll.Payslip(ctx, month, employeeID)
	if err != nil {
		writePayrollError(w, err)
		return
	}
	lines := make([]map[string]any, 0, len(v.Lines))
	for _, l := range v.Lines {
		lines = append(lines, map[string]any{
			"code": l.Code, "type": l.Type, "tag": string(l.Tag),
			"direction": l.Direction, "amount": l.Amount.StringFixed(4),
		})
	}
	deferrals := make([]map[string]any, 0, len(v.Deferrals))
	for _, d := range v.Deferrals {
		deferrals = append(deferrals, map[string]any{
			"component": d.Component, "reason": d.Reason, "citation": d.Citation,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"pay_run_id": v.PayRunID.String(), "wage_month": v.WageMonth,
		"employee_id": v.EmployeeID.String(), "employee_code": v.EmployeeCode,
		"employee_name": v.EmployeeName,
		"days_paid":     v.DaysPaid.StringFixed(4), "lop_days": v.LOPDays.StringFixed(4),
		"gross": v.Gross.StringFixed(4), "deductions": v.Deductions.StringFixed(4),
		"net":   v.Net.StringFixed(4),
		"lines": lines,
		// The deferrals are ON THE PAYSLIP rather than in a log: a blank where a
		// number should be is a question the employee will ask, and the honest
		// answer is "the rate has not been notified", not "zero".
		"deferrals":          deferrals,
		"ss_ruleset_version": v.SSRuleSetVersion,
		"it_ruleset_version": v.ITRuleSetVersion,
	})
}

// ---------------------------------------------------------------------------
// The three statutory FILES.
// ---------------------------------------------------------------------------

// handlePayrollPFECR emits the EPFO Electronic Challan-cum-Return.
//
// GET /v1/companies/{companyID}/payroll-pf-ecr?month=YYYY-MM
func (s *Server) handlePayrollPFECR(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	month, ok := wageMonthParam(w, r)
	if !ok {
		return
	}
	name := payrolldb.ECRFileName(month)
	body, err := renderToBuffer(func(buf io.Writer) error {
		return s.payroll.WriteECR(ctx, buf, month)
	})
	if err != nil {
		writePayrollError(w, err)
		return
	}
	writeStatutoryZip(w, name, body, payrolldb.ECRDisclosures())
}

// handlePayrollESIReturn emits the ESIC monthly contribution return.
//
// GET /v1/companies/{companyID}/payroll-esi-return?month=YYYY-MM
//
//	&leaving_reason=CODE=Reason  (repeatable)
//
// The leaving reason is NOT derivable: the employee master records a date of
// leaving and not why, and "Retired", "Expired" and "Employee has left service"
// are three different filings. A leaver with no supplied reason is refused with
// a 422 naming the employee code — never filed with a blank reason, which the
// portal reads as an accepted-but-empty row.
func (s *Server) handlePayrollESIReturn(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	month, ok := wageMonthParam(w, r)
	if !ok {
		return
	}
	opts := payrolldb.ESIOptions{LeavingReasons: map[string]string{}}
	for _, raw := range r.URL.Query()["leaving_reason"] {
		code, reason, found := strings.Cut(raw, "=")
		if !found || strings.TrimSpace(code) == "" || strings.TrimSpace(reason) == "" {
			writeError(w, http.StatusBadRequest,
				`leaving_reason must be "EMPLOYEE_CODE=Reason", e.g. leaving_reason=E-0007=Employee%20has%20left%20service`)
			return
		}
		opts.LeavingReasons[strings.TrimSpace(code)] = strings.TrimSpace(reason)
	}
	name := payrolldb.ESIFileName(month)
	body, err := renderToBuffer(func(buf io.Writer) error {
		return s.payroll.WriteESIReturn(ctx, buf, month, opts)
	})
	if err != nil {
		writePayrollError(w, err)
		return
	}
	writeStatutoryZip(w, name, body, payrolldb.ESIDisclosures())
}

// handlePayrollForm138 emits the Form 138 (ex-24Q) Annexure I statement.
//
// GET /v1/companies/{companyID}/payroll-form-138
//
//	&tax_year=2026-27  &quarter=Q1
//	&tan=AAAA99999A    &deductor_pan=AAAAA9999A  &deductor_name=Acme+Pvt+Ltd
//	&form_number=24Q   &file_creation_date=2026-07-10  [&file_sequence=1]
//	&challan=2026-04,0221826,2026-05-07,12345[,25000][,book]   (repeatable)
//
// NOT ONE of the deductor or challan particulars is in the database — the
// company master carries no TAN and no challan — so all of them are supplied
// here and an absent one is a 400. Nothing is defaulted into a filed record.
//
// Q4 is refused by payrollfile with ErrQ4FormatNotPublished: the Q4 / Annexure
// II record structure is not published, and guessing it would mis-file every
// employee's whole-year salary detail.
func (s *Server) handlePayrollForm138(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	in, ok := parseForm138Query(w, r)
	if !ok {
		return
	}
	name := payrolldb.Form138FileName(in)
	body, err := renderToBuffer(func(buf io.Writer) error {
		return s.payroll.WriteForm138AnnexureI(ctx, buf, in)
	})
	if err != nil {
		writePayrollError(w, err)
		return
	}
	writeStatutoryZip(w, name, body, payrolldb.Form138Disclosures())
}

// parseForm138Query builds the Form 138 request from the query string, writing
// a 400 naming the offending param on any failure.
func parseForm138Query(w http.ResponseWriter, r *http.Request) (payrolldb.Form138Input, bool) {
	q := r.URL.Query()
	var in payrolldb.Form138Input

	ty, err := parseTaxYear(q.Get("tax_year"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return in, false
	}
	in.TaxYear = ty
	in.Quarter = q.Get("quarter")
	in.TAN = q.Get("tan")
	in.DeductorPAN = q.Get("deductor_pan")
	in.DeductorName = q.Get("deductor_name")
	in.FormNumber = q.Get("form_number")
	in.ProducerVersion = producerVersion

	if raw := q.Get("file_creation_date"); raw != "" {
		d, derr := time.Parse("2006-01-02", raw)
		if derr != nil {
			writeError(w, http.StatusBadRequest, "file_creation_date must be YYYY-MM-DD")
			return in, false
		}
		in.FileCreationDate = d
	}
	in.FileSequenceNumber = 1
	if raw := q.Get("file_sequence"); raw != "" {
		n, cerr := strconv.Atoi(raw)
		if cerr != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "file_sequence must be an integer >= 1")
			return in, false
		}
		in.FileSequenceNumber = n
	}

	for _, raw := range q["challan"] {
		ch, cerr := parseChallanParam(raw)
		if cerr != nil {
			writeError(w, http.StatusBadRequest, cerr.Error())
			return in, false
		}
		in.Challans = append(in.Challans, ch)
	}
	return in, true
}

// parseChallanParam parses one repeatable `challan` query param:
//
//	MONTH,BSR,DEPOSIT_DATE,SERIAL[,AMOUNT[,book]]
//	2026-04,0221826,2026-05-07,12345,25000
//
// AMOUNT is optional: omitted, the challan total is DERIVED from the month's
// payslips (what the books say was deducted). A supplied amount that does not
// equal the deductee sum is refused by payrollfile — that reconciliation is the
// whole point of the statement.
func parseChallanParam(raw string) (payrolldb.Form138ChallanInput, error) {
	var ch payrolldb.Form138ChallanInput
	parts := strings.Split(raw, ",")
	if len(parts) < 4 || len(parts) > 6 {
		return ch, errors.New("challan must be MONTH,BSR,DEPOSIT_DATE,SERIAL[,AMOUNT[,book]] e.g. 2026-04,0221826,2026-05-07,12345")
	}
	m, err := parseWageMonth(strings.TrimSpace(parts[0]))
	if err != nil {
		return ch, fmt.Errorf("challan month: %w", err)
	}
	ch.Month = m
	ch.BSRCode = strings.TrimSpace(parts[1])
	d, err := time.Parse("2006-01-02", strings.TrimSpace(parts[2]))
	if err != nil {
		return ch, errors.New("challan deposit date must be YYYY-MM-DD")
	}
	ch.DateOfDeposit = d
	n, err := strconv.Atoi(strings.TrimSpace(parts[3]))
	if err != nil || n < 0 {
		return ch, errors.New("challan serial must be a non-negative integer")
	}
	ch.ChallanSerialNumber = n
	if len(parts) >= 5 && strings.TrimSpace(parts[4]) != "" {
		amt, aerr := decimal.NewFromString(strings.TrimSpace(parts[4]))
		if aerr != nil {
			return ch, errors.New("challan amount is not a decimal")
		}
		ch.TotalTDSDeposited = amt
	}
	if len(parts) == 6 && strings.EqualFold(strings.TrimSpace(parts[5]), "book") {
		ch.BookEntry = true
	}
	return ch, nil
}

// ---------------------------------------------------------------------------
// Shared helpers.
// ---------------------------------------------------------------------------

// wageMonthParam reads and validates the ?month=YYYY-MM param, writing a 400
// with the expected shape when it is missing or malformed.
func wageMonthParam(w http.ResponseWriter, r *http.Request) (payroll.WageMonth, bool) {
	m, err := parseWageMonth(r.URL.Query().Get("month"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return payroll.WageMonth{}, false
	}
	return m, true
}

// parseWageMonth parses "YYYY-MM" into a payroll.WageMonth.
//
// It uses time.Parse with a strict layout rather than splitting on '-', so
// "2026-13" and "2026-1" are both refused: a wage month is what every
// social-security scheme keys off, and a month silently coerced to a
// neighbouring one computes somebody's contributions under the wrong window.
func parseWageMonth(s string) (payroll.WageMonth, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return payroll.WageMonth{}, errors.New("month is required and must be YYYY-MM (e.g. 2026-07)")
	}
	t, err := time.Parse("2006-01", s)
	if err != nil {
		return payroll.WageMonth{}, fmt.Errorf("month %q is not a wage month; it must be YYYY-MM (e.g. 2026-07)", s)
	}
	return payroll.WageMonth{Year: t.Year(), Month: t.Month()}, nil
}

// parseTaxYear accepts "2026-27" or "2026" and returns the Indian tax year
// beginning 1 April of the leading year.
func parseTaxYear(s string) (payroll.TaxYear, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return payroll.TaxYear{}, errors.New("tax_year is required, e.g. 2026-27")
	}
	head, _, _ := strings.Cut(s, "-")
	y, err := strconv.Atoi(head)
	if err != nil || y < 1900 || y > 9999 {
		return payroll.TaxYear{}, fmt.Errorf("tax_year %q is not a tax year; use 2026-27 or 2026", s)
	}
	return payroll.TaxYear{StartYear: y}, nil
}

// renderToBuffer runs an emitter into memory so a mapping failure becomes an
// HTTP status instead of a truncated download.
//
// Streaming a statutory file straight to the ResponseWriter would commit a 200
// before the emitter reaches the row that has no UAN — and the operator would
// save a half-written ECR believing it succeeded. The files are one line per
// employee; buffering a month of them costs nothing.
func renderToBuffer(emit func(io.Writer) error) ([]byte, error) {
	var buf bytes.Buffer
	if err := emit(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeStatutoryZip serves a statutory file and its disclosure sidecar as one
// application/zip download. See the file header for why a zip and not the bare
// file: none of the three formats permits an in-file comment, so the disclosure
// can only travel beside the file, and a sidecar the download does not carry is
// a sidecar nobody reads.
func writeStatutoryZip(w http.ResponseWriter, fileName string, body []byte, notes []string) {
	var out bytes.Buffer
	zw := zip.NewWriter(&out)

	f, err := zw.Create(fileName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not package the statutory file")
		return
	}
	if _, err := f.Write(body); err != nil {
		writeError(w, http.StatusInternalServerError, "could not package the statutory file")
		return
	}
	sc, err := zw.Create(fileName + ".README.txt")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not package the disclosure")
		return
	}
	if err := payrolldb.WriteSidecar(sc, fileName, notes...); err != nil {
		writeError(w, http.StatusInternalServerError, "could not package the disclosure")
		return
	}
	if err := zw.Close(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not package the statutory file")
		return
	}

	zipName := strings.TrimSuffix(fileName, filepathExt(fileName)) + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+zipName+`"`)
	w.Header().Set("X-Statutory-File", fileName)
	// Loud, and in a place a scripted caller cannot fail to see: no emitted file
	// in this product has been through the FVU, the EPFO portal or the ESIC
	// portal, none of which can run in this environment.
	w.Header().Set("X-Statutory-Disclosure", "NOT validator-verified; see the .README.txt inside this archive")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out.Bytes())
}

// filepathExt returns the final extension of a name (".txt", ".csv") or "".
// Written here rather than importing path/filepath because these are archive
// member names, not OS paths, and filepath would apply Windows separator rules
// to them on a Windows build.
func filepathExt(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i:]
	}
	return ""
}

// writePayrollError maps a payroll service error to an HTTP status.
//
// The message is passed through, deliberately: every refusal in payrolldb and
// payrollfile is written to tell an operator what to fix ("employee E-0007 has
// no UAN"), and swallowing it into "unprocessable entity" would leave them with
// a rejected filing and nothing to act on. None of those messages interpolates
// a PAN, a UAN or an IP number — see payrollfile/ecr.go's note on PII in error
// strings — so passing them through does not disclose an identifier.
func writePayrollError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrNoCompany):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, payrolldb.ErrPayRunNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, payrolldb.ErrInvalidEmployeeField),
		errors.Is(err, payrolldb.ErrAadhaarShapedValue):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	}
}
