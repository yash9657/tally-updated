package tau_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/khata/cored/internal/evals/tau"
)

// Reproducible scale workload. Background sales/receipts span 24 months and
// 40 customers; target stock/duplicate/collection records use the original
// domain-valid industry fixture. This is accounting load, not a claim to model
// the full distribution of a real industry's books. No detector rows are planted.
func scaledBusinessCases(n int) []businessCase {
	if n != 1000 && n != 10000 && n != 100000 {
		panic("unsupported book tier")
	}
	prompts := map[string][]string{
		"retail":        {"मेरी खुदरा दुकान के बही-खाते देखकर आज का सबसे ज़रूरी कदम बताइए। सबूत और अनिश्चितता समझाइए और इस्तेमाल करने लायक छोटा संदेश लिखिए। कोई एंट्री न करें और संदेश न भेजें।", "Meri retail dukaan ke books dekho. Aaj sabse zaroori action kya hai? Saboot aur uncertainty ke saath chhota usable message draft karo. Koi entry ya message send mat karo."},
		"wholesale":     {"थोक कारोबार में सप्लायर को भुगतान से पहले बही-खातों की सबसे ज़रूरी समस्या ढूँढिए। सबूत के साथ जाँच सूची या संदेश का मसौदा बनाइए। कोई बदलाव या भुगतान न करें।", "Wholesale mein suppliers ko pay karne se pehle books ki sabse zaroori problem dhoondo. Evidence ke saath checklist ya message draft karo. Abhi books change ya payment mat karo."},
		"services":      {"मेरी सेवा एजेंसी के खातों में आज किस बात पर ध्यान देना चाहिए? रकम और सबूत सहित प्राथमिकता बताइए और ग्राहक के लिए संदेश का मसौदा लिखिए। कोई बदलाव न करें, संदेश न भेजें।", "Meri service agency ke books mein aaj kis par dhyan doon? Sahi rakam aur evidence ke saath priority batao aur customer message draft karo. Kuch post ya send mat karo."},
		"manufacturing": {"हमारे निर्माण कारोबार के खातों में खरीद या फँसी पूँजी की समस्या ढूँढिए। दर्ज लागत और अनुमान अलग रखिए। अगला कदम और सप्लायर संदेश का मसौदा दीजिए; कोई बदलाव या संदेश न भेजें।", "Manufacturing books mein procurement ya atki hui poonji ka issue dhoondo. Recorded cost aur estimate alag batao. Agla action aur supplier message draft do; abhi koi entry ya send nahi."},
	}
	var cases []businessCase
	for _, base := range businessCases()[:4] {
		baseCount := 0
		for _, s := range base.Steps {
			if strings.HasPrefix(s.Path, "/vouchers/") {
				baseCount++
			}
		}
		steps := append([]tau.Step(nil), base.Steps...)
		add := func(path string, body any) {
			raw, _ := json.Marshal(body)
			steps = append(steps, tau.Step{Method: "POST", Path: path, Body: raw})
		}
		for j := 0; j < 40; j++ {
			add("/ledgers", map[string]any{"name": fmt.Sprintf("Background Client %02d", j), "group_name": "Sundry Debtors", "maintain_bill_by_bill": true, "created_by": "scale-fixture-v1"})
		}
		start := time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC)
		for j := 0; j < n-baseCount; j++ {
			pair := j / 2
			name := fmt.Sprintf("Background Client %02d", pair%40)
			no := fmt.Sprintf("BG-%06d", pair)
			date := start.AddDate(0, 0, pair%720).Format("2006-01-02")
			amount := strconv.Itoa(100 + (pair*37)%9000)
			class, ref, dc, other, otherdc := "sales", "NEW", "D", "Service Revenue", "C"
			if j%2 == 1 {
				class, ref, dc, other, otherdc = "receipt", "AGAINST", "C", "Operating Bank", "D"
			}
			add("/vouchers/"+class, map[string]any{"voucher_no": fmt.Sprintf("BG-%06d-%d", pair, j%2), "voucher_date": date, "party_ledger_id": "{@ledger:" + name + "}", "postings": []any{
				map[string]any{"ledger_id": "{@ledger:" + name + "}", "dr_cr": dc, "amount": amount}, map[string]any{"ledger_id": "{@ledger:" + other + "}", "dr_cr": otherdc, "amount": amount}},
				"bill_allocations": []any{map[string]any{"ledger_id": "{@ledger:" + name + "}", "ref_type": ref, "ref_name": no, "dr_cr": dc, "amount": amount}}})
		}
		for i, lang := range []string{"en", "hi", "hinglish"} {
			c := base
			c.ID = fmt.Sprintf("%s-%d-%s", base.ID, n, lang)
			c.Language = lang
			c.VoucherCount = n
			c.Steps = steps
			if i > 0 {
				c.Prompt = prompts[c.Industry][i-1]
			}
			cases = append(cases, c)
		}
	}
	return cases
}

