package server

// The PDP mapping of the section-vocabulary route. It lives in the INTERNAL test
// package because routeAction and synthPath do; the response test that goes with
// it is handler_tds_sections_test.go, in the external one.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// TestTDSSectionsIsAMasterNotAReport pins the ACCESS DECISION, which is the
// whole reason this route is mapped where it is. The DATA ENTRY level keys the
// vouchers that carry a deduction and is DENIED report:tds-register by migration
// 0068 (that register is every supplier paid, with amounts). If this list were
// mapped as a report it would be denied to exactly the operator who needs it,
// and D3 would stay open behind a different error.
func TestTDSSectionsIsAMasterNotAReport(t *testing.T) {
	companyID := uuid.NewString()
	path, rctx := synthPath("/v1/companies/{companyID}/tds-sections", companyID)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req = req.WithContext(withRouteCtx(req, rctx))

	act, res, resName, mapped := routeAction(req)
	if !mapped {
		t.Fatal("GET /tds-sections is not mapped by the PDP at all — it would 403 for everyone")
	}
	if string(res) != "master" {
		t.Errorf("GET /tds-sections maps to resource %q (name %q), want \"master\" — a report "+
			"mapping denies the picker to the Data Entry level that keys the deduction",
			res, resName)
	}
	if act.IsWrite() {
		t.Errorf("GET /tds-sections maps to a WRITE action (%v); it would inherit the "+
			"back-dated cutoff and the WebAuthn step-up for a statutory rate table", act)
	}
}
