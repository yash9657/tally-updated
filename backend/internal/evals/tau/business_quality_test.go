package tau_test

import (
	"bytes"
	"encoding/json"
	"github.com/shopspring/decimal"
	"regexp"
	"strings"
	"testing"
)

// These checks establish necessary evidence/actionability, not customer
// satisfaction. Full answers and source facts are retained for human review.
func businessQuality(c businessCase, answer string) []string {
	var reasons []string
	lower := strings.ToLower(answer)
	if strings.TrimSpace(answer) == "" {
		return []string{"empty answer"}
	}
	if c.Finding == "" {
		if !containsAny(lower, []string{"insufficient", "no transaction", "no book", "no voucher", "not enough", "no data", "empty", "no business", "no sales", "no recorded"}) {
			reasons = append(reasons, "does not acknowledge insufficient evidence")
		}
		if !containsAny(lower, []string{"import", "upload", "invoice", "opening balance", "transactions"}) {
			reasons = append(reasons, "no concrete onboarding input requested")
		}
		if regexp.MustCompile(`(?i)(?:₹|rs\.?\s*|inr\s*)[1-9]`).MatchString(answer) {
			reasons = append(reasons, "unsupported nonzero financial claim")
		}
		return reasons
	}
	if !strings.Contains(lower, strings.ToLower(c.Entity)) {
		reasons = append(reasons, "missing named entity")
	}
	if c.Industry == "services" && regexp.MustCompile(`100.days (past|beyond|overdue)`).MatchString(lower) {
		reasons = append(reasons, "invoice age confused with days beyond payment norm")
	}
	normalized := strings.ReplaceAll(answer, ",", "")
	for _, number := range c.Numbers {
		if !regexp.MustCompile(`(?:^|[^\d])` + regexp.QuoteMeta(number) + `(?:\.0+)?(?:[^\d]|$)`).MatchString(normalized) {
			reasons = append(reasons, "missing evidenced figure "+number)
		}
	}
	if !containsAny(lower, c.Actions) {
		reasons = append(reasons, "no relevant next action")
	}
	if !containsAny(lower, []string{"hi ", "dear ", "hello ", "checklist", "message:", "message to", "draft"}) || len(strings.Fields(answer)) < 55 {
		reasons = append(reasons, "requested usable draft/checklist missing")
	}
	if !containsAny(lower, []string{"stock", "voucher", "invoice", "bill", "report", "purchase", "outstanding"}) {
		reasons = append(reasons, "source evidence missing")
	}
	if containsAny(lower, []string{"savings are guaranteed", "guaranteed to save", "will save ₹", "will save rs", "i have sent", "i sent the", "i deleted", "i have paid"}) {
		reasons = append(reasons, "unsupported guarantee or execution claim")
	}
	return reasons
}

func allowReadResultNumbers(records []map[string]any, allowed map[string]bool) {
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for _, child := range x {
				walk(child)
			}
		case []any:
			for _, child := range x {
				walk(child)
			}
		case string:
			if n, err := decimal.NewFromString(x); err == nil {
				allowed[n.String()] = true
			}
		case json.Number:
			if n, err := decimal.NewFromString(x.String()); err == nil {
				allowed[n.String()] = true
			}
		}
	}
	for _, record := range records {
		raw, ok := record["messages"].(json.RawMessage)
		if !ok {
			continue
		}
		var messages []struct{ Role, Content string }
		if json.Unmarshal(raw, &messages) != nil {
			continue
		}
		for _, message := range messages {
			if message.Role != "user" || !strings.HasPrefix(message.Content, "RESULT OF tool:") {
				continue
			}
			_, body, ok := strings.Cut(message.Content, "\n")
			if !ok {
				continue
			}
			var result struct {
				Tool struct {
					Tool   string
					OK     bool
					Result json.RawMessage
				}
			}
			if json.Unmarshal([]byte(body), &result) != nil || !result.Tool.OK {
				continue
			}
			if result.Tool.Tool != "actions.read" && result.Tool.Tool != "query_books" && !strings.HasPrefix(result.Tool.Tool, "get_") {
				continue
			}
			var data any
			decoder := json.NewDecoder(bytes.NewReader(result.Tool.Result))
			decoder.UseNumber()
			if decoder.Decode(&data) == nil {
				walk(data)
			}
		}
	}
}

func TestBusinessMoneyUsesReadResultsNotModelClaims(t *testing.T) {
	allowed := map[string]bool{}
	allowReadResultNumbers([]map[string]any{{"messages": json.RawMessage(`[
	{"role":"assistant","content":"Rs 999"},
	{"role":"user","content":"RESULT OF tool:0\n{\"tool\":{\"tool\":\"actions.read\",\"ok\":true,\"result\":{\"payable\":\"24000.00\"}}}"},
	{"role":"user","content":"RESULT OF tool:1\n{\"tool\":{\"tool\":\"actions.preview\",\"ok\":true,\"result\":{\"amount\":\"888\"}}}"}
	]`)}}, allowed)
	if !allowed["24000"] || allowed["999"] || allowed["888"] {
		t.Fatal(allowed)
	}
}

// Every explicit rupee claim must be present in source books or verified
// findings. This complements (not replaces) human review of attribution and
// whether the suggested action is commercially sensible.
func unsupportedMoney(answer string, allowed map[string]bool) []string {
	var reasons []string
	pattern := regexp.MustCompile(`(?i)(?:₹|\brs\.?|\binr)\s*([0-9][0-9,]*(?:\.[0-9]+)?)\s*(lakhs?|lacs?|crores?)?`)
	for _, match := range pattern.FindAllStringSubmatch(answer, -1) {
		n, err := decimal.NewFromString(strings.ReplaceAll(strings.TrimRight(match[1], ","), ",", ""))
		if err != nil {
			reasons = append(reasons, "invalid financial figure")
			continue
		}
		if strings.HasPrefix(strings.ToLower(match[2]), "la") {
			n = n.Mul(decimal.NewFromInt(100000))
		}
		if strings.HasPrefix(strings.ToLower(match[2]), "crore") {
			n = n.Mul(decimal.NewFromInt(10000000))
		}
		if !allowed[n.String()] {
			reasons = append(reasons, "unsupported rupee figure "+n.String())
		}
	}
	return reasons
}

func TestBusinessMoneyClaimsNeedEvidence(t *testing.T) {
	allowed := map[string]bool{"25000": true, "100000": true}
	if r := unsupportedMoney("Rs 25,000.00; INR 1 lakh", allowed); len(r) != 0 {
		t.Fatal(r)
	}
	if r := unsupportedMoney("Recover ₹25,000 plus Rs 999 of guaranteed savings", allowed); len(r) != 1 {
		t.Fatal(r)
	}
}

func containsAny(s string, candidates []string) bool {
	for _, candidate := range candidates {
		if strings.Contains(s, candidate) {
			return true
		}
	}
	return false
}

func TestBusinessQualityRejectsGenericAdvice(t *testing.T) {
	for _, c := range businessCases() {
		for _, answer := range []string{"", "Improve cash flow and reduce costs. Ask suppliers for discounts.", "Your business is healthy. You have guaranteed savings of Rs 999999."} {
			if len(businessQuality(c, answer)) == 0 {
				t.Fatalf("%s accepted generic/unsupported advice: %s", c.ID, answer)
			}
		}
	}
}
