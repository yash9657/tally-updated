package integration

import (
	"bufio"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
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
//
// ⛔ THERE ARE TWO SHAPES AND ONLY ONE OF THEM WAS MODELLED. The hand-authored
// import envelopes every integrator publishes put DESC and DATA directly under
// BODY, and that is what this parser was built against. A file that TallyPrime
// itself writes — Gateway > Export > Masters / Day Book, XML format — nests them
// one level deeper, inside IMPORTDATA, and renames them:
//
//	<BODY><IMPORTDATA><REQUESTDESC>…</REQUESTDESC><REQUESTDATA>…</REQUESTDATA>
//
// (An export is written AS an import request so it can be fed straight back in;
// some report exports spell the wrapper EXPORTDATA instead.) With only the flat
// shape modelled, a genuine 84 MB export parsed WITHOUT ERROR and yielded ZERO
// messages — a "successful" import of nothing at all, which is the worst
// possible failure for a migration tool. All three shapes are now accepted and
// Messages()/SourceCompany() are the only supported way to read them.
type TallyBody struct {
	Desc       TallyDesc       `xml:"DESC"`
	Data       TallyData       `xml:"DATA"`
	ImportData TallyRequestBox `xml:"IMPORTDATA"`
	ExportData TallyRequestBox `xml:"EXPORTDATA"`
}

// TallyRequestBox is the IMPORTDATA/EXPORTDATA wrapper TallyPrime writes around
// the description and payload of a real export.
type TallyRequestBox struct {
	Desc TallyDesc `xml:"REQUESTDESC"`
	Data TallyData `xml:"REQUESTDATA"`
}

// Messages returns the TALLYMESSAGE list from whichever of the three envelope
// shapes this document uses. It is the ONLY supported accessor: reading
// Body.Data.Messages directly is what silently imported nothing.
func (e *TallyEnvelope) Messages() []TallyMessage {
	switch {
	case len(e.Body.Data.Messages) > 0:
		return e.Body.Data.Messages
	case len(e.Body.ImportData.Data.Messages) > 0:
		return e.Body.ImportData.Data.Messages
	default:
		return e.Body.ExportData.Data.Messages
	}
}

// SourceCompany returns the SVCURRENTCOMPANY name from whichever envelope shape
// this document uses.
func (e *TallyEnvelope) SourceCompany() string {
	for _, d := range []TallyDesc{e.Body.Desc, e.Body.ImportData.Desc, e.Body.ExportData.Desc} {
		if n := strings.TrimSpace(d.StaticVariables.CurrentCompany); n != "" {
			return n
		}
	}
	return ""
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
	// Group is a GROUP master. A real export carries the company's whole
	// chart-of-accounts tree, INCLUDING groups the user added themselves, and a
	// ledger whose PARENT names one of those cannot be created until the group
	// exists — see the group pass in migration.ImportEnvelope.
	Group *TallyGroupMaster `xml:"GROUP"`
	// The THREE INVENTORY MASTERS a real Masters.xml carries. They were parsed by
	// NOTHING until the importer was taught quantities: an export's stock lines
	// name an item, a unit, a godown and a batch, and an importer that reads none
	// of those brings in the ACCOUNTING VALUE and throws the goods away — a
	// trader whose closing stock sits on the Balance Sheet with an empty Stock
	// Summary, and a Trial Balance that BALANCES the whole time.
	//
	// ⛔ ORDER MATTERS AT IMPORT AND IT IS NOT THE ORDER OF THIS STRUCT. A
	// stock item names a unit and its opening position names a godown, so units
	// and godowns must land first; see migration.importInventoryMasters.
	StockItem *TallyStockItemMaster `xml:"STOCKITEM"`
	Unit      *TallyUnitMaster      `xml:"UNIT"`
	Godown    *TallyGodownMaster    `xml:"GODOWN"`
	// StockGroup is a STOCKGROUP master: one node of the item hierarchy (T10).
	// PARENT names another stock group, or is empty / "Primary" for a top-level
	// one — and Tally spells the root both ways depending on the export.
	StockGroup *TallyStockGroupMaster `xml:"STOCKGROUP"`
}

// TallyStockGroupMaster is a STOCKGROUP master: a node of the item hierarchy.
type TallyStockGroupMaster struct {
	NameAttr  string `xml:"NAME,attr"`
	NameElem  string `xml:"NAME"`
	Parent    string `xml:"PARENT"`
	IsDeleted string `xml:"ISDELETED"`
}

// Name returns the group's name, preferring the NAME attribute.
func (g TallyStockGroupMaster) Name() string {
	if n := strings.TrimSpace(g.NameAttr); n != "" {
		return n
	}
	return strings.TrimSpace(g.NameElem)
}

// ParentName returns the parent group's name, or "" for a top-level group.
//
// ⛔ "Primary" IS TRANSLATED TO "no parent", and it has to be. Tally's item tree
// has a synthetic root spelled "Primary" that is not a group anybody created —
// the same trap the ACCOUNT side documents (masters resolves a parent by name and
// answers "parent group "Primary" not found" for what the operator read as the
// ordinary top-level choice).
func (g TallyStockGroupMaster) ParentName() string {
	p := strings.TrimSpace(g.Parent)
	if strings.EqualFold(p, "Primary") {
		return ""
	}
	return p
}

// Deleted reports whether the master is tombstoned in the export.
func (g TallyStockGroupMaster) Deleted() bool {
	return strings.EqualFold(strings.TrimSpace(g.IsDeleted), "Yes")
}

// TallyUnitMaster is a UNIT master: a unit of measure.
//
// ⚠️ A COMPOUND UNIT ("Box of 12 Nos") IS A UNIT MASTER WITH A BASE AND A
// CONVERSION, and Tally emits it as ISSIMPLEUNIT=No with BASEUNITS, ADDITIONALUNITS
// and CONVERSION. The fields are parsed here so that the importer can state what
// it could not represent instead of silently importing "Box" as a simple symbol
// and reading every quantity in it as if it were pieces.
type TallyUnitMaster struct {
	NameAttr string `xml:"NAME,attr"`
	NameElem string `xml:"NAME"`
	// Symbol is Tally's own <SYMBOL>; the NAME is usually the same string.
	Symbol string `xml:"SYMBOL"`
	// FormalName is the long name ("Numbers" for "Nos").
	FormalName string `xml:"FORMALNAME"`
	// DecimalPlaces is how many decimals a quantity in this unit may carry.
	DecimalPlaces string `xml:"DECIMALPLACES"`
	// IsSimpleUnit is "No" for a COMPOUND unit.
	IsSimpleUnit string `xml:"ISSIMPLEUNIT"`
	// BaseUnits / AdditionalUnits / Conversion describe a compound unit:
	// 1 <BaseUnits> = <Conversion> <AdditionalUnits>.
	BaseUnits       string `xml:"BASEUNITS"`
	AdditionalUnits string `xml:"ADDITIONALUNITS"`
	Conversion      string `xml:"CONVERSION"`
	IsDeleted       string `xml:"ISDELETED"`
}

// Name returns the unit's symbol, preferring <SYMBOL>, then the NAME attribute,
// then <NAME>. The symbol is what a stock item references and what
// unit.symbol holds.
func (u TallyUnitMaster) Name() string {
	for _, c := range []string{u.Symbol, u.NameAttr, u.NameElem} {
		if c = strings.TrimSpace(c); c != "" {
			return c
		}
	}
	return ""
}

// Compound reports whether this is a compound unit (Tally: ISSIMPLEUNIT=No).
func (u TallyUnitMaster) Compound() bool {
	return strings.EqualFold(strings.TrimSpace(u.IsSimpleUnit), "No")
}

// Deleted reports whether the master is tombstoned in the export.
func (u TallyUnitMaster) Deleted() bool {
	return strings.EqualFold(strings.TrimSpace(u.IsDeleted), "Yes")
}

// TallyGodownMaster is a GODOWN (stock location) master. PARENT names a parent
// location in Tally's location tree; cored's godowns are flat, which is
// something the importer REPORTS rather than silently flattens.
type TallyGodownMaster struct {
	NameAttr  string `xml:"NAME,attr"`
	NameElem  string `xml:"NAME"`
	Parent    string `xml:"PARENT"`
	IsDeleted string `xml:"ISDELETED"`
}

// Name returns the location's name, preferring the NAME attribute.
func (g TallyGodownMaster) Name() string {
	if n := strings.TrimSpace(g.NameAttr); n != "" {
		return n
	}
	return strings.TrimSpace(g.NameElem)
}

// Deleted reports whether the master is tombstoned in the export.
func (g TallyGodownMaster) Deleted() bool {
	return strings.EqualFold(strings.TrimSpace(g.IsDeleted), "Yes")
}

// TallyStockItemMaster is a STOCKITEM master: the inventory item, its unit, its
// HSN/GST attributes and its OPENING POSITION.
//
// ⛔ THE OPENING IS THE PART THAT CANNOT BE SKIPPED. A migrated trader whose
// items arrive with no opening stock will drive every position negative on the
// first sale of goods bought before the export window — and if negative stock is
// allowed, the sale consumes NOTHING, so the cost of goods sold is understated
// by the whole opening value and the Balance Sheet's closing stock with it. The
// opening is stated per position (godown, batch) under BATCHALLOCATIONS.LIST when
// the item is tracked, and as a bare OPENINGBALANCE/OPENINGVALUE pair when it is
// not.
type TallyStockItemMaster struct {
	NameAttr string `xml:"NAME,attr"`
	NameElem string `xml:"NAME"`
	Parent   string `xml:"PARENT"`
	// BaseUnits is the item's unit of measure (Tally: <BASEUNITS>).
	BaseUnits string `xml:"BASEUNITS"`
	// UnitAlt is the older/alternative spelling some exports use.
	UnitAlt string `xml:"UNIT"`
	HSN     string `xml:"HSNCODE"`
	HSNAlt  string `xml:"GSTHSNCODE"`
	GSTRate string `xml:"GSTRATE"`
	// OpeningBalance is a QUANTITY WITH ITS UNIT ("120 BAG"); OpeningValue is
	// money; OpeningRate is "82.50/BAG".
	OpeningBalance string `xml:"OPENINGBALANCE"`
	OpeningValue   string `xml:"OPENINGVALUE"`
	OpeningRate    string `xml:"OPENINGRATE"`
	// Openings are the per-(godown, batch) opening positions.
	Openings  []TallyBatchAllocation `xml:"BATCHALLOCATIONS.LIST"`
	IsDeleted string                 `xml:"ISDELETED"`
}

// Name returns the item's name, preferring the NAME attribute.
func (i TallyStockItemMaster) Name() string {
	if n := strings.TrimSpace(i.NameAttr); n != "" {
		return n
	}
	return strings.TrimSpace(i.NameElem)
}

// StockGroupName returns the item's STOCK GROUP — Tally puts it in <PARENT> —
// with the synthetic root "Primary" translated to "" (ungrouped), for the reason
// TallyStockGroupMaster.ParentName gives.
func (i TallyStockItemMaster) StockGroupName() string {
	p := strings.TrimSpace(i.Parent)
	if strings.EqualFold(p, "Primary") {
		return ""
	}
	return p
}

// UnitSymbol returns the item's unit of measure, preferring <BASEUNITS>.
func (i TallyStockItemMaster) UnitSymbol() string {
	if u := strings.TrimSpace(i.BaseUnits); u != "" {
		return u
	}
	return strings.TrimSpace(i.UnitAlt)
}

// HSNCode returns the item's HSN/SAC, preferring <HSNCODE>.
func (i TallyStockItemMaster) HSNCode() string {
	if h := strings.TrimSpace(i.HSN); h != "" {
		return h
	}
	return strings.TrimSpace(i.HSNAlt)
}

// Deleted reports whether the master is tombstoned in the export.
func (i TallyStockItemMaster) Deleted() bool {
	return strings.EqualFold(strings.TrimSpace(i.IsDeleted), "Yes")
}

// TallyGroupMaster is a Tally GROUP master: one node of the chart-of-accounts
// tree. PARENT is empty for one of Tally's 15 primary groups and names the
// parent group otherwise.
type TallyGroupMaster struct {
	NameAttr     string `xml:"NAME,attr"`
	ReservedName string `xml:"RESERVEDNAME,attr"`
	NameElem     string `xml:"NAME"`
	Parent       string `xml:"PARENT"`
	IsDeleted    string `xml:"ISDELETED"`
}

// Name returns the group's name, preferring the NAME attribute.
func (g TallyGroupMaster) Name() string {
	if n := strings.TrimSpace(g.NameAttr); n != "" {
		return n
	}
	return strings.TrimSpace(g.NameElem)
}

// IsReserved reports whether this is one of Tally's own built-in groups (the
// export stamps RESERVEDNAME on them). A reserved group is already seeded in
// every cored company, so the importer resolves rather than creates it.
func (g TallyGroupMaster) IsReserved() bool {
	return strings.TrimSpace(g.ReservedName) != ""
}

// Deleted reports whether the master is tombstoned in the export.
func (g TallyGroupMaster) Deleted() bool {
	return strings.EqualFold(strings.TrimSpace(g.IsDeleted), "Yes")
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
	PriorStateName  string `xml:"PRIORSTATENAME"`
	OldStateName    string `xml:"OLDLEDSTATENAME"`
	// GSTRegs is the DATED registration history TallyPrime keeps under
	// LEDGSTREGDETAILS.LIST. It — not the flat PARTYGSTIN above — is the
	// authority: a party that re-registered carries several entries, and in the
	// real export that was tested against, the flat field still held the 2020
	// GSTIN while the list carried the one applicable from 2026-07-28.
	GSTRegs []TallyLedgerGSTReg `xml:"LEDGSTREGDETAILS.LIST"`
	// OpeningBills are the bill-wise references that make up an opening
	// receivable/payable balance. Without them a migrated debtor has a correct
	// total and an EMPTY ageing — every opening rupee falls outside the buckets.
	OpeningBills []TallyOpeningBill `xml:"BILLALLOCATIONS.LIST"`
	// IsDeleted tombstones the master (D101). Five other master kinds already
	// honour this flag; the ledger master — one of the two oldest readers — was
	// the one it never reached.
	IsDeleted string `xml:"ISDELETED"`
}

// Deleted reports whether the master is tombstoned in the export.
func (l TallyLedgerMaster) Deleted() bool {
	return strings.EqualFold(strings.TrimSpace(l.IsDeleted), "Yes")
}

// TallyLedgerGSTReg is one dated GST registration of a party ledger.
// ApplicableFrom is YYYYMMDD; State is a state NAME, not a code.
type TallyLedgerGSTReg struct {
	ApplicableFrom   string `xml:"APPLICABLEFROM"`
	RegistrationType string `xml:"GSTREGISTRATIONTYPE"`
	State            string `xml:"STATE"`
	PlaceOfSupply    string `xml:"PLACEOFSUPPLY"`
	GSTIN            string `xml:"GSTIN"`
}

// TallyOpeningBill is one bill-wise reference making up a ledger's OPENING
// balance (as opposed to a bill raised by an imported voucher). OpeningBalance
// carries the same signed convention as the ledger's own: negative = debit.
type TallyOpeningBill struct {
	Name           string `xml:"NAME"`
	BillDate       string `xml:"BILLDATE"`
	CreditPeriod   string `xml:"BILLCREDITPERIOD"`
	IsAdvance      string `xml:"ISADVANCE"`
	OpeningBalance string `xml:"OPENINGBALANCE"`
}

// currentGSTReg returns the registration with the greatest APPLICABLEFROM that
// actually carries a GSTIN, and whether one was found.
func (m TallyLedgerMaster) currentGSTReg() (TallyLedgerGSTReg, bool) {
	var best TallyLedgerGSTReg
	var found bool
	for _, r := range m.GSTRegs {
		if strings.TrimSpace(r.GSTIN) == "" {
			continue
		}
		if !found || strings.TrimSpace(r.ApplicableFrom) > strings.TrimSpace(best.ApplicableFrom) {
			best, found = r, true
		}
	}
	return best, found
}

// GSTStateName returns the state NAME declared for this ledger, preferring the
// current dated registration and falling back to the flat spellings. It is a
// name ("Maharashtra"), never a code — see migration.stateCodeFor, which is
// where a name becomes a two-digit GST code.
func (m TallyLedgerMaster) GSTStateName() string {
	if r, ok := m.currentGSTReg(); ok {
		if s := strings.TrimSpace(r.State); s != "" {
			return s
		}
	}
	for _, r := range m.GSTRegs {
		if s := strings.TrimSpace(r.State); s != "" {
			return s
		}
	}
	for _, s := range []string{m.LedgerStateName, m.StateName, m.State, m.PriorStateName, m.OldStateName} {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return ""
}

// RegistrationTypeValue returns the GST registration type, preferring the
// current dated registration over the flat field.
func (m TallyLedgerMaster) RegistrationTypeValue() string {
	if r, ok := m.currentGSTReg(); ok {
		if t := strings.TrimSpace(r.RegistrationType); t != "" {
			return t
		}
	}
	return strings.TrimSpace(m.GSTRegistration)
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
// It prefers the CURRENT dated registration under LEDGSTREGDETAILS.LIST, because
// the flat PARTYGSTIN can be stale: in the real export tested against, three
// parties carried a superseded GSTIN there and sixteen carried none at all while
// the dated list held one. A stale GSTIN on a B2B invoice is a wrong GSTR-1 row.
func (m TallyLedgerMaster) GSTINValue() string {
	if r, ok := m.currentGSTReg(); ok {
		return strings.TrimSpace(r.GSTIN)
	}
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
	// InventoryEntries are the STOCK lines of an item invoice — and they are not
	// optional colour. ⛔ ON AN ITEM INVOICE THE REVENUE LINES DO NOT APPEAR AT
	// THE TOP LEVEL AT ALL: only the party, the tax/charge ledgers and the round
	// off do. Each sales/purchase ledger amount hangs off its stock line inside
	// ACCOUNTINGALLOCATIONS.LIST. Reading only the top level therefore yields a
	// voucher missing its entire income side: of the 121 vouchers in the real
	// export tested against, 63 were unbalanced by exactly the goods value and
	// would have been REFUSED by the double-entry trigger.
	InventoryEntries    []TallyInventoryEntry `xml:"ALLINVENTORYENTRIES.LIST"`
	InventoryEntriesAlt []TallyInventoryEntry `xml:"INVENTORYENTRIES.LIST"`
	// ⛔ THE THREE FLAGS THAT DECIDE WHETHER THIS VOUCHER IS MONEY AT ALL (D101).
	// A real TallyPrime <VOUCHER> has 207 distinct direct children and this
	// struct binds a handful; almost all of the rest are internals nobody should
	// read. These three are not. Tally keeps a CANCELLED voucher out of the books
	// while RETAINING its number, and an OPTIONAL voucher is provisional — only
	// Cash Flow's "Include Scenarios" pulls the latter in (RESEARCH.md:2225).
	// Importing either as a live entry puts money in the books that the source
	// company excludes, and nothing downstream can tell it apart afterwards.
	IsCancelled string `xml:"ISCANCELLED"`
	IsOptional  string `xml:"ISOPTIONAL"`
	IsDeleted   string `xml:"ISDELETED"`
}

// Cancelled reports whether Tally CANCELLED this voucher. A cancelled voucher
// keeps its number in Tally (which is why a filtered export leaves no gap in a
// number series) and carries no money.
func (v TallyVoucher) Cancelled() bool {
	return strings.EqualFold(strings.TrimSpace(v.IsCancelled), "Yes")
}

// Optional reports whether this is an OPTIONAL voucher — Tally's provisional
// entry, excluded from the books everywhere except Cash Flow's "Include
// Scenarios".
func (v TallyVoucher) Optional() bool {
	return strings.EqualFold(strings.TrimSpace(v.IsOptional), "Yes")
}

// Deleted reports whether the voucher is tombstoned in the export.
func (v TallyVoucher) Deleted() bool {
	return strings.EqualFold(strings.TrimSpace(v.IsDeleted), "Yes")
}

// VoidReason names why this voucher is not money, for a report an operator
// reads. It returns "" when the voucher is live. ⛔ THE ORDER IS DELIBERATE:
// a voucher can carry more than one flag, and naming the strongest reason is
// better than naming the first one found.
func (v TallyVoucher) VoidReason() string {
	switch {
	case v.Deleted():
		return "deleted"
	case v.Cancelled():
		return "cancelled"
	case v.Optional():
		return "optional"
	default:
		return ""
	}
}

// TallyInventoryEntry is one stock line of an item invoice. Its
// AccountingAllocations carry the ledger postings the line generates — the sales
// or purchase ledger, split when one item is booked across several.
type TallyInventoryEntry struct {
	StockItemName string `xml:"STOCKITEMNAME"`
	Rate          string `xml:"RATE"`
	Amount        string `xml:"AMOUNT"`
	ActualQty     string `xml:"ACTUALQTY"`
	BilledQty     string `xml:"BILLEDQTY"`
	// IsDeemedPositive is Tally's own direction flag on the stock line. It is
	// read only as a LAST RESORT (see Movements): the voucher CLASS decides
	// whether goods came in or went out, and a signed quantity overrides both.
	IsDeemedPositive string `xml:"ISDEEMEDPOSITIVE"`
	// GodownName is the location on the LINE, for an export that states one
	// without a batch allocation.
	GodownName string `xml:"GODOWNNAME"`
	// BatchAllocations are the per-(godown, batch) splits of this line. ⛔ A REAL
	// EXPORT PUTS THE LOCATION HERE, NOT ON THE LINE: the file the importer was
	// fixed against carried 227 stock lines with batches and godowns, all of them
	// inside these. A parser that read only the line saw stock with no location
	// at all — which posts as untracked, so the quantity is right and the
	// warehouse is wrong, and no total anywhere disagrees.
	BatchAllocations      []TallyBatchAllocation `xml:"BATCHALLOCATIONS.LIST"`
	AccountingAllocations []TallyLedgerEntry     `xml:"ACCOUNTINGALLOCATIONS.LIST"`
}

// TallyBatchAllocation is one (godown, batch) split of a stock line — and, on a
// STOCKITEM master, one opening position.
//
// GodownName/BatchName may both be empty, which is a POSITION like any other
// (untracked): a company that never enabled multi-location keeps all its stock
// there, and inventing a location for it would move stock into a warehouse the
// operator has never heard of.
type TallyBatchAllocation struct {
	GodownName string `xml:"GODOWNNAME"`
	BatchName  string `xml:"BATCHNAME"`
	// ActualQty / BilledQty carry the quantity WITH ITS UNIT ("100.00 BAG").
	ActualQty string `xml:"ACTUALQTY"`
	BilledQty string `xml:"BILLEDQTY"`
	Amount    string `xml:"AMOUNT"`
	Rate      string `xml:"RATE"`
	// OpeningBalance / OpeningValue / OpeningRate are used when this allocation
	// is an OPENING position on a STOCKITEM master rather than a movement.
	OpeningBalance string `xml:"OPENINGBALANCE"`
	OpeningValue   string `xml:"OPENINGVALUE"`
	OpeningRate    string `xml:"OPENINGRATE"`
	// ExpiryPeriod / MfdOn are the batch's dates when the export carries them.
	ExpiryPeriod string `xml:"EXPIRYPERIOD"`
	MfdOn        string `xml:"MFDON"`
}

// SplitQtyUnit splits one of Tally's quantity-or-rate strings into its number
// and its unit symbol: "100.00 BAG" -> (100, "BAG"), "-2 Nos" -> (-2, "Nos"),
// "82.50/BAG" -> (82.5, "BAG"), "1,200" -> (1200, ""), "" -> (0, "").
//
// ⛔ THE UNIT IS IN THE VALUE, WHICH IS THE WHOLE REASON THIS EXISTS. Tally does
// not send a quantity and a unit in two fields — it sends "100.00 BAG" in one,
// and a rate as "82.50/BAG". decimal.NewFromString refuses both, so an importer
// that reads them straight either errors on every stock line or (worse, if it
// swallows the error) reads a quantity of zero and imports a voucher that moves
// no goods. The UNIT recovered here is also the ONLY place a unit master can be
// inferred from a file that carries no UNIT masters at all — the real export
// tested against carried none, while its 227 stock lines all named one.
//
// ⚠️ A COMPOUND QUANTITY ("5 Box of 12 Nos") KEEPS ONLY ITS OUTER UNIT here, and
// the caller is expected to say so rather than to pretend the inner one does not
// exist. Nothing in this build is unit-qualified, so "5" is stored and "Box" is
// the item's unit; the 12 is a conversion this schema cannot hold.
func SplitQtyUnit(s string) (decimal.Decimal, string, error) {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return decimal.Zero, "", nil
	}
	// A rate is "<number>/<unit>"; a quantity is "<number> <unit>". Normalise the
	// separator so one scan handles both.
	raw = strings.ReplaceAll(raw, "/", " ")
	// The leading run of number characters. Commas are thousands separators in
	// Tally's own output and are not part of the number.
	i := 0
	for i < len(raw) {
		c := raw[i]
		if (c >= '0' && c <= '9') || c == '.' || c == '-' || c == '+' || c == ',' {
			i++
			continue
		}
		break
	}
	number := strings.ReplaceAll(strings.TrimSpace(raw[:i]), ",", "")
	unit := strings.TrimSpace(raw[i:])
	// "5 Box of 12 Nos" -> "Box". See the compound-unit note above.
	if cut := strings.Index(strings.ToLower(unit), " of "); cut >= 0 {
		unit = strings.TrimSpace(unit[:cut])
	}
	if number == "" || number == "-" || number == "+" {
		return decimal.Zero, unit, fmt.Errorf("integration: %q carries no number", s)
	}
	d, err := decimal.NewFromString(number)
	if err != nil {
		return decimal.Zero, unit, fmt.Errorf("integration: bad quantity/rate %q: %w", s, err)
	}
	return d, unit, nil
}

// InventoryLines returns EVERY stock line of the voucher under either spelling
// Tally emits, in document order.
//
// It exists for the same reason Entries() does: the two spellings can COEXIST in
// one file (they do, across the real export), so a reader that takes one list or
// the other silently loses the goods on half the vouchers — and quantity is
// invisible in a Trial Balance, so nothing would disagree.
func (v TallyVoucher) InventoryLines() []TallyInventoryEntry {
	out := make([]TallyInventoryEntry, 0, len(v.InventoryEntries)+len(v.InventoryEntriesAlt))
	out = append(out, v.InventoryEntries...)
	out = append(out, v.InventoryEntriesAlt...)
	return out
}

// StockPositions returns one entry per (godown, batch) position the line moves,
// with the quantity, the unit and the rate resolved.
//
// ⛔ A LINE WITH NO BATCH ALLOCATION IS STILL ONE POSITION, and returning none
// for it would drop the goods. A line WITH allocations is split across them and
// its own ACTUALQTY is IGNORED — the allocations are the authority, and adding
// the line's total to them would double every quantity in the warehouse.
func (e TallyInventoryEntry) StockPositions() []TallyStockPosition {
	if len(e.BatchAllocations) == 0 {
		return []TallyStockPosition{{
			GodownName: strings.TrimSpace(e.GodownName),
			Qty:        firstNonEmpty(e.ActualQty, e.BilledQty),
			BilledQty:  firstNonEmpty(e.BilledQty, e.ActualQty),
			Rate:       strings.TrimSpace(e.Rate),
			Amount:     strings.TrimSpace(e.Amount),
		}}
	}
	out := make([]TallyStockPosition, 0, len(e.BatchAllocations))
	for _, b := range e.BatchAllocations {
		godown := strings.TrimSpace(b.GodownName)
		if godown == "" {
			godown = strings.TrimSpace(e.GodownName)
		}
		rate := strings.TrimSpace(b.Rate)
		if rate == "" {
			rate = strings.TrimSpace(e.Rate)
		}
		out = append(out, TallyStockPosition{
			GodownName: godown,
			BatchName:  strings.TrimSpace(b.BatchName),
			Qty:        firstNonEmpty(b.ActualQty, b.BilledQty),
			BilledQty:  firstNonEmpty(b.BilledQty, b.ActualQty),
			Rate:       rate,
			Amount:     strings.TrimSpace(b.Amount),
			ExpiryDate: strings.TrimSpace(b.ExpiryPeriod),
			MfgDate:    strings.TrimSpace(b.MfdOn),
		})
	}
	return out
}

// TallyStockPosition is one (godown, batch) slice of a stock line, with every
// figure still as Tally wrote it — the quantity carries its unit and the rate
// carries "/UNIT". Use SplitQtyUnit on Qty and Rate.
type TallyStockPosition struct {
	GodownName string
	BatchName  string
	Qty        string
	BilledQty  string
	Rate       string
	Amount     string
	ExpiryDate string
	MfgDate    string
}

// OpeningPositions returns the item master's opening positions: one per
// (godown, batch) allocation, or a single untracked one from the flat
// OPENINGBALANCE when the item carries no allocations.
//
// An item with no opening at all returns nothing — which is different from
// returning a zero position: a zero row would claim a position exists at a
// location for every item in the file.
func (i TallyStockItemMaster) OpeningPositions() []TallyStockPosition {
	if len(i.Openings) > 0 {
		out := make([]TallyStockPosition, 0, len(i.Openings))
		for _, b := range i.Openings {
			qty := firstNonEmpty(b.OpeningBalance, b.ActualQty, b.BilledQty)
			if strings.TrimSpace(qty) == "" {
				continue
			}
			out = append(out, TallyStockPosition{
				GodownName: strings.TrimSpace(b.GodownName),
				BatchName:  strings.TrimSpace(b.BatchName),
				Qty:        qty,
				Rate:       firstNonEmpty(b.OpeningRate, b.Rate),
				Amount:     firstNonEmpty(b.OpeningValue, b.Amount),
				ExpiryDate: strings.TrimSpace(b.ExpiryPeriod),
				MfgDate:    strings.TrimSpace(b.MfdOn),
			})
		}
		return out
	}
	if strings.TrimSpace(i.OpeningBalance) == "" {
		return nil
	}
	return []TallyStockPosition{{
		Qty:    strings.TrimSpace(i.OpeningBalance),
		Rate:   strings.TrimSpace(i.OpeningRate),
		Amount: strings.TrimSpace(i.OpeningValue),
	}}
}

// firstNonEmpty returns the first argument that is not blank after trimming.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return ""
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

// Entries returns EVERY accounting line of the voucher: the top-level lines
// under either spelling Tally emits (LEDGERENTRIES.LIST / ALLLEDGERENTRIES.LIST)
// PLUS the per-item allocations nested inside the stock lines of an item
// invoice. Order is document order — top-level lines first, then item lines.
//
// ⛔ IT USED TO RETURN ONE LIST *OR* THE OTHER and never looked inside the
// inventory entries. Both halves of that were wrong against a real export: the
// two top-level spellings can coexist (they do, across the file tested against),
// and an item invoice keeps its revenue lines only in the nested allocations.
// Together they are what makes a voucher balance; see the comment on
// InventoryEntries for the count.
func (v TallyVoucher) Entries() []TallyLedgerEntry {
	out := make([]TallyLedgerEntry, 0,
		len(v.LedgerEntries)+len(v.AllLedgerEntries)+len(v.InventoryEntries))
	out = append(out, v.LedgerEntries...)
	out = append(out, v.AllLedgerEntries...)
	for _, inv := range v.InventoryEntries {
		out = append(out, inv.AccountingAllocations...)
	}
	for _, inv := range v.InventoryEntriesAlt {
		out = append(out, inv.AccountingAllocations...)
	}
	return out
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
	// THE SIGN OF AMOUNT IS THE AUTHORITY, not the flag. Both normally agree, and
	// where they disagree it is the flag that is wrong: ISDEEMEDPOSITIVE records
	// the side the voucher TYPE deems the line to be on, so a line that ends up
	// contra to its own voucher keeps the type's flag and flips only its sign. A
	// round-off that reduces an invoice is the everyday case —
	//
	//	<LEDGERNAME>ROUND OFF SALES</LEDGERNAME>
	//	<ISDEEMEDPOSITIVE>No</ISDEEMEDPOSITIVE>   (says credit)
	//	<AMOUNT>-0.12</AMOUNT>                     (is a debit)
	//
	// — and reading the flag puts 12 paise on the wrong side, so the voucher
	// misses by 24 and the double-entry trigger refuses it. In the real export
	// tested against, 25 lines disagreed; trusting the sign balanced 121 of 121
	// vouchers, trusting the flag balanced 96.
	//
	// The flag is still the fallback, and it is the ONLY answer for a zero or
	// absent amount, where a sign cannot exist.
	if raw := strings.TrimSpace(e.Amount); raw != "" {
		if d, err := decimal.NewFromString(raw); err == nil && !d.IsZero() {
			return d.IsNegative(), nil
		}
	}
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
//
// The stream is put through NewTallyDecodeReader first, which is what makes a
// file TallyPrime actually wrote readable at all; see that function for the two
// things about such a file that stop encoding/xml dead.
func ParseTallyEnvelope(r io.Reader) (*TallyEnvelope, error) {
	var env TallyEnvelope
	dec := xml.NewDecoder(NewTallyDecodeReader(r))
	// Tally documents declare namespaces (xmlns:UDF); the strict default rejects
	// unbound prefixes on some exports, so be lenient about charset/namespaces.
	dec.Strict = false
	// Anything reaching the decoder is already UTF-8 (NewTallyDecodeReader
	// transcoded it), so a declared encoding is passed straight through rather
	// than refused as unsupported.
	dec.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) { return input, nil }
	if err := dec.Decode(&env); err != nil {
		return nil, fmt.Errorf("integration: decode tally envelope: %w", err)
	}
	return &env, nil
}

// NewTallyDecodeReader wraps a raw Tally export so encoding/xml can read it: it
// transcodes UTF-16 to UTF-8, drops the BOM, and removes the characters XML 1.0
// does not permit.
//
// ⛔ BOTH FIXES ARE LOAD-BEARING, AND EITHER ONE ALONE STOPS THE PARSE DEAD.
//
//  1. TALLYPRIME WRITES UTF-16LE. Not UTF-8, and with no <?xml?> declaration to
//     say so — just a byte-order mark and then 16-bit code units. Handed to
//     encoding/xml as-is, that fails on the first byte pair: "XML syntax error
//     on line 1: invalid UTF-8".
//
//  2. TALLYPRIME EMITS U+0004 IN ORDINARY FIELD VALUES. It uses it as an
//     internal marker on "not applicable" style values, so the file is littered
//     with things like <CSTFORMISSUETYPE>&#4; Not Applicable</CSTFORMISSUETYPE>.
//     A numeric reference to a C0 control is FORBIDDEN by XML 1.0 — not merely
//     discouraged — and Go rejects it: "illegal character code U+0004". The two
//     files tested against carried 32,464 of them.
//
// Neither is a corner case; they are simply what the program whose output this
// parser exists to read produces. A stripped control is dropped, not replaced,
// so "&#4; Not Applicable" reads as " Not Applicable" and callers keep trimming.
func NewTallyDecodeReader(r io.Reader) io.Reader {
	return newControlStripper(newTranscodingReader(r))
}

// newTranscodingReader sniffs the byte-order mark and returns a reader that
// yields UTF-8. A UTF-8 BOM is dropped; UTF-16 in either byte order is decoded.
// With no BOM the bytes are passed through untouched — a UTF-8 file, which is
// what every hand-authored envelope is.
func newTranscodingReader(r io.Reader) io.Reader {
	br := bufio.NewReaderSize(r, 64<<10)
	prefix, err := br.Peek(3)
	if err != nil && len(prefix) < 2 {
		return br // too short to sniff; let the decoder produce the error
	}
	switch {
	case len(prefix) >= 3 && prefix[0] == 0xEF && prefix[1] == 0xBB && prefix[2] == 0xBF:
		_, _ = br.Discard(3)
		return br
	case prefix[0] == 0xFF && prefix[1] == 0xFE:
		return transform.NewReader(br,
			unicode.UTF16(unicode.LittleEndian, unicode.ExpectBOM).NewDecoder())
	case prefix[0] == 0xFE && prefix[1] == 0xFF:
		return transform.NewReader(br,
			unicode.UTF16(unicode.BigEndian, unicode.ExpectBOM).NewDecoder())
	case prefix[0] == 0x00:
		// UTF-16BE with no BOM: the high byte of '<' is zero.
		return transform.NewReader(br,
			unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM).NewDecoder())
	case len(prefix) >= 2 && prefix[1] == 0x00:
		// UTF-16LE with no BOM.
		return transform.NewReader(br,
			unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewDecoder())
	default:
		return br
	}
}

// controlStripper removes characters XML 1.0 forbids from a UTF-8 stream, both
// as raw bytes and as numeric character references (&#4; / &#x4;).
type controlStripper struct {
	br  *bufio.Reader
	out []byte // bytes produced but not yet handed to the caller
}

func newControlStripper(r io.Reader) io.Reader {
	if br, ok := r.(*bufio.Reader); ok {
		return &controlStripper{br: br}
	}
	return &controlStripper{br: bufio.NewReaderSize(r, 64<<10)}
}

// legalXMLChar reports whether a rune may appear in an XML 1.0 document.
func legalXMLChar(r rune) bool {
	switch {
	case r == 0x09, r == 0x0A, r == 0x0D:
		return true
	case r >= 0x20 && r <= 0xD7FF:
		return true
	case r >= 0xE000 && r <= 0xFFFD:
		return true
	case r >= 0x10000 && r <= 0x10FFFF:
		return true
	default:
		return false
	}
}

func (c *controlStripper) Read(p []byte) (int, error) {
	for len(c.out) == 0 {
		if err := c.fill(); err != nil {
			if len(c.out) == 0 {
				return 0, err
			}
			break
		}
	}
	n := copy(p, c.out)
	c.out = c.out[n:]
	return n, nil
}

// fill appends at least one byte to c.out, or returns the reader's error. It
// works a byte at a time because the only bytes it may drop are ASCII controls
// and a numeric reference — neither can occur inside a multi-byte UTF-8
// sequence, whose continuation bytes are all >= 0x80.
func (c *controlStripper) fill() error {
	for {
		b, err := c.br.ReadByte()
		if err != nil {
			return err
		}
		if b < 0x20 && b != '\t' && b != '\n' && b != '\r' {
			continue // raw C0 control: drop
		}
		if b != '&' {
			c.out = append(c.out, b)
			return nil
		}
		// '&': it may open a numeric character reference. Peek far enough to see
		// the whole of a plausible one (&#x10FFFF; is 10 bytes).
		peek, _ := c.br.Peek(10)
		if n, r, ok := scanNumericRef(peek); ok {
			_, _ = c.br.Discard(n)
			if !legalXMLChar(r) {
				continue // e.g. &#4; — drop the reference entirely
			}
			c.out = append(c.out, '&')
			c.out = append(c.out, peek[:n]...)
			return nil
		}
		c.out = append(c.out, b)
		return nil
	}
}

// scanNumericRef reads a numeric character reference from the bytes FOLLOWING an
// '&'. It returns how many bytes the reference occupies (through the ';'), the
// rune it denotes, and whether one was found at all.
func scanNumericRef(b []byte) (int, rune, bool) {
	if len(b) < 3 || b[0] != '#' {
		return 0, 0, false
	}
	i, base := 1, 10
	if b[1] == 'x' || b[1] == 'X' {
		i, base = 2, 16
	}
	start := i
	for i < len(b) && b[i] != ';' {
		i++
	}
	if i == start || i >= len(b) || b[i] != ';' {
		return 0, 0, false
	}
	v, err := strconv.ParseInt(string(b[start:i]), base, 64)
	if err != nil {
		return 0, 0, false
	}
	return i + 1, rune(v), true
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
