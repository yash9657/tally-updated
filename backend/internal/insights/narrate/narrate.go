// Package narrate is the morning digest's narrator: the ONLY part of the
// insight engine that may reach a model, and the number-provenance check that
// decides whether what the model wrote may be shown.
//
// ⛔ THE MODEL NEVER SEES A NUMBER AND NEVER WRITES ONE (PLAN-AGENTIC §2.2).
// It is sent, per item, the insight's title and its payload KEYS with their
// labels and units — not one value, not even the party's name (so no tenant
// data leaves for narration at all, and there is nothing for it to copy
// wrong). It returns one line per item in which every figure and every name
// is a {{key}} placeholder. Go then:
//
//  1. REFUSES the narration if the model's raw text contains any digit in any
//     script (ASCII, Devanagari, full-width, superscript — unicode.IsNumber),
//     any number word (English, romanised Hindi, Devanagari), a magnitude word
//     (lakh, crore, thousand, k), a % that does not follow a percentage key, a
//     placeholder naming a key the item does not have, or a line count/order
//     that does not match the items;
//  2. renders the placeholders from payload_json (insights.RenderTemplate), and
//  3. re-checks the RENDERED text: every number token in it must be a number
//     token of that item's payload values (CheckRendered). This second wall is
//     what the deterministic template must also pass, so a template bug is
//     caught by the same rule as a model's.
//
// Any refusal falls back to the deterministic templates, and the digest row
// records narrator = 'template' with narration_rejected saying why. No model
// configured is the same path with an empty reason.
//
// ⚠️ RESIDUAL, STATED: a number spelled in a language this checker does not
// list (Tamil words, say) passes step 1 — the English, Hindi and Devanagari
// lists here are not every language. Step 3 cannot see a spelled number
// either. The structural defence is upstream: the model is never TOLD a
// value, so a spelled number it produces is invented, not copied — and an
// invented number in words is what the adversarial list tries to close.
package narrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/insights"
	"github.com/khata/cored/internal/llm"
)

// Feature is the llm / trajectory feature name (and the cassette directory).
const Feature = "insight.narrate"

// PromptVersion is bumped whenever the prompt or the schema changes.
const PromptVersion = "insight-narrate-v1"

// Tool is the forced tool the model answers through.
const Tool = "narrate_digest"

// ErrRejected wraps every provenance refusal.
var ErrRejected = errors.New("narrate: narration rejected")

// Model narrates through an llm.Client. A nil Client narrates by template.
type Model struct {
	Client *llm.Client
}

var _ insights.Narrator = Model{}

const staticPrompt = `You write the owner's morning digest for an Indian small business's accounting software.
You are given up to five findings. For each, write ONE short sentence an owner reads on a phone.

RULES — a sentence that breaks any of them is discarded and a fixed template is shown instead:
- You are NOT given any figures or names. Refer to every amount, count, date, rate, name and number
  ONLY by its placeholder, written exactly as {{key}}, using the keys listed for THAT finding.
- Never write a digit, a number in words (one, ten, hundred, thousand, lakh, crore, ek, das, sau...),
  or a quantity like "double" or "half". Never do arithmetic.
- Write % only straight after a key whose unit is pct.
- One line per finding, in the order given, with its ref.`

