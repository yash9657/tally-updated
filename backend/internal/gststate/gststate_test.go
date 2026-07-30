package gststate_test

// gststate_test.go — THE NAME ON THE FACE OF A TAX INVOICE.
//
// What these tests protect, and what it costs when they break:
//
//  1. A WRONG STATE NAME IS READ DIRECTLY BY A GST OFFICER. Rule 46(n) puts the
//     place of supply "along with the name of the State" on every inter-State
//     invoice, so a table that maps 29 to Kerala prints Kerala on a Karnataka
//     supply — and the document then contradicts the IGST it charges. The
//     expected names below are written out INDEPENDENTLY, one per code, and are
//     deliberately not derived from the package's own table: a table generated
//     from a source and asserted against the same source proves nothing at all.
//
//  2. A CODE THIS TABLE CANNOT STAND BEHIND MUST NOT BE NAMED. 25 and 28 denote
//     no State that can be supplied to today, and 96/99 could not be verified
//     from this environment. Every one of them must answer "no", because the
//     alternative is a plausible name on a statutory document — the one failure
//     mode nobody downstream can detect.
//
//  3. THE MERGER AND THE BIFURCATION HAVE A DIRECTION. 25/26 and 28/36/37 are
//     the pairs it is easiest to get backwards, and getting them backwards is
//     invisible: "Daman and Diu" printed against code 26 looks like a name to
//     everyone who does not know that the Union territory was merged in 2020. So
//     the direction is pinned in both directions rather than spot-checked.

import (
	"strings"
	"testing"
	"time"

	"github.com/khata/cored/internal/gststate"
)

// wantCurrent is the INDEPENDENT expectation: the two-digit GST State code and
// the name a tax invoice must print for it. Written out here by hand so that a
// typo, a transposition or a stale post-2019 entry in the package's table shows
// up as a named failure rather than as agreement with itself.
//
// SOURCE AND EFFECTIVE DATE. These are the Census of India 2011 State/UT codes
// adopted by GSTN as the first two digits of a GSTIN; the requirement to print
// the name alongside the code is CGST Rules 2017, Rule 46(e) and Rule 46(n), in
// force from 2017-07-01. Four entries changed after that date, by the Andhra
// Pradesh Reorganisation Act 2014 (in force before GST began), the Jammu and
// Kashmir Reorganisation Act 2019 (appointed day 2019-10-31) and the Dadra and
// Nagar Haveli and Daman and Diu (Merger of Union Territories) Act 2019
// (appointed day 2020-01-26).
//
// NOT VERIFIED AGAINST A PUBLISHED LIST FROM THIS ENVIRONMENT: the GST portal,
// the NIC e-invoice API and the FVU cannot be reached from here. This is the
// same limitation the package header states, and it is why the codes that could
// not be stated with confidence (96, 99) are asserted to be REFUSED below rather
// than asserted to have a name.
var wantCurrent = map[string]string{
	"01": "Jammu and Kashmir",
	"02": "Himachal Pradesh",
	"03": "Punjab",
	"04": "Chandigarh",
	"05": "Uttarakhand",
	"06": "Haryana",
	"07": "Delhi",
	"08": "Rajasthan",
	"09": "Uttar Pradesh",
	"10": "Bihar",
	"11": "Sikkim",
	"12": "Arunachal Pradesh",
	"13": "Nagaland",
	"14": "Manipur",
	"15": "Mizoram",
	"16": "Tripura",
	"17": "Meghalaya",
	"18": "Assam",
	"19": "West Bengal",
	"20": "Jharkhand",
	"21": "Odisha",
	"22": "Chhattisgarh",
	"23": "Madhya Pradesh",
	"24": "Gujarat",
	// 25 is RETIRED, not current: see TestAnInvoiceNamesADamanAndDiuThatNoLongerExists.
	"26": "Dadra and Nagar Haveli and Daman and Diu",
	"27": "Maharashtra",
	// 28 is RETIRED, not current: see TestAnInvoiceNamesThePreBifurcationAndhraPradesh.
	"29": "Karnataka",
	"30": "Goa",
	"31": "Lakshadweep",
	"32": "Kerala",
	"33": "Tamil Nadu",
	"34": "Puducherry",
	"35": "Andaman and Nicobar Islands",
	"36": "Telangana",
	"37": "Andhra Pradesh",
	"38": "Ladakh",
	"97": "Other Territory",
}

