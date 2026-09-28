package bankstatement

// READING THE NARRATION — payment rail, counterparty, VPA and UTR out of the
// free text banks write into every row.
//
// Design memo: docs/design/d123-w5-documents-bank.md §2.4.
//
// ⛔ WHAT THIS IS ALLOWED TO DECIDE: NOTHING THAT MOVES MONEY
// -----------------------------------------------------------
// A narration is the bank's free text, and its shape differs by bank, by rail
// and by year. Everything parsed here is an ANNOTATION on a row whose amount,
// direction and date came from their own columns: it may be shown to the
// operator, used to SUGGEST a party, and used as one more token for the matcher
// to look for — and it is never an input to an amount, a direction, a date or a
// clearance. A mis-parsed narration therefore costs a worse suggestion; it
// cannot cost a wrong number. That boundary is what makes a heuristic parser
// acceptable in a package whose every other decision refuses on ambiguity.
//
// WHAT IT RECOGNISES, AND FROM WHAT EVIDENCE
// ------------------------------------------
// The RAIL is read off a keyword the narration itself carries — UPI, NEFT,
// IMPS, RTGS, NACH/ACH/ECS, cheque clearing (CHQ, CLG, CTS, MICR), ATM, card
// POS, interest and charges — matched as a whole token, so "NEFTY TRADERS" is
// not an NEFT. The identifiers are read by their own FORMATS, which are fixed
// by the rails rather than by any bank:
//
//   - a VPA is handle@psp;
//   - a UPI or IMPS retrieval reference (RRN) is exactly twelve digits;
//   - an IFSC is four letters, a zero, and six alphanumerics;
//   - an NEFT/RTGS UTR is a long alphanumeric token carrying at least eight
//     digits (16 characters for NEFT, 22 for RTGS, in the common layouts —
//     length is NOT used to tell them apart, the keyword is);
//   - a cheque number is six digits on a cheque-clearing row.
//
// The COUNTERPARTY is the weakest field, and it is labelled as such: the first
// token that is not a rail keyword, a direction word, an identifier, a masked
// account or a number. Each bank orders its tokens differently (HDFC puts the
// name first; SBI after the RRN; ICICI sometimes puts a remark there), so it is
// a suggestion for the operator's party picker and nothing more.

import (
	"regexp"
	"strings"
)

// Rail is the payment system a row moved on.
type Rail string

const (
	RailUPI      Rail = "UPI"
	RailNEFT     Rail = "NEFT"
	RailIMPS     Rail = "IMPS"
	RailRTGS     Rail = "RTGS"
	RailNACH     Rail = "NACH"
	RailCheque   Rail = "CHEQUE"
	RailATM      Rail = "ATM"
	RailCard     Rail = "CARD"
	RailInterest Rail = "INTEREST"
	RailCharges  Rail = "CHARGES"
	// RailUnknown is a narration no keyword placed. It is stored as NULL, not
	// as "OTHER": "we did not recognise this" and "this is a known kind of
	// thing" are different facts.
	RailUnknown Rail = ""
)

// Narration is the structured reading of one row's description.
type Narration struct {
	Rail Rail
	// Counterparty is a SUGGESTION — see the header.
	Counterparty string
	VPA          string
	// Reference is the rail's own identifier: the RRN for UPI/IMPS, the UTR for
	// NEFT/RTGS, the cheque number for a cheque. Empty when none was found.
	Reference string
	IFSC      string
}

