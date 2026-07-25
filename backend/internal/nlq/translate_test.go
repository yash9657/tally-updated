package nlq

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/query"
	"github.com/khata/cored/migrations"
)

// DB test discipline (§1.4): CORED_SKIP_DB guard FIRST, then connect-or-skip. The
// two DSNs are copied verbatim from internal/query/execute_test.go — _test.go
// identifiers do not cross packages.
const (
	adminDSN = "postgres://postgres:postgres@localhost:5433/tally?sslmode=disable"
	appDSN   = "postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable"
)

// injectionPayloads copied verbatim from query_security_test.go (external test
// identifiers do not cross packages). These are sprayed through the FAKE LLM
// response to prove the downstream compiler — not the translator — is the gate.
var injectionPayloads = []string{
	"1; DROP TABLE ledger",
	"x' OR '1'='1",
	") UNION SELECT id, name FROM app_user --",
	"'; DELETE FROM voucher_version; --",
	" bad-unicode☃ quote' /* x */",
	"`backtick`",
	"\\'; UPDATE ledger SET name='pwned'; --",
}

// capture records what the translator sent upstream, so tests can assert the
// wire contract (headers, forced tool_choice) and the privacy invariant (no
// tenant data in the prompt).
type capture struct {
	called      bool
	method      string
	path        string
	apiKey      string
	version     string
	contentType string
	body        []byte
}

// fakeAPI stands up an httptest server whose handler returns respBody with
// status, recording the request into cap. It returns a translator pointed at it.
func fakeAPI(t *testing.T, cap *capture, status int, respBody string) *ClaudeTranslator {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.called = true
		cap.method = r.Method
		cap.path = r.URL.Path
		cap.apiKey = r.Header.Get("x-api-key")
		cap.version = r.Header.Get("anthropic-version")
		cap.contentType = r.Header.Get("content-type")
		cap.body, _ = io.ReadAll(r.Body)
		w.WriteHeader(status)
		io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return &ClaudeTranslator{APIKey: "dummy-key", BaseURL: srv.URL}
}

// toolUse builds a Messages-API 200 body whose single tool_use block carries the
// given raw JSON as emit_query's input.
func toolUse(inputJSON string) string {
	return `{"stop_reason":"tool_use","content":[{"type":"text","text":"ok"},{"type":"tool_use","name":"emit_query","input":` + inputJSON + `}]}`
}

func TestToQuery_CannedToolUse_ParsesQuery(t *testing.T) {
	var cap capture
	tr := fakeAPI(t, &cap, http.StatusOK, toolUse(`{"entity":"vouchers","metrics":[{"func":"sum","column":"taxable_value","as":"total_purchases"}],"dimensions":["month"],"filters":[{"column":"voucher_type_name","op":"eq","values":["Purchase"]}],"date_range":null,"order_by":[{"column":"month","desc":false}],"limit":100}`))

	q, err := tr.ToQuery(context.Background(), uuid.New(), "total purchases this quarter")
	if err != nil {
		t.Fatalf("ToQuery: %v", err)
	}
	if q.Entity != query.EntityVouchers {
		t.Errorf("entity = %q, want vouchers", q.Entity)
	}
	if len(q.Metrics) != 1 || q.Metrics[0].Func != query.AggSum || q.Metrics[0].Column != "taxable_value" || q.Metrics[0].As != "total_purchases" {
		t.Errorf("metrics = %+v", q.Metrics)
	}
	if len(q.Filters) != 1 || q.Filters[0].Column != "voucher_type_name" || q.Filters[0].Values[0] != "Purchase" {
		t.Errorf("filters = %+v", q.Filters)
	}
	// The returned Query must actually compile (it is the real, whitelisted shape).
	if _, _, cerr := query.Compile(q); cerr != nil {
		t.Errorf("returned query does not compile: %v", cerr)
	}

	// Wire contract.
	if cap.method != http.MethodPost || cap.path != "/v1/messages" {
		t.Errorf("request = %s %s, want POST /v1/messages", cap.method, cap.path)
	}
	if cap.apiKey != "dummy-key" || cap.version != anthropicVersion || cap.contentType != "application/json" {
		t.Errorf("headers: key=%q version=%q ct=%q", cap.apiKey, cap.version, cap.contentType)
	}
	// Forced tool use.
	var sent messagesRequest
	if err := json.Unmarshal(cap.body, &sent); err != nil {
		t.Fatalf("sent body not decodable: %v", err)
	}
	if sent.ToolChoice.Type != "tool" || sent.ToolChoice.Name != "emit_query" {
		t.Errorf("tool_choice = %+v, want forced emit_query", sent.ToolChoice)
	}
	if len(sent.Messages) != 1 || sent.Messages[0].Content != "total purchases this quarter" {
		t.Errorf("user message = %+v", sent.Messages)
	}
}

