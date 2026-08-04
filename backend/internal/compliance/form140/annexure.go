package form140

import "sort"

// The annexures of the Form 140 file format, transcribed from
// docs/statutory/form-140-file-format.xlsx sheet "File Format_R".
//
// Annexures 1 and 5 are both "State Name / State Code" and their rows are
// IDENTICAL in the workbook — 1 is cited by the two address-state fields,
// 5 by the "State Name" field that only Government deductors fill in. They are
// one table here, with both citations, rather than two copies that can drift.

// ---------------------------------------------------------------------------
// Annexure 2 — the payment codes
// ---------------------------------------------------------------------------

// PaymentCodes is every "Section code to be used in the return" in Annexure 2,
// mapped to the 2025-Act limb it reports.
//
// THIS PACKAGE DOES NOT CHOOSE A CODE. compliance.TDSReturnCode does, keyed on
// (section, deductee type), at the moment the deduction is derived — because the
// code is not a function of the section: 1027 (professional services) and 1028
// (directors' fees) are BOTH s.393(1) [Table: Sl. No. 6(iii).D(b)] at 10%. This
// map exists so a code arriving in a DD record can be CHECKED against the
// annexure, and so ReasonFlagValid has a domain.
var PaymentCodes = map[string]string{
	"1004": "392(7)",
	"1005": "393(1) [Table: Sl. No. 1(i)]",
	"1006": "393(1) [Table: Sl. No. 1(ii)]",
	"1008": "393(1) [Table: Sl. No. 2(ii).D(a)]",
	"1009": "393(1) [Table: Sl. No. 2(ii).D(b)]",
	"1011": "393(1) [Table: Sl. No. 3(ii)]",
	"1012": "393(1) [Table: Sl. No. 3(iii)]",
	"1013": "393(1) [Table: Sl. No. 4(i)]",
	"1014": "393(1) [Table: Sl. No. 4(ii)]",
	"1015": "393(1) [Table: Sl. No. 4(ii)]",
	"1016": "393(1) [Table: Sl. No. 4(ii)]",
	"1017": "393(1) [Table: Sl. No. 4(iii)]",
	"1018": "393(1) [Table: Sl. No. 4(iv)]",
	"1019": "393(1) [Table: Sl. No. 5(i)]",
	"1020": "393(1) [Table: Sl. No. 5(ii).D(a)]",
	"1021": "393(1) [Table: Sl. No. 5(ii).D(b)]",
	"1022": "393(1) [Table: Sl. No. 5(iii)]",
	"1023": "393(1) [Table: Sl. No. 6(i).D(a)]",
	"1024": "393(1) [Table: Sl. No. 6(i).D(b)]",
	"1026": "393(1) [Table: Sl. No. 6(iii).D(a)]",
	"1027": "393(1) [Table: Sl. No. 6(iii).D(b)]",
	"1028": "393(1) [Table: Sl. No. 6(iii).D(b)]",
	"1029": "393(1) [Table: Sl. No. 7]",
	"1030": "393(1) [Table: Sl. No. 8(i)]",
	"1031": "393(1) [Table: Sl. No. 8(ii)]",
	"1033": "393(1) [Table: Sl. No. 8(iv)]",
	"1034": "393(1) [Table: Sl. No. 8(iv)] Note 6",
	"1035": "393(1) [Table: Sl. No. 8(v)]",
	"1037": "393(1) [Table: Sl. No. 8(vi)]",
	"1038": "393(1) [Table: Sl. No. 8(vi)] Note 6",
	"1058": "393(3) [Table: Sl. No. 1]",
	"1059": "393(3) [Table: Sl. No. 1] Note 2",
	"1060": "393(3) [Table: Sl. No. 2]",
	"1061": "393(3) [Table: Sl. No. 2] Note 2",
	"1062": "393(3) [Table: Sl. No. 3]",
	"1063": "393(3) [Table: Sl. No. 4]",
	"1064": "393(3) [Table: Sl. No. 5.D(a)]",
	"1065": "393(3) [Table: Sl. No. 5.D(b)]",
	"1066": "393(3) [Table: Sl. No. 6]",
	"1067": "393(3) [Table: Sl. No. 7]",
}

