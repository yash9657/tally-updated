package itc

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// ---------------------------------------------------------------------------
// THE PARSER IS TOLERANT ON PURPOSE, AND IT SAYS WHAT IT COULD NOT READ
// ---------------------------------------------------------------------------
//
// GSTN's own GSTR-2B JSON schema is behind a taxpayer login and IS NOT IN THIS
// REPOSITORY. What is decoded here is a GSP's rendering of it, learned
// second-hand. Two failure modes are available and only one of them is
// survivable:
//
//   - A STRICT parser against a guessed schema REJECTS REAL FILES. The operator
//     then has no reconciliation at all, which is loud, annoying and safe.
//   - A SILENT TOLERANT parser UNDER-REPORTS. A section it did not recognise is
//     a set of documents that are simply absent, and under model (b) absent
//     documents look EXACTLY LIKE A SUPPLIER WHO DID NOT FILE — they REVERSE
//     ITC. That is a wrong number nobody can see.
//
// So this parser is tolerant AND it counts what it skipped. Every skip is
// recorded in ParseResult.Skipped, and any skip at all raises the
// `statement-partially-parsed` disclosure, which raises `not-filing-ready`,
// which is what lets a frontend refuse to present the return as final.
//
// MONEY: the decoder runs with UseNumber(), so no JSON number is ever decoded
// into a float64. Every money field goes json.Number (or string) ->
// decimal.NewFromString. There is no float64, no strconv.ParseFloat and no
// json.Unmarshal-into-float anywhere in this package, and the acceptance gate
// greps for exactly that.

// ParseResult is one ingested statement file, decoded.
type ParseResult struct {
	// Kind is "GSTR2B" or "GSTR2A".
	Kind string
	// ReturnPeriod is the statement's own period in MMYYYY form.
	ReturnPeriod string
	// FilerGSTIN is the GSTIN the statement was generated FOR (ours).
	FilerGSTIN string
	// GenDate is the statement's own generation date. It is PREFERRED over any
	// computed 14th when resolving deemed acceptance — see ResolutionInstant and
	// the Dec-2024 slip it exists for.
	GenDate *time.Time
	Chksum  string
	Version string
	// SourceSHA256 is the hex digest over the RAW BYTES as supplied. It is what
	// makes re-uploading the identical file idempotent and what makes a
	// DIFFERENT file for the same period a new, superseding statement.
	SourceSHA256 string
	Documents    []StatementRow
	// Skipped names everything the parser recognised as data but could not
	// decode. Non-empty means `statement-partially-parsed`.
	Skipped []string
}

// Partial reports whether anything was skipped.
func (r ParseResult) Partial() bool { return len(r.Skipped) > 0 }

// sectionSpec describes how one statement section is laid out. Keeping the
// layout as DATA rather than as ten near-identical parse functions is what makes
// the tolerance auditable: you can read off exactly which spellings are accepted.
type sectionSpec struct {
	// jsonKeys are the spellings of the section's key under docdata.
	jsonKeys []string
	section  string
	// itemKeys are the spellings of the nested per-document array inside each
	// supplier block. Empty means the section is FLAT (imports carry no supplier
	// block at all).
	itemKeys []string
	// docNoKeys / docDateKeys are the spellings of the document's own number and
	// date.
	docNoKeys   []string
	docDateKeys []string
	// typeKeys are the spellings of the note-type field ("C"/"D"). Empty means
	// the section's documents are always invoices.
	typeKeys []string
	// origNoKeys / origDateKeys are the spellings of the ORIGINAL document an
	// amendment amends.
	origNoKeys   []string
	origDateKeys []string
}

