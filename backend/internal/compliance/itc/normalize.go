package itc

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// MaxInvoiceNoLen is rule 46(b)'s cap on the length of a tax-invoice number:
// sixteen characters. A supplier document number whose STRICT key exceeds it is
// itself a reported finding (FindingDocNoInvalid) — it means either the supplier
// issued a non-conforming invoice or somebody keyed two numbers into one field.
// It is NOT a match failure: the document still matches on the strict key if the
// other side carries the same over-long number, because refusing to reconcile a
// real invoice because it is badly numbered helps nobody.
const MaxInvoiceNoLen = 16

// NormalizeDocNo reduces a supplier document number to the two keys the matcher
// uses, and it is THE ONLY PLACE THIS RULE IS STATED. There is deliberately no
// generated column and no SQL restatement of it: a normalisation written twice
// is a normalisation that drifts, and the drift shows up as invoices that
// stopped matching for no visible reason. Nothing normalised is stored on
// voucher_version; the matcher normalises both sides in Go at match time (it
// reads every purchase row of the period anyway).
//
// strict is: Unicode NFKC, then every rune that is not [0-9A-Za-z] dropped, then
// ASCII-uppercased.
//
// NFKC first, because a supplier's system may emit a full-width digit or a
// ligature and "INV１" must reduce to "INV1". ASCII uppercasing EXPLICITLY, not
// strings.ToUpper's Unicode-aware casing: Unicode casing is locale-dependent for
// a handful of scripts (the Turkish dotless i is the standard example) and a
// key whose value depends on the environment's locale is a key that pairs
// differently on the server and on the developer's laptop. Every character rule
// 46(b) permits in an invoice number is ASCII, so restricting the fold to ASCII
// loses nothing real and buys determinism.
//
// loose is strict with LEADING ZEROS STRIPPED FROM EVERY MAXIMAL DIGIT RUN:
// "INV0012A007" -> "INV12A7".
//
// WHY TWO KEYS, AND WHY LEADING ZEROS SURVIVE THE STRICT ONE. "INV001" and
// "INV01" are plausibly two different invoices from a supplier whose system pads
// to a fixed width — and are plausibly the same invoice typed twice. There is no
// rule that decides which, so the code must not pretend there is:
//
//   - stripping zeros in the pairing key is the single commonest source of
//     FALSE PAIRS, and a false pair means ITC claimed against the wrong invoice;
//   - not stripping them at all is the single commonest source of false
//     non-matches, and a false non-match means ITC reversed that was good.
//
// So the STRICT key decides PAIRING and the LOOSE key decides CANDIDACY: a
// loose-only agreement produces a NEAR_MATCH that names the candidate and pairs
// nothing, leaving a human to say which it is. See match.go's pass 3 and
// Line.Status == NearMatch.
//
// A digit run that is ALL zeros collapses to a single "0" rather than to the
// empty string, so "INV000" and "INV0" both reduce to "INV0" and neither
// reduces to "INV" (which would collide with a genuinely different number).
func NormalizeDocNo(s string) (strict, loose string) {
	folded := norm.NFKC.String(s)

	var b strings.Builder
	b.Grow(len(folded))
	for _, r := range folded {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 'a' + 'A')
		default:
			// Dropped: separators, punctuation, spaces, and every non-ASCII rune
			// that survived NFKC. A supplier writing "INV-001 /24-25" and a clerk
			// keying "inv001/2425" must reduce to the same thing.
		}
	}
	strict = b.String()

	loose = stripLeadingZeroRuns(strict)
	return strict, loose
}

// stripLeadingZeroRuns removes leading zeros from every maximal run of digits,
// leaving a single "0" where a run was entirely zeros.
func stripLeadingZeroRuns(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	for i := 0; i < len(s); {
		if !isASCIIDigit(s[i]) {
			out.WriteByte(s[i])
			i++
			continue
		}
		j := i
		for j < len(s) && isASCIIDigit(s[j]) {
			j++
		}
		run := s[i:j]
		k := 0
		for k < len(run)-1 && run[k] == '0' {
			k++
		}
		out.WriteString(run[k:])
		i = j
	}
	return out.String()
}

func isASCIIDigit(c byte) bool { return c >= '0' && c <= '9' }

// NormalizeGSTIN uppercases and strips ALL whitespace from a GSTIN and does
// NOTHING ELSE. There is no tolerance and there will not be one: a GSTIN is a
// taxpayer's identity, and one character different is a different taxpayer.
// It deliberately does not validate the checksum or the 15-character length —
// this function is used to KEY a comparison, and silently dropping a malformed
// GSTIN would turn "the supplier's GSTIN is wrong in our master" into "the
// supplier did not file", which is a reversal against an innocent supplier.
// Validity is reported separately, as a finding.
func NormalizeGSTIN(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsSpace(r) {
			continue
		}
		if r >= 'a' && r <= 'z' {
			b.WriteRune(r - 'a' + 'A')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