// codesRequiringRealPAN are the four codes for which DD field 8 forbids the
// default PAN placeholders: "For section codes '1059', '1034', '1038' or
// '1061', no default PAN values should be allowed under this field."
//
// They are also the four for which tax deducted, tax deposited and the rate
// must all be zero (DD fields 24, 25, 28) — they are the "tax paid before the
// benefit was released" cases, where the deductor bore the tax itself. This
// engine derives none of them, so the constant is a guard rather than a path.
var codesRequiringRealPAN = map[string]bool{
	"1034": true, "1038": true, "1059": true, "1061": true,
}

// ---------------------------------------------------------------------------
// Annexure 6 — the reason flags, AND WHY THIS IS A MATRIX AND NOT A LIST
// ---------------------------------------------------------------------------

// ReasonFlags maps each Annexure 6 flag to its meaning, verbatim.
//
// ⚠️ THERE ARE THIRTEEN, NOT "A THROUGH D". A secondary source carried a
// four-valued set; the primary spec has A, B, C, D, E, N, O, P, Q, S, T, Y, Z.
var ReasonFlags = map[string]string{
	"A": "\"Lower deduction\" or \"no deduction\" is on account of a certificate under section 395(1)",
	"B": "No deduction is on account of declaration under section 393(6)",
	"C": "Deduction is on higher rate under section 397(2) on account of non-furnishing of PAN",
	"D": "No deduction or lower deduction is on account of payment made to a person or class of person " +
		"on account of notification issued under section 194A(5) of the Income-tax Act, 1961 or being " +
		"issued under section 400(1) of the Act",
	"E": "No deduction is on account of payment being made to a person referred to in Board Circular " +
		"No. 3 of 2002, No. 11 of 2002 or No. 18/2017",
	"N": "No deduction is on account of payment made to a person referred to in section 393(4) " +
		"[Table: Sl. No. 18], or notification under the fifth proviso to section 194N of the 1961 Act",
	"O": "No deduction as per the provisions of section 393(4) [Table: Sl. No. 5]",
	"P": "No deduction is on account of payment of dividend made to a business trust referred to in " +
		"section 393(4) [Table: Sl. No. 10(d)] or a notification under [Sl. No. 10(e)]",
	"Q": "No deduction is on account of payment made to an entity referred to in section 393(4) " +
		"[Table: Sl. No. 7(c)(v)]",
	"S": "No deduction is in view of the tax being deductible or collectible under any other provision " +
		"of the Act, per section 393(1) [Table: Sl. No. 8(ii)] Note 1(a)",
	"T": "No deduction is on account of the deductee being a transporter; PAN of the deductee is " +
		"MANDATORY {section 393(4) [Table: Sl. No. 8]}",
	"Y": "No deduction is on account of payment below threshold limit as specified in the Act",
	"Z": "No deduction or lower deduction is on account of payment in view of notification issued " +
		"under section 400(1) of the Act",
}

// allPaymentCodes is every key of PaymentCodes. Flags A, C and E are valid
// against EXACTLY this set — checked row by row against the workbook, not
// assumed — so they are expressed as the whole domain rather than as three
// forty-line copies that could drift from PaymentCodes.
func allPaymentCodes() map[string]bool {
	out := make(map[string]bool, len(PaymentCodes))
	for c := range PaymentCodes {
		out[c] = true
	}
	return out
}

func codeSet(codes ...string) map[string]bool {
	out := make(map[string]bool, len(codes))
	for _, c := range codes {
		out[c] = true
	}
	return out
}

