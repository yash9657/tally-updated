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

// TallyMessage is a single import object. A message carries EITHER a VOUCHER or
// a LEDGER master (Tally emits both kinds of TALLYMESSAGE in a full export). The
// importer runs a first pass over LEDGER masters (opening balances + GST master
// attributes) and a second pass over VOUCHERs.
type TallyMessage struct {
	Voucher *TallyVoucher      `xml:"VOUCHER"`
	Ledger  *TallyLedgerMaster `xml:"LEDGER"`
}

// TallyLedgerMaster is a Tally LEDGER master message. The NAME comes from either
// the NAME attribute or a <NAME> child (Tally uses the attribute on the LEDGER
// element); PARENT is the Tally account group. OpeningBalance follows the same
// signed convention as a ledger AMOUNT (debit-natured negative, credit positive)
// — use OpeningSide()/OpeningMagnitude() rather than the raw field.
type TallyLedgerMaster struct {
	NameAttr        string `xml:"NAME,attr"`
	NameElem        string `xml:"NAME"`
	Parent          string `xml:"PARENT"`
	OpeningBalance  string `xml:"OPENINGBALANCE"`
	GSTIN           string `xml:"PARTYGSTIN"`
	GSTINAlt        string `xml:"GSTIN"`
	GSTRegistration string `xml:"GSTREGISTRATIONTYPE"`
	State           string `xml:"STATE"`
	StateName       string `xml:"STATENAME"`
	LedgerStateName string `xml:"LEDSTATENAME"`
}

// Name returns the ledger master's name, preferring the NAME attribute and
// falling back to a <NAME> child.
func (m TallyLedgerMaster) Name() string {
	if n := strings.TrimSpace(m.NameAttr); n != "" {
		return n
	}
	return strings.TrimSpace(m.NameElem)
}

// GSTINValue returns the counter-party GSTIN, accepting either the PARTYGSTIN or
// GSTIN spelling Tally emits.
func (m TallyLedgerMaster) GSTINValue() string {
	if g := strings.TrimSpace(m.GSTIN); g != "" {
		return g
	}
	return strings.TrimSpace(m.GSTINAlt)
}

// OpeningSide reports whether the opening balance is a debit (true) or credit
// (false), and whether an opening balance is present at all (ok=false when the
// field is empty or zero). Tally signs OPENINGBALANCE like an AMOUNT: a
// debit-natured opening carries a NEGATIVE value, a credit-natured opening a
// POSITIVE value (RESEARCH sec 19), so the Dr/Cr is the inverse of the sign.
func (m TallyLedgerMaster) OpeningSide() (debit bool, ok bool, err error) {
	raw := strings.TrimSpace(m.OpeningBalance)
	if raw == "" {
		return false, false, nil
	}
	d, err := decimal.NewFromString(raw)
	if err != nil {
		return false, false, fmt.Errorf("integration: ledger %q: bad OPENINGBALANCE %q: %w", m.Name(), raw, err)
	}
	if d.IsZero() {
		return false, false, nil
	}
	// Negative magnitude => debit-natured opening; positive => credit-natured.
	return d.IsNegative(), true, nil
}

// OpeningMagnitude returns the absolute opening balance (sign dropped). The sign
// only encodes Dr/Cr, which OpeningSide() reports.
func (m TallyLedgerMaster) OpeningMagnitude() (decimal.Decimal, error) {
	raw := strings.TrimSpace(m.OpeningBalance)
	if raw == "" {
		return decimal.Zero, nil
	}
	d, err := decimal.NewFromString(raw)
	if err != nil {
		return decimal.Zero, fmt.Errorf("integration: ledger %q: bad OPENINGBALANCE %q: %w", m.Name(), raw, err)
	}
	return d.Abs(), nil
}

