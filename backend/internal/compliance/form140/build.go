package form140

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// ErrNoDeducteeRecords is General Note 15: "At least one deductee/collectee
// details should be mentioned in regular statement. Statements with zero/no
// deductee/collectee details should not be allowed to validate through FVU."
//
// A quarter in which nothing was payable does not produce an empty Form 140 —
// it produces no Form 140. Emitting a deductee-less file would hand an operator
// something that looks filed and is not.
var ErrNoDeducteeRecords = errors.New(
	"form140: a regular statement must carry at least one deductee record")

// ErrNoValidReasonFlag is the refusal Annexure 6's validity matrix forces, and
// it is the single most surprising thing the primary spec settled.
//
// A deductee record with no tax on it has to say WHY. Annexure 6 flag `Y` is
// "payment below threshold limit" — but the matrix does not permit `Y` against
// 1008 or 1009 (rent, machinery and other). Rent HAS a threshold (₹50,000 a
// month), so a sub-threshold rent payment genuinely occurs, and there is no
// below-threshold flag available for it; 1009 admits only A, B, C and E, none
// of which is true of it.
//
// This build REFUSES rather than picking. The two available guesses are (a)
// leave field 32 empty, which files a record asserting that 0.00 of tax was
// deducted at 0.0000 for no stated reason, and (b) quote a flag the matrix
// forbids, which is a rejected statement at best and a false assertion about a
// certificate or a declaration at worst. The workbook does not say which — most
// likely such a payment is simply not reported in the annexure at all — and
// deciding that silently is precisely the class of guess this track exists to
// avoid. See HANDOFF-FEATURE-LOCK.md §1.1: cite it or refuse.
var ErrNoValidReasonFlag = errors.New(
	"form140: this payment carries no tax and Annexure 6 permits no reason flag for its section code")

// Build renders a complete Form 140 file.
//
// It is pure: same Statement in, same bytes out, no clock and no I/O. Every
// aggregate the format asks for — batch totals, challan totals, record counts,
// line numbers — is COMPUTED here from the records rather than taken from the
// caller. A supplied total that disagrees with the rows it heads is the classic
// way a return reconciles to nothing, and the FVU (which cannot run here) is
// where that would otherwise be discovered.
//
// ⚠️ WHAT THIS RETURNS HAS NOT BEEN VALIDATED, and no caller may say it has.
// See the package doc.
func Build(st Statement) ([]byte, error) {
	if err := validateStatement(st); err != nil {
		return nil, err
	}
	if err := validateDeductor(st.Deductor); err != nil {
		return nil, err
	}

	qStart, qEnd, ok := st.Period.bounds(st.TaxYearStart)
	if !ok {
		return nil, fmt.Errorf("form140: period %q is not Q1, Q2, Q3 or Q4", st.Period)
	}

	deducteeCount := 0
	batchDeposit := decimal.Zero
	for i := range st.Challans {
		c := &st.Challans[i]
		if err := validateChallan(c, i+1, st, qStart, qEnd); err != nil {
			return nil, err
		}
		deducteeCount += len(c.Deductees)
		batchDeposit = batchDeposit.Add(challanDeposit(c))
	}
	if deducteeCount == 0 {
		return nil, ErrNoDeducteeRecords
	}

	var lines []string
	lineNo := 0
	next := func() int { lineNo++; return lineNo }

	fh, err := buildFileHeader(st, next())
	if err != nil {
		return nil, err
	}
	lines = append(lines, fh)

	bh, err := buildBatchHeader(st, next(), batchDeposit)
	if err != nil {
		return nil, err
	}
	lines = append(lines, bh)

	// The deductee record number runs across the BATCH, not the challan.
	//
	// The workbook says only "Running serial no to indicate detail record no."
	// for DD field 4, while CD field 4 is explicitly "in a batch" — and since
	// there is a separate DD field 5 carrying the challan reference, a per-batch
	// numbering is the reading that makes field 5 necessary. RECORDED AS AN
	// INFERENCE rather than a citation, because it is one.
	ddNo := 0
	for i := range st.Challans {
		c := &st.Challans[i]
		cd, err := buildChallanDetail(c, i+1, next(), st.FileCreationDate)
		if err != nil {
			return nil, err
		}
		lines = append(lines, cd)
		for j := range c.Deductees {
			ddNo++
			dd, err := buildDeducteeDetail(&c.Deductees[j], i+1, ddNo, next(), st.FileCreationDate)
			if err != nil {
				return nil, err
			}
			lines = append(lines, dd)
		}
	}

	// General Note 2: EVERY record including the last ends with the terminator.
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l)
		b.WriteString(lineTerminator)
	}
	return []byte(b.String()), nil
}