// TestAnInvoicePrintsAStateNameThatIsNotTheStateSupplied is named for the
// consequence: an invoice whose place of supply reads "Kerala" on a supply to
// Karnataka states the wrong State's revenue on a statutory document, and the
// recipient's input tax credit hangs on that line.
//
// Both directions of every claim: Name answers, and Lookup agrees with Name.
func TestAnInvoicePrintsAStateNameThatIsNotTheStateSupplied(t *testing.T) {
	for code, want := range wantCurrent {
		got, ok := Name(t, code)
		if !ok {
			t.Errorf("state code %s cannot be named, so every invoice whose place of supply is %s "+
				"(%s) is refused and cannot be issued at all", code, code, want)
			continue
		}
		if got != want {
			t.Errorf("state code %s is named %q; the State is %q, and a tax invoice stating the "+
				"wrong State's name contradicts the tax it charges", code, got, want)
		}
		e, known := gststate.Lookup(code)
		if !known {
			t.Errorf("Lookup does not know state code %s, though Name returns %q for it: the two "+
				"halves of the table disagree", code, got)
			continue
		}
		if e.Name != want || e.Status != gststate.StatusCurrent {
			t.Errorf("Lookup(%s) returns name %q status %q; want %q and %q",
				code, e.Name, e.Status, want, gststate.StatusCurrent)
		}
		if e.Code != code {
			t.Errorf("Lookup(%s) returns an entry keyed %q: an entry that does not carry its own "+
				"code cannot be printed as \"State: X (code)\"", code, e.Code)
		}
	}
}

// TestTheStateCodeTableHasAHoleInTheMiddleOfIt is named for the failure that
// would otherwise be silent: a code missing from the table refuses every invoice
// whose place of supply is that State, and nothing about a missing map key says
// so. Codes 01 to 38 are contiguous — every one of them denotes a State or Union
// territory or is one of the two explicitly retired codes — so a gap is a defect.
func TestTheStateCodeTableHasAHoleInTheMiddleOfIt(t *testing.T) {
	for _, code := range twoDigitRange(1, 38) {
		if _, known := gststate.Lookup(code); !known {
			t.Errorf("state code %s is absent from the table: it is one of the contiguous 01..38 "+
				"State/UT codes, and a party or a place of supply carrying it can print nothing",
				code)
		}
	}
	// And the reverse: nothing OUTSIDE 01..38 plus the three special codes may be
	// in the table, or the table is asserting a code the GSTIN format has no room
	// for.
	allowed := map[string]bool{"96": true, "97": true, "99": true}
	for _, code := range twoDigitRange(1, 38) {
		allowed[code] = true
	}
	for _, e := range gststate.All() {
		if !allowed[e.Code] {
			t.Errorf("the table carries code %q (%q), which is neither one of the 01..38 State/UT "+
				"codes nor one of the special place-of-supply codes 96/97/99", e.Code, e.Name)
		}
	}
}

// TestAnInvoiceNamesADamanAndDiuThatNoLongerExists pins the DIRECTION of the
// 2020 merger, which is the single easiest thing in this table to get backwards.
//
// The Dadra and Nagar Haveli and Daman and Diu (Merger of Union Territories) Act
// 2019 merged the two Union territories with effect from 2020-01-26. The
// SURVIVING code is 26 and it denotes the merged Union territory; 25 was retired
// and its registrations were migrated to 26. Backwards, this table would print
// "Daman and Diu" for a supply to the merged UT and refuse the code that is
// actually in use — and both faults reach paper.
func TestAnInvoiceNamesADamanAndDiuThatNoLongerExists(t *testing.T) {
	name, ok := Name(t, "26")
	if !ok {
		t.Fatal("state code 26 cannot be named, so no invoice to the merged Union territory of " +
			"Dadra and Nagar Haveli and Daman and Diu can be printed at all")
	}
	for _, part := range []string{"Dadra and Nagar Haveli", "Daman and Diu"} {
		if !strings.Contains(name, part) {
			t.Errorf("code 26 is named %q, which does not contain %q: after the 2020 merger the "+
				"surviving Union territory is named for BOTH halves, and an invoice naming one of "+
				"them names a Union territory that no longer exists", name, part)
		}
	}

	if got, ok := gststate.Name("25"); ok {
		t.Errorf("code 25 is named %q; the Union territory of Daman and Diu ceased to exist on "+
			"2020-01-26 and its code was retired, so naming it prints a place of supply that no "+
			"longer exists on an invoice issued today", got)
	}
	e, known := gststate.Lookup("25")
	if !known || e.Status != gststate.StatusRetired {
		t.Fatalf("Lookup(25) reports known=%v status=%q; a retired code must be RETIRED and not "+
			"unknown, so the refusal can tell an operator to correct the master instead of "+
			"telling them 25 is not a code at all", known, e.Status)
	}
	if !strings.Contains(gststate.Explain("25"), "26") {
		t.Errorf("the explanation for the retired code 25 is %q and does not name 26: an operator "+
			"told only that the code is retired has nothing to correct it TO",
			gststate.Explain("25"))
	}
	if e.Since != "2020-01-26" {
		t.Errorf("code 25's retirement is dated %q; the appointed day under the Merger Act is "+
			"2020-01-26, and a statutory entry without its effective date cannot be checked",
			e.Since)
	}
}

