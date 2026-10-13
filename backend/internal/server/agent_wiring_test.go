package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/khata/cored/internal/llm"
	"github.com/khata/cored/internal/nlq"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/query"
	"github.com/khata/cored/internal/trajectory"
)

// This file covers the WIRING that W0 added to the composition root, and only
// the wiring: internal/llm, internal/trajectory and internal/actions each carry
// their own suites for their own behaviour. What cannot be tested anywhere but
// here is the three-way join — that a provider selection is refused out loud
// rather than degraded, that the shipped default is still hermetic, that the
// adapter between two packages which deliberately do not know about each other
// carries the right fields and none of the wrong ones, and that a request's
// identity reaches a span at all.
//
// ⛔ NONE OF IT NEEDS A DATABASE OR A NETWORK, and that is a requirement rather
// than a happy accident: these are the assertions that must still run in the CI
// job that has neither, because they are the ones that fail when somebody
// "simplifies" the boot path.

// ---------------------------------------------------------------------------
// buildLLM: a half-configured provider is a refusal, not a degradation.
// ---------------------------------------------------------------------------

// TestBuildLLMRefusesAHalfConfiguredProvider pins the loud half of the contract.
//
// ⚠️ THE LAST SUBTEST IS THE CONTROL AND THE SUITE IS WORTHLESS WITHOUT IT. Every
// assertion above it is satisfied by a buildLLM that refuses EVERYTHING —
// including a correct configuration — which would present in the field as "the
// agent plane cannot be turned on" rather than as a test failure. The control is
// the case that must be ACCEPTED.
func TestBuildLLMRefusesAHalfConfiguredProvider(t *testing.T) {
	cases := []struct {
		name       string
		env        map[string]string
		wantRefuse bool
		wantSubstr string
	}{
		{
			name:       "a provider this binary cannot speak to",
			env:        map[string]string{"LLM_PROVIDER": "fireworks.ai", "LLM_API_KEY": "k", "LLM_MODEL": "m"},
			wantRefuse: true,
			// The message must name the mistake AND say what it refused to do
			// instead, because the fallback it is refusing (FakeTranslator) is
			// the one an operator would otherwise never notice.
			wantSubstr: "FakeTranslator",
		},
		{
			name:       "a provider with no model id",
			env:        map[string]string{"LLM_PROVIDER": "fireworks", "LLM_API_KEY": "k"},
			wantRefuse: true,
			wantSubstr: "LLM_MODEL",
		},
		{
			name:       "a hosted provider with no credential",
			env:        map[string]string{"LLM_PROVIDER": "fireworks", "LLM_MODEL": "accounts/x/models/y"},
			wantRefuse: true,
			wantSubstr: "incomplete",
		},
		{
			// ⚠️ NOT A DUPLICATE OF THE CASE ABOVE. vLLM is the one provider
			// llm.New exempts from needing a key, and the exemption lives THERE
			// rather than being restated here — this case is what proves
			// buildLLM asks the real constructor instead of carrying its own
			// copy of the rule. It also proves the missing piece is the HOST: a
			// self-hosted server has no default one.
			name:       "a self-hosted vllm with no base url",
			env:        map[string]string{"LLM_PROVIDER": "vllm", "LLM_MODEL": "Qwen3-8B"},
			wantRefuse: true,
			wantSubstr: "base URL",
		},
		{
			name: "CONTROL: a complete selection is ACCEPTED",
			env: map[string]string{
				"LLM_PROVIDER": "vllm",
				"LLM_MODEL":    "Qwen3-8B",
				"LLM_BASE_URL": "http://127.0.0.1:8000",
			},
			wantRefuse: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearLLMEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			set, configured, err := buildLLM()
			if tc.wantRefuse {
				if err == nil {
					t.Fatalf("buildLLM accepted %v; a half-configured provider must REFUSE, because the "+
						"alternative is a deployment that answers questions with a keyword matcher while "+
						"the operator believes a model is running", tc.env)
				}
				if configured {
					t.Errorf("buildLLM refused but also reported configured=true")
				}
				if !strings.Contains(err.Error(), tc.wantSubstr) {
					t.Errorf("refusal message does not name the fix: got %q, want it to contain %q", err, tc.wantSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildLLM refused a COMPLETE configuration %v: %v — every assertion above this "+
					"case is also satisfied by a buildLLM that refuses everything, which is why this "+
					"case exists", tc.env, err)
			}
			if !configured {
				t.Fatalf("buildLLM accepted %v but reported configured=false", tc.env)
			}
			if set.Provider != llm.ProviderVLLM || set.Model != "Qwen3-8B" {
				t.Errorf("settings did not survive the parse: %+v", set)
			}
		})
	}
}