// sectionSpecs is the closed table of sections this parser understands. A
// section GSTN adds later is SKIPPED AND REPORTED, never silently dropped.
var sectionSpecs = []sectionSpec{
	{
		jsonKeys: []string{"b2b"}, section: SectionB2B,
		itemKeys: []string{"inv"}, docNoKeys: []string{"inum"}, docDateKeys: []string{"dt"},
	},
	{
		jsonKeys: []string{"b2ba"}, section: SectionB2BA,
		itemKeys: []string{"inv"}, docNoKeys: []string{"inum"}, docDateKeys: []string{"dt"},
		origNoKeys: []string{"oinum"}, origDateKeys: []string{"oidt"},
	},
	{
		jsonKeys: []string{"cdnr"}, section: SectionCDNR,
		itemKeys: []string{"nt"}, docNoKeys: []string{"ntnum"}, docDateKeys: []string{"dt"},
		typeKeys: []string{"nttyp", "ntty", "typ"},
	},
	{
		jsonKeys: []string{"cdnra"}, section: SectionCDNRA,
		itemKeys: []string{"nt"}, docNoKeys: []string{"ntnum"}, docDateKeys: []string{"dt"},
		typeKeys: []string{"nttyp", "ntty", "typ"},
		// GSTN spells the original note number ontnum in some renderings and
		// ontnum/ont_num in others; the original TYPE is carried as ntty.
		origNoKeys: []string{"ontnum", "ont_num", "oantnum"}, origDateKeys: []string{"ontdt", "ont_dt"},
	},
	{
		jsonKeys: []string{"impg"}, section: SectionIMPG,
		docNoKeys: []string{"benum", "boenum"}, docDateKeys: []string{"bedt", "boedt"},
	},
	{
		jsonKeys: []string{"impgsez"}, section: SectionIMPGSEZ,
		docNoKeys: []string{"benum", "boenum"}, docDateKeys: []string{"bedt", "boedt"},
	},
	{
		jsonKeys: []string{"isd"}, section: SectionISD,
		itemKeys: []string{"doclist", "docs"}, docNoKeys: []string{"docnum", "isddocnum"},
		docDateKeys: []string{"docdt", "isddocdt"},
	},
	{
		jsonKeys: []string{"isda"}, section: SectionISDA,
		itemKeys: []string{"doclist", "docs"}, docNoKeys: []string{"docnum", "isddocnum"},
		docDateKeys: []string{"docdt", "isddocdt"},
		origNoKeys:  []string{"odocnum"}, origDateKeys: []string{"odocdt"},
	},
	{
		jsonKeys: []string{"ecom", "b2bec"}, section: SectionECOM,
		itemKeys: []string{"doc", "inv"}, docNoKeys: []string{"docnum", "inum"},
		docDateKeys: []string{"docdt", "dt"},
	},
	{
		jsonKeys: []string{"ecoma", "b2beca"}, section: SectionECOMA,
		itemKeys: []string{"doc", "inv"}, docNoKeys: []string{"docnum", "inum"},
		docDateKeys: []string{"docdt", "dt"},
		origNoKeys:  []string{"odocnum", "oinum"}, origDateKeys: []string{"odocdt", "oidt"},
	},
}