// reasonFlagValidity is Annexure 6 in full: for each flag, the section codes it
// may legally be quoted against.
//
// ===========================================================================
// ⛔ THIS IS A VALIDATION RULE, NOT A LOOKUP, AND THE DIFFERENCE COSTS MONEY
// ===========================================================================
// The workbook tabulates flags AGAINST section codes. A flag quoted against a
// code that is not listed for it is a rejected statement — so this cannot be
// modelled as "pick a flag from the situation" alone. Two consequences that
// fall straight out of the matrix and would otherwise have been guessed:
//
//  1. **Y IS NOT VALID AGAINST 1028** (directors' fees). That is not an
//     omission: s.393(1) [Table: Sl. No. 6(iii)] threshold "(ii) for (c) of Col.
//     B: Nil", so a directors' fee CANNOT be below a threshold — there isn't
//     one. compliance's BasisNil says the same thing from the statute's side,
//     and the two agreeing is the strongest check available here that both were
//     read correctly.
//
//  2. **Y IS NOT VALID AGAINST 1008 OR 1009** (rent — machinery, and other),
//     and rent DOES have a threshold. So a sub-threshold rent payment has no
//     below-threshold flag available to it, and 1009 admits no Z either. This
//     build REFUSES to emit such a deductee record rather than choosing between
//     a blank flag and an invalid one — see errNoValidReasonFlag. The workbook
//     does not say what to do instead, and inventing an answer to that is
//     exactly the class of guess this whole track exists to avoid.
//
// ⚠️ Two codes appear here that are NOT in Annexure 2: 1045 and 1046, both
// s.393(2), against flag Z. The annexure's own header reads "Code in FN 139",
// which suggests the matrix is shared across the form family and carries codes
// this form does not use. Recorded rather than silently dropped; they simply
// never arrive, because PaymentCodes is what a DD record is checked against
// first.
var reasonFlagValidity = map[string]map[string]bool{
	"A": allPaymentCodes(),
	"B": codeSet("1004", "1005", "1008", "1009", "1013", "1019", "1020", "1021", "1022", "1029", "1030"),
	"C": allPaymentCodes(),
	"D": codeSet("1020", "1021", "1022"),
	"E": allPaymentCodes(),
	"N": codeSet("1064", "1065"),
	"O": codeSet("1015"),
	"P": codeSet("1029"),
	"Q": codeSet("1020", "1021", "1022"),
	"S": codeSet("1031"),
	"T": codeSet("1023", "1024"),
	"Y": codeSet("1004", "1005", "1006", "1012", "1013", "1019", "1020", "1021", "1022",
		"1023", "1024", "1026", "1027", "1029", "1030", "1031", "1033", "1034", "1035",
		"1037", "1038", "1058", "1059", "1062", "1063", "1066", "1067"),
	"Z": codeSet("1005", "1006", "1008", "1017", "1019", "1023", "1024", "1026", "1027",
		"1028", "1029", "1045", "1046"),
}

// nilChallanFlags are the flags General Note 11 requires on every deductee
// record under a nil challan or transfer voucher: "Nil challans/transfer
// vouchers need to mandatorily have deductee records with flag 'A', 'B', 'D',
// 'E', 'N', 'O', 'P', 'Q', 'Y', 'S', 'T' & 'Z' in the remarks for lower or
// non-deduction."
//
// Note what is ABSENT: C. A nil challan means nothing was deposited, and C
// means tax was deducted at the higher no-PAN rate — which would have to be
// deposited. The exclusion is coherent, and it is the spec's, not an inference.
var nilChallanFlags = codeSet("A", "B", "D", "E", "N", "O", "P", "Q", "Y", "S", "T", "Z")

// ReasonFlagValid reports whether Annexure 6 permits flag against code.
func ReasonFlagValid(flag, code string) bool {
	codes, ok := reasonFlagValidity[flag]
	if !ok {
		return false
	}
	return codes[code]
}

