//go:build livefireworks

package tau_test

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/khata/cored/internal/agent"
	"github.com/khata/cored/internal/evals/tau"
	"github.com/khata/cored/internal/insights"
	"github.com/shopspring/decimal"
)

func runBusinessLive(t *testing.T, r *rig, spend *spendTransport, ledger *spendLedger) {
	ctx := context.Background()
	start := time.Now()
	var results []map[string]any
	passed := 0
	a := liveAgent{rig: r, spend: spend, approve: map[string]bool{}, logf: t.Logf}
	cases := businessCases()
	if os.Getenv("EVAL_SUITE") == "expanded" {
		n, err := strconv.Atoi(os.Getenv("EVAL_BOOK_SIZE"))
		if err != nil || (n != 1000 && n != 10000 && n != 100000) {
			t.Fatal("expanded suite needs EVAL_BOOK_SIZE 1000, 10000 or 100000")
		}
		if n == 100000 && os.Getenv("EVAL_LARGE_BOOK_RESOURCES_CHECKED") != "1" {
			t.Fatal("100000 tier requires resource check")
		}
		cases = scaledBusinessCases(n)
	}
	if ids := os.Getenv("EVAL_TASK_IDS"); ids != "" {
		wanted := map[string]bool{}
		for _, id := range strings.Split(ids, ",") {
			wanted[id] = true
		}
		var selected []businessCase
		for _, c := range cases {
			if wanted[c.ID] {
				selected = append(selected, c)
				delete(wanted, c.ID)
			}
		}
		if len(wanted) != 0 || len(selected) == 0 {
			t.Fatalf("unknown business case selection: %v", wanted)
		}
		cases = selected
	}
	for _, c := range cases {
		if reason := spend.providerStopReason(); reason != "" {
			results = append(results, map[string]any{"id": c.ID, "industry": c.Industry, "language": c.Language, "voucher_count": c.VoucherCount, "pass": false, "not_attempted": true, "reasons": []string{reason}, "provider_calls": 0})
			continue
		}
		books, findings := seedBusiness(t, r, c)
		before, err := tau.Snapshot(ctx, books)
		if err != nil {
			t.Fatal(err)
		}
		callStart := spend.calls
		caseStart := time.Now()
		language := c.Language
		if language == "" {
			language = "en"
		}
		answer, runErr := a.Run(ctx, tau.Env{Books: books}, tau.Prompt{TaskID: c.ID, Language: language, Today: businessToday, Utterance: c.Prompt})
		reasons := businessQualityWithDetails(c, answer.Answer, answer.Details)
		allowed := map[string]bool{"0": true}
		allow := func(value string) {
			if n, err := decimal.NewFromString(value); err == nil {
				allowed[n.String()] = true
			}
		}
		for _, finding := range findings {
			for _, field := range finding.Fields {
				if field.Unit == insights.UnitINR {
					allow(field.Value)
				}
			}
		}
		for _, ledger := range before.Ledgers {
			allow(ledger.OpeningBalance)
		}
		for _, voucher := range before.Vouchers {
			for _, posting := range voucher.Postings {
				allow(posting.Amount)
			}
			for _, item := range voucher.Inventory {
				allow(item.Rate)
				allow(item.Amount)
			}
		}
		allowReadResultNumbers(spend.records[callStart:], allowed)
		allowLedgerBalances(before, allowed)
		reasons = append(reasons, unsupportedMoney(answer.Answer+"\n"+answer.Details, allowed)...)
		if runErr != nil {
			reasons = append(reasons, runErr.Error())
		}
		after, err := tau.Snapshot(ctx, books)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, after) {
			reasons = append(reasons, "advisory task changed the books")
		}
		if len(reasons) == 0 {
			passed++
		}
		var evidence []map[string]any
		for _, finding := range findings {
			evidence = append(evidence, map[string]any{"type": finding.Type, "facts": finding.Payload, "as_of": finding.AsOf})
		}
		results = append(results, map[string]any{"id": c.ID, "industry": c.Industry, "language": language, "voucher_count": c.VoucherCount, "fixture_version": "scale-fixture-v1", "prompt": c.Prompt, "answer": answer.Answer, "details": answer.Details, "pass": len(reasons) == 0, "reasons": reasons, "evidence": evidence, "elapsed_seconds": time.Since(caseStart).Seconds(), "provider_calls": spend.calls - callStart})
	}
	result := map[string]any{"synthetic": true, "suite": "business_insights", "agent_prompt_version": agent.PromptVersion, "source_revision": os.Getenv("EVAL_SOURCE_REVISION"), "source_sha256": os.Getenv("EVAL_SOURCE_SHA256"), "model": os.Getenv("FIREWORKS_MODEL"), "started_at": start.UTC(), "elapsed_seconds": time.Since(start).Seconds(), "calls": spend.records, "results": results, "passed": passed, "total": len(results), "shared_budget_charged_usd": ledger.Charged.String(), "token_price_per_million_usd": ledger.Rate.String(), "max_usd": ledger.Cap.String(), "reserved_usd": spend.reserved.String()}
	raw, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("EVAL_REPORT"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("business usefulness necessary checks: %d/%d; shared conservative charge $%s", passed, len(results), ledger.Charged)
	if passed != len(results) {
		t.Errorf("business evaluation failures; inspect full answers and reasons in %s", os.Getenv("EVAL_REPORT"))
	}
}
