package server

// handler_itc_test.go exercises the ITC/IMS handler methods DIRECTLY, through
// httptest, because nothing is mounted yet: the serial wiring stage owns
// server.go, auth_middleware.go and authz/vocabulary.go, so there is no route to
// drive until it runs. That is why this is an IN-PACKAGE test (package server,
// not server_test) — the handler methods are unexported.
//
// WHAT IS TESTED HERE, AND WHY THESE CASES. Every case below is a REFUSAL, and
// each one guards a specific silently-wrong number:
//
//   - a multi-period window: only ONE period's GSTR-2B is ever loaded, so a
//     quarterly call reverses two thirds of the quarter's input tax credit in
//     table 4(B)(2) while returning 200 and looking entirely successful;
//   - a malformed as_on: as_on decides which IMS records have deem-accepted, so
//     falling back to now would answer for a different instant than was asked
//     about, changing table 4(A) with nothing on the wire to show for it;
//   - a scalar common credit: C2 and Tr are head-wise tax, and putting one
//     figure into one head reverses credit in a head the filer does not hold it
//     in — a wrong number in cash;
//   - the GSTR-3B backward-compatibility pin: with no statement_id the response
//     must still be the bare return, byte for byte.
//
// The four 400-path tests need NO DATABASE by construction: every refusal is
// evaluated before compliance/itc touches its TxRunner (Reconcile runs
// checkOnePeriod before any loader, deliberately, "because the failure mode is a
// plausible-looking report that reverses real credit rather than an obvious
// crash"). They therefore run against a Server whose itc service holds a nil
// runner: if a refusal ever stopped short-circuiting, the test panics rather
// than passing.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/compliance/gstr"
	"github.com/khata/cored/internal/compliance/itc"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/migrations"
)

// itcTestAppDSN is the application-role DSN. It is duplicated from
// server_test.go rather than shared because that file is in the EXTERNAL test
// package (server_test) and its consts are not visible here.
var itcTestAppDSN = testEnvDSN("DATABASE_URL", "postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable")

// noDBServer is a Server whose ITC service holds a NIL TxRunner.
//
// The nil runner is the assertion, not a shortcut: every test that uses this
// server expects the handler to refuse BEFORE the engine reaches the database.
// A regression that let one of these requests through would nil-panic here
// instead of quietly returning a report for the wrong window or the wrong
// instant.
func noDBServer() *Server { return &Server{itc: itc.NewService(nil)} }

// scoped builds a request carrying a chi route context with a {companyID}, the
// way the real router would, so scopeCtx resolves a tenant without the auth
// middleware.
func scoped(method, target string, body string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("companyID", uuid.NewString())
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// callErr runs h and returns the status plus the decoded `error` message.
func callErr(t *testing.T, h http.HandlerFunc, req *http.Request) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h(rec, req)
	var body map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("response body is not JSON (%q): %v", rec.Body.String(), err)
		}
	}
	msg, _ := body["error"].(string)
	return rec.Code, msg
}

// ---------------------------------------------------------------------------
// GET /itc-reconciliation
// ---------------------------------------------------------------------------

func TestITCReconciliationRequiresFromAndTo(t *testing.T) {
	srv := noDBServer()
	for _, tc := range []struct{ name, query string }{
		{"neither", ""},
		{"only from", "?from=2025-04-01"},
		{"only to", "?to=2025-04-30"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, msg := callErr(t, srv.handleITCReconciliation,
				scoped(http.MethodGet, "/v1/companies/x/itc-reconciliation"+tc.query, ""))
			if code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: a reconciliation with no period is not a report of "+
					"nothing, it is a request the server cannot answer — serving one would let a "+
					"caller believe a period reconciled clean when no period was ever named", code)
			}
			if !strings.Contains(msg, "from") || !strings.Contains(msg, "to") {
				t.Fatalf("error %q must name the missing from/to params", msg)
			}
		})
	}
}

