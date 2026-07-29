package itc

import (
	"encoding/json"
	"strings"
	"testing"
)

// sample2B is a GSTR-2B in the GSP rendering this parser was written against.
// It deliberately exercises BOTH tax spellings, BOTH the invoice-level and the
// itms[]-nested forms, an UNQUOTED money number, a CDNR credit note, an import
// row, and a docRejdata block.
const sample2B = `{
  "data": {
    "gstin": "29zzzzz0000z1z5",
    "rtnprd": "012026",
    "version": "2.0",
    "gendt": "14-02-2026",
    "chksum": "abc123",
    "docdata": {
      "b2b": [
        {
          "ctin": "29ABCDE1234F1Z5",
          "trdnm": "Karnataka Supplies Pvt Ltd",
          "supprd": "012026",
          "supfildt": "11-02-2026",
          "inv": [
            {
              "inum": "INV-1", "dt": "05-01-2026", "val": "11800.00",
              "pos": "29", "rev": "N", "itcavl": "Y", "rsn": "",
              "txval": "10000.00", "cgst": "900.00", "sgst": "900.00",
              "igst": "0.00", "cess": "0.00", "irn": "IRN-1"
            },
            {
              "inum": "INV-2", "dt": "09-01-2026", "val": 2360,
              "pos": "29", "rev": "N", "itcavl": "N",
              "rsn": "Return filed post annual cut-off date",
              "camt": "180.00", "samt": "180.00", "iamt": "0.00", "txval": 2000
            },
            {
              "inum": "INV-3", "dt": "12-01-2026", "val": "5900.00", "pos": "29",
              "itms": [
                {"num": 1, "itm_det": {"txval": "3000.00", "camt": "270.00", "samt": "270.00", "iamt": "0.00"}},
                {"num": 2, "itm_det": {"txval": "2000.00", "camt": "180.00", "samt": "180.00", "iamt": "0.00"}}
              ]
            }
          ]
        }
      ],
      "cdnr": [
        {
          "ctin": "29ABCDE1234F1Z5",
          "nt": [
            {"ntnum": "CN-9", "nttyp": "C", "dt": "20-01-2026", "val": "2360.00",
             "txval": "2000.00", "cgst": "180.00", "sgst": "180.00", "igst": "0.00"}
          ]
        }
      ],
      "impg": [
        {"benum": "BE-77", "bedt": "15-01-2026", "txval": "50000.00", "iamt": "9000.00", "portcode": "INMAA1"}
      ]
    },
    "docRejdata": {
      "b2b": [
        {
          "ctin": "27PQRST5678G1Z9",
          "inv": [
            {"inum": "REJ-1", "dt": "07-01-2026", "txval": "99999.00",
             "cgst": "8999.91", "sgst": "8999.91", "igst": "0.00"}
          ]
        }
      ]
    }
  }
}`

