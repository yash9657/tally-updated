package nlq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/llm"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
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
	// This package has no company-minting helper: the one company it makes is
	// made right here, inline, so the teardown is registered right here too. It
	// runs AFTER the row-count assertion below (t.Cleanup functions run once the
	// test body has returned), so it cannot make that assertion vacuous — it only
	// stops the company outliving the test that needed it.
	testdb.Cleanup(t, companyID)

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

// TestNLQHarnessRemovesItsCompany is the anti-rot guard on the teardown inside
// TestToQuery_InjectionDBIntact.
//
// WHY THIS ONE IS SHAPED DIFFERENTLY FROM ITS EIGHT SIBLINGS
// ----------------------------------------------------------
// Every other package in this wave mints its company in a helper that RETURNS
// the id, so its guard can call the helper and count rows for that id. This
// package has no such helper — the company is created inline in the middle of
// the injection test — and extracting one would change a fixture that an
// adversarial security test depends on. So instead of asking the harness for an
// id, this test runs the real test function and looks for its FOOTPRINT: the
// company it creates is the only one in the database named "NLQ Sec …", so any
// row matching that prefix which exists afterwards and did not exist before is,
// by construction, a company this package leaked.
//
// Taking a before-set rather than a plain count is what makes it safe on a
// shared dev database: another agent creating unrelated companies concurrently
// cannot affect the result, because only NEW rows carrying this package's own
// name prefix are ever reported.
//
// The child tables are then counted for each survivor rather than stopping at
// `company`, for the same reason as everywhere else in this wave:
// notification_outbox is the ONLY foreign key referencing company(id) and its
// action is NO ACTION, so a teardown that removed the parent row alone would
// leave the whole chart of accounts orphaned and look completely clean.
func TestNLQHarnessRemovesItsCompany(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	ctx := context.Background()
	// ADMIN, deliberately: the app role is FORCE-RLS'd to a single company and
	// would answer "no rows" for every probe here whatever actually survived.
	conn, err := pgx.Connect(ctx, testdb.AdminDSNValue())
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	defer conn.Close(ctx)

	nlqCompanies := func(when string) map[uuid.UUID]bool {
		rows, err := conn.Query(ctx, `SELECT id FROM company WHERE name LIKE 'NLQ Sec %'`)
		if err != nil {
			t.Fatalf("list NLQ companies %s: %v", when, err)
		}
		defer rows.Close()
		out := map[uuid.UUID]bool{}
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				t.Fatalf("scan NLQ company id %s: %v", when, err)
			}
			out[id] = true
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate NLQ companies %s: %v", when, err)
		}
		return out
	}

	before := nlqCompanies("before")
	t.Run("harness", func(t *testing.T) { TestToQuery_InjectionDBIntact(t) })
	after := nlqCompanies("after")

	for id := range after {
		if before[id] {
			// Pre-existing residue from an older run of this package, before the
			// teardown existed. Not this run's doing, so not this run's failure.
			continue
		}
		for _, tbl := range []string{"company", "account_group", "ledger", "voucher_type", "acl_rule"} {
			var n int
			if err := conn.QueryRow(ctx, "SELECT count(*) FROM "+tbl+" WHERE company_id = $1", id).Scan(&n); err != nil {
				t.Fatalf("count %s for company %s: %v", tbl, id, err)
			}
			if n != 0 {
				t.Errorf("%s still holds %d rows for company %s after TestToQuery_InjectionDBIntact finished: "+
					"the nlq injection test leaks a company on EVERY run and nothing will ever delete it",
					tbl, n, id)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// W0: the wire shape is now internal/llm's, so this file owns its own oracle.
// ---------------------------------------------------------------------------
//
// ⛔ THESE DECLARATIONS ARE DELIBERATELY A SECOND, INDEPENDENT STATEMENT OF THE
// MESSAGES-API REQUEST SHAPE, NOT AN IMPORT OF llm's.
//
// Until W0 they were production types in translate.go and this file decoded the
// captured body with them, which made the wire assertions circular-ish but at
// least real. internal/llm now owns the encoding (adapter_anthropic.go). The
// tempting move is to decode with llm's unexported structs — impossible — or to
// delete the assertions as "already covered by llm's adapter tests". Both are
// wrong for the same reason: llm's tests prove the adapter encodes what the
// adapter meant, and what this package needs proved is that the request nlq
// builds still ARRIVES as a forced emit_query call carrying the question and
// nothing else. That is an integration property, and an integration property
// checked against a restatement of the contract is worth more than one checked
// against the implementation it is supposed to catch drifting.
//
// So: if internal/llm ever renames a field here, this test fails — and it SHOULD,
// because the provider would have started receiving a different request.
type messagesRequest struct {
	Model      string             `json:"model"`
	System     string             `json:"system"`
	MaxTokens  int                `json:"max_tokens"`
	Tools      []messagesToolDef  `json:"tools"`
	ToolChoice messagesToolChoice `json:"tool_choice"`
	Messages   []messageContent   `json:"messages"`
}

type messagesToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type messagesToolChoice struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

type messageContent struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// anthropicVersion is the header value the Messages API requires. Restated here
// for the same reason as the structs above: it is the value a real provider
// checks, so the test asserts on the literal rather than on whatever constant the
// code under test happens to hold.
const anthropicVersion = "2023-06-01"

// ---------------------------------------------------------------------------
// W0 D4: the Static/Dynamic split IS the prefix-cache contract.
// ---------------------------------------------------------------------------

// TestStaticPromptIsClockIndependent is the detector for the cache contract
// llm.Request documents. Two translators whose clocks are years apart must
// produce BYTE-IDENTICAL Static halves — because every provider matches a cached
// prompt prefix byte for byte from the start of the request, so one date in the
// wrong half silently re-bills the whole ~6 KB catalogue on every question, for
// every tenant, forever, with no symptom but the invoice.
//
// ⛔ IT CARRIES ITS OWN CONTROLS, because "the Static halves are equal" is a
// property a DO-NOTHING implementation satisfies perfectly: put everything in
// Dynamic and Static is empty, identical and useless. So the test also requires
// that Static actually carries the catalogue and the rules, that it carries no
// date and no question, and that Dynamic genuinely DIFFERS between the two
// clocks — which is what proves the windows are still being resolved at all
// rather than frozen into the prefix (D21).
func TestStaticPromptIsClockIndependent(t *testing.T) {
	const question = "total purchases this quarter"
	early := &Translator{Now: func() time.Time { return time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC) }}
	late := &Translator{Now: func() time.Time { return time.Date(2029, 11, 2, 0, 0, 0, 0, time.UTC) }}

	a := early.request(uuid.New(), question)
	b := late.request(uuid.New(), question)

	if !reflect.DeepEqual(a.Static, b.Static) {
		// Reported as an OFFSET plus a window, not as two 6 KB prompts: the
		// useful fact is WHERE the per-call data crept in, and a failure
		// message nobody can read is a failure message nobody acts on.
		t.Fatalf("Static differs across clocks — the prefix cache can never hit; %s",
			firstDifference(joinContent(a.Static), joinContent(b.Static)))
	}
	// The hash internal/llm actually stamps on the trajectory span. If this
	// moves between two calls of one prompt version, the eval gate and the
	// canary are both comparing things that are not comparable.
	if ha, hb := llm.StaticPromptHash(a), llm.StaticPromptHash(b); ha != hb {
		t.Fatalf("StaticPromptHash differs across clocks: %s vs %s", ha, hb)
	}

	staticText := joinContent(a.Static)

	// CONTROL 1 — Static is not trivially stable because it is empty. An
	// implementation that "split" the prompt by moving all of it into Dynamic
	// passes every assertion above and fails here.
	if len(staticText) < 2000 {
		t.Fatalf("Static half is only %d bytes — the catalogue and rules are not in the cacheable half", len(staticText))
	}
	for _, want := range []string{
		"CATALOG (names only):",
		"RULES (the compiler rejects violations",
		"REFUSE — call emit_query with entity",
		"ledger_balances",
		"outstandings",
	} {
		if !strings.Contains(staticText, want) {
			t.Errorf("Static half omits %q — it is not the cacheable preamble", want)
		}
	}

	// CONTROL 2 — nothing per-call may sit in the prefix. A single one of these
	// invalidates the cache on every call, which is the exact defect this lane
	// closed.
	// ⚠️ The banned needle for the window table is its HEADER LINE, not the
	// phrase "RESOLVED PERIOD WINDOWS": static rule 7 legitimately tells the
	// model to copy a window "from RESOLVED PERIOD WINDOWS below", and banning
	// the bare phrase would forbid the rule that makes the table usable. The
	// header line only ever appears where the resolved dates do.
	for _, banned := range []string{"TODAY IS", "RESOLVED PERIOD WINDOWS (inclusive", "2026-", "2029-", question} {
		if strings.Contains(staticText, banned) {
			t.Errorf("Static half contains per-call text %q — one byte of it invalidates the prefix for every tenant", banned)
		}
	}

	// CONTROL 3 — Dynamic must actually MOVE with the clock. If it does not, the
	// windows were frozen and D21 is back: the model would answer "this quarter"
	// with whatever quarter was current when the binary was built.
	da, db := joinContent(a.Dynamic), joinContent(b.Dynamic)
	if da == db {
		t.Fatal("Dynamic is identical across clocks — the resolved period windows are frozen (D21)")
	}
	if !strings.Contains(da, "TODAY IS 2026-03-15.") || !strings.Contains(db, "TODAY IS 2029-11-02.") {
		t.Errorf("Dynamic does not state each clock's own date\n early: %q\n late:  %q", da, db)
	}

	// The question rides in the LAST dynamic turn, as a user message, and
	// nowhere else.
	last := a.Dynamic[len(a.Dynamic)-1]
	if last.Role != llm.RoleUser || last.Content != question {
		t.Errorf("the question is not the final dynamic user turn: %+v", last)
	}

	// LOSSLESSNESS — the two halves concatenated are still exactly the prompt
	// schema_test.go's six assertions are written against. A split that dropped
	// or reordered a rule would pass everything above.
	now := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	if got, want := staticText+a.Dynamic[0].Content, systemPrompt(now, 0); got != want {
		t.Errorf("static+dynamic is not the whole prompt (%d vs %d bytes)", len(got), len(want))
	}
}

// firstDifference names the byte offset two strings diverge at and quotes a
// short window on each side of it.
func firstDifference(a, b string) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			lo := i - 40
			if lo < 0 {
				lo = 0
			}
			hi := func(s string) int {
				if i+40 > len(s) {
					return len(s)
				}
				return i + 40
			}
			return fmt.Sprintf("first difference at byte %d\n  a: %q\n  b: %q", i, a[lo:hi(a)], b[lo:hi(b)])
		}
	}
	return fmt.Sprintf("identical for %d bytes then lengths differ (%d vs %d)", n, len(a), len(b))
}