// TestITCReconciliationRefusesAMultiPeriodWindow is the guard on the sentinel
// itc.ErrWindowNotOnePeriod. It must be a 400 that NAMES the resolved period,
// never a 500 and never a silent iteration over the months.
func TestITCReconciliationRefusesAMultiPeriodWindow(t *testing.T) {
	srv := noDBServer()
	code, msg := callErr(t, srv.handleITCReconciliation,
		scoped(http.MethodGet, "/v1/companies/x/itc-reconciliation?from=2025-04-01&to=2025-06-30", ""))

	if code != http.StatusBadRequest {
		t.Fatalf("a quarterly window returned %d, want 400. Only APRIL's GSTR-2B is ever loaded, "+
			"so every May and June purchase has nothing to match against, is classified "+
			"MISSING_IN_2B and is REVERSED IN FULL in table 4(B)(2): two thirds of the quarter's "+
			"input tax credit destroyed by a report that returned 200 and looked successful. A 500 "+
			"is equally wrong — it reads as 'the server broke, retry' when the refusal is the "+
			"designed contract", code)
	}
	for _, want := range []string{"042025", "052025", "062025", "4(B)(2)"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q; the refusal must name the resolved tax period "+
				"and the months whose credit would have been reversed, or the caller cannot tell "+
				"which months they still have to ask for", msg, want)
		}
	}
	if !strings.Contains(msg, "from=2025-04-01") || !strings.Contains(msg, "to=2025-04-30") {
		t.Errorf("error %q must show the exact one-month window that would have been accepted", msg)
	}
}

// TestITCReconciliationRefusesAMalformedAsOn pins that as_on is NEVER defaulted
// to now when it is present but unparseable.
func TestITCReconciliationRefusesAMalformedAsOn(t *testing.T) {
	srv := noDBServer()
	for _, raw := range []string{"2025-11-15", "yesterday", "1731628800"} {
		code, msg := callErr(t, srv.handleITCReconciliation,
			scoped(http.MethodGet,
				"/v1/companies/x/itc-reconciliation?from=2025-04-01&to=2025-04-30&as_on="+raw, ""))
		if code != http.StatusBadRequest {
			t.Fatalf("as_on=%q returned %d, want 400. Falling back to now would answer for a "+
				"DIFFERENT instant than the caller asked about: as_on decides which IMS records have "+
				"deem-accepted, so the credit in table 4(A) would change with nothing on the wire to "+
				"say it had", raw, code)
		}
		if !strings.Contains(msg, "as_on") || !strings.Contains(msg, "RFC3339") {
			t.Errorf("as_on=%q error %q must name the param and the wanted format", raw, msg)
		}
	}
}

// TestResolveAsOnNeverFallsBackToNow is the unit-level twin of the two handler
// as_on tests. It exists separately because the handler tests run against a nil
// TxRunner: a regression there surfaces as a panic (loud, but with no domain
// message), whereas this one states the consequence in the terms that matter.
func TestResolveAsOnNeverFallsBackToNow(t *testing.T) {
	if _, err := resolveAsOn("2025-11-15"); err == nil {
		t.Fatal("resolveAsOn accepted a bare date and returned an instant. A date is not an " +
			"instant: 'now' and '2025-11-15T00:00:00+05:30' sit on opposite sides of the 14th, so " +
			"the same books would report a document as DEEMED_ACCEPTED or as un-actioned depending " +
			"on when the report was run — a swing of the document's whole input tax in table 4(A)")
	}
	if _, err := resolveAsOn("not a time"); err == nil {
		t.Fatal("resolveAsOn silently substituted now for an unparseable as_on; a report for an " +
			"instant the caller never asked about is indistinguishable from a correct one")
	}
	// The EMPTY value is the one case that defaults, and it must: this is the
	// single boundary that reads the clock for the whole ITC surface.
	got, err := resolveAsOn("")
	if err != nil || got.IsZero() {
		t.Fatalf("resolveAsOn(\"\") = (%v, %v), want now: compliance/itc returns ErrNoAsOn rather "+
			"than reading a clock, so an absent as_on that did not default here would make every "+
			"reconciliation a 400", got, err)
	}
}

func TestITCReconciliationRefusesAnUnknownStatementKind(t *testing.T) {
	srv := noDBServer()
	code, msg := callErr(t, srv.handleITCReconciliation,
		scoped(http.MethodGet,
			"/v1/companies/x/itc-reconciliation?from=2025-04-01&to=2025-04-30&kind=GSTR2C", ""))
	if code != http.StatusBadRequest {
		t.Fatalf("kind=GSTR2C returned %d, want 400: an unrecognised kind silently reconciling "+
			"against the GSTR2B default would attribute the answer to a statement the caller did "+
			"not ask for", code)
	}
	if !strings.Contains(msg, "GSTR2B") {
		t.Errorf("error %q must name the accepted kinds", msg)
	}
}