func TestParse2B(t *testing.T) {
	res, err := Parse2B([]byte(sample2B), "GSTR2B")
	if err != nil {
		t.Fatalf("Parse2B: %v", err)
	}

	if res.FilerGSTIN != "29ZZZZZ0000Z1Z5" {
		t.Fatalf("filer GSTIN %q, want the uppercased form", res.FilerGSTIN)
	}
	if res.ReturnPeriod != "012026" {
		t.Fatalf("return period %q", res.ReturnPeriod)
	}
	if res.GenDate == nil {
		t.Fatalf("gendt was not read; ResolutionInstant would fall back to the " +
			"hard-coded 14th, which has already slipped once (Dec-2024 -> 16-Jan-2025)")
	}
	if got := res.GenDate.In(IST).Format("2006-01-02"); got != "2026-02-14" {
		t.Fatalf("gendt %s, want 2026-02-14 (DD-MM-YYYY must not be read as MM-DD-YYYY)", got)
	}
	if len(res.SourceSHA256) != 64 {
		t.Fatalf("source sha256 %q is not a 64-character hex digest", res.SourceSHA256)
	}
	if res.Partial() {
		t.Fatalf("the sample was reported partially parsed: %v", res.Skipped)
	}

	byNo := map[string]StatementRow{}
	for _, r := range res.Documents {
		byNo[r.DocNo] = r
	}
	if len(res.Documents) != 6 {
		t.Fatalf("want 6 documents (3 b2b + 1 cdnr + 1 impg + 1 rejected), got %d: %v",
			len(res.Documents), byNo)
	}

	t.Run("invoice_level_tax_and_supplier_header", func(t *testing.T) {
		r := byNo["INV-1"]
		if r.Section != SectionB2B || r.DocType != DocInvoice {
			t.Fatalf("INV-1 section/type = %s/%s", r.Section, r.DocType)
		}
		if !r.Taxable.Equal(d("10000.00")) || !r.Tax.CGST.Equal(d("900.00")) {
			t.Fatalf("INV-1 taxable=%s cgst=%s", r.Taxable, r.Tax.CGST)
		}
		if r.SupplierGSTIN != "29ABCDE1234F1Z5" || r.SupplierTradeName == "" {
			t.Fatalf("INV-1 supplier header not carried down: %+v", r)
		}
		if got := r.SupplierFiledDate.In(IST).Format("2006-01-02"); got != "2026-02-11" {
			t.Fatalf("INV-1 supplier filed date %s, want 2026-02-11 — it is what the 2B "+
				"window is on, NOT the document date", got)
		}
		if !r.ITCAvailable {
			t.Fatalf("INV-1 itcavl=Y was read as unavailable")
		}
		if r.IRN != "IRN-1" {
			t.Fatalf("INV-1 irn %q", r.IRN)
		}
	})

	t.Run("camt_samt_iamt_spelling_and_unquoted_numbers", func(t *testing.T) {
		r := byNo["INV-2"]
		if !r.Tax.CGST.Equal(d("180.00")) || !r.Tax.SGST.Equal(d("180.00")) {
			t.Fatalf("INV-2 camt/samt spelling not accepted: %+v", r.Tax)
		}
		// txval and val arrived as UNQUOTED JSON numbers. They must survive as
		// exact decimals — UseNumber is what makes that true.
		if !r.Taxable.Equal(d("2000")) {
			t.Fatalf("INV-2 unquoted txval = %s, want 2000", r.Taxable)
		}
		if !r.DocValue.Equal(d("2360")) {
			t.Fatalf("INV-2 unquoted val = %s, want 2360", r.DocValue)
		}
		if r.ITCAvailable {
			t.Fatalf("INV-2 carries itcavl=N and must be read as UNAVAILABLE — GSTN's " +
				"verdict is the one the department will use")
		}
		if r.ITCUnavailableReason == "" {
			t.Fatalf("INV-2's rsn was dropped; the filer cannot see WHY GSTN barred it")
		}
	})

	t.Run("nested_itms_are_summed_to_invoice_grain", func(t *testing.T) {
		r := byNo["INV-3"]
		if !r.Taxable.Equal(d("5000.00")) {
			t.Fatalf("INV-3 nested txval sum = %s, want 5000.00", r.Taxable)
		}
		if !r.Tax.CGST.Equal(d("450.00")) || !r.Tax.SGST.Equal(d("450.00")) {
			t.Fatalf("INV-3 nested tax sum = %+v, want 450/450", r.Tax)
		}
	})

	t.Run("cdnr_note_type", func(t *testing.T) {
		r := byNo["CN-9"]
		if r.Section != SectionCDNR || r.DocType != DocCreditNote {
			t.Fatalf("CN-9 section/type = %s/%s, want %s/%s", r.Section, r.DocType, SectionCDNR, DocCreditNote)
		}
		// The statement reports the note's value POSITIVELY; the direction is in
		// the type.
		if r.Taxable.IsNegative() {
			t.Fatalf("CN-9 taxable %s: a 2B credit note is reported as a positive magnitude", r.Taxable)
		}
	})

	t.Run("flat_import_section", func(t *testing.T) {
		r := byNo["BE-77"]
		if r.Section != SectionIMPG {
			t.Fatalf("BE-77 section %s, want %s", r.Section, SectionIMPG)
		}
		if !r.Tax.IGST.Equal(d("9000.00")) {
			t.Fatalf("BE-77 IGST %s", r.Tax.IGST)
		}
		if r.SupplierGSTIN != "" {
			t.Fatalf("an import row must carry no supplier GSTIN, got %q", r.SupplierGSTIN)
		}
		if IMSApplies(r.Section, r.ReverseCharge) {
			t.Fatalf("IMS must not apply to an import row")
		}
	})

	t.Run("docRejdata_is_flagged_not_dropped", func(t *testing.T) {
		r, ok := byNo["REJ-1"]
		if !ok {
			t.Fatalf("the docRejdata row was DROPPED. It must be stored and flagged: the " +
				"operator needs to know a supplier reported something against their GSTIN")
		}
		if !r.Rejected {
			t.Fatalf("REJ-1 was ingested with rejected=false. It is VIEW-ONLY (GSTN IMS "+
				"FAQ 24) and would be summed into ITC — an overclaim of %s per head",
				r.Tax.CGST)
		}
	})
}