// TestAnInvoiceNamesThePreBifurcationAndhraPradesh pins the other trap: the
// Andhra Pradesh / Telangana codes.
//
// Telangana was created by the Andhra Pradesh Reorganisation Act 2014 with
// effect from 2014-06-02 — three years BEFORE GST. Under GST, Telangana is 36
// and the successor Andhra Pradesh is 37; the pre-bifurcation code 28 denotes no
// State that can be supplied to and no GSTIN issued under GST begins with it.
// Swapping 36 and 37, or naming 28 "Andhra Pradesh", puts the wrong State on an
// inter-State invoice between two States that share a border and a language.
func TestAnInvoiceNamesThePreBifurcationAndhraPradesh(t *testing.T) {
	if got, ok := Name(t, "36"); !ok || got != "Telangana" {
		t.Errorf("code 36 is named %q (ok=%v); under GST 36 is Telangana", got, ok)
	}
	if got, ok := Name(t, "37"); !ok || got != "Andhra Pradesh" {
		t.Errorf("code 37 is named %q (ok=%v); under GST 37 is the successor Andhra Pradesh", got, ok)
	}
	if got, ok := gststate.Name("28"); ok {
		t.Errorf("code 28 is named %q; 28 is the PRE-BIFURCATION Andhra Pradesh code, it denotes "+
			"no State that can be supplied to today, and printing it would state a place of supply "+
			"that has not existed since 2014", got)
	}
	e, known := gststate.Lookup("28")
	if !known || e.Status != gststate.StatusRetired {
		t.Fatalf("Lookup(28) reports known=%v status=%q, want a RETIRED entry so the refusal can "+
			"say what to correct the master to", known, e.Status)
	}
	explain := gststate.Explain("28")
	if !strings.Contains(explain, "36") || !strings.Contains(explain, "37") {
		t.Errorf("the explanation for code 28 is %q; it must name BOTH successors (36 Telangana "+
			"and 37 Andhra Pradesh), because which one is right depends on where the party is",
			explain)
	}
}

// TestAnInvoiceNamesALadakhThatWasStillJammuAndKashmir pins the 2019
// reorganisation: Ladakh took the NEW code 38 and code 01 kept its name.
//
// The failure this guards is the plausible-looking one — moving Jammu and
// Kashmir to 38 and giving 01 to Ladakh, or renaming 01 — which would misstate
// the place of supply for every supply into either Union territory.
func TestAnInvoiceNamesALadakhThatWasStillJammuAndKashmir(t *testing.T) {
	if got, ok := Name(t, "38"); !ok || got != "Ladakh" {
		t.Errorf("code 38 is named %q (ok=%v); the Jammu and Kashmir Reorganisation Act 2019 gave "+
			"Ladakh the NEW code 38", got, ok)
	}
	if got, ok := Name(t, "01"); !ok || got != "Jammu and Kashmir" {
		t.Errorf("code 01 is named %q (ok=%v); the reorganisation changed the STATUS of Jammu and "+
			"Kashmir and not its name, and 01 still denotes it", got, ok)
	}
	for _, code := range []string{"01", "38"} {
		e, _ := gststate.Lookup(code)
		if e.Since != "2019-10-31" {
			t.Errorf("code %s carries the effective date %q; the appointed day under the Jammu and "+
				"Kashmir Reorganisation Act 2019 is 2019-10-31, and an undated statutory entry "+
				"cannot be checked against the gazette", code, e.Since)
		}
	}
}

