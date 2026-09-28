package bankstatement

// BANK HEADER SIGNATURES — five banks' export layouts, recognised BY THEIR
// HEADER NAMES, proposing a mapping the operator still confirms.
//
// Design memo: docs/design/d123-w5-documents-bank.md §2.2, which records this
// as a deliberate, bounded DEPARTURE from t4-slice2-model.md §8.2.
//
// ⛔ WHY §8.2 FORBADE PROFILES, AND WHY THESE DO NOT REOPEN ITS HAZARD
// --------------------------------------------------------------------
// §8.2's objection was specific: a profile "recalled from memory" whose COLUMN
// ORDER is wrong by one reads the running balance as the amount, and every
// figure stays plausible. Every signature below is immune to that failure by
// construction, for three reasons that together are the licence to ship them:
//
//  1. A SIGNATURE IS A SET OF HEADER NAMES, NEVER POSITIONS. The mapping it
//     proposes names columns by their text; resolve() turns names into
//     positions from THE FILE's header. Column order cannot be wrong because
//     it is never stated.
//  2. IT MATCHES ONLY WHEN EVERY NAME IN IT IS PRESENT IN THE FILE. A layout
//     recalled wrongly — a header spelled differently, a column the bank no
//     longer exports — does not match, and the proposal falls back to the
//     synonym reading in propose.go, exactly as before. So a wrong recollection
//     costs "not recognised", never "mis-read".
//  3. THE MANDATORY BALANCE PROOF (proof.go) SITS BEHIND IT. A mapping that
//     survived 1 and 2 and is still wrong — say, deposit and withdrawal
//     swapped — walks no running balance and foots no declared closing.
//
// ⚠️ AND THE HONEST LIMIT, STATED WHERE THE DATA IS: NO REAL EXPORT FROM ANY OF
// THESE BANKS EXISTS IN THIS ENVIRONMENT. The header texts are the layouts
// these banks' net-banking downloads are commonly documented with; each
// profile's Verified field is false and the proposal SAYS so to the operator.
// The way to make one true is §8.2's own route: confirm it against a real file
// an operator imported, then flip the field in the same commit as a test built
// from that file's header (with the transaction rows removed).
//
// THE DATE LAYOUT IS NEVER TAKEN FROM A PROFILE. It is proposed from the file's
// own dates by proposeDateLayout, rule 3 in propose.go, because a bank that
// changed its date format would otherwise shift every statement by months with
// a recognised header on top.

import "strings"

// BankProfile is one bank's export, as a header signature and the mapping its
// header names imply.
type BankProfile struct {
	// Bank is the display name shown in the proposal.
	Bank string
	// Signature is every header (normalised by normaliseHeader) the file must
	// carry for this profile to match. ALL of them, not most.
	Signature []string
	// Mapping names columns by the file's own header text. Encoding is set;
	// DateLayout is deliberately left empty (see the file header).
	Mapping ColumnMapping
	// Verified is true only when the signature was confirmed against a real
	// export. None is yet.
	Verified bool
}

// bankProfiles is checked in order and the FIRST full match wins. Two
// profiles matching one file would be a signature too loose to trust, and
// TestNoTwoProfilesMatchTheSameHeader pins that none do.
var bankProfiles = []BankProfile{
	{
		// HDFC Bank net-banking "Delimited"/XLS download. Two date columns, and
		// "Date" is the TRANSACTION date: the value date is "Value Dt". The
		// synonym reading cannot settle that (both match value_date), which is
		// precisely what this profile adds.
		Bank:      "HDFC Bank",
		Signature: []string{"date", "narration", "chqrefno", "valuedt", "withdrawalamt", "depositamt", "closingbalance"},
		Mapping: ColumnMapping{
			Encoding: TwoColumns, ValueDate: "Value Dt", TxnDate: "Date", Description: "Narration",
			Withdrawal: "Withdrawal Amt.", Deposit: "Deposit Amt.", RunningBalance: "Closing Balance",
			BankReference: "Chq./Ref.No.",
		},
	},
	{
		// ICICI Bank detailed statement (XLS/CSV). Amount headers carry the
		// currency in parentheses.
		Bank: "ICICI Bank",
		Signature: []string{"valuedate", "transactiondate", "chequenumber", "transactionremarks",
			"withdrawalamountinr", "depositamountinr", "balanceinr"},
		Mapping: ColumnMapping{
			Encoding: TwoColumns, ValueDate: "Value Date", TxnDate: "Transaction Date",
			Description: "Transaction Remarks", Withdrawal: "Withdrawal Amount (INR )",
			Deposit: "Deposit Amount (INR )", RunningBalance: "Balance (INR )", ChequeNumber: "Cheque Number",
		},
	},
	{
		// State Bank of India account statement. The ".xls" is TAB-separated
		// text, which readTable handles by delimiter.
		Bank:      "State Bank of India",
		Signature: []string{"txndate", "valuedate", "description", "refnochequeno", "debit", "credit", "balance"},
		Mapping: ColumnMapping{
			Encoding: TwoColumns, ValueDate: "Value Date", TxnDate: "Txn Date", Description: "Description",
			Withdrawal: "Debit", Deposit: "Credit", RunningBalance: "Balance", BankReference: "Ref No./Cheque No.",
		},
	},
	{
		// Axis Bank statement. Terse headers the synonym list only partly knows.
		Bank:      "Axis Bank",
		Signature: []string{"trandate", "chqno", "particulars", "dr", "cr", "bal"},
		Mapping: ColumnMapping{
			Encoding: TwoColumns, ValueDate: "Tran Date", Description: "PARTICULARS",
			Withdrawal: "DR", Deposit: "CR", RunningBalance: "BAL", ChequeNumber: "CHQNO",
		},
	},
	{
		// Kotak Mahindra Bank. ONE amount column with its own Dr/Cr flag, and a
		// SECOND "Dr / Cr" column carrying the BALANCE's sign — the repeated
		// header dedupeHeader renames "Dr / Cr (2)". Without that rename the
		// amount's direction was read off the balance's flag.
		Bank:      "Kotak Mahindra Bank",
		Signature: []string{"slno", "date", "description", "chqrefnumber", "amount", "drcr", "balance", "drcr2"},
		Mapping: ColumnMapping{
			Encoding: AmountAndFlag, ValueDate: "Date", Description: "Description",
			Amount: "Amount", Direction: "Dr / Cr", RunningBalance: "Balance", BalanceSign: "Dr / Cr (2)",
			BankReference: "Chq / Ref number",
		},
	},
}

// recogniseBank returns the ONE profile whose whole signature is in header.
func recogniseBank(header []string) (BankProfile, bool) {
	have := map[string]bool{}
	for _, h := range header {
		have[normaliseHeader(h)] = true
	}
	for _, p := range bankProfiles {
		ok := true
		for _, s := range p.Signature {
			if !have[s] {
				ok = false
				break
			}
		}
		if ok {
			return p, true
		}
	}
	return BankProfile{}, false
}

// headerNamed returns the file's own spelling of a profile column, so the
// mapping carries exactly what the file says (resolve() is case-insensitive,
// but the operator sees these strings in the picker).
func headerNamed(header []string, name string) string {
	if name == "" {
		return ""
	}
	for _, h := range header {
		if strings.EqualFold(strings.TrimSpace(h), name) || normaliseHeader(h) == normaliseHeader(name) {
			return h
		}
	}
	return name
}
