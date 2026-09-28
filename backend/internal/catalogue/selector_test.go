package catalogue

import (
	"testing"
)

// heldOut are questions written BEFORE the selector was tuned and kept OUT of
// entries.go on purpose — the self-retrieval test below can be won by
// memorising the catalogue's own sentences; this set cannot. Each names the
// report a competent accountant would open. Mixed English / Hinglish, some
// with typos and party names, because that is how they arrive.
//
// ⚠️ THE FIRST MEASUREMENT IS RECORDED IN docs/design/d122 AND THE GATE IS
// PINNED TO IT. Adding a synonym to make one of these pass is tuning on the
// test set; do it only with a new held-out question beside it.
var heldOut = []struct {
	q    string
	want string
}{
	{"how much does Sunrise Traders owe", "outstandings"},
	{"customers ka paisa kitna baki hai", "outstandings"},
	{"overdue receivables above 60 days", "outstandings"},
	{"what do we have to pay Sunrise Steel", "bills-payable"},
	{"creditors list with dues", "bills-payable"},
	{"net profit for the quarter", "profit-and-loss"},
	{"is saal ka nafa", "profit-and-loss"},
	{"sales revenue in april", "profit-and-loss"},
	{"trial balance as on 31 march", "trial-balance"},
	{"balance sheet as at year end", "balance-sheet"},
	{"total assets and liabilities", "balance-sheet"},
	{"vouchers entered yesterday", "day-book"},
	{"rojnamcha dikhao", "day-book"},
	{"cash forecast for next 4 weeks", "cash-flow"},
	{"paisa kab aayega customers se", "cash-flow"},
	{"any duplicate entries in the books", "anomalies"},
	{"GSTR-1 for september", "gstr1"},
	{"GST payable for this month", "gstr3b"},
	{"bank reconciliation for Canara", "bank-reconciliation"},
	{"uncleared cheques", "bank-reconciliation"},
	{"deposits in the bank statement", "bank-statement"},
	{"TDS deducted under 194C", "tds-register"},
	{"department wise expenses", "cost-centre-summary"},
	{"pending sales orders to dispatch", "sales-order-book"},
	{"open purchase orders", "purchase-order-book"},
	{"closing stock value", "stock-summary"},
	{"maal kitna bacha hai godam me", "stock-summary"},
	{"units sold of Sprocket", "stock-movement"},
	{"stock in each warehouse", "godown-summary"},
	{"items expiring soon", "batch-summary"},
	{"invoices missing in GSTR-2B", "itc-reconciliation"},
	{"accept or reject invoices in IMS", "ims-inbox"},
	{"salary register for june", "payroll-register"},
	{"salary slip of Suresh", "payroll-payslip"},
	{"PF challan for august", "payroll-pf-ecr"},
	{"ESIC contribution return", "payroll-esi-return"},
	{"budget vs actual for marketing", "budget-variance"},
	{"annual GST return GSTR-9", "gstr9"},
	{"exchange difference on dollar invoices", "unadjusted-forex"},
	{"entries that did not match on import", "reconciliation-queue"},
}

// heldOutNames are the party, bank, employee and item names the held-out
// questions use. ⛔ NONE MAY APPEAR IN THE INDEX (TestHeldOutNamesAreUnseen).
// The first set reused the catalogue's own example names — Kaveri, Bharat
// Metals, Ramesh, HDFC, Widget are all in entries.go's Questions — so part of
// the 38/40 top-1 was the selector recognising a memorised party, not routing
// the question (the wave-1 review). A tenant's questions name the tenant's
// parties, which the index has never seen and which therefore score zero.
var heldOutNames = []string{"Sunrise Traders", "Sunrise Steel", "Canara", "Suresh", "Sprocket"}

// heldOutTop3Floor is the MEASURED top-3 accuracy on heldOut. First pinned at
// 0.975 (top-1 38/40, top-3 39/40); RE-MEASURED at 0.95 (top-1 36/40, top-3
// 38/40) once the held-out names were replaced with ones the index has never
// seen — no synonym, weight or entry was changed to recover the loss, because
// that would be tuning on the test set. d122 §2 records both. A change that
// loses one is a regression; one that gains may raise this constant.
const heldOutTop3Floor = 0.95

func rankOf(ms []Match, report string) int {
	for i, m := range ms {
		if m.Report == report {
			return i + 1
		}
	}
	return 0
}

// TestSelectorSelfRetrieval: every typical question in the catalogue ranks its
// own report in the top 3. It is the weak half (the sentences are in the
// index); it exists so an entry whose questions are dominated by ANOTHER
// report's vocabulary is found and fixed rather than shipped.
func TestSelectorSelfRetrieval(t *testing.T) {
	total, miss := 0, 0
	for _, e := range Reports() {
		for _, q := range e.Questions {
			total++
			if r := rankOf(Select(q, 3), e.Report); r == 0 {
				miss++
				got := Select(q, 3)
				t.Errorf("%q should rank %s in the top 3; got %v", q, e.Report, got)
			}
		}
	}
	t.Logf("self-retrieval top-3: %d/%d", total-miss, total)
}

