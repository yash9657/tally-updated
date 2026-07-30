package printing

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func TestTheAmountInWordsOnTheFaceOfAnInvoice(t *testing.T) {
	// The pinned example. Note there is NO "and" before "Sixty Seven": the only
	// "and" in the wording is the one that introduces the paise.
	const want = "Rupees Twelve Lakh Thirty Four Thousand Five Hundred Sixty Seven and Eighty Nine Paise Only"
	if got := AmountInWords(dec(t, "1234567.89")); got != want {
		t.Fatalf("the amount in words reads\n  %q\nbut the invoice must say\n  %q", got, want)
	}
}

func TestWordsForOneToTwentyAndForTheTens(t *testing.T) {
	cases := []struct{ in, want string }{
		{"1", "One"}, {"2", "Two"}, {"3", "Three"}, {"4", "Four"},
		{"5", "Five"}, {"6", "Six"}, {"7", "Seven"}, {"8", "Eight"},
		{"9", "Nine"}, {"10", "Ten"}, {"11", "Eleven"}, {"12", "Twelve"},
		{"13", "Thirteen"}, {"14", "Fourteen"}, {"15", "Fifteen"},
		{"16", "Sixteen"}, {"17", "Seventeen"}, {"18", "Eighteen"},
		{"19", "Nineteen"}, {"20", "Twenty"},
		{"30", "Thirty"}, {"40", "Forty"}, {"50", "Fifty"}, {"60", "Sixty"},
		{"70", "Seventy"}, {"80", "Eighty"}, {"90", "Ninety"},
	}
	for _, c := range cases {
		want := "Rupees " + c.want + " Only"
		if got := AmountInWords(dec(t, c.in)); got != want {
			t.Errorf("%s in words is %q; a cheque for that amount must read %q", c.in, got, want)
		}
	}
}

func TestWordsAtTheBoundariesIndianNumberingGetsWrong(t *testing.T) {
	cases := []struct{ in, want string }{
		{"21", "Twenty One"},
		{"99", "Ninety Nine"},
		{"100", "One Hundred"},
		{"101", "One Hundred One"},
		{"110", "One Hundred Ten"},
		{"115", "One Hundred Fifteen"},
		{"999", "Nine Hundred Ninety Nine"},
		{"1000", "One Thousand"},
		{"1001", "One Thousand One"},
		{"1100", "One Thousand One Hundred"},
		{"10000", "Ten Thousand"},
		{"99999", "Ninety Nine Thousand Nine Hundred Ninety Nine"},
		{"100000", "One Lakh"},
		{"100001", "One Lakh One"},
		{"1000000", "Ten Lakh"},
		{"1010101", "Ten Lakh Ten Thousand One Hundred One"},
		{"10000000", "One Crore"},
		{"10101010", "One Crore One Lakh One Thousand Ten"},
	}
	for _, c := range cases {
		want := "Rupees " + c.want + " Only"
		if got := AmountInWords(dec(t, c.in)); got != want {
			t.Errorf("%s in words is\n  %q\nbut it must read\n  %q", c.in, got, want)
		}
	}
}

func TestElevenHundredIsNotIndianUsage(t *testing.T) {
	// American English says "eleven hundred". No Indian cheque does, and a bank
	// may return an instrument whose words are not in the local convention.
	got := AmountInWords(dec(t, "1100"))
	if strings.Contains(got, "Eleven Hundred") {
		t.Fatalf("1100 in words is %q; \"Eleven Hundred\" is not Indian usage — it must be \"One Thousand One Hundred\"", got)
	}
	if got != "Rupees One Thousand One Hundred Only" {
		t.Fatalf("1100 in words is %q; want %q", got, "Rupees One Thousand One Hundred Only")
	}
}

func TestThePaiseClauseAppearsOnlyWhenThereArePaise(t *testing.T) {
	cases := []struct{ in, want string }{
		{"0", "Rupees Zero Only"},
		{"0.50", "Rupees Zero and Fifty Paise Only"},
		{"0.05", "Rupees Zero and Five Paise Only"},
		{"0.01", "Rupees Zero and One Paise Only"}, // "Paise" always, never "Paisa"
		{"100.00", "Rupees One Hundred Only"},
		{"100.99", "Rupees One Hundred and Ninety Nine Paise Only"},
		{"1000.05", "Rupees One Thousand and Five Paise Only"},
	}
	for _, c := range cases {
		if got := AmountInWords(dec(t, c.in)); got != c.want {
			t.Errorf("%s in words is\n  %q\nbut it must read\n  %q", c.in, got, c.want)
		}
	}
	if got := AmountInWords(dec(t, "100.00")); strings.Contains(got, "Zero Paise") {
		t.Errorf("a whole-rupee amount reads %q; a cheque for a round amount does not say \"and Zero Paise\"", got)
	}
	if got := AmountInWords(dec(t, "0.50")); !strings.Contains(got, "Rupees Zero and") {
		t.Errorf("fifty paise reads %q; dropping the \"Zero\" leaves \"Rupees and Fifty Paise Only\", which reads like a word went missing", got)
	}
}

