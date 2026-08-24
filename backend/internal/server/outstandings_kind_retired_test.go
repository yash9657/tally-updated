package server

// The retirement of `?kind=PAYABLE` on the two ageing report doors (D97).
//
// ⛔ THE ROW. `/outstandings` is decided as `report:outstandings` and
// `/print/reports/outstandings` as `print report:outstandings`. Serving the
// PAYABLES book from either handed a level that had been refused
// `report:bills-payable` the very same figures under the other name — measured on
// the wire in `drive-l24-d94.mjs` as an explicit arm. The parameter existed only
// because the bill-wise picker had no other way to ask; it now has one
// (`/vouchers/{class}/open-bills/{ledgerID}`), so nothing needs it.
//
// ⚠️ WHY THE ANSWER IS A REDIRECT AND NOT A 400 OR A 403. A stale bookmark should
// be an ANSWER — that is `handleBillsPayable`'s own recorded call. The follow-up
// request is a NEW request over a DIFFERENT path, so routeAction decides it as
// `report:bills-payable` and the middleware refuses it there if the level may not
// have it. The PDP stays in exactly one place, which is what makes it auditable.
//
// ⚠️ THESE RUN AGAINST A ZERO-VALUE Server ON PURPOSE. Both redirects are decided
// before a row is read, so no pool and no reporting service is touched. That is
// itself the assertion: a refusal that had to query the books first would have
// already built the answer it declines to give.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func kindRequest(t *testing.T, pattern, query string) (*httptest.ResponseRecorder, *http.Request, string) {
	t.Helper()
	companyID := uuid.NewString()
	path, rctx := synthPath(pattern, companyID)
	req := httptest.NewRequest(http.MethodGet, path+query, nil)
	req = req.WithContext(withRouteCtx(req, rctx))
	return httptest.NewRecorder(), req, companyID
}

// TestPayableKindIsRedirectedToItsOwnName is the row. Before the split this
// answered 200 with the payables book under the receivables rule.
func TestPayableKindIsRedirectedToItsOwnName(t *testing.T) {
	for _, spelling := range []string{"PAYABLE", "payable"} {
		w, req, companyID := kindRequest(t,
			"/v1/companies/{companyID}/outstandings", "?kind="+spelling+"&as_on=2026-03-31")
		(&Server{}).handleOutstandings(w, req)

		if w.Code != http.StatusPermanentRedirect {
			t.Fatalf("GET /outstandings?kind=%s = %d, want %d — any 2xx here is the payables "+
				"book served from under report:outstandings, which IS the row",
				spelling, w.Code, http.StatusPermanentRedirect)
		}
		want := "/v1/companies/" + companyID + "/bills-payable?as_on=2026-03-31"
		if got := w.Header().Get("Location"); got != want {
			t.Errorf("Location = %q, want %q — the as_on is the caller's QUESTION and dropping "+
				"it answers a different one; the kind is dropped because the target route IS "+
				"the kind", got, want)
		}
	}
}

// TestPrintedPayableKindIsRedirectedToo. Closing the JSON door and leaving the
// PAPER one open is §5.19 exactly: a refusal fully present and fully wrong, with
// the same rows on a filed sheet.
func TestPrintedPayableKindIsRedirectedToo(t *testing.T) {
	w, req, companyID := kindRequest(t,
		"/v1/companies/{companyID}/print/reports/outstandings", "?kind=PAYABLE")
	if _, built := (&Server{}).printOutstandings(req.Context(), w, req, uuid.Nil); built {
		t.Fatal("printOutstandings BUILT a table for ?kind=PAYABLE — the paper door is still open")
	}
	if w.Code != http.StatusPermanentRedirect {
		t.Fatalf("GET /print/reports/outstandings?kind=PAYABLE = %d, want %d",
			w.Code, http.StatusPermanentRedirect)
	}
	want := "/v1/companies/" + companyID + "/print/reports/bills-payable"
	if got := w.Header().Get("Location"); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

// TestReceivablesIsUntouchedAndAnInvalidKindStill400s is the CONTROL PAIR, and
// without it the two above prove only that SOMETHING changed. The receivables
// read must reach the report body exactly as before (it panics on the zero-value
// Server's nil reporting service, which is the proof it got that far and did NOT
// redirect), and a misspelled kind must still be the 400 it always was rather
// than a redirect to the payables book.
func TestReceivablesIsUntouchedAndAnInvalidKindStill400s(t *testing.T) {
	for _, query := range []string{"", "?kind=RECEIVABLE", "?kind=receivable"} {
		w, req, _ := kindRequest(t, "/v1/companies/{companyID}/outstandings", query)
		func() {
			// ⚠️ THE PANIC IS THE PASS. A zero-value Server has no reporting service,
			// so reaching the report BODY is exactly what blows up here — which is the
			// proof this read was not diverted. The assertion is on the recorder either
			// way: a receivables read must never answer a redirect.
			defer func() {
				_ = recover()
				if w.Code == http.StatusPermanentRedirect {
					t.Errorf("GET /outstandings%s REDIRECTED; the receivables ageing is not "+
						"the row and must be untouched", query)
				}
				if loc := w.Header().Get("Location"); loc != "" {
					t.Errorf("GET /outstandings%s set Location %q", query, loc)
				}
			}()
			(&Server{}).handleOutstandings(w, req)
		}()
	}

	w, req, _ := kindRequest(t, "/v1/companies/{companyID}/outstandings", "?kind=BOTH")
	(&Server{}).handleOutstandings(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("GET /outstandings?kind=BOTH = %d, want 400 — a misspelled kind must stay a "+
			"stated error, not become a redirect to a report the caller did not ask for", w.Code)
	}
}