// TestAnExportInvoicePrintsAPlaceOfSupplyThisTableInvented is named for the
// breach it prevents. Codes 96 and 99 are BELIEVED to appear in published
// place-of-supply lists ("Other Country", "Centre Jurisdiction") and neither the
// code, the wording nor the date could be verified from this environment — so
// they must be refused, not guessed. A refusal costs an operator an export
// invoice today; a guess puts a fabricated place of supply on a document a GST
// officer reads.
func TestAnExportInvoicePrintsAPlaceOfSupplyThisTableInvented(t *testing.T) {
	for _, code := range []string{"96", "99"} {
		if got, ok := gststate.Name(code); ok {
			t.Errorf("code %s is named %q, but its meaning could not be verified from this "+
				"environment: a tax invoice would then state a place of supply this codebase "+
				"cannot stand behind", code, got)
		}
		e, known := gststate.Lookup(code)
		if !known || e.Status != gststate.StatusUnverified {
			t.Errorf("Lookup(%s) reports known=%v status=%q; an unverifiable code must be marked "+
				"UNVERIFIED, which is a different sentence from \"that is not a code\"",
				code, known, e.Status)
		}
		if e.Name != "" {
			t.Errorf("the unverified code %s carries the name %q in its entry; a name sitting in "+
				"the table is a name something will eventually print", code, e.Name)
		}
		if !strings.Contains(e.Authority+" "+e.Note, "UNVERIFIED") {
			t.Errorf("code %s is not marked UNVERIFIED in its authority (%q) or note (%q); the "+
				"marker is what stops the entry being read as reference data",
				code, e.Authority, e.Note)
		}
		if !strings.Contains(strings.ToLower(gststate.Explain(code)), "cannot be named") {
			t.Errorf("Explain(%s) is %q; it must say the code cannot be named, or the operator "+
				"reads the refusal as a bug in the software", code, gststate.Explain(code))
		}
	}
}

// TestEveryEntryStatesTheAuthorityAndTheDateBehindIt enforces the citation
// discipline this table is held to: statutory reference data with no source and
// no effective date cannot be checked by the next person, and an unverifiable
// entry is exactly how a wrong rate or a wrong name survives a review.
func TestEveryEntryStatesTheAuthorityAndTheDateBehindIt(t *testing.T) {
	entries := gststate.All()
	if len(entries) == 0 {
		t.Fatal("the table is empty, so no state name can be printed at all")
	}
	for _, e := range entries {
		if strings.TrimSpace(e.Authority) == "" {
			t.Errorf("code %s (%q) cites no authority: an uncited statutory entry cannot be "+
				"checked and cannot be corrected with confidence", e.Code, e.Name)
		}
		switch e.Status {
		case gststate.StatusCurrent, gststate.StatusRetired:
			if _, err := time.Parse("2006-01-02", e.Since); err != nil {
				t.Errorf("code %s (%q) carries the effective-from date %q, which is not an ISO "+
					"date: a name without a date cannot be applied to a document by its date",
					e.Code, e.Name, e.Since)
			}
			if e.Name == "" {
				t.Errorf("code %s has status %q and no name, so nothing can be printed or "+
					"explained for it", e.Code, e.Status)
			}
		case gststate.StatusUnverified:
			if e.Note == "" {
				t.Errorf("the unverified code %s records no note saying what could not be "+
					"verified", e.Code)
			}
		default:
			t.Errorf("code %s carries the status %q, which is not one of the four the package "+
				"declares", e.Code, e.Status)
		}
	}
}

// TestNameAnswersForACodeItHasNoRightToName is the invariant that makes the
// refusals above structural rather than incidental: Name answers if and only if
// the entry is CURRENT. A retired or unverified entry that Name answered for
// would put a stale or unverified name on paper, and there is nothing further
// downstream that could catch it.
func TestNameAnswersForACodeItHasNoRightToName(t *testing.T) {
	for _, e := range gststate.All() {
		got, ok := gststate.Name(e.Code)
		wantOK := e.Status == gststate.StatusCurrent
		if ok != wantOK {
			t.Errorf("Name(%s) answered=%v for an entry with status %q (name %q): only a CURRENT "+
				"code may be named", e.Code, ok, e.Status, e.Name)
		}
		if ok && got != e.Name {
			t.Errorf("Name(%s) returns %q but the entry holds %q", e.Code, got, e.Name)
		}
		if !ok && got != "" {
			t.Errorf("Name(%s) refused and still returned %q, which a caller ignoring the second "+
				"return value would print", e.Code, got)
		}
	}
}

