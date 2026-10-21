package tau

import "testing"

func TestAnswerUUIDsAreNotMoney(t *testing.T) {
	const id = "01a15ff9-4b3c-704e-b9fa-2df963492ebc"
	if got := checkOutputs(nil, "Recorded receipt ₹12,500.00, voucher_id "+id); len(got) != 0 {
		t.Fatal(got)
	}
	// A matching numeric fragment inside an identifier cannot satisfy an answer.
	if got := checkOutputs([]string{"963492"}, "voucher_id "+id); len(got) == 0 {
		t.Fatal("UUID satisfied required money output")
	}
	if got := checkOutputs(nil, "₹12,500.00 and ₹99,999.00, voucher_id "+id); len(got) == 0 {
		t.Fatal("unexpected money escaped judge")
	}
}

func TestTaxIdentifiersAndDatesCannotSatisfyMoneyAnswers(t *testing.T) {
	text := "June 2025 GSTR-1, filing period 062025, GSTIN 27AAACK1234A1ZA, SAC 998314: taxable ₹89,000.00, total ₹1,05,020.00, IGST ₹16,020.00. As of 2025-06-30."
	if got := checkOutputs([]string{"89000", "105020"}, text); len(got) != 0 {
		t.Fatal(got)
	}
	for _, amount := range []string{"62025", "1234", "998314"} {
		if len(checkOutputs([]string{amount}, text)) == 0 {
			t.Fatalf("identifier satisfied %s", amount)
		}
	}
	if len(checkOutputs([]string{"89000"}, "89000 10000 11000 12000")) == 0 {
		t.Fatal("shotgun answer passed")
	}
}

func TestTrailingPunctuationIsNotDigitGrouping(t *testing.T) {
	if extra := extraMoneyFigures([]string{"590"}, "Posted ₹590.00, voucher 1, approval 2,"); len(extra) != 0 {
		t.Fatalf("punctuation turned identifiers into money: %v", extra)
	}
	if extra := extraMoneyFigures([]string{"590"}, "₹590.00, unrelated 12,500,"); len(extra) != 1 {
		t.Fatalf("real grouped money escaped: %v", extra)
	}
}