// TestUnsetLLMProviderIsNotAProvider is the other half of the boot contract: an
// empty LLM_PROVIDER means "no provider selected", not "the zero-value provider".
func TestUnsetLLMProviderIsNotAProvider(t *testing.T) {
	clearLLMEnv(t)
	set, configured, err := buildLLM()
	if err != nil {
		t.Fatalf("an unconfigured deployment must not be an error: %v", err)
	}
	if configured {
		t.Fatalf("configured=true with LLM_PROVIDER unset: %+v", set)
	}
}

// TestLLMTimeoutIsLoggedNotHonouredWhenUnparseable pins the statutorydTimeout
// shape: a mistyped duration must not become a zero timeout (which http.Client
// reads as NO deadline — the opposite of what the operator asked for).
func TestLLMTimeoutIsLoggedNotHonouredWhenUnparseable(t *testing.T) {
	clearLLMEnv(t)
	t.Setenv("LLM_TIMEOUT", "20 seconds")
	if d := llmTimeout(); d != 0 {
		t.Fatalf("llmTimeout()=%s for a bad value; want 0 (meaning the feature's own default)", d)
	}
	// CONTROL: a good value must be honoured, or the assertion above is
	// satisfied by an llmTimeout that always returns 0.
	t.Setenv("LLM_TIMEOUT", "22s")
	if d := llmTimeout(); d.String() != "22s" {
		t.Fatalf("llmTimeout()=%s for %q; want 22s", d, "22s")
	}
}

// ---------------------------------------------------------------------------
// The shipped default stays hermetic.
// ---------------------------------------------------------------------------

// TestTheShippedDefaultTranslatorIsHermetic is the gate the whole CI suite rests
// on: with no keys in the environment, buildTranslator must return the
// deterministic FakeTranslator and must not refuse to boot.
//
// ⚠️ THE SECOND HALF IS THE CONTROL. "returns FakeTranslator" is satisfied by a
// buildTranslator that ALWAYS returns FakeTranslator — which would silently
// disable the real translator for every deployment that has one configured, and
// no other test in this repo would notice. So the pre-W0 path is asserted to
// select the real one.
func TestTheShippedDefaultTranslatorIsHermetic(t *testing.T) {
	clearLLMEnv(t)
	tr, err := buildTranslator(nil)
	if err != nil {
		t.Fatalf("an unconfigured binary must boot: %v", err)
	}
	if _, ok := tr.(query.FakeTranslator); !ok {
		t.Fatalf("with no keys set, buildTranslator returned %T; want query.FakeTranslator. Every CI "+
			"gate in this repo runs with no keys and no network", tr)
	}

	// CONTROL (pre-W0 path): ANTHROPIC_API_KEY alone still selects the real one.
	t.Setenv("ANTHROPIC_API_KEY", "dummy-key-for-test")
	tr, err = buildTranslator(nil)
	if err != nil {
		t.Fatalf("the pre-W0 path must still boot: %v", err)
	}
	real, ok := tr.(*nlq.Translator)
	if !ok {
		t.Fatalf("with ANTHROPIC_API_KEY set, buildTranslator returned %T; want *nlq.Translator", tr)
	}
	// ⛔ AND IT MUST CARRY THE W0 SEAMS. A re-point that left Recorder nil would
	// leave the ONE feature that already makes model calls invisible to the
	// trajectory store — a hole exactly where the existing traffic is.
	if real.Recorder == nil {
		t.Error("the translator was built with no Recorder: existing NL-query traffic would record no trajectory")
	}
	if real.Budget == nil {
		t.Error("the translator was built with no Budget: llm resolves nil to NoBudget, but the composition root is what must SAY so")
	}
}

