package server_test

// agent_parity_test.go — THE W2 PARITY GATE (PLAN-AGENTIC §3 W2): the same
// recorded trace, replayed once through the UI-equivalent API calls and once
// through the agent plane, must leave the books in IDENTICAL canonical state
// (W1a's tau.Snapshot, the judge the tau suite uses) — and with the same
// voucher numbers, which the canonical form deliberately ignores and this gate
// deliberately does not.
//
// ⛔ WHAT A "TRACE" IS HERE, SAID PLAINLY. The traces are the WRITE steps of
// the tau suite's hand-written reference plans (corpora/tau, W1a) — the exact
// API calls the product's own screens make for those tasks (POST
// /vouchers/<class>, PUT /bank-reconciliation/..., POST /ledgers, POST
// /stock-items), with names resolved against the live company. They are NOT
// keystroke recordings: no browser keyed them, so this gate proves the agent
// plane preserves what the API does, not that the F-key screens produce these
// bodies. The keystroke-to-body half (capturing the bodies the real
// VoucherScreen sends from corpora/keystroke's events) is OWED — D121 "Wave 2
// breadth" §B.9.
//
// THE AGENT PATH, per write step: a run whose (scripted — there are no provider
// keys) model previews exactly that request as the matching ActionSpec, asks
// to commit it, parks at the default L1, is approved by the owner in the
// approval inbox, and commits the RECORDED preview. A master the agent creates
// is born blocked, so the harness then does what a person must do before the
// next step may post to it: unblock it. Reads (the steps that capture a bank
// leg's id, say) go straight to the API on both paths.
//
// What this catches: a preview that leaks a write or a numbering gap, a commit
// that is not the previewed body, a dispatcher that re-routes a request, an
// approval that commits something other than what it showed, an attribution or
// block that changes a figure.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/actions"
	"github.com/khata/cored/internal/agent"
	"github.com/khata/cored/internal/evals/tau"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/internal/server"
)

// parityFloor is the number of traces the gate must replay. PLAN-AGENTIC W2
// asks for 50; every write task in the tau suite is used (measured: 143).
const parityFloor = 50

func tauDir(t *testing.T) string {
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

// traceTasks is every tau task whose reference plan writes.
func traceTasks(t *testing.T) []tau.Task {
	t.Helper()
	all, err := tau.LoadDir(tauDir(t))
	if err != nil {
		t.Fatalf("load tau: %v", err)
	}
	var out []tau.Task
	for _, tk := range all {
		for _, s := range tk.ReferencePlan {
			if s.Method != "" && s.Method != http.MethodGet {
				out = append(out, tk)
				break
			}
		}
	}
	return out
}

// specFor matches a concrete request onto its ActionSpec: the registry route
// with every {param} matching one segment, the most literal match winning.
func specFor(method, path string) (actions.ActionSpec, map[string]string, bool) {
	path = strings.SplitN(path, "?", 2)[0]
	segs := strings.Split(strings.Trim(path, "/"), "/")
	var best actions.ActionSpec
	var bestParams map[string]string
	bestLit := -1
	for _, s := range actions.Specs {
		if s.Method != method {
			continue
		}
		route := strings.TrimPrefix(s.Route, "/v1/companies/{companyID}")
		rs := strings.Split(strings.Trim(route, "/"), "/")
		if len(rs) != len(segs) {
			continue
		}
		params, lit, ok := map[string]string{}, 0, true
		for i, r := range rs {
			if strings.HasPrefix(r, "{") && strings.HasSuffix(r, "}") {
				params[r[1:len(r)-1]] = segs[i]
				continue
			}
			if r != segs[i] {
				ok = false
				break
			}
			lit++
		}
		if ok && lit > bestLit {
			best, bestParams, bestLit = s, params, lit
		}
	}
	return best, bestParams, bestLit >= 0
}

// parityRig is one server, one owner token, and a company factory.
type parityRig struct {
	t     *testing.T
	srv   *server.Server
	token string
}

func (r *parityRig) company(seed map[string]any) (tau.HandlerBooks, agentBooks) {
	r.t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		r.t.Fatal(err)
	}
	body := map[string]any{}
	for k, v := range seed {
		body[k] = v
	}
	body["id"], body["name"] = id.String(), "Parity "+id.String()[:8]
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/companies", strings.NewReader(string(raw)))
	req.Header.Set("Authorization", "Bearer "+r.token)
	rec := httptest.NewRecorder()
	r.srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		r.t.Fatalf("create company = %d: %s", rec.Code, rec.Body.String())
	}
	testdb.Cleanup(r.t, id)
	base := "/v1/companies/" + id.String()
	return tau.HandlerBooks{Handler: r.srv.Router(), Token: r.token, Base: base},
		agentBooks{companyID: id.String(), base: base, ledgers: map[string]string{}}
}