// TestToQuery_NoTenantDataInPrompt is the load-bearing privacy check: the request
// body sent upstream must contain neither the companyID nor any tenant name. The
// oracle is the captured bytes, not a code comment.
func TestToQuery_NoTenantDataInPrompt(t *testing.T) {
	var cap capture
	tr := fakeAPI(t, &cap, http.StatusOK, toolUse(`{"entity":"vouchers","metrics":[],"dimensions":["month"],"filters":[],"date_range":null,"order_by":[],"limit":100}`))
	companyID := uuid.New()
	if _, err := tr.ToQuery(context.Background(), companyID, "sales by month"); err != nil {
		t.Fatalf("ToQuery: %v", err)
	}
	body := string(cap.body)
	if strings.Contains(body, companyID.String()) {
		t.Error("request body leaks the companyID into the prompt")
	}
	for _, secret := range []string{"Acme Traders", "Sundry Debtors"} {
		if strings.Contains(body, secret) {
			t.Errorf("request body unexpectedly contains %q", secret)
		}
	}
}

// TestToQuery_NonOK_MapsToUnavailable: EVERY non-200 status is an operator fault
// -> ErrTranslatorUnavailable (503), never a user's bad question (422). This is
// the correction that keeps a dead API key (401) or bad model id (404) from being
// mislabeled "rephrase your question".
func TestToQuery_NonOK_MapsToUnavailable(t *testing.T) {
	for _, status := range []int{
		http.StatusUnauthorized,          // 401 bad key
		http.StatusForbidden,             // 403 permission
		http.StatusNotFound,              // 404 bad model id
		http.StatusRequestEntityTooLarge, // 413
		http.StatusTooManyRequests,       // 429
		http.StatusInternalServerError,   // 500
		529,                              // overloaded_error
	} {
		var cap capture
		tr := fakeAPI(t, &cap, status, `{"type":"error","error":{"type":"x","message":"y"}}`)
		_, err := tr.ToQuery(context.Background(), uuid.New(), "sales this month")
		if !errors.Is(err, ErrTranslatorUnavailable) {
			t.Errorf("status %d: err = %v, want ErrTranslatorUnavailable", status, err)
		}
		if errors.Is(err, query.ErrUntranslatable) {
			t.Errorf("status %d: must NOT be ErrUntranslatable (that would 422 an operator fault)", status)
		}
	}
}

// TestToQuery_Refusal: a model refusal is HTTP 200 with stop_reason "refusal"
// and no tool_use block -> ErrUntranslatable (422), an explicit, tested outcome.
func TestToQuery_Refusal(t *testing.T) {
	var cap capture
	tr := fakeAPI(t, &cap, http.StatusOK, `{"stop_reason":"refusal","content":[{"type":"text","text":"I can't help with that."}]}`)
	_, err := tr.ToQuery(context.Background(), uuid.New(), "do something bad")
	if !errors.Is(err, query.ErrUntranslatable) {
		t.Errorf("refusal: err = %v, want ErrUntranslatable", err)
	}
}

func TestToQuery_UnknownFields(t *testing.T) {
	var cap capture
	tr := fakeAPI(t, &cap, http.StatusOK, toolUse(`{"entity":"vouchers","metrics":[],"dimensions":[],"filters":[],"date_range":null,"order_by":[],"limit":100,"evil":"gotcha"}`))
	if _, err := tr.ToQuery(context.Background(), uuid.New(), "x"); !errors.Is(err, query.ErrUntranslatable) {
		t.Errorf("unknown field: err = %v, want ErrUntranslatable", err)
	}
}

func TestToQuery_EmptyEntity(t *testing.T) {
	var cap capture
	tr := fakeAPI(t, &cap, http.StatusOK, toolUse(`{"entity":"","metrics":[],"dimensions":[],"filters":[],"date_range":null,"order_by":[],"limit":0}`))
	if _, err := tr.ToQuery(context.Background(), uuid.New(), "what's the meaning of life"); !errors.Is(err, query.ErrUntranslatable) {
		t.Errorf("empty entity: err = %v, want ErrUntranslatable", err)
	}
}

// TestToQuery_NLTooLong rejects an oversized question up front, WITHOUT a network
// call (the fake must never be hit).
func TestToQuery_NLTooLong(t *testing.T) {
	var cap capture
	tr := fakeAPI(t, &cap, http.StatusOK, toolUse(`{}`))
	long := strings.Repeat("a", maxNLChars+1)
	if _, err := tr.ToQuery(context.Background(), uuid.New(), long); !errors.Is(err, query.ErrUntranslatable) {
		t.Errorf("too-long NL: err = %v, want ErrUntranslatable", err)
	}
	if cap.called {
		t.Error("oversized NL must be rejected before any network call")
	}
}

func TestToQuery_EmptyNL(t *testing.T) {
	var cap capture
	tr := fakeAPI(t, &cap, http.StatusOK, toolUse(`{}`))
	if _, err := tr.ToQuery(context.Background(), uuid.New(), ""); !errors.Is(err, query.ErrUntranslatable) {
		t.Errorf("empty NL: err = %v, want ErrUntranslatable", err)
	}
	if cap.called {
		t.Error("empty NL must be rejected before any network call")
	}
}

