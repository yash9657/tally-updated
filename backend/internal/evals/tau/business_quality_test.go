package tau_test

import (
	"bytes"
	"encoding/json"
	"github.com/khata/cored/internal/evals/tau"
	"github.com/shopspring/decimal"
	"regexp"
	"strings"
	"testing"
	"unicode"
)

// These checks establish necessary evidence/actionability, not customer
// satisfaction. Full answers and source facts are retained for human review.
func businessQuality(c businessCase, answer string) []string {
	return businessQualityWithDetails(c, answer, "")
}

func businessQualityWithDetails(c businessCase, summary, details string) []string {
	var reasons []string
	answer := strings.TrimSpace(summary + "\n" + details)
	lower := strings.ToLower(answer)
	reasons = append(reasons, businessLanguage(c.Language, answer)...)
	if len(strings.Fields(summary)) > 250 {
		reasons = append(reasons, "default brief exceeds 250 words")
	}
	if strings.TrimSpace(summary) == "" {
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
	normalized := strings.ReplaceAll(normalizeMoneyScript(answer), ",", "")
	for _, number := range c.Numbers {
		if !regexp.MustCompile(`(?:^|[^\d])` + regexp.QuoteMeta(number) + `(?:\.0+)?(?:[^\d]|$)`).MatchString(normalized) {
			reasons = append(reasons, "missing evidenced figure "+number)
		}
	}
	actions := append(append([]string{}, c.Actions...), "वापसी", "जाँच", "पुष्टि", "भुगतान", "वसूली", "कीमत", "भाव", "वापस", "jaanch", "pushti", "bhugtan", "vasooli", "wapas", "bhaav", "daam")
	if !containsAny(lower, actions) {
		reasons = append(reasons, "no relevant next action")
	}
	if !containsAny(lower, []string{"hi ", "dear ", "hello ", "checklist", "message:", "message to", "draft", "मसौदा", "संदेश", "नमस्ते", "जाँच सूची", "namaste", "sandesh"}) || len(strings.Fields(answer)) < 25 {
		reasons = append(reasons, "requested usable draft/checklist missing")
	}
	if !containsAny(lower, []string{"stock", "voucher", "invoice", "bill", "report", "purchase", "outstanding", "बिल", "वाउचर", "स्टॉक", "रिपोर्ट", "खरीद", "बकाया"}) {
		reasons = append(reasons, "source evidence missing")
	}
	if containsAny(lower, []string{"savings are guaranteed", "guaranteed to save", "will save ₹", "will save rs", "i have sent", "i sent the", "i deleted", "i have paid", "will realise less", "will realize less", "recovers the most", "without a margin hit", "cheapest exit", "भेज दिया", "भुगतान कर दिया", "bhej diya", "payment kar diya"}) {
		reasons = append(reasons, "unsupported guarantee or execution claim")
	}
	return reasons
}

// Necessary script/connective controls, not a fluency or translation oracle.
func businessLanguage(language, answer string) []string {
	letters, dev := 0, 0
	for _, r := range answer {
		if unicode.IsLetter(r) {
			letters++
			if r >= '\u0900' && r <= '\u097f' {
				dev++
			}
		}
	}
	if language == "hi" && (letters == 0 || dev*4 < letters) {
		return []string{"Hindi answer lacks Devanagari prose"}
	}
	if language == "hinglish" && (dev > 0 || !containsAny(strings.ToLower(answer), []string{" hai", " hain", " ke ", " ka ", " mein ", " aur ", " karo", "karein", "chahiye"})) {
		return []string{"Hinglish answer lacks Latin-script Hindi prose"}
	}
	// Three Hindi prose connectives catch a fully Hinglish answer to an English
	// question without classifying a single Indian name as a language choice.
	if language == "en" && (dev*2 > letters || len(regexp.MustCompile(`(?i)\b(?:hai|hain|karo|karein|chahiye|gaya|gayi|karna|nahi|kuch|aap|aapki|aapke|maine|ke|ki)\b`).FindAllString(answer, -1)) >= 3) {
		return []string{"English task answered in Hindi/Hinglish prose"}
	}
	return nil
}

func TestEnglishLanguageControl(t *testing.T) {
	if len(businessLanguage("en", "Samajh gaya, kuch bhi post nahi kiya. Aap kal confirm karein.")) == 0 {
		t.Fatal("wrong-language control accepted")
	}
	if r := businessLanguage("en", "Nothing was posted for Khan Auto Parts. I will wait for your confirmation."); len(r) != 0 {
		t.Fatal(r)
	}
	if r := businessLanguage("hinglish", "June ke report mein SAC 998313: taxable value ₹68,000 aur total ₹80,240."); len(r) != 0 {
		t.Fatal(r)
	}
	if r := businessLanguage("hinglish", "SAC 998313 (June 2025 GSTR-1 HSN summary): taxable value ₹68,000 aur total value ₹80,240."); len(r) != 0 {
		t.Fatal(r)
	}
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

// An independently calculated current ledger balance is evidence too. Accept
// a sum only within one named ledger, including its signed opening; do not
// whitelist arbitrary sums of figures or add overlapping ledger exposures.
func allowLedgerBalances(state tau.State, allowed map[string]bool) {
	balances := map[string]decimal.Decimal{}
	for _, l := range state.Ledgers {
		n, err := decimal.NewFromString(l.OpeningBalance)
		if err != nil {
			continue
		}
		if l.OpeningDC == "C" {
			n = n.Neg()
		}
		balances[l.Name] = n
	}
	for _, v := range state.Vouchers {
		for _, p := range v.Postings {
			n, err := decimal.NewFromString(p.Amount)
			if err != nil {
				continue
			}
			if p.DrCr == "C" {
				n = n.Neg()
			}
			balances[p.Ledger] = balances[p.Ledger].Add(n)
		}
	}
	for _, balance := range balances {
		allowed[balance.String()] = true
		allowed[balance.Abs().String()] = true
	}
}

func TestMoneyRecognizesIndependentLedgerTotals(t *testing.T) {
	s := tau.State{Ledgers: []tau.Ledger{{Name: "Supplier", OpeningBalance: "100", OpeningDC: "C"}}, Vouchers: []tau.Voucher{{Postings: []tau.Posting{{Ledger: "Supplier", DrCr: "C", Amount: "50"}, {Ledger: "Supplier", DrCr: "D", Amount: "20"}}}}}
	allowed := map[string]bool{}
	allowLedgerBalances(s, allowed)
	if !allowed["130"] || allowed["170"] || allowed["260"] {
		t.Fatal(allowed)
	}
	if len(unsupportedMoney("₹130 payable", allowed)) != 0 || len(unsupportedMoney("₹260 payable", allowed)) == 0 {
		t.Fatal("incorrect aggregate-money oracle")
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
	answer = normalizeMoneyScript(answer)
	answer = regexp.MustCompile(`([0-9][0-9,]*(?:\.[0-9]+)?)\s*(लाख|करोड़)?\s*(?:रुपये|रुपया|रु\.)`).ReplaceAllString(answer, "₹$1 $2")
	answer = strings.NewReplacer("लाख", "lakh", "करोड़", "crore").Replace(answer)
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

func normalizeMoneyScript(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '०' && r <= '९' {
			return '0' + r - '०'
		}
		return r
	}, s)
}

func TestBusinessMultilingualControls(t *testing.T) {
	c := businessCases()[0]
	c.Language = "hi"
	good := "Slow Shelving Kit के 100 पीस 200 दिन से नहीं बिके। स्टॉक रिपोर्ट में अंतिम दर पर ₹25,000 मूल्य है, वसूली की गारंटी नहीं। पहले सप्लायर से वापसी की शर्तें पूछें। संदेश: नमस्ते, कृपया इन सामानों की वापसी या क्रेडिट की शर्तें बताइए। कोई संदेश भेजा नहीं गया।"
	if r := businessQuality(c, good); len(r) != 0 {
		t.Fatal(r)
	}
	for _, bad := range []string{strings.ReplaceAll(good, "25,000", "24,000"), "Review your inventory and contact your supplier.", good + " भुगतान कर दिया", good + " A return recovers the most without a margin hit.", strings.Repeat(good, 10)} {
		if len(businessQuality(c, bad)) == 0 {
			t.Fatal("bad Hindi answer passed")
		}
	}
	if r := unsupportedMoney("२५,००० रुपये; ₹१ लाख; २ करोड़ रुपये", map[string]bool{"25000": true, "100000": true, "20000000": true}); len(r) != 0 {
		t.Fatal(r)
	}
	if r := unsupportedMoney("९९९ रुपये", map[string]bool{"25000": true}); len(r) == 0 {
		t.Fatal("unverified Hindi amount accepted")
	}
	if len(businessLanguage("hinglish", "Please review the invoices and contact the supplier.")) == 0 {
		t.Fatal("English passed as Hinglish")
	}
	if r := businessLanguage("hinglish", "Stock ka value ₹25,000 hai; supplier ke return terms confirm karo."); len(r) != 0 {
		t.Fatal(r)
	}
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
