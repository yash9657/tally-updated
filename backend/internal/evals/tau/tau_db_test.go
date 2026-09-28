package tau_test

// tau_db_test.go runs the W1a suite END TO END against a real cored and a real
// Postgres: every trial is a freshly created company, seeded through the HTTP
// API, driven by an agent through the same API, and judged by a snapshot read
// back through it.
//
// THREE AGENTS, THREE PINNED COUNTS, and each exists to catch a different lie:
//
//   - reference passes 200/200. If it does not, a fixture's expected state
//     disagrees with the engine — either the fixture or the engine is wrong,
//     and the failing reason says which figure.
//   - null passes EXACTLY the guard tasks. More would mean a task whose
//     expected state equals its seed — a task that cannot fail by doing
//     nothing, i.e. one that measures nothing. Fewer would mean a guard task
//     whose seed the judge does not see as unchanged.
//   - wrong passes NOTHING. One pass would mean a judge blind to the slip the
//     WrongAgent made on that task (a date off by a day, a transposed name, a
//     figure off by a rupee, the tempting write on a guard task).
//
// And one agent that is right on some trials and wrong on others proves the
// pass^k arithmetic through the whole pipeline, not just in passk_test.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/evals/tau"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/internal/server"
	"github.com/khata/cored/migrations"
)

// Exact pins. Changing a fixture count is a reviewed edit to these lines.
const (
	suiteTasks      = 200
	suiteGuardTasks = 10
)

// dsns honours ADMIN_DATABASE_URL / DATABASE_URL (the Makefile's names), so a
// lane can run this against its own database without touching the shared one.
// The teardown harness reads TESTDB_ADMIN_DSN; it is pointed at the same
// database here, because a teardown against a DIFFERENT database than the one
// the companies were created in deletes nothing and reports nothing.
func dsns(t *testing.T) (admin, app string) {
	t.Helper()
	admin, app = os.Getenv("ADMIN_DATABASE_URL"), os.Getenv("DATABASE_URL")
	if admin == "" {
		admin = testdb.AdminDSNValue()
	}
	if app == "" {
		app = testdb.AppDSN
	}
	if v := os.Getenv(testdb.AdminDSNEnv); v == "" {
		t.Setenv(testdb.AdminDSNEnv, admin)
	} else if v != admin {
		t.Fatalf("%s=%q but ADMIN_DATABASE_URL=%q: teardown would target a different database "+
			"than the one the companies are created in", testdb.AdminDSNEnv, v, admin)
	}
	return admin, app
}

type rig struct {
	srv   *server.Server
	token string
}

