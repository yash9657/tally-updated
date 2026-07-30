// Package gststate turns a two-digit GST State code into the NAME of the State
// or Union territory it denotes.
//
// WHY THIS PACKAGE EXISTS. This codebase works entirely in codes: company and
// ledger store state_code, voucher_version stores place_of_supply, and
// internal/compliance decides intra-versus-inter-State by comparing two codes.
// That is enough to derive the tax and to file a return, and it is NOT enough to
// print a tax invoice: Rule 46(n) of the CGST Rules 2017 requires the "place of
// supply along with the name of the State" on an inter-State supply, and Rule
// 46(e) requires the recipient's "name of the State and its code" on a supply of
// fifty thousand rupees or more to an unregistered person. Without a code-to-name
// map the print path could only refuse, and it did — every IGST sale answered 4xx
// with no field an operator could fill in, because there is no field: the name is
// not data anybody keys, it is a property of the code.
//
// So this is STATUTORY REFERENCE DATA, and it is held to the same standard as a
// rate or a threshold: every entry carries a source and an effective-from date,
// and anything that could not be verified from this environment is marked and is
// NOT answered. A wrong State name prints a wrong place of supply on the face of
// a tax invoice, which is a defect a GST officer reads directly and a defect the
// recipient's input tax credit hangs on — so a refusal that names the code beats
// a plausible guess, every time.
//
// SOURCE AND EFFECTIVE DATE
// -------------------------
// The State/UT codes used under GST are the Census of India 2011 State/UT codes,
// adopted as the first two digits of a GSTIN. The list is published by GSTN on
// the GST portal and reproduced in the NIC e-invoice and e-way-bill master codes.
// The requirement to state the name of the State ALONGSIDE the code comes from
// Rule 46(e) and Rule 46(n) of the Central Goods and Services Tax Rules 2017,
// notified by Notification No. 3/2017-Central Tax dated 19 June 2017, in force
// from 1 July 2017 — which is the effective-from date of the table below except
// where an entry says otherwise.
//
// SEVEN codes carry their OWN authority and date below rather than the shared
// citation, in three groups. Two of the groups were changed by Parliament AFTER
// GST began; the third predates GST by three years and is listed because it is
// the group most often got backwards, not because it changed under GST:
//
//   - 38 Ladakh, and 01 Jammu and Kashmir. The Jammu and Kashmir Reorganisation
//     Act, 2019 (Act 34 of 2019) split the State of Jammu and Kashmir into two
//     Union territories with effect from the appointed day, 31 October 2019.
//     Ladakh took the NEW code 38; code 01 continued to denote Jammu and Kashmir,
//     whose NAME did not change (only its status, from State to Union territory,
//     which this table does not record).
//   - 26 Dadra and Nagar Haveli and Daman and Diu, and 25, retired. The Dadra and
//     Nagar Haveli and Daman and Diu (Merger of Union Territories) Act, 2019 (Act
//     44 of 2019) merged the two Union territories into one, named "Dadra and
//     Nagar Haveli and Daman and Diu", with effect from the appointed day,
//     26 January 2020. Code 26 (formerly Dadra and Nagar Haveli) denotes the
//     merged Union territory; code 25 (formerly Daman and Diu) was retired and
//     registrations under it were migrated to 26.
//   - 36 Telangana and 37 Andhra Pradesh, with 28 not in use. The Andhra Pradesh
//     Reorganisation Act, 2014 (Act 6 of 2014) created Telangana with effect from
//     the appointed day, 2 June 2014 — three years BEFORE GST. Under GST,
//     Telangana is 36 and Andhra Pradesh is 37; the pre-bifurcation Andhra Pradesh
//     code 28 appears in published lists only as a historical entry ("Andhra
//     Pradesh (before division)") and denotes no State that can be supplied to
//     today.
//
// WHAT COULD NOT BE VERIFIED FROM THIS ENVIRONMENT, STATED PLAINLY
// ---------------------------------------------------------------
// The GST portal, the NIC e-invoice API and the FVU CANNOT be reached from here,
// so NOTHING in this table was checked against a published list at the time of
// writing. It is reproduced from knowledge of that list, and the entries most
// likely to be wrong are the ones deliberately NOT answered:
//
//   - 96 and 99. Published place-of-supply code lists carry entries beyond 97 —
//     "Other Country" is believed to be 96 and "Centre Jurisdiction" is believed
//     to be 99 — but neither the code, the wording nor the date they came into use
//     could be verified here, so Name refuses both and Explain says why. They are
//     in the table with StatusUnverified rather than omitted, because "the code
//     could not be verified" is a different sentence from "that is not a code",
//     and an operator staring at an export invoice deserves the first one.
//   - The exact date GSTN began issuing 26 for the merged Union territory, and
//     stopped accepting 25. The Merger Act's appointed day (26 January 2020) is
//     recorded as the effective date of the NAME; the portal's own migration
//     window is not modelled, so a pre-2020 document that legitimately carried
//     Daman and Diu under code 25 gets a refusal rather than its historical name.
//     Deliberate: see the comment on Name.
//   - Whether a State/UT designation belongs in this table at all. Union
//     territory tax (UTGST) applies instead of State tax for some of these codes,
//     which matters to the tax heads on an invoice — but that determination lives
//     in internal/compliance, from the codes, and duplicating it here as a flag
//     nothing reads would be an unverified claim with no test behind it.
//
// The table carries names ONLY. No rate, no threshold, no jurisdiction, no
// UTGST/SGST determination.
//
// STDLIB ONLY, NO SIBLING IMPORT. This is a pure leaf: no database, no I/O, no
// internal import (internal/archtest declares its allowed set as the empty set).
// A code-to-name map that could read the database would be a map that answers
// differently per company, and the name of a State is not a per-company fact.
package gststate