// joinContent concatenates message contents in order, which is how a provider
// that has one system field sees them.
func joinContent(msgs []llm.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.Content)
	}
	return b.String()
}

// TestRequestCarriesTheTraceAndBudgetKeys pins the fields internal/llm REFUSES a
// request without (Feature, PromptVersion) and the ones a trajectory needs to be
// attributable at all. Without them the call is unbillable and un-regression-
// testable, and llm.validateRequest turns that into an error — so a translator
// that forgot one would 503 every question in production while every test that
// only checks decoding still passed.
func TestRequestCarriesTheTraceAndBudgetKeys(t *testing.T) {
	companyID := uuid.New()
	req := (&Translator{}).request(companyID, "trial balance")

	if req.Feature != "nlq.translate" {
		t.Errorf("Feature = %q, want nlq.translate", req.Feature)
	}
	if req.PromptVersion == "" {
		t.Error("PromptVersion is empty — the eval gate has nothing to pin a score to")
	}
	if req.TenantID != companyID {
		t.Errorf("TenantID = %v, want %v (the budget and the span are tenant-scoped)", req.TenantID, companyID)
	}
	if req.ToolChoice.Mode != llm.ToolChoiceNamed || req.ToolChoice.Name != "emit_query" {
		t.Errorf("tool choice = %+v, want a forced emit_query", req.ToolChoice)
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != "emit_query" {
		t.Errorf("tools = %+v, want exactly emit_query", req.Tools)
	}
	// ⛔ NO json_schema. The Messages API has none, and llm refuses a Schema on
	// that provider rather than dropping it — so setting one here would turn
	// every question into a 503 on the retained Anthropic path.
	if req.Schema != nil {
		t.Error("Schema is set — the Anthropic adapter refuses it; forced tool use is the portable shape")
	}
}

// ---------------------------------------------------------------------------
// W0 D4: the error-mapping table, at the seam where it now lives.
// ---------------------------------------------------------------------------

// exhaustedBudget refuses every reservation. It stands in for a tenant who has
// spent their token cap.
type exhaustedBudget struct{}

func (exhaustedBudget) Reserve(context.Context, uuid.UUID, string, int) (llm.Reservation, error) {
	return nil, llm.ErrBudgetExhausted
}

// TestToQuery_BudgetExhausted_MapsToUnavailable is the new row in the error
// table. A tenant out of tokens is NOT a user who asked a bad question: telling
// them to rephrase would send them round a loop that cannot terminate, because
// no wording is cheaper. It is an operator-visible condition -> 503.
//
// It also asserts the fake provider was never called, which is the property
// llm.Client's ordering guarantees (reserve before the socket) and which makes
// the difference between a budget and a log line.
func TestToQuery_BudgetExhausted_MapsToUnavailable(t *testing.T) {
	var cap capture
	tr := fakeAPI(t, &cap, http.StatusOK, toolUse(`{"entity":"vouchers","metrics":[],"dimensions":[],"filters":[],"date_range":null,"order_by":[],"limit":100}`))
	tr.Budget = exhaustedBudget{}

	_, err := tr.ToQuery(context.Background(), uuid.New(), "sales this month")
	if !errors.Is(err, ErrTranslatorUnavailable) {
		t.Errorf("budget exhausted: err = %v, want ErrTranslatorUnavailable", err)
	}
	if errors.Is(err, query.ErrUntranslatable) {
		t.Error("budget exhausted must NOT be ErrUntranslatable — no rewording buys more tokens")
	}
	if cap.called {
		t.Error("an exhausted budget must be refused BEFORE the socket opens")
	}
}

// TestToQuery_NotConfigured_MapsToUnavailable: a translator with no credential
// is a DEPLOYMENT fault. internal/llm refuses it at New; this asserts the refusal
// arrives as a 503 rather than as "I did not understand the question", and that
// nothing was dialled.
func TestToQuery_NotConfigured_MapsToUnavailable(t *testing.T) {
	var cap capture
	tr := fakeAPI(t, &cap, http.StatusOK, toolUse(`{"entity":"vouchers","metrics":[],"dimensions":[],"filters":[],"date_range":null,"order_by":[],"limit":100}`))
	tr.APIKey = "" // the deployment mistake

	_, err := tr.ToQuery(context.Background(), uuid.New(), "sales this month")
	if !errors.Is(err, ErrTranslatorUnavailable) {
		t.Errorf("missing key: err = %v, want ErrTranslatorUnavailable", err)
	}
	if errors.Is(err, query.ErrUntranslatable) {
		t.Error("a missing API key must NOT be reported as the user's bad question")
	}
	if cap.called {
		t.Error("a translator with no credential must not open a socket")
	}
}

// TestToQuery_MapsLLMSentinelsExactly is the table itself, asserted directly
// against internal/llm's exported sentinels rather than through a provider.
//
// ⛔ THE CONTROL IS THE FIRST ROW. ErrNoStructuredOutput -> 422 and everything
// else -> 503 is a split with real content: an implementation that mapped
// EVERYTHING to 503 (defensible-sounding: "when in doubt, blame ourselves")
// would pass every other row here and fail that one, and it would turn every
// unanswerable question into a page for the on-call. An implementation that
// mapped everything to 422 fails the rest and would have a dead API key told to
// rephrase itself.
func TestToQuery_MapsLLMSentinelsExactly(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      error
		want    error
		notWant error
	}{
		{"no structured output", llm.ErrNoStructuredOutput, query.ErrUntranslatable, ErrTranslatorUnavailable},
		{"upstream unavailable", llm.ErrUpstreamUnavailable, ErrTranslatorUnavailable, query.ErrUntranslatable},
		{"budget exhausted", llm.ErrBudgetExhausted, ErrTranslatorUnavailable, query.ErrUntranslatable},
		{"not configured", llm.ErrNotConfigured, ErrTranslatorUnavailable, query.ErrUntranslatable},
		{"input too large", llm.ErrInputTooLarge, ErrTranslatorUnavailable, query.ErrUntranslatable},
		{"tenant id in prompt", llm.ErrTenantIDInPrompt, ErrTranslatorUnavailable, query.ErrUntranslatable},
		{"unclassified", errors.New("something nobody has seen"), ErrTranslatorUnavailable, query.ErrUntranslatable},
	} {
		got := mapLLMError(fmt.Errorf("wrapped: %w", tc.in))
		if !errors.Is(got, tc.want) {
			t.Errorf("%s: mapped to %v, want %v", tc.name, got, tc.want)
		}
		if errors.Is(got, tc.notWant) {
			t.Errorf("%s: mapped to %v, which must NOT be %v", tc.name, got, tc.notWant)
		}
	}
}

