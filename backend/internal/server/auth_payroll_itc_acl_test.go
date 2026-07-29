package server

// auth_payroll_itc_acl_test.go — the ACL half of the Track 2 + Track 3 wiring,
// asserted THROUGH THE REAL ROUTER against the REAL seeded security levels.
//
// ===========================================================================
// WHAT THIS FILE IS FOR, AND WHY THE OTHER TESTS DO NOT COVER IT
// ===========================================================================
// route_vocabulary_test.go proves the mapping table is CLOSED: every route is
// mapped, and every token it emits is one an administrator could name in a rule.
// It does not — cannot — prove that the rules migrations 0033 and 0035 already
// seeded actually REFUSE anybody, because it never evaluates the PDP.
//
// That gap is exactly where the money is. Migration 0011 seeds the 'Data Entry'
// level a COARSE `allow display report`, and internal/authz's resource matcher
// matches a bare `report` rule against a request for ANY `report:NAME`. So the
// DEFAULT behaviour of adding a report to this product is to hand it to every
// operator in every tenant, by inheritance, without anybody deciding to. Seven
// reports mount in this wave, and five of them are somebody's salary.
//
// So the oracle here is the seeded level itself, driven over HTTP:
//
//	Owner  (default_access 'full')  -> 200 on all seven
//	Data Entry (seeded, 0033+0035)  -> 403 on all seven
//
// BOTH HALVES ARE LOAD-BEARING AND THEY FAIL IN OPPOSITE DIRECTIONS. A 403 for
// the Owner means the route is unmounted or the reportName()/routeAction entry
// is missing — routeAction default-denies unmapped {companyID} paths, so the
// feature would ship reachable by nobody. A 200 for Data Entry means the
// report:NAME is not being emitted (or the seeded disallow does not match it),
// and the coarse grant has silently handed over the payroll.
//
// ===========================================================================
// WHY IT IS AN IN-PACKAGE TEST
// ===========================================================================
// The Owner leg needs a POSTED PAY RUN, and the salary structure and statutory
// election have no HTTP surface in this wave (handler_payroll.go's header says
// so). Reaching s.payroll for those two setup calls requires package `server`,
// which is also where handler_payroll_test.go and route_vocabulary_test.go live.
// Everything ASSERTED on goes through s.Router() with a real Bearer token, so
// the middleware, the PDP and the seeded rules are all in the path.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/payroll"
	"github.com/khata/cored/internal/payroll/payrolldb"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
)

// ---------------------------------------------------------------------------
// In-package HTTP helpers.
//
// server_test.go has doRaw/mintToken already, but they are in package
// server_test and are not reachable from here. These are the same three moves
// (mint a dev token, create a company, invite+claim a member) written against
// s.Router() so this file can also touch s.payroll.
// ---------------------------------------------------------------------------

// aclRequest issues one in-process request with an optional Bearer token.
func aclRequest(t *testing.T, s *Server, method, path, token string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)

	var decoded map[string]any
	if rec.Body.Len() > 0 {
		// A statutory file endpoint answers application/zip; only decode JSON.
		if rec.Header().Get("Content-Type") == "application/json" {
			if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
				t.Fatalf("%s %s: decode %q: %v", method, path, rec.Body.String(), err)
			}
		}
	}
	return rec, decoded
}

