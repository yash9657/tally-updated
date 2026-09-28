package tau

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// Agent is anything that can be put under test: a scripted plan (below), W1b's
// single-model bake-off loop, W2's plan-and-execute agent. It sees a Prompt and
// the Books, and returns its final reply.
//
// An error is a RESULT, not an abort: the harness still snapshots and judges
// the books, because an agent that crashed halfway through a two-voucher task
// has left one voucher behind, and the state is what the user is left with.
type Agent interface {
	Name() string
	Run(ctx context.Context, env Env, p Prompt) (Result, error)
}

// Env is the agent's world for one trial.
type Env struct {
	Books Books
	// Trial is the 0-based trial index within the task. Honest agents ignore
	// it; it exists so a test can build an agent that fails on chosen trials
	// and prove the pass^k arithmetic end to end.
	Trial int
}

// Result is the agent's final reply.
type Result struct {
	Answer string
}

// ReferenceAgent executes each task's hand-written plan. Its plans are handed
// over at construction — from the fixtures, which is the single place a plan
// and a prompt are allowed to meet (see doc.go).
type ReferenceAgent struct{ plans map[string][]Step }

// NewReferenceAgent indexes the reference plans of tasks.
func NewReferenceAgent(tasks []Task) *ReferenceAgent {
	m := make(map[string][]Step, len(tasks))
	for _, t := range tasks {
		m[t.ID] = t.ReferencePlan
	}
	return &ReferenceAgent{plans: m}
}

// Name implements Agent.
func (*ReferenceAgent) Name() string { return "reference" }

// Run implements Agent.
func (a *ReferenceAgent) Run(ctx context.Context, env Env, p Prompt) (Result, error) {
	plan, ok := a.plans[p.TaskID]
	if !ok {
		return Result{}, fmt.Errorf("reference: no plan for %s", p.TaskID)
	}
	ans, err := NewExecutor(env.Books).Run(ctx, plan)
	return Result{Answer: ans}, err
}

// NullAgent does nothing and says nothing. It must pass exactly the guard tasks.
type NullAgent struct{}

// Name implements Agent.
func (NullAgent) Name() string { return "null" }

// Run implements Agent.
func (NullAgent) Run(context.Context, Env, Prompt) (Result, error) { return Result{}, nil }

// WrongAgent is the plausible-but-wrong agent: it follows the reference plan
// with ONE realistic slip per write — the date off by a day, a master's name
// mistyped — says every figure one rupee off, and on a guard task makes the
// tempting write. Every task must fail it; a task that does not is a task
// whose judge cannot see the mistake it was written to catch.
//
// ⚠️ A WRITE NO RULE CAN PERTURB IS DROPPED, NOT EXECUTED AS-IS. Executing it
// unchanged would let the WrongAgent pass a task for the reason that it was,
// on that task, simply right — which proves nothing about the judge.
type WrongAgent struct{ plans, naive map[string][]Step }

// NewWrongAgent indexes the reference and naive plans of tasks.
func NewWrongAgent(tasks []Task) *WrongAgent {
	w := &WrongAgent{plans: map[string][]Step{}, naive: map[string][]Step{}}
	for _, t := range tasks {
		w.plans[t.ID] = t.ReferencePlan
		if len(t.NaivePlan) > 0 {
			w.naive[t.ID] = t.NaivePlan
		}
	}
	return w
}

// Name implements Agent.
func (*WrongAgent) Name() string { return "wrong" }

// Run implements Agent.
func (w *WrongAgent) Run(ctx context.Context, env Env, p Prompt) (Result, error) {
	if naive, ok := w.naive[p.TaskID]; ok {
		ans, err := NewExecutor(env.Books).Run(ctx, naive)
		return Result{Answer: ans}, err
	}
	plan, ok := w.plans[p.TaskID]
	if !ok {
		return Result{}, fmt.Errorf("wrong: no plan for %s", p.TaskID)
	}
	var steps []Step
	for _, s := range plan {
		switch {
		case s.IsAnswer(), s.Method == http.MethodGet:
			// Reads run as written; the rendered answer is perturbed below.
			steps = append(steps, s)
			steps = append(steps, s)
		default:
			if m, ok := perturb(s); ok {
				steps = append(steps, m)
			}
		}
	}
	ans, err := NewExecutor(env.Books).Run(ctx, steps)
	return Result{Answer: offByOne(ans)}, err
}

// perturb applies the first applicable slip to a write step.
func perturb(s Step) (Step, bool) {
	if len(s.Body) == 0 {
		return s, false
	}
	var body map[string]any
	dec := json.NewDecoder(bytes.NewReader(s.Body))
	dec.UseNumber()
	if err := dec.Decode(&body); err != nil {
		return s, false
	}
	changed := false
	for _, k := range []string{"voucher_date", "bank_date"} {
		if d, ok := body[k].(string); ok {
			if t, err := time.Parse("2006-01-02", d); err == nil {
				body[k] = t.AddDate(0, 0, 1).Format("2006-01-02")
				changed = true
			}
		}
	}
	if !changed {
		if n, ok := body["name"].(string); ok && !strings.Contains(n, "{") {
			// A transposition, the commonest typing slip: "Acme" -> "Amce".
			body["name"] = transpose(n)
			changed = true
		}
	}
	if !changed {
		return s, false
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return s, false
	}
	s.Body = raw
	return s, true
}

func transpose(s string) string {
	r := []rune(s)
	for i := 1; i+1 < len(r); i++ {
		if r[i] != r[i+1] && r[i] != ' ' && r[i+1] != ' ' {
			r[i], r[i+1] = r[i+1], r[i]
			return string(r)
		}
	}
	return s + " X"
}

var figureRe = regexp.MustCompile(`\d[\d,]*(?:\.\d+)?`)

// offByOne adds one rupee to every figure in an answer.
func offByOne(a string) string {
	return figureRe.ReplaceAllStringFunc(a, func(m string) string {
		d, err := decimal.NewFromString(strings.ReplaceAll(m, ",", ""))
		if err != nil {
			return m
		}
		return d.Add(decimal.NewFromInt(1)).StringFixed(2)
	})
}
