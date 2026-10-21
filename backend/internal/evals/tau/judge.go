package tau

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/shopspring/decimal"
)

// Verdict is the judge's ruling on one trial.
type Verdict struct {
	Pass bool
	// Reasons names every difference, in a stable order, so a failing trial can
	// be read without re-running it. Empty on a pass.
	Reasons []string
}

// Judge rules on one trial.
//
// defaults is the snapshot of the company BEFORE seeding — the masters a
// company is born with. The state the books must be in is defaults ∪ the
// task's expected entities, compared as a MULTISET of canonical JSON: an
// entity missing from the books fails the trial, and so does an entity the
// books hold that nobody expected. There is no partial credit and no fuzzy
// match; "nearly the right amount" is the wrong amount.
func Judge(exp Expected, defaults, after State, answer string) Verdict {
	want := State{
		Ledgers:    overlay(defaults.Ledgers, exp.Ledgers, func(l Ledger) string { return l.Name }),
		StockItems: overlay(defaults.StockItems, exp.StockItems, func(i StockItem) string { return i.Name }),
		Vouchers:   append(append([]Voucher{}, defaults.Vouchers...), exp.Vouchers...),
	}.Canonical()
	got := after.Canonical()

	var reasons []string
	reasons = append(reasons, diffSet("ledger", keys(want.Ledgers), keys(got.Ledgers))...)
	reasons = append(reasons, diffSet("stock item", keys(want.StockItems), keys(got.StockItems))...)
	reasons = append(reasons, diffSet("voucher", keys(want.Vouchers), keys(got.Vouchers))...)
	reasons = append(reasons, checkOutputs(exp.Outputs, answer)...)
	return Verdict{Pass: len(reasons) == 0, Reasons: reasons}
}

// overlay is defaults with any master the task RESTATES (same name) replaced by
// the task's version, plus the task's own masters.
//
// A seed may properly change a born-with master — "Cash" with an opening
// balance is the ordinary case — and the fixture must then say what that
// master looks like afterwards. Replacing by NAME, not appending, is what keeps
// that expressible without letting the untouched defaults go unjudged: every
// default the task does not restate must still be exactly as it was born.
func overlay[T any](defaults, restated []T, name func(T) string) []T {
	over := map[string]bool{}
	for _, r := range restated {
		over[name(r)] = true
	}
	out := make([]T, 0, len(defaults)+len(restated))
	for _, d := range defaults {
		if !over[name(d)] {
			out = append(out, d)
		}
	}
	return append(out, restated...)
}

func keys[T any](xs []T) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = key(x)
	}
	return out
}

// diffSet reports multiset differences between want and got.
func diffSet(kind string, want, got []string) []string {
	count := map[string]int{}
	for _, w := range want {
		count[w]++
	}
	for _, g := range got {
		count[g]--
	}
	var out []string
	for k, n := range count {
		switch {
		case n > 0:
			out = append(out, fmt.Sprintf("missing %s (x%d): %s", kind, n, k))
		case n < 0:
			out = append(out, fmt.Sprintf("unexpected %s (x%d): %s", kind, -n, k))
		}
	}
	sort.Strings(out)
	return out
}

// numberRe finds figures in free text: 59000, 59,000.00, 5,90,000 (Indian
// grouping), with an optional lakh/crore multiplier after them.
//
// ⚠️ "Cr" IS NOT A MULTIPLIER HERE, deliberately: in an Indian ledger "59,000
// Cr" is a CREDIT balance, and reading it as 59,000 crore would turn the most
// common way a balance is written into a figure ten million times too large.
// UUIDs are identifiers, not money. Exclude the complete token, including its
// numeric segments, from both required-output matching and extra-figure checks.
var uuidInAnswer = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)

var nonMoneyIDs = regexp.MustCompile(`(?i)\b[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z][0-9A-Z]Z[0-9A-Z]\b|\b(?:HSN|SAC)(?:\s+code)?[:\s]+[0-9]{4,8}\b|\b(?:filing\s+)?period[:\s]+(?:0[1-9]|1[0-2])20[0-9]{2}\b|\b20[0-9]{2}-[0-9]{2}-[0-9]{2}\b|https?://[^\s)]+`)

func answerNumberText(answer string) string {
	s := devanagariDigits.Replace(answer)
	s = uuidInAnswer.ReplaceAllString(s, " ")
	return nonMoneyIDs.ReplaceAllString(s, " ")
}

var numberRe = regexp.MustCompile(`(?i)(\d[\d,]*(?:\.\d+)?)\s*(lakhs?|lacs?|crores?|लाख|करोड़|करोड)?`)

// devanagariDigits maps ० … ९ onto 0 … 9. A Hindi answer may write its figures
// in either script, and "₹५९,०००" states the same amount as "₹59,000".
var devanagariDigits = strings.NewReplacer(
	"०", "0", "१", "1", "२", "2", "३", "3", "४", "4",
	"५", "5", "६", "6", "७", "7", "८", "8", "९", "9",
)

