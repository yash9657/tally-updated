package server

// handler_gstr_datasource_test.go — D117: A DEAD DATABASE IS NOT AN
// "UNPROCESSABLE ENTITY".
//
// ⛔⛔ THE ROW WAS REGISTERED FROM A CODE READING AND NEVER DRIVEN, and it said
// so: "no query was actually made to fail, so the exact body a filer would see
// is inferred from the handler, and whoever takes this should reproduce it
// first". This file is that reproduction, kept as the regression test.
//
// HOW IT REPRODUCES. The register suggested breaking a column name in a scratch
// schema. This does the same thing one layer up and hermetically: the GSTR
// service is built over a TxRunner that FAILS, once with the sentence Postgres
// gives for a column that is not there (the register's own scenario) and once
// with the sentence pgx gives for a database that is not listening (the "dead
// database" the row is named after). Nothing else about the handler is stubbed.
//
// WHAT WAS MEASURED BEFORE THE FIX (run against the handler as it stood):
//
//	GET /v1/companies/{id}/gstr3b?from=…&to=…
//	  422 gstr: query 3b versions: ERROR: column vv.taxable_value does not exist (SQLSTATE 42703)
//
// A 422 says "the request was well-formed but the books cannot answer it" — a
// statement about the FILER'S data. This is a statement about the SERVER, and
// the two send an operator to completely different places.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/compliance/gstr"
	"github.com/khata/cored/internal/platform/db"
)

// deadRunner is a database that cannot answer. Its error is returned from
// RunInTx itself, which is what a server whose Postgres is down actually does —
// the transaction never begins, so no `gstr: ` wrap is ever applied and the
// naked driver sentence is what used to reach the filer.
type deadRunner struct{ err error }

func (d deadRunner) RunInTx(context.Context, db.TxFunc) error { return d.err }

const (
	// The two shapes, verbatim in the form each library produces.
	brokenColumn = `ERROR: column vv.taxable_value does not exist (SQLSTATE 42703)`
	deadDatabase = `failed to connect to ` +
		`host=localhost user=tally_app database=tally: dial error (connection refused)`
)

// gstrServer builds a Server whose ONLY wired dependency is a GSTR service over
// a failing runner. Everything the handler does before it reaches that service
// — the route scope, the query parsing — is the real code.
func gstrServer(failure error) *Server {
	return &Server{gstr: gstr.NewService(deadRunner{err: failure})}
}

// callCoded runs h and returns the status, the `error` sentence and the `code`.
func callCoded(t *testing.T, h http.HandlerFunc, req *http.Request) (int, string, string) {
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
	code, _ := body["code"].(string)
	return rec.Code, msg, code
}

const janWindow = "?from=2026-01-01&to=2026-01-31"

// assertBooksUnreadable is the whole contract of the fix, in one place: the
// status, the machine-readable code, and — the half that is easy to forget —
// that the SQL and the Go package name did NOT travel to the filer.
func assertBooksUnreadable(t *testing.T, document string, code int, msg, errCode string) {
	t.Helper()
	if code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500. A failed query is a statement about the SERVER; "+
			"422 says the request was well-formed but the BOOKS cannot answer it, which "+
			"sends the filer into their own vouchers looking for something that was never "+
			"there. Body: %q", code, msg)
	}
	if errCode != "BOOKS_UNREADABLE" {
		t.Fatalf("code = %q, want BOOKS_UNREADABLE. A screen that has to recognise a server "+
			"fault by matching its PROSE breaks the next time the wording improves — D112 "+
			"shipped for exactly that reason", errCode)
	}
	if !strings.Contains(msg, document) {
		t.Fatalf("the sentence does not name the document (%s): %q", document, msg)
	}
	// ⛔ THE DIAGNOSTIC MUST NOT BE IN THE BODY. It keeps its `gstr: ` prefix on
	// purpose (errors_operator_facing_test.go says why) because it is for a LOG.
	// The defect was never the prefix — it was showing a filer a diagnostic as
	// if it were a refusal.
	for _, leak := range []string{"SQLSTATE", "gstr:", "column ", "dial error", "pgx", "tally_app"} {
		if strings.Contains(msg, leak) {
			t.Fatalf("the filer was shown %q, which is a diagnostic and not a refusal: %q", leak, msg)
		}
	}
	// And it must say whose fault it is and that nothing was filed — the first
	// two questions anybody asks when a statutory document errors.
	if !strings.Contains(msg, "fault on the server") {
		t.Fatalf("the sentence does not say whose fault it is: %q", msg)
	}
	if !strings.Contains(msg, "nothing has been filed or altered") {
		t.Fatalf("the sentence does not say nothing was filed: %q", msg)
	}
}