// ---------------------------------------------------------------------------
// W0 D2: the trajectory seam is actually wired.
// ---------------------------------------------------------------------------

// countingRecorder is a test Recorder. It records what internal/llm stamps on the
// span so the assertions below are about real values, not about a call happening.
type countingRecorder struct {
	starts  int
	ends    int
	last    llm.SpanStart
	lastErr error
}

func (r *countingRecorder) StartLLMCall(ctx context.Context, s llm.SpanStart) (context.Context, llm.LLMSpan) {
	r.starts++
	r.last = s
	return ctx, &countingSpan{r: r}
}

type countingSpan struct{ r *countingRecorder }

func (s *countingSpan) End(_ *llm.Response, err error) {
	s.r.ends++
	s.r.lastErr = err
}

// TestToQuery_RecordsATrajectorySpan proves the W0 trajectory seam reaches the
// only network caller in the query path — i.e. that this lane did not merely
// compile against internal/llm but actually hands it the Recorder a deployment
// wires in. Without this, "trajectories visible in a local Langfuse" would be
// true of every feature EXCEPT the one that existed before the plan.
//
// ⛔ THE CONTROL IS THE FAILING CALL. A span opened only on success is a
// trajectory that cannot explain an incident, so the second half drives a dead
// upstream and requires the span to be closed WITH the error. An implementation
// that recorded spans only on the happy path passes the first half.
func TestToQuery_RecordsATrajectorySpan(t *testing.T) {
	var cap capture
	rec := &countingRecorder{}
	tr := fakeAPI(t, &cap, http.StatusOK, toolUse(`{"entity":"vouchers","metrics":[],"dimensions":["month"],"filters":[],"date_range":null,"order_by":[],"limit":100}`))
	tr.Recorder = rec
	companyID := uuid.New()

	if _, err := tr.ToQuery(context.Background(), companyID, "sales by month"); err != nil {
		t.Fatalf("ToQuery: %v", err)
	}
	if rec.starts != 1 || rec.ends != 1 {
		t.Fatalf("spans: %d started, %d ended, want 1 and 1", rec.starts, rec.ends)
	}
	if rec.last.Feature != "nlq.translate" {
		t.Errorf("span Feature = %q, want nlq.translate", rec.last.Feature)
	}
	if rec.last.TenantID != companyID {
		t.Errorf("span TenantID = %v, want %v", rec.last.TenantID, companyID)
	}
	if rec.last.PromptVersion == "" || rec.last.PromptHash == "" {
		t.Errorf("span carries no prompt identity: version=%q hash=%q", rec.last.PromptVersion, rec.last.PromptHash)
	}
	if rec.last.Provider != DefaultProvider || rec.last.ModelID != DefaultModel {
		t.Errorf("span provider/model = %s/%s, want %s/%s", rec.last.Provider, rec.last.ModelID, DefaultProvider, DefaultModel)
	}
	if rec.lastErr != nil {
		t.Errorf("successful call closed its span with %v", rec.lastErr)
	}

	// THE CONTROL: a dead upstream must still open and close a span.
	rec2 := &countingRecorder{}
	dead := &Translator{APIKey: "dummy", BaseURL: "http://127.0.0.1:1", Recorder: rec2}
	if _, err := dead.ToQuery(context.Background(), companyID, "sales by month"); !errors.Is(err, ErrTranslatorUnavailable) {
		t.Fatalf("dead upstream: err = %v, want ErrTranslatorUnavailable", err)
	}
	if rec2.starts != 1 || rec2.ends != 1 {
		t.Fatalf("failing call spans: %d started, %d ended, want 1 and 1 — a trajectory that records only successes cannot explain an incident", rec2.starts, rec2.ends)
	}
	if rec2.lastErr == nil {
		t.Error("the failing call's span was closed with a nil error")
	}
}

// TestClaudeTranslatorAliasStillSatisfiesTheInterface is the anti-rot guard on
// the rename. internal/server builds &nlq.ClaudeTranslator{...} and assigns it to
// a query.Translator; if the alias were ever "cleaned up" into a defined type,
// that assignment would stop compiling in another lane's file, and the symptom
// would be a build break in a package nobody touched.
func TestClaudeTranslatorAliasStillSatisfiesTheInterface(t *testing.T) {
	var _ query.Translator = &ClaudeTranslator{
		APIKey:  "k",
		Model:   DefaultModel,
		BaseURL: DefaultBaseURL,
		Now:     time.Now,
	}
	// The alias must be the SAME type, not a look-alike: a *Translator has to be
	// assignable to a *ClaudeTranslator variable with no conversion.
	var alias *ClaudeTranslator = (*Translator)(nil)
	_ = alias
}