// AnswerFigures extracts every figure an answer states, as magnitudes.
//
// ⚠️ MAGNITUDES, NOT SIGNED VALUES. A hyphen in free text is a date separator
// ("2025-06-12") far more often than a minus sign, and a balance's direction is
// said in words ("Dr", "payable", "बकाया"). Outputs are therefore stated as
// magnitudes, and the DIRECTION of a balance is judged — where it matters — by
// the ledger state, not the prose.
func AnswerFigures(answer string) []decimal.Decimal {
	s := answerNumberText(answer)
	var out []decimal.Decimal
	for _, m := range numberRe.FindAllStringSubmatch(s, -1) {
		d, err := decimal.NewFromString(strings.ReplaceAll(m[1], ",", ""))
		if err != nil {
			continue
		}
		switch strings.TrimSuffix(strings.ToLower(m[2]), "s") {
		case "lakh", "lac", "लाख":
			d = d.Mul(decimal.New(1, 5))
		case "crore", "करोड़", "करोड":
			d = d.Mul(decimal.New(1, 7))
		}
		out = append(out, d)
	}
	return out
}

// checkOutputs requires every expected figure to appear in the answer, to the
// paisa. A figure stated in lakh must still be exact: "1.18 lakh" is 118000.00,
// and if the books say 118000.50 the answer is wrong.
func checkOutputs(expected []string, answer string) []string {
	// Write-only tasks specify their exact resulting books, without an answer
	// constraint. Acknowledging the posted amounts is not an extra-output error.
	if len(expected) == 0 {
		return nil
	}
	figs := AnswerFigures(answer)
	var out []string
	for _, e := range expected {
		want, err := decimal.NewFromString(e)
		if err != nil {
			out = append(out, fmt.Sprintf("fixture output %q is not a decimal", e))
			continue
		}
		want = want.Abs().Round(2)
		found := false
		for _, f := range figs {
			if f.Round(2).Equal(want) {
				found = true
				break
			}
		}
		if !found {
			out = append(out, fmt.Sprintf("answer does not state %s (answer: %q)", want.StringFixed(2), trimAnswer(answer)))
		}
	}
	if extra := extraMoneyFigures(expected, answer); len(extra) > maxExtraFigures(len(expected)) {
		out = append(out, fmt.Sprintf("answer states %d money figures the task did not ask for (%s…) — "+
			"a figure found among many is not an answer (answer: %q)", len(extra), strings.Join(extra[:min(3, len(extra))], ", "), trimAnswer(answer)))
	}
	return out
}

// maxExtraFigures bounds how many money figures an answer may state beyond the
// ones the task asks for: one per expected output, at least one. A sentence
// giving context ("29,750 of the 35,000 billed") fits; a dump does not.
//
// ⛔ WHY A BOUND AT ALL. Without it "every expected figure appears somewhere"
// was satisfied by an answer that listed the whole trial balance, so a
// verbose model passed every nl_query and gst_returns task by shotgun (the
// wave-1 review) — inflating pass^k for exactly the least useful answers.
func maxExtraFigures(expected int) int { return max(1, expected) }

// moneyShapedRe is a figure written the way money is written: a rupee sign or
// Rs/INR before it, digit grouping, exactly two decimals, a lakh/crore word,
// or a plain integer of four or more digits. Years (1900–2100), day and month
// numbers, "3.1(a)", "@18%" and similar are not money-shaped and never count.
var moneyShapedRe = regexp.MustCompile(`(?i)(₹|\brs\.?|\binr)?\s*(\d[\d,]*(?:\.\d+)?)\s*(lakhs?|lacs?|crores?|लाख|करोड़|करोड)?`)

func extraMoneyFigures(expected []string, answer string) []string {
	want := map[string]bool{}
	for _, e := range expected {
		if d, err := decimal.NewFromString(e); err == nil {
			want[d.Abs().Round(2).StringFixed(2)] = true
		}
	}
	s := answerNumberText(answer)
	seen := map[string]bool{}
	var out []string
	for _, m := range moneyShapedRe.FindAllStringSubmatch(s, -1) {
		raw := strings.TrimRight(m[2], ",")
		intPart, frac, _ := strings.Cut(raw, ".")
		shaped := m[1] != "" || m[3] != "" || strings.Contains(raw, ",") || len(frac) == 2
		if !shaped && frac == "" && len(intPart) >= 4 {
			if n, err := decimal.NewFromString(intPart); err == nil && !(n.GreaterThanOrEqual(decimal.NewFromInt(1900)) && n.LessThanOrEqual(decimal.NewFromInt(2100))) {
				shaped = true
			}
		}
		if !shaped {
			continue
		}
		// The same figure, parsed exactly as AnswerFigures parses it.
		for _, d := range AnswerFigures(m[0]) {
			k := d.Round(2).StringFixed(2)
			if !want[k] && !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	return out
}

func trimAnswer(a string) string {
	if len(a) > 200 {
		return a[:200] + "…"
	}
	return a
}