// TestConfiguredProviderDoesNotInheritAnthropicsHost is the trap lane F named:
// passing nlq.DefaultBaseURL unconditionally would POST a Fireworks request to
// api.anthropic.com, where the failure reads as a 404 on a model id.
func TestConfiguredProviderDoesNotInheritAnthropicsHost(t *testing.T) {
	clearLLMEnv(t)
	t.Setenv("LLM_PROVIDER", "fireworks")
	t.Setenv("LLM_API_KEY", "dummy")
	t.Setenv("LLM_MODEL", "accounts/fireworks/models/x")
	// QUERY_TRANSLATOR_BASE_URL is the PRE-W0 seam and must not leak into the
	// W0 path — a stale value in a deployment's env would otherwise silently
	// redirect a newly selected provider.
	t.Setenv("QUERY_TRANSLATOR_BASE_URL", "https://api.anthropic.com")

	tr, err := buildTranslator(nil)
	if err != nil {
		t.Fatalf("buildTranslator: %v", err)
	}
	real, ok := tr.(*nlq.Translator)
	if !ok {
		t.Fatalf("got %T, want *nlq.Translator", tr)
	}
	if real.BaseURL != "" {
		t.Fatalf("BaseURL=%q for provider %s; want empty so the chosen adapter supplies its own host",
			real.BaseURL, real.Provider)
	}
	if real.Provider != llm.ProviderFireworks {
		t.Fatalf("Provider=%q; want fireworks", real.Provider)
	}
}

// clearLLMEnv makes each case independent of the machine it runs on. t.Setenv
// restores on cleanup, so this is safe and it is also why none of these tests
// may be t.Parallel.
func clearLLMEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"LLM_PROVIDER", "LLM_API_KEY", "LLM_MODEL", "LLM_BASE_URL", "LLM_TIMEOUT",
		"LLM_BUDGET_TOKENS_PER_MONTH", "ANTHROPIC_API_KEY",
		"QUERY_TRANSLATOR_MODEL", "QUERY_TRANSLATOR_BASE_URL",
	} {
		t.Setenv(k, "")
	}
}

// ---------------------------------------------------------------------------
// The trajectory adapter: the right fields, and none of the wrong ones.
// ---------------------------------------------------------------------------

type capturingRecorder struct{ records []trajectory.Record }

func (c *capturingRecorder) Record(sp trajectory.Record) { c.records = append(c.records, sp) }