// Parse2B decodes a GSTR-2B (or 2A) JSON file.
//
// kind is "GSTR2B" or "GSTR2A" and is what the caller intends to store; the
// file's own shape is not used to guess it, because a 2A and a 2B in this
// rendering are structurally identical and guessing wrong would put a 2A's
// documents under a 2B's deemed-acceptance clock.
func Parse2B(raw []byte, kind string) (ParseResult, error) {
	sum := sha256.Sum256(raw)
	out := ParseResult{Kind: kind, SourceSHA256: hex.EncodeToString(sum[:])}

	dec := json.NewDecoder(bytes.NewReader(raw))
	// UseNumber is the whole reason this is a Decoder and not json.Unmarshal.
	// Without it encoding/json decodes every JSON number into a float64 and the
	// paisa are gone before any of our code runs.
	dec.UseNumber()
	var top any
	if err := dec.Decode(&top); err != nil {
		return out, fmt.Errorf("itc: decode statement JSON: %w", err)
	}

	root, ok := top.(map[string]any)
	if !ok {
		return out, fmt.Errorf("itc: statement JSON root is not an object")
	}
	// Some renderings wrap everything in {"data": {...}}; some do not.
	if d, ok := obj(root, "data"); ok {
		root = d
	}

	out.FilerGSTIN = NormalizeGSTIN(str(root, "gstin"))
	out.ReturnPeriod = strings.TrimSpace(str(root, "rtnprd", "ret_period", "fp"))
	out.Chksum = str(root, "chksum")
	out.Version = str(root, "version", "ver")
	if g := str(root, "gendt", "gen_date", "gendate"); g != "" {
		if t, err := parseDate(g); err == nil {
			out.GenDate = &t
		} else {
			out.Skipped = append(out.Skipped, "gendt="+g)
		}
	}

	// The live documents, and then the REJECTED ones.
	//
	// docRejdata rows are ingested with rejected=true and are VIEW-ONLY (GSTN
	// IMS FAQ 24). They are stored so the operator can see them, and they are
	// flagged so nothing downstream can sum them into ITC. Dropping them at
	// ingest instead would be safe for the arithmetic and useless for the human,
	// who needs to know a supplier reported something against their GSTIN.
	for _, spec := range []struct {
		keys     []string
		rejected bool
	}{
		{keys: []string{"docdata", "doc_data"}, rejected: false},
		{keys: []string{"docRejdata", "docrejdata", "doc_rej_data"}, rejected: true},
	} {
		blk, ok := obj(root, spec.keys...)
		if !ok {
			continue
		}
		docs, skipped := parseDocData(blk, spec.rejected)
		out.Documents = append(out.Documents, docs...)
		out.Skipped = append(out.Skipped, skipped...)
	}

	// Every recognised key that is NOT a section and NOT a known header field is
	// reported, so a section GSTN adds later shows up as a skip rather than as
	// silently missing credit.
	out.Skipped = append(out.Skipped, unknownSections(root)...)
	sort.Strings(out.Skipped)
	return out, nil
}

// knownHeaderKeys are the root keys that are not document sections.
var knownHeaderKeys = map[string]bool{
	"gstin": true, "rtnprd": true, "ret_period": true, "fp": true,
	"chksum": true, "version": true, "ver": true,
	"gendt": true, "gen_date": true, "gendate": true,
	"docdata": true, "doc_data": true,
	"docRejdata": true, "docrejdata": true, "doc_rej_data": true,
	"itcsumm": true, "itc_summ": true, "summary": true,
}

func unknownSections(root map[string]any) []string {
	var out []string
	for k := range root {
		if !knownHeaderKeys[k] {
			out = append(out, "unknown-root-key="+k)
		}
	}
	return out
}

// parseDocData walks one docdata / docRejdata block.
func parseDocData(blk map[string]any, rejected bool) ([]StatementRow, []string) {
	var rows []StatementRow
	var skipped []string

	handled := map[string]bool{}
	for _, spec := range sectionSpecs {
		for _, key := range spec.jsonKeys {
			v, ok := blk[key]
			if !ok {
				continue
			}
			handled[key] = true
			arr, ok := v.([]any)
			if !ok {
				skipped = append(skipped, key+"=not-an-array")
				continue
			}
			r, s := parseSection(spec, arr, rejected)
			rows = append(rows, r...)
			skipped = append(skipped, s...)
		}
	}
	for k := range blk {
		if !handled[k] {
			skipped = append(skipped, "unhandled-section="+k)
		}
	}
	return rows, skipped
}