var (
	// ⚠️ NO '-' IN THE HANDLE, although NPCI permits one. HDFC separates every
	// narration field with '-', so a handle class that allowed it swallowed the
	// counterparty: "UPI-ACME TRADERS-acme.traders@okhdfcbank" read the VPA as
	// "traders-acme.traders@…". A hyphenated handle therefore loses its prefix
	// (the part after the last '-' is kept), which costs a worse VPA suggestion;
	// the other way cost the name.
	vpaRe     = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9._]{1,255}@[A-Za-z][A-Za-z0-9]{1,63}`)
	ifscRe    = regexp.MustCompile(`\b[A-Z]{4}0[A-Z0-9]{6}\b`)
	rrnRe     = regexp.MustCompile(`^\d{12}$`)
	chequeRe  = regexp.MustCompile(`^\d{6}$`)
	maskedAcc = regexp.MustCompile(`^[X*x]{2,}\d{2,}$`)
	digitsRe  = regexp.MustCompile(`\d`)
)

// railKeywords maps a WHOLE token to its rail. Checked in the order of
// railOrder, because a row can carry two ("UPI/IMPS" appears on some
// app-initiated IMPS rows and is an IMPS).
var railKeywords = map[string]Rail{
	"UPI":  RailUPI,
	"NEFT": RailNEFT, "NEFTCR": RailNEFT, "NEFTDR": RailNEFT, "NEFTINW": RailNEFT,
	"IMPS": RailIMPS, "MMT": RailIMPS,
	"RTGS": RailRTGS, "RTGSCR": RailRTGS, "RTGSDR": RailRTGS,
	"NACH": RailNACH, "ACH": RailNACH, "ECS": RailNACH,
	"CHQ": RailCheque, "CHEQUE": RailCheque, "CLG": RailCheque, "CLEARING": RailCheque,
	"CTS": RailCheque, "MICR": RailCheque,
	"ATM": RailATM, "ATW": RailATM, "NWD": RailATM,
	"POS": RailCard, "PCD": RailCard,
	"INT": RailInterest, "INTEREST": RailInterest,
	"CHRG": RailCharges, "CHRGS": RailCharges, "CHARGES": RailCharges, "CHGS": RailCharges,
}

var railOrder = []Rail{RailIMPS, RailRTGS, RailNEFT, RailUPI, RailNACH, RailCheque, RailATM, RailCard, RailInterest, RailCharges}

// noise are tokens that are never a counterparty: direction words, transfer
// boilerplate, UPI sub-types and bank names that rails print beside the name.
var noise = map[string]bool{
	"CR": true, "DR": true, "BY": true, "TO": true, "TRANSFER": true, "TRF": true, "FROM": true,
	"P2A": true, "P2M": true, "P2P": true, "INW": true, "OUTW": true, "INWARD": true, "OUTWARD": true,
	"PAYMENT": true, "PAY": true, "PAID": true, "DEP": true, "DEPOSIT": true, "REF": true, "NO": true,
	"BANK": true, "LTD": true, "UPIINTENT": true, "COLLECT": true, "TXN": true, "IB": true, "MB": true,
	"HDFC": true, "ICICI": true, "SBI": true, "SBIN": true, "AXIS": true, "UTIB": true, "KOTAK": true,
	"KKBK": true, "ICIC": true, "YESB": true, "PUNB": true, "BARB": true, "CNRB": true, "IDFB": true,
	"PD": true, "FOR": true, "THE": true, "OF": true, "AC": true, "A/C": true, "WDL": true, "CASH": true,
}

// ParseNarration reads one description. It never fails: an unrecognised
// narration is a Narration with every field empty.
func ParseNarration(desc string) Narration {
	var n Narration
	text := strings.TrimSpace(desc)
	if text == "" {
		return n
	}

	// VPAs first, out of the RAW text: a handle contains '.', which is a word
	// boundary everywhere else.
	if m := vpaRe.FindString(text); m != "" {
		n.VPA = strings.ToLower(m)
		text = strings.Replace(text, m, " ", 1)
	}
	upper := strings.ToUpper(text)
	if m := ifscRe.FindString(upper); m != "" {
		n.IFSC = m
	}

	// SEGMENTS are what a bank separates with '/', '-' or '*'; WORDS are what
	// a segment separates with spaces. The distinction is the counterparty:
	// "NISHA RAO/Rent April" is a name segment and a remark segment, and
	// joining words across the '/' read "NISHA RAO RENT APRIL".
	segments := strings.FieldsFunc(upper, func(r rune) bool {
		return strings.ContainsRune("/-*:|,;()[]#\\_", r)
	})
	var tokens []string
	for _, seg := range segments {
		tokens = append(tokens, strings.Fields(seg)...)
	}

	seen := map[Rail]bool{}
	for _, t := range tokens {
		if r, ok := railKeyword(t); ok {
			seen[r] = true
		}
	}
	for _, r := range railOrder {
		if seen[r] {
			n.Rail = r
			break
		}
	}

	// The rail's own reference, by format.
	for _, t := range tokens {
		switch n.Rail {
		case RailUPI, RailIMPS:
			if rrnRe.MatchString(t) {
				n.Reference = t
			}
		case RailNEFT, RailRTGS:
			if isUTR(t) && t != n.IFSC {
				n.Reference = t
			}
		case RailCheque:
			if chequeRe.MatchString(t) {
				n.Reference = t
			}
		}
		if n.Reference != "" {
			break
		}
	}

	// The counterparty: the FIRST SEGMENT that reads as a name — no digits, no
	// rail keyword, no identifier, and at least one word that is not
	// boilerplate. The segment is kept whole ("BHARAT STEEL LTD"), because the
	// suffix is part of how an operator recognises the party.
	for _, seg := range segments {
		words := strings.Fields(seg)
		if segmentIsName(words, n) {
			n.Counterparty = strings.Join(words, " ")
			break
		}
	}
	return n
}

// railKeyword reads a token, or the part of it before a full stop ("INT.PD"),
// as a rail keyword.
func railKeyword(t string) (Rail, bool) {
	bare := strings.TrimRight(t, ".")
	if r, ok := railKeywords[bare]; ok {
		return r, true
	}
	if i := strings.IndexByte(bare, '.'); i > 0 {
		if r, ok := railKeywords[bare[:i]]; ok {
			return r, true
		}
	}
	return RailUnknown, false
}

func segmentIsName(words []string, n Narration) bool {
	nameLike := 0
	for _, w := range words {
		bare := strings.TrimRight(w, ".")
		if bare == "" {
			continue
		}
		if _, ok := railKeyword(bare); ok {
			return false
		}
		if bare == n.IFSC || bare == n.Reference || maskedAcc.MatchString(bare) || digitsRe.MatchString(bare) {
			// Names do not carry digits in any rail's layout; an alphanumeric
			// word is a reference, an account or a remark code.
			return false
		}
		if !noise[bare] && len(bare) >= 3 {
			nameLike++
		}
	}
	return nameLike > 0
}

// isUTR is an NEFT/RTGS transaction reference: 12 to 22 alphanumerics carrying
// at least eight digits and at least one letter or twelve-plus digits.
func isUTR(t string) bool {
	if len(t) < 12 || len(t) > 22 {
		return false
	}
	for _, r := range t {
		if !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') {
			return false
		}
	}
	return len(digitsRe.FindAllString(t, -1)) >= 8
}
