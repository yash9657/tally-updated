package tau

// Unit tests for the pieces of the harness that need no database: the
// selector, the answer reader, the judge, the pass^k estimator, the loader and
// the WrongAgent's perturbation. The end-to-end proof is tau_db_test.go.

import (
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

func TestSelect(t *testing.T) {
	doc := []byte(`{"sup_details":{"osup_det":{"txval":"50000","iamt":0}},
		"rows":[{"ledger":"Acme Pvt. Ltd.","debit":"12.5000"},{"ledger":"Cash","debit":"1"}],
		"dup":[{"k":"a"},{"k":"a"}], "b2cs":[{"camt":"4500"}]}`)
	for sel, want := range map[string]string{
		"sup_details.osup_det.txval":        "50000",
		"sup_details.osup_det.iamt":         "0",
		"rows[ledger=Acme Pvt. Ltd.].debit": "12.5000",
		"b2cs[0].camt":                      "4500",
	} {
		got, err := Select(doc, sel)
		if err != nil || got != want {
			t.Errorf("Select(%q) = %q, %v; want %q", sel, got, err, want)
		}
	}
	// Each of these must be an ERROR, never "": a selector that silently
	// returned nothing would let an answer template render "taxable " and the
	// judge would then report a missing figure for the wrong reason.
	for _, sel := range []string{"dup[k=a].k", "rows[ledger=Nobody].debit", "rows", "b2cs[3].camt", "nokey"} {
		if got, err := Select(doc, sel); err == nil {
			t.Errorf("Select(%q) = %q, want an error", sel, got)
		}
	}
}

func TestAnswerFigures(t *testing.T) {
	cases := map[string][]string{
		"Posted ₹5,90,000.50 to Acme": {"590000.5"},
		"Sales stand at 1.18 lakh":    {"118000"},
		"HDFC Bank: 59,000.0000 Cr":   {"59000"}, // Cr is a side, not a crore
		"कुल ₹५९,००० बकाया":           {"59000"},
		"2 crore":       {"20000000"},
		"on 2025-06-12": nil,
	}
	for in, want := range cases {
		got := AnswerFigures(in)
		var gs []string
		for _, g := range got {
			gs = append(gs, g.String())
		}
		if strings.Join(gs, ",") != strings.Join(want, ",") {
			t.Errorf("AnswerFigures(%q) = %v, want %v", in, gs, want)
		}
	}
}

func baseState() State {
	return State{
		Ledgers:    []Ledger{{Name: "Cash", Group: "Cash-in-Hand", OpeningBalance: "0"}},
		StockItems: nil,
	}
}

func saleVoucher(amt string) Voucher {
	return Voucher{Class: "sales", Date: "2025-06-12", Postings: []Posting{
		{Ledger: "Acme", DrCr: "D", Amount: amt}, {Ledger: "Sales", DrCr: "C", Amount: amt}}}
}

func TestJudge(t *testing.T) {
	defaults := baseState()
	exp := Expected{
		Ledgers:  []Ledger{{Name: "Acme", Group: "Sundry Debtors"}, {Name: "Sales", Group: "Sales Accounts"}},
		Vouchers: []Voucher{saleVoucher("1000")},
	}
	good := State{
		Ledgers:  append(append([]Ledger{}, defaults.Ledgers...), exp.Ledgers...),
		Vouchers: []Voucher{saleVoucher("1000.0000")},
	}
	acknowledgement := "Posted ₹1000.00, including ₹847.46 base and ₹152.54 tax."
	if v := Judge(exp, defaults, good, acknowledgement); !v.Pass {
		t.Fatalf("identical books judged failing: %v", v.Reasons)
	}

	mut := func(f func(*State)) State {
		b, _ := json.Marshal(good)
		var s State
		_ = json.Unmarshal(b, &s)
		f(&s)
		return s
	}
	for name, st := range map[string]State{
		"amount off by a paisa": mut(func(s *State) { s.Vouchers[0] = saleVoucher("1000.01") }),
		"an extra voucher":      mut(func(s *State) { s.Vouchers = append(s.Vouchers, saleVoucher("5")) }),
		"a missing voucher":     mut(func(s *State) { s.Vouchers = nil }),
		"a duplicate voucher":   mut(func(s *State) { s.Vouchers = append(s.Vouchers, s.Vouchers[0]) }),
		"a date off by a day":   mut(func(s *State) { s.Vouchers[0].Date = "2025-06-13" }),
		"a renamed default":     mut(func(s *State) { s.Ledgers[0].Name = "Cash A/c" }),
		"a default's opening":   mut(func(s *State) { s.Ledgers[0].OpeningBalance = "5" }),
		"an extra master":       mut(func(s *State) { s.StockItems = []StockItem{{Name: "X"}} }),
		"a bank date set":       mut(func(s *State) { s.Vouchers[0].Postings[0].BankDate = "2025-06-13" }),
		"a side swapped":        mut(func(s *State) { s.Vouchers[0].Postings[0].DrCr = "C"; s.Vouchers[0].Postings[1].DrCr = "D" }),
	} {
		if v := Judge(exp, defaults, st, acknowledgement); v.Pass {
			t.Errorf("%s: judged passing", name)
		}
	}

	// A restated default replaces the born-with one — and only that one.
	restated := exp
	restated.Ledgers = append([]Ledger{{Name: "Cash", Group: "Cash-in-Hand", OpeningBalance: "100", OpeningDC: "D"}}, exp.Ledgers...)
	withCash := mut(func(s *State) { s.Ledgers[0].OpeningBalance, s.Ledgers[0].OpeningDC = "100.0000", "D" })
	if v := Judge(restated, defaults, withCash, ""); !v.Pass {
		t.Errorf("restated default judged failing: %v", v.Reasons)
	}
	if v := Judge(restated, defaults, good, ""); v.Pass {
		t.Errorf("restated default not applied, yet judged passing")
	}

	// Outputs.
	q := Expected{Ledgers: exp.Ledgers, Vouchers: exp.Vouchers, Outputs: []string{"1180.00", "90.50"}}
	if v := Judge(q, defaults, good, "total ₹1,180 of which tax 90.50"); !v.Pass {
		t.Errorf("answer stating both figures judged failing: %v", v.Reasons)
	}
	if v := Judge(q, defaults, good, "total ₹1,180"); v.Pass {
		t.Errorf("answer missing a figure judged passing")
	}
	if v := Judge(q, defaults, good, "total ₹1,181 of which tax 90.50"); v.Pass {
		t.Errorf("answer off by a rupee judged passing")
	}
}

func TestPassHatK(t *testing.T) {
	rep := Report{K: 5, Tasks: []TaskResult{
		{Trials: make([]TrialResult, 5), Successes: 5},
		{Trials: make([]TrialResult, 5), Successes: 3},
		{Trials: make([]TrialResult, 5), Successes: 0},
	}}
	// pass^k = mean over tasks of C(c,k)/C(5,k).
	want := map[int]*big.Rat{
		1: big.NewRat(8, 15),  // (1 + 3/5 + 0)/3
		2: big.NewRat(13, 30), // (1 + 3/10)/3
		3: big.NewRat(11, 30), // (1 + 1/10)/3
		5: big.NewRat(1, 3),
	}
	for k, w := range want {
		wf, _ := w.Float64()
		if got := rep.PassHatK(k); got != wf {
			t.Errorf("pass^%d = %v, want %v", k, got, wf)
		}
	}
	if rep.PassHatK(6) != 0 || rep.PassHatK(0) != 0 {
		t.Errorf("k outside 1..K must be 0")
	}
	if rep.Passed() != 1 {
		t.Errorf("Passed() = %d, want 1", rep.Passed())
	}
}

func suiteDir(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for i := 0; i < 8; i++ {
		c := filepath.Join(dir, "corpora", "tau")
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			return c
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not locate corpora/tau")
	return ""
}

func TestLoadDirRefusesEmptyAndUnlabelled(t *testing.T) {
	if _, err := LoadDir(t.TempDir()); err == nil {
		t.Fatal("an empty suite directory loaded without error")
	}
	dir := t.TempDir()
	raw, _ := os.ReadFile(filepath.Join(suiteDir(t), "ve-sale-000-en.json"))
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	m["synthetic"] = false
	b, _ := json.Marshal(m)
	_ = os.WriteFile(filepath.Join(dir, "x.json"), b, 0o644)
	if _, err := LoadDir(dir); err == nil || !strings.Contains(err.Error(), "SYNTHETIC") {
		t.Fatalf("a fixture not labelled synthetic loaded: %v", err)
	}
}

// TestSuiteShape pins the suite's composition. The DB test pins the counts the
// AGENTS produce; this pins what the fixtures ARE.
func TestSuiteShape(t *testing.T) {
	tasks, err := LoadDir(suiteDir(t))
	if err != nil {
		t.Fatal(err)
	}
	fam := map[string]int{}
	lang := map[string]int{}
	for _, tk := range tasks {
		fam[tk.Family]++
		lang[tk.Language]++
		hasDeva := strings.IndexFunc(tk.Utterance, func(r rune) bool { return unicode.In(r, unicode.Devanagari) }) >= 0
		if (tk.Language == LangHindi) != hasDeva {
			t.Errorf("%s: language %s but Devanagari present=%v", tk.ID, tk.Language, hasDeva)
		}
		for _, ex := range []struct {
			name string
			v    any
		}{{"reference_plan", tk.ReferencePlan}, {"utterance", tk.Utterance}} {
			b, _ := json.Marshal(ex.v)
			if strings.Contains(string(b), "expected") {
				t.Errorf("%s: %s mentions the expected state", tk.ID, ex.name)
			}
		}
	}
	want := map[string]int{FamilyVoucherEntry: 50, FamilyItemInvoice: 30, FamilyBankMatch: 30,
		FamilyGSTReturns: 25, FamilyNLQuery: 30, FamilyMasters: 25, FamilyGuard: 10}
	for f, n := range want {
		if fam[f] != n {
			t.Errorf("family %s has %d tasks, pinned at %d", f, fam[f], n)
		}
	}
	for _, l := range []string{LangEnglish, LangHindi, LangHinglish} {
		if lang[l] < 60 {
			t.Errorf("language %s has %d tasks, floor 60", l, lang[l])
		}
	}
}

// TestWrongAgentPerturbsEveryWrite: for the WrongAgent to be evidence about
// the JUDGE, every write in every reference plan must either be perturbed or
// dropped — never executed as written.
func TestWrongAgentPerturbsEveryWrite(t *testing.T) {
	tasks, err := LoadDir(suiteDir(t))
	if err != nil {
		t.Fatal(err)
	}
	writes, perturbed, dropped := 0, 0, 0
	for _, tk := range tasks {
		for _, s := range tk.ReferencePlan {
			if s.IsAnswer() || s.Method == "GET" {
				continue
			}
			writes++
			m, ok := perturb(s)
			switch {
			case !ok:
				dropped++
			case string(m.Body) == string(s.Body):
				t.Errorf("%s: perturb reported a change and changed nothing", tk.ID)
			default:
				perturbed++
			}
		}
	}
	t.Logf("reference-plan writes=%d perturbed=%d dropped=%d", writes, perturbed, dropped)
	if writes == 0 || perturbed == 0 {
		t.Fatalf("no writes perturbed (writes=%d)", writes)
	}
}

// TestJudgeRefusesAShotgunAnswer: every expected figure appearing SOMEWHERE
// among many is not an answer. Before the bound, an answer that dumped the
// whole trial balance passed any read-only task (the wave-1 review).
func TestJudgeRefusesAShotgunAnswer(t *testing.T) {
	defaults := baseState()
	q := Expected{Outputs: []string{"29750.00"}}
	cases := []struct {
		answer string
		pass   bool
	}{
		{"Acme Traders: 29,750.00 Dr as on 30 June 2025.", true},
		{"Acme Traders owes ₹29,750 of the ₹35,000 billed in June 2025.", true},
		{"HSN 998311: taxable 29,750.00.", true},
		{"Trial balance: Cash 12,000.00 Dr; HDFC Bank 2,50,067.00 Dr; Acme Traders 29,750.00 Dr; Sales 47,500.00 Cr; Rent 21,333.00 Dr.", false},
		{"Balances: 12000, 250067, 29750, 47500, 21333.", false},
	}
	for _, c := range cases {
		if v := Judge(q, defaults, defaults, c.answer); v.Pass != c.pass {
			t.Errorf("%q: pass=%v, want %v (%v)", c.answer, v.Pass, c.pass, v.Reasons)
		}
	}
}