// agentPath is a tau.Books whose WRITES go through the agent plane and whose
// reads go straight to the API.
type agentPath struct {
	r      *parityRig
	api    tau.HandlerBooks
	b      agentBooks
	taskID string
	step   int
	// runs counts agent runs; blocked counts ledgers the harness unblocked.
	runs, unblocked int
}

func (p *agentPath) Do(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	if method == http.MethodGet {
		return p.api.Do(ctx, method, path, body)
	}
	t := p.r.t
	spec, params, ok := specFor(method, path)
	if !ok {
		return 0, nil, fmt.Errorf("parity: no ActionSpec for %s %s", method, path)
	}
	if !agent.Previewable(spec.ID) {
		return 0, nil, fmt.Errorf("parity: %s is not previewable by the agent", spec.ID)
	}
	p.step++
	p.runs++
	var preview map[string]any
	s := func(t *testing.T, turn int, res []json.RawMessage) agent.ModelDecision {
		switch turn {
		case 0:
			args := map[string]any{"action_id": spec.ID, "body": json.RawMessage(body)}
			if len(params) > 0 {
				args["path_params"] = params
			}
			return decision(agent.ToolPreview, args)
		case 1:
			preview = toolResult(t, res, 0)
			return decision(agent.ToolCommit, map[string]string{"preview": "tool:0"})
		}
		return finish("replayed")
	}
	model := scriptedModel(t, s)
	p.r.srv.ConfigureAgent(server.AgentOptions{LLM: model.client(t)})
	runID := startRun(t, p.r.srv, p.b, map[string]any{
		"instruction": fmt.Sprintf("Replay trace %s step %d: %s %s", p.taskID, p.step, method, path),
		"delegate":    []string{spec.ACLKey},
	})
	pollUntilQuiet(t, p.r.srv)
	pend := pendingApprovals(t, p.r.srv, p.b, runID)
	if len(pend) != 1 {
		return 0, nil, fmt.Errorf("parity: %s step %d parked %d approvals (preview %v)", p.taskID, p.step, len(pend), preview)
	}
	if code, resp := decide(t, p.r.srv, p.b, pend[0]["id"].(string), map[string]any{"decision": "approve"}); code != http.StatusOK {
		return 0, nil, fmt.Errorf("parity: approve = %d %v", code, resp)
	}
	pollUntilQuiet(t, p.r.srv)
	run := getRun(t, p.r.srv, p.b, runID)
	if run["status"] != "completed" {
		return 0, nil, fmt.Errorf("parity: run %s", run["status"])
	}
	if !strings.Contains(intentKinds(t, p.b.companyID, runID), ",commit,") {
		return http.StatusUnprocessableEntity, []byte(`{"error":"the agent's commit was refused"}`), nil
	}
	// ⛔ BORN BLOCKED: the person reviews the agent's new master and releases it
	// before the trace's next step may post to it.
	if spec.ID == "post.ledgers" {
		var name struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(body, &name)
		if row := ledgerRow(t, p.r.srv, p.b, name.Name); row != nil && row["blocked"] == true {
			rec, _ := do(t, p.r.srv, http.MethodPost, p.b.base+"/ledgers/"+row["id"].(string)+"/unblock", nil)
			if rec.Code != http.StatusOK {
				return 0, nil, fmt.Errorf("parity: unblock = %d", rec.Code)
			}
			p.unblocked++
		} else {
			return 0, nil, fmt.Errorf("parity: the agent's ledger %q was not born blocked", name.Name)
		}
	}
	return http.StatusCreated, []byte(`{}`), nil
}

