package server

// auth_print_service_test.go — the SERVICE-PRINCIPAL boundary on the print
// surface (finding C6).
//
// ===========================================================================
// WHAT THIS FILE IS FOR
// ===========================================================================
// companyAuthMiddleware has a fast path for service principals: a token whose
// subject begins "svc:" is granted access to whatever company the PATH names,
// with no membership row, no routeAction mapping and no PDP evaluation. Task D
// created that path deliberately, and two callers need it:
//
//	statutoryd    POST /vouchers/{id}/external-reference  (pin an IRN on a
//	              voucher it is not a member of) and GET /vouchers/{id}
//	offline sync  POST /sync/commit, POST /import/tally-xml
//
// Neither of those is a document. Track T1 then mounted the PRINT surface —
// including the payroll register and the per-employee payslip — inside the same
// {companyID} subtree, and the fast path handed a sync credential the finished
// PDF of the wage bill in any company it could name a UUID for. Migration 0038
// exists precisely so that a later administrator's convenience cannot reach the
// wage bill; a service token reached it with no rule at all, and there was no
// rule an owner could write to stop it, because the PDP never ran.
//
// THE BOUNDARY DRAWN, and it is narrow on purpose:
//
//	action Print       -> 403 for every service principal, always. Printing is
//	                     taking a document out of the building; no integrator
//	                     needs it, and nothing in the tree asks for it.
//	unmapped sub-path  -> 403, the same default-DENY a person gets. The fast
//	                     path used to skip routeAction entirely, so an
//	                     unregistered {companyID} sub-path reached whatever chi
//	                     did next instead of being refused.
//	everything else    -> unchanged. A service principal keeps the synthetic
//	                     full access Task D gave it (Create/Alter/Display), so
//	                     the freeze hook, sync commit and Tally-XML import are
//	                     untouched.
//
// Giving service principals a real ACL identity — a level, rules, a cutoff — is
// a Task-D-sized change and is NOT attempted here.
//
// It is an IN-PACKAGE test (package server) because it reuses newPrintFixture
// from handler_print_test.go, which needs s.payroll to lay down a salary
// structure that has no HTTP surface in this wave. Everything ASSERTED goes
// through s.Router() with a real Bearer token, so the middleware is in the path.

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/authn"
	"github.com/khata/cored/internal/printing"
)

// svcToken mints a dev token for a SERVICE subject ("svc:<name>"), the shape
// statutoryd and the sync worker present. It goes through /auth/token so the
// token is signed and verified exactly as a real one is.
func svcToken(t *testing.T, s *Server, name string) string {
	t.Helper()
	subject := authn.ServicePrefix + name
	tok := aclToken(t, s, subject)
	// Guard against the test passing vacuously if the prefix ever changes: if
	// this subject is not a service principal, the fast path is not the code
	// under test at all.
	if !(&authn.Claims{Subject: subject}).IsService() {
		t.Fatalf("subject %q is not recognised as a service principal, so this test "+
			"exercises the ordinary member path and proves nothing about the svc fast path",
			subject)
	}
	return tok
}

// svcGet issues one authenticated GET and returns the raw recorder, so PDF bytes
// survive (aclRequest only decodes JSON).
func svcGet(t *testing.T, s *Server, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	return rec
}

// svcLeakedDocument describes a print response in DOMAIN terms for a failure
// message: what a service credential was actually handed. It returns "" when the
// response is not a document.
func svcLeakedDocument(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if rec.Header().Get("Content-Type") != "application/pdf" {
		return ""
	}
	body := rec.Body.Bytes()
	if !strings.HasPrefix(string(body), "%PDF-") {
		return ""
	}
	lines, err := printing.ExtractText(body)
	if err != nil {
		return "a PDF of " + strconv.Itoa(len(body)) + " bytes whose text could not be extracted"
	}
	return strings.Join(lines, " | ")
}

// ---------------------------------------------------------------------------
// 1. THE WAGE BILL.
// ---------------------------------------------------------------------------

