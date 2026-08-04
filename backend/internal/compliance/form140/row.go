package form140

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// The delimiter, and the reason no value may contain it.
//
// "^" separates fields. A caret inside a deductee's name would add a delimiter
// to that record, break General Note 10's count, and shift every following
// column by one — so it is refused at the point of setting, with the field
// named, rather than producing a file that is wrong further along than the
// mistake.
const delimiter = "^"

// lineTerminator is CRLF, per General Note 2: "Each Record (including last
// record) must start on new line and must end with a newline character. Hex
// Values: '0D' & '0A'." Note the "including last record" — the file ends with a
// terminator, so there is no special case for the final line and no trailing
// empty record either.
const lineTerminator = "\r\n"

// row is one record under construction: a layout, and one value slot per field.
//
// Values are addressed BY FIELD NUMBER. There is no Append, deliberately — see
// layout.go's header for why appending is the failure mode this design exists to
// make unreachable.
type row struct {
	rec  *record
	vals []string
	// horizon is the file creation date; no date field may be after it
	// (General Note 8).
	horizon time.Time
	errs    []error
}

func newRow(rec *record, lineNo int, horizon time.Time) *row {
	r := &row{rec: rec, vals: make([]string, len(rec.fields)), horizon: horizon}
	r.setInt(1, lineNo)
	r.setChar(2, rec.typ)
	return r
}

func (r *row) fail(format string, args ...any) {
	r.errs = append(r.errs, fmt.Errorf(format, args...))
}

// field looks up a field by its Sr. No. and returns its slot index.
func (r *row) field(no int) (field, int, bool) {
	for i, fl := range r.rec.fields {
		if fl.no == no {
			return fl, i, true
		}
	}
	r.fail("form140: %s record has no field %d", r.rec.typ, no)
	return field{}, 0, false
}

// put writes a rendered value after the checks every kind shares.
func (r *row) put(no int, fl field, idx int, v string) {
	if strings.Contains(v, delimiter) {
		r.fail("form140: %s field %d (%s) contains the %q delimiter, which would "+
			"shift every later column of the record", r.rec.typ, no, fl.name, delimiter)
		return
	}
	if fl.size > 0 && len(v) > fl.size {
		r.fail("form140: %s field %d (%s) is %d characters, over the specified size of %d",
			r.rec.typ, no, fl.name, len(v), fl.size)
		return
	}
	r.vals[idx] = v
}

func (r *row) expect(no int, want kind) (field, int, bool) {
	fl, idx, ok := r.field(no)
	if !ok {
		return fl, idx, false
	}
	if fl.k == kindUnused {
		r.fail("form140: %s field %d (%s) is a \"no value should be specified\" field "+
			"and must stay empty", r.rec.typ, no, fl.name)
		return fl, idx, false
	}
	if fl.k != want {
		r.fail("form140: %s field %d (%s) is of a different kind than the value being set",
			r.rec.typ, no, fl.name)
		return fl, idx, false
	}
	return fl, idx, true
}

func (r *row) setChar(no int, v string) {
	fl, idx, ok := r.expect(no, kindChar)
	if !ok {
		return
	}
	r.put(no, fl, idx, v)
}

func (r *row) setInt(no int, v int) {
	fl, idx, ok := r.expect(no, kindInteger)
	if !ok {
		return
	}
	if v < 0 {
		r.fail("form140: %s field %d (%s) is negative (%d); the format has no sign",
			r.rec.typ, no, fl.name, v)
		return
	}
	// "no need to provide leading '0' for numerals" (General Note 9) — so the
	// plain decimal rendering is the correct one, not a zero-padded one.
	r.put(no, fl, idx, strconv.Itoa(v))
}

