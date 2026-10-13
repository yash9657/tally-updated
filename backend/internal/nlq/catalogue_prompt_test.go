package nlq

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/catalogue"
	"github.com/khata/cored/internal/llm"
	"github.com/khata/cored/internal/query"
)

// catalogue_prompt_test.go pins W3 wave 2's change to the translator prompt:
// the semantic catalogue is IN it now — the entity guide in the cacheable half,
// the reports a question selects in the per-call half — and neither half
// breaks the cache contract (Static byte-identical across calls) or the
// privacy contract (no tenant data, no echo of the question outside the user
// turn).

// TestTheEntityGuideIsStaticAndComplete: every DSL entity's catalogue entry
// reaches the cacheable half, in words, and the half does not move with the
// question.
func TestTheEntityGuideIsStaticAndComplete(t *testing.T) {
	s := staticPrompt()
	if !strings.Contains(s, "ENTITY GUIDE") {
		t.Fatal("the static half carries no ENTITY GUIDE")
	}
	ents := catalogue.Entities()
	if len(ents) != len(query.Catalog()) {
		t.Fatalf("catalogue describes %d entities, the DSL has %d", len(ents), len(query.Catalog()))
	}
	for _, e := range ents {
		line := "- " + string(e.Entity) + ": " + e.Description
		if !strings.Contains(s, line) {
			t.Errorf("entity %s's catalogue description is not in the static half", e.Entity)
		}
		for _, w := range append(append([]string{}, e.Synonyms...), e.Hinglish...) {
			if !strings.Contains(s, w) {
				t.Errorf("entity %s: the word %q is not in the static half", e.Entity, w)
			}
		}
	}
	if !strings.Contains(s, "11. CATALOGUE HINTS") {
		t.Error("rule 11 (hints are shapes, not answers) is missing")
	}

	tr := &Translator{Now: func() time.Time { return time.Date(2025, 6, 30, 9, 0, 0, 0, time.UTC) }}
	a := tr.request(uuid.New(), "Kaveri ka kitna baki hai")
	b := tr.request(uuid.New(), "salary register for May")
	if !reflect.DeepEqual(a.Static, b.Static) {
		t.Fatalf("Static differs between two QUESTIONS — the hints leaked into the prefix; %s",
			firstDifference(joinContent(a.Static), joinContent(b.Static)))
	}
	if llm.StaticPromptHash(a) != llm.StaticPromptHash(b) {
		t.Fatal("StaticPromptHash differs between two questions")
	}
	if strings.Contains(joinContent(a.Static), "CATALOGUE HINTS for this question") {
		t.Fatal("per-question hints are in the static half")
	}
}

// TestHintsAreThePerQuestionHalf: the hints are a Dynamic system message just
// before the question, they name what the selector chose, and they differ
// between questions.
func TestHintsAreThePerQuestionHalf(t *testing.T) {
	tr := &Translator{Now: func() time.Time { return time.Date(2025, 6, 30, 9, 0, 0, 0, time.UTC) }}
	cases := []struct {
		q, want, not string
	}{
		{"Kaveri ka kitna baki hai", "1. Outstandings (receivables) (report outstandings) -> entities: outstandings", ""},
		{"How much do we still owe Kumar Packaging?", "1. Bills Payable (report bills-payable) -> entities: outstandings", ""},
		{"salary register for May", "The DSL cannot answer this report: Payroll figures", ""},
		{"what is the weather in Pune", "CATALOGUE HINTS: no catalogue report shares a word", "example shape"},
	}
	seen := map[string]bool{}
	for _, c := range cases {
		req := tr.request(uuid.New(), c.q)
		n := len(req.Dynamic)
		if n < 3 || req.Dynamic[n-1].Role != llm.RoleUser || req.Dynamic[n-2].Role != llm.RoleSystem {
			t.Fatalf("%q: Dynamic is not [..., hints(system), question(user)]: %+v", c.q, req.Dynamic)
		}
		hints := req.Dynamic[n-2].Content
		if hints != hintsPrompt(c.q) {
			t.Errorf("%q: the hints message is not hintsPrompt(question)", c.q)
		}
		if !strings.Contains(hints, c.want) {
			t.Errorf("%q: hints do not contain %q:\n%s", c.q, c.want, hints)
		}
		if c.not != "" && strings.Contains(hints, c.not) {
			t.Errorf("%q: hints unexpectedly contain %q", c.q, c.not)
		}
		if seen[hints] {
			t.Errorf("%q: two different questions produced the same hints", c.q)
		}
		seen[hints] = true
	}
}

// TestHintsNeverEchoTheQuestion: the hints are catalogue text only. A word
// that is only in the question — a party name, a UUID — must not reappear in
// them (it rides once, in the user turn, where llm's tenant-id needle check
// and the privacy test already look).
func TestHintsNeverEchoTheQuestion(t *testing.T) {
	id := uuid.New().String()
	for _, q := range []string{
		"how much does Zyxwvut Traders owe us " + id,
		"Zyxwvut ka kitna baki hai",
		"trial balance for Zyxwvut",
	} {
		h := hintsPrompt(q)
		if strings.Contains(h, "Zyxwvut") || strings.Contains(h, id) {
			t.Errorf("hints echo question text: %q ->\n%s", q, h)
		}
	}
}