// aclToken mints a dev token for a subject, with AMR webauthn so the Owner
// level's requires_webauthn step-up is satisfied on writes.
func aclToken(t *testing.T, s *Server, subject string) string {
	t.Helper()
	rec, body := aclRequest(t, s, http.MethodPost, "/auth/token", "", map[string]any{
		"subject": subject, "amr": []string{"webauthn"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("mint token %q = %d, want 200 (%s)", subject, rec.Code, rec.Body.String())
	}
	tok, _ := body["token"].(string)
	if tok == "" {
		t.Fatalf("mint token %q returned no token (%s)", subject, rec.Body.String())
	}
	return tok
}

// aclOwnerCompany creates a company THROUGH THE HTTP PATH, which is what runs
// seed_company_auth_defaults — the function migrations 0033 and 0035 extended.
// A company created straight through identity.CreateCompany has no security
// levels at all and would make every assertion in this file vacuous.
func aclOwnerCompany(t *testing.T, s *Server) (companyID, ownerTok string) {
	t.Helper()
	ownerTok = aclToken(t, s, "acl-owner-"+uuid.NewString()[:8])
	cid := uuid.New()
	companyID = cid.String()
	rec, _ := aclRequest(t, s, http.MethodPost, "/v1/companies", ownerTok, map[string]any{
		"id":            companyID,
		"name":          "ACL Co " + companyID[:8],
		"fy_start_date": "2026-04-01",
		"base_currency": "INR",
		"state_code":    "29",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create company = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	testdb.Cleanup(t, cid)
	return companyID, ownerTok
}

// aclSeededDataEntryToken invites a fresh subject at the SEEDED 'Data Entry'
// level (never a hand-written one) and claims the invite. The seeded level is
// the whole point: it is the level every tenant ships with, and its rule set is
// what migrations 0033 and 0035 wrote.
func aclSeededDataEntryToken(t *testing.T, s *Server, base, ownerTok string) string {
	t.Helper()
	subject := "acl-de-" + uuid.NewString()[:8]
	rec, _ := aclRequest(t, s, http.MethodPost, base+"/members/invites", ownerTok,
		map[string]any{"subject": subject, "security_level_name": "Data Entry"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite at Data Entry = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	tok := aclToken(t, s, subject)
	rec, body := aclRequest(t, s, http.MethodGet, "/v1/me", tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("claim invite = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	claimed, _ := body["claimed_invites"].([]any)
	if len(claimed) != 1 {
		t.Fatalf("claimed_invites = %v, want exactly one (%s)", claimed, rec.Body.String())
	}
	return tok
}

// ---------------------------------------------------------------------------
// The fixture: a company with a posted July-2026 pay run and two employees.
//
// TWO employees, because ONE cannot make all five payroll reports answer 200:
//
//	acl-ESI  BASIC 18,000  — under the Rs 21,000 ESI coverage ceiling, so the
//	                         ESIC monthly contribution return has a row to file;
//	                         owes no tax.
//	acl-TDS  BASIC 250,000 — far above the s.156 rebate limit, so tax is actually
//	                         DEDUCTED and the Form 138 Annexure I has a deductee.
//	                         Not ESI-covered.
//
// Both are provident-fund members at the ceiling, so the ECR has rows.
//
// NOTHING IN THIS FILE ASSERTS A PAYROLL FIGURE. The arithmetic is proven in
// internal/payroll and handler_payroll_test.go; here the run exists only so that
// a 200 is REACHABLE, and so a 403 can be attributed to the PDP rather than to
// an empty month.
// ---------------------------------------------------------------------------

type aclFixture struct {
	companyID string
	base      string
	ownerTok  string
	dataEntry string
	esiEmpID  string
	month     string
}

func newACLFixture(t *testing.T, s *Server) aclFixture {
	t.Helper()
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID
	cid := uuid.MustParse(companyID)
	cctx := db.WithCompany(context.Background(), cid)

	newEmployee := func(code, name, pan, uan, ip, basic string, esi bool) string {
		rec, body := aclRequest(t, s, http.MethodPost, base+"/employees", ownerTok, map[string]any{
			"code": code, "name": name, "doj": "2026-04-01", "designation": "Analyst",
			"pan": pan, "uan": uan, "esic_ip_number": ip,
			"bank_ifsc": "HDFC0001234", "bank_account_last4": "4321",
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST /employees (%s) = %d, want 201 — an Owner cannot even create the "+
				"employee master, so the route or its (create, employee) mapping is missing (%s)",
				code, rec.Code, rec.Body.String())
		}
		id, _ := body["employee_id"].(string)
		empID, err := uuid.Parse(id)
		if err != nil {
			t.Fatalf("employee_id %q is not a uuid (%s)", id, rec.Body.String())
		}
		// The structure and the election have no HTTP surface in this wave.
		if _, err := s.payroll.SetSalaryComponent(cctx, payrolldb.SalaryComponent{
			EmployeeID: empID, EffectiveFrom: "2026-04-01",
			Code: "BASIC", Type: payroll.ComponentBasic, Amount: decimal.RequireFromString(basic),
		}); err != nil {
			t.Fatalf("set salary component for %s: %v", code, err)
		}
		if _, err := s.payroll.SetStatutoryElection(cctx, payrolldb.StatutoryElection{
			EmployeeID: empID, EffectiveFrom: "2026-04-01",
			PFBase: payroll.PFBaseCeiling, EPSMember: true, ESIApplicable: esi,
			Regime: payroll.RegimeNew,
		}); err != nil {
			t.Fatalf("set statutory election for %s: %v", code, err)
		}
		return id
	}

	esiEmp := newEmployee("ACL-ESI-1", "Acl Esi Employee", "ABCPD1234E", "100200300411", "1234567811", "18000", true)
	_ = newEmployee("ACL-TDS-1", "Acl Tds Employee", "ABCPD1234F", "100200300412", "1234567812", "250000", false)

	month := "2026-07"
	rec, _ := aclRequest(t, s, http.MethodPost, base+"/vouchers/payroll", ownerTok, map[string]any{
		"wage_month": month,
		"created_by": "acl-test",
		"narration":  "Payroll for " + month,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /vouchers/payroll = %d, want 201 — an Owner cannot post a pay run, so the "+
			"route or the `payroll` voucher class in isVoucherClass is missing (%s)", rec.Code, rec.Body.String())
	}

	return aclFixture{
		companyID: companyID,
		base:      base,
		ownerTok:  ownerTok,
		dataEntry: aclSeededDataEntryToken(t, s, base, ownerTok),
		esiEmpID:  esiEmp,
		month:     month,
	}
}

// ---------------------------------------------------------------------------
// The gate.
// ---------------------------------------------------------------------------

// TestSeededDataEntryIsRefusedEveryPayrollAndITCReport is the disclosure gate
// for this whole wave.
//
// It drives the SEVEN report routes twice against the SAME live company: once
// as the Owner who runs payroll and signs the return, and once as an operator
// holding the security level every tenant ships with. The Owner must get 200 on
// all seven; the operator must get 403 on all seven.
//
// If the operator half regresses, the failure message says what that operator
// can now read. That is the only form in which this test is worth having: "403
// != 200" tells a future reader nothing about why anybody cared.
func TestSeededDataEntryIsRefusedEveryPayrollAndITCReport(t *testing.T) {
	s := payrollServer(t)
	f := newACLFixture(t, s)

	// The Form 138 particulars are NOT in the database — no TAN, no challan —
	// so every one of them is supplied on the query string. July 2026 is Q2 of
	// tax year 2026-27. The challan carries no amount, which means "derive it
	// from what was actually deducted", so the statement is self-consistent.
	form138 := "/payroll-form-138?tax_year=2026-27&quarter=Q2" +
		"&tan=BLRA12345B&deductor_pan=AAACD1234F&deductor_name=Acl+Test+Deductor" +
		"&form_number=24Q&file_creation_date=2026-08-05" +
		"&challan=2026-07,0221826,2026-08-07,12345"

	// CONTROL, and this file would be worthless without it. Every assertion
	// below expects 403 for the Data Entry token, and a token that is 403 on
	// EVERYTHING — an unclaimed invite, a level with no rules, a subject that is
	// not a member — would satisfy all seven and prove nothing. So first show the
	// same token reaching a report it is SUPPOSED to reach. The Trial Balance is
	// the right control: migration 0011's coarse `allow display report` is what
	// grants it, and that is precisely the grant the seven disallows below are
	// counterweighting.
	if rec, _ := aclRequest(t, s, http.MethodGet,
		f.base+"/trial-balance?as_on=2026-07-31", f.dataEntry, nil); rec.Code != http.StatusOK {
		t.Fatalf("CONTROL: seeded Data Entry GET /trial-balance = %d, want 200 — this token is refused "+
			"everything, so the 403s below are not evidence that the payroll and ITC disallows bite (%s)",
			rec.Code, rec.Body.String())
	}

	for _, tc := range []struct {
		report string // the report NAME, which is also its path segment
		query  string
		// discloses states, in plain words, what a Data Entry operator would be
		// able to read if this route stopped emitting its report:NAME.
		discloses string
	}{
		{
			report:    "payroll-register",
			query:     "/payroll-register?month=" + f.month,
			discloses: "a Data Entry operator can read every employee's gross, deductions and net",
		},
		{
			report:    "payroll-payslip",
			query:     "/payroll-payslip?month=" + f.month + "&employee_id=" + f.esiEmpID,
			discloses: "a Data Entry operator can read one named employee's whole pay, component by component",
		},
		{
			report:    "payroll-pf-ecr",
			query:     "/payroll-pf-ecr?month=" + f.month,
			discloses: "a Data Entry operator can download every employee's UAN and provident-fund wages for the month",
		},
		{
			report:    "payroll-esi-return",
			query:     "/payroll-esi-return?month=" + f.month,
			discloses: "a Data Entry operator can download every insured person's ESIC IP number, wages and last working day",
		},
		{
			report:    "payroll-form-138",
			query:     form138,
			discloses: "a Data Entry operator can download every employee's PAN, salary and tax deducted for the quarter",
		},
		{
			report:    "itc-reconciliation",
			query:     "/itc-reconciliation?from=2026-07-01&to=2026-07-31",
			discloses: "a Data Entry operator can read the whole supplier book — who we buy from, at what price, invoice by invoice",
		},
		{
			report:    "ims-inbox",
			query:     "/ims-inbox?return_period=072026",
			discloses: "a Data Entry operator can read the filer's working paper on which suppliers' invoices we think are wrong",
		},
	} {
		path := f.base + tc.query

		// The Owner leg. A 403 here is a WIRING failure, not an authorization
		// one: routeAction default-denies any unmapped {companyID} path, so the
		// report would be reachable by nobody at all.
		if rec, _ := aclRequest(t, s, http.MethodGet, path, f.ownerTok, nil); rec.Code != http.StatusOK {
			t.Errorf("Owner GET /%s = %d, want 200 — the Owner runs payroll and signs the return, so a "+
				"non-200 here means the route is unmounted or its reportName()/routeAction entry is "+
				"missing and NOBODY can reach this report (%s)",
				tc.report, rec.Code, rec.Body.String())
		}

		// The operator leg. This is the one that costs money if it regresses.
		if rec, _ := aclRequest(t, s, http.MethodGet, path, f.dataEntry, nil); rec.Code != http.StatusForbidden {
			t.Errorf("seeded Data Entry GET /%s = %d, want 403 — %s. Migration %s seeded "+
				"`disallow display report:%s` for exactly this, and it only bites if the route emits "+
				"report:%s; migration 0011's coarse `allow display report` matches every report:NAME "+
				"at specificity 1 and hands the report over the moment the name is not emitted (%s)",
				tc.report, rec.Code, tc.discloses, aclSeedingMigration(tc.report), tc.report, tc.report,
				rec.Body.String())
		}
	}
}

// aclSeedingMigration names the migration that seeded the disallow for a report,
// so the failure message points at the row to look at.
func aclSeedingMigration(report string) string {
	switch report {
	case "itc-reconciliation", "ims-inbox":
		return "0033"
	default:
		return "0035"
	}
}

// TestSeededDataEntryIsRefusedTheEmployeeMasterAndThePayRun covers the two BARE
// resource tokens this wave introduced on the payroll side.
//
// These are refused by a different mechanism from the reports above and the
// distinction is worth pinning, because it is the one that is easy to get wrong:
// a bare token no rule names is already denied by the seeded level's
// default_access of 'none', so the six rows migration 0035 wrote for `payroll`
// and `employee` change no answer TODAY. What they buy is survival of a later
// administrator's convenience — `allow display *` is a natural thing for an
// owner to write, it scores specificity 1, and silence would then hand over the
// PANs and the wage bill while an explicit disallow at specificity 2 would not.
//
// The assertion that would actually have caught the design mistake is the
// employee one: had `employees` been folded into isMasterPath, migration 0022's
// `allow display master` — which every seeded Data Entry level holds so the F9
// party picker works — would have carried the whole staff list with it.
func TestSeededDataEntryIsRefusedTheEmployeeMasterAndThePayRun(t *testing.T) {
	s := payrollServer(t)
	f := newACLFixture(t, s)

	// The Owner can list employees; the operator cannot.
	if rec, _ := aclRequest(t, s, http.MethodGet, f.base+"/employees", f.ownerTok, nil); rec.Code != http.StatusOK {
		t.Errorf("Owner GET /employees = %d, want 200 — an unmapped route default-denies before any "+
			"handler runs, so this is a routeAction gap (%s)", rec.Code, rec.Body.String())
	}
	if rec, _ := aclRequest(t, s, http.MethodGet, f.base+"/employees", f.dataEntry, nil); rec.Code != http.StatusForbidden {
		t.Errorf("seeded Data Entry GET /employees = %d, want 403 — a Data Entry operator can read the "+
			"PAN, UAN, ESIC IP number, bank IFSC and account last-4 of every person in the company. "+
			"That is somebody's identity, not somebody's margin, and it is what folding /employees "+
			"into isMasterPath would hand over via migration 0022's `allow display master` (%s)",
			rec.Code, rec.Body.String())
	}

	// Creating an employee is a write on the same token.
	if rec, _ := aclRequest(t, s, http.MethodPost, f.base+"/employees", f.dataEntry, map[string]any{
		"code": "ROGUE-1", "name": "Rogue Hire", "doj": "2026-07-01",
	}); rec.Code != http.StatusForbidden {
		t.Errorf("seeded Data Entry POST /employees = %d, want 403 — a Data Entry operator can put a "+
			"person on the staff list, and the next pay run pays them (%s)", rec.Code, rec.Body.String())
	}

	// The pay run itself.
	if rec, _ := aclRequest(t, s, http.MethodPost, f.base+"/vouchers/payroll", f.dataEntry, map[string]any{
		"wage_month": "2026-08", "created_by": "rogue",
	}); rec.Code != http.StatusForbidden {
		t.Errorf("seeded Data Entry POST /vouchers/payroll = %d, want 403 — whoever keys the sales "+
			"invoices can now decide what the company pays its staff, and the pay run's postings ARE "+
			"the wage bill (%s)", rec.Code, rec.Body.String())
	}
}

// TestSeededDataEntryIsRefusedTheThreeITCWrites covers the other three bare
// tokens: inward-statement, ims-action and gst-filing.
//
// Migration 0033 deliberately seeds NO rule for any of them, and its header says
// why: the seeded level's default_access is 'none', so a token no rule names is
// already denied. This test is what turns that sentence into something checkable
// — if any of the three were ever mapped to a token the level DOES hold (say
// `import`, which is the tempting one for the statement upload), the refusal
// would evaporate silently.
//
// The Owner leg is deliberately NOT asserted to be 200: an empty body or an
// unfiled period is a 4xx from the handler, and pinning which 4xx would be
// asserting handler_itc.go's contract from a file that does not own it. What is
// asserted is that the Owner is not 403 — i.e. the request REACHED the handler —
// which is the only thing the wiring is responsible for.
func TestSeededDataEntryIsRefusedTheThreeITCWrites(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID
	de := aclSeededDataEntryToken(t, s, base, ownerTok)

	for _, tc := range []struct {
		path, token, consequence string
		body                     any
	}{
		{
			path:  base + "/gstr2b-import?kind=GSTR2B",
			token: "inward-statement",
			body:  map[string]any{},
			consequence: "a Data Entry operator can supersede the month's GSTR-2B with a file of their " +
				"own, and every credit the return claims is matched against it",
		},
		{
			path:  base + "/ims-actions",
			token: "ims-action",
			body:  map[string]any{"document_id": uuid.NewString(), "action": "REJECT"},
			consequence: "a Data Entry operator can REJECT a supplier's genuine invoice, which silently " +
				"forfeits its input tax credit, or accept one that should not be accepted, which claims " +
				"credit that is not ours",
		},
		{
			path:  base + "/gst-filings",
			token: "gst-filing",
			body:  map[string]any{"form": "GSTR3B", "return_period": "072026"},
			consequence: "a Data Entry operator can declare the return filed, which stamps the IMS freeze " +
				"and makes every later IMS action count for nothing",
		},
	} {
		if rec, _ := aclRequest(t, s, http.MethodPost, tc.path, de, tc.body); rec.Code != http.StatusForbidden {
			t.Errorf("seeded Data Entry POST %s = %d, want 403 — %s. The refusal rests entirely on the "+
				"route emitting the %q resource, which the level holds no rule for and default_access "+
				"'none' therefore denies (%s)", tc.path, rec.Code, tc.consequence, tc.token, rec.Body.String())
		}
		if rec, _ := aclRequest(t, s, http.MethodPost, tc.path, ownerTok, tc.body); rec.Code == http.StatusForbidden {
			t.Errorf("Owner POST %s = 403 — the Owner's default_access is 'full', so a 403 means the "+
				"route is unmapped in routeAction and default-denied before any handler ran; this write "+
				"is reachable by nobody (%s)", tc.path, rec.Body.String())
		}
	}
}