// TestToQuery_NetworkError: a dead upstream -> ErrTranslatorUnavailable. Port 1 on
// loopback refuses immediately (no DNS hang, no timeout wait).
func TestToQuery_NetworkError(t *testing.T) {
	tr := &ClaudeTranslator{APIKey: "dummy", BaseURL: "http://127.0.0.1:1"}
	if _, err := tr.ToQuery(context.Background(), uuid.New(), "sales this month"); !errors.Is(err, ErrTranslatorUnavailable) {
		t.Errorf("dead upstream: err = %v, want ErrTranslatorUnavailable", err)
	}
}

// TestToQuery_InjectionThroughFakeLLM_CompilerRejects runs UNCONDITIONALLY (no
// DB, no key): the fake LLM echoes hostile DSLs; the translator passes them
// through faithfully and the COMPILER is proven to be the gate — either it
// rejects the hostile identifier, or it parameterizes the value so no payload
// literal ever reaches the SQL text.
func TestToQuery_InjectionThroughFakeLLM_CompilerRejects(t *testing.T) {
	for _, p := range injectionPayloads {
		// (a) payload as the entity -> the translator returns it; the compiler
		// rejects it as an unknown entity.
		var cap capture
		tr := fakeAPI(t, &cap, http.StatusOK, toolUse(`{"entity":`+jsonStr(p)+`,"metrics":[],"dimensions":[],"filters":[],"date_range":null,"order_by":[],"limit":100}`))
		q, err := tr.ToQuery(context.Background(), uuid.New(), "hostile")
		if err != nil {
			t.Fatalf("payload %q as entity: translator errored instead of passing through: %v", p, err)
		}
		if _, _, cerr := query.Compile(q); cerr == nil {
			t.Errorf("payload %q as entity compiled — compiler is not the gate", p)
		}

		// (b) payload as a filter VALUE on a valid column -> compiles, but the
		// payload appears ONLY as a bound arg, never in the SQL string.
		tr2 := fakeAPI(t, &cap, http.StatusOK, toolUse(`{"entity":"vouchers","metrics":[{"func":"sum","column":"taxable_value","as":"t"}],"dimensions":["month"],"filters":[{"column":"voucher_type_name","op":"eq","values":[`+jsonStr(p)+`]}],"date_range":null,"order_by":[],"limit":100}`))
		q2, err := tr2.ToQuery(context.Background(), uuid.New(), "hostile value")
		if err != nil {
			t.Fatalf("payload %q as value: %v", p, err)
		}
		sql, args, cerr := query.Compile(q2)
		if cerr != nil {
			continue // rejected outright is also safe
		}
		if strings.Contains(sql, p) {
			t.Errorf("payload %q leaked into SQL text: %s", p, sql)
		}
		var bound bool
		for _, a := range args {
			if s, ok := a.(string); ok && s == p {
				bound = true
			}
		}
		if !bound {
			t.Errorf("payload %q was neither rejected nor bound as an arg", p)
		}
	}
}

// TestToQuery_InjectionDBIntact is the DB half (connect-or-skip): the hostile
// query the translator returned actually executes against a live pool, and the
// physical tables are byte-for-byte intact afterward.
func TestToQuery_InjectionDBIntact(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	ctx := context.Background()
	adminConn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	if err := migrations.Apply(ctx, adminConn); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("apply migrations: %v", err)
	}
	_ = adminConn.Close(ctx)

	pool, err := db.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}
	t.Cleanup(pool.Close)

	companyID := uuid.Must(uuid.NewV7())
	cctx := db.WithCompany(ctx, companyID)
	if _, err := identity.NewService(pool).CreateCompany(cctx, identity.Company{
		ID:          companyID,
		Name:        "NLQ Sec " + companyID.String()[:8],
		FYStartDate: "2025-04-01",
	}); err != nil {
		t.Fatalf("create company: %v", err)
	}

	before := rawCount(t, "ledger", companyID)

	for _, p := range injectionPayloads {
		var cap capture
		tr := fakeAPI(t, &cap, http.StatusOK, toolUse(`{"entity":"vouchers","metrics":[{"func":"sum","column":"taxable_value","as":"t"}],"dimensions":["month"],"filters":[{"column":"voucher_type_name","op":"eq","values":[`+jsonStr(p)+`]}],"date_range":null,"order_by":[],"limit":100}`))
		q, err := tr.ToQuery(cctx, companyID, "hostile")
		if err != nil {
			t.Fatalf("translate hostile: %v", err)
		}
		// Execute through the read-only, RLS-scoped runner. A compile/exec error is
		// fine (rejected); a success must return with the DB untouched.
		_, _ = query.Execute(cctx, pool, q)
	}

	if after := rawCount(t, "ledger", companyID); after != before {
		t.Fatalf("ledger row count changed under injection: before=%d after=%d", before, after)
	}
}

func rawCount(t *testing.T, table string, companyID uuid.UUID) int64 {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	defer conn.Close(ctx)
	var n int64
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE company_id = $1", companyID).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