func TestACreditNoteAmountInWordsKeepsItsSign(t *testing.T) {
	const want = "Minus Rupees One Thousand Two Hundred Thirty Four and Fifty Six Paise Only"
	if got := AmountInWords(dec(t, "-1234.56")); got != want {
		t.Fatalf("a negative note total in words is\n  %q\nbut it must read\n  %q — words that silently drop the sign contradict the figures on the same document", got, want)
	}
	// And an amount that quantizes to zero from below is not "Minus Zero".
	if got := AmountInWords(dec(t, "-0.001")); got != "Rupees Zero Only" {
		t.Errorf("-0.001 in words is %q; want %q", got, "Rupees Zero Only")
	}
}

func TestWordsAndFiguresAgreeOnAFourDecimalLedgerValue(t *testing.T) {
	// The ledger stores NUMERIC(19,4) and decimal.StringFixed(4) is everywhere in
	// this codebase, so the value handed to both formatters routinely carries FOUR
	// decimal places. If the words truncated where the figures round, the same
	// invoice would show 1,234.57 in the total box and "Fifty Six Paise" in the
	// words — and on a negotiable instrument the words are the operative amount.
	cases := []struct {
		in     string
		figure string
		words  string
	}{
		{"1234.5650", "1,234.57", "Rupees One Thousand Two Hundred Thirty Four and Fifty Seven Paise Only"},
		{"1234.5649", "1,234.56", "Rupees One Thousand Two Hundred Thirty Four and Fifty Six Paise Only"},
		// The carry crosses both the paise clause and the grouping boundary: the
		// paise clause must VANISH and the rupees must go up by one.
		{"999.9950", "1,000.00", "Rupees One Thousand Only"},
		{"99999.9950", "1,00,000.00", "Rupees One Lakh Only"},
		{"-1234.5650", "-1,234.57", "Minus Rupees One Thousand Two Hundred Thirty Four and Fifty Seven Paise Only"},
	}
	for _, c := range cases {
		d := dec(t, c.in)
		figure, words := FormatMoney(d), AmountInWords(d)
		if figure != c.figure || words != c.words {
			t.Errorf("the ledger value %s prints as figure %q and words %q; want figure %q and words %q", c.in, figure, words, c.figure, c.words)
			continue
		}
		// The property behind the two literals: the paise spoken in the words are
		// the paise shown in the figures.
		paise := figure[len(figure)-2:]
		hasPaiseClause := strings.Contains(words, " Paise ")
		if paise == "00" && hasPaiseClause {
			t.Errorf("%s prints figure %q (no paise) yet the words %q still speak paise", c.in, figure, words)
		}
		if paise != "00" && !hasPaiseClause {
			t.Errorf("%s prints figure %q (paise .%s) yet the words %q have no paise clause", c.in, figure, paise, words)
		}
	}
}

func TestAnAmountAboveOneCroreIsSpokenInTheIndianSystem(t *testing.T) {
	// Above a crore the Indian system keeps going with crore as the unit rather
	// than borrowing million and billion. Past 10^9 that means a repeated
	// "Crore", which looks odd and is what the system does; "arab" and "kharab"
	// are not used in Indian financial writing.
	cases := []struct{ in, want string }{
		{"10000000", "Rupees One Crore Only"},
		{"100000000", "Rupees Ten Crore Only"},
		{"1000000000", "Rupees One Hundred Crore Only"},
		{"12345678901", "Rupees One Thousand Two Hundred Thirty Four Crore Fifty Six Lakh Seventy Eight Thousand Nine Hundred One Only"},
		{"999999999999999.99", "Rupees Nine Crore Ninety Nine Lakh Ninety Nine Thousand Nine Hundred Ninety Nine Crore Ninety Nine Lakh Ninety Nine Thousand Nine Hundred Ninety Nine and Ninety Nine Paise Only"},
	}
	for _, c := range cases {
		if got := AmountInWords(dec(t, c.in)); got != c.want {
			t.Errorf("%s in words is\n  %q\nbut it must read\n  %q", c.in, got, c.want)
		}
	}
	if got := AmountInWords(dec(t, "1000000")); strings.Contains(got, "Million") || strings.Contains(got, "Billion") {
		t.Errorf("10 lakh in words is %q; Indian numbering has no million or billion", got)
	}
}

func TestAmountInWordsNeverPanicsOnAValueBeyondInt64(t *testing.T) {
	// decimal.IntPart() would wrap 1e30 to 5076944270305263616 and speak a
	// completely different amount with no error at all. The digits are consumed as
	// a string precisely so this cannot happen.
	beyondInt64 := decimal.New(1, 30) // 1 followed by 30 zeros
	got := AmountInWords(beyondInt64)
	if !strings.HasPrefix(got, "Rupees One ") || !strings.HasSuffix(got, " Only") {
		t.Fatalf("1e30 in words is %q; it must still be spoken as one followed by crores, not int64 wraparound", got)
	}
	if strings.Contains(got, "Five Thousand Seventy Six") {
		t.Fatalf("1e30 in words is %q, which is the int64 wraparound of IntPart() being spoken as an amount", got)
	}
	// Whatever else it does, it terminates and does not panic.
	for _, d := range []decimal.Decimal{
		decimal.New(1, 30).Neg(),
		decimal.New(-1, -30),
		decimal.Zero,
		decimal.New(999999999999999999, -2),
	} {
		if AmountInWords(d) == "" {
			t.Errorf("AmountInWords returned an empty string for %s; a blank words line on an invoice is not printable", d.String())
		}
	}
}
