package tau

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"sync"
)

// CompanyFactory creates one fresh, empty company and returns its Books. The
// harness calls it once per TRIAL, never once per task: trials that shared a
// company would see each other's writes, and pass^k over non-independent
// trials is not pass^k.
type CompanyFactory func(ctx context.Context, company map[string]any) (Books, error)

// Config parameterises a run.
type Config struct {
	// K is the number of trials per task (pass^K is the headline).
	K int
	// Workers bounds concurrent trials. Trials are independent companies, so
	// they parallelise; the bound is for the database, not for correctness.
	Workers    int
	NewCompany CompanyFactory
}

// TrialResult is one trial's outcome.
type TrialResult struct {
	Trial    int
	Pass     bool
	Reasons  []string
	AgentErr string
	Answer   string
}

// TaskResult aggregates a task's trials.
type TaskResult struct {
	TaskID    string
	Family    string
	Language  string
	Trials    []TrialResult
	Successes int
}

// Report is a run's outcome.
type Report struct {
	Agent string
	K     int
	Tasks []TaskResult
}

// ErrHarness wraps a failure that is the FIXTURE's or the environment's, not
// the agent's: a company that could not be created, a seed step that was
// refused, a snapshot that could not be read. Such a failure aborts the run
// rather than being scored, because scoring it would charge an agent for a
// broken task — and, worse, would let a broken seed pass the NullAgent.
var ErrHarness = errors.New("tau: harness failure")

// Run executes every task K times against agent.
func Run(ctx context.Context, cfg Config, tasks []Task, agent Agent) (Report, error) {
	if cfg.K < 1 {
		return Report{}, fmt.Errorf("tau: K must be >= 1, got %d", cfg.K)
	}
	if cfg.Workers < 1 {
		cfg.Workers = 1
	}
	if len(tasks) == 0 {
		return Report{}, fmt.Errorf("tau: no tasks")
	}
	rep := Report{Agent: agent.Name(), K: cfg.K, Tasks: make([]TaskResult, len(tasks))}
	for i, t := range tasks {
		rep.Tasks[i] = TaskResult{TaskID: t.ID, Family: t.Family, Language: t.Language,
			Trials: make([]TrialResult, cfg.K)}
	}

	type job struct{ ti, trial int }
	jobs := make(chan job)
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	for w := 0; w < cfg.Workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				tr, err := runTrial(ctx, cfg, tasks[j.ti], agent, j.trial)
				mu.Lock()
				if err != nil && firstErr == nil {
					firstErr = fmt.Errorf("%w: task %s trial %d: %v", ErrHarness, tasks[j.ti].ID, j.trial, err)
				}
				rep.Tasks[j.ti].Trials[j.trial] = tr
				mu.Unlock()
			}
		}()
	}
	for ti := range tasks {
		for k := 0; k < cfg.K; k++ {
			jobs <- job{ti, k}
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return rep, firstErr
	}
	for i := range rep.Tasks {
		for _, tr := range rep.Tasks[i].Trials {
			if tr.Pass {
				rep.Tasks[i].Successes++
			}
		}
	}
	return rep, nil
}

func runTrial(ctx context.Context, cfg Config, t Task, agent Agent, trial int) (TrialResult, error) {
	tr := TrialResult{Trial: trial}
	books, err := cfg.NewCompany(ctx, t.Seed.Company)
	if err != nil {
		return tr, fmt.Errorf("create company: %w", err)
	}
	defaults, err := Snapshot(ctx, books)
	if err != nil {
		return tr, fmt.Errorf("snapshot defaults: %w", err)
	}
	if _, err := NewExecutor(books).Run(ctx, t.Seed.Steps); err != nil {
		return tr, fmt.Errorf("seed: %w", err)
	}
	res, aerr := agent.Run(ctx, Env{Books: books, Trial: trial}, t.Prompt())
	if aerr != nil {
		tr.AgentErr = aerr.Error()
	}
	tr.Answer = res.Answer
	after, err := Snapshot(ctx, books)
	if err != nil {
		return tr, fmt.Errorf("snapshot after: %w", err)
	}
	v := Judge(t.Expected, defaults, after, res.Answer)
	tr.Pass, tr.Reasons = v.Pass, v.Reasons
	if aerr != nil {
		tr.Pass = false
		tr.Reasons = append(tr.Reasons, "agent did not complete: "+aerr.Error())
	}
	return tr, nil
}

// PassHatK is the unbiased pass^k estimator (tau-bench): for a task with n
// trials of which c passed, C(c,k)/C(n,k) — the probability that k trials
// drawn without replacement ALL pass — averaged over tasks. k must not exceed
// the trials run; pass^1 is the plain success rate.
func (r Report) PassHatK(k int) float64 {
	if k < 1 || k > r.K || len(r.Tasks) == 0 {
		return 0
	}
	sum := new(big.Rat)
	for _, t := range r.Tasks {
		sum.Add(sum, passHatKTask(len(t.Trials), t.Successes, k))
	}
	f, _ := new(big.Rat).Quo(sum, big.NewRat(int64(len(r.Tasks)), 1)).Float64()
	return f
}

func passHatKTask(n, c, k int) *big.Rat {
	if c < k {
		return new(big.Rat)
	}
	num := new(big.Int).Binomial(int64(c), int64(k))
	den := new(big.Int).Binomial(int64(n), int64(k))
	return new(big.Rat).SetFrac(num, den)
}

// Passed counts tasks all of whose trials passed.
func (r Report) Passed() int {
	n := 0
	for _, t := range r.Tasks {
		if t.Successes == len(t.Trials) {
			n++
		}
	}
	return n
}

// Breakdown is pass^1 and all-trials-passed counts per group.
type Breakdown struct {
	Group  string
	Tasks  int
	Passed int
}

// By groups tasks by family or language ("family" | "language").
func (r Report) By(dim string) []Breakdown {
	m := map[string]*Breakdown{}
	for _, t := range r.Tasks {
		g := t.Family
		if dim == "language" {
			g = t.Language
		}
		b, ok := m[g]
		if !ok {
			b = &Breakdown{Group: g}
			m[g] = b
		}
		b.Tasks++
		if t.Successes == len(t.Trials) {
			b.Passed++
		}
	}
	out := make([]Breakdown, 0, len(m))
	for _, b := range m {
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Group < out[j].Group })
	return out
}

// Summary renders the report for a log line.
func (r Report) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "agent=%s tasks=%d k=%d all-trials-passed=%d", r.Agent, len(r.Tasks), r.K, r.Passed())
	for k := 1; k <= r.K; k++ {
		fmt.Fprintf(&b, " pass^%d=%.4f", k, r.PassHatK(k))
	}
	return b.String()
}
