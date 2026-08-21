package nlq

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/khata/cored/internal/query"
)

// TestSchemaEnumsMatchDSLConstants is an INDEPENDENT-ORACLE consistency check:
// the emit_query JSON Schema is hand-written (schema.go), and here we assert its
// entity/func/op enums equal query's exported Go constants (dsl.go). If either
// side drifts — a new op added to dsl.go but not the schema, or a typo in the
// schema — this fails. The oracle is the Go constants, NOT the schema echoing
// itself.
func TestSchemaEnumsMatchDSLConstants(t *testing.T) {
	var schema struct {
		Properties struct {
			Entity  struct{ Enum []string } `json:"entity"`
			Metrics struct {
				Items struct {
					Properties struct {
						Func struct{ Enum []string } `json:"func"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"metrics"`
			Filters struct {
				Items struct {
					Properties struct {
						Op struct{ Enum []string } `json:"op"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"filters"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(emitQuerySchema), &schema); err != nil {
		t.Fatalf("emitQuerySchema is not valid JSON: %v", err)
	}

	// entity enum: the four query entity constants PLUS the "" escape hatch that
	// nlq adds so the model can signal "cannot answer from the catalog".
	wantEntities := []string{
		string(query.EntityLedgerBalances),
		string(query.EntityVouchers),
		string(query.EntityPostings),
		string(query.EntityOutstandings),
		"",
	}
	assertSet(t, "entity enum", wantEntities, schema.Properties.Entity.Enum)

	wantFuncs := []string{
		string(query.AggSum), string(query.AggCount),
		string(query.AggMin), string(query.AggMax), string(query.AggAvg),
	}
	assertSet(t, "func enum", wantFuncs, schema.Properties.Metrics.Items.Properties.Func.Enum)

	wantOps := []string{
		string(query.OpEq), string(query.OpNe), string(query.OpGt), string(query.OpGe),
		string(query.OpLt), string(query.OpLe), string(query.OpLike), string(query.OpIn),
	}
	assertSet(t, "op enum", wantOps, schema.Properties.Filters.Items.Properties.Op.Enum)
}

// TestSystemPromptCarriesNoSQL is a defense-in-depth check that the runtime-built
// prompt exposes NAMES ONLY: no SQL fragment from the whitelist leaks into it.
func TestSystemPromptCarriesNoSQL(t *testing.T) {
	p := systemPrompt(time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC), 0)
	for _, leak := range []string{"SELECT", "vv.taxable_value", "lb.debit", "voucher_head", "COALESCE", "::text"} {
		if strings.Contains(p, leak) {
			t.Errorf("system prompt leaks a SQL fragment %q", leak)
		}
	}
	// It must still mention the entities so the model can use them.
	for _, ent := range []string{"ledger_balances", "vouchers", "postings", "outstandings"} {
		if !strings.Contains(p, ent) {
			t.Errorf("system prompt omits entity %q", ent)
		}
	}
}

func assertSet(t *testing.T, label string, want, got []string) {
	t.Helper()
	w := append([]string(nil), want...)
	g := append([]string(nil), got...)
	sort.Strings(w)
	sort.Strings(g)
	if len(w) != len(g) {
		t.Errorf("%s: want %v, got %v", label, w, g)
		return
	}
	for i := range w {
		if w[i] != g[i] {
			t.Errorf("%s: want %v, got %v", label, w, g)
			return
		}
	}
}

// --------------------------------------------------------------------------
// L5: the prompt IS the production translator's behaviour.
// --------------------------------------------------------------------------

// promptExamples extracts every worked example's DSL JSON from the rendered
// prompt by finding the "-> emit_query " marker. It parses the PROMPT TEXT
// rather than any Go literal, so an example edited by hand is what gets checked.
func promptExamples(t *testing.T, prompt string) []string {
	t.Helper()
	const marker = "-> emit_query "
	var out []string
	for rest := prompt; ; {
		i := strings.Index(rest, marker)
		if i < 0 {
			break
		}
		rest = rest[i+len(marker):]
		line := rest
		if j := strings.IndexByte(line, '\n'); j >= 0 {
			line = line[:j]
		}
		out = append(out, strings.TrimSpace(line))
	}
	if len(out) == 0 {
		t.Fatal("no worked examples found in the prompt")
	}
	return out
}

// decodeExample decodes one example EXACTLY as ToQuery decodes a real tool_use
// input — DisallowUnknownFields — so an example whose shape the production
// decoder would reject fails here instead of in front of a customer.
func decodeExample(t *testing.T, js string) query.Query {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(js))
	dec.DisallowUnknownFields()
	var q query.Query
	if err := dec.Decode(&q); err != nil {
		t.Fatalf("example is not decodable by the production decoder: %v\n  %s", err, js)
	}
	return q
}

// TestSystemPromptExamplesCompile pins that every worked example is a query the
// compiler ACCEPTS (the refusal example, entity "", excepted). A prompt teaching
// a shape the compiler rejects turns every question it matches into a 422 that
// looks like the user's fault.
func TestSystemPromptExamplesCompile(t *testing.T) {
	prompt := systemPrompt(time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC), 0)
	sawRefusal := false
	for _, js := range promptExamples(t, prompt) {
		q := decodeExample(t, js)
		if q.Entity == "" {
			sawRefusal = true
			continue
		}
		if _, _, err := query.Compile(q); err != nil {
			t.Errorf("the compiler rejects a worked example: %v\n  %s", err, js)
		}
	}
	if !sawRefusal {
		// The escape hatch is only usable if the model has seen it emitted.
		t.Error("no entity \"\" refusal example in the prompt")
	}
}

// TestSystemPromptTeachesThePLMeasure is D51 pinned AT THE PROMPT. The worked
// sales/purchase example must emit the postings/natural_net/pl_group shape, and
// NO example may sum taxable_value: that column excludes GST and excludes credit
// notes, so a prompt teaching it teaches the defect this lane closed.
func TestSystemPromptTeachesThePLMeasure(t *testing.T) {
	prompt := systemPrompt(time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC), 0)
	saw := false
	for _, js := range promptExamples(t, prompt) {
		q := decodeExample(t, js)
		for _, m := range q.Metrics {
			if m.Column == "taxable_value" {
				t.Errorf("a worked example sums taxable_value — that is D51's shape:\n  %s", js)
			}
		}
		if !strings.Contains(js, "purchase") && !strings.Contains(js, "sale") {
			continue
		}
		saw = true
		if q.Entity != query.EntityPostings {
			t.Errorf("the sales/purchase example must be measured on postings, got %q:\n  %s", q.Entity, js)
		}
		if len(q.Metrics) != 1 || q.Metrics[0].Column != "natural_net" || q.Metrics[0].Func != query.AggSum {
			t.Errorf("the sales/purchase example must be sum(natural_net):\n  %s", js)
		}
		found := false
		for _, f := range q.Filters {
			if f.Column == "pl_group" {
				found = true
			}
		}
		if !found {
			t.Errorf("the sales/purchase example must filter on pl_group (the P&L root subtree):\n  %s", js)
		}
	}
	if !saw {
		t.Error("the prompt has no worked sales/purchase example — rule 6 is then untaught by demonstration")
	}
}

// TestSystemPromptWindowsMatchTheTranslator is D21 pinned AT THE PROMPT, against
// an INDEPENDENT oracle: the window the prompt tells the model to copy for a
// phrase must equal the window FakeTranslator's ToQuery actually answers that
// same phrase with. The oracle runs the whole NL path (ToQuery -> relativeRange
// -> parsePeriod), not the helper the prompt is built from, so the two
// translators cannot name different periods for one question.
func TestSystemPromptWindowsMatchTheTranslator(t *testing.T) {
	now := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	prompt := systemPrompt(now, 0)
	f := query.FakeTranslator{Now: func() time.Time { return now }}

	for _, phrase := range []string{"this month", "last month", "this quarter", "last quarter", "this year", "last year"} {
		q, err := f.ToQuery(context.Background(), uuid.Nil, "sales "+phrase)
		if err != nil {
			t.Fatalf("oracle: FakeTranslator refused %q: %v", "sales "+phrase, err)
		}
		if q.DateRange == nil {
			t.Fatalf("oracle: FakeTranslator produced no window for %q", phrase)
		}
		want := "- \"" + phrase + "\": " + q.DateRange.From + " .. " + q.DateRange.To
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt does not carry the translator's window for %q\n  want line: %s", phrase, want)
		}
	}
	// TODAY must be stated: without it the model has no anchor for a phrase the
	// table does not list ("April 2026", "since 1 July").
	if !strings.Contains(prompt, "TODAY IS 2026-08-21.") {
		t.Error("prompt does not state today's date")
	}
}

// TestSystemPromptRefusesOtherScopes is D50 pinned AT THE PROMPT: the refusal
// rules must name the cross-tenant case, because ClaudeTranslator does NOT run
// namesAnotherScope — the model is the only thing that can refuse there.
func TestSystemPromptRefusesOtherScopes(t *testing.T) {
	prompt := systemPrompt(time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC), 0)
	for _, want := range []string{"all companies", "every tenant", "UUID"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the refusal rules do not mention %q", want)
		}
	}
}
