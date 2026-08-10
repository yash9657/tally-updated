package server

// handler_forecast_disclosure_test.go pins the CASH-FLOW WIRE SHAPE's disclosure.
//
// ⛔⛔ THE DEFECT, MEASURED ON THE WIRE. The response carried exactly seven keys —
// as_on, horizon_days, bucket_days, opening_cash, buckets, beyond_horizon,
// overdue_folded_into_first_bucket — and NOT ONE of them contained the substrings
// "projection", "estimate", "basis", "assumed" or "method". Every money value in
// it is a fixed-4 string and is rendered exactly like a posted ledger figure,
// while the engine invents a settlement date for every unpaid bill (due date, else
// the party's credit_days, else a 30-day constant compiled into
// internal/forecast). An operator cannot tell a guess from a balance.
//
// This test is in `package server` (not server_test) and drives cashFlowJSON
// DIRECTLY: the disclosure is a property of the RENDERER, and asserting it through
// an HTTP round trip would make it depend on a database being up. The routed
// behaviour is covered by handler_forecast_test.go.
//
// ⚠️ It asserts the substrings the audit measured, not prose. A future rewording
// of the sentences must keep the words a reader (and a grep) looks for.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/forecast"
)

func testProjection(t *testing.T) forecast.Projection {
	t.Helper()
	asOn, err := time.Parse("2006-01-02", "2025-05-01")
	if err != nil {
		t.Fatal(err)
	}
	due, _ := time.Parse("2006-01-02", "2025-05-10")
	credit := 15
	bills := []forecast.ProjBill{
		{Kind: forecast.Inflow, Pending: decimal.RequireFromString("1000"), DueDate: &due, VoucherDate: asOn},
		{Kind: forecast.Inflow, Pending: decimal.RequireFromString("500"), VoucherDate: asOn, CreditDays: &credit},
		// No due date and no credit days: placed by the 30-day CONVENTION.
		{Kind: forecast.Outflow, Pending: decimal.RequireFromString("700"), VoucherDate: asOn},
	}
	p, err := forecast.Project(bills, decimal.RequireFromString("250"), asOn, 60, 7)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	return p
}

func TestCashFlowResponseSaysItIsAnEstimate(t *testing.T) {
	out := cashFlowJSON(testProjection(t))

	est, ok := out["estimate"].(bool)
	if !ok || !est {
		t.Fatalf("estimate = %v (%T), want true — a projected number that does not "+
			"say it is projected is indistinguishable from a ledger balance", out["estimate"], out["estimate"])
	}
	method, _ := out["method"].(string)
	if method == "" {
		t.Fatal("method is empty — the response must name what produced it")
	}
	if _, ok := out["basis"].(map[string]any); !ok {
		t.Fatalf("basis missing or wrong type (%T)", out["basis"])
	}
}

func TestCashFlowResponseNamesTheINVENTEDSettlementDates(t *testing.T) {
	out := cashFlowJSON(testProjection(t))
	basis := out["basis"].(map[string]any)
	assumed, ok := basis["assumed_settlement_date"].(map[string]any)
	if !ok {
		t.Fatalf("basis.assumed_settlement_date missing (%T)", basis["assumed_settlement_date"])
	}

	// The three rules, each with its own bill count and its own SIDES (RULE 4 — a
	// single total would hide which side of the books is the guess).
	for _, key := range []string{"from_due_date", "from_party_credit_days", "from_default_credit_days"} {
		row, ok := assumed[key].(map[string]any)
		if !ok {
			t.Fatalf("basis.assumed_settlement_date.%s missing (%T)", key, assumed[key])
		}
		if _, ok := row["bills"].(int); !ok {
			t.Errorf("%s.bills is %T, want int", key, row["bills"])
		}
		for _, side := range []string{"inflow", "outflow"} {
			s, ok := row[side].(string)
			if !ok {
				t.Fatalf("%s.%s is %T, want a fixed-4 STRING — money is never a float here", key, side, row[side])
			}
			if len(s) < 6 || s[len(s)-5] != '.' {
				t.Errorf("%s.%s = %q, want fixed-4", key, side, s)
			}
		}
	}

	// The bill placed by the 30-day convention is reported as such, with its money.
	conv := assumed["from_default_credit_days"].(map[string]any)
	if conv["bills"].(int) != 1 || conv["outflow"].(string) != "700.0000" {
		t.Errorf("from_default_credit_days = %+v, want 1 bill / 700.0000 outflow", conv)
	}
	// ...and the constant itself is PUBLISHED rather than left implicit.
	if days, ok := assumed["default_credit_days"].(int); !ok || days != 30 {
		t.Errorf("default_credit_days = %v, want 30", assumed["default_credit_days"])
	}

	// And what the projection does NOT model is stated, because an operator who
	// believes a cash-flow projection includes salaries and rent will plan on it.
	notes, ok := basis["not_modelled"].([]string)
	if !ok || len(notes) == 0 {
		t.Fatalf("basis.not_modelled missing (%T)", basis["not_modelled"])
	}
}

func TestTheDisclosureWORDSAreOnTheWire(t *testing.T) {
	// The audit's own measurement, inverted: it grepped the serialised response
	// for projection/estimate/basis/assumed/method and found none of them. This
	// asserts the same grep now succeeds, so a reword that drops the vocabulary
	// fails here rather than quietly re-opening the defect.
	raw, err := json.Marshal(cashFlowJSON(testProjection(t)))
	if err != nil {
		t.Fatal(err)
	}
	body := strings.ToLower(string(raw))
	for _, word := range []string{"estimate", "basis", "assumed", "method", "project"} {
		if !strings.Contains(body, word) {
			t.Errorf("the serialised cash-flow response contains no %q — this is the "+
				"exact measurement the audit made when it found seven keys and no "+
				"disclosure", word)
		}
	}
}

func TestTheProjectionsMoneyIsStillFixed4Strings(t *testing.T) {
	// THE CONTROL for the change above: adding a disclosure must not have turned
	// any money value into a number. ⛔ A float in a money path is a defect even
	// when the number looks right.
	out := cashFlowJSON(testProjection(t))
	if _, ok := out["opening_cash"].(string); !ok {
		t.Fatalf("opening_cash is %T, want string", out["opening_cash"])
	}
	buckets, ok := out["buckets"].([]map[string]any)
	if !ok || len(buckets) == 0 {
		t.Fatalf("buckets missing (%T)", out["buckets"])
	}
	for _, b := range buckets {
		for _, k := range []string{"inflow", "outflow", "nett", "opening_cash", "closing_cash"} {
			if _, ok := b[k].(string); !ok {
				t.Fatalf("bucket %s is %T, want a fixed-4 string", k, b[k])
			}
		}
	}
	// Hand-derived so this is not vacuous: 7-day buckets from 2025-05-01, so the
	// receivable due 05-10 is day 9 -> bucket 1, the one at voucher+15 = 05-16 is
	// day 15 -> bucket 2, and the payable at voucher+30 = 05-31 is day 30 ->
	// bucket 4.
	if got := buckets[1]["inflow"].(string); got != "1000.0000" {
		t.Errorf("bucket[1].inflow = %q, want 1000.0000", got)
	}
	if got := buckets[2]["inflow"].(string); got != "500.0000" {
		t.Errorf("bucket[2].inflow = %q, want 500.0000", got)
	}
	if got := buckets[4]["outflow"].(string); got != "700.0000" {
		t.Errorf("bucket[4].outflow = %q, want 700.0000", got)
	}
}
