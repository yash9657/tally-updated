package server

// handler_payroll_test.go — HANDLER-LEVEL tests for the Track 3 payroll surface.
//
// ===========================================================================
// WHY THESE CALL THE METHODS DIRECTLY
// ===========================================================================
// NOTHING IS MOUNTED YET. handler_payroll.go deliberately registers no route
// (the two-touch rule: the wiring agent adds the routes AND the routeAction /
// reportName entries in one pass). So these tests build the request the way chi
// would — a RouteContext carrying {companyID} — and invoke the handler method.
// They are in package `server` rather than `server_test` for the same reason
// route_vocabulary_test.go is: the handlers are unexported enforcement wiring,
// not API.
//
// When the routes land, these tests keep working unchanged, and
// TestEveryCompanyRouteIsMappedAndInVocabulary is what will fail if the PDP
// mappings are forgotten.
//
// ===========================================================================
// WHAT IS ACTUALLY BEING PROVED
// ===========================================================================
//  1. A malformed or absent wage month is a 400 with a message naming the
//     shape — not a 500, and never a silently coerced neighbouring month.
//  2. A payslip request naming ANOTHER COMPANY'S employee returns 404 and NOT
//     ONE BYTE of that employee's figures. This is the test that matters most
//     in this file: the payslip is one person's whole pay, and RLS is the only
//     thing between two tenants.
//  3. The three statutory files are actually REACHABLE over HTTP and arrive
//     WITH their disclosure sidecar. Before this wave payrollfile had no caller
//     at all and the product could not emit a single statutory payroll file.
//
// NOTHING HERE HAS BEEN VALIDATED BY THE FVU, THE EPFO PORTAL OR THE ESIC
// PORTAL. None of them can run in this environment. The golden bytes below
// prove the mapper produces the DOCUMENTED layout; they do not prove the layout
// is right.

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/payroll"
	"github.com/khata/cored/internal/payroll/payrolldb"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/migrations"
)

var (
	payrollAdminDSN = testEnvDSN("ADMIN_DATABASE_URL", "postgres://postgres:postgres@localhost:5433/tally?sslmode=disable")
	payrollAppDSN   = testEnvDSN("DATABASE_URL", "postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable")
)

// payrollWageMonth is the month every test in this file runs.
var payrollWageMonth = payroll.WageMonth{Year: 2026, Month: 7}

// payrollServer applies migrations (idempotent) and returns a wired Server.
func payrollServer(t *testing.T) *Server {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped (CORED_SKIP_DB set)")
	}
	ctx := context.Background()
	adminConn, err := pgx.Connect(ctx, payrollAdminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable, skipping: %v", err)
	}
	if err := migrations.Apply(ctx, adminConn); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("apply migrations: %v", err)
	}
	_ = adminConn.Close(ctx)

	t.Setenv("AUTH_DEV_TOKEN_ENABLED", "true")
	srv, err := New(ctx, payrollAppDSN)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	t.Cleanup(srv.Close)
	return srv
}

// payrollCompany creates one throwaway seeded company and registers its
// teardown the moment it exists. Never Demo Traders: those are live fixtures
// the browser gates depend on.
func payrollCompany(t *testing.T, s *Server) (uuid.UUID, context.Context) {
	t.Helper()
	companyID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new company id: %v", err)
	}
	cctx := db.WithCompany(context.Background(), companyID)
	if _, err := s.identity.CreateCompany(cctx, identity.Company{
		ID:          companyID,
		Name:        "Payroll HTTP Co " + companyID.String()[:8],
		FYStartDate: "2026-04-01",
		StateCode:   "29",
	}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	testdb.Cleanup(t, companyID)
	return companyID, cctx
}