// TestTrajectoryAdapterCarriesTheFingerprintAndNoContent exercises the six-line
// adapter that exists solely because internal/llm and internal/trajectory do not
// import each other. Two things must hold at once, and each is the other's
// control:
//
//	POSITIVE — the span carries what an incident needs: the provider, the model
//	that ANSWERED (not the one requested), the fingerprint, the token counts and
//	the repair count. Without this half, "no content escaped" is satisfied by an
//	adapter that records nothing at all.
//
//	NEGATIVE — no completion text, no prompt, no error MESSAGE reaches an
//	attribute. Without this half, the positive assertions are satisfied by an
//	adapter that copies the whole Response onto the span.
func TestTrajectoryAdapterCarriesTheFingerprintAndNoContent(t *testing.T) {
	cap := &capturingRecorder{}
	trajectory.SetRecorder(cap)
	// ⛔ RESTORE THE NOP RECORDER. SetRecorder is process-global; leaving a
	// capturing recorder installed would make every later test in this package
	// allocate spans and would make the zero-allocation guarantee untestable.
	defer trajectory.SetRecorder(trajectory.Nop())

	tenant := uuid.New()
	user := uuid.New()
	ctx := trajectory.WithScope(context.Background(), trajectory.Scope{
		TenantID: tenant,
		UserID:   user,
		Subject:  "operator@example.test",
		Action:   "display",
		Resource: "report:day-book",
	})

	const secret = "Acme Traders private ledger question about outstandings"
	_, span := trajectoryRecorder{}.StartLLMCall(ctx, llm.SpanStart{
		TenantID:      tenant,
		Feature:       "nlq.translate",
		PromptVersion: "nlq-dsl-v2",
		PromptHash:    "0f1e2d3c",
		Provider:      llm.ProviderAnthropic,
		ModelID:       "claude-haiku-4-5",
	})
	span.End(&llm.Response{
		Text:         secret,
		FinishReason: "tool_use",
		Usage:        llm.Usage{PromptTokens: 2100, CachedPromptTokens: 1800, CompletionTokens: 64},
		Fingerprint: llm.Fingerprint{
			Provider:          llm.ProviderAnthropic,
			RequestedModel:    "claude-haiku-4-5",
			ServedModel:       "claude-haiku-4-5-20251001",
			SystemFingerprint: "fp_abc123",
			ResponseID:        "msg_01",
		},
		Repairs: 1,
	}, errors.New("provider said: "+secret))

	if len(cap.records) != 1 {
		t.Fatalf("recorded %d spans, want exactly 1 — an adapter that records nothing passes every "+
			"content assertion below", len(cap.records))
	}
	rec := cap.records[0]
	attrs := rec.Attributes

	// POSITIVE half.
	want := map[string]any{
		trajectory.AttrTenantID:              tenant.String(),
		trajectory.AttrEnduserID:             user.String(),
		trajectory.AttrAction:                "display",
		trajectory.AttrResource:              "report:day-book",
		trajectory.AttrFeature:               "nlq.translate",
		trajectory.AttrGenAIProviderName:     string(llm.ProviderAnthropic),
		trajectory.AttrGenAIRequestModel:     "claude-haiku-4-5",
		trajectory.AttrGenAIResponseModel:    "claude-haiku-4-5-20251001",
		trajectory.AttrModelFingerprint:      "fp_abc123",
		trajectory.AttrGenAIResponseID:       "msg_01",
		trajectory.AttrPromptVersion:         "nlq-dsl-v2",
		trajectory.AttrPromptHash:            "0f1e2d3c",
		trajectory.AttrGenAIUsageInputTokens: int64(2100),
		trajectory.AttrCachedInputTokens:     int64(1800),
		trajectory.AttrSchemaRepairAttempts:  int64(1),
	}
	for k, v := range want {
		got, ok := attrs[k]
		if !ok {
			t.Errorf("span is missing %s — an incident cannot be explained without it", k)
			continue
		}
		if got != v {
			t.Errorf("%s = %v (%T), want %v (%T)", k, got, got, v, v)
		}
	}
	if rec.Status != "ERROR" {
		t.Errorf("status = %q for a call that returned an error; want ERROR", rec.Status)
	}
	if _, ok := attrs[trajectory.AttrErrorType]; !ok {
		t.Errorf("an errored call recorded no %s", trajectory.AttrErrorType)
	}
	if v, ok := attrs[trajectory.AttrScopeMissing]; ok {
		t.Errorf("%s=%v on a span started under a Scope", trajectory.AttrScopeMissing, v)
	}

	// NEGATIVE half. The completion text was handed to the adapter and the error
	// message quoted it back; neither may appear anywhere in the recorded span.
	for k, v := range attrs {
		s, isStr := v.(string)
		if !isStr {
			continue
		}
		if strings.Contains(s, secret) || strings.Contains(s, "Acme") {
			t.Errorf("attribute %s carries model/provider CONTENT: %q. Content must never be an "+
				"attribute — it is not redacted, not size-capped in the way a prompt needs, and it "+
				"leaves the building with the trace", k, s)
		}
	}
	if strings.Contains(rec.Name, secret) {
		t.Errorf("span NAME carries content: %q", rec.Name)
	}
}

// ---------------------------------------------------------------------------
// The scope middleware.
// ---------------------------------------------------------------------------

// TestTrajectoryScopeMiddlewarePopulatesTheScope drives the real middleware over
// REAL route patterns taken from the REAL router, so a route whose routeAction
// arm changes shape shows up here rather than as a mis-attributed trace row.
//
// It runs the middleware directly rather than through the whole stack because
// companyAuthMiddleware needs a database and a token; what is under test is the
// copy from the request context to the Scope, and that is exactly what running
// it directly exercises.
func TestTrajectoryScopeMiddlewarePopulatesTheScope(t *testing.T) {
	company := uuid.New()
	user := uuid.New()
	const subject = "operator@example.test"

	cases := []struct {
		method, pattern string
		wantAction      string
		wantResource    string
	}{
		// ⚠️ THE EXPECTATIONS BELOW ARE THE ROUTER'S, NOT A GUESS. A voucher's
		// resource token is the CLASS itself (`create sales`), not `voucher:sales`
		// — the first draft of this test assumed otherwise and was wrong, which
		// is precisely the drift a trace row derived from a screen name would
		// have shipped. Cross-check against frontend/src/api/goldens/
		// action-registry.json, which is generated from the same routeAction.
		{"POST", "/v1/companies/{companyID}/vouchers/sales", "create", "sales"},
		// THE CONTROL FOR THE RENDERER. A report route must carry the NAME after
		// the colon; a plain resource must NOT gain one. An implementation that
		// dropped the name would pass every other assertion here (and would make
		// every report trace row say the same thing), and one that always
		// appended a colon would fail the row below it.
		//
		// Note the PATH: the Day Book is mounted at /{companyID}/day-book and not
		// under a /reports/ prefix. Another assumption this test got wrong first.
		{"GET", "/v1/companies/{companyID}/day-book", "display", "report:day-book"},
		{"GET", "/v1/companies/{companyID}/backup", "display", "backup"},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.pattern, func(t *testing.T) {
			path, rctx := synthPath(tc.pattern, company.String())
			ctx := context.WithValue(context.Background(), chi.RouteCtxKey, rctx)
			ctx = db.WithSubject(ctx, subject)
			ctx = db.WithUser(ctx, user)
			ctx = db.WithCompany(ctx, company)
			req := httptest.NewRequest(tc.method, path, nil).WithContext(ctx)

			var got trajectory.Scope
			var found bool
			h := trajectoryScopeMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got, found = trajectory.ScopeFromContext(r.Context())
			}))
			h.ServeHTTP(httptest.NewRecorder(), req)

			if !found {
				t.Fatal("no Scope in the handler's context: every span below this route would be " +
					"unattributed, which looks exactly like a correct row")
			}
			if got.TenantID != company {
				t.Errorf("TenantID = %s, want %s", got.TenantID, company)
			}
			if got.UserID != user {
				t.Errorf("UserID = %s, want %s", got.UserID, user)
			}
			if got.Subject != subject {
				t.Errorf("Subject = %q, want %q", got.Subject, subject)
			}
			if got.Action != tc.wantAction {
				t.Errorf("Action = %q, want %q", got.Action, tc.wantAction)
			}
			if got.Resource != tc.wantResource {
				t.Errorf("Resource = %q, want %q — the trace row must spell the resource the way an "+
					"ACL RULE spells it, or the two cannot be joined", got.Resource, tc.wantResource)
			}
		})
	}
}