// setIntString writes a numeric field that is carried as text because it is an
// identifier rather than a quantity — a BSR code, a challan number, a pincode.
// Leading zeros are SIGNIFICANT in these and strconv would eat them.
func (r *row) setIntString(no int, v string) {
	fl, idx, ok := r.expect(no, kindInteger)
	if !ok {
		return
	}
	if v != "" && strings.Trim(v, "0123456789") != "" {
		r.fail("form140: %s field %d (%s) is %q, which is not all digits",
			r.rec.typ, no, fl.name, v)
		return
	}
	r.put(no, fl, idx, v)
}

func (r *row) setAmount(no int, v decimal.Decimal) {
	fl, idx, ok := r.field(no)
	if !ok {
		return
	}
	switch fl.k {
	case kindAmount:
	case kindWholeAmount:
		// "No fractional portion is allowed in this field ... value '1000.50'
		// will not be allowed, whereas value '1000.00' will be considered to be
		// valid." Both halves of that rule: paise must be zero, AND the value is
		// still written with two decimals.
		if !v.Equal(v.Truncate(0)) {
			r.fail("form140: %s field %d (%s) is %s, but this field admits no paise — "+
				"the deposit split must be in whole rupees", r.rec.typ, no, fl.name, v.String())
			return
		}
	default:
		r.fail("form140: %s field %d (%s) is not an amount field", r.rec.typ, no, fl.name)
		return
	}
	if v.IsNegative() {
		r.fail("form140: %s field %d (%s) is negative (%s); the format carries "+
			"magnitudes, never signs", r.rec.typ, no, fl.name, v.String())
		return
	}
	r.put(no, fl, idx, v.StringFixed(2))
}

func (r *row) setRate(no int, v decimal.Decimal) {
	fl, idx, ok := r.expect(no, kindRate)
	if !ok {
		return
	}
	if v.IsNegative() {
		r.fail("form140: %s field %d (%s) is a negative rate (%s)", r.rec.typ, no, fl.name, v.String())
		return
	}
	// General Note 5: "if the rate is 2 then the same should be mentioned as
	// 2.0000". Four decimals, always — which is exactly the scale
	// tds_deduction.rate_pct NUMERIC(9,4) was given for.
	r.put(no, fl, idx, v.StringFixed(4))
}

func (r *row) setDate(no int, t time.Time) {
	fl, idx, ok := r.expect(no, kindDate)
	if !ok {
		return
	}
	if t.IsZero() {
		r.fail("form140: %s field %d (%s) was set to the zero time", r.rec.typ, no, fl.name)
		return
	}
	if !r.horizon.IsZero() && t.After(r.horizon) {
		// General Note 8, and it is a real trap rather than a formality: a
		// deposit date typed as next year passes every arithmetic check in this
		// package and is rejected only by the FVU, which cannot run here.
		r.fail("form140: %s field %d (%s) is %s, which is after the file creation date %s — "+
			"General Note 8 forbids any future date", r.rec.typ, no, fl.name,
			t.Format("2006-01-02"), r.horizon.Format("2006-01-02"))
		return
	}
	r.put(no, fl, idx, t.Format("02012006"))
}

// render joins the record, after checking that every unconditionally-mandatory
// field carries a value.
//
// Conditional fields (field.cond) are skipped here BY DESIGN: their presence
// depends on the value of another field, which this layer cannot see. build.go
// owns them, and TestTheConditionalFieldsAreTheDocumentedSet fails if one is
// added to a layout without a rule being written for it — because a cond field
// with no rule is a mandatory field that nothing checks.
func (r *row) render() (string, error) {
	for i, fl := range r.rec.fields {
		if fl.n == needMandatory && !fl.cond && r.vals[i] == "" {
			r.fail("form140: %s field %d (%s) is mandatory and was not set",
				r.rec.typ, fl.no, fl.name)
		}
	}
	if len(r.errs) > 0 {
		return "", r.errs[0]
	}
	// General Note 10 holds structurally: one value per layout field, joined by
	// the delimiter, gives exactly len(fields)-1 delimiters on every path.
	return strings.Join(r.vals, delimiter), nil
}
