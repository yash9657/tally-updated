package tau_test

// Business fixtures use ordinary book-entry APIs and the production detectors.
// Expected findings and grading criteria never enter an agent request.
import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/khata/cored/internal/evals/tau"
	"github.com/khata/cored/internal/insights"
	"github.com/khata/cored/internal/platform/db"
	"github.com/shopspring/decimal"
)

type businessCase struct {
	ID, Industry, Prompt, Finding, Entity string
	Numbers, Actions                      []string
	Steps                                 []tau.Step
	Language                              string
	VoucherCount                          int
}

const businessToday = "2026-03-31"

func businessCases() []businessCase {
	step := func(path string, body map[string]any) tau.Step {
		raw, _ := json.Marshal(body)
		return tau.Step{Method: "POST", Path: path, Body: raw}
	}
	ledger := func(name, group string) tau.Step {
		return step("/ledgers", map[string]any{"name": name, "group_name": group, "maintain_bill_by_bill": group == "Sundry Debtors", "created_by": "evaluation"})
	}
	item := func(name string) tau.Step {
		return step("/stock-items", map[string]any{"name": name, "unit_symbol": "Nos", "hsn": "7318", "gst_rate_pct": "0", "created_by": "evaluation"})
	}
	day := func(offset int) string {
		d, _ := time.Parse("2006-01-02", businessToday)
		return d.AddDate(0, 0, offset).Format("2006-01-02")
	}
	posting := func(name, dc, amount string) map[string]any {
		return map[string]any{"ledger_id": "{@ledger:" + name + "}", "dr_cr": dc, "amount": amount}
	}
	purchase := func(party, product, no, invoice, date, rate string) tau.Step {
		return step("/vouchers/purchase", map[string]any{"voucher_no": no, "voucher_date": date,
			"party_ledger_id": "{@ledger:" + party + "}", "item_ledger_id": "{@ledger:Purchases}", "inward": map[string]any{"supplier_invoice_no": invoice, "supplier_invoice_date": date},
			"postings":  []any{posting("Purchases", "D", decimal.RequireFromString(rate).Mul(decimal.NewFromInt(100)).String()), posting(party, "C", decimal.RequireFromString(rate).Mul(decimal.NewFromInt(100)).String())},
			"inventory": []any{map[string]any{"stock_item_id": "{@item:" + product + "}", "direction": "IN", "qty": "100", "rate": rate}}})
	}
	common := func() []tau.Step {
		return []tau.Step{ledger("Purchases", "Purchase Accounts"), ledger("Service Revenue", "Sales Accounts"), step("/ledgers", map[string]any{"name": "Operating Bank", "group_name": "Bank Accounts", "opening_balance": "1000000", "opening_dc": "D"})}
	}
	retail := common()
	retail = append(retail, ledger("Metro Supplies", "Sundry Creditors"), item("Slow Shelving Kit"), purchase("Metro Supplies", "Slow Shelving Kit", "RETAIL-OLD", "MS-17", day(-200), "250"))
	wholesale := common()
	wholesale = append(wholesale, ledger("Kaveri Components", "Sundry Creditors"), item("Standard Bolt"), purchase("Kaveri Components", "Standard Bolt", "WHOLE-1", "KC/77", day(-10), "120"), purchase("Kaveri Components", "Standard Bolt", "WHOLE-2", "kc-77", day(-9), "120"))
	manufacturing := common()
	manufacturing = append(manufacturing, ledger("Deccan Metals", "Sundry Creditors"), item("Steel Blank"))
	for i, rate := range []string{"50", "55", "60"} {
		manufacturing = append(manufacturing, purchase("Deccan Metals", "Steel Blank", fmt.Sprintf("MFG-%d", i+1), fmt.Sprintf("DM-%d", i+1), day(-90+i*40), rate))
	}
	services := common()
	services = append(services, ledger("Lotus Design Client", "Sundry Debtors"))
	for i := 0; i < 6; i++ {
		no := fmt.Sprintf("SVC-%d", i+1)
		services = append(services, step("/vouchers/sales", map[string]any{"voucher_no": no, "voucher_date": day(-220 + 30*i), "party_ledger_id": "{@ledger:Lotus Design Client}",
			"postings":         []any{posting("Lotus Design Client", "D", "10000"), posting("Service Revenue", "C", "10000")},
			"bill_allocations": []any{map[string]any{"ledger_id": "{@ledger:Lotus Design Client}", "ref_type": "NEW", "ref_name": no, "dr_cr": "D", "amount": "10000"}}}))
		if i < 4 {
			services = append(services, step("/vouchers/receipt", map[string]any{"voucher_date": day(-200 + 30*i),
				"postings":         []any{posting("Operating Bank", "D", "10000"), posting("Lotus Design Client", "C", "10000")},
				"bill_allocations": []any{map[string]any{"ledger_id": "{@ledger:Lotus Design Client}", "ref_type": "AGAINST", "ref_name": no, "dr_cr": "C", "amount": "10000"}}}))
		}
	}
	cases := []businessCase{
		{ID: "retail-stock", Industry: "retail", Prompt: "I run a retail shop. Look at my books, identify the most useful action I should take today, and draft a short message I can use to act on it. Explain the evidence and any uncertainty. Do not change the books or send anything.", Finding: insights.TypeDeadStock, Entity: "Slow Shelving Kit", Numbers: []string{"25000", "100", "200"}, Actions: []string{"return", "supplier", "discount"}, Steps: retail},
		{ID: "wholesale-duplicate", Industry: "wholesale", Prompt: "I run a wholesale business. Before I pay suppliers, find the most important issue in my books and prepare a useful message or review checklist to resolve it. Show the evidence; do not change anything or send anything.", Finding: insights.TypeDuplicateBill, Entity: "Kaveri Components", Numbers: []string{"12000"}, Actions: []string{"compare", "confirm", "verify", "duplicate"}, Steps: wholesale},
		{ID: "service-collections", Industry: "services", Prompt: "I run a service agency. What deserves my attention today based on my books? Prioritize the most useful action and draft the message I should use, with the relevant figures and evidence. Do not change the books or send it.", Finding: insights.TypeCustomerSlower, Entity: "Lotus Design Client", Numbers: []string{"20000", "20"}, Actions: []string{"payment", "collect", "outstanding"}, Steps: services},
		{ID: "manufacturing-cost", Industry: "manufacturing", Prompt: "I run a manufacturing business. Find a purchasing or working-capital issue worth acting on from my books. Give me a specific next step and draft a supplier message. Distinguish observed costs from estimates; do not change anything or send it.", Finding: insights.TypeSupplierPriceCreep, Entity: "Deccan Metals", Numbers: []string{"50", "60", "20"}, Actions: []string{"quote", "negotiate", "pricing", "price"}, Steps: manufacturing},
		{ID: "new-business-control", Industry: "new business", Prompt: "I have just onboarded my business. What specific opportunities can you prove from my books? Tell me if the information is insufficient and what I should provide first. Do not invent savings or change anything.", Steps: nil},
	}
	// Holdout books change parties, items and figures without changing the task.
	// This catches answers that merely repeat a stock example for an industry.
	variants := [][]string{
		{"Metro Supplies", "Harbor Supplies", "Slow Shelving Kit", "Spare Display Rack", "250", "370", "25000", "37000"},
		{"Kaveri Components", "Narmada Parts", "Standard Bolt", "Machine Pin", "120", "185", "12000", "18500"},
		{"Lotus Design Client", "Orchid Consulting Client", "10000", "17500", "20000", "35000"},
		{"Deccan Metals", "Coastal Metals", "Steel Blank", "Aluminium Blank", "50", "80", "55", "88", "60", "96"},
	}
	for i, pairs := range variants {
		original := cases[i]
		v := original
		v.ID += "-holdout"
		replace := strings.NewReplacer(pairs...)
		v.Entity = replace.Replace(original.Entity)
		v.Numbers = append([]string(nil), original.Numbers...)
		for n, value := range v.Numbers {
			v.Numbers[n] = replace.Replace(value)
		}
		v.Steps = append([]tau.Step(nil), original.Steps...)
		for n, st := range v.Steps {
			v.Steps[n].Body = json.RawMessage(replace.Replace(string(st.Body)))
		}
		cases = append(cases, v)
	}
	return cases

}