// TestASyncCredentialNobodyThinksOfAsAPersonReadEveryEmployeesSalary is the gate
// of this fix.
//
// A "svc:" token is not a person: it has no membership, no security level and no
// entry in the members list an owner reviews. It exists so statutoryd can pin an
// IRN and so an offline client can commit. If it can also GET
// /print/reports/payroll-register, then every employee's gross, EPF, TDS and net
// pay leaves the building as a finished PDF, in ANY company whose UUID the holder
// can name — and no rule an administrator writes can stop it, because the PDP
// never runs on that path.
//
// The oracle is the RESPONSE BODY, not just the status: a 200 that is genuinely a
// PDF is reported with the wage-bill text it disclosed, so the failure says what
// was leaked rather than "want 403, got 200".
func TestASyncCredentialNobodyThinksOfAsAPersonReadEveryEmployeesSalary(t *testing.T) {
	s := payrollServer(t)
	fx := newPrintFixture(t, s)
	svc := svcToken(t, s, "statutoryd")

	for _, tc := range []struct{ what, path string }{
		{
			"the payroll register — every employee's pay in one document",
			fx.base + "/print/reports/payroll-register?month=" + fx.payrollMonth,
		},
		{
			"a named employee's payslip",
			fx.base + "/print/reports/payroll-payslip?month=" + fx.payrollMonth +
				"&employee_id=" + fx.employeeID,
		},
	} {
		rec := svcGet(t, s, tc.path, svc)
		if leaked := svcLeakedDocument(t, rec); leaked != "" {
			t.Errorf("a service credential printed %s: the response is a finished PDF "+
				"containing %q. A svc: token has no membership, no security level and no "+
				"rule an owner can write against it; it must never be handed the wage bill.",
				tc.what, leaked)
			continue
		}
		if rec.Code != http.StatusForbidden {
			t.Errorf("a service credential asking to print %s = %d, want 403; the print "+
				"surface must be closed to service principals (body %.200s)",
				tc.what, rec.Code, rec.Body.String())
		}
	}
}

// ---------------------------------------------------------------------------
// 2. THE COMPANY'S OWN DOCUMENTS.
// ---------------------------------------------------------------------------

// TestASyncCredentialCouldTakeTheCompanysInvoicesOutOfTheBuildingAsPdfs is the
// same hole on the voucher print route. A printed sales invoice carries the
// party, the address, the GSTIN and the amount; the printed reports carry the
// trial balance and the stock position with its cost values. All of it is
// authority a "print" action exists to govern, and none of it was governed for a
// service principal.
func TestASyncCredentialCouldTakeTheCompanysInvoicesOutOfTheBuildingAsPdfs(t *testing.T) {
	s := payrollServer(t)
	fx := newPrintFixture(t, s)
	svc := svcToken(t, s, "offline-sync")

	for _, tc := range []struct{ what, path string }{
		{"a sales invoice", fx.base + "/print/vouchers/sales/" + fx.salesID},
		{"the receipt it did not issue", fx.base + "/print/vouchers/receipt/" + fx.receiptID},
		{"the trial balance", fx.base + "/print/reports/trial-balance?as_on=2026-07-31"},
	} {
		rec := svcGet(t, s, tc.path, svc)
		if leaked := svcLeakedDocument(t, rec); leaked != "" {
			t.Errorf("a service credential printed %s as a PDF containing %q; printing is "+
				"an authority the PDP governs, and the svc fast path skipped the PDP entirely",
				tc.what, leaked)
			continue
		}
		if rec.Code != http.StatusForbidden {
			t.Errorf("a service credential asking to print %s = %d, want 403 (body %.200s)",
				tc.what, rec.Code, rec.Body.String())
		}
	}
}

// ---------------------------------------------------------------------------
// 3. DEFAULT-DENY REACHES THE SERVICE PRINCIPAL TOO.
// ---------------------------------------------------------------------------