// ---------------------------------------------------------------------------
// Statement- and deductor-level checks
// ---------------------------------------------------------------------------

func validateStatement(st Statement) error {
	if st.FileCreationDate.IsZero() {
		return errors.New("form140: the file creation date is required; it is also the horizon " +
			"General Note 8's no-future-date rule is measured against")
	}
	if st.FileSequenceNo <= 0 {
		return fmt.Errorf("form140: the file sequence number is %d; it must be positive and "+
			"unique across all files you send", st.FileSequenceNo)
	}
	if strings.TrimSpace(st.RPUName) == "" {
		return errors.New("form140: the name of the return preparation utility is mandatory (FH field 10)")
	}
	// "Value should be greater than or equal to 202728", i.e. this format
	// version begins at tax year 2026-27. An earlier year is not merely
	// unsupported here, it is outside the document.
	if st.TaxYearStart < 2026 {
		return fmt.Errorf("form140: tax year %d-%02d is before 2026-27, which is where this "+
			"format version begins; an earlier year needs its own file format",
			st.TaxYearStart, (st.TaxYearStart+1)%100)
	}
	if len(st.Challans) == 0 {
		return ErrNoDeducteeRecords
	}
	if st.PriorStatementFiled && strings.TrimSpace(st.PriorTokenNo) == "" {
		return errors.New("form140: BH field 52 says a regular statement was filed for an earlier " +
			"period, so the 15-digit token number of that statement is mandatory (BH field 9)")
	}
	if !st.PriorStatementFiled && st.PriorTokenNo != "" {
		return errors.New("form140: a previous-statement token number was supplied but BH field 52 " +
			"says no earlier regular statement was filed; the workbook allows a value only when it was")
	}
	return nil
}