func seedBusiness(t *testing.T, r *rig, c businessCase) (tau.Books, []insights.Insight) {
	t.Helper()
	ctx := context.Background()
	books, err := r.factory(t)(ctx, map[string]any{"fy_start_date": "2024-04-01", "state_code": "27"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tau.NewExecutor(books).Run(ctx, c.Steps); err != nil {
		t.Fatal(err)
	}
	_, app := dsns(t)
	pool, err := db.New(ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	cid := uuid.MustParse(strings.TrimPrefix(books.(tau.HandlerBooks).Base, "/v1/companies/"))
	ctx = db.WithCompany(ctx, cid)
	date, _ := time.Parse("2006-01-02", businessToday)
	if _, err := insights.ProcessEvents(ctx, pool, cid, date, 500); err != nil {
		t.Fatal(err)
	}
	stats, err := insights.RunNightly(ctx, pool, cid, date)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats.EvidenceRefused) != 0 {
		t.Fatalf("evidence refused: %v", stats.EvidenceRefused)
	}
	rows, err := insights.List(ctx, pool, insights.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	found := c.Finding == ""
	for _, row := range rows {
		if row.Type == c.Finding {
			found = true
			if err := insights.VerifyStored(ctx, pool, row.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !found {
		t.Fatalf("fixture %s did not produce %s: %v", c.ID, c.Finding, rows)
	}
	return books, rows
}

func TestBusinessInsightFixturesHaveIndependentEvidence(t *testing.T) {
	r := newRig(t)
	for _, c := range businessCases() {
		t.Run(c.ID, func(t *testing.T) { seedBusiness(t, r, c) })
	}
}