var schema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["lines"],
  "properties": {
    "lines": {
      "type": "array", "maxItems": 5,
      "items": {
        "type": "object", "additionalProperties": false, "required": ["ref", "text"],
        "properties": {
          "ref":  {"type": "string", "pattern": "^i[1-5]$"},
          "text": {"type": "string", "maxLength": 400}
        }
      }
    }
  }
}`)

// Line is one line of the model's answer.
type Line struct {
	Ref  string `json:"ref"`
	Text string `json:"text"`
}

// Answer is the model's structured output.
type Answer struct {
	Lines []Line `json:"lines"`
}

// BuildRequest is the request for items. Exported so the replay test can
// re-derive the cassette key from the INPUT (tier0_test.go's per-feature rule).
func BuildRequest(companyID uuid.UUID, items []insights.DigestItem) *llm.Request {
	type field struct {
		Key   string `json:"key"`
		Label string `json:"label"`
		Unit  string `json:"unit"`
	}
	type finding struct {
		Ref    string  `json:"ref"`
		Title  string  `json:"title"`
		Keys   []field `json:"keys"`
		Action string  `json:"action"`
	}
	var fs []finding
	for i, it := range items {
		td, _ := insights.ByCode(it.Type)
		f := finding{Ref: fmt.Sprintf("i%d", i+1), Title: td.Title, Action: td.Action}
		for _, fl := range td.Fields {
			f.Keys = append(f.Keys, field{fl.Key, fl.Label, string(fl.Unit)})
		}
		fs = append(fs, f)
	}
	body, _ := json.MarshalIndent(fs, "", " ")
	temp := 0.0
	return &llm.Request{
		TenantID:      companyID,
		Feature:       Feature,
		PromptVersion: PromptVersion,
		Static:        []llm.Message{{Role: llm.RoleSystem, Content: staticPrompt}},
		Dynamic:       []llm.Message{{Role: llm.RoleUser, Content: "FINDINGS (keys only; no values are given to you):\n" + string(body)}},
		Tools:         []llm.Tool{{Name: Tool, Description: "Record one digest line per finding.", Parameters: schema}},
		ToolChoice:    llm.ToolChoice{Mode: llm.ToolChoiceNamed, Name: Tool},
		MaxTokens:     800,
		Temperature:   &temp,
	}
}

// Narrate implements insights.Narrator. It never returns an error for a bad
// narration: it returns the template narration with Rejected set. It returns
// an error only when there is nothing to fall back on, which cannot happen.
func (m Model) Narrate(ctx context.Context, companyID uuid.UUID, items []insights.DigestItem) (insights.Narration, error) {
	fallback := insights.TemplateNarration(items)
	for i, t := range fallback.Texts {
		if err := CheckRendered(t, items[i].Payload); err != nil {
			// A template that fails its own check is a bug in types.go; say
			// so loudly rather than send it.
			return insights.Narration{}, fmt.Errorf("narrate: template for %s fails provenance: %w", items[i].Type, err)
		}
	}
	if m.Client == nil || len(items) == 0 {
		return fallback, nil
	}
	req := BuildRequest(companyID, items)
	var ans Answer
	resp, err := m.Client.DecodeInto(ctx, req, Tool, &ans)
	if err != nil {
		fallback.Rejected = "model unavailable: " + err.Error()
		return fallback, nil
	}
	texts, err := Accept(ans, items)
	if err != nil {
		fallback.Rejected = err.Error()
		return fallback, nil
	}
	n := insights.Narration{Texts: texts, Narrator: "model", PromptVersion: PromptVersion}
	if resp != nil {
		n.ModelID = resp.Fingerprint.ServedModel
		if n.ModelID == "" {
			n.ModelID = m.Client.Model()
		}
	}
	return n, nil
}

// Accept validates a model answer against the items and renders it. It is
// the whole provenance gate; Narrate is plumbing around it.
func Accept(ans Answer, items []insights.DigestItem) ([]string, error) {
	if len(ans.Lines) != len(items) {
		return nil, fmt.Errorf("%w: %d lines for %d findings", ErrRejected, len(ans.Lines), len(items))
	}
	out := make([]string, len(items))
	for i, it := range items {
		ln := ans.Lines[i]
		if want := fmt.Sprintf("i%d", i+1); ln.Ref != want {
			return nil, fmt.Errorf("%w: line %d has ref %q, want %q", ErrRejected, i+1, ln.Ref, want)
		}
		if err := CheckRaw(ln.Text, it); err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrRejected, ln.Ref, err)
		}
		rendered := insights.RenderTemplate(ln.Text, it.Payload)
		if err := CheckRendered(rendered, it.Payload); err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrRejected, ln.Ref, err)
		}
		out[i] = rendered
	}
	return out, nil
}

// numberWords are refused anywhere in a model's raw line: English, romanised
// Hindi/Hinglish and Devanagari numerals and magnitudes. ("do" — Hindi two —
// is left out on purpose: it is also the English verb, and refusing every
// "do" would refuse honest sentences; its residual is the header's.)
var numberWords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`zero one two three four five six seven eight nine ten eleven twelve
		thirteen fourteen fifteen sixteen seventeen eighteen nineteen twenty thirty forty fifty sixty
		seventy eighty ninety hundred hundreds thousand thousands million millions billion billions
		lakh lakhs lac lacs crore crores cr k mn bn dozen dozens half halves double doubled twice
		triple tripled quarter percent
		ek teen char chaar paanch panch chhe chhah saat aath nau das bees pachaas pachas sau hazaar
		hazar hajar lakhon karod karor karodon arab
		एक दो तीन चार पाँच पांच छह सात आठ नौ दस बीस पचास सौ हज़ार हजार लाख करोड़ करोड अरब`) {
		numberWords[strings.ToLower(w)] = true
	}
}

var wordRE = regexp.MustCompile(`[\p{L}\p{M}]+`)

// statutoryNames are the form names whose digits are part of a NAME, not a
// figure: "GSTR-2B" says which return, not "two". They are removed before
// either check looks for numbers — a closed list, matched whole-word and
// case-insensitively, so "GSTR-2B" passes and "GSTR-2,00,000" does not.
var statutoryNameRE = regexp.MustCompile(`(?i)\b(GSTR-(?:1|2A|2B|3B|9)|Form 140|Rule 138)\b`)

func withoutStatutoryNames(s string) string { return statutoryNameRE.ReplaceAllString(s, " ") }

// CheckRaw is step 1: the model's line BEFORE rendering.
func CheckRaw(text string, it insights.DigestItem) error {
	td, ok := insights.ByCode(it.Type)
	if !ok {
		return fmt.Errorf("unknown type %s", it.Type)
	}
	if strings.Contains(text, "{{") != strings.Contains(text, "}}") {
		return errors.New("unbalanced placeholder braces")
	}
	for _, k := range insights.Placeholders(text) {
		if _, ok := it.Payload[k]; !ok {
			return fmt.Errorf("placeholder {{%s}} is not a key of this finding", k)
		}
	}
	// % only straight after a pct key.
	pctOK := map[int]bool{}
	for _, m := range regexp.MustCompile(`\{\{\s*([a-z][a-z0-9_]*)\s*\}\}\s*%`).FindAllStringSubmatchIndex(text, -1) {
		key := text[m[2]:m[3]]
		if f, ok := td.Field(key); ok && f.Unit == insights.UnitPct {
			pctOK[m[1]-1] = true
		}
	}
	for i, r := range text {
		if r == '%' && !pctOK[i] {
			return errors.New("a % that does not follow a percentage key")
		}
	}
	rest := withoutStatutoryNames(insights.StripPlaceholders(text))
	if strings.ContainsAny(rest, "{}") {
		return errors.New("a stray brace outside a placeholder")
	}
	for _, r := range rest {
		if unicode.IsNumber(r) {
			return fmt.Errorf("a literal numeral %q outside a placeholder", r)
		}
	}
	for _, w := range wordRE.FindAllString(rest, -1) {
		if numberWords[strings.ToLower(w)] {
			return fmt.Errorf("a number word %q", w)
		}
	}
	return nil
}

// numTokenRE finds number tokens in rendered text: digit runs with Indian or
// Western grouping and an optional decimal part.
var numTokenRE = regexp.MustCompile(`\d[\d,]*(?:\.\d+)?`)

// CheckRendered is step 3: every number in the rendered text must be a number
// that appears in the payload's values (compared with grouping commas removed,
// so "1,25,000.00" and "125000.00" are the same token). A date is its three
// tokens; a voucher number "INV-012" contributes "012".
func CheckRendered(text string, payload map[string]string) error {
	allowed := map[string]bool{}
	for _, v := range payload {
		for _, t := range numTokenRE.FindAllString(v, -1) {
			allowed[strings.ReplaceAll(strings.TrimRight(t, ","), ",", "")] = true
		}
	}
	for _, r := range text {
		if unicode.IsNumber(r) && (r < '0' || r > '9') {
			return fmt.Errorf("a non-ASCII numeral %q in the rendered text", r)
		}
	}
	for _, t := range numTokenRE.FindAllString(withoutStatutoryNames(text), -1) {
		n := strings.ReplaceAll(strings.TrimRight(t, ","), ",", "")
		if !allowed[n] {
			return fmt.Errorf("the number %s is not in the finding's payload", t)
		}
	}
	return nil
}
