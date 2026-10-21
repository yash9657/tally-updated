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
