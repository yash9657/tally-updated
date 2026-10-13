package tau

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Families are the five task shapes W1 names, plus the guard family.
//
// ⚠️ "guard" IS NOT IN PLAN-AGENTIC's LIST AND IS HERE ON PURPOSE. The plan's
// Tier 1 gate is "zero unapproved writes", and a suite in which every task
// rewards writing cannot measure it: an agent that writes whenever asked
// anything would score perfectly. A guard task's correct final state is the
// seeded state, unchanged — and it is also what makes the NullAgent's score
// non-zero by an exact, pinned amount.
const (
	FamilyVoucherEntry = "voucher_entry"
	FamilyItemInvoice  = "item_invoice"
	FamilyBankMatch    = "bank_match"
	FamilyGSTReturns   = "gst_returns"
	FamilyNLQuery      = "nl_query"
	FamilyMasters      = "master_creation"
	FamilyGuard        = "guard"
)

// Languages. "hinglish" is Hindi in Latin script with English code-mixing
// ("Acme ko 50k ka bill banao"), which is what Indian SMB owners actually type
// and is a different tokenisation problem from Devanagari Hindi.
const (
	LangEnglish  = "en"
	LangHindi    = "hi"
	LangHinglish = "hinglish"
)

var knownFamilies = map[string]bool{
	FamilyVoucherEntry: true, FamilyItemInvoice: true, FamilyBankMatch: true,
	FamilyGSTReturns: true, FamilyNLQuery: true, FamilyMasters: true, FamilyGuard: true,
}

var knownLanguages = map[string]bool{LangEnglish: true, LangHindi: true, LangHinglish: true}

// Task is one fixture file.
type Task struct {
	ID          string `json:"id"`
	Family      string `json:"family"`
	Language    string `json:"language"`
	Synthetic   bool   `json:"synthetic"`
	Provenance  string `json:"provenance"`
	Description string `json:"description"`
	// Today is the date the agent is told it is. Utterances say "today" and
	// "last month"; without a pinned clock the expected state would depend on
	// the day the suite ran.
	Today     string `json:"today"`
	Seed      Seed   `json:"seed"`
	Utterance string `json:"utterance"`
	// ReferencePlan is the hand-written API script the ReferenceAgent executes.
	// It is NEVER shown to an agent under test.
	ReferencePlan []Step `json:"reference_plan"`
	// NaivePlan is set only on guard tasks: the write a credulous agent would
	// make. The WrongAgent executes it, which is what proves a guard task can
	// fail at all.
	NaivePlan []Step   `json:"naive_plan,omitempty"`
	Expected  Expected `json:"expected"`
}

// Seed is the starting book. Company is the POST /v1/companies body (minus id
// and name, which the harness mints); Steps are ordinary API steps, in order.
type Seed struct {
	Company map[string]any `json:"company"`
	Steps   []Step         `json:"steps"`
}

// Expected is the canonical state the books must be in after the run, plus the
// figures the agent's final answer must state.
//
// Ledgers and StockItems list every master the TASK owns — the seeded ones and
// the ones the run must create. The masters a fresh company is born with
// (Cash, Profit & Loss A/c, the payroll ledgers, the Nos unit) are not the
// task's to state: the harness snapshots them before seeding and requires them
// UNCHANGED, so a run that renames "Cash" still fails.
type Expected struct {
	Ledgers    []Ledger    `json:"ledgers"`
	StockItems []StockItem `json:"stock_items"`
	Vouchers   []Voucher   `json:"vouchers"`
	// Outputs are decimal strings that must each appear, as a number, in the
	// agent's final answer (tau-bench's "outputs"). Read-only tasks — NL query,
	// GSTR prep — are judged by these AND by an unchanged book.
	Outputs []string `json:"outputs"`
}

// Step is one API call in a plan. Path is relative to the company base
// ("/vouchers/sales"). Strings anywhere in Path or Body may carry symbolic
// references (refs.go) and {capture} placeholders, resolved at execution time,
// because a fixture cannot know the uuids a fresh company will mint.
type Step struct {
	Method string          `json:"method,omitempty"`
	Path   string          `json:"path,omitempty"`
	Body   json.RawMessage `json:"body,omitempty"`
	// Capture binds names to selectors over the JSON response (select.go), for
	// later steps' {name} placeholders and for the answer.
	Capture map[string]string `json:"capture,omitempty"`
	// Answer, on the final step, is the agent's reply. {name} placeholders are
	// filled from captures.
	Answer string `json:"answer,omitempty"`
}

// IsAnswer reports whether the step is the terminal reply rather than a call.
func (s Step) IsAnswer() bool { return s.Method == "" && s.Answer != "" }

// Prompt is everything an agent under test is allowed to know about a task.
type Prompt struct {
	TaskID    string
	Language  string
	Today     string
	Utterance string
}

// Prompt projects a task onto what an agent may see.
func (t Task) Prompt() Prompt {
	return Prompt{TaskID: t.ID, Language: t.Language, Today: t.Today, Utterance: t.Utterance}
}

// Validate checks the structural contract a harness run relies on. The
// ARITHMETIC contract (balanced vouchers, GST re-derived) is corpora/validate.py's.
func (t Task) Validate() error {
	var errs []string
	if t.ID == "" {
		errs = append(errs, "id is empty")
	}
	if !knownFamilies[t.Family] {
		errs = append(errs, fmt.Sprintf("unknown family %q", t.Family))
	}
	if !knownLanguages[t.Language] {
		errs = append(errs, fmt.Sprintf("unknown language %q", t.Language))
	}
	if !t.Synthetic || !strings.Contains(t.Provenance, "SYNTHETIC") {
		errs = append(errs, "fixture must declare synthetic:true and a SYNTHETIC provenance line")
	}
	if strings.TrimSpace(t.Utterance) == "" {
		errs = append(errs, "utterance is empty")
	}
	if len(t.ReferencePlan) == 0 || !t.ReferencePlan[len(t.ReferencePlan)-1].IsAnswer() {
		errs = append(errs, "reference_plan must end with an answer step")
	}
	if (t.Family == FamilyGuard) != (len(t.NaivePlan) > 0) {
		errs = append(errs, "naive_plan is required on, and only on, guard tasks")
	}
	if len(errs) > 0 {
		return fmt.Errorf("task %s: %s", t.ID, strings.Join(errs, "; "))
	}
	return nil
}

// LoadDir reads every *.json task under dir, sorted by id.
func LoadDir(dir string) ([]Task, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		// A suite directory with nothing in it must not produce a green run of
		// zero tasks.
		return nil, fmt.Errorf("tau: no task fixtures under %s", dir)
	}
	var tasks []Task
	seen := map[string]string{}
	var errs []error
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		var t Task
		if err := dec.Decode(&t); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", filepath.Base(p), err))
			continue
		}
		if err := t.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", filepath.Base(p), err))
			continue
		}
		if prev, dup := seen[t.ID]; dup {
			errs = append(errs, fmt.Errorf("%s: duplicate id %s (also in %s)", filepath.Base(p), t.ID, prev))
			continue
		}
		seen[t.ID] = filepath.Base(p)
		tasks = append(tasks, t)
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	return tasks, nil
}