import (
	"fmt"
	"sort"
	"strings"
)

// Status says what a code IS, which is not the same question as what it is
// called. Three of the four values carry no printable name, and each is a
// different sentence to an operator.
type Status string

const (
	// StatusUnknown: the table has no such code. Not a GST State code.
	StatusUnknown Status = "unknown"
	// StatusCurrent: a code in use today, whose name may be printed.
	StatusCurrent Status = "current"
	// StatusRetired: a code that once denoted a State or Union territory and no
	// longer does. It has a historical name, which Name deliberately does not
	// return — see Name.
	StatusRetired Status = "retired"
	// StatusUnverified: a code believed to appear in published lists whose
	// meaning could not be verified from this environment. Never named.
	StatusUnverified Status = "unverified"
)

// Entry is one row of the table.
type Entry struct {
	// Code is the two-digit code, zero-padded ("07", not "7").
	Code string
	// Name is the name of the State or Union territory, spelled as the statute
	// that named it spells it. For a retired code it is the HISTORICAL name and
	// Name does not return it; for an unverified code it is empty and the belief
	// is in Note instead, so that nothing can mistake a guess for a name.
	Name string
	// Status decides whether Name answers for this code.
	Status Status
	// Since is the ISO date from which this code carries this name, or "" where
	// it has carried it since GST began on 2017-07-01.
	Since string
	// Authority cites the instrument that made it so.
	Authority string
	// Note explains a retirement or records what could not be verified. It ends
	// up in front of an operator, through Explain.
	Note string
}

// gstStart is the effective-from date of the whole table except where an entry
// says otherwise: the day the CGST Rules 2017 came into force.
const gstStart = "2017-07-01"

// censusList is the citation shared by every entry that has not changed since
// GST began.
const censusList = "Census of India 2011 State/UT codes, adopted by GSTN as the first two digits of a GSTIN " +
	"and published in the GST portal's State code list; the requirement to state the name of the State " +
	"alongside its code is CGST Rules 2017, Rule 46(e) and Rule 46(n)"

