package agent

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
)

var internalID = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)

func finalText(d ModelDecision) string {
	if strings.TrimSpace(d.Final) != "" {
		return d.Final
	}
	var nested struct {
		Final string `json:"final"`
	}
	_ = json.Unmarshal(d.Args, &nested)
	return nested.Final
}

// Reject and request a revision; never truncate money qualifications, status,
// or a draft halfway through. Letters/digits exclude Devanagari combining marks
// so script encoding does not unfairly consume the visible-length allowance.
// This is a presentation contract, not a semantic truth or language-quality judge.
func finalProblem(final, details string) string {
	visible := 0
	for _, r := range final {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			visible++
		}
	}
	if len(strings.Fields(final)) > 250 || visible > 1800 {
		return "summary exceeds 250 words or 1800 letters/digits"
	}
	all := final + "\n" + details
	if internalID.MatchString(all) {
		return "replace internal IDs with verified voucher numbers and dates"
	}
	for _, claim := range []string{"will realise less", "will realize less", "guaranteed to save", "savings are guaranteed", "recovers the most", "without a margin hit", "cheapest exit"} {
		if strings.Contains(strings.ToLower(all), claim) {
			return "future recovery, margin and savings depend on verified terms and costs; remove the unsupported outcome claim"
		}
	}
	for _, claim := range []string{"your money is safe", "aapke paise safe", "आपके पैसे सुरक्षित"} {
		if strings.Contains(strings.ToLower(all), claim) {
			return "confirm only that this run created no payment; account-wide safety has not been verified"
		}
	}
	return ""
}