// TestParse2B_ToleranceAndReporting pins the two halves of the tolerance
// contract: unknown keys do not fail, and everything skipped is REPORTED.
func TestParse2B_ToleranceAndReporting(t *testing.T) {
	t.Run("unknown_keys_inside_a_document_are_ignored", func(t *testing.T) {
		raw := `{"data":{"gstin":"29A","rtnprd":"012026","docdata":{"b2b":[
			{"ctin":"29ABCDE1234F1Z5","inv":[
				{"inum":"INV-1","dt":"05-01-2026","txval":"100.00","cgst":"9.00","sgst":"9.00",
				 "some_future_gstn_field":{"nested":true},"another":[1,2,3]}]}]}}}`
		res, err := Parse2B([]byte(raw), "GSTR2B")
		if err != nil {
			t.Fatalf("an unknown key inside a document made the parse FAIL: %v — a strict "+
				"parser against a schema we do not have rejects real files", err)
		}
		if len(res.Documents) != 1 {
			t.Fatalf("want 1 document, got %d", len(res.Documents))
		}
	})

	t.Run("an_unknown_section_is_reported_not_silently_dropped", func(t *testing.T) {
		raw := `{"data":{"gstin":"29A","rtnprd":"012026","docdata":{
			"b2b":[{"ctin":"29ABCDE1234F1Z5","inv":[{"inum":"I1","dt":"05-01-2026","txval":"100.00"}]}],
			"someNewSection":[{"x":1}]}}}`
		res, err := Parse2B([]byte(raw), "GSTR2B")
		if err != nil {
			t.Fatalf("Parse2B: %v", err)
		}
		if !res.Partial() {
			t.Fatalf("an unrecognised section was skipped SILENTLY. Under model (b) an " +
				"absent document is indistinguishable from a supplier who did not file, " +
				"so a silent skip REVERSES ITC that nobody knows about")
		}
		if !strings.Contains(strings.Join(res.Skipped, ","), "someNewSection") {
			t.Fatalf("the skip does not name the section: %v", res.Skipped)
		}
	})

	t.Run("a_document_with_no_number_or_a_bad_date_is_reported", func(t *testing.T) {
		raw := `{"data":{"gstin":"29A","rtnprd":"012026","docdata":{"b2b":[
			{"ctin":"29ABCDE1234F1Z5","inv":[
				{"dt":"05-01-2026","txval":"100.00"},
				{"inum":"I2","dt":"the fifth of January","txval":"100.00"},
				{"inum":"I3","dt":"05-01-2026","txval":"100.00"}]}]}}}`
		res, err := Parse2B([]byte(raw), "GSTR2B")
		if err != nil {
			t.Fatalf("Parse2B: %v", err)
		}
		if len(res.Documents) != 1 || res.Documents[0].DocNo != "I3" {
			t.Fatalf("want only the well-formed document, got %d", len(res.Documents))
		}
		if len(res.Skipped) != 2 {
			t.Fatalf("want 2 reported skips, got %v", res.Skipped)
		}
	})

	t.Run("iso_dates_are_accepted_too", func(t *testing.T) {
		raw := `{"gstin":"29A","rtnprd":"012026","gendt":"2026-02-14","docdata":{"b2b":[
			{"ctin":"29ABCDE1234F1Z5","inv":[{"inum":"I1","dt":"2026-01-05","txval":"100.00"}]}]}}`
		res, err := Parse2B([]byte(raw), "GSTR2B")
		if err != nil {
			t.Fatalf("Parse2B: %v", err)
		}
		if len(res.Documents) != 1 {
			t.Fatalf("ISO dates were rejected: %v", res.Skipped)
		}
		if got := res.Documents[0].DocDate.In(IST).Format("2006-01-02"); got != "2026-01-05" {
			t.Fatalf("ISO date parsed as %s", got)
		}
	})

	t.Run("the_data_wrapper_is_optional", func(t *testing.T) {
		raw := `{"gstin":"29A","rtnprd":"012026","docdata":{"b2b":[
			{"ctin":"29ABCDE1234F1Z5","inv":[{"inum":"I1","dt":"05-01-2026","txval":"100.00"}]}]}}`
		if res, err := Parse2B([]byte(raw), "GSTR2B"); err != nil || len(res.Documents) != 1 {
			t.Fatalf("an unwrapped file failed: err=%v docs=%d", err, len(res.Documents))
		}
	})
}