func validateDeductor(d Deductor) error {
	if len(d.TAN) != 10 || d.TAN != strings.ToUpper(d.TAN) {
		return fmt.Errorf("form140: the deductor's TAN %q is not ten characters in capitals. "+
			"It is NOT the GSTIN and is not derivable from it", d.TAN)
	}
	if len(d.PAN) != 10 {
		return fmt.Errorf("form140: the deductor's PAN %q is not ten characters. Where a deductor "+
			"is not required to have one the workbook prescribes the literal PANNOTREQD", d.PAN)
	}
	if blank(d.Name) {
		return errors.New("form140: the deductor's name is mandatory and may not be only blanks, " +
			"dots or special characters (BH field 19)")
	}
	if strings.TrimSpace(d.Address1) == "" || strings.TrimSpace(d.Country) == "" {
		return errors.New("form140: the deductor's address line 1 and country/region are mandatory " +
			"(BH fields 21 and 20)")
	}
	if _, ok := StateCodes[d.StateCode]; !ok {
		return fmt.Errorf("form140: the deductor's state code %q is not in Annexure 1. The field "+
			"carries a two-digit CODE, not a state name", d.StateCode)
	}
	if len(d.Pincode) != 6 {
		return fmt.Errorf("form140: the deductor's pincode %q is not six digits", d.Pincode)
	}
	if _, ok := DeductorCategories[d.Category]; !ok {
		return fmt.Errorf("form140: deductor category %q is not an Annexure 4 code. Note this is "+
			"NOT compliance's s.402(11) entity category — mapping between the two is a decision, "+
			"not a derivation", d.Category)
	}
	// BH fields 28 and 42 are each printed M and each made conditional on the
	// other: "mandatory if no value is provided under field no. 42" and the
	// mirror. So at least one, and whichever is present must be well formed.
	if d.Email == "" && d.Responsible.Email == "" {
		return errors.New("form140: an e-mail address is mandatory — the workbook requires the " +
			"deductor's (BH 28) OR the responsible person's (BH 42), and neither was supplied")
	}
	if err := checkEmail("the deductor's e-mail (BH 28)", d.Email); err != nil {
		return err
	}
	if err := checkEmail("the responsible person's e-mail (BH 42)", d.Responsible.Email); err != nil {
		return err
	}
	if d.ContactCountryCode == "" || d.ContactNumber == "" {
		return errors.New("form140: the deductor's contact country code and number are both " +
			"mandatory (BH fields 29 and 30); this format version splits them")
	}

	// Annexure 5's State Name — mandatory for exactly four categories, and
	// forbidden for every other. Conditional in both directions, which is why
	// the layout marks it cond and the check lives here.
	switch {
	case stateNameCodeMandatory[d.Category]:
		if _, ok := StateCodes[d.StateName]; !ok {
			return fmt.Errorf("form140: deductor category %q (%s) requires the Annexure 5 state "+
				"code in BH field 54, and %q is not one", d.Category,
				DeductorCategories[d.Category], d.StateName)
		}
	case d.StateName != "":
		return fmt.Errorf("form140: BH field 54 (State Name) carries %q, but the workbook allows "+
			"a value only for the State-Government categories S, E, H and N — not %q (%s)",
			d.StateName, d.Category, DeductorCategories[d.Category])
	}

	// Annexure 3's Ministry — mandatory for three categories, optional for four
	// more, forbidden otherwise.
	switch {
	case ministryMandatory[d.Category]:
		if d.Ministry == "" {
			return fmt.Errorf("form140: deductor category %q (%s) requires an Annexure 3 ministry "+
				"code in BH field 57", d.Category, DeductorCategories[d.Category])
		}
	case !ministryOptional[d.Category] && d.Ministry != "":
		return fmt.Errorf("form140: BH field 57 (Ministry Name) carries %q, but the workbook "+
			"allows no value for deductor category %q (%s)", d.Ministry, d.Category,
			DeductorCategories[d.Category])
	}
	if d.Ministry == "99" && d.MinistryOther == "" {
		return errors.New("form140: ministry code 99 is \"Other\", so BH field 58 must name it")
	}
	if d.Ministry != "99" && d.MinistryOther != "" {
		return errors.New("form140: BH field 58 names an \"other\" ministry, but field 57 is not " +
			"the code 99 that field 58 exists to expand")
	}

	r := d.Responsible
	if blank(r.Name) || blank(r.Designation) {
		return errors.New("form140: the name and designation of the person responsible for " +
			"deduction are mandatory (BH fields 33 and 34)")
	}
	if len(r.PAN) != 10 {
		return fmt.Errorf("form140: the responsible person's own PAN %q is not ten characters "+
			"(BH field 59); it is a different field from the deductor's PAN", r.PAN)
	}
	if strings.TrimSpace(r.Address1) == "" || strings.TrimSpace(r.Country) == "" {
		return errors.New("form140: the responsible person's address line 1 and country/region " +
			"are mandatory (BH fields 35 and 43)")
	}
	if _, ok := StateCodes[r.StateCode]; !ok {
		return fmt.Errorf("form140: the responsible person's state code %q is not in Annexure 1", r.StateCode)
	}
	if len(r.Pincode) != 6 {
		return fmt.Errorf("form140: the responsible person's pincode %q is not six digits", r.Pincode)
	}
	if r.ContactCountryCode == "" || r.ContactNumber == "" {
		return errors.New("form140: the responsible person's contact country code and number are " +
			"both mandatory (BH fields 44 and 45)")
	}
	return nil
}