// parseSection walks one section array. Each element is either a SUPPLIER BLOCK
// carrying ctin plus a nested document array, or — for the flat sections
// (imports) — a document in its own right.
func parseSection(spec sectionSpec, arr []any, rejected bool) ([]StatementRow, []string) {
	var rows []StatementRow
	var skipped []string

	for i, el := range arr {
		blk, ok := el.(map[string]any)
		if !ok {
			skipped = append(skipped, fmt.Sprintf("%s[%d]=not-an-object", spec.section, i))
			continue
		}

		supplier := supplierHeader(blk)

		// Find the nested document array, if this section has one.
		var docs []any
		for _, ik := range spec.itemKeys {
			if v, ok := blk[ik]; ok {
				if a, ok := v.([]any); ok {
					docs = a
				} else {
					skipped = append(skipped, fmt.Sprintf("%s[%d].%s=not-an-array", spec.section, i, ik))
				}
				break
			}
		}
		if docs == nil {
			// FLAT section, or a supplier block whose item array is spelled
			// something we do not know. Distinguish the two by whether the block
			// itself carries a document number: if it does, it IS the document.
			if str(blk, spec.docNoKeys...) != "" || len(spec.itemKeys) == 0 {
				docs = []any{blk}
			} else {
				skipped = append(skipped, fmt.Sprintf("%s[%d]=no-recognised-item-array", spec.section, i))
				continue
			}
		}

		for j, de := range docs {
			d, ok := de.(map[string]any)
			if !ok {
				skipped = append(skipped, fmt.Sprintf("%s[%d][%d]=not-an-object", spec.section, i, j))
				continue
			}
			row, err := parseDocument(spec, supplier, d, rejected)
			if err != nil {
				skipped = append(skipped, fmt.Sprintf("%s[%d][%d]=%v", spec.section, i, j, err))
				continue
			}
			rows = append(rows, row)
		}
	}
	return rows, skipped
}

// supplierBlock is the per-filer header that wraps a section's documents.
type supplierBlock struct {
	gstin      string
	tradeName  string
	returnPrd  string
	filedDate  time.Time
	haveFiledD bool
}

func supplierHeader(blk map[string]any) supplierBlock {
	s := supplierBlock{
		gstin:     NormalizeGSTIN(str(blk, "ctin", "gstin", "supgstin")),
		tradeName: str(blk, "trdnm", "trade_name", "tradename", "supname"),
		returnPrd: str(blk, "supprd", "sup_prd", "supretprd"),
	}
	if fd := str(blk, "supfildt", "sup_fil_dt", "supfiledt"); fd != "" {
		if t, err := parseDate(fd); err == nil {
			s.filedDate, s.haveFiledD = t, true
		}
	}
	return s
}

// parseDocument decodes one document. A document with no number or no date
// cannot be matched against anything, so it is an ERROR (which becomes a
// reported skip) rather than a row with empty keys that would silently
// near-match everything.
func parseDocument(spec sectionSpec, sup supplierBlock, d map[string]any, rejected bool) (StatementRow, error) {
	docNo := strings.TrimSpace(str(d, spec.docNoKeys...))
	if docNo == "" {
		return StatementRow{}, fmt.Errorf("no document number")
	}
	rawDate := str(d, spec.docDateKeys...)
	docDate, err := parseDate(rawDate)
	if err != nil {
		return StatementRow{}, fmt.Errorf("document date %q: %w", rawDate, err)
	}

	row := StatementRow{
		Section:              spec.section,
		Rejected:             rejected,
		SupplierGSTIN:        sup.gstin,
		SupplierTradeName:    sup.tradeName,
		SupplierReturnPeriod: sup.returnPrd,
		DocNo:                docNo,
		DocDate:              docDate,
		DocType:              DocInvoice,
		POS:                  str(d, "pos"),
		IRN:                  str(d, "irn"),
		IMSStatusInFile:      str(d, "ims_status", "imsstatus", "imsaction", "srcfilstatus"),
		Tax:                  ZeroTax(),
		Taxable:              decimal.Zero,
		DocValue:             decimal.Zero,
		Cess:                 decimal.Zero,
		// GSTN marks itcavl="Y" on the ordinary document. Defaulting to AVAILABLE
		// when the key is absent is the deliberate choice: a rendering that omits
		// the field entirely (several do) must not be read as "GSTN says this
		// credit is barred", which would move every document in the file into
		// 4(D)(2) and produce a return claiming nothing.
		ITCAvailable: true,
	}
	if sup.haveFiledD {
		row.SupplierFiledDate = sup.filedDate
	}

	if t := strings.ToUpper(strings.TrimSpace(str(d, spec.typeKeys...))); t != "" {
		switch {
		case strings.HasPrefix(t, "C"):
			row.DocType = DocCreditNote
		case strings.HasPrefix(t, "D"):
			row.DocType = DocDebitNote
		}
	}

	row.ReverseCharge = yes(str(d, "rev", "reverse_charge", "rchrg"))
	if v := str(d, "itcavl", "itc_avl", "itcavail"); v != "" {
		row.ITCAvailable = yes(v)
	}
	row.ITCUnavailableReason = str(d, "rsn", "reason", "itcrsn")

	if v, ok, err := money(d, "val", "doc_value", "docval"); err != nil {
		return StatementRow{}, fmt.Errorf("doc value: %w", err)
	} else if ok {
		row.DocValue = v
	}

	// Tax at INVOICE level; falling back to the itms[] nesting when absent.
	taxable, tax, cess, found, err := invoiceLevelTax(d)
	if err != nil {
		return StatementRow{}, err
	}
	if !found {
		taxable, tax, cess, err = nestedItemTax(d)
		if err != nil {
			return StatementRow{}, err
		}
	}
	row.Taxable, row.Tax, row.Cess = taxable, tax, cess

	if len(spec.origNoKeys) > 0 {
		row.OrigDocNo = strings.TrimSpace(str(d, spec.origNoKeys...))
		if od := str(d, spec.origDateKeys...); od != "" {
			if t, err := parseDate(od); err == nil {
				row.OrigDocDate = t
			}
		}
	}
	return row, nil
}