// TestTrajectoryScopeOmitsTheUserForAServicePrincipal pins the one identity
// shape that is a normal state rather than a defect: a `svc:` token has a subject
// and no user row, and uuid.Nil must travel as "omit", not as a person whose id
// happens to be all zeroes.
func TestTrajectoryScopeOmitsTheUserForAServicePrincipal(t *testing.T) {
	company := uuid.New()
	path, rctx := synthPath("/v1/companies/{companyID}/sync/commit", company.String())
	ctx := context.WithValue(context.Background(), chi.RouteCtxKey, rctx)
	ctx = db.WithSubject(ctx, "svc:statutoryd")
	ctx = db.WithCompany(ctx, company)
	req := httptest.NewRequest("POST", path, nil).WithContext(ctx)

	var got trajectory.Scope
	h := trajectoryScopeMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = trajectory.ScopeFromContext(r.Context())
	}))
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got.UserID != uuid.Nil {
		t.Errorf("UserID = %s for a service principal; want uuid.Nil", got.UserID)
	}
	if got.Subject != "svc:statutoryd" {
		t.Errorf("Subject = %q; a service principal's subject is the ONLY identity it has", got.Subject)
	}
	// CONTROL: the route pair must still be resolved, or this test would pass on
	// a middleware that gave up entirely when there was no user.
	if got.Action == "" || got.Resource == "" {
		t.Errorf("service-principal request carried no action/resource pair: %q %q", got.Action, got.Resource)
	}
}

// ---------------------------------------------------------------------------
// The spend control
// ---------------------------------------------------------------------------

