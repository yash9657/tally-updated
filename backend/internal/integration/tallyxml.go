package integration

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"github.com/shopspring/decimal"
)

// TallyEnvelope is the root of a Tally data export/import document
// (RESEARCH sec 19): ENVELOPE > BODY > DATA > TALLYMESSAGE*. Only the fields
// cored consumes on import are modelled; unknown elements are ignored by the
// encoding/xml decoder.
type TallyEnvelope struct {
	XMLName xml.Name    `xml:"ENVELOPE"`
	Header  TallyHeader `xml:"HEADER"`
	Body    TallyBody   `xml:"BODY"`
}

// TallyHeader is the request header. TallyRequest is "Import"/"Export"; ID names
// the report (e.g. "Vouchers").
type TallyHeader struct {
	Version      string `xml:"VERSION"`
	TallyRequest string `xml:"TALLYREQUEST"`
	Type         string `xml:"TYPE"`
	ID           string `xml:"ID"`
}

// TallyBody wraps the request description and the data payload.
type TallyBody struct {
	Desc TallyDesc `xml:"DESC"`
	Data TallyData `xml:"DATA"`
}

// TallyDesc carries the static variables, notably the source company name.
type TallyDesc struct {
	StaticVariables TallyStaticVariables `xml:"STATICVARIABLES"`
}

// TallyStaticVariables holds export-time context. CurrentCompany is the name of
// the Tally company the export was taken from (informational on import).
type TallyStaticVariables struct {
	CurrentCompany string `xml:"SVCURRENTCOMPANY"`
}

// TallyData holds the list of TALLYMESSAGE envelopes — one per imported object.
type TallyData struct {
	Messages []TallyMessage `xml:"TALLYMESSAGE"`
}

// TallyMessage is a single import object. In Phase 1 only VOUCHER messages are
// consumed; ledger/stockitem master messages are ignored (ledgers are created
// on demand from the voucher's LEDGERENTRIES).
type TallyMessage struct {
	Voucher *TallyVoucher `xml:"VOUCHER"`
}

// TallyVoucher is a Tally voucher with its ledger entries. VchType/Action come
// from XML attributes on the VOUCHER element; the body carries the date, number,
// narration and the per-ledger debit/credit lines.
type TallyVoucher struct {
	VchType         string             `xml:"VCHTYPE,attr"`
	Action          string             `xml:"ACTION,attr"`
	Date            string             `xml:"DATE"`
	VoucherTypeName string             `xml:"VOUCHERTYPENAME"`
	VoucherNumber   string             `xml:"VOUCHERNUMBER"`
	Narration       string             `xml:"NARRATION"`
	PartyLedgerName string             `xml:"PARTYLEDGERNAME"`
	LedgerEntries   []TallyLedgerEntry `xml:"LEDGERENTRIES.LIST"`
	// AllLedgerEntries is Tally's alternate element name for accounting lines on
	// inventory vouchers; both are merged by Entries().
	AllLedgerEntries []TallyLedgerEntry `xml:"ALLLEDGERENTRIES.LIST"`
}

// Entries returns the combined ledger lines, accepting either the
// LEDGERENTRIES.LIST or ALLLEDGERENTRIES.LIST spelling Tally emits.
func (v TallyVoucher) Entries() []TallyLedgerEntry {
	if len(v.LedgerEntries) > 0 {
		return v.LedgerEntries
	}
	return v.AllLedgerEntries
}

// TallyLedgerEntry is one accounting line of a voucher. Tally's sign convention
// (RESEARCH sec 19): ISDEEMEDPOSITIVE="Yes" marks the debit side and
// ISDEEMEDPOSITIVE="No" the credit side; the AMOUNT magnitude is the value and
// its sign is the inverse of the deemed-positive flag (debits carry a negative
// AMOUNT, credits a positive AMOUNT). Use Side() and Magnitude() rather than the
// raw fields so the convention is applied consistently.
type TallyLedgerEntry struct {
	LedgerName       string `xml:"LEDGERNAME"`
	IsDeemedPositive string `xml:"ISDEEMEDPOSITIVE"`
	Amount           string `xml:"AMOUNT"`
}

// Side reports whether this entry is a debit (true) or credit (false) per the
// ISDEEMEDPOSITIVE flag.
func (e TallyLedgerEntry) Side() (debit bool, err error) {
	switch strings.ToUpper(strings.TrimSpace(e.IsDeemedPositive)) {
	case "YES":
		return true, nil
	case "NO":
		return false, nil
	default:
		return false, fmt.Errorf("integration: ledger %q: invalid ISDEEMEDPOSITIVE %q",
			e.LedgerName, e.IsDeemedPositive)
	}
}

// Magnitude returns the absolute posting amount for this entry (the AMOUNT field
// with its sign dropped). The sign only encodes Dr/Cr, which Side() already
// reports; the ledger posting API takes a non-negative amount plus a Dr/Cr flag.
func (e TallyLedgerEntry) Magnitude() (decimal.Decimal, error) {
	raw := strings.TrimSpace(e.Amount)
	if raw == "" {
		return decimal.Zero, fmt.Errorf("integration: ledger %q: empty AMOUNT", e.LedgerName)
	}
	d, err := decimal.NewFromString(raw)
	if err != nil {
		return decimal.Zero, fmt.Errorf("integration: ledger %q: bad AMOUNT %q: %w", e.LedgerName, raw, err)
	}
	return d.Abs(), nil
}

// ParseTallyEnvelope decodes a Tally XML export stream into a TallyEnvelope. It
// is pure (no I/O beyond the reader) and does not validate accounting
// semantics — that is the importer's job.
func ParseTallyEnvelope(r io.Reader) (*TallyEnvelope, error) {
	var env TallyEnvelope
	dec := xml.NewDecoder(r)
	// Tally documents declare namespaces (xmlns:UDF); the strict default rejects
	// unbound prefixes on some exports, so be lenient about charset/namespaces.
	dec.Strict = false
	if err := dec.Decode(&env); err != nil {
		return nil, fmt.Errorf("integration: decode tally envelope: %w", err)
	}
	return &env, nil
}

// TallyDateToISO converts Tally's YYYYMMDD date string into the ISO YYYY-MM-DD
// form the ledger posting API expects. An already-hyphenated value is returned
// unchanged.
func TallyDateToISO(s string) (string, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "-") {
		return s, nil
	}
	if len(s) != 8 {
		return "", fmt.Errorf("integration: invalid tally date %q (want YYYYMMDD)", s)
	}
	return s[0:4] + "-" + s[4:6] + "-" + s[6:8], nil
}
