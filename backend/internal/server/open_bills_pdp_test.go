package server

// The PDP mapping of the bill-wise picker's own door (D97), and the retirement
// of the query parameter it used to ride.
//
// It lives in the INTERNAL test package because routeAction and synthPath do.
//
// ⛔ THE ROW THIS PINS. Until `/vouchers/{class}/open-bills/{ledgerID}` existed, a
// clerk listing a supplier's open bills to settle one called
// `GET /outstandings?kind=PAYABLE` — the RECEIVABLES report's door. So a level
// carrying `disallow display report:bills-payable` was refused the payables ageing
// and could still read every figure in it: the rule governed the DOOR, not the
// bytes. routeAction sees the PATH and never the query, which is why the fix had
// to be a route and could not be a mapping change.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// TestOpenBillsIsAuthorisedAsTheVoucherClass is the decision itself. The picker's
// read is authorised as `display <class>` — the authority the clerk keying that
// voucher ALREADY holds under migration 0011 — and NOT as a report name and not as
// a token of its own. A report mapping would re-create the row (the payables book
// under some report rule); a token of its own would need a grant migration, and a
// hand-written default-deny level that the migration missed would lose its picker
// on upgrade and post every settlement ON ACCOUNT.
func TestOpenBillsIsAuthorisedAsTheVoucherClass(t *testing.T) {
	for _, cls := range []string{
		"sales", "purchase", "receipt", "payment", "contra", "journal",
		"credit-note", "debit-note",
	} {
		companyID := uuid.NewString()
		path, rctx := synthPath(
			"/v1/companies/{companyID}/vouchers/"+cls+"/open-bills/{ledgerID}", companyID)
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req = req.WithContext(withRouteCtx(req, rctx))

		act, res, resName, mapped := routeAction(req)
		if !mapped {
			t.Fatalf("GET /vouchers/%s/open-bills/{ledgerID} is not mapped by the PDP at all — "+
				"it 403s for EVERY level, so the picker is gone and every settlement lands "+
				"ON ACCOUNT", cls)
		}
		if string(res) != cls || resName != "" {
			t.Errorf("GET /vouchers/%s/open-bills maps to %q (name %q), want the voucher class "+
				"%q with no name — a report mapping puts the picker back under a report rule, "+
				"which IS the row", cls, res, resName, cls)
		}
		if act.IsWrite() {
			t.Errorf("GET /vouchers/%s/open-bills maps to a WRITE action (%v); listing open "+
				"bills would inherit the back-dated cutoff and the WebAuthn step-up", cls, act)
		}
	}
}

// TestOpenBillsRefusesANonClassSegment pins the FAILURE DIRECTION. routeAction
// default-denies what it does not recognise, and `isVoucherClass` is what stands
// between "the class in the path is an authority" and "any word in the path is an
// authority". A segment that is not a class must fall through to unmapped (a 403),
// never to some neighbouring arm's resource.
func TestOpenBillsRefusesANonClassSegment(t *testing.T) {
	for _, seg := range []string{"outstandings", "bills-payable", "report", "not-a-class"} {
		companyID := uuid.NewString()
		path, rctx := synthPath(
			"/v1/companies/{companyID}/vouchers/"+seg+"/open-bills/{ledgerID}", companyID)
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req = req.WithContext(withRouteCtx(req, rctx))

		if _, res, _, mapped := routeAction(req); mapped {
			t.Errorf("GET /vouchers/%s/open-bills is MAPPED, to %q — a path segment that is not "+
				"a voucher class must default-deny, or the class stops being the authority",
				seg, res)
		}
	}
}

// TestTheVoucherGetsStillMapAsBefore is the CONTROL, and without it the two tests
// above prove nothing about what they did not break. The new four-segment arm sits
// inside the same `GET /vouchers/` case as the single-voucher read and its edit
// log; both must still map to `display voucher` exactly as they did.
func TestTheVoucherGetsStillMapAsBefore(t *testing.T) {
	for _, pattern := range []string{
		"/v1/companies/{companyID}/vouchers/{voucherID}",
		"/v1/companies/{companyID}/vouchers/{voucherID}/history",
	} {
		companyID := uuid.NewString()
		path, rctx := synthPath(pattern, companyID)
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req = req.WithContext(withRouteCtx(req, rctx))

		act, res, _, mapped := routeAction(req)
		if !mapped || string(res) != "voucher" || act.IsWrite() {
			t.Errorf("GET %s maps to (%v, %q, mapped=%v), want (display, \"voucher\", true)",
				pattern, act, res, mapped)
		}
	}
}