// TestMalformedBudgetRefusesToBoot pins the class buildBudget's env var belongs
// to.
//
// ⛔ IT SHIPPED AS A LOGGED FALLBACK TO NoBudget, which means
// LLM_BUDGET_TOKENS_PER_MONTH="100_000" — or a trailing space, or "2m" — gave an
// UNCAPPED deployment whose operator believed a cap was in force. That is the
// same failure the deleted `export` ACL token is a monument to: a control that
// validates, is shown as accepted, and governs nothing. A cost/abuse cap is in
// the class of LLM_PROVIDER (refuse to boot), not the class of INSIGHTD_TIMEOUT
// (degrade to a stated default of the same kind) — because the degradation here
// is not a different number, it is the ABSENCE of the control.
func TestMalformedBudgetRefusesToBoot(t *testing.T) {
	for _, raw := range []string{"100_000", "2m", "-1", "0", "1e6", " 200000"} {
		clearLLMEnv(t)
		t.Setenv("LLM_BUDGET_TOKENS_PER_MONTH", raw)
		tr, err := buildTranslator(nil)
		if err == nil {
			t.Errorf("LLM_BUDGET_TOKENS_PER_MONTH=%q booted (translator %T). An operator who "+
				"set a cap and got none has a spend control that is not where they think it is",
				raw, tr)
			continue
		}
		if !strings.Contains(err.Error(), "LLM_BUDGET_TOKENS_PER_MONTH") {
			t.Errorf("the refusal for %q does not name the variable: %v", raw, err)
		}
	}

	// CONTROL 1: a well-formed cap boots and is ENFORCED — a refusal that fired
	// on everything would satisfy the loop above while making the feature
	// unusable.
	clearLLMEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "dummy-key-for-test")
	t.Setenv("LLM_BUDGET_TOKENS_PER_MONTH", "2000000")
	tr, err := buildTranslator(nil)
	if err != nil {
		t.Fatalf("a well-formed cap must boot: %v", err)
	}
	real, ok := tr.(*nlq.Translator)
	if !ok {
		t.Fatalf("buildTranslator returned %T, want *nlq.Translator", tr)
	}
	pg, ok := real.Budget.(*llm.PGBudget)
	if !ok {
		t.Fatalf("Budget is %T, want *llm.PGBudget — a parsed cap that produced NoBudget would "+
			"be the original defect with extra steps", real.Budget)
	}
	if pg.DefaultLimitTokens != 2_000_000 {
		t.Errorf("DefaultLimitTokens = %d, want 2000000", pg.DefaultLimitTokens)
	}

	// CONTROL 2: UNSET still boots uncapped. That half is deliberate — a
	// deployment that never asked for a cap must not be refused — and without
	// this line a "refuse unless set" implementation would pass everything above.
	clearLLMEnv(t)
	if _, err := buildTranslator(nil); err != nil {
		t.Fatalf("an unset LLM_BUDGET_TOKENS_PER_MONTH must still boot: %v", err)
	}
}

// ---------------------------------------------------------------------------
// One value, two implementations
// ---------------------------------------------------------------------------

// TestPromptHashIsOnePrimitiveAcrossPackages is the only place the agreement
// between llm.StaticPromptHash and trajectory.PromptHash can be ASSERTED rather
// than described, because this is the only package allowed to import both.
//
// ⛔ WHY THERE ARE TWO IMPLEMENTATIONS AT ALL: internal/llm's allowed-dependency
// set does not contain internal/trajectory (decision D-B), so llm cannot call
// trajectory.PromptHash, and trajectory must not import llm. The value they both
// compute is khata.prompt.hash — which is ALSO the key a cassette is looked up
// by. They shipped as SHA-256 (llm) and, in trajectory's prose, BLAKE3; both
// render as 64 lowercase hex characters, so a cassette seeded under one era and
// recorded under the other would have missed with ErrCassetteMiss naming the
// wrong fix, and no reader, diff or dashboard could have seen why.
//
// It reconstructs StaticPromptHash's documented framing rather than hashing a
// bare string, because that framing IS the input; if the framing changes, this
// test goes red and the person changing it decides deliberately whether the
// cassettes on disk are still valid.
func TestPromptHashIsOnePrimitiveAcrossPackages(t *testing.T) {
	const role, content = "system", "you are an accountant"
	req := &llm.Request{Static: []llm.Message{{Role: llm.RoleSystem, Content: content}}}

	got := llm.StaticPromptHash(req)
	want := trajectory.PromptHash(fmt.Sprintf("%s\x00%s\x00", role, content))
	if got != want {
		t.Fatalf("llm.StaticPromptHash = %s\ntrajectory.PromptHash = %s\n\n"+
			"These two must be the same primitive over the same bytes: this value is the "+
			"attribute khata.prompt.hash AND a cassette's replay key. If the framing in "+
			"StaticPromptHash changed, update this test and re-record the cassettes "+
			"(TRAJECTORY_RECORD=1 go test ./internal/trajectory/ -run TestSeedCassettes). If "+
			"the ALGORITHM changed on one side only, that is the defect this test exists for.",
			got, want)
	}

	// CONTROL: the digest is not some constant both sides happen to return. A
	// different prompt must give a different hash, and the value must be the
	// 64-hex shape both packages document.
	other := trajectory.PromptHash(fmt.Sprintf("%s\x00%s\x00", role, content+"!"))
	if other == want {
		t.Fatal("two different prompts hashed to the same value; the hash is not a function of its input")
	}
	if len(got) != 64 || strings.ToLower(got) != got || strings.Trim(got, "0123456789abcdef") != "" {
		t.Errorf("prompt hash %q is not 64 lowercase hex characters", got)
	}
}