func TestITCReconciliationRefusesAMalformedStatementID(t *testing.T) {
	srv := noDBServer()
	code, _ := callErr(t, srv.handleITCReconciliation,
		scoped(http.MethodGet,
			"/v1/companies/x/itc-reconciliation?from=2025-04-01&to=2025-04-30&statement_id=not-a-uuid", ""))
	if code != http.StatusBadRequest {
		t.Fatalf("statement_id=not-a-uuid returned %d, want 400", code)
	}
}

// ---------------------------------------------------------------------------
// GET /ims-inbox
// ---------------------------------------------------------------------------

func TestIMSInboxRequiresAValidReturnPeriod(t *testing.T) {
	srv := noDBServer()
	for _, tc := range []struct{ name, query string }{
		{"absent", ""},
		{"a date, not MMYYYY", "?return_period=2025-10-01"},
		{"month 13", "?return_period=132025"},
		{"YYYYMM transposed", "?return_period=202510"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, msg := callErr(t, srv.handleIMSInbox,
				scoped(http.MethodGet, "/v1/companies/x/ims-inbox"+tc.query, ""))
			if code != http.StatusBadRequest {
				t.Fatalf("return_period %q returned %d, want 400: the period decides which return "+
					"the documents belong to and which deemed-acceptance clock applies, and a "+
					"guessed one files credit in the wrong month", tc.query, code)
			}
			if !strings.Contains(msg, "return_period") {
				t.Errorf("error %q must name return_period", msg)
			}
		})
	}
}

func TestIMSInboxRefusesAMalformedAsOn(t *testing.T) {
	srv := noDBServer()
	code, msg := callErr(t, srv.handleIMSInbox,
		scoped(http.MethodGet, "/v1/companies/x/ims-inbox?return_period=102025&as_on=whenever", ""))
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: as_on decides whether an un-actioned document has "+
			"deem-accepted, so an inbox rendered at the wrong instant shows the operator actions "+
			"that no longer count", code)
	}
	if !strings.Contains(msg, "as_on") {
		t.Errorf("error %q must name as_on", msg)
	}
}

// ---------------------------------------------------------------------------
// POST /gstr2b-import, /ims-actions, /gst-filings
// ---------------------------------------------------------------------------

// TestGSTR2BImportRequiresAnExplicitKind pins that the statement kind is STATED,
// never guessed.
func TestGSTR2BImportRequiresAnExplicitKind(t *testing.T) {
	srv := noDBServer()
	for _, q := range []string{"", "?kind=", "?kind=GSTR2X"} {
		code, msg := callErr(t, srv.handleGSTR2BImport,
			scoped(http.MethodPost, "/v1/companies/x/gstr2b-import"+q, `{"x":1}`))
		if code != http.StatusBadRequest {
			t.Fatalf("kind %q returned %d, want 400. A 2A ingested as a 2B is put under a "+
				"deemed-acceptance clock that does not apply to it, so credit deem-accepts on a "+
				"date the portal never set", q, code)
		}
		if !strings.Contains(msg, "GSTR2B") || !strings.Contains(msg, "GSTR2A") {
			t.Errorf("error %q must name both accepted kinds", msg)
		}
	}
}

func TestGSTR2BImportRefusesAnEmptyBody(t *testing.T) {
	srv := noDBServer()
	code, _ := callErr(t, srv.handleGSTR2BImport,
		scoped(http.MethodPost, "/v1/companies/x/gstr2b-import?kind=GSTR2B&ingested_by=op", ""))
	if code != http.StatusBadRequest {
		t.Fatalf("empty body returned %d, want 400: an empty statement parses to zero documents, "+
			"and under model (b) zero documents SUPERSEDE the real 2B and reverse every purchase in "+
			"the period", code)
	}
}

