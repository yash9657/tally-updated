package printing

import (
	"bytes"
	"strconv"
	"strings"
)

// PageSize is a page's trim size in POINTS. A point is 1/72 inch, which is the
// only unit a PDF content stream understands, so nothing in this package deals
// in millimetres.
type PageSize struct {
	Width, Height float64
}

// A4 is 210mm x 297mm. 210 / 25.4 * 72 = 595.276 and 297 / 25.4 * 72 = 841.890,
// which is the /MediaBox every Indian statutory print uses.
var (
	A4Portrait  = PageSize{595.28, 841.89}
	A4Landscape = PageSize{841.89, 595.28}
)

// Page is one sheet. It accumulates a PDF content stream as operators are
// appended; nothing is laid out or measured lazily, so the stream for a page is
// complete the moment the last drawing call returns.
//
// COORDINATE SYSTEM — READ THIS BEFORE DRAWING ANYTHING.
// The origin (0,0) is the BOTTOM-LEFT corner of the page and y INCREASES
// UPWARD. This is PDF's own default user space and we do not install a flipping
// CTM, because a flipped matrix also mirrors glyphs unless it is undone per text
// object. So: the top of an A4 portrait page is y = 841.89, the first line of a
// letterhead is drawn at a LARGE y, and each following line at a SMALLER one. A
// renderer written as if the origin were top-left produces a document that is
// upside down and off the page, and it will look "nearly right" in a thumbnail.
//
// The y passed to Text is the text BASELINE, not the top of the glyph.
type Page struct {
	doc  *Doc
	size PageSize
	buf  bytes.Buffer
}

// Size reports the page's trim size in points.
func (p *Page) Size() PageSize { return p.size }

// Text draws s with its left edge at x and its baseline at y.
func (p *Page) Text(x, y float64, f Font, size float64, s string) {
	if s == "" {
		return
	}
	lit, err := p.literal(s)
	if err != nil {
		p.doc.note(err)
	}
	p.buf.WriteString("BT /")
	p.buf.WriteString(f.resourceName())
	p.buf.WriteByte(' ')
	p.buf.WriteString(fmtNum(size))
	p.buf.WriteString(" Tf 1 0 0 1 ")
	p.buf.WriteString(fmtNum(x))
	p.buf.WriteByte(' ')
	p.buf.WriteString(fmtNum(y))
	p.buf.WriteString(" Tm ")
	p.buf.Write(lit)
	p.buf.WriteString(" Tj ET\n")
}

// TextRight draws s so that its RIGHT edge lands on xRight. Every amount column
// on an invoice is set this way: a right-aligned rupee figure is how a reader
// compares two totals at a glance, and it is what Tally prints.
func (p *Page) TextRight(xRight, y float64, f Font, size float64, s string) {
	p.Text(xRight-Measure(f, size, s), y, f, size, s)
}

// TextCenter draws s centred on xCenter.
func (p *Page) TextCenter(xCenter, y float64, f Font, size float64, s string) {
	p.Text(xCenter-Measure(f, size, s)/2, y, f, size, s)
}

// Line strokes a straight line from (x1,y1) to (x2,y2) at the given width.
func (p *Page) Line(x1, y1, x2, y2, width float64) {
	p.buf.WriteString(fmtNum(width))
	p.buf.WriteString(" w ")
	p.buf.WriteString(fmtNum(x1))
	p.buf.WriteByte(' ')
	p.buf.WriteString(fmtNum(y1))
	p.buf.WriteString(" m ")
	p.buf.WriteString(fmtNum(x2))
	p.buf.WriteByte(' ')
	p.buf.WriteString(fmtNum(y2))
	p.buf.WriteString(" l S\n")
}

// Rect strokes (does not fill) the rectangle whose LOWER-LEFT corner is (x,y),
// extending w to the right and h upward — upward, because y increases upward.
func (p *Page) Rect(x, y, w, h, lineWidth float64) {
	p.buf.WriteString(fmtNum(lineWidth))
	p.buf.WriteString(" w ")
	p.buf.WriteString(fmtNum(x))
	p.buf.WriteByte(' ')
	p.buf.WriteString(fmtNum(y))
	p.buf.WriteByte(' ')
	p.buf.WriteString(fmtNum(w))
	p.buf.WriteByte(' ')
	p.buf.WriteString(fmtNum(h))
	p.buf.WriteString(" re S\n")
}

// content is the finished content stream for the page.
func (p *Page) content() []byte { return p.buf.Bytes() }

// literal turns a Go string into a PDF string literal, parentheses and all.
//
// WHY THIS IS THE MOST DANGEROUS FUNCTION IN THE PACKAGE: a PDF string literal
// is delimited by ( and ). An unescaped ")" inside it CLOSES THE STRING EARLY
// and everything after it is parsed as operators — the reader then reports a
// broken file, or worse, silently drops the rest of the page. Real invoice
// descriptions contain parentheses and backslashes ("Widget (Large) \ 50%"), so
// this is not a theoretical case.
//
// It is encode() + escapeLiteral(), in that order: WinAnsi first, delimiters
// second.
func (p *Page) literal(s string) ([]byte, error) {
	codes, encErr := encode(s)
	out := escapeLiteral(codes)
	if encErr != nil {
		return out, encErr
	}
	return out, nil
}

// escapeLiteral wraps already-encoded WinAnsi bytes in parentheses, escaping
// what has to be escaped: \ ( ) and every byte below 0x20 (as a three-digit
// octal). Bytes 0x80 and above are emitted RAW, which is legal in a literal
// string and keeps the output greppable.
//
// The sub-0x20 handling cannot be reached through Page.Text today, because
// encode() refuses C0 control bytes outright (they have no WinAnsi glyph, so
// there is nothing honest to print). It exists so that no future substitution can
// smuggle a raw control byte into a stream, and it is tested directly.
func escapeLiteral(codes []byte) []byte {
	var out bytes.Buffer
	out.WriteByte('(')
	for _, c := range codes {
		switch c {
		case '\\', '(', ')':
			out.WriteByte('\\')
			out.WriteByte(c)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			if c < 0x20 {
				out.WriteByte('\\')
				oct := strconv.FormatUint(uint64(c), 8)
				out.WriteString(strings.Repeat("0", 3-len(oct)))
				out.WriteString(oct)
				continue
			}
			out.WriteByte(c)
		}
	}
	out.WriteByte(')')
	return out.Bytes()
}

// fmtNum formats a coordinate for a content stream.
//
// float64 HERE IS GEOMETRY, NOT MONEY. The API takes points because the PDF
// coordinate system is real-valued; no amount is ever computed in this package.
// Every money value arrives as an ALREADY-FORMATTED STRING (see the Row and
// LineItem contract in doc.go) and passes straight into a string literal, so a
// rounding in this function can move a rule by a hundredth of a point but can
// never change a printed figure.
//
// Two decimals, fixed, then trailing zeros trimmed: fixed so the same input
// always produces the same bytes, trimmed so the stream stays readable. "-0" is
// normalised to "0" because IEEE negative zero would otherwise make two
// arithmetically identical documents differ byte for byte.
func fmtNum(v float64) string {
	s := strconv.FormatFloat(v, 'f', 2, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	if s == "" || s == "-" || s == "-0" {
		return "0"
	}
	return s
}