// TestSelectorHeldOut is the honest half: questions the catalogue never saw.
func TestSelectorHeldOut(t *testing.T) {
	top1, top3 := 0, 0
	for _, h := range heldOut {
		got := Select(h.q, 3)
		switch rankOf(got, h.want) {
		case 1:
			top1++
			top3++
		case 2, 3:
			top3++
		default:
			t.Logf("MISS %q: want %s, got %v", h.q, h.want, got)
		}
	}
	n := float64(len(heldOut))
	t.Logf("held-out: top-1 %d/%d (%.1f%%), top-3 %d/%d (%.1f%%)", top1, len(heldOut), 100*float64(top1)/n, top3, len(heldOut), 100*float64(top3)/n)
	if float64(top3)/n < heldOutTop3Floor {
		t.Errorf("held-out top-3 accuracy %.3f fell below the measured floor %.2f", float64(top3)/n, heldOutTop3Floor)
	}
}

// TestHeldOutNamesAreUnseen: the held-out set measures routing, not recall of
// the catalogue's example parties. Every name it uses must be absent from the
// index, and every held-out question naming a party must use one of them.
func TestHeldOutNamesAreUnseen(t *testing.T) {
	for _, name := range heldOutNames {
		for _, tok := range tokens(name) {
			if idx.df[tok] > 0 {
				t.Errorf("held-out name %q: token %q is in the index (%d reports) — the held-out set would measure memorisation", name, tok, idx.df[tok])
			}
		}
	}
	// The catalogue's own example names, which the held-out set must not use.
	for _, seen := range []string{"kaveri", "bharat", "ramesh", "hdfc", "widget", "gadget"} {
		if idx.df[seen] == 0 {
			continue // no longer an example name; nothing to guard
		}
		for _, h := range heldOut {
			for _, tok := range tokens(h.q) {
				if tok == seen {
					t.Errorf("held-out question %q uses %q, a name the catalogue's own questions contain", h.q, seen)
				}
			}
		}
	}
}

// TestReceivableAndPayableWordsPointTheRightWay: लेनदारी (lenadari) is what
// we are OWED and देनदारी (denadari) what we OWE. The first cut listed
// lenadari under Bills Payable and denadari nowhere, so a Hinglish question
// about receivables opened the payables report and the payables word selected
// nothing (the wave-1 review). The root rule is pinned too: no "lena" word
// names the payables report and no "dena" word names the receivables one.
func TestReceivableAndPayableWordsPointTheRightWay(t *testing.T) {
	for _, c := range []struct{ q, want string }{
		{"lenadari kitni hai", "outstandings"},
		{"customers ki lenadari", "outstandings"},
		{"denadari kitni hai", "bills-payable"},
		{"suppliers ki denadari", "bills-payable"},
	} {
		got := Select(c.q, 3)
		if len(got) == 0 || got[0].Report != c.want {
			t.Errorf("%q should select %s first; got %v", c.q, c.want, got)
		}
	}
	for _, e := range Reports() {
		for _, h := range e.Hinglish {
			for _, tok := range tokens(h) {
				if e.Report == "bills-payable" && (tok == "lena" || tok == "lenadari") {
					t.Errorf("bills-payable lists the receivables word %q (in %q)", tok, h)
				}
				if e.Report == "outstandings" && (tok == "dena" || tok == "denadari") {
					t.Errorf("outstandings lists the payables word %q (in %q)", tok, h)
				}
			}
		}
	}
}

// TestSelectorRefusesToGuess: a question sharing no term with any report
// returns nothing, and that empty result is the planner's cue to refuse.
func TestSelectorRefusesToGuess(t *testing.T) {
	if got := Select("what is the weather in Pune", 3); len(got) != 0 {
		t.Errorf("an off-domain question must select nothing; got %v", got)
	}
}

// TestSelectorIsDeterministic: identical input, identical ranking, across
// calls — the property W1/W8 evals depend on.
func TestSelectorIsDeterministic(t *testing.T) {
	a := Select("kitna baki hai customers ka", 5)
	for i := 0; i < 20; i++ {
		b := Select("kitna baki hai customers ka", 5)
		if len(a) != len(b) {
			t.Fatalf("length changed across calls")
		}
		for j := range a {
			if a[j].Report != b[j].Report || a[j].Score != b[j].Score {
				t.Fatalf("ranking changed across calls: %v vs %v", a, b)
			}
		}
	}
}

// TestSelectorSecondHopNamesEntities: a DSL-backed report's match carries the
// entities its examples use, which is what the translator narrows to.
func TestSelectorSecondHopNamesEntities(t *testing.T) {
	got := Select("Kaveri ka kitna baki hai", 1)
	if len(got) != 1 || got[0].Report != "outstandings" {
		t.Fatalf("want outstandings first, got %v", got)
	}
	if len(got[0].Entities) != 1 || got[0].Entities[0] != "outstandings" {
		t.Errorf("outstandings should name the outstandings entity; got %v", got[0].Entities)
	}
}