// voucherNumbers is the book's (class, number) list in day-book order: the
// numbering the canonical form ignores, and a leaked preview would shift.
func voucherNumbers(t *testing.T, b tau.Books) []string {
	t.Helper()
	code, resp, err := b.Do(context.Background(), http.MethodGet, "/day-book?from=2000-04-01&to=2099-03-31", nil)
	if err != nil || code != http.StatusOK {
		t.Fatalf("day book = %d %v", code, err)
	}
	var dbk struct {
		Entries []struct {
			Class     string `json:"class"`
			VoucherNo string `json:"voucher_no"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(resp, &dbk); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range dbk.Entries {
		out = append(out, e.Class+"#"+e.VoucherNo)
	}
	sort.Strings(out)
	return out
}

func TestAgentParityTracesMatchTheUIPath(t *testing.T) {
	srv := newAgentServer(t)
	r := &parityRig{t: t, srv: srv, token: ownerToken(t, srv)}
	tasks := traceTasks(t)
	if len(tasks) < parityFloor {
		t.Fatalf("only %d write traces; PLAN-AGENTIC W2 asks for %d", len(tasks), parityFloor)
	}
	start := time.Now()
	byFamily := map[string]int{}
	runs, unblocked, steps := 0, 0, 0
	for _, tk := range tasks {
		tk := tk
		t.Run(tk.ID, func(t *testing.T) {
			pr := &parityRig{t: t, srv: srv, token: r.token}
			// ---- the UI path
			ui, _ := pr.company(tk.Seed.Company)
			if _, err := tau.NewExecutor(ui).Run(context.Background(), tk.Seed.Steps); err != nil {
				t.Fatalf("seed (UI): %v", err)
			}
			seeded, err := tau.Snapshot(context.Background(), ui)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tau.NewExecutor(ui).Run(context.Background(), tk.ReferencePlan); err != nil {
				t.Fatalf("trace (UI): %v", err)
			}
			want, err := tau.Snapshot(context.Background(), ui)
			if err != nil {
				t.Fatal(err)
			}
			// ---- the agent path
			api, ab := pr.company(tk.Seed.Company)
			if _, err := tau.NewExecutor(api).Run(context.Background(), tk.Seed.Steps); err != nil {
				t.Fatalf("seed (agent): %v", err)
			}
			ap := &agentPath{r: pr, api: api, b: ab, taskID: tk.ID}
			if _, err := tau.NewExecutor(ap).Run(context.Background(), tk.ReferencePlan); err != nil {
				t.Fatalf("trace (agent): %v", err)
			}
			got, err := tau.Snapshot(context.Background(), api)
			if err != nil {
				t.Fatal(err)
			}
			wj, _ := json.Marshal(want)
			gj, _ := json.Marshal(got)
			sj, _ := json.Marshal(seeded)
			if string(wj) == string(sj) {
				t.Fatalf("VACUOUS: the UI path's trace changed nothing canonical")
			}
			if string(gj) != string(wj) {
				t.Fatalf("PARITY BROKEN: the agent path's canonical books differ from the UI path's\nUI:    %s\nagent: %s", wj, gj)
			}
			if w, g := voucherNumbers(t, ui), voucherNumbers(t, api); strings.Join(w, ",") != strings.Join(g, ",") {
				t.Fatalf("PARITY BROKEN: voucher numbering differs (a preview leaked a number?)\nUI:    %v\nagent: %v", w, g)
			}
			byFamily[tk.Family]++
			runs += ap.runs
			unblocked += ap.unblocked
			steps += ap.step
		})
	}
	t.Logf("parity: %d traces (%v), %d write steps through %d agent runs (each previewed, approved in the inbox, committed), "+
		"%d agent-created ledgers unblocked by a person; canonical books and voucher numbers identical on both paths (%s)",
		len(tasks), byFamily, steps, runs, unblocked, time.Since(start).Round(time.Second))
}