// TestParse2B_SameBytesSameDigest pins the idempotency key: re-uploading the
// identical file must produce the identical digest, and ANY change must produce
// a different one. That is what makes a second, different 2B for the same period
// a SUPERSEDING statement rather than a silent overwrite.
func TestParse2B_SameBytesSameDigest(t *testing.T) {
	a, err := Parse2B([]byte(sample2B), "GSTR2B")
	if err != nil {
		t.Fatalf("Parse2B: %v", err)
	}
	b, err := Parse2B([]byte(sample2B), "GSTR2B")
	if err != nil {
		t.Fatalf("Parse2B: %v", err)
	}
	if a.SourceSHA256 != b.SourceSHA256 {
		t.Fatalf("the same bytes produced different digests: %s vs %s", a.SourceSHA256, b.SourceSHA256)
	}
	c, err := Parse2B([]byte(strings.Replace(sample2B, "10000.00", "10000.01", 1)), "GSTR2B")
	if err != nil {
		t.Fatalf("Parse2B: %v", err)
	}
	if c.SourceSHA256 == a.SourceSHA256 {
		t.Fatalf("a one-paisa change did not change the digest — a re-generated 2B would " +
			"be treated as the identical file and would not supersede the previous one")
	}
}

// TestMoneyRefusesAFloat is the guard on the one mistake that cannot be seen
// afterwards.
//
// Every money field goes json.Number -> decimal.NewFromString. If a future edit
// drops UseNumber() from the decoder, encoding/json produces float64 and the
// paisa are gone BEFORE any of this package's code runs. money() then returns an
// error naming the field rather than accepting a value that has already lost
// precision — that is what the default case in its type switch is for, and this
// test is what proves the default case is reachable and correct.
func TestMoneyRefusesAFloat(t *testing.T) {
	// Decode WITHOUT UseNumber, exactly as a careless edit would.
	var m map[string]any
	if err := json.Unmarshal([]byte(`{"txval": 0.1}`), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := m["txval"].(float64); !ok {
		t.Fatalf("test premise broken: plain json.Unmarshal did not produce a float64")
	}
	if _, _, err := money(m, "txval"); err == nil {
		t.Fatalf("money() ACCEPTED a float64. Without UseNumber the paisa are lost before " +
			"this package sees the value, and every figure on the return is quietly " +
			"approximate")
	} else if !strings.Contains(err.Error(), "UseNumber") {
		t.Fatalf("the error does not name the cause: %v", err)
	}

	// And the two accepted forms both survive exactly.
	var n map[string]any
	dec := json.NewDecoder(strings.NewReader(`{"a": 0.1, "b": "0.1"}`))
	dec.UseNumber()
	if err := dec.Decode(&n); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, k := range []string{"a", "b"} {
		v, ok, err := money(n, k)
		if err != nil || !ok {
			t.Fatalf("money(%q): %v ok=%v", k, err, ok)
		}
		if !v.Equal(d("0.1")) {
			t.Fatalf("money(%q) = %s, want exactly 0.1", k, v)
		}
	}
}
