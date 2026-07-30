package printing

import (
	"strconv"
	"strings"

	"github.com/shopspring/decimal"
)

// AmountInWords renders an amount as the words that go on the face of an
// invoice, a cheque and a receipt. Indian numbering: lakh and crore, never
// million and billion.
//
// THE WORDING IS PINNED HERE. Everything about the output is a decision, so
// every decision is written down and each one has a named test:
//
//		  1234567.89 -> "Rupees Twelve Lakh Thirty Four Thousand Five Hundred Sixty Seven and Eighty Nine Paise Only"
//		       100   -> "Rupees One Hundred Only"
//		         0.5 -> "Rupees Zero and Fifty Paise Only"
//		         0   -> "Rupees Zero Only"
//		     -1234   -> "Minus Rupees One Thousand Two Hundred Thirty Four Only"
//
//	  - PREFIX "Rupees", SUFFIX "Only". Both are the Negotiable Instruments Act
//	    cheque-writing convention: "Only" closes the amount so digits cannot be
//	    appended after it.
//	  - NO INTERNAL "and". 1001 is "One Thousand One", not "One Thousand and One".
//	    The only "and" in the output is the one that introduces the paise, which is
//	    what makes it unambiguous where the rupees stop.
//	  - NO PAISE CLAUSE WHEN THE PAISE ARE ZERO. A whole-rupee amount reads
//	    "Rupees One Hundred Only", not "... and Zero Paise Only".
//	  - ZERO RUPEES ARE STILL SPOKEN. 0.50 is "Rupees Zero and Fifty Paise Only",
//	    because "Rupees and Fifty Paise Only" reads like a word went missing.
//	  - "Paise" ALWAYS, never "Paisa", even for one: "Rupees Zero and One Paise
//	    Only". Strict grammar wants the singular, and every Indian accounting
//	    package prints the plural; a document that matches what a cheque-clearing
//	    clerk expects to see beats one that matches a grammar book.
//	  - A NEGATIVE AMOUNT IS PREFIXED "Minus". A credit note total is negative and
//	    the words must not silently drop the sign that the figures show.
//	  - NO HYPHENS. "Thirty Four", not "Thirty-Four".
//
// ROUNDING IS IDENTICAL TO FormatMoney and this is the whole reason the function
// takes a decimal rather than a pre-rounded string. The ledger stores
// NUMERIC(19,4), so the value handed here routinely has four decimal places; if
// the words truncated where the figures rounded, the same invoice would say
// 1,234.57 in the total box and "Fifty Six Paise" in the words, and the words are
// the legally operative amount on a negotiable instrument. Both call
// StringFixed(2), which is half away from zero.
// Pinned by TestWordsAndFiguresAgreeOnAFourDecimalLedgerValue.
//
// ABOVE ONE CRORE the naming continues RECURSIVELY in the same system rather
// than borrowing "arab" or "kharab", which are not used in Indian financial
// writing: 10^9 is "One Hundred Crore" and 10^15 is "Ten Crore Crore". A
// repeated "Crore" looks odd, and it is what the Indian system actually does
// past 10^9 in practice. Pinned by
// TestAnAmountAboveOneCroreCroreIsStillSpokenInTheIndianSystem.
//
// IT NEVER PANICS AND NEVER OVERFLOWS. The digits come from StringFixed and are
// consumed as a string; the recursion peels seven digits at a time and only ever
// hands a group of at most seven digits to strconv.Atoi, so a fifteen- or
// thirty-digit amount is worded correctly rather than wrapping through int64 the
// way decimal.IntPart() silently does.
func AmountInWords(d decimal.Decimal) string {
	fixed := d.StringFixed(2) // same quantization as FormatMoney

	neg := strings.HasPrefix(fixed, "-")
	fixed = strings.TrimPrefix(fixed, "-")

	rupeeDigits, paiseDigits := splitFixed(fixed)

	var b strings.Builder
	if neg {
		b.WriteString("Minus ")
	}
	b.WriteString("Rupees ")
	b.WriteString(indianWords(rupeeDigits))

	if paise := atoiDigits(paiseDigits); paise > 0 {
		b.WriteString(" and ")
		b.WriteString(underHundred(paise))
		b.WriteString(" Paise")
	}
	b.WriteString(" Only")
	return b.String()
}

// onesWords covers 0..19, where English stops being compositional.
var onesWords = [20]string{
	"Zero", "One", "Two", "Three", "Four", "Five", "Six", "Seven", "Eight",
	"Nine", "Ten", "Eleven", "Twelve", "Thirteen", "Fourteen", "Fifteen",
	"Sixteen", "Seventeen", "Eighteen", "Nineteen",
}

// tensWords is indexed by the tens digit; the first two slots are unreachable
// and present only to keep the indexing honest.
var tensWords = [10]string{
	"", "", "Twenty", "Thirty", "Forty", "Fifty", "Sixty", "Seventy",
	"Eighty", "Ninety",
}

// underHundred words 0..99. "Forty" has no u and "Ninety" has no i: both are the
// spellings people get wrong on a cheque.
func underHundred(n int) string {
	if n < 20 {
		return onesWords[n]
	}
	t := tensWords[n/10]
	if n%10 == 0 {
		return t
	}
	return t + " " + onesWords[n%10]
}

// underCrore words 0..99,99,999 in the Indian system: lakh, thousand, hundred.
// It returns "" for zero so its callers can decide whether a zero group is
// spoken at all.
func underCrore(n int) string {
	if n == 0 {
		return ""
	}
	var parts []string
	if lakh := n / 100000; lakh > 0 {
		parts = append(parts, underHundred(lakh), "Lakh")
		n %= 100000
	}
	if thousand := n / 1000; thousand > 0 {
		parts = append(parts, underHundred(thousand), "Thousand")
		n %= 1000
	}
	if hundred := n / 100; hundred > 0 {
		parts = append(parts, onesWords[hundred], "Hundred")
		n %= 100
	}
	if n > 0 {
		parts = append(parts, underHundred(n))
	}
	return strings.Join(parts, " ")
}

// indianWords words an arbitrarily long string of digits.
//
// Everything below one crore is one Atoi away. Above that it peels the low SEVEN
// digits (one crore) and recurses on what is left, so the value never passes
// through an int64 and "1100" can never come out as "Eleven Hundred" — the
// grouping is fixed by the Indian system, not by digit count.
func indianWords(digits string) string {
	digits = strings.TrimLeft(digits, "0")
	if digits == "" {
		return onesWords[0] // "Zero"
	}

	if len(digits) <= 7 {
		return underCrore(atoiDigits(digits))
	}

	crores := digits[:len(digits)-7]
	rest := digits[len(digits)-7:]

	out := indianWords(crores) + " Crore"
	if strings.Trim(rest, "0") != "" {
		out += " " + underCrore(atoiDigits(rest))
	}
	return out
}

// atoiDigits parses a string of at most seven ASCII digits. Callers only ever
// hand it output of StringFixed, so a parse error is impossible; it returns 0
// rather than panicking if that ever stops being true, because a formatter that
// panics takes out the print handler for a real invoice.
func atoiDigits(digits string) int {
	if digits == "" {
		return 0
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