func TestGSTR3BAnswers500WhenTheBooksCannotBeRead(t *testing.T) {
	for _, tc := range []struct{ name, failure string }{
		{"a column that is not there", brokenColumn},
		{"a database that is not listening", deadDatabase},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := gstrServer(errors.New(tc.failure))
			code, msg, errCode := callCoded(t, srv.handleGSTR3B,
				scoped(http.MethodGet, "/v1/companies/x/gstr3b"+janWindow, ""))
			assertBooksUnreadable(t, "GSTR-3B", code, msg, errCode)
		})
	}
}

func TestGSTR1Answers500WhenTheBooksCannotBeRead(t *testing.T) {
	// ⚠️ /gstr1 IS HERE BECAUSE D117's ROW SAYS "check the same default arm on
	// /gstr1 at the same time". It had the identical bare
	// `writeError(w, 422, err.Error())` and nothing pointed at it.
	srv := gstrServer(errors.New(brokenColumn))
	// ⚠️ THE GSTIN IS SUPPLIED so the handler does not first go to the database
	// for the COMPANY's one — this Server has no pool at all, deliberately: what
	// is under test is the status a GENERATION failure produces, not the route's
	// other reads.
	code, msg, errCode := callCoded(t, srv.handleGSTR1,
		scoped(http.MethodGet, "/v1/companies/x/gstr1"+janWindow+"&gstin=29AAAAA0000A1Z5", ""))
	assertBooksUnreadable(t, "GSTR-1", code, msg, errCode)
}

func TestGSTR9Answers500WhenAnyPeriodCouldNotBeRead(t *testing.T) {
	// ⛔ THE ANNUAL RETURN IS THE HARDEST OF THE THREE, and it is the one a
	// naive fix misses. Generate9 FLATTENS each of the twelve periods' errors
	// into a sentence for the operator, which destroys the chain `errors.Is`
	// would walk — so a dead database failed all twelve months and the year came
	// back a 422 built out of twelve stringified SQL errors.
	srv := gstrServer(errors.New(deadDatabase))
	req := scoped(http.MethodGet,
		"/v1/companies/x/gstr9?from=2025-04-01&to=2026-03-31"+
			"&cgst_ledger_id=11111111-1111-1111-1111-111111111111"+
			"&sgst_ledger_id=22222222-2222-2222-2222-222222222222"+
			"&igst_ledger_id=33333333-3333-3333-3333-333333333333"+
			"&input_cgst_ledger_id=44444444-4444-4444-4444-444444444444"+
			"&input_sgst_ledger_id=55555555-5555-5555-5555-555555555555"+
			"&input_igst_ledger_id=66666666-6666-6666-6666-666666666666", "")
	code, msg, errCode := callCoded(t, srv.handleGSTR9, req)
	assertBooksUnreadable(t, "GSTR-9", code, msg, errCode)
	// The twelve stringified failures must not be in there either.
	if strings.Contains(msg, "of the 12 periods") {
		t.Fatalf("the filer was handed the internal twelve-period diagnostic: %q", msg)
	}
}

// ===========================================================================
// ⭐⭐ THE CONTROLS. Every one of these must STILL be what it was: the fix is a
// status DECISION, and a decision that answers 500 for everything is the same
// defect facing the other way — it would tell a filer that a negative table 4B
// or a window that is not a financial year is somebody else's problem, when it
// is theirs and the message says how to fix it.
// ===========================================================================