// newRig boots cored against the database. Same shape as handler_itc_test.go:
// CORED_SKIP_DB skips, an unreachable database with it unset FAILS.
func newRig(t *testing.T) *rig {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped (CORED_SKIP_DB set)")
	}
	admin, app := dsns(t)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatalf("admin postgres unreachable and CORED_SKIP_DB is unset — this gate must not "+
			"pass without running: %v", err)
	}
	if err := migrations.Apply(ctx, conn); err != nil {
		_ = conn.Close(ctx)
		t.Fatalf("apply migrations: %v", err)
	}
	_ = conn.Close(ctx)

	t.Setenv("AUTH_DEV_TOKEN_ENABLED", "true")
	srv, err := server.New(ctx, app)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	t.Cleanup(srv.Close)

	body := strings.NewReader(`{"subject":"tau-` + uuid.NewString() + `","amr":["webauthn"]}`)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/token", body))
	var tok struct {
		Token string `json:"token"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &tok) != nil || tok.Token == "" {
		t.Fatalf("mint token = %d: %s", rec.Code, rec.Body.String())
	}
	return &rig{srv: srv, token: tok.Token}
}

// factory mints one company per trial and registers its teardown.
//
// testdb.Cleanup is called on the TEST's t from inside the factory, under a
// mutex, because trials run on worker goroutines and t.Cleanup's own locking
// is what makes that legal; t.Fatalf from here would not be (it must run on
// the test goroutine), which is why a failure is RETURNED to the harness.
//
// ⚠️ THE FIRST COMPANY IS CREATED ALONE. The first POST /v1/companies for a new
// subject also creates the user row (create-on-first-login), and eight workers
// racing that first request lose it with a 500 "resolve user" — measured on
// this suite's first run. Every later creation runs concurrently.
func (r *rig) factory(t *testing.T) tau.CompanyFactory {
	var mu, first sync.Mutex
	warmed := false
	return func(ctx context.Context, company map[string]any) (tau.Books, error) {
		first.Lock()
		if warmed {
			first.Unlock()
		} else {
			defer func() { warmed = true; first.Unlock() }()
		}
		id, err := uuid.NewV7()
		if err != nil {
			return nil, err
		}
		body := map[string]any{}
		for k, v := range company {
			body[k] = v
		}
		body["id"] = id.String()
		body["name"] = "Tau " + id.String()[:8]
		raw, _ := json.Marshal(body)
		books := tau.HandlerBooks{Handler: r.srv.Router(), Token: r.token}
		req := httptest.NewRequest(http.MethodPost, "/v1/companies", strings.NewReader(string(raw)))
		req.Header.Set("Authorization", "Bearer "+r.token)
		rec := httptest.NewRecorder()
		r.srv.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			return nil, fmt.Errorf("create company = %d: %s", rec.Code, rec.Body.String())
		}
		mu.Lock()
		testdb.Cleanup(t, id)
		mu.Unlock()
		books.Base = "/v1/companies/" + id.String()
		return books, nil
	}
}

func suiteDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		c := filepath.Join(dir, "corpora", "tau")
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			return c
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not locate corpora/tau")
	return ""
}

func loadSuite(t *testing.T) []tau.Task {
	t.Helper()
	tasks, err := tau.LoadDir(suiteDir(t))
	if err != nil {
		t.Fatalf("load suite: %v", err)
	}
	if len(tasks) != suiteTasks {
		t.Fatalf("suite has %d tasks, pinned at %d", len(tasks), suiteTasks)
	}
	return tasks
}

func run(t *testing.T, r *rig, tasks []tau.Task, a tau.Agent, k int) tau.Report {
	t.Helper()
	start := time.Now()
	rep, err := tau.Run(context.Background(), tau.Config{K: k, Workers: 8, NewCompany: r.factory(t)}, tasks, a)
	if err != nil {
		t.Fatalf("harness failure running %s: %v", a.Name(), err)
	}
	t.Logf("%s (%.1fs)", rep.Summary(), time.Since(start).Seconds())
	return rep
}

func failures(rep tau.Report, limit int) string {
	var b strings.Builder
	n := 0
	for _, tr := range rep.Tasks {
		for _, trial := range tr.Trials {
			if trial.Pass || n >= limit {
				continue
			}
			n++
			fmt.Fprintf(&b, "\n  %s trial %d: agentErr=%q", tr.TaskID, trial.Trial, trial.AgentErr)
			for i, reason := range trial.Reasons {
				if i >= 6 {
					fmt.Fprintf(&b, "\n    … %d more", len(trial.Reasons)-i)
					break
				}
				fmt.Fprintf(&b, "\n    - %s", reason)
			}
		}
	}
	return b.String()
}

func TestSuiteReferenceAgentSolvesEveryTask(t *testing.T) {
	r := newRig(t)
	tasks := loadSuite(t)
	rep := run(t, r, tasks, tau.NewReferenceAgent(tasks), 1)
	if rep.Passed() != suiteTasks {
		t.Fatalf("reference agent passed %d/%d — a fixture disagrees with the engine:%s",
			rep.Passed(), suiteTasks, failures(rep, 12))
	}
	for _, tr := range rep.Tasks {
		if tr.Trials[0].AgentErr != "" {
			t.Errorf("%s: reference agent errored yet passed: %s", tr.TaskID, tr.Trials[0].AgentErr)
		}
	}
	for _, dim := range []string{"family", "language"} {
		for _, b := range rep.By(dim) {
			t.Logf("  %s=%s tasks=%d passed=%d", dim, b.Group, b.Tasks, b.Passed)
		}
	}
}

func TestSuiteNullAgentPassesOnlyGuardTasks(t *testing.T) {
	r := newRig(t)
	tasks := loadSuite(t)
	rep := run(t, r, tasks, tau.NullAgent{}, 1)
	for _, tr := range rep.Tasks {
		isGuard := tr.Family == tau.FamilyGuard
		if passed := tr.Successes == 1; passed != isGuard {
			t.Errorf("%s (family %s): null agent pass=%v, want %v%s", tr.TaskID, tr.Family, passed, isGuard,
				failures(tau.Report{Tasks: []tau.TaskResult{tr}}, 1))
		}
	}
	if rep.Passed() != suiteGuardTasks {
		t.Fatalf("null agent passed %d, pinned at exactly the %d guard tasks", rep.Passed(), suiteGuardTasks)
	}
}

func TestSuiteWrongAgentFailsEveryTask(t *testing.T) {
	r := newRig(t)
	tasks := loadSuite(t)
	rep := run(t, r, tasks, tau.NewWrongAgent(tasks), 1)
	for _, tr := range rep.Tasks {
		if tr.Successes != 0 {
			t.Errorf("%s: the wrong agent PASSED — the judge cannot see the slip this task exists to catch "+
				"(answer %q)", tr.TaskID, tr.Trials[0].Answer)
		}
	}
}

// flakyAgent is right on even trials and wrong on odd ones.
type flakyAgent struct{ right, wrong tau.Agent }

func (flakyAgent) Name() string { return "flaky" }
func (f flakyAgent) Run(ctx context.Context, env tau.Env, p tau.Prompt) (tau.Result, error) {
	if env.Trial%2 == 0 {
		return f.right.Run(ctx, env, p)
	}
	return f.wrong.Run(ctx, env, p)
}

// TestPassHatFiveThroughTheHarness runs k=5 on one task per family and
// language cell. The flaky agent passes 3 of 5 trials on every non-guard task,
// so pass^1 = 3/5, pass^3 = C(3,3)/C(5,3) = 1/10, and pass^4 = pass^5 = 0 —
// on real trials, real companies and a real judge. The reference agent at k=5
// must be pass^5 = 1: its plans are deterministic, and a non-1 here is a
// cross-trial leak (trials sharing a company) or a nondeterministic engine.
func TestPassHatFiveThroughTheHarness(t *testing.T) {
	r := newRig(t)
	all := loadSuite(t)
	seen := map[string]bool{}
	var tasks []tau.Task
	for _, tk := range all {
		cell := tk.Family + "/" + tk.Language
		if tk.Family == tau.FamilyGuard || seen[cell] {
			continue
		}
		seen[cell] = true
		tasks = append(tasks, tk)
	}
	if len(tasks) != 18 { // 6 non-guard families x 3 languages
		t.Fatalf("picked %d family/language cells, want 18", len(tasks))
	}
	ref := run(t, r, tasks, tau.NewReferenceAgent(all), 5)
	if got := ref.PassHatK(5); got != 1 {
		t.Fatalf("reference pass^5 = %v, want 1:%s", got, failures(ref, 6))
	}
	fl := run(t, r, tasks, flakyAgent{tau.NewReferenceAgent(all), tau.NewWrongAgent(all)}, 5)
	for k, want := range map[int]float64{1: 0.6, 2: 0.3, 3: 0.1, 4: 0, 5: 0} {
		if got := fl.PassHatK(k); fmt.Sprintf("%.6f", got) != fmt.Sprintf("%.6f", want) {
			t.Errorf("flaky pass^%d = %.6f, want %.6f", k, got, want)
		}
	}
}