// TestAPathNobodyMappedIsRefusedForASyncCredentialExactlyAsForAPerson pins the
// second half of the fix. routeAction default-DENIES any {companyID} sub-path it
// does not map, and that is the property the whole subtree's safety rests on: a
// route mounted without a mapping is refused rather than served unauthorized.
// The svc fast path never called routeAction, so a service principal was the one
// caller for whom that guarantee did not hold — every future route would be born
// reachable by it.
//
// ⛔ THIS TEST ASSERTED THE LITERAL 403 AND WAS PINNING D40 ALONGSIDE ITS OWN
// PROPERTY. D40's fix makes a path the router mounts NOTHING at answer 404 (a
// path it mounts under another method, 405), and all three paths below are of
// that kind — so a literal 403 here demanded the very confusion D40 removed:
// "never built" reading as "not permitted".
//
// It now asserts what it was always ABOUT, which is strictly stronger than the
// old check and cannot be satisfied by the regression it was built to catch:
//
//	(1) PARITY — the service credential gets the SAME answer as the Owner. The
//	    bug was an ASYMMETRY (svc served where a person was refused), so parity
//	    is the property, not the number it happens to be.
//	(2) NOT SERVED — neither principal gets a 2xx. A svc token that started
//	    being served here would break parity AND this clause together.
//
// The 403 arm itself has not gone anywhere and is pinned on its own, over a path
// the router really does mount, in
// TestAMountedButUnmappedRouteStillFailsClosed (auth_unmapped_route_shape_test.go).
func TestAPathNobodyMappedIsRefusedForASyncCredentialExactlyAsForAPerson(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	svc := svcToken(t, s, "statutoryd")
	base := "/v1/companies/" + companyID

	for _, path := range []string{
		base + "/not-a-real-route",
		base + "/print/reports/not-a-report",
		base + "/print/vouchers/not-a-class/" + uuid.NewString(),
	} {
		// The control: a real Owner is not served this path either. That is what
		// makes the svc leg below a statement about the credential rather than
		// about the route.
		recOwner := svcGet(t, s, path, ownerTok)
		if recOwner.Code < 400 {
			t.Fatalf("control: an Owner on the unmapped path %s = %d, want a refusal — the "+
				"default-deny this test compares against is not in force, so the svc leg "+
				"below would prove nothing", path, recOwner.Code)
		}
		rec := svcGet(t, s, path, svc)
		if rec.Code < 400 {
			t.Errorf("an unmapped sub-path %s SERVED to a service credential = %d; a route nobody "+
				"mapped must be refused for a sync credential exactly as it is for a person, or "+
				"every route added after this one is born reachable by it (body %.200s)",
				path, rec.Code, rec.Body.String())
			continue
		}
		if rec.Code != recOwner.Code {
			t.Errorf("an unmapped sub-path %s answers %d for a service credential but %d for an "+
				"Owner. The refusal must not DEPEND on the credential: an asymmetry here is the "+
				"exact shape of the bug this test exists for (svc body %.200s)",
				path, rec.Code, recOwner.Code, rec.Body.String())
		}
	}
}

// ---------------------------------------------------------------------------
// 4. WHAT THE FIX MUST NOT BREAK.
// ---------------------------------------------------------------------------

// TestTheFreezeHookAndOfflineSyncStillWorkForAServiceCredential is the
// counterweight. The fix is only correct if it is narrow: statutoryd pins an IRN
// on a voucher in a company it is not a member of, and the offline client commits
// and imports. If any of these regress, e-invoicing and offline sync are broken
// in the field, which is a worse outcome than the leak.
func TestTheFreezeHookAndOfflineSyncStillWorkForAServiceCredential(t *testing.T) {
	s := payrollServer(t)
	fx := newPrintFixture(t, s)
	svc := svcToken(t, s, "statutoryd")

	// (a) The company master. verify-tenancy.mts asserts a svc principal reads
	// company A with no membership; that contract is unchanged.
	rec, _ := aclRequest(t, s, http.MethodGet, fx.base, svc, nil)
	if rec.Code != http.StatusOK {
		t.Errorf("a service credential reading the company master = %d, want 200 — Task D's "+
			"scoped full access for integrators has been narrowed too far (%s)",
			rec.Code, rec.Body.String())
	}

	// (b) GET the voucher statutoryd is about to sign (cored_client.GetVoucher).
	rec, _ = aclRequest(t, s, http.MethodGet, fx.base+"/vouchers/"+fx.salesID, svc, nil)
	if rec.Code != http.StatusOK {
		t.Errorf("statutoryd reading the voucher it must sign = %d, want 200 — e-invoicing "+
			"cannot fetch the invoice it submits to the IRP (%s)", rec.Code, rec.Body.String())
	}

	// (c) THE FREEZE HOOK: pin the IRN. This is the whole reason the fast path
	// exists (cored_client.RegisterExternalReference).
	rec, _ = aclRequest(t, s, http.MethodPost,
		fx.base+"/vouchers/"+fx.salesID+"/external-reference", svc, map[string]any{
			"kind": "IRN", "ref_value": "IRN-C6-" + uuid.NewString()[:8], "created_by": "statutoryd",
		})
	if rec.Code != http.StatusCreated {
		t.Errorf("statutoryd pinning an IRN = %d, want 201 — the freeze hook is the caller "+
			"the svc fast path was built for and it must not be collateral damage (%s)",
			rec.Code, rec.Body.String())
	}

	// (d) The trial balance as JSON: a DISPLAY, still open to a service principal.
	// Only Print was closed, and this is the assertion that says so.
	rec, _ = aclRequest(t, s, http.MethodGet, fx.base+"/trial-balance?as_on=2026-07-31", svc, nil)
	if rec.Code != http.StatusOK {
		t.Errorf("a service credential reading the trial balance as JSON = %d, want 200 — "+
			"this fix closes PRINT, not DISPLAY (%s)", rec.Code, rec.Body.String())
	}

	// (e) OFFLINE SYNC. An empty batch is a legitimate no-op commit, and it must
	// answer 200 with an outcome list — not merely "something other than 403".
	// A 403 here means offline sync is dead in the field.
	rec, _ = aclRequest(t, s, http.MethodPost, fx.base+"/sync/commit", svc, map[string]any{
		"device_id": uuid.NewString(), "vouchers": []any{},
	})
	if rec.Code != http.StatusOK {
		t.Errorf("the offline sync commit path answered a service credential %d, want 200 "+
			"(%s) — the fix must close printing only, never the sync seam",
			rec.Code, rec.Body.String())
	}

	// (f) THE TALLY-XML IMPORT, the other integrator path. It takes RAW XML in the
	// body (not JSON), so it needs its own request; a real two-ledger opening
	// envelope is posted so a 200 means the import actually RAN under the service
	// principal's company scope rather than merely getting past the middleware.
	const openingXML = `<ENVELOPE><BODY><DESC><STATICVARIABLES>` +
		`<SVCURRENTCOMPANY>C6 Import Co</SVCURRENTCOMPANY></STATICVARIABLES></DESC><DATA>` +
		`<TALLYMESSAGE><LEDGER NAME="C6 Imported Capital"><PARENT>Capital Account</PARENT>` +
		`<OPENINGBALANCE>100000.00</OPENINGBALANCE></LEDGER></TALLYMESSAGE>` +
		`<TALLYMESSAGE><LEDGER NAME="C6 Imported Cash"><PARENT>Cash-in-Hand</PARENT>` +
		`<OPENINGBALANCE>-100000.00</OPENINGBALANCE></LEDGER></TALLYMESSAGE>` +
		`</DATA></BODY></ENVELOPE>`
	req := httptest.NewRequest(http.MethodPost,
		fx.base+"/import/tally-xml?created_by=svc-importer", strings.NewReader(openingXML))
	req.Header.Set("Authorization", "Bearer "+svc)
	req.Header.Set("Content-Type", "application/xml")
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("the Tally-XML import answered a service credential %d, want 200 (%s) — "+
			"integrators import with a svc: token and the fix must not reach them",
			rec.Code, rec.Body.String())
	}
	for _, want := range []string{"C6 Imported Capital", "C6 Imported Cash"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("the Tally-XML import under a service credential did not report ensuring "+
				"the ledger %q the envelope names (%s) — a 200 that imported nothing would make "+
				"this leg vacuous", want, rec.Body.String())
		}
	}
}