// invoiceLevelTax reads the tax heads stated directly on the document, in either
// spelling. found is false when NONE of them is present, which is the signal to
// try the nested form.
func invoiceLevelTax(d map[string]any) (taxable decimal.Decimal, tax Tax, cess decimal.Decimal, found bool, err error) {
	taxable, tax, cess = decimal.Zero, ZeroTax(), decimal.Zero
	get := func(keys ...string) (decimal.Decimal, bool, error) { return money(d, keys...) }

	if v, ok, e := get("txval", "taxable_value", "txvl"); e != nil {
		return taxable, tax, cess, false, fmt.Errorf("txval: %w", e)
	} else if ok {
		taxable, found = v, true
	}
	if v, ok, e := get("cgst", "camt"); e != nil {
		return taxable, tax, cess, false, fmt.Errorf("cgst: %w", e)
	} else if ok {
		tax.CGST, found = v, true
	}
	if v, ok, e := get("sgst", "samt"); e != nil {
		return taxable, tax, cess, false, fmt.Errorf("sgst: %w", e)
	} else if ok {
		tax.SGST, found = v, true
	}
	if v, ok, e := get("igst", "iamt"); e != nil {
		return taxable, tax, cess, false, fmt.Errorf("igst: %w", e)
	} else if ok {
		tax.IGST, found = v, true
	}
	if v, ok, e := get("cess", "csamt"); e != nil {
		return taxable, tax, cess, false, fmt.Errorf("cess: %w", e)
	} else if ok {
		cess, found = v, true
	}
	return taxable, tax, cess, found, nil
}

// nestedItemTax sums the per-rate itms[] block. Each element is either the tax
// fields directly, or {"num": n, "itm_det": {...}}.
//
// SUMMING THE NESTED FORM IS NOT RATE-LEVEL MATCHING. It produces an
// invoice-grain total, which is what Match compares. Per-rate comparison is
// OUT OF SCOPE and is disclosed in doc.go — a multi-rate invoice whose total
// agrees but whose split is wrong will report MATCHED.
func nestedItemTax(d map[string]any) (decimal.Decimal, Tax, decimal.Decimal, error) {
	taxable, tax, cess := decimal.Zero, ZeroTax(), decimal.Zero
	raw, ok := d["itms"]
	if !ok {
		raw, ok = d["items"]
	}
	if !ok {
		return taxable, tax, cess, nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return taxable, tax, cess, fmt.Errorf("itms is not an array")
	}
	for _, el := range arr {
		m, ok := el.(map[string]any)
		if !ok {
			return taxable, tax, cess, fmt.Errorf("itms element is not an object")
		}
		if det, ok := obj(m, "itm_det", "itmdet", "det"); ok {
			m = det
		}
		add := func(dst *decimal.Decimal, keys ...string) error {
			v, ok, err := money(m, keys...)
			if err != nil {
				return err
			}
			if ok {
				*dst = dst.Add(v)
			}
			return nil
		}
		if err := add(&taxable, "txval", "taxable_value"); err != nil {
			return taxable, tax, cess, fmt.Errorf("itms txval: %w", err)
		}
		if err := add(&tax.CGST, "cgst", "camt"); err != nil {
			return taxable, tax, cess, fmt.Errorf("itms cgst: %w", err)
		}
		if err := add(&tax.SGST, "sgst", "samt"); err != nil {
			return taxable, tax, cess, fmt.Errorf("itms sgst: %w", err)
		}
		if err := add(&tax.IGST, "igst", "iamt"); err != nil {
			return taxable, tax, cess, fmt.Errorf("itms igst: %w", err)
		}
		if err := add(&cess, "cess", "csamt"); err != nil {
			return taxable, tax, cess, fmt.Errorf("itms cess: %w", err)
		}
	}
	return taxable, tax, cess, nil
}