func TestARefusalAboutTheBOOKSKeepsIts422AndItsSentence(t *testing.T) {
	// ErrInvalid3B: the generated document violated one of the report's own
	// honesty rules. Not plumbing — and the sentence is the whole point of it.
	//
	// ⚠️ IT IS PUT THROUGH THE WRITER DIRECTLY AND NOT THROUGH A RUNNER, because
	// a runner cannot produce it and pretending otherwise would test a fiction:
	// `Validate()` runs AFTER the transaction has closed, which is exactly why
	// marking the transaction's own result cannot mis-classify a refusal. Every
	// one of the four closures in that package holds queries and scans and
	// nothing else.
	rec := httptest.NewRecorder()
	writeGSTR3BError(rec, "2026-01-01", "2026-01-31",
		fmt.Errorf("%w: table 3.2 IGST 500.00 exceeds 3.1(a) IGST 100.00", gstr.ErrInvalid3B))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON (%q): %v", rec.Body.String(), err)
	}
	code := rec.Code
	msg, _ := body["error"].(string)
	errCode, _ := body["code"].(string)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 — a validation refusal is about the DOCUMENT, "+
			"and answering 500 would tell the filer to report a fault instead of reading "+
			"the sentence. Body: %q", code, msg)
	}
	if errCode != "" {
		t.Fatalf("code = %q, want none: BOOKS_UNREADABLE must not be attached to a refusal", errCode)
	}
	if !strings.Contains(msg, "table 3.2 IGST") {
		t.Fatalf("the refusal lost its own sentence: %q", msg)
	}
}

func TestAWindowThatIsNotAFinancialYearKeepsIts400(t *testing.T) {
	srv := gstrServer(errors.New(deadDatabase))
	code, msg, _ := callCoded(t, srv.handleGSTR9,
		scoped(http.MethodGet, "/v1/companies/x/gstr9?from=2025-01-01&to=2025-12-31", ""))
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: the REQUEST is wrong and names the parameter to "+
			"change — the runner never even ran. Body: %q", code, msg)
	}
}

func TestAMissingPeriodStillAnswers400AndNeverReachesTheDatabase(t *testing.T) {
	srv := gstrServer(errors.New(deadDatabase))
	code, msg, _ := callCoded(t, srv.handleGSTR3B,
		scoped(http.MethodGet, "/v1/companies/x/gstr3b", ""))
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400. Body: %q", code, msg)
	}
}

// ⭐ AND THE CLASSIFIER ITSELF, from the outside: a marked failure answers the
// sentinel, an ordinary refusal does not, and neither loses its message.
func TestIsDataSourceIsExactAboutWhatItClaims(t *testing.T) {
	svc := gstr.NewService(deadRunner{err: errors.New(brokenColumn)})
	// The tenant scope is checked BEFORE any round trip, so the context has to
	// carry a company for the runner to be reached at all.
	ctx := db.WithCompany(context.Background(), uuid.New())
	_, err := svc.Generate3B(ctx, "2026-01-01", "2026-01-31", gstr.Options3B{})
	if err == nil {
		t.Fatal("a dead runner produced a GSTR-3B")
	}
	if !gstr.IsDataSource(err) {
		t.Fatalf("IsDataSource(%v) = false; a failure out of the row transaction is plumbing", err)
	}
	// ⛔ THE MARK DOES NOT REWRITE THE DIAGNOSTIC. The log line has to keep
	// saying what actually broke.
	if !strings.Contains(err.Error(), brokenColumn) {
		t.Fatalf("the diagnostic was altered by marking it: %q", err.Error())
	}
	// And a validation refusal is NOT one, whatever else is true of it.
	if gstr.IsDataSource(fmt.Errorf("%w: table 4C", gstr.ErrInvalid3B)) {
		t.Fatal("a validation refusal was classified as a plumbing failure")
	}
}