// TallyVoucher is a Tally voucher with its ledger entries. VchType/Action come
// from XML attributes on the VOUCHER element; the body carries the date, number,
// narration and the per-ledger debit/credit lines.
type TallyVoucher struct {
	VchType         string `xml:"VCHTYPE,attr"`
	Action          string `xml:"ACTION,attr"`
	Date            string `xml:"DATE"`
	VoucherTypeName string `xml:"VOUCHERTYPENAME"`
	VoucherNumber   string `xml:"VOUCHERNUMBER"`
	Narration       string `xml:"NARRATION"`
	PartyLedgerName string `xml:"PARTYLEDGERNAME"`
	// GUID is Tally's stable identity for the voucher (RESEARCH sec 19); REMOTEID
	// is the sync fallback. GUIDValue() returns GUID, falling back to REMOTEID.
	GUID     string `xml:"GUID"`
	RemoteID string `xml:"REMOTEID"`
	// PlaceOfSupply / reverse-charge attributes carried on Sales/Purchase exports.
	PlaceOfSupply string             `xml:"PLACEOFSUPPLY"`
	StateName     string             `xml:"STATENAME"`
	ReverseCharge string             `xml:"ISINVOICEORDER"`
	RCMFlag       string             `xml:"ISREVERSECHARGEAPPLICABLE"`
	LedgerEntries []TallyLedgerEntry `xml:"LEDGERENTRIES.LIST"`
	// AllLedgerEntries is Tally's alternate element name for accounting lines on
	// inventory vouchers; both are merged by Entries().
	AllLedgerEntries []TallyLedgerEntry `xml:"ALLLEDGERENTRIES.LIST"`
}

// GUIDValue returns the stable source GUID, preferring <GUID> and falling back
// to <REMOTEID>. Empty when neither is present (the voucher then always posts on
// re-import — it does not participate in (company, guid) idempotency).
func (v TallyVoucher) GUIDValue() string {
	if g := strings.TrimSpace(v.GUID); g != "" {
		return g
	}
	return strings.TrimSpace(v.RemoteID)
}

// ReverseChargeFlag reports whether the voucher is marked reverse-charge. It
// accepts either of the RCM-style flags Tally may emit; absent/"No" is false.
func (v TallyVoucher) ReverseChargeFlag() bool {
	yes := func(s string) bool { return strings.EqualFold(strings.TrimSpace(s), "Yes") }
	return yes(v.RCMFlag)
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
	// HSN and GSTRate are optional GST attributes Tally may stamp on the taxable
	// (sales income) line. When present they are preferred over rate inference.
	HSN     string `xml:"HSNCODE"`
	GSTRate string `xml:"GSTRATE"`
	// BillAllocations are the bill-wise references Tally stamps on a party ledger
	// line (<BILLALLOCATIONS.LIST>) — the source of receivables/payables ageing.
	BillAllocations []TallyBillAllocation `xml:"BILLALLOCATIONS.LIST"`
}

// TallyBillAllocation is one bill-wise reference under a party ledger line. NAME
// is the bill reference (e.g. an invoice number); BILLTYPE is Tally's reference
// kind ("New Ref" opens a bill, "Agst Ref" settles one, "Advance", "On Account");
// AMOUNT carries the same Dr/Cr sign as the parent line; DUEDATE is the credit-
// period due date used for ageing.
type TallyBillAllocation struct {
	Name     string `xml:"NAME"`
	BillType string `xml:"BILLTYPE"`
	Amount   string `xml:"AMOUNT"`
	DueDate  string `xml:"DUEDATE"`
}

// RefType maps Tally's BILLTYPE label to the ledger bill-reference type. An
// unknown/empty label falls back to a new reference (Tally's default).
func (b TallyBillAllocation) RefType() string {
	switch strings.ToUpper(strings.Join(strings.Fields(b.BillType), " ")) {
	case "AGST REF", "AGAINST REF", "AGAINST":
		return "AGAINST"
	case "ADVANCE":
		return "ADVANCE"
	case "ON ACCOUNT":
		return "ON_ACCOUNT"
	default:
		return "NEW"
	}
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