// table is the code-to-name map. ONE ENTRY PER CODE, each with the source and the
// date behind it; the four post-2017 changes and the two unverified codes carry
// their own citations rather than the shared one.
var table = map[string]Entry{
	"01": {Code: "01", Name: "Jammu and Kashmir", Status: StatusCurrent, Since: "2019-10-31",
		Authority: "Jammu and Kashmir Reorganisation Act, 2019 (Act 34 of 2019), appointed day 31 October 2019",
		Note: "the NAME did not change on reorganisation: code 01 denoted the State of Jammu and Kashmir " +
			"from " + gstStart + " and denotes the Union territory of the same name from the appointed day; " +
			"Ladakh was carved out under the new code 38"},
	"02": {Code: "02", Name: "Himachal Pradesh", Status: StatusCurrent, Since: gstStart, Authority: censusList},
	"03": {Code: "03", Name: "Punjab", Status: StatusCurrent, Since: gstStart, Authority: censusList},
	"04": {Code: "04", Name: "Chandigarh", Status: StatusCurrent, Since: gstStart, Authority: censusList},
	"05": {Code: "05", Name: "Uttarakhand", Status: StatusCurrent, Since: gstStart, Authority: censusList},
	"06": {Code: "06", Name: "Haryana", Status: StatusCurrent, Since: gstStart, Authority: censusList},
	"07": {Code: "07", Name: "Delhi", Status: StatusCurrent, Since: gstStart, Authority: censusList},
	"08": {Code: "08", Name: "Rajasthan", Status: StatusCurrent, Since: gstStart, Authority: censusList},
	"09": {Code: "09", Name: "Uttar Pradesh", Status: StatusCurrent, Since: gstStart, Authority: censusList},
	"10": {Code: "10", Name: "Bihar", Status: StatusCurrent, Since: gstStart, Authority: censusList},
	"11": {Code: "11", Name: "Sikkim", Status: StatusCurrent, Since: gstStart, Authority: censusList},
	"12": {Code: "12", Name: "Arunachal Pradesh", Status: StatusCurrent, Since: gstStart, Authority: censusList},
	"13": {Code: "13", Name: "Nagaland", Status: StatusCurrent, Since: gstStart, Authority: censusList},
	"14": {Code: "14", Name: "Manipur", Status: StatusCurrent, Since: gstStart, Authority: censusList},
	"15": {Code: "15", Name: "Mizoram", Status: StatusCurrent, Since: gstStart, Authority: censusList},
	"16": {Code: "16", Name: "Tripura", Status: StatusCurrent, Since: gstStart, Authority: censusList},
	"17": {Code: "17", Name: "Meghalaya", Status: StatusCurrent, Since: gstStart, Authority: censusList},
	"18": {Code: "18", Name: "Assam", Status: StatusCurrent, Since: gstStart, Authority: censusList},
	"19": {Code: "19", Name: "West Bengal", Status: StatusCurrent, Since: gstStart, Authority: censusList},
	"20": {Code: "20", Name: "Jharkhand", Status: StatusCurrent, Since: gstStart, Authority: censusList},
	"21": {Code: "21", Name: "Odisha", Status: StatusCurrent, Since: gstStart, Authority: censusList},
	"22": {Code: "22", Name: "Chhattisgarh", Status: StatusCurrent, Since: gstStart, Authority: censusList},
	"23": {Code: "23", Name: "Madhya Pradesh", Status: StatusCurrent, Since: gstStart, Authority: censusList},
	"24": {Code: "24", Name: "Gujarat", Status: StatusCurrent, Since: gstStart, Authority: censusList},

	// 25 AND 26 ARE THE PAIR IT IS EASIEST TO GET BACKWARDS, so the direction is
	// spelled out in both entries: the SURVIVING code is 26 and the RETIRED code
	// is 25.
	"25": {Code: "25", Name: "Daman and Diu", Status: StatusRetired, Since: "2020-01-26",
		Authority: "Dadra and Nagar Haveli and Daman and Diu (Merger of Union Territories) Act, 2019 " +
			"(Act 44 of 2019), appointed day 26 January 2020",
		Note: "the Union territory of Daman and Diu ceased to exist on the appointed day: it merged with " +
			"Dadra and Nagar Haveli into a single Union territory, which is code 26. Registrations under " +
			"25 were migrated to 26. A document dated before the merger legitimately carried Daman and " +
			"Diu under 25, but this table does not name a retired code — correct the master to 26"},
	"26": {Code: "26", Name: "Dadra and Nagar Haveli and Daman and Diu", Status: StatusCurrent, Since: "2020-01-26",
		Authority: "Dadra and Nagar Haveli and Daman and Diu (Merger of Union Territories) Act, 2019 " +
			"(Act 44 of 2019), appointed day 26 January 2020",
		Note: "code 26 denoted Dadra and Nagar Haveli from " + gstStart + " and denotes the MERGED Union " +
			"territory from the appointed day; the retired code is 25"},

	"27": {Code: "27", Name: "Maharashtra", Status: StatusCurrent, Since: gstStart, Authority: censusList},

	// 28 IS THE OTHER TRAP. It is the pre-bifurcation Andhra Pradesh code and it
	// denotes nothing that can be supplied to under GST: Telangana is 36 and
	// Andhra Pradesh is 37, and both were already so when GST began.
	"28": {Code: "28", Name: "Andhra Pradesh (before division)", Status: StatusRetired, Since: "2014-06-02",
		Authority: "Andhra Pradesh Reorganisation Act, 2014 (Act 6 of 2014), appointed day 2 June 2014",
		Note: "the State of Andhra Pradesh was bifurcated three years before GST: Telangana is code 36 " +
			"and the successor Andhra Pradesh is code 37. Code 28 appears in published lists only as a " +
			"historical entry and no GSTIN issued under GST begins with it — correct the master to 36 or 37"},

	"29": {Code: "29", Name: "Karnataka", Status: StatusCurrent, Since: gstStart, Authority: censusList},
	"30": {Code: "30", Name: "Goa", Status: StatusCurrent, Since: gstStart, Authority: censusList},
	"31": {Code: "31", Name: "Lakshadweep", Status: StatusCurrent, Since: gstStart, Authority: censusList},
	"32": {Code: "32", Name: "Kerala", Status: StatusCurrent, Since: gstStart, Authority: censusList},
	"33": {Code: "33", Name: "Tamil Nadu", Status: StatusCurrent, Since: gstStart, Authority: censusList},
	"34": {Code: "34", Name: "Puducherry", Status: StatusCurrent, Since: gstStart, Authority: censusList},
	"35": {Code: "35", Name: "Andaman and Nicobar Islands", Status: StatusCurrent, Since: gstStart, Authority: censusList},
	"36": {Code: "36", Name: "Telangana", Status: StatusCurrent, Since: gstStart,
		Authority: "Andhra Pradesh Reorganisation Act, 2014 (Act 6 of 2014), appointed day 2 June 2014; " +
			"in use under GST from " + gstStart,
		Note: "Telangana is 36 and Andhra Pradesh is 37; the pre-bifurcation code 28 is retired"},
	"37": {Code: "37", Name: "Andhra Pradesh", Status: StatusCurrent, Since: gstStart,
		Authority: "Andhra Pradesh Reorganisation Act, 2014 (Act 6 of 2014), appointed day 2 June 2014; " +
			"in use under GST from " + gstStart,
		Note: "the successor State keeps the name Andhra Pradesh under the NEW code 37; 28 is retired"},
	"38": {Code: "38", Name: "Ladakh", Status: StatusCurrent, Since: "2019-10-31",
		Authority: "Jammu and Kashmir Reorganisation Act, 2019 (Act 34 of 2019), appointed day 31 October 2019",
		Note: "Ladakh was carved out of Jammu and Kashmir under a NEW code; code 01 continues to denote " +
			"Jammu and Kashmir"},

	"97": {Code: "97", Name: "Other Territory", Status: StatusCurrent, Since: gstStart,
		Authority: censusList + "; \"Other Territory\" is the place-of-supply entry for territory outside " +
			"any State or Union territory (for example the exclusive economic zone), and it is the one " +
			"non-geographic code in the published State code list that could be stated with confidence here",
		Note: "not a State: it is what the published list calls the residual territory, and it prints as " +
			"that name because that is the name a return is filed against"},

	// THE TWO CODES THIS PACKAGE REFUSES TO NAME. See the header.
	"96": {Code: "96", Status: StatusUnverified,
		Authority: "UNVERIFIED — believed to appear in the NIC e-invoice / e-way-bill place-of-supply " +
			"master codes; not checked against any published list from this environment",
		Note: "code 96 is BELIEVED to mean \"Other Country\" (the place of supply on an export), but " +
			"neither the code, the wording nor the date it came into use could be verified here, so no " +
			"name is stated for it. An export invoice therefore refuses to print rather than asserting a " +
			"place of supply this table cannot stand behind"},
	"99": {Code: "99", Status: StatusUnverified,
		Authority: "UNVERIFIED — believed to appear in the GST portal's State code list; not checked " +
			"against any published list from this environment",
		Note: "code 99 is BELIEVED to mean \"Centre Jurisdiction\", which is a jurisdiction and not a " +
			"State at all; it could not be verified here and it is not named"},
}