// payrollCall builds the request chi would hand the handler and runs it.
func payrollCall(t *testing.T, h http.HandlerFunc, method, suffix string, companyID uuid.UUID, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader = bytes.NewReader(nil)
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(b)
	}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("companyID", companyID.String())
	req := httptest.NewRequest(method, "/v1/companies/"+companyID.String()+suffix, rdr).
		WithContext(context.WithValue(context.Background(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// payrollJSON decodes a JSON response body, failing the test if it is not JSON.
func payrollJSON(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if rec.Body.Len() == 0 {
		return out
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return out
}

// ---------------------------------------------------------------------------
// The fixture.
//
// ONE employee, a single BASIC of 18,000 from 1 April 2026, July 2026 run.
// Hand-derived under ruleset "ss-2026-07" (EPF 12%, EPS 8.33%, all three wage
// ceilings 15,000; ESI 0.75/3.25, coverage ceiling 21,000):
//
//	STATUTORY WAGE 18,000; EPF/EPS/EDLI wages min(18,000, 15,000) = 15,000
//	  employee EPF 12% x 15,000                                   = 1,800
//	  EPS          8.33% x 15,000 = 1,249.50                      -> 1,250
//	  employer EPF 1,800 - 1,250 = 550 (STORED NET OF EPS)
//	  employer TOTAL share = 1,800, so the ECR's field 9 is 550
//	ESI covered (18,000 <= 21,000), reckonable wage 18,000:
//	  employee 0.75% = 135 ; employer 3.25% = 585
//	s.392: 18,000 x 9 = 1,62,000 less the 75,000 standard deduction is far
//	  below the s.156 rebate limit -> TDS = 0
//	gross 18,000 - (1,800 + 135) = NET 16,065
// ---------------------------------------------------------------------------

const (
	payrollEmpCode = "E-HTTP-1"
	payrollEmpName = "Http Employee"
	payrollEmpUAN  = "100200300411"
	payrollEmpIP   = "1234567811"
	payrollEmpPAN  = "ABCPD1234E"
)

// payrollFixture creates the company, creates the employee THROUGH THE HANDLER,
// lays down the structure and election, and posts the month THROUGH THE
// HANDLER. It returns the company, the scoped context and the employee id.
func payrollFixture(t *testing.T, s *Server) (uuid.UUID, context.Context, uuid.UUID) {
	t.Helper()
	companyID, cctx := payrollCompany(t, s)

	// is_active is deliberately OMITTED from the body. An omitted flag must mean
	// ACTIVE: an inactive employee is silently excluded from the pay run, and
	// the operator discovers the missing salary on pay day.
	rec := payrollCall(t, s.handleCreateEmployee, http.MethodPost, "/employees", companyID, map[string]any{
		"code": payrollEmpCode, "name": payrollEmpName, "doj": "2025-06-01",
		"designation": "Analyst", "pan": payrollEmpPAN, "uan": payrollEmpUAN,
		"esic_ip_number": payrollEmpIP, "bank_ifsc": "HDFC0001234",
		"bank_account_last4": "4321",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /employees = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	empID, err := uuid.Parse(payrollJSON(t, rec)["employee_id"].(string))
	if err != nil {
		t.Fatalf("employee_id is not a uuid: %v", err)
	}

	// The salary structure and the election have no HTTP surface in this wave,
	// so they go in through the service. Everything the tests ASSERT on goes
	// through a handler.
	if _, err := s.payroll.SetSalaryComponent(cctx, payrolldb.SalaryComponent{
		EmployeeID: empID, EffectiveFrom: "2026-04-01",
		Code: "BASIC", Type: payroll.ComponentBasic, Amount: decimal.RequireFromString("18000"),
	}); err != nil {
		t.Fatalf("set salary component: %v", err)
	}
	if _, err := s.payroll.SetStatutoryElection(cctx, payrolldb.StatutoryElection{
		EmployeeID: empID, EffectiveFrom: "2026-04-01",
		PFBase: payroll.PFBaseCeiling, EPSMember: true, ESIApplicable: true,
		Regime: payroll.RegimeNew,
	}); err != nil {
		t.Fatalf("set statutory election: %v", err)
	}

	rec = payrollCall(t, s.handlePayrollRun, http.MethodPost, "/vouchers/payroll", companyID, map[string]any{
		"wage_month": payrollWageMonth.String(),
		"created_by": "payroll-http-test",
		"narration":  "Payroll for " + payrollWageMonth.String(),
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /vouchers/payroll = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if got := payrollJSON(t, rec)["status"]; got != "POSTED" {
		t.Fatalf("pay run status = %v, want POSTED", got)
	}
	return companyID, cctx, empID
}

// ---------------------------------------------------------------------------
// 1. The wage month.
// ---------------------------------------------------------------------------

// TestPayrollHandlersRefuseAMalformedWageMonth pins the 400.
//
// A wage month is what every social-security scheme keys off. A month silently
// coerced to a neighbouring one — or a 500 that tells the operator nothing —
// computes somebody's contributions under the wrong statutory window, and the
// resulting figure is plausible, wrong, and never queried.
func TestPayrollHandlersRefuseAMalformedWageMonth(t *testing.T) {
	s := payrollServer(t)
	companyID, _ := payrollCompany(t, s)

	handlers := map[string]http.HandlerFunc{
		"payroll-register":   s.handlePayrollRegister,
		"payroll-payslip":    s.handlePayrollPayslip,
		"payroll-pf-ecr":     s.handlePayrollPFECR,
		"payroll-esi-return": s.handlePayrollESIReturn,
	}
	// "" is absent; "2026-13" is a month that does not exist; "2026-7" is the
	// un-padded form; "July 2026" is what a human types.
	for _, month := range []string{"", "2026-13", "2026-7", "July%202026"} {
		for name, h := range handlers {
			suffix := "/" + name + "?month=" + month
			if name == "payroll-payslip" {
				suffix += "&employee_id=" + uuid.NewString()
			}
			rec := payrollCall(t, h, http.MethodGet, suffix, companyID, nil)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("GET /%s?month=%q = %d, want 400 (%s)", name, month, rec.Code, rec.Body.String())
				continue
			}
			if msg, _ := payrollJSON(t, rec)["error"].(string); !strings.Contains(msg, "YYYY-MM") {
				t.Errorf("GET /%s?month=%q: the 400 does not tell the caller the shape it wants: %q", name, month, msg)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 2. THE TENANT BOUNDARY. This is the most important test in the file.
// ---------------------------------------------------------------------------

// TestPayrollPayslipNeverCrossesCompanies proves that asking company B's
// endpoint for company A's employee returns 404 and DISCLOSES NOTHING.
//
// The payslip is one person's whole pay — gross, every deduction, net. There is
// no query param, no header and no body field on this handler by which a caller
// can name a company other than the one the request was authorized for: the
// scoped context comes from scopeCtx, the read runs inside a tenant-scoped
// transaction, and RLS makes another tenant's payslip INVISIBLE rather than
// forbidden. "No rows" and "not yours" are the same observation, which is the
// correct answer to give either way.
//
// The assertion is not merely on the status code. A handler that 404'd and then
// echoed the row it had found would pass a status check; this test greps the
// whole response body for the employee's name, code and net pay.
func TestPayrollPayslipNeverCrossesCompanies(t *testing.T) {
	s := payrollServer(t)

	// Company A: a real employee with a real, posted payslip.
	companyA, _, empA := payrollFixture(t, s)

	// Company B: a different tenant entirely.
	companyB, _ := payrollCompany(t, s)

	// Sanity: company A CAN read its own payslip, so a 404 below is about the
	// tenant boundary and not about a fixture that never produced a payslip.
	rec := payrollCall(t, s.handlePayrollPayslip, http.MethodGet,
		"/payroll-payslip?month="+payrollWageMonth.String()+"&employee_id="+empA.String(), companyA, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("company A reading its OWN payslip = %d, want 200 (%s) — the cross-tenant assertion below would be vacuous",
			rec.Code, rec.Body.String())
	}
	own := payrollJSON(t, rec)
	if own["net"] != "16065.0000" {
		t.Fatalf("company A's own payslip net = %v, want 16065.0000 (hand-derived: 18,000 - 1,800 EPF - 135 ESI)", own["net"])
	}

	// Company B asks for company A's employee, by id.
	rec = payrollCall(t, s.handlePayrollPayslip, http.MethodGet,
		"/payroll-payslip?month="+payrollWageMonth.String()+"&employee_id="+empA.String(), companyB, nil)
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusForbidden {
		t.Fatalf("company B reading company A's payslip = %d, want 404 or 403 (%s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, leaked := range []string{payrollEmpName, payrollEmpCode, "16065", "18000", payrollEmpPAN, payrollEmpUAN} {
		if strings.Contains(body, leaked) {
			t.Fatalf("company B's 404 body leaked %q from company A's payslip: %s\n"+
				"An employee's pay is disclosed to a different tenant; the status code is not the disclosure, the body is.",
				leaked, body)
		}
	}

	// The REGISTER is the same boundary at company scale: company B sees no
	// live run at all, not company A's rows.
	rec = payrollCall(t, s.handlePayrollRegister, http.MethodGet,
		"/payroll-register?month="+payrollWageMonth.String(), companyB, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("company B reading the register for a month it never ran = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), payrollEmpName) {
		t.Fatalf("company B's register response leaked company A's employee: %s", rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// 3. The reports.
// ---------------------------------------------------------------------------

// TestPayrollRegisterReportsTheMonth checks the register handler renders the
// run with its ruleset versions and its money as fixed-scale STRINGS.
//
// The ruleset versions are on the report and not merely in the table: a payroll
// figure with no statement of which law produced it cannot be checked by
// anybody who was not present when it was run.
func TestPayrollRegisterReportsTheMonth(t *testing.T) {
	s := payrollServer(t)
	companyID, _, empID := payrollFixture(t, s)

	rec := payrollCall(t, s.handlePayrollRegister, http.MethodGet,
		"/payroll-register?month="+payrollWageMonth.String(), companyID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /payroll-register = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	got := payrollJSON(t, rec)
	if got["ss_ruleset_version"] == nil || got["ss_ruleset_version"] == "" {
		t.Errorf("the register carries no social-security ruleset version: %v", got)
	}
	if got["it_ruleset_version"] == nil || got["it_ruleset_version"] == "" {
		t.Errorf("the register carries no income-tax ruleset version: %v", got)
	}
	rows, _ := got["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("register has %d rows, want 1: %v", len(rows), got)
	}
	row, _ := rows[0].(map[string]any)
	if row["employee_id"] != empID.String() {
		t.Errorf("register row employee_id = %v, want %s", row["employee_id"], empID)
	}
	for col, want := range map[string]string{
		"gross":        "18000.0000",
		"epf_employee": "1800.0000",
		"esi_employee": "135.0000",
		"tds":          "0.0000",
		"net":          "16065.0000",
		"epf_employer": "550.0000",
		"eps_employer": "1250.0000",
		"esi_employer": "585.0000",
	} {
		if row[col] != want {
			t.Errorf("register %s = %v, want %s (hand-derived from the statute — see the fixture comment)", col, row[col], want)
		}
	}
}

// TestListEmployeesTreatsAnOmittedIsActiveAsActive proves the *bool default.
//
// A plain bool would make every employee created by a client that did not know
// about the field INACTIVE — and an inactive employee is silently excluded from
// the pay run. The fixture omits the flag; the run above found the employee, and
// the list must agree.
func TestListEmployeesTreatsAnOmittedIsActiveAsActive(t *testing.T) {
	s := payrollServer(t)
	companyID, _, _ := payrollFixture(t, s)

	rec := payrollCall(t, s.handleListEmployees, http.MethodGet, "/employees", companyID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /employees = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	list, _ := payrollJSON(t, rec)["employees"].([]any)
	if len(list) != 1 {
		t.Fatalf("GET /employees returned %d employees, want 1: %s", len(list), rec.Body.String())
	}
	e, _ := list[0].(map[string]any)
	if e["is_active"] != true {
		t.Fatalf("an employee created with is_active OMITTED came back inactive; they would be silently left out "+
			"of every pay run and the missing salary is discovered on pay day: %v", e)
	}
	if e["code"] != payrollEmpCode || e["uan"] != payrollEmpUAN {
		t.Errorf("the master round trip lost a field: %v", e)
	}
}

// ---------------------------------------------------------------------------
// 4. THE STATUTORY FILES ARE REACHABLE, AND THEY CARRY THEIR SIDECAR.
// ---------------------------------------------------------------------------

// unzip reads the archive a statutory endpoint returned into name -> bytes.
func unzip(t *testing.T, body []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("the statutory response is not a zip archive: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		out[f.Name] = string(b)
	}
	return out
}

// TestPFECREndpointEmitsTheFileAndItsSidecar is the test that closes Track 3's
// third blocker: before this wave payrollfile had NO CALLER, and the product
// could not emit a single statutory payroll file.
//
// It pins three things:
//
//  1. THE BYTES. The ECR line is the golden file, hand-derived above. Field 9
//     ("EPF & EPS Diff Remitted") is 550: the payslip stores the employer share
//     already net of EPS, payrollfile wants the TOTAL and subtracts EPS from it,
//     and getting that backwards either refuses the file outright or over-states
//     the establishment's remittance by the whole pension share.
//  2. THE SIDECAR TRAVELS WITH IT. None of the three statutory formats permits
//     an in-file comment, so the "not validator-verified" statement can only
//     travel beside the file — and a sidecar the download does not carry is a
//     sidecar nobody reads.
//  3. THE FILENAME. The archive member carries the statutory name, not a
//     generic one, so what the operator uploads is what the portal expects.
func TestPFECREndpointEmitsTheFileAndItsSidecar(t *testing.T) {
	s := payrollServer(t)
	companyID, _, _ := payrollFixture(t, s)

	rec := payrollCall(t, s.handlePayrollPFECR, http.MethodGet,
		"/payroll-pf-ecr?month="+payrollWageMonth.String(), companyID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /payroll-pf-ecr = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type = %q, want application/zip", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="ECR_072026.zip"`) {
		t.Errorf("Content-Disposition = %q, want the statutory ECR_072026 name", cd)
	}

	members := unzip(t, rec.Body.Bytes())
	const name = "ECR_072026.txt"
	want := payrollEmpUAN + "#~#" + payrollEmpName + "#~#18000#~#15000#~#15000#~#15000#~#1800#~#1250#~#550#~#0#~#0\n"
	if got := members[name]; got != want {
		t.Fatalf("the ECR the endpoint served is not the file the EPFO Unified Portal would be given.\n got: %q\nwant: %q\n"+
			"A wrong column here is a mis-credited provident fund the member carries for the rest of their service.", got, want)
	}
	sidecar, ok := members[name+".README.txt"]
	if !ok {
		t.Fatalf("the download carries no disclosure sidecar (members: %v). None of the three statutory formats "+
			"permits an in-file comment, so a file that arrives alone arrives with no statement that NOTHING in "+
			"this environment has validated it.", keysOf(members))
	}
	for _, must := range []string{
		"has NOT been checked by the NSDL/Protean FVU",
		"MAPPING DISCLOSURES",
		"Refund of Advances",
	} {
		if !strings.Contains(sidecar, must) {
			t.Errorf("the sidecar does not carry %q:\n%s", must, sidecar)
		}
	}
}

// TestESIReturnEndpointEmitsTheFileAndItsSidecar is the ECR test for the ESIC
// monthly contribution return, whose format is CSV and whose day count rounds
// UP (a person who worked any part of a day is insured for that day).
func TestESIReturnEndpointEmitsTheFileAndItsSidecar(t *testing.T) {
	s := payrollServer(t)
	companyID, _, _ := payrollFixture(t, s)

	rec := payrollCall(t, s.handlePayrollESIReturn, http.MethodGet,
		"/payroll-esi-return?month="+payrollWageMonth.String(), companyID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /payroll-esi-return = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	members := unzip(t, rec.Body.Bytes())
	const name = "ESI_Monthly_Contribution_072026.csv"
	want := "IP Number,IP Name,No. of Days for which wages paid,Total Monthly Wages,Reason code for 0 wages,Last Working Day\n" +
		payrollEmpIP + "," + payrollEmpName + ",31,18000,,\n"
	if got := members[name]; got != want {
		t.Fatalf("the ESI return the endpoint served is not the file the ESIC template would be given.\n got: %q\nwant: %q",
			got, want)
	}
	if _, ok := members[name+".README.txt"]; !ok {
		t.Fatalf("the ESI download carries no disclosure sidecar (members: %v)", keysOf(members))
	}
}

// TestForm138EndpointRefusesMissingDeductorParticulars proves nothing is
// defaulted into a filed record.
//
// The TAN, the deductor PAN, the form number, the file creation date and the
// challan are NOT in this database. A handler that filled any of them in — with
// today's date, with "24Q", with a zero challan — would emit a statement that
// looks complete and reconciles against nothing.
func TestForm138EndpointRefusesMissingDeductorParticulars(t *testing.T) {
	s := payrollServer(t)
	companyID, _, _ := payrollFixture(t, s)

	base := "/payroll-form-138?tax_year=2026-27&quarter=Q2" +
		"&tan=BLRA12345B&deductor_pan=AAACD1234F&deductor_name=Payroll+Test+Deductor" +
		"&form_number=24Q&file_creation_date=2026-08-05"
	const challan = "&challan=2026-07,0221826,2026-08-07,12345"

	for _, tc := range []struct {
		name  string
		query string
		want  int
	}{
		{"no tax year", "/payroll-form-138?quarter=Q2" + challan, http.StatusBadRequest},
		{"no challan", base, http.StatusUnprocessableEntity},
		{"malformed challan", base + "&challan=2026-07,0221826", http.StatusBadRequest},
		{"no file creation date",
			"/payroll-form-138?tax_year=2026-27&quarter=Q2&tan=BLRA12345B&deductor_pan=AAACD1234F" +
				"&deductor_name=X&form_number=24Q" + challan, http.StatusUnprocessableEntity},
		{"no form number",
			"/payroll-form-138?tax_year=2026-27&quarter=Q2&tan=BLRA12345B&deductor_pan=AAACD1234F" +
				"&deductor_name=X&file_creation_date=2026-08-05" + challan, http.StatusUnprocessableEntity},
	} {
		rec := payrollCall(t, s.handlePayrollForm138, http.MethodGet, tc.query, companyID, nil)
		if rec.Code != tc.want {
			t.Errorf("%s: GET /payroll-form-138 = %d, want %d (%s)", tc.name, rec.Code, tc.want, rec.Body.String())
		}
	}

	// And the complete request is REFUSED too, for the right reason: this
	// fixture's employee earns 18,000 and owes no tax, so there is no deductee
	// and Annexure I is the statement of tax DEDUCTED. A handler that emitted an
	// empty statement here would file a nil return for a quarter that had one.
	rec := payrollCall(t, s.handlePayrollForm138, http.MethodGet, base+challan, companyID, nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a Form 138 for a quarter with no deduction = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if msg, _ := payrollJSON(t, rec)["error"].(string); !strings.Contains(msg, "no employee had tax deducted") {
		t.Errorf("the refusal does not say why: %q", msg)
	}
}

// TestStatutoryEndpointsRefuseAnUnpostedMonth pins the filing/review split at
// the HTTP edge: a month with no posted run yields 404 (no run) rather than an
// empty file that looks like a nil return.
func TestStatutoryEndpointsRefuseAnUnpostedMonth(t *testing.T) {
	s := payrollServer(t)
	companyID, _ := payrollCompany(t, s)

	for name, h := range map[string]http.HandlerFunc{
		"payroll-pf-ecr":     s.handlePayrollPFECR,
		"payroll-esi-return": s.handlePayrollESIReturn,
		"payroll-register":   s.handlePayrollRegister,
	} {
		rec := payrollCall(t, h, http.MethodGet, "/"+name+"?month="+payrollWageMonth.String(), companyID, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET /%s for a month with no run = %d, want 404 (%s)", name, rec.Code, rec.Body.String())
		}
	}
}

// keysOf renders an archive's member names for an error message.
func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestUnpostedMonthIsMachineReadableAndLegible is D112's backend half.
//
// ⛔ THE 404 BODY HAD NOTHING A SCREEN COULD SWITCH ON, so the register screen
// rendered the server's sentence verbatim and an ordinary empty month read as a
// crash:
//
//	payrolldb: payroll register for 2026-09: payrolldb: pay run not found:
//	no live pay run for 2026-09
//
// Measured on the wire at badea6f. The package name appears TWICE, the sentinel
// is wrapped inside its own text, and "you have not run September payroll yet"
// — an ordinary state on a screen whose whole purpose is a month that may not
// have been run — is nowhere in it.
//
// Two things are asserted, and they are different things:
//
//	(1) a MACHINE-READABLE code, so the screen can tell this apart from a real
//	    failure without matching on prose. `{"error":…,"code":…}` is the house
//	    two-key shape — see writeDraftAlreadyAccepted, STEPUP_REQUIRED, LOCKED.
//	(2) a LEGIBLE sentence, because the export routes still show it to a human.
func TestUnpostedMonthIsMachineReadableAndLegible(t *testing.T) {
	s := payrollServer(t)
	companyID, _ := payrollCompany(t, s)

	for name, h := range map[string]http.HandlerFunc{
		"payroll-pf-ecr":     s.handlePayrollPFECR,
		"payroll-esi-return": s.handlePayrollESIReturn,
		"payroll-register":   s.handlePayrollRegister,
	} {
		rec := payrollCall(t, h, http.MethodGet, "/"+name+"?month="+payrollWageMonth.String(), companyID, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET /%s for a month with no run = %d, want 404 (%s)", name, rec.Code, rec.Body.String())
		}
		body := payrollJSON(t, rec)

		code, _ := body["code"].(string)
		if code != "NO_PAY_RUN" {
			t.Errorf("GET /%s: 404 body carries code %q, want %q — without it the screen has "+
				"only the prose to switch on, which is how an empty month came to look like a crash",
				name, code, "NO_PAY_RUN")
		}

		msg, _ := body["error"].(string)
		if strings.Count(msg, "payrolldb:") > 0 {
			t.Errorf("GET /%s: the message names its own Go package %d time(s): %q",
				name, strings.Count(msg, "payrolldb:"), msg)
		}
		// The control: trimming must not eat the facts. The month is the one
		// thing the operator needs back.
		if !strings.Contains(msg, payrollWageMonth.String()) {
			t.Errorf("GET /%s: the message no longer names the month: %q", name, msg)
		}
	}
}