// TestWritesRefuseAnAnonymousActor pins the audit columns. gst_inward_statement.
// ingested_by, gst_ims_action.acted_by and gst_return_filing.filed_by are all NOT
// NULL, and an empty string satisfies the constraint while destroying the trail.
func TestWritesRefuseAnAnonymousActor(t *testing.T) {
	srv := noDBServer()
	cases := []struct {
		name string
		h    http.HandlerFunc
		req  *http.Request
	}{
		{"gstr2b-import", srv.handleGSTR2BImport,
			scoped(http.MethodPost, "/v1/companies/x/gstr2b-import?kind=GSTR2B", `{"x":1}`)},
		{"ims-actions", srv.handleIMSAction,
			scoped(http.MethodPost, "/v1/companies/x/ims-actions",
				`{"document_id":"`+uuid.NewString()+`","action":"ACCEPT"}`)},
		{"gst-filings", srv.handleGSTFiling,
			scoped(http.MethodPost, "/v1/companies/x/gst-filings",
				`{"return_period":"102025"}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, msg := callErr(t, tc.h, tc.req)
			if code != http.StatusBadRequest {
				t.Fatalf("%s with no subject and no actor returned %d, want 400: the actor column is "+
					"the only record of who ingested a statement or actioned a supplier's document, "+
					"and a blank one turns the append-only log into a list of events with no author",
					tc.name, code)
			}
			if !strings.Contains(msg, "required") {
				t.Errorf("%s: error %q must say the actor is required", tc.name, msg)
			}
		})
	}
}

// TestWritesPreferTheAuthenticatedSubjectOverTheBody is the other half: a
// request that DOES carry a subject must not be refused, and must not let the
// body override who acted.
func TestWritesPreferTheAuthenticatedSubjectOverTheBody(t *testing.T) {
	req := scoped(http.MethodPost, "/v1/companies/x/gst-filings", `{"filed_by":"someone-else"}`)
	req = req.WithContext(db.WithSubject(req.Context(), "auth-subject-1"))
	rec := httptest.NewRecorder()
	got, ok := requireActorValue(rec, "someone-else", req, "filed_by")
	if !ok {
		t.Fatalf("a request carrying an authenticated subject must not be refused for want of an actor")
	}
	if got != "auth-subject-1" {
		t.Fatalf("actor = %q, want the AUTHENTICATED subject %q: a body-supplied actor that could "+
			"override the token would let any member record a filing in another user's name",
			got, "auth-subject-1")
	}
}

func TestIMSActionRefusesAnUnknownActionKind(t *testing.T) {
	srv := noDBServer()
	code, msg := callErr(t, srv.handleIMSAction,
		scoped(http.MethodPost, "/v1/companies/x/ims-actions",
			`{"document_id":"`+uuid.NewString()+`","action":"APPROVE","acted_by":"op"}`))
	if code != http.StatusBadRequest {
		t.Fatalf("action=APPROVE returned %d, want 400: the action vocabulary is CLOSED, and an "+
			"unrecognised action reaching the log would claim the filer did something the portal "+
			"has no counterpart for", code)
	}
	for _, want := range []string{"ACCEPT", "REJECT", "PENDING", "RESET"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q must name the accepted action %q", msg, want)
		}
	}
}

// TestIMSActionRefusesAnUnknownPendingCase is the important one of the pair: the
// service COERCES an empty PendingCase to ORDINARY, and ORDINARY is the single
// case on which PENDING is always allowed. A typo'd case would therefore turn a
// legal refusal into an acceptance, which is why the boundary closes the
// vocabulary itself.
func TestIMSActionRefusesAnUnknownPendingCase(t *testing.T) {
	srv := noDBServer()
	code, msg := callErr(t, srv.handleIMSAction,
		scoped(http.MethodPost, "/v1/companies/x/ims-actions",
			`{"document_id":"`+uuid.NewString()+`","action":"PENDING",`+
				`"pending_case":"ORIGNAL_CREDIT_NOTE","acted_by":"op"}`))
	if code != http.StatusBadRequest {
		t.Fatalf("a misspelt pending_case returned %d, want 400: it would be coerced to ORDINARY, "+
			"the one case PENDING is always allowed on, so a credit note that the law says cannot "+
			"be deferred before October 2025 would be deferred anyway", code)
	}
	if !strings.Contains(msg, string(itc.PendingOriginalCreditNote)) {
		t.Errorf("error %q must list the accepted pending cases", msg)
	}
}

func TestGSTFilingRefusesAMalformedFiledAt(t *testing.T) {
	srv := noDBServer()
	code, msg := callErr(t, srv.handleGSTFiling,
		scoped(http.MethodPost, "/v1/companies/x/gst-filings",
			`{"return_period":"102025","filed_at":"20 Nov 2025","filed_by":"op"}`))
	if code != http.StatusBadRequest {
		t.Fatalf("filed_at=\"20 Nov 2025\" returned %d, want 400: filed_at IS the IMS freeze, so "+
			"defaulting an unparseable one to now would silently change which IMS actions counted "+
			"towards a return the department has already received", code)
	}
	if !strings.Contains(msg, "filed_at") {
		t.Errorf("error %q must name filed_at", msg)
	}
}

func TestGSTFilingRefusesAnUnknownForm(t *testing.T) {
	srv := noDBServer()
	code, _ := callErr(t, srv.handleGSTFiling,
		scoped(http.MethodPost, "/v1/companies/x/gst-filings",
			`{"form":"GSTR9","return_period":"102025","filed_by":"op"}`))
	if code != http.StatusBadRequest {
		t.Fatalf("form=GSTR9 returned %d, want 400: only GSTR3B stamps the IMS freeze, and "+
			"accepting another form would freeze the action set on a return that does not freeze it", code)
	}
}

// ---------------------------------------------------------------------------
// GSTR-3B: rule 42/43 inputs
// ---------------------------------------------------------------------------

// TestGSTR3BRefusesAScalarCommonCredit is the money-shaped refusal. C2 and Tr are
// TAX, and table 4 is head-wise.
func TestGSTR3BRefusesAScalarCommonCredit(t *testing.T) {
	srv := noDBServer()
	for _, param := range []string{"common_credit", "capital_common_credit"} {
		// Unit level first, so the failure states the consequence: the handler
		// tests below run against a Server with no gstr service, where a
		// regression surfaces as a panic rather than a message.
		// The head-wise C2 is supplied alongside so that the ONLY thing standing
		// between this call and a successfully built policy is the scalar
		// refusal itself.
		if _, err := rule4243Policy(map[string][]string{
			param:                {"1000"},
			"common_credit_cgst": {"900"},
			"exempt_turnover":    {"250000"},
			"total_turnover":     {"1000000"},
		}); err == nil {
			t.Fatalf("rule4243Policy accepted a scalar %s. C2 and Tr are TAX and table 4 is "+
				"head-wise: whatever this did with the 1000 — all of it into one head, or silently "+
				"nothing — reverses credit in a head the filer does not hold it in, or reverses "+
				"nothing while clearing the disclosure that says rule 42/43 was not computed. Both "+
				"are wrong cash on a filed return", param)
		}
		code, msg := callErr(t, srv.handleGSTR3B,
			scoped(http.MethodGet,
				"/v1/companies/x/gstr3b?from=2025-04-01&to=2025-04-30&"+param+"=1000", ""))
		if code != http.StatusBadRequest {
			t.Fatalf("%s=1000 returned %d, want 400. A single figure cannot be apportioned across "+
				"CGST/SGST/IGST without inventing a split, and putting the whole 1000 into one head "+
				"reverses credit the filer holds in a head they do not — a wrong number in cash on a "+
				"filed return", param, code)
		}
		if !strings.Contains(msg, param+"_cgst") {
			t.Errorf("%s: error %q must name the head-wise params that would have worked", param, msg)
		}
	}
}

func TestGSTR3BRefusesRuleInputsWithoutBothTurnovers(t *testing.T) {
	srv := noDBServer()
	code, msg := callErr(t, srv.handleGSTR3B,
		scoped(http.MethodGet,
			"/v1/companies/x/gstr3b?from=2025-04-01&to=2025-04-30&common_credit_cgst=900&exempt_turnover=100", ""))
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: rule 42 is D1 = (E/F) x C2, and a missing F cannot be "+
			"substituted from this return — the books carry no export/SEZ marker, so a derived E "+
			"would be zero and the reversal would be zero for every taxpayer", code)
	}
	if !strings.Contains(msg, "total_turnover") {
		t.Errorf("error %q must name the missing input", msg)
	}
}

func TestGSTR3BRefusesTurnoversWithNoCommonCredit(t *testing.T) {
	srv := noDBServer()
	code, msg := callErr(t, srv.handleGSTR3B,
		scoped(http.MethodGet,
			"/v1/companies/x/gstr3b?from=2025-04-01&to=2025-04-30&exempt_turnover=100&total_turnover=1000", ""))
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: an apportionment over a zero common credit reverses nothing "+
			"while CLEARING the rule-42-43-not-computed disclosure, which is how a return that never "+
			"applied rule 42 comes to claim it did", code)
	}
	if !strings.Contains(msg, "common_credit_cgst") {
		t.Errorf("error %q must name the head-wise credit params", msg)
	}
}

func TestGSTR3BRefusesANonNumericTurnover(t *testing.T) {
	srv := noDBServer()
	code, _ := callErr(t, srv.handleGSTR3B,
		scoped(http.MethodGet,
			"/v1/companies/x/gstr3b?from=2025-04-01&to=2025-04-30&exempt_turnover=1,00,000&total_turnover=1000", ""))
	if code != http.StatusBadRequest {
		t.Fatalf("exempt_turnover=1,00,000 returned %d, want 400: money is parsed with "+
			"decimal.NewFromString and a rejected string must never become a zero E, which would "+
			"report a zero rule-42 reversal on a return that needed one", code)
	}
}

// TestRule4243PolicyBuildsBothRulesFromHeadwiseInputs is the positive control:
// the refusals above would all pass if the parser simply never built a policy.
func TestRule4243PolicyBuildsBothRulesFromHeadwiseInputs(t *testing.T) {
	q := map[string][]string{
		"exempt_turnover":            {"250000"},
		"total_turnover":             {"1000000"},
		"common_credit_cgst":         {"900.50"},
		"common_credit_sgst":         {"900.50"},
		"capital_common_credit_igst": {"4000"},
	}
	p, err := rule4243Policy(q)
	if err != nil {
		t.Fatalf("rule4243Policy: %v", err)
	}
	if p == nil || p.Rule42 == nil || p.Rule43 == nil {
		t.Fatalf("policy = %+v, want both rule 42 and rule 43 populated: a caller that supplied "+
			"every input and still got no apportionment would file a return with an unreversed "+
			"common credit and the disclosure claiming it was computed", p)
	}
	if got := p.Rule42.C2.CGST.String(); got != "900.5" {
		t.Errorf("rule 42 C2 CGST = %s, want 900.5 (parsed by decimal.NewFromString, never a float)", got)
	}
	if got := p.Rule42.E.String(); got != "250000" {
		t.Errorf("rule 42 E = %s, want 250000", got)
	}
	if got := p.Rule43.Tr.IGST.String(); got != "4000" {
		t.Errorf("rule 43 Tr IGST = %s, want 4000", got)
	}
	// The heads NOT supplied must be ZERO, not absent: apportionment scales each
	// head independently and a nil-valued decimal would panic or, worse, be read
	// as an unset field somewhere downstream.
	if !p.Rule43.Tr.CGST.IsZero() || !p.Rule42.C2.IGST.IsZero() {
		t.Errorf("unsupplied heads must be zero, got Tr.CGST=%s C2.IGST=%s",
			p.Rule43.Tr.CGST, p.Rule42.C2.IGST)
	}
}

// TestNoRuleInputsMeansNoPolicy is the backward-compatibility half of the parser:
// a caller that supplies none of the rule params must get a NIL policy, because a
// non-nil empty one changes nothing except the disclosure that says the
// apportionment was not computed.
func TestNoRuleInputsMeansNoPolicy(t *testing.T) {
	p, err := rule4243Policy(map[string][]string{"from": {"2025-04-01"}, "to": {"2025-04-30"}})
	if err != nil {
		t.Fatalf("rule4243Policy: %v", err)
	}
	if p != nil {
		t.Fatalf("policy = %+v, want nil: a non-nil policy with nil Rule42/Rule43 still reaches "+
			"disclosures3B, and any drift there would silently drop rule-42-43-not-computed from a "+
			"return that never computed it", p)
	}
}

// ---------------------------------------------------------------------------
// THE BACKWARD-COMPATIBILITY PIN (live Postgres)
// ---------------------------------------------------------------------------

// TestGSTR3BWithoutStatementIDIsTheBareReturn pins the hard requirement: with no
// statement_id the handler's response must be BYTE-IDENTICAL to what the bare
// generator produces from a zero Options3B — the shape this endpoint has served
// all along — and must carry `no-inward-statement` and `not-filing-ready`.
//
// The mutation it is built to catch is one line: hoisting `opts.ITC = s.itc` out
// of the `if statementID != uuid.Nil` branch. opts.ITC is an INTERFACE, so an
// unconditional assignment makes it non-nil for EVERY caller; gstr then takes the
// reconciled path, calls itc.Decisions on a context with no as-on instant, and
// this endpoint starts returning 422 to every existing client — or, if a clock
// were threaded in too, silently drops the `no-inward-statement` disclosure and
// tells a frontend the return is backed by a GSTR-2B nobody ever ingested.
func TestGSTR3BWithoutStatementIDIsTheBareReturn(t *testing.T) {
	srv := newITCLiveServer(t)

	companyID := uuid.New()
	testdb.Cleanup(t, companyID)
	ctx := db.WithCompany(context.Background(), companyID)

	const from, to = "2025-04-01", "2025-04-30"
	want, err := srv.gstr.Generate3B(ctx, from, to, gstr.Options3B{})
	if err != nil {
		t.Fatalf("bare Generate3B: %v", err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal bare return: %v", err)
	}

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("companyID", companyID.String())
	req := httptest.NewRequest(http.MethodGet,
		"/v1/companies/"+companyID.String()+"/gstr3b?from="+from+"&to="+to, nil).
		WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	srv.handleGSTR3B(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s). With no statement_id this endpoint must behave "+
			"exactly as it always has; a non-200 here is every existing 3B caller broken",
			rec.Code, rec.Body.String())
	}
	gotJSON := bytes.TrimRight(rec.Body.Bytes(), "\n")
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("the no-statement_id response is NOT the bare return.\n got: %s\nwant: %s",
			gotJSON, wantJSON)
	}

	var decoded struct {
		Disclosures []string `json:"disclosures"`
	}
	if err := json.Unmarshal(gotJSON, &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, want := range []string{gstr.DiscNoInwardStatement, gstr.DiscNotFilingReady} {
		found := false
		for _, d := range decoded.Disclosures {
			if d == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("disclosures %v do not contain %q: nothing in table 4 has been matched against "+
				"what the suppliers actually filed, and a frontend that cannot read that will "+
				"present an unreconciled return as final", decoded.Disclosures, want)
		}
	}
}

// TestGSTR3BStatementIDWithNoIngestedStatementIsRefused is the other side of the
// switch: asking for a specific statement that does not exist must be a 400
// naming the period, not a 200 quietly served from the books alone.
func TestGSTR3BStatementIDWithNoIngestedStatementIsRefused(t *testing.T) {
	srv := newITCLiveServer(t)

	companyID := uuid.New()
	testdb.Cleanup(t, companyID)
	ctx := db.WithCompany(context.Background(), companyID)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("companyID", companyID.String())
	statementID := uuid.NewString()
	req := httptest.NewRequest(http.MethodGet,
		"/v1/companies/"+companyID.String()+"/gstr3b?from=2025-04-01&to=2025-04-30&statement_id="+statementID, nil).
		WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))

	code, msg := callErr(t, srv.handleGSTR3B, req)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: serving a books-only table 4 to a caller who asked for a "+
			"specific GSTR-2B would attribute an unmatched return to a statement that does not "+
			"exist", code)
	}
	if !strings.Contains(msg, "042025") {
		t.Errorf("error %q must name the tax period with no ingested statement", msg)
	}
}

// newITCLiveServer boots a real Server against the live Postgres.
//
// It does NOT t.Skip when the database is unavailable, unlike newServer in the
// external test package: a green run containing skips is not evidence, and these
// two tests are the only proof that the no-statement_id path still serves the
// bytes it always did.
func newITCLiveServer(t *testing.T) *Server {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Fatal("CORED_SKIP_DB is set: this test is the backward-compatibility pin and cannot be " +
			"skipped — a skipped pin is indistinguishable from a passing one")
	}
	ctx := context.Background()
	adminConn, err := pgx.Connect(ctx, testdb.AdminDSNValue())
	if err != nil {
		t.Fatalf("admin postgres unavailable: %v", err)
	}
	if err := migrations.Apply(ctx, adminConn); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("apply migrations: %v", err)
	}
	_ = adminConn.Close(ctx)

	t.Setenv("AUTH_DEV_TOKEN_ENABLED", "true")
	srv, err := New(ctx, itcTestAppDSN)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	t.Cleanup(srv.Close)
	return srv
}
