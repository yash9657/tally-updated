package agent

import (
	"strings"
	"testing"
)

func TestFinalPresentationContract(t *testing.T) {
	for _, s := range []string{
		strings.Repeat("review ", 251), strings.Repeat("समीक्षा ", 251), strings.Repeat("jaanch ", 251),
		strings.Repeat("क", 1801), "Voucher 0199aabb-ccdd-7000-8000-112233445566",
		"Recovery will realise less than ₹25,000.",
		"A return recovers the most without a margin hit.", "Return is the cheapest exit.",
		"Nothing posted. Your money is safe.", "Aapke paise safe hain.", "आपके पैसे सुरक्षित हैं।",
	} {
		if finalProblem(s, "") == "" {
			t.Errorf("accepted bad answer: %.80s", s)
		}
	}
	if finalProblem("Check supplier terms before choosing a return.", "A return recovers the most.") == "" {
		t.Fatal("unsupported claim in expanded details accepted")
	}
	for _, s := range []string{
		"100 units valued at ₹25,000 at the last rate. Recovery depends on agreed return terms. Draft: Please confirm return or credit terms for RETAIL-OLD.",
		"₹16,500 is payable on account, not allocated to bills; the ₹3,000 rate comparison is a scenario, not savings.",
		"₹20,000 बाकी है। सबसे पुराना बिल 100 दिन का है; 20 दिन के सामान्य भुगतान समय से 80 दिन अधिक।",
		"₹12,000 suspected duplicate ₹24,000 total payable mein shamil hai; dono ko jodna sahi nahi. Approval abhi pending hai.",
	} {
		if p := finalProblem(s, strings.Repeat("Detailed requested explanation. ", 300)); p != "" {
			t.Fatal(p)
		}
	}
}
