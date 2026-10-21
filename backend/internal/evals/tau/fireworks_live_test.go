//go:build livefireworks

package tau_test

// Explicit opt-in: this test spends money and runs only against a newly created
// synthetic database. No expected answers or reference plans reach the model.
import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/khata/cored/internal/actions"
	"github.com/khata/cored/internal/agent"
	"github.com/khata/cored/internal/evals/tau"
	"github.com/shopspring/decimal"
)

type liveAgent struct {
	rig     *rig
	spend   *spendTransport
	approve map[string]bool
	logf    func(string, ...any)
}

func (a liveAgent) Name() string { return "fireworks/" + os.Getenv("FIREWORKS_MODEL") }
func (a liveAgent) Run(ctx context.Context, env tau.Env, p tau.Prompt) (tau.Result, error) {
	a.logf("live evaluation task=%s language=%s", p.TaskID, p.Language)
	if reason := a.spend.providerStopReason(); reason != "" {
		return tau.Result{}, fmt.Errorf("task not attempted: %s", reason)
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	// The fixture operator delegates the supported writes, exactly as the UI's
	// delegation controls do. Approvals remain required; no reference plan or
	// expected answer is used to select actions.
	keys := map[string]bool{}
	for _, spec := range actions.Specs {
		if agent.Previewable(spec.ID) {
			keys[spec.ACLKey] = true
		}
	}
	var delegate []string
	for key := range keys {
		delegate = append(delegate, key)
	}
	sort.Strings(delegate)
	request := map[string]any{"delegate": delegate, "instruction": "Today is " + p.Today + ".\n" + p.Utterance, "channel": "api"}
	raw, _ := json.Marshal(request)
	var view struct {
		Run struct {
			ID      string `json:"id"`
			Status  string `json:"status"`
			Outcome struct {
				Final   string `json:"final"`
				Details string `json:"details"`
				Reason  string `json:"reason"`
			} `json:"outcome"`
			LastError string `json:"last_error"`
		} `json:"run"`
	}
	call := func(method, path string, body []byte, into any) error {
		code, resp, err := env.Books.Do(ctx, method, path, body)
		if err != nil {
			return err
		}
		if code < 200 || code >= 300 {
			return fmt.Errorf("%s %s returned %d", method, path, code)
		}
		return json.Unmarshal(resp, into)
	}
	if err := call("POST", "/agent/runs", raw, &view); err != nil {
		return tau.Result{}, err
	}
	id := view.Run.ID
	if id == "" {
		return tau.Result{}, fmt.Errorf("agent returned no run id")
	}
	defer func() { env.Books.Do(context.Background(), "POST", "/agent/runs/"+id+"/cancel", nil) }()
	for turn := 0; turn < 30; turn++ {
		if reason := a.spend.providerStopReason(); reason != "" {
			return tau.Result{}, fmt.Errorf("task incomplete: %s", reason)
		}
		if a.spend.exhausted {
			return tau.Result{}, fmt.Errorf("evaluation spending ceiling reached; task incomplete")
		}
		if err := ctx.Err(); err != nil {
			return tau.Result{}, err
		}
		if _, err := a.rig.srv.AgentPollOnce(ctx); err != nil {
			return tau.Result{}, err
		}
		if err := call("GET", "/agent/runs/"+id, nil, &view); err != nil {
			return tau.Result{}, err
		}
		result := tau.Result{Answer: view.Run.Outcome.Final, Details: view.Run.Outcome.Details}
		if view.Run.Status == "completed" {
			if strings.TrimSpace(result.Answer) == "" {
				return result, fmt.Errorf("run completed without a user-visible answer")
			}
			return result, nil
		}
		if view.Run.Status == "failed" || view.Run.Status == "expired" || view.Run.Status == "cancelled" {
			return result, fmt.Errorf("run %s: %s %s", view.Run.Status, view.Run.LastError, view.Run.Outcome.Reason)
		}
		var pending struct {
			Approvals []struct {
				ID string `json:"id"`
			} `json:"approvals"`
		}
		if err := call("GET", "/agent/approvals?state=pending&run_id="+id, nil, &pending); err != nil {
			return result, err
		}
		if len(pending.Approvals) > 0 {
			if !a.approve[p.TaskID] {
				return result, fmt.Errorf("approval withheld for guard task")
			}
			for _, approval := range pending.Approvals {
				var response any
				if err := call("POST", "/agent/approvals/"+approval.ID+"/decision", []byte(`{"decision":"approve"}`), &response); err != nil {
					return result, err
				}
			}
		}
	}
	return tau.Result{}, fmt.Errorf("run exceeded 30 evaluation polls")
}

func TestFireworksLiveEvaluation(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Fatal("unset CORED_SKIP_DB; live evaluation cannot skip its database")
	}
	for _, key := range []string{"FIREWORKS_API_KEY", "FIREWORKS_MODEL", "EVAL_MAX_USD", "EVAL_TOKEN_PRICE_PER_MILLION_USD", "EVAL_BUDGET_LEDGER", "ADMIN_DATABASE_URL"} {
		if os.Getenv(key) == "" {
			t.Fatalf("%s is required; no provider calls made", key)
		}
	}
	if os.Getenv("EVAL_SYNTHETIC_APPROVALS") != "1" {
		t.Fatal("set EVAL_SYNTHETIC_APPROVALS=1 to permit fixture-only approvals")
	}
	cap, err := decimal.NewFromString(os.Getenv("EVAL_MAX_USD"))
	if err != nil || !cap.IsPositive() {
		t.Fatal("EVAL_MAX_USD must be positive")
	}
	rate, err := decimal.NewFromString(os.Getenv("EVAL_TOKEN_PRICE_PER_MILLION_USD"))
	if err != nil || !rate.IsPositive() {
		t.Fatal("token price must be positive")
	}
	ledger, err := openSpendLedger(os.Getenv("EVAL_BUDGET_LEDGER"), cap, rate)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ledger.close)
	ctx := context.Background()
	adminURL, err := url.Parse(os.Getenv("ADMIN_DATABASE_URL"))
	if err != nil || adminURL.Host == "" {
		t.Fatal("ADMIN_DATABASE_URL must be a PostgreSQL URL")
	}
	admin, err := pgx.Connect(ctx, adminURL.String())
	if err != nil {
		t.Fatal("cannot connect to evaluation database administrator")
	}
	t.Cleanup(func() { admin.Close(ctx) })
	name := "tally_fireworks_eval_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	})
	adminURL.Path = "/" + name
	t.Setenv("ADMIN_DATABASE_URL", adminURL.String())
	t.Setenv("TESTDB_ADMIN_DSN", adminURL.String())
	appURL := *adminURL
	appURL.User = url.UserPassword("tally_app", "tally_app")
	t.Setenv("DATABASE_URL", appURL.String())
	t.Setenv("TESTDB_APP_DSN", appURL.String())
	t.Setenv("LLM_PROVIDER", "fireworks")
	t.Setenv("LLM_MODEL", os.Getenv("FIREWORKS_MODEL"))
	t.Setenv("LLM_API_KEY", os.Getenv("FIREWORKS_API_KEY"))
	t.Setenv("LLM_BASE_URL", "")
	t.Setenv("LLM_TIMEOUT", "90s")
	transport := &spendTransport{next: http.DefaultTransport, cap: cap, rate: rate, ledger: ledger}
	old := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = old })
	r := newRig(t)
	if os.Getenv("EVAL_SUITE") == "insights" || os.Getenv("EVAL_SUITE") == "expanded" {
		runBusinessLive(t, r, transport, ledger)
		return
	}
	all := loadSuite(t)
	var tasks []tau.Task
	approve := map[string]bool{}
	seen := map[string]bool{}
	// A balanced smoke suite: one task per family/language, plus every guard.
	for _, task := range all {
		cell := task.Family + "/" + task.Language
		if !seen[cell] || task.Family == tau.FamilyGuard {
			tasks = append(tasks, task)
			seen[cell] = true
			approve[task.ID] = task.Family != tau.FamilyGuard
		}
	}
	// Optional explicit smoke selection; ids are never converted into plans.
	if ids := os.Getenv("EVAL_TASK_IDS"); ids != "" {
		wanted := map[string]bool{}
		for _, id := range strings.Split(ids, ",") {
			wanted[id] = true
		}
		tasks = nil
		for _, task := range all {
			if wanted[task.ID] {
				tasks = append(tasks, task)
				approve[task.ID] = task.Family != tau.FamilyGuard
				delete(wanted, task.ID)
			}
		}
		if len(wanted) > 0 {
			t.Fatalf("unknown task ids: %v", wanted)
		}
	}
	start := time.Now()
	report, runErr := tau.Run(ctx, tau.Config{K: 1, Workers: 1, NewCompany: r.factory(t)}, tasks, liveAgent{rig: r, spend: transport, approve: approve, logf: t.Logf})
	result := map[string]any{"synthetic": true, "agent_prompt_version": agent.PromptVersion, "source_revision": os.Getenv("EVAL_SOURCE_REVISION"), "source_sha256": os.Getenv("EVAL_SOURCE_SHA256"), "model": os.Getenv("FIREWORKS_MODEL"), "started_at": start.UTC(), "elapsed_seconds": time.Since(start).Seconds(), "reserved_usd": transport.reserved.String(), "max_usd": cap.String(), "token_price_per_million_usd": rate.String(), "calls": transport.records, "report": report}
	result["shared_budget_charged_usd"] = ledger.Charged.String()
	languageFailures := 0
	var languageChecks []map[string]any
	for _, task := range report.Tasks {
		for _, trial := range task.Trials {
			reasons := businessLanguage(task.Language, trial.Answer+"\n"+trial.Details)
			if len(reasons) > 0 {
				languageFailures++
			}
			languageChecks = append(languageChecks, map[string]any{"id": task.TaskID, "language": task.Language, "pass": len(reasons) == 0, "reasons": reasons})
		}
	}
	result["language_checks"] = languageChecks
	result["language_check_limit"] = "Necessary script/connective heuristic; not a fluency or translation oracle. State verdicts are retained separately."
	if runErr != nil {
		result["harness_error"] = runErr.Error()
	}
	out := os.Getenv("EVAL_REPORT")
	if out == "" {
		out = filepath.Join(t.TempDir(), "fireworks-evaluation.json")
	}
	raw, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(out, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("evaluation report: %s; provider calls=%d; conservative reservation=$%s", out, transport.calls, transport.reserved)
	if runErr != nil {
		t.Fatal(runErr)
	}
	passed := 0
	for _, task := range report.Tasks {
		passed += task.Successes
	}
	t.Logf("state-based pass@1: %d/%d", passed, len(tasks))
	if transport.calls == 0 || passed != len(tasks) || languageFailures > 0 {
		t.Fatalf("evaluation: state %d/%d; language flags %d", passed, len(tasks), languageFailures)
	}
}