// Name returns the name of the State or Union territory a code denotes, and
// whether the code is one this table will name.
//
// IT ANSWERS FOR CURRENT CODES ONLY, and that is the whole safety property. A
// retired code (25, 28) and an unverified one (96, 99) both return "", false, so
// a caller that prints only what it was given can never put a wrong or stale
// State name on a document. The caller is expected to REFUSE on false and to use
// Explain for the message: "state code 25 was retired when the Union territory
// merged, correct the master to 26" is a sentence somebody can act on, and
// "Daman and Diu" silently printed on a 2026 invoice is not.
//
// NORMALISATION IS DELIBERATELY MINIMAL: surrounding whitespace is trimmed and
// nothing else. A one-digit value is NOT zero-padded — "9" is not a code, and
// turning it into "09" would put Uttar Pradesh on an invoice on the strength of a
// half-typed field. Anything that is not exactly two ASCII digits after trimming
// is unknown.
func Name(code string) (string, bool) {
	e, ok := table[normalise(code)]
	if !ok || e.Status != StatusCurrent {
		return "", false
	}
	return e.Name, true
}

// Lookup returns the whole entry for a code — including a retired or unverified
// one — so a caller can tell the three refusals apart. ok is false only when the
// table has no such code at all, and the returned Entry then carries
// StatusUnknown and the normalised code.
func Lookup(code string) (Entry, bool) {
	c := normalise(code)
	e, ok := table[c]
	if !ok {
		return Entry{Code: c, Status: StatusUnknown}, false
	}
	return e, true
}