// TestAHalfTypedStateCodePrintsSomebodyElsesState is named for the breach, and
// the breach is what a lenient normaliser buys: zero-padding "9" to "09" prints
// UTTAR PRADESH as the place of supply on the strength of one keystroke, and
// nothing on the document says a digit was invented.
//
// Trimming surrounding whitespace IS accepted — a state code pasted out of a
// spreadsheet arrives with a trailing space and that is not ambiguous.
func TestAHalfTypedStateCodePrintsSomebodyElsesState(t *testing.T) {
	for _, in := range []string{" 29 ", "\t29", "29\n", "  29\r\n"} {
		got, ok := gststate.Name(in)
		if !ok || got != "Karnataka" {
			t.Errorf("Name(%q) returned %q, ok=%v; surrounding whitespace on a pasted code is not "+
				"ambiguous and must not refuse a supply to Karnataka", in, got, ok)
		}
	}
	for _, in := range []string{
		"",    // nothing keyed at all
		"   ", // whitespace only
		"9",   // half typed: must NOT become 09 Uttar Pradesh
		"7",   // half typed: must NOT become 07 Delhi
		"029", // three digits: must NOT be truncated to 02 or 29
		"2 9", // an interior space is not whitespace around a code
		"1o",  // a letter o typed for a zero
		"2A",  // an upper-case letter where a digit belongs
		"2a",  // and its lower-case twin
		"KA",  // the vehicle-registration code, not the GST code
		"karnataka",
		"-1",
		"+9",
		"２９", // full-width digits: not two ASCII digits
	} {
		if got, ok := gststate.Name(in); ok {
			t.Errorf("Name(%q) named the State %q: a value that is not exactly two ASCII digits is "+
				"not a GST State code, and inferring one puts a State nobody keyed on an invoice",
				in, got)
		}
		e, known := gststate.Lookup(in)
		if known {
			t.Errorf("Lookup(%q) claims to know the code and returned %+v", in, e)
		}
		if e.Status != gststate.StatusUnknown {
			t.Errorf("Lookup(%q) returns status %q, want %q", in, e.Status, gststate.StatusUnknown)
		}
		if strings.TrimSpace(gststate.Explain(in)) == "" {
			t.Errorf("Explain(%q) is blank; a refusal with no sentence in it reaches an operator "+
				"as an empty error", in)
		}
	}
}

// TestAllHandsOutTheTableItselfForAnyoneToRewrite guards the reference data
// against a caller — a report, a UI, a test helper — mutating the entry it was
// handed. This table is statutory data held in a package-level map: if All
// returned the map's own values by reference, one careless assignment anywhere in
// the process would change what every subsequent tax invoice says about a State.
func TestAllHandsOutTheTableItselfForAnyoneToRewrite(t *testing.T) {
	first := gststate.All()
	for i := range first {
		first[i].Name = "Vandalised"
		first[i].Status = gststate.StatusUnverified
	}
	if got, ok := gststate.Name("29"); !ok || got != "Karnataka" {
		t.Fatalf("after a caller overwrote the slice All returned, code 29 names %q (ok=%v): the "+
			"statutory table is writable from anywhere in the process", got, ok)
	}
	second := gststate.All()
	if len(second) != len(first) {
		t.Fatalf("All returned %d entries and then %d", len(first), len(second))
	}
	// And it is ORDERED, so anything printing the table (a settings screen, a
	// report) is stable between calls.
	for i := 1; i < len(second); i++ {
		if second[i-1].Code >= second[i].Code {
			t.Fatalf("All is not ordered by code: %q precedes %q", second[i-1].Code, second[i].Code)
		}
	}
	if second[0].Name == "Vandalised" {
		t.Fatal("All returned the vandalised entries a previous caller wrote into")
	}
}

// --- helpers ----------------------------------------------------------------

// Name wraps gststate.Name for the assertions above. It exists only so a
// failure inside a loop reports the code it was asked about.
func Name(t *testing.T, code string) (string, bool) {
	t.Helper()
	return gststate.Name(code)
}

// twoDigitRange lists the zero-padded codes from lo to hi inclusive.
func twoDigitRange(lo, hi int) []string {
	out := make([]string, 0, hi-lo+1)
	for i := lo; i <= hi; i++ {
		out = append(out, string(rune('0'+i/10))+string(rune('0'+i%10)))
	}
	return out
}