// ---------------------------------------------------------------------------
// Scalar readers. Each accepts several spellings and NONE of them ever produces
// a float.
// ---------------------------------------------------------------------------

func obj(m map[string]any, keys ...string) (map[string]any, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if o, ok := v.(map[string]any); ok {
				return o, true
			}
		}
	}
	return nil, false
}

// str reads a string-ish scalar. A json.Number is rendered by its ORIGINAL
// TEXT (json.Number IS a string), never through a float, so an invoice number
// a supplier emitted unquoted survives byte-for-byte.
func str(m map[string]any, keys ...string) string {
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			continue
		}
		switch t := v.(type) {
		case string:
			return t
		case json.Number:
			return t.String()
		case bool:
			if t {
				return "Y"
			}
			return "N"
		}
	}
	return ""
}

// money reads a money scalar as a decimal.Decimal.
//
// THE ONLY TWO ACCEPTED FORMS ARE json.Number AND string, and both go through
// decimal.NewFromString. A float64 arriving here is IMPOSSIBLE because the
// decoder runs with UseNumber() — and if a future edit removes UseNumber, this
// function returns an error naming the field rather than silently accepting a
// value that has already lost its paisa. That is the point of the default case.
func money(m map[string]any, keys ...string) (decimal.Decimal, bool, error) {
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			continue
		}
		switch t := v.(type) {
		case json.Number:
			d, err := decimal.NewFromString(t.String())
			if err != nil {
				return decimal.Zero, false, fmt.Errorf("%q is not a decimal: %w", t.String(), err)
			}
			return d, true, nil
		case string:
			s := strings.TrimSpace(t)
			if s == "" {
				return decimal.Zero, true, nil
			}
			d, err := decimal.NewFromString(s)
			if err != nil {
				return decimal.Zero, false, fmt.Errorf("%q is not a decimal: %w", s, err)
			}
			return d, true, nil
		case nil:
			return decimal.Zero, true, nil
		default:
			return decimal.Zero, false, fmt.Errorf(
				"field %q decoded as %T, not json.Number or string — the JSON decoder "+
					"is not running with UseNumber() and money has lost precision", k, v)
		}
	}
	return decimal.Zero, false, nil
}

// yes reads GSTN's Y/N booleans, tolerating true/false and 1/0.
func yes(s string) bool {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "Y", "YES", "TRUE", "1":
		return true
	default:
		return false
	}
}

// dateLayouts are the forms a GSTN JSON date has been seen in. DD-MM-YYYY is the
// portal's usual form; ISO is accepted because several GSP renderings normalise
// to it.
//
// THE PARSE IS IN IST, not UTC. A date parsed in UTC and later rendered in IST
// is the SAME DAY, but a date parsed in UTC and compared against an IST midnight
// boundary is five and a half hours out — and this package compares dates
// against IST midnights everywhere (s.16(4), the deemed-accept cutoff).
var dateLayouts = []string{"02-01-2006", "2006-01-02", "02/01/2006", "2006/01/02"}

func parseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty date")
	}
	for _, l := range dateLayouts {
		if t, err := time.ParseInLocation(l, s, IST); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised date format %q", s)
}