// All returns every entry in the table, current, retired and unverified alike,
// ordered by code so output is stable.
//
// It returns copies of the entries and a fresh slice each call: the table is
// statutory reference data and a caller that could mutate it could change what a
// tax invoice says about a State from anywhere in the process.
func All() []Entry {
	codes := make([]string, 0, len(table))
	for c := range table {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	out := make([]Entry, 0, len(codes))
	for _, c := range codes {
		out = append(out, table[c])
	}
	return out
}

// Explain gives one sentence about a code, for a refusal an operator has to act
// on. It never returns "".
func Explain(code string) string {
	c := normalise(code)
	e, ok := table[c]
	if !ok {
		return fmt.Sprintf(
			"%q is not a GST State code: the codes are the two-digit State and Union territory codes "+
				"01 to 38, plus 97 for Other Territory", strings.TrimSpace(code))
	}
	switch e.Status {
	case StatusCurrent:
		return fmt.Sprintf("state code %s is %s", e.Code, e.Name)
	case StatusRetired:
		return fmt.Sprintf("state code %s (%s) is retired: %s (%s)", e.Code, e.Name, e.Note, e.Authority)
	default: // StatusUnverified
		return fmt.Sprintf("state code %s cannot be named: %s (%s)", e.Code, e.Note, e.Authority)
	}
}

// normalise trims surrounding whitespace and accepts exactly two ASCII digits.
// Anything else returns "", which no entry is keyed on, so it reads as unknown.
func normalise(code string) string {
	c := strings.TrimSpace(code)
	if len(c) != 2 {
		return ""
	}
	for i := 0; i < len(c); i++ {
		if c[i] < '0' || c[i] > '9' {
			return ""
		}
	}
	return c
}
