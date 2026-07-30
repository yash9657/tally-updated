package printing

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Font names one of the base-14 Type 1 fonts every PDF reader is required to
// have. We use ONLY these five, which is why this package ships no font file and
// embeds no font program: a reader supplies the glyphs, we supply the widths.
type Font int

const (
	Helvetica Font = iota
	HelveticaBold
	HelveticaOblique
	Courier
	CourierBold
)

// baseFontName is the /BaseFont value written into the font dictionary. These
// are the exact Adobe names for the standard 14 fonts (PDF 1.4 spec, Annex H,
// "Standard Type 1 Fonts"); a typo here makes a reader substitute a font and the
// widths we measured with stop matching what is drawn.
func (f Font) baseFontName() string {
	switch f {
	case HelveticaBold:
		return "Helvetica-Bold"
	case HelveticaOblique:
		return "Helvetica-Oblique"
	case Courier:
		return "Courier"
	case CourierBold:
		return "Courier-Bold"
	default:
		return "Helvetica"
	}
}

// resourceName is the key this font gets in the shared /Font resource dict. It
// is fixed per Font value, never generated, so the same document always emits
// the same names in the same order — see the determinism note in pdf.go.
func (f Font) resourceName() string {
	switch f {
	case HelveticaBold:
		return "F2"
	case HelveticaOblique:
		return "F3"
	case Courier:
		return "F4"
	case CourierBold:
		return "F5"
	default:
		return "F1"
	}
}

// allFonts is the emission order of the font objects. Fixed slice, never a map:
// map iteration order is the classic way a "deterministic" writer stops being
// deterministic.
var allFonts = []Font{Helvetica, HelveticaBold, HelveticaOblique, Courier, CourierBold}

// widths returns the per-character advance table for the font, indexed by
// WinAnsi character CODE (not by rune), in 1/1000 em. Multiply by the point size
// and divide by 1000 to get points.
//
// SOURCE: the Adobe Core 14 AFM metrics (Adobe Font Metrics files
// Helvetica.afm, Helvetica-Bold.afm, Helvetica-Oblique.afm, Courier.afm,
// Courier-Bold.afm, as distributed by Adobe with the "Core 14 AFM" package and
// reproduced identically in every PDF toolkit's core-font tables). These
// metrics are frozen — the standard 14 fonts have not changed since PDF 1.0 —
// so there is no effective-from date to track.
//
// Helvetica-Oblique has the SAME advance widths as Helvetica: the oblique face
// is a shear of the upright one, so shearing does not change the advance. That
// is why both map to helveticaWidths rather than to a duplicated table.
//
// Courier and Courier-Bold are MONOSPACED at exactly 600/1000 em for every
// glyph in the font, so they need no table at all.
func (f Font) widths() *[256]int16 {
	switch f {
	case HelveticaBold:
		return &helveticaBoldWidths
	case Courier, CourierBold:
		return &courierWidths
	default:
		return &helveticaWidths
	}
}

// Measure returns the width, in POINTS, of s set in f at size.
//
// Measure and Text agree by construction: both run the string through encode(),
// so a rune that prints as a substitution is also MEASURED as that
// substitution. If they disagreed, every table column computed from Measure
// would overflow by exactly the difference.
func Measure(f Font, size float64, s string) float64 {
	codes, _ := encode(s)
	w := f.widths()
	total := 0
	for _, c := range codes {
		total += int(w[c])
	}
	return float64(total) * size / 1000.0
}