// TestHintExamplesDecodeAndCompile: every report's example, rendered EXACTLY
// as a hint prints it, decodes with the production decoder's strictness,
// carries every property the emit_query schema requires, and compiles. A hint
// that taught a schema-invalid or uncompilable shape would turn every question
// it matches into a repair round trip or a 422.
func TestHintExamplesDecodeAndCompile(t *testing.T) {
	var schema struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal([]byte(emitQuerySchema), &schema); err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, e := range catalogue.Reports() {
		for _, q := range e.DSL {
			js := emitShape(q)
			var raw map[string]json.RawMessage
			if err := json.Unmarshal([]byte(js), &raw); err != nil {
				t.Fatalf("%s: %v", e.Report, err)
			}
			for _, k := range schema.Required {
				v, ok := raw[k]
				if !ok {
					t.Errorf("%s: example lacks required %q: %s", e.Report, k, js)
				}
				if k != "date_range" && string(v) == "null" {
					t.Errorf("%s: example has %q null (schema wants an array/integer): %s", e.Report, k, js)
				}
			}
			got := decodeExample(t, js)
			if _, _, err := query.Compile(got); err != nil {
				t.Errorf("%s: hint example does not compile: %v\n  %s", e.Report, err, js)
			}
			checked++
		}
	}
	if checked < 13 {
		t.Errorf("only %d hint examples checked; the catalogue has 13 entries with DSL", checked)
	}
	t.Logf("%d hint examples: every one schema-shaped, strictly decodable and compiling", checked)
}

// TestTauNLQuestionsReachTheirEntityThroughTheHints measures, over W1a's 30
// nl_query tau utterances (en / hi / hinglish), how often the hints put an
// entity that CAN answer the task among the entities they name. It is a
// selector measurement through the prompt path, not a model score (no model
// has run). The floor is the figure measured when the hints landed; it may
// only rise. ⚠️ The Devanagari utterances are expected misses: the catalogue
// has no Devanagari phrasings yet (d122 §6 item 11).
func TestTauNLQuestionsReachTheirEntityThroughTheHints(t *testing.T) {
	files, err := filepath.Glob("../../../corpora/tau/nlq-*.json")
	if err != nil || len(files) != 30 {
		t.Fatalf("want the 30 nl_query tau tasks, found %d (%v)", len(files), err)
	}
	// Which entities can answer each task family (the task's own description).
	answerable := map[string][]string{
		"bank":     {"ledger_balances"},                 // closing balance of a bank ledger
		"creditor": {"ledger_balances", "outstandings"}, // closing balance of a creditor ledger
		"debtor":   {"ledger_balances", "outstandings"}, // closing balance of a debtor ledger
		"expense":  {"ledger_balances", "postings"},     // closing balance of an expense ledger
		"outtax":   {"ledger_balances", "postings"},     // output tax ledgers by head
		"sales":    {"postings", "ledger_balances"},     // sales ledger total
	}
	type row struct{ lang, family string }
	hit := map[row]int{}
	total := map[row]int{}
	hits := 0
	var misses []string
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var task struct {
			ID        string `json:"id"`
			Language  string `json:"language"`
			Utterance string `json:"utterance"`
		}
		if err := json.Unmarshal(raw, &task); err != nil {
			t.Fatal(err)
		}
		fam := strings.Split(task.ID, "-")[1]
		ok := false
		for _, m := range catalogue.Select(task.Utterance, hintTopK) {
			for _, en := range m.Entities {
				for _, want := range answerable[fam] {
					if string(en) == want {
						ok = true
					}
				}
			}
		}
		r := row{task.Language, fam}
		total[r]++
		if ok {
			hit[r]++
			hits++
		} else {
			misses = append(misses, task.ID+": "+task.Utterance)
		}
	}
	var keys []row
	for r := range total {
		keys = append(keys, r)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].lang != keys[j].lang {
			return keys[i].lang < keys[j].lang
		}
		return keys[i].family < keys[j].family
	})
	byLang := map[string][2]int{}
	for _, r := range keys {
		v := byLang[r.lang]
		v[0] += hit[r]
		v[1] += total[r]
		byLang[r.lang] = v
	}
	t.Logf("tau nl_query through the hints: %d/%d reach an answering entity (en %d/%d, hinglish %d/%d, hi %d/%d)",
		hits, len(files), byLang["en"][0], byLang["en"][1], byLang["hinglish"][0], byLang["hinglish"][1], byLang["hi"][0], byLang["hi"][1])
	for _, m := range misses {
		t.Logf("  miss: %s", m)
	}
	if hits < tauHintFloor {
		t.Errorf("only %d of 30 tau nl_query utterances reach an answering entity through the hints; the floor is %d", hits, tauHintFloor)
	}
}

// tauHintFloor is the figure measured when the hints landed: 21/30 (en 8/10,
// hinglish 8/10, hi 5/10). The catalogue was NOT tuned against these
// utterances (the wave-1 review's finding 8 is what tuning to a test set
// looks like); raise the floor when the catalogue improves for its own
// reasons, never lower it to make a change pass.
const tauHintFloor = 21