// ReasonFlagsFor lists every Annexure 6 flag legal against a payment code, in
// the workbook's order. It exists so a refusal can say what the alternatives
// WERE rather than only that the chosen flag was wrong — an empty result is the
// 1009-sub-threshold case above, and a caller must be able to tell that apart
// from "you picked the wrong one of six".
func ReasonFlagsFor(code string) []string {
	var out []string
	for flag, codes := range reasonFlagValidity {
		if codes[code] {
			out = append(out, flag)
		}
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// Annexure 4 — deductor category
// ---------------------------------------------------------------------------

// DeductorCategories maps the workbook's "Value for Category in database / FVU"
// to its description.
//
// ⚠️ THIS IS NOT compliance's EntityCategory, and the two must not be conflated.
// s.402(11)'s categories answer "must this company deduct at all"; these answer
// "what letter goes in BH field 32". They overlap but do not correspond: the
// statute has one "individual or HUF" limb, this annexure has one code Q for
// both; the statute has no notion of "Branch / Division of Company" (M) at all,
// and this annexure splits Government into Central (A) and State (S), which the
// statute does not. A mapping between them is a DECISION, not a derivation, and
// it belongs to whatever stores the deductor's return profile — not here.
var DeductorCategories = map[string]string{
	"A": "Central Government",
	"S": "State Government",
	"D": "Statutory body (Central Govt.)",
	"E": "Statutory body (State Govt.)",
	"G": "Autonomous body (Central Govt.)",
	"H": "Autonomous body (State Govt.)",
	"L": "Local Authority (Central Govt.)",
	"N": "Local Authority (State Govt.)",
	"K": "Company",
	"M": "Branch / Division of Company",
	"P": "Association of Person (AOP)",
	"T": "Association of Person (Trust)",
	"J": "Artificial Juridical Person",
	"B": "Body of Individuals",
	"Q": "Individual/HUF",
	"F": "Firm",
}

// stateNameCodeMandatory are the deductor categories for which BH field 54
// ("State Name", Annexure 5) is mandatory and for which it is otherwise
// forbidden: "Mandatory if deductor type is State Govt. (code S), Statutory
// body - State Govt. (code E), Autonomous body - State Govt. (code H) and Local
// Authority - State Govt. (code N). For other deductor category no value should
// be provided."
var stateNameCodeMandatory = codeSet("S", "E", "H", "N")

// ministryMandatory are the deductor categories for which BH field 57 (Ministry
// Name, Annexure 3) is mandatory: Central Govt (A), Statutory body - Central
// Govt. (D) and Autonomous body - Central Govt. (G). It is OPTIONAL for E, H, L
// and N, and forbidden for everything else.
var ministryMandatory = codeSet("A", "D", "G")

var ministryOptional = codeSet("E", "H", "L", "N")

// ---------------------------------------------------------------------------
// Annexures 1 and 5 — state codes
// ---------------------------------------------------------------------------

// StateCodes maps the workbook's two-digit state code to its name. Note there
// is NO code 08: the workbook jumps from 07 (Dadra & Nagar Haveli and Daman &
// Diu, merged) to 09 (Delhi). Transcribed as printed.
var StateCodes = map[string]string{
	"01": "ANDAMAN AND NICOBAR ISLANDS", "02": "ANDHRA PRADESH",
	"03": "ARUNACHAL PRADESH", "04": "ASSAM", "05": "BIHAR", "06": "CHANDIGARH",
	"07": "DADRA & NAGAR HAVELI AND DAMAN & DIU", "09": "DELHI", "10": "GOA",
	"11": "GUJARAT", "12": "HARYANA", "13": "HIMACHAL PRADESH",
	"14": "JAMMU & KASHMIR", "15": "KARNATAKA", "16": "KERALA",
	"17": "LAKSHWADEEP", "18": "MADHYA PRADESH", "19": "MAHARASHTRA",
	"20": "MANIPUR", "21": "MEGHALAYA", "22": "MIZORAM", "23": "NAGALAND",
	"24": "ODISHA", "25": "PONDICHERRY", "26": "PUNJAB", "27": "RAJASTHAN",
	"28": "SIKKIM", "29": "TAMIL NADU", "30": "TRIPURA", "31": "UTTAR PRADESH",
	"32": "WEST BENGAL", "33": "CHHATTISGARH", "34": "UTTARAKHAND",
	"35": "JHARKHAND", "36": "TELANGANA", "37": "LADAKH",
}

// ---------------------------------------------------------------------------
// Annexure 7 — minor head of challan
// ---------------------------------------------------------------------------

// MinorHeads maps the challan's minor head code to its meaning.
//
// 200 is the ordinary one — tax the deductor pays over of its own motion. 400
// is a demand raised by the department on assessment. 100 is Advance Tax, and
// the workbook attaches an extra rule to it (CD field 19 must fall within the
// quarter, and it must not be later than the payment date of any DD record
// mapped to it) which challanRules enforces.
var MinorHeads = map[string]string{
	"200": "TDS payable by taxpayer",
	"400": "TDS regular assessment (raised by the Income Tax Department)",
	"100": "Advance Tax",
}