// ---------------------------------------------------------------------------
// 5. THE PERSON'S PATH IS UNTOUCHED.
// ---------------------------------------------------------------------------

// TestAnOrdinaryOwnerStillPrintsSoTheDemoGateIsUnaffected guards the dev-token
// flow the demo and e2e scripts use. frontend/scripts/auth.mts mints its token
// through POST /auth/token for an ORDINARY subject ("e2e-owner-<uuid>"), and the
// browser fixture uses "browser-dev-owner" — never a "svc:" one — so both take
// the member path and keep printing, and verify-demo.mts's print gate (which
// asserts the BYTES of a sales invoice and of the Trial Balance) is untouched.
// Without this leg a fix that accidentally widened the refusal to every token
// would still look green on the four tests above.
func TestAnOrdinaryOwnerStillPrintsSoTheDemoGateIsUnaffected(t *testing.T) {
	s := payrollServer(t)
	fx := newPrintFixture(t, s)

	for _, demoSubject := range []string{"e2e-owner-" + uuid.NewString(), "browser-dev-owner", "demo-data-entry"} {
		if (&authn.Claims{Subject: demoSubject}).IsService() {
			t.Fatalf("the demo subject %q is being treated as a service principal, which "+
				"would take the whole demo print gate down the svc fast path and refuse it",
				demoSubject)
		}
	}

	rec := svcGet(t, s, fx.base+"/print/vouchers/receipt/"+fx.receiptID, fx.ownerTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("an Owner printing the receipt = %d, want 200 — the person's print path has "+
			"been broken by the service-principal fix (body %.200s)", rec.Code, rec.Body.String())
	}
	if got := svcLeakedDocument(t, rec); got == "" {
		t.Fatalf("an Owner printing the receipt did not get a PDF back (Content-Type %q)",
			rec.Header().Get("Content-Type"))
	}
	rec = svcGet(t, s, fx.base+"/print/reports/payroll-register?month="+fx.payrollMonth,
		fx.ownerTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("an Owner printing the payroll register = %d, want 200 — the register must "+
			"stay printable by the person who runs payroll (body %.200s)",
			rec.Code, rec.Body.String())
	}
}