// Wrap breaks s into lines no wider than maxWidth points when set in f at size.
//
// The contract, all of it tested, because every one of these cases has hung or
// truncated a real report:
//
//   - A single word wider than maxWidth is HARD-BROKEN at the character that
//     would overflow. It is never dropped and the loop always advances, so a
//     200-character HSN-less description cannot spin the server.
//   - An empty or whitespace-only string returns exactly one empty line, so a
//     caller may always index [0] and always draws the same number of rows.
//   - Runs of spaces, tabs and other blanks COLLAPSE to a single space. A
//     description pasted out of a spreadsheet is full of them and they must not
//     become visible ragged gaps.
//   - "\n" is a HARD break: an address stored with embedded newlines wraps at
//     the newline and then within each resulting line.
//   - maxWidth <= 0 returns the input as one line rather than looping. A zero
//     column width is a caller bug, not a reason to hang.
func Wrap(f Font, size float64, s string, maxWidth float64) []string {
	if maxWidth <= 0 {
		return []string{s}
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		out = append(out, wrapParagraph(f, size, para, maxWidth)...)
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

func wrapParagraph(f Font, size float64, s string, maxWidth float64) []string {
	words := strings.Fields(s) // Fields collapses every run of blanks
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	cur := ""
	for _, word := range words {
		cand := word
		if cur != "" {
			cand = cur + " " + word
		}
		if Measure(f, size, cand) <= maxWidth {
			cur = cand
			continue
		}
		if cur != "" {
			lines = append(lines, cur)
			cur = ""
		}
		// The word alone may still not fit: hard-break it.
		for Measure(f, size, word) > maxWidth {
			head, rest := breakOnce(f, size, word, maxWidth)
			lines = append(lines, head)
			word = rest
		}
		cur = word
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// breakOnce splits off the longest prefix of word that fits in maxWidth. It
// always consumes at least one rune, which is what guarantees Wrap terminates
// even when a single glyph is wider than the column.
func breakOnce(f Font, size float64, word string, maxWidth float64) (head, rest string) {
	n := 0
	for i := range word {
		if i == 0 {
			continue
		}
		if Measure(f, size, word[:i]) > maxWidth {
			break
		}
		n = i
	}
	if n == 0 {
		_, w := utf8.DecodeRuneInString(word)
		if w == 0 {
			w = 1
		}
		n = w
	}
	return word[:n], word[n:]
}

// ---------------------------------------------------------------------------
// WinAnsiEncoding
// ---------------------------------------------------------------------------

// EncodingError says that a string handed to this package cannot be printed in
// WinAnsiEncoding, which is the only encoding the base-14 fonts are used with
// here.
//
// WHY THIS IS AN ERROR AND NOT A BEST EFFORT: WinAnsi is a single-byte encoding
// covering Latin-1 plus the CP1252 punctuation. A Devanagari party name, a Tamil
// address or a bare rupee sign has NO code point in it. Emitting the low byte of
// such a rune would print a random accented Latin letter — a customer's name
// rendered as garbage on their own invoice. So the document REFUSES: the offence
// is recorded when the text is added and Doc.Bytes returns this error instead of
// a corrupt-looking document.
//
// The substitutions in winAnsiSubstitutions are the deliberate exceptions, and
// they are exhaustive and documented there.
//
// C0 CONTROL BYTES (below 0x20) are refused the same way, with the one exception
// of a tab, which becomes a space. A 0x01 arriving from an OCR'd bill has no
// glyph in any font; only the caller can decide what the description was meant to
// say, so it is the caller who is told.
type EncodingError struct {
	Rune rune   // the first rune that cannot be encoded
	Text string // the string it appeared in
}

func (e *EncodingError) Error() string {
	return fmt.Sprintf("printing: %q (U+%04X) cannot be printed in WinAnsiEncoding and has no documented substitution; the text was %q", e.Rune, e.Rune, e.Text)
}

// Representable reports whether s can be printed, returning an *EncodingError
// naming the first offending rune if it cannot. Callers that hold a
// transliteration (printingdb, importers) should use this to decide BEFORE
// building a document, rather than discovering it at Bytes() time.
func Representable(s string) error {
	if _, bad := encode(s); bad != nil {
		return bad
	}
	return nil
}

// winAnsiSubstitutions is the COMPLETE set of runes this package silently
// rewrites, with the reason for each. Everything not listed here and not
// encodable is an EncodingError.
//
// The rupee sign is the one that matters: no base-14 font has a rupee glyph and
// WinAnsi has no code for it, so an invoice total would print as a blank or a
// box. "Rs." is what Indian pre-printed stationery has used for a century and is
// unambiguous, so that is what we print.
var winAnsiSubstitutions = map[rune]string{
	'₹':      "Rs.", // INDIAN RUPEE SIGN: no WinAnsi code, no base-14 glyph
	'₨':      "Rs.", // RUPEE SIGN (legacy ligature)
	'−':      "-",   // MINUS SIGN -> hyphen-minus
	'‑':      "-",   // NON-BREAKING HYPHEN -> hyphen-minus
	'‐':      "-",   // HYPHEN -> hyphen-minus
	'⁄':      "/",   // FRACTION SLASH -> solidus
	'\u00ad': "",    // SOFT HYPHEN: invisible; printing it is never wanted
	'\ufeff': "",    // BOM smuggled in by a spreadsheet export
	'\u200b': "",    // ZERO WIDTH SPACE
	'\u2028': " ",   // LINE SEPARATOR -> space
	'\u2029': " ",   // PARAGRAPH SEPARATOR -> space
	'\t':     " ",   // tab: a PDF text object has no tab stop
}

// winAnsiFromRune maps a rune to its WinAnsiEncoding code, or reports false.
//
// WinAnsiEncoding is CP1252: identical to Latin-1 (ISO 8859-1) except for the
// 0x80-0x9F block, which CP1252 fills with punctuation and a handful of letters.
// Reference: PDF 1.4 spec, Annex D.2 "Latin Character Set and Encodings",
// WinAnsiEncoding column.
func winAnsiFromRune(r rune) (byte, bool) {
	switch {
	case r >= 0x20 && r <= 0x7E:
		return byte(r), true
	case r >= 0xA0 && r <= 0xFF:
		return byte(r), true
	}
	if c, ok := winAnsiHigh[r]; ok {
		return c, true
	}
	return 0, false
}

// winAnsiHigh is the CP1252 0x80-0x9F block. 0x81, 0x8D, 0x8F, 0x90 and 0x9D are
// UNDEFINED in CP1252 and deliberately absent.
var winAnsiHigh = map[rune]byte{
	'€': 0x80, // Euro
	'‚': 0x82, // quotesinglbase
	'ƒ': 0x83, // florin
	'„': 0x84, // quotedblbase
	'…': 0x85, // ellipsis
	'†': 0x86, // dagger
	'‡': 0x87, // daggerdbl
	'ˆ': 0x88, // circumflex
	'‰': 0x89, // perthousand
	'Š': 0x8A, // Scaron
	'‹': 0x8B, // guilsinglleft
	'Œ': 0x8C, // OE
	'Ž': 0x8E, // Zcaron
	'‘': 0x91, // quoteleft
	'’': 0x92, // quoteright
	'“': 0x93, // quotedblleft
	'”': 0x94, // quotedblright
	'•': 0x95, // bullet
	'–': 0x96, // endash
	'—': 0x97, // emdash
	'˜': 0x98, // tilde
	'™': 0x99, // trademark
	'š': 0x9A, // scaron
	'›': 0x9B, // guilsinglright
	'œ': 0x9C, // oe
	'ž': 0x9E, // zcaron
	'Ÿ': 0x9F, // Ydieresis
}

// substituteByte is what stands in for a rune that could not be encoded. The
// document is already in error when this is used, so it exists only so that
// measuring and escaping stay total functions — it never reaches a reader,
// because Bytes() fails first.
const substituteByte = byte('?')

// encode turns a Go string into WinAnsi bytes, applying the documented
// substitutions. It returns the bytes and, if some rune had no encoding and no
// substitution, the *EncodingError for the FIRST such rune.
func encode(s string) ([]byte, *EncodingError) {
	out := make([]byte, 0, len(s))
	var bad *EncodingError
	for _, r := range s {
		if sub, ok := winAnsiSubstitutions[r]; ok {
			for _, sr := range sub {
				if c, ok := winAnsiFromRune(sr); ok {
					out = append(out, c)
				}
			}
			continue
		}
		if c, ok := winAnsiFromRune(r); ok {
			out = append(out, c)
			continue
		}
		if bad == nil {
			bad = &EncodingError{Rune: r, Text: s}
		}
		out = append(out, substituteByte)
	}
	return out, bad
}

// runeFromWinAnsi is the inverse of winAnsiFromRune, used by ExtractText to read
// our own output back.
func runeFromWinAnsi(c byte) rune {
	if c >= 0x80 && c <= 0x9F {
		for r, b := range winAnsiHigh {
			if b == c {
				return r
			}
		}
		return 0xFFFD
	}
	return rune(c)
}

// ---------------------------------------------------------------------------
// The metric tables. Indexed by WinAnsi code; a zero means "no glyph at this
// code", which cannot happen for text that came through encode().
// ---------------------------------------------------------------------------

// courierWidths: Courier and Courier-Bold are monospaced at 600/1000 em.
var courierWidths = func() [256]int16 {
	var w [256]int16
	for i := range w {
		w[i] = 600
	}
	return w
}()

// helveticaWidths: Adobe Helvetica.afm (and Helvetica-Oblique.afm, identical).
var helveticaWidths = [256]int16{
	32: 278, 33: 278, 34: 355, 35: 556, 36: 556, 37: 889, 38: 667, 39: 191,
	40: 333, 41: 333, 42: 389, 43: 584, 44: 278, 45: 333, 46: 278, 47: 278,
	48: 556, 49: 556, 50: 556, 51: 556, 52: 556, 53: 556, 54: 556, 55: 556,
	56: 556, 57: 556, 58: 278, 59: 278, 60: 584, 61: 584, 62: 584, 63: 556,
	64: 1015, 65: 667, 66: 667, 67: 722, 68: 722, 69: 667, 70: 611, 71: 778,
	72: 722, 73: 278, 74: 500, 75: 667, 76: 556, 77: 833, 78: 722, 79: 778,
	80: 667, 81: 778, 82: 722, 83: 667, 84: 611, 85: 722, 86: 667, 87: 944,
	88: 667, 89: 667, 90: 611, 91: 278, 92: 278, 93: 278, 94: 469, 95: 556,
	96: 333, 97: 556, 98: 556, 99: 500, 100: 556, 101: 556, 102: 278, 103: 556,
	104: 556, 105: 222, 106: 222, 107: 500, 108: 222, 109: 833, 110: 556,
	111: 556, 112: 556, 113: 556, 114: 333, 115: 500, 116: 278, 117: 556,
	118: 500, 119: 722, 120: 500, 121: 500, 122: 500, 123: 334, 124: 260,
	125: 334, 126: 584,
	128: 556, 130: 222, 131: 556, 132: 333, 133: 1000, 134: 556, 135: 556,
	136: 333, 137: 1000, 138: 667, 139: 333, 140: 1000, 142: 611, 145: 222,
	146: 222, 147: 333, 148: 333, 149: 350, 150: 556, 151: 1000, 152: 333,
	153: 1000, 154: 500, 155: 333, 156: 944, 158: 500, 159: 667,
	160: 278, 161: 333, 162: 556, 163: 556, 164: 556, 165: 556, 166: 260,
	167: 556, 168: 333, 169: 737, 170: 370, 171: 556, 172: 584, 173: 333,
	174: 737, 175: 333, 176: 400, 177: 584, 178: 333, 179: 333, 180: 333,
	181: 556, 182: 537, 183: 278, 184: 333, 185: 333, 186: 365, 187: 556,
	188: 834, 189: 834, 190: 834, 191: 611,
	192: 667, 193: 667, 194: 667, 195: 667, 196: 667, 197: 667, 198: 1000,
	199: 722, 200: 667, 201: 667, 202: 667, 203: 667, 204: 278, 205: 278,
	206: 278, 207: 278, 208: 722, 209: 722, 210: 778, 211: 778, 212: 778,
	213: 778, 214: 778, 215: 584, 216: 778, 217: 722, 218: 722, 219: 722,
	220: 722, 221: 667, 222: 667, 223: 611,
	224: 556, 225: 556, 226: 556, 227: 556, 228: 556, 229: 556, 230: 889,
	231: 500, 232: 556, 233: 556, 234: 556, 235: 556, 236: 278, 237: 278,
	238: 278, 239: 278, 240: 556, 241: 556, 242: 556, 243: 556, 244: 556,
	245: 556, 246: 556, 247: 584, 248: 611, 249: 556, 250: 556, 251: 556,
	252: 556, 253: 500, 254: 556, 255: 500,
}

// helveticaBoldWidths: Adobe Helvetica-Bold.afm.
var helveticaBoldWidths = [256]int16{
	32: 278, 33: 333, 34: 474, 35: 556, 36: 556, 37: 889, 38: 722, 39: 238,
	40: 333, 41: 333, 42: 389, 43: 584, 44: 278, 45: 333, 46: 278, 47: 278,
	48: 556, 49: 556, 50: 556, 51: 556, 52: 556, 53: 556, 54: 556, 55: 556,
	56: 556, 57: 556, 58: 333, 59: 333, 60: 584, 61: 584, 62: 584, 63: 611,
	64: 975, 65: 722, 66: 722, 67: 722, 68: 722, 69: 667, 70: 611, 71: 778,
	72: 722, 73: 278, 74: 556, 75: 722, 76: 611, 77: 833, 78: 722, 79: 778,
	80: 667, 81: 778, 82: 722, 83: 667, 84: 611, 85: 722, 86: 667, 87: 944,
	88: 667, 89: 667, 90: 611, 91: 333, 92: 278, 93: 333, 94: 584, 95: 556,
	96: 333, 97: 556, 98: 611, 99: 556, 100: 611, 101: 556, 102: 333, 103: 611,
	104: 611, 105: 278, 106: 278, 107: 556, 108: 278, 109: 889, 110: 611,
	111: 611, 112: 611, 113: 611, 114: 389, 115: 556, 116: 333, 117: 611,
	118: 556, 119: 778, 120: 556, 121: 556, 122: 500, 123: 389, 124: 280,
	125: 389, 126: 584,
	128: 556, 130: 278, 131: 556, 132: 500, 133: 1000, 134: 556, 135: 556,
	136: 333, 137: 1000, 138: 667, 139: 333, 140: 1000, 142: 611, 145: 278,
	146: 278, 147: 500, 148: 500, 149: 350, 150: 556, 151: 1000, 152: 333,
	153: 1000, 154: 556, 155: 333, 156: 944, 158: 500, 159: 667,
	160: 278, 161: 333, 162: 556, 163: 556, 164: 556, 165: 556, 166: 280,
	167: 556, 168: 333, 169: 737, 170: 370, 171: 556, 172: 584, 173: 333,
	174: 737, 175: 333, 176: 400, 177: 584, 178: 333, 179: 333, 180: 333,
	181: 611, 182: 556, 183: 278, 184: 333, 185: 333, 186: 365, 187: 556,
	188: 834, 189: 834, 190: 834, 191: 611,
	192: 722, 193: 722, 194: 722, 195: 722, 196: 722, 197: 722, 198: 1000,
	199: 722, 200: 667, 201: 667, 202: 667, 203: 667, 204: 278, 205: 278,
	206: 278, 207: 278, 208: 722, 209: 722, 210: 778, 211: 778, 212: 778,
	213: 778, 214: 778, 215: 584, 216: 778, 217: 722, 218: 722, 219: 722,
	220: 722, 221: 667, 222: 667, 223: 611,
	224: 556, 225: 556, 226: 556, 227: 556, 228: 556, 229: 556, 230: 889,
	231: 556, 232: 556, 233: 556, 234: 556, 235: 556, 236: 278, 237: 278,
	238: 278, 239: 278, 240: 611, 241: 611, 242: 611, 243: 611, 244: 611,
	245: 611, 246: 611, 247: 584, 248: 611, 249: 611, 250: 611, 251: 611,
	252: 611, 253: 556, 254: 611, 255: 556,
}