// blank reports whether a name field is "only blank values or only special
// characters or only dots, spaces etc.", which the workbook forbids for the
// deductor and responsible-person names.
func blank(s string) bool {
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// checkEmail applies the workbook's own four numbered rules for BH 28 / 42.
// An empty value passes: whether it is ALLOWED to be empty is the pair rule's
// business, not this function's.
func checkEmail(what, addr string) error {
	if addr == "" {
		return nil
	}
	at := strings.Index(addr, "@")
	// Rules 1 and 2: "at least @ and '.' should be mentioned"; "both @ and '.'
	// should be preceded and succeeded by at least one character".
	if at <= 0 || at == len(addr)-1 {
		return fmt.Errorf("form140: %s (%q) has no @ with a character on each side", what, addr)
	}
	// Rule 3: "at least one '.' should come after '@'".
	dot := strings.Index(addr[at:], ".")
	if dot <= 1 || at+dot == len(addr)-1 {
		return fmt.Errorf("form140: %s (%q) has no '.' after the @ with a character on each side",
			what, addr)
	}
	// Rule 4: "all printable characters allowed except '^' and space".
	if strings.ContainsAny(addr, "^ ") {
		return fmt.Errorf("form140: %s (%q) contains a space or a caret, both of which the "+
			"workbook excludes", what, addr)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Challan-level checks
// ---------------------------------------------------------------------------

func challanDeposit(c *Challan) decimal.Decimal {
	return c.TaxDeductedRupees.Add(c.InterestRupees).Add(c.FeeRupees).Add(c.PenaltyRupees)
}

func validateChallan(c *Challan, n int, st Statement, qStart, qEnd time.Time) error {
	for _, pair := range []struct {
		name string
		v    decimal.Decimal
	}{
		{"tax deducted", c.TaxDeductedRupees}, {"interest", c.InterestRupees},
		{"fee", c.FeeRupees}, {"penalty/other", c.PenaltyRupees},
	} {
		if pair.v.IsNegative() {
			return fmt.Errorf("form140: challan %d has a negative %s (%s)", n, pair.name, pair.v)
		}
	}

	if c.Nil {
		// "In cases where no tax has been deposited in bank, value should be Y".
		// Everything that identifies a deposit must then be absent, and the
		// deposit date is pinned: "Value should be equal to last date of
		// respective quarter if the value in NIL Challan Indicator is Y".
		if !challanDeposit(c).IsZero() {
			return fmt.Errorf("form140: challan %d is marked NIL but carries a deposit of %s; "+
				"a nil challan is one under which nothing was paid", n, challanDeposit(c))
		}
		if c.BSRCode != "" || c.ChallanNo != "" {
			return fmt.Errorf("form140: challan %d is marked NIL but carries a BSR code or challan "+
				"number; the workbook requires no value for either in a nil statement", n)
		}
		if !c.DepositDate.Equal(qEnd) {
			return fmt.Errorf("form140: challan %d is marked NIL, so its date must be the last day "+
				"of %s (%s), not %s", n, st.Period, qEnd.Format("2006-01-02"),
				c.DepositDate.Format("2006-01-02"))
		}
		// General Note 11. Every deductee under a nil challan must explain
		// itself, and C — the higher no-PAN rate — is deliberately not on the
		// list, because tax deducted at 20% would have had to be deposited.
		for i, d := range c.Deductees {
			if !nilChallanFlags[d.ReasonFlag] {
				return fmt.Errorf("form140: challan %d is NIL, so deductee record %d (%s) must "+
					"carry one of the Annexure 6 flags A, B, D, E, N, O, P, Q, Y, S, T or Z; "+
					"it carries %q", n, i+1, d.Name, d.ReasonFlag)
			}
		}
	} else {
		if len(c.BSRCode) != 7 {
			return fmt.Errorf("form140: challan %d has BSR code %q; it must be the seven-digit "+
				"code of the receiving branch (or, for book entry, the Form 24G receipt number)",
				n, c.BSRCode)
		}
		if c.DepositDate.IsZero() {
			return fmt.Errorf("form140: challan %d has no deposit date (CD field 19)", n)
		}
		// "It can be any date on or after 1st April of immediate previous
		// financial year for which the return is prepared."
		earliest := time.Date(st.TaxYearStart-1, time.April, 1, 0, 0, 0, 0, time.UTC)
		if c.DepositDate.Before(earliest) {
			return fmt.Errorf("form140: challan %d was deposited on %s, before 1 April %d — the "+
				"earliest date this statement may carry", n, c.DepositDate.Format("2006-01-02"),
				st.TaxYearStart-1)
		}
	}

	if c.MinorHead != "" {
		if _, ok := MinorHeads[c.MinorHead]; !ok {
			return fmt.Errorf("form140: challan %d has minor head %q, which is not in Annexure 7 "+
				"(200, 400 or 100)", n, c.MinorHead)
		}
		// The extra rule the workbook attaches to minor head 100 alone.
		if c.MinorHead == "100" && !c.Nil {
			if c.DepositDate.Before(qStart) || c.DepositDate.After(qEnd) {
				return fmt.Errorf("form140: challan %d carries minor head 100, so its date must "+
					"fall within %s (%s to %s); it is %s", n, st.Period,
					qStart.Format("2006-01-02"), qEnd.Format("2006-01-02"),
					c.DepositDate.Format("2006-01-02"))
			}
			for i, d := range c.Deductees {
				if c.DepositDate.After(d.PaymentDate) {
					return fmt.Errorf("form140: challan %d carries minor head 100 and was deposited "+
						"on %s, after deductee record %d was paid on %s", n,
						c.DepositDate.Format("2006-01-02"), i+1, d.PaymentDate.Format("2006-01-02"))
				}
			}
		}
	}

	for i := range c.Deductees {
		if err := validateDeductee(&c.Deductees[i], i+1, n, qStart, qEnd); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Deductee-level checks
// ---------------------------------------------------------------------------

// panPlaceholders are the workbook's own stand-ins, each meaning something
// different: a PAN applied for, a PAN quoted in an invalid format, and no
// declaration at all. They are not interchangeable and this package never
// substitutes one for another — it only knows that four section codes forbid
// all three.
var panPlaceholders = map[string]bool{
	"PANAPPLIED": true, "PANINVALID": true, "PANNOTAVBL": true,
}

func validateDeductee(d *Deductee, i, challanNo int, qStart, qEnd time.Time) error {
	where := fmt.Sprintf("challan %d deductee record %d (%s)", challanNo, i, d.Name)

	if len(d.PAN) != 10 {
		return fmt.Errorf("form140: %s has PAN %q, which is not ten characters. Where none is "+
			"available the workbook prescribes PANAPPLIED, PANINVALID or PANNOTAVBL — and those "+
			"three mean different things", where, d.PAN)
	}
	if blank(d.Name) {
		return fmt.Errorf("form140: %s has no name (DD field 9)", where)
	}
	if _, ok := PaymentCodes[d.SectionCode]; !ok {
		return fmt.Errorf("form140: %s carries section code %q, which is not in Annexure 2. This "+
			"package does not invent a code — compliance.TDSReturnCode decides it when the "+
			"deduction is derived", where, d.SectionCode)
	}
	if !d.AmountPaidRupees.GreaterThan(decimal.Zero) {
		return fmt.Errorf("form140: %s has an amount paid of %s; the workbook requires it to be "+
			"greater than 0.00", where, d.AmountPaidRupees)
	}
	if d.TaxDeductedRupees.IsNegative() || d.TaxDepositedRupees.IsNegative() {
		return fmt.Errorf("form140: %s has a negative tax amount", where)
	}
	if d.PaymentDate.Before(qStart) || d.PaymentDate.After(qEnd) {
		return fmt.Errorf("form140: %s was paid on %s, outside the quarter %s to %s this statement "+
			"covers", where, d.PaymentDate.Format("2006-01-02"),
			qStart.Format("2006-01-02"), qEnd.Format("2006-01-02"))
	}

	if codesRequiringRealPAN[d.SectionCode] {
		if panPlaceholders[d.PAN] {
			return fmt.Errorf("form140: %s carries section code %s, for which the workbook forbids "+
				"the default PAN placeholders — a real PAN is required", where, d.SectionCode)
		}
		if !d.TaxDeductedRupees.IsZero() || !d.TaxDepositedRupees.IsZero() || !d.RatePct.IsZero() {
			return fmt.Errorf("form140: %s carries section code %s, for which tax deducted, tax "+
				"deposited and the rate must all be 0.00 — the tax was borne by the deductor before "+
				"the benefit was released", where, d.SectionCode)
		}
	}

	// DD 27 and 28, and they pull in opposite directions: the date of deduction
	// is mandatory when tax was deducted and must carry NO VALUE when it was
	// not, while the rate must be 0.0000 rather than absent.
	if d.TaxDeductedRupees.IsZero() {
		if !d.DeductionDate.IsZero() {
			return fmt.Errorf("form140: %s deducted no tax, so DD field 27 (date of deduction) "+
				"must carry no value; it carries %s", where, d.DeductionDate.Format("2006-01-02"))
		}
		if !d.RatePct.IsZero() {
			return fmt.Errorf("form140: %s deducted no tax, so the rate must be 0.0000; it is %s",
				where, d.RatePct)
		}
	} else {
		if d.DeductionDate.IsZero() {
			return fmt.Errorf("form140: %s deducted %s of tax, so the date of deduction is "+
				"mandatory (DD field 27)", where, d.TaxDeductedRupees)
		}
		// "this date should not be less than the relevant quarter".
		if d.DeductionDate.Before(qStart) {
			return fmt.Errorf("form140: %s was deducted on %s, before the quarter beginning %s",
				where, d.DeductionDate.Format("2006-01-02"), qStart.Format("2006-01-02"))
		}
		if d.RatePct.IsZero() {
			return fmt.Errorf("form140: %s deducted %s of tax at a rate of zero", where,
				d.TaxDeductedRupees)
		}
	}
	if d.TaxDepositedRupees.GreaterThan(d.TaxDeductedRupees) {
		return fmt.Errorf("form140: %s reports %s deposited against %s deducted; more cannot be "+
			"paid over than was taken", where, d.TaxDepositedRupees, d.TaxDeductedRupees)
	}
	if d.Deposited && !d.TaxDepositedRupees.Equal(d.TaxDeductedRupees) {
		return fmt.Errorf("form140: %s answers \"Y\" to DD field 18 (has the tax deducted been "+
			"deposited) but reports %s deposited of %s deducted", where,
			d.TaxDepositedRupees, d.TaxDeductedRupees)
	}

	// Annexure 6 — the validity matrix, and the refusal it forces.
	if d.ReasonFlag != "" {
		if _, ok := ReasonFlags[d.ReasonFlag]; !ok {
			return fmt.Errorf("form140: %s carries reason flag %q, which is not one of Annexure "+
				"6's thirteen (A B C D E N O P Q S T Y Z)", where, d.ReasonFlag)
		}
		if !ReasonFlagValid(d.ReasonFlag, d.SectionCode) {
			legal := ReasonFlagsFor(d.SectionCode)
			return fmt.Errorf("form140: %s quotes Annexure 6 flag %q against section code %s, and "+
				"the annexure's validity matrix does not permit that pair. The flags legal against "+
				"%s are %v", where, d.ReasonFlag, d.SectionCode, d.SectionCode, legal)
		}
	} else if d.TaxDeductedRupees.IsZero() {
		// THIS RULE IS THIS BUILD'S, NOT THE WORKBOOK'S, and the distinction
		// matters. The workbook prints DD field 32 as Optional and makes it
		// mandatory only under a nil challan (General Note 11). But a record
		// reporting a payment with 0.00 of tax at 0.0000 and no reason asserts
		// nothing about WHY, and the reason is the only thing that distinguishes
		// a lawful sub-threshold payment from a missed deduction. So a reason is
		// required here whenever the tax is zero.
		if legal := ReasonFlagsFor(d.SectionCode); len(legal) == 0 {
			return fmt.Errorf("%w: %s reports a payment of %s with no tax under section code %s, "+
				"and Annexure 6 lists no flag at all against that code — see ErrNoValidReasonFlag",
				ErrNoValidReasonFlag, where, d.AmountPaidRupees, d.SectionCode)
		} else if !ReasonFlagValid("Y", d.SectionCode) {
			return fmt.Errorf("%w: %s reports a payment of %s with no tax under section code %s, "+
				"but Annexure 6 does not permit the below-threshold flag Y against that code. The "+
				"flags it does permit are %v, and none of them means \"below the threshold\"",
				ErrNoValidReasonFlag, where, d.AmountPaidRupees, d.SectionCode, legal)
		}
		return fmt.Errorf("form140: %s reports a payment of %s with no tax deducted and gives no "+
			"Annexure 6 reason; a zero-tax record must say why", where, d.AmountPaidRupees)
	}
	if d.ReasonFlag == "A" && len(d.CertificateNo395) != 15 {
		return fmt.Errorf("form140: %s claims a lower deduction on a s.395 certificate (flag A), "+
			"so the fifteen-character certificate number is mandatory (DD field 33); it is %q",
			where, d.CertificateNo395)
	}
	// Flag T: "PAN of deductee/payee is mandatory".
	if d.ReasonFlag == "T" && panPlaceholders[d.PAN] {
		return fmt.Errorf("form140: %s carries flag T (transporter), for which Annexure 6 makes "+
			"the deductee's PAN mandatory; %q is a placeholder", where, d.PAN)
	}

	// DD 22 and 23 belong to exactly one section code each and are forbidden
	// elsewhere.
	if !d.CashWithdrawalOver1Cr.IsZero() && d.SectionCode != "1065" {
		return fmt.Errorf("form140: %s sets DD field 22 (cash withdrawal over ₹1 crore), which the "+
			"workbook allows only against section code 1065", where)
	}
	if !d.CashWithdrawalOver3Cr.IsZero() && d.SectionCode != "1064" {
		return fmt.Errorf("form140: %s sets DD field 23 (cash withdrawal over ₹3 crore), which the "+
			"workbook allows only against section code 1064", where)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Record construction
// ---------------------------------------------------------------------------

func buildFileHeader(st Statement, lineNo int) (string, error) {
	r := newRow(&fileHeader, lineNo, st.FileCreationDate)
	r.setChar(fhFileType, fileTypeNonSalary)
	r.setChar(fhUploadType, uploadTypeRegular)
	r.setDate(fhCreated, st.FileCreationDate)
	r.setInt(fhSequenceNo, st.FileSequenceNo)
	r.setChar(fhUploader, uploaderTypeD)
	r.setChar(fhTAN, st.Deductor.TAN)
	// "Single Batch Header record for the entire file" — the workbook's own note
	// under the Batch Header table. One, always.
	r.setInt(fhBatchCount, 1)
	r.setChar(fhRPUName, st.RPUName)
	return r.render()
}

func buildBatchHeader(st Statement, lineNo int, batchDeposit decimal.Decimal) (string, error) {
	d := st.Deductor
	r := newRow(&batchHeader, lineNo, st.FileCreationDate)
	// "Value must start with 1", and there is exactly one batch.
	r.setInt(bhBatchNumber, 1)
	r.setInt(bhChallanCount, len(st.Challans))
	r.setChar(bhFormNumber, FormNumber)
	if st.PriorStatementFiled {
		r.setIntString(bhPriorToken, st.PriorTokenNo)
	}
	r.setChar(bhTAN, d.TAN)
	r.setChar(bhPAN, d.PAN)
	// "Assessment year e.g. value should be 202728 for Tax Year 2026-27" — the
	// assessment year is the tax year plus one, and both are written as the
	// pair of years with the second at two digits.
	r.setInt(bhAssessmentYear, yearPair(st.TaxYearStart+1))
	r.setInt(bhTaxYear, yearPair(st.TaxYearStart))
	r.setChar(bhPeriod, string(st.Period))
	r.setChar(bhDeductorName, d.Name)
	r.setChar(bhAddrCountry, d.Country)
	r.setChar(bhAddr1, d.Address1)
	r.setChar(bhAddr2, d.Address2)
	r.setChar(bhAddr3, d.Address3)
	r.setChar(bhAddr4, d.Address4)
	r.setChar(bhAddr5, d.Address5)
	r.setIntString(bhState, d.StateCode)
	r.setIntString(bhPincode, d.Pincode)
	r.setChar(bhEmail, d.Email)
	r.setIntString(bhContactCC, d.ContactCountryCode)
	r.setChar(bhContact, d.ContactNumber)
	r.setChar(bhDeductorType, d.Category)

	p := d.Responsible
	r.setChar(bhRespName, p.Name)
	r.setChar(bhRespDesignation, p.Designation)
	r.setChar(bhRespAddr1, p.Address1)
	r.setChar(bhRespAddr2, p.Address2)
	r.setChar(bhRespAddr3, p.Address3)
	r.setChar(bhRespAddr4, p.Address4)
	r.setChar(bhRespAddr5, p.Address5)
	r.setIntString(bhRespState, p.StateCode)
	r.setIntString(bhRespPin, p.Pincode)
	r.setChar(bhRespEmail, p.Email)
	r.setChar(bhRespCountry, p.Country)
	r.setIntString(bhRespContactCC, p.ContactCountryCode)
	r.setChar(bhRespContact, p.ContactNumber)
	r.setChar(bhRespPAN, p.PAN)

	// "The value here should be same as sum of values in field 'Total of Deposit
	// Amount as per Challan' in the Challan Detail record." Summed from the
	// challans rather than supplied, so it cannot disagree with them.
	r.setAmount(bhDepositTotal, batchDeposit)
	if st.PriorStatementFiled {
		r.setChar(bhPriorFiled, "Y")
	} else {
		r.setChar(bhPriorFiled, "N")
	}
	r.setChar(bhStateName, d.StateName)
	r.setChar(bhMinistry, d.Ministry)
	r.setChar(bhMinistryOther, d.MinistryOther)
	r.setChar(bhAIN, d.AIN)
	r.setChar(bhGSTIN, d.GSTIN)
	return r.render()
}

// yearPair renders a tax or assessment year as the workbook's six digits:
// 2026 -> 202627.
func yearPair(start int) int { return start*100 + (start+1)%100 }

func buildChallanDetail(c *Challan, n, lineNo int, horizon time.Time) (string, error) {
	r := newRow(&challanDetail, lineNo, horizon)
	r.setInt(cdBatchNumber, 1)
	r.setInt(cdRecordNumber, n)
	r.setInt(cdDeducteeCount, len(c.Deductees))
	if c.Nil {
		r.setChar(cdNilIndicator, nilChallanYes)
	} else {
		r.setChar(cdNilIndicator, nilChallanNo)
	}
	r.setAmount(cdTaxDeducted, c.TaxDeductedRupees)
	r.setAmount(cdInterest, c.InterestRupees)
	r.setAmount(cdFee, c.FeeRupees)
	r.setAmount(cdPenalty, c.PenaltyRupees)
	// "Value in this field should be equal to total of values in column numbers
	// B, C, D, E." Added here rather than accepted, for the same reason as the
	// batch total.
	r.setAmount(cdDepositTotal, challanDeposit(c))
	if !c.Nil {
		if c.ByBookEntry {
			r.setChar(cdMode, modeBookAdjust)
		} else {
			r.setChar(cdMode, modeChallan)
		}
		r.setIntString(cdBSRCode, c.BSRCode)
		r.setIntString(cdChallanNo, c.ChallanNo)
	}
	r.setDate(cdDepositDate, c.DepositDate)

	// CD 21 and 22 must tie to the annexure below them. Summed from the
	// deductee records; a mismatch here is impossible by construction rather
	// than caught by a test.
	deposited, deducted := decimal.Zero, decimal.Zero
	for _, d := range c.Deductees {
		deposited = deposited.Add(d.TaxDepositedRupees)
		deducted = deducted.Add(d.TaxDeductedRupees)
	}
	r.setAmount(cdAnnexDeposit, deposited)
	r.setAmount(cdAnnexDeducted, deducted)
	r.setIntString(cdMinorHead, c.MinorHead)
	return r.render()
}

func buildDeducteeDetail(d *Deductee, challanNo, ddNo, lineNo int, horizon time.Time) (string, error) {
	r := newRow(&deducteeDetail, lineNo, horizon)
	r.setInt(ddBatchNumber, 1)
	r.setInt(ddRecordNumber, ddNo)
	// "Running serial number for 'Challan Detail' records in a batch" — i.e.
	// this points at the CD record above, by its record number.
	r.setInt(ddChallanRef, challanNo)
	r.setChar(ddMode, ddModeO)
	r.setChar(ddPAN, d.PAN)
	r.setChar(ddName, d.Name)
	r.setChar(ddSectionCode, d.SectionCode)
	if d.Deposited {
		r.setChar(ddDeposited, "Y")
	} else {
		r.setChar(ddDeposited, "N")
	}
	r.setDate(ddPaymentDate, d.PaymentDate)
	r.setAmount(ddAmountPaid, d.AmountPaidRupees)
	if !d.CashWithdrawalOver1Cr.IsZero() {
		r.setAmount(ddCashOver1Cr, d.CashWithdrawalOver1Cr)
	}
	if !d.CashWithdrawalOver3Cr.IsZero() {
		r.setAmount(ddCashOver3Cr, d.CashWithdrawalOver3Cr)
	}
	r.setAmount(ddTaxDeducted, d.TaxDeductedRupees)
	r.setAmount(ddTaxDeposited, d.TaxDepositedRupees)
	// No value when no tax was deducted — validateDeductee has already refused
	// a date in that case, so this branch cannot silently drop one.
	if !d.DeductionDate.IsZero() {
		r.setDate(ddDeductDate, d.DeductionDate)
	}
	r.setRate(ddRate, d.RatePct)
	r.setChar(ddUINForm121, d.UINForm121)
	r.setChar(ddReasonFlag, d.ReasonFlag)
	r.setChar(ddCertificate, d.CertificateNo395)
	return r.render()
}
