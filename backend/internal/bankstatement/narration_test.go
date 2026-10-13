package bankstatement

// Narration parsing. The fields are ANNOTATIONS (narration.go's header), so
// these tests pin two things: that each rail's identifiers are read by their
// own FORMAT, and that a lookalike is NOT read — a narration mis-labelled NEFT
// is harmless to money but would poison the matcher's reference tokens.

import (
	"testing"
)

func TestNarrationsAcrossRailsAndBanks(t *testing.T) {
	for _, tc := range []struct {
		name, desc string
		want       Narration
	}{
		{"HDFC UPI", "UPI-ACME TRADERS-acme.traders@okhdfcbank-HDFC0001234-412345678901-INV 42",
			Narration{Rail: RailUPI, Counterparty: "ACME TRADERS", VPA: "acme.traders@okhdfcbank",
				Reference: "412345678901", IFSC: "HDFC0001234"}},
		{"SBI UPI credit", "BY TRANSFER-UPI/CR/412355556666/MEHTA ST/HDFC/mehta.store@okhdfc/Payment--",
			Narration{Rail: RailUPI, Counterparty: "MEHTA ST", VPA: "mehta.store@okhdfc", Reference: "412355556666"}},
		{"Axis UPI P2A", "UPI/P2A/412399990000/NISHA RAO/Rent April/HDFC BANK",
			Narration{Rail: RailUPI, Counterparty: "NISHA RAO", Reference: "412399990000"}},
		{"HDFC NEFT credit", "NEFT CR-ICIC0000104-BHARAT STEEL LTD-HDFCN52026040312345678",
			Narration{Rail: RailNEFT, Counterparty: "BHARAT STEEL LTD", Reference: "HDFCN52026040312345678", IFSC: "ICIC0000104"}},
		{"SBI NEFT debit", "TO TRANSFER-NEFT*HDFC0000001*SBINN52026040400123*PUNE FABRICATORS--",
			Narration{Rail: RailNEFT, Counterparty: "PUNE FABRICATORS", Reference: "SBINN52026040400123", IFSC: "HDFC0000001"}},
		{"HDFC IMPS", "IMPS-412398765432-RAVI KUMAR-SBIN-XXXXXXXX4321-SALARY",
			Narration{Rail: RailIMPS, Counterparty: "RAVI KUMAR", Reference: "412398765432"}},
		{"RTGS", "RTGS CR-SBIN0001234-GREEN FARMS PVT LTD-SBINR52026042000123456",
			Narration{Rail: RailRTGS, Counterparty: "GREEN FARMS PVT LTD", Reference: "SBINR52026042000123456", IFSC: "SBIN0001234"}},
		{"cheque clearing", "CHQ PAID-MICR CTS-PUNE-STEEL SUPPLIERS 000123",
			Narration{Rail: RailCheque, Counterparty: "PUNE", Reference: "000123"}},
		{"NACH", "NACH/ACH DR/HDFC LIFE INSURANCE/HDFC7021807230034209",
			Narration{Rail: RailNACH, Counterparty: "HDFC LIFE INSURANCE"}},
		{"interest", "INT.PD 01-04-2026 TO 30-04-2026",
			Narration{Rail: RailInterest}},
		{"ATM", "NWD-XXXXXX1234-S1ANPN12-PUNE",
			Narration{Rail: RailATM, Counterparty: "PUNE"}},
		// ⛔ LOOKALIKES. A word that merely CONTAINS a rail keyword is not that
		// rail, and a vendor called "NEFTY" is not an NEFT transfer.
		{"not NEFT", "NEFTY TRADERS PAYMENT", Narration{Counterparty: "NEFTY TRADERS PAYMENT"}},
		{"empty", "", Narration{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseNarration(tc.desc)
			if got != tc.want {
				t.Errorf("ParseNarration(%q)\n got  %+v\n want %+v", tc.desc, got, tc.want)
			}
		})
	}
}

// ⛔ THE NARRATION IS NOT PART OF THE LINE HASH. Improving the parser must
// never make a re-import of the same file look like new transactions, which is
// what folding a parsed field into the hash would do the day the parser
// changes.
func TestTheNarrationDoesNotEnterTheLineHash(t *testing.T) {
	st, err := Parse(twoColCSV, twoColMapping())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	l := st.Lines[1]
	before := hashLine(l)
	l.Narration = Narration{Rail: RailRTGS, Counterparty: "SOMEONE ELSE", Reference: "X"}
	if hashLine(l) != before {
		t.Fatal("the line hash moved when only the parsed narration changed")
	}
	if st.Lines[1].Narration.Rail != RailNEFT {
		t.Errorf("Parse must annotate each line; line 2 is %+v", st.Lines[1].Narration)
	}
}