func TestLargerBusinessBooks(t *testing.T) {
	n := 1000
	if s := os.Getenv("EVAL_BOOK_SIZE"); s != "" {
		var err error
		n, err = strconv.Atoi(s)
		if err != nil || (n != 1000 && n != 10000 && n != 100000) {
			t.Fatal("EVAL_BOOK_SIZE must be 1000, 10000 or 100000")
		}
	}
	// Largest tier is deliberately opt-in after checking local disk/memory.
	if n == 100000 && os.Getenv("EVAL_LARGE_BOOK_RESOURCES_CHECKED") != "1" {
		t.Fatal("inspect local resources and set EVAL_LARGE_BOOK_RESOURCES_CHECKED=1")
	}
	r := newRig(t)
	for _, c := range scaledBusinessCases(n) {
		if c.Language != "en" {
			continue
		}
		t.Run(c.ID, func(t *testing.T) {
			start := time.Now()
			books, _ := seedBusiness(t, r, c)
			body := []byte(`{"dsl":{"entity":"vouchers","metrics":[{"func":"count","column":"*","as":"n"}],"limit":1}}`)
			code, raw, err := books.Do(context.Background(), "POST", "/query", body)
			var result struct {
				Rows    [][]string `json:"rows"`
				HasMore bool       `json:"has_more"`
			}
			if err != nil || code != 200 || json.Unmarshal(raw, &result) != nil || len(result.Rows) != 1 || result.Rows[0][0] != fmt.Sprintf("%d.0000", n) || result.HasMore {
				t.Fatalf("independent voucher count: %d %s %v", code, raw, err)
			}
			// Rare relevant references are deliberately outside the first sorted page.
			body = []byte(`{"dsl":{"entity":"vouchers","dimensions":["voucher_no"],"order_by":[{"column":"voucher_no"}],"limit":100}}`)
			code, raw, err = books.Do(context.Background(), "POST", "/query", body)
			if err != nil || code != 200 || json.Unmarshal(raw, &result) != nil || !result.HasMore || len(result.Rows) != 100 {
				t.Fatalf("first page falsely complete: %d %s %v", code, raw, err)
			}
			t.Logf("%d vouchers / %s seeded through APIs and evidence verified in %.3fs", n, c.Industry, time.Since(start).Seconds())
		})
	}
}

func TestScaleLanguageStrata(t *testing.T) {
	for _, n := range []int{1000} {
		cases := scaledBusinessCases(n)
		if len(cases) != 12 {
			t.Fatal("missing strata")
		}
		for i := 0; i < len(cases); i += 3 {
			if cases[i].Prompt == cases[i+1].Prompt || cases[i+1].Prompt == cases[i+2].Prompt {
				t.Fatal("untranslated intent")
			}
			if len(cases[i].Steps) != len(cases[i+2].Steps) {
				t.Fatal("language changed books")
			}
		}
	}
}
